// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav"
	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The address book sources (docs/plans/address-book.md A1, A6, A8):
// CardDAV collections of a caldav account with contacts, and the contact
// folders of a graph account with contacts.sync. Both map their data onto
// the vCard model (vcard.Card); the cache keeps the raw vCard or Graph
// JSON (ab_contacts.raw) so that contact.get and an update start from what
// the server sent.

// contactsDataVersion is the providers' data version
// (contactsync.Versioned): a change re-lists every book.
const contactsDataVersion = 1

// graphUnknownYear is the year Graph needs for a birthday without one
// (Apple's convention); it reads back as no year.
const graphUnknownYear = 1604

// contactsProvider makes the address book source of an account for the
// engine (contactsync.Options.Providers).
func (b *Backend) contactsProvider(ctx context.Context, a store.Account) (contactsync.Provider, error) {
	switch a.Config.Protocol() {
	case api.AccountCalDAV:
		return b.cardDAVProvider(ctx, a)
	case api.AccountGraph:
		return b.graphContactsProvider(ctx, a)
	}
	return nil, api.NewError(api.CodeInvalidArgument, "account %q has no address books", a.ID)
}

// --- CardDAV -----------------------------------------------------------------

// cardDAVContacts is the CardDAV source of a caldav account: the
// account's server, password and pin, the origins its configuration names,
// and one client per origin a book lives on (iCloud and Fastmail keep the
// address books on another host of the same site).
type cardDAVContacts struct {
	b        *Backend
	cd       api.CalDAVConfig
	password func(context.Context) (string, error)

	mu       sync.Mutex
	origins  []string
	home     string
	clients  map[string]*caldav.Client
	versions map[string][]string // book remote id → supported vCard versions
}

var _ contactsync.Provider = (*cardDAVContacts)(nil)
var _ contactsync.Versioned = (*cardDAVContacts)(nil)

func (b *Backend) cardDAVProvider(_ context.Context, a store.Account) (contactsync.Provider, error) {
	cd := a.Config.CalDAV
	if cd == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav account %q without caldav settings", a.ID)
	}
	var mu sync.Mutex
	var cached string
	id := a.ID
	password := func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != "" {
			return cached, nil
		}
		pw, err := b.PasswordFor(ctx, id)
		if err != nil {
			return "", err
		}
		cached = pw
		return pw, nil
	}
	p := &cardDAVContacts{b: b, cd: *cd, password: password, home: cd.AddressBookHomeSetURL,
		origins: calDAVConfiguredOrigins(*cd), clients: map[string]*caldav.Client{}, versions: map[string][]string{}}
	if o, ok := calDAVOrigin(cd.ServerURL, cd.ServerURL); ok && !slices.Contains(p.origins, o) {
		p.origins = append(p.origins, o)
	}
	return p, nil
}

func (p *cardDAVContacts) DataVersion() int { return contactsDataVersion }

// client is the client for the origin of raw (an absolute URL), when the
// account trusts it.
func (p *cardDAVContacts) client(raw string) (*caldav.Client, *url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, nil, api.NewError(api.CodeServerError, "caldav: unusable address book URL")
	}
	origin, ok := calDAVOrigin(p.cd.ServerURL, raw)
	if !ok {
		return nil, nil, caldav.ToAPIError(caldav.ErrOtherOrigin)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[origin]; ok {
		return c, u, nil
	}
	server := p.cd.ServerURL
	if su, err := url.Parse(server); err != nil || su.Scheme+"://"+su.Host != origin {
		server = origin
	}
	origins := slices.Clone(p.origins)
	if !slices.Contains(origins, origin) {
		origins = append(origins, origin)
	}
	su, _ := url.Parse(p.cd.ServerURL)
	c, err := caldav.NewClient(caldav.Options{
		ServerURL: server,
		Username:  p.cd.Username,
		Password:  p.password,
		Origins:   origins,
		// The pin applies to the account's server host only.
		HTTP: p.b.calDAVHTTPClient(su.Host, p.cd.CertificateSHA256),
		Log:  p.b.log,
	})
	if err != nil {
		return nil, nil, err
	}
	p.clients[origin] = c
	return c, u, nil
}

