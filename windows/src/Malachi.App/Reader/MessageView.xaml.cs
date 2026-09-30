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

    // Copy Address: another program may hold the clipboard open for a moment
    // (a clipboard manager, a remote desktop session); tried this many times,
    // this far apart, as WinForms' Clipboard.SetDataObject does.
    private const int ClipboardAttempts = 5;
    private static readonly TimeSpan ClipboardRetryDelay = TimeSpan.FromMilliseconds(50);

    private readonly ReaderServices services;
    private readonly WindowCommands? commands;
    private readonly ILogger logger;
    private readonly List<SettingsChangeToken> tokens = [];
    private MessageWebView? web;
    private bool closed;

    // The message whose headers are on display: another one starts them at
    // their top.
    private MessageId? headersOf;

    // Save All of the message on display, following AttachmentOpener's run.
    private Button? saveAllButton;
    private MessageId? saveAllOf;

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

        // The issue card of a Jira message (issue_card.go): its menu acts on
        // the message on display, its key opens the issue in the browser.
        IssueCard.Issues = services.Issues;
        IssueCard.Subject = () => Reader.Current is { } shown ? Malachi.Core.Controllers.IssueActionsController.SubjectOf(shown) : null;
        IssueCard.OpenIssue = OpenIssue;
        WireCommands();
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

        var settings = services.State.Settings;
        ApplyBodyFont();
        tokens.Add(settings.OnChange(SettingsKey.TextZoom, ApplyBodyFont));
        tokens.Add(settings.OnChange(SettingsKey.MonospacePlainText, ApplyBodyFont));
    }

    /// <summary>What the view shows.</summary>
    public ReaderController Reader { get; }

    /// <summary>The Assistant's ✦ button of a message window's command row (message_window.blp assistant_button).</summary>
    public AppBarButton Assistant => AssistantButton;

    /// <summary>The window the view is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow { get; set; }

    /// <summary>win.change-status / msg.change-status: pops up the Change Status menu of the card on display.</summary>
    public bool OpenStatusMenu() => IssueCard.OpenStatusMenu();

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
    /// </summary>
    public void FocusBody()
    {
        if (Reader.BodyPage == ReaderBodyPage.Html && web is not null)
        {
            web.FocusPage();
        }
        else
        {
            TextScroller.Focus(FocusState.Programmatic);
        }
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
        Reader.Close();
        web?.Close();
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
        JunkButton.Command = commands.Junk.Command;
        ArchiveButton.Command = commands.Archive.Command;
        // A toggle is not given the command: its click flips it before the
        // command runs, so the click runs the command and the button then
        // shows the flags (as the main window's MessageCommandBar).
        StarButton.Click += (_, _) =>
        {
            commands.ToggleFlag.TryExecute();
            ShowFlags();
        };
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
        JunkButton.Visibility = Main.ActionPresentation.Shown(f, MessageActionKind.Junk);
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
            case nameof(ReaderController.Page):
                if (Reader.Page != ReaderPage.Message)
                {
                    headersOf = null; // the next message, even the same one, starts at the top
                }
                FadeIn(Reader.Page switch
                {
                    ReaderPage.Empty => EmptyPage,
                    ReaderPage.NoAccounts => NoAccountsPage,
                    _ => MessagePage,
                });
                break;
        }
    }

    // message_stack's crossfade (0.2 s): the page coming in fades in; its
    // Visibility follows the binding, which runs after this handler.
    private void FadeIn(UIElement page)
    {
        page.Opacity = 0;
        DispatcherQueue.TryEnqueue(() => page.Opacity = 1);
    }

    // Another message's headers start at their top, as a new page of GTK's
    // box would; a re-render of the same one leaves them where they are.
    private void OnRendered(object? sender, ReaderRender r)
    {
        if (closed || headersOf == r.Summary.Id)
        {
            return;
        }
        headersOf = r.Summary.Id;
        HeaderScroller.ChangeView(null, 0, null, disableAnimation: true);
    }

    // The headers' share of the page: past it they scroll.
    private void OnMessagePageSizeChanged(object sender, SizeChangedEventArgs e) =>
        HeaderScroller.MaxHeight = Math.Max(0, Math.Floor(e.NewSize.Height * HeaderShare));

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
        HtmlSlot.Children.Add(view);
        web = view;
        return view;
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

    // Address rows (addresses.go fill).

    private (TextBlock Label, WrapBox Box) RowOf(AddressRowKind kind) => kind switch
    {
        AddressRowKind.From => (FromLabel, FromChips),
        AddressRowKind.To => (ToLabel, ToChips),
        _ => (CcLabel, CcChips),
    };

    private void FillAddressRow(AddressRowKind kind)
    {
        var row = Reader.Addresses.Row(kind);
        var (label, box) = RowOf(kind);
        // A chip about to go may hold the focus: the body is somewhere
        // harmless to put it.
        if (XamlRoot is not null && FocusManager.GetFocusedElement(XamlRoot) is DependencyObject focused && IsInside(focused, box))
        {
            FocusBody();
        }
        box.Children.Clear();
        foreach (var chip in row.Chips)
        {
            box.Children.Add(AddressButton(chip));
        }
        if (row.More > 0)
        {
            box.Children.Add(MoreChip(kind, row));
        }
        label.Visibility = ReaderBind.Visible(row.Visible);
        box.Visibility = ReaderBind.Visible(row.Visible);
    }

    // addresses.go chip: the name on a pill, the whole address as its
    // tooltip; a click opens the menu, built the first time it opens.
    private Button AddressButton(AddressChip chip)
    {
        var button = new Button
        {
            Content = new TextBlock { Text = chip.Label, TextWrapping = TextWrapping.NoWrap },
            Style = Look("AddressChipStyle"),
        };
        AutomationProperties.SetName(button, Format.FormatAddress(chip.Address));
        if (chip.Tooltip is { } tip)
        {
            ToolTipService.SetToolTip(button, tip);
        }
        var menu = new MenuFlyout { Placement = FlyoutPlacementMode.BottomEdgeAlignedLeft };
        menu.Opening += (_, _) =>
        {
            if (menu.Items.Count == 0)
            {
                FillAddressMenu(menu, chip);
            }
        };
        button.Flyout = menu;
        return button;
    }

    // addresses.go addressMenu: the name and the address on top, as text,
    // then Copy Address and New Message.
    private void FillAddressMenu(MenuFlyout menu, AddressChip chip)
    {
        if (chip.MenuName.Length > 0)
        {
            menu.Items.Add(new MenuFlyoutItem { Text = chip.MenuName, IsEnabled = false, FontWeight = FontWeights.SemiBold });
        }
        if (chip.MenuAddress.Length > 0)
        {
            menu.Items.Add(new MenuFlyoutItem { Text = chip.MenuAddress, IsEnabled = false });
        }
        if (menu.Items.Count > 0)
        {
            menu.Items.Add(new MenuFlyoutSeparator());
        }
        var copy = new MenuFlyoutItem { IsEnabled = chip.CanAct };
        MnemonicLabel.Apply(copy, L10n.T("_Copy Address"));
        copy.Click += (_, _) => _ = CopyAddressAsync(chip);
        var write = new MenuFlyoutItem { IsEnabled = chip.CanAct };
        MnemonicLabel.Apply(write, L10n.T("_New Message"));
        write.Click += (_, _) => services.Router.NewMessage(chip.Address, chip.Account);
        menu.Items.Add(copy);
        menu.Items.Add(write);
    }

    // The chip's Copy Address: the bare address on the clipboard, and a
    // toast that says so. The clipboard is the system's: while another
    // program holds it open, SetContent throws (CLIPBRD_E_CANT_OPEN), which in
    // a menu item's handler would end the application. It is tried again a
    // few times; failing that, a toast says so and the log has the kind only,
    // never the address. Flushed, so the address stays on the clipboard after
    // the application quits.
    private async Task CopyAddressAsync(AddressChip chip)
    {
        if (!chip.CanAct)
        {
            return;
        }
        var window = HostWindow;
        for (var attempt = 1; ; attempt++)
        {
            try
            {
                var package = new DataPackage();
                package.SetText(chip.Email);
                Clipboard.SetContent(package);
                break;
            }
            catch (Exception e) when (e is COMException or UnauthorizedAccessException)
            {
                if (attempt >= ClipboardAttempts)
                {
                    LogCopyFailed(logger, e.GetType().Name, e.HResult);
                    // Windows-only string: GTK's clipboard cannot refuse.
                    services.ToastIn(window, L10n.T("The address could not be copied"));
                    return;
                }
            }
            await Task.Delay(ClipboardRetryDelay);
        }
        try
        {
            Clipboard.Flush();
        }
        catch (Exception e) when (e is COMException or UnauthorizedAccessException)
        {
            // On the clipboard all the same, until the application quits.
            LogFlushFailed(logger, e.GetType().Name, e.HResult);
        }
        services.ToastIn(window, L10n.T("Address copied"));
    }

    // addresses.go moreChip: "+N more" unfolds every line of the message; a
    // click leaves the focus alone, from the keyboard the focus moves on to
    // the first chip the unfold revealed.
    private Button MoreChip(AddressRowKind kind, AddressRow row)
    {
        var button = new Button
        {
            Content = row.MoreText,
            Style = Look("MoreChipStyle"),
            AllowFocusOnInteraction = false,
        };
        var at = row.Chips.Count;
        button.Click += (_, _) =>
        {
            var keyboard = button.FocusState == FocusState.Keyboard;
            Reader.Addresses.Expand();
            var box = RowOf(kind).Box;
            if (keyboard && at < box.Children.Count && box.Children[at] is Control next)
            {
                next.Focus(FocusState.Keyboard);
            }
        };
        return button;
    }

    // Attachment chips (attachments.go renderAttachments, buildChip,
    // buildSaveAll).

    // The chip that holds the focus (one used a moment ago, whose download
    // starts or ends now) goes with the rest; the focus goes to the chip in
    // its place, from the keyboard when it came from the keyboard, or to the
    // body when there is none there that can take it (renderAttachments,
    // MessageViewController.swift renderAttachments). The new chips are
    // added before the old ones go, so the focus moves from the old chip
    // straight to its successor, never to what WinUI would pick for a
    // focused element that leaves the tree.
    private void FillAttachments()
    {
        var chips = AttachmentChips.Children;
        var (focusAt, how) = FocusedChip();
        var old = chips.Count;
        saveAllButton = null;
        saveAllOf = null;
        var icons = services.Icons.StartBatch();
        foreach (var chip in Reader.Chips)
        {
            chips.Add(AttachmentButton(chip, icons));
        }
        if (Reader.SaveAll.Count > 0 && Reader.Current is { } s)
        {
            saveAllButton = SaveAllButton(s, Reader.SaveAll, Reader.SaveAllRemote);
            saveAllOf = s.Id;
            chips.Add(saveAllButton);
        }
        if (focusAt >= 0 && !(old + focusAt < chips.Count && chips[old + focusAt] is Control next && next.Focus(how)))
        {
            // A disabled chip (on a wrapper) takes no focus, and fewer
            // chips may leave none in its place.
            FocusBody();
        }
        for (var i = 0; i < old; i++)
        {
            chips.RemoveAt(0);
        }
        AttachmentChips.Visibility = ReaderBind.Visible(chips.Count > 0);
    }

    // The position of the chip (or Save All) that holds the focus, and how
    // to give it to the one in its place (attachments.go focusedChip): -1
    // when none does.
    private (int At, FocusState How) FocusedChip()
    {
        if (XamlRoot is null || FocusManager.GetFocusedElement(XamlRoot) is not DependencyObject focused)
        {
            return (-1, FocusState.Unfocused);
        }
        for (var e = focused; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (VisualTreeHelper.GetParent(e) is { } parent && ReferenceEquals(parent, AttachmentChips) && e is UIElement chip)
            {
                var how = focused is Control { FocusState: FocusState.Keyboard } ? FocusState.Keyboard : FocusState.Programmatic;
                return (AttachmentChips.Children.IndexOf(chip), how);
            }
        }
        return (-1, FocusState.Unfocused);
    }

    // One attachment: the click previews it (an attached message opens in
    // its own window), the arrow offers View, Open and Save As…. The
    // actions close over the chip, so it never acts on another message; a
    // chip of a part on the mail server downloads the message first.
    private FrameworkElement AttachmentButton(AttachmentChip chip, IconLookups<ImageSource>.Batch icons)
    {
        var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
        var icon = ChipIcons.For(icons, chip.Attachment.Filename);
        icon.VerticalAlignment = VerticalAlignment.Center;
        content.Children.Add(icon);
        content.Children.Add(new TextBlock { Text = chip.Label, Style = Look("ChipNameStyle") });
        if (chip.SizeText.Length > 0)
        {
            content.Children.Add(new TextBlock { Text = chip.SizeText, Style = Look("ChipSizeStyle") });
        }
        if (chip.OnServer)
        {
            content.Children.Add(RemoteIndicator(chip));
        }
        var button = new SplitButton
        {
            Content = content,
            // The SplitButton's own style is not public: set, not based on.
            Padding = new Thickness(8, 3, 8, 3),
            MinHeight = 0,
            IsEnabled = chip.Available,
        };
        AutomationProperties.SetName(button, chip.Name);
        // The reason of a chip that cannot be used is "" until message.body
        // answered: no tooltip, rather than an empty one.
        var tooltip = chip.Tooltip.Length > 0 ? chip.Tooltip : null;
        if (tooltip is not null)
        {
            AutomationProperties.SetHelpText(button, tooltip);
        }

        var menu = new MenuFlyout { Placement = FlyoutPlacementMode.BottomEdgeAlignedLeft };
        if (chip.Nested)
        {
            var view = new MenuFlyoutItem();
            MnemonicLabel.Apply(view, L10n.T("_View"));
            view.Click += (_, _) => OpenAttached(chip);
            menu.Items.Add(view);
        }
        var open = new MenuFlyoutItem { IsEnabled = chip.CanOpen };
        MnemonicLabel.Apply(open, L10n.T("_Open"));
        open.Click += (_, _) => _ = services.Attachments.OpenAsync(chip.Attachment, chip.Message, chip.OnServer, HostWindow);
        var save = new MenuFlyoutItem();
        MnemonicLabel.Apply(save, L10n.T("Save _As…"));
        save.Click += (_, _) => _ = services.Attachments.SaveAsAsync(chip.Attachment, chip.Message, chip.OnServer, HostWindow);
        menu.Items.Add(open);
        // assistant.go bindAskItem: "Ask the Assistant…" while the Assistant
        // is shown, enabled while the chosen target can take the file (for
        // In App, a type the bridge reads); looked at again as the menu opens.
        var ask = new MenuFlyoutItem { Text = Core.Assistants.Assistant.Texts().AskFile };
        ask.Click += (_, _) => services.State.MainWindow?.AssistantActions?.AskAboutAttachment(chip.Attachment, chip.Message, chip.OnServer, HostWindow);
        menu.Opening += (_, _) =>
        {
            var assistant = services.State.Assistant;
            assistant.RefreshHandlers();
            var shown = assistant.Shown;
            if (shown && !menu.Items.Contains(ask))
            {
                menu.Items.Insert(menu.Items.IndexOf(open) + 1, ask);
            }
            else if (!shown)
            {
                menu.Items.Remove(ask);
            }
            ask.IsEnabled = assistant.CanAskFile(chip.Attachment.ContentType);
        };
        menu.Items.Add(save);
        button.Flyout = menu;
        button.Click += (_, _) =>
        {
            if (chip.Nested)
            {
                OpenAttached(chip);
            }
            else
            {
                _ = services.Preview.ShowAsync(chip.Attachment, chip.Message, chip.OnServer, HostWindow);
            }
        };
        if (chip.Available)
        {
            if (tooltip is not null)
            {
                ToolTipService.SetToolTip(button, tooltip);
            }
            return button;
        }
        // A disabled control shows no tooltip: the reason goes on a wrapper.
        var wrapper = new Border { Child = button, Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        if (tooltip is not null)
        {
            ToolTipService.SetToolTip(wrapper, tooltip);
        }
        return wrapper;
    }

    // attachments.go remoteIndicator: after the size of a part on the mail
    // server only, the server glyph with the reason as its tooltip, dimmed
    // like the size, or a spinner while the message is being downloaded.
    private static FrameworkElement RemoteIndicator(AttachmentChip chip)
    {
        if (chip.Downloading)
        {
            var spinner = new ProgressRing
            {
                Width = 14,
                Height = 14,
                IsActive = true,
                VerticalAlignment = VerticalAlignment.Center,
            };
            AutomationProperties.SetName(spinner, L10n.T("Downloading…"));
            return spinner;
        }
        var glyph = Icons.Create("network-server", Icons.Small);
        glyph.VerticalAlignment = VerticalAlignment.Center;
        glyph.Opacity = 0.55; // GTK's .dim-label
        ToolTipService.SetToolTip(glyph, chip.ServerTooltip);
        AutomationProperties.SetName(glyph, chip.ServerTooltip);
        return glyph;
    }

    // A style of the view's resources (MessageView.xaml).
    private Style Look(string key) => (Style)Resources[key];

    private void OpenAttached(AttachmentChip chip) =>
        _ = services.Windows.OpenEmbeddedAsync(chip.Message, chip.Attachment, chip.OnServer, HostWindow);

    // buildSaveAll: flat, as dense as the chips beside it; disabled while
    // the run lasts, which AttachmentOpener keeps by message, so a button
    // rebuilt by a re-render, or the same message's in another window, is
    // disabled too. remote: some are on the mail server only, the message
    // is downloaded once first.
    private Button SaveAllButton(MessageSummary s, IReadOnlyList<Attachment> atts, bool remote)
    {
        var mnemonic = Mnemonic.Parse(L10n.T("Save _All"));
        var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
        content.Children.Add(Icons.Create("document-save", Icons.Small));
        content.Children.Add(new TextBlock { Text = mnemonic.Label, Style = Look("ChipNameStyle") });
        var button = new Button
        {
            Content = content,
            Style = Look("ChipActionStyle"),
            AccessKey = mnemonic.AccessKey ?? "",
            IsEnabled = !services.Attachments.IsSavingAll(s.Id),
        };
        AutomationProperties.SetName(button, mnemonic.Label);
        AutomationProperties.SetAutomationId(button, "SaveAllButton");
        button.Click += (_, _) => _ = services.Attachments.SaveAllAsync(atts, s, remote, HostWindow);
        return button;
    }

    private void OnSavingAllChanged(object? sender, MessageId id)
    {
        if (!closed && saveAllButton is { } button && saveAllOf == id)
        {
            button.IsEnabled = !services.Attachments.IsSavingAll(id);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "opening an issue failed: {Kind}")]
    private static partial void LogOpenIssueFailed(ILogger logger, string kind);

    [LoggerMessage(Level = LogLevel.Warning, Message = "copying an address failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogCopyFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "flushing the clipboard failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogFlushFailed(ILogger logger, string kind, int hResult);
}
