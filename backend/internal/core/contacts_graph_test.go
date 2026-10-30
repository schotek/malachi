// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// graphContactsFake is a minimal Graph contacts service: the default
// folder only, a delta with tokens, reads and writes of one contact.
type graphContactsFake struct {
	srv *httptest.Server

	mu      sync.Mutex
	seq     int
	items   map[string]map[string]any
	itemSeq map[string]int
	removed map[string]int
	deny    bool
	patches []map[string]any
}

func newGraphContactsFake(t *testing.T) *graphContactsFake {
	f := &graphContactsFake{items: map[string]map[string]any{}, itemSeq: map[string]int{}, removed: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *graphContactsFake) put(c map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putLocked(c)
}

func (f *graphContactsFake) putLocked(c map[string]any) {
	f.seq++
	id := c["id"].(string)
	c["changeKey"] = fmt.Sprintf("ck-%s-%d", id, f.seq)
	f.items[id], f.itemSeq[id] = c, f.seq
}

func (f *graphContactsFake) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (f *graphContactsFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deny {
		f.reply(w, 403, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied", "message": "denied"}})
		return
	}
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case p == "me/contactFolders":
		f.reply(w, 200, map[string]any{"value": []any{}})
	case p == "me/contacts/delta":
		since := 0
		fmt.Sscan(strings.TrimPrefix(r.URL.Query().Get("token"), "t"), &since)
		var out []any
		for id, c := range f.items {
			if f.itemSeq[id] > since {
				out = append(out, c)
			}
		}
		if since > 0 {
			for id, s := range f.removed {
				if s > since {
					out = append(out, map[string]any{"id": id, "@removed": map[string]any{"reason": "deleted"}})
				}
			}
		}
		f.reply(w, 200, map[string]any{"value": out, "@odata.deltaLink": fmt.Sprintf("%s/me/contacts/delta?token=t%d", f.srv.URL, f.seq)})
	case p == "me/contacts" && r.Method == http.MethodPost:
		body["id"] = fmt.Sprintf("NEW%d", f.seq+1)
		f.putLocked(body)
		f.reply(w, 201, body)
	case strings.HasPrefix(p, "me/contacts/"):
		id := strings.TrimPrefix(p, "me/contacts/")
		c, ok := f.items[id]
		if !ok {
			f.reply(w, 404, map[string]any{"error": map[string]any{"code": "ErrorItemNotFound", "message": "gone"}})
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.reply(w, 200, c)
		case http.MethodPatch:
			f.patches = append(f.patches, body)
			for k, v := range body {
				c[k] = v
			}
			f.putLocked(c)
			f.reply(w, 200, c)
		case http.MethodDelete:
			delete(f.items, id)
			f.seq++
			f.removed[id] = f.seq
			w.WriteHeader(204)
		}
	default:
		f.reply(w, 404, map[string]any{"error": map[string]any{"code": "ResourceNotFound", "message": "no"}})
	}
}

func TestGraphContactsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newGraphContactsFake(t)
	fake.put(map[string]any{"id": "C1", "displayName": "Ada Lovelace", "givenName": "Ada", "surname": "Lovelace",
		"emailAddresses": []any{map[string]any{"address": "ada@example.org"}}, "mobilePhone": "+420 777",
		"businessHomePage": "https://example.org", "birthday": "1604-12-10T11:59:00Z", "unknownField": "x"})
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	sup, out := newFakeSupervisor(), newFakeOutbox()
	b.Supervisor, b.Delivery = sup, out
	fg := &fakeGOA{accounts: []goa.Account{{ID: goaID, ProviderType: goa.ProviderMicrosoft365, Email: "me@contoso.invalid", OAuth2: true, ContactsDisabled: true}},
		tokens: map[string]string{goaID: "tok"}}
	b.GOA = fg
	b.CalendarGraphBaseURL, b.CalendarGraphHTTP = fake.srv.URL, fake.srv.Client()
	rec := &contactsRec{}
	b.SetNotifier(rec)
	b.contactSync = contactsync.New(contactsync.Options{Store: b.store, Providers: b.contactsProvider, Interval: time.Hour,
		Changed: func(accountID string, ids []string) { b.contactsChanged(accountID, ids...) }})
	done := make(chan struct{})
	go func() { b.contactSync.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	cfg := graphConfig()
	cfg.Graph.Contacts = &api.ContactsConfig{Sync: true}
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	id := string(add.AccountID)
	a, _ := b.store.GetAccount(ctx, id)
	b.contactSync.Start(a)
	waitLong(t, "consentRequired", func() bool {
		st, ok := b.contactSync.State(id)
		return ok && st.Status == contactsync.StatusConsentRequired
	})
	ab := b.AddressBooks()
	_, err = ab.ListBooks(ctx, api.AddressBookListParams{AccountID: add.AccountID})
	if errCode(t, err) != api.CodeContactsConsentRequired {
		t.Fatalf("consent: %v", err)
	}
	if d, ok := err.(*api.Error).Data.(api.ContactsConsentRequiredData); !ok || d.SignIn || d.AccountID != add.AccountID {
		t.Fatalf("data %+v", err.(*api.Error).Data)
	}
	if all, err := ab.ListBooks(ctx, api.AddressBookListParams{}); err != nil || len(all.Books) != 1 {
		t.Fatalf("all books without the account: %+v %v", all, err)
	}

	fg.accounts[0].ContactsDisabled = false
	b.contactSync.Restart(a)
	waitLong(t, "a pass", func() bool {
		st, ok := b.contactSync.State(id)
		return ok && !st.LastSync.IsZero()
	})
	books, err := ab.ListBooks(ctx, api.AddressBookListParams{AccountID: add.AccountID})
	if err != nil || len(books.Books) != 1 || books.Books[0].Kind != api.AddressBookKindGraph || books.Books[0].Count != 1 {
		t.Fatalf("books %+v %v", books, err)
	}
	l, _ := ab.ListContacts(ctx, api.ContactListParams{BookID: books.Books[0].ID})
	got, err := ab.GetContact(ctx, api.ContactGetParams{ContactID: l.Contacts[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	c := got.Contact
	if c.DisplayName != "Ada Lovelace" || c.Name.Family != "Lovelace" || len(c.Phones) != 1 || c.Phones[0].Label != "mobile" ||
		c.Birthday == nil || c.Birthday.Year != 0 || c.Birthday.Month != 12 || len(c.URLs) != 1 || c.Revision == "" {
		t.Fatalf("card %+v", c)
	}

	// An update patches only the changed fields.
	c.Title = "Countess"
	up, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: c.ID, Revision: c.Revision, Card: c})
	if err != nil || up.Revision == c.Revision {
		t.Fatalf("update %+v %v", up, err)
	}
	fake.mu.Lock()
	if len(fake.patches) != 1 || len(fake.patches[0]) != 1 || fake.patches[0]["jobTitle"] != "Countess" {
		t.Errorf("patch %+v", fake.patches)
	}
	fake.mu.Unlock()
	// Changed on the server meanwhile: conflict with its version.
	fake.put(map[string]any{"id": "C1", "displayName": "Ada King", "surname": "King"})
	c.Title = "Stale"
	_, err = ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: c.ID, Revision: up.Revision, Card: c})
	if errCode(t, err) != api.CodeContactConflict {
		t.Fatalf("conflict: %v", err)
	}
	if cd, ok := err.(*api.Error).Data.(api.ContactConflictData); !ok || cd.Server.DisplayName != "Ada King" {
		t.Fatalf("conflict data %+v", err.(*api.Error).Data)
	}
	// What Graph cannot hold is refused before a request.
	c2 := api.ContactCard{DisplayName: "Many", Emails: []api.ContactEmail{{Address: "a@x.org"}, {Address: "b@x.org"}, {Address: "c@x.org"}, {Address: "d@x.org"}}}
	if _, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: books.Books[0].ID, Card: c2}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("four addresses: %v", err)
	}
	cr, err := ab.CreateContact(ctx, api.ContactCreateParams{BookID: books.Books[0].ID, Card: api.ContactCard{DisplayName: "Bob", Emails: []api.ContactEmail{{Address: "bob@x.org"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ab.DeleteContact(ctx, api.ContactDeleteParams{ContactID: cr.ContactID, Revision: cr.Revision}); err != nil {
		t.Fatal(err)
	}
	// Gone on the server: removed from the cache, contactNotFound.
	cur, _ := ab.GetContact(ctx, api.ContactGetParams{ContactID: c.ID})
	fake.mu.Lock()
	delete(fake.items, "C1")
	fake.mu.Unlock()
	if _, err := ab.UpdateContact(ctx, api.ContactUpdateParams{ContactID: c.ID, Revision: cur.Contact.Revision, Card: cur.Contact}); errCode(t, err) != api.CodeContactNotFound {
		t.Fatalf("gone: %v", err)
	}
	if _, err := ab.GetContact(ctx, api.ContactGetParams{ContactID: c.ID}); errCode(t, err) != api.CodeContactNotFound {
		t.Fatalf("gone from the cache: %v", err)
	}

	// Contacts sync off: the cache goes.
	cfg.Graph.Contacts.Sync = false
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: add.AccountID, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := b.store.ListAddressBooks(ctx, id); len(bs) != 0 {
		t.Fatalf("books after off %+v", bs)
	}
	waitLong(t, "notify.contactsChanged", func() bool { return rec.count() > 0 })
}

func TestGraphVCardMapping(t *testing.T) {
	card, err := checkContactCard(api.ContactCard{Name: api.ContactName{Given: "A", Family: "B", Prefix: "Dr."},
		Phones:    []api.ContactPhone{{Label: "home", Number: "1"}, {Label: "mobile", Number: "2"}, {Label: "mobile", Number: "3"}, {Label: "fax", Number: "4"}},
		Addresses: []api.ContactAddress{{Label: "work", City: "Brno"}, {Label: "Boat", City: "Sea"}},
		Birthday:  &api.ContactBirthday{Month: 5, Day: 1}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := vcardToGraph(card)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Dr." || f.MobilePhone != "2" || len(f.BusinessPhones) != 2 || f.HomePhones[0] != "1" ||
		f.BusinessAddress.City != "Brno" || f.OtherAddress.City != "Sea" || f.Birthday != "1604-05-01" {
		t.Fatalf("%+v", f)
	}
	back := graphToVCard(f)
	if back.Birthday.Year != 0 || back.Name.Prefix != "Dr." {
		t.Fatalf("%+v", back)
	}
	card.Addresses = append(card.Addresses, card.Addresses[1])
	if _, err := vcardToGraph(card); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("two other addresses: %v", err)
	}
}
