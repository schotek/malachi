// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's controller (MalachiCore/Controllers/BoardController.swift)
// over an in-memory source: what the user looks at, what they decide about
// a case, and which `Changes` the page is told about. Swift-first; the GTK
// port brings ui/internal/board/controller_test.go of the same shape.

private typealias F = BoardFixture
private typealias Changes = BoardController.Changes

/// The controller's observer: every `onChange` call, in order.
@MainActor private final class Probe {
    var log: [Changes] = []
}

@MainActor private final class Clock {
    var now = F.now
}

/// Rows in the list under Overview: c1 hot, c3 c2 you (c3 is newer), c4 them, c5 info.
private func sampleCases() -> [Board.Case] {
    [
        F.mk("c1", .hot, hours: 4), F.mk("c2", .you, hours: 3), F.mk("c3", .you, hours: 2),
        F.mk("c4", .them, hours: 1), F.mk("c5", .info, account: F.accountB, hours: 5),
        F.mk("d1", .info, hours: 6, done: true), F.mk("d2", .info, hours: 7, done: true),
    ]
}

@MainActor private func make(
    _ cases: [Board.Case] = sampleCases(), annotated: Bool = false, clock: Clock? = nil
) -> (c: BoardController, source: InMemoryBoardSource, probe: Probe) {
    let source = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: cases, annotated: annotated))
    let c = BoardController(source: source, now: { clock?.now ?? F.now }, calendar: F.calendar)
    let probe = Probe()
    c.onChange = { probe.log.append($0) }
    return (c, source, probe)
}

/// A source that holds the user's writes until `flush`, as a daemon-backed
/// one reports them when the daemon answers.
@MainActor private final class LateSource: BoardSource {
    var snapshot: Board.Snapshot
    var onChange: (@MainActor () -> Void)?
    private var queued: [(inout Board.Snapshot) -> Void] = []

    init(_ snapshot: Board.Snapshot) {
        self.snapshot = snapshot
    }

    func setState(_ state: Board.State?, of id: Board.CaseID) {
        queued.append { s in s.cases = s.cases.map { var c = $0; if c.id == id { c.userState = state }; return c } }
    }

    func setDone(_ done: Bool, of id: Board.CaseID) {
        queued.append { s in s.cases = s.cases.map { var c = $0; if c.id == id { c.done = done }; return c } }
    }

    var onError: (@MainActor (String) -> Void)?
    var onNotice: (@MainActor (String) -> Void)?
    var onArchived: (@MainActor (Board.ArchiveOutcome) -> Void)?
    /// The cases whose conversation was asked for, in order.
    var loads: [Board.CaseID] = []

    func remind(until: Date?, of id: Board.CaseID) {
        queued.append { s in
            s.cases = s.cases.map { var c = $0; if c.id == id { c.visibility = until.map { .snoozed(until: $0) } ?? .live }; return c }
        }
    }

    func archive(_ id: Board.CaseID) {
        setDone(true, of: id)
    }

    func setCommitmentDone(_ done: Bool, of id: String) {}
    func discardDraft(of id: Board.CaseID) {}
    func unflag(_ id: Board.CaseID) {}
    func loadMessages(of id: Board.CaseID) { loads.append(id) }
    /// How many times the data was asked for anew.
    var refreshes = 0
    func refresh() { refreshes += 1 }

    func flush() {
        for q in queued {
            q(&snapshot)
        }
        queued = []
        onChange?()
    }
}

@MainActor private func rows(_ c: BoardController) -> [String] {
    c.view.sections.flatMap(\.rows).map(\.id.rawValue)
}

@MainActor @Suite struct BoardControllerTests {
    // MARK: Initial state

    @Test func initialState() {
        let (c, source, probe) = make()
        #expect(c.state.style == .list && c.state.filter == .all && c.state.account == .all)
        #expect(c.state.inlineDetail && !c.state.revealsWhy)
        #expect(c.state.selection == F.id("c1"))  // the list selects its first row
        #expect(c.view.selection == F.id("c1") && c.view.detail?.id == F.id("c1"))
        #expect(!c.view.showsPanel)
        #expect(rows(c) == ["c1", "c3", "c2", "c4", "c5"])
        #expect(source.onChange != nil)
        #expect(probe.log.isEmpty)  // nothing is reported at construction
    }

    @Test func emptyBoard() {
        let (c, _, _) = make([])
        #expect(c.view.isEmpty && c.state.selection == nil && c.view.detail == nil)
    }

    @Test func changesBits() {
        #expect([Changes.content, .selection, .style, .filters].map(\.rawValue) == [1, 2, 4, 8])
    }

    // MARK: Style

