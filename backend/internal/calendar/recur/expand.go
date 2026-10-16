// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package recur

import (
	"sort"
	"time"

	"github.com/teambition/rrule-go"
)

// Window is a half-open query window [From, To).
type Window struct {
	From, To time.Time
}

// earliest and latest DTSTART of an expandable series (§3.3).
var (
	minStart = time.Date(MinYear, 1, 1, 0, 0, 0, 0, time.UTC)
	maxStart = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
)

// cycleYears is the Gregorian cycle: dates 400 years apart have the same
// weekday, leap status and ISO week numbers.
const cycleYears = 400

// rruleMaxYear is the year after which rrule-go's iterator gives up.
const rruleMaxYear = 9999

// CheckStart reports whether r can be expanded from dtstart in loc: the
// local DTSTART lies in 1900-01-01..2200-01-01, BYMINUTE and BYSECOND
// hold only 0 or DTSTART's minute and second, and an HOURLY rule's
// BYHOUR contains an hour its INTERVAL reaches from DTSTART.
func CheckStart(r Rule, dtstart time.Time, loc *time.Location) error {
	if loc == nil {
		loc = time.UTC
	}
	w := wall(dtstart.In(loc))
	if w.Before(minStart) || w.After(maxStart) {
		return unsupported("DTSTART outside %d–2200", MinYear)
	}
	for _, m := range r.ByMinute {
		if m != 0 && m != w.Minute() {
			return unsupported("BYMINUTE other than 0 or DTSTART's minute")
		}
	}
	for _, s := range r.BySecond {
		if s != 0 && s != w.Second() {
			return unsupported("BYSECOND other than 0 or DTSTART's second")
		}
	}
	if r.Freq == Hourly && len(r.ByHour) > 0 {
		g := gcd(r.Interval, 24)
		reach := false
		for _, h := range r.ByHour {
			if (h-w.Hour())%g == 0 {
				reach = true
			}
		}
		if !reach {
			return impossible("BYHOUR never reached by INTERVAL")
		}
	}
	return nil
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// Expand returns the start instants of r's instances whose start lies in
// win, in order, computed from dtstart in the zone loc (nil = UTC): the
// rule runs on loc's wall clock (time.Date in loc), a local time in a
// spring-forward gap moves forward by the gap and a repeated local time
// takes its first occurrence (RFC 5545 §3.3.10). Instances are returned
// in loc. DTSTART counts as an instance only when it matches the rule
// (RFC 5545 leaves an unsynchronised DTSTART undefined; this follows
// python-dateutil, whose port rrule-go is).
//
// At most limit instances are returned (limit ≤ 0 = MaxOccurrences);
// truncated is true when the expansion stopped at limit, at
// MaxHourlyCandidates for HOURLY, or at the internal candidate budget,
// with more instances possibly left in the window. A rule rrule-go
// cannot expand (a panic) is ErrUnsupported, as is everything
// CheckStart refuses.
func Expand(r Rule, dtstart time.Time, loc *time.Location, win Window, limit int) (out []time.Time, truncated bool, err error) {
	if loc == nil {
		loc = time.UTC
	}
	if err := r.validate(); err != nil {
		return nil, false, err
	}
	if err := CheckStart(r, dtstart, loc); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = MaxOccurrences
	}
	if !win.From.Before(win.To) {
		return nil, false, nil
	}
	dtWall := wall(dtstart.In(loc))
	fromWall := wall(win.From.In(loc))
	toWall := wall(win.To.In(loc))
	if toWall.After(maxStart.AddDate(2, 0, 0)) {
		// Nothing a client asks for lies beyond 2201; keeping the end
		// there bounds the iteration.
		toWall = maxStart.AddDate(2, 0, 0)
	}
	if !toWall.After(dtWall.Add(-48 * time.Hour)) {
		return nil, false, nil
	}

	// The wall-clock limit handed to rrule-go: a day past the window's
	// end and the rule's UNTIL (margins for zone offsets; the exact
	// comparison follows below).
	limitWall := toWall.Add(24 * time.Hour)
	switch r.UntilKind {
	case UntilUTC:
		if u := wall(r.Until.In(loc)).Add(24 * time.Hour); u.Before(limitWall) {
			limitWall = u
		}
	case UntilDate, UntilFloating:
		if u := r.Until.Add(48 * time.Hour); u.Before(limitWall) {
			limitWall = u
		}
	}
	if limitWall.Before(dtWall) {
		return nil, false, nil
	}

	opt := r.option(dtWall)
	if r.Count == 0 {
		opt.Dtstart = fastForward(r, dtWall, fromWall)
	}
	// Run rrule-go whole 400-year cycles later, so that a rule whose
	// filters never match (in a way the static check cannot see, e.g.
	// DAILY;INTERVAL=7;BYDAY=TU from a Monday) stops at rrule-go's year
	// 9999 shortly after the window instead of 8000 years later.
	shift := (rruleMaxYear - 1 - limitWall.Year()) / cycleYears * cycleYears
	if shift < 0 {
		shift = 0
	}
	opt.Dtstart = opt.Dtstart.AddDate(shift, 0, 0)
	opt.Until = limitWall.AddDate(shift, 0, 0)

	defer func() {
		if p := recover(); p != nil {
			out, truncated, err = nil, false, unsupported("expansion failed (%T)", p)
		}
	}()
	rr, err := rrule.NewRRule(opt)
	if err != nil {
		return nil, false, unsupported("rrule: %v", err)
	}
	next := rr.Iterator()
	budget := maxCandidates
	if r.Freq == Hourly {
		budget = MaxHourlyCandidates
	}
	for n := 0; ; n++ {
		t, ok := next()
		if !ok {
			return out, false, nil
		}
		if n >= budget {
			return out, true, nil
		}
		w := t.AddDate(-shift, 0, 0)
		if r.pastUntil(w, loc) {
			return out, false, nil
		}
		inst := localize(w, loc)
		if !inst.Before(win.To) {
			return out, false, nil
		}
		if inst.Before(win.From) {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, inst)
	}
}

