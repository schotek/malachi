// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/ConversationChatViewController.swift;
// GTK: ui/internal/window/conversation_chat.go (conversationChat) and
// conversation_chat_layout.go. The chat layout of the conversation in the
// reading pane (docs/plans/chat-view.md, Conversation.ChatRows): the
// conversation as a messaging app shows it, oldest first from the top, open
// at the end. The others' messages are bubbles on the left, the user's own
// (and the user's replies in Sent, whichever identity sent them) on the
// right, the drafts last on the right; an issue's events and the row of
// older members left out are centred rows; a Jira conversation has its
// issue card on top across the column (ConversationIssueBox). The column is
// the timeline's (no wider than 900, the same insets), without its gutter
// and line: the avatars stand beside the bubbles (ConversationChatRow).
//
// A bubble is plain text (ConversationBubbleView): the message's text as the
// daemon derived it, without its quoted history (Conversation.BubbleBodyFor),
// the summary's snippet dimmed until the body is there. With the preference
// conversation-chat-html (SettingsStore.ConversationChatHtml,
// Conversation.BubbleRendersHtml) a body with HTML not withheld shows the
// timeline card's document instead, in a CardWebView from this view's pool
// (IChatWebHost), for the bubbles the timeline's rule makes live
// (ConversationLayout.LiveCards with the HTML bubbles marked, at most
// MaxLiveWebViews); the others show their text, and the setting is observed,
// so flipping it renders every bubble anew. A hidden chat lets go of every
// web view. The bodies are asked for only for the bubbles near the viewport
// (LiveCards over the rows' spans, two heights of the view), from the same
// controller and cache of bodies as the timeline, so switching layouts asks
// thread.get nothing. A double
// tap anywhere in a bubble opens the message in its own window, a draft in
// the compose window (MessageActionRouter.OpenMessage, EditThreadDraft,
// once the second press is released, AfterRelease); nothing else is
// interactive, and nothing in the rows takes the keyboard.
//
// The chat opens at its end. While the viewport shows the end (and until
// the user scrolls away from it: the wheel, a press, a touch), the end
// stays in view as bodies arrive and members come; otherwise the row under
// the viewport's top keeps its place. A change of the pane's size keeps the
// end when it showed before, else the row read (ConversationLayout.
// ChatResizeFor). Space and Shift+Space from the list page through it
// (PageBy, through the host). The host (ConversationView) hands this view
// the controller's changes (Changed, EntryLoaded) and trims the controller's
// bodies with the near set this view reports (NearChanged) only while it is
// the layout shown.
//
// Windows: the ScrollViewer's own anchoring (every row an anchor
// candidate) follows the same state as those explicit moves, so the two
// never pull different ways: its anchor is the viewport's bottom
// (VerticalAnchorRatio 1) while the end is pinned or shows, and so a chat
// at its end stays there through a reflow nothing here sees coming (a
// bubble's text wrapping anew); the viewport's top (0) once the user
// scrolled away from it, the row Preserving and Restore keep in place.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>The chat layout of the conversation page of the main window's reading pane.</summary>
internal sealed partial class ConversationChatView : UserControl, IChatWebHost
{
    // convIdleWebViews: how many web views the pool keeps while no
    // conversation shows (the timeline's).
    private const int IdleWebViews = 2;

    // libadwaita's body text, which the text-zoom setting scales (as the
    // timeline's plain-text bodies).
    private const double BaseBodyFontSize = 14;

    // How many layouts UpdateNear waits for the rows to be laid out.
    private const int LayoutTries = 30;

    // The children of the column before the rows: the issue card.
    private const int HeadViews = 1;

    private readonly ConversationView host;
    private readonly ReaderServices services;
    private readonly ConversationController ctrl;
    private readonly ConversationIssueBox issue;
    private readonly Dictionary<MessageId, ConversationChatRow> bubbles = [];
    private readonly Dictionary<MessageId, ConversationChatCenterRow> events = [];
    private readonly Dictionary<DraftId, ConversationChatRow> draftRows = [];
    private readonly ConversationChatCenterRow truncatedRow;
    private readonly List<SettingsChangeToken> tokens = [];
    private readonly List<CardWebView> webPool = [];

    // The body each bubble rendered last, for a reload of its document when
    // its pictures were downloaded (ConversationLayout.PicturesArrived).
    private readonly Dictionary<MessageId, MessageBodyResult> sources = [];

    // The bubbles the user expanded while Shorten My/Others' Messages in Chat shorten
    // them; forgotten with the conversation (RemoveAll), as the timeline's
    // folds, kept across a switch of the layout.
    private readonly ConversationExpanded expanded = new();

