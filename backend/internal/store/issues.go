// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Issue-tracker accounts (kind jira, migration 0015). An issue is a thread
// (IssueThreadID) whose items, the description, the comments and the
// events, are ordinary messages rows of the space's folder, with a copy in
// every virtual folder (Folder.Virtual) the issue belongs to; the copies
// of an item share its remote id ("i:<issue>", "c:<comment>",
// "h:<history>"). The tables here hold what those rows are built from:
// internal/jira writes them, core reads the projections (IssueDecorations,
// IssuesByThread). Functions that delete rows recount the folders they
// touch.

const (
	// IssueThreadPrefix starts the thread id of an issue's rows. It is
	// not a local thread id (thread.IsLocalID): the linker leaves such
	// rows alone.
	IssueThreadPrefix = "jira:"
	// MetaIssueMePrefix + account id is the meta key under which the
	// syncer keeps the site's view of the user; DeleteAccount removes it.
	MetaIssueMePrefix = "issues.me."
	// MetaIssueMailPrefix + account id is the meta key under which core
	// records what the account's notification mail was last judged for;
	// DeleteAccount removes it.
	MetaIssueMailPrefix = "issues.mail.settled."
)

// IssueThreadID is the thread id of the rows of an issue.
func IssueThreadID(issueID string) string { return IssueThreadPrefix + issueID }

// IssueIDOfThread is the issue id of an issue's thread id; false for any
// other thread id.
func IssueIDOfThread(threadID string) (string, bool) {
	id, ok := strings.CutPrefix(threadID, IssueThreadPrefix)
	return id, ok && id != ""
}

// IssueSpace is a row of issue_spaces: one space (a Jira project) of an
// issue-tracker account as the site last listed it. Key and Name are the
// site's display text.
type IssueSpace struct {
	AccountID   string
	SpaceID     string // folders.mailbox of its folder = "space:" + SpaceID
	Key         string
	Name        string
	ServiceDesk bool
}

