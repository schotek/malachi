// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/calendar"
)

func TestParseDevStart(t *testing.T) {
	steps, err := parseDevStart(" mail;board ; calendar;calendar:month;;event-popover;event-editor;event-editor:edit;caldav-dialog;prefs-calendar;size=1280x800;quit")
	if err != nil {
		t.Fatal(err)
	}
	want := []devStepKind{devMail, devBoard, devCalendar, devCalendarView, devEventPopover, devEventEditor, devEventEdit, devCalDAV, devPrefsCalendar, devSize, devQuit}
	if len(steps) != len(want) {
		t.Fatalf("%d steps, want %d: %+v", len(steps), len(want), steps)
	}
	for i, k := range want {
		if steps[i].Kind != k {
			t.Errorf("step %d (%s) is kind %d, want %d", i, steps[i].Name, steps[i].Kind, k)
		}
	}
	if steps[3].View != calendar.ViewMonth || steps[1].Name != "board" {
		t.Errorf("step 3 view %v, step 1 name %q", steps[3].View, steps[1].Name)
	}
	if steps[9].Width != 1280 || steps[9].Height != 800 {
		t.Errorf("size step %+v", steps[9])
	}
	for _, bad := range []string{"nope", "calendar:year", "size=12", "size=0x5", "size=axb", "event-editor:new"} {
		if _, err := parseDevStart("mail;" + bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	crmSteps, err := parseDevStart("crm-dialog;eway-wizard")
	if err != nil || len(crmSteps) != 2 || crmSteps[0].Kind != devCrmDialog || crmSteps[1].Kind != devEwayWizard {
		t.Errorf("crm steps %+v %v", crmSteps, err)
	}
	if s, err := parseDevStart(""); err != nil || len(s) != 0 {
		t.Errorf("empty script: %v %v", s, err)
	}
}

func TestParseDevInterval(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 3 * time.Second, "1": time.Second, "0.5": 500 * time.Millisecond, "-1": 3 * time.Second, "x": 3 * time.Second} {
		if got := parseDevInterval(in); got != want {
			t.Errorf("parseDevInterval(%q) = %v, want %v", in, got, want)
		}
	}
}
