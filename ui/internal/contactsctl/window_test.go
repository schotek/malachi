// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

func startWindow(t *testing.T) (*WindowController, *daemon, *loop) {
	t.Helper()
	d, l := standard()
	w := NewWindowController(options(d, l))
	w.Start()
	l.run()
	return w, d, l
}

func rowTitles(w *WindowController) []string {
	var out []string
	for _, r := range w.Rows() {
		if r.IsHeading() {
			out = append(out, "["+r.Heading+"]")
		} else {
			out = append(out, r.Title)
		}
	}
	return out
}

func TestWindowLoads(t *testing.T) {
	w, d, _ := startWindow(t)
	if !w.Loaded() || w.SelectedBook() != api.AddressBookAll || w.Error() != "" {
		t.Fatalf("book %q err %q", w.SelectedBook(), w.Error())
	}
	if got := rowTitles(w); !slices.Equal(got, []string{"[A]", "Adámek", "[B]", "Beneš", "Bílá", "[N]", "Nováková", "[P]", "Pokorný"}) {
		t.Fatalf("rows %v", got)
	}
	if w.Subtitle() != "All Contacts · 5" || w.Status() != "Contacts synchronized 3 minutes ago" {
		t.Fatalf("subtitle %q status %q", w.Subtitle(), w.Status())
	}
	if c := w.Consent(); len(c) != 1 || c[0].AccountID != "acc_p" || !c[0].SignIn || c[0].Account != "petr@firma2.cz" {
		t.Fatalf("consent %+v", c)
	}
	var p api.ContactListParams
	d.last(api.MethodContactList, &p)
	if p.BookID != api.AddressBookAll || p.Limit != PageSize || p.Offset != 0 {
		t.Fatalf("params %+v", p)
	}
	if w.ListEmpty() != contacts.EmptyNone || w.DetailEmpty() != contacts.EmptySelection || w.HasMore() {
		t.Fatal("states")
	}
	if w.NewContactBook() != "b_m1" || !w.CanCreate() {
		t.Fatal("new contact book")
	}
	signed := api.AccountID("")
	w.OnSignIn = func(id api.AccountID) { signed = id }
	w.AllowConsent("acc_p")
	if signed != "acc_p" {
		t.Fatal("allow")
	}
	if b, a, ok := w.Book("b_d"); !ok || a == nil || a.ID != "acc_d" || b.Name != "Personal" {
		t.Fatal("book")
	}
	if _, a, ok := w.Book(api.AddressBookRecent); !ok || a != nil {
		t.Fatal("recent book")
	}
}

func TestWindowSearchDebouncedAndFlat(t *testing.T) {
	w, d, l := startWindow(t)
	n := d.count(api.MethodContactList)
	w.SetQuery("b")
	w.SetQuery("be")
	l.run()
	if d.count(api.MethodContactList) != n {
		t.Fatal("searched before the debounce")
	}
	l.fire()
	if d.count(api.MethodContactList) != n+1 {
		t.Fatalf("searches %d", d.count(api.MethodContactList)-n)
	}
	if got := rowTitles(w); !slices.Equal(got, []string{"Beneš"}) {
		t.Fatalf("rows %v", got)
	}
	w.SetQuery("zz")
	l.fire()
	if w.ListEmpty() != contacts.EmptySearch {
		t.Fatal("empty search")
	}
	w.SetQuery(" ")
	l.fire()
	if len(w.Contacts()) != 5 || w.ListEmpty() != contacts.EmptyNone {
		t.Fatal("cleared search")
	}
}

