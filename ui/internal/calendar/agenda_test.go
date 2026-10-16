// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAgenda(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	now := at(loc, 2026, 10, 14, 12, 0)
	trip := allDayOcc("trip", at(loc, 2026, 10, 15, 0, 0), 3)
	night := timed("night", at(loc, 2026, 10, 14, 22, 0), at(loc, 2026, 10, 15, 2, 0))
	midnight := timed("mid", at(loc, 2026, 10, 14, 23, 0), at(loc, 2026, 10, 15, 0, 0))
	early := timed("early", at(loc, 2026, 10, 14, 8, 0), at(loc, 2026, 10, 14, 9, 0))
	declined := timed("declined", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 10, 0))
	declined.MyResponse = api.ResponseDeclined
	before := timed("before", at(loc, 2026, 10, 1, 8, 0), at(loc, 2026, 10, 1, 9, 0))
	days := Agenda([]api.Occurrence{trip, night, midnight, early, declined, before}, now, 30, now, env, false)
	var got []string
	for _, d := range days {
		line := d.Heading + ":"
		for _, it := range d.Items {
			line += fmt.Sprintf(" %s(%d/%d %s)", it.Occurrence.EventID, it.DayOf, it.Days, ItemTime(it, env))
		}
		got = append(got, line)
	}
	want := []string{
		"Today: early(1/1 08:00–09:00) night(1/2 from 22:00) mid(1/1 23:00–00:00)",
		"Tomorrow: trip(1/3 All Day) night(2/2 until 02:00)",
		"Friday 16 October: trip(2/3 All Day)",
		"Saturday 17 October: trip(3/3 All Day)",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("agenda\n got %q\nwant %q", got, want)
	}
	if d := Agenda(nil, now, 30, now, env, false); len(d) != 0 {
		t.Errorf("empty agenda %v", d)
	}
	if got := DayOfText(2, 3, en{}); got != "Day 2 of 3" {
		t.Errorf("DayOfText %q", got)
	}
	middle := AgendaItem{Occurrence: timed("x", now, now.Add(72*time.Hour)), Start: now, End: now.Add(72 * time.Hour), DayOf: 2, Days: 4}
	if got := ItemTime(middle, env); got != "All Day" {
		t.Errorf("middle day %q", got)
	}
}

func TestDayHeading(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	now := at(loc, 2026, 12, 31, 23, 30)
	tests := []struct {
		day  time.Time
		want string
	}{
		{at(loc, 2026, 12, 31, 0, 0), "Today"},
		{at(loc, 2027, 1, 1, 0, 0), "Tomorrow"},
		{at(loc, 2026, 12, 30, 0, 0), "Yesterday"},
		{at(loc, 2026, 12, 25, 0, 0), "Friday 25 December"},
		{at(loc, 2027, 1, 5, 0, 0), "Tuesday 5 January 2027"},
	}
	for _, tt := range tests {
		if got := DayHeading(tt.day, now, env); got != tt.want {
			t.Errorf("DayHeading(%v) = %q, want %q", tt.day, got, tt.want)
		}
	}
}
