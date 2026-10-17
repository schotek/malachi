// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

import (
	"strings"
	"testing"
)

func TestCalendarCSSHasTheWeekRules(t *testing.T) {
	css := CalendarCSS([]string{"#123456"})
	colours := strings.Index(css, ".cal-c-0.cal-fill")
	week := strings.Index(css, "button.cal-block")
	if colours < 0 || week < 0 || week < colours {
		t.Fatalf("the week's rules are missing or before the colours (colours %d, week %d)", colours, week)
	}
	for _, class := range []string{".cal-today-number", ".cal-continues-before", ".cal-continues-after", ".cal-selected", ".cal-badge"} {
		if !strings.Contains(css, class) {
			t.Errorf("CalendarCSS has no %s rule", class)
		}
	}
}
