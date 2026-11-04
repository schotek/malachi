// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardCaseMenu.swift
// (BoardMenuAction.item: an item's action as a closure). The board's menus
// act on one case, which no window command names, so each item is a
// command of its own made for the menu as it opens and run through
// CommandBinding.Item, as the context menus of the mail run theirs.

using System;
using Malachi.App.Commands;
using Malachi.App.Localization;
using Malachi.App.Main;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>Menu items of the board's menus.</summary>
internal static class BoardMenuItem
{
    /// <summary>
    /// An item titled <paramref name="title"/> (a GTK mnemonic is turned into
    /// an access key) that runs <paramref name="run"/>, enabled as
    /// <paramref name="enabled"/> says now.
    /// </summary>
    public static MenuFlyoutItem Make(string title, Action run, bool enabled = true, string? automationId = null)
    {
        ArgumentNullException.ThrowIfNull(run);
        var command = new AppCommand("Board." + (automationId ?? "Item"))
        {
            Handler = run,
            CanExecute = () => enabled,
        };
        var item = CommandBinding.Item(command);
        MnemonicLabel.Apply(item, title);
        if (automationId is not null)
        {
            AutomationProperties.SetAutomationId(item, automationId);
        }
        return item;
    }

    /// <summary>A checkable item of a group (the states of Move To), checked when <paramref name="isChecked"/>.</summary>
    public static RadioMenuFlyoutItem Radio(string title, string group, bool isChecked, Action run, string? automationId = null)
    {
        ArgumentNullException.ThrowIfNull(run);
        var item = new RadioMenuFlyoutItem { Text = title, GroupName = group, IsChecked = isChecked };
        item.Click += (_, _) => run();
        if (automationId is not null)
        {
            AutomationProperties.SetAutomationId(item, automationId);
        }
        return item;
    }
}
