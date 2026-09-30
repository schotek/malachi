// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AddressHeaderView.swift (the
// chips and their menu, "+N more") and AttachmentChipView.swift /
// AttachmentChipFactory (the chip, its menu, the server symbol or the
// spinner, Save All); GTK: ui/internal/window/addresses.go (fill, chip,
// addressMenu, moreChip) and attachments.go (renderAttachments, buildChip,
// remoteIndicator, chipMenu, buildSaveAll). The chips of one message display
// (MessageView, and each card of the conversation view, whose recipients and
// chips are the single-message pane's own code in GTK and macOS too): built
// from Core's AddressHeader rows and AttachmentChips, their clicks sent to
// the MessageActionRouter (an address's New Message), the AttachmentOpener
// and previewer, and the registry (an attached message's window). Every text
// is set through a Text property; no markup is ever parsed.
//
// Windows differences (MessageView): the chips are split buttons (the click
// previews, the arrow's menu has View, Open, Save As…), a chip GTK makes
// insensitive shows its reason in a tooltip on a wrapper, "+N more" takes no
// focus on a click but is reached by Tab, Copy Address can fail while
// another program holds the clipboard and says so, and Save All stays
// disabled while its run lasts (AttachmentOpener's, by message).

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Threading.Tasks;
using Malachi.App.Attachments;
using Malachi.App.Localization;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
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

/// <summary>The address and attachment chips of one message display.</summary>
internal sealed partial class MessageChips
{
    // Copy Address: another program may hold the clipboard open for a moment
    // (a clipboard manager, a remote desktop session); tried this many times,
    // this far apart, as WinForms' Clipboard.SetDataObject does.
    private const int ClipboardAttempts = 5;
    private static readonly TimeSpan ClipboardRetryDelay = TimeSpan.FromMilliseconds(50);

    private readonly ReaderServices services;
    private readonly FrameworkElement owner;
    private readonly Func<Window?> hostWindow;
    private readonly Action focusBody;
    private readonly ILogger logger;

    /// <param name="services">The reader's services.</param>
    /// <param name="owner">The view whose resources hold ChipStyles.xaml and whose XamlRoot has the focus.</param>
    /// <param name="hostWindow">The window the view is in: where its dialogs and toasts go.</param>
    /// <param name="focusBody">Puts the keyboard somewhere harmless (the body) when a chip that had it goes.</param>
    /// <param name="logger">Receives the kind of a failure, never an address.</param>
    public MessageChips(ReaderServices services, FrameworkElement owner, Func<Window?> hostWindow, Action focusBody, ILogger logger)
    {
        this.services = services;
        this.owner = owner;
        this.hostWindow = hostWindow;
        this.focusBody = focusBody;
        this.logger = logger;
    }

    /// <summary>The Save All button last built by <see cref="FillAttachments"/>, and the message it saves; null when none.</summary>
    public (Button Button, MessageId Message)? SaveAll { get; private set; }

    /// <summary>
    /// addresses.go fill: the chips of <paramref name="row"/> into
    /// <paramref name="box"/>, "+N more" after them; the row's label and box
    /// shown while it has any. <paramref name="expand"/> unfolds every line
    /// of the message (the header's own Expand), and <paramref name="boxOf"/>
    /// finds the row's box again after it.
    /// </summary>
    public void FillAddressRow(AddressRow row, TextBlock label, WrapBox box, Action expand, Func<WrapBox> boxOf)
    {
        ArgumentNullException.ThrowIfNull(row);
        ArgumentNullException.ThrowIfNull(label);
        ArgumentNullException.ThrowIfNull(box);
        // A chip about to go may hold the focus: the body is somewhere
        // harmless to put it.
        if (IsFocusInside(box))
        {
            focusBody();
        }
        box.Children.Clear();
        foreach (var chip in row.Chips)
        {
            box.Children.Add(AddressButton(chip));
        }
        if (row.More > 0)
        {
            box.Children.Add(MoreChip(row, expand, boxOf));
        }
        label.Visibility = ReaderBind.Visible(row.Visible);
        box.Visibility = ReaderBind.Visible(row.Visible);
    }

