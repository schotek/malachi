// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/MessageViewController.swift
// (the AppKit half: the pages, the web view made on first use, the fonts,
// the chips and the address rows built from the controller, the focus
// leaving the bar), AddressHeaderView.swift (the chips and their menu, "+N
// more"), AttachmentChipView.swift (the chip, its menu, Save All) and
// RemoteBarView.swift; GTK: ui/internal/window/message_view.go
// (newMessageView, setBarVisible, setBarLoading, htmlView, setZoom),
// addresses.go (chip, addressMenu, moreChip), attachments.go (buildChip,
// chipMenu, buildSaveAll) and style.go (.message-body: the text-zoom and
// monospace settings). The state is Core's ReaderController; this view
// draws it and sends the clicks back: to the window's commands (the
// command row and its menu), the MessageActionRouter (the banners, the
// bar, an address's New Message), the AttachmentOpener and previewer (the
// chips), the LinkOpener (the viewer's links) and the registry (an
// attached message's window). Every text of a message is set through a
// Text property; no markup is ever parsed.
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
// button (the run is AttachmentOpener's, by message).

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
        };
        Reader.Toast = text => services.ToastIn(HostWindow, text);
        Reader.PropertyChanged += OnReaderChanged;
        Reader.ScrollToTopRequested += (_, _) => TextScroller.ChangeView(null, 0, null, disableAnimation: true);
        Reader.Rendered += OnRendered;
        Reader.Addresses.RowChanged += (_, kind) => FillAddressRow(kind);
        services.Attachments.SavingAllChanged += OnSavingAllChanged;
        InitializeComponent();

        WireCommands();
        // GTK SetFocusOnClick(false): a click leaves the focus where it was,
        // since the bar goes away the moment the images are in; the keyboard
        // still reaches them.
        RemoteLoad.AllowFocusOnInteraction = false;
        RemoteTrust.AllowFocusOnInteraction = false;

        var settings = services.State.Settings;
        ApplyBodyFont();
        tokens.Add(settings.OnChange(SettingsKey.TextZoom, ApplyBodyFont));
        tokens.Add(settings.OnChange(SettingsKey.MonospacePlainText, ApplyBodyFont));
    }

    /// <summary>What the view shows.</summary>
    public ReaderController Reader { get; }

    /// <summary>The window the view is in: where its dialogs and toasts go.</summary>
    public Window? HostWindow { get; set; }

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
        StarButton.Command = commands.ToggleFlag.Command;
        MarkUnreadItem.Command = commands.MarkUnread.Command;
        MarkReadItem.Command = commands.MarkRead.Command;
        LoadImagesItem.Command = commands.LoadImages.Command;
        TrustSenderItem.Command = commands.TrustSender.Command;
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
        StarButton.Label = star;
        StarGlyph.Glyph = Icons.Glyph(f.Flagged ? "starred" : "non-starred");
        ToolTipService.SetToolTip(StarButton, star);
        var trash = Outbox.TrashTooltip(f.Outbox);
        TrashButton.Label = trash;
        ToolTipService.SetToolTip(TrashButton, trash);
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
                MoveFocusOutOfBar();
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

    private void MoveFocusOutOfBar()
    {
        if (XamlRoot is null || FocusManager.GetFocusedElement(XamlRoot) is not DependencyObject focused || !IsInside(focused, RemoteBar))
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

    private void FillAttachments()
    {
        if (XamlRoot is not null && FocusManager.GetFocusedElement(XamlRoot) is DependencyObject focused && IsInside(focused, AttachmentChips))
        {
            FocusBody();
        }
        AttachmentChips.Children.Clear();
        saveAllButton = null;
        saveAllOf = null;
        var icons = services.Icons.StartBatch();
        foreach (var chip in Reader.Chips)
        {
            AttachmentChips.Children.Add(AttachmentButton(chip, icons));
        }
        if (Reader.SaveAll.Count > 0 && Reader.Current is { } s)
        {
            saveAllButton = SaveAllButton(s, Reader.SaveAll);
            saveAllOf = s.Id;
            AttachmentChips.Children.Add(saveAllButton);
        }
        AttachmentChips.Visibility = ReaderBind.Visible(AttachmentChips.Children.Count > 0);
    }

    // One attachment: the click previews it (an attached message opens in
    // its own window), the arrow offers View, Open and Save As…. The
    // actions close over the chip, so it never acts on another message.
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
        open.Click += (_, _) => _ = services.Attachments.OpenAsync(chip.Attachment, chip.Message, HostWindow);
        var save = new MenuFlyoutItem();
        MnemonicLabel.Apply(save, L10n.T("Save _As…"));
        save.Click += (_, _) => _ = services.Attachments.SaveAsAsync(chip.Attachment, chip.Message, HostWindow);
        menu.Items.Add(open);
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
                _ = services.Preview.ShowAsync(chip.Attachment, chip.Message, HostWindow);
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

    // A style of the view's resources (MessageView.xaml).
    private Style Look(string key) => (Style)Resources[key];

    private void OpenAttached(AttachmentChip chip) =>
        _ = services.Windows.OpenEmbeddedAsync(chip.Message, chip.Attachment.PartId, HostWindow);

    // buildSaveAll: flat, as dense as the chips beside it; disabled while
    // the run lasts, which AttachmentOpener keeps by message, so a button
    // rebuilt by a re-render, or the same message's in another window, is
    // disabled too.
    private Button SaveAllButton(MessageSummary s, IReadOnlyList<Attachment> atts)
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
        button.Click += (_, _) => _ = services.Attachments.SaveAllAsync(atts, s, HostWindow);
        return button;
    }

    private void OnSavingAllChanged(object? sender, MessageId id)
    {
        if (!closed && saveAllButton is { } button && saveAllOf == id)
        {
            button.IsEnabled = !services.Attachments.IsSavingAll(id);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "copying an address failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogCopyFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "flushing the clipboard failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogFlushFailed(ILogger logger, string kind, int hResult);
}
