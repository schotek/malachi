// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The photos of contacts (migration 0024, docs/plans/address-book.md §9):
// one row per contact, made from one revision of it (its etag). Data is
// the picture as internal/contactphoto re-encoded it, never the server's
// bytes. Rows go with their contact (ON DELETE CASCADE).

// Stored states of a photo row (ab_photos.state).
const (
	PhotoOK      = "ok"
	PhotoNone    = "none"
	PhotoRefused = "refused"
)

// What a contact's photo is as the API names it (StoredContact.PhotoState).
const (
	// ContactPhotoReady: a photo of the contact's current revision is
	// stored.
	ContactPhotoReady = "ready"
	// ContactPhotoRemote: a Graph contact whose photo, if any, has not
	// been fetched for its current revision.
	ContactPhotoRemote = "remote"
)

// MaxPhotoBytes is the largest picture a row holds (the CHECK of 0024).
const MaxPhotoBytes = 64 << 10

// photoStateColumn computes StoredContact.PhotoState of c and its photo
// row p (LEFT JOIN): ready with an ok row of the current revision; remote
// for a Graph contact without a row of the current revision; else "".
const photoStateColumn = `CASE
		WHEN p.state = 'ok' AND p.revision = c.etag THEN 'ready'
		WHEN c.raw_kind = 'graph' AND (p.contact_id IS NULL OR p.revision <> c.etag) THEN 'remote'
		ELSE '' END`

// ContactPhoto is a stored photo row.
type ContactPhoto struct {
	ContactID string
	Revision  string
	State     string // PhotoOK, PhotoNone, PhotoRefused
	MIME      string
	Data      []byte
}

// SetContactPhoto stores (or replaces) the photo row of a contact. MIME
// and Data are kept only for PhotoOK. A contact that is gone is
// ErrNotFound.
func (s *Store) SetContactPhoto(ctx context.Context, p ContactPhoto) error {
	switch p.State {
	case PhotoOK:
		if len(p.Data) == 0 || len(p.Data) > MaxPhotoBytes || p.MIME == "" {
			return fmt.Errorf("set contact photo: %d bytes of %q", len(p.Data), p.MIME)
		}
	case PhotoNone, PhotoRefused:
		p.MIME, p.Data = "", []byte{}
	default:
		return fmt.Errorf("set contact photo: state %q", p.State)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO ab_photos (contact_id, revision, state, mime, data, updated_at)
		SELECT c.id, ?, ?, ?, ?, ? FROM ab_contacts c WHERE c.id = ?
		ON CONFLICT (contact_id) DO UPDATE SET revision = excluded.revision, state = excluded.state,
			mime = excluded.mime, data = excluded.data, updated_at = excluded.updated_at`,
		p.Revision, p.State, p.MIME, p.Data, nowStamp(), p.ContactID)
	if err != nil {
		return fmt.Errorf("set contact photo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetContactPhoto returns the photo row of a contact; ErrNotFound when it
// has none.
func (s *Store) GetContactPhoto(ctx context.Context, contactID string) (ContactPhoto, error) {
	p := ContactPhoto{ContactID: contactID}
	err := s.db.QueryRowContext(ctx, `SELECT revision, state, mime, data FROM ab_photos WHERE contact_id = ?`, contactID).
		Scan(&p.Revision, &p.State, &p.MIME, &p.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return ContactPhoto{}, ErrNotFound
	}
	if err != nil {
		return ContactPhoto{}, fmt.Errorf("get contact photo: %w", err)
	}
	return p, nil
}

// ContactPhotoRevision is the revision of a contact's photo row, false
// when it has none (no data read).
func (s *Store) ContactPhotoRevision(ctx context.Context, contactID string) (string, bool, error) {
	var rev string
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM ab_photos WHERE contact_id = ?`, contactID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("contact photo revision: %w", err)
	}
	return rev, true, nil
}

// VCardContactsWithoutPhoto returns up to limit contacts of a book kept
// as vCards (with Raw) whose photo row is missing or of another revision:
// the ones whose embedded photo is still to be read.
func (s *Store) VCardContactsWithoutPhoto(ctx context.Context, bookID string, limit int) ([]StoredContact, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+contactFrom+`
		WHERE c.book_id = ? AND c.raw_kind = 'vcard' AND (p.contact_id IS NULL OR p.revision <> c.etag)
		ORDER BY c.id LIMIT ?`, bookID, limit)
	if err != nil {
		return nil, fmt.Errorf("contacts without photo: %w", err)
	}
	defer rows.Close()
	var out []StoredContact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan contact: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactPhotoMatch is the contact whose photo stands for an address.
type ContactPhotoMatch struct {
	ContactID string
	State     string // ContactPhotoReady or ContactPhotoRemote
}

// LookupContactPhotos maps each of addresses (normalised) to the contact
// of bookIDs with that address whose photo is ready or remote, the first
// by sort key when several are; addresses without such a contact are
// absent.
func (s *Store) LookupContactPhotos(ctx context.Context, addresses, bookIDs []string) (map[string]ContactPhotoMatch, error) {
	out := map[string]ContactPhotoMatch{}
	if len(addresses) == 0 || len(bookIDs) == 0 {
		return out, nil
	}
	args := append(toAny(addresses), toAny(bookIDs)...)
	rows, err := s.db.QueryContext(ctx, `SELECT address, id, photo FROM (
			SELECT e.address AS address, c.id AS id, c.sort_key AS sort_key, `+photoStateColumn+` AS photo
			FROM ab_emails e JOIN ab_contacts c ON c.id = e.contact_id LEFT JOIN ab_photos p ON p.contact_id = c.id
			WHERE e.address IN (`+inPlaceholders(len(addresses))+`) AND c.book_id IN (`+inPlaceholders(len(bookIDs))+`))
		WHERE photo <> '' ORDER BY sort_key DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup contact photos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var m ContactPhotoMatch
		if err := rows.Scan(&a, &m.ContactID, &m.State); err != nil {
			return nil, err
		}
		out[a] = m // the last row, the first by sort key, wins
	}
	return out, rows.Err()
}
