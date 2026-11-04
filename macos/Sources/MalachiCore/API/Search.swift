// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Search (docs/api.md §4.6; types.go "Search"): the daemon searches its
// local store, newest first; `ranges` are byte ranges into `snippet`.

import Foundation

/// api.SearchQueryParams. `accountId` nil = every account.
public struct SearchQueryParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var folderId: FolderID?
    /// The user-typed query (FTS5 subset with field prefixes; docs/api.md §4.6).
    public var query: String
    public var page: Page
    /// With `folderId`: also the user's sent mail of the folder's threads
    /// (omitted on the wire when false).
    public var withSent: Bool

    public init(accountId: AccountID? = nil, folderId: FolderID? = nil, query: String, page: Page = Page(), withSent: Bool = false) {
        self.accountId = accountId
        self.folderId = folderId
        self.query = query
        self.page = page
        self.withSent = withSent
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, folderId, query, page, withSent
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decodeIfPresent(AccountID.self, forKey: .accountId)
        folderId = try c.decodeIfPresent(FolderID.self, forKey: .folderId)
        query = try c.decode(String.self, forKey: .query)
        page = try c.decode(Page.self, forKey: .page)
        withSent = try c.decodeIfPresent(Bool.self, forKey: .withSent) ?? false
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(accountId, forKey: .accountId)
        try c.encodeIfPresent(folderId, forKey: .folderId)
        try c.encode(query, forKey: .query)
        try c.encode(page, forKey: .page)
        if withSent { try c.encode(true, forKey: .withSent) }
    }
}

/// api.MatchRange: a byte range within `SearchResult.snippet`.
public struct MatchRange: Codable, Sendable, Equatable {
    public var start: Int
    public var end: Int

    public init(start: Int, end: Int) {
        self.start = start
        self.end = end
    }
}

/// api.SearchResult. `snippet` is a plain-text excerpt, never HTML.
public struct SearchResult: Codable, Sendable, Equatable {
    public var message: MessageSummary
    public var snippet: String
    public var ranges: [MatchRange]?
    public var score: Double

    public init(message: MessageSummary, snippet: String, ranges: [MatchRange]? = nil, score: Double) {
        self.message = message
        self.snippet = snippet
        self.ranges = ranges
        self.score = score
    }
}

/// api.SearchQueryResult.
public struct SearchQueryResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var results: [SearchResult]
    public var page: PageInfo

    public init(results: [SearchResult], page: PageInfo) {
        self.results = results
        self.page = page
    }
}
