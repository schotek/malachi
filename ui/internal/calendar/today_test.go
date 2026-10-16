// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestTodayRows(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	// Tuesday 13 October 2026 at noon in the samples' week.
	now := at(loc, 2026, 10, 13, 12, 0)
	s := SampleData(now, loc)
	var got []string
	for _, r := range TodayRows(s.Occurrences, now, env, s.Preferences.ShowDeclined) {
		got = append(got, fmt.Sprintf("%s %s [%s] past=%v respond=%v", r.Time, r.Title, r.Place, r.TookPlace, r.Respond))
	}
	want := []string{
		"09:00–09:15 Standup [Room B] past=true respond=false",
		"10:00–11:30 Q4 planning [Room A] past=true respond=false",
		"10:30–11:00 Call with the supplier [] past=true respond=false",
		"11:00–12:00 Design review [] past=true respond=false",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Tuesday\n got %q\nwant %q", got, want)
	}
	wed := at(loc, 2026, 10, 14, 14, 30)
	rows := TodayRows(s.Occurrences, wed, env, false)
	if len(rows) != 2 || rows[1].Title != "Budget review" || !rows[1].Respond || rows[1].TookPlace || !rows[0].TookPlace {
		t.Errorf("Wednesday %+v", rows)
	}
	if rows := TodayRows(s.Occurrences, wed, env, true); len(rows) != 3 || rows[2].State != BlockDeclined {
		t.Errorf("Wednesday with declined %+v", rows)
	}
	thu := TodayRows(s.Occurrences, at(loc, 2026, 10, 15, 8, 0), env, false)
	if len(thu) < 1 || thu[0].Time != "All Day" || thu[0].Title != "Team offsite" || thu[0].TookPlace {
		t.Errorf("Thursday %+v", thu)
	}
	if TodayRows(nil, now, env, false) != nil {
		t.Error("rows without occurrences")
	}
	if !ShowsTodayStrip(s.Accounts) || ShowsTodayStrip([]api.Account{{Capabilities: api.MailCapabilities}}) ||
		ShowsTodayStrip([]api.Account{{}}) {
		t.Error("ShowsTodayStrip")
	}
}
