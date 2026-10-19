// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_view.go (conversationView:
// changed, setShown, takeWebView, returnWebView, trimWebPool, open, update,
// setCardFolded, apply with the draft cards, replaceRow, detach, removeAll,
// pageBy, scrollBy, scheduleLiveUpdate, updateLive, afterLayout, setZoom);
// macOS: MessageView/ConversationViewController.swift. Until the chat
// layout (docs/plans/chat-view.md) this was ConversationView itself; the
// host of both layouts kept that name (ConversationView.xaml.cs).
//
// The timeline, the default layout of the conversation in the reading
// pane: selecting a folded conversation row of the grouped list stacks
// every member the folder holds as Jira shows an issue: what opened the
// conversation first, folded to its header while more follows, then the
// rest newest first (ConversationLayout.DisplayOrder), the pane opened at
// its top. The user's replies in Sent that the folder lacks are cards among
// them by date, folded. Every card folds and opens with its arrow
// (ConversationFolds, fold.go), and Collapse All / Expand All in the host's
// strip folds or opens them all (Conversation.FoldAllOffer, reported to the
// host through FoldAllChanged, carried out in FoldAll). The user's unsent
// replies (and comments) are draft cards right under what opened the
// conversation, newest first (ConversationDraftView, conversation_draft.go),
// never folded and never live: their text is plain. A Jira conversation
// has its issue card once on top (ConversationIssueBox); its description
// and comments are cards (ConversationCard), its status and assignee
// changes compact rows, and older members left out by thread.get's cap one
// row at the bottom (ConversationEventRow, the truncated row). The
// controller half is Core's ConversationController; the host hands this
// view its changes (Changed, EntryLoaded) and trims the controller's bodies with
// the near set this view reports (NearChanged) while it is the layout
// shown. Hidden (the chat shows), the cards let go of their web views and
// nothing is measured or fetched; what changed meanwhile is caught up with
// once shown again (SetShown), the folds and the revealed quoted text as
// they were (D10).
//
// A stack of native cards in one scrolling column, never one composed
// document: each card's body is its own locked view with one sanitiser
// output in it (CardWebView, docs/security.md §3.2). The cards are cheap: a
// body is fetched only near the viewport and a web view exists only for the
// nearest few HTML cards (ConversationLayout.LiveCards), from a pool; the
// others keep the height they last had.
//
// Windows: while heights settle (bodies arriving, web views measuring their
// documents) the item the user reads stays in place through the
// ScrollViewer's own anchoring (every row an anchor candidate, the anchor at
// the viewport's top), where GTK moves the adjustment back itself; at the
// top of the conversation the view stays at the top, so an arrival under
// the opening card is in view, as GTK's pinned anchor keeps it. A card the
// user folds, alone or with Collapse All, whose top was above the viewport
// is brought back with its header at the viewport's top (macOS
// headerAnchor), which the ScrollViewer's anchoring alone would leave above
// it. The wheel over a card whose document fits scrolls the column (the
// card forwards it); the list keeps the keyboard, and Space and Shift+Space
// page through the conversation (through the host).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>The timeline layout of the conversation page of the main window's reading pane.</summary>
internal sealed partial class ConversationTimelineView : UserControl
{
    // convIdleWebViews: how many card views the pool keeps while no
    // conversation is shown (or the timeline is hidden).
    private const int IdleWebViews = 2;

    // convLayoutTries: how many layouts UpdateLive waits for the rows to be
    // laid out, so a card that never gets a size does not keep a callback.
    private const int LayoutTries = 30;

    // libadwaita's body text, which the text-zoom setting scales.
    private const double BaseBodyFontSize = 14;

    // The children of the column before the model's items: the issue card
    // (macOS headViews; Collapse All went to the host's strip).
    private const int HeadViews = 1;

    private readonly ConversationView host;
    private readonly ConversationController ctrl;
    private readonly ConversationIssueBox issue;
    private readonly Dictionary<MessageId, ConversationCard> cards = [];
    private readonly Dictionary<MessageId, ConversationEventRow> events = [];
    private readonly Dictionary<MessageId, ConversationRow> rows = [];

