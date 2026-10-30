// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The invented address books of MALACHI_CONTACTS_SAMPLES=1 (like
// MALACHI_CALENDAR_SAMPLES): an in-memory daemon that answers the contacts
// methods, account.list, account.update and account.oauthStart, so that
// the Contacts window, the address chips and Preferences → Contacts can be
// seen and clicked through without a daemon, and the smoke tests have
// something to show. Writes change the memory only. Every name, address
// and number is invented (W1–W6). Other methods answer methodNotFound.

// SamplesEnv turns the samples on ("1").
const SamplesEnv = "MALACHI_CONTACTS_SAMPLES"

// SamplesOn reports MALACHI_CONTACTS_SAMPLES=1.
func SamplesOn() bool { return os.Getenv(SamplesEnv) == "1" }

// SampleCaller is the in-memory daemon of the samples; safe for calls
// from any goroutine.
type SampleCaller struct {
	mu       sync.Mutex
	accounts []api.Account
	books    []api.AddressBook
	cards    map[api.ContactID]*api.ContactCard
	recent   []api.ContactSummary
	next     int
	// Notify, when set, gets notify.contactsChanged after a write (on the
	// caller's goroutine).
	Notify func(method string, params json.RawMessage)
}

// NewSampleCaller is the sample daemon as of now.
func NewSampleCaller(now time.Time) *SampleCaller {
	synced := now.Add(-3 * time.Minute)
	contactsCaps := append(append([]api.AccountCapability(nil), api.MailCapabilities...), api.CapabilityCalendar, api.CapabilityContacts)
	s := &SampleCaller{cards: map[api.ContactID]*api.ContactCard{}}
	s.accounts = []api.Account{
		{ID: "acc_work", Enabled: true, Capabilities: contactsCaps, Config: api.AccountConfig{Name: "Práce", Email: "jana@firma.cz",
			Kind: api.AccountGraph, Graph: &api.GraphConfig{Contacts: &api.ContactsConfig{Sync: true}}}},
		{ID: "acc_petr", Enabled: true, Capabilities: contactsCaps, Config: api.AccountConfig{Name: "Petr", Email: "petr@firma2.cz",
			Kind: api.AccountGraph, Graph: &api.GraphConfig{Contacts: &api.ContactsConfig{Sync: true}}}},
		{ID: "acc_cloud", Enabled: true, Capabilities: []api.AccountCapability{api.CapabilityCalendar, api.CapabilityContacts},
			Config: api.AccountConfig{Name: "Osobní", Kind: api.AccountCalDAV,
				CalDAV: &api.CalDAVConfig{ServerURL: "https://cloud.example.cz", Contacts: true}}},
	}
	s.books = []api.AddressBook{
		{ID: "ab_work", AccountID: "acc_work", Name: "Kontakty", Kind: api.AddressBookKindGraph, Visible: true, LastSyncAt: &synced},
		{ID: "ab_work_old", AccountID: "acc_work", Name: "Archiv", Kind: api.AddressBookKindGraph, Visible: false, LastSyncAt: &synced},
		{ID: "ab_family", AccountID: "acc_cloud", Name: "Rodina", Kind: api.AddressBookKindCardDAV, Visible: true, LastSyncAt: &synced},
		{ID: "ab_shared", AccountID: "acc_cloud", Name: "Sdílené", Kind: api.AddressBookKindCardDAV, ReadOnly: true, Visible: true, LastSyncAt: &synced},
		{ID: api.AddressBookRecent, Name: "Recently Used", Kind: api.AddressBookKindRecent, ReadOnly: true, Visible: true},
	}
	work := func(given, family, org, title, email, phone string) api.ContactCard {
		c := api.ContactCard{BookID: "ab_work", Name: api.ContactName{Given: given, Family: family},
			DisplayName: given + " " + family, Org: org, Title: title,
			Emails: []api.ContactEmail{{Label: api.ContactLabelWork, Address: email}}}
		if phone != "" {
			c.Phones = []api.ContactPhone{{Label: api.ContactLabelMobile, Number: phone}}
		}
		return c
	}
	jana := work("Jana", "Nováková", "Stavby Nová s.r.o.", "projektová manažerka", "jana.novakova@example.cz", "+420 601 234 567")
	jana.Emails = append(jana.Emails, api.ContactEmail{Label: api.ContactLabelHome, Address: "jana@example.org"})
	jana.Addresses = []api.ContactAddress{{Label: api.ContactLabelWork, Street: "Dlouhá 12", Postcode: "110 00", City: "Praha 1", Country: "Česko"}}
	jana.Birthday = &api.ContactBirthday{Month: 3, Day: 14}
	jana.URLs = []api.ContactURL{{Label: api.ContactLabelWork, URL: "https://stavby-nova.example.cz"}}
	jana.Note = "Preferuje schůzky dopoledne.\nParkování ve dvoře."
	for _, c := range []api.ContactCard{
		jana,
		work("Pavel", "Novák", "", "", "pavel.novak@example.cz", "+420 602 111 222"),
		work("Marie", "Svobodová", "Svobodová účetnictví", "účetní", "marie@example.cz", ""),
		work("Karel", "Kučera", "Pila Kučera", "", "karel@kucera.example", "+420 603 333 444"),
		work("Lucie", "Procházková", "", "", "lucie.prochazkova@example.cz", ""),
		work("Hana", "Růžičková", "Stavby Nová s.r.o.", "asistentka", "hana@stavby-nova.example.cz", ""),
		work("Tomáš", "Svoboda", "Pila Svoboda s.r.o.", "", "tomas@svoboda.example", ""),
		work("Eva", "Veselá", "", "", "eva.vesela@example.cz", ""),
		{BookID: "ab_work", Org: "Tiskárna Dvořák", DisplayName: "Tiskárna Dvořák",
			Emails: []api.ContactEmail{{Label: api.ContactLabelWork, Address: "info@tiskarna.example"}}},
		{BookID: "ab_family", Name: api.ContactName{Given: "Babička"}, DisplayName: "Babička",
			Phones: []api.ContactPhone{{Label: api.ContactLabelHome, Number: "+420 241 000 111"}}},
		{BookID: "ab_family", Name: api.ContactName{Given: "David", Family: "Dvořák"}, DisplayName: "David Dvořák",
			Emails: []api.ContactEmail{{Address: "david@dvorak.example"}}, Birthday: &api.ContactBirthday{Year: 1980, Month: 6, Day: 2}},
		{BookID: "ab_family", Name: api.ContactName{Given: "Jiří", Family: "Pokorný"}, DisplayName: "Jiří Pokorný",
			Emails: []api.ContactEmail{{Label: api.ContactLabelHome, Address: "jiri.pokorny@example.org"}}},
		{BookID: "ab_shared", Name: api.ContactName{Given: "Petra", Family: "Malá"}, DisplayName: "Petra Malá", Org: "Školka Sluníčko",
			Emails: []api.ContactEmail{{Label: api.ContactLabelWork, Address: "petra@skolka.example"}}},
	} {
		s.add(c)
	}
	for _, a := range []string{"jan.horak@example.com", "objednavky@eshop.example", "petr.svoboda@example.com"} {
		s.recent = append(s.recent, api.ContactSummary{ID: api.ContactID("recent:" + a), BookID: api.AddressBookRecent,
			DisplayName: a, SortKey: a, Section: strings.ToUpper(a[:1]), Email: a})
	}
	return s
}

