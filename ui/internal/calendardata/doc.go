// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package calendardata is the GTK client's calendar data seam
// (docs/calendar.md §5.2 "Source seam", §6.5 "Data seam"): where the
// calendar's controllers (ui/internal/calendarctl) get the accounts with
// their calendars and states, the unanswered invitations, the preferences,
// the occurrences of a range and single events, and where every write of
// the calendar contract (docs/api.md §4.14–4.16) goes.
//
// It ports MalachiCore/Calendar/CalendarSource.swift (DataSource,
// Snapshot, Phase, Change, ErrPreview) and InMemoryCalendarSource.swift
// with CalendarSamples.swift (InMemorySource over calendar.SampleData,
// the samples of MALACHI_CALENDAR_SAMPLES and the controllers' tests).
// The daemon's source (DaemonSource, daemon.go, task G3) implements the
// same interface over board.Caller and board.Loop with the raw
// api.Method* names; see the seam notes on DataSource.
//
// Pure Go: no gotk4. Everything runs on the main loop; a source delivers
// every answer through the board.Loop it was given (Post), so a callback
// never runs before the call that asked for it returns (a nil loop runs
// it at once, for code that has no main loop). Texts are not made here:
// a refused write carries its *api.Error, the controllers turn it into a
// sentence (ErrorMessage, calendar.ErrorText).
package calendardata
