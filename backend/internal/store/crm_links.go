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

// CRM links (migration 0021, docs/eway.md D12): which messages are saved in
// a CRM account and what they are linked to there. A display cache, the
// CRM is the truth; the daemon checks a row again when it is old. A row is
// keyed by the message's Message-ID, so every copy of the message (Inbox,
// Archive) shows as saved; a message without one by its id
// (CrmMessageKey).

// CrmRelation is one item a saved message is linked to, as last read from
// the CRM. Name and Detail are cleaned display text.
type CrmRelation struct {
	Kind     api.CrmItemKind `json:"kind"`
	GUID     string          `json:"guid"`
	Name     string          `json:"name,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	Superior bool            `json:"superior,omitempty"`
}

// CrmLink is one saved message of a mail account in a CRM account.
type CrmLink struct {
	CrmAccountID  string
	MailAccountID string
	MsgKey        string
	ItemGUID      string
	SavedAt       time.Time
	CheckedAt     time.Time
	Private       bool
	Relations     []CrmRelation
	// Auto: the daemon saved the message by itself, following the
	// conversation (migration 0022, docs/eway.md D22).
	Auto bool
}

// CrmMessageKey is the key of m's CRM links: its Message-ID (as stored,
// without brackets), else "id:" and its id.
func CrmMessageKey(m Message) string {
	if m.RFCMessageID != "" {
		return m.RFCMessageID
	}
	return "id:" + m.ID
}

// UpsertCrmLink writes l, replacing the row of its key.
func (s *Store) UpsertCrmLink(ctx context.Context, l CrmLink) error {
	if l.CrmAccountID == "" || l.MailAccountID == "" || l.MsgKey == "" || l.ItemGUID == "" {
		return fmt.Errorf("crm link: missing key")
	}
	rel := l.Relations
	if rel == nil {
		rel = []CrmRelation{}
	}
	raw, err := json.Marshal(rel)
	if err != nil {
		return fmt.Errorf("crm link relations: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO crm_links
		(crm_account_id, mail_account_id, msg_key, item_guid, saved_at, checked_at, private, relations, auto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (crm_account_id, mail_account_id, msg_key) DO UPDATE SET
			item_guid = excluded.item_guid, saved_at = excluded.saved_at,
			checked_at = excluded.checked_at, private = excluded.private,
			relations = excluded.relations, auto = excluded.auto`,
		l.CrmAccountID, l.MailAccountID, l.MsgKey, l.ItemGUID, l.SavedAt.Unix(), l.CheckedAt.Unix(), boolInt(l.Private), string(raw), boolInt(l.Auto))
	if err != nil {
		return fmt.Errorf("upsert crm link: %w", err)
	}
	return nil
}

