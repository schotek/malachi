// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// The contact shown in the detail and edited on its card (W1, W4): the
// card from contact.get, the form (contacts.Form) while editing, Done as
// contact.create or contact.update with the card's revision, Delete
// Contact… with its question and contact.delete. Writes go to the server
// (online only): a failure keeps the form open with the sentence (offline,
// read only, a refused card), a contactConflict asks ConflictQuestion and
// Reload rebases the form on the server's version keeping what the user
// changed; nothing is overwritten blindly. An unchanged form's Done sends
// nothing.

// BookResolver finds a book and its account (WindowController.Book).
type BookResolver func(api.AddressBookID) (api.AddressBook, *api.Account, bool)

// ContactController is the controller of the detail and the form.
type ContactController struct {
	Opts Options
	// Books resolves the card's book for the source line, the question's
	// account and New Contact's book; nil knows none.
	Books BookResolver

	// OnChange: the card, the form or a state changed.
	OnChange func()
	// OnQuestion asks; answer(true) is the question's Confirm, never
	// answering is Cancel.
	OnQuestion func(q contacts.Question, answer func(bool))
	// OnCreated: Done created the contact (the window selects it and
	// reloads).
	OnCreated func(api.ContactID)
	// OnDeleted: the contact was deleted (or was gone already).
	OnDeleted func(api.ContactID)

	id       api.ContactID
	card     *api.ContactCard
	form     *contacts.Form
	problems []contacts.Problem
	loading  bool
	saving   bool
	deleting bool
	err      string
	gen      int
	closed   bool
}

// NewContactController is the detail's controller.
func NewContactController(opts Options, books BookResolver) *ContactController {
	return &ContactController{Opts: opts, Books: books}
}

// Stop ends the controller: no answer is taken any more.
func (c *ContactController) Stop() { c.closed = true }

func (c *ContactController) changed() {
	if c.OnChange != nil && !c.closed {
		c.OnChange()
	}
}

// ID is the shown contact ("" none or a new one).
func (c *ContactController) ID() api.ContactID { return c.id }

// Card is the shown card (nil before contact.get answers).
func (c *ContactController) Card() *api.ContactCard { return c.card }

// Loading reports a contact.get without its answer.
func (c *ContactController) Loading() bool { return c.loading }

// Saving reports Done without its answer; Deleting Delete's.
func (c *ContactController) Saving() bool   { return c.saving }
func (c *ContactController) Deleting() bool { return c.deleting }

// Error is the last failure's sentence ("" none).
func (c *ContactController) Error() string { return c.err }

// Editing reports the form; Form is it (nil when not editing).
func (c *ContactController) Editing() bool        { return c.form != nil }
func (c *ContactController) Form() *contacts.Form { return c.form }

// Problems are the form's problems of the last Done.
func (c *ContactController) Problems() []contacts.Problem { return c.problems }

// Detail is the detail of the shown card.
func (c *ContactController) Detail() (contacts.Detail, bool) {
	if c.card == nil {
		return contacts.Detail{}, false
	}
	return contacts.DetailOf(*c.card, c.Opts.Format, c.Opts.Tr), true
}

// account is the card's (or the new contact's) book and account label.
func (c *ContactController) account(book api.AddressBookID) (api.AddressBook, string) {
	if c.Books == nil {
		return api.AddressBook{}, ""
	}
	b, a, ok := c.Books(book)
	if !ok {
		return api.AddressBook{}, ""
	}
	return b, contacts.AccountLabel(a, c.Opts.Tr)
}

// SourceLine is the detail's footer ("Address book Contacts · Microsoft
// 365 (…)"); Recently Used says On This Computer.
func (c *ContactController) SourceLine() string {
	if c.card == nil {
		return ""
	}
	b, acc := c.account(c.card.BookID)
	if b.ID == "" {
		return ""
	}
	if b.ID == api.AddressBookRecent || acc == "" {
		acc = contacts.OnThisComputer(c.Opts.Tr)
	}
	return contacts.SourceLine(contacts.BookName(b, c.Opts.Tr), acc, c.Opts.Tr)
}

