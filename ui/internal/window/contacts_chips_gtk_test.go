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
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The address chips' contacts item over the sample daemon
// (MALACHI_CONTACTS_SAMPLES): a known address offers Show Contact, an
// unknown one Add to Contacts…, whose popover saves the contact, after
// which the address is known. Run with MALACHI_GTK_SMOKE=1 and a GTK
// display.
func TestAddressChipContactsGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	t.Setenv(contactsctl.SamplesEnv, "1")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	flush := func() {
		ctx := glib.MainContextDefault()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for ctx.Pending() {
				ctx.Iteration(false)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	w := &Window{log: slog.New(slog.DiscardHandler)}
	var said []string
	v := &messageView{win: w, toast: func(s string) { said = append(said, s) }}
	row := func() *addressRow { return &addressRow{label: gtk.NewLabel(""), box: adw.NewWrapBox()} }
	h := &addressHeader{v: v, from: row(), to: row(), cc: row()}
	known := api.Address{Name: "Jana Nováková", Address: "Jana.Novakova@example.cz"}
	stranger := api.Address{Name: "Petr Svoboda", Address: "petr.svoboda@example.com"}
	h.show("m_1", "acc_work", []api.Address{stranger}, []api.Address{known}, nil)
	flush()

	c := w.chipState()
	if !c.loaded {
		t.Fatal("books not listed")
	}
	if it, id := c.item(h, known.Address); it != contacts.ChipShow || id == "" {
		t.Errorf("known address: %v %q", it, id)
	}
	if it, _ := c.item(h, stranger.Address); it != contacts.ChipAdd {
		t.Errorf("unknown address: %v", it)
	}

	// Add to Contacts… over the sender's chip, saved as prefilled.
	anchor := h.from.chips[0].(*gtk.MenuButton)
	win := gtk.NewWindow()
	win.SetChild(h.from.box)
	h.from.box.SetVisible(true)
	win.Present()
	flush()
	h.addToContacts(anchor, stranger)
	flush()
	var pop *gtk.Popover
	for child := gtk.BaseWidget(anchor).FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
		if p, ok := child.(*gtk.Popover); ok && p.Visible() {
			pop = p
		}
	}
	if pop == nil {
		t.Fatal("no Add to Contacts popover")
	}
	var add *gtk.Button
	var walk func(gtk.Widgetter)
	walk = func(wd gtk.Widgetter) {
		if b, ok := wd.(*gtk.Button); ok && b.Label() == contacts.Add(i18n.Tr) {
			add = b
		}
		for ch := gtk.BaseWidget(wd).FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
			walk(ch)
		}
	}
	walk(pop.Child())
	if add == nil || !add.Sensitive() {
		t.Fatal("no sensitive Add button")
	}
	add.Activate()
	flush()
	if len(said) != 1 || said[0] != contacts.ContactAdded(i18n.Tr) {
		t.Errorf("toasts %q", said)
	}
	if it, id := c.item(h, stranger.Address); it != contacts.ChipShow || id == "" {
		t.Errorf("after Add: %v %q", it, id)
	}
	win.Destroy()
}
