// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Preferences → Calendar → Display (CalendarPreferencesControllerTests of
// the macOS client): the rows' choices, a change shown at once and
// written, the answer kept, a refusal undone, the working hours.

func g8Prefs(writes bool) (*CalendarPreferencesController, *calendardata.InMemorySource, *g8Loop) {
	s, l := g8Samples(writes)
	p := NewCalendarPreferencesController(s, g8Opts(l))
	p.Start()
	return p, s, l
}

func TestPreferencesRoundTrip(t *testing.T) {
	p, s, l := g8Prefs(true)
	var ws []api.WeekStart
	for _, c := range p.WeekStartChoices() {
		ws = append(ws, c.Value)
	}
	if !slices.Equal(ws, []api.WeekStart{api.WeekStartLocale, api.WeekStartMonday, api.WeekStartSunday, api.WeekStartSaturday}) ||
		p.WeekStartChoices()[0].Title != "System" {
		t.Fatalf("week starts %+v", p.WeekStartChoices())
	}
	var ds []int
	for _, c := range p.DurationChoices() {
		ds = append(ds, c.Value)
	}
	if !slices.Equal(ds, []int{15, 30, 45, 60, 90, 120}) {
		t.Fatalf("durations %v", ds)
	}
	if p.AlertChoices()[0].Title != calendar.AlertTitle(-1, g8En{}) {
		t.Fatalf("alerts %+v", p.AlertChoices())
	}
	if p.WorkingHoursText() != "Every weekday · 08:00–17:00" {
		t.Fatalf("working hours %q", p.WorkingHoursText())
	}
	p.SetWeekStart(api.WeekStartSunday)
	if p.Preferences().WeekStart != api.WeekStartSunday || !p.Saving() {
		t.Fatal("not shown at once")
	}
	l.run()
	if p.Saving() || len(s.Writes()) != 1 || *s.Writes()[0].Params.(api.CalendarPreferencesUpdate).WeekStart != api.WeekStartSunday {
		t.Fatalf("writes %+v", s.Writes())
	}
	if s.Snapshot().Preferences.WeekStart != api.WeekStartSunday {
		t.Fatal("not stored")
	}
	p.SetShowDeclined(true)
	p.SetDefaultDuration(30)
	l.run()
	if !p.Preferences().ShowDeclined || p.Preferences().DefaultDurationMinutes != 30 || p.Saving() {
		t.Fatalf("prefs %+v", p.Preferences())
	}
	// A value that is not a choice is listed.
	p.SetDefaultAlert(20)
	l.run()
	if !slices.ContainsFunc(p.AlertChoices(), func(c PreferenceChoice[int]) bool { return c.Value == 20 }) {
		t.Fatalf("alerts %+v", p.AlertChoices())
	}
	// The same value writes nothing.
	n := len(s.Writes())
	p.SetDefaultAlert(20)
	if len(s.Writes()) != n {
		t.Fatal("wrote an unchanged value")
	}
	// The source's change shows while nothing is written.
	s.Update(func(sn *calendardata.Snapshot) { sn.Preferences.DefaultDurationMinutes = 45 })
	if p.Preferences().DefaultDurationMinutes != 45 {
		t.Fatal("did not follow the source")
	}
	p.Stop()
	if s.Observers() != 0 {
		t.Fatal("still observing")
	}
}

func TestPreferencesRefusalIsUndone(t *testing.T) {
	p, s, l := g8Prefs(true)
	s.Failures[api.MethodCalendarSetPreferences] = &api.Error{Code: api.CodeInvalidArgument, Message: "range"}
	p.SetDefaultDuration(90)
	if p.Preferences().DefaultDurationMinutes != 90 {
		t.Fatal("not shown at once")
	}
	l.run()
	if p.Preferences().DefaultDurationMinutes != 60 || p.Error() == "" || p.Saving() {
		t.Fatalf("prefs %+v error %q", p.Preferences(), p.Error())
	}
	// The preview says so.
	q, _, ql := g8Prefs(false)
	q.SetShowDeclined(true)
	ql.run()
	if q.Preferences().ShowDeclined || q.Error() != calendar.PreviewOnly(g8En{}) {
		t.Fatalf("preview %q", q.Error())
	}
}

func TestPreferencesWorkingHours(t *testing.T) {
	p, s, l := g8Prefs(true)
	var days []string
	for _, c := range p.WorkingDayChoices() {
		days = append(days, c.Value)
	}
	if !slices.Equal(days, []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}) || p.WorkingDayChoices()[0].Title != "Mon" {
		t.Fatalf("days %+v", p.WorkingDayChoices())
	}
	if !p.IsWorkingDay("MO") || p.IsWorkingDay("SA") {
		t.Fatal("working days")
	}
	p.SetWorkingDay("SA", true)
	l.run()
	if !slices.Equal(p.Preferences().WorkingHours.Days, []string{"MO", "TU", "WE", "TH", "FR", "SA"}) {
		t.Fatalf("days %v", p.Preferences().WorkingHours.Days)
	}
	times := p.WorkingTimeChoices("08:15")
	if len(times) != 49 || !slices.ContainsFunc(times, func(c PreferenceChoice[string]) bool { return c.Value == "08:15" }) ||
		times[0].Title != "00:00" || times[0].Value != "00:00" {
		t.Fatalf("times %d %+v", len(times), times[:2])
	}
	p.SetWorkingStart("18:00")
	if p.Preferences().WorkingHours.Start != "08:00" {
		t.Fatal("a start after the end")
	}
	p.SetWorkingStart("09:30")
	l.run()
	p.SetWorkingEnd("09:00")
	if p.Preferences().WorkingHours.End != "17:00" {
		t.Fatal("an end before the start")
	}
	p.SetWorkingEnd("18:30")
	l.run()
	if wh := s.Snapshot().Preferences.WorkingHours; wh.Start != "09:30" || wh.End != "18:30" {
		t.Fatalf("stored %+v", wh)
	}
	for _, d := range []string{"MO", "TU", "WE", "TH", "FR"} {
		p.SetWorkingDay(d, false)
		l.run()
	}
	// The last day stays.
	p.SetWorkingDay("SA", false)
	if !slices.Equal(p.Preferences().WorkingHours.Days, []string{"SA"}) {
		t.Fatalf("days %v", p.Preferences().WorkingHours.Days)
	}
	// An ordinal or an unknown code is no day.
	p.SetWorkingDay("2MO", true)
	p.SetWorkingDay("XX", true)
	if !slices.Equal(p.Preferences().WorkingHours.Days, []string{"SA"}) {
		t.Fatalf("days %v", p.Preferences().WorkingHours.Days)
	}
}
