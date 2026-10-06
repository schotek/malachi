// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardView.swift (Board.Style.nick);
// GTK: ui/internal/board/mode.go (Style.Nick).

namespace Malachi.Core.Board;

/// <summary>Swift's computed properties of <see cref="BoardStyle"/>.</summary>
public static class BoardStyleExtensions
{
    extension(BoardStyle style)
    {
        /// <summary>
        /// The style's value in the settings (board-default-style): "list",
        /// "columns", "today"; an unknown number is "list".
        /// </summary>
        public string Nick => style switch
        {
            BoardStyle.Columns => "columns",
            BoardStyle.Today => "today",
            _ => "list",
        };
    }
}
