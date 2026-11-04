// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar's keys (Go keys.go), the part that needs no AppKit: which
// shortcuts there are with their texts, what a key does in which context,
// where Escape goes, and how the arrows move the selection. Primary is ⌘,
// Alt ⌥ and Control ⌃ on macOS; Linux and Windows use Shift for both of
// the last two (`Press.shiftModifier`).

import Foundation

extension Cal {
    /// keys.go `KeyAction`.
    public enum KeyAction: Int, Sendable, CaseIterable {
        /// Switches the main window to the Calendar (⌘3, in every mode).
        case showCalendar
        /// ⌘T.
        case today
        /// ⌘← and ⌘→.
        case previous, next
        /// ⌃⌘1–4.
        case dayView, weekView, monthView, agendaView
        /// ⌥⌘N (⌘N stays New Message).
        case newEvent
        /// Return: the selected event's detail.
        case openDetail
        /// ⌫: delete or remove the selected event, with its question.
        case remove
        /// Escape (`escapeFor` says what it does).
        case escape
        /// ← ↑ and → ↓: the selection among the occurrences in time order.
        case selectPrevious, selectNext
    }

    /// keys.go `Special`: a key without a character.
    public enum Special: Int, Sendable, CaseIterable {
        case none, left, right, up, down, `return`
        /// ⌫ on macOS, Delete (and Backspace) elsewhere.
        case delete
        case escape
    }

    /// keys.go `Key`: a shortcut of the calendar.
    public struct Key: Sendable, Equatable {
        public var action: KeyAction
        /// Lower case ("t", "n", "1"…"4"), or nil with `special`.
        public var rune: Character?
        public var special: Special = .none
        public var primary = false
        public var alt = false
        public var control = false
        /// Only in Calendar mode.
        public var calendarOnly = false
        /// Not while the keyboard is in a text field.
        public var outsideText = false
        /// The shortcut's text in the menus ("" for Escape).
        public var title = ""
    }

    /// keys.go `Press`: a key as a client received it (the digits as the
    /// physical number-row key).
    public struct Press: Sendable, Equatable {
        public var rune: Character?
        public var special: Special
        public var primary: Bool
        public var alt: Bool
        public var control: Bool
        public var shift: Bool
        /// Shift stands for Alt and Control (Linux, Windows).
        public var shiftModifier: Bool

        public init(
            rune: Character? = nil, special: Special = .none, primary: Bool = false, alt: Bool = false,
            control: Bool = false, shift: Bool = false, shiftModifier: Bool = false
        ) {
            self.rune = rune
            self.special = special
            self.primary = primary
            self.alt = alt
            self.control = control
            self.shift = shift
            self.shiftModifier = shiftModifier
        }

        func matches(_ k: Key, _ r: Character?) -> Bool {
            guard k.rune == r, k.special == special, k.primary == primary else { return false }
            if shiftModifier {
                return !alt && !control && (k.alt || k.control) == shift
            }
            return !shift && k.alt == alt && k.control == control
        }
    }

    /// keys.go `KeyContext`. `calendarMode` stands for Go's `Mode ==
    /// ModeCalendar` until the window's mode has a calendar case.
    public struct KeyContext: Sendable, Equatable {
        public var calendarMode: Bool
        /// The keyboard is in a text field.
        public var inText: Bool
        /// An occurrence is selected.
        public var selected: Bool

        public init(calendarMode: Bool, inText: Bool = false, selected: Bool = false) {
            self.calendarMode = calendarMode
            self.inText = inText
            self.selected = selected
        }
    }