// CopyText is Copy's text.
func (c *ContactController) CopyText() string {
	if c.card == nil {
		return ""
	}
	return contacts.CopyText(*c.card, c.Opts.Format, c.Opts.Tr)
}

// Open shows a contact ("" clears the detail); a form being edited is
// dropped (the window asks before it selects another contact).
func (c *ContactController) Open(id api.ContactID) {
	c.gen++
	c.id, c.card, c.form, c.problems, c.err = id, nil, nil, nil, ""
	c.saving, c.deleting = false, false
	if id == "" {
		c.loading = false
		c.changed()
		return
	}
	c.load(false)
	c.changed()
}

// load asks contact.get; quiet keeps the shown card while it loads.
func (c *ContactController) load(quiet bool) {
	gen, id := c.gen, c.id
	c.loading = !quiet
	call(c.Opts, api.MethodContactGet, api.ContactGetParams{ContactID: id}, 0, func(r *api.ContactGetResult, e *api.Error) {
		if c.closed || gen != c.gen {
			return
		}
		c.loading = false
		switch {
		case e == nil:
			card := r.Contact
			c.card = &card
		case e.Code == api.CodeContactNotFound:
			c.card = nil
			c.err = c.Opts.ErrorMessage(e, "", "")
		default:
			c.err = c.Opts.ErrorMessage(e, "", "")
		}
		c.changed()
	})
}

// ContactsChanged takes notify.contactsChanged: the shown card is asked
// again when its book (or any of its account) changed and no form is
// open (a form meets the change as a conflict on Done).
func (c *ContactController) ContactsChanged(n api.ContactsChangedNotification) {
	if c.card == nil || c.form != nil || c.saving || c.deleting || c.id == "" {
		return
	}
	b, a, _ := c.resolve(c.card.BookID)
	if a != nil && n.AccountID != "" && n.AccountID != a.ID {
		return
	}
	if a == nil && n.AccountID != "" && b.AccountID != n.AccountID {
		return
	}
	if len(n.BookIDs) > 0 && !slices.Contains(n.BookIDs, c.card.BookID) {
		return
	}
	c.load(true)
}

func (c *ContactController) resolve(id api.AddressBookID) (api.AddressBook, *api.Account, bool) {
	if c.Books == nil {
		return api.AddressBook{}, nil, false
	}
	return c.Books(id)
}

// CanEdit reports Edit: a card of a writable book.
func (c *ContactController) CanEdit() bool {
	return c.card != nil && !c.card.ReadOnly && c.form == nil && !c.deleting
}

// BeginEdit opens the form over the shown card.
func (c *ContactController) BeginEdit() {
	if !c.CanEdit() {
		return
	}
	c.form = contacts.EditForm(*c.card)
	c.problems, c.err = nil, ""
	c.changed()
}

// BeginNew opens the form of a new contact in book (the detail shows no
// card meanwhile); form, when not nil, is prefilled (Add to Contacts' More
// Details…).
func (c *ContactController) BeginNew(book api.AddressBookID, form *contacts.Form) {
	c.gen++
	c.id, c.card, c.loading, c.saving, c.deleting = "", nil, false, false, false
	if form == nil {
		form = contacts.NewForm(book)
	}
	c.form = form
	c.problems, c.err = nil, ""
	c.changed()
}

// Dirty reports a form with a change (closing or selecting another
// contact asks first).
func (c *ContactController) Dirty() bool { return c.form != nil && c.form.Dirty() }

// CancelEdit closes the form without saving (Cancel).
func (c *ContactController) CancelEdit() {
	if c.form == nil || c.saving {
		return
	}
	c.form, c.problems, c.err = nil, nil, ""
	c.changed()
}

