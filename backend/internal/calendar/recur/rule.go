// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package recur parses, validates and expands RFC 5545 recurrence rules
// (docs/calendar.md §3.3, D2).
//
// A rule is parsed and validated here, with limits far tighter than the
// RFC, before github.com/teambition/rrule-go sees it; rrule-go only ever
// gets a validated ROption whose UNTIL is clamped to the query window.
// Everything outside the limits is ErrUnsupported: the event is then
// shown as its first instance only (recur_ok = 0).
package recur

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Version is the version of the expansion rules; it is part of meta
// calendar.expand, and a change re-expands every stored series.
const Version = 1

// Limits of §3.3.
const (
	MaxInterval  = 1000
	MaxCount     = 5000
	MaxListLen   = 366 // entries of one BYxxx list
	MaxRDates    = 1000
	MaxEXDates   = 5000
	MinYear      = 1900
	MaxUntilYear = 2200
	// MaxRuleBytes bounds the RRULE value looked at.
	MaxRuleBytes = 8 << 10
	// MaxOccurrences is the default cap of Expand (api.MaxSeriesOccurrences).
	MaxOccurrences = 1000
	// MaxHourlyCandidates stops an HOURLY expansion (§3.3).
	MaxHourlyCandidates = 20000
	// maxCandidates stops any expansion; far above what a valid rule
	// needs for one window.
	maxCandidates = 400000
)

var (
	// ErrUnsupported: the rule is invalid, outside the limits, or cannot
	// be expanded; the event is shown as its first instance only.
	ErrUnsupported = errors.New("recur: recurrence unsupported")
	// ErrImpossible: the rule's filters can never match. Every
	// ErrImpossible is also ErrUnsupported.
	ErrImpossible = errors.New("recur: recurrence never matches")
)

// Frequency is the FREQ of a rule.
type Frequency int

// Frequencies, in the order of RFC 5545 (shortest last).
const (
	Yearly Frequency = iota + 1
	Monthly
	Weekly
	Daily
	Hourly
)

var freqNames = map[Frequency]string{
	Yearly: "YEARLY", Monthly: "MONTHLY", Weekly: "WEEKLY", Daily: "DAILY", Hourly: "HOURLY",
}

func (f Frequency) String() string { return freqNames[f] }

// UntilKind is the form of a rule's UNTIL.
type UntilKind int

const (
	// UntilNone: no UNTIL.
	UntilNone UntilKind = iota
	// UntilDate: a DATE (inclusive, compared to the local date).
	UntilDate
	// UntilUTC: a DATE-TIME in UTC (inclusive, compared as instants).
	UntilUTC
	// UntilFloating: a DATE-TIME without Z (inclusive, compared to the
	// local wall clock of the event's zone).
	UntilFloating
)

// WeekdayNum is one BYDAY entry: N is the ordinal (0 = every, ±1..±53).
type WeekdayNum struct {
	N   int
	Day time.Weekday
}

// Rule is a parsed, validated RRULE. Lists are sorted and without
// duplicates. Until holds its fields in UTC for every UntilKind (for
// UntilDate and UntilFloating they are wall-clock fields, not an
// instant).
type Rule struct {
	Freq       Frequency
	Interval   int // ≥ 1
	Count      int // 0 = none
	Until      time.Time
	UntilKind  UntilKind
	BySecond   []int
	ByMinute   []int
	ByHour     []int
	ByDay      []WeekdayNum
	ByMonthDay []int
	ByYearDay  []int
	ByWeekNo   []int
	ByMonth    []int
	BySetPos   []int
	WeekStart  time.Weekday // Monday unless WKST says otherwise
}

var dayNames = [7]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

func dayByName(s string) (time.Weekday, bool) {
	for i, n := range dayNames {
		if n == s {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrUnsupported}, args...)...)
}

func impossible(format string, args ...any) error {
	return fmt.Errorf("%w: %w: "+format, append([]any{ErrUnsupported, ErrImpossible}, args...)...)
}

