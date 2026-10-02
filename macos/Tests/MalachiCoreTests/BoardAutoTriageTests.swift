// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// Automatic triage: the pure rule (`Board.AutoTriage.decide`) as a table,
// and the schedule (`BoardAutoTriageScheduler`) against a stand-in target
// with a clock the test sets and sleeps the test ends (`TriageClock`), so
// nothing depends on how fast the machine is.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(10), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(2))
    }
}

/// The schedule's sleeps: each waits until the test fires the clock; a
/// cancelled sleep ends at once and is no longer counted.
actor TriageClock {
    private var waiting: [(id: Int, c: CheckedContinuation<Void, Never>)] = []
    private var next = 0
    private var cancelled: Set<Int> = []

    func sleep(_ d: Duration) async {
        next += 1
        let id = next
        await withTaskCancellationHandler {
            await withCheckedContinuation { c in
                if cancelled.contains(id) || Task.isCancelled {
                    c.resume()
                } else {
                    waiting.append((id, c))
                }
            }
        } onCancel: {
            Task { await self.cancel(id) }
        }
    }

    private func cancel(_ id: Int) {
        cancelled.insert(id)
        if let i = waiting.firstIndex(where: { $0.id == id }) {
            waiting.remove(at: i).c.resume()
        }
    }

    var count: Int { waiting.count }

    func fire() {
        let w = waiting
        waiting = []
        for s in w {
            s.c.resume()
        }
    }
}

private var utc: Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "UTC")!
    return c
}

/// 2026-10-01 10:00:00 UTC.
private let t0 = Date(timeIntervalSince1970: 1_790_848_800)

private func minutes(_ m: Double) -> TimeInterval { m * 60 }

/// The schedule's target: inputs the test sets, the runs it started.
@MainActor
private final class FakeTarget: BoardAutoTriageTarget {
    var inputs = Board.AutoTriage.Inputs(now: t0)
    var boardRevision = 0
    var lastEnded: (trigger: Board.TriageTrigger, failure: Board.TriageFailure?)?
    var starts: [Int?] = []
    var pause: Board.AutoTriagePause?
    var rechecks = 0
    private let changes = BoardObservers()
    private let ends = BoardObservers()

    var autoTriageInputs: Board.AutoTriage.Inputs { inputs }

    func start(_ trigger: Board.TriageTrigger, limit: Int?) -> Bool {
        guard !inputs.running else { return false }
        starts.append(limit)
        inputs.running = true
        changes.notify()
        return true
    }

    func setAutoPause(_ p: Board.AutoTriagePause?) {
        pause = p
    }

    func recheckSignIn() {
        rechecks += 1
    }

    func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken { changes.add(f) }
    func observeEnded(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken { ends.add(f) }

    /// Something but the board changed.
    func change(_ f: (inout Board.AutoTriage.Inputs) -> Void) {
        f(&inputs)
        changes.notify()
    }

    /// New board data.
    func board(queue: Int, annotatedToday: Int? = nil) {
        inputs.queue = queue
        if let annotatedToday {
            inputs.annotatedToday = annotatedToday
        }
        boardRevision += 1
        changes.notify()
    }

    /// The run ended, reported as the controller does: the end first.
    func end(_ trigger: Board.TriageTrigger = .automatic, _ failure: Board.TriageFailure? = nil) {
        lastEnded = (trigger, failure)
        ends.notify()
        inputs.running = false
        changes.notify()
    }
}

@MainActor
private final class Harness {
    let target = FakeTarget()
    let clock = TriageClock()
    var now = t0
    var scheduler: BoardAutoTriageScheduler!

    init(debounce: Duration = .seconds(60)) {
        let clock = clock
        scheduler = BoardAutoTriageScheduler(
            target: target, sleep: { await clock.sleep($0) }, now: { [unowned self] in self.now }, calendar: utc,
            debounce: debounce)
    }

