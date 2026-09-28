// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The partial state of a stored message (migration 0014). A message whose
// large attachments stay on the mail server is stored as a skeleton of
// itself, those parts' bodies left empty (mime.Skeleton), and its row names
// them in remote_parts until message.download makes it whole again. What
// to leave on the server is internal/ingest's decision; this file keeps the
// row and the file in step, and finds the messages the background pass may
// reduce.

// The values of strippable_bytes (Message.StrippableBytes, RawCommit) that
// are not a size; 0 is "nothing the rule the message was judged by could
// leave on the server", more what it could.
const (
	// StrippableUnknown: not evaluated yet.
	StrippableUnknown int64 = -1
	// StrippableNever: never to be reduced, whatever the rule: its parts
	// cannot be left on the server safely (a signed or encrypted message,
	// a doubtful MIME structure, a skeleton that does not verify, a file
	// that cannot be read). A download that stores it again judges it
	// anew.
	StrippableNever int64 = -2
)

// RawState says whether a stored message's raw file is complete.
type RawState string

const (
	RawFull    RawState = "full"    // the file is the message as received
	RawPartial RawState = "partial" // the parts in remote_parts have empty bodies
)

// RawExpect is the state CommitMessageRaw expects the message's row to be
// in; a zero field matches any value.
type RawExpect struct {
	BodyState BodyState
	RawState  RawState
	// HydratedAt: nil matches anything, the zero time a message never
	// made whole on demand.
	HydratedAt *time.Time
}

// RawCommit is a new raw file for a message and what its row is to say
// about it (CommitMessageRaw).
type RawCommit struct {
	// Source is the new file (StageRaw, or StageMemory). Committing a
	// plain one staged on disk renames it into place; the caller still
	// calls its Remove.
	Source *Staged
	// RemoteParts are the ids of the parts whose bodies Source leaves out
	// and RemoteBytes their decoded size: both empty for a whole message,
	// both set otherwise.
	RemoteParts []string
	RemoteBytes int64
	// StrippableBytes is stored as given: StrippableUnknown,
	// StrippableNever, 0 nothing to leave on the server, more what could
	// be.
	StrippableBytes int64
	// Body, when set, is the parse of the whole message and is stored as
	// SetMessageBody stores it (a download); nil leaves the body columns
	// alone (a message reduced in place).
	Body *BodyUpdate
	// Hydrated marks the commit as an on-demand download: hydrated_at
	// becomes now.
	Hydrated bool
	Expect   RawExpect
}

// CommitMessageRaw replaces the message's raw file with c.Source and brings
// its row in step, under the message's write lock, so that wherever a crash
// interrupts it the row never claims a part is stored that the file lacks:
//  1. the row is checked against c.Expect (ErrConflict; no row:
//     ErrNotFound; an outbox message: ErrOutbox; a message in Drafts, which
//     keeps all its parts, with c.RemoteParts: ErrConflict);
//  2. phase A: when the new file leaves out a part the row calls stored,
//     the row first names the union of the old and new remote sets, and
//     when the file replaces a stored one that commit is flushed to disk
//     first (beginDurable): the file's replacement is, and must not
//     survive a power loss that the row's change does not;
//  3. the file is replaced (RawTx.Replace);
//  4. phase B: the row gets the final remote set, the other columns of c
//     and, with c.Body, the body columns and the conversation link.
//
// A row deleted in the meantime takes the new file with it: ErrNotFound.
// It returns the message's length. Only the body columns bump updated_at,
// as in SetMessageBody.
func (s *Store) CommitMessageRaw(ctx context.Context, accountID, id string, c RawCommit) (int64, error) {
	var n int64
	err := s.WithMessageRaw(ctx, accountID, id, func(tx *RawTx) error {
		var err error
		n, err = tx.Commit(c)
		return err
	})
	return n, err
}

