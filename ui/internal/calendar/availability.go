// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The editor's availability rows (calendar.freeBusy, Graph only): per
// person the busy spans on the editor's day axis, "availability unknown"
// for people without free/busy, the summary line of the proposed time and
// Next Free Slot (calendar.findFreeTime).

// AxisSpan is a busy span on an axis, as fractions 0–1 of its length.
type AxisSpan struct {
	From, To float64
	ShowAs   api.ShowAs
}

// AvailabilityRow is one person's row.
type AvailabilityRow struct {
	// Label is "You" or the attendee's name (else address).
	Label   string
	Address string
	Known   bool
	Busy    []AxisSpan
}

// Busy reports a show-as that blocks a meeting: busy, out of office,
// tentative and unknown; free and working elsewhere do not.
func Busy(s api.ShowAs) bool {
	return s != api.ShowAsFree && s != api.ShowAsWorkingElsewhere
}

// axisSpans places the busy intervals on [from, to).
func axisSpans(busy []api.BusyInterval, from, to time.Time) []AxisSpan {
	total := to.Sub(from)
	if total <= 0 {
		return nil
	}
	var out []AxisSpan
	for _, b := range busy {
		if !Busy(b.ShowAs) || !b.End.After(from) || !b.Start.Before(to) || !b.End.After(b.Start) {
			continue
		}
		s, e := maxTime(b.Start, from), minTime(b.End, to)
		out = append(out, AxisSpan{From: float64(s.Sub(from)) / float64(total),
			To: float64(e.Sub(from)) / float64(total), ShowAs: b.ShowAs})
	}
	slices.SortStableFunc(out, func(a, b AxisSpan) int {
		switch {
		case a.From < b.From:
			return -1
		case a.From > b.From:
			return 1
		}
		return 0
	})
	return out
}

// AvailabilityRows are the rows of a freeBusy result on the axis [from,
// to): the user first, then the attendees in the order asked; names maps
// an address to the name the recipient field shows.
func AvailabilityRows(res api.CalendarFreeBusyResult, names map[string]string, from, to time.Time, tr Translator) []AvailabilityRow {
	rows := []AvailabilityRow{{Label: You(tr), Address: res.Me.Address, Known: true,
		Busy: axisSpans(res.Me.Busy, from, to)}}
	for _, a := range res.Attendees {
		label := cleanLine(names[a.Address])
		if label == "" {
			label = cleanLine(a.Address)
		}
		row := AvailabilityRow{Label: label, Address: a.Address, Known: a.Known}
		if a.Known {
			row.Busy = axisSpans(a.Busy, from, to)
		}
		rows = append(rows, row)
	}
	return rows
}

// You labels the user's own row.
func You(tr Translator) string {
	// TRANSLATORS: the user's own row of the availability rows.
	return tr.T("You")
}

// Unknown is the row text of a person without free/busy.
func Unknown(tr Translator) string { return tr.T("availability unknown") }

// busyDuring reports whether busy blocks any of [start, end).
func busyDuring(busy []api.BusyInterval, start, end time.Time) bool {
	for _, b := range busy {
		if Busy(b.ShowAs) && b.Start.Before(end) && b.End.After(start) {
			return true
		}
	}
	return false
}

// Summary is the availability line of the proposed time [start, end):
// "10:00–11:00: all known attendees are free" or "2 of 3 attendees are
// busy" (the user and unknown people left out), with the unknown count
// on a second line. "" without attendees.
func Summary(res api.CalendarFreeBusyResult, start, end time.Time, env Env) []string {
	tr := env.Tr
	known, busy, unknown := 0, 0, 0
	for _, a := range res.Attendees {
		if !a.Known {
			unknown++
			continue
		}
		known++
		if busyDuring(a.Busy, start, end) {
			busy++
		}
	}
	var lines []string
	switch {
	case known > 0 && busy == 0:
		// TRANSLATORS: the availability summary; %s is the proposed time,
		// "10:00–11:00".
		lines = append(lines, fmt.Sprintf(tr.T("%s: all known attendees are free"), TimeRangeText(start, end, env)))
	case known > 0:
		// TRANSLATORS: the availability summary: how many of the attendees
		// whose availability is known are busy at the proposed time.
		lines = append(lines, fmt.Sprintf(tr.N("%d of %d attendees is busy", "%d of %d attendees are busy", busy), busy, known))
	}
	if unknown > 0 {
		lines = append(lines, fmt.Sprintf(tr.N("Availability of %d attendee is unknown",
			"Availability of %d attendees is unknown", unknown), unknown))
	}
	return lines
}

// NextFreeSlot is the first slot of a findFreeTime result that starts
// at or after after; false when there is none.
func NextFreeSlot(res api.CalendarFindFreeTimeResult, after time.Time) (api.TimeSlot, bool) {
	best, ok := api.TimeSlot{}, false
	for _, s := range res.Slots {
		if s.Start.Before(after) || !s.End.After(s.Start) {
			continue
		}
		if !ok || s.Start.Before(best.Start) {
			best, ok = s, true
		}
	}
	return best, ok
}

// NextFreeSlotButton is the button that moves the event to the next free
// slot.
func NextFreeSlotButton(tr Translator) string { return tr.T("Next Free Slot") }

// NextFreeSlotText names the next free slot, "Next free slot: Wednesday
// 15 October 2026 · 14:00–15:00".
func NextFreeSlotText(s api.TimeSlot, env Env) string {
	// TRANSLATORS: the editor's availability: the next time everyone is
	// free; %s is its date and time.
	return fmt.Sprintf(env.Tr.T("Next free slot: %s"), dots(env.Tr, LongDateText(s.Start, env), TimeRangeText(s.Start, s.End, env)))
}

// NoFreeSlot is the text when findFreeTime found nothing.
func NoFreeSlot(tr Translator) string { return tr.T("No free slot was found in the next two weeks.") }