    /// <summary>
    /// attachments.go renderAttachments: the chips of <paramref name="chips"/>
    /// and Save All into <paramref name="box"/> (shown while it has any). The
    /// chip that holds the focus (one used a moment ago, whose download starts
    /// or ends now) goes with the rest; the focus goes to the chip in its
    /// place, from the keyboard when it came from the keyboard, or to the
    /// body when there is none there that can take it. The new chips are
    /// added before the old ones go, so the focus moves from the old chip
    /// straight to its successor.
    /// </summary>
    public void FillAttachments(
        Panel box, IReadOnlyList<AttachmentChip> chips, IReadOnlyList<Attachment> saveAll, bool saveAllRemote, MessageSummary? message)
    {
        ArgumentNullException.ThrowIfNull(box);
        ArgumentNullException.ThrowIfNull(chips);
        ArgumentNullException.ThrowIfNull(saveAll);
        var children = box.Children;
        var (focusAt, how) = FocusedChip(box);
        var old = children.Count;
        SaveAll = null;
        var icons = services.Icons.StartBatch();
        foreach (var chip in chips)
        {
            children.Add(AttachmentButton(chip, icons));
        }
        if (saveAll.Count > 0 && message is { } s)
        {
            var button = SaveAllButton(s, saveAll, saveAllRemote);
            SaveAll = (button, s.Id);
            children.Add(button);
        }
        if (focusAt >= 0 && !(old + focusAt < children.Count && children[old + focusAt] is Control next && next.Focus(how)))
        {
            // A disabled chip (on a wrapper) takes no focus, and fewer chips
            // may leave none in its place.
            focusBody();
        }
        for (var i = 0; i < old; i++)
        {
            children.RemoveAt(0);
        }
        box.Visibility = ReaderBind.Visible(children.Count > 0);
    }

    /// <summary>AttachmentOpener's Save All run of <paramref name="id"/> began or ended: the button follows.</summary>
    public void SavingAllChanged(MessageId id)
    {
        if (SaveAll is { } s && s.Message == id)
        {
            s.Button.IsEnabled = !services.Attachments.IsSavingAll(id);
        }
    }

    // Whether the focus is inside container.
    private bool IsFocusInside(DependencyObject container)
    {
        if (owner.XamlRoot is null || FocusManager.GetFocusedElement(owner.XamlRoot) is not DependencyObject focused)
        {
            return false;
        }
        for (var e = focused; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (ReferenceEquals(e, container))
            {
                return true;
            }
        }
        return false;
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
        var window = hostWindow();
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
    private Button MoreChip(AddressRow row, Action expand, Func<WrapBox> boxOf)
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
            expand();
            var box = boxOf();
            if (keyboard && at < box.Children.Count && box.Children[at] is Control next)
            {
                next.Focus(FocusState.Keyboard);
            }
        };
        return button;
    }

    // The position of the chip (or Save All) that holds the focus, and how
    // to give it to the one in its place (attachments.go focusedChip): -1
    // when none does.
    private (int At, FocusState How) FocusedChip(Panel box)
    {
        if (owner.XamlRoot is null || FocusManager.GetFocusedElement(owner.XamlRoot) is not DependencyObject focused)
        {
            return (-1, FocusState.Unfocused);
        }
        for (var e = focused; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (VisualTreeHelper.GetParent(e) is { } parent && ReferenceEquals(parent, box) && e is UIElement chip)
            {
                var how = focused is Control { FocusState: FocusState.Keyboard } ? FocusState.Keyboard : FocusState.Programmatic;
                return (box.Children.IndexOf(chip), how);
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
        open.Click += (_, _) => _ = services.Attachments.OpenAsync(chip.Attachment, chip.Message, chip.OnServer, hostWindow());
        var save = new MenuFlyoutItem();
        MnemonicLabel.Apply(save, L10n.T("Save _As…"));
        save.Click += (_, _) => _ = services.Attachments.SaveAsAsync(chip.Attachment, chip.Message, chip.OnServer, hostWindow());
        menu.Items.Add(open);
        // assistant.go bindAskItem: "Ask the Assistant…" while the Assistant
        // is shown, enabled while the chosen target can take the file (for
        // In App, a type the bridge reads); looked at again as the menu opens.
        var ask = new MenuFlyoutItem { Text = Core.Assistants.Assistant.Texts().AskFile };
        ask.Click += (_, _) => services.State.MainWindow?.AssistantActions?.AskAboutAttachment(chip.Attachment, chip.Message, chip.OnServer, hostWindow());
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
                _ = services.Preview.ShowAsync(chip.Attachment, chip.Message, chip.OnServer, hostWindow());
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

    // A style of the owner's resources (ChipStyles.xaml).
    private Style Look(string key) => (Style)owner.Resources[key];

    private void OpenAttached(AttachmentChip chip) =>
        _ = services.Windows.OpenEmbeddedAsync(chip.Message, chip.Attachment, chip.OnServer, hostWindow());

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
        button.Click += (_, _) => _ = services.Attachments.SaveAllAsync(atts, s, remote, hostWindow());
        return button;
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "copying an address failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogCopyFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "flushing the clipboard failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogFlushFailed(ILogger logger, string kind, int hResult);
}
