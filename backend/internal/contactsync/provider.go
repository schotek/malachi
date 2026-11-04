// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package contactsync is the address book engine (docs/plans/address-book.md
// A4, A9): one syncer per account whose contacts are on, keeping the
// store's cache of the server's address books (ab_books, ab_contacts,
// ab_emails) up to date, and the Provider interface every source (CardDAV,
// Microsoft Graph) implements. It mirrors internal/calsync in small: it
// never imports a provider's package; core wires them.
//
// The server is the authority. A pass lists the books (keeping the user's
// visible), synchronises the visible ones and empties the hidden ones; it
// never writes to the server. Writes (contact.create, update, delete) are
// core's, through the provider, followed by StoreCard / RemoveCard.
package contactsync

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
)

// RemoteBook is an address book as the server lists it. Every string is
// untrusted; the engine cleans it before storing.
type RemoteBook struct {
	// RemoteID is the CardDAV collection URL (absolute) or the Graph
	// contact folder id.
	RemoteID string
	Name     string
	ReadOnly bool
}

// Card is one contact as the server has it.
type Card struct {
	RemoteID string
	// ETag is the CardDAV ETag or the Graph changeKey (the revision).
	ETag string
	// Raw is the vCard or the Graph JSON as received (≤ 1 MiB);
	// RawKind is store.ContactRawVCard or store.ContactRawGraph.
	Raw     []byte
	RawKind string
	// Card is the model of the contact (vcard.Clean'ed by StoreCard).
	Card *vcard.Card
}

// Sink receives the changes of one Sync, in order. An error ends the
// pass.
type Sink interface {
	// Upsert stores or replaces one contact by its remote id.
	Upsert(c Card) error
	// Remove deletes the stored contact with that remote id; an unknown
	// id is not an error.
	Remove(remoteID string) error
	// Seen marks a contact the server still has unchanged (a Full pass
	// keeps it).
	Seen(remoteID string)
}

// SyncState is what a provider needs to continue incrementally; the
// engine stores it with the book and passes it back in StoredBook.
type SyncState struct {
	Token     string // CardDAV sync-token
	CTag      string // CardDAV getctag
	DeltaLink string // Graph delta link
	// Full: the pass named every contact of the book (Upsert or Seen), so
	// the engine removes every stored one it did not name.
	Full bool
}

// StoredBook is the stored book row (sync state, remote id, name).
type StoredBook = store.AddressBook

// StoredContact is the stored contact row.
type StoredContact = store.StoredContact

// Provider is one account's address book source. Every method may block
// on the network and must honour ctx; errors are *api.Error, or the
// errors below.
type Provider interface {
	// AddressBooks lists the account's books as the server has them.
	AddressBooks(ctx context.Context) ([]RemoteBook, error)
	// Sync brings one book up to date given its stored state and the
	// ETags of its stored contacts (remote id → etag); it calls sink for
	// every change and returns the new state.
	Sync(ctx context.Context, book StoredBook, known map[string]string, sink Sink) (SyncState, error)
	// Get fetches one contact (ErrGone when the server no longer has it).
	Get(ctx context.Context, book StoredBook, remoteID string) (Card, error)
	// Create stores a new contact in the book.
	Create(ctx context.Context, book StoredBook, card *vcard.Card) (Card, error)
	// Update changes the stored contact from before (what the user saw,
	// the stored version) to after; a server version other than the
	// stored one is a *ConflictError.
	Update(ctx context.Context, book StoredBook, stored StoredContact, before, after *vcard.Card) (Card, error)
	// Delete removes the stored contact (its version only).
	Delete(ctx context.Context, book StoredBook, stored StoredContact) error
}

// Versioned is an optional interface of a Provider: the version of how it
// maps the server's data. A book stored with another version is listed
// again whole (its stored state is dropped first).
type Versioned interface {
	DataVersion() int
}

// ErrGone: the contact (or book) is no longer on the server.
var ErrGone = errors.New("contactsync: the contact is no longer on the server")

// ConflictError: the contact changed on the server; Server is its current
// version (Server.Card nil when it could not be read).
type ConflictError struct {
	Server Card
}

func (e *ConflictError) Error() string { return "contactsync: the contact changed on the server" }
