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
	"strings"

	"github.com/schotek/malachi/backend/internal/thread"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Conversation threading. Every message row carries a thread id (never
// empty since migration 0011); a message is linked into its conversation
// inside the transaction that writes it (UpsertMessages, SetMessageBody,
// EnqueueOutbox), by the union rule internal/thread describes. Listing
// groups a folder's rows by thread id at query time. Hidden rows
// (Message.Hidden) are no member of any listing, and an account-wide
// scope leaves out the copies in virtual folders (Folder.Virtual), which
// would count an issue's messages once per view.

// linkPolicy caps the linking work; tests lower it.
var linkPolicy = thread.DefaultPolicy

const (
	// threadDetailRows is how many newest members per thread the details
	// query reads for the participant list.
	threadDetailRows = 64
	// linkChunk is the IN-list size of the lookup queries.
	linkChunk = 500
)

// linkRow is what linking a message needs to know about it.
type linkRow struct {
	id, accountID, threadID, rfcID, inReplyTo string
	references                                []string
}

// insertRefsTx records the identifiers a message points at.
func insertRefsTx(ctx context.Context, tx *sql.Tx, messageID, accountID, inReplyTo string, references []string) error {
	seen := make(map[string]bool, len(references)+1)
	add := func(ref string) error {
		if ref == "" || seen[ref] {
			return nil
		}
		seen[ref] = true
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_refs (message_id, account_id, ref) VALUES (?, ?, ?)`,
			messageID, accountID, ref)
		if err != nil {
			return fmt.Errorf("insert message reference: %w", err)
		}
		return nil
	}
	if err := add(inReplyTo); err != nil {
		return err
	}
	for _, ref := range references {
		if err := add(ref); err != nil {
			return err
		}
	}
	return nil
}

// linkMessageTx merges the message's thread with every thread it is linked
// to (see internal/thread) and reports whether anything changed. A row
// without a thread id gets one first. A server-threaded row is left alone.
func linkMessageTx(ctx context.Context, tx *sql.Tx, r linkRow) (bool, error) {
	if r.threadID == "" {
		r.threadID = newID(thread.IDPrefix)
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET thread_id = ? WHERE id = ?`, r.threadID, r.id); err != nil {
			return false, fmt.Errorf("assign thread id: %w", err)
		}
	}
	if !thread.IsLocalID(r.threadID) {
		return false, nil
	}

	// The identifiers to look up, best link first: In-Reply-To, then the
	// references from the newest end.
	type rank struct {
		link thread.Link
		pos  int
	}
	ranks := map[string]rank{}
	var ids []string
	if r.inReplyTo != "" {
		ranks[r.inReplyTo] = rank{link: thread.LinkInReplyTo}
		ids = append(ids, r.inReplyTo)
	}
	for i := len(r.references) - 1; i >= 0; i-- {
		ref := r.references[i]
		if ref == "" {
			continue
		}
		if _, seen := ranks[ref]; !seen {
			ranks[ref] = rank{link: thread.LinkReference, pos: len(r.references) - 1 - i}
			ids = append(ids, ref)
		}
	}
	lookup := ids
	if r.rfcID != "" {
		if _, seen := ranks[r.rfcID]; !seen {
			lookup = append(lookup, r.rfcID)
		}
	}

	best := map[string]thread.Candidate{}
	note := func(threadID string, link thread.Link, pos int) {
		if threadID == "" || threadID == r.threadID {
			return
		}
		c := thread.Candidate{ThreadID: threadID, Link: link, Pos: pos}
		if b, seen := best[threadID]; !seen || link < b.Link || (link == b.Link && pos < b.Pos) {
			best[threadID] = c
		}
	}
	// Twins and ancestors.
	for _, chunk := range chunkStrings(lookup, linkChunk) {
		args := []any{r.accountID}
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, r.id, linkPolicy.MaxLinkRows)
		rows, err := tx.QueryContext(ctx, `SELECT thread_id, rfc_message_id FROM messages
			WHERE account_id = ? AND rfc_message_id IN (`+inPlaceholders(len(chunk))+`) AND id != ? LIMIT ?`, args...)
		if err != nil {
			return false, fmt.Errorf("look up thread parents: %w", err)
		}
		for rows.Next() {
			var tid, rfc string
			if err := rows.Scan(&tid, &rfc); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan thread parent: %w", err)
			}
			if rk, ok := ranks[rfc]; ok {
				note(tid, rk.link, rk.pos)
			} else {
				note(tid, thread.LinkTwin, 0)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("look up thread parents: %w", err)
		}
	}
	// Descendants: stored messages that name this one.
	if r.rfcID != "" {
		rows, err := tx.QueryContext(ctx, `SELECT m.thread_id FROM message_refs x JOIN messages m ON m.id = x.message_id
			WHERE x.account_id = ? AND x.ref = ? AND m.id != ? LIMIT ?`, r.accountID, r.rfcID, r.id, linkPolicy.MaxLinkRows)
		if err != nil {
			return false, fmt.Errorf("look up thread children: %w", err)
		}
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan thread child: %w", err)
			}
			note(tid, thread.LinkChild, 0)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("look up thread children: %w", err)
		}
	}
	if len(best) == 0 {
		return false, nil
	}

	// Sizes of every thread involved.
	threadIDs := make([]string, 0, len(best)+1)
	threadIDs = append(threadIDs, r.threadID)
	for id := range best {
		threadIDs = append(threadIDs, id)
	}
	sizes := map[string]int{}
	for _, chunk := range chunkStrings(threadIDs, linkChunk) {
		args := []any{r.accountID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx, `SELECT thread_id, COUNT(*) FROM messages
			WHERE account_id = ? AND thread_id IN (`+inPlaceholders(len(chunk))+`) GROUP BY thread_id`, args...)
		if err != nil {
			return false, fmt.Errorf("count thread members: %w", err)
		}
		for rows.Next() {
			var tid string
			var n int
			if err := rows.Scan(&tid, &n); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan thread size: %w", err)
			}
			sizes[tid] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("count thread members: %w", err)
		}
	}
	cands := make([]thread.Candidate, 0, len(best))
	for _, c := range best {
		c.Size = sizes[c.ThreadID]
		cands = append(cands, c)
	}
	plan, ok := thread.Resolve(r.threadID, sizes[r.threadID], cands, linkPolicy)
	if !ok {
		return false, nil
	}
	for _, from := range plan.Absorb {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET thread_id = ? WHERE account_id = ? AND thread_id = ?`,
			plan.Canonical, r.accountID, from); err != nil {
			return false, fmt.Errorf("merge threads: %w", err)
		}
		// The board's case goes with its thread (board_maint.go).
		if err := mergeBoardCaseTx(ctx, tx, r.accountID, from, plan.Canonical); err != nil {
			return false, fmt.Errorf("merge threads: %w", err)
		}
	}
	return true, nil
}

// relinkTx rebuilds the references of a stored message from its columns
// and links it; SetMessageBody and the upgrade backfill use it.
func relinkTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	r := linkRow{id: id}
	var refs string
	err := tx.QueryRowContext(ctx, `SELECT account_id, thread_id, rfc_message_id, in_reply_to, references_json FROM messages WHERE id = ?`, id).
		Scan(&r.accountID, &r.threadID, &r.rfcID, &r.inReplyTo, &refs)
	if err != nil {
		return false, fmt.Errorf("read message for linking: %w", err)
	}
	if err := json.Unmarshal([]byte(refs), &r.references); err != nil {
		return false, fmt.Errorf("decode references of %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_refs WHERE message_id = ?`, id); err != nil {
		return false, fmt.Errorf("clear message references: %w", err)
	}
	if err := insertRefsTx(ctx, tx, id, r.accountID, r.inReplyTo, r.references); err != nil {
		return false, err
	}
	return linkMessageTx(ctx, tx, r)
}

