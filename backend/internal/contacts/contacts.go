// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package contacts is the address-book side of recipient completion: the
// types the core consumes and the Directory interface a system
// address-book client (internal/contacts/eds) implements. It depends on
// nothing of its own, so core and the clients can share it without a cycle.
package contacts

import "context"

// Book is one address book a directory offers for completion.
type Book struct {
	UID     string // the source's uid; a single D-Bus path segment
	Name    string // display name, untrusted text
	Backend string // "microsoft365", "carddav", "google", "local", …
	// Collection is the parent source's uid, "" for a stand-alone book.
	// GOAAccountID and Email are the collection's identity when it has one
	// (a GNOME Online Accounts account id, and its normalised address).
	Collection   string
	GOAAccountID string
	Email        string
}

// Contact is one card's address. A card with several addresses yields one
// Contact per address.
type Contact struct {
	Name    string // untrusted display text; "" when the card has none
	Address string // normalised, syntactically valid
	Book    string // display name of the book it came from, untrusted
}

// Directory is what core needs from a system address-book client. Books
// lists the address books that take part in completion; Search queries
// the given books. Both report api.CodeUnavailable when there is no
// session bus or no address-book service, which core treats as "no
// address-book results", never as a failed search.
type Directory interface {
	Books(ctx context.Context) ([]Book, error)
	Search(ctx context.Context, books []Book, query string, limit int) ([]Contact, error)
}
