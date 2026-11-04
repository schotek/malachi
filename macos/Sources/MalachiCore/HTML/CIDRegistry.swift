// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The pure part of ui/internal/editor/cid.go: the registry behind the cid:
// scheme of the compose editor and the gate every served image passes.
// The scheme handler itself (reading the file, calling the fetcher under
// the timeout, answering the request) belongs to the WebKit layer.

import Foundation

/// editor.maxCIDBytes: caps what the cid: handler will serve; inline
/// images are attachments and share their limit.
public let maxCIDBytes = 25 << 20

/// editor.Fetcher: produces the bytes and the media type of one inline
/// image the backend holds (attachment.get).
public typealias CIDFetcher = @Sendable () async throws -> (data: Data, contentType: String)

/// editor.cidFile: one registered id: a local file, or a fetcher.
public enum CIDEntry: Sendable {
    case file(path: String, contentType: String)
    case fetcher(CIDFetcher)
}

/// What `checkInline` refuses.
public enum InlineImageError: Error, Equatable, Sendable, CustomStringConvertible {
    case empty
    case tooBig
    case notAPicture

    public var description: String {
        switch self {
        case .empty: return "inline image is empty"
        case .tooBig: return "inline image too big"
        case .notAPicture: return "inline image is not a picture"
        }
    }
}

/// editor.checkInline: the gate every served image passes: bytes present,
/// within the cap, of a picture type the view may render (never SVG,
/// which can script). Parameters are stripped and the type lower-cased.
public func checkInline(data: Data, contentType: String) throws {
    if data.isEmpty {
        throw InlineImageError.empty
    }
    if data.count > maxCIDBytes {
        throw InlineImageError.tooBig
    }
    let ct = bareMediaType(contentType)
    if !ct.utf8.starts(with: "image/".utf8) || ct == "image/svg+xml" {
        throw InlineImageError.notAPicture
    }
}

/// The cid: scheme serves the inline images of drafts being composed:
/// files the UI itself picked, and copies the backend made of a quoted
/// original's pictures, which the backend hands over through
/// attachment.get. Only ids registered here are served, so a pasted
/// `<img src="cid:../../etc/passwd">` yields an error. The message viewer
/// uses a different scheme (malachi-cid:), so a displayed message can
/// never address compose attachments. Go keeps one registry per process;
/// `shared` is that one.
@MainActor
public final class CIDRegistry {
    /// editor.fetchTimeout: bounds one fetcher call: the daemon reads a
    /// file of the attachment store, which is quick, but the request must
    /// not hang the view's image forever when the daemon is gone.
    public static let fetchTimeout: Duration = .seconds(60)

    /// The process-wide registry every editor shares.
    public static let shared = CIDRegistry()

    private var files: [String: CIDEntry] = [:]

    public init() {}

    /// editor.RegisterCID: makes cid:<id> resolve to the file at `path`.
    public func register(_ id: String, path: String, contentType: String) {
        files[id] = .file(path: path, contentType: contentType)
    }

    /// editor.RegisterCIDFetcher: makes cid:<id> resolve to what `fetch`
    /// returns.
    public func registerFetcher(_ id: String, fetch: @escaping CIDFetcher) {
        files[id] = .fetcher(fetch)
    }

    /// editor.CIDRegistered: whether `id` resolves to anything.
    public func isRegistered(_ id: String) -> Bool {
        files[id] != nil
    }

    /// editor.UnregisterCID: forgets `id`.
    public func unregister(_ id: String) {
        files[id] = nil
    }

    /// editor.lookupCID: the entry of `id`, exactly as registered.
    public func lookup(_ id: String) -> CIDEntry? {
        files[id]
    }
}
