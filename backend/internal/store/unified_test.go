// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// unifiedPair seeds two accounts, each with an inbox and another folder.
func unifiedPair(t *testing.T, s *Store) (a, b, aOther Folder) {
	t.Helper()
	a = seedFolder(t, s, "accA", "INBOX", api.RoleInbox)
	aOther = seedFolder(t, s, "accA", "Other", api.RoleNone)
	b = seedFolder(t, s, "accB", "INBOX", api.RoleInbox)
	return a, b, aOther
}

func TestListMessagesInMergesFolders(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, b, aOther := unifiedPair(t, s)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	// Interleaved dates, two pairs sharing a date across the accounts so
	// that the id tiebreak crosses the accounts.
	for i := 0; i < 6; i++ {
		d := base.Add(time.Duration(i/2) * time.Hour)
		var flags []api.Flag
		if i%3 == 0 {
			flags = []api.Flag{api.FlagSeen}
		}
		if i == 4 {
			flags = append(flags, api.FlagFlagged)
		}
		seedMessage(t, s, a, uint32(i+1), fmt.Sprint("a", i), d, flags...)
		seedMessage(t, s, b, uint32(i+1), fmt.Sprint("b", i), d.Add(time.Duration(i%2)*time.Minute), flags...)
	}
	seedMessage(t, s, aOther, 1, "elsewhere", base.Add(10*time.Hour))
	hidden := seedMessage(t, s, b, 99, "hidden", base.Add(11*time.Hour))
	if _, _, err := s.SetMessageHidden(ctx, hidden.ID, true); err != nil {
		t.Fatal(err)
	}
	refs := []FolderRef{{"accA", a.ID}, {"accB", b.ID}}

	walk := func(sortOrder api.SortOrder, filter api.MessageFilter, limit int) (all []Message, total int) {
		t.Helper()
		cursor := ""
		for {
			items, next, tot, err := s.ListMessagesIn(ctx, refs, cursor, limit, sortOrder, filter)
			if err != nil {
				t.Fatal(err)
			}
			total = tot
			all = append(all, items...)
			if next == "" {
				return all, total
			}
			cursor = next
		}
	}
	desc, total := walk(api.SortDateDesc, "", 5)
	if total != 12 || len(desc) != 12 {
		t.Fatalf("desc: total %d, %d rows", total, len(desc))
	}
	seen := map[string]bool{}
	for i, m := range desc {
		if seen[m.ID] || m.Subject == "hidden" || m.Subject == "elsewhere" {
			t.Fatalf("row %d: %s", i, m.Subject)
		}
		seen[m.ID] = true
		if i > 0 {
			p := desc[i-1]
			if m.Date.After(p.Date) || (m.Date.Equal(p.Date) && m.ID > p.ID) {
				t.Fatalf("desc order broken at %d", i)
			}
		}
	}
	asc, _ := walk(api.SortDateAsc, api.FilterAll, 4)
	for i := range asc {
		if asc[i].ID != desc[len(desc)-1-i].ID {
			t.Fatalf("asc is not the reverse of desc at %d", i)
		}
	}
	if unread, total := walk("", api.FilterUnread, 3); total != 8 || len(unread) != 8 {
		t.Errorf("unread: total %d, %d rows", total, len(unread))
	}
	if flagged, total := walk("", api.FilterFlagged, 1); total != 2 || len(flagged) != 2 {
		t.Errorf("flagged: total %d, %d rows", total, len(flagged))
	}

	// The same rows as each folder's own listing.
	one, _, n, err := s.ListMessages(ctx, "accB", b.ID, "", 50, "", "")
	if err != nil || n != 6 || len(one) != 6 {
		t.Fatalf("single folder: %d %v", n, err)
	}

	// Cursors do not cross between the listings or the orders.
	_, next, _, err := s.ListMessagesIn(ctx, refs, "", 2, api.SortDateDesc, "")
	if err != nil || next == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListMessages(ctx, "accA", a.ID, next, 2, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("unified cursor in a folder: %v", err)
	}
	if _, _, _, err := s.ListMessagesIn(ctx, refs, next, 2, api.SortDateAsc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("unified cursor of the other order: %v", err)
	}
	_, single, _, err := s.ListMessages(ctx, "accA", a.ID, "", 2, api.SortDateDesc, "")
	if err != nil || single == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListMessagesIn(ctx, refs, single, 2, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("folder cursor in unified: %v", err)
	}
	if _, _, _, err := s.ListMessagesIn(ctx, refs, "!!", 2, "", ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("garbage cursor: %v", err)
	}

	// A ref of the wrong account is skipped; nothing in scope is empty.
	items, _, total, err := s.ListMessagesIn(ctx, []FolderRef{{"accA", b.ID}, {"accA", a.ID}, {"accA", a.ID}}, "", 50, "", "")
	if err != nil || total != 6 || len(items) != 6 {
		t.Errorf("foreign ref: %d/%d %v", len(items), total, err)
	}
	items, next, total, err = s.ListMessagesIn(ctx, nil, "", 50, "", "")
	if err != nil || total != 0 || next != "" || items == nil || len(items) != 0 {
		t.Errorf("empty scope: %v %q %d %v", items, next, total, err)
	}
}

