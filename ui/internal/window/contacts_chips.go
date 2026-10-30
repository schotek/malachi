// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The address chips' contacts item (W2, docs/plans/address-book.md A12):
// Show Contact for an address a visible book has exactly
// (contactsctl.Lookup, contact.lookup, never by name), else Add to
// Contacts… when some book can take a new contact, which opens a popover
// over the chip (W4 b) with the header's name prefilled and selected,
// because the sender wrote it. Nothing is saved before Add. One state for
// the main window: the pane, the conversation cards and the message
// windows ask through it; with MALACHI_CONTACTS_SAMPLES=1 it talks to the
// sample daemon, which the Contacts window shares (contactsOptions).

// chipContactsHeaders caps the headers whose chips follow the lookup's
// answers: the shown message, its conversation's cards and a few message
// windows; an older header asks again when it renders.
const chipContactsHeaders = 64

// chipContacts is the lookup with the books and accounts it decides by.
type chipContacts struct {
	w      *Window
	caller board.Caller
	lookup *contactsctl.Lookup

	books    []api.AddressBook
	accounts []api.Account
	loaded   bool
	gen      int
	headers  []*addressHeader
}

// chipContacts is the window's state, made (and its books listed) on
// first use.
func (w *Window) chipState() *chipContacts {
	if w.chipContacts != nil {
		return w.chipContacts
	}
	c := &chipContacts{w: w, caller: contactsCaller(w.client)}
	listenContacts(func(method string, _ json.RawMessage) {
		if method == api.NotifyContactsChanged {
			w.contactsChanged()
		} else {
			w.contactsAccountsChanged()
		}
	})
	w.chipContacts = c
	c.lookup = contactsctl.NewLookup(w.contactsOptions())
	c.lookup.OnChange = c.answered
	c.refresh()
	return c
}

// contactsOptions are the options of the contacts controllers of this
// window: the daemon (or the samples), the main loop, the translations.
func (w *Window) contactsOptions() contactsctl.Options {
	return contactsctl.Options{
		Caller: w.chipState().caller, Loop: glibLoop{}, Tr: i18n.Tr, Format: widget.Strftime,
		RPCText: widget.RPCErrorText, Log: w.log,
	}
}

// contactsChanged is notify.contactsChanged: every answer is forgotten,
// the books listed again and the chips on show ask again.
func (w *Window) contactsChanged() {
	if w.chipContacts == nil {
		return
	}
	c := w.chipContacts
	c.lookup.Invalidate()
	c.refresh()
	c.askAll()
}

// contactsAccountsChanged is notify.accountsChanged's effect on the
// chips: the books are listed again.
func (w *Window) contactsAccountsChanged() {
	if w.chipContacts != nil {
		w.chipContacts.refresh()
	}
}

// call runs a method off the main loop and hands done its error on it.
func (c *chipContacts) call(method string, params, result any, timeout time.Duration, done func(*api.Error)) {
	caller := c.caller
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		e := contactsctl.AsError(caller.Call(ctx, method, params, result))
		glib.IdleAdd(func() { done(e) })
	}()
}

// refresh lists the accounts and the books; the chips on show follow.
func (c *chipContacts) refresh() {
	c.gen++
	gen := c.gen
	var accounts api.AccountListResult
	c.call(api.MethodAccountList, api.AccountListParams{}, &accounts, contactsctl.DefaultCallTimeout, func(e *api.Error) {
		if gen != c.gen {
			return
		}
		if e == nil {
			c.accounts = accounts.Accounts
		}
		var books api.AddressBookListResult
		c.call(api.MethodAddressBookList, api.AddressBookListParams{}, &books, contactsctl.DefaultCallTimeout, func(e *api.Error) {
			if gen != c.gen {
				return
			}
			if e != nil {
				// No book is a safe answer: Add to Contacts… is not
				// offered, Show Contact still is.
				c.books, c.loaded = nil, false
			} else {
				c.books, c.loaded = books.Books, true
			}
			c.refreshAll()
		})
	})
}

// watch follows h's chips and asks for the addresses of its message.
func (c *chipContacts) watch(h *addressHeader) {
	if !c.loaded && c.gen > 0 {
		// The last listing failed (no daemon yet): try again.
		c.refresh()
	}
	c.headers = append(slices.DeleteFunc(c.headers, func(x *addressHeader) bool { return x == h }), h)
	if len(c.headers) > chipContactsHeaders {
		c.headers = c.headers[len(c.headers)-chipContactsHeaders:]
	}
	c.ask(h)
}

func (c *chipContacts) ask(h *addressHeader) {
	if h.message == "" {
		return
	}
	var addrs []string
	for _, list := range h.lists {
		for _, a := range list {
			addrs = append(addrs, a.Address)
		}
	}
	c.lookup.Ask(string(h.message), addrs)
}

func (c *chipContacts) askAll() {
	for _, h := range c.headers {
		c.ask(h)
	}
}

// answered is the lookup's answer for a message: its chips' items.
func (c *chipContacts) answered(key string) {
	for _, h := range c.headers {
		if string(h.message) == key {
			h.refreshContacts()
		}
	}
}

func (c *chipContacts) refreshAll() {
	for _, h := range c.headers {
		h.refreshContacts()
	}
}

