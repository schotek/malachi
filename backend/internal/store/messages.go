// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/thread"
	"github.com/schotek/malachi/backend/pkg/api"
)

// BodyState says what the store holds of a message's content.
type BodyState string

const (
	BodyNone    BodyState = "none"    // headers only; the body has not been fetched
	BodyFetched BodyState = "fetched" // text body (and raw file) available
	BodyTooBig  BodyState = "tooBig"  // over the raw-message cap; never downloaded
	BodyFailed  BodyState = "failed"  // downloaded but unparsable
)

// Message is a row of the messages table. UID is the IMAP UID within
// FolderID, or 0 while a local move waits to be pushed. RemoteID is the
// server's opaque message id for backends that have one (Microsoft Graph);
// IMAP rows leave it empty. The "unread" column is derived from Flags (no
// "seen") and has no field of its own.
type Message struct {
	ID        string
	AccountID string
	FolderID  string
	UID       uint32
	RemoteID  string
	ModSeq    uint64
	Flags     []api.Flag

	From, To, CC, BCC, ReplyTo []api.Address
	Subject                    string
	Date                       time.Time // header date, best effort; the list sort key
	InternalDate               time.Time
	RFCMessageID               string
	InReplyTo                  string
	References                 []string
	Size                       int64
	Snippet                    string
	HasAttachments             bool
	Attachments                []api.Attachment
	Headers                    map[string]string
	HasHTML                    bool
	BodyState                  BodyState
	ThreadID                   string
	CreatedAt, UpdatedAt       time.Time
}

// MessageRef identifies a message whose body is still to be fetched, by
// UID (IMAP) or RemoteID (Graph).
type MessageRef struct {
	ID       string
	UID      uint32
	RemoteID string
	Size     int64
}

// BodyUpdate is what SetMessageBody stores after the raw message was
// parsed. Text, HasHTML, Snippet, Attachments, HasAttachments, Headers,
// References and State (empty → BodyFetched) replace the stored values;
// Subject, From, Date, RFCMessageID and InReplyTo only fill in a column
// that is still empty (the envelope from the server wins over the parser);
// Size replaces the stored size when > 0 (backends whose listing carries
// no size learn it from the download).
type BodyUpdate struct {
	Text           string
	HasHTML        bool
	Snippet        string
	Attachments    []api.Attachment
	HasAttachments bool
	Headers        map[string]string
	References     []string
	State          BodyState
	Size           int64

	Subject      string
	From         []api.Address
	Date         time.Time
	RFCMessageID string
	InReplyTo    string
}

