// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// Preferences → Contacts (W3), after Calendar: a section per account with
// the contacts capability in account order — a Microsoft account's switch
// Sync Contacts (account.update with graph.contacts.sync; switching on
// whose sign-in lacks Contacts.ReadWrite shows the consent row with
// Allow…, which hands the account to the re-sign-in with contacts: true),
// a CardDAV account's switch (caldav.contacts) and note, the books of the
// account with their checkboxes (addressBook.set visible) — then the group
// Recipient Suggestions (the key contacts-suggest-scope). Each change is
// shown at once and undone when the daemon refuses it.

// PrefsAccount is an account's section.
type PrefsAccount struct {
	Account api.Account
	// Title is the heading (contacts.AccountName), Badge the kind capsule.
	Title, Badge string
	// SyncOn is the switch Sync Contacts.
	SyncOn bool
	// Consent: contacts are on but the grant lacks the scope; SignIn says
	// Allow… can fix it (else contacts.ConsentOnline).
	Consent, SignIn bool
	// Books are the account's books with their checkboxes (Visible).
	Books []api.AddressBook
	// CardDAV: the note contacts.CardDAVNote follows the books.
	CardDAV bool
}

// PreferencesController is the controller of Preferences → Contacts.
type PreferencesController struct {
	Opts Options
	// Scope and SetScope read and write contacts-suggest-scope
	// (settings.Store.ContactsSuggestScope); nil is the default, not
	// stored.
	Scope    func() api.ContactSearchScope
	SetScope func(api.ContactSearchScope)

	// OnChange: the page changed; the view reads it anew.
	OnChange func()
	// OnSignIn is Allow… (account.oauthStart {accountId, contacts: true}).
	OnSignIn func(api.AccountID)

	accounts []api.Account
	books    []api.AddressBook
	consent  map[api.AccountID]ConsentNeed
	saving   map[api.AccountID]bool
	err      string
	loaded   bool
	gen      int
	closed   bool
}

// NewPreferencesController is the page's controller; Start loads it.
func NewPreferencesController(opts Options) *PreferencesController {
	return &PreferencesController{Opts: opts, consent: map[api.AccountID]ConsentNeed{}, saving: map[api.AccountID]bool{}}
}

// Start loads the accounts and the books.
func (p *PreferencesController) Start() { p.refresh() }

// Stop ends the page.
func (p *PreferencesController) Stop() { p.closed = true }

// Loaded reports the first answer.
func (p *PreferencesController) Loaded() bool { return p.loaded }

// Error is the last refusal's sentence ("" none).
func (p *PreferencesController) Error() string { return p.err }

// Books are addressBook.list's books (for Add to Contacts' default).
func (p *PreferencesController) Books() []api.AddressBook { return p.books }

func (p *PreferencesController) changed() {
	if p.OnChange != nil && !p.closed {
		p.OnChange()
	}
}

// Handle reloads after notify.contactsChanged and notify.accountsChanged.
func (p *PreferencesController) Handle(method string, _ json.RawMessage) {
	if method == api.NotifyContactsChanged || method == api.NotifyAccountsChanged {
		p.refresh()
	}
}

func (p *PreferencesController) refresh() {
	if p.closed {
		return
	}
	p.gen++
	gen := p.gen
	call(p.Opts, api.MethodAccountList, api.AccountListParams{}, 0, func(r *api.AccountListResult, e *api.Error) {
		if p.closed || gen != p.gen {
			return
		}
		if e != nil {
			p.err = p.Opts.ErrorMessage(e, "", "")
			p.changed()
			return
		}
		call(p.Opts, api.MethodAddressBookList, api.AddressBookListParams{}, 0, func(b *api.AddressBookListResult, e *api.Error) {
			if p.closed || gen != p.gen {
				return
			}
			p.accounts = r.Accounts
			if e == nil {
				p.books = b.Books
			}
			p.loaded = true
			p.checkConsent(gen)
			p.changed()
		})
	})
}

func (p *PreferencesController) checkConsent(gen int) {
	cands := consentCandidates(p.accounts, p.books)
	for id := range p.consent {
		if !slices.ContainsFunc(cands, func(a api.Account) bool { return a.ID == id }) {
			delete(p.consent, id)
		}
	}
	for _, a := range cands {
		a := a
		call(p.Opts, api.MethodAddressBookList, api.AddressBookListParams{AccountID: a.ID}, 0, func(_ *api.AddressBookListResult, e *api.Error) {
			if p.closed || gen != p.gen {
				return
			}
			delete(p.consent, a.ID)
			if d, ok := ConsentData(e); ok || (e != nil && e.Code == api.CodeContactsConsentRequired) {
				p.consent[a.ID] = ConsentNeed{AccountID: a.ID, Account: contacts.AccountName(a), SignIn: !ok || d.SignIn}
			}
			p.changed()
		})
	}
}

