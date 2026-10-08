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
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// BoardCase is a row of board_cases with its annotation and linked draft.
// The store's own type (core projects it onto api.BoardCase): it keeps the
// raw time stamps as times, the zero time for "none".
type BoardCase struct {
	ID        string
	AccountID string
	ThreadID  string

	// Derived (DrainBoard).
	RuleState       api.BoardState
	RuleReason      api.BoardReason
	RulesVersion    string
	InputKey        string // the current input key: an annotation of another is stale
	Subject         string
	Snippet         string
	Person          api.Address
	Date            time.Time
	Unread          bool
	HasAttachments  bool
	CanArchive      bool
	MessageCount    int
	ReplyMessageID  string
	ReplyFolderID   string
	LatestMessageID string
	Issue           *BoardIssueInfo // a jira account's case
	ComputedAt      time.Time

	// The user's.
	UserState   api.BoardState // "" = automatic
	UserStateAt time.Time
	DoneAt      time.Time
	// RemindAt stays set after it came due (Reminded): the case is live
	// again and kept until the user acts on it (done, another remind, a
	// state, unflag) or inbound mail that counts arrives.
	RemindAt time.Time
	Reminded bool // RemindAt came due and the version went up (ClearDueBoardReminds)

	// OrphanedAt: since when the thread has had no visible member (zero:
	// it has some). Such a case is off the board and pruned after
	// BoardOrphanGrace unless members come back.
	OrphanedAt time.Time

	Annotation *BoardAnnotation // nil: none (Stale says whether it is current)
	// DraftID is the draft linked to the case (board.setDraft, or an
	// annotation's draftId), "" = none; it stays after the draft is gone.
	DraftID   string
	Draft     *BoardDraft // the linked draft while it exists in the case's account
	Version   int64
	CreatedAt time.Time

	membersKey string
	// done_seen: the Message-IDs the case had when marked done, or while a
	// remind is set, the time it was set (boardRemindMark) and the
	// Message-IDs the case had then
	doneSeen  string
	memberIDs string // member_ids: the Message-IDs of its newest members
}

// SeenAtDone says whether the Message-ID (bare or in angle brackets) was
// one of the inbound members that counted when the case was marked done,
// or when its remind was set: a later copy of it (a move by another
// client stores the message anew) neither reopens the case nor ends the
// remind. False when the case is neither done nor snoozed.
func (c BoardCase) SeenAtDone(rfcMessageID string) bool {
	id := boardSeenID(rfcMessageID)
	return id != "" && c.doneSeen != "" && strings.Contains(c.doneSeen, "\n"+id+"\n")
}

// boardRemindMark starts the first line of done_seen while a remind is
// set: the time it was set follows (the store's clock, as StoredAt), then
// a line per Message-ID the case had then. done_seen holds nothing else
// meanwhile: a remind clears done (migration 0017 has no column of its
// own for it; done and a remind never stand together).
const boardRemindMark = "~remind "

// boardRemindSeen is done_seen for a remind set at set over the members:
// the mark with the time, then the inbound members' Message-IDs as
// boardDoneSeen lists them.
func boardRemindSeen(set time.Time, members []BoardMember) string {
	ids := []string{boardRemindMark + stamp(set)}
	for i := len(members) - 1; i >= 0; i-- {
		if m := members[i]; m.Counts && !m.Mine {
			ids = append(ids, m.RFCMessageID)
		}
	}
	return joinDoneSeen(ids)
}

// RemindSetAt is when the case's remind was set, zero when it has none,
// is done, or was set before the store recorded the time (an older
// daemon; a thread merge): new mail then does not end it.
func (c BoardCase) RemindSetAt() time.Time {
	if c.RemindAt.IsZero() || !c.DoneAt.IsZero() {
		return time.Time{}
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(c.doneSeen, "\n"), "\n")
	at, ok := strings.CutPrefix(first, boardRemindMark)
	if !ok {
		return time.Time{}
	}
	return parseStamp(at)
}

// RemindedAt is when the remind came due (RemindAt) while the case is
// live after it, zero otherwise: until the user acts on the case or new
// mail that counts arrives.
func (c BoardCase) RemindedAt(now time.Time) time.Time {
	if !c.Reminded || c.RemindAt.IsZero() || c.Visibility(now) != api.BoardLive {
		return time.Time{}
	}
	return c.RemindAt
}

// BoardIssueInfo is the issue behind a case, as the case last saw it.
type BoardIssueInfo struct {
	Key            string // "" while the stored issue has lost its key to another (store.PutIssue)
	Status         string
	StatusCategory api.IssueStatusCategory
}

