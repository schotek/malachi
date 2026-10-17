// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSearchEvents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	start := threadBase
	put := func(remote string, evs ...Event) []Event {
		t.Helper()
		st, err := s.ReplaceResource(ctx, cal.ID, remote, evs, nil)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	plan := put("/plan", Event{Kind: EventSingle, Title: "Q4 planning", Location: "Zasedačka B", StartAt: start, EndAt: start.Add(time.Hour),
		Attendees: []api.Attendee{{Name: "Jana Nováková", Address: "jana@example.invalid"}}})
	series := put("/series",
		Event{Kind: EventMaster, Title: "Weekly sync", StartAt: start, EndAt: start.Add(time.Hour), Organizer: &api.Address{Name: "Petr Dvořák", Address: "petr@example.invalid"}},
		Event{Kind: EventOverride, RecurrenceID: "x", Title: "Weekly sync", Notes: "Agenda: dodavatel"})

	for q, want := range map[string]string{
		"novak":      plan[0].ID, // diacritics and case folded, prefix
		"NOVÁKOVÁ":   plan[0].ID,
		"zased":      plan[0].ID,
		"dvorak":     series[0].ID,
		"dodavatel":  series[0].ID, // a match in an override counts for its master
		"planning q": plan[0].ID,
		"jana@exa":   plan[0].ID,
	} {
		ids, total, err := s.SearchEvents(ctx, EventSearch{Query: q})
		if err != nil || total != 1 || len(ids) != 1 || ids[0] != want {
			t.Errorf("%q: %v %d %v", q, ids, total, err)
		}
	}
	if ids, total, err := s.SearchEvents(ctx, EventSearch{Query: "weekly OR \"x"}); err != nil || total != 0 {
		t.Errorf("operators are words: %v %d %v", ids, total, err)
	}
	if ids, _, err := s.SearchEvents(ctx, EventSearch{Query: " ,.; "}); err != nil || ids != nil {
		t.Errorf("no word: %v %v", ids, err)
	}
	if ids, _, _ := s.SearchEvents(ctx, EventSearch{Query: "novak", AccountIDs: []string{"acc_other"}}); len(ids) != 0 {
		t.Errorf("other account: %v", ids)
	}
	if ids, _, _ := s.SearchEvents(ctx, EventSearch{Query: "novak", ExcludeAccountIDs: []string{a.ID}}); len(ids) != 0 {
		t.Errorf("excluded account: %v", ids)
	}

	// An update re-indexes the changed text.
	plan[0].Title, plan[0].Attendees = "Budget", nil
	if _, err := s.ReplaceResource(ctx, cal.ID, "/plan", []Event{plan[0]}, nil); err != nil {
		t.Fatal(err)
	}
	if ids, _, _ := s.SearchEvents(ctx, EventSearch{Query: "novak"}); len(ids) != 0 {
		t.Errorf("old text after update: %v", ids)
	}
	if ids, _, _ := s.SearchEvents(ctx, EventSearch{Query: "budg"}); len(ids) != 1 {
		t.Errorf("new text after update: %v", ids)
	}
}

// contentless_delete (SQLite ≥ 3.43, in the bundled modernc.org/sqlite):
// deleting an event, also through its calendar's cascade, drops its index
// entry, so neither a match nor a stale rowid is left.
func TestSearchEventsContentlessDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, cal := calAccount(t, s, "me@example.invalid")
	st, err := s.ReplaceResource(ctx, cal.ID, "/a", []Event{{Kind: EventSingle, Title: "Dentist"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceResource(ctx, cal.ID, "/b", []Event{{Kind: EventSingle, Title: "Dentist again"}}, nil); err != nil {
		t.Fatal(err)
	}
	var docid int64
	if err := s.db.QueryRow(`SELECT docid FROM cal_events_fts_docs WHERE event_id = ?`, st[0].ID).Scan(&docid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveResource(ctx, cal.ID, "/a"); err != nil {
		t.Fatal(err)
	}
	if n := calRows(t, s, "cal_events_fts", "cal_events_fts MATCH 'dentist' AND rowid = ?", docid); n != 0 {
		t.Fatalf("deleted event still indexed (%d)", n)
	}
	if n := calRows(t, s, "cal_events_fts", "cal_events_fts MATCH 'dentist'"); n != 1 {
		t.Fatalf("index after delete: %d", n)
	}
	if err := s.DeleteCalendar(ctx, cal.ID); err != nil {
		t.Fatal(err)
	}
	if n := calRows(t, s, "cal_events_fts", "cal_events_fts MATCH 'dentist'"); n != 0 {
		t.Fatalf("index after the calendar's cascade: %d", n)
	}
	if n := calRows(t, s, "cal_events_fts_docs", ""); n != 0 {
		t.Fatalf("docs after the cascade: %d", n)
	}
}
