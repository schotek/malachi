// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// rangeFixture stores, in one calendar: a timed event 10:00–11:00 UTC on
// 2026-10-14, an all-day event on 2026-10-15, a floating event at 09:00
// wall clock on 2026-10-16, a declined one and one waiting for an answer.
func rangeFixture(t *testing.T) (*Store, Account, Calendar, map[string]string) {
	t.Helper()
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	ids := map[string]string{}
	day := func(d int, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	put := func(name string, e Event, o Occurrence) {
		t.Helper()
		e.Title = name
		st, err := s.ReplaceResource(ctx, cal.ID, "/"+name, []Event{e}, func(st []Event) []Occurrence {
			o.EventID, o.CalendarID, o.AccountID = st[0].ID, cal.ID, a.ID
			return []Occurrence{o}
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = st[0].ID
	}
	put("timed", Event{Kind: EventSingle, StartAt: day(14, 10), EndAt: day(14, 11)}, Occurrence{StartAt: day(14, 10), EndAt: day(14, 11)})
	put("allday", Event{Kind: EventSingle, AllDay: true, StartDate: "2026-10-15", EndDate: "2026-10-16"},
		Occurrence{AllDay: true, StartAt: day(15, 0), EndAt: day(16, 0)})
	put("floating", Event{Kind: EventSingle, Floating: true, StartAt: day(16, 9), EndAt: day(16, 10)},
		Occurrence{Floating: true, StartAt: day(16, 9), EndAt: day(16, 10)})
	put("declined", Event{Kind: EventSingle, MyResponse: api.ResponseDeclined, StartAt: day(14, 12), EndAt: day(14, 13)},
		Occurrence{StartAt: day(14, 12), EndAt: day(14, 13)})
	put("unanswered", Event{Kind: EventSingle, MyResponse: api.ResponseNeedsAction, StartAt: day(14, 14), EndAt: day(14, 15)},
		Occurrence{StartAt: day(14, 14), EndAt: day(14, 15)})
	return s, a, cal, ids
}

func titles(l OccurrenceList) []string {
	var out []string
	for _, o := range l.Occurrences {
		out = append(out, l.Events[o.EventID].Title)
	}
	sort.Strings(out)
	return out
}

func TestEventRange(t *testing.T) {
	ctx := context.Background()
	s, a, cal, _ := rangeFixture(t)
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	cases := []struct {
		name string
		q    OccurrenceQuery
		want string
	}{
		// 2026-10-14 in Prague (UTC+2): the timed event, not the all-day one of the 15th.
		{"prague 14th", OccurrenceQuery{From: time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC), Zone: prague},
			"[timed unanswered]"},
		// Without a zone the all-day event of the 15th is a candidate of the 14th (±14 h).
		{"widened 14th", OccurrenceQuery{From: time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC)},
			"[allday timed unanswered]"},
		// The 15th in Prague: only the all-day event.
		{"prague 15th", OccurrenceQuery{From: time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC), Zone: prague},
			"[allday]"},
		// The floating 09:00 shows in Tokyo's morning of the 16th although
		// that is 00:00 UTC.
		{"tokyo morning", OccurrenceQuery{From: time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC), To: time.Date(2026, 10, 16, 1, 30, 0, 0, time.UTC), Zone: tokyo},
			"[floating]"},
		{"declined shown", OccurrenceQuery{From: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), Zone: time.UTC, IncludeDeclined: true},
			"[declined timed unanswered]"},
		{"needsAction hidden", OccurrenceQuery{From: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), Zone: time.UTC,
			HideNeedsActionAccountIDs: []string{a.ID}}, "[timed]"},
		{"paused", OccurrenceQuery{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), ExcludeAccountIDs: []string{a.ID}}, "[]"},
		{"other calendar", OccurrenceQuery{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), CalendarIDs: []string{"cal_x"}}, "[]"},
		// [from, to) is half open: an event ending at From is out.
		{"half open", OccurrenceQuery{From: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), Zone: time.UTC}, "[]"},
	}
	for _, c := range cases {
		got, err := s.EventRange(ctx, c.q)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if g := fmtList(titles(got)); g != c.want {
			t.Errorf("%s: %s, want %s", c.name, g, c.want)
		}
	}
	// A hidden calendar is out unless asked for.
	if _, err := s.UpdateCalendarUser(ctx, cal.ID, ptr(false), nil); err != nil {
		t.Fatal(err)
	}
	q := OccurrenceQuery{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	if got, _ := s.EventRange(ctx, q); len(got.Occurrences) != 0 {
		t.Fatalf("hidden: %v", titles(got))
	}
	q.IncludeHidden, q.Limit = true, 2
	got, err := s.EventRange(ctx, q)
	if err != nil || len(got.Occurrences) != 2 || !got.Truncated || got.Events[got.Occurrences[0].EventID].Title != "timed" {
		t.Fatalf("limit: %v %v %v", titles(got), got.Truncated, err)
	}
}

func fmtList(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}

func TestOccurrenceHelpers(t *testing.T) {
	ctx := context.Background()
	s, a, cal, ids := rangeFixture(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	// A series with instances on the 10th, 20th and 30th, the 20th overridden.
	master := Event{Kind: EventMaster, UID: "s", Title: "Series", StartAt: time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), EndAt: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	k := func(d int) string { return FormatCalendarTime(time.Date(2026, 10, d, 8, 0, 0, 0, time.UTC)) }
	override := Event{Kind: EventOverride, UID: "s", RecurrenceID: k(20), Title: "Series (moved)"}
	st, err := s.ReplaceResource(ctx, cal.ID, "/series", []Event{master, override}, func(st []Event) []Occurrence {
		var out []Occurrence
		for _, d := range []int{10, 20, 30} {
			ev := st[0].ID
			if d == 20 {
				ev = st[1].ID
			}
			t0 := time.Date(2026, 10, d, 8, 0, 0, 0, time.UTC)
			out = append(out, Occurrence{EventID: ev, OccurrenceKey: k(d), SeriesID: st[0].ID, CalendarID: cal.ID, AccountID: a.ID, StartAt: t0, EndAt: t0.Add(time.Hour)})
		}
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	reps, err := s.RepresentativeOccurrences(ctx, []string{st[0].ID, ids["timed"]}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r := reps[st[0].ID]; r.OccurrenceKey != k(20) || r.EventID != st[1].ID {
		t.Errorf("series next: %+v", r)
	}
	if r := reps[ids["timed"]]; r.EventID != ids["timed"] {
		t.Errorf("past single: %+v", r)
	}
	if o, err := s.SeriesOccurrence(ctx, st[0].ID, k(20)); err != nil || o.EventID != st[1].ID {
		t.Errorf("series occurrence: %+v %v", o, err)
	}
	if _, err := s.GetOccurrence(ctx, st[0].ID, k(11)); err != ErrNotFound {
		t.Errorf("missing occurrence: %v", err)
	}
	n, err := s.DeleteOccurrencesOutside(ctx, a.ID, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 5 { // timed, declined, unanswered (14th), series 10th and 30th
		t.Fatalf("outside: %d %v", n, err)
	}
}
