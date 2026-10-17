// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Recurrence texts ("Every 2 weeks on Monday and Wednesday", "Monthly on
// the second Tuesday", "Yearly on 14 October, until 1 March 2027") and the
// editor's Repeat choices with their Recurrence values. A rule outside
// the editor's shape (Recurrence.Complex) is "Custom".

// dayCodes are the iCalendar day codes by time.Weekday.
var dayCodes = [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// DayCode is the iCalendar code of a weekday, "MO".
func DayCode(d time.Weekday) string { return dayCodes[d] }

// ParseDayCode reads "MO" or "2TU"/"-1FR": the weekday and the ordinal (0
// none).
func ParseDayCode(s string) (time.Weekday, int, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	code := s[len(s)-2:]
	ord := 0
	if n := s[:len(s)-2]; n != "" {
		v, err := strconv.Atoi(strings.TrimPrefix(n, "+"))
		if err != nil || v == 0 || v < -5 || v > 5 {
			return 0, 0, false
		}
		ord = v
	}
	for d, c := range dayCodes {
		if c == code {
			return time.Weekday(d), ord, true
		}
	}
	return 0, 0, false
}

// weekdayName is a weekday's name in the locale, through a reference date.
func weekdayName(d time.Weekday, env Env) string {
	// 2026-10-04 is a Sunday.
	ref := time.Date(2026, time.October, 4+int(d), 12, 0, 0, 0, env.loc())
	return WeekdayText(ref, env)
}

// ListText joins names into a list: "Monday", "Monday and Wednesday",
// "Monday, Wednesday and Friday".
func ListText(items []string, tr Translator) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	out := items[0]
	for _, it := range items[1 : len(items)-1] {
		// TRANSLATORS: joins all but the last two items of a list, e.g. the
		// weekdays "Monday, Wednesday and Friday".
		out = fmt.Sprintf(tr.T("%s, %s"), out, it)
	}
	// TRANSLATORS: joins the last item of a list, e.g. "Monday and
	// Wednesday".
	return fmt.Sprintf(tr.T("%s and %s"), out, items[len(items)-1])
}

// ordinalWeekday is "second Tuesday", "last Friday".
func ordinalWeekday(ord int, d time.Weekday, env Env) string {
	tr, day := env.Tr, weekdayName(d, env)
	switch ord {
	case 1:
		// TRANSLATORS: a weekday of a month in a recurrence, %s is its name:
		// "Monthly on the first Monday".
		return fmt.Sprintf(tr.T("first %s"), day)
	case 2:
		// TRANSLATORS: a weekday of a month in a recurrence: "second Tuesday".
		return fmt.Sprintf(tr.T("second %s"), day)
	case 3:
		// TRANSLATORS: a weekday of a month in a recurrence: "third Tuesday".
		return fmt.Sprintf(tr.T("third %s"), day)
	case 4:
		// TRANSLATORS: a weekday of a month in a recurrence: "fourth Tuesday".
		return fmt.Sprintf(tr.T("fourth %s"), day)
	case 5:
		// TRANSLATORS: a weekday of a month in a recurrence: "fifth Tuesday".
		return fmt.Sprintf(tr.T("fifth %s"), day)
	case -1:
		// TRANSLATORS: a weekday of a month in a recurrence: "last Friday".
		return fmt.Sprintf(tr.T("last %s"), day)
	}
	// TRANSLATORS: a weekday of a month in a recurrence: "second-to-last
	// Friday" (and further from the end).
	return fmt.Sprintf(tr.T("second-to-last %s"), day)
}

// Custom is the text of a rule the editor cannot show.
func Custom(tr Translator) string {
	// TRANSLATORS: a recurrence rule the calendar cannot describe.
	return tr.T("Custom")
}

// RecurrenceText describes r; start is the series' first start in the
// zone (it gives the day where the rule names none). "" for nil.
func RecurrenceText(r *api.Recurrence, start time.Time, env Env) string {
	if r == nil {
		return ""
	}
	tr := env.Tr
	if r.Complex {
		return Custom(tr)
	}
	text, ok := ruleText(*r, start.In(env.loc()), env)
	if !ok {
		return Custom(tr)
	}
	switch {
	case r.Count > 0:
		// TRANSLATORS: a recurrence that ends after a number of times:
		// "Every week on Monday, 10 times".
		return fmt.Sprintf(tr.N("%s, %d time", "%s, %d times", r.Count), text, r.Count)
	case r.Until != "":
		if u, ok := ParseDate(r.Until, env.loc()); ok {
			// TRANSLATORS: a recurrence that ends on a date: "Every day,
			// until 1 March 2027".
			return fmt.Sprintf(tr.T("%s, until %s"), text, DayMonthYearText(u, env))
		}
	}
	return text
}

