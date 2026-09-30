// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The arithmetic of the conversation view (ConversationLayout.swift): which
// cards are near the viewport and which of them get a web view, where the
// viewport goes to keep an item in place and for a page of Space, and how a
// card's web view follows its document's height (WebHeightGovernor); and
// the attachment chips' plan (ChipPlan.swift); the order the pane shows the
// items in (`displayOrder`, ported with conversation_layout_test.go
// `TestConvDisplayOrder`); the timeline beside the cards (which item has an
// avatar and which a dot, where the line runs) and where the parts of a
// card's header go (ConversationHeaderLayout).

private typealias Span = ConversationLayout.Span

/// `n` items of `height` each, stacked from 0.
private func stacked(_ n: Int, _ height: Double) -> [Span] {
    (0..<n).map { Span(Double($0) * height, Double($0 + 1) * height) }
}

struct ConversationLayoutTests {
    @Test func spanDistance() {
        let s = Span(100, 200)
        #expect(s.distance(to: Span(150, 400)) == 0)
        #expect(s.distance(to: Span(200, 300)) == 0, "touching counts as overlapping")
        #expect(s.distance(to: Span(250, 300)) == 50)
        #expect(s.distance(to: Span(0, 40)) == 60)
        #expect(Span(10, 5).maxY == 10, "an inverted span is empty at its top")
    }

    @Test func liveWindowIsTwoScreensEachWay() {
        // 40 items of 100 in a viewport of 300 at 1000..1300: near is
        // 400..1900 (two screens each way), items 4 to 18 (touching counts).
        let frames = stacked(40, 100)
        let live = ConversationLayout.live(frames: frames, html: Array(repeating: false, count: 40), visible: Span(1000, 1300))
        #expect(live.near == Set(3...19))
        #expect(live.web.isEmpty, "no HTML, no web view")

        // At the top.
        let top = ConversationLayout.live(frames: frames, html: [], visible: Span(0, 300))
        #expect(top.near == Set(0...9))

        // Screens as asked.
        let tight = ConversationLayout.live(frames: frames, html: [], visible: Span(1000, 1300), screens: 0)
        #expect(tight.near == Set(9...13))
    }

    @Test func webViewsGoToTheNearestHTMLCardsUpToTheCap() {
        let frames = stacked(40, 100)
        let html = Array(repeating: true, count: 40)
        let live = ConversationLayout.live(frames: frames, html: html, visible: Span(1000, 1300))
        #expect(live.web.count == ConversationLayout.maxLiveWebViews)
        // The visible ones (9–13, touching counts) first, then the nearest
        // by distance, the earlier one on a tie.
        #expect(live.web.isSuperset(of: Set(9...13)))
        #expect(live.web == Set([8, 9, 10, 11, 12, 13, 14, 7]))
        #expect(live.web.isSubset(of: live.near))

        // Only HTML cards; a cap of two.
        var mixed = Array(repeating: false, count: 40)
        mixed[2] = true
        mixed[11] = true
        mixed[30] = true
        let few = ConversationLayout.live(frames: frames, html: mixed, visible: Span(1000, 1300), cap: 2)
        #expect(few.web == [11], "items 2 and 30 are not near")
        let none = ConversationLayout.live(frames: frames, html: mixed, visible: Span(1000, 1300), cap: 0)
        #expect(none.web.isEmpty)
        #expect(ConversationLayout.live(frames: [], html: [], visible: Span(0, 300)) == ConversationLayout.Live())
    }

    @Test func anchoredTopKeepsTheItemAndStaysInTheDocument() {
        // The item that was 30 below the viewport's top now starts at 500.
        #expect(ConversationLayout.anchoredTop(itemTop: 500, offset: 30, documentHeight: 2000, viewportHeight: 400) == 530)
        // Clamped to the end and to the start.
        #expect(ConversationLayout.anchoredTop(itemTop: 1900, offset: 0, documentHeight: 2000, viewportHeight: 400) == 1600)
        #expect(ConversationLayout.anchoredTop(itemTop: 10, offset: -50, documentHeight: 2000, viewportHeight: 400) == 0)
        // A document shorter than the viewport stays at the top.
        #expect(ConversationLayout.anchoredTop(itemTop: 200, offset: 0, documentHeight: 300, viewportHeight: 400) == 0)
    }

