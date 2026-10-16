// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Materialisation (docs/calendar.md §3.7): the conversion between the
// canonical model (calendar.Object) and the stored rows (store.Event,
// store.Occurrence), and the occurrences of an object in the window —
// the provider's own (Graph calendarView, Object.Occurrences) or the
// daemon's expansion (calendar.Occurrences) for every other source.

// StoredKey is the stored form of a recurrence id or occurrence key
// (store.FormatRecurrenceID): a date stays a date, an instant becomes
// UTC with milliseconds, so that equal instants are equal strings. A key
// that does not read is kept as it is.
func StoredKey(k api.DateOrTime) string {
	if k == "" {
		return ""
	}
	t, ok := calendar.ParseKey(k)
	if !ok {
		return string(k)
	}
	return store.FormatRecurrenceID(t, len(k) == len(time.DateOnly))
}

// ModelKey is the calendar package's form of a stored key
// ("2006-01-02" or "2006-01-02T15:04:05Z").
func ModelKey(s string) api.DateOrTime {
	if s == "" {
		return ""
	}
	t, ok := calendar.ParseKey(api.DateOrTime(s))
	if !ok {
		return api.DateOrTime(s)
	}
	if len(s) == len(time.DateOnly) {
		return api.DateOrTime(t.Format(time.DateOnly))
	}
	return api.DateOrTime(t.UTC().Format(time.RFC3339))
}

// StoreEvents converts an object into the rows ReplaceResource takes, in
// the order master (when present), then the overrides. ETag goes on every
// row, Raw on the first.
func StoreEvents(obj Object) []store.Event {
	evs := obj.Events()
	out := make([]store.Event, 0, len(evs))
	for i, ev := range evs {
		e := storeEvent(ev)
		e.RemoteID, e.ETag = obj.RemoteID, obj.ETag
		if i == 0 {
			e.Raw = obj.Raw
		}
		out = append(out, e)
	}
	return out
}

func storeEvent(ev *calendar.Event) store.Event {
	e := store.Event{
		UID:               ev.UID,
		Kind:              store.EventKind(ev.Kind),
		RecurrenceID:      StoredKey(ev.RecurrenceID),
		Sequence:          ev.Sequence,
		Title:             ev.Title,
		Location:          ev.Location,
		Notes:             ev.Notes,
		Links:             ev.Links,
		AllDay:            ev.AllDay,
		Floating:          ev.Floating,
		TimeZone:          ev.Zone,
		EndTimeZone:       ev.EndZone,
		RRule:             ev.RRule,
		Recurrence:        ev.Recurrence,
		RecurRefused:      ev.Kind == calendar.KindMaster && !ev.RecurOK,
		Status:            ev.Status,
		ShowAs:            ev.ShowAs,
		Private:           ev.Private,
		Organizer:         ev.Organizer,
		IAmOrganizer:      ev.IAmOrganizer,
		MyResponse:        ev.MyResponse,
		ResponseRequested: ev.ResponseRequested,
		NewTimeOK:         ev.NewTimeOK,
		AttendeeCount:     ev.AttendeeCount,
		Attendees:         ev.Attendees,
		Alarms:            ev.Alarms,
		OnlineProviders:   ev.OnlineMeetingProvider,
		ServerUpdated:     ev.Updated,
	}
	if e.Kind == "" {
		e.Kind = store.EventSingle
	}
	if ev.VideoCall != nil {
		e.VideoURL, e.VideoProvider = ev.VideoCall.URL, string(ev.VideoCall.Provider)
	}
	if ev.AllDay {
		e.StartDate, e.EndDate = string(ev.StartDate), string(ev.EndDate)
		if e.StartDate == "" && !ev.Start.IsZero() {
			e.StartDate = ev.Start.Format(time.DateOnly)
		}
		if e.EndDate == "" && !ev.End.IsZero() {
			e.EndDate = ev.End.Format(time.DateOnly)
		}
	} else {
		e.StartAt, e.EndAt = ev.Start, ev.End
	}
	for _, t := range ev.RDates {
		e.RDates = append(e.RDates, store.FormatRecurrenceID(t, ev.AllDay))
	}
	for _, t := range ev.EXDates {
		e.ExDates = append(e.ExDates, store.FormatRecurrenceID(t, ev.AllDay))
	}
	return e
}

// ObjectOf rebuilds the object of one remote resource from its stored
// rows (EventsByRemoteID, CalendarEvents grouped by remote id): the
// master or single row is Master, the rest Overrides in their order; Raw
// and ETag come from the first row. Occurrences stay empty.
func ObjectOf(rows []store.Event) Object {
	var obj Object
	for i := range rows {
		r := &rows[i]
		if i == 0 {
			obj.RemoteID, obj.ETag, obj.Raw = r.RemoteID, r.ETag, r.Raw
		}
		ev := modelEvent(r)
		if r.Kind == store.EventOverride {
			obj.Overrides = append(obj.Overrides, ev)
		} else if obj.Master == nil {
			m := ev
			obj.Master = &m
		}
	}
	return obj
}

