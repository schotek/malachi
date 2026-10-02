// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board from the daemon (MalachiCore/Board/DaemonBoardSource.swift)
// against a fake daemon: loading, notifications with their debounce, one
// board.list at a time, dropped stale replies, optimistic writes and their
// revert, the phases and the retries, the conversations cached by version.
// No test depends on how fast the machine is: the source sleeps on a clock
// the test advances (`ManualClock`), the fake daemon holds replies until
// the test releases them, and "nothing happened" is checked once the
// source is idle (`isIdle`: nothing waiting or on its way).

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(2))
    }
}

/// The source's sleeps (the debounce, a retry's back-off): each waits until
/// the test fires the clock.
private actor ManualClock {
    private var waiting: [(Duration, CheckedContinuation<Void, Never>)] = []

    func sleep(_ d: Duration) async {
        await withCheckedContinuation { waiting.append((d, $0)) }
    }

    /// The durations of the sleeps waiting, in order.
    var pending: [Duration] { waiting.map(\.0) }

    /// Ends every sleep waiting.
    func fire() {
        let w = waiting
        waiting = []
        for (_, c) in w {
            c.resume()
        }
    }
}

/// The replies the fake daemon can hold until the test releases them.
private enum Held: Hashable {
    case lists, writes, gets
}

private let t0 = Date(timeIntervalSince1970: 1_790_000_000)

private func wireCase(_ n: String, _ state: BoardState = .you, version: Int64 = 1) -> BoardCase {
    BoardCase(
        id: BoardCaseID(rawValue: "c_\(n)"), accountId: "acc_1", threadId: ThreadID(rawValue: "t_\(n)"),
        ruleState: state, ruleReason: .youAddressed, subject: "Subject \(n)",
        person: Address(name: "Person \(n)", address: "\(n)@example.invalid"), date: t0, messageCount: 2,
        replyMessageId: MessageID(rawValue: "m_\(n)"), replyFolderId: "f_inbox",
        latestMessageId: MessageID(rawValue: "m_\(n)"), canArchive: true, version: version)
}

private func caseID(_ n: String) -> Board.CaseID { Board.CaseID(rawValue: "c_\(n)") }

private func commitment(_ state: BoardCommitmentState = .open) -> BoardCommitment {
    BoardCommitment(
        id: "k_1", caseId: "c_1", accountId: "acc_1", messageId: "m_1", text: "Send it", quote: "I will send it",
        state: state, at: t0)
}

