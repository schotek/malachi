// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardView.swift (Board.Style);
// GTK: ui/internal/board/text.go (Style), mode.go (Styles). A top-level
// enum: SettingsStore stores it under board-default-style, whose gschema
// nicks are the members' names in lower case (list, columns, today).

namespace Malachi.Core.Boards;

/// <summary>
/// How the board lays the cases out. The number is the index of the style
/// switch's segment and is never stored; the settings store the nick
/// (<see cref="BoardStyleExtensions"/>).
/// </summary>
public enum BoardStyle
{
    /// <summary>A navigation column, the list of cases and the detail.</summary>
    List,

    /// <summary>One column of cards per state.</summary>
    Columns,

    /// <summary>The day: what needs the user, deadlines.</summary>
    Today,
}