// add stores a card with a new id and revision.
func (s *SampleCaller) add(c api.ContactCard) api.ContactID {
	s.next++
	c.ID = api.ContactID("c_" + strconv.Itoa(s.next))
	c.Revision = "r1"
	c.ReadOnly = s.book(c.BookID).ReadOnly
	s.cards[c.ID] = &c
	return c.ID
}

func (s *SampleCaller) book(id api.AddressBookID) api.AddressBook {
	for _, b := range s.books {
		if b.ID == id {
			return b
		}
	}
	return api.AddressBook{}
}

func summary(c *api.ContactCard) api.ContactSummary {
	key := strings.ToLower(c.Name.Family + " " + c.Name.Given)
	if strings.TrimSpace(key) == "" {
		key = strings.ToLower(c.DisplayName)
	}
	key = strings.TrimSpace(key)
	sec := ""
	for _, r := range key {
		sec = strings.ToUpper(string(r))
		break
	}
	s := api.ContactSummary{ID: c.ID, BookID: c.BookID, DisplayName: c.DisplayName, SortKey: key, Section: sec, Org: c.Org}
	if len(c.Emails) > 0 {
		s.Email = c.Emails[0].Address
	}
	return s
}

// Call answers a method like the daemon.
func (s *SampleCaller) Call(_ context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	s.mu.Lock()
	v, notify, err := s.answer(method, raw)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if notify != nil && s.Notify != nil {
		p, _ := json.Marshal(notify)
		s.Notify(api.NotifyContactsChanged, p)
	}
	if result == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func notFound(id api.ContactID) error {
	return &api.Error{Code: api.CodeContactNotFound, Message: "no contact " + string(id)}
}

func (s *SampleCaller) answer(method string, raw json.RawMessage) (any, *api.ContactsChangedNotification, error) {
	switch method {
	case api.MethodAccountList:
		return api.AccountListResult{Accounts: s.accounts}, nil, nil
	case api.MethodAccountUpdate:
		var p api.AccountUpdateParams
		_ = json.Unmarshal(raw, &p)
		for i := range s.accounts {
			if s.accounts[i].ID == p.AccountID {
				s.accounts[i].Config = p.Config
				return api.AccountUpdateResult{}, &api.ContactsChangedNotification{AccountID: p.AccountID}, nil
			}
		}
		return nil, nil, &api.Error{Code: api.CodeAccountNotFound, Message: "no account"}
	case api.MethodAccountOAuthStart:
		return nil, nil, &api.Error{Code: api.CodeUnavailable, Message: "samples cannot sign in"}
	case api.MethodAddressBookList:
		var p api.AddressBookListParams
		_ = json.Unmarshal(raw, &p)
		if p.AccountID == "acc_petr" {
			return nil, nil, &api.Error{Code: api.CodeContactsConsentRequired, Message: "consent",
				Data: map[string]any{"accountId": "acc_petr", "signIn": true}}
		}
		var out []api.AddressBook
		for _, b := range s.books {
			if p.AccountID != "" && b.AccountID != p.AccountID {
				continue
			}
			if a := s.account(b.AccountID); b.AccountID != "" && (a == nil || !SyncOn(a.Config)) {
				continue
			}
			b.Count = s.count(b.ID)
			out = append(out, b)
		}
		return api.AddressBookListResult{Books: out}, nil, nil
	case api.MethodAddressBookSet:
		var p api.AddressBookSetParams
		_ = json.Unmarshal(raw, &p)
		for i := range s.books {
			if s.books[i].ID == p.BookID {
				s.books[i].Visible = p.Visible
				b := s.books[i]
				b.Count = s.count(b.ID)
				return api.AddressBookSetResult{Book: b}, &api.ContactsChangedNotification{AccountID: b.AccountID}, nil
			}
		}
		return nil, nil, &api.Error{Code: api.CodeAddressBookNotFound, Message: "no book"}
	case api.MethodContactList:
		var p api.ContactListParams
		_ = json.Unmarshal(raw, &p)
		return s.list(p), nil, nil
	case api.MethodContactGet:
		var p api.ContactGetParams
		_ = json.Unmarshal(raw, &p)
		if c, ok := s.cards[p.ContactID]; ok {
			return api.ContactGetResult{Contact: *c}, nil, nil
		}
		for _, r := range s.recent {
			if r.ID == p.ContactID {
				return api.ContactGetResult{Contact: api.ContactCard{ID: r.ID, BookID: api.AddressBookRecent, ReadOnly: true,
					DisplayName: r.DisplayName, Emails: []api.ContactEmail{{Address: r.Email}}}}, nil, nil
			}
		}
		return nil, nil, notFound(p.ContactID)
	case api.MethodContactLookup:
		var p api.ContactLookupParams
		_ = json.Unmarshal(raw, &p)
		known := map[string]api.ContactID{}
		for _, a := range p.Addresses {
			for _, c := range s.cards {
				if !s.book(c.BookID).Visible {
					continue
				}
				for _, e := range c.Emails {
					if strings.EqualFold(e.Address, a) {
						known[a] = c.ID
					}
				}
			}
		}
		return api.ContactLookupResult{Known: known}, nil, nil
	case api.MethodContactCreate:
		var p api.ContactCreateParams
		_ = json.Unmarshal(raw, &p)
		b := s.book(p.BookID)
		switch {
		case b.ID == "":
			return nil, nil, &api.Error{Code: api.CodeAddressBookNotFound, Message: "no book"}
		case b.ReadOnly:
			return nil, nil, &api.Error{Code: api.CodeAddressBookReadOnly, Message: "read only"}
		}
		p.Card.BookID = p.BookID
		id := s.add(p.Card)
		return api.ContactCreateResult{ContactID: id, Revision: "r1"}, &api.ContactsChangedNotification{AccountID: b.AccountID, BookIDs: []api.AddressBookID{b.ID}}, nil
	case api.MethodContactUpdate:
		var p api.ContactUpdateParams
		_ = json.Unmarshal(raw, &p)
		c, ok := s.cards[p.ContactID]
		switch {
		case !ok:
			return nil, nil, notFound(p.ContactID)
		case c.ReadOnly:
			return nil, nil, &api.Error{Code: api.CodeAddressBookReadOnly, Message: "read only"}
		case c.Revision != p.Revision:
			return nil, nil, &api.Error{Code: api.CodeContactConflict, Message: "conflict", Data: api.ContactConflictData{Server: *c}}
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(c.Revision, "r"))
		card := p.Card
		card.ID, card.BookID, card.ReadOnly, card.Revision = c.ID, c.BookID, c.ReadOnly, fmt.Sprintf("r%d", n+1)
		s.cards[c.ID] = &card
		return api.ContactUpdateResult{Revision: card.Revision}, &api.ContactsChangedNotification{AccountID: s.book(c.BookID).AccountID, BookIDs: []api.AddressBookID{c.BookID}}, nil
	case api.MethodContactDelete:
		var p api.ContactDeleteParams
		_ = json.Unmarshal(raw, &p)
		c, ok := s.cards[p.ContactID]
		switch {
		case !ok:
			return nil, nil, notFound(p.ContactID)
		case c.ReadOnly:
			return nil, nil, &api.Error{Code: api.CodeAddressBookReadOnly, Message: "read only"}
		}
		delete(s.cards, p.ContactID)
		return api.ContactDeleteResult{}, &api.ContactsChangedNotification{AccountID: s.book(c.BookID).AccountID, BookIDs: []api.AddressBookID{c.BookID}}, nil
	}
	return nil, nil, &api.Error{Code: api.CodeMethodNotFound, Message: method}
}

func (s *SampleCaller) account(id api.AccountID) *api.Account {
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			return &s.accounts[i]
		}
	}
	return nil
}

