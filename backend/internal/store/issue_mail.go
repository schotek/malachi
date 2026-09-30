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
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Notification mail of an issue-tracker site in a mail account (migration
// 0015). issue_mail_links says which issue a stored mail message names:
// the message (its row goes with the message's), the issue-tracker
// account and the issue's key, and the issue's id once the issue is
// stored. messages.hidden is the display filter such a message may be
// under (Message.Hidden): core decides, from the account's
// JiraConfig.NotificationMail, the functions here apply it and recount
// the folders they touch in the same transaction. Nothing here changes a
// flag, a folder or anything a server would hear of.
//
// The functions that change what is hidden return the folders whose rows
// changed, by mail account id, each list sorted: what
// notify.messagesChanged names.

// IssueMailLink is a row of issue_mail_links with what the matcher reads
// of its message.
type IssueMailLink struct {
	MessageID      string
	MailAccountID  string
	FolderID       string
	IssueAccountID string
	IssueKey       string
	IssueID        string // "" = the issue was not stored when the mail was matched
	From           []api.Address
	Subject        string
	Date           time.Time
	InternalDate   time.Time
	Hidden         bool
}

// LinkIssueMail records that the message names the issue with the key of
// the issue-tracker account: issueID is the issue's id when the caller
// knows it, else the stored issue's with that key, else empty. A link of
// the message to another issue is replaced, and the message shown again
// if it was hidden because of that one. The stored issue is marked as
// named by a notification mail (Issue.ViaMail). ErrNotFound when no such
// message is stored.
func (s *Store) LinkIssueMail(ctx context.Context, messageID, issueAccountID, key, issueID string) error {
	if messageID == "" || issueAccountID == "" || key == "" {
		return fmt.Errorf("link issue mail: message id, account id and key are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("link issue mail: %w", err)
	}
	defer tx.Rollback()

	var folderID string
	var hidden int
	err = tx.QueryRowContext(ctx, `SELECT folder_id, hidden FROM messages WHERE id = ?`, messageID).Scan(&folderID, &hidden)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("link issue mail: %w", err)
	}
	var oldAccount, oldKey string
	err = tx.QueryRowContext(ctx, `SELECT issue_account_id, issue_key FROM issue_mail_links WHERE message_id = ?`, messageID).Scan(&oldAccount, &oldKey)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("link issue mail: %w", err)
	case hidden != 0 && (oldAccount != issueAccountID || oldKey != key):
		// Hidden because of the other issue.
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET hidden = 0 WHERE id = ?`, messageID); err != nil {
			return fmt.Errorf("link issue mail: %w", err)
		}
		if _, _, err := recountFolderTx(ctx, tx, folderID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if issueID == "" {
		err = tx.QueryRowContext(ctx, `SELECT issue_id FROM issues WHERE account_id = ? AND key = ?`, issueAccountID, key).Scan(&issueID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("link issue mail: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issue_mail_links (message_id, issue_account_id, issue_key, issue_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (message_id) DO UPDATE SET
			issue_account_id = excluded.issue_account_id, issue_key = excluded.issue_key, issue_id = excluded.issue_id`,
		messageID, issueAccountID, key, issueID); err != nil {
		return fmt.Errorf("link issue mail: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE issues SET via_mail = 1 WHERE account_id = ? AND key = ? AND via_mail = 0`,
		issueAccountID, key); err != nil {
		return fmt.Errorf("link issue mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("link issue mail: %w", err)
	}
	return nil
}

// IssueMailLinked reports whether a stored mail message names the issue
// with the key of the issue-tracker account.
func (s *Store) IssueMailLinked(ctx context.Context, issueAccountID, key string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM issue_mail_links WHERE issue_account_id = ? AND issue_key = ?)`,
		issueAccountID, key).Scan(&n); err != nil {
		return false, fmt.Errorf("issue mail linked: %w", err)
	}
	return n != 0, nil
}

// IssueMailLinks pages through the links of an issue-tracker account by
// message id: those after afterMessageID ("" = from the start), at most
// limit (<= 0: 200).
func (s *Store) IssueMailLinks(ctx context.Context, issueAccountID, afterMessageID string, limit int) ([]IssueMailLink, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.message_id, m.account_id, m.folder_id, l.issue_account_id, l.issue_key, l.issue_id,
			m.from_json, m.subject, m.date, m.internal_date, m.hidden
		FROM issue_mail_links l JOIN messages m ON m.id = l.message_id
		WHERE l.issue_account_id = ? AND l.message_id > ?
		ORDER BY l.message_id LIMIT ?`, issueAccountID, afterMessageID, limit)
	if err != nil {
		return nil, fmt.Errorf("list issue mail links: %w", err)
	}
	defer rows.Close()
	var out []IssueMailLink
	for rows.Next() {
		var l IssueMailLink
		var from, date, internal string
		var hidden int
		if err := rows.Scan(&l.MessageID, &l.MailAccountID, &l.FolderID, &l.IssueAccountID, &l.IssueKey, &l.IssueID,
			&from, &l.Subject, &date, &internal, &hidden); err != nil {
			return nil, fmt.Errorf("scan issue mail link: %w", err)
		}
		if err := json.Unmarshal([]byte(from), &l.From); err != nil {
			return nil, fmt.Errorf("decode addresses of %s: %w", l.MessageID, err)
		}
		l.Date, l.InternalDate, l.Hidden = parseStamp(date), parseStamp(internal), hidden != 0
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issue mail links: %w", err)
	}
	return out, nil
}

