// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's keys and its Escape, the part that needs no AppKit: which
// shortcuts there are, with their texts for the menus, and where Escape
// goes. Every client binds the same keys: Primary is ⌘ on macOS, Ctrl on
// Linux and Windows. The single keys work only while the board shows and
// the keyboard is not in a text field (a reply editor, a recipient field,
// the instruction of Suggest Reply), so typing never acts on a case.
//
// A 1:1 port of the Go reference ui/internal/board (keys.go), which holds
// the msgids.

import Foundation

extension Board {
    /// What a key of the board does.
    public enum KeyAction: Sendable, Equatable, CaseIterable {
        /// Switches the main window to Mail (⌘1).
        case showMail
        /// Switches it to the Board (⌘2).
        case showBoard
        /// Archive on the selected case (E).
        case archive
        /// Done on the selected case, or Move Back to Board on a done one
        /// (D).
        case done
        /// Opens Remind… on the selected case (R).
        case remind
    }

    /// A shortcut of the board.
    public struct Key: Sendable, Equatable {
        public var action: KeyAction
        /// With the platform's primary modifier (⌘); else a single key
        /// without modifiers.
        public var primary: Bool
        /// The key, lower case: "1", "2", "e", "d", "r".
        public var character: Character
        /// Only while the board shows and the keyboard is not in a text
        /// field. The mode keys work in both modes.
        public var boardOnly: Bool
        /// The shortcut's text (the menu item's title).
        public var title: String
    }

    /// The board's shortcuts in the shortcuts list's order.
    public static func boardKeys() -> [Key] {
        [
            Key(action: .showMail, primary: true, character: "1", boardOnly: false, title: Text.showMailKey),
            Key(action: .showBoard, primary: true, character: "2", boardOnly: false, title: Text.showBoardKey),
            Key(action: .archive, primary: false, character: "e", boardOnly: true, title: Text.archive),
            Key(action: .done, primary: false, character: "d", boardOnly: true, title: Text.doneKey),
            Key(action: .remind, primary: false, character: "r", boardOnly: true, title: Text.remind),
        ]
    }

    /// The heading of the board's group of shortcuts.
    public static var keysGroup: String { Text.boardName }

    /// The layout-independent digit of a key code of the number row
    /// (`kVK_ANSI_1` 0x12 → "1", `kVK_ANSI_2` 0x13 → "2"), nil for any
    /// other key. ⌘1 and ⌘2 go by the key, not by the character the layout
    /// types (Go `KeyFor`'s rule): on Czech QWERTZ the key types "+" and
    /// "ě" with ⌘ too (measured with `UCKeyTranslate` on the system's
    /// Czech and Czech-QWERTY layouts), so the View menu's key equivalents
    /// "1" and "2" would not match there; the window catches the key code
    /// and asks `keyFor` with this digit (Go `NumberRowDigit`, which reads
    /// GTK's hardware keycodes).
    public static func numberRowDigit(_ code: UInt16) -> Character? {
        switch code {
        case 0x12: return "1"
        case 0x13: return "2"
        default: return nil
        }
    }

    /// The action of a key pressed: `character` is the key (any case; for
    /// ⌘1 and ⌘2 the layout-independent digit, `numberRowDigit`, never
    /// the character the layout types), `primary` whether the primary
    /// modifier is held and `other` whether any other modifier (Shift,
    /// Option, Control) is; `inText` whether the keyboard is in a text
    /// field. nil when the key is not the board's.
    public static func keyFor(
        _ character: Character, primary: Bool, other: Bool, inText: Bool, mode: Mode
    ) -> KeyAction? {
        guard !other else { return nil }
        // ASCII letters only are folded: the keys are ASCII.
        var c = character
        if let a = c.asciiValue, a >= 0x41, a <= 0x5A {
            c = Character(Unicode.Scalar(a + 0x20))
        }
        guard let k = boardKeys().first(where: { $0.character == c && $0.primary == primary }) else { return nil }
        if k.boardOnly && (mode != .board || inText) {
            return nil
        }
        return k.action
    }

    /// What Escape does on the board.
    public enum EscapeTarget: Sendable, Equatable {
        /// Escape does nothing here (the List's own page).
        case nothing
        /// Closes the open popup (a menu, a popover, the recipients'
        /// suggestions) and nothing else.
        case closePopup
        /// Moves the keyboard from the reply editor or a recipient field
        /// to the detail's state pill; nothing closes.
        case focusStatePill
        /// Closes the sliding detail panel (Columns, Today, a narrow List).
        case closePanel
    }

    /// Where Escape goes, in two steps: an open popup closes first; the
    /// keyboard in the editor or the recipient fields goes to the state
    /// pill; anywhere else on the page an open panel closes. Without a
    /// panel nothing happens.
    public static func escapeFor(focusInEditorOrRecipients: Bool, popupOpen: Bool, panelOpen: Bool) -> EscapeTarget {
        if popupOpen {
            return .closePopup
        }
        if focusInEditorOrRecipients {
            return .focusStatePill
        }
        return panelOpen ? .closePanel : .nothing
    }
}

extension Board.Text {
    /// The shortcut's text of ⌘1.
    public static var showMailKey: String { L10n.T("Show Mail") }
    /// The shortcut's text of ⌘2.
    public static var showBoardKey: String { L10n.T("Show Board") }
    /// The shortcut's text of D.
    public static var doneKey: String { L10n.T("Mark as Done or Move Back to Board") }
}
