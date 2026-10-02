// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The board (migration 0017, docs/api.md §4.13). A case is one thread of an
// account; its row in board_cases holds what the rules derived (a cache,
// rebuilt by DrainBoard from the thread's members whenever a trigger marked
// the thread dirty) and what the user decided (never recomputed). The
// rules themselves are not the store's: DrainBoard hands each dirty thread
// to a BoardDecider, which core wires to internal/board. What the store
// does decide is mechanical and comes from the contract: which members
// count (boardCounts), the keys an annotation is checked against
// (input_key, members_key), keeping a case the rules dropped while the
// user's choice, a remind, a deadline or a commitment holds it ("kept"),
// keeping a case whose thread lost every visible member for a grace
// period (orphaned_at: a move between folders deletes one row before the
// other arrives), reopening a done case when inbound mail arrives later
// (never for a copy of a message it already had when it was marked done),
// closing commitments when the user writes again, and moving a case with
// its thread when threads merge.

// Errors of the board's methods (core maps them to API codes).
var (
	// ErrBoardConflict: the members of the case changed since the caller
	// read its input key (board.annotate, board.commit → conflict), or the
	// message of a commitment is no longer the user's.
	ErrBoardConflict = errors.New("store: board case changed")
	// ErrBoardNotMine: a commitment's message is not a member of the case
	// that is the user's own (board.commit → invalidArgument).
	ErrBoardNotMine = errors.New("store: not one of the user's messages in the case")
	// ErrBoardDraftLinked: the case already links another draft that still
	// exists (SetBoardDraft).
	ErrBoardDraftLinked = errors.New("store: the case already links a draft")
)

const (
	// boardThreadRows caps the members DrainBoard loads of one thread (the
	// newest). Local threads are capped at 500 by the linker; this guards
	// server threads (Graph) and long issues.
	boardThreadRows = 2000
	// boardTextCap caps one message's text BoardThread.Text and the member
	// readers return, in characters (SQLite substr); the caller trims
	// further.
	boardTextCap = 64 << 10
	// boardCaseIDPrefix starts a case id ("c_" + 32 hex).
	boardCaseIDPrefix = "c_"
	// Default budgets of one DrainBoard batch (BoardDrainOptions): the
	// write transaction it holds must stay well below the sync's
	// busy_timeout (5 s).
	boardDrainLimit   = 50
	boardDrainMembers = 10000
	boardDrainBudget  = 500 * time.Millisecond
)

// BoardOrphanGrace is how long PruneBoardCases keeps a case whose thread
// has no visible member (BoardPrune.OrphanBefore zero): long enough for
// the other folder of a move to sync.
const BoardOrphanGrace = 24 * time.Hour

// BoardMember is one visible member of a thread (not hidden, not in a
// virtual folder) as a BoardDecider sees it.
type BoardMember struct {
	ID       string
	FolderID string
	Role     api.FolderRole
	// Mine: the row is in a folder of role sent or outbox — never because
	// of its From.
	Mine bool
	// TwinOfMine: not Mine itself but sharing its Message-ID with a Mine
	// member of the thread (a Bcc to oneself, a Gmail label).
	TwinOfMine bool
	// Counts: a member that counts (docs/api.md §4.13): not in trash, junk
	// or drafts, classified as no bulk mail (the user's own rows count
	// whatever their classification), and no Jira event.
	Counts   bool
	RemoteID string
	// ItemKind and ItemAuthorID describe the Jira item behind the row ("" for
	// mail).
	ItemKind     api.IssueItemKind
	ItemAuthorID string
	// ItemAt is the site's time of a Jira item (zero for mail): its
	// creation, or for a comment a bot relayed (ItemVia set, whose Date and
	// InternalDate come from the relayed text and are no evidence of when
	// it was written) the site's last change of it, which is never earlier.
	// Members of an issue are ordered by it; the rules use it, never Date.
	ItemAt  time.Time
	ItemVia string

	From, To, CC, BCC []api.Address
	ReplyTo           []api.Address
	Subject, Snippet  string
	Date              time.Time // the Date header (zero when unknown)
	InternalDate      time.Time // the server's arrival time (zero when unknown)
	StoredAt          time.Time // when the store wrote the row: the local arrival, which no sender can forge
	UpdatedAt         time.Time // the row's last change (a body, an edited Jira comment)
	RFCMessageID      string
	InReplyTo         string
	// References are the first identifiers of the References header, as
	// many as boardReferencesMax from the first boardReferencesChars of the
	// stored list (decodeBoardReferences): enough to tell whether the
	// message answers anything, never the whole chain. nil when it has
	// none, or when the stored list is malformed.
	References      []string
	Flags           []api.Flag
	Unread, Flagged bool
	Bulk            string            // "" not classified yet, "none", or the kind
	Headers         map[string]string // the curated headers (Importance, X-Priority, …)
	HasAttachments  bool
	Attachments     []api.Attachment
	BodyState       BodyState
}

