// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

private let searchLog = Logger(subsystem: "io.github.schotek.Malachi", category: "search")

/// The search over the message list (ui/internal/window/search.go): while
/// a search is on the list shows search.query results for the text, in
/// the chosen scope, instead of the selected folder. The daemon searches
/// its local store; the list only sends the text and shows the answer. The
/// results are a snapshot: they are asked for again when the text, the
/// scope or (for the Folder and Account scopes) the selected folder
/// changes, not when mail arrives, so nothing moves under the pointer.
///
/// On the Mac the search field is in the toolbar, as in Mail: a search is
/// on while the field holds text (`setSearchText`), Return selects the
/// first result (`activateSearch`), and the scope bar over the list sets
/// the scope (`setSearchScope`).
extension ListController {
    /// Whether the list shows search results.
    public var searchActive: Bool {
        mailbox.model.search.active
    }

    /// What a message row displays: the summary, and in search the excerpt
    /// and where the message lies.
    public func rowMessage(_ s: MessageSummary) -> RowMessage {
        mailbox.model.rowMessage(s)
    }

    /// The search field's text changed (after the pause the field waits
    /// for): text starts a search, or narrows the one on; an emptied field
    /// ends it and the folder is listed again.
    public func setSearchText(_ text: String) {
        if text.isEmpty {
            setSearchActive(false)
            return
        }
        setSearchActive(true)
        mailbox.model.search.text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        runSearch(force: false)
    }

    /// Return in the search field: the first result is selected, now when
    /// the results for the text are on show, otherwise as soon as they
    /// arrive (search.go `onSearchActivate`).
    public func activateSearch(_ text: String) {
        guard !text.isEmpty else { return }
        setSearchActive(true)
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        mailbox.model.search.text = trimmed
        let (params, _) = mailbox.model.searchRequest(trimmed, scope: mailbox.model.search.scope)
        if mailbox.model.search.shown, params == mailbox.model.search.params {
            focusFirstResult()
            return
        }
        mailbox.model.search.focusFirst = true
        runSearch(force: false)
    }

    /// The scope bar: the choice is kept for the next search (search.go
    /// `onSearchScopeChanged`).
    public func setSearchScope(_ scope: Settings.SearchScope) {
        guard scope != mailbox.model.search.scope else { return }
        mailbox.model.search.scope = scope
        settings.searchScope = scope
        publishSearchBar()
        runSearch(force: false)
        mailbox.refreshListTitle()
    }

    /// Switches the list between the folder and search (search.go
    /// `onSearchModeChanged`): starting empties it and shows the prompt,
    /// ending loads the folder again.
    public func setSearchActive(_ on: Bool) {
        guard on != mailbox.model.search.active else { return }
        mailbox.model.search.active = on
        mailbox.model.search.shown = false
        mailbox.model.search.focusFirst = false
        mailbox.model.search.params = SearchQueryParams(query: "")
        mailbox.model.search.text = ""
        _ = mailbox.model.bumpList()
        mailbox.model.loading = false
        mailbox.model.loadingMore = false
        mailbox.model.listErr = nil
        mailbox.model.clearSearchResults()
        if on {
            // No folder is listed: new mail and outbox changes leave the
            // results alone (applyNewMessage, refreshOutboxViews).
            mailbox.model.listFolder = nil
            mailbox.model.grouped = false
            mailbox.model.search.scope = settings.searchScope
            loadOfflineDays()
            reconcile(.clear)
            publishSearchBar()
        } else {
            reconcile(.clear)
            onSearchBar?(nil)
            loadMessages()
        }
        mailbox.refreshListTitle()
        showLoadMore()
    }

