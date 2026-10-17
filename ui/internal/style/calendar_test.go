// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

import (
	"strings"
	"testing"
)

func TestCalendarColor(t *testing.T) {
	cases := []struct {
		in   string
		i    int
		want string
	}{
		{"#1F6FEB", 0, "#1f6feb"},
		{"#0082c9", 5, "#0082c9"},
		{"", 0, CalendarPalette[0]},
		{"#abc", 1, CalendarPalette[1]},
		{"#1234567", 2, CalendarPalette[2]},
		{"#12345g", 3, CalendarPalette[3]},
		{"red", 4, CalendarPalette[4]},
		{"#123456; } * { color: red", 5, CalendarPalette[5]},
		{"#123456\n", 6, CalendarPalette[6]},
		{" #123456", 7, CalendarPalette[7]},
		{"url(file:///etc/passwd)", len(CalendarPalette), CalendarPalette[0]},
		{"#１２３４５６", 1, CalendarPalette[1]},
		{"bad", -3, CalendarPalette[0]},
	}
	for _, c := range cases {
		if got := CalendarColor(c.in, c.i); got != c.want {
			t.Errorf("CalendarColor(%q, %d) = %q, want %q", c.in, c.i, got, c.want)
		}
	}
}

func TestCalendarCSSHostile(t *testing.T) {
	hostile := []string{
		"#123456; } window { background: red; } .x {",
		"red",
		"@import url(\"http://example.com/x.css\");",
		"#12345\"",
		"#aBcDeF",
		"\u202e#123456",
		"#123456*/",
	}
	css := CalendarCSS(hostile)
	for _, bad := range []string{"window {", "import", "example", "red;", "\"", "*/", "\u202e", "url("} {
		if strings.Contains(css, bad) {
			t.Errorf("stylesheet carries %q:\n%s", bad, css)
		}
	}
	if !strings.Contains(css, ".cal-c-4.cal-fill { background-color: mix(@window_bg_color, #abcdef, 0.18); }") {
		t.Errorf("valid colour not used:\n%s", css)
	}
	if !strings.Contains(css, ".cal-c-0.cal-swatch { background-color: "+CalendarPalette[0]+"; }") {
		t.Errorf("palette not used for a hostile colour:\n%s", css)
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Errorf("unbalanced braces:\n%s", css)
	}
	for i := range hostile {
		if !strings.Contains(css, "."+CalendarClass(i)+".cal-fill") {
			t.Errorf("no fill class for calendar %d", i)
		}
	}
}

func TestCalendarCSSViews(t *testing.T) {
	saved := calendarViewCSS
	defer func() { calendarViewCSS = saved }()
	calendarViewCSS = nil
	registerCalendarCSS(".cal-a { margin: 1px; }")
	registerCalendarCSS(".cal-b { margin: 2px; }\n")
	css := CalendarCSS(nil)
	if css != ".cal-a { margin: 1px; }\n.cal-b { margin: 2px; }\n" {
		t.Errorf("views' rules = %q", css)
	}
}