// UpsertMessages stores a batch in one transaction. Rows are matched on
// (FolderID, UID) when UID > 0 or on (FolderID, RemoteID) when RemoteID is
// set: an existing row keeps everything except flags, modseq, thread id
// (remote-id rows only, and only when the batch carries one) and
// updated_at, and msgs[i].ID is set to its id; new rows get an "m_" id
// (an empty msgs[i].ID is filled in). A new row with an empty ThreadID
// gets a local one and is linked into its conversation (threads.go);
// msgs[i].ThreadID is set to the stored id either way. Nothing is stored
// when any row fails. Folder counts are not touched (RecountFolder).
func (s *Store) UpsertMessages(ctx context.Context, msgs []*Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("upsert messages: %w", err)
	}
	defer tx.Rollback()

	// The last parameter is the batch's own thread id (possibly empty):
	// a remote-id row keeps its stored id when the batch has none.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages (id, account_id, folder_id, uid, remote_id, modseq, flags, unread, flagged,
			from_json, to_json, cc_json, bcc_json, reply_to_json, subject, date, internal_date,
			rfc_message_id, in_reply_to, references_json, size, snippet, has_attachments,
			attachments_json, headers_json, has_html, text_body, body_state, thread_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)
		ON CONFLICT (folder_id, uid) WHERE uid > 0 DO UPDATE SET
			flags = excluded.flags, modseq = excluded.modseq, unread = excluded.unread,
			flagged = excluded.flagged, updated_at = excluded.updated_at
		ON CONFLICT (folder_id, remote_id) WHERE remote_id != '' DO UPDATE SET
			flags = excluded.flags, modseq = excluded.modseq, unread = excluded.unread,
			flagged = excluded.flagged,
			thread_id = CASE WHEN ? = '' THEN thread_id ELSE excluded.thread_id END,
			updated_at = excluded.updated_at
		RETURNING id, created_at, thread_id`)
	if err != nil {
		return fmt.Errorf("upsert messages: %w", err)
	}
	defer stmt.Close()

	now := nowStamp()
	type result struct{ id, created, thread string }
	results := make([]result, len(msgs))
	var fresh []linkRow
	for i, m := range msgs {
		if m == nil {
			return fmt.Errorf("upsert messages: nil message at %d", i)
		}
		id := m.ID
		if id == "" {
			id = newID("m_")
		}
		enc, err := encodeMessage(m)
		if err != nil {
			return err
		}
		state := m.BodyState
		if state == "" {
			state = BodyNone
		}
		tid := m.ThreadID
		if tid == "" {
			tid = newID(thread.IDPrefix)
		}
		var r result
		err = stmt.QueryRowContext(ctx,
			id, m.AccountID, m.FolderID, int64(m.UID), m.RemoteID, int64(m.ModSeq), enc.flags, enc.unread, enc.flagged,
			enc.from, enc.to, enc.cc, enc.bcc, enc.replyTo, m.Subject, stamp(m.Date), optStamp(m.InternalDate),
			m.RFCMessageID, m.InReplyTo, enc.references, m.Size, m.Snippet, boolInt(m.HasAttachments),
			enc.attachments, enc.headers, boolInt(m.HasHTML), string(state), tid, now, now, m.ThreadID,
		).Scan(&r.id, &r.created, &r.thread)
		if err != nil {
			return fmt.Errorf("upsert message uid %d in %s: %w", m.UID, m.FolderID, err)
		}
		results[i] = r
		if r.id == id {
			// Inserted, not matched: the id we chose came back.
			fresh = append(fresh, linkRow{id: id, accountID: m.AccountID, threadID: r.thread,
				rfcID: m.RFCMessageID, inReplyTo: m.InReplyTo, references: m.References})
		}
	}
	// References of the whole batch first, so a parent links to the replies
	// that arrived in the same batch whatever the order.
	for _, r := range fresh {
		if err := insertRefsTx(ctx, tx, r.id, r.accountID, r.inReplyTo, r.references); err != nil {
			return err
		}
	}
	for _, r := range fresh {
		if _, err := linkMessageTx(ctx, tx, r); err != nil {
			return err
		}
	}
	if len(fresh) > 0 {
		// A merge may have moved any fresh row; report the final ids.
		ids := make([]string, len(fresh))
		for i, r := range fresh {
			ids[i] = r.id
		}
		final := map[string]string{}
		for _, chunk := range chunkStrings(ids, linkChunk) {
			rows, err := tx.QueryContext(ctx, `SELECT id, thread_id FROM messages WHERE id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...)
			if err != nil {
				return fmt.Errorf("read thread ids: %w", err)
			}
			for rows.Next() {
				var id, tid string
				if err := rows.Scan(&id, &tid); err != nil {
					rows.Close()
					return fmt.Errorf("read thread ids: %w", err)
				}
				final[id] = tid
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("read thread ids: %w", err)
			}
		}
		for i := range results {
			if tid, ok := final[results[i].id]; ok {
				results[i].thread = tid
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upsert messages: %w", err)
	}
	for i, m := range msgs {
		m.ID = results[i].id
		m.ThreadID = results[i].thread
		m.CreatedAt = parseStamp(results[i].created)
		m.UpdatedAt = parseStamp(now)
	}
	return nil
}

// ListUIDs returns the UIDs present in a folder, ascending (pending rows
// with UID 0 are not listed).
// ListUIDsOlderThan returns the assigned UIDs of a folder whose INTERNALDATE
// is before the cutoff, ascending. It backs the client-side retention
// window when the server rejects SEARCH SINCE.
func (s *Store) ListUIDsOlderThan(ctx context.Context, folderID string, before time.Time) ([]uint32, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT uid FROM messages WHERE folder_id = ? AND uid > 0 AND internal_date < ? ORDER BY uid`,
		folderID, before.UTC().Format(timeLayout))
	if err != nil {
		return nil, fmt.Errorf("list old uids: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan uid: %w", err)
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func (s *Store) ListUIDs(ctx context.Context, folderID string) ([]uint32, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uid FROM messages WHERE folder_id = ? AND uid > 0 ORDER BY uid`, folderID)
	if err != nil {
		return nil, fmt.Errorf("list uids: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan uid: %w", err)
		}
		out = append(out, uint32(uid))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list uids: %w", err)
	}
	return out, nil
}

