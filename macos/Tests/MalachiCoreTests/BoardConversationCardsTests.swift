// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board detail's conversation cards (Board.ConversationCards): what a
// card shows, the live web view limit and which card gives way, which
// refreshes touch the cards, and the scroll compensation for a card whose
// height changes above the viewport.

@Suite struct BoardConversationCardsTests {
    typealias Cards = Board.ConversationCards
    typealias Member = Cards.Member

    static let caseA = Board.CaseID("case-a")
    static let caseB = Board.CaseID("case-b")

    static func member(_ n: Int, _ text: String? = nil) -> Member {
        Member(id: MessageID("m\(n)"), text: text ?? "text \(n)")
    }

    static func key(_ ns: [Int], case c: Board.CaseID = caseA) -> Cards.Key {
        Cards.Key(caseID: c, members: ns.map { member($0) })
    }

    @Test func firstCaseResetsNewestOpenOthersFolded() {
        var cards = Cards()
        #expect(cards.apply(Self.key([1, 2, 3])) == .reset)
        #expect(cards.newest == 2)
        #expect(cards.isFolded(0) && cards.isFolded(1) && !cards.isFolded(2))
        #expect(cards.shows(0, live: true) == .folded)
        #expect(cards.shows(2, live: true) == .text)
        #expect(!cards.foldable(2) && cards.foldable(0))
        // Only the open card with an id asks.
        #expect(!cards.needsBody(0) && cards.needsBody(2))
    }