// HasRFC822 says whether the member carries an attached message.
func (m BoardMember) HasRFC822() bool {
	for _, a := range m.Attachments {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(a.ContentType, ";", 2)[0]), "message/rfc822") {
			return true
		}
	}
	return false
}

// BoardThread is what a BoardDecider gets of one dirty thread. It is valid
// only during the call: Text reads inside DrainBoard's transaction.
type BoardThread struct {
	AccountID string
	ThreadID  string
	// Members are the visible members oldest first by (Date, ID) — for an
	// issue by (ItemAt, ID) — at most the newest boardThreadRows
	// (Truncated then). Never empty.
	Members   []BoardMember
	Truncated bool
	// Issue, Items and Me are set for an issue's thread ("jira:" + id):
	// the issue row (nil when the store has none), its items, and the
	// account's Jira user id ("" while the site has not been asked).
	Issue *Issue
	Items []IssueItem
	Me    string
	// Case is the existing case of the thread (with its annotation, without
	// the draft), nil when there is none; OpenCommitments counts its open
	// commitments.
	Case            *BoardCase
	OpenCommitments int

	ctx context.Context
	tx  *sql.Tx
}

// Text is the stored plain text of a member of the thread, at most
// boardTextCap characters; ErrNotFound for any other id. It reads on each
// call (members carry no text): call it only for the members whose text
// the rules look at.
func (t *BoardThread) Text(id string) (string, error) {
	if t.tx == nil {
		return "", fmt.Errorf("board thread text: read outside DrainBoard")
	}
	var text string
	err := t.tx.QueryRowContext(t.ctx, `SELECT substr(text_body, 1, ?) FROM messages WHERE id = ? AND account_id = ? AND thread_id = ?`,
		boardTextCap, id, t.AccountID, t.ThreadID).Scan(&text)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("board thread text: %w", err)
	}
	return text, nil
}

// BoardVerdict is a BoardDecider's verdict on a thread.
type BoardVerdict struct {
	// Skip leaves the case row as it is (the thread is not ready to judge);
	// the thread is no longer dirty all the same.
	Skip bool
	// State is the rules' state; "" = the rules make no case of the thread
	// (the store keeps an existing case that something holds, see
	// DrainBoard, else deletes it).
	State        api.BoardState
	Reason       api.BoardReason
	RulesVersion string

	// The display fields of the case. Fill them whenever a member counts,
	// also with State "": a kept case shows them. LatestMessageID "" leaves
	// an existing row's display fields as they are.
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

	// NewestInboundStored and NewestInboundDate are the StoredAt and the
	// arrival (InternalDate, else Date) of the newest inbound member that
	// counts, or of the inbound member that reopens a done case;
	// NewestInboundMessageID is that member's RFCMessageID. Zero when there
	// is none. A done case reopens when that member was stored after
	// DoneAt, arrived after DoneAt less a day (so a backfill of old mail
	// does not reopen it), and its Message-ID is not one the case already
	// had when it was marked done (BoardCase.SeenAtDone: a copy another
	// client moved is stored anew). Pick the member with SeenAtDone in mind:
	// the store only refuses, it does not look for another.
	NewestInboundStored    time.Time
	NewestInboundDate      time.Time
	NewestInboundMessageID string
}

// BoardDecider judges one thread. It runs inside DrainBoard's write
// transaction: it must be quick and must not call the store. Read a
// member's text with BoardThread.Text only when the rules need it.
type BoardDecider func(t *BoardThread) (BoardVerdict, error)

// BoardDrainOptions bound one DrainBoard batch.
type BoardDrainOptions struct {
	Limit int // dirty threads to take at most; 0 = 50
	Now   time.Time
	// Since: a thread that is no case yet becomes one only when its
	// verdict's Date (the arrival of its newest member that counts) is at
	// or after Since; zero = any. An existing case is updated, kept or
	// deleted as usual (the hourly prune ages it out). Core passes now less
	// the longest window.
	Since time.Time
	// MaxMembers and Budget end the batch early, after the thread that
	// crosses either (the rest stays dirty for the next batch): the members
	// loaded (0 = 10000) and the time spent inside the transaction (0 =
	// 500 ms). At least one thread is taken.
	MaxMembers int
	Budget     time.Duration
}

// BoardDrainFailure is a thread DrainBoard could not evaluate: its writes
// were rolled back and it is no longer dirty (the next change to it, or a
// pass that marks its account, brings it back). The caller logs it.
type BoardDrainFailure struct {
	AccountID string
	ThreadID  string
	Err       error
}

// BoardDrain reports one DrainBoard call.
type BoardDrain struct {
	Threads int // dirty threads taken (and no longer dirty), failed ones included
	// Accounts whose listing changed (a case created, changed, deleted or a
	// commitment closed), sorted.
	Accounts []string
	Failed   []BoardDrainFailure
	More     bool // dirty threads are left
}

