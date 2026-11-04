// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The main window's modes: Mail (the folders, the list, the reader and
// the assistant panel, as always), Board (a triage board:
// BoardController, `Board.view`) and Calendar (the calendar page:
// CalendarController, docs/calendar.md D27). The window shows one of them at a time under the same
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
        /// The calendar page (docs/calendar.md D27): raw value 2, nick
        /// "calendar".
        case calendar

        /// The mode's value in the settings (the key `board-last-mode`:
        /// "mail", "board", "calendar").
        public var nick: String {
            switch self {
            case .mail: "mail"
            case .board: "board"
            case .calendar: "calendar"
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
        /// The Calendar (docs/calendar.md D27), before Last Used in
        /// Settings' order (Go `StartModesWithCalendar`).
        case calendar
        /// The mode shown last (the key `board-last-mode`).
        case last

        /// The choice's value in the settings: "mail", "board",
        /// "calendar", "last".
        public var nick: String {
            switch self {
            case .mail: "mail"
            case .board: "board"
            case .calendar: "calendar"
            case .last: Board.nickLast
            }
        }
    }

    /// The choices of Open at Launch in Settings' order (Go
    /// `StartModesWithCalendar`: this client has the Calendar).
    public static let startModes = StartChoice.allCases

    /// The choice a stored nick names; an unknown or empty one is Mail.
    public static func parseStartChoice(_ nick: String) -> StartChoice {
        StartChoice.allCases.first { $0.nick == nick } ?? .mail
    }

    /// The mode a new main window opens in: `start` is the key
    /// `board-start-mode`, `last` the key `board-last-mode` (written
    /// whenever the mode switches). With the board turned off
    /// (`boardEnabled` false) a Board start or last mode is Mail; the
    /// Calendar does not depend on the board and stays (Go `StartMode`).
    public static func startMode(start: String, last: String, boardEnabled: Bool) -> Mode {
        let m: Mode
        switch parseStartChoice(start) {
        case .mail: m = .mail
        case .board: m = .board
        case .calendar: m = .calendar
        case .last: m = parseMode(last) ?? .mail
        }
        if m == .board && !boardEnabled {
            return .mail
        }
        return m
    }

    /// The mode a window opens in (Go `ResolveStartMode`): `choice` is
    /// Open at Launch, `last` the mode shown last. A client without the
    /// Calendar mode (`hasCalendar` false) reads a stored Calendar start
    /// or last mode as Mail, so that a setting written by another client
    /// never leaves the window on a page it cannot show. This client has
    /// the Calendar and passes true. The Board's own rules are
    /// `startMode`'s and `startDecision`'s.
    public static func resolveStartMode(_ choice: StartChoice, last: Mode, hasCalendar: Bool) -> Mode {
        let m: Mode
        switch choice {
        case .mail: m = .mail
        case .board: m = .board
        case .calendar: m = .calendar
        case .last: m = last
        }
        if m == .calendar && !hasCalendar {
            return .mail
        }
        return m
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
    /// - a start that is the Calendar (or Last Used with the Calendar last)
    ///   needs no daemon preferences: Calendar at once, unless the user
    ///   switched or acted in Mail, then Mail;
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
        if wanted == .calendar {
            return (.calendar, true)
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
        /// The switch's segment and View menu item for the Calendar
        /// (Go `CalendarName`).
        public var calendar: String

        /// The name of `mode`'s segment.
        public func name(_ mode: Mode) -> String {
            switch mode {
            case .mail: mail
            case .board: board
            case .calendar: calendar
            }
        }
    }

    public static func texts() -> Texts {
        Texts(
            mail: L10n.T("Mail"),
            board: Text.boardName,
            calendar: Cal.Text.modeName
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
        /// New Event (⌥⌘N).
        case newEvent
    }

    /// Whether `command` may run in `mode`. Mail allows everything but the
    /// board's views; Board only what does not need the folders, the list
    /// and the reader: the list keeps its selection while hidden, and no
    /// key may act on a message the user cannot see. Calendar keeps Switch
    /// Mode, New Message, Check for New Mail and New Event; New Event is
    /// not a Board command (Go `Allows`).
    public static func allows(_ command: Command, in mode: Mode) -> Bool {
        switch mode {
        case .mail:
            return command != .boardView
        case .board:
            switch command {
            case .switchMode, .newMessage, .checkForNewMail, .boardView: return true
            case .mailView, .messageAction, .newEvent: return false
            }
        case .calendar:
            switch command {
            case .switchMode, .newMessage, .checkForNewMail, .newEvent: return true
            case .mailView, .messageAction, .boardView: return false
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
    /// window showing the board or the calendar does not count, so the
    /// folder's desktop notifications stay.
    public static func viewsMail(_ mode: Mode, windowIsKey: Bool) -> Bool {
        mode == .mail && windowIsKey
    }

    /// The layout-independent digit of a mode key's key code: ⌘1 and ⌘2
    /// (`numberRowDigit`) and ⌘3 (`kVK_ANSI_3` 0x14 → "3", Show Calendar,
    /// `Cal.keyFor`'s `showCalendar`), nil for any other key. On Czech
    /// QWERTZ the key types "š" with ⌘ too, as for ⌘1 and ⌘2.
    public static func modeKeyDigit(_ code: UInt16) -> Character? {
        if let d = numberRowDigit(code) {
            return d
        }
        return code == 0x14 ? "3" : nil
    }

    /// The mode ⌘ with `digit` (the layout-independent one) switches to in
    /// `mode`: ⌘1 Mail, ⌘2 Board (`keyFor`), ⌘3 Calendar (`Cal.keyFor`).
    public static func modeForKey(_ digit: Character, in mode: Mode) -> Mode? {
        switch keyFor(digit, primary: true, other: false, inText: false, mode: mode) {
        case .showMail: return .mail
        case .showBoard: return .board
        default: break
        }
        let press = Cal.Press(rune: digit, primary: true)
        return Cal.keyFor(press, context: Cal.KeyContext(mode: mode)) == .showCalendar ? .calendar : nil
    }
}

extension Cal.KeyContext {
    /// The calendar's key context in the window's `mode` (Go `KeyContext`'s
    /// `Mode == ModeCalendar`).
    public init(mode: Board.Mode, inText: Bool = false, selected: Bool = false) {
        self.init(calendarMode: mode == .calendar, inText: inText, selected: selected)
    }
}