    @Test func setStyle() {
        let (c, _, probe) = make()
        c.select(F.id("c1"))  // picked by the user: it stays, now in the panel
        probe.log = []
        c.setStyle(.columns)
        #expect(c.state.style == .columns && c.state.selection == F.id("c1") && c.view.showsPanel)
        #expect(probe.log == [[.style, .selection]])
        c.setStyle(.columns)
        #expect(probe.log.count == 1)  // the same style: silent
        c.setStyle(.today)
        #expect(probe.log == [[.style, .selection], .style])
        c.setStyle(.list)  // back beside the list
        #expect(c.state.selection == F.id("c1") && !c.view.showsPanel)
        #expect(probe.log.last == [.style, .selection])
    }

    /// Audit row 18: the panel opens on the case, so that an inline reply
    /// editor can move there.
    @Test func leavingTheListKeepsTheSelection() {
        for style in [Board.Style.columns, .today] {
            let (c, _, probe) = make()
            c.select(F.id("c3"))
            probe.log = []
            c.setStyle(style)
            #expect(c.state.selection == F.id("c3"))
            #expect(c.view.showsPanel && c.view.detail?.id == F.id("c3"))
            #expect(probe.log == [[.style, .selection]])
        }
    }

    @Test func leavingTheDoneListDropsADoneSelection() {
        let (c, _, _) = make()
        c.setFilter(.done)
        #expect(c.state.selection == F.id("d1"))
        c.setStyle(.columns)  // Columns show only live cases
        #expect(c.state.selection == nil)
    }

    @Test func enteringTheListWithoutInlineDetailSelectsNothing() {
        let (c, _, probe) = make()
        c.setInlineDetail(false)
        c.setStyle(.columns)
        c.select(nil)
        probe.log = []
        c.setStyle(.list)
        #expect(c.state.selection == nil)
        #expect(probe.log == [.style])
    }

    @Test func styleChangeKeepsWhyOfTheSameCase() {
        let (c, _, _) = make()
        c.select(F.id("c1"))
        c.toggleWhy()
        #expect(c.state.revealsWhy)
        c.setStyle(.columns)
        #expect(c.state.revealsWhy)  // the case stayed
        c.select(F.id("c2"))
        #expect(!c.state.revealsWhy)
    }

    // MARK: Filters

    @Test func setFilter() {
        let (c, _, probe) = make()
        c.setFilter(.state(.you))
        #expect(c.state.filter == .state(.you))
        #expect(c.state.selection == F.id("c3"))  // the first row of the filter
        #expect(probe.log == [[.filters, .selection, .content]])
        c.setFilter(.state(.you))
        #expect(probe.log.count == 1)
        c.setFilter(.done)
        #expect(c.state.selection == F.id("d1"))
        #expect(rows(c) == ["d1", "d2"])
        c.setFilter(.state(.info))  // c5 is the live info case
        #expect(c.state.selection == F.id("c5"))
    }

    @Test func filterWithNothingSelectsNothing() {
        let (c, _, _) = make([F.mk("c1", .you)])
        c.setFilter(.state(.hot))
        #expect(c.state.selection == nil && c.view.detail == nil && c.view.sections.isEmpty)
    }

    @Test func filterInColumnsLeavesNoSelection() {
        let (c, _, probe) = make()
        c.setStyle(.columns)
        c.select(nil)
        probe.log = []
        c.setFilter(.state(.hot))
        #expect(c.state.selection == nil)
        #expect(probe.log == [[.filters, .content]])  // the list behind it changed, the columns did not
        #expect(c.view.columns.count == 4)
    }

    @Test func setAccount() {
        let (c, _, probe) = make()
        c.setAccount(.account(F.accountB))
        #expect(c.state.account == .account(F.accountB))
        #expect(rows(c) == ["c5"] && c.state.selection == F.id("c5"))
        #expect(c.view.subtitle == "Beta · 1 case")
        #expect(probe.log == [[.filters, .selection, .content]])
        c.setAccount(.account(F.accountB))
        #expect(probe.log.count == 1)
        c.setAccount(.all)
        #expect(c.state.selection == F.id("c1"))
        #expect(probe.log.count == 2)
    }

    @Test func filterChangeResetsWhy() {
        let (c, _, _) = make()
        c.toggleWhy()
        c.setFilter(.state(.you))
        #expect(!c.state.revealsWhy)
        c.toggleWhy()
        c.setAccount(.account(F.accountB))
        #expect(!c.state.revealsWhy)
    }

    // MARK: Selection

    @Test func select() {
        let (c, _, probe) = make()
        c.select(F.id("c3"))
        #expect(c.state.selection == F.id("c3") && c.view.detail?.id == F.id("c3"))
        #expect(probe.log == [.selection])  // another case's detail is no content change
        c.select(F.id("c3"))
        #expect(probe.log.count == 1)
        // A case that is not shown resolves like nothing: the first row.
        c.select(F.id("nope"))
        #expect(c.state.selection == F.id("c1"))
        c.select(F.id("d1"))  // done, and the list shows live cases
        #expect(c.state.selection == F.id("c1"))
        c.select(nil)
        #expect(c.state.selection == F.id("c1"))
        #expect(probe.log == [.selection, .selection])
    }

