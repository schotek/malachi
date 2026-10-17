// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// DefaultSlotStep is the step of FreeSlots' candidate starts.
const DefaultSlotStep = 15 * time.Minute

// MaxConflicts is how many conflicts an invitation card shows.
const MaxConflicts = 3

// ErrWorkingHours: working hours that do not read ("HH:MM", end after
// start, known days).
var ErrWorkingHours = errors.New("calendar: bad working hours")

// Busy reports whether an occurrence takes the user's time: not
// cancelled, not declined, not shown as free.
func Busy(o api.Occurrence) bool {
	return o.Status != api.EventStatusCancelled && o.MyResponse != api.ResponseDeclined && o.ShowAs != api.ShowAsFree
}

// span returns an occurrence's instants: timed from Start/End, all-day
// from its dates read in loc.
func span(o api.Occurrence, loc *time.Location) (time.Time, time.Time, bool) {
	if o.AllDay {
		s, ok1 := ParseDate(o.StartDate)
		e, ok2 := ParseDate(o.EndDate)
		if !ok1 || !ok2 {
			return time.Time{}, time.Time{}, false
		}
		return localize(s, loc).UTC(), localize(e, loc).UTC(), true
	}
	if o.Start == nil || o.End == nil {
		return time.Time{}, time.Time{}, false
	}
	return o.Start.UTC(), o.End.UTC(), true
}

// BusyIntervals returns the merged busy time of occurrences (those Busy
// accepts; all-day ones as their days in loc, nil = UTC).
func BusyIntervals(occs []api.Occurrence, loc *time.Location) []api.BusyInterval {
	if loc == nil {
		loc = time.UTC
	}
	var out []api.BusyInterval
	for _, o := range occs {
		if !Busy(o) {
			continue
		}
		s, e, ok := span(o, loc)
		if !ok || !e.After(s) {
			continue
		}
		show := o.ShowAs
		if show == "" {
			show = api.ShowAsBusy
		}
		out = append(out, api.BusyInterval{Start: s, End: e, ShowAs: show})
	}
	return MergeBusy(out)
}

// busyRank orders the states of overlapping busy time: the merged
// interval takes the strongest.
var busyRank = map[api.ShowAs]int{
	api.ShowAsUnknown: 1, api.ShowAsTentative: 2, api.ShowAsWorkingElsewhere: 3, api.ShowAsBusy: 4, api.ShowAsOOF: 5,
}

