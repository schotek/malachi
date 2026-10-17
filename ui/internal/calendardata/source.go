// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
)

// Range is a span of event.list: [From, To) shown in Loc. A view's range
// is RangeIn(calendar.ViewRange(…), loc).
type Range struct {
	From, To time.Time
	Loc      *time.Location
}

// RangeIn is r of a view, in loc.
func RangeIn(r calendar.Range, loc *time.Location) Range {
	return Range{From: r.From, To: r.To, Loc: loc}
}

// Zone is the IANA name event.list gets as timeZone (ZoneName).
func (r Range) Zone() api.TimeZone { return ZoneName(r.Loc) }

// Key is the range's key in the occurrence caches (Cal.rangeKey).
func (r Range) Key() string {
	return strconv.FormatInt(r.From.Unix(), 10) + "|" + strconv.FormatInt(r.To.Unix(), 10) + "|" + string(r.Zone())
}

// location is Loc, else time.Local.
func (r Range) location() *time.Location {
	if r.Loc == nil {
		return time.Local
	}
	return r.Loc
}

// EventKey is an event's key in the event caches (Cal.eventKey).
func EventKey(id api.EventID, rec api.DateOrTime) string { return string(id) + "|" + string(rec) }

// ZoneName is loc's IANA name, which the daemon requires (event.list
// refuses a zone that is not IANA). Go names the process's zone "Local";
// then TZ (":Europe/Prague", or a path into a zoneinfo tree) or the
// /etc/localtime link names it, else "UTC". A nil loc is time.Local.
func ZoneName(loc *time.Location) api.TimeZone {
	if loc == nil {
		loc = time.Local
	}
	if n := loc.String(); n != "Local" && n != "" {
		return api.TimeZone(n)
	}
	if tz, ok := os.LookupEnv("TZ"); ok {
		if n := zoneOfPath(strings.TrimPrefix(tz, ":")); n != "" {
			return api.TimeZone(n)
		}
		if tz == "" {
			return "UTC"
		}
	}
	if p, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if n := zoneOfPath(p); n != "" {
			return api.TimeZone(n)
		}
	}
	return "UTC"
}

// zoneOfPath is the zone named by a TZ value or a zoneinfo path: the part
// after "zoneinfo/", or the value itself when it is a name Go can load.
func zoneOfPath(p string) string {
	if i := strings.LastIndex(p, "zoneinfo/"); i >= 0 {
		p = p[i+len("zoneinfo/"):]
	} else if strings.HasPrefix(p, "/") {
		return ""
	}
	if p == "" || p == "Local" {
		return ""
	}
	if _, err := time.LoadLocation(p); err != nil {
		return ""
	}
	return p
}