// DrainBoard takes dirty threads (at most opt.Limit, within its budgets)
// and, in one write transaction, evaluates each: it loads the thread's
// members (and for an issue the issue, its items and the Jira user),
// calls decide, and writes the outcome, then removes the thread from the
// dirty set. Sync writes cannot slip between the read and the removal:
// they wait for the transaction and mark the thread dirty again after it.
// Without dirty threads it does not open a transaction.
//
// The outcome for each thread:
//   - no visible member: the case is kept but marked orphaned (off the
//     board, pruned after BoardOrphanGrace unless members come back);
//   - Skip: nothing changes;
//   - State set: the case is created (only when dated at or after
//     opt.Since) or its derived columns updated;
//   - State "": an existing case is kept, with rule_reason "kept" and its
//     last rule state, while the user set a state, a remind is set (ahead,
//     or come due and not followed by done or another remind), a
//     commitment is open, its annotation is current and has a deadline
//     after now, or it links a draft that exists; otherwise it is deleted.
//
// For a kept or ruled case, a done case reopens per BoardVerdict, and the
// open commitments a newer message of the user's answers are closed
// (replied). The version goes up whenever any column changes.
//
// A thread whose evaluation fails (decide's error, an unknown state, a row
// that does not decode) is rolled back to before it, reported in Failed
// and dropped from the dirty set; the batch goes on. Only a cancelled ctx
// or a failure of the transaction itself fails the call (nothing is
// written).
func (s *Store) DrainBoard(ctx context.Context, opt BoardDrainOptions, decide BoardDecider) (BoardDrain, error) {
	limit, maxMembers, budget := opt.Limit, opt.MaxMembers, opt.Budget
	if limit <= 0 {
		limit = boardDrainLimit
	}
	if maxMembers <= 0 {
		maxMembers = boardDrainMembers
	}
	if budget <= 0 {
		budget = boardDrainBudget
	}
	var out BoardDrain
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM board_dirty)`).Scan(&pending); err != nil {
		return out, fmt.Errorf("drain board: %w", err)
	}
	if pending == 0 {
		return out, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("drain board: %w", err)
	}
	defer tx.Rollback()
	start := time.Now()

	type key struct{ account, thread string }
	var batch []key
	rows, err := tx.QueryContext(ctx, `SELECT account_id, thread_id FROM board_dirty LIMIT ?`, limit)
	if err != nil {
		return out, fmt.Errorf("drain board: %w", err)
	}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.account, &k.thread); err != nil {
			rows.Close()
			return out, fmt.Errorf("drain board: %w", err)
		}
		batch = append(batch, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("drain board: %w", err)
	}
	changed := map[string]bool{}
	loaded := 0
	for i, k := range batch {
		if i > 0 && (loaded >= maxMembers || time.Since(start) >= budget) {
			break
		}
		if _, err := tx.ExecContext(ctx, `SAVEPOINT board_thread`); err != nil {
			return BoardDrain{}, fmt.Errorf("drain board: %w", err)
		}
		c, n, err := s.evaluateBoardThreadTx(ctx, tx, k.account, k.thread, opt, decide)
		loaded += n
		if err != nil {
			if ctx.Err() != nil {
				return BoardDrain{}, fmt.Errorf("drain board: %w", ctx.Err())
			}
			if _, rerr := tx.ExecContext(ctx, `ROLLBACK TO board_thread`); rerr != nil {
				return BoardDrain{}, fmt.Errorf("drain board: roll back thread %s: %w (after %v)", k.thread, rerr, err)
			}
			out.Failed = append(out.Failed, BoardDrainFailure{AccountID: k.account, ThreadID: k.thread, Err: err})
		} else if c {
			changed[k.account] = true
		}
		if _, err := tx.ExecContext(ctx, `RELEASE board_thread`); err != nil {
			return BoardDrain{}, fmt.Errorf("drain board: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM board_dirty WHERE account_id = ? AND thread_id = ?`, k.account, k.thread); err != nil {
			return BoardDrain{}, fmt.Errorf("drain board: %w", err)
		}
		out.Threads++
	}
	var more int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM board_dirty)`).Scan(&more); err != nil {
		return BoardDrain{}, fmt.Errorf("drain board: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BoardDrain{}, fmt.Errorf("drain board: %w", err)
	}
	out.More = more != 0
	out.Accounts = sortedKeys(changed)
	return out, nil
}

// boardDerived is what DrainBoard writes of a case and compares to decide
// whether its version goes up.
type boardDerived struct {
	ruleState, ruleReason, rulesVersion  string
	inputKey, membersKey                 string
	subject, snippet, person, date       string
	unread, att, archive                 bool
	count                                int
	replyID, replyFolder, latestID       string
	issueKey, issueStatus, issueCategory string
	doneAt, doneSeen, orphanedAt         string
	memberIDs                            string
}

// evaluateBoardThreadTx is DrainBoard's work for one thread; it reports
// whether the account's listing changed and how many members it loaded.
func (s *Store) evaluateBoardThreadTx(ctx context.Context, tx *sql.Tx, accountID, threadID string, opt BoardDrainOptions, decide BoardDecider) (bool, int, error) {
	now := opt.Now
	existing, err := boardCaseByThreadTx(ctx, tx, accountID, threadID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, 0, err
	}
	var cur *BoardCase
	if err == nil {
		cur = &existing
	}
	members, truncated, err := loadBoardMembersTx(ctx, tx, accountID, threadID, boardThreadRows)
	if err != nil {
		return false, 0, err
	}
	n := len(members)
	nowStr := stamp(now)
	if cur == nil && len(members) > 0 {
		// A thread new to the board that holds a message an orphaned case
		// had (moved by another client, stored anew in a thread of its
		// own) takes that case over, with the user's decisions.
		if cur, err = adoptOrphanTx(ctx, tx, accountID, threadID, members, nowStr); err != nil {
			return false, n, err
		}
	}
	if len(members) == 0 {
		// Every member gone or hidden: often for a moment only (a move
		// between folders, a Gmail label change). The case and the user's
		// decisions stay, off the board; the prune deletes it later.
		if cur == nil || !cur.OrphanedAt.IsZero() {
			return false, n, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET orphaned_at = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			nowStr, nowStr, cur.ID); err != nil {
			return false, n, fmt.Errorf("board: orphan case: %w", err)
		}
		return true, n, nil
	}
	t := &BoardThread{AccountID: accountID, ThreadID: threadID, Members: members, Truncated: truncated, Case: cur, ctx: ctx, tx: tx}
	defer func() { t.tx = nil }()
	if cur != nil {
		if t.OpenCommitments, err = countOpenCommitmentsTx(ctx, tx, cur.ID); err != nil {
			return false, n, err
		}
	}
	if issueID, ok := IssueIDOfThread(threadID); ok {
		if err := loadBoardIssueTx(ctx, tx, t, issueID); err != nil {
			return false, n, err
		}
	}
	v, err := decide(t)
	if err != nil {
		return false, n, fmt.Errorf("board: judge thread %s: %w", threadID, err)
	}
	if v.Skip {
		return false, n, nil
	}
	if v.State != "" && !v.State.Valid() {
		return false, n, fmt.Errorf("board: judge thread %s: unknown state %q", threadID, v.State)
	}
	inputKey, membersKey := boardKeys(members)

	var issueKey, issueStatus, issueCategory string
	if t.Issue != nil {
		issueKey, issueStatus, issueCategory = t.Issue.Key, t.Issue.Status, string(t.Issue.StatusCategory)
		if strings.HasPrefix(issueKey, issueKeyPlaceholder) {
			issueKey = ""
		}
	}

	if v.State == "" {
		if cur == nil {
			return false, n, nil
		}
		// A linked suggested reply that still exists keeps its case too:
		// unstarring it or replying elsewhere never orphans the draft.
		kept := cur.UserState != "" || !cur.RemindAt.IsZero() || t.OpenCommitments > 0 || cur.Draft != nil ||
			(cur.Annotation != nil && cur.Annotation.InputKey == inputKey && cur.Annotation.Due != nil && cur.Annotation.Due.At.After(now))
		if !kept {
			return true, n, deleteBoardCaseTx(ctx, tx, cur.ID)
		}
		v.State, v.Reason = cur.RuleState, api.BoardReasonKept
	}
	if cur == nil && !opt.Since.IsZero() && !v.Date.IsZero() && v.Date.Before(opt.Since) {
		// Older than any window: no row for the prune to delete again.
		return false, n, nil
	}

	d := boardDerived{
		ruleState: string(v.State), ruleReason: string(v.Reason), rulesVersion: v.RulesVersion,
		inputKey: inputKey, membersKey: membersKey, memberIDs: boardMemberIDs(members),
		issueKey: issueKey, issueStatus: issueStatus, issueCategory: issueCategory,
	}
	if v.LatestMessageID != "" || cur == nil {
		person, err := encodeJSON(v.Person, "{}")
		if err != nil {
			return false, n, fmt.Errorf("board: encode person: %w", err)
		}
		d.subject, d.snippet, d.person, d.date = v.Subject, v.Snippet, person, stamp(v.Date)
		d.unread, d.att, d.archive, d.count = v.Unread, v.HasAttachments, v.CanArchive, v.MessageCount
		d.replyID, d.replyFolder, d.latestID = v.ReplyMessageID, v.ReplyFolderID, v.LatestMessageID
	} else {
		old := derivedOf(*cur)
		d.subject, d.snippet, d.person, d.date = old.subject, old.snippet, old.person, old.date
		d.unread, d.att, d.archive, d.count = old.unread, old.att, old.archive, old.count
		d.replyID, d.replyFolder, d.latestID = old.replyID, old.replyFolder, old.latestID
	}

	if cur == nil {
		id := newID(boardCaseIDPrefix)
		if _, err := tx.ExecContext(ctx, `INSERT INTO board_cases (id, account_id, thread_id,
				rule_state, rule_reason, rules_version, input_key, members_key, subject, snippet, person_json, date,
				unread, has_attachments, can_archive, message_count, reply_message_id, reply_folder_id, latest_message_id,
				issue_key, issue_status, issue_status_category, member_ids, computed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, accountID, threadID, d.ruleState, d.ruleReason, d.rulesVersion, d.inputKey, d.membersKey,
			d.subject, d.snippet, d.person, d.date, boolInt(d.unread), boolInt(d.att), boolInt(d.archive), d.count,
			d.replyID, d.replyFolder, d.latestID, d.issueKey, d.issueStatus, d.issueCategory, d.memberIDs, nowStr, nowStr, nowStr); err != nil {
			return false, n, fmt.Errorf("board: create case: %w", err)
		}
		return true, n, nil
	}

	// A done case reopens when inbound mail that counts arrived later.
	d.doneAt, d.doneSeen = optStamp(cur.DoneAt), cur.doneSeen
	if !cur.DoneAt.IsZero() && v.NewestInboundStored.After(cur.DoneAt) && v.NewestInboundDate.After(cur.DoneAt.Add(-24*time.Hour)) &&
		!cur.SeenAtDone(v.NewestInboundMessageID) {
		d.doneAt, d.doneSeen = "", ""
	}
	closed, err := closeRepliedCommitmentsTx(ctx, tx, cur.ID, members, nowStr)
	if err != nil {
		return false, n, err
	}
	if d == derivedOf(*cur) {
		return closed, n, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET
			rule_state = ?, rule_reason = ?, rules_version = ?, input_key = ?, members_key = ?,
			subject = ?, snippet = ?, person_json = ?, date = ?, unread = ?, has_attachments = ?, can_archive = ?,
			message_count = ?, reply_message_id = ?, reply_folder_id = ?, latest_message_id = ?,
			issue_key = ?, issue_status = ?, issue_status_category = ?, done_at = ?, done_seen = ?, orphaned_at = ?,
			member_ids = ?, computed_at = ?, updated_at = ?, version = version + 1
		WHERE id = ?`,
		d.ruleState, d.ruleReason, d.rulesVersion, d.inputKey, d.membersKey,
		d.subject, d.snippet, d.person, d.date, boolInt(d.unread), boolInt(d.att), boolInt(d.archive),
		d.count, d.replyID, d.replyFolder, d.latestID, d.issueKey, d.issueStatus, d.issueCategory, d.doneAt, d.doneSeen, d.orphanedAt,
		d.memberIDs,
		nowStr, nowStr, cur.ID); err != nil {
		return false, n, fmt.Errorf("board: update case: %w", err)
	}
	return true, n, nil
}

// BoardCaseThread loads the thread of a case as DrainBoard hands it to
// the decider (the same members, the issue, its items and the Jira user;
// Text cannot be read), for a caller that must judge exactly what the
// rules judged (board.unflag). ErrNotFound for an unknown case.
func (s *Store) BoardCaseThread(ctx context.Context, caseID string) (*BoardThread, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("board case thread: %w", err)
	}
	defer tx.Rollback()
	c, err := boardCaseWhere(ctx, tx, `c.id = ?`, caseID)
	if err != nil {
		return nil, err
	}
	members, truncated, err := loadBoardMembersTx(ctx, tx, c.AccountID, c.ThreadID, boardThreadRows)
	if err != nil {
		return nil, err
	}
	t := &BoardThread{AccountID: c.AccountID, ThreadID: c.ThreadID, Members: members, Truncated: truncated, Case: &c}
	if issueID, ok := IssueIDOfThread(c.ThreadID); ok {
		if err := loadBoardIssueTx(ctx, tx, t, issueID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("board case thread: %w", err)
	}
	return t, nil
}

// derivedOf is the derived part of a stored case.
func derivedOf(c BoardCase) boardDerived {
	person, _ := encodeJSON(c.Person, "{}")
	return boardDerived{
		ruleState: string(c.RuleState), ruleReason: string(c.RuleReason), rulesVersion: c.RulesVersion,
		inputKey: c.InputKey, membersKey: c.membersKey,
		subject: c.Subject, snippet: c.Snippet, person: person, date: stamp(c.Date),
		unread: c.Unread, att: c.HasAttachments, archive: c.CanArchive, count: c.MessageCount,
		replyID: c.ReplyMessageID, replyFolder: c.ReplyFolderID, latestID: c.LatestMessageID,
		issueKey: c.issueField(0), issueStatus: c.issueField(1), issueCategory: c.issueField(2),
		doneAt: optStamp(c.DoneAt), doneSeen: c.doneSeen, orphanedAt: optStamp(c.OrphanedAt),
		memberIDs: c.memberIDs,
	}
}

// deleteBoardCaseTx deletes a case; its annotation and commitments go with
// it (foreign keys).
func deleteBoardCaseTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM board_cases WHERE id = ?`, id); err != nil {
		return fmt.Errorf("board: delete case: %w", err)
	}
	return nil
}

