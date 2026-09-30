// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"testing"
	"time"
	_ "time/tzdata" // Europe/Prague on every platform, for comparison only
)

// The CET emulation equals Europe/Prague wherever the clock reading is
// unambiguous, 1996 (the EU's October rule) to 2037.
func TestCentralEuropeanMatchesPrague(t *testing.T) {
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatalf("load Europe/Prague: %v", err)
	}
	for y := 1996; y <= 2037; y++ {
		march, october := lastSunday(y, time.March), lastSunday(y, time.October)
		for d := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC); d.Year() == y; d = d.AddDate(0, 0, 1) {
			change := d.Equal(march) || d.Equal(october)
			for m := 0; m < 24*60; m += 30 {
				wall := d.Add(time.Duration(m) * time.Minute)
				if change && wall.Hour() == 2 {
					continue // skipped or repeated: see TestCentralEuropeanChanges
				}
				want := time.Date(y, wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, prague)
				if got := centralEuropean(wall); !got.Equal(want) {
					t.Fatalf("%s CET = %s, want %s", wall.Format("2006-01-02 15:04"), got.UTC(), want.UTC())
				}
			}
		}
	}
}

// The hour the clocks skip is read in winter time (java.time moves it
// forward by the gap: the same instant); the hour they repeat is read in
// summer time (java.time takes the earlier offset).
func TestCentralEuropeanChanges(t *testing.T) {
	for wall, want := range map[string]string{
		"2026-03-29 01:59": "2026-03-29 00:59",
		"2026-03-29 02:00": "2026-03-29 01:00",
		"2026-03-29 02:30": "2026-03-29 01:30",
		"2026-03-29 03:00": "2026-03-29 01:00",
		"2026-10-25 01:59": "2026-10-24 23:59",
		"2026-10-25 02:00": "2026-10-25 00:00",
		"2026-10-25 02:30": "2026-10-25 00:30",
		"2026-10-25 03:00": "2026-10-25 02:00",
		"2025-12-31 23:30": "2025-12-31 22:30",
		"2026-01-01 00:30": "2025-12-31 23:30",
	} {
		w, err := time.Parse("2006-01-02 15:04", wall)
		if err != nil {
			t.Fatal(err)
		}
		if got := centralEuropean(w).UTC().Format("2006-01-02 15:04"); got != want {
			t.Errorf("%s CET = %s UTC, want %s", wall, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	plus3 := time.FixedZone("", 3*3600)
	fb := time.Date(2026, 6, 11, 12, 0, 0, 0, plus3)
	for _, tc := range []struct {
		in   string
		want string // UTC; "" = not a date
	}{
		{"10/06/26 10:54 AM CEST", "2026-06-10 08:54"},
		{"10/06/26 14:39 GMT+2", "2026-06-10 12:39"},
		{"10/06/26 14:39 GMT +02:00", "2026-06-10 12:39"},
		{"10/06/26 14:39 +0200", "2026-06-10 12:39"},
		{"10/06/26 14:39 -0330", "2026-06-10 18:09"},
		{"10/06/26 14:39 +18", "2026-06-09 20:39"},
		{"10/06/26 14:39 +18:01", ""},
		{"10/06/26 14:39 +1860", ""},
		{"10/06/26 2:39PM", "2026-06-10 11:39"}, // no zone: fallback's location
		{"10/06/26 2:39 p", "2026-06-09 23:39"}, // "p" is neither PM nor a zone
		{"10/06/26 14:39 utc", "2026-06-10 14:39"},
		{"10/06/26 14:39 BST", "2026-06-10 13:39"},
		{"10/06/26 14:39 IST", "2026-06-10 11:39"}, // ambiguous: fallback's location
		{"10/06/26 14:39 EEST", "2026-06-10 11:39"},
		{"10/06/26 14:39 CST", "2026-06-10 20:39"},
		{"10/06/26 14:39 NZDT", "2026-06-10 01:39"},
		{"10/06/26 14:39 XYZ", "2026-06-10 11:39"},
		{"29/02/28 10:00 UTC", "2028-02-29 10:00"},
		{"29/02/27 10:00 UTC", ""},
		{"00/06/26 10:00 UTC", ""},
		{"10/00/26 10:00 UTC", ""},
		{"10/06/26 10:60 UTC", ""},
		{"10/06/026 10:00 UTC", "2026-06-10 10:00"},
		{"10/06/1999 10:00 UTC", "1999-06-10 10:00"},
		{"12/13/26 10:00 UTC", "2026-12-13 10:00"},
		{"13/12/26 10:00 UTC", "2026-12-13 10:00"},
		{"on 10/06/26 at 10:00 UTC", ""}, // "at" between date and time
		{"yesterday", ""},
		{"", ""},
		{"10/06/26", ""},
		{"99999/06/26 10:00 UTC", ""}, // day 99
	} {
		got, ok := parseDate(tc.in, fb)
		switch {
		case tc.want == "" && ok:
			t.Errorf("parseDate(%q) = %s, want no date", tc.in, got.UTC())
		case tc.want != "" && !ok:
			t.Errorf("parseDate(%q): no date, want %s", tc.in, tc.want)
		case ok && got.UTC().Format("2006-01-02 15:04") != tc.want:
			t.Errorf("parseDate(%q) = %s, want %s", tc.in, got.UTC().Format("2006-01-02 15:04"), tc.want)
		}
	}
}
