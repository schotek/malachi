// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	srch "github.com/schotek/malachi/backend/internal/search"
	"github.com/schotek/malachi/backend/pkg/api"
)

// match is the FTS expression of a user query, as core builds it.
func match(q string) string { return srch.Parse(q).MatchExpr() }

// searchSubjects runs a search and returns the subjects, newest first.
func searchSubjects(t *testing.T, s *Store, f SearchFilter) []string {
	t.Helper()
	rows, _, total, err := s.SearchMessages(context.Background(), f, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Subject)
	}
	if total != len(out) {
		t.Fatalf("total %d, %d rows", total, len(out))
	}
	return out
}

// indexed counts the entries of the full-text index and of its map.
func indexed(t *testing.T, s *Store) (fts, docs int) {
	t.Helper()
	ctx := context.Background()
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages_fts_docsize`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_docs`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO messages_fts (messages_fts) VALUES ('integrity-check')`); err != nil {
		t.Fatalf("integrity-check: %v", err)
	}
	return fts, docs
}

func setBody(t *testing.T, s *Store, id, text string, attachments ...string) {
	t.Helper()
	u := BodyUpdate{Text: text, Snippet: text}
	for i, name := range attachments {
		u.Attachments = append(u.Attachments, api.Attachment{PartID: fmt.Sprint(i + 2), Filename: name, ContentType: "application/pdf"})
		u.HasAttachments = true
	}
	if err := s.SetMessageBody(context.Background(), id, u); err != nil {
		t.Fatal(err)
	}
}

var day0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestSearchIndexFollowsWrites(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, acc, "Archiv", api.RoleArchive)
	all := SearchFilter{AccountID: acc}

	// The envelope is indexed on insert: subject and people.
	m := seedMessage(t, s, inbox, 1, "Faktura za září", day0)
	for _, q := range []string{"zari", "faktur", "from:alice", "subject:fakt", "alice@example"} {
		f := all
		f.Match = match(q)
		if got := searchSubjects(t, s, f); len(got) != 1 {
			t.Errorf("%q: %v", q, got)
		}
	}
	// The body arrives later and is indexed then, attachment names too.
	f := all
	f.Match = match("priloh")
	if got := searchSubjects(t, s, f); len(got) != 0 {
		t.Fatalf("body found before it was stored: %v", got)
	}
	setBody(t, s, m.ID, "Dobrý den, posílám přílohy.", "vyuctovani.pdf")
	for _, q := range []string{"priloh", `"dobry den"`, "vyuctovani", "PŘÍLOHY"} {
		f.Match = match(q)
		if got := searchSubjects(t, s, f); len(got) != 1 {
			t.Errorf("%q after the body: %v", q, got)
		}
	}
	f.Match = match("to:priloh")
	if got := searchSubjects(t, s, f); len(got) != 0 {
		t.Errorf("a field term matched the body: %v", got)
	}

	// A move keeps the entry: folders are joined at query time.
	var docBefore, docAfter int64
	s.db.QueryRowContext(ctx, `SELECT docid FROM search_docs WHERE message_id = ?`, m.ID).Scan(&docBefore)
	if err := s.MoveMessages(ctx, acc, []string{m.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRowContext(ctx, `SELECT docid FROM search_docs WHERE message_id = ?`, m.ID).Scan(&docAfter)
	f.Match, f.FolderIDs = match("priloh"), []string{archive.ID}
	if got := searchSubjects(t, s, f); len(got) != 1 || docBefore != docAfter {
		t.Errorf("after the move: %v, docid %d → %d", got, docBefore, docAfter)
	}
	if n, d := indexed(t, s); n != 1 || d != 1 {
		t.Fatalf("index %d/%d, want 1/1", n, d)
	}

	// Every way a message leaves takes its entry along.
	seedMessage(t, s, inbox, 2, "expunged", day0)
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	seedMessage(t, s, inbox, 3, "reset", day0)
	if err := s.ResetFolder(ctx, inbox.ID, 99); err != nil {
		t.Fatal(err)
	}
	if n, d := indexed(t, s); n != 1 || d != 1 {
		t.Fatalf("after expunge and reset: index %d/%d, want 1/1", n, d)
	}
	if err := s.DeleteAccount(ctx, acc, true); err != nil {
		t.Fatal(err)
	}
	if n, d := indexed(t, s); n != 0 || d != 0 {
		t.Fatalf("after the account went: index %d/%d", n, d)
	}
}

func TestSearchFilters(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ids := seedAccounts(t, s, 2)
	a, b := ids[0], ids[1]
	inbox := seedFolder(t, s, a, "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, a, "Trash", api.RoleTrash)
	other := seedFolder(t, s, b, "INBOX", api.RoleInbox)

	read := seedMessage(t, s, inbox, 1, "read report", day0, api.FlagSeen)
	seedMessage(t, s, inbox, 2, "unread report", day0.Add(24*time.Hour))
	seedMessage(t, s, inbox, 3, "flagged report", day0.Add(48*time.Hour), api.FlagSeen, api.FlagFlagged)
	seedMessage(t, s, trash, 4, "deleted report", day0.Add(72*time.Hour), api.FlagSeen)
	seedMessage(t, s, other, 5, "other report", day0.Add(96*time.Hour), api.FlagSeen)
	setBody(t, s, read.ID, "with a file", "report.pdf")

	tests := []struct {
		name string
		f    SearchFilter
		want []string
	}{
		{"account", SearchFilter{AccountID: a, Match: match("report")},
			[]string{"deleted report", "flagged report", "unread report", "read report"}},
		{"all enabled accounts", SearchFilter{Match: match("report")},
			[]string{"other report", "deleted report", "flagged report", "unread report", "read report"}},
		{"folder", SearchFilter{AccountID: a, FolderIDs: []string{trash.ID}, Match: match("report")}, []string{"deleted report"}},
		{"trash left out", SearchFilter{AccountID: a, ExcludeRoles: []api.FolderRole{api.RoleTrash, api.RoleJunk}, Match: match("report")},
			[]string{"flagged report", "unread report", "read report"}},
		{"unread", SearchFilter{AccountID: a, Unread: true}, []string{"unread report"}},
		{"flagged", SearchFilter{AccountID: a, Flagged: true}, []string{"flagged report"}},
		{"attachments", SearchFilter{AccountID: a, Attachments: true}, []string{"read report"}},
		{"after inclusive", SearchFilter{AccountID: a, After: day0.Add(48 * time.Hour)}, []string{"deleted report", "flagged report"}},
		{"before exclusive", SearchFilter{AccountID: a, Before: day0.Add(24 * time.Hour)}, []string{"read report"}},
		{"no match", SearchFilter{Match: match("invoice")}, []string{}},
	}
	for _, tc := range tests {
		if got := searchSubjects(t, s, tc.f); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}

	if err := s.SetAccountEnabled(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if got := searchSubjects(t, s, SearchFilter{Match: match("other")}); len(got) != 0 {
		t.Errorf("a paused account was searched: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: b, Match: match("other")}); len(got) != 1 {
		t.Errorf("naming a paused account should still search it: %v", got)
	}
}

func TestSearchPaging(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	for i := 1; i <= 7; i++ {
		date := day0.Add(time.Duration(i) * time.Hour)
		if i >= 5 {
			date = day0.Add(10 * time.Hour) // equal dates: the id decides
		}
		seedMessage(t, s, inbox, uint32(i), fmt.Sprintf("note %d", i), date)
	}
	f := SearchFilter{AccountID: acc, Match: match("note")}
	var got []string
	cursor := ""
	for page := 0; ; page++ {
		rows, next, total, err := s.SearchMessages(ctx, f, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		if total != 7 {
			t.Fatalf("total %d", total)
		}
		for _, r := range rows {
			got = append(got, r.ID)
		}
		if next == "" {
			break
		}
		if page > 5 {
			t.Fatal("paging does not end")
		}
		cursor = next
	}
	all, _, _, err := s.SearchMessages(ctx, f, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(all))
	for _, r := range all {
		want = append(want, r.ID)
	}
	if !slices.Equal(got, want) || len(got) != 7 {
		t.Fatalf("paged %v, whole %v", got, want)
	}

	_, listCursor, _, err := s.ListMessages(ctx, acc, inbox.ID, "", 2, api.SortDateDesc, api.FilterAll)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{listCursor, "garbage"} {
		if _, _, _, err := s.SearchMessages(ctx, f, c, 3); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: %v, want ErrBadCursor", c, err)
		}
	}
}

func TestSearchRejectsBadExpressionQuietly(t *testing.T) {
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	seedMessage(t, s, seedFolder(t, s, acc, "INBOX", api.RoleInbox), 1, "secret", day0)
	for _, expr := range []string{`secret AND (`, `"secret words`} {
		_, _, _, err := s.SearchMessages(context.Background(), SearchFilter{Match: expr}, "", 10)
		if !errors.Is(err, ErrSearchRejected) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: err = %v", expr, err)
		}
	}
}

