// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_host.go (conversationHost:
// showLayout, the strip, the spinner, hover, pageBy, shownIssueCard) and
// conversation_fanout.go (convFanout: changed, loaded, trim); macOS:
// MessageView/ConversationHostViewController.swift and
// ConversationStripView.swift; what the reading pane's quick-reply bar
// learns of the conversation: GTK conversation_host.go (updateQuickReply,
// applyLayout's SetShown), macOS ConversationHostViewController
// (onQuickReplyChange) and ConversationViewController (the keyboard of a
// hiding bar). Until the chat layout this class was the
// timeline, which is ConversationTimelineView.xaml.cs now; the name stayed
// with the page, so the reader's callers (ReaderHub, MessageView) did not
// change.
//
// The conversation in the reading pane (docs/plans/chat-view.md §6.1): the
// one page the pane shows for a selected conversation row. Above, a fixed
// strip that does not scroll (D8): Collapse All / Expand All of the
// timeline and the layout toggle, Show as Chat or Show as Thread as the
// setting stands (D9, Conversation.ChatToggleLabel); below it, filling the
// rest, the two layouts of the same conversation, of which one shows: the
// timeline (ConversationTimelineView, the default) and the chat
// (ConversationChatView). The setting conversation-chat-layout picks it
// (SettingsStore.ConversationChatLayout, for every conversation and across
// restarts): the toggle only writes the setting, and the layout follows the
// setting's change (OnChange), not the click, so a change from elsewhere
// switches too. The hidden layout stays alive, so the timeline's folds and
// revealed quoted text outlive a visit to the chat (D10).
//
// The host is the one subscriber of the conversation controller in the
// pane (Core's ConversationController has multicast events, Swift and Go
// one slot each): every change goes to both layouts, which act on it at
// once while shown and catch up when shown again; the news of a body goes
// to the layout shown, which renders every body held when it is shown. The
// host alone trims the controller's bodies (ConversationController.Trim),
// with the near set of the layout shown; a hidden layout asks for no body
// and the hidden timeline lets go of its web views. Marking the
// conversation read is not the views' business: the list controller arms
// it when the members are known (ConversationModel.MarkRead), whichever
// layout shows.
//
// The Jira issue card: each layout has its own (ConversationIssueBox) at
// the top of its column, the timeline's beside its gutter, the chat's
// across the column; the busy state and the refreshed issue of a transition
// go to both (IssueBusyChanged, ApplyIssue, from ReaderHub), Change Status
// in the menus pops up the card of the layout shown (OpenStatusMenu). Two
// cards are simpler than moving one between the layouts' columns, whose
// anchors and scroll positions it belongs to (macOS and GTK chose the
// same). The fan-out of the message cache (ShowLoaded, RefreshBars,
// RefreshChips, from ReaderHub) reaches the host, which keeps the
// controller's entry (ConversationController.Adopt) and passes the news to
// the layout shown. Space and Shift+Space from the list page the layout
// shown (PageBy). The host owns the spinner while the members load and the
// status label of the link under the pointer (only the timeline has links).
//
// The quick-reply bar (docs/plans/chat-view.md §10–§12) is the reading
// pane's, not this page's: it stands under the page of a single message and
// under the chat in the same place (MessageView in ReaderMode.Pane owns it
// and its controller). This page only tells the pane that what the bar shows
// of it may have changed (QuickReplyChanged: every change of the
// conversation controller but Cleared, and every switch of the layout); the
// pane then reads the conversation (Controller, ChatShown) and feeds the
// bar's controller. Cleared is not told: the hub clears the conversation
// only for a row that is no conversation, and the page the pane shows next
// (the message, the empty page, the count of several rows) feeds the bar, so
// that the bar never flickers hidden on the way from a conversation to a
// message of the same thread (macOS switches the page before clearing for
// the same reason). A bar hiding with the keyboard gives it to the layout
// shown (TakeKeyboard).

using System;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>The conversation page of the main window's reading pane: the strip and the two layouts.</summary>
internal sealed partial class ConversationView : UserControl
{
    // convStatusMaxRunes: the most of a link the status shows.
    private const int StatusMaxChars = 512;

