// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// api.ErrorCode: the numeric error enumeration of docs/api.md §2. Codes are
/// stable, never renumbered, only appended; an unknown one still decodes.
public struct ErrorCode: RawRepresentable, Hashable, Codable, Sendable, ExpressibleByIntegerLiteral,
    CustomStringConvertible {
    public let rawValue: Int

    public init(rawValue: Int) { self.rawValue = rawValue }
    public init(integerLiteral value: Int) { self.rawValue = value }

    // JSON-RPC 2.0 reserved codes.
    public static let parseError: ErrorCode = -32700
    public static let invalidRequest: ErrorCode = -32600
    public static let methodNotFound: ErrorCode = -32601
    public static let invalidParams: ErrorCode = -32602
    public static let internalError: ErrorCode = -32603

    // 1000–1099: general.
    public static let notImplemented: ErrorCode = 1000
    public static let invalidArgument: ErrorCode = 1001
    /// Optimistic-concurrency conflict (drafts, sending).
    public static let conflict: ErrorCode = 1002
    public static let cancelled: ErrorCode = 1003
    /// Daemon busy or shutting down.
    public static let unavailable: ErrorCode = 1004

    // 1100–1199: not found.
    public static let accountNotFound: ErrorCode = 1100
    public static let folderNotFound: ErrorCode = 1101
    public static let messageNotFound: ErrorCode = 1102
    public static let threadNotFound: ErrorCode = 1103
    public static let draftNotFound: ErrorCode = 1104
    /// Unknown id, another account's, or bound to a different draft.
    public static let attachmentNotFound: ErrorCode = 1105

    // 1200–1299: authentication.
    /// Credentials missing or token expired; see notify.authRequired.
    public static let authRequired: ErrorCode = 1200
    /// The server rejected the credentials.
    public static let authFailed: ErrorCode = 1201
    /// Secret storage unavailable.
    public static let keyringError: ErrorCode = 1202
    /// The daemon's own sign-in needs an OAuth client id for the provider
    /// and none is configured.
    public static let oauthClientMissing: ErrorCode = 1203

    // 1300–1399: network and remote servers.
    public static let offline: ErrorCode = 1300
    public static let networkError: ErrorCode = 1301
    public static let serverError: ErrorCode = 1302
    public static let tlsError: ErrorCode = 1303
    public static let serverTimeout: ErrorCode = 1304

    // 1400–1499: local storage.
    public static let storageError: ErrorCode = 1400
    public static let migrationFailed: ErrorCode = 1401

    // 1500–1599: content.
    public static let malformedMessage: ErrorCode = 1500
    /// The sanitiser refused the input; the body is withheld.
    public static let sanitizeFailed: ErrorCode = 1501
    /// Over a documented limit; `data` is `{"limit": n, "size": n}`.
    public static let attachmentTooBig: ErrorCode = 1502
    public static let partNotFound: ErrorCode = 1503

    /// The stable symbolic name (api.ErrorCode.String): `"attachmentTooBig"`,
    /// or `"unknown(1234)"` for a code this client does not know.
    public var name: String {
        ErrorCode.names[self] ?? "unknown(\(rawValue))"
    }

    public var description: String { name }

    /// Every code of protocol version 1, in the order of errors.go.
    public static let all: [ErrorCode] = [
        .parseError, .invalidRequest, .methodNotFound, .invalidParams, .internalError,
        .notImplemented, .invalidArgument, .conflict, .cancelled, .unavailable,
        .accountNotFound, .folderNotFound, .messageNotFound, .threadNotFound, .draftNotFound, .attachmentNotFound,
        .authRequired, .authFailed, .keyringError, .oauthClientMissing,
        .offline, .networkError, .serverError, .tlsError, .serverTimeout,
        .storageError, .migrationFailed,
        .malformedMessage, .sanitizeFailed, .attachmentTooBig, .partNotFound,
    ]

    private static let names: [ErrorCode: String] = [
        .parseError: "parseError",
        .invalidRequest: "invalidRequest",
        .methodNotFound: "methodNotFound",
        .invalidParams: "invalidParams",
        .internalError: "internalError",
        .notImplemented: "notImplemented",
        .invalidArgument: "invalidArgument",
        .conflict: "conflict",
        .cancelled: "cancelled",
        .unavailable: "unavailable",
        .accountNotFound: "accountNotFound",
        .folderNotFound: "folderNotFound",
        .messageNotFound: "messageNotFound",
        .threadNotFound: "threadNotFound",
        .draftNotFound: "draftNotFound",
        .attachmentNotFound: "attachmentNotFound",
        .authRequired: "authRequired",
        .authFailed: "authFailed",
        .keyringError: "keyringError",
        .oauthClientMissing: "oauthClientMissing",
        .offline: "offline",
        .networkError: "networkError",
        .serverError: "serverError",
        .tlsError: "tlsError",
        .serverTimeout: "serverTimeout",
        .storageError: "storageError",
        .migrationFailed: "migrationFailed",
        .malformedMessage: "malformedMessage",
        .sanitizeFailed: "sanitizeFailed",
        .attachmentTooBig: "attachmentTooBig",
        .partNotFound: "partNotFound",
    ]
}

/// The `data` of an `attachmentTooBig` error: the cap that was exceeded and,
/// where the daemon knows it, the offending size (`message.part` reports the
/// limit only).
public struct SizeLimit: Sendable, Equatable {
    public let limit: Int
    public let size: Int?

    public init(limit: Int, size: Int? = nil) {
        self.limit = limit
        self.size = size
    }
}

extension RPCError {
    /// The structured detail of an `attachmentTooBig` error; nil for any
    /// other code or when `data` has not the documented shape.
    public var attachmentTooBig: SizeLimit? {
        guard code == .attachmentTooBig, let limit = data?["limit"]?.intValue else { return nil }
        return SizeLimit(limit: limit, size: data?["size"]?.intValue)
    }
}
