// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
)

// The Contacts window over the sample daemon (MALACHI_CONTACTS_SAMPLES,
// no daemon, no main window): the sidebar, the list with its sections,
// the consent banner, the status line, the detail, an edit saved, a
// conflict reloaded under the user's change, a new contact, a deletion
// and a search. Run with MALACHI_GTK_SMOKE=1 and a GTK display.
func TestContactsWindowGTKSmoke(t *testing.T) {
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
	w := &Window{log: slog.New(slog.DiscardHandler)}
	cw := w.OpenContacts()
	defer cw.Close()
	flush()

	if !cw.ctl.Loaded() {
		t.Fatal("books not listed")
	}
	if cw.ctl.SelectedBook() != api.AddressBookAll {
		t.Errorf("selected book %q", cw.ctl.SelectedBook())
	}
	if n := len(cw.bookRows); n < 5 {
		t.Errorf("%d sidebar rows", n)
	}
	headings := 0
	for _, id := range cw.rowIDs {
		if id == "" {
			headings++
		}
	}
	if headings == 0 || headings == len(cw.rowIDs) {
		t.Errorf("list rows %d, headings %d", len(cw.rowIDs), headings)
	}
	if got := cw.listStack.VisibleChildName(); got != "list" {
		t.Errorf("list stack %q", got)
	}
	if !cw.status.IsVisible() || cw.status.Text() == "" {
		t.Error("no status line")
	}
	if cw.banners.FirstChild() == nil {
		t.Error("no consent banner for the account without the consent")
	}
	if got := cw.detailStack.VisibleChildName(); got != "empty" {
		t.Errorf("detail stack %q without a selection", got)
	}

	// Select Jana Nováková: the detail.
	var jana api.ContactID
	for _, c := range cw.ctl.Contacts() {
		if c.DisplayName == "Jana Nováková" {
			jana = c.ID
		}
	}
	for i, id := range cw.rowIDs {
		if id == jana {
			cw.contactList.SelectRow(cw.contactList.RowAtIndex(i))
		}
	}
	flush()
	if got := cw.detailStack.VisibleChildName(); got != "detail" || cw.detailBox.FirstChild() == nil {
		t.Fatalf("detail stack %q", got)
	}
	if !cw.editButton.IsVisible() {
		t.Error("no Edit for a writable contact")
	}

	// Edit and save.
	cw.editButton.Emit("clicked")
	flush()
	if cw.detailStack.VisibleChildName() != "form" || !cw.doneButton.IsVisible() {
		t.Fatal("no form")
	}
	cw.contact.Form().Nickname = "Janička"
	cw.doneButton.Emit("clicked")
	flush()
	if cw.contact.Editing() || cw.contact.Card() == nil || cw.contact.Card().Nickname != "Janička" {
		t.Fatalf("saved: editing %v card %+v err %q", cw.contact.Editing(), cw.contact.Card(), cw.contact.Error())
	}

	// Somebody changes the contact meanwhile: Done asks, Reload keeps the
	// user's change over the server's version.
	cw.contact.BeginEdit()
	flush()
	server := *cw.contact.Card()
	server.Org = "Jinde s.r.o."
	if err := contactsSamples.Call(t.Context(), api.MethodContactUpdate,
		api.ContactUpdateParams{ContactID: server.ID, Revision: server.Revision, Card: server}, nil); err != nil {
		t.Fatal(err)
	}
	var asked []string
	cw.contact.OnQuestion = func(q contacts.Question, answer func(bool)) {
		asked = append(asked, q.Heading)
		answer(true)
	}
	cw.contact.Form().Title = "ředitelka"
	cw.contact.Save()
	flush()
	if len(asked) != 1 || !cw.contact.Editing() {
		t.Fatalf("conflict: asked %v editing %v", asked, cw.contact.Editing())
	}
	if f := cw.contact.Form(); f.Title != "ředitelka" || f.Org != "Jinde s.r.o." {
		t.Errorf("rebased form: title %q org %q", f.Title, f.Org)
	}
	cw.contact.Save()
	flush()
	if cw.contact.Editing() || cw.contact.Card().Title != "ředitelka" {
		t.Errorf("after reload: editing %v err %q", cw.contact.Editing(), cw.contact.Error())
	}

	// A new contact.
	cw.newButton.Emit("clicked")
	flush()
	f := cw.contact.Form()
	if f == nil || !f.IsNew || cw.detailStack.VisibleChildName() != "form" {
		t.Fatal("no new contact's form")
	}
	f.Given, f.Family = "Zdeněk", "Zelený"
	f.Emails = []contacts.LabelledValue{{Value: "zdenek@example.cz"}}
	cw.contact.Save()
	flush()
	if cw.contact.Editing() || !strings.Contains(cw.rowsKey, "Zdeněk Zelený") {
		t.Errorf("created: editing %v err %q", cw.contact.Editing(), cw.contact.Error())
	}

	// Deleted after the question.
	asked = nil
	cw.contact.Delete()
	flush()
	if len(asked) != 1 || cw.contact.Card() != nil || strings.Contains(cw.rowsKey, "Zdeněk Zelený") {
		t.Errorf("deleted: asked %v card %v", asked, cw.contact.Card())
	}

	// A search lists flat.
	cw.searchBar.SetSearchMode(true)
	cw.searchEntry.SetText("svob")
	flush()
	if len(cw.rowIDs) == 0 {
		t.Fatal("search found nothing")
	}
	for _, id := range cw.rowIDs {
		if id == "" {
			t.Error("a heading in the search's list")
		}
	}
	cw.searchEntry.SetText("nikdo-takovy")
	flush()
	if cw.listStack.VisibleChildName() != "empty" {
		t.Errorf("empty search shows %q", cw.listStack.VisibleChildName())
	}
}
