// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation
import Network
@testable import MalachiCore

/// An in-process stand-in for malachid: a unix-socket listener speaking the
/// same newline-delimited JSON-RPC, handshake included (docs/api.md §1.4).
///
/// `start()` writes a fresh key beside the socket (`keyPath`, 0600), as the
/// daemon does at every start, and the daemon's side of the handshake is
/// answered here, line by line and in order: `system.hello`, then
/// `system.authenticate`. Those lines are recorded in `handshakes`, never
/// in `calls`, so a test counting calls sees only what the client asked
/// once it was connected. Before a connection is authenticated every other
/// request is refused with 1005 `unauthenticated` and the connection is
/// closed; notifications go to authenticated connections only.
/// `setHandshake(_:)` turns the daemon's side into another daemon's, or into
/// a script (`HandshakeMode`); `restart()` and `rotateKey()` give it a new
/// key.
///
/// Answers come from per-method handlers registered with `on(_:_:)`, which
/// get the params object as JSON (empty when absent) and return the result
/// JSON or throw an `RPCError`. A method nobody registered goes to the
/// generic `Handler` of the initialiser, whose default answers
/// methodNotFound (-32601).
actor FakeDaemon {
    typealias Handler = @Sendable (_ method: String, _ line: Data) async -> Result<Data, RPCError>
    typealias MethodHandler = @Sendable (_ params: Data) async throws -> Data

    /// How the daemon's side of the handshake behaves. A connection keeps
    /// the mode it was accepted in, whatever `setHandshake` says later.
    enum HandshakeMode: Sendable {
        /// As malachid does.
        case normal
        /// A daemon of protocol 1: no handshake, methodNotFound for
        /// system.hello, and every connection served (and notified) at once.
        case oldDaemon
        /// A daemon of another protocol version: its system.hello result
        /// carries this one.
        case protocolVersion(Int)
        /// Something that does not hold the key file's key: its daemonProof
        /// is made with another key.
        case wrongProof
        /// The daemon refuses the client's proof: 1005 for
        /// system.authenticate, then it closes the connection.
        case rejectClient
        /// system.hello is read and never answered.
        case silent
        /// The daemonProof is 63 hex digits, not 64.
        case malformedProof
        /// The daemon writes what a script says instead of its answers, as
        /// the fakeDaemon of backend/pkg/api/handshake_test.go does.
        case raw(Script)
    }

    /// What a scripted daemon writes (`HandshakeMode.raw`), as exact bytes:
    /// each line with its "\n", or a line cut short.
    struct Script: Sendable {
        /// Written for system.hello, given the JSON of the right result
        /// (`helloResult`); nil writes nothing.
        var hello: @Sendable (_ rightResult: String) -> String?
        /// Closes the connection's write side after the system.hello answer.
        var closeAfterHello = false
        /// Written for a system.authenticate with the right proof instead of
        /// its answer, given the client's nonce as hex; nil writes nothing.
        var authenticate: @Sendable (_ clientNonce: String) -> String? = { _ in nil }
        /// Closes the connection's write side after that.
        var closeAfterAuthenticate = false
    }

    /// JSON-RPC's own code for an unknown method (docs/api.md §2).
    static let methodNotFoundCode: ErrorCode = .methodNotFound
    /// JSON-RPC's own code for a handler that threw something else.
    static let internalErrorCode: ErrorCode = .internalError

    static let methodNotFound: Handler = { method, _ in
        .failure(RPCError(code: methodNotFoundCode, message: "unknown method \(method)"))
    }

    struct IncomingRequest: Decodable {
        let id: Int
        let method: String
    }

    /// A connection's progress through the handshake.
    private enum Peer {
        case new
        case helloAnswered(clientNonce: Data, daemonNonce: Data)
        case authenticated
        /// Refused, or its script has run: what the client still sends is
        /// recorded and ignored.
        case done
    }

    let path: String
    /// The key file beside the socket (docs/api.md §1.4).
    let keyPath: String
    private let listener: NWListener
    private let queue = DispatchQueue(label: "fake-daemon")
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    private var framers: [ObjectIdentifier: LineFramer] = [:]
    private var peers: [ObjectIdentifier: Peer] = [:]
    /// The handshake mode each connection was accepted in.
    private var modes: [ObjectIdentifier: HandshakeMode] = [:]
    private let handler: Handler
    private var methods: [String: MethodHandler] = [:]
    private var readyContinuation: CheckedContinuation<Void, any Error>?
    /// The key in the key file, which the handshake proves with.
    private var authKey = Data()
    private var handshake: HandshakeMode = .normal
    private var notificationsBeforeHello = 0
    private var notificationWithAuthenticateAnswer: String?
    private(set) var accepted = 0
    /// Every method served, in order; the handshake's lines are not.
    private(set) var calls: [String] = []
    /// What the handshake saw, in order: the method of every line a
    /// connection sent before it was authenticated ("" for a line without
    /// one), and every later system.hello or system.authenticate.
    private(set) var handshakes: [String] = []
    /// The method of every line as it arrived, handshake and calls alike.
    private(set) var received: [String] = []
    /// Every key, nonce and proof of this daemon so far, as lowercase hex:
    /// what no error text may show (api TestHandshakeErrorsCarryNoSecrets).
    private(set) var secrets: [String] = []

    init(handler: @escaping Handler = FakeDaemon.methodNotFound) throws {
        // Short and unique: sun_path has 104 bytes.
        let dir = FileManager.default.temporaryDirectory
        let socket = dir.appendingPathComponent("malachi-\(UUID().uuidString.prefix(8)).sock").path
        path = socket
        keyPath = RPCAuth.keyPath(socket: socket)
        try? FileManager.default.removeItem(atPath: socket)
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .unix(path: socket)
        listener = try NWListener(using: params)
        self.handler = handler
    }

    /// Registers the answer for `method`; consulted before the generic
    /// handler. Registering again replaces the earlier handler.
    func on(_ method: String, _ handler: @escaping MethodHandler) {
        methods[method] = handler
    }

    /// Switches the daemon's side of the handshake for the connections
    /// accepted from now on.
    func setHandshake(_ mode: HandshakeMode) {
        handshake = mode
    }

    /// Sends `n` notifications (`notify.early`) right before each
    /// system.hello answer, in the same write, as a daemon of protocol 1
    /// broadcasting would.
    func setNotificationsBeforeHello(_ n: Int) {
        notificationsBeforeHello = n
    }

    /// Sends a notification of this method in the same write as each
    /// system.authenticate answer, right after it; nil sends none.
    func setNotificationWithAuthenticateAnswer(_ method: String?) {
        notificationWithAuthenticateAnswer = method
    }

    /// Writes the key file, then starts listening; returns once the socket
    /// is bound.
    func start() async throws {
        try rotateKey()
        listener.stateUpdateHandler = { [weak self] st in
            Task { await self?.listenerStateChanged(st) }
        }
        listener.newConnectionHandler = { [weak self] conn in
            Task { await self?.accept(conn) }
        }
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, any Error>) in
            readyContinuation = cont
            listener.start(queue: queue)
        }
    }

    /// Writes a new key to `keyPath` (0600, replaced atomically, as the
    /// daemon does); later handshakes prove with it, authenticated
    /// connections stay.
    func rotateKey() throws {
        let fresh = RPCAuth.newNonce()
        let tmp = "\(keyPath).tmp-\(UUID().uuidString.prefix(8))"
        let content = Data((RPCAuth.hex(fresh) + "\n").utf8)
        guard FileManager.default.createFile(atPath: tmp, contents: content, attributes: [.posixPermissions: 0o600]) else {
            throw CocoaError(.fileWriteUnknown)
        }
        do {
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp)
        } catch {
            try? FileManager.default.removeItem(atPath: tmp)
            throw error
        }
        guard Darwin.rename(tmp, keyPath) == 0 else {
            try? FileManager.default.removeItem(atPath: tmp)
            throw CocoaError(.fileWriteUnknown)
        }
        authKey = fresh
        secrets.append(RPCAuth.hex(fresh))
    }

    /// The daemon restarted: every connection is gone and the key file
    /// holds a new key.
    func restart() throws {
        closeAll()
        try rotateKey()
    }

    private func listenerStateChanged(_ st: NWListener.State) {
        switch st {
        case .ready:
            readyContinuation?.resume()
            readyContinuation = nil
        case .failed(let error), .waiting(let error):
            readyContinuation?.resume(throwing: error)
            readyContinuation = nil
        default:
            break
        }
    }

    private func accept(_ conn: NWConnection) {
        let key = ObjectIdentifier(conn)
        connections[key] = conn
        framers[key] = LineFramer()
        let mode = handshake
        modes[key] = mode
        // A daemon of protocol 1 has no handshake: it serves at once.
        if case .oldDaemon = mode {
            peers[key] = .authenticated
        } else {
            peers[key] = .new
        }
        accepted += 1
        conn.stateUpdateHandler = { [weak self] st in
            if case .cancelled = st { Task { await self?.forget(key) } }
            if case .failed = st { Task { await self?.forget(key) } }
        }
        conn.start(queue: queue)
        receiveNext(key)
    }

    private func receiveNext(_ key: ObjectIdentifier) {
        connections[key]?.receive(minimumIncompleteLength: 1, maximumLength: 64 << 10) { [weak self] data, _, complete, error in
            Task { await self?.didReceive(key, data, complete: complete, error: error) }
        }
    }

    private func didReceive(_ key: ObjectIdentifier, _ data: Data?, complete: Bool, error: NWError?) {
        guard connections[key] != nil else { return }
        let lines = (try? framers[key]?.append(data ?? Data())) ?? []
        for line in lines {
            let method = Self.method(of: line)
            received.append(method)
            switch peers[key] {
            case .authenticated? where method != API.SystemHello.name && method != API.SystemAuthenticate.name:
                // Calls are served concurrently, as the daemon does once a
                // connection is authenticated.
                Task { await self.serve(key, line) }
            case .done?:
                continue
            default:
                // The handshake is answered in order, line by line, in the
                // read loop, as the daemon does.
                handshakeLine(key, line, method: method)
                guard connections[key] != nil else { return }
            }
        }
        if error != nil || complete {
            forget(key)
            return
        }
        receiveNext(key)
    }

    // MARK: Handshake

    private struct HelloRequest: Decodable {
        let params: SystemHelloParams
    }

    private struct AuthenticateRequest: Decodable {
        let params: SystemAuthenticateParams
    }

    /// A line of a connection that is not authenticated, or a handshake
    /// method on one that is: docs/api.md §1.4's table, the daemon's side,
    /// in the mode the connection was accepted in.
    private func handshakeLine(_ key: ObjectIdentifier, _ line: Data, method: String) {
        handshakes.append(method)
        guard let req = try? JSONDecoder().decode(IncomingRequest.self, from: line) else {
            // Not a request with an id: closed without an answer.
            forget(key)
            return
        }
        let mode = modes[key] ?? handshake
        switch peers[key] ?? .new {
        case .authenticated:
            if case .oldDaemon = mode, req.method == API.SystemHello.name {
                var out = earlyNotifications()
                out.append(Self.response(id: req.id, error: RPCError(code: .methodNotFound, message: "unknown method \"system.hello\"")))
                send(key, out)
            } else {
                send(key, Self.response(id: req.id, error: RPCError(code: .invalidRequest, message: "already authenticated")))
            }
        case .new:
            guard req.method == API.SystemHello.name,
                  let hello = try? JSONDecoder().decode(HelloRequest.self, from: line),
                  let clientNonce = RPCAuth.decodeHex32(hello.params.clientNonce) else {
                refuse(key, req.id)
                return
            }
            answerHello(key, req.id, clientNonce: clientNonce, mode: mode)
        case .helloAnswered(let clientNonce, let daemonNonce):
            guard req.method == API.SystemAuthenticate.name,
                  let auth = try? JSONDecoder().decode(AuthenticateRequest.self, from: line),
                  let proof = RPCAuth.decodeHex32(auth.params.clientProof),
                  proof == RPCAuth.clientProof(key: authKey, clientNonce: clientNonce, daemonNonce: daemonNonce) else {
                refuse(key, req.id)
                return
            }
            switch mode {
            case .rejectClient:
                refuse(key, req.id)
            case .raw(let script):
                peers[key] = .done
                let out = Data((script.authenticate(RPCAuth.hex(clientNonce)) ?? "").utf8)
                write(key, out, close: script.closeAfterAuthenticate)
            default:
                // Authenticated in the same step as the answer is written:
                // no notification can go out before it.
                peers[key] = .authenticated
                var out = Self.response(id: req.id, result: Data("{}".utf8))
                if let m = notificationWithAuthenticateAnswer {
                    out.append(Self.notification(m, "{}"))
                }
                send(key, out)
            }
        case .done:
            break
        }
    }

    private func answerHello(_ key: ObjectIdentifier, _ id: Int, clientNonce: Data, mode: HandshakeMode) {
        let daemonNonce = RPCAuth.newNonce()
        let rightProof = RPCAuth.daemonProof(key: authKey, clientNonce: clientNonce, daemonNonce: daemonNonce)
        let clientProof = RPCAuth.clientProof(key: authKey, clientNonce: clientNonce, daemonNonce: daemonNonce)
        secrets += [RPCAuth.hex(clientNonce), RPCAuth.hex(daemonNonce), RPCAuth.hex(rightProof), RPCAuth.hex(clientProof)]
        var version = API.protocolVersion
        var proof = RPCAuth.hex(rightProof)
        switch mode {
        case .normal, .rejectClient, .oldDaemon:
            break
        case .protocolVersion(let v):
            version = v
        case .wrongProof:
            let otherKey = RPCAuth.newNonce()
            proof = RPCAuth.hex(RPCAuth.daemonProof(key: otherKey, clientNonce: clientNonce, daemonNonce: daemonNonce))
            secrets += [RPCAuth.hex(otherKey), proof]
        case .malformedProof:
            proof = String(proof.dropLast())
        case .silent:
            return
        case .raw(let script):
            peers[key] = .helloAnswered(clientNonce: clientNonce, daemonNonce: daemonNonce)
            let right = Self.helloResult(version: version, daemonNonce: daemonNonce, proof: proof)
            write(key, Data((script.hello(right) ?? "").utf8), close: script.closeAfterHello)
            return
        }
        peers[key] = .helloAnswered(clientNonce: clientNonce, daemonNonce: daemonNonce)
        var out = earlyNotifications()
        out.append(Self.response(id: id, result: Data(Self.helloResult(version: version, daemonNonce: daemonNonce, proof: proof).utf8)))
        send(key, out)
    }

    /// The system.hello result as the daemon writes it.
    static func helloResult(version: Int, daemonNonce: Data, proof: String) -> String {
        #"{"protocolVersion":\#(version),"daemonNonce":"\#(RPCAuth.hex(daemonNonce))","daemonProof":"\#(proof)"}"#
    }

    /// The notifications `setNotificationsBeforeHello` asked for.
    private func earlyNotifications() -> Data {
        var out = Data()
        for _ in 0..<notificationsBeforeHello {
            out.append(Self.notification("notify.early", "{}"))
        }
        return out
    }

    /// Refuses a request before authentication, as the daemon does: 1005
    /// with its id, then the connection is closed (here its write side, so
    /// the answer is delivered first).
    private func refuse(_ key: ObjectIdentifier, _ id: Int) {
        write(key, Self.response(id: id, error: RPCError(code: .unauthenticated, message: "unauthenticated")), close: true)
    }

    /// Writes `data` (nothing when empty) and, when asked, closes the
    /// connection's write side after it; a closed connection is done.
    private func write(_ key: ObjectIdentifier, _ data: Data, close: Bool) {
        if close {
            peers[key] = .done
            connections[key]?.send(
                content: data.isEmpty ? nil : data, contentContext: .finalMessage, isComplete: true,
                completion: .contentProcessed { _ in })
        } else if !data.isEmpty {
            send(key, data)
        }
    }

    // MARK: Serving

    private func serve(_ key: ObjectIdentifier, _ line: Data) async {
        guard let req = try? JSONDecoder().decode(IncomingRequest.self, from: line) else { return }
        calls.append(req.method)
        let outcome: Result<Data, RPCError>
        if let method = methods[req.method] {
            do {
                outcome = .success(try await method(Self.paramsJSON(of: line)))
            } catch let err as RPCError {
                outcome = .failure(err)
            } catch {
                outcome = .failure(RPCError(code: Self.internalErrorCode, message: "\(error)"))
            }
        } else {
            outcome = await handler(req.method, line)
        }
        switch outcome {
        case .success(let result):
            send(key, Self.response(id: req.id, result: result))
        case .failure(let err):
            send(key, Self.response(id: req.id, error: err))
        }
    }

    /// The `params` member of a request line re-serialised on its own, or
    /// empty when the request has none.
    static func paramsJSON(of line: Data) -> Data {
        guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
              let params = obj["params"] else { return Data() }
        return (try? JSONSerialization.data(withJSONObject: params, options: [.sortedKeys, .fragmentsAllowed])) ?? Data()
    }

    private struct MethodOnly: Decodable {
        let method: String?
    }

    /// The `method` of a line, "" when it has none.
    static func method(of line: Data) -> String {
        (try? JSONDecoder().decode(MethodOnly.self, from: line))?.method ?? ""
    }

    static func response(id: Int, result: Data) -> Data {
        var out = Data("{\"jsonrpc\":\"2.0\",\"id\":\(id),\"result\":".utf8)
        out.append(result)
        out.append(Data("}\n".utf8))
        return out
    }

    static func response(id: Int, error: RPCError) -> Data {
        var out = Data("{\"jsonrpc\":\"2.0\",\"id\":\(id),\"error\":".utf8)
        out.append(try! JSONEncoder().encode(error))
        out.append(Data("}\n".utf8))
        return out
    }

    static func notification(_ method: String, _ paramsJSON: String) -> Data {
        Data("{\"jsonrpc\":\"2.0\",\"method\":\"\(method)\",\"params\":\(paramsJSON)}\n".utf8)
    }

    private func send(_ key: ObjectIdentifier, _ data: Data) {
        connections[key]?.send(content: data, completion: .contentProcessed { _ in })
    }

    /// Pushes a notification to every authenticated client, as the daemon
    /// does.
    func pushNotification(method: String, paramsJSON: String) {
        let line = Self.notification(method, paramsJSON)
        for (key, peer) in peers {
            if case .authenticated = peer {
                send(key, line)
            }
        }
    }

    /// Closes every client connection (the daemon went away).
    func closeAll() {
        for (key, conn) in connections {
            conn.cancel()
            framers[key] = nil
            peers[key] = nil
            modes[key] = nil
        }
        connections.removeAll()
    }

    func stop() {
        closeAll()
        listener.cancel()
        try? FileManager.default.removeItem(atPath: path)
        try? FileManager.default.removeItem(atPath: keyPath)
    }

    private func forget(_ key: ObjectIdentifier) {
        connections[key]?.cancel()
        connections[key] = nil
        framers[key] = nil
        peers[key] = nil
        modes[key] = nil
    }
}

/// JSON bytes helper for handlers.
func json(_ s: String) -> Data { Data(s.utf8) }