// AddressBooks lists the collections of the address book home set (found
// by a signed-in listing when the configuration has none).
func (p *cardDAVContacts) AddressBooks(ctx context.Context) ([]contactsync.RemoteBook, error) {
	p.mu.Lock()
	home := p.home
	p.mu.Unlock()
	if home == "" {
		pw, err := p.password(ctx)
		if err != nil {
			return nil, err
		}
		l, err := p.b.listCalDAV(ctx, p.cd, pw)
		if err != nil {
			return nil, err
		}
		if l.BookHome == "" {
			return nil, api.NewError(api.CodeServerError, "caldav: the server names no address book home set")
		}
		home = l.BookHome
		p.mu.Lock()
		p.home = home
		p.mu.Unlock()
	}
	c, hu, err := p.client(home)
	if err != nil {
		return nil, err
	}
	books, err := c.ListAddressBooks(ctx, hu.String())
	if err != nil {
		return nil, caldav.ToAPIError(err)
	}
	origin := hu.Scheme + "://" + hu.Host
	out := make([]contactsync.RemoteBook, 0, len(books))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, bk := range books {
		remote := origin + bk.Href
		p.versions[remote] = bk.SupportedVersions
		out = append(out, contactsync.RemoteBook{RemoteID: remote, Name: bk.Name, ReadOnly: bk.ReadOnly})
	}
	return out, nil
}

// cardOf parses a fetched object; ok false for one the cache does not
// keep (gone, too large, not a vCard).
func cardOf(o caldav.Object) (contactsync.Card, bool) {
	if o.Status != 200 || o.TooLarge || len(o.Data) == 0 {
		return contactsync.Card{}, false
	}
	card, err := vcard.Parse(o.Data)
	if err != nil {
		return contactsync.Card{}, false
	}
	return contactsync.Card{RemoteID: o.Href, ETag: o.ETag, Raw: o.Data, RawKind: store.ContactRawVCard, Card: card}, true
}

// Sync brings a book up to date: nothing when its ctag did not change,
// else sync-collection from the stored token (a refused token starts
// over), or, for a server without it, the ETags of every member; changed
// cards are fetched with addressbook-multiget.
func (p *cardDAVContacts) Sync(ctx context.Context, book contactsync.StoredBook, known map[string]string, sink contactsync.Sink) (contactsync.SyncState, error) {
	c, u, err := p.client(book.RemoteID)
	if err != nil {
		return contactsync.SyncState{}, err
	}
	coll := u.String()
	st, err := c.State(ctx, coll)
	if err != nil {
		return contactsync.SyncState{}, caldav.ToAPIError(err)
	}
	if st.CTag != "" && st.CTag == book.CTag && (book.SyncToken != "" || !st.SupportsSync) && len(known) > 0 {
		return contactsync.SyncState{Token: book.SyncToken, CTag: st.CTag}, nil
	}
	// A pass fetches at most as many cards as a book keeps: a server that
	// keeps naming new hrefs does not keep the pass going.
	room := api.MaxContactsPerBook
	fetch := func(res []caldav.Resource, full bool) error {
		var hrefs []string
		for _, r := range res {
			if n := caldav.NormaliseHref(r.Href); n != "" {
				r.Href = n
			}
			if full {
				sink.Seen(r.Href)
			}
			if r.ETag != "" && known[r.Href] == r.ETag {
				sink.Seen(r.Href)
				continue
			}
			if room > 0 {
				room--
				hrefs = append(hrefs, r.Href)
			}
		}
		if len(hrefs) == 0 {
			return nil
		}
		objs, err := c.MultigetCards(ctx, coll, hrefs)
		if err != nil {
			return caldav.ToAPIError(err)
		}
		for _, o := range objs {
			if o.Status == 404 {
				if err := sink.Remove(o.Href); err != nil {
					return err
				}
				continue
			}
			card, ok := cardOf(o)
			if !ok {
				continue
			}
			if err := sink.Upsert(card); err != nil {
				return err
			}
		}
		return nil
	}
	if st.SupportsSync {
		token, full := book.SyncToken, book.SyncToken == ""
		for range 100 {
			res, err := c.SyncCollection(ctx, coll, token)
			if errors.Is(err, caldav.ErrInvalidSyncToken) && token != "" {
				token, full = "", true
				continue
			}
			if err != nil {
				return contactsync.SyncState{}, caldav.ToAPIError(err)
			}
			if err := fetch(res.Changed, full); err != nil {
				return contactsync.SyncState{}, err
			}
			for _, h := range res.Removed {
				if n := caldav.NormaliseHref(h); n != "" {
					h = n
				}
				if err := sink.Remove(h); err != nil {
					return contactsync.SyncState{}, err
				}
			}
			token = res.Token
			if !res.Truncated {
				// A truncated initial listing is complete only at its end.
				return contactsync.SyncState{Token: token, CTag: st.CTag, Full: full}, nil
			}
		}
		return contactsync.SyncState{}, api.NewError(api.CodeServerError, "caldav: sync-collection never ended")
	}
	res, err := c.ListETags(ctx, coll)
	if err != nil {
		return contactsync.SyncState{}, caldav.ToAPIError(err)
	}
	if err := fetch(res, true); err != nil {
		return contactsync.SyncState{}, err
	}
	return contactsync.SyncState{CTag: st.CTag, Full: true}, nil
}

