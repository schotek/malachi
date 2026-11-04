// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/contactsctl"
)

// The Contacts window's photos (docs/plans/address-book.md §9, F9): the
// list's rows near the viewport and the detail show a contact's photo by
// id instead of the initials, through the application's photo hub
// (contact_photos.go). The accessible name stays the row's and the
// detail's title.

// The hub's names of the window's two views.
const (
	contactsListPhotos   = "contacts-list"
	contactsDetailPhotos = "contacts-detail"
)

// watchPhotos registers the list and the detail with the hub.
func (cw *ContactsWindow) watchPhotos() {
	hub := cw.main.photoHub()
	hub.addPhotoView(contactsListPhotos, cw.listPhotos)
	hub.addPhotoView(contactsDetailPhotos, cw.detailPhotos)
	cw.listScroller.VAdjustment().ConnectValueChanged(cw.main.photosChanged)
	cw.listScroller.VAdjustment().ConnectChanged(cw.main.photosChanged)
	cw.detailBox.ConnectMap(cw.main.photosChanged)
	cw.detailBox.ConnectUnmap(cw.main.photosChanged)
}

// contactRef is the photo state of a contact the window shows.
func contactRef(id api.ContactID, book api.AddressBookID, photo api.ContactPhotoAvailability) contactsctl.ContactRef {
	return contactsctl.ContactRef{ID: id, Book: book, State: contacts.PhotoState(photo)}
}

// listPhotos is the list's provider: the contacts of the rows near the
// viewport.
func (cw *ContactsWindow) listPhotos() ([]photoAvatar, []contactsctl.ContactRef) {
	if cw.closed || len(cw.listRows) != len(cw.listAvatars) {
		return nil, nil
	}
	near := nearListRows(cw.contactList, cw.listScroller, len(cw.listAvatars))
	avatars := make([]photoAvatar, 0, len(near))
	refs := make([]contactsctl.ContactRef, 0, len(near))
	for _, i := range near {
		av, r := cw.listAvatars[i], cw.listRows[i]
		if av == nil || r.IsHeading() {
			continue
		}
		avatars = append(avatars, photoAvatar{av: av, a: contacts.AvatarOfRow(r, contacts.PhotoState(r.Contact.Photo))})
		refs = append(refs, contactRef(r.Contact.ID, r.Contact.BookID, r.Contact.Photo))
	}
	return avatars, refs
}

// detailAvatar is the detail's avatar of card (title d.Title), the
// contact's photo once the hub has it.
func (cw *ContactsWindow) detailAvatar(card api.ContactCard, d contacts.Detail) *adw.Avatar {
	cw.detailAv = adw.NewAvatar(64, d.Title, true)
	cw.detailPhoto = contacts.AvatarOfDetail(card, d, contacts.PhotoState(card.Photo))
	cw.detailRef = contactRef(card.ID, card.BookID, card.Photo)
	cw.main.photosChanged()
	return cw.detailAv
}

// detailPhotos is the detail's provider: its contact while it shows.
func (cw *ContactsWindow) detailPhotos() ([]photoAvatar, []contactsctl.ContactRef) {
	if cw.closed || cw.detailAv == nil || !cw.detailBox.Mapped() || cw.detailStack.VisibleChildName() != "detail" {
		return nil, nil
	}
	return []photoAvatar{{av: cw.detailAv, a: cw.detailPhoto}}, []contactsctl.ContactRef{cw.detailRef}
}
