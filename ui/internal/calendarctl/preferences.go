// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Preferences → Calendar → Display (docs/calendar.md §5.8), the port of
// CalendarPreferencesController.swift, over calendar.preferences /
// calendar.setPreferences: Week Starts On, Default Duration, Default
// Alert, Show Declined Events, Working Hours (a start and an end at half
// hours and seven day toggles; the last day stays on). A change shows at
// once and is written; a refusal takes it back and says why (Error). The
// daemon keeps the preferences, not GSettings.

// PreferenceChoice is a popup choice: its value and title.
type PreferenceChoice[T comparable] struct {
	Value T
	Title string
}

// CalendarPreferencesController runs the Display group.
type CalendarPreferencesController struct {
	Source calendardata.DataSource
	Opts   Options

	// OnChange: the shown preferences, Saving or Error changed.
	OnChange func()

	prefs  api.CalendarPreferences
	saving bool
	err    string
	token  *board.ObserverToken
	writes int
}

// NewCalendarPreferencesController shows src's preferences.
func NewCalendarPreferencesController(src calendardata.DataSource, opts Options) *CalendarPreferencesController {
	return &CalendarPreferencesController{Source: src, Opts: opts, prefs: prefsClone(src.Snapshot().Preferences)}
}

// Preferences are the preferences as shown (a written change before its
// answer).
func (p *CalendarPreferencesController) Preferences() api.CalendarPreferences { return p.prefs }

// Saving reports a write without its answer.
func (p *CalendarPreferencesController) Saving() bool { return p.saving }

// Error is the last refusal ("" none).
func (p *CalendarPreferencesController) Error() string { return p.err }

// Start follows the source's preferences while nothing is being written.
func (p *CalendarPreferencesController) Start() {
	if p.token != nil {
		return
	}
	p.token = p.Source.Observe(func(c calendardata.Change) {
		if c.Kind != calendardata.ChangeSnapshot || p.writes != 0 {
			return
		}
		if s := p.Source.Snapshot().Preferences; !reflect.DeepEqual(s, p.prefs) {
			p.prefs = prefsClone(s)
			p.changed()
		}
	})
}

// Stop stops following the source.
func (p *CalendarPreferencesController) Stop() {
	if p.token != nil {
		p.token.Cancel()
		p.token = nil
	}
}

// WeekStartChoices are Week Starts On's choices.
func (p *CalendarPreferencesController) WeekStartChoices() []PreferenceChoice[api.WeekStart] {
	out := make([]PreferenceChoice[api.WeekStart], 0, len(calendar.WeekStarts))
	for _, w := range calendar.WeekStarts {
		out = append(out, PreferenceChoice[api.WeekStart]{Value: w, Title: calendar.WeekStartTitle(w, p.Opts.Env)})
	}
	return out
}

// prefsWithValue is choices with v added in order when it is not one.
func prefsWithValue(choices []int, v int) []int {
	out := slices.Clone(choices)
	if !slices.Contains(out, v) {
		out = append(out, v)
		slices.Sort(out)
	}
	return out
}

// DurationChoices are Default Duration's choices, with the stored value
// when it is not one.
func (p *CalendarPreferencesController) DurationChoices() []PreferenceChoice[int] {
	var out []PreferenceChoice[int]
	for _, m := range prefsWithValue(calendar.DurationChoices, p.prefs.DefaultDurationMinutes) {
		out = append(out, PreferenceChoice[int]{Value: m, Title: calendar.DurationTitle(m, p.Opts.Env.Tr)})
	}
	return out
}

// AlertChoices are Default Alert's choices (-1 None), with the stored
// value when it is not one.
func (p *CalendarPreferencesController) AlertChoices() []PreferenceChoice[int] {
	var out []PreferenceChoice[int]
	for _, m := range prefsWithValue(calendar.AlertChoices, p.prefs.DefaultAlertMinutes) {
		out = append(out, PreferenceChoice[int]{Value: m, Title: calendar.AlertTitle(m, p.Opts.Env.Tr)})
	}
	return out
}

// WorkingHoursText is "Every weekday · 08:00–17:00".
func (p *CalendarPreferencesController) WorkingHoursText() string {
	return calendar.WorkingHoursText(p.prefs.WorkingHours, p.Opts.Env)
}

// SetWeekStart is Week Starts On.
func (p *CalendarPreferencesController) SetWeekStart(w api.WeekStart) {
	if w == p.prefs.WeekStart {
		return
	}
	p.write(api.CalendarPreferencesUpdate{WeekStart: &w}, func(c *api.CalendarPreferences) { c.WeekStart = w })
}

// SetDefaultDuration is Default Duration, in minutes.
func (p *CalendarPreferencesController) SetDefaultDuration(minutes int) {
	if minutes == p.prefs.DefaultDurationMinutes {
		return
	}
	p.write(api.CalendarPreferencesUpdate{DefaultDurationMinutes: &minutes},
		func(c *api.CalendarPreferences) { c.DefaultDurationMinutes = minutes })
}

// SetDefaultAlert is Default Alert, in minutes (-1 None).
func (p *CalendarPreferencesController) SetDefaultAlert(minutes int) {
	if minutes == p.prefs.DefaultAlertMinutes {
		return
	}
	p.write(api.CalendarPreferencesUpdate{DefaultAlertMinutes: &minutes},
		func(c *api.CalendarPreferences) { c.DefaultAlertMinutes = minutes })
}

