// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardController.swift
// (BoardController.Changes); GTK: ui/internal/board/controller.go (Changes,
// ChangeContent, ChangeSelection, ChangeStyle, ChangeFilters). Swift's
// OptionSet is a flags enum of the same bits.

using System;

namespace Malachi.Core.Controllers;

public sealed partial class BoardController
{
    /// <summary>What changed, for the page. Several at once are possible.</summary>
    [Flags]
    public enum Changes
    {
        /// <summary>Nothing.</summary>
        None = 0,

        /// <summary>
        /// The cases shown changed (rows, sections, columns, the Today page,
        /// the counts, or the selected case's detail itself).
        /// </summary>
        Content = 1 << 0,

        /// <summary>
        /// Another case is selected, none is, the panel opens or closes, or
        /// the "Why is this here?" box opens or closes
        /// (<see cref="Boards.Board.ViewState.RevealsWhy"/>).
        /// </summary>
        Selection = 1 << 1,

        /// <summary><see cref="Boards.Board.ViewState.Style"/> changed.</summary>
        Style = 1 << 2,

        /// <summary><see cref="Boards.Board.ViewState.Filter"/> or <see cref="Boards.Board.ViewState.Account"/> changed.</summary>
        Filters = 1 << 3,
    }
}
