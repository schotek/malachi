// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

func photoSample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vcard", "photos", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// samplePicture is the embedded picture of a corpus card.
func samplePicture(t *testing.T, name string) []byte {
	t.Helper()
	c, err := vcard.Parse(photoSample(t, name))
	if err != nil || c.Photo == nil {
		t.Fatalf("%s: %v", name, err)
	}
	return c.Photo.Data
}

// checkPhoto verifies a ready photo: the daemon's own encoding within the
// API's limits.
func checkPhoto(t *testing.T, what string, p *api.ContactPhotoResult) {
	t.Helper()
	if p.State != api.ContactPhotoStateReady || len(p.Data) == 0 || len(p.Data) > api.MaxContactPhotoBytes {
		t.Fatalf("%s: %s, %d bytes", what, p.State, len(p.Data))
	}
	img, format, err := image.Decode(bytes.NewReader(p.Data))
	if err != nil || "image/"+format != p.MIME {
		t.Fatalf("%s: %q decodes as %q: %v", what, p.MIME, format, err)
	}
	if b := img.Bounds(); b.Dx() != b.Dy() || b.Dx() > api.MaxContactPhotoPixels {
		t.Fatalf("%s: %v", what, b.Size())
	}
}

func summaries(t *testing.T, ab api.AddressBookService, ctx context.Context, book api.AddressBookID) map[string]api.ContactSummary {
	t.Helper()
	l, err := ab.ListContacts(ctx, api.ContactListParams{BookID: book})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.ContactSummary{}
	for _, c := range l.Contacts {
		out[c.DisplayName] = c
	}
	return out
}

func TestCardDAVContactPhotos(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	book := srv.AddressBookHref("contacts")
	srv.PutCard(book, "jana.vcf", bytes.Replace(photoSample(t, "jpeg-3.0.vcf"), []byte("FN:jpeg-3.0"), []byte("FN:Jana"), 1))
	srv.PutCard(book, "link.vcf", vcf("L1", "Link Person", "link@example.org", "PHOTO;VALUE=uri:https://tracker.example/p.jpg"))
	srv.PutCard(book, "bomb.vcf", bytes.Replace(photoSample(t, "bomb-png-40000.vcf"), []byte("FN:bomb-png-40000"), []byte("FN:Bomb"), 1))
	h := newContactsHarness(t, srv, srv.ServerURL())
	ab, ctx := h.b.AddressBooks(), h.ctx
	books := h.books(t)
	got := summaries(t, ab, ctx, books[0].ID)
	if got["Jana"].Photo != api.ContactPhotoReady || got["Link Person"].Photo != "" || got["Bomb"].Photo != "" {
		t.Fatalf("summaries %+v", got)
	}
	jana := got["Jana"].ID
	card, err := ab.GetContact(ctx, api.ContactGetParams{ContactID: jana})
	if err != nil || card.Contact.Photo != api.ContactPhotoReady {
		t.Fatalf("card photo %q %v", card.Contact.Photo, err)
	}
	p, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: jana})
	if err != nil {
		t.Fatal(err)
	}
	checkPhoto(t, "jana", p)
	if p.Revision != card.Contact.Revision || p.MIME != "image/jpeg" {
		t.Errorf("revision %q of %q, mime %q", p.Revision, card.Contact.Revision, p.MIME)
	}
	if bytes.Equal(p.Data, samplePicture(t, "jpeg-3.0.vcf")) {
		t.Error("the server's bytes went out")
	}
	for _, name := range []string{"Link Person", "Bomb"} {
		np, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got[name].ID})
		if err != nil || np.State != api.ContactPhotoStateNone || np.Data != nil || np.MIME != "" {
			t.Errorf("%s: %+v %v", name, np, err)
		}
	}
	for _, r := range srv.Requests() {
		if strings.Contains(r.Path, "tracker") || strings.Contains(r.Path, "p.jpg") {
			t.Errorf("a photo link was fetched: %s", r.Path)
		}
	}

	// Lookup with photos: only the contact with one.
	lk, err := ab.Lookup(ctx, api.ContactLookupParams{Addresses: []string{"JPEG-3.0@photo.example", "link@example.org"}, WithPhotos: true})
	if err != nil || len(lk.Known) != 2 || len(lk.Photos) != 1 ||
		lk.Photos["jpeg-3.0@photo.example"] != (api.ContactPhotoRef{ContactID: jana, State: api.ContactPhotoReady}) {
		t.Fatalf("lookup %+v %v", lk, err)
	}
	if lk, _ := ab.Lookup(ctx, api.ContactLookupParams{Addresses: []string{"jpeg-3.0@photo.example"}}); lk.Photos != nil {
		t.Errorf("photos without withPhotos: %+v", lk.Photos)
	}

	// An edit keeps the PHOTO on the server and the photo stays ready.
	c := card.Contact
	c.Title = "Editor"
	up, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: jana, Revision: c.Revision, Card: c})
	if err != nil {
		t.Fatal(err)
	}
	data, _, _ := srv.Card(book + "jana.vcf")
	orig := photoSample(t, "jpeg-3.0.vcf")
	start := bytes.Index(orig, []byte("PHOTO"))
	end := start + bytes.Index(orig[start:], []byte("\r\nEND:VCARD"))
	if !bytes.Contains(data, orig[start:end]) || !bytes.Contains(data, []byte("TITLE:Editor")) {
		t.Fatalf("the patched card lost its PHOTO:\n%.300s", data)
	}
	p2, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: jana})
	if err != nil || p2.Revision != up.Revision {
		t.Fatalf("after update: %+v %v", p2, err)
	}
	checkPhoto(t, "after update", p2)
	if s := summaries(t, ab, ctx, books[0].ID); s["Jana"].Photo != api.ContactPhotoReady {
		t.Errorf("after update %+v", s["Jana"])
	}

	// Errors and the recent book.
	if _, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: "p_none"}); errCode(t, err) != api.CodeContactNotFound {
		t.Errorf("unknown: %v", err)
	}
	if _, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: recentID("nobody@example.org")}); errCode(t, err) != api.CodeContactNotFound {
		t.Errorf("unknown recent: %v", err)
	}
	if err := h.b.store.TouchCollectedAddresses(ctx, []api.Address{{Address: "r@example.org"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rp, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: recentID("r@example.org")}); err != nil || rp.State != api.ContactPhotoStateNone {
		t.Errorf("recent: %+v %v", rp, err)
	}
}

