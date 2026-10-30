// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/contactsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The address books of connected services (docs/api.md §4.19,
// docs/plans/address-book.md): addressBook.* and contact.*. The cache is
// the store's (ab_*), kept by the engine (internal/contactsync); writes go
// to the server first (the provider) and change the cache only after the
// server accepted them. The daemon logs counts and ids only, never a name,
// an address or a card.

const (
	// contactsNotifyCoalesce is how long notify.contactsChanged gathers.
	contactsNotifyCoalesce = 250 * time.Millisecond
	// contactsStaleAge: addressBook.list asks for a pass when the last one
	// is older.
	contactsStaleAge = time.Minute
	// recentContactPrefix is the prefix of the ids of recent-book
	// contacts (the address, hex-encoded).
	recentContactPrefix = "r_"
)

// AddressBooks is the address book service.
func (b *Backend) AddressBooks() api.AddressBookService { return &addressBookService{b} }

type addressBookService struct{ b *Backend }

var _ api.AddressBookService = (*addressBookService)(nil)

// --- notifications -----------------------------------------------------------

// contactsNotify gathers notify.contactsChanged: the books of each account
// ("" key = the recent book's), "" for any book of the account.
type contactsNotify struct {
	mu      sync.Mutex
	changed map[string]map[string]bool
	timer   *time.Timer
}

// contactsChanged queues notify.contactsChanged for the books of an
// account (none: any book of it; accountID "" = the recent book).
func (b *Backend) contactsChanged(accountID string, bookIDs ...string) {
	cn := &b.cn
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.changed == nil {
		cn.changed = map[string]map[string]bool{}
	}
	set := cn.changed[accountID]
	if set == nil {
		set = map[string]bool{}
		cn.changed[accountID] = set
	}
	if len(bookIDs) == 0 {
		set[""] = true
	}
	for _, id := range bookIDs {
		set[id] = true
	}
	if cn.timer == nil {
		cn.timer = time.AfterFunc(contactsNotifyCoalesce, b.flushContactsChanged)
	}
}

// collectedChanged is notify.contactsChanged for the recent book: the
// outbox recorded a delivered mail's recipients.
func (b *Backend) collectedChanged() { b.contactsChanged("") }

func (b *Backend) flushContactsChanged() {
	cn := &b.cn
	cn.mu.Lock()
	changed := cn.changed
	cn.changed, cn.timer = nil, nil
	cn.mu.Unlock()
	n, ok := b.getNotifier().(api.ContactsNotifier)
	if !ok {
		return
	}
	accounts := make([]string, 0, len(changed))
	for acc := range changed {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	for _, acc := range accounts {
		ev := api.ContactsChangedNotification{AccountID: api.AccountID(acc)}
		if acc == "" {
			ev.BookIDs = []api.AddressBookID{api.AddressBookRecent}
		} else if !changed[acc][""] {
			ids := make([]string, 0, len(changed[acc]))
			for id := range changed[acc] {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				ev.BookIDs = append(ev.BookIDs, api.AddressBookID(id))
			}
		}
		n.ContactsChanged(ev)
	}
}

// --- the engine's surroundings -----------------------------------------------

// withContacts runs the address books of a kind beside its supervisor:
// started, stopped and restarted with it (contactsync.HasContacts decides).
// Trigger asks for a contacts pass too when triggers is set (the caldav
// kind, whose accounts may have address books only).
type withContacts struct {
	SyncSupervisor
	contacts *contactsync.Supervisor
	triggers bool
}

var _ SyncSupervisor = (*withContacts)(nil)

func (w *withContacts) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.SyncSupervisor.Run(ctx) }()
	go func() { defer wg.Done(); w.contacts.Run(ctx) }()
	wg.Wait()
}

func (w *withContacts) Start(a store.Account) {
	w.SyncSupervisor.Start(a)
	w.contacts.Start(a)
}

func (w *withContacts) Stop(accountID string) {
	w.SyncSupervisor.Stop(accountID)
	w.contacts.Stop(accountID)
}

func (w *withContacts) Restart(a store.Account) {
	w.SyncSupervisor.Restart(a)
	w.contacts.Restart(a)
}

func (w *withContacts) Trigger(accountID string, folder api.FolderID, full bool) bool {
	ok := w.SyncSupervisor.Trigger(accountID, folder, full)
	if w.triggers && folder == "" {
		ok = w.contacts.SyncNow(accountID) || ok
	}
	return ok
}

// TriggerIssue and RefreshIssueWait are the inner supervisor's, if any.
func (w *withContacts) TriggerIssue(accountID, key string) bool {
	is, ok := w.SyncSupervisor.(IssueSupervisor)
	return ok && is.TriggerIssue(accountID, key)
}

