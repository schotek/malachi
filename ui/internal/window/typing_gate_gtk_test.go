// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Run in a dedicated test process with a display, for example Broadway
// (see board_gtk_smoke_test.go):
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run '^TestTypingGateGTKSmoke$' -count=1
// Only bare widgets are built: no window, settings, daemon or account.
func TestTypingGateGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()

	readOnlyView := gtk.NewTextView()
	readOnlyView.SetEditable(false)
	readOnlyText := gtk.NewText()
	readOnlyText.SetEditable(false)
	cases := []struct {
		name string
		f    gtk.Widgetter
		want bool
	}{
		{"nothing", nil, false},
		{"text view", gtk.NewTextView(), true},
		{"read-only text view", readOnlyView, false},
		{"text", gtk.NewText(), true},
		{"read-only text", readOnlyText, false},
		{"entry", gtk.NewEntry(), true},
		{"search entry", gtk.NewSearchEntry(), true},
		{"password entry", gtk.NewPasswordEntry(), true},
		{"spin button", gtk.NewSpinButtonWithRange(0, 10, 1), true},
		{"label", gtk.NewLabel("x"), false},
		{"button", gtk.NewButton(), false},
		{"list box", gtk.NewListBox(), false},
	}
	for _, c := range cases {
		if got := focusTakesTyping(c.f); got != c.want {
			t.Errorf("focusTakesTyping(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