    @Test func selectInColumnsOpensThePanel() {
        let (c, _, probe) = make()
        c.setStyle(.columns)
        probe.log = []
        c.select(F.id("c4"))
        #expect(c.state.selection == F.id("c4") && c.view.showsPanel)
        #expect(probe.log == [.selection])
        c.select(nil)
        #expect(c.state.selection == nil && !c.view.showsPanel)
        #expect(probe.log == [.selection, .selection])
        c.select(nil)
        #expect(probe.log.count == 2)
        c.select(F.id("d1"))  // done: not on the board
        #expect(c.state.selection == nil)
    }

    @Test func toggleWhyAndItsReset() {
        let (c, _, probe) = make()
        c.toggleWhy()
        #expect(c.state.revealsWhy && c.view.detail != nil)
        #expect(probe.log == [.selection])
        c.select(F.id("c1"))  // the same case: stays open
        #expect(c.state.revealsWhy && probe.log.count == 1)
        c.select(F.id("c2"))
        #expect(!c.state.revealsWhy)
        #expect(probe.log == [.selection, .selection])
        c.toggleWhy()
        c.toggleWhy()
        #expect(!c.state.revealsWhy && probe.log.count == 4)
    }

    @Test func toggleWhyNeedsASelection() {
        let (c, _, probe) = make()
        c.setStyle(.columns)
        c.select(nil)
        probe.log = []
        c.toggleWhy()
        #expect(!c.state.revealsWhy && probe.log.isEmpty)
    }

    @Test func setInlineDetail() {
        let (c, _, probe) = make()
        c.setInlineDetail(true)
        #expect(probe.log.isEmpty)
        c.select(F.id("c1"))
        probe.log = []
        c.setInlineDetail(false)  // narrow: the selected case moves to the panel
        #expect(!c.state.inlineDetail && c.state.selection == F.id("c1") && c.view.showsPanel)
        #expect(probe.log == [.selection])
        c.select(F.id("c2"))
        #expect(c.view.showsPanel)
        c.setInlineDetail(true)  // wide again: beside the list, the selection stays
        #expect(c.state.selection == F.id("c2") && !c.view.showsPanel)
        #expect(probe.log == [.selection, .selection, .selection])
        c.setInlineDetail(false)
        c.select(nil)  // the panel closed
        c.setInlineDetail(true)  // nothing selected: the first row
        #expect(c.state.selection == F.id("c1"))
    }

    /// The List's automatic first row is not kept by a style switch or a
    /// narrowing (no panel slides in by itself); an explicit pick or a live
    /// reply pane is (Go `TestAutoSelectedRowIsNotKept`).
    @Test func autoSelectedRowIsNotKept() {
        let (c, _, _) = make()
        #expect(c.state.selection == F.id("c1"))
        c.setStyle(.columns)
        #expect(c.state.selection == nil && !c.view.showsPanel)

        let (c2, _, _) = make()
        c2.setInlineDetail(false)
        #expect(c2.state.selection == nil && !c2.view.showsPanel)

        let (c3, _, _) = make()
        c3.paneLive = { $0 == F.id("c1") }
        c3.setStyle(.columns)
        #expect(c3.state.selection == F.id("c1") && c3.view.showsPanel)
    }

    @Test func showWaitingForYou() {
        let (c, _, probe) = make()
        c.setAccount(.account(F.accountA))
        c.setStyle(.columns)
        probe.log = []
        c.showWaitingForYou()
        #expect(c.state.style == .list && c.state.filter == .state(.you))
        #expect(c.state.account == .account(F.accountA))  // kept
        #expect(c.state.selection == F.id("c3"))
        #expect(probe.log == [[.style, .filters, .selection, .content]])
        // Already there: the selection stays and nothing is reported.
        c.select(F.id("c2"))
        probe.log = []
        c.showWaitingForYou()
        #expect(c.state.selection == F.id("c2") && probe.log.isEmpty)
    }

    // MARK: What the user decides

    @Test func setStateMovesTheCase() {
        let (c, source, probe) = make()
        c.setState(.them, of: F.id("c3"))
        #expect(source.snapshot.cases.first { $0.id == F.id("c3") }?.userState == .them)
        #expect(c.view.sections.map(\.kind) == [.state(.hot), .state(.you), .state(.them), .state(.info)])
        #expect(rows(c) == ["c1", "c2", "c4", "c3", "c5"])
        #expect(c.view.nav.map(\.count) == [5, 1, 1, 2, 1, 0, 2])
        #expect(c.state.selection == F.id("c1"))
        #expect(probe.log == [.content])
        c.setState(.them, of: F.id("c3"))  // nothing changes
        c.setState(.hot, of: F.id("missing"))
        #expect(probe.log.count == 1)
    }

    @Test func movingTheSelectedCaseKeepsItSelectedWhileItIsShown() {
        let (c, _, probe) = make()
        c.select(F.id("c3"))
        probe.log = []
        c.setState(.info, of: F.id("c3"))
        #expect(c.state.selection == F.id("c3"))
        #expect(c.view.detail?.state == .info && c.view.detail?.source == .user)
        #expect(probe.log == [.content])  // the same case's changed detail is content
    }