func FuzzSearchMatch(f *testing.F) {
	s := openTestStore(f)
	acc := seedAccountsF(f, s)
	inbox := seedFolderF(f, s, acc)
	m := &Message{AccountID: acc, FolderID: inbox.ID, UID: 1, Subject: "Faktura", Date: day0,
		From: []api.Address{{Name: "Jiří", Address: "jiri@example.invalid"}}}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"faktura", `"a b`, "NEAR(a b)", "from:x* ^y", `x"y`, "a:b:c", "{subject}:x", "-x +y"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, q string) {
		expr := srch.Parse(q).MatchExpr()
		if expr == "" {
			return
		}
		if _, _, _, err := s.SearchMessages(context.Background(), SearchFilter{Match: expr}, "", 5); err != nil {
			t.Fatalf("%q → %s: %v", q, expr, err)
		}
	})
}

// seedAccountsF and seedFolderF are the seeders for a fuzz target's setup.
func seedAccountsF(f *testing.F, s *Store) string {
	a := Account{Name: "a", Enabled: true, Config: testAccountConfig("a@example.invalid")}
	if err := s.AddAccount(context.Background(), &a); err != nil {
		f.Fatal(err)
	}
	return a.ID
}

func seedFolderF(f *testing.F, s *Store, acc string) Folder {
	stored, _, err := s.UpsertFolders(context.Background(), acc, []Folder{{Mailbox: "INBOX", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Subscribed: true}})
	if err != nil {
		f.Fatal(err)
	}
	return stored[0]
}

