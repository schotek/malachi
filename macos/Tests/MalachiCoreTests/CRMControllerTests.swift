// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// CRMController and CRMDialogSession against a fake daemon: crm.lookup into
// the dialog, crm.search merged in, crm.save with the partNotDownloaded
// round (message.download, then crm.save again), crm.remove after the
// question, and the shared busy state. Fictional people only.

private actor Calls {
    var list: [(String, Data)] = []

    func record(_ m: String, _ p: Data) {
        list.append((m, p))
    }

    func count(_ m: String) -> Int {
        list.filter { $0.0 == m }.count
    }

    var methods: [String] { list.map(\.0) }

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

private func summary(_ id: String, subject: String = "Offer for 2027") -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "m1", folderId: "f_inbox", from: [Address(name: "Jan Novák", address: "jan@acme.example")],
        subject: subject, date: .goZero, snippet: "", flags: [], hasAttachments: false, size: 0)
}

private func encoded<T: Encodable>(_ v: T) -> Data {
    (try? JSONCoding.encoder().encode(v)) ?? Data()
}

@MainActor
private final class Harness {
    let state = CRMState()
    let controller: CRMController
    var sessions: [CRMDialogSession] = []
    var toasts: [String] = []
    var opened: [String] = []
    var questions: [CRM.Question] = []
    var answer = true
    var subjects: [MessageSummary] = [summary("m_1")]

    init(client: RPCClient) {
        state.accounts = { [crmImap, crmEwayA] }
        state.folderRole = { _ in .inbox }
        controller = CRMController(client: client, state: state)
        controller.subjects = { [unowned self] in self.subjects }
        controller.present = { [unowned self] in self.sessions.append($0) }
        controller.toast = { [unowned self] in self.toasts.append($0) }
        controller.openURL = { [unowned self] in self.opened.append($0) }
        controller.confirm = { [unowned self] q in
            self.questions.append(q)
            return self.answer
        }
    }
}

// One line: the fake daemon frames its answers by line.
private let lookupJSON = [
    #"{"accountId":"e1","saved":[{"messageId":"m_1","record":null}],"suggestions":["#,
    #"{"kind":"contact","guid":"00000000-0000-4000-8000-000000000001","name":"Novák, Jan","reason":"address","url":"https://open.eway-crm.com/?l=1"},"#,
    #"{"kind":"deal","guid":"00000000-0000-4000-8000-000000000005","name":"Fleet renewal","reason":"contact","superior":true,"url":"https://open.eway-crm.com/?l=5"}]}"#,
].joined()

private let savedRecordJSON = #"{"accountId":"e1","itemGuid":"00000000-0000-4000-8000-000000000100","savedAt":"2026-10-17T10:00:00Z","checkedAt":"2026-10-17T10:00:00Z","private":false,"items":[],"url":"https://open.eway-crm.com/?l=e"}"#

@MainActor
@Suite(.serialized) struct CRMControllerTests {
    private func connect(_ fake: FakeDaemon) async throws -> RPCClient {
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        return client
    }

