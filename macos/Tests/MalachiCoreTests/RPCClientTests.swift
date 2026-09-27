// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

private let systemInfoJSON = #"{"version":"fake","protocolVersion":2,"pid":42,"storePath":"/tmp/store.db"}"#

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
        #expect(info == SystemInfo(version: "fake", protocolVersion: 2, pid: 42, storePath: "/tmp/store.db"))
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
        // The fake notifies authenticated connections only, and a connected
        // client is one it has authenticated; a round trip first all the same.
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

    @Test func statesStreamReportsTransitionsInOrder() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        await client.close()
        // Buffered: the consumer may start late and still see everything.
        var seen: [RPCClient.State] = []
        for await s in client.states {
            seen.append(s)
            if seen.count == 3 { break }
        }
        #expect(seen == [.connecting, .connected, .disconnected(reason: nil)])
    }

    @Test func notificationsStreamPreservesOrder() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        for i in 1...20 {
            await fake.pushNotification(method: "notify.n\(i)", paramsJSON: #"{"i":\#(i)}"#)
        }
        var methods: [String] = []
        for await n in client.notifications {
            methods.append(n.method)
            if methods.count == 20 { break }
        }
        #expect(methods == (1...20).map { "notify.n\($0)" })
    }

    @Test func perMethodHandlersSeeParams() async throws {
        let fake = try FakeDaemon()
        await fake.on("test.echo") { params in
            struct P: Decodable { let x: Int }
            let p = try JSONDecoder().decode(P.self, from: params)
            return json(#"{"which":"x=\#(p.x)"}"#)
        }
        await fake.on("test.fail") { _ in throw RPCError(code: 1001, message: "no") }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        struct X: Encodable { let x: Int }
        let r: Which = try await client.call("test.echo", X(x: 5))
        #expect(r == Which(which: "x=5"))
        await #expect(throws: RPCError(code: 1001, message: "no")) {
            let _: Which = try await client.call("test.fail", EmptyParams())
        }
        await #expect(throws: RPCError(code: FakeDaemon.methodNotFoundCode, message: "unknown method nope")) {
            let _: Which = try await client.call("nope", EmptyParams())
        }
        #expect(await fake.calls == ["test.echo", "test.fail", "nope"])
    }

    @Test func cancellationFailsThePendingCall() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let pending = Task<Which, any Error> { try await client.call("test.never", EmptyParams(), timeout: .seconds(10)) }
        try? await Task.sleep(for: .milliseconds(50))
        let start = ContinuousClock.now
        pending.cancel()
        await #expect(throws: CancellationError.self) { _ = try await pending.value }
        #expect(ContinuousClock.now - start < .seconds(1))
        // The connection itself is unharmed.
        let info: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(info.pid == 42)
        #expect(await client.state == .connected)
    }

    @Test func alreadyCancelledTaskDoesNotSend() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let t = Task<Which, any Error> {
            try? await Task.sleep(for: .seconds(5))
            return try await client.call("test.fast", EmptyParams())
        }
        t.cancel()
        await #expect(throws: CancellationError.self) { _ = try await t.value }
        let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(await fake.calls == [API.systemInfo])
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

    // MARK: Handshake (docs/api.md §1.4; api handshake_test.go)

    @Test func handshakePrecedesTheFirstCall() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        #expect(await client.state == .connected)
        #expect(await fake.handshakes == [API.SystemHello.name, API.SystemAuthenticate.name])
        let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(await fake.received == [API.SystemHello.name, API.SystemAuthenticate.name, API.systemInfo])
        #expect(await fake.calls == [API.systemInfo], "the handshake is not a call")
        await client.close()
    }

    @Test func callsWaitForTheHandshake() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.silent)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path, handshakeTimeout: .seconds(10))
        let dial = Task { try await client.connect() }
        try await eventually { await fake.handshakes == [API.SystemHello.name] }
        #expect(await client.state == .connecting, "connecting until the handshake is done")
        await #expect(throws: RPCClient.ClientError.notConnected) {
            let _: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        }
        await client.close()
        await #expect(throws: RPCClient.ClientError.disconnected) { try await dial.value }
        #expect(await fake.calls.isEmpty)
        #expect(await fake.received == [API.SystemHello.name])
    }

    @Test func notificationsBeforeTheHelloAnswerAreDropped() async throws {
        let fake = try await startFake()
        await fake.setNotificationsBeforeHello(8)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        await fake.pushNotification(method: "notify.late", paramsJSON: "{}")
        var first: String?
        for await n in client.notifications {
            first = n.method
            break
        }
        #expect(first == "notify.late", "the eight notify.early before the answer never reach the consumer")
        await client.close()
    }

    @Test func aNinthNotificationBeforeTheHelloAnswerIsMalformed() async throws {
        let fake = try await startFake()
        await fake.setNotificationsBeforeHello(9)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        #expect(await refusal(client) == .malformed("an unexpected notification"))
        #expect(await fake.handshakes == [API.SystemHello.name])
    }

    /// The system.authenticate answer and a notification in one write: the
    /// notification is held and delivered right after .connected.
    @Test func aNotificationWithTheAuthenticateAnswerFollowsConnected() async throws {
        let fake = try await startFake()
        await fake.setNotificationWithAuthenticateAnswer("notify.first")
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        let events = Events()
        await client.setStateHandler { s in events.add(.state(s)) }
        await client.setNotificationHandler { n in events.add(.notification(n.method)) }
        try await client.connect()
        try await eventually { events.all.count >= 3 }
        #expect(events.all == [.state(.connecting), .state(.connected), .notification("notify.first")])
        await client.close()
    }

    @Test func aDaemonOfProtocolOneIsAMismatch() async throws {
        for early in [0, RPCAuth.maxSkippedNotifications] {
            let fake = try await startFake()
            await fake.setHandshake(.oldDaemon)
            await fake.setNotificationsBeforeHello(early)
            let client = RPCClient(socketPath: fake.path)
            // Both ways a consumer gets notifications: the handler and the stream.
            let events = Events()
            await client.setNotificationHandler { n in events.add(.notification(n.method)) }
            let reader = Task {
                for await n in client.notifications {
                    events.add(.notification(n.method))
                }
            }
            let refused = await refusal(client)
            #expect(refused == .protocolMismatch(daemon: 1), "\(early) notifications first")
            #expect(await client.state == .disconnected(reason: refused?.description))
            // Nothing after system.hello: no falling back to an
            // unauthenticated connection.
            try await Task.sleep(for: .milliseconds(100))
            #expect(await fake.received == [API.SystemHello.name])
            // The notifications before the answer never reach the consumer.
            reader.cancel()
            #expect(events.all.isEmpty, "\(early) notifications first: \(events.all)")
            await fake.stop()
        }
    }

    @Test func aDaemonOfALaterProtocolIsAMismatchBeforeTheKeyIsRead() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.protocolVersion(99))
        defer { Task { await fake.stop() } }
        // No key file at all: the version is compared before it is read.
        try FileManager.default.removeItem(atPath: fake.keyPath)
        let client = RPCClient(socketPath: fake.path)
        #expect(await refusal(client) == .protocolMismatch(daemon: 99))
        try await Task.sleep(for: .milliseconds(100))
        #expect(await fake.received == [API.SystemHello.name])
    }

    @Test func aProcessWithoutTheKeyIsNeverAuthenticatedTo() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.wrongProof)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        #expect(await refusal(client) == .daemonUnproven)
        try await Task.sleep(for: .milliseconds(100))
        #expect(await fake.handshakes == [API.SystemHello.name], "system.authenticate was never sent")
        #expect(await fake.received == [API.SystemHello.name])
    }

    @Test func aRefusedProofIsRejected() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.rejectClient)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        #expect(await refusal(client) == .rejected(.unauthenticated))
        #expect(await fake.handshakes == [API.SystemHello.name, API.SystemAuthenticate.name])
        #expect(await fake.calls.isEmpty)
    }

    @Test func aMissingKeyFileIsKeyUnavailable() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        try FileManager.default.removeItem(atPath: fake.keyPath)
        let client = RPCClient(socketPath: fake.path)
        guard case .keyUnavailable(let reason)? = await refusal(client) else {
            Issue.record("expected keyUnavailable")
            return
        }
        #expect(reason == "\(fake.keyPath) does not exist")
        try await Task.sleep(for: .milliseconds(100))
        #expect(await fake.received == [API.SystemHello.name], "nothing was sent after system.hello")
    }

    @Test func aMalformedProofIsMalformed() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.malformedProof)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        #expect(await refusal(client) == .malformed("daemonProof is not 64 lowercase hex digits"))
        try await Task.sleep(for: .milliseconds(100))
        #expect(await fake.received == [API.SystemHello.name])
    }

    @Test func aSilentDaemonTimesOut() async throws {
        let fake = try await startFake()
        await fake.setHandshake(.silent)
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path, handshakeTimeout: .milliseconds(200))
        let start = ContinuousClock.now
        #expect(await refusal(client) == .timedOut)
        #expect(ContinuousClock.now - start < .seconds(3))
        #expect(await client.state == .disconnected(reason: RPCClient.HandshakeError.timedOut.description))
    }

    /// A restarted daemon has a new key: the client reads the key file
    /// afresh for every connection and never keeps one.
    @Test func aRestartedDaemonIsAuthenticatedWithItsNewKey() async throws {
        let fake = try await startFake()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let before = try Data(contentsOf: URL(fileURLWithPath: fake.keyPath))
        try await fake.restart()
        try await eventually {
            if case .disconnected = await client.state {
                return true
            }
            return false
        }
        #expect(try Data(contentsOf: URL(fileURLWithPath: fake.keyPath)) != before, "a new key")
        try await client.connect()
        let info: SystemInfo = try await client.call(API.systemInfo, EmptyParams())
        #expect(info.pid == 42)
        #expect(await fake.handshakes == [
            API.SystemHello.name, API.SystemAuthenticate.name, API.SystemHello.name, API.SystemAuthenticate.name,
        ])
        await client.close()
    }
}

