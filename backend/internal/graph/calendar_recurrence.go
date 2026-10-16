// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Graph's patternedRecurrence ⇄ recur.Rule. Graph expands its own series
// (D5), so a pattern read here only gives the editor its shape and the
// stored RRULE; a rule written must be one Graph can hold, else the write
// is recurrenceUnsupported.

var graphDays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

var graphDayNames = [7]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

var graphIndex = map[string]int{"first": 1, "second": 2, "third": 3, "fourth": 4, "last": -1}

func indexName(n int) (string, bool) {
	for k, v := range graphIndex {
		if v == n {
			return k, true
		}
	}
	return "", false
}

func unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: graph: "+format, append([]any{recur.ErrUnsupported}, args...)...)
}

func frequencyOfPattern(t string) api.RecurrenceFrequency {
	switch t {
	case "weekly":
		return api.FrequencyWeekly
	case "absoluteMonthly", "relativeMonthly":
		return api.FrequencyMonthly
	case "absoluteYearly", "relativeYearly":
		return api.FrequencyYearly
	}
	return api.FrequencyDaily
}

func daysOf(names []string) ([]time.Weekday, error) {
	if len(names) > 7 {
		return nil, unsupportedf("too many days")
	}
	var out []time.Weekday
	for _, n := range names {
		d, ok := graphDays[strings.ToLower(strings.TrimSpace(n))]
		if !ok {
			return nil, unsupportedf("unknown day")
		}
		out = append(out, d)
	}
	return out, nil
}

// ruleFromPattern reads a Graph recurrence as a validated rule for a
// series starting in loc (all-day: UNTIL as a date).
func ruleFromPattern(pr patternedRecurrence, allDay bool, loc *time.Location) (recur.Rule, error) {
	pt := pr.Pattern
	r := recur.Rule{Interval: pt.Interval, WeekStart: time.Monday}
	if r.Interval < 1 {
		r.Interval = 1
	}
	if r.Interval > 1000 {
		return recur.Rule{}, unsupportedf("interval")
	}
	if pt.FirstDayOfWeek != "" {
		if d, ok := graphDays[strings.ToLower(pt.FirstDayOfWeek)]; ok {
			r.WeekStart = d
		}
	}
	days, err := daysOf(pt.DaysOfWeek)
	if err != nil {
		return recur.Rule{}, err
	}
	relative := func() error {
		n, ok := graphIndex[strings.ToLower(pt.Index)]
		if pt.Index == "" {
			n, ok = 1, true
		}
		if !ok || len(days) == 0 {
			return unsupportedf("relative pattern")
		}
		if len(days) == 1 {
			r.ByDay = []recur.WeekdayNum{{N: n, Day: days[0]}}
			return nil
		}
		// "the first Monday or Tuesday": the n-th of those days.
		for _, d := range days {
			r.ByDay = append(r.ByDay, recur.WeekdayNum{Day: d})
		}
		r.BySetPos = []int{n}
		return nil
	}
	switch pt.Type {
	case "daily":
		r.Freq = recur.Daily
	case "weekly":
		r.Freq = recur.Weekly
		for _, d := range days {
			r.ByDay = append(r.ByDay, recur.WeekdayNum{Day: d})
		}
	case "absoluteMonthly":
		r.Freq = recur.Monthly
		if pt.DayOfMonth < 1 || pt.DayOfMonth > 31 {
			return recur.Rule{}, unsupportedf("dayOfMonth")
		}
		r.ByMonthDay = []int{pt.DayOfMonth}
	case "relativeMonthly":
		r.Freq = recur.Monthly
		if err := relative(); err != nil {
			return recur.Rule{}, err
		}
	case "absoluteYearly":
		r.Freq = recur.Yearly
		if pt.Month < 1 || pt.Month > 12 || pt.DayOfMonth < 1 || pt.DayOfMonth > 31 {
			return recur.Rule{}, unsupportedf("month or dayOfMonth")
		}
		r.ByMonth, r.ByMonthDay = []int{pt.Month}, []int{pt.DayOfMonth}
	case "relativeYearly":
		r.Freq = recur.Yearly
		if pt.Month < 1 || pt.Month > 12 {
			return recur.Rule{}, unsupportedf("month")
		}
		r.ByMonth = []int{pt.Month}
		if err := relative(); err != nil {
			return recur.Rule{}, err
		}
	default:
		return recur.Rule{}, unsupportedf("pattern type")
	}
	switch pr.Range.Type {
	case "numbered":
		if pr.Range.NumberOfOccurrences < 1 {
			return recur.Rule{}, unsupportedf("numberOfOccurrences")
		}
		r.Count = pr.Range.NumberOfOccurrences
	case "endDate":
		d, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(pr.Range.EndDate), time.UTC)
		if err != nil || d.Year() < recur.MinYear || d.Year() > recur.MaxUntilYear {
			return recur.Rule{}, unsupportedf("endDate")
		}
		if allDay {
			r.Until, r.UntilKind = d, recur.UntilDate
		} else {
			if loc == nil {
				loc = time.UTC
			}
			r.Until, r.UntilKind = time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, loc).UTC(), recur.UntilUTC
		}
	case "noEnd", "":
	default:
		return recur.Rule{}, unsupportedf("range type")
	}
	// Validate through the parser: the rule must be one recur accepts.
	parsed, err := recur.Parse(recur.Normalise(r))
	if err != nil {
		return recur.Rule{}, err
	}
	return parsed, nil
}

