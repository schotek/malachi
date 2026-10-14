// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board from the daemon (docs/api.md §4.13): board.list for the cases,
// account.list for the accounts' names, board.get for a case's
// conversation when it is selected, and the user's decisions written back
// with board.setState, setDone, remind, archive, discardDraft and
// setCommitment.
//
// It subscribes to nothing itself: the application's notification fan-out
// calls `boardChanged(_:)` on notify.boardChanged, `accountsChanged()` on
// notify.accountsChanged and `connectionChanged(connected:)` when the
// connection comes and goes; `start()` loads the first time. A
// notification is answered after `debounce`, several in that time with
// one board.list. One board.list is on its way at a time: asked for again
// meanwhile, it runs once more after the reply is applied, so a slow list
// under a stream of notifications still lands. A lost connection drops the
// reply on its way (generation counter). While the daemon cannot be asked
// the last snapshot stays, in phase `unavailable`; a list the daemon could
// not answer is phase `failed` and is asked again after a back-off (a few
// seconds, growing to a minute), and a daemon without the board is phase
// `unsupported`.
//
// Writes are optimistic: the change is laid over the daemon's data at
// once (one report: the board controller's selection logic takes it as
// the result of its write), the call follows, and its answer replaces the
// case; a refused write is taken back and `onError` says why. A list asked
// for before a write's answer never undoes it: a case keeps the newer
// version, a promise the answered state until a list asked for later
// arrives. The conversations are cached by case and version, the
// `conversationsKept` most recently used: a case that did not change is
// not asked for again.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself.

import Foundation
import os

@MainActor
public final class DaemonBoardSource: BoardSource {
    public private(set) var snapshot = Board.Snapshot(phase: .loading)
    public var onChange: (@MainActor () -> Void)?
    public var onError: (@MainActor (String) -> Void)?
    public var onNotice: (@MainActor (String) -> Void)?
    public var onArchived: (@MainActor (Board.ArchiveOutcome) -> Void)?
    /// Called after `onChange` with the new snapshot, for the application's
    /// board triage (`BoardTriageController.boardChanged`), which is not the
    /// board controller of this window.
    public var onSnapshot: (@MainActor (Board.Snapshot) -> Void)?

    /// Waits `Duration` (the debounce, a retry's back-off); returns early
    /// when the task is cancelled. The tests put a clock of their own here.
    public typealias Sleep = @Sendable (Duration) async -> Void

    /// How long a notification waits for others before the board is
    /// listed again.
    public nonisolated static let defaultDebounce: Duration = .milliseconds(300)
    /// The conversations kept (`board.get`), the most recently used.
    public nonisolated static let conversationsKept = 50

    /// The back-off before the `attempt`th retry of a failed board.list
    /// (from 1): 2, 4, 8, 16, 32 seconds, then a minute.
    public nonisolated static func retryDelay(_ attempt: Int) -> Duration {
        .seconds(min(60, 1 << min(max(attempt, 1), 6)))
    }

    public nonisolated static let taskSleep: Sleep = { d in
        try? await Task.sleep(for: d)
    }

    private let client: RPCClient
    private let debounce: Duration
    private let sleep: Sleep
    private let retryDelay: @Sendable (Int) -> Duration
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    /// The daemon's data as last answered (board.list, then each write's
    /// and board.get's case), without the writes under way.
    private var base = Base()
    private var accounts: [Board.AccountInfo] = []
    private var phase: Board.Phase = .loading
    /// The writes under way, laid over `base` in order.
    private var overlays: [(token: Int, apply: (inout Board.Snapshot) -> Void)] = []
    private var nextToken = 0
    /// The conversations, by case: the version they are of.
    private var messages: [Board.CaseID: (version: Int64, list: [Board.CaseMessage])] = [:]
    /// The cases of `messages`, the least recently used first.
    private var messageOrder: [Board.CaseID] = []
    /// The version of a case whose conversation is being loaded, or whose
    /// load failed.
    private var loading: [Board.CaseID: Int64] = [:]
    private var failed: [Board.CaseID: Int64] = [:]
    /// Promises as board.setCommitment answered them, with the list
    /// generation of that moment: a list asked for no later is older than
    /// the answer and does not replace them.
    private var commitmentPins: [String: (commitment: Board.Commitment, after: Int)] = [:]
    /// The latest board.list and account.list asked for; older replies
    /// are dropped.
    private var listGeneration = 0
    private var accountGeneration = 0
    /// The generation of the board.list (account.list) on its way.
    private var listInFlight: Int?
    private var accountsInFlight: Int?
    /// Another board.list was asked for while one was on its way.
    private var listAgain = false
    private var debounceTask: Task<Void, Never>?
    /// The next try after a failed board.list, and how many failed in a row.
    private var retryTask: Task<Void, Never>?
    private var failures = 0
    private var stopped = false
    /// Between `start()` and `stop()`: nothing loads on its own outside it.
    private var started = false

