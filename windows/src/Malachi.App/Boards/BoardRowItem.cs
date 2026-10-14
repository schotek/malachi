// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardListViewController.swift
// (Item.row); GTK: the case rows of renderList (window/board_list.go). A
// case in the List style's ListView; the template is BoardRowView.

using Malachi.Core.Boards;

namespace Malachi.App.Boards;

/// <summary>A case row of the board's list.</summary>
/// <param name="Row">The view model's row (its strings cleaned by Core).</param>
public sealed record BoardRowItem(Board.Row Row);