// CrmLinksFor returns the links of the mail account's messages with the
// given keys, by key (every CRM account's, in no particular order).
func (s *Store) CrmLinksFor(ctx context.Context, mailAccountID string, keys []string) (map[string][]CrmLink, error) {
	out := map[string][]CrmLink{}
	for _, chunk := range chunkStrings(dedupeStrings(keys), 500) {
		args := append([]any{mailAccountID}, toAny(chunk)...)
		rows, err := s.db.QueryContext(ctx, `SELECT `+crmLinkColumns+`
			FROM crm_links WHERE mail_account_id = ? AND msg_key IN (`+inPlaceholders(len(chunk))+`)
			ORDER BY crm_account_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("list crm links: %w", err)
		}
		for rows.Next() {
			l, err := scanCrmLink(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[l.MsgKey] = append(out[l.MsgKey], l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list crm links: %w", err)
		}
	}
	return out, nil
}

const crmLinkColumns = `crm_account_id, mail_account_id, msg_key, item_guid, saved_at, checked_at, private, relations, auto`

type rowScanner interface{ Scan(dest ...any) error }

func scanCrmLink(r rowScanner) (CrmLink, error) {
	var l CrmLink
	var saved, checked int64
	var priv, auto int
	var rel string
	if err := r.Scan(&l.CrmAccountID, &l.MailAccountID, &l.MsgKey, &l.ItemGUID, &saved, &checked, &priv, &rel, &auto); err != nil {
		return l, fmt.Errorf("scan crm link: %w", err)
	}
	l.SavedAt, l.CheckedAt, l.Private, l.Auto = time.Unix(saved, 0).UTC(), time.Unix(checked, 0).UTC(), priv == 1, auto == 1
	if json.Unmarshal([]byte(rel), &l.Relations) != nil {
		l.Relations = nil // a damaged cache is an empty one; the next check rewrites it
	}
	return l, nil
}

// crmMemberKey is the SQL of a member's link key (CrmMessageKey) over the
// messages alias m.
const crmMemberKey = `(CASE WHEN m.rfc_message_id != '' THEN m.rfc_message_id ELSE 'id:' || m.id END)`

// CrmThreadTemplate is what a followed conversation copies (D22): the link
// of its most recently saved member and the time of its first save.
type CrmThreadTemplate struct {
	Link       CrmLink
	FirstSaved time.Time
}

// CrmFollowedThreadTemplate returns the template of the mail account's
// thread for the CRM account: the most recently saved member's link and
// the first save's time; ok false when no member is saved there (the
// thread is not followed).
func (s *Store) CrmFollowedThreadTemplate(ctx context.Context, mailAccountID, threadID, crmAccountID string) (CrmThreadTemplate, bool, error) {
	var t CrmThreadTemplate
	if threadID == "" {
		return t, false, nil
	}
	const members = ` FROM messages m JOIN crm_links l ON l.crm_account_id = ? AND l.mail_account_id = m.account_id
		AND l.msg_key = ` + crmMemberKey + ` WHERE m.account_id = ? AND m.thread_id = ?`
	row := s.db.QueryRowContext(ctx, `SELECT `+prefixed("l.", crmLinkColumns)+members+` ORDER BY l.saved_at DESC LIMIT 1`,
		crmAccountID, mailAccountID, threadID)
	l, err := scanCrmLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	var first int64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(l.saved_at)`+members, crmAccountID, mailAccountID, threadID).Scan(&first); err != nil {
		return t, false, fmt.Errorf("crm thread first save: %w", err)
	}
	t.Link, t.FirstSaved = l, time.Unix(first, 0).UTC()
	return t, true, nil
}

// prefixed puts p before every column of a comma list.
func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// crmFollowRoles are the folder roles whose messages a followed
// conversation saves (D22): never drafts, outbox, junk, trash or all.
const crmFollowRoles = `('inbox', 'sent', 'archive', 'none')`

// CrmFollowable reports whether message id of the mail account may be
// saved by following its conversation: stored after since (the thread's
// first save) and not dated before it, in a folder of role inbox, sent,
// archive or none, not hidden, classified as not bulk mail (bulk 'none':
// a newsletter, list or automated message is never saved by itself, and
// one not classified yet waits for the sweep), not saved in the CRM
// account yet.
func (s *Store) CrmFollowable(ctx context.Context, mailAccountID, id, crmAccountID string, since time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? AND m.id = ?`+crmFollowCond, mailAccountID, id, stamp(since), stamp(since), crmAccountID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("crm followable: %w", err)
	}
	return n > 0, nil
}

// crmFollowCond is the shared condition of CrmFollowable and
// CrmUnsavedMembers; its parameters are since (twice) and the CRM account.
const crmFollowCond = ` AND m.created_at > ? AND (m.internal_date = '' OR m.internal_date >= ?)
	AND f.role IN ` + crmFollowRoles + ` AND m.hidden = 0 AND m.bulk = 'none'
	AND NOT EXISTS (SELECT 1 FROM crm_links l WHERE l.crm_account_id = ? AND l.mail_account_id = m.account_id
		AND l.msg_key = ` + crmMemberKey + `)`

// CrmUnsavedMembers returns the members of the thread that following it
// would save (CrmFollowable), oldest stored first, one per link key, at
// most limit.
func (s *Store) CrmUnsavedMembers(ctx context.Context, mailAccountID, threadID, crmAccountID string, since time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT MIN(m.id) FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ? AND m.thread_id = ?`+crmFollowCond+`
		GROUP BY `+crmMemberKey+` ORDER BY MIN(m.created_at), MIN(m.id) LIMIT ?`,
		mailAccountID, threadID, stamp(since), stamp(since), crmAccountID, limit)
	if err != nil {
		return nil, fmt.Errorf("crm unsaved members: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("crm unsaved members: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CrmFollowedThread is a conversation a CRM account follows.
type CrmFollowedThread struct {
	MailAccountID string
	ThreadID      string
}

// CrmFollowedThreads lists the conversations with a member saved in the
// CRM account since since, the most recently saved first, at most limit
// (the hourly catch-up, D22).
func (s *Store) CrmFollowedThreads(ctx context.Context, crmAccountID string, since time.Time, limit int) ([]CrmFollowedThread, error) {
	// Two indexed joins (by Message-ID, by id) rather than one on the key
	// expression, which no index serves.
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, thread_id FROM (
			SELECT m.account_id, m.thread_id, l.saved_at FROM crm_links l
				JOIN messages m ON m.account_id = l.mail_account_id AND m.rfc_message_id = l.msg_key
				WHERE l.crm_account_id = ? AND l.saved_at >= ? AND l.msg_key NOT LIKE 'id:%'
			UNION ALL
			SELECT m.account_id, m.thread_id, l.saved_at FROM crm_links l
				JOIN messages m ON m.id = substr(l.msg_key, 4) AND m.account_id = l.mail_account_id
				WHERE l.crm_account_id = ? AND l.saved_at >= ? AND l.msg_key LIKE 'id:%')
		WHERE thread_id != '' GROUP BY account_id, thread_id ORDER BY MAX(saved_at) DESC LIMIT ?`,
		crmAccountID, since.Unix(), crmAccountID, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("crm followed threads: %w", err)
	}
	defer rows.Close()
	var out []CrmFollowedThread
	for rows.Next() {
		var t CrmFollowedThread
		if err := rows.Scan(&t.MailAccountID, &t.ThreadID); err != nil {
			return nil, fmt.Errorf("crm followed threads: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteCrmLink removes one link; no row is not an error.
func (s *Store) DeleteCrmLink(ctx context.Context, crmAccountID, mailAccountID, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM crm_links
		WHERE crm_account_id = ? AND mail_account_id = ? AND msg_key = ?`, crmAccountID, mailAccountID, key); err != nil {
		return fmt.Errorf("delete crm link: %w", err)
	}
	return nil
}

// MarkCrmChecked records that the link was read from the CRM at at.
func (s *Store) MarkCrmChecked(ctx context.Context, crmAccountID, mailAccountID, key string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE crm_links SET checked_at = ?
		WHERE crm_account_id = ? AND mail_account_id = ? AND msg_key = ?`, at.Unix(), crmAccountID, mailAccountID, key); err != nil {
		return fmt.Errorf("mark crm link checked: %w", err)
	}
	return nil
}

// DeleteCrmLinksOfAccount removes every link of the account, as the CRM
// account or as the mail account; DeleteAccount does it in its
// transaction.
func (s *Store) DeleteCrmLinksOfAccount(ctx context.Context, accountID string) error {
	return deleteCrmLinksTx(ctx, s.db, accountID)
}

// sqlExecer is what deleteCrmLinksTx needs of a *sql.DB or *sql.Tx.
type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deleteCrmLinksTx(ctx context.Context, x sqlExecer, accountID string) error {
	if _, err := x.ExecContext(ctx, `DELETE FROM crm_links WHERE crm_account_id = ? OR mail_account_id = ?`, accountID, accountID); err != nil {
		return fmt.Errorf("delete crm links: %w", err)
	}
	return nil
}

// CrmFoldersOf returns the folders of the mail account that hold a message
// with one of the keys (for notify.messagesChanged).
func (s *Store) CrmFoldersOf(ctx context.Context, mailAccountID string, keys []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	var ids, msgids []string
	for _, k := range dedupeStrings(keys) {
		if rest, ok := strings.CutPrefix(k, "id:"); ok {
			ids = append(ids, rest)
		} else {
			msgids = append(msgids, k)
		}
	}
	for _, q := range []struct {
		col  string
		vals []string
	}{{"id", ids}, {"rfc_message_id", msgids}} {
		for _, chunk := range chunkStrings(q.vals, 500) {
			args := append([]any{mailAccountID}, toAny(chunk)...)
			rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT folder_id FROM messages
				WHERE account_id = ? AND `+q.col+` IN (`+inPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return nil, fmt.Errorf("crm folders: %w", err)
			}
			for rows.Next() {
				var f string
				if err := rows.Scan(&f); err != nil {
					rows.Close()
					return nil, fmt.Errorf("crm folders: %w", err)
				}
				if !seen[f] {
					seen[f] = true
					out = append(out, f)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("crm folders: %w", err)
			}
		}
	}
	return out, nil
}

