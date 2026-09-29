// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// Claude Desktop around a change of "Register with Claude": a fake Claude
// Desktop (running or not, quitting or not), fake writes that log in the
// same place, so the order of quit, write and launch shows. Nothing here
// touches the real Claude Desktop or its configuration. No Go counterpart:
// GTK has no equivalent yet.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(5))
    }
}

/// What the bridge reports: Claude Desktop (present as asked) registered
/// as `desktop`, Claude Code present and not registered.
private func status(desktop: Bool, present: Bool = true) -> MCPStatus {
    MCPStatus(command: "/Applications/Malachi Mail.app/Contents/MacOS/malachi-mcp", clients: [
        MCPClient(id: "claude-desktop", name: "Claude Desktop", present: present, registered: desktop),
        MCPClient(id: "claude-code", name: "Claude Code", present: true, registered: false),
    ])
}

/// A stand-in Claude Desktop and the writes, logging in one place.
@MainActor
private final class Fake {
    var running: Bool
    /// Whether a quit request makes it quit (false: the wait times out).
    var quits = true
    /// Keeps the quit waiting until cleared.
    var holdQuit = false
    /// Runs inside quit once it quit: the termination notification.
    var onQuit: (@MainActor () -> Void)?
    var log: [String] = []
    var timeouts: [Duration] = []
    /// The writes that fail (by name).
    var failing: Set<String> = []
    /// How long each write takes.
    var writeDelay: Duration = .zero

    init(running: Bool) {
        self.running = running
    }

    var platform: ClaudeDesktopController.Platform {
        ClaudeDesktopController.Platform(
            isRunning: { [unowned self] in self.running },
            quit: { [unowned self] timeout in
                self.log.append("quit")
                self.timeouts.append(timeout)
                while self.holdQuit {
                    try? await Task.sleep(for: .milliseconds(5))
                }
                guard self.quits else { return false }
                self.running = false
                self.onQuit?()
                return true
            },
            launch: { [unowned self] in
                self.log.append("launch")
                self.running = true
            })
    }

    /// A write that logs "`name` true|false" (with "… end" after a delay)
    /// and reports that registration, or nil when `name` is failing.
    func write(_ name: String) -> ClaudeDesktopController.Write {
        { [unowned self] want in
            self.log.append("\(name) \(want)")
            if self.writeDelay > .zero {
                try? await Task.sleep(for: self.writeDelay)
                self.log.append("\(name) \(want) end")
            }
            return self.failing.contains(name) ? nil : status(desktop: want)
        }
    }

    /// The question, answered with `answer` and logged.
    func ask(_ answer: ClaudeDesktopController.Answer) -> @MainActor () async -> ClaudeDesktopController.Answer {
        { [unowned self] in
            self.log.append("ask")
            return answer
        }
    }
}

/// A controller over `fake` whose own write is `fake.write("own")`, with
/// what it reports collected.
@MainActor
private final class Harness {
    let fake: Fake
    let c: ClaudeDesktopController
    var statuses: [Bool] = []
    var toasts: [String] = []
    var changes: [(pending: Bool?, busy: Bool)] = []

    init(running: Bool) {
        fake = Fake(running: running)
        c = ClaudeDesktopController(platform: fake.platform, write: fake.write("own"))
        c.onStatus = { [unowned self] s in
            self.statuses.append(s.clients.first { $0.id == "claude-desktop" }?.registered ?? false)
        }
        c.onToast = { [unowned self] in self.toasts.append($0) }
        c.onChange = { [unowned self] in self.changes.append((self.c.pending, self.c.busy)) }
    }

    /// The page's flip to `want` with the question answered `answer`.
    func flip(_ want: Bool, _ answer: ClaudeDesktopController.Answer, present: Bool = true) async {
        await c.change(registered: want, status: status(desktop: !want, present: present), ask: fake.ask(answer), write: fake.write("page"))
    }
}

@MainActor
@Suite(.serialized) struct ClaudeDesktopControllerTests {
    @Test func theRestartIsOfferedWhileClaudeDesktopRunsAsAPresentClient() {
        let h = Harness(running: true)
        #expect(h.c.offersRestart(status(desktop: true)))
        #expect(h.c.offersRestart(status(desktop: false)))
        #expect(!h.c.offersRestart(status(desktop: true, present: false)), "not a client: nothing of it to restart")
        #expect(!h.c.offersRestart(nil), "no status yet")
        #expect(!h.c.offersRestart(MCPStatus(command: "/x", clients: [])))
        h.fake.running = false
        #expect(!h.c.offersRestart(status(desktop: true)), "not running: the write sticks")
        #expect(h.fake.log.isEmpty)
    }

