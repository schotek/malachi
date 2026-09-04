// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Account is a row of the accounts table. Config is the non-secret wire
// form; secrets never enter the store.
type Account struct {
	ID        string
	Email     string // normalised (NormalizeAddress); Config.Email keeps the user's form
	Name      string
	Enabled   bool
	Config    api.AccountConfig
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

const accountColumns = `id, email, name, enabled, config, position, created_at, updated_at`

// AddAccount inserts a. a.ID is honoured when set (config.toml import),
// otherwise generated; a.Email is normalised from a.Config.Email when empty;
// Position is appended at the end and the timestamps are filled in.
// ErrExists when an account with the same e-mail (case-insensitive) or the
// same id already exists.
func (s *Store) AddAccount(ctx context.Context, a *Account) error {
	if a.ID == "" {
		a.ID = newID("acc_")
	}
	if a.Email == "" {
		a.Email = NormalizeAddress(a.Config.Email)
	}
	if a.Email == "" {
		return fmt.Errorf("add account: empty e-mail")
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
		`SELECT id FROM accounts WHERE email = ? OR id = ? LIMIT 1`, a.Email, a.ID).Scan(&existing)
	switch {
	case err == nil:
		return ErrExists
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("add account: %w", err)
	}

	stamp := nowStamp()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO accounts (id, email, name, enabled, config, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM accounts), ?, ?)
		 RETURNING position`,
		a.ID, a.Email, a.Name, boolInt(a.Enabled), string(cfg), stamp, stamp).Scan(&a.Position)
	if err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("add account: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = parseStamp(stamp), parseStamp(stamp)
	return nil
}

// ListAccounts returns every account in display order (creation order until
// reordering exists).
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

// UpdateAccount replaces name, e-mail and configuration of a.ID; Enabled and
// Position are left alone. ErrNotFound for an unknown id, ErrExists when
// another account already uses the e-mail (case-insensitive).
func (s *Store) UpdateAccount(ctx context.Context, a *Account) error {
	if a.Email == "" {
		a.Email = NormalizeAddress(a.Config.Email)
	}
	if a.Email == "" {
		return fmt.Errorf("update account: empty e-mail")
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
		`SELECT id FROM accounts WHERE email = ? AND id != ? LIMIT 1`, a.Email, a.ID).Scan(&other)
	switch {
	case err == nil:
		return ErrExists
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("update account: %w", err)
	}
	stamp := nowStamp()
	res, err := tx.ExecContext(ctx,
		`UPDATE accounts SET email = ?, name = ?, config = ?, updated_at = ? WHERE id = ?`,
		a.Email, a.Name, string(cfg), stamp, a.ID)
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

// DeleteAccount removes the account row. With deleteLocalData it also
// deletes the account's drafts and attachments (rows in the same
// transaction, files afterwards). ErrNotFound for an unknown id.
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
	for _, aid := range files {
		s.removeAttachmentFile(aid)
	}
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
	if err := row.Scan(&a.ID, &a.Email, &a.Name, &enabled, &cfg, &a.Position, &created, &updated); err != nil {
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