    // The draft cards of the user's unsent replies (ConversationDraftView)
    // and their rows, by draft id (conversation_view.go drafts, draftRows).
    private readonly Dictionary<DraftId, ConversationDraftView> drafts = [];
    private readonly Dictionary<DraftId, ConversationRow> draftRows = [];
    private readonly List<CardWebView> webPool = [];
    private readonly List<SettingsChangeToken> tokens = [];
    private List<ConversationRow> items = [];
    private TextBlock? truncated;
    private ConversationRow? truncatedRow;

    // The user's folds and unfolds of the cards of the conversation on show
    // (SetCardFolded, Collapse All / Expand All), which win over the default
    // (Conversation.DefaultFolds) until another conversation is shown; the
    // cards' fold state as last applied; what Collapse All / Expand All
    // offers.
    private ConversationFolds folds = new();
    private Dictionary<MessageId, bool> foldState = [];
    private bool compact;
    private double width = -1;
    private bool liveScheduled;
    private int layoutTries;
    private bool waitingForLayout;

    // This layout is the one the host shows, and what a change while it was
    // hidden left to do once shown (macOS Stale).
    private bool shown;
    private Stale stale;

    /// <summary>The timeline over <paramref name="ctrl"/> in <paramref name="host"/>, with the reader's services.</summary>
    public ConversationTimelineView(ConversationView host, ReaderServices services, ConversationController ctrl, Func<Window?> hostWindow, Action focusList)
    {
        ArgumentNullException.ThrowIfNull(host);
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(ctrl);
        this.host = host;
        Services = services;
        this.ctrl = ctrl;
        HostWindowOf = hostWindow;
        FocusList = focusList;
        Logger = services.State.Logs.CreateLogger<ConversationTimelineView>();
        InitializeComponent();
        AfterRelease = new AfterRelease(this);
        issue = new ConversationIssueBox(IssueCard, services, ctrl, host.OpenIssue);

        Scroller.ViewChanged += (_, _) => ScheduleLiveUpdate();
        Scroller.SizeChanged += (_, _) => ScheduleLiveUpdate();
        Column.SizeChanged += (_, _) => ScheduleLiveUpdate();
        var settings = services.State.Settings;
        tokens.Add(settings.OnChange(SettingsKey.TextZoom, () =>
        {
            ApplyBodyFont();
            foreach (var c in cards.Values)
            {
                c.SetZoom(settings.TextZoom);
            }
        }));
        tokens.Add(settings.OnChange(SettingsKey.MonospacePlainText, ApplyBodyFont));
        tokens.Add(settings.OnChange(SettingsKey.MonochromeAvatars, OnMonochromeChanged));
        services.Attachments.SavingAllChanged += OnSavingAllChanged;
    }

    /// <summary>What Collapse All / Expand All offers changed (the host's strip shows it while the timeline shows).</summary>
    public event EventHandler<ConversationFoldAll>? FoldAllChanged;

    /// <summary>The members whose bodies the cards near the viewport keep (the host trims the controller with them while the timeline shows).</summary>
    public event EventHandler<IReadOnlySet<MessageId>>? NearChanged;

    // What a change of the controller while hidden left to do.
    private enum Stale
    {
        None,
        Open,
        Update,
    }

    /// <summary>The reader's services.</summary>
    internal ReaderServices Services { get; }

    /// <summary>The window the pane is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow => HostWindowOf();

    /// <summary>Puts the keyboard back in the list (a chip that had it went).</summary>
    internal Action FocusList { get; }

    /// <summary>The pane's logger.</summary>
    internal ILogger Logger { get; }

    /// <summary>
    /// What a double click on a card's header waits for before it opens a
    /// window: the release of its second press, as the list's does (a window
    /// opened on the press can come up behind this one).
    /// </summary>
    internal AfterRelease AfterRelease { get; }

