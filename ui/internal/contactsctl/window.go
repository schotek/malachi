// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// The Contacts window (W1, W5): the sidebar of books (account.list and
// addressBook.list), the list of the selected row (contact.list, a page at
// a time, searched by the daemon from the first character after
// SearchDebounce), the selected contact (kept by id across reloads), the
// consent banners of Microsoft accounts whose contacts are on but whose
// sign-in lacks the scope (addressBook.list {accountId} answers
// contactsConsentRequired), and the status line. notify.contactsChanged
// and notify.accountsChanged reload everything after ChangeDebounce,
// keeping the book, the search, as many rows as were shown and the
// selection.

// The window's timing and paging.
const (
	SearchDebounce = 150 * time.Millisecond
	ChangeDebounce = 300 * time.Millisecond
	// PageSize is how many contacts a page asks for.
	PageSize = api.DefaultContactListLimit
)

// ConsentNeed is an account whose contacts wait for Microsoft's consent:
// the banner ConsentBanner(Account) with Allow… (SignIn) or the Online
// Accounts advice (ConsentOnline).
type ConsentNeed struct {
	AccountID api.AccountID
	Account   string
	SignIn    bool
}

// WindowController is the controller of the Contacts window.
type WindowController struct {
	Opts Options

	// OnChange: the sidebar, the list or a state changed; the view reads
	// it anew.
	OnChange func()
	// OnSelect: the selected contact changed ("" none); the detail opens
	// it (ContactController.Open).
	OnSelect func(api.ContactID)
	// OnSignIn is Allow… of a consent banner: the window calls
	// account.oauthStart {accountId, contacts: true} and opens the page.
	OnSignIn func(api.AccountID)

	accounts []api.Account
	books    []api.AddressBook
	items    []contacts.Item
	book     api.AddressBookID
	query    string
	list     []api.ContactSummary
	total    int
	listed   bool
	selected api.ContactID
	consent  []ConsentNeed
	loaded   bool
	loading  bool
	err      string

	refreshGen int
	listGen    int
	searchGen  int
	changeGen  int
	armed      bool
	closed     bool
}

// NewWindowController is the window's controller; Start loads it.
func NewWindowController(opts Options) *WindowController {
	return &WindowController{Opts: opts}
}

// Start loads the accounts, the books and the first page.
func (w *WindowController) Start() { w.refresh() }

// Stop ends the window: no answer is taken any more.
func (w *WindowController) Stop() { w.closed = true }

// Loaded reports that the books have been listed once.
func (w *WindowController) Loaded() bool { return w.loaded }

// Loading reports a contact.list without its answer.
func (w *WindowController) Loading() bool { return w.loading }

// Error is the last failure's sentence ("" none).
func (w *WindowController) Error() string { return w.err }

// Accounts are account.list's accounts.
func (w *WindowController) Accounts() []api.Account { return w.accounts }

// Books are addressBook.list's books (hidden ones too).
func (w *WindowController) Books() []api.AddressBook { return w.books }

// Items are the sidebar's rows.
func (w *WindowController) Items() []contacts.Item { return w.items }

// SelectedBook is the sidebar's selected row.
func (w *WindowController) SelectedBook() api.AddressBookID { return w.book }

// Query is the search.
func (w *WindowController) Query() string { return w.query }

// Contacts are the listed contacts (the pages asked for so far).
func (w *WindowController) Contacts() []api.ContactSummary { return w.list }

// Total is contact.list's total of the book and search.
func (w *WindowController) Total() int { return w.total }

// Rows are the list's rows: with index headings, flat while searching.
func (w *WindowController) Rows() []contacts.Row {
	return contacts.Rows(w.list, w.query == "", w.Opts.Tr)
}

// Selected is the selected contact ("" none).
func (w *WindowController) Selected() api.ContactID { return w.selected }

// Consent are the consent banners, in account order.
func (w *WindowController) Consent() []ConsentNeed { return w.consent }

// HasMore reports more contacts than listed (the list asks LoadMore near
// its end).
func (w *WindowController) HasMore() bool { return w.listed && len(w.list) < w.total }

// Subtitle is the list's subtitle ("All Contacts · 412"); "" before the
// first answer.
func (w *WindowController) Subtitle() string {
	it, ok := contacts.Find(w.items, w.book)
	if !ok || !w.listed {
		return ""
	}
	return contacts.ListSubtitle(it, w.total, w.Opts.Tr)
}

// Status is the status bar's line.
func (w *WindowController) Status() string {
	return contacts.StatusLine(w.books, w.Opts.Clock(), w.Opts.Tr)
}