func (w *withContacts) RefreshIssueWait(ctx context.Context, accountID, key string, d time.Duration) error {
	if is, ok := w.SyncSupervisor.(IssueSupervisor); ok {
		return is.RefreshIssueWait(ctx, accountID, key, d)
	}
	return api.NewError(api.CodeUnavailable, "account %s has no issues to refresh", accountID)
}

// contactsCacheDropped compares two configurations of an account: whether
// its cached address books go (contacts switched off, the kind, server or
// user changed).
func contactsCacheDropped(old, next api.AccountConfig) bool {
	if !contactsync.ContactsOn(old) {
		return false
	}
	switch {
	case !contactsync.ContactsOn(next), old.Protocol() != next.Protocol():
		return true
	case next.Protocol() == api.AccountCalDAV:
		return !sameCalDAVLogin(old, next)
	case next.Protocol() == api.AccountGraph:
		return store.NormalizeAddress(old.Email) != store.NormalizeAddress(next.Email)
	}
	return false
}

// dropContactsCache deletes the cached address books an account update no
// longer covers; the account's contacts syncer is stopped first, the
// caller restarts it.
func (b *Backend) dropContactsCache(ctx context.Context, old, next store.Account) {
	if b.contactSync == nil || !contactsCacheDropped(old.Config, next.Config) {
		return
	}
	b.contactSync.Stop(old.ID)
	ids, err := b.store.DeleteAccountAddressBooks(ctx, old.ID)
	if err != nil {
		b.log.Warn("delete account address books", "account", old.ID, "err", err)
		return
	}
	b.log.Info("contacts cache dropped", "account", old.ID, "books", len(ids))
	b.contactsChanged(old.ID)
}

// --- addressBook.* -------------------------------------------------------------

