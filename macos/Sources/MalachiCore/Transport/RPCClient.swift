// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Network

/// A JSON-RPC 2.0 client for the malachid unix socket, the Swift counterpart
/// of ui/internal/client. It is transport only: it sends requests, matches
/// responses by id and forwards notifications. Reconnecting is the caller's
/// job; `connect()` is single-shot.
///
/// Every connection is authenticated before it is used (docs/api.md §1.4,
/// the port of api.ClientHandshake): `connect()` runs the handshake between
/// Network.framework's `.ready` and `.connected`, and `state` stays
/// `.connecting` meanwhile, so `call` refuses with `.notConnected` until
/// both ends proved that they hold the daemon's key. The key file is read
/// afresh on every connection and never kept.
///
/// The actor owns the `NWConnection`, which never leaves it. Every framework
/// callback captures only the actor and value types and hops back in with a
/// `Task`; a generation counter discards callbacks of a connection that has
/// been torn down meanwhile, and the handshake compares it after every
/// `await`, since the actor is reentrant (`close()` or a failed connection
/// can run in between). Exactly one receive is in flight at any time and
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

    /// Why `connect()` refused a daemon it reached (api.HandshakeError): one
    /// it cannot or must not use. Kept apart from `ClientError`, which is what
    /// calls fail with. No description carries the key, a nonce or a proof.
    public enum HandshakeError: Error, Sendable, Equatable, CustomStringConvertible {
        /// The daemon speaks another protocol version; 1 for a daemon that
        /// does not know system.hello. The key file was not read and nothing
        /// was sent after system.hello.
        case protocolMismatch(daemon: Int)
        /// The key file is missing, not a key file, or not safe to use
        /// (`DaemonKey`); the reason names the path, never the content.
        case keyUnavailable(String)
        /// Whatever answers on the socket did not prove that it holds the
        /// key of the key file: another process, or a daemon of another run.
        /// Nothing was sent after system.hello.
        case daemonUnproven
        /// The daemon answered a handshake call with this error
        /// (unauthenticated for a proof it refused); its message is dropped.
        case rejected(ErrorCode)
        /// An answer broke the protocol; the detail is fixed text.
        case malformed(String)
        /// No complete answer within the handshake timeout.
        case timedOut

        public var description: String {
            switch self {
            case .protocolMismatch(let daemon):
                return "malachid speaks protocol version \(daemon), this client \(API.protocolVersion)"
            case .keyUnavailable(let reason):
                return "cannot use malachid's connection key: \(reason)"
            case .daemonUnproven:
                return "the process on the socket did not prove it holds malachid's connection key"
            case .rejected(let code):
                return "malachid rejected the handshake (\(code.name))"
            case .malformed(let detail):
                return "malformed handshake answer from malachid: \(detail)"
            case .timedOut:
                return "malachid did not complete the handshake in time"
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
    private let handshakeTimeout: Duration
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

    // The handshake of the current connection. While `handshaking`, what the
    // daemon sends is kept raw in `handshakeBytes` and read line by line by
    // the handshake alone, like the bufio.Reader of api.ClientHandshake;
    // whatever follows the system.authenticate answer goes to the framer
    // once the connection is `.connected`.
    private var handshaking = false
    private var handshakeBytes: [UInt8] = []
    /// Why no more bytes will come: the daemon closed the connection, or a
    /// read or send failed.
    private var handshakeEnded: String?
    private var handshakeTimedOut = false
    /// The handshake waiting for bytes, woken by every change of the three
    /// above and by `teardown`.
    private var handshakeWaiter: CheckedContinuation<Void, any Error>?
    private var handshakeDeadline: Task<Void, Never>?

    /// - Parameters:
    ///   - socketPath: the daemon's socket; its key file lies beside it
    ///     (`RPCAuth.keyPath`).
    ///   - handshakeTimeout: bounds the whole handshake of each connection;
    ///     tests shorten it.
    public init(socketPath: String, handshakeTimeout: Duration = RPCTimeouts.handshake) {
        self.socketPath = socketPath
        self.handshakeTimeout = handshakeTimeout
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

    /// Dials the socket and authenticates the connection. Returns once it is
    /// usable; throws `ClientError` when nothing answers or the connection
    /// breaks, and `HandshakeError` for a daemon that cannot or must not be
    /// used. A connected client returns at once.
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
        handshaking = true
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
        // The socket is open; the connection is usable only once both ends
        // proved that they hold the daemon's key. `state` stays .connecting.
        try await handshake(gen: gen)
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
            guard let cont = connectContinuation else { return }
            connectDeadline?.cancel()
            connectDeadline = nil
            connectContinuation = nil
            cont.resume()
            // The handshake's answers arrive through the same receive loop
            // as everything after them.
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
        if handshaking {
            handshakeReceived(data, complete: complete, error: error, gen: gen)
            return
        }
        guard consume(data ?? Data()) else {
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

    /// Frames a chunk and dispatches its lines; false when the connection
    /// was torn down for a line over the cap.
    private func consume(_ chunk: Data) -> Bool {
        do {
            for line in try framer.append(chunk) {
                dispatch(line)
            }
        } catch {
            teardown(reason: "a line from malachid exceeds \(LineFramer.defaultMaxLine) bytes")
            return false
        }
        return true
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
        handshakeDeadline?.cancel()
        handshakeDeadline = nil
        if let conn = connection {
            conn.stateUpdateHandler = nil
            conn.cancel()
        }
        connection = nil
        framer = LineFramer()
        handshaking = false
        handshakeBytes = []
        handshakeEnded = nil
        handshakeTimedOut = false
        for id in Array(pending.keys) {
            fail(id: id, ClientError.disconnected)
        }
        connectContinuation?.resume(throwing: ClientError.disconnected)
        connectContinuation = nil
        let waiter = handshakeWaiter
        handshakeWaiter = nil
        waiter?.resume(throwing: reason.map { ClientError.transport($0) } ?? ClientError.disconnected)
        setState(.disconnected(reason: reason))
    }

    private func setState(_ s: State) {
        guard s != state else { return }
        state = s
        statesIn.yield(s)
        onState?(s)
    }

    // MARK: Handshake (docs/api.md §1.4, api.ClientHandshake)

    /// Authenticates the connection of generation `gen` and makes it
    /// usable, or tears it down and throws. The whole exchange is bounded by
    /// `handshakeTimeout`.
    private func handshake(gen: Int) async throws {
        guard gen == generation else {
            throw ClientError.disconnected
        }
        let timeout = handshakeTimeout
        handshakeDeadline = Task { [weak self] in
            do {
                try await Task.sleep(for: timeout)
            } catch {
                return // cancelled: the handshake ended first
            }
            await self?.handshakeTimerFired(gen: gen)
        }
        do {
            try await exchangeProofs(gen: gen)
        } catch {
            if gen == generation {
                teardown(reason: Self.reason(error))
            }
            throw error
        }
        guard gen == generation else {
            throw ClientError.disconnected
        }
        handshakeDeadline?.cancel()
        handshakeDeadline = nil
        handshaking = false
        let rest = handshakeBytes
        let ended = handshakeEnded
        handshakeBytes = []
        handshakeEnded = nil
        handshakeTimedOut = false
        setState(.connected)
        // What the daemon sent after the system.authenticate answer, held
        // until now: notifications reach the consumer after .connected, in
        // the daemon's order.
        if !rest.isEmpty, !consume(Data(rest)) {
            return
        }
        if let ended {
            // The daemon closed the connection right after answering; the
            // receive loop has stopped already.
            teardown(reason: ended)
        }
    }

    /// The two exchanges, in the order api.ClientHandshake has them: hello,
    /// the version before anything else, the key file read afresh, the
    /// daemon's proof, then the client's. On any failure nothing more is
    /// sent; the caller tears the connection down.
    private func exchangeProofs(gen: Int) async throws {
        let clientNonce = RPCAuth.newNonce()
        try sendHandshake(
            id: 1, method: API.SystemHello.name, params: SystemHelloParams(clientNonce: RPCAuth.hex(clientNonce)), gen: gen
        )
        let result: JSONValue
        let hello = try await handshakeAnswer(id: 1, skip: RPCAuth.maxSkippedNotifications, gen: gen)
        guard gen == generation else {
            throw ClientError.disconnected
        }
        switch hello {
        case .error(let code) where code == .methodNotFound:
            // A daemon of protocol 1 does not know system.hello.
            throw HandshakeError.protocolMismatch(daemon: 1)
        case .error(let code):
            throw HandshakeError.rejected(code)
        case .result(let r):
            result = r
        }

        // protocolVersion first and on its own: it is the one member of the
        // result that every protocol version keeps, whatever the others are.
        guard let version = result["protocolVersion"]?.intValue, version > 0 else {
            throw HandshakeError.malformed("the system.hello result has no valid protocolVersion")
        }
        guard version == API.protocolVersion else {
            throw HandshakeError.protocolMismatch(daemon: version)
        }
        guard let nonceText = helloMember(result, "daemonNonce"), let proofText = helloMember(result, "daemonProof") else {
            throw HandshakeError.malformed("the system.hello result does not decode")
        }
        guard let daemonNonce = RPCAuth.decodeHex32(nonceText) else {
            throw HandshakeError.malformed("daemonNonce is not 64 lowercase hex digits")
        }
        guard let daemonProof = RPCAuth.decodeHex32(proofText) else {
            throw HandshakeError.malformed("daemonProof is not 64 lowercase hex digits")
        }

        // Only now, and on every connection: a restarted daemon has a new key.
        let key: Data
        do {
            key = try DaemonKey.read(RPCAuth.keyPath(socket: socketPath))
        } catch let e as DaemonKey.Unavailable {
            throw HandshakeError.keyUnavailable(e.reason)
        }
        guard RPCAuth.isValidDaemonProof(daemonProof, key: key, clientNonce: clientNonce, daemonNonce: daemonNonce) else {
            throw HandshakeError.daemonUnproven
        }

        let proof = RPCAuth.clientProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce)
        try sendHandshake(
            id: 2, method: API.SystemAuthenticate.name, params: SystemAuthenticateParams(clientProof: RPCAuth.hex(proof)), gen: gen
        )
        let done = try await handshakeAnswer(id: 2, skip: 0, gen: gen)
        guard gen == generation else {
            throw ClientError.disconnected
        }
        switch done {
        case .error(let code):
            throw HandshakeError.rejected(code)
        case .result(let r):
            guard r.objectValue != nil else {
                throw HandshakeError.malformed("the system.authenticate result is not an object")
            }
        }
    }

    /// Sends one handshake request on the connection of generation `gen`,
    /// the private way in while `call` still refuses. A failed send ends the
    /// handshake.
    private func sendHandshake<P: Encodable>(id: Int, method: String, params: P, gen: Int) throws {
        guard gen == generation, let connection else {
            throw ClientError.disconnected
        }
        // Nothing is written past the deadline, as a Go connection past its
        // deadline writes nothing, even when an answer read in time is
        // still being worked through.
        guard !handshakeTimedOut else {
            throw HandshakeError.timedOut
        }
        var line = try JSONEncoder().encode(Request(id: id, method: method, params: params))
        line.append(0x0A)
        connection.send(content: line, completion: .contentProcessed { [weak self] error in
            if let error {
                Task { await self?.handshakeSendFailed(describe(error), gen: gen) }
            }
        })
    }

    private enum HandshakeAnswer {
        case result(JSONValue)
        case error(ErrorCode)
    }

    /// The daemon's answer to the handshake request `id`, skipping at most
    /// `skip` notifications before it (api `answer`). Every other line
    /// breaks the protocol.
    private func handshakeAnswer(id: Int, skip: Int, gen: Int) async throws -> HandshakeAnswer {
        var skip = skip
        while true {
            let raw = try await handshakeLine(gen: gen)
            guard let m = try? JSONDecoder().decode(HandshakeLine.self, from: raw), m.jsonrpc == "2.0" else {
                throw HandshakeError.malformed("a line is not a JSON-RPC 2.0 message")
            }
            if m.isNotification {
                guard skip > 0 else {
                    throw HandshakeError.malformed("an unexpected notification")
                }
                skip -= 1
                continue
            }
            guard (m.method ?? "").isEmpty else {
                throw HandshakeError.malformed("a request instead of an answer")
            }
            guard m.id == .number(id) else {
                throw HandshakeError.malformed("an answer without the request's id")
            }
            if let code = m.errorCode {
                guard case .absent = m.result else {
                    throw HandshakeError.malformed("an answer with both a result and an error")
                }
                return .error(code)
            }
            guard case .value(let value) = m.result else {
                throw HandshakeError.malformed("an answer without a result")
            }
            return .result(value)
        }
    }

    /// The next line from the daemon during the handshake, without its
    /// "\n". Complete lines already received come first, then whatever
    /// ended the connection or the handshake's time.
    private func handshakeLine(gen: Int) async throws -> Data {
        while true {
            guard gen == generation else {
                throw ClientError.disconnected
            }
            if let line = try takeHandshakeLine() {
                return line
            }
            if let ended = handshakeEnded {
                throw ClientError.transport(ended)
            }
            if handshakeTimedOut {
                throw HandshakeError.timedOut
            }
            try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, any Error>) in
                handshakeWaiter = cont
            }
        }
    }

    /// Takes the first complete line off `handshakeBytes`, nil while there
    /// is none. A line over `RPCAuth.maxHandshakeLine`, finished or not,
    /// breaks the protocol.
    private func takeHandshakeLine() throws -> Data? {
        guard let nl = handshakeBytes.firstIndex(of: 0x0A) else {
            if handshakeBytes.count > RPCAuth.maxHandshakeLine {
                throw HandshakeError.malformed("a line is longer than 64 KiB")
            }
            return nil
        }
        if nl + 1 > RPCAuth.maxHandshakeLine {
            throw HandshakeError.malformed("a line is longer than 64 KiB")
        }
        let line = Data(handshakeBytes[..<nl])
        handshakeBytes.removeFirst(nl + 1)
        return line
    }

    /// A chunk while the handshake runs: kept for it, and the handshake
    /// woken. The receive loop goes on until the connection ends. Past the
    /// deadline nothing more is taken, as a Go connection past its deadline
    /// reads nothing: only lines that came in time can still be answers.
    private func handshakeReceived(_ data: Data?, complete: Bool, error: NWError?, gen: Int) {
        if let data, !handshakeTimedOut {
            handshakeBytes.append(contentsOf: data)
        }
        if let error {
            handshakeEnded = handshakeEnded ?? describe(error)
        } else if complete {
            handshakeEnded = handshakeEnded ?? "connection closed by malachid"
        }
        wakeHandshake()
        if handshakeEnded == nil {
            receiveNext(gen: gen)
        }
    }

    private func handshakeSendFailed(_ reason: String, gen: Int) {
        guard gen == generation, handshaking else { return }
        handshakeEnded = handshakeEnded ?? reason
        wakeHandshake()
    }

    private func handshakeTimerFired(gen: Int) {
        guard gen == generation, handshaking else { return }
        handshakeTimedOut = true
        wakeHandshake()
    }

    private func wakeHandshake() {
        let waiter = handshakeWaiter
        handshakeWaiter = nil
        waiter?.resume()
    }

    /// A string member of the system.hello result as Go decodes it into a
    /// string field: absent or null is "", a string is itself, anything
    /// else does not decode (nil).
    private func helloMember(_ result: JSONValue, _ name: String) -> String? {
        switch result[name] {
        case .none, .some(.null):
            return ""
        case .some(.string(let s)):
            return s
        default:
            return nil
        }
    }

    /// The reason a failed connect leaves in `state`.
    private static func reason(_ error: any Error) -> String {
        switch error {
        case let e as HandshakeError:
            return e.description
        case let e as ClientError:
            return e.description
        default:
            return String(describing: error)
        }
    }
}

/// One line from the daemon during the handshake, as api.ClientHandshake
/// reads it (its handshakeLine): the members that tell an answer from a
/// notification and a result from an error. A member of the wrong type
/// fails the decoding, as it fails Go's.
private struct HandshakeLine: Decodable {
    enum ID: Equatable {
        case absent, null, number(Int), other
    }

    /// A member that may be missing, null, or a value.
    enum Member {
        case absent, null, value(JSONValue)
    }

    let jsonrpc: String?
    let id: ID
    let method: String?
    let result: Member
    /// The error's code; nil without an error object (absent or null).
    let errorCode: ErrorCode?

    private enum CodingKeys: String, CodingKey {
        case jsonrpc, id, method, result, error
    }

    /// api.Error as far as the handshake reads it: the code (0 when
    /// missing, as Go leaves it); a message of another type is malformed.
    private struct ErrorObject: Decodable {
        let code: Int?
        let message: String?
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jsonrpc = try c.decodeIfPresent(String.self, forKey: .jsonrpc)
        method = try c.decodeIfPresent(String.self, forKey: .method)
        if !c.contains(.id) {
            id = .absent
        } else if try c.decodeNil(forKey: .id) {
            id = .null
        } else if let n = try? c.decode(Int.self, forKey: .id) {
            id = .number(n)
        } else {
            id = .other
        }
        if !c.contains(.result) {
            result = .absent
        } else if try c.decodeNil(forKey: .result) {
            result = .null
        } else {
            let value = try c.decode(JSONValue.self, forKey: .result)
            result = .value(value)
        }
        var code: ErrorCode?
        if c.contains(.error) {
            let isNull = try c.decodeNil(forKey: .error)
            if !isNull {
                let e = try c.decode(ErrorObject.self, forKey: .error)
                code = ErrorCode(rawValue: e.code ?? 0)
            }
        }
        errorCode = code
    }

    /// A notification: a method and no id, or a null one.
    var isNotification: Bool {
        (id == .absent || id == .null) && !(method ?? "").isEmpty
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