// Parse parses and validates an RRULE value ("FREQ=WEEKLY;BYDAY=MO", an
// optional "RRULE:" prefix is accepted). Part names and values are
// case-insensitive; a repeated part, an unknown part, a value out of the
// limits of §3.3 or a filter combination that can never match is
// ErrUnsupported. Checks that need DTSTART are done by CheckStart and
// Expand.
func Parse(value string) (Rule, error) {
	if len(value) > MaxRuleBytes {
		return Rule{}, unsupported("rule longer than %d bytes", MaxRuleBytes)
	}
	v := strings.TrimSpace(value)
	if len(v) >= 6 && strings.EqualFold(v[:6], "RRULE:") {
		v = v[6:]
	}
	r := Rule{Interval: 1, WeekStart: time.Monday}
	seen := map[string]bool{}
	for _, part := range strings.Split(v, ";") {
		if part == "" {
			continue
		}
		name, val, ok := strings.Cut(part, "=")
		if !ok {
			return Rule{}, unsupported("part without value")
		}
		name = strings.ToUpper(name)
		val = strings.ToUpper(val)
		if seen[name] {
			return Rule{}, unsupported("repeated part %s", safe(name))
		}
		seen[name] = true
		var err error
		switch name {
		case "FREQ":
			err = r.parseFreq(val)
		case "INTERVAL":
			r.Interval, err = number(val, 1, MaxInterval)
		case "COUNT":
			r.Count, err = number(val, 1, MaxCount)
		case "UNTIL":
			err = r.parseUntil(val)
		case "BYSECOND":
			r.BySecond, err = intList(val, 0, 59, false)
		case "BYMINUTE":
			r.ByMinute, err = intList(val, 0, 59, false)
		case "BYHOUR":
			r.ByHour, err = intList(val, 0, 23, false)
		case "BYDAY":
			r.ByDay, err = dayList(val)
		case "BYMONTHDAY":
			r.ByMonthDay, err = intList(val, 1, 31, true)
		case "BYYEARDAY":
			r.ByYearDay, err = intList(val, 1, 366, true)
		case "BYWEEKNO":
			r.ByWeekNo, err = intList(val, 1, 53, true)
		case "BYMONTH":
			r.ByMonth, err = intList(val, 1, 12, false)
		case "BYSETPOS":
			r.BySetPos, err = intList(val, 1, 366, true)
		case "WKST":
			d, ok := dayByName(val)
			if !ok {
				err = unsupported("bad WKST")
			}
			r.WeekStart = d
		default:
			err = unsupported("part %s", safe(name))
		}
		if err != nil {
			return Rule{}, err
		}
	}
	if r.Freq == 0 {
		return Rule{}, unsupported("no FREQ")
	}
	if err := r.validate(); err != nil {
		return Rule{}, err
	}
	return r, nil
}

func (r *Rule) parseFreq(val string) error {
	for f, n := range freqNames {
		if n == val {
			r.Freq = f
			return nil
		}
	}
	return unsupported("FREQ %s", safe(val))
}

func (r *Rule) parseUntil(val string) error {
	var layout string
	switch {
	case len(val) == 8:
		layout, r.UntilKind = "20060102", UntilDate
	case len(val) == 16 && val[15] == 'Z':
		layout, r.UntilKind = "20060102T150405Z", UntilUTC
	case len(val) == 15:
		layout, r.UntilKind = "20060102T150405", UntilFloating
	default:
		return unsupported("bad UNTIL")
	}
	for i := 0; i < 8; i++ {
		if val[i] < '0' || val[i] > '9' {
			return unsupported("bad UNTIL")
		}
	}
	t, err := time.ParseInLocation(layout, val, time.UTC)
	if err != nil {
		return unsupported("bad UNTIL")
	}
	if t.Year() < MinYear || t.Year() > MaxUntilYear {
		return unsupported("UNTIL year %d outside %d–%d", t.Year(), MinYear, MaxUntilYear)
	}
	r.Until = t
	return nil
}

// number parses a plain decimal in [lo, hi].
func number(s string, lo, hi int) (int, error) {
	n, err := signed(s)
	if err != nil || n < lo || n > hi {
		return 0, unsupported("value out of %d–%d", lo, hi)
	}
	return n, nil
}

func signed(s string) (int, error) {
	if s == "" || len(s) > 7 {
		return 0, errors.New("bad number")
	}
	body := s
	if body[0] == '+' || body[0] == '-' {
		body = body[1:]
	}
	if body == "" {
		return 0, errors.New("bad number")
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, errors.New("bad number")
		}
	}
	return strconv.Atoi(s)
}

