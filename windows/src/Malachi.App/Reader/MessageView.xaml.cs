// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/MessageViewController.swift
// (the AppKit half: the pages, the web view made on first use, the fonts,
// the chips and the address rows built from the controller, the focus
// leaving the bars, downloadPictures), AddressHeaderView.swift (the chips
// and their menu, "+N more"), AttachmentChipView.swift (the chip, its menu,
// the server symbol or the spinner, Save All) and RemoteBarView.swift (the
// remote-image bar and the pictures bar); GTK:
// ui/internal/window/message_view.go (newMessageView, setBarVisible,
// setBarLoading, htmlView, setZoom), remote.go (showPicturesBar),
// addresses.go (chip, addressMenu, moreChip), attachments.go
// (renderAttachments, buildChip, remoteIndicator, chipMenu, buildSaveAll)
// and style.go (.message-body: the text-zoom and monospace settings). The
// state is Core's ReaderController; this view draws it and sends the clicks
// back: to the window's commands (the command row and its menu), the
// MessageActionRouter (the banners, the bar, an address's New Message),
// the AttachmentOpener and previewer (the chips), the LinkOpener (the
// viewer's links) and the registry (an attached message's window). Every
// text of a message is set through a Text property; no markup is ever
// parsed.
//
// Windows differences: the chips are split buttons (the click previews, the
// arrow's menu has View, Open, Save As…; from the keyboard F4 or Alt+Down,
// SplitButton's keys, where GTK's arrow is a button of its own), a chip GTK
// makes insensitive shows its reason in a tooltip on a wrapper (a disabled
// WinUI control shows none), the remote bar's and "+N more"'s buttons take
// no focus on a click (GTK SetFocusOnClick(false)) but are reached by Tab
// (GTK; macOS keeps them out of the key loop), headers taller than two
// thirds of the page scroll (GTK's grow), Copy Address can fail while
// another program holds the clipboard (GTK's cannot) and says so, and Save
// All stays disabled while its run lasts even when a re-render rebuilds the
// button (the run is AttachmentOpener's, by message). A part kept on the
// mail server shows the server glyph (Segoe Fluent's download from the
// cloud for GTK's network-server icon) with the reason as its tooltip, or a
// ProgressRing while the message downloads, after the size inside the
// split button's main part, as GTK has it inside the chip's button (macOS
// puts it after the control).
//
// The invitation card above the header (MessageViewController+Invitation.swift
// renderInvitation, openInvitationLink; docs/calendar.md §5.7): every render
// feeds it the card of message.get (InvitationCardPresenter), the account's
// switch decides, an attached message's window shows none; Join Video Call
// goes through the link confirmation of the view's window with no listed
// links, Show in Calendar to the main window.
//
// The quick-reply bar of the pane (docs/plans/chat-view.md §12, S1–S9;
// macOS ReadingPaneViewController: the bar, its controller, the host
// adapter, the ✦ observer, show(_ row:), conversationChanged,
// showSingleReply, keyboardTarget; GTK S-G's window-owned bar): the last
// row of the pane, under the message stack and outside every scrolled
// area, made in ReaderMode.Pane only (a message window has none). Its
// controller (QuickReplyController over QuickReplyHostAdapter) follows the
// page the reader shows: the page of a single message feeds it the model of
// that message (ConversationController.QuickReplyInput(MessageSummary):
// Show with the message's account and thread, Update, SetShown false in the
// Outbox and Drafts), on every change of the page and every render; the
// conversation page feeds it the conversation (Show with
// ConversationController.ThreadAccount and Thread, Update with
// QuickReplyInput() or, while the members load, the empty model, SetShown
// while the chat shows) on every report of the page (QuickReplyChanged);
// the empty page, No Accounts and the count of several rows hide it
// (Show of none, SetShown false). The text typed is kept per account and
// thread, so it is the same under a message and under its conversation.
// The bar's icon-only mode follows the pane's width; a bar hiding with the
// keyboard gives it to the view shown (FocusBody, the conversation's
// layout, else the list).

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Threading.Tasks;
using Malachi.App.Attachments;
using Malachi.App.Commands;
using Malachi.App.Localization;
using Malachi.App.Resources;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.ApplicationModel.DataTransfer;

