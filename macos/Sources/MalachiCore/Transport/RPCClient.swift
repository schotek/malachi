// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Network

/// A JSON-RPC 2.0 client for the malachid unix socket, the Swift counterpart
/// of ui/internal/client. It is transport only: it sends requests, matches
/// responses by id and forwards notifications. Reconnecting is the caller's
/// job; `connect()` is single-shot.
///
/// The actor owns the `NWConnection`, which never leaves it. Every framework
/// callback captures only the actor and value types and hops back in with a
/// `Task`; a generation counter discards callbacks of a connection that has
/// been torn down meanwhile. Exactly one receive is in flight at any time and
/// the next one is armed from the actor after the chunk was consumed, so
/// chunks cannot be reordered.
public actor RPCClient {
    public enum State: Sendable, Equatable {
        case disconnected(reason: String?)
        case connecting
        case connected
    }

    public enum ClientError: Error, Sendable, Equatable, CustomStringConvertible {
        case notConnected
        case timeout(method: String)
        case disconnected
        case transport(String)

        public var description: String {
            switch self {
            case .notConnected:
                return "not connected to malachid"
            case .timeout(let method):
                return "\(method) timed out"
            case .disconnected:
                return "connection to malachid lost"
            case .transport(let reason):
                return reason
            }
        }
    }

    public nonisolated let socketPath: String

    /// Every notification, in the order the daemon sent it (a newMessage
    /// never overtakes the syncState that follows it). One consumer only:
    /// an `AsyncStream` is not broadcast. Buffered while nobody reads.
    public nonisolated let notifications: AsyncStream<RPCNotification>
    /// Every state transition, in order. One consumer only.
    public nonisolated let states: AsyncStream<State>

    private let notificationsIn: AsyncStream<RPCNotification>.Continuation
    private let statesIn: AsyncStream<State>.Continuation

    private let queue = DispatchQueue(label: "io.github.schotek.Malachi.rpc")
    private var connection: NWConnection?
    private var generation = 0
    private var framer = LineFramer()
    private var nextID = 1
    private var pending: [Int: CheckedContinuation<Data, any Error>] = [:]
    private var timeouts: [Int: Task<Void, Never>] = [:]
    private var connectContinuation: CheckedContinuation<Void, any Error>?
    public private(set) var state: State = .disconnected(reason: nil)
    private var onState: (@Sendable (State) -> Void)?
    private var onNotification: (@Sendable (RPCNotification) -> Void)?

    public init(socketPath: String) {
        self.socketPath = socketPath
        (notifications, notificationsIn) = AsyncStream.makeStream(of: RPCNotification.self, bufferingPolicy: .unbounded)
        (states, statesIn) = AsyncStream.makeStream(of: State.self, bufferingPolicy: .unbounded)
    }

    deinit {
        notificationsIn.finish()
        statesIn.finish()
    }

    /// A closure alternative to `states`; called on the actor.
    public func setStateHandler(_ handler: @escaping @Sendable (State) -> Void) {
        onState = handler
    }

    /// A closure alternative to `notifications`; called on the actor.
    public func setNotificationHandler(_ handler: @escaping @Sendable (RPCNotification) -> Void) {
        onNotification = handler
    }

    /// How long a dial may take once something listens on the socket.
    public static let connectTimeout: Duration = .seconds(5)

    /// Dials the socket. Returns once the connection is ready; throws when
    /// nothing answers. A connected client returns at once.
    public func connect() async throws {
        if case .connected = state {
            return
        }
        teardown(reason: nil)
        // A POSIX probe first: Network.framework keeps a refused unix dial
        // in .waiting (the peer might appear), which is exactly the wrong
        // answer for a local daemon that is simply not running.
        guard UnixSocketProbe.answers(socketPath) else {
            let reason = "nothing listens on \(socketPath)"
            setState(.disconnected(reason: reason))
            throw ClientError.transport(reason)
        }
        generation += 1
        let gen = generation
        let conn = NWConnection(to: .unix(path: socketPath), using: .tcp)
        connection = conn
        setState(.connecting)
        conn.stateUpdateHandler = { [weak self] st in
            Task { await self?.connectionStateChanged(st, gen: gen) }
        }
        connectDeadline = Task { [weak self] in
            try? await Task.sleep(for: RPCClient.connectTimeout)
            await self?.connectTimedOut(gen: gen)
        }
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, any Error>) in
            connectContinuation = cont
            conn.start(queue: queue)
        }
    }

    private var connectDeadline: Task<Void, Never>?

    private func connectTimedOut(gen: Int) {
        guard gen == generation, connectContinuation != nil else { return }
        connectContinuation?.resume(throwing: ClientError.timeout(method: "connect"))
        connectContinuation = nil
        teardown(reason: "connecting to \(socketPath) timed out")
    }

    private func connectionStateChanged(_ st: NWConnection.State, gen: Int) {
        guard gen == generation else { return }
        switch st {
        case .ready:
            connectDeadline?.cancel()
            connectDeadline = nil
            setState(.connected)
            connectContinuation?.resume()
            connectContinuation = nil
            receiveNext(gen: gen)
        case .failed(let error), .waiting(let error):
            // Network.framework reports a refused unix connect as .waiting
            // (it might become reachable); for a local socket that means
            // nobody listens, so it is a failure here.
            let reason = describe(error)
            connectContinuation?.resume(throwing: ClientError.transport(reason))
            connectContinuation = nil
            teardown(reason: reason)
        case .cancelled:
            teardown(reason: nil)
        default:
            break
        }
    }

    private func receiveNext(gen: Int) {
        connection?.receive(minimumIncompleteLength: 1, maximumLength: 64 << 10) { [weak self] data, _, complete, error in
            Task { await self?.didReceive(data, complete: complete, error: error, gen: gen) }
        }
    }

    private func didReceive(_ data: Data?, complete: Bool, error: NWError?, gen: Int) {
        guard gen == generation else { return }
        do {
            for line in try framer.append(data ?? Data()) {
                dispatch(line)
            }
        } catch {
            teardown(reason: "a line from malachid exceeds \(LineFramer.defaultMaxLine) bytes")
            return
        }
        if let error {
            teardown(reason: describe(error))
            return
        }
        if complete {
            teardown(reason: "connection closed by malachid")
            return
        }
        receiveNext(gen: gen)
    }

    private func dispatch(_ line: Data) {
        guard let env = try? JSONCoding.decoder().decode(Envelope.self, from: line) else {
            return // a malformed line from the daemon is ignored, as the GTK client does
        }
        if let id = env.id, let cont = pending.removeValue(forKey: id) {
            timeouts.removeValue(forKey: id)?.cancel()
            if let err = env.error {
                cont.resume(throwing: err)
            } else {
                cont.resume(returning: line)
            }
        } else if env.isNotification, let method = env.method {
            let n = RPCNotification(method: method, line: line)
            notificationsIn.yield(n)
            onNotification?(n)
        }
    }

    /// Performs one request. A daemon error arrives as `RPCError`; a lost
    /// connection as `ClientError.disconnected`; silence as `.timeout`.
    /// Cancelling the calling task fails the call with `CancellationError`
    /// (the request itself is not recalled; a late reply is dropped).
    public func call<P: Encodable & Sendable, R: Decodable & Sendable>(
        _ method: String, _ params: P, timeout: Duration = .seconds(30)
    ) async throws -> R {
        try Task.checkCancellation()
        guard let connection, case .connected = state else {
            throw ClientError.notConnected
        }
        let id = nextID
        nextID += 1
        var line = try JSONCoding.encoder().encode(Request(id: id, method: method, params: params))
        line.append(0x0A)
        let raw: Data = try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Data, any Error>) in
                pending[id] = cont
                // The cancellation handler may have run before the
                // continuation was registered; it found nothing to fail then.
                if Task.isCancelled {
                    fail(id: id, CancellationError())
                    return
                }
                timeouts[id] = Task { [weak self] in
                    try? await Task.sleep(for: timeout)
                    await self?.fail(id: id, ClientError.timeout(method: method))
                }
                connection.send(content: line, completion: .contentProcessed { [weak self] error in
                    if let error {
                        Task { await self?.fail(id: id, ClientError.transport(describe(error))) }
                    }
                })
            }
        } onCancel: {
            Task { await self.fail(id: id, CancellationError()) }
        }
        return try JSONCoding.decoder().decode(ResultEnvelope<R>.self, from: raw).result
    }

    private func fail(id: Int, _ error: any Error) {
        timeouts.removeValue(forKey: id)?.cancel()
        pending.removeValue(forKey: id)?.resume(throwing: error)
    }

    /// Drops the connection; pending calls fail with `.disconnected`.
    public func close() {
        teardown(reason: nil)
    }

    private func teardown(reason: String?) {
        // Callbacks of the connection being torn down (a receive failing
        // with "cancelled") must not reach the actor after this point.
        generation += 1
        connectDeadline?.cancel()
        connectDeadline = nil
        if let conn = connection {
            conn.stateUpdateHandler = nil
            conn.cancel()
        }
        connection = nil
        framer = LineFramer()
        for id in Array(pending.keys) {
            fail(id: id, ClientError.disconnected)
        }
        connectContinuation?.resume(throwing: ClientError.disconnected)
        connectContinuation = nil
        setState(.disconnected(reason: reason))
    }

    private func setState(_ s: State) {
        guard s != state else { return }
        state = s
        statesIn.yield(s)
        onState?(s)
    }
}

/// A short, content-free description of a transport error for the UI.
private func describe(_ error: NWError) -> String {
    switch error {
    case .posix(let code):
        return String(cString: strerror(code.rawValue))
    default:
        return String(describing: error)
    }
}
