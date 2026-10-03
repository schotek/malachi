// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package maildate

import (
	"reflect"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func prague(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	return instant(s).In(loc)
}

func TestBoundaries(t *testing.T) {
	now := prague(t, "2026-10-22T10:00:00Z")
	cases := []struct {
		date string
		want Group
	}{
		{"2026-10-22T00:00:00Z", Group{Kind: Today}},
		{"2026-10-21T10:00:00Z", Group{Kind: Yesterday}},
		{"2026-10-19T10:00:00Z", Group{Kind: ThisWeek}},
		{"2026-10-12T10:00:00Z", Group{Kind: LastWeek}},
		{"2026-10-01T10:00:00Z", Group{Kind: ThisMonth}},
		{"2026-09-01T10:00:00Z", Group{Kind: LastMonth}},
		{"2026-01-01T10:00:00Z", Group{Kind: ThisYear}},
		{"2025-12-01T10:00:00Z", Group{Kind: OlderYear, Year: 2025}},
	}
	for _, tc := range cases {
		if got := Containing(instant(tc.date), now, time.Monday); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.date, got, tc.want)
		}
	}
}

func TestLocalMidnightAndDST(t *testing.T) {
	now := prague(t, "2026-03-30T10:00:00Z")
	cases := map[string]Kind{
		"2026-03-29T22:00:00Z": Today,
		"2026-03-29T21:59:59Z": Yesterday,
		"2026-03-28T23:00:00Z": Yesterday,
		"2026-03-28T22:59:59Z": LastWeek,
	}
	for date, want := range cases {
		if got := Containing(instant(date), now, time.Monday).Kind; got != want {
			t.Errorf("%s: %v want %v", date, got, want)
		}
	}
}

func TestWeekLocaleAndYearBoundary(t *testing.T) {
	now := prague(t, "2026-10-21T10:00:00Z")
	sunday := instant("2026-10-18T10:00:00Z")
	if Containing(sunday, now, time.Monday).Kind != LastWeek || Containing(sunday, now, time.Sunday).Kind != ThisWeek {
		t.Fatal("week start ignored")
	}
	if Containing(instant("2026-12-31T10:00:00Z"), prague(t, "2027-01-04T10:00:00Z"), time.Monday).Kind != LastWeek {
		t.Fatal("week crossing year")
	}
}

func TestFlaggedFirstAndThreadChildrenStayWithParent(t *testing.T) {
	now := prague(t, "2026-10-22T10:00:00Z")
	rows := []Row[string]{
		{Key: "today", Date: now},
		{Key: "parent", Date: instant("2026-10-21T10:00:00Z"), Flagged: true},
		{Key: "child", Date: instant("2025-01-01T10:00:00Z"), Member: true},
		{Key: "old", Date: instant("2024-01-01T10:00:00Z"), Flagged: true},
	}
	want := []Section[string]{{Group: Group{Kind: Flagged}, Keys: []string{"parent", "child", "old"}}, {Group: Group{Kind: Today}, Keys: []string{"today"}}}
	if got := Sections(rows, now, time.Monday); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v want %+v", got, want)
	}
	rows[1].Flagged = false
	got := Sections(rows, now, time.Monday)
	if !reflect.DeepEqual(got[0].Keys, []string{"old"}) || got[2].Group.Kind != Yesterday || !reflect.DeepEqual(got[2].Keys, []string{"parent", "child"}) {
		t.Fatalf("unflagging: %+v", got)
	}
	if len(Sections([]Row[string]{}, now, time.Monday)) != 0 {
		t.Fatal("empty sections")
	}
}

func TestPagingAndMidnight(t *testing.T) {
	now := prague(t, "2026-10-22T10:00:00Z")
	rows := []Row[int]{{Key: 1, Date: now}, {Key: 2, Date: now.Add(-time.Hour)}}
	if got := Sections(rows, now, time.Monday); len(got) != 1 || len(got[0].Keys) != 2 {
		t.Fatalf("duplicate headers: %+v", got)
	}
	if Sections(rows, now.AddDate(0, 0, 1), time.Monday)[0].Group.Kind != Yesterday {
		t.Fatal("midnight did not move rows")
	}
}

func TestLocaleFirstWeekday(t *testing.T) {
	cases := []struct {
		output string
		want   time.Weekday
	}{
		{"week-1stday=19971130\nfirst_weekday=1", time.Sunday},
		{"week-1stday=19971130\nfirst_weekday=2", time.Monday},
		{"week-1stday=19971201\nfirst_weekday=1", time.Monday},
		{"first_weekday=99", time.Monday},
		{"", time.Monday},
	}
	for _, tc := range cases {
		if got := FirstWeekday(tc.output); got != tc.want {
			t.Errorf("%q: %v want %v", tc.output, got, tc.want)
		}
	}
}
