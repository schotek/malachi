// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The address books of connected services and their contacts (migration
// 0023, docs/api.md §4.19): a cache of the server. Only ab_books.visible
// is the user's. ab_books and ab_contacts are written with INSERT ... ON
// CONFLICT DO UPDATE, UPDATE and DELETE only, never INSERT OR REPLACE
// (the search index's delete trigger and the cascades must run).

// AddressBook is a stored address book (ab_books). Count is filled by
// the listings only.
type AddressBook struct {
	ID         string
	AccountID  string
	RemoteID   string
	Name       string
	ReadOnly   bool
	Visible    bool
	SyncToken  string
	CTag       string
	DeltaLink  string
	Version    int
	LastSyncAt time.Time
	Position   int
	Count      int
}

// ContactEmailRow is one address of a stored contact (ab_emails),
// normalised.
type ContactEmailRow struct {
	Address string
	Label   string
}

// StoredContact is a stored contact (ab_contacts) with its addresses.
// The derived columns are the caller's (cleaned); Raw is the vCard or the
// Graph JSON as received.
type StoredContact struct {
	ID           string
	BookID       string
	RemoteID     string
	ETag         string
	Raw          []byte
	RawKind      string // "vcard" or "graph"
	DisplayName  string
	SortKey      string
	Section      string
	Org          string
	PrimaryEmail string
	Emails       []ContactEmailRow
	UpdatedAt    time.Time
}

// Raw kinds of ab_contacts.
const (
	ContactRawVCard = "vcard"
	ContactRawGraph = "graph"
)

const bookColumns = `b.id, b.account_id, b.remote_id, b.name, b.read_only, b.visible, b.sync_token, b.ctag,
	b.delta_link, b.version, b.last_sync_at, b.position,
	(SELECT COUNT(*) FROM ab_contacts c WHERE c.book_id = b.id)`

func scanBook(r interface{ Scan(...any) error }) (AddressBook, error) {
	var b AddressBook
	var ro, vis int
	var last string
	if err := r.Scan(&b.ID, &b.AccountID, &b.RemoteID, &b.Name, &ro, &vis, &b.SyncToken, &b.CTag,
		&b.DeltaLink, &b.Version, &last, &b.Position, &b.Count); err != nil {
		return AddressBook{}, err
	}
	b.ReadOnly, b.Visible = ro != 0, vis != 0
	if last != "" {
		b.LastSyncAt = parseStamp(last)
	}
	return b, nil
}

// UpsertAddressBooks stores the books of an account as the server lists
// them, matched by remote id: name, read-only and position change, the
// user's visible and the engine's sync state stay. It returns the stored
// rows in the order given. Books of the account not in the list are left
// alone (DeleteAddressBook removes them).
func (s *Store) UpsertAddressBooks(ctx context.Context, accountID string, books []AddressBook) ([]AddressBook, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("upsert address books: %w", err)
	}
	defer tx.Rollback()
	out := make([]AddressBook, 0, len(books))
	for _, b := range books {
		if b.RemoteID == "" {
			return nil, errors.New("upsert address books: empty remote id")
		}
		now := nowStamp()
		if _, err := tx.ExecContext(ctx, `INSERT INTO ab_books (id, account_id, remote_id, name, read_only, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, remote_id) DO UPDATE SET
				name = excluded.name, read_only = excluded.read_only, position = excluded.position,
				updated_at = excluded.updated_at`,
			newID("b_"), accountID, b.RemoteID, b.Name, boolInt(b.ReadOnly), b.Position, now, now); err != nil {
			return nil, fmt.Errorf("upsert address book: %w", err)
		}
		stored, err := scanBook(tx.QueryRowContext(ctx, `SELECT `+bookColumns+` FROM ab_books b
			WHERE b.account_id = ? AND b.remote_id = ?`, accountID, b.RemoteID))
		if err != nil {
			return nil, fmt.Errorf("read address book: %w", err)
		}
		out = append(out, stored)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("upsert address books: %w", err)
	}
	return out, nil
}

// ListAddressBooks returns the books of an account ("" = every account),
// by account and server order, each with its number of contacts.
func (s *Store) ListAddressBooks(ctx context.Context, accountID string) ([]AddressBook, error) {
	q := `SELECT ` + bookColumns + ` FROM ab_books b`
	var args []any
	if accountID != "" {
		q += ` WHERE b.account_id = ?`
		args = append(args, accountID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY b.account_id, b.position, b.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list address books: %w", err)
	}
	defer rows.Close()
	var out []AddressBook
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, fmt.Errorf("scan address book: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list address books: %w", err)
	}
	return out, nil
}