    /// keys.go `CalendarKeys`: in the shortcuts window's order.
    public static var calendarKeys: [Key] {
        [
            Key(action: .showCalendar, rune: "3", primary: true, title: showCalendarKey),
            Key(action: .today, rune: "t", primary: true, calendarOnly: true, title: Text.today),
            Key(action: .previous, special: .left, primary: true, calendarOnly: true, title: Text.previous),
            Key(action: .next, special: .right, primary: true, calendarOnly: true, title: Text.next),
            Key(action: .dayView, rune: "1", primary: true, control: true, calendarOnly: true, title: Text.viewTitle(.day)),
            Key(action: .weekView, rune: "2", primary: true, control: true, calendarOnly: true, title: Text.viewTitle(.week)),
            Key(action: .monthView, rune: "3", primary: true, control: true, calendarOnly: true, title: Text.viewTitle(.month)),
            Key(action: .agendaView, rune: "4", primary: true, control: true, calendarOnly: true, title: Text.viewTitle(.agenda)),
            Key(action: .newEvent, rune: "n", primary: true, alt: true, title: Text.newEvent),
            Key(action: .openDetail, special: .return, calendarOnly: true, outsideText: true, title: openDetailKey),
            Key(action: .remove, special: .delete, calendarOnly: true, outsideText: true, title: removeKey),
            Key(action: .escape, special: .escape, calendarOnly: true),
            Key(action: .selectPrevious, special: .left, calendarOnly: true, outsideText: true, title: selectPreviousKey),
            Key(action: .selectPrevious, special: .up, calendarOnly: true, outsideText: true, title: selectPreviousKey),
            Key(action: .selectNext, special: .right, calendarOnly: true, outsideText: true, title: selectNextKey),
            Key(action: .selectNext, special: .down, calendarOnly: true, outsideText: true, title: selectNextKey),
        ]
    }

    public static var keysGroup: String { Text.modeName }
    public static var showCalendarKey: String { L10n.T("Show Calendar") }
    public static var openDetailKey: String { L10n.T("Show Event Details") }
    public static var removeKey: String { L10n.T("Delete or Remove Event") }
    public static var selectPreviousKey: String { L10n.T("Select Previous Event") }
    public static var selectNextKey: String { L10n.T("Select Next Event") }

    /// keys.go `KeyFor`: nil when the key is not the calendar's there; a
    /// key that acts on the selection needs one.
    public static func keyFor(_ p: Press, context ctx: KeyContext) -> KeyAction? {
        var r = p.rune
        if let c = r, let a = c.asciiValue, a >= 65, a <= 90 {
            r = Character(Unicode.Scalar(a + 32))
        }
        for k in calendarKeys where p.matches(k, r) {
            if k.calendarOnly && !ctx.calendarMode || k.outsideText && ctx.inText
                || (k.action == .openDetail || k.action == .remove) && !ctx.selected
            {
                return nil
            }
            return k.action
        }
        return nil
    }

    /// keys.go `EscapeTarget`.
    public enum EscapeTarget: Int, Sendable, CaseIterable {
        case nothing, closePopover, clearSearch
    }

    /// keys.go `EscapeFor`: an open popover closes first, then the search
    /// is cleared.
    public static func escapeFor(popoverOpen: Bool, searching: Bool) -> EscapeTarget {
        popoverOpen ? .closePopover : (searching ? .clearSearch : .nothing)
    }

    /// keys.go `SelectionOrder`: the occurrences' keys by day, all-day
    /// first, by start, end and key; duplicates once.
    public static func selectionOrder(_ occurrences: [Occurrence], zone: TimeZone) -> [String] {
        struct Entry {
            var key: String
            var day: Date
            var allDay: Bool
            var start: Date
            var end: Date
        }
        var es: [Entry] = []
        var seen = Set<String>()
        for o in occurrences {
            let k = occurrenceKey(o)
            guard let (s, e) = span(o, zone), !seen.contains(k) else { continue }
            seen.insert(k)
            es.append(Entry(key: k, day: startOfDay(s, zone), allDay: o.allDay, start: s, end: e))
        }
        return stableSorted(es) { a, b in
            let c = compare(a.day, b.day)
            if c != 0 { return c }
            if a.allDay != b.allDay { return a.allDay ? -1 : 1 }
            return or(compare(a.start, b.start), compare(a.end, b.end), compareBytes(a.key, b.key))
        }.map(\.key)
    }

    /// keys.go `MoveSelection`: clamped to the ends; without a current one
    /// the first forward and the last back; "" for an empty order.
    public static func moveSelection(_ order: [String], current: String, delta: Int) -> String {
        guard !order.isEmpty else { return "" }
        guard let i = order.firstIndex(of: current) else {
            return delta < 0 ? order[order.count - 1] : order[0]
        }
        return order[min(max(i + delta, 0), order.count - 1)]
    }
}
