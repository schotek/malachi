// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactsync

import (
	"context"
	"errors"
	"log/slog"

	"github.com/schotek/malachi/backend/internal/contactphoto"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
)

// The photos of vCard contacts (docs/plans/address-book.md §9, F2, F5,
// F7): the picture embedded in the card is read when the card is stored,
// normalised (internal/contactphoto) and kept in ab_photos under the
// card's revision. A Graph contact's photo is not in its data: core
// fetches it when a client asks (contact.photo). A photo named by a link
// is never fetched.

// photoBackfillPerPass caps the stored vCards whose photo one pass reads
// (cards stored before 0024, or whose row is stale).
const photoBackfillPerPass = 500

// PhotoRow is the photo row of a card as stored under contactID: the
// card's embedded picture normalised (PhotoOK), PhotoRefused when it
// cannot be, PhotoNone without one (or with a link only). class is
// contactphoto.Class of a refusal, for a log.
func PhotoRow(contactID, revision string, card *vcard.Card) (row store.ContactPhoto, class string) {
	row = store.ContactPhoto{ContactID: contactID, Revision: revision, State: store.PhotoNone}
	if card == nil || card.Photo == nil || len(card.Photo.Data) == 0 {
		return row, ""
	}
	data, mime, err := contactphoto.Normalize(card.Photo.Data, card.Photo.MIME)
	if err != nil {
		row.State = store.PhotoRefused
		return row, contactphoto.Class(err)
	}
	row.State, row.MIME, row.Data = store.PhotoOK, mime, data
	return row, ""
}

// storePhoto stores the photo row of a vCard contact just stored, unless
// a row of that revision is there already (a revision "" is always read
// again). ok reports a stored picture (the listing's state changed).
func storePhoto(ctx context.Context, st *store.Store, log *slog.Logger, contactID string, c Card) (ok bool, err error) {
	if c.RawKind != store.ContactRawVCard {
		return false, nil
	}
	if c.ETag != "" {
		rev, has, err := st.ContactPhotoRevision(ctx, contactID)
		if err != nil {
			return false, err
		}
		if has && rev == c.ETag {
			return false, nil
		}
	}
	row, class := PhotoRow(contactID, c.ETag, c.Card)
	if class != "" && log != nil {
		log.Info("contact photo refused", "contact", contactID, "class", class)
	}
	err = st.Paced(ctx, func() error { return st.SetContactPhoto(ctx, row) })
	if errors.Is(err, store.ErrNotFound) {
		return false, nil // the contact went meanwhile
	}
	return err == nil && row.State == store.PhotoOK, err
}

// backfillPhotos reads the photos of a book's stored vCards that have no
// row of their revision; changed reports a stored picture.
func backfillPhotos(ctx context.Context, st *store.Store, log *slog.Logger, bookID string) (changed bool, err error) {
	done := 0
	for done < photoBackfillPerPass {
		rows, err := st.VCardContactsWithoutPhoto(ctx, bookID, 50)
		if err != nil || len(rows) == 0 {
			return changed, err
		}
		for _, r := range rows {
			card, perr := vcard.Parse(r.Raw)
			if perr != nil {
				card = nil // stored as no photo; the card itself is the engine's problem
			}
			row, class := PhotoRow(r.ID, r.ETag, card)
			if class != "" && log != nil {
				log.Info("contact photo refused", "contact", r.ID, "class", class)
			}
			if err := st.Paced(ctx, func() error { return st.SetContactPhoto(ctx, row) }); err != nil && !errors.Is(err, store.ErrNotFound) {
				return changed, err
			}
			changed = changed || row.State == store.PhotoOK
			done++
		}
	}
	return changed, nil
}
