// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"io"
	"log/slog"
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// smokeAssistant is an Assistant over a memory store for the widget smoke
// tests: a locator that finds no Claude Code (the test's directory has
// none), no bridge, no ChatGPT service.
func smokeAssistant(t *testing.T, s *settings.Store) *Assistant {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Assistant{
		settings:  s,
		log:       log,
		handlers:  make(map[assistant.Target]bool),
		observers: make(map[int]func()),
		locator:   assistantpanel.NewLocator(s, []string{"PATH=" + t.TempDir()}, glibLoop{}, t.TempDir(), log),
	}
	// What initChatGPT wires in the application: another provider is a
	// runtime generation.
	for _, key := range []string{settings.KeyAssistantProvider, settings.KeyAssistantCodexPath, settings.KeyAssistantChatGPTModel, settings.KeyBoardChatGPTModel} {
		s.OnChanged(key, a.runtimeChanged)
	}
	return a
}

// flushMain runs the pending callbacks of the main loop.
func flushMain() {
	ctx := glib.MainContextDefault()
	for i := 0; i < 100 && ctx.Pending(); i++ {
		ctx.Iteration(false)
	}
}

// Preferences → AI over made-up AI provider accounts (no daemon): the
// Assistant Uses row lists Claude Code, ChatGPT and the accounts, with an
// account that cannot run insensitive and its reason as the subtitle; the
// Model row follows the provider in effect (the fixed models of Claude,
// the account's model); the MCP and ChatGPT groups are gone from the
// page. Run with MALACHI_GTK_SMOKE=1 and a GTK display.
func TestPreferencesAIGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	s := settings.NewMemory()
	s.SetAssistantTarget(assistant.App)
	a := smokeAssistant(t, s)
	on := aiTestAccount("acc_0123456789abcdef0123456789abcdef")
	off := aiTestAccount("acc_ffffffffffffffffffffffffffffffff")
	off.Enabled = false
	off.Config.Name = "Spare key"
	a.setAIAccounts([]api.Account{on, off})
	b := data.Builder("preferences.ui")
	for _, id := range []string{"mcp_group", "chatgpt_group", "assistant_provider", "assistant_claude_code"} {
		if b.GetObject(id) != nil {
			t.Errorf("%s is still on the AI page", id)
		}
	}
	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		assist:            a, settings: s,
		assistantGroup:  b.GetObject("assistant_group").Cast().(*adw.PreferencesGroup),
		assistantSource: b.GetObject("assistant_source").Cast().(*adw.ComboRow),
		assistantMenu:   b.GetObject("assistant_menu_switch").Cast().(*adw.SwitchRow),
		assistantTarget: b.GetObject("assistant_target").Cast().(*adw.ComboRow),
		assistantModel:  b.GetObject("assistant_model").Cast().(*adw.ComboRow),
	}
	unbind := d.bindAssistant(s)
	defer func() { d.closed = true; unbind() }()
	x := assistant.APIText(i18n.Tr)
	if d.assistantSource.Title() != x.UsesRow || d.assistantSource.Model().NItems() != 4 || d.assistantSource.Selected() != 0 {
		t.Errorf("Assistant Uses: %q, %d entries, selected %d", d.assistantSource.Title(), d.assistantSource.Model().NItems(), d.assistantSource.Selected())
	}
	// Claude Code chosen without a bridge: the row says why it cannot run.
	if d.assistantSource.Subtitle() != assistant.PanelTexts(tr).ToolsMissing {
		t.Errorf("subtitle %q", d.assistantSource.Subtitle())
	}
	if !d.assistantModel.Visible() || d.assistantModel.Model().NItems() != uint(len(assistant.Models)) {
		t.Errorf("Claude's model row: visible %v, %d entries", d.assistantModel.Visible(), d.assistantModel.Model().NItems())
	}

	// The account is chosen: its model, and the menu switch needs no
	// registration.
	d.assistantSource.SetSelected(2)
	flushMain()
	if s.AssistantProvider() != string(on.ID) || !a.usesAPI() {
		t.Fatalf("choosing the account: provider %q", s.AssistantProvider())
	}
	if d.assistantSource.Subtitle() != x.UsesSubtitle || !d.assistantMenu.Sensitive() || d.assistantMenu.Subtitle() != "" {
		t.Errorf("an account chosen: subtitle %q, switch %v %q", d.assistantSource.Subtitle(), d.assistantMenu.Sensitive(), d.assistantMenu.Subtitle())
	}
	if d.assistantModel.Subtitle() != x.ModelSubtitle || d.assistantModel.Model().NItems() != 1 {
		t.Errorf("the account's model row: %q, %d entries", d.assistantModel.Subtitle(), d.assistantModel.Model().NItems())
	}
	if item, ok := d.assistantModel.SelectedItem().Cast().(*gtk.StringObject); !ok || item.String() != "claude-sonnet-4-5" {
		t.Errorf("the account's model: %v", ok)
	}

	// The switched-off account chosen elsewhere: insensitive here, and
	// the row says why.
	s.SetAssistantProvider(string(off.ID))
	flushMain()
	if d.assistantSource.Selected() != 3 || d.assistantSource.Subtitle() != x.AccountOff {
		t.Errorf("a switched-off account: selected %d, subtitle %q", d.assistantSource.Selected(), d.assistantSource.Subtitle())
	}

	// A removed account reads as Claude and is listed as such.
	a.setAIAccounts([]api.Account{on})
	flushMain()
	if d.assistantSource.Model().NItems() != 4 || d.assistantSource.Selected() != 3 || d.assistantSource.Subtitle() != x.UsesSubtitle {
		t.Errorf("a removed account: %d entries, selected %d, subtitle %q", d.assistantSource.Model().NItems(), d.assistantSource.Selected(), d.assistantSource.Subtitle())
	}
	if a.source() != assistant.SourceClaude || d.assistantModel.Model().NItems() != uint(len(assistant.Models)) {
		t.Error("a removed account's model row is Claude's")
	}
}