// option builds the rrule-go options of r on the wall clock (UTC fields)
// with every default rrule-go would derive from DTSTART made explicit,
// so that DTSTART can be moved by fastForward.
func (r Rule) option(dt time.Time) rrule.ROption {
	o := rrule.ROption{
		Freq:       map[Frequency]rrule.Frequency{Yearly: rrule.YEARLY, Monthly: rrule.MONTHLY, Weekly: rrule.WEEKLY, Daily: rrule.DAILY, Hourly: rrule.HOURLY}[r.Freq],
		Dtstart:    dt,
		Interval:   r.Interval,
		Wkst:       weekday(WeekdayNum{Day: r.WeekStart}),
		Count:      r.Count,
		Bysetpos:   clone(r.BySetPos),
		Bymonth:    clone(r.ByMonth),
		Bymonthday: clone(r.ByMonthDay),
		Byyearday:  clone(r.ByYearDay),
		Byweekno:   clone(r.ByWeekNo),
		Byhour:     clone(r.ByHour),
		Byminute:   clone(r.ByMinute),
		Bysecond:   clone(r.BySecond),
	}
	for _, d := range r.ByDay {
		o.Byweekday = append(o.Byweekday, weekday(d))
	}
	if len(o.Byweekno) == 0 && len(o.Byyearday) == 0 && len(o.Bymonthday) == 0 && len(o.Byweekday) == 0 {
		switch r.Freq {
		case Yearly:
			if len(o.Bymonth) == 0 {
				o.Bymonth = []int{int(dt.Month())}
			}
			o.Bymonthday = []int{dt.Day()}
		case Monthly:
			o.Bymonthday = []int{dt.Day()}
		case Weekly:
			o.Byweekday = []rrule.Weekday{weekday(WeekdayNum{Day: dt.Weekday()})}
		}
	}
	if len(o.Byhour) == 0 && r.Freq != Hourly {
		o.Byhour = []int{dt.Hour()}
	}
	if len(o.Byminute) == 0 {
		o.Byminute = []int{dt.Minute()}
	}
	if len(o.Bysecond) == 0 {
		o.Bysecond = []int{dt.Second()}
	}
	return o
}

func clone(l []int) []int {
	if len(l) == 0 {
		return nil
	}
	return append([]int(nil), l...)
}