// MergeBusy sorts busy intervals and merges the ones that overlap or
// touch; free intervals and empty ones are dropped. Never nil.
func MergeBusy(in []api.BusyInterval) []api.BusyInterval {
	list := make([]api.BusyInterval, 0, len(in))
	for _, b := range in {
		if b.ShowAs != api.ShowAsFree && b.End.After(b.Start) {
			b.Start, b.End = b.Start.UTC(), b.End.UTC()
			list = append(list, b)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
	out := make([]api.BusyInterval, 0, len(list))
	for _, b := range list {
		if n := len(out); n > 0 && !b.Start.After(out[n-1].End) {
			last := &out[n-1]
			if b.End.After(last.End) {
				last.End = b.End
			}
			if busyRank[b.ShowAs] > busyRank[last.ShowAs] {
				last.ShowAs = b.ShowAs
			}
			continue
		}
		out = append(out, b)
	}
	return out
}

// workingDay is one day's working hours, minutes since midnight.
type workingDay struct{ start, end int }

func parseWorkingHours(wh *api.WorkingHours) (map[time.Weekday]workingDay, error) {
	if wh == nil {
		return nil, nil
	}
	clock := func(s string) (int, bool) {
		h, m, ok := strings.Cut(s, ":")
		hh, err1 := strconv.Atoi(h)
		mm, err2 := strconv.Atoi(m)
		if !ok || err1 != nil || err2 != nil || len(h) != 2 || len(m) != 2 || hh < 0 || hh > 24 || mm < 0 || mm > 59 || hh == 24 && mm != 0 {
			return 0, false
		}
		return hh*60 + mm, true
	}
	s, ok1 := clock(wh.Start)
	e, ok2 := clock(wh.End)
	if !ok1 || !ok2 || e <= s || len(wh.Days) == 0 {
		return nil, ErrWorkingHours
	}
	days := map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}
	out := map[time.Weekday]workingDay{}
	for _, d := range wh.Days {
		wd, ok := days[strings.ToUpper(d)]
		if !ok {
			return nil, ErrWorkingHours
		}
		out[wd] = workingDay{s, e}
	}
	return out, nil
}

// FreeSlots returns free slots of the given duration in [from, to): their
// starts lie on multiples of step (≤ 0: DefaultSlotStep) of loc's wall
// clock (nil = UTC), each lies wholly inside the working hours of its day
// (wh nil = any time) and overlaps no busy interval; a slot starts no
// earlier than the end of the previous one, so slots never overlap. At
// most limit (≤ 0 or over api.MaxFreeSlots: api.MaxFreeSlots).
func FreeSlots(busy []api.BusyInterval, from, to time.Time, duration time.Duration, wh *api.WorkingHours, loc *time.Location, step time.Duration, limit int) ([]api.TimeSlot, error) {
	if loc == nil {
		loc = time.UTC
	}
	if step <= 0 {
		step = DefaultSlotStep
	}
	if limit <= 0 || limit > api.MaxFreeSlots {
		limit = api.MaxFreeSlots
	}
	hours, err := parseWorkingHours(wh)
	if err != nil {
		return nil, err
	}
	out := []api.TimeSlot{}
	if duration <= 0 || !from.Before(to) {
		return out, nil
	}
	busy = MergeBusy(busy)
	// The first candidate: from rounded up to a step of the local clock.
	l := from.In(loc)
	mid := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	cand := mid
	for cand.Before(from) {
		cand = cand.Add(step)
	}
	bi := 0
	for guard := 0; cand.Add(duration).Compare(to) <= 0 && len(out) < limit && guard < 1_000_000; guard++ {
		end := cand.Add(duration)
		if hours != nil {
			lc := cand.In(loc)
			day, ok := hours[lc.Weekday()]
			dayStart := time.Date(lc.Year(), lc.Month(), lc.Day(), 0, 0, 0, 0, loc)
			ws, we := dayStart.Add(time.Duration(day.start)*time.Minute), dayStart.Add(time.Duration(day.end)*time.Minute)
			switch {
			case !ok || !cand.Before(we) || end.After(we):
				cand = nextStep(dayStart.AddDate(0, 0, 1), step, loc)
				continue
			case cand.Before(ws):
				cand = nextStep(ws, step, loc)
				continue
			}
		}
		for bi < len(busy) && !busy[bi].End.After(cand) {
			bi++
		}
		if bi < len(busy) && busy[bi].Start.Before(end) {
			cand = nextStep(busy[bi].End, step, loc)
			continue
		}
		out = append(out, api.TimeSlot{Start: cand.UTC(), End: end.UTC()})
		cand = nextStep(end, step, loc)
	}
	return out, nil
}

// nextStep rounds t up to a multiple of step of its local day.
func nextStep(t time.Time, step time.Duration, loc *time.Location) time.Time {
	l := t.In(loc)
	c := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	for c.Before(t) {
		c = c.Add(step)
	}
	return c
}

// Conflicts returns the occurrences that overlap [start, end) and take
// the user's time (Busy), by start, at most max (≤ 0: MaxConflicts).
// All-day occurrences are read in loc (nil = UTC). The caller leaves out
// the event the slot belongs to.
func Conflicts(start, end time.Time, occs []api.Occurrence, loc *time.Location, max int) []api.Occurrence {
	if loc == nil {
		loc = time.UTC
	}
	if max <= 0 {
		max = MaxConflicts
	}
	type hit struct {
		o api.Occurrence
		s time.Time
	}
	var hits []hit
	for _, o := range occs {
		if !Busy(o) {
			continue
		}
		s, e, ok := span(o, loc)
		if !ok {
			continue
		}
		if s.Before(end) && (e.After(start) || e.Equal(s) && !s.Before(start)) {
			hits = append(hits, hit{o, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].s.Before(hits[j].s) })
	out := make([]api.Occurrence, 0, min(len(hits), max))
	for i := 0; i < len(hits) && i < max; i++ {
		out = append(out, hits[i].o)
	}
	return out
}
