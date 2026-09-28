// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the message page's header bar of ui/internal/window/window.go
// (the reply buttons, the star's clicked handler) and actions.go
// (setMessageActionsSensitive, setStar, trashTooltip); macOS:
// MainWindow/MainToolbar.swift and MainWindowController's validation
// (starTitle, trashTitle). See MessageCommandBar.xaml. The buttons run the
// main window's commands, which are enabled from the selection's
// ActionFlags; the star and the trash follow the flags too.

using System;
using Malachi.App.Commands;
using Malachi.App.Resources;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

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
        CommandBinding.Bind(JunkButton, windowCommands.Junk);
        CommandBinding.Bind(ArchiveButton, windowCommands.Archive);
        CommandBinding.Bind(MenuMarkUnread, windowCommands.MarkUnread);
        CommandBinding.Bind(MenuMarkRead, windowCommands.MarkRead);
        CommandBinding.Bind(MenuLoadImages, windowCommands.LoadImages);
        CommandBinding.Bind(MenuTrustSender, windowCommands.TrustSender);
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
    }

    // window.go: the star acts on the selection (every member of a
    // conversation row, flagTarget); the button then shows the model.
    private void OnStarClick(object sender, RoutedEventArgs e)
    {
        commands?.ToggleFlag.TryExecute();
        ShowFlags();
    }
}