func (c BoardCase) issueField(i int) string {
	if c.Issue == nil {
		return ""
	}
	return [...]string{c.Issue.Key, c.Issue.Status, string(c.Issue.StatusCategory)}[i]
}

// Visibility derives where the case is listed at now. A remind that came
// due is live even when the clock went back before RemindAt since: no
// timer would end that snooze.
func (c BoardCase) Visibility(now time.Time) api.BoardVisibility {
	switch {
	case !c.DoneAt.IsZero():
		return api.BoardDone
	case !c.RemindAt.IsZero() && !c.Reminded && c.RemindAt.After(now):
		return api.BoardSnoozed
	}
	return api.BoardLive
}

// BoardAnnotation is an assistant's annotation of a case.
type BoardAnnotation struct {
	InputKey string
	State    api.BoardState // "" = left to the rules
	Title    string
	Summary  string
	Why      string
	Tasks    []string // never nil
	Due      *BoardDue
	Source   string
	RunID    string
	At       time.Time
	// Stale: InputKey is not the case's current one.
	Stale bool
}

// BoardDue is an annotation's deadline with its quote.
type BoardDue struct {
	At        time.Time
	Quote     string
	MessageID string
}

// BoardDraft is a draft linked to a case.
type BoardDraft struct {
	DraftID string
	Text    string // at most api.MaxBoardDraftTextBytes, cut at a character boundary
	Updated time.Time
}

// boardCaseSelect reads a case with its annotation and draft: c is
// board_cases, a board_annotations, d drafts.
const boardCaseSelect = `SELECT c.id, c.account_id, c.thread_id, c.rule_state, c.rule_reason, c.rules_version,
		c.input_key, c.members_key, c.subject, c.snippet, c.person_json, c.date, c.unread, c.has_attachments,
		c.can_archive, c.message_count, c.reply_message_id, c.reply_folder_id, c.latest_message_id,
		c.issue_key, c.issue_status, c.issue_status_category, c.computed_at,
		c.user_state, c.user_state_at, c.done_at, c.remind_at, c.reminded, c.done_seen, c.orphaned_at, c.member_ids, c.draft_id, c.version, c.created_at,
		a.case_id IS NOT NULL, COALESCE(a.input_key, ''), COALESCE(a.state, ''), COALESCE(a.title, ''),
		COALESCE(a.summary, ''), COALESCE(a.why, ''), COALESCE(a.tasks_json, '[]'), COALESCE(a.due_at, ''),
		COALESCE(a.due_quote, ''), COALESCE(a.due_message_id, ''), COALESCE(a.source, ''),
		COALESCE(a.run_id, ''), COALESCE(a.created_at, ''),
		d.id IS NOT NULL, COALESCE(substr(d.text_body, 1, ?), ''), COALESCE(d.updated_at, '')
	FROM board_cases c
	LEFT JOIN board_annotations a ON a.case_id = c.id
	LEFT JOIN drafts d ON c.draft_id != '' AND d.id = c.draft_id AND d.account_id = c.account_id`