    @Test func pageTopStepsAViewportLessTheOverlap() {
        #expect(ConversationLayout.pageTop(from: 0, up: false, viewportHeight: 400, documentHeight: 2000, overlap: 40) == 360)
        #expect(ConversationLayout.pageTop(from: 360, up: true, viewportHeight: 400, documentHeight: 2000, overlap: 40) == 0)
        #expect(ConversationLayout.pageTop(from: 1500, up: false, viewportHeight: 400, documentHeight: 2000, overlap: 40) == 1600)
        #expect(ConversationLayout.pageTop(from: 100, up: true, viewportHeight: 400, documentHeight: 2000, overlap: 40) == 0)
        // An overlap as large as the viewport still moves half a page.
        #expect(ConversationLayout.pageTop(from: 0, up: false, viewportHeight: 400, documentHeight: 2000, overlap: 400) == 200)
    }
}

// MARK: The timeline

/// 2026-09-01T09:00:00Z.
private let railT0 = Date(timeIntervalSince1970: 1_788_253_200)

private func railMember(_ id: String, _ min: Int, from: String = "jana@acme.example", issue: MessageIssue? = nil) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: "f1", threadId: "t1",
        from: [Address(name: "Jana Dvořáková", address: from)], subject: "Quarterly report",
        date: railT0.addingTimeInterval(Double(min) * 60), snippet: "", flags: [.seen], hasAttachments: false, size: 0,
        issue: issue)
}

private let railAccount = Account(
    id: "a1", config: AccountConfig(name: "Work", email: "petr@acme.example"), enabled: true,
    state: SyncState(accountId: "a1", status: .idle))

private let railIssue = IssueInfo(
    key: "WEB-12", url: "https://acme.atlassian.net/browse/WEB-12", summary: "Footer overlaps the form", status: "To Do",
    statusCategory: .todo)

private func railThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "a1", subject: "Quarterly report", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: railMember("latest", 0), snippet: "", flags: [], hasAttachments: false, folderIds: [])
}

private typealias Rail = ConversationLayout.Rail