    /// Moves the clock on and ends every sleep, then waits for the schedule
    /// to have decided again (when anything slept).
    func advance(_ seconds: TimeInterval) async throws {
        let live = scheduler.liveSleeps
        try await waitUntil { await self.clock.count == live }
        let before = scheduler.evaluations
        now = now.addingTimeInterval(seconds)
        await clock.fire()
        if live > 0 {
            try await waitUntil { self.scheduler.evaluations > before }
        }
    }

    func stop() async {
        scheduler.stop()
        await clock.fire()
    }
}

@MainActor
@Suite(.serialized) struct BoardAutoTriageTests {
    // MARK: The rule

    @Test func decideTable() {
        typealias I = Board.AutoTriage.Inputs
        let next = t0.addingTimeInterval(14 * 3600) // 2026-10-02 00:00 UTC
        let rows: [(String, I, Board.AutoTriage.Decision)] = [
            ("all set, first run", I(now: t0), .run(limit: 40)),
            ("switched off", I(enabled: false, now: t0), .off(.switchedOff)),
            ("assistant cannot run", I(available: false, now: t0), .off(.unavailable)),
            ("signed out", I(signedIn: false, now: t0), .off(.signedOut)),
            ("sign-in not known counts as signed in", I(signedIn: nil, now: t0), .run(limit: 40)),
            ("no consent", I(consent: false, now: t0), .off(.noConsent)),
            ("a run under way", I(running: true, now: t0), .off(.running)),
            ("empty queue", I(queue: 0, now: t0), .off(.emptyQueue)),
            ("no daily cases", I(dailyCap: 0, now: t0), .off(.noDailyCases)),
            ("the cap is the limit", I(annotatedToday: 50, dailyCap: 60, now: t0), .run(limit: 10)),
            ("the batch is the limit", I(annotatedToday: 0, dailyCap: 1000, now: t0), .run(limit: 40)),
            ("cap used up: tomorrow", I(annotatedToday: 60, dailyCap: 60, now: t0), .wait(until: next)),
            ("cap counted yesterday is 0",
             I(annotatedToday: 60, countedAt: t0.addingTimeInterval(-86400), dailyCap: 60, now: t0), .run(limit: 40)),
            ("interval not over",
             I(minutes: 30, lastAttempt: t0.addingTimeInterval(-minutes(10)), now: t0),
             .wait(until: t0.addingTimeInterval(minutes(20)))),
            ("interval over", I(minutes: 30, lastAttempt: t0.addingTimeInterval(-minutes(30)), now: t0), .run(limit: 40)),
            ("one failure doubles it",
             I(minutes: 30, lastAttempt: t0.addingTimeInterval(-minutes(30)), failures: 1, now: t0),
             .wait(until: t0.addingTimeInterval(minutes(30)))),
            ("three failures: 4 hours",
             I(minutes: 30, lastAttempt: t0, failures: 3, now: t0), .wait(until: t0.addingTimeInterval(minutes(240)))),
            ("the back-off stops at a day",
             I(minutes: 600, lastAttempt: t0, failures: 5, now: t0), .wait(until: t0.addingTimeInterval(86400))),
            ("the switch before everything", I(enabled: false, available: false, signedIn: false, now: t0), .off(.switchedOff)),
            ("the cap before the interval",
             I(annotatedToday: 60, dailyCap: 60, lastAttempt: t0, now: t0), .wait(until: next)),
            ("manual ignores the switch, interval, cap, back-off and queue",
             I(trigger: .manual, enabled: false, queue: 0, annotatedToday: 60, dailyCap: 60, lastAttempt: t0, failures: 4,
               now: t0), .run(limit: 40)),
            ("manual without consent runs (it asks)", I(trigger: .manual, consent: false, now: t0), .run(limit: 40)),
            ("manual: not while one runs", I(trigger: .manual, running: true, now: t0), .off(.running)),
            ("manual: not signed out", I(trigger: .manual, signedIn: false, now: t0), .off(.signedOut)),
            ("manual: not unavailable", I(trigger: .manual, available: false, now: t0), .off(.unavailable)),
        ]
        for (name, i, want) in rows {
            #expect(Board.AutoTriage.decide(i, calendar: utc) == want, "\(name)")
        }
    }