// DataSource is the calendar's data and its writes (CalendarDataSource).
// Every method runs on the main loop and returns at once; every answer
// (done) comes later on the main loop, through the source's board.Loop,
// with a nil result and the error when the call failed. done may be nil.
// A write's answer also lands in the caches, and the observers hear of it
// (ChangeEvent, ChangeOccurrences, ChangeSnapshot) before done runs.
//
// Errors are *api.Error: the daemon's own, ErrPreview (the samples), or a
// transport failure turned into one by AsError (no connection =
// CodeUnavailable, timeout = CodeCancelled).
//
// Seam for the daemon's source (G3, daemon.go): it calls the raw names of
// pkg/api — Snapshot/Refresh: MethodAccountList, MethodCalendarList,
// MethodInvitationList (state unanswered), MethodCalendarPreferences;
// Occurrences MethodEventList (cached per Range.Key, IncludeInvitations
// true); Event MethodEventGet (cached per EventKey); Search
// MethodEventSearch; FreeBusy MethodCalendarFreeBusy; FindFreeTime
// MethodCalendarFindFreeTime; SetVisible and SetColor
// MethodCalendarUpdate; Sync MethodCalendarSync; SetPreferences
// MethodCalendarSetPreferences; CreateEvent/UpdateEvent/DeleteEvent/
// Respond MethodEventCreate/Update/Delete/Respond; SnoozeAlert and
// DismissAlert MethodEventSnoozeAlert/DismissAlert; RespondToInvitation
// MethodInvitationRespond; ApplyCancel MethodInvitationApplyCancel;
// DiscoverCalDAV MethodAccountDiscoverCalDAV; ListCalDAVCalendars
// MethodAccountListCalDAVCalendars; AddAccount MethodAccountAdd;
// UpdateAccount MethodAccountUpdate; ImportAttachment
// MethodAttachmentImport. Handle takes NotifyCalendarChanged,
// NotifyCalendarState, NotifyEventAlert and NotifyInvitationsChanged (the
// window also forwards NotifyAccountsChanged and connection changes to
// the daemon's source through its own methods, not through this
// interface).
type DataSource interface {
	// Snapshot is what the source knows now.
	Snapshot() Snapshot
	// Observe calls f after every change, in the order the observers were
	// added; the token removes it (dropping the token does not).
	Observe(f func(Change)) *board.ObserverToken
	// Refresh asks for the snapshot anew.
	Refresh()
	// Handle takes a calendar notification of the daemon (docs/api.md §5:
	// method and its raw params); false for another method. A malformed
	// payload is ignored (true: the method was the calendar's).
	Handle(method string, raw json.RawMessage) bool

	// Occurrences is event.list of r (in r's zone, with the unanswered
	// invitations), cached per range until a notification drops it.
	Occurrences(r Range, done func([]api.Occurrence, *api.Error))
	// CachedOccurrences is the cached answer for r, if any.
	CachedOccurrences(r Range) ([]api.Occurrence, bool)
	// Event is event.get of id (rec "" = not an instance), cached by id
	// and recurrence id until a write or a notification replaces it;
	// reload asks the daemon anyway.
	Event(id api.EventID, rec api.DateOrTime, reload bool, done func(*api.Event, *api.Error))
	Search(p api.EventSearchParams, done func(*api.EventSearchResult, *api.Error))
	FreeBusy(p api.CalendarFreeBusyParams, done func(*api.CalendarFreeBusyResult, *api.Error))
	FindFreeTime(p api.CalendarFindFreeTimeParams, done func(*api.CalendarFindFreeTimeResult, *api.Error))

	// SetVisible is calendar.update visible: shown in the snapshot at once
	// (ChangeSnapshot, ChangeOccurrences), undone with ChangeError when
	// the daemon refuses.
	SetVisible(id api.CalendarID, visible bool)
	// SetColor is calendar.update userColor ("" = back to the server's).
	SetColor(id api.CalendarID, color string, done func(*api.Calendar, *api.Error))
	Sync(p api.CalendarSyncParams, done func(*api.Error))
	SetPreferences(u api.CalendarPreferencesUpdate, done func(*api.CalendarPreferences, *api.Error))
	CreateEvent(p api.EventCreateParams, done func(*api.EventCreateResult, *api.Error))
	// UpdateEvent is also a move (a drag, a new time). A conflict stores
	// the current version from the error's data (ChangeEvent) before done
	// gets the error.
	UpdateEvent(p api.EventUpdateParams, done func(*api.EventUpdateResult, *api.Error))
	DeleteEvent(p api.EventDeleteParams, done func(*api.EventDeleteResult, *api.Error))
	Respond(p api.EventRespondParams, done func(*api.EventRespondResult, *api.Error))
	SnoozeAlert(p api.EventSnoozeAlertParams, done func(*api.Error))
	DismissAlert(p api.EventDismissAlertParams, done func(*api.Error))
	// RespondToInvitation is invitation.respond, a proposed new time
	// included (ProposedStart/End, answering tentative).
	RespondToInvitation(p api.InvitationRespondParams, done func(*api.InvitationRespondResult, *api.Error))
	ApplyCancel(p api.InvitationApplyCancelParams, done func(*api.InvitationApplyCancelResult, *api.Error))
	DiscoverCalDAV(p api.AccountDiscoverCalDAVParams, done func(*api.AccountDiscoverCalDAVResult, *api.Error))
	ListCalDAVCalendars(p api.AccountListCalDAVCalendarsParams, done func(*api.AccountListCalDAVCalendarsResult, *api.Error))
	// AddAccount is account.add (a caldav account of the wizard).
	AddAccount(p api.AccountAddParams, done func(*api.AccountAddResult, *api.Error))
	// UpdateAccount is account.update (an account's calendar config).
	UpdateAccount(p api.AccountUpdateParams, done func(*api.Error))
	// ImportAttachment is attachment.import for the event editor.
	ImportAttachment(p api.AttachmentImportParams, done func(*api.AttachmentImportResult, *api.Error))
}

// AsError is err as the calendar's error: the daemon's *api.Error as it
// is, a call without a connection CodeUnavailable, one that ran out of
// time or was cancelled CodeCancelled, anything else CodeInternalError;
// nil for nil. The messages are technical English, never shown.
func AsError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var e *api.Error
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, client.ErrDisconnected):
		return &api.Error{Code: api.CodeUnavailable, Message: "not connected"}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &api.Error{Code: api.CodeCancelled, Message: "timed out"}
	}
	return &api.Error{Code: api.CodeInternalError, Message: "call failed"}
}

// IsCalendarNotification reports one of the four calendar notifications.
func IsCalendarNotification(method string) bool {
	switch method {
	case api.NotifyCalendarChanged, api.NotifyCalendarState, api.NotifyEventAlert, api.NotifyInvitationsChanged:
		return true
	}
	return false
}

// observers are a source's observers over board.Observers: the change
// being announced is on a stack, so that an observer that causes another
// change sees its own and the rest of the outer round still the outer one.
type observers struct {
	o   board.Observers
	cur []Change
}

func (s *observers) add(f func(Change)) *board.ObserverToken {
	return s.o.Add(func() { f(s.cur[len(s.cur)-1]) })
}

func (s *observers) notify(c Change) {
	s.cur = append(s.cur, c)
	s.o.Notify()
	s.cur = s.cur[:len(s.cur)-1]
}

// post runs f through loop, at once without one.
func post(loop board.Loop, f func()) {
	if loop == nil {
		f()
		return
	}
	loop.Post(f)
}
