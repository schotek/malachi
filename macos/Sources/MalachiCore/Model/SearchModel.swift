// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The search side of the message list, without AppKit
// (ui/internal/window/search_model.go): while a search is on the flat list
// holds search.query results instead of the selected folder's messages.
// The results are summaries like any listing, each carrying its own
// account and folder, so every action on a row works unchanged.

/// How much has to be typed before a search runs (search_model.go
/// `searchMinRunes`), in Unicode scalars as Go counts runes.
public let searchMinScalars = 2

/// What the list knows of the search (search_model.go `searchState`).
public struct SearchState: Sendable {
    public var active = false
    /// The field's text, trimmed.
    public var text = ""
    /// As chosen in the scope bar.
    public var scope: Settings.SearchScope = .folder
    /// The scope of `params`: Folder and Account fall back to All while no
    /// folder is selected.
    public var effective: Settings.SearchScope = .folder
    /// Of the results on show, or on their way.
    public var params = SearchQueryParams(query: "")
    /// The results of `params` are on show.
    public var shown = false
    public var hits: [MessageID: SearchHit] = [:]
    /// Return was pressed before the results came: the first one is
    /// selected when they do.
    public var focusFirst = false
    /// The retention window from config.get, which the retention note
    /// names; unknown until that answered.
    public var offlineDays = 0
    public var offlineKnown = false

    public init() {}
}

/// What a result shows beyond its summary: the excerpt around the match
/// and the matched words in it (search_model.go `searchHit`).
public struct SearchHit: Sendable, Equatable {
    public var snippet: String
    public var ranges: [MatchRange]

    public init(snippet: String, ranges: [MatchRange]) {
        self.snippet = snippet
        self.ranges = ranges
    }
}

/// The scope bar over the list while a search is on: the chosen scope,
/// whether Folder and Account can be chosen (they need a selected folder),
/// and what each searches, for the tooltips (search.go
/// `refreshSearchScope`). Plain text: AppKit tooltips take no markup.
public struct SearchBarState: Sendable, Equatable {
    public var scope: Settings.SearchScope
    public var narrowEnabled: Bool
    public var folderTooltip: String
    public var accountTooltip: String
    public var allTooltip: String

    public init(scope: Settings.SearchScope, narrowEnabled: Bool, folderTooltip: String, accountTooltip: String, allTooltip: String) {
        self.scope = scope
        self.narrowEnabled = narrowEnabled
        self.folderTooltip = folderTooltip
        self.accountTooltip = accountTooltip
        self.allTooltip = allTooltip
    }
}

/// Whether enough was typed to search (search_model.go `searchReady`).
public func searchReady(_ text: String) -> Bool {
    text.trimmingCharacters(in: .whitespacesAndNewlines).unicodeScalars.count >= searchMinScalars
}

extension MailModel {
    /// The first-page search.query for `text` in `scope`, and the scope it
    /// actually covers: without a selected folder there is nothing to
    /// narrow Folder or Account to, so every account is searched
    /// (search_model.go `searchRequest`).
    public func searchRequest(_ text: String, scope: Settings.SearchScope) -> (params: SearchQueryParams, effective: Settings.SearchScope) {
        let query = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let page = Page(limit: API.Limits.defaultPageLimit)
        guard let k = selected else {
            return (SearchQueryParams(query: query, page: page), .all)
        }
        switch scope {
        case .folder:
            return (SearchQueryParams(accountId: k.account, folderId: k.folder, query: query, page: page), .folder)
        case .account:
            return (SearchQueryParams(accountId: k.account, query: query, page: page), .account)
        case .all:
            return (SearchQueryParams(query: query, page: page), .all)
        }
    }

    /// Replaces the list with the first page of results.
    public mutating func setSearchResults(_ res: SearchQueryResult) {
        search.hits = [:]
        setMessages(takeHits(res.results), page: res.page)
    }

    /// Adds a further page and returns how many results were new. A result
    /// listed already keeps the excerpt it came with.
    @discardableResult
    public mutating func appendSearchResults(_ res: SearchQueryResult) -> Int {
        let fresh = res.results.filter { index[$0.message.id] == nil }
        return appendMessages(takeHits(fresh), page: res.page)
    }

    /// Remembers the excerpts of `results` and returns their summaries.
    private mutating func takeHits(_ results: [SearchResult]) -> [MessageSummary] {
        for r in results {
            search.hits[r.message.id] = SearchHit(snippet: r.snippet, ranges: r.ranges ?? [])
        }
        return results.map(\.message)
    }

    /// Empties the list of results.
    public mutating func clearSearchResults() {
        clearMessages()
        search.hits = [:]
    }

    /// What the list row of `s` displays: its summary, and in search the
    /// excerpt with the matched words and where the message lies
    /// (search_model.go `rowMessage`).
    public func rowMessage(_ s: MessageSummary) -> RowMessage {
        var m = summaryMessage(s)
        guard search.active else { return m }
        if let hit = search.hits[s.id] {
            m.snippet = hit.snippet
            m.highlights = hit.ranges
        }
        let origin = searchOrigin(s)
        m.origin = origin.label
        m.originTooltip = origin.tooltip
        return m
    }

