// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
@preconcurrency import Network

/// Loopback only HTTP/1.1 with bounded headers/body, no transfer encoding,
/// duplicate headers or pipelining. Owned sockets end with the session.
@MainActor public final class ChatGPTLoopback {
    public struct Request {
        public var method: String
        public var target: String
        public var headers: [String: String]
        public var body: Data
    }
    @MainActor public final class Response {
        let connection: NWConnection
        private(set) var headerSent = false
        init(_ connection: NWConnection) { self.connection = connection }
        public func write(_ data: Data) async throws {
            try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
                connection.send(content: data, completion: .contentProcessed { error in
                    if let error { c.resume(throwing: error) } else { c.resume() }
                })
            }
        }
        public func header(status: Int, type: String) async throws {
            headerSent = true
            try await write(Data("HTTP/1.1 \(status) Response\r\nContent-Type: \(type)\r\nConnection: close\r\nCache-Control: no-store\r\n\r\n".utf8))
        }
        func read() async throws -> Data {
            try await withCheckedThrowingContinuation { c in
                connection.receive(minimumIncompleteLength: 1, maximumLength: 8192) { data, _, done, error in
                    if let error { c.resume(throwing: error) }
                    else if let data, !data.isEmpty { c.resume(returning: data) }
                    else { c.resume(throwing: ChatGPTFailure(done ? "loopback_closed" : "loopback_empty")) }
                }
            }
        }
        func request() async throws -> Request {
            var data = Data()
            let marker = Data("\r\n\r\n".utf8)
            while data.range(of: marker) == nil {
                guard data.count < 32768 else { throw ChatGPTFailure("loopback_header_limit") }
                data.append(try await read())
            }
            guard let range = data.range(of: marker), range.lowerBound <= 32768,
                  let header = String(data: data[..<range.lowerBound], encoding: .utf8) else { throw ChatGPTFailure("loopback_header_invalid") }
            let lines = header.components(separatedBy: "\r\n")
            let first = lines[0].split(separator: " ")
            guard first.count == 3, first[2] == "HTTP/1.1" else { throw ChatGPTFailure("loopback_request_invalid") }
            var headers: [String: String] = [:]
            for line in lines.dropFirst() {
                guard let colon = line.firstIndex(of: ":") else { throw ChatGPTFailure("loopback_header_invalid") }
                let key = String(line[..<colon]).lowercased()
                guard !key.isEmpty, headers[key] == nil else { throw ChatGPTFailure("loopback_duplicate_header") }
                headers[key] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
            }
            guard headers["transfer-encoding"] == nil else { throw ChatGPTFailure("loopback_transfer_denied") }
            let length: Int
            if let raw = headers["content-length"] {
                guard !raw.isEmpty, raw.utf8.allSatisfy({ (48...57).contains($0) }), let n = Int(raw), n <= CodexPolicy.frameLimit else { throw ChatGPTFailure("loopback_body_limit") }
                length = n
            } else { length = 0 }
            var body = Data(data[range.upperBound...])
            while body.count < length { body.append(try await read()); guard body.count <= length else { throw ChatGPTFailure("loopback_pipeline_denied") } }
            guard body.count == length else { throw ChatGPTFailure("loopback_pipeline_denied") }
            return Request(method: String(first[0]), target: String(first[1]), headers: headers, body: body)
        }
    }
    public private(set) var port: UInt16 = 0
    private var listener: NWListener?
    private var clients: [UUID: NWConnection] = [:]
    private var tasks: [UUID: Task<Void, Never>] = [:]
    private var started: CheckedContinuation<Void, Error>?
    public init() {}
    public func start(_ handler: @escaping @MainActor (Request, Response) async throws -> Void) async throws {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
        let listener = try NWListener(using: parameters)
        self.listener = listener
        listener.stateUpdateHandler = { [weak self] state in
            Task { @MainActor in
                guard let self else { return }
                switch state {
                case .ready:
                    self.port = listener.port?.rawValue ?? 0
                    let c = self.started; self.started = nil; c?.resume()
                case .failed:
                    let c = self.started; self.started = nil; c?.resume(throwing: ChatGPTFailure("loopback_failed"))
                default: break
                }
            }
        }
        listener.newConnectionHandler = { [weak self] connection in
            Task { @MainActor in
                guard let self, self.clients.count < 4 else { connection.cancel(); return }
                let id = UUID(); self.clients[id] = connection
                connection.start(queue: DispatchQueue.global(qos: .userInitiated))
                self.tasks[id] = Task { @MainActor [weak self] in
                    let timeout = Task { @MainActor in
                        try? await Task.sleep(for: .seconds(180)); if !Task.isCancelled { connection.cancel() }
                    }
                    defer { timeout.cancel(); connection.cancel(); self?.clients[id] = nil; self?.tasks[id] = nil }
                    do { let response = Response(connection); try await handler(response.request(), response) } catch { /* fixed error only; discard hostile inputs */ }
                }
            }
        }
        try await withCheckedThrowingContinuation { c in started = c; listener.start(queue: DispatchQueue.global(qos: .userInitiated)) }
    }
    public func close() {
        listener?.cancel(); listener = nil
        let c = started; started = nil; c?.resume(throwing: CancellationError())
        for connection in clients.values { connection.cancel() }; clients = [:]
        for task in tasks.values { task.cancel() }; tasks = [:]
    }
}

