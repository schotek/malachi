// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"fmt"
	"strings"
	"time"
)

// Kind says how a Time is anchored.
type Kind uint8

// Kinds of Time.
const (
	KindDate     Kind = iota + 1 // DATE: a day, no time of day (all-day)
	KindFloating                 // DATE-TIME without Z or TZID: the same wall clock everywhere
	KindUTC                      // DATE-TIME with Z: an instant
	KindZoned                    // DATE-TIME with a TZID: a wall clock in that zone
)

// Time is a DATE or DATE-TIME value. The fields are the value as written;
// for KindZoned, TZID is the parameter as written (this package does not
// resolve zones: calendar/tz does). Year is 1–9999; Second 0–59 (a leap
// second 60 is read as 59).
type Time struct {
	Kind                      Kind
	Year                      int
	Month                     time.Month
	Day, Hour, Minute, Second int
	TZID                      string
}

// Wall returns the fields as a time.Time in time.UTC: a wall clock, not an
// instant (except for KindUTC, where it is the instant).
func (t Time) Wall() time.Time {
	return time.Date(t.Year, t.Month, t.Day, t.Hour, t.Minute, t.Second, 0, time.UTC)
}

// UTC returns the instant of a KindUTC value.
func (t Time) UTC() (time.Time, bool) {
	if t.Kind != KindUTC {
		return time.Time{}, false
	}
	return t.Wall(), true
}

// In returns the value in loc: the instant for KindUTC, otherwise the wall
// clock read in loc (a date at midnight).
func (t Time) In(loc *time.Location) time.Time {
	if t.Kind == KindUTC {
		return t.Wall().In(loc)
	}
	return time.Date(t.Year, t.Month, t.Day, t.Hour, t.Minute, t.Second, 0, loc)
}

// Value formats the property value (no TZID: see NewTime).
func (t Time) Value() string {
	if t.Kind == KindDate {
		return fmt.Sprintf("%04d%02d%02d", t.Year, int(t.Month), t.Day)
	}
	s := fmt.Sprintf("%04d%02d%02dT%02d%02d%02d", t.Year, int(t.Month), t.Day, t.Hour, t.Minute, t.Second)
	if t.Kind == KindUTC {
		s += "Z"
	}
	return s
}

// DateOf returns the date of a time.Time's wall clock as a KindDate value.
func DateOf(t time.Time) Time {
	return Time{Kind: KindDate, Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// UTCOf returns the instant as a KindUTC value.
func UTCOf(t time.Time) Time {
	t = t.UTC()
	return Time{Kind: KindUTC, Year: t.Year(), Month: t.Month(), Day: t.Day(), Hour: t.Hour(), Minute: t.Minute(), Second: t.Second()}
}

// NewTime builds a DATE or DATE-TIME property (VALUE=DATE for KindDate,
// TZID for KindZoned).
func NewTime(name string, t Time) Property {
	p := NewProperty(name, t.Value())
	switch t.Kind {
	case KindDate:
		p.SetParam("VALUE", "DATE")
	case KindZoned:
		p.SetParam("TZID", t.TZID)
	}
	return p
}

// Time reads the value as DATE or DATE-TIME, by its form (a VALUE
// parameter that disagrees is ignored: real data gets it wrong), with the
// property's TZID for a local DATE-TIME. A TZID beside a UTC value is
// ignored.
func (p *Property) Time() (Time, error) { return ParseTime(p.val(), p.Param("TZID")) }

// Times reads a comma-separated list of DATE / DATE-TIME (EXDATE, RDATE).
// It returns the values it could read and the first error, if any.
func (p *Property) Times() ([]Time, error) {
	var (
		out   []Time
		first error
	)
	tzid := p.Param("TZID")
	for _, v := range p.Values() {
		t, err := ParseTime(v, tzid)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		out = append(out, t)
	}
	return out, first
}

// ParseTime reads a DATE (YYYYMMDD) or DATE-TIME (YYYYMMDDTHHMMSS[Z]).
func ParseTime(v, tzid string) (Time, error) {
	v = strings.TrimSpace(v)
	switch {
	case len(v) == 8:
		t, err := parseDate(v)
		if err != nil {
			return Time{}, err
		}
		t.Kind = KindDate
		return t, nil
	case len(v) == 15 || len(v) == 16:
		if v[8] != 'T' && v[8] != 't' {
			return Time{}, fmt.Errorf("date-time: %w", ErrSyntax)
		}
		t, err := parseDate(v[:8])
		if err != nil {
			return Time{}, err
		}
		if !digits(v[9:15]) {
			return Time{}, fmt.Errorf("date-time: %w", ErrSyntax)
		}
		t.Hour, t.Minute, t.Second = num(v[9:11]), num(v[11:13]), num(v[13:15])
		if t.Hour > 23 || t.Minute > 59 || t.Second > 60 {
			return Time{}, fmt.Errorf("date-time: %w", ErrRange)
		}
		if t.Second == 60 {
			t.Second = 59
		}
		switch {
		case len(v) == 16 && (v[15] == 'Z' || v[15] == 'z'):
			t.Kind = KindUTC
		case len(v) == 16:
			return Time{}, fmt.Errorf("date-time: %w", ErrSyntax)
		case tzid != "":
			t.Kind, t.TZID = KindZoned, tzid
		default:
			t.Kind = KindFloating
		}
		return t, nil
	}
	return Time{}, fmt.Errorf("date-time: %w", ErrSyntax)
}

func parseDate(v string) (Time, error) {
	if !digits(v) {
		return Time{}, fmt.Errorf("date: %w", ErrSyntax)
	}
	t := Time{Year: num(v[:4]), Month: time.Month(num(v[4:6])), Day: num(v[6:8])}
	if t.Year < 1 || t.Month < 1 || t.Month > 12 || t.Day < 1 || t.Day > daysIn(t.Year, t.Month) {
		return Time{}, fmt.Errorf("date: %w", ErrRange)
	}
	return t, nil
}

func daysIn(year int, m time.Month) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// num reads at most 9 ASCII digits (callers checked them).
func num(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// UTCOffset reads the value as UTC-OFFSET (TZOFFSETFROM, TZOFFSETTO).
func (p *Property) UTCOffset() (int, error) { return ParseUTCOffset(p.val()) }

// ParseUTCOffset reads ("+" / "-") HHMM[SS] as seconds east of UTC. Only
// the grammar is checked (hours ≤ 23); whether an offset is plausible is
// for calendar/tz to decide.
func ParseUTCOffset(v string) (int, error) {
	v = strings.TrimSpace(v)
	if (len(v) != 5 && len(v) != 7) || v[0] != '+' && v[0] != '-' || !digits(v[1:]) {
		return 0, fmt.Errorf("utc-offset: %w", ErrSyntax)
	}
	h, m, s := num(v[1:3]), num(v[3:5]), 0
	if len(v) == 7 {
		s = num(v[5:7])
	}
	if h > 23 || m > 59 || s > 59 {
		return 0, fmt.Errorf("utc-offset: %w", ErrRange)
	}
	off := h*3600 + m*60 + s
	if v[0] == '-' {
		off = -off
	}
	return off, nil
}

// FormatUTCOffset writes seconds east of UTC as a UTC-OFFSET.
func FormatUTCOffset(off int) string {
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	s := fmt.Sprintf("%c%02d%02d", sign, off/3600%100, off/60%60)
	if off%60 != 0 {
		s += fmt.Sprintf("%02d", off%60)
	}
	return s
}