    /// The daemon's side of the snapshot.
    struct Base {
        var cases: [Board.Case] = []
        var commitments: [Board.Commitment] = []
        var annotated = false
        var run: Board.Run?
        var triage = Board.Triage()
        var truncated = false
    }

    public init(
        client: RPCClient, debounce: Duration = DaemonBoardSource.defaultDebounce,
        sleep: @escaping Sleep = DaemonBoardSource.taskSleep,
        retryDelay: @escaping @Sendable (Int) -> Duration = DaemonBoardSource.retryDelay
    ) {
        self.client = client
        self.debounce = debounce
        self.sleep = sleep
        self.retryDelay = retryDelay
    }

    /// Writes whose answer has not arrived.
    private var writesInFlight = 0

    /// Nothing is waiting or on its way (a debounce, a retry, board.list,
    /// account.list, a write, board.get of a case on the board): what the
    /// tests wait for before they count calls.
    var isIdle: Bool {
        debounceTask == nil && retryTask == nil && listInFlight == nil && accountsInFlight == nil
            && writesInFlight == 0 && loading.isEmpty
    }

    // MARK: What the application calls

    /// Loads the accounts and the board.
    public func start() {
        stopped = false
        started = true
        refresh()
    }

    /// Stops: no call is made and no reply is taken any more.
    public func stop() {
        stopped = true
        started = false
        cancelTimers()
    }

    /// notify.boardChanged: lists the board again after `debounce`. The
    /// accounts it names are not used: one board.list covers them all.
    public func boardChanged(_ n: BoardChangedNotification = BoardChangedNotification()) {
        guard started, !stopped, debounceTask == nil else { return }
        let debounce = debounce
        let sleep = sleep
        debounceTask = Task { [weak self] in
            await sleep(debounce)
            guard let self, !Task.isCancelled else { return }
            self.debounceTask = nil
            self.loadList()
        }
    }

    /// notify.accountsChanged: the accounts' names and the board again.
    public func accountsChanged() {
        guard started else { return }
        refresh()
    }

    /// The connection came (everything is loaded again) or went (the
    /// snapshot stays, in phase `unavailable`).
    public func connectionChanged(connected: Bool) {
        guard started else { return }
        if connected {
            refresh()
        } else {
            cancelTimers()
            // Replies on the way are dropped: they come from the old
            // connection, or fail.
            listGeneration += 1
            accountGeneration += 1
            listInFlight = nil
            accountsInFlight = nil
            listAgain = false
            failures = 0
            setPhase(.unavailable)
        }
    }

    /// Asks for the accounts and the board now (a retry waiting is
    /// brought forward).
    public func refresh() {
        guard started, !stopped else { return }
        loadAccounts()
        loadList()
    }

    private func cancelTimers() {
        debounceTask?.cancel()
        debounceTask = nil
        retryTask?.cancel()
        retryTask = nil
    }

    // MARK: Loading

