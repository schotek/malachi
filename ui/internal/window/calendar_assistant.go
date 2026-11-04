// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The assistant in the Calendar (docs/calendar.md §5.9, D28, §6.5 G19):
// ✦ in the page's header bar moves the window's one assistant panel, its
// transcript with it, into the page's calendar_assistant_split and shows
// it (calendar_assistant_move.go); leaving the Calendar gives it back to
// Mail. The panel's calendar quick actions (What's on Today, Find a Free
// Slot: assistant.PanelActionsWith) show while the window's calendar is
// ready, and an answer's calendar card opens the Calendar on its event
// (openCalendarRef). Port of the calendar parts of macOS
// MalachiMail/Assistant and AssistantPanelController.

// askAboutEvent is the detail's Ask about This Event: the panel shown in
// the Calendar with the question about e (assistant.AskAboutEventPrompt,
// its ids only) in its field, the keyboard there, for the user's words.
func (w *Window) askAboutEvent(e api.Event) {
	ap := w.assistantPanel
	if ap == nil || e.EventID == "" || !w.showCalendarAssistant() {
		return
	}
	buf := ap.input.Buffer()
	buf.SetText(assistant.AskAboutEventPrompt(tr, string(e.EventID), string(e.RecurrenceID)))
	buf.PlaceCursor(buf.EndIter())
	glib.IdleAdd(func() { ap.input.GrabFocus() })
}

// revealCalendarAssistant is ✦ in the Calendar's header bar: the panel
// into the page and shown, or folded when it shows already.
func (w *Window) revealCalendarAssistant() {
	ap := w.assistantPanel
	if ap == nil {
		return
	}
	if ap.borrowed && ap.split.ShowSidebar() {
		ap.split.SetShowSidebar(false)
		return
	}
	w.showCalendarAssistant()
}

// showCalendarAssistant shows the panel inside the Calendar: false when it
// may not be shown (the Assistant off, another target). Without the page's
// slot (it should always be there) the Board's way applies: Mail with the
// panel open.
func (w *Window) showCalendarAssistant() bool {
	ap := w.assistantPanel
	if ap == nil || w.assist == nil || !w.assist.panelShown() {
		return false
	}
	if w.mode == board.ModeCalendar && w.borrowCalendarAssistant() {
		ap.reveal()
		return true
	}
	w.setMode(board.ModeMail)
	ap.reveal()
	return true
}

// openCalendarRef is a calendar card's Show in Calendar: the Calendar's
// Day view of the event, its detail open; the panel, open in Mail, goes
// with it.
func (w *Window) openCalendarRef(ref assistant.CalendarRef) {
	ap := w.assistantPanel
	open := ap != nil && ap.split.ShowSidebar()
	rec, _ := ref.Recurrence()
	day, ok := ref.Day(time.Local)
	if !ok {
		day = time.Time{}
	}
	w.showInCalendar(ref.EventID, rec, day)
	if open && w.mode == board.ModeCalendar {
		w.showCalendarAssistant()
	}
}

// calendarActionsShown says whether the panel shows the calendar's quick
// actions: the window's calendar has loaded and some account has the
// calendar capability.
func (w *Window) calendarActionsShown() bool {
	src := w.cal.source
	if src == nil {
		return false
	}
	s := src.Snapshot()
	return s.Phase == calendardata.PhaseReady && calendar.ShowsTodayStrip(s.Accounts)
}

// watchCalendarForAssistant follows the window's calendar source for the
// panel's quick actions (made on the first idle after the panel).
func (w *Window) watchCalendarForAssistant(p *assistantPanel) {
	glib.IdleAdd(func() {
		w.calendarSource().Observe(func(c calendardata.Change) {
			if c.Kind == calendardata.ChangeSnapshot {
				p.syncQuickActions()
			}
		})
		p.syncQuickActions()
	})
}