// The Claude integration's dialog (no daemon, no Claude Code): the MCP
// switch insensitive without a bridge, the Claude Code row saying it was
// not found with Get Claude Code…. Run with MALACHI_GTK_SMOKE=1 and a GTK
// display.
func TestClaudeIntegrationGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	s := settings.NewMemory()
	a := smokeAssistant(t, s)
	d := newClaudeIntegration(s, a, nil)
	defer func() { d.closed = true }()
	if d.mcpSwitch.Sensitive() || d.mcpGroup.Description() == "" {
		t.Errorf("without a bridge the switch waits and the group says why: %v %q", d.mcpSwitch.Sensitive(), d.mcpGroup.Description())
	}
	if d.claudeCodeRow.Title() != assistant.TargetName(tr, assistant.Code) || d.claudeChoose.Label() != assistant.PanelTexts(tr).Choose {
		t.Errorf("the Claude Code row: %q, %q", d.claudeCodeRow.Title(), d.claudeChoose.Label())
	}
	if d.claudeCodeRow.Subtitle() != assistant.Problem(tr, assistant.App, assistant.Availability{}) {
		t.Errorf("without Claude Code the row says so: %q", d.claudeCodeRow.Subtitle())
	}
}

// The ChatGPT integration's dialog (no daemon, no Codex, no connection):
// the rows' texts, Get Codex… offered, the connection not connected, and
// the real ComboRow regression of the model rows: choosing a model must
// not replace a row's model inside GTK's selected notification, for the
// panel's row here and the board's row of the preferences. Run with
// MALACHI_GTK_SMOKE=1 and a GTK display.
func TestChatGPTIntegrationGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	s := settings.NewMemory()
	s.SetAssistantProvider("chatgpt")
	s.SetAssistantTarget(assistant.App)
	s.SetAssistantCodexPath("/synthetic/no-codex")
	s.SetAssistantChatGPTModel("synthetic-model")
	s.SetBoardChatGPTModel("synthetic-model")
	a := smokeAssistant(t, s)
	d := newChatGPTIntegration(s, a, nil)
	defer func() { d.closed = true }()
	tx := assistant.ChatGPTText(tr)
	if d.Title() != tx.Name || d.codexRow.Title() != tx.Codex || d.codexRow.Subtitle() != tx.NativeMissingCodex || !d.codexInstall.Visible() {
		t.Errorf("the Codex row: %q %q %q", d.Title(), d.codexRow.Title(), d.codexRow.Subtitle())
	}
	if d.connectionRow.Subtitle() != tx.Disconnected || !d.signIn.Visible() || d.disconnect.Visible() || d.modelRow.Sensitive() {
		t.Error("not connected: Continue with ChatGPT offered, the model row waits")
	}
	b := data.Builder("preferences.ui")
	p := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		assist:            a, settings: s,
		boardTriageModel: b.GetObject("board_triage_model").Cast().(*adw.ComboRow),
	}
	stopBoard := bindProviderModel(p, s, p.boardTriageModel, modelBoard)
	defer func() { p.closed = true; stopBoard() }()
	for _, row := range []*adw.ComboRow{d.modelRow, p.boardTriageModel} {
		if row.Model().NItems() != 2 {
			t.Fatalf("the ChatGPT catalogue without Codex: the default and the chosen model, %d", row.Model().NItems())
		}
		insideSelection := false
		h := row.NotifyProperty("model", func() {
			if insideSelection {
				t.Error("replaced ComboRow model during selection notification")
			}
		})
		for i := 0; i < 20; i++ {
			insideSelection = true
			row.SetSelected(uint(i % 2))
			insideSelection = false
			flushMain()
			if row.Selected() != uint(i%2) {
				t.Fatalf("selection lost: %d", row.Selected())
			}
			if row.Model().NItems() != 2 {
				t.Fatalf("catalog replaced after choosing model: %d", row.Model().NItems())
			}
		}
		row.HandlerDisconnect(h)
	}
	// Claude's board choices use the same deferred path.
	s.SetAssistantProvider("claude")
	flushMain()
	insideSelection := false
	h := p.boardTriageModel.NotifyProperty("model", func() {
		if insideSelection {
			t.Error("replaced Claude model during selection notification")
		}
	})
	for i := 0; i < 12; i++ {
		insideSelection = true
		p.boardTriageModel.SetSelected(uint(i % len(assistant.Models)))
		insideSelection = false
		flushMain()
	}
	p.boardTriageModel.HandlerDisconnect(h)
	if s.BoardTriageModel() != assistant.Models[11%len(assistant.Models)] {
		t.Errorf("the board's Claude model follows the row: %q", s.BoardTriageModel())
	}
	// Closing while an update is queued must not touch disposed widgets.
	s.SetBoardTriageModel(assistant.Haiku)
	p.closed = true
	flushMain()
}
