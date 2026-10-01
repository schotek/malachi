// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The quoted history under a body (quoted.go, ConversationQuoted.swift):
// the button's offer and label, what a view revealed, the two variants of
// a cached body, and the cache and the conversation asking message.body
// for the variant on display (`trimQuoted`) over a FakeDaemon. The tests
// run without a catalogue (L10n falls back to the msgids).

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let account = AccountID("acc_1")
private let folder = FolderID("fld_1")

private func summary(_ id: String) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: account, folderId: folder,
        from: [Address(name: "Alice", address: "alice@example.com")], subject: "Hello \(id)",
        date: Date(timeIntervalSince1970: 1_700_000_000), snippet: "", flags: [], hasAttachments: false, size: 10)
}

/// The body of `id`: trimmed (`cut` says whether anything was) or whole,
/// under `remote`.
private func body(
    _ id: String, whole: Bool, cut: Bool = true, remote: RemoteContentPolicy = .block
) -> MessageBodyResult {
    MessageBodyResult(
        messageId: MessageID(id), bodyState: .fetched, hasHtml: true,
        html: whole ? "<p>new</p><blockquote>old</blockquote>" : "<p>new</p>",
        text: whole ? "new\n> old" : "new", remoteContent: remote, sanitizerVersion: "1",
        quotedTrimmed: whole ? nil : cut)
}

/// The message.body requests a daemon answered: whether `trimQuoted` was
/// asked for, and the remote-content policy.
private actor Requests {
    var all: [(trim: Bool, policy: RemoteContentPolicy?)] = []

    func add(_ p: MessageBodyParams) { all.append((p.trimQuoted == true, p.remoteContent)) }
}

/// A daemon answering message.body for any message: trimmed with
/// `trimQuoted`, else whole (`cut` for the trimmed answer).
@MainActor
private final class Harness {
    let daemon: FakeDaemon
    let client: RPCClient
    let cache: MessageCache
    let requests = Requests()
    var loaded: [MessageID] = []

    init(cut: Bool = true) async throws {
        daemon = try FakeDaemon()
        client = RPCClient(socketPath: daemon.path)
        cache = MessageCache(client: client) { _ in }
        cache.onLoaded = { [unowned self] id, _ in self.loaded.append(id) }
        let requests = requests
        await daemon.on(API.MessageBody.name) { params in
            let p = try JSONCoding.decoder().decode(MessageBodyParams.self, from: params)
            await requests.add(p)
            let b = body(
                p.messageId.rawValue, whole: p.trimQuoted != true, cut: cut,
                remote: p.remoteContent == .allow ? .allow : .block)
            return try JSONCoding.encoder().encode(b)
        }
        try await daemon.start()
        try await client.connect()
    }

    func stop() async {
        await client.close()
        await daemon.stop()
    }

    func bodyCalls() async -> Int {
        await daemon.calls.filter { $0 == API.MessageBody.name }.count
    }
}

struct QuotedTextLogicTests {
    @Test func labels() {
        #expect(Conversation.quotedTextLabel(shown: false) == "Show Quoted Text")
        #expect(Conversation.quotedTextLabel(shown: true) == "Hide Quoted Text")
        #expect(QuotedTextOffer.show.label == "Show Quoted Text")
        #expect(QuotedTextOffer.hide.label == "Hide Quoted Text")
    }

    @Test func revealHoldsForTheSelection() {
        var r = QuotedReveal()
        r.show("t1")
        #expect(!r.isRevealed("a"))
        r.set("a", true)
        r.set("b", true)
        r.set("b", false)
        r.set("", true)
        #expect(r.isRevealed("a") && !r.isRevealed("b") && !r.isRevealed(""))
        r.show("t1")
        #expect(r.isRevealed("a"), "an update of the same selection keeps it")
        r.show("t2")
        #expect(!r.isRevealed("a"), "another selection forgets it")
        r.set("a", true)
        r.clear()
        #expect(!r.isRevealed("a"))
        r.show("t2")
        #expect(!r.isRevealed("a"), "cleared, the same selection starts anew")
    }

