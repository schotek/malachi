// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// MaxDraftSyncAttempts is how often an upload of the same version is
// tried; after that the draft waits for its next save (SaveDraft clears
// the retry state) instead of re-sending a copy the server keeps refusing.
const MaxDraftSyncAttempts = 8

// DraftCopy locates a draft's copy in the Drafts folder (migration 0012):
// the folder, the UID under the folder's UIDVALIDITY (IMAP; 0 = not
// known) or the item's immutable id (Graph), and its Message-ID. The zero
// value is "no copy".
type DraftCopy struct {
	FolderID     string
	UIDValidity  uint32
	UID          uint32
	RemoteID     string
	RFCMessageID string
}

// IsZero reports that there is no copy.
func (c DraftCopy) IsZero() bool {
	return c.FolderID == "" && c.RFCMessageID == ""
}

// CopyOf is the DraftCopy of a stored message of folder f.
func CopyOf(m Message, f Folder) DraftCopy {
	c := DraftCopy{FolderID: m.FolderID, UID: m.UID, RemoteID: m.RemoteID, RFCMessageID: m.RFCMessageID}
	if m.UID != 0 {
		c.UIDValidity = f.UIDValidity
	}
	return c
}

// DraftUpload is one draft version built for its server copy (core
// builds it, the syncers store it): the raw RFC 5322 message under a fresh
// Message-ID and the version it holds.
type DraftUpload struct {
	DraftID      string
	Version      int
	RFCMessageID string
	Date         time.Time
	Raw          []byte
}

// DueDraftUploads lists the account's drafts whose newest version is not
// on the server yet and has rested for quiet (no save since), whose retry
// time has come and that have attempts left; the oldest first. A local
// draft is never due.
func (s *Store) DueDraftUploads(ctx context.Context, accountID string, now time.Time, quiet time.Duration, limit int) ([]Draft, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+draftColumns+` FROM drafts
		WHERE account_id = ? AND local = 0 AND synced_version < version AND updated_at <= ?
		  AND (sync_next_at = '' OR sync_next_at <= ?) AND sync_attempts < ?
		ORDER BY updated_at, id LIMIT ?`,
		accountID, stamp(now.Add(-quiet)), stamp(now), MaxDraftSyncAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("list due drafts: %w", err)
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due drafts: %w", err)
	}
	return out, nil
}