// boardMemberColumns are the columns loadBoardMembersTx reads; m is
// messages, f folders, i the issue item.
const boardMemberColumns = `m.id, m.folder_id, f.role, m.remote_id, COALESCE(i.kind, ''), COALESCE(i.author_id, ''),
	COALESCE(i.via, ''), COALESCE(i.updated, ''),
	m.from_json, m.to_json, m.cc_json, m.bcc_json, m.reply_to_json, m.subject, m.snippet, m.date, m.internal_date, m.created_at,
	m.updated_at, m.rfc_message_id, m.in_reply_to,
	substr(m.references_json, 1, ` + boardReferencesCharsSQL + `), length(m.references_json), m.flags, m.unread, m.flagged, m.bulk, m.headers_json,
	m.has_attachments, m.attachments_json, m.body_state`

// boardReferencesChars is how much of a member's stored References list
// (references_json) the board reads, in characters, and boardReferencesMax
// how many identifiers it keeps: the rules only ask whether a message
// answers anything (board.Member.References).
const (
	boardReferencesChars    = 16 << 10
	boardReferencesCharsSQL = "16384"
	boardReferencesMax      = 64
)

// decodeBoardReferences decodes the first identifiers of a stored
// References list (a JSON array of strings) from raw, its first
// boardReferencesChars characters; cut says the list was longer. It never
// decodes more than boardReferencesMax identifiers. A malformed list is
// none (nil): the message then answers nothing as far as References go,
// which can only make the rules take it for a thread start, and a thread
// start is judged a forward more readily — never a case the list would not
// give. A list cut short keeps the identifiers read whole before the cut.
func decodeBoardReferences(raw string, cut bool) []string {
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil
	}
	var out []string
	for len(out) < boardReferencesMax {
		tok, err := dec.Token()
		if err != nil {
			if cut {
				return out
			}
			return nil
		}
		switch v := tok.(type) {
		case string:
			out = append(out, v)
		case json.Delim:
			if v == ']' {
				return out
			}
			return nil
		default:
			return nil
		}
	}
	return out
}