// Commit is CommitMessageRaw for a caller that holds the message's lock
// already: one that read the message under it and decided what to keep
// (the background pass reducing a message), so that nothing changes the
// message between the reading and the commit.
func (tx *RawTx) Commit(c RawCommit) (int64, error) {
	if tx.done {
		return 0, errRawTxDone
	}
	if c.Source == nil {
		return 0, fmt.Errorf("commit message file: no source")
	}
	remote := normalizeParts(c.RemoteParts)
	switch {
	case (len(remote) > 0) != (c.RemoteBytes > 0):
		return 0, fmt.Errorf("commit message file: %d remote parts of %d bytes", len(remote), c.RemoteBytes)
	case c.StrippableBytes < StrippableNever:
		return 0, fmt.Errorf("commit message file: strippable bytes %d", c.StrippableBytes)
	}
	s, ctx, accountID, id := tx.s, tx.ctx, tx.h.accountID, tx.h.id
	stored, err := statRaw(tx.h.dir, id)
	if err != nil {
		return 0, err
	}
	if err := s.commitPhaseA(ctx, accountID, id, c.Expect, remote, c.RemoteBytes, stored.any()); err != nil {
		return 0, err
	}
	if s.afterPhaseA != nil {
		if err := s.afterPhaseA(); err != nil {
			return 0, err
		}
	}
	info, err := tx.Replace(RawWrite{Size: c.Source.Size()}, c.Source)
	if err != nil {
		return 0, err
	}
	// The file is in place: finish the row even if the caller has given
	// up meanwhile.
	found, err := s.commitPhaseB(context.WithoutCancel(ctx), accountID, id, remote, c)
	if err != nil {
		return 0, err
	}
	if !found {
		s.unlinkRaw(tx.h.l, tx.h.dir, id, nil)
		return 0, ErrNotFound
	}
	return info.Bytes, nil
}