    @Test func movingTheSelectedCaseOutOfTheFilterSelectsTheNextRow() {
        let (c, _, probe) = make()
        c.setFilter(.state(.you))
        #expect(c.state.selection == F.id("c3"))
        probe.log = []
        c.setState(.hot, of: F.id("c3"))
        #expect(rows(c) == ["c2"] && c.state.selection == F.id("c2"))
        #expect(probe.log == [[.selection, .content]])
        c.setState(.them, of: F.id("c2"))  // the last row: nothing is left
        #expect(c.state.selection == nil && c.view.detail == nil)
    }

    @Test func setStateBackToAutomatic() {
        let ann = Board.Annotation(state: .hot, title: "T")
        let (c, source, _) = make([F.mk("c1", .you, annotation: ann)], annotated: true)
        func user() -> Board.State? { source.snapshot.cases[0].userState }
        // The automatic state is the assistant's (hot), not the rules' (you).
        c.setState(.them, of: F.id("c1"))
        #expect(user() == .them)
        c.setState(.hot, of: F.id("c1"))  // the same as automatic: back to automatic
        #expect(user() == nil && c.view.detail?.source == .assistantChanged(from: .you))
        c.setState(.you, of: F.id("c1"))  // the rules' state is not automatic here: the user's choice
        #expect(user() == .you && c.view.detail?.source == .user)
        c.setState(.hot, of: F.id("c1"))
        #expect(user() == nil)

        // Without annotations the rules decide.
        let (d, src2, _) = make([F.mk("c1", .you, annotation: ann)], annotated: false)
        d.setState(.them, of: F.id("c1"))
        d.setState(.you, of: F.id("c1"))
        #expect(src2.snapshot.cases[0].userState == nil)
        d.setState(.hot, of: F.id("c1"))
        #expect(src2.snapshot.cases[0].userState == .hot)
    }

    @Test func markDoneInTheListSelectsTheNextRow() {
        let (c, source, probe) = make()
        c.select(F.id("c3"))
        probe.log = []
        c.markDone(F.id("c3"))
        #expect(source.snapshot.cases.first { $0.id == F.id("c3") }?.done == true)
        #expect(rows(c) == ["c1", "c2", "c4", "c5"])
        #expect(c.state.selection == F.id("c2"))  // the row that followed
        #expect(probe.log == [[.selection, .content]])
        c.select(F.id("c5"))
        c.markDone(F.id("c5"))  // the last row: the previous one
        #expect(c.state.selection == F.id("c4"))
        c.select(F.id("c1"))
        c.markDone(F.id("c1"))  // the first row: the next
        #expect(c.state.selection == F.id("c2"))
    }

    @Test func markDoneTheOnlyRow() {
        let (c, _, _) = make([F.mk("c1")])
        c.markDone(F.id("c1"))
        #expect(c.state.selection == nil && c.view.detail == nil && c.view.sections.isEmpty)
        #expect(!c.view.isEmpty)  // a done case still counts
    }

    @Test func markDoneUnderAFilter() {
        let (c, _, _) = make()
        c.setFilter(.state(.you))
        c.markDone(F.id("c3"))
        #expect(c.state.selection == F.id("c2"))
        c.markDone(F.id("c2"))
        #expect(c.state.selection == nil)
    }

    @Test func markDoneInColumnsAndTodayClearsTheSelection() {
        for style in [Board.Style.columns, .today] {
            let (c, _, probe) = make()
            c.setStyle(style)
            c.select(F.id("c2"))
            #expect(c.view.showsPanel)
            probe.log = []
            c.markDone(F.id("c2"))
            #expect(c.state.selection == nil && !c.view.showsPanel && c.view.detail == nil)
            #expect(probe.log == [[.selection, .content]])
        }
    }

    @Test func markDoneAnotherCaseKeepsTheSelection() {
        let (c, _, probe) = make()
        c.markDone(F.id("c4"))
        #expect(c.state.selection == F.id("c1"))
        #expect(probe.log == [.content])
        c.markDone(F.id("c4"))  // already done: silent
        c.markDone(F.id("missing"))
        #expect(probe.log.count == 1)
    }

    @Test func reopen() {
        let (c, source, probe) = make()
        c.setFilter(.done)
        #expect(c.state.selection == F.id("d1"))
        probe.log = []
        c.reopen(F.id("d1"))
        #expect(source.snapshot.cases.first { $0.id == F.id("d1") }?.done == false)
        #expect(rows(c) == ["d2"] && c.state.selection == F.id("d2"))
        #expect(probe.log == [[.selection, .content]])
        c.reopen(F.id("d2"))
        #expect(c.state.selection == nil && c.view.sections.isEmpty)
        // The reopened cases are back on the board.
        c.setFilter(.all)
        #expect(rows(c).contains("d1") && rows(c).contains("d2"))
    }

