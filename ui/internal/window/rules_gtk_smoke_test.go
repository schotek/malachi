// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/rules"
	"github.com/schotek/malachi/ui/internal/rulesctl"
)

// waitMainLoop runs the main loop until ok holds or a second has passed.
func waitMainLoop(ok func() bool) {
	ctx := glib.MainContextDefault()
	deadline := time.Now().Add(time.Second)
	for !ok() && time.Now().Before(deadline) {
		if !ctx.Iteration(false) {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// Preferences → Rules and the rule editor over the samples' in-memory
// daemon (no daemon, no window): the rows of the invented rules with their
// problems and the sending icon, the switch of a rule that sends mail
// (the question, then the daemon), the editor of a stored rule with its
// condition and action rows, a field changed (the rows rebuilt), Try It,
// and a new rule saved. Run with MALACHI_GTK_SMOKE=1 and a GTK display.
func TestRulesGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	t.Setenv(rulesctl.SamplesEnv, "1")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
	}
	stop := d.bindRules(b, nil)
	defer func() { d.closed = true; stop() }()
	p := d.rulesPage
	if p == nil || !p.page.IsVisible() {
		t.Fatal("no rules page")
	}
	joined := func() string { return strings.Join(p.lines, "\n") }
	waitMainLoop(func() bool { return len(p.lines) == 4 })
	for _, l := range p.lines {
		t.Log("LAYOUT: " + l)
	}
	if len(p.lines) != 4 {
		t.Fatalf("rows: %v", p.lines)
	}
	if !strings.Contains(p.lines[1], "outgoing=true") || strings.Contains(p.lines[0], "outgoing=true") ||
		!strings.Contains(p.lines[2], "problem=true") || !strings.Contains(p.lines[3], "problem=true") {
		t.Errorf("rows: %s", joined())
	}
	if p.emptyRow.IsVisible() {
		t.Error("empty state with rules")
	}

	// The switch of a rule that sends mail asks first; yes turns it on.
	var asked []string
	p.ctl.OnQuestion = func(q rules.Question, answer func(bool)) {
		asked = append(asked, q.Heading)
		answer(true)
	}
	away := p.rows[2].rule.ID
	p.rows[2].toggle.SetActive(true)
	waitMainLoop(func() bool { return strings.Contains(joined(), "Out of office [All Mail Accounts] on=true") })
	if len(asked) != 1 || !strings.Contains(joined(), "Out of office [All Mail Accounts] on=true") {
		t.Errorf("asked %v rows %s", asked, joined())
	}

	// The keyboard order: Ctrl+Down on the first rule.
	first := p.rows[0].rule.ID
	p.ctl.MoveBy(first, 1)
	waitMainLoop(func() bool { return p.indexOf(first) == 1 })
	if p.indexOf(first) != 1 || p.indexOf(away) != 2 {
		t.Errorf("order after MoveBy: %s", joined())
	}

	// The editor of the rule that sends mail.
	dir := rulesctl.NewDirectory(p.ctl.Opts)
	dir.Load()
	stored, _ := p.ctl.Rule(p.rows[0].rule.ID)
	ed := newRulesEditor(p.ctl.Opts, dir, rules.FromRule(stored), &stored, nil)
	defer ed.dialog.ForceClose()
	waitMainLoop(func() bool { return len(ed.testChoices) > 0 })
	t.Log("LAYOUT: editor " + ed.summary())
	if len(ed.condRows) != len(stored.Conditions) || len(ed.actionRows) != len(stored.Actions) || !ed.save.IsSensitive() {
		t.Errorf("editor rows: %s", ed.summary())
	}
	if ed.name.Text() != stored.Name {
		t.Errorf("name %q", ed.name.Text())
	}

	// A field changed rebuilds the row; the value of the other kind starts over.
	ed.ctl.SetConditionField(0, api.RuleFieldBulk)
	waitMainLoop(func() bool { return ed.condRows[0].choice != nil })
	if ed.condRows[0].choice == nil || ed.condRows[0].entry != nil {
		t.Error("the bulk condition has no choice")
	}
	// A blank value is a problem shown under its row, and Save is off.
	ed.ctl.SetConditionField(0, api.RuleFieldSubject)
	ed.ctl.SetConditionValue(0, "")
	waitMainLoop(func() bool { return ed.condRows[0].entry != nil && !ed.save.IsSensitive() })
	if ed.save.IsSensitive() || !ed.condRows[0].problem.IsVisible() {
		t.Errorf("a blank value: save %v problem %v", ed.save.IsSensitive(), ed.condRows[0].problem.IsVisible())
	}
	ed.ctl.SetConditionValue(0, "invoice")
	waitMainLoop(func() bool { return ed.save.IsSensitive() })

	// Try It.
	ed.ctl.Test()
	waitMainLoop(func() bool { return ed.testRow.Title() != "" })
	if !strings.Contains(ed.testRow.Title(), "3 messages") {
		t.Errorf("test result %q", ed.testRow.Title())
	}

	// A new rule saved shows as a fifth row.
	fresh := newRulesEditor(p.ctl.Opts, dir, rules.NewForm("acc_work"), nil, nil)
	defer fresh.dialog.ForceClose()
	waitMainLoop(func() bool { return len(fresh.accountChoices) == 3 })
	fresh.ctl.SetName("New rule")
	fresh.ctl.SetConditionValue(0, "x@example.com")
	fresh.ctl.AddAction(api.RuleActionMarkRead)
	waitMainLoop(func() bool { return len(fresh.actionRows) == 1 })
	fresh.ctl.Save()
	waitMainLoop(func() bool { return len(p.lines) == 5 })
	if len(p.lines) != 5 || !strings.Contains(joined(), "New rule [Práce]") {
		t.Errorf("rows after Save: %s", joined())
	}
}
