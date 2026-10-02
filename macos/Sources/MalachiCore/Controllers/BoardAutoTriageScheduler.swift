// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// What the automatic triage's schedule reads and starts: the
/// application's `BoardTriageController` (the tests put a stand-in here).
@MainActor
public protocol BoardAutoTriageTarget: AnyObject {
    /// The rule's inputs as the target knows them: the preferences, the
    /// availability, the sign-in, the consents, its run, the board's queue
    /// and counts, and the daemon's last automatic run as `lastAttempt`.
    /// The schedule sets the trigger, the clock, its own last attempt and
    /// the failures.
    var autoTriageInputs: Board.AutoTriage.Inputs { get }
    /// Changes whenever the board reported new triage data.
    var boardRevision: Int { get }
    /// The run that ended last: its trigger and failure (nil: success).
    var lastEnded: (trigger: Board.TriageTrigger, failure: Board.TriageFailure?)? { get }
    /// Starts an automatic run of at most `limit` cases; false when one is
    /// active.
    @discardableResult func start(_ trigger: Board.TriageTrigger, limit: Int?) -> Bool
    /// Why automatic triage pauses, for the status strip.
    func setAutoPause(_ p: Board.AutoTriagePause?)
    /// Asks whether Claude Code is signed in afresh (after a sign-in
    /// elsewhere, such as in a terminal).
    func recheckSignIn()
    func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken
    func observeEnded(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken
}

/// Runs the board's triage on its own (`Board.AutoTriage.decide`) while the
/// application runs, once for the whole application.
///
/// It decides again whenever its target reports a change: at once for a
/// change of the preferences, the consents, the sign-in, the availability
/// or the run; after `debounce` (a minute) for new board data, so that a
/// burst of new mail gives one run; at the time a `wait` names; and every
/// `heartbeat` while automatic triage is on (asking the sign-in afresh while
/// Claude Code is signed out). A `run` starts the target's automatic run.
///
/// It remembers, in memory only, when it last started an automatic run and
/// how many failed in a row (the back-off): a success or a manual run resets
/// that. A cancelled run, an empty queue, a declined consent or the
/// assistant off are no failures. After a restart the daemon's last
/// automatic run stands in for the last attempt; nothing else persists.
@MainActor
public final class BoardAutoTriageScheduler {
    public typealias Sleep = @Sendable (Duration) async -> Void

    /// How long new board data waits for more before the rule is asked.
    public nonisolated static let defaultDebounce: Duration = .seconds(60)
    /// The longest time between two decisions while automatic triage is on.
    public nonisolated static let defaultHeartbeat: Duration = .seconds(30 * 60)

    public nonisolated static let taskSleep: Sleep = { d in
        try? await Task.sleep(for: d)
    }

    /// The last decision (nil before the first).
    public private(set) var decision: Board.AutoTriage.Decision?
    /// When it decides again (a wait, or the heartbeat); nil when nothing
    /// is scheduled.
    public private(set) var wakeAt: Date?
    /// Automatic runs that failed in a row.
    public private(set) var failures = 0
    /// When this schedule last started an automatic run.
    public private(set) var lastAttempt: Date?

    private weak var target: (any BoardAutoTriageTarget)?
    private let sleep: Sleep
    private let now: @MainActor () -> Date
    private let calendar: Calendar
    private let debounce: Duration
    private let heartbeat: Duration
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")
    private var tokens: [BoardObserverToken] = []
    private var seenRevision: Int?
    private var lastFailure: Board.TriageFailure?
    private var debounceTask: Task<Void, Never>?
    private var wakeTask: Task<Void, Never>?
    private var started = false
    private var deciding = false

    public init(
        target: any BoardAutoTriageTarget, sleep: @escaping Sleep = BoardAutoTriageScheduler.taskSleep,
        now: @escaping @MainActor () -> Date = { Date() }, calendar: Calendar = .current,
        debounce: Duration = BoardAutoTriageScheduler.defaultDebounce,
        heartbeat: Duration = BoardAutoTriageScheduler.defaultHeartbeat
    ) {
        self.target = target
        self.sleep = sleep
        self.now = now
        self.calendar = calendar
        self.debounce = debounce
        self.heartbeat = heartbeat
    }

    /// Nothing is waiting for the debounce (the tests read it).
    var debouncing: Bool { debounceTask != nil }
    /// The decisions made so far (the tests wait for the next).
    private(set) var evaluations = 0
    /// The sleeps that are live: the debounce and the wake (the tests wait
    /// for them before they move the clock).
    var liveSleeps: Int { (debounceTask == nil ? 0 : 1) + (wakeTask == nil ? 0 : 1) }

    /// Starts listening and decides once.
    public func start() {
        guard !started, let target else { return }
        started = true
        seenRevision = target.boardRevision
        tokens = [
            target.observe { [weak self] in self?.changed() },
            target.observeEnded { [weak self] in self?.ended() },
        ]
        evaluate()
    }