// NextDraftUpload is when the account's next upload falls due: the
// earliest, over the drafts waiting for one, of the later of its resting
// time and its retry time; zero when nothing waits. Local drafts never
// wait.
func (s *Store) NextDraftUpload(ctx context.Context, accountID string, quiet time.Duration) (time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT updated_at, sync_next_at FROM drafts
		WHERE account_id = ? AND local = 0 AND synced_version < version AND sync_attempts < ?`,
		accountID, MaxDraftSyncAttempts)
	if err != nil {
		return time.Time{}, fmt.Errorf("next draft upload: %w", err)
	}
	defer rows.Close()
	var next time.Time
	for rows.Next() {
		var updated, retry string
		if err := rows.Scan(&updated, &retry); err != nil {
			return time.Time{}, fmt.Errorf("next draft upload: %w", err)
		}
		due := parseStamp(updated).Add(quiet)
		if r := parseStamp(retry); r.After(due) {
			due = r
		}
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, fmt.Errorf("next draft upload: %w", err)
	}
	return next, nil
}

// MarkDraftSynced records c as the copy of version of the draft: the
// version is the synced one (never lowered: a later save stays due) and
// the copy it replaces is deleted in the same transaction (dropCopyTx),
// unless keepPrevious (it was changed on the server by someone else and
// both are kept). found is false when the draft is gone — sent or deleted
// while its upload ran — and c is then deleted instead, so no orphan copy
// is left behind. The same holds for a draft that became local while its
// upload ran (picked by DueDraftUploads before a board case linked it): c
// is deleted, the version is not marked synced, and found is false. A c
// that cannot be addressed yet goes to draft_stray_copies instead
// (strayCopyTx), for DropStrayDraftCopies.
func (s *Store) MarkDraftSynced(ctx context.Context, accountID, draftID string, version int, c DraftCopy, keepPrevious bool) (found bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	d, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ? AND account_id = ?`, draftID, accountID))
	var gone []messageFile
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && d.Local:
		// Gone, or made local (makeDraftLocalTx dealt with the copy it had
		// then; this one was in flight). The copy was stored by this upload
		// a moment ago: nobody can have changed it, no edit check.
		if gone, _, err = strayCopyTx(ctx, tx, accountID, c, time.Time{}, false); err != nil {
			return false, err
		}
	case err != nil:
		return false, fmt.Errorf("load draft: %w", err)
	default:
		found = true
		if !keepPrevious && d.Copy != c {
			if gone, err = dropCopyTx(ctx, tx, accountID, d.Copy); err != nil {
				return false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drafts SET rfc_message_id = ?, server_folder_id = ?,
				server_uidvalidity = ?, server_uid = ?, server_remote_id = ?,
				synced_version = MAX(synced_version, ?), synced_at = ?,
				sync_attempts = 0, sync_next_at = '', sync_error = ''
			WHERE id = ?`,
			c.RFCMessageID, c.FolderID, int64(c.UIDValidity), int64(c.UID), c.RemoteID,
			version, nowStamp(), draftID); err != nil {
			return false, fmt.Errorf("mark draft synced: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	s.removeMessageFiles(gone)
	return found, nil
}

// MarkDraftSyncFailed records a failed upload: one attempt more, the
// error and the time of the next try.
func (s *Store) MarkDraftSyncFailed(ctx context.Context, draftID, errText string, retryAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE drafts SET sync_attempts = sync_attempts + 1, sync_error = ?, sync_next_at = ?
		WHERE id = ?`, errText, stamp(retryAt), draftID)
	if err != nil {
		return fmt.Errorf("mark draft sync failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DraftForMessage is the draft whose copy m (a message of a Drafts
// folder) is, matched by Message-ID or by server identity; ErrNotFound
// when m belongs to no draft.
func (s *Store) DraftForMessage(ctx context.Context, accountID string, m Message) (Draft, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts
		WHERE account_id = ? AND (
			(rfc_message_id != '' AND rfc_message_id = ?)
			OR (server_folder_id = ? AND (
				(server_uid != 0 AND server_uid = ?
				 AND server_uidvalidity = (SELECT uidvalidity FROM folders WHERE id = ?))
				OR (server_remote_id != '' AND server_remote_id = ?))))
		ORDER BY updated_at DESC, id DESC LIMIT 1`,
		accountID, m.RFCMessageID, m.FolderID, int64(m.UID), m.FolderID, m.RemoteID)
	d, err := scanDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	if d.Attachments, err = attachmentsForDraft(ctx, s.db, d.ID); err != nil {
		return Draft{}, err
	}
	return d, nil
}

// MessageIDByRFC finds the stored message of the account with the given
// Message-ID outside the Drafts folders and the outbox (the parent of a
// reply draft); ErrNotFound when there is none.
func (s *Store) MessageIDByRFC(ctx context.Context, accountID, rfcID string) (string, error) {
	if rfcID == "" {
		return "", ErrNotFound
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT m.id FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? AND m.rfc_message_id = ? AND f.role NOT IN (?, ?)
		ORDER BY m.date DESC, m.id LIMIT 1`,
		accountID, rfcID, string(api.RoleDrafts), string(api.RoleOutbox)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find message: %w", err)
	}
	return id, nil
}

// dropCopyTx deletes a draft copy: every local row of a Drafts folder that
// is the copy (same Message-ID, or same server identity) goes the way of
// a permanent delete (deleteMessageTx: row now, delete operation queued),
// and when no row stands for the server identity — the copy was never
// synchronised down — a delete operation is queued for it directly. The
// folders are recounted; the caller unlinks the returned raw files after
// the commit.
func dropCopyTx(ctx context.Context, tx *sql.Tx, accountID string, c DraftCopy) ([]messageFile, error) {
	if c.IsZero() {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, m.folder_id, m.uid, m.remote_id, f.uidvalidity
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? AND f.role = ? AND (
			(? != '' AND m.rfc_message_id = ?)
			OR (m.folder_id = ? AND ((? != 0 AND m.uid = ? AND f.uidvalidity = ?)
			                         OR (? != '' AND m.remote_id = ?))))`,
		accountID, string(api.RoleDrafts),
		c.RFCMessageID, c.RFCMessageID,
		c.FolderID, int64(c.UID), int64(c.UID), int64(c.UIDValidity),
		c.RemoteID, c.RemoteID)
	if err != nil {
		return nil, fmt.Errorf("find draft copies: %w", err)
	}
	type row struct {
		loc         messageLoc
		uidValidity uint32
	}
	var found []row
	for rows.Next() {
		var r row
		var uid, uidValidity int64
		if err := rows.Scan(&r.loc.id, &r.loc.folderID, &uid, &r.loc.remoteID, &uidValidity); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan draft copy: %w", err)
		}
		r.loc.uid, r.uidValidity = uint32(uid), uint32(uidValidity)
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find draft copies: %w", err)
	}

	now := nowStamp()
	folders := map[string]bool{}
	var files []messageFile
	covered := false
	for _, r := range found {
		if r.loc.folderID == c.FolderID &&
			((c.UID != 0 && r.loc.uid == c.UID && r.uidValidity == c.UIDValidity) ||
				(c.RemoteID != "" && r.loc.remoteID == c.RemoteID)) {
			covered = true
		}
		if r.loc.uid == 0 && r.loc.remoteID == "" {
			// No server identity yet: nothing to address, the row just goes.
			if err := deleteMessageRowsTx(ctx, tx, []messageFile{{accountID: accountID, id: r.loc.id}}); err != nil {
				return nil, err
			}
		} else if err := deleteMessageTx(ctx, tx, accountID, r.loc, now); err != nil {
			return nil, err
		}
		folders[r.loc.folderID] = true
		files = append(files, messageFile{accountID: accountID, id: r.loc.id})
	}
	if !covered && (c.UID != 0 || c.RemoteID != "") {
		var uidValidity int64
		err := tx.QueryRowContext(ctx, `SELECT uidvalidity FROM folders WHERE id = ? AND account_id = ?`,
			c.FolderID, accountID).Scan(&uidValidity)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// The folder is gone, and the copy with it.
		case err != nil:
			return nil, fmt.Errorf("lookup drafts folder: %w", err)
		case c.RemoteID != "" || uint32(uidValidity) == c.UIDValidity:
			loc := messageLoc{id: newID("dc_"), folderID: c.FolderID, uid: c.UID, remoteID: c.RemoteID}
			if err := enqueueOp(ctx, tx, accountID, OpDelete, loc, "{}", now); err != nil {
				return nil, err
			}
		}
	}
	if err := recountFoldersTx(ctx, tx, folders); err != nil {
		return nil, err
	}
	return files, nil
}

// adoptCopyTx links draft draftID to copy c (draft.save with replaces).
// Another draft that holds c is deleted when everything it has is
// uploaded — its attachments released, not deleted, in case a compose
// window still lists them — and when it has changes to upload the
// adoption is refused (false) so that nothing is lost. The draft's own
// previous copy, superseded by c, is deleted; the caller unlinks the
// returned raw files after the commit.
func adoptCopyTx(ctx context.Context, tx *sql.Tx, accountID, draftID string, c DraftCopy) (bool, []messageFile, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, version, synced_version FROM drafts
		WHERE account_id = ? AND id != ? AND (
			(rfc_message_id != '' AND rfc_message_id = ?)
			OR (server_folder_id = ? AND ((server_uid != 0 AND server_uid = ? AND server_uidvalidity = ?)
			                              OR (server_remote_id != '' AND server_remote_id = ?))))`,
		accountID, draftID, c.RFCMessageID, c.FolderID, int64(c.UID), int64(c.UIDValidity), c.RemoteID)
	if err != nil {
		return false, nil, fmt.Errorf("find drafts of copy: %w", err)
	}
	var holders []string
	pending := false
	for rows.Next() {
		var id string
		var version, synced int
		if err := rows.Scan(&id, &version, &synced); err != nil {
			rows.Close()
			return false, nil, fmt.Errorf("scan draft: %w", err)
		}
		if synced < version {
			pending = true
		}
		holders = append(holders, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("find drafts of copy: %w", err)
	}
	if pending {
		return false, nil, nil
	}
	for _, id := range holders {
		if err := dropDraftKeepAttachmentsTx(ctx, tx, id); err != nil {
			return false, nil, err
		}
	}

	prev, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ?`, draftID))
	if err != nil {
		return false, nil, fmt.Errorf("load draft: %w", err)
	}
	var files []messageFile
	if !prev.Copy.IsZero() && prev.Copy != c {
		// A copy with c's Message-ID is c's own, not an older one.
		old := prev.Copy
		if old.RFCMessageID == c.RFCMessageID {
			old.RFCMessageID = ""
		}
		if files, err = dropCopyTx(ctx, tx, accountID, old); err != nil {
			return false, nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drafts SET rfc_message_id = ?, server_folder_id = ?,
			server_uidvalidity = ?, server_uid = ?, server_remote_id = ?
		WHERE id = ?`,
		c.RFCMessageID, c.FolderID, int64(c.UIDValidity), int64(c.UID), c.RemoteID, draftID); err != nil {
		return false, nil, fmt.Errorf("adopt draft copy: %w", err)
	}
	return true, files, nil
}

// makeDraftLocalTx marks the account's draft local (no syncer uploads it
// again) and hands the copy it has in the Drafts folder, if any, to
// strayCopyTx: deleted now when it can be addressed and nobody but this
// store can have changed it (IMAP), else recorded in draft_stray_copies —
// a Graph copy waits there for its syncer's edit check, so a copy Outlook
// changed in place is not deleted. A draft that was ordinary until now is
// marked edited as well: the daemon cannot tell who wrote it, so should
// the draft lose its case it becomes an ordinary draft again rather than
// being deleted (releaseLinkedDraftTx). wake says the account's syncer has
// work (a queued delete or a stray copy); the caller unlinks the returned
// raw files after the commit. An upload already under way is dealt with by
// MarkDraftSynced. An unknown draft is not an error (found false); a draft
// already local changes nothing.
func makeDraftLocalTx(ctx context.Context, tx *sql.Tx, accountID, draftID string) (found, wake bool, files []messageFile, err error) {
	d, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ? AND account_id = ?`, draftID, accountID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, nil, nil
	case err != nil:
		return false, false, nil, fmt.Errorf("load draft: %w", err)
	}
	if d.Local {
		return true, false, nil, nil
	}
	if files, wake, err = strayCopyTx(ctx, tx, accountID, d.Copy, d.SyncedAt, true); err != nil {
		return true, false, nil, err
	}
	// synced_version goes back to 0: should the draft become an ordinary
	// one again, it is uploaded anew (its copy is gone or not its own).
	if _, err := tx.ExecContext(ctx, `UPDATE drafts SET local = 1, edited = 1, rfc_message_id = '', server_folder_id = '',
			server_uidvalidity = 0, server_uid = 0, server_remote_id = '', synced_version = 0,
			sync_attempts = 0, sync_next_at = '', sync_error = ''
		WHERE id = ? AND account_id = ?`, draftID, accountID); err != nil {
		return true, false, nil, fmt.Errorf("make draft local: %w", err)
	}
	return true, wake, files, nil
}

// strayCopyTx deals with copy c of the Drafts folder that no draft holds
// any more: when it can be addressed (draftCopyPendingTx) and no edit
// check is asked for it — or it has no immutable id to ask about — it is
// deleted now through dropCopyTx; otherwise it is recorded in
// draft_stray_copies for DropStrayDraftCopies (syncedAt: when this store
// stored it, for the Graph syncer's edit check). wake: the account's
// syncer has work. The caller unlinks the returned raw files after the
// commit.
func strayCopyTx(ctx context.Context, tx *sql.Tx, accountID string, c DraftCopy, syncedAt time.Time, checkEdit bool) ([]messageFile, bool, error) {
	if c.IsZero() {
		return nil, false, nil
	}
	pending, err := draftCopyPendingTx(ctx, tx, accountID, c)
	if err != nil {
		return nil, false, err
	}
	if !pending && !(checkEdit && c.RemoteID != "") {
		files, err := dropCopyTx(ctx, tx, accountID, c)
		return files, true, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO draft_stray_copies (account_id, folder_id, uidvalidity, uid, remote_id,
			rfc_message_id, synced_at, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, c.FolderID, int64(c.UIDValidity), int64(c.UID), c.RemoteID, c.RFCMessageID, optStamp(syncedAt), nowStamp()); err != nil {
		return nil, false, fmt.Errorf("record a stray draft copy: %w", err)
	}
	return nil, true, nil
}

// StrayDraftCopyKeep is how long DropStrayDraftCopies keeps trying an
// entry of draft_stray_copies it cannot resolve (a copy whose row never
// arrives, a UID under a UIDVALIDITY the folder no longer has, a Graph
// copy whose syncer does not run) before it forgets it with a log line:
// the copy then stays in the Drafts folder, where the user sees it.
const StrayDraftCopyKeep = 7 * 24 * time.Hour

// StrayDraftCopy is an entry of draft_stray_copies.
type StrayDraftCopy struct {
	ID         int64
	AccountID  string
	Copy       DraftCopy
	SyncedAt   time.Time // when this store stored the copy; zero = unknown
	RecordedAt time.Time
}

// CopyEditedFunc says whether a Graph copy was changed on the server after
// it was stored (c.SyncedAt, never zero here); an error ends the round.
type CopyEditedFunc func(ctx context.Context, c StrayDraftCopy) (bool, error)

// DropStrayDraftCopies works through the account's ("" = every account)
// entries of draft_stray_copies: a copy that can be addressed now is
// deleted through dropCopyTx and its entry goes; one still pending stays,
// until StrayDraftCopyKeep after it was recorded, when it is forgotten
// with a warning. A Graph copy (immutable id) needs edited, the account's
// syncer's question to the server: without it (the board's upkeep) the
// entry waits; one changed on the server since it was stored — or stored
// at an unknown time — is the user's own draft now and is forgotten (it
// stays in the Drafts folder), one not changed is deleted. The syncers
// call it at the start of every pass, before they push the operation log;
// the board's upkeep hourly. Returns the accounts with queued deletions,
// sorted; an error (storage, or edited's) ends the round, and what was
// done stays done.
func (s *Store) DropStrayDraftCopies(ctx context.Context, accountID string, edited CopyEditedFunc, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, folder_id, uidvalidity, uid, remote_id, rfc_message_id,
			synced_at, recorded_at
		FROM draft_stray_copies WHERE ? = '' OR account_id = ? ORDER BY id`, accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("stray draft copies: %w", err)
	}
	var list []StrayDraftCopy
	for rows.Next() {
		var e StrayDraftCopy
		var uidValidity, uid int64
		var synced, recorded string
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Copy.FolderID, &uidValidity, &uid, &e.Copy.RemoteID,
			&e.Copy.RFCMessageID, &synced, &recorded); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stray draft copies: %w", err)
		}
		e.Copy.UIDValidity, e.Copy.UID = uint32(uidValidity), uint32(uid)
		e.SyncedAt, e.RecordedAt = parseStamp(synced), parseStamp(recorded)
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stray draft copies: %w", err)
	}
	accounts := map[string]bool{}
	for _, e := range list {
		expired := now.Sub(e.RecordedAt) > StrayDraftCopyKeep
		drop, forget := true, false
		if e.Copy.RemoteID != "" {
			switch {
			case edited == nil:
				drop, forget = false, expired
				if expired {
					s.log.Warn("gave up deleting a draft copy: its account's syncer did not check it; it stays in the Drafts folder",
						"account", e.AccountID, "recorded", e.RecordedAt)
				}
			case e.SyncedAt.IsZero():
				drop, forget = false, true
				s.log.Info("draft copy of unknown age left in the Drafts folder", "account", e.AccountID)
			default:
				changed, err := edited(ctx, e)
				if err != nil {
					return sortedKeys(accounts), err
				}
				if changed {
					drop, forget = false, true
					s.log.Info("draft copy changed on the server since it was stored, left in the Drafts folder as the user's own",
						"account", e.AccountID)
				}
			}
		}
		queued, err := s.resolveStrayCopy(ctx, e, drop, forget || expired)
		if err != nil {
			return sortedKeys(accounts), err
		}
		if queued {
			accounts[e.AccountID] = true
		}
	}
	return sortedKeys(accounts), nil
}

