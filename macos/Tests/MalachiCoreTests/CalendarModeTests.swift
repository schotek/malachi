// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Testing
@testable import MalachiCore

// The window's Calendar mode (docs/calendar.md D27): the parts of
// ui/internal/board/mode_test.go that concern it (TestResolveStartMode,
// TestStartDecisionCalendar) and the mode keys.

@Suite struct CalendarModeTests {
    @Test func nicks() {
        #expect(Board.Mode.calendar.rawValue == 2)
        #expect(Board.Mode.calendar.nick == "calendar")
        #expect(Board.parseMode("calendar") == .calendar)
        #expect(Board.StartChoice.calendar.nick == "calendar")
        #expect(Board.parseStartChoice("calendar") == .calendar)
        #expect(Board.texts().name(.calendar) == "Calendar")
        #expect(Board.texts().name(.mail) == "Mail")
    }

    @Test func resolveStartMode() {
        let cases: [(Board.StartChoice, Board.Mode, Bool, Board.Mode)] = [
            (.mail, .calendar, true, .mail),
            (.board, .calendar, true, .board),
            (.calendar, .mail, true, .calendar),
            (.calendar, .mail, false, .mail),
            (.last, .calendar, true, .calendar),
            (.last, .calendar, false, .mail),
            (.last, .board, false, .board),
            (.last, .mail, false, .mail),
        ]
        for (choice, last, has, want) in cases {
            #expect(Board.resolveStartMode(choice, last: last, hasCalendar: has) == want, "\(choice) \(last) \(has)")
        }
    }

    @Test func startDecisionCalendar() {
        let cases: [(Board.StartChoice, Board.Mode, Bool, Bool, Board.Mode)] = [
            (.calendar, .mail, false, false, .calendar),
            (.last, .calendar, false, false, .calendar),
            (.calendar, .mail, true, false, .mail),
            (.calendar, .mail, false, true, .mail),
        ]
        for (start, last, switched, acted, want) in cases {
            let d = Board.startDecision(
                start: start, lastMode: last, prefsKnown: false, enabled: false, userSwitched: switched,
                userInteracted: acted, waited: 0)
            #expect(d.mode == want && d.decided, "\(start) \(last) \(switched) \(acted)")
        }
    }

    @Test func modeKeys() {
        #expect(Board.modeKeyDigit(0x12) == "1")
        #expect(Board.modeKeyDigit(0x13) == "2")
        #expect(Board.modeKeyDigit(0x14) == "3")
        #expect(Board.modeKeyDigit(0x15) == nil)
        for mode in Board.Mode.allCases {
            #expect(Board.modeForKey("1", in: mode) == .mail)
            #expect(Board.modeForKey("2", in: mode) == .board)
            #expect(Board.modeForKey("3", in: mode) == .calendar)
            #expect(Board.modeForKey("4", in: mode) == nil)
        }
    }

    @Test func keyContextFollowsTheMode() {
        #expect(Cal.KeyContext(mode: .calendar).calendarMode)
        #expect(!Cal.KeyContext(mode: .mail).calendarMode)
        #expect(!Cal.KeyContext(mode: .board, inText: true, selected: true).calendarMode)
        // ⌘T is the calendar's only in Calendar.
        let t = Cal.Press(rune: "t", primary: true)
        #expect(Cal.keyFor(t, context: Cal.KeyContext(mode: .calendar)) == .today)
        #expect(Cal.keyFor(t, context: Cal.KeyContext(mode: .mail)) == nil)
    }
}