private struct TimedOut: Error {}

/// Polls `cond` until it holds, for at most `timeout`.
private func eventually(_ timeout: Duration = .seconds(5), _ cond: () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while true {
        if await cond() {
            return
        }
        if ContinuousClock.now > deadline {
            throw TimedOut()
        }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What `connect()` threw as a handshake refusal; nil, with an issue
/// recorded, when it connected or threw anything else.
private func refusal(_ client: RPCClient) async -> RPCClient.HandshakeError? {
    do {
        try await client.connect()
        Issue.record("connect() succeeded")
    } catch let e as RPCClient.HandshakeError {
        return e
    } catch {
        Issue.record("connect() threw \(error)")
    }
    return nil
}

/// What a client reported through its handlers, in the order it happened:
/// they run on the client's actor, one after the other.
private final class Events: @unchecked Sendable {
    enum Event: Equatable {
        case state(RPCClient.State)
        case notification(String)
    }

    private let lock = NSLock()
    private var list: [Event] = []

    func add(_ e: Event) {
        lock.lock()
        list.append(e)
        lock.unlock()
    }

    var all: [Event] {
        lock.lock()
        defer { lock.unlock() }
        return list
    }
}

/// Records states seen by a handler.
actor StateLog {
    private var states: [RPCClient.State] = []
    func add(_ s: RPCClient.State) { states.append(s) }
    func saw(_ pred: (RPCClient.State) -> Bool) -> Bool { states.contains(where: pred) }
}