func scanBoardCase(row scanner) (BoardCase, error) {
	var c BoardCase
	var ruleState, ruleReason, person, date, issueKey, issueStatus, issueCat, computed string
	var userState, userAt, doneAt, remindAt, orphaned, created string
	var unread, att, archive, reminded, hasAnn, hasDraft int
	var aKey, aState, aTitle, aSummary, aWhy, aTasks, aDue, aQuote, aDueMsg, aSource, aRun, aAt string
	var dText, dUpdated string
	if err := row.Scan(&c.ID, &c.AccountID, &c.ThreadID, &ruleState, &ruleReason, &c.RulesVersion,
		&c.InputKey, &c.membersKey, &c.Subject, &c.Snippet, &person, &date, &unread, &att,
		&archive, &c.MessageCount, &c.ReplyMessageID, &c.ReplyFolderID, &c.LatestMessageID,
		&issueKey, &issueStatus, &issueCat, &computed,
		&userState, &userAt, &doneAt, &remindAt, &reminded, &c.doneSeen, &orphaned, &c.memberIDs, &c.DraftID, &c.Version, &created,
		&hasAnn, &aKey, &aState, &aTitle, &aSummary, &aWhy, &aTasks, &aDue, &aQuote, &aDueMsg, &aSource, &aRun, &aAt,
		&hasDraft, &dText, &dUpdated); err != nil {
		return BoardCase{}, err
	}
	c.RuleState, c.RuleReason, c.UserState = api.BoardState(ruleState), api.BoardReason(ruleReason), api.BoardState(userState)
	if err := json.Unmarshal([]byte(person), &c.Person); err != nil {
		return BoardCase{}, fmt.Errorf("decode person of case %s: %w", c.ID, err)
	}
	c.Date, c.ComputedAt, c.CreatedAt = parseStamp(date), parseStamp(computed), parseStamp(created)
	c.UserStateAt, c.DoneAt, c.RemindAt = parseStamp(userAt), parseStamp(doneAt), parseStamp(remindAt)
	c.Reminded, c.OrphanedAt = reminded != 0, parseStamp(orphaned)
	c.Unread, c.HasAttachments, c.CanArchive = unread != 0, att != 0, archive != 0
	if issueKey != "" || issueStatus != "" || issueCat != "" {
		c.Issue = &BoardIssueInfo{Key: issueKey, Status: issueStatus, StatusCategory: api.IssueStatusCategory(issueCat)}
	}
	if hasAnn != 0 {
		a := &BoardAnnotation{InputKey: aKey, State: api.BoardState(aState), Title: aTitle, Summary: aSummary, Why: aWhy,
			Source: aSource, RunID: aRun, At: parseStamp(aAt), Stale: aKey != c.InputKey}
		if err := json.Unmarshal([]byte(aTasks), &a.Tasks); err != nil {
			return BoardCase{}, fmt.Errorf("decode tasks of case %s: %w", c.ID, err)
		}
		if a.Tasks == nil {
			a.Tasks = []string{}
		}
		if aDue != "" {
			a.Due = &BoardDue{At: parseStamp(aDue), Quote: aQuote, MessageID: aDueMsg}
		}
		c.Annotation = a
	}
	if hasDraft != 0 {
		c.Draft = &BoardDraft{DraftID: c.DraftID, Text: CutUTF8(dText, api.MaxBoardDraftTextBytes), Updated: parseStamp(dUpdated)}
	}
	return c, nil
}

// boardDraftChars is the substr length of a draft's text before the byte
// cut: no character takes more than 4 bytes.
const boardDraftChars = api.MaxBoardDraftTextBytes

func boardCaseWhere(ctx context.Context, q querier, where string, args ...any) (BoardCase, error) {
	rows, err := q.QueryContext(ctx, boardCaseSelect+` WHERE `+where, append([]any{boardDraftChars}, args...)...)
	if err != nil {
		return BoardCase{}, fmt.Errorf("board case: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return BoardCase{}, fmt.Errorf("board case: %w", err)
		}
		return BoardCase{}, ErrNotFound
	}
	c, err := scanBoardCase(rows)
	if err != nil {
		return BoardCase{}, fmt.Errorf("board case: %w", err)
	}
	return c, nil
}

func boardCaseByThreadTx(ctx context.Context, q querier, accountID, threadID string) (BoardCase, error) {
	return boardCaseWhere(ctx, q, `c.account_id = ? AND c.thread_id = ?`, accountID, threadID)
}

// GetBoardCase returns one case with its annotation and draft; ErrNotFound
// when there is none.
func (s *Store) GetBoardCase(ctx context.Context, id string) (BoardCase, error) {
	if id == "" {
		return BoardCase{}, ErrNotFound
	}
	return boardCaseWhere(ctx, s.db, `c.id = ?`, id)
}

// BoardCaseByThread returns the case of an account's thread; ErrNotFound
// when the thread is no case.
func (s *Store) BoardCaseByThread(ctx context.Context, accountID, threadID string) (BoardCase, error) {
	return boardCaseByThreadTx(ctx, s.db, accountID, threadID)
}

// BoardWindowDays are how long cases of each state stay listed, in days
// from their Date (api.BoardWindows).
type BoardWindowDays struct {
	Hot, You, Them, Info int
}

// BoardListQuery selects the cases of board.list.
type BoardListQuery struct {
	AccountIDs []string // the accounts to list; none = no cases
	Now        time.Time
	Windows    BoardWindowDays
	// DoneDays is how long a done case stays listed after DoneAt; 0 = 30.
	DoneDays int
	// Assistant: a current annotation's state counts for the window (the
	// board's assistant preference).
	Assistant bool
	Limit     int // 0 = api.MaxBoardCases
}

// BoardListing is what ListBoard found.
type BoardListing struct {
	Cases []BoardCase // newest Date first; never nil
	// Commitments are the open commitments of the live cases in Cases,
	// oldest first; never nil.
	Commitments []BoardCommitment
	Truncated   bool // more cases than the limit
}

