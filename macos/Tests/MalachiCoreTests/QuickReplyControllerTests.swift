// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/quickreply/controller_test.go: the quick-reply bar's
// controller over a fake host whose calls go to a MailFixture with scripted
// handlers for draft.create, draft.save, message.send and draft.delete. The
// host's main loop is the test: `post` queues, `drain` runs what was posted
// (Go's goroutine and Host.Post). A gate, when set for a method, holds that
// call until the test opens it.

private struct Timeout: Error {}

/// The daemon's answers and what holds them, off the main actor.
private actor Script {
    var results: [String: Data] = [:]
    var errors: [String: RPCError] = [:]
    var gates: [String: Bool] = [:]
    private var waiters: [String: [CheckedContinuation<Void, Never>]] = [:]

    func set(_ method: String, result: some Encodable) throws {
        results[method] = try JSONCoding.encoder().encode(result)
    }

    func set(_ method: String, error: RPCError) {
        errors[method] = error
    }

    /// Holds `method` until `open(method)`.
    func gate(_ method: String) {
        gates[method] = true
    }

    func open(_ method: String) {
        gates[method] = nil
        for w in waiters.removeValue(forKey: method) ?? [] {
            w.resume()
        }
    }

    func answer(_ method: String) async throws -> Data {
        if gates[method] == true {
            await withCheckedContinuation { waiters[method, default: []].append($0) }
        }
        if let e = errors[method] {
            throw e
        }
        return results[method] ?? Data("{}".utf8)
    }
}

/// quickreply fakeHost: records what the controller asks of the window.
@MainActor
private final class FakeHost: QuickReplyHost {
    let client: RPCClient
    /// The calls, in order, by method name, with their timeouts.
    var methods: [String] = []
    var timeouts: [Duration] = []
    var creates: [DraftCreateParams] = []
    var saves: [DraftSaveParams] = []
    var sends: [MessageSendParams] = []
    var deletes: [DraftDeleteParams] = []

    var posted: [@MainActor () -> Void] = []

    /// The host's own methods, in order.
    var log: [String] = []
    var attributions: [MessageID] = []
    var before: [MessageID] = []
    var holdBefore = false
    var beforeDone: [@MainActor () -> Void] = []
    var opens: [(mode: Conversation.QuickReplyMode, id: MessageID, ownHTML: String, opened: @MainActor () -> Void)] = []
    var sent: [Bool] = []
    var failed: [any Error] = []

    init(client: RPCClient) {
        self.client = client
    }

    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration) async throws -> M.Result {
        methods.append(M.name)
        timeouts.append(timeout)
        if let p = params as? DraftCreateParams { creates.append(p) }
        if let p = params as? DraftSaveParams { saves.append(p) }
        if let p = params as? MessageSendParams { sends.append(p) }
        if let p = params as? DraftDeleteParams { deletes.append(p) }
        return try await client.call(method, params, timeout: timeout)
    }

    func post(_ f: @escaping @MainActor () -> Void) {
        posted.append(f)
    }

    func attribution(mode: Conversation.QuickReplyMode, id: MessageID) -> String {
        log.append("attribution")
        attributions.append(id)
        return "On Monday, Jana wrote:"
    }

    func beforeCreate(id: MessageID, done: @escaping @MainActor () -> Void) {
        log.append("before")
        before.append(id)
        if holdBefore {
            beforeDone.append(done)
            return
        }
        done()
    }

    func openCompose(mode: Conversation.QuickReplyMode, id: MessageID, ownHTML: String, opened: @escaping @MainActor () -> Void) {
        log.append("open")
        opens.append((mode, id, ownHTML, opened))
    }

    func sent(comment: Bool) {
        log.append("sent")
        sent.append(comment)
    }

    func failed(_ error: any Error) {
        log.append("failed")
        failed.append(error)
    }

    /// Runs one posted callback (the end of a Send), waiting for it.
    func drain() async throws {
        let deadline = ContinuousClock.now + .seconds(5)
        while posted.isEmpty {
            if ContinuousClock.now > deadline { throw Timeout() }
            try await Task.sleep(for: .milliseconds(5))
        }
        posted.removeFirst()()
    }
}

