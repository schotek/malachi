// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// en is the identity translator: the msgid, the singular for n == 1.
type en struct{}

func (en) T(msgid string) string { return msgid }
func (en) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (en) C(_, msgid string) string { return msgid }

// prague is Europe/Prague (embedded tzdata in the test binary is not
// guaranteed, so the test skips without it).
func prague(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Skipf("no Europe/Prague: %v", err)
	}
	return loc
}

// envIn is the English env in loc.
func envIn(loc *time.Location) Env { return Env{Tr: en{}, Format: EnglishStrftime, Loc: loc} }

// at is a time in loc.
func at(loc *time.Location, y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, loc)
}

// timed is a timed occurrence with an id.
func timed(id string, start, end time.Time) api.Occurrence {
	s, e := start.UTC(), end.UTC()
	return api.Occurrence{EventID: api.EventID(id), Title: id, Start: &s, End: &e, Status: api.EventStatusConfirmed,
		ShowAs: api.ShowAsBusy}
}

// allDayOcc is an all-day occurrence from start for days days.
func allDayOcc(id string, start time.Time, days int) api.Occurrence {
	return api.Occurrence{EventID: api.EventID(id), Title: id, AllDay: true, StartDate: DateOf(start, start.Location()),
		EndDate: DateOf(AddDays(StartOfDay(start, start.Location()), days), start.Location()), Status: api.EventStatusConfirmed}
}

func ptr[T any](v T) *T { return &v }