// LinkThreadsBatch is the upgrade backfill: it links up to limit messages
// whose id sorts after afterID ("" starts from the beginning) and returns
// the last id visited ("" when nothing was left), how many rows it
// visited and how many changed thread. One transaction per batch, so it
// interleaves with a running sync.
func (s *Store) LinkThreadsBatch(ctx context.Context, afterID string, limit int) (lastID string, visited, linked int, err error) {
	if limit <= 0 {
		limit = 500
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, 0, fmt.Errorf("link threads: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM messages WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return "", 0, 0, fmt.Errorf("link threads: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", 0, 0, fmt.Errorf("link threads: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", 0, 0, fmt.Errorf("link threads: %w", err)
	}
	for _, id := range ids {
		changed, err := relinkTx(ctx, tx, id)
		if err != nil {
			return "", 0, 0, err
		}
		if changed {
			linked++
		}
	}
	if err := tx.Commit(); err != nil {
		return "", 0, 0, fmt.Errorf("link threads: %w", err)
	}
	if len(ids) == 0 {
		return "", 0, 0, nil
	}
	return ids[len(ids)-1], len(ids), linked, nil
}

// ThreadRow is one conversation as ListThreads and GetThread aggregate it
// over the members in scope (a folder, or the whole account).
type ThreadRow struct {
	ID, AccountID  string
	Latest         Message // the newest member in scope by (date, id)
	MessageCount   int
	UnreadCount    int
	Flags          []api.Flag    // union over the members in scope; sorted, never nil
	HasAttachments bool          // any member in scope
	Participants   []api.Address // distinct senders, newest first, at most api.MaxThreadParticipants; never nil
	FolderIDs      []string      // every folder of the account with a member; sorted, never nil
	// SentCount, in a folder's scope, counts the members in the account's
	// sent folders the folder lacks (sentScope, ThreadSentMessages); 0
	// account-wide.
	SentCount int
}

// threadGroup is one row of the grouped query.
type threadGroup struct {
	id            string
	count, unread int
	flagged, att  int
	latest        string // raw date stamp of the newest member
}

// ListThreads pages through the conversations that have a member in the
// folder, ordered by the date of their newest member there (ties by thread
// id). The cursor is bound to the sort order (prefixes "T"/"t", so a
// message.list cursor is ErrBadCursor) and does not encode the filter.
// sort "" means SortDateDesc; filter "" means api.FilterAll and matches
// the members' union (unread: any unread member, flagged: any flagged);
// limit <= 0 means 50. total counts the threads under the filter.
// ErrNotFound when the account has no such folder.
func (s *Store) ListThreads(ctx context.Context, accountID, folderID, cursor string, limit int, sortOrder api.SortOrder, listFilter api.MessageFilter) (items []ThreadRow, next string, total int, err error) {
	if limit <= 0 {
		limit = 50
	}
	var prefix, cmp, order string
	switch sortOrder {
	case "", api.SortDateDesc:
		prefix, cmp, order = "T", "<", "DESC"
	case api.SortDateAsc:
		prefix, cmp, order = "t", ">", "ASC"
	default:
		return nil, "", 0, fmt.Errorf("list threads: unsupported sort %q", sortOrder)
	}
	var cursorStamp, cursorID string
	if cursor != "" {
		if cursorStamp, cursorID, err = decodeSortCursor(cursor, prefix); err != nil {
			return nil, "", 0, err
		}
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE id = ? AND account_id = ?`, folderID, accountID).Scan(&exists); err != nil {
		return nil, "", 0, fmt.Errorf("list threads: %w", err)
	}
	if exists == 0 {
		return nil, "", 0, ErrNotFound
	}

	having := ""
	switch listFilter {
	case "", api.FilterAll:
	case api.FilterUnread:
		having = ` HAVING SUM(unread) > 0`
	case api.FilterFlagged:
		having = ` HAVING MAX(flagged) = 1`
	default:
		return nil, "", 0, fmt.Errorf("list threads: unsupported filter %q", listFilter)
	}
	grouped := threadGroupQuery(having)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+grouped+`)`, folderID, folderID).Scan(&total); err != nil {
		return nil, "", 0, fmt.Errorf("count threads: %w", err)
	}

	query := `WITH t AS (` + grouped + `) SELECT thread_id, n, unread, flagged, att, latest FROM t`
	args := []any{folderID, folderID}
	if cursor != "" {
		query += ` WHERE (latest ` + cmp + ` ? OR (latest = ? AND thread_id ` + cmp + ` ?))`
		args = append(args, cursorStamp, cursorStamp, cursorID)
	}
	query += ` ORDER BY latest ` + order + `, thread_id ` + order + ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list threads: %w", err)
	}
	var groups []threadGroup
	for rows.Next() {
		var g threadGroup
		if err := rows.Scan(&g.id, &g.count, &g.unread, &g.flagged, &g.att, &g.latest); err != nil {
			rows.Close()
			return nil, "", 0, fmt.Errorf("scan thread: %w", err)
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", 0, fmt.Errorf("list threads: %w", err)
	}
	if len(groups) > limit {
		groups = groups[:limit]
		next = encodeSortCursor(prefix, groups[limit-1].latest, groups[limit-1].id)
	}
	items, err = s.threadRows(ctx, accountID, folderID, groups)
	if err != nil {
		return nil, "", 0, err
	}
	return items, next, total, nil
}

// threadGroupQuery aggregates one folder's messages (the parameter, given
// twice) by thread: the folder/thread index serves the grouping in thread
// order and the page then sorts one row per thread. having narrows the
// threads. Hidden rows are left out by id, from the partial index of the
// hidden ones, so that the grouping still reads the covering index only.
func threadGroupQuery(having string) string {
	return `SELECT thread_id, COUNT(*) AS n, SUM(unread) AS unread, MAX(flagged) AS flagged,
			MAX(has_attachments) AS att, MAX(date) AS latest
		FROM messages WHERE folder_id = ? AND thread_id != ''
			AND id NOT IN (SELECT id FROM messages WHERE folder_id = ? AND hidden = 1)
		GROUP BY thread_id` + having
}

// notVirtual leaves out the rows in the account's virtual folders; its
// parameter is the account id.
const notVirtual = `folder_id NOT IN (SELECT id FROM folders WHERE account_id = ? AND virtual != '')`

// GetThread aggregates one conversation over its members in folderID, or
// over every member of the account outside virtual folders when folderID
// is "". ErrNotFound when nothing is in scope.
func (s *Store) GetThread(ctx context.Context, accountID, threadID, folderID string) (ThreadRow, error) {
	if threadID == "" {
		return ThreadRow{}, ErrNotFound
	}
	where, args := threadScope(accountID, threadID, folderID)
	var g threadGroup
	g.id = threadID
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(unread), 0), COALESCE(MAX(flagged), 0),
		COALESCE(MAX(has_attachments), 0), COALESCE(MAX(date), '') FROM messages`+where, args...).
		Scan(&g.count, &g.unread, &g.flagged, &g.att, &g.latest)
	if err != nil {
		return ThreadRow{}, fmt.Errorf("get thread: %w", err)
	}
	if g.count == 0 {
		return ThreadRow{}, ErrNotFound
	}
	rows, err := s.threadRows(ctx, accountID, folderID, []threadGroup{g})
	if err != nil {
		return ThreadRow{}, err
	}
	if len(rows) != 1 {
		return ThreadRow{}, ErrNotFound
	}
	return rows[0], nil
}