// resolveStrayCopy does one entry's part of DropStrayDraftCopies in its own
// transaction: with drop, a copy that can be addressed is deleted and the
// entry goes; with forget (or a pending copy past its time) the entry goes
// and the copy stays. queued: a delete was queued.
func (s *Store) resolveStrayCopy(ctx context.Context, e StrayDraftCopy, drop, forget bool) (queued bool, err error) {
	if !drop && !forget {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	var files []messageFile
	done := forget && !drop
	if drop {
		pending, err := draftCopyPendingTx(ctx, tx, e.AccountID, e.Copy)
		if err != nil {
			return false, err
		}
		switch {
		case !pending:
			if files, err = dropCopyTx(ctx, tx, e.AccountID, e.Copy); err != nil {
				return false, err
			}
			done, queued = true, true
		case forget:
			done = true
			s.log.Warn("gave up deleting a draft copy that never became addressable; it stays in the Drafts folder",
				"account", e.AccountID, "recorded", e.RecordedAt)
		}
	}
	if !done {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM draft_stray_copies WHERE id = ?`, e.ID); err != nil {
		return false, fmt.Errorf("forget a stray draft copy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	s.removeMessageFiles(files)
	return queued, nil
}

// draftCopyPendingTx says whether dropCopyTx could not address copy c yet:
// no local row of a Drafts folder is the copy, it has no immutable id
// (Graph), and its UID is unknown or under another UIDVALIDITY than the
// folder's (the folder's pass has not run since the APPEND). A copy whose
// folder is gone is not pending: it went with the folder.
func draftCopyPendingTx(ctx context.Context, tx *sql.Tx, accountID string, c DraftCopy) (bool, error) {
	if c.IsZero() || c.RemoteID != "" {
		return false, nil
	}
	var rows int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? AND f.role = ? AND (
			(? != '' AND m.rfc_message_id = ?)
			OR (m.folder_id = ? AND ? != 0 AND m.uid = ? AND f.uidvalidity = ?))`,
		accountID, string(api.RoleDrafts), c.RFCMessageID, c.RFCMessageID,
		c.FolderID, int64(c.UID), int64(c.UID), int64(c.UIDValidity)).Scan(&rows); err != nil {
		return false, fmt.Errorf("find draft copies: %w", err)
	}
	if rows > 0 {
		return false, nil
	}
	var uidValidity int64
	err := tx.QueryRowContext(ctx, `SELECT uidvalidity FROM folders WHERE id = ? AND account_id = ?`, c.FolderID, accountID).Scan(&uidValidity)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("lookup drafts folder: %w", err)
	}
	return c.UID == 0 || uint32(uidValidity) != c.UIDValidity, nil
}