    /// <summary>What Collapse All / Expand All offers now (None: fewer than two cards fold).</summary>
    public ConversationFoldAll FoldAllOffer { get; private set; }

    /// <summary>The plain-text body's size (the text-zoom setting).</summary>
    public double BodyFontSize => BaseBodyFontSize * Services.State.Settings.TextZoom / 100.0;

    /// <summary>The plain-text body's family: monospace with its setting, else the default (null).</summary>
    public FontFamily? BodyFontFamily =>
        Services.State.Settings.MonospacePlainText && Application.Current.Resources.TryGetValue("MonospaceFontFamily", out var font) && font is FontFamily mono
            ? mono
            : null;

    private Func<Window?> HostWindowOf { get; }

    /// <summary>The entry held for the card of member <paramref name="id"/>.</summary>
    public LoadedMessage? Held(MessageId id) => ctrl.Loaded.GetValueOrDefault(id);

    /// <summary>The role of the folder <paramref name="s"/> lies in (the bulk strip of its card changes in the junk folder).</summary>
    public FolderRole RoleOf(MessageSummary s) => ctrl.FolderRoleOf(s);

    /// <summary>The buttons the card of <paramref name="s"/> offers.</summary>
    public CapabilityActions Actions(MessageSummary s) => ctrl.Actions(s);

    /// <summary>The card of member <paramref name="id"/> is near and needs its body (and message.get with <paramref name="details"/>).</summary>
    public void NeedsBody(MessageId id, bool details) => ctrl.NeedsBody(id, details);

    /// <summary>
    /// The card's "•••": the controller switches its body to the variant
    /// asked for (<see cref="ConversationController.SetQuoted"/>), held or
    /// fetched, and the card shows it when it arrives (EntryLoaded, ShowLoaded;
    /// macOS <c>cardSetQuoted</c>).
    /// </summary>
    public void SetCardQuoted(ConversationCard card, bool on)
    {
        ArgumentNullException.ThrowIfNull(card);
        ctrl.SetQuoted(card.Id, on, card.DetailsOpen);
    }

    /// <summary>
    /// A change of the controller, from the host. While hidden the cards are
    /// only taken out when the conversation goes; what else changed waits
    /// for <see cref="SetShown"/>.
    /// </summary>
    public void Changed(ConversationController.Change change)
    {
        switch (change)
        {
            case ConversationController.Change.Loading:
                stale = Stale.None;
                RemoveAll();
                break;
            case ConversationController.Change.Opened:
                if (shown)
                {
                    stale = Stale.None;
                    Open();
                }
                else
                {
                    RemoveAll();
                    stale = Stale.Open;
                }
                break;
            case ConversationController.Change.Updated:
                if (shown)
                {
                    Update();
                }
                else if (stale == Stale.None)
                {
                    stale = Stale.Update;
                }
                break;
            case ConversationController.Change.Cleared:
                stale = Stale.None;
                RemoveAll();
                // The pane shows something else: only a few views wait for
                // the next conversation, the others' processes end.
                TrimWebPool(IdleWebViews);
                break;
        }
    }

    /// <summary>A card's entry has news (the controller's EntryLoaded, from the host while the timeline shows).</summary>
    public void EntryLoaded(MessageId id, LoadedMessage lm)
    {
        if (cards.TryGetValue(id, out var c))
        {
            c.Render(lm);
        }
    }

