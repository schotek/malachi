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

    @Test func protocolMismatchIsReported() async throws {
        let fake = try await makeFake(protocolVersion: 99)
        defer { Task { await fake.stop() } }
        let cc = ConnectionController(client: RPCClient(socketPath: fake.path), supervisor: nil, reconnectInterval: .milliseconds(100))
        cc.start()
        try await waitUntil { cc.state == .protocolMismatch(daemon: 99) }
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