namespace Malachi.App.Reader;

/// <summary>One message display: the pane, a message window's or an attached message's window.</summary>
public sealed partial class MessageView : UserControl
{
    // libadwaita's body text, which the text-zoom setting scales (.message-body).
    private const double BodyFontSize = 14;

    // The headers take at most this share of the message page; past it they
    // scroll, and the body keeps the rest.
    private const double HeaderShare = 2.0 / 3.0;


    private readonly ReaderServices services;
    private readonly WindowCommands? commands;
    private readonly ILogger logger;
    private readonly List<SettingsChangeToken> tokens = [];
    private MessageWebView? web;
    private bool closed;

    // The message whose headers are on display: another one starts them at
    // their top.
    private MessageId? headersOf;

    // The chips of the message on display (MessageChips).
    private readonly MessageChips chips;

    // The pane's conversation page (window.go convPageName), the pane only.
    private Conversation.ConversationView? conversation;

    // The quick-reply bar and its controller (the pane only).
    private readonly Malachi.Core.Controllers.QuickReplyController? quickReply;
    private readonly Conversation.ConversationQuickReplyBar? replyBar;
    private readonly EventHandler? onAssistantChanged;

    // The invitation card above the header; none in an attached message's view.
    private readonly InvitationCardPresenter? invitation;

    // The eWay-CRM bar under the header (Crm/CrmBarView); none in an
    // attached message's view.
    private readonly Crm.CrmBarView? crmBar;

