// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The event a list_events or get_event result names, for the answer's
// Show in Calendar card (docs/calendar.md §5.9). Port of macos
// MalachiCore/Assistant/AssistantCalendar.swift (Assistant.CalendarRef,
// Assistant.parseCalendarResult).

// CalendarRef is the event a list_events or get_event result names first:
// its id and instance, and its day (Start for a timed event, Date for an
// all-day one).
type CalendarRef struct {
	EventID      api.EventID
	RecurrenceID string
	// Start is the start of a timed event (nil for an all-day one).
	Start *time.Time
	// Date is the day of an all-day event ("" for a timed one).
	Date api.Date
}

// Recurrence is the instance, read as the API reads it: an RFC 3339
// instant or a date; false for none or neither.
func (r CalendarRef) Recurrence() (api.DateOrTime, bool) {
	if _, err := time.Parse(time.RFC3339, r.RecurrenceID); err == nil {
		return api.DateOrTime(r.RecurrenceID), true
	}
	if validDate(api.Date(r.RecurrenceID)) {
		return api.DateOrTime(r.RecurrenceID), true
	}
	return "", false
}

// Day is the day the Calendar opens on, in loc (the instant of a timed
// event, else the midnight of the date); false for neither.
func (r CalendarRef) Day(loc *time.Location) (time.Time, bool) {
	if r.Start != nil {
		return *r.Start, true
	}
	if t, err := time.ParseInLocation("2006-01-02", string(r.Date), loc); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func validDate(d api.Date) bool {
	_, err := time.Parse("2006-01-02", string(d))
	return err == nil
}

// ParseCalendarResult is the first event a result of the bridge's
// list_events or get_event names (tool with or without the bridge's
// prefix); false for another tool, a result without an event, or text
// that is not the bridge's. Only the bridge's own lines before the first
// fence are read (ids and times it writes itself, one line each); the
// titles and notes inside the fence are written by third parties and never
// looked at. Swift: Assistant.parseCalendarResult.
func ParseCalendarResult(tool, text string) (CalendarRef, bool) {
	name := strings.TrimPrefix(tool, toolPrefix)
	if name != "list_events" && name != "get_event" {
		return CalendarRef{}, false
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "--- BEGIN UNTRUSTED") || strings.HasPrefix(line, "titles (") ||
			strings.HasPrefix(line, "text fields:") {
			break
		}
		lines = append(lines, line)
	}
	var ref CalendarRef
	if name == "list_events" {
		var row string
		found := false
		for _, l := range lines {
			if strings.HasPrefix(l, "1. ") {
				row, found = l[3:], true
				break
			}
		}
		if !found {
			return CalendarRef{}, false
		}
		words := spaceWords(row)
		id, ok := wordField("eventId=", words)
		if len(words) == 0 || !ok || id == "" {
			return CalendarRef{}, false
		}
		ref.EventID = api.EventID(id)
		ref.RecurrenceID, _ = wordField("recurrenceId=", words)
		if words[0] == "all-day" && len(words) > 1 {
			ref.Date = allDayDate(words[1])
		} else {
			ref.Start = startTime(words[0])
		}
	} else {
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "event ") {
			return CalendarRef{}, false
		}
		words := spaceWords(lines[0][len("event "):])
		if len(words) == 0 || strings.Contains(words[0], "=") {
			return CalendarRef{}, false
		}
		ref.EventID = api.EventID(words[0])
		ref.RecurrenceID, _ = wordField("recurrenceId=", words)
		var tline string
		found := false
		for _, l := range lines {
			if strings.HasPrefix(l, "time: ") {
				tline, found = l[len("time: "):], true
				break
			}
		}
		if !found {
			return CalendarRef{}, false
		}
		t := spaceWords(tline)
		if len(t) > 1 && t[0] == "all-day" {
			ref.Date = allDayDate(t[1])
		} else if len(t) > 0 {
			ref.Start = startTime(t[0])
		}
	}
	if ref.Start == nil && ref.Date == "" {
		return CalendarRef{}, false
	}
	return ref, true
}

// spaceWords splits on single spaces, dropping empty words.
func spaceWords(s string) []string {
	var out []string
	for _, w := range strings.Split(s, " ") {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

func wordField(key string, words []string) (string, bool) {
	for _, w := range words {
		if strings.HasPrefix(w, key) {
			return w[len(key):], true
		}
	}
	return "", false
}

// allDayDate: "2026-10-16..2026-10-17" → its first date, when a real one.
func allDayDate(s string) api.Date {
	if len(s) > 10 {
		s = s[:10]
	}
	if d := api.Date(s); validDate(d) {
		return d
	}
	return ""
}

// startTime: "2026-10-16T08:00:00Z..2026-10-16T09:00:00Z" → its start.
func startTime(s string) *time.Time {
	first, _, _ := strings.Cut(s, "..")
	t, err := time.Parse(time.RFC3339, first)
	if err != nil {
		return nil
	}
	return &t
}
