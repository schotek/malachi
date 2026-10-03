// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package maildate projects inbox rows into collapsible sections without
// changing the underlying message order. It mirrors MalachiCore/MailDateGroups.
package maildate

import (
	"strconv"
	"strings"
	"time"
)

type Kind int

const (
	Flagged Kind = iota
	Today
	Yesterday
	ThisWeek
	LastWeek
	ThisMonth
	LastMonth
	ThisYear
	OlderYear
)

type Group struct {
	Kind Kind
	Year int
}
type Translator interface{ T(string) string }

func (g Group) Title(tr Translator) string {
	switch g.Kind {
	case Flagged:
		return tr.T("Flagged")
	case Today:
		return tr.T("Today")
	case Yesterday:
		return tr.T("Yesterday")
	case ThisWeek:
		return tr.T("This week")
	case LastWeek:
		return tr.T("Last week")
	case ThisMonth:
		return tr.T("This month")
	case LastMonth:
		return tr.T("Last month")
	case ThisYear:
		return tr.T("This year")
	default:
		return strconv.Itoa(g.Year)
	}
}

func Containing(date, now time.Time, firstDay time.Weekday) Group {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	week := today.AddDate(0, 0, -(int(today.Weekday())-int(firstDay)+7)%7)
	month := time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
	boundaries := []struct {
		start time.Time
		kind  Kind
	}{
		{today, Today}, {today.AddDate(0, 0, -1), Yesterday},
		{week, ThisWeek}, {week.AddDate(0, 0, -7), LastWeek},
		{month, ThisMonth}, {month.AddDate(0, -1, 0), LastMonth},
		{time.Date(y, 1, 1, 0, 0, 0, 0, now.Location()), ThisYear},
	}
	for _, b := range boundaries {
		if !date.Before(b.start) {
			return Group{Kind: b.kind}
		}
	}
	return Group{Kind: OlderYear, Year: date.In(now.Location()).Year()}
}

type Row[K comparable] struct {
	Key             K
	Date            time.Time
	Flagged, Member bool
}
type Section[K comparable] struct {
	Group Group
	Keys  []K
}

// Sections keeps expanded members with their parent, and moves flagged
// parents to the first section. It preserves order inside every section.
func Sections[K comparable](rows []Row[K], now time.Time, firstDay time.Weekday) []Section[K] {
	var sections []Section[K]
	index := make(map[Group]int)
	var parent Group
	for i, row := range rows {
		group := Containing(row.Date, now, firstDay)
		if row.Flagged {
			group = Group{Kind: Flagged}
		}
		if row.Member && i > 0 {
			group = parent
		} else {
			parent = group
		}
		at, ok := index[group]
		if !ok {
			at = len(sections)
			index[group] = at
			sections = append(sections, Section[K]{Group: group})
		}
		sections[at].Keys = append(sections[at].Keys, row.Key)
	}
	if at, ok := index[Group{Kind: Flagged}]; ok && at > 0 {
		flagged := sections[at]
		copy(sections[1:at+1], sections[:at])
		sections[0] = flagged
	}
	return sections
}

// FirstWeekday reads glibc's `locale -k LC_TIME`. Its first_weekday is
// relative to week-1stday (usually Sunday 19971130, sometimes Monday
// 19971201), not an ISO weekday. Unsupported locale implementations use Monday.
func FirstWeekday(output string) time.Weekday {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.Trim(v, "\"")
		}
	}
	first, err := strconv.Atoi(values["first_weekday"])
	anchor, anchorErr := time.Parse("20060102", values["week-1stday"])
	if err != nil || anchorErr != nil || first < 1 || first > 7 {
		return time.Monday
	}
	return time.Weekday((int(anchor.Weekday()) + first - 1) % 7)
}
