// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// A calendar source over data held in memory (InMemoryCalendarSource):
// the sample calendar of MALACHI_CALENDAR_SAMPLES (NewSampleSource) and
// the controllers' tests. Reads answer from the data (event.list by
// overlap with the visible calendars and the unanswered invitations,
// event.get by id, a plain substring search, the user's free/busy from
// the occurrences); writes answer ErrPreview, unless a test turns
// EmulatesWrites on: then each write is recorded in Writes, fails with
// the error a test put in Failures (by method name, api.Method*), or
// changes the data the simplest way (an answer sets the response, a
// delete removes the occurrence, a create adds one). The calendars'
// visibility always works: it only changes what is shown. Every answer
// comes through the loop, after the data changed and the observers heard
// of it; a test with a manual loop holds every answer until it runs the
// loop.

// SamplesEnv is the switch of the sample calendar, read once at start
// like MALACHI_BOARD_SAMPLES: "1" gives a window NewSampleSource instead
// of the daemon's source. A development aid, never the owner's data.
const SamplesEnv = "MALACHI_CALENDAR_SAMPLES"

// Write is a write as the in-memory source received it: the method's
// name (api.Method*) and its params (api.*Params; SetVisible and SetColor
// are api.CalendarUpdateParams).
type Write struct {
	Method string
	Params any
}

// InMemorySource is a DataSource over calendar.Samples.
type InMemorySource struct {
	loop     board.Loop
	loc      *time.Location
	snapshot Snapshot
	data     calendar.Samples
	obs      observers
	writes   []Write
	reads    []string
	next     int

	// EmulatesWrites: writes change the data instead of answering
	// ErrPreview.
	EmulatesWrites bool
	// Failures is the error a method answers, by its name.
	Failures map[string]*api.Error
	// What the CalDAV calls, free/busy, findFreeTime and account.add
	// answer (with EmulatesWrites for the writes).
	DiscoverResult     api.AccountDiscoverCalDAVResult
	ListResult         api.AccountListCalDAVCalendarsResult
	AddedAccount       api.AccountID
	FreeBusyResult     *api.CalendarFreeBusyResult
	FindFreeTimeResult api.CalendarFindFreeTimeResult
	// RespondedCard is what invitation.respond answers; nil: the first
	// sample card with the answer.
	RespondedCard *api.InvitationCard
}

// The technical English of the errors the emulated writes answer (an
// api.Error's message is never shown).
const (
	noEvent   = "no event"
	staleETag = "etag"
)

// NewInMemorySource holds data; loc is the zone of the overlap tests and
// the parsed wall-clock times (nil = time.Local); answers go through loop
// (nil = at once).
func NewInMemorySource(data calendar.Samples, loc *time.Location, loop board.Loop) *InMemorySource {
	if loc == nil {
		loc = time.Local
	}
	return &InMemorySource{loop: loop, loc: loc, data: cloneSamples(data), snapshot: SampleSnapshot(data),
		Failures: map[string]*api.Error{}, AddedAccount: "acc_added",
		DiscoverResult:     api.AccountDiscoverCalDAVResult{FoundVia: api.CalDAVFoundURL, NeedsCredentials: true},
		ListResult:         api.AccountListCalDAVCalendarsResult{Scheduling: api.CalDAVSchedulingServer, Addresses: []string{}, Calendars: []api.CalDAVCalendar{}},
		FindFreeTimeResult: api.CalendarFindFreeTimeResult{Slots: []api.TimeSlot{}, Unknown: []string{}}}
}

// NewEmptySource is an empty calendar, ready.
func NewEmptySource(loc *time.Location, loop board.Loop) *InMemorySource {
	return NewInMemorySource(calendar.Samples{Preferences: api.DefaultCalendarPreferences()}, loc, loop)
}

