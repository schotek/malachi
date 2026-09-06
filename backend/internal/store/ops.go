// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// OpKind is the type of a queued local change.
type OpKind string

const (
	OpFlag   OpKind = "flag"
	OpMove   OpKind = "move"
	OpDelete OpKind = "delete"
)

// Op is a row of message_ops: one local change waiting to be pushed.
// FolderID/UID/RemoteID are the snapshot taken before the change, i.e.
// where the server still has the message; UID 0 without a RemoteID means
// the message had no server identity yet (blocked until AssignUID).
// Set/Clear belong to OpFlag, TargetFolderID to OpMove.
type Op struct {
	ID             int64
	AccountID      string
	Kind           OpKind
	MessageID      string
	FolderID       string
	UID            uint32
	RemoteID       string
	Set, Clear     []api.Flag
	TargetFolderID string
	Attempts       int
	NextAttemptAt  time.Time // zero = due now
	LastError      string
	CreatedAt      time.Time
}

// opPayload is the JSON form of the kind-specific fields.
type opPayload struct {
	Set            []api.Flag `json:"set,omitempty"`
	Clear          []api.Flag `json:"clear,omitempty"`
	TargetFolderID string     `json:"targetFolderId,omitempty"`
}

// messageLoc is the (folder, uid) snapshot of one message inside a
// mutation transaction. outbox is set for a row of the account's outbox
// pseudo-folder; outboxState is then its delivery state ("" when the row
// has no outbox entry, which should not happen).
type messageLoc struct {
	id          string
	folderID    string
	uid         uint32
	remoteID    string
	flags       []api.Flag
	outbox      bool
	outboxState OutboxState
}

// rejectOutbox is the guard of the operations an outbox row cannot take:
// ErrOutbox when any of locs lives in the outbox folder.
func rejectOutbox(locs []messageLoc) error {
	for _, loc := range locs {
		if loc.outbox {
			return ErrOutbox
		}
	}
	return nil
}

