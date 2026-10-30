// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package contacts is the reference of the Contacts window's pure UI logic
// and of every text it shows (docs/plans/address-book.md §5, wireframes
// W1–W6): the sidebar of address books, the list with its index sections,
// the contact's detail rows and actions, the edit form with its validation
// and conflict rebase, Add to Contacts… of an address chip, the
// preferences' texts, the errors by code and the window's breakpoints.
//
// The package is pure (no GTK, no gettext, no cgo): the caller passes a
// Translator (the method set of board.Translator, so GTK passes i18n.Tr)
// and, for birthdays, a Strftime. The macOS client ports it 1:1 into
// MalachiCore/Contacts and Windows into Malachi.Core/Contacts (class
// ContactsText) with key = msgid. Every string of a book or a contact is
// untrusted display text the daemon cleaned; the package cleans it again
// (Clean) and formats it into its msgids, never concatenating sentences.
package contacts

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// Translator translates a msgid of the malachi domain: T a plain one, N a
// plural form for n, C one with a context (board.Translator).
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// Strftime writes t with a strftime format in the user's locale (the
// signature of calendar.Strftime; calendar.EnglishStrftime in the tests).
type Strftime func(t time.Time, format string) string

// ---------------------------------------------------------------------------
// The window
// ---------------------------------------------------------------------------

// WindowTitle is the window's title, the menu item that opens it (Window ▸
// Contacts, the main menu's Contacts, … ▸ Contacts), the Preferences tab
// and the narrow window's back button (‹ Contacts).
func WindowTitle(tr Translator) string {
	// TRANSLATORS: the Contacts window (address books of the user's
	// accounts), its menu item and its Preferences page.
	return tr.T("Contacts")
}

// AllContacts is the sidebar's first row: every visible address book.
func AllContacts(tr Translator) string {
	// TRANSLATORS: the Contacts window's sidebar row that lists the
	// contacts of every address book.
	return tr.T("All Contacts")
}

// RecentlyUsed is the virtual book of the addresses the user wrote to.
func RecentlyUsed(tr Translator) string {
	// TRANSLATORS: the Contacts window's address book of the addresses the
	// user has written to (read only).
	return tr.T("Recently Used")
}

// OnThisComputer heads the sidebar's group of Recently Used (the macOS
// client says On This Mac, a macOS-only string).
func OnThisComputer(tr Translator) string {
	// TRANSLATORS: the Contacts window's sidebar heading of the address
	// book kept on this computer (Recently Used).
	return tr.T("On This Computer")
}

// SearchContacts is the placeholder of the window's search field.
func SearchContacts(tr Translator) string { return tr.T("Search Contacts") }

// NewContact is the toolbar's "+", its tooltip and the form's title for a
// new contact.
func NewContact(tr Translator) string { return tr.T("New Contact") }

// ContactCount is a book's count of contacts, the list's subtitle.
func ContactCount(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d contact", "%d contacts", n), n)
}

// AddressCount is Recently Used's count, the list's subtitle.
func AddressCount(n int, tr Translator) string {
	// TRANSLATORS: the number of recently used e-mail addresses.
	return fmt.Sprintf(tr.N("%d address", "%d addresses", n), n)
}

// TwoParts joins two parts of one line ("All Contacts · 412", "Job ·
// Company", a suggestion's "Contacts · Microsoft 365").
func TwoParts(a, b string, tr Translator) string {
	// TRANSLATORS: two parts of one line, e.g. "All Contacts · 412" or
	// "Contacts · Microsoft 365" (address book and account).
	return fmt.Sprintf(tr.T("%s · %s"), a, b)
}

// SyncedLine is the status bar's text: last is the oldest lastSyncAt of
// the visible books (SyncedAt).
func SyncedLine(last, now time.Time, tr Translator) string {
	// TRANSLATORS: the Contacts window's status bar; %s is a relative time
	// such as "3 minutes ago".
	return fmt.Sprintf(tr.T("Contacts synchronized %s"), board.RelativeTime(last, now, tr))
}

// ---------------------------------------------------------------------------
// The detail
// ---------------------------------------------------------------------------

// WriteEmail is the detail's button that opens New Message to the contact.
func WriteEmail(tr Translator) string {
	// TRANSLATORS: the contact detail's button that writes a new message to
	// the contact.
	return tr.T("Write Email")
}

// ShowMail is the detail's button that searches the mail from the contact.
func ShowMail(tr Translator) string { return tr.T("Show Mail") }

// Copy is the detail's button that copies the contact as text.
func Copy(tr Translator) string {
	// TRANSLATORS: the contact detail's button that copies the whole
	// contact as plain text.
	return tr.T("Copy")
}

// Edit is the detail's button that opens the form.
func Edit(tr Translator) string { return tr.T("Edit") }