// cardURL is the absolute URL of a card of the book.
func cardURL(book contactsync.StoredBook, href string) string {
	u, err := url.Parse(book.RemoteID)
	if err != nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return u.ResolveReference(ref).String()
}

// writeError maps a CardDAV failure of a write: 412 is a conflict with the
// server's version, 404/410 ErrGone, 403 addressBookReadOnly.
func (p *cardDAVContacts) writeError(ctx context.Context, c *caldav.Client, book contactsync.StoredBook, href string, err error) error {
	switch {
	case caldav.IsConflict(err):
		cur, gerr := p.get(ctx, c, book, href)
		if errors.Is(gerr, contactsync.ErrGone) {
			return contactsync.ErrGone
		}
		return &contactsync.ConflictError{Server: cur}
	case caldav.IsGone(err):
		return contactsync.ErrGone
	case caldav.IsForbidden(err):
		return api.NewError(api.CodeAddressBookReadOnly, "the server does not let the user change this address book")
	}
	return caldav.ToAPIError(err)
}

func (p *cardDAVContacts) get(ctx context.Context, c *caldav.Client, book contactsync.StoredBook, href string) (contactsync.Card, error) {
	o, err := c.GetCard(ctx, cardURL(book, href))
	if caldav.IsGone(err) {
		return contactsync.Card{}, contactsync.ErrGone
	}
	if err != nil {
		return contactsync.Card{}, caldav.ToAPIError(err)
	}
	o.Href = href
	card, ok := cardOf(o)
	if !ok {
		return contactsync.Card{}, api.NewError(api.CodeServerError, "caldav: the contact cannot be read")
	}
	return card, nil
}

func (p *cardDAVContacts) Get(ctx context.Context, book contactsync.StoredBook, remoteID string) (contactsync.Card, error) {
	c, _, err := p.client(book.RemoteID)
	if err != nil {
		return contactsync.Card{}, err
	}
	return p.get(ctx, c, book, remoteID)
}

// version is the vCard version of a new card of the book: 3.0 where the
// server takes it (iCloud takes nothing else), else 4.0.
func (p *cardDAVContacts) version(book contactsync.StoredBook) string {
	p.mu.Lock()
	vs, ok := p.versions[book.RemoteID]
	p.mu.Unlock()
	if !ok || len(vs) == 0 || slices.Contains(vs, "3.0") {
		return "3.0"
	}
	return "4.0"
}

