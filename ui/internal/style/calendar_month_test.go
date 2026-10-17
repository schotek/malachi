// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

import (
	"strings"
	"testing"
)

// The Month view's rules follow the colour classes, so that its dot and
// bar sizes win over .cal-c-N .cal-bar.
func TestCalendarMonthCSS(t *testing.T) {
	css := CalendarCSS([]string{"#123456"})
	colour := strings.Index(css, ".cal-c-0 .cal-bar")
	for _, rule := range []string{"button.cal-month-pill .cal-month-dot", "button.cal-month-bar.cal-continues-before",
		"button.cal-month-bar.cal-continues-after", ".cal-month-day.cal-month-today"} {
		if i := strings.Index(css, rule); i < 0 || i < colour {
			t.Errorf("%q missing or before the colour classes (at %d, colours at %d)", rule, i, colour)
		}
	}
}
