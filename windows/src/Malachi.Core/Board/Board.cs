// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/Board.swift and the style rules of
// BoardView.swift (parseStyle, styleOnShow); GTK: ui/internal/board/mode.go
// (InitialMode, Allows, ModeFor, ViewsMail, ParseStyle, StyleOnShow) and
// text.go (Mail, BoardName).
//
// The main window's two modes: Mail (the folders, the list, the reader and
// the assistant panel, as always) and Board (a triage board). The window
// shows one of them at a time under the same status bar; the title bar's
// switch changes it. This is the part that needs no WinUI: which actions a
// mode allows, which requests bring the mail back, and when the user counts
// as looking at the folder.
//
// Swift's namespace enum Board is this class, as in the other ports of
// the board. Code outside the namespace Malachi.Core.Board finds the
// namespace under the name Board before the class (SettingsStore's reason
// for its own name), so it imports the class: using static
// Malachi.Core.Board.Board, or an alias. The nested types keep Swift's
// names (Board.Case, Board.Snapshot, Board.Text); a function whose name a
// nested type takes has Go's name (StateOf, AnnotationOf, ModeFor, and
// View, which builds a ViewModel). Board.Style is the top-level BoardStyle,
// which the settings store.

using System;
using Malachi.Core.I18n;

namespace Malachi.Core.Board;

/// <summary>The board's model: its types and pure functions (Swift's <c>Board</c>, Go's package <c>board</c>).</summary>
public static partial class Board
{
    /// <summary>
    /// What the main window shows. The number is the index of the title bar
    /// switch's segment.
    /// </summary>
    public enum Mode
    {
        /// <summary>The folders, the list, the reader and the assistant panel, as always.</summary>
        Mail,

        /// <summary>The triage board.</summary>
        Board,
    }

    /// <summary>The mode a new main window starts in. The mode is not remembered.</summary>
    public const Mode InitialMode = Mode.Mail;

    /// <summary>The window's actions, as far as a mode cares.</summary>
    public enum Command
    {
        /// <summary>Switching between the modes.</summary>
        SwitchMode,

        /// <summary>New Message.</summary>
        NewMessage,

        /// <summary>Check for New Mail.</summary>
        CheckForNewMail,

        /// <summary>
        /// The views of the mail: the sidebar's, the message list's and the
        /// assistant panel's toggles, the filter, Find…, Load Images, Always
        /// Load Images From This Sender.
        /// </summary>
        MailView,

        /// <summary>
        /// Everything that acts on the selected message or conversation
        /// (reply, flags, moves, the assistant's message actions, Summarize
        /// Unread in This Folder).
        /// </summary>
        MessageAction,

        /// <summary>The views of the board: its style (List, Columns, Today).</summary>
        BoardView,
    }

    /// <summary>Requests from elsewhere in the application that may reach the main window in either mode.</summary>
    public enum Request
    {
        /// <summary>The status bar's Outbox: the outbox folder in the list.</summary>
        ShowOutbox,

        /// <summary>The assistant panel unfolds (the Assistant button, a menu run in the panel).</summary>
        RevealAssistant,

        /// <summary>A message opens in its own window (a notification's click).</summary>
        OpenMessageWindow,

        /// <summary>A compose window opens (New Message, <c>mailto:</c>).</summary>
        Compose,
    }

    /// <summary>The texts of the mode switch (Swift <c>Board.Texts</c>; the board's own are <see cref="Text"/>).</summary>
    /// <param name="Mail">The switch's segment and menu item for Mail.</param>
    /// <param name="Board">The switch's segment and menu item for Board, and the window's title in it.</param>
    public sealed record ModeTexts(string Mail, string Board);

    /// <summary>The texts of the mode switch (Swift <c>Board.texts()</c>).</summary>
    public static ModeTexts Texts() => new(L10n.T("Mail"), Text.BoardName);

    /// <summary>
    /// Whether <paramref name="command"/> may run in <paramref name="mode"/>.
    /// Mail allows everything but the board's views; Board only what does
    /// not need the folders, the list and the reader: the list keeps its
    /// selection while hidden, and no key may act on a message the user
    /// cannot see.
    /// </summary>
    public static bool Allows(Command command, Mode mode) => mode switch
    {
        Mode.Mail => command != Command.BoardView,
        _ => command is Command.SwitchMode or Command.NewMessage or Command.CheckForNewMail or Command.BoardView,
    };

    /// <summary>
    /// The mode the main window is in after <paramref name="request"/>
    /// (Swift <c>mode(for:current:)</c>): what shows the mail in the main
    /// window brings Mail back, what opens a window of its own leaves the
    /// mode as it is.
    /// </summary>
    public static Mode ModeFor(Request request, Mode current) => request switch
    {
        Request.ShowOutbox or Request.RevealAssistant => Mode.Mail,
        _ => current,
    };

    /// <summary>
    /// Whether the user looks at the selected folder: the main window shows
    /// the mail and is the active window. An active window showing the board
    /// does not count, so the folder's desktop notifications stay.
    /// </summary>
    public static bool ViewsMail(Mode mode, bool windowIsKey) => mode == Mode.Mail && windowIsKey;

    /// <summary>The style a stored nick names; an unknown or empty one is the List.</summary>
    public static BoardStyle ParseStyle(string? nick)
    {
        foreach (var s in Enum.GetValues<BoardStyle>())
        {
            if (string.Equals(s.Nick, nick, StringComparison.Ordinal))
            {
                return s;
            }
        }
        return BoardStyle.List;
    }

    /// <summary>
    /// The style the board takes as it shows: the default (from the
    /// settings) the first time in a run, else the one it has, which is the
    /// user's last choice. A default changed after the first show waits for
    /// the next launch: the style never changes under the user.
    /// </summary>
    public static BoardStyle StyleOnShow(BoardStyle current, BoardStyle defaultStyle, bool firstShow) =>
        firstShow ? defaultStyle : current;
}