// boardShown is the condition of a case on the board at a time and its
// arguments: its thread has visible members (not orphaned), and the
// effective state's window from Date holds, or something keeps it (a user
// state, a remind, ahead or come due, an open commitment, a current
// annotation's future deadline while the assistant preference is on, a
// linked draft that exists), or it was
// marked done within doneSince or links a draft that exists (a done case
// keeps its suggested reply until the prune drops the link at the end of
// the done retention, PruneBoardCases).
// c is board_cases and a board_annotations (LEFT JOIN).
func boardShown(now time.Time, w BoardWindowDays, doneDays int, assistant bool) (string, []any) {
	if doneDays <= 0 {
		doneDays = 30
	}
	day := 24 * time.Hour
	since := func(days int) string { return stamp(now.Add(-time.Duration(days) * day)) }
	n := stamp(now)
	cond := `(c.orphaned_at = '' AND ((c.done_at = '' AND (c.user_state != ''
			OR c.remind_at != ''
			OR EXISTS (SELECT 1 FROM board_commitments k WHERE k.case_id = c.id AND k.state = 'open')
			OR ` + boardHasDraft + `
			OR (? AND a.due_at IS NOT NULL AND a.due_at != '' AND a.due_at > ? AND a.input_key = c.input_key)
			OR c.date >= CASE ` + boardEffectiveState + `
				WHEN 'hot' THEN ? WHEN 'you' THEN ? WHEN 'them' THEN ? ELSE ? END))
		OR (c.done_at != '' AND (c.done_at >= ? OR ` + boardHasDraft + `))))`
	return cond, []any{boolInt(assistant), n, boolInt(assistant), since(w.Hot), since(w.You), since(w.Them), since(w.Info), since(doneDays)}
}

// boardHasDraft: the case c links a draft that exists.
const boardHasDraft = `EXISTS (SELECT 1 FROM drafts d WHERE c.draft_id != '' AND d.id = c.draft_id AND d.account_id = c.account_id)`

// boardEffectiveState is the state a client shows (its argument: whether
// the assistant preference is on).
const boardEffectiveState = `CASE WHEN c.user_state != '' THEN c.user_state
		WHEN ? AND a.state IS NOT NULL AND a.state != '' AND a.input_key = c.input_key THEN a.state
		ELSE c.rule_state END`

// ListBoard lists the cases on the board of the accounts: live, snoozed
// and recently done ones (boardShown), newest Date first, at most the
// limit, with the open commitments of the live ones. One read.
func (s *Store) ListBoard(ctx context.Context, q BoardListQuery) (BoardListing, error) {
	out := BoardListing{Cases: []BoardCase{}, Commitments: []BoardCommitment{}}
	if len(q.AccountIDs) == 0 {
		return out, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = api.MaxBoardCases
	}
	shown, shownArgs := boardShown(q.Now, q.Windows, q.DoneDays, q.Assistant)
	args := []any{boardDraftChars}
	args = append(args, toAny(q.AccountIDs)...)
	args = append(args, shownArgs...)
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, boardCaseSelect+` WHERE c.account_id IN (`+inPlaceholders(len(q.AccountIDs))+`) AND `+
		shown+` ORDER BY c.date DESC, c.id DESC LIMIT ?`, args...)
	if err != nil {
		return out, fmt.Errorf("list board: %w", err)
	}
	for rows.Next() {
		c, err := scanBoardCase(rows)
		if err != nil {
			rows.Close()
			return out, fmt.Errorf("list board: %w", err)
		}
		out.Cases = append(out.Cases, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("list board: %w", err)
	}
	if len(out.Cases) > limit {
		out.Cases, out.Truncated = out.Cases[:limit], true
	}
	var live []string
	for _, c := range out.Cases {
		if c.Visibility(q.Now) == api.BoardLive {
			live = append(live, c.ID)
		}
	}
	if out.Commitments, err = s.boardCommitmentsOf(ctx, live, true); err != nil {
		return out, err
	}
	return out, nil
}

// boardBumpOnly is what an apply of setBoardUser returns when only the
// version goes up (a change outside board_cases, such as the draft link).
const boardBumpOnly = "\x00bump"

