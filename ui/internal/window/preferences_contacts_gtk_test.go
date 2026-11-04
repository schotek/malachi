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

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/contactsctl"
)

// Preferences → Contacts over the samples' in-memory daemon (no daemon,
// no window): a group per account with the contacts capability, the
// consent row only for the account that lacks it, the books with their
// checkboxes, and a checkbox that hides a book on the daemon. Run with
// MALACHI_GTK_SMOKE=1 and a GTK display.
func TestPreferencesContactsGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	t.Setenv(contactsctl.SamplesEnv, "1")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
	}
	stop := d.bindContacts(b, nil, nil)
	defer func() { d.closed = true; stop() }()
	p := d.contactsPage
	if p == nil || !p.page.IsVisible() {
		t.Fatal("no contacts page")
	}
	// The calls answer on their own goroutines through the main loop: run
	// it until the page shows what is asked or a second has passed.
	waitFor := func(ok func() bool) {
		ctx := glib.MainContextDefault()
		deadline := time.Now().Add(time.Second)
		for !ok() && time.Now().Before(deadline) {
			if !ctx.Iteration(false) {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	joined := func() string { return strings.Join(p.lines, "\n") }
	waitFor(func() bool { return strings.Contains(joined(), "consent") })
	for _, l := range p.lines {
		t.Log("LAYOUT: " + l)
	}
	if len(p.lines) != 3 {
		t.Fatalf("groups: %v", p.lines)
	}
	if !strings.Contains(p.lines[0], "jana@firma.cz [M365]") || strings.Contains(p.lines[0], "consent") ||
		!strings.Contains(p.lines[0], "book=ab_work:true") || !strings.Contains(p.lines[0], "book=ab_work_old:false") {
		t.Errorf("work account: %s", p.lines[0])
	}
	if !strings.Contains(p.lines[1], "petr@firma2.cz") || !strings.Contains(p.lines[1], "consent") {
		t.Errorf("the account without consent: %s", p.lines[1])
	}
	if !strings.Contains(p.lines[2], "[CARDDAV]") || !strings.Contains(p.lines[2], "book=ab_family:true") {
		t.Errorf("CardDAV account: %s", p.lines[2])
	}
	if p.empty.IsVisible() {
		t.Error("empty state with accounts")
	}
	if p.lastErr != "" {
		t.Errorf("error right after load: %s", p.lastErr)
	}

	// Hiding a book goes to the daemon and comes back on the next listing.
	p.ctl.SetVisible("ab_family", false)
	waitFor(func() bool { return strings.Contains(joined(), "book=ab_family:false") })
	p.ctl.Start()
	waitFor(func() bool { return false })
	if !strings.Contains(joined(), "book=ab_family:false") {
		t.Errorf("hidden book not shown hidden after a reload: %v", p.lines)
	}
	p.ctl.SetVisible("ab_family", true)
	waitFor(func() bool { return strings.Contains(joined(), "book=ab_family:true") })
}