func (p *cardDAVContacts) Create(ctx context.Context, book contactsync.StoredBook, card *vcard.Card) (contactsync.Card, error) {
	c, u, err := p.client(book.RemoteID)
	if err != nil {
		return contactsync.Card{}, err
	}
	data := vcard.New(card, p.version(book), caldav.NewUID())
	href, etag, err := c.CreateCard(ctx, u.String(), data)
	if err != nil {
		if caldav.IsForbidden(err) {
			return contactsync.Card{}, api.NewError(api.CodeAddressBookReadOnly, "the server does not let the user change this address book")
		}
		return contactsync.Card{}, caldav.ToAPIError(err)
	}
	if etag == "" {
		return p.get(ctx, c, book, href)
	}
	parsed, err := vcard.Parse(data)
	if err != nil {
		return contactsync.Card{}, api.NewError(api.CodeStorageError, "vcard: %v", err)
	}
	return contactsync.Card{RemoteID: href, ETag: etag, Raw: data, RawKind: store.ContactRawVCard, Card: parsed}, nil
}

func (p *cardDAVContacts) Update(ctx context.Context, book contactsync.StoredBook, stored contactsync.StoredContact, before, after *vcard.Card) (contactsync.Card, error) {
	c, _, err := p.client(book.RemoteID)
	if err != nil {
		return contactsync.Card{}, err
	}
	data, err := vcard.Patch(stored.Raw, before, after)
	if err != nil {
		return contactsync.Card{}, api.NewError(api.CodeServerError, "the stored vCard cannot be changed: %s", errors.Unwrap(err))
	}
	if len(data) > caldav.MaxCardBytes {
		return contactsync.Card{}, api.NewError(api.CodeInvalidArgument, "the contact would be over %d bytes", caldav.MaxCardBytes)
	}
	if bytes.Equal(data, stored.Raw) {
		card, _ := vcard.Parse(stored.Raw)
		return contactsync.Card{RemoteID: stored.RemoteID, ETag: stored.ETag, Raw: stored.Raw, RawKind: store.ContactRawVCard, Card: card}, nil
	}
	etag, err := c.PutCard(ctx, cardURL(book, stored.RemoteID), data, stored.ETag)
	if err != nil {
		return contactsync.Card{}, p.writeError(ctx, c, book, stored.RemoteID, err)
	}
	if etag == "" {
		return p.get(ctx, c, book, stored.RemoteID)
	}
	parsed, err := vcard.Parse(data)
	if err != nil {
		return contactsync.Card{}, api.NewError(api.CodeStorageError, "vcard: %v", err)
	}
	return contactsync.Card{RemoteID: stored.RemoteID, ETag: etag, Raw: data, RawKind: store.ContactRawVCard, Card: parsed}, nil
}

func (p *cardDAVContacts) Delete(ctx context.Context, book contactsync.StoredBook, stored contactsync.StoredContact) error {
	c, _, err := p.client(book.RemoteID)
	if err != nil {
		return err
	}
	if err := c.Delete(ctx, cardURL(book, stored.RemoteID), stored.ETag); err != nil {
		return p.writeError(ctx, c, book, stored.RemoteID, err)
	}
	return nil
}

// --- Microsoft Graph ---------------------------------------------------------

// graphContacts is the contacts source of a graph account: its contact
// folders through the account's token and limiter.
type graphContacts struct {
	accountID api.AccountID
	signIn    bool
	client    *graph.Client
}

var _ contactsync.Provider = (*graphContacts)(nil)
var _ contactsync.Versioned = (*graphContacts)(nil)

// graphContactsProvider: the account's sign-in must allow the contacts —
// the scope its grant recorded (source daemon, ContactsConsent) or the
// Contacts switch of its GNOME Online Accounts entry — else
// contactsConsentRequired. The client shares the account's token and
// limiter with its mail and calendar.
func (b *Backend) graphContactsProvider(ctx context.Context, a store.Account) (contactsync.Provider, error) {
	g := a.Config.Graph
	if g == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "graph account %q without graph settings", a.ID)
	}
	signIn := g.Source == api.GraphSourceDaemon
	allowed := true
	switch g.Source {
	case api.GraphSourceDaemon:
		ok, err := b.ContactsConsent(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		allowed = ok
	case api.GraphSourceGOA:
		disabled, err := b.goaContactsDisabled(ctx, g.GOAAccountID)
		if err != nil {
			return nil, err
		}
		allowed = !disabled
	}
	if !allowed {
		return nil, contactsConsentError(api.AccountID(a.ID), signIn)
	}
	id := a.ID
	client := graph.NewClient(graph.Options{
		BaseURL:    b.CalendarGraphBaseURL,
		HTTP:       b.CalendarGraphHTTP,
		Token:      func(ctx context.Context) (string, error) { return b.GraphTokenFor(ctx, id) },
		Invalidate: func() { b.invalidateGraphTokenFor(id) },
		Log:        b.log,
		Limiter:    b.graphLimiter(id),
	})
	return &graphContacts{accountID: api.AccountID(id), signIn: signIn, client: client}, nil
}

