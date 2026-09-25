// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Known senders (docs/api.md §4.9; types.go "Config", KnownSender). The
// allow-list behind the `knownSenders` remote-content policy: never fed
// from incoming From headers, matched on the bare address.

import Foundation

/// api.KnownSender.
public struct KnownSender: Codable, Sendable, Equatable {
    public var address: String
    public var source: KnownSenderSource
    public var addedAt: Date

    public init(address: String, source: KnownSenderSource, addedAt: Date) {
        self.address = address
        self.source = source
        self.addedAt = addedAt
    }
}

/// api.SenderListResult.
public struct SenderListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var senders: [KnownSender]

    public init(senders: [KnownSender]) {
        self.senders = senders
    }
}

/// api.SenderAddParams: a bare address or `Name <address>`; only the address
/// is stored.
public struct SenderAddParams: Codable, Sendable, Equatable {
    public var address: String

    public init(address: String) {
        self.address = address
    }
}

/// api.SenderRemoveParams. Removing an unknown address is not an error.
public struct SenderRemoveParams: Codable, Sendable, Equatable {
    public var address: String

    public init(address: String) {
        self.address = address
    }
}
