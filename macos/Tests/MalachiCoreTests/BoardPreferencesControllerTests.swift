// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's preferences and runs against a fake daemon
// (`TriageDaemon`, shared with BoardTriageControllerTests): loading,
// optimistic writes, their order and their revert.

struct TriageTimeout: Error {}

@MainActor
func triageWait(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw TriageTimeout() }
        try await Task.sleep(for: .milliseconds(2))
    }
}

/// The daemon's side of the board's preferences and runs.
actor TriageScript {
    var prefs = BoardPreferences()
    var getFailure: RPCError?
    var setFailure: RPCError?
    var runStartFailure: RPCError?
    private(set) var gets = 0
    private(set) var sets: [BoardPreferences] = []
    private(set) var runStarts: [BoardRunStartParams] = []
    private(set) var runEnds: [BoardRunEndParams] = []
    private var holdingSets = false
    private var heldSets: [CheckedContinuation<Void, Never>] = []

    func set(prefs: BoardPreferences) { self.prefs = prefs }
    func set(getFailure: RPCError?) { self.getFailure = getFailure }
    func set(setFailure: RPCError?) { self.setFailure = setFailure }
    func set(runStartFailure: RPCError?) { self.runStartFailure = runStartFailure }

    /// board.setPreferences replies wait until released.
    func hold(sets: Bool) {
        holdingSets = sets
        if !sets {
            let w = heldSets
            heldSets = []
            for c in w {
                c.resume()
            }
        }
    }

    var waitingSets: Int { heldSets.count }

    private var holdingRunStarts = false
    private var heldRunStarts: [CheckedContinuation<Void, Never>] = []

    /// board.runStart replies wait until released.
    func hold(runStarts: Bool) {
        holdingRunStarts = runStarts
        if !runStarts {
            let w = heldRunStarts
            heldRunStarts = []
            for c in w {
                c.resume()
            }
        }
    }

    var waitingRunStarts: Int { heldRunStarts.count }

    private var holdingRunEnds = false
    private var heldRunEnds: [CheckedContinuation<Void, Never>] = []

    /// board.runEnd replies wait until released (a daemon that does not
    /// answer).
    func hold(runEnds: Bool) {
        holdingRunEnds = runEnds
        if !runEnds {
            let w = heldRunEnds
            heldRunEnds = []
            for c in w {
                c.resume()
            }
        }
    }

    var waitingRunEnds: Int { heldRunEnds.count }

    func get() throws -> Data {
        gets += 1
        if let getFailure { throw getFailure }
        return try JSONCoding.encoder().encode(BoardPreferencesResult(preferences: prefs))
    }

    func setPreferences(_ params: Data) async throws -> Data {
        let p = try JSONCoding.decoder().decode(BoardSetPreferencesParams.self, from: params)
        sets.append(p.preferences)
        if holdingSets {
            await withCheckedContinuation { heldSets.append($0) }
        }
        if let setFailure { throw setFailure }
        prefs = p.preferences
        return try JSONCoding.encoder().encode(BoardSetPreferencesResult(preferences: prefs))
    }

    func runStart(_ params: Data) async throws -> Data {
        let p = try JSONCoding.decoder().decode(BoardRunStartParams.self, from: params)
        runStarts.append(p)
        if holdingRunStarts {
            await withCheckedContinuation { heldRunStarts.append($0) }
        }
        if let runStartFailure { throw runStartFailure }
        return try JSONCoding.encoder().encode(BoardRunStartResult(runId: BoardRunID(rawValue: "run_\(runStarts.count)")))
    }

    func runEnd(_ params: Data) async throws -> Data {
        runEnds.append(try JSONCoding.decoder().decode(BoardRunEndParams.self, from: params))
        if holdingRunEnds {
            await withCheckedContinuation { heldRunEnds.append($0) }
        }
        return Data("{}".utf8)
    }
}

/// A fake daemon answering the preferences and the runs from a script.
@MainActor
final class TriageDaemon {
    let fake: FakeDaemon
    let script = TriageScript()
    let client: RPCClient