// LinkedDraftRelease is what releaseLinkedDraftTx did with a draft.
type LinkedDraftRelease string

const (
	// ReleaseNone: the draft is gone, or not local.
	ReleaseNone LinkedDraftRelease = ""
	// ReleaseOrdinary: an edited draft became an ordinary draft (local =
	// 0): it uploads to the Drafts folder like any draft.
	ReleaseOrdinary LinkedDraftRelease = "ordinary"
	// ReleaseDeleted: an untouched suggestion was deleted.
	ReleaseDeleted LinkedDraftRelease = "deleted"
)

// releaseLinkedDraftTx is the one rule for a linked draft that loses its
// case without Send or Discard (a thread merge, the prune of an orphaned
// or long-done case, the removal of its account with its local data kept):
// the caller drops the link in the same transaction. A draft the user may
// have written in (edited) becomes an ordinary draft — never destroyed,
// never left invisible; an untouched suggestion is deleted, and so never
// reaches the server. A local draft records no copy (makeDraftLocalTx), so
// there is none to delete. With keepFiles its attachments are released
// for SweepAttachments (a caller deep in another write cannot unlink
// files after its commit); without, they go with the draft and their ids
// are returned for removeAttachmentFiles after the commit.
func releaseLinkedDraftTx(ctx context.Context, tx *sql.Tx, accountID, draftID string, keepFiles bool) (LinkedDraftRelease, []string, error) {
	if draftID == "" {
		return ReleaseNone, nil, nil
	}
	var local, edited bool
	err := tx.QueryRowContext(ctx, `SELECT local, edited FROM drafts WHERE id = ? AND account_id = ?`, draftID, accountID).Scan(&local, &edited)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ReleaseNone, nil, nil
	case err != nil:
		return ReleaseNone, nil, fmt.Errorf("release draft: %w", err)
	case !local:
		return ReleaseNone, nil, nil
	case edited:
		if _, err := tx.ExecContext(ctx, `UPDATE drafts SET local = 0 WHERE id = ? AND account_id = ?`, draftID, accountID); err != nil {
			return ReleaseNone, nil, fmt.Errorf("release draft: %w", err)
		}
		return ReleaseOrdinary, nil, nil
	case keepFiles:
		if err := dropDraftKeepAttachmentsTx(ctx, tx, draftID); err != nil {
			return ReleaseNone, nil, err
		}
		return ReleaseDeleted, nil, nil
	}
	files, err := draftAttachmentIDs(ctx, tx, accountID, draftID)
	if err != nil {
		return ReleaseNone, nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE id = ? AND account_id = ?`, draftID, accountID); err != nil {
		return ReleaseNone, nil, fmt.Errorf("delete draft: %w", err)
	}
	return ReleaseDeleted, files, nil
}

// ReleaseEditedLocalDrafts makes every edited local draft that no case
// links an ordinary draft (releaseLinkedDraftTx does it when the link
// goes, so this finds nothing unless a path was missed) and returns the
// accounts concerned, sorted. The board's upkeep calls it before the
// sweep, which never takes an edited draft.
func (s *Store) ReleaseEditedLocalDrafts(ctx context.Context) ([]string, error) {
	accounts, err := stringColumn(ctx, s.db, `UPDATE drafts SET local = 0
		WHERE local = 1 AND edited = 1
			AND NOT EXISTS (SELECT 1 FROM board_cases c WHERE c.draft_id = drafts.id AND c.account_id = drafts.account_id)
		RETURNING account_id`)
	if err != nil {
		return nil, fmt.Errorf("release edited local drafts: %w", err)
	}
	return distinctSorted(accounts), nil
}

// UnlinkedLocalDrafts lists local drafts that no board case links, that
// were never edited and that were last saved before before (a triage
// run's leftover, a Suggest Reply whose link never came), of accounts that
// still exist, oldest first, at most limit (0 = 100), as (account id,
// draft id) pairs. Nothing else shows them: they are not in the Drafts
// folder. A draft that lost its case was dealt with then
// (releaseLinkedDraftTx), and an edited one is never listed.
func (s *Store) UnlinkedLocalDrafts(ctx context.Context, before time.Time, limit int) ([][2]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.account_id, d.id FROM drafts d
		WHERE d.local = 1 AND d.edited = 0 AND d.updated_at < ?
			AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = d.account_id)
			AND NOT EXISTS (SELECT 1 FROM board_cases c WHERE c.draft_id = d.id AND c.account_id = d.account_id)
		ORDER BY d.updated_at, d.id LIMIT ?`, stamp(before), limit)
	if err != nil {
		return nil, fmt.Errorf("unlinked local drafts: %w", err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, fmt.Errorf("unlinked local drafts: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unlinked local drafts: %w", err)
	}
	return out, nil
}