func modelEvent(r *store.Event) calendar.Event {
	ev := calendar.Event{
		UID:                   r.UID,
		Kind:                  calendar.Kind(r.Kind),
		RecurrenceID:          ModelKey(r.RecurrenceID),
		Sequence:              r.Sequence,
		Title:                 r.Title,
		Location:              r.Location,
		Notes:                 r.Notes,
		Links:                 r.Links,
		OnlineMeetingProvider: r.OnlineProviders,
		AllDay:                r.AllDay,
		Floating:              r.Floating,
		Zone:                  r.TimeZone,
		EndZone:               r.EndTimeZone,
		RRule:                 r.RRule,
		Recurrence:            r.Recurrence,
		RecurOK:               !r.RecurRefused,
		Status:                r.Status,
		ShowAs:                r.ShowAs,
		Private:               r.Private,
		Organizer:             r.Organizer,
		IAmOrganizer:          r.IAmOrganizer,
		MyResponse:            r.MyResponse,
		ResponseRequested:     r.ResponseRequested,
		NewTimeOK:             r.NewTimeOK,
		Attendees:             r.Attendees,
		AttendeeCount:         r.AttendeeCount,
		Alarms:                r.Alarms,
		Updated:               r.ServerUpdated,
	}
	if r.VideoURL != "" {
		ev.VideoCall = &api.VideoCall{URL: r.VideoURL, Provider: api.VideoProvider(r.VideoProvider)}
	}
	if r.AllDay {
		ev.StartDate, ev.EndDate = api.Date(r.StartDate), api.Date(r.EndDate)
		ev.Start, _ = time.ParseInLocation(time.DateOnly, r.StartDate, time.UTC)
		ev.End, _ = time.ParseInLocation(time.DateOnly, r.EndDate, time.UTC)
	} else {
		ev.Start, ev.End = r.StartAt, r.EndAt
	}
	for _, s := range r.RDates {
		if t, ok := calendar.ParseKey(api.DateOrTime(s)); ok {
			ev.RDates = append(ev.RDates, t)
		}
	}
	for _, s := range r.ExDates {
		if t, ok := calendar.ParseKey(api.DateOrTime(s)); ok {
			ev.EXDates = append(ev.EXDates, t)
		}
	}
	return ev
}

// expandObject returns the occurrences of obj within win: the provider's
// own when it gave any, none for a provider-expanded source that gave
// none, else the daemon's expansion (all-day and floating instances
// matched on the wall clock of loc).
func expandObject(obj Object, win Window, loc *time.Location, daemon bool) ([]calendar.Occurrence, error) {
	if len(obj.Occurrences) > 0 {
		var out []calendar.Occurrence
		for _, o := range obj.Occurrences {
			if occursIn(o, win, loc) {
				out = append(out, o)
			}
		}
		return out, nil
	}
	if !daemon {
		return nil, nil
	}
	occ, _, err := calendar.Occurrences(obj, calendar.Window{From: win.From, To: win.To}, loc, 0)
	if err != nil {
		return nil, fmt.Errorf("expand %s: %w", obj.RemoteID, err)
	}
	return occ, nil
}

// occursIn reports whether a provider's occurrence overlaps win.
func occursIn(o calendar.Occurrence, win Window, loc *time.Location) bool {
	from, to := win.From, win.To
	if o.AllDay || o.Floating {
		from, to = wallUTC(from, loc), wallUTC(to, loc)
	}
	if o.End.After(o.Start) {
		return o.Start.Before(to) && o.End.After(from)
	}
	return !o.Start.Before(from) && o.Start.Before(to)
}