// ListUnfetched returns up to limit messages of the folder whose body has
// not been fetched (BodyNone) and that have a server identity (UID or
// RemoteID), newest first. limit <= 0 → 100.
func (s *Store) ListUnfetched(ctx context.Context, folderID string, limit int) ([]MessageRef, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, uid, remote_id, size FROM messages
		WHERE folder_id = ? AND body_state = 'none' AND (uid > 0 OR remote_id != '')
		ORDER BY date DESC, id DESC LIMIT ?`, folderID, limit)
	if err != nil {
		return nil, fmt.Errorf("list unfetched: %w", err)
	}
	defer rows.Close()
	var out []MessageRef
	for rows.Next() {
		var r MessageRef
		var uid int64
		if err := rows.Scan(&r.ID, &uid, &r.RemoteID, &r.Size); err != nil {
			return nil, fmt.Errorf("scan unfetched: %w", err)
		}
		r.UID = uint32(uid)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list unfetched: %w", err)
	}
	return out, nil
}

// ApplyServerFlags stores the flags the server reports for (folder, uid).
// It is a no-op returning false while a local "flag" operation for the
// message is still pending (the local change wins until it is pushed) and
// when the flags already match (modseq is still refreshed then).
// ErrNotFound when the folder has no such UID.
func (s *Store) ApplyServerFlags(ctx context.Context, folderID string, uid uint32, flags []api.Flag, modseq uint64) (changed bool, err error) {
	return s.applyServerFlags(ctx, `folder_id = ? AND uid = ?`, []any{folderID, int64(uid)}, flags, modseq)
}

// ApplyServerFlagsByRemoteID is ApplyServerFlags for a message addressed
// by its remote id. ErrNotFound when the folder has no such message.
func (s *Store) ApplyServerFlagsByRemoteID(ctx context.Context, folderID, remoteID string, flags []api.Flag, modseq uint64) (changed bool, err error) {
	if remoteID == "" {
		return false, ErrNotFound
	}
	return s.applyServerFlags(ctx, `folder_id = ? AND remote_id = ?`, []any{folderID, remoteID}, flags, modseq)
}

func (s *Store) applyServerFlags(ctx context.Context, where string, args []any, flags []api.Flag, modseq uint64) (changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("apply server flags: %w", err)
	}
	defer tx.Rollback()

	var id, stored string
	var storedModseq int64
	err = tx.QueryRowContext(ctx, `SELECT id, flags, modseq FROM messages WHERE `+where, args...).Scan(&id, &stored, &storedModseq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("apply server flags: %w", err)
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_ops WHERE message_id = ? AND kind = 'flag'`, id).Scan(&pending); err != nil {
		return false, fmt.Errorf("apply server flags: %w", err)
	}
	if pending > 0 {
		return false, nil
	}
	encoded, unread, flagged := encodeFlags(flags)
	old, err := decodeFlags(stored)
	if err != nil {
		return false, fmt.Errorf("decode flags of %s: %w", id, err)
	}
	if sameFlags(old, flags) {
		if storedModseq != int64(modseq) {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET modseq = ? WHERE id = ?`, int64(modseq), id); err != nil {
				return false, fmt.Errorf("apply server flags: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("apply server flags: %w", err)
			}
		}
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET flags = ?, unread = ?, flagged = ?, modseq = ?, updated_at = ? WHERE id = ?`,
		encoded, unread, flagged, int64(modseq), nowStamp(), id); err != nil {
		return false, fmt.Errorf("apply server flags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("apply server flags: %w", err)
	}
	return true, nil
}

// DeleteMessagesByUID removes messages the server no longer has (expunged)
// together with their pending operations and raw files (files after the
// commit). Unknown UIDs are ignored; counts are not touched.
func (s *Store) DeleteMessagesByUID(ctx context.Context, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete messages by uid: %w", err)
	}
	defer tx.Rollback()

	var files []messageFile
	for start := 0; start < len(uids); start += 500 {
		end := min(start+500, len(uids))
		args := []any{folderID}
		for _, uid := range uids[start:end] {
			if uid == 0 {
				continue
			}
			args = append(args, int64(uid))
		}
		if len(args) == 1 {
			continue
		}
		in := inPlaceholders(len(args) - 1)
		got, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ? AND uid IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		files = append(files, got...)
	}
	if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete messages by uid: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// ListRemoteIDs returns the remote ids present in a folder, sorted.
func (s *Store) ListRemoteIDs(ctx context.Context, folderID string) ([]string, error) {
	return s.listRemoteIDs(ctx, `SELECT remote_id FROM messages WHERE folder_id = ? AND remote_id != '' ORDER BY remote_id`, folderID)
}

// ListRemoteIDsOlderThan returns the remote ids of a folder whose
// internal date (the server's received time) is before the cutoff, sorted.
// It backs the retention window of delta-query backends.
func (s *Store) ListRemoteIDsOlderThan(ctx context.Context, folderID string, before time.Time) ([]string, error) {
	return s.listRemoteIDs(ctx,
		`SELECT remote_id FROM messages WHERE folder_id = ? AND remote_id != '' AND internal_date < ? ORDER BY remote_id`,
		folderID, before.UTC().Format(timeLayout))
}

func (s *Store) listRemoteIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list remote ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan remote id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list remote ids: %w", err)
	}
	return out, nil
}

