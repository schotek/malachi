// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Annotations, commitments and triage runs of the board. Core cleans and
// checks what an assistant sent (limits, quotes, the draft) before it
// calls these; the store re-checks the input key inside its transaction,
// so an annotation of members that changed in between is refused.

// BoardRunRef names the run a call counts in: RunID when it names a run
// that is open (manual or auto), else the implicit external run of Source
// and Day (the caller's local day, "YYYY-MM-DD"; "" = Now's UTC day),
// created on first use.
type BoardRunRef struct {
	RunID  string
	Source string
	Day    string
}

// BoardAnnotationInput is an annotation to store (board.annotate), cleaned
// and checked by the caller.
type BoardAnnotationInput struct {
	CaseID   string
	InputKey string // the caller's; must be the case's current one
	State    api.BoardState
	Title    string
	Summary  string
	Why      string
	Tasks    []string
	Due      *BoardDue
	// DraftID links a draft to the case ("" leaves the case's link as it
	// is): only when the case links no other draft that still exists.
	DraftID string
	Source  string
	Run     BoardRunRef
	Now     time.Time
}

// AnnotateBoardCase replaces the case's annotation as a whole and counts
// it (annotated) in the run Run names, in one transaction that derives the
// case's input key again from its members: ErrBoardConflict when that is
// not in.InputKey (nothing is written or counted; the caller counts the
// rejection with CountBoardRejected), ErrNotFound for an unknown case. The
// case's input key is brought up to date as well. Returns the case as
// stored and the id of the run counted in.
//
// The case's draft link is not part of the annotation: in.DraftID "" keeps
// it, and in.DraftID is linked only when the case links no other draft that
// still exists (a draft the user linked with board.setDraft, or an earlier
// annotation's, stays). Whether in.DraftID was linked shows in the case
// returned (its DraftID). A draft it links is made local as SetBoardDraft
// does.
func (s *Store) AnnotateBoardCase(ctx context.Context, in BoardAnnotationInput) (BoardCase, string, error) {
	if in.State != "" && !in.State.Valid() {
		return BoardCase{}, "", fmt.Errorf("annotate board case: unknown state %q", in.State)
	}
	tasks, err := encodeJSON(in.Tasks, "[]")
	if err != nil {
		return BoardCase{}, "", fmt.Errorf("annotate board case: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BoardCase{}, "", fmt.Errorf("annotate board case: %w", err)
	}
	defer tx.Rollback()
	c, err := s.currentBoardCaseTx(ctx, tx, in.CaseID, in.InputKey)
	if err != nil {
		return BoardCase{}, "", err
	}
	runID, err := countBoardRunTx(ctx, tx, in.Run, in.Now, "annotated")
	if err != nil {
		return BoardCase{}, "", err
	}
	var dueAt, dueQuote, dueMsg string
	if in.Due != nil {
		dueAt, dueQuote, dueMsg = stamp(in.Due.At), in.Due.Quote, in.Due.MessageID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO board_annotations (case_id, input_key, state, title, summary, why, tasks_json,
			due_at, due_quote, due_message_id, source, run_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (case_id) DO UPDATE SET input_key = excluded.input_key, state = excluded.state, title = excluded.title,
			summary = excluded.summary, why = excluded.why, tasks_json = excluded.tasks_json, due_at = excluded.due_at,
			due_quote = excluded.due_quote, due_message_id = excluded.due_message_id,
			source = excluded.source, run_id = excluded.run_id, created_at = excluded.created_at`,
		c.ID, in.InputKey, string(in.State), in.Title, in.Summary, in.Why, tasks,
		dueAt, dueQuote, dueMsg, in.Source, runID, stamp(in.Now)); err != nil {
		return BoardCase{}, "", fmt.Errorf("annotate board case: %w", err)
	}
	draftID := c.DraftID
	var files []messageFile
	if in.DraftID != "" && c.Draft == nil {
		// As SetBoardDraft: the linked draft is local from now on.
		if _, _, files, err = makeDraftLocalTx(ctx, tx, c.AccountID, in.DraftID); err != nil {
			return BoardCase{}, "", err
		}
		draftID = in.DraftID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET input_key = ?, draft_id = ?, version = version + 1, updated_at = ? WHERE id = ?`,
		in.InputKey, draftID, nowStamp(), c.ID); err != nil {
		return BoardCase{}, "", fmt.Errorf("annotate board case: %w", err)
	}
	if c, err = boardCaseWhere(ctx, tx, `c.id = ?`, c.ID); err != nil {
		return BoardCase{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return BoardCase{}, "", fmt.Errorf("annotate board case: %w", err)
	}
	s.removeMessageFiles(files)
	return c, runID, nil
}

// currentBoardCaseTx reads a case and checks key against its input key
// derived again from the members.
func (s *Store) currentBoardCaseTx(ctx context.Context, tx *sql.Tx, caseID, key string) (BoardCase, error) {
	c, err := boardCaseWhere(ctx, tx, `c.id = ?`, caseID)
	if err != nil {
		return BoardCase{}, err
	}
	current, _, err := boardInputKeyTx(ctx, tx, c.AccountID, c.ThreadID)
	if err != nil {
		return BoardCase{}, err
	}
	if key == "" || key != current {
		return BoardCase{}, ErrBoardConflict
	}
	return c, nil
}

// BoardCommitment is a row of board_commitments.
type BoardCommitment struct {
	ID          string
	CaseID      string
	AccountID   string
	MessageID   string
	MessageDate time.Time // the message's Date when recorded
	// RepliedAfter: a message of the user's that counts and is dated after
	// it closes the commitment (replied). The Date of the user's newest
	// member when it was recorded, never before MessageDate: one recorded
	// on an older message stays open until the user writes again.
	RepliedAfter time.Time
	Text         string
	Quote        string
	Due          time.Time // zero: none
	State        api.BoardCommitmentState
	ClosedReason string
	Source       string
	RunID        string
	At           time.Time
	ClosedAt     time.Time
	// Existing (AddBoardCommitment only): the commitment was recorded
	// before for the same message and quote and is returned instead of a
	// new one.
	Existing bool
}

// BoardCommitmentInput is a commitment to record (board.commit), cleaned
// and checked by the caller (the quote against the message's own text).
type BoardCommitmentInput struct {
	CaseID    string
	InputKey  string
	MessageID string
	Text      string
	Quote     string
	Due       time.Time // zero: none
	Source    string
	Run       BoardRunRef
	Now       time.Time
	// SameQuote says whether two quotes on the same message name the same
	// promise (core passes board.SameCommitment); nil = equal strings.
	SameQuote func(a, b string) bool
}

const boardCommitmentColumns = `id, case_id, account_id, message_id, message_date, replied_after, text, quote, due_at, state,
	closed_reason, source, run_id, created_at, closed_at`

func scanBoardCommitment(row scanner) (BoardCommitment, error) {
	var k BoardCommitment
	var msgDate, repliedAfter, due, state, created, closed string
	if err := row.Scan(&k.ID, &k.CaseID, &k.AccountID, &k.MessageID, &msgDate, &repliedAfter, &k.Text, &k.Quote, &due, &state,
		&k.ClosedReason, &k.Source, &k.RunID, &created, &closed); err != nil {
		return BoardCommitment{}, err
	}
	k.MessageDate, k.Due, k.At, k.ClosedAt = parseStamp(msgDate), parseStamp(due), parseStamp(created), parseStamp(closed)
	k.RepliedAfter = parseStamp(repliedAfter)
	k.State = api.BoardCommitmentState(state)
	return k, nil
}

// AddBoardCommitment records a commitment and counts it (commitments) in
// the run, in one transaction that checks the input key as
// AnnotateBoardCase does (ErrBoardConflict) and that the message is a
// member of the case that counts and is the user's own (ErrBoardNotMine).
// ErrNotFound for an unknown case. Returns the commitment and the run id.
//
// A commitment is identified by its case, its message and its quote
// (in.SameQuote): when one of the case is already recorded on the same
// message with the same quote, in any state, no row is added and nothing
// is counted; that one is returned (Existing set, run id "") as it is —
// its wording kept, a closed one still closed — except that one open or
// done without a deadline takes in.Due (the case's version then goes up).
func (s *Store) AddBoardCommitment(ctx context.Context, in BoardCommitmentInput) (BoardCommitment, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
	}
	defer tx.Rollback()
	c, err := boardCaseWhere(ctx, tx, `c.id = ?`, in.CaseID)
	if err != nil {
		return BoardCommitment{}, "", err
	}
	current, members, err := boardInputKeyTx(ctx, tx, c.AccountID, c.ThreadID)
	if err != nil {
		return BoardCommitment{}, "", err
	}
	if in.InputKey == "" || in.InputKey != current {
		return BoardCommitment{}, "", ErrBoardConflict
	}
	var msg *BoardMember
	for i := range members {
		if members[i].ID == in.MessageID {
			msg = &members[i]
		}
	}
	if msg == nil || !msg.Mine || !msg.Counts {
		return BoardCommitment{}, "", ErrBoardNotMine
	}
	if prev, found, err := sameBoardCommitmentTx(ctx, tx, c.ID, msg.ID, in.Quote, in.SameQuote); err != nil {
		return BoardCommitment{}, "", err
	} else if found {
		if prev.Due.IsZero() && !in.Due.IsZero() && prev.State != api.CommitmentClosed {
			if _, err := tx.ExecContext(ctx, `UPDATE board_commitments SET due_at = ? WHERE id = ?`, stamp(in.Due), prev.ID); err != nil {
				return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET version = version + 1, updated_at = ? WHERE id = ?`,
				nowStamp(), c.ID); err != nil {
				return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
			}
			prev.Due = parseStamp(stamp(in.Due))
		}
		if err := tx.Commit(); err != nil {
			return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
		}
		prev.Existing = true
		return prev, "", nil
	}
	runID, err := countBoardRunTx(ctx, tx, in.Run, in.Now, "commitments")
	if err != nil {
		return BoardCommitment{}, "", err
	}
	repliedAfter := msg.Date
	for _, m := range members {
		if m.Mine && m.Counts && m.Date.After(repliedAfter) {
			repliedAfter = m.Date
		}
	}
	k := BoardCommitment{
		ID: newID("k_"), CaseID: c.ID, AccountID: c.AccountID, MessageID: msg.ID, MessageDate: msg.Date, RepliedAfter: repliedAfter,
		Text: in.Text, Quote: in.Quote, Due: in.Due, State: api.CommitmentOpen, Source: in.Source, RunID: runID,
		At: parseStamp(stamp(in.Now)),
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO board_commitments (`+boardCommitmentColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', '', ?, ?, ?, '')`,
		k.ID, k.CaseID, k.AccountID, k.MessageID, stamp(k.MessageDate), stamp(k.RepliedAfter), k.Text, k.Quote, optStamp(k.Due),
		k.Source, k.RunID, stamp(in.Now)); err != nil {
		return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BoardCommitment{}, "", fmt.Errorf("add board commitment: %w", err)
	}
	return k, runID, nil
}

// sameBoardCommitmentTx finds the commitment of the case recorded on the
// message with the same quote (same, nil = equal strings): an open one
// first, then a done one, then a closed one, the oldest of each.
func sameBoardCommitmentTx(ctx context.Context, tx *sql.Tx, caseID, messageID, quote string, same func(a, b string) bool) (BoardCommitment, bool, error) {
	if same == nil {
		same = func(a, b string) bool { return a == b }
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+boardCommitmentColumns+` FROM board_commitments WHERE case_id = ? AND message_id = ?
		ORDER BY CASE state WHEN 'open' THEN 0 WHEN 'done' THEN 1 ELSE 2 END, created_at, id`, caseID, messageID)
	if err != nil {
		return BoardCommitment{}, false, fmt.Errorf("add board commitment: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		k, err := scanBoardCommitment(rows)
		if err != nil {
			return BoardCommitment{}, false, fmt.Errorf("add board commitment: %w", err)
		}
		if same(k.Quote, quote) {
			return k, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return BoardCommitment{}, false, fmt.Errorf("add board commitment: %w", err)
	}
	return BoardCommitment{}, false, nil
}

// DedupBoardCommitments merges the open and done commitments that were
// recorded more than once (before AddBoardCommitment looked for them):
// per case and message, those whose quotes are the same (same, nil =
// equal strings; one that matches any of a group joins it) are one
// commitment. The oldest of each group stays with its text; it is done
// when any of the group is (closed at the earliest such time), takes the
// first deadline of the group when it has none, and the others are
// deleted. Closed ones are left alone. In one transaction; the versions
// of the cases concerned go up. Returns the threads of those cases by
// account (none: nothing to merge). Running it again changes nothing.
func (s *Store) DedupBoardCommitments(ctx context.Context, same func(a, b string) bool) (map[string][]string, error) {
	if same == nil {
		same = func(a, b string) bool { return a == b }
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dedup board commitments: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+boardCommitmentColumns+` FROM board_commitments WHERE state IN ('open', 'done')
		ORDER BY case_id, message_id, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("dedup board commitments: %w", err)
	}
	var all []BoardCommitment
	for rows.Next() {
		k, err := scanBoardCommitment(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("dedup board commitments: %w", err)
		}
		all = append(all, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dedup board commitments: %w", err)
	}
	cases := map[string]bool{}
	for start := 0; start < len(all); {
		end := start
		for end < len(all) && all[end].CaseID == all[start].CaseID && all[end].MessageID == all[start].MessageID {
			end++
		}
		var groups [][]BoardCommitment
		for _, k := range all[start:end] {
			joined := false
			for g := range groups {
				for _, m := range groups[g] {
					if same(m.Quote, k.Quote) {
						groups[g] = append(groups[g], k)
						joined = true
						break
					}
				}
				if joined {
					break
				}
			}
			if !joined {
				groups = append(groups, []BoardCommitment{k})
			}
		}
		for _, g := range groups {
			if len(g) < 2 {
				continue
			}
			if err := mergeBoardCommitmentsTx(ctx, tx, g); err != nil {
				return nil, err
			}
			cases[g[0].CaseID] = true
		}
		start = end
	}
	out := map[string][]string{}
	for id := range cases {
		var account, thread string
		err := tx.QueryRowContext(ctx, `UPDATE board_cases SET version = version + 1, updated_at = ? WHERE id = ?
			RETURNING account_id, thread_id`, nowStamp(), id).Scan(&account, &thread)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("dedup board commitments: %w", err)
		}
		if err == nil {
			out[account] = append(out[account], thread)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("dedup board commitments: %w", err)
	}
	for _, threads := range out {
		sort.Strings(threads)
	}
	return out, nil
}

// mergeBoardCommitmentsTx keeps the first (oldest) of a group of the same
// commitment and deletes the others (DedupBoardCommitments).
func mergeBoardCommitmentsTx(ctx context.Context, tx *sql.Tx, g []BoardCommitment) error {
	keep := g[0]
	state, closedAt, due := keep.State, keep.ClosedAt, keep.Due
	for _, k := range g[1:] {
		if k.State == api.CommitmentDone && (state != api.CommitmentDone || k.ClosedAt.Before(closedAt)) {
			state, closedAt = api.CommitmentDone, k.ClosedAt
		}
		if due.IsZero() {
			due = k.Due
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM board_commitments WHERE id = ?`, k.ID); err != nil {
			return fmt.Errorf("dedup board commitments: %w", err)
		}
	}
	closed := ""
	if state == api.CommitmentDone {
		closed = stamp(closedAt)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_commitments SET state = ?, closed_reason = '', closed_at = ?, due_at = ? WHERE id = ?`,
		string(state), closed, optStamp(due), keep.ID); err != nil {
		return fmt.Errorf("dedup board commitments: %w", err)
	}
	return nil
}

// SetBoardCommitment ticks a commitment off (done) or opens it again
// (from done or closed). ErrNotFound for an unknown commitment.
func (s *Store) SetBoardCommitment(ctx context.Context, id string, done bool, now time.Time) (BoardCommitment, error) {
	var err error
	if done {
		_, err = s.db.ExecContext(ctx, `UPDATE board_commitments SET state = 'done', closed_reason = '', closed_at = ?
			WHERE id = ? AND state != 'done'`, stamp(now), id)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE board_commitments SET state = 'open', closed_reason = '', closed_at = ''
			WHERE id = ? AND state != 'open'`, id)
	}
	if err != nil {
		return BoardCommitment{}, fmt.Errorf("set board commitment: %w", err)
	}
	return s.GetBoardCommitment(ctx, id)
}

// GetBoardCommitment returns one commitment; ErrNotFound otherwise.
func (s *Store) GetBoardCommitment(ctx context.Context, id string) (BoardCommitment, error) {
	k, err := scanBoardCommitment(s.db.QueryRowContext(ctx, `SELECT `+boardCommitmentColumns+` FROM board_commitments WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return BoardCommitment{}, ErrNotFound
	case err != nil:
		return BoardCommitment{}, fmt.Errorf("get board commitment: %w", err)
	}
	return k, nil
}

// BoardCommitments lists every commitment of a case, oldest first.
func (s *Store) BoardCommitments(ctx context.Context, caseID string) ([]BoardCommitment, error) {
	return s.boardCommitmentsOf(ctx, []string{caseID}, false)
}

// boardCommitmentsOf lists the (open) commitments of the cases, oldest
// first; never nil.
func (s *Store) boardCommitmentsOf(ctx context.Context, caseIDs []string, open bool) ([]BoardCommitment, error) {
	out := []BoardCommitment{}
	cond := ""
	if open {
		cond = ` AND state = 'open'`
	}
	for _, chunk := range chunkStrings(caseIDs, linkChunk) {
		rows, err := s.db.QueryContext(ctx, `SELECT `+boardCommitmentColumns+` FROM board_commitments WHERE case_id IN (`+
			inPlaceholders(len(chunk))+`)`+cond+` ORDER BY created_at, id`, toAny(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("board commitments: %w", err)
		}
		for rows.Next() {
			k, err := scanBoardCommitment(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("board commitments: %w", err)
			}
			out = append(out, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("board commitments: %w", err)
		}
	}
	return out, nil
}

// BoardRun is a row of board_runs.
type BoardRun struct {
	ID          string
	Trigger     api.BoardTrigger
	Source      string
	Day         string // external runs
	StartedAt   time.Time
	EndedAt     time.Time // zero while it runs; an external run's: its latest call
	Annotated   int
	Commitments int
	Rejected    int
	Error       api.BoardRunError // "" after a success
	Usage       *api.BoardUsage   // nil = unknown (EndBoardRun)
}

const boardRunColumns = `id, trigger, source, day, started_at, ended_at, annotated, commitments, rejected, error,
	input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens`

func scanBoardRun(row scanner) (BoardRun, error) {
	var r BoardRun
	var trigger, started, ended, errClass string
	var in, out, created, read sql.NullInt64
	if err := row.Scan(&r.ID, &trigger, &r.Source, &r.Day, &started, &ended, &r.Annotated, &r.Commitments, &r.Rejected, &errClass,
		&in, &out, &created, &read); err != nil {
		return BoardRun{}, err
	}
	if in.Valid {
		r.Usage = &api.BoardUsage{InputTokens: in.Int64, OutputTokens: out.Int64, CacheCreationInputTokens: created.Int64,
			CacheReadInputTokens: read.Int64}
	}
	r.Trigger, r.Error = api.BoardTrigger(trigger), api.BoardRunError(errClass)
	r.StartedAt, r.EndedAt = parseStamp(started), parseStamp(ended)
	return r, nil
}

// StartBoardRun records a run a client starts (trigger manual or auto)
// and returns its id.
func (s *Store) StartBoardRun(ctx context.Context, trigger api.BoardTrigger, source string, now time.Time) (string, error) {
	if trigger != api.TriggerManual && trigger != api.TriggerAuto {
		return "", fmt.Errorf("start board run: trigger %q", trigger)
	}
	id := newID("r_")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO board_runs (id, trigger, source, started_at) VALUES (?, ?, ?, ?)`,
		id, string(trigger), source, stamp(now)); err != nil {
		return "", fmt.Errorf("start board run: %w", err)
	}
	return id, nil
}

// EndBoardRun ends a run with an error class ("" = success; a class the
// store does not know is stored as failed) and the token usage the client
// reported (nil = unknown: the run keeps none; each counter is clamped to
// 0..api.MaxBoardUsageTokens). Ending a run that has ended, or an
// external one, changes nothing, its usage included. ErrNotFound for an
// unknown id.
func (s *Store) EndBoardRun(ctx context.Context, id string, errClass api.BoardRunError, usage *api.BoardUsage, now time.Time) error {
	switch errClass {
	case "", api.RunCancelled, api.RunTimeout, api.RunSignedOut, api.RunFailed:
	default:
		errClass = api.RunFailed
	}
	var in, out, created, read any // NULL without usage
	if usage != nil {
		u := usage.Clamped()
		in, out, created, read = u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens
	}
	res, err := s.db.ExecContext(ctx, `UPDATE board_runs SET ended_at = ?, error = ?,
			input_tokens = ?, output_tokens = ?, cache_creation_input_tokens = ?, cache_read_input_tokens = ?
		WHERE id = ? AND ended_at = '' AND trigger != 'external'`,
		stamp(now), string(errClass), in, out, created, read, id)
	if err != nil {
		return fmt.Errorf("end board run: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.GetBoardRun(ctx, id); err != nil {
		return err
	}
	return nil
}

// GetBoardRun returns one run; ErrNotFound otherwise.
func (s *Store) GetBoardRun(ctx context.Context, id string) (BoardRun, error) {
	r, err := scanBoardRun(s.db.QueryRowContext(ctx, `SELECT `+boardRunColumns+` FROM board_runs WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return BoardRun{}, ErrNotFound
	case err != nil:
		return BoardRun{}, fmt.Errorf("get board run: %w", err)
	}
	return r, nil
}

// CountBoardRejected counts a refused annotate or commit call (rejected)
// in the run ref names; returns the run id.
func (s *Store) CountBoardRejected(ctx context.Context, ref BoardRunRef, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("count board run: %w", err)
	}
	defer tx.Rollback()
	id, err := countBoardRunTx(ctx, tx, ref, now, "rejected")
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("count board run: %w", err)
	}
	return id, nil
}

// countBoardRunTx adds one to the counter column (annotated, commitments
// or rejected) of the run ref names (BoardRunRef) and returns its id. An
// external run's ended_at follows its latest call.
func countBoardRunTx(ctx context.Context, tx *sql.Tx, ref BoardRunRef, now time.Time, counter string) (string, error) {
	switch counter {
	case "annotated", "commitments", "rejected":
	default:
		return "", fmt.Errorf("count board run: counter %q", counter)
	}
	if ref.RunID != "" {
		res, err := tx.ExecContext(ctx, `UPDATE board_runs SET `+counter+` = `+counter+` + 1
			WHERE id = ? AND ended_at = '' AND trigger != 'external'`, ref.RunID)
		if err != nil {
			return "", fmt.Errorf("count board run: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return ref.RunID, nil
		}
	}
	day := ref.Day
	if day == "" {
		day = now.UTC().Format(time.DateOnly)
	}
	n := stamp(now)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO board_runs (id, trigger, source, day, started_at, ended_at)
		VALUES (?, 'external', ?, ?, ?, ?)`, newID("r_"), ref.Source, day, n, n); err != nil {
		return "", fmt.Errorf("count board run: %w", err)
	}
	var id string
	if err := tx.QueryRowContext(ctx, `UPDATE board_runs SET `+counter+` = `+counter+` + 1, ended_at = ?
		WHERE trigger = 'external' AND source = ? AND day = ? RETURNING id`, n, ref.Source, day).Scan(&id); err != nil {
		return "", fmt.Errorf("count board run: %w", err)
	}
	return id, nil
}

// CloseOpenBoardRuns ends with failed the manual and auto runs still open
// that started before (the daemon's start: now; then now − 2 h).
func (s *Store) CloseOpenBoardRuns(ctx context.Context, startedBefore, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE board_runs SET ended_at = ?, error = 'failed'
		WHERE ended_at = '' AND trigger != 'external' AND started_at < ?`, stamp(now), stamp(startedBefore))
	if err != nil {
		return 0, fmt.Errorf("close open board runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneBoardRuns deletes the runs that started before (docs/api.md: kept
// for 90 days).
func (s *Store) PruneBoardRuns(ctx context.Context, startedBefore time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM board_runs WHERE started_at < ?`, stamp(startedBefore))
	if err != nil {
		return 0, fmt.Errorf("prune board runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// BoardRunUsage returns the token usage summed over the runs that ended
// at or after since (by ended_at) and carry usage, and how many those are;
// a zero api.BoardUsageTotal when none. Runs carry usage only when a
// client ended them with board.runEnd, so external runs never count. The
// partial index board_runs_usage holds only runs with usage, and
// PruneBoardRuns bounds the table to 90 days of runs.
func (s *Store) BoardRunUsage(ctx context.Context, since time.Time) (api.BoardUsageTotal, error) {
	var t api.BoardUsageTotal
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_input_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0)
		FROM board_runs WHERE input_tokens IS NOT NULL AND ended_at >= ?`, stamp(since)).Scan(&t.Runs,
		&t.InputTokens, &t.OutputTokens, &t.CacheCreationInputTokens, &t.CacheReadInputTokens); err != nil {
		return api.BoardUsageTotal{}, fmt.Errorf("board run usage: %w", err)
	}
	return t, nil
}

// BoardRunStats are the run figures of board.list's triage.
type BoardRunStats struct {
	// LastRun is the run with the latest activity, any trigger: a run
	// still open first (the newest by start), else the latest ended_at (an
	// external run's: its latest call). Nil before the first.
	LastRun *BoardRun
	// AnnotatedAuto sums annotated over the auto runs started at or after
	// the day start the caller passed.
	AnnotatedAuto int
}

// BoardRunStats reads the run of the latest activity and the cases
// annotated by auto runs started since dayStart (the caller's local
// midnight).
func (s *Store) BoardRunStats(ctx context.Context, dayStart time.Time) (BoardRunStats, error) {
	var out BoardRunStats
	r, err := scanBoardRun(s.db.QueryRowContext(ctx, `SELECT `+boardRunColumns+` FROM board_runs
		ORDER BY ended_at = '' DESC, max(started_at, ended_at) DESC, id DESC LIMIT 1`))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return out, fmt.Errorf("board run stats: %w", err)
	default:
		out.LastRun = &r
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(annotated), 0) FROM board_runs WHERE trigger = 'auto' AND started_at >= ?`,
		stamp(dayStart)).Scan(&out.AnnotatedAuto); err != nil {
		return out, fmt.Errorf("board run stats: %w", err)
	}
	return out, nil
}
