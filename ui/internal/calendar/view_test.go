// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestViewNicks(t *testing.T) {
	for _, v := range Views {
		if got, ok := ParseView(v.Nick()); !ok || got != v {
			t.Errorf("ParseView(%q) = %v, %v", v.Nick(), got, ok)
		}
	}
	if v, ok := ParseView("year"); ok || v != ViewWeek {
		t.Errorf("ParseView(year) = %v, %v; want the default", v, ok)
	}
}

func TestViewRange(t *testing.T) {
	loc := prague(t)
	anchor := at(loc, 2026, 10, 16, 15, 30) // a Friday
	tests := []struct {
		name     string
		v        View
		anchor   time.Time
		ws       time.Weekday
		from, to time.Time
		hours    float64
	}{
		{"week from Monday", ViewWeek, anchor, time.Monday, at(loc, 2026, 10, 12, 0, 0), at(loc, 2026, 10, 19, 0, 0), 168},
		{"week from Sunday", ViewWeek, anchor, time.Sunday, at(loc, 2026, 10, 11, 0, 0), at(loc, 2026, 10, 18, 0, 0), 168},
		{"week from Saturday", ViewWeek, anchor, time.Saturday, at(loc, 2026, 10, 10, 0, 0), at(loc, 2026, 10, 17, 0, 0), 168},
		{"week with the autumn change", ViewWeek, at(loc, 2026, 10, 25, 12, 0), time.Monday, at(loc, 2026, 10, 19, 0, 0), at(loc, 2026, 10, 26, 0, 0), 169},
		{"day", ViewDay, anchor, time.Monday, at(loc, 2026, 10, 16, 0, 0), at(loc, 2026, 10, 17, 0, 0), 24},
		{"day of 23 hours", ViewDay, at(loc, 2026, 3, 29, 12, 0), time.Monday, at(loc, 2026, 3, 29, 0, 0), at(loc, 2026, 3, 30, 0, 0), 23},
		{"day of 25 hours", ViewDay, at(loc, 2026, 10, 25, 12, 0), time.Monday, at(loc, 2026, 10, 25, 0, 0), at(loc, 2026, 10, 26, 0, 0), 25},
		{"month grid", ViewMonth, anchor, time.Monday, at(loc, 2026, 9, 28, 0, 0), at(loc, 2026, 11, 9, 0, 0), 42*24 + 1},
		{"agenda", ViewAgenda, anchor, time.Monday, at(loc, 2026, 10, 16, 0, 0), at(loc, 2026, 11, 15, 0, 0), 30*24 + 1},
	}
	for _, tt := range tests {
		r := ViewRange(tt.v, tt.anchor, tt.ws, loc)
		if !r.From.Equal(tt.from) || !r.To.Equal(tt.to) {
			t.Errorf("%s: %v–%v, want %v–%v", tt.name, r.From, r.To, tt.from, tt.to)
		}
		if h := r.To.Sub(r.From).Hours(); h != tt.hours {
			t.Errorf("%s: %v hours, want %v", tt.name, h, tt.hours)
		}
	}
	if n := len(ViewRange(ViewWeek, at(loc, 2026, 3, 29, 0, 0), time.Monday, loc).Days()); n != 7 {
		t.Errorf("a week with the spring change has %d days", n)
	}
}

func TestStep(t *testing.T) {
	loc := prague(t)
	tests := []struct {
		v      View
		anchor time.Time
		dir    int
		want   time.Time
	}{
		{ViewDay, at(loc, 2026, 3, 28, 10, 0), 1, at(loc, 2026, 3, 29, 0, 0)},
		{ViewDay, at(loc, 2026, 3, 29, 10, 0), 1, at(loc, 2026, 3, 30, 0, 0)},
		{ViewWeek, at(loc, 2026, 10, 16, 0, 0), -1, at(loc, 2026, 10, 9, 0, 0)},
		{ViewWeek, at(loc, 2026, 10, 20, 0, 0), 1, at(loc, 2026, 10, 27, 0, 0)},
		{ViewMonth, at(loc, 2026, 1, 31, 0, 0), 1, at(loc, 2026, 2, 28, 0, 0)},
		{ViewMonth, at(loc, 2026, 3, 31, 0, 0), -1, at(loc, 2026, 2, 28, 0, 0)},
		{ViewMonth, at(loc, 2026, 12, 15, 0, 0), 1, at(loc, 2027, 1, 15, 0, 0)},
		{ViewAgenda, at(loc, 2026, 10, 16, 0, 0), 1, at(loc, 2026, 11, 15, 0, 0)},
	}
	for _, tt := range tests {
		if got := Step(tt.v, tt.anchor, tt.dir, loc); !got.Equal(tt.want) {
			t.Errorf("Step(%v, %v, %d) = %v, want %v", tt.v, tt.anchor, tt.dir, got, tt.want)
		}
	}
}

