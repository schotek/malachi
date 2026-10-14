// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardCaseMenu.swift
// (contextMenu(for:actions:)); GTK: showCaseContextMenu
// (window/board_actions.go). The context menu of a row or a card: Move To ▸
// (the four states, the current one checked; disabled for a done case,
// which a new state would not take out of Done), Mark as Done or Move Back
// to Board, Remind Me ▸, Archive, Unstar (only for a case on the board
// because of a star and not done, Board.CanUnstar); Reply (Comment on an
// issue tracker's account), Show in Mail. The items run BoardActions, as
// the detail's buttons do; the controller reports the change.

using System;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The menus of a case.</summary>
internal static class BoardCaseMenu
{
    /// <summary>The context menu of case <paramref name="id"/>, built as it opens.</summary>
    public static MenuFlyout ContextMenu(BoardCaseId id, BoardActions actions)
    {
        ArgumentNullException.ThrowIfNull(actions);
        var menu = new MenuFlyout();
        AutomationProperties.SetAutomationId(menu, "BoardCaseMenu");
        if (actions.Case(id) is not { } c)
        {
            return menu;
        }
        var done = c.Visibility.IsDone;
        var move = new MenuFlyoutSubItem { Text = Board.Text.MoveTo, IsEnabled = !done };
        AutomationProperties.SetAutomationId(move, "BoardMoveTo");
        actions.FillStateMenu(move.Items, id);
        menu.Items.Add(move);
        menu.Items.Add(new MenuFlyoutSeparator());
        menu.Items.Add(BoardMenuItem.Make(
            done ? Board.Text.NotDone : Board.Text.MarkAsDone, () => actions.ToggleDone(id), automationId: "BoardMenuDone"));
        var remind = new MenuFlyoutSubItem { Text = Board.Text.RemindMe };
        AutomationProperties.SetAutomationId(remind, "BoardMenuRemind");
        actions.FillRemindMenu(remind.Items, id);
        menu.Items.Add(remind);
        menu.Items.Add(BoardMenuItem.Make(
            Board.Text.Archive, () => actions.Archive(id), actions.CanArchive(id), "BoardMenuArchive"));
        if (actions.CanUnstar(id))
        {
            menu.Items.Add(BoardMenuItem.Make(Board.Text.Unstar, () => actions.Unstar(id), automationId: "BoardMenuUnstar"));
        }
        menu.Items.Add(new MenuFlyoutSeparator());
        menu.Items.Add(BoardMenuItem.Make(
            actions.ReplyLabel(c.Account), () => actions.Reply(id), actions.CanReply(id), "BoardMenuReply"));
        menu.Items.Add(BoardMenuItem.Make(
            Board.Text.ShowInMail, () => actions.ShowInMail(id), actions.CanShowInMail(id), "BoardMenuShowInMail"));
        return menu;
    }
}
