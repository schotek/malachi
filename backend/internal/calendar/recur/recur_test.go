// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package recur

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // as in cmd/malachid (D4)
)

func zone(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func mustParse(t testing.TB, s string) Rule {
	t.Helper()
	r, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return r
}

var everything = Window{
	From: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2201, 1, 1, 0, 0, 0, 0, time.UTC),
}

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04Z", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestExpandTable(t *testing.T) {
	prague := zone(t, "Europe/Prague")
	ny := zone(t, "America/New_York")
	tests := []struct {
		name    string
		rule    string
		dtstart time.Time
		loc     *time.Location
		win     Window
		want    []string // instants in UTC
	}{
		{"prague spring gap moves forward", "FREQ=DAILY;COUNT=4",
			time.Date(2026, 3, 27, 2, 30, 0, 0, prague), prague, everything,
			[]string{"2026-03-27T01:30Z", "2026-03-28T01:30Z", "2026-03-29T01:30Z", "2026-03-30T00:30Z"}},
		{"prague autumn repeat takes the first", "FREQ=DAILY;COUNT=3",
			time.Date(2026, 10, 24, 2, 30, 0, 0, prague), prague, everything,
			[]string{"2026-10-24T00:30Z", "2026-10-25T00:30Z", "2026-10-26T01:30Z"}},
		{"prague 09:00 across both changes", "FREQ=WEEKLY;BYDAY=SU;UNTIL=20261101T080000Z",
			time.Date(2026, 3, 22, 9, 0, 0, 0, prague), prague,
			Window{utc("2026-03-01T00:00Z"), utc("2026-04-01T00:00Z")},
			[]string{"2026-03-22T08:00Z", "2026-03-29T07:00Z"}},
		{"new york spring gap", "FREQ=WEEKLY;BYDAY=SU;COUNT=3",
			time.Date(2026, 3, 1, 2, 30, 0, 0, ny), ny, everything,
			[]string{"2026-03-01T07:30Z", "2026-03-08T07:30Z", "2026-03-15T06:30Z"}},
		{"new york autumn repeat", "FREQ=DAILY;COUNT=2",
			time.Date(2026, 10, 31, 1, 30, 0, 0, ny), ny, everything,
			[]string{"2026-10-31T05:30Z", "2026-11-01T05:30Z"}},
		{"leap day yearly skips non-leap years", "FREQ=YEARLY;COUNT=3",
			time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2024-02-29T10:00Z", "2028-02-29T10:00Z", "2032-02-29T10:00Z"}},
		{"last day of the month", "FREQ=MONTHLY;BYMONTHDAY=-1;COUNT=3",
			time.Date(2028, 1, 31, 10, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2028-01-31T10:00Z", "2028-02-29T10:00Z", "2028-03-31T10:00Z"}},
		{"BYMONTHDAY=31 skips short months", "FREQ=MONTHLY;BYMONTHDAY=31;COUNT=3",
			time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2026-03-31T10:00Z", "2026-05-31T10:00Z", "2026-07-31T10:00Z"}},
		{"last friday", "FREQ=MONTHLY;BYDAY=-1FR;COUNT=4",
			time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2026-01-30T09:00Z", "2026-02-27T09:00Z", "2026-03-27T09:00Z", "2026-04-24T09:00Z"}},
		{"last workday via BYSETPOS", "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1;COUNT=3",
			time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2026-01-30T09:00Z", "2026-02-27T09:00Z", "2026-03-31T09:00Z"}},
		{"second tuesday of november yearly", "FREQ=YEARLY;BYMONTH=11;BYDAY=2TU;COUNT=2",
			time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC, everything,
			[]string{"2026-11-10T09:00Z", "2027-11-09T09:00Z"}},
		// RFC 5545 §3.8.5.3: WKST changes the weeks of INTERVAL=2.
		{"WKST=MO", "FREQ=WEEKLY;INTERVAL=2;COUNT=4;BYDAY=TU,SU;WKST=MO",
			time.Date(1997, 8, 5, 9, 0, 0, 0, ny), ny, everything,
			[]string{"1997-08-05T13:00Z", "1997-08-10T13:00Z", "1997-08-19T13:00Z", "1997-08-24T13:00Z"}},
		{"WKST=SU", "FREQ=WEEKLY;INTERVAL=2;COUNT=4;BYDAY=TU,SU;WKST=SU",
			time.Date(1997, 8, 5, 9, 0, 0, 0, ny), ny, everything,
			[]string{"1997-08-05T13:00Z", "1997-08-17T13:00Z", "1997-08-19T13:00Z", "1997-08-31T13:00Z"}},
		{"UNTIL inclusive", "FREQ=DAILY;UNTIL=20260108T090000Z",
			utc("2026-01-05T09:00Z"), time.UTC, everything,
			[]string{"2026-01-05T09:00Z", "2026-01-06T09:00Z", "2026-01-07T09:00Z", "2026-01-08T09:00Z"}},
		{"UNTIL a second before", "FREQ=DAILY;UNTIL=20260108T085959Z",
			utc("2026-01-05T09:00Z"), time.UTC, everything,
			[]string{"2026-01-05T09:00Z", "2026-01-06T09:00Z", "2026-01-07T09:00Z"}},
		{"UNTIL as a DATE includes the day", "FREQ=DAILY;UNTIL=20260107",
			time.Date(2026, 1, 5, 23, 0, 0, 0, prague), prague, everything,
			[]string{"2026-01-05T22:00Z", "2026-01-06T22:00Z", "2026-01-07T22:00Z"}},
		{"floating UNTIL in the zone", "FREQ=DAILY;UNTIL=20260107T090000",
			time.Date(2026, 1, 5, 9, 0, 0, 0, prague), prague, everything,
			[]string{"2026-01-05T08:00Z", "2026-01-06T08:00Z", "2026-01-07T08:00Z"}},
		{"COUNT counts before the window", "FREQ=DAILY;COUNT=5",
			utc("2026-01-05T09:00Z"), time.UTC,
			Window{utc("2026-01-08T00:00Z"), utc("2026-02-01T00:00Z")},
			[]string{"2026-01-08T09:00Z", "2026-01-09T09:00Z"}},
		{"window is half open", "FREQ=DAILY",
			utc("2026-01-05T09:00Z"), time.UTC,
			Window{utc("2026-01-06T09:00Z"), utc("2026-01-08T09:00Z")},
			[]string{"2026-01-06T09:00Z", "2026-01-07T09:00Z"}},
		{"fast forward from 1900", "FREQ=DAILY",
			time.Date(1900, 1, 1, 9, 0, 0, 0, prague), prague,
			Window{utc("2026-10-01T00:00Z"), utc("2026-10-04T00:00Z")},
			[]string{"2026-10-01T07:00Z", "2026-10-02T07:00Z", "2026-10-03T07:00Z"}},
		{"fast forward yearly leap day", "FREQ=YEARLY",
			time.Date(1904, 2, 29, 9, 0, 0, 0, time.UTC), time.UTC,
			Window{utc("2025-01-01T00:00Z"), utc("2033-01-01T00:00Z")},
			[]string{"2028-02-29T09:00Z", "2032-02-29T09:00Z"}},
		{"fast forward monthly 31st", "FREQ=MONTHLY;INTERVAL=2",
			time.Date(1900, 1, 31, 9, 0, 0, 0, time.UTC), time.UTC,
			Window{utc("2026-01-01T00:00Z"), utc("2026-12-01T00:00Z")},
			[]string{"2026-01-31T09:00Z", "2026-03-31T09:00Z", "2026-05-31T09:00Z", "2026-07-31T09:00Z"}},
		{"hourly with BYHOUR", "FREQ=HOURLY;INTERVAL=3;BYHOUR=9,12;COUNT=4",
			utc("2026-01-05T09:15Z"), time.UTC, everything,
			[]string{"2026-01-05T09:15Z", "2026-01-05T12:15Z", "2026-01-06T09:15Z", "2026-01-06T12:15Z"}},
		{"BYMINUTE=0 with a DTSTART at :30", "FREQ=DAILY;BYMINUTE=0,30;COUNT=3",
			utc("2026-01-05T09:30Z"), time.UTC, everything,
			[]string{"2026-01-05T09:30Z", "2026-01-06T09:00Z", "2026-01-06T09:30Z"}},
		{"impossible by interval ends empty", "FREQ=DAILY;INTERVAL=7;BYDAY=TU",
			time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC), time.UTC, everything, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated, err := Expand(mustParse(t, tc.rule), tc.dtstart, tc.loc, tc.win, 0)
			if err != nil {
				t.Fatal(err)
			}
			if truncated {
				t.Error("truncated")
			}
			var s []string
			for _, g := range got {
				if g.Location() != tc.loc {
					t.Errorf("instance %v not in %v", g, tc.loc)
				}
				s = append(s, g.UTC().Format("2006-01-02T15:04Z"))
			}
			if !reflect.DeepEqual(s, tc.want) {
				t.Errorf("got  %v\nwant %v", s, tc.want)
			}
		})
	}
}

// TestFastForwardMatchesFullExpansion compares a window far from DTSTART
// with the same rule expanded from DTSTART and cut to the window.
func TestFastForwardMatchesFullExpansion(t *testing.T) {
	prague := zone(t, "Europe/Prague")
	rules := []string{
		"FREQ=HOURLY;INTERVAL=7",
		"FREQ=HOURLY;INTERVAL=5;BYHOUR=1,6,11,16,21",
		"FREQ=DAILY;INTERVAL=3;BYHOUR=8,20",
		"FREQ=WEEKLY;INTERVAL=3;BYDAY=MO,TH,SU;WKST=SU",
		"FREQ=WEEKLY;INTERVAL=2",
		"FREQ=MONTHLY;INTERVAL=5;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=1,-1",
		"FREQ=MONTHLY;INTERVAL=7",
		"FREQ=YEARLY;BYWEEKNO=1,53;BYDAY=MO",
		"FREQ=YEARLY;BYYEARDAY=-1,60",
		"FREQ=YEARLY;INTERVAL=2",
	}
	dt := time.Date(2019, 1, 31, 10, 30, 0, 0, prague)
	win := Window{utc("2026-03-01T00:00Z"), utc("2028-01-15T00:00Z")}
	for _, s := range rules {
		r := mustParse(t, s)
		got, _, err := Expand(r, dt, prague, win, 5000)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		full, _, err := Expand(r, dt, prague, Window{dt, win.To}, 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		var want []time.Time
		for _, f := range full {
			if !f.Before(win.From) {
				want = append(want, f)
			}
		}
		if !reflect.DeepEqual(got, want) || len(got) == 0 {
			t.Errorf("%s: fast-forwarded %d instances, full %d\n%v\n%v", s, len(got), len(want), head(got), head(want))
		}
	}
}

func head(l []time.Time) []time.Time {
	if len(l) > 5 {
		return l[:5]
	}
	return l
}

func TestExpandLimits(t *testing.T) {
	dt := utc("2026-01-05T09:00Z")
	got, truncated, err := Expand(mustParse(t, "FREQ=DAILY"), dt, nil, Window{dt, dt.AddDate(10, 0, 0)}, 100)
	if err != nil || len(got) != 100 || !truncated {
		t.Errorf("limit: %d %v %v", len(got), truncated, err)
	}
	got, truncated, err = Expand(mustParse(t, "FREQ=DAILY;COUNT=10"), dt, nil, Window{dt, dt.AddDate(1, 0, 0)}, 10)
	if err != nil || len(got) != 10 || truncated {
		t.Errorf("exactly the limit: %d %v %v", len(got), truncated, err)
	}
	got, truncated, err = Expand(mustParse(t, "FREQ=HOURLY"), dt, nil, Window{dt, dt.AddDate(5, 0, 0)}, 1<<20)
	if err != nil || !truncated || len(got) > MaxHourlyCandidates {
		t.Errorf("hourly candidates: %d %v %v", len(got), truncated, err)
	}
	if got, _, err := Expand(mustParse(t, "FREQ=DAILY"), dt, nil, Window{dt, dt}, 0); err != nil || len(got) != 0 {
		t.Errorf("empty window: %v %v", got, err)
	}
	for _, tc := range []struct {
		rule string
		dt   time.Time
	}{
		{"FREQ=DAILY", utc("1899-12-31T23:00Z")},
		{"FREQ=DAILY", utc("2200-01-02T00:00Z")},
		{"FREQ=DAILY;BYMINUTE=15", dt},
		{"FREQ=DAILY;BYSECOND=30", dt},
		{"FREQ=HOURLY;INTERVAL=24;BYHOUR=4", utc("2026-01-05T03:00Z")},
		{"FREQ=HOURLY;INTERVAL=6;BYHOUR=1,2,4,5", utc("2026-01-05T03:00Z")},
	} {
		if _, _, err := Expand(mustParse(t, tc.rule), tc.dt, nil, everything, 0); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s from %v: %v, want ErrUnsupported", tc.rule, tc.dt, err)
		}
	}
	if err := CheckStart(mustParse(t, "FREQ=HOURLY;INTERVAL=6;BYHOUR=9"), utc("2026-01-05T03:00Z"), nil); err != nil {
		t.Errorf("reachable BYHOUR refused: %v", err)
	}
}

func TestParse(t *testing.T) {
	ok := map[string]string{
		"FREQ=WEEKLY;BYDAY=MO":                              "FREQ=WEEKLY;BYDAY=MO",
		"RRULE:freq=weekly;byday=fr,mo,mo;wkst=mo":          "FREQ=WEEKLY;BYDAY=MO,FR",
		"FREQ=MONTHLY;BYDAY=-1FR;INTERVAL=1":                "FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=YEARLY;BYMONTH=11,2;BYDAY=+2TU;COUNT=10":      "FREQ=YEARLY;COUNT=10;BYDAY=2TU;BYMONTH=2,11",
		"FREQ=DAILY;UNTIL=20261231T225959Z;INTERVAL=2;":     "FREQ=DAILY;INTERVAL=2;UNTIL=20261231T225959Z",
		"FREQ=DAILY;UNTIL=20261231":                         "FREQ=DAILY;UNTIL=20261231",
		"FREQ=DAILY;UNTIL=20261231T120000":                  "FREQ=DAILY;UNTIL=20261231T120000",
		"FREQ=WEEKLY;BYDAY=SU,SA;WKST=SU":                   "FREQ=WEEKLY;BYDAY=SA,SU;WKST=SU",
		"FREQ=YEARLY;BYWEEKNO=-1,20;BYDAY=MO":               "FREQ=YEARLY;BYDAY=MO;BYWEEKNO=-1,20",
		"FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=-1,1":            "FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=-1,1",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29":               "FREQ=YEARLY;BYMONTHDAY=29;BYMONTH=2",
		"FREQ=HOURLY;INTERVAL=1000;COUNT=5000":              "FREQ=HOURLY;INTERVAL=1000;COUNT=5000",
		"FREQ=YEARLY;BYYEARDAY=366,-366":                    "FREQ=YEARLY;BYYEARDAY=-366,366",
		"FREQ=DAILY;BYHOUR=23,0;BYMINUTE=0;BYSECOND=0":      "FREQ=DAILY;BYSECOND=0;BYMINUTE=0;BYHOUR=0,23",
		"FREQ=DAILY;UNTIL=19000101":                         "FREQ=DAILY;UNTIL=19000101",
		"FREQ=DAILY;UNTIL=22001231T235959Z":                 "FREQ=DAILY;UNTIL=22001231T235959Z",
		"FREQ=YEARLY;BYDAY=20MO":                            "FREQ=YEARLY;BYDAY=20MO",
		"FREQ=MONTHLY;BYMONTHDAY=31":                        "FREQ=MONTHLY;BYMONTHDAY=31",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYSETPOS=7": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYSETPOS=7",
	}
	for in, want := range ok {
		r, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got := Normalise(r); got != want {
			t.Errorf("Normalise(Parse(%q)) = %q, want %q", in, got, want)
		}
		again, err := Parse(Normalise(r))
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Errorf("round trip of %q: %+v %v", in, again, err)
		}
	}
	bad := []string{
		"", "FREQ", "FREQ=", "INTERVAL=2", "FREQ=SECONDLY", "FREQ=MINUTELY", "FREQ=FORTNIGHTLY",
		"FREQ=DAILY;INTERVAL=0", "FREQ=DAILY;INTERVAL=1001", "FREQ=DAILY;INTERVAL=-1",
		"FREQ=DAILY;INTERVAL= 2", "FREQ=DAILY;INTERVAL=0x10", "FREQ=DAILY;INTERVAL=99999999999",
		"FREQ=DAILY;COUNT=0", "FREQ=DAILY;COUNT=5001", "FREQ=DAILY;COUNT=10;UNTIL=20261231",
		"FREQ=DAILY;UNTIL=18991231", "FREQ=DAILY;UNTIL=22010101", "FREQ=DAILY;UNTIL=99991231T235959Z",
		"FREQ=DAILY;UNTIL=2026-12-31", "FREQ=DAILY;UNTIL=20261332", "FREQ=DAILY;UNTIL=20261231T250000Z",
		"FREQ=DAILY;FREQ=DAILY", "FREQ=DAILY;freq=weekly", "FREQ=WEEKLY;BYDAY=MO;BYDAY=TU",
		"FREQ=DAILY;X-FOO=1", "FREQ=DAILY;RSCALE=GREGORIAN", "FREQ=DAILY;BYEASTER=0",
		"FREQ=MONTHLY;BYMONTHDAY=0", "FREQ=MONTHLY;BYMONTHDAY=32", "FREQ=MONTHLY;BYMONTHDAY=-32",
		"FREQ=YEARLY;BYYEARDAY=367", "FREQ=YEARLY;BYWEEKNO=54", "FREQ=YEARLY;BYWEEKNO=0",
		"FREQ=DAILY;BYHOUR=24", "FREQ=DAILY;BYMINUTE=60", "FREQ=DAILY;BYSECOND=60", "FREQ=DAILY;BYSECOND=-1",
		"FREQ=YEARLY;BYMONTH=13", "FREQ=YEARLY;BYMONTH=0", "FREQ=YEARLY;BYMONTH=-1",
		"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=0", "FREQ=MONTHLY;BYDAY=MO;BYSETPOS=367",
		"FREQ=WEEKLY;BYDAY=XX", "FREQ=WEEKLY;BYDAY=0MO", "FREQ=YEARLY;BYDAY=54MO", "FREQ=WEEKLY;BYDAY=,",
		"FREQ=WEEKLY;WKST=XX", "FREQ=WEEKLY;BYDAY=1MO", "FREQ=DAILY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYWEEKNO=1", "FREQ=WEEKLY;BYMONTHDAY=1", "FREQ=DAILY;BYYEARDAY=1",
		"FREQ=YEARLY;BYWEEKNO=1;BYDAY=1MO", "FREQ=DAILY;BYSETPOS=1",
		"FREQ=DAILY;BYHOUR=" + strings.Repeat("1,", 366) + "1",
		strings.Repeat("A", MaxRuleBytes+1),
	}
	for _, in := range bad {
		if r, err := Parse(in); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrUnsupported", in, r, err)
		}
	}
}

func TestImpossibleFilters(t *testing.T) {
	impossibleRules := []string{
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30,31",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-30",
		"FREQ=YEARLY;BYMONTH=4,6,9,11;BYMONTHDAY=31",
		"FREQ=MONTHLY;BYMONTH=4,6,9,11;BYMONTHDAY=31",
		"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30",
		"FREQ=YEARLY;BYMONTH=2;BYYEARDAY=100",
		"FREQ=YEARLY;BYMONTH=1;BYYEARDAY=-1",
		"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=6",
		"FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=11,-11",
		"FREQ=WEEKLY;BYDAY=MO;BYSETPOS=2",
		"FREQ=DAILY;BYHOUR=9;BYSETPOS=2",
		"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=1;BYSETPOS=2",
		"FREQ=MONTHLY;BYDAY=6MO",
		"FREQ=MONTHLY;BYDAY=-6FR",
		"FREQ=YEARLY;BYMONTH=5;BYDAY=6MO",
	}
	for _, in := range impossibleRules {
		_, err := Parse(in)
		if !errors.Is(err, ErrImpossible) || !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q): %v, want ErrImpossible", in, err)
		}
	}
	possible := []string{
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=YEARLY;BYMONTH=2,3;BYMONTHDAY=30",
		"FREQ=YEARLY;BYMONTH=2;BYYEARDAY=60",
		"FREQ=YEARLY;BYMONTH=12;BYYEARDAY=-1",
		"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=5,6",
		"FREQ=MONTHLY;BYDAY=5MO",
		"FREQ=YEARLY;BYDAY=53MO",
		"FREQ=DAILY;BYHOUR=9,10;BYSETPOS=2",
	}
	for _, in := range possible {
		if _, err := Parse(in); err != nil {
			t.Errorf("Parse(%q): %v", in, err)
		}
	}
}