// DeleteMessagesByRemoteID removes messages the server no longer has in
// the folder, together with their pending operations and raw files (files
// after the commit). Unknown ids are ignored; counts are not touched.
func (s *Store) DeleteMessagesByRemoteID(ctx context.Context, folderID string, remoteIDs []string) error {
	if len(remoteIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete messages by remote id: %w", err)
	}
	defer tx.Rollback()

	var files []messageFile
	for _, chunk := range chunkStrings(remoteIDs, 500) {
		args := []any{folderID}
		for _, id := range chunk {
			if id != "" {
				args = append(args, id)
			}
		}
		if len(args) == 1 {
			continue
		}
		in := inPlaceholders(len(args) - 1)
		got, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ? AND remote_id IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		files = append(files, got...)
	}
	if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete messages by remote id: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// MoveByRemoteID records a move the server reports: the account's message
// with the remote id is moved to targetFolderID keeping its local id (uid
// and modseq are reset; pending operations are untouched, their snapshot
// still names the folder they were queued in). It reports whether a row
// was moved: false when the account has no such message or it is already
// in the target. Both folders are recounted.
func (s *Store) MoveByRemoteID(ctx context.Context, accountID, remoteID, targetFolderID string) (moved bool, err error) {
	if remoteID == "" {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("move by remote id: %w", err)
	}
	defer tx.Rollback()

	var id, folderID string
	err = tx.QueryRowContext(ctx, `SELECT id, folder_id FROM messages WHERE account_id = ? AND remote_id = ? ORDER BY updated_at, id LIMIT 1`,
		accountID, remoteID).Scan(&id, &folderID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("move by remote id: %w", err)
	case folderID == targetFolderID:
		return false, nil
	}
	if err := requireFolder(ctx, tx, accountID, targetFolderID); err != nil {
		return false, err
	}
	// A row already holding (target, remote id) — the target was synchronised
	// first — gives way so the local id of the original stays stable.
	dup, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ? AND remote_id = ? AND id != ?`,
		targetFolderID, remoteID, id)
	if err != nil {
		return false, err
	}
	if err := deleteMessageRowsTx(ctx, tx, dup); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET folder_id = ?, uid = 0, modseq = 0, updated_at = ? WHERE id = ?`,
		targetFolderID, nowStamp(), id); err != nil {
		return false, fmt.Errorf("move by remote id: %w", err)
	}
	for _, f := range []string{folderID, targetFolderID} {
		if _, _, err := recountFolderTx(ctx, tx, f); err != nil && !errors.Is(err, ErrNotFound) {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("move by remote id: %w", err)
	}
	s.removeMessageFiles(dup)
	return true, nil
}

// FindPendingMessage returns the oldest row of the folder that still waits
// for a server UID (a local move) and carries the given Message-ID header;
// ErrNotFound otherwise (always for an empty rfcMessageID).
func (s *Store) FindPendingMessage(ctx context.Context, folderID, rfcMessageID string) (Message, error) {
	if rfcMessageID == "" {
		return Message{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE folder_id = ? AND uid = 0 AND rfc_message_id = ? ORDER BY updated_at, id LIMIT 1`, folderID, rfcMessageID)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("find pending message: %w", err)
	}
	return m, nil
}

// AssignUID gives a message that was moved locally its server identity:
// uid, modseq and flags are stored on the row and the pending operations
// that were blocked on it (uid 0) are patched with uid.
//
// The UID belongs to the folder the message was in when the blocked
// operations were queued: the folder of the oldest such operation, or the
// row's current folder when none is pending. If the row has since moved on
// (a second local move is pending) only the operations are patched; the row
// keeps uid 0 until its own move is reconciled. A row of another message
// already holding (folder, uid) — the folder was synchronised before the
// move was reconciled — is dropped in favour of this one so the local id
// stays stable. ErrNotFound when neither the row nor a blocked operation
// exists.
func (s *Store) AssignUID(ctx context.Context, id string, uid uint32, modseq uint64, flags []api.Flag) error {
	if uid == 0 {
		return fmt.Errorf("assign uid: uid 0")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assign uid: %w", err)
	}
	defer tx.Rollback()

	var accountID, rowFolder string
	rowFound := true
	err = tx.QueryRowContext(ctx, `SELECT account_id, folder_id FROM messages WHERE id = ?`, id).Scan(&accountID, &rowFolder)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		rowFound = false
	case err != nil:
		return fmt.Errorf("assign uid: %w", err)
	}
	var opFolder string
	err = tx.QueryRowContext(ctx, `SELECT folder_id FROM message_ops WHERE message_id = ? AND uid = 0 ORDER BY id LIMIT 1`, id).Scan(&opFolder)
	opFound := true
	switch {
	case errors.Is(err, sql.ErrNoRows):
		opFound = false
	case err != nil:
		return fmt.Errorf("assign uid: %w", err)
	}
	if !rowFound && !opFound {
		return ErrNotFound
	}
	target := rowFolder
	if opFound {
		target = opFolder
	}

	var files []messageFile
	if rowFound && rowFolder == target {
		dup, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE folder_id = ? AND uid = ? AND id != ?`,
			target, int64(uid), id)
		if err != nil {
			return err
		}
		if err := deleteMessageRowsTx(ctx, tx, dup); err != nil {
			return err
		}
		files = dup
		encoded, unread, flagged := encodeFlags(flags)
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET uid = ?, modseq = ?, flags = ?, unread = ?, flagged = ?, updated_at = ? WHERE id = ?`,
			int64(uid), int64(modseq), encoded, unread, flagged, nowStamp(), id); err != nil {
			return fmt.Errorf("assign uid: %w", err)
		}
	}
	if opFound {
		if _, err := tx.ExecContext(ctx, `UPDATE message_ops SET uid = ? WHERE message_id = ? AND uid = 0 AND folder_id = ?`,
			int64(uid), id, target); err != nil {
			return fmt.Errorf("assign uid to operations: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assign uid: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// DeleteStalePending removes rows of the folder that still have UID 0 and
// no remote id, were last touched before the given time and have no
// pending operation left (their move was pushed but never reconciled, so
// the server copy has shown up under another local id). It returns the
// number of rows removed; raw files go after the commit. The outbox
// pseudo-folder is never touched (its rows all have UID 0 by design): 0 is
// returned for it.
func (s *Store) DeleteStalePending(ctx context.Context, folderID string, before time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("delete stale pending: %w", err)
	}
	defer tx.Rollback()

	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM folders WHERE id = ?`, folderID).Scan(&role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("delete stale pending: %w", err)
	case role == string(api.RoleOutbox):
		return 0, nil
	}
	files, err := listMessageFiles(ctx, tx, `
		SELECT account_id, id FROM messages m
		WHERE folder_id = ? AND uid = 0 AND remote_id = '' AND updated_at < ?
		  AND NOT EXISTS (SELECT 1 FROM message_ops o WHERE o.message_id = m.id)`, folderID, stamp(before))
	if err != nil {
		return 0, err
	}
	if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("delete stale pending: %w", err)
	}
	s.removeMessageFiles(files)
	return len(files), nil
}

