// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// RPCClient's handshake against a scripted daemon (FakeDaemon's `.raw`),
// ported from backend/pkg/api/handshake_test.go: the failure table
// (failCases, droppedScripts, the timeouts) with Go's outcomes, including
// that the client sends nothing after the line Go stops at; that no text
// of a failure shows a key, a nonce or a proof
// (TestHandshakeErrorsCarryNoSecrets); and the texts themselves
// (TestHandshakeErrorText).

/// A line as the daemon writes it.
private func line(_ s: String) -> String {
    s + "\n"
}

/// The answer to the request `id` with this result.
private func answer(_ id: Int, _ result: String) -> String {
    line(#"{"jsonrpc":"2.0","id":\#(id),"result":\#(result)}"#)
}

/// The error answer to the request `id`.
private func errorAnswer(_ id: Int, _ code: ErrorCode, _ message: String) -> String {
    line(#"{"jsonrpc":"2.0","id":\#(id),"error":{"code":\#(code.rawValue),"message":"\#(message)"}}"#)
}

/// A notification as a daemon of protocol 1 broadcasts it to everyone.
private let testNotification = line(#"{"jsonrpc":"2.0","method":"notify.accountsChanged","params":{}}"#)

/// How a case ends, as Go's table says.
private enum Outcome {
    /// connect() refused the daemon with this error.
    case refused(RPCClient.HandshakeError)
    /// keyUnavailable, its reason ending in this text (the path comes first).
    case keyUnavailable(String)
    /// The connection broke: a ClientError, where Go has a plain error.
    case broken
}

/// What is done to the key file before the client dials.
private enum KeyFile {
    case keep, remove, uppercase, crlf
}

private struct FailCase {
    let name: String
    let script: FakeDaemon.Script
    var keyFile: KeyFile = .keep
    let outcome: Outcome
    /// The methods of the lines the client sent (Go's `sent`), and nothing
    /// after them.
    let sent: [String]
}

private func failCases() -> [FailCase] {
    let hello = [API.SystemHello.name]
    let both = [API.SystemHello.name, API.SystemAuthenticate.name]
    let rightHello: @Sendable (String) -> String? = { answer(1, $0) }
    let someHex = String(repeating: "ab", count: 32)
    let version = API.protocolVersion
    let big = line(#"{"jsonrpc":"2.0","method":"notify.big","params":{"p":""# + String(repeating: "a", count: 64 << 10) + #""}}"#)
    return [
        // Other protocol versions: nothing is sent after system.hello, and
        // the key file is not read.
        FailCase(name: "protocol 1",
                 script: .init(hello: { _ in errorAnswer(1, .methodNotFound, "unknown method") }),
                 outcome: .refused(.protocolMismatch(daemon: 1)), sent: hello),
        FailCase(name: "protocol 1 after 8 notifications",
                 script: .init(hello: { _ in String(repeating: testNotification, count: 8) + errorAnswer(1, .methodNotFound, "unknown method") }),
                 outcome: .refused(.protocolMismatch(daemon: 1)), sent: hello),
        FailCase(name: "protocol 99 without a key file",
                 script: .init(hello: { _ in answer(1, #"{"protocolVersion":99}"#) }),
                 keyFile: .remove, outcome: .refused(.protocolMismatch(daemon: 99)), sent: hello),
        FailCase(name: "protocol 3 with another result",
                 script: .init(hello: { _ in answer(1, #"{"protocolVersion":3,"daemonNonce":{"bytes":32}}"#) }),
                 keyFile: .remove, outcome: .refused(.protocolMismatch(daemon: 3)), sent: hello),

        // The key file.
        FailCase(name: "missing key file", script: .init(hello: rightHello), keyFile: .remove,
                 outcome: .keyUnavailable("does not exist"), sent: hello),
        FailCase(name: "upper-case key file", script: .init(hello: rightHello), keyFile: .uppercase,
                 outcome: .keyUnavailable("is not a key file"), sent: hello),
        FailCase(name: "key file with CRLF", script: .init(hello: rightHello), keyFile: .crlf,
                 outcome: .keyUnavailable("is not 65 bytes"), sent: hello),

        // Malformed system.hello answers.
        FailCase(name: "result and error",
                 script: .init(hello: { line(#"{"jsonrpc":"2.0","id":1,"result":\#($0),"error":{"code":1005,"message":"no"}}"#) }),
                 outcome: .refused(.malformed("an answer with both a result and an error")), sent: hello),
        FailCase(name: "result null",
                 script: .init(hello: { _ in line(#"{"jsonrpc":"2.0","id":1,"result":null}"#) }),
                 outcome: .refused(.malformed("an answer without a result")), sent: hello),
        FailCase(name: "neither result nor error",
                 script: .init(hello: { _ in line(#"{"jsonrpc":"2.0","id":1}"#) }),
                 outcome: .refused(.malformed("an answer without a result")), sent: hello),
        FailCase(name: "result a number",
                 script: .init(hello: { _ in answer(1, "2") }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "result an array",
                 script: .init(hello: { _ in answer(1, "[2]") }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "result a string",
                 script: .init(hello: { _ in answer(1, #""ok""#) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "protocolVersion 0",
                 script: .init(hello: { answer(1, $0.replacingOccurrences(of: #""protocolVersion":\#(version)"#, with: #""protocolVersion":0"#)) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "negative protocolVersion",
                 script: .init(hello: { answer(1, $0.replacingOccurrences(of: #""protocolVersion":\#(version)"#, with: #""protocolVersion":-2"#)) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "protocolVersion a string",
                 script: .init(hello: { answer(1, $0.replacingOccurrences(of: #""protocolVersion":\#(version)"#, with: #""protocolVersion":"\#(version)""#)) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "protocolVersion a fraction",
                 script: .init(hello: { answer(1, $0.replacingOccurrences(of: #""protocolVersion":\#(version)"#, with: #""protocolVersion":2.5"#)) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "missing protocolVersion",
                 script: .init(hello: { _ in answer(1, #"{"daemonNonce":"\#(someHex)","daemonProof":"\#(someHex)"}"#) }),
                 outcome: .refused(.malformed("the system.hello result has no valid protocolVersion")), sent: hello),
        FailCase(name: "daemonProof a number",
                 script: .init(hello: { _ in answer(1, #"{"protocolVersion":\#(version),"daemonNonce":"\#(someHex)","daemonProof":5}"#) }),
                 outcome: .refused(.malformed("the system.hello result does not decode")), sent: hello),
        FailCase(name: "wrong id",
                 script: .init(hello: { line(#"{"jsonrpc":"2.0","id":7,"result":\#($0)}"#) }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: hello),
        FailCase(name: "id a string",
                 script: .init(hello: { line(#"{"jsonrpc":"2.0","id":"1","result":\#($0)}"#) }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: hello),
        FailCase(name: "missing id",
                 script: .init(hello: { line(#"{"jsonrpc":"2.0","result":\#($0)}"#) }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: hello),
        FailCase(name: "null id",
                 script: .init(hello: { line(#"{"jsonrpc":"2.0","id":null,"result":\#($0)}"#) }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: hello),
        FailCase(name: "no jsonrpc member",
                 script: .init(hello: { line(#"{"id":1,"result":\#($0)}"#) }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "JSON-RPC 1.0",
                 script: .init(hello: { line(#"{"jsonrpc":"1.0","id":1,"result":\#($0)}"#) }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "a request",
                 script: .init(hello: { _ in line(#"{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{}}"#) }),
                 outcome: .refused(.malformed("a request instead of an answer")), sent: hello),
        FailCase(name: "a JSON array",
                 script: .init(hello: { line(#"[{"jsonrpc":"2.0","id":1,"result":\#($0)}]"#) }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "not JSON",
                 script: .init(hello: { _ in line("hello") }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "JSON null",
                 script: .init(hello: { _ in line("null") }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "an empty line",
                 script: .init(hello: { _ in line("") }),
                 outcome: .refused(.malformed("a line is not a JSON-RPC 2.0 message")), sent: hello),
        FailCase(name: "an empty message",
                 script: .init(hello: { _ in line(#"{"jsonrpc":"2.0"}"#) }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: hello),
        FailCase(name: "9 notifications",
                 script: .init(hello: { String(repeating: testNotification, count: 9) + answer(1, $0) }),
                 outcome: .refused(.malformed("an unexpected notification")), sent: hello),
        FailCase(name: "a line over 64 KiB",
                 script: .init(hello: { big + answer(1, $0) }),
                 outcome: .refused(.malformed("a line is longer than 64 KiB")), sent: hello),

        // Answers other than success.
        FailCase(name: "system.hello rejected",
                 script: .init(hello: { _ in errorAnswer(1, .unauthenticated, "no") }),
                 outcome: .refused(.rejected(.unauthenticated)), sent: hello),
        FailCase(name: "system.hello invalidRequest",
                 script: .init(hello: { _ in errorAnswer(1, .invalidRequest, "already authenticated") }),
                 outcome: .refused(.rejected(.invalidRequest)), sent: hello),
        // The message is dropped; it quotes the client's nonce to show that.
        FailCase(name: "system.authenticate rejected",
                 script: .init(hello: rightHello, authenticate: { errorAnswer(2, .unauthenticated, "wrong proof for " + $0) }),
                 outcome: .refused(.rejected(.unauthenticated)), sent: both),
        FailCase(name: "notification before the system.authenticate answer",
                 script: .init(hello: rightHello, authenticate: { _ in testNotification + answer(2, "{}") }),
                 outcome: .refused(.malformed("an unexpected notification")), sent: both),
        FailCase(name: "system.authenticate result null",
                 script: .init(hello: rightHello, authenticate: { _ in line(#"{"jsonrpc":"2.0","id":2,"result":null}"#) }),
                 outcome: .refused(.malformed("an answer without a result")), sent: both),
        FailCase(name: "system.authenticate without a result",
                 script: .init(hello: rightHello, authenticate: { _ in line(#"{"jsonrpc":"2.0","id":2}"#) }),
                 outcome: .refused(.malformed("an answer without a result")), sent: both),
        FailCase(name: "system.authenticate result not an object",
                 script: .init(hello: rightHello, authenticate: { _ in answer(2, "true") }),
                 outcome: .refused(.malformed("the system.authenticate result is not an object")), sent: both),
        FailCase(name: "system.authenticate answered with id 1",
                 script: .init(hello: rightHello, authenticate: { _ in answer(1, "{}") }),
                 outcome: .refused(.malformed("an answer without the request's id")), sent: both),

        // The connection ends (droppedScripts): a plain error, handled like
        // any dropped connection.
        FailCase(name: "closed after hello",
                 script: .init(hello: { _ in nil }, closeAfterHello: true),
                 outcome: .broken, sent: hello),
        FailCase(name: "closed mid-answer",
                 script: .init(hello: { _ in #"{"jsonrpc":"2.0","id":1,"res"# }, closeAfterHello: true),
                 outcome: .broken, sent: hello),
        FailCase(name: "closed after authenticate",
                 script: .init(hello: rightHello, closeAfterAuthenticate: true),
                 outcome: .broken, sent: both),

        // Silence (TestHandshakeTimesOut).
        FailCase(name: "silent after hello",
                 script: .init(hello: { _ in nil }),
                 outcome: .refused(.timedOut), sent: hello),
        FailCase(name: "silent after authenticate",
                 script: .init(hello: rightHello),
                 outcome: .refused(.timedOut), sent: both),
    ]
}

/// Changes the key file as a case asks, keeping it private (0600) so that
/// only its content or its absence is the problem.
private func edit(_ keyFile: KeyFile, at path: String) throws {
    let content: String
    switch keyFile {
    case .keep:
        return
    case .remove:
        try FileManager.default.removeItem(atPath: path)
        return
    case .uppercase:
        let key = try Data(contentsOf: URL(fileURLWithPath: path))
        content = String(decoding: key, as: UTF8.self).uppercased()
    case .crlf:
        let key = try Data(contentsOf: URL(fileURLWithPath: path))
        content = String(decoding: key.prefix(64), as: UTF8.self) + "\r\n"
    }
    guard FileManager.default.createFile(atPath: path, contents: Data(content.utf8), attributes: [.posixPermissions: 0o600]) else {
        throw CocoaError(.fileWriteUnknown)
    }
    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path)
}

/// The handshake deadline for a case: short where the daemon stays silent
/// and the case is the timeout itself, the real one everywhere else, so a
/// slow machine cannot turn another case into a timeout.
private func handshakeTimeout(for outcome: Outcome) -> Duration {
    if case .refused(.timedOut) = outcome {
        return .milliseconds(300)
    }
    return RPCTimeouts.handshake
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

/// What connect() threw; nil when it connected.
private func connectError(_ client: RPCClient) async -> (any Error)? {
    do {
        try await client.connect()
        return nil
    } catch {
        return error
    }
}

private func matches(_ error: (any Error)?, _ want: Outcome) -> Bool {
    switch want {
    case .refused(let refusal):
        return (error as? RPCClient.HandshakeError) == refusal
    case .keyUnavailable(let ending):
        guard case .keyUnavailable(let reason)? = error as? RPCClient.HandshakeError else {
            return false
        }
        return reason.hasSuffix(ending)
    case .broken:
        return error is RPCClient.ClientError
    }
}

/// Every way a failure prints: its description, its reflection (Go's %v,
/// %+v, %#v), and the reason the client's state keeps.
private func texts(_ error: (any Error)?, _ state: RPCClient.State) -> [String] {
    var out = ["\(state)", String(reflecting: state)]
    if let error {
        out += [String(describing: error), String(reflecting: error), "\(error)"]
    }
    return out
}

/// Records an issue for every text that shows one of the secrets.
private func expectNoSecrets(_ texts: [String], _ secrets: [String], _ name: String) {
    for text in texts {
        let lower = text.lowercased()
        for secret in secrets where lower.contains(secret) {
            Issue.record("\(name): \"\(text)\" shows a key, a nonce or a proof")
        }
    }
}

@Suite(.serialized) struct HandshakeTests {
    /// TestHandshakeFailures, TestHandshakeConnectionDropped and
    /// TestHandshakeTimesOut over one table: Go's outcome, nothing sent
    /// after the line Go stops at, and no key, nonce or proof in any text of
    /// the failure (TestHandshakeErrorsCarryNoSecrets).
    @Test func failuresAsInGo() async throws {
        for fc in failCases() {
            let fake = try FakeDaemon()
            await fake.setHandshake(.raw(fc.script))
            try await fake.start()
            try edit(fc.keyFile, at: fake.keyPath)
            let client = RPCClient(socketPath: fake.path, handshakeTimeout: handshakeTimeout(for: fc.outcome))
            let error = await connectError(client)
            #expect(matches(error, fc.outcome), "\(fc.name): \(String(describing: error))")
            // The lines Go's case sends have arrived; whatever the client
            // might still send has 100 ms more.
            try await eventually { await fake.received.count >= fc.sent.count }
            try await Task.sleep(for: .milliseconds(100))
            let sent = await fake.received
            #expect(sent == fc.sent, "\(fc.name): the client sent \(sent)")
            let state = await client.state
            let secrets = await fake.secrets
            expectNoSecrets(texts(error, state), secrets, fc.name)
            await fake.stop()
        }
    }

    /// The fake daemon's own refusing modes show no secret either.
    @Test func refusalsCarryNoSecrets() async throws {
        let modes: [(String, FakeDaemon.HandshakeMode)] = [
            ("wrongProof", .wrongProof), ("rejectClient", .rejectClient), ("malformedProof", .malformedProof),
            ("silent", .silent), ("oldDaemon", .oldDaemon), ("protocolVersion", .protocolVersion(99)),
        ]
        for (name, mode) in modes {
            let fake = try FakeDaemon()
            await fake.setHandshake(mode)
            try await fake.start()
            // Short only for the silent daemon, whose case is the timeout.
            var timeout = RPCTimeouts.handshake
            if case .silent = mode {
                timeout = .milliseconds(300)
            }
            let client = RPCClient(socketPath: fake.path, handshakeTimeout: timeout)
            let error = await connectError(client)
            #expect(error is RPCClient.HandshakeError, "\(name): \(String(describing: error))")
            let state = await client.state
            let secrets = await fake.secrets
            expectNoSecrets(texts(error, state), secrets, name)
            await fake.stop()
        }
    }

    /// TestHandshakeErrorText: Go's texts, word for word.
    @Test func errorTextsAreGos() {
        let cases: [(RPCClient.HandshakeError, String)] = [
            (.protocolMismatch(daemon: 1), "malachid speaks protocol version 1, this client \(API.protocolVersion)"),
            (.keyUnavailable("rpc key unavailable"), "cannot use malachid's connection key: rpc key unavailable"),
            (.daemonUnproven, "the process on the socket did not prove it holds malachid's connection key"),
            (.rejected(.unauthenticated), "malachid rejected the handshake (unauthenticated)"),
            (.rejected(ErrorCode(rawValue: 1234)), "malachid rejected the handshake (unknown(1234))"),
            (.malformed("a line is longer than 64 KiB"), "malformed handshake answer from malachid: a line is longer than 64 KiB"),
            (.timedOut, "malachid did not complete the handshake in time"),
        ]
        for (refusal, want) in cases {
            #expect(refusal.description == want)
            #expect("\(refusal)" == want)
        }
    }
}
