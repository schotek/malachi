// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
)

// The calendar model of the Provider interface is the canonical one of
// internal/calendar and the stored rows are those of internal/store
// (aliases below), so providers, the engine and the core share one set of
// types.

// StoredCalendar is a stored calendar row as the engine keeps it
// (internal/store); the provider reads its identity, remote id and
// CalendarSyncState.
type StoredCalendar = store.Calendar

// StoredEvent is a stored event row (remote id, UID, ETag, change key and
// the fields the engine materialised).
type StoredEvent = store.Event

// Object is one remote object: a CalDAV resource (Master and its
// Overrides, Raw the resource as fetched) or a Graph event with its
// exceptions and its own expansion (Occurrences).
type Object = calendar.Object

// Event is the canonical event model.
type Event = calendar.Event

// Occurrence is one materialised instance.
type Occurrence = calendar.Occurrence

// Scope is the edit scope of a recurring event.
type Scope = calendar.Scope

// Change is an update of an event within a scope (calendar.ApplyChange).
type Change = calendar.Change

// Response is the user's answer as an attendee.
type Response = calendar.Response

// Availability is one person's free/busy.
type Availability = calendar.Availability

// Attachment is a file of a new event (docs/calendar.md D21).
type Attachment = calendar.Attachment
