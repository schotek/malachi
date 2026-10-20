// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestPreferenceTexts(t *testing.T) {
	env := envIn(time.UTC)
	titles := map[api.WeekStart]string{api.WeekStartLocale: "System", api.WeekStartMonday: "Monday",
		api.WeekStartSunday: "Sunday", api.WeekStartSaturday: "Saturday"}
	for w, want := range titles {
		if got := WeekStartTitle(w, env); got != want {
			t.Errorf("WeekStartTitle(%s) = %q", w, got)
		}
	}
	hours := []struct {
		wh   api.WorkingHours
		want string
	}{
		{api.DefaultCalendarPreferences().WorkingHours, "Every weekday · 08:00–17:00"},
		{api.WorkingHours{Days: []string{"WE", "MO", "MO", "2TU"}, Start: "09:30", End: "12:00"}, "Monday and Wednesday · 09:30–12:00"},
		{api.WorkingHours{Days: []string{"SU"}, Start: "x", End: "12:00"}, "Sunday"},
		{api.WorkingHours{}, ""},
	}
	for _, tt := range hours {
		if got := WorkingHoursText(tt.wh, env); got != tt.want {
			t.Errorf("WorkingHoursText(%+v) = %q, want %q", tt.wh, got, tt.want)
		}
	}
	keep := map[int]string{30: "1 month", 90: "3 months", 180: "6 months", 365: "1 year", 730: "2 years", 7: "7 days"}
	for d, want := range keep {
		if got := KeepTitle(d, en{}); got != want {
			t.Errorf("KeepTitle(%d) = %q", d, got)
		}
	}
	notes := []struct {
		c    api.Calendar
		want string
	}{
		{api.Calendar{IsDefault: true, Access: api.CalendarAccessOwner}, "default"},
		{api.Calendar{Access: api.CalendarAccessRead}, "read only"},
		{api.Calendar{Access: api.CalendarAccessWrite, Owner: &api.Address{Name: "Petr", Address: "p@example.org"}}, "shared · Petr"},
		{api.Calendar{IsDefault: true, Access: api.CalendarAccessRead, Owner: &api.Address{Address: "p@example.org"}}, "default · shared · p@example.org"},
		{api.Calendar{Access: api.CalendarAccessOwner}, ""},
	}
	for _, tt := range notes {
		if got := CalendarRowNote(tt.c, en{}); got != tt.want {
			t.Errorf("CalendarRowNote(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
	writable := []struct {
		c    api.Calendar
		want bool
	}{
		{api.Calendar{Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents}, true},
		{api.Calendar{Access: api.CalendarAccessWrite}, true},
		{api.Calendar{Access: api.CalendarAccessRead}, false},
		{api.Calendar{Access: api.CalendarAccessOwner, Kind: api.CalendarKindBirthdays}, false},
		{api.Calendar{Access: api.CalendarAccessOwner, Kind: api.CalendarKindHolidays}, false},
	}
	for _, tt := range writable {
		if Writable(tt.c) != tt.want {
			t.Errorf("Writable(%+v)", tt.c)
		}
	}
	if ReplacePassword(en{}) != "Replace Password…" || ReplacePasswordTitle(en{}) != "Replace the Password?" ||
		ReloadEvent(en{}) != "Reload Event" {
		t.Error("ReplacePassword, ReloadEvent")
	}
	if UnansweredRow(en{}) != "Unanswered" || UnansweredHeading(en{}) != "Invitations" {
		t.Error("the sidebar's invitations")
	}
	if AfterAnswerTitle(api.AfterAnswerArchive, en{}) != "Archive" || AfterAnswerTitle("", en{}) != "Keep in Inbox" {
		t.Error("AfterAnswerTitle")
	}
}

func TestPreferencesLayout(t *testing.T) {
	loc := time.UTC
	s := SampleData(time.Date(2026, 10, 14, 12, 0, 0, 0, loc), loc)
	imap := api.Account{ID: "acc_imap", Config: api.AccountConfig{Kind: api.AccountIMAP}, Capabilities: api.MailCapabilities}
	got := PreferencesLayout(append([]api.Account{imap}, s.Accounts...))
	if len(got) != 5 || got[0].Kind != SectionDisplay || got[4].Kind != SectionAdd || len(got[0].Rows) != 5 {
		t.Fatalf("layout %+v", got)
	}
	work, nextcloud := got[1], got[3]
	if work.AccountID != SampleWork || work.Rows[0] != RowSync || slices.Contains(work.Rows, RowRemove) ||
		!slices.Contains(work.Rows, RowAfterAnswer) {
		t.Errorf("graph section %+v", work)
	}
	if nextcloud.AccountKind != api.AccountCalDAV || nextcloud.Rows[0] != RowServer || slices.Contains(nextcloud.Rows, RowSync) ||
		nextcloud.Rows[len(nextcloud.Rows)-1] != RowRemove || !slices.Contains(nextcloud.Rows, RowReplacePassword) ||
		slices.Contains(nextcloud.Rows, RowCard) {
		t.Errorf("caldav section %+v", nextcloud)
	}
	empty := PreferencesLayout([]api.Account{imap})
	if len(empty) != 3 || empty[1].Kind != SectionEmpty || empty[2].Kind != SectionAdd {
		t.Errorf("empty layout %+v", empty)
	}
	if DisplayGroup(en{}) != "Display" || RemoveAccount(en{}) != "Remove Account" || NoCalendarsTitle(en{}) != "No calendars yet." {
		t.Error("tab texts")
	}
}