// FlagMessages sets and clears flags on the messages locally and queues one
// flag operation per message. All-or-nothing: an id the account does not
// own → ErrNotFound, an id in the outbox → ErrOutbox, and nothing changes.
// Duplicate ids are processed once; affected folders are recounted.
func (s *Store) FlagMessages(ctx context.Context, accountID string, ids []string, set, clear []api.Flag) error {
	return s.mutate(ctx, accountID, ids, func(ctx context.Context, tx *sql.Tx, locs []messageLoc, _ string) ([]messageFile, error) {
		if err := rejectOutbox(locs); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(opPayload{Set: normalizeFlags(set), Clear: normalizeFlags(clear)})
		if err != nil {
			return nil, fmt.Errorf("encode flag operation: %w", err)
		}
		now := nowStamp()
		for _, loc := range locs {
			flags := applyFlags(loc.flags, set, clear)
			encoded, unread, flagged := encodeFlags(flags)
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET flags = ?, unread = ?, flagged = ?, updated_at = ? WHERE id = ?`,
				encoded, unread, flagged, now, loc.id); err != nil {
				return nil, fmt.Errorf("flag message: %w", err)
			}
			if err := enqueueOp(ctx, tx, accountID, OpFlag, loc, string(payload), now); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

// MoveMessages moves the messages to targetFolderID locally (the row keeps
// its id, gets UID 0 and waits for AssignUID) and queues one move operation
// per message with the source (folder, uid) snapshot. Messages already in
// the target are skipped. ErrNotFound for a foreign/unknown message id or
// target folder, ErrOutbox when a message lives in the outbox or the target
// is the outbox folder; nothing changes then.
func (s *Store) MoveMessages(ctx context.Context, accountID string, ids []string, targetFolderID string) error {
	return s.mutate(ctx, accountID, ids, func(ctx context.Context, tx *sql.Tx, locs []messageLoc, outboxFolderID string) ([]messageFile, error) {
		if err := requireFolder(ctx, tx, accountID, targetFolderID); err != nil {
			return nil, err
		}
		if targetFolderID == outboxFolderID {
			return nil, ErrOutbox
		}
		if err := rejectOutbox(locs); err != nil {
			return nil, err
		}
		unsynced, err := folderUnsynced(ctx, tx, targetFolderID)
		if err != nil {
			return nil, err
		}
		now := nowStamp()
		var files []messageFile
		for _, loc := range locs {
			if loc.folderID == targetFolderID {
				continue
			}
			if unsynced {
				// The target is never downloaded (Gmail's All Mail): the
				// server gets the move as usual, but locally the message
				// is gone, as an archived message is from a Gmail inbox.
				if err := archiveMessageTx(ctx, tx, accountID, loc, targetFolderID, now); err != nil {
					return nil, err
				}
				files = append(files, messageFile{accountID: accountID, id: loc.id})
				continue
			}
			if err := moveMessageTx(ctx, tx, accountID, loc, targetFolderID, now); err != nil {
				return nil, err
			}
		}
		return files, nil
	}, targetFolderID)
}

// folderUnsynced reads the flag of a folder requireFolder has vouched for.
func folderUnsynced(ctx context.Context, tx *sql.Tx, folderID string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT unsynced FROM folders WHERE id = ?`, folderID).Scan(&n); err != nil {
		return false, fmt.Errorf("lookup folder: %w", err)
	}
	return n != 0, nil
}

// TrashMessages moves the messages to the trash folder; a message already
// in the trash is deleted permanently instead (row now, raw file after the
// commit, delete operation queued). A message in the outbox is deleted
// permanently as well, without an operation (it has no server copy);
// ErrOutboxBusy when its delivery is in progress. One transaction;
// ErrNotFound as for MoveMessages; nothing changes on any error.
func (s *Store) TrashMessages(ctx context.Context, accountID string, ids []string, trashFolderID string) error {
	return s.mutate(ctx, accountID, ids, func(ctx context.Context, tx *sql.Tx, locs []messageLoc, _ string) ([]messageFile, error) {
		if err := requireFolder(ctx, tx, accountID, trashFolderID); err != nil {
			return nil, err
		}
		if err := rejectSendingOutbox(locs); err != nil {
			return nil, err
		}
		now := nowStamp()
		var files []messageFile
		for _, loc := range locs {
			switch {
			case loc.outbox:
				if err := deleteOutboxRowTx(ctx, tx, loc); err != nil {
					return nil, err
				}
				files = append(files, messageFile{accountID: accountID, id: loc.id})
				continue
			case loc.folderID == trashFolderID:
				if err := deleteMessageTx(ctx, tx, accountID, loc, now); err != nil {
					return nil, err
				}
				files = append(files, messageFile{accountID: accountID, id: loc.id})
				continue
			}
			if err := moveMessageTx(ctx, tx, accountID, loc, trashFolderID, now); err != nil {
				return nil, err
			}
		}
		return files, nil
	}, trashFolderID)
}

// DeleteMessages removes the messages permanently: rows now, raw files
// after the commit, one delete operation per message so the server copy is
// expunged (none for a message in the outbox, which has no server copy;
// ErrOutboxBusy when its delivery is in progress). ErrNotFound for a
// foreign/unknown id; nothing changes on any error.
func (s *Store) DeleteMessages(ctx context.Context, accountID string, ids []string) error {
	return s.mutate(ctx, accountID, ids, func(ctx context.Context, tx *sql.Tx, locs []messageLoc, _ string) ([]messageFile, error) {
		if err := rejectSendingOutbox(locs); err != nil {
			return nil, err
		}
		now := nowStamp()
		files := make([]messageFile, 0, len(locs))
		for _, loc := range locs {
			if loc.outbox {
				if err := deleteOutboxRowTx(ctx, tx, loc); err != nil {
					return nil, err
				}
			} else if err := deleteMessageTx(ctx, tx, accountID, loc, now); err != nil {
				return nil, err
			}
			files = append(files, messageFile{accountID: accountID, id: loc.id})
		}
		return files, nil
	})
}

// rejectSendingOutbox is the guard of the deletions: ErrOutboxBusy when any
// of locs is an outbox row whose SMTP session is running.
func rejectSendingOutbox(locs []messageLoc) error {
	for _, loc := range locs {
		if loc.outbox && loc.outboxState == OutboxSending {
			return ErrOutboxBusy
		}
	}
	return nil
}

// deleteOutboxRowTx drops an outbox message locally: the row (its outbox
// entry cascades) and, defensively, any operation that names it. No delete
// operation is queued — the server never had the message.
func deleteOutboxRowTx(ctx context.Context, tx *sql.Tx, loc messageLoc) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_ops WHERE message_id = ?`, loc.id); err != nil {
		return fmt.Errorf("delete outbox message operations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, loc.id); err != nil {
		return fmt.Errorf("delete outbox message: %w", err)
	}
	return nil
}

// mutate runs one local change in a transaction: it resolves every id to
// its (folder, uid) snapshot (ErrNotFound aborts before anything is
// changed), applies fn — which also learns the account's outbox folder id
// ("" when the account has none yet) — recounts the source folders plus
// extraFolders and unlinks the files fn hands back after the commit.
func (s *Store) mutate(ctx context.Context, accountID string, ids []string,
	fn func(ctx context.Context, tx *sql.Tx, locs []messageLoc, outboxFolderID string) ([]messageFile, error), extraFolders ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var outboxFolderID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM folders WHERE account_id = ? AND role = ? ORDER BY position, path, id LIMIT 1`,
		accountID, string(api.RoleOutbox)).Scan(&outboxFolderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup outbox folder: %w", err)
	}

	locs := make([]messageLoc, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	folders := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var loc messageLoc
		var uid int64
		var flags, outboxState string
		err := tx.QueryRowContext(ctx, `
			SELECT m.id, m.folder_id, m.uid, m.remote_id, m.flags, COALESCE(o.state, '')
			FROM messages m LEFT JOIN outbox o ON o.message_id = m.id
			WHERE m.id = ? AND m.account_id = ?`,
			id, accountID).Scan(&loc.id, &loc.folderID, &uid, &loc.remoteID, &flags, &outboxState)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("lookup message: %w", err)
		}
		loc.uid = uint32(uid)
		if loc.flags, err = decodeFlags(flags); err != nil {
			return fmt.Errorf("decode flags of %s: %w", id, err)
		}
		loc.outbox = outboxFolderID != "" && loc.folderID == outboxFolderID
		loc.outboxState = OutboxState(outboxState)
		locs = append(locs, loc)
		folders[loc.folderID] = true
	}
	files, err := fn(ctx, tx, locs, outboxFolderID)
	if err != nil {
		return err
	}
	for _, f := range extraFolders {
		folders[f] = true
	}
	sorted := make([]string, 0, len(folders))
	for f := range folders {
		sorted = append(sorted, f)
	}
	sort.Strings(sorted)
	for _, f := range sorted {
		if _, _, err := recountFolderTx(ctx, tx, f); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

func requireFolder(ctx context.Context, tx *sql.Tx, accountID, folderID string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE id = ? AND account_id = ?`, folderID, accountID).Scan(&n); err != nil {
		return fmt.Errorf("lookup folder: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func moveMessageTx(ctx context.Context, tx *sql.Tx, accountID string, loc messageLoc, targetFolderID, now string) error {
	if err := enqueueMoveTx(ctx, tx, accountID, loc, targetFolderID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET folder_id = ?, uid = 0, modseq = 0, updated_at = ? WHERE id = ?`,
		targetFolderID, now, loc.id); err != nil {
		return fmt.Errorf("move message: %w", err)
	}
	return nil
}