    @Test func interval() {
        #expect(Board.AutoTriage.interval(minutes: 30, failures: 0) == 1800)
        #expect(Board.AutoTriage.interval(minutes: 30, failures: 1) == 3600)
        #expect(Board.AutoTriage.interval(minutes: 30, failures: 2) == 7200)
        #expect(Board.AutoTriage.interval(minutes: 30, failures: 100) == 86400)
        #expect(Board.AutoTriage.interval(minutes: 1440, failures: 0) == 86400)
        // Out of range minutes are taken as the nearest allowed.
        #expect(Board.AutoTriage.interval(minutes: 1, failures: 0) == 300)
        #expect(Board.AutoTriage.interval(minutes: 99999, failures: 0) == 86400)
        #expect(Board.AutoTriage.interval(minutes: 30, failures: -3) == 1800)
    }

    // MARK: The schedule

    /// New board data waits a minute for more: a burst gives one run.
    @Test func debounce() async throws {
        let h = Harness()
        h.target.inputs.queue = 0
        h.scheduler.start()
        #expect(h.scheduler.decision == .off(.emptyQueue))
        h.target.board(queue: 1)
        h.target.board(queue: 2)
        h.target.board(queue: 3)
        #expect(h.scheduler.debouncing && h.target.starts.isEmpty)
        try await h.advance(60)
        try await waitUntil { h.target.starts.count == 1 }
        #expect(h.target.starts == [40])
        #expect(h.scheduler.lastAttempt == h.now)
        #expect(!h.scheduler.debouncing)
        // While it runs nothing else starts.
        h.target.board(queue: 4)
        try await h.advance(60)
        #expect(h.scheduler.decision == .off(.running) && h.target.starts.count == 1)
        await h.stop()
    }

    /// A change that is not the board's decides at once.
    @Test func preferencesDecideAtOnce() async throws {
        let h = Harness()
        h.target.inputs.enabled = false
        h.scheduler.start()
        #expect(h.scheduler.decision == .off(.switchedOff) && h.scheduler.wakeAt == nil)
        h.target.change { $0.enabled = true }
        #expect(h.target.starts == [40])
        await h.stop()
    }

    /// After a run the next waits for the interval from its start.
    @Test func intervalBetweenRuns() async throws {
        let h = Harness()
        h.scheduler.start()
        #expect(h.target.starts == [40])
        try await h.advance(minutes(5))
        h.target.end()
        #expect(h.scheduler.decision == .wait(until: t0.addingTimeInterval(minutes(30))))
        #expect(h.scheduler.wakeAt == t0.addingTimeInterval(minutes(30)))
        #expect(h.target.pause == nil)
        try await h.advance(minutes(25))
        try await waitUntil { h.target.starts.count == 2 }
        await h.stop()
    }

    /// Failed automatic runs double the wait (and say why on the strip);
    /// a success resets it, and so does a manual run.
    @Test func backOff() async throws {
        let h = Harness()
        h.scheduler.start()
        #expect(h.target.starts.count == 1)
        h.target.end(.automatic, .timeout)
        #expect(h.scheduler.failures == 1)
        let until1 = t0.addingTimeInterval(minutes(60))
        #expect(h.scheduler.decision == .wait(until: until1))
        #expect(h.target.pause == .failed(.timeout, until: until1))
        try await h.advance(minutes(60))
        try await waitUntil { h.target.starts.count == 2 }
        let second = h.now
        h.target.end(.automatic, .stopped)
        #expect(h.scheduler.failures == 2)
        #expect(h.scheduler.decision == .wait(until: second.addingTimeInterval(minutes(120))))
        #expect(h.target.pause == .failed(.stopped, until: second.addingTimeInterval(minutes(120))))
        // A manual run resets it; the interval from the last automatic
        // attempt still holds.
        h.target.change { $0.running = true }
        h.target.end(.manual, nil)
        #expect(h.scheduler.failures == 0 && h.target.pause == nil)
        #expect(h.scheduler.decision == .wait(until: second.addingTimeInterval(minutes(30))))
        // A cancelled run is no failure.
        try await h.advance(minutes(30))
        try await waitUntil { h.target.starts.count == 3 }
        h.target.end(.automatic, .cancelled)
        #expect(h.scheduler.failures == 0)
        // Nor are an empty queue or the assistant off.
        #expect(!BoardAutoTriageScheduler.countsAsFailure(.nothingToDo))
        #expect(!BoardAutoTriageScheduler.countsAsFailure(.assistantOff))
        #expect(!BoardAutoTriageScheduler.countsAsFailure(.declined))
        #expect(BoardAutoTriageScheduler.countsAsFailure(.notSignedIn))
        #expect(BoardAutoTriageScheduler.countsAsFailure(.backend))
        // An automatic run that added no note, or whose notes were all
        // refused, is one: it must not repeat at every interval.
        #expect(BoardAutoTriageScheduler.countsAsFailure(.noProgress))
        #expect(BoardAutoTriageScheduler.countsAsFailure(.notesRefused))
        // A success resets after failures too.
        try await h.advance(minutes(30))
        try await waitUntil { h.target.starts.count == 4 }
        h.target.end(.automatic, .notFound)
        #expect(h.scheduler.failures == 1)
        try await h.advance(minutes(60))
        try await waitUntil { h.target.starts.count == 5 }
        h.target.end(.automatic, nil)
        #expect(h.scheduler.failures == 0 && h.target.pause == nil)
        await h.stop()
    }