// UnlinkIssueMail forgets the links of the messages and shows those that
// were hidden again. Unknown ids and messages without a link are ignored.
func (s *Store) UnlinkIssueMail(ctx context.Context, messageIDs []string) (map[string][]string, error) {
	touched := map[string][]string{}
	ids := dedupeStrings(messageIDs)
	if len(ids) == 0 {
		return touched, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("unlink issue mail: %w", err)
	}
	defer tx.Rollback()
	for _, chunk := range chunkStrings(ids, 500) {
		in := inPlaceholders(len(chunk))
		if err := setHiddenTx(ctx, tx, false, `m.hidden = 1 AND m.id IN (SELECT message_id FROM issue_mail_links WHERE message_id IN (`+in+`))`,
			touched, toAny(chunk)...); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_mail_links WHERE message_id IN (`+in+`)`, toAny(chunk)...); err != nil {
			return nil, fmt.Errorf("unlink issue mail: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("unlink issue mail: %w", err)
	}
	return touched, nil
}

// SetIssueMailHidden applies an issue-tracker account's hiding to the
// mail linked to its issues, those with the keys or (keys nil) all of
// them. hide true: a linked message is hidden exactly when its issue is
// stored in the account, so one whose issue is stored is hidden (and its
// link learns the issue's id) and one whose issue is gone is shown
// again. hide false: every linked message is shown. keys empty but not
// nil changes nothing.
func (s *Store) SetIssueMailHidden(ctx context.Context, issueAccountID string, hide bool, keys []string) (map[string][]string, error) {
	touched := map[string][]string{}
	if issueAccountID == "" {
		return nil, fmt.Errorf("set issue mail hidden: account id is required")
	}
	scopes := [][]string{nil}
	if keys != nil {
		scopes = chunkStrings(dedupeStrings(keys), 500)
	}
	if len(scopes) == 0 {
		return touched, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("set issue mail hidden: %w", err)
	}
	defer tx.Rollback()
	const stored = `EXISTS (SELECT 1 FROM issues i WHERE i.account_id = l.issue_account_id AND i.key = l.issue_key)`
	for _, chunk := range scopes {
		scope, args := `l.issue_account_id = ?`, []any{issueAccountID}
		if chunk != nil {
			scope += ` AND l.issue_key IN (` + inPlaceholders(len(chunk)) + `)`
			args = append(args, toAny(chunk)...)
		}
		linked := func(cond string) string {
			return `m.id IN (SELECT l.message_id FROM issue_mail_links l WHERE ` + scope + cond + `)`
		}
		if !hide {
			if err := setHiddenTx(ctx, tx, false, `m.hidden = 1 AND `+linked(``), touched, args...); err != nil {
				return nil, err
			}
			continue
		}
		if err := setHiddenTx(ctx, tx, false, `m.hidden = 1 AND `+linked(` AND NOT `+stored), touched, args...); err != nil {
			return nil, err
		}
		if err := setHiddenTx(ctx, tx, true, `m.hidden = 0 AND `+linked(` AND `+stored), touched, args...); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_mail_links AS l SET issue_id =
				(SELECT i.issue_id FROM issues i WHERE i.account_id = l.issue_account_id AND i.key = l.issue_key)
			WHERE `+scope+` AND `+stored+` AND l.issue_id !=
				(SELECT i.issue_id FROM issues i WHERE i.account_id = l.issue_account_id AND i.key = l.issue_key)`, args...); err != nil {
			return nil, fmt.Errorf("set issue mail hidden: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("set issue mail hidden: %w", err)
	}
	return touched, nil
}

// SetMessageHidden hides a message or shows it again, and recounts its
// folder. changed is false when it was so already. ErrNotFound for an
// unknown id.
func (s *Store) SetMessageHidden(ctx context.Context, messageID string, hidden bool) (folderID string, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("set message hidden: %w", err)
	}
	defer tx.Rollback()
	var was int
	err = tx.QueryRowContext(ctx, `SELECT folder_id, hidden FROM messages WHERE id = ?`, messageID).Scan(&folderID, &was)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("set message hidden: %w", err)
	}
	if (was != 0) == hidden {
		return folderID, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET hidden = ? WHERE id = ?`, boolInt(hidden), messageID); err != nil {
		return "", false, fmt.Errorf("set message hidden: %w", err)
	}
	if _, _, err := recountFolderTx(ctx, tx, folderID); err != nil && !errors.Is(err, ErrNotFound) {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("set message hidden: %w", err)
	}
	return folderID, true, nil
}

// MessageHidden reports whether the message is under the display filter;
// false for an unknown id.
func (s *Store) MessageHidden(ctx context.Context, messageID string) (bool, error) {
	var hidden int
	err := s.db.QueryRowContext(ctx, `SELECT hidden FROM messages WHERE id = ?`, messageID).Scan(&hidden)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("message hidden: %w", err)
	}
	return hidden != 0, nil
}

// MailFromSenders pages, by message id, through the messages of the
// accounts that arrived since the time (the server's date of arrival,
// else the Date header) and have a sender among senders: "addr@host" is
// an address, "@host" any address of the host, compared without regard to
// ASCII case. It returns those after afterID ("" = from the start), at
// most limit (<= 0: 200). Hidden messages are listed too. It is a filter
// for the matcher, which decides: a message with several senders is
// listed when any of them is among senders. No accounts or no usable
// sender: nothing.
func (s *Store) MailFromSenders(ctx context.Context, accountIDs, senders []string, since time.Time, afterID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	accounts := dedupeStrings(accountIDs)
	var conds []string
	var senderArgs []any
	for _, sender := range dedupeStrings(senders) {
		sender = asciiLower(strings.TrimSpace(sender))
		switch at := strings.LastIndexByte(sender, '@'); {
		case at < 0 || at == len(sender)-1:
			continue
		case at == 0:
			conds = append(conds, `substr(`+senderAddress+`, -length(?)) = ?`)
			senderArgs = append(senderArgs, sender, sender)
		default:
			conds = append(conds, senderAddress+` = ?`)
			senderArgs = append(senderArgs, sender)
		}
	}
	if len(accounts) == 0 || len(conds) == 0 {
		return nil, nil
	}
	if len(accounts) > 500 {
		return nil, fmt.Errorf("mail from senders: too many accounts (%d)", len(accounts))
	}
	args := append(toAny(accounts), afterID, stamp(since))
	args = append(args, senderArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE account_id IN (`+inPlaceholders(len(accounts))+`) AND id > ?
			AND (CASE WHEN internal_date != '' THEN internal_date ELSE date END) >= ?
			AND EXISTS (SELECT 1 FROM json_each(messages.from_json) j WHERE `+strings.Join(conds, ` OR `)+`)
		ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("mail from senders: %w", err)
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
		return nil, fmt.Errorf("mail from senders: %w", err)
	}
	return out, nil
}

// senderAddress is the lower-cased address of one element (j) of a
// message's sender list; NULL for an element that is no object, which the
// encoder never writes.
const senderAddress = `lower(CASE WHEN j.type = 'object' THEN json_extract(j.value, '$.address') END)`

// asciiLower lowers the ASCII letters of s and nothing else, as SQLite's
// lower() does: a letter of another script that only looks like one never
// becomes it.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// setHiddenTx hides (or shows) the messages (alias m) that match where,
// recounts their folders and adds them to touched (account id → sorted
// folder ids).
func setHiddenTx(ctx context.Context, tx *sql.Tx, hidden bool, where string, touched map[string][]string, args ...any) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.id, m.account_id, m.folder_id FROM messages m WHERE `+where, args...)
	if err != nil {
		return fmt.Errorf("find linked mail: %w", err)
	}
	var ids []string
	folders := map[string]string{} // folder id → account id
	for rows.Next() {
		var id, acc, folder string
		if err := rows.Scan(&id, &acc, &folder); err != nil {
			rows.Close()
			return fmt.Errorf("scan linked mail: %w", err)
		}
		ids = append(ids, id)
		folders[folder] = acc
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("find linked mail: %w", err)
	}
	for _, chunk := range chunkStrings(ids, 500) {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET hidden = ? WHERE id IN (`+inPlaceholders(len(chunk))+`)`,
			append([]any{boolInt(hidden)}, toAny(chunk)...)...); err != nil {
			return fmt.Errorf("set hidden: %w", err)
		}
	}
	for folder, acc := range folders {
		if _, _, err := recountFolderTx(ctx, tx, folder); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		list := touched[acc]
		if i := sort.SearchStrings(list, folder); i == len(list) || list[i] != folder {
			list = append(list, "")
			copy(list[i+1:], list[i:])
			list[i] = folder
		}
		touched[acc] = list
	}
	return nil
}
