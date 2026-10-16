// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func parseLines(t *testing.T, lines ...string) Object {
	t.Helper()
	all := append([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN"}, lines...)
	all = append(all, "END:VCALENDAR", "")
	obj, err := Parse([]byte(strings.Join(all, "\r\n")), FromOptions{Me: me})
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func starts(occ []Occurrence) []string {
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.Format(time.RFC3339)
	}
	return out
}

func window(from, to string) Window { return Window{From: utc(from), To: utc(to)} }

func TestOccurrencesOverridesExdatesRdates(t *testing.T) {
	obj := parseLines(t,
		"BEGIN:VEVENT", "UID:s@example.org", "DTSTART;TZID=Europe/Prague:20261005T090000", "DTEND;TZID=Europe/Prague:20261005T100000",
		"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE;TZID=Europe/Prague:20261019T090000", "RDATE;TZID=Europe/Prague:20261021T090000",
		"SUMMARY:Series", "END:VEVENT",
		// moved instance
		"BEGIN:VEVENT", "UID:s@example.org", "RECURRENCE-ID;TZID=Europe/Prague:20261012T090000",
		"DTSTART;TZID=Europe/Prague:20261013T150000", "DTEND;TZID=Europe/Prague:20261013T160000", "SUMMARY:Moved", "END:VEVENT",
		// cancelled instance
		"BEGIN:VEVENT", "UID:s@example.org", "RECURRENCE-ID;TZID=Europe/Prague:20261102T090000",
		"DTSTART;TZID=Europe/Prague:20261102T090000", "DTEND;TZID=Europe/Prague:20261102T100000", "STATUS:CANCELLED", "END:VEVENT",
		// an override of an instance the rule does not have
		"BEGIN:VEVENT", "UID:s@example.org", "RECURRENCE-ID:20261029T080000Z",
		"DTSTART:20261029T080000Z", "DTEND:20261029T090000Z", "SUMMARY:Extra", "END:VEVENT",
	)
	occ, trunc, err := Occurrences(obj, window("2026-10-01T00:00:00Z", "2026-11-10T00:00:00Z"), nil, 0)
	if err != nil || trunc {
		t.Fatal(err, trunc)
	}
	want := []string{
		"2026-10-05T07:00:00Z", // CEST
		"2026-10-13T13:00:00Z", // the moved override
		"2026-10-21T07:00:00Z", // RDATE (19 Oct is an EXDATE)
		"2026-10-26T08:00:00Z", // CET after the DST end on 25 Oct
		"2026-10-29T08:00:00Z", // the override without an instance
		"2026-11-09T08:00:00Z",
	}
	if got := starts(occ); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("starts\n %v\nwant\n %v", got, want)
	}
	if occ[1].Override < 0 || obj.EventOf(occ[1]).Title != "Moved" || occ[1].Key != "2026-10-12T07:00:00Z" || !occ[1].Series {
		t.Errorf("override occurrence %+v", occ[1])
	}
	if occ[0].Override != -1 || occ[0].Key != "2026-10-05T07:00:00Z" || !occ[0].End.Equal(utc("2026-10-05T08:00:00Z")) {
		t.Errorf("master occurrence %+v", occ[0])
	}
	a := obj.APIOccurrence(occ[3], nil)
	if a.Title != "Series" || a.Start == nil || !a.Start.Equal(utc("2026-10-26T08:00:00Z")) || !a.Recurring || a.TimeZone != "Europe/Prague" {
		t.Errorf("api occurrence %+v", a)
	}
}

func TestOccurrencesOverlapAndCap(t *testing.T) {
	obj := parseLines(t,
		"BEGIN:VEVENT", "UID:d@example.org", "DTSTART:20261001T220000Z", "DTEND:20261002T020000Z", "RRULE:FREQ=DAILY", "END:VEVENT")
	// The instance that began on 4 Oct at 22:00 still runs at midnight.
	occ, _, _ := Occurrences(obj, window("2026-10-05T00:00:00Z", "2026-10-06T00:00:00Z"), nil, 0)
	if got := starts(occ); len(got) != 2 || got[0] != "2026-10-04T22:00:00Z" {
		t.Errorf("overlap: %v", got)
	}
	occ, trunc, _ := Occurrences(obj, window("2026-10-01T00:00:00Z", "2030-01-01T00:00:00Z"), nil, 10)
	if len(occ) != 10 || !trunc {
		t.Errorf("cap: %d %v", len(occ), trunc)
	}
	occ, trunc, _ = Occurrences(obj, window("2026-10-01T00:00:00Z", "2030-01-01T00:00:00Z"), nil, 0)
	if len(occ) != api.MaxSeriesOccurrences || !trunc {
		t.Errorf("default cap: %d %v", len(occ), trunc)
	}
}

