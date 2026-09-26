// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// searchBox is two accounts with Czech mail in an inbox, Trash and a
// nested folder, and one message in the second account.
type searchBox struct {
	b                          *Backend
	acc, other                 api.AccountID
	inbox, trash, junk, nested api.FolderID
}

func seedSearchBox(t *testing.T) *searchBox {
	t.Helper()
	old := searchTimeZone
	searchTimeZone = time.UTC
	t.Cleanup(func() { searchTimeZone = old })
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	other := seedAccount(t, b, "other@example.invalid")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
		{Mailbox: "Archiv/Faktury", Name: "Faktury", Path: "Archiv/Faktury", Subscribed: true, Selectable: true},
	})
	otherInbox := seedFolders(t, b, other, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
	})["INBOX"].ID
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 12, 0, 0, 0, time.UTC) }
	jiri := []api.Address{{Name: "Jiří Novák", Address: "jiri@example.cz"}}
	radka := []api.Address{{Name: "Radka Kovářová", Address: "radka@example.cz"}}
	rows := []struct {
		m    *store.Message
		body string
		att  string
	}{
		{&store.Message{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 1, Subject: "Faktura za září", Date: day(9, 10), From: jiri, To: radka},
			"Dobrý den, posílám přílohy k faktuře.", "vyuctovani.pdf"},
		{&store.Message{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 2, Subject: "Schůzka", Date: day(9, 20), From: radka, To: jiri,
			Flags: []api.Flag{api.FlagSeen, api.FlagFlagged}}, "Sejdeme se v pondělí. Faktura dorazí později.", ""},
		{&store.Message{AccountID: acc, FolderID: folders["Trash"].ID, UID: 3, Subject: "Stará faktura", Date: day(8, 1), From: jiri,
			Flags: []api.Flag{api.FlagSeen}}, "faktura smazána", ""},
		{&store.Message{AccountID: acc, FolderID: folders["Junk"].ID, UID: 4, Subject: "Faktura výhra", Date: day(8, 2),
			Flags: []api.Flag{api.FlagSeen}}, "faktura podvod", ""},
		{&store.Message{AccountID: acc, FolderID: folders["Archiv/Faktury"].ID, UID: 5, Subject: "Faktura srpen", Date: day(8, 15), From: jiri,
			Flags: []api.Flag{api.FlagSeen}}, "srpnová faktura", ""},
		{&store.Message{AccountID: other, FolderID: otherInbox, UID: 1, Subject: "Faktura jiný účet", Date: day(9, 25),
			Flags: []api.Flag{api.FlagSeen}}, "faktura", ""},
	}
	for _, r := range rows {
		if err := b.store.UpsertMessages(ctx, []*store.Message{r.m}); err != nil {
			t.Fatal(err)
		}
		u := store.BodyUpdate{Text: r.body, Snippet: "summary snippet"}
		if r.att != "" {
			u.Attachments = []api.Attachment{{PartID: "2", Filename: r.att, ContentType: "application/pdf"}}
			u.HasAttachments = true
		}
		if err := b.store.SetMessageBody(ctx, r.m.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	return &searchBox{b: b, acc: api.AccountID(acc), other: api.AccountID(other),
		inbox: api.FolderID(folders["INBOX"].ID), trash: api.FolderID(folders["Trash"].ID),
		junk: api.FolderID(folders["Junk"].ID), nested: api.FolderID(folders["Archiv/Faktury"].ID)}
}

func (sb *searchBox) query(t *testing.T, p api.SearchQueryParams) *api.SearchQueryResult {
	t.Helper()
	res, err := sb.b.Search().Query(context.Background(), p)
	if err != nil {
		t.Fatalf("%+v: %v", p, err)
	}
	return res
}

func subjects(res *api.SearchQueryResult) []string {
	out := []string{}
	for _, r := range res.Results {
		out = append(out, r.Message.Subject)
	}
	return out
}

func TestSearchScopesAndSyntax(t *testing.T) {
	sb := seedSearchBox(t)
	acc := func(q string) api.SearchQueryParams { return api.SearchQueryParams{AccountID: sb.acc, Query: q} }
	tests := []struct {
		name string
		p    api.SearchQueryParams
		want []string
	}{
		{"account leaves Trash and Junk out", acc("faktur"), []string{"Schůzka", "Faktura za září", "Faktura srpen"}},
		{"folder", api.SearchQueryParams{AccountID: sb.acc, FolderID: sb.inbox, Query: "faktur"}, []string{"Schůzka", "Faktura za září"}},
		{"Trash as the folder", api.SearchQueryParams{AccountID: sb.acc, FolderID: sb.trash, Query: "faktur"}, []string{"Stará faktura"}},
		{"all accounts", api.SearchQueryParams{Query: "faktur"}, []string{"Faktura jiný účet", "Schůzka", "Faktura za září", "Faktura srpen"}},
		{"in: role keyword", acc("faktur in:trash"), []string{"Stará faktura"}},
		{"in: spam", acc("faktur in:spam"), []string{"Faktura výhra"}},
		{"in: folder name", acc("faktur in:faktury"), []string{"Faktura srpen"}},
		{"in: path", acc(`faktur in:"Archiv/Faktury"`), []string{"Faktura srpen"}},
		{"in: two folders", acc("faktur in:inbox in:trash"), []string{"Schůzka", "Faktura za září", "Stará faktura"}},
		{"in: nothing", acc("faktur in:nowhere"), []string{}},
		{"in: outside the folder", api.SearchQueryParams{AccountID: sb.acc, FolderID: sb.inbox, Query: "faktur in:trash"}, []string{}},
		{"prefix without diacritics", acc("priloh"), []string{"Faktura za září"}},
		{"upper case", acc("PŘÍLOHY"), []string{"Faktura za září"}},
		{"phrase", acc(`"dobry den"`), []string{"Faktura za září"}},
		{"phrase out of order", acc(`"den dobry"`), []string{}},
		{"sender", acc("from:jiri"), []string{"Faktura za září", "Faktura srpen"}},
		{"sender name", acc("from:novak"), []string{"Faktura za září", "Faktura srpen"}},
		{"recipient", acc("to:jiri"), []string{"Schůzka"}},
		{"subject", acc("subject:schuzka"), []string{"Schůzka"}},
		{"attachment name", acc("vyuctovani"), []string{"Faktura za září"}},
		{"unread", acc("faktur is:unread"), []string{"Faktura za září"}},
		{"flagged", acc("is:flagged"), []string{"Schůzka"}},
		{"has:attachment", acc("has:attachment"), []string{"Faktura za září"}},
		{"after", acc("faktur after:2026-09-15"), []string{"Schůzka"}},
		{"before", acc("faktur before:2026-09-15"), []string{"Faktura za září", "Faktura srpen"}},
		{"after is inclusive", acc("after:2026-09-20"), []string{"Schůzka"}},
		{"punctuation only", acc("--- !!"), []string{}},
		{"words AND", acc("faktur pondeli"), []string{"Schůzka"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := sb.query(t, tc.p)
			if got := subjects(res); !slices.Equal(got, tc.want) {
				t.Errorf("%q", got)
			}
			if res.Page.Total != len(tc.want) {
				t.Errorf("total %d", res.Page.Total)
			}
		})
	}

	if _, err := sb.b.Accounts().SetEnabled(context.Background(), api.AccountSetEnabledParams{AccountID: sb.other, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got := subjects(sb.query(t, api.SearchQueryParams{Query: "jiny"})); len(got) != 0 {
		t.Errorf("a paused account was searched: %q", got)
	}
}

func TestSearchExcerpt(t *testing.T) {
	sb := seedSearchBox(t)
	res := sb.query(t, api.SearchQueryParams{AccountID: sb.acc, Query: "priloh"})
	r := res.Results[0]
	if len(r.Ranges) != 1 || r.Snippet[r.Ranges[0].Start:r.Ranges[0].End] != "přílohy" {
		t.Errorf("excerpt %q %+v", r.Snippet, r.Ranges)
	}
	if r.Score != 0 {
		t.Errorf("score %v", r.Score)
	}
	// A match in the subject or the people only: the summary snippet.
	res = sb.query(t, api.SearchQueryParams{AccountID: sb.acc, Query: "from:jiri subject:zari"})
	if r := res.Results[0]; r.Snippet != "summary snippet" || len(r.Ranges) != 0 {
		t.Errorf("fallback %q %+v", r.Snippet, r.Ranges)
	}
}

func TestSearchPagingAndErrors(t *testing.T) {
	sb := seedSearchBox(t)
	ctx := context.Background()
	first := sb.query(t, api.SearchQueryParams{AccountID: sb.acc, Query: "faktur", Page: api.Page{Limit: 2}})
	if len(first.Results) != 2 || first.Page.NextCursor == "" || first.Page.Total != 3 {
		t.Fatalf("first page %q %+v", subjects(first), first.Page)
	}
	second := sb.query(t, api.SearchQueryParams{AccountID: sb.acc, Query: "faktur", Page: api.Page{Limit: 2, Cursor: first.Page.NextCursor}})
	if !slices.Equal(subjects(second), []string{"Faktura srpen"}) || second.Page.NextCursor != "" {
		t.Fatalf("second page %q %+v", subjects(second), second.Page)
	}

	tooMany := strings.Repeat("slovo ", api.MaxSearchTerms+1)
	for _, tc := range []struct {
		name string
		p    api.SearchQueryParams
		want api.ErrorCode
	}{
		{"empty", api.SearchQueryParams{Query: ""}, api.CodeInvalidArgument},
		{"blank", api.SearchQueryParams{Query: " \t"}, api.CodeInvalidArgument},
		{"too long", api.SearchQueryParams{Query: strings.Repeat("a", api.MaxSearchQueryBytes+1)}, api.CodeInvalidArgument},
		{"too many terms", api.SearchQueryParams{Query: tooMany}, api.CodeInvalidArgument},
		{"folder without account", api.SearchQueryParams{FolderID: sb.inbox, Query: "x"}, api.CodeInvalidArgument},
		{"unknown account", api.SearchQueryParams{AccountID: "nope", Query: "x"}, api.CodeAccountNotFound},
		{"unknown folder", api.SearchQueryParams{AccountID: sb.acc, FolderID: "nope", Query: "x"}, api.CodeFolderNotFound},
		{"bad cursor", api.SearchQueryParams{AccountID: sb.acc, Query: "x", Page: api.Page{Cursor: "garbage"}}, api.CodeInvalidArgument},
	} {
		if _, err := sb.b.Search().Query(ctx, tc.p); err == nil || errCode(t, err) != tc.want {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.want)
		}
	}
}

func TestSearchBackfillFinishes(t *testing.T) {
	sb := seedSearchBox(t)
	ctx := context.Background()
	if err := sb.b.backfillSearch(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := sb.b.store.GetMeta(ctx, metaSearchIndexed); v != searchIndexedDone {
		t.Fatalf("meta %q", v)
	}
	if err := sb.b.backfillSearch(ctx); err != nil { // a no-op once done
		t.Fatal(err)
	}
	if got := subjects(sb.query(t, api.SearchQueryParams{Query: "faktur"})); len(got) != 4 {
		t.Errorf("after the backfill: %q", got)
	}
}
