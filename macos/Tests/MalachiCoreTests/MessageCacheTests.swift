// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The loaded-message cache over a FakeDaemon (ui/internal/window/
// message_view.go `fetchMessage`/`settleLoaded`, remote.go
// `loadRemoteImages`, outbox.go `refetchMessage`, download.go `download`,
// `partData`, `embeddedData`).

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
    /// Every `onChips`, with whether the spinner showed then.
    var chips: [(MessageID, Bool)] = []
}

/// The daemon's side of message.download, message.part and
/// message.embedded for one message with a part kept on the server: the
/// part answers partNotDownloaded until the message was downloaded.
private actor DownloadScript {
    var downloaded = false
    var delay: Duration = .zero
    var failure: RPCError?
    /// message.download, message.part:<id> and message.embedded:<id>, in order.
    var log: [String] = []
    /// The message the download answers with.
    var message: Message

    init(message: Message) {
        self.message = message
    }

    func set(delay: Duration) { self.delay = delay }
    func set(failure: RPCError?) { self.failure = failure }
    func set(downloaded: Bool) { self.downloaded = downloaded }

    func download() async throws -> Data {
        log.append("message.download")
        if delay > .zero {
            try await Task.sleep(for: delay)
        }
        if let failure {
            throw failure
        }
        downloaded = true
        return try JSONCoding.encoder().encode(MessageDownloadResult(message: message))
    }

    func part(_ params: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(MessagePartParams.self, from: params)
        log.append("message.part:" + p.partId)
        guard downloaded else {
            throw RPCError(code: .partNotDownloaded, message: "on the server")
        }
        return try JSONCoding.encoder().encode(
            MessagePartResult(partId: p.partId, contentType: "application/pdf", filename: "big.pdf", size: 3, data: Data([7, 8, 9])))
    }

    func embedded(_ params: Data) throws -> Data {
        let p = try JSONCoding.decoder().decode(MessageEmbeddedParams.self, from: params)
        log.append("message.embedded:" + p.partId)
        guard downloaded else {
            throw RPCError(code: .partNotDownloaded, message: "on the server")
        }
        return try JSONCoding.encoder().encode(MessageEmbeddedResult(
            partId: p.partId, message: Message(summary: summary("inner")),
            body: MessageBodyResult(messageId: "inner", bodyState: .fetched, hasHtml: false, text: "inner", remoteContent: .block, sanitizerVersion: "1")))
    }
}

private func bigAttachment(_ id: String = "2", remote: Bool? = true) -> MalachiCore.Attachment {
    MalachiCore.Attachment(partId: id, filename: "big.pdf", contentType: "application/pdf", size: 300_000, inline: false, remote: remote)
}

/// A daemon serving `message.get` and `message.body` from tables, a
/// connected client and a cache.
@MainActor
private final class Harness {
    let daemon: FakeDaemon
    let client: RPCClient
    let cache: MessageCache
    let log = Log()

    init(spinnerDelay: Duration = MessageCache.downloadSpinnerDelay) async throws {
        daemon = try FakeDaemon()
        client = RPCClient(socketPath: daemon.path)
        let log = log
        let cache = MessageCache(client: client, spinnerDelay: spinnerDelay) { log.toasts.append($0) }
        self.cache = cache
        cache.onLoaded = { id, _ in log.loaded.append(id) }
        cache.onRemoteBar = { id, lm in log.bars.append((id, lm.loadingImages)) }
        cache.onChips = { [weak cache] id, _ in log.chips.append((id, cache?.showsDownload(id) ?? false)) }
    }