// fixtureRule reads the DTSTART and RRULE lines of a corpus file with a
// trivial scanner (unfolding only; the ics parser is not used here).
func fixtureRule(t *testing.T, path string) (dtstart time.Time, loc *time.Location, rule string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(l, " ") && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "X-MALACHI-TEST:") {
		t.Errorf("%s: no X-MALACHI-TEST line", path)
	}
	loc = time.UTC
	for _, l := range lines {
		name, value, _ := strings.Cut(l, ":")
		switch {
		case name == "RRULE":
			rule = value
		case strings.HasPrefix(name, "DTSTART"):
			if _, tzid, ok := strings.Cut(name, ";TZID="); ok {
				loc = zone(t, tzid)
			}
			dtstart, err = time.ParseInLocation("20060102T150405", value, loc)
			if err != nil {
				t.Fatalf("%s: DTSTART: %v", path, err)
			}
		}
	}
	return dtstart, loc, rule
}

func TestPathologicalFixtures(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/calendar/rrule-*.ics")
	if err != nil || len(paths) < 9 {
		t.Fatalf("fixtures: %v %v", paths, err)
	}
	for _, p := range paths {
		dt, loc, rule := fixtureRule(t, p)
		start := time.Now()
		r, err := Parse(rule)
		if err == nil {
			_, _, err = Expand(r, dt, loc, everything, 0)
		}
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Errorf("%s took %v", filepath.Base(p), elapsed)
		}
		t.Logf("%s %v", filepath.Base(p), elapsed)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", filepath.Base(p), err)
		}
	}
}