func TestListThreadsInGroupsPerFolder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, b, aOther := unifiedPair(t, s)
	// Account A: two threads; account B: one thread with the same
	// Message-IDs as A's first (a thread id is unique within an account
	// only) and one more.
	linked(t, s, a, 1, "r1", "")
	linked(t, s, a, 2, "r1a", "r1")
	linked(t, s, a, 3, "r2", "")
	linked(t, s, b, 4, "r1", "")
	bLate := linked(t, s, b, 6, "r1b", "r1")
	linked(t, s, b, 5, "r3", "")
	linked(t, s, aOther, 7, "r9", "")
	refs := []FolderRef{{"accA", a.ID}, {"accB", b.ID}}

	walk := func(sortOrder api.SortOrder, filter api.MessageFilter, limit int) (all []ThreadRow, total int) {
		t.Helper()
		cursor := ""
		for {
			items, next, tot, err := s.ListThreadsIn(ctx, refs, cursor, limit, sortOrder, filter)
			if err != nil {
				t.Fatal(err)
			}
			total = tot
			all = append(all, items...)
			if next == "" {
				return all, total
			}
			cursor = next
		}
	}
	desc, total := walk(api.SortDateDesc, "", 1)
	if total != 4 || len(desc) != 4 {
		t.Fatalf("desc: total %d, %d rows", total, len(desc))
	}
	if desc[0].AccountID != "accB" || desc[0].Latest.ID != bLate.ID || desc[0].MessageCount != 2 {
		t.Errorf("newest row = %+v", desc[0])
	}
	for i, r := range desc {
		if len(r.FolderIDs) == 0 || (r.AccountID == "accA") != (r.Latest.FolderID == a.ID) {
			t.Errorf("row %d: account %s, folder %s", i, r.AccountID, r.Latest.FolderID)
		}
		if i > 0 && r.Latest.Date.After(desc[i-1].Latest.Date) {
			t.Errorf("desc order broken at %d", i)
		}
	}
	asc, _ := walk(api.SortDateAsc, "", 3)
	for i := range asc {
		if asc[i].ID != desc[3-i].ID || asc[i].AccountID != desc[3-i].AccountID {
			t.Fatalf("asc is not the reverse of desc at %d", i)
		}
	}
	// Each row is what the folder's own listing has.
	own, _, _, err := s.ListThreads(ctx, "accA", a.ID, "", 50, "", "")
	if err != nil || len(own) != 2 {
		t.Fatalf("folder listing: %v", err)
	}
	for _, o := range own {
		found := false
		for _, r := range desc {
			if r.AccountID == "accA" && r.ID == o.ID {
				found = r.MessageCount == o.MessageCount && r.Latest.ID == o.Latest.ID
			}
		}
		if !found {
			t.Errorf("thread %s differs from the folder's listing", o.ID)
		}
	}
	if unread, total := walk("", api.FilterUnread, 50); total != 4 || len(unread) != 4 {
		t.Errorf("unread: %d/%d", len(unread), total)
	}
	if flagged, total := walk("", api.FilterFlagged, 50); total != 0 || len(flagged) != 0 {
		t.Errorf("flagged: %d/%d", len(flagged), total)
	}

	_, next, _, err := s.ListThreadsIn(ctx, refs, "", 1, api.SortDateDesc, "")
	if err != nil || next == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListThreads(ctx, "accA", a.ID, next, 1, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("unified cursor in a folder: %v", err)
	}
	if _, _, _, err := s.ListMessagesIn(ctx, refs, next, 1, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("thread cursor in unified messages: %v", err)
	}
	_, single, _, err := s.ListThreads(ctx, "accA", a.ID, "", 1, api.SortDateDesc, "")
	if err != nil || single == "" {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListThreadsIn(ctx, refs, single, 1, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("folder cursor in unified: %v", err)
	}
	bad := encodeSortCursor("v", desc[0].Latest.Date.UTC().Format(timeLayout), "no-separator")
	if _, _, _, err := s.ListThreadsIn(ctx, refs, bad, 1, api.SortDateDesc, ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("cursor without a folder: %v", err)
	}
	items, next, total, err := s.ListThreadsIn(ctx, nil, "", 50, "", "")
	if err != nil || total != 0 || next != "" || items == nil {
		t.Errorf("empty scope: %v %q %d %v", items, next, total, err)
	}
}
