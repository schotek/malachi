// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"errors"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Window is a half-open query window [From, To) of instants.
type Window struct {
	From, To time.Time
}

// Occurrence is one materialised instance (a cal_occurrences row) of an
// Object.
type Occurrence struct {
	// Key is the occurrence key: "" for a single event, else the
	// recurrence id (Event.KeyOf of the instance's original start).
	Key api.DateOrTime
	// RecurrenceID is the same as Key ("" for a single event); kept as a
	// separate name for the api.Occurrence it fills.
	RecurrenceID api.DateOrTime
	// Start and End: UTC instants of a timed instance; of an all-day or
	// floating one the wall clock as if UTC (cal_occurrences: "the date's
	// 00:00 as if UTC").
	Start, End time.Time
	AllDay     bool
	Floating   bool
	// Override is the index in Object.Overrides of the event whose fields
	// show, -1 for the master (or single event).
	Override int
	// Series: the instance belongs to a series (series_id is the master's
	// id; an override without a stored master has none).
	Series bool
	// RemoteID is the source's id of the instance (Graph occurrence id).
	RemoteID string
}

// EventOf returns the event whose fields an occurrence shows.
func (o *Object) EventOf(occ Occurrence) *Event {
	if occ.Override >= 0 && occ.Override < len(o.Overrides) {
		return &o.Overrides[occ.Override]
	}
	return o.Master
}

