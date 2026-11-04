// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav"
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Events serves event.* (docs/api.md §4.15, docs/calendar.md §4.5): the
// occurrences of the stored window (and, once, of a range outside it),
// one event in full, search, the writes and the alerts. Writes go to the
// server first and answer when it answered (D6); what it answered is then
// stored through the engine (ApplyWrite).
func (b *Backend) Events() api.EventService { return &eventService{b} }

type eventService struct{ b *Backend }

var _ api.EventService = (*eventService)(nil)

const (
	// eventWriteTimeout bounds a write's server round trip (D6).
	eventWriteTimeout = 30 * time.Second
	// eventOutsideTimeout bounds the online part of event.list.
	eventOutsideTimeout = 10 * time.Second
	// eventSearchQueryBytes caps event.search's query.
	eventSearchQueryBytes = 256
	// eventSearchTotal caps event.search's total.
	eventSearchTotal = 1000
	// eventSearchDefault is event.search's default limit.
	eventSearchDefault = 50
	// maxInvitationMessages caps Event.invitationMessages.
	maxInvitationMessages = 20
	// imipOverhead estimates the headers, the text/plain part and the
	// multipart framing of an iMIP message (bytes).
	imipOverhead = 16 << 10
)

// ---------------------------------------------------------------------------
// The view of the stored calendars
// ---------------------------------------------------------------------------

// calendarView is what a reading method needs of the stored calendars:
// each one by id with its account, and the calendar order (account
// order, then server order).
type calendarView struct {
	cals     map[string]store.Calendar
	order    map[string]int
	accounts map[string]store.Account
	readable []store.Account // enabled, with calendars, in the accounts' order
}

func (b *Backend) calendarView(ctx context.Context) (calendarView, error) {
	v := calendarView{cals: map[string]store.Calendar{}, order: map[string]int{}, accounts: map[string]store.Account{}}
	accounts, err := b.calendarAccounts(ctx)
	if err != nil {
		return v, err
	}
	n := 0
	for _, a := range accounts {
		v.accounts[a.ID] = a
		if calsync.HasCalendars(a) {
			v.readable = append(v.readable, a)
		}
		cals, err := b.accountCalendars(ctx, a)
		if err != nil {
			return v, err
		}
		for _, c := range cals {
			v.cals[c.ID] = c
			v.order[c.ID] = n
			n++
		}
	}
	return v, nil
}

func (v calendarView) readableIDs() []string {
	out := make([]string, 0, len(v.readable))
	for _, a := range v.readable {
		out = append(out, a.ID)
	}
	return out
}

// ---------------------------------------------------------------------------
// Conversion of stored rows
// ---------------------------------------------------------------------------

// wallIn is the instant of a wall clock stored as if UTC, read in loc.
func wallIn(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc).UTC()
}

func showAsOf(s api.ShowAs) api.ShowAs {
	switch s {
	case api.ShowAsFree, api.ShowAsTentative, api.ShowAsBusy, api.ShowAsOOF, api.ShowAsWorkingElsewhere, api.ShowAsUnknown:
		return s
	case "":
		return api.ShowAsBusy
	}
	return api.ShowAsUnknown
}

func statusOf(s api.EventStatus) api.EventStatus {
	switch s {
	case api.EventStatusConfirmed, api.EventStatusTentative, api.EventStatusCancelled:
		return s
	}
	return api.EventStatusConfirmed
}

// hiddenPrivate: a private event of a calendar shared with the user that
// the user does not organise shows without title and location.
func hiddenPrivate(e store.Event, c store.Calendar) bool {
	return e.Private && c.OwnerAddress != "" && !e.IAmOrganizer
}

// apiOccurrence is a stored occurrence with the row whose fields it shows.
func apiOccurrence(o store.Occurrence, e store.Event, c store.Calendar, loc *time.Location) api.Occurrence {
	out := api.Occurrence{
		EventID: api.EventID(o.EventID), RecurrenceID: calsync.ModelKey(o.OccurrenceKey), SeriesID: api.EventID(o.SeriesID),
		CalendarID: api.CalendarID(o.CalendarID), AccountID: api.AccountID(o.AccountID),
		AllDay: o.AllDay, Floating: o.Floating && !o.AllDay,
	}
	switch {
	case o.AllDay:
		out.StartDate = api.Date(o.StartAt.UTC().Format(time.DateOnly))
		out.EndDate = api.Date(o.EndAt.UTC().Format(time.DateOnly))
	case o.Floating:
		s, en := wallIn(o.StartAt, loc), wallIn(o.EndAt, loc)
		out.Start, out.End = &s, &en
	default:
		s, en := o.StartAt.UTC(), o.EndAt.UTC()
		out.Start, out.End = &s, &en
		out.TimeZone = api.TimeZone(e.TimeZone)
	}
	fillOccurrence(&out, e, c)
	if out.SeriesID == "" && e.Kind == store.EventMaster {
		out.Recurring = true
	}
	return out
}

// fillOccurrence sets the fields of an occurrence that come from its row.
func fillOccurrence(out *api.Occurrence, e store.Event, c store.Calendar) {
	out.Title = calendar.CleanLine(e.Title, calendar.MaxTitleBytes)
	out.Location = calendar.CleanLine(e.Location, calendar.MaxLocationBytes)
	out.Status, out.ShowAs = statusOf(e.Status), showAsOf(e.ShowAs)
	switch e.MyResponse {
	case api.ResponseNeedsAction, api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined, api.ResponseDelegated:
		out.MyResponse = e.MyResponse
	}
	out.Organizer = e.IAmOrganizer
	out.AttendeeCount = e.AttendeeCount
	out.Recurring = out.SeriesID != "" || e.Kind == store.EventMaster || e.Kind == store.EventOverride && e.MasterID != ""
	out.VideoCall = e.VideoURL != ""
	out.Private = e.Private
	out.Alert = len(e.Alarms) > 0
	out.ReadOnly = calendarReadOnly(c)
	if hiddenPrivate(e, c) {
		out.Title, out.Location = "", ""
	}
}

// occurrenceStart orders occurrences: the start instant (an all-day one
// at the midnight of its first day in loc).
func occurrenceStart(o api.Occurrence, loc *time.Location) time.Time {
	if o.AllDay {
		if d, ok := calendar.ParseDate(o.StartDate); ok {
			return wallIn(d, loc)
		}
		return time.Time{}
	}
	if o.Start != nil {
		return *o.Start
	}
	return time.Time{}
}

func occurrenceEnd(o api.Occurrence, loc *time.Location) time.Time {
	if o.AllDay {
		if d, ok := calendar.ParseDate(o.EndDate); ok {
			return wallIn(d, loc)
		}
		return occurrenceStart(o, loc).AddDate(0, 0, 1)
	}
	if o.End != nil {
		return *o.End
	}
	return occurrenceStart(o, loc)
}

// overlaps: the occurrence overlaps [from, to) (an all-day one by its
// dates in loc; an instant one when it lies in the range).
func overlaps(o api.Occurrence, from, to time.Time, loc *time.Location) bool {
	s, e := occurrenceStart(o, loc), occurrenceEnd(o, loc)
	if s.IsZero() {
		return false
	}
	if e.Equal(s) {
		return !s.Before(from) && s.Before(to)
	}
	return s.Before(to) && e.After(from)
}

// sortOccurrences: by start (all-day first at the same instant), then
// calendar order (invitations without a calendar after them).
func sortOccurrences(occs []api.Occurrence, v calendarView, loc *time.Location) {
	rank := func(o api.Occurrence) int {
		if r, ok := v.order[string(o.CalendarID)]; ok {
			return r
		}
		return len(v.order)
	}
	sort.SliceStable(occs, func(i, j int) bool {
		a, b := occs[i], occs[j]
		sa, sb := occurrenceStart(a, loc), occurrenceStart(b, loc)
		if !sa.Equal(sb) {
			return sa.Before(sb)
		}
		if a.AllDay != b.AllDay {
			return a.AllDay
		}
		return rank(a) < rank(b)
	})
}

// ---------------------------------------------------------------------------
// event.list
// ---------------------------------------------------------------------------

// listZone reads a request's zone: an IANA name, "" = the daemon's.
func (b *Backend) listZone(z api.TimeZone) (*time.Location, error) {
	if z == "" {
		return b.calendarLocation(), nil
	}
	loc, err := calendar.LoadZone(string(z))
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "timeZone is not an IANA zone")
	}
	return loc, nil
}

