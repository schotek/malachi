// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The loaded-message cache over a FakeDaemon (ui/internal/window/
// message_view.go `fetchMessage`/`settleLoaded`, remote.go
// `loadRemoteImages`, outbox.go `refetchMessage`).

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

private func message(_ id: String) -> Message {
    Message(summary: summary(id), cc: [Address(address: "cc@example.com")], attachments: [])
}

private func body(_ id: String, html: String? = nil, remote: RemoteContentPolicy = .block) -> MessageBodyResult {
    MessageBodyResult(
        messageId: MessageID(id), bodyState: .fetched, hasHtml: html != nil, html: html, text: "text of \(id)",
        blocked: BlockedContent(remoteImages: remote == .block ? 2 : 0), remoteContent: remote, sanitizerVersion: "1")
}

private func encode<T: Encodable>(_ value: T) throws -> Data {
    try JSONCoding.encoder().encode(value)
}

/// What the cache reported, in order.
@MainActor
private final class Log {
    var toasts: [String] = []
    var loaded: [MessageID] = []
    var bars: [(MessageID, Bool)] = []
}

/// A daemon serving `message.get` and `message.body` from tables, a
/// connected client and a cache.
@MainActor
private final class Harness {
    let daemon: FakeDaemon
    let client: RPCClient
    let cache: MessageCache
    let log = Log()

    init() async throws {
        daemon = try FakeDaemon()
        client = RPCClient(socketPath: daemon.path)
        let log = log
        cache = MessageCache(client: client) { log.toasts.append($0) }
        cache.onLoaded = { id, _ in log.loaded.append(id) }
        cache.onRemoteBar = { id, lm in log.bars.append((id, lm.loadingImages)) }
    }

    /// Serves message.get and message.body for `id`, each after `delay`;
    /// `getFails`/`bodyFails` answer with an internal error instead. The
    /// body under `remoteContent: allow` carries `allowHTML` and no
    /// blocked images.
    func serve(
        _ id: String, delay: Duration = .zero, getFails: Bool = false, bodyFails: Bool = false, html: String? = nil,
        allowHTML: String? = nil, allowFails: Bool = false
    ) async throws {
        let get = try encode(MessageGetResult(message: message(id)))
        let block = try encode(body(id, html: html))
        let allow = try encode(body(id, html: allowHTML ?? html, remote: .allow))
        await daemon.on(API.MessageGet.name) { _ in
            if delay > .zero { try await Task.sleep(for: delay) }
            if getFails { throw RPCError(code: .internalError, message: "get failed") }
            return get
        }
        await daemon.on(API.MessageBody.name) { params in
            if delay > .zero { try await Task.sleep(for: delay) }
            let p = try JSONCoding.decoder().decode(MessageBodyParams.self, from: params)
            if p.remoteContent == .allow {
                if allowFails { throw RPCError(code: .networkError, message: "no network") }
                return allow
            }
            if bodyFails { throw RPCError(code: .internalError, message: "body failed") }
            return block
        }
    }

    func start() async throws {
        try await daemon.start()
        try await client.connect()
    }

    func stop() async {
        await client.close()
        await daemon.stop()
    }

    func calls(_ method: String) async -> Int {
        await daemon.calls.filter { $0 == method }.count
    }
}

@MainActor
@Suite(.serialized) struct MessageCacheTests {
    @Test func fetchRunsBothHalvesOnceAndAnswersEveryWaiter() async throws {
        let h = try await Harness()
        try await h.serve("m1", delay: .milliseconds(60))
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m1")
        var first: [Bool] = []
        var second: [Bool] = []
        h.cache.fetch(s) { first.append($0.complete) }
        // A second request while the first runs joins it.
        h.cache.fetch(s) { second.append($0.complete) }
        #expect(h.cache.loaded(s.id)?.getting == true)
        #expect(h.cache.loaded(s.id)?.fetching == true)

        try await waitUntil { h.cache.loaded(s.id)?.complete == true && first.count == 2 && second.count == 2 }
        // Once per half, for both waiters: the first answer is a partial
        // entry, the second the complete one.
        #expect(first == [false, true])
        #expect(second == [false, true])
        #expect(await h.calls(API.MessageGet.name) == 1)
        #expect(await h.calls(API.MessageBody.name) == 1)
        let lm = try #require(h.cache.loaded(s.id))
        #expect(lm.msg?.summary.subject == "Hello m1")
        #expect(lm.msg?.cc?.first?.address == "cc@example.com")
        #expect(lm.body?.text == "text of m1")
        #expect(lm.err == nil)
        #expect(h.log.loaded == [s.id, s.id])

        // A complete entry answers at once and asks the daemon nothing.
        var third = 0
        h.cache.fetch(s) { _ in third += 1 }
        #expect(third == 1)
        try await Task.sleep(for: .milliseconds(30))
        #expect(await h.calls(API.MessageGet.name) == 1)
        #expect(await h.calls(API.MessageBody.name) == 1)
        #expect(h.log.loaded.count == 2)
        #expect(h.log.toasts.isEmpty)
    }

