// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Spreadsheet values as stored, not as displayed (design §4): a number
// with up to 15 significant digits, plain from 1e-9 to below 1e15 and with
// an exponent outside that; a percentage times 100 with "%"; a date or
// time ISO-like. Only what kind of number a format shows is taken from it,
// never its digits, separators, colours or text.

// numKind is what a number format shows a number as.
type numKind uint8

const (
	kindNumber   numKind = iota // a number
	kindPercent                 // a percentage
	kindDate                    // a date: YYYY-MM-DD
	kindTime                    // a time of day: HH:MM[:SS]
	kindDateTime                // both
	kindElapsed                 // a duration in hours: H:MM[:SS] ([h], [m], [s])
	kindDateAuto                // a locale's built-in date format: date, time or both by the value
)

// numFormat is a number format as far as the text cares.
type numFormat struct {
	kind    numKind
	seconds bool // the format shows seconds
}

// dateSystem is how a workbook counts days.
type dateSystem uint8

const (
	// date1900 counts from 1900-01-01 as day 1 and keeps Lotus 1-2-3's
	// leap-year bug: day 60 is 1900-02-29.
	date1900 dateSystem = iota
	// date1900NoBug counts from 1899-12-30 as day 0 without the bug
	// (workbookPr dateCompatibility="false", as LibreOffice writes).
	date1900NoBug
	// date1904 counts from 1904-01-01 as day 0 (workbookPr date1904).
	date1904
)

// maxSerial is 9999-12-31 in the 1900 date system; a larger serial, or a
// negative one, is shown as a number.
const maxSerial = 2958465

// builtinFormat is a built-in number format by its id. Ids it does not
// list show a number.
func builtinFormat(id int) numFormat {
	switch {
	case id == 9 || id == 10:
		return numFormat{kind: kindPercent}
	case id >= 14 && id <= 17:
		return numFormat{kind: kindDate}
	case id == 18 || id == 20:
		return numFormat{kind: kindTime}
	case id == 19 || id == 21 || id == 45 || id == 47:
		return numFormat{kind: kindTime, seconds: true}
	case id == 22:
		return numFormat{kind: kindDateTime}
	case id == 46:
		return numFormat{kind: kindElapsed, seconds: true}
	case id >= 27 && id <= 36, id >= 50 && id <= 58:
		return numFormat{kind: kindDateAuto}
	}
	return numFormat{}
}

// parseFormat reads a custom format code: its first section (the one for
// positive numbers), outside quoted text, escaped characters ("\x", "_x",
// "*x") and brackets other than the elapsed-time ones ([h], [mm], [ss]).
// Year and day letters make a date; hours, seconds and AM/PM a time; "m"
// is a minute next to them and a month otherwise; "%" a percentage.
func parseFormat(code string) numFormat {
	var date, clock, minute, seconds, elapsed, percent bool
scan:
	for i := 0; i < len(code); {
		c := code[i]
		switch {
		case c == ';':
			break scan
		case c == '"':
			j := strings.IndexByte(code[i+1:], '"')
			if j < 0 {
				break scan
			}
			i += j + 2
			continue
		case c == '\\' || c == '_' || c == '*':
			i++
			if i < len(code) {
				_, n := utf8.DecodeRuneInString(code[i:])
				i += n
			}
			continue
		case c == '[':
			j := strings.IndexByte(code[i+1:], ']')
			if j < 0 {
				break scan
			}
			if inner := asciiLower(code[i+1 : i+1+j]); elapsedToken(inner) {
				elapsed = true
				seconds = seconds || inner[0] == 's'
			}
			i += j + 2
			continue
		case c == '%':
			percent = true
		case foldPrefix(code[i:], "general"):
			i += len("general")
			continue
		case foldPrefix(code[i:], "am/pm"):
			clock = true
			i += len("am/pm")
			continue
		case foldPrefix(code[i:], "a/p"):
			clock = true
			i += len("a/p")
			continue
		default:
			switch c | 0x20 {
			case 'y', 'd':
				date = true
			case 'h':
				clock = true
			case 's':
				clock, seconds = true, true
			case 'm':
				minute = true
			case 'e':
				// "E+" and "E-" are an exponent; a lone "e" is a year.
				if i+1 >= len(code) || code[i+1] != '+' && code[i+1] != '-' {
					date = true
				}
			}
		}
		i++
	}
	month := minute && !clock
	switch {
	case elapsed && !date:
		return numFormat{kind: kindElapsed, seconds: seconds}
	case (date || month) && clock:
		return numFormat{kind: kindDateTime, seconds: seconds}
	case date || month:
		return numFormat{kind: kindDate}
	case clock:
		return numFormat{kind: kindTime, seconds: seconds}
	case percent:
		return numFormat{kind: kindPercent}
	}
	return numFormat{}
}