func contactsConsentError(accountID api.AccountID, signIn bool) error {
	e := api.NewError(api.CodeContactsConsentRequired, "the account's sign-in does not allow the contacts")
	e.Data = api.ContactsConsentRequiredData{AccountID: accountID, SignIn: signIn}
	return e
}

func (p *graphContacts) DataVersion() int { return contactsDataVersion }

// mapError maps the client's contact errors onto the engine's.
func (p *graphContacts) mapError(err error) error {
	var ce *graph.ContactConflictError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		card, _ := graphCard(ce.Server)
		return &contactsync.ConflictError{Server: card}
	case errors.Is(err, graph.ErrContactsAccessDenied):
		return contactsConsentError(p.accountID, p.signIn)
	case errors.Is(err, graph.ErrContactGone):
		return contactsync.ErrGone
	}
	return graph.ToAPIError(err)
}

func (p *graphContacts) AddressBooks(ctx context.Context) ([]contactsync.RemoteBook, error) {
	folders, err := p.client.ContactFolders(ctx)
	if err != nil {
		return nil, p.mapError(err)
	}
	out := make([]contactsync.RemoteBook, 0, len(folders))
	for _, f := range folders {
		out = append(out, contactsync.RemoteBook{RemoteID: f.ID, Name: f.Name})
	}
	return out, nil
}

// graphCard is the card of a Graph contact; ok false when its JSON was not
// kept (over 1 MiB).
func graphCard(c graph.Contact) (contactsync.Card, bool) {
	if c.Raw == nil {
		return contactsync.Card{}, false
	}
	return contactsync.Card{RemoteID: c.ID, ETag: c.ChangeKey, Raw: c.Raw, RawKind: store.ContactRawGraph, Card: graphToVCard(c.ContactFields)}, true
}

