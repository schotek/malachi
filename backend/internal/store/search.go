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

	"github.com/schotek/malachi/backend/pkg/api"
)

// The full-text index (migration 0013_search.sql): messages_fts holds the
// tokens, search_docs maps its rowids to message ids, and the triggers on
// messages keep both current. SearchMessages reads it, IndexSearchBatch
// fills it for the rows stored before it existed.

// searchTextBytes is how much of a result's body SearchMessages returns
// for the excerpt: a match further in still finds the message, it only
// shows the summary snippet instead.
const searchTextBytes = 64 << 10

// SearchFilter is what SearchMessages looks through. The zero value of
// each field means no restriction by it.
type SearchFilter struct {
	// Match is an FTS5 expression (search.Query.MatchExpr); "" searches
	// by the other fields alone.
	Match string
	// AccountID "" means every enabled account.
	AccountID string
	// FolderIDs, when set, are the only folders searched.
	FolderIDs []string
	// ExcludeRoles are folder roles left out (Trash and Junk outside an
	// explicit folder).
	ExcludeRoles []api.FolderRole
	Unread       bool
	Flagged      bool
	Attachments  bool
	After        time.Time // inclusive
	Before       time.Time // exclusive
}

// SearchRow is one result: the message and the start of its plain-text
// body (searchTextBytes at most) for the excerpt.
type SearchRow struct {
	Message
	Text string
}

// ErrSearchRejected is returned when SQLite refuses the full-text
// expression. The caller's expression is built to be always valid; the
// error never repeats it, since it is what the user typed.
var ErrSearchRejected = errors.New("full-text query rejected")