// The fixtures: a mail account and an issue tracker's, a conversation whose
// newest message of someone else is m2 (m3 is the user's).
private let mailAcc = Account(
    id: "acc", config: AccountConfig(name: "Work", email: "petr@example.org"), enabled: true,
    state: SyncState(accountId: "acc", status: .idle), capabilities: API.mailCapabilities)
private let jiraAcc = Account(
    id: "jira", config: AccountConfig(name: "Jira", email: "petr@example.org", kind: .jira), enabled: true,
    state: SyncState(accountId: "jira", status: .idle), capabilities: [.comment, .forward, .transition])

private let mailActions = Capabilities.Actions(reply: true, replyAll: true, forward: true)
private let commentActions = Capabilities.Actions(reply: true, forward: true, comment: true)

private let original = "<blockquote type=\"cite\"><p>Hi</p><img src=\"cid:pic1\"></blockquote>"

private func item(_ id: String, _ acc: AccountID, mine: Bool) -> Conversation.Item {
    var it = Conversation.Item(kind: .message, message: MessageSummary(
        id: MessageID(id), accountId: acc, folderId: "f", threadId: nil, from: [], to: [], subject: "",
        date: .goZero, snippet: "", flags: [], hasAttachments: false, size: 0))
    it.sender = "Jana " + id
    it.mine = mine
    return it
}

private func model(_ thread: ThreadID, _ acc: AccountID) -> Conversation.Model {
    var m = Conversation.Model(thread: thread)
    m.items = [item("m1", acc, mine: false), item("m2", acc, mine: false), item("m3", acc, mine: true)]
    return m
}

private func created(_ acc: AccountID) -> DraftCreateResult {
    DraftCreateResult(draft: Draft(
        accountId: acc,
        to: [Address(name: "Jana", address: "jana@example.org")],
        cc: [Address(address: "petr@example.org")],
        subject: "Re: Lunch",
        textBody: "> Hi",
        htmlBody: original,
        inReplyTo: "m2",
        attachments: [DraftAttachment(id: "att1", filename: "pic.png", contentType: "image/png", size: 12, inline: true, contentId: "pic1")]
    ), quoted: .html)
}

/// A fixture with the four handlers, a connected client and the fake host.
@MainActor
private final class Harness {
    let fixture: MailFixture
    let client: RPCClient
    let script = Script()
    let host: FakeHost
    var changes = 0

    init(mail: Bool = true) async throws {
        fixture = try MailFixture()
        let script = script
        for method in [API.DraftCreate.name, API.DraftSave.name, API.MessageSend.name, API.DraftDelete.name] {
            await fixture.on(method) { _ in try await script.answer(method) }
        }
        if mail {
            try await script.set(API.DraftCreate.name, result: created(mailAcc.id))
            try await script.set(API.DraftSave.name, result: DraftSaveResult(draftId: "d1", version: 1, textBody: ""))
            try await script.set(API.MessageSend.name, result: MessageSendResult(outboxId: "o1"))
        }
        try await script.set(API.DraftDelete.name, result: EmptyResult())
        try await fixture.start()
        client = RPCClient(socketPath: fixture.path)
        try await client.connect()
        host = FakeHost(client: client)
    }

    /// quickreply newShown: a controller showing thread t1 of the mail
    /// account as chat.
    func shown() -> QuickReplyController {
        let c = QuickReplyController(host: host)
        c.onChange = { [unowned self] in self.changes += 1 }
        c.setChatShown(true)
        c.show("t1")
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        return c
    }

    func stop() async {
        await client.close()
        await fixture.stop()
    }
}

@MainActor
@Suite(.serialized) struct QuickReplyControllerTests {
    @Test func targetAndModes() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        var s = c.state
        #expect(s.visible && s.target?.message?.id == "m2" && s.thread == "t1" && !s.comment)
        #expect(s.modes == [.reply, .replyAll, .forward] && s.mode == .reply)

        c.update(model("t1", jiraAcc.id), actions: commentActions, account: jiraAcc)
        s = c.state
        #expect(s.visible && s.comment && s.modes == [.reply] && s.mode == .reply)

        c.update(model("t1", mailAcc.id), actions: Capabilities.Actions(), account: mailAcc)
        s = c.state
        #expect(!s.visible && s.modes.isEmpty)