struct ConversationTimelineTests {
    @Test func messagesHaveAvatarsEventsAndTheOlderRowDots() {
        let status = MessageIssue(
            info: railIssue, item: .event, changes: [IssueChange(field: .status, from: "To Do", to: "Done")], mine: true)
        let m = Conversation.build(railThread(7), [
            railMember("d", 0, issue: MessageIssue(info: railIssue, item: .description)),
            railMember("c1", 10, issue: MessageIssue(info: railIssue, item: .comment, mine: true)),
            railMember("e1", 20, issue: status),
            railMember("c2", 30, issue: MessageIssue(info: railIssue, item: .comment, via: "Issue Sync", mine: true)),
        ], account: railAccount)
        #expect(m.items.map(\.kind) == [.truncated, .message, .message, .event, .message])
        #expect(ConversationLayout.rails(m.items) == [
            // The line starts at the first mark and ends at the last.
            Rail(marker: .dot, below: true),
            Rail(marker: .avatar, above: true, below: true),
            Rail(marker: .avatar, accent: true, above: true, below: true),
            // The user's own change: a dot is never tinted.
            Rail(marker: .dot, above: true, below: true),
            // A relayed comment is never the user's.
            Rail(marker: .avatar, above: true),
        ])
    }

    @Test func ownMailIsTinted() {
        let m = Conversation.build(railThread(3), [
            railMember("m1", 0), railMember("m2", 10, from: "Petr@acme.example"), railMember("m3", 20),
        ], account: railAccount)
        #expect(ConversationLayout.rails(m.items) == [
            Rail(marker: .avatar, below: true),
            Rail(marker: .avatar, accent: true, above: true, below: true),
            Rail(marker: .avatar, above: true),
        ])
        // Without the account nothing is.
        let plain = Conversation.build(railThread(3), [railMember("m1", 0), railMember("m2", 10, from: "petr@acme.example")])
        #expect(ConversationLayout.rails(plain.items).map(\.accent) == [false, false, false])
        #expect(plain.items.first?.kind == .truncated)
    }

    @Test func oneItemHasNoLine() {
        var only = Conversation.Item(kind: .message, message: railMember("m1", 0))
        only.mine = true
        #expect(ConversationLayout.rails([only]) == [Rail(marker: .avatar, accent: true)])
        #expect(ConversationLayout.rails([]).isEmpty)
        let two = ConversationLayout.rails([only, Conversation.Item(kind: .event, message: railMember("e1", 1))])
        #expect(two == [Rail(marker: .avatar, accent: true, below: true), Rail(marker: .dot, above: true)])
    }

    @Test func theColumnKeepsToTheClampWithTheGutterInIt() {
        typealias M = ConversationLayout.Metrics
        let beside = 2 * M.sideInset + M.avatar + M.gutterGap
        #expect(ConversationLayout.cardWidth(pane: 600) == 600 - beside)
        #expect(ConversationLayout.cardWidth(pane: M.maxWidth) == M.maxWidth - beside)
        #expect(ConversationLayout.cardWidth(pane: 1600) == M.maxWidth - beside, "the gutter is inside the 900")
        #expect(ConversationLayout.cardWidth(pane: 40) == 0)
        #expect(ConversationLayout.cardWidth(pane: 0) == 0)
        #expect(M.maxWidth == 900 && M.itemGap == 12 && M.cardRadius == 10)
        #expect((26...28).contains(M.avatar) && M.gutterGap == 12 && M.dot == 7 && M.line == 1)

        // The short date while the header is narrower than the threshold.
        let edge = M.compactHeader + 2 * M.cardPaddingH + beside
        #expect(ConversationLayout.compactDates(pane: edge - 1))
        #expect(!ConversationLayout.compactDates(pane: edge))
        #expect(ConversationLayout.compactDates(pane: 300), "the pane at its narrowest")
        #expect(!ConversationLayout.compactDates(pane: 2000))
    }
}

// MARK: The order shown

struct ConversationDisplayOrderTests {
    private func ids(_ items: [Conversation.Item]) -> [String] {
        items.map { $0.kind == .truncated ? "…" : ($0.message?.id.rawValue ?? "") }
    }

    private func msg(_ id: String) -> Conversation.Item {
        Conversation.Item(kind: .message, message: railMember(id, 0))
    }

    private func event(_ id: String) -> Conversation.Item {
        Conversation.Item(kind: .event, message: railMember(id, 0))
    }

    private func issue(_ id: String, _ kind: IssueItemKind) -> Conversation.Item {
        Conversation.Item(kind: .message, message: railMember(id, 0, issue: MessageIssue(info: railIssue, item: kind)))
    }

    private var truncated: Conversation.Item {
        var it = Conversation.Item(kind: .truncated)
        it.text = "2 earlier messages are not shown"
        return it
    }