    /// <summary>
    /// The host shows this layout (<paramref name="on"/>) or the chat.
    /// Hidden, the cards let go of their web views and nothing is measured or
    /// fetched; shown again, the cards catch up with the controller (built
    /// anew when the conversation was, reconciled when it changed, every card
    /// rendered from the bodies held) and the pane opens at the top of the
    /// conversation, the folds and the revealed quoted text as they were.
    /// </summary>
    public void SetShown(bool on)
    {
        if (on == shown)
        {
            return;
        }
        shown = on;
        if (!on)
        {
            foreach (var c in cards.Values)
            {
                c.SetLive(false);
            }
            TrimWebPool(IdleWebViews);
            host.Hover("");
            return;
        }
        // Just made visible: laid out first, so that the width Open and
        // UpdateLive read (the short dates) is the pane's, not the hidden
        // layout's.
        Scroller.UpdateLayout();
        var was = stale;
        stale = Stale.None;
        if (was == Stale.Open)
        {
            Open();
            return;
        }
        if (ctrl.Model is not { } m)
        {
            return;
        }
        if (was == Stale.Update)
        {
            Apply(m);
        }
        else
        {
            ShowFoldAll();
        }
        // Only the entries held: a card whose entry was let go keeps what it
        // shows (a render with null would take its chips and bars).
        foreach (var (id, card) in cards)
        {
            if (ctrl.Loaded.GetValueOrDefault(id) is { } lm)
            {
                card.Render(lm);
            }
        }
        Scroller.ChangeView(null, 0, null, disableAnimation: true);
        ScheduleLiveUpdate();
    }

    /// <summary>The page of the pane is shown again (another page was on top): the cards near the viewport are looked at anew.</summary>
    public void PageShown() => ScheduleLiveUpdate();

    /// <summary>Whether member <paramref name="id"/> has a card here (macOS <c>displays</c>).</summary>
    internal bool Displays(MessageId id) => cards.ContainsKey(id);

    /// <summary>
    /// conversationShowLoaded: the cache has news about member
    /// <paramref name="id"/> (the remote images, a download), which the host
    /// adopted as the controller's entry: the card renders it.
    /// </summary>
    public void ShowLoaded(MessageId id, LoadedMessage lm)
    {
        if (cards.TryGetValue(id, out var c))
        {
            c.Render(lm);
        }
    }

    /// <summary>conversationRefreshBars: redraws the bars of the card of <paramref name="id"/>.</summary>
    public void RefreshBars(MessageId id, LoadedMessage lm)
    {
        if (cards.TryGetValue(id, out var c))
        {
            c.RenderBars(lm);
        }
    }

    /// <summary>conversationRefreshChips: redraws the chips of the card of <paramref name="id"/>.</summary>
    public void RefreshChips(MessageId id, LoadedMessage? lm)
    {
        if (cards.TryGetValue(id, out var c))
        {
            c.RefreshChips(lm);
        }
    }

    /// <summary>conversationSetIssueBusy: a transition started or ended on an issue; the issue card on top follows.</summary>
    public void IssueBusyChanged(AccountId account, string key) => issue.BusyChanged(account, key);

    /// <summary>conversationApplyIssue: the refreshed issue of a transition on the issue card at once when it is the issue on show.</summary>
    public void ApplyIssue(AccountId account, IssueInfo info) => issue.Apply(account, info);

    /// <summary>win.change-status: the Change Status menu of the issue card on top; false when there is none.</summary>
    public bool OpenStatusMenu() => issue.OpenStatusMenu();

    /// <summary>pageBy: one page down (or up) for Space (Shift+Space) in the list; true when the key was used.</summary>
    public bool PageBy(bool up)
    {
        if (ctrl.Model is null)
        {
            return false;
        }
        var page = Scroller.ViewportHeight;
        var top = L.PageTop(Scroller.VerticalOffset, up, page, Scroller.ExtentHeight, page * 0.1);
        Scroller.ChangeView(null, top, null);
        return true;
    }

    /// <summary>
    /// scrollBy: scrolls the column by what a card whose document fits handed
    /// on: <paramref name="wheelDelta"/> in the wheel's units (120 a step), a
    /// step being GTK's own for a scrolled window (the viewport's height to
    /// the power 2/3).
    /// </summary>
    public void ScrollBy(double wheelDelta)
    {
        var step = Math.Pow(Math.Max(Scroller.ViewportHeight, 1), 2.0 / 3.0);
        var to = Scroller.VerticalOffset + (wheelDelta / 120.0 * step);
        Scroller.ChangeView(null, Math.Clamp(to, 0, Math.Max(0, Scroller.ScrollableHeight)), null);
    }