func (s *eventService) List(ctx context.Context, p api.EventListParams) (*api.EventListResult, error) {
	b := s.b
	if p.From.IsZero() || p.To.IsZero() || !p.From.Before(p.To) {
		return nil, api.NewError(api.CodeInvalidArgument, "from must be before to")
	}
	if p.To.Sub(p.From) > time.Duration(api.MaxEventListDays)*24*time.Hour {
		return nil, api.NewError(api.CodeInvalidArgument, "range over %d days", api.MaxEventListDays)
	}
	loc, err := b.listZone(p.TimeZone)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	switch {
	case limit < 0 || limit > api.MaxEventListResults:
		return nil, api.NewError(api.CodeInvalidArgument, "limit out of 1–%d", api.MaxEventListResults)
	case limit == 0:
		limit = api.DefaultEventListResults
	}
	prefs, _ := b.store.CalendarPreferences(ctx)
	includeDeclined := prefs.ShowDeclined
	if p.IncludeDeclined != nil {
		includeDeclined = *p.IncludeDeclined
	}
	v, err := b.calendarView(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range p.CalendarIDs {
		if _, ok := v.cals[string(id)]; !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "unknown calendar %q", id)
		}
	}
	for _, id := range p.AccountIDs {
		if _, ok := v.accounts[string(id)]; !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "unknown calendar account %q", id)
		}
	}
	// The accounts that count: readable ones, narrowed by accountIds.
	var accounts []store.Account
	for _, a := range v.readable {
		if len(p.AccountIDs) == 0 || slices.Contains(p.AccountIDs, api.AccountID(a.ID)) {
			accounts = append(accounts, a)
		}
	}
	res := &api.EventListResult{Occurrences: []api.Occurrence{}, Complete: true}
	if len(accounts) == 0 {
		return res, nil
	}
	ids := make([]string, len(accounts))
	var hideNeeds []string
	for i, a := range accounts {
		ids[i] = a.ID
		if a.Config.Calendar != nil && !a.Config.Calendar.ShowsUnanswered() {
			hideNeeds = append(hideNeeds, a.ID)
		}
	}
	calIDs := make([]string, len(p.CalendarIDs))
	for i, id := range p.CalendarIDs {
		calIDs[i] = string(id)
	}
	includeHidden := p.IncludeHidden || len(calIDs) > 0
	list, err := b.store.EventRange(ctx, store.OccurrenceQuery{
		From: p.From, To: p.To, Zone: loc, CalendarIDs: calIDs, AccountIDs: ids,
		IncludeHidden: includeHidden, IncludeDeclined: includeDeclined,
		HideNeedsActionAccountIDs: hideNeeds, Limit: limit,
	})
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	res.Truncated = list.Truncated
	for _, o := range list.Occurrences {
		e, ok := list.Events[o.EventID]
		c, okc := v.cals[o.CalendarID]
		if !ok || !okc {
			continue
		}
		res.Occurrences = append(res.Occurrences, apiOccurrence(o, e, c, loc))
	}

	// Parts of the range outside an account's window, once from the server.
	f := listFilter{calendars: calIDs, includeHidden: includeHidden, includeDeclined: includeDeclined, hideNeeds: hideNeeds}
	outside, complete := b.outsideWindow(ctx, v, accounts, f, p.From, p.To, loc)
	res.Occurrences = append(res.Occurrences, outside...)
	res.Complete = complete

	includeInv := p.IncludeInvitations == nil || *p.IncludeInvitations
	if includeInv && len(calIDs) == 0 {
		inv, err := b.tentativeInvitations(ctx, v, p.AccountIDs, p.From, p.To, loc)
		if err != nil {
			return nil, err
		}
		res.Occurrences = append(res.Occurrences, inv...)
	}
	sortOccurrences(res.Occurrences, v, loc)
	if len(res.Occurrences) > limit {
		res.Occurrences, res.Truncated = res.Occurrences[:limit], true
	}
	return res, nil
}

// listFilter is event.list's selection, applied to occurrences fetched
// outside the window.
type listFilter struct {
	calendars                      []string
	includeHidden, includeDeclined bool
	hideNeeds                      []string
}

func (f listFilter) calendar(c store.Calendar) bool {
	if len(f.calendars) > 0 {
		return slices.Contains(f.calendars, c.ID)
	}
	return f.includeHidden || c.Visible
}

func (f listFilter) keep(o api.Occurrence) bool {
	if o.MyResponse == api.ResponseDeclined && !f.includeDeclined {
		return false
	}
	if o.MyResponse == api.ResponseNeedsAction && slices.Contains(f.hideNeeds, string(o.AccountID)) {
		return false
	}
	return o.Status != api.EventStatusCancelled
}

// rangeObjects is the Graph provider's listing with the events
// (graph.CalendarProvider.RangeObjects); a provider without it gives
// occurrences only (Provider.Range).
type rangeObjects interface {
	RangeObjects(ctx context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calendar.Object, error)
}