// Accounts are the page's sections: the enabled accounts with the contacts
// capability (graph and caldav), in account order.
func (p *PreferencesController) Accounts() []PrefsAccount {
	var out []PrefsAccount
	for _, a := range p.accounts {
		kind := a.Config.Protocol()
		if !a.Can(api.CapabilityContacts) || (kind != api.AccountGraph && kind != api.AccountCalDAV) {
			continue
		}
		s := PrefsAccount{Account: a, Title: contacts.AccountName(a), Badge: contacts.AccountKindBadge(a),
			SyncOn: SyncOn(a.Config), CardDAV: kind == api.AccountCalDAV}
		if c, ok := p.consent[a.ID]; ok && s.SyncOn {
			s.Consent, s.SignIn = true, c.SignIn
		}
		if s.SyncOn {
			for _, b := range p.books {
				if b.AccountID == a.ID {
					s.Books = append(s.Books, b)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// Empty reports a page without an account that can have contacts
// (contacts.NoContactAccounts).
func (p *PreferencesController) Empty() bool { return p.loaded && len(p.Accounts()) == 0 }

// SyncOn is an account's contacts switch: graph.contacts.sync, or
// caldav.contacts.
func SyncOn(c api.AccountConfig) bool {
	switch c.Protocol() {
	case api.AccountGraph:
		return c.Graph != nil && c.Graph.Contacts != nil && c.Graph.Contacts.Sync
	case api.AccountCalDAV:
		return c.CalDAV != nil && c.CalDAV.Contacts
	}
	return false
}

// withSync is c with the contacts switch set.
func withSync(c api.AccountConfig, on bool) api.AccountConfig {
	c = cloneConfig(c)
	switch c.Protocol() {
	case api.AccountGraph:
		if c.Graph == nil {
			c.Graph = &api.GraphConfig{}
		}
		c.Graph.Contacts = &api.ContactsConfig{Sync: on}
	case api.AccountCalDAV:
		if c.CalDAV == nil {
			c.CalDAV = &api.CalDAVConfig{}
		}
		c.CalDAV.Contacts = on
	}
	return c
}

// Saving reports an account.update of the account without its answer.
func (p *PreferencesController) Saving(id api.AccountID) bool { return p.saving[id] }

// SetSync is Sync Contacts: one account.update of the whole configuration
// with empty credentials (the keyring keeps the secret), shown at once and
// undone on a refusal.
func (p *PreferencesController) SetSync(id api.AccountID, on bool) {
	i := slices.IndexFunc(p.accounts, func(a api.Account) bool { return a.ID == id })
	if i < 0 || p.saving[id] || SyncOn(p.accounts[i].Config) == on || !p.accounts[i].Can(api.CapabilityContacts) {
		return
	}
	before := p.accounts[i].Config
	name := contacts.AccountName(p.accounts[i])
	cfg := withSync(before, on)
	p.accounts[i].Config = cfg
	p.saving[id], p.err = true, ""
	p.changed()
	gen := p.gen
	call(p.Opts, api.MethodAccountUpdate, api.AccountUpdateParams{AccountID: id, Config: cfg}, WriteTimeout, func(_ *api.AccountUpdateResult, e *api.Error) {
		delete(p.saving, id)
		if p.closed {
			return
		}
		if e != nil {
			if gen == p.gen {
				if j := slices.IndexFunc(p.accounts, func(a api.Account) bool { return a.ID == id }); j >= 0 {
					p.accounts[j].Config = before
				}
			}
			p.err = p.Opts.ErrorMessage(e, name, "")
			p.changed()
			return
		}
		p.refresh()
	})
}

// SetVisible is a book's checkbox (addressBook.set), shown at once and
// undone on a refusal.
func (p *PreferencesController) SetVisible(book api.AddressBookID, on bool) {
	i := slices.IndexFunc(p.books, func(b api.AddressBook) bool { return b.ID == book })
	if i < 0 || p.books[i].Visible == on {
		return
	}
	p.books[i].Visible = on
	p.err = ""
	p.changed()
	call(p.Opts, api.MethodAddressBookSet, api.AddressBookSetParams{BookID: book, Visible: on}, 0, func(r *api.AddressBookSetResult, e *api.Error) {
		if p.closed {
			return
		}
		j := slices.IndexFunc(p.books, func(b api.AddressBook) bool { return b.ID == book })
		switch {
		case e != nil:
			if j >= 0 {
				p.books[j].Visible = !on
			}
			p.err = p.Opts.ErrorMessage(e, "", "")
		case j >= 0:
			p.books[j] = r.Book
		}
		p.changed()
	})
}

// Allow is Allow… of an account's consent row.
func (p *PreferencesController) Allow(id api.AccountID) {
	if p.OnSignIn != nil {
		p.OnSignIn(id)
	}
}

// SuggestScope is Recipient Suggestions' choice (the account's books by
// default).
func (p *PreferencesController) SuggestScope() api.ContactSearchScope {
	if p.Scope == nil {
		return api.ContactScopeAccount
	}
	if s := p.Scope(); s == api.ContactScopeAll {
		return s
	}
	return api.ContactScopeAccount
}

// SetSuggestScope writes Recipient Suggestions' choice; a value outside
// contacts.Scopes is ignored.
func (p *PreferencesController) SetSuggestScope(s api.ContactSearchScope) {
	if !slices.Contains(contacts.Scopes, s) || s == p.SuggestScope() {
		return
	}
	if p.SetScope != nil {
		p.SetScope(s)
	}
	p.changed()
}
