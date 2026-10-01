// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Account is a row of the accounts table. Config is the non-secret wire
// form; secrets never enter the store.
type Account struct {
	ID    string
	Email string // normalised (NormalizeAddress); Config.Email keeps the user's form
	// Realm is where Email is unique: "" for a mailbox, the site of an
	// issue-tracker account (RealmOf). Derived from Config on every write;
	// a value set by the caller is ignored.
	Realm     string
	Name      string
	Enabled   bool
	Config    api.AccountConfig
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

const accountColumns = `id, email, realm, name, enabled, config, position, created_at, updated_at`

// RealmOf is the realm an account's address is unique in (migration
// 0015): "" for a mailbox (imap, graph), and for an issue-tracker account
// its site, the lower-cased host[:port] and path of JiraConfig.SiteURL
// without a trailing slash ("acme.atlassian.net",
// "jira.example.org:8443/jira"); the scheme and a default port do not
// count. "" as well for a jira config without a usable site URL, which
// AddAccount and UpdateAccount refuse.
func RealmOf(cfg api.AccountConfig) string {
	if cfg.Protocol() != api.AccountJira || cfg.Jira == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(cfg.Jira.SiteURL))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return ""
	}
	host := u.Host
	if port := u.Port(); (port == "443" && u.Scheme == "https") || (port == "80" && u.Scheme == "http") {
		host = u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]" // an IPv6 literal keeps its brackets
		}
	}
	return strings.ToLower(host + strings.TrimRight(u.EscapedPath(), "/"))
}

// accountRealm fills a.Realm from a.Config; an issue-tracker account
// without a site is an error.
func accountRealm(a *Account) error {
	a.Realm = RealmOf(a.Config)
	if a.Realm == "" && a.Config.Protocol() == api.AccountJira {
		return fmt.Errorf("jira account without a site URL")
	}
	return nil
}

// CheckAccountID reports an account id that cannot name the account's
// directory of message files: one that could escape it (checkPathSegment),
// or one ending in a dot or a space, which Windows drops from a file name
// ("acc." would be the directory of "acc"). An id written in config.toml;
// AddAccount refuses it.
func CheckAccountID(id string) error {
	if err := checkPathSegment(id); err != nil {
		return fmt.Errorf("account id: %w", err)
	}
	if strings.HasSuffix(id, ".") || strings.HasSuffix(id, " ") {
		return fmt.Errorf("account id: %q ends in a dot or a space", id)
	}
	return nil
}

// sameAccountDir reports whether two different account ids would name the
// same directory of message files on a file system that ignores case
// (macOS and Windows by default) or Unicode normalisation (APFS): equal
// under simple case folding of their NFC forms.
func sameAccountDir(a, b string) bool {
	return a != b && strings.EqualFold(norm.NFC.String(a), norm.NFC.String(b))
}

// AddAccount inserts a. a.ID is honoured when set (config.toml import) and
// valid (CheckAccountID), otherwise generated; a.Email is normalised from
// a.Config.Email when empty; a.Realm is derived from a.Config (RealmOf);
// Position is appended at the end and the timestamps are filled in.
// ErrExists when an account with the same e-mail (case-insensitive) in the
// same realm or the same id already exists; ErrAccountIDTaken when a.ID
// differs from another account's id only in case or Unicode normalisation,
// which would share its directory of message files.
func (s *Store) AddAccount(ctx context.Context, a *Account) error {
	if a.ID == "" {
		a.ID = newID("acc_")
	} else if err := CheckAccountID(a.ID); err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	if a.Email == "" {
		a.Email = NormalizeAddress(a.Config.Email)
	}
	if a.Email == "" {
		return fmt.Errorf("add account: empty e-mail")
	}
	if err := accountRealm(a); err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		return fmt.Errorf("encode account config: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM accounts WHERE (email = ? AND realm = ?) OR id = ? LIMIT 1`, a.Email, a.Realm, a.ID).Scan(&existing)
	switch {
	case err == nil:
		return ErrExists
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("add account: %w", err)
	}
	if taken, err := accountDirTaken(ctx, tx, a.ID); err != nil {
		return fmt.Errorf("add account: %w", err)
	} else if taken {
		return ErrAccountIDTaken
	}

	stamp := nowStamp()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO accounts (id, email, realm, name, enabled, config, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM accounts), ?, ?)
		 RETURNING position`,
		a.ID, a.Email, a.Realm, a.Name, boolInt(a.Enabled), string(cfg), stamp, stamp).Scan(&a.Position)
	if err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = parseStamp(stamp), parseStamp(stamp)
	return nil
}

// accountDirTaken reports whether id would share the directory of message
// files of an account already stored (sameAccountDir). The accounts table
// is small; the comparison needs Go's folding, not SQLite's ASCII lower().
func accountDirTaken(ctx context.Context, q querier, id string) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM accounts`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return false, err
		}
		if sameAccountDir(id, other) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ListAccounts returns every account in display order: the order the user
// arranged with ReorderAccounts, creation order until then.
func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY position, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return out, nil
}

