// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestRecurrenceText(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	start := at(loc, 2026, 10, 13, 10, 0) // a Tuesday
	tests := []struct {
		r    *api.Recurrence
		want string
	}{
		{nil, ""},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1}, "Every day"},
		{&api.Recurrence{Frequency: api.FrequencyDaily}, "Every day"},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 3}, "Every 3 days"},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Count: 10}, "Every day, 10 times"},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Count: 1}, "Every day, 1 time"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1}, "Every week on Tuesday"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"TU"}, Until: "2027-03-01"},
			"Every week on Tuesday, until 1 March 2027"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 2, ByDay: []string{"WE", "MO"}},
			"Every 2 weeks on Monday and Wednesday"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"FR", "MO", "WE"}},
			"Every week on Monday, Wednesday and Friday"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"MO", "TU", "WE", "TH", "FR"}}, "Every weekday"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}}, "Every day"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 2, ByDay: []string{"SU", "MO"}}, "Every 2 weeks on Monday and Sunday"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1}, "Monthly on day 13"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByMonthDay: []int{14}}, "Monthly on day 14"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 3, ByMonthDay: []int{14}}, "Every 3 months on day 14"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByMonthDay: []int{-1}}, "Monthly on the last day"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"2TU"}}, "Monthly on the second Tuesday"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"+1MO"}}, "Monthly on the first Monday"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"-1FR"}}, "Monthly on the last Friday"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 2, ByDay: []string{"-2FR"}}, "Every 2 months on the second-to-last Friday"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1}, "Yearly on 13 October"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1, ByMonth: []int{2}, ByMonthDay: []int{29}}, "Yearly on 29 February"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 2, ByMonth: []int{10}, ByDay: []string{"2TU"}},
			"Every 2 years on the second Tuesday of October"},
		// Outside the editor's shape.
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, Complex: true}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"2TU"}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"1MO", "3MO"}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"MO"}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByMonthDay: []int{-2}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByMonth: []int{3}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, ByDay: []string{"MO"}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1, ByMonth: []int{2, 3}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1, ByMonth: []int{4}, ByMonthDay: []int{31}}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1, ByMonth: []int{13}}, "Custom"},
		{&api.Recurrence{Frequency: "hourly", Interval: 1}, "Custom"},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"XX"}}, "Custom"},
	}
	for _, tt := range tests {
		if got := RecurrenceText(tt.r, start, env); got != tt.want {
			t.Errorf("RecurrenceText(%+v) = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestRepeatChoices(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	start := at(loc, 2026, 10, 13, 10, 0)
	texts := map[Repeat]string{RepeatDaily: "Every day", RepeatWeekly: "Every week on Tuesday",
		RepeatBiweekly: "Every 2 weeks on Tuesday", RepeatMonthly: "Monthly on day 13", RepeatYearly: "Yearly on 13 October"}
	for _, c := range Repeats {
		r := RepeatRecurrence(c, start)
		if (r == nil) != (c == RepeatNever || c == RepeatCustom) {
			t.Errorf("%v: rule %+v", c, r)
		}
		if got := RecurrenceText(r, start, env); got != texts[c] {
			t.Errorf("%v: %q, want %q", c, got, texts[c])
		}
		if c != RepeatCustom {
			if got := RepeatOf(r, start); got != c {
				t.Errorf("RepeatOf(%v's rule) = %v", c, got)
			}
		}
		if RepeatTitle(c, en{}) == "" {
			t.Errorf("%v has no title", c)
		}
	}
	tests := []struct {
		r    *api.Recurrence
		want Repeat
	}{
		{&api.Recurrence{Frequency: api.FrequencyWeekly}, RepeatWeekly},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 2}, RepeatBiweekly},
		{&api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1}, RepeatMonthly},
		{&api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1}, RepeatYearly},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Count: 3}, RepeatCustom},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"MO"}}, RepeatCustom},
		{&api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 3}, RepeatCustom},
		{&api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Complex: true}, RepeatCustom},
	}
	for _, tt := range tests {
		if got := RepeatOf(tt.r, start); got != tt.want {
			t.Errorf("RepeatOf(%+v) = %v, want %v", tt.r, got, tt.want)
		}
	}
	if RepeatTitle(RepeatBiweekly, en{}) != "Every 2 Weeks" || RepeatTitle(RepeatCustom, en{}) != "Custom…" ||
		RepeatTitle(RepeatNever, en{}) != "Never" {
		t.Error("RepeatTitle")
	}
}

func TestCustomRecurrence(t *testing.T) {
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	r := CustomRecurrence(api.FrequencyWeekly, 0, []time.Weekday{time.Friday, time.Monday, time.Friday, time.Sunday}, start)
	if r.Interval != 1 || !slices.Equal(r.ByDay, []string{"MO", "FR", "SU"}) {
		t.Errorf("weekly %+v", r)
	}
	if r := CustomRecurrence(api.FrequencyWeekly, 150, nil, start); r.Interval != 99 || !slices.Equal(r.ByDay, []string{"TU"}) {
		t.Errorf("weekly default %+v", r)
	}
	if r := CustomRecurrence(api.FrequencyMonthly, 2, nil, start); !slices.Equal(r.ByMonthDay, []int{13}) {
		t.Errorf("monthly %+v", r)
	}
	if r := CustomRecurrence(api.FrequencyYearly, 1, nil, start); !slices.Equal(r.ByMonth, []int{10}) || !slices.Equal(r.ByMonthDay, []int{13}) {
		t.Errorf("yearly %+v", r)
	}
	if r := CustomRecurrence(api.FrequencyDaily, 5, nil, start); r.Interval != 5 || r.ByDay != nil {
		t.Errorf("daily %+v", r)
	}
	if IntervalUnit(api.FrequencyWeekly, 2, en{}) != "weeks" || IntervalUnit(api.FrequencyDaily, 1, en{}) != "day" {
		t.Error("IntervalUnit")
	}
}

func TestParseDayCode(t *testing.T) {
	tests := []struct {
		in  string
		d   time.Weekday
		ord int
		ok  bool
	}{
		{"MO", time.Monday, 0, true}, {"2TU", time.Tuesday, 2, true}, {"-1FR", time.Friday, -1, true},
		{"+3SU", time.Sunday, 3, true}, {"0MO", 0, 0, false}, {"6MO", 0, 0, false}, {"M", 0, 0, false},
		{"XX", 0, 0, false}, {"aMO", 0, 0, false},
	}
	for _, tt := range tests {
		d, ord, ok := ParseDayCode(tt.in)
		if ok != tt.ok || ok && (d != tt.d || ord != tt.ord) {
			t.Errorf("ParseDayCode(%q) = %v %d %v", tt.in, d, ord, ok)
		}
	}
	if ListText(nil, en{}) != "" || ListText([]string{"a"}, en{}) != "a" || ListText([]string{"a", "b"}, en{}) != "a and b" {
		t.Error("ListText")
	}
}