    /// The pane shows what opened the conversation first, then the rest
    /// newest first and the row of older members last; the model keeps its
    /// own order, and the timeline follows the order shown.
    @Test func displayOrder() {
        struct Case {
            var name: String
            var items: [Conversation.Item]
            var want: [String]
            var root: Int
            var folded: Bool
        }
        let cases = [
            Case(
                name: "mail: the first message on top, folded", items: [msg("a"), msg("b"), msg("c")],
                want: ["a", "c", "b"], root: 0, folded: true),
            Case(
                name: "issue: the description on top",
                items: [issue("desc", .description), event("e1"), issue("c1", .comment), event("e2")],
                want: ["desc", "e2", "c1", "e1"], root: 0, folded: true),
            Case(
                name: "issue with only status changes: the description stays open",
                items: [issue("desc", .description), event("e1"), event("e2")],
                want: ["desc", "e2", "e1"], root: 0, folded: false),
            Case(
                name: "cut mail: no first message, older ones last", items: [truncated, msg("x"), msg("y")],
                want: ["y", "x", "…"], root: -1, folded: false),
            Case(
                name: "cut issue: the description when it is there",
                items: [truncated, issue("desc", .description), issue("c1", .comment)],
                want: ["desc", "c1", "…"], root: 0, folded: true),
            Case(name: "an event first is no opening", items: [event("e"), msg("m")], want: ["m", "e"], root: -1, folded: false),
            Case(name: "nothing", items: [], want: [], root: -1, folded: false),
        ]
        for c in cases {
            let before = c.items
            let d = ConversationLayout.displayOrder(c.items)
            #expect(ids(d.items) == c.want, "\(c.name)")
            #expect(d.root == c.root && d.rootFolded == c.folded, "\(c.name)")
            #expect(c.items == before, "\(c.name): the model's items were changed")
        }

        // The timeline runs from the opening card down to the row of older
        // members.
        let shown = ConversationLayout.displayOrder([truncated, msg("x"), msg("y")]).items
        let rails = ConversationLayout.rails(shown)
        #expect(rails.first == Rail(marker: .avatar, below: true), "top")
        #expect(rails.last == Rail(marker: .dot, above: true), "the older row at the bottom")
    }

    /// What opened the conversation (conversation_layout.go `convRoot`).
    @Test func root() {
        #expect(ConversationLayout.root([msg("a"), msg("b")]) == 0)
        #expect(ConversationLayout.root([issue("c1", .comment), event("e"), issue("desc", .description)]) == 2)
        #expect(ConversationLayout.root([truncated, msg("a")]) == -1, "thread.get cut the conversation")
        #expect(ConversationLayout.root([truncated, issue("c1", .comment), issue("desc", .description)]) == 2)
        #expect(ConversationLayout.root([event("e"), msg("a")]) == -1)
        #expect(ConversationLayout.root([]) == -1)
        // An event is never the opening, whatever its issue says.
        var odd = event("e")
        odd.message?.issue = MessageIssue(info: railIssue, item: .description)
        #expect(ConversationLayout.root([odd]) == -1)
    }
}

// MARK: A card's header

private typealias Header = ConversationHeaderLayout
private typealias Slot = ConversationHeaderLayout.Slot

/// A sender of 120 with the disclosure, the three badges (80, 50, 35), a
/// date of 130 and three buttons (76).
private func headerParts(
    dot: Double = 0, sender: Double = 120, badges: [Double] = [80, 50, 35], date: Double = 130, buttons: Double = 76
) -> Header.Parts {
    Header.Parts(dot: dot, sender: sender, disclosure: 13, badges: badges, date: date, buttons: buttons)
}

struct ConversationHeaderLayoutTests {
    @Test func aWideHeaderIsOneLine() {
        for shown in [false, true] {
            let h = Header(headerParts(), width: 600, buttonsShown: shown)
            #expect(h.lines == 1)
            #expect(h.dot == nil)
            #expect(h.sender == Slot(0, 120))
            #expect(h.disclosure == Slot(126, 13))
            #expect(h.badges == [Slot(147, 80), Slot(235, 50), Slot(293, 35)])
            #expect(h.date == Slot(470, 130), "at the trailing edge")
            #expect(h.buttons == Slot(386, 76), "before the date, nothing under them")
        }
        // The unread dot moves the rest.
        let unread = Header(headerParts(dot: 8), width: 600, buttonsShown: false)
        #expect(unread.dot == Slot(0, 8) && unread.sender == Slot(14, 120) && unread.disclosure == Slot(140, 13))
        #expect(unread.badges.first == Slot(161, 80))
        #expect(unread.fold == nil)
    }

