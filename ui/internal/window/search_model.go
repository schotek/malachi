// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The search side of the message list, without GTK (search.go drives it):
// while the search bar is open the flat list holds search.query results
// instead of the selected folder's messages. The results are summaries
// like any listing, each carrying its own account and folder, so every
// action on a row works unchanged.

// searchMinRunes is how much has to be typed before a search runs.
const searchMinRunes = 2

// searchState is what the list knows of the search bar.
type searchState struct {
	active bool
	text   string               // the entry's text, trimmed
	scope  settings.SearchScope // as chosen in the bar
	// effective is the scope of params: Folder and Account fall back to
	// All while no folder is selected.
	effective settings.SearchScope
	params    api.SearchQueryParams // of the results on show, or on their way
	shown     bool                  // the results of params are on show
	hits      map[api.MessageID]searchHit
	// focusFirst: Enter was pressed before the results came; the first
	// one is selected when they do.
	focusFirst bool
	// offlineDays is the retention window from config.get, which the
	// retention note names; unknown until that answered.
	offlineDays  int
	offlineKnown bool
}

// searchHit is what a result shows beyond its summary: the excerpt around
// the match and the matched words in it.
type searchHit struct {
	snippet string
	ranges  []api.MatchRange
}

// searchReady reports whether enough was typed to search.
func searchReady(text string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(text)) >= searchMinRunes
}

// searchRequest is the first-page search.query for text in scope, and the
// scope it actually covers: without a selected folder there is nothing to
// narrow Folder or Account to, so every account is searched.
func (m *mailModel) searchRequest(text string, scope settings.SearchScope) (api.SearchQueryParams, settings.SearchScope) {
	p := api.SearchQueryParams{Query: strings.TrimSpace(text), Page: api.Page{Limit: api.DefaultPageLimit}}
	k := m.selected
	if k == (folderKey{}) {
		scope = settings.SearchAll
	}
	switch scope {
	case settings.SearchFolder:
		p.AccountID, p.FolderID = k.Account, k.Folder
	case settings.SearchAccount:
		p.AccountID = k.Account
	default:
		scope = settings.SearchAll
	}
	return p, scope
}

// setSearchResults replaces the list with the first page of results.
func (m *mailModel) setSearchResults(res api.SearchQueryResult) {
	m.search.hits = make(map[api.MessageID]searchHit, len(res.Results))
	m.setMessages(m.takeHits(res.Results), res.Page)
}

// appendSearchResults adds a further page and returns how many results
// were new. A result listed already keeps the excerpt it came with.
func (m *mailModel) appendSearchResults(res api.SearchQueryResult) int {
	if m.search.hits == nil {
		m.search.hits = make(map[api.MessageID]searchHit, len(res.Results))
	}
	fresh := make([]api.SearchResult, 0, len(res.Results))
	for _, r := range res.Results {
		if _, _, listed := m.message(r.Message.ID); !listed {
			fresh = append(fresh, r)
		}
	}
	return m.appendMessages(m.takeHits(fresh), res.Page)
}

// takeHits remembers the excerpts of results and returns their summaries.
func (m *mailModel) takeHits(results []api.SearchResult) []api.MessageSummary {
	list := make([]api.MessageSummary, 0, len(results))
	for _, r := range results {
		list = append(list, r.Message)
		m.search.hits[r.Message.ID] = searchHit{snippet: r.Snippet, ranges: r.Ranges}
	}
	return list
}

// clearSearchResults empties the list of results.
func (m *mailModel) clearSearchResults() {
	m.clearMessages()
	m.search.hits = nil
}

// rowMessage is what the list row of s displays: its summary, and in
// search the excerpt with the matched words and where the message lies.
func (m *mailModel) rowMessage(s api.MessageSummary) widget.Message {
	msg := summaryMessage(s)
	if !m.search.active {
		return msg
	}
	if h, ok := m.search.hits[s.ID]; ok {
		msg.Snippet, msg.Highlights = h.snippet, h.ranges
	}
	msg.Origin, msg.OriginTooltip = m.searchOrigin(s)
	return msg
}

// searchOrigin names where a result lies when the search spans more than
// one folder: the folder, and its account as well when every account is
// searched and there is more than one. The tooltip has the folder's path
// and the account.
func (m *mailModel) searchOrigin(s api.MessageSummary) (label, tooltip string) {
	if m.search.effective == settings.SearchFolder {
		return "", ""
	}
	f, ok := m.folder(folderKey{Account: s.AccountID, Folder: s.FolderID})
	if !ok {
		return "", ""
	}
	label, tooltip = folderTitle(f), strings.TrimSpace(f.Path)
	if tooltip == "" {
		tooltip = label
	}
	if a, ok := m.account(s.AccountID); ok {
		tooltip += "\n" + accountLabel(a)
		if m.search.effective == settings.SearchAll && len(m.enabledAccounts()) > 1 {
			// TRANSLATORS: where a search result lies, shown in its row:
			// the folder, then the account.
			label = fmt.Sprintf(i18n.C("search result origin", "%s · %s"), label, accountLabel(a))
		}
	}
	return label, tooltip
}

// searchRetentionText says how far back the local store, and so search,
// reaches: the offlineDays window, everything, or (before config.get
// answered) just that it is the mail on this computer.
func searchRetentionText(days int, known bool) string {
	switch {
	case !known:
		return i18n.T("Searches the mail stored on this computer.")
	case days <= 0:
		return i18n.T("Searches all mail stored on this computer.")
	}
	return fmt.Sprintf(i18n.N("Searches the mail of the last %d day stored on this computer.",
		"Searches the mail of the last %d days stored on this computer.", days), days)
}

// searchTotalText is the subtitle of the list while searching: how many
// results there are, "more than" beyond what the daemon counts, nothing
// while the count is not known.
func searchTotalText(total int, shown bool) string {
	switch {
	case !shown:
		return ""
	case total < 0:
		return fmt.Sprintf(i18n.T("More than %d results"), api.MaxSearchTotal)
	}
	return fmt.Sprintf(i18n.N("%d result", "%d results", total), total)
}

// searchEmptyText explains an empty result in its scope; the wider ones
// say that Trash and Junk were left out.
func searchEmptyText(scope settings.SearchScope) string {
	switch scope {
	case settings.SearchFolder:
		return i18n.T("Nothing in this folder matches.")
	case settings.SearchAccount:
		return i18n.T("Nothing in this account matches. Trash and Junk are searched only when chosen as the folder.")
	}
	return i18n.T("Nothing in any account matches. Trash and Junk are searched only when chosen as the folder.")
}
