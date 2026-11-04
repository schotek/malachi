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

	"github.com/schotek/malachi/backend/internal/bulk"
)

// classifyBulkTx is the value of the bulk and list_id columns for the
// headers of message id: internal/bulk's verdict, "none" for every message
// of an issue-tracker account (a realm other than "" in accounts).
func classifyBulkTx(ctx context.Context, tx *sql.Tx, id string, headers map[string]string) (kind, listID string, err error) {
	issue, err := isIssueMessageTx(ctx, tx, id)
	switch {
	case err != nil:
		return "", "", err
	case issue:
		return bulk.StoredNone, "", nil
	}
	r := bulk.Classify(headers)
	return r.Stored(), r.ListID, nil
}

// isIssueMessageTx says whether the message belongs to an issue-tracker
// account (a realm other than "" in accounts). A row that is gone, or
// whose account is, is not.
func isIssueMessageTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var realm sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT a.realm FROM messages m LEFT JOIN accounts a ON a.id = m.account_id WHERE m.id = ?`, id).Scan(&realm)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("classify bulk mail: %w", err)
	}
	return realm.Valid && realm.String != "", nil
}

// BulkCandidate is a row the bulk classification pass still has to visit.
type BulkCandidate struct {
	ID        string
	AccountID string
	// Headers is the stored curated header map.
	Headers map[string]string
}

// ListUnclassified returns up to limit rows whose bulk column is still
// empty, in id order.
func (s *Store) ListUnclassified(ctx context.Context, limit int) ([]BulkCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, headers_json FROM messages WHERE bulk = '' ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unclassified messages: %w", err)
	}
	defer rows.Close()
	var out []BulkCandidate
	for rows.Next() {
		var c BulkCandidate
		var raw string
		if err := rows.Scan(&c.ID, &c.AccountID, &raw); err != nil {
			return nil, fmt.Errorf("list unclassified messages: %w", err)
		}
		c.Headers = decodeHeaders(raw)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list unclassified messages: %w", err)
	}
	return out, nil
}

// decodeHeaders reads a headers_json column; nil when it is not a map.
func decodeHeaders(raw string) map[string]string {
	var h map[string]string
	if json.Unmarshal([]byte(raw), &h) != nil {
		return nil
	}
	return h
}

// BulkVerdict is the classification the pass computed for a row.
type BulkVerdict struct {
	ID     string
	Bulk   string // bulk.Result.Stored()
	ListID string
}

// SetBulkBatch stores verdicts in one transaction, only for rows that are
// still unclassified (a body ingested meanwhile has classified its row
// from the whole message), and "none" for rows of issue-tracker accounts.
func (s *Store) SetBulkBatch(ctx context.Context, verdicts []BulkVerdict) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set bulk classification: %w", err)
	}
	defer tx.Rollback()
	for _, v := range verdicts {
		kind, listID := v.Bulk, v.ListID
		issue, err := isIssueMessageTx(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		if issue {
			kind, listID = bulk.StoredNone, ""
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET bulk = ?, list_id = ? WHERE id = ? AND bulk = ''`, kind, listID, v.ID); err != nil {
			return fmt.Errorf("set bulk classification: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set bulk classification: %w", err)
	}
	return nil
}

// ResetBulk marks every row unclassified again (the rule version
// changed).
func (s *Store) ResetBulk(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET bulk = '', list_id = '' WHERE bulk != ''`); err != nil {
		return fmt.Errorf("reset bulk classification: %w", err)
	}
	return nil
}

// Unsubscription is a remembered unsubscription.
type Unsubscription struct {
	Method string // "oneClick" or "mailto"
	At     time.Time
}

// GetUnsubscription returns what the account remembers about key (see
// bulk.RememberKey); ok is false when nothing.
func (s *Store) GetUnsubscription(ctx context.Context, accountID, key string) (u Unsubscription, ok bool, err error) {
	var at string
	err = s.db.QueryRowContext(ctx, `SELECT method, unsubscribed_at FROM unsubscriptions WHERE account_id = ? AND key = ?`, accountID, key).Scan(&u.Method, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return u, false, nil
	case err != nil:
		return u, false, fmt.Errorf("get unsubscription: %w", err)
	}
	u.At = parseStamp(at)
	return u, true, nil
}

// RememberUnsubscription records (or renews) an unsubscription.
func (s *Store) RememberUnsubscription(ctx context.Context, accountID, key, method string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO unsubscriptions (account_id, key, method, unsubscribed_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (account_id, key) DO UPDATE SET method = excluded.method, unsubscribed_at = excluded.unsubscribed_at`,
		accountID, key, method, stamp(at))
	if err != nil {
		return fmt.Errorf("remember unsubscription: %w", err)
	}
	return nil
}
