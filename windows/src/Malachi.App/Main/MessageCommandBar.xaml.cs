// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the message page's header bar of ui/internal/window/window.go
// (the reply buttons, the star's clicked handler) and actions.go
// (setMessageActionsSensitive, setStar, trashTooltip); macOS:
// MainWindow/MainToolbar.swift and MainWindowController's validation
// (starTitle, trashTitle). See MessageCommandBar.xaml. The buttons run the
// main window's commands, which are enabled from the selection's
// ActionFlags; the star and the trash follow the flags too, and so do the
// actions an account does not offer (hidden) and Reply's label
// (ActionPresentation).

using System;
using Malachi.App.Commands;
using Malachi.App.Resources;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Main;

/// <summary>The message pane's header bar.</summary>
public sealed partial class MessageCommandBar : UserControl
{
    private WindowCommands? commands;

    /// <summary>A bar whose buttons are disabled until <see cref="Attach"/>.</summary>
    public MessageCommandBar()
    {
        InitializeComponent();
        StarButton.IsEnabled = false;
        StarGlyph.Glyph = Icons.Glyph("non-starred-symbolic");
        ReplyGlyph.Glyph = Icons.Glyph("mail-reply-sender-symbolic");
        StarButton.Click += OnStarClick;
    }

    /// <summary>Binds the buttons to the main window's commands and shows their flags.</summary>
    public void Attach(WindowCommands windowCommands)
    {
        ArgumentNullException.ThrowIfNull(windowCommands);
        commands = windowCommands;
        CommandBinding.Bind(ReplyButton, windowCommands.Reply);
        CommandBinding.Bind(ReplyAllButton, windowCommands.ReplyAll);
        CommandBinding.Bind(ForwardButton, windowCommands.Forward);
        CommandBinding.Bind(TrashButton, windowCommands.Trash);
        CommandBinding.Bind(ArchiveButton, windowCommands.Archive);
        CommandBinding.Bind(MenuJunk, windowCommands.Junk);
        CommandBinding.Bind(MenuMarkUnread, windowCommands.MarkUnread);
        CommandBinding.Bind(MenuMarkRead, windowCommands.MarkRead);
        CommandBinding.Bind(MenuLoadImages, windowCommands.LoadImages);
        CommandBinding.Bind(MenuTrustSender, windowCommands.TrustSender);
        CommandBinding.Bind(MenuChangeStatus, windowCommands.ChangeStatus);
        windowCommands.ChangeStatus.Command.CanExecuteChanged += (_, _) => ShowFlags();
        windowCommands.ToggleFlag.Command.CanExecuteChanged += (_, _) => ShowFlags();
        windowCommands.Trash.Command.CanExecuteChanged += (_, _) => ShowFlags();
        ShowFlags();
    }

    /// <summary>
    /// actions.go setStar and trashTooltip for the current flags: the
    /// star's state, icon and tooltip (Star or Unstar), and Cancel Sending
    /// for an outbox message's trash.
    /// </summary>
    public void ShowFlags()
    {
        if (commands is not { } c)
        {
            return;
        }
        var flags = c.Flags;
        StarButton.IsEnabled = c.ToggleFlag.IsEnabled;
        StarButton.IsChecked = flags.Flagged;
        StarGlyph.Glyph = Icons.Glyph(flags.Flagged ? "starred-symbolic" : "non-starred-symbolic");
        var star = flags.Flagged ? L10n.T("Unstar") : L10n.T("Star");
        ToolTipService.SetToolTip(StarButton, star);
        AutomationProperties.SetName(StarButton, star);
        var trash = Outbox.TrashTooltip(flags.Outbox);
        ToolTipService.SetToolTip(TrashButton, trash);
        AutomationProperties.SetName(TrashButton, trash);
        // actions.go presentReply and the capabilities' visibility.
        var reply = ActionPresentation.ReplyLabel(flags);
        ReplyGlyph.Glyph = ActionPresentation.ReplyGlyph(flags);
        ToolTipService.SetToolTip(ReplyButton, reply);
        AutomationProperties.SetName(ReplyButton, reply);
        ReplyButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Reply);
        ReplyAllButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.ReplyAll);
        ForwardButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Forward);
        TrashButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Trash);
        ArchiveButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Archive);
        var status = c.ChangeStatus.IsEnabled ? Visibility.Visible : Visibility.Collapsed;
        MenuChangeStatus.Visibility = status;
        MenuChangeStatusSeparator.Visibility = status;
    }

    /// <summary>The Assistant's ✦ button (window.blp assistant_button); the main window gives it its menu.</summary>
    public Button Assistant => AssistantButton;

    /// <summary>The assistant panel's toggle (window.blp assistant_panel_button).</summary>
    public ToggleButton AssistantPanel => AssistantPanelToggle;

    /// <summary>
    /// The width the bar needs to show every visible button: the two groups,
    /// the padding and the spacing between them (the minimum width GTK's
    /// header bar would ask its pane for).
    /// </summary>
    public double MinimumWidth
    {
        get
        {
            var infinite = new Windows.Foundation.Size(double.PositiveInfinity, double.PositiveInfinity);
            StartGroup.Measure(infinite);
            EndGroup.Measure(infinite);
            var bar = (Grid)Content;
            return bar.Padding.Left + bar.Padding.Right + StartGroup.DesiredSize.Width + (2 * bar.ColumnSpacing) + EndGroup.DesiredSize.Width;
        }
    }

    // window.go: the star acts on the selection (every member of a
    // conversation row, flagTarget); the button then shows the model.
    private void OnStarClick(object sender, RoutedEventArgs e)
    {
        commands?.ToggleFlag.TryExecute();
        ShowFlags();
    }
}