// contactAccounts are the enabled accounts whose contacts are on, in
// account.list order.
func (b *Backend) contactAccounts(ctx context.Context) ([]store.Account, error) {
	all, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	var out []store.Account
	for _, a := range all {
		if contactsync.HasContacts(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func bookKind(a store.Account) api.AddressBookKind {
	if a.Config.Protocol() == api.AccountGraph {
		return api.AddressBookKindGraph
	}
	return api.AddressBookKindCardDAV
}

func apiBook(a store.Account, bk store.AddressBook) api.AddressBook {
	out := api.AddressBook{ID: api.AddressBookID(bk.ID), AccountID: api.AccountID(a.ID), Name: bk.Name, Kind: bookKind(a),
		ReadOnly: bk.ReadOnly, Visible: bk.Visible, Count: bk.Count}
	if !bk.LastSyncAt.IsZero() {
		t := bk.LastSyncAt.UTC()
		out.LastSyncAt = &t
	}
	return out
}

// consentError is the account's contactsConsentRequired when its syncer
// waits for consent, nil otherwise.
func (b *Backend) contactsConsentState(accountID string) *api.Error {
	if b.contactSync == nil {
		return nil
	}
	st, ok := b.contactSync.State(accountID)
	if ok && st.Status == contactsync.StatusConsentRequired && st.Error != nil {
		return st.Error
	}
	return nil
}

func (s *addressBookService) ListBooks(ctx context.Context, p api.AddressBookListParams) (*api.AddressBookListResult, error) {
	b := s.b
	var accounts []store.Account
	if p.AccountID != "" {
		a, err := b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if contactsync.HasContacts(a) {
			accounts = []store.Account{a}
		}
	} else {
		var err error
		if accounts, err = b.contactAccounts(ctx); err != nil {
			return nil, err
		}
	}
	out := &api.AddressBookListResult{Books: []api.AddressBook{}}
	for _, a := range accounts {
		if e := b.contactsConsentState(a.ID); e != nil {
			if p.AccountID != "" {
				return nil, e
			}
			continue
		}
		if b.contactSync != nil {
			b.contactSync.SyncIfStale(a.ID, contactsStaleAge)
		}
		books, err := b.store.ListAddressBooks(ctx, a.ID)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, bk := range books {
			out.Books = append(out.Books, apiBook(a, bk))
		}
	}
	if p.AccountID == "" {
		n, err := b.store.CountCollectedAddresses(ctx)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		out.Books = append(out.Books, api.AddressBook{ID: api.AddressBookRecent, Name: "", Kind: api.AddressBookKindRecent,
			ReadOnly: true, Visible: true, Count: n})
	}
	return out, nil
}

// book returns a stored book with its account (contacts on); a book of an
// account whose contacts are off or that is disabled is not found.
func (b *Backend) book(ctx context.Context, id api.AddressBookID) (store.AddressBook, store.Account, error) {
	bk, err := b.store.GetAddressBook(ctx, string(id))
	if errors.Is(err, store.ErrNotFound) {
		return bk, store.Account{}, api.NewError(api.CodeAddressBookNotFound, "unknown address book %q", id)
	}
	if err != nil {
		return bk, store.Account{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	a, err := b.store.GetAccount(ctx, bk.AccountID)
	if err != nil || !contactsync.HasContacts(a) {
		return bk, store.Account{}, api.NewError(api.CodeAddressBookNotFound, "unknown address book %q", id)
	}
	return bk, a, nil
}

func (s *addressBookService) SetBook(ctx context.Context, p api.AddressBookSetParams) (*api.AddressBookSetResult, error) {
	b := s.b
	if p.BookID == api.AddressBookRecent {
		return nil, api.NewError(api.CodeInvalidArgument, "the recent book is always shown")
	}
	bk, a, err := b.book(ctx, p.BookID)
	if err != nil {
		return nil, err
	}
	if bk.Visible != p.Visible {
		if err := b.store.SetAddressBookVisible(ctx, bk.ID, p.Visible); err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		if p.Visible {
			if b.contactSync != nil {
				b.contactSync.SyncNow(a.ID)
			}
		} else if err := b.store.DeleteAddressBookContents(ctx, bk.ID); err != nil {
			// A hidden book is not synchronised: its cards go now.
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		b.log.Info("address book visibility", "book", bk.ID, "visible", p.Visible)
		b.contactsChanged(a.ID, bk.ID)
	}
	bk, err = b.store.GetAddressBook(ctx, bk.ID)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.AddressBookSetResult{Book: apiBook(a, bk)}, nil
}

// --- contact.list, contact.get, contact.lookup -----------------------------------

// visibleBookIDs are the ids of the visible books of the accounts (every
// account whose contacts are on when accountID is "").
func (b *Backend) visibleBookIDs(ctx context.Context, accountID string) ([]string, map[string]string, error) {
	accounts, err := b.contactAccounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	names := map[string]string{}
	for _, a := range accounts {
		if accountID != "" && a.ID != accountID {
			continue
		}
		books, err := b.store.ListAddressBooks(ctx, a.ID)
		if err != nil {
			return nil, nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, bk := range books {
			if bk.Visible {
				ids = append(ids, bk.ID)
				names[bk.ID] = bk.Name
			}
		}
	}
	return ids, names, nil
}

func checkContactQuery(q string) error {
	switch {
	case len(q) > api.MaxContactQueryBytes:
		return api.NewError(api.CodeInvalidArgument, "query longer than %d bytes", api.MaxContactQueryBytes)
	case !utf8.ValidString(q) || hasControl(q):
		return api.NewError(api.CodeInvalidArgument, "query contains control or invalid characters")
	}
	return nil
}

func (s *addressBookService) ListContacts(ctx context.Context, p api.ContactListParams) (*api.ContactListResult, error) {
	b := s.b
	q := strings.TrimSpace(p.Query)
	if err := checkContactQuery(q); err != nil {
		return nil, err
	}
	if p.Offset < 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "offset must not be negative")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = api.DefaultContactListLimit
	}
	limit = min(limit, api.MaxContactListLimit)
	out := &api.ContactListResult{Contacts: []api.ContactSummary{}}
	switch p.BookID {
	case api.AddressBookRecent:
		rows, total, err := b.store.ListCollectedAddresses(ctx, q, p.Offset, limit)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		out.Total = total
		for _, c := range rows {
			out.Contacts = append(out.Contacts, recentSummary(c))
		}
		return out, nil
	case "":
		return nil, api.NewError(api.CodeInvalidArgument, "bookId is required")
	}
	var ids []string
	if p.BookID == api.AddressBookAll {
		var err error
		if ids, _, err = b.visibleBookIDs(ctx, ""); err != nil {
			return nil, err
		}
	} else {
		bk, _, err := b.book(ctx, p.BookID)
		if err != nil {
			return nil, err
		}
		ids = []string{bk.ID}
	}
	rows, total, err := b.store.ListContacts(ctx, store.ContactQuery{BookIDs: ids, Query: q, Offset: p.Offset, Limit: limit})
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out.Total = total
	for _, c := range rows {
		out.Contacts = append(out.Contacts, api.ContactSummary{ID: api.ContactID(c.ID), BookID: api.AddressBookID(c.BookID),
			DisplayName: c.DisplayName, SortKey: c.SortKey, Section: c.Section, Org: c.Org, Email: c.PrimaryEmail})
	}
	return out, nil
}

// recentCard is the model of a collected address.
func recentCard(c store.CollectedAddress) *vcard.Card {
	return vcard.Clean(&vcard.Card{DisplayName: c.Name, Emails: []vcard.Email{{Address: c.Address}}})
}

func recentID(address string) api.ContactID {
	return api.ContactID(recentContactPrefix + hex.EncodeToString([]byte(address)))
}

func recentSummary(c store.CollectedAddress) api.ContactSummary {
	card := recentCard(c)
	key := vcard.SortKey(card)
	return api.ContactSummary{ID: recentID(c.Address), BookID: api.AddressBookRecent, DisplayName: vcard.DisplayNameOf(card),
		SortKey: key, Section: vcard.Section(key), Email: c.Address}
}

// shownCard is the card as the API shows it: cleaned, without the e-mail
// addresses that are not plain addresses (a server's junk). An update
// compares with the same form, so such a line stays on the server as it
// is.
func shownCard(card *vcard.Card) *vcard.Card {
	c := vcard.Clean(card)
	emails := c.Emails[:0:0]
	for _, e := range c.Emails {
		if contactsync.NormalizeAddress(e.Address) != "" {
			emails = append(emails, e)
		}
	}
	c.Emails = emails
	return c
}

// apiShownCard is shownCard within the API's caps: at most
// MaxContactEmails, …Phones, …Addresses and …URLs items, the name parts,
// display name and nickname within MaxContactNameBytes and a web address
// within MaxContactFieldBytes. It is exactly what contact.get shows, so
// contact.update compares the client's card with it: an item or a tail
// the client never saw is not in the edit and stays on the server as it
// is, and a field it sends back unchanged passes checkContactCard.
func apiShownCard(card *vcard.Card) *vcard.Card {
	c := shownCard(card)
	name := func(s string) string { return capUTF8(s, api.MaxContactNameBytes) }
	c.Name = vcard.Name{Given: name(c.Name.Given), Family: name(c.Name.Family), Middle: name(c.Name.Middle),
		Prefix: name(c.Name.Prefix), Suffix: name(c.Name.Suffix)}
	c.DisplayName, c.Nickname = name(c.DisplayName), name(c.Nickname)
	c.Emails = c.Emails[:min(len(c.Emails), api.MaxContactEmails)]
	c.Phones = c.Phones[:min(len(c.Phones), api.MaxContactPhones)]
	c.Addresses = c.Addresses[:min(len(c.Addresses), api.MaxContactAddresses)]
	urls := make([]vcard.URL, 0, min(len(c.URLs), api.MaxContactURLs))
	for _, u := range c.URLs[:min(len(c.URLs), api.MaxContactURLs)] {
		u.URL = capUTF8(u.URL, api.MaxContactFieldBytes)
		urls = append(urls, u)
	}
	c.URLs = urls
	return c
}

// capUTF8 cuts s to at most n bytes on a rune boundary, trailing space
// trimmed (as vcard.Clean leaves a value).
func capUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}

// apiCard is the API form of a card (apiShownCard).
func apiCard(card *vcard.Card, id api.ContactID, bookID api.AddressBookID, readOnly bool, revision string) api.ContactCard {
	c := apiShownCard(card)
	out := api.ContactCard{
		ID: id, BookID: bookID, ReadOnly: readOnly, Revision: revision,
		Name:        api.ContactName{Given: c.Name.Given, Family: c.Name.Family, Middle: c.Name.Middle, Prefix: c.Name.Prefix, Suffix: c.Name.Suffix},
		DisplayName: vcard.DisplayNameOf(c), Nickname: c.Nickname, Org: c.Org, Title: c.Title, Note: c.Note,
		Emails: []api.ContactEmail{}, Phones: []api.ContactPhone{}, Addresses: []api.ContactAddress{}, URLs: []api.ContactURL{},
	}
	for _, e := range c.Emails {
		out.Emails = append(out.Emails, api.ContactEmail{Label: api.ContactLabel(e.Label), Address: e.Address})
	}
	for _, p := range c.Phones {
		out.Phones = append(out.Phones, api.ContactPhone{Label: api.ContactLabel(p.Label), Number: p.Number})
	}
	for _, a := range c.Addresses {
		out.Addresses = append(out.Addresses, api.ContactAddress{Label: api.ContactLabel(a.Label), Street: a.Street,
			City: a.City, Region: a.Region, Postcode: a.Postcode, Country: a.Country})
	}
	for _, u := range c.URLs {
		out.URLs = append(out.URLs, api.ContactURL{Label: api.ContactLabel(u.Label), URL: u.URL})
	}
	if !c.Birthday.IsZero() {
		out.Birthday = &api.ContactBirthday{Year: c.Birthday.Year, Month: c.Birthday.Month, Day: c.Birthday.Day}
	}
	return out
}

func (s *addressBookService) GetContact(ctx context.Context, p api.ContactGetParams) (*api.ContactGetResult, error) {
	b := s.b
	if addr, ok := recentAddress(p.ContactID); ok {
		c, err := b.store.GetCollectedAddress(ctx, addr)
		if errors.Is(err, store.ErrNotFound) {
			return nil, api.NewError(api.CodeContactNotFound, "unknown contact %q", p.ContactID)
		}
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		return &api.ContactGetResult{Contact: apiCard(recentCard(c), p.ContactID, api.AddressBookRecent, true, "")}, nil
	}
	stored, bk, _, err := b.storedContact(ctx, p.ContactID)
	if err != nil {
		return nil, err
	}
	card, err := storedCard(stored)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "the stored contact cannot be read")
	}
	return &api.ContactGetResult{Contact: apiCard(card, api.ContactID(stored.ID), api.AddressBookID(bk.ID), bk.ReadOnly, stored.ETag)}, nil
}

// recentAddress is the address of a recent-book contact id.
func recentAddress(id api.ContactID) (string, bool) {
	h, ok := strings.CutPrefix(string(id), recentContactPrefix)
	if !ok {
		return "", false
	}
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) == 0 || len(raw) > 320 || !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

// storedContact is a stored contact with its book and account (a contact
// of a book that is not found is not found).
func (b *Backend) storedContact(ctx context.Context, id api.ContactID) (store.StoredContact, store.AddressBook, store.Account, error) {
	c, err := b.store.GetContact(ctx, string(id))
	if errors.Is(err, store.ErrNotFound) {
		return c, store.AddressBook{}, store.Account{}, api.NewError(api.CodeContactNotFound, "unknown contact %q", id)
	}
	if err != nil {
		return c, store.AddressBook{}, store.Account{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	bk, a, err := b.book(ctx, api.AddressBookID(c.BookID))
	if err != nil {
		return c, bk, a, api.NewError(api.CodeContactNotFound, "unknown contact %q", id)
	}
	return c, bk, a, nil
}

func (s *addressBookService) Lookup(ctx context.Context, p api.ContactLookupParams) (*api.ContactLookupResult, error) {
	b := s.b
	if len(p.Addresses) > api.MaxContactLookupAddresses {
		return nil, api.NewError(api.CodeInvalidArgument, "at most %d addresses", api.MaxContactLookupAddresses)
	}
	out := &api.ContactLookupResult{Known: map[string]api.ContactID{}}
	var addrs []string
	seen := map[string]bool{}
	for _, a := range p.Addresses {
		n := contactsync.NormalizeAddress(a)
		if n != "" && !seen[n] {
			seen[n] = true
			addrs = append(addrs, n)
		}
	}
	if len(addrs) == 0 {
		return out, nil
	}
	ids, _, err := b.visibleBookIDs(ctx, "")
	if err != nil {
		return nil, err
	}
	known, err := b.store.LookupContactAddresses(ctx, addrs, ids)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	for a, id := range known {
		out.Known[a] = api.ContactID(id)
	}
	return out, nil
}

// --- contact.create, contact.update, contact.delete --------------------------------

// checkContactCard validates a card of contact.create or contact.update
// against the API's limits and turns it into the model; every failure is
// invalidArgument naming the field (card.emails[2].address).
func checkContactCard(c api.ContactCard) (*vcard.Card, error) {
	bad := func(field, format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "card.%s: %s", field, fmt.Sprintf(format, args...))
	}
	text := func(field, v string, max int) error {
		switch {
		case !utf8.ValidString(v):
			return bad(field, "not UTF-8")
		case len(v) > max:
			return bad(field, "over %d bytes", max)
		}
		return nil
	}
	label := func(field string, l api.ContactLabel) error {
		return text(field+".label", string(l), api.MaxContactLabelBytes)
	}
	checks := []struct {
		field string
		v     string
		max   int
	}{
		{"name.given", c.Name.Given, api.MaxContactNameBytes}, {"name.family", c.Name.Family, api.MaxContactNameBytes},
		{"name.middle", c.Name.Middle, api.MaxContactNameBytes}, {"name.prefix", c.Name.Prefix, api.MaxContactNameBytes},
		{"name.suffix", c.Name.Suffix, api.MaxContactNameBytes}, {"displayName", c.DisplayName, api.MaxContactNameBytes},
		{"nickname", c.Nickname, api.MaxContactNameBytes}, {"org", c.Org, api.MaxContactFieldBytes},
		{"title", c.Title, api.MaxContactFieldBytes}, {"note", c.Note, api.MaxContactNoteBytes},
	}
	for _, ch := range checks {
		if err := text(ch.field, ch.v, ch.max); err != nil {
			return nil, err
		}
	}
	switch {
	case len(c.Emails) > api.MaxContactEmails:
		return nil, bad("emails", "at most %d", api.MaxContactEmails)
	case len(c.Phones) > api.MaxContactPhones:
		return nil, bad("phones", "at most %d", api.MaxContactPhones)
	case len(c.Addresses) > api.MaxContactAddresses:
		return nil, bad("addresses", "at most %d", api.MaxContactAddresses)
	case len(c.URLs) > api.MaxContactURLs:
		return nil, bad("urls", "at most %d", api.MaxContactURLs)
	}
	card := &vcard.Card{
		Name:        vcard.Name{Given: c.Name.Given, Family: c.Name.Family, Middle: c.Name.Middle, Prefix: c.Name.Prefix, Suffix: c.Name.Suffix},
		DisplayName: c.DisplayName, Nickname: c.Nickname, Org: c.Org, Title: c.Title, Note: c.Note,
	}
	for i, e := range c.Emails {
		f := fmt.Sprintf("emails[%d]", i)
		if err := label(f, e.Label); err != nil {
			return nil, err
		}
		a := strings.TrimSpace(e.Address)
		if err := text(f+".address", a, api.MaxContactFieldBytes); err != nil {
			return nil, err
		}
		if parsed, err := mail.ParseAddress(a); err != nil || parsed.Address != a || parsed.Name != "" {
			return nil, bad(f+".address", "not an e-mail address")
		}
		card.Emails = append(card.Emails, vcard.Email{Label: string(e.Label), Address: a})
	}
	for i, ph := range c.Phones {
		f := fmt.Sprintf("phones[%d]", i)
		if err := label(f, ph.Label); err != nil {
			return nil, err
		}
		if err := text(f+".number", ph.Number, api.MaxContactFieldBytes); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ph.Number) == "" {
			return nil, bad(f+".number", "empty")
		}
		card.Phones = append(card.Phones, vcard.Phone{Label: string(ph.Label), Number: ph.Number})
	}
	for i, a := range c.Addresses {
		f := fmt.Sprintf("addresses[%d]", i)
		if err := label(f, a.Label); err != nil {
			return nil, err
		}
		for _, part := range []struct{ n, v string }{{"street", a.Street}, {"city", a.City}, {"region", a.Region}, {"postcode", a.Postcode}, {"country", a.Country}} {
			if err := text(f+"."+part.n, part.v, api.MaxContactFieldBytes); err != nil {
				return nil, err
			}
		}
		card.Addresses = append(card.Addresses, vcard.Address{Label: string(a.Label), Street: a.Street, City: a.City,
			Region: a.Region, Postcode: a.Postcode, Country: a.Country})
	}
	for i, u := range c.URLs {
		f := fmt.Sprintf("urls[%d]", i)
		if err := label(f, u.Label); err != nil {
			return nil, err
		}
		if err := text(f+".url", u.URL, api.MaxContactFieldBytes); err != nil {
			return nil, err
		}
		if strings.TrimSpace(u.URL) == "" {
			return nil, bad(f+".url", "empty")
		}
		card.URLs = append(card.URLs, vcard.URL{Label: string(u.Label), URL: u.URL})
	}
	if bd := c.Birthday; bd != nil {
		ok := bd.Month >= 1 && bd.Month <= 12 && bd.Day >= 1 && bd.Year >= 0 && bd.Year <= 9999
		if ok {
			y := bd.Year
			if y == 0 {
				y = 2000 // a leap year: 29 February without a year is a birthday
			}
			ok = bd.Day <= time.Date(y, time.Month(bd.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
		}
		if !ok {
			return nil, bad("birthday", "not a date")
		}
		card.Birthday = vcard.Birthday{Year: bd.Year, Month: bd.Month, Day: bd.Day}
	}
	cleaned := vcard.Clean(card)
	if cleaned.Name == (vcard.Name{}) && cleaned.DisplayName == "" && cleaned.Nickname == "" && cleaned.Org == "" && len(cleaned.Emails) == 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "card: a contact needs a name, an organisation or an e-mail address")
	}
	return cleaned, nil
}

// writableBook is a book a write may go to: found, not the recent book,
// not read-only.
func (b *Backend) writableBook(ctx context.Context, id api.AddressBookID) (store.AddressBook, store.Account, error) {
	if id == api.AddressBookRecent {
		return store.AddressBook{}, store.Account{}, api.NewError(api.CodeAddressBookReadOnly, "the recent book is read only")
	}
	bk, a, err := b.book(ctx, id)
	if err != nil {
		return bk, a, err
	}
	if bk.ReadOnly {
		return bk, a, api.NewError(api.CodeAddressBookReadOnly, "the address book is read only")
	}
	return bk, a, nil
}

// contactsProviderFor is the account's source: the running syncer's, else
// a new one.
func (b *Backend) contactsProviderFor(ctx context.Context, a store.Account) (contactsync.Provider, error) {
	if b.contactSync == nil {
		return b.contactsProvider(ctx, a)
	}
	return b.contactSync.ProviderFor(ctx, a)
}

// writeError maps what a provider's write returned: the server's read-only
// answer is addressBookReadOnly.
func contactWriteError(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		if ae.Code == api.CodeReadOnly {
			return api.NewError(api.CodeAddressBookReadOnly, "the server does not let the user change this address book")
		}
		return ae
	}
	return api.NewError(api.CodeServerError, "%s", cleanMessageText(err.Error()))
}

func cleanMessageText(s string) string {
	s = strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// conflict stores the server's version of a contact (when it has one)
// and returns contactConflict with it.
func (b *Backend) conflict(ctx context.Context, bk store.AddressBook, ce *contactsync.ConflictError) error {
	e := api.NewError(api.CodeContactConflict, "the contact changed on the server")
	if ce.Server.Card == nil {
		return e
	}
	id, err := contactsync.StoreCard(ctx, b.store, bk.ID, ce.Server)
	if err != nil {
		b.log.Warn("store the server's contact", "book", bk.ID, "err", err)
		return e
	}
	e.Data = api.ContactConflictData{Server: apiCard(ce.Server.Card, api.ContactID(id), api.AddressBookID(bk.ID), bk.ReadOnly, ce.Server.ETag)}
	b.contactsChanged(bk.AccountID, bk.ID)
	return e
}

// gone removes a contact the server no longer has and answers
// contactNotFound.
func (b *Backend) gone(ctx context.Context, bk store.AddressBook, c store.StoredContact) error {
	if err := b.store.DeleteContact(ctx, c.ID); err != nil {
		b.log.Warn("delete a gone contact", "contact", c.ID, "err", err)
	}
	b.contactsChanged(bk.AccountID, bk.ID)
	return api.NewError(api.CodeContactNotFound, "the contact is no longer on the server")
}

// afterWrite asks the engine for a pass of the account soon (the server
// may have changed more than the write's answer says).
func (b *Backend) afterContactWrite(a store.Account, bk store.AddressBook) {
	b.contactsChanged(a.ID, bk.ID)
	if b.contactSync != nil {
		b.contactSync.SyncNow(a.ID)
	}
}

func (s *addressBookService) CreateContact(ctx context.Context, p api.ContactCreateParams) (*api.ContactCreateResult, error) {
	b := s.b
	card, err := checkContactCard(p.Card)
	if err != nil {
		return nil, err
	}
	bk, a, err := b.writableBook(ctx, p.BookID)
	if err != nil {
		return nil, err
	}
	prov, err := b.contactsProviderFor(ctx, a)
	if err != nil {
		return nil, err
	}
	got, err := prov.Create(ctx, bk, card)
	if err != nil {
		return nil, contactWriteError(err)
	}
	id, err := contactsync.StoreCard(ctx, b.store, bk.ID, got)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.log.Info("contact created", "book", bk.ID, "contact", id)
	b.afterContactWrite(a, bk)
	return &api.ContactCreateResult{ContactID: api.ContactID(id), Revision: got.ETag}, nil
}

// serverVersion is a contact as the server has it now, for a conflict
// found before any request was sent; the cached version when the server
// cannot be asked.
func (b *Backend) serverVersion(ctx context.Context, prov contactsync.Provider, bk store.AddressBook, c store.StoredContact) error {
	cur, err := prov.Get(ctx, bk, c.RemoteID)
	switch {
	case errors.Is(err, contactsync.ErrGone):
		return b.gone(ctx, bk, c)
	case err != nil:
		card, perr := storedCard(c)
		if perr != nil {
			return api.NewError(api.CodeContactConflict, "the contact changed on the server")
		}
		cur = contactsync.Card{RemoteID: c.RemoteID, ETag: c.ETag, Raw: c.Raw, RawKind: c.RawKind, Card: card}
	}
	return b.conflict(ctx, bk, &contactsync.ConflictError{Server: cur})
}

func (s *addressBookService) UpdateContact(ctx context.Context, p api.ContactUpdateParams) (*api.ContactUpdateResult, error) {
	b := s.b
	if _, ok := recentAddress(p.ContactID); ok {
		return nil, api.NewError(api.CodeAddressBookReadOnly, "the recent book is read only")
	}
	after, err := checkContactCard(p.Card)
	if err != nil {
		return nil, err
	}
	stored, bk, a, err := b.storedContact(ctx, p.ContactID)
	if err != nil {
		return nil, err
	}
	if _, _, err := b.writableBook(ctx, api.AddressBookID(bk.ID)); err != nil {
		return nil, err
	}
	prov, err := b.contactsProviderFor(ctx, a)
	if err != nil {
		return nil, err
	}
	if p.Revision != stored.ETag {
		// The client edited another version than the stored one.
		return nil, b.serverVersion(ctx, prov, bk, stored)
	}
	before, err := storedCard(stored)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "the stored contact cannot be read")
	}
	// What the client was shown: anything beyond it is not part of the
	// edit (vcard.Patch leaves lines before does not name alone).
	before = apiShownCard(before)
	got, err := prov.Update(ctx, bk, stored, before, after)
	var ce *contactsync.ConflictError
	switch {
	case errors.As(err, &ce):
		return nil, b.conflict(ctx, bk, ce)
	case errors.Is(err, contactsync.ErrGone):
		return nil, b.gone(ctx, bk, stored)
	case err != nil:
		return nil, contactWriteError(err)
	}
	if _, err := contactsync.StoreCard(ctx, b.store, bk.ID, got); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.log.Info("contact updated", "book", bk.ID, "contact", stored.ID)
	b.afterContactWrite(a, bk)
	return &api.ContactUpdateResult{Revision: got.ETag}, nil
}

