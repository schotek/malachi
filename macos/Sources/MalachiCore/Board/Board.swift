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
    }

    /// The mode a new main window starts in. The mode is not remembered.
    public static let initialMode: Mode = .mail

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