// GetAccount returns one account or ErrNotFound.
func (s *Store) GetAccount(ctx context.Context, id string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// UpdateAccount replaces name, e-mail and configuration of a.ID (and the
// realm derived from it); Enabled and Position are left alone. ErrNotFound
// for an unknown id, ErrExists when another account of the same realm
// already uses the e-mail (case-insensitive).
func (s *Store) UpdateAccount(ctx context.Context, a *Account) error {
	if a.Email == "" {
		a.Email = NormalizeAddress(a.Config.Email)
	}
	if a.Email == "" {
		return fmt.Errorf("update account: empty e-mail")
	}
	if err := accountRealm(a); err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		return fmt.Errorf("encode account config: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	defer tx.Rollback()

	var found string
	err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id = ?`, a.ID).Scan(&found)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("update account: %w", err)
	}
	var other string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM accounts WHERE email = ? AND realm = ? AND id != ? LIMIT 1`, a.Email, a.Realm, a.ID).Scan(&other)
	switch {
	case err == nil:
		return ErrExists
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("update account: %w", err)
	}
	stamp := nowStamp()
	res, err := tx.ExecContext(ctx,
		`UPDATE accounts SET email = ?, realm = ?, name = ?, config = ?, updated_at = ? WHERE id = ?`,
		a.Email, a.Realm, a.Name, string(cfg), stamp, a.ID)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	a.UpdatedAt = parseStamp(stamp)
	return nil
}

// ReorderAccounts arranges the display order (ListAccounts): the given
// accounts take the head in the given order, accounts the caller did not
// name keep their relative order behind them — so a client that has not
// seen a just-added account does not move it. Positions are rewritten to
// 0..n-1, which also closes the gaps DeleteAccount leaves behind.
//
// Only position changes; updated_at is not touched, because the accounts
// themselves did not change. A duplicate id or more ids than accounts is
// ErrBadOrder, an unknown id is ErrNotFound, and nothing is written then.
func (s *Store) ReorderAccounts(ctx context.Context, ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("%w: duplicate id %q", ErrBadOrder, id)
		}
		seen[id] = true
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reorder accounts: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM accounts ORDER BY position, created_at, id`)
	if err != nil {
		return fmt.Errorf("reorder accounts: %w", err)
	}
	var current []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("reorder accounts: %w", err)
		}
		current = append(current, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reorder accounts: %w", err)
	}
	if len(ids) > len(current) {
		return fmt.Errorf("%w: %d ids for %d accounts", ErrBadOrder, len(ids), len(current))
	}
	known := make(map[string]bool, len(current))
	for _, id := range current {
		known[id] = true
	}
	for _, id := range ids {
		if !known[id] {
			return ErrNotFound
		}
	}

	order := make([]string, 0, len(current))
	order = append(order, ids...)
	for _, id := range current {
		if !seen[id] {
			order = append(order, id)
		}
	}
	for i, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET position = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("reorder accounts: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reorder accounts: %w", err)
	}
	return nil
}

// SetAccountEnabled pauses or resumes an account. ErrNotFound for an unknown
// id.
func (s *Store) SetAccountEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set account enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAccount removes the account row together with its mail cache
// (folders, messages, pending operations — always, they are worthless
// without the account; raw message files after the commit, and what a
// reader keeps open on Windows with the next sweep) and, for an
// issue-tracker account, its issue tables, showing again (and recounting
// the folders of) the mail of other accounts it hid. With
// deleteLocalData it also deletes the account's drafts and attachments (rows
// in the same transaction, files afterwards). ErrNotFound for an unknown id.
func (s *Store) DeleteAccount(ctx context.Context, id string, deleteLocalData bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_ops WHERE account_id = ?`, id); err != nil {
		return fmt.Errorf("delete account operations: %w", err)
	}
	// Messages go with their folders (ON DELETE CASCADE).
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE account_id = ?`, id); err != nil {
		return fmt.Errorf("delete account folders: %w", err)
	}
	if err := unhideLinkedMailTx(ctx, tx, `l.issue_account_id = ?`, map[string][]string{}, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	for _, q := range []string{
		`DELETE FROM issue_mail_links WHERE issue_account_id = ?`,
		`DELETE FROM issue_items WHERE account_id = ?`,
		`DELETE FROM issues WHERE account_id = ?`,
		`DELETE FROM issue_spaces WHERE account_id = ?`,
		`DELETE FROM unsubscriptions WHERE account_id = ?`,
		`DELETE FROM meta WHERE key = '` + MetaIssueMePrefix + `' || ?`,
		`DELETE FROM meta WHERE key = '` + MetaIssueMailPrefix + `' || ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete account issues: %w", err)
		}
	}
	if checkPathSegment(id) == nil {
		// The raw files go after the commit, and what a reader keeps open
		// (Windows) or a crash leaves goes with the sweep: the record says
		// the directory is this store's to remove whole (removeMessageDir).
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, metaDeletedDir+id, nowStamp()); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
	}

	var files []string
	if deleteLocalData {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM attachments WHERE account_id = ?`, id)
		if err != nil {
			return fmt.Errorf("list account attachments: %w", err)
		}
		for rows.Next() {
			var aid string
			if err := rows.Scan(&aid); err != nil {
				rows.Close()
				return err
			}
			files = append(files, aid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list account attachments: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE account_id = ?`, id); err != nil {
			return fmt.Errorf("delete account attachments: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE account_id = ?`, id); err != nil {
			return fmt.Errorf("delete account drafts: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	s.removeAttachmentFiles(files...)
	s.removeMessageDir(ctx, id)
	return nil
}

// GetMeta reads a key of the meta table. The second result is false when the
// key is absent.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("get meta %q: %w", key, err)
	}
	return v, true, nil
}

// SetMeta writes a key of the meta table.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

func scanAccount(row scanner) (Account, error) {
	var a Account
	var enabled int
	var cfg, created, updated string
	if err := row.Scan(&a.ID, &a.Email, &a.Realm, &a.Name, &enabled, &cfg, &a.Position, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, err
		}
		return Account{}, fmt.Errorf("scan account: %w", err)
	}
	if err := json.Unmarshal([]byte(cfg), &a.Config); err != nil {
		return Account{}, fmt.Errorf("decode config of %s: %w", a.ID, err)
	}
	a.Enabled = enabled != 0
	a.CreatedAt, a.UpdatedAt = parseStamp(created), parseStamp(updated)
	return a, nil
}
