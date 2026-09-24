// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

private let systemInfoJSON = #"{"version":"fake","protocolVersion":1,"pid":42,"storePath":"/tmp/store.db"}"#

/// A handler answering system.info and a few test methods.
private let standardHandler: FakeDaemon.Handler = { method, _ in
    switch method {
    case API.systemInfo:
        return .success(json(systemInfoJSON))
    case "test.slow":
        try? await Task.sleep(for: .milliseconds(300))
        return .success(json(#"{"which":"slow"}"#))
    case "test.fast":
        return .success(json(#"{"which":"fast"}"#))
    case "test.never":
        try? await Task.sleep(for: .seconds(30))
        return .success(json("{}"))
    default:
        return .failure(RPCError(code: -32601, message: "unknown method \(method)"))
    }
}

private struct Which: Decodable, Sendable, Equatable { let which: String }

@Suite(.serialized) struct RPCClientTests {
    private func startFake(_ handler: @escaping FakeDaemon.Handler = standardHandler) async throws -> FakeDaemon {
        let fake = try FakeDaemon(handler: handler)
        try await fake.start()
        return fake
    }

    @Test func systemInfoRoundTrip() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        #expect(await client.state == .connected)
        let info: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(info == SystemInfo(version: "fake", protocolVersion: 1, pid: 42, storePath: "/tmp/store.db"))
        #expect(UnixSocketProbe.answers(fake.path))
        await client.close()
        #expect(await client.state == .disconnected(reason: nil))
    }

    @Test func daemonErrorArrivesAsRPCError() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        await #expect(throws: RPCError(code: -32601, message: "unknown method nope")) {
            let _: Which = try await client.call("nope", EmptyParams())
        }
    }

    @Test func outOfOrderRepliesAreMatchedById() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        async let slow: Which = client.call("test.slow", EmptyParams())
        try? await Task.sleep(for: .milliseconds(20))
        async let fast: Which = client.call("test.fast", EmptyParams())
        let (s, f) = try await (slow, fast)
        #expect(s == Which(which: "slow"))
        #expect(f == Which(which: "fast"))
    }

    @Test func silenceTimesOut() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        await #expect(throws: RPCClient.ClientError.timeout(method: "test.never")) {
            let _: Which = try await client.call("test.never", EmptyParams(), timeout: .milliseconds(200))
        }
        // The connection itself is unharmed.
        let info: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(info.pid == 42)
    }

    @Test func notificationsAreDelivered() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        // One round trip first: the fake registers an accepted connection
        // asynchronously, and a notification pushed before that goes nowhere.
        let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        let received: RPCNotification = try await withCheckedThrowingContinuation { cont in
            Task {
                await client.setNotificationHandler { n in cont.resume(returning: n) }
                await fake.pushNotification(method: "notify.accountsChanged", paramsJSON: "{}")
            }
        }
        #expect(received.method == "notify.accountsChanged")
        struct Empty: Decodable {}
        _ = try received.params(Empty.self)
    }

    @Test func serverCloseFailsPendingCallAndDisconnects() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let states = StateLog()
        await client.setStateHandler { s in Task { await states.add(s) } }
        let pending = Task<Which, any Error> { try await client.call("test.never", EmptyParams(), timeout: .seconds(10)) }
        try? await Task.sleep(for: .milliseconds(50))
        await fake.closeAll()
        await #expect(throws: RPCClient.ClientError.disconnected) { _ = try await pending.value }
        for _ in 0..<50 {
            if case .disconnected = await client.state { break }
            try? await Task.sleep(for: .milliseconds(20))
        }
        guard case .disconnected(let reason) = await client.state else {
            Issue.record("client still \(await client.state)")
            return
        }
        #expect(reason != nil)
        #expect(await states.saw { if case .disconnected = $0 { return true } else { return false } })
    }

    @Test func deadPathFailsPromptly() async throws {
        let path = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-nobody.sock").path
        try? FileManager.default.removeItem(atPath: path)
        let client = RPCClient(socketPath: path)
        let start = ContinuousClock.now
        await #expect(throws: (any Error).self) { try await client.connect() }
        #expect(ContinuousClock.now - start < .seconds(3), "a refused unix connect must not hang")
        #expect(!UnixSocketProbe.answers(path))
        if case .disconnected = await client.state {} else { Issue.record("expected disconnected") }
    }
}

/// Records states seen by a handler.
actor StateLog {
    private var states: [RPCClient.State] = []
    func add(_ s: RPCClient.State) { states.append(s) }
    func saw(_ pred: (RPCClient.State) -> Bool) -> Bool { states.contains(where: pred) }
}