// ruleText is a rule without its end; false when the rule cannot be
// described (several ordinals, a by-month-day list with a weekday, …).
func ruleText(r api.Recurrence, start time.Time, env Env) (string, bool) {
	tr := env.Tr
	n := max(1, r.Interval)
	switch r.Frequency {
	case api.FrequencyDaily:
		if len(r.ByDay) > 0 || len(r.ByMonthDay) > 0 || len(r.ByMonth) > 0 {
			return "", false
		}
		if n == 1 {
			return tr.T("Every day"), true
		}
		return fmt.Sprintf(tr.N("Every %d day", "Every %d days", n), n), true
	case api.FrequencyWeekly:
		return weeklyText(r, n, start, env)
	case api.FrequencyMonthly:
		return monthlyText(r, n, start, env)
	case api.FrequencyYearly:
		return yearlyText(r, n, start, env)
	}
	return "", false
}

func weeklyText(r api.Recurrence, n int, start time.Time, env Env) (string, bool) {
	tr := env.Tr
	if len(r.ByMonthDay) > 0 || len(r.ByMonth) > 0 {
		return "", false
	}
	days := []time.Weekday{start.Weekday()}
	if len(r.ByDay) > 0 {
		days = days[:0]
		for _, c := range r.ByDay {
			d, ord, ok := ParseDayCode(c)
			if !ok || ord != 0 {
				return "", false
			}
			if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
	}
	// In the week's order from Monday, as people read them.
	slices.SortFunc(days, func(a, b time.Weekday) int { return (int(a)+6)%7 - (int(b)+6)%7 })
	if n == 1 && slices.Equal(days, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}) {
		return tr.T("Every weekday"), true
	}
	if n == 1 && len(days) == 7 {
		return tr.T("Every day"), true
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = weekdayName(d, env)
	}
	list := ListText(names, tr)
	if n == 1 {
		// TRANSLATORS: a weekly recurrence; %s is a list of weekdays, "Every
		// week on Monday and Wednesday".
		return fmt.Sprintf(tr.T("Every week on %s"), list), true
	}
	// TRANSLATORS: a recurrence every few weeks; %d is the number of
	// weeks, %s a list of weekdays: "Every 2 weeks on Monday and Wednesday".
	return fmt.Sprintf(tr.N("Every %d week on %s", "Every %d weeks on %s", n), n, list), true
}

// monthPart is the "on …" of a monthly or yearly rule: the ordinal
// weekday, or the day of the month (from start when the rule names none).
func monthPart(r api.Recurrence, start time.Time, env Env) (kind int, ord int, day time.Weekday, mday int, ok bool) {
	switch {
	case len(r.ByDay) == 1 && len(r.ByMonthDay) == 0:
		d, o, ok := ParseDayCode(r.ByDay[0])
		if !ok || o == 0 {
			return 0, 0, 0, 0, false
		}
		return 1, o, d, 0, true
	case len(r.ByDay) == 0 && len(r.ByMonthDay) == 1:
		return 2, 0, 0, r.ByMonthDay[0], r.ByMonthDay[0] != 0
	case len(r.ByDay) == 0 && len(r.ByMonthDay) == 0:
		return 2, 0, 0, start.Day(), true
	}
	return 0, 0, 0, 0, false
}

