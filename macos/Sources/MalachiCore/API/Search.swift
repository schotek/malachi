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

    public init(accountId: AccountID? = nil, folderId: FolderID? = nil, query: String, page: Page = Page()) {
        self.accountId = accountId
        self.folderId = folderId
        self.query = query
        self.page = page
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
