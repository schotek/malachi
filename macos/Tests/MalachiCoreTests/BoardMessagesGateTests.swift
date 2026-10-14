// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's messages gate (MalachiCore/Board/BoardMessagesGate.swift),
// ports of the GTK window's TestBoardMessagesGate and
// TestBoardMessagesGateUndo, and Open at Launch's rule (`Board.startDecision`).

private typealias F = BoardFixture

/// A source that counts the conversations asked for and forwards the rest
/// to an in-memory one.
@MainActor private final class CountingSource: BoardSource {
    let inner: InMemoryBoardSource
    var loads: [Board.CaseID] = []

    init(_ inner: InMemoryBoardSource) {
        self.inner = inner
    }

    var snapshot: Board.Snapshot { inner.snapshot }
    var onChange: (@MainActor () -> Void)? {
        get { inner.onChange }
        set { inner.onChange = newValue }
    }
    var onError: (@MainActor (String) -> Void)? {
        get { inner.onError }
        set { inner.onError = newValue }
    }
    var onNotice: (@MainActor (String) -> Void)? {
        get { inner.onNotice }
        set { inner.onNotice = newValue }
    }
    var onArchived: (@MainActor (Board.ArchiveOutcome) -> Void)? {
        get { inner.onArchived }
        set { inner.onArchived = newValue }
    }
    func setState(_ state: Board.State?, of id: Board.CaseID) { inner.setState(state, of: id) }
    func setDone(_ done: Bool, of id: Board.CaseID) { inner.setDone(done, of: id) }
    func remind(until: Date?, of id: Board.CaseID) { inner.remind(until: until, of: id) }
    func archive(_ id: Board.CaseID) { inner.archive(id) }
    func setCommitmentDone(_ done: Bool, of id: String) { inner.setCommitmentDone(done, of: id) }
    func discardDraft(of id: Board.CaseID) { inner.discardDraft(of: id) }
    func unflag(_ id: Board.CaseID) { inner.unflag(id) }
    func loadMessages(of id: Board.CaseID) {
        loads.append(id)
        inner.loadMessages(of: id)
    }
    func refresh() { inner.refresh() }
}

@MainActor @Suite struct BoardMessagesGateTests {
    /// Closed (Mail), nothing is fetched; opening asks for the last case
    /// held, once; open, a case is asked for at once.
    @Test func holdsWhileClosed() {
        let inner = CountingSource(InMemoryBoardSource(.empty))
        let g = BoardMessagesGate(inner)
        g.loadMessages(of: F.id("c1"))
        g.loadMessages(of: F.id("c2"))
        #expect(inner.loads.isEmpty)
        g.isOpen = true
        g.isOpen = true
        #expect(inner.loads == [F.id("c2")])
        g.loadMessages(of: F.id("c3"))
        #expect(inner.loads == [F.id("c2"), F.id("c3")])
        // Closed and opened again with nothing held: nothing.
        g.isOpen = false
        g.isOpen = true
        #expect(inner.loads.count == 2)
    }

    /// The gate keeps Undo working for a source that cannot move messages
    /// back: the case is only reopened.
    @Test func undoThroughTheGate() throws {
        let src = InMemoryBoardSource.dummy(samples: true, now: F.now, calendar: F.calendar)
        let id = try #require(src.snapshot.cases.first { !$0.visibility.isDone }?.id)
        let g = BoardMessagesGate(src)
        g.isOpen = true
        g.setDone(true, of: id)
        #expect(src.snapshot.cases.first { $0.id == id }?.visibility.isDone == true)
        g.undoArchive(Board.ArchiveOutcome(caseID: id, account: "a", text: "", undoLabel: nil))
        #expect(src.snapshot.cases.first { $0.id == id }?.visibility.isDone == false)
    }

    /// Opened after `boardWillShow` (the window's order): the first show
    /// asks for one conversation, the one the board shows.
    @Test func firstShowAsksOnce() {
        let inner = CountingSource(InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: [F.mk("c1", .hot), F.mk("c2", .you)])))
        let g = BoardMessagesGate(inner)
        let c = BoardController(source: g, now: { F.now }, calendar: F.calendar)
        #expect(inner.loads.isEmpty)
        c.boardWillShow()
        g.isOpen = true
        #expect(inner.loads == [c.state.selection].compactMap { $0 })
    }
}

/// Open at Launch's rule (Go `TestStartDecision`, the same table).
@Suite struct BoardStartDecisionTests {
    @Test func table() {
        let no = false, yes = true
        typealias M = Board.Mode
        let w = Board.startWait
        let cases: [(String, Board.StartChoice, M, Bool, Bool, Bool, Bool, TimeInterval, M, Bool)] = [
            ("mail start decides at once", .mail, .board, no, yes, no, no, 0, .mail, yes),
            ("last used with mail last decides at once", .last, .mail, no, yes, no, no, 0, .mail, yes),
            ("board start waits for prefs", .board, .mail, no, yes, no, no, 1, .mail, no),
            ("last used board waits for prefs", .last, .board, no, yes, no, no, 4, .mail, no),
            ("board start, prefs on", .board, .mail, yes, yes, no, no, 1, .board, yes),
            ("last used board, prefs on", .last, .board, yes, yes, no, no, 0, .board, yes),
            ("board start, board turned off", .board, .mail, yes, no, no, no, 0, .mail, yes),
            ("user switched first", .board, .mail, no, yes, yes, no, 0, .mail, yes),
            ("user switched, prefs on too", .board, .mail, yes, yes, yes, no, 0, .mail, yes),
            ("user acted in mail", .last, .board, no, yes, no, yes, 1, .mail, yes),
            ("user acted in mail before prefs on", .board, .mail, yes, yes, no, yes, 0, .mail, yes),
            ("bound passed without prefs", .board, .mail, no, yes, no, no, w, .mail, yes),
            ("long after the bound", .last, .board, no, yes, no, no, 60, .mail, yes),
            ("just under the bound", .board, .mail, no, yes, no, no, w - 0.001, .mail, no),
        ]
        for (name, start, last, known, enabled, sw, acted, waited, mode, decided) in cases {
            let got = Board.startDecision(
                start: start, lastMode: last, prefsKnown: known, enabled: enabled, userSwitched: sw,
                userInteracted: acted, waited: waited)
            #expect(got.mode == mode && got.decided == decided, "\(name)")
        }
        #expect(Board.startWait == 5)
    }
}
