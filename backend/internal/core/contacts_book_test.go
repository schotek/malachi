// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// contactsRec records notify.contactsChanged.
type contactsRec struct {
	recNotifier
	mu  sync.Mutex
	evs []api.ContactsChangedNotification
}

func (r *contactsRec) ContactsChanged(n api.ContactsChangedNotification) {
	r.mu.Lock()
	r.evs = append(r.evs, n)
	r.mu.Unlock()
}

func (r *contactsRec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.evs)
}

// contactsHarness is a backend over a caldavtest server with the address
// book engine running (the mail supervisors stay fakes).
type contactsHarness struct {
	b   *Backend
	srv *caldavtest.Server
	rec *contactsRec
	ctx context.Context
	id  api.AccountID
}

func newContactsHarness(t *testing.T, srv *caldavtest.Server, server string) *contactsHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b, _, _, _ := newCalDAVBackend(t, srv)
	rec := &contactsRec{}
	b.SetNotifier(rec)
	b.contactSync = contactsync.New(contactsync.Options{Store: b.store, Providers: b.contactsProvider, Interval: time.Hour,
		Changed: func(accountID string, ids []string) { b.contactsChanged(accountID, ids...) }})
	done := make(chan struct{})
	go func() { b.contactSync.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	h := &contactsHarness{b: b, srv: srv, rec: rec, ctx: ctx}
	if server == "" {
		return h
	}
	cfg := calDAVConfig(server)
	cfg.CalDAV.Contacts = true
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	h.id = add.AccountID
	h.start(t)
	return h
}

