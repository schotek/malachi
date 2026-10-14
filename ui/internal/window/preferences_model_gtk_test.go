// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/settings"
)

// A real ComboRow regression: selecting a model must not replace its model
// inside GTK's selected notification. No daemon, account or Codex is used.
// Run with MALACHI_GTK_SMOKE=1 and a dedicated GTK display.
func TestPreferencesModelGTKSmoke(t *testing.T) {
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
	a := &Assistant{settings: s, observers: map[int]func(){}}
	for _, key := range []string{settings.KeyAssistantChatGPTModel, settings.KeyBoardChatGPTModel, settings.KeyAssistantProvider} {
		s.OnChanged(key, func() { a.runtimeGeneration++; a.notify() })
	}
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{
		PreferencesDialog:  b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		assist:             a,
		assistantProvider:  b.GetObject("assistant_provider").Cast().(*adw.ComboRow),
		chatGPTGroup:       b.GetObject("chatgpt_group").Cast().(*adw.PreferencesGroup),
		codexRow:           b.GetObject("codex_executable").Cast().(*adw.ActionRow),
		codexChoose:        b.GetObject("codex_choose").Cast().(*gtk.Button),
		codexInstall:       b.GetObject("codex_install").Cast().(*gtk.Button),
		chatGPTConnection:  b.GetObject("chatgpt_connection").Cast().(*adw.ActionRow),
		chatGPTSignIn:      b.GetObject("chatgpt_sign_in").Cast().(*gtk.Button),
		chatGPTDisconnect:  b.GetObject("chatgpt_disconnect").Cast().(*gtk.Button),
		chatGPTModel:       b.GetObject("chatgpt_model").Cast().(*adw.ComboRow),
		chatGPTUsage:       b.GetObject("chatgpt_usage").Cast().(*adw.ActionRow),
		chatGPTManageUsage: b.GetObject("chatgpt_manage_usage").Cast().(*gtk.Button),
		boardTriageModel:   b.GetObject("board_triage_model").Cast().(*adw.ComboRow),
	}
	stopPanel := d.bindChatGPT(s)
	stopBoard := d.bindBoardProviderModel(s)
	defer func() { d.closed = true; stopPanel(); stopBoard() }()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 100 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	for _, row := range []*adw.ComboRow{d.chatGPTModel, d.boardTriageModel} {
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
			flush()
			if row.Selected() != uint(i%2) {
				t.Fatalf("selection lost: %d", row.Selected())
			}
			if row.Model().NItems() != 2 {
				t.Fatalf("catalog replaced after choosing model: %d", row.Model().NItems())
			}
		}
		row.HandlerDisconnect(h)
	}
	// Claude Board choices use the same safe, deferred update path.
	s.SetAssistantProvider("claude")
	flush()
	insideSelection := false
	h := d.boardTriageModel.NotifyProperty("model", func() {
		if insideSelection {
			t.Error("replaced Claude model during selection notification")
		}
	})
	for i := 0; i < 12; i++ {
		insideSelection = true
		d.boardTriageModel.SetSelected(uint(i % len(assistant.Models)))
		insideSelection = false
		flush()
	}
	d.boardTriageModel.HandlerDisconnect(h)
	// Closing while an update is queued must not touch disposed widgets.
	s.SetBoardTriageModel(assistant.Haiku)
	d.closed = true
	flush()
}