// Done is the form's button that saves to the server.
func Done(tr Translator) string {
	// TRANSLATORS: the contact form's button that saves the contact.
	return tr.C("contacts", "Done")
}

// Cancel is the form's and the questions' Cancel.
func Cancel(tr Translator) string { return tr.T("Cancel") }

// DeleteContact is the form's button that asks to delete the contact.
func DeleteContact(tr Translator) string { return tr.T("Delete Contact…") }

// Delete is the delete question's button.
func Delete(tr Translator) string { return tr.T("Delete") }

// ReadOnly is the capsule in place of Edit for a read-only book.
func ReadOnly(tr Translator) string {
	// TRANSLATORS: a capsule beside a contact whose address book cannot be
	// changed.
	return tr.T("Read Only")
}

// NoName stands for a contact without a name, company or address.
func NoName(tr Translator) string {
	// TRANSLATORS: a contact without any name.
	return tr.T("No Name")
}

// SourceLine is the detail's footer: the book and the account
// (AccountLabel), "Address book Contacts · Microsoft 365 (jana@firma.cz)".
func SourceLine(book, account string, tr Translator) string {
	// TRANSLATORS: the contact detail's footer; the first %s is the
	// address book's name, the second the account it belongs to.
	return fmt.Sprintf(tr.T("Address book %s · %s"), book, account)
}

// ---------------------------------------------------------------------------
// Address chips and suggestions
// ---------------------------------------------------------------------------

// ShowContact is the address chip's item for an address with a contact.
func ShowContact(tr Translator) string { return tr.T("Show Contact") }

// AddToContacts is the address chip's item for an address without one.
func AddToContacts(tr Translator) string { return tr.T("Add to Contacts…") }

// AddTitle heads the Add to Contacts popover.
func AddTitle(tr Translator) string { return AddToContacts(tr) }

// Add is the Add to Contacts popover's button.
func Add(tr Translator) string {
	// TRANSLATORS: the Add to Contacts popover's button that saves the
	// new contact.
	return tr.C("contacts", "Add")
}

// MoreDetails opens the full form from the Add to Contacts popover.
func MoreDetails(tr Translator) string { return tr.T("More Details…") }

// SenderNameWarning stands under the prefilled name of Add to Contacts:
// the name is the sender's text.
func SenderNameWarning(tr Translator) string {
	// TRANSLATORS: under the name of a new contact taken from a message's
	// header: the sender wrote it, so it may be false.
	return tr.T("The sender wrote this name — check it.")
}

// SuggestionTooltip is the tooltip of a recipient suggestion of the
// source contacts: the book and the account's label (AccountLabel, ""
// when unknown).
func SuggestionTooltip(book, account string, tr Translator) string {
	switch {
	case book == "" && account == "":
		// TRANSLATORS: tooltip of a recipient suggestion that came from a
		// system address book whose name is unknown.
		return tr.T("Address book")
	case account == "":
		return book
	case book == "":
		return account
	}
	return TwoParts(book, account, tr)
}

// ---------------------------------------------------------------------------
// Questions, errors and states
// ---------------------------------------------------------------------------

// Question is what a dialog asks; Confirm is its default button.
type Question struct {
	Heading, Body string
	Confirm       string
	// Destructive: Confirm deletes.
	Destructive bool
}

// DeleteQuestion asks before a contact is deleted; account is its
// AccountLabel. CardDAV has no trash, so it is always asked.
func DeleteQuestion(name, account string, tr Translator) Question {
	return Question{
		// TRANSLATORS: the question before a contact is deleted; %s is its
		// name.
		Heading: fmt.Sprintf(tr.T("Delete contact %s?"), name),
		// TRANSLATORS: %s is the account, e.g. "Microsoft 365
		// (jana@example.org)".
		Body:        fmt.Sprintf(tr.T("The contact will also be deleted on the server %s and on every device that synchronizes it."), account),
		Confirm:     Delete(tr),
		Destructive: true,
	}
}

// ConflictQuestion is asked when the server's contact changed since the
// form opened (contactConflict).
func ConflictQuestion(tr Translator) Question {
	return Question{
		Heading: tr.T("The contact changed meanwhile"),
		Body:    tr.T("Someone changed the contact on another device. Load the new version; the details you changed stay in the form."),
		// TRANSLATORS: the button that loads the server's new version of a
		// contact into the form.
		Confirm: tr.T("Reload"),
	}
}

// ConsentBanner says that a Microsoft account's sign-in lacks contacts
// access (contactsConsentRequired); account is its address.
func ConsentBanner(account string, tr Translator) string {
	return fmt.Sprintf(tr.T("Microsoft needs your permission to access the contacts of %s."), account)
}