// listed reports a book contact.list "all" includes: visible, of an
// account whose contacts are on.
func (s *SampleCaller) listed(id api.AddressBookID) bool {
	b := s.book(id)
	a := s.account(b.AccountID)
	return b.Visible && a != nil && SyncOn(a.Config)
}

func (s *SampleCaller) count(id api.AddressBookID) int {
	if id == api.AddressBookRecent {
		return len(s.recent)
	}
	n := 0
	for _, c := range s.cards {
		if c.BookID == id {
			n++
		}
	}
	return n
}

func (s *SampleCaller) list(p api.ContactListParams) api.ContactListResult {
	var src []api.ContactSummary
	switch p.BookID {
	case api.AddressBookRecent:
		src = slices.Clone(s.recent)
	default:
		for _, c := range s.cards {
			if (p.BookID == api.AddressBookAll && s.listed(c.BookID)) || c.BookID == p.BookID {
				src = append(src, summary(c))
			}
		}
	}
	slices.SortFunc(src, func(a, b api.ContactSummary) int { return strings.Compare(a.SortKey, b.SortKey) })
	q := strings.ToLower(strings.TrimSpace(p.Query))
	match := []api.ContactSummary{}
	for _, c := range src {
		if q == "" || strings.Contains(strings.ToLower(c.DisplayName+" "+c.Org+" "+c.Email), q) {
			match = append(match, c)
		}
	}
	limit := p.Limit
	if limit <= 0 {
		limit = api.DefaultContactListLimit
	}
	start := min(max(p.Offset, 0), len(match))
	end := min(start+limit, len(match))
	return api.ContactListResult{Contacts: match[start:end], Total: len(match)}
}