// TestHeavyValidRules: rules inside the limits that are as expensive as
// they get still end within 100 ms.
func TestHeavyValidRules(t *testing.T) {
	prague := zone(t, "Europe/Prague")
	monday1900 := time.Date(1900, 1, 1, 9, 0, 0, 0, prague)
	allDays := "MO,TU,WE,TH,FR,SA,SU"
	var ordinals []string
	for n := 1; n <= 53; n++ {
		for _, d := range []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"} {
			ordinals = append(ordinals, strings.Repeat("-", n%2)+strconv.Itoa(n)+d)
		}
	}
	rules := []string{
		"FREQ=DAILY;INTERVAL=7;BYDAY=TU;COUNT=5000",      // never matches
		"FREQ=DAILY;INTERVAL=7;BYDAY=TU",                 // never matches
		"FREQ=YEARLY;INTERVAL=4;BYMONTH=2;BYMONTHDAY=29", // 1901+4k: never leap
		"FREQ=DAILY;BYDAY=MO;BYMONTH=2;BYMONTHDAY=29;COUNT=5000",
		"FREQ=DAILY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;BYMINUTE=0;BYSECOND=0",
		"FREQ=DAILY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;BYMINUTE=0;BYSECOND=0;COUNT=5000",
		"FREQ=YEARLY;BYDAY=" + strings.Join(ordinals[:366], ","),
		"FREQ=YEARLY;BYDAY=" + allDays + ";BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23",
		"FREQ=YEARLY;BYWEEKNO=53;BYDAY=" + allDays,
		"FREQ=YEARLY;BYYEARDAY=366;BYMONTH=12",
		"FREQ=MONTHLY;BYMONTHDAY=29,30,31;BYMONTH=2;BYSETPOS=3",
		"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=29;COUNT=5000",
		"FREQ=HOURLY;INTERVAL=23;BYHOUR=3",
		"FREQ=HOURLY;INTERVAL=1",
	}
	win := Window{utc("2026-01-01T00:00Z"), utc("2200-01-01T00:00Z")}
	for _, s := range rules {
		r := mustParse(t, s)
		start := time.Now()
		_, _, err := Expand(r, monday1900, prague, win, 0)
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Errorf("%.60s took %v", s, elapsed)
		}
		t.Logf("%-60.60s %v", s, elapsed)
		if err != nil {
			t.Errorf("%.60s: %v", s, err)
		}
	}
}

