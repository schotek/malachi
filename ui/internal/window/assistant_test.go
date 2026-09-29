// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/mcpsetup"
	"github.com/schotek/malachi/ui/internal/settings"
)

// testAssistant is an Assistant over a memory store with a bridge that is
// never run: the tests set the status and the handlers themselves.
func testAssistant() *Assistant {
	return &Assistant{
		settings:  settings.NewMemory(),
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		bridge:    "/nonexistent/malachi-mcp",
		handlers:  make(map[assistant.Target]bool),
		observers: make(map[int]func()),
	}
}

// statusWith is a bridge report with Claude Desktop and Claude Code present
// and registered as given.
func statusWith(desktop, code bool) mcpsetup.Status {
	return mcpsetup.Status{Command: "/usr/bin/malachi-mcp", Clients: []mcpsetup.Client{
		{ID: "claude-desktop", Name: "Claude Desktop", Present: true, Registered: desktop},
		{ID: "claude-code", Name: "Claude Code", Present: true, Registered: code},
	}}
}

// GTK opens Claude Code only: whatever is stored reads as Claude Code,
// and the menu and the settings list Claude Desktop without offering it.
func TestGtkTarget(t *testing.T) {
	for _, in := range []assistant.Target{assistant.Desktop, assistant.Code, assistant.App, "elsewhere"} {
		if got := gtkTarget(in); got != assistant.Code {
			t.Errorf("gtkTarget(%q) = %q, want code", in, got)
		}
	}
	if slices.Contains(assistantTargets, assistant.App) {
		t.Error("assistantTargets lists App, which GTK does not have")
	}
	if supportedTarget(assistant.Desktop) || !supportedTarget(assistant.Code) {
		t.Error("supportedTarget: want Claude Code only")
	}
	a := testAssistant()
	for _, stored := range assistant.Targets {
		a.settings.SetAssistantTarget(stored)
		if got := a.target(); got != assistant.Code {
			t.Errorf("target with %q stored = %q, want code", stored, got)
		}
	}
}

// Claude Code is usable with its handler; the message actions also need
// the bridge registered in Claude Code, never only in Claude Desktop,
// which this client never opens, whatever is stored or registered.
func TestAssistantPick(t *testing.T) {
	cases := []struct {
		name           string
		stored         assistant.Target
		status         *mcpsetup.Status
		handler        bool // Claude Code's
		wantOK, fileOK bool
		wantShown      bool
	}{
		{"nothing known", assistant.Code, nil, true, false, true, false},
		{"code registered", assistant.Code, ptr(statusWith(false, true)), true, true, true, true},
		{"code registered, desktop stored", assistant.Desktop, ptr(statusWith(true, true)), true, true, true, true},
		{"code missing", assistant.Code, ptr(statusWith(false, true)), false, false, false, true},
		{"only desktop registered", assistant.Code, ptr(statusWith(true, false)), true, false, true, true},
		{"registered nowhere", assistant.Code, ptr(statusWith(false, false)), true, false, true, false},
	}
	for _, c := range cases {
		a := testAssistant()
		a.settings.SetAssistantTarget(c.stored)
		a.status = c.status
		a.handlers = map[assistant.Target]bool{assistant.Desktop: true, assistant.Code: c.handler}
		target, ok := a.pick(true)
		if target != assistant.Code || ok != c.wantOK {
			t.Errorf("%s: pick(true) = (%q, %v), want (code, %v)", c.name, target, ok, c.wantOK)
		}
		if _, ok := a.pick(false); ok != c.fileOK {
			t.Errorf("%s: pick(false) ok = %v, want %v", c.name, ok, c.fileOK)
		}
		if got := a.shown(); got != c.wantShown {
			t.Errorf("%s: shown = %v, want %v", c.name, got, c.wantShown)
		}
		if got := a.problem(target) == ""; got != c.wantOK {
			t.Errorf("%s: problem %q, want one exactly when the target cannot run", c.name, a.problem(target))
		}
		if av := a.availability(assistant.Desktop); av != (assistant.Availability{}) {
			t.Errorf("%s: Claude Desktop available %+v, want nothing", c.name, av)
		}
	}

	a := testAssistant()
	a.status = ptr(statusWith(true, true))
	a.settings.SetAssistantMenu(false)
	if a.shown() {
		t.Error("shown with assistant-menu off")
	}
}

func ptr[T any](v T) *T { return &v }

// Observers hear a new status, new handlers and the assistant keys, and
// nothing that changes nothing.
func TestAssistantNotifies(t *testing.T) {
	a := testAssistant()
	s := a.settings
	for _, key := range []string{settings.KeyAssistantMenu, settings.KeyAssistantTarget} {
		s.OnChanged(key, a.notify)
	}
	calls := 0
	remove := a.OnChange(func() { calls++ })

	a.Apply(statusWith(true, false))
	a.Apply(statusWith(true, false))
	if calls != 1 {
		t.Fatalf("after the same status twice: %d calls, want 1", calls)
	}
	a.Apply(statusWith(true, true))
	s.SetAssistantMenu(false)
	s.SetAssistantTarget(assistant.Code)
	if calls != 4 {
		t.Fatalf("after a new status and two keys: %d calls, want 4", calls)
	}
	remove()
	a.Apply(statusWith(false, false))
	if calls != 4 {
		t.Errorf("removed observer still called: %d calls", calls)
	}
}

