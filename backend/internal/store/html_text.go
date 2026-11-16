// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
)

// TextCandidate is a stored message whose plain-text body starts with
// "<": the pass that reads HTML markup in text parts as HTML
// (mime.LooksLikeHTML) decides whether it is markup.
type TextCandidate struct {
	ID        string
	AccountID string
	Text      string
}

// ListAngleTexts returns up to limit messages after afterID, in id order,
// whose text_body starts with "<" after whitespace or a byte order mark.
func (s *Store) ListAngleTexts(ctx context.Context, afterID string, limit int) ([]TextCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, text_body FROM messages
		WHERE id > ? AND substr(ltrim(text_body, ' '||char(9)||char(10)||char(13)||char(65279)), 1, 1) = '<'
		ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list texts with markup: %w", err)
	}
	defer rows.Close()
	var out []TextCandidate
	for rows.Next() {
		var c TextCandidate
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Text); err != nil {
			return nil, fmt.Errorf("list texts with markup: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list texts with markup: %w", err)
	}
	return out, nil
}

// TextFix replaces a message's plain-text body and snippet; Old is the
// body it was computed from.
type TextFix struct {
	ID      string
	Old     string
	Text    string
	Snippet string
}

// SetTexts writes fixes in one transaction, each only while the row still
// holds its Old body (a body stored meanwhile was parsed afresh), and
// returns the ids it changed.
func (s *Store) SetTexts(ctx context.Context, fixes []TextFix) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("set message texts: %w", err)
	}
	defer tx.Rollback()
	var changed []string
	now := nowStamp()
	for _, f := range fixes {
		res, err := tx.ExecContext(ctx, `UPDATE messages SET text_body = ?, snippet = ?, updated_at = ? WHERE id = ? AND text_body = ?`,
			f.Text, f.Snippet, now, f.ID, f.Old)
		if err != nil {
			return nil, fmt.Errorf("set message texts: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = append(changed, f.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("set message texts: %w", err)
	}
	return changed, nil
}