func weekday(d WeekdayNum) rrule.Weekday {
	days := [7]rrule.Weekday{rrule.SU, rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA}
	w := days[d.Day%7]
	if d.N != 0 {
		return w.Nth(d.N)
	}
	return w
}

// fastForward moves a rule without COUNT forward by whole intervals to
// at least one interval before the window, so that a series started in
// 1900 is not iterated through a century of instances. For MONTHLY and
// YEARLY the new start is the beginning of its period (the defaults of
// the old DTSTART are explicit in the options).
func fastForward(r Rule, dt, from time.Time) time.Time {
	if !from.After(dt) {
		return dt
	}
	n := r.Interval
	whole := func(periods int) int { // intervals to skip, keeping one in hand
		q := periods/n - 1
		if q < 0 {
			return 0
		}
		return q
	}
	secs := from.Unix() - dt.Unix()
	switch r.Freq {
	case Hourly:
		q := whole(int(secs / 3600))
		return dt.Add(time.Duration(q*n) * time.Hour)
	case Daily:
		q := whole(int(secs / 86400))
		return dt.AddDate(0, 0, q*n)
	case Weekly:
		q := whole(int(secs / (7 * 86400)))
		return dt.AddDate(0, 0, 7*q*n)
	case Monthly:
		q := whole((from.Year()-dt.Year())*12 + int(from.Month()) - int(dt.Month()))
		if q == 0 {
			return dt
		}
		return time.Date(dt.Year(), dt.Month()+time.Month(q*n), 1, 0, 0, 0, 0, time.UTC)
	case Yearly:
		q := whole(from.Year() - dt.Year())
		if q == 0 {
			return dt
		}
		return time.Date(dt.Year()+q*n, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	return dt
}

// pastUntil compares an instance's wall clock to the rule's UNTIL.
func (r Rule) pastUntil(w time.Time, loc *time.Location) bool {
	switch r.UntilKind {
	case UntilUTC:
		return localize(w, loc).After(r.Until)
	case UntilDate:
		y, m, d := w.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).After(r.Until)
	case UntilFloating:
		return w.After(r.Until)
	}
	return false
}

// wall returns t's wall clock as a time in UTC.
func wall(t time.Time) time.Time {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

// localize turns a wall clock (UTC fields) into the instant of loc: a
// time in a spring-forward gap moves forward by the gap, a repeated
// time takes the earlier instant.
func localize(w time.Time, loc *time.Location) time.Time {
	if loc == time.UTC {
		return w
	}
	u := w.Unix()
	var best time.Time
	for _, probe := range []int64{u - 86400, u, u + 86400} {
		_, off := time.Unix(probe, 0).In(loc).Zone()
		c := time.Unix(u-int64(off), 0).In(loc)
		if _, o := c.Zone(); o == off && (best.IsZero() || c.Before(best)) {
			best = c
		}
	}
	if !best.IsZero() {
		return best
	}
	// A gap: the offset before it applies, which moves the wall clock
	// forward by the gap.
	_, before := time.Unix(u-86400, 0).In(loc).Zone()
	return time.Unix(u-int64(before), 0).In(loc)
}

// ApplyExceptions returns instances with rdates added and exdates
// removed (compared as instants), sorted and without duplicates. The
// caller passes rdates already limited to the query window. More than
// MaxRDates RDATEs or MaxEXDates EXDATEs are ErrUnsupported.
func ApplyExceptions(instances, rdates, exdates []time.Time) ([]time.Time, error) {
	if len(rdates) > MaxRDates {
		return nil, unsupported("more than %d RDATE", MaxRDates)
	}
	if len(exdates) > MaxEXDates {
		return nil, unsupported("more than %d EXDATE", MaxEXDates)
	}
	ex := make(map[int64]bool, len(exdates))
	for _, t := range exdates {
		ex[t.UnixNano()] = true
	}
	seen := make(map[int64]bool, len(instances)+len(rdates))
	out := make([]time.Time, 0, len(instances)+len(rdates))
	for _, list := range [][]time.Time{instances, rdates} {
		for _, t := range list {
			k := t.UnixNano()
			if ex[k] || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}