// SetMessageBody stores the parse result of a fetched message; see
// BodyUpdate for which fields replace and which only fill in. The message
// is then linked into its conversation again, since the body may be the
// first to carry References. ErrNotFound for an unknown id.
func (s *Store) SetMessageBody(ctx context.Context, id string, u BodyUpdate) error {
	state := u.State
	if state == "" {
		state = BodyFetched
	}
	attachments, err := encodeJSON(u.Attachments, "[]")
	if err != nil {
		return fmt.Errorf("encode attachments: %w", err)
	}
	headers, err := encodeJSON(u.Headers, "{}")
	if err != nil {
		return fmt.Errorf("encode headers: %w", err)
	}
	references, err := encodeJSON(u.References, "[]")
	if err != nil {
		return fmt.Errorf("encode references: %w", err)
	}
	from, err := encodeJSON(u.From, "[]")
	if err != nil {
		return fmt.Errorf("encode from: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set message body: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE messages SET
			text_body = ?, has_html = ?, snippet = ?, attachments_json = ?, has_attachments = ?,
			headers_json = ?, references_json = ?, body_state = ?,
			subject        = CASE WHEN subject = '' THEN ? ELSE subject END,
			from_json      = CASE WHEN from_json = '[]' THEN ? ELSE from_json END,
			date           = CASE WHEN date = ? THEN ? ELSE date END,
			rfc_message_id = CASE WHEN rfc_message_id = '' THEN ? ELSE rfc_message_id END,
			in_reply_to    = CASE WHEN in_reply_to = '' THEN ? ELSE in_reply_to END,
			size           = CASE WHEN ? > 0 THEN ? ELSE size END,
			updated_at = ?
		WHERE id = ?`,
		u.Text, boolInt(u.HasHTML), u.Snippet, attachments, boolInt(u.HasAttachments),
		headers, references, string(state),
		u.Subject, from, zeroStamp, stamp(u.Date), u.RFCMessageID, u.InReplyTo, u.Size, u.Size, nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set message body: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := relinkTx(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set message body: %w", err)
	}
	return nil
}

// MarkBodyState records why a body is (not) available without touching
// the content columns. ErrNotFound for an unknown id.
func (s *Store) MarkBodyState(ctx context.Context, id string, state BodyState) error {
	res, err := s.db.ExecContext(ctx, `UPDATE messages SET body_state = ?, updated_at = ? WHERE id = ?`,
		string(state), nowStamp(), id)
	if err != nil {
		return fmt.Errorf("mark body state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListMessages pages through a folder. The cursor is opaque and bound to
// the sort order it was issued for: a cursor from the other order (or a
// malformed one) is ErrBadCursor. It does not encode the filter, so a
// caller that changes the filter must start again from the first page.
// sort "" means SortDateDesc; filter "" means api.FilterAll; limit <= 0
// means 50. total counts the whole folder under the filter.
// ErrNotFound when the account has no such folder.
func (s *Store) ListMessages(ctx context.Context, accountID, folderID, cursor string, limit int, sortOrder api.SortOrder, listFilter api.MessageFilter) (items []Message, next string, total int, err error) {
	if limit <= 0 {
		limit = 50
	}
	var prefix, cmp, order string
	switch sortOrder {
	case "", api.SortDateDesc:
		prefix, cmp, order = "d", "<", "DESC"
	case api.SortDateAsc:
		prefix, cmp, order = "a", ">", "ASC"
	default:
		return nil, "", 0, fmt.Errorf("list messages: unsupported sort %q", sortOrder)
	}
	var cursorStamp, cursorID string
	if cursor != "" {
		if cursorStamp, cursorID, err = decodeSortCursor(cursor, prefix); err != nil {
			return nil, "", 0, err
		}
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE id = ? AND account_id = ?`, folderID, accountID).Scan(&exists); err != nil {
		return nil, "", 0, fmt.Errorf("list messages: %w", err)
	}
	if exists == 0 {
		return nil, "", 0, ErrNotFound
	}

	where := ` WHERE folder_id = ? AND account_id = ?`
	args := []any{folderID, accountID}
	switch listFilter {
	case "", api.FilterAll:
	case api.FilterUnread:
		where += ` AND unread = 1`
	case api.FilterFlagged:
		where += ` AND flagged = 1`
	default:
		return nil, "", 0, fmt.Errorf("list messages: unsupported filter %q", listFilter)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`+where, args...).Scan(&total); err != nil {
		return nil, "", 0, fmt.Errorf("count messages: %w", err)
	}

	query := `SELECT ` + messageColumns + ` FROM messages` + where
	if cursor != "" {
		query += ` AND (date ` + cmp + ` ? OR (date = ? AND id ` + cmp + ` ?))`
		args = append(args, cursorStamp, cursorStamp, cursorID)
	}
	query += ` ORDER BY date ` + order + `, id ` + order + ` LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var stamps []string
	for rows.Next() {
		m, st, err := scanMessageStamp(rows)
		if err != nil {
			return nil, "", 0, fmt.Errorf("scan message: %w", err)
		}
		items = append(items, m)
		stamps = append(stamps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, fmt.Errorf("list messages: %w", err)
	}
	if len(items) > limit {
		items = items[:limit]
		next = encodeSortCursor(prefix, stamps[limit-1], items[limit-1].ID)
	}
	return items, next, total, nil
}

// GetMessage returns one message of the account; ErrNotFound otherwise
// (also for a message of another account).
func (s *Store) GetMessage(ctx context.Context, accountID, id string) (Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ? AND account_id = ?`, id, accountID)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message: %w", err)
	}
	return m, nil
}

// GetMessageText returns the plain-text body and its state. ErrNotFound
// for an unknown id or another account's message.
func (s *Store) GetMessageText(ctx context.Context, accountID, id string) (text string, hasHTML bool, state BodyState, err error) {
	var html int
	var st string
	err = s.db.QueryRowContext(ctx, `SELECT text_body, has_html, body_state FROM messages WHERE id = ? AND account_id = ?`,
		id, accountID).Scan(&text, &html, &st)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, "", ErrNotFound
	case err != nil:
		return "", false, "", fmt.Errorf("get message text: %w", err)
	}
	return text, html != 0, BodyState(st), nil
}

// MessageRawPath is the raw RFC 822 file of a message:
// MessageDir()/<accountID>/<id>.
func (s *Store) MessageRawPath(accountID, id string) string {
	return filepath.Join(s.MessageDir(), accountID, id)
}

// WriteMessageRaw stores the raw message read from r (at most limit bytes,
// otherwise ErrTooBig and nothing is kept) as a 0600 file, written to a
// temporary name and renamed into place; an existing file is replaced. It
// returns the number of bytes written.
func (s *Store) WriteMessageRaw(ctx context.Context, accountID, id string, r io.Reader, limit int64) (int64, error) {
	return s.WriteMessageRawFunc(ctx, accountID, id, limit, func(w io.Writer) error {
		_, err := io.Copy(w, r)
		return err
	})
}

// WriteMessageRawFunc is WriteMessageRaw for a producer: fn streams the
// raw message into the writer it is given. The writer refuses the first
// byte past limit with ErrTooBig, so a runaway builder stops early; the
// result is then ErrTooBig (whatever fn returned) and nothing is kept. Any
// other error from fn is returned wrapped, again with nothing kept.
func (s *Store) WriteMessageRawFunc(ctx context.Context, accountID, id string, limit int64, fn func(w io.Writer) error) (int64, error) {
	if err := checkPathSegment(accountID); err != nil {
		return 0, fmt.Errorf("write message file: %w", err)
	}
	if err := checkPathSegment(id); err != nil {
		return 0, fmt.Errorf("write message file: %w", err)
	}
	if fn == nil {
		return 0, fmt.Errorf("write message file: nil producer")
	}
	final := s.MessageRawPath(accountID, id)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return 0, fmt.Errorf("create message directory: %w", err)
	}
	tmp := final + ".tmp"
	// A crashed earlier attempt may have left the temporary file behind;
	// O_EXCL then only guards against a concurrent writer.
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create message file: %w", err)
	}
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	lw := &limitedWriter{w: f, limit: limit}
	err = fn(lw)
	switch {
	case lw.exceeded || errors.Is(err, ErrTooBig):
		// The limit is checked first: a producer that swallows the write
		// error must not be able to store a truncated message.
		cleanup()
		return 0, ErrTooBig
	case err != nil:
		cleanup()
		return 0, fmt.Errorf("write message: %w", err)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return 0, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("close message file: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("finalise message file: %w", err)
	}
	return lw.n, nil
}

// limitedWriter counts what it passes on and fails with ErrTooBig as soon
// as a write would take the total past limit (nothing of that write is
// stored). exceeded stays set even if the producer ignores the error.
type limitedWriter struct {
	w        io.Writer
	n, limit int64
	exceeded bool
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.exceeded || l.n+int64(len(p)) > l.limit {
		l.exceeded = true
		return 0, ErrTooBig
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}

// OpenMessageRaw opens the raw file of a message for reading; ErrNotFound
// when there is none.
func (s *Store) OpenMessageRaw(ctx context.Context, accountID, id string) (*os.File, error) {
	if err := checkPathSegment(accountID); err != nil {
		return nil, fmt.Errorf("open message file: %w", err)
	}
	if err := checkPathSegment(id); err != nil {
		return nil, fmt.Errorf("open message file: %w", err)
	}
	f, err := os.Open(s.MessageRawPath(accountID, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open message file: %w", err)
	}
	return f, nil
}

// checkPathSegment rejects ids that could escape their directory. Ids are
// generated here, account ids may come from config.toml.
func checkPathSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\x00") {
		return fmt.Errorf("invalid path segment %q", seg)
	}
	return nil
}

// messageFile locates one raw message file.
type messageFile struct{ accountID, id string }

func listMessageFiles(ctx context.Context, q querier, query string, args ...any) ([]messageFile, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []messageFile
	for rows.Next() {
		var mf messageFile
		if err := rows.Scan(&mf.accountID, &mf.id); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, mf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return out, nil
}

// deleteMessageRowsTx removes the rows and their pending operations.
func deleteMessageRowsTx(ctx context.Context, tx *sql.Tx, files []messageFile) error {
	ids := make([]string, len(files))
	for i, mf := range files {
		ids[i] = mf.id
	}
	for _, chunk := range chunkStrings(ids, 500) {
		in := inPlaceholders(len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_ops WHERE message_id IN (`+in+`)`, args...); err != nil {
			return fmt.Errorf("delete message operations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id IN (`+in+`)`, args...); err != nil {
			return fmt.Errorf("delete messages: %w", err)
		}
	}
	return nil
}

// removeMessageFiles unlinks raw files after their rows are gone; a missing
// file is not an error (the row is authoritative).
func (s *Store) removeMessageFiles(files []messageFile) {
	for _, mf := range files {
		if checkPathSegment(mf.accountID) != nil || checkPathSegment(mf.id) != nil {
			continue
		}
		if err := os.Remove(s.MessageRawPath(mf.accountID, mf.id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("remove message file", "id", mf.id, "err", err)
		}
	}
}

// removeMessageDir drops the whole raw-message directory of an account.
func (s *Store) removeMessageDir(accountID string) {
	if checkPathSegment(accountID) != nil {
		return
	}
	if err := os.RemoveAll(filepath.Join(s.MessageDir(), accountID)); err != nil {
		s.log.Warn("remove message directory", "account", accountID, "err", err)
	}
}

const messageColumns = `id, account_id, folder_id, uid, remote_id, modseq, flags,
	from_json, to_json, cc_json, bcc_json, reply_to_json, subject, date, internal_date,
	rfc_message_id, in_reply_to, references_json, size, snippet, has_attachments,
	attachments_json, headers_json, has_html, body_state, thread_id, created_at, updated_at`

func scanMessage(row scanner) (Message, error) {
	m, _, err := scanMessageStamp(row)
	return m, err
}

// scanMessageStamp also returns the raw date stamp, which the list cursor
// needs verbatim.
func scanMessageStamp(row scanner) (Message, string, error) {
	var m Message
	var uid, modseq int64
	var flags, from, to, cc, bcc, replyTo, date, internalDate, references, attachments, headers, state, created, updated string
	var hasAttachments, hasHTML int
	if err := row.Scan(&m.ID, &m.AccountID, &m.FolderID, &uid, &m.RemoteID, &modseq, &flags,
		&from, &to, &cc, &bcc, &replyTo, &m.Subject, &date, &internalDate,
		&m.RFCMessageID, &m.InReplyTo, &references, &m.Size, &m.Snippet, &hasAttachments,
		&attachments, &headers, &hasHTML, &state, &m.ThreadID, &created, &updated); err != nil {
		return Message{}, "", err
	}
	m.UID, m.ModSeq = uint32(uid), uint64(modseq)
	var err error
	if m.Flags, err = decodeFlags(flags); err != nil {
		return Message{}, "", fmt.Errorf("decode flags of %s: %w", m.ID, err)
	}
	for _, p := range []struct {
		raw string
		dst *[]api.Address
	}{{from, &m.From}, {to, &m.To}, {cc, &m.CC}, {bcc, &m.BCC}, {replyTo, &m.ReplyTo}} {
		if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
			return Message{}, "", fmt.Errorf("decode addresses of %s: %w", m.ID, err)
		}
	}
	if err := json.Unmarshal([]byte(references), &m.References); err != nil {
		return Message{}, "", fmt.Errorf("decode references of %s: %w", m.ID, err)
	}
	if err := json.Unmarshal([]byte(attachments), &m.Attachments); err != nil {
		return Message{}, "", fmt.Errorf("decode attachments of %s: %w", m.ID, err)
	}
	if err := json.Unmarshal([]byte(headers), &m.Headers); err != nil {
		return Message{}, "", fmt.Errorf("decode headers of %s: %w", m.ID, err)
	}
	m.Date, m.InternalDate = parseStamp(date), parseStamp(internalDate)
	m.HasAttachments, m.HasHTML = hasAttachments != 0, hasHTML != 0
	m.BodyState = BodyState(state)
	m.CreatedAt, m.UpdatedAt = parseStamp(created), parseStamp(updated)
	return m, date, nil
}

type encodedMessage struct {
	flags                            string
	unread, flagged                  int
	from, to, cc, bcc, replyTo       string
	references, attachments, headers string
}

func encodeMessage(m *Message) (encodedMessage, error) {
	var e encodedMessage
	var err error
	e.flags, e.unread, e.flagged = encodeFlags(m.Flags)
	for _, p := range []struct {
		src []api.Address
		dst *string
	}{{m.From, &e.from}, {m.To, &e.to}, {m.CC, &e.cc}, {m.BCC, &e.bcc}, {m.ReplyTo, &e.replyTo}} {
		if *p.dst, err = encodeJSON(p.src, "[]"); err != nil {
			return e, fmt.Errorf("encode addresses: %w", err)
		}
	}
	if e.references, err = encodeJSON(m.References, "[]"); err != nil {
		return e, fmt.Errorf("encode references: %w", err)
	}
	if e.attachments, err = encodeJSON(m.Attachments, "[]"); err != nil {
		return e, fmt.Errorf("encode attachments: %w", err)
	}
	if e.headers, err = encodeJSON(m.Headers, "{}"); err != nil {
		return e, fmt.Errorf("encode headers: %w", err)
	}
	return e, nil
}

// encodeJSON marshals v, substituting empty for a nil slice or map so the
// column never holds "null".
func encodeJSON(v any, empty string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(b) == "null" {
		return empty, nil
	}
	return string(b), nil
}

// encodeFlags stores flags as a sorted, de-duplicated JSON array and
// derives the unread column (1 when "seen" is absent) and the flagged
// column (1 when "flagged" is present). Both exist only so the filtered
// listings can use a partial index; the array stays the source of truth.
func encodeFlags(flags []api.Flag) (encoded string, unread, flagged int) {
	set := normalizeFlags(flags)
	b, _ := json.Marshal(set)
	unread = 1
	for _, f := range set {
		switch f {
		case api.FlagSeen:
			unread = 0
		case api.FlagFlagged:
			flagged = 1
		}
	}
	return string(b), unread, flagged
}

func decodeFlags(raw string) ([]api.Flag, error) {
	var flags []api.Flag
	if err := json.Unmarshal([]byte(raw), &flags); err != nil {
		return nil, err
	}
	if flags == nil {
		flags = []api.Flag{}
	}
	return flags, nil
}

// normalizeFlags returns the sorted set of non-empty flags (never nil).
func normalizeFlags(flags []api.Flag) []api.Flag {
	seen := make(map[api.Flag]bool, len(flags))
	out := make([]api.Flag, 0, len(flags))
	for _, f := range flags {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sameFlags(a, b []api.Flag) bool {
	na, nb := normalizeFlags(a), normalizeFlags(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// Sorted-list cursors carry the sort order they belong to:
// "<d|a>\x00<date stamp>\x00<id>", base64url without padding.
func encodeSortCursor(prefix, stamp, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(prefix + "\x00" + stamp + "\x00" + id))
}

func decodeSortCursor(c, prefix string) (stamp, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", ErrBadCursor
	}
	parts := strings.SplitN(string(raw), "\x00", 3)
	if len(parts) != 3 || parts[0] != prefix || parts[1] == "" || parts[2] == "" {
		return "", "", ErrBadCursor
	}
	if _, err := time.Parse(timeLayout, parts[1]); err != nil {
		return "", "", ErrBadCursor
	}
	return parts[1], parts[2], nil
}
