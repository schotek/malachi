// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// SignatureController (the details popover), PGPKeysController (Settings →
// Security) and SignatureState against a fake daemon. Fictional people only.

private actor Calls {
    var list: [(String, Data)] = []

    func record(_ m: String, _ p: Data) {
        list.append((m, p))
    }

    func count(_ m: String) -> Int {
        list.filter { $0.0 == m }.count
    }

    func last<T: Decodable>(_ m: String, as t: T.Type) throws -> T? {
        guard let d = list.last(where: { $0.0 == m })?.1 else { return nil }
        return try JSONCoding.decoder().decode(t, from: d)
    }
}

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let fp = "0123456789abcdef0123456789abcdef01234567"
private let sha = String(repeating: "ab12cd34", count: 8)

private let smimeJSON = #"""
{"signature":{"kind":"smime","status":"valid","signer":{"name":"Jan","email":"jan@example.org","fingerprint":"\#(sha)"},
 "messageId":"m_1","arrivedAt":"2026-11-02T08:00:00Z","algorithm":"RSA 2048 / SHA-256",
 "smime":{"chain":[{"sha256":"\#(sha)","subject":"CN=Jan","notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z"}],
          "revocation":{"state":"unchecked","checkable":true}}}}
"""#.replacingOccurrences(of: "\n", with: "") // one line: the fake daemon frames by newline

private let checkedJSON = smimeJSON.replacingOccurrences(
    of: #""revocation":{"state":"unchecked","checkable":true}"#,
    with: #""revocation":{"state":"good","checkedAt":"2026-11-02T09:00:00Z","source":"ocsp","checkable":true}"#)

private let pgpJSON = #"""
{"signature":{"kind":"pgp","status":"validSeen","signer":{"name":"Jan","email":"jan@example.org","fingerprint":"\#(fp)"},
 "messageId":"m_1","arrivedAt":"2026-11-02T08:00:00Z",
 "pgp":{"keyTrust":"seen","primaryFingerprint":"\#(fp)","importable":{"fingerprint":"\#(fp)","partId":"3"}}}}
