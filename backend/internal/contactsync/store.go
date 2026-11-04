// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactsync

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/vcard"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Limits of what is stored of a server's data.
const (
	maxRawBytes      = api.MaxVCardBytes
	maxRemoteIDBytes = 2048
	maxETagBytes     = 1024
	maxBookNameBytes = 256
)

// Row derives the stored row of a card of a book: the cleaned model's
// display name, sort key, section, organisation and normalised addresses
// (each once, at most api.MaxContactEmails). ok is false for a card that
// cannot be stored (no remote id, a raw over 1 MiB).
func Row(bookID string, c Card) (store.StoredContact, bool) {
	if c.RemoteID == "" || len(c.RemoteID) > maxRemoteIDBytes || len(c.Raw) > maxRawBytes || len(c.ETag) > maxETagBytes {
		return store.StoredContact{}, false
	}
	card := vcard.Clean(c.Card)
	key := vcard.SortKey(card)
	row := store.StoredContact{
		BookID: bookID, RemoteID: c.RemoteID, ETag: c.ETag, Raw: c.Raw, RawKind: c.RawKind,
		DisplayName: vcard.DisplayNameOf(card), SortKey: key, Section: vcard.Section(key), Org: card.Org,
	}
	seen := map[string]bool{}
	for _, e := range card.Emails {
		a := NormalizeAddress(e.Address)
		if a == "" || seen[a] || len(row.Emails) >= api.MaxContactEmails {
			continue
		}
		seen[a] = true
		row.Emails = append(row.Emails, store.ContactEmailRow{Address: a, Label: e.Label})
	}
	if len(row.Emails) > 0 {
		row.PrimaryEmail = row.Emails[0].Address
	}
	return row, true
}

// NormalizeAddress is an address as the store keeps it (lower case), ""
// when it is not a plain address.
func NormalizeAddress(s string) string {
	s = store.NormalizeAddress(s)
	if s == "" {
		return ""
	}
	if a, err := mail.ParseAddress(s); err != nil || !strings.EqualFold(a.Address, s) {
		return ""
	}
	return s
}

// StoreCard stores one card of a book (insert or update by remote id),
// with the photo embedded in a vCard, and returns the contact id. A card
// that cannot be stored is serverError.
func StoreCard(ctx context.Context, st *store.Store, bookID string, c Card) (string, error) {
	row, ok := Row(bookID, c)
	if !ok {
		return "", api.NewError(api.CodeServerError, "contactsync: the server's contact cannot be stored")
	}
	id, err := st.UpsertContact(ctx, row)
	if err != nil {
		return "", err
	}
	if _, err := storePhoto(ctx, st, nil, id, c); err != nil {
		return "", err
	}
	return id, nil
}

// storageError makes an *api.Error of a failure of the store; an
// *api.Error and a cancelled context pass through.
func storageError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return api.NewError(api.CodeStorageError, "contactsync: %s", cleanMessage(err.Error()))
}

func cleanMessage(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.ToValidUTF8(s, "")
}