// outsideWindow fetches the parts of [from, to) that lie outside each
// account's window from its server, storing nothing; complete is false
// when an account could not answer (no provider, offline, an error).
func (b *Backend) outsideWindow(ctx context.Context, v calendarView, accounts []store.Account, f listFilter,
	from, to time.Time, loc *time.Location) ([]api.Occurrence, bool) {
	eng := b.calendarEngine()
	if eng == nil {
		return nil, true
	}
	ctx, cancel := context.WithTimeout(ctx, eventOutsideTimeout)
	defer cancel()
	complete := true
	var out []api.Occurrence
	for _, a := range accounts {
		w := eng.Window(a)
		type part struct{ from, to time.Time }
		var parts []part
		if from.Before(w.From) {
			parts = append(parts, part{from, minTime(to, w.From)})
		}
		if to.After(w.To) {
			parts = append(parts, part{maxTime(from, w.To), to})
		}
		if len(parts) == 0 {
			continue
		}
		var cals []store.Calendar
		for _, c := range v.cals {
			if c.AccountID == a.ID && f.calendar(c) {
				cals = append(cals, c)
			}
		}
		if len(cals) == 0 {
			continue
		}
		prov, ok := eng.Provider(a.ID)
		if !ok {
			complete = false
			continue
		}
		for _, c := range cals {
			for _, pt := range parts {
				occs, err := b.fetchRange(ctx, prov, c, pt.from, pt.to, loc)
				if err != nil {
					b.log.Info("event.list outside the window", "account", a.ID, "calendar", c.ID, "err", err)
					complete = false
					continue
				}
				for _, o := range occs {
					if f.keep(o) && overlaps(o, from, to, loc) {
						out = append(out, o)
					}
				}
			}
		}
	}
	return out, complete
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// fetchRange asks one calendar's server for [from, to) and converts what
// it answers. Events the store also has (a series with instances in the
// window) take their stored ids; others have no eventId. A CalDAV answer
// carries only the times of an occurrence and its href: its fields come
// from the stored resource when there is one.
func (b *Backend) fetchRange(ctx context.Context, prov calsync.Provider, c store.Calendar, from, to time.Time, loc *time.Location) ([]api.Occurrence, error) {
	var objs []calendar.Object
	if ro, ok := prov.(rangeObjects); ok {
		var err error
		if objs, err = ro.RangeObjects(ctx, c, from, to); err != nil {
			return nil, err
		}
	} else {
		occs, err := prov.Range(ctx, c, from, to)
		if err != nil {
			return nil, err
		}
		byHref := map[string]*calendar.Object{}
		var hrefs []string
		for _, o := range occs {
			obj := byHref[o.RemoteID]
			if obj == nil {
				rows, err := b.store.EventsByRemoteID(ctx, c.ID, o.RemoteID)
				if err != nil {
					return nil, err
				}
				x := calsync.ObjectOf(rows)
				x.RemoteID = o.RemoteID
				obj = &x
				byHref[o.RemoteID] = obj
				hrefs = append(hrefs, o.RemoteID)
			}
			o.Override = -1
			for i := range obj.Overrides {
				if obj.Overrides[i].RecurrenceID == o.RecurrenceID {
					o.Override = i
				}
			}
			obj.Occurrences = append(obj.Occurrences, o)
		}
		for _, h := range hrefs {
			objs = append(objs, *byHref[h])
		}
	}
	var out []api.Occurrence
	for i := range objs {
		obj := &objs[i]
		rows, err := b.store.EventsByRemoteID(ctx, c.ID, obj.RemoteID)
		if err != nil {
			return nil, err
		}
		var master *store.Event
		byKey := map[string]store.Event{}
		for _, r := range rows {
			if r.Kind != store.EventOverride && master == nil {
				m := r
				master = &m
			}
			byKey[r.RecurrenceID] = r
		}
		for _, occ := range obj.Occurrences {
			ev := obj.EventOf(occ)
			o := api.Occurrence{RecurrenceID: occ.RecurrenceID, AllDay: occ.AllDay, Floating: occ.Floating && !occ.AllDay,
				CalendarID: api.CalendarID(c.ID), AccountID: api.AccountID(c.AccountID), Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy}
			switch {
			case occ.AllDay:
				o.StartDate, o.EndDate = api.Date(occ.Start.UTC().Format(time.DateOnly)), api.Date(occ.End.UTC().Format(time.DateOnly))
			case occ.Floating:
				s, e := wallIn(occ.Start, loc), wallIn(occ.End, loc)
				o.Start, o.End = &s, &e
			default:
				s, e := occ.Start.UTC(), occ.End.UTC()
				o.Start, o.End = &s, &e
			}
			if ev != nil {
				row := calsync.StoreEvents(calendar.Object{Master: ev})[0]
				fillOccurrence(&o, row, c)
				if !occ.AllDay && !occ.Floating {
					o.TimeZone = api.TimeZone(ev.Zone)
				}
				o.Recurring = occ.Series || o.Recurring
			} else {
				o.ReadOnly = calendarReadOnly(c)
			}
			if r, ok := byKey[calsync.StoredKey(occ.RecurrenceID)]; ok && occ.RecurrenceID != "" {
				o.EventID = api.EventID(r.ID)
			} else if master != nil {
				o.EventID = api.EventID(master.ID)
			}
			if master != nil && master.Kind == store.EventMaster && o.EventID != api.EventID(master.ID) {
				o.SeriesID = api.EventID(master.ID)
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// tentativeInvitations are the unanswered invitations of enabled accounts
// with showUnanswered that are in no calendar, as occurrences in [from,
// to) (event.list includeInvitations). Junk and trash never show.
func (b *Backend) tentativeInvitations(ctx context.Context, v calendarView, only []api.AccountID, from, to time.Time, loc *time.Location) ([]api.Occurrence, error) {
	all, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	var off []string
	accounts := map[string]store.Account{}
	for _, a := range all {
		accounts[a.ID] = a
		if !a.Enabled || a.Config.Calendar != nil && !a.Config.Calendar.ShowsUnanswered() ||
			len(only) > 0 && !slices.Contains(only, api.AccountID(a.ID)) {
			off = append(off, a.ID)
		}
	}
	invs, err := b.store.ListInvitations(ctx, store.InvitationQuery{ExcludeAccountIDs: off,
		State: api.InvitationUnanswered, Limit: api.MaxInvitationList}, store.InvitationNow())
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	var out []api.Occurrence
	for _, li := range invs {
		if li.FolderRole == api.RoleJunk || li.FolderRole == api.RoleTrash || li.UID == "" || li.StartAt.IsZero() {
			continue
		}
		if b.uidInCalendar(ctx, v, li.UID) {
			continue
		}
		o := invitationOccurrence(li)
		if overlaps(o, from, to, loc) {
			out = append(out, o)
		}
	}
	return out, nil
}

// uidInCalendar: a stored event of a readable account has the UID.
func (b *Backend) uidInCalendar(ctx context.Context, v calendarView, uid string) bool {
	for _, a := range v.readable {
		if rows, err := b.store.EventsByUID(ctx, a.ID, uid); err == nil && len(rows) > 0 {
			return true
		}
	}
	return false
}

// invitationOccurrence is an unanswered invitation shown tentatively.
func invitationOccurrence(li store.ListedInvitation) api.Occurrence {
	o := api.Occurrence{
		AccountID: api.AccountID(li.AccountID),
		Title:     calendar.CleanLine(li.Title, calendar.MaxTitleBytes),
		AllDay:    li.AllDay, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsTentative,
		MyResponse: api.ResponseNeedsAction,
		Invitation: &api.MessageRef{AccountID: api.AccountID(li.AccountID), MessageID: api.MessageID(li.MessageID)},
	}
	if li.Summary.Title != "" {
		o.Title = calendar.CleanLine(li.Summary.Title, calendar.MaxTitleBytes)
	}
	var card api.InvitationCard
	if len(li.Card) > 0 && json.Unmarshal(li.Card, &card) == nil {
		o.Location = calendar.CleanLine(card.Location, calendar.MaxLocationBytes)
		o.AttendeeCount = card.AttendeeCount
		o.VideoCall = card.VideoCall != nil
		o.Recurring = card.Recurrence != nil
		if !li.AllDay {
			o.TimeZone = api.TimeZone(calendar.CleanLine(string(card.TimeZone), 64))
		}
	}
	end := li.EndAt
	if li.AllDay {
		if !end.After(li.StartAt) {
			end = li.StartAt.AddDate(0, 0, 1)
		}
		o.StartDate, o.EndDate = api.Date(li.StartAt.UTC().Format(time.DateOnly)), api.Date(end.UTC().Format(time.DateOnly))
	} else {
		if end.Before(li.StartAt) {
			end = li.StartAt
		}
		s, e := li.StartAt.UTC(), end.UTC()
		o.Start, o.End = &s, &e
	}
	return o
}

// ---------------------------------------------------------------------------
// event.get
// ---------------------------------------------------------------------------

// shownEvent is one instance of a stored event: the row whose fields show,
// the series' master (nil for a single event or an override without
// one), the occurrence, its calendar and account.
type shownEvent struct {
	row    store.Event
	master *store.Event
	occ    store.Occurrence
	cal    store.Calendar
	acc    store.Account
}

// root is the row a write of the whole series goes to: the master when
// stored, else the row itself.
func (s shownEvent) root() store.Event {
	if s.master != nil {
		return *s.master
	}
	return s.row
}

// series: the event belongs to a stored series (a master with instances).
func (s shownEvent) series() bool {
	return s.master != nil && s.master.Kind == store.EventMaster || s.row.Kind == store.EventMaster
}

func eventNotFound(id string) error {
	return api.NewError(api.CodeEventNotFound, "unknown event %q", id)
}

// syntheticOccurrence is an occurrence built from a row's own times
// (the event outside the stored occurrences: the master of a series as
// its first instance).
func syntheticOccurrence(e store.Event) store.Occurrence {
	o := store.Occurrence{EventID: e.ID, CalendarID: e.CalendarID, AccountID: e.AccountID, AllDay: e.AllDay, Floating: e.Floating}
	if e.Kind == store.EventOverride {
		o.OccurrenceKey, o.SeriesID = e.RecurrenceID, e.MasterID
	}
	if e.AllDay {
		o.StartAt, _ = time.ParseInLocation(time.DateOnly, e.StartDate, time.UTC)
		o.EndAt, _ = time.ParseInLocation(time.DateOnly, e.EndDate, time.UTC)
		if !o.EndAt.After(o.StartAt) {
			o.EndAt = o.StartAt.AddDate(0, 0, 1)
		}
	} else {
		o.StartAt, o.EndAt = e.StartAt, e.EndAt
	}
	if e.Kind == store.EventMaster {
		o.OccurrenceKey = store.FormatRecurrenceID(o.StartAt, e.AllDay)
		o.SeriesID = e.ID
	}
	return o
}

// shown resolves an event id and an optional recurrence id to the
// instance to show (event.get's rules).
func (b *Backend) shown(ctx context.Context, eventID string, rid api.DateOrTime) (shownEvent, error) {
	if eventID == "" {
		return shownEvent{}, api.NewError(api.CodeInvalidArgument, "eventId is required")
	}
	row, err := b.store.GetEvent(ctx, eventID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return shownEvent{}, eventNotFound(eventID)
	case err != nil:
		return shownEvent{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	cal, acc, err := b.requireCalendar(ctx, row.CalendarID)
	if err != nil {
		if apiCode(err) == api.CodeCalendarNotFound {
			return shownEvent{}, eventNotFound(eventID)
		}
		return shownEvent{}, err
	}
	s := shownEvent{row: row, cal: cal, acc: acc}
	rootID := row.ID
	if row.Kind == store.EventOverride && row.MasterID != "" {
		if m, err := b.store.GetEvent(ctx, row.MasterID); err == nil {
			s.master, rootID = &m, m.ID
		}
	} else if row.Kind == store.EventMaster {
		m := row
		s.master = &m
	}
	var key string
	switch {
	case rid != "":
		if _, ok := calendar.ParseKey(rid); !ok {
			return shownEvent{}, api.NewError(api.CodeInvalidArgument, "bad recurrenceId")
		}
		key = calsync.StoredKey(rid)
	case row.Kind == store.EventOverride:
		key = row.RecurrenceID
	case row.Kind == store.EventMaster:
		key = syntheticOccurrence(row).OccurrenceKey
	}
	if key == "" {
		o, err := b.store.GetOccurrence(ctx, row.ID, "")
		if err != nil {
			o = syntheticOccurrence(row)
		}
		s.occ = o
		return s, nil
	}
	o, err := b.store.SeriesOccurrence(ctx, rootID, key)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		switch {
		case row.Kind == store.EventOverride && row.RecurrenceID == key:
			o = syntheticOccurrence(row)
		case rid == "" && row.Kind == store.EventMaster:
			o = syntheticOccurrence(row)
		default:
			return shownEvent{}, api.NewError(api.CodeEventNotFound, "the event has no such instance")
		}
	default:
		return shownEvent{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.occ = o
	if o.EventID != row.ID {
		r, err := b.store.GetEvent(ctx, o.EventID)
		if err != nil {
			return shownEvent{}, eventNotFound(o.EventID)
		}
		s.row = r
	}
	return s, nil
}

func apiCode(err error) api.ErrorCode {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// videoCallOf is the stored video link as the API shows it.
func videoCallOf(e store.Event) *api.VideoCall {
	if e.VideoURL == "" {
		return nil
	}
	u, err := url.Parse(e.VideoURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil
	}
	p := api.VideoProvider(e.VideoProvider)
	switch p {
	case api.VideoProviderTeams, api.VideoProviderZoom, api.VideoProviderMeet, api.VideoProviderWebex,
		api.VideoProviderJitsi, api.VideoProviderWhereby, api.VideoProviderOther:
	default:
		p = calendar.VideoProviderOf(u.Hostname())
		if p == "" {
			p = api.VideoProviderOther
		}
	}
	return &api.VideoCall{URL: e.VideoURL, Provider: p, Host: u.Hostname()}
}

// apiEvent is the full event of an instance (event.get, the writes'
// results and the conflict data).
func (b *Backend) apiEvent(ctx context.Context, s shownEvent, loc *time.Location) api.Event {
	e := s.row
	ev := api.Event{Occurrence: apiOccurrence(s.occ, e, s.cal, loc)}
	if s.series() {
		ev.Recurring = true
		if ev.SeriesID == "" && s.master != nil && s.master.ID != "" {
			ev.SeriesID = api.EventID(s.master.ID)
		}
	}
	ev.ETag = e.ETag
	hidden := hiddenPrivate(e, s.cal)
	if !hidden {
		ev.Notes = calendar.CleanBlock(e.Notes, calendar.MaxNotesBytes)
		for _, l := range e.Links {
			if u, ok := calendar.SafeLink(l); ok {
				ev.Links = append(ev.Links, u)
			}
		}
		ev.VideoCallLink = videoCallOf(e)
	}
	if e.Organizer != nil && e.Organizer.Address != "" {
		ev.OrganizerAddress = &api.Address{Name: calendar.CleanLine(e.Organizer.Name, calendar.MaxNameBytes),
			Address: calendar.CleanLine(e.Organizer.Address, calendar.MaxNameBytes)}
	}
	for i, a := range e.Attendees {
		if i == api.MaxEventAttendeesStored {
			ev.AttendeesTruncated = true
			break
		}
		a.Name = calendar.CleanLine(a.Name, calendar.MaxNameBytes)
		a.Address = calendar.CleanLine(a.Address, calendar.MaxNameBytes)
		ev.Attendees = append(ev.Attendees, a)
	}
	if e.AttendeeCount > len(ev.Attendees) {
		ev.AttendeesTruncated = true
	}
	if ev.AttendeeCount < len(e.Attendees) {
		ev.AttendeeCount = len(e.Attendees)
	}
	if s.master != nil && s.master.Recurrence != nil {
		r := *s.master.Recurrence
		ev.Recurrence = &r
	} else if e.Recurrence != nil {
		r := *e.Recurrence
		ev.Recurrence = &r
	}
	if s.master != nil && s.master.RecurRefused && ev.Recurrence != nil {
		ev.Recurrence.Complex = true
	}
	if len(e.Alarms) > 0 {
		ev.Alerts = slices.Clone(e.Alarms)
	}
	ev.ResponseRequested = e.ResponseRequested
	ro := calendarReadOnly(s.cal) || !s.acc.Enabled
	attendee := e.MyResponse != "" && !e.IAmOrganizer
	ev.CanEdit = !ro && ev.Status != api.EventStatusCancelled
	ev.CanDelete = !ro
	ev.CanRespond = !ro && attendee && ev.Status != api.EventStatusCancelled
	ev.CanProposeNewTime = ev.CanRespond && e.NewTimeOK && calendarSource(s.acc) == api.CalendarSourceGraph &&
		b.calendarFeatures(s.acc).ProposeNewTime
	ev.RemoveIsDelete = !attendee
	if ev.ReadOnly {
		ev.CanEdit, ev.CanDelete, ev.CanRespond, ev.CanProposeNewTime = false, false, false, false
	}
	ev.InvitationMessages = b.invitationMessages(ctx, e.UID)
	if !e.CreatedAt.IsZero() {
		t := e.CreatedAt.UTC()
		ev.Created = &t
	}
	switch {
	case !e.ServerUpdated.IsZero():
		t := e.ServerUpdated.UTC()
		ev.Updated = &t
	case !e.UpdatedAt.IsZero():
		t := e.UpdatedAt.UTC()
		ev.Updated = &t
	}
	return ev
}

// invitationsOfUID are the stored invitations of a UID in every account.
func (b *Backend) invitationsOfUID(ctx context.Context, uid string) []store.Invitation {
	if uid == "" {
		return nil
	}
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil
	}
	var out []store.Invitation
	for _, a := range accounts {
		invs, err := b.store.InvitationsByUID(ctx, a.ID, uid)
		if err != nil {
			continue
		}
		out = append(out, invs...)
	}
	return out
}

// invitationMessages are the messages of a UID's invitations (Show
// Invitation in Mail), newest last, at most maxInvitationMessages.
func (b *Backend) invitationMessages(ctx context.Context, uid string) []api.MessageRef {
	var out []api.MessageRef
	for _, inv := range b.invitationsOfUID(ctx, uid) {
		ref := api.MessageRef{AccountID: api.AccountID(inv.AccountID), MessageID: api.MessageID(inv.MessageID)}
		if !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	if len(out) > maxInvitationMessages {
		out = out[len(out)-maxInvitationMessages:]
	}
	return out
}

func (s *eventService) Get(ctx context.Context, p api.EventGetParams) (*api.EventGetResult, error) {
	b := s.b
	loc, err := b.listZone(p.TimeZone)
	if err != nil {
		return nil, err
	}
	sh, err := b.shown(ctx, string(p.EventID), p.RecurrenceID)
	if err != nil {
		return nil, err
	}
	return &api.EventGetResult{Event: b.apiEvent(ctx, sh, loc)}, nil
}

// ---------------------------------------------------------------------------
// event.search
// ---------------------------------------------------------------------------

func (s *eventService) Search(ctx context.Context, p api.EventSearchParams) (*api.EventSearchResult, error) {
	b := s.b
	q := strings.TrimSpace(p.Query)
	if q == "" || len(p.Query) > eventSearchQueryBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "query empty or over %d bytes", eventSearchQueryBytes)
	}
	limit := p.Limit
	switch {
	case limit < 0 || limit > api.MaxEventSearchResults:
		return nil, api.NewError(api.CodeInvalidArgument, "limit out of 1–%d", api.MaxEventSearchResults)
	case limit == 0:
		limit = eventSearchDefault
	}
	if p.From != nil && p.To != nil && !p.From.Before(*p.To) {
		return nil, api.NewError(api.CodeInvalidArgument, "from must be before to")
	}
	v, err := b.calendarView(ctx)
	if err != nil {
		return nil, err
	}
	calIDs := make([]string, len(p.CalendarIDs))
	for i, id := range p.CalendarIDs {
		if _, ok := v.cals[string(id)]; !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "unknown calendar %q", id)
		}
		calIDs[i] = string(id)
	}
	res := &api.EventSearchResult{Occurrences: []api.Occurrence{}}
	accounts := v.readableIDs()
	if len(accounts) == 0 {
		return res, nil
	}
	ids, total, err := b.store.SearchEvents(ctx, store.EventSearch{Query: q, AccountIDs: accounts, CalendarIDs: calIDs, Limit: eventSearchTotal})
	if err != nil {
		if errors.Is(err, store.ErrSearchRejected) {
			return nil, api.NewError(api.CodeInvalidArgument, "query not accepted")
		}
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	now := time.Now()
	reps, err := b.store.RepresentativeOccurrences(ctx, ids, now)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	type hit struct {
		o        store.Occurrence
		upcoming bool
	}
	var hits []hit
	for _, id := range ids {
		o, ok := reps[id]
		if !ok {
			continue
		}
		if _, ok := v.cals[o.CalendarID]; !ok {
			continue
		}
		start, end := o.StartAt, o.EndAt
		if o.AllDay || o.Floating {
			start, end = wallIn(o.StartAt, b.calendarLocation()), wallIn(o.EndAt, b.calendarLocation())
		}
		if p.From != nil && !end.After(*p.From) && !(end.Equal(start) && !start.Before(*p.From)) {
			continue
		}
		if p.To != nil && !start.Before(*p.To) {
			continue
		}
		hits = append(hits, hit{o: o, upcoming: end.After(now)})
	}
	if p.From != nil || p.To != nil || len(hits) < len(ids) {
		total = len(hits)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, c := hits[i], hits[j]
		if a.upcoming != c.upcoming {
			return a.upcoming
		}
		if a.upcoming {
			return a.o.StartAt.Before(c.o.StartAt)
		}
		return a.o.StartAt.After(c.o.StartAt)
	})
	loc := b.calendarLocation()
	for _, h := range hits {
		if len(res.Occurrences) == limit {
			break
		}
		e, err := b.store.GetEvent(ctx, h.o.EventID)
		if err != nil {
			continue
		}
		res.Occurrences = append(res.Occurrences, apiOccurrence(h.o, e, v.cals[h.o.CalendarID], loc))
	}
	res.Total = min(total, eventSearchTotal)
	return res, nil
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

// calendarProviderOf is the running provider of an account; without one
// the account's calendar state answers (consent, sign-in) or offline.
func (b *Backend) calendarProviderOf(a store.Account) (calsync.Provider, error) {
	if !calsync.HasCalendars(a) {
		return nil, api.NewError(api.CodeCalendarNotFound, "the account's calendars are not synchronised")
	}
	eng := b.calendarEngine()
	if eng != nil {
		if p, ok := eng.Provider(a.ID); ok {
			return p, nil
		}
		if st, ok := eng.CalendarState(a.ID); ok && st.Error != nil {
			switch st.Status {
			case api.CalendarSyncConsentRequired, api.CalendarSyncAuthRequired:
				return nil, st.Error
			}
		}
	}
	return nil, api.NewError(api.CodeOffline, "the account's calendar is not connected")
}

// writeError maps a provider's failure: ErrUnsupported is invalidArgument,
// a conflict carries the server's current event (api.ConflictEventData),
// anything not an *api.Error is serverError.
func (b *Backend) writeError(ctx context.Context, s *shownEvent, res calsync.WriteResult, err error) error {
	if errors.Is(err, calsync.ErrUnsupported) {
		return api.NewError(api.CodeInvalidArgument, "not supported by this calendar")
	}
	var ae *api.Error
	if !errors.As(err, &ae) {
		if errors.Is(err, context.DeadlineExceeded) {
			return api.NewError(api.CodeServerTimeout, "the calendar server did not answer in time")
		}
		return api.NewError(api.CodeServerError, "%v", err)
	}
	if ae.Code == api.CodeConflict {
		var obj *calendar.Object
		switch d := ae.Data.(type) {
		case *calendar.Object:
			obj = d
		case calendar.Object:
			obj = &d
		}
		if obj == nil && len(res.Objects) > 0 {
			obj = &res.Objects[0]
		}
		out := *ae
		out.Data = nil
		if obj != nil && s != nil {
			out.Data = api.ConflictEventData{Event: b.objectEvent(ctx, *s, *obj)}
		}
		if eng := b.calendarEngine(); eng != nil && s != nil {
			eng.Touch(s.cal.ID)
		}
		return &out
	}
	if ae.Data != nil {
		if _, ok := ae.Data.(*calendar.Object); ok {
			out := *ae
			out.Data = nil
			return &out
		}
	}
	return ae
}

// objectEvent is an object from the server (not stored) as the event of
// the instance s shows, keeping the stored ids.
func (b *Backend) objectEvent(ctx context.Context, s shownEvent, obj calendar.Object) api.Event {
	rows := calsync.StoreEvents(obj)
	if len(rows) == 0 {
		return b.apiEvent(ctx, s, b.calendarLocation())
	}
	key := s.occ.OccurrenceKey
	pick := rows[0]
	var master *store.Event
	for i := range rows {
		r := rows[i]
		r.ID, r.CalendarID, r.AccountID = s.row.ID, s.cal.ID, s.acc.ID
		if r.Kind != store.EventOverride && master == nil {
			m := r
			if s.master != nil {
				m.ID = s.master.ID
			}
			master = &m
		}
		if key != "" && r.RecurrenceID == key {
			pick = r
		}
	}
	pick.ID, pick.CalendarID, pick.AccountID = s.row.ID, s.cal.ID, s.acc.ID
	t := shownEvent{row: pick, cal: s.cal, acc: s.acc, occ: s.occ}
	if master != nil && master.Kind == store.EventMaster {
		t.master = master
	}
	if pick.RecurrenceID == "" && pick.Kind != store.EventMaster {
		t.occ = syntheticOccurrence(pick)
		t.occ.EventID = pick.ID
	} else if pick.Kind == store.EventOverride {
		o := syntheticOccurrence(pick)
		o.EventID, o.SeriesID = s.occ.EventID, s.occ.SeriesID
		t.occ = o
	}
	return b.apiEvent(ctx, t, b.calendarLocation())
}

// checkMail validates a MailText.
func checkMail(m *api.MailText) error {
	if m != nil && (len(m.Subject) > api.MaxMailTextBytes || len(m.Text) > api.MaxMailTextBytes) {
		return api.NewError(api.CodeInvalidArgument, "mail subject or text over %d bytes", api.MaxMailTextBytes)
	}
	return nil
}

// loadAttachments reads attachment.import ids for an event (D21): ≤ 10,
// each ≤ 3 MiB, of the calendar's account or the account its invitations
// leave from.
func (b *Backend) loadAttachments(ctx context.Context, ids []string, accounts ...string) ([]calendar.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > api.MaxEventAttachments {
		return nil, api.NewError(api.CodeInvalidArgument, "more than %d attachments", api.MaxEventAttachments)
	}
	var rows []store.Attachment
	found := false
	for _, acc := range accounts {
		if acc == "" {
			continue
		}
		r, err := b.store.GetAttachments(ctx, acc, ids)
		if err == nil {
			rows, found = r, true
			break
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
	}
	if !found {
		return nil, api.NewError(api.CodeAttachmentNotFound, "unknown attachment")
	}
	out := make([]calendar.Attachment, 0, len(rows))
	for _, r := range rows {
		if r.Size > api.MaxEventAttachmentBytes {
			return nil, tooBig(api.MaxEventAttachmentBytes, r.Size)
		}
		data, err := os.ReadFile(b.store.AttachmentPath(r.ID))
		if err != nil {
			return nil, api.NewError(api.CodeAttachmentNotFound, "attachment data unreadable")
		}
		if len(data) > api.MaxEventAttachmentBytes {
			return nil, tooBig(api.MaxEventAttachmentBytes, int64(len(data)))
		}
		out = append(out, calendar.Attachment{Name: r.Filename, MediaType: r.ContentType, Size: int64(len(data)), Data: data})
	}
	return out, nil
}

// base64Size is the size of n bytes in base64 with 76-column lines.
func base64Size(n int64) int64 {
	enc := (n + 2) / 3 * 4
	return enc + enc/76*2 + 2
}

// checkIMIPSize answers attachmentTooBig when the iMIP REQUEST of ev
// with its files would be over the outbox's limit (§4.8), before anything
// is written.
func checkIMIPSize(ev calendar.Event, atts []calendar.Attachment, mail *api.MailText) error {
	if len(atts) == 0 {
		return nil
	}
	data, err := calendar.ToICS(calendar.Object{Master: &ev}, calendar.WriteOptions{Now: time.Now(), Method: "REQUEST", IgnoreRaw: true})
	if err != nil {
		return api.NewError(api.CodeInvalidArgument, "the event does not encode")
	}
	n := int64(imipOverhead) + base64Size(int64(len(data)))
	if mail != nil {
		n += int64(len(mail.Subject)) + 2*int64(len(mail.Text))
	}
	for _, a := range atts {
		n += base64Size(int64(len(a.Data))) + 512
	}
	if n > outgoingLimit {
		return tooBig(outgoingLimit, n)
	}
	return nil
}

// writeContext bounds a write's server round trip.
func writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), eventWriteTimeout)
}

// applyWrite stores what the server answered (logged on failure: the next
// pass brings it anyway).
func (b *Backend) applyWrite(ctx context.Context, a store.Account, calendarID string, res calsync.WriteResult) {
	if len(res.Objects) == 0 && len(res.Removed) == 0 {
		if eng := b.calendarEngine(); eng != nil {
			eng.Touch(calendarID)
		}
		return
	}
	if eng := b.calendarEngine(); eng != nil {
		if err := eng.ApplyWrite(context.WithoutCancel(ctx), a, calendarID, res); err != nil {
			b.log.Warn("calendar write not stored", "account", a.ID, "calendar", calendarID, "err", err)
		}
	}
}

// resultEvent is the stored event of a written object (its master or
// single row) as an instance (rid "" = its first), or the object itself
// when it was not stored (outside the window).
func (b *Backend) resultEvent(ctx context.Context, cal store.Calendar, acc store.Account, obj calendar.Object, rid api.DateOrTime) (api.Event, error) {
	rows, err := b.store.EventsByRemoteID(ctx, cal.ID, obj.RemoteID)
	if err == nil && len(rows) > 0 {
		id := rows[0].ID
		for _, r := range rows {
			if r.Kind != store.EventOverride {
				id = r.ID
				break
			}
		}
		if sh, err := b.shown(ctx, id, rid); err == nil {
			return b.apiEvent(ctx, sh, b.calendarLocation()), nil
		}
		if sh, err := b.shown(ctx, id, ""); err == nil {
			return b.apiEvent(ctx, sh, b.calendarLocation()), nil
		}
	}
	srows := calsync.StoreEvents(obj)
	if len(srows) == 0 {
		return api.Event{}, api.NewError(api.CodeServerError, "the server answered no event")
	}
	r := srows[0]
	r.CalendarID, r.AccountID = cal.ID, acc.ID
	sh := shownEvent{row: r, cal: cal, acc: acc, occ: syntheticOccurrence(r)}
	if r.Kind == store.EventMaster {
		sh.master = &r
	}
	ev := b.apiEvent(ctx, sh, b.calendarLocation())
	ev.EventID, ev.SeriesID = "", ""
	return ev, nil
}

func (s *eventService) Create(ctx context.Context, p api.EventCreateParams) (*api.EventCreateResult, error) {
	b := s.b
	cal, acc, err := b.requireCalendar(ctx, string(p.CalendarID))
	if err != nil {
		return nil, err
	}
	if !acc.Enabled || !calsync.HasCalendars(acc) {
		return nil, api.NewError(api.CodeCalendarNotFound, "the calendar's account is paused")
	}
	if calendarReadOnly(cal) {
		return nil, api.NewError(api.CodeReadOnly, "the calendar is read-only")
	}
	if err := checkMail(p.Mail); err != nil {
		return nil, err
	}
	prefs, _ := b.store.CalendarPreferences(ctx)
	org := api.Address{Name: acc.Config.DisplayName, Address: acc.Config.Email}
	ev, err := calendar.FromInput(p.Event, calendar.InputOptions{UID: caldav.NewUID(), Organizer: &org, DefaultAlertMinutes: prefs.DefaultAlertMinutes})
	if err != nil {
		return nil, err
	}
	send := p.SendInvitations == nil || *p.SendInvitations
	src := calendarSource(acc)
	if src == api.CalendarSourceGraph && len(ev.Attendees) > 0 && !send {
		return nil, api.NewError(api.CodeInvalidArgument, "Exchange always sends the invitations")
	}
	prov, err := b.calendarProviderOf(acc)
	if err != nil {
		return nil, err
	}
	feats := prov.Features()
	if ev.OnlineMeetingProvider != "" && !feats.OnlineMeetings {
		return nil, api.NewError(api.CodeInvalidArgument, "this calendar cannot create a video call")
	}
	atts, err := b.loadAttachments(ctx, p.Event.Attachments, acc.ID, string(feats.MailAccountID))
	if err != nil {
		return nil, err
	}
	if len(ev.Attendees) > 0 && send && feats.Invitations == api.InvitationsViaMail {
		if err := checkIMIPSize(ev, atts, p.Mail); err != nil {
			return nil, err
		}
	}
	opts := calsync.WriteOptions{Notify: send, Mail: p.Mail, MailAccountID: feats.MailAccountID, Attachments: atts}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	res, werr := prov.Create(wctx, cal, ev, opts)
	b.applyWrite(ctx, acc, cal.ID, res)
	if werr != nil {
		return nil, b.writeError(ctx, nil, res, werr)
	}
	if len(res.Objects) == 0 {
		return nil, api.NewError(api.CodeServerError, "the server answered no event")
	}
	out, err := b.resultEvent(ctx, cal, acc, res.Objects[0], "")
	if err != nil {
		return nil, err
	}
	return &api.EventCreateResult{Event: out, Invitations: invitationsSent(res.Invitations)}, nil
}

func invitationsSent(v api.InvitationsSent) api.InvitationsSent {
	if v.Via == "" {
		v.Via = api.InvitationsViaNone
	}
	return v
}

// writeTarget resolves an event write: the instance, its writable
// calendar and the account's provider.
func (b *Backend) writeTarget(ctx context.Context, eventID string, rid api.DateOrTime, etag string) (shownEvent, calsync.Provider, error) {
	if etag == "" {
		return shownEvent{}, nil, api.NewError(api.CodeInvalidArgument, "etag is required")
	}
	sh, err := b.shown(ctx, eventID, rid)
	if err != nil {
		return shownEvent{}, nil, err
	}
	if !sh.acc.Enabled {
		return shownEvent{}, nil, api.NewError(api.CodeReadOnly, "the account is paused")
	}
	if calendarReadOnly(sh.cal) {
		return shownEvent{}, nil, api.NewError(api.CodeReadOnly, "the calendar is read-only")
	}
	prov, err := b.calendarProviderOf(sh.acc)
	if err != nil {
		return shownEvent{}, nil, err
	}
	return sh, prov, nil
}

// instanceKey is the instance a write names: the request's recurrence id,
// else the override's own.
func instanceKey(sh shownEvent, rid api.DateOrTime) api.DateOrTime {
	if rid != "" {
		return rid
	}
	if sh.row.Kind == store.EventOverride {
		return calsync.ModelKey(sh.row.RecurrenceID)
	}
	return ""
}

func validScope(s api.EditScope, allowFuture bool) bool {
	switch s {
	case api.EditScopeThis, api.EditScopeAll:
		return true
	case api.EditScopeThisAndFuture:
		return allowFuture
	}
	return false
}

// modelOf is the model event of an instance from the stored rows of its
// resource: the override of key, else the master (or single event).
func (b *Backend) modelOf(ctx context.Context, sh shownEvent, key string) (calendar.Event, bool) {
	rows, err := b.store.EventsByRemoteID(ctx, sh.cal.ID, sh.root().RemoteID)
	if err != nil || len(rows) == 0 {
		return calendar.Event{}, false
	}
	obj := calsync.ObjectOf(rows)
	if key != "" {
		if o := obj.Override(calsync.ModelKey(key)); o != nil {
			return *o, true
		}
	}
	if obj.Master != nil {
		return *obj.Master, true
	}
	return calendar.Event{}, false
}

// attendeeEdit is an attendee's update: only alerts, showAs and private
// may differ from the stored event; anything else is readOnly.
func attendeeEdit(cur calendar.Event, occ store.Occurrence, in calendar.Event, keepRecurrence bool) (calendar.Event, error) {
	refuse := func() (calendar.Event, error) {
		return calendar.Event{}, api.NewError(api.CodeReadOnly, "an attendee may change only alerts, show as and private")
	}
	if in.Title != cur.Title || in.Location != cur.Location || in.Notes != cur.Notes || in.AllDay != cur.AllDay {
		return refuse()
	}
	start, end := occ.StartAt, occ.EndAt
	if cur.AllDay {
		if in.StartDate != api.Date(start.UTC().Format(time.DateOnly)) || in.EndDate != api.Date(end.UTC().Format(time.DateOnly)) {
			return refuse()
		}
	} else if cur.Floating {
		if !wallIn(start, in.Loc()).Equal(in.Start) || !wallIn(end, in.Loc()).Equal(in.End) {
			return refuse()
		}
	} else if !in.Start.Equal(start) || !in.End.Equal(end) {
		return refuse()
	}
	if !keepRecurrence && in.RRule != cur.RRule && !(cur.Kind == calendar.KindOverride && in.RRule == "") {
		return refuse()
	}
	for _, a := range in.Attendees {
		found := false
		for _, c := range cur.Attendees {
			found = found || strings.EqualFold(a.Address, c.Address)
		}
		if !found {
			return refuse()
		}
	}
	out := cur
	out.Attendees = slices.Clone(cur.Attendees)
	out.Alarms, out.ShowAs, out.Private = in.Alarms, in.ShowAs, in.Private
	if !cur.Floating {
		// The instance's times (the same instants): an override made from
		// the master must not take the series' first start.
		out.Start, out.End, out.StartDate, out.EndDate = in.Start, in.End, in.StartDate, in.EndDate
	}
	return out, nil
}

// organizerEdit keeps what the user did not edit of the stored event: the
// organiser, and the answers and kinds of attendees still on the list.
func organizerEdit(cur calendar.Event, in calendar.Event) calendar.Event {
	if cur.Organizer != nil && cur.Organizer.Address != "" && len(in.Attendees) > 0 {
		o := *cur.Organizer
		in.Organizer, in.IAmOrganizer = &o, true
	}
	for i, a := range in.Attendees {
		for _, c := range cur.Attendees {
			if strings.EqualFold(a.Address, c.Address) {
				a.Response, a.Type, a.Me, a.Organizer = c.Response, c.Type, c.Me, c.Organizer
				if a.Name == "" {
					a.Name = c.Name
				}
				if a.Role == api.AttendeeRequired && c.Role == api.AttendeeChair {
					a.Role = c.Role
				}
				in.Attendees[i] = a
			}
		}
	}
	in.Sequence = cur.Sequence
	in.UID = cur.UID
	return in
}

func (s *eventService) Update(ctx context.Context, p api.EventUpdateParams) (*api.EventUpdateResult, error) {
	b := s.b
	if !validScope(p.Scope, true) {
		return nil, api.NewError(api.CodeInvalidArgument, "bad scope")
	}
	if err := checkMail(p.Mail); err != nil {
		return nil, err
	}
	sh, prov, err := b.writeTarget(ctx, string(p.EventID), p.RecurrenceID, p.ETag)
	if err != nil {
		return nil, err
	}
	rid := instanceKey(sh, p.RecurrenceID)
	scope := p.Scope
	if !sh.series() {
		if scope != api.EditScopeAll && sh.row.Kind != store.EventOverride {
			return nil, api.NewError(api.CodeInvalidArgument, "the event does not repeat: scope must be all")
		}
	} else {
		root := sh.root()
		complex := root.RecurRefused || root.Recurrence != nil && root.Recurrence.Complex
		if complex && scope != api.EditScopeAll {
			return nil, api.NewError(api.CodeRecurrenceUnsupported, "the rule can be edited for the whole series only")
		}
		if scope != api.EditScopeAll && rid == "" {
			return nil, api.NewError(api.CodeInvalidArgument, "recurrenceId is required for this scope")
		}
	}
	prefs, _ := b.store.CalendarPreferences(ctx)
	org := api.Address{Name: sh.acc.Config.DisplayName, Address: sh.acc.Config.Email}
	in, err := calendar.FromInput(p.Event, calendar.InputOptions{UID: firstNonEmpty(sh.row.UID, caldav.NewUID()), Organizer: &org,
		DefaultAlertMinutes: prefs.DefaultAlertMinutes})
	if err != nil {
		return nil, err
	}
	key := ""
	if scope == api.EditScopeThis && rid != "" {
		key = calsync.StoredKey(rid)
	}
	cur, ok := b.modelOf(ctx, sh, key)
	if !ok {
		return nil, eventNotFound(string(p.EventID))
	}
	attendee := sh.row.MyResponse != "" && !sh.row.IAmOrganizer
	var next calendar.Event
	if attendee {
		occ := sh.occ
		if scope == api.EditScopeAll && sh.master != nil {
			occ = syntheticOccurrence(*sh.master)
		}
		if next, err = attendeeEdit(cur, occ, in, p.Event.KeepRecurrence || scope != api.EditScopeAll); err != nil {
			return nil, err
		}
		if scope == api.EditScopeAll && sh.master != nil {
			next.RRule, next.Recurrence, next.RDates, next.EXDates = sh.master.RRule, sh.master.Recurrence, cur.RDates, cur.EXDates
		}
	} else {
		next = organizerEdit(cur, in)
	}
	send := p.SendUpdates == nil || *p.SendUpdates
	src := calendarSource(sh.acc)
	if src == api.CalendarSourceGraph && !attendee && (len(next.Attendees) > 0 || len(cur.Attendees) > 0) && !send {
		return nil, api.NewError(api.CodeInvalidArgument, "Exchange always sends the updates")
	}
	feats := prov.Features()
	if next.OnlineMeetingProvider != "" && next.OnlineMeetingProvider != cur.OnlineMeetingProvider && !feats.OnlineMeetings {
		return nil, api.NewError(api.CodeInvalidArgument, "this calendar cannot create a video call")
	}
	atts, err := b.loadAttachments(ctx, p.Event.Attachments, sh.acc.ID, string(feats.MailAccountID))
	if err != nil {
		return nil, err
	}
	if !attendee && len(next.Attendees) > 0 && send && feats.Invitations == api.InvitationsViaMail {
		if err := checkIMIPSize(next, atts, p.Mail); err != nil {
			return nil, err
		}
	}
	change := calsync.Change{Scope: scope, RecurrenceID: rid, Event: next, KeepRecurrence: p.Event.KeepRecurrence}
	opts := calsync.WriteOptions{IfMatch: p.ETag, RecurrenceID: rid, Notify: send, Mail: p.Mail,
		MailAccountID: feats.MailAccountID, Attachments: atts}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	target := sh.root()
	res, werr := prov.Update(wctx, sh.cal, target, change, opts)
	b.applyWrite(ctx, sh.acc, sh.cal.ID, res)
	if werr != nil {
		return nil, b.writeError(ctx, &sh, res, werr)
	}
	out := &api.EventUpdateResult{DroppedExceptions: res.DroppedExceptions, Invitations: invitationsSent(res.Invitations)}
	resultObj := calendar.Object{RemoteID: target.RemoteID}
	resultRID := api.DateOrTime("")
	switch {
	case scope == api.EditScopeThis:
		resultRID = rid
	case scope == api.EditScopeThisAndFuture:
		for _, o := range res.Objects {
			if o.RemoteID != target.RemoteID {
				resultObj = o
			}
		}
	}
	for _, o := range res.Objects {
		if o.RemoteID == resultObj.RemoteID {
			resultObj = o
		}
	}
	ev, err := b.resultEvent(ctx, sh.cal, sh.acc, resultObj, resultRID)
	if err != nil {
		return nil, err
	}
	out.Event = ev
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *eventService) Delete(ctx context.Context, p api.EventDeleteParams) (*api.EventDeleteResult, error) {
	b := s.b
	if !validScope(p.Scope, true) {
		return nil, api.NewError(api.CodeInvalidArgument, "bad scope")
	}
	if len(p.Comment) > api.MaxCommentBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "comment over %d bytes", api.MaxCommentBytes)
	}
	if err := checkMail(p.Mail); err != nil {
		return nil, err
	}
	sh, prov, err := b.writeTarget(ctx, string(p.EventID), p.RecurrenceID, p.ETag)
	if err != nil {
		return nil, err
	}
	rid := instanceKey(sh, p.RecurrenceID)
	scope := p.Scope
	if !sh.series() && sh.row.Kind != store.EventOverride {
		scope = api.EditScopeAll
	}
	if scope != api.EditScopeAll && rid == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "recurrenceId is required for this scope")
	}
	notify := p.Notify == nil || *p.Notify
	feats := prov.Features()
	attendee := sh.row.MyResponse != "" && !sh.row.IAmOrganizer
	comment := calendar.CleanBlock(p.Comment, api.MaxCommentBytes)
	wctx, cancel := writeContext(ctx)
	defer cancel()
	target := sh.root()
	etag := p.ETag
	sent := api.InvitationsSent{Via: api.InvitationsViaNone}
	if attendee && notify {
		// Decline first (the organiser learns), then remove the copy.
		rscope := api.EditScopeAll
		if scope == api.EditScopeThis {
			rscope = api.EditScopeThis
		}
		rres, rerr := prov.Respond(wctx, sh.cal, target, calsync.Response{Scope: rscope, Response: api.ResponseDeclined},
			calsync.WriteOptions{IfMatch: etag, RecurrenceID: rid, Notify: true, Mail: p.Mail, MailAccountID: feats.MailAccountID, Comment: comment})
		b.applyWrite(ctx, sh.acc, sh.cal.ID, rres)
		if rerr != nil {
			return nil, b.writeError(ctx, &sh, rres, rerr)
		}
		sent = invitationsSent(rres.Invitations)
		b.recordResponse(ctx, sh, rscope, rid, api.ResponseDeclined, sent.Via)
		for _, o := range rres.Objects {
			if o.RemoteID == target.RemoteID && o.ETag != "" {
				etag = o.ETag
			}
		}
		if fresh, err := b.store.GetEvent(ctx, target.ID); err == nil {
			target = fresh
		}
		notify = false
	}
	opts := calsync.WriteOptions{IfMatch: etag, RecurrenceID: rid, Notify: notify && !attendee, Mail: p.Mail,
		MailAccountID: feats.MailAccountID, Comment: comment}
	res, werr := prov.Delete(wctx, sh.cal, target, scope, opts)
	b.applyWrite(ctx, sh.acc, sh.cal.ID, res)
	if werr != nil {
		return nil, b.writeError(ctx, &sh, res, werr)
	}
	if !attendee {
		sent = invitationsSent(res.Invitations)
	}
	return &api.EventDeleteResult{Invitations: sent}, nil
}