    private func loadList() {
        guard started, !stopped else { return }
        guard listInFlight == nil else {
            listAgain = true
            return
        }
        retryTask?.cancel()
        retryTask = nil
        listGeneration += 1
        let generation = listGeneration
        listInFlight = generation
        let client = client
        Task { [weak self] in
            let outcome: Result<BoardListResult, any Error>
            do {
                outcome = .success(try await client.call(API.BoardList.self, BoardListParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            if self.listInFlight == generation {
                self.listInFlight = nil
            }
            guard !self.stopped, generation == self.listGeneration else { return }
            switch outcome {
            case .success(let r):
                self.failures = 0
                self.apply(r, generation: generation)
            case .failure(let err):
                self.log.info("board.list: \(String(describing: err), privacy: .public)")
                self.listFailed(err)
            }
            if self.listAgain {
                self.listAgain = false
                self.loadList()
            }
        }
    }

    /// Why board.list failed decides the phase: not connected (the
    /// reconnect lists again), a daemon without the board, or anything
    /// else, which is asked again after the back-off.
    private func listFailed(_ err: any Error) {
        if let e = err as? RPCClient.ClientError, e == .notConnected || e == .disconnected {
            setPhase(.unavailable)
            return
        }
        if let e = err as? RPCError, e.code == .methodNotFound || e.code == .notImplemented {
            setPhase(.unsupported)
            return
        }
        setPhase(.failed)
        failures += 1
        let delay = retryDelay(failures)
        let sleep = sleep
        retryTask?.cancel()
        retryTask = Task { [weak self] in
            await sleep(delay)
            guard let self, !Task.isCancelled else { return }
            self.retryTask = nil
            self.loadList()
        }
    }

    private func loadAccounts() {
        guard started, !stopped else { return }
        accountGeneration += 1
        let generation = accountGeneration
        accountsInFlight = generation
        let client = client
        Task { [weak self] in
            let res = try? await client.call(API.AccountList.self, EmptyParams())
            guard let self else { return }
            if self.accountsInFlight == generation {
                self.accountsInFlight = nil
            }
            guard !self.stopped, generation == self.accountGeneration, let res else { return }
            self.accounts = res.accounts.filter(\.enabled).map {
                Board.AccountInfo(
                    id: $0.id, name: accountLabel($0), badge: accountHeaderBadge($0),
                    canReply: $0.can(.reply) || $0.can(.comment))
            }
            self.publish()
        }
    }

    /// Takes board.list's answer, asked for as `generation`.
    private func apply(_ r: BoardListResult, generation: Int) {
        let known = Dictionary(base.cases.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        var cases: [Board.Case] = []
        cases.reserveCapacity(r.cases.count)
        for wire in r.cases {
            let c = Self.convert(wire)
            // A list asked for before a write answered can be older than
            // the write's case: the newer version stays.
            if let k = known[c.id], k.version > c.version {
                cases.append(k)
            } else {
                cases.append(c)
            }
        }
        // Likewise a promise board.setCommitment answered after this list
        // was asked for; a list asked for later has the last word.
        var commitments = r.commitments.map(Self.convert)
        for (id, pin) in commitmentPins {
            guard generation <= pin.after else {
                commitmentPins[id] = nil
                continue
            }
            if let i = commitments.firstIndex(where: { $0.id == id }) {
                commitments[i] = pin.commitment
            }
        }
        base = Base(
            cases: cases, commitments: commitments, annotated: r.assistant,
            run: r.triage.lastRun.map(Self.convert),
            triage: Board.Triage(
                queue: r.triage.queue, annotatedToday: r.triage.annotatedTodayAuto, usage24h: r.triage.usage24h),
            truncated: r.truncated ?? false)
        let ids = Set(cases.map(\.id))
        messages = messages.filter { ids.contains($0.key) }
        messageOrder.removeAll { !ids.contains($0) }
        loading = loading.filter { ids.contains($0.key) }
        failed = failed.filter { ids.contains($0.key) }
        phase = !r.enabled ? .off : r.ready ? .ready : .preparing
        publish()
    }

    private func setPhase(_ p: Board.Phase) {
        guard phase != p else { return }
        phase = p
        publish()
    }

    /// Builds the snapshot from the daemon's data, the conversations and
    /// the writes under way, and reports it when it changed.
    func publish() {
        var s = Board.Snapshot(
            accounts: accounts,
            cases: base.cases.map { c in
                var c = c
                if let m = messages[c.id] {
                    c.messages = m.list
                } else if loading[c.id] == nil, failed[c.id] != nil {
                    c.messagesFailed = true
                }
                return c
            },
            commitments: base.commitments, annotated: base.annotated, run: base.run, phase: phase,
            triage: base.triage, truncated: base.truncated)
        for o in overlays {
            o.apply(&s)
        }
        guard s != snapshot else { return }
        snapshot = s
        onChange?()
        onSnapshot?(snapshot)
    }

    /// Replaces the daemon's case with `c` unless a newer one is there.
    func store(_ c: Board.Case) {
        guard let i = base.cases.firstIndex(where: { $0.id == c.id }) else { return }
        guard c.version >= base.cases[i].version else { return }
        base.cases[i] = c
    }

    /// Marks `id`'s conversation as the most recently used and forgets the
    /// least recently used beyond `conversationsKept`.
    private func touch(_ id: Board.CaseID) {
        messageOrder.removeAll { $0 == id }
        messageOrder.append(id)
        while messageOrder.count > Self.conversationsKept {
            messages[messageOrder.removeFirst()] = nil
        }
    }

    // MARK: Conversations

    public func loadMessages(of id: Board.CaseID) {
        guard !stopped, let c = base.cases.first(where: { $0.id == id }) else { return }
        if messages[id]?.version == c.version {
            touch(id)
            return
        }
        if loading[id] == c.version {
            return
        }
        loading[id] = c.version
        failed[id] = nil
        publish()
        let client = client
        let version = c.version
        Task { [weak self] in
            let outcome: Result<BoardGetResult, any Error>
            do {
                outcome = .success(
                    try await client.call(API.BoardGet.self, BoardGetParams(caseId: BoardCaseID(rawValue: id.rawValue))))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.stopped else { return }
            if self.loading[id] == version {
                self.loading[id] = nil
            }
            switch outcome {
            case .success(let r):
                // A case that left the board meanwhile keeps nothing.
                guard self.base.cases.contains(where: { $0.id == id }) else { return }
                let c = Self.convert(r.case)
                self.messages[id] = (c.version, r.messages.map(Self.convert))
                self.touch(id)
                self.store(c)
                self.publish()
                // The case changed meanwhile: its conversation may have too.
                if let now = self.base.cases.first(where: { $0.id == id }), now.version != c.version {
                    self.loadMessages(of: id)
                }
            case .failure(let err):
                self.log.info("board.get: \(String(describing: err), privacy: .public)")
                self.failed[id] = version
                self.publish()
            }
        }
    }
}

// MARK: The user's decisions

extension DaemonBoardSource {
    public func setState(_ state: Board.State?, of id: Board.CaseID) {
        let wire = state.map { BoardState(rawValue: $0.rawValue) }
        write(
            API.BoardSetState.self, BoardSetStateParams(caseId: Self.wireID(id), state: wire), .move, id,
            change: { c in
                c.userState = state
                c.remindedAt = nil
            }, done: { r in r.case })
    }

    public func setDone(_ done: Bool, of id: Board.CaseID) {
        write(
            API.BoardSetDone.self, BoardSetDoneParams(caseId: Self.wireID(id), done: done), done ? .done : .reopen, id,
            change: { c in
                c.done = done
                c.remindedAt = nil
            }, done: { r in r.case })
    }

    public func remind(until: Date?, of id: Board.CaseID) {
        write(
            API.BoardRemind.self, BoardRemindParams(caseId: Self.wireID(id), until: until), .remind, id,
            change: { c in
                c.remindedAt = nil
                if let until {
                    c.visibility = .snoozed(until: until)
                } else if c.visibility.remindAt != nil {
                    c.visibility = .live
                }
            }, done: { r in r.case })
    }

    /// `onArchived` gets what it did, with the moved messages for Undo
    /// (`undoArchive`).
    public func archive(_ id: Board.CaseID) {
        write(
            API.BoardArchive.self, BoardArchiveParams(caseId: Self.wireID(id)), .archive, id,
            change: { c in
                c.visibility = .done(at: nil)
                c.remindedAt = nil
            },
            done: { [weak self] r in
                let moved = r.moved ?? []
                // No moved messages: the daemon cannot take this archive
                // back (an archive folder it does not sync), so the plain
                // toast.
                self?.notifyArchived(
                    Board.ArchiveOutcome(
                        caseID: id, account: r.case.accountId, moved: moved,
                        text: Board.Text.archived(r.archived, noArchive: r.noArchive ?? false),
                        undoLabel: moved.isEmpty ? nil : Board.Text.undo))
                return r.case
            })
    }

    public func discardDraft(of id: Board.CaseID) {
        write(
            API.BoardDiscardDraft.self, BoardDiscardDraftParams(caseId: Self.wireID(id)), .discardDraft, id,
            change: { $0.draft = nil }, done: { r in r.case })
    }

    /// The inline editor's Discard: board.discardDraft while the case links
    /// `draft` (optimistic, taken back when refused), else draft.delete of
    /// that draft alone. The caller reports a failure (no `onError`).
    public func discardDraft(_ draft: DraftID, account: AccountID, of id: Board.CaseID) async throws {
        guard !stopped else { throw CancellationError() }
        guard snapshot.cases.first(where: { $0.id == id })?.draft?.id == draft else {
            _ = try await client.call(API.DraftDelete.self, DraftDeleteParams(accountId: account, draftId: draft))
            return
        }
        let token = lay { s in
            if let i = s.cases.firstIndex(where: { $0.id == id }) {
                s.cases[i].draft = nil
            }
        }
        writesInFlight += 1
        defer { writesInFlight -= 1 }
        do {
            let r = try await client.call(API.BoardDiscardDraft.self, BoardDiscardDraftParams(caseId: Self.wireID(id)))
            store(Self.convert(r.case))
            lift(token)
        } catch {
            log.info("\(API.BoardDiscardDraft.name, privacy: .public): \(String(describing: error), privacy: .public)")
            lift(token)
            throw error
        }
    }

    /// board.unflag. Nothing changes optimistically (the rules decide
    /// what the case becomes); the board is listed again once the stars
    /// are gone, so the case moves without waiting for the notification.
    public func unflag(_ id: Board.CaseID) {
        write(
            API.BoardUnflag.self, BoardUnflagParams(caseId: Self.wireID(id)), .unflag, id,
            change: { $0.remindedAt = nil },
            done: { [weak self] r in
                self?.refresh()
                return r.case
            })
    }

    public func setCommitmentDone(_ done: Bool, of id: String) {
        guard !stopped, snapshot.commitments.contains(where: { $0.id == id }) else { return }
        let state: Board.CommitmentState = done ? .done : .open
        let token = lay { s in
            if let i = s.commitments.firstIndex(where: { $0.id == id }) {
                s.commitments[i].state = state
            }
        }
        let client = client
        let params = BoardSetCommitmentParams(commitmentId: BoardCommitmentID(rawValue: id), done: done)
        writesInFlight += 1
        Task { [weak self] in
            defer { self?.writesInFlight -= 1 }
            do {
                let r = try await client.call(API.BoardSetCommitment.self, params)
                guard let self else { return }
                let k = Self.convert(r.commitment)
                if let i = self.base.commitments.firstIndex(where: { $0.id == k.id }) {
                    self.base.commitments[i] = k
                }
                // A list on its way, or asked for before now, is older.
                self.commitmentPins[k.id] = (k, self.listGeneration)
                self.lift(token)
            } catch {
                guard let self else { return }
                self.lift(token)
                self.onError?(Board.Text.failed(.commitment, error))
            }
        }
    }

    /// Lays `change` over case `id` at once, calls `method`, and then puts
    /// the case the daemon answered (`done`) in place of the change, or
    /// takes the change back and reports the error. An unknown case is
    /// left alone.
    private func write<M: RPCMethod>(
        _ method: M.Type, _ params: M.Params, _ action: Board.Text.Action, _ id: Board.CaseID,
        change: @escaping (inout Board.Case) -> Void, done: @escaping @MainActor (M.Result) -> BoardCase
    ) {
        guard !stopped, snapshot.cases.contains(where: { $0.id == id }) else { return }
        let token = lay { s in
            if let i = s.cases.firstIndex(where: { $0.id == id }) {
                change(&s.cases[i])
            }
        }
        let client = client
        writesInFlight += 1
        Task { [weak self] in
            defer { self?.writesInFlight -= 1 }
            do {
                let r = try await client.call(method, params)
                guard let self else { return }
                self.store(Self.convert(done(r)))
                self.lift(token)
            } catch {
                guard let self else { return }
                self.log.info("\(M.name, privacy: .public): \(String(describing: error), privacy: .public)")
                self.lift(token)
                self.onError?(Board.Text.failed(action, error))
            }
        }
    }

    /// Adds an overlay and reports the snapshot with it.
    private func lay(_ apply: @escaping (inout Board.Snapshot) -> Void) -> Int {
        nextToken += 1
        overlays.append((nextToken, apply))
        publish()
        return nextToken
    }

    /// Removes an overlay and reports what is left.
    private func lift(_ token: Int) {
        overlays.removeAll { $0.token == token }
        publish()
    }
}

// MARK: Undo of Archive

extension DaemonBoardSource: BoardArchiveUndoer {
    /// The calls of `Board.undoArchive`, the moves first, then the case
    /// back on the board (optimistic, as `setDone`; a refusal is a toast).
    /// A move that fails stops the undo (Go `UndoArchive`): the case stays
    /// done, because the mail is still archived, and `onError` says
    /// `Text.undoFailed`.
    public func undoArchive(_ o: Board.ArchiveOutcome) {
        guard !stopped else { return }
        let calls = Board.undoArchive(o.moved, account: o.account, id: o.caseID)
        guard !calls.moves.isEmpty else {
            setDone(false, of: o.caseID)
            return
        }
        let client = client
        let log = log
        writesInFlight += 1
        Task { [weak self] in
            let failed = await withTaskGroup(of: Bool.self) { group in
                for m in calls.moves {
                    group.addTask {
                        do {
                            _ = try await client.call(API.MessageMove.self, m)
                            return false
                        } catch {
                            log.info("message.move back failed: \(String(describing: error), privacy: .public)")
                            return true
                        }
                    }
                }
                var any = false
                for await f in group where f {
                    any = true
                }
                return any
            }
            guard let self else { return }
            self.writesInFlight -= 1
            guard !failed else {
                // The mail is still archived: the case stays done.
                self.onError?(Board.Text.undoFailed)
                return
            }
            self.setDone(false, of: o.caseID)
        }
    }
}

// MARK: From the wire

extension DaemonBoardSource {
    static func wireID(_ id: Board.CaseID) -> BoardCaseID {
        BoardCaseID(rawValue: id.rawValue)
    }

    static func state(_ s: BoardState?) -> Board.State? {
        s.flatMap { Board.State(rawValue: $0.rawValue) }
    }

    static func convert(_ c: BoardCase) -> Board.Case {
        let visibility: Board.Visibility
        switch c.visibility {
        case .done: visibility = .done(at: c.doneAt)
        case .snoozed: visibility = c.remindAt.map { .snoozed(until: $0) } ?? .live
        default: visibility = .live
        }
        let reply: Board.ReplyTarget? =
            c.replyMessageId.rawValue.isEmpty
            ? nil : Board.ReplyTarget(message: c.replyMessageId, folder: c.replyFolderId)
        return Board.Case(
            id: Board.CaseID(rawValue: c.id.rawValue), account: c.accountId, thread: c.threadId,
            person: displayName(c.person), date: c.date, subject: c.subject, snippet: c.snippet, unread: c.unread,
            hasAttachments: c.hasAttachments, messageCount: c.messageCount,
            issue: c.issue.map {
                Board.IssueInfo(key: $0.key, status: $0.status, style: Jira.styleOf($0.statusCategory))
            },
            // A state this client does not know reads as for reading.
            ruleState: state(c.ruleState) ?? .info, ruleReason: c.ruleReason,
            annotation: c.annotation.map(convert), userState: state(c.userState), visibility: visibility,
            // Only a live case is back from a reminder.
            remindedAt: visibility.isLive ? c.remindedAt : nil, reply: reply, latestMessage: c.latestMessageId.rawValue.isEmpty ? nil : c.latestMessageId,
            canArchive: c.canArchive, draft: c.draft.map { Board.DraftLink(id: $0.draftId, text: $0.text) },
            version: c.version)
    }

    static func convert(_ a: BoardAnnotation) -> Board.Annotation {
        Board.Annotation(
            state: state(a.state), title: a.title, summary: a.summary, why: a.why, due: a.due?.at,
            dueQuote: a.due?.quote ?? "", dueMessage: a.due?.messageId, tasks: a.tasks, source: a.source, at: a.at,
            stale: a.stale ?? false)
    }

    static func convert(_ k: BoardCommitment) -> Board.Commitment {
        let state: Board.CommitmentState
        switch k.state {
        case .open: state = .open
        case .done: state = .done
        // Closed, or a state this client does not know: not shown.
        default: state = .closed
        }
        return Board.Commitment(
            id: k.id.rawValue, caseID: Board.CaseID(rawValue: k.caseId.rawValue), text: k.text, quote: k.quote,
            due: k.due, messageID: k.messageId, state: state)
    }

    static func convert(_ m: BoardMessage) -> Board.CaseMessage {
        Board.CaseMessage(
            id: m.id, folder: m.folderId, from: displayName(m.from), date: m.date, text: m.text, mine: m.mine,
            trimmed: m.trimmed ?? false)
    }

    static func convert(_ r: BoardRun) -> Board.Run {
        Board.Run(
            model: r.source, date: r.endedAt ?? r.at, annotated: r.annotated, running: r.endedAt == nil,
            error: r.error?.rawValue, trigger: r.trigger.rawValue, started: r.at)
    }
}