    @Test func bodyFailureIsRetriedOnTheNextFetch() async throws {
        let h = try await Harness()
        try await h.serve("m2", bodyFails: true)
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m2")
        var seen: [Bool] = []
        h.cache.fetch(s) { seen.append($0.bodySettled) }
        try await waitUntil { h.cache.loaded(s.id)?.bodySettled == true && h.cache.loaded(s.id)?.msg != nil }
        let lm = try #require(h.cache.loaded(s.id))
        #expect(lm.body == nil)
        #expect((lm.err as? RPCError)?.code == .internalError)
        #expect(!lm.complete)
        // The failure is the pane's to show, not a toast.
        #expect(h.log.toasts.isEmpty)

        // The next fetch retries the body and keeps the headers.
        try await h.serve("m2")
        var again: [Bool] = []
        h.cache.fetch(s) { again.append($0.complete) }
        #expect(lm.err == nil) // cleared before the retry
        #expect(lm.fetching)
        try await waitUntil { lm.complete }
        #expect(again == [true])
        #expect(await h.calls(API.MessageGet.name) == 1)
        #expect(await h.calls(API.MessageBody.name) == 2)
        #expect(lm.body?.text == "text of m2")
    }

    @Test func getFailureKeepsTheBodyAndRetriesTheHeaders() async throws {
        let h = try await Harness()
        try await h.serve("m3", getFails: true)
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m3")
        var answers = 0
        h.cache.fetch(s) { _ in answers += 1 }
        try await waitUntil { answers == 2 }
        let lm = try #require(h.cache.loaded(s.id))
        #expect(lm.msg == nil)
        #expect(lm.body?.text == "text of m3")
        #expect(lm.err == nil)
        #expect(!lm.getting && !lm.fetching)

        try await h.serve("m3")
        h.cache.fetch(s) { _ in answers += 1 }
        try await waitUntil { lm.complete }
        #expect(answers == 3)
        #expect(await h.calls(API.MessageGet.name) == 2)
        #expect(await h.calls(API.MessageBody.name) == 1)
    }

    @Test func evictedEntryIsStoredBackWhenItsHalfArrives() async throws {
        let h = try await Harness()
        try await h.serve("m4", delay: .milliseconds(60))
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m4")
        var answers = 0
        h.cache.fetch(s) { _ in answers += 1 }
        let inFlight = try #require(h.cache.loaded(s.id))
        h.cache.evict(s.id)
        #expect(h.cache.loaded(s.id) == nil)

        try await waitUntil { answers == 2 }
        // The same entry, complete, is back in the cache.
        #expect(h.cache.loaded(s.id) === inFlight)
        #expect(inFlight.complete)
        #expect(h.cache.cache.count == 1)
    }