// wallUTC is t's wall clock in loc as if it were UTC.
func wallUTC(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

// storeOccurrences maps occurrences of an object onto its stored rows
// (in StoreEvents' order).
func storeOccurrences(obj Object, rows []store.Event, occ []calendar.Occurrence) []store.Occurrence {
	base := 0
	masterID := ""
	if obj.Master != nil && len(rows) > 0 {
		base, masterID = 1, rows[0].ID
	}
	out := make([]store.Occurrence, 0, len(occ))
	for _, o := range occ {
		idx := 0
		if o.Override >= 0 {
			idx = base + o.Override
		} else if obj.Master == nil {
			continue
		}
		if idx >= len(rows) {
			continue
		}
		so := store.Occurrence{
			EventID: rows[idx].ID, OccurrenceKey: StoredKey(o.Key),
			CalendarID: rows[idx].CalendarID, AccountID: rows[idx].AccountID,
			StartAt: o.Start, EndAt: o.End, AllDay: o.AllDay, Floating: o.Floating, RemoteID: o.RemoteID,
		}
		if o.Series && masterID != "" {
			so.SeriesID = masterID
		}
		out = append(out, so)
	}
	return out
}

// span is a changed time span; unbounded when nothing bounds it.
type span struct {
	from, to  time.Time
	unbounded bool
	empty     bool
}

func emptySpan() span { return span{empty: true} }

func (s *span) add(from, to time.Time) {
	if s.unbounded {
		return
	}
	if to.Before(from) {
		to = from
	}
	if s.empty {
		s.from, s.to, s.empty = from, to, false
		return
	}
	if from.Before(s.from) {
		s.from = from
	}
	if to.After(s.to) {
		s.to = to
	}
}

func (s *span) merge(o span) {
	switch {
	case o.empty:
	case o.unbounded:
		s.unbounded, s.empty = true, false
	default:
		s.add(o.from, o.to)
	}
}

// rowsSpan widens s by stored rows (their old place): a master's
// instances are not known from its row, so it makes the span unbounded.
// All-day and floating rows are widened by a day either side (their
// stored times are wall clocks).
func rowsSpan(s *span, rows []store.Event) {
	for _, r := range rows {
		if r.Kind == store.EventMaster {
			s.unbounded, s.empty = true, false
			return
		}
		if r.AllDay {
			f, _ := time.ParseInLocation(time.DateOnly, r.StartDate, time.UTC)
			t, _ := time.ParseInLocation(time.DateOnly, r.EndDate, time.UTC)
			s.add(f.Add(-24*time.Hour), t.Add(24*time.Hour))
			continue
		}
		if r.StartAt.IsZero() {
			continue
		}
		f, t := r.StartAt, r.EndAt
		if r.Floating {
			f, t = f.Add(-24*time.Hour), t.Add(24*time.Hour)
		}
		s.add(f, t)
	}
}

func occSpan(s *span, occ []store.Occurrence) {
	for _, o := range occ {
		f, t := o.StartAt, o.EndAt
		if o.AllDay || o.Floating {
			f, t = f.Add(-14*time.Hour), t.Add(14*time.Hour)
		}
		s.add(f, t)
	}
}

// applyResult is what storing one object changed.
type applyResult struct {
	span    span
	outside bool // no occurrence in the window: nothing stored
}

// storeObject makes obj the stored rows of its resource in cal: the rows
// and their occurrences in one transaction (ReplaceResource), or — when no
// occurrence lies in the window — no rows at all (the resource is then
// "outside"). daemon: the account's objects are expanded here.
func storeObject(ctx context.Context, st *store.Store, cal store.Calendar, obj Object, win Window, loc *time.Location, daemon bool) (applyResult, error) {
	res := applyResult{span: emptySpan()}
	if obj.RemoteID == "" {
		return res, fmt.Errorf("store object: empty remote id")
	}
	old, err := st.EventsByRemoteID(ctx, cal.ID, obj.RemoteID)
	if err != nil {
		return res, err
	}
	rowsSpan(&res.span, old)
	occ, err := expandObject(obj, win, loc, daemon)
	if err != nil {
		// A series that cannot be expanded keeps its rows without
		// occurrences rather than ending the pass.
		occ = nil
	}
	events := StoreEvents(obj)
	if len(events) == 0 || len(occ) == 0 {
		res.outside = true
		if len(old) > 0 {
			if _, err := st.RemoveResource(ctx, cal.ID, obj.RemoteID); err != nil {
				return res, err
			}
		}
		return res, nil
	}
	var stored []store.Occurrence
	_, err = st.ReplaceResource(ctx, cal.ID, obj.RemoteID, events, func(rows []store.Event) []store.Occurrence {
		stored = storeOccurrences(obj, rows, occ)
		return stored
	})
	if err != nil {
		return res, err
	}
	occSpan(&res.span, stored)
	return res, nil
}

// rebuildCalendar recomputes the daemon-side occurrences of every stored
// resource of a calendar from cal_events, without network (meta
// calendar.expand changed, or the window moved). Resources whose
// expansion is empty keep their rows (TrimEvents decides). Returns the
// number of resources rebuilt.
func rebuildCalendar(ctx context.Context, st *store.Store, cal store.Calendar, win Window, loc *time.Location, mastersOnly bool) (int, error) {
	rows, err := st.CalendarEvents(ctx, cal.ID)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].RemoteID == rows[i].RemoteID {
			j++
		}
		group := rows[i:j]
		i = j
		if mastersOnly && group[0].Kind != store.EventMaster {
			continue
		}
		obj := ObjectOf(group)
		occ, _, err := calendar.Occurrences(obj, calendar.Window{From: win.From, To: win.To}, loc, 0)
		if err != nil {
			occ = nil
		}
		ids := make([]string, len(group))
		for k := range group {
			ids[k] = group[k].ID
		}
		if err := st.ReplaceOccurrences(ctx, ids, storeOccurrences(obj, group, occ)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