func TestWindowSelectionKeptAcrossReload(t *testing.T) {
	w, d, l := startWindow(t)
	var selected []api.ContactID
	w.OnSelect = func(id api.ContactID) { selected = append(selected, id) }
	id := w.Contacts()[2].ID
	w.Select(id)
	w.Select(id)
	if !slices.Equal(selected, []api.ContactID{id}) || w.DetailEmpty() != contacts.EmptyNone {
		t.Fatal("select")
	}
	// A change elsewhere: one reload after the debounce, selection kept.
	n := d.count(api.MethodAddressBookList)
	raw, _ := json.Marshal(api.ContactsChangedNotification{AccountID: "acc_m"})
	w.Handle(api.NotifyContactsChanged, raw)
	w.Handle(api.NotifyContactsChanged, raw)
	w.Handle(api.NotifyNewMessage, raw)
	l.fire()
	if d.count(api.MethodAddressBookList) != n+2 { // the books and acc_p's consent
		t.Fatalf("reloads %d", d.count(api.MethodAddressBookList)-n)
	}
	if w.Selected() != id || len(selected) != 1 {
		t.Fatal("selection lost")
	}
	// The contact goes away: the selection with it.
	serveList(d, map[api.AddressBookID][]api.ContactSummary{"b_m1": people("b_m1", "Adámek")})
	w.Reload()
	l.run()
	if w.Selected() != "" || selected[len(selected)-1] != "" {
		t.Fatal("a vanished contact stays selected")
	}
}

func TestWindowBooksAndPaging(t *testing.T) {
	w, d, l := startWindow(t)
	w.SelectBook("b_m2") // hidden: not in the sidebar
	if w.SelectedBook() != api.AddressBookAll {
		t.Fatal("selected a hidden book")
	}
	w.SelectBook(api.AddressBookRecent)
	l.run()
	if w.Subtitle() != "Recently Used · 2 addresses" {
		t.Fatal(w.Subtitle())
	}
	if w.NewContactBook() != "b_m1" {
		t.Fatal("recent is not writable")
	}
	names := make([]string, 0, 250)
	for i := range 250 {
		names = append(names, "N"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	big := people("b_d", names...)
	serveList(d, map[api.AddressBookID][]api.ContactSummary{"b_d": big})
	w.SelectBook("b_d")
	l.run()
	if len(w.Contacts()) != PageSize || !w.HasMore() || w.Total() != 250 {
		t.Fatalf("first page %d", len(w.Contacts()))
	}
	w.LoadMore()
	w.LoadMore() // on its way: nothing
	l.run()
	w.LoadMore()
	l.run()
	if len(w.Contacts()) != 250 || w.HasMore() {
		t.Fatalf("pages %d", len(w.Contacts()))
	}
	var p api.ContactListParams
	d.last(api.MethodContactList, &p)
	if p.Offset != 200 {
		t.Fatalf("offset %d", p.Offset)
	}
	// A reload keeps every shown row.
	w.Reload()
	l.run()
	d.last(api.MethodContactList, &p)
	if p.Limit != 250 || len(w.Contacts()) != 250 {
		t.Fatalf("reload limit %d", p.Limit)
	}
}

func TestWindowOnlyRecentAndFailure(t *testing.T) {
	d, l := newDaemon(), &loop{}
	d.answer(api.MethodAccountList, api.AccountListResult{})
	d.answer(api.MethodAddressBookList, api.AddressBookListResult{Books: books()[3:]})
	serveList(d, map[api.AddressBookID][]api.ContactSummary{})
	w := NewWindowController(options(d, l))
	changes := 0
	w.OnChange = func() { changes++ }
	w.Start()
	l.run()
	if w.SelectedBook() != api.AddressBookRecent || w.DetailEmpty() != contacts.EmptyNoBooks || w.ListEmpty() != contacts.EmptyBook || w.CanCreate() {
		t.Fatal("only recent")
	}
	if changes == 0 {
		t.Fatal("no change reported")
	}
	d.fail(api.MethodAddressBookList, &api.Error{Code: api.CodeStorageError, Message: "x"})
	w.Reload()
	l.run()
	if w.Error() == "" {
		t.Fatal("no error")
	}
	w.Stop()
	w.Reload()
	l.run()
}