func TestApplyExceptions(t *testing.T) {
	a, b, c, d := utc("2026-01-05T09:00Z"), utc("2026-01-06T09:00Z"), utc("2026-01-07T09:00Z"), utc("2026-01-06T15:00Z")
	prague := zone(t, "Europe/Prague")
	got, err := ApplyExceptions([]time.Time{a, b, c}, []time.Time{d, a}, []time.Time{b.In(prague)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []time.Time{a, d, c}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := ApplyExceptions(nil, make([]time.Time, MaxRDates+1), nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("RDATE flood: %v", err)
	}
	if _, err := ApplyExceptions(nil, nil, make([]time.Time, MaxEXDates+1)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("EXDATE flood: %v", err)
	}
}

func TestAPIShape(t *testing.T) {
	prague := zone(t, "Europe/Prague")
	tests := []struct {
		rule string
		want Recurrence
	}{
		{"FREQ=WEEKLY;BYDAY=MO,WE", Recurrence{Frequency: "weekly", Interval: 1, ByDay: []string{"MO", "WE"}}},
		{"FREQ=MONTHLY;BYDAY=-1FR;COUNT=10", Recurrence{Frequency: "monthly", Interval: 1, ByDay: []string{"-1FR"}, Count: 10}},
		{"FREQ=YEARLY;BYMONTH=10;BYMONTHDAY=14;INTERVAL=2", Recurrence{Frequency: "yearly", Interval: 2, ByMonthDay: []int{14}, ByMonth: []int{10}}},
		{"FREQ=DAILY;UNTIL=20261231T225959Z", Recurrence{Frequency: "daily", Interval: 1, Until: "2026-12-31"}},
		{"FREQ=WEEKLY;BYDAY=SU;WKST=SU", Recurrence{Frequency: "weekly", Interval: 1, ByDay: []string{"SU"}, WeekStart: "SU"}},
		{"FREQ=HOURLY;INTERVAL=2", Recurrence{Frequency: "daily", Interval: 2, Complex: true}},
		{"FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=-1", Recurrence{Frequency: "monthly", Interval: 1, ByDay: []string{"MO", "TU"}, Complex: true}},
		{"FREQ=MONTHLY;BYDAY=1MO,3MO", Recurrence{Frequency: "monthly", Interval: 1, ByDay: []string{"1MO", "3MO"}, Complex: true}},
		{"FREQ=YEARLY;BYYEARDAY=100", Recurrence{Frequency: "yearly", Interval: 1, Complex: true}},
		{"FREQ=YEARLY;BYWEEKNO=20;BYDAY=MO", Recurrence{Frequency: "yearly", Interval: 1, ByDay: []string{"MO"}, Complex: true}},
		{"FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR", Recurrence{Frequency: "daily", Interval: 1, ByDay: []string{"MO", "TU", "WE", "TH", "FR"}, Complex: true}},
		{"FREQ=DAILY;BYHOUR=9,17", Recurrence{Frequency: "daily", Interval: 1, Complex: true}},
		{"FREQ=MONTHLY;BYMONTH=1", Recurrence{Frequency: "monthly", Interval: 1, ByMonth: []int{1}, Complex: true}},
	}
	dt := time.Date(2026, 1, 5, 9, 0, 0, 0, prague)
	for _, tc := range tests {
		got := ToAPI(mustParse(t, tc.rule), prague)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ToAPI(%q) = %+v, want %+v", tc.rule, got, tc.want)
			continue
		}
		if got.Complex {
			if _, err := FromAPI(got, dt, prague, false); !errors.Is(err, ErrUnsupported) {
				t.Errorf("FromAPI of complex %q: %v", tc.rule, err)
			}
			continue
		}
		back, err := FromAPI(got, dt, prague, false)
		if err != nil {
			t.Errorf("FromAPI(%+v): %v", got, err)
			continue
		}
		if again := ToAPI(back, prague); !reflect.DeepEqual(again, got) {
			t.Errorf("round trip of %q: %+v", tc.rule, again)
		}
	}
	// A local until date becomes the end of that day in UTC, or a DATE.
	r, err := FromAPI(Recurrence{Frequency: "daily", Interval: 1, Until: "2026-07-01"}, dt, prague, false)
	if err != nil || Normalise(r) != "FREQ=DAILY;UNTIL=20260701T215959Z" {
		t.Errorf("timed until: %q %v", Normalise(r), err)
	}
	r, err = FromAPI(Recurrence{Frequency: "daily", Interval: 1, Until: "2026-07-01"}, dt, prague, true)
	if err != nil || Normalise(r) != "FREQ=DAILY;UNTIL=20260701" {
		t.Errorf("all-day until: %q %v", Normalise(r), err)
	}
	for _, bad := range []Recurrence{
		{Frequency: "hourly", Interval: 1},
		{Frequency: "daily", Interval: 1001},
		{Frequency: "daily", Interval: 1, Count: 3, Until: "2026-07-01"},
		{Frequency: "daily", Interval: 1, ByDay: []string{"MO"}},
		{Frequency: "weekly", Interval: 1, ByMonthDay: []int{1}},
		{Frequency: "weekly", Interval: 1, ByDay: []string{"2MO"}},
		{Frequency: "monthly", Interval: 1, ByMonth: []int{2}},
		{Frequency: "monthly", Interval: 1, ByMonthDay: []int{0}},
		{Frequency: "yearly", Interval: 1, ByMonth: []int{2}, ByMonthDay: []int{30}},
		{Frequency: "weekly", Interval: 1, WeekStart: "XX"},
		{Frequency: "daily", Interval: 1, Until: "9999-01-01"},
		{Frequency: "daily", Interval: 1, Until: "2026-13-01"},
	} {
		if _, err := FromAPI(bad, dt, prague, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("FromAPI(%+v): %v, want ErrUnsupported", bad, err)
		}
	}
}

func FuzzExpand(f *testing.F) {
	seeds := []string{
		"FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO,WE;INTERVAL=2", "FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=YEARLY;BYWEEKNO=53;BYDAY=MO", "FREQ=YEARLY;BYWEEKNO=-53,1,52;BYDAY=SU;WKST=SU",
		"FREQ=YEARLY;BYDAY=-53MO,53SU", "FREQ=YEARLY;BYYEARDAY=-366,366",
		"FREQ=HOURLY;INTERVAL=23;BYHOUR=3", "FREQ=DAILY;INTERVAL=7;BYDAY=TU",
		"FREQ=SECONDLY", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30,31", "FREQ=DAILY;COUNT=5000",
		"FREQ=WEEKLY;UNTIL=22001231T235959Z", "FREQ=YEARLY;INTERVAL=1000;BYMONTH=12;BYDAY=5SU",
	}
	for i, s := range seeds {
		f.Add(s, int64(i)*86400*397, int64(i)*3600*7)
	}
	prague := zone(f, "Europe/Prague")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, rule string, dtOffset, winOffset int64) {
		r, err := Parse(rule)
		if err != nil {
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Parse error not ErrUnsupported: %v", err)
			}
			return
		}
		if _, err := Parse(Normalise(r)); err != nil {
			t.Fatalf("Normalise(%q) = %q does not parse: %v", rule, Normalise(r), err)
		}
		const span = int64(300 * 365 * 86400)
		dt := base.Add(time.Duration((dtOffset%span)/2) * time.Second).Truncate(time.Second)
		from := base.Add(time.Duration((winOffset%span)/2) * time.Second)
		win := Window{from, from.AddDate(1, 0, 0)}
		start := time.Now()
		got, _, err := Expand(r, dt, prague, win, 0)
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Expand(%q) took %v", rule, elapsed)
		}
		if err != nil {
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Expand error not ErrUnsupported: %v", err)
			}
			return
		}
		if len(got) > MaxOccurrences {
			t.Fatalf("%d instances", len(got))
		}
		for i, g := range got {
			if g.Before(win.From) || !g.Before(win.To) || (i > 0 && !got[i-1].Before(g)) {
				t.Fatalf("instance %d %v out of order or window", i, g)
			}
		}
	})
}