// archiveMessageTx is a move into a folder that is never downloaded: the
// operation is queued like any move, but the local row goes, as for a
// delete; the caller removes the raw file after the commit.
func archiveMessageTx(ctx context.Context, tx *sql.Tx, accountID string, loc messageLoc, targetFolderID, now string) error {
	if err := enqueueMoveTx(ctx, tx, accountID, loc, targetFolderID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, loc.id); err != nil {
		return fmt.Errorf("archive message: %w", err)
	}
	return nil
}

func enqueueMoveTx(ctx context.Context, tx *sql.Tx, accountID string, loc messageLoc, targetFolderID, now string) error {
	payload, err := json.Marshal(opPayload{TargetFolderID: targetFolderID})
	if err != nil {
		return fmt.Errorf("encode move operation: %w", err)
	}
	return enqueueOp(ctx, tx, accountID, OpMove, loc, string(payload), now)
}

func deleteMessageTx(ctx context.Context, tx *sql.Tx, accountID string, loc messageLoc, now string) error {
	if err := enqueueOp(ctx, tx, accountID, OpDelete, loc, "{}", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, loc.id); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

func enqueueOp(ctx context.Context, tx *sql.Tx, accountID string, kind OpKind, loc messageLoc, payload, now string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_ops (account_id, kind, message_id, folder_id, uid, remote_id, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, string(kind), loc.id, loc.folderID, int64(loc.uid), loc.remoteID, payload, now); err != nil {
		return fmt.Errorf("enqueue %s operation: %w", kind, err)
	}
	return nil
}

// applyFlags returns (flags ∪ set) − clear.
func applyFlags(flags, set, clear []api.Flag) []api.Flag {
	drop := make(map[api.Flag]bool, len(clear))
	for _, f := range clear {
		drop[f] = true
	}
	out := make([]api.Flag, 0, len(flags)+len(set))
	for _, f := range append(append([]api.Flag{}, flags...), set...) {
		if !drop[f] {
			out = append(out, f)
		}
	}
	return normalizeFlags(out)
}

// NextOps returns the account's operations that are due at now (never
// attempted, or whose retry time has passed), oldest first, at most limit
// (<= 0 → 100). Blocked operations (no server identity) are included; the
// caller decides what to do with them.
func (s *Store) NextOps(ctx context.Context, accountID string, now time.Time, limit int) ([]Op, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, account_id, kind, message_id, folder_id, uid, remote_id, payload, attempts, next_attempt_at, last_error, created_at
		FROM message_ops
		WHERE account_id = ? AND (next_attempt_at = '' OR next_attempt_at <= ?)
		ORDER BY id LIMIT ?`, accountID, stamp(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	defer rows.Close()
	var out []Op
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	return out, nil
}

// MarkOpDone removes a pushed operation. ErrNotFound for an unknown id.
func (s *Store) MarkOpDone(ctx context.Context, id int64) error {
	return s.deleteOp(ctx, id, "mark operation done")
}

// MarkOpFailed records a failed attempt: attempts is incremented, retryAt
// becomes the earliest next attempt and errText is kept for diagnostics.
// ErrNotFound for an unknown id.
func (s *Store) MarkOpFailed(ctx context.Context, id int64, errText string, retryAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE message_ops SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?`,
		stamp(retryAt), errText, id)
	if err != nil {
		return fmt.Errorf("mark operation failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DropOp discards an operation that will never succeed. ErrNotFound for an
// unknown id.
func (s *Store) DropOp(ctx context.Context, id int64) error {
	return s.deleteOp(ctx, id, "drop operation")
}

func (s *Store) deleteOp(ctx context.Context, id int64, what string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_ops WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountPendingOps returns how many operations of the account are still
// queued, due or not.
func (s *Store) CountPendingOps(ctx context.Context, accountID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_ops WHERE account_id = ?`, accountID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count operations: %w", err)
	}
	return n, nil
}

func scanOp(row scanner) (Op, error) {
	var op Op
	var kind, payload, next, created string
	var uid int64
	if err := row.Scan(&op.ID, &op.AccountID, &kind, &op.MessageID, &op.FolderID, &uid, &op.RemoteID, &payload,
		&op.Attempts, &next, &op.LastError, &created); err != nil {
		return Op{}, fmt.Errorf("scan operation: %w", err)
	}
	op.Kind = OpKind(kind)
	op.UID = uint32(uid)
	var p opPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return Op{}, fmt.Errorf("decode payload of operation %d: %w", op.ID, err)
	}
	op.Set, op.Clear, op.TargetFolderID = p.Set, p.Clear, p.TargetFolderID
	op.NextAttemptAt = parseStamp(next)
	op.CreatedAt = parseStamp(created)
	return op, nil
}
