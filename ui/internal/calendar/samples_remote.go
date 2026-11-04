// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import "github.com/schotek/malachi/backend/pkg/api"

// SampleRemoteIDs are the remote ids of the sample calendars (Swift
// CalendarSampleData's remoteId values): the Graph calendar ids and the
// CalDAV href that Preferences → Calendar writes into
// CalendarConfig.DisabledCalendars and DefaultCalendar.
var SampleRemoteIDs = map[api.CalendarID]string{
	SampleWorkCalendar: "AAMkSampleCalendar",
	SampleBirthdays:    "AAMkSampleBirthdays",
	SampleHolidays:     "AAMkSampleHolidays",
	SamplePersonal:     "me@example.com",
	SampleFamily:       "family@group.example.com",
	SampleSport:        "/remote.php/dav/calendars/me/sport/",
}

// WithRemoteIDs is s with every sample calendar's RemoteID set from
// SampleRemoteIDs (api.Calendar's remoteId is always present); s itself
// is not changed.
func (s Samples) WithRemoteIDs() Samples {
	accounts := make([]api.CalendarAccount, len(s.Calendars.Accounts))
	for i, a := range s.Calendars.Accounts {
		cals := make([]api.Calendar, len(a.Calendars))
		for j, c := range a.Calendars {
			if id, ok := SampleRemoteIDs[c.ID]; ok && c.RemoteID == "" {
				c.RemoteID = id
			}
			cals[j] = c
		}
		a.Calendars = cals
		accounts[i] = a
	}
	s.Calendars.Accounts = accounts
	return s
}
