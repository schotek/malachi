// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The main window's two modes: Mail (the folders, the list, the reader and
// the assistant panel, as always) and Board (a triage board:
// BoardController, `Board.view`). The window shows one of them at a time under the same
// status bar; the toolbar's switch and the View menu change it. This is the
// part that needs no AppKit: which actions a mode allows, which requests
// bring the mail back, and when the user counts as looking at the folder.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board/board.go with the board itself. The texts are English
// until then (no msgids yet).

import Foundation

/// The board package: a namespace, so the Go names will map 1:1
/// (`board.Allows` → `Board.allows`).
public enum Board {
    /// What the main window shows. The raw value is the tag of the View
    /// menu's item and the index of the toolbar switch's segment.
    public enum Mode: Int, Sendable, CaseIterable {
        case mail
        case board

        /// The mode's value in the settings (the key `board-last-mode`:
        /// "mail", "board").
        public var nick: String {
            switch self {
            case .mail: "mail"
            case .board: "board"
            }
        }
    }

    /// The mode a stored nick names; nil for an unknown one.
    public static func parseMode(_ nick: String) -> Mode? {
        Mode.allCases.first { $0.nick == nick }
    }

    /// The mode a new main window starts in when nothing else says (the
    /// default of the key `board-start-mode`); `startMode` decides.
    public static let initialMode: Mode = .mail

    /// The stored value of a choice that takes what the user had last
    /// (`board-start-mode`, `board-default-style`).
    public static let nickLast = "last"

    /// A value of Open at Launch (the key `board-start-mode`); `allCases`
    /// is Settings' order.
    public enum StartChoice: Sendable, CaseIterable {
        case mail
        case board
        /// The mode shown last (the key `board-last-mode`).
        case last

        /// The choice's value in the settings: "mail", "board", "last".
        public var nick: String {
            switch self {
            case .mail: "mail"
            case .board: "board"
            case .last: Board.nickLast
            }
        }
    }

    /// The choices of Open at Launch in Settings' order (Go `StartModes`).
    public static let startModes = StartChoice.allCases

    /// The choice a stored nick names; an unknown or empty one is Mail.
    public static func parseStartChoice(_ nick: String) -> StartChoice {
        StartChoice.allCases.first { $0.nick == nick } ?? .mail
    }

    /// The mode a new main window opens in: `start` is the key
    /// `board-start-mode`, `last` the key `board-last-mode` (written
    /// whenever the mode switches). With the board turned off
    /// (`boardEnabled` false) always Mail.
    public static func startMode(start: String, last: String, boardEnabled: Bool) -> Mode {
        guard boardEnabled else { return .mail }
        switch parseStartChoice(start) {
        case .mail: return .mail
        case .board: return .board
        case .last: return parseMode(last) ?? .mail
        }
    }

    /// How long a new main window waits for the daemon's board
    /// preferences before it settles in Mail for good (`startDecision`;
    /// Go `StartWait`).
    public static let startWait: TimeInterval = 5

    /// The mode a new main window opens in while the daemon's board
    /// preferences may still be on their way (Go `StartDecision`). `start`
    /// is Open at Launch (`board-start-mode`), `lastMode` the mode shown
    /// last (`board-last-mode`); `prefsKnown` whether the daemon's
    /// preferences arrived and `enabled` their Show the Board;
    /// `userSwitched` whether the user switched the mode, `userInteracted`
    /// whether the user acted in Mail (a click or a key in its content),
    /// `waited` the time since the window opened.
    ///
    /// `decided` is false while the window should keep waiting (it shows
    /// Mail meanwhile). Once decided the window applies `mode` if it still
    /// shows Mail and never moves on its own again:
    ///
    /// - a start that is Mail whatever the preferences say: Mail at once;
    /// - the user switched, or acted in Mail: Mail (the reader never jumps
    ///   away from what the user is doing);
    /// - the preferences arrived: `startMode` with their Show the Board;
    /// - `startWait` passed without them: Mail, and no later jump.
    public static func startDecision(
        start: StartChoice, lastMode: Mode, prefsKnown: Bool, enabled: Bool, userSwitched: Bool,
        userInteracted: Bool, waited: TimeInterval
    ) -> (mode: Mode, decided: Bool) {
        let wanted = startMode(start: start.nick, last: lastMode.nick, boardEnabled: true)
        if wanted == .mail || userSwitched || userInteracted {
            return (.mail, true)
        }
        if prefsKnown {
            return (startMode(start: start.nick, last: lastMode.nick, boardEnabled: enabled), true)
        }
        if waited >= startWait {
            return (.mail, true)
        }
        return (.mail, false)
    }

    /// The texts of the switch (the board's own are `Board.Text`).
    public struct Texts: Sendable, Equatable {
        /// The switch's segment and View menu item for Mail.
        public var mail: String
        /// The switch's segment and View menu item for Board, and the
        /// window's title in Board.
        public var board: String
    }

    public static func texts() -> Texts {
        Texts(
            mail: L10n.T("Mail"),
            board: Text.boardName
        )
    }

    /// The window's actions, as far as a mode cares.
    public enum Command: Sendable, CaseIterable {
        /// Switching between the modes.
        case switchMode
        /// New Message.
        case newMessage
        /// Check for New Mail.
        case checkForNewMail
        /// The views of the mail: the sidebar's, the message list's and the
        /// assistant panel's toggles, the filter, Find…, Load Images, Always Load Images
        /// From This Sender.
        case mailView
        /// Everything that acts on the selected message or conversation
        /// (reply, flags, moves, the assistant's message actions, Summarize
        /// Unread in This Folder).
        case messageAction
        /// The views of the board: its style (List, Columns, Today).
        case boardView
    }

    /// Whether `command` may run in `mode`. Mail allows everything but the
    /// board's views; Board only what does not need the folders, the list
    /// and the reader: the list keeps its selection while hidden, and no
    /// key may act on a message the user cannot see.
    public static func allows(_ command: Command, in mode: Mode) -> Bool {
        switch mode {
        case .mail:
            return command != .boardView
        case .board:
            switch command {
            case .switchMode, .newMessage, .checkForNewMail, .boardView: return true
            case .mailView, .messageAction: return false
            }
        }
    }

    /// Requests from elsewhere in the application that may reach the main
    /// window in either mode.
    public enum Request: Sendable, CaseIterable {
        /// The status bar's Outbox: the outbox folder in the list.
        case showOutbox
        /// The assistant panel unfolds (the Assistant button, a menu run
        /// in the panel).
        case revealAssistant
        /// A message opens in its own window (a notification's click).
        case openMessageWindow
        /// A compose window opens (New Message, `mailto:`).
        case compose
    }

    /// The mode the main window is in after `request`: what shows the mail
    /// in the main window brings Mail back, what opens a window of its own
    /// leaves the mode as it is.
    public static func mode(for request: Request, current: Mode) -> Mode {
        switch request {
        case .showOutbox, .revealAssistant: return .mail
        case .openMessageWindow, .compose: return current
        }
    }

    /// Whether the user looks at the selected folder: the main window shows
    /// the mail and is the key window (window.go, `is-active`). A key
    /// window showing the board does not count, so the folder's desktop
    /// notifications stay.
    public static func viewsMail(_ mode: Mode, windowIsKey: Bool) -> Bool {
        mode == .mail && windowIsKey
    }
}
