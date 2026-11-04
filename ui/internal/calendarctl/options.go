// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Options are what every controller of the package runs with besides its
// source. The window builds one and hands it to each controller; a field
// a controller does not use is ignored. Frozen: controllers written in
// parallel rely on it.
type Options struct {
	// Env writes the texts with dates (translator, strftime, zone; Loc
	// nil is time.Local).
	Env calendar.Env
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Loop runs the debounces (After); nil runs nothing later (a debounced
	// request is then never sent — tests always pass a loop).
	Loop board.Loop
	// LocaleFirstDay is the locale's first day of the week, for the
	// preference WeekStartLocale.
	LocaleFirstDay time.Weekday
	// RPCText is the usual sentence of a failed call with the action what
	// (widget.RPCErrorText), for an error the calendar has no sentence of
	// (calendardata.ErrorMessage false); nil is what alone.
	RPCText func(what string, err error) string
	// View and SetView read and write the key calendar-view (a nick of
	// calendar.View; settings.Store.CalendarView / SetCalendarView); nil
	// is the default view, not stored.
	View    func() string
	SetView func(nick string)
}

// Clock is now.
func (o Options) Clock() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// Loc is the zone of the days (Env.Loc, else time.Local).
func (o Options) Loc() *time.Location {
	if o.Env.Loc == nil {
		return time.Local
	}
	return o.Env.Loc
}

// After runs f through the loop once d has passed; nothing without a loop.
func (o Options) After(d time.Duration, f func()) {
	if o.Loop != nil {
		o.Loop.After(d, f)
	}
}

// ErrorMessage is the sentence of a failed calendar call (Cal.errorMessage):
// the preview's, the calendar's own by code, else RPCText with what (""
// is the mode's name); "" for nil. Never the daemon's message.
func (o Options) ErrorMessage(e *api.Error, what string) string {
	if e == nil {
		return ""
	}
	if t, ok := calendardata.ErrorMessage(e, o.Env.Tr); ok {
		return t
	}
	if what == "" {
		what = calendar.ModeName(o.Env.Tr)
	}
	if o.RPCText == nil {
		return what
	}
	return o.RPCText(what, e)
}