// NewSampleSource is the sample calendar of the week of now in loc
// (calendar.SampleData with its remote ids): reads work, writes answer
// ErrPreview, the calendars' checkboxes only change what is shown.
func NewSampleSource(now time.Time, loc *time.Location, loop board.Loop) *InMemorySource {
	if loc == nil {
		loc = time.Local
	}
	return NewInMemorySource(calendar.SampleData(now, loc).WithRemoteIDs(), loc, loop)
}

// Snapshot implements DataSource.
func (m *InMemorySource) Snapshot() Snapshot { return m.snapshot }

// Data is the data held now.
func (m *InMemorySource) Data() calendar.Samples { return m.data }

// Writes are the writes received, in order (with EmulatesWrites; a
// visibility change always).
func (m *InMemorySource) Writes() []Write { return m.writes }

// Reads are the methods of the reads received, in order.
func (m *InMemorySource) Reads() []string { return m.reads }

// Observe implements DataSource.
func (m *InMemorySource) Observe(f func(Change)) *board.ObserverToken { return m.obs.add(f) }

// Observers is the number of observers installed (tests).
func (m *InMemorySource) Observers() int { return m.obs.o.Len() }

// Refresh implements DataSource: nothing to ask.
func (m *InMemorySource) Refresh() {}

// Replace replaces the data, as a source does when its data arrives; the
// visibility the user chose stays.
func (m *InMemorySource) Replace(data calendar.Samples, phase Phase) {
	m.data = cloneSamples(data)
	s := SampleSnapshot(data)
	s.Phase = phase
	for ai := range s.CalendarAccounts {
		for ci := range s.CalendarAccounts[ai].Calendars {
			c := &s.CalendarAccounts[ai].Calendars[ci]
			if old, ok := m.snapshot.Calendar(c.ID); ok {
				c.Visible = old.Visible
			}
		}
	}
	m.snapshot = s
	m.obs.notify(Change{Kind: ChangeSnapshot})
	m.obs.notify(Change{Kind: ChangeOccurrences})
}

// Update changes the snapshot (a state, the count, the phase) and tells
// the observers; no change tells no one.
func (m *InMemorySource) Update(change func(*Snapshot)) {
	s := m.snapshot.clone()
	change(&s)
	if s.Equal(m.snapshot) {
		return
	}
	m.snapshot = s
	m.obs.notify(Change{Kind: ChangeSnapshot})
}

