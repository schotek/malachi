// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's upkeep: filling the dirty set beyond what the triggers do
// (the first evaluation of stored mail, a new rule version, a change of an
// account's settings), the remind timer, retention, and moving a case
// with its thread when threads merge.

// MarkBoardDirtyBatch is the first evaluation's pass over stored mail: it
// visits up to limit messages whose id sorts after afterID ("" = from the
// start) and marks the threads of the visible ones dated (header or
// arrival) at or after since dirty. It returns the last id visited ("" when
// nothing was left) and how many rows it visited. One statement per batch,
// so it interleaves with a running sync.
func (s *Store) MarkBoardDirtyBatch(ctx context.Context, since time.Time, afterID string, limit int) (lastID string, visited int, err error) {
	if limit <= 0 {
		limit = 2000
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("mark board dirty: %w", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(id), '') FROM (SELECT id FROM messages WHERE id > ? ORDER BY id LIMIT ?)`,
		afterID, limit).Scan(&visited, &lastID); err != nil {
		return "", 0, fmt.Errorf("mark board dirty: %w", err)
	}
	if visited == 0 {
		return "", 0, nil
	}
	sinceStamp := stamp(since)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO board_dirty (account_id, thread_id)
		SELECT DISTINCT account_id, thread_id FROM messages
		WHERE id > ? AND id <= ? AND hidden = 0 AND thread_id != '' AND (date >= ? OR internal_date >= ?)`,
		afterID, lastID, sinceStamp, sinceStamp); err != nil {
		return "", 0, fmt.Errorf("mark board dirty: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", 0, fmt.Errorf("mark board dirty: %w", err)
	}
	return lastID, visited, nil
}

// MarkBoardThreadsDirty marks threads of an account dirty.
func (s *Store) MarkBoardThreadsDirty(ctx context.Context, accountID string, threadIDs []string) error {
	for _, chunk := range chunkStrings(threadIDs, linkChunk) {
		args := []any{}
		values := make([]string, 0, len(chunk))
		for _, t := range chunk {
			if t == "" {
				continue
			}
			values = append(values, "(?, ?)")
			args = append(args, accountID, t)
		}
		if len(values) == 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO board_dirty (account_id, thread_id) VALUES `+strings.Join(values, ", "), args...); err != nil {
			return fmt.Errorf("mark board threads dirty: %w", err)
		}
	}
	return nil
}

// MarkBoardAccountDirty marks dirty every thread of the account with a
// visible member dated at or after since (zero: any) and every thread of
// its cases (a change of the account's settings, folder roles or
// capabilities that the rules read).
func (s *Store) MarkBoardAccountDirty(ctx context.Context, accountID string, since time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark board account dirty: %w", err)
	}
	defer tx.Rollback()
	sinceStamp := ""
	if !since.IsZero() {
		sinceStamp = stamp(since)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO board_dirty (account_id, thread_id)
			SELECT DISTINCT account_id, thread_id FROM messages
			WHERE account_id = ? AND hidden = 0 AND thread_id != '' AND (date >= ? OR internal_date >= ?)`,
			[]any{accountID, sinceStamp, sinceStamp}},
		{`INSERT OR IGNORE INTO board_dirty (account_id, thread_id) SELECT account_id, thread_id FROM board_cases WHERE account_id = ?`,
			[]any{accountID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("mark board account dirty: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mark board account dirty: %w", err)
	}
	return nil
}

// MarkBoardCasesFrom marks dirty the threads of the cases of the accounts
// that have a member from one of the addresses (its From or a Reply-To,
// compared lower case and trimmed): whose known-ness changed (the user's
// known correspondents were read again). Only the cases: an unknown
// sender's mail is always a case, so a thread whose verdict changes with
// its sender's known-ness is one. Nothing without accounts or addresses.
func (s *Store) MarkBoardCasesFrom(ctx context.Context, accountIDs, addresses []string) error {
	if len(accountIDs) == 0 || len(addresses) == 0 {
		return nil
	}
	accounts, err := json.Marshal(accountIDs)
	if err != nil {
		return fmt.Errorf("mark board cases dirty: %w", err)
	}
	addrs, err := json.Marshal(addresses)
	if err != nil {
		return fmt.Errorf("mark board cases dirty: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `WITH changed(a) AS (SELECT lower(trim(value)) FROM json_each(?))
		INSERT OR IGNORE INTO board_dirty (account_id, thread_id)
		SELECT c.account_id, c.thread_id FROM board_cases c
		WHERE c.account_id IN (SELECT value FROM json_each(?))
			AND EXISTS (SELECT 1 FROM messages m WHERE m.account_id = c.account_id AND m.thread_id = c.thread_id
				AND (EXISTS (SELECT 1 FROM json_each(m.from_json) j WHERE j.type = 'object'
						AND lower(trim(json_extract(j.value, '$.address'))) IN (SELECT a FROM changed))
					OR EXISTS (SELECT 1 FROM json_each(m.reply_to_json) j WHERE j.type = 'object'
						AND lower(trim(json_extract(j.value, '$.address'))) IN (SELECT a FROM changed))))`,
		string(addrs), string(accounts)); err != nil {
		return fmt.Errorf("mark board cases dirty: %w", err)
	}
	return nil
}

// MarkBoardCasesDirty marks the thread of every case dirty (a new rule
// version; the caller also starts the pass of MarkBoardDirtyBatch again).
func (s *Store) MarkBoardCasesDirty(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO board_dirty (account_id, thread_id) SELECT account_id, thread_id FROM board_cases`); err != nil {
		return fmt.Errorf("mark board cases dirty: %w", err)
	}
	return nil
}

// CountBoardDirty counts the threads waiting to be evaluated.
func (s *Store) CountBoardDirty(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM board_dirty`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count board dirty: %w", err)
	}
	return n, nil
}

// NextBoardRemind is the earliest remind of any case that has not come due
// yet (ClearDueBoardReminds has not seen it); false when none. One already
// past (the daemon was not running) is returned too, so the caller fires
// it at once.
func (s *Store) NextBoardRemind(ctx context.Context) (time.Time, bool, error) {
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT remind_at FROM board_cases WHERE reminded = 0 AND remind_at != ''
		ORDER BY remind_at LIMIT 1`).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("next board remind: %w", err)
	}
	return parseStamp(at), true, nil
}

// ClearDueBoardReminds marks the reminds that came due at now as reminded
// (the cases are live again, their version up, once; BoardCase.RemindedAt)
// and returns the accounts concerned, sorted. remind_at stays: a remind
// that came due keeps its case on the board until the user acts on the
// case or inbound mail that counts arrives. reminded stays as well when
// the clock goes back before remind_at afterwards (the case stays live).
func (s *Store) ClearDueBoardReminds(ctx context.Context, now time.Time) ([]string, error) {
	accounts, err := stringColumn(ctx, s.db, `UPDATE board_cases SET reminded = 1, version = version + 1, updated_at = ?
		WHERE reminded = 0 AND remind_at != '' AND remind_at <= ? RETURNING account_id`, nowStamp(), stamp(now))
	if err != nil {
		return nil, fmt.Errorf("clear board reminds: %w", err)
	}
	return distinctSorted(accounts), nil
}

// BoardPrune says which cases PruneBoardCases deletes.
type BoardPrune struct {
	// Before: cases dated before it that were done before DoneBefore
	// (whatever kept them while they were live: the user's state among
	// them), and those not done unless something keeps them (a user state,
	// a remind, an open commitment, a current annotation's deadline after
	// Now while Assistant, a linked draft that exists).
	Before time.Time
	// DoneBefore: the done retention. A case done before it is pruned as
	// above; a suggested reply it still links loses it first (the board
	// lists a done case with one until then).
	DoneBefore time.Time
	// OrphanBefore: cases whose thread has had no visible member since
	// before it, whatever keeps them; zero = Now less BoardOrphanGrace.
	OrphanBefore time.Time
	Now          time.Time
	// Assistant: the board's assistant preference is on (an annotation's
	// deadline keeps a case only then).
	Assistant bool
}

// PruneBoardCases deletes the cases off the board for good (the hourly
// tick) with their annotations and commitments, and returns the accounts
// concerned, sorted. In the same transaction a suggested reply loses its
// case when the case goes (orphaned) or its done retention ends
// (DoneBefore: the link is dropped, the case stays to be pruned as any
// done case): releaseLinkedDraftTx makes an edited one an ordinary draft
// and deletes an untouched one, and the line under MetaBoardUnlinkedDrafts
// tells the upkeep.
func (s *Store) PruneBoardCases(ctx context.Context, p BoardPrune) ([]string, error) {
	n := stamp(p.Now)
	orphanBefore := p.OrphanBefore
	if orphanBefore.IsZero() {
		orphanBefore = p.Now.Add(-BoardOrphanGrace)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prune board cases: %w", err)
	}
	defer tx.Rollback()
	type gone struct{ account, id, draft, reason string }
	collect := func(query string, args ...any) ([]gone, error) {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []gone
		for rows.Next() {
			var g gone
			if err := rows.Scan(&g.account, &g.id, &g.draft, &g.reason); err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, rows.Err()
	}
	// (RETURNING would give the new, empty draft_id: read first.)
	unlinked, err := collect(`SELECT account_id, id, draft_id, 'done' FROM board_cases
		WHERE draft_id != '' AND done_at != '' AND done_at < ? AND NOT (orphaned_at != '' AND orphaned_at < ?)`,
		stamp(p.DoneBefore), stamp(orphanBefore))
	if err != nil {
		return nil, fmt.Errorf("prune board cases: the replies of done cases: %w", err)
	}
	for _, g := range unlinked {
		if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET draft_id = '', version = version + 1, updated_at = ? WHERE id = ?`,
			n, g.id); err != nil {
			return nil, fmt.Errorf("prune board cases: unlink the reply of a done case: %w", err)
		}
	}
	deleted, err := collect(`DELETE FROM board_cases AS c
		WHERE (c.orphaned_at != '' AND c.orphaned_at < ?)
			OR (c.date < ? AND c.done_at != '' AND c.done_at < ?)
			OR (c.date < ? AND c.done_at = '' AND c.user_state = '' AND c.remind_at = ''
			AND NOT EXISTS (SELECT 1 FROM board_commitments k WHERE k.case_id = c.id AND k.state = 'open')
			AND NOT EXISTS (SELECT 1 FROM drafts d WHERE c.draft_id != '' AND d.id = c.draft_id AND d.account_id = c.account_id)
			AND NOT (? AND EXISTS (SELECT 1 FROM board_annotations a
				WHERE a.case_id = c.id AND a.input_key = c.input_key AND a.due_at != '' AND a.due_at > ?)))
		RETURNING account_id, id, draft_id, CASE WHEN orphaned_at != '' AND orphaned_at < ? THEN 'orphan' ELSE 'old' END`,
		stamp(orphanBefore), stamp(p.Before), stamp(p.DoneBefore), stamp(p.Before), boolInt(p.Assistant), n, stamp(orphanBefore))
	if err != nil {
		return nil, fmt.Errorf("prune board cases: %w", err)
	}
	accounts := map[string]bool{}
	var files []string
	for _, g := range append(unlinked, deleted...) {
		accounts[g.account] = true
		outcome, f, err := releaseLinkedDraftTx(ctx, tx, g.account, g.draft, false)
		if err != nil {
			return nil, err
		}
		files = append(files, f...)
		if err := recordUnlinkedDraftTx(ctx, tx, BoardUnlinkedDraft{AccountID: g.account, DraftID: g.draft, CaseID: g.id,
			Reason: g.reason, Outcome: outcome}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("prune board cases: %w", err)
	}
	s.removeAttachmentFiles(files...)
	return sortedKeys(accounts), nil
}

// BoardOwnAddresses lists the From addresses of the account's rows in its
// folders of role sent, lower case, the most frequent first, at most
// limit: with the account's own address, the user's addresses the rules
// use for "addressed to the user" (never for "the user's own").
func (s *Store) BoardOwnAddresses(ctx context.Context, accountID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	out, err := stringColumn(ctx, s.db, `SELECT lower(trim(json_extract(j.value, '$.address'))) AS addr
		FROM messages m, json_each(m.from_json) j
		WHERE m.account_id = ? AND m.hidden = 0
			AND m.folder_id IN (SELECT id FROM folders WHERE account_id = ? AND role = '`+string(api.RoleSent)+`')
			AND j.type = 'object' AND COALESCE(trim(json_extract(j.value, '$.address')), '') != ''
		GROUP BY addr ORDER BY COUNT(*) DESC, addr LIMIT ?`, accountID, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("board own addresses: %w", err)
	}
	return out, nil
}

// MetaBoardUnlinkedDrafts is the meta key under which the store records
// the suggested replies that lost their case (TakeBoardUnlinkedDrafts).
const MetaBoardUnlinkedDrafts = "board.unlinkedDrafts"

// boardUnlinkedBytes caps that record; the oldest lines go first.
const boardUnlinkedBytes = 64 << 10

// boardMergeRow is what mergeBoardCaseTx reads of a case: plain columns,
// nothing decoded, so board data can never fail the mail write that
// merges the threads.
type boardMergeRow struct {
	id, userState, userAt, doneAt, remindAt, doneSeen string
	reminded                                          int
	annotated                                         bool
	draftID, memberIDs                                string
}

func boardMergeRowTx(ctx context.Context, tx *sql.Tx, accountID, threadID string) (boardMergeRow, bool, error) {
	var r boardMergeRow
	err := tx.QueryRowContext(ctx, `SELECT c.id, c.user_state, c.user_state_at, c.done_at, c.remind_at, c.reminded, c.done_seen,
			a.case_id IS NOT NULL, c.draft_id, c.member_ids
		FROM board_cases c LEFT JOIN board_annotations a ON a.case_id = c.id
		WHERE c.account_id = ? AND c.thread_id = ?`, accountID, threadID).
		Scan(&r.id, &r.userState, &r.userAt, &r.doneAt, &r.remindAt, &r.reminded, &r.doneSeen, &r.annotated, &r.draftID, &r.memberIDs)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return r, false, nil
	case err != nil:
		return r, false, fmt.Errorf("board: read case for the merge: %w", err)
	}
	return r, true, nil
}

// mergeBoardCaseTx moves the case of an absorbed thread to the canonical
// one inside the linking transaction (linkMessageTx). Without a case on
// the canonical thread the absorbed case is renamed: its id, the user's
// decisions, its annotation and commitments stay. With one, the canonical
// case stays and takes over: the user state set later, done only when both
// were done (the later time, the Message-IDs of both; else the commitments
// that done closed are open again), else the earlier
// remind of either (with when it was set and the Message-IDs it saw, plus
// those the other case remembers of its members, so that the merge itself
// does not end it), the canonical annotation (the absorbed one when it
// has none), the canonical draft link (the absorbed one when it has none,
// or links a draft that is gone while the absorbed one's exists) and
// every commitment. When both cases link a draft that exists, the
// canonical link stays and the absorbed case's draft loses its case:
// releaseLinkedDraftTx makes it an ordinary draft when the user edited it
// and deletes it when untouched, and recordUnlinkedDraftTx notes it under
// MetaBoardUnlinkedDrafts. Both annotations are stale after a merge anyway (the members changed), and
// the triggers have marked both threads dirty. It reads plain columns and
// compares the store's fixed-width stamps as text: nothing here decodes,
// so a board row cannot fail the merge.
// boardMergeLog is where mergeBoardCaseTx reports what it carries on
// past: it runs inside the thread linker's transaction, which has no
// logger of the store's (a variable for the tests).
var boardMergeLog = slog.Default

func mergeBoardCaseTx(ctx context.Context, tx *sql.Tx, accountID, from, canonical string) error {
	absorbed, ok, err := boardMergeRowTx(ctx, tx, accountID, from)
	if err != nil || !ok {
		return err
	}
	now := nowStamp()
	keep, ok, err := boardMergeRowTx(ctx, tx, accountID, canonical)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET thread_id = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			canonical, now, absorbed.id); err != nil {
			return fmt.Errorf("board: move case to merged thread: %w", err)
		}
		return nil
	}

	userState, userAt := keep.userState, keep.userAt
	if absorbed.userState != "" && (keep.userState == "" || absorbed.userAt > keep.userAt) {
		userState, userAt = absorbed.userState, absorbed.userAt
	}
	var doneAt, doneSeen, remindAt string
	reminded := 0
	if keep.doneAt != "" && absorbed.doneAt != "" {
		doneAt = max(keep.doneAt, absorbed.doneAt)
		doneSeen = mergeDoneSeen(keep.doneSeen, absorbed.doneSeen)
	} else {
		for i, r := range []boardMergeRow{keep, absorbed} {
			if r.remindAt != "" && (remindAt == "" || r.remindAt < remindAt) {
				other := [2]boardMergeRow{absorbed, keep}[i]
				remindAt, reminded = r.remindAt, r.reminded
				doneSeen = ""
				if r.doneAt == "" {
					doneSeen = mergeDoneSeen(r.doneSeen, other.memberIDs)
				}
			}
		}
	}
	if doneAt == "" {
		// Live after the merge: the commitments that marking either case
		// done closed are open again (SetBoardDone with done false). A
		// failure here is the board's alone and never fails the mail
		// write that merges the threads: logged, the merge goes on.
		for _, r := range []boardMergeRow{keep, absorbed} {
			if r.doneAt == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE board_commitments SET state = 'open', closed_reason = '', closed_at = ''
				WHERE case_id = ? AND state = 'closed' AND closed_reason = ? AND closed_at >= ?`,
				r.id, api.CommitmentClosedDone, r.doneAt); err != nil {
				boardMergeLog().Warn("board: merge: reopen the commitments done closed", "case", r.id, "err", err)
			}
		}
	}
	switch {
	case absorbed.annotated && !keep.annotated:
		if _, err := tx.ExecContext(ctx, `UPDATE board_annotations SET case_id = ? WHERE case_id = ?`, keep.id, absorbed.id); err != nil {
			return fmt.Errorf("board: merge annotations: %w", err)
		}
	}
	draftID := keep.draftID
	keepHas, err := boardDraftExistsTx(ctx, tx, accountID, keep.draftID)
	if err != nil {
		return err
	}
	absorbedHas, err := boardDraftExistsTx(ctx, tx, accountID, absorbed.draftID)
	if err != nil {
		return err
	}
	release := ""
	switch {
	case absorbed.draftID != "" && (keep.draftID == "" || (!keepHas && absorbedHas)):
		// The canonical case links no draft, or one that is gone while
		// the absorbed one's exists.
		draftID = absorbed.draftID
	case absorbedHas && absorbed.draftID != keep.draftID:
		// The absorbed case's suggested reply loses its case: the one rule
		// (releaseLinkedDraftTx), once the absorbed case is gone.
		release = absorbed.draftID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_commitments SET case_id = ? WHERE case_id = ?`, keep.id, absorbed.id); err != nil {
		return fmt.Errorf("board: merge commitments: %w", err)
	}
	if err := deleteBoardCaseTx(ctx, tx, absorbed.id); err != nil {
		return err
	}
	if release != "" {
		// Attachments are released for SweepAttachments: this runs inside
		// the mail write that merges the threads.
		outcome, _, err := releaseLinkedDraftTx(ctx, tx, accountID, release, true)
		if err != nil {
			return err
		}
		if err := recordUnlinkedDraftTx(ctx, tx, BoardUnlinkedDraft{AccountID: accountID, DraftID: release, CaseID: keep.id,
			Reason: "merge", Outcome: outcome}); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET user_state = ?, user_state_at = ?, done_at = ?, done_seen = ?,
			remind_at = ?, reminded = ?, draft_id = ?, version = version + 1, updated_at = ? WHERE id = ?`,
		userState, userAt, doneAt, doneSeen, remindAt, reminded, draftID, now, keep.id); err != nil {
		return fmt.Errorf("board: merge cases: %w", err)
	}
	return nil
}