func (s *addressBookService) DeleteContact(ctx context.Context, p api.ContactDeleteParams) (*api.ContactDeleteResult, error) {
	b := s.b
	if _, ok := recentAddress(p.ContactID); ok {
		return nil, api.NewError(api.CodeAddressBookReadOnly, "the recent book is read only")
	}
	stored, bk, a, err := b.storedContact(ctx, p.ContactID)
	if err != nil {
		return nil, err
	}
	if _, _, err := b.writableBook(ctx, api.AddressBookID(bk.ID)); err != nil {
		return nil, err
	}
	prov, err := b.contactsProviderFor(ctx, a)
	if err != nil {
		return nil, err
	}
	if p.Revision != stored.ETag {
		return nil, b.serverVersion(ctx, prov, bk, stored)
	}
	err = prov.Delete(ctx, bk, stored)
	var ce *contactsync.ConflictError
	switch {
	case errors.As(err, &ce):
		return nil, b.conflict(ctx, bk, ce)
	case errors.Is(err, contactsync.ErrGone):
		// Gone already: what the user asked for.
	case err != nil:
		return nil, contactWriteError(err)
	}
	if err := b.store.DeleteContact(ctx, stored.ID); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	b.log.Info("contact deleted", "book", bk.ID, "contact", stored.ID)
	b.afterContactWrite(a, bk)
	return &api.ContactDeleteResult{}, nil
}

// --- contact.search ------------------------------------------------------------

// searchSyncedBooks are the completion candidates of the synchronised
// books: the visible books of the sending account (scope account) or of
// every account (scope all), at most limit.
func (b *Backend) searchSyncedBooks(ctx context.Context, a store.Account, q string, scope api.ContactSearchScope, limit int) []api.Contact {
	accountID := a.ID
	if scope == api.ContactScopeAll {
		accountID = ""
	}
	ids, names, err := b.visibleBookIDs(ctx, accountID)
	if err != nil || len(ids) == 0 {
		return nil
	}
	found, err := b.store.SearchContactAddresses(ctx, q, ids, limit)
	if err != nil {
		b.log.Debug("contact search", "err", err)
		return nil
	}
	out := make([]api.Contact, 0, len(found))
	for _, f := range found {
		out = append(out, api.Contact{Name: f.DisplayName, Address: f.Address, Source: api.ContactSourceContacts,
			Book: names[f.BookID], ContactID: api.ContactID(f.ContactID)})
	}
	return out
}