func (h *contactsHarness) account(t *testing.T) store.Account {
	t.Helper()
	a, err := h.b.store.GetAccount(h.ctx, string(h.id))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// start runs the account's syncer and waits for its first pass.
func (h *contactsHarness) start(t *testing.T) {
	t.Helper()
	h.b.contactSync.Start(h.account(t))
	h.waitPass(t, time.Time{})
}

func (h *contactsHarness) waitPass(t *testing.T, after time.Time) {
	t.Helper()
	waitLong(t, "a contacts pass", func() bool {
		st, ok := h.b.contactSync.State(string(h.id))
		return ok && st.LastSync.After(after) && st.Status == contactsync.StatusIdle
	})
}

func (h *contactsHarness) syncNow(t *testing.T) {
	t.Helper()
	st, _ := h.b.contactSync.State(string(h.id))
	h.b.contactSync.SyncNow(string(h.id))
	h.waitPass(t, st.LastSync)
}

func (h *contactsHarness) books(t *testing.T) []api.AddressBook {
	t.Helper()
	res, err := h.b.AddressBooks().ListBooks(h.ctx, api.AddressBookListParams{AccountID: h.id})
	if err != nil {
		t.Fatal(err)
	}
	return res.Books
}

func vcf(uid, fn, email string, extra ...string) []byte {
	given, family, _ := strings.Cut(fn, " ")
	lines := []string{"BEGIN:VCARD", "VERSION:3.0", "UID:" + uid, "FN:" + fn, "N:" + family + ";" + given + ";;;"}
	if email != "" {
		lines = append(lines, "EMAIL;TYPE=WORK:"+email)
	}
	lines = append(lines, extra...)
	lines = append(lines, "END:VCARD", "")
	return []byte(strings.Join(lines, "\r\n"))
}

func TestCardDAVContactsEndToEnd(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	book := srv.AddressBookHref("contacts")
	srv.PutCard(book, "jana.vcf", vcf("J1", "Jana Nováková", "jana@example.org", "X-CUSTOM;X-A=1:keep me", "item1.TEL:+420 111", "item1.X-ABLabel:Boat"))
	srv.PutCard(book, "petr.vcf", vcf("P1", "Petr Dvořák", "petr@example.org"))
	srv.PutCard(srv.AddressBookHref("team-shared_by_petr"), "eva.vcf", vcf("E1", "Eva Malá", "eva@example.org"))
	h := newContactsHarness(t, srv, srv.ServerURL())
	b, ctx := h.b, h.ctx
	ab := b.AddressBooks()

	a := h.account(t)
	if a.Config.CalDAV.AddressBookHomeSetURL != srv.AddressBookHomeURL() {
		t.Fatalf("home set %q", a.Config.CalDAV.AddressBookHomeSetURL)
	}
	if caps := capabilitiesFor(a.Config); !slices.Equal(caps, []api.AccountCapability{api.CapabilityCalendar, api.CapabilityContacts}) {
		t.Fatalf("caps %v", caps)
	}
	books := h.books(t)
	if len(books) != 2 || books[0].Name != "Contacts" || books[0].Count != 2 || books[0].Kind != api.AddressBookKindCardDAV ||
		!books[1].ReadOnly || books[1].Count != 1 || books[0].LastSyncAt == nil {
		t.Fatalf("books %+v", books)
	}
	all, _ := ab.ListBooks(ctx, api.AddressBookListParams{})
	if n := len(all.Books); n != 3 || all.Books[2].ID != api.AddressBookRecent || all.Books[2].Kind != api.AddressBookKindRecent {
		t.Fatalf("all books %+v", all.Books)
	}

	list, err := ab.ListContacts(ctx, api.ContactListParams{BookID: api.AddressBookAll})
	if err != nil || list.Total != 3 || list.Contacts[0].DisplayName != "Petr Dvořák" || list.Contacts[0].Section != "D" {
		t.Fatalf("list %+v %v", list, err)
	}
	q, _ := ab.ListContacts(ctx, api.ContactListParams{BookID: books[0].ID, Query: "novak"})
	if q.Total != 1 {
		t.Fatalf("query %+v", q)
	}
	jana := q.Contacts[0].ID
	got, err := ab.GetContact(ctx, api.ContactGetParams{ContactID: jana})
	if err != nil {
		t.Fatal(err)
	}
	c := got.Contact
	if c.DisplayName != "Jana Nováková" || len(c.Emails) != 1 || c.Emails[0].Label != "work" || c.Revision == "" || c.ReadOnly ||
		len(c.Phones) != 1 || c.Phones[0].Label != "Boat" {
		t.Fatalf("card %+v", c)
	}
	known, err := ab.Lookup(ctx, api.ContactLookupParams{Addresses: []string{"JANA@example.org", "x@example.org"}})
	if err != nil || len(known.Known) != 1 || known.Known["jana@example.org"] != jana {
		t.Fatalf("lookup %+v %v", known, err)
	}

	// An update is a patch: the X- property and the group stay.
	c.Title = "Ředitelka"
	c.Emails = append(c.Emails, api.ContactEmail{Label: "home", Address: "jana@home.example"})
	up, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: jana, Revision: c.Revision, Card: c})
	if err != nil || up.Revision == "" || up.Revision == c.Revision {
		t.Fatalf("update %+v %v", up, err)
	}
	data, _, _ := srv.Card(book + "jana.vcf")
	for _, want := range []string{"X-CUSTOM;X-A=1:keep me", "item1.X-ABLabel:Boat", "TITLE:Ředitelka", "jana@home.example"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("patched card lacks %q:\n%s", want, data)
		}
	}
	if k, _ := ab.Lookup(ctx, api.ContactLookupParams{Addresses: []string{"jana@home.example"}}); k.Known["jana@home.example"] != jana {
		t.Errorf("the cache does not have the new address: %+v", k)
	}

	// The server changed it meanwhile: conflict with the server's version.
	srv.PutCard(book, "jana.vcf", vcf("J1", "Jana Svobodová", "jana@example.org"))
	c.Title = "Stale"
	_, err = ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: jana, Revision: up.Revision, Card: c})
	if errCode(t, err) != api.CodeContactConflict {
		t.Fatalf("conflict: %v", err)
	}
	ae := err.(*api.Error)
	cd, ok := ae.Data.(api.ContactConflictData)
	if !ok || cd.Server.DisplayName != "Jana Svobodová" || cd.Server.Revision == up.Revision {
		t.Fatalf("conflict data %+v", ae.Data)
	}
	// The cache holds the server's version now; a write over it works.
	cd.Server.Title = "Fresh"
	if _, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: jana, Revision: cd.Server.Revision, Card: cd.Server}); err != nil {
		t.Fatalf("after reload: %v", err)
	}
	// An old revision that the cache already moved past: conflict too.
	if _, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: jana, Revision: `"old"`, Card: c}); errCode(t, err) != api.CodeContactConflict {
		t.Fatalf("stale revision: %v", err)
	}

	// Create, then delete only on an explicit call.
	cr, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: books[0].ID, Card: api.ContactCard{
		Name: api.ContactName{Given: "Karel", Family: "Nový"}, Emails: []api.ContactEmail{{Address: "karel@example.org"}},
		Birthday: &api.ContactBirthday{Month: 2, Day: 29}}})
	if err != nil || cr.ContactID == "" || cr.Revision == "" {
		t.Fatalf("create %+v %v", cr, err)
	}
	if len(srv.Cards(book)) != 3 {
		t.Fatalf("server cards %v", srv.Cards(book))
	}
	nc, _ := ab.GetContact(ctx, api.ContactGetParams{ContactID: cr.ContactID})
	if nc.Contact.DisplayName != "Karel Nový" || nc.Contact.Birthday == nil || nc.Contact.Birthday.Year != 0 || nc.Contact.Birthday.Day != 29 {
		t.Fatalf("created %+v", nc.Contact)
	}
	if _, err := ab.DeleteContact(ctx, api.ContactDeleteParams{ContactID: cr.ContactID, Revision: cr.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(srv.Cards(book)) != 2 {
		t.Fatalf("after delete %v", srv.Cards(book))
	}
	if _, err := ab.GetContact(ctx, api.ContactGetParams{ContactID: cr.ContactID}); errCode(t, err) != api.CodeContactNotFound {
		t.Fatalf("deleted: %v", err)
	}

	// Read-only book and recent book refuse writes.
	if _, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: books[1].ID, Card: api.ContactCard{DisplayName: "X"}}); errCode(t, err) != api.CodeAddressBookReadOnly {
		t.Fatalf("read only: %v", err)
	}
	if _, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: api.AddressBookRecent, Card: api.ContactCard{DisplayName: "X"}}); errCode(t, err) != api.CodeAddressBookReadOnly {
		t.Fatalf("recent: %v", err)
	}
	if _, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: "b_none", Card: api.ContactCard{DisplayName: "X"}}); errCode(t, err) != api.CodeAddressBookNotFound {
		t.Fatalf("unknown: %v", err)
	}

	// Another client deletes a card: the next pass drops it; the sync
	// never deletes on the server.
	srv.RemoveCard(book + "petr.vcf")
	h.syncNow(t)
	if l, _ := ab.ListContacts(ctx, api.ContactListParams{BookID: books[0].ID}); l.Total != 1 {
		t.Fatalf("after a remote delete %+v", l)
	}
	for _, r := range srv.Requests() {
		if r.Method == "DELETE" && !strings.Contains(r.Path, ".vcf") {
			t.Errorf("unexpected delete %s", r.Path)
		}
	}

	// Hiding a book empties it at once; showing it brings it back.
	if _, err := ab.SetBook(ctx, api.AddressBookSetParams{BookID: books[1].ID, Visible: false}); err != nil {
		t.Fatal(err)
	}
	if l, _ := ab.ListContacts(ctx, api.ContactListParams{BookID: books[1].ID}); l.Total != 0 {
		t.Fatal("a hidden book keeps its cards")
	}
	set, err := ab.SetBook(ctx, api.AddressBookSetParams{BookID: books[1].ID, Visible: true})
	if err != nil || !set.Book.Visible {
		t.Fatal(set, err)
	}
	waitLong(t, "the shown book filled", func() bool {
		l, _ := ab.ListContacts(ctx, api.ContactListParams{BookID: books[1].ID})
		return l.Total == 1
	})
	waitLong(t, "notify.contactsChanged", func() bool { return h.rec.count() > 0 })

	if srv.AuthLeaks() != 0 || srv.EvilRequests() != 0 {
		t.Fatal("credentials leaked")
	}

	// Contacts off: the cache goes, the capability too.
	cfg := a.Config
	cfg.CalDAV.Contacts = false
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: h.id, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := b.store.ListAddressBooks(ctx, string(h.id)); len(bs) != 0 {
		t.Fatalf("books after contacts off: %+v", bs)
	}
	if _, ok := b.contactSync.State(string(h.id)); ok {
		t.Error("syncer after contacts off")
	}
	a = h.account(t)
	if slices.Contains(capabilitiesFor(a.Config), api.CapabilityContacts) {
		t.Error("contacts capability after off")
	}
}