// item is the contacts item of an address on h's chips.
func (c *chipContacts) item(h *addressHeader, address string) (contacts.ChipItem, api.ContactID) {
	return c.lookup.Item(string(h.message), address, c.books)
}

// accountLabel is the label of the account of a book ("" unknown).
func (c *chipContacts) accountLabel(book api.AddressBookID) string {
	for _, b := range c.books {
		if b.ID != book {
			continue
		}
		for i := range c.accounts {
			if c.accounts[i].ID == b.AccountID {
				return contacts.AccountLabel(&c.accounts[i], i18n.Tr)
			}
		}
	}
	return ""
}

// refreshContacts updates the contacts item of every chip of h.
func (h *addressHeader) refreshContacts() {
	for _, r := range []*addressRow{h.from, h.to, h.cc} {
		for _, f := range r.contactFills {
			f()
		}
	}
}

// addToContacts opens Add to Contacts… for a over the chip anchor.
func (h *addressHeader) addToContacts(anchor *gtk.MenuButton, a api.Address) {
	c := h.v.win.chipState()
	st := contacts.NewAddFromChip(a, h.acc, c.books, c.accounts, i18n.Tr)
	tr := i18n.Tr

	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.SetSizeRequest(300, -1)
	pop.SetChild(box)

	caption := func(text string) *gtk.Label {
		l := gtk.NewLabel(text)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.AddCSSClass("caption-heading")
		return l
	}
	title := gtk.NewLabel(contacts.AddTitle(tr))
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.AddCSSClass("heading")
	box.Append(title)

	box.Append(caption(contacts.NameLabel(tr)))
	name := gtk.NewEntry()
	name.SetText(st.Name)
	name.SetMaxLength(api.MaxContactNameBytes)
	box.Append(name)
	if st.Warning != "" {
		warn := gtk.NewLabel(st.Warning)
		warn.SetUseMarkup(false)
		warn.SetXAlign(0)
		warn.SetWrap(true)
		warn.SetWrapMode(pango.WrapWordChar)
		warn.AddCSSClass("caption")
		warn.AddCSSClass("dim-label")
		box.Append(warn)
	}

	box.Append(caption(contacts.EmailLabel(tr)))
	email := gtk.NewEntry()
	email.SetText(st.Address)
	email.SetInputPurpose(gtk.InputPurposeEmail)
	box.Append(email)

	box.Append(caption(contacts.BookLabel(tr)))
	var texts []string
	at := uint(0)
	for i, b := range st.Books {
		texts = append(texts, b.Text)
		if b.Book == st.Book {
			at = uint(i)
		}
	}
	books := gtk.NewDropDownFromStrings(texts)
	if len(texts) > 0 {
		books.SetSelected(at)
	}
	box.Append(books)

	problem := gtk.NewLabel("")
	problem.SetUseMarkup(false)
	problem.SetXAlign(0)
	problem.SetWrap(true)
	problem.SetWrapMode(pango.WrapWordChar)
	problem.AddCSSClass("error")
	problem.SetVisible(false)
	box.Append(problem)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetMarginTop(6)
	more := gtk.NewButtonWithLabel(contacts.MoreDetails(tr))
	more.AddCSSClass("flat")
	more.SetHExpand(true)
	more.SetHAlign(gtk.AlignStart)
	add := gtk.NewButtonWithLabel(contacts.Add(tr))
	add.AddCSSClass("suggested-action")
	buttons.Append(more)
	buttons.Append(add)
	box.Append(buttons)

	saving := false
	read := func() {
		st.Name, st.Address = name.Text(), email.Text()
		if i := books.Selected(); i != gtk.INVALID_LIST_POSITION && int(i) < len(st.Books) {
			st.Book = st.Books[i].Book
		}
		add.SetSensitive(!saving && st.CanAdd())
	}
	read()
	name.ConnectChanged(read)
	email.ConnectChanged(read)
	books.NotifyProperty("selected", read)

	save := func() {
		read()
		if saving || !st.CanAdd() {
			return
		}
		saving = true
		add.SetSensitive(false)
		problem.SetVisible(false)
		form := st.Form()
		p := api.ContactCreateParams{BookID: form.Book, Card: form.Card()}
		account := c.accountLabel(form.Book)
		var r api.ContactCreateResult
		c.call(api.MethodContactCreate, p, &r, contactsctl.WriteTimeout, func(e *api.Error) {
			saving = false
			if e != nil {
				problem.SetLabel(h.v.win.contactsOptions().ErrorMessage(e, account, ""))
				problem.SetVisible(true)
				read()
				return
			}
			pop.Popdown()
			h.v.say(contacts.ContactAdded(tr))
			c.lookup.Invalidate()
			c.askAll()
		})
	}
	add.ConnectClicked(save)
	name.ConnectActivate(save)
	email.ConnectActivate(save)
	more.ConnectClicked(func() {
		read()
		form := st.Form()
		pop.Popdown()
		h.v.win.newContactFrom(form)
	})

	pop.SetParent(anchor)
	pop.ConnectClosed(func() {
		// After the popover's own handling.
		glib.IdleAdd(func() { pop.Unparent() })
	})
	pop.Popup()
	name.GrabFocus()
	if st.SelectName {
		name.SelectRegion(0, -1)
	} else {
		name.SetPosition(-1)
	}
}
