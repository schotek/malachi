// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"reflect"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// Phase is how far a source's data is (Cal.Phase).
type Phase int

const (
	// PhaseLoading: nothing has arrived yet.
	PhaseLoading Phase = iota
	// PhaseReady: the snapshot holds the daemon's (or the samples') data.
	PhaseReady
	// PhaseUnavailable: no connection (offline); the last snapshot stays.
	PhaseUnavailable
	// PhaseFailed: a load failed; the last snapshot stays.
	PhaseFailed
	// PhaseUnsupported: a daemon without the calendar (methodNotFound).
	PhaseUnsupported
)

// String is the phase's name, for logs and tests.
func (p Phase) String() string {
	switch p {
	case PhaseLoading:
		return "loading"
	case PhaseReady:
		return "ready"
	case PhaseUnavailable:
		return "unavailable"
	case PhaseFailed:
		return "failed"
	case PhaseUnsupported:
		return "unsupported"
	}
	return "unknown"
}

// Snapshot is what the calendar shows outside a range: the accounts and
// their calendars, the unanswered invitations and the preferences
// (Cal.Snapshot). A source hands out its snapshot by value; the caller
// does not change the slices in it.
type Snapshot struct {
	Phase Phase
	// Accounts: account.list, every account of the daemon (names, kinds,
	// the calendar settings of Config.Calendar).
	Accounts []api.Account
	// CalendarAccounts: calendar.list, the accounts with the calendar
	// capability in the accounts' order, with their calendars (disabled
	// ones included, for Preferences) and states.
	CalendarAccounts []api.CalendarAccount
	// Unanswered: calendar.list unanswered.
	Unanswered int
	// Invitations: invitation.list with state unanswered.
	Invitations []api.InvitationListItem
	Preferences api.CalendarPreferences
}

// EmptySnapshot is a source's snapshot before anything arrived.
func EmptySnapshot() Snapshot {
	return Snapshot{Phase: PhaseLoading, Preferences: api.DefaultCalendarPreferences()}
}

// SampleSnapshot is the snapshot of the sample data, ready.
func SampleSnapshot(d calendar.Samples) Snapshot {
	return Snapshot{Phase: PhaseReady, Accounts: d.Accounts, CalendarAccounts: d.Calendars.Accounts,
		Unanswered: d.Calendars.Unanswered, Invitations: d.Invitations, Preferences: d.Preferences}.clone()
}

// Equal reports two snapshots with the same content.
func (s Snapshot) Equal(o Snapshot) bool { return reflect.DeepEqual(s, o) }

// clone copies the slices a source changes in place (accounts, calendar
// accounts with their calendars, invitations).
func (s Snapshot) clone() Snapshot {
	c := s
	c.Accounts = append([]api.Account(nil), s.Accounts...)
	c.CalendarAccounts = make([]api.CalendarAccount, len(s.CalendarAccounts))
	for i, a := range s.CalendarAccounts {
		a.Calendars = append([]api.Calendar(nil), a.Calendars...)
		c.CalendarAccounts[i] = a
	}
	if s.CalendarAccounts == nil {
		c.CalendarAccounts = nil
	}
	c.Invitations = append([]api.InvitationListItem(nil), s.Invitations...)
	return c
}

// Account is the account of id.
func (s Snapshot) Account(id api.AccountID) (api.Account, bool) {
	for _, a := range s.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return api.Account{}, false
}

// CalendarAccount is the calendar account of id, with its disabled
// calendars (Preferences → Calendar).
func (s Snapshot) CalendarAccount(id api.AccountID) (api.CalendarAccount, bool) {
	for _, a := range s.CalendarAccounts {
		if a.AccountID == id {
			return a, true
		}
	}
	return api.CalendarAccount{}, false
}

// ActiveCalendarAccounts are the calendar accounts with only their active
// calendars (calendar.ActiveCalendars): what every place but Preferences →
// Calendar shows.
func (s Snapshot) ActiveCalendarAccounts() []api.CalendarAccount {
	out := make([]api.CalendarAccount, 0, len(s.CalendarAccounts))
	for _, a := range s.CalendarAccounts {
		a.Calendars = calendar.ActiveCalendars(a.Calendars)
		out = append(out, a)
	}
	return out
}

// Calendar is an active calendar; false for an unknown or disabled one.
func (s Snapshot) Calendar(id api.CalendarID) (api.Calendar, bool) {
	for _, a := range s.CalendarAccounts {
		for _, c := range a.Calendars {
			if c.ID == id {
				if c.Disabled {
					return api.Calendar{}, false
				}
				return c, true
			}
		}
	}
	return api.Calendar{}, false
}

// AccountName is the account's name ("" for an unknown id), cleaned.
func (s Snapshot) AccountName(id api.AccountID) string {
	a, _ := s.Account(id)
	return cleanLine(a.Config.Name)
}