// attachCrmSavedTo sets Message.CrmSaved of the listed rows that have a
// link in an enabled account of kind eway: one indexed lookup per page, skipped
// entirely while the table is empty (no CRM account ever saved
// anything), so a listing without CRM costs one EXISTS.
func attachCrmSavedTo(ctx context.Context, q querier, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM crm_links)`)
	if err != nil {
		return fmt.Errorf("crm links: %w", err)
	}
	var nonEmpty bool
	if rows.Next() {
		if err := rows.Scan(&nonEmpty); err != nil {
			rows.Close()
			return fmt.Errorf("crm links: %w", err)
		}
	}
	rows.Close()
	if !nonEmpty {
		return nil
	}
	byAccount := map[string]map[string][]*Message{}
	for _, m := range msgs {
		k := CrmMessageKey(*m)
		if byAccount[m.AccountID] == nil {
			byAccount[m.AccountID] = map[string][]*Message{}
		}
		byAccount[m.AccountID][k] = append(byAccount[m.AccountID][k], m)
	}
	for acc, keyed := range byAccount {
		keys := make([]string, 0, len(keyed))
		for k := range keyed {
			keys = append(keys, k)
		}
		for _, chunk := range chunkStrings(keys, 500) {
			args := append([]any{acc}, toAny(chunk)...)
			rows, err := q.QueryContext(ctx, `SELECT DISTINCT l.msg_key FROM crm_links l
				JOIN accounts a ON a.id = l.crm_account_id AND a.enabled = 1
					AND json_extract(a.config, '$.kind') = 'eway'
				WHERE l.mail_account_id = ? AND l.msg_key IN (`+inPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return fmt.Errorf("crm links: %w", err)
			}
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err != nil {
					rows.Close()
					return fmt.Errorf("crm links: %w", err)
				}
				for _, m := range keyed[k] {
					m.CrmSaved = true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("crm links: %w", err)
			}
		}
	}
	return nil
}

// CrmFollowedBy returns which of the CRM accounts follow the conversation
// of the mail account's message id (a member of its thread is saved
// there): one query, for the Stored hook of the mail syncers (D22).
func (s *Store) CrmFollowedBy(ctx context.Context, mailAccountID, id string, crmAccountIDs []string) ([]string, error) {
	if len(crmAccountIDs) == 0 {
		return nil, nil
	}
	args := append([]any{mailAccountID, id}, toAny(crmAccountIDs)...)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT l.crm_account_id FROM messages x
		JOIN messages m ON m.account_id = x.account_id AND m.thread_id = x.thread_id
		JOIN crm_links l ON l.mail_account_id = m.account_id AND l.msg_key = `+crmMemberKey+`
		WHERE x.account_id = ? AND x.id = ? AND x.thread_id != ''
			AND l.crm_account_id IN (`+inPlaceholders(len(crmAccountIDs))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("crm followed by: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("crm followed by: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
