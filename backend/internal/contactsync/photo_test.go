// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactsync

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
)

func (f *fakeProvider) putRaw(book, id, etag string, raw []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cards[book] == nil {
		f.cards[book] = map[string]Card{}
	}
	c, err := vcard.Parse(raw)
	if err != nil {
		panic(err)
	}
	f.cards[book][id] = Card{RemoteID: id, ETag: etag, Raw: raw, RawKind: store.ContactRawVCard, Card: c}
}

func photoSample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vcard", "photos", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestEnginePhotos: a pass stores the embedded photos of the cards it
// stores (ok, refused, none for a link), a card stored before photos
// existed gets its row in the next pass, and a new revision is read again.
func TestEnginePhotos(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	fp := newFake()
	const book = "https://dav.test/a/"
	fp.books = []RemoteBook{{RemoteID: book}}
	fp.putRaw(book, "/ok.vcf", "e1", photoSample(t, "jpeg-3.0.vcf"))
	fp.putRaw(book, "/bomb.vcf", "e1", photoSample(t, "bomb-png-40000.vcf"))
	fp.putRaw(book, "/link.vcf", "e1", photoSample(t, "uri-4.0.vcf"))
	var mu sync.Mutex
	passes := 0
	sv := New(Options{Store: st, Interval: time.Hour,
		Providers: func(context.Context, store.Account) (Provider, error) { return fp, nil },
		Changed:   func(string, []string) { mu.Lock(); passes++; mu.Unlock() }})
	sv.Start(account())
	go sv.Run(ctx)
	waitFor(t, "a first pass", func() bool { s, ok := sv.State("acc_1"); return ok && !s.LastSync.IsZero() })
	books, _ := st.ListAddressBooks(ctx, "acc_1")
	states := func() map[string]string {
		rows, _, err := st.ListContacts(ctx, store.ContactQuery{BookIDs: []string{books[0].ID}})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, r := range rows {
			p, _ := st.GetContactPhoto(ctx, r.ID)
			out[r.RemoteID] = r.PhotoState + "/" + p.State
		}
		return out
	}
	want := map[string]string{"/ok.vcf": "ready/ok", "/bomb.vcf": "/refused", "/link.vcf": "/none"}
	if got := states(); len(got) != 3 || got["/ok.vcf"] != want["/ok.vcf"] || got["/bomb.vcf"] != want["/bomb.vcf"] || got["/link.vcf"] != want["/link.vcf"] {
		t.Fatalf("states %v", got)
	}
	rows, _, _ := st.ListContacts(ctx, store.ContactQuery{BookIDs: []string{books[0].ID}})
	for _, r := range rows {
		if r.RemoteID == "/ok.vcf" {
			p, _ := st.GetContactPhoto(ctx, r.ID)
			if p.MIME != "image/jpeg" || len(p.Data) == 0 || p.Revision != "e1" {
				t.Errorf("photo %q of %d bytes, revision %q", p.MIME, len(p.Data), p.Revision)
			}
		}
	}

	// A card stored without its row (as before 0024): the next pass reads
	// it although the server's card did not change.
	raw := photoSample(t, "png-alpha-4.0.vcf")
	c, _ := vcard.Parse(raw)
	row, _ := Row(books[0].ID, Card{RemoteID: "/old.vcf", ETag: "e1", Raw: raw, RawKind: store.ContactRawVCard, Card: c})
	if _, err := st.UpsertContact(ctx, row); err != nil {
		t.Fatal(err)
	}
	fp.putRaw(book, "/old.vcf", "e1", raw)
	// A new revision of a card is read again.
	fp.putRaw(book, "/link.vcf", "e2", photoSample(t, "gif-2.1.vcf"))
	last, _ := sv.State("acc_1")
	sv.SyncNow("acc_1")
	waitFor(t, "a second pass", func() bool { s, _ := sv.State("acc_1"); return s.LastSync.After(last.LastSync) })
	got := states()
	if got["/old.vcf"] != "ready/ok" || got["/link.vcf"] != "ready/ok" || got["/ok.vcf"] != "ready/ok" {
		t.Errorf("after: %v", got)
	}
	mu.Lock()
	if passes < 2 {
		t.Errorf("the backfill notified %d times", passes)
	}
	mu.Unlock()
}

// TestStoreCardPhoto: a write's answer stores its photo too.
func TestStoreCardPhoto(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	books, _ := st.UpsertAddressBooks(ctx, "acc_1", []store.AddressBook{{RemoteID: "/ab/"}})
	raw := photoSample(t, "jpeg-3.0.vcf")
	c, _ := vcard.Parse(raw)
	id, err := StoreCard(ctx, st, books[0].ID, Card{RemoteID: "/1.vcf", ETag: "e9", Raw: raw, RawKind: store.ContactRawVCard, Card: c})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := st.GetContactPhoto(ctx, id); err != nil || p.State != store.PhotoOK || p.Revision != "e9" {
		t.Errorf("%+v %v", p, err)
	}
	// A Graph card has no row: its photo is fetched on demand.
	gid, err := StoreCard(ctx, st, books[0].ID, Card{RemoteID: "g", ETag: "ck", Raw: []byte("{}"), RawKind: store.ContactRawGraph, Card: &vcard.Card{DisplayName: "G"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetContactPhoto(ctx, gid); err == nil {
		t.Error("a graph contact got a row")
	}
}