"""#.replacingOccurrences(of: "\n", with: "")

private let keyJSON = #"{"fingerprint":"\#(fp)","userIds":["Jan <jan@example.org>"],"emails":["jan@example.org"],"trust":"seen","source":"autocrypt","firstSeen":"2026-11-01T00:00:00Z","createdAt":"2025-01-01T00:00:00Z"}"#

private let fallback = SignatureSummary(
    kind: .smime, status: .valid, signer: SignatureSigner(name: "Jan", email: "jan@example.org"))

@MainActor
@Suite(.serialized) struct SignatureControllerTests {
    private func connect(_ fake: FakeDaemon) async throws -> RPCClient {
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        return client
    }

    private func controller(_ client: RPCClient, summary: SignatureSummary? = fallback) -> SignatureController {
        SignatureController(
            client: client, messageID: "m_1", from: "jan@example.org", fallback: summary, date: { _ in "date" })
    }

    @Test func showsTheSummaryUntilTheDaemonAnswers() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.MessageSignatureGet.name) { _ in json(smimeJSON) }
        let c = controller(try await connect(fake))
        #expect(c.details == nil && c.model.title == "Valid signature" && c.model.rows.map(\.key) == [.signer, .kind])
        c.load()
        try await waitUntil { c.details != nil }
        let m = c.model
        #expect(m.buttons.map(\.action) == [.checkRevocation] && m.rows.contains { $0.key == .algorithm })
        #expect(!c.loading)
    }

    @Test func anOlderDaemonLeavesTheSummary() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.MessageSignatureGet.name) { _ in
            throw RPCError(code: .notImplemented, message: "x")
        }
        let c = controller(try await connect(fake))
        var changes = 0
        c.onChange = { changes += 1 }
        c.load()
        try await waitUntil { changes >= 2 }
        #expect(c.details == nil && c.model.title == "Valid signature" && !c.loading)
        // No verdict at all: nothing to show.
        let none = controller(try await connect(fake), summary: nil)
        #expect(none.model == SignatureView.Details() && none.current == nil)
    }

    @Test func checkRevocationShowsBusyThenTheResult() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.MessageSignatureRevocation.name) { p in
            await calls.record(API.MessageSignatureRevocation.name, p)
            try? await Task.sleep(for: .milliseconds(100))
            return json(checkedJSON)
        }
        let c = controller(try await connect(fake))
        c.seed(try JSONCoding.decoder().decode(MessageSignatureResult.self, from: json(smimeJSON)).signature!)
        c.perform(.checkRevocation)
        #expect(c.busy == .checkRevocation)
        #expect(c.model.buttons == [SignatureView.Button(action: .checkRevocation, label: "Checking…", enabled: false)])
        c.perform(.checkRevocation) // a second press while it runs does nothing
        try await waitUntil { c.busy == .none }
        #expect(await calls.count(API.MessageSignatureRevocation.name) == 1)
        let sent = try #require(try await calls.last(API.MessageSignatureRevocation.name, as: MessageSignatureParams.self))
        #expect(sent == MessageSignatureParams(messageId: "m_1"))
        #expect(c.model.buttons.map(\.label) == ["Check Revocation Again"])
        #expect(c.model.rows.first { $0.key == .revocation }?.value == "Not revoked (checked date)")
    }

    @Test func aFailedCheckToastsTheClassAndFreesTheButton() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.MessageSignatureRevocation.name) { _ in
            throw RPCError(code: .revocationFailed, message: "x", data: .object(["reason": .string("timeout")]))
        }
        let c = controller(try await connect(fake))
        c.seed(try JSONCoding.decoder().decode(MessageSignatureResult.self, from: json(smimeJSON)).signature!)
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        c.perform(.checkRevocation)
        try await waitUntil { !toasts.isEmpty }
        #expect(toasts == ["The certificate's issuer did not answer in time."])
        try await waitUntil { c.busy == .none }
        #expect(c.model.buttons.map(\.label) == ["Check Revocation"])
    }

    @Test func importKeyPassesTheOfferAndLoadsAgain() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyImport.name) { p in
            await calls.record(API.PGPKeyImport.name, p)
            return json(#"{"keys":[\#(keyJSON)],"added":1,"updated":0}"#)
        }
        await fake.on(API.MessageSignatureGet.name) { _ in json(pgpJSON) }
        let c = controller(try await connect(fake))
        c.seed(try JSONCoding.decoder().decode(MessageSignatureResult.self, from: json(pgpJSON)).signature!)
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        #expect(c.model.buttons.map(\.action) == [.importKey, .confirmKey])
        c.perform(.importKey)
        try await waitUntil { toasts == ["1 key imported"] }
        let sent = try #require(try await calls.last(API.PGPKeyImport.name, as: PGPKeyImportParams.self))
        #expect(sent == PGPKeyImportParams(messageId: "m_1", partId: "3"))
        try await waitUntil { c.busy == .none }
    }

    @Test func aRefusedKeyToastsItsClass() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyImport.name) { _ in
            throw RPCError(code: .invalidArgument, message: "x", data: .object(["reason": .string("secretKey")]))
        }
        let c = controller(try await connect(fake))
        c.seed(try JSONCoding.decoder().decode(MessageSignatureResult.self, from: json(pgpJSON)).signature!)
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        c.perform(.importKey)
        try await waitUntil { !toasts.isEmpty }
        #expect(toasts == ["This is a secret key. Import only the public key: Malachi Mail never stores secret keys."])
    }

    @Test func confirmKeyAsksFirst() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyVerify.name) { p in
            await calls.record(API.PGPKeyVerify.name, p)
            return json(#"{"key":\#(keyJSON.replacingOccurrences(of: "seen", with: "verified"))}"#)
        }
        await fake.on(API.MessageSignatureGet.name) { _ in json(pgpJSON) }
        let c = controller(try await connect(fake))
        c.seed(try JSONCoding.decoder().decode(MessageSignatureResult.self, from: json(pgpJSON)).signature!)
        var asked: [(SignatureView.Question, String)] = []
        var answer = false
        c.confirm = { q, fingerprint in
            asked.append((q, fingerprint))
            return answer
        }
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        c.perform(.confirmKey)
        try await waitUntil { asked.count == 1 && c.busy == .none }
        try await Task.sleep(for: .milliseconds(50))
        #expect(await calls.count(API.PGPKeyVerify.name) == 0, "declined")
        #expect(asked[0].0.heading == "Confirm This Key?" && asked[0].1 == SignatureView.formatFingerprint(fp))
        answer = true
        c.perform(.confirmKey)
        try await waitUntil { toasts == ["Key confirmed"] }
        let sent = try #require(try await calls.last(API.PGPKeyVerify.name, as: PGPKeyVerifyParams.self))
        #expect(sent == PGPKeyVerifyParams(fingerprint: fp, verified: true))
    }

    @Test func theStateCountsAndTellsItsObservers() {
        let state = SignatureState()
        var heard = 0
        let token = state.observe { heard += 1 }
        state.changed()
        state.changed()
        #expect(state.generation == 2 && heard == 2)
        token.cancel()
        state.changed()
        #expect(state.generation == 3 && heard == 2)
    }
}

@MainActor
@Suite(.serialized) struct PGPKeysControllerTests {
    private func connect(_ fake: FakeDaemon) async throws -> RPCClient {
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        return client
    }

    private let now = RFC3339.parse("2026-11-02T08:00:00Z") ?? .goZero

    @Test func listsTheKeysAsRows() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyList.name) { _ in json(#"{"keys":[\#(keyJSON)]}"#) }
        let c = PGPKeysController(client: try await connect(fake), now: { [now = self.now] in now }, date: { _ in "date" })
        #expect(!c.loaded && c.rows.isEmpty)
        c.load()
        try await waitUntil { c.loaded }
        #expect(c.rows.count == 1 && c.rows[0].title == "Jan <jan@example.org>" && c.rows[0].origin == "Found in a message header on date")
        #expect(c.rows[0].actions.map(\.action) == [.confirmKey, .removeKey] && !c.loadFailed)
        #expect(c.texts.title == "Security")
    }

    @Test func aFailedListKeepsWhatItHad() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyList.name) { _ in throw RPCError(code: .notImplemented, message: "x") }
        let c = PGPKeysController(client: try await connect(fake))
        c.load()
        try await waitUntil { c.loaded }
        #expect(c.loadFailed && c.rows.isEmpty)
    }

    @Test func importsAPastedKeyAndAFile() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyImport.name) { p in
            await calls.record(API.PGPKeyImport.name, p)
            return json(#"{"keys":[\#(keyJSON)],"added":1,"updated":0}"#)
        }
        await fake.on(API.PGPKeyList.name) { _ in json(#"{"keys":[\#(keyJSON)]}"#) }
        let c = PGPKeysController(client: try await connect(fake))
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        c.importPasted("  \n")
        c.importPasted("-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n")
        try await waitUntil { toasts == ["1 key imported"] }
        let pasted = try #require(try await calls.last(API.PGPKeyImport.name, as: PGPKeyImportParams.self))
        #expect(pasted.armored == "-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc" && pasted.data == nil)
        try await waitUntil { !c.busy && c.rows.count == 1 }
        c.importFile(Data([0x99, 0x01]))
        try await waitUntil { await calls.count(API.PGPKeyImport.name) == 2 }
        let file = try #require(try await calls.last(API.PGPKeyImport.name, as: PGPKeyImportParams.self))
        #expect(file.data == Data([0x99, 0x01]) && file.armored == nil)
        // A file over the limit never reaches the daemon.
        try await waitUntil { !c.busy }
        c.importFile(Data(count: API.Limits.maxPGPKeyBytes + 1))
        #expect(toasts.last == "The key data is too large.")
        #expect(await calls.count(API.PGPKeyImport.name) == 2)
    }

    @Test func removeAsksAndConfirmationCanBeTakenBack() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.PGPKeyRemove.name) { p in
            await calls.record(API.PGPKeyRemove.name, p)
            return json("{}")
        }
        await fake.on(API.PGPKeyVerify.name) { p in
            await calls.record(API.PGPKeyVerify.name, p)
            return json(#"{"key":\#(keyJSON)}"#)
        }
        await fake.on(API.PGPKeyList.name) { _ in json(#"{"keys":[]}"#) }
        let c = PGPKeysController(client: try await connect(fake))
        var questions: [SignatureView.Question] = []
        var answer = false
        c.ask = { q, _ in
            questions.append(q)
            return answer
        }
        var toasts: [String] = []
        c.toast = { toasts.append($0) }
        c.perform(.removeKey, fingerprint: fp)
        try await waitUntil { questions.count == 1 && !c.busy }
        try await Task.sleep(for: .milliseconds(50))
        #expect(await calls.count(API.PGPKeyRemove.name) == 0, "declined")
        #expect(questions[0].heading == "Remove This Key?" && questions[0].destructive)
        answer = true
        c.perform(.removeKey, fingerprint: fp)
        try await waitUntil { toasts == ["Key removed"] }
        let removed = try #require(try await calls.last(API.PGPKeyRemove.name, as: PGPKeyRemoveParams.self))
        #expect(removed == PGPKeyRemoveParams(fingerprint: fp))
        try await waitUntil { !c.busy }
        // Remove Confirmation needs no question.
        c.perform(.unconfirmKey, fingerprint: fp)
        try await waitUntil { toasts.last == "Confirmation removed" }
        let sent = try #require(try await calls.last(API.PGPKeyVerify.name, as: PGPKeyVerifyParams.self))
        #expect(sent == PGPKeyVerifyParams(fingerprint: fp, verified: false))
        #expect(questions.count == 2)
    }
}