    @Test func restartQuitsThenWritesThenLaunches() async {
        let h = Harness(running: true)
        await h.flip(true, .restart)
        #expect(h.fake.log == ["ask", "quit", "page true", "launch"])
        #expect(h.fake.timeouts == [.seconds(20)])
        #expect(h.fake.running)
        #expect(h.c.pending == nil)
        #expect(!h.c.busy)
        #expect(h.statuses == [true], "the new status goes to the Assistant")
        #expect(h.toasts.isEmpty)
        #expect(h.changes.map(\.busy) == [true, false], "busy for the whole restart, nothing pending")

        await h.flip(false, .restart)
        #expect(h.fake.log.suffix(4) == ["ask", "quit", "page false", "launch"])
        #expect(h.statuses == [true, false])
    }

    @Test func aQuitTimeoutWritesAnywayKeepsItPendingAndSaysSo() async throws {
        let h = Harness(running: true)
        h.fake.quits = false
        await h.flip(true, .restart)
        #expect(h.fake.log == ["ask", "quit", "page true"], "written anyway, and not started again: it still runs")
        #expect(h.toasts == ["Claude Desktop did not quit"])
        #expect(h.c.pending == true)
        #expect(h.statuses == [true])

        // It quits later after all: the change is written once more.
        h.fake.running = false
        h.c.terminated()
        try await waitUntil { h.c.pending == nil }
        #expect(h.fake.log == ["ask", "quit", "page true", "own true"])
        #expect(h.statuses == [true, true])
    }

    @Test func laterWritesNowAndTheTerminationWritesOnceMore() async throws {
        let h = Harness(running: true)
        await h.flip(false, .later)
        #expect(h.fake.log == ["ask", "page false"])
        #expect(h.c.pending == false)
        #expect(h.statuses == [false])
        #expect(!h.c.busy)

        // Claude Desktop quits by itself; the notification may come twice.
        h.fake.running = false
        h.c.terminated()
        h.c.terminated()
        try await waitUntil { h.c.pending == nil }
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.fake.log == ["ask", "page false", "own false"], "written once, never started")
        #expect(h.statuses == [false, false])
        #expect(!h.c.busy)