// boardMemberOrder is the time members are ordered by: the Date column,
// except for a Jira comment a bot relayed, whose Date comes from the
// relayed text (see BoardMember.ItemAt).
const boardMemberOrder = `(CASE WHEN COALESCE(i.via, '') != '' AND COALESCE(i.updated, '') != '' THEN i.updated ELSE m.date END)`

// boardMembersFrom is the FROM and WHERE of a thread's visible members;
// its parameters are the account id and the thread id.
const boardMembersFrom = ` FROM messages m JOIN folders f ON f.id = m.folder_id
	LEFT JOIN issue_items i ON m.remote_id != '' AND i.account_id = m.account_id AND i.remote_id = m.remote_id
	WHERE m.account_id = ? AND m.thread_id = ? AND m.hidden = 0 AND f.virtual = ''`

// loadBoardMembersTx reads the newest limit visible members of a thread,
// oldest first, and reports whether there were more.
func loadBoardMembersTx(ctx context.Context, q querier, accountID, threadID string, limit int) ([]BoardMember, bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+boardMemberColumns+boardMembersFrom+
		` ORDER BY `+boardMemberOrder+` DESC, m.id DESC LIMIT ?`, accountID, threadID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("board: thread members: %w", err)
	}
	defer rows.Close()
	var out []BoardMember
	for rows.Next() {
		var m BoardMember
		var role, kind, itemUpdated, from, to, cc, bcc, replyTo, date, internal, created, updated, flags, headers, atts, state string
		var refs string
		var unread, flagged, hasAtt, refsLen int
		if err := rows.Scan(&m.ID, &m.FolderID, &role, &m.RemoteID, &kind, &m.ItemAuthorID, &m.ItemVia, &itemUpdated,
			&from, &to, &cc, &bcc, &replyTo, &m.Subject, &m.Snippet, &date, &internal, &created, &updated,
			&m.RFCMessageID, &m.InReplyTo, &refs, &refsLen, &flags, &unread, &flagged, &m.Bulk, &headers,
			&hasAtt, &atts, &state); err != nil {
			return nil, false, fmt.Errorf("board: scan member: %w", err)
		}
		m.Role, m.ItemKind, m.BodyState = api.FolderRole(role), api.IssueItemKind(kind), BodyState(state)
		for _, p := range []struct {
			raw string
			dst *[]api.Address
		}{{from, &m.From}, {to, &m.To}, {cc, &m.CC}, {bcc, &m.BCC}, {replyTo, &m.ReplyTo}} {
			if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
				return nil, false, fmt.Errorf("board: decode addresses of %s: %w", m.ID, err)
			}
		}
		if m.Flags, err = decodeFlags(flags); err != nil {
			return nil, false, fmt.Errorf("board: decode flags of %s: %w", m.ID, err)
		}
		if err := json.Unmarshal([]byte(atts), &m.Attachments); err != nil {
			return nil, false, fmt.Errorf("board: decode attachments of %s: %w", m.ID, err)
		}
		m.Headers = decodeHeaders(headers)
		m.References = decodeBoardReferences(refs, refsLen > boardReferencesChars)
		m.Date, m.InternalDate, m.StoredAt, m.UpdatedAt = parseStamp(date), parseStamp(internal), parseStamp(created), parseStamp(updated)
		if m.ItemKind != "" {
			m.ItemAt = m.Date
			if at := parseStamp(itemUpdated); m.ItemVia != "" && !at.IsZero() {
				m.ItemAt = at
			}
		}
		m.Unread, m.Flagged, m.HasAttachments = unread != 0, flagged != 0, hasAtt != 0
		m.Mine = m.Role == api.RoleSent || m.Role == api.RoleOutbox
		m.Counts = boardCounts(m)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("board: thread members: %w", err)
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	// Oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	mineIDs := map[string]bool{}
	for _, m := range out {
		if m.Mine && m.RFCMessageID != "" {
			mineIDs[m.RFCMessageID] = true
		}
	}
	for i := range out {
		out[i].TwinOfMine = !out[i].Mine && out[i].RFCMessageID != "" && mineIDs[out[i].RFCMessageID]
	}
	return out, truncated, nil
}