func (s *eventService) Respond(ctx context.Context, p api.EventRespondParams) (*api.EventRespondResult, error) {
	b := s.b
	if p.Scope != api.EditScopeThis && p.Scope != api.EditScopeAll {
		return nil, api.NewError(api.CodeInvalidArgument, "scope must be this or all")
	}
	switch p.Response {
	case api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "bad response")
	}
	if len(p.Comment) > api.MaxCommentBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "comment over %d bytes", api.MaxCommentBytes)
	}
	if err := checkMail(p.Mail); err != nil {
		return nil, err
	}
	if (p.ProposedStart == nil) != (p.ProposedEnd == nil) {
		return nil, api.NewError(api.CodeInvalidArgument, "a proposed time needs both ends")
	}
	sh, prov, err := b.writeTarget(ctx, string(p.EventID), p.RecurrenceID, p.ETag)
	if err != nil {
		return nil, err
	}
	if sh.row.MyResponse == "" || sh.row.IAmOrganizer {
		return nil, api.NewError(api.CodeInvalidArgument, "the user is not an attendee of the event")
	}
	loc := b.calendarLocation()
	if p.ProposedStart != nil {
		if !b.apiEvent(ctx, sh, loc).CanProposeNewTime {
			return nil, api.NewError(api.CodeInvalidArgument, "the organiser takes no proposed time")
		}
		if p.ProposedEnd.Before(*p.ProposedStart) {
			return nil, api.NewError(api.CodeInvalidArgument, "the proposed end is before its start")
		}
	}
	rid := instanceKey(sh, p.RecurrenceID)
	scope := p.Scope
	if !sh.series() && sh.row.Kind != store.EventOverride {
		scope = api.EditScopeAll
	}
	if scope == api.EditScopeThis && rid == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "recurrenceId is required for scope this")
	}
	send := p.SendResponse == nil || *p.SendResponse
	feats := prov.Features()
	r := calsync.Response{Scope: scope, Response: p.Response}
	if p.ProposedStart != nil {
		ps, pe := p.ProposedStart.UTC(), p.ProposedEnd.UTC()
		r.ProposedStart, r.ProposedEnd = &ps, &pe
	}
	opts := calsync.WriteOptions{IfMatch: p.ETag, RecurrenceID: rid, Notify: send, Mail: p.Mail,
		MailAccountID: feats.MailAccountID, Comment: calendar.CleanBlock(p.Comment, api.MaxCommentBytes)}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	target := sh.root()
	res, werr := prov.Respond(wctx, sh.cal, target, r, opts)
	b.applyWrite(ctx, sh.acc, sh.cal.ID, res)
	if werr != nil {
		return nil, b.writeError(ctx, &sh, res, werr)
	}
	via := invitationsSent(res.Invitations).Via
	b.recordResponse(ctx, sh, scope, rid, p.Response, via)
	resultRID := api.DateOrTime("")
	if scope == api.EditScopeThis {
		resultRID = rid
	}
	obj := calendar.Object{RemoteID: target.RemoteID}
	for _, o := range res.Objects {
		if o.RemoteID == target.RemoteID {
			obj = o
		}
	}
	ev, err := b.resultEvent(ctx, sh.cal, sh.acc, obj, resultRID)
	if err != nil {
		return nil, err
	}
	return &api.EventRespondResult{Event: ev, Via: via}, nil
}