    /// Where a result lies when the search spans more than one folder: the
    /// folder, and its account as well when every account is searched and
    /// there is more than one. The tooltip has the folder's path and the
    /// account (search_model.go `searchOrigin`).
    public func searchOrigin(_ s: MessageSummary) -> (label: String, tooltip: String) {
        guard search.effective != .folder, let f = folder(FolderKey(account: s.accountId, folder: s.folderId)) else {
            return ("", "")
        }
        var label = folderTitle(f)
        var tooltip = f.path.trimmingCharacters(in: .whitespacesAndNewlines)
        if tooltip.isEmpty {
            tooltip = label
        }
        if let a = account(s.accountId) {
            tooltip += "\n" + accountLabel(a)
            if search.effective == .all, enabledAccounts(accounts).count > 1 {
                // TRANSLATORS: where a search result lies, shown in its row:
                // the folder, then the account.
                label = L10n.format(L10n.C("search result origin", "%s · %s"), [label, accountLabel(a)])
            }
        }
        return (label, tooltip)
    }

    /// The scope bar for the current selection (search.go
    /// `refreshSearchScope`).
    public func searchBar() -> SearchBarState {
        let f = selected.flatMap { folder($0) }
        let folderTip = f.map { L10n.T("Search in %s", folderTitle($0)) } ?? L10n.T("Select a folder to search in it")
        let accountTip = selected.flatMap { account($0.account) }.map {
            L10n.T("Search every folder of %s except Trash and Junk", accountLabel($0))
        } ?? ""
        return SearchBarState(
            scope: search.scope, narrowEnabled: f != nil, folderTooltip: folderTip, accountTooltip: accountTip,
            allTooltip: L10n.T("Search every account except Trash and Junk")
        )
    }
}

/// How far back the local store, and so search, reaches: the offlineDays
/// window, everything, or (before config.get answered) just that it is the
/// mail on this computer (search_model.go `searchRetentionText`).
public func searchRetentionText(days: Int, known: Bool) -> String {
    if !known {
        return L10n.T("Searches the mail stored on this computer.")
    }
    if days <= 0 {
        return L10n.T("Searches all mail stored on this computer.")
    }
    return L10n.N(
        "Searches the mail of the last %d day stored on this computer.",
        "Searches the mail of the last %d days stored on this computer.", days
    )
}

/// The subtitle while searching: how many results there are, "more than"
/// beyond what the daemon counts, nothing while the count is not known
/// (search_model.go `searchTotalText`).
public func searchTotalText(total: Int, shown: Bool) -> String {
    guard shown else { return "" }
    if total < 0 {
        return L10n.T("More than %d results", API.Limits.maxSearchTotal)
    }
    return L10n.N("%d result", "%d results", total)
}

/// An empty result explained in its scope; the wider ones say that Trash
/// and Junk were left out (search_model.go `searchEmptyText`).
public func searchEmptyText(_ scope: Settings.SearchScope) -> String {
    switch scope {
    case .folder:
        return L10n.T("Nothing in this folder matches.")
    case .account:
        return L10n.T("Nothing in this account matches. Trash and Junk are searched only when chosen as the folder.")
    case .all:
        return L10n.T("Nothing in any account matches. Trash and Junk are searched only when chosen as the folder.")
    }
}

/// The most bold ranges one excerpt gets (widget/highlight.go
/// `maxHighlights`).
public let maxHighlights = 32

/// The match ranges of an excerpt as ranges of an attributed string
/// (widget/highlight.go `validRanges`): the daemon's are UTF-8 byte
/// ranges, NSAttributedString counts UTF-16 units. Only ranges that can
/// be applied as they are survive: inside the text, not empty, starting
/// and ending on a character boundary, in order and not overlapping, at
/// most `maxHighlights`. The daemon promises all of that; a range that
/// breaks it is dropped rather than trusted.
public func highlightRanges(_ text: String, _ ranges: [MatchRange]) -> [NSRange] {
    let utf8 = Array(text.utf8)
    let boundary: (Int) -> Bool = { i in i == utf8.count || (utf8[i] & 0xC0) != 0x80 }
    let valid = ranges.filter { r in
        r.start >= 0 && r.start < r.end && r.end <= utf8.count && boundary(r.start) && boundary(r.end)
    }.sorted { $0.start < $1.start }
    var out: [NSRange] = []
    var end = -1
    for r in valid {
        if r.start < end {
            continue
        }
        let from = text.utf8.index(text.utf8.startIndex, offsetBy: r.start)
        let to = text.utf8.index(text.utf8.startIndex, offsetBy: r.end)
        out.append(NSRange(from..<to, in: text))
        end = r.end
        if out.count == maxHighlights {
            break
        }
    }
    return out
}