    @Test func reopenFromAnotherViewKeepsTheSelection() {
        let (c, _, probe) = make()
        c.reopen(F.id("d1"))  // from the list under Overview: a new row appears
        #expect(c.state.selection == F.id("c1"))
        #expect(probe.log == [.content])
    }

    @Test func discardDraft() {
        let ann = Board.Annotation(state: .you, title: "T")
        let (c, source, probe) = make([F.mk("c1", annotation: ann, draft: "Hi,\n\nbye"), F.mk("c2", .you, hours: 2)], annotated: true)
        #expect(c.view.detail?.draft == "Hi,\n\nbye")
        c.discardDraft(F.id("c1"))
        #expect(source.snapshot.cases[0].draft == nil && source.snapshot.cases[0].annotation == ann)
        #expect(c.view.detail?.draft == "" && c.view.detail?.title == "T")
        #expect(probe.log == [.content])
        c.discardDraft(F.id("c1"))  // nothing left to discard
        c.discardDraft(F.id("c2"))  // no draft
        c.discardDraft(F.id("missing"))
        #expect(probe.log.count == 1)
    }

    // MARK: Refresh and the source changing from outside

    @Test func refreshWithoutChangeIsSilent() {
        let (c, _, probe) = make()
        let before = c.view
        c.refresh()
        #expect(c.view == before && probe.log.isEmpty)
    }

    @Test func refreshFollowsTheClock() {
        let clock = Clock()
        let ann = Board.Annotation(state: .you, title: "T", due: F.day(16, 10))
        let (c, _, probe) = make([F.mk("c1", annotation: ann)], annotated: true, clock: clock)
        #expect(c.view.sections[0].rows[0].due == "Tomorrow")
        #expect(c.view.today.dueGroups.map(\.kind) == [.tomorrow])
        clock.now = F.day(16, 9)  // a new day moves the deadline
        c.refresh()
        #expect(c.view.sections[0].rows[0].due == "Today")
        #expect(c.view.today.dueGroups.map(\.kind) == [.today])
        #expect(probe.log == [.content])
    }

    @Test func aSourceChangeFromOutside() {
        let (c, source, probe) = make()
        // A new, newer hot case: content only, the selection stays.
        var s = source.snapshot
        s.cases.append(F.mk("c6", .hot, hours: 0.5))
        source.replace(s)
        #expect(rows(c).first == "c6" && c.state.selection == F.id("c1"))
        #expect(probe.log == [.content])
        // The same snapshot again: silent.
        source.replace(s)
        #expect(probe.log.count == 1)
        // The selected case disappears: the first row.
        s.cases.removeAll { $0.id == F.id("c1") }
        source.replace(s)
        #expect(c.state.selection == F.id("c6"))
        #expect(probe.log.last == [.selection, .content])
    }

    @Test func aWriteStraightToTheSourceIsFollowed() {
        let (c, source, probe) = make()
        source.setDone(true, of: F.id("c1"))  // not through the controller
        #expect(c.state.selection == F.id("c3") && probe.log == [[.selection, .content]])
        source.setState(.them, of: F.id("c2"))
        #expect(c.view.nav.map(\.count) == [4, 0, 1, 2, 1, 0, 3])
    }