    /// <summary>A view of <paramref name="mode"/>; <paramref name="commands"/> drive its command row (none for an attached message).</summary>
    public MessageView(ReaderMode mode, ReaderServices services, WindowCommands? commands)
    {
        ArgumentNullException.ThrowIfNull(services);
        this.services = services;
        this.commands = commands;
        logger = services.State.Logs.CreateLogger<MessageView>();
        Reader = new ReaderController(
            mode, services.Cache, services.FileTypes, logger: services.State.Logs.CreateLogger<ReaderController>())
        {
            IsDraft = mode == ReaderMode.Pane ? services.Router.IsDraft : null,
            FolderRoleOf = services.Router.FolderRoleOf,
            IssueSite = services.IssueSite,
            CanTransition = services.Issues.CanTransition,
            IssueBusy = services.Issues.IsBusy,
        };
        Reader.Toast = text => services.ToastIn(HostWindow, text);
        Reader.PropertyChanged += OnReaderChanged;
        Reader.ScrollToTopRequested += (_, _) => TextScroller.ChangeView(null, 0, null, disableAnimation: true);
        Reader.Rendered += OnRendered;
        Reader.Addresses.RowChanged += (_, kind) => FillAddressRow(kind);
        services.Attachments.SavingAllChanged += OnSavingAllChanged;
        InitializeComponent();
        UpdateBodyFit();
        chips = new MessageChips(services, this, () => HostWindow, () => FocusBody(), logger);

        // The issue card of a Jira message (issue_card.go): its menu acts on
        // the message on display, its key opens the issue in the browser.
        IssueCard.Issues = services.Issues;
        IssueCard.Subject = () => Reader.Current is { } shown ? Malachi.Core.Controllers.IssueActionsController.SubjectOf(shown) : null;
        IssueCard.OpenIssue = OpenIssue;
        if (mode != ReaderMode.Embedded)
        {
            invitation = new InvitationCardPresenter(InvitationCard, services.State, () => HostWindow)
            {
                OpenLink = OpenInvitationLink,
            };
        }
        WireCommands();
        if (mode != ReaderMode.Embedded)
        {
            // The bar of a message saved in eWay-CRM, last under the header
            // (macOS MessageViewController's crmBar), redrawn whenever the
            // application's CRM state changes.
            crmBar = new Crm.CrmBarView(new Thickness(12, 6, 12, 6));
            crmBar.OpenRequested += (_, url) => Crm.CrmWindowSupport.OpenUrl(services.State, HostWindow, url);
            Headers.Children.Add(crmBar);
            services.State.Crm.Changed += OnCrmChanged;
        }
        // GTK SetFocusOnClick(false): a click leaves the focus where it was,
        // since the bar goes away the moment the images are in; the keyboard
        // still reaches them.
        RemoteLoad.AllowFocusOnInteraction = false;
        RemoteTrust.AllowFocusOnInteraction = false;
        PicturesDownload.AllowFocusOnInteraction = false;
        Reader.HtmlReloadRequested += (_, _) =>
        {
            // The same HTML, whose malachi-cid: pictures have something to
            // serve now (their download ended).
            if (!closed && Reader.Html is { } html)
            {
                EnsureWeb().Load(html, reload: true);
            }
        };

        if (mode == ReaderMode.Pane)
        {
            // The quick-reply bar, last under the message stack.
            quickReply = new Malachi.Core.Controllers.QuickReplyController(new Conversation.QuickReplyHostAdapter(services, () => HostWindow));
            var bar = new Conversation.ConversationQuickReplyBar(quickReply)
            {
                FocusList = () => FocusList?.Invoke(),
                ListOnScreen = () => ListOnScreen?.Invoke() ?? true,
                OpenAssistant = anchor => (HostWindow as MainWindow)?.ShowAssistantFrom(anchor),
                AssistantShown = services.State.Assistant.Shown,
                KeyboardTarget = TakeKeyboardFromBar,
            };
            replyBar = bar;
            Grid.SetRow(bar, 7);
            Root.Children.Add(bar);
            onAssistantChanged = (_, _) => bar.AssistantShown = services.State.Assistant.Shown;
            services.State.Assistant.Changed += onAssistantChanged;
            SizeChanged += (_, e) => bar.SetPaneWidth(e.NewSize.Width);
        }

        var settings = services.State.Settings;
        ApplyBodyFont();
        tokens.Add(settings.OnChange(SettingsKey.TextZoom, ApplyBodyFont));
        tokens.Add(settings.OnChange(SettingsKey.MonospacePlainText, ApplyBodyFont));
    }

    /// <summary>What the view shows.</summary>
    public ReaderController Reader { get; }

    /// <summary>The Assistant's ✦ button of a message window's command row (message_window.blp assistant_button).</summary>
    public AppBarButton Assistant => AssistantButton;

    /// <summary>The eWay-CRM button of a message window's command row (message_window.blp crm_button).</summary>
    internal Crm.CrmToolbarButton CrmButton => CrmCommand;

    /// <summary>The window the view is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow { get; set; }

    /// <summary>The quick-reply bar's Escape (and the last resort of its keyboard): the message list takes the keyboard.</summary>
    internal Action? FocusList { get; set; }

    /// <summary>
    /// Whether the message list is on screen (MainWindow.MessageListOnScreen):
    /// the quick-reply bar's Escape gives it the keyboard only then; null
    /// counts as on screen.
    /// </summary>
    internal Func<bool>? ListOnScreen { get; set; }

    /// <summary>
    /// win.change-status / msg.change-status: pops up the Change Status menu
    /// of the card on display: the message's, or the issue card on top of the
    /// conversation shown.
    /// </summary>
    public bool OpenStatusMenu() =>
        Reader.Page == ReaderPage.Conversation ? conversation?.OpenStatusMenu() == true : IssueCard.OpenStatusMenu();

