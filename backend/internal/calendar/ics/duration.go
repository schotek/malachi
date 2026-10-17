// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration is a DURATION value. Weeks and days are nominal (calendar days,
// which DST can make 23 or 25 hours long), hours, minutes and seconds exact,
// as RFC 5545 §3.3.6 says.
type Duration struct {
	Negative                             bool
	Weeks, Days, Hours, Minutes, Seconds int
}

// Approx returns the duration with every day 24 hours long.
func (d Duration) Approx() time.Duration {
	s := int64(d.Weeks*7+d.Days)*86400 + int64(d.Hours)*3600 + int64(d.Minutes)*60 + int64(d.Seconds)
	if d.Negative {
		s = -s
	}
	return time.Duration(s) * time.Second
}

// AddTo adds the duration to t: the nominal part with AddDate (in t's
// location), then the exact part.
func (d Duration) AddTo(t time.Time) time.Time {
	sign := 1
	if d.Negative {
		sign = -1
	}
	t = t.AddDate(0, 0, sign*(d.Weeks*7+d.Days))
	exact := time.Duration(d.Hours)*time.Hour + time.Duration(d.Minutes)*time.Minute + time.Duration(d.Seconds)*time.Second
	return t.Add(time.Duration(sign) * exact)
}

// String writes the duration in RFC 5545 form (weeks alone as PnW,
// otherwise weeks as days).
func (d Duration) String() string {
	var b strings.Builder
	if d.Negative {
		b.WriteByte('-')
	}
	b.WriteByte('P')
	if d.Weeks != 0 && d.Days == 0 && d.Hours == 0 && d.Minutes == 0 && d.Seconds == 0 {
		b.WriteString(strconv.Itoa(d.Weeks) + "W")
		return b.String()
	}
	if days := d.Weeks*7 + d.Days; days != 0 {
		b.WriteString(strconv.Itoa(days) + "D")
	}
	if d.Hours != 0 || d.Minutes != 0 || d.Seconds != 0 || d.Weeks*7+d.Days == 0 {
		b.WriteByte('T')
		if d.Hours != 0 {
			b.WriteString(strconv.Itoa(d.Hours) + "H")
		}
		if d.Minutes != 0 {
			b.WriteString(strconv.Itoa(d.Minutes) + "M")
		}
		if d.Seconds != 0 || d.Hours == 0 && d.Minutes == 0 {
			b.WriteString(strconv.Itoa(d.Seconds) + "S")
		}
	}
	return b.String()
}

// Duration reads the value as DURATION.
func (p *Property) Duration() (Duration, error) { return ParseDuration(p.val()) }

// ParseDuration reads [+/-]P followed by weeks, days and a T part (hours,
// minutes, seconds). It is a little more lenient than RFC 5545 (weeks may
// be combined with the other units) and bounded: more than
// MaxDurationSeconds in total is ErrRange.
func ParseDuration(v string) (Duration, error) {
	var d Duration
	s := strings.TrimSpace(v)
	if s != "" && (s[0] == '+' || s[0] == '-') {
		d.Negative = s[0] == '-'
		s = s[1:]
	}
	if s == "" || s[0] != 'P' && s[0] != 'p' {
		return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
	}
	s = s[1:]
	units := "WDT"
	inTime, seen := false, false
	for s != "" {
		if s[0] == 'T' || s[0] == 't' {
			if inTime {
				return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
			}
			inTime, units, s = true, "HMS", s[1:]
			if s == "" {
				return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
			}
			continue
		}
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 || i == len(s) {
			return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
		}
		if i > 9 {
			return Duration{}, fmt.Errorf("duration: %w", ErrRange)
		}
		n := num(s[:i])
		u := strings.ToUpper(s[i : i+1])
		k := strings.Index(units, u)
		if k < 0 || u == "T" {
			return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
		}
		units = units[k+1:] // units only in order, each at most once
		switch u {
		case "W":
			d.Weeks = n
		case "D":
			d.Days = n
		case "H":
			d.Hours = n
		case "M":
			d.Minutes = n
		case "S":
			d.Seconds = n
		}
		seen = true
		s = s[i+1:]
	}
	if !seen {
		return Duration{}, fmt.Errorf("duration: %w", ErrSyntax)
	}
	total := int64(d.Weeks)*7*86400 + int64(d.Days)*86400 + int64(d.Hours)*3600 + int64(d.Minutes)*60 + int64(d.Seconds)
	if total > MaxDurationSeconds {
		return Duration{}, fmt.Errorf("duration: %w", ErrRange)
	}
	return d, nil
}

// Period is a PERIOD value: a start and either an explicit end or a
// duration (HasDuration).
type Period struct {
	Start, End  Time
	Duration    Duration
	HasDuration bool
}

// Periods reads a comma-separated list of PERIOD (RDATE;VALUE=PERIOD,
// FREEBUSY), with the property's TZID. It returns what it could read and
// the first error.
func (p *Property) Periods() ([]Period, error) {
	var (
		out   []Period
		first error
	)
	tzid := p.Param("TZID")
	for _, v := range p.Values() {
		pe, err := ParsePeriod(v, tzid)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		out = append(out, pe)
	}
	return out, first
}

// ParsePeriod reads start "/" (end | duration); start and end are
// DATE-TIMEs.
func ParsePeriod(v, tzid string) (Period, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(v), "/")
	if !ok {
		return Period{}, fmt.Errorf("period: %w", ErrSyntax)
	}
	var pe Period
	var err error
	if pe.Start, err = ParseTime(a, tzid); err != nil {
		return Period{}, err
	}
	if pe.Start.Kind == KindDate {
		return Period{}, fmt.Errorf("period: %w", ErrSyntax)
	}
	if b != "" && (b[0] == 'P' || b[0] == 'p' || b[0] == '+' || b[0] == '-') {
		if pe.Duration, err = ParseDuration(b); err != nil {
			return Period{}, err
		}
		pe.HasDuration = true
		return pe, nil
	}
	if pe.End, err = ParseTime(b, tzid); err != nil {
		return Period{}, err
	}
	if pe.End.Kind == KindDate {
		return Period{}, fmt.Errorf("period: %w", ErrSyntax)
	}
	return pe, nil
}
