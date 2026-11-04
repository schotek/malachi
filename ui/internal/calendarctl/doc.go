// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package calendarctl holds the controllers of the GTK client's calendar
// (docs/calendar.md §6.5): plain structs over a calendardata.DataSource
// that keep the state of a page, a popover, a window or a card, build
// what the widgets show from calendar's pure helpers and send what the
// user did to the source. No widgets and no gotk4: every method runs on
// the main loop, every answer of the source comes back on it, and the
// controllers report through callbacks (OnChange, OnError, OnOpenDetail,
// OnNewEvent, OnSignIn, …). Swift's async questions are continuations
// here (a callback with an answer function). Debounces go through
// board.Loop.After, so the tests drive a fake loop.
//
// The macOS client leads (macos/Sources/MalachiCore/Controllers/
// Calendar*.swift, EventDetailController.swift, InvitationController.swift,
// EventEditorController.swift, CalDAVWizardController.swift); every file
// here is the port of one of them, with its tests.
package calendarctl