func monthlyText(r api.Recurrence, n int, start time.Time, env Env) (string, bool) {
	tr := env.Tr
	if len(r.ByMonth) > 0 {
		return "", false
	}
	kind, ord, wd, mday, ok := monthPart(r, start, env)
	if !ok {
		return "", false
	}
	var on string
	switch {
	case kind == 1:
		// TRANSLATORS: the day of a monthly recurrence; %s is an ordinal
		// weekday such as "second Tuesday": "Monthly on the second Tuesday".
		on = fmt.Sprintf(tr.T("on the %s"), ordinalWeekday(ord, wd, env))
	case mday == -1:
		// TRANSLATORS: the day of a monthly recurrence: the month's last day.
		on = tr.T("on the last day")
	case mday < 0:
		return "", false
	default:
		// TRANSLATORS: the day of a monthly recurrence: "Monthly on day 14".
		on = fmt.Sprintf(tr.T("on day %d"), mday)
	}
	if n == 1 {
		// TRANSLATORS: a monthly recurrence; %s is its day: "Monthly on day
		// 14", "Monthly on the second Tuesday".
		return fmt.Sprintf(tr.T("Monthly %s"), on), true
	}
	// TRANSLATORS: a recurrence every few months; %d is the number of
	// months, %s the day: "Every 3 months on day 14".
	return fmt.Sprintf(tr.N("Every %d month %s", "Every %d months %s", n), n, on), true
}

func yearlyText(r api.Recurrence, n int, start time.Time, env Env) (string, bool) {
	tr := env.Tr
	month := start.Month()
	switch len(r.ByMonth) {
	case 0:
	case 1:
		if r.ByMonth[0] < 1 || r.ByMonth[0] > 12 {
			return "", false
		}
		month = time.Month(r.ByMonth[0])
	default:
		return "", false
	}
	kind, ord, wd, mday, ok := monthPart(r, start, env)
	if !ok || (kind == 2 && mday < 1) {
		return "", false
	}
	var on string
	if kind == 1 {
		monthName := MonthText(time.Date(2026, month, 1, 12, 0, 0, 0, env.loc()), env)
		// TRANSLATORS: the day of a yearly recurrence: an ordinal weekday
		// and a month, "on the second Tuesday of October".
		on = fmt.Sprintf(tr.T("on the %s of %s"), ordinalWeekday(ord, wd, env), monthName)
	} else {
		// A leap year, so that 29 February has a date.
		ref := time.Date(2028, month, mday, 12, 0, 0, 0, env.loc())
		if ref.Month() != month {
			return "", false
		}
		// TRANSLATORS: the day of a yearly recurrence: "on 14 October".
		on = fmt.Sprintf(tr.T("on %s"), DayMonthText(ref, env))
	}
	if n == 1 {
		// TRANSLATORS: a yearly recurrence; %s is its day: "Yearly on 14
		// October".
		return fmt.Sprintf(tr.T("Yearly %s"), on), true
	}
	// TRANSLATORS: a recurrence every few years; %d is the number of years,
	// %s the day: "Every 2 years on 14 October".
	return fmt.Sprintf(tr.N("Every %d year %s", "Every %d years %s", n), n, on), true
}

// Repeat is a choice of the editor's Repeat popup.
type Repeat int

// The choices, in the popup's order.
const (
	RepeatNever Repeat = iota
	RepeatDaily
	RepeatWeekly
	RepeatBiweekly
	RepeatMonthly
	RepeatYearly
	// RepeatCustom opens the small popover with interval and days; it is
	// also the choice of a rule none of the others is.
	RepeatCustom
)

// Repeats lists the choices in the popup's order.
var Repeats = []Repeat{RepeatNever, RepeatDaily, RepeatWeekly, RepeatBiweekly, RepeatMonthly, RepeatYearly, RepeatCustom}

// RepeatTitle is a choice's title in the popup.
func RepeatTitle(c Repeat, tr Translator) string {
	switch c {
	case RepeatDaily:
		return tr.T("Every Day")
	case RepeatWeekly:
		return tr.T("Every Week")
	case RepeatBiweekly:
		return tr.T("Every 2 Weeks")
	case RepeatMonthly:
		return tr.T("Every Month")
	case RepeatYearly:
		return tr.T("Every Year")
	case RepeatCustom:
		return tr.T("Custom…")
	}
	// TRANSLATORS: the editor's Repeat popup: the event does not repeat.
	return tr.T("Never")
}

// RepeatRecurrence is the rule of a choice for an event starting at
// start (in the event's zone): weekly rules name start's weekday,
// monthly its day, yearly its day and month. Never and Custom are nil
// (Custom's rule comes from CustomRecurrence).
func RepeatRecurrence(c Repeat, start time.Time) *api.Recurrence {
	day := []string{DayCode(start.Weekday())}
	switch c {
	case RepeatDaily:
		return &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1}
	case RepeatWeekly:
		return &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: day}
	case RepeatBiweekly:
		return &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 2, ByDay: day}
	case RepeatMonthly:
		return &api.Recurrence{Frequency: api.FrequencyMonthly, Interval: 1, ByMonthDay: []int{start.Day()}}
	case RepeatYearly:
		return &api.Recurrence{Frequency: api.FrequencyYearly, Interval: 1, ByMonth: []int{int(start.Month())},
			ByMonthDay: []int{start.Day()}}
	}
	return nil
}