// setBoardUser runs one change of the user's columns: it reads the case,
// lets apply return the SET clause and its arguments ("" = nothing to
// change, boardBumpOnly = only the version), bumps the version and
// returns the case as stored.
func (s *Store) setBoardUser(ctx context.Context, id, what string, apply func(tx *sql.Tx, c BoardCase) (string, []any, error)) (BoardCase, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BoardCase{}, fmt.Errorf("%s: %w", what, err)
	}
	defer tx.Rollback()
	c, err := boardCaseWhere(ctx, tx, `c.id = ?`, id)
	if err != nil {
		return BoardCase{}, err
	}
	set, args, err := apply(tx, c)
	if err != nil {
		return BoardCase{}, err
	}
	if set != "" {
		args = append(args, nowStamp(), id)
		if set == boardBumpOnly {
			set = ""
		} else {
			set += ", "
		}
		if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET `+set+`version = version + 1, updated_at = ? WHERE id = ?`, args...); err != nil {
			return BoardCase{}, fmt.Errorf("%s: %w", what, err)
		}
		if c, err = boardCaseWhere(ctx, tx, `c.id = ?`, id); err != nil {
			return BoardCase{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return BoardCase{}, fmt.Errorf("%s: %w", what, err)
	}
	return c, nil
}

// SetBoardUserState sets the user's state of a case ("" = back to
// automatic). ErrNotFound for an unknown case; an invalid state is an
// error. Unchanged: the case as it is, version kept.
func (s *Store) SetBoardUserState(ctx context.Context, id string, state api.BoardState, now time.Time) (BoardCase, error) {
	if state != "" && !state.Valid() {
		return BoardCase{}, fmt.Errorf("set board state: unknown state %q", state)
	}
	return s.setBoardUser(ctx, id, "set board state", func(_ *sql.Tx, c BoardCase) (string, []any, error) {
		switch {
		case c.UserState == state && !c.Reminded:
			return "", nil, nil
		case c.UserState == state:
			return boardEndReminded, nil, nil
		}
		set := `user_state = ?, user_state_at = ?`
		if c.Reminded {
			set += ", " + boardEndReminded
		}
		return set, []any{string(state), optStamp(now)}, nil
	})
}

// boardEndReminded is the SET clause that ends a remind that came due (the
// user acted on the case): it no longer keeps the case, nor marks it as
// reminded. done_seen held the remind's Message-IDs (a remind and done
// never stand together).
const boardEndReminded = `remind_at = '', reminded = 0, done_seen = CASE WHEN done_at = '' THEN '' ELSE done_seen END`

// ClearBoardReminded ends a remind of the case that came due (board.unflag:
// the user acted on it); nothing else changes, and nothing when it has
// none. ErrNotFound for an unknown case.
func (s *Store) ClearBoardReminded(ctx context.Context, id string) (BoardCase, error) {
	return s.setBoardUser(ctx, id, "clear board remind", func(_ *sql.Tx, c BoardCase) (string, []any, error) {
		if !c.Reminded {
			return "", nil, nil
		}
		return boardEndReminded, nil, nil
	})
}

// loadBoardSeenTx reads of a thread's newest visible members (as
// loadBoardMembersTx, oldest first) only what done_seen records: the
// Message-ID and what decides whether a member counts and is the user's
// (boardCounts). Nothing of it is decoded, so a member row whose
// addresses, flags or attachments do not decode cannot fail board.done or
// board.remind; the marker names every member that counts.
func loadBoardSeenTx(ctx context.Context, q querier, accountID, threadID string) ([]BoardMember, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.rfc_message_id, f.role, COALESCE(i.kind, ''), m.bulk`+boardMembersFrom+
		` ORDER BY `+boardMemberOrder+` DESC, m.id DESC LIMIT ?`, accountID, threadID, boardThreadRows)
	if err != nil {
		return nil, fmt.Errorf("board: thread members: %w", err)
	}
	defer rows.Close()
	var out []BoardMember
	for rows.Next() {
		var m BoardMember
		var role, kind string
		if err := rows.Scan(&m.RFCMessageID, &role, &kind, &m.Bulk); err != nil {
			return nil, fmt.Errorf("board: scan member: %w", err)
		}
		m.Role, m.ItemKind = api.FolderRole(role), api.IssueItemKind(kind)
		m.Mine = m.Role == api.RoleSent || m.Role == api.RoleOutbox
		m.Counts = boardCounts(m)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: thread members: %w", err)
	}
	slices.Reverse(out) // oldest first
	return out, nil
}

// reopenDoneCommitmentsTx opens again the commitments that marking the case
// done closed (closed_reason done, at or after its done time): the case is
// live again without them having been kept.
func reopenDoneCommitmentsTx(ctx context.Context, tx *sql.Tx, c BoardCase) error {
	_, err := reopenDoneCommitmentsCountTx(ctx, tx, c)
	return err
}