    /// Asks for the first page of results for the text in the chosen scope
    /// (search.go `runSearch`). With too little typed the prompt is shown
    /// instead; the same request as the results on show (or the one on its
    /// way) is not sent again unless `force`.
    func runSearch(force: Bool) {
        guard mailbox.model.search.active else { return }
        let text = mailbox.model.search.text
        if !searchReady(text) {
            _ = mailbox.model.bumpList()
            mailbox.model.search.params = SearchQueryParams(query: "")
            mailbox.model.search.shown = false
            mailbox.model.search.focusFirst = false
            mailbox.model.loading = false
            mailbox.model.listErr = nil
            if mailbox.model.rowCount > 0 {
                mailbox.model.clearSearchResults()
                reconcile(.clear)
            } else {
                showListState()
            }
            mailbox.refreshListTitle()
            return
        }
        let (params, effective) = mailbox.model.searchRequest(text, scope: mailbox.model.search.scope)
        if !force, params == mailbox.model.search.params, mailbox.model.search.shown || mailbox.model.loading {
            return
        }
        mailbox.model.search.params = params
        mailbox.model.search.effective = effective
        mailbox.model.search.shown = false
        let gen = mailbox.model.bumpList()
        mailbox.model.loading = true
        mailbox.model.loadingMore = false
        mailbox.model.listErr = nil
        showListState()
        mailbox.refreshListTitle()
        mailbox.perform(API.SearchQuery.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen, self.mailbox.model.search.active else { return }
            self.mailbox.model.loading = false
            switch outcome {
            case .failure(let err):
                // The query is what the user typed: never logged.
                searchLog.warning("search.query: \(String(describing: err), privacy: .public)")
                self.mailbox.model.clearSearchResults()
                self.mailbox.model.listErr = err
                self.reconcile(.clear)
                self.mailbox.refreshListTitle()
            case .success(let res):
                self.mailbox.model.search.shown = true
                self.mailbox.model.setSearchResults(res)
                self.reconcile(.keep)
                // A message listed for the text before keeps its row, but
                // its excerpt belongs to the new text: every row redraws.
                self.onRowsRefreshed?(self.rows.map(\.key))
                self.mailbox.refreshListTitle()
                if self.mailbox.model.search.focusFirst {
                    self.mailbox.model.search.focusFirst = false
                    self.focusFirstResult()
                }
            }
        }
    }

    /// Fetches the next page of results (`loadMore` while searching;
    /// search.go `loadMoreSearch`).
    func loadMoreSearch(_ gen: UInt64, cursor: String) {
        var params = mailbox.model.search.params
        params.page = Page(cursor: cursor, limit: API.Limits.defaultPageLimit)
        mailbox.perform(API.SearchQuery.self, params) { [weak self] outcome in
            guard let self, gen == self.mailbox.model.listGen, self.mailbox.model.search.active else { return }
            self.mailbox.model.loadingMore = false
            switch outcome {
            case .failure(let err):
                searchLog.warning("search.query (more): \(String(describing: err), privacy: .public)")
                self.mailbox.toast(rpcErrorText(L10n.T("Loading more results"), err))
                self.showLoadMore() // the cursor is still there; the button offers a retry
            case .success(let res):
                self.mailbox.model.appendSearchResults(res)
                self.reconcile(.keep)
            }
        }
    }

    /// What the list pane shows while searching and no result is listed
    /// (search.go `showSearchState`): the prompt, the search under way,
    /// its failure, or no results.
    func searchListState() -> ListState {
        let m = mailbox.model
        let st = m.search
        if !searchReady(st.text) {
            return .status(
                icon: "edit-find-symbolic", title: L10n.T("Search Mail"),
                description: searchRetentionText(days: st.offlineDays, known: st.offlineKnown), retry: false
            )
        }
        if let err = m.listErr {
            return .status(
                icon: "dialog-warning-symbolic", title: L10n.T("Search Failed"),
                description: rpcErrorText(L10n.T("Searching"), err), retry: true
            )
        }
        if m.loading || !st.shown {
            return .status(icon: "", title: L10n.T("Searching…"), description: "", retry: false)
        }
        return .status(icon: "edit-find-symbolic", title: L10n.T("No Results"), description: searchEmptyText(st.effective), retry: false)
    }

    /// Announces the scope bar for the current selection while searching.
    func publishSearchBar() {
        guard mailbox.model.search.active else { return }
        onSearchBar?(mailbox.model.searchBar())
    }

    /// Hands the first result to the view to select and focus.
    private func focusFirstResult() {
        guard let first = rows.first else { return }
        onFocusRow?(first.key)
    }

    /// Learns the retention window the retention note names.
    private func loadOfflineDays() {
        mailbox.perform(API.ConfigGet.self, EmptyParams()) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                searchLog.debug("config.get for the search note: \(String(describing: err), privacy: .public)")
            case .success(let got):
                self.mailbox.model.search.offlineDays = got.preferences.offlineDays
                self.mailbox.model.search.offlineKnown = true
                if self.mailbox.model.search.active {
                    self.showListState()
                    self.showLoadMore()
                }
            }
        }
    }
}
