// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAddressBooksAndContacts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	books, err := s.UpsertAddressBooks(ctx, "acc_1", []AddressBook{
		{RemoteID: "https://dav.test/ab/a/", Name: "Personal", Position: 0},
		{RemoteID: "https://dav.test/ab/b/", Name: "Team", ReadOnly: true, Position: 1},
	})
	if err != nil || len(books) != 2 || !books[0].Visible || !books[1].ReadOnly || books[0].ID[:2] != "b_" {
		t.Fatalf("%+v %v", books, err)
	}
	a, b := books[0], books[1]
	// The user's choice and the engine's state survive a new listing.
	if err := s.SetAddressBookVisible(ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAddressBookSyncState(ctx, a.ID, AddressBookSyncState{SyncToken: "tok", CTag: "c1", Version: 1, LastSyncAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	again, err := s.UpsertAddressBooks(ctx, "acc_1", []AddressBook{{RemoteID: "https://dav.test/ab/b/", Name: "Team 2", Position: 0}, {RemoteID: "https://dav.test/ab/a/", Name: "Personal", Position: 1}})
	if err != nil || again[0].ID != b.ID || again[0].Visible || again[0].Name != "Team 2" || again[0].ReadOnly || again[1].SyncToken != "tok" || again[1].LastSyncAt.IsZero() {
		t.Fatalf("relisted %+v %v", again, err)
	}
	if err := s.SetAddressBookVisible(ctx, "b_none", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown book: %v", err)
	}

	id, err := s.UpsertContact(ctx, StoredContact{BookID: a.ID, RemoteID: "/ab/a/1.vcf", ETag: `"1"`, Raw: []byte("BEGIN:VCARD"), RawKind: ContactRawVCard,
		DisplayName: "Jana Nováková", SortKey: "novakova jana", Section: "N", Org: "Acme", PrimaryEmail: "jana@example.org",
		Emails: []ContactEmailRow{{Address: "jana@example.org", Label: "work"}, {Address: "jana@home.example"}}})
	if err != nil || id[:2] != "p_" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := s.UpsertContact(ctx, StoredContact{BookID: a.ID, RemoteID: "/ab/a/2.vcf", Raw: []byte("x"), RawKind: ContactRawVCard,
		DisplayName: "Petr Dvořák", SortKey: "dvorak petr", Section: "D", Emails: []ContactEmailRow{{Address: "petr@example.org"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertContact(ctx, StoredContact{BookID: a.ID, RemoteID: "/x", RawKind: "html"}); err == nil {
		t.Fatal("a raw kind outside the two")
	}
	// An update by remote id keeps the id and replaces the addresses.
	id2, err := s.UpsertContact(ctx, StoredContact{BookID: a.ID, RemoteID: "/ab/a/1.vcf", ETag: `"2"`, Raw: []byte("BEGIN:VCARD 2"), RawKind: ContactRawVCard,
		DisplayName: "Jana Svobodová", SortKey: "svobodova jana", Section: "S", PrimaryEmail: "jana@example.org",
		Emails: []ContactEmailRow{{Address: "jana@example.org"}}})
	if err != nil || id2 != id {
		t.Fatalf("update %q %v", id2, err)
	}
	c, err := s.GetContact(ctx, id)
	if err != nil || c.ETag != `"2"` || string(c.Raw) != "BEGIN:VCARD 2" || len(c.Emails) != 1 || c.Section != "S" {
		t.Fatalf("get %+v %v", c, err)
	}
	if _, err := s.GetContact(ctx, "p_none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	rows, total, err := s.ListContacts(ctx, ContactQuery{BookIDs: []string{a.ID}, Limit: 10})
	if err != nil || total != 2 || len(rows) != 2 || rows[0].DisplayName != "Petr Dvořák" || rows[0].Raw != nil {
		t.Fatalf("list %+v %d %v", rows, total, err)
	}
	// Search folds diacritics and takes prefixes, of names and addresses.
	for q, want := range map[string]int{"svobod": 1, "dvorak": 1, "petr@": 1, "example": 2, "nováková": 0, "zz": 0, "!!": 0} {
		if _, n, err := s.ListContacts(ctx, ContactQuery{BookIDs: []string{a.ID}, Query: q}); err != nil || n != want {
			t.Errorf("query %q: %d %v", q, n, err)
		}
	}
	if rows, n, _ := s.ListContacts(ctx, ContactQuery{BookIDs: []string{a.ID}, Offset: 1, Limit: 1}); n != 2 || len(rows) != 1 || rows[0].ID != id {
		t.Errorf("page %+v %d", rows, n)
	}
	if rows, _, _ := s.ListContacts(ctx, ContactQuery{}); len(rows) != 0 {
		t.Error("no books lists something")
	}

	known, err := s.LookupContactAddresses(ctx, []string{"jana@example.org", "nobody@example.org"}, []string{a.ID})
	if err != nil || len(known) != 1 || known["jana@example.org"] != id {
		t.Fatalf("lookup %v %v", known, err)
	}
	if known, _ := s.LookupContactAddresses(ctx, []string{"jana@example.org"}, []string{b.ID}); len(known) != 0 {
		t.Error("lookup outside the books")
	}
	found, err := s.SearchContactAddresses(ctx, "jan", []string{a.ID, b.ID}, 10)
	if err != nil || len(found) != 1 || found[0].Address != "jana@example.org" || found[0].ContactID != id {
		t.Fatalf("search %+v %v", found, err)
	}
	if found, _ := s.SearchContactAddresses(ctx, "dvo", []string{a.ID}, 10); len(found) != 1 || found[0].Address != "petr@example.org" {
		t.Errorf("search by a folded name word: %+v", found)
	}
	etags, err := s.ContactETags(ctx, a.ID)
	if err != nil || len(etags) != 2 || etags["/ab/a/1.vcf"] != `"2"` {
		t.Fatalf("etags %v %v", etags, err)
	}

	listed, _ := s.ListAddressBooks(ctx, "acc_1")
	if len(listed) != 2 || listed[1].ID != a.ID || listed[1].Count != 2 {
		t.Fatalf("books %+v", listed)
	}
	if gone, err := s.RemoveContactByRemote(ctx, a.ID, "/ab/a/2.vcf"); err != nil || !gone {
		t.Fatal(gone, err)
	}
	if gone, _ := s.RemoveContactByRemote(ctx, a.ID, "/ab/a/2.vcf"); gone {
		t.Error("removed twice")
	}
	if err := s.DeleteAddressBookContents(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	bk, err := s.GetAddressBook(ctx, a.ID)
	if err != nil || bk.Count != 0 || bk.SyncToken != "" || !bk.LastSyncAt.IsZero() {
		t.Fatalf("emptied %+v %v", bk, err)
	}
	if _, n, _ := s.ListContacts(ctx, ContactQuery{BookIDs: []string{a.ID}, Query: "jana"}); n != 0 {
		t.Error("the index keeps an emptied book's contact")
	}
	ids, err := s.DeleteAccountAddressBooks(ctx, "acc_1")
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
	if listed, _ := s.ListAddressBooks(ctx, ""); len(listed) != 0 {
		t.Errorf("books left %+v", listed)
	}
}

// The account's removal takes its address books along.
func TestDeleteAccountTakesAddressBooks(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := Account{Name: "Cal", Email: "jana@example.test", Enabled: true, Config: api.AccountConfig{Name: "Cal", Email: "jana@example.test",
		Kind: api.AccountCalDAV, CalDAV: &api.CalDAVConfig{ServerURL: "https://dav.test", Username: "jana", Contacts: true}}}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	books, _ := s.UpsertAddressBooks(ctx, acc.ID, []AddressBook{{RemoteID: "https://dav.test/ab/"}})
	if _, err := s.UpsertContact(ctx, StoredContact{BookID: books[0].ID, RemoteID: "/1", Raw: []byte("x"), RawKind: ContactRawVCard}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acc.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ab_contacts`); n != 0 {
		t.Errorf("%d contacts left", n)
	}
}

func TestCollectedAddressesAsABook(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.TouchCollectedAddresses(ctx, []api.Address{{Name: "Zora", Address: "zora@example.org"}, {Address: "adam@example.org"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, total, err := s.ListCollectedAddresses(ctx, "", 0, 10)
	if err != nil || total != 2 || rows[0].Address != "adam@example.org" {
		t.Fatalf("%+v %d %v", rows, total, err)
	}
	if _, total, _ := s.ListCollectedAddresses(ctx, "zor", 0, 10); total != 1 {
		t.Errorf("query: %d", total)
	}
	if n, _ := s.CountCollectedAddresses(ctx); n != 2 {
		t.Errorf("count %d", n)
	}
	if c, err := s.GetCollectedAddress(ctx, "zora@example.org"); err != nil || c.Name != "Zora" {
		t.Errorf("%+v %v", c, err)
	}
	if _, err := s.GetCollectedAddress(ctx, "x@example.org"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
}
