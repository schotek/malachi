// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The search side of the list model. i18n is not bound in tests, so the
// English msgids come back verbatim.

func searchModel() *mailModel {
	return &mailModel{
		accounts: []api.Account{
			{ID: "a1", Config: api.AccountConfig{Name: "Work", Email: "me@work.example"}, Enabled: true},
			{ID: "a2", Config: api.AccountConfig{Email: "me@home.example"}, Enabled: true},
		},
		folders: map[api.AccountID][]api.Folder{
			"a1": {
				{ID: "f_in", AccountID: "a1", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox},
				{ID: "f_x", AccountID: "a1", Name: "Faktury", Path: "Archiv/Faktury"},
			},
			"a2": {{ID: "g_in", AccountID: "a2", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox}},
		},
		selected: folderKey{Account: "a1", Folder: "f_in"},
	}
}

func TestSearchReady(t *testing.T) {
	for in, want := range map[string]bool{"": false, "a": false, "  a  ": false, "ab": true, "př": true, "ř": false} {
		if got := searchReady(in); got != want {
			t.Errorf("searchReady(%q) = %v", in, got)
		}
	}
}

func TestSearchRequest(t *testing.T) {
	m := searchModel()
	page := api.Page{Limit: api.DefaultPageLimit}
	tests := []struct {
		scope     settings.SearchScope
		want      api.SearchQueryParams
		effective settings.SearchScope
	}{
		{settings.SearchFolder, api.SearchQueryParams{AccountID: "a1", FolderID: "f_in", Query: "faktura", Page: page}, settings.SearchFolder},
		{settings.SearchAccount, api.SearchQueryParams{AccountID: "a1", Query: "faktura", Page: page}, settings.SearchAccount},
		{settings.SearchAll, api.SearchQueryParams{Query: "faktura", Page: page}, settings.SearchAll},
		{"", api.SearchQueryParams{Query: "faktura", Page: page}, settings.SearchAll},
	}
	for _, tc := range tests {
		got, eff := m.searchRequest("  faktura ", tc.scope)
		if got != tc.want || eff != tc.effective {
			t.Errorf("%q: %+v %q, want %+v %q", tc.scope, got, eff, tc.want, tc.effective)
		}
	}
	// Nothing selected: nothing to narrow to.
	m.selected = folderKey{}
	if got, eff := m.searchRequest("x", settings.SearchFolder); got.AccountID != "" || got.FolderID != "" || eff != settings.SearchAll {
		t.Errorf("no folder: %+v %q", got, eff)
	}
}

func TestSearchResultsAndRows(t *testing.T) {
	m := searchModel()
	sum := func(id api.MessageID, acc api.AccountID, folder api.FolderID) api.MessageSummary {
		return api.MessageSummary{ID: id, AccountID: acc, FolderID: folder, Subject: string(id), Snippet: "summary", Flags: []api.Flag{api.FlagSeen}}
	}
	hit := []api.MatchRange{{Start: 3, End: 8}}
	m.search.active = true
	m.search.effective = settings.SearchAll
	m.setSearchResults(api.SearchQueryResult{
		Results: []api.SearchResult{
			{Message: sum("m1", "a1", "f_x"), Snippet: "…a přílohy", Ranges: hit},
			{Message: sum("m2", "a2", "g_in"), Snippet: "summary"},
		},
		Page: api.PageInfo{NextCursor: "c", Total: 3},
	})
	if len(m.messages) != 2 || m.nextCursor != "c" || m.total != 3 {
		t.Fatalf("first page: %d messages, cursor %q, total %d", len(m.messages), m.nextCursor, m.total)
	}
	if added := m.appendSearchResults(api.SearchQueryResult{
		Results: []api.SearchResult{{Message: sum("m3", "a1", "f_in"), Snippet: "x"}, {Message: sum("m1", "a1", "f_x")}},
		Page:    api.PageInfo{Total: 3},
	}); added != 1 || len(m.messages) != 3 || m.nextCursor != "" {
		t.Fatalf("second page added %d, %d messages", added, len(m.messages))
	}

	row := m.rowMessage(m.messages[0])
	if row.Snippet != "…a přílohy" || !reflect.DeepEqual(row.Highlights, hit) {
		t.Errorf("excerpt %q %v", row.Snippet, row.Highlights)
	}
	if row.Origin != "Faktury · Work" || row.OriginTooltip != "Archiv/Faktury\nWork" {
		t.Errorf("all accounts: origin %q, tooltip %q", row.Origin, row.OriginTooltip)
	}
	if row := m.rowMessage(m.messages[1]); row.Origin != "Inbox · me@home.example" {
		t.Errorf("an account without a name: %q", row.Origin)
	}

	m.search.effective = settings.SearchAccount
	if row := m.rowMessage(m.messages[0]); row.Origin != "Faktury" {
		t.Errorf("account scope: %q", row.Origin)
	}
	m.search.effective = settings.SearchFolder
	if row := m.rowMessage(m.messages[0]); row.Origin != "" || row.OriginTooltip != "" {
		t.Errorf("folder scope: %q", row.Origin)
	}
	// One enabled account: no account in the label.
	m.search.effective = settings.SearchAll
	m.accounts[1].Enabled = false
	if row := m.rowMessage(m.messages[0]); row.Origin != "Faktury" {
		t.Errorf("one account: %q", row.Origin)
	}

	// Outside search a row is the plain summary.
	m.search.active = false
	if row := m.rowMessage(m.messages[0]); row.Snippet != "summary" || row.Highlights != nil || row.Origin != "" {
		t.Errorf("outside search: %+v", row)
	}
	m.clearSearchResults()
	if len(m.messages) != 0 || m.search.hits != nil {
		t.Error("clear left results behind")
	}
}

func TestSearchTexts(t *testing.T) {
	for _, tc := range []struct {
		days  int
		known bool
		want  string
	}{
		{90, true, "Searches the mail of the last 90 days stored on this computer."},
		{1, true, "Searches the mail of the last 1 day stored on this computer."},
		{0, true, "Searches all mail stored on this computer."},
		{0, false, "Searches the mail stored on this computer."},
	} {
		if got := searchRetentionText(tc.days, tc.known); got != tc.want {
			t.Errorf("retention %d/%v: %q", tc.days, tc.known, got)
		}
	}
	if got := searchTotalText(3, true); got != "3 results" {
		t.Errorf("total: %q", got)
	}
	if got := searchTotalText(-1, true); got != "More than 1000 results" {
		t.Errorf("uncounted: %q", got)
	}
	if got := searchTotalText(3, false); got != "" {
		t.Errorf("not shown: %q", got)
	}
	if searchEmptyText(settings.SearchFolder) == searchEmptyText(settings.SearchAll) {
		t.Error("the empty texts should name the scope")
	}
}
