// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package tz

import (
	"fmt"
	"time"
)

// MaxVTIMEZONEYears caps the years a generated VTIMEZONE covers (D4).
const MaxVTIMEZONEYears = 10

// maxTransitions bounds the observances of one VTIMEZONE (no real zone
// has more than a handful of transitions a year).
const maxTransitions = 8 * MaxVTIMEZONEYears

// WriteVTIMEZONE returns the content lines of a VTIMEZONE component for
// the IANA zone loc covering the calendar years from from.Year() to
// to.Year() in loc, capped at MaxVTIMEZONEYears years from the first.
//
// The lines are unfolded and unescaped iCalendar content lines of the
// form NAME[;PARAM=…]:VALUE, starting with BEGIN:VTIMEZONE and ending
// with END:VTIMEZONE, without line terminators; the ics encoder folds
// and terminates them. Every value is plain ASCII that needs no
// escaping. The first observance is the one in effect on January 1 of
// the first year (its DTSTART is the transition that started it, or
// 19700101T000000 for a zone without transitions); every transition
// found with time.Time.ZoneBounds up to the end of the last year
// follows as a STANDARD or DAYLIGHT observance whose DTSTART is the
// local time just before the transition (RFC 5545 §3.6.5) and whose
// TZOFFSETFROM/TZOFFSETTO are the offsets before and after it.
func WriteVTIMEZONE(loc *time.Location, from, to time.Time) ([]string, error) {
	if loc == nil {
		return nil, ErrNotIANA
	}
	name := loc.String()
	if name != "UTC" {
		if l, ok := loadIANA(name); !ok || l.String() != name {
			return nil, fmt.Errorf("%w: %q", ErrNotIANA, short(name))
		}
	}
	first, last := from.In(loc).Year(), to.In(loc).Year()
	if last < first {
		return nil, fmt.Errorf("tz: VTIMEZONE range ends before it starts")
	}
	if last > first+MaxVTIMEZONEYears-1 {
		last = first + MaxVTIMEZONEYears - 1
	}
	start := time.Date(first, time.January, 1, 0, 0, 0, 0, loc)
	end := time.Date(last+1, time.January, 1, 0, 0, 0, 0, loc)

	lines := []string{"BEGIN:VTIMEZONE", "TZID:" + name}

	// The observance in effect at the start of the range.
	zs, _ := start.ZoneBounds()
	_, offTo := start.Zone()
	offFrom := offTo
	dtstart := "19700101T000000"
	if !zs.IsZero() {
		before := zs.Add(-time.Second)
		_, offFrom = before.Zone()
		dtstart = localStamp(zs, offFrom)
	}
	lines = observance(lines, start, offFrom, offTo, dtstart)

	t := start
	for n := 0; n < maxTransitions; n++ {
		_, next := t.ZoneBounds()
		if next.IsZero() || !next.Before(end) {
			break
		}
		_, before := next.Add(-time.Second).Zone()
		_, after := next.Zone()
		lines = observance(lines, next, before, after, localStamp(next, before))
		t = next
	}
	return append(lines, "END:VTIMEZONE"), nil
}

func observance(lines []string, at time.Time, from, to int, dtstart string) []string {
	kind := "STANDARD"
	if at.IsDST() {
		kind = "DAYLIGHT"
	}
	abbr, _ := at.Zone()
	lines = append(lines,
		"BEGIN:"+kind,
		"DTSTART:"+dtstart,
		"TZOFFSETFROM:"+offsetValue(from),
		"TZOFFSETTO:"+offsetValue(to),
	)
	if a := tzname(abbr); a != "" {
		lines = append(lines, "TZNAME:"+a)
	}
	return append(lines, "END:"+kind)
}

// localStamp is the instant t as a local DATE-TIME in the given offset.
func localStamp(t time.Time, offset int) string {
	return t.In(time.FixedZone("", offset)).Format("20060102T150405")
}

// offsetValue formats a UTC-OFFSET value (±HHMM, ±HHMMSS with seconds).
func offsetValue(sec int) string {
	sign := '+'
	if sec < 0 {
		sign, sec = '-', -sec
	}
	h, m, s := sec/3600, sec%3600/60, sec%60
	if s != 0 {
		return fmt.Sprintf("%c%02d%02d%02d", sign, h, m, s)
	}
	return fmt.Sprintf("%c%02d%02d", sign, h, m)
}

// tzname keeps a zone abbreviation that is plain ASCII (tzdata's "CEST"
// or "+03"); anything else is left out.
func tzname(s string) string {
	if len(s) == 0 || len(s) > 16 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-') {
			return ""
		}
	}
	return s
}