    // The rows in the order shown, under the issue card.
    private List<FrameworkElement> rowViews = [];
    private bool compactDates;
    private bool shown;

    // The end of the chat stays in view until the user scrolls.
    private bool pinnedEnd;

    // Where the user was, for a change of the pane's size: the row and how
    // far below its top the viewport's top was, and whether the end showed.
    private (FrameworkElement View, double Offset)? lastAnchor;
    private bool lastAtEnd;

    // A change of the viewport's size waits for its layout: the near update
    // meanwhile keeps the position it is to restore.
    private bool resizing;
    private int anchorDepth;
    private bool nearScheduled;
    private int layoutTries;
    private bool waitingForLayout;

    /// <summary>The chat over <paramref name="ctrl"/> in <paramref name="host"/>, with the reader's services.</summary>
    public ConversationChatView(ConversationView host, ReaderServices services, ConversationController ctrl)
    {
        ArgumentNullException.ThrowIfNull(host);
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(ctrl);
        this.host = host;
        this.services = services;
        this.ctrl = ctrl;
        InitializeComponent();
        AfterRelease = new AfterRelease(this);
        issue = new ConversationIssueBox(IssueCard, services, ctrl, host.OpenIssue);
        truncatedRow = new ConversationChatCenterRow(Look);

        Scroller.ViewChanged += (_, _) => ScheduleNear();
        Scroller.SizeChanged += OnScrollerSizeChanged;
        Column.SizeChanged += (_, _) => ScheduleNear();
        // The user scrolls (the wheel, a press on the scroll bar or a row, a
        // touch): the end is no longer pinned.
        Scroller.AddHandler(PointerWheelChangedEvent, new PointerEventHandler((_, _) => Unpin()), handledEventsToo: true);
        Scroller.AddHandler(PointerPressedEvent, new PointerEventHandler((_, _) => Unpin()), handledEventsToo: true);
        Scroller.DirectManipulationStarted += (_, _) => Unpin();

        var settings = services.State.Settings;
        tokens.Add(settings.OnChange(SettingsKey.TextZoom, () =>
        {
            ApplyBodyFont();
            ApplyZoom();
        }));
        tokens.Add(settings.OnChange(SettingsKey.MonospacePlainText, ApplyBodyFont));
        tokens.Add(settings.OnChange(SettingsKey.MonochromeAvatars, ApplyAvatars));
        tokens.Add(settings.OnChange(SettingsKey.ConversationChatHtml, ApplyHtmlSetting));
        tokens.Add(settings.OnChange(SettingsKey.ConversationChatTruncateMine, ApplyShortened));
        tokens.Add(settings.OnChange(SettingsKey.ConversationChatTruncateOthers, ApplyShortened));
    }

    /// <summary>The members whose bodies the bubbles near the viewport keep (the host trims the controller with them while the chat shows).</summary>
    public event EventHandler<IReadOnlySet<MessageId>>? NearChanged;

    /// <summary>What a double tap on a bubble waits for before it opens a window: the release of its second press.</summary>
    internal AfterRelease AfterRelease { get; }

    // The bubbles' text size (the text-zoom setting).
    private double BodyFontSize => BaseBodyFontSize * services.State.Settings.TextZoom / 100.0;

    // The monospaced family a plain-text body shows in with its setting.
    private static FontFamily? MonospaceFamily =>
        Application.Current.Resources.TryGetValue("MonospaceFontFamily", out var font) && font is FontFamily mono ? mono : null;

    // The column's inner width (ConversationLayout.ChatColumn): the clamp
    // less its side insets.
    private double ColumnWidth => L.ChatColumn(Scroller.ActualWidth);

    // The preference conversation-chat-html: bubbles with HTML render it.
    private bool HtmlSetting => services.State.Settings.ConversationChatHtml;

    // The preferences conversation-chat-truncate-mine and -others: whether
    // a bubble of that side (a draft is the user's own) shows a preview,
    // unless the user expanded it.
    private bool Shortened(bool mine, bool expandedOne) =>
        Core.Model.Conversation.BubbleShortened(
            services.State.Settings.ConversationChatTruncateMine, services.State.Settings.ConversationChatTruncateOthers, mine, expandedOne);