    private readonly ReaderServices services;
    private readonly ConversationController ctrl;
    private readonly ConversationTimelineView timeline;
    private readonly ConversationChatView chat;
    private readonly DispatcherQueueTimer spinnerTimer;
    private readonly ILogger logger;
    private readonly EventHandler<ConversationController.Change> onChanged;
    private readonly EventHandler<(MessageId Id, LoadedMessage Entry)> onLoaded;
    private SettingsChangeToken? layoutToken;

    // The chat is the layout shown; started: ShowLayout ran once.
    private bool chatShown;
    private bool started;

    /// <summary>The page over <paramref name="ctrl"/>, with the reader's services.</summary>
    public ConversationView(ReaderServices services, ConversationController ctrl, Func<Window?> hostWindow, Action focusList)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(ctrl);
        this.services = services;
        this.ctrl = ctrl;
        HostWindowOf = hostWindow;
        logger = services.State.Logs.CreateLogger<ConversationView>();
        InitializeComponent();
        spinnerTimer = DispatcherQueue.GetForCurrentThread().CreateTimer();
        spinnerTimer.IsRepeating = false;
        spinnerTimer.Interval = ReaderController.SpinnerDelay;
        spinnerTimer.Tick += (_, _) =>
        {
            Spinner.IsActive = true;
            Spinner.Visibility = Visibility.Visible;
        };
        ChatToggleGlyph.Glyph = Icons.Glyph("malachi-chat");

        timeline = new ConversationTimelineView(this, services, ctrl, hostWindow, focusList);
        chat = new ConversationChatView(this, services, ctrl);
        Layouts.Children.Add(timeline);
        Layouts.Children.Add(chat);
        timeline.FoldAllChanged += (_, offer) =>
        {
            if (!chatShown)
            {
                ShowFoldAll(offer);
            }
        };
        timeline.NearChanged += (_, near) =>
        {
            if (!chatShown)
            {
                ctrl.Trim(near);
            }
        };
        chat.NearChanged += (_, near) =>
        {
            if (chatShown)
            {
                ctrl.Trim(near);
            }
        };

        onChanged = (_, change) => ControllerChanged(change);
        onLoaded = (_, e) => ControllerEntryLoaded(e.Id, e.Entry);
        ctrl.Changed += onChanged;
        ctrl.EntryLoaded += onLoaded;
        layoutToken = services.State.Settings.OnChange(SettingsKey.ConversationChatLayout, () => ShowLayout());