// CalendarName is the active calendar's name ("" for an unknown or
// disabled id), cleaned.
func (s Snapshot) CalendarName(id api.CalendarID) string {
	c, ok := s.Calendar(id)
	if !ok {
		return ""
	}
	return cleanLine(c.Name)
}

// States are the states of the calendar accounts, for the status line.
func (s Snapshot) States() []api.CalendarSyncState {
	out := make([]api.CalendarSyncState, 0, len(s.CalendarAccounts))
	for _, a := range s.CalendarAccounts {
		out = append(out, a.State)
	}
	return out
}

// VisibleCalendars is the set of the visible active calendars.
func (s Snapshot) VisibleCalendars() map[api.CalendarID]bool {
	out := map[api.CalendarID]bool{}
	for _, a := range s.ActiveCalendarAccounts() {
		for _, c := range a.Calendars {
			if c.Visible {
				out[c.ID] = true
			}
		}
	}
	return out
}

// WritableCalendars are the calendars a new event can go to
// (calendar.Writable), each account's default first, accounts in their
// order.
func (s Snapshot) WritableCalendars() []api.Calendar {
	var out []api.Calendar
	for _, a := range s.ActiveCalendarAccounts() {
		var rest []api.Calendar
		for _, c := range a.Calendars {
			if !calendar.Writable(c) {
				continue
			}
			if c.IsDefault {
				out = append(out, c)
			} else {
				rest = append(rest, c)
			}
		}
		out = append(out, rest...)
	}
	return out
}

// MyAddress is the user's own address in an account ("" unknown).
func (s Snapshot) MyAddress(id api.AccountID) string {
	a, _ := s.Account(id)
	return cleanLine(a.Config.Email)
}

// MyName is the user's name in an account, else the address.
func (s Snapshot) MyName(id api.AccountID) string {
	a, _ := s.Account(id)
	if n := cleanLine(a.Config.DisplayName); n != "" {
		return n
	}
	return s.MyAddress(id)
}

// cleanLine is calendar's cleanLine: one line of server text.
func cleanLine(s string) string { return board.CleanLine(s, api.MaxEventLocationBytes) }

// ChangeKind is what changed in a source (Cal.SourceChange).
type ChangeKind int

const (
	// ChangeSnapshot: the snapshot (accounts, calendars, states,
	// invitations, preferences, phase).
	ChangeSnapshot ChangeKind = iota
	// ChangeOccurrences: the occurrences of ranges overlapping [From, To)
	// were dropped (nil bounds are open: both nil = every range); a
	// controller showing one asks again.
	ChangeOccurrences
	// ChangeEvent: a cached event (Event) was replaced or dropped (a
	// write's answer, a conflict's current version).
	ChangeEvent
	// ChangeAlert: notify.eventAlert (Alert).
	ChangeAlert
	// ChangeError: a fire-and-forget write (a calendar's visibility) was
	// refused and undone (Err); the controller shows a toast.
	ChangeError
)

// Change is one change of a source, for its observers.
type Change struct {
	Kind     ChangeKind
	From, To *time.Time
	Event    api.EventID
	Alert    api.EventAlert
	Err      *api.Error
}

// Overlaps reports a ChangeOccurrences whose span touches [from, to) (an
// open bound touches everything); false for the other kinds.
func (c Change) Overlaps(from, to time.Time) bool {
	if c.Kind != ChangeOccurrences {
		return false
	}
	return RangesOverlap(from, to, c.From, c.To)
}

// RangesOverlap reports [a, b) and [from, to) overlapping; a nil bound is
// open.
func RangesOverlap(a, b time.Time, from, to *time.Time) bool {
	return (to == nil || a.Before(*to)) && (from == nil || b.After(*from))
}

// PreviewCode is the code of ErrPreview. It is no code of the contract
// (docs/api.md §2 has none below zero other than JSON-RPC's -327xx/-326xx),
// so no daemon answer is ever taken for it.
const PreviewCode api.ErrorCode = -1

// ErrPreview is the error of a source that cannot write (the samples):
// "The preview cannot do that." (calendar.PreviewOnly). Never change it.
var ErrPreview = &api.Error{Code: PreviewCode, Message: "the preview cannot write"}

// IsPreview reports ErrPreview.
func IsPreview(e *api.Error) bool { return e != nil && e.Code == PreviewCode }

// ErrorMessage is the sentence of a failed calendar call: the preview's
// (calendar.PreviewOnly), else the calendar's own by code
// (calendar.ErrorText); false when neither applies (the caller's usual
// RPC text then, with the mode's name). Never the daemon's message.
func ErrorMessage(e *api.Error, tr calendar.Translator) (string, bool) {
	if e == nil {
		return "", false
	}
	if IsPreview(e) {
		return calendar.PreviewOnly(tr), true
	}
	return calendar.ErrorText(e.Code, tr)
}