    @Test func replaceCanEmptyTheBoard() {
        let (c, source, probe) = make()
        c.select(F.id("c3"))
        probe.log = []
        source.replace(.empty)
        #expect(c.view.isEmpty && c.state.selection == nil && c.view.detail == nil)
        #expect(probe.log == [[.selection, .content]])
        // Data arriving later fills it again.
        source.replace(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
        #expect(!c.view.isEmpty && c.state.selection == F.id("c1"))
    }

    @Test func aWriteToAnUnknownCaseCallsNoOne() {
        let (c, _, probe) = make()
        c.setState(.hot, of: F.id("nope"))
        c.markDone(F.id("nope"))
        c.reopen(F.id("nope"))
        c.discardDraft(F.id("nope"))
        #expect(probe.log.isEmpty && c.state.selection == F.id("c1"))
    }

    // MARK: The departure after a write applies once

    /// The selected case moved where it stays shown, then dropped by the
    /// source: the rule for any outside change, not the neighbour noted
    /// before the move.
    @Test func aDepartureDoesNotOutliveTheReportOfItsWrite() {
        // (style, inline detail, the selection once c3 is gone)
        let table: [(Board.Style, Bool, String?)] = [
            (.list, true, "c1"), (.list, false, nil), (.columns, true, nil), (.today, true, nil),
        ]
        for (style, inline, want) in table {
            let (c, source, _) = make()
            c.setStyle(style)
            c.setInlineDetail(inline)
            c.select(F.id("c3"))
            c.setState(.info, of: F.id("c3"))  // still shown
            #expect(c.state.selection == F.id("c3"), "\(style) \(inline)")
            var s = source.snapshot
            s.cases.removeAll { $0.id == F.id("c3") }
            source.replace(s)
            #expect(c.state.selection?.rawValue == want, "\(style) \(inline)")
        }
    }

    @Test func aWriteThatChangesNothingNotesNoDeparture() {
        let (c, source, probe) = make()
        c.select(F.id("c3"))
        probe.log = []
        c.setState(.you, of: F.id("c3"))  // its automatic state already: nothing to write
        c.markDone(F.id("d1"))  // done already
        c.reopen(F.id("c3"))  // not done
        #expect(probe.log.isEmpty)
        var s = source.snapshot
        s.cases.removeAll { $0.id == F.id("c3") }
        source.replace(s)
        #expect(c.state.selection == F.id("c1"))  // the first row, not c2
    }

    @Test func aSourceThatReportsLater() {
        // Nothing in between: the departure waits for the report.
        do {
            let source = LateSource(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
            let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
            c.select(F.id("c3"))
            c.markDone(F.id("c3"))
            #expect(c.state.selection == F.id("c3"))  // not reported yet
            source.flush()
            #expect(c.state.selection == F.id("c2") && !rows(c).contains("c3"))
        }
        // The user selects another case meanwhile: their choice wins.
        do {
            let source = LateSource(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
            let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
            c.select(F.id("c3"))
            c.markDone(F.id("c3"))
            c.select(F.id("c4"))
            source.flush()
            #expect(c.state.selection == F.id("c4"))
            // And a later report removing c4 follows the rule for outside changes.
            var s = source.snapshot
            s.cases.removeAll { $0.id == F.id("c4") }
            source.snapshot = s
            source.flush()
            #expect(c.state.selection == F.id("c1"))
        }
    }

    @Test func markDoneInTheNarrowList() {
        let (c, source, probe) = make()
        c.setInlineDetail(false)
        c.select(F.id("c3"))
        #expect(c.view.showsPanel)
        probe.log = []
        c.markDone(F.id("c3"))
        #expect(c.state.selection == F.id("c2") && c.view.showsPanel)  // the panel moves to the next row
        #expect(probe.log == [[.selection, .content]])
        // An unrelated change later keeps it; dropping it selects nothing (no first row here).
        var s = source.snapshot
        s.cases.append(F.mk("c6", .hot, hours: 0.5))
        source.replace(s)
        #expect(c.state.selection == F.id("c2"))
        s.cases.removeAll { $0.id == F.id("c2") }
        source.replace(s)
        #expect(c.state.selection == nil && !c.view.showsPanel)
    }

    // MARK: An account filter whose account goes away

    @Test func aVanishedAccountFallsBackToAll() {
        let (c, source, probe) = make()
        c.setAccount(.account(F.accountB))
        #expect(c.state.account == .account(F.accountB) && rows(c) == ["c5"])
        probe.log = []
        var s = source.snapshot
        s.accounts.removeAll { $0.id == F.accountB }
        s.cases.removeAll { $0.account == F.accountB }
        source.replace(s)
        #expect(c.state.account == .all)
        #expect(rows(c) == ["c1", "c3", "c2", "c4"] && c.state.selection == F.id("c1"))
        #expect(c.view.accounts.allSatisfy { $0.filter != .account(F.accountB) })
        #expect(c.view.accounts.first?.selected == true && c.view.accountTitle == "All Accounts")
        #expect(probe.log == [[.filters, .selection, .content]])
        // Choosing an account that is not there is no change.
        c.setAccount(.account(F.accountB))
        #expect(c.state.account == .all && probe.log.count == 1)
    }

    // MARK: Calls from inside onChange

    @Test func aListenerThatSelectsGetsItsChangeAfterwards() {
        let (c, _, _) = make()
        c.select(F.id("c1"))
        var log: [(Changes, String?)] = []
        var nested = false
        c.onChange = { changes in
            log.append((changes, c.view.selection?.rawValue))
            #expect(c.view.selection == c.state.selection)
            if !nested {
                nested = true
                c.select(F.id("c4"))
                // Not yet delivered: this call is still the outer one.
                #expect(log.count == 1)
            }
        }
        c.setStyle(.columns)
        #expect(log.count == 2)
        #expect(log[0].0 == [.style, .selection] && log[0].1 == "c1")
        #expect(log[1].0 == [.selection] && log[1].1 == "c4")
        #expect(c.state.selection == F.id("c4"))
    }

    @Test func aListenerThatMarksDoneGetsItsChangeAfterwards() {
        let (c, _, _) = make()
        var log: [(Changes, String?, [String])] = []
        var nested = false
        c.onChange = { changes in
            log.append((changes, c.view.selection?.rawValue, rows(c)))
            if !nested {
                nested = true
                c.markDone(F.id("c3"))
                #expect(log.count == 1)
            }
        }
        c.select(F.id("c3"))
        #expect(log.count == 2)
        #expect(log[0].0 == [.selection] && log[0].1 == "c3" && log[0].2.contains("c3"))
        #expect(log[1].0 == [.selection, .content] && log[1].1 == "c2" && !log[1].2.contains("c3"))
    }

    @Test func dummySources() {
        let on = InMemoryBoardSource.dummy(samples: true, now: F.now, calendar: F.calendar)
        #expect(on.snapshot == Board.sampleSnapshot(now: F.now, calendar: F.calendar))
        let off = InMemoryBoardSource.dummy(samples: false, now: F.now, calendar: F.calendar)
        #expect(off.snapshot == .empty)
        let c = BoardController(source: on, now: { F.now }, calendar: F.calendar)
        #expect(c.state.selection != nil && c.view.detail != nil)
    }

    // MARK: Remind, archive, promises, the conversation, toasts

    @Test func remindTakesTheCaseOffLikeDone() {
        let (c, source, probe) = make()
        c.select(F.id("c3"))
        probe.log = []
        let until = F.day(16, 9)
        c.remind(F.id("c3"), until: until)
        #expect(source.snapshot.cases.first { $0.id == F.id("c3") }?.visibility == .snoozed(until: until))
        #expect(rows(c) == ["c1", "c2", "c4", "c5"])
        #expect(c.state.selection == F.id("c2"))
        #expect(probe.log == [[.selection, .content]])
        // The same remind again changes nothing.
        c.remind(F.id("c3"), until: until)
        #expect(probe.log.count == 1)
        // Under Done, ending the remind puts it back and moves on.
        c.setFilter(.done)
        c.select(F.id("c3"))
        c.remind(F.id("c3"), until: nil)
        #expect(source.snapshot.cases.first { $0.id == F.id("c3") }?.visibility == .live)
        #expect(c.state.selection == F.id("d1"))
    }

    @Test func remindAPresetFromTheController() {
        let (c, _, _) = make()
        #expect(c.remindPresets() == Board.remindPresets(now: F.now, calendar: F.calendar))
    }

    @Test func archiveMarksDoneAndToasts() {
        var cases = sampleCases()
        cases[2].canArchive = true  // c3
        let (c, source, _) = make(cases)
        var toasts: [String] = []
        c.onToast = { toasts.append($0) }
        c.select(F.id("c3"))
        c.archive(F.id("c3"))
        #expect(source.snapshot.cases.first { $0.id == F.id("c3") }?.done == true)
        #expect(c.state.selection == F.id("c2"))
        #expect(toasts == ["Archived 1 message."])
        c.archive(F.id("c2"))
        #expect(toasts.last == "Marked as done. This account has no archive.")
    }

    @Test func setCommitmentDone() {
        let k = Board.Commitment(id: "k1", caseID: F.id("c1"), text: "Promise")
        let source = InMemoryBoardSource(
            Board.Snapshot(accounts: F.accounts, cases: sampleCases(), commitments: [k], annotated: true))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        #expect(c.view.commitments.map(\.id) == ["k1"])
        c.setCommitmentDone("k1", done: true)
        #expect(source.snapshot.commitments[0].state == .done && c.view.commitments.isEmpty)
        c.setCommitmentDone("k1", done: false)
        #expect(c.view.commitments.map(\.id) == ["k1"])
    }

    /// Selecting a case asks for its conversation once per case and
    /// version.
    @Test func selectionLoadsTheConversation() {
        var cases = sampleCases()
        for i in cases.indices { cases[i].messages = nil }
        let source = LateSource(Board.Snapshot(accounts: F.accounts, cases: cases))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        #expect(source.loads == [F.id("c1")])  // the list's first row, at once
        #expect(c.view.detail?.messagesLoading == true)
        c.select(F.id("c3"))
        c.refresh()
        #expect(source.loads == [F.id("c1"), F.id("c3")])
        // A new version of the selected case asks again.
        source.snapshot.cases[2].version = 2
        source.flush()
        #expect(source.loads == [F.id("c1"), F.id("c3"), F.id("c3")])
        // Back to c1: asked again (the source knows whether it has it).
        c.select(F.id("c1"))
        #expect(source.loads.last == F.id("c1") && source.loads.count == 4)
        // Nothing selected asks for nothing.
        c.setStyle(.columns)
        #expect(source.loads.count == 4)
    }

    @Test func sourceToastsReachThePage() {
        let (c, source, _) = make()
        var toasts: [String] = []
        c.onToast = { toasts.append($0) }
        source.onError?("bad")
        source.onNotice?("good")
        #expect(toasts == ["bad", "good"])
    }

    @Test func phaseAndTriageInTheView() {
        let source = InMemoryBoardSource(
            Board.Snapshot(accounts: F.accounts, phase: .preparing, triage: Board.Triage(queue: 3, annotatedToday: 1)))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        #expect(c.phase == .preparing && c.view.phase == .preparing)
        #expect(c.view.emptyTitle == Board.Text.emptyTitle(.preparing))
        #expect(c.view.triage.queue == 3)
        var s = source.snapshot
        s.phase = .ready
        source.replace(s)
        #expect(c.view.phase == .ready && c.view.emptyTitle == Board.Text.emptyTitle)
    }


    /// A conversation that could not be loaded is asked for again when the
    /// case is selected again, when the board comes back from a failure,
    /// and from the detail's Try Again; not on every report.
    @Test func aFailedConversationIsAskedForAgain() {
        var cases = sampleCases()
        for i in cases.indices { cases[i].messages = nil }
        let source = LateSource(Board.Snapshot(accounts: F.accounts, cases: cases))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        #expect(source.loads == [F.id("c1")])
        // The load fails.
        source.snapshot.cases[0].messagesFailed = true
        source.flush()
        #expect(c.view.detail?.messagesRetry == true && c.view.detail?.messagesNote == Board.Text.messagesFailed)
        #expect(source.loads.count == 1)  // not by itself
        // Selected again: asked again.
        c.select(F.id("c1"))
        #expect(source.loads == [F.id("c1"), F.id("c1")])
        source.flush()
        #expect(source.loads.count == 2)
        // Try Again.
        c.retryMessages()
        #expect(source.loads.count == 3)
        // The connection goes and comes back with the same version: asked
        // again once the board is back.
        source.snapshot.phase = .unavailable
        source.flush()
        #expect(source.loads.count == 3)
        source.snapshot.phase = .ready
        source.flush()
        #expect(source.loads.count == 4)
        source.flush()
        #expect(source.loads.count == 4)
        // Loaded: Try Again and selecting it again ask for nothing more.
        source.snapshot.cases[0].messagesFailed = false
        source.snapshot.cases[0].messages = []
        source.flush()
        #expect(c.view.detail?.messagesRetry == false)
        c.retryMessages()
        c.select(F.id("c1"))
        #expect(source.loads.count == 4)
    }

    /// Entering the board while it could not be listed asks for it again.
    @Test func boardShownRetriesAFailedBoard() {
        let source = LateSource(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        c.boardShown()
        #expect(source.refreshes == 0)
        for phase in [Board.Phase.unavailable, .failed, .unsupported] {
            source.snapshot.phase = phase
            source.flush()
            let before = source.refreshes
            c.boardShown()
            #expect(source.refreshes == before + 1, "\(phase)")
        }
    }

    // MARK: The default style (Settings → General → Board)

    /// The style of the first show is the setting's, read at that moment.
    @Test func defaultStyleAtFirstShow() {
        var setting = Board.DefaultStyle.style(.columns)
        let source = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar, defaultStyle: { setting })
        let probe = Probe()
        c.onChange = { probe.log.append($0) }
        // Until the board shows it holds the List.
        #expect(c.state.style == .list)
        #expect(!c.hasShown)
        // Changed before the first show: that one counts.
        setting = .style(.today)
        c.boardWillShow()
        #expect(c.hasShown)
        #expect(c.state.style == .today)
        #expect(probe.log.contains { $0.contains(.style) })
        // The default List changes nothing.
        let list = make()
        list.c.boardWillShow()
        #expect(list.c.state.style == .list)
        #expect(list.probe.log.isEmpty)
    }

    /// Once the user picked a style it is the user's: leaving and entering
    /// again keeps it, and so does a setting changed meanwhile.
    @Test func laterShowsKeepTheUsersStyle() {
        var setting = Board.DefaultStyle.style(.columns)
        let source = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: sampleCases()))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar, defaultStyle: { setting })
        c.boardWillShow()
        c.boardShown()
        #expect(c.state.style == .columns)
        c.setStyle(.today)
        // Back from Mail.
        c.boardWillShow()
        c.boardShown()
        #expect(c.state.style == .today)
        // The setting changes after the user picked one: the style stays.
        setting = .style(.list)
        c.boardWillShow()
        #expect(c.state.style == .today)
        setting = .style(.columns)
        c.boardWillShow()
        #expect(c.state.style == .today)
        // The user's own choice still works.
        c.setStyle(.list)
        c.boardWillShow()
        #expect(c.state.style == .list)
    }

    @Test func styleOnShowRule() {
        for current in Board.Style.allCases {
            for last in Board.Style.allCases {
                for d in Board.defaultStyles {
                    let want: Board.Style
                    switch d {
                    case .last: want = last
                    case .style(let s): want = s
                    }
                    #expect(Board.styleOnShow(defaultStyle: d, lastStyle: last, current: current, pickedThisRun: false) == want)
                    #expect(Board.styleOnShow(defaultStyle: d, lastStyle: last, current: current, pickedThisRun: true) == current)
                }
            }
        }
    }

    @Test func styleNicks() {
        #expect(Board.Style.allCases.map(\.nick) == ["list", "columns", "today"])
        for s in Board.Style.allCases {
            #expect(Board.parseStyle(s.nick) == s)
        }
        for junk in ["", "List", "0", "1", "grid", " today"] {
            #expect(Board.parseStyle(junk) == .list, "\(junk)")
        }
    }
}
