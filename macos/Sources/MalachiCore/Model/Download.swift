// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The pure half of ui/internal/window/download.go: how a part or an
// attached message kept on the mail server is fetched. The download itself
// (message.download, one per message, the spinner) is `MessageCache`'s.

/// Whether `error` is the daemon's partNotDownloaded (1504): the part is
/// kept on the mail server only, message.download fetches it (download.go
/// `partNotDownloaded`).
public func isPartNotDownloaded(_ error: any Error) -> Bool {
    (error as? RPCError)?.code == .partNotDownloaded
}

/// Whether `error` says the daemon does not offer the method at all: an
/// older one (methodNotFound) or one without it yet (notImplemented)
/// (download.go `methodUnsupported`). A forward then goes on without a
/// download, and Disk Space Used is hidden.
public func methodUnsupported(_ error: any Error) -> Bool {
    guard let e = error as? RPCError else { return false }
    return e.code == .methodNotFound || e.code == .notImplemented
}

/// What `withDownload` throws when the downloaded message no longer lists
/// the attachment (`partAfterDownload` found none): nothing is fetched,
/// since the old part id may name another file by now, and the action's
/// toast says that the attachment no longer exists (partNotFound in
/// `rpcErrorText`; download.go `errPartNotFound`).
public let partNotFoundAfterDownload = RPCError(code: .partNotFound, message: partNotFoundDetail)

/// Its technical English, for the log only: like the daemon's messages it
/// is never shown.
private let partNotFoundDetail = "the downloaded message no longer lists the part"

/// Fetches something of attachment `a` that may be on the mail server only
/// (download.go `withDownload`): `remote` (the chip said so) downloads the
/// message first and fetches once; otherwise it fetches, and only when the
/// daemon answers partNotDownloaded (the part went to the server since the
/// chip was drawn) it downloads once and fetches once more. Any other error,
/// and a second partNotDownloaded, is thrown as it is: never a loop. After
/// a download the part is looked up again in the downloaded message
/// (`partAfterDownload`), since Microsoft 365 may move part ids; when that
/// message no longer lists it, nothing is fetched and
/// `partNotFoundAfterDownload` is thrown.
///
/// `download` answers the message after the download (nil when unknown).
@MainActor
public func withDownload<T>(
    _ a: Attachment, remote: Bool,
    fetch: (Attachment) async throws -> T,
    download: () async throws -> Message?
) async throws -> T {
    if remote {
        let m = try await download()
        return try await fetch(try listed(a, m))
    }
    do {
        return try await fetch(a)
    } catch where isPartNotDownloaded(error) {
        let m = try await download()
        return try await fetch(try listed(a, m))
    }
}

/// `a` as the downloaded message `m` lists it (`partAfterDownload`), or
/// `partNotFoundAfterDownload` when it does not.
private func listed(_ a: Attachment, _ m: Message?) throws -> Attachment {
    guard let now = partAfterDownload(a, m) else {
        throw partNotFoundAfterDownload
    }
    return now
}