        // Shown again (another page of the pane was on top): the layout
        // shown looks at what is near its viewport anew.
        RegisterPropertyChangedCallback(VisibilityProperty, (_, _) =>
        {
            if (Visibility == Visibility.Visible)
            {
                if (chatShown)
                {
                    chat.PageShown();
                }
                else
                {
                    timeline.PageShown();
                }
            }
        });
        ShowLayout(initial: true);
    }

    /// <summary>The window the pane is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow => HostWindowOf();

    /// <summary>Whether the pane shows a conversation (conversationShown).</summary>
    public bool Shown => ctrl.Thread is not null;

    /// <summary>The page itself is the pane's page on show (the layouts measure and fetch only then).</summary>
    internal bool PageVisible => Visibility == Visibility.Visible;

    /// <summary>The conversation controller of the pane (the quick-reply bar's input: Thread, ThreadAccount, QuickReplyInput).</summary>
    internal ConversationController Controller => ctrl;

    /// <summary>The chat is the layout shown (the quick-reply bar stands under it, not under the timeline).</summary>
    internal bool ChatShown => chatShown;

    private Func<Window?> HostWindowOf { get; }

    /// <summary>
    /// conversationShowLoaded: the cache has news about member
    /// <paramref name="id"/> (the remote images, a download): when the
    /// layout shown displays it, the controller's entry is that one and the
    /// layout renders it (macOS <c>showLoaded</c> after <c>displays</c>, GTK
    /// conversationShowLoaded); news of a member it does not show is left to
    /// the cache.
    /// </summary>
    public void ShowLoaded(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        if (chatShown)
        {
            if (chat.Displays(id))
            {
                ctrl.Adopt(id, lm);
                chat.EntryLoaded(id, lm);
            }
        }
        else if (timeline.Displays(id))
        {
            ctrl.Adopt(id, lm);
            timeline.ShowLoaded(id, lm);
        }
    }

    /// <summary>conversationRefreshBars: redraws the bars of the timeline's card of <paramref name="id"/> (the chat has no bars).</summary>
    public void RefreshBars(MessageId id, LoadedMessage lm)
    {
        if (!chatShown)
        {
            timeline.RefreshBars(id, lm);
        }
    }

    /// <summary>
    /// conversationRefreshChips: redraws the chips of the timeline's card of
    /// <paramref name="id"/>; in the chat, the bubble's paperclip follows the
    /// entry.
    /// </summary>
    public void RefreshChips(MessageId id, LoadedMessage? lm)
    {
        if (!chatShown)
        {
            timeline.RefreshChips(id, lm);
        }
        else if (lm is not null)
        {
            chat.EntryLoaded(id, lm);
        }
    }

    /// <summary>conversationSetIssueBusy: a transition started or ended on an issue; both layouts' issue cards follow.</summary>
    public void IssueBusyChanged(AccountId account, string key)
    {
        timeline.IssueBusyChanged(account, key);
        chat.IssueBusyChanged(account, key);
    }

    /// <summary>conversationApplyIssue: the refreshed issue of a transition on both layouts' issue cards.</summary>
    public void ApplyIssue(AccountId account, IssueInfo info)
    {
        timeline.ApplyIssue(account, info);
        chat.ApplyIssue(account, info);
    }

    /// <summary>win.change-status: the Change Status menu of the issue card of the layout shown; false when there is none.</summary>
    public bool OpenStatusMenu() => Shown && (chatShown ? chat.OpenStatusMenu() : timeline.OpenStatusMenu());

    /// <summary>pageBy: one page of the layout shown down (or up) for Space (Shift+Space) in the list; true when the key was used.</summary>
    public bool PageBy(bool up) => chatShown ? chat.PageBy(up) : timeline.PageBy(up);

    /// <summary>hover: the link under the pointer of any card of the timeline ("" hides it): plain text, capped.</summary>
    public void Hover(string link)
    {
        var text = L.ClampText(link ?? "", StatusMaxChars);
        StatusText.Text = text;
        StatusBox.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>issue_card.go openKey: the issue in the browser (a URL of the account's own site, the card checked it); a failure is a toast.</summary>
    public void OpenIssue(string url) => _ = OpenIssueAsync(url);

    /// <summary>
    /// What the reading pane's quick-reply bar shows of the conversation may
    /// have changed: a change of the conversation controller other than
    /// <see cref="ConversationController.Change.Cleared"/>, or the layout
    /// shown (updateQuickReply, applyLayout).
    /// </summary>
    internal event EventHandler? QuickReplyChanged;

    /// <summary>
    /// A hiding quick-reply bar had the keyboard: the layout shown takes it
    /// (its scroller, which is no Tab stop: macOS
    /// ConversationScrollView, GTK shownScroller().GrabFocus()); false when
    /// it could not.
    /// </summary>
    internal bool TakeKeyboard() => chatShown ? chat.TakeKeyboard() : timeline.TakeKeyboard();

    /// <summary>
    /// Gives <paramref name="scroller"/>, a scroller that is no Tab stop, the
    /// keyboard: it is a Tab stop while it holds it (WinUI focuses no control
    /// that is none) and stops being one when the keyboard leaves it, so
    /// that Tab still never stops there; false when it could not take it.
    /// </summary>
    internal static bool FocusScroller(ScrollViewer scroller)
    {
        ArgumentNullException.ThrowIfNull(scroller);
        if (scroller.Visibility != Visibility.Visible)
        {
            return false;
        }
        if (scroller.IsTabStop)
        {
            return scroller.Focus(FocusState.Programmatic);
        }
        scroller.IsTabStop = true;
        if (!scroller.Focus(FocusState.Programmatic))
        {
            scroller.IsTabStop = false;
            return false;
        }
        scroller.LostFocus += OnTakenScrollerLostFocus;
        return true;
    }

    /// <summary>The pane's views go for good (the window closes).</summary>
    public void Close()
    {
        ctrl.Changed -= onChanged;
        ctrl.EntryLoaded -= onLoaded;
        layoutToken?.Cancel();
        layoutToken = null;
        timeline.Close();
        chat.Close();
        spinnerTimer.Stop();
    }

    // showLayout: the layout the setting asks for: the other one hidden
    // first (it lets go of what it holds), then the one asked for shown (it
    // catches up with the controller and opens at its start: the timeline's
    // top, the chat's end), and the strip follows.
    private void ShowLayout(bool initial = false)
    {
        var on = services.State.Settings.ConversationChatLayout;
        ShowToggle(on);
        if (on == chatShown && started && !initial)
        {
            return;
        }
        started = true;
        chatShown = on;
        if (on)
        {
            timeline.SetShown(false);
            timeline.Visibility = Visibility.Collapsed;
            chat.Visibility = Visibility.Visible;
            ShowFoldAll(ConversationFoldAll.None);
            chat.SetShown(true);
        }
        else
        {
            chat.SetShown(false);
            chat.Visibility = Visibility.Collapsed;
            timeline.Visibility = Visibility.Visible;
            timeline.SetShown(true);
            ShowFoldAll(timeline.FoldAllOffer);
        }
        QuickReplyChanged?.Invoke(this, EventArgs.Empty);
    }

    // The toggle for the chat on or off: its state, its label, tooltip and
    // accessible name (Conversation.ChatToggleLabel, ChatToggleTooltip).
    private void ShowToggle(bool on)
    {
        ChatToggle.IsChecked = on;
        var label = Core.Model.Conversation.ChatToggleLabel(on);
        var tooltip = Core.Model.Conversation.ChatToggleTooltip(on);
        ChatToggleText.Text = label;
        ToolTipService.SetToolTip(ChatToggle, tooltip);
        AutomationProperties.SetName(ChatToggle, label);
        AutomationProperties.SetHelpText(ChatToggle, tooltip);
    }

    // A click writes the setting only; the layout and the toggle's state
    // follow the setting's change (ShowLayout). A click that left the state
    // where the setting has it already shows the setting's state again.
    private void OnChatToggleClick(object sender, RoutedEventArgs e)
    {
        var on = ChatToggle.IsChecked == true;
        if (on == services.State.Settings.ConversationChatLayout)
        {
            ShowToggle(on);
            return;
        }
        services.State.Settings.ConversationChatLayout = on;
    }

    // What Collapse All / Expand All offers (None hides it: the chat is
    // shown, or fewer than two cards fold).
    private void ShowFoldAll(ConversationFoldAll offer)
    {
        var label = offer.Label;
        FoldAllButton.Content = label;
        AutomationProperties.SetName(FoldAllButton, label);
        FoldAllButton.Visibility = offer == ConversationFoldAll.None ? Visibility.Collapsed : Visibility.Visible;
    }

    private void OnFoldAllClick(object sender, RoutedEventArgs e)
    {
        if (!chatShown)
        {
            timeline.FoldAll();
        }
    }

    // convFanout.changed: the host's own part (the spinner), then both
    // layouts.
    private void ControllerChanged(ConversationController.Change change)
    {
        switch (change)
        {
            case ConversationController.Change.Loading:
                Hover("");
                StartSpinner();
                break;
            case ConversationController.Change.Cleared:
                Hover("");
                StopSpinner();
                break;
            default:
                StopSpinner();
                break;
        }
        timeline.Changed(change);
        chat.Changed(change);
        if (change != ConversationController.Change.Cleared)
        {
            QuickReplyChanged?.Invoke(this, EventArgs.Empty);
        }
    }

    // convFanout.loaded: the news of a body, for the layout shown only.
    private void ControllerEntryLoaded(MessageId id, LoadedMessage lm)
    {
        if (chatShown)
        {
            chat.EntryLoaded(id, lm);
        }
        else
        {
            timeline.EntryLoaded(id, lm);
        }
    }

    // The keyboard left a scroller FocusScroller made a Tab stop: no longer one.
    private static void OnTakenScrollerLostFocus(object sender, RoutedEventArgs e)
    {
        var scroller = (ScrollViewer)sender;
        scroller.LostFocus -= OnTakenScrollerLostFocus;
        scroller.IsTabStop = false;
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

    // issue_card.go openKey: the issue in the browser; a failure is a toast.
    private async System.Threading.Tasks.Task OpenIssueAsync(string url)
    {
        var window = HostWindow;
        try
        {
            await services.State.Launcher.OpenLinkAsync(url, ReaderServices.Owner(window));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogOpenIssueFailed(logger, e.GetType().Name);
            // TRANSLATORS: %s is a technical error message.
            services.ToastIn(window, L10n.T("The link could not be opened: %s", e.Message));
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "opening an issue failed: {Kind}")]
    private static partial void LogOpenIssueFailed(ILogger logger, string kind);
}