// SearchMessages returns one page of the messages that match f, newest
// first (date, then id, descending), and how many match: the exact number
// up to api.MaxSearchTotal, -1 beyond it (counting every match of a
// two-letter prefix in a large store costs as much as the search). The
// cursor is opaque and belongs to search: a message.list cursor (or a
// malformed one) is ErrBadCursor. limit <= 0 means 50.
func (s *Store) SearchMessages(ctx context.Context, f SearchFilter, cursor string, limit int) (items []SearchRow, next string, total int, err error) {
	if limit <= 0 {
		limit = 50
	}
	var cursorStamp, cursorID string
	if cursor != "" {
		if cursorStamp, cursorID, err = decodeSortCursor(cursor, searchCursorPrefix); err != nil {
			return nil, "", 0, err
		}
	}
	from, where, args := searchScope(f)

	countArgs := append(append([]any{}, args...), api.MaxSearchTotal+1)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM `+from+where+` LIMIT ?)`, countArgs...).Scan(&total); err != nil {
		return nil, "", 0, searchError(err)
	}
	if total > api.MaxSearchTotal {
		total = -1
	}
	// The matches are sorted as narrow (id, date) rows and only the page
	// is joined for its columns and text: sorting whole rows made a
	// two-letter search of a large store several times slower.
	inner := `SELECT m.id AS id, m.date AS date FROM ` + from + where
	pageArgs := append([]any{searchTextBytes}, args...)
	if cursor != "" {
		inner += ` AND (m.date < ? OR (m.date = ? AND m.id < ?))`
		pageArgs = append(pageArgs, cursorStamp, cursorStamp, cursorID)
	}
	inner += ` ORDER BY m.date DESC, m.id DESC LIMIT ?`
	pageArgs = append(pageArgs, limit+1)
	query := `SELECT ` + qualifiedMessageColumns + `, substr(m.text_body, 1, ?) FROM (` + inner + `) p
		JOIN messages m ON m.id = p.id ORDER BY p.date DESC, p.id DESC`

	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, "", 0, searchError(err)
	}
	defer rows.Close()
	var stamps []string
	for rows.Next() {
		var text string
		m, st, err := scanMessageStamp(withExtra{rows, []any{&text}})
		if err != nil {
			return nil, "", 0, fmt.Errorf("scan search result: %w", err)
		}
		items = append(items, SearchRow{Message: m, Text: text})
		stamps = append(stamps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, searchError(err)
	}
	if len(items) > limit {
		items = items[:limit]
		next = encodeSortCursor(searchCursorPrefix, stamps[limit-1], items[limit-1].ID)
	}
	return items, next, total, nil
}

// searchCursorPrefix tags search cursors, so a message.list cursor ("d",
// "a") is not taken for one.
const searchCursorPrefix = "s"

// searchScope builds the FROM and WHERE of a search, every value a
// parameter. Messages are m, their folders f.
func searchScope(f SearchFilter) (from, where string, args []any) {
	var conds []string
	if f.Match != "" {
		from = `messages_fts JOIN search_docs d ON d.docid = messages_fts.rowid JOIN messages m ON m.id = d.message_id`
		conds = append(conds, `messages_fts MATCH ?`)
		args = append(args, f.Match)
	} else {
		from = `messages m`
	}
	from += ` JOIN folders f ON f.id = m.folder_id`
	if f.AccountID != "" {
		conds = append(conds, `m.account_id = ?`)
		args = append(args, f.AccountID)
	} else {
		conds = append(conds, `m.account_id IN (SELECT id FROM accounts WHERE enabled = 1)`)
	}
	if len(f.FolderIDs) > 0 {
		conds = append(conds, `m.folder_id IN (`+placeholders(len(f.FolderIDs))+`)`)
		for _, id := range f.FolderIDs {
			args = append(args, id)
		}
	}
	if len(f.ExcludeRoles) > 0 {
		conds = append(conds, `f.role NOT IN (`+placeholders(len(f.ExcludeRoles))+`)`)
		for _, r := range f.ExcludeRoles {
			args = append(args, string(r))
		}
	}
	if f.Unread {
		conds = append(conds, `m.unread = 1`)
	}
	if f.Flagged {
		conds = append(conds, `m.flagged = 1`)
	}
	if f.Attachments {
		conds = append(conds, `m.has_attachments = 1`)
	}
	if !f.After.IsZero() {
		conds = append(conds, `m.date >= ?`)
		args = append(args, stamp(f.After))
	}
	if !f.Before.IsZero() {
		conds = append(conds, `m.date < ?`)
		args = append(args, stamp(f.Before))
	}
	return from, ` WHERE ` + strings.Join(conds, ` AND `), args
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// searchError wraps a query error; a complaint about the full-text
// expression may quote it, which is what the user typed, so it becomes
// ErrSearchRejected instead.
func searchError(err error) error {
	if msg := err.Error(); strings.Contains(msg, "fts5") || strings.Contains(msg, "unterminated string") {
		return ErrSearchRejected
	}
	return fmt.Errorf("search messages: %w", err)
}

// withExtra scans the columns scanMessageStamp knows and then extra ones.
type withExtra struct {
	rows  *sql.Rows
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.extra...)...) }

// qualifiedMessageColumns is messageColumns with the m. alias of the
// search queries, whose joins share column names (id, subject).
var qualifiedMessageColumns = func() string {
	cols := strings.Split(messageColumns, ",")
	for i, c := range cols {
		cols[i] = "m." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}()

// IndexSearchBatch indexes, in one transaction, the messages after afterID
// in id order that the full-text index does not hold yet: the rows stored
// before migration 0013, which core.Maintain visits in the background.
// A batch stops at limit rows or once maxBytes of body text are covered,
// so the write lock is short. lastID "" means there is nothing after
// afterID; indexed counts the rows added.
func (s *Store) IndexSearchBatch(ctx context.Context, afterID string, limit int, maxBytes int64) (lastID string, indexed int, err error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, length(text_body) FROM messages WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	var bytes int64
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return "", 0, fmt.Errorf("index search: %w", err)
		}
		lastID = id
		if bytes += n; bytes >= maxBytes && maxBytes > 0 {
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	if lastID == "" {
		return "", 0, tx.Commit()
	}
	var before int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(docid), 0) FROM search_docs`).Scan(&before); err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO search_docs (message_id)
		SELECT id FROM messages WHERE id > ? AND id <= ? AND id NOT IN (SELECT message_id FROM search_docs) ORDER BY id`,
		afterID, lastID)
	if err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages_fts (rowid, subject, sender, recipients, attachments, body)
			SELECT d.docid, t.subject, t.sender, t.recipients, t.attachments, t.body
			  FROM search_docs d JOIN message_search_text t ON t.message_id = d.message_id
			 WHERE d.docid > ?`, before); err != nil {
			return "", 0, fmt.Errorf("index search: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", 0, fmt.Errorf("index search: %w", err)
	}
	return lastID, int(n), nil
}