// GetAddressBook returns one book; ErrNotFound when there is none.
func (s *Store) GetAddressBook(ctx context.Context, id string) (AddressBook, error) {
	b, err := scanBook(s.db.QueryRowContext(ctx, `SELECT `+bookColumns+` FROM ab_books b WHERE b.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AddressBook{}, ErrNotFound
	}
	if err != nil {
		return AddressBook{}, fmt.Errorf("get address book: %w", err)
	}
	return b, nil
}

// SetAddressBookVisible records the user's choice; ErrNotFound for an
// unknown book.
func (s *Store) SetAddressBookVisible(ctx context.Context, id string, visible bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE ab_books SET visible = ?, updated_at = ? WHERE id = ?`, boolInt(visible), nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set address book visible: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddressBookSyncState is the engine's part of a book.
type AddressBookSyncState struct {
	SyncToken  string
	CTag       string
	DeltaLink  string
	Version    int
	LastSyncAt time.Time
}

// SetAddressBookSyncState stores the engine's state of a book;
// ErrNotFound for an unknown book.
func (s *Store) SetAddressBookSyncState(ctx context.Context, id string, st AddressBookSyncState) error {
	last := ""
	if !st.LastSyncAt.IsZero() {
		last = stamp(st.LastSyncAt)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ab_books SET sync_token = ?, ctag = ?, delta_link = ?, version = ?,
		last_sync_at = ?, updated_at = ? WHERE id = ?`, st.SyncToken, st.CTag, st.DeltaLink, st.Version, last, nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set address book sync state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAddressBook removes a book with its contacts.
func (s *Store) DeleteAddressBook(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ab_books WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete address book: %w", err)
	}
	return nil
}

// DeleteAddressBookContents removes a book's contacts and resets its sync
// state; the row (and the user's visible) stays.
func (s *Store) DeleteAddressBookContents(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete address book contents: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM ab_contacts WHERE book_id = ?`, id); err != nil {
		return fmt.Errorf("delete address book contents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ab_books SET sync_token = '', ctag = '', delta_link = '', version = 0,
		last_sync_at = '', updated_at = ? WHERE id = ?`, nowStamp(), id); err != nil {
		return fmt.Errorf("reset address book: %w", err)
	}
	return tx.Commit()
}

// DeleteAccountAddressBooks removes every book of an account with its
// contacts and returns their ids.
func (s *Store) DeleteAccountAddressBooks(ctx context.Context, accountID string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("delete account address books: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM ab_books WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("delete account address books: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM ab_books WHERE account_id = ?`, accountID); err != nil {
		return nil, fmt.Errorf("delete account address books: %w", err)
	}
	return ids, tx.Commit()
}

// UpsertContact stores a contact of a book by its remote id (insert or
// update in place; the id stays) with its addresses, and returns its id.
func (s *Store) UpsertContact(ctx context.Context, c StoredContact) (string, error) {
	if c.BookID == "" || c.RemoteID == "" {
		return "", errors.New("upsert contact: book and remote id are required")
	}
	if c.RawKind != ContactRawVCard && c.RawKind != ContactRawGraph {
		return "", fmt.Errorf("upsert contact: raw kind %q", c.RawKind)
	}
	if c.Raw == nil {
		c.Raw = []byte{}
	}
	section := c.Section
	if section == "" {
		section = "#"
	}
	emails := make([]string, 0, len(c.Emails))
	for _, e := range c.Emails {
		emails = append(emails, e.Address)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("upsert contact: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ab_contacts (id, book_id, remote_id, etag, raw, raw_kind, display_name,
			sort_key, section, org, primary_email, emails, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (book_id, remote_id) DO UPDATE SET
			etag = excluded.etag, raw = excluded.raw, raw_kind = excluded.raw_kind,
			display_name = excluded.display_name, sort_key = excluded.sort_key, section = excluded.section,
			org = excluded.org, primary_email = excluded.primary_email, emails = excluded.emails,
			updated_at = excluded.updated_at`,
		newID("p_"), c.BookID, c.RemoteID, c.ETag, c.Raw, c.RawKind, c.DisplayName, c.SortKey, section, c.Org,
		c.PrimaryEmail, strings.Join(emails, " "), nowStamp()); err != nil {
		return "", fmt.Errorf("upsert contact: %w", err)
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ab_contacts WHERE book_id = ? AND remote_id = ?`, c.BookID, c.RemoteID).Scan(&id); err != nil {
		return "", fmt.Errorf("upsert contact: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ab_emails WHERE contact_id = ?`, id); err != nil {
		return "", fmt.Errorf("upsert contact emails: %w", err)
	}
	for i, e := range c.Emails {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ab_emails (contact_id, position, address, label) VALUES (?, ?, ?, ?)`,
			id, i, e.Address, e.Label); err != nil {
			return "", fmt.Errorf("upsert contact email: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("upsert contact: %w", err)
	}
	return id, nil
}

// RemoveContactByRemote deletes the contact of a book with that remote
// id; false when there was none.
func (s *Store) RemoveContactByRemote(ctx context.Context, bookID, remoteID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ab_contacts WHERE book_id = ? AND remote_id = ?`, bookID, remoteID)
	if err != nil {
		return false, fmt.Errorf("remove contact: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteContact deletes one contact by id (a missing one is not an error).
func (s *Store) DeleteContact(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ab_contacts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}

// ContactETags returns remote id → ETag (or change key) of a book's
// contacts.
func (s *Store) ContactETags(ctx context.Context, bookID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT remote_id, etag FROM ab_contacts WHERE book_id = ?`, bookID)
	if err != nil {
		return nil, fmt.Errorf("contact etags: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var r, e string
		if err := rows.Scan(&r, &e); err != nil {
			return nil, err
		}
		out[r] = e
	}
	return out, rows.Err()
}

const contactColumns = `c.id, c.book_id, c.remote_id, c.etag, c.raw, c.raw_kind, c.display_name, c.sort_key, c.section,
	c.org, c.primary_email, c.updated_at`

func scanContact(r interface{ Scan(...any) error }) (StoredContact, error) {
	var c StoredContact
	var updated string
	if err := r.Scan(&c.ID, &c.BookID, &c.RemoteID, &c.ETag, &c.Raw, &c.RawKind, &c.DisplayName, &c.SortKey, &c.Section,
		&c.Org, &c.PrimaryEmail, &updated); err != nil {
		return StoredContact{}, err
	}
	c.UpdatedAt = parseStamp(updated)
	return c, nil
}

// GetContact returns one contact with its addresses; ErrNotFound when
// there is none.
func (s *Store) GetContact(ctx context.Context, id string) (StoredContact, error) {
	c, err := scanContact(s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM ab_contacts c WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredContact{}, ErrNotFound
	}
	if err != nil {
		return StoredContact{}, fmt.Errorf("get contact: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT address, label FROM ab_emails WHERE contact_id = ? ORDER BY position`, id)
	if err != nil {
		return StoredContact{}, fmt.Errorf("get contact emails: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e ContactEmailRow
		if err := rows.Scan(&e.Address, &e.Label); err != nil {
			return StoredContact{}, err
		}
		c.Emails = append(c.Emails, e)
	}
	return c, rows.Err()
}

// ContactQuery is a listing of contacts: of BookIDs (none = nothing),
// matching Query (words of the name, organisation and addresses, each a
// prefix; "" = every contact), from Offset, at most Limit.
type ContactQuery struct {
	BookIDs []string
	Query   string
	Offset  int
	Limit   int
}

// ListContacts returns a page of contacts by sort key (then display name
// and id) and the number of every match. Raw is left out.
func (s *Store) ListContacts(ctx context.Context, q ContactQuery) ([]StoredContact, int, error) {
	if len(q.BookIDs) == 0 {
		return nil, 0, nil
	}
	where := ` WHERE c.book_id IN (` + inPlaceholders(len(q.BookIDs)) + `)`
	args := toAny(q.BookIDs)
	if strings.TrimSpace(q.Query) != "" {
		match := EventSearchMatch(q.Query)
		if match == "" {
			return nil, 0, nil
		}
		where += ` AND c.id IN (SELECT d.contact_id FROM ab_contacts_fts f JOIN ab_contacts_fts_docs d ON d.docid = f.rowid
			WHERE ab_contacts_fts MATCH ?)`
		args = append(args, match)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ab_contacts c`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count contacts: %w", err)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.book_id, c.remote_id, c.etag, x'', c.raw_kind, c.display_name,
			c.sort_key, c.section, c.org, c.primary_email, c.updated_at
		FROM ab_contacts c`+where+` ORDER BY c.sort_key, c.display_name, c.id LIMIT ? OFFSET ?`,
		append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()
	var out []StoredContact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan contact: %w", err)
		}
		c.Raw = nil
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// LookupContactAddresses maps each of addresses (normalised) that a
// contact of bookIDs has to that contact's id (the first by sort key
// when several have it).
func (s *Store) LookupContactAddresses(ctx context.Context, addresses, bookIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(addresses) == 0 || len(bookIDs) == 0 {
		return out, nil
	}
	args := append(toAny(addresses), toAny(bookIDs)...)
	rows, err := s.db.QueryContext(ctx, `SELECT e.address, c.id FROM ab_emails e JOIN ab_contacts c ON c.id = e.contact_id
		WHERE e.address IN (`+inPlaceholders(len(addresses))+`) AND c.book_id IN (`+inPlaceholders(len(bookIDs))+`)
		ORDER BY c.sort_key DESC, c.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, id string
		if err := rows.Scan(&a, &id); err != nil {
			return nil, err
		}
		out[a] = id // the last row, the first by sort key, wins
	}
	return out, rows.Err()
}

// ContactAddressMatch is one address of a contact matching a completion
// query.
type ContactAddressMatch struct {
	ContactID   string
	BookID      string
	DisplayName string
	Address     string
}

// SearchContactAddresses returns up to limit addresses of contacts of
// bookIDs whose address or name contains q (case-insensitive) or whose
// name, organisation or address words start with it; by sort key.
func (s *Store) SearchContactAddresses(ctx context.Context, q string, bookIDs []string, limit int) ([]ContactAddressMatch, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" || len(bookIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultCollectedLimit
	}
	pattern := "%" + escapeLike(q) + "%"
	args := append(toAny(bookIDs), pattern, pattern)
	cond := `e.address LIKE ? ESCAPE '\' OR lower(c.display_name) LIKE ? ESCAPE '\'`
	if match := EventSearchMatch(q); match != "" {
		cond += ` OR c.id IN (SELECT d.contact_id FROM ab_contacts_fts f JOIN ab_contacts_fts_docs d ON d.docid = f.rowid
			WHERE ab_contacts_fts MATCH ?)`
		args = append(args, match)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.book_id, c.display_name, e.address
		FROM ab_emails e JOIN ab_contacts c ON c.id = e.contact_id
		WHERE c.book_id IN (`+inPlaceholders(len(bookIDs))+`) AND (`+cond+`)
		ORDER BY c.sort_key, c.id, e.position LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search contact addresses: %w", err)
	}
	defer rows.Close()
	var out []ContactAddressMatch
	for rows.Next() {
		var m ContactAddressMatch
		if err := rows.Scan(&m.ContactID, &m.BookID, &m.DisplayName, &m.Address); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListCollectedAddresses returns a page of collected addresses (the
// recent book) by name, then address, optionally those whose address or
// name contains q, and the number of every match.
func (s *Store) ListCollectedAddresses(ctx context.Context, q string, offset, limit int) ([]CollectedAddress, int, error) {
	where := ``
	var args []any
	if q = strings.ToLower(strings.TrimSpace(q)); q != "" {
		pattern := "%" + escapeLike(q) + "%"
		where = ` WHERE address LIKE ? ESCAPE '\' OR name_lc LIKE ? ESCAPE '\'`
		args = append(args, pattern, pattern)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collected_addresses`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count collected addresses: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT address, name, first_used, last_used, uses FROM collected_addresses`+where+`
		ORDER BY CASE WHEN name_lc = '' THEN address ELSE name_lc END, address LIMIT ? OFFSET ?`,
		append(args, limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("list collected addresses: %w", err)
	}
	defer rows.Close()
	var out []CollectedAddress
	for rows.Next() {
		var c CollectedAddress
		var first, last string
		if err := rows.Scan(&c.Address, &c.Name, &first, &last, &c.Uses); err != nil {
			return nil, 0, err
		}
		c.FirstUsed, c.LastUsed = parseStamp(first), parseStamp(last)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// GetCollectedAddress returns one collected address; ErrNotFound when
// there is none.
func (s *Store) GetCollectedAddress(ctx context.Context, address string) (CollectedAddress, error) {
	var c CollectedAddress
	var first, last string
	err := s.db.QueryRowContext(ctx, `SELECT address, name, first_used, last_used, uses FROM collected_addresses WHERE address = ?`,
		address).Scan(&c.Address, &c.Name, &first, &last, &c.Uses)
	if errors.Is(err, sql.ErrNoRows) {
		return CollectedAddress{}, ErrNotFound
	}
	if err != nil {
		return CollectedAddress{}, fmt.Errorf("get collected address: %w", err)
	}
	c.FirstUsed, c.LastUsed = parseStamp(first), parseStamp(last)
	return c, nil
}

// CountCollectedAddresses is the size of the recent book.
func (s *Store) CountCollectedAddresses(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collected_addresses`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count collected addresses: %w", err)
	}
	return n, nil
}
