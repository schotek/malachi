// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Add to Contacts… of an address chip (W4, A12): a popover with the name
// and the address of the header prefilled — the name selected and
// SenderNameWarning under it, because the sender wrote it — and the book
// to save in. Nothing is saved before Add; More Details… opens the whole
// form with the same values. Show Contact is offered only for an address
// contact.lookup knows exactly, never by name.

// BookChoice is a book in a choice of books.
type BookChoice struct {
	Book api.AddressBookID
	Text string // BookChoiceText
}

// BookChoices are the books a new contact can be created in (Writable),
// in the order of books; account names each one's account for the text.
func BookChoices(books []api.AddressBook, accounts []api.Account, tr Translator) []BookChoice {
	names := map[api.AccountID]string{}
	for _, a := range accounts {
		names[a.ID] = AccountName(a)
	}
	var out []BookChoice
	for _, b := range books {
		if Writable(b) {
			out = append(out, BookChoice{Book: b.ID, Text: BookChoiceText(BookName(b, tr), names[b.AccountID], tr)})
		}
	}
	return out
}

// DefaultBook is the book a new contact goes to: the selected book when it
// is writable (New Contact in a book), else the first visible writable
// book of account (the message's account for Add to Contacts…), else the
// first visible writable book, else the first writable one; "" none.
func DefaultBook(books []api.AddressBook, selected api.AddressBookID, account api.AccountID) api.AddressBookID {
	for _, b := range books {
		if selected != "" && b.ID == selected && Writable(b) {
			return b.ID
		}
	}
	pick := func(ok func(api.AddressBook) bool) api.AddressBookID {
		for _, b := range books {
			if Writable(b) && ok(b) {
				return b.ID
			}
		}
		return ""
	}
	if account != "" {
		if id := pick(func(b api.AddressBook) bool { return b.Visible && b.AccountID == account }); id != "" {
			return id
		}
	}
	if id := pick(func(b api.AddressBook) bool { return b.Visible }); id != "" {
		return id
	}
	return pick(func(api.AddressBook) bool { return true })
}

// AddFromChip is the state of Add to Contacts….
type AddFromChip struct {
	Name    string
	Address string
	// SelectName: the name field starts selected (a name came from the
	// header).
	SelectName bool
	// Warning is SenderNameWarning when the name came from the header ("" otherwise).
	Warning string
	Books   []BookChoice
	Book    api.AddressBookID
}

// NewAddFromChip prefills Add to Contacts… for an address of a header of a
// message of account.
func NewAddFromChip(addr api.Address, account api.AccountID, books []api.AddressBook, accounts []api.Account, tr Translator) AddFromChip {
	a := AddFromChip{
		Name:    Clean(addr.Name, api.MaxContactNameBytes),
		Address: Clean(addr.Address, api.MaxContactFieldBytes),
		Books:   BookChoices(books, accounts, tr),
		Book:    DefaultBook(books, "", account),
	}
	if strings.EqualFold(a.Name, a.Address) {
		a.Name = ""
	}
	if a.Name != "" {
		a.SelectName = true
		a.Warning = SenderNameWarning(tr)
	}
	return a
}

// CanAdd reports that Add may save: a book and a valid address.
func (a AddFromChip) CanAdd() bool { return a.Book != "" && ValidEmail(strings.TrimSpace(a.Address)) }

// Form is the form Add saves, and More Details… opens: a new contact in
// the chosen book with the name split (SplitName) and the address without
// a label.
func (a AddFromChip) Form() *Form {
	f := NewForm(a.Book)
	f.Given, f.Family = SplitName(a.Name)
	if addr := strings.TrimSpace(a.Address); addr != "" {
		f.Emails = []LabelledValue{{Value: addr}}
	}
	return f
}

// ChipItem is the address chip's contacts item.
type ChipItem int

// The chip's items.
const (
	// ChipNone: nothing (no address book can be written and none knows
	// the address, or the lookup has not answered).
	ChipNone ChipItem = iota
	// ChipShow is Show Contact (contact.lookup knows the address).
	ChipShow
	// ChipAdd is Add to Contacts… (a writable book exists).
	ChipAdd
)

// ChipItemFor is the chip's item: Show Contact for a known address, else
// Add to Contacts… when some book can take it; known is the lookup's
// answer (false: not known or not asked yet).
func ChipItemFor(known bool, books []api.AddressBook) ChipItem {
	if known {
		return ChipShow
	}
	for _, b := range books {
		if Writable(b) {
			return ChipAdd
		}
	}
	return ChipNone
}

// ChipItemText is the item's text ("" for ChipNone).
func ChipItemText(i ChipItem, tr Translator) string {
	switch i {
	case ChipShow:
		return ShowContact(tr)
	case ChipAdd:
		return AddToContacts(tr)
	}
	return ""
}