// dropDraftsOfMessagesTx deletes the drafts whose copies the given rows of
// a Drafts folder are, before the user's trash, move or delete takes the
// rows away: a draft left behind would upload itself again. Their
// attachments are released rather than deleted, so a compose window that
// still has the draft open saves it as a new one with its files. Returns
// the ids of the drafts it deleted, each once.
func dropDraftsOfMessagesTx(ctx context.Context, tx *sql.Tx, accountID string, locs []messageLoc) ([]string, error) {
	var dropped []string
	for _, loc := range locs {
		rows, err := tx.QueryContext(ctx, `
			SELECT d.id FROM messages m
			JOIN folders f ON f.id = m.folder_id
			JOIN drafts d ON d.account_id = m.account_id AND (
				(d.rfc_message_id != '' AND d.rfc_message_id = m.rfc_message_id)
				OR (d.server_folder_id = m.folder_id AND (
					(d.server_uid != 0 AND d.server_uid = m.uid AND d.server_uidvalidity = f.uidvalidity)
					OR (d.server_remote_id != '' AND d.server_remote_id = m.remote_id))))
			WHERE m.id = ? AND m.account_id = ? AND f.role = ?`,
			loc.id, accountID, string(api.RoleDrafts))
		if err != nil {
			return nil, fmt.Errorf("find drafts of message: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan draft: %w", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("find drafts of message: %w", err)
		}
		for _, id := range ids {
			if slices.Contains(dropped, id) {
				continue // two copies of one draft
			}
			if err := dropDraftKeepAttachmentsTx(ctx, tx, id); err != nil {
				return nil, err
			}
			dropped = append(dropped, id)
		}
	}
	return dropped, nil
}

// dropDraftKeepAttachmentsTx deletes a draft row after releasing its
// attachments (the orphan sweep takes them after a day).
func dropDraftKeepAttachmentsTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE attachments SET draft_id = NULL, position = 0 WHERE draft_id = ?`, id); err != nil {
		return fmt.Errorf("release attachments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	return nil
}

// recountFoldersTx recounts the given folders in a stable order.
func recountFoldersTx(ctx context.Context, tx *sql.Tx, folders map[string]bool) error {
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
	return nil
}
