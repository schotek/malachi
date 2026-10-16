// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/recur"
)

func TestRecurrencePatternRoundTrip(t *testing.T) {
	prague, _ := time.LoadLocation("Europe/Prague")
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, prague) // the second Tuesday
	for _, tc := range []struct {
		name    string
		pattern recurrencePattern
		rng     recurrenceRange
		rrule   string
	}{
		{"weekly", recurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"tuesday"}, FirstDayOfWeek: "monday", Index: "first"},
			recurrenceRange{Type: "noEnd", StartDate: "2026-10-13", EndDate: "0001-01-01"}, "FREQ=WEEKLY;BYDAY=TU"},
		{"daily count", recurrencePattern{Type: "daily", Interval: 2}, recurrenceRange{Type: "numbered", NumberOfOccurrences: 10}, "FREQ=DAILY;INTERVAL=2;COUNT=10"},
		{"absolute monthly until", recurrencePattern{Type: "absoluteMonthly", Interval: 1, DayOfMonth: 13},
			recurrenceRange{Type: "endDate", EndDate: "2027-03-13"}, "FREQ=MONTHLY;UNTIL=20270313T225959Z;BYMONTHDAY=13"},
		{"relative monthly", recurrencePattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"tuesday"}, Index: "second"},
			recurrenceRange{Type: "noEnd"}, "FREQ=MONTHLY;BYDAY=2TU"},
		{"last friday yearly", recurrencePattern{Type: "relativeYearly", Interval: 1, Month: 10, DaysOfWeek: []string{"friday"}, Index: "last"},
			recurrenceRange{Type: "noEnd"}, "FREQ=YEARLY;BYDAY=-1FR;BYMONTH=10"},
		{"absolute yearly", recurrencePattern{Type: "absoluteYearly", Interval: 1, Month: 10, DayOfMonth: 13},
			recurrenceRange{Type: "noEnd"}, "FREQ=YEARLY;BYMONTHDAY=13;BYMONTH=10"},
		{"weekdays", recurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"monday", "tuesday", "wednesday", "thursday", "friday"}, FirstDayOfWeek: "sunday"},
			recurrenceRange{Type: "noEnd"}, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;WKST=SU"},
	} {
		pr := patternedRecurrence{Pattern: tc.pattern, Range: tc.rng}
		rule, err := ruleFromPattern(pr, false, prague)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := recur.Normalise(rule); got != tc.rrule {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.rrule)
		}
		back, err := patternFromRule(rule, start, prague, false, "Central Europe Standard Time")
		if err != nil {
			t.Errorf("%s back: %v", tc.name, err)
			continue
		}
		again, err := ruleFromPattern(back, false, prague)
		if err != nil || recur.Normalise(again) != tc.rrule {
			t.Errorf("%s: round trip %+v → %v %v", tc.name, back, recur.Normalise(again), err)
		}
		if back.Range.StartDate != "2026-10-13" || back.Range.RecurrenceTimeZone != "Central Europe Standard Time" {
			t.Errorf("%s range %+v", tc.name, back.Range)
		}
	}
	// What Graph cannot hold, and hostile patterns.
	for _, s := range []string{"FREQ=MONTHLY;BYDAY=1MO,3MO", "FREQ=DAILY;BYHOUR=9,10", "FREQ=MONTHLY;BYMONTHDAY=-1", "FREQ=YEARLY;BYWEEKNO=20", "FREQ=MONTHLY;BYDAY=5MO"} {
		r, err := recur.Parse(s)
		if err != nil {
			continue
		}
		if _, err := patternFromRule(r, start, prague, false, "UTC"); !errors.Is(err, recur.ErrUnsupported) {
			t.Errorf("%s written: %v", s, err)
		}
	}
	for _, pr := range []patternedRecurrence{
		{Pattern: recurrencePattern{Type: "hourly"}},
		{Pattern: recurrencePattern{Type: "absoluteMonthly", DayOfMonth: 40}},
		{Pattern: recurrencePattern{Type: "weekly", DaysOfWeek: []string{"funday"}}},
		{Pattern: recurrencePattern{Type: "daily", Interval: 1 << 30}},
		{Pattern: recurrencePattern{Type: "daily"}, Range: recurrenceRange{Type: "endDate", EndDate: "9999-01-01"}},
		{Pattern: recurrencePattern{Type: "relativeYearly", Month: 13, DaysOfWeek: []string{"monday"}}},
	} {
		if _, err := ruleFromPattern(pr, false, prague); err == nil {
			t.Errorf("%+v accepted", pr)
		}
	}
	// "First Monday or Tuesday" reads as BYSETPOS (shown as complex).
	r, err := ruleFromPattern(patternedRecurrence{Pattern: recurrencePattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"monday", "tuesday"}, Index: "first"}}, false, prague)
	if err != nil || recur.Normalise(r) != "FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=1" || !recur.ToAPI(r, prague).Complex {
		t.Errorf("set position %v %v", recur.Normalise(r), err)
	}
	// All-day: UNTIL is a date.
	r, err = ruleFromPattern(patternedRecurrence{Pattern: recurrencePattern{Type: "daily", Interval: 1}, Range: recurrenceRange{Type: "endDate", EndDate: "2026-10-20"}}, true, nil)
	if err != nil || recur.Normalise(r) != "FREQ=DAILY;UNTIL=20261020" {
		t.Errorf("all-day until %v %v", recur.Normalise(r), err)
	}
}