func TestOccurrencesAllDayAndFloating(t *testing.T) {
	ad := parseLines(t,
		"BEGIN:VEVENT", "UID:a@example.org", "DTSTART;VALUE=DATE:20261030", "DTEND;VALUE=DATE:20261101",
		"RRULE:FREQ=YEARLY", "SUMMARY:Two days", "END:VEVENT")
	prague, _ := time.LoadLocation("Europe/Prague")
	// 31 Oct 00:30 in Prague is still 30 Oct in UTC: the window is read
	// on Prague's wall clock.
	occ, _, _ := Occurrences(ad, Window{From: time.Date(2027, 10, 31, 0, 30, 0, 0, prague), To: time.Date(2027, 10, 31, 1, 0, 0, 0, prague)}, prague, 0)
	if len(occ) != 1 || !occ[0].AllDay || occ[0].Key != "2027-10-30" {
		t.Fatalf("all-day: %+v", occ)
	}
	a := ad.APIOccurrence(occ[0], prague)
	if a.StartDate != "2027-10-30" || a.EndDate != "2027-11-01" || a.Start != nil {
		t.Errorf("all-day api %+v", a)
	}
	fl := parseLines(t,
		"BEGIN:VEVENT", "UID:f@example.org", "DTSTART:20261020T090000", "DTEND:20261020T100000", "RRULE:FREQ=DAILY;COUNT=3", "END:VEVENT")
	occ, _, _ = Occurrences(fl, window("2026-10-20T00:00:00Z", "2026-10-30T00:00:00Z"), prague, 0)
	if len(occ) != 3 || !occ[0].Floating || !occ[0].Start.Equal(utc("2026-10-20T09:00:00Z")) {
		t.Fatalf("floating: %+v", occ)
	}
	// A floating instance shows at its wall clock in the caller's zone.
	f := fl.APIOccurrence(occ[0], prague)
	if f.Start == nil || !f.Start.Equal(utc("2026-10-20T07:00:00Z")) || !f.Floating {
		t.Errorf("floating api %+v", f)
	}
}

func TestOccurrencesDST(t *testing.T) {
	ny := parseLines(t,
		"BEGIN:VEVENT", "UID:ny@example.org", "DTSTART;TZID=America/New_York:20260307T023000", "DTEND;TZID=America/New_York:20260307T033000",
		"RRULE:FREQ=DAILY;COUNT=3", "END:VEVENT")
	occ, _, _ := Occurrences(ny, window("2026-03-01T00:00:00Z", "2026-03-20T00:00:00Z"), nil, 0)
	// 8 March 02:30 does not exist: it moves forward by the gap (03:30 EDT).
	want := []string{"2026-03-07T07:30:00Z", "2026-03-08T07:30:00Z", "2026-03-09T06:30:00Z"}
	if got := starts(occ); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("DST gap: %v want %v", got, want)
	}
}

func TestOccurrencesRefusedAndCancelled(t *testing.T) {
	for _, name := range []string{"rrule-secondly.ics", "rrule-minutely.ics", "rrule-count-huge.ics", "rrule-until-year-9999.ics",
		"rrule-byday-flood.ics", "rrule-impossible-feb30.ics", "rrule-bysetpos-out-of-range.ics", "rrule-interval-zero.ics",
		"rrule-repeated-parts.ics", "exdate-flood.ics", "rdate-flood.ics", "override-flood.ics", "dtstart-year-9999.ics"} {
		obj := parseFixture(t, name, FromOptions{})
		start := time.Now()
		occ, _, err := Occurrences(obj, window("1900-01-01T00:00:00Z", "2200-01-01T00:00:00Z"), nil, 0)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s took %v", name, d)
		}
		if len(occ) > api.MaxSeriesOccurrences {
			t.Errorf("%s: %d occurrences", name, len(occ))
		}
	}
	sec := parseFixture(t, "rrule-secondly.ics", FromOptions{})
	occ, _, _ := Occurrences(sec, window("1900-01-01T00:00:00Z", "2200-01-01T00:00:00Z"), nil, 0)
	if len(occ) != 1 {
		t.Errorf("a refused rule shows its first instance: %d", len(occ))
	}
	c := parseLines(t, "BEGIN:VEVENT", "UID:c@example.org", "DTSTART:20261020T090000Z", "STATUS:CANCELLED", "END:VEVENT")
	if occ, _, _ := Occurrences(c, window("2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z"), nil, 0); len(occ) != 0 {
		t.Errorf("cancelled: %+v", occ)
	}
	z := parseLines(t, "BEGIN:VEVENT", "UID:z@example.org", "DTSTART:20261020T090000Z", "END:VEVENT")
	if occ, _, _ := Occurrences(z, window("2026-10-20T09:00:00Z", "2026-10-20T09:00:01Z"), nil, 0); len(occ) != 1 || occ[0].Key != "" {
		t.Errorf("zero-length single: %+v", occ)
	}
}
