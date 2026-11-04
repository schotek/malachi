// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The alert scheduler (D12, docs/calendar.md §3.7): cal_alerts holds the
// alarms of the next 7 days with the user's state; one timer waits for
// the next due one.

const (
	alertHorizon = 7 * 24 * time.Hour
	// alertLate is how late an alert may still be announced (its
	// occurrence not ended); a later one is marked fired silently.
	alertLate = 15 * time.Minute
	// alertRefresh recomputes the alerts this often, so that the 7-day
	// horizon moves without a change of the calendars.
	alertRefresh = time.Hour
	// alertLookahead bounds the occurrences read: an alarm may lie up to
	// api.MaxAlertMinutes before its start.
	alertLookahead  = alertHorizon + time.Duration(api.MaxAlertMinutes)*time.Minute
	alertQueryLimit = api.MaxEventListResults
)

type alertScheduler struct {
	sv    *Supervisor
	kickc chan struct{}

	mu       sync.Mutex
	dirty    bool
	computed time.Time
}

func newAlertScheduler(sv *Supervisor) *alertScheduler {
	return &alertScheduler{sv: sv, kickc: make(chan struct{}, 1), dirty: true}
}

// kick asks for a recomputation of the alerts.
func (a *alertScheduler) kick() {
	a.mu.Lock()
	a.dirty = true
	a.mu.Unlock()
	a.poke()
}

func (a *alertScheduler) poke() {
	select {
	case a.kickc <- struct{}{}:
	default:
	}
}

// changed is told of every flushed calendar change: one that may touch
// the alerts' horizon recomputes them.
func (a *alertScheduler) changed(_ string, s span) {
	if s.empty {
		return
	}
	if !s.unbounded {
		now := a.sv.clock.Now()
		if !s.to.After(now.Add(-24*time.Hour)) || !s.from.Before(now.Add(alertLookahead+24*time.Hour)) {
			return
		}
	}
	a.kick()
}

func (a *alertScheduler) run(ctx context.Context) {
	for ctx.Err() == nil {
		now := a.sv.clock.Now()
		a.mu.Lock()
		dirty := a.dirty || !now.Before(a.computed.Add(alertRefresh))
		a.dirty = false
		a.mu.Unlock()
		if dirty {
			if err := a.recompute(ctx, now); err != nil && ctx.Err() == nil {
				a.sv.log.Warn("alerts not recomputed", "err", err)
			}
			a.mu.Lock()
			a.computed = now
			a.mu.Unlock()
		}
		if err := a.fireDue(ctx, now); err != nil && ctx.Err() == nil {
			a.sv.log.Warn("alerts not fired", "err", err)
		}
		wait := a.computed.Add(alertRefresh).Sub(now)
		if next, ok, err := a.sv.opts.Store.NextAlert(ctx); err == nil && ok && next.Sub(now) < wait {
			wait = next.Sub(now)
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		tick := make(chan struct{}, 1)
		stop := a.sv.clock.AfterFunc(wait, func() { tick <- struct{}{} })
		select {
		case <-ctx.Done():
		case <-a.kickc:
		case <-tick:
		}
		stop()
	}
}

// recompute makes cal_alerts the alarms of the occurrences of the running
// accounts' visible calendars whose alert falls before now + 7 days and
// whose occurrence has not ended (declined events have none, paused
// accounts — no running syncer — none either).
func (a *alertScheduler) recompute(ctx context.Context, now time.Time) error {
	st := a.sv.opts.Store
	accounts := a.sv.runningAccounts()
	var want []store.Alert
	if len(accounts) > 0 {
		prefs := a.sv.preferences(ctx)
		startH, startM := workingStart(prefs.WorkingHours.Start)
		list, err := st.EventRange(ctx, store.OccurrenceQuery{
			From: now, To: now.Add(alertLookahead), Zone: a.sv.loc, AccountIDs: accounts, Limit: alertQueryLimit,
		})
		if err != nil {
			return err
		}
		horizon := now.Add(alertHorizon)
		for _, o := range list.Occurrences {
			ev, ok := list.Events[o.EventID]
			if !ok || ev.Status == api.EventStatusCancelled || ev.MyResponse == api.ResponseDeclined {
				continue
			}
			start, end := a.instants(o, startH, startM)
			if !end.After(now) && !(end.Equal(start) && !start.Before(now)) {
				continue
			}
			seen := map[int]bool{}
			for _, m := range ev.Alarms {
				if seen[m] || m < -api.MaxAlertMinutes || m > api.MaxAlertMinutes {
					continue
				}
				seen[m] = true
				due := start.Add(-time.Duration(m) * time.Minute)
				if due.After(horizon) {
					continue
				}
				want = append(want, store.Alert{EventID: o.EventID, OccurrenceKey: o.OccurrenceKey, MinutesBefore: m, DueAt: due})
			}
		}
	}
	_, err := st.SyncAlerts(ctx, nil, want)
	return err
}

// instants are the start an alert counts from and the end of an
// occurrence: a timed one's own; an all-day one's date at the working-day
// start and its end date's midnight in the daemon's zone; a floating
// one's wall clock in the daemon's zone.
func (a *alertScheduler) instants(o store.Occurrence, h, m int) (time.Time, time.Time) {
	loc := a.sv.loc
	switch {
	case o.AllDay:
		s, e := o.StartAt.UTC(), o.EndAt.UTC()
		return time.Date(s.Year(), s.Month(), s.Day(), h, m, 0, 0, loc), time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, loc)
	case o.Floating:
		return asWall(o.StartAt, loc), asWall(o.EndAt, loc)
	}
	return o.StartAt, o.EndAt
}