    /// <summary>
    /// The pane's conversation page (window.go conversationPane): shown in
    /// place of the message while the reader's page is
    /// <see cref="ReaderPage.Conversation"/>.
    /// </summary>
    internal void HostConversation(Conversation.ConversationView view)
    {
        ArgumentNullException.ThrowIfNull(view);
        conversation = view;
        view.Visibility = ReaderBind.PageVisible(Reader.Page, nameof(ReaderPage.Conversation));
        Pages.Children.Add(view);
        view.QuickReplyChanged += (_, _) =>
        {
            if (Reader.Page == ReaderPage.Conversation)
            {
                FeedQuickReply();
            }
        };
    }

    // The quick-reply bar follows the page shown (ReadingPaneViewController
    // show(_ row:), conversationChanged, showSingleReply): a single message,
    // the conversation (its id first: a new one has no target until its
    // model is built, and the empty model meanwhile), or nothing.
    private void FeedQuickReply()
    {
        if (quickReply is null || closed)
        {
            return;
        }
        switch (Reader.Page)
        {
            case ReaderPage.Message when Reader.Current is { } s && conversation is not null:
                {
                    var input = conversation.Controller.QuickReplyInput(s);
                    quickReply.Show(s.AccountId, s.ThreadId ?? new ThreadId(""));
                    quickReply.Update(input.Model, input.Actions, input.Account);
                    quickReply.SetShown(input.Shown);
                    return;
                }
            case ReaderPage.Conversation when conversation is not null:
                {
                    var ctrl = conversation.Controller;
                    quickReply.Show(ctrl.ThreadAccount, ctrl.Thread ?? new ThreadId(""));
                    if (ctrl.QuickReplyInput() is { } input)
                    {
                        quickReply.Update(input.Model, input.Actions, input.Account);
                    }
                    else
                    {
                        quickReply.Update(new ConversationModel(), default, null);
                    }
                    quickReply.SetShown(conversation.ChatShown);
                    return;
                }
        }
        quickReply.Show(null, new ThreadId(""));
        quickReply.SetShown(false);
    }

    // The bar hid with the keyboard in it (macOS keyboardTarget): the view
    // shown takes it, the message's body or the conversation's layout; the
    // list when neither can (never a collapsed control). After the turn: the
    // reader passes through the empty page on its way from a message to a
    // conversation (LeaveForConversation clears first), and the keyboard goes
    // to the page it lands on, not to the list for the empty page between.
    private void TakeKeyboardFromBar() => DispatcherQueue.TryEnqueue(GiveKeyboardToShownView);

    private void GiveKeyboardToShownView()
    {
        // Shown again within the turn (the bar came back): nothing to hand.
        if (closed || replyBar?.Visibility == Visibility.Visible)
        {
            return;
        }
        switch (Reader.Page)
        {
            case ReaderPage.Message when FocusBody():
                return;
            case ReaderPage.Conversation when conversation is not null && conversation.TakeKeyboard():
                return;
        }
        if (ListOnScreen?.Invoke() != false)
        {
            FocusList?.Invoke();
        }
    }

    // issue_card.go openKey: the issue in the browser (a URL of the
    // account's own site, the card checked it); a failure is a toast.
    private void OpenIssue(string url) => _ = OpenIssueAsync(url);

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

    /// <summary>
    /// Puts the keyboard in the body (message_window.blp focus-widget:
    /// message_scroller), so the selectable subject is not what has it.
    /// False when nothing could take it: the text scroller is collapsed
    /// while the body loads, failed or is HTML without a viewer yet, and
    /// a collapsed control refuses the focus silently.
    /// </summary>
    public bool FocusBody()
    {
        if (Reader.BodyPage == ReaderBodyPage.Html && web is not null)
        {
            web.FocusPage();
            return true;
        }
        return TextScroller.Visibility == Visibility.Visible && TextScroller.Focus(FocusState.Programmatic);
    }

