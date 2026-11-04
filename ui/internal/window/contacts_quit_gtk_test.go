// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/ui/internal/contactsctl"
)

// Quit with a changed contact form asks DiscardQuestion in the Contacts
// window: Cancel keeps the form and the application, Discard lets Quit
// go on. Run with MALACHI_GTK_SMOKE=1 and a GTK display.
func TestContactsQuitGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	t.Setenv(contactsctl.SamplesEnv, "1")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	flush := func() {
		ctx := glib.MainContextDefault()
		deadline := time.Now().Add(600 * time.Millisecond)
		for time.Now().Before(deadline) {
			for ctx.Pending() {
				ctx.Iteration(false)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	answers := []bool{}
	quit := func(ok bool) { answers = append(answers, ok) }

	// No Contacts window: Quit goes on.
	ConfirmContactsQuit(quit)
	if len(answers) != 1 || !answers[0] {
		t.Fatalf("without a window: %v", answers)
	}

	w := &Window{log: slog.New(slog.DiscardHandler)}
	cw := w.OpenContacts()
	defer func() {
		cw.closing = true
		cw.Close()
	}()
	flush()
	// A contact shown, not edited: Quit goes on.
	if len(cw.rowIDs) == 0 {
		t.Fatal("no contacts")
	}
	for i, id := range cw.rowIDs {
		if id != "" {
			cw.contactList.SelectRow(cw.contactList.RowAtIndex(i))
			break
		}
	}
	flush()
	ConfirmContactsQuit(quit)
	if len(answers) != 2 || !answers[1] || cw.VisibleDialog() != nil {
		t.Fatalf("not edited: %v", answers)
	}

	cw.editButton.Emit("clicked")
	flush()
	cw.contact.Form().Nickname = "Před koncem"
	if !cw.contact.Dirty() {
		t.Fatal("the form is not changed")
	}
	respond := func(response string) {
		t.Helper()
		ConfirmContactsQuit(quit)
		ConfirmContactsQuit(func(bool) { t.Error("a second Quit asked again") })
		flush()
		d := cw.VisibleDialog()
		if d == nil {
			t.Fatal("no question in the Contacts window")
		}
		d.Emit("response", response)
		d.ForceClose()
		flush()
	}
	respond("cancel")
	if len(answers) != 3 || answers[2] {
		t.Fatalf("Cancel: %v", answers)
	}
	if !cw.contact.Editing() || !cw.contact.Dirty() || cw.contact.Form().Nickname != "Před koncem" || cw.closed {
		t.Fatal("Cancel did not keep the form")
	}
	respond("confirm")
	if len(answers) != 4 || !answers[3] {
		t.Fatalf("Discard: %v", answers)
	}
}
