// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalendarKeys.swift, the cases of Go keys_test.go.

private typealias F = CalFixture

@Suite struct CalendarKeysTests {
    @Test func keyFor() {
        let cal = Cal.KeyContext(calendarMode: true)
        let sel = Cal.KeyContext(calendarMode: true, selected: true)
        let mail = Cal.KeyContext(calendarMode: false)
        let text = Cal.KeyContext(calendarMode: true, inText: true)
        typealias P = Cal.Press
        let cases: [(String, P, Cal.KeyContext, Cal.KeyAction?)] = [
            ("⌘3 in Mail", P(rune: "3", primary: true), mail, .showCalendar),
            ("⌘T", P(rune: "t", primary: true), cal, .today),
            ("⌘T upper case", P(rune: "T", primary: true), cal, .today),
            ("⌘T in Mail", P(rune: "t", primary: true), mail, nil),
            ("⌘←", P(special: .left, primary: true), cal, .previous),
            ("⌘→", P(special: .right, primary: true), cal, .next),
            ("⌘→ in the search field", P(special: .right, primary: true), text, .next),
            ("⌃⌘1", P(rune: "1", primary: true, control: true), cal, .dayView),
            ("⌃⌘2", P(rune: "2", primary: true, control: true), cal, .weekView),
            ("⌃⌘3", P(rune: "3", primary: true, control: true), cal, .monthView),
            ("⌃⌘4", P(rune: "4", primary: true, control: true), cal, .agendaView),
            ("⌃⌘4 in Mail", P(rune: "4", primary: true, control: true), mail, nil),
            ("Ctrl+Shift+3 elsewhere", P(rune: "3", primary: true, shift: true, shiftModifier: true), cal, .monthView),
            ("⇧⌘3 on macOS", P(rune: "3", primary: true, shift: true), cal, nil),
            ("⌥⌘N", P(rune: "n", primary: true, alt: true), mail, .newEvent),
            ("Ctrl+Shift+N elsewhere", P(rune: "n", primary: true, shift: true, shiftModifier: true), mail, .newEvent),
            ("Ctrl+Alt+N elsewhere", P(rune: "n", primary: true, alt: true, shiftModifier: true), mail, nil),
            ("⌘N is New Message", P(rune: "n", primary: true), cal, nil),
            ("Return with a selection", P(special: .return), sel, .openDetail),
            ("Return without one", P(special: .return), cal, nil),
            ("⌫ with a selection", P(special: .delete), sel, .remove),
            ("⌫ in text", P(special: .delete), Cal.KeyContext(calendarMode: true, inText: true, selected: true), nil),
            ("Escape", P(special: .escape), cal, .escape),
            ("↑", P(special: .up), cal, .selectPrevious),
            ("←", P(special: .left), cal, .selectPrevious),
            ("↓", P(special: .down), cal, .selectNext),
            ("→ in text", P(special: .right), text, nil),
            ("x", P(rune: "x"), cal, nil),
        ]
        for (name, p, ctx, want) in cases {
            #expect(Cal.keyFor(p, context: ctx) == want, "\(name)")
        }
        for k in Cal.calendarKeys where k.title.isEmpty {
            #expect(k.action == .escape)
        }
    }

    @Test func escapeFor() {
        let cases: [(Bool, Bool, Cal.EscapeTarget)] = [
            (true, true, .closePopover), (true, false, .closePopover), (false, true, .clearSearch), (false, false, .nothing),
        ]
        for (popover, search, want) in cases {
            #expect(Cal.escapeFor(popoverOpen: popover, searching: search) == want)
        }
    }

    @Test func selectionOrder() {
        let p = F.prague
        var broken = F.occ()
        broken.eventId = "broken"
        let occ = [
            F.timed("b", F.at(2026, 10, 14, 10), F.at(2026, 10, 14, 11)),
            F.timed("a", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 10)),
            F.allDayOcc("allday", F.at(2026, 10, 14), 1),
            F.timed("c", F.at(2026, 10, 13, 18), F.at(2026, 10, 13, 19)),
            F.timed("a", F.at(2026, 10, 14, 9), F.at(2026, 10, 14, 10)),
            broken,
        ]
        let order = Cal.selectionOrder(occ, zone: p)
        #expect(order == ["c|", "allday|", "a|", "b|"])
        let moves: [(String, Int, String)] = [
            ("a|", 1, "b|"), ("a|", -1, "allday|"), ("b|", 1, "b|"), ("c|", -1, "c|"), ("", 1, "c|"), ("", -1, "b|"), ("gone", 1, "c|"),
        ]
        for (cur, delta, want) in moves {
            #expect(Cal.moveSelection(order, current: cur, delta: delta) == want)
        }
        #expect(Cal.moveSelection([], current: "a", delta: 1) == "")
    }
}