        // Forward without Reply: the menu would have it, the bar is hidden.
        c.update(model("t1", mailAcc.id), actions: Capabilities.Actions(forward: true), account: mailAcc)
        #expect(!c.state.visible)
    }

    @Test func visibility() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = QuickReplyController(host: h.host)
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        #expect(!c.state.visible, "visible in the timeline")
        c.setChatShown(true)
        #expect(c.state.visible && c.state.thread == "t1", "Update did not show t1")
        c.show("t2")
        #expect(!c.state.visible && c.state.target == nil, "a target before Update")
        c.update(Conversation.Model(thread: "t2"), actions: mailActions, account: mailAcc)
        #expect(!c.state.visible, "visible without a target")
        c.show("")
        c.setText("lost")
        #expect(!c.state.visible && c.state.text.isEmpty && c.keptThreads.isEmpty)
    }

    @Test func blank() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        let cases: [String: Bool] = [
            "": true, " \n\t": true, jiraScalar(0x200B) + jiraScalar(0x2066): true, "\u{0}": true, "a": false, " ok ": false,
        ]
        for (text, blank) in cases {
            c.setText(text)
            #expect(c.state.blank == blank, "\(text.unicodeScalars.map { $0.value })")
        }
    }

    @Test func onChange() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        let before = h.changes
        c.setText("a")
        c.setText("a") // no change
        c.setMode(.replyAll)
        c.setMode(.replyAll)
        c.setMode(.forward) // ignored
        c.setChatShown(true) // no change
        c.show("t1") // no change
        #expect(h.changes - before == 2)
    }

    @Test func textAndModePerThread() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        c.setText("for t1")
        c.setMode(.replyAll)

        c.show("t2")
        c.update(model("t2", mailAcc.id), actions: mailActions, account: mailAcc)
        #expect(c.state.text.isEmpty && c.state.mode == .reply, "t2 starts with \(c.state)")
        c.setText("for t2")

        c.show("t1")
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        #expect(c.state.text == "for t1" && c.state.mode == .replyAll)
        // A mode no longer offered falls back to the first.
        c.update(model("t1", mailAcc.id), actions: Capabilities.Actions(reply: true), account: mailAcc)
        #expect(c.state.mode == .reply)
        c.setMode(.replyAll) // not offered: ignored
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        #expect(c.state.mode == .replyAll, "the chosen mode was forgotten")
        c.show("t2")
        #expect(c.state.text == "for t2")
        // An emptied conversation is forgotten.
        c.setText("")
        #expect(!c.keptThreads.contains("t2"))
    }

    @Test func send() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        c.setMode(.replyAll)
        let text = "Sure,\nsee you <there> & then"
        c.setText(text)
        c.send()
        #expect(c.state.sending)
        c.send() // a second Send while one is on its way
        try await h.host.drain()

        #expect(h.host.methods == [API.DraftCreate.name, API.DraftSave.name, API.MessageSend.name])
        #expect(h.host.timeouts == Array(repeating: RPCTimeouts.compose, count: 3))
        #expect(h.host.log == ["before", "attribution", "sent"])
        #expect(h.host.before == ["m2"])
        #expect(h.host.creates == [DraftCreateParams(
            accountId: "acc", mode: .replyAll, messageId: "m2", attribution: "On Monday, Jana wrote:")])
        let tmpl = created(mailAcc.id).draft
        let wantDraft = Draft(
            accountId: "acc", to: tmpl.to, cc: tmpl.cc, subject: "Re: Lunch", textBody: text,
            htmlBody: Conversation.quickReplyBody(mode: .replyAll, own: text, draft: tmpl), inReplyTo: "m2",
            attachments: [DraftAttachment(id: "att1", filename: "", contentType: "", size: 0, inline: false)])
        let got = try #require(h.host.saves.first?.draft)
        #expect(got == wantDraft)
        #expect(got.htmlBody == "<p>Sure,<br>see you &lt;there&gt; &amp; then</p>\n" + original)
        #expect(h.host.sends == [MessageSendParams(accountId: "acc", draftId: "d1", version: 1)])
        let s = c.state
        #expect(!s.sending && s.text.isEmpty && h.host.sent == [false] && h.host.failed.isEmpty)
        // The mode stays for the next reply.
        #expect(s.mode == .replyAll)
    }

    /// `to` is never nil on the wire (the daemon reads "to": null as no
    /// field).
    @Test func saveDraftToNeverNil() throws {
        let d = QuickReplyController.saveDraft(mode: .reply, text: "x", draft: Draft(accountId: "acc"))
        #expect(d.to.isEmpty && d.attachments == nil && d.comment == nil)
        let json = try JSONSerialization.jsonObject(with: JSONCoding.encoder().encode(d)) as? [String: Any]
        let to = json?["to"] as? [Any]
        #expect(to != nil && to?.isEmpty == true)
    }

    @Test func sendComment() async throws {
        let h = try await Harness(mail: false)
        defer { Task { await h.stop() } }
        let issue = IssueInfo(key: "PRJ-1", url: "", summary: "", status: "")
        let tmpl = Draft(accountId: "jira", subject: "PRJ-1: Bug", inReplyTo: "m2",
                         comment: DraftComment(issue: issue, visibility: .internal))
        try await h.script.set(API.DraftCreate.name, result: DraftCreateResult(draft: tmpl, quoted: .none))
        try await h.script.set(API.DraftSave.name, result: DraftSaveResult(draftId: "d1", version: 1, textBody: ""))
        try await h.script.set(API.MessageSend.name, result: MessageSendResult(outboxId: "o1"))
        let c = QuickReplyController(host: h.host)
        c.setChatShown(true)
        c.update(model("t1", jiraAcc.id), actions: commentActions, account: jiraAcc)
        c.setText("Fixed in 1.2\n\nThanks")
        c.send()
        try await h.host.drain()

        #expect(h.host.log == ["sent"], "no download, no attribution")
        #expect(h.host.creates == [DraftCreateParams(accountId: "jira", mode: .reply, messageId: "m2")])
        let d = try #require(h.host.saves.first?.draft)
        #expect(d.htmlBody == "<p>Fixed in 1.2</p><p>Thanks</p>")
        #expect(d.comment?.visibility == .public && d.comment?.issue.key == "PRJ-1")
        #expect(h.host.sent == [true])
    }

    @Test(arguments: [
        (API.DraftCreate.name, [API.DraftCreate.name]),
        (API.DraftSave.name, [API.DraftCreate.name, API.DraftSave.name]),
        (API.MessageSend.name, [API.DraftCreate.name, API.DraftSave.name, API.MessageSend.name, API.DraftDelete.name]),
    ])
    func sendFailures(method: String, calls: [String]) async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let fail = RPCError(code: .internalError, message: "boom")
        await h.script.set(method, error: fail)
        await h.script.set(API.DraftDelete.name, error: RPCError(code: .serverError, message: "ignored"))
        let c = h.shown()
        c.setText("keep me")
        c.send()
        try await h.host.drain()
        #expect(h.host.methods == calls)
        #expect(h.host.failed.count == 1 && (h.host.failed.first as? RPCError) == fail && h.host.sent.isEmpty)
        #expect(c.state.text == "keep me" && !c.state.sending)
        if method == API.MessageSend.name {
            #expect(h.host.deletes == [DraftDeleteParams(accountId: "acc", draftId: "d1")])
        }
    }

    /// What Send does not do: blank text, a hidden bar, a Send on its way.
    @Test func sendGuards() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        c.setText(" \n ")
        c.send()
        c.setText("text")
        c.setChatShown(false)
        c.send()
        c.setChatShown(true)
        c.update(model("t1", mailAcc.id), actions: Capabilities.Actions(), account: mailAcc)
        c.send()
        try await Task.sleep(for: .milliseconds(30))
        #expect(h.host.log.isEmpty && h.host.methods.isEmpty)
    }

    /// A Send ends in the conversation it started in, even when another is
    /// shown by then; that one's text and bar are left alone.
    @Test func sendingPerThreadAcrossASwitch() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.gate(API.MessageSend.name)
        let c = h.shown()
        c.setText("to t1")
        c.send()

        c.show("t2")
        c.update(model("t2", mailAcc.id), actions: mailActions, account: mailAcc)
        #expect(!c.state.sending && c.state.text.isEmpty, "t2 sending")
        c.setText("to t2")
        c.send() // t2 sends on its own
        c.show("t1")
        #expect(c.state.sending, "t1 not sending")
        c.show("t2")
        await h.script.open(API.MessageSend.name)
        try await h.host.drain()
        try await h.host.drain()
        #expect(!c.state.sending && c.state.text.isEmpty, "t2 after")
        c.show("t1")
        #expect(!c.state.sending && c.state.text.isEmpty, "t1 after")
        #expect(h.host.sent.count == 2 && c.keptThreads.isEmpty)
    }

    /// One conversation's failure while another is shown keeps its text
    /// there.
    @Test func failureAfterASwitch() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        h.host.holdBefore = true
        await h.script.set(API.DraftCreate.name, error: RPCError(code: .serverError, message: "down"))
        let c = h.shown()
        c.setText("to t1")
        c.send()
        c.show("t2")
        c.update(model("t2", mailAcc.id), actions: mailActions, account: mailAcc)
        c.setText("to t2")
        h.host.beforeDone[0]() // the download ends while t2 is shown
        #expect(h.host.attributions == ["m2"])
        try await h.host.drain()
        #expect(c.state.text == "to t2" && !c.state.sending, "t2")
        c.show("t1")
        #expect(c.state.text == "to t1" && !c.state.sending && h.host.failed.count == 1, "t1")
    }

    /// What was typed while sending stays after a success.
    @Test func textTypedWhileSendingStays() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.gate(API.DraftSave.name)
        let c = h.shown()
        c.setText("first")
        c.send()
        c.setText("first and more")
        await h.script.open(API.DraftSave.name)
        try await h.host.drain()
        #expect(c.state.text == "first and more" && h.host.sent.count == 1)
    }

    @Test func openWindowAndForward() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = h.shown()
        c.setMode(.replyAll)
        c.setText("a < b\n\nc")

        c.openWindow()
        try #require(h.host.opens.count == 1)
        var o = h.host.opens[0]
        #expect(o.mode == .replyAll && o.id == "m2" && o.ownHTML == "<p>a &lt; b</p><p>c</p>")
        // No window yet: the text stays.
        #expect(c.state.text == "a < b\n\nc", "cleared before the window opened")
        o.opened()
        #expect(c.state.text.isEmpty, "not cleared once opened")

        // Forward: the forward mode, the text above it; a window that never
        // opens leaves the text.
        c.setText("fyi")
        c.forward()
        o = h.host.opens[1]
        #expect(o.mode == .forward && o.ownHTML == "<p>fyi</p>")
        #expect(c.state.text == "fyi" && c.state.mode == .replyAll)

        // Opened after the conversation changed: it clears its own text only.
        c.show("t2")
        c.update(model("t2", mailAcc.id), actions: mailActions, account: mailAcc)
        c.setText("t2 text")
        o.opened()
        #expect(c.state.text == "t2 text")
        c.show("t1")
        #expect(c.state.text.isEmpty)

        // An empty bar opens an empty editor; no draft is saved by the bar.
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        c.openWindow()
        #expect(h.host.opens[2].ownHTML.isEmpty)
        #expect(h.host.methods.isEmpty)
    }

    @Test func openGuards() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let c = QuickReplyController(host: h.host)
        c.update(model("t1", mailAcc.id), actions: mailActions, account: mailAcc)
        c.openWindow() // timeline: hidden
        c.setChatShown(true)
        c.update(model("t1", mailAcc.id), actions: Capabilities.Actions(reply: true), account: mailAcc)
        c.forward() // the account does not forward
        try #require(h.host.opens.isEmpty)
        // While sending neither opens.
        await h.script.gate(API.DraftCreate.name)
        c.setText("x")
        c.send()
        c.openWindow()
        await h.script.open(API.DraftCreate.name)
        try await h.host.drain()
        #expect(h.host.opens.isEmpty, "opened while sending")
        // A comment opens the window as a reply (the window decides it is a
        // comment).
        c.update(model("t1", jiraAcc.id), actions: commentActions, account: jiraAcc)
        c.openWindow()
        #expect(h.host.opens.count == 1 && h.host.opens.first?.mode == .reply)
    }
}
