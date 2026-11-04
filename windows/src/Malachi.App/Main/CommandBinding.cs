// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: runs a window's AppCommand from a button or a menu
// item, enabled while the command is (macOS validateUserInterfaceItem, GTK
// action-name). The control is not given the command's XamlUICommand:
// assigning one to Button.Command replaces the button's content with the
// command's Label, which these commands leave empty (measured: the icon
// buttons of the header bars came out blank), so the click and the enabled
// state are wired here instead, and the content stays the control's own.

using System;
using Malachi.App.Commands;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Main;

/// <summary>Wires a control to an <see cref="AppCommand"/>.</summary>
internal static class CommandBinding
{
    /// <summary>A click runs <paramref name="command"/>; the button is enabled while the command is.</summary>
    public static void Bind(ButtonBase button, AppCommand command)
    {
        ArgumentNullException.ThrowIfNull(button);
        ArgumentNullException.ThrowIfNull(command);
        button.Click += (_, _) => command.TryExecute();
        command.Command.CanExecuteChanged += (_, _) => button.IsEnabled = command.IsEnabled;
        button.IsEnabled = command.IsEnabled;
    }

    /// <summary>A click runs <paramref name="command"/>; the item is enabled while the command is.</summary>
    public static void Bind(MenuFlyoutItem item, AppCommand command)
    {
        ArgumentNullException.ThrowIfNull(item);
        ArgumentNullException.ThrowIfNull(command);
        item.Click += (_, _) => command.TryExecute();
        command.Command.CanExecuteChanged += (_, _) => item.IsEnabled = command.IsEnabled;
        item.IsEnabled = command.IsEnabled;
    }

    /// <summary>A menu item for a context menu opened now: enabled as the command is at this moment.</summary>
    public static MenuFlyoutItem Item(AppCommand command)
    {
        ArgumentNullException.ThrowIfNull(command);
        var item = new MenuFlyoutItem { IsEnabled = command.IsEnabled };
        item.Click += (_, _) => command.TryExecute();
        return item;
    }
}