func TestIndexSearchBatchBackfills(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	for i := 1; i <= 9; i++ {
		m := seedMessage(t, s, inbox, uint32(i), fmt.Sprintf("old %d", i), day0)
		setBody(t, s, m.ID, strings.Repeat("slovo ", 100)+"stary")
	}
	// As if the rows predated migration 0013.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO messages_fts (messages_fts) VALUES ('delete-all')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_docs`); err != nil {
		t.Fatal(err)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc, Match: match("stary")}); len(got) != 0 {
		t.Fatalf("found before the backfill: %v", got)
	}
	// A write meanwhile indexes its own row; the backfill leaves it be.
	var first string
	s.db.QueryRowContext(ctx, `SELECT id FROM messages ORDER BY id LIMIT 1`).Scan(&first)
	setBody(t, s, first, "stary a novy")

	cursor, total, batches := "", 0, 0
	for {
		last, n, err := s.IndexSearchBatch(ctx, cursor, 4, 1000) // 1000 bytes: two bodies a batch
		if err != nil {
			t.Fatal(err)
		}
		if last == "" {
			break
		}
		cursor, total, batches = last, total+n, batches+1
	}
	if total != 8 || batches < 4 {
		t.Errorf("indexed %d in %d batches, want 8 in at least 4", total, batches)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc, Match: match("stary")}); len(got) != 9 {
		t.Errorf("after the backfill: %d results", len(got))
	}
	if last, n, err := s.IndexSearchBatch(ctx, "", 100, 0); err != nil || n != 0 || last == "" {
		t.Errorf("second pass: last %q, %d indexed, %v", last, n, err)
	}
	if n, d := indexed(t, s); n != 9 || d != 9 {
		t.Errorf("index %d/%d, want 9/9", n, d)
	}
}

// BenchmarkSearchPrefix measures the two-letter prefix queries of live
// search over a large store (the question whether messages_fts needs a
// prefix index). Run with -bench SearchPrefix; the setup takes a while.
func BenchmarkSearchPrefix(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/store.db", slog.New(slog.DiscardHandler))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	a := Account{Name: "a", Enabled: true, Config: testAccountConfig("a@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		b.Fatal(err)
	}
	folders, _, err := s.UpsertFolders(ctx, a.ID, []Folder{{Mailbox: "INBOX", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Subscribed: true}})
	if err != nil {
		b.Fatal(err)
	}
	words := strings.Fields("faktura příloha dobrý den posílám smlouva schůzka projekt zpráva odpověď objednávka " +
		"prosím děkuji termín návrh rozpočet report meeting invoice attachment please thanks review draft")
	const n = 50000
	for start := 0; start < n; start += 1000 {
		batch := make([]*Message, 0, 1000)
		for i := start; i < start+1000; i++ {
			batch = append(batch, &Message{AccountID: a.ID, FolderID: folders[0].ID, UID: uint32(i + 1),
				Subject: words[i%len(words)] + " " + words[(i*7)%len(words)], Date: day0.Add(time.Duration(i) * time.Minute)})
		}
		if err := s.UpsertMessages(ctx, batch); err != nil {
			b.Fatal(err)
		}
	}
	var body strings.Builder
	for i := 0; i < 150; i++ {
		body.WriteString(words[(i*13)%len(words)] + fmt.Sprint(i%40) + " ")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET text_body = ? || id`, body.String()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := []string{"pr", "fa", "de", "re", "od"}[i%5]
		if _, _, _, err := s.SearchMessages(ctx, SearchFilter{Match: match(q)}, "", 50); err != nil {
			b.Fatal(err)
		}
	}
}
