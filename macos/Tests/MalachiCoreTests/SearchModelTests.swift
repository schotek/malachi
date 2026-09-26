// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/search_model_test.go and
// ui/internal/widget/highlight_test.go: the search side of the list model.
// The catalogue is English in tests, so the msgids come back verbatim.

private func searchModel() -> MailModel {
    var m = MailModel(
        accounts: [
            testAccount("a1", name: "Work", email: "me@work.example"),
            testAccount("a2", email: "me@home.example"),
        ],
        folders: [
            "a1": [
                testFolder("f_in", path: "INBOX", role: .inbox, name: "INBOX"),
                testFolder("f_x", path: "Archiv/Faktury", name: "Faktury"),
            ],
            "a2": [testFolder("g_in", path: "INBOX", role: .inbox, name: "INBOX")],
        ]
    )
    m.selected = FolderKey(account: "a1", folder: "f_in")
    return m
}

private func summary(_ id: String, _ acc: String, _ folder: String) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: AccountID(acc), folderId: FolderID(folder), threadId: nil,
        from: [Address(name: "Alice", address: "alice@example.invalid")], subject: id,
        date: Date(timeIntervalSince1970: 0), snippet: "summary", flags: [.seen], hasAttachments: false, size: 0
    )
}

@Suite struct SearchModelTests {
    @Test func ready() {
        for (text, want) in [("", false), ("a", false), ("  a  ", false), ("ab", true), ("př", true), ("ř", false)] {
            #expect(searchReady(text) == want, "\(text)")
        }
    }

    @Test func request() {
        var m = searchModel()
        let page = Page(limit: API.Limits.defaultPageLimit)
        let cases: [(Settings.SearchScope, SearchQueryParams, Settings.SearchScope)] = [
            (.folder, SearchQueryParams(accountId: "a1", folderId: "f_in", query: "faktura", page: page), .folder),
            (.account, SearchQueryParams(accountId: "a1", query: "faktura", page: page), .account),
            (.all, SearchQueryParams(query: "faktura", page: page), .all),
        ]
        for (scope, want, effective) in cases {
            let got = m.searchRequest("  faktura ", scope: scope)
            #expect(got.params == want && got.effective == effective, "\(scope)")
        }
        // Nothing selected: nothing to narrow to.
        m.selected = nil
        let got = m.searchRequest("x", scope: .folder)
        #expect(got.params.accountId == nil && got.params.folderId == nil && got.effective == .all)
    }

    @Test func resultsAndRows() {
        var m = searchModel()
        let hit = [MatchRange(start: 3, end: 8)]
        m.search.active = true
        m.search.effective = .all
        m.setSearchResults(SearchQueryResult(
            results: [
                SearchResult(message: summary("m1", "a1", "f_x"), snippet: "…a přílohy", ranges: hit, score: 0),
                SearchResult(message: summary("m2", "a2", "g_in"), snippet: "summary", score: 0),
            ],
            page: PageInfo(nextCursor: "c", total: 3)
        ))
        #expect(m.messages.count == 2 && m.nextCursor == "c" && m.total == 3)
        let added = m.appendSearchResults(SearchQueryResult(
            results: [
                SearchResult(message: summary("m3", "a1", "f_in"), snippet: "x", score: 0),
                SearchResult(message: summary("m1", "a1", "f_x"), snippet: "", score: 0),
            ],
            page: PageInfo(total: 3)
        ))
        #expect(added == 1 && m.messages.count == 3 && m.nextCursor == nil)

        // A repeated result keeps the excerpt it came with.
        let row = m.rowMessage(m.messages[0])
        #expect(row.snippet == "…a přílohy" && row.highlights == hit)
        #expect(row.origin == "Faktury · Work" && row.originTooltip == "Archiv/Faktury\nWork")
        #expect(m.rowMessage(m.messages[1]).origin == "Inbox · me@home.example")

        m.search.effective = .account
        #expect(m.rowMessage(m.messages[0]).origin == "Faktury")
        m.search.effective = .folder
        #expect(m.rowMessage(m.messages[0]).origin == "" && m.rowMessage(m.messages[0]).originTooltip == "")
        // One enabled account: no account in the label.
        m.search.effective = .all
        m.accounts[1].enabled = false
        #expect(m.rowMessage(m.messages[0]).origin == "Faktury")

        // Outside search a row is the plain summary.
        m.search.active = false
        let plain = m.rowMessage(m.messages[0])
        #expect(plain.snippet == "summary" && plain.highlights.isEmpty && plain.origin.isEmpty)
        m.clearSearchResults()
        #expect(m.messages.isEmpty && m.search.hits.isEmpty)
    }

    @Test func bar() {
        var m = searchModel()
        m.search.scope = .account
        var bar = m.searchBar()
        #expect(bar.scope == .account && bar.narrowEnabled)
        #expect(bar.folderTooltip == "Search in Inbox")
        #expect(bar.accountTooltip == "Search every folder of Work except Trash and Junk")
        m.selected = nil
        bar = m.searchBar()
        #expect(!bar.narrowEnabled && bar.folderTooltip == "Select a folder to search in it" && bar.accountTooltip.isEmpty)
    }

    @Test func texts() {
        #expect(searchRetentionText(days: 90, known: true) == "Searches the mail of the last 90 days stored on this computer.")
        #expect(searchRetentionText(days: 1, known: true) == "Searches the mail of the last 1 day stored on this computer.")
        #expect(searchRetentionText(days: 0, known: true) == "Searches all mail stored on this computer.")
        #expect(searchRetentionText(days: 0, known: false) == "Searches the mail stored on this computer.")
        #expect(searchTotalText(total: 3, shown: true) == "3 results")
        #expect(searchTotalText(total: -1, shown: true) == "More than 1000 results")
        #expect(searchTotalText(total: 3, shown: false) == "")
        #expect(searchEmptyText(.folder) != searchEmptyText(.all))
    }

    @Test func highlights() {
        let text = "posílám přílohy k faktuře" // "í" and "ř" are two bytes each
        let r = { (s: Int, e: Int) in MatchRange(start: s, end: e) }
        let cases: [(String, [MatchRange], [String])] = [
            ("good", [r(10, 19)], ["přílohy"]),
            ("sorted", [r(22, 30), r(10, 19)], ["přílohy", "faktuře"]),
            ("overlap dropped", [r(10, 19), r(11, 20)], ["přílohy"]),
            ("mid-character start", [r(4, 8)], []),
            ("mid-character end", [r(0, 4)], []),
            ("outside", [r(-1, 3), r(10, 99), r(5, 5), r(8, 6)], []),
        ]
        let ns = text as NSString
        for (name, ranges, want) in cases {
            let got = highlightRanges(text, ranges).map { ns.substring(with: $0) }
            #expect(got == want, "\(name)")
        }
        let many = (0..<50).map { r($0, $0 + 1) }
        #expect(highlightRanges(String(repeating: "x", count: 60), many).count == maxHighlights)
    }
}