// Occurrences expands the object into the instances that overlap win, in
// order of start, at most limit (≤ 0: api.MaxSeriesOccurrences);
// truncated says that more instances may lie in the window.
//
// A master is expanded with recur (instances whose start lies in
// [win.From − duration, win.To), so that an instance that began before
// the window and still runs is included), DTSTART counted as the first
// instance (RFC 5545 §3.8.5.3), RDATEs added and EXDATEs removed; an
// instance an override replaces is dropped and every override is shown
// by itself (also one whose instance the rule does not produce or an
// EXDATE removed, and one without a master). Cancelled events and
// instances are not returned. A master whose rule was refused (RecurOK
// false) shows its first instance only. Timed instances are computed in
// the event's zone (DST as recur does it); all-day and floating instances
// on the wall clock, and they overlap the window when their wall clock
// overlaps the window's wall clock in loc (nil = UTC). An event without a
// start is not shown.
func Occurrences(obj Object, win Window, loc *time.Location, limit int) (out []Occurrence, truncated bool, err error) {
	if loc == nil {
		loc = time.UTC
	}
	if limit <= 0 || limit > api.MaxSeriesOccurrences {
		limit = api.MaxSeriesOccurrences
	}
	if !win.From.Before(win.To) {
		return nil, false, nil
	}
	m := obj.Master
	replaced := map[api.DateOrTime]bool{}
	for _, ov := range obj.Overrides {
		replaced[ov.RecurrenceID] = true
	}
	if m != nil && !m.Start.IsZero() && m.Status != api.EventStatusCancelled {
		occs, trunc, err := expandMaster(m, win, loc, limit)
		if err != nil {
			return nil, false, err
		}
		truncated = trunc
		for _, o := range occs {
			if !replaced[o.Key] || o.Key == "" {
				out = append(out, o)
			}
		}
	}
	for i := range obj.Overrides {
		ov := &obj.Overrides[i]
		if ov.Start.IsZero() || ov.Status == api.EventStatusCancelled {
			continue
		}
		o := Occurrence{
			Key: ov.RecurrenceID, RecurrenceID: ov.RecurrenceID,
			Start: ov.Start, End: ov.End, AllDay: ov.AllDay, Floating: ov.Floating,
			Override: i, Series: m != nil,
		}
		if overlaps(o, win, loc) {
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out, truncated = out[:limit], true
	}
	return out, truncated, nil
}

// wallWindow is the window as wall clocks in loc (for all-day and
// floating instances).
func wallWindow(win Window, loc *time.Location) Window {
	return Window{From: wallOf(win.From.In(loc)), To: wallOf(win.To.In(loc))}
}

func overlaps(o Occurrence, win Window, loc *time.Location) bool {
	if o.AllDay || o.Floating {
		win = wallWindow(win, loc)
	}
	if o.End.After(o.Start) {
		return o.Start.Before(win.To) && o.End.After(win.From)
	}
	return !o.Start.Before(win.From) && o.Start.Before(win.To)
}

// expandMaster returns the master's instances overlapping win.
func expandMaster(m *Event, win Window, loc *time.Location, limit int) ([]Occurrence, bool, error) {
	length := m.End.Sub(m.Start)
	mk := func(start time.Time, key api.DateOrTime) Occurrence {
		end := start.Add(length)
		if m.AllDay {
			end = start.AddDate(0, 0, int(length.Hours()+12)/24)
		}
		return Occurrence{Key: key, RecurrenceID: key, Start: start.UTC(), End: end.UTC(), AllDay: m.AllDay, Floating: m.Floating, Override: -1, Series: m.Kind == KindMaster}
	}
	recurring := m.Kind == KindMaster && m.RecurOK && (m.RRule != "" || len(m.RDates) > 0)
	if !recurring {
		key := api.DateOrTime("")
		if m.Kind == KindMaster {
			key = m.KeyOf(m.Start)
		}
		o := mk(m.Start, key)
		if overlaps(o, win, loc) {
			return []Occurrence{o}, false, nil
		}
		return nil, false, nil
	}
	// The expansion window, in the instants recur works with.
	ew := win
	eloc := m.Loc()
	if m.AllDay || m.Floating {
		ew, eloc = wallWindow(win, loc), time.UTC
	}
	from := ew.From.Add(-length)
	if m.AllDay {
		from = ew.From.AddDate(0, 0, -int(length.Hours()+12)/24)
	}
	var starts []time.Time
	truncated := false
	if m.RRule != "" {
		rule, err := recur.Parse(m.RRule)
		if err != nil {
			return nil, false, err
		}
		dt := m.Start
		if !m.AllDay && !m.Floating {
			dt = m.Start.In(eloc)
		}
		got, trunc, err := recur.Expand(rule, dt, eloc, recur.Window{From: from, To: ew.To}, limit)
		if err != nil {
			if errors.Is(err, recur.ErrUnsupported) {
				// A rule that cannot be expanded shows its first instance.
				o := mk(m.Start, m.KeyOf(m.Start))
				if overlaps(o, win, loc) {
					return []Occurrence{o}, false, nil
				}
				return nil, false, nil
			}
			return nil, false, err
		}
		starts, truncated = got, trunc
	}
	var extra []time.Time
	if !m.Start.Before(from) && m.Start.Before(ew.To) {
		extra = append(extra, m.Start)
	}
	for _, t := range m.RDates {
		if !t.Before(from) && t.Before(ew.To) {
			extra = append(extra, t)
		}
	}
	all, err := recur.ApplyExceptions(starts, extra, m.EXDates)
	if err != nil {
		return nil, false, err
	}
	var out []Occurrence
	for _, s := range all {
		o := mk(s, m.KeyOf(s))
		if overlaps(o, win, loc) {
			out = append(out, o)
		}
		if len(out) == limit {
			return out, true, nil
		}
	}
	return out, truncated, nil
}

// HasInstance reports whether the master's series has an instance with
// that recurrence id (the rule, DTSTART and RDATEs, without EXDATEs).
func HasInstance(m *Event, key api.DateOrTime) bool {
	t, ok := ParseKey(key)
	if !ok || m == nil || m.Start.IsZero() {
		return false
	}
	if t.Equal(m.Start) {
		return true
	}
	for _, r := range m.RDates {
		if r.Equal(t) {
			return true
		}
	}
	if m.RRule == "" || !m.RecurOK {
		return false
	}
	rule, err := recur.Parse(m.RRule)
	if err != nil {
		return false
	}
	eloc := m.Loc()
	dt := m.Start
	if m.AllDay || m.Floating {
		eloc = time.UTC
	} else {
		dt = m.Start.In(eloc)
	}
	got, _, err := recur.Expand(rule, dt, eloc, recur.Window{From: t.Add(-time.Second), To: t.Add(time.Second)}, 2)
	if err != nil {
		return false
	}
	for _, g := range got {
		if g.Equal(t) {
			return true
		}
	}
	return false
}

// APIOccurrence fills the fields of an api.Occurrence that the model
// knows (not EventID, SeriesID, CalendarID, AccountID, ReadOnly, Invitation
// and the privacy rule of Title, which belong to the store and the core).
// A floating instance's Start and End are its wall clock in loc (the
// caller's zone, §4.2; nil = UTC).
func (o *Object) APIOccurrence(occ Occurrence, loc *time.Location) api.Occurrence {
	if loc == nil {
		loc = time.UTC
	}
	ev := o.EventOf(occ)
	out := api.Occurrence{RecurrenceID: occ.RecurrenceID, AllDay: occ.AllDay, Floating: occ.Floating}
	if ev == nil {
		return out
	}
	switch {
	case occ.AllDay:
		out.StartDate, out.EndDate = api.Date(occ.Start.Format(keyDate)), api.Date(occ.End.Format(keyDate))
	case occ.Floating:
		s, e := localize(occ.Start, loc).UTC(), localize(occ.End, loc).UTC()
		out.Start, out.End = &s, &e
	default:
		s, e := occ.Start.UTC(), occ.End.UTC()
		out.Start, out.End = &s, &e
		out.TimeZone = api.TimeZone(ev.Zone)
	}
	out.Title, out.Location = ev.Title, ev.Location
	out.Status, out.ShowAs = ev.Status, ev.ShowAs
	out.MyResponse = ev.MyResponse
	out.Organizer = ev.IAmOrganizer
	out.AttendeeCount = ev.AttendeeCount
	out.Recurring = occ.Series
	out.VideoCall = ev.VideoCall != nil
	out.Private = ev.Private
	out.Alert = len(ev.Alarms) > 0
	return out
}