    /// The card that opened the conversation: its fold arrow starts the
    /// line, and the rest moves.
    @Test func theFoldArrowComesFirst() {
        var parts = headerParts()
        parts.fold = 16
        let h = Header(parts, width: 600, buttonsShown: false)
        #expect(h.fold == Slot(0, 16))
        #expect(h.dot == nil && h.sender == Slot(22, 120) && h.disclosure == Slot(148, 13))
        #expect(h.badges.first == Slot(169, 80))
        #expect(h.date == Slot(470, 130), "the date keeps its place")
        // With the unread dot after it; folded, the disclosure is not shown.
        parts.dot = 8
        parts.disclosure = 0
        let folded = Header(parts, width: 600, buttonsShown: false)
        #expect(folded.fold == Slot(0, 16) && folded.dot == Slot(22, 8) && folded.sender == Slot(36, 120))
        #expect(folded.disclosure == nil && folded.badges.first == Slot(164, 80))
    }

    @Test func partsNotShownHaveNoPlace() {
        let h = Header(headerParts(badges: [0, 50, 0], buttons: 0), width: 600, buttonsShown: true)
        #expect(h.badges == [nil, Slot(147, 50), nil])
        #expect(h.buttons == nil)
        let mail = Header(headerParts(badges: [0, 0, 0], date: 0), width: 600, buttonsShown: false)
        #expect(mail.badges == [nil, nil, nil] && mail.date == nil && mail.lines == 1)
        #expect(mail.buttons == Slot(524, 76), "no date: the buttons keep to the edge")
        // No sender at all: the disclosure starts the line, the badges follow it.
        let nameless = Header(headerParts(sender: 0, badges: [0, 50, 0]), width: 600, buttonsShown: false)
        #expect(nameless.sender == nil && nameless.disclosure == Slot(0, 13))
        #expect(nameless.badges == [nil, Slot(27, 50), nil])
    }

    @Test func badgesThatDoNotFitGoToASecondLine() {
        // 330 wide: the date leaves 192 for the sender's group (139): the
        // first badge does not fit any more and takes the others with it.
        let h = Header(headerParts(), width: 330, buttonsShown: false)
        #expect(h.lines == 2)
        #expect(h.sender == Slot(0, 120) && h.disclosure == Slot(126, 13))
        #expect(h.badges == [Slot(0, 80, line: 1), Slot(88, 50, line: 1), Slot(146, 35, line: 1)])
        #expect(h.date == Slot(200, 130))

        // Only the last one does not fit: the others stay.
        let last = Header(headerParts(date: 45), width: 345, buttonsShown: false)
        #expect(last.badges == [Slot(147, 80), Slot(235, 50), Slot(0, 35, line: 1)])
        #expect(last.lines == 2)

        // On the second line a badge too long is cut, and left out when
        // nothing worth showing is left of it.
        let long = Header(headerParts(badges: [400, 50, 35], date: 45), width: 200, buttonsShown: false)
        #expect(long.badges == [Slot(0, 200, line: 1), nil, nil])
        let tight = Header(headerParts(badges: [150, 50, 35], date: 45), width: 200, buttonsShown: false)
        #expect(tight.badges == [Slot(0, 150, line: 1), Slot(158, 42, line: 1), nil])
    }

    @Test func theSenderGivesWayToTheDate() {
        let h = Header(headerParts(sender: 300, badges: []), width: 330, buttonsShown: false)
        #expect(h.date == Slot(200, 130))
        #expect(h.sender == Slot(0, 173) && h.disclosure == Slot(179, 13), "up to the date's spacing")
        #expect(h.lines == 1)
        // Never below its least width; a short name keeps its own.
        let least = Header(headerParts(sender: 300, badges: []), width: 150, buttonsShown: false)
        #expect(least.sender?.width == Header.minSender)
        let short = Header(headerParts(sender: 30, badges: []), width: 150, buttonsShown: false)
        #expect(short.sender?.width == 30)
    }