// ThreadMessages lists the members in scope oldest first; when more than
// limit exist the newest limit are returned, still oldest first. limit <= 0
// means api.MaxThreadMessages. folderID "" means every folder but the
// virtual ones. ErrNotFound when nothing is in scope.
func (s *Store) ThreadMessages(ctx context.Context, accountID, threadID, folderID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = api.MaxThreadMessages
	}
	if threadID == "" {
		return nil, ErrNotFound
	}
	where, args := threadScope(accountID, threadID, folderID)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM (SELECT `+messageColumns+` FROM messages`+where+
		` ORDER BY date DESC, id DESC LIMIT ?) ORDER BY date ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("thread messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("thread messages: %w", err)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// RetitleThread gives every row of the account's thread the subject (an
// issue renamed or moved to another key: its rows all carry "KEY:
// Summary"); the full-text index follows. ErrNotFound when the thread has
// no row.
func (s *Store) RetitleThread(ctx context.Context, accountID, threadID, subject string) error {
	if threadID == "" {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("retitle thread: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE account_id = ? AND thread_id = ?`, accountID, threadID).Scan(&n); err != nil {
		return fmt.Errorf("retitle thread: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET subject = ?, updated_at = ? WHERE account_id = ? AND thread_id = ? AND subject != ?`,
		subject, nowStamp(), accountID, threadID, subject); err != nil {
		return fmt.Errorf("retitle thread: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("retitle thread: %w", err)
	}
	return nil
}

// threadScope is the WHERE clause of one thread's visible members, in a
// folder or account-wide (without the virtual folders' copies).
func threadScope(accountID, threadID, folderID string) (string, []any) {
	where := ` WHERE account_id = ? AND thread_id = ? AND hidden = 0`
	args := []any{accountID, threadID}
	if folderID != "" {
		where += ` AND folder_id = ?`
		args = append(args, folderID)
	} else {
		where += ` AND ` + notVirtual
		args = append(args, accountID)
	}
	return where, args
}

// The user's own replies live in the sent folders, not in the folder a
// conversation is read in. In a folder's scope, a thread's sent members
// are its visible rows in the account's folders of role sent that the
// folder lacks: a row whose Message-ID header a visible member of the
// folder has (a Bcc to oneself, a Gmail label) is the folder's, and of the
// rows sharing a Message-ID only one counts. A row without a Message-ID is
// one sent member of its own. A sent folder and the outbox have none (the
// outbox holds the user's queued mail itself), nor does an issue-tracker
// account (no folder of role sent).

// sentMembers is the FROM and WHERE of the sent members of the threads of
// the account; its parameters are the account id twice and the folder id,
// and the caller appends its thread condition on s.thread_id.
const sentMembers = ` FROM messages s
	WHERE s.account_id = ? AND s.hidden = 0
		AND s.folder_id IN (SELECT id FROM folders WHERE account_id = ? AND role = '` + string(api.RoleSent) + `')
		AND (s.rfc_message_id = '' OR NOT EXISTS (SELECT 1 FROM messages f
			WHERE f.folder_id = ? AND f.thread_id = s.thread_id AND f.rfc_message_id = s.rfc_message_id AND f.hidden = 0))`

// sentScope reports whether the folder's threads can have sent members:
// it is not a sent folder, the outbox or a virtual folder. ErrNotFound when
// the account has no such folder.
func (s *Store) sentScope(ctx context.Context, accountID, folderID string) (bool, error) {
	var role, virtual string
	err := s.db.QueryRowContext(ctx, `SELECT role, virtual FROM folders WHERE id = ? AND account_id = ?`, folderID, accountID).Scan(&role, &virtual)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("folder role: %w", err)
	}
	r := api.FolderRole(role)
	return r != api.RoleSent && r != api.RoleOutbox && virtual == "", nil
}

// ThreadSentMessages lists the sent members of a thread read in folderID
// (see sentMembers), oldest first; when more than limit exist the newest
// limit are returned, still oldest first. Of the rows sharing a Message-ID
// the one with the lowest id stands for them. limit <= 0 means
// api.MaxThreadMessages. Empty (never ErrNotFound) when there is none or
// the folder has none (sentScope); ErrNotFound when the account has no
// such folder.
func (s *Store) ThreadSentMessages(ctx context.Context, accountID, threadID, folderID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = api.MaxThreadMessages
	}
	ok, err := s.sentScope(ctx, accountID, folderID)
	if err != nil || !ok || threadID == "" {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM (
			SELECT `+messageColumns+` FROM (
				SELECT s.*, ROW_NUMBER() OVER (
					PARTITION BY CASE WHEN s.rfc_message_id = '' THEN 'id:' || s.id ELSE 'mid:' || s.rfc_message_id END
					ORDER BY s.id) AS rn`+sentMembers+` AND s.thread_id = ?)
			WHERE rn = 1 ORDER BY date DESC, id DESC LIMIT ?)
		ORDER BY date ASC, id ASC`, accountID, accountID, folderID, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("thread sent messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("thread sent messages: %w", err)
	}
	return out, nil
}

// sentCounts counts the sent members (sentMembers) of the threads read in
// folderID, each Message-ID once; one query for the ids given (a page of
// thread.list is within one chunk).
func (s *Store) sentCounts(ctx context.Context, accountID, folderID string, ids []string) (map[string]int, error) {
	out := map[string]int{}
	ok, err := s.sentScope(ctx, accountID, folderID)
	if err != nil || !ok {
		return out, err
	}
	for _, chunk := range chunkStrings(ids, linkChunk) {
		args := append([]any{accountID, accountID, folderID}, toAny(chunk)...)
		rows, err := s.db.QueryContext(ctx, `SELECT s.thread_id,
				COUNT(DISTINCT NULLIF(s.rfc_message_id, '')) + SUM(s.rfc_message_id = '')`+sentMembers+
			` AND s.thread_id IN (`+inPlaceholders(len(chunk))+`) GROUP BY s.thread_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("thread sent counts: %w", err)
		}
		for rows.Next() {
			var tid string
			var n int
			if err := rows.Scan(&tid, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread sent count: %w", err)
			}
			out[tid] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread sent counts: %w", err)
		}
	}
	return out, nil
}

// threadRows completes the grouped rows with the newest member, the
// participants, the flag union, the folders and, in a folder's scope, the
// sent count, in the order given. folderID "" scopes the details to the
// whole account but its virtual folders; the folders are every one with a
// visible member.
func (s *Store) threadRows(ctx context.Context, accountID, folderID string, groups []threadGroup) ([]ThreadRow, error) {
	out := make([]ThreadRow, 0, len(groups))
	if len(groups) == 0 {
		return out, nil
	}
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.id
	}
	type detail struct {
		latestID     string
		participants []api.Address
		flags        []api.Flag
		folders      []string
	}
	details := make(map[string]*detail, len(groups))
	get := func(id string) *detail {
		d := details[id]
		if d == nil {
			d = &detail{}
			details[id] = d
		}
		return d
	}
	scope := ` WHERE account_id = ? AND hidden = 0`
	if folderID != "" {
		scope += ` AND folder_id = ?`
	} else {
		scope += ` AND ` + notVirtual
	}
	scopeArgs := func(chunk []string) []any {
		args := []any{accountID}
		if folderID != "" {
			args = append(args, folderID)
		} else {
			args = append(args, accountID)
		}
		for _, id := range chunk {
			args = append(args, id)
		}
		return args
	}

	for _, chunk := range chunkStrings(ids, linkChunk) {
		in := ` AND thread_id IN (` + inPlaceholders(len(chunk)) + `)`
		// Newest members first: the head and the participant list.
		rows, err := s.db.QueryContext(ctx, `SELECT thread_id, id, from_json FROM (
				SELECT thread_id, id, from_json,
					ROW_NUMBER() OVER (PARTITION BY thread_id ORDER BY date DESC, id DESC) AS rn
				FROM messages`+scope+in+`) WHERE rn <= ? ORDER BY thread_id, rn`,
			append(scopeArgs(chunk), threadDetailRows)...)
		if err != nil {
			return nil, fmt.Errorf("thread details: %w", err)
		}
		seen := map[string]map[string]int{}
		for rows.Next() {
			var tid, id, from string
			if err := rows.Scan(&tid, &id, &from); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread detail: %w", err)
			}
			d := get(tid)
			if d.latestID == "" {
				d.latestID = id
			}
			var addrs []api.Address
			if err := json.Unmarshal([]byte(from), &addrs); err != nil {
				rows.Close()
				return nil, fmt.Errorf("decode senders of %s: %w", id, err)
			}
			if seen[tid] == nil {
				seen[tid] = map[string]int{}
			}
			d.participants = addParticipants(d.participants, seen[tid], addrs)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread details: %w", err)
		}

		rows, err = s.db.QueryContext(ctx, `SELECT m.thread_id, j.value FROM messages m, json_each(m.flags) j`+
			strings.Replace(scope, "account_id", "m.account_id", 1)+strings.Replace(in, "thread_id", "m.thread_id", 1)+
			` GROUP BY m.thread_id, j.value ORDER BY m.thread_id, j.value`, scopeArgs(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("thread flags: %w", err)
		}
		for rows.Next() {
			var tid, flag string
			if err := rows.Scan(&tid, &flag); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread flag: %w", err)
			}
			if flag != "" {
				d := get(tid)
				d.flags = append(d.flags, api.Flag(flag))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread flags: %w", err)
		}

		rows, err = s.db.QueryContext(ctx, `SELECT thread_id, folder_id FROM messages WHERE account_id = ? AND hidden = 0`+in+
			` GROUP BY thread_id, folder_id ORDER BY thread_id, folder_id`, append([]any{accountID}, toAny(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("thread folders: %w", err)
		}
		for rows.Next() {
			var tid, fid string
			if err := rows.Scan(&tid, &fid); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread folder: %w", err)
			}
			d := get(tid)
			d.folders = append(d.folders, fid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread folders: %w", err)
		}
	}

	var sentCount map[string]int
	if folderID != "" {
		var err error
		if sentCount, err = s.sentCounts(ctx, accountID, folderID, ids); err != nil {
			return nil, err
		}
	}

	// The newest members in full.
	heads := make([]string, 0, len(groups))
	for _, g := range groups {
		if d := details[g.id]; d != nil && d.latestID != "" {
			heads = append(heads, d.latestID)
		}
	}
	latest := make(map[string]Message, len(heads))
	for _, chunk := range chunkStrings(heads, linkChunk) {
		rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("thread heads: %w", err)
		}
		for rows.Next() {
			m, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread head: %w", err)
			}
			latest[m.ID] = m
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread heads: %w", err)
		}
	}

	for _, g := range groups {
		d := details[g.id]
		if d == nil {
			continue // vanished between the queries
		}
		head, ok := latest[d.latestID]
		if !ok {
			continue
		}
		row := ThreadRow{
			ID: g.id, AccountID: accountID, Latest: head,
			MessageCount: g.count, UnreadCount: g.unread,
			Flags: normalizeFlags(d.flags), HasAttachments: g.att != 0,
			Participants: d.participants, FolderIDs: d.folders,
			SentCount: sentCount[g.id],
		}
		if row.Participants == nil {
			row.Participants = []api.Address{}
		}
		if row.FolderIDs == nil {
			row.FolderIDs = []string{}
		}
		sort.Strings(row.FolderIDs)
		out = append(out, row)
	}
	return out, nil
}

// addParticipants folds the senders of one message (newest messages first)
// into the list: each address once, compared case-insensitively, the
// display name from the first occurrence that has one, at most
// api.MaxThreadParticipants entries. seen maps the key to the list index.
func addParticipants(list []api.Address, seen map[string]int, addrs []api.Address) []api.Address {
	for _, a := range addrs {
		key := strings.ToLower(strings.TrimSpace(a.Address))
		name := strings.TrimSpace(a.Name)
		if key == "" {
			if name == "" {
				continue
			}
			key = "name:" + strings.ToLower(name)
		}
		if i, ok := seen[key]; ok {
			if list[i].Name == "" && name != "" {
				list[i].Name = name
			}
			continue
		}
		if len(list) >= api.MaxThreadParticipants {
			continue
		}
		seen[key] = len(list)
		list = append(list, api.Address{Name: name, Address: strings.TrimSpace(a.Address)})
	}
	return list
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