// Allow is the consent banner's button (account.oauthStart contacts:
// true).
func Allow(tr Translator) string {
	// TRANSLATORS: the button that signs in again to give Microsoft's
	// permission to the contacts.
	return tr.T("Allow…")
}

// ConsentOnline is the consent banner's advice for an account of GNOME
// Online Accounts (signIn false), which has no Allow….
func ConsentOnline(tr Translator) string {
	return tr.T("Allow contacts access in the desktop’s Online Accounts settings.")
}

// Empty is an empty state of the window.
type Empty int

// The empty states.
const (
	// EmptyNone: something to show.
	EmptyNone Empty = iota
	// EmptyNoBooks: no address book but Recently Used (W5 a), with Open
	// Preferences….
	EmptyNoBooks
	// EmptyBook: the selected book has no contact.
	EmptyBook
	// EmptySearch: the search found nothing.
	EmptySearch
	// EmptySelection: no contact selected (the detail).
	EmptySelection
)

// EmptyText is an empty state's title and body ("" none).
func EmptyText(e Empty, tr Translator) (title, body string) {
	switch e {
	case EmptyNoBooks:
		return tr.T("No Address Books"),
			tr.T("Turn on contacts from Microsoft 365 or a CardDAV server in Preferences. Until then, here are the addresses you have written to.")
	case EmptyBook:
		return tr.T("No Contacts"), ""
	case EmptySearch:
		return tr.T("No Contacts Found"), tr.T("Try another name, company or address.")
	case EmptySelection:
		return tr.T("No Contact Selected"), ""
	}
	return "", ""
}

// OpenPreferences is EmptyNoBooks' button (Preferences → Contacts).
func OpenPreferences(tr Translator) string { return tr.T("Open Preferences…") }

// ErrorText is the sentence of a failed contacts call by its code; false
// when the contacts have none (the caller then writes its usual sentence
// of a failed call). account is the AccountLabel of the contact's account
// ("" unknown), for contactsConsentRequired. Never the daemon's message.
func ErrorText(code api.ErrorCode, account string, tr Translator) (string, bool) {
	switch code {
	case api.CodeContactNotFound:
		return tr.T("The contact no longer exists."), true
	case api.CodeAddressBookNotFound:
		return tr.T("The address book no longer exists."), true
	case api.CodeAddressBookReadOnly:
		return tr.T("This address book cannot be changed."), true
	case api.CodeContactConflict:
		return ConflictQuestion(tr).Heading, true
	case api.CodeContactsConsentRequired:
		if account != "" {
			return ConsentBanner(account, tr), true
		}
		return tr.T("Microsoft needs your permission to access these contacts."), true
	case api.CodeOffline:
		return tr.T("Contacts cannot be changed while offline."), true
	case api.CodeInvalidArgument:
		return tr.T("The contact could not be saved. Check its details."), true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Preferences → Contacts and the CalDAV wizard
// ---------------------------------------------------------------------------

// SyncContacts is a Microsoft account's switch (graph.contacts.sync).
func SyncContacts(tr Translator) string { return tr.T("Sync Contacts") }

// CardDAVNote is under a CardDAV account's books: its connection is
// edited on the Calendar page.
func CardDAVNote(tr Translator) string {
	return tr.T("Edit the server, password and certificate on the Calendar page.")
}

// SuggestionsGroup heads the group of the setting contacts-suggest-scope.
func SuggestionsGroup(tr Translator) string {
	// TRANSLATORS: the Preferences group that decides which address books
	// complete the recipients of a new message.
	return tr.T("Recipient Suggestions")
}

// ScopeText is a choice of Recipient Suggestions.
func ScopeText(s api.ContactSearchScope, tr Translator) string {
	if s == api.ContactScopeAll {
		return tr.T("All Address Books")
	}
	return tr.T("Address Books of the Account You Write From")
}

// Scopes are Recipient Suggestions' choices in their order.
var Scopes = []api.ContactSearchScope{api.ContactScopeAccount, api.ContactScopeAll}

// NoContactAccounts is Preferences → Contacts without an account that can
// have contacts.
func NoContactAccounts(tr Translator) string {
	return tr.T("Add a Microsoft 365 account or a CardDAV server to see its contacts here.")
}

// AddressBooksHeading heads the CalDAV wizard's found address books.
func AddressBooksHeading(tr Translator) string {
	// TRANSLATORS: the heading of the address books a CardDAV server has.
	return tr.T("Address Books")
}

// CalendarsHeading heads the CalDAV wizard's found calendars.
func CalendarsHeading(tr Translator) string { return tr.T("Calendars") }

// WizardTitle is the CalDAV wizard's title once it also adds contacts.
func WizardTitle(tr Translator) string {
	return tr.T("Add Calendar and Contacts (CalDAV/CardDAV)")
}