// A conversation's members come oldest first; a prompt takes them newest
// first. One message stays as it is.
func TestNewestFirst(t *testing.T) {
	ids := []api.MessageID{"a", "b", "c"}
	if got := newestFirst(ids, true); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("conversation: %v", got)
	}
	if got := newestFirst(ids[:1], false); !slices.Equal(got, []string{"a"}) {
		t.Errorf("message: %v", got)
	}
	if got := newestFirst(nil, true); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

// The Assistant group in Settings → AI follows the bridge: it cannot be
// turned on without it, and shows the key while nothing is known yet.
func TestAssistantGroupFor(t *testing.T) {
	cases := []struct {
		name                         string
		menu, registered, known      bool
		problem                      string
		wantSensitive, wantOn, first bool
		wantTarget                   string
	}{
		{"registered, on", true, true, true, "", true, true, false, ""},
		{"registered, off", false, true, true, "", true, false, false, ""},
		{"registered, target missing", true, true, true, "Claude Code is not installed", true, true, false, "Claude Code is not installed"},
		{"not registered", true, false, true, "Turn on …", false, false, true, ""},
		{"not known yet", true, false, false, "Turn on …", false, true, false, ""},
		{"not known yet, key off", false, false, false, "", false, false, false, ""},
	}
	for _, c := range cases {
		got := assistantGroupFor(c.menu, c.registered, c.known, c.problem)
		want := assistantGroupState{sensitive: c.wantSensitive, on: c.wantOn, registerFirst: c.first, targetSubtitle: c.wantTarget}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, want)
		}
	}
}

// The menu: the message actions on the window's prefix with their names,
// Summarize Unread only where asked for, the targets as a choice, and the
// problem with "Set Up the Assistant…" only while the target cannot run.
func TestAssistantMenuModel(t *testing.T) {
	a := testAssistant()
	menu, update := a.assistantMenu("msg", false)
	sections := sectionsOf(menu)
	if len(sections) != 3 {
		t.Fatalf("message window menu: %d sections, want 3 (actions, open in, set up)", len(sections))
	}
	var actions []string
	for i := range sections[0].NItems() {
		if got := itemString(sections[0], i, "action"); got != "msg.assistant" {
			t.Errorf("action item %d: action %q, want msg.assistant", i, got)
		}
		actions = append(actions, itemString(sections[0], i, "target"))
	}
	if want := []string{"summarize", "draft-reply", "tasks", "ask"}; !slices.Equal(actions, want) {
		t.Errorf("message actions %v, want %v", actions, want)
	}
	var targets, targetActions []string
	for i := range sections[1].NItems() {
		targets = append(targets, itemString(sections[1], i, "target"))
		targetActions = append(targetActions, itemString(sections[1], i, "action"))
	}
	if want := []string{"desktop", "code"}; !slices.Equal(targets, want) {
		t.Errorf("targets %v, want %v", targets, want)
	}
	if want := []string{"app.assistant-unsupported", "app.assistant-target"}; !slices.Equal(targetActions, want) {
		t.Errorf("target actions %v, want %v (Claude Desktop listed, not offered)", targetActions, want)
	}
	if got := sections[2].NItems(); got != 2 {
		t.Errorf("nothing known: %d set-up items, want 2 (the problem and Set Up)", got)
	}
	if got := itemString(sections[2], 0, "action"); got != "app.assistant-problem" {
		t.Errorf("problem item action %q", got)
	}
	if got := itemString(sections[2], 1, "action"); got != "app.assistant-setup" {
		t.Errorf("set-up item action %q", got)
	}

	a.status = ptr(statusWith(false, true))
	a.handlers = map[assistant.Target]bool{assistant.Code: true}
	update()
	if got := sections[2].NItems(); got != 0 {
		t.Errorf("usable target: %d set-up items, want none", got)
	}

	main, _ := a.assistantMenu("win", true)
	sections = sectionsOf(main)
	if len(sections) != 4 {
		t.Fatalf("main window menu: %d sections, want 4", len(sections))
	}
	if got := itemString(sections[1], 0, "action"); got != "win.assistant-unread" {
		t.Errorf("unread item action %q", got)
	}
}

// Ask the Assistant… sits after Open and hides while its action is
// missing (the Assistant is not shown).
func TestChipMenuAskItem(t *testing.T) {
	m := &chipMenu(false).MenuModel
	var actions []string
	for i := range m.NItems() {
		actions = append(actions, itemString(m, i, "action"))
	}
	if want := []string{"att.open", "att.ask", "att.save"}; !slices.Equal(actions, want) {
		t.Fatalf("chip menu actions %v, want %v", actions, want)
	}
	if got := itemString(m, 1, "hidden-when"); got != "action-missing" {
		t.Errorf("ask item hidden-when %q, want action-missing", got)
	}
}

// sectionsOf is the sections of a menu made of sections only.
func sectionsOf(m *gio.Menu) []*gio.MenuModel {
	var out []*gio.MenuModel
	for i := range m.NItems() {
		if s := m.ItemLink(i, gio.MENU_LINK_SECTION); s != nil {
			out = append(out, gio.BaseMenuModel(s))
		}
	}
	return out
}

// itemString is a string attribute of item i, "" without it.
func itemString(m *gio.MenuModel, i int, attr string) string {
	v := m.ItemAttributeValue(i, attr, glib.NewVariantType("s"))
	if v == nil {
		return ""
	}
	return v.String()
}
