// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
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
	contactSteps, err := parseDevStart("contacts;contacts:edit;prefs-contacts")
	if err != nil || len(contactSteps) != 3 || contactSteps[0].Kind != devContacts ||
		contactSteps[1].Kind != devContactsEdit || contactSteps[2].Kind != devPrefsContacts {
		t.Errorf("contacts steps %+v %v", contactSteps, err)
	}
	if _, err := parseDevStart("contacts:new"); err == nil {
		t.Error("contacts:new parsed")
	}
	if s, err := parseDevStart(""); err != nil || len(s) != 0 {
		t.Errorf("empty script: %v %v", s, err)
	}
}

func TestParseDevStartScenes(t *testing.T) {
	steps, err := parseDevStart("group=on;chat=off;folder=Web;row=Q4 planning;board:list:select;board:columns;compose-open;crm-save;shot=/tmp/a b.png;quit")
	if err != nil {
		t.Fatal(err)
	}
	want := []devStep{
		{Kind: devGroup, On: true},
		{Kind: devChat},
		{Kind: devFolder, Text: "Web"},
		{Kind: devRow, Text: "Q4 planning"},
		{Kind: devBoardStyle, Style: board.StyleList, Select: true},
		{Kind: devBoardStyle, Style: board.StyleColumns},
		{Kind: devComposeOpen},
		{Kind: devCrmSave},
		{Kind: devShot, Text: "/tmp/a b.png"},
		{Kind: devQuit},
	}
	if len(steps) != len(want) {
		t.Fatalf("%d steps, want %d: %+v", len(steps), len(want), steps)
	}
	for i, w := range want {
		got := steps[i]
		got.Name = ""
		if got != w {
			t.Errorf("step %d (%s) = %+v, want %+v", i, steps[i].Name, got, w)
		}
	}
	for _, bad := range []string{"group=yes", "chat=", "row=", "folder= ", "shot=", "board:grid", "board:list:pick", "board:list:select:x", "board:"} {
		if _, err := parseDevStart("mail;" + bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestDevFindFolder(t *testing.T) {
	accounts := []api.Account{{ID: "mail"}, {ID: "jira"}}
	folders := map[api.AccountID][]api.Folder{
		"jira": {{ID: "w", AccountID: "jira", Name: "Web"}},
		"mail": {{ID: "i", AccountID: "mail", Name: "INBOX"}, {ID: "s", AccountID: "mail", Name: "Sent"}, {ID: "x", AccountID: "mail", Name: "Webinars"}},
	}
	for text, want := range map[string]api.FolderID{"inbox": "i", "sent": "s", "Web": "x", "web": "x"} {
		if f, ok := devFindFolder(accounts, folders, text); !ok || f.ID != want {
			t.Errorf("folder %q = %+v %v, want %s", text, f, ok, want)
		}
	}
	if f, ok := devFindFolder(accounts[1:], folders, "Web"); !ok || f.ID != "w" {
		t.Errorf("folder Web of jira = %+v %v", f, ok)
	}
	if _, ok := devFindFolder(accounts, folders, "Trash"); ok {
		t.Error("Trash found")
	}
}

func TestDevFindRow(t *testing.T) {
	rows := []listRow{
		{Message: api.MessageSummary{Subject: "Design Weekly #212"}},
		{Message: api.MessageSummary{Subject: "Re: Quotation for the warehouse extension"}},
		{Message: api.MessageSummary{Subject: "Quotation for the warehouse extension"}},
	}
	for text, want := range map[string]int{"quotation": 1, "DESIGN weekly": 0, "Nabídka": -1} {
		if got := devFindRow(rows, text); got != want {
			t.Errorf("row %q = %d, want %d", text, got, want)
		}
	}
}

func TestDevRichestCase(t *testing.T) {
	cases := []board.Case{
		{ID: "a", Messages: []board.CaseMessage{{Text: "short"}}},
		{ID: "b", Messages: []board.CaseMessage{{Text: "a longer one"}, {Text: "and more"}}},
		{ID: "c", Messages: []board.CaseMessage{{Text: "twenty characters!!"}}},
		{ID: "d"},
	}
	if got := devRichestCase(cases); got != "b" {
		t.Errorf("richest %q, want b", got)
	}
	if got := devRichestCase(nil); got != "" {
		t.Errorf("richest of none %q", got)
	}
}

func TestParseDevInterval(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 3 * time.Second, "1": time.Second, "0.5": 500 * time.Millisecond, "-1": 3 * time.Second, "x": 3 * time.Second} {
		if got := parseDevInterval(in); got != want {
			t.Errorf("parseDevInterval(%q) = %v, want %v", in, got, want)
		}
	}
}