    @Test func theButtonsTakeRoomOnlyWhileTheyShow() {
        // 200 wide with the short date: the sender's group reaches under
        // the buttons' place.
        let parts = headerParts(date: 45)
        let resting = Header(parts, width: 200, buttonsShown: false)
        #expect(resting.sender == Slot(0, 120) && resting.disclosure == Slot(126, 13))
        #expect(resting.buttons == Slot(71, 76))
        let hover = Header(parts, width: 200, buttonsShown: true)
        #expect(hover.sender == Slot(0, 46), "the name is cut")
        #expect(hover.disclosure == Slot(52, 13), "the disclosure stays in reach")
        #expect(hover.buttons == Slot(71, 76))
        #expect(hover.date == resting.date)
        // The badges do not move and the height does not change.
        #expect(hover.badges == resting.badges && hover.lines == resting.lines)
        #expect(resting.badges == [Slot(0, 80, line: 1), Slot(88, 50, line: 1), Slot(146, 35, line: 1)])

        // Too narrow even for the least name: the buttons start after the
        // disclosure and cover what is behind them.
        let narrow = Header(headerParts(sender: 300, date: 130), width: 200, buttonsShown: true)
        #expect(narrow.sender == Slot(0, 40) && narrow.disclosure == Slot(46, 13))
        #expect(narrow.buttons == Slot(65, 76))
        #expect(narrow.date == Slot(70, 130))
    }

    @Test func oddWidthsDoNotBreakIt() {
        for width in [0, -20, Double.nan, Double.infinity, 1, 39.5] {
            for shown in [false, true] {
                let h = Header(headerParts(dot: 8), width: width, buttonsShown: shown)
                let slots = [h.dot, h.sender, h.disclosure, h.date, h.buttons] + h.badges
                for s in slots.compactMap({ $0 }) {
                    #expect(s.x.isFinite && s.width.isFinite && s.x >= 0 && s.width >= 0, "width \(width): \(s)")
                }
                #expect(h.lines == 1 || h.lines == 2)
                #expect(h.badges.count == 3)
            }
        }
        let odd = Header(
            Header.Parts(dot: -.infinity, sender: .nan, disclosure: -3, badges: [.nan, -1], date: .infinity, buttons: .nan),
            width: 300, buttonsShown: true)
        #expect(odd.dot == nil && odd.sender == nil && odd.disclosure == nil && odd.date == nil && odd.buttons == nil)
        #expect(odd.badges == [nil, nil] && odd.lines == 1)
    }
}

// MARK: An event's row

private typealias Event = ConversationEventLayout

struct ConversationEventLayoutTests {
    @Test func aWideRowKeepsEverything() {
        // 207 of lines, a name of 80 and a time of 110 in 600.
        let e = Event(width: 600, lines: 207, sender: 80, date: 110)
        #expect(e.sender == 80)
        #expect(e.lines == 600 - 110 - 8 - 80 - 8, "the lines take what is left, so the rest keeps to the end")
        // Nobody named, no time.
        #expect(Event(width: 600, lines: 207, sender: 0, date: 110) == Event(width: 600, lines: 0, sender: 0, date: 110))
        #expect(Event(width: 600, lines: 207, sender: 0, date: 110).lines == 482)
        #expect(Event(width: 600, lines: 207, sender: 80, date: 0).lines == 512)
        // A long name is capped.
        #expect(Event(width: 900, lines: 207, sender: 700, date: 110).sender == Event.maxSender)
    }

    @Test func theNameGivesWayBeforeTheLinesWrap() {
        // 312 wide with a short date of 28: 276 for the lines and the
        // name; the lines keep their 207, the name gets the 61 left.
        let e = Event(width: 312, lines: 207, sender: 80, date: 28)
        #expect(e.sender == 61 && e.lines == 207)
        // At its least the name stays and the lines wrap.
        let wrapped = Event(width: 300, lines: 207, sender: 80, date: 28)
        #expect(wrapped.sender == Event.minSender && wrapped.lines == 196)
        // A name shorter than the least keeps its own width.
        let short = Event(width: 280, lines: 240, sender: 30, date: 28)
        #expect(short.sender == 30 && short.lines == 206)
    }

