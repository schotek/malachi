// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/Board.swift (Mode.nick,
// StartChoice.nick) and BoardView.swift (DefaultStyle.nick); GTK: ui/internal/board/mode.go
// (Mode.Nick, StartChoice.Nick, DefaultStyle.Nick). The nicks are the
// values of the keys board-last-mode, board-start-mode and
// board-default-style.

namespace Malachi.Core.Boards;

/// <summary>Go's method of <see cref="Board.Mode"/>.</summary>
/// <remarks>One static class per receiver: several extension blocks in one
/// class trip CA1708 over their synthesised names.</remarks>
public static class BoardModeNickExtensions
{
    extension(Board.Mode mode)
    {
        /// <summary>The mode's value in the settings (board-last-mode): "mail", "board".</summary>
        public string Nick => mode == Board.Mode.Board ? "board" : "mail";
    }
}

/// <summary>Go's method of <see cref="Board.StartChoice"/>.</summary>
public static class BoardStartChoiceExtensions
{
    extension(Board.StartChoice choice)
    {
        /// <summary>The choice's value in the settings (board-start-mode): "mail", "board", "last".</summary>
        public string Nick => choice switch
        {
            Board.StartChoice.Board => "board",
            Board.StartChoice.Last => Board.NickLast,
            _ => "mail",
        };
    }
}

/// <summary>Go's methods of <see cref="Board.DefaultStyle"/>.</summary>
public static class BoardDefaultStyleExtensions
{
    extension(Board.DefaultStyle choice)
    {
        /// <summary>Last Used: the style the user had last; <see cref="Style"/> is then unused.</summary>
        public bool IsLast => choice == Board.DefaultStyle.Last;

        /// <summary>The style chosen; the List for Last Used.</summary>
        public BoardStyle Style => choice switch
        {
            Board.DefaultStyle.Columns => BoardStyle.Columns,
            Board.DefaultStyle.Today => BoardStyle.Today,
            _ => BoardStyle.List,
        };

        /// <summary>The choice's value in the settings (board-default-style): "last", "list", "columns", "today".</summary>
        public string Nick => choice == Board.DefaultStyle.Last ? Board.NickLast : choice.Style.Nick;
    }
}