// patternFromRule writes a rule as Graph's recurrence for a series whose
// first instance starts at start (an instant; all-day: midnight as if
// UTC) in loc; zone is the Windows (or IANA) name for
// recurrenceTimeZone. A rule Graph cannot hold is recur.ErrUnsupported.
func patternFromRule(r recur.Rule, start time.Time, loc *time.Location, allDay bool, zone string) (patternedRecurrence, error) {
	if loc == nil || allDay {
		loc = time.UTC
	}
	local := start.In(loc)
	var pr patternedRecurrence
	pt := &pr.Pattern
	pt.Interval = max(r.Interval, 1)
	if len(r.BySecond)+len(r.ByMinute)+len(r.ByHour)+len(r.ByYearDay)+len(r.ByWeekNo) > 0 || len(r.BySetPos) > 1 {
		return pr, unsupportedf("rule parts Graph has no pattern for")
	}
	var plain []time.Weekday
	var ordinal []recur.WeekdayNum
	for _, d := range r.ByDay {
		if d.N == 0 {
			plain = append(plain, d.Day)
		} else {
			ordinal = append(ordinal, d)
		}
	}
	names := func(days []time.Weekday) []string {
		out := make([]string, 0, len(days))
		for _, d := range days {
			out = append(out, graphDayNames[d%7])
		}
		return out
	}
	// relative fills daysOfWeek and index from one ordinal day or plain
	// days with one BYSETPOS.
	relative := func() (bool, error) {
		switch {
		case len(ordinal) == 1 && len(plain) == 0 && len(r.BySetPos) == 0:
			idx, ok := indexName(ordinal[0].N)
			if !ok {
				return false, unsupportedf("ordinal")
			}
			pt.DaysOfWeek, pt.Index = names([]time.Weekday{ordinal[0].Day}), idx
			return true, nil
		case len(ordinal) == 0 && len(plain) > 0 && len(r.BySetPos) == 1:
			idx, ok := indexName(r.BySetPos[0])
			if !ok {
				return false, unsupportedf("set position")
			}
			pt.DaysOfWeek, pt.Index = names(plain), idx
			return true, nil
		case len(r.ByDay) > 0 || len(r.BySetPos) > 0:
			return false, unsupportedf("days")
		}
		return false, nil
	}
	oneDay := func() (int, error) {
		switch len(r.ByMonthDay) {
		case 0:
			return local.Day(), nil
		case 1:
			if r.ByMonthDay[0] < 1 {
				return 0, unsupportedf("negative month day")
			}
			return r.ByMonthDay[0], nil
		}
		return 0, unsupportedf("several month days")
	}
	switch r.Freq {
	case recur.Daily:
		if len(r.ByMonthDay)+len(r.ByMonth)+len(r.BySetPos)+len(ordinal) > 0 {
			return pr, unsupportedf("daily with filters")
		}
		pt.Type = "daily"
		if len(plain) > 0 {
			if pt.Interval != 1 {
				return pr, unsupportedf("daily on some days with an interval")
			}
			pt.Type, pt.DaysOfWeek = "weekly", names(plain)
		}
	case recur.Weekly:
		if len(r.ByMonthDay)+len(r.ByMonth)+len(r.BySetPos)+len(ordinal) > 0 {
			return pr, unsupportedf("weekly with filters")
		}
		pt.Type = "weekly"
		if len(plain) == 0 {
			plain = []time.Weekday{local.Weekday()}
		}
		pt.DaysOfWeek = names(plain)
		pt.FirstDayOfWeek = graphDayNames[r.WeekStart%7]
	case recur.Monthly:
		if len(r.ByMonth) > 0 {
			return pr, unsupportedf("monthly with months")
		}
		rel, err := relative()
		if err != nil {
			return pr, err
		}
		if rel {
			if len(r.ByMonthDay) > 0 {
				return pr, unsupportedf("monthly with days and month days")
			}
			pt.Type = "relativeMonthly"
			break
		}
		d, err := oneDay()
		if err != nil {
			return pr, err
		}
		pt.Type, pt.DayOfMonth = "absoluteMonthly", d
	case recur.Yearly:
		month := int(local.Month())
		switch len(r.ByMonth) {
		case 0:
		case 1:
			month = r.ByMonth[0]
		default:
			return pr, unsupportedf("several months")
		}
		pt.Month = month
		rel, err := relative()
		if err != nil {
			return pr, err
		}
		if rel {
			if len(r.ByMonthDay) > 0 {
				return pr, unsupportedf("yearly with days and month days")
			}
			pt.Type = "relativeYearly"
			break
		}
		d, err := oneDay()
		if err != nil {
			return pr, err
		}
		pt.Type, pt.DayOfMonth = "absoluteYearly", d
	default:
		return pr, unsupportedf("frequency")
	}
	pr.Range.StartDate = local.Format(time.DateOnly)
	pr.Range.RecurrenceTimeZone = zone
	switch {
	case r.Count > 0:
		pr.Range.Type, pr.Range.NumberOfOccurrences = "numbered", r.Count
	case r.UntilKind == recur.UntilUTC:
		pr.Range.Type, pr.Range.EndDate = "endDate", r.Until.In(loc).Format(time.DateOnly)
	case r.UntilKind == recur.UntilDate, r.UntilKind == recur.UntilFloating:
		pr.Range.Type, pr.Range.EndDate = "endDate", r.Until.Format(time.DateOnly)
	default:
		pr.Range.Type = "noEnd"
	}
	return pr, nil
}