// reopenDoneCommitmentsCountTx is reopenDoneCommitmentsTx, reporting how
// many it opened (DrainBoard counts them as open for kept).
func reopenDoneCommitmentsCountTx(ctx context.Context, tx *sql.Tx, c BoardCase) (int, error) {
	if c.DoneAt.IsZero() {
		return 0, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE board_commitments SET state = 'open', closed_reason = '', closed_at = ''
		WHERE case_id = ? AND state = 'closed' AND closed_reason = ? AND closed_at >= ?`,
		c.ID, api.CommitmentClosedDone, stamp(c.DoneAt))
	if err != nil {
		return 0, fmt.Errorf("board: reopen the commitments done closed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("board: reopen the commitments done closed: %w", err)
	}
	return int(n), nil
}

// SetBoardDone marks a case done at now (clearing a remind and closing its
// open commitments with reason done) or live again (opening again the
// commitments done closed). Done records the Message-IDs of the case's
// inbound members (BoardCase.SeenAtDone). Either ends a remind that came
// due. ErrNotFound for an unknown case.
func (s *Store) SetBoardDone(ctx context.Context, id string, done bool, now time.Time) (BoardCase, error) {
	return s.setBoardUser(ctx, id, "set board done", func(tx *sql.Tx, c BoardCase) (string, []any, error) {
		if !done {
			if c.DoneAt.IsZero() {
				if c.Reminded {
					return boardEndReminded, nil, nil
				}
				return "", nil, nil
			}
			if err := reopenDoneCommitmentsTx(ctx, tx, c); err != nil {
				return "", nil, err
			}
			return `done_at = '', done_seen = ''`, nil, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE board_commitments SET state = 'closed', closed_reason = ?, closed_at = ?
			WHERE case_id = ? AND state = 'open'`, api.CommitmentClosedDone, stamp(now), id); err != nil {
			return "", nil, fmt.Errorf("set board done: %w", err)
		}
		if !c.DoneAt.IsZero() && c.RemindAt.IsZero() {
			return "", nil, nil
		}
		members, err := loadBoardSeenTx(ctx, tx, c.AccountID, c.ThreadID)
		if err != nil {
			return "", nil, fmt.Errorf("set board done: %w", err)
		}
		return `done_at = ?, done_seen = ?, remind_at = '', reminded = 0`, []any{stamp(now), boardDoneSeen(members)}, nil
	})
}

// SetBoardRemind snoozes a case until (zero: no more, which also lets a
// remind that came due stop keeping the case), clearing done (and opening
// again the commitments done closed). It records when it was set and the
// Message-IDs of the case's inbound members then (BoardCase.RemindSetAt):
// inbound mail that counts stored later ends the remind (DrainBoard).
// ErrNotFound for an unknown case. The caller checks that until lies
// ahead.
func (s *Store) SetBoardRemind(ctx context.Context, id string, until time.Time) (BoardCase, error) {
	return s.setBoardUser(ctx, id, "set board remind", func(tx *sql.Tx, c BoardCase) (string, []any, error) {
		if until.IsZero() {
			if c.RemindAt.IsZero() {
				return "", nil, nil
			}
			return boardEndReminded, nil, nil
		}
		if c.RemindAt.Equal(until) && !c.Reminded && c.DoneAt.IsZero() {
			return "", nil, nil
		}
		if err := reopenDoneCommitmentsTx(ctx, tx, c); err != nil {
			return "", nil, err
		}
		members, err := loadBoardSeenTx(ctx, tx, c.AccountID, c.ThreadID)
		if err != nil {
			return "", nil, fmt.Errorf("set board remind: %w", err)
		}
		// The store's clock, as the members' StoredAt.
		set := parseStamp(nowStamp())
		return `remind_at = ?, reminded = 0, done_at = '', done_seen = ?`, []any{stamp(until), boardRemindSeen(set, members)}, nil
	})
}

// SetBoardDraft links the draft to the case (board.setDraft; an
// annotation is not needed), or unlinks it with "". A link to another
// draft that still exists in the case's account is kept: ErrBoardDraftLinked
// (unlink first). Linking the draft already linked changes nothing.
// ErrNotFound for an unknown case. The caller checks the draft.
//
// A linked draft is local from then on, in the same transaction
// (makeDraftLocalTx): not uploaded to the Drafts folder, edited when it
// was an ordinary draft, and a copy it has there already is deleted
// through the operation log or recorded as a stray copy (the caller wakes
// the syncer). Linking the draft already linked makes it local too.
func (s *Store) SetBoardDraft(ctx context.Context, id, draftID string) (BoardCase, error) {
	var files []messageFile
	c, err := s.setBoardUser(ctx, id, "set board draft", func(tx *sql.Tx, c BoardCase) (string, []any, error) {
		if draftID != "" && (c.DraftID == draftID || c.Draft == nil) {
			_, _, gone, err := makeDraftLocalTx(ctx, tx, c.AccountID, draftID)
			if err != nil {
				return "", nil, err
			}
			files = gone
		}
		switch {
		case c.DraftID == draftID:
			return "", nil, nil
		case draftID != "" && c.Draft != nil:
			return "", nil, ErrBoardDraftLinked
		}
		return `draft_id = ?`, []any{draftID}, nil
	})
	if err == nil {
		s.removeMessageFiles(files)
	}
	return c, err
}