// recordUnlinkedDraftTx appends what happened to a suggested reply that
// lost its case to MetaBoardUnlinkedDrafts, for the upkeep's log and
// upload wake-up (TakeBoardUnlinkedDrafts). A draft that was gone already
// (ReleaseNone) is not recorded.
func recordUnlinkedDraftTx(ctx context.Context, tx *sql.Tx, u BoardUnlinkedDraft) error {
	if u.Outcome == ReleaseNone {
		return nil
	}
	line := strings.Join([]string{u.AccountID, u.DraftID, u.CaseID, u.Reason, string(u.Outcome)}, "\t") + "\n"
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = substr(value || excluded.value, -?)`,
		MetaBoardUnlinkedDrafts, line, boardUnlinkedBytes); err != nil {
		return fmt.Errorf("board: record an unlinked draft: %w", err)
	}
	return nil
}

// BoardUnlinkedDraft is a suggested reply that lost its case without Send
// or Discard, and what releaseLinkedDraftTx did with it.
type BoardUnlinkedDraft struct {
	AccountID string
	DraftID   string
	// CaseID: the case it was linked to, or the surviving case of a merge.
	CaseID string
	// Reason: "merge" (both merged cases had a reply, the surviving one
	// kept its own), "orphan" (the conversation is gone), "done" (the done
	// retention ended), "old", or "account" (the account was removed with
	// its local data kept).
	Reason string
	// Outcome: ReleaseOrdinary (edited: an ordinary draft now, uploaded
	// to the Drafts folder) or ReleaseDeleted (untouched: deleted).
	Outcome LinkedDraftRelease
}

// TakeBoardUnlinkedDrafts returns the suggested replies that lost their
// case since the last call, oldest first, and forgets them.
func (s *Store) TakeBoardUnlinkedDrafts(ctx context.Context) ([]BoardUnlinkedDraft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("take unlinked drafts: %w", err)
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `DELETE FROM meta WHERE key = ? RETURNING value`, MetaBoardUnlinkedDrafts).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("take unlinked drafts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("take unlinked drafts: %w", err)
	}
	var out []BoardUnlinkedDraft
	for _, line := range strings.Split(raw, "\n") {
		// The cap may have cut the oldest line: a line without its five
		// fields is skipped.
		f := strings.Split(line, "\t")
		if len(f) != 5 || f[0] == "" || f[1] == "" || f[2] == "" || f[4] == "" {
			continue
		}
		out = append(out, BoardUnlinkedDraft{AccountID: f[0], DraftID: f[1], CaseID: f[2], Reason: f[3], Outcome: LinkedDraftRelease(f[4])})
	}
	return out, nil
}

// boardDraftExistsTx says whether the account has the draft ("" = no).
func boardDraftExistsTx(ctx context.Context, tx *sql.Tx, accountID, draftID string) (bool, error) {
	if draftID == "" {
		return false, nil
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM drafts WHERE id = ? AND account_id = ?)`,
		draftID, accountID).Scan(&ok); err != nil {
		return false, fmt.Errorf("board: read a draft for the merge: %w", err)
	}
	return ok, nil
}

