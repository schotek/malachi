// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The calendar's colours (docs/calendar.md §6.5, CSS): a second CSS
// provider next to Apply's, reloaded whenever the calendars change. The
// window maps each calendar to an index (its place in calendar.list) and
// the views put the class CalendarClass(i) on what shows the calendar's
// colour; a calendar's id never reaches the stylesheet, and a colour only
// when it is "#rrggbb" exactly (CalendarColor), else the fixed palette by
// index. The classes:
//
//	.cal-c-N.cal-fill         the opaque fill of a block, a bar or a pill:
//	                          the colour at 18 % over the window background
//	                          (mix(), so dark mode follows)
//	.cal-c-N .cal-bar,        the 3 px bar at a block's start, the colour
//	.cal-c-N.cal-bar          itself
//	.cal-c-N.cal-swatch       the sidebar's colour square
//	.cal-c-N.cal-unanswered   an unanswered invitation: a dashed outline
//	.cal-c-N.cal-tentative    tentative: diagonal stripes over the fill
//
// Each view task adds its own static rules in style/calendar_<view>.go
// through registerCalendarCSS (an init function), which CalendarCSS
// appends in file order after the colour classes.

// CalendarPalette is the fallback colour of calendar i
// (CalendarPalette[i % len]): colours of the GNOME palette.
var CalendarPalette = []string{
	"#3584e4", "#2ec27e", "#e5a50a", "#e66100", "#c01c28", "#813d9c", "#865e3c", "#1c71d8",
	"#26a269", "#f5c211", "#ed333b", "#9141ac",
}

// calendarColorPattern is the only colour the stylesheet takes from the
// daemon.
var calendarColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// calendarViewCSS are the views' static rules (registerCalendarCSS).
var calendarViewCSS []string

// registerCalendarCSS adds a view's static rules to CalendarCSS; call it
// from an init function of style/calendar_<view>.go.
func registerCalendarCSS(css string) {
	calendarViewCSS = append(calendarViewCSS, css)
}

// CalendarClass is the CSS class of the calendar at index i.
func CalendarClass(i int) string { return "cal-c-" + strconv.Itoa(i) }

// CalendarColor is calendar i's colour as the stylesheet writes it: c in
// lower case when it is "#rrggbb", else the palette's. A negative i is 0.
func CalendarColor(c string, i int) string {
	if calendarColorPattern.MatchString(c) {
		return strings.ToLower(c)
	}
	if i < 0 {
		i = 0
	}
	return CalendarPalette[i%len(CalendarPalette)]
}

// CalendarCSS is the calendar's stylesheet for the calendars' colours in
// index order (colors[i] is calendar i's, as the daemon sent it).
func CalendarCSS(colors []string) string {
	var b strings.Builder
	for i, raw := range colors {
		c := CalendarColor(raw, i)
		cls := "." + CalendarClass(i)
		b.WriteString(cls + ".cal-fill { background-color: mix(@window_bg_color, " + c + ", 0.18); }\n")
		b.WriteString(cls + " .cal-bar, " + cls + ".cal-bar { background-color: " + c + "; min-width: 3px; }\n")
		b.WriteString(cls + ".cal-swatch { background-color: " + c + "; }\n")
		b.WriteString(cls + ".cal-unanswered { border: 1px dashed " + c + "; }\n")
		b.WriteString(cls + ".cal-tentative { background-image: repeating-linear-gradient(45deg, alpha(" + c +
			", 0.18), alpha(" + c + ", 0.18) 4px, transparent 4px, transparent 8px); }\n")
	}
	for _, css := range calendarViewCSS {
		b.WriteString(css)
		if !strings.HasSuffix(css, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// calendarProvider is the calendar's provider, installed on the first
// ApplyCalendar.
var calendarProvider *gtk.CSSProvider

// calendarLoaded is the stylesheet calendarProvider has, so an unchanged
// one is not loaded again.
var calendarLoaded string

// ApplyCalendar loads the calendar's stylesheet for colors (CalendarCSS)
// into its own provider on the default display, installed on the first
// call. Main loop only.
func ApplyCalendar(colors []string) {
	css := CalendarCSS(colors)
	if calendarProvider == nil {
		d := gdk.DisplayGetDefault()
		if d == nil {
			return
		}
		calendarProvider = gtk.NewCSSProvider()
		gtk.StyleContextAddProviderForDisplay(d, calendarProvider, uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	} else if css == calendarLoaded {
		return
	}
	calendarLoaded = css
	calendarProvider.LoadFromString(css)
}
