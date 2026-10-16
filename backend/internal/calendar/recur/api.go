// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package recur

import (
	"strconv"
	"strings"
	"time"
)

// Recurrence is the simple shape of a rule that the event editor shows,
// field for field the api.Recurrence of docs/calendar.md §4.2 (without
// its "exceptions" count, which belongs to an event's instances, not to
// the rule). It is defined here so that this package does not import
// pkg/api; the event model converts it one to one.
type Recurrence struct {
	Frequency  string   `json:"frequency"`            // daily|weekly|monthly|yearly
	Interval   int      `json:"interval"`             // ≥ 1
	ByDay      []string `json:"byDay,omitempty"`      // "MO"; monthly/yearly with an ordinal, "2TU", "-1FR"
	ByMonthDay []int    `json:"byMonthDay,omitempty"` // -31..31, not 0
	ByMonth    []int    `json:"byMonth,omitempty"`    // 1–12
	Until      string   `json:"until,omitempty"`      // Date "2026-10-14", the last day an instance may start
	Count      int      `json:"count,omitempty"`
	WeekStart  string   `json:"weekStart,omitempty"` // "MO".."SU"
	Complex    bool     `json:"complex,omitempty"`   // the rule has more than this shape
}

var apiFreq = map[Frequency]string{Daily: "daily", Weekly: "weekly", Monthly: "monthly", Yearly: "yearly"}

// ToAPI returns the simple shape of r. A rule with more than the shape
// (HOURLY, BYSETPOS, BYYEARDAY, BYWEEKNO, BYHOUR/BYMINUTE/BYSECOND,
// BYDAY with DAILY, plain weekdays with MONTHLY/YEARLY, more than one
// ordinal, ordinals mixed with plain days, BYMONTHDAY outside
// MONTHLY/YEARLY, BYMONTH outside YEARLY, BYDAY and BYMONTHDAY
// together) is returned as far as it fits with Complex set; HOURLY reads
// as "daily". A UNTIL becomes the local date in loc (nil = UTC) of its
// last possible instance. The caller adds Complex for RDATEs.
func ToAPI(r Rule, loc *time.Location) Recurrence {
	if loc == nil {
		loc = time.UTC
	}
	out := Recurrence{Frequency: apiFreq[r.Freq], Interval: r.Interval, Count: r.Count}
	complexRule := false
	if r.Freq == Hourly {
		out.Frequency, complexRule = "daily", true
	}
	if len(r.BySetPos)+len(r.ByYearDay)+len(r.ByWeekNo)+len(r.ByHour)+len(r.ByMinute)+len(r.BySecond) > 0 {
		complexRule = true
	}
	ordinals, plain := 0, 0
	for _, d := range r.ByDay {
		out.ByDay = append(out.ByDay, dayString(d))
		if d.N != 0 {
			ordinals++
		} else {
			plain++
		}
	}
	switch r.Freq {
	case Weekly:
		// plain days only (ordinals are refused by Parse)
	case Monthly, Yearly:
		if plain > 0 || ordinals > 1 || (ordinals > 0 && len(r.ByMonthDay) > 0) {
			complexRule = true
		}
	default:
		if len(r.ByDay) > 0 {
			complexRule = true
		}
	}
	out.ByMonthDay = clone(r.ByMonthDay)
	if len(r.ByMonthDay) > 0 && r.Freq != Monthly && r.Freq != Yearly {
		complexRule = true
	}
	out.ByMonth = clone(r.ByMonth)
	if len(r.ByMonth) > 0 && r.Freq != Yearly {
		complexRule = true
	}
	switch r.UntilKind {
	case UntilDate:
		out.Until = r.Until.Format(time.DateOnly)
	case UntilFloating:
		out.Until = r.Until.Format(time.DateOnly)
	case UntilUTC:
		out.Until = r.Until.In(loc).Format(time.DateOnly)
	}
	if r.WeekStart != time.Monday {
		out.WeekStart = dayNames[r.WeekStart%7]
	}
	out.Complex = complexRule
	return out
}

// FromAPI builds a validated rule from the simple shape for an event
// starting at dtstart in loc (nil = UTC). Until becomes a DATE for an
// all-day event and otherwise the UTC instant of the end of that local
// day (RFC 5545 requires a UTC UNTIL with a zoned DTSTART). A shape with
// Complex set, an unknown frequency or weekday, or values the shape does
// not allow for its frequency is ErrUnsupported; so is anything Parse
// or CheckStart would refuse.
func FromAPI(rec Recurrence, dtstart time.Time, loc *time.Location, allDay bool) (Rule, error) {
	if loc == nil {
		loc = time.UTC
	}
	if rec.Complex {
		return Rule{}, unsupported("a complex rule cannot be built from its shape")
	}
	r := Rule{Interval: rec.Interval, Count: rec.Count, WeekStart: time.Monday}
	for f, n := range apiFreq {
		if n == rec.Frequency {
			r.Freq = f
		}
	}
	if r.Freq == 0 {
		return Rule{}, unsupported("frequency %s", safe(rec.Frequency))
	}
	if r.Interval == 0 {
		r.Interval = 1
	}
	if rec.WeekStart != "" {
		d, ok := dayByName(strings.ToUpper(rec.WeekStart))
		if !ok {
			return Rule{}, unsupported("weekStart %s", safe(rec.WeekStart))
		}
		r.WeekStart = d
	}
	if len(rec.ByDay) > 0 {
		days, err := dayList(strings.ToUpper(strings.Join(rec.ByDay, ",")))
		if err != nil {
			return Rule{}, err
		}
		r.ByDay = days
	}
	var err error
	if len(rec.ByMonthDay) > 0 {
		if r.ByMonthDay, err = intList(joinInts(rec.ByMonthDay), 1, 31, true); err != nil {
			return Rule{}, err
		}
	}
	if len(rec.ByMonth) > 0 {
		if r.ByMonth, err = intList(joinInts(rec.ByMonth), 1, 12, false); err != nil {
			return Rule{}, err
		}
	}
	if rec.Until != "" {
		d, err := time.ParseInLocation(time.DateOnly, rec.Until, time.UTC)
		if err != nil || d.Year() < MinYear || d.Year() > MaxUntilYear {
			return Rule{}, unsupported("bad until")
		}
		if allDay {
			r.Until, r.UntilKind = d, UntilDate
		} else {
			end := time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, loc)
			r.Until, r.UntilKind = end.UTC(), UntilUTC
		}
	}
	// The shape's own rules: which list goes with which frequency.
	switch r.Freq {
	case Daily:
		if len(r.ByDay)+len(r.ByMonthDay)+len(r.ByMonth) > 0 {
			return Rule{}, unsupported("daily with byDay, byMonthDay or byMonth")
		}
	case Weekly:
		if len(r.ByMonthDay)+len(r.ByMonth) > 0 {
			return Rule{}, unsupported("weekly with byMonthDay or byMonth")
		}
	case Monthly:
		if len(r.ByMonth) > 0 {
			return Rule{}, unsupported("monthly with byMonth")
		}
	}
	if err := r.validate(); err != nil {
		return Rule{}, err
	}
	if err := CheckStart(r, dtstart, loc); err != nil {
		return Rule{}, err
	}
	return r, nil
}

func joinInts(l []int) string {
	s := make([]string, len(l))
	for i, n := range l {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}
