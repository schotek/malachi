// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The loaded-message cache of the message pane (ui/internal/window/
// message_view.go) and the pure text helpers of the pane. The fetching
// itself, with its waiters, is the controller's (`MessageCache`).

/// What fetching produced for one message (message_view.go
/// `loadedMessage`): the full header view, the body, or the error that
/// prevented the body. A failed message.get is only logged (the summary
/// headers stay) and retried the next time the message is shown; a failed
/// body is retried the same way. A class, because the pane, a message
/// window and the compose prefill share one entry and see each other's
/// halves arrive.
@MainActor
public final class LoadedMessage {
    public var msg: Message?
    /// The body on display: without the quoted history (message.body with
    /// `trimQuoted`), or whole while `quotedShown`.
    public var body: MessageBodyResult?
    /// The message.body failure.
    public var err: (any Error)?
    /// The last message.get failure (nil once it answered).
    public var getErr: (any Error)?

    /// The quoted history is shown: `body` is the whole body, asked for
    /// without `trimQuoted` (the view's Show Quoted Text, `QuotedReveal`).
    /// Off by default: a body comes trimmed.
    public private(set) var quotedShown = false
    /// The other variant of the body, kept for switching back without
    /// asking the daemon again; dropped when `body` is replaced under
    /// another remote-content policy (the remote images, the pictures),
    /// which it would not have.
    public var otherBody: MessageBodyResult?
    /// A message.body for the other variant is in flight (a switch while
    /// the request for the variant shown before ran).
    public var fetchingOther = false
    /// The remote-content policy the variant switched to is asked with
    /// when it has to be fetched (`picturesPolicy` of the body shown before
    /// the switch: images the user loaded stay loaded).
    public var switchPolicy: RemoteContentPolicy?

    /// Insertion order in `LoadedCache`.
    public var seq: UInt64

    /// The account the message belongs to, from the summary it was fetched
    /// for: `LoadedCache.removeAll(of:)` finds the entries of an account
    /// whose messages the daemon rebuilt (notify.messagesChanged). nil for
    /// an entry made without a summary.
    public var accountId: AccountID?

    /// In-flight halves; a second fetch for the same id while one runs
    /// joins instead of asking the daemon twice.
    public var getting: Bool
    public var fetching: Bool

    /// Set from the moment the user asks for the remote images (Load
    /// Images, Always From This Sender) until the daemon has answered; the
    /// bar shows it instead of the buttons (`remoteBarState`).
    public var loadingImages: Bool

    /// Set from the moment the user asks for the pictures kept on the mail
    /// server (Download Pictures) until the message was downloaded and its
    /// body asked for again; the pictures bar shows it instead of the
    /// button (`picturesBarState`).
    public var loadingPictures: Bool

    /// Set once a picture of the body went missing and the body was asked
    /// for again, until the next download of the message
    /// (`recheckPictures`): never more than once in between.
    public var picturesRechecked: Bool

    /// Set from the click on the bulk strip's button until
    /// `message.unsubscribe` has answered (window/bulk.go `unsubscribing`);
    /// the button waits.
    public var unsubscribing = false

    public init(
        msg: Message? = nil, body: MessageBodyResult? = nil, err: (any Error)? = nil, seq: UInt64 = 0,
        getting: Bool = false, fetching: Bool = false, loadingImages: Bool = false, loadingPictures: Bool = false,
        picturesRechecked: Bool = false, accountId: AccountID? = nil
    ) {
        self.msg = msg
        self.body = body
        self.err = err
        self.seq = seq
        self.accountId = accountId ?? msg?.summary.accountId
        self.getting = getting
        self.fetching = fetching
        self.loadingImages = loadingImages
        self.loadingPictures = loadingPictures
        self.picturesRechecked = picturesRechecked
    }

    /// Nothing is left to fetch.
    public var complete: Bool { msg != nil && body != nil }

    /// Shows the body with its quoted history (`on`) or without: the two
    /// variants trade places (`body`, `otherBody`, and their requests in
    /// flight), and a body error belongs to the variant left. True when
    /// anything changed; `body` is then nil when the variant has yet to be
    /// fetched.
    @discardableResult
    public func showQuoted(_ on: Bool) -> Bool {
        guard on != quotedShown else { return false }
        if otherBody == nil {
            switchPolicy = picturesPolicy(self)
        }
        quotedShown = on
        swap(&body, &otherBody)
        swap(&fetching, &fetchingOther)
        err = nil
        return true
    }

    /// Stores a message.body answer asked for the variant `quoted` (with
    /// the quoted history or not): as `body` when that variant is still
    /// shown, else as `otherBody`. `replacing` (an answer under another
    /// remote-content policy) drops the other variant shown before.
    public func store(_ res: MessageBodyResult, quoted: Bool, replacing: Bool = false) {
        if quoted == quotedShown {
            body = res
            err = nil
            if replacing {
                otherBody = nil
            }
        } else {
            otherBody = res
        }
    }

    /// The body half has an answer (content or error).
    public var bodySettled: Bool { body != nil || err != nil }

    /// What the entry costs the cache: its body (an HTML body carries its
    /// inlined pictures).
    public var size: Int {
        [body, otherBody].reduce(0) { total, b in
            guard let b else { return total }
            return total + (b.html?.utf8.count ?? 0) + b.text.utf8.count
        }
    }
}

