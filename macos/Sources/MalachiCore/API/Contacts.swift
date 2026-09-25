// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Recipient completion (docs/api.md §4.11; types.go Contact).

import Foundation

/// api.Contact: one recipient suggestion. `name` and `book` are untrusted
/// display text.
public struct Contact: Codable, Sendable, Equatable {
    public var name: String?
    /// Normalised, syntactically valid.
    public var address: String
    public var source: ContactSource
    /// The address book the contact came from; nil for a collected address.
    public var book: String?

    public init(name: String? = nil, address: String, source: ContactSource, book: String? = nil) {
        self.name = name
        self.address = address
        self.source = source
        self.book = book
    }
}

/// api.ContactSearchParams. `limit` nil = `API.Limits.defaultContactLimit`,
/// clamped to `maxContactLimit`; `query` at most `maxContactQueryBytes`.
public struct ContactSearchParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var query: String
    public var limit: Int?

    public init(accountId: AccountID, query: String, limit: Int? = nil) {
        self.accountId = accountId
        self.query = query
        self.limit = limit
    }
}

/// api.ContactSearchResult: ranked best first.
public struct ContactSearchResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var contacts: [Contact]

    public init(contacts: [Contact]) {
        self.contacts = contacts
    }
}
