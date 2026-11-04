// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package calsync is the calendar engine: one syncer per account with
// calendars, the materialised occurrences, the alert scheduler, and the
// Provider interface every calendar source (caldav, graph; later google)
// implements (docs/calendar.md §3.7). It never imports a provider's
// package; core wires them. This file is the frozen interface; it holds
// no implementation.
package calsync

import (
	"context"
	"errors"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ErrUnsupported is what a Provider returns for an operation its source
// does not have (FreeBusy of a CalDAV account, a proposed new time). It is
// not an *api.Error: the engine maps it to the answer of the method
// (attendees known: false, invalidArgument, …).
var ErrUnsupported = errors.New("calsync: not supported by this calendar source")

// Provider is one account's calendar source. Implementations: caldav,
// graph; later google. Every method may block on the network and must
// honour ctx. Errors are *api.Error (or ErrUnsupported).
type Provider interface {
	// Calendars lists the account's calendars as the server has them.
	Calendars(ctx context.Context) ([]RemoteCalendar, error)
	// Sync brings one calendar up to date within window, given the
	// stored state (token, ctag, known resources); it calls sink for
	// every change, in order, and returns the new state.
	Sync(ctx context.Context, cal StoredCalendar, window Window, sink Sink) (SyncState, error)
	// Range fetches occurrences of [from, to) without storing anything
	// (event.list outside the window).
	Range(ctx context.Context, cal StoredCalendar, from, to time.Time) ([]Occurrence, error)
	Create(ctx context.Context, cal StoredCalendar, ev Event, opts WriteOptions) (WriteResult, error)
	Update(ctx context.Context, cal StoredCalendar, ev StoredEvent, change Change, opts WriteOptions) (WriteResult, error)
	Delete(ctx context.Context, cal StoredCalendar, ev StoredEvent, scope Scope, opts WriteOptions) (WriteResult, error)
	Respond(ctx context.Context, cal StoredCalendar, ev StoredEvent, r Response, opts WriteOptions) (WriteResult, error)
	// FreeBusy of other people; ErrUnsupported when the source has none.
	FreeBusy(ctx context.Context, attendees []string, from, to time.Time, step time.Duration) ([]Availability, error)
	// Features are what the source can do (api.CalendarFeatures).
	Features() api.CalendarFeatures
}

// RemoteCalendar is a calendar as the server lists it. Every string is
// untrusted text from the server; the engine cleans it before storing.
type RemoteCalendar struct {
	// RemoteID is the Graph calendar id or the CalDAV collection href;
	// it is what CalendarConfig.DisabledCalendars and DefaultCalendar name.
	RemoteID string
	Name     string
	Color    string // as the server gives it; the engine normalises to #rrggbb or drops it
	Access   api.CalendarAccess
	Kind     api.CalendarKind
	Owner    *api.Address // a calendar shared with the user
	// IsDefault: the server's default calendar (Graph isDefaultCalendar,
	// CalDAV schedule-default-calendar-URL).
	IsDefault              bool
	TimeZone               api.TimeZone
	OnlineMeetingProviders []string
	// CTag is the collection's change tag when the server reports it with
	// the listing (CalDAV getctag); "" otherwise.
	CTag string
}

// Window is the span of an account's stored events, [From, To): From is
// KeepDays back, To is api.CalendarFutureDays ahead (docs/calendar.md §2.3).
type Window struct {
	From time.Time
	To   time.Time
}

// Sink receives the changes of one Sync, in order. An error ends the pass:
// Sync returns it and the state of the pass is not stored.
type Sink interface {
	// Upsert stores or replaces one remote object (a CalDAV resource, a
	// Graph event with its expansion) by its RemoteID.
	Upsert(obj Object) error
	// Remove deletes the stored object with that remote id; an unknown id
	// is not an error.
	Remove(remoteID string) error
}

// SyncState is what a provider needs to continue incrementally. The
// engine stores it with the calendar and passes it back in StoredCalendar.
type SyncState struct {
	// Token is the Graph delta link or the CalDAV sync-token; "" = none.
	Token string
	// CTag is the collection's change tag after the pass (CalDAV).
	CTag string
	// DeltaMode is "delta" or "list" for Graph (docs/calendar.md D14); ""
	// for other sources.
	DeltaMode string
	// Full: the pass listed the whole window, so the engine may remove
	// every stored object the sink did not receive.
	Full bool
}

// WriteOptions are the parts of a write that are not the event itself.
type WriteOptions struct {
	// IfMatch is the version the client read (Event.ETag); "" only for
	// Create. A mismatch is conflict.
	IfMatch string
	// RecurrenceID names the instance of a series the write is about; ""
	// = the series or a single event.
	RecurrenceID api.DateOrTime
	// Notify: send invitations, updates, cancellations or the response
	// (sendInvitations, sendUpdates, notify, sendResponse).
	Notify bool
	// Mail is the client's localised iMIP subject and text, when the
	// daemon sends the mail.
	Mail *api.MailText
	// MailAccountID is the account iMIP leaves from; "" when the server
	// schedules.
	MailAccountID api.AccountID
	// Comment goes with a response or a cancellation (≤ api.MaxCommentBytes).
	Comment string
	// Attachments are files of a new event (docs/calendar.md D21).
	Attachments []Attachment
}

// WriteResult is what the server holds after a write. The engine upserts
// Objects and removes Removed in the same transaction as the rest of the
// write's local effects.
type WriteResult struct {
	Objects           []Object
	Removed           []string // remote ids
	Invitations       api.InvitationsSent
	DroppedExceptions int
}
