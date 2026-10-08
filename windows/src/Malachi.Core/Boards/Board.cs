// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/Board.swift and the style rules of
// BoardView.swift (parseStyle, styleOnShow); GTK: ui/internal/board/mode.go
// (InitialMode, ParseMode, StartChoice, StartModes, NickLast,
// ParseStartChoice, StartMode, StartWait, StartDecision, Allows, ModeFor, ViewsMail, ParseStyle,
// DefaultStyle, DefaultStyles, ParseDefaultStyle, StyleOnShow,
// FilterOnShow) and text.go (Mail, BoardName). The nicks are
// BoardModeExtensions.
//
// The main window's two modes: Mail (the folders, the list, the reader and
// the assistant panel, as always) and Board (a triage board). The window
// shows one of them at a time under the same status bar; the title bar's
// switch changes it. This is the part that needs no WinUI: which actions a
// mode allows, which requests bring the mail back, and when the user counts
// as looking at the folder.
//
// Swift's namespace enum Board is this class, as in the other ports of
// the board. The namespace is Boards because a namespace Board would hide
// the class from every other Malachi.Core namespace (as Jira's is
// IssueTrackers), so Board.Case reads the same everywhere. The nested types keep Swift's
// names (Board.Case, Board.Snapshot, Board.Text); a function whose name a
// nested type takes has Go's name (StateOf, AnnotationOf, ModeFor, and
// View, which builds a ViewModel). Board.Style is the top-level BoardStyle,
// which the settings store.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

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

    /// <summary>
    /// The mode a new main window starts in when nothing else says (the
    /// default of the key board-start-mode); <see cref="StartMode"/> decides.
    /// </summary>
    public const Mode InitialMode = Mode.Mail;

    /// <summary>
    /// The stored value of a choice that takes what the user had last
    /// (board-start-mode, board-default-style).
    /// </summary>
    public const string NickLast = "last";

    /// <summary>
    /// A value of Open at Launch (the key board-start-mode). The members'
    /// names in lower case are the gschema's nicks, in its order.
    /// </summary>
    public enum StartChoice
    {
        /// <summary>Mail.</summary>
        Mail,

        /// <summary>The board.</summary>
        Board,

        /// <summary>The mode shown last (the key board-last-mode).</summary>
        Last,
    }

    /// <summary>The choices of Open at Launch in Settings' order.</summary>
    public static IReadOnlyList<StartChoice> StartModes { get; } = [StartChoice.Mail, StartChoice.Board, StartChoice.Last];

    /// <summary>
    /// A value of Board View (the key board-default-style): a style, or the
    /// one used last (Go's <c>DefaultStyle{Last, Style}</c>). The members'
    /// names in lower case are the gschema's nicks, in its order; Settings
    /// lists them as <see cref="DefaultStyles"/>.
    /// </summary>
    public enum DefaultStyle
    {
        /// <summary>The List.</summary>
        List,

        /// <summary>Columns.</summary>
        Columns,

        /// <summary>Today.</summary>
        Today,

        /// <summary>Last Used: the style the user had last (board-last-style).</summary>
        Last,
    }

    /// <summary>Board View's choices in Settings' order: Last Used, List, Columns, Today.</summary>
    public static IReadOnlyList<DefaultStyle> DefaultStyles { get; } =
        [DefaultStyle.Last, DefaultStyle.List, DefaultStyle.Columns, DefaultStyle.Today];

    /// <summary>The mode a stored nick names (board-last-mode); null for an unknown one.</summary>
    public static Mode? ParseMode(string? nick) => nick switch
    {
        "mail" => Mode.Mail,
        "board" => Mode.Board,
        _ => null,
    };

    /// <summary>The choice of Open at Launch a stored nick names; an unknown or empty one is Mail.</summary>
    public static StartChoice ParseStartChoice(string? nick) =>
        StartModes.FirstOrDefault(s => string.Equals(s.Nick, nick, StringComparison.Ordinal));

    /// <summary>
    /// The mode a new main window opens in: <paramref name="startMode"/> is
    /// the key board-start-mode, <paramref name="lastMode"/> the key
    /// board-last-mode (written whenever the mode switches). With the board
    /// turned off (<paramref name="boardEnabled"/> false) always Mail.
    /// </summary>
    public static Mode StartMode(StartChoice startMode, string? lastMode, bool boardEnabled)
    {
        if (!boardEnabled)
        {
            return Mode.Mail;
        }
        return startMode switch
        {
            StartChoice.Board => Mode.Board,
            StartChoice.Last => ParseMode(lastMode) ?? Mode.Mail,
            _ => Mode.Mail,
        };
    }

    /// <summary>
    /// <see cref="StartMode(StartChoice, string, bool)"/> with the stored nick
    /// of board-start-mode (<see cref="ParseStartChoice"/>).
    /// </summary>
    public static Mode StartMode(string? startMode, string? lastMode, bool boardEnabled) =>
        StartMode(ParseStartChoice(startMode), lastMode, boardEnabled);

    /// <summary>
    /// How long a new main window waits for the daemon's board preferences
    /// before it settles its start in Mail (<see cref="StartDecision"/>).
    /// </summary>
    public static readonly TimeSpan StartWait = TimeSpan.FromSeconds(5);

    /// <summary>
    /// Whether, and in which mode, a new main window settles its start
    /// (Go <c>StartDecision</c>). <paramref name="start"/> is Open at Launch
    /// (board-start-mode), <paramref name="lastMode"/> the mode shown last
    /// (board-last-mode, null when unknown); <paramref name="prefsKnown"/>
    /// whether the daemon's preferences arrived and <paramref name="enabled"/>
    /// their Show the Board; <paramref name="userSwitched"/> whether the user
    /// switched the mode, <paramref name="userInteracted"/> whether the user
    /// acted in Mail (a click or a key there), <paramref name="waited"/> the
    /// time since the window opened.
    /// <para>
    /// Decided is false while the window should keep waiting; until then it
    /// shows Mail, writes nothing to board-last-mode and may still move to
    /// the Board. Once decided the window applies the mode if it still shows
    /// Mail and never moves on its own again: a start that is Mail whatever
    /// the preferences say settles at once; the user's switch or act in Mail
    /// settles in Mail (the reader never jumps away from what the user is
    /// doing); the preferences settle with <see cref="StartMode(StartChoice, string, bool)"/>;
    /// <see cref="StartWait"/> without them settles in Mail, with no later jump.
    /// </para>
    /// </summary>
    public static (Mode Mode, bool Decided) StartDecision(
        StartChoice start, Mode? lastMode, bool prefsKnown, bool enabled, bool userSwitched, bool userInteracted, TimeSpan waited)
    {
        var lastNick = lastMode == Mode.Board ? "board" : "mail";
        if (StartMode(start, lastNick, boardEnabled: true) == Mode.Mail || userSwitched || userInteracted)
        {
            return (Mode.Mail, true);
        }
        if (prefsKnown)
        {
            return (StartMode(start, lastNick, enabled), true);
        }
        return (Mode.Mail, waited >= StartWait);
    }

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

    /// <summary>
    /// The style a stored nick names (the key board-last-style); an unknown
    /// or empty one, and "last", is the List.
    /// </summary>
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
    /// The choice a stored nick of board-default-style names: "last" is Last
    /// Used, a style's nick that style, anything else (empty, unknown) Last
    /// Used, the key's default.
    /// </summary>
    public static DefaultStyle ParseDefaultStyle(string? nick)
    {
        foreach (var d in DefaultStyles)
        {
            if (!d.IsLast && string.Equals(d.Nick, nick, StringComparison.Ordinal))
            {
                return d;
            }
        }
        return DefaultStyle.Last;
    }

    /// <summary>
    /// The style the board takes as it shows. Once the user picked a style in
    /// this run (<paramref name="pickedThisRun"/>) the board keeps it
    /// (<paramref name="current"/>); before that it takes Board View: the
    /// style used last (<paramref name="lastStyle"/>, the key
    /// board-last-style) for Last Used, else the chosen one, so a change of
    /// Board View applies the next time the board shows unless the user
    /// already picked a style. The style never changes while the board
    /// shows.
    /// </summary>
    public static BoardStyle StyleOnShow(DefaultStyle defaultStyle, BoardStyle lastStyle, BoardStyle current, bool pickedThisRun)
    {
        if (pickedThisRun)
        {
            return current;
        }
        return defaultStyle.IsLast ? lastStyle : defaultStyle.Style;
    }

    /// <summary>
    /// The account filter the board takes as it shows: the one saved (the
    /// key board-account-filter) while that account is still among
    /// <paramref name="accounts"/>, else every account ("").
    /// </summary>
    public static string FilterOnShow(string? saved, IReadOnlyList<AccountInfo> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        if (string.IsNullOrEmpty(saved))
        {
            return "";
        }
        return accounts.Any(a => string.Equals(a.Id.Value, saved, StringComparison.Ordinal)) ? saved : "";
    }
}