// SetShowDeclined is Show Declined Events.
func (p *CalendarPreferencesController) SetShowDeclined(on bool) {
	if on == p.prefs.ShowDeclined {
		return
	}
	p.write(api.CalendarPreferencesUpdate{ShowDeclined: &on}, func(c *api.CalendarPreferences) { c.ShowDeclined = on })
}

// SetWorkingHours replaces the working hours whole.
func (p *CalendarPreferencesController) SetWorkingHours(wh api.WorkingHours) {
	if reflect.DeepEqual(wh, p.prefs.WorkingHours) {
		return
	}
	wh.Days = slices.Clone(wh.Days)
	p.write(api.CalendarPreferencesUpdate{WorkingHours: &wh}, func(c *api.CalendarPreferences) { c.WorkingHours = wh })
}

// WorkingDayChoices are the working days' toggles from Monday: the day
// code and the short weekday name.
func (p *CalendarPreferencesController) WorkingDayChoices() []PreferenceChoice[string] {
	out := make([]PreferenceChoice[string], 0, 7)
	for i := range 7 {
		d := time.Weekday((i + 1) % 7)
		// 2026-10-05 is a Monday.
		at := time.Date(2026, time.October, 5+i, 12, 0, 0, 0, p.Opts.Loc())
		out = append(out, PreferenceChoice[string]{Value: calendar.DayCode(d), Title: calendar.ShortWeekdayText(at, p.Opts.Env)})
	}
	return out
}

// IsWorkingDay reports a day code among the working days.
func (p *CalendarPreferencesController) IsWorkingDay(code string) bool {
	return slices.Contains(p.prefs.WorkingHours.Days, code)
}

// SetWorkingDay is a day's toggle; the last working day stays.
func (p *CalendarPreferencesController) SetWorkingDay(code string, on bool) {
	if _, ord, ok := calendar.ParseDayCode(code); !ok || ord != 0 || on == p.IsWorkingDay(code) {
		return
	}
	wh := p.prefs.WorkingHours
	wh.Days = slices.Clone(wh.Days)
	if on {
		wh.Days = append(wh.Days, code)
		order := make([]string, 0, 7)
		for _, c := range p.WorkingDayChoices() {
			order = append(order, c.Value)
		}
		rank := func(c string) int {
			if i := slices.Index(order, c); i >= 0 {
				return i
			}
			return 7
		}
		slices.SortStableFunc(wh.Days, func(a, b string) int { return rank(a) - rank(b) })
	} else {
		if len(wh.Days) <= 1 {
			return
		}
		wh.Days = slices.DeleteFunc(wh.Days, func(c string) bool { return c == code })
	}
	p.SetWorkingHours(wh)
}

// WorkingTimeChoices are the start and end choices: every half hour,
// with current when it is not one.
func (p *CalendarPreferencesController) WorkingTimeChoices(current string) []PreferenceChoice[string] {
	minutes := make([]int, 0, 49)
	for m := 0; m < 24*60; m += 30 {
		minutes = append(minutes, m)
	}
	if m, ok := calendar.ParseClock(current); ok {
		minutes = prefsWithValue(minutes, m)
	}
	out := make([]PreferenceChoice[string], 0, len(minutes))
	for _, m := range minutes {
		at := time.Date(2026, time.October, 5, m/60, m%60, 0, 0, p.Opts.Loc())
		out = append(out, PreferenceChoice[string]{Value: prefsClock(m), Title: calendar.TimeText(at, p.Opts.Env)})
	}
	return out
}

// prefsClock is minutes from midnight as "HH:MM".
func prefsClock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// SetWorkingStart is the start; refused when it is not before the end.
func (p *CalendarPreferencesController) SetWorkingStart(s string) {
	a, ok1 := calendar.ParseClock(s)
	b, ok2 := calendar.ParseClock(p.prefs.WorkingHours.End)
	if !ok1 || !ok2 || a >= b {
		return
	}
	wh := p.prefs.WorkingHours
	wh.Start = s
	p.SetWorkingHours(wh)
}

// SetWorkingEnd is the end; refused when it is not after the start.
func (p *CalendarPreferencesController) SetWorkingEnd(s string) {
	b, ok1 := calendar.ParseClock(s)
	a, ok2 := calendar.ParseClock(p.prefs.WorkingHours.Start)
	if !ok1 || !ok2 || a >= b {
		return
	}
	wh := p.prefs.WorkingHours
	wh.End = s
	p.SetWorkingHours(wh)
}

// write shows the change at once and writes it; the last answer wins, a
// refusal goes back to the source's preferences (or what was shown
// before, while other writes run).
func (p *CalendarPreferencesController) write(u api.CalendarPreferencesUpdate, local func(*api.CalendarPreferences)) {
	before := prefsClone(p.prefs)
	local(&p.prefs)
	p.err, p.saving = "", true
	p.writes++
	p.changed()
	p.Source.SetPreferences(u, func(answer *api.CalendarPreferences, e *api.Error) {
		p.writes--
		switch {
		case e != nil:
			if p.writes == 0 {
				p.prefs = prefsClone(p.Source.Snapshot().Preferences)
			} else {
				p.prefs = before
			}
			p.err = p.Opts.ErrorMessage(e, "")
		case answer != nil && p.writes == 0:
			p.prefs = prefsClone(*answer)
		}
		p.saving = p.writes > 0
		p.changed()
	})
}

func (p *CalendarPreferencesController) changed() {
	if p.OnChange != nil {
		p.OnChange()
	}
}

// prefsClone copies the preferences' day list.
func prefsClone(c api.CalendarPreferences) api.CalendarPreferences {
	c.WorkingHours.Days = slices.Clone(c.WorkingHours.Days)
	return c
}
