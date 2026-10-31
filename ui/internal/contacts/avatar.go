// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Contact photos, read only (docs/plans/address-book.md §9, F1–F11). An
// avatar shows a contact's photo instead of its initials when there is
// one: in the Contacts window's list and detail (F9) and wherever mail
// draws a sender's avatar (F10: the message list with show-avatars, the
// conversation's cards, the chat's bubbles of Mail and the Board, a
// message's header). The bytes come from the daemon only (re-encoded by
// it, F5) through contactsctl.Photos; the views decode them and clip the
// circle. Without a photo the avatar is what it was.

// PhotoState is a contact's photo as contact.list, contact.get and
// contact.lookup {withPhotos} report it (ContactSummary.photo,
// ContactCard.photo): PhotoReady (stored by the daemon), PhotoRemote (a
// Microsoft 365 photo not fetched yet; contact.photo fetches it) or
// PhotoNone (absent). Any other value is PhotoNone.
type PhotoState string

const (
	PhotoNone   PhotoState = ""
	PhotoReady  PhotoState = "ready"
	PhotoRemote PhotoState = "remote"
)

// MayHave reports a state whose contact has, or may have, a photo.
func (s PhotoState) MayHave() bool { return s == PhotoReady || s == PhotoRemote }

// Avatar is what an avatar shows: Initials, and the photo of Contact (by
// id, the Contacts window) or of Sender's contact (by address, mail) when
// contactsctl.Photos has it. Both empty: initials only, never a photo.
type Avatar struct {
	Initials string
	// Contact is the contact whose photo replaces the initials.
	Contact api.ContactID
	// Sender is the From address (NormalAddress) whose contact's photo
	// replaces the initials; matched only by the exact address, never
	// by name (F10).
	Sender string
}

// MayShowPhoto reports an avatar that asks contactsctl.Photos.
func (a Avatar) MayShowPhoto() bool { return a.Contact != "" || a.Sender != "" }

// NormalAddress is an address as contact.lookup's keys are: trimmed, lower
// case.
func NormalAddress(a string) string { return strings.ToLower(strings.TrimSpace(a)) }

// IsRecent reports a contact of the recent book (its book, or the "r_" id
// contact.get takes): recent addresses never have a photo.
func IsRecent(book api.AddressBookID, id api.ContactID) bool {
	return book == api.AddressBookRecent || strings.HasPrefix(string(id), "r_")
}

// AvatarOfRow is the avatar of a row of the list (Rows) whose summary
// reports photo.
func AvatarOfRow(r Row, photo PhotoState) Avatar {
	a := Avatar{Initials: r.Initials}
	if photo.MayHave() && r.Contact.ID != "" && !IsRecent(r.Contact.BookID, r.Contact.ID) {
		a.Contact = r.Contact.ID
	}
	return a
}

// AvatarOfDetail is the avatar of the detail d of card, whose card
// reports photo.
func AvatarOfDetail(card api.ContactCard, d Detail, photo PhotoState) Avatar {
	a := Avatar{Initials: d.Initials}
	if photo.MayHave() && card.ID != "" && !IsRecent(card.BookID, card.ID) {
		a.Contact = card.ID
	}
	return a
}

// AvatarOfSender is the avatar of a message's sender: initials of name
// (else of address) and the photo of the contact with exactly address.
// The user's own messages (mine: the row of a sent or outbox folder, a
// bubble on the right) never get a photo; the "You" avatar stays (F10).
func AvatarOfSender(address, name string, mine bool) Avatar {
	a := Avatar{Initials: Initials(name, address)}
	if n := NormalAddress(address); !mine && strings.Contains(n, "@") {
		a.Sender = n
	}
	return a
}

// Monochrome reports whether the monochrome-avatars setting greys this
// avatar: it applies to initials only, never to a photo shown.
func Monochrome(setting, photoShown bool) bool { return setting && !photoShown }

// NearRows is the range [from, to) of rows whose photos are asked for:
// the visible rows first..last (inclusive) and as many again above and
// below (one screen each way, as the chat asks for bodies near the
// visible part), clamped to count rows. Nothing visible (last < first)
// is an empty range.
func NearRows(first, last, count int) (from, to int) {
	if count <= 0 || last < first {
		return 0, 0
	}
	first, last = max(first, 0), min(last, count-1)
	if last < first {
		return 0, 0
	}
	span := last - first + 1
	return max(first-span, 0), min(last+span+1, count)
}