// Save is Done: the form validated, then created or updated on the
// server.
func (c *ContactController) Save() {
	f := c.form
	if f == nil || c.saving {
		return
	}
	c.problems = f.Validate(c.Opts.Tr)
	if len(c.problems) > 0 {
		c.changed()
		return
	}
	if !f.IsNew && !f.Dirty() {
		c.form, c.err = nil, ""
		c.changed()
		return
	}
	c.saving, c.err = true, ""
	gen := c.gen
	_, acc := c.account(f.Book)
	if f.IsNew {
		p := api.ContactCreateParams{BookID: f.Book, Card: f.Card()}
		call(c.Opts, api.MethodContactCreate, p, WriteTimeout, func(r *api.ContactCreateResult, e *api.Error) {
			if c.closed || gen != c.gen {
				return
			}
			c.saving = false
			if e != nil {
				c.err = c.Opts.ErrorMessage(e, acc, "")
				c.changed()
				return
			}
			f.Saved(r.ContactID, r.Revision)
			card := f.Original
			c.id, c.card, c.form = r.ContactID, &card, nil
			c.load(true)
			c.changed()
			if c.OnCreated != nil {
				c.OnCreated(r.ContactID)
			}
		})
		c.changed()
		return
	}
	p := api.ContactUpdateParams{ContactID: f.Original.ID, Revision: f.Original.Revision, Card: f.Card()}
	call(c.Opts, api.MethodContactUpdate, p, WriteTimeout, func(r *api.ContactUpdateResult, e *api.Error) {
		if c.closed || gen != c.gen {
			return
		}
		c.saving = false
		if e != nil {
			c.saveFailed(f, e, acc)
			return
		}
		f.Saved("", r.Revision)
		card := f.Original
		c.card, c.form = &card, nil
		c.load(true)
		c.changed()
	})
	c.changed()
}

// saveFailed keeps the form: a conflict asks for Reload, anything else
// shows its sentence.
func (c *ContactController) saveFailed(f *contacts.Form, e *api.Error, acc string) {
	c.err = c.Opts.ErrorMessage(e, acc, "")
	if e.Code != api.CodeContactConflict {
		c.changed()
		return
	}
	server, ok := ConflictCard(e)
	ask := func(server api.ContactCard) {
		if c.OnQuestion == nil {
			return
		}
		gen := c.gen
		c.OnQuestion(contacts.ConflictQuestion(c.Opts.Tr), func(reload bool) {
			if !reload || c.closed || gen != c.gen || c.form != f {
				return
			}
			f.Rebase(server)
			s := server
			c.card, c.err = &s, ""
			c.changed()
		})
	}
	c.changed()
	if ok {
		ask(server)
		return
	}
	// A conflict without the server's version: ask contact.get for it.
	gen := c.gen
	call(c.Opts, api.MethodContactGet, api.ContactGetParams{ContactID: f.Original.ID}, 0, func(r *api.ContactGetResult, e *api.Error) {
		if c.closed || gen != c.gen || e != nil {
			return
		}
		ask(r.Contact)
	})
}

// CanDelete reports Delete Contact…: a card of a writable book.
func (c *ContactController) CanDelete() bool {
	return c.card != nil && !c.card.ReadOnly && !c.saving && !c.deleting
}

// Delete asks DeleteQuestion and then deletes the contact on the server.
func (c *ContactController) Delete() {
	if !c.CanDelete() || c.OnQuestion == nil {
		return
	}
	card := *c.card
	_, acc := c.account(card.BookID)
	gen := c.gen
	q := contacts.DeleteQuestion(contacts.Title(card, c.Opts.Tr), acc, c.Opts.Tr)
	c.OnQuestion(q, func(yes bool) {
		if !yes || c.closed || gen != c.gen || c.deleting {
			return
		}
		c.deleting, c.err = true, ""
		c.changed()
		p := api.ContactDeleteParams{ContactID: card.ID, Revision: card.Revision}
		call(c.Opts, api.MethodContactDelete, p, WriteTimeout, func(_ *api.ContactDeleteResult, e *api.Error) {
			if c.closed || gen != c.gen {
				return
			}
			c.deleting = false
			switch {
			case e == nil, e.Code == api.CodeContactNotFound:
				c.gen++
				c.id, c.card, c.form = "", nil, nil
				c.changed()
				if c.OnDeleted != nil {
					c.OnDeleted(card.ID)
				}
				return
			case e.Code == api.CodeContactConflict:
				// Somebody changed it meanwhile: show the new version, the
				// user decides again.
				if s, ok := ConflictCard(e); ok {
					c.card = &s
				} else {
					c.load(true)
				}
			}
			c.err = c.Opts.ErrorMessage(e, acc, "")
			c.changed()
		})
	})
}