// elapsedToken reports a bracketed elapsed-time token: [h], [hh], [m],
// [mm], [s], [ss], ….
func elapsedToken(s string) bool {
	if s == "" || s[0] != 'h' && s[0] != 'm' && s[0] != 's' {
		return false
	}
	return strings.Count(s, s[:1]) == len(s)
}

// foldPrefix reports whether s starts with prefix (lower-case ASCII),
// ignoring ASCII case.
func foldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && asciiLower(s[:len(prefix)]) == prefix
}

// formatNumeric shows the stored value v of a number cell in format f. A
// value that is not a plain decimal number is shown as it is stored.
func formatNumeric(v string, f numFormat, sys dateSystem) string {
	x, ok := parseDecimal(v)
	if !ok {
		return v
	}
	switch f.kind {
	case kindPercent:
		if p := x * 100; !math.IsInf(p, 0) {
			return formatNumber(p) + "%"
		}
	case kindDate, kindTime, kindDateTime, kindElapsed, kindDateAuto:
		if s, ok := formatSerial(x, f, sys); ok {
			return s
		}
	}
	return formatNumber(x)
}

// parseDecimal reads a decimal number as SpreadsheetML stores one: an
// optional sign, digits with an optional fraction, an optional exponent.
// Anything else (hexadecimal, "NaN", "Inf", digits with underscores, a
// number too large for a float64) is not one.
func parseDecimal(s string) (float64, bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		digits++
	}
	if i < len(s) && s[i] == '.' {
		for i++; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			digits++
		}
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			exp++
		}
		if exp == 0 {
			return 0, false
		}
	}
	if i != len(s) {
		return 0, false
	}
	x, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(x, 0) || math.IsNaN(x) {
		return 0, false
	}
	return x, true
}

// formatNumber shows x with at most 15 significant digits, trailing zeros
// dropped: plain when 1e-9 <= |x| < 1e15 after rounding, else with an
// exponent ("1.5E+20").
func formatNumber(x float64) string {
	if x == 0 {
		return "0"
	}
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	s := strconv.FormatFloat(x, 'e', 14, 64)
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	mant, e, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(e)
	digits := strings.TrimRight(mant[:1]+mant[2:], "0")
	var out string
	switch {
	case exp >= 15 || exp < -9:
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		es := strconv.Itoa(exp)
		if len(es) < 2 {
			es = "0" + es
		}
		out += "E" + sign + es
	case exp >= 0:
		if len(digits) <= exp+1 {
			out = digits + strings.Repeat("0", exp+1-len(digits))
		} else {
			out = digits[:exp+1] + "." + digits[exp+1:]
		}
	default:
		out = "0." + strings.Repeat("0", -exp-1) + digits
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatSerial shows a serial date-time x in format f: false when x is
// outside 0..maxSerial or the date is after 9999. The time is rounded to
// the second.
func formatSerial(x float64, f numFormat, sys dateSystem) (string, bool) {
	if !(x >= 0 && x <= maxSerial) {
		return "", false
	}
	days := math.Floor(x)
	secs := int(math.Round((x - days) * 86400))
	if secs >= 86400 {
		days++
		secs -= 86400
	}
	d := int(days)
	showSecs := f.seconds || secs%60 != 0
	if f.kind == kindElapsed {
		return clockText(d*24+secs/3600, secs/60%60, secs%60, showSecs, false), true
	}
	var y int
	var m time.Month
	var dd int
	switch {
	case sys == date1904:
		y, m, dd = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Date()
	case sys == date1900NoBug:
		y, m, dd = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Date()
	case d == 60:
		y, m, dd = 1900, time.February, 29
	case d < 60:
		y, m, dd = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Date()
	default:
		y, m, dd = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Date()
	}
	if y > 9999 {
		return "", false
	}
	date := pad(y, 4) + "-" + pad(int(m), 2) + "-" + pad(dd, 2)
	clock := clockText(secs/3600, secs/60%60, secs%60, showSecs, true)
	kind := f.kind
	if kind == kindDateAuto {
		switch {
		case secs == 0:
			kind = kindDate
		case d == 0:
			kind = kindTime
		default:
			kind = kindDateTime
		}
	}
	switch kind {
	case kindDate:
		return date, true
	case kindTime:
		return clock, true
	}
	return date + " " + clock, true
}

// clockText is H:MM or H:MM:SS, the hours padded to two digits when
// padHours.
func clockText(h, m, s int, withSecs, padHours bool) string {
	hs := strconv.Itoa(h)
	if padHours {
		hs = pad(h, 2)
	}
	t := hs + ":" + pad(m, 2)
	if withSecs {
		t += ":" + pad(s, 2)
	}
	return t
}

// pad is n with leading zeros to width digits.
func pad(n, width int) string {
	s := strconv.Itoa(n)
	if len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	return s
}