// RepeatOf is the choice that shows r for an event starting at start:
// Never for nil, the choice whose rule r is (without an end), else Custom.
func RepeatOf(r *api.Recurrence, start time.Time) Repeat {
	if r == nil {
		return RepeatNever
	}
	if r.Complex || r.Count > 0 || r.Until != "" {
		return RepeatCustom
	}
	for _, c := range Repeats[1 : len(Repeats)-1] {
		if sameRule(*r, *RepeatRecurrence(c, start)) {
			return c
		}
	}
	// A weekly or monthly rule without days takes them from the start.
	bare := api.Recurrence{Frequency: r.Frequency, Interval: max(1, r.Interval)}
	if len(r.ByDay) == 0 && len(r.ByMonthDay) == 0 && len(r.ByMonth) == 0 {
		switch {
		case bare.Frequency == api.FrequencyDaily && bare.Interval == 1:
			return RepeatDaily
		case bare.Frequency == api.FrequencyWeekly && bare.Interval == 1:
			return RepeatWeekly
		case bare.Frequency == api.FrequencyWeekly && bare.Interval == 2:
			return RepeatBiweekly
		case bare.Frequency == api.FrequencyMonthly && bare.Interval == 1:
			return RepeatMonthly
		case bare.Frequency == api.FrequencyYearly && bare.Interval == 1:
			return RepeatYearly
		}
	}
	return RepeatCustom
}

func sameRule(a, b api.Recurrence) bool {
	return a.Frequency == b.Frequency && max(1, a.Interval) == max(1, b.Interval) &&
		slices.Equal(a.ByDay, b.ByDay) && slices.Equal(a.ByMonthDay, b.ByMonthDay) && slices.Equal(a.ByMonth, b.ByMonth)
}

// CustomRecurrence is the Custom… popover's rule: a frequency, an
// interval clamped to 1–MaxRecurrenceInterval and, for weekly, the days
// (none: start's day) in the week's order from Monday.
func CustomRecurrence(f api.RecurrenceFrequency, interval int, days []time.Weekday, start time.Time) api.Recurrence {
	r := api.Recurrence{Frequency: f, Interval: min(max(1, interval), api.MaxRecurrenceInterval)}
	switch f {
	case api.FrequencyWeekly:
		if len(days) == 0 {
			days = []time.Weekday{start.Weekday()}
		}
		ds := slices.Clone(days)
		slices.SortFunc(ds, func(a, b time.Weekday) int { return (int(a)+6)%7 - (int(b)+6)%7 })
		ds = slices.Compact(ds)
		for _, d := range ds {
			r.ByDay = append(r.ByDay, DayCode(d))
		}
	case api.FrequencyMonthly:
		r.ByMonthDay = []int{start.Day()}
	case api.FrequencyYearly:
		r.ByMonth, r.ByMonthDay = []int{int(start.Month())}, []int{start.Day()}
	}
	return r
}

// IntervalUnit is the Custom… popover's unit after the interval field:
// "day"/"days", "week"/"weeks", ….
func IntervalUnit(f api.RecurrenceFrequency, n int, tr Translator) string {
	switch f {
	case api.FrequencyWeekly:
		// TRANSLATORS: the unit after the interval of a custom recurrence,
		// "Every [2] weeks".
		return tr.N("week", "weeks", n)
	case api.FrequencyMonthly:
		// TRANSLATORS: the unit after the interval of a custom recurrence.
		return tr.N("month", "months", n)
	case api.FrequencyYearly:
		// TRANSLATORS: the unit after the interval of a custom recurrence.
		return tr.N("year", "years", n)
	}
	// TRANSLATORS: the unit after the interval of a custom recurrence.
	return tr.N("day", "days", n)
}

// EveryLabel is the Custom… popover's label before the interval field.
func EveryLabel(tr Translator) string {
	// TRANSLATORS: the custom recurrence popover: "Every [2] weeks".
	return tr.T("Every")
}