// asWall reads t's UTC fields as a wall clock in loc.
func asWall(t time.Time, loc *time.Location) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), u.Minute(), u.Second(), u.Nanosecond(), loc)
}

// workingStart reads "HH:MM" (08:00 when it does not read).
func workingStart(s string) (int, int) {
	hh, mm, ok := strings.Cut(s, ":")
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if !ok || err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 8, 0
	}
	return h, m
}

// fireDue fires the pending alerts that are due: announced when at most
// 15 minutes late and the occurrence has not ended, else marked fired
// silently.
func (a *alertScheduler) fireDue(ctx context.Context, now time.Time) error {
	st := a.sv.opts.Store
	due, err := st.DueAlerts(ctx, now)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}
	prefs := a.sv.preferences(ctx)
	h, m := workingStart(prefs.WorkingHours.Start)
	sort.SliceStable(due, func(i, j int) bool { return due[i].When().Before(due[j].When()) })
	for _, al := range due {
		info, end, ok := a.describe(ctx, al, h, m)
		announce := ok && now.Sub(al.When()) <= alertLate && (end.After(now) || end.Equal(info.startInstant) && !end.Before(now))
		if err := st.FireAlert(ctx, al.ID); err != nil {
			if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if announce {
			info.alert.State = api.AlertDue
			a.sv.notify.alert(info.alert)
		}
	}
	return nil
}

type alertInfo struct {
	alert        api.EventAlert
	startInstant time.Time
}

// describe builds the notification of an alert from its occurrence and
// event; false when either is gone.
func (a *alertScheduler) describe(ctx context.Context, al store.Alert, h, m int) (alertInfo, time.Time, bool) {
	st := a.sv.opts.Store
	o, err := st.GetOccurrence(ctx, al.EventID, al.OccurrenceKey)
	if err != nil {
		return alertInfo{}, time.Time{}, false
	}
	ev, err := st.GetEvent(ctx, al.EventID)
	if err != nil {
		return alertInfo{}, time.Time{}, false
	}
	start, end := a.instants(o, h, m)
	out := api.EventAlert{
		AlertID: api.AlertID(al.ID), EventID: api.EventID(ev.ID), RecurrenceID: ModelKey(al.OccurrenceKey),
		AccountID: api.AccountID(o.AccountID), CalendarID: api.CalendarID(o.CalendarID),
		Title: ev.Title, AllDay: o.AllDay, Location: ev.Location, VideoCall: ev.VideoURL != "",
		MinutesBefore: al.MinutesBefore,
	}
	if o.AllDay {
		out.StartDate = api.Date(o.StartAt.UTC().Format(time.DateOnly))
	} else {
		s, e := start.UTC(), end.UTC()
		out.Start, out.End = &s, &e
	}
	return alertInfo{alert: out, startInstant: start}, end, true
}

// SnoozeAlert makes a fired alert due again at until (event.snoozeAlert):
// the scheduler re-arms and announces it then. Errors: eventNotFound for
// an unknown alert, conflict for one that has not fired.
func (sv *Supervisor) SnoozeAlert(ctx context.Context, alertID string, until time.Time) error {
	if err := alertError(sv.opts.Store.SnoozeAlert(ctx, alertID, until)); err != nil {
		return err
	}
	sv.alerts.poke()
	return nil
}

// DismissAlert marks an alert dismissed (event.dismissAlert) and sends
// notify.eventAlert dismissed, so that other clients withdraw theirs.
func (sv *Supervisor) DismissAlert(ctx context.Context, alertID string) error {
	st := sv.opts.Store
	al, err := st.GetAlert(ctx, alertID)
	if err != nil {
		return alertError(err)
	}
	if err := alertError(st.DismissAlert(ctx, alertID)); err != nil {
		return err
	}
	prefs := sv.preferences(ctx)
	h, m := workingStart(prefs.WorkingHours.Start)
	info, _, ok := sv.alerts.describe(ctx, al, h, m)
	if !ok {
		info.alert = api.EventAlert{AlertID: api.AlertID(al.ID), EventID: api.EventID(al.EventID),
			RecurrenceID: ModelKey(al.OccurrenceKey), MinutesBefore: al.MinutesBefore}
	}
	info.alert.State = api.AlertDismissed
	sv.notify.alert(info.alert)
	sv.alerts.poke()
	return nil
}

func alertError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeEventNotFound, "alert not found")
	case errors.Is(err, store.ErrConflict):
		return api.NewError(api.CodeConflict, "the alert has not fired")
	}
	return api.NewError(api.CodeStorageError, "%s", cleanMessage(err.Error()))
}
