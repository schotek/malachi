// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The loaded-message cache and its fetching: ui/internal/window/
/// message_view.go (`fetchMessage`, `settleLoaded`, `loadedFor`,
/// `storeLoaded`), remote.go (`loadRemoteImages`, `fetchRemoteImages`,
/// `imagesDone`, `refreshRemoteBar`, `showLoaded`, `downloadPictures`,
/// `lostPicture`, `reloadPictures`), outbox.go
/// (`refetchMessage`), attachments.go (`fetchAttachment`) and embedded.go
/// (`fetchEmbedded`), minus the widgets. The pane, every message window and
/// the compose prefill share one cache; the views register for the answers
/// they asked for and every view showing a message hears about it through
/// `onLoaded`.
///
/// Every RPC runs as a `Task` on the main actor, so the continuation after
/// the call is on the main actor too and the entries are only ever touched
/// there (the GTK window's `glib.IdleAdd` discipline). Callers guard
/// staleness themselves (the pane by its current message, a window by its
/// closed flag): the cache is keyed by id and stays valid whatever is on
/// display now.
@MainActor
public final class MessageCache {
    public typealias Waiter = @MainActor (LoadedMessage) -> Void

    public let client: RPCClient

    /// The entries (`Window.loaded`).
    public private(set) var cache = LoadedCache()

    /// Fired after every settle of a half (message.get or message.body
    /// answered) and after remote images arrived, with the entry as it is
    /// now: every view showing the message re-renders (remote.go
    /// `showLoaded`, generalised).
    public var onLoaded: (@MainActor (MessageID, LoadedMessage) -> Void)?

    /// Fired when only the remote-image bar of a message changed (the
    /// request for its images started or ended without a new body): the
    /// views redraw the bar and leave the body alone (remote.go
    /// `refreshRemoteBar`, which avoids reloading the web view).
    public var onRemoteBar: (@MainActor (MessageID, LoadedMessage) -> Void)?

    /// Fired when the attachment chips of a message have to be drawn again:
    /// its download began to show the spinner, or ended (download.go
    /// `refreshChips`, the pane and the open windows). The entry is nil
    /// when the cache no longer holds the message.
    public var onChips: (@MainActor (MessageID, LoadedMessage?) -> Void)?

    /// How long a download runs before the chips show a spinner
    /// (download.go, message_view.go `bodySpinnerDelay`): a download that
    /// finds nothing missing answers at once and never flashes one.
    public nonisolated static let downloadSpinnerDelay: Duration = .milliseconds(400)