    /// <summary>
    /// A change of the controller, from the host. While hidden the rows are
    /// only taken out when the conversation goes; <see cref="SetShown"/>
    /// builds the rest.
    /// </summary>
    public void Changed(ConversationController.Change change)
    {
        switch (change)
        {
            case ConversationController.Change.Loading:
                RemoveAll();
                break;
            case ConversationController.Change.Opened:
                if (shown)
                {
                    Open();
                }
                else
                {
                    RemoveAll();
                }
                break;
            case ConversationController.Change.Updated:
                if (shown)
                {
                    Update();
                }
                break;
            case ConversationController.Change.Cleared:
                RemoveAll();
                // The pane shows something else: only a few views wait for
                // the next conversation, the others' processes end.
                TrimWebPool(IdleWebViews);
                break;
        }
    }

    /// <summary>A bubble's entry has news (from the host while the chat shows): its text and its paperclip.</summary>
    public void EntryLoaded(MessageId id, LoadedMessage lm)
    {
        if (bubbles.TryGetValue(id, out var row))
        {
            Preserving(() => Render(row, lm));
        }
    }

    /// <summary>
    /// The host shows this layout (<paramref name="on"/>) or the timeline.
    /// Shown, the chat catches up with the controller (the rows reconciled
    /// with the model, every bubble rendered from the bodies held) and opens
    /// at its end.
    /// </summary>
    public void SetShown(bool on)
    {
        if (on == shown)
        {
            return;
        }
        shown = on;
        if (on)
        {
            Open();
            return;
        }
        // Hidden: every bubble lets go of its web view (they show their text).
        foreach (var row in bubbles.Values)
        {
            row.Bubble.SetLive(false);
        }
        TrimWebPool(IdleWebViews);
        host.Hover("");
    }

    /// <summary>The page of the pane is shown again: the bubbles near the viewport are looked at anew.</summary>
    public void PageShown() => ScheduleNear();

    /// <summary>Whether member <paramref name="id"/> has a bubble here (macOS <c>displays</c>).</summary>
    internal bool Displays(MessageId id) => bubbles.ContainsKey(id);

    /// <summary>The refreshed issue of a transition (through the host).</summary>
    public void ApplyIssue(AccountId account, IssueInfo info) => issue.Apply(account, info);

    /// <summary>A transition started or ended on an issue (through the host).</summary>
    public void IssueBusyChanged(AccountId account, string key) => issue.BusyChanged(account, key);

    /// <summary>win.change-status: the Change Status menu of the chat's issue card; false when there is none.</summary>
    public bool OpenStatusMenu() => issue.OpenStatusMenu();

    /// <summary>
    /// A hiding quick-reply bar had the keyboard: this layout's scroller
    /// takes it (<see cref="ConversationView.FocusScroller"/>); false when it
    /// could not.
    /// </summary>
    internal bool TakeKeyboard() => ConversationView.FocusScroller(Scroller);

    /// <summary>Space (or Shift+Space, <paramref name="up"/>) from the list: one page of the chat down (up); true when the key was used.</summary>
    public bool PageBy(bool up)
    {
        if (ctrl.Model is null)
        {
            return false;
        }
        var page = Scroller.ViewportHeight;
        var top = L.PageTop(Scroller.VerticalOffset, up, page, Scroller.ExtentHeight, page * 0.1);
        Unpin();
        Scroller.ChangeView(null, top, null);
        return true;
    }

