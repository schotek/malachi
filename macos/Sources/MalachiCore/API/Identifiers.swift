// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The opaque identifiers of backend/pkg/api/types.go. Each is its own type so
// that a folder id cannot be passed where a message id is expected; on the
// wire every one is a bare string. A Go field whose empty string means
// "none" (`threadId`, `parentId`, `Draft.id`) is an Optional in Swift.

import Foundation

/// A string-valued wire type: `RawRepresentable` over `String`, written and
/// read as the bare string, buildable from a literal. The base of both the
/// identifiers and the extensible enums.
public protocol StringWireValue: RawRepresentable, Hashable, Codable, Sendable, ExpressibleByStringLiteral,
    CustomStringConvertible where RawValue == String {
    init(rawValue: String)
}

extension StringWireValue {
    public init(_ rawValue: String) {
        self.init(rawValue: rawValue)
    }

    public init(stringLiteral value: String) {
        self.init(rawValue: value)
    }

    public var description: String { rawValue }
}

/// An identifier the daemon minted: opaque, compared by value, never parsed.
public protocol OpaqueID: StringWireValue {}

/// api.AccountID: a configured account. Stable across restarts.
public struct AccountID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.FolderID: a folder within an account. Not the IMAP mailbox name.
public struct FolderID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.MessageID: a message in the local store. Never the RFC 5322
/// Message-ID header, which is attacker-controlled and not unique.
public struct MessageID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.ThreadID: a conversation within an account.
public struct ThreadID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.DraftID: a locally stored draft.
public struct DraftID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}