    @Test func sameKeyChangesNothing() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        cards.asked(1)
        _ = cards.answered(1, html: true)
        let before = cards
        #expect(cards.apply(Self.key([1, 2])) == .none)
        #expect(cards == before)
        #expect(cards.shows(1, live: true) == .web)
    }

    @Test func keyIgnoresTheRestOfTheDetail() {
        let a = Board.MessageCard(id: MessageID("m1"), from: "Ann", when: "09:00", text: "Hi", mine: false)
        let b = Board.MessageCard(id: MessageID("m1"), from: "Ann B.", when: "Yesterday", text: "Hi", mine: true)
        #expect(Cards.Member(id: a.id, text: a.text) == Cards.Member(id: b.id, text: b.text))
    }

    @Test func otherCaseResets() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        _ = cards.setFolded(0, false)
        #expect(cards.apply(Self.key([1, 2], case: Self.caseB)) == .reset)
        #expect(cards.isFolded(0))
        #expect(cards.body(1) == .unknown)
    }

    @Test func newMessageKeepsTheRestAndOpensTheNewest() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        cards.asked(1)
        _ = cards.answered(1, html: true)
        _ = cards.setFolded(0, false)
        let change = cards.apply(Self.key([1, 2, 3]))
        #expect(change == .members(kept: [0: 0, 1: 1], reload: []))
        // Kept as they were; the new newest opens and asks.
        #expect(!cards.isFolded(0) && !cards.isFolded(1) && !cards.isFolded(2))
        #expect(cards.body(1) == .html)
        #expect(cards.needsBody(2))
    }

    @Test func changedExcerptKeepsTheCardAndAsksToReloadAnOpenOne() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2, 3]))
        _ = cards.setFolded(0, false)
        cards.asked(0)
        _ = cards.answered(0, html: true)
        cards.asked(2)
        _ = cards.answered(2, html: true)
        let rebuilt = Cards.Key(
            caseID: Self.caseA, members: [Self.member(1, "edited"), Self.member(2, "edited"), Self.member(3)])
        // 0 open with HTML: reloaded in place; 1 folded: new body later.
        #expect(cards.apply(rebuilt) == .members(kept: [0: 0, 1: 1, 2: 2], reload: [0]))
        #expect(!cards.isFolded(0) && cards.isFolded(1) && !cards.isFolded(2))
        #expect(cards.members[0].text == "edited")
        // The old body stays on show until the new one arrives.
        #expect(cards.body(0) == .html)
        #expect(cards.shows(0, live: true) == .web)
        #expect(!cards.needsBody(0))
        #expect(cards.body(1) == .unknown)
        // Opened later, the folded one asks.
        _ = cards.setFolded(1, false)
        #expect(cards.needsBody(1))
        // Its recency was kept: 0 is still the older of the opened.
        #expect(cards.webCards == [0, 2])
    }

    @Test func changedExcerptOfAnInFlightBodyAsksAgain() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        cards.asked(1)
        let rebuilt = Cards.Key(caseID: Self.caseA, members: [Self.member(1), Self.member(2, "edited")])
        #expect(cards.apply(rebuilt) == .members(kept: [0: 0, 1: 1], reload: []))
        #expect(cards.body(1) == .unknown)
        #expect(cards.needsBody(1))
    }

    @Test func changedExcerptWithANewMemberAndARemovedOne() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2, 3]))
        // 2 is gone, 1 edited, 4 is new and the newest.
        let next = Cards.Key(caseID: Self.caseA, members: [Self.member(1, "edited"), Self.member(3), Self.member(4)])
        #expect(cards.apply(next) == .members(kept: [0: 0, 1: 2], reload: []))
        #expect(cards.isFolded(0) && !cards.isFolded(2))
        #expect(cards.needsBody(2))
    }

    @Test func removedMemberGoesAndRepeatedTextsKeepTheirOwnCards() {
        var cards = Cards()
        let s = Member(id: nil, text: "same")
        _ = cards.apply(Cards.Key(caseID: Self.caseA, members: [s, s, Self.member(3)]))
        _ = cards.setFolded(1, false)
        #expect(cards.apply(Cards.Key(caseID: Self.caseA, members: [s, s])) == .members(kept: [0: 0, 1: 1], reload: []))
        #expect(cards.isFolded(0))
        // The second, opened before, is now the newest and open.
        #expect(!cards.isFolded(1))
    }

    @Test func samplesNeverAskAndStayText() {
        var cards = Cards()
        _ = cards.apply(Cards.Key(caseID: Self.caseA, members: [Member(id: nil, text: "a"), Member(id: nil, text: "b")]))
        #expect(!cards.needsBody(1))
        #expect(cards.shows(1, live: true) == .text)
        #expect(!cards.arrow(0, long: false, canFetch: true))
        #expect(cards.arrow(0, long: true, canFetch: true))
    }

    @Test func arrowRules() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        #expect(cards.arrow(0, long: false, canFetch: true))
        #expect(!cards.arrow(0, long: nil, canFetch: false))
        #expect(cards.arrow(0, long: true, canFetch: false))
        // The newest never folds.
        #expect(!cards.arrow(1, long: true, canFetch: true))
    }

    @Test func bodyOutcomes() {
        var cards = Cards()
        _ = cards.apply(Self.key([1]))
        #expect(cards.needsBody(0))
        cards.asked(0)
        #expect(!cards.needsBody(0))
        #expect(cards.body(0) == .asked)
        #expect(cards.shows(0, live: true) == .text)
        // No HTML part, withheld, failed, gone: the excerpt.
        _ = cards.answered(0, html: false)
        #expect(cards.shows(0, live: true) == .text)
        _ = cards.answered(0, html: true)
        #expect(cards.shows(0, live: true) == .web)
        // Not live (hidden, out of a window): the excerpt.
        #expect(cards.shows(0, live: false) == .text)
        // Asked twice changes nothing once answered.
        cards.asked(0)
        #expect(cards.body(0) == .html)
    }

    @Test func foldedCardHoldsNoWebView() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        _ = cards.setFolded(0, false)
        cards.asked(0)
        _ = cards.answered(0, html: true)
        #expect(cards.shows(0, live: true) == .web)
        _ = cards.setFolded(0, true)
        #expect(cards.shows(0, live: true) == .folded)
        #expect(cards.webCards.isEmpty)
        // Opened again: the body is known, no second ask.
        _ = cards.setFolded(0, false)
        #expect(!cards.needsBody(0))
        #expect(cards.shows(0, live: true) == .web)
    }

    @Test func newestCannotBeFolded() {
        var cards = Cards()
        _ = cards.apply(Self.key([1, 2]))
        #expect(cards.setFolded(1, true).isEmpty)
        #expect(!cards.isFolded(1))
    }

    /// Six members; all opened with HTML in the order 0, 1, 2, 3, 4.
    private func openAll(limit: Int = 4) -> (Cards, [[Int]]) {
        var cards = Cards(maxLive: limit)
        _ = cards.apply(Self.key([1, 2, 3, 4, 5, 6]))
        var gave: [[Int]] = []
        cards.asked(5)
        gave.append(cards.answered(5, html: true))
        for i in 0..<5 {
            gave.append(cards.setFolded(i, false))
            cards.asked(i)
            gave.append(cards.answered(i, html: true))
        }
        return (cards, gave)
    }

    @Test func limitFoldsTheLeastRecentlyOpened() {
        let (cards, gave) = openAll()
        // The newest (5) and 0, 1, 2 fill the four; 3's HTML folds 0, 4's
        // folds 1.
        #expect(gave.flatMap { $0 } == [0, 1])
        #expect(cards.webCards == [2, 3, 4, 5])
        #expect(cards.isFolded(0) && cards.isFolded(1))
    }

    @Test func limitSparesTheNewestAndTheCardJustOpened() {
        var cards = Cards(maxLive: 1)
        _ = cards.apply(Self.key([1, 2, 3]))
        cards.asked(2)
        #expect(cards.answered(2, html: true).isEmpty)
        _ = cards.setFolded(0, false)
        cards.asked(0)
        // Over the limit, but the newest and the card just opened stay.
        #expect(cards.answered(0, html: true).isEmpty)
        #expect(cards.webCards == [0, 2])
        // Opening another: the earlier opened one gives way.
        _ = cards.setFolded(1, false)
        cards.asked(1)
        #expect(cards.answered(1, html: true) == [0])
    }

    @Test func reopeningMakesACardTheMostRecent() {
        var cards = Cards(maxLive: 3)
        _ = cards.apply(Self.key([1, 2, 3, 4]))
        cards.asked(3)
        _ = cards.answered(3, html: true)
        for i in [0, 1] {
            _ = cards.setFolded(i, false)
            cards.asked(i)
            _ = cards.answered(i, html: true)
        }
        // 0 folded and opened again: 1 is now the least recent.
        _ = cards.setFolded(0, true)
        _ = cards.setFolded(0, false)
        _ = cards.setFolded(2, false)
        cards.asked(2)
        #expect(cards.answered(2, html: true) == [1])
    }

    @Test func textCardsDoNotCount() {
        var cards = Cards(maxLive: 1)
        _ = cards.apply(Self.key([1, 2, 3]))
        cards.asked(2)
        _ = cards.answered(2, html: false)
        _ = cards.setFolded(0, false)
        cards.asked(0)
        #expect(cards.answered(0, html: true).isEmpty)
        _ = cards.setFolded(1, false)
        cards.asked(1)
        #expect(cards.answered(1, html: false).isEmpty)
        #expect(cards.webCards == [0])
    }

    @Test func cardThatCannotShowHTMLFreesItsSlot() {
        var cards = Cards(maxLive: 2)
        _ = cards.apply(Self.key([1, 2, 3, 4]))
        cards.asked(3)
        _ = cards.answered(3, html: true)
        _ = cards.setFolded(0, false)
        cards.asked(0)
        _ = cards.answered(0, html: true)
        #expect(cards.webCards == [0, 3])
        // 0's web view is unavailable: it no longer counts.
        #expect(cards.answered(0, html: false).isEmpty)
        #expect(cards.webCards == [3])
        #expect(cards.shows(0, live: true) == .text)
        // Another opens with HTML: nothing folds back.
        _ = cards.setFolded(1, false)
        cards.asked(1)
        #expect(cards.answered(1, html: true).isEmpty)
        #expect(!cards.isFolded(0) && !cards.isFolded(1))
        #expect(cards.webCards == [1, 3])
    }

    @Test func enforceWithNoCandidatesFoldsNothing() {
        var cards = Cards(maxLive: 1)
        _ = cards.apply(Self.key([1, 2]))
        cards.asked(1)
        _ = cards.answered(1, html: true)
        _ = cards.setFolded(0, false)
        cards.asked(0)
        // Newest and the protected card are both spared.
        #expect(cards.answered(0, html: true).isEmpty)
        #expect(cards.webCards == [0, 1])
    }

    @Test func compensation() {
        // A card above the viewport grew by 200: the viewport follows.
        #expect(Cards.compensatedTop(viewportTop: 500, cardMaxY: 400, delta: 200, documentHeight: 2000, viewportHeight: 600) == 700)
        // Ending exactly at the viewport's top counts as above.
        #expect(Cards.compensatedTop(viewportTop: 500, cardMaxY: 500, delta: -100, documentHeight: 2000, viewportHeight: 600) == 400)
        // In view or below: nothing above it moves.
        #expect(Cards.compensatedTop(viewportTop: 500, cardMaxY: 800, delta: 200, documentHeight: 2000, viewportHeight: 600) == 500)
        // Clamped to the document.
        #expect(Cards.compensatedTop(viewportTop: 500, cardMaxY: 300, delta: 800, documentHeight: 1000, viewportHeight: 600) == 400)
        #expect(Cards.compensatedTop(viewportTop: 50, cardMaxY: 20, delta: -300, documentHeight: 1000, viewportHeight: 600) == 0)
    }
}