    init() async throws {
        fake = try FakeDaemon()
        let s = script
        await fake.on(API.BoardPreferencesGet.name) { _ in try await s.get() }
        await fake.on(API.BoardSetPreferences.name) { p in try await s.setPreferences(p) }
        await fake.on(API.BoardRunStart.name) { p in try await s.runStart(p) }
        await fake.on(API.BoardRunEnd.name) { p in try await s.runEnd(p) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
    }

    func stop() async {
        await script.hold(sets: false)
        await script.hold(runStarts: false)
        await script.hold(runEnds: false)
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct BoardPreferencesControllerTests {
    /// Show the Board, the windows and the triage accounts go through the
    /// same write as every preference; windows the daemon would refuse
    /// never leave (Go TestPreferencesFieldSetters).
    @Test func fieldSetters() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        #expect(await c.loadNow())
        func wait(_ set: (@escaping @MainActor (Bool) -> Void) -> Void) async throws -> Bool {
            var got: [Bool] = []
            set { got.append($0) }
            try await triageWait { !got.isEmpty }
            return got[0]
        }
        #expect(try await wait { c.setEnabled(false, completion: $0) } && c.preferences?.enabled == false)
        let w = BoardWindows(hot: 7, you: 365, them: 1, info: 2)
        let windowsStored = try await wait { done in
            let taken = c.setWindows(w, completion: done)
            #expect(taken)
        }
        #expect(windowsStored && c.preferences?.windows == w)
        let sets = await d.script.sets.count
        for bad in [
            BoardWindows(hot: 0, you: 1, them: 1, info: 1),
            BoardWindows(hot: 1, you: API.Limits.maxBoardWindowDays + 1, them: 1, info: 1),
            BoardWindows(hot: 1, you: 1, them: -3, info: 1), BoardWindows(hot: 1, you: 1, them: 1, info: 0),
        ] {
            #expect(!BoardPreferencesController.validWindows(bad))
            let taken = c.setWindows(bad, completion: { _ in Issue.record("completion called") })
            #expect(!taken)
        }
        #expect(BoardPreferencesController.validWindows(BoardPreferencesController.defaultWindows))
        #expect(BoardPreferencesController.defaultWindows == BoardWindows())
        try await Task.sleep(for: .milliseconds(20))
        #expect(await d.script.sets.count == sets)
        #expect(try await wait { c.setTriageAccounts(["b", "a", "b", ""], completion: $0) })
        #expect(c.preferences?.triageAccounts == ["b", "a"])
        #expect(try await wait { c.setTriageAccounts([], completion: $0) } && c.preferences?.triageAccounts == [])
        await d.stop()
    }

    /// Go TestTriageAccountsSubtitle: "all accounts" only for an empty
    /// list; a list of accounts that are gone or off triages nothing.
    @Test func triageAccountsSubtitle() {
        func acct(_ id: String, _ kind: AccountKind?, _ enabled: Bool) -> Account {
            Account(
                id: AccountID(rawValue: id), config: AccountConfig(name: id, email: id + "@example.org", kind: kind),
                enabled: enabled, state: SyncState(accountId: AccountID(rawValue: id), status: .idle))
        }
        let accounts = [acct("m1", nil, true), acct("j", .jira, true), acct("off", nil, false)]
        typealias C = BoardPreferencesController.TriageAccountsCoverage
        let cases: [(String, [AccountID], [Account], C)] = [
            ("nothing listed", [], accounts, .all),
            ("nothing listed, no accounts", [], [], .all),
            ("one listed", ["m1"], accounts, .some),
            ("issue tracker listed", ["j"], accounts, .some),
            ("only removed accounts", ["gone", "gone2"], accounts, .none),
            ("only a disabled account", ["off"], accounts, .none),
            ("removed and present", ["gone", "m1"], accounts, .some),
            ("listed, no accounts at all", ["m1"], [], .none),
        ]
        for (name, listed, accts, want) in cases {
            #expect(BoardPreferencesController.triageAccountsSubtitle(listed, accts) == want, "\(name)")
        }
        #expect(Board.Text.triageSettingsAccountsNone == "No account is selected, so the triage reads nothing.")
    }

    /// Go TestTriageAccountsChecklist.
    @Test func triageAccountsChecklist() {
        func acct(_ id: String, _ kind: AccountKind?, _ enabled: Bool) -> Account {
            Account(
                id: AccountID(rawValue: id), config: AccountConfig(name: id, email: id + "@example.org", kind: kind),
                enabled: enabled, state: SyncState(accountId: AccountID(rawValue: id), status: .idle))
        }
        let accounts = [acct("m1", nil, true), acct("m2", .graph, true), acct("j", .jira, true), acct("off", nil, false)]
        // Nothing listed: every enabled mail account, no issue tracker,
        // nothing disabled.
        for a in accounts {
            #expect(BoardPreferencesController.triageAccountChecked([], a) == (a.id == "m1" || a.id == "m2"), "\(a.id)")
        }
        #expect(!BoardPreferencesController.triageAccountChecked(["off"], accounts[3]))
        let cases: [(String, [AccountID], AccountID, Bool, [AccountID]?)] = [
            ("uncheck one of all", [], "m2", false, ["m1"]),
            ("check the tracker", [], "j", true, ["m1", "m2", "j"]),
            ("back to all is empty", ["m1"], "m2", true, []),
            ("the last one stays", ["m1"], "m1", false, nil),
            ("unknown account", [], "x", true, nil),
            ("a disabled account cannot be toggled", ["m1"], "off", true, nil),
            ("a disabled listed account stays listed", ["off", "m1"], "m2", true, ["m1", "m2", "off"]),
            ("only the disabled one would be left", ["off", "m1"], "m1", false, nil),
        ]
        for (name, listed, id, on, want) in cases {
            #expect(
                BoardPreferencesController.toggleTriageAccount(listed, accounts: accounts, id: id, on: on) == want, "\(name)")
        }
    }

    @Test func loads() async throws {
        let d = try await TriageDaemon()
        await d.script.set(prefs: BoardPreferences(assistant: true, autoTriage: true, autoTriageMinutes: 60))
        let c = BoardPreferencesController(client: d.client)
        var reports = 0
        let token = c.observe { reports += 1 }
        #expect(c.preferences == nil)
        #expect(await c.loadNow())
        #expect(c.preferences == BoardPreferences(assistant: true, autoTriage: true, autoTriageMinutes: 60))
        #expect(reports == 1)
        // The same answer again reports nothing.
        #expect(await c.loadNow())
        #expect(reports == 1)
        token.cancel()
        await d.stop()
    }

    /// A write shows at once, sends the whole object, and the answer
    /// stands.
    @Test func optimisticWrite() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        #expect(await c.loadNow())
        await d.script.hold(sets: true)
        var results: [Bool] = []
        c.update({ $0.autoTriage = true }, completion: { results.append($0) })
        #expect(c.preferences?.autoTriage == true)
        try await triageWait { await d.script.waitingSets == 1 }
        await d.script.hold(sets: false)
        try await triageWait { results == [true] && c.isIdle }
        #expect(await d.script.sets == [BoardPreferences(autoTriage: true)])
        #expect(c.preferences == BoardPreferences(autoTriage: true))
        await d.stop()
    }