// boardCounts is the store's one definition of a member that counts
// (docs/api.md §4.13): visible (the reader's scope), not in trash, junk or
// drafts, classified as no bulk mail, and no Jira event. The user's own
// rows (sent, outbox) count whatever their classification: an outbox row
// is classified only by the upgrade pass, and the user's own message is
// never bulk mail to the user. So does a Jira item (an issue's rows are
// never bulk mail, migration 0016) that is not an event.
func boardCounts(m BoardMember) bool {
	switch m.Role {
	case api.RoleTrash, api.RoleJunk, api.RoleDrafts:
		return false
	}
	if m.ItemKind != "" {
		return m.ItemKind != api.IssueItemEvent
	}
	return m.Mine || m.Bulk == "none"
}

// boardKeys computes a thread's input key (the members that count and
// their body state; an annotation of another key is stale) and its members
// key (what board.get shows of them; the row's updated_at stands for its
// text, which an edited Jira comment changes without a new body state),
// each 32 hex digits.
func boardKeys(members []BoardMember) (inputKey, membersKey string) {
	counting := make([]BoardMember, 0, len(members))
	for _, m := range members {
		if m.Counts {
			counting = append(counting, m)
		}
	}
	sort.Slice(counting, func(i, j int) bool { return counting[i].ID < counting[j].ID })
	in, mem := sha256.New(), sha256.New()
	for _, m := range counting {
		fmt.Fprintf(in, "%s|%s\n", m.ID, m.BodyState)
		from, _ := json.Marshal(m.From)
		fmt.Fprintf(mem, "%s|%s|%s|%s|%s|%s|%s|%q\n", m.ID, m.BodyState, m.FolderID, stamp(m.Date), stamp(m.InternalDate),
			stamp(m.UpdatedAt), from, m.Subject)
	}
	return hex.EncodeToString(in.Sum(nil))[:32], hex.EncodeToString(mem.Sum(nil))[:32]
}

