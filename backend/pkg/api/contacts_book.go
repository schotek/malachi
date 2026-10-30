// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"time"
)

// Address books and contacts of connected services (docs/api.md §4.19; the
// design is docs/plans/address-book.md). The daemon keeps a cache of the
// server's address books (Microsoft Graph contact folders, CardDAV
// collections) and a virtual read-only book of recently used addresses;
// writes go to the server first and change the cache only after it
// accepted them. Every string of these types that comes from a server or
// a vCard (names, organisations, labels, notes, book names) is untrusted
// display text: cleaned by the daemon and shown as plain text.

// ---------------------------------------------------------------------------
// Limits (docs/plans/address-book.md §4.2). Exceeding an input limit is
// invalidArgument; a server's data over a stored limit is truncated or
// skipped.
// ---------------------------------------------------------------------------

const (
	DefaultContactListLimit   = 100
	MaxContactListLimit       = 500
	MaxVCardBytes             = 1 << 20 // one vCard as stored or sent
	MaxVCardProperties        = 500
	MaxContactLookupAddresses = 200
	MaxAddressBooksPerAccount = 200
	MaxContactsPerBook        = 50000
	MaxContactNameBytes       = 256 // each ContactName part, DisplayName, Nickname
	MaxContactFieldBytes      = 1024
	MaxContactLabelBytes      = 64
	MaxContactNoteBytes       = 32 << 10
	MaxContactEmails          = 50
	MaxContactPhones          = 50
	MaxContactAddresses       = 20
	MaxContactURLs            = 20
)

// ---------------------------------------------------------------------------
// Identifiers and enumerations
// ---------------------------------------------------------------------------

// AddressBookID names an address book ("b_…"), opaque; AddressBookRecent
// is the virtual book of recently used addresses.
type AddressBookID string

// ContactID names a contact ("p_…"), opaque. A contact of the recent book
// has an id too ("r_…"), valid for contact.get only.
type ContactID string

const (
	// AddressBookRecent is the id of the virtual book of recently used
	// addresses (collected from mail the user sent), read only.
	AddressBookRecent AddressBookID = "recent"
	// AddressBookAll in contact.list lists every visible book.
	AddressBookAll AddressBookID = "all"
)

// AddressBookKind is where an address book comes from. Open enum: an
// unknown value is treated as a read-only book of an unknown source.
type AddressBookKind string

const (
	AddressBookKindGraph   AddressBookKind = "graph"   // a Microsoft Graph contact folder
	AddressBookKindCardDAV AddressBookKind = "carddav" // a CardDAV collection of a caldav account
	AddressBookKindRecent  AddressBookKind = "recent"  // AddressBookRecent
)

// ContactLabel is the label of an e-mail address, phone number, postal
// address or URL. Open enum: a value outside the constants is the
// server's own label, untrusted display text (≤ MaxContactLabelBytes);
// "" means none.
type ContactLabel string

const (
	ContactLabelHome   ContactLabel = "home"
	ContactLabelWork   ContactLabel = "work"
	ContactLabelOther  ContactLabel = "other"
	ContactLabelMobile ContactLabel = "mobile" // phones
	ContactLabelFax    ContactLabel = "fax"    // phones
	ContactLabelPager  ContactLabel = "pager"  // phones
	ContactLabelMain   ContactLabel = "main"   // phones
)

// ContactSearchScope is which address books contact.search reads.
type ContactSearchScope string

const (
	// ContactScopeAccount: the books of the sending account and the
	// recent book (the default when empty).
	ContactScopeAccount ContactSearchScope = "account"
	// ContactScopeAll: every visible book of every enabled account.
	ContactScopeAll ContactSearchScope = "all"
)

// ContactSourceContacts: a contact.search suggestion from an address book
// the daemon synchronises (Contact.Book names it).
const ContactSourceContacts ContactSource = "contacts"

// CapabilityContacts: the account can have address books (graph, caldav);
// it does not change with consent or sync state.
const CapabilityContacts AccountCapability = "contacts"

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

// ContactsConfig is the contacts part of a graph account
// (GraphConfig.Contacts); a caldav account uses CalDAVConfig.Contacts
// instead.
type ContactsConfig struct {
	// Sync: synchronise the account's contact folders; default false.
	Sync bool `json:"sync"`
}

