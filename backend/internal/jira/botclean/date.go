// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dateRE finds the time in a header's date part: day and month in either
// order, a year of two to four digits, hours and minutes, then optionally
// AM/PM (any case), a zone abbreviation and a numeric offset. The
// prototype's pattern, except that AM/PM and the abbreviation must end at a
// word boundary ("AMT" is a zone, not AM and a stray T; "Europe/Prague" is
// no abbreviation) and that an offset of three or four digits is read as
// hours and minutes ("+530" is +05:30).
var dateRE = regexp.MustCompile(`(\d{1,2})/(\d{1,2})/(\d{2,4})\s+(\d{1,2}):(\d{2})\s*(?:([AaPp][Mm])\b)?\s*(?:([A-Za-z]{2,5})\b)?\s*([+-](?:\d{1,2}:?\d{2}|\d{1,2}))?`)

// zones are the abbreviations with one fixed offset. There is no time zone
// database here, so that the daemon behaves the same on every platform
// (Windows has none). Ambiguous abbreviations are left out (IST, AST, SST)
// or read the way an English-language Jira most likely means them: BST is
// British Summer Time, CST is US Central. CET, whose offset changes, is
// centralEuropean.
var zones = map[string]time.Duration{
	"UT": 0, "UTC": 0, "GMT": 0,
	"WET": 0, "WEST": time.Hour, "BST": time.Hour,
	"CEST": 2 * time.Hour,
	"EET":  2 * time.Hour, "EEST": 3 * time.Hour, "MSK": 3 * time.Hour,
	"EST": -5 * time.Hour, "EDT": -4 * time.Hour,
	"CST": -6 * time.Hour, "CDT": -5 * time.Hour,
	"MST": -7 * time.Hour, "MDT": -6 * time.Hour,
	"PST": -8 * time.Hour, "PDT": -7 * time.Hour,
	"AKST": -9 * time.Hour, "AKDT": -8 * time.Hour, "HST": -10 * time.Hour,
	"JST": 9 * time.Hour, "AEST": 10 * time.Hour, "AEDT": 11 * time.Hour,
	"NZST": 12 * time.Hour, "NZDT": 13 * time.Hour,
}

// parseDate reads the date part of a header, like the prototype:
//
//   - The first number is the day and the second the month (dd/MM, the
//     user's decision), unless the first is over 12, or only the second is
//     over 12, which makes it MM/dd. The date must exist (31/02 does not).
//   - A year under 100 is 2000 + year.
//   - PM adds 12 to an hour under 12, AM makes 12 midnight; the result must
//     be a valid hour and minute (a "13:00 PM" stays 13:00, as before).
//   - A numeric offset wins over an abbreviation: "+2", "+02", "+0200",
//     "+02:00", with or without a "GMT"/"UTC" before it, up to ±18:00.
//   - "CET" is Central European time as the prototype read it through
//     Europe/Prague: +1, but +2 in summer by the EU rule (from 01:00 UTC on
//     the last Sunday of March to 01:00 UTC on the last Sunday of October,
//     applied to every year). A time the change skips is read in winter time
//     and one it repeats in summer time, as java.time did. "CEST" is always
//     +2 (the prototype made it +1 in winter). Other known abbreviations
//     are in zones.
//   - With no zone, or one not in the table, the time is read in the
//     location of fallback (the prototype used the computer's zone).
//
// ok is false when no date is found or it is not valid.
func parseDate(s string, fallback time.Time) (t time.Time, ok bool) {
	m := dateRE.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	first, second := atoi(m[1]), atoi(m[2])
	day, month := first, second
	if first <= 12 && second > 12 {
		day, month = second, first
	}
	if day < 1 || day > 31 || month < 1 || month > 12 {
		return time.Time{}, false
	}
	year := atoi(m[3])
	if year < 100 {
		year += 2000
	}
	hour, minute := atoi(m[4]), atoi(m[5])
	switch strings.ToUpper(m[6]) {
	case "PM":
		if hour < 12 {
			hour += 12
		}
	case "AM":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return time.Time{}, false
	}
	// wall holds the clock reading in UTC's fields.
	wall := time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
	if wall.Day() != day {
		return time.Time{}, false
	}

	if m[8] != "" {
		off, ok := parseOffset(m[8])
		if !ok {
			return time.Time{}, false
		}
		return inOffset(wall, off), true
	}
	zone := strings.ToUpper(m[7])
	if zone == "CET" {
		return centralEuropean(wall), true
	}
	if off, known := zones[zone]; known {
		return inOffset(wall, off), true
	}
	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, fallback.Location()), true
}

// parseOffset reads "+2", "-05", "+0530", "+05:30".
func parseOffset(s string) (time.Duration, bool) {
	sign := time.Duration(1)
	if s[0] == '-' {
		sign = -1
	}
	digits := strings.ReplaceAll(s[1:], ":", "")
	hours, minutes := atoi(digits), 0
	if len(digits) > 2 {
		hours, minutes = atoi(digits[:len(digits)-2]), atoi(digits[len(digits)-2:])
	}
	if hours > 18 || minutes > 59 || (hours == 18 && minutes > 0) {
		return 0, false
	}
	return sign * (time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute), true
}

// inOffset is the instant at which the clock reading wall is shown in a zone
// off from UTC.
func inOffset(wall time.Time, off time.Duration) time.Time {
	return wall.Add(-off).In(time.FixedZone("", int(off/time.Second)))
}

// centralEuropean reads the clock reading wall in Central European time.
func centralEuropean(wall time.Time) time.Time {
	if summer := wall.Add(-2 * time.Hour); euSummerTime(summer) {
		return inOffset(wall, 2*time.Hour)
	}
	return inOffset(wall, time.Hour)
}

// euSummerTime reports whether the instant u is in EU summer time.
func euSummerTime(u time.Time) bool {
	y := u.Year()
	start := lastSunday(y, time.March).Add(time.Hour)
	end := lastSunday(y, time.October).Add(time.Hour)
	return !u.Before(start) && u.Before(end)
}

// lastSunday is midnight UTC of the last Sunday of the month.
func lastSunday(year int, month time.Month) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
	return last.AddDate(0, 0, -int(last.Weekday()))
}

// atoi reads a string of at most four ASCII digits the pattern guarantees.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
