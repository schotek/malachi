// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The loaded-message cache and its fetching: ui/internal/window/
/// message_view.go (`fetchMessage`, `settleLoaded`, `loadedFor`,
/// `storeLoaded`), remote.go (`loadRemoteImages`, `fetchRemoteImages`,
/// `imagesDone`, `refreshRemoteBar`, `showLoaded`), outbox.go
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

    private let toast: @MainActor (String) -> Void
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "message")

    /// Who wants to hear about the in-flight halves of an entry
    /// (`loadedMessage.waiters`), kept beside the entry so that an entry
    /// evicted while in flight keeps its own waiters.
    private var waiters: [ObjectIdentifier: [Waiter]] = [:]

    /// - Parameters:
    ///   - client: the transport; calls fail with `notConnected` until the
    ///     connection controller reports a connection.
    ///   - toast: shows a transient message (the window's toast overlay).
    public init(client: RPCClient, toast: @escaping @MainActor (String) -> Void) {
        self.client = client
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

    /// Forgets `id` (an entry in flight is put back when its half arrives).
    /// For tests.
    func evict(_ id: MessageID) {
        cache.remove(id)
    }

    // MARK: Fetching

    /// Runs message.get and message.body for `s` (each only when the cache
    /// lacks it and no request is in flight) and calls `then` on the main
    /// actor after every answer, with the cache entry so far; a complete
    /// entry calls `then` at once (message_view.go `fetchMessage`). A failed
    /// message.get is only logged (the summary headers stay) and retried the
    /// next time; a failed body sets `err` and is retried the same way.
    public func fetch(_ s: MessageSummary, _ then: @escaping Waiter) {
        let id = s.id
        let lm = cache.loadedFor(id)
        if lm.complete {
            then(lm)
            return
        }
        waiters[ObjectIdentifier(lm), default: []].append(then)
        let client = client
        if lm.msg == nil, !lm.getting {
            lm.getting = true
            Task { [weak self] in
                let outcome: Result<MessageGetResult, any Error>
                do {
                    outcome = .success(try await client.call(
                        API.MessageGet.self, MessageGetParams(accountId: s.accountId, messageId: id), timeout: RPCTimeouts.default))
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
        if lm.body == nil, !lm.fetching {
            lm.fetching = true
            lm.err = nil // a retry after a failure
            Task { [weak self] in
                let outcome: Result<MessageBodyResult, any Error>
                do {
                    outcome = .success(try await client.call(
                        API.MessageBody.self, MessageBodyParams(accountId: s.accountId, messageId: id), timeout: RPCTimeouts.default))
                } catch {
                    outcome = .failure(error)
                }
                guard let self else { return }
                lm.fetching = false
                switch outcome {
                case .failure(let err):
                    self.log.warning("message.body: \(String(describing: err), privacy: .public)")
                    lm.err = err
                case .success(let res):
                    lm.body = res
                }
                self.settle(id, lm)
            }
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
        Task { [weak self] in
            let outcome: Result<MessageBodyResult, any Error>
            do {
                outcome = .success(try await client.call(
                    API.MessageBody.self,
                    MessageBodyParams(accountId: s.accountId, messageId: id, remoteContent: .allow),
                    timeout: RPCTimeouts.remote))
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
                lm.body = res
                lm.err = nil
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
    /// type may be shown is the caller's check (`isImageType`).
    public func fetchPart(accountID: AccountID, messageID: MessageID, partID: String) async throws -> (contentType: String, data: Data) {
        let res = try await fetchAttachment(accountID: accountID, messageID: messageID, partID: partID)
        return (res.contentType, res.data)
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
}