// boardInputKeyTx derives the current input key of a thread inside tx.
func boardInputKeyTx(ctx context.Context, tx *sql.Tx, accountID, threadID string) (string, []BoardMember, error) {
	members, _, err := loadBoardMembersTx(ctx, tx, accountID, threadID, boardThreadRows)
	if err != nil {
		return "", nil, err
	}
	key, _ := boardKeys(members)
	return key, members, nil
}

// loadBoardIssueTx fills the issue part of t.
func loadBoardIssueTx(ctx context.Context, tx *sql.Tx, t *BoardThread, issueID string) error {
	is, err := scanIssue(tx.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issues WHERE account_id = ? AND issue_id = ?`, t.AccountID, issueID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("board: read issue: %w", err)
	default:
		t.Issue = &is
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+issueItemColumns+` FROM issue_items WHERE account_id = ? AND issue_id = ? ORDER BY remote_id`, t.AccountID, issueID)
	if err != nil {
		return fmt.Errorf("board: read issue items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanIssueItem(rows)
		if err != nil {
			return err
		}
		t.Items = append(t.Items, it)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("board: read issue items: %w", err)
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, MetaIssueMePrefix+t.AccountID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("board: read the jira user: %w", err)
	default:
		var me struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &me) == nil {
			t.Me = me.ID
		}
	}
	return nil
}

// closeRepliedCommitmentsTx closes (replied) the case's open commitments
// that a newer message of the user's answers: the user's newest member
// that counts is dated after the commitment's replied_after (the user's
// newest message when it was recorded, so a commitment recorded on an
// older message stays open until the user writes again).
func closeRepliedCommitmentsTx(ctx context.Context, tx *sql.Tx, caseID string, members []BoardMember, now string) (bool, error) {
	var newest *BoardMember
	for i := range members {
		m := &members[i]
		if !m.Mine || !m.Counts {
			continue
		}
		if newest == nil || m.Date.After(newest.Date) || (m.Date.Equal(newest.Date) && m.ID > newest.ID) {
			newest = m
		}
	}
	if newest == nil {
		return false, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE board_commitments SET state = 'closed', closed_reason = ?, closed_at = ?
		WHERE case_id = ? AND state = 'open' AND message_id != ?
			AND (CASE WHEN replied_after != '' THEN replied_after ELSE message_date END) < ?`,
		api.CommitmentClosedReplied, now, caseID, newest.ID, stamp(newest.Date))
	if err != nil {
		return false, fmt.Errorf("board: close replied commitments: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func countOpenCommitmentsTx(ctx context.Context, q querier, caseID string) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT COUNT(*) FROM board_commitments WHERE case_id = ? AND state = 'open'`, caseID)
	if err != nil {
		return 0, fmt.Errorf("board: count commitments: %w", err)
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, fmt.Errorf("board: count commitments: %w", err)
		}
	}
	return n, rows.Err()
}

// CutUTF8 cuts s to at most n bytes at a character boundary (n <= 0: "").
// The one byte cutter of the board (core uses it too).
func CutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// boardDoneSeenMax caps the Message-IDs a done case remembers
// (done_seen), and boardDoneSeenBytes their length together.
const (
	boardDoneSeenMax   = 200
	boardDoneSeenBytes = 32 << 10
)

// boardSeenID is the form of a Message-ID in done_seen ("" = not
// recordable: none, or a line break in it).
func boardSeenID(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "<>")
	if id == "" || len(id) > 998 || strings.ContainsAny(id, "\r\n") {
		return ""
	}
	return id
}

