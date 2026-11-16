// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Messages kept outside the offlineDays window (migration 0028): rows a
// search on the server stored (search.server). The sync engines spare a
// kept row when they drop what left the window; once its time is over it
// is a row outside the window like any other.

// KeepMessages spares the messages from the retention window until
// until. A message that is kept already keeps the later of the two times;
// an id that names no message (deleted meanwhile) is skipped.
func (s *Store) KeepMessages(ctx context.Context, ids []string, until time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("keep messages: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO kept_messages (message_id, kept_until)
		SELECT id, ? FROM messages WHERE id = ?
		ON CONFLICT (message_id) DO UPDATE SET kept_until = MAX(kept_until, excluded.kept_until)`)
	if err != nil {
		return fmt.Errorf("keep messages: %w", err)
	}
	defer stmt.Close()
	stampUntil := until.UTC().Format(timeLayout)
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, stampUntil, id); err != nil {
			return fmt.Errorf("keep message %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("keep messages: %w", err)
	}
	return nil
}

// KeptUIDs lists the UIDs of a folder's rows that are kept at now,
// ascending.
func (s *Store) KeptUIDs(ctx context.Context, folderID string, now time.Time) ([]uint32, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.uid FROM kept_messages k JOIN messages m ON m.id = k.message_id
		WHERE m.folder_id = ? AND m.uid > 0 AND k.kept_until > ? ORDER BY m.uid`,
		folderID, now.UTC().Format(timeLayout))
	if err != nil {
		return nil, fmt.Errorf("list kept uids: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan kept uid: %w", err)
		}
		out = append(out, uint32(uid))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list kept uids: %w", err)
	}
	return out, nil
}

// KeptRemoteIDs is the set of the remote ids of a folder's rows that are
// kept at now.
func (s *Store) KeptRemoteIDs(ctx context.Context, folderID string, now time.Time) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.remote_id FROM kept_messages k JOIN messages m ON m.id = k.message_id
		WHERE m.folder_id = ? AND m.remote_id != '' AND k.kept_until > ?`,
		folderID, now.UTC().Format(timeLayout))
	if err != nil {
		return nil, fmt.Errorf("list kept remote ids: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan kept remote id: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list kept remote ids: %w", err)
	}
	return out, nil
}

// IsKept reports whether the message is kept at now.
func (s *Store) IsKept(ctx context.Context, id string, now time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kept_messages WHERE message_id = ? AND kept_until > ?`,
		id, now.UTC().Format(timeLayout)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("read kept message: %w", err)
	}
	return n > 0, nil
}

// RenameRemoteIDs re-keys the folder's rows that a search on the server
// stored under the service's REST ids to the immutable ids the sync keys
// messages by (graph.Found.Renamed: REST id → immutable id). A row the
// folder already holds under the immutable id makes the REST-id row a
// second row of the same message: that one is removed with its pending
// operations and raw files, as a row the server no longer lists, and the
// folder is recounted. Ids the folder does not hold are skipped.
func (s *Store) RenameRemoteIDs(ctx context.Context, folderID string, renames map[string]string) error {
	if len(renames) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename remote ids: %w", err)
	}
	defer tx.Rollback()
	var files []messageFile
	for from, to := range renames {
		if from == "" || to == "" || from == to {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE folder_id = ? AND remote_id = ?`, folderID, to).Scan(&n); err != nil {
			return fmt.Errorf("rename remote ids: %w", err)
		}
		if n > 0 {
			got, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ? AND remote_id = ?`, folderID, from)
			if err != nil {
				return err
			}
			files = append(files, got...)
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET remote_id = ?, updated_at = ? WHERE folder_id = ? AND remote_id = ?`,
			to, nowStamp(), folderID, from); err != nil {
			return fmt.Errorf("rename remote id: %w", err)
		}
	}
	if len(files) > 0 {
		if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
			return err
		}
		if _, _, err := recountFolderTx(ctx, tx, folderID); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("rename remote ids: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rename remote ids: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}