// SetIssueSpaces replaces the account's spaces with spaces (their
// AccountID is ignored). A space id listed twice or an empty one is an
// error and nothing is changed.
func (s *Store) SetIssueSpaces(ctx context.Context, accountID string, spaces []IssueSpace) error {
	seen := make(map[string]bool, len(spaces))
	for _, sp := range spaces {
		if sp.SpaceID == "" {
			return fmt.Errorf("set issue spaces: empty space id")
		}
		if seen[sp.SpaceID] {
			return fmt.Errorf("set issue spaces: duplicate space id %q", sp.SpaceID)
		}
		seen[sp.SpaceID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set issue spaces: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_spaces WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("set issue spaces: %w", err)
	}
	for _, sp := range spaces {
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_spaces (account_id, space_id, key, name, service_desk)
			VALUES (?, ?, ?, ?, ?)`, accountID, sp.SpaceID, sp.Key, sp.Name, boolInt(sp.ServiceDesk)); err != nil {
			return fmt.Errorf("set issue spaces: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set issue spaces: %w", err)
	}
	return nil
}

// IssueSpaces returns the account's spaces by key.
func (s *Store) IssueSpaces(ctx context.Context, accountID string) ([]IssueSpace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, space_id, key, name, service_desk FROM issue_spaces
		WHERE account_id = ? ORDER BY key, space_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list issue spaces: %w", err)
	}
	defer rows.Close()
	var out []IssueSpace
	for rows.Next() {
		var sp IssueSpace
		var desk int
		if err := rows.Scan(&sp.AccountID, &sp.SpaceID, &sp.Key, &sp.Name, &desk); err != nil {
			return nil, fmt.Errorf("scan issue space: %w", err)
		}
		sp.ServiceDesk = desk != 0
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issue spaces: %w", err)
	}
	return out, nil
}

// Issue is a row of issues: an issue as the syncer last read it. Every
// string but the ids is the site's display text.
type Issue struct {
	AccountID      string
	IssueID        string // the site's id; the rows' thread is IssueThreadID(IssueID)
	Key            string // "ITSD-42"; unique per account
	SpaceID        string
	Summary        string
	StatusID       string
	Status         string
	StatusCategory api.IssueStatusCategory
	Type           string
	Priority       string
	AssigneeID     string
	AssigneeName   string
	ReporterID     string
	ReporterName   string
	Watching       bool
	ServiceDesk    bool
	ViaMail        bool // kept because a notification mail named it, whatever onlyMine says
	Created        time.Time
	Updated        time.Time // the server's, as last seen
	// SyncedUpdated is the Updated the stored rows reflect: a different
	// Updated means the issue owes a refresh.
	SyncedUpdated time.Time
	// RenderKey identifies the rendering settings the rows were built with
	// (a hash the syncer computes): another one means a rebuild.
	RenderKey string
	// Views are the virtual folders the issue has copies in.
	Views []api.VirtualFolder
}

// IssueStamp is what the syncer compares to decide whether an issue owes
// a refresh (ListIssueStamps).
type IssueStamp struct {
	Updated       time.Time
	SyncedUpdated time.Time
	RenderKey     string
	Views         []api.VirtualFolder
}

const issueColumns = `account_id, issue_id, key, space_id, summary, status_id, status, status_category,
	type, priority, assignee_id, assignee_name, reporter_id, reporter_name, watching, service_desk, via_mail,
	created, updated, synced_updated, render_key, views`

// issueKeyPlaceholder marks a stale row whose key another issue took
// (PutIssue); no key of the site starts with it.
const issueKeyPlaceholder = "~"

// PutIssue writes an issue, replacing the stored row of its id. Should
// another stored issue of the account hold the key (it moved away and the
// store has not seen that yet), that row keeps its place under a
// placeholder key that IssueByKey never finds, until its own next refresh.
func (s *Store) PutIssue(ctx context.Context, is Issue) error {
	if is.AccountID == "" || is.IssueID == "" || is.Key == "" {
		return fmt.Errorf("put issue: account id, issue id and key are required")
	}
	views, err := encodeJSON(is.Views, "[]")
	if err != nil {
		return fmt.Errorf("encode issue views: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put issue: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE issues SET key = ? || issue_id WHERE account_id = ? AND key = ? AND issue_id != ?`,
		issueKeyPlaceholder, is.AccountID, is.Key, is.IssueID); err != nil {
		return fmt.Errorf("put issue: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issues (`+issueColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, issue_id) DO UPDATE SET
			key = excluded.key, space_id = excluded.space_id, summary = excluded.summary,
			status_id = excluded.status_id, status = excluded.status, status_category = excluded.status_category,
			type = excluded.type, priority = excluded.priority, assignee_id = excluded.assignee_id,
			assignee_name = excluded.assignee_name, reporter_id = excluded.reporter_id,
			reporter_name = excluded.reporter_name, watching = excluded.watching,
			service_desk = excluded.service_desk, via_mail = excluded.via_mail, created = excluded.created,
			updated = excluded.updated, synced_updated = excluded.synced_updated,
			render_key = excluded.render_key, views = excluded.views`,
		is.AccountID, is.IssueID, is.Key, is.SpaceID, is.Summary, is.StatusID, is.Status, string(is.StatusCategory),
		is.Type, is.Priority, is.AssigneeID, is.AssigneeName, is.ReporterID, is.ReporterName,
		boolInt(is.Watching), boolInt(is.ServiceDesk), boolInt(is.ViaMail),
		optStamp(is.Created), optStamp(is.Updated), optStamp(is.SyncedUpdated), is.RenderKey, views); err != nil {
		return fmt.Errorf("put issue %s: %w", is.Key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put issue: %w", err)
	}
	return nil
}

// GetIssue returns one issue of the account; ErrNotFound otherwise.
func (s *Store) GetIssue(ctx context.Context, accountID, issueID string) (Issue, error) {
	return s.oneIssue(ctx, `issue_id = ?`, accountID, issueID)
}

// IssueByKey returns the account's issue with the key (exact match);
// ErrNotFound otherwise.
func (s *Store) IssueByKey(ctx context.Context, accountID, key string) (Issue, error) {
	return s.oneIssue(ctx, `key = ?`, accountID, key)
}

func (s *Store) oneIssue(ctx context.Context, cond, accountID, value string) (Issue, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+issueColumns+` FROM issues WHERE account_id = ? AND `+cond, accountID, value)
	is, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, fmt.Errorf("get issue: %w", err)
	}
	return is, nil
}

// IssuesByID returns the account's issues among ids, keyed by issue id;
// unknown ids are absent.
func (s *Store) IssuesByID(ctx context.Context, accountID string, ids []string) (map[string]Issue, error) {
	out := make(map[string]Issue, len(ids))
	for _, chunk := range chunkStrings(dedupeStrings(ids), 500) {
		rows, err := s.db.QueryContext(ctx, `SELECT `+issueColumns+` FROM issues WHERE account_id = ? AND issue_id IN (`+
			inPlaceholders(len(chunk))+`)`, append([]any{accountID}, toAny(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
		for rows.Next() {
			is, err := scanIssue(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan issue: %w", err)
			}
			out[is.IssueID] = is
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
	}
	return out, nil
}

// IssuesByThread returns the issues whose threads are among threadIDs,
// keyed by thread id; a thread id that names no issue of the account
// (another account kind's, or unknown) is absent.
func (s *Store) IssuesByThread(ctx context.Context, accountID string, threadIDs []string) (map[string]Issue, error) {
	var ids []string
	for _, tid := range threadIDs {
		if id, ok := IssueIDOfThread(tid); ok {
			ids = append(ids, id)
		}
	}
	out := map[string]Issue{}
	if len(ids) == 0 {
		return out, nil
	}
	byID, err := s.IssuesByID(ctx, accountID, ids)
	if err != nil {
		return nil, err
	}
	for id, is := range byID {
		out[IssueThreadID(id)] = is
	}
	return out, nil
}

// ListIssueStamps returns what the syncer compares of every stored issue
// of the account, keyed by issue id.
func (s *Store) ListIssueStamps(ctx context.Context, accountID string) (map[string]IssueStamp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id, updated, synced_updated, render_key, views FROM issues WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list issue stamps: %w", err)
	}
	defer rows.Close()
	out := map[string]IssueStamp{}
	for rows.Next() {
		var id, updated, synced, views string
		var st IssueStamp
		if err := rows.Scan(&id, &updated, &synced, &st.RenderKey, &views); err != nil {
			return nil, fmt.Errorf("scan issue stamp: %w", err)
		}
		if err := json.Unmarshal([]byte(views), &st.Views); err != nil {
			return nil, fmt.Errorf("decode views of issue %s: %w", id, err)
		}
		st.Updated, st.SyncedUpdated = parseStamp(updated), parseStamp(synced)
		out[id] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issue stamps: %w", err)
	}
	return out, nil
}

// IssuesUpdatedBefore returns the ids of the account's issues last updated
// before t, sorted: the candidates for leaving the retention window. The
// caller keeps whichever it must (open issues assigned to the user). An
// issue whose update time is unknown is never a candidate.
func (s *Store) IssuesUpdatedBefore(ctx context.Context, accountID string, t time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id FROM issues WHERE account_id = ? AND updated != '' AND updated < ?
		ORDER BY issue_id`, accountID, stamp(t))
	if err != nil {
		return nil, fmt.Errorf("list old issues: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan old issue: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list old issues: %w", err)
	}
	return out, nil
}

// DeleteIssues forgets issues of the account: their rows in every folder
// (with their pending operations; the raw files after the commit) but the
// outbox's (a queued comment keeps its row, and its delivery reports what
// became of the issue), their
// items and the issue rows, and it shows again the mail of other accounts
// hidden because of them (issue_mail_links, which stay). Every folder
// touched is recounted. It returns the mail folders whose hidden rows
// were shown again, by account id, each list sorted. Unknown ids are
// ignored.
func (s *Store) DeleteIssues(ctx context.Context, accountID string, ids []string) (map[string][]string, error) {
	ids = dedupeStrings(ids)
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("delete issues: %w", err)
	}
	defer tx.Rollback()

	var files []messageFile
	folders := map[string]bool{}
	var keys []string
	for _, chunk := range chunkStrings(ids, 500) {
		in := inPlaceholders(len(chunk))
		idArgs := append([]any{accountID}, toAny(chunk)...)
		threadArgs := []any{accountID}
		for _, id := range chunk {
			threadArgs = append(threadArgs, IssueThreadID(id))
		}
		got, err := stringColumn(ctx, tx, `SELECT key FROM issues WHERE account_id = ? AND issue_id IN (`+in+`)`, idArgs...)
		if err != nil {
			return nil, fmt.Errorf("delete issues: %w", err)
		}
		keys = append(keys, got...)
		fids, err := stringColumn(ctx, tx, `SELECT DISTINCT folder_id FROM messages WHERE account_id = ? AND remote_id != '' AND thread_id IN (`+in+`)`, threadArgs...)
		if err != nil {
			return nil, fmt.Errorf("delete issues: %w", err)
		}
		for _, f := range fids {
			folders[f] = true
		}
		mf, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages WHERE account_id = ? AND remote_id != '' AND thread_id IN (`+in+`)`, threadArgs...)
		if err != nil {
			return nil, err
		}
		files = append(files, mf...)
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_items WHERE account_id = ? AND issue_id IN (`+in+`)`, idArgs...); err != nil {
			return nil, fmt.Errorf("delete issue items: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issues WHERE account_id = ? AND issue_id IN (`+in+`)`, idArgs...); err != nil {
			return nil, fmt.Errorf("delete issues: %w", err)
		}
	}
	if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
		return nil, err
	}
	if err := recountFoldersTx(ctx, tx, folders); err != nil {
		return nil, err
	}
	// The links name the issue by id, or by key only (a mail matched
	// before its issue was stored).
	touched := map[string][]string{}
	for _, chunk := range chunkStrings(ids, 500) {
		if err := unhideLinkedMailTx(ctx, tx, `l.issue_account_id = ? AND l.issue_id IN (`+inPlaceholders(len(chunk))+`)`,
			touched, append([]any{accountID}, toAny(chunk)...)...); err != nil {
			return nil, err
		}
	}
	for _, chunk := range chunkStrings(keys, 500) {
		if err := unhideLinkedMailTx(ctx, tx, `l.issue_account_id = ? AND l.issue_key IN (`+inPlaceholders(len(chunk))+`)`,
			touched, append([]any{accountID}, toAny(chunk)...)...); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("delete issues: %w", err)
	}
	s.removeMessageFiles(files)
	return touched, nil
}

