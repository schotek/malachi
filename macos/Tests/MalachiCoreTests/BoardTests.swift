// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Testing
@testable import MalachiCore

// Swift-first (MalachiCore/Board/Board.swift); the GTK port brings
// ui/internal/board/board_test.go of the same shape.

@Suite struct BoardTests {
    @Test func initialMode() {
        #expect(Board.initialMode == .mail)
        #expect(Board.Mode.allCases.map(\.rawValue) == [0, 1])
    }

    @Test func allows() {
        let cases: [(Board.Command, Board.Mode, Bool)] = [
            (.switchMode, .mail, true),
            (.newMessage, .mail, true),
            (.checkForNewMail, .mail, true),
            (.mailView, .mail, true),
            (.messageAction, .mail, true),
            (.boardView, .mail, false),
            (.switchMode, .board, true),
            (.newMessage, .board, true),
            (.checkForNewMail, .board, true),
            (.mailView, .board, false),
            (.messageAction, .board, false),
            (.boardView, .board, true),
        ]
        #expect(cases.count == Board.Command.allCases.count * Board.Mode.allCases.count)
        for (command, mode, want) in cases {
            #expect(Board.allows(command, in: mode) == want, "allows(\(command), \(mode))")
        }
    }

    @Test func modeForRequest() {
        let cases: [(Board.Request, Board.Mode, Board.Mode)] = [
            (.showOutbox, .mail, .mail),
            (.showOutbox, .board, .mail),
            (.revealAssistant, .mail, .mail),
            (.revealAssistant, .board, .mail),
            (.openMessageWindow, .mail, .mail),
            (.openMessageWindow, .board, .board),
            (.compose, .mail, .mail),
            (.compose, .board, .board),
        ]
        #expect(cases.count == Board.Request.allCases.count * Board.Mode.allCases.count)
        for (request, current, want) in cases {
            #expect(Board.mode(for: request, current: current) == want, "mode(for: \(request), current: \(current))")
        }
    }

    @Test func viewsMail() {
        let cases: [(Board.Mode, Bool, Bool)] = [
            (.mail, true, true),
            (.mail, false, false),
            (.board, true, false),
            (.board, false, false),
        ]
        for (mode, key, want) in cases {
            #expect(Board.viewsMail(mode, windowIsKey: key) == want, "viewsMail(\(mode), \(key))")
        }
    }

    @Test func texts() {
        let t = Board.texts()
        for s in [t.mail, t.board] {
            #expect(!s.isEmpty)
        }
        #expect(t.mail != t.board)
    }
}