func TestWeekStart(t *testing.T) {
	tests := []struct {
		pref   api.WeekStart
		locale time.Weekday
		want   time.Weekday
	}{
		{api.WeekStartLocale, time.Sunday, time.Sunday},
		{api.WeekStartLocale, time.Monday, time.Monday},
		{api.WeekStartMonday, time.Sunday, time.Monday},
		{api.WeekStartSunday, time.Monday, time.Sunday},
		{api.WeekStartSaturday, time.Monday, time.Saturday},
		{"tuesday", time.Monday, time.Monday},
	}
	for _, tt := range tests {
		if got := WeekStart(tt.pref, tt.locale); got != tt.want {
			t.Errorf("WeekStart(%q, %v) = %v, want %v", tt.pref, tt.locale, got, tt.want)
		}
	}
}

func TestWeekNumber(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		day  time.Time
		ws   time.Weekday
		want int
	}{
		// ISO 8601.
		{at(loc, 2026, 1, 1, 0, 0), time.Monday, 1},
		{at(loc, 2026, 10, 16, 0, 0), time.Monday, 42},
		{at(loc, 2027, 1, 1, 0, 0), time.Monday, 53},
		{at(loc, 2027, 1, 4, 0, 0), time.Monday, 1},
		{at(loc, 2024, 12, 30, 0, 0), time.Monday, 1},
		// US: the week that holds 1 January is week 1.
		{at(loc, 2026, 1, 1, 0, 0), time.Sunday, 1},
		{at(loc, 2025, 12, 28, 0, 0), time.Sunday, 1},
		{at(loc, 2026, 1, 4, 0, 0), time.Sunday, 2},
		{at(loc, 2026, 10, 16, 0, 0), time.Sunday, 42},
		{at(loc, 2026, 12, 27, 0, 0), time.Sunday, 1},
		{at(loc, 2026, 12, 26, 0, 0), time.Sunday, 52},
		{at(loc, 2027, 1, 1, 0, 0), time.Saturday, 1},
		{at(loc, 2027, 1, 2, 0, 0), time.Saturday, 2},
	}
	for _, tt := range tests {
		if got := WeekNumber(tt.day, tt.ws, loc); got != tt.want {
			t.Errorf("WeekNumber(%s, %v) = %d, want %d", tt.day.Format("2006-01-02"), tt.ws, got, tt.want)
		}
	}
}

func TestTitleAndSubtitle(t *testing.T) {
	loc := prague(t)
	env := envIn(loc)
	tests := []struct {
		v               View
		anchor          time.Time
		ws              time.Weekday
		title, subtitle string
	}{
		{ViewWeek, at(loc, 2026, 10, 16, 9, 0), time.Monday, "October 2026", "12–18 October · week 42"},
		{ViewWeek, at(loc, 2026, 10, 1, 9, 0), time.Monday, "September–October 2026", "28 September–4 October · week 40"},
		{ViewWeek, at(loc, 2026, 12, 30, 9, 0), time.Monday, "December 2026–January 2027", "28 December 2026–3 January 2027 · week 53"},
		{ViewWeek, at(loc, 2026, 10, 16, 9, 0), time.Sunday, "October 2026", "11–17 October · week 42"},
		{ViewDay, at(loc, 2026, 10, 16, 9, 0), time.Monday, "16 October 2026", "Friday · week 42"},
		{ViewMonth, at(loc, 2026, 10, 16, 9, 0), time.Monday, "October 2026", ""},
		{ViewMonth, at(loc, 2026, 6, 1, 9, 0), time.Monday, "June 2026", ""},
		{ViewAgenda, at(loc, 2026, 10, 16, 9, 0), time.Monday, "October 2026", "16 October–14 November 2026"},
	}
	for _, tt := range tests {
		r := ViewRange(tt.v, tt.anchor, tt.ws, loc)
		if got := Title(tt.v, r, env); got != tt.title {
			t.Errorf("Title(%v, %v) = %q, want %q", tt.v, tt.anchor, got, tt.title)
		}
		if got := Subtitle(tt.v, r, tt.ws, env); got != tt.subtitle {
			t.Errorf("Subtitle(%v, %v) = %q, want %q", tt.v, tt.anchor, got, tt.subtitle)
		}
	}
}

