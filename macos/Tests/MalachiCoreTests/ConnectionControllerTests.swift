// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

private let fakeInfo = SystemInfo(version: "fake", protocolVersion: API.protocolVersion, pid: 7, storePath: "/tmp/s.db")

private func infoJSON(protocolVersion: Int) -> Data {
    json(#"{"version":"fake","protocolVersion":\#(protocolVersion),"pid":7,"storePath":"/tmp/s.db"}"#)
}

/// A fake answering system.info; started unless `start` is false.
private func makeFake(protocolVersion: Int = API.protocolVersion, start: Bool = true) async throws -> FakeDaemon {
    let fake = try FakeDaemon()
    await fake.on(API.systemInfo) { _ in infoJSON(protocolVersion: protocolVersion) }
    if start {
        try await fake.start()
    }
    return fake
}

/// Collects what the controller reports.
@MainActor
private final class Log {
    var states: [ConnectionController.ConnectionState] = []
    var notifications: [String] = []

    func attach(_ cc: ConnectionController) {
        cc.onState = { [unowned self] s in self.states.append(s) }
        cc.onNotification = { [unowned self] n in self.notifications.append(n.method) }
    }
}

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

@MainActor
private func waitUntilAsync(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while true {
        if await cond() {
            return
        }
        if ContinuousClock.now > deadline {
            throw Timeout()
        }
        try await Task.sleep(for: .milliseconds(10))
    }
}

@MainActor
private func isConnected(_ s: ConnectionController.ConnectionState) -> Bool {
    if case .connected = s { return true } else { return false }
}

@MainActor
private func isUnavailable(_ s: ConnectionController.ConnectionState) -> Bool {
    if case .unavailable = s { return true } else { return false }
}

@MainActor
@Suite(.serialized) struct ConnectionControllerTests {
    @Test func connectsAndReportsSystemInfo() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        #expect(cc.state == .connecting)

        cc.start()
        try await waitUntil { isConnected(cc.state) }
        #expect(cc.state == .connected(fakeInfo))
        #expect(log.states == [.connecting, .connected(fakeInfo)])
        #expect(await fake.calls == [API.systemInfo])

        await cc.stop()
        #expect(cc.state == .stopping)
        #expect(log.states.last == .stopping)
        #expect(await cc.client.state == .disconnected(reason: nil))
        // Nothing after stopping: the close must not surface as unavailable.
        try await Task.sleep(for: .milliseconds(150))
        #expect(log.states.last == .stopping)
    }

    @Test func adoptsTheDaemonThroughTheSupervisor() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let sup = DaemonSupervisor(launch: nil, socket: fake.path)
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: sup, reconnectInterval: .milliseconds(100))
        cc.start()
        try await waitUntil { isConnected(cc.state) }
        #expect(await sup.spawns == 0)
        await cc.stop()
        // Somebody else's daemon is left alone.
        #expect(UnixSocketProbe.answers(fake.path))
    }

    /// The handshake agreed, system.info does not: still a mismatch, as a
    /// defence.
    @Test func protocolMismatchIsReported() async throws {
        let fake = try await makeFake(protocolVersion: 99)
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        cc.start()
        try await waitUntil { cc.state == .protocolMismatch(daemon: 99) }
        await cc.stop()
    }

    /// A daemon of another protocol version fails the handshake's check:
    /// the mismatch is reported and nothing is called, and the retry loop
    /// keeps it up instead of saying "Connecting…" on every attempt, until
    /// an attempt ends otherwise.
    @Test func anotherProtocolIsAStickyMismatch() async throws {
        let cases: [(FakeDaemon.HandshakeMode, Int)] = [(.protocolVersion(99), 99), (.oldDaemon, 1)]
        for (mode, daemon) in cases {
            let fake = try await makeFake()
            await fake.setHandshake(mode)
            let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
            let log = Log()
            log.attach(cc)
            cc.start()
            try await waitUntil { cc.state == .protocolMismatch(daemon: daemon) }
            try await waitUntilAsync { await fake.handshakes.count >= 3 }
            #expect(log.states == [.connecting, .protocolMismatch(daemon: daemon)], "no Connecting… between the attempts")
            #expect(await fake.calls.isEmpty, "nothing is called on a daemon of another protocol")
            #expect(cc.loggedRefusals == [RPCClient.HandshakeError.protocolMismatch(daemon: daemon).description], "logged once")

            // A daemon of this protocol takes over: the connection ends the
            // mismatch, "Connecting…" until system.info answers.
            await fake.setHandshake(.normal)
            try await waitUntil { isConnected(cc.state) }
            #expect(log.states == [.connecting, .protocolMismatch(daemon: daemon), .connecting, .connected(fakeInfo)])
            #expect(cc.loggedRefusals.isEmpty, "a connection starts the log afresh")
            await cc.stop()
            await fake.stop()
        }
    }

    /// GTK drops the mismatch at Connected: here too, as soon as the client
    /// is connected, while system.info is still on its way.
    @Test func aConnectionEndsTheMismatchBeforeSystemInfoAnswers() async throws {
        // system.info answers only once the gate is opened.
        let (gate, open) = AsyncStream<Void>.makeStream()
        let fake = try FakeDaemon()
        await fake.on(API.systemInfo) { _ in
            for await _ in gate {
                break
            }
            return infoJSON(protocolVersion: API.protocolVersion)
        }
        await fake.setHandshake(.oldDaemon)
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        let cc = ConnectionController(client: client, supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { cc.state == .protocolMismatch(daemon: 1) }

        await fake.setHandshake(.normal)
        try await waitUntil { cc.state == .connecting }
        #expect(await client.state == .connected)
        #expect(!log.states.contains(where: isConnected), "system.info has not answered yet")
        open.yield(())
        open.finish()
        try await waitUntil { isConnected(cc.state) }
        #expect(log.states == [.connecting, .protocolMismatch(daemon: 1), .connecting, .connected(fakeInfo)])
        await cc.stop()
    }

    /// The mismatch stays up only while the attempts end in one: an attempt
    /// that ends otherwise replaces it, and the next says "Connecting…".
    @Test func aMismatchGivesWayWhenAnAttemptEndsOtherwise() async throws {
        let fake = try await makeFake()
        await fake.setHandshake(.oldDaemon)
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { cc.state == .protocolMismatch(daemon: 1) }
        await fake.stop() // nothing answers on the socket any more
        try await waitUntil { log.states.count >= 4 }
        #expect(log.states.prefix(4).map(\.kind) == ["connecting", "protocolMismatch", "unavailable", "connecting"])
        await cc.stop()
    }

    /// A connection that breaks during the handshake is routine, as when the
    /// daemon went away: unavailable, logged at debug level, not a refusal.
    @Test func aBrokenHandshakeIsUnavailableNotARefusal() async throws {
        let fake = try await makeFake()
        await fake.setHandshake(.raw(.init(hello: { _ in nil }, closeAfterHello: true)))
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        // The retry loop keeps moving: what was reported, not the state now.
        try await waitUntil { log.states.contains(where: isUnavailable) }
        try await waitUntilAsync { await fake.handshakes.count >= 2 }
        #expect(cc.loggedRefusals.isEmpty, "a broken connection is no refusal")
        #expect(await fake.calls.isEmpty)
        await cc.stop()
    }

    /// The peer on the socket chooses the error codes of its refusals, and
    /// so their texts: what the log remembers starts afresh after 16.
    @Test func loggedRefusalsAreCapped() async throws {
        let attempts = Counter()
        let fake = try await makeFake()
        await fake.setHandshake(.raw(.init(hello: { _ in
            let refusal = RPCError(code: ErrorCode(rawValue: 2000 + attempts.next()), message: "no")
            return String(decoding: FakeDaemon.response(id: 1, error: refusal), as: UTF8.self)
        })))
        defer { Task { await fake.stop() } }
        // The attempts are driven by hand, one at a time: the retry loop
        // never moves in between.
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .seconds(60))
        let refused = { (n: Int) in RPCClient.HandshakeError.rejected(ErrorCode(rawValue: 2000 + n)).description }
        cc.start()
        for n in 1...17 {
            if n > 1 {
                cc.reconnectNow()
            }
            try await waitUntil { cc.state == .unavailable(refused(n)) }
            if n == 16 {
                #expect(cc.loggedRefusals == (1...16).map(refused), "16 distinct refusals are remembered")
            }
        }
        #expect(cc.loggedRefusals == [refused(17)], "the 17th starts afresh")
        await cc.stop()
    }

    /// Something on the socket that does not hold the key is no daemon to
    /// use: unavailable, retried, and logged at error level only once.
    @Test func anUnprovenDaemonIsUnavailableAndLoggedOnce() async throws {
        let fake = try await makeFake()
        await fake.setHandshake(.wrongProof)
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        let refused = RPCClient.HandshakeError.daemonUnproven.description
        // The retry loop keeps moving: what was reported, not the state now.
        try await waitUntil { log.states.contains(.unavailable(refused)) }
        try await waitUntilAsync { await fake.handshakes.count >= 3 }
        #expect(cc.loggedRefusals == [refused], "logged once, the repeats at debug level")
        #expect(!log.states.contains(where: isConnected))
        let seen = await fake.handshakes
        #expect(seen.allSatisfy { $0 == API.SystemHello.name }, "system.authenticate was never sent")
        #expect(await fake.calls.isEmpty)
        await cc.stop()
    }

    /// A restarted daemon has a new key: the connection drops, and the next
    /// attempt reads the new key and connects.
    @Test func aRestartedDaemonIsReconnectedWithItsNewKey() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { isConnected(cc.state) }
        try await fake.restart()
        try await waitUntil { log.states.count >= 5 && isConnected(cc.state) }
        #expect(log.states.map(\.kind) == ["connecting", "connected", "unavailable", "connecting", "connected"])
        #expect(await fake.handshakes == [
            API.SystemHello.name, API.SystemAuthenticate.name, API.SystemHello.name, API.SystemAuthenticate.name,
        ])
        #expect(cc.loggedRefusals.isEmpty)
        await cc.stop()
    }

    @Test func infoFailureIsReported() async throws {
        let fake = try FakeDaemon()
        await fake.on(API.systemInfo) { _ in throw RPCError(code: 1000, message: "broken") }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        cc.start()
        try await waitUntil { if case .infoFailed = cc.state { return true } else { return false } }
        #expect(cc.state == .infoFailed("broken (1000)"))
        await cc.stop()
    }

    @Test func serverCloseIsUnavailableThenReconnects() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { isConnected(cc.state) }

        await fake.closeAll()
        try await waitUntil { isUnavailable(cc.state) }
        guard case .unavailable(let reason) = cc.state else { return }
        #expect(!reason.isEmpty)

        // The retry loop dials again after the interval.
        let start = ContinuousClock.now
        try await waitUntil { log.states.count >= 5 && isConnected(cc.state) }
        #expect(ContinuousClock.now - start < .seconds(3))
        #expect(await fake.accepted == 2)
        #expect(log.states.map(\.kind) == ["connecting", "connected", "unavailable", "connecting", "connected"])
        await cc.stop()
    }

    @Test func retriesUntilTheDaemonAppears() async throws {
        let fake = try await makeFake(start: false)
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { isUnavailable(cc.state) }
        // Repeated identical failures do not repeat the state.
        try await Task.sleep(for: .milliseconds(250))
        #expect(log.states.filter(isUnavailable).count >= 1)

        try await fake.start()
        try await waitUntil { isConnected(cc.state) }
        #expect(log.states.first == .connecting)
        #expect(log.states.contains(where: isUnavailable))
        await cc.stop()
    }

    @Test func supervisorFailureIsUnavailable() async throws {
        let dead = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-cc-\(UUID().uuidString.prefix(8)).sock").path
        let sup = DaemonSupervisor(launch: nil, socket: dead)
        let cc = ConnectionController(client: RPCClient(socketPath: dead), supervisor: sup, reconnectInterval: .milliseconds(100))
        cc.start()
        try await waitUntil { isUnavailable(cc.state) }
        #expect(cc.state == .unavailable(DaemonSupervisor.SupervisorError.noDaemon.description))
        await cc.stop()
    }

    @Test func notificationsAreForwardedInOrder() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { isConnected(cc.state) }

        for i in 1...5 {
            await fake.pushNotification(method: "notify.test\(i)", paramsJSON: #"{"i":\#(i)}"#)
        }
        try await waitUntil { log.notifications.count == 5 }
        #expect(log.notifications == (1...5).map { "notify.test\($0)" })
        await cc.stop()
    }

    @Test func reconnectNowIsANoOpWhileConnected() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .seconds(30))
        let log = Log()
        log.attach(cc)
        cc.start()
        try await waitUntil { isConnected(cc.state) }
        cc.reconnectNow()
        cc.start()
        try await Task.sleep(for: .milliseconds(100))
        #expect(log.states == [.connecting, .connected(fakeInfo)])
        #expect(await fake.accepted == 1)
        await cc.stop()
    }
}

/// Counts calls from any thread (a scripted daemon's attempts).
private final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0

    func next() -> Int {
        lock.lock()
        defer { lock.unlock() }
        n += 1
        return n
    }
}

private extension ConnectionController.ConnectionState {
    var kind: String {
        switch self {
        case .connecting: return "connecting"
        case .connected: return "connected"
        case .protocolMismatch: return "protocolMismatch"
        case .infoFailed: return "infoFailed"
        case .unavailable: return "unavailable"
        case .stopping: return "stopping"
        }
    }
}