// CardDAVAddressBook is an address book collection of a CardDAV server
// (account.listCalDAVCalendars).
type CardDAVAddressBook struct {
	Href     string `json:"href"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// ContactsConsentRequiredData is Error.Data of contactsConsentRequired.
// SignIn: account.oauthStart with Contacts can fix it (source daemon);
// false for GOA (the desktop's Online Accounts settings).
type ContactsConsentRequiredData struct {
	AccountID AccountID `json:"accountId"`
	SignIn    bool      `json:"signIn"`
}

// ContactConflictData is Error.Data of contactConflict: the server's
// current version of the contact, as contact.get returns it.
// ContactFieldData is the Error.Data of an invalidArgument from
// contact.create and contact.update that names the refused field of the
// card as a path relative to it: "emails[2].address", "name.given",
// "addresses[0]", "emails" (the whole list). Clients mark that field;
// the message is not for display.
type ContactFieldData struct {
	Field string `json:"field"`
}

type ContactConflictData struct {
	Server ContactCard `json:"server"`
}

// ---------------------------------------------------------------------------
// Common types
// ---------------------------------------------------------------------------

// AddressBook is one address book.
type AddressBook struct {
	ID AddressBookID `json:"id"`
	// AccountID is empty for the recent book.
	AccountID AccountID       `json:"accountId,omitempty"`
	Name      string          `json:"name"`
	Kind      AddressBookKind `json:"kind"`
	ReadOnly  bool            `json:"readOnly"`
	// SearchOnly: the book cannot be listed whole, only searched (no kind
	// of stage 1 sets it).
	SearchOnly bool `json:"searchOnly,omitempty"`
	// Visible: the user's choice (addressBook.set); hidden books are
	// neither listed by contact.list "all" nor searched.
	Visible    bool       `json:"visible"`
	Count      int        `json:"count"`
	LastSyncAt *time.Time `json:"lastSyncAt,omitempty"`
}

// ContactSummary is a row of the contact list.
type ContactSummary struct {
	ID          ContactID     `json:"id"`
	BookID      AddressBookID `json:"bookId"`
	DisplayName string        `json:"displayName"`
	// SortKey orders the list (family name first when known), compared
	// by the client's collation; Section is its index letter, upper
	// case, or "#".
	SortKey string `json:"sortKey"`
	Section string `json:"section"`
	Org     string `json:"org,omitempty"`
	Email   string `json:"email,omitempty"` // the first address
}

// ContactName is the structured name of a contact (vCard N).
type ContactName struct {
	Given  string `json:"given,omitempty"`
	Family string `json:"family,omitempty"`
	Middle string `json:"middle,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

type ContactEmail struct {
	Label   ContactLabel `json:"label,omitempty"`
	Address string       `json:"address"`
}

type ContactPhone struct {
	Label  ContactLabel `json:"label,omitempty"`
	Number string       `json:"number"`
}

type ContactAddress struct {
	Label    ContactLabel `json:"label,omitempty"`
	Street   string       `json:"street,omitempty"`
	City     string       `json:"city,omitempty"`
	Region   string       `json:"region,omitempty"`
	Postcode string       `json:"postcode,omitempty"`
	Country  string       `json:"country,omitempty"`
}

// ContactURL is a web address; clients open only http and https, through
// their "Open this link?".
type ContactURL struct {
	Label ContactLabel `json:"label,omitempty"`
	URL   string       `json:"url"`
}

// ContactBirthday: Year 0 = unknown.
type ContactBirthday struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// ContactCard is a whole contact. As input (contact.create, contact.update)
// ID, BookID, ReadOnly and Revision are ignored.
type ContactCard struct {
	ID       ContactID     `json:"id"`
	BookID   AddressBookID `json:"bookId"`
	ReadOnly bool          `json:"readOnly"`
	// Revision is the server's version (CardDAV ETag, Graph changeKey),
	// opaque; contact.update and contact.delete send it back.
	Revision    string           `json:"revision"`
	Name        ContactName      `json:"name"`
	DisplayName string           `json:"displayName"`
	Nickname    string           `json:"nickname,omitempty"`
	Org         string           `json:"org,omitempty"`
	Title       string           `json:"title,omitempty"`
	Emails      []ContactEmail   `json:"emails"`
	Phones      []ContactPhone   `json:"phones"`
	Addresses   []ContactAddress `json:"addresses"`
	URLs        []ContactURL     `json:"urls"`
	Birthday    *ContactBirthday `json:"birthday,omitempty"`
	Note        string           `json:"note,omitempty"`
}

// ---------------------------------------------------------------------------
// addressBook.* and contact.* (docs/api.md §4.19)
// ---------------------------------------------------------------------------

type AddressBookListParams struct {
	AccountID AccountID `json:"accountId,omitempty"` // empty = every account and the recent book
}

type AddressBookListResult struct {
	Books []AddressBook `json:"books"` // never null
}

type AddressBookSetParams struct {
	BookID  AddressBookID `json:"bookId"`
	Visible bool          `json:"visible"`
}

type AddressBookSetResult struct {
	Book AddressBook `json:"book"`
}

type ContactListParams struct {
	BookID AddressBookID `json:"bookId"` // a book or AddressBookAll
	// Query filters by name, organisation and address (≤
	// MaxContactQueryBytes); empty lists everything.
	Query  string `json:"query,omitempty"`
	Offset int    `json:"offset,omitempty"`
	// Limit: 0 = DefaultContactListLimit, more than MaxContactListLimit
	// is clamped.
	Limit int `json:"limit,omitempty"`
}

type ContactListResult struct {
	Contacts []ContactSummary `json:"contacts"` // never null; by sort key
	Total    int              `json:"total"`
}

type ContactGetParams struct {
	ContactID ContactID `json:"contactId"`
}

type ContactGetResult struct {
	Contact ContactCard `json:"contact"`
}

type ContactLookupParams struct {
	Addresses []string `json:"addresses"` // ≤ MaxContactLookupAddresses
}

// ContactLookupResult maps each address with a contact in a visible book
// (normalised, lower case) to that contact; unknown addresses are absent.
type ContactLookupResult struct {
	Known map[string]ContactID `json:"known"` // never null
}

type ContactCreateParams struct {
	BookID AddressBookID `json:"bookId"`
	Card   ContactCard   `json:"card"`
}

type ContactCreateResult struct {
	ContactID ContactID `json:"contactId"`
	Revision  string    `json:"revision"`
}

type ContactUpdateParams struct {
	ContactID ContactID   `json:"contactId"`
	Revision  string      `json:"revision"`
	Card      ContactCard `json:"card"`
}

type ContactUpdateResult struct {
	Revision string `json:"revision"`
}

type ContactDeleteParams struct {
	ContactID ContactID `json:"contactId"`
	Revision  string    `json:"revision"`
}

type ContactDeleteResult struct{}

// ContactsChangedNotification: the contacts of these books changed (a
// pass, a write, a book added or removed, addressBook.set). AccountID is
// empty for the recent book; no BookIDs = any book of the account.
type ContactsChangedNotification struct {
	AccountID AccountID       `json:"accountId,omitempty"`
	BookIDs   []AddressBookID `json:"bookIds,omitempty"`
}

// AddressBookService is the address-book part of the contract (§4.19),
// Backend.AddressBooks.
type AddressBookService interface {
	ListBooks(ctx context.Context, p AddressBookListParams) (*AddressBookListResult, error)
	SetBook(ctx context.Context, p AddressBookSetParams) (*AddressBookSetResult, error)
	ListContacts(ctx context.Context, p ContactListParams) (*ContactListResult, error)
	GetContact(ctx context.Context, p ContactGetParams) (*ContactGetResult, error)
	Lookup(ctx context.Context, p ContactLookupParams) (*ContactLookupResult, error)
	CreateContact(ctx context.Context, p ContactCreateParams) (*ContactCreateResult, error)
	UpdateContact(ctx context.Context, p ContactUpdateParams) (*ContactUpdateResult, error)
	DeleteContact(ctx context.Context, p ContactDeleteParams) (*ContactDeleteResult, error)
}