    /// A refused write is taken back and says why.
    @Test func refusedWriteIsTakenBack() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        var errors: [String] = []
        c.onError = { errors.append($0) }
        #expect(await c.loadNow())
        await d.script.set(setFailure: RPCError(code: .invalidArgument, message: "autoTriageMinutes out of range"))
        await d.script.hold(sets: true)
        var reports = 0
        let token = c.observe { reports += 1 }
        let task = Task { await c.update { $0.autoTriageMinutes = 3 } }
        try await triageWait { c.preferences?.autoTriageMinutes == 3 }
        await d.script.hold(sets: false)
        #expect(await task.value == false)
        #expect(c.preferences == BoardPreferences())
        #expect(reports == 2)
        #expect(errors == ["Changing the board’s settings failed: the board did not accept it."])
        token.cancel()
        await d.stop()
    }

    /// Writes go one after another, each with what the earlier left.
    @Test func writesInOrder() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        #expect(await c.loadNow())
        await d.script.hold(sets: true)
        c.update({ $0.autoTriage = true }, completion: nil)
        c.update({ $0.autoTriageDailyCases = 10 }, completion: nil)
        #expect(c.preferences == BoardPreferences(autoTriage: true, autoTriageDailyCases: 10))
        try await triageWait { await d.script.waitingSets == 1 }
        await d.script.hold(sets: false)
        try await triageWait { c.isIdle }
        #expect(await d.script.sets == [
            BoardPreferences(autoTriage: true), BoardPreferences(autoTriage: true, autoTriageDailyCases: 10),
        ])
        #expect(c.preferences == BoardPreferences(autoTriage: true, autoTriageDailyCases: 10))
        await d.stop()
    }

    /// A write before the first load loads first; a load that fails takes
    /// the write back.
    @Test func writeBeforeLoad() async throws {
        let d = try await TriageDaemon()
        await d.script.set(prefs: BoardPreferences(windows: BoardWindows(hot: 7)))
        let c = BoardPreferencesController(client: d.client)
        #expect(await c.update { $0.assistant = true })
        #expect(await d.script.gets == 1)
        #expect(await d.script.sets == [BoardPreferences(assistant: true, windows: BoardWindows(hot: 7))])
        #expect(c.preferences == BoardPreferences(assistant: true, windows: BoardWindows(hot: 7)))

        let other = BoardPreferencesController(client: d.client)
        var errors: [String] = []
        other.onError = { errors.append($0) }
        await d.script.set(getFailure: RPCError(code: .storageError, message: "disk"))
        #expect(await other.update { $0.assistant = false } == false)
        #expect(other.preferences == nil)
        #expect(errors == ["Changing the board’s settings failed: the mail backend could not save it."])
        #expect(await d.script.sets.count == 1)
        await d.stop()
    }

    /// A write reads the preferences afresh and lays only its own change
    /// over them: what another client changed since the last load stays.
    @Test func writeKeepsAnotherClientsChange() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        #expect(await c.loadNow())
        // Another client changes the interval after this one loaded.
        await d.script.set(prefs: BoardPreferences(autoTriageMinutes: 60))
        #expect(await c.update { $0.autoTriage = true })
        #expect(await d.script.sets == [BoardPreferences(autoTriage: true, autoTriageMinutes: 60)])
        #expect(c.preferences == BoardPreferences(autoTriage: true, autoTriageMinutes: 60))
        // A fresh read that fails: the write goes on with the last one.
        await d.script.set(getFailure: RPCError(code: .storageError, message: "disk"))
        #expect(await c.update { $0.autoTriageDailyCases = 20 })
        #expect(await d.script.sets.last == BoardPreferences(autoTriage: true, autoTriageMinutes: 60, autoTriageDailyCases: 20))
        await d.stop()
    }

    /// A quiet write that fails says nothing through `onError`; the result
    /// still tells.
    @Test func quietWrite() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        var errors: [String] = []
        c.onError = { errors.append($0) }
        #expect(await c.loadNow())
        await d.script.set(setFailure: RPCError(code: .storageError, message: "disk"))
        #expect(await c.update(quiet: true) { $0.assistant = true } == false)
        #expect(errors.isEmpty)
        #expect(await c.update { $0.assistant = true } == false)
        #expect(errors.count == 1)
        await d.stop()
    }

    /// A write still waiting when the controller goes away completes, with
    /// a failure, so an `await update` never hangs.
    @Test func writeCompletesWhenTheControllerGoes() async throws {
        let d = try await TriageDaemon()
        var c: BoardPreferencesController? = BoardPreferencesController(client: d.client)
        #expect(await c!.loadNow())
        await d.script.hold(sets: true)
        var results: [Bool] = []
        c!.update({ $0.autoTriage = true }, completion: { results.append($0) })
        c!.update({ $0.autoTriageDailyCases = 10 }, completion: { results.append($0) })
        try await triageWait { await d.script.waitingSets == 1 }
        weak var gone = c
        c = nil
        await d.script.hold(sets: false)
        try await triageWait { results.count == 2 }
        #expect(results == [true, false])
        #expect(gone == nil)
        await d.stop()
    }

    /// `lastLoadFailed` follows the loads; `observeLoaded` reports each
    /// answer.
    @Test func loadState() async throws {
        let d = try await TriageDaemon()
        let c = BoardPreferencesController(client: d.client)
        var loads = 0
        let token = c.observeLoaded { loads += 1 }
        await d.script.set(getFailure: RPCError(code: .storageError, message: "disk"))
        #expect(await c.loadNow() == false)
        #expect(c.lastLoadFailed && loads == 0 && c.stored == nil)
        await d.script.set(getFailure: nil)
        #expect(await c.loadNow())
        #expect(!c.lastLoadFailed && loads == 1 && c.stored == BoardPreferences())
        token.cancel()
        await d.stop()
    }
}