    /// The day's cap used up: the next try is at midnight, when the count
    /// read yesterday no longer holds.
    @Test func dailyCap() async throws {
        let h = Harness()
        h.target.inputs.annotatedToday = 60
        h.target.inputs.countedAt = t0
        h.scheduler.start()
        let midnight = t0.addingTimeInterval(14 * 3600)
        #expect(h.scheduler.decision == .wait(until: midnight))
        // The heartbeat comes first and decides the same.
        #expect(h.scheduler.wakeAt == t0.addingTimeInterval(minutes(30)))
        try await h.advance(minutes(30))
        #expect(h.scheduler.decision == .wait(until: midnight) && h.target.starts.isEmpty)
        try await h.advance(midnight.timeIntervalSince(h.now))
        try await waitUntil { h.target.starts == [40] }
        await h.stop()
    }

    /// Signed out: paused, and the heartbeat asks the sign-in afresh.
    @Test func signedOut() async throws {
        let h = Harness()
        h.target.inputs.signedIn = false
        h.scheduler.start()
        #expect(h.scheduler.decision == .off(.signedOut) && h.target.pause == .signedOut)
        #expect(h.scheduler.wakeAt == t0.addingTimeInterval(minutes(30)))
        try await h.advance(minutes(30))
        #expect(h.target.rechecks == 1)
        h.target.change { $0.signedIn = true }
        #expect(h.target.starts == [40] && h.target.pause == nil)
        // The other pauses.
        h.target.end()
        h.target.change { $0.consent = false }
        #expect(h.target.pause == .noConsent)
        h.target.change { $0.available = false }
        #expect(h.target.pause == .unavailable)
        // Switched off: no pause, nothing scheduled.
        h.target.change { $0.enabled = false }
        #expect(h.target.pause == nil && h.scheduler.wakeAt == nil)
        await h.stop()
    }

    /// The daemon's last automatic run stands in for the last attempt
    /// after a restart; this schedule's own attempt counts when newer.
    @Test func lastAttemptFromTheDaemon() async throws {
        let h = Harness()
        h.target.inputs.lastAttempt = t0.addingTimeInterval(-minutes(10))
        h.scheduler.start()
        #expect(h.scheduler.decision == .wait(until: t0.addingTimeInterval(minutes(20))))
        await h.stop()
    }

    /// Stopped: nothing is decided any more.
    @Test func stop() async throws {
        let h = Harness()
        h.target.inputs.queue = 0
        h.scheduler.start()
        h.scheduler.stop()
        h.target.board(queue: 3)
        h.target.change { $0.enabled = true }
        try await h.advance(minutes(60))
        #expect(h.target.starts.isEmpty && !h.scheduler.debouncing && h.scheduler.wakeAt == nil)
        await h.clock.fire()
    }
}
