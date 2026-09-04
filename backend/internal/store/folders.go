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

// Folder is a row of the folders table: the local mirror of one IMAP
// mailbox. Mailbox is the raw server name, Path the "/"-separated display
// path. ParentMailbox is input-only for UpsertFolders (it is resolved to
// ParentID within the batch); on read it is filled from the parent row.
type Folder struct {
	ID            string
	AccountID     string
	Mailbox       string
	Delimiter     string
	ParentMailbox string
	ParentID      string
	Name          string
	Path          string
	Role          api.FolderRole
	Subscribed    bool
	Selectable    bool

	// Sync engine state; untouched by UpsertFolders for existing rows.
	UIDValidity    uint32
	UIDNext        uint32
	HighestModSeq  uint64
	ServerMessages int
	ServerUnseen   int
	Unread         int // local count, see RecountFolder
	Total          int
	LastSyncAt     time.Time

	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FolderSyncState is the part of a folder the sync engine owns.
type FolderSyncState struct {
	UIDValidity    uint32
	UIDNext        uint32
	HighestModSeq  uint64
	ServerMessages int
	ServerUnseen   int
	LastSyncAt     time.Time
}

// zeroStamp is how the zero time is stored in sortable date columns: never
// empty, so a cursor can always be built from it.
var zeroStamp = time.Time{}.UTC().Format(timeLayout)

// stamp formats t for a date column; zero → zeroStamp.
func stamp(t time.Time) string {
	if t.IsZero() {
		return zeroStamp
	}
	return t.UTC().Format(timeLayout)
}

// optStamp formats t for an optional column; zero → empty string.
func optStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// UpsertFolders replaces the folder list of an account with folders, in
// that order (Position is the index in the batch). Rows are matched on
// (account, Mailbox): existing rows keep their id, timestamps, counts and
// sync columns and take the descriptive fields from the batch; new rows get
// an "f_" id. ParentID is resolved from ParentMailbox within the batch (an
// unknown parent → no parent). Folders of the account absent from the batch
// are deleted with their messages, pending operations and raw files (files
// after the commit). The stored rows are returned in batch order together
// with the ids of the removed folders. A mailbox listed twice is an error
// and nothing is changed.
//
// The account's outbox pseudo-folder (OutboxFolder) is local only: it is
// neither deleted nor returned, and a batch entry claiming its reserved
// mailbox "" or role outbox is an error.
func (s *Store) UpsertFolders(ctx context.Context, accountID string, folders []Folder) (stored []Folder, removed []string, err error) {
	ids := make(map[string]string, len(folders)) // mailbox → id
	for _, f := range folders {
		if f.Mailbox == "" {
			return nil, nil, fmt.Errorf("upsert folders: empty mailbox is reserved for the outbox")
		}
		if f.Role == api.RoleOutbox {
			return nil, nil, fmt.Errorf("upsert folders: role %q is reserved for the outbox", f.Role)
		}
		if _, dup := ids[f.Mailbox]; dup {
			return nil, nil, fmt.Errorf("upsert folders: duplicate mailbox %q", f.Mailbox)
		}
		ids[f.Mailbox] = ""
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("upsert folders: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id, mailbox, role FROM folders WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("list folders: %w", err)
	}
	var stale []string
	for rows.Next() {
		var id, mailbox, role string
		if err := rows.Scan(&id, &mailbox, &role); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan folder: %w", err)
		}
		if role == string(api.RoleOutbox) {
			continue // local only; the server list has no say over it
		}
		if _, keep := ids[mailbox]; keep {
			ids[mailbox] = id
		} else {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list folders: %w", err)
	}
	for mailbox, id := range ids {
		if id == "" {
			ids[mailbox] = newID("f_")
		}
	}

	now := nowStamp()
	stored = make([]Folder, 0, len(folders))
	for i, f := range folders {
		parentID := ""
		if f.ParentMailbox != "" {
			parentID = ids[f.ParentMailbox] // "" when the parent is not in the batch
		}
		role := f.Role
		if role == "" {
			role = api.RoleNone
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO folders (id, account_id, mailbox, delimiter, parent_id, name, path, role,
			                     subscribed, selectable, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, mailbox) DO UPDATE SET
				delimiter = excluded.delimiter, parent_id = excluded.parent_id, name = excluded.name,
				path = excluded.path, role = excluded.role, subscribed = excluded.subscribed,
				selectable = excluded.selectable, position = excluded.position, updated_at = excluded.updated_at
			RETURNING `+folderColumns,
			ids[f.Mailbox], accountID, f.Mailbox, f.Delimiter, parentID, f.Name, f.Path, string(role),
			boolInt(f.Subscribed), boolInt(f.Selectable), i, now, now)
		got, err := scanFolder(row)
		if err != nil {
			return nil, nil, fmt.Errorf("upsert folder %q: %w", f.Mailbox, err)
		}
		got.ParentMailbox = f.ParentMailbox
		if parentID == "" {
			got.ParentMailbox = ""
		}
		stored = append(stored, got)
	}

	var files []messageFile
	if len(stale) > 0 {
		if files, err = deleteFoldersTx(ctx, tx, stale); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("upsert folders: %w", err)
	}
	s.removeMessageFiles(files)
	return stored, stale, nil
}

// ListFolders returns the account's folders in position order.
func (s *Store) ListFolders(ctx context.Context, accountID string) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+folderColumns+` FROM folders WHERE account_id = ? ORDER BY position, path, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan folder: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	return out, nil
}

// GetFolder returns one folder of the account; ErrNotFound otherwise (also
// for a folder of another account).
func (s *Store) GetFolder(ctx context.Context, accountID, id string) (Folder, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+folderColumns+` FROM folders WHERE id = ? AND account_id = ?`, id, accountID)
	f, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("get folder: %w", err)
	}
	return f, nil
}

// FolderByRole returns the first folder (in position order) of the account
// with the given special-use role; ErrNotFound when there is none.
func (s *Store) FolderByRole(ctx context.Context, accountID string, role api.FolderRole) (Folder, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+folderColumns+` FROM folders WHERE account_id = ? AND role = ? ORDER BY position, path, id LIMIT 1`,
		accountID, string(role))
	f, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("folder by role: %w", err)
	}
	return f, nil
}