// intList parses a BYxxx list in lo..hi, and also -hi..-lo when
// negative is set; the result is sorted without duplicates.
func intList(s string, lo, hi int, negative bool) ([]int, error) {
	items := strings.Split(s, ",")
	if len(items) > MaxListLen {
		return nil, unsupported("list longer than %d", MaxListLen)
	}
	set := map[int]bool{}
	for _, it := range items {
		n, err := signed(it)
		ok := err == nil && (n >= lo && n <= hi || negative && n <= -lo && n >= -hi)
		if !ok {
			return nil, unsupported("list value out of range")
		}
		set[n] = true
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

func dayList(s string) ([]WeekdayNum, error) {
	items := strings.Split(s, ",")
	if len(items) > MaxListLen {
		return nil, unsupported("list longer than %d", MaxListLen)
	}
	set := map[WeekdayNum]bool{}
	for _, it := range items {
		if len(it) < 2 {
			return nil, unsupported("bad BYDAY")
		}
		d, ok := dayByName(it[len(it)-2:])
		if !ok {
			return nil, unsupported("bad BYDAY")
		}
		n := 0
		if ord := it[:len(it)-2]; ord != "" {
			v, err := signed(ord)
			if err != nil || v == 0 || v > 53 || v < -53 {
				return nil, unsupported("bad BYDAY ordinal")
			}
			n = v
		}
		set[WeekdayNum{N: n, Day: d}] = true
	}
	out := make([]WeekdayNum, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sortDays(out)
	return out, nil
}

// sortDays orders BYDAY entries by ordinal (every day first, then
// positive, then negative) and weekday from Monday.
func sortDays(days []WeekdayNum) {
	key := func(w WeekdayNum) (int, int) {
		n := w.N
		switch {
		case n == 0:
			n = -1000
		case n < 0:
			n = 1000 - n
		}
		return n, (int(w.Day) + 6) % 7
	}
	sort.Slice(days, func(i, j int) bool {
		a1, a2 := key(days[i])
		b1, b2 := key(days[j])
		if a1 != b1 {
			return a1 < b1
		}
		return a2 < b2
	})
}

// validate checks the RFC 5545 restrictions between parts and the
// static impossible filters (§3.3).
func (r *Rule) validate() error {
	if r.Freq < Yearly || r.Freq > Hourly {
		return unsupported("FREQ")
	}
	if r.Interval < 1 || r.Interval > MaxInterval || r.Count < 0 || r.Count > MaxCount {
		return unsupported("INTERVAL or COUNT out of range")
	}
	if r.Count > 0 && r.UntilKind != UntilNone {
		return unsupported("COUNT and UNTIL together")
	}
	lists := [][]int{r.BySecond, r.ByMinute, r.ByHour, r.ByMonthDay, r.ByYearDay, r.ByWeekNo, r.ByMonth, r.BySetPos}
	for _, l := range lists {
		if len(l) > MaxListLen {
			return unsupported("list longer than %d", MaxListLen)
		}
	}
	if len(r.ByDay) > MaxListLen {
		return unsupported("list longer than %d", MaxListLen)
	}
	if len(r.ByWeekNo) > 0 && r.Freq != Yearly {
		return unsupported("BYWEEKNO only with YEARLY")
	}
	if len(r.ByYearDay) > 0 && (r.Freq == Monthly || r.Freq == Weekly || r.Freq == Daily) {
		return unsupported("BYYEARDAY with %s", r.Freq)
	}
	if len(r.ByMonthDay) > 0 && r.Freq == Weekly {
		return unsupported("BYMONTHDAY with WEEKLY")
	}
	for _, d := range r.ByDay {
		if d.N == 0 {
			continue
		}
		switch {
		case r.Freq != Monthly && r.Freq != Yearly:
			return unsupported("BYDAY ordinal with %s", r.Freq)
		case r.Freq == Yearly && len(r.ByWeekNo) > 0:
			return unsupported("BYDAY ordinal with BYWEEKNO")
		case (r.Freq == Monthly || len(r.ByMonth) > 0) && (d.N > 5 || d.N < -5):
			return impossible("BYDAY ordinal %d within a month", d.N)
		}
	}
	if len(r.BySetPos) > 0 && len(r.BySecond)+len(r.ByMinute)+len(r.ByHour)+len(r.ByDay)+
		len(r.ByMonthDay)+len(r.ByYearDay)+len(r.ByWeekNo)+len(r.ByMonth) == 0 {
		return unsupported("BYSETPOS without another BYxxx")
	}
	return r.checkPossible()
}

// monthLen is the longest length of month m (February 29).
func monthLen(m int) int {
	switch m {
	case 2:
		return 29
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

var monthStart = [13]int{0, 0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335} // leap year, 0-based day of the 1st

// checkPossible refuses filter combinations that can never match.
func (r *Rule) checkPossible() error {
	months := r.ByMonth
	if len(months) == 0 {
		months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	if len(r.ByMonthDay) > 0 {
		fits := false
		for _, d := range r.ByMonthDay {
			for _, m := range months {
				if d <= monthLen(m) && -d <= monthLen(m) {
					fits = true
				}
			}
		}
		if !fits {
			return impossible("BYMONTHDAY never in BYMONTH")
		}
	}
	if len(r.ByYearDay) > 0 && len(r.ByMonth) > 0 {
		fits := false
		for _, yd := range r.ByYearDay {
			for _, leap := range []bool{false, true} {
				if m := monthOfYearDay(yd, leap); m != 0 && contains(r.ByMonth, m) {
					fits = true
				}
			}
		}
		if !fits {
			return impossible("BYYEARDAY never in BYMONTH")
		}
	}
	if len(r.BySetPos) > 0 {
		size := r.maxSetSize()
		fits := false
		for _, p := range r.BySetPos {
			if p <= size && -p <= size {
				fits = true
			}
		}
		if !fits {
			return impossible("BYSETPOS beyond a set of at most %d", size)
		}
	}
	return nil
}

// monthOfYearDay is the month (1–12) of a ±day of the year, 0 if the
// year has no such day.
func monthOfYearDay(yd int, leap bool) int {
	length := 365
	if leap {
		length = 366
	}
	if yd < 0 {
		yd = length + 1 + yd
	}
	if yd < 1 || yd > length {
		return 0
	}
	day := yd - 1 // 0-based
	for m := 12; m >= 1; m-- {
		start := monthStart[m]
		if !leap && m > 2 {
			start--
		}
		if day >= start {
			return m
		}
	}
	return 1
}

// maxSetSize is an upper bound of the instances in one period of the
// rule (the set BYSETPOS indexes).
func (r *Rule) maxSetSize() int {
	days := map[Frequency]int{Yearly: 366, Monthly: 31, Weekly: 7, Daily: 1, Hourly: 1}[r.Freq]
	monthsInPeriod := 1
	if r.Freq == Yearly {
		monthsInPeriod = 12
		if len(r.ByMonth) > 0 {
			monthsInPeriod = len(r.ByMonth)
		}
	}
	bound := func(n int) {
		if n < days {
			days = n
		}
	}
	if len(r.ByMonthDay) > 0 {
		bound(len(r.ByMonthDay) * monthsInPeriod)
	}
	if len(r.ByYearDay) > 0 {
		bound(len(r.ByYearDay))
	}
	if len(r.ByWeekNo) > 0 {
		bound(len(r.ByWeekNo) * 7)
	}
	if len(r.ByDay) > 0 {
		n := 0
		for _, d := range r.ByDay {
			switch {
			case d.N != 0:
				n += monthsInPeriod
			case r.Freq == Yearly && len(r.ByMonth) == 0:
				n += 53
			case r.Freq == Yearly || r.Freq == Monthly:
				n += 5 * monthsInPeriod
			default:
				n++
			}
		}
		bound(n)
	}
	if r.Freq == Yearly && len(r.ByMonth) > 0 {
		bound(31 * len(r.ByMonth))
	}
	times := 1
	if r.Freq != Hourly && len(r.ByHour) > 0 {
		times *= len(r.ByHour)
	}
	if len(r.ByMinute) > 0 {
		times *= len(r.ByMinute)
	}
	if len(r.BySecond) > 0 {
		times *= len(r.BySecond)
	}
	return days * times
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// safe bounds a hostile token quoted in an error.
func safe(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	return strconv.Quote(s)
}

// Normalise returns the canonical RRULE value of r (without "RRULE:"):
// parts in a fixed order, sorted lists, INTERVAL only when > 1, WKST
// only when it is not Monday.
func Normalise(r Rule) string {
	var b strings.Builder
	b.WriteString("FREQ=" + r.Freq.String())
	if r.Interval > 1 {
		b.WriteString(";INTERVAL=" + strconv.Itoa(r.Interval))
	}
	if r.Count > 0 {
		b.WriteString(";COUNT=" + strconv.Itoa(r.Count))
	}
	switch r.UntilKind {
	case UntilDate:
		b.WriteString(";UNTIL=" + r.Until.Format("20060102"))
	case UntilUTC:
		b.WriteString(";UNTIL=" + r.Until.UTC().Format("20060102T150405Z"))
	case UntilFloating:
		b.WriteString(";UNTIL=" + r.Until.Format("20060102T150405"))
	}
	list := func(name string, l []int) {
		if len(l) == 0 {
			return
		}
		s := append([]int(nil), l...)
		sort.Ints(s)
		b.WriteString(";" + name + "=")
		for i, n := range s {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(n))
		}
	}
	list("BYSECOND", r.BySecond)
	list("BYMINUTE", r.ByMinute)
	list("BYHOUR", r.ByHour)
	if len(r.ByDay) > 0 {
		days := append([]WeekdayNum(nil), r.ByDay...)
		sortDays(days)
		b.WriteString(";BYDAY=")
		for i, d := range days {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(dayString(d))
		}
	}
	list("BYMONTHDAY", r.ByMonthDay)
	list("BYYEARDAY", r.ByYearDay)
	list("BYWEEKNO", r.ByWeekNo)
	list("BYMONTH", r.ByMonth)
	list("BYSETPOS", r.BySetPos)
	if r.WeekStart != time.Monday {
		b.WriteString(";WKST=" + dayNames[r.WeekStart%7])
	}
	return b.String()
}

func dayString(d WeekdayNum) string {
	if d.N == 0 {
		return dayNames[d.Day%7]
	}
	return strconv.Itoa(d.N) + dayNames[d.Day%7]
}