        // Nothing pending any more: a later termination does nothing.
        h.c.terminated()
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.fake.log.count == 3)
    }

    @Test func nothingPendingMeansTheTerminationDoesNothing() async throws {
        let h = Harness(running: true)
        h.fake.running = false
        h.c.terminated()
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.fake.log.isEmpty)
        #expect(h.statuses.isEmpty)
        #expect(h.changes.isEmpty)
    }

    @Test func withoutTheOfferTheChangeIsWrittenAtOnce() async {
        // Not running: no question, and the change sticks.
        let h = Harness(running: false)
        await h.flip(true, .restart)
        #expect(h.fake.log == ["page true"])
        #expect(h.c.pending == nil)
        #expect(h.statuses == [true])

        // Running, but not a client the bridge knows: no question either.
        let g = Harness(running: true)
        await g.flip(true, .restart, present: false)
        #expect(g.fake.log == ["page true"])
        #expect(g.c.pending == nil)
    }

    @Test func aWriteWhileClaudeDesktopDoesNotRunClearsWhatWasPending() async throws {
        let h = Harness(running: true)
        await h.flip(true, .later)
        #expect(h.c.pending == true)
        // It quit, and its termination was not seen: the next write sticks.
        h.fake.running = false
        await h.flip(false, .later)
        #expect(h.fake.log == ["ask", "page true", "page false"])
        #expect(h.c.pending == nil)
    }

    @Test func theRestartsOwnTerminationIsNotWrittenAgain() async throws {
        let h = Harness(running: true)
        await h.flip(true, .later)
        #expect(h.c.pending == true)
        // The pending row's Restart; the notification arrives while the
        // restart waits for the quit.
        h.fake.onQuit = { h.c.terminated() }
        await h.c.restartPending(write: h.fake.write("page"))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.fake.log == ["ask", "page true", "quit", "page true", "launch"])
        #expect(h.c.pending == nil)
    }

    @Test func restartPendingAppliesThePendingState() async {
        let h = Harness(running: true)
        await h.flip(false, .later)
        await h.c.restartPending(write: h.fake.write("page"))
        #expect(h.fake.log == ["ask", "page false", "quit", "page false", "launch"])
        #expect(h.c.pending == nil)
        #expect(h.statuses == [false, false])

        // Nothing pending: nothing happens.
        await h.c.restartPending(write: h.fake.write("page"))
        #expect(h.fake.log.count == 5)
    }

    @Test func noSecondWriteWhileClaudeDesktopRunsAgain() async throws {
        let h = Harness(running: true)
        await h.flip(true, .later)
        // Terminated, but already started again when the write would run:
        // it would not stick, so it stays pending.
        h.c.terminated()
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.fake.log == ["ask", "page true"])
        #expect(h.c.pending == true)
    }

    @Test func aFailedWriteLeavesThePendingStateAlone() async throws {
        let h = Harness(running: true)
        h.fake.failing = ["page"]
        await h.flip(true, .later)
        #expect(h.c.pending == nil, "nothing was written, nothing is pending")
        #expect(h.statuses.isEmpty)

        // The restart's write fails: Claude Desktop is started again all
        // the same.
        await h.flip(true, .restart)
        #expect(h.fake.log == ["ask", "page true", "ask", "quit", "page true", "launch"])
        #expect(h.c.pending == nil)

        // A pending change whose second write fails stays pending.
        h.fake.failing = ["own"]
        await h.flip(true, .later)
        #expect(h.c.pending == true)
        h.fake.running = false
        h.c.terminated()
        try await waitUntil { h.fake.log.last == "own true" }
        try await Task.sleep(for: .milliseconds(20))
        #expect(h.c.pending == true)
    }

    @Test func writesNeverOverlap() async throws {
        let h = Harness(running: true)
        await h.flip(true, .later)
        h.fake.writeDelay = .milliseconds(100)
        h.fake.running = false
        // The re-apply starts; the switch flips while it runs.
        h.c.terminated()
        try await waitUntil { h.fake.log.last == "own true" }
        #expect(h.c.busy)
        await h.flip(false, .later)
        #expect(h.fake.log == ["ask", "page true", "own true", "own true end", "page false", "page false end"])
        #expect(h.c.pending == nil)
        #expect(!h.c.busy)
    }

    @Test func busyWhileTheRestartWaitsForTheQuit() async throws {
        let h = Harness(running: true)
        h.fake.holdQuit = true
        let task = Task { await h.flip(true, .restart) }
        try await waitUntil { h.fake.log.last == "quit" }
        #expect(h.c.busy)
        h.fake.holdQuit = false
        await task.value
        #expect(!h.c.busy)
        #expect(h.fake.log == ["ask", "quit", "page true", "launch"])
    }

    @Test func aClosedControllerIgnoresLateEvents() async throws {
        // Closed while the restart waits for the quit: no write, no launch.
        let h = Harness(running: true)
        h.fake.holdQuit = true
        let task = Task { await h.flip(true, .restart) }
        try await waitUntil { h.fake.log.last == "quit" }
        h.c.close()
        h.fake.holdQuit = false
        await task.value
        #expect(h.fake.log == ["ask", "quit"])
        #expect(h.statuses.isEmpty)
        #expect(h.c.pending == nil)

        // Closed with a change pending: the termination does nothing, and
        // neither does anything else.
        let g = Harness(running: true)
        await g.flip(true, .later)
        #expect(g.c.pending == true)
        g.c.close()
        g.fake.running = false
        g.c.terminated()
        await g.c.restartPending(write: g.fake.write("page"))
        await g.flip(false, .later)
        #expect(!g.c.offersRestart(status(desktop: true)))
        #expect(await g.c.writeOwn(registered: false) == nil)
        try await Task.sleep(for: .milliseconds(50))
        #expect(g.fake.log == ["ask", "page true"])
        #expect(g.statuses == [true])
    }

    @Test func closedWhileTheQuestionIsUpNothingIsWritten() async {
        let h = Harness(running: true)
        await h.c.change(registered: true, status: status(desktop: false), ask: {
            h.c.close()
            return .later
        }, write: h.fake.write("page"))
        #expect(h.fake.log.isEmpty)
    }

    @Test func theOwnWriteRunsTheBridge() async throws {
        // The convenience initialiser's own registration against a
        // stand-in malachi-mcp that logs its arguments.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-claude-\(UUID().uuidString.prefix(8))", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let log = dir.appendingPathComponent("calls")
        let bridge = dir.appendingPathComponent("malachi-mcp")
        let json = #"{"command": "/x/malachi-mcp", "clients": [{"id": "claude-desktop", "name": "Claude Desktop", "present": true, "registered": true}]}"#
        let script = """
            #!/bin/sh
            printf '%s\\n' "$*" >> '\(log.path)'
            [ "$1 $2" = "install --json" ] || { echo "unexpected $*" >&2; exit 2; }
            printf '%s\\n' '\(json)'
            """
        try Data(script.utf8).write(to: bridge)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: bridge.path)

        let fake = Fake(running: true)
        let c = ClaudeDesktopController(bridge: bridge.path, platform: fake.platform)
        var statuses: [MCPStatus] = []
        c.onStatus = { statuses.append($0) }
        await c.change(registered: true, status: status(desktop: false), ask: fake.ask(.later), write: fake.write("page"))
        #expect(c.pending == true)
        fake.running = false
        c.terminated()
        try await waitUntil { c.pending == nil }
        #expect(try String(contentsOf: log, encoding: .utf8) == "install --json\n")
        #expect(statuses.count == 2)
        #expect(statuses.last?.isRegistered == true)

        // Without a bridge the own write fails quietly and the change stays
        // pending.
        let none = ClaudeDesktopController(bridge: nil, platform: fake.platform)
        fake.running = true
        await none.change(registered: true, status: status(desktop: false), ask: fake.ask(.later), write: fake.write("page"))
        #expect(await none.writeOwn(registered: true) == nil)
        #expect(none.pending == true)
    }
}
