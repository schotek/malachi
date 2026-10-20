// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The chat layout of a case's conversation on the board
// (Board.chatRows, chatOpens, the toggle's texts) and the sender and date
// the detail gives each member. Port of
// ui/internal/board/conversation_chat_test.go.

private typealias F = BoardFixture

@Suite struct BoardConversationChatTests {
    private static func cards(_ mine: [Bool]) -> [Board.MessageCard] {
        mine.enumerated().map { i, m in
            Board.MessageCard(
                id: MessageID("m" + String(UnicodeScalar(UInt8(97 + i)))), from: "", when: "", text: "", mine: m)
        }
    }

    private static func bubbles(_ pairs: [(Int, Bool)]) -> [Board.ChatBubble] {
        pairs.map { Board.ChatBubble(index: $0.0, right: $0.1) }
    }

    @Test func chatRowsSidesAndOrder() {
        #expect(Board.chatRows([]) == [])
        #expect(
            Board.chatRows(Self.cards([false, true, false, true]))
                == Self.bubbles([(0, false), (1, true), (2, false), (3, true)]))
        #expect(Board.chatRows(Self.cards([true, true])) == Self.bubbles([(0, true), (1, true)]))
        #expect(
            Board.chatRows(Self.cards([false, false, false]))
                == Self.bubbles([(0, false), (1, false), (2, false)]))
        // Samples (no id) are bubbles too.
        let samples = [
            Board.MessageCard(id: nil, from: "Ann", when: "", text: "", mine: false),
            Board.MessageCard(id: nil, from: "You", when: "", text: "", mine: true),
        ]
        #expect(Board.chatRows(samples) == Self.bubbles([(0, false), (1, true)]))
    }

    @Test func chatOpens() {
        #expect(Board.chatOpens(Board.MessageCard(id: MessageID("m1"), from: "", when: "", text: "", mine: false)))
        #expect(Board.chatOpens(Board.MessageCard(id: MessageID("m1"), from: "", when: "", text: "", mine: true)))
        #expect(!Board.chatOpens(Board.MessageCard(id: nil, from: "Ann", when: "", text: "sample", mine: false)))
    }

    @Test func chatToggleTexts() {
        #expect(Board.Text.chatToggleLabel(on: false) == "Show as Chat")
        #expect(Board.Text.chatToggleLabel(on: true) == "Show as Thread")
        #expect(
            Board.Text.chatToggleTooltip(on: false)
                == "Others on the left, your messages on the right; double-click opens the message")
        #expect(
            Board.Text.chatToggleTooltip(on: true)
                == "Back to the cards: the messages as cards, oldest first, the newest open")
    }

    @Test func detailMessagesSenderAndDate() throws {
        let ann = F.ago(5), me = F.ago(1)
        let m = [
            Board.CaseMessage(id: MessageID("m1"), from: "Ann\u{200B} Example", date: ann, text: "hi"),
            Board.CaseMessage(id: MessageID("m2"), from: "Me Myself", date: me, text: "hello", mine: true),
        ]
        let d = try #require(F.view([F.mk("c1", count: 2, messages: m)]).detail)
        try #require(d.messages.count == 2)
        let theirs = d.messages[0], mine = d.messages[1]
        #expect(theirs.from == "Ann Example")
        #expect(theirs.sender == "Ann Example")
        #expect(mine.from == Board.Text.you)
        #expect(mine.sender == "Me Myself")
        #expect(theirs.date == ann && mine.date == me)
        #expect(!theirs.when.isEmpty && !mine.when.isEmpty)  // the cards' short dates stay
        let rows = Board.chatRows(d.messages)
        #expect(rows[1].right && !rows[0].right)
    }
}
