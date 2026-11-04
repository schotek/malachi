// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The views and their ranges: which days a view shows around its anchor,
// how ‹ and › move the anchor, the toolbar's title and subtitle with the
// week number, and which weekday a week starts on. Days are midnights in
// the caller's zone, made with time.Date, so a day of 23 or 25 hours
// (a daylight-saving change) is still one day.

// View is what the calendar page shows (the key calendar-view).
type View int

// The views, in the toolbar's order.
const (
	ViewDay View = iota
	ViewWeek
	ViewMonth
	ViewAgenda
)

// Views lists the views in the toolbar's order.
var Views = []View{ViewDay, ViewWeek, ViewMonth, ViewAgenda}

// DefaultView is the key calendar-view's default.
const DefaultView = ViewWeek

// AgendaDays is the span of an agenda page (it scrolls on in pages of
// this many days).
const AgendaDays = 30

// Nick is the view's value of the key calendar-view.
func (v View) Nick() string {
	switch v {
	case ViewDay:
		return "day"
	case ViewMonth:
		return "month"
	case ViewAgenda:
		return "agenda"
	}
	return "week"
}

// ParseView is the view a stored nick names; an unknown one is the
// default with false.
func ParseView(nick string) (View, bool) {
	for _, v := range Views {
		if v.Nick() == nick {
			return v, true
		}
	}
	return DefaultView, false
}

// Range is the span of days a view shows: [From, To), both midnights in
// the zone it was made in.
type Range struct {
	From, To time.Time
}

// Days lists the midnights of the range.
func (r Range) Days() []time.Time {
	var out []time.Time
	for d := r.From; d.Before(r.To); d = AddDays(d, 1) {
		out = append(out, d)
	}
	return out
}

// Contains reports t within [From, To).
func (r Range) Contains(t time.Time) bool { return !t.Before(r.From) && t.Before(r.To) }

// StartOfDay is the midnight of t's day in loc.
func StartOfDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// AddDays is the midnight n calendar days after day's (in day's zone).
func AddDays(day time.Time, n int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+n, 0, 0, 0, 0, day.Location())
}

// SameDay reports a and b on one calendar day in loc.
func SameDay(a, b time.Time, loc *time.Location) bool {
	return StartOfDay(a, loc).Equal(StartOfDay(b, loc))
}

// DaysBetween is the number of calendar days from a's day to b's in loc.
func DaysBetween(a, b time.Time, loc *time.Location) int {
	x, y := StartOfDay(a, loc), StartOfDay(b, loc)
	// Noon to noon in UTC: whole days whatever the offsets.
	ux := time.Date(x.Year(), x.Month(), x.Day(), 12, 0, 0, 0, time.UTC)
	uy := time.Date(y.Year(), y.Month(), y.Day(), 12, 0, 0, 0, time.UTC)
	return int(uy.Sub(ux).Hours() / 24)
}

// StartOfWeek is the midnight of the first day of t's week.
func StartOfWeek(t time.Time, weekStart time.Weekday, loc *time.Location) time.Time {
	d := StartOfDay(t, loc)
	back := (int(d.Weekday()) - int(weekStart) + 7) % 7
	return AddDays(d, -back)
}

// ViewRange is the range view v shows around anchor: the day, the week
// (from weekStart), the month's 6×7 grid (MonthCells), or AgendaDays days
// from the anchor's day.
func ViewRange(v View, anchor time.Time, weekStart time.Weekday, loc *time.Location) Range {
	switch v {
	case ViewDay:
		d := StartOfDay(anchor, loc)
		return Range{d, AddDays(d, 1)}
	case ViewMonth:
		first := gridStart(anchor, weekStart, loc)
		return Range{first, AddDays(first, 42)}
	case ViewAgenda:
		d := StartOfDay(anchor, loc)
		return Range{d, AddDays(d, AgendaDays)}
	}
	w := StartOfWeek(anchor, weekStart, loc)
	return Range{w, AddDays(w, 7)}
}

// Step is the anchor after ‹ (dir -1) or › (dir +1): a day, a week, a
// month (the day of the month clamped to the new month's length), or an
// agenda page.
func Step(v View, anchor time.Time, dir int, loc *time.Location) time.Time {
	d := StartOfDay(anchor, loc)
	switch v {
	case ViewDay:
		return AddDays(d, dir)
	case ViewMonth:
		first := time.Date(d.Year(), d.Month()+time.Month(dir), 1, 0, 0, 0, 0, loc)
		last := AddDays(time.Date(first.Year(), first.Month()+1, 1, 0, 0, 0, 0, loc), -1).Day()
		return time.Date(first.Year(), first.Month(), min(d.Day(), last), 0, 0, 0, 0, loc)
	case ViewAgenda:
		return AddDays(d, dir*AgendaDays)
	}
	return AddDays(d, 7*dir)
}

// SwitchAnchor is the anchor after switching from view from to view to:
// switching to the Agenda starts it at today (the midnight of today in
// loc), whatever day the other view showed (the owner's decision of
// 2026-10-16); the Agenda moves only with ‹ ›, Today or a date the user
// picks while it shows. Switching from the Agenda keeps its anchor (today
// unless the user stepped), and every other switch keeps the anchor.
func SwitchAnchor(from, to View, anchor, today time.Time, loc *time.Location) time.Time {
	if to == ViewAgenda && from != ViewAgenda {
		return StartOfDay(today, loc)
	}
	return anchor
}

