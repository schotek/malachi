// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package calendar is the canonical calendar model of the daemon
// (docs/calendar.md §3.1): the Event of one VEVENT (or one Graph event),
// the Object of one remote resource (a master with its overrides), their
// conversion from and to iCalendar (event.go), the expansion of a series
// into occurrences (expand.go), the cleaning of every string and the
// detection of https links and video calls (text.go), and busy time, free
// slots and conflicts (freebusy.go). The iTIP/iMIP messages are built and
// read by the subpackage imip.
//
// The package is pure: no store, no network, no clock but a parameter.
// Every input is hostile (CLAUDE.md rule 3): strings leave it cleaned and
// capped (CleanLine, CleanBlock), notes are plain text only (D11: HTML is
// turned into text with NotesFromHTML, X-ALT-DESC is never read), links
// are https only (Links), and nothing from an object is ever fetched or
// executed (ATTACH, URL, CONFERENCE, alarm actions other than DISPLAY and
// AUDIO).
//
// Times: a timed event's Start and End are UTC instants and Zone names
// the IANA zone its wall clock is in; an all-day event has StartDate and
// EndDate (exclusive) and Start/End at those dates' midnight as if UTC; a
// floating event (no zone in the source) has its wall clock as if UTC in
// Start/End. TZIDs are resolved through calendar/tz (D4): IANA, Windows,
// vendor prefix, matching offsets of the object's own VTIMEZONE, and as
// the last resort a fixed offset, which is written as the IANA zone
// Etc/GMT∓N when it is a whole hour and as UTC otherwise.
package calendar

// ParserVersion is the version of how an invitation's calendar part is
// read (cal_invitations.parser_version, meta calendar.invites.scan). Bump
// it when FromICS or imip.ParseMessage reads something differently, so
// that stored invitations are read again. 2: the card of a reply keeps
// the attendee's answer (InvitationCard.response); 3: ownEvent.
const ParserVersion = 3

// ExpandVersion is the version of Occurrences; together with
// recur.Version it makes the meta key calendar.expand, whose change
// rebuilds the materialised occurrences.
const ExpandVersion = 1