// boardDoneSeen lists the Message-IDs of the inbound members that count
// (not the user's own rows), newest first, in done_seen's form: "\n" +
// each id + "\n", at most boardDoneSeenMax of them.
func boardDoneSeen(members []BoardMember) string {
	ids := make([]string, 0, len(members))
	for i := len(members) - 1; i >= 0; i-- {
		if m := members[i]; m.Counts && !m.Mine {
			ids = append(ids, m.RFCMessageID)
		}
	}
	return joinDoneSeen(ids)
}

// joinDoneSeen builds a done_seen value of the ids in order, each once,
// within the caps.
func joinDoneSeen(ids []string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, id := range ids {
		id = boardSeenID(id)
		if id == "" || seen[id] {
			continue
		}
		if len(seen) >= boardDoneSeenMax || b.Len()+len(id)+2 > boardDoneSeenBytes {
			break
		}
		seen[id] = true
		if b.Len() == 0 {
			b.WriteByte('\n')
		}
		b.WriteString(id)
		b.WriteByte('\n')
	}
	return b.String()
}

// boardMemberIDsMax caps the Message-IDs a case remembers of its members
// (member_ids): enough to find a moved thread again.
const boardMemberIDsMax = 20

// boardMemberIDs lists the Message-IDs of the newest visible members in
// done_seen's form.
func boardMemberIDs(members []BoardMember) string {
	ids := make([]string, 0, min(len(members), boardMemberIDsMax))
	for i := len(members) - 1; i >= 0 && len(ids) < boardMemberIDsMax; i-- {
		ids = append(ids, members[i].RFCMessageID)
	}
	return joinDoneSeen(ids)
}

// adoptOrphanTx moves the newest orphaned case of the account that
// remembers one of the members' Message-IDs to the thread and returns it;
// nil when there is none.
func adoptOrphanTx(ctx context.Context, tx *sql.Tx, accountID, threadID string, members []BoardMember, now string) (*BoardCase, error) {
	ids := strings.Split(strings.Trim(boardMemberIDs(members), "\n"), "\n")
	if len(ids) == 0 || ids[0] == "" {
		return nil, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("board: adopt orphan: %w", err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT c.id FROM board_cases c
		WHERE c.account_id = ? AND c.orphaned_at != '' AND c.member_ids != ''
			AND EXISTS (SELECT 1 FROM json_each(?) j WHERE instr(c.member_ids, char(10) || j.value || char(10)) > 0)
		ORDER BY c.orphaned_at DESC, c.id LIMIT 1`, accountID, string(raw)).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("board: adopt orphan: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE board_cases SET thread_id = ?, version = version + 1, updated_at = ? WHERE id = ?`,
		threadID, now, id); err != nil {
		return nil, fmt.Errorf("board: adopt orphan: %w", err)
	}
	c, err := boardCaseWhere(ctx, tx, `c.id = ?`, id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// mergeDoneSeen joins two done_seen values (a thread merge), a's first.
func mergeDoneSeen(a, b string) string {
	return joinDoneSeen(append(strings.Split(a, "\n"), strings.Split(b, "\n")...))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
