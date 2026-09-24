// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Network
@testable import MalachiCore

/// An in-process stand-in for malachid: a unix-socket listener speaking the
/// same newline-delimited JSON-RPC. The handler decides the answer per
/// method; the whole request line is passed so a test can look at params.
actor FakeDaemon {
    typealias Handler = @Sendable (_ method: String, _ line: Data) async -> Result<Data, RPCError>

    struct IncomingRequest: Decodable {
        let id: Int
        let method: String
    }

    let path: String
    private let listener: NWListener
    private let queue = DispatchQueue(label: "fake-daemon")
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    private var framers: [ObjectIdentifier: LineFramer] = [:]
    private let handler: Handler
    private var readyContinuation: CheckedContinuation<Void, any Error>?
    private(set) var accepted = 0

    init(handler: @escaping Handler) throws {
        // Short and unique: sun_path has 104 bytes.
        let dir = FileManager.default.temporaryDirectory
        path = dir.appendingPathComponent("malachi-\(UUID().uuidString.prefix(8)).sock").path
        try? FileManager.default.removeItem(atPath: path)
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .unix(path: path)
        listener = try NWListener(using: params)
        self.handler = handler
    }

    /// Starts listening; returns once the socket is bound.
    func start() async throws {
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
            Task { await self.serve(key, line) }
        }
        if error != nil || complete {
            forget(key)
            return
        }
        receiveNext(key)
    }

    private func serve(_ key: ObjectIdentifier, _ line: Data) async {
        guard let req = try? JSONDecoder().decode(IncomingRequest.self, from: line) else { return }
        let outcome = await handler(req.method, line)
        var out = Data("{\"jsonrpc\":\"2.0\",\"id\":\(req.id),".utf8)
        switch outcome {
        case .success(let result):
            out.append(Data("\"result\":".utf8))
            out.append(result)
        case .failure(let err):
            out.append(Data("\"error\":".utf8))
            out.append(try! JSONEncoder().encode(err))
        }
        out.append(Data("}\n".utf8))
        send(key, out)
    }

    private func send(_ key: ObjectIdentifier, _ data: Data) {
        connections[key]?.send(content: data, completion: .contentProcessed { _ in })
    }

    /// Pushes a notification to every client.
    func pushNotification(method: String, paramsJSON: String) {
        let line = Data("{\"jsonrpc\":\"2.0\",\"method\":\"\(method)\",\"params\":\(paramsJSON)}\n".utf8)
        for key in connections.keys {
            send(key, line)
        }
    }

    /// Closes every client connection (the daemon went away).
    func closeAll() {
        for (key, conn) in connections {
            conn.cancel()
            framers[key] = nil
        }
        connections.removeAll()
    }

    func stop() {
        closeAll()
        listener.cancel()
        try? FileManager.default.removeItem(atPath: path)
    }

    private func forget(_ key: ObjectIdentifier) {
        connections[key]?.cancel()
        connections[key] = nil
        framers[key] = nil
    }
}

/// JSON bytes helper for handlers.
func json(_ s: String) -> Data { Data(s.utf8) }