    /// <summary>hover: the link under the pointer of any card ("" hides it), in the host's status.</summary>
    public void Hover(string link) => host.Hover(link);

    /// <summary>
    /// takeWebView: a card view for a card going live: one from the pool, its
    /// zoom brought up to date, or a new one. Reusing them keeps their number
    /// at the live window's however the cards come and go.
    /// </summary>
    public CardWebView TakeWebView()
    {
        var zoom = Services.State.Settings.TextZoom;
        if (webPool.Count > 0)
        {
            var pooled = webPool[^1];
            webPool.RemoveAt(webPool.Count - 1);
            pooled.Zoom = zoom;
            return pooled;
        }
        var web = new CardWebView { Zoom = zoom };
        var cache = Services.Cache;
        web.Parts = (part, token) => cache.FetchPartAsync(part.AccountId, part.MessageId, part.PartId, token);
        return web;
    }

    /// <summary>returnWebView: takes a card view back (already out of its card): reset for the next card, or released when the pool is full.</summary>
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

    /// <summary>
    /// setCardFolded: folds or opens a card at the user's request (its arrow,
    /// a click on its preview); the choice holds while the conversation is
    /// shown. An opened card asks for its body. Until the user scrolls the
    /// top of the conversation stays in view; otherwise the card keeps its
    /// header in view. Collapse All / Expand All follows.
    /// </summary>
    public void SetCardFolded(ConversationCard card, bool folded)
    {
        ArgumentNullException.ThrowIfNull(card);
        folds.Set(card.Id, folded);
        foldState[card.Id] = folded;
        KeepingHeader(rows.GetValueOrDefault(card.Id), () => card.SetFolded(folded));
        ShowFoldAll();
        ScheduleLiveUpdate();
    }

    /// <summary>
    /// Collapse All or Expand All (the host's strip): every card of the
    /// conversation shown folds or opens (ConversationFolds.SetAll); a card
    /// that arrives later starts as its default. Until the user scrolls the
    /// top of the conversation stays in view; otherwise the item under the
    /// viewport's top keeps its header in view.
    /// </summary>
    public void FoldAll()
    {
        if (ctrl.Model is not { } m || FoldAllOffer == ConversationFoldAll.None)
        {
            return;
        }
        var display = L.DisplayOrder(m.Items);
        folds.SetAll(display.Items, FoldAllOffer.Folded);
        foldState = folds.State(display.Items, display.Opening);
        KeepingHeader(RowAtTop(), () =>
        {
            foreach (var (id, card) in cards)
            {
                if (card.Foldable)
                {
                    card.SetFolded(foldState.GetValueOrDefault(id));
                }
            }
        });
        ShowFoldAll();
        ScheduleLiveUpdate();
    }