// WeekStart is the first day of the week: the preference, or the
// locale's first day for WeekStartLocale (and an unknown value).
func WeekStart(pref api.WeekStart, localeFirstDay time.Weekday) time.Weekday {
	switch pref {
	case api.WeekStartMonday:
		return time.Monday
	case api.WeekStartSunday:
		return time.Sunday
	case api.WeekStartSaturday:
		return time.Saturday
	}
	return localeFirstDay
}

// WeekNumber is the number of day's week: ISO 8601 when weeks start on
// Monday; otherwise the US rule (week 1 is the week, from weekStart, that
// holds 1 January; a week across the new year is week 1 of the new
// year).
func WeekNumber(day time.Time, weekStart time.Weekday, loc *time.Location) int {
	d := StartOfDay(day, loc)
	if weekStart == time.Monday {
		_, w := d.ISOWeek()
		return w
	}
	first := StartOfWeek(d, weekStart, loc)
	last := AddDays(first, 6)
	jan1 := time.Date(last.Year(), time.January, 1, 0, 0, 0, 0, loc)
	week1 := StartOfWeek(jan1, weekStart, loc)
	return DaysBetween(week1, first, loc)/7 + 1
}

// Title is the toolbar's title of a view's range: the day's date (Day),
// the month and year (Week, Month, Agenda; a week across two months
// names both).
func Title(v View, r Range, env Env) string {
	switch v {
	case ViewDay:
		return DayMonthYearText(r.From, env)
	case ViewMonth:
		return MonthYearText(AddDays(r.From, 14), env)
	case ViewAgenda:
		return MonthYearText(r.From, env)
	}
	last := AddDays(r.To, -1)
	switch {
	case r.From.Month() == last.Month() && r.From.Year() == last.Year():
		return MonthYearText(r.From, env)
	case r.From.Year() == last.Year():
		return RangeText(MonthText(r.From, env), MonthYearText(last, env), env.Tr)
	}
	return RangeText(MonthYearText(r.From, env), MonthYearText(last, env), env.Tr)
}

// Subtitle is the toolbar's subtitle: the weekday and week (Day), the
// days with the week number (Week: "12–18 October · week 42"), the span
// of an agenda page; "" for Month.
func Subtitle(v View, r Range, weekStart time.Weekday, env Env) string {
	loc := env.loc()
	switch v {
	case ViewDay:
		return Dot(WeekdayText(r.From, env), WeekLabel(WeekNumber(r.From, weekStart, loc), env.Tr), env.Tr)
	case ViewMonth:
		return ""
	case ViewAgenda:
		return RangeText(DayMonthText(r.From, env), DayMonthYearText(AddDays(r.To, -1), env), env.Tr)
	}
	last := AddDays(r.To, -1)
	var days string
	switch {
	case r.From.Month() == last.Month() && r.From.Year() == last.Year():
		days = RangeText(DayText(r.From, env), DayMonthText(last, env), env.Tr)
	case r.From.Year() == last.Year():
		days = RangeText(DayMonthText(r.From, env), DayMonthText(last, env), env.Tr)
	default:
		days = RangeText(DayMonthYearText(r.From, env), DayMonthYearText(last, env), env.Tr)
	}
	return Dot(days, WeekLabel(WeekNumber(r.From, weekStart, loc), env.Tr), env.Tr)
}

// WeekLabel is "week 42" (the subtitle, the month grid's week column
// tooltip).
func WeekLabel(n int, tr Translator) string {
	// TRANSLATORS: the number of a week of the year, in the calendar's
	// subtitle "12–18 October · week 42".
	return fmt.Sprintf(tr.T("week %d"), n)
}

// ---------------------------------------------------------------------------
// Occurrences in a zone
// ---------------------------------------------------------------------------

// ParseDate is the midnight of an API date in loc.
func ParseDate(d api.Date, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", string(d), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// DateOf is t's calendar date in loc.
func DateOf(t time.Time, loc *time.Location) api.Date {
	return api.Date(t.In(loc).Format("2006-01-02"))
}

// Span is an occurrence's [start, end) in loc: a timed one's instants, an
// all-day one's midnights (EndDate exclusive; an end not after the start
// is the next day). False when the occurrence carries no usable time.
func Span(o api.Occurrence, loc *time.Location) (start, end time.Time, ok bool) {
	if o.AllDay {
		s, ok := ParseDate(o.StartDate, loc)
		if !ok {
			return time.Time{}, time.Time{}, false
		}
		e, ok := ParseDate(o.EndDate, loc)
		if !ok || !e.After(s) {
			e = AddDays(s, 1)
		}
		return s, e, true
	}
	if o.Start == nil {
		return time.Time{}, time.Time{}, false
	}
	s := o.Start.In(loc)
	e := s
	if o.End != nil && o.End.After(*o.Start) {
		e = o.End.In(loc)
	}
	return s, e, true
}

// OccurrenceKey is an occurrence's stable identity in the views: the event and the
// recurrence id, or the invitation message of one not in a calendar.
func OccurrenceKey(o api.Occurrence) string {
	if o.EventID == "" && o.Invitation != nil {
		return "inv:" + string(o.Invitation.AccountID) + "/" + string(o.Invitation.MessageID)
	}
	return string(o.EventID) + "|" + string(o.RecurrenceID)
}

// Overlaps reports an occurrence that touches [from, to) in loc. A timed
// one of no length counts at its start.
func Overlaps(o api.Occurrence, from, to time.Time, loc *time.Location) bool {
	s, e, ok := Span(o, loc)
	if !ok {
		return false
	}
	if e.Equal(s) {
		return !s.Before(from) && s.Before(to)
	}
	return s.Before(to) && e.After(from)
}
