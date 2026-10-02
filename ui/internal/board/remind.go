// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "time"

// Remind…: the presets the button's menu and the case menu offer. A remind
// hides a case until its time (board.remind); the daemon takes any time in
// the future up to a year ahead, so every preset lies after now.
//
// The macOS client leads (MalachiCore/Board/BoardRemind.swift); this is
// its port.

// RemindChoice is a remind time the menu offers (Swift
// Board.RemindPreset; its title is RemindPreset).
type RemindChoice struct {
	Kind RemindKind
	Date time.Time
	// Title is "Later Today".
	Title string
	// When is "Thu 18:00".
	When string
}

// The hours of the presets.
const (
	// remindMorningHour is the hour of Tomorrow and Next Week.
	remindMorningHour = 9
	// remindEveningHour is the hour Later Today does not go past while it
	// is an hour away.
	remindEveningHour = 18
)

// RemindPresets are the presets for now, in menu order: Later Today
// (three hours from now rounded up to the hour, or 18:00 when that comes
// first and is an hour away or more; none when neither is today), Tomorrow
// (09:00) and Next Week (09:00 next Monday, a week ahead on a Monday). A
// time an earlier preset offers already (Next Week on a Sunday is
// Tomorrow) is offered once, under the first. The days are those of
// env.Loc.
func RemindPresets(now time.Time, env Env) []RemindChoice {
	loc := env.loc()
	var out []RemindChoice
	add := func(kind RemindKind, date time.Time, ok bool) {
		if !ok || !date.After(now) {
			return
		}
		for _, p := range out {
			if p.Date.Equal(date) {
				return
			}
		}
		out = append(out, RemindChoice{
			Kind: kind, Date: date, Title: RemindPreset(kind, env.Tr),
			When: env.Dates.Weekday(date) + " " + env.Dates.Time(date),
		})
	}
	later, ok := laterToday(now, loc)
	add(RemindLaterToday, later, ok)
	y, m, d := now.In(loc).Date()
	add(RemindTomorrow, time.Date(y, m, d+1, remindMorningHour, 0, 0, 0, loc), true)
	ahead := (int(time.Monday) - int(time.Date(y, m, d, 12, 0, 0, 0, loc).Weekday()) + 7) % 7
	if ahead == 0 {
		ahead = 7
	}
	add(RemindNextWeek, time.Date(y, m, d+ahead, remindMorningHour, 0, 0, 0, loc), true)
	return out
}

// laterToday is Later Today's time, false when it would not be today.
func laterToday(now time.Time, loc *time.Location) (time.Time, bool) {
	plus3 := now.Add(3 * time.Hour)
	// Rounded up to the hour of the wall clock: an exact hour stays.
	local := plus3.In(loc)
	rounded := plus3.Add(-time.Duration(local.Minute())*time.Minute - time.Duration(local.Second())*time.Second -
		time.Duration(local.Nanosecond()))
	if rounded.Before(plus3) {
		rounded = rounded.Add(time.Hour)
	}
	pick := rounded
	y, m, d := now.In(loc).Date()
	evening := time.Date(y, m, d, remindEveningHour, 0, 0, 0, loc)
	if evening.Before(rounded) && !evening.Before(now.Add(time.Hour)) {
		pick = evening
	}
	py, pm, pd := pick.In(loc).Date()
	if py != y || pm != m || pd != d {
		return time.Time{}, false
	}
	return pick, true
}
