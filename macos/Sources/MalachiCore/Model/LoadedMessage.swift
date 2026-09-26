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
    public var body: MessageBodyResult?
    /// The message.body failure.
    public var err: (any Error)?

    /// Insertion order in `LoadedCache`.
    public var seq: UInt64

    /// In-flight halves; a second fetch for the same id while one runs
    /// joins instead of asking the daemon twice.
    public var getting: Bool
    public var fetching: Bool

    /// Set from the moment the user asks for the remote images (Load
    /// Images, Always From This Sender) until the daemon has answered; the
    /// bar shows it instead of the buttons (`remoteBarState`).
    public var loadingImages: Bool

    public init(
        msg: Message? = nil, body: MessageBodyResult? = nil, err: (any Error)? = nil, seq: UInt64 = 0,
        getting: Bool = false, fetching: Bool = false, loadingImages: Bool = false
    ) {
        self.msg = msg
        self.body = body
        self.err = err
        self.seq = seq
        self.getting = getting
        self.fetching = fetching
        self.loadingImages = loadingImages
    }

    /// Nothing is left to fetch.
    public var complete: Bool { msg != nil && body != nil }

    /// The body half has an answer (content or error).
    public var bodySettled: Bool { body != nil || err != nil }

    /// What the entry costs the cache: its body (an HTML body carries its
    /// inlined pictures).
    public var size: Int {
        guard let body else { return 0 }
        return (body.html?.utf8.count ?? 0) + body.text.utf8.count
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