    @Test func theNameIsLeftOutWhereTheLinesWouldStarve() {
        // 200 wide: beside a name of 60 the lines would have 96.
        let e = Event(width: 200, lines: 207, sender: 80, date: 28)
        #expect(e.sender == 0 && e.lines == 164)
        // The pane at its narrowest: a change that fits alone stays on
        // one line.
        let narrow = Event(width: 200, lines: 135, sender: 65, date: 24)
        #expect(narrow.sender == 0 && narrow.lines == 168)
        // Short lines that fit beside the name keep it.
        let fits = Event(width: 200, lines: 90, sender: 80, date: 28)
        #expect(fits.sender == 66 && fits.lines == 90)
    }

    @Test func oddWidthsDoNotBreakIt() {
        for width in [0, -5, Double.nan, Double.infinity, 1, 30] {
            let e = Event(width: width, lines: 207, sender: 80, date: 110)
            #expect(e.lines.isFinite && e.lines >= 0 && e.sender.isFinite && e.sender >= 0, "width \(width)")
        }
        let odd = Event(width: 300, lines: .nan, sender: -.infinity, date: .nan)
        #expect(odd.sender == 0 && odd.lines == 300)
    }
}

struct WebHeightGovernorTests {
    @Test func followsTheDocumentAtTheZoom() {
        var g = WebHeightGovernor(zoom: 1.25)
        #expect(g.fits, "nothing reported: the wheel goes on")
        #expect(g.report(css: 400, viewport: false) == 500)
        #expect(g.applied == 500 && g.content == 500 && g.fits)
        #expect(g.report(css: 400, viewport: false) == nil, "the same height again")
        #expect(g.report(css: 300.2, viewport: false) == 376, "points round up")
        #expect(g.report(css: 0, viewport: false) == 1, "never below one point")
        #expect(g.report(css: .nan, viewport: false) == nil)
        #expect(g.report(css: -5, viewport: false) == nil)
    }

    @Test func capsTheHeightAndScrollsInside() {
        var g = WebHeightGovernor()
        #expect(g.report(css: 9000, viewport: false) == WebHeightGovernor.maxHeight)
        #expect(!g.fits, "beyond the cap the card scrolls inside")
        #expect(g.report(css: 800, viewport: false) == 800)
        #expect(g.fits)
    }

    @Test func freezesContentThatGrowsWithTheView() {
        // A 100vh document reports the view's height plus the column's
        // padding every time the view grows.
        var g = WebHeightGovernor()
        #expect(g.report(css: 156, viewport: false) == 156)
        #expect(g.report(css: 192, viewport: true) == 192)
        #expect(g.report(css: 228, viewport: true) == 228)
        #expect(g.report(css: 264, viewport: true) == 264)
        #expect(g.report(css: 300, viewport: true) == nil, "the fourth growth in a row freezes")
        #expect(g.frozen && g.applied == 264 && !g.fits)
        #expect(g.report(css: 100, viewport: false) == nil, "frozen until something changes")

        // A new width measures again.
        g.widthChanged()
        #expect(!g.frozen)
        #expect(g.report(css: 100, viewport: false) == 100)

        // Growth that the view's own growth did not cause (pictures
        // arriving) never freezes, and resets the count.
        var p = WebHeightGovernor()
        for (i, h) in [200.0, 260, 330, 420, 500, 610, 700].enumerated() {
            #expect(p.report(css: h, viewport: i % 3 == 2) == h, "report \(i)")
        }
        #expect(!p.frozen)

        // A shrink in answer to the view resets the count too.
        var s = WebHeightGovernor()
        _ = s.report(css: 100, viewport: false)
        _ = s.report(css: 200, viewport: true)
        _ = s.report(css: 300, viewport: true)
        #expect(s.report(css: 250, viewport: true) == 250)
        #expect(s.report(css: 350, viewport: true) == 350)
        #expect(!s.frozen)
    }