    /// Serves message.download, message.part and message.embedded from
    /// `script`.
    func serveDownloads(_ script: DownloadScript) async {
        await daemon.on(API.MessageDownload.name) { _ in try await script.download() }
        await daemon.on(API.MessagePart.name) { try await script.part($0) }
        await daemon.on(API.MessageEmbedded.name) { try await script.embedded($0) }
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

    // MARK: Downloads (download.go)

    /// One message.download however often it is asked for while it runs;
    /// the chips show the spinner only once it took the delay, and the
    /// cached message is replaced with the answer.
    @Test func downloadIsSharedAndShowsTheSpinnerLate() async throws {
        let h = try await Harness(spinnerDelay: .milliseconds(80))
        try await h.serve("m9")
        var after = message("m9")
        after.attachments = [bigAttachment(remote: nil)]
        let script = DownloadScript(message: after)
        await script.set(delay: .milliseconds(400))
        await h.serveDownloads(script)
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m9")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let lm = try #require(h.cache.loaded(s.id))
        lm.msg?.attachments = [bigAttachment()]

        var answers: [Message] = []
        var failures = 0
        for _ in 0..<2 {
            Task {
                do {
                    answers.append(try await h.cache.download(accountID: account, messageID: s.id))
                } catch {
                    failures += 1
                }
            }
        }
        try await waitUntil { h.cache.downloading(s.id) }
        #expect(!h.cache.showsDownload(s.id), "no spinner before the delay")
        try await waitUntil { h.cache.showsDownload(s.id) }
        #expect(h.log.chips.map(\.1) == [true])
        try await waitUntil { answers.count == 2 }
        #expect(failures == 0)
        #expect(answers.allSatisfy { $0 == after })
        #expect(await script.log == ["message.download"], "one call for both")
        #expect(!h.cache.showsDownload(s.id) && !h.cache.downloading(s.id))
        #expect(h.log.chips.map(\.0) == [s.id, s.id])
        #expect(h.log.chips.map(\.1) == [true, false], "drawn with the spinner, then without")
        // The cached message is the downloaded one; the body stays.
        #expect(lm.msg == after && lm.msg?.attachments.first?.isRemote == false)
        #expect(lm.body?.text == "text of m9")
        #expect(await h.calls(API.MessageBody.name) == 1)
        #expect(h.log.toasts.isEmpty, "the callers say what failed, not the cache")
    }

    @Test func aQuickDownloadNeverShowsTheSpinner() async throws {
        let h = try await Harness(spinnerDelay: .milliseconds(300))
        try await h.serve("m10")
        let script = DownloadScript(message: message("m10"))
        await h.serveDownloads(script)
        try await h.start()
        defer { Task { await h.stop() } }
        _ = try await h.cache.download(accountID: account, messageID: "m10")
        #expect(h.log.chips.count == 1 && h.log.chips.first?.1 == false)
        try await Task.sleep(for: .milliseconds(400))
        #expect(h.log.chips.count == 1, "the delayed spinner of a finished download never comes")
        #expect(!h.cache.showsDownload("m10"))
    }

    /// A body that was not downloaded is dropped after the download and
    /// asked for again; every view hears about it through onLoaded.
    @Test func anUnfetchedBodyIsFetchedAgainAfterTheDownload() async throws {
        let h = try await Harness(spinnerDelay: .milliseconds(10))
        let pending = try encode(MessageBodyResult(
            messageId: "m11", bodyState: .pending, hasHtml: false, text: "", remoteContent: .block, sanitizerVersion: "1"))
        let fetched = try encode(body("m11"))
        let get = try encode(MessageGetResult(message: message("m11")))
        let script = DownloadScript(message: message("m11"))
        await h.serveDownloads(script)
        await h.daemon.on(API.MessageGet.name) { _ in get }
        await h.daemon.on(API.MessageBody.name) { _ in await script.downloaded ? fetched : pending }
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m11")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let lm = try #require(h.cache.loaded(s.id))
        #expect(lm.body?.bodyState == .pending)
        let loadedBefore = h.log.loaded.count

        _ = try await h.cache.download(accountID: account, messageID: s.id)
        try await waitUntil { lm.body?.bodyState == .fetched }
        #expect(lm.body?.text == "text of m11")
        #expect(await h.calls(API.MessageBody.name) == 2)
        #expect(await h.calls(API.MessageGet.name) == 1, "the message came with the download")
        #expect(h.log.loaded.count == loadedBefore + 1)
    }

    @Test func aFailedDownloadReachesEveryCallerAndStopsTheSpinner() async throws {
        let h = try await Harness(spinnerDelay: .milliseconds(20))
        try await h.serve("m12")
        let script = DownloadScript(message: message("m12"))
        await script.set(delay: .milliseconds(150))
        await script.set(failure: RPCError(code: .offline, message: "no network"))
        await h.serveDownloads(script)
        try await h.start()
        defer { Task { await h.stop() } }
        let s = summary("m12")
        h.cache.fetch(s) { _ in }
        try await waitUntil { h.cache.loaded(s.id)?.complete == true }
        let before = h.cache.loaded(s.id)?.msg

        var codes: [ErrorCode] = []
        for _ in 0..<2 {
            Task {
                do {
                    _ = try await h.cache.download(accountID: account, messageID: s.id)
                } catch {
                    codes.append((error as? RPCError)?.code ?? 0)
                }
            }
        }
        try await waitUntil { codes.count == 2 }
        #expect(codes == [.offline, .offline])
        #expect(await script.log == ["message.download"])
        #expect(!h.cache.showsDownload(s.id) && !h.cache.downloading(s.id))
        #expect(h.log.chips.map(\.1) == [true, false])
        #expect(h.cache.loaded(s.id)?.msg == before, "nothing replaced")
        #expect(h.log.toasts.isEmpty)

        // The next request asks again.
        await script.set(failure: nil)
        await script.set(delay: .zero)
        _ = try await h.cache.download(accountID: account, messageID: s.id)
        #expect(await script.log == ["message.download", "message.download"])
    }

    /// download.go `partData`: a remote part downloads first; a part the
    /// daemon answers partNotDownloaded for gets one download and a retry.
    @Test func partDataDownloadsWhatIsOnTheServer() async throws {
        let h = try await Harness(spinnerDelay: .milliseconds(500))
        try await h.serve("m13")
        var after = message("m13")
        after.attachments = [bigAttachment(remote: nil)]
        let script = DownloadScript(message: after)
        await h.serveDownloads(script)
        try await h.start()
        defer { Task { await h.stop() } }

        let res = try await h.cache.partData(accountID: account, messageID: "m13", attachment: bigAttachment(), onServer: true)
        #expect(res.data == Data([7, 8, 9]) && res.partId == "2")
        #expect(await script.log == ["message.download", "message.part:2"])

        // The chip said local, the daemon disagrees: one download, one retry.
        await script.set(downloaded: false)
        let again = try await h.cache.partData(accountID: account, messageID: "m13", attachment: bigAttachment(remote: nil), onServer: false)
        #expect(again.data == Data([7, 8, 9]))
        #expect(await script.log.suffix(3) == ["message.part:2", "message.download", "message.part:2"])

        // An attached message the same way.
        await script.set(downloaded: false)
        var eml = bigAttachment("3")
        eml.filename = "fwd.eml"
        eml.contentType = "message/rfc822"
        var withEml = message("m13")
        withEml.attachments = [MalachiCore.Attachment(partId: "4", filename: "fwd.eml", contentType: "message/rfc822", size: 300_000, inline: false)]
        let emlScript = DownloadScript(message: withEml)
        await h.serveDownloads(emlScript)
        let embedded = try await h.cache.embeddedData(accountID: account, messageID: "m13", attachment: eml, onServer: true)
        // The download moved the part: the rendered one is the new id.
        #expect(embedded.partId == "4")
        #expect(await emlScript.log == ["message.download", "message.embedded:4"])
    }

    /// attachments.go `savingAll`: one Save All per message at a time, its
    /// buttons disabled wherever the message is shown (the chips drawn
    /// again at the start and at the end).
    @Test func saveAllIsOnePerMessage() async throws {
        let h = try await Harness()
        #expect(!h.cache.isSavingAll("m20"))
        #expect(h.cache.beginSaveAll("m20"))
        #expect(h.cache.isSavingAll("m20"))
        #expect(!h.cache.beginSaveAll("m20"), "a second one waits its turn")
        #expect(h.cache.beginSaveAll("m21"), "another message is another matter")
        h.cache.endSaveAll("m20")
        #expect(!h.cache.isSavingAll("m20") && h.cache.isSavingAll("m21"))
        h.cache.endSaveAll("m20") // nothing to end: no redraw
        #expect(h.log.chips.map(\.0) == ["m20", "m21", "m20"])
        #expect(h.cache.beginSaveAll("m20"))
    }
}