// ListEmpty is the list's empty state: EmptySearch, EmptyBook or
// EmptyNone.
func (w *WindowController) ListEmpty() contacts.Empty {
	switch {
	case !w.listed || w.loading || w.total > 0:
		return contacts.EmptyNone
	case w.query != "":
		return contacts.EmptySearch
	}
	return contacts.EmptyBook
}

// DetailEmpty is the detail's empty state without a selected contact:
// EmptyNoBooks (with Open Preferences…) when only Recently Used exists,
// else EmptySelection; EmptyNone with a selection or before the books.
func (w *WindowController) DetailEmpty() contacts.Empty {
	switch {
	case !w.loaded || w.selected != "":
		return contacts.EmptyNone
	case !contacts.HasBooks(w.items):
		return contacts.EmptyNoBooks
	}
	return contacts.EmptySelection
}

// CanCreate reports that New Contact has a book to create in.
func (w *WindowController) CanCreate() bool { return w.NewContactBook() != "" }

// NewContactBook is the book New Contact creates in: the selected one when
// writable, else the default (contacts.DefaultBook).
func (w *WindowController) NewContactBook() api.AddressBookID {
	return contacts.DefaultBook(w.books, w.book, "")
}

// Book is a book with its account (nil for Recently Used or an account
// account.list no longer has).
func (w *WindowController) Book(id api.AddressBookID) (api.AddressBook, *api.Account, bool) {
	for _, b := range w.books {
		if b.ID != id {
			continue
		}
		for i := range w.accounts {
			if w.accounts[i].ID == b.AccountID {
				a := w.accounts[i]
				return b, &a, true
			}
		}
		return b, nil, true
	}
	return api.AddressBook{}, nil, false
}

func (w *WindowController) changed() {
	if w.OnChange != nil && !w.closed {
		w.OnChange()
	}
}

// refresh lists the accounts and the books, then the list of the shown
// book with as many rows as are shown, then the consent of accounts
// without books.
func (w *WindowController) refresh() {
	if w.closed {
		return
	}
	w.refreshGen++
	gen := w.refreshGen
	call(w.Opts, api.MethodAccountList, api.AccountListParams{}, 0, func(r *api.AccountListResult, e *api.Error) {
		if w.closed || gen != w.refreshGen {
			return
		}
		if e == nil {
			w.accounts = r.Accounts
		}
		call(w.Opts, api.MethodAddressBookList, api.AddressBookListParams{}, 0, func(b *api.AddressBookListResult, e *api.Error) {
			if w.closed || gen != w.refreshGen {
				return
			}
			if e != nil {
				w.err = w.Opts.ErrorMessage(e, "", "")
				w.changed()
				return
			}
			w.err = ""
			w.books = b.Books
			w.loaded = true
			w.items = contacts.Sidebar(w.books, w.accounts, w.Opts.Tr)
			if _, ok := contacts.Find(w.items, w.book); !ok {
				w.book = contacts.DefaultSelection(w.items)
				w.query = ""
				w.listed = false
			}
			w.listFirst(max(len(w.list), PageSize))
			w.checkConsent()
			w.changed()
		})
	})
}

// listFirst asks for the first limit contacts of the book and search.
func (w *WindowController) listFirst(limit int) {
	w.listGen++
	gen := w.listGen
	if w.book == "" {
		w.list, w.total, w.listed, w.loading = nil, 0, true, false
		w.keepSelection()
		return
	}
	w.loading = true
	p := api.ContactListParams{BookID: w.book, Query: w.query, Limit: min(limit, api.MaxContactListLimit)}
	call(w.Opts, api.MethodContactList, p, 0, func(r *api.ContactListResult, e *api.Error) {
		if w.closed || gen != w.listGen {
			return
		}
		w.loading = false
		if e != nil {
			w.err = w.Opts.ErrorMessage(e, "", "")
			w.changed()
			return
		}
		w.err = ""
		w.list, w.total, w.listed = r.Contacts, r.Total, true
		w.keepSelection()
		w.changed()
	})
}

// keepSelection drops a selection the list no longer has.
func (w *WindowController) keepSelection() {
	if w.selected == "" {
		return
	}
	if !slices.ContainsFunc(w.list, func(c api.ContactSummary) bool { return c.ID == w.selected }) {
		w.Select("")
	}
}