    /// <summary>
    /// The owning window closed: the settings are let go, the spinner's
    /// timer and late replies dropped, the viewer released.
    /// </summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        services.Attachments.SavingAllChanged -= OnSavingAllChanged;
        if (onAssistantChanged is not null)
        {
            services.State.Assistant.Changed -= onAssistantChanged;
        }
        replyBar?.Close();
        if (crmBar is not null)
        {
            services.State.Crm.Changed -= OnCrmChanged;
        }
        Reader.Close();
        web?.Close();
        conversation?.Close();
        invitation?.Close();
    }

    // The command row: the window's commands (window.blp's action-name and
    // the msg.* group of message_window.go), none for an attached message.
    // In the main window's pane the row is the main window's own
    // MessageCommandBar, level with the other panes' header rows as
    // window.blp's message header bar is, so the view shows none there.
    private void WireCommands()
    {
        if (commands is not null)
        {
            // The No Accounts page's button (window.blp no_accounts_page,
            // app.add-account), which only the pane shows. Its click and
            // enabled state are wired, not a Command: a XamlUICommand
            // would replace the label with its own empty one.
            Main.CommandBinding.Bind(AddAccountButton, commands.AddAccount);
            Main.CommandBinding.Bind(AddJiraAccountButton, commands.AddJiraAccount);
            Main.CommandBinding.Bind(AddCalDAVCalendarButton, commands.AddCalDAVCalendar);
        }
        if (commands is null || Reader.Mode == ReaderMode.Pane)
        {
            CommandRow.Visibility = Visibility.Collapsed;
            return;
        }
        ReplyButton.Command = commands.Reply.Command;
        ReplyAllButton.Command = commands.ReplyAll.Command;
        ForwardButton.Command = commands.Forward.Command;
        TrashButton.Command = commands.Trash.Command;
        ArchiveButton.Command = commands.Archive.Command;
        // A toggle is not given the command: its click flips it before the
        // command runs, so the click runs the command and the button then
        // shows the flags (as the main window's MessageCommandBar).
        StarButton.Click += (_, _) =>
        {
            commands.ToggleFlag.TryExecute();
            ShowFlags();
        };
        JunkItem.Command = commands.Junk.Command;
        MarkUnreadItem.Command = commands.MarkUnread.Command;
        MarkReadItem.Command = commands.MarkRead.Command;
        LoadImagesItem.Command = commands.LoadImages.Command;
        TrustSenderItem.Command = commands.TrustSender.Command;
        ChangeStatusItem.Command = commands.ChangeStatus.Command;
        commands.ChangeStatus.Command.CanExecuteChanged += (_, _) => ShowFlags();
        // The star shows the flagged state and the trash button says what it
        // does (actions.go setStar, outbox.go trashTooltip): both follow the
        // flags, whose change re-validates these commands.
        commands.ToggleFlag.Command.CanExecuteChanged += (_, _) => ShowFlags();
        commands.Trash.Command.CanExecuteChanged += (_, _) => ShowFlags();
        ShowFlags();
    }

    private void ShowFlags()
    {
        if (commands is null)
        {
            return;
        }
        var f = commands.Flags;
        var star = f.Flagged ? L10n.T("Unstar") : L10n.T("Star");
        StarButton.IsEnabled = commands.ToggleFlag.IsEnabled;
        StarButton.IsChecked = f.Flagged;
        StarButton.Label = star;
        StarGlyph.Glyph = Icons.Glyph(f.Flagged ? "starred" : "non-starred");
        ToolTipService.SetToolTip(StarButton, star);
        var trash = Outbox.TrashTooltip(f.Outbox);
        TrashButton.Label = trash;
        ToolTipService.SetToolTip(TrashButton, trash);
        // message_window.go: the capabilities' visibility and presentReply.
        var reply = Main.ActionPresentation.ReplyLabel(f);
        ReplyButton.Label = reply;
        ReplyGlyph.Glyph = Main.ActionPresentation.ReplyGlyph(f);
        ToolTipService.SetToolTip(ReplyButton, reply);
        ReplyButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.Reply);
        ReplyAllButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.ReplyAll);
        ForwardButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.Forward);
        TrashButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.Trash);
        ArchiveButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.Archive);
        var status = commands.ChangeStatus.IsEnabled ? Visibility.Visible : Visibility.Collapsed;
        ChangeStatusItem.Visibility = status;
        ChangeStatusSeparator.Visibility = status;
    }

    private void OnReaderChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (closed)
        {
            return;
        }
        switch (e.PropertyName)
        {
            case nameof(ReaderController.BodyPage):
                UpdateBodyFit();
                break;
            case nameof(ReaderController.Html):
                if (Reader.Html is { } html)
                {
                    EnsureWeb().Load(html);
                }
                else
                {
                    web?.Clear(); // drop the pictures of the message before
                }
                break;
            case nameof(ReaderController.Bulk):
            case nameof(ReaderController.BulkBusy):
                ShowBulk();
                break;
            case nameof(ReaderController.RemoteBarVisible) when !Reader.RemoteBarVisible:
            case nameof(ReaderController.RemoteBarLoading) when Reader.RemoteBarLoading:
                // Hiding the button that has the focus would hand it on to
                // whatever comes next: the body is somewhere harmless
                // (message_view.go setBarVisible, setBarLoading).
                MoveFocusOutOf(RemoteBar);
                break;
            case nameof(ReaderController.PicturesBarVisible) when !Reader.PicturesBarVisible:
            case nameof(ReaderController.PicturesBarLoading) when Reader.PicturesBarLoading:
                // The same for the pictures bar (remote.go showPicturesBar).
                MoveFocusOutOf(PicturesBar);
                break;
            case nameof(ReaderController.Chips):
            case nameof(ReaderController.SaveAll):
                FillAttachments();
                break;
            case nameof(ReaderController.QuotedOffer):
                if (Reader.QuotedOffer is null)
                {
                    MoveFocusOutOf(QuotedButton);
                }
                QuotedButton.Show(Reader.QuotedOffer);
                QuotedRow.Visibility = QuotedButton.Visibility;
                break;
            case nameof(ReaderController.Page):
                if (Reader.Page != ReaderPage.Message)
                {
                    headersOf = null; // the next message, even the same one, starts at the top
                    invitation?.Clear();
                }
                if (conversation is not null)
                {
                    conversation.Visibility = ReaderBind.PageVisible(Reader.Page, nameof(ReaderPage.Conversation));
                }
                FadeIn(Reader.Page switch
                {
                    ReaderPage.Empty => EmptyPage,
                    ReaderPage.NoAccounts => NoAccountsPage,
                    ReaderPage.Selection => SelectionPage,
                    ReaderPage.Conversation when conversation is not null => conversation,
                    _ => MessagePage,
                });
                FeedQuickReply();
                break;
        }
    }

    // The bulk strip (bulk.go showBulk): the focus leaves the button before
    // it or the strip goes away, for the reason given at the remote bar.
    private void ShowBulk()
    {
        if (Reader.Mode == ReaderMode.Embedded)
        {
            return;
        }
        if (BulkStripView.LosesButton(Reader.Bulk) && BulkBar.OwnsFocus)
        {
            FocusBody();
        }
        BulkBar.Show(Reader.Bulk, Reader.BulkBusy);
    }

    // The strip's Unsubscribe (bulk.go unsubscribe over this view's message):
    // the question, then the daemon or the sender's page; what comes of it
    // is said in this view's window.
    private void OnBulkClick(object? sender, EventArgs e)
    {
        if (Reader.Mode == ReaderMode.Embedded || Reader.Current is not { } s)
        {
            return;
        }
        var window = HostWindow;
        services.Bulk.Unsubscribe(s.Id, window, text => services.ToastIn(window, text));
    }

    // The "•••" under the body: its quoted history shows, or goes again
    // (MessageViewController.swift toggleQuoted).
    private void OnQuotedClick(object sender, RoutedEventArgs e) => Reader.ToggleQuoted();

    // message_stack's crossfade (0.2 s): the page coming in fades in; its
    // Visibility follows the binding, which runs after this handler.
    private void FadeIn(UIElement page)
    {
        page.Opacity = 0;
        DispatcherQueue.TryEnqueue(() => page.Opacity = 1);
    }

    // Another message's headers start at their top, as a new page of GTK's
    // box would; a re-render of the same one leaves them where they are.
    // The eWay-CRM bar of the message on display (CrmState.Record: the
    // loaded message's record, or what a save or removal just reported);
    // none without an enabled eWay-CRM account (macOS renderCRM).
    private void RenderCrm()
    {
        if (crmBar is null || closed)
        {
            return;
        }
        var crm = services.State.Crm;
        crmBar.Show(crm.Account is not null && Reader.Current is { } s ? crm.Record(s) : null);
    }

    private void OnCrmChanged(object? sender, EventArgs e) => RenderCrm();

    private void OnRendered(object? sender, ReaderRender r)
    {
        if (closed)
        {
            return;
        }
        // Another message on the same page (no change of the page), or the
        // same one with what loaded since: the bar answers it.
        FeedQuickReply();
        RenderInvitation(r);
        RenderCrm();
        if (headersOf == r.Summary.Id)
        {
            return;
        }
        headersOf = r.Summary.Id;
        HeaderScroller.ChangeView(null, 0, null, disableAnimation: true);
    }

    // renderInvitation: the card of message.get and the account's switch;
    // an attached message has none.
    private void RenderInvitation(ReaderRender r)
    {
        if (invitation is null || Reader.Mode == ReaderMode.Embedded)
        {
            return;
        }
        invitation.Show(r.Summary, r.Loaded?.Msg, InvitationReading.ShowsInvitationCard(services.AccountOf(r.Summary.AccountId)));
    }

    // openInvitationLink: Join Video Call through the link confirmation of
    // the view's window, as a link the daemon did not list.
    private void OpenInvitationLink(string url) => _ = services.Links.OpenAsync(new ActivatedLink(url, url), [], HostWindow);

    // The headers' share of the page: past it they scroll.
    private void OnMessagePageSizeChanged(object sender, SizeChangedEventArgs e) =>
        HeaderScroller.MaxHeight = Math.Max(0, Math.Floor(e.NewSize.Height * HeaderShare));

    /// <summary>
    /// The body on display is drawn: the text, or an HTML body whose web
    /// view has finished loading (the <c>shot=</c> step of
    /// MainWindow.DevStart.cs waits for it; a cold WebView2 takes seconds).
    /// </summary>
    internal bool DevelopmentBodyShown => web is null || !web.IsLoading;

    // htmlView: made on first use, so a plain-text mailbox never starts a
    // web process; one per view, reused.
    private MessageWebView EnsureWeb()
    {
        if (web is not null)
        {
            return web;
        }
        var view = new MessageWebView();
        view.UseCache(services.Cache);
        view.Zoom = services.State.Settings.TextZoom;
        view.LinkActivated += (_, link) => _ = services.Links.OpenAsync(link, Reader.Links, HostWindow);
        view.Unavailable += (_, _) => Reader.HtmlUnavailable();
        view.ContentHeightChanged += (_, _) => UpdateBodyFit();
        HtmlSlot.Children.Add(view);
        web = view;
        return view;
    }

    // The body area's height for the page on display (message_view_fit.go
    // bodyFit.apply): the text's, the document's while it is known, the
    // whole area otherwise; the "•••" follows it (BodyFitPanel).
    private void UpdateBodyFit()
    {
        var html = web?.ContentHeight;
        BodyFitArea.Mode = BodyFit.ModeFor(Reader.BodyPage, html);
        BodyFitArea.FitHeight = html ?? 0;
    }

    // The text-zoom and monospace settings, for the text body and the viewer
    // (style.go .message-body).
    private void ApplyBodyFont()
    {
        var settings = services.State.Settings;
        BodyLabel.FontSize = BodyFontSize * settings.TextZoom / 100.0;
        if (settings.MonospacePlainText && Application.Current.Resources.TryGetValue("MonospaceFontFamily", out var font) && font is FontFamily mono)
        {
            BodyLabel.FontFamily = mono;
        }
        else
        {
            BodyLabel.ClearValue(TextBlock.FontFamilyProperty);
        }
        if (web is not null)
        {
            web.Zoom = settings.TextZoom;
        }
    }

    private void MoveFocusOutOf(DependencyObject bar)
    {
        if (XamlRoot is null || FocusManager.GetFocusedElement(XamlRoot) is not DependencyObject focused || !IsInside(focused, bar))
        {
            return;
        }
        FocusBody();
    }

    private static bool IsInside(DependencyObject element, DependencyObject container)
    {
        for (var e = element; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (ReferenceEquals(e, container))
            {
                return true;
            }
        }
        return false;
    }

    // message_menu: the More Actions button opens the message menu.
    private void OnMore(object sender, RoutedEventArgs e) => FlyoutBase.ShowAttachedFlyout(MoreButton);

    // The banners and the bar act on the message on display.

    private void OnRetry(object sender, RoutedEventArgs e)
    {
        if (Reader.Current is { } s)
        {
            services.Router.RetryOutbox(s.Id);
        }
    }

    private void OnEditDraft(object sender, RoutedEventArgs e)
    {
        if (Reader.Current is { } s)
        {
            services.Router.EditDraft(s.Id);
        }
    }

    private void OnLoadImages(object sender, RoutedEventArgs e)
    {
        if (Reader.Mode == ReaderMode.Embedded)
        {
            _ = Reader.LoadEmbeddedImagesAsync();
            return;
        }
        if (Reader.Current is { } s)
        {
            services.Router.LoadImages(s.Id);
        }
    }

    private void OnTrustSender(object sender, RoutedEventArgs e)
    {
        if (Reader.Current is { } s)
        {
            services.Router.TrustSender(s.Id);
        }
    }

    // The pictures bar's Download Pictures (remote.go downloadPictures): a
    // failure is said in this view's window.
    private void OnDownloadPictures(object sender, RoutedEventArgs e)
    {
        if (Reader.Mode == ReaderMode.Embedded || Reader.Current is not { } s)
        {
            return;
        }
        var window = HostWindow;
        services.Router.DownloadPictures(s.Id, text => services.ToastIn(window, text));
    }

    // Address rows (addresses.go fill) and attachment chips
    // (attachments.go renderAttachments): MessageChips, shared with the
    // cards of the conversation view.

    private (TextBlock Label, WrapBox Box) RowOf(AddressRowKind kind) => kind switch
    {
        AddressRowKind.From => (FromLabel, FromChips),
        AddressRowKind.To => (ToLabel, ToChips),
        _ => (CcLabel, CcChips),
    };

    private void FillAddressRow(AddressRowKind kind)
    {
        var (label, box) = RowOf(kind);
        chips.FillAddressRow(Reader.Addresses.Row(kind), label, box, Reader.Addresses.Expand, () => RowOf(kind).Box);
    }

    private void FillAttachments() =>
        chips.FillAttachments(AttachmentChips, Reader.Chips, Reader.SaveAll, Reader.SaveAllRemote, Reader.Current);

    private void OnSavingAllChanged(object? sender, MessageId id)
    {
        if (!closed)
        {
            chips.SavingAllChanged(id);
        }
    }
    [LoggerMessage(Level = LogLevel.Warning, Message = "opening an issue failed: {Kind}")]
    private static partial void LogOpenIssueFailed(ILogger logger, string kind);

}
