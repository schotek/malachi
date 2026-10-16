// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// calendar.freeBusy and calendar.findFreeTime (docs/calendar.md §4.4,
// D22): the user's busy time from the stored occurrences of every visible
// calendar of enabled accounts, other people's through the source of the
// account when it has features.freeBusy (Graph getSchedule). A failed or
// slow free/busy request of the source is never an error: those people
// are known: false.

// freeBusyTimeout is the budget of the source's free/busy request.
const freeBusyTimeout = 20 * time.Second

// freeBusyLimit caps the occurrences read for the user's busy time.
const freeBusyLimit = api.MaxEventListResults

type freeBusyQuery struct {
	account   *store.Account // nil: only the user
	attendees []string
	from, to  time.Time
	step      time.Duration
}

func (b *Backend) checkFreeBusy(ctx context.Context, accountID api.AccountID, attendees []string, from, to time.Time, required bool) (freeBusyQuery, error) {
	q := freeBusyQuery{from: from.UTC(), to: to.UTC()}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return q, api.NewError(api.CodeInvalidArgument, "from must be before to")
	}
	if to.Sub(from) > time.Duration(api.MaxFreeBusyDays)*24*time.Hour {
		return q, api.NewError(api.CodeInvalidArgument, "range over %d days", api.MaxFreeBusyDays)
	}
	if len(attendees) > api.MaxFreeBusyAttendees {
		return q, api.NewError(api.CodeInvalidArgument, "more than %d attendees", api.MaxFreeBusyAttendees)
	}
	for _, a := range attendees {
		addr := strings.ToLower(strings.TrimSpace(a))
		if validateAddress(api.Address{Address: addr}) != nil {
			return q, api.NewError(api.CodeInvalidArgument, "bad attendee address")
		}
		q.attendees = append(q.attendees, addr)
	}
	if accountID == "" {
		if required {
			return q, api.NewError(api.CodeInvalidArgument, "accountId is required")
		}
		return q, nil
	}
	a, err := b.requireAccount(ctx, string(accountID))
	if err != nil {
		return q, err
	}
	if !can(a.Config, api.CapabilityCalendar) {
		return q, api.NewError(api.CodeInvalidArgument, "account %q has no calendars", a.ID)
	}
	if st := b.calendarState(a); st.Status == api.CalendarSyncConsentRequired {
		if st.Error != nil {
			return q, st.Error
		}
		e := api.NewError(api.CodeCalendarConsentRequired, "the account's sign-in does not allow the calendar")
		e.Data = api.CalendarConsentRequiredData{AccountID: api.AccountID(a.ID), SignIn: a.Config.Graph != nil && a.Config.Graph.Source == api.GraphSourceDaemon}
		return q, e
	}
	q.account = &a
	return q, nil
}

// myBusy is the user's busy time in [from, to).
func (b *Backend) myBusy(ctx context.Context, from, to time.Time) ([]api.BusyInterval, error) {
	v, err := b.calendarView(ctx)
	if err != nil {
		return nil, err
	}
	busy := []api.BusyInterval{}
	ids := v.readableIDs()
	if len(ids) == 0 {
		return busy, nil
	}
	loc := b.calendarLocation()
	list, err := b.store.EventRange(ctx, store.OccurrenceQuery{From: from, To: to, Zone: loc, AccountIDs: ids, Limit: freeBusyLimit})
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	occs := make([]api.Occurrence, 0, len(list.Occurrences))
	for _, o := range list.Occurrences {
		e, ok := list.Events[o.EventID]
		c, okc := v.cals[o.CalendarID]
		if !ok || !okc {
			continue
		}
		occs = append(occs, apiOccurrence(o, e, c, loc))
	}
	for _, iv := range calendar.BusyIntervals(occs, loc) {
		s, e := iv.Start, iv.End
		if s.Before(from) {
			s = from
		}
		if e.After(to) {
			e = to
		}
		if s.Before(e) {
			busy = append(busy, api.BusyInterval{Start: s, End: e, ShowAs: iv.ShowAs})
		}
	}
	return busy, nil
}