    private let toast: @MainActor (String) -> Void
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "message")

    /// Who wants to hear about the in-flight halves of an entry
    /// (`loadedMessage.waiters`), kept beside the entry so that an entry
    /// evicted while in flight keeps its own waiters.
    private var waiters: [ObjectIdentifier: [Waiter]] = [:]

    /// The message.download calls in flight, one per message
    /// (`Window.downloads`): a second request joins the first.
    private var downloads: [MessageID: Task<Message, any Error>] = [:]
    /// The messages whose chips show the spinner (`spinning`).
    private var spinning: Set<MessageID> = []
    /// The messages whose Save All runs (`Window.savingAll`).
    private var savingAll: Set<MessageID> = []
    private let spinnerDelay: Duration

    /// - Parameters:
    ///   - client: the transport; calls fail with `notConnected` until the
    ///     connection controller reports a connection.
    ///   - toast: shows a transient message (the window's toast overlay).
    ///   - spinnerDelay: `downloadSpinnerDelay`, shorter in tests.
    public init(
        client: RPCClient, spinnerDelay: Duration = MessageCache.downloadSpinnerDelay,
        toast: @escaping @MainActor (String) -> Void
    ) {
        self.client = client
        self.spinnerDelay = spinnerDelay
        self.toast = toast
    }

    // MARK: Lookup

    /// The cached entry of `id`, if any, in whatever state it is.
    public func loaded(_ id: MessageID) -> LoadedMessage? {
        cache[id]
    }

    /// What the cache knows about message `id` beyond the list: the summary
    /// of the cached full message (the second half of message_view.go
    /// `summary`, for a message window outliving the folder it was opened
    /// from).
    public func summary(_ id: MessageID) -> MessageSummary? {
        cache[id]?.msg?.summary
    }

    /// Applies a flag change to the cached full messages of `ids`
    /// (`ActionsController.setSeen`): a message window of a message no
    /// list shows, opened from a notification, reads its flags from here
    /// (`ActionsController.summary`). An entry without its message.get
    /// answer is left alone.
    public func applyFlags(_ ids: [MessageID], set: [Flag] = [], clear: [Flag] = []) {
        for id in ids {
            guard let lm = cache[id], let msg = lm.msg else { continue }
            let (flags, did) = applyFlagChange(msg.summary.flags, set: set, clear: clear)
            if did {
                lm.msg?.summary.flags = flags
            }
        }
    }

    /// Forgets `id` (an entry in flight is put back when its half arrives).
    /// For tests.
    func evict(_ id: MessageID) {
        cache.remove(id)
    }

    /// Forgets every entry of the account's messages (notify.messagesChanged:
    /// the daemon rebuilt them in place, keeping their ids — a Jira pass
    /// with other rendering settings, an edited or re-attributed comment, a
    /// renamed issue; docs/api.md §5): the next fetch asks the daemon
    /// again, so the views showing one are told to show it again after
    /// this (`MailboxController.refreshShown`). An entry with a half in
    /// flight is let go as well: its answer is put back only when nothing
    /// has asked for the message since (`settle`). The daemon emits the
    /// notification after the rebuild, so an answer that arrives later
    /// carries the rebuilt message.
    public func evict(account: AccountID) {
        let gone = cache.removeAll(of: account)
        if !gone.isEmpty {
            log.debug("cache: dropped \(gone.count) messages of \(account.rawValue, privacy: .public)")
        }
    }

    // MARK: Fetching

    /// Runs message.get and message.body for `s` (each only when the cache
    /// lacks it and no request is in flight) and calls `then` on the main
    /// actor after every answer, with the cache entry so far; a complete
    /// entry calls `then` at once (message_view.go `fetchMessage`). A failed
    /// message.get is only logged (the summary headers stay) and retried the
    /// next time; a failed body sets `err` and is retried the same way.
    ///
    /// `quoted` is the variant of the body the view shows: with its quoted
    /// history (true) or without (false, message.body with `trimQuoted`);
    /// nil keeps the variant the entry shows. A switch shows the variant
    /// held already (`LoadedMessage.showQuoted`), else fetches it, and
    /// every view showing the message hears about it (`onLoaded`).
    public func fetch(_ s: MessageSummary, quoted: Bool? = nil, _ then: @escaping Waiter) {
        let id = s.id
        let lm = cache.loadedFor(id)
        lm.accountId = s.accountId
        switchQuoted(id, lm, quoted)
        if lm.complete {
            then(lm)
            return
        }
        waiters[ObjectIdentifier(lm), default: []].append(then)
        if lm.msg == nil, !lm.getting {
            startGet(s.accountId, id, lm)
        }
        if lm.body == nil, !lm.fetching {
            startBody(s, lm)
        }
    }

    /// The summary of message `id` of account `accountId` for a caller that
    /// has nothing but the ids, a clicked desktop notification
    /// (notify_open.go `OpenNotifiedMessage`): the cached one at once, else
    /// message.get, whose answer the cache keeps as `fetch`'s first half
    /// does. `then` gets nil when the daemon no longer has the message (or
    /// cannot say).
    public func lookUp(accountId: AccountID, id: MessageID, _ then: @escaping @MainActor (MessageSummary?) -> Void) {
        if let s = summary(id) {
            then(s)
            return
        }
        let lm = cache.loadedFor(id)
        if lm.accountId == nil {
            lm.accountId = accountId
        }
        // The waiters hear about each half as it arrives, and maybe again
        // later: only the answer of message.get decides, once.
        var decided = false
        waiters[ObjectIdentifier(lm), default: []].append { lm in
            guard !decided, !lm.getting else { return }
            decided = true
            then(lm.msg?.summary)
        }
        if !lm.getting {
            startGet(accountId, id, lm)
        }
    }

    /// `fetch` for the body half alone: message.body when the cache lacks
    /// the body and no request is in flight, never message.get; `then` runs
    /// as `fetch`'s does, at once when the body (or its error, which is
    /// retried the same way) is there already and nothing is in flight. A
    /// card of the conversation view whose message has no attachments needs
    /// nothing message.get adds to its summary.
    public func fetchBody(_ s: MessageSummary, quoted: Bool? = nil, _ then: @escaping Waiter) {
        let lm = cache.loadedFor(s.id)
        lm.accountId = s.accountId
        switchQuoted(s.id, lm, quoted)
        if lm.body != nil, !lm.getting, !lm.fetching {
            then(lm)
            return
        }
        waiters[ObjectIdentifier(lm), default: []].append(then)
        if lm.body == nil, !lm.fetching {
            startBody(s, lm)
        }
    }

    /// Shows the variant `quoted` of the body of `lm` (nil: as it is); a
    /// variant held already is shown at once wherever the message is on
    /// display, one to fetch is the caller's next step.
    private func switchQuoted(_ id: MessageID, _ lm: LoadedMessage, _ quoted: Bool?) {
        guard let quoted, lm.showQuoted(quoted) else { return }
        // The entry grew by the variant it keeps aside.
        cache.prune()
        if lm.body != nil {
            onLoaded?(id, lm)
        }
    }

    /// The message.body parameters for the variant `quoted` of the body of
    /// `s`: without `trimQuoted` only for the whole body.
    private func bodyParams(
        _ s: MessageSummary, quoted: Bool, remoteContent: RemoteContentPolicy? = nil
    ) -> MessageBodyParams {
        MessageBodyParams(accountId: s.accountId, messageId: s.id, remoteContent: remoteContent, trimQuoted: !quoted)
    }

    /// message.get for message `id` of account `accountId` into `lm` (the
    /// first half of `fetch`).
    private func startGet(_ accountId: AccountID, _ id: MessageID, _ lm: LoadedMessage) {
        let client = client
        lm.getting = true
        Task { [weak self] in
            let outcome: Result<MessageGetResult, any Error>
            do {
                outcome = .success(try await client.call(
                    API.MessageGet.self, MessageGetParams(accountId: accountId, messageId: id), timeout: RPCTimeouts.default))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            lm.getting = false
            switch outcome {
            case .failure(let err):
                self.log.warning("message.get: \(String(describing: err), privacy: .public)")
            case .success(let res):
                lm.msg = res.message
            }
            self.settle(id, lm)
        }
    }

    /// message.body for `s` into `lm` (the second half of `fetch`), for
    /// the variant the entry shows. An answer that arrives after the view
    /// switched to the other variant is kept aside for switching back
    /// (`LoadedMessage.store`); its failure is only logged.
    private func startBody(_ s: MessageSummary, _ lm: LoadedMessage) {
        let id = s.id
        let client = client
        let quoted = lm.quotedShown
        let params = bodyParams(s, quoted: quoted, remoteContent: lm.switchPolicy)
        lm.fetching = true
        lm.err = nil // a retry after a failure
        Task { [weak self] in
            let outcome: Result<MessageBodyResult, any Error>
            do {
                outcome = .success(try await client.call(API.MessageBody.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            let current = quoted == lm.quotedShown
            if current {
                lm.fetching = false
            } else {
                lm.fetchingOther = false
            }
            switch outcome {
            case .failure(let err):
                self.log.warning("message.body: \(String(describing: err), privacy: .public)")
                if current {
                    lm.err = err
                }
            case .success(let res):
                lm.store(res, quoted: quoted)
            }
            self.settle(id, lm)
        }
    }

    /// Forgets the cached message.get result of `s` (unless one is in
    /// flight, which then serves) and fetches again; the body stays cached
    /// (outbox.go `refetchMessage`). `then` runs as `fetch`'s does.
    public func refetch(_ s: MessageSummary, _ then: @escaping Waiter) {
        if let lm = cache[s.id], !lm.getting {
            lm.msg = nil
        }
        fetch(s, then)
    }

    /// `refetch` for a message the cache knows the account of (its full
    /// message is cached); nothing happens otherwise.
    public func refetch(_ id: MessageID, _ then: @escaping Waiter) {
        guard let s = summary(id) else { return }
        refetch(s, then)
    }

    /// Runs on the main actor after one half of `lm` arrived
    /// (`settleLoaded`): the entry is put back if it was evicted meanwhile
    /// and the waiters hear about it; once nothing is in flight any more
    /// they are dropped.
    private func settle(_ id: MessageID, _ lm: LoadedMessage) {
        if cache[id] == nil {
            cache.store(id, lm)
        }
        let key = ObjectIdentifier(lm)
        let ws = waiters[key] ?? []
        if !lm.getting, !lm.fetching {
            waiters[key] = nil
        }
        for w in ws {
            w(lm)
        }
        onLoaded?(id, lm)
    }

    // MARK: Remote images

    /// Fetches the body of `s` again with remote images allowed for this
    /// one call and shows the result wherever the message is on display
    /// (remote.go `loadRemoteImages`). The daemon does the fetching; the
    /// views only get the inlined pictures. The bar shows the wait from the
    /// click on (`onRemoteBar`). A request already running is left alone
    /// and `then` is not called.
    ///
    /// `then` gets the entry with the new body, or the error; the error was
    /// already toasted and the bar put back (`imagesDone`).
    public func loadImages(_ s: MessageSummary, _ then: @escaping @MainActor (Result<LoadedMessage, any Error>) -> Void) {
        guard let lm = beginLoadingImages(s.id) else { return }
        fetchRemoteImages(s, lm, then)
    }

    /// Marks the entry of `id` as waiting for its remote images and redraws
    /// the bar (the first step of `loadRemoteImages` and `trustSender`);
    /// nil when a request is already running.
    public func beginLoadingImages(_ id: MessageID) -> LoadedMessage? {
        let lm = cache.loadedFor(id)
        if lm.loadingImages {
            return nil
        }
        lm.loadingImages = true
        refreshRemoteBar(id, lm)
        return lm
    }

    /// The message.body call under allow for a request the bar already
    /// shows as loading (`lm.loadingImages`, set by `beginLoadingImages`);
    /// it ends the request either way, with the images on display or the
    /// bar back as it was and a toast (remote.go `fetchRemoteImages`).
    public func fetchRemoteImages(
        _ s: MessageSummary, _ lm: LoadedMessage, _ then: @escaping @MainActor (Result<LoadedMessage, any Error>) -> Void
    ) {
        let id = s.id
        let client = client
        let quoted = lm.quotedShown
        let params = bodyParams(s, quoted: quoted, remoteContent: .allow)
        Task { [weak self] in
            let outcome: Result<MessageBodyResult, any Error>
            do {
                outcome = .success(try await client.call(API.MessageBody.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("message.body (allow): \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(L10n.T("Loading the images"), err))
                self.imagesDone(id, lm)
                then(.failure(err))
            case .success(let res):
                lm.loadingImages = false
                lm.store(res, quoted: quoted, replacing: true)
                if self.cache[id] == nil {
                    self.cache.store(id, lm)
                }
                self.onLoaded?(id, lm)
                then(.success(lm))
            }
        }
    }

    /// Ends a request for the images without a new body: the bar offers
    /// them again (remote.go `imagesDone`).
    public func imagesDone(_ id: MessageID, _ lm: LoadedMessage) {
        lm.loadingImages = false
        refreshRemoteBar(id, lm)
    }

    /// Redraws the bar of message `id` wherever it is on display and leaves
    /// the body alone (remote.go `refreshRemoteBar`).
    public func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {
        onRemoteBar?(id, lm)
    }

    // MARK: Pictures on the server

    /// Downloads the pictures of `s` kept on the mail server only
    /// (`remotePictures`) and shows the message again wherever it is on
    /// display (remote.go `downloadPictures`): message.download (joining
    /// one already running, `download`, so the chips spin too), then
    /// message.body again under `picturesPolicy`, so remote images loaded
    /// for it stay. The bars show the wait from the click on
    /// (`onRemoteBar`, `loadingPictures`). A request already running is
    /// left alone and `then` is not called.
    ///
    /// A failure of either call is toasted through `toast` (the window the
    /// click came from; the cache's own when nil) and the bar offers the
    /// pictures again. `then` gets the entry with the new body, or the
    /// error.
    public func downloadPictures(
        _ s: MessageSummary, toast: (@MainActor (String) -> Void)? = nil,
        _ then: @escaping @MainActor (Result<LoadedMessage, any Error>) -> Void
    ) {
        let id = s.id
        let lm = cache.loadedFor(id)
        if lm.loadingPictures {
            return
        }
        lm.loadingPictures = true
        refreshRemoteBar(id, lm)
        let client = client
        Task { [weak self] in
            let outcome: Result<MessageBodyResult, any Error>
            var quoted = lm.quotedShown
            do {
                guard let self else { return }
                try await self.download(accountID: s.accountId, messageID: id)
                // Back on the main actor after the download: the policy and
                // the variant of the body on display now.
                quoted = lm.quotedShown
                let params = self.bodyParams(s, quoted: quoted, remoteContent: picturesPolicy(lm))
                outcome = .success(try await client.call(API.MessageBody.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            lm.loadingPictures = false
            switch outcome {
            case .failure(let err):
                self.log.warning("download pictures: \(String(describing: err), privacy: .public)")
                (toast ?? self.toast)(rpcErrorText(L10n.T("Downloading the pictures"), err))
                self.refreshRemoteBar(id, lm)
                then(.failure(err))
            case .success(let res):
                lm.store(res, quoted: quoted, replacing: true)
                if self.cache[id] == nil {
                    self.cache.store(id, lm)
                }
                self.onLoaded?(id, lm)
                then(.success(lm))
            }
        }
    }

    // MARK: The count going out of date

    // A body cached while the daemon held the message in memory counts no
    // picture on the server; once the daemon has dropped that copy (30
    // minutes unused, its memory cap, the switch turned off, a restart) the
    // same body shown again asks for pictures that message.part answers
    // partNotDownloaded for, and without a new count there would be no bar
    // to get them back. The first such answer for a picture the body lists
    // asks for the body again (store and memory only, never the mail
    // server), whose count brings the bar back. The other way round, a
    // download of the message for anything else (a chip, a reply, a
    // forward) asks again for a body that counts pictures on the server, so
    // that they show and the bar goes (`endDownload`).

    /// Asks for the body of message `id` again when picture `partID`, which
    /// the cached body counts as here, turned out to be on the mail server
    /// only (remote.go `lostPicture`, `recheckPictures`).
    func lostPicture(_ accountID: AccountID, _ id: MessageID, _ partID: String) {
        guard let lm = cache[id], recheckPictures(lm, partID) else { return }
        lm.picturesRechecked = true
        reloadPictures(accountID, id, lm)
    }

    /// Asks for the body of message `id` again, under the policy of the
    /// body on display (`picturesPolicy`: remote images the user loaded
    /// stay), and shows it wherever the message is on display (`onLoaded`;
    /// remote.go `reloadPictures`). The daemon answers from its store and
    /// memory. A body that replaced the one on display meanwhile, or
    /// Download Pictures started meanwhile, wins over the answer; a failure
    /// is only logged and the body on display stays.
    private func reloadPictures(_ accountID: AccountID, _ id: MessageID, _ lm: LoadedMessage) {
        let shown = lm.body
        let quoted = lm.quotedShown
        let params = MessageBodyParams(
            accountId: accountID, messageId: id, remoteContent: picturesPolicy(lm), trimQuoted: !quoted)
        let client = client
        Task { [weak self] in
            let outcome: Result<MessageBodyResult, any Error>
            do {
                outcome = .success(try await client.call(API.MessageBody.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("message.body (pictures again): \(String(describing: err), privacy: .public)")
            case .success(let res):
                guard lm.body == shown, lm.quotedShown == quoted, !lm.loadingPictures else { return }
                lm.store(res, quoted: quoted, replacing: true)
                if self.cache[id] == nil {
                    self.cache.store(id, lm)
                }
                self.onLoaded?(id, lm)
            }
        }
    }

    // MARK: Parts and attached messages

    /// message.part for one attachment (attachments.go `fetchAttachment`):
    /// the part may be 16 MiB of base64 on the socket, hence the long
    /// timeout.
    public func fetchAttachment(accountID: AccountID, messageID: MessageID, partID: String) async throws -> MessagePartResult {
        try await client.call(
            API.MessagePart.self,
            MessagePartParams(accountId: accountID, messageId: messageID, partId: partID),
            timeout: RPCTimeouts.part)
    }

    /// Serves the web view's malachi-cid: pictures through message.part
    /// (remote.go `fetchPart`): the claimed type and the bytes; whether the
    /// type may be shown is the caller's check (`isImageType`). A picture
    /// the daemon answers partNotDownloaded for goes to `lostPicture`
    /// before the error is thrown (remote.go `pictureFailed`).
    public func fetchPart(accountID: AccountID, messageID: MessageID, partID: String) async throws -> (contentType: String, data: Data) {
        do {
            let res = try await fetchAttachment(accountID: accountID, messageID: messageID, partID: partID)
            return (res.contentType, res.data)
        } catch {
            if isPartNotDownloaded(error) {
                lostPicture(accountID, messageID, partID)
            }
            throw error
        }
    }

    /// message.embedded for one part (embedded.go `fetchEmbedded`); the
    /// timeout allows for the daemon fetching remote images under allow.
    public func fetchEmbedded(
        accountID: AccountID, messageID: MessageID, partID: String, remote: RemoteContentPolicy? = nil
    ) async throws -> MessageEmbeddedResult {
        try await client.call(
            API.MessageEmbedded.self,
            MessageEmbeddedParams(accountId: accountID, messageId: messageID, partId: partID, remoteContent: remote),
            timeout: RPCTimeouts.remote)
    }

    /// The data of attachment `a` of message `messageID` (download.go
    /// `partData`): message.part, after message.download when the chip
    /// showed the part on the server (`onServer`), or once when message.part
    /// answers partNotDownloaded (`withDownload`). The result's `partId` is
    /// the part actually fetched.
    public func partData(
        accountID: AccountID, messageID: MessageID, attachment a: Attachment, onServer: Bool
    ) async throws -> MessagePartResult {
        try await withDownload(
            a, remote: onServer,
            fetch: { a in try await self.fetchAttachment(accountID: accountID, messageID: messageID, partID: a.partId) },
            download: { try await self.download(accountID: accountID, messageID: messageID) })
    }

    /// The attached message `a` of message `messageID` rendered by the
    /// daemon (download.go `embeddedData`): message.embedded, downloading
    /// the containing message first or after a partNotDownloaded, as
    /// `partData`. `policy` is message.embedded's `remoteContent`.
    public func embeddedData(
        accountID: AccountID, messageID: MessageID, attachment a: Attachment, onServer: Bool,
        policy: RemoteContentPolicy? = nil
    ) async throws -> MessageEmbeddedResult {
        try await withDownload(
            a, remote: onServer,
            fetch: { a in
                try await self.fetchEmbedded(accountID: accountID, messageID: messageID, partID: a.partId, remote: policy)
            },
            download: { try await self.download(accountID: accountID, messageID: messageID) })
    }

    // MARK: Save All

    /// Whether a Save All of message `id` runs (attachments.go
    /// `savingAll`): its Save All buttons stay disabled wherever the
    /// message is shown, across the chips being drawn again.
    public func isSavingAll(_ id: MessageID) -> Bool {
        savingAll.contains(id)
    }

    /// Marks a Save All of message `id` as running and draws its chips
    /// again (`onChips`); false, and nothing changes, when one runs already.
    public func beginSaveAll(_ id: MessageID) -> Bool {
        guard !savingAll.contains(id) else { return false }
        savingAll.insert(id)
        onChips?(id, cache[id])
        return true
    }

    /// Ends the Save All of message `id` and draws its chips again.
    public func endSaveAll(_ id: MessageID) {
        guard savingAll.remove(id) != nil else { return }
        onChips?(id, cache[id])
    }

    // MARK: Downloads

    /// Whether the chips of message `id` show the download spinner
    /// (download.go `spinning`): a download of it has run for
    /// `downloadSpinnerDelay` and not ended yet.
    public func showsDownload(_ id: MessageID) -> Bool {
        spinning.contains(id)
    }

    /// Whether a download of message `id` is running.
    public func downloading(_ id: MessageID) -> Bool {
        downloads[id] != nil
    }

    /// message.download for message `id` (download.go `download`): makes
    /// the parts kept on the mail server, and a body not downloaded yet,
    /// local, and answers the message as the daemon reports it afterwards.
    /// One call per message: a request while one runs waits for the same
    /// answer. The chips show a spinner once the call has taken
    /// `downloadSpinnerDelay` (`showsDownload`, `onChips`); when it ends the
    /// cached message is replaced with the answer (Microsoft 365 may move
    /// part ids), a body that was not fetched is dropped and fetched again,
    /// and the chips are drawn again (`endDownload`). A failure is thrown to
    /// every caller, for its own toast; the daemon finishes a download its
    /// caller gave up on, so the call has the long `RPCTimeouts.download`.
    @discardableResult
    public func download(accountID: AccountID, messageID id: MessageID) async throws -> Message {
        if let running = downloads[id] {
            return try await running.value
        }
        let client = client
        let task = Task { [weak self] () async throws -> Message in
            let outcome: Result<Message, any Error>
            do {
                outcome = .success(try await client.call(
                    API.MessageDownload.self, MessageDownloadParams(accountId: accountID, messageId: id),
                    timeout: RPCTimeouts.download).message)
            } catch {
                outcome = .failure(error)
            }
            // Before any caller resumes, so every one of them finds the
            // cache as the download left it.
            self?.endDownload(accountID, id, outcome)
            return try outcome.get()
        }
        downloads[id] = task
        beginDownload(id, task)
        return try await task.value
    }

    /// Shows the spinner on the chips of `id` once `task` has run for the
    /// delay and is still the message's download (`beginDownload`).
    private func beginDownload(_ id: MessageID, _ task: Task<Message, any Error>) {
        let delay = spinnerDelay
        Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard let self, self.downloads[id] == task, !self.spinning.contains(id) else { return }
            self.spinning.insert(id)
            self.onChips?(id, self.cache[id])
        }
    }

    /// The download of `id` ended (`endDownload`): no spinner, the cached
    /// message replaced with the downloaded one, a body that was not
    /// fetched dropped and fetched again (every view re-renders through
    /// `onLoaded`), and so is one that counts pictures on the mail server
    /// only (`reloadAfterDownload`), which the daemon holds now: they show
    /// and the pictures bar goes, whatever the download was for. The chips
    /// are drawn again.
    private func endDownload(_ accountID: AccountID, _ id: MessageID, _ outcome: Result<Message, any Error>) {
        downloads[id] = nil
        spinning.remove(id)
        let lm = cache[id]
        switch outcome {
        case .failure(let err):
            log.warning("message.download: \(String(describing: err), privacy: .public)")
        case .success(let m):
            if let lm {
                lm.msg = m
                // Pictures that go missing from now on may ask for the body
                // once more (`recheckPictures`).
                lm.picturesRechecked = false
                if let b = lm.otherBody, b.bodyState != .fetched {
                    lm.otherBody = nil // fetched again when switched to
                }
                if let b = lm.body, b.bodyState != .fetched, !lm.fetching {
                    lm.body = nil
                    lm.err = nil
                    fetch(m.summary) { _ in }
                } else if reloadAfterDownload(lm) {
                    reloadPictures(accountID, id, lm)
                }
            }
        }
        onChips?(id, lm)
    }
}
