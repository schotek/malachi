// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_view.go (conversationView:
// modelChanged, takeWebView, returnWebView, trimWebPool, open, update,
// setCardFolded, apply, replaceRow, detach, removeAll, showIssue,
// clearIssue, convFirstMember, pageBy, scrollBy, scheduleLiveUpdate,
// updateLive, afterLayout, hover, setZoom, startSpinner, stopSpinner) and
// conversation_issue.go; macOS: MessageView/ConversationViewController.swift.
//
// The whole conversation in the reading pane: selecting a folded
// conversation row of the grouped list stacks every member the folder holds
// as Jira shows an issue: what opened the conversation first, folded to its
// header while more follows, then the rest newest first
// (ConversationLayout.DisplayOrder), the pane opened at its top. A Jira
// conversation has its issue card once on top; its description and
// comments are cards (ConversationCard), its status and assignee changes
// compact rows, and older members left out by thread.get's cap one row at
// the bottom (ConversationEventRow, the truncated row). The controller half
// is Core's ConversationController; this view lays the cards out and tells
// it which of them are near enough to need a body.
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
// the opening card is in view, as GTK's pinned anchor keeps it. The wheel
// over a card whose document fits scrolls the column (the card forwards
// it); the list keeps the keyboard, and Space and Shift+Space page through
// the conversation.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>The conversation page of the main window's reading pane.</summary>
internal sealed partial class ConversationView : UserControl
{
    // convIdleWebViews: how many card views the pool keeps while no
    // conversation is shown.
    private const int IdleWebViews = 2;

    // convLayoutTries: how many layouts UpdateLive waits for the rows to be
    // laid out, so a card that never gets a size does not keep a callback.
    private const int LayoutTries = 30;

    // convStatusMaxRunes: the most of a link the status shows.
    private const int StatusMaxChars = 512;

    // libadwaita's body text, which the text-zoom setting scales.
    private const double BaseBodyFontSize = 14;

    private readonly ConversationController ctrl;
    private readonly Dictionary<MessageId, ConversationCard> cards = [];
    private readonly Dictionary<MessageId, ConversationEventRow> events = [];
    private readonly Dictionary<MessageId, ConversationRow> rows = [];
    private readonly List<CardWebView> webPool = [];
    private readonly List<SettingsChangeToken> tokens = [];
    private readonly DispatcherQueueTimer spinnerTimer;
    private List<ConversationRow> items = [];
    private TextBlock? truncated;
    private ConversationRow? truncatedRow;
    private Dictionary<MessageId, bool>? folds;
    private bool compact;
    private double width = -1;
    private bool liveScheduled;
    private int layoutTries;
    private bool waitingForLayout;

    // The issue card on show and the account it acts in.
    private AccountId? issueAccount;

    /// <summary>A pane over <paramref name="ctrl"/>, with the reader's services.</summary>
    public ConversationView(ReaderServices services, ConversationController ctrl, Func<Window?> hostWindow, Action focusList)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(ctrl);
        Services = services;
        this.ctrl = ctrl;
        HostWindowOf = hostWindow;
        FocusList = focusList;
        Logger = services.State.Logs.CreateLogger<ConversationView>();
        InitializeComponent();
        spinnerTimer = DispatcherQueue.GetForCurrentThread().CreateTimer();
        spinnerTimer.IsRepeating = false;
        spinnerTimer.Interval = ReaderController.SpinnerDelay;
        spinnerTimer.Tick += (_, _) =>
        {
            Spinner.IsActive = true;
            Spinner.Visibility = Visibility.Visible;
        };
        IssueCard.Issues = services.Issues;
        IssueCard.Subject = () => FirstMember(ctrl.Model) is { } first ? IssueActionsController.SubjectOf(first) : null;
        IssueCard.OpenIssue = url => _ = OpenIssueAsync(url);
        IssueCard.State = null;

