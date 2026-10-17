// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The account's window (docs/calendar.md §2.3): from today − keepDays to
// today + api.CalendarFutureDays, days of the daemon's zone.

// rebaselineSlack is how far a Graph calendar's token window may lag
// behind the current window before a new delta chain starts.
const rebaselineSlack = 7 * 24 * time.Hour

// KeepDays resolves an account's CalendarConfig.KeepDays (0 or absent =
// api.DefaultCalendarKeepDays).
func KeepDays(a store.Account) int {
	if c := a.Config.Calendar; c != nil && c.KeepDays > 0 {
		return c.KeepDays
	}
	return api.DefaultCalendarKeepDays
}

// WindowFor is the window of an account keeping keepDays, at now in loc
// (nil = UTC): midnight of today − keepDays to midnight of today + 400.
func WindowFor(now time.Time, keepDays int, loc *time.Location) Window {
	if loc == nil {
		loc = time.UTC
	}
	if keepDays <= 0 {
		keepDays = api.DefaultCalendarKeepDays
	}
	l := now.In(loc)
	today := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	return Window{From: today.AddDate(0, 0, -keepDays).UTC(), To: today.AddDate(0, 0, api.CalendarFutureDays).UTC()}
}

// windowDates are the window's edges as dates of loc (cal_calendars
// window_from/window_to, api.CalendarWindow).
func windowDates(w Window, loc *time.Location) (string, string) {
	if loc == nil {
		loc = time.UTC
	}
	return w.From.In(loc).Format(time.DateOnly), w.To.In(loc).Format(time.DateOnly)
}

// APIWindow is the window as the API shows it.
func APIWindow(w Window, loc *time.Location) api.CalendarWindow {
	f, t := windowDates(w, loc)
	return api.CalendarWindow{From: api.Date(f), To: api.Date(t)}
}

// needsRebaseline is the Graph rule: the stored token was made for a
// window whose start lies more than 7 days from the current one (the
// window moved on, or keepDays changed). No stored window: no token to
// keep either.
func needsRebaseline(storedFrom string, w Window, loc *time.Location) bool {
	if storedFrom == "" {
		return false
	}
	if loc == nil {
		loc = time.UTC
	}
	f, err := time.ParseInLocation(time.DateOnly, storedFrom, loc)
	if err != nil {
		return true
	}
	d := w.From.Sub(f)
	if d < 0 {
		d = -d
	}
	return d > rebaselineSlack
}

// ExpandKey is the value of meta calendar.expand for this build:
// "<recur.Version>.<calendar.ExpandVersion>:<tzdata>", where the tzdata
// version is the Go release whose embedded time/tzdata the daemon carries
// (Go exposes no tzdata version of its own).
func ExpandKey() string {
	return fmt.Sprintf("%d.%d:%s", recur.Version, calendar.ExpandVersion, runtime.Version())
}

// daemonExpanded reports whether a stored calendar's occurrences are the
// daemon's (every source but Graph, whose sync_mode is "delta" or
// "list").
func daemonExpanded(c store.Calendar) bool {
	return c.SyncMode != "delta" && c.SyncMode != "list" && c.SyncMode != SyncModeDisabled
}

// SyncModeDisabled is cal_calendars.sync_mode of a calendar the account
// lists in CalendarConfig.DisabledCalendars: its row is kept (the server's
// fields, the user's columns) with no events, occurrences, resources or
// alerts, and it is never synchronised (calendar.list shows it as
// Calendar.disabled).
const SyncModeDisabled = "disabled"

// Disabled reports whether a stored calendar is disabled (SyncModeDisabled).
func Disabled(c store.Calendar) bool { return c.SyncMode == SyncModeDisabled }

// syncModeDaemon is cal_calendars.sync_mode of a daemon-expanded calendar
// that has been synchronised.
const syncModeDaemon = "daemon"

// rebuildExpansions compares meta calendar.expand with key and, when it
// differs, rebuilds the occurrences of every daemon-expanded calendar of
// the store from cal_events (no network), each in the window of its
// account (windowOf; nil or false = the default keepDays), then stores
// key. Returns the calendars rebuilt.
func rebuildExpansions(ctx context.Context, st *store.Store, key string, now time.Time, loc *time.Location, windowOf func(accountID string) (Window, bool)) ([]store.Calendar, error) {
	have, _, err := st.GetMeta(ctx, store.MetaCalendarExpand)
	if err != nil {
		return nil, err
	}
	if have == key {
		return nil, nil
	}
	cals, err := st.ListCalendars(ctx, "")
	if err != nil {
		return nil, err
	}
	var done []store.Calendar
	for _, c := range cals {
		if !daemonExpanded(c) {
			continue
		}
		win := WindowFor(now, api.DefaultCalendarKeepDays, loc)
		if windowOf != nil {
			if w, ok := windowOf(c.AccountID); ok {
				win = w
			}
		}
		if _, err := rebuildCalendar(ctx, st, c, win, loc, false); err != nil {
			return done, fmt.Errorf("rebuild %s: %w", c.ID, err)
		}
		done = append(done, c)
	}
	if err := st.SetMeta(ctx, store.MetaCalendarExpand, key); err != nil {
		return done, err
	}
	return done, nil
}

// moveWindow applies a new window to an account's stored data: the
// occurrences outside it go, events that ended before it go (TrimEvents
// after the occurrences), and the series of daemon-expanded calendars
// get the instances that entered at the far end (rebuilt from cal_events,
// masters only). Returns the calendars touched.
func moveWindow(ctx context.Context, st *store.Store, accountID string, win Window, loc *time.Location) ([]string, error) {
	if _, err := st.DeleteOccurrencesOutside(ctx, accountID, win.From, win.To); err != nil {
		return nil, err
	}
	cals, err := st.ListCalendars(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range cals {
		ids = append(ids, c.ID)
		if daemonExpanded(c) {
			if _, err := rebuildCalendar(ctx, st, c, win, loc, true); err != nil {
				return ids, err
			}
		}
	}
	gone, err := st.TrimEvents(ctx, accountID, win.From)
	if err != nil {
		return ids, err
	}
	// A trimmed CalDAV resource is now outside the window: its href stays
	// known so that a pass does not fetch it again while its ETag holds.
	byCal := map[string][]store.Resource{}
	for _, g := range gone {
		byCal[g.CalendarID] = append(byCal[g.CalendarID], store.Resource{Href: g.RemoteID, State: store.ResourceOutside})
	}
	for _, c := range cals {
		if res := byCal[c.ID]; len(res) > 0 && daemonExpanded(c) {
			known, err := st.ListResources(ctx, c.ID)
			if err != nil {
				return ids, err
			}
			etag := map[string]string{}
			for _, k := range known {
				etag[k.Href] = k.ETag
			}
			for i := range res {
				res[i].ETag = etag[res[i].Href]
			}
			if err := st.PutResources(ctx, c.ID, res); err != nil {
				return ids, err
			}
		}
	}
	return ids, nil
}