    /// <summary>The chat's views go for good (the window closes).</summary>
    public void Close()
    {
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        if (waitingForLayout)
        {
            Column.LayoutUpdated -= OnceLaidOut;
            waitingForLayout = false;
        }
        RemoveAll();
        TrimWebPool(0);
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new ConversationGroupPeer(this, nameof(ConversationChatView), AutomationControlType.Pane);

    /// <summary>The timeline's takeWebView: a view from the pool, its zoom brought up to date, or a new one.</summary>
    public CardWebView TakeWebView()
    {
        var zoom = services.State.Settings.TextZoom;
        if (webPool.Count > 0)
        {
            var pooled = webPool[^1];
            webPool.RemoveAt(webPool.Count - 1);
            pooled.Zoom = zoom;
            return pooled;
        }
        // A chat bubble's view: transparent, the chat's document.
        var web = new CardWebView(chat: true) { Zoom = zoom };
        var cache = services.Cache;
        web.Parts = (part, token) => cache.FetchPartAsync(part.AccountId, part.MessageId, part.PartId, token);
        return web;
    }

    /// <summary>The timeline's returnWebView: reset for the next bubble, or released when the pool is full.</summary>
    public void ReturnWebView(CardWebView web)
    {
        ArgumentNullException.ThrowIfNull(web);
        if (webPool.Count >= L.MaxLiveWebViews)
        {
            web.Release();
            return;
        }
        web.Reset();
        webPool.Add(web);
    }

    /// <summary>A link activated in a bubble's document: the cards' link path (the confirmation, the browser).</summary>
    public void OpenLink(ActivatedLink link, IReadOnlyList<Link> links) => _ = services.Links.OpenAsync(link, links, host.HostWindow);

    /// <summary>The link under the pointer of a bubble's document ("" hides it), in the host's status.</summary>
    public void Hover(string link) => host.Hover(link);

    /// <summary>The timeline's scrollBy: the wheel a bubble whose document fits handed on, in the wheel's units (120 a step).</summary>
    public void ScrollBy(double wheelDelta)
    {
        Unpin();
        var step = Math.Pow(Math.Max(Scroller.ViewportHeight, 1), 2.0 / 3.0);
        var to = Scroller.VerticalOffset + (wheelDelta / 120.0 * step);
        Scroller.ChangeView(null, Math.Clamp(to, 0, Math.Max(0, Scroller.ScrollableHeight)), null);
    }

    /// <summary>A bubble's height changes: the end or the row read keeps its place.</summary>
    public void Reflow(FrameworkElement bubble, Action change)
    {
        ArgumentNullException.ThrowIfNull(change);
        Preserving(change);
    }

    /// <summary>A bubble's want of a web view changed: the live ones are decided anew.</summary>
    public void LiveChanged() => ScheduleNear();

    // The conversation shown anew (a new one, or this layout shown): the rows
    // as the model has them, the viewport at the end, the bubbles near it
    // asked for their bodies.
    private void Open()
    {
        if (ctrl.Model is not { } m)
        {
            RemoveAll();
            return;
        }
        // The layout was just shown: its width is the pane's once laid out.
        Scroller.UpdateLayout();
        compactDates = L.ChatCompactDates(ColumnWidth);
        anchorDepth++;
        Apply(m);
        foreach (var (id, row) in bubbles)
        {
            Render(row, ctrl.Loaded.GetValueOrDefault(id));
        }
        anchorDepth--;
        Scroller.UpdateLayout();
        pinnedEnd = true;
        Scroller.VerticalAnchorRatio = 1;
        ScrollToEnd();
        UpdateNear();
    }

    // The shown conversation changed: the rows are reconciled by id, and the
    // end stays in view when it was in view, else the row the user reads
    // keeps its place.
    private void Update()
    {
        if (ctrl.Model is not { } m)
        {
            return;
        }
        Preserving(() => Apply(m));
    }

    // Puts the rows of m (Conversation.ChatRows) into the column, reusing
    // the views of the members, events and drafts shown already; a member
    // whose side changed gets a new row.
    private void Apply(ConversationModel m)
    {
        issue.Show(m);
        var rows = Core.Model.Conversation.ChatRows(m, ctrl.Drafts);
        var monochrome = services.State.Settings.MonochromeAvatars;
        var monospaceDrafts = services.State.Settings.MonospacePlainText;
        var column = ColumnWidth;
        var desired = new List<FrameworkElement>(rows.Count);
        var bubbleIds = new HashSet<MessageId>();
        var eventIds = new HashSet<MessageId>();
        var draftIds = new HashSet<DraftId>();
        foreach (var row in rows)
        {
            switch (row.Kind)
            {
                case ChatRowKind.Truncated:
                    truncatedRow.ShowText(row.Item?.Text ?? "");
                    truncatedRow.SetColumnWidth(column);
                    desired.Add(truncatedRow);
                    break;
                case ChatRowKind.Bubble:
                    {
                        if (row.Item is not { Message: { } s } item)
                        {
                            continue;
                        }
                        var id = s.Id;
                        bubbleIds.Add(id);
                        var right = row.Side == ChatSide.Right;
                        if (!bubbles.TryGetValue(id, out var view) || view.Right != right)
                        {
                            if (view is not null)
                            {
                                view.Bubble.SetLive(false);
                                Detach(view);
                            }
                            view = MakeRow(id, null, right);
                            view.Bubble.Activate = () => OpenMember(id);
                            view.Bubble.Expand = () => ExpandMember(id);
                            bubbles[id] = view;
                        }
                        view.Bubble.SetShortened(Shortened(view.Right, expanded.Is(id)));
                        view.Bubble.CompactDate = compactDates;
                        view.Bubble.ShowMessage(item, Attachments(id));
                        if (!view.Bubble.ShowsText)
                        {
                            var lm = ctrl.Loaded.GetValueOrDefault(id);
                            view.Bubble.ShowBody(Core.Model.Conversation.BubbleBodyFor(lm, s.Snippet, HtmlSetting), Monospace(lm), PicturesArrived(id, lm));
                        }
                        view.ShowAvatar(item.Sender, monochrome, ConversationRow.PhotoSenderOf(item));
                        view.SetColumnWidth(column);
                        desired.Add(view);
                        break;
                    }
                case ChatRowKind.Event:
                    {
                        if (row.Item is not { Message: { } s } item)
                        {
                            continue;
                        }
                        eventIds.Add(s.Id);
                        if (!events.TryGetValue(s.Id, out var view))
                        {
                            view = new ConversationChatCenterRow(Look);
                            events[s.Id] = view;
                        }
                        view.CompactDate = compactDates;
                        view.ShowEvent(item);
                        view.SetColumnWidth(column);
                        desired.Add(view);
                        break;
                    }
                case ChatRowKind.Draft:
                    {
                        if (row.Draft is not { } card || card.Draft.Id is not { } id)
                        {
                            continue;
                        }
                        draftIds.Add(id);
                        if (!draftRows.TryGetValue(id, out var view))
                        {
                            view = MakeRow(null, id, right: true);
                            draftRows[id] = view;
                        }
                        // A draft is always shortened; a tap opens it.
                        view.Bubble.SetShortened(true);
                        var draft = card.Draft;
                        view.Bubble.Activate = () => AfterRelease.Run(() => services.Router.EditThreadDraft(draft));
                        view.Bubble.CompactDate = compactDates;
                        view.Bubble.ShowDraft(card, monospaceDrafts);
                        view.ShowAvatar(card.Sender, monochrome);
                        view.SetColumnWidth(column);
                        desired.Add(view);
                        break;
                    }
            }
        }
        foreach (var id in bubbles.Keys.Where(id => !bubbleIds.Contains(id)).ToList())
        {
            bubbles[id].Bubble.SetLive(false);
            Detach(bubbles[id]);
            bubbles.Remove(id);
            sources.Remove(id);
        }
        foreach (var id in events.Keys.Where(id => !eventIds.Contains(id)).ToList())
        {
            Detach(events[id]);
            events.Remove(id);
        }
        foreach (var id in draftRows.Keys.Where(id => !draftIds.Contains(id)).ToList())
        {
            Detach(draftRows[id]);
            draftRows.Remove(id);
        }
        if (!desired.Contains(truncatedRow))
        {
            Detach(truncatedRow);
        }
        // The column after the issue card, in the order shown.
        var children = Column.Children;
        for (var i = 0; i < desired.Count; i++)
        {
            var v = desired[i];
            var place = i + HeadViews;
            var now = children.IndexOf(v);
            if (now == place)
            {
                continue;
            }
            if (now >= 0)
            {
                children.RemoveAt(now);
            }
            else
            {
                Scroller.RegisterAnchorCandidate(v);
            }
            children.Insert(Math.Min(place, children.Count), v);
        }
        rowViews = desired;
    }

    private ConversationChatRow MakeRow(MessageId? id, DraftId? draft, bool right)
    {
        var bubble = new ConversationBubbleView(right, Look, BodyFontSize, MonospaceFamily) { CompactDate = compactDates };
        if (id is not null)
        {
            // A message's bubble may render its HTML; a draft's never does.
            bubble.WebHost = this;
        }
        return new ConversationChatRow(id, draft, right, bubble);
    }

    // A double tap on member id's bubble: the message in its own window (a
    // draft of the Drafts folder in the compose window), as a double click
    // on its row in the list, once the second press is released.
    private void OpenMember(MessageId id)
    {
        if (ctrl.Member(id) is { } s)
        {
            AfterRelease.Run(() => services.Router.OpenMessage(s));
        }
    }

    // The bubble of a member shows what its entry lm holds: the text
    // (Conversation.BubbleBodyFor) and its attachments pill.
    private void Render(ConversationChatRow row, LoadedMessage? lm)
    {
        if (row.Id is not { } id || ctrl.Model is not { } m)
        {
            return;
        }
        var i = m.Index(id);
        if (i < 0)
        {
            return;
        }
        var item = m.Items[i];
        row.Bubble.ShowMessage(item, Attachments(id));
        row.Bubble.ShowBody(Core.Model.Conversation.BubbleBodyFor(lm, item.Message?.Snippet ?? "", HtmlSetting), Monospace(lm), PicturesArrived(id, lm));
    }

    // Whether the pictures of the body member id's bubble reads were
    // downloaded since it rendered last (its document loads again); the body
    // is remembered for the next time.
    private bool PicturesArrived(MessageId id, LoadedMessage? lm)
    {
        if (lm is null || Core.Model.Conversation.ChatBody(lm, lm.QuotedShown) is not { } now)
        {
            return false;
        }
        var arrived = sources.TryGetValue(id, out var before) && L.PicturesArrived(before, now);
        sources[id] = now;
        return arrived;
    }

    // The attachments pill of member id (Conversation.BubbleAttachmentBadge):
    // the paperclip from the summary at once, the number of the timeline's
    // chips once message.get told it.
    private BubbleAttachments Attachments(MessageId id) =>
        Core.Model.Conversation.BubbleAttachmentBadge(ctrl.Member(id), ctrl.Loaded.GetValueOrDefault(id));

    // Whether a bubble whose entry is lm shows its text monospaced
    // (Conversation.BubbleMonospace: the setting, a plain-text body).
    private bool Monospace(LoadedMessage? lm) =>
        Core.Model.Conversation.BubbleMonospace(lm, services.State.Settings.MonospacePlainText);

    private void Detach(FrameworkElement v)
    {
        var i = Column.Children.IndexOf(v);
        if (i >= 0)
        {
            Scroller.UnregisterAnchorCandidate(v);
            Column.Children.RemoveAt(i);
        }
    }

    // Takes everything out (another conversation, or none).
    private void RemoveAll()
    {
        foreach (var row in bubbles.Values)
        {
            row.Bubble.SetLive(false);
        }
        foreach (var v in rowViews)
        {
            Detach(v);
        }
        foreach (var v in bubbles.Values.Concat(draftRows.Values).Cast<FrameworkElement>().Concat(events.Values))
        {
            Detach(v);
        }
        Detach(truncatedRow);
        bubbles.Clear();
        sources.Clear();
        expanded.Reset();
        events.Clear();
        draftRows.Clear();
        rowViews = [];
        pinnedEnd = false;
        lastAnchor = null;
        lastAtEnd = false;
        layoutTries = 0;
        issue.Clear();
    }

    // Runs change (which alters heights) and keeps the end in view when it
    // was (or until the user scrolls), else the row the user reads in its
    // place. Nested changes are laid out once, by the outermost.
    private void Preserving(Action change)
    {
        if (anchorDepth > 0 || !shown)
        {
            change();
            return;
        }
        var atEnd = pinnedEnd || IsAtEnd();
        var anchor = atEnd ? null : CurrentAnchor();
        anchorDepth++;
        change();
        anchorDepth--;
        Scroller.UpdateLayout();
        if (atEnd)
        {
            ScrollToEnd();
        }
        else
        {
            Restore(anchor);
        }
        ScheduleNear();
    }

    // The user scrolls (the wheel, a press, a touch, Space): the end is no
    // longer pinned, and the ScrollViewer anchors at the viewport's top, as
    // Preserving does, until UpdateNear finds the end in view again (a
    // press that scrolled nothing: at once).
    private void Unpin()
    {
        pinnedEnd = false;
        Scroller.VerticalAnchorRatio = 0;
        ScheduleNear();
    }

    private bool IsAtEnd() => L.ChatAtEnd(Scroller.VerticalOffset, Scroller.ViewportHeight, Scroller.ExtentHeight);

    private void ScrollToEnd()
    {
        var top = L.ChatEndTop(Scroller.ViewportHeight, Scroller.ExtentHeight);
        if (Math.Abs(top - Scroller.VerticalOffset) >= 0.5)
        {
            Scroller.ChangeView(null, top, null, disableAnimation: true);
        }
    }

    // The first row (the issue card first) reaching below the viewport's
    // top, and how far below its top the viewport's top is.
    private (FrameworkElement View, double Offset)? CurrentAnchor()
    {
        var top = Scroller.VerticalOffset;
        foreach (var v in rowViews.Prepend(IssueCard))
        {
            if (v.Visibility != Visibility.Visible || SpanOf(v) is not { } span)
            {
                continue;
            }
            if (span.Max > top + 0.5)
            {
                return (v, top - span.Min);
            }
        }
        return null;
    }

    private void Restore((FrameworkElement View, double Offset)? anchor)
    {
        if (anchor is not { } a || SpanOf(a.View) is not { } span)
        {
            return;
        }
        var top = L.AnchoredTop(span.Min, a.Offset, Scroller.ExtentHeight, Scroller.ViewportHeight);
        if (Math.Abs(top - Scroller.VerticalOffset) >= 0.5)
        {
            Scroller.ChangeView(null, top, null, disableAnimation: true);
        }
    }

    // A row's stretch of the column's document; null while it is not laid
    // out yet, so nothing is decided from a place it does not have.
    private L.Span? SpanOf(FrameworkElement e)
    {
        if (e.Parent is null && e.XamlRoot is null)
        {
            return null;
        }
        if (e.ActualHeight <= 0 && e.ActualWidth <= 0)
        {
            return null;
        }
        try
        {
            var top = e.TransformToVisual(Document).TransformPoint(new Point(0, 0)).Y;
            return L.Span.Of(top, top + e.ActualHeight);
        }
        catch (ArgumentException)
        {
            return null; // not in the tree any more
        }
    }

    // The pane's size changed (ConversationLayout.ChatResizeFor): a chat that
    // showed its end (or had it pinned) before shows it after, whether the
    // pane got narrower, wider, shorter or taller; otherwise the row the
    // user read comes back once the bubbles reflowed. Only a change of the
    // width reflows (every row's widest bubble) and may switch the short
    // dates. The position is the one measured before the change (lastAtEnd,
    // lastAnchor), not what the clipped viewport says after it.
    private void OnScrollerSizeChanged(object sender, SizeChangedEventArgs e)
    {
        if (!shown || e.NewSize.Width <= 0 || e.NewSize.Height <= 0)
        {
            return;
        }
        if (L.ChatResizeFor((e.PreviousSize.Width, e.PreviousSize.Height), (e.NewSize.Width, e.NewSize.Height), pinnedEnd, lastAtEnd) is not { } resize)
        {
            return;
        }
        if (resize.Width)
        {
            var column = L.ChatColumn(e.NewSize.Width);
            var compact = L.ChatCompactDates(column);
            var compactChanged = compact != compactDates;
            compactDates = compact;
            foreach (var row in bubbles.Values.Concat(draftRows.Values))
            {
                if (compactChanged)
                {
                    row.Bubble.CompactDate = compact;
                }
                row.SetColumnWidth(column);
            }
            foreach (var ev in events.Values)
            {
                if (compactChanged)
                {
                    ev.CompactDate = compact;
                }
                ev.SetColumnWidth(column);
            }
            truncatedRow.SetColumnWidth(column);
        }
        var atEnd = resize.ToEnd;
        var anchor = lastAnchor;
        resizing = true;
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            resizing = false;
            if (!shown)
            {
                return;
            }
            Scroller.UpdateLayout();
            if (atEnd)
            {
                ScrollToEnd();
            }
            else
            {
                Restore(anchor);
            }
            ScheduleNear();
        });
    }

    private void ScheduleNear()
    {
        if (nearScheduled)
        {
            return;
        }
        nearScheduled = true;
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            nearScheduled = false;
            UpdateNear();
        });
    }

    // Asks for the bodies of the bubbles near the viewport (within two of its
    // heights, ConversationLayout.LiveCards without web views) and tells the
    // host which they are; a message with attachments with message.get as
    // well, as the timeline's card asks for its chips, so that its pill gets
    // its number. Only while this layout is shown and the page is.
    private void UpdateNear()
    {
        if (!shown || ctrl.Model is null || !host.PageVisible || XamlRoot is null)
        {
            return;
        }
        var visible = L.Span.Of(Scroller.VerticalOffset, Scroller.VerticalOffset + Scroller.ViewportHeight);
        var ids = new List<MessageId>();
        var placed = new List<ConversationChatRow>();
        var frames = new List<L.Span>();
        var html = new List<bool>();
        var unplaced = false;
        foreach (var v in rowViews)
        {
            if (v is not ConversationChatRow { Id: { } id } row)
            {
                continue;
            }
            if (SpanOf(row) is not { } span)
            {
                unplaced = true;
                continue;
            }
            ids.Add(id);
            placed.Add(row);
            frames.Add(span);
            html.Add(row.Bubble.WantsWebView);
        }
        // The timeline's rule: web views for the nearest HTML bubbles, at
        // most its cap; a bubble far away or beyond it shows its text.
        var live = L.LiveCards(frames, html, visible, L.LiveScreens, L.MaxLiveWebViews);
        var changes = new List<(ConversationBubbleView Bubble, bool On)>();
        for (var i = 0; i < placed.Count; i++)
        {
            var on = live.Web.Contains(i);
            if (placed[i].Bubble.IsLive != on)
            {
                changes.Add((placed[i].Bubble, on));
            }
        }
        if (changes.Count > 0)
        {
            Preserving(() =>
            {
                foreach (var (bubble, on) in changes)
                {
                    bubble.SetLive(on);
                }
            });
        }
        var near = new HashSet<MessageId>();
        foreach (var i in live.Near.Order())
        {
            var id = ids[i];
            near.Add(id);
            ctrl.NeedsBody(id, details: ctrl.Member(id)?.HasAttachments == true);
        }
        NearChanged?.Invoke(this, near);
        if (!resizing)
        {
            lastAtEnd = IsAtEnd();
            lastAnchor = CurrentAnchor();
            // The ScrollViewer's anchor follows: the bottom while the end
            // is pinned or shows, else the top, as Preserving keeps it.
            Scroller.VerticalAnchorRatio = pinnedEnd || lastAtEnd ? 1 : 0;
        }
        if (unplaced)
        {
            AfterLayout();
        }
        else
        {
            layoutTries = 0;
        }
    }

    // UpdateNear again after the next layout (rows that had no size yet were
    // skipped), a bounded number of times.
    private void AfterLayout()
    {
        if (waitingForLayout || layoutTries >= LayoutTries)
        {
            return;
        }
        layoutTries++;
        waitingForLayout = true;
        Column.LayoutUpdated += OnceLaidOut;
    }

    private void OnceLaidOut(object? sender, object e)
    {
        Column.LayoutUpdated -= OnceLaidOut;
        waitingForLayout = false;
        ScheduleNear();
    }

    // The text zoom or the monospace setting changed: every bubble's font,
    // and the font each text shows in (a bubble whose entry was let go keeps
    // its choice while the setting is on).
    private void ApplyBodyFont()
    {
        var size = BodyFontSize;
        var family = MonospaceFamily;
        var setting = services.State.Settings.MonospacePlainText;
        Preserving(() =>
        {
            foreach (var (id, row) in bubbles)
            {
                row.Bubble.SetFonts(size, family);
                var lm = ctrl.Loaded.GetValueOrDefault(id);
                row.Bubble.SetMonospace(lm is not null ? Monospace(lm) : setting && row.Bubble.IsMonospace);
            }
            foreach (var row in draftRows.Values)
            {
                row.Bubble.SetFonts(size, family);
                row.Bubble.SetMonospace(setting);
            }
        });
    }

    // The text-zoom setting: the documents of the bubbles' web views scaled.
    private void ApplyZoom()
    {
        var zoom = services.State.Settings.TextZoom;
        foreach (var row in bubbles.Values)
        {
            row.Bubble.SetZoom(zoom);
        }
    }

    // The preference conversation-chat-html changed: every bubble rendered
    // anew from the body held (on: HTML bubbles, which UpdateNear gives web
    // views; off: the text, every web view let go). A bubble whose entry was
    // let go reads its text when the preference goes off, and its HTML once
    // its body is fetched again (it is near) when it goes on.
    private void ApplyHtmlSetting()
    {
        var on = HtmlSetting;
        Preserving(() =>
        {
            foreach (var (id, row) in bubbles)
            {
                if (ctrl.Loaded.GetValueOrDefault(id) is { } lm)
                {
                    Render(row, lm);
                }
                else if (!on)
                {
                    row.Bubble.DropHtml();
                }
            }
        });
        ScheduleNear();
    }

    // A tap on a shortened bubble of member id: recorded, and the bubble
    // shows the whole message; nothing collapses back (a draft's tap opens
    // the draft instead, ConversationBubbleView.IsDraft).
    private void ExpandMember(MessageId id)
    {
        expanded.Expand(id);
        if (bubbles.TryGetValue(id, out var row))
        {
            Preserving(() => row.Bubble.SetShortened(false));
        }
    }

    // The preferences conversation-chat-truncate-mine or -others changed:
    // every message bubble shortened or whole anew (the expanded ones stay
    // whole; drafts are always shortened).
    private void ApplyShortened()
    {
        Preserving(() =>
        {
            foreach (var (id, row) in bubbles)
            {
                row.Bubble.SetShortened(Shortened(row.Right, expanded.Is(id)));
            }
        });
    }

    // The avatars' setting: every row's avatar anew.
    private void ApplyAvatars()
    {
        if (ctrl.Model is not { } m)
        {
            return;
        }
        var monochrome = services.State.Settings.MonochromeAvatars;
        foreach (var (id, row) in bubbles)
        {
            var i = m.Index(id);
            if (i >= 0)
            {
                row.ShowAvatar(m.Items[i].Sender, monochrome, ConversationRow.PhotoSenderOf(m.Items[i]));
            }
        }
        foreach (var card in ctrl.Drafts)
        {
            if (card.Draft.Id is { } id && draftRows.TryGetValue(id, out var row))
            {
                row.ShowAvatar(card.Sender, monochrome);
            }
        }
    }

    private void TrimWebPool(int keep)
    {
        while (webPool.Count > keep)
        {
            var web = webPool[^1];
            webPool.RemoveAt(webPool.Count - 1);
            web.Release();
        }
    }

    // A style of the chat's resources (ConversationStyles.xaml).
    private Style Look(string key) => (Style)Resources[key];
}