    @Test func apiCoding() throws {
        let trim = try JSONCoding.encoder().encode(MessageBodyParams(accountId: account, messageId: "m", trimQuoted: true))
        let trimObj = try #require(JSONSerialization.jsonObject(with: trim) as? [String: Any])
        #expect(trimObj["trimQuoted"] as? Bool == true)
        for p in [
            MessageBodyParams(accountId: account, messageId: "m"),
            MessageBodyParams(accountId: account, messageId: "m", trimQuoted: false),
        ] {
            #expect(p.trimQuoted == nil, "false is left out")
            let obj = try #require(
                JSONSerialization.jsonObject(with: try JSONCoding.encoder().encode(p)) as? [String: Any])
            #expect(obj["trimQuoted"] == nil)
        }
        let old = #"""
        {"messageId":"m_1","bodyState":"fetched","hasHtml":false,"text":"x",
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
         "links":[],"remoteContent":"block","sanitizerVersion":"1"}
        """#
        let r = try JSONCoding.decoder().decode(MessageBodyResult.self, from: Data(old.utf8))
        #expect(r.quotedTrimmed == nil && !r.isQuotedTrimmed, "an older daemon: absent is false")
        let cut = try JSONCoding.decoder().decode(
            MessageBodyResult.self, from: Data(old.replacingOccurrences(of: #""sanitizerVersion":"1""#, with: #""sanitizerVersion":"1","quotedTrimmed":true"#).utf8))
        #expect(cut.isQuotedTrimmed)
    }

    @MainActor @Test func offer() {
        #expect(quotedTextOffer(nil) == nil)
        let lm = LoadedMessage()
        #expect(quotedTextOffer(lm) == nil, "nothing yet")
        lm.body = body("a", whole: false, cut: false)
        #expect(quotedTextOffer(lm) == nil, "nothing was cut")
        lm.body = body("a", whole: false)
        #expect(quotedTextOffer(lm) == .show)
        lm.err = RPCError(code: .internalError, message: "x")
        #expect(quotedTextOffer(lm) == nil, "a failed body")
        lm.err = nil
        lm.showQuoted(true)
        #expect(lm.body == nil && quotedTextOffer(lm) == .hide, "the whole body on its way")
        lm.err = RPCError(code: .internalError, message: "x")
        #expect(quotedTextOffer(lm) == .hide, "failed: the way back stays")
    }

    @MainActor @Test func variantsTradePlaces() {
        let trimmed = body("a", whole: false)
        let whole = body("a", whole: true)
        let lm = LoadedMessage(body: trimmed)
        let small = lm.size
        #expect(!lm.showQuoted(false), "already trimmed")
        #expect(lm.showQuoted(true) && lm.quotedShown && lm.body == nil && lm.otherBody == trimmed)
        #expect(lm.switchPolicy == nil, "no images loaded")
        lm.store(whole, quoted: true)
        #expect(lm.body == whole && lm.size > small, "both variants count")
        lm.showQuoted(false)
        #expect(lm.body == trimmed && lm.otherBody == whole, "back without asking")
        // An answer for the variant left goes aside.
        let allowed = body("a", whole: true, remote: .allow)
        lm.store(allowed, quoted: true)
        #expect(lm.body == trimmed && lm.otherBody == allowed)
        // Under another policy the variant aside goes.
        let trimmedAllowed = body("a", whole: false, remote: .allow)
        lm.store(trimmedAllowed, quoted: false, replacing: true)
        #expect(lm.body == trimmedAllowed && lm.otherBody == nil)
        lm.showQuoted(true)
        #expect(lm.switchPolicy == .allow, "the images stay loaded in the whole body")
    }
}

@MainActor
@Suite(.serialized) struct QuotedTextCacheTests {
    @Test func fetchAsksForTheVariantOnDisplay() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let s = summary("a")
        var got: LoadedMessage?
        h.cache.fetchBody(s) { got = $0 }
        try await waitUntil { got?.body != nil }
        #expect(got?.body?.isQuotedTrimmed == true && quotedTextOffer(got) == .show)
        #expect(await h.requests.all.map(\.trim) == [true], "trimmed by default")

        // Show Quoted Text: the whole body is asked for.
        got = nil
        h.cache.fetchBody(s, quoted: true) { got = $0 }
        try await waitUntil { got?.body != nil }
        #expect(got?.body?.text == "new\n> old" && quotedTextOffer(got) == .hide)
        #expect(await h.requests.all.map(\.trim) == [true, false])

        // Hide and show again: both are held, nothing is asked.
        let before = h.loaded.count
        got = nil
        h.cache.fetchBody(s, quoted: false) { got = $0 }
        #expect(got?.body?.text == "new", "at once")
        #expect(h.loaded.count == before + 1, "the views showing it hear about it")
        h.cache.fetchBody(s, quoted: true) { got = $0 }
        #expect(got?.body?.text == "new\n> old")
        // nil keeps the variant.
        h.cache.fetch(s) { got = $0 }
        #expect(got?.quotedShown == true)
        #expect(await h.bodyCalls() == 2)
    }

    @Test func remoteImagesReaskTheVariantShown() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let s = summary("a")
        var got: LoadedMessage?
        h.cache.fetchBody(s) { got = $0 }
        try await waitUntil { got?.body != nil }
        h.cache.fetchBody(s, quoted: true) { got = $0 }
        try await waitUntil { got?.body?.text == "new\n> old" }

        var done: LoadedMessage?
        h.cache.loadImages(s) { done = try? $0.get() }
        try await waitUntil { done != nil }
        let lm = try #require(done)
        #expect(lm.quotedShown && lm.body?.remoteContent == .allow && lm.body?.text == "new\n> old")
        #expect(lm.otherBody == nil, "the trimmed body under block went")
        // Hide: the trimmed body is asked for again, with the images.
        got = nil
        h.cache.fetchBody(s, quoted: false) { got = $0 }
        try await waitUntil { got?.body != nil }
        #expect(got?.body?.text == "new" && got?.body?.remoteContent == .allow)
        let all = await h.requests.all
        #expect(all.map(\.trim) == [true, false, false, true])
        #expect(all.map(\.policy) == [nil, nil, .allow, .allow])
    }

    @Test func nothingCutNoButton() async throws {
        let h = try await Harness(cut: false)
        defer { Task { await h.stop() } }
        var got: LoadedMessage?
        h.cache.fetchBody(summary("a")) { got = $0 }
        try await waitUntil { got?.body != nil }
        #expect(quotedTextOffer(got) == nil)
    }
}