// commitPhaseA checks the row against want and, when the new remote set is
// not within the old one, widens the row's set to their union. With
// replacing (the message has a stored file, which the commit's file is to
// replace) a widening is flushed to disk before it returns: the stored file
// may be the whole message, and a row that lost the widening while the
// skeleton that replaced the file survived would call its parts stored for
// good. A message's first file needs no such care: a crash that loses the
// row's commits leaves its body not downloaded, and the syncer fetches it
// again.
func (s *Store) commitPhaseA(ctx context.Context, accountID, id string, want RawExpect, remote []string, remoteBytes int64, replacing bool) error {
	tx, end, err := s.beginTx(ctx, replacing && len(remote) > 0)
	if err != nil {
		return fmt.Errorf("commit message file: %w", err)
	}
	defer end()
	var body, raw, hydrated, parts, role string
	err = tx.QueryRowContext(ctx, `
		SELECT m.body_state, m.raw_state, m.hydrated_at, m.remote_parts, f.role
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.id = ? AND m.account_id = ?`, id, accountID).Scan(&body, &raw, &hydrated, &parts, &role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("commit message file: %w", err)
	case role == string(api.RoleOutbox):
		return ErrOutbox
	case role == string(api.RoleDrafts) && len(remote) > 0:
		// Moved into Drafts since the caller decided: a draft keeps
		// every part (ingest.Decide).
		return fmt.Errorf("%w: the message is in Drafts", ErrConflict)
	case want.BodyState != "" && BodyState(body) != want.BodyState:
		return fmt.Errorf("%w: body %s, expected %s", ErrConflict, body, want.BodyState)
	case want.RawState != "" && RawState(raw) != want.RawState:
		return fmt.Errorf("%w: file %s, expected %s", ErrConflict, raw, want.RawState)
	case want.HydratedAt != nil && optStamp(*want.HydratedAt) != hydrated:
		return fmt.Errorf("%w: downloaded at %q", ErrConflict, hydrated)
	}
	var old []string
	if err := json.Unmarshal([]byte(parts), &old); err != nil {
		return fmt.Errorf("decode remote parts of %s: %w", id, err)
	}
	if isSubset(remote, old) {
		return nil
	}
	union, err := json.Marshal(normalizeParts(append(old, remote...)))
	if err != nil {
		return fmt.Errorf("encode remote parts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET remote_parts = ?, raw_state = ?, remote_bytes = MAX(remote_bytes, ?)
		WHERE id = ?`, string(union), string(RawPartial), remoteBytes, id); err != nil {
		return fmt.Errorf("commit message file: %w", err)
	}
	if s.phaseACommit != nil {
		if err := s.phaseACommit(tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit message file: %w", err)
	}
	return nil
}

// MarkPartsRemote records that the message's stored file lacks the data of
// parts its row calls stored, as phase A of a reduction would: those of
// parts the row's attachments list join remote_parts and their sizes
// remote_bytes, and the row becomes partial, so that Attachment.remote says
// so and message.download fetches the message again. It repairs a file its
// row does not describe (a skeleton kept by a crash that lost the row's
// commits, a leftover file of the other codec); a reduction never needs
// it. The body columns and updated_at stay. ErrNotFound without a row,
// ErrOutbox for an outbox message (no server copy to fetch it from),
// ErrConflict when its body is not fetched (a download fetches it whole
// anyway).
func (s *Store) MarkPartsRemote(ctx context.Context, accountID, id string, parts []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark parts remote: %w", err)
	}
	defer tx.Rollback()
	var body, current, atts, role string
	err = tx.QueryRowContext(ctx, `
		SELECT m.body_state, m.remote_parts, m.attachments_json, f.role
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.id = ? AND m.account_id = ?`, id, accountID).Scan(&body, &current, &atts, &role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("mark parts remote: %w", err)
	case role == string(api.RoleOutbox):
		return ErrOutbox
	case BodyState(body) != BodyFetched:
		return fmt.Errorf("%w: body %s", ErrConflict, body)
	}
	var old []string
	if err := json.Unmarshal([]byte(current), &old); err != nil {
		return fmt.Errorf("decode remote parts of %s: %w", id, err)
	}
	var stored []api.Attachment
	if err := json.Unmarshal([]byte(atts), &stored); err != nil {
		return fmt.Errorf("decode attachments of %s: %w", id, err)
	}
	union := slices.Clone(old)
	var added int64
	for _, p := range normalizeParts(parts) {
		i := slices.IndexFunc(stored, func(a api.Attachment) bool { return a.PartID == p })
		if i < 0 || slices.Contains(union, p) {
			continue
		}
		union = append(union, p)
		added += max(stored[i].Size, 1) // remote_bytes > 0 whenever the row is partial
	}
	if added == 0 {
		return nil
	}
	enc, err := json.Marshal(normalizeParts(union))
	if err != nil {
		return fmt.Errorf("encode remote parts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET remote_parts = ?, raw_state = ?, remote_bytes = remote_bytes + ?
		WHERE id = ?`, string(enc), string(RawPartial), added, id); err != nil {
		return fmt.Errorf("mark parts remote: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mark parts remote: %w", err)
	}
	return nil
}

// commitPhaseB writes the row's final state; false when the row is gone.
func (s *Store) commitPhaseB(ctx context.Context, accountID, id string, remote []string, c RawCommit) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("commit message file: %w", err)
	}
	defer tx.Rollback()
	if c.Body != nil {
		if err := setBodyTx(ctx, tx, id, *c.Body); err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
	}
	state := RawFull
	if len(remote) > 0 {
		state = RawPartial
	}
	parts, err := json.Marshal(remote)
	if err != nil {
		return false, fmt.Errorf("encode remote parts: %w", err)
	}
	hydrated := ""
	if c.Hydrated {
		hydrated = nowStamp()
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE messages SET raw_state = ?, remote_parts = ?, remote_bytes = ?, strippable_bytes = ?,
			hydrated_at = CASE WHEN ? = 1 THEN ? ELSE hydrated_at END
		WHERE id = ? AND account_id = ?`,
		string(state), string(parts), c.RemoteBytes, c.StrippableBytes, boolInt(c.Hydrated), hydrated, id, accountID)
	if err != nil {
		return false, fmt.Errorf("commit message file: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if c.Body != nil {
		if _, err := relinkTx(ctx, tx, id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit message file: %w", err)
	}
	return true, nil
}

// normalizeParts returns the non-empty part ids, de-duplicated and sorted
// (never nil, so it encodes as "[]").
func normalizeParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// isSubset reports whether every element of a is in b.
func isSubset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// withoutRemote returns atts with no Remote flag set, for storing: the
// flag lives in remote_parts only (overlayRemote puts it back).
func withoutRemote(atts []api.Attachment) []api.Attachment {
	if !slices.ContainsFunc(atts, func(a api.Attachment) bool { return a.Remote }) {
		return atts
	}
	out := slices.Clone(atts)
	for i := range out {
		out[i].Remote = false
	}
	return out
}

// overlayRemote sets Attachments[i].Remote from the row's remote set, so
// the flag always says what the file holds. It is set only on a fetched
// body (api.Attachment.Remote).
func overlayRemote(m *Message) {
	partial := m.BodyState == BodyFetched && len(m.RemoteParts) > 0
	for i := range m.Attachments {
		m.Attachments[i].Remote = partial && slices.Contains(m.RemoteParts, m.Attachments[i].PartID)
	}
}

// StripQuery selects the messages the background pass may reduce
// (ListStripCandidates).
type StripQuery struct {
	// Cutoff: messages older than it, by the age messageAge describes.
	// Ignored with All.
	Cutoff time.Time
	// All takes messages of any age (attachmentOfflineDays -1).
	All bool
	// HydratedBefore: messages never made whole on demand, or last before
	// it (the grace after a download). The zero time admits only messages
	// never made whole.
	HydratedBefore time.Time
	// AnyHydrated takes messages however recently they were made whole on
	// demand (Preferences.NeverStoreAttachments); HydratedBefore is
	// ignored.
	AnyHydrated bool
	// Partial takes messages stored partial instead of whole: those that
	// still hold a non-empty attachment (storesAttachment), whatever their
	// age, to lose it too (Preferences.NeverStoreAttachments); Cutoff, All,
	// HydratedBefore and AnyHydrated are ignored.
	Partial bool
	// Limit caps the result (<= 0: 50).
	Limit int
}

// storesAttachment is the SQL condition that the message m lists an
// attachment of at least the parameter's bytes that its stored file holds:
// one not among its remote_parts. A message stored whole holds every one.
const storesAttachment = `EXISTS (SELECT 1 FROM json_each(m.attachments_json) j
	WHERE j.type = 'object' AND COALESCE(json_extract(j.value, '$.size'), 0) >= ?
	  AND NOT EXISTS (SELECT 1 FROM json_each(m.remote_parts) r WHERE r.value = json_extract(j.value, '$.partId')))`

// StripCandidate is a message ListStripCandidates found, with the role of
// its folder.
type StripCandidate struct {
	Message
	Role api.FolderRole
}

// messageAge is the SQL key a message's age is judged by: the server's
// internal date, else the Date header (which the sender chose), else
// infinitely new ('9999'), the order ingest.Decide uses. The parameter is
// zeroStamp: a missing Date is stored as the zero time, which would make
// the message the oldest of all.
const messageAge = `COALESCE(NULLIF(m.internal_date, ''), NULLIF(NULLIF(m.date, ''), ?), '9999')`

// ListStripCandidates returns messages the background pass may reduce,
// oldest first: stored whole (or with q.Partial stored partial, still
// holding a non-empty attachment) and fetched, neither known to have
// nothing to leave on the server (strippable_bytes 0) nor never to be
// reduced (StrippableNever), outside Drafts and the outbox, with a copy on
// the server (a UID or a remote id), of an enabled account, and within q.
func (s *Store) ListStripCandidates(ctx context.Context, q StripQuery) ([]StripCandidate, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	// "strippable_bytes != 0" as the partial index messages_strippable
	// says it, so that the index serves the search; the partial messages
	// are found through messages_partial.
	state := `m.raw_state = 'full'`
	if q.Partial {
		state = `m.raw_state = 'partial'`
	}
	where := `
		WHERE ` + state + ` AND m.body_state = 'fetched' AND m.strippable_bytes != 0
		  AND m.strippable_bytes != ?
		  AND f.role NOT IN (?, ?)
		  AND (m.uid > 0 OR m.remote_id != '')
		  AND m.account_id IN (SELECT id FROM accounts WHERE enabled = 1)`
	args := []any{StrippableNever, string(api.RoleDrafts), string(api.RoleOutbox)}
	switch {
	case q.Partial:
		where += ` AND ` + storesAttachment
		args = append(args, 1)
	default:
		if !q.AnyHydrated {
			where += ` AND (m.hydrated_at = '' OR m.hydrated_at < ?)`
			args = append(args, optStamp(q.HydratedBefore))
		}
		if !q.All {
			where += ` AND ` + messageAge + ` < ?`
			args = append(args, zeroStamp, stamp(q.Cutoff))
		}
	}
	args = append(args, zeroStamp, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+qualifiedMessageColumns+`, f.role
		FROM messages m JOIN folders f ON f.id = m.folder_id`+where+`
		ORDER BY `+messageAge+`, m.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list strip candidates: %w", err)
	}
	defer rows.Close()
	var out []StripCandidate
	for rows.Next() {
		var role string
		m, _, err := scanMessageStamp(withExtra{rows, []any{&role}})
		if err != nil {
			return nil, fmt.Errorf("scan strip candidate: %w", err)
		}
		out = append(out, StripCandidate{Message: m, Role: api.FolderRole(role)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list strip candidates: %w", err)
	}
	return out, nil
}

// classifyBatch bounds one statement of ClassifySmall, so that the write
// lock is short.
const classifyBatch = 2000

// ClassifySmall marks fetched, whole messages not evaluated yet whose
// attachments are all smaller than threshold as having nothing to leave on
// the server (strippable_bytes 0), without reading their files, and
// returns how many it marked.
func (s *Store) ClassifySmall(ctx context.Context, threshold int64) (int, error) {
	total := 0
	for {
		// "strippable_bytes != 0" as the partial index messages_strippable
		// says it, so that the daily pass reads the index, not the table.
		res, err := s.db.ExecContext(ctx, `
			UPDATE messages SET strippable_bytes = 0 WHERE id IN (
				SELECT m.id FROM messages m
				WHERE m.strippable_bytes = -1 AND m.strippable_bytes != 0
				  AND m.raw_state = 'full' AND m.body_state = 'fetched'
				  AND NOT EXISTS (SELECT 1 FROM json_each(m.attachments_json) j
				                  WHERE j.type = 'object' AND COALESCE(json_extract(j.value, '$.size'), 0) >= ?)
				LIMIT ?)`, threshold, classifyBatch)
		if err != nil {
			return total, fmt.Errorf("classify small messages: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
		if n < classifyBatch {
			return total, nil
		}
	}
}

// ReevaluateSettled marks the fetched messages settled as having nothing
// to leave on the server (strippable_bytes 0) whose stored file holds an
// attachment of at least minBytes (storesAttachment: any such of a whole
// message, one not among remote_parts of a partial one; a picture the
// HTML shows counts, it is listed as one) as not evaluated again
// (StrippableUnknown), so that the background pass judges them by a rule
// that leaves more on the server (Preferences.NeverStoreAttachments:
// smaller attachments, large pictures the HTML shows); messages without
// such an attachment keep their 0, and those never to be reduced
// (StrippableNever) theirs. It walks the table in slices of the primary key, so that
// the write lock is short, and returns how many it marked.
func (s *Store) ReevaluateSettled(ctx context.Context, minBytes int64) (int, error) {
	total, after := 0, ""
	for {
		var last sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM (SELECT id FROM messages WHERE id > ? ORDER BY id LIMIT ?)`,
			after, classifyBatch).Scan(&last); err != nil {
			return total, fmt.Errorf("re-evaluate settled messages: %w", err)
		}
		if !last.Valid {
			return total, nil
		}
		res, err := s.db.ExecContext(ctx, `
			UPDATE messages AS m SET strippable_bytes = ?
			WHERE m.id > ? AND m.id <= ? AND m.strippable_bytes = 0 AND m.body_state = 'fetched'
			  AND `+storesAttachment,
			StrippableUnknown, after, last.String, minBytes)
		if err != nil {
			return total, fmt.Errorf("re-evaluate settled messages: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
		after = last.String
	}
}

// ReevaluateSettledMessage is ReevaluateSettled for one message: one
// stored under a rule that no longer holds (a download that was under way
// when Preferences.NeverStoreAttachments was switched on). It reports
// whether it marked the message; an unknown id marks nothing.
func (s *Store) ReevaluateSettledMessage(ctx context.Context, id string, minBytes int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE messages AS m SET strippable_bytes = ?
		WHERE m.id = ? AND m.strippable_bytes = 0 AND m.body_state = 'fetched'
		  AND `+storesAttachment,
		StrippableUnknown, id, minBytes)
	if err != nil {
		return false, fmt.Errorf("re-evaluate message %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetStrippableBytes records what the background pass found it could leave
// on the server of a message (StrippableUnknown, StrippableNever, 0
// nothing, more). ErrNotFound for an unknown id.
func (s *Store) SetStrippableBytes(ctx context.Context, id string, n int64) error {
	if n < StrippableNever {
		return fmt.Errorf("set strippable bytes: %d", n)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE messages SET strippable_bytes = ? WHERE id = ?`, n, id)
	if err != nil {
		return fmt.Errorf("set strippable bytes: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ServerLocation is where the mail server keeps a message: its remote id
// (Graph), else its UID in Folder (IMAP; Folder carries the mailbox name
// and UIDVALIDITY).
type ServerLocation struct {
	Folder   Folder
	UID      uint32
	RemoteID string
}

// MessageServerLocation finds a message on its server: by its remote id,
// else its UID, else, for a message whose local move is not reconciled yet
// (UID 0), by the oldest snapshot of its pending operations that has a
// UID, which names where the server still has it. ErrNotFound when the
// message or any server identity is missing.
func (s *Store) MessageServerLocation(ctx context.Context, accountID, id string) (ServerLocation, error) {
	var folderID, remoteID string
	var uid int64
	err := s.db.QueryRowContext(ctx, `SELECT folder_id, uid, remote_id FROM messages WHERE id = ? AND account_id = ?`,
		id, accountID).Scan(&folderID, &uid, &remoteID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ServerLocation{}, ErrNotFound
	case err != nil:
		return ServerLocation{}, fmt.Errorf("message location: %w", err)
	}
	loc := ServerLocation{UID: uint32(uid), RemoteID: remoteID}
	if remoteID == "" && uid == 0 {
		err := s.db.QueryRowContext(ctx, `SELECT folder_id, uid FROM message_ops
			WHERE message_id = ? AND account_id = ? AND uid > 0 ORDER BY id LIMIT 1`, id, accountID).Scan(&folderID, &uid)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ServerLocation{}, ErrNotFound
		case err != nil:
			return ServerLocation{}, fmt.Errorf("message location: %w", err)
		}
		loc.UID = uint32(uid)
	}
	f, err := s.GetFolder(ctx, accountID, folderID)
	if err != nil {
		return ServerLocation{}, err
	}
	loc.Folder = f
	return loc, nil
}

// RemoteStats counts the messages whose large attachments stay on the
// server and those attachments' decoded size.
type RemoteStats struct {
	Messages int
	Bytes    int64
}

// RemoteStats counts the partial messages of the account ("" = all).
func (s *Store) RemoteStats(ctx context.Context, accountID string) (RemoteStats, error) {
	query := `SELECT COUNT(*), COALESCE(SUM(remote_bytes), 0) FROM messages WHERE raw_state = 'partial'`
	var args []any
	if accountID != "" {
		query += ` AND account_id = ?`
		args = append(args, accountID)
	}
	var st RemoteStats
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&st.Messages, &st.Bytes); err != nil {
		return RemoteStats{}, fmt.Errorf("remote stats: %w", err)
	}
	return st, nil
}