// IssueItem is a row of issue_items: one item of an issue, which every
// copy of it (the account's messages rows with RemoteID) shows.
type IssueItem struct {
	AccountID  string
	RemoteID   string // "i:<issue>" | "c:<comment>" | "h:<history>"
	IssueID    string
	Kind       api.IssueItemKind
	Visibility api.CommentVisibility
	AuthorID   string
	Via        string // the relaying integration of a re-attributed comment
	Changes    []api.IssueChange
	Edited     bool
	Updated    time.Time
}

const issueItemColumns = `account_id, remote_id, issue_id, kind, visibility, author_id, via, changes, edited, updated`

// PutIssueItems writes items of the account (their AccountID is ignored),
// replacing stored ones with the same remote id, in one transaction.
func (s *Store) PutIssueItems(ctx context.Context, accountID string, items []IssueItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put issue items: %w", err)
	}
	defer tx.Rollback()
	for _, it := range items {
		if it.RemoteID == "" || it.IssueID == "" {
			return fmt.Errorf("put issue items: remote id and issue id are required")
		}
		changes, err := encodeJSON(it.Changes, "[]")
		if err != nil {
			return fmt.Errorf("encode issue changes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_items (`+issueItemColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, remote_id) DO UPDATE SET
				issue_id = excluded.issue_id, kind = excluded.kind, visibility = excluded.visibility,
				author_id = excluded.author_id, via = excluded.via, changes = excluded.changes,
				edited = excluded.edited, updated = excluded.updated`,
			accountID, it.RemoteID, it.IssueID, string(it.Kind), string(it.Visibility), it.AuthorID, it.Via,
			changes, boolInt(it.Edited), optStamp(it.Updated)); err != nil {
			return fmt.Errorf("put issue item %s: %w", it.RemoteID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put issue items: %w", err)
	}
	return nil
}

// IssueItems returns the items of one issue by remote id.
func (s *Store) IssueItems(ctx context.Context, accountID, issueID string) ([]IssueItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+issueItemColumns+` FROM issue_items
		WHERE account_id = ? AND issue_id = ? ORDER BY remote_id`, accountID, issueID)
	if err != nil {
		return nil, fmt.Errorf("list issue items: %w", err)
	}
	defer rows.Close()
	var out []IssueItem
	for rows.Next() {
		it, err := scanIssueItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issue items: %w", err)
	}
	return out, nil
}

// DeleteIssueItems removes items of the account (a deleted comment) with
// every row showing them, their pending operations and raw files (files
// after the commit); the folders are recounted. Unknown ids are ignored.
func (s *Store) DeleteIssueItems(ctx context.Context, accountID string, remoteIDs []string) error {
	var ids []string
	for _, id := range dedupeStrings(remoteIDs) {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete issue items: %w", err)
	}
	defer tx.Rollback()
	var files []messageFile
	folders := map[string]bool{}
	for _, chunk := range chunkStrings(ids, 500) {
		in := inPlaceholders(len(chunk))
		args := append([]any{accountID}, toAny(chunk)...)
		fids, err := stringColumn(ctx, tx, `SELECT DISTINCT folder_id FROM messages
			WHERE account_id = ? AND remote_id != '' AND remote_id IN (`+in+`)`, args...)
		if err != nil {
			return fmt.Errorf("delete issue items: %w", err)
		}
		for _, f := range fids {
			folders[f] = true
		}
		mf, err := listMessageFiles(ctx, tx, `SELECT account_id, id FROM messages
			WHERE account_id = ? AND remote_id != '' AND remote_id IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		files = append(files, mf...)
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_items WHERE account_id = ? AND remote_id IN (`+in+`)`, args...); err != nil {
			return fmt.Errorf("delete issue items: %w", err)
		}
	}
	if err := deleteMessageRowsTx(ctx, tx, files); err != nil {
		return err
	}
	if err := recountFoldersTx(ctx, tx, folders); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete issue items: %w", err)
	}
	s.removeMessageFiles(files)
	return nil
}

// IssueRow is one stored copy of an item of an issue, as the syncer needs
// it to refresh the issue.
type IssueRow struct {
	ID        string
	FolderID  string
	RemoteID  string
	Flags     []api.Flag
	BodyState BodyState
	RawState  RawState
}

// IssueRows returns every row of an issue's items in the account, by
// folder and remote id. The thread's rows without a remote id — a comment
// queued in the outbox, which joined the thread by its In-Reply-To — are
// no item's and not listed.
func (s *Store) IssueRows(ctx context.Context, accountID, issueID string) ([]IssueRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, folder_id, remote_id, flags, body_state, raw_state FROM messages
		WHERE account_id = ? AND thread_id = ? AND remote_id != '' ORDER BY folder_id, remote_id, id`, accountID, IssueThreadID(issueID))
	if err != nil {
		return nil, fmt.Errorf("list issue rows: %w", err)
	}
	defer rows.Close()
	var out []IssueRow
	for rows.Next() {
		var r IssueRow
		var flags, body, raw string
		if err := rows.Scan(&r.ID, &r.FolderID, &r.RemoteID, &flags, &body, &raw); err != nil {
			return nil, fmt.Errorf("scan issue row: %w", err)
		}
		if r.Flags, err = decodeFlags(flags); err != nil {
			return nil, fmt.Errorf("decode flags of %s: %w", r.ID, err)
		}
		r.BodyState, r.RawState = BodyState(body), RawState(raw)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issue rows: %w", err)
	}
	return out, nil
}

// IssueDecoration is what a message of an issue-tracker account is: the
// issue and the item it shows.
type IssueDecoration struct {
	Issue Issue
	Item  IssueItem
}

// IssueDecorations returns, keyed by remote id, the item and the issue of
// every remote id among remoteIDs that the account has an item of (and
// that item's issue); others are absent.
func (s *Store) IssueDecorations(ctx context.Context, accountID string, remoteIDs []string) (map[string]IssueDecoration, error) {
	out := map[string]IssueDecoration{}
	var ids []string
	for _, id := range dedupeStrings(remoteIDs) {
		if id != "" {
			ids = append(ids, id)
		}
	}
	for _, chunk := range chunkStrings(ids, 500) {
		rows, err := s.db.QueryContext(ctx, `SELECT `+qualifiedColumns(issueItemColumns, "it")+`, `+qualifiedColumns(issueColumns, "i")+`
			FROM issue_items it JOIN issues i ON i.account_id = it.account_id AND i.issue_id = it.issue_id
			WHERE it.account_id = ? AND it.remote_id IN (`+inPlaceholders(len(chunk))+`)`,
			append([]any{accountID}, toAny(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("issue decorations: %w", err)
		}
		for rows.Next() {
			var d IssueDecoration
			var itemExtra itemScan
			var issueExtra issueScan
			dest := append(itemExtra.dest(&d.Item), issueExtra.dest(&d.Issue)...)
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan issue decoration: %w", err)
			}
			if err := itemExtra.finish(&d.Item); err != nil {
				rows.Close()
				return nil, err
			}
			if err := issueExtra.finish(&d.Issue); err != nil {
				rows.Close()
				return nil, err
			}
			out[d.Item.RemoteID] = d
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("issue decorations: %w", err)
		}
	}
	return out, nil
}

// unhideLinkedMailTx shows again the hidden messages whose issue_mail_links
// row (alias l) matches where, recounts their folders and adds them to
// touched (account id → sorted folder ids).
func unhideLinkedMailTx(ctx context.Context, tx *sql.Tx, where string, touched map[string][]string, args ...any) error {
	return setHiddenTx(ctx, tx, false,
		`m.hidden = 1 AND m.id IN (SELECT l.message_id FROM issue_mail_links l WHERE `+where+`)`, touched, args...)
}

// stringColumn runs a query of one text column and returns its values.
func stringColumn(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// qualifiedColumns prefixes every column of a column list with alias.
func qualifiedColumns(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// issueScan holds the columns of issueColumns that need decoding.
type issueScan struct {
	category, created, updated, synced, views string
	watching, desk, viaMail                   int
}

func (x *issueScan) dest(is *Issue) []any {
	return []any{&is.AccountID, &is.IssueID, &is.Key, &is.SpaceID, &is.Summary, &is.StatusID, &is.Status, &x.category,
		&is.Type, &is.Priority, &is.AssigneeID, &is.AssigneeName, &is.ReporterID, &is.ReporterName,
		&x.watching, &x.desk, &x.viaMail, &x.created, &x.updated, &x.synced, &is.RenderKey, &x.views}
}

func (x *issueScan) finish(is *Issue) error {
	is.StatusCategory = api.IssueStatusCategory(x.category)
	is.Watching, is.ServiceDesk, is.ViaMail = x.watching != 0, x.desk != 0, x.viaMail != 0
	is.Created, is.Updated, is.SyncedUpdated = parseStamp(x.created), parseStamp(x.updated), parseStamp(x.synced)
	if err := json.Unmarshal([]byte(x.views), &is.Views); err != nil {
		return fmt.Errorf("decode views of issue %s: %w", is.IssueID, err)
	}
	return nil
}

func scanIssue(row scanner) (Issue, error) {
	var is Issue
	var x issueScan
	if err := row.Scan(x.dest(&is)...); err != nil {
		return Issue{}, err
	}
	if err := x.finish(&is); err != nil {
		return Issue{}, err
	}
	return is, nil
}

// itemScan holds the columns of issueItemColumns that need decoding.
type itemScan struct {
	kind, visibility, changes, updated string
	edited                             int
}

func (x *itemScan) dest(it *IssueItem) []any {
	return []any{&it.AccountID, &it.RemoteID, &it.IssueID, &x.kind, &x.visibility, &it.AuthorID, &it.Via,
		&x.changes, &x.edited, &x.updated}
}

func (x *itemScan) finish(it *IssueItem) error {
	it.Kind, it.Visibility = api.IssueItemKind(x.kind), api.CommentVisibility(x.visibility)
	it.Edited, it.Updated = x.edited != 0, parseStamp(x.updated)
	if err := json.Unmarshal([]byte(x.changes), &it.Changes); err != nil {
		return fmt.Errorf("decode changes of item %s: %w", it.RemoteID, err)
	}
	return nil
}

func scanIssueItem(row scanner) (IssueItem, error) {
	var it IssueItem
	var x itemScan
	if err := row.Scan(x.dest(&it)...); err != nil {
		return IssueItem{}, fmt.Errorf("scan issue item: %w", err)
	}
	if err := x.finish(&it); err != nil {
		return IssueItem{}, err
	}
	return it, nil
}