// Handle implements DataSource.
func (m *InMemorySource) Handle(method string, raw json.RawMessage) bool {
	if !IsCalendarNotification(method) {
		return false
	}
	switch method {
	case api.NotifyCalendarChanged:
		var n api.CalendarChangedNotification
		if json.Unmarshal(raw, &n) == nil {
			m.obs.notify(Change{Kind: ChangeOccurrences, From: n.From, To: n.To})
		}
	case api.NotifyCalendarState:
		var n api.CalendarStateNotification
		if json.Unmarshal(raw, &n) == nil {
			m.Update(func(s *Snapshot) {
				for i := range s.CalendarAccounts {
					if s.CalendarAccounts[i].AccountID == n.State.AccountID {
						s.CalendarAccounts[i].State = n.State
					}
				}
			})
		}
	case api.NotifyEventAlert:
		var n api.EventAlertNotification
		if json.Unmarshal(raw, &n) == nil {
			m.obs.notify(Change{Kind: ChangeAlert, Alert: n.Alert})
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// read records the read and answers its failure or what answer gives.
func read[T any](m *InMemorySource, method string, answer func() (T, *api.Error), done func(T, *api.Error)) {
	m.reads = append(m.reads, method)
	post(m.loop, func() {
		var zero T
		if f := m.Failures[method]; f != nil {
			call(done, zero, f)
			return
		}
		r, err := answer()
		call(done, r, err)
	})
}

func call[T any](done func(T, *api.Error), r T, err *api.Error) {
	if done != nil {
		done(r, err)
	}
}

// CachedOccurrences implements DataSource: nothing is cached.
func (m *InMemorySource) CachedOccurrences(Range) ([]api.Occurrence, bool) { return nil, false }

// Occurrences implements DataSource.
func (m *InMemorySource) Occurrences(r Range, done func([]api.Occurrence, *api.Error)) {
	read(m, api.MethodEventList, func() ([]api.Occurrence, *api.Error) {
		visible := m.snapshot.VisibleCalendars()
		out := []api.Occurrence{}
		for _, o := range m.data.Occurrences {
			if (o.CalendarID == "" || visible[o.CalendarID]) && calendar.Overlaps(o, r.From, r.To, r.location()) {
				out = append(out, o)
			}
		}
		return out, nil
	}, done)
}

// Event implements DataSource.
func (m *InMemorySource) Event(id api.EventID, rec api.DateOrTime, _ bool, done func(*api.Event, *api.Error)) {
	read(m, api.MethodEventGet, func() (*api.Event, *api.Error) {
		if i := m.eventIndex(id, rec); i >= 0 {
			e := m.data.Events[i]
			return &e, nil
		}
		return nil, &api.Error{Code: api.CodeEventNotFound, Message: noEvent}
	}, done)
}

// eventIndex is the index of the event id (of the instance rec, any with
// rec ""), -1 for none.
func (m *InMemorySource) eventIndex(id api.EventID, rec api.DateOrTime) int {
	return slices.IndexFunc(m.data.Events, func(e api.Event) bool {
		return e.EventID == id && (rec == "" || e.RecurrenceID == rec)
	})
}

// Search implements DataSource: a substring of the title or the place,
// each event once.
func (m *InMemorySource) Search(p api.EventSearchParams, done func(*api.EventSearchResult, *api.Error)) {
	read(m, api.MethodEventSearch, func() (*api.EventSearchResult, *api.Error) {
		q := strings.ToLower(p.Query)
		seen := map[string]bool{}
		out := []api.Occurrence{}
		for _, o := range m.data.Occurrences {
			if !strings.Contains(strings.ToLower(o.Title), q) && !strings.Contains(strings.ToLower(o.Location), q) {
				continue
			}
			k := string(o.EventID)
			if k == "" {
				k = calendar.OccurrenceKey(o)
			}
			if !seen[k] {
				seen[k] = true
				out = append(out, o)
			}
		}
		return &api.EventSearchResult{Occurrences: out, Total: len(out)}, nil
	}, done)
}

// FreeBusy implements DataSource: FreeBusyResult, else the user's busy
// time from the timed occurrences and nothing known of the attendees.
func (m *InMemorySource) FreeBusy(p api.CalendarFreeBusyParams, done func(*api.CalendarFreeBusyResult, *api.Error)) {
	read(m, api.MethodCalendarFreeBusy, func() (*api.CalendarFreeBusyResult, *api.Error) {
		if m.FreeBusyResult != nil {
			r := *m.FreeBusyResult
			return &r, nil
		}
		busy := []api.BusyInterval{}
		for _, o := range m.data.Occurrences {
			if o.AllDay || o.ShowAs == api.ShowAsFree || o.MyResponse == api.ResponseDeclined || o.Start == nil || o.End == nil ||
				!o.Start.Before(p.To) || !o.End.After(p.From) {
				continue
			}
			busy = append(busy, api.BusyInterval{Start: *o.Start, End: *o.End, ShowAs: o.ShowAs})
		}
		r := api.CalendarFreeBusyResult{Me: api.Availability{Address: m.snapshot.MyAddress(p.AccountID), Known: true, Busy: busy},
			Attendees: []api.Availability{}}
		for _, a := range p.Attendees {
			r.Attendees = append(r.Attendees, api.Availability{Address: a, Busy: []api.BusyInterval{}})
		}
		return &r, nil
	}, done)
}

// FindFreeTime implements DataSource: FindFreeTimeResult.
func (m *InMemorySource) FindFreeTime(_ api.CalendarFindFreeTimeParams, done func(*api.CalendarFindFreeTimeResult, *api.Error)) {
	read(m, api.MethodCalendarFindFreeTime, func() (*api.CalendarFindFreeTimeResult, *api.Error) {
		r := m.FindFreeTimeResult
		return &r, nil
	}, done)
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// write answers ErrPreview, or records the write and answers its failure
// or what apply does.
func write[T any](m *InMemorySource, method string, params any, apply func() (*T, *api.Error), done func(*T, *api.Error)) {
	if !m.EmulatesWrites {
		post(m.loop, func() { call(done, nil, ErrPreview) })
		return
	}
	m.writes = append(m.writes, Write{Method: method, Params: params})
	post(m.loop, func() {
		if f := m.Failures[method]; f != nil {
			call(done, nil, f)
			return
		}
		r, err := apply()
		if err != nil {
			r = nil
		}
		call(done, r, err)
	})
}

// errOnly adapts a done without a result.
func errOnly(done func(*api.Error)) func(*struct{}, *api.Error) {
	if done == nil {
		return nil
	}
	return func(_ *struct{}, e *api.Error) { done(e) }
}

// nothing is the apply of a write that changes nothing.
func nothing() (*struct{}, *api.Error) { return &struct{}{}, nil }

// SetVisible implements DataSource: always works, at once (a refusal in
// Failures[MethodCalendarUpdate] is ChangeError and changes nothing).
func (m *InMemorySource) SetVisible(id api.CalendarID, visible bool) {
	c, ok := m.snapshot.Calendar(id)
	if !ok || c.Visible == visible {
		return
	}
	v := visible
	m.writes = append(m.writes, Write{Method: api.MethodCalendarUpdate, Params: api.CalendarUpdateParams{CalendarID: id, Visible: &v}})
	if f := m.Failures[api.MethodCalendarUpdate]; f != nil {
		m.obs.notify(Change{Kind: ChangeError, Err: f})
		return
	}
	m.Update(func(s *Snapshot) {
		for ai := range s.CalendarAccounts {
			for ci := range s.CalendarAccounts[ai].Calendars {
				if s.CalendarAccounts[ai].Calendars[ci].ID == id {
					s.CalendarAccounts[ai].Calendars[ci].Visible = visible
				}
			}
		}
	})
	m.obs.notify(Change{Kind: ChangeOccurrences})
}

// SetColor implements DataSource.
func (m *InMemorySource) SetColor(id api.CalendarID, color string, done func(*api.Calendar, *api.Error)) {
	c := color
	write(m, api.MethodCalendarUpdate, api.CalendarUpdateParams{CalendarID: id, UserColor: &c}, func() (*api.Calendar, *api.Error) {
		if _, ok := m.snapshot.Calendar(id); !ok {
			return nil, &api.Error{Code: api.CodeCalendarNotFound, Message: "no calendar"}
		}
		var out api.Calendar
		m.Update(func(s *Snapshot) {
			for ai := range s.CalendarAccounts {
				for ci := range s.CalendarAccounts[ai].Calendars {
					k := &s.CalendarAccounts[ai].Calendars[ci]
					if k.ID != id {
						continue
					}
					k.UserColor = color
					if color != "" {
						k.Color = color
					} else if k.ServerColor != "" {
						k.Color = k.ServerColor
					}
					out = *k
				}
			}
		})
		return &out, nil
	}, done)
}

// Sync implements DataSource.
func (m *InMemorySource) Sync(p api.CalendarSyncParams, done func(*api.Error)) {
	write(m, api.MethodCalendarSync, p, nothing, errOnly(done))
}

// SetPreferences implements DataSource.
func (m *InMemorySource) SetPreferences(u api.CalendarPreferencesUpdate, done func(*api.CalendarPreferences, *api.Error)) {
	write(m, api.MethodCalendarSetPreferences, u, func() (*api.CalendarPreferences, *api.Error) {
		p := m.data.Preferences
		if u.WeekStart != nil {
			p.WeekStart = *u.WeekStart
		}
		if u.DefaultDurationMinutes != nil {
			p.DefaultDurationMinutes = *u.DefaultDurationMinutes
		}
		if u.DefaultAlertMinutes != nil {
			p.DefaultAlertMinutes = *u.DefaultAlertMinutes
		}
		if u.ShowDeclined != nil {
			p.ShowDeclined = *u.ShowDeclined
		}
		if u.WorkingHours != nil {
			p.WorkingHours = *u.WorkingHours
		}
		m.data.Preferences = p
		m.Update(func(s *Snapshot) { s.Preferences = p })
		return &p, nil
	}, done)
}

// CreateEvent implements DataSource: a new event "ev_created_N" in the
// calendar, its wall-clock times read in their zone (else the source's).
func (m *InMemorySource) CreateEvent(p api.EventCreateParams, done func(*api.EventCreateResult, *api.Error)) {
	write(m, api.MethodEventCreate, p, func() (*api.EventCreateResult, *api.Error) {
		m.next++
		in := p.Event
		c, _ := m.snapshot.Calendar(p.CalendarID)
		o := api.Occurrence{EventID: api.EventID("ev_created_" + strconv.Itoa(m.next)), CalendarID: p.CalendarID,
			AccountID: c.AccountID, Title: in.Title, AllDay: in.AllDay, Location: in.Location,
			Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, AttendeeCount: len(in.Attendees)}
		if in.AllDay {
			o.StartDate, o.EndDate = in.StartDate, in.EndDate
		} else {
			loc := m.loc
			if in.TimeZone != "" {
				if l, err := time.LoadLocation(string(in.TimeZone)); err == nil {
					loc = l
				}
			}
			o.Start, o.End, o.TimeZone = wallClock(in.Start, loc), wallClock(in.End, loc), in.TimeZone
		}
		e := api.Event{Occurrence: o, ETag: "\"1\"", Notes: in.Notes, CanEdit: true, CanDelete: true, RemoveIsDelete: true}
		m.data.Events = append(m.data.Events, e)
		m.data.Occurrences = append(m.data.Occurrences, o)
		m.obs.notify(Change{Kind: ChangeOccurrences})
		via := api.InvitationsViaNone
		if len(in.Attendees) > 0 {
			via = api.InvitationsViaServer
		}
		return &api.EventCreateResult{Event: e, Invitations: api.InvitationsSent{Via: via, Count: len(in.Attendees)}}, nil
	}, done)
}

// wallClock is a LocalDateTime in loc (nil for an unreadable one).
func wallClock(t api.LocalDateTime, loc *time.Location) *time.Time {
	for _, f := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if v, err := time.ParseInLocation(f, string(t), loc); err == nil {
			u := v.UTC()
			return &u
		}
	}
	return nil
}

// UpdateEvent implements DataSource: the title, the place and the notes,
// a new etag; a stale etag is conflict.
func (m *InMemorySource) UpdateEvent(p api.EventUpdateParams, done func(*api.EventUpdateResult, *api.Error)) {
	write(m, api.MethodEventUpdate, p, func() (*api.EventUpdateResult, *api.Error) {
		i := m.eventIndex(p.EventID, "")
		if i < 0 {
			return nil, &api.Error{Code: api.CodeEventNotFound, Message: noEvent}
		}
		e := &m.data.Events[i]
		if e.ETag != p.ETag {
			return nil, &api.Error{Code: api.CodeConflict, Message: staleETag}
		}
		e.Title, e.Location, e.Notes = p.Event.Title, p.Event.Location, p.Event.Notes
		e.ETag += "+"
		m.obs.notify(Change{Kind: ChangeEvent, Event: p.EventID})
		return &api.EventUpdateResult{Event: *e, Invitations: api.InvitationsSent{Via: api.InvitationsViaNone}}, nil
	}, done)
}

// DeleteEvent implements DataSource: the event (scope this: the instance)
// and its occurrences go.
func (m *InMemorySource) DeleteEvent(p api.EventDeleteParams, done func(*api.EventDeleteResult, *api.Error)) {
	write(m, api.MethodEventDelete, p, func() (*api.EventDeleteResult, *api.Error) {
		gone := func(id api.EventID, rec api.DateOrTime) bool {
			return id == p.EventID && (p.Scope != api.EditScopeThis || rec == p.RecurrenceID)
		}
		m.data.Events = slices.DeleteFunc(m.data.Events, func(e api.Event) bool { return gone(e.EventID, e.RecurrenceID) })
		m.data.Occurrences = slices.DeleteFunc(m.data.Occurrences, func(o api.Occurrence) bool { return gone(o.EventID, o.RecurrenceID) })
		m.obs.notify(Change{Kind: ChangeEvent, Event: p.EventID})
		m.obs.notify(Change{Kind: ChangeOccurrences})
		return &api.EventDeleteResult{Invitations: api.InvitationsSent{Via: api.InvitationsViaNone}}, nil
	}, done)
}

// Respond implements DataSource: the answer on the event and on its
// occurrences, a new etag.
func (m *InMemorySource) Respond(p api.EventRespondParams, done func(*api.EventRespondResult, *api.Error)) {
	write(m, api.MethodEventRespond, p, func() (*api.EventRespondResult, *api.Error) {
		i := m.eventIndex(p.EventID, p.RecurrenceID)
		if i < 0 {
			return nil, &api.Error{Code: api.CodeEventNotFound, Message: noEvent}
		}
		e := &m.data.Events[i]
		e.MyResponse = p.Response
		e.ETag += "+"
		for j := range m.data.Occurrences {
			if m.data.Occurrences[j].EventID == p.EventID {
				m.data.Occurrences[j].MyResponse = p.Response
			}
		}
		m.obs.notify(Change{Kind: ChangeEvent, Event: p.EventID})
		via := api.InvitationsViaServer
		if p.SendResponse != nil && !*p.SendResponse {
			via = api.InvitationsViaNone
		}
		return &api.EventRespondResult{Event: *e, Via: via}, nil
	}, done)
}

// SnoozeAlert implements DataSource.
func (m *InMemorySource) SnoozeAlert(p api.EventSnoozeAlertParams, done func(*api.Error)) {
	write(m, api.MethodEventSnoozeAlert, p, nothing, errOnly(done))
}

// DismissAlert implements DataSource.
func (m *InMemorySource) DismissAlert(p api.EventDismissAlertParams, done func(*api.Error)) {
	write(m, api.MethodEventDismissAlert, p, nothing, errOnly(done))
}

// RespondToInvitation implements DataSource: RespondedCard, else the
// first sample card answered.
func (m *InMemorySource) RespondToInvitation(p api.InvitationRespondParams, done func(*api.InvitationRespondResult, *api.Error)) {
	write(m, api.MethodInvitationRespond, p, func() (*api.InvitationRespondResult, *api.Error) {
		var card api.InvitationCard
		switch {
		case m.RespondedCard != nil:
			card = *m.RespondedCard
		default:
			if len(m.data.Cards) > 0 {
				card = m.data.Cards[0]
			} else {
				card = api.InvitationCard{InvitationSummary: api.InvitationSummary{Method: api.InvitationMethodRequest,
					Kind: api.InvitationKindInvitation, State: api.InvitationUnanswered},
					Conflicts: []api.InvitationConflict{}, Targets: []api.InvitationTarget{},
					Day: api.InvitationDay{Busy: []api.DayBusy{}}, CanRespond: true, ReplyVia: api.InvitationsViaNone}
			}
			card.State, card.Response, card.CalendarID = api.InvitationAnswered, p.Response, p.CalendarID
		}
		return &api.InvitationRespondResult{Invitation: card, EventID: card.EventID}, nil
	}, done)
}

// ApplyCancel implements DataSource.
func (m *InMemorySource) ApplyCancel(p api.InvitationApplyCancelParams, done func(*api.InvitationApplyCancelResult, *api.Error)) {
	write(m, api.MethodInvitationApplyCancel, p, func() (*api.InvitationApplyCancelResult, *api.Error) {
		return &api.InvitationApplyCancelResult{Removed: true}, nil
	}, done)
}

// DiscoverCalDAV implements DataSource: DiscoverResult.
func (m *InMemorySource) DiscoverCalDAV(p api.AccountDiscoverCalDAVParams, done func(*api.AccountDiscoverCalDAVResult, *api.Error)) {
	write(m, api.MethodAccountDiscoverCalDAV, p, func() (*api.AccountDiscoverCalDAVResult, *api.Error) {
		r := m.DiscoverResult
		return &r, nil
	}, done)
}

// ListCalDAVCalendars implements DataSource: ListResult.
func (m *InMemorySource) ListCalDAVCalendars(p api.AccountListCalDAVCalendarsParams, done func(*api.AccountListCalDAVCalendarsResult, *api.Error)) {
	write(m, api.MethodAccountListCalDAVCalendars, p, func() (*api.AccountListCalDAVCalendarsResult, *api.Error) {
		r := m.ListResult
		return &r, nil
	}, done)
}

// AddAccount implements DataSource: AddedAccount.
func (m *InMemorySource) AddAccount(p api.AccountAddParams, done func(*api.AccountAddResult, *api.Error)) {
	write(m, api.MethodAccountAdd, p, func() (*api.AccountAddResult, *api.Error) {
		return &api.AccountAddResult{AccountID: m.AddedAccount}, nil
	}, done)
}

// UpdateAccount implements DataSource: the account's config in the data
// and the snapshot.
func (m *InMemorySource) UpdateAccount(p api.AccountUpdateParams, done func(*api.Error)) {
	write(m, api.MethodAccountUpdate, p, func() (*struct{}, *api.Error) {
		i := slices.IndexFunc(m.data.Accounts, func(a api.Account) bool { return a.ID == p.AccountID })
		if i >= 0 {
			m.data.Accounts[i].Config = p.Config
			m.Update(func(s *Snapshot) {
				for j := range s.Accounts {
					if s.Accounts[j].ID == p.AccountID {
						s.Accounts[j].Config = p.Config
					}
				}
			})
		}
		return &struct{}{}, nil
	}, errOnly(done))
}

// ImportAttachment implements DataSource: an attachment named after the
// file.
func (m *InMemorySource) ImportAttachment(p api.AttachmentImportParams, done func(*api.AttachmentImportResult, *api.Error)) {
	write(m, api.MethodAttachmentImport, p, func() (*api.AttachmentImportResult, *api.Error) {
		name := p.Filename
		if name == "" && p.Path != "" {
			name = filepath.Base(p.Path)
		}
		if name == "" {
			name = "file"
		}
		return &api.AttachmentImportResult{Attachment: api.DraftAttachment{ID: "att_" + strconv.Itoa(len(m.writes)),
			Filename: name, ContentType: "application/octet-stream", Size: 1}}, nil
	}, done)
}

// cloneSamples copies the slices the source changes in place.
func cloneSamples(d calendar.Samples) calendar.Samples {
	d.Accounts = slices.Clone(d.Accounts)
	d.Events = slices.Clone(d.Events)
	d.Occurrences = slices.Clone(d.Occurrences)
	return d
}

// The in-memory source is a DataSource.
var _ DataSource = (*InMemorySource)(nil)
