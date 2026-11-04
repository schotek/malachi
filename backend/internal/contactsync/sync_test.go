// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactsync

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeProvider is an in-memory source: books, cards per book, an error
// to return from the next listing, a version.
type fakeProvider struct {
	mu      sync.Mutex
	books   []RemoteBook
	cards   map[string]map[string]Card // book remote id → remote id → card
	listErr error
	version int
	syncs   map[string]int // book remote id → passes
}

func newFake() *fakeProvider {
	return &fakeProvider{cards: map[string]map[string]Card{}, syncs: map[string]int{}, version: 1}
}

func (f *fakeProvider) put(book, id, name, email, etag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cards[book] == nil {
		f.cards[book] = map[string]Card{}
	}
	c := &vcard.Card{DisplayName: name, Emails: []vcard.Email{{Address: email}}}
	f.cards[book][id] = Card{RemoteID: id, ETag: etag, Raw: vcard.New(c, "3.0", id), RawKind: store.ContactRawVCard, Card: c}
}

func (f *fakeProvider) AddressBooks(context.Context) ([]RemoteBook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return slices.Clone(f.books), nil
}

// Sync always lists the whole book (Full), fetching only changed cards.
func (f *fakeProvider) Sync(_ context.Context, b StoredBook, known map[string]string, sink Sink) (SyncState, error) {
	f.mu.Lock()
	f.syncs[b.RemoteID]++
	cards := make([]Card, 0, len(f.cards[b.RemoteID]))
	for _, c := range f.cards[b.RemoteID] {
		cards = append(cards, c)
	}
	f.mu.Unlock()
	for _, c := range cards {
		if known[c.RemoteID] == c.ETag {
			sink.Seen(c.RemoteID)
			continue
		}
		if err := sink.Upsert(c); err != nil {
			return SyncState{}, err
		}
	}
	return SyncState{Token: "t", Full: true}, nil
}

func (f *fakeProvider) Get(context.Context, StoredBook, string) (Card, error) { return Card{}, ErrGone }
func (f *fakeProvider) Create(context.Context, StoredBook, *vcard.Card) (Card, error) {
	return Card{}, api.NewError(api.CodeNotImplemented, "x")
}
func (f *fakeProvider) Update(context.Context, StoredBook, StoredContact, *vcard.Card, *vcard.Card) (Card, error) {
	return Card{}, api.NewError(api.CodeNotImplemented, "x")
}
func (f *fakeProvider) Delete(context.Context, StoredBook, StoredContact) error { return nil }
func (f *fakeProvider) DataVersion() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func account() store.Account {
	return store.Account{ID: "acc_1", Enabled: true, Config: api.AccountConfig{Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: "https://dav.test", Username: "u", Contacts: true}}}
}

func TestHasContacts(t *testing.T) {
	a := account()
	if !HasContacts(a) {
		t.Fatal("caldav with contacts")
	}
	a.Enabled = false
	if HasContacts(a) {
		t.Fatal("disabled")
	}
	g := store.Account{Enabled: true, Config: api.AccountConfig{Kind: api.AccountGraph, Graph: &api.GraphConfig{}}}
	if HasContacts(g) {
		t.Fatal("graph without contacts")
	}
	g.Config.Graph.Contacts = &api.ContactsConfig{Sync: true}
	if !HasContacts(g) {
		t.Fatal("graph with contacts.sync")
	}
	imap := store.Account{Enabled: true, Config: api.AccountConfig{Kind: api.AccountIMAP}}
	if HasContacts(imap) {
		t.Fatal("imap")
	}
}

func TestEngineSyncsVisibleBooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	fp := newFake()
	fp.books = []RemoteBook{{RemoteID: "https://dav.test/a/", Name: "Personal‮"}, {RemoteID: "https://dav.test/b/", Name: "Team", ReadOnly: true}}
	fp.put("https://dav.test/a/", "/a/1.vcf", "Jana", "jana@example.org", "e1")
	fp.put("https://dav.test/a/", "/a/2.vcf", "Petr", "petr@example.org", "e1")
	fp.put("https://dav.test/b/", "/b/1.vcf", "Eva", "eva@example.org", "e1")
	var mu sync.Mutex
	var notified [][]string
	sv := New(Options{Store: st, Interval: time.Hour,
		Providers: func(context.Context, store.Account) (Provider, error) { return fp, nil },
		Changed: func(_ string, ids []string) {
			mu.Lock()
			notified = append(notified, ids)
			mu.Unlock()
		}})
	sv.Start(account()) // queued before Run
	go sv.Run(ctx)
	waitFor(t, "a first pass", func() bool { s, ok := sv.State("acc_1"); return ok && !s.LastSync.IsZero() })
	books, _ := st.ListAddressBooks(ctx, "acc_1")
	if len(books) != 2 || books[0].Count != 2 || books[1].Count != 1 || books[0].Name != "Personal" || !books[1].ReadOnly {
		t.Fatalf("%+v", books)
	}
	mu.Lock()
	if len(notified) == 0 || len(notified[0]) != 2 {
		t.Errorf("notified %v", notified)
	}
	mu.Unlock()

	// Hidden: not synchronised, its cards gone.
	if err := st.SetAddressBookVisible(ctx, books[1].ID, false); err != nil {
		t.Fatal(err)
	}
	fp.put("https://dav.test/a/", "/a/1.vcf", "Jana N", "jana@example.org", "e2")
	fp.mu.Lock()
	delete(fp.cards["https://dav.test/a/"], "/a/2.vcf")
	before := fp.syncs["https://dav.test/b/"]
	fp.mu.Unlock()
	last, _ := sv.State("acc_1")
	sv.SyncNow("acc_1")
	waitFor(t, "a second pass", func() bool { s, _ := sv.State("acc_1"); return s.LastSync.After(last.LastSync) })
	books, _ = st.ListAddressBooks(ctx, "acc_1")
	if books[0].Count != 1 || books[1].Count != 0 {
		t.Fatalf("after: %+v", books)
	}
	fp.mu.Lock()
	if fp.syncs["https://dav.test/b/"] != before {
		t.Error("a hidden book was synchronised")
	}
	fp.mu.Unlock()
	rows, _, _ := st.ListContacts(ctx, store.ContactQuery{BookIDs: []string{books[0].ID}})
	if len(rows) != 1 || rows[0].DisplayName != "Jana N" {
		t.Fatalf("rows %+v", rows)
	}

	// A new data version lists everything again; a vanished book goes.
	fp.mu.Lock()
	fp.version = 2
	fp.books = fp.books[:1]
	fp.mu.Unlock()
	last, _ = sv.State("acc_1")
	sv.SyncNow("acc_1")
	waitFor(t, "a third pass", func() bool { s, _ := sv.State("acc_1"); return s.LastSync.After(last.LastSync) })
	books, _ = st.ListAddressBooks(ctx, "acc_1")
	if len(books) != 1 || books[0].Version != 2 || books[0].Count != 1 {
		t.Fatalf("version: %+v", books)
	}

	// An account-wide failure is the state, and the provider is made again.
	fp.mu.Lock()
	fp.listErr = api.NewError(api.CodeContactsConsentRequired, "no consent")
	fp.mu.Unlock()
	sv.SyncNow("acc_1")
	waitFor(t, "consentRequired", func() bool { s, _ := sv.State("acc_1"); return s.Status == StatusConsentRequired })
	if _, ok := sv.Provider("acc_1"); ok {
		t.Error("the provider stays after a consent failure")
	}
	sv.Stop("acc_1")
	if _, ok := sv.State("acc_1"); ok {
		t.Error("state after stop")
	}
}

func TestRow(t *testing.T) {
	c := &vcard.Card{Name: vcard.Name{Given: "Jana", Family: "Nováková"}, Org: "Acme",
		Emails: []vcard.Email{{Address: "Jana@Example.org"}, {Address: "jana@example.org"}, {Address: "not an address"}}}
	row, ok := Row("b_1", Card{RemoteID: "/1", Card: c, Raw: []byte("x"), RawKind: store.ContactRawVCard})
	if !ok || row.SortKey != "novakova jana" || row.Section != "N" || row.PrimaryEmail != "jana@example.org" || len(row.Emails) != 1 || row.DisplayName != "Jana Nováková" {
		t.Fatalf("%+v", row)
	}
	if _, ok := Row("b_1", Card{Card: c}); ok {
		t.Error("no remote id")
	}
	if _, ok := Row("b_1", Card{RemoteID: "/1", Card: c, Raw: make([]byte, api.MaxVCardBytes+1)}); ok {
		t.Error("raw over the limit")
	}
}