// othersBusy asks the account's source about the attendees; unknown for
// everyone without a source with free/busy, or when it failed.
func (b *Backend) othersBusy(ctx context.Context, q freeBusyQuery) []api.Availability {
	out := make([]api.Availability, len(q.attendees))
	for i, a := range q.attendees {
		out[i] = api.Availability{Address: a, Busy: []api.BusyInterval{}}
	}
	if len(q.attendees) == 0 || q.account == nil {
		return out
	}
	eng := b.calendarEngine()
	if eng == nil {
		return out
	}
	prov, ok := eng.Provider(q.account.ID)
	if !ok || !prov.Features().FreeBusy {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, freeBusyTimeout)
	defer cancel()
	step := q.step
	if step <= 0 {
		step = 30 * time.Minute
	}
	got, err := prov.FreeBusy(ctx, q.attendees, q.from, q.to, step)
	if err != nil {
		b.log.Info("free/busy not answered", "account", q.account.ID, "err", err)
		return out
	}
	byAddr := map[string]calsync.Availability{}
	for _, g := range got {
		byAddr[strings.ToLower(strings.TrimSpace(g.Address))] = g
	}
	for i, a := range q.attendees {
		g, ok := byAddr[a]
		if !ok || !g.Known {
			continue
		}
		out[i].Known = true
		for _, iv := range g.Busy {
			if iv.Start.Before(iv.End) {
				out[i].Busy = append(out[i].Busy, api.BusyInterval{Start: iv.Start.UTC(), End: iv.End.UTC(), ShowAs: showAsOf(iv.ShowAs)})
			}
		}
	}
	return out
}

// meAddress is the user's address of the account (else of the first
// readable account).
func (b *Backend) meAddress(ctx context.Context, a *store.Account) string {
	if a != nil {
		return a.Config.Email
	}
	if list, err := b.readableCalendarAccounts(ctx); err == nil && len(list) > 0 {
		return list[0].Config.Email
	}
	return ""
}

func (s *calendarService) FreeBusy(ctx context.Context, p api.CalendarFreeBusyParams) (*api.CalendarFreeBusyResult, error) {
	b := s.b
	q, err := b.checkFreeBusy(ctx, p.AccountID, p.Attendees, p.From, p.To, true)
	if err != nil {
		return nil, err
	}
	switch p.IntervalMinutes {
	case 0, 15, 30, 60:
		q.step = time.Duration(p.IntervalMinutes) * time.Minute
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "intervalMinutes is 15, 30 or 60")
	}
	mine, err := b.myBusy(ctx, q.from, q.to)
	if err != nil {
		return nil, err
	}
	return &api.CalendarFreeBusyResult{
		Me:        api.Availability{Address: b.meAddress(ctx, q.account), Known: true, Busy: mine},
		Attendees: b.othersBusy(ctx, q),
	}, nil
}

func (s *calendarService) FindFreeTime(ctx context.Context, p api.CalendarFindFreeTimeParams) (*api.CalendarFindFreeTimeResult, error) {
	b := s.b
	q, err := b.checkFreeBusy(ctx, p.AccountID, p.Attendees, p.From, p.To, false)
	if err != nil {
		return nil, err
	}
	if p.DurationMinutes <= 0 || time.Duration(p.DurationMinutes)*time.Minute > q.to.Sub(q.from) {
		return nil, api.NewError(api.CodeInvalidArgument, "durationMinutes must be positive and fit the range")
	}
	limit := p.Limit
	switch {
	case limit < 0 || limit > api.MaxFreeSlots:
		return nil, api.NewError(api.CodeInvalidArgument, "limit out of 1–%d", api.MaxFreeSlots)
	case limit == 0:
		limit = 5
	}
	busy, err := b.myBusy(ctx, q.from, q.to)
	if err != nil {
		return nil, err
	}
	res := &api.CalendarFindFreeTimeResult{Slots: []api.TimeSlot{}, Unknown: []string{}}
	for _, av := range b.othersBusy(ctx, q) {
		if !av.Known {
			res.Unknown = append(res.Unknown, av.Address)
			continue
		}
		busy = append(busy, av.Busy...)
	}
	var wh *api.WorkingHours
	if p.WorkingHoursOnly == nil || *p.WorkingHoursOnly {
		prefs, err := b.store.CalendarPreferences(ctx)
		if err != nil {
			prefs = api.DefaultCalendarPreferences()
		}
		wh = &prefs.WorkingHours
	}
	slots, err := calendar.FreeSlots(busy, q.from, q.to, time.Duration(p.DurationMinutes)*time.Minute, wh,
		b.calendarLocation(), calendar.DefaultSlotStep, limit)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "working hours do not read")
	}
	res.Slots = append(res.Slots, slots...)
	return res, nil
}