// newGraphPhotoBackend is a backend over the Graph contacts fake with one
// account whose contacts are on, after its first pass.
func newGraphPhotoBackend(t *testing.T, fake *graphContactsFake) (*Backend, *contactsRec, context.Context, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	b.GOA = &fakeGOA{accounts: []goa.Account{{ID: goaID, ProviderType: goa.ProviderMicrosoft365, Email: "me@contoso.invalid", OAuth2: true}},
		tokens: map[string]string{goaID: "tok"}}
	b.CalendarGraphBaseURL, b.CalendarGraphHTTP = fake.srv.URL, fake.srv.Client()
	rec := &contactsRec{}
	b.SetNotifier(rec)
	b.contactSync = contactsync.New(contactsync.Options{Store: b.store, Providers: b.contactsProvider, Interval: time.Hour,
		Changed: func(accountID string, ids []string) { b.contactsChanged(accountID, ids...) }})
	done := make(chan struct{})
	go func() { b.contactSync.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	cfg := graphConfig()
	cfg.Graph.Contacts = &api.ContactsConfig{Sync: true}
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	id := string(add.AccountID)
	a, _ := b.store.GetAccount(ctx, id)
	b.contactSync.Start(a)
	waitLong(t, "a pass", func() bool { st, ok := b.contactSync.State(id); return ok && !st.LastSync.IsZero() })
	return b, rec, ctx, id
}

func TestGraphContactPhotos(t *testing.T) {
	fake := newGraphContactsFake(t)
	for _, c := range []struct{ id, name string }{{"C1", "Ada"}, {"C2", "Bob"}, {"C3", "Cecil"}, {"C4", "Dora"}} {
		fake.put(map[string]any{"id": c.id, "displayName": c.name,
			"emailAddresses": []any{map[string]any{"address": strings.ToLower(c.name) + "@example.org"}}})
	}
	fake.photos["C1"] = samplePicture(t, "jpeg-3.0.vcf")
	fake.photos["C3"] = samplePicture(t, "png-alpha-4.0.vcf")
	fake.photos["C4"] = samplePicture(t, "bomb-png-40000.vcf")
	b, rec, ctx, id := newGraphPhotoBackend(t, fake)
	ab := b.AddressBooks()
	books, _ := ab.ListBooks(ctx, api.AddressBookListParams{AccountID: api.AccountID(id)})
	bookID := books.Books[0].ID
	got := summaries(t, ab, ctx, bookID)
	for _, n := range []string{"Ada", "Bob", "Cecil", "Dora"} {
		if got[n].Photo != api.ContactPhotoRemote {
			t.Fatalf("%s: %q before any download", n, got[n].Photo)
		}
	}
	fake.mu.Lock()
	if fake.photoGets != 0 {
		t.Fatalf("a pass downloaded %d photos", fake.photoGets)
	}
	fake.mu.Unlock()

	// Ada: downloaded, normalised, stored; the book is notified; a second
	// request does not download again.
	notes := rec.count()
	p, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got["Ada"].ID})
	if err != nil {
		t.Fatal(err)
	}
	checkPhoto(t, "ada", p)
	card, _ := ab.GetContact(ctx, api.ContactGetParams{ContactID: got["Ada"].ID})
	if p.Revision != card.Contact.Revision || card.Contact.Photo != api.ContactPhotoReady {
		t.Errorf("revision %q of %q, card photo %q", p.Revision, card.Contact.Revision, card.Contact.Photo)
	}
	waitLong(t, "notify.contactsChanged", func() bool { return rec.count() > notes })
	rec.mu.Lock()
	last := rec.evs[len(rec.evs)-1]
	rec.mu.Unlock()
	if string(last.AccountID) != id || len(last.BookIDs) != 1 || last.BookIDs[0] != bookID {
		t.Errorf("notification %+v", last)
	}
	if _, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got["Ada"].ID}); err != nil {
		t.Fatal(err)
	}
	// Bob has none (404), Dora's is refused: both none, remembered.
	for _, n := range []string{"Bob", "Dora"} {
		np, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got[n].ID})
		if err != nil || np.State != api.ContactPhotoStateNone || np.Data != nil {
			t.Fatalf("%s: %+v %v", n, np, err)
		}
		if _, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got[n].ID}); err != nil {
			t.Fatal(err)
		}
	}
	fake.mu.Lock()
	if fake.photoGets != 3 {
		t.Errorf("%d downloads for three contacts", fake.photoGets)
	}
	fake.mu.Unlock()
	got = summaries(t, ab, ctx, bookID)
	if got["Ada"].Photo != api.ContactPhotoReady || got["Bob"].Photo != "" || got["Dora"].Photo != "" || got["Cecil"].Photo != api.ContactPhotoRemote {
		t.Errorf("after downloads %+v", got)
	}

	// Concurrent requests for Cecil share one download.
	fake.mu.Lock()
	fake.photoWait = make(chan struct{})
	wait := fake.photoWait
	fake.mu.Unlock()
	var wg sync.WaitGroup
	results := make([]*api.ContactPhotoResult, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = ab.Photo(ctx, api.ContactPhotoParams{ContactID: got["Cecil"].ID})
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(wait)
	wg.Wait()
	fake.mu.Lock()
	fake.photoWait = nil
	if fake.photoGets != 4 {
		t.Errorf("%d downloads after five requests for one contact", fake.photoGets-3)
	}
	fake.mu.Unlock()
	for i, r := range results {
		if r == nil || r.State != api.ContactPhotoStateReady || r.MIME != "image/png" {
			t.Fatalf("request %d: %+v", i, r)
		}
	}

	// Lookup: the ready and the remote ones, not those without.
	lk, err := ab.Lookup(ctx, api.ContactLookupParams{WithPhotos: true,
		Addresses: []string{"ada@example.org", "bob@example.org", "cecil@example.org", "dora@example.org"}})
	if err != nil || len(lk.Known) != 4 || len(lk.Photos) != 2 || lk.Photos["ada@example.org"].State != api.ContactPhotoReady ||
		lk.Photos["cecil@example.org"].ContactID != got["Cecil"].ID {
		t.Fatalf("lookup %+v %v", lk, err)
	}

	// A new revision on the server: remote again after the next pass.
	fake.put(map[string]any{"id": "C1", "displayName": "Ada", "jobTitle": "Countess",
		"emailAddresses": []any{map[string]any{"address": "ada@example.org"}}})
	st, _ := b.contactSync.State(id)
	b.contactSync.SyncNow(id)
	waitLong(t, "a second pass", func() bool { s, _ := b.contactSync.State(id); return s.LastSync.After(st.LastSync) })
	if s := summaries(t, ab, ctx, bookID); s["Ada"].Photo != api.ContactPhotoRemote {
		t.Errorf("after a new revision %+v", s["Ada"])
	}

	// A failed download is an error and stores nothing.
	fake.mu.Lock()
	fake.deny = true
	fake.mu.Unlock()
	if _, err := ab.Photo(ctx, api.ContactPhotoParams{ContactID: got["Ada"].ID}); errCode(t, err) != api.CodeContactsConsentRequired {
		t.Errorf("denied: %v", err)
	}
	if s := summaries(t, ab, ctx, bookID); s["Ada"].Photo != api.ContactPhotoRemote {
		t.Errorf("after a failure %+v", s["Ada"])
	}

	// The wire form.
	raw, _ := json.Marshal(results[0])
	if !strings.HasPrefix(string(raw), `{"state":"ready","mime":"image/png","data":"iVBOR`) {
		t.Errorf("wire %.80s", raw)
	}
}