/// The cache of loaded messages (`Window.loaded` with `storeLoaded`,
/// `loadedFor` and `pruneLoaded` of message_view.go), bounded by entries
/// and by the size of the bodies in it; the oldest entries are evicted
/// first, the newest never.
@MainActor
public struct LoadedCache {
    /// The caps (message_view.go `maxLoaded`, `maxLoadedBytes`).
    public nonisolated static let maxLoaded = 64
    public nonisolated static let maxLoadedBytes = 32 << 20

    public private(set) var entries: [MessageID: LoadedMessage]
    /// Numbers insertions so `prune` can find the oldest (`loadedSeq`).
    private var seq: UInt64

    public init(entries: [MessageID: LoadedMessage] = [:]) {
        self.entries = entries
        self.seq = entries.values.map(\.seq).max() ?? 0
    }

    public subscript(id: MessageID) -> LoadedMessage? {
        entries[id]
    }

    public var count: Int { entries.count }

    /// The entry of `id`, created empty when there is none (`loadedFor`).
    public mutating func loadedFor(_ id: MessageID) -> LoadedMessage {
        if let lm = entries[id] {
            return lm
        }
        let lm = LoadedMessage()
        store(id, lm)
        return lm
    }

    /// Caches `lm` for `id`, evicting the oldest entries beyond the caps
    /// (`storeLoaded`).
    public mutating func store(_ id: MessageID, _ lm: LoadedMessage) {
        seq += 1
        lm.seq = seq
        entries[id] = lm
        prune()
    }

    /// Forgets `id`.
    public mutating func remove(_ id: MessageID) {
        entries[id] = nil
    }

    /// Forgets everything.
    public mutating func removeAll() {
        entries = [:]
    }

    /// Forgets the entries of the account's messages (notify.messagesChanged:
    /// the daemon rebuilt them in place, docs/api.md §5), and those whose
    /// account is not known, which may be its. Returns the ids let go.
    @discardableResult
    public mutating func removeAll(of account: AccountID) -> [MessageID] {
        let gone = entries.filter { $0.value.accountId == nil || $0.value.accountId == account }.map(\.key)
        for id in gone {
            entries[id] = nil
        }
        return gone.sorted { $0.rawValue < $1.rawValue }
    }

    /// Evicts the entries with the lowest `seq` until at most `limit` remain
    /// and their bodies fit in `maxBytes`; the newest entry always stays
    /// (`pruneLoaded`).
    public mutating func prune(limit: Int = LoadedCache.maxLoaded, maxBytes: Int = LoadedCache.maxLoadedBytes) {
        var total = entries.values.reduce(0) { $0 + $1.size }
        while entries.count > 1, entries.count > limit || total > maxBytes {
            guard let oldest = entries.min(by: { $0.value.seq < $1.value.seq }) else {
                return
            }
            total -= oldest.value.size
            entries[oldest.key] = nil
        }
    }
}

// MARK: Pane text

/// The subject to display; an empty one gets a placeholder
/// (message_view.go `subjectText`).
public func subjectText(_ subject: String) -> String {
    let s = subject.trimmingCharacters(in: .whitespacesAndNewlines)
    if !s.isEmpty {
        return s
    }
    return L10n.T("(No subject)")
}

/// The button under a body that shows or hides the quoted history the
/// daemon cut from it (`MessageBodyParams.trimQuoted`).
public enum QuotedTextOffer: Sendable, Equatable {
    /// The body is trimmed: Show Quoted Text.
    case show
    /// The whole body shows: Hide Quoted Text.
    case hide

    /// The button's tooltip and accessibility label
    /// (conversation.QuotedTextLabel).
    public var label: String {
        Conversation.quotedTextLabel(shown: self == .hide)
    }
}

/// The button for what `lm` shows (nil: none): Hide while the whole body
/// shows (or is on its way, or failed: the way back to the trimmed one),
/// Show when the daemon cut the quoted history from the body on display.
@MainActor
public func quotedTextOffer(_ lm: LoadedMessage?) -> QuotedTextOffer? {
    guard let lm else { return nil }
    if lm.quotedShown {
        return .hide
    }
    guard lm.err == nil, let b = lm.body, b.isQuotedTrimmed else { return nil }
    return .show
}

/// What the body label shows for a message.body result (message_view.go
/// `bodyText`).
public func bodyText(_ b: MessageBodyResult?) -> String {
    guard let b else {
        return L10n.T("(Empty message)")
    }
    switch b.bodyState {
    case .pending:
        return L10n.T("Downloading…")
    case .tooBig:
        return L10n.T("This message is too large to download.")
    case .failed:
        return L10n.T("This message could not be read.")
    default:
        break
    }
    let text = trimTrailing(b.text, " \t\r\n")
    if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        return L10n.T("(Empty message)")
    }
    return text
}

/// Go's `strings.TrimRight` over Unicode scalars, so that a trailing CR LF
/// (one grapheme in Swift) is trimmed like two characters.
private func trimTrailing(_ s: String, _ cutset: String) -> String {
    let set = Set(cutset.unicodeScalars)
    var scalars = s.unicodeScalars
    while let last = scalars.last, set.contains(last) {
        scalars.removeLast()
    }
    return String(scalars)
}

/// Whether the body goes into the HTML view (attachments.go `showsHTML`).
public func showsHTML(_ b: MessageBodyResult?) -> Bool {
    guard let b, b.bodyState == .fetched, let html = b.html else {
        return false
    }
    return !html.isEmpty
}