func TestCardDAVContactsProfiles(t *testing.T) {
	cases := []struct {
		profile caldavtest.Profile
		server  func(s *caldavtest.Server) string
		book    string
		cal     bool
	}{
		{caldavtest.ICloud, func(s *caldavtest.Server) string { return s.ServerURL() }, "card", true},
		// iCloud's contacts host alone: a CardDAV-only account.
		{caldavtest.ICloud, func(s *caldavtest.Server) string { return s.CardServerURL() }, "card", false},
		{caldavtest.Fastmail, func(s *caldavtest.Server) string { return s.CardServerURL() }, "Default", false},
		{caldavtest.Radicale, func(s *caldavtest.Server) string { return s.ServerURL() }, "contacts", true},
	}
	for _, tc := range cases {
		t.Run(string(tc.profile)+"/"+tc.book, func(t *testing.T) {
			srv := caldavtest.New(t, tc.profile)
			href := srv.AddressBookHref(tc.book)
			srv.PutCard(href, "a.vcf", vcf("A1", "Anna Bílá", "anna@example.org"))
			h := newContactsHarness(t, srv, tc.server(srv))
			a := h.account(t)
			if got := slices.Contains(capabilitiesFor(a.Config), api.CapabilityCalendar); got != tc.cal {
				t.Fatalf("calendar capability %v, config %+v %+v", got, a.Config.CalDAV, a.Config.Calendar)
			}
			books := h.books(t)
			if len(books) != 1 || books[0].Count != 1 {
				t.Fatalf("books %+v", books)
			}
			ab := h.b.AddressBooks()
			cr, err := ab.CreateContact(h.ctx, api.ContactCreateParams{BookID: books[0].ID, Card: api.ContactCard{DisplayName: "Bob", Emails: []api.ContactEmail{{Address: "bob@example.org"}}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(srv.Cards(href)) != 2 {
				t.Fatalf("server %v", srv.Cards(href))
			}
			got, _ := ab.GetContact(h.ctx, api.ContactGetParams{ContactID: cr.ContactID})
			got.Contact.Nickname = "B"
			if _, err := ab.UpdateContact(h.ctx, api.ContactUpdateParams{ContactID: cr.ContactID, Revision: got.Contact.Revision, Card: got.Contact}); err != nil {
				t.Fatal(err)
			}
			h.syncNow(t)
			if bs := h.books(t); bs[0].Count != 2 {
				t.Fatalf("after pass %+v", bs)
			}
			if srv.AuthLeaks() != 0 || srv.EvilRequests() != 0 {
				t.Fatal("credentials leaked")
			}
		})
	}
}

// A hostile server: names, cards and books are cleaned; the password
// never leaves the server's site.
func TestCardDAVContactsHostile(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Hostile)
	href := srv.AddressBookHref("contacts")
	srv.PutCard(href, "evil.vcf", []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Mal\u202eory\x01\r\nEMAIL:not an address\r\nEMAIL:m@evil.test\r\nPHOTO;VALUE=URI:https://evil.test/p.jpg\r\nURL:javascript:alert(1)\r\nEND:VCARD\r\n"))
	srv.PutCard(href, "junk.vcf", []byte("this is not a card"))
	h := newContactsHarness(t, srv, srv.ServerURL())
	ab := h.b.AddressBooks()
	l, err := ab.ListContacts(h.ctx, api.ContactListParams{BookID: api.AddressBookAll})
	if err != nil || l.Total != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	got, _ := ab.GetContact(h.ctx, api.ContactGetParams{ContactID: l.Contacts[0].ID})
	raw, _ := json.Marshal(got.Contact)
	if strings.ContainsAny(got.Contact.DisplayName, "\u202e\x01") || len(got.Contact.Emails) != 1 || strings.Contains(string(raw), "p.jpg") {
		t.Fatalf("hostile card %s", raw)
	}
	if srv.AuthLeaks() != 0 || srv.EvilRequests() != 0 {
		t.Fatal("credentials leaked")
	}
}

func TestContactCardValidation(t *testing.T) {
	ok := api.ContactCard{DisplayName: "Jana"}
	if _, err := checkContactCard(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]api.ContactCard{
		"card: a contact needs":     {},
		"card.emails[0].address":    {DisplayName: "J", Emails: []api.ContactEmail{{Address: "Jana <j@x.org>"}}},
		"card.name.given":           {Name: api.ContactName{Given: strings.Repeat("a", api.MaxContactNameBytes+1)}},
		"card.note":                 {DisplayName: "J", Note: strings.Repeat("a", api.MaxContactNoteBytes+1)},
		"card.emails: at most":      {DisplayName: "J", Emails: make([]api.ContactEmail, api.MaxContactEmails+1)},
		"card.birthday":             {DisplayName: "J", Birthday: &api.ContactBirthday{Year: 2025, Month: 2, Day: 29}},
		"card.phones[0].number":     {DisplayName: "J", Phones: []api.ContactPhone{{Number: " "}}},
		"card.urls[0].label":        {DisplayName: "J", URLs: []api.ContactURL{{Label: api.ContactLabel(strings.Repeat("l", 65)), URL: "https://x"}}},
		"card.addresses[0].street":  {DisplayName: "J", Addresses: []api.ContactAddress{{Street: "\xff"}}},
		"card.displayName: not UTF": {DisplayName: "\xff"},
	}
	for want, c := range cases {
		_, err := checkContactCard(c)
		if errCode(t, err) != api.CodeInvalidArgument || !strings.Contains(err.Error(), strings.SplitN(want, ":", 2)[0]) {
			t.Errorf("%s: %v", want, err)
			continue
		}
		path := strings.TrimPrefix(strings.Fields(strings.SplitN(want, ":", 2)[0])[0], "card.")
		var e *api.Error
		if path == "card" {
			continue // no single field to mark
		}
		if !errors.As(err, &e) || e.Data != (api.ContactFieldData{Field: path}) {
			t.Errorf("%s: data %#v, want field %q", want, e.Data, path)
		}
	}
	// A birthday without a year may be 29 February.
	if _, err := checkContactCard(api.ContactCard{DisplayName: "J", Birthday: &api.ContactBirthday{Month: 2, Day: 29}}); err != nil {
		t.Error(err)
	}
}

func TestContactsCacheDropped(t *testing.T) {
	on := calDAVConfig("https://dav.example.test")
	on.CalDAV.Contacts = true
	off := calDAVConfig("https://dav.example.test")
	other := calDAVConfig("https://dav.example.test")
	other.CalDAV.Contacts, other.CalDAV.Username = true, "petr"
	g := graphConfig()
	g.Graph.Contacts = &api.ContactsConfig{Sync: true}
	if contactsCacheDropped(off, on) || !contactsCacheDropped(on, off) || !contactsCacheDropped(on, other) || contactsCacheDropped(on, on) {
		t.Error("caldav")
	}
	if !contactsCacheDropped(g, graphConfig()) || contactsCacheDropped(g, g) || !contactsCacheDropped(g, on) {
		t.Error("graph")
	}
}

// contact.search merges the synchronised books with the collected
// addresses: one row per address, reported as the book's with its name.
func TestContactSearchMergesSyncedBooks(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	srv.PutCard(srv.AddressBookHref("contacts"), "jana.vcf", vcf("J1", "Jana Nováková", "jana@example.org"))
	h := newContactsHarness(t, srv, srv.ServerURL())
	mail, err := h.b.Accounts().Add(h.ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.b.store.TouchCollectedAddresses(h.ctx, []api.Address{{Name: "jn", Address: "jana@example.org"}, {Name: "Jan Novák", Address: "jan@example.org"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cs := h.b.Contacts()
	// Scope account: the mail account has no books of its own.
	res, err := cs.Search(h.ctx, api.ContactSearchParams{AccountID: mail.AccountID, Query: "jan"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Contacts {
		if c.Source == api.ContactSourceContacts {
			t.Errorf("account scope found another account's book: %+v", c)
		}
	}
	res, err = cs.Search(h.ctx, api.ContactSearchParams{AccountID: mail.AccountID, Query: "jan", Scope: api.ContactScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	var jana *api.Contact
	n := 0
	for i, c := range res.Contacts {
		if c.Address == "jana@example.org" {
			jana, n = &res.Contacts[i], n+1
		}
	}
	if n != 1 || jana.Source != api.ContactSourceContacts || jana.Name != "Jana Nováková" || jana.Book != "Contacts" || jana.ContactID == "" {
		t.Fatalf("merged %+v", res.Contacts)
	}
	if _, err := cs.Search(h.ctx, api.ContactSearchParams{AccountID: mail.AccountID, Query: "jan", Scope: "everything"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("bad scope: %v", err)
	}
	// The recent book.
	rl, err := h.b.AddressBooks().ListContacts(h.ctx, api.ContactListParams{BookID: api.AddressBookRecent})
	if err != nil || rl.Total != 2 {
		t.Fatalf("recent %+v %v", rl, err)
	}
	rg, err := h.b.AddressBooks().GetContact(h.ctx, api.ContactGetParams{ContactID: rl.Contacts[0].ID})
	if err != nil || !rg.Contact.ReadOnly || rg.Contact.BookID != api.AddressBookRecent || len(rg.Contact.Emails) != 1 {
		t.Fatalf("recent get %+v %v", rg, err)
	}
	if _, err := h.b.AddressBooks().UpdateContact(h.ctx, api.ContactUpdateParams{ContactID: rl.Contacts[0].ID, Card: rg.Contact}); errCode(t, err) != api.CodeAddressBookReadOnly {
		t.Fatalf("recent update: %v", err)
	}
}

// A card with more items or longer names than contact.get shows: an
// update of another field leaves what the client never saw on the server
// (vcard.Patch gets only the shown part as before), and the card the
// client sends back unchanged passes the API's limits.
func TestContactUpdateKeepsWhatWasNotShown(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	book := srv.AddressBookHref("contacts")
	var extra []string
	for i := range api.MaxContactAddresses + 5 {
		extra = append(extra, "ADR;TYPE=HOME:;;Street "+strings.Repeat("x", 3)+string(rune('a'+i))+";City;;;")
	}
	for i := range api.MaxContactEmails + 3 {
		extra = append(extra, "EMAIL:m"+strings.Repeat("0", 2)+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.org")
	}
	long := strings.Repeat("Ž", api.MaxContactNameBytes) // 2 bytes a rune: over the cap
	extra = append(extra, "NICKNAME:"+long)
	srv.PutCard(book, "many.vcf", vcf("M1", "Many Items", "", extra...))
	h := newContactsHarness(t, srv, srv.ServerURL())
	ab := h.b.AddressBooks()
	books := h.books(t)
	l, err := ab.ListContacts(h.ctx, api.ContactListParams{BookID: books[0].ID})
	if err != nil || l.Total != 1 {
		t.Fatalf("list %+v %v", l, err)
	}
	got, err := ab.GetContact(h.ctx, api.ContactGetParams{ContactID: l.Contacts[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	c := got.Contact
	if len(c.Addresses) != api.MaxContactAddresses || len(c.Emails) != api.MaxContactEmails || len(c.Nickname) > api.MaxContactNameBytes {
		t.Fatalf("shown %d addresses, %d emails, nickname %d bytes", len(c.Addresses), len(c.Emails), len(c.Nickname))
	}
	c.Title = "Boss"
	if _, err := ab.UpdateContact(h.ctx, api.ContactUpdateParams{ContactID: c.ID, Revision: c.Revision, Card: c}); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, _, _ := srv.Card(book + "many.vcf")
	if n := strings.Count(string(data), "\nADR"); n != api.MaxContactAddresses+5 {
		t.Errorf("%d addresses on the server after the update:\n%s", n, data)
	}
	if n := strings.Count(string(data), "\nEMAIL"); n != api.MaxContactEmails+3 {
		t.Errorf("%d e-mails on the server after the update", n)
	}
	if !strings.Contains(string(data), "NICKNAME:"+long) || !strings.Contains(string(data), "TITLE:Boss") {
		t.Errorf("nickname or title:\n%s", data)
	}
}