// recordResponse gives the stored invitations of the event the answer
// (cal_responses: scope all answers every invitation of the UID, this the
// instance's) and tells the clients that unanswered invitations changed.
func (b *Backend) recordResponse(ctx context.Context, sh shownEvent, scope api.EditScope, rid api.DateOrTime, r api.EventResponse, via api.InvitationsVia) {
	invs := b.invitationsOfUID(ctx, sh.root().UID)
	if len(invs) == 0 {
		return
	}
	how := string(calendarSource(sh.acc))
	if via == api.InvitationsViaMail {
		how = "imip"
	}
	key := ""
	if scope == api.EditScopeThis {
		key = calsync.StoredKey(rid)
	}
	type k struct{ account, rid string }
	seq := map[k]int{}
	var order []k
	for _, inv := range invs {
		if inv.Method != api.InvitationMethodRequest || scope == api.EditScopeThis && inv.RecurrenceID != key {
			continue
		}
		x := k{inv.AccountID, inv.RecurrenceID}
		if _, ok := seq[x]; !ok {
			order = append(order, x)
			seq[x] = inv.Sequence
		}
		seq[x] = max(seq[x], inv.Sequence)
	}
	var accounts []api.AccountID
	for _, x := range order {
		if err := b.store.PutResponse(ctx, store.Response{AccountID: x.account, UID: sh.root().UID, RecurrenceID: x.rid,
			Sequence: seq[x], Response: r, Via: how, CalendarID: sh.cal.ID, EventID: sh.root().ID}); err != nil {
			b.log.Warn("invitation answer not recorded", "account", x.account, "err", err)
			continue
		}
		if !slices.Contains(accounts, api.AccountID(x.account)) {
			accounts = append(accounts, api.AccountID(x.account))
		}
	}
	if len(accounts) > 0 {
		if cn, ok := b.getNotifier().(api.CalendarNotifier); ok {
			cn.InvitationsChanged(api.InvitationsChangedNotification{AccountIDs: accounts})
		}
	}
}