    @Test func loadImagesReplacesTheBodyAndShowsTheWait() async throws {
        let h = try await Harness()
        try await h.serve("m5", html: "<p>blocked</p>", allowHTML: "<p>with pictures</p>")
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m5")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let lm = try #require(h.cache.loaded(s.id))
        #expect(loadableImages(lm.body) == 2)

        var outcome: Result<LoadedMessage, any Error>?
        h.cache.loadImages(s) { outcome = $0 }
        // The bar shows the wait from the click on.
        #expect(lm.loadingImages)
        #expect(h.log.bars.map(\.1) == [true])
        // A second click while the daemon works is ignored.
        h.cache.loadImages(s) { _ in Issue.record("a second request was started") }

        try await waitUntil { outcome != nil }
        guard case .success(let got)? = outcome else {
            Issue.record("loadImages failed")
            return
        }
        #expect(got === lm)
        #expect(!lm.loadingImages)
        #expect(lm.body?.html == "<p>with pictures</p>")
        #expect(lm.body?.remoteContent == .allow)
        #expect(lm.err == nil)
        #expect(loadableImages(lm.body) == 0)
        // Two settles of the fetch, then the images.
        #expect(h.log.loaded == [s.id, s.id, s.id])
        #expect(h.log.bars.count == 1)
        #expect(await h.calls(API.MessageBody.name) == 2)
        #expect(h.log.toasts.isEmpty)
    }

    @Test func loadImagesFailureToastsAndOffersTheImagesAgain() async throws {
        let h = try await Harness()
        try await h.serve("m6", html: "<p>blocked</p>", allowFails: true)
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m6")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let lm = try #require(h.cache.loaded(s.id))

        var outcome: Result<LoadedMessage, any Error>?
        h.cache.loadImages(s) { outcome = $0 }
        try await waitUntil { outcome != nil }
        guard case .failure(let err)? = outcome else {
            Issue.record("loadImages succeeded")
            return
        }
        #expect((err as? RPCError)?.code == .networkError)
        #expect(h.log.toasts == [rpcErrorText(L10n.T("Loading the images"), err)])
        // The body on display stays, the flag is cleared and the bar was
        // redrawn on the way in and on the way out.
        #expect(lm.body?.html == "<p>blocked</p>")
        #expect(!lm.loadingImages)
        #expect(h.log.bars.map(\.1) == [true, false])
        #expect(h.log.loaded.count == 2)
    }

    @Test func refetchDropsTheHeadersAndKeepsTheBody() async throws {
        let h = try await Harness()
        try await h.serve("m7")
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m7")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let lm = try #require(h.cache.loaded(s.id))
        let body = lm.body

        var answers: [Bool] = []
        h.cache.refetch(s) { answers.append($0.complete) }
        #expect(lm.msg == nil)
        #expect(lm.getting)
        #expect(lm.body == body) // untouched
        try await waitUntil { lm.complete }
        #expect(answers == [true])
        #expect(await h.calls(API.MessageGet.name) == 2)
        #expect(await h.calls(API.MessageBody.name) == 1)

        // By id: only for a message whose full message is cached.
        #expect(h.cache.summary(s.id)?.subject == "Hello m7")
        h.cache.refetch(MessageID("unknown")) { _ in Issue.record("refetched an unknown message") }
        h.cache.refetch(s.id) { _ in }
        try await waitUntil { lm.complete }
        #expect(await h.calls(API.MessageGet.name) == 3)
    }

    @Test func partsAndAttachedMessagesComeThrough() async throws {
        let h = try await Harness()
        let part = MessagePartResult(partId: "2", contentType: "image/png; x=y", filename: "a.png", size: 3, data: Data([1, 2, 3]))
        let embedded = MessageEmbeddedResult(partId: "3", message: message("inner"), body: body("inner"))
        let partJSON = try encode(part)
        let embeddedJSON = try encode(embedded)
        await h.daemon.on(API.MessagePart.name) { _ in partJSON }
        await h.daemon.on(API.MessageEmbedded.name) { params in
            let p = try JSONCoding.decoder().decode(MessageEmbeddedParams.self, from: params)
            guard p.partId == "3", p.remoteContent == .allow else {
                throw RPCError(code: .invalidArgument, message: "unexpected params")
            }
            return embeddedJSON
        }
        try await h.start()
        defer { Task { await h.stop() } }

        let got = try await h.cache.fetchPart(accountID: account, messageID: MessageID("m8"), partID: "2")
        #expect(got.contentType == "image/png; x=y")
        #expect(got.data == Data([1, 2, 3]))
        let res = try await h.cache.fetchEmbedded(accountID: account, messageID: MessageID("m8"), partID: "3", remote: .allow)
        #expect(res == embedded)
    }
}
