// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package calendar is the reference of the calendar's pure UI logic and
// of every text it shows (docs/calendar.md §5.1): the views' ranges and
// titles, the time-grid and month layouts, the agenda, the event detail,
// recurrence texts, the event editor's defaults, validation and iMIP
// texts, availability, the invitation pill and card, the Board's Today
// strip, notifications, preferences, the Add CalDAV sheet's flow, the
// keyboard rules and the invented sample data.
//
// The package is pure (no GTK, no gettext, no cgo): the caller passes a
// Translator (the method set of ui/internal/board's, so GTK passes i18n.Tr)
// and a Strftime that writes dates in the user's locale. The macOS client
// ports it 1:1 into MalachiCore/Calendar with its tests; GTK and Windows
// follow. Times of the API are UTC instants; every layout happens in the
// *time.Location the caller supplies. Strings from servers and other
// people are display text the daemon cleaned; the package cleans them
// again and formats them into its msgids, never concatenating sentences.
package calendar

import (
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/ui/internal/board"
)

// Translator translates a msgid of the malachi domain (board.Translator:
// T a plain one, N a plural form for n, C one with a context).
type Translator = board.Translator

// Strftime writes t (already in the zone it is to be shown in) with a
// strftime format in the user's locale: GTK through GLib, macOS and
// Windows with their own formatter. The formats are msgids of this
// package, so a locale may reorder them; EnglishStrftime is the C-locale
// writer the tests and the samples use.
type Strftime func(t time.Time, format string) string

// Env is what the texts with dates are written with: the translator, the
// strftime writer and the zone that decides what a day is. Tr and Format
// are required; Loc nil is time.Local.
type Env struct {
	Tr     Translator
	Format Strftime
	Loc    *time.Location
}

func (e Env) loc() *time.Location {
	if e.Loc == nil {
		return time.Local
	}
	return e.Loc
}

// date writes t in the env's zone with a format (a msgid the caller
// translated).
func (e Env) date(t time.Time, format string) string {
	return e.Format(t.In(e.loc()), format)
}

var (
	englishDays   = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	englishMonths = [...]string{"January", "February", "March", "April", "May", "June", "July",
		"August", "September", "October", "November", "December"}
)

// EnglishStrftime writes t with format in the C locale (English names).
// It knows %a %A %b %B %OB (the standalone month name, in English the
// same as %B) %d %e %H %I %M %m %p %Y %y %% and the "-" flag that drops
// the padding (%-d, %-H, %-I, %-m); anything else is copied.
func EnglishStrftime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 == len(format) {
			b.WriteByte(c)
			continue
		}
		i++
		pad := true
		if format[i] == 'O' && i+1 < len(format) && format[i+1] == 'B' {
			// The standalone month name: in English the same as %B.
			i++
		}
		if format[i] == '-' && i+1 < len(format) {
			pad = false
			i++
		}
		num := func(n int) {
			if pad {
				fmt.Fprintf(&b, "%02d", n)
			} else {
				fmt.Fprintf(&b, "%d", n)
			}
		}
		switch format[i] {
		case 'a':
			b.WriteString(englishDays[t.Weekday()][:3])
		case 'A':
			b.WriteString(englishDays[t.Weekday()])
		case 'b':
			b.WriteString(englishMonths[t.Month()-1][:3])
		case 'B':
			b.WriteString(englishMonths[t.Month()-1])
		case 'd':
			num(t.Day())
		case 'e':
			if pad {
				fmt.Fprintf(&b, "%2d", t.Day())
			} else {
				fmt.Fprintf(&b, "%d", t.Day())
			}
		case 'H':
			num(t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			num(h)
		case 'M':
			num(t.Minute())
		case 'm':
			num(int(t.Month()))
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'Y':
			fmt.Fprintf(&b, "%d", t.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			if !pad {
				b.WriteByte('-')
			}
			b.WriteByte(format[i])
		}
	}
	return b.String()
}