// ---------------------------------------------------------------------------
// Alerts
// ---------------------------------------------------------------------------

func alertArgError(err error) error {
	switch apiCode(err) {
	case api.CodeEventNotFound, api.CodeConflict:
		return api.NewError(api.CodeInvalidArgument, "unknown alert, or not fired")
	}
	return err
}

func (s *eventService) SnoozeAlert(ctx context.Context, p api.EventSnoozeAlertParams) (*api.EventSnoozeAlertResult, error) {
	if p.AlertID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "alertId is required")
	}
	if p.Minutes < 1 || p.Minutes > 1440 {
		return nil, api.NewError(api.CodeInvalidArgument, "minutes out of 1–1440")
	}
	eng := s.b.calendarEngine()
	if eng == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "unknown alert")
	}
	if err := eng.SnoozeAlert(ctx, string(p.AlertID), time.Now().Add(time.Duration(p.Minutes)*time.Minute)); err != nil {
		return nil, alertArgError(err)
	}
	return &api.EventSnoozeAlertResult{}, nil
}

func (s *eventService) DismissAlert(ctx context.Context, p api.EventDismissAlertParams) (*api.EventDismissAlertResult, error) {
	if p.AlertID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "alertId is required")
	}
	eng := s.b.calendarEngine()
	if eng == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "unknown alert")
	}
	if err := eng.DismissAlert(ctx, string(p.AlertID)); err != nil {
		return nil, alertArgError(err)
	}
	return &api.EventDismissAlertResult{}, nil
}