func (p *graphContacts) Sync(ctx context.Context, book contactsync.StoredBook, known map[string]string, sink contactsync.Sink) (contactsync.SyncState, error) {
	full := book.DeltaLink == ""
	d, err := p.client.DeltaContacts(ctx, book.RemoteID, book.DeltaLink, func(pg graph.ContactPage) error {
		full = full || pg.Full
		for _, c := range pg.Changed {
			if k, ok := known[c.ID]; ok && k == c.ChangeKey && c.ChangeKey != "" {
				sink.Seen(c.ID)
				continue
			}
			card, ok := graphCard(c)
			if !ok {
				continue
			}
			if err := sink.Upsert(card); err != nil {
				return err
			}
		}
		for _, id := range pg.Removed {
			if err := sink.Remove(id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			return contactsync.SyncState{}, ae
		}
		return contactsync.SyncState{}, p.mapError(err)
	}
	return contactsync.SyncState{DeltaLink: d.DeltaLink, Full: full || d.Full}, nil
}

func (p *graphContacts) Get(ctx context.Context, _ contactsync.StoredBook, remoteID string) (contactsync.Card, error) {
	c, err := p.client.GetContact(ctx, remoteID)
	if err != nil {
		return contactsync.Card{}, p.mapError(err)
	}
	card, ok := graphCard(c)
	if !ok {
		return contactsync.Card{}, api.NewError(api.CodeServerError, "graph: the contact is too large to keep")
	}
	return card, nil
}

func (p *graphContacts) Create(ctx context.Context, book contactsync.StoredBook, card *vcard.Card) (contactsync.Card, error) {
	f, err := vcardToGraph(card)
	if err != nil {
		return contactsync.Card{}, err
	}
	c, err := p.client.CreateContact(ctx, book.RemoteID, f)
	if err != nil {
		return contactsync.Card{}, p.mapError(err)
	}
	out, ok := graphCard(c)
	if !ok {
		return contactsync.Card{}, api.NewError(api.CodeServerError, "graph: the contact is too large to keep")
	}
	return out, nil
}

func (p *graphContacts) Update(ctx context.Context, _ contactsync.StoredBook, stored contactsync.StoredContact, before, after *vcard.Card) (contactsync.Card, error) {
	bf, err := vcardToGraph(before)
	if err != nil {
		bf = graph.ContactFields{}
	}
	af, err := vcardToGraph(after)
	if err != nil {
		return contactsync.Card{}, err
	}
	if stored.RawKind == store.ContactRawGraph {
		if cur, perr := graph.ParseContact(stored.Raw); perr == nil {
			keepEmailNames(&bf, cur.Emails)
			keepEmailNames(&af, cur.Emails)
		}
	}
	patch, err := graph.ContactPatch(bf, af)
	if err != nil {
		return contactsync.Card{}, err
	}
	c, err := p.client.UpdateContact(ctx, stored.RemoteID, stored.ETag, patch)
	if err != nil {
		return contactsync.Card{}, p.mapError(err)
	}
	out, ok := graphCard(c)
	if !ok {
		return contactsync.Card{}, api.NewError(api.CodeServerError, "graph: the contact is too large to keep")
	}
	return out, nil
}

func (p *graphContacts) Delete(ctx context.Context, _ contactsync.StoredBook, stored contactsync.StoredContact) error {
	return p.mapError(p.client.DeleteContact(ctx, stored.RemoteID, stored.ETag))
}

// keepEmailNames gives each address of f the name the stored contact has
// for it (the model has no per-address name): a changed list sent whole
// keeps the names of the addresses that stay.
func keepEmailNames(f *graph.ContactFields, stored []graph.ContactEmail) {
	for i, e := range f.Emails {
		for _, s := range stored {
			if strings.EqualFold(s.Address, e.Address) {
				f.Emails[i].Name = s.Name
				break
			}
		}
	}
}

// graphToVCard maps Graph's fields onto the model: title and generation
// are the name's prefix and suffix, business, home and mobile phones and
// addresses take the work, home, mobile and other labels, the business
// home page is a work URL.
func graphToVCard(f graph.ContactFields) *vcard.Card {
	c := &vcard.Card{
		Name:        vcard.Name{Given: f.GivenName, Family: f.Surname, Middle: f.MiddleName, Prefix: f.Title, Suffix: f.Generation},
		DisplayName: f.DisplayName, Nickname: f.NickName, Org: f.CompanyName, Title: f.JobTitle, Note: f.PersonalNotes,
	}
	for _, e := range f.Emails {
		c.Emails = append(c.Emails, vcard.Email{Address: e.Address})
	}
	for _, p := range f.BusinessPhones {
		c.Phones = append(c.Phones, vcard.Phone{Label: vcard.LabelWork, Number: p})
	}
	for _, p := range f.HomePhones {
		c.Phones = append(c.Phones, vcard.Phone{Label: vcard.LabelHome, Number: p})
	}
	if f.MobilePhone != "" {
		c.Phones = append(c.Phones, vcard.Phone{Label: vcard.LabelMobile, Number: f.MobilePhone})
	}
	for _, a := range []struct {
		label string
		addr  graph.ContactAddress
	}{{vcard.LabelWork, f.BusinessAddress}, {vcard.LabelHome, f.HomeAddress}, {vcard.LabelOther, f.OtherAddress}} {
		if !a.addr.IsZero() {
			c.Addresses = append(c.Addresses, vcard.Address{Label: a.label, Street: a.addr.Street, City: a.addr.City,
				Region: a.addr.State, Postcode: a.addr.PostalCode, Country: a.addr.CountryOrRegion})
		}
	}
	if f.BusinessHomePage != "" {
		c.URLs = append(c.URLs, vcard.URL{Label: vcard.LabelWork, URL: f.BusinessHomePage})
	}
	if t, err := time.Parse(time.DateOnly, f.Birthday); err == nil {
		y := t.Year()
		if y == graphUnknownYear {
			y = 0
		}
		c.Birthday = vcard.Birthday{Year: y, Month: int(t.Month()), Day: t.Day()}
	}
	return vcard.Clean(c)
}

// vcardToGraph maps the model onto Graph's fields: at most 3 addresses
// (Graph's own limit), phones by label (home, mobile — the first; every
// other number is a business phone), one address each of work, home and
// other (other and custom labels share Graph's other address), one URL.
// What Graph cannot hold is invalidArgument naming the field.
func vcardToGraph(card *vcard.Card) (graph.ContactFields, error) {
	c := vcard.Clean(card)
	f := graph.ContactFields{
		DisplayName: vcard.DisplayNameOf(c), GivenName: c.Name.Given, Surname: c.Name.Family, MiddleName: c.Name.Middle,
		Title: c.Name.Prefix, Generation: c.Name.Suffix, NickName: c.Nickname, CompanyName: c.Org, JobTitle: c.Title,
		PersonalNotes: c.Note,
	}
	if len(c.Emails) > graph.MaxContactEmails {
		return f, cardFieldError("emails", "a Microsoft contact has at most %d e-mail addresses", graph.MaxContactEmails)
	}
	for _, e := range c.Emails {
		f.Emails = append(f.Emails, graph.ContactEmail{Address: e.Address})
	}
	for _, p := range c.Phones {
		switch {
		case p.Label == vcard.LabelHome:
			f.HomePhones = append(f.HomePhones, p.Number)
		case p.Label == vcard.LabelMobile && f.MobilePhone == "":
			f.MobilePhone = p.Number
		default:
			f.BusinessPhones = append(f.BusinessPhones, p.Number)
		}
	}
	if len(f.HomePhones) > graph.MaxContactPhones || len(f.BusinessPhones) > graph.MaxContactPhones {
		return f, cardFieldError("phones", "a Microsoft contact has at most %d numbers of a kind", graph.MaxContactPhones)
	}
	set := map[string]bool{}
	for i, a := range c.Addresses {
		ga := graph.ContactAddress{Street: a.Street, City: a.City, State: a.Region, PostalCode: a.Postcode, CountryOrRegion: a.Country}
		slot := vcard.LabelOther
		switch a.Label {
		case vcard.LabelWork, vcard.LabelHome:
			slot = a.Label
		}
		if set[slot] {
			return f, cardFieldError(fmt.Sprintf("addresses[%d]", i), "a Microsoft contact has one %s address", slot)
		}
		set[slot] = true
		switch slot {
		case vcard.LabelWork:
			f.BusinessAddress = ga
		case vcard.LabelHome:
			f.HomeAddress = ga
		default:
			f.OtherAddress = ga
		}
	}
	switch len(c.URLs) {
	case 0:
	case 1:
		f.BusinessHomePage = c.URLs[0].URL
	default:
		return f, cardFieldError("urls", "a Microsoft contact has one web address")
	}
	if b := c.Birthday; !b.IsZero() {
		y := b.Year
		if y == 0 {
			y = graphUnknownYear
		}
		f.Birthday = fmt.Sprintf("%04d-%02d-%02d", y, b.Month, b.Day)
	}
	return f, nil
}

// storedCard is the model of a stored contact, read from its raw vCard or
// Graph JSON.
func storedCard(c store.StoredContact) (*vcard.Card, error) {
	switch c.RawKind {
	case store.ContactRawVCard:
		return vcard.Parse(c.Raw)
	case store.ContactRawGraph:
		g, err := graph.ParseContact(c.Raw)
		if err != nil {
			return nil, err
		}
		return graphToVCard(g.ContactFields), nil
	}
	return nil, errors.New("unknown raw kind " + strconv.Quote(c.RawKind))
}
