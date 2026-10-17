// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"
)

func TestSampleRemoteIDs(t *testing.T) {
	s := SampleData(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), time.UTC)
	r := s.WithRemoteIDs()
	n := 0
	for i, a := range r.Calendars.Accounts {
		for j, c := range a.Calendars {
			n++
			if c.RemoteID == "" || c.RemoteID != SampleRemoteIDs[c.ID] {
				t.Errorf("%s remote id %q", c.ID, c.RemoteID)
			}
			if s.Calendars.Accounts[i].Calendars[j].RemoteID != "" {
				t.Error("WithRemoteIDs changed its receiver")
			}
		}
	}
	if n != len(SampleRemoteIDs) {
		t.Errorf("%d calendars, %d remote ids", n, len(SampleRemoteIDs))
	}
}
