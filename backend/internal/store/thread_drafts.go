// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A draft belongs to the conversation of the message it replies to: the
// user's unsent reply, and on an issue-tracker account the unsent comment
// on the issue. Drafts carry no thread id, so the link is in_reply_to, a
// stored message of the account whose thread_id names the conversation
// whatever folder holds it (a jira item's copies in the virtual views
// share the thread). A forward replies to nothing and belongs to none. A
// board case's suggested reply is the board's (it is edited there) and is
// left out. In a Drafts folder a draft's uploaded copy is a member of its
// thread already, and the outbox holds sent mail, so their threads have
// none.

// draftMembers is the FROM and WHERE of the drafts of the account's
// threads; its parameter is the account id, and the caller appends its
// thread condition on m.thread_id.
const draftMembers = ` FROM drafts d JOIN messages m ON m.id = d.in_reply_to AND m.account_id = d.account_id
	WHERE d.account_id = ? AND d.in_reply_to != ''
		AND NOT EXISTS (SELECT 1 FROM board_cases c WHERE c.draft_id = d.id)`

// draftScope reports whether the folder's threads can have drafts: it is
// not a Drafts folder or the outbox. ErrNotFound when the account has no
// such folder.
func (s *Store) draftScope(ctx context.Context, accountID, folderID string) (bool, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM folders WHERE id = ? AND account_id = ?`, folderID, accountID).Scan(&role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("folder role: %w", err)
	}
	r := api.FolderRole(role)
	return r != api.RoleDrafts && r != api.RoleOutbox, nil
}

// ThreadDrafts lists the drafts of a thread read in folderID (see
// draftMembers) with their attachments, newest updated first, at most
// limit (<= 0: api.MaxThreadDrafts). Empty (never ErrNotFound) when there
// is none or the folder has none (draftScope); ErrNotFound when the
// account has no such folder.
func (s *Store) ThreadDrafts(ctx context.Context, accountID, threadID, folderID string, limit int) ([]Draft, error) {
	if limit <= 0 {
		limit = api.MaxThreadDrafts
	}
	ok, err := s.draftScope(ctx, accountID, folderID)
	if err != nil || !ok || threadID == "" {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id IN (SELECT d.id`+draftMembers+
		` AND m.thread_id = ?) ORDER BY updated_at DESC, id DESC LIMIT ?`, accountID, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("thread drafts: %w", err)
	}
	var out []Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan draft: %w", err)
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("thread drafts: %w", err)
	}
	for i := range out {
		if out[i].Attachments, err = attachmentsForDraft(ctx, s.db, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// draftCounts counts the drafts (draftMembers) of the threads read in
// folderID; one query a chunk of ids.
func (s *Store) draftCounts(ctx context.Context, accountID, folderID string, ids []string) (map[string]int, error) {
	out := map[string]int{}
	ok, err := s.draftScope(ctx, accountID, folderID)
	if err != nil || !ok {
		return out, err
	}
	for _, chunk := range chunkStrings(ids, linkChunk) {
		args := append([]any{accountID}, toAny(chunk)...)
		rows, err := s.db.QueryContext(ctx, `SELECT m.thread_id, COUNT(*)`+draftMembers+
			` AND m.thread_id IN (`+inPlaceholders(len(chunk))+`) GROUP BY m.thread_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("thread draft counts: %w", err)
		}
		for rows.Next() {
			var tid string
			var n int
			if err := rows.Scan(&tid, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread draft count: %w", err)
			}
			out[tid] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("thread draft counts: %w", err)
		}
	}
	return out, nil
}

// DraftThread is the thread of the message a draft replies to; "" when
// it replies to nothing or the message is no longer stored.
func (s *Store) DraftThread(ctx context.Context, accountID, inReplyTo string) (string, error) {
	if inReplyTo == "" {
		return "", nil
	}
	var tid string
	err := s.db.QueryRowContext(ctx, `SELECT thread_id FROM messages WHERE id = ? AND account_id = ?`, inReplyTo, accountID).Scan(&tid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("draft thread: %w", err)
	}
	return tid, nil
}