// BoardDraftLinked says whether a case of the account links the draft (a
// save or deletion of it changes that case).
func (s *Store) BoardDraftLinked(ctx context.Context, accountID, draftID string) (bool, error) {
	if accountID == "" || draftID == "" {
		return false, nil
	}
	var linked bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM board_cases WHERE draft_id = ? AND account_id = ?)`,
		draftID, accountID).Scan(&linked); err != nil {
		return false, fmt.Errorf("board draft linked: %w", err)
	}
	return linked, nil
}

// MaxBoardKnownCorrespondents caps BoardKnownCorrespondents.
const MaxBoardKnownCorrespondents = 20000

// BoardKnownCorrespondents lists the addresses the user has written to:
// those in To or Cc of the rows of the accounts' folders of role sent or
// outbox, lower case and trimmed, the most recently written to first (by
// the Date of the newest such message), at most limit (0 or more than
// MaxBoardKnownCorrespondents: that cap). One read over the sent mail of
// the accounts: call it rarely (hourly) and cache the result.
func (s *Store) BoardKnownCorrespondents(ctx context.Context, accountIDs []string, limit int) ([]string, error) {
	if len(accountIDs) == 0 {
		return []string{}, nil
	}
	if limit <= 0 || limit > MaxBoardKnownCorrespondents {
		limit = MaxBoardKnownCorrespondents
	}
	args := append(toAny(accountIDs), limit)
	out, err := stringColumn(ctx, s.db, `WITH sent AS (
			SELECT m.date, m.to_json, m.cc_json FROM messages m JOIN folders f ON f.id = m.folder_id
			WHERE m.account_id IN (`+inPlaceholders(len(accountIDs))+`)
				AND f.role IN ('`+string(api.RoleSent)+`', '`+string(api.RoleOutbox)+`')),
		addrs AS (
			SELECT sent.date, j.value AS v FROM sent, json_each(sent.to_json) j WHERE j.type = 'object'
			UNION ALL
			SELECT sent.date, j.value AS v FROM sent, json_each(sent.cc_json) j WHERE j.type = 'object')
		SELECT lower(trim(json_extract(v, '$.address'))) AS addr FROM addrs
		WHERE COALESCE(trim(json_extract(v, '$.address')), '') != ''
		GROUP BY addr ORDER BY MAX(date) DESC, addr LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("board known correspondents: %w", err)
	}
	return out, nil
}