    @Test func savesThroughTheDialogAndDownloadsFirst() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.CrmLookup.name) { p in
            await calls.record(API.CrmLookup.name, p)
            return json(lookupJSON)
        }
        await fake.on(API.CrmSave.name) { p in
            await calls.record(API.CrmSave.name, p)
            if await calls.count(API.CrmSave.name) == 1 {
                throw RPCError(code: .partNotDownloaded, message: "not whole",
                               data: .object(["messageIds": .array([.string("m_1")])]))
            }
            try? await Task.sleep(for: .milliseconds(100))
            return json(#"{"results":[{"messageId":"m_1","created":true,"record":\#(savedRecordJSON)}]}"#)
        }
        await fake.on(API.MessageDownload.name) { p in
            await calls.record(API.MessageDownload.name, p)
            return encoded(MessageDownloadResult(message: Message(summary: summary("m_1"))))
        }
        let h = Harness(client: try await connect(fake))
        #expect(h.controller.button == CRM.buttonFor(.notSaved))
        #expect(h.controller.offer == CRM.Offer(shown: true, enabled: true, count: 1))
        h.controller.save()
        let session = try #require(h.sessions.first)
        #expect(session.phase == .loading && session.summary == "Offer for 2027 · Jan Novák")
        try await waitUntil { session.phase != .loading }
        #expect(session.phase == .ready)
        let lookup = try #require(try await calls.last(API.CrmLookup.name, as: CrmLookupParams.self))
        #expect(lookup == CrmLookupParams(accountId: "e1", messageAccountId: "m1", messageIds: ["m_1"]))
        let dialog = try #require(session.dialog)
        #expect(!dialog.editing && dialog.superior == crmRef(.deal, 5) && session.texts.title == "Save to eWay-CRM")

        var closed = false
        session.onClose = { closed = true }
        session.save()
        #expect(closed && session.finished)
        #expect(h.controller.button.state == .saving && h.state.inFlight.has("m1", "m_1"))
        try await waitUntil { h.toasts.contains("Saved to eWay-CRM") }
        #expect(h.toasts == ["Downloading the message first…", "Saved to eWay-CRM"])
        #expect(await calls.methods == [API.CrmLookup.name, API.CrmSave.name, API.MessageDownload.name, API.CrmSave.name])
        let download = try #require(try await calls.last(API.MessageDownload.name, as: MessageDownloadParams.self))
        #expect(download == MessageDownloadParams(accountId: "m1", messageId: "m_1"))
        let saved = try #require(try await calls.last(API.CrmSave.name, as: CrmSaveParams.self))
        #expect(saved.link == [crmRef(.contact, 1)] && saved.superior == crmRef(.deal, 5) && saved.messageIds == ["m_1"])
        // The call's own answer turns the button before the daemon's
        // notification refreshes the message.
        #expect(!h.state.inFlight.has("m1", "m_1") && h.controller.button.state == .saved)
        #expect(h.controller.record?.url == "https://open.eway-crm.com/?l=e")
        h.controller.open()
        #expect(h.opened == ["https://open.eway-crm.com/?l=e"])
    }

    @Test func endsTheBusyMarkWhenTheWindowCloses() async throws {
        // A message window closed while its save runs takes its controller
        // with it; the shared state must still release the message and keep
        // the call's answer, or the main window would show Saving for ever.
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        let gate = Calls()
        await fake.on(API.CrmSave.name) { p in
            await gate.record(API.CrmSave.name, p)
            try? await Task.sleep(for: .milliseconds(200))
            return json(#"{"results":[{"messageId":"m_1","created":true,"record":\#(savedRecordJSON)}]}"#)
        }
        let client = try await connect(fake)
        let state = CRMState()
        state.accounts = { [crmImap, crmEwayA] }
        state.folderRole = { _ in .inbox }
        var controller: CRMController? = CRMController(client: client, state: state)
        controller?.subjects = { [summary("m_1")] }
        let params = CrmSaveParams(accountId: "e1", messageAccountId: "m1", messageIds: ["m_1"], link: [crmRef(.contact, 1)])
        controller?.runSave(params)
        #expect(state.inFlight.has("m1", "m_1"))
        try await waitUntil { await gate.count(API.CrmSave.name) == 1 }
        controller = nil
        try await waitUntil { !state.inFlight.has("m1", "m_1") }
        #expect(state.isSaved(summary("m_1")))
    }

    @Test func removesAfterTheQuestion() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.CrmRemove.name) { p in
            await calls.record(API.CrmRemove.name, p)
            return json("{}")
        }
        let h = Harness(client: try await connect(fake))
        var s = summary("m_1")
        s.crmSaved = true
        h.subjects = [s]
        #expect(h.controller.button.state == .saved && h.controller.offer.saved)
        h.answer = false
        h.controller.remove()
        try await waitUntil { h.questions.count == 1 }
        try await Task.sleep(for: .milliseconds(50))
        #expect(await calls.count(API.CrmRemove.name) == 0, "declined")
        #expect(h.questions[0].heading == "Remove from eWay-CRM?" && h.questions[0].body.hasPrefix("“Offer for 2027”"))
        h.answer = true
        h.controller.remove()
        try await waitUntil { h.toasts == ["Removed from eWay-CRM"] }
        let p = try #require(try await calls.last(API.CrmRemove.name, as: CrmRemoveParams.self))
        #expect(p == CrmRemoveParams(accountId: "e1", messageAccountId: "m1", messageId: "m_1"))
        #expect(h.controller.button.state == .notSaved, "the call's answer wins over the stale summary")
    }

    @Test func searchMergesAndLookupFailureShows() async throws {
        let calls = Calls()
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        await fake.on(API.CrmLookup.name) { _ in json(lookupJSON) }
        await fake.on(API.CrmSearch.name) { p in
            await calls.record(API.CrmSearch.name, p)
            return json(#"{"items":[{"kind":"project","guid":"00000000-0000-4000-8000-000000000007","name":"Pilot","reason":"search","url":""}]}"#)
        }
        let h = Harness(client: try await connect(fake))
        h.controller.save()
        let session = try #require(h.sessions.first)
        try await waitUntil { session.phase == .ready }
        session.search("p")
        session.search("pi")
        session.search("pil")
        try await waitUntil { session.dialog?.groups.contains { $0.kind == .project } == true }
        #expect(await calls.count(API.CrmSearch.name) == 1, "typing pauses before the search")
        let p = try #require(try await calls.last(API.CrmSearch.name, as: CrmSearchParams.self))
        #expect(p.query == "pil" && p.accountId == "e1" && !session.searching)
        session.cancel()
        #expect(session.finished)

        await fake.on(API.CrmLookup.name) { _ in throw RPCError(code: .networkError, message: "dial") }
        h.controller.save()
        let failing = try #require(h.sessions.last)
        try await waitUntil { failing.phase != .loading }
        #expect(failing.phase == .failed("Contacting eWay-CRM failed: the server could not be reached"))
        failing.save()
        #expect(!failing.finished, "nothing to save")
    }

    /// Several rows: the Save button with their count; nothing for an
    /// account the rules refuse.
    @Test func buttonOfSeveral() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        let h = Harness(client: try await connect(fake))
        h.subjects = [summary("m_1"), summary("m_2"), summary("m_3")]
        let b = h.controller.button
        #expect(b.state == .notSaved && b.label == "Save 3 Messages to eWay-CRM…" && b.enabled && !b.menu)
        h.state.begin("m1", ["m_2"])
        #expect(h.controller.button.state == .saving && !h.controller.button.enabled)
        h.state.end("m1", ["m_2"])
        h.state.accounts = { [crmImap] }
        #expect(h.controller.button.state == .hidden && !h.controller.offer.shown)
        h.subjects = []
        #expect(h.controller.button.state == .hidden)
    }
}