// BoardMessage is a member that counts as board.get and board.queue show
// it; core trims the text and cleans it.
type BoardMessage struct {
	ID           string
	FolderID     string
	Role         api.FolderRole
	Mine         bool // in a folder of role sent or outbox
	From         []api.Address
	To, CC       []api.Address
	Subject      string
	Date         time.Time
	InternalDate time.Time
	StoredAt     time.Time // when the store wrote the row (BoardMember.StoredAt)
	RFCMessageID string
	BodyState    BodyState
	Text         string // the stored plain text, at most boardTextCap characters
	TextCut      bool   // the store cut Text
}

// BoardMessages lists the members that count of a case, the newest limit
// (0 = api.MaxBoardMessages) oldest first, each Message-ID once (the
// user's own row of it when there is one, else the lowest id), with their
// text. ErrNotFound for an unknown case; empty when no member counts.
func (s *Store) BoardMessages(ctx context.Context, caseID string, limit int) ([]BoardMessage, error) {
	c, err := s.GetBoardCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	return boardMessages(ctx, s.db, c.AccountID, c.ThreadID, limit)
}

// boardMessages is BoardMessages for a thread.
func boardMessages(ctx context.Context, q querier, accountID, threadID string, limit int) ([]BoardMessage, error) {
	if limit <= 0 {
		limit = api.MaxBoardMessages
	}
	members, _, err := loadBoardMembersTx(ctx, q, accountID, threadID, boardThreadRows)
	if err != nil {
		return nil, err
	}
	// Newest first, each Message-ID once.
	byRFC := map[string]int{}
	var picked []BoardMember
	for i := len(members) - 1; i >= 0; i-- {
		m := members[i]
		if !m.Counts {
			continue
		}
		if m.RFCMessageID != "" {
			if j, ok := byRFC[m.RFCMessageID]; ok {
				if (m.Mine && !picked[j].Mine) || (m.Mine == picked[j].Mine && m.ID < picked[j].ID) {
					picked[j] = m
				}
				continue
			}
			byRFC[m.RFCMessageID] = len(picked)
		}
		picked = append(picked, m)
	}
	if len(picked) > limit {
		picked = picked[:limit]
	}
	out := make([]BoardMessage, len(picked))
	ids := make([]string, len(picked))
	for i, m := range picked {
		k := len(picked) - 1 - i // oldest first
		out[k] = BoardMessage{ID: m.ID, FolderID: m.FolderID, Role: m.Role, Mine: m.Mine, From: m.From, To: m.To, CC: m.CC,
			Subject: m.Subject, Date: m.Date, InternalDate: m.InternalDate, StoredAt: m.StoredAt, RFCMessageID: m.RFCMessageID, BodyState: m.BodyState}
		ids[k] = m.ID
	}
	texts := map[string]string{}
	cut := map[string]bool{}
	for _, chunk := range chunkStrings(ids, linkChunk) {
		rows, err := q.QueryContext(ctx, `SELECT id, substr(text_body, 1, ?), length(text_body) > ? FROM messages WHERE id IN (`+
			inPlaceholders(len(chunk))+`)`, append([]any{boardTextCap, boardTextCap}, toAny(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("board messages: %w", err)
		}
		for rows.Next() {
			var id, text string
			var long int
			if err := rows.Scan(&id, &text, &long); err != nil {
				rows.Close()
				return nil, fmt.Errorf("board messages: %w", err)
			}
			texts[id], cut[id] = text, long != 0
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("board messages: %w", err)
		}
	}
	for i := range out {
		out[i].Text, out[i].TextCut = texts[out[i].ID], cut[out[i].ID]
	}
	return out, nil
}

// BoardQueueQuery selects the cases board.queue offers: live cases of the
// accounts (on the board by their window, not done, not snoozed) whose
// annotation is missing or stale and whose thread is not waiting to be
// evaluated again (its input key may be about to change).
type BoardQueueQuery struct {
	AccountIDs []string // none = nothing
	CaseIDs    []string // none = any
	Now        time.Time
	Windows    BoardWindowDays
	Limit      int // 0 = api.DefaultBoardQueueLimit
	Messages   int // per case; 0 = api.MaxBoardQueueMessages
}

// BoardQueueItem is one case of the queue with its members.
type BoardQueueItem struct {
	Case     BoardCase
	Messages []BoardMessage // the newest members that count, oldest first, as BoardMessages
}

// boardQueueWhere is the FROM and WHERE of the queue and its arguments.
func boardQueueWhere(q BoardQueueQuery) (string, []any) {
	shown, shownArgs := boardShown(q.Now, q.Windows, 0, false)
	where := ` FROM board_cases c LEFT JOIN board_annotations a ON a.case_id = c.id
		WHERE c.account_id IN (` + inPlaceholders(len(q.AccountIDs)) + `)
			AND c.done_at = '' AND (c.remind_at = '' OR c.reminded = 1 OR c.remind_at <= ?)
			AND (a.case_id IS NULL OR a.input_key != c.input_key)
			AND NOT EXISTS (SELECT 1 FROM board_dirty x WHERE x.account_id = c.account_id AND x.thread_id = c.thread_id)
			AND ` + shown
	args := append(toAny(q.AccountIDs), stamp(q.Now))
	args = append(args, shownArgs...)
	if len(q.CaseIDs) > 0 {
		// One parameter whatever the number of ids (an IN list of them
		// could exceed SQLite's variable limit).
		where += ` AND c.id IN (SELECT value FROM json_each(?))`
		args = append(args, boardIDList(q.CaseIDs))
	}
	return where, args
}

// boardIDList is the ids, each once and sorted, as a JSON array.
func boardIDList(ids []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	raw, _ := json.Marshal(out)
	return string(raw)
}

// CountBoardQueue counts the cases BoardQueue would offer.
func (s *Store) CountBoardQueue(ctx context.Context, q BoardQueueQuery) (int, error) {
	if len(q.AccountIDs) == 0 {
		return 0, nil
	}
	where, args := boardQueueWhere(q)
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count board queue: %w", err)
	}
	return n, nil
}

// BoardQueue returns the first limit cases of the queue, newest Date
// first, with their members, and how many further cases it would offer.
// One read transaction, so the count and the items agree.
func (s *Store) BoardQueue(ctx context.Context, q BoardQueueQuery) ([]BoardQueueItem, int, error) {
	out := []BoardQueueItem{}
	if len(q.AccountIDs) == 0 {
		return out, 0, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = api.DefaultBoardQueueLimit
	}
	per := q.Messages
	if per <= 0 {
		per = api.MaxBoardQueueMessages
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("board queue: %w", err)
	}
	defer tx.Rollback()
	where, args := boardQueueWhere(q)
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("board queue: %w", err)
	}
	ids, err := stringColumn(ctx, tx, `SELECT c.id`+where+` ORDER BY c.date DESC, c.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("board queue: %w", err)
	}
	for _, id := range ids {
		c, err := boardCaseWhere(ctx, tx, `c.id = ?`, id)
		if err != nil {
			return nil, 0, err
		}
		msgs, err := boardMessages(ctx, tx, c.AccountID, c.ThreadID, per)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, BoardQueueItem{Case: c, Messages: msgs})
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("board queue: %w", err)
	}
	return out, total - len(out), nil
}

// BoardMemberOf reports whether the message is a member of the case that
// counts (a visible row of the case's thread, as the rules' members are),
// and whether it is the user's own (a row in a folder of role sent or
// outbox). Used to check a deadline's or a commitment's message before the
// quote check. Two lookups by primary key; ErrNotFound when the case is
// unknown.
func (s *Store) BoardMemberOf(ctx context.Context, caseID, messageID string) (counts, mine bool, err error) {
	if caseID == "" {
		return false, false, ErrNotFound
	}
	var accountID, threadID string
	err = s.db.QueryRowContext(ctx, `SELECT account_id, thread_id FROM board_cases WHERE id = ?`, caseID).Scan(&accountID, &threadID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, ErrNotFound
	case err != nil:
		return false, false, fmt.Errorf("board: case of member: %w", err)
	}
	if messageID == "" {
		return false, false, nil
	}
	var role, kind string
	var m BoardMember
	err = s.db.QueryRowContext(ctx, boardMemberOfQuery, accountID, threadID, messageID).Scan(&role, &m.Bulk, &kind)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("board: member: %w", err)
	}
	m.Role, m.ItemKind = api.FolderRole(role), api.IssueItemKind(kind)
	m.Mine = m.Role == api.RoleSent || m.Role == api.RoleOutbox
	return boardCounts(m), m.Mine, nil
}

// boardMemberOfQuery reads one visible member of a thread (boardMembersFrom)
// by its id; its parameters are the account id, the thread id and the
// message id.
const boardMemberOfQuery = `SELECT f.role, m.bulk, COALESCE(i.kind, '')` + boardMembersFrom + ` AND m.id = ?`