        ctrl.Changed += (_, change) => ModelChanged(change);
        ctrl.EntryLoaded += (_, e) =>
        {
            if (cards.TryGetValue(e.Id, out var c))
            {
                c.Render(e.Entry);
            }
        };
        Scroller.ViewChanged += (_, _) => ScheduleLiveUpdate();
        Scroller.SizeChanged += (_, _) => ScheduleLiveUpdate();
        Column.SizeChanged += (_, _) => ScheduleLiveUpdate();
        // Shown again (another page of the pane was on top): the cards near
        // the viewport are looked at anew.
        RegisterPropertyChangedCallback(VisibilityProperty, (_, _) =>
        {
            if (Visibility == Visibility.Visible)
            {
                ScheduleLiveUpdate();
            }
        });
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
        tokens.Add(settings.OnChange(SettingsKey.MonochromeAvatars, () => Apply(ctrl.Model)));
        services.Attachments.SavingAllChanged += OnSavingAllChanged;
    }

    /// <summary>The reader's services.</summary>
    internal ReaderServices Services { get; }

    /// <summary>The window the pane is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow => HostWindowOf();

    /// <summary>Puts the keyboard back in the list (a chip that had it went).</summary>
    internal Action FocusList { get; }

    /// <summary>The pane's logger.</summary>
    internal ILogger Logger { get; }

    /// <summary>The plain-text body's size (the text-zoom setting).</summary>
    public double BodyFontSize => BaseBodyFontSize * Services.State.Settings.TextZoom / 100.0;

    /// <summary>The plain-text body's family: monospace with its setting, else the default (null).</summary>
    public FontFamily? BodyFontFamily =>
        Services.State.Settings.MonospacePlainText && Application.Current.Resources.TryGetValue("MonospaceFontFamily", out var font) && font is FontFamily mono
            ? mono
            : null;

    /// <summary>Whether the pane shows a conversation (conversationShown).</summary>
    public bool Shown => ctrl.Thread is not null;

    private Func<Window?> HostWindowOf { get; }

    /// <summary>The entry held for the card of member <paramref name="id"/>.</summary>
    public LoadedMessage? Held(MessageId id) => ctrl.Loaded.GetValueOrDefault(id);

    /// <summary>The buttons the card of <paramref name="s"/> offers.</summary>
    public CapabilityActions Actions(MessageSummary s) => ctrl.Actions(s);

    /// <summary>The card of member <paramref name="id"/> is near and needs its body (and message.get with <paramref name="details"/>).</summary>
    public void NeedsBody(MessageId id, bool details) => ctrl.NeedsBody(id, details);

    /// <summary>
    /// conversationShowLoaded: the cache has news about member
    /// <paramref name="id"/> (the remote images, a download): its card's entry
    /// is that one, and the card renders it.
    /// </summary>
    public void ShowLoaded(MessageId id, LoadedMessage lm)
    {
        if (cards.TryGetValue(id, out var c))
        {
            ctrl.Adopt(id, lm);
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
    public void IssueBusyChanged(AccountId account, string key)
    {
        if (IssueCard.State is { } st && st.Account == account && st.Card.Key == Jira.Clean(key))
        {
            var busy = Services.Issues.IsBusy(account, key);
            if (busy != st.Busy)
            {
                IssueCard.State = st with { Busy = busy };
            }
        }
    }

    /// <summary>conversationApplyIssue: the refreshed issue of a transition on the issue card at once when it is the issue on show.</summary>
    public void ApplyIssue(AccountId account, IssueInfo info)
    {
        ArgumentNullException.ThrowIfNull(info);
        if (IssueCard.State is not { } st || st.Account != account || st.Card.Key != Jira.Clean(info.Key))
        {
            return;
        }
        IssueCard.State = CardState(Jira.IssueCard(info, null), account);
    }

    /// <summary>win.change-status: the Change Status menu of the issue card on top; false when there is none.</summary>
    public bool OpenStatusMenu() => Shown && IssueCard.OpenStatusMenu();

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

    /// <summary>hover: the link under the pointer of any card ("" hides it): plain text, capped.</summary>
    public void Hover(string link)
    {
        var text = L.ClampText(link ?? "", StatusMaxChars);
        StatusText.Text = text;
        StatusBox.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

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
    /// setCardFolded: folds or opens the card that opened the conversation at
    /// the user's request (its arrow, a click on its preview); the choice
    /// holds while the conversation is shown. An opened card asks for its
    /// body.
    /// </summary>
    public void SetCardFolded(ConversationCard card, bool folded)
    {
        ArgumentNullException.ThrowIfNull(card);
        folds ??= [];
        folds[card.Id] = folded;
        card.SetFolded(folded);
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

    /// <summary>The pane's views go for good (the window closes).</summary>
    public void Close()
    {
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        Services.Attachments.SavingAllChanged -= OnSavingAllChanged;
        RemoveAll();
        TrimWebPool(0);
        spinnerTimer.Stop();
    }

    private void OnSavingAllChanged(object? sender, MessageId id)
    {
        foreach (var c in cards.Values)
        {
            c.SavingAllChanged(id);
        }
    }

    // modelChanged: the controller's news.
    private void ModelChanged(ConversationController.Change change)
    {
        switch (change)
        {
            case ConversationController.Change.Loading:
                RemoveAll();
                StartSpinner();
                break;
            case ConversationController.Change.Opened:
                StopSpinner();
                Open();
                break;
            case ConversationController.Change.Updated:
                StopSpinner();
                Update();
                break;
            case ConversationController.Change.Cleared:
                StopSpinner();
                RemoveAll();
                // The pane shows something else: only a few views wait for
                // the next conversation, the others' processes end.
                TrimWebPool(IdleWebViews);
                break;
        }
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
    // (DisplayOrder), reusing the rows of the members shown already, each
    // with its piece of the timeline; the opening card folds as the user or
    // the default says.
    private void Apply(ConversationModel? m)
    {
        if (m is null)
        {
            return;
        }
        ShowIssue(m);
        var display = L.DisplayOrder(m.Items);
        var shown = display.Items;
        var rails = L.Rails(shown);
        var monochrome = Services.State.Settings.MonochromeAvatars;
        var desired = new List<ConversationRow>(shown.Count);
        var cardIds = new HashSet<MessageId>();
        var eventIds = new HashSet<MessageId>();
        for (var i = 0; i < shown.Count; i++)
        {
            var it = shown[i];
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
            row.Show(rails[i], it.Sender, monochrome);
            if (row.Card is { } rowCard)
            {
                var folded = folds is not null && folds.TryGetValue(rowCard.Id, out var chosen) ? chosen : display.RootFolded;
                rowCard.SetFold(i == display.Root, folded);
            }
            desired.Add(row);
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
            var at = i + 1;
            var now = children.IndexOf(r);
            if (now == at)
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
            children.Insert(Math.Min(at, children.Count), r);
        }
        items = desired;
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
        folds = null;
        layoutTries = 0;
        foreach (var c in cards.Values)
        {
            c.Detach();
        }
        foreach (var r in rows.Values)
        {
            Detach(r);
        }
        Detach(truncatedRow);
        cards.Clear();
        events.Clear();
        rows.Clear();
        items = [];
        ClearIssue();
        Hover("");
    }

    // showIssue: the issue card of a Jira conversation once on top; a mail
    // conversation has none.
    private void ShowIssue(ConversationModel m)
    {
        if (m.Issue is not { } card || FirstMember(m) is not { } first)
        {
            ClearIssue();
            return;
        }
        var state = CardState(card, first.AccountId);
        if (IssueCard.State is { } shown && issueAccount == first.AccountId && SameCard(shown, state))
        {
            return;
        }
        issueAccount = first.AccountId;
        IssueCard.State = state;
    }

    private void ClearIssue()
    {
        issueAccount = null;
        IssueCard.State = null;
    }

    // conversation_issue.go conversationIssueCard: the key a link on the
    // account's own site only, the status pill the Change Status menu on an
    // account that changes statuses.
    private IssueCardState CardState(JiraCard card, AccountId account) => new()
    {
        Card = card,
        Account = account,
        Openable = card.Url.Length > 0 && Jira.IsIssueUrl(card.Url, ctrl.IssueSite(account)),
        Menu = Services.Issues.CanTransition(account) && card.Status.Length > 0,
        Busy = card.Key.Length > 0 && Services.Issues.IsBusy(account, card.Key),
    };

    // The same card, by value (a record compares its lists by reference, and
    // JiraCard is no API type the JSON context could encode).
    private static bool SameCard(IssueCardState a, IssueCardState b) =>
        a.Account == b.Account && a.Openable == b.Openable && a.Menu == b.Menu && a.Busy == b.Busy
        && a.Card with { Rows = NoRows } == b.Card with { Rows = NoRows }
        && a.Card.Rows.SequenceEqual(b.Card.Rows);

    private static readonly JiraCardRow[] NoRows = [];

    // convFirstMember: the first member of m, which names the conversation's
    // account (and its issue).
    private static MessageSummary? FirstMember(ConversationModel? m) =>
        m?.Items.FirstOrDefault(it => it.Kind != ConversationItemKind.Truncated)?.Message;

    // issue_card.go openKey: the issue in the browser; a failure is a toast.
    private async System.Threading.Tasks.Task OpenIssueAsync(string url)
    {
        var window = HostWindow;
        try
        {
            await Services.State.Launcher.OpenLinkAsync(url, ReaderServices.Owner(window));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogOpenIssueFailed(Logger, e.GetType().Name);
            // TRANSLATORS: %s is a technical error message.
            Services.ToastIn(window, L10n.T("The link could not be opened: %s", e.Message));
        }
    }

    // updateLive: the bodies of the cards near the viewport, web views for
    // the nearest HTML cards and none for the rest (LiveCards), far entries
    // let go beyond the controller's budget, the pane's width followed (the
    // short dates, the documents' reflow).
    private void UpdateLive()
    {
        if (ctrl.Model is null || Visibility != Visibility.Visible || XamlRoot is null)
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
        ctrl.Trim(near);
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
    }

    // startSpinner: the spinner once the members take long enough to notice.
    private void StartSpinner()
    {
        StopSpinner();
        spinnerTimer.Start();
    }

    private void StopSpinner()
    {
        spinnerTimer.Stop();
        Spinner.IsActive = false;
        Spinner.Visibility = Visibility.Collapsed;
    }

    // A style of the pane's resources (ConversationStyles.xaml).
    private Style Look(string key) => (Style)Resources[key];

    [LoggerMessage(Level = LogLevel.Warning, Message = "opening an issue failed: {Kind}")]
    private static partial void LogOpenIssueFailed(ILogger logger, string kind);
}