    /// Stops: nothing is decided or started any more.
    public func stop() {
        started = false
        for t in tokens {
            t.cancel()
        }
        tokens = []
        debounceTask?.cancel()
        debounceTask = nil
        cancelWake()
        target?.setAutoPause(nil)
    }

    // MARK: Events

    private func changed() {
        guard started, let target, !deciding else { return }
        if target.boardRevision != seenRevision {
            seenRevision = target.boardRevision
            guard debounceTask == nil else { return }
            let sleep = sleep
            let debounce = debounce
            debounceTask = Task { [weak self] in
                await sleep(debounce)
                guard let self, !Task.isCancelled else { return }
                self.debounceTask = nil
                self.evaluate()
            }
            return
        }
        evaluate()
    }

    private func ended() {
        guard started, let e = target?.lastEnded else { return }
        if e.trigger == .manual || e.failure == nil {
            failures = 0
            lastFailure = nil
        } else if let f = e.failure, Self.countsAsFailure(f) {
            failures += 1
            lastFailure = f
        }
        evaluate()
    }

    /// Whether a failed automatic run makes the next one wait longer.
    static func countsAsFailure(_ f: Board.TriageFailure) -> Bool {
        switch f {
        case .notSignedIn, .notFound, .toolsMissing, .timeout, .backend, .stopped, .notesRefused, .noProgress:
            return true
        case .cancelled, .declined, .assistantOff, .nothingToDo: return false
        }
    }

    // MARK: Deciding

    /// Asks the rule now and acts on it.
    public func evaluate() {
        guard started, let target, !deciding else { return }
        deciding = true
        defer { deciding = false }
        evaluations += 1
        var i = target.autoTriageInputs
        i.trigger = .automatic
        i.now = now()
        if let mine = lastAttempt, i.lastAttempt.map({ $0 < mine }) ?? true {
            i.lastAttempt = mine
        }
        i.failures = failures
        let d = Board.AutoTriage.decide(i, calendar: calendar)
        decision = d
        target.setAutoPause(pause(d, i))
        switch d {
        case .run(let limit):
            lastAttempt = i.now
            cancelWake()
            if target.start(.automatic, limit: limit) {
                log.info("board triage: automatic run of at most \(limit, privacy: .public) cases")
            }
        case .wait(let until):
            schedule(min(until, i.now.addingTimeInterval(Self.seconds(heartbeat))))
        case .off(let reason):
            if reason == .switchedOff {
                cancelWake()
            } else {
                schedule(i.now.addingTimeInterval(Self.seconds(heartbeat)))
            }
        }
    }

    /// The pause the status strip shows for decision `d`.
    private func pause(_ d: Board.AutoTriage.Decision, _ i: Board.AutoTriage.Inputs) -> Board.AutoTriagePause? {
        switch d {
        case .off(.signedOut): return .signedOut
        case .off(.unavailable): return .unavailable
        case .off(.noConsent): return .noConsent
        case .wait(let until):
            if failures > 0, let f = lastFailure {
                return .failed(f, until: until)
            }
            return nil
        default:
            return nil
        }
    }

    /// Decides again at `at` (a wait, or the heartbeat).
    private func schedule(_ at: Date) {
        guard wakeAt != at || wakeTask == nil else { return }
        cancelWake()
        wakeAt = at
        let delay = max(0, at.timeIntervalSince(now()))
        let sleep = sleep
        wakeTask = Task { [weak self] in
            await sleep(.milliseconds(Int64(delay * 1000)))
            guard let self, !Task.isCancelled else { return }
            self.wakeTask = nil
            self.wakeAt = nil
            if case .off(.signedOut) = self.decision {
                self.target?.recheckSignIn()
            }
            self.evaluate()
        }
    }

    private func cancelWake() {
        wakeTask?.cancel()
        wakeTask = nil
        wakeAt = nil
    }

    private static func seconds(_ d: Duration) -> TimeInterval {
        let c = d.components
        return TimeInterval(c.seconds) + TimeInterval(c.attoseconds) / 1e18
    }
}

// MARK: The application's target

extension BoardTriageController: BoardAutoTriageTarget {
    public var autoTriageInputs: Board.AutoTriage.Inputs {
        let p = preferences.preferences
        let lastAuto = board.lastRun.flatMap { $0.trigger == BoardTrigger.auto.rawValue ? $0.started : nil }
        return Board.AutoTriage.Inputs(
            trigger: .automatic, enabled: p?.autoTriage ?? false, available: canRun, signedIn: signedIn,
            consent: consentGiven, running: state.isActive,
            queue: board.known && board.assistantOn ? board.queue : 0, annotatedToday: board.annotatedToday,
            countedAt: board.countedAt,
            dailyCap: p?.autoTriageDailyCases ?? API.Limits.defaultBoardAutoTriageDailyCases,
            minutes: p?.autoTriageMinutes ?? API.Limits.defaultBoardAutoTriageMinutes, lastAttempt: lastAuto,
            failures: 0, now: Date())
    }

    public func recheckSignIn() {
        locator.refresh()
        checkSignIn()
    }
}