    /// <summary>scheduleLiveUpdate: UpdateLive once the UI thread is free.</summary>
    public void ScheduleLiveUpdate()
    {
        if (liveScheduled)
        {
            return;
        }
        liveScheduled = true;
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            liveScheduled = false;
            UpdateLive();
        });
    }

    /// <summary>The timeline's views go for good (the window closes).</summary>
    public void Close()
    {
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        Services.Attachments.SavingAllChanged -= OnSavingAllChanged;
        if (waitingForLayout)
        {
            Column.LayoutUpdated -= OnceLaidOut;
            waitingForLayout = false;
        }
        RemoveAll();
        TrimWebPool(0);
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new ConversationGroupPeer(this, nameof(ConversationTimelineView), AutomationControlType.Pane);

    private void OnSavingAllChanged(object? sender, MessageId id)
    {
        foreach (var c in cards.Values)
        {
            c.SavingAllChanged(id);
        }
    }

    // The avatars' setting: the rows anew while shown; hidden, once shown.
    private void OnMonochromeChanged()
    {
        if (ctrl.Model is null)
        {
            return;
        }
        if (!shown)
        {
            if (stale == Stale.None)
            {
                stale = Stale.Update;
            }
            return;
        }
        Apply(ctrl.Model);
    }

    // open: a conversation built anew, laid out from its top.
    private void Open()
    {
        RemoveAll();
        if (ctrl.Model is not { } m)
        {
            return;
        }
        compact = L.CompactDates(Scroller.ActualWidth);
        width = Scroller.ActualWidth;
        Apply(m);
        Scroller.ChangeView(null, 0, null, disableAnimation: true);
        ScheduleLiveUpdate();
    }

    // update: the shown conversation changed; the cards are reconciled by id,
    // and what the user reads stays in place (the ScrollViewer's anchor).
    private void Update()
    {
        if (ctrl.Model is not { } m)
        {
            return;
        }
        Apply(m);
        ScheduleLiveUpdate();
    }

    // apply: the model's items into the column in the order shown
    // (DisplayOrder), the draft cards of the user's unsent replies right
    // under what opened it (Conversation.DraftsFirst), reusing the rows of
    // the members and drafts shown already, each with its piece of the
    // timeline; every card folds as the user or the default says
    // (ConversationFolds.State), and Collapse All / Expand All follows.
    private void Apply(ConversationModel? m)
    {
        if (m is null)
        {
            return;
        }
        issue.Show(m);
        var display = L.DisplayOrder(m.Items);
        var shownItems = display.Items;
        folds.Show(m.Thread);
        foldState = folds.State(shownItems, display.Opening);
        var cardsOfDrafts = ctrl.Drafts;
        var at = Math.Min(Core.Model.Conversation.DraftsFirst(display.Root), shownItems.Count);
        var rails = L.Rails(L.WithDrafts(shownItems, at, cardsOfDrafts.Count));
        var monochrome = Services.State.Settings.MonochromeAvatars;
        var desired = new List<ConversationRow>(shownItems.Count + cardsOfDrafts.Count);
        var cardIds = new HashSet<MessageId>();
        var eventIds = new HashSet<MessageId>();
        var draftIds = new HashSet<DraftId>();
        void PutDrafts()
        {
            for (var j = 0; j < cardsOfDrafts.Count; j++)
            {
                var card = cardsOfDrafts[j];
                var id = card.Draft.Id ?? default;
                draftIds.Add(id);
                if (drafts.TryGetValue(id, out var view) && draftRows.TryGetValue(id, out var existing))
                {
                    view.Update(card);
                }
                else
                {
                    view = new ConversationDraftView(this, card, Look);
                    drafts[id] = view;
                    existing = new ConversationRow(view, RailMarker.Avatar, Look);
                    draftRows[id] = existing;
                }
                existing.Show(rails[at + j], card.Sender, monochrome);
                desired.Add(existing);
            }
        }
        for (var i = 0; i < shownItems.Count; i++)
        {
            if (i == at)
            {
                PutDrafts();
            }
            var it = shownItems[i];
            var rail = rails[i < at ? i : i + cardsOfDrafts.Count];
            ConversationRow row;
            switch (it.Kind)
            {
                case ConversationItemKind.Truncated:
                    if (truncatedRow is null)
                    {
                        truncated = new TextBlock
                        {
                            Style = Look("ConversationCaptionStyle"),
                            TextWrapping = TextWrapping.Wrap,
                            Margin = new Thickness(L.CardPaddingH, 0, L.CardPaddingH, 0),
                        };
                        truncatedRow = new ConversationRow(truncated, RailMarker.Dot, Look);
                    }
                    truncated!.Text = it.Text;
                    row = truncatedRow;
                    break;
                case ConversationItemKind.Message:
                    {
                        var id = it.Message!.Id;
                        cardIds.Add(id);
                        if (cards.TryGetValue(id, out var c) && rows.TryGetValue(id, out var r) && r.Card is not null)
                        {
                            c.Update(it);
                            row = r;
                        }
                        else
                        {
                            var card = new ConversationCard(this, it, compact);
                            cards[id] = card;
                            events.Remove(id);
                            row = new ConversationRow(card, RailMarker.Avatar, Look) { Card = card };
                            ReplaceRow(id, row);
                            card.Render(Held(id));
                        }
                        break;
                    }
                case ConversationItemKind.Event:
                    {
                        var id = it.Message!.Id;
                        eventIds.Add(id);
                        if (events.TryGetValue(id, out var e) && rows.TryGetValue(id, out var r) && r.Card is null)
                        {
                            e.Update(it, compact);
                            row = r;
                        }
                        else
                        {
                            var ev = new ConversationEventRow(it, compact, Look);
                            events[id] = ev;
                            if (cards.Remove(id, out var gone))
                            {
                                gone.Detach();
                            }
                            row = new ConversationRow(ev, RailMarker.Dot, Look);
                            ReplaceRow(id, row);
                        }
                        break;
                    }
                default:
                    continue;
            }
            row.Show(rail, it.Sender, monochrome);
            if (row.Card is { } rowCard)
            {
                rowCard.SetFold(Core.Model.Conversation.Foldable(it), foldState.GetValueOrDefault(rowCard.Id));
            }
            desired.Add(row);
        }
        if (at == shownItems.Count)
        {
            PutDrafts();
        }
        foreach (var id in draftRows.Keys.Where(id => !draftIds.Contains(id)).ToList())
        {
            Detach(draftRows[id]);
            draftRows.Remove(id);
            drafts.Remove(id);
        }
        foreach (var id in cards.Keys.Where(id => !cardIds.Contains(id)).ToList())
        {
            cards[id].Detach();
            cards.Remove(id);
        }
        foreach (var id in events.Keys.Where(id => !eventIds.Contains(id)).ToList())
        {
            events.Remove(id);
        }
        foreach (var id in rows.Keys.Where(id => !cardIds.Contains(id) && !eventIds.Contains(id)).ToList())
        {
            Detach(rows[id]);
            rows.Remove(id);
        }
        if (truncatedRow is not null && !desired.Contains(truncatedRow))
        {
            Detach(truncatedRow);
        }
        // The column after the issue card, in the order shown.
        var children = Column.Children;
        for (var i = 0; i < desired.Count; i++)
        {
            var r = desired[i];
            var place = i + HeadViews;
            var now = children.IndexOf(r);
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
                Scroller.RegisterAnchorCandidate(r);
            }
            children.Insert(Math.Min(place, children.Count), r);
        }
        items = desired;
        ShowFoldAll();
    }

    // What Collapse All / Expand All offers for the cards' fold state
    // (Conversation.FoldAllOffer; None while fewer than two fold), told to
    // the host's strip.
    private void ShowFoldAll() => SetFoldAllOffer(Core.Model.Conversation.FoldAllOffer(foldState));

    private void SetFoldAllOffer(ConversationFoldAll offer)
    {
        FoldAllOffer = offer;
        FoldAllChanged?.Invoke(this, offer);
    }

    // The row under the viewport's top; null at the top of the conversation
    // or above the rows (the issue card).
    private ConversationRow? RowAtTop()
    {
        var top = Scroller.VerticalOffset;
        foreach (var r in items)
        {
            if (SpanOf(r) is { } span && span.Max > top + 0.5)
            {
                return span.Min <= top ? r : null;
            }
        }
        return null;
    }

    // Runs change, which folds or opens cards, and brings row's header back
    // to the viewport's top when the viewport began inside row (its top
    // above the viewport's): a card that folds keeps its header where the
    // user sees it (macOS headerAnchor). At the top of the conversation, and
    // for a row whose top is in view, the ScrollViewer's own anchoring keeps
    // what the user reads.
    private void KeepingHeader(ConversationRow? row, Action change)
    {
        var top = Scroller.VerticalOffset;
        var inside = row is not null && top > 0.5 && SpanOf(row) is { } before && before.Min < top;
        change();
        if (!inside)
        {
            return;
        }
        Scroller.UpdateLayout();
        if (SpanOf(row!) is { } after)
        {
            Scroller.ChangeView(null, Math.Clamp(after.Min, 0, Math.Max(0, Scroller.ScrollableHeight)), null, disableAnimation: true);
        }
    }

    // replaceRow: row is member id's; the one it had before (the member was
    // a card and is an event now, or the other way round) goes.
    private void ReplaceRow(MessageId id, ConversationRow row)
    {
        if (rows.TryGetValue(id, out var old))
        {
            Detach(old);
        }
        rows[id] = row;
    }

    // detach: a row out of the column.
    private void Detach(ConversationRow? r)
    {
        if (r is null)
        {
            return;
        }
        var i = Column.Children.IndexOf(r);
        if (i >= 0)
        {
            Scroller.UnregisterAnchorCandidate(r);
            Column.Children.RemoveAt(i);
        }
    }

    // removeAll: everything out (another conversation, or none).
    private void RemoveAll()
    {
        folds = new ConversationFolds();
        foldState = [];
        SetFoldAllOffer(ConversationFoldAll.None);
        layoutTries = 0;
        foreach (var c in cards.Values)
        {
            c.Detach();
        }
        foreach (var r in rows.Values)
        {
            Detach(r);
        }
        foreach (var r in draftRows.Values)
        {
            Detach(r);
        }
        Detach(truncatedRow);
        cards.Clear();
        events.Clear();
        rows.Clear();
        drafts.Clear();
        draftRows.Clear();
        items = [];
        issue.Clear();
        host.Hover("");
    }

    // updateLive: the bodies of the cards near the viewport, web views for
    // the nearest HTML cards and none for the rest (LiveCards), the near set
    // told to the host (which lets go of far entries beyond the controller's
    // budget), the pane's width followed (the short dates, the documents'
    // reflow). Only while the timeline is the layout shown and the page is.
    private void UpdateLive()
    {
        if (!shown || ctrl.Model is null || !host.PageVisible || XamlRoot is null)
        {
            return;
        }
        var w = Scroller.ActualWidth;
        if (Math.Abs(w - width) >= 0.5)
        {
            width = w;
            var nowCompact = L.CompactDates(w);
            if (nowCompact != compact)
            {
                compact = nowCompact;
                foreach (var c in cards.Values)
                {
                    c.SetCompact(nowCompact);
                }
                foreach (var e in events.Values)
                {
                    e.SetCompact(nowCompact);
                }
            }
        }
        var visible = L.Span.Of(Scroller.VerticalOffset, Scroller.VerticalOffset + Scroller.ViewportHeight);
        var ordered = new List<ConversationCard>();
        var frames = new List<L.Span>();
        var html = new List<bool>();
        var unplaced = false;
        foreach (var r in items)
        {
            if (r.Card is not { } card || card.Folded)
            {
                continue; // no body, no view while folded
            }
            if (SpanOf(card) is not { } span)
            {
                unplaced = true;
                continue;
            }
            ordered.Add(card);
            frames.Add(span);
            html.Add(card.IsHtml);
        }
        var live = L.LiveCards(frames, html, visible, L.LiveScreens, L.MaxLiveWebViews);
        var near = new HashSet<MessageId>();
        for (var i = 0; i < ordered.Count; i++)
        {
            var c = ordered[i];
            c.SetLive(live.Web.Contains(i));
            if (live.Near.Contains(i))
            {
                near.Add(c.Id);
                ctrl.NeedsBody(c.Id, c.DetailsOpen);
            }
        }
        NearChanged?.Invoke(this, near);
        if (unplaced)
        {
            AfterLayout();
        }
        else
        {
            layoutTries = 0;
        }
    }

    // A card's stretch of the column's document; null while it is not laid
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

    // afterLayout: UpdateLive again after the next layout (cards that had no
    // size yet were skipped, and nothing else may come to run it), a bounded
    // number of times.
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
        ScheduleLiveUpdate();
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

    private void ApplyBodyFont()
    {
        foreach (var c in cards.Values)
        {
            c.ApplyBodyFont();
        }
        foreach (var d in drafts.Values)
        {
            d.ApplyBodyFont();
        }
    }

    // A style of the timeline's resources (ConversationStyles.xaml).
    private Style Look(string key) => (Style)Resources[key];
}