// LoadMore asks for the next page; nothing while a page is on its way or
// everything is listed.
func (w *WindowController) LoadMore() {
	if w.loading || !w.HasMore() || w.book == "" {
		return
	}
	w.loading = true
	gen := w.listGen
	p := api.ContactListParams{BookID: w.book, Query: w.query, Offset: len(w.list), Limit: PageSize}
	call(w.Opts, api.MethodContactList, p, 0, func(r *api.ContactListResult, e *api.Error) {
		if w.closed || gen != w.listGen {
			return
		}
		w.loading = false
		if e != nil {
			w.err = w.Opts.ErrorMessage(e, "", "")
			w.changed()
			return
		}
		seen := map[api.ContactID]bool{}
		for _, c := range w.list {
			seen[c.ID] = true
		}
		for _, c := range r.Contacts {
			if !seen[c.ID] {
				w.list = append(w.list, c)
			}
		}
		w.total = r.Total
		if len(r.Contacts) == 0 {
			w.total = len(w.list)
		}
		w.changed()
	})
}

// SelectBook shows a sidebar row; the search stays.
func (w *WindowController) SelectBook(id api.AddressBookID) {
	if _, ok := contacts.Find(w.items, id); !ok || id == w.book {
		return
	}
	w.book = id
	w.list, w.total, w.listed = nil, 0, false
	w.listFirst(PageSize)
	w.changed()
}

// SetQuery searches: the daemon is asked after SearchDebounce, from the
// first character; "" lists the book again.
func (w *WindowController) SetQuery(q string) {
	q = strings.TrimSpace(q)
	if len(q) > api.MaxContactQueryBytes {
		q = contacts.Clean(q, api.MaxContactQueryBytes)
	}
	if q == w.query {
		return
	}
	w.query = q
	w.searchGen++
	gen := w.searchGen
	w.Opts.after(SearchDebounce, func() {
		if w.closed || gen != w.searchGen {
			return
		}
		w.listFirst(PageSize)
		w.changed()
	})
}

// Select selects a contact ("" none).
func (w *WindowController) Select(id api.ContactID) {
	if id == w.selected {
		return
	}
	w.selected = id
	if w.OnSelect != nil && !w.closed {
		w.OnSelect(id)
	}
}

// Handle takes a notification of the daemon: notify.contactsChanged and
// notify.accountsChanged reload after ChangeDebounce (several in a row
// once).
func (w *WindowController) Handle(method string, params json.RawMessage) {
	if method != api.NotifyContactsChanged && method != api.NotifyAccountsChanged {
		return
	}
	w.changeGen++
	gen := w.changeGen
	w.Opts.after(ChangeDebounce, func() {
		if w.closed || gen != w.changeGen {
			return
		}
		w.refresh()
	})
}

// Reload loads everything again at once (after the window's own write).
func (w *WindowController) Reload() { w.refresh() }

// AllowConsent is Allow… of an account's banner.
func (w *WindowController) AllowConsent(id api.AccountID) {
	if w.OnSignIn != nil {
		w.OnSignIn(id)
	}
}

// consentCandidates are the Microsoft accounts with contacts on and no
// listed book: addressBook.list left them out, perhaps for the consent.
func consentCandidates(accounts []api.Account, books []api.AddressBook) []api.Account {
	have := map[api.AccountID]bool{}
	for _, b := range books {
		have[b.AccountID] = true
	}
	var out []api.Account
	for _, a := range accounts {
		g := a.Config.Graph
		if a.Enabled && a.Config.Protocol() == api.AccountGraph && a.Can(api.CapabilityContacts) &&
			g != nil && g.Contacts != nil && g.Contacts.Sync && !have[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// checkConsent asks each candidate's books alone; a contactsConsentRequired
// makes a banner, anything else drops it.
func (w *WindowController) checkConsent() {
	cands := consentCandidates(w.accounts, w.books)
	keep := w.consent[:0:0]
	for _, c := range w.consent {
		if slices.ContainsFunc(cands, func(a api.Account) bool { return a.ID == c.AccountID }) {
			keep = append(keep, c)
		}
	}
	w.consent = keep
	gen := w.refreshGen
	for _, a := range cands {
		a := a
		call(w.Opts, api.MethodAddressBookList, api.AddressBookListParams{AccountID: a.ID}, 0, func(_ *api.AddressBookListResult, e *api.Error) {
			if w.closed || gen != w.refreshGen {
				return
			}
			w.consent = slices.DeleteFunc(w.consent, func(c ConsentNeed) bool { return c.AccountID == a.ID })
			if d, ok := ConsentData(e); ok || (e != nil && e.Code == api.CodeContactsConsentRequired) {
				w.consent = append(w.consent, ConsentNeed{AccountID: a.ID, Account: contacts.AccountName(a), SignIn: !ok || d.SignIn})
				w.sortConsent()
			}
			w.changed()
		})
	}
}

func (w *WindowController) sortConsent() {
	pos := map[api.AccountID]int{}
	for i, a := range w.accounts {
		pos[a.ID] = i
	}
	slices.SortStableFunc(w.consent, func(a, b ConsentNeed) int { return pos[a.AccountID] - pos[b.AccountID] })
}