// releaseAccountDraftsTx: the account goes with its local data kept, so
// the suggested replies of its cases lose their cases as anywhere else
// (releaseLinkedDraftTx, reason "account"). Returns the attachment ids to
// unlink after the commit.
func releaseAccountDraftsTx(ctx context.Context, tx *sql.Tx, accountID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, draft_id FROM board_cases WHERE account_id = ? AND draft_id != '' ORDER BY id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("delete account board: %w", err)
	}
	var links [][2]string
	for rows.Next() {
		var l [2]string
		if err := rows.Scan(&l[0], &l[1]); err != nil {
			rows.Close()
			return nil, fmt.Errorf("delete account board: %w", err)
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delete account board: %w", err)
	}
	var files []string
	for _, l := range links {
		outcome, f, err := releaseLinkedDraftTx(ctx, tx, accountID, l[1], false)
		if err != nil {
			return nil, err
		}
		files = append(files, f...)
		if err := recordUnlinkedDraftTx(ctx, tx, BoardUnlinkedDraft{AccountID: accountID, DraftID: l[1], CaseID: l[0],
			Reason: "account", Outcome: outcome}); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// deleteBoardAccountTx removes the account's board rows (DeleteAccount),
// after its messages and issues, whose deletion marked its threads dirty.
func deleteBoardAccountTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	for _, q := range []string{
		`DELETE FROM board_cases WHERE account_id = ?`, // annotations and commitments follow
		`DELETE FROM board_commitments WHERE account_id = ?`,
		`DELETE FROM board_dirty WHERE account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, accountID); err != nil {
			return fmt.Errorf("delete account board: %w", err)
		}
	}
	return nil
}

func distinctSorted(in []string) []string {
	m := make(map[string]bool, len(in))
	for _, s := range in {
		m[s] = true
	}
	return sortedKeys(m)
}
