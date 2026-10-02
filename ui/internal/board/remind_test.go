// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Remind… presets: Later Today, Tomorrow at 9, next Monday at 9 (macOS
// BoardRemindTests.swift, the same cases). UTC and a fixed date; the 15th
// of October 2026 is a Thursday.

func presetsAt(now time.Time) []RemindChoice { return RemindPresets(now, testEnv) }

func laterTodayAt(now time.Time) (time.Time, bool) {
	for _, p := range presetsAt(now) {
		if p.Kind == RemindLaterToday {
			return p.Date, true
		}
	}
	return time.Time{}, false
}

func TestRemindAtNoon(t *testing.T) {
	p := presetsAt(day(15, 12, 0))
	var kinds []RemindKind
	var dates []time.Time
	var titles, whens []string
	for _, c := range p {
		kinds = append(kinds, c.Kind)
		dates = append(dates, c.Date)
		titles = append(titles, c.Title)
		whens = append(whens, c.When)
	}
	eq(t, "kinds", kinds, []RemindKind{RemindLaterToday, RemindTomorrow, RemindNextWeek})
	eq(t, "dates", dates, []time.Time{day(15, 15, 0), day(16, 9, 0), day(19, 9, 0)})
	eq(t, "titles", titles, []string{"Later Today", "Tomorrow", "Next Week"})
	eq(t, "whens", whens, []string{"Thu 15:00", "Fri 09:00", "Mon 09:00"})
}

// TestLaterTodayRule: three hours ahead, rounded up to the hour; 18:00 when
// that is earlier and still an hour away; none past the day.
func TestLaterTodayRule(t *testing.T) {
	cases := []struct {
		now  time.Time
		want *time.Time
	}{
		{day(15, 8, 0), at(day(15, 11, 0))},
		{day(15, 12, 20), at(day(15, 16, 0))},
		{day(15, 14, 59), at(day(15, 18, 0))},
		{day(15, 15, 30), at(day(15, 18, 0))}, // 19:00 rounded, 18:00 first
		{day(15, 16, 30), at(day(15, 18, 0))},
		{day(15, 17, 0), at(day(15, 18, 0))},  // exactly an hour away
		{day(15, 17, 30), at(day(15, 21, 0))}, // 18:00 too close
		{day(15, 19, 30), at(day(15, 23, 0))},
		{day(15, 20, 59), nil}, // 23:59 rounds up to midnight: tomorrow
		{day(15, 21, 0), nil},
		{day(15, 23, 30), nil},
	}
	for _, c := range cases {
		got, ok := laterTodayAt(c.now)
		switch {
		case c.want == nil && ok:
			t.Errorf("%v: Later Today at %v", c.now, got)
		case c.want != nil && (!ok || !got.Equal(*c.want)):
			t.Errorf("%v: Later Today at %v (%v), want %v", c.now, got, ok, *c.want)
		}
	}
}

func TestNextWeekIsTheComingMonday(t *testing.T) {
	// Sunday the 18th: Monday is tomorrow, offered once, as Tomorrow.
	sunday := presetsAt(day(18, 10, 0))
	var kinds []RemindKind
	for _, p := range sunday {
		kinds = append(kinds, p.Kind)
	}
	eq(t, "sunday", kinds, []RemindKind{RemindLaterToday, RemindTomorrow})
	eq(t, "sunday's tomorrow", sunday[1].Date, day(19, 9, 0))
	nextWeek := func(now time.Time) time.Time {
		for _, p := range presetsAt(now) {
			if p.Kind == RemindNextWeek {
				return p.Date
			}
		}
		return time.Time{}
	}
	// Monday the 19th: a week ahead.
	eq(t, "monday", nextWeek(day(19, 8, 0)), day(26, 9, 0))
	// Saturday the 17th.
	eq(t, "saturday", nextWeek(day(17, 23, 0)), day(19, 9, 0))
}

// TestRemindAlwaysInTheFuture: every preset lies after now and within the
// daemon's year.
func TestRemindAlwaysInTheFuture(t *testing.T) {
	for now := day(12, 0, 0); now.Before(day(20, 0, 0)); now = now.Add(17 * time.Minute) {
		for _, p := range presetsAt(now) {
			if !p.Date.After(now) || p.Date.Sub(now) >= api.MaxBoardRemind {
				t.Errorf("%d at %v: %v", p.Kind, now, p.Date)
			}
		}
	}
}

// TestRemindDaylightSavingTime: across a change of daylight saving time the
// presets keep their wall clock time (Prague: summer time ends on Sunday 25
// October 2026 and starts on Sunday 29 March 2026).
func TestRemindDaylightSavingTime(t *testing.T) {
	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	env := envIn(prague)
	local := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, prague) }
	dates := func(p []RemindChoice) []time.Time {
		var out []time.Time
		for _, c := range p {
			out = append(out, c.Date)
		}
		return out
	}
	whens := func(p []RemindChoice) []string {
		var out []string
		for _, c := range p {
			out = append(out, c.When)
		}
		return out
	}
	same := func(what string, got, want []time.Time) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
			return
		}
		for i := range got {
			if !got[i].Equal(want[i]) {
				t.Errorf("%s: %v, want %v", what, got, want)
				return
			}
		}
	}
	// Saturday before the autumn change: Tomorrow is Sunday 09:00, Next
	// Week Monday 09:00, both in winter time.
	p := RemindPresets(local(10, 24, 12, 0), env)
	same("autumn", dates(p), []time.Time{local(10, 24, 15, 0), local(10, 25, 9, 0), local(10, 26, 9, 0)})
	eq(t, "autumn whens", whens(p), []string{"Sat 15:00", "Sun 09:00", "Mon 09:00"})
	// On the night of the change: Later Today counts real hours.
	p = RemindPresets(local(10, 25, 1, 30), env)
	check(t, len(p) > 0 && p[0].Kind == RemindLaterToday && p[0].Date.Equal(local(10, 25, 4, 0)), "the night: %v", dates(p))
	// Saturday before the spring change.
	p = RemindPresets(local(3, 28, 20, 0), env)
	same("spring", dates(p), []time.Time{local(3, 28, 23, 0), local(3, 29, 9, 0), local(3, 30, 9, 0)})
	eq(t, "spring whens", whens(p), []string{"Sat 23:00", "Sun 09:00", "Mon 09:00"})
}