/// The daemon's side of the board: its cases, and what each method did.
private actor BoardScript {
    var cases: [BoardCase] = [wireCase("1"), wireCase("2", .hot)]
    var commitments: [BoardCommitment] = []
    var accountName = "Work"
    var enabled = true
    var ready = true
    var assistant = false
    var lastRun: BoardRun?
    var listFailure: RPCError?
    var writeFailure: RPCError?
    var getFailure: RPCError?
    var calls: [String] = []
    private var holding: Set<Held> = []
    private var waiting: [Held: [CheckedContinuation<Void, Never>]] = [:]

    func set(cases: [BoardCase]) { self.cases = cases }
    func set(commitments: [BoardCommitment]) { self.commitments = commitments }
    func set(accountName: String) { self.accountName = accountName }
    func set(enabled: Bool, ready: Bool) { self.enabled = enabled; self.ready = ready }
    func set(listFailure: RPCError?) { self.listFailure = listFailure }
    func set(lastRun: BoardRun?) { self.lastRun = lastRun }
    func set(writeFailure: RPCError?) { self.writeFailure = writeFailure }
    func set(getFailure: RPCError?) { self.getFailure = getFailure }
    private(set) var deleted: [DraftDeleteParams] = []
    func delete(_ params: Data) throws -> Data {
        calls.append(API.DraftDelete.name)
        deleted.append(try JSONCoding.decoder().decode(DraftDeleteParams.self, from: params))
        return try JSONCoding.encoder().encode(EmptyResult())
    }
    func count(_ method: String) -> Int { calls.filter { $0 == method }.count }

    /// Holds the replies of board.list (writes, board.get) from now on, or
    /// lets them through again and releases the ones held.
    func hold(lists on: Bool) { hold(.lists, on) }
    func hold(writes on: Bool) { hold(.writes, on) }
    func hold(gets on: Bool) { hold(.gets, on) }
    var listsHeld: Int { waiting[.lists, default: []].count }
    var writesHeld: Int { waiting[.writes, default: []].count }
    var getsHeld: Int { waiting[.gets, default: []].count }

    private func hold(_ which: Held, _ on: Bool) {
        if on {
            holding.insert(which)
            return
        }
        holding.remove(which)
        let w = waiting[which, default: []]
        waiting[which] = nil
        for c in w {
            c.resume()
        }
    }

    private func wait(_ which: Held) async {
        guard holding.contains(which) else { return }
        await withCheckedContinuation { waiting[which, default: []].append($0) }
    }

    func accountList() -> Data {
        calls.append(API.AccountList.name)
        let a = Account(
            id: "acc_1", config: AccountConfig(name: accountName, email: "me@example.invalid"), enabled: true,
            state: SyncState(accountId: "acc_1", status: .idle))
        let off = Account(
            id: "acc_2", config: AccountConfig(name: "Paused", email: "p@example.invalid"), enabled: false,
            state: SyncState(accountId: "acc_2", status: .idle))
        return try! JSONCoding.encoder().encode(AccountListResult(accounts: [a, off]))
    }

    func list() async throws -> Data {
        calls.append(API.BoardList.name)
        // What the daemon has at the time of the call, sent when released.
        let result = BoardListResult(
            cases: enabled ? cases : [], commitments: commitments, enabled: enabled, assistant: assistant,
            triage: BoardTriage(lastRun: lastRun, annotatedTodayAuto: 2, queue: 4), ready: ready)
        let failure = listFailure
        await wait(.lists)
        if let failure { throw failure }
        return try JSONCoding.encoder().encode(result)
    }

    func get(_ params: Data) async throws -> Data {
        calls.append(API.BoardGet.name)
        let failure = getFailure
        let p = try JSONCoding.decoder().decode(BoardGetParams.self, from: params)
        let c = cases.first(where: { $0.id == p.caseId })
        await wait(.gets)
        if let failure { throw failure }
        guard let c else {
            throw RPCError(code: .caseNotFound, message: "no case")
        }
        let m = BoardMessage(
            id: "m_a", folderId: "f_inbox", from: Address(name: "Ann", address: "ann@example.invalid"), date: t0,
            text: "version \(c.version)")
        return try JSONCoding.encoder().encode(BoardGetResult(case: c, messages: [m]))
    }

    /// A write: changes case `id` (version + 1) and answers it.
    func write(_ method: String, _ id: BoardCaseID, _ change: (inout BoardCase) -> Void) async throws -> BoardCase {
        calls.append(method)
        await wait(.writes)
        if let writeFailure { throw writeFailure }
        guard let i = cases.firstIndex(where: { $0.id == id }) else {
            throw RPCError(code: .caseNotFound, message: "no case")
        }
        change(&cases[i])
        cases[i].version += 1
        return cases[i]
    }

    /// What the rules make of an unflagged case: no longer hot.
    func unflagged(_ id: BoardCaseID) {
        guard let i = cases.firstIndex(where: { $0.id == id }) else { return }
        cases[i].ruleState = .you
        cases[i].ruleReason = .youAddressed
        cases[i].version += 1
    }

    func setCommitment(_ params: Data) async throws -> Data {
        calls.append(API.BoardSetCommitment.name)
        await wait(.writes)
        if let writeFailure { throw writeFailure }
        let p = try JSONCoding.decoder().decode(BoardSetCommitmentParams.self, from: params)
        guard let i = commitments.firstIndex(where: { $0.id == p.commitmentId }) else {
            throw RPCError(code: .caseNotFound, message: "no commitment")
        }
        commitments[i].state = p.done ? .done : .open
        return try JSONCoding.encoder().encode(BoardSetCommitmentResult(commitment: commitments[i]))
    }
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = BoardScript()
    let clock = ManualClock()
    let client: RPCClient
    let source: DaemonBoardSource
    var reports = 0
    var errors: [String] = []
    var notices: [String] = []

    init(connect: Bool = true) async throws {
        fake = try FakeDaemon()
        let s = script
        await fake.on(API.BoardList.name) { _ in try await s.list() }
        await fake.on(API.BoardGet.name) { p in try await s.get(p) }
        await fake.on(API.AccountList.name) { _ in await s.accountList() }
        await fake.on(API.BoardSetState.name) { p in
            let q = try JSONCoding.decoder().decode(BoardSetStateParams.self, from: p)
            let c = try await s.write(API.BoardSetState.name, q.caseId) { $0.userState = q.state }
            return try JSONCoding.encoder().encode(BoardSetStateResult(case: c))
        }
        await fake.on(API.BoardSetDone.name) { p in
            let q = try JSONCoding.decoder().decode(BoardSetDoneParams.self, from: p)
            let c = try await s.write(API.BoardSetDone.name, q.caseId) {
                $0.visibility = q.done ? .done : .live
                $0.doneAt = q.done ? t0 : nil
                $0.remindAt = nil
            }
            return try JSONCoding.encoder().encode(BoardSetDoneResult(case: c))
        }
        await fake.on(API.BoardRemind.name) { p in
            let q = try JSONCoding.decoder().decode(BoardRemindParams.self, from: p)
            let c = try await s.write(API.BoardRemind.name, q.caseId) {
                $0.visibility = q.until == nil ? .live : .snoozed
                $0.remindAt = q.until
            }
            return try JSONCoding.encoder().encode(BoardRemindResult(case: c))
        }
        await fake.on(API.BoardArchive.name) { p in
            let q = try JSONCoding.decoder().decode(BoardArchiveParams.self, from: p)
            let c = try await s.write(API.BoardArchive.name, q.caseId) {
                $0.visibility = .done
                $0.canArchive = false
            }
            return try JSONCoding.encoder().encode(BoardArchiveResult(archived: 2, case: c))
        }
        await fake.on(API.BoardDiscardDraft.name) { p in
            let q = try JSONCoding.decoder().decode(BoardDiscardDraftParams.self, from: p)
            let c = try await s.write(API.BoardDiscardDraft.name, q.caseId) { $0.draft = nil }
            return try JSONCoding.encoder().encode(BoardDiscardDraftResult(case: c))
        }
        await fake.on(API.BoardUnflag.name) { p in
            let q = try JSONCoding.decoder().decode(BoardUnflagParams.self, from: p)
            // The case as stored: the rules re-evaluate it later (the
            // board.list after the write shows what they made of it).
            let c = try await s.write(API.BoardUnflag.name, q.caseId) { _ in }
            await s.unflagged(q.caseId)
            return try JSONCoding.encoder().encode(BoardUnflagResult(case: c, unflagged: 2))
        }
        await fake.on(API.BoardSetCommitment.name) { p in try await s.setCommitment(p) }
        await fake.on(API.DraftDelete.name) { p in try await s.delete(p) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        if connect {
            try await client.connect()
        }
        let clock = clock
        source = DaemonBoardSource(client: client, sleep: { await clock.sleep($0) })
        source.onChange = { [weak self] in self?.reports += 1 }
        source.onError = { [weak self] in self?.errors.append($0) }
        source.onNotice = { [weak self] in self?.notices.append($0) }
    }

    /// Starts and waits for the board and the accounts.
    func started() async throws -> Harness {
        source.start()
        try await idle()
        return self
    }

    /// Waits until nothing is waiting or on its way in the source.
    func idle() async throws {
        try await waitUntil { self.source.isIdle }
    }

    func stop() async {
        source.stop()
        await script.hold(lists: false)
        await script.hold(writes: false)
        await script.hold(gets: false)
        await clock.fire()
        await client.close()
        await fake.stop()
    }

    func c(_ n: String) -> Board.Case? {
        source.snapshot.cases.first { $0.id == caseID(n) }
    }

    func count(_ method: String) async -> Int {
        await script.count(method)
    }
}

@MainActor
@Suite(.serialized) struct DaemonBoardSourceTests {
    @Test func loadsTheBoardAndTheAccounts() async throws {
        let h = try await Harness().started()
        let s = h.source.snapshot
        #expect(s.phase == .ready)
        #expect(s.accounts == [Board.AccountInfo(id: "acc_1", name: "Work", badge: "IMAP")])  // enabled only
        #expect(s.cases.map(\.id) == [caseID("1"), caseID("2")])
        let c = try #require(h.c("1"))
        #expect(c.person == "Person 1" && c.subject == "Subject 1" && c.ruleState == .you)
        #expect(c.ruleReason == .youAddressed && c.thread == "t_1" && c.canArchive && c.version == 1)
        #expect(c.reply == Board.ReplyTarget(message: "m_1", folder: "f_inbox") && c.latestMessage == "m_1")
        #expect(c.messages == nil && c.visibility == .live)
        #expect(s.triage == Board.Triage(queue: 4, annotatedToday: 2) && !s.annotated)
        await h.stop()
    }

    /// Every published snapshot also goes to `onSnapshot` (the
    /// application's triage); the last run carries its trigger and start.
    @Test func snapshotsReachTheTriage() async throws {
        let h = try await Harness()
        let started = t0.addingTimeInterval(-600)
        await h.script.set(lastRun: BoardRun(
            at: started, endedAt: t0, trigger: .auto, source: "claude-code", annotated: 3, error: .timeout))
        var seen: [Board.Snapshot] = []
        h.source.onSnapshot = { seen.append($0) }
        _ = try await h.started()
        #expect(!seen.isEmpty && seen.last == h.source.snapshot)
        #expect(seen.count == h.reports)
        let run = try #require(h.source.snapshot.run)
        #expect(run.trigger == "auto" && run.started == started && run.date == t0)
        #expect(run.annotated == 3 && run.error == "timeout" && !run.running && run.model == "claude-code")
        await h.stop()
    }

    @Test func phases() async throws {
        let h = try await Harness()
        await h.script.set(enabled: true, ready: false)
        _ = try await h.started()
        #expect(h.source.phase == .preparing)
        await h.script.set(enabled: false, ready: true)
        h.source.refresh()
        try await h.idle()
        #expect(h.source.phase == .off && h.source.snapshot.cases.isEmpty)
        // The board comes back: ready.
        await h.script.set(enabled: true, ready: true)
        h.source.refresh()
        try await h.idle()
        #expect(h.source.phase == .ready && h.source.snapshot.cases.count == 2)
        await h.stop()
    }

    /// A list the daemon could not answer is phase failed, keeps the cases
    /// and is asked again after a back-off that grows; an answer ends it.
    @Test func aFailedListIsAskedAgainLater() async throws {
        let h = try await Harness().started()
        await h.script.set(listFailure: RPCError(code: .storageError, message: "disk"))
        h.source.refresh()
        try await waitUntil { await h.clock.pending == [.seconds(2)] }
        #expect(h.source.phase == .failed && h.source.snapshot.cases.count == 2 && h.errors.isEmpty)
        #expect(await h.count(API.BoardList.name) == 2)
        // Still failing: the next wait is longer.
        await h.clock.fire()
        try await waitUntil { await h.clock.pending == [.seconds(4)] }
        #expect(await h.count(API.BoardList.name) == 3)
        await h.clock.fire()
        try await waitUntil { await h.clock.pending == [.seconds(8)] }
        // Asked for now (the board shown again): the wait is over at once.
        await h.script.set(listFailure: nil)
        h.source.refresh()
        try await h.idle()
        #expect(h.source.phase == .ready)
        #expect(await h.count(API.BoardList.name) == 5)
        // The next failure starts the back-off over.
        await h.script.set(listFailure: RPCError(code: .internalError, message: "x"))
        h.source.refresh()
        try await waitUntil { await h.clock.pending.last == .seconds(2) }
        #expect(h.source.phase == .failed)
        await h.stop()
    }

    @Test func retryDelays() {
        #expect((1...8).map(DaemonBoardSource.retryDelay) == [2, 4, 8, 16, 32, 60, 60, 60].map { Duration.seconds($0) })
    }

    /// A backend without the board says so and is not asked again by
    /// itself; one that is not running is unavailable.
    @Test func aBackendWithoutTheBoard() async throws {
        let h = try await Harness().started()
        await h.script.set(listFailure: RPCError(code: .methodNotFound, message: "board.list"))
        h.source.refresh()
        try await h.idle()
        #expect(h.source.phase == .unsupported)
        #expect(await h.clock.pending.isEmpty)
        await h.client.close()
        h.source.refresh()
        try await h.idle()
        #expect(h.source.phase == .unavailable)
        #expect(await h.clock.pending.isEmpty)
        await h.stop()
    }

    /// Without a daemon the board is unavailable; the connection coming
    /// loads it.
    @Test func unavailableDaemonAndReconnect() async throws {
        let h = try await Harness(connect: false)
        _ = try await h.started()
        #expect(h.source.phase == .unavailable && h.source.snapshot.cases.isEmpty)
        try await h.client.connect()
        h.source.connectionChanged(connected: true)
        try await h.idle()
        #expect(h.source.phase == .ready && h.source.snapshot.accounts.count == 1)
        #expect(h.source.snapshot.cases.count == 2)
        // The connection goes: the last snapshot stays.
        h.source.connectionChanged(connected: false)
        #expect(h.source.phase == .unavailable && h.source.snapshot.cases.count == 2)
        await h.stop()
    }

    /// Nothing loads before start() or after stop(): nothing is even on its
    /// way.
    @Test func nothingLoadsOutsideStartAndStop() async throws {
        let h = try await Harness()
        h.source.boardChanged()
        h.source.accountsChanged()
        h.source.connectionChanged(connected: true)
        h.source.refresh()
        #expect(h.source.isIdle && h.source.phase == .loading)
        #expect(await h.clock.pending.isEmpty)
        _ = try await h.started()
        let lists = await h.count(API.BoardList.name)
        let accounts = await h.count(API.AccountList.name)
        #expect(lists == 1 && accounts == 1)
        h.source.stop()
        h.source.boardChanged()
        h.source.accountsChanged()
        h.source.connectionChanged(connected: true)
        h.source.refresh()
        #expect(h.source.isIdle)
        #expect(await h.count(API.BoardList.name) == lists)
        #expect(await h.count(API.AccountList.name) == accounts)
        await h.stop()
    }

    /// Notifications in a burst make one board.list after the debounce.
    @Test func notificationsAreDebounced() async throws {
        let h = try await Harness().started()
        await h.script.set(cases: [wireCase("1"), wireCase("2", .hot), wireCase("3", .info)])
        for _ in 0..<5 {
            h.source.boardChanged(BoardChangedNotification(accountIds: ["acc_1"]))
        }
        try await waitUntil { await h.clock.pending == [DaemonBoardSource.defaultDebounce] }
        #expect(await h.count(API.BoardList.name) == 1)  // not before the debounce
        await h.clock.fire()
        try await h.idle()
        #expect(h.source.snapshot.cases.count == 3)
        #expect(await h.count(API.BoardList.name) == 2)
        await h.stop()
    }

    /// A board.list asked for while one is on its way waits for it and runs
    /// once after it, however often it was asked for: a slow list under a
    /// stream of notifications still lands.
    @Test func oneListAtATime() async throws {
        let h = try await Harness().started()
        await h.script.hold(lists: true)
        h.source.refresh()
        try await waitUntil { await h.script.listsHeld == 1 }
        await h.script.set(cases: [wireCase("1"), wireCase("2", .hot), wireCase("3", .info)])
        for _ in 0..<3 {
            h.source.boardChanged()
            try await waitUntil { await h.clock.pending.count == 1 }
            await h.clock.fire()
            try await waitUntil { await h.clock.pending.isEmpty }
        }
        h.source.refresh()
        #expect(await h.count(API.BoardList.name) == 2)  // none started meanwhile
        // The held reply lands (the old cases), then one more list.
        await h.script.hold(lists: false)
        try await h.idle()
        #expect(h.source.snapshot.cases.count == 3)
        #expect(await h.count(API.BoardList.name) == 3)
        await h.stop()
    }

    /// The reply of a list asked for before the connection went is dropped.
    @Test func aLostConnectionDropsTheReplyOnItsWay() async throws {
        let h = try await Harness().started()
        await h.script.hold(lists: true)
        await h.script.set(cases: [wireCase("1")])
        h.source.refresh()
        try await waitUntil { await h.script.listsHeld == 1 }
        h.source.connectionChanged(connected: false)
        #expect(h.source.phase == .unavailable)
        await h.script.hold(lists: false)
        try await waitUntil { await h.script.listsHeld == 0 }
        try await h.idle()
        #expect(h.source.phase == .unavailable && h.source.snapshot.cases.count == 2)
        await h.stop()
    }

    /// notify.accountsChanged lists the accounts and the board again.
    @Test func accountsChanged() async throws {
        let h = try await Harness().started()
        await h.script.set(accountName: "Office")
        h.source.accountsChanged()
        try await h.idle()
        #expect(h.source.snapshot.accounts.map(\.name) == ["Office"])
        #expect(await h.count(API.AccountList.name) == 2)
        #expect(await h.count(API.BoardList.name) == 2)
        await h.stop()
    }

    /// A write shows at once, in one report, and the daemon's case follows.
    @Test func optimisticWrite() async throws {
        let h = try await Harness().started()
        await h.script.hold(writes: true)
        let before = h.reports
        h.source.setState(.them, of: caseID("1"))
        #expect(h.reports == before + 1)  // at once
        #expect(h.c("1")?.userState == .them && h.c("1")?.version == 1)
        await h.script.hold(writes: false)
        try await h.idle()
        #expect(h.c("1")?.userState == .them && h.c("1")?.version == 2 && h.errors.isEmpty)
        // Done, then back.
        h.source.setDone(true, of: caseID("1"))
        #expect(h.c("1")?.done == true)
        try await h.idle()
        #expect(h.c("1")?.visibility == .done(at: t0) && h.c("1")?.version == 3)
        h.source.setDone(false, of: caseID("1"))
        try await h.idle()
        #expect(h.c("1")?.visibility == .live && h.c("1")?.version == 4)
        // Remind.
        let until = t0.addingTimeInterval(86400)
        h.source.remind(until: until, of: caseID("2"))
        #expect(h.c("2")?.visibility == .snoozed(until: until))
        try await h.idle()
        #expect(h.c("2")?.visibility == .snoozed(until: until) && h.c("2")?.version == 2)
        await h.stop()
    }

    /// A refused write is taken back and said; a list arriving while a
    /// write is under way keeps the write's change.
    @Test func refusedWriteIsReverted() async throws {
        let h = try await Harness().started()
        await h.script.set(writeFailure: RPCError(code: .caseNotFound, message: "internal words"))
        h.source.setDone(true, of: caseID("1"))
        #expect(h.c("1")?.done == true)
        try await h.idle()
        #expect(h.c("1")?.done == false)
        #expect(h.errors == ["Marking the case done failed: the case is no longer on the board."])
        await h.script.set(writeFailure: nil)
        await h.script.hold(writes: true)
        h.source.setState(.info, of: caseID("2"))
        h.source.refresh()
        try await waitUntil { await h.count(API.BoardList.name) == 2 && h.source.snapshot.accounts.count == 1 }
        try await waitUntil { await h.script.writesHeld == 1 }
        #expect(h.c("2")?.userState == .info)  // still laid over the list
        await h.script.hold(writes: false)
        try await h.idle()
        #expect(h.c("2")?.userState == .info && h.c("2")?.version == 2)
        // An unknown case is not written.
        let reports = h.reports
        h.source.setDone(true, of: caseID("nope"))
        #expect(h.source.isIdle && h.reports == reports)
        #expect(await h.count(API.BoardSetDone.name) == 1)
        await h.stop()
    }

    /// A list asked for before a write's answer, and answered after it,
    /// keeps the newer case.
    @Test func aListOlderThanTheWriteKeepsTheWritesCase() async throws {
        let h = try await Harness().started()
        await h.script.hold(lists: true)
        h.source.refresh()
        try await waitUntil { await h.script.listsHeld == 1 }  // it has version 1
        h.source.setDone(true, of: caseID("1"))
        try await waitUntil { h.c("1")?.version == 2 }
        await h.script.hold(lists: false)
        try await h.idle()
        #expect(h.c("1")?.version == 2 && h.c("1")?.done == true)
        await h.stop()
    }

    /// The connection going during a write takes the change back and says
    /// why.
    @Test func theConnectionGoesDuringAWrite() async throws {
        let h = try await Harness().started()
        await h.script.hold(writes: true)
        h.source.setDone(true, of: caseID("1"))
        try await waitUntil { await h.script.writesHeld == 1 }
        await h.fake.closeAll()
        try await waitUntil { !h.errors.isEmpty }
        #expect(h.c("1")?.done == false)
        #expect(h.errors == ["Marking the case done failed: the mail backend is not running."])
        await h.stop()
    }

    @Test func archiveSaysWhatItDid() async throws {
        let h = try await Harness().started()
        h.source.archive(caseID("1"))
        #expect(h.c("1")?.done == true)
        try await h.idle()
        #expect(h.notices == ["Archived 2 messages."])
        #expect(h.c("1")?.canArchive == false)
        await h.stop()
    }

    @Test func discardDraft() async throws {
        let h = try await Harness()
        var c = wireCase("1")
        c.draft = BoardDraft(draftId: "d_1", text: "Hi", updated: t0)
        await h.script.set(cases: [c])
        _ = try await h.started()
        #expect(h.c("1")?.draft == Board.DraftLink(id: "d_1", text: "Hi"))
        h.source.discardDraft(of: caseID("1"))
        #expect(h.c("1")?.draft == nil)
        try await h.idle()
        #expect(h.c("1")?.draft == nil && h.c("1")?.version == 2)
        await h.stop()
    }

    /// The inline editor's Discard deletes the draft it edits: through the
    /// case while the case links it, alone when it no longer does, and a
    /// refusal is thrown (the editor says so), never a toast of the source.
    @Test func discardTheEditorsDraft() async throws {
        let h = try await Harness()
        var c = wireCase("1")
        c.draft = BoardDraft(draftId: "d_1", text: "Hi", updated: t0)
        await h.script.set(cases: [c])
        _ = try await h.started()
        try await h.source.discardDraft("d_1", account: "acc_1", of: caseID("1"))
        #expect(h.c("1")?.draft == nil && h.c("1")?.version == 2)
        #expect(await h.script.count(API.BoardDiscardDraft.name) == 1)
        #expect(await h.script.deleted.isEmpty)
        await h.stop()
    }

    @Test func discardADraftTheCaseNoLongerLinks() async throws {
        let h = try await Harness()
        var c = wireCase("1")
        c.draft = BoardDraft(draftId: "d_2", text: "Newer", updated: t0)
        await h.script.set(cases: [c])
        _ = try await h.started()
        try await h.source.discardDraft("d_1", account: "acc_1", of: caseID("1"))
        #expect(await h.script.deleted == [DraftDeleteParams(accountId: "acc_1", draftId: "d_1")])
        #expect(await h.script.count(API.BoardDiscardDraft.name) == 0)
        #expect(h.c("1")?.draft?.id == "d_2", "the case's own link stays")
        await h.stop()
    }

    @Test func aRefusedDiscardIsThrownAndTheLinkComesBack() async throws {
        let h = try await Harness()
        var c = wireCase("1")
        c.draft = BoardDraft(draftId: "d_1", text: "Hi", updated: t0)
        await h.script.set(cases: [c])
        _ = try await h.started()
        await h.script.set(writeFailure: RPCError(code: .storageError, message: "disk"))
        await #expect(throws: RPCError.self) {
            try await h.source.discardDraft("d_1", account: "acc_1", of: caseID("1"))
        }
        #expect(h.c("1")?.draft?.id == "d_1")
        #expect(h.errors.isEmpty, "the editor says it")
        await h.stop()
    }

    /// Unstar: board.unflag, nothing changed beforehand, then the board
    /// listed again so the rules' new state arrives without waiting for
    /// the notification.
    @Test func unflagListsTheBoardAgain() async throws {
        let h = try await Harness()
        var hot = wireCase("2", .hot)
        hot.ruleReason = .hotFlagged
        await h.script.set(cases: [wireCase("1"), hot])
        _ = try await h.started()
        let lists = await h.count(API.BoardList.name)
        #expect(h.c("2")?.ruleReason == .hotFlagged)
        h.source.unflag(caseID("2"))
        #expect(h.c("2")?.ruleReason == .hotFlagged && h.c("2")?.ruleState == .hot, "nothing optimistic")
        try await waitUntil { await h.count(API.BoardList.name) == lists + 1 }
        try await h.idle()
        #expect(await h.count(API.BoardUnflag.name) == 1)
        #expect(h.c("2")?.ruleReason == .youAddressed && h.c("2")?.ruleState == .you)
        #expect(h.errors.isEmpty)
        // Refused: said, and nothing listed again.
        await h.script.set(writeFailure: RPCError(code: .caseNotFound, message: "x"))
        h.source.unflag(caseID("1"))
        try await waitUntil { !h.errors.isEmpty }
        try await h.idle()
        #expect(h.errors == ["Removing the star failed: the case is no longer on the board."])
        #expect(await h.count(API.BoardList.name) == lists + 1)
        // An unknown case: nothing asked.
        h.source.unflag(caseID("9"))
        try await h.idle()
        #expect(await h.count(API.BoardUnflag.name) == 2)
        await h.stop()
    }

    @Test func commitments() async throws {
        let h = try await Harness()
        await h.script.set(commitments: [commitment()])
        _ = try await h.started()
        #expect(h.source.snapshot.commitments.map(\.state) == [.open])
        h.source.setCommitmentDone(true, of: "k_1")
        #expect(h.source.snapshot.commitments.map(\.state) == [.done])
        try await h.idle()
        #expect(h.source.snapshot.commitments.map(\.state) == [.done] && h.errors.isEmpty)
        // Refused: back to done.
        await h.script.set(writeFailure: RPCError(code: .storageError, message: "x"))
        h.source.setCommitmentDone(false, of: "k_1")
        #expect(h.source.snapshot.commitments.map(\.state) == [.open])
        try await h.idle()
        #expect(h.source.snapshot.commitments.map(\.state) == [.done])
        #expect(h.errors == ["Changing the promise failed: the mail backend could not save it."])
        await h.stop()
    }

    /// A promise ticked off stays ticked while a list asked for before the
    /// answer arrives; a list asked for later has the last word.
    @Test func aListOlderThanTheTickKeepsThePromiseTicked() async throws {
        let h = try await Harness()
        await h.script.set(commitments: [commitment()])
        _ = try await h.started()
        await h.script.hold(lists: true)
        h.source.refresh()
        try await waitUntil { await h.script.listsHeld == 1 }  // it has the promise open
        h.source.setCommitmentDone(true, of: "k_1")
        try await waitUntil { await h.count(API.BoardSetCommitment.name) == 1 }
        await h.script.hold(lists: false)
        try await h.idle()
        #expect(h.source.snapshot.commitments.map(\.state) == [.done])
        // The daemon reopens it (another client): the next list says so.
        await h.script.set(commitments: [commitment(.open)])
        h.source.refresh()
        try await h.idle()
        #expect(h.source.snapshot.commitments.map(\.state) == [.open])
        await h.stop()
    }

    /// The conversation is asked for once per version.
    @Test func messagesAreCachedByVersion() async throws {
        let h = try await Harness().started()
        h.source.loadMessages(of: caseID("1"))
        try await h.idle()
        #expect(h.c("1")?.messages?.map(\.text) == ["version 1"])
        #expect(h.c("1")?.messages?.first?.from == "Ann")
        h.source.loadMessages(of: caseID("1"))
        #expect(h.source.isIdle)
        #expect(await h.count(API.BoardGet.name) == 1)
        // A new version: the old messages stay shown until the new arrive.
        await h.script.set(cases: [wireCase("1", version: 5), wireCase("2", .hot)])
        h.source.refresh()
        try await h.idle()
        #expect(h.c("1")?.version == 5 && h.c("1")?.messages?.map(\.text) == ["version 1"])
        h.source.loadMessages(of: caseID("1"))
        try await h.idle()
        #expect(h.c("1")?.messages?.map(\.text) == ["version 5"])
        #expect(await h.count(API.BoardGet.name) == 2)
        // A failure with nothing loaded says so; asking again retries.
        await h.script.set(getFailure: RPCError(code: .storageError, message: "x"))
        h.source.loadMessages(of: caseID("2"))
        #expect(h.c("2")?.messages == nil && h.c("2")?.messagesFailed == false)  // loading
        try await h.idle()
        #expect(h.c("2")?.messagesFailed == true && h.errors.isEmpty)  // the detail says it; no toast
        await h.script.set(getFailure: nil)
        h.source.loadMessages(of: caseID("2"))
        #expect(h.c("2")?.messagesFailed == false)
        try await h.idle()
        #expect(h.c("2")?.messages != nil)
        await h.stop()
    }

    /// The source keeps the conversations of the most recently opened
    /// cases only.
    @Test func conversationsAreBounded() async throws {
        let h = try await Harness()
        let n = DaemonBoardSource.conversationsKept + 5
        await h.script.set(cases: (1...n).map { wireCase("\($0)") })
        _ = try await h.started()
        for i in 1...n {
            h.source.loadMessages(of: caseID("\(i)"))
            try await h.idle()
        }
        let kept = h.source.snapshot.cases.filter { $0.messages != nil }.map(\.id)
        #expect(kept == (6...n).map { caseID("\($0)") })
        // Opening a kept one again keeps it longest.
        h.source.loadMessages(of: caseID("6"))
        h.source.loadMessages(of: caseID("1"))
        try await h.idle()
        #expect(h.c("6")?.messages != nil && h.c("7")?.messages == nil && h.c("1")?.messages != nil)
        await h.stop()
    }

    /// A conversation whose case left the board while it loaded is not
    /// kept, and nothing waits for it.
    @Test func aConversationOfACaseThatLeftIsDropped() async throws {
        let h = try await Harness().started()
        await h.script.hold(gets: true)
        h.source.loadMessages(of: caseID("2"))
        try await waitUntil { await h.script.getsHeld == 1 }
        await h.script.set(cases: [wireCase("1")])
        h.source.refresh()
        try await h.idle()  // board.get is still held: the loading was pruned
        #expect(h.source.snapshot.cases.count == 1)
        await h.script.set(cases: [wireCase("1"), wireCase("2", .hot)])
        await h.script.hold(gets: false)
        try await waitUntil { await h.script.getsHeld == 0 }
        h.source.refresh()
        try await h.idle()
        #expect(h.c("2")?.messages == nil)
        h.source.loadMessages(of: caseID("2"))
        try await h.idle()
        #expect(h.c("2")?.messages != nil)
        #expect(await h.count(API.BoardGet.name) == 2)
        await h.stop()
    }

    /// The board controller over the daemon's source: the selection moves
    /// on with the optimistic report, and a revert brings the case back.
    @Test func controllerDepartureAndRevert() async throws {
        let h = try await Harness()
        await h.script.set(cases: [wireCase("1"), wireCase("2"), wireCase("3")])
        _ = try await h.started()
        let c = BoardController(source: h.source, now: { t0 }, calendar: BoardFixture.calendar)
        var toasts: [String] = []
        c.onToast = { toasts.append($0) }
        #expect(c.state.selection == caseID("1"))
        try await h.idle()
        #expect(h.c("1")?.messages != nil)  // selecting loaded the conversation
        await h.script.set(writeFailure: RPCError(code: .invalidArgument, message: "no"))
        await h.script.hold(writes: true)
        c.markDone(caseID("1"))
        #expect(c.state.selection == caseID("2"))  // at once, with the optimistic report
        await h.script.hold(writes: false)
        try await waitUntil { !toasts.isEmpty }
        #expect(toasts == ["Marking the case done failed: the board did not accept it."])
        #expect(c.view.sections.flatMap(\.rows).map(\.id) == [caseID("1"), caseID("2"), caseID("3")])
        #expect(c.state.selection == caseID("2"))
        await h.stop()
    }

    /// The controller over the daemon's source asks again for a
    /// conversation that failed once the connection is back.
    @Test func controllerRetriesAFailedConversationAfterAReconnect() async throws {
        let h = try await Harness()
        _ = try await h.started()
        await h.script.set(getFailure: RPCError(code: .storageError, message: "x"))
        let c = BoardController(source: h.source, now: { t0 }, calendar: BoardFixture.calendar)
        try await h.idle()
        #expect(c.view.detail?.messagesRetry == true)
        await h.script.set(getFailure: nil)
        h.source.connectionChanged(connected: false)
        h.source.connectionChanged(connected: true)
        try await h.idle()
        // The hot case is the list's first row, selected.
        #expect(c.view.detail?.id == caseID("2"))
        #expect(c.view.detail?.messagesRetry == false && h.c("2")?.messages != nil)
        #expect(await h.count(API.BoardGet.name) == 2)
        await h.stop()
    }
}