@MainActor final class CodexInferenceGateway {
    let credential = ChatGPTOAuth.random()
    private let path = "/" + ChatGPTOAuth.random() + "/v1/responses"
    private let listener = ChatGPTLoopback()
    private let connection: ChatGPTConnection
    private let policy: CodexPolicy
    private let session: URLSession
    var baseURL: String { "http://127.0.0.1:\(listener.port)" + path.dropLast(10) }
    private(set) var failure = "codex_session_ended"
    init(connection: ChatGPTConnection, policy: CodexPolicy) {
        self.connection = connection; self.policy = policy
        let config = URLSessionConfiguration.ephemeral; config.urlCache = nil; config.httpCookieStorage = nil
        session = URLSession(configuration: config, delegate: ChatGPTNoRedirect(), delegateQueue: nil)
    }
    func start() async throws {
        try await listener.start { [weak self] request, response in
            guard let self else { return }
            do { try await self.serve(request, response) }
            catch {
                self.failure = (error as? ChatGPTFailure)?.code ?? "chatgpt_network"
                if !response.headerSent {
                    try? await response.header(status: 502, type: "application/json")
                    try? await response.write(Data("{\"error\":{\"message\":\"ChatGPT inference refused\"}}".utf8))
                }
            }
        }
    }
    private func serve(_ incoming: ChatGPTLoopback.Request, _ response: ChatGPTLoopback.Response) async throws {
        guard incoming.method == "POST", incoming.target == path,
              incoming.headers["authorization"] == "Bearer " + credential, !incoming.body.isEmpty else { throw ChatGPTFailure("codex_gate_request_denied") }
        var request = URLRequest(url: URL(string: "https://api.openai.com/v1/responses")!, timeoutInterval: 180)
        let token = try await connection.accessToken()
        request.httpMethod = "POST"; request.httpBody = try policy.filterRequest(incoming.body)
        request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        let (bytes, upstream) = try await session.bytes(for: request)
        guard let upstream = upstream as? HTTPURLResponse else { throw ChatGPTFailure("chatgpt_network") }
        guard (200..<300).contains(upstream.statusCode) else {
            failure = upstream.statusCode == 429 ? "chatgpt_usage_limit" : upstream.statusCode == 401 ? "chatgpt_reconnect_required" : "chatgpt_inference_refused"
            await connection.rejected(token, status: upstream.statusCode)
            try await response.header(status: upstream.statusCode, type: "application/json")
            try await response.write(Data("{\"error\":{\"message\":\"ChatGPT inference refused\"}}".utf8)); return
        }
        let missingType = try CodexPolicy.isMissingStreamType(upstream.value(forHTTPHeaderField: "Content-Type"))
        var line = Data(), event: [String] = [], size = 0
        for try await byte in bytes {
            try Task.checkCancellation()
            guard size < CodexPolicy.frameLimit else { throw ChatGPTFailure("codex_gate_response_limit") }; size += 1
            if byte != 10 { line.append(byte); continue }
            if line.last == 13 { line.removeLast() }
            guard let text = String(data: line, encoding: .utf8) else { throw ChatGPTFailure("codex_gate_invalid_utf8") }
            line.removeAll(keepingCapacity: true)
            if !text.isEmpty { event.append(text); continue }
            let data = event.filter { $0.hasPrefix("data:") }.map { String($0.dropFirst(5)).trimmingCharacters(in: .whitespaces) }.joined(separator: "\n")
            if !response.headerSent {
                if data.isEmpty {
                    guard event.allSatisfy({ $0.hasPrefix(":") }) else { throw ChatGPTFailure("codex_gate_non_streaming_response") }
                    event = []; size = 0
                    continue
                }
                if missingType { try policy.validateInitialStreamEvent(data) }
                else { try policy.validateEvent(data) }
                try await response.header(status: 200, type: "text/event-stream")
            } else if !data.isEmpty { try policy.validateEvent(data) }
            try await response.write(Data((event.joined(separator: "\n") + "\n\n").utf8))
            event = []; size = 0
        }
        guard response.headerSent else { throw ChatGPTFailure("codex_gate_non_streaming_response") }
        guard line.isEmpty, event.isEmpty else { throw ChatGPTFailure("codex_gate_truncated_event") }
    }
    func close() { listener.close(); session.invalidateAndCancel() }
}