// DeleteFolder removes one folder with its messages, pending operations and
// raw files (files after the commit); children become roots. ErrNotFound
// when the account has no such folder.
func (s *Store) DeleteFolder(ctx context.Context, accountID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	defer tx.Rollback()

	var found string
	err = tx.QueryRowContext(ctx, `SELECT id FROM folders WHERE id = ? AND account_id = ?`, id, accountID).Scan(&found)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("delete folder: %w", err)
	}
	files, err := deleteFoldersTx(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// deleteFoldersTx removes the folders (messages cascade), their pending
// operations and detaches their children; it returns the raw files to
// unlink after the commit.
func deleteFoldersTx(ctx context.Context, tx *sql.Tx, ids []string) ([]messageFile, error) {
	var files []messageFile
	for _, chunk := range chunkStrings(ids, 500) {
		in := inPlaceholders(len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		got, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id IN (`+in+`)`, args...)
		if err != nil {
			return nil, err
		}
		files = append(files, got...)
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_ops WHERE folder_id IN (`+in+`)`, args...); err != nil {
			return nil, fmt.Errorf("delete folder operations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET parent_id = '' WHERE parent_id IN (`+in+`)`, args...); err != nil {
			return nil, fmt.Errorf("detach folder children: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id IN (`+in+`)`, args...); err != nil {
			return nil, fmt.Errorf("delete folders: %w", err)
		}
	}
	return files, nil
}

// SetFolderSyncState stores the engine-owned columns of a folder.
// ErrNotFound for an unknown id.
func (s *Store) SetFolderSyncState(ctx context.Context, id string, st FolderSyncState) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE folders SET uidvalidity = ?, uidnext = ?, highestmodseq = ?, server_messages = ?,
		       server_unseen = ?, last_sync_at = ?, updated_at = ?
		WHERE id = ?`,
		int64(st.UIDValidity), int64(st.UIDNext), int64(st.HighestModSeq), st.ServerMessages,
		st.ServerUnseen, optStamp(st.LastSyncAt), nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set folder sync state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetFolder forgets everything synchronised for a folder (UIDVALIDITY
// changed): its messages with their raw files, its pending operations and
// the server counters; the folder then carries the new uidValidity.
// ErrNotFound for an unknown id.
func (s *Store) ResetFolder(ctx context.Context, id string, uidValidity uint32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reset folder: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE folders SET uidvalidity = ?, uidnext = 0, highestmodseq = 0, server_messages = 0,
		       server_unseen = 0, unread = 0, total = 0, last_sync_at = '', updated_at = ?
		WHERE id = ?`, int64(uidValidity), nowStamp(), id)
	if err != nil {
		return fmt.Errorf("reset folder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	files, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ?`, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE folder_id = ?`, id); err != nil {
		return fmt.Errorf("reset folder messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_ops WHERE folder_id = ?`, id); err != nil {
		return fmt.Errorf("reset folder operations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reset folder: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// RecountFolder recomputes the local unread/total counts of a folder from
// its messages and returns them. ErrNotFound for an unknown id.
func (s *Store) RecountFolder(ctx context.Context, id string) (unread, total int, err error) {
	unread, total, err = recountFolderTx(ctx, s.db, id)
	if err != nil {
		return 0, 0, err
	}
	return unread, total, nil
}

// execQuerier is what both *sql.DB and *sql.Tx provide.
type execQuerier interface {
	querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func recountFolderTx(ctx context.Context, q execQuerier, id string) (unread, total int, err error) {
	err = q.QueryRowContext(ctx, `
		UPDATE folders SET
			unread = (SELECT COUNT(*) FROM messages WHERE folder_id = folders.id AND unread = 1),
			total  = (SELECT COUNT(*) FROM messages WHERE folder_id = folders.id)
		WHERE id = ?
		RETURNING unread, total`, id).Scan(&unread, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("recount folder: %w", err)
	}
	return unread, total, nil
}

const folderColumns = `id, account_id, mailbox, delimiter, parent_id,
	COALESCE((SELECT p.mailbox FROM folders p WHERE p.id = folders.parent_id), ''),
	name, path, role, subscribed, selectable, uidvalidity, uidnext, highestmodseq,
	server_messages, server_unseen, unread, total, last_sync_at, position, created_at, updated_at`

func scanFolder(row scanner) (Folder, error) {
	var f Folder
	var role, lastSync, created, updated string
	var subscribed, selectable int
	var uidvalidity, uidnext, modseq int64
	if err := row.Scan(&f.ID, &f.AccountID, &f.Mailbox, &f.Delimiter, &f.ParentID, &f.ParentMailbox,
		&f.Name, &f.Path, &role, &subscribed, &selectable, &uidvalidity, &uidnext, &modseq,
		&f.ServerMessages, &f.ServerUnseen, &f.Unread, &f.Total, &lastSync, &f.Position, &created, &updated); err != nil {
		return Folder{}, err
	}
	f.Role = api.FolderRole(role)
	f.Subscribed, f.Selectable = subscribed != 0, selectable != 0
	f.UIDValidity, f.UIDNext, f.HighestModSeq = uint32(uidvalidity), uint32(uidnext), uint64(modseq)
	f.LastSyncAt = parseStamp(lastSync)
	f.CreatedAt, f.UpdatedAt = parseStamp(created), parseStamp(updated)
	return f, nil
}

// inPlaceholders returns "?,?,…" with n entries.
func inPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return "?" + strings.Repeat(",?", n-1)
}

func chunkStrings(in []string, size int) [][]string {
	var out [][]string
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}