    @Test func zoomAndNewDocumentsStartOver() {
        var g = WebHeightGovernor()
        _ = g.report(css: 400, viewport: false)
        #expect(g.setZoom(1.5) == 600, "the last height at the new zoom at once")
        #expect(g.setZoom(1.5) == nil)
        #expect(g.setZoom(0) == 400, "zero is 100 %")
        _ = g.report(css: 100, viewport: false)
        for h in [136.0, 172, 208, 244] {
            _ = g.report(css: h, viewport: true)
        }
        #expect(g.frozen)
        // The last report (the frozen document's own height) at the new
        // zoom: a zoom measures again.
        #expect(g.setZoom(2) == 488)
        #expect(!g.frozen)

        g.reset()
        #expect(g.css == nil && g.content == nil && !g.frozen)
        #expect(g.applied == 488, "the view keeps its height until the new document reports")
        #expect(g.report(css: 50, viewport: false) == 100)

        var fresh = WebHeightGovernor()
        #expect(fresh.setZoom(2) == nil, "nothing reported yet")
    }
}

struct ChipPlanTests {
    private func attachment(_ part: String, _ name: String, size: Int = 1000, cid: String? = nil, remote: Bool? = nil) -> MalachiCore.Attachment {
        MalachiCore.Attachment(partId: part, filename: name, contentType: "application/pdf", size: size, inline: false, contentId: cid, remote: remote)
    }

    private func body(_ state: BodyState = .fetched, html: String? = nil, inline: [String: String]? = nil) -> MessageBodyResult {
        MessageBodyResult(
            messageId: "m1", bodyState: state, hasHtml: html != nil, html: html, text: "t", inlineParts: inline,
            remoteContent: .block, sanitizerVersion: "1")
    }

    private func message(_ atts: [MalachiCore.Attachment]) -> Message {
        Message(summary: MessageSummary(
            id: "m1", accountId: "a", folderId: "in", from: [], subject: "", date: .goZero, snippet: "", flags: [],
            hasAttachments: !atts.isEmpty, size: 0), attachments: atts)
    }

    @Test func noMessageNoChips() {
        #expect(ChipPlan(nil, body()).isEmpty)
        #expect(ChipPlan(message([]), body()) == ChipPlan())
    }

    @Test func chipsAndSaveAll() {
        let atts = [attachment("2", "a.pdf"), attachment("3", "b.pdf", remote: true)]
        let plan = ChipPlan(message(atts), body())
        #expect(plan.chips.map(\.state) == [.local, .remote])
        #expect(plan.chips[1].why == "On the server only; it is downloaded when you open it")
        #expect(plan.saveAll && plan.saveAllRemote)

        // One chip: no Save All.
        #expect(!ChipPlan(message([atts[0]]), body()).saveAll)
        // Waiting for the body: chips, but nothing can be saved yet.
        let waiting = ChipPlan(message(atts), nil)
        #expect(waiting.chips.map(\.state) == [.waiting, .waiting] && !waiting.saveAll)
        // A part out of reach: no Save All.
        let big = ChipPlan(message([atts[0], attachment("4", "c.pdf", size: API.Limits.maxAttachmentDataBytes + 1)]), body())
        #expect(big.chips[1].state == .unavailable && !big.saveAll)
    }

    @Test func picturesTheHTMLShowsAreLeftOut() {
        let atts = [attachment("2", "logo.png", cid: "logo@x"), attachment("3", "a.pdf")]
        let shown = ChipPlan(message(atts), body(html: "<img src=\"malachi-cid:a/m1/2\">", inline: ["logo@x": "2"]))
        #expect(shown.chips.map(\.attachment.partId) == ["3"])
        #expect(!shown.saveAll)
        // Plain text: the picture is a file like any other.
        #expect(ChipPlan(message(atts), body(inline: ["logo@x": "2"])).chips.count == 2)
    }

    @Test func partsOfAnAttachedMessageAreOutOfReach() {
        let plan = ChipPlan(message([attachment("2", "a.pdf"), attachment("3", "b.pdf")]), body(), embedded: true)
        #expect(plan.chips.allSatisfy { $0.state == .unavailable })
        #expect(plan.chips[0].why == "Files inside an attached message cannot be opened or saved yet.")
        #expect(!plan.saveAll)
    }
}
