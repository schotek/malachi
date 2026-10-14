// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"fmt"
	"time"
)

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
	// When is "Thu at 18:00" (DayAndTime of the weekday and the time).
	When string
	// Label is Title and When as one menu item (RemindItem).
	Label string
}

// The hours of the presets.
const (
	// remindMorningHour is the hour of This Morning, Tomorrow and Next
	// Week.
	remindMorningHour = 9
	// remindEveningHour is This Evening's hour, and the latest Later Today
	// may be.
	remindEveningHour = 20
	// remindEveningFrom is the hour from which This Evening is offered
	// (until remindNightFrom).
	remindEveningFrom = 17
	// remindNightFrom is the hour from which neither Later Today nor This
	// Evening is offered.
	remindNightFrom = 19
	// remindDawn is the hour before which a night still belongs to the day
	// before: Tomorrow then means this morning (This Morning).
	remindDawn = 5
)

// RemindItem is a remind preset's menu item: "Later Today, Thu at 18:00".
func RemindItem(title, when string, tr Translator) string {
	// TRANSLATORS: a reminder preset's menu item: its name and when it
	// comes back, such as "Later Today, Thu at 18:00".
	return fmt.Sprintf(tr.C("remind preset", "%s, %s"), title, when)
}

// RemindPresets are the presets for now, in menu order (which is the
// order of their times), by the wall clock of env.Loc:
//
//   - Later Today: three hours from now rounded up to the hour, offered
//     only when that is 20:00 or earlier the same day (so not from 17:01,
//     and never from 19:00) and not before dawn (05:00);
//   - This Evening: 20:00, offered from 17:00 to 18:59;
//   - Tomorrow: 09:00 tomorrow; before 05:00 the night still belongs to
//     yesterday, so it is 09:00 today, named This Morning;
//   - Next Week: 09:00 next Monday (a week ahead on a Monday).
//
// A time an earlier preset offers already (This Evening at 17:00 is Later
// Today; Next Week on a Sunday is Tomorrow) is offered once, under the
// first. Every preset lies after now.
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
		title := RemindPreset(kind, env.Tr)
		when := DayAndTime(env.Dates.Weekday(date), env.Dates.Time(date), env.Tr)
		out = append(out, RemindChoice{
			Kind: kind, Date: date, Title: title, When: when, Label: RemindItem(title, when, env.Tr),
		})
	}
	local := now.In(loc)
	y, m, d := local.Date()
	hour := local.Hour()
	later, ok := laterToday(now, loc)
	add(RemindLaterToday, later, ok)
	add(RemindThisEvening, time.Date(y, m, d, remindEveningHour, 0, 0, 0, loc),
		hour >= remindEveningFrom && hour < remindNightFrom)
	if hour < remindDawn {
		add(RemindThisMorning, time.Date(y, m, d, remindMorningHour, 0, 0, 0, loc), true)
	} else {
		add(RemindTomorrow, time.Date(y, m, d+1, remindMorningHour, 0, 0, 0, loc), true)
	}
	ahead := (int(time.Monday) - int(time.Date(y, m, d, 12, 0, 0, 0, loc).Weekday()) + 7) % 7
	if ahead == 0 {
		ahead = 7
	}
	add(RemindNextWeek, time.Date(y, m, d+ahead, remindMorningHour, 0, 0, 0, loc), true)
	return out
}

// laterToday is Later Today's time, false when it is not offered: three
// hours from now rounded up to the hour of the wall clock (an exact hour
// stays), when that is the same day at 20:00 or earlier and now is before
// 19:00.
func laterToday(now time.Time, loc *time.Location) (time.Time, bool) {
	local := now.In(loc)
	if local.Hour() >= remindNightFrom || local.Hour() < remindDawn {
		return time.Time{}, false
	}
	plus3 := now.Add(3 * time.Hour)
	l3 := plus3.In(loc)
	rounded := plus3.Add(-time.Duration(l3.Minute())*time.Minute - time.Duration(l3.Second())*time.Second -
		time.Duration(l3.Nanosecond()))
	if rounded.Before(plus3) {
		rounded = rounded.Add(time.Hour)
	}
	y, m, d := local.Date()
	if rounded.After(time.Date(y, m, d, remindEveningHour, 0, 0, 0, loc)) {
		return time.Time{}, false
	}
	return rounded, true
}