func TestSpanAndKey(t *testing.T) {
	loc := prague(t)
	s := at(loc, 2026, 10, 16, 10, 0)
	o := timed("ev", s, s.Add(time.Hour))
	if a, b, ok := Span(o, loc); !ok || !a.Equal(s) || !b.Equal(s.Add(time.Hour)) {
		t.Errorf("Span(timed) = %v %v %v", a, b, ok)
	}
	o.End = nil
	if a, b, ok := Span(o, loc); !ok || !a.Equal(b) {
		t.Errorf("Span without an end = %v %v %v", a, b, ok)
	}
	ad := api.Occurrence{AllDay: true, StartDate: "2026-10-16"}
	if a, b, ok := Span(ad, loc); !ok || !a.Equal(at(loc, 2026, 10, 16, 0, 0)) || !b.Equal(at(loc, 2026, 10, 17, 0, 0)) {
		t.Errorf("Span(all-day without an end) = %v %v %v", a, b, ok)
	}
	if _, _, ok := Span(api.Occurrence{AllDay: true, StartDate: "16.10."}, loc); ok {
		t.Error("Span of a bad date is ok")
	}
	if _, _, ok := Span(api.Occurrence{}, loc); ok {
		t.Error("Span without a start is ok")
	}
	if k := OccurrenceKey(api.Occurrence{EventID: "ev", RecurrenceID: "2026-10-16T08:00:00Z"}); k != "ev|2026-10-16T08:00:00Z" {
		t.Errorf("key %q", k)
	}
	if k := OccurrenceKey(api.Occurrence{Invitation: &api.MessageRef{AccountID: "a", MessageID: "m"}}); k != "inv:a/m" {
		t.Errorf("invitation key %q", k)
	}
	point := timed("p", s, s)
	if !Overlaps(point, at(loc, 2026, 10, 16, 0, 0), at(loc, 2026, 10, 17, 0, 0), loc) {
		t.Error("an occurrence of no length at its day does not overlap")
	}
	if Overlaps(o, at(loc, 2026, 10, 17, 0, 0), at(loc, 2026, 10, 18, 0, 0), loc) {
		t.Error("overlaps the next day")
	}
}

func TestEnglishStrftime(t *testing.T) {
	tm := time.Date(2026, 10, 4, 9, 5, 0, 0, time.UTC)
	tests := map[string]string{
		"%a %A %b %B":   "Sun Sunday Oct October",
		"%d %-d %e|%-e": "04 4  4|4",
		"%H:%M %-H":     "09:05 9",
		"%I %-I %p":     "09 9 AM",
		"%m %-m %Y %y":  "10 10 2026 26",
		"100%% %q":      "100% %q",
		"%OB %Y|%OX":    "October 2026|%OX",
	}
	for f, want := range tests {
		if got := EnglishStrftime(tm, f); got != want {
			t.Errorf("EnglishStrftime(%q) = %q, want %q", f, got, want)
		}
	}
	if got := EnglishStrftime(tm.Add(15*time.Hour), "%-I %p"); got != "12 AM" && got != "12 PM" {
		t.Errorf("noon-ish %q", got)
	}
}

func TestSwitchAnchor(t *testing.T) {
	loc := prague(t)
	today := at(loc, 2026, 10, 16, 15, 30)
	anchor := at(loc, 2026, 12, 3, 0, 0)
	tests := []struct {
		from, to View
		want     time.Time
	}{
		{ViewWeek, ViewAgenda, at(loc, 2026, 10, 16, 0, 0)},
		{ViewDay, ViewAgenda, at(loc, 2026, 10, 16, 0, 0)},
		{ViewMonth, ViewAgenda, at(loc, 2026, 10, 16, 0, 0)},
		{ViewAgenda, ViewAgenda, anchor},
		{ViewAgenda, ViewWeek, anchor},
		{ViewAgenda, ViewMonth, anchor},
		{ViewWeek, ViewMonth, anchor},
		{ViewMonth, ViewDay, anchor},
		{ViewDay, ViewWeek, anchor},
	}
	for _, tt := range tests {
		if got := SwitchAnchor(tt.from, tt.to, anchor, today, loc); !got.Equal(tt.want) {
			t.Errorf("SwitchAnchor(%v → %v) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
