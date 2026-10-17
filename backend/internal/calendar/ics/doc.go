// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package ics reads and writes iCalendar (RFC 5545) content: a lenient,
// bounded parser of content lines into a component tree, typed accessors
// for property values, and an encoder (docs/calendar.md §3.2).
//
// Every input is hostile. Parse refuses only input over MaxBytes
// (ErrTooBig); anything else yields an *Object whose Problems say what was
// tolerated or dropped and whose Truncated says a cap was reached (limits
// in limits.go). It never panics: a bug becomes ErrMalformed.
//
// Input handling: a UTF-8 BOM is ignored, NUL bytes removed, CRLF, LF and
// CR each end a line, blank lines are skipped, a line starting with a
// space or tab continues the previous one (unfolding works on bytes, so a
// UTF-8 sequence split by folding is whole again), invalid UTF-8 becomes
// U+FFFD. Names are [A-Za-z0-9-], at most 64, stored upper case.
// Parameter values lose their quotes; a value with a quote inside or a
// control character drops the property. A control character in a
// property value is kept (and noted): cleaning text for display is the
// caller's job.
//
// Structure: BEGIN/END nest up to MaxDepth. An END closes up to the
// matching open component (ProblemMismatchedEnd for each one it closes);
// an END matching nothing is ignored; the end of input closes what is
// open (ProblemMissingEnd). VCALENDAR is only ever top-level: a BEGIN:VCALENDAR
// inside an open object closes it first. Components found outside any
// VCALENDAR are wrapped in one synthetic VCALENDAR (ProblemNoVCalendar;
// an END:VCALENDAR closes it), properties outside any component are
// dropped (ProblemStrayProperty). Several VCALENDARs in one input are all
// kept, in order, in Object.Calendars (ProblemMultipleCalendars, so that
// iMIP, which expects one, can refuse); Object.Calendar returns the first.
// A component cut by a cap (too deep, over MaxComponentProperties, open
// when MaxComponents or MaxProperties is reached) is dropped whole, never
// kept partial; only the top-level VCALENDAR open at an object-wide cap is
// kept, with Truncated set.
//
// Values stay raw in Property.Value. The accessors read them:
// Text/TextList (TEXT, unescaped), Time/Times (DATE and DATE-TIME as Time
// with a Kind: KindDate, KindFloating, KindUTC, KindZoned with the TZID
// as written; zones are not resolved here, see calendar/tz), Duration
// (nominal days, bounded to ten years), Periods (PERIOD), UTCOffset,
// Integer (int32), CalAddress (only a well-formed mailto: has an Email)
// and URI (text; never fetched). Errors wrap ErrSyntax or ErrRange and
// never contain the value.
//
// Encode writes CRLF lines folded at 75 octets on UTF-8 boundaries, each
// component's properties in stored order and then its children; it
// refuses (ErrInvalid) what the parser would not read back. For every
// object Parse returned without problems, Parse(Encode(o)) equals o.
//
// Example:
//
//	obj, err := ics.Parse(part)
//	if err != nil {
//		return err // ErrTooBig or ErrMalformed
//	}
//	for _, ev := range obj.Calendar().Children("VEVENT") {
//		uid := ev.Property("UID").Text()
//		start, err := ev.Property("DTSTART").Time()
//		if err != nil { ... }
//		if org := ev.Property("ORGANIZER"); org != nil {
//			addr := org.CalAddress() // addr.Email == "" unless mailto:
//		}
//		_ = uid
//	}
//
//	reply := &ics.Component{Name: "VCALENDAR"}
//	reply.Add(ics.NewProperty("METHOD", "REPLY"))
//	ev := &ics.Component{Name: "VEVENT"}
//	ev.Add(ics.NewText("SUMMARY", "Planning, room 4"))
//	ev.Add(ics.NewTime("DTSTART", ics.UTCOf(time.Now())))
//	reply.Components = append(reply.Components, ev)
//	data, err := ics.EncodeComponent(reply)
//
// Property, Component.Property and the other lookups take names
// case-insensitively; Component.Property returns nil when absent, and
// every Property method is safe on what Parse returned.
package ics
