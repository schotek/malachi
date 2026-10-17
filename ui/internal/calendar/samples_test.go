// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSampleData(t *testing.T) {
	loc := prague(t)
	for _, now := range []time.Time{at(loc, 2026, 10, 14, 12, 0), at(loc, 2026, 10, 18, 23, 0), at(loc, 2026, 3, 30, 8, 0)} {
		s := SampleData(now, loc)
		if len(s.Accounts) != 3 || len(s.Calendars.Accounts) != 3 || len(s.Invitations) != 3 || s.Calendars.Unanswered != 3 ||
			len(s.Cards) != len(InvitationStates) {
			t.Fatalf("%v: counts %d %d %d %d", now, len(s.Accounts), len(s.Calendars.Accounts), len(s.Invitations), len(s.Cards))
		}
		week := ViewRange(ViewWeek, now, time.Monday, loc)
		states := map[BlockState]int{}
		video, recurring, allDays, invitations := 0, 0, 0, 0
		for _, o := range s.Occurrences {
			if !Overlaps(o, week.From, week.To, loc) {
				t.Errorf("%v: %s is outside the week", now, o.Title)
			}
			states[StateOf(o)]++
			if o.VideoCall {
				video++
			}
			if o.Recurring {
				recurring++
			}
			if o.Invitation != nil {
				invitations++
			}
			if o.AllDay {
				if s, e, _ := Span(o, loc); DaysBetween(s, e, loc) == 2 {
					allDays++
				}
			}
			if o.CalendarID != "" && s.CalendarName(o.CalendarID) == "" {
				t.Errorf("%s names no sample calendar", o.Title)
			}
		}
		if states[BlockTentative] == 0 || states[BlockUnanswered] < 2 || states[BlockDeclined] != 1 || video != 1 ||
			recurring != 5 || allDays != 1 || invitations != 1 {
			t.Errorf("%v: states %v video %d recurring %d two-day %d invitations %d", now, states, video, recurring, allDays, invitations)
		}
		tue := LayoutDay(s.Occurrences, AddDays(week.From, 1), loc, false)
		overlapping := 0
		for _, b := range tue {
			if b.Columns > 1 {
				overlapping++
			}
		}
		if overlapping < 3 {
			t.Errorf("%v: %d overlapping blocks on Tuesday", now, overlapping)
		}
		for i, c := range s.Cards {
			if c.State != InvitationStates[i] || c.Conflicts == nil || c.Targets == nil || c.Day.Busy == nil {
				t.Errorf("card %d: %+v", i, c)
			}
		}
		if s.SampleAccountName(SampleNextcloud) != "Nextcloud" || s.SampleAccountName("x") != "" || s.CalendarName("x") != "" {
			t.Error("names")
		}
		for _, a := range s.Accounts {
			if !a.Can(api.CapabilityCalendar) {
				t.Errorf("%s lacks the calendar capability", a.Config.Name)
			}
		}
	}
}
