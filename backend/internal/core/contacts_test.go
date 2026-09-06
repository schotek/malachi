// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeDirectory stands in for the address-book client.
type fakeDirectory struct {
	books     []contacts.Book
	booksErr  error
	found     []contacts.Contact
	searchErr error

	gotBooks []contacts.Book
	gotQuery string
	searches int
}

func (f *fakeDirectory) Books(context.Context) ([]contacts.Book, error) {
	return f.books, f.booksErr
}

func (f *fakeDirectory) Search(_ context.Context, books []contacts.Book, query string, _ int) ([]contacts.Contact, error) {
	f.searches++
	f.gotBooks, f.gotQuery = books, query
	return f.found, f.searchErr
}

func TestContactSearchValidation(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	cases := []struct {
		name string
		p    api.ContactSearchParams
		code api.ErrorCode
	}{
		{"no account", api.ContactSearchParams{Query: "al"}, api.CodeInvalidArgument},
		{"unknown account", api.ContactSearchParams{AccountID: "acc_nope", Query: "al"}, api.CodeAccountNotFound},
		{"empty query", api.ContactSearchParams{AccountID: acc, Query: "  "}, api.CodeInvalidArgument},
		{"long query", api.ContactSearchParams{AccountID: acc, Query: strings.Repeat("a", api.MaxContactQueryBytes+1)}, api.CodeInvalidArgument},
		{"control character", api.ContactSearchParams{AccountID: acc, Query: "al\x00"}, api.CodeInvalidArgument},
		{"invalid utf-8", api.ContactSearchParams{AccountID: acc, Query: "al\xff"}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		_, err := b.Contacts().Search(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
	// A valid query on an empty store is an empty, non-nil list.
	res, err := b.Contacts().Search(ctx, api.ContactSearchParams{AccountID: acc, Query: "al"})
	if err != nil || res.Contacts == nil || len(res.Contacts) != 0 {
		t.Fatalf("empty search = %+v, %v", res, err)
	}
}

func TestContactSearchMergesSources(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	now := time.Now()
	if err := b.store.TouchCollectedAddresses(ctx, []api.Address{
		{Name: "Alice Sent", Address: "alice@example.org"},
		{Name: "Alan Only", Address: "alan@example.org"},
	}, now); err != nil {
		t.Fatal(err)
	}
	mine := contacts.Book{UID: "b1", Name: "Contacts", Backend: "carddav", Collection: "c1", Email: "me@example.invalid"}
	other := contacts.Book{UID: "b2", Name: "Elsewhere", Backend: "carddav", Collection: "c2", Email: "other@example.invalid"}
	dir := &fakeDirectory{
		books: []contacts.Book{other, mine},
		found: []contacts.Contact{
			{Name: "Alice Book", Address: "Alice@Example.org", Book: "Contacts"},
			{Name: "Albert Book", Address: "albert@example.org", Book: "Contacts"},
		},
	}
	b.Directory = dir

	res, err := b.Contacts().Search(ctx, api.ContactSearchParams{AccountID: api.AccountID(acc), Query: "al"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.gotBooks) != 1 || dir.gotBooks[0].UID != "b1" || dir.gotQuery != "al" {
		t.Errorf("directory asked for books %+v, query %q", dir.gotBooks, dir.gotQuery)
	}
	byAddr := map[string]api.Contact{}
	for _, c := range res.Contacts {
		byAddr[c.Address] = c
	}
	if len(res.Contacts) != 3 {
		t.Fatalf("merged = %+v", res.Contacts)
	}
	// The address in both sources is one row, the address book's, with
	// the book's name; the collected-only and book-only ones keep theirs.
	if a := byAddr["alice@example.org"]; a.Source != api.ContactSourceAddressBook || a.Name != "Alice Book" || a.Book != "Contacts" {
		t.Errorf("alice = %+v", a)
	}
	if a := byAddr["alan@example.org"]; a.Source != api.ContactSourceSent || a.Name != "Alan Only" || a.Book != "" {
		t.Errorf("alan = %+v", a)
	}
	if a := byAddr["albert@example.org"]; a.Source != api.ContactSourceAddressBook || a.Name != "Albert Book" {
		t.Errorf("albert = %+v", a)
	}
	// Alice was both written to and found in the book: that lifts her
	// above Alan, whose match and use are otherwise the same.
	if res.Contacts[0].Address != "alice@example.org" {
		t.Errorf("order = %+v", res.Contacts)
	}

	// An account without books of its own never reaches the directory.
	dir.searches = 0
	dir.books = []contacts.Book{other}
	res, err = b.Contacts().Search(ctx, api.ContactSearchParams{AccountID: api.AccountID(acc), Query: "al"})
	if err != nil || len(res.Contacts) != 2 || dir.searches != 0 {
		t.Errorf("without own books: %+v, %v, searches %d", res.Contacts, err, dir.searches)
	}
}

func TestContactSearchWithoutDirectory(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	if err := b.store.TouchCollectedAddresses(ctx, []api.Address{{Name: "Alice", Address: "alice@example.org"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	mine := contacts.Book{UID: "b1", Name: "Contacts", Email: "me@example.invalid"}
	for name, dir := range map[string]contacts.Directory{
		"nil":               nil,
		"books unavailable": &fakeDirectory{booksErr: api.NewError(api.CodeUnavailable, "no bus")},
		"search fails":      &fakeDirectory{books: []contacts.Book{mine}, searchErr: api.NewError(api.CodeServerError, "timed out")},
	} {
		b.Directory = dir
		res, err := b.Contacts().Search(ctx, api.ContactSearchParams{AccountID: acc, Query: "ali"})
		if err != nil || len(res.Contacts) != 1 || res.Contacts[0].Source != api.ContactSourceSent {
			t.Errorf("%s: %+v, %v", name, res, err)
		}
	}
}

func TestAccountBooks(t *testing.T) {
	books := []contacts.Book{
		{UID: "goa", GOAAccountID: "account_1", Email: "work@example.invalid"},
		{UID: "mail", Email: "me@example.invalid"},
		{UID: "local"},
		{UID: "foreign", GOAAccountID: "account_2", Email: "other@example.invalid"},
	}
	graph := store.Account{Config: api.AccountConfig{Email: "Work@Example.invalid", Graph: &api.GraphConfig{GOAAccountID: "account_1"}}}
	imap := store.Account{Config: api.AccountConfig{Email: "ME@example.invalid"}}
	lonely := store.Account{Config: api.AccountConfig{Email: "nobody@example.invalid"}}
	uids := func(bs []contacts.Book) string {
		var s []string
		for _, b := range bs {
			s = append(s, b.UID)
		}
		return strings.Join(s, ",")
	}
	if got := uids(accountBooks(graph, books)); got != "goa" {
		t.Errorf("graph account: %s", got)
	}
	if got := uids(accountBooks(imap, books)); got != "mail" {
		t.Errorf("imap account: %s", got)
	}
	if got := uids(accountBooks(lonely, books)); got != "" {
		t.Errorf("account without a collection: %s", got)
	}
}

func TestMergeContacts(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	old := now.Add(-90 * 24 * time.Hour)
	collected := []store.CollectedAddress{
		{Address: "bob@example.org", Name: "Bob Builder", Uses: 1, LastUsed: old},
		{Address: "bo@example.org", Name: "", Uses: 30, LastUsed: now.Add(-time.Hour)},
		{Address: "zed@example.org", Name: "Bo\x00b", Uses: 1, LastUsed: now},
	}
	found := []contacts.Contact{
		{Name: "Roberta Bo", Address: "roberta@example.org", Book: "GAL"},
		{Name: "Bob Builder", Address: "BOB@example.org", Book: "GAL"},
		{Name: "Server Match", Address: "sm@example.org", Book: "GAL"},
	}
	got := mergeContacts("bo", collected, found, now, 10)
	var order []string
	for _, c := range got {
		order = append(order, c.Address)
	}
	// bo@: exact address, heavy and recent use (130). bob@: address prefix
	// in both sources, the collected use old (81 + 5; the book copy wins
	// the row). roberta: a word of the name (70). zed: "Bo\x00b" is cleaned
	// to "" before scoring, so nothing matches — the floor plus a recent
	// single use (41). sm: the floor alone (30).
	want := "bo@example.org,bob@example.org,roberta@example.org,zed@example.org,sm@example.org"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %v", order)
	}
	if got[1].Source != api.ContactSourceAddressBook || got[1].Book != "GAL" || got[1].Name != "Bob Builder" {
		t.Errorf("bob merged = %+v", got[1])
	}
	if got[3].Name != "" {
		t.Errorf("control characters survived: %q", got[3].Name)
	}
	if len(mergeContacts("bo", collected, found, now, 2)) != 2 {
		t.Error("limit not applied")
	}
	if len(mergeContacts("bo", nil, nil, now, 5)) != 0 {
		t.Error("nothing in, something out")
	}
}

func TestBackfillCollectedAddresses(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Selectable: true, Subscribed: true},
	})
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	msgs := []*store.Message{
		{AccountID: acc, FolderID: folders["Sent"].ID, UID: 1, Subject: "older", Date: day,
			To: []api.Address{{Name: "Old Name", Address: "alice@example.org"}}},
		{AccountID: acc, FolderID: folders["Sent"].ID, UID: 2, Subject: "newer", Date: day.Add(24 * time.Hour),
			To: []api.Address{{Name: "New Name", Address: "alice@example.org"}}, CC: []api.Address{{Address: "cc@example.org"}}},
		{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 3, Subject: "incoming", Date: day,
			From: []api.Address{{Name: "Mallory", Address: "mallory@example.org"}}, To: []api.Address{{Address: "me@example.invalid"}}},
	}
	if err := b.store.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillCollectedAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := b.store.SearchCollectedAddresses(ctx, "example", 10)
	if err != nil {
		t.Fatal(err)
	}
	byAddr := map[string]store.CollectedAddress{}
	for _, c := range got {
		byAddr[c.Address] = c
	}
	if len(got) != 2 {
		t.Fatalf("collected = %+v", got)
	}
	if a := byAddr["alice@example.org"]; a.Name != "New Name" || a.Uses != 2 || !a.FirstUsed.Equal(day) || !a.LastUsed.Equal(day.Add(24*time.Hour)) {
		t.Errorf("alice = %+v", a)
	}
	if _, ok := byAddr["cc@example.org"]; !ok {
		t.Error("cc recipient not collected")
	}
	if _, ok := byAddr["mallory@example.org"]; ok {
		t.Error("an incoming sender was collected")
	}
	if _, ok := byAddr["me@example.invalid"]; ok {
		t.Error("the inbox's To was collected")
	}

	// Once is once: a later Sent message is the outbox worker's business.
	if _, done, _ := b.store.GetMeta(ctx, metaContactsBackfilled); !done {
		t.Fatal("meta key not set")
	}
	late := []*store.Message{{AccountID: acc, FolderID: folders["Sent"].ID, UID: 4, Subject: "late", Date: day,
		To: []api.Address{{Address: "late@example.org"}}}}
	if err := b.store.UpsertMessages(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillCollectedAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.store.SearchCollectedAddresses(ctx, "late@", 10); len(got) != 0 {
		t.Errorf("second backfill collected: %+v", got)
	}
}
