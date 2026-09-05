// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// OutboxState is the delivery state of a queued message (the store's own
// enum; it mirrors api.OutboxState value for value).
type OutboxState string

const (
	OutboxQueued  OutboxState = "queued"  // waiting for the next attempt
	OutboxSending OutboxState = "sending" // an SMTP session is running
	OutboxSent    OutboxState = "sent"    // delivered; the Sent copy is pending
	OutboxFailed  OutboxState = "failed"  // permanent failure; RetryOutbox re-queues
)

// OutboxEntry is a row of the outbox table: the delivery state of the
// message with MessageID, which lives in the account's outbox folder.
type OutboxEntry struct {
	MessageID    string
	AccountID    string
	EnvelopeFrom string
	Recipients   []string // to+cc+bcc addresses, de-duplicated
	State        OutboxState
	Attempts     int
	// NextAttemptAt is when the next attempt may start; zero = due now.
	NextAttemptAt time.Time
	LastErrorCode api.ErrorCode
	LastError     string // technical text, at most maxOutboxErrorBytes
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// EnqueueInput is what EnqueueOutbox needs to turn a draft into a queued
// message.
type EnqueueInput struct {
	// The draft must exist for Message.AccountID with exactly this version.
	DraftID      string
	DraftVersion int

	// Message supplies the header columns of the new row: AccountID,
	// From/To/CC/BCC/ReplyTo, Subject, Date, InternalDate, RFCMessageID,
	// InReplyTo, References, Snippet, HasAttachments, HasHTML, Attachments
	// and Headers. ID, FolderID, UID, ModSeq, Flags, Size, BodyState and
	// ThreadID are set by the store.
	Message Message
	// Text is the plain-text body cached in messages.text_body.
	Text string

	EnvelopeFrom string
	Recipients   []string

	// Build streams the raw RFC 5322 message; more than Limit bytes (<= 0 →
	// api.MaxOutgoingMessageBytes) is ErrTooBig.
	Build func(w io.Writer) error
	Limit int64
}

// maxOutboxErrorBytes caps last_error (valid UTF-8 is preserved).
const maxOutboxErrorBytes = 200

// outboxFolderName is the name and path of the pseudo-folder.
const outboxFolderName = "Outbox"

// OutboxFolder returns the account's outbox pseudo-folder, creating it on
// first use: role outbox, mailbox "" (reserved — the IMAP LIST never yields
// an empty name), name and path "Outbox", selectable and subscribed,
// position -1 (the API layer orders it among the role folders itself).
func (s *Store) OutboxFolder(ctx context.Context, accountID string) (Folder, error) {
	if accountID == "" {
		return Folder{}, fmt.Errorf("outbox folder: empty account id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Folder{}, fmt.Errorf("outbox folder: %w", err)
	}
	defer tx.Rollback()
	f, err := outboxFolderTx(ctx, tx, accountID)
	if err != nil {
		return Folder{}, err
	}
	if err := tx.Commit(); err != nil {
		return Folder{}, fmt.Errorf("outbox folder: %w", err)
	}
	return f, nil
}

func outboxFolderTx(ctx context.Context, tx *sql.Tx, accountID string) (Folder, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE account_id = ? AND role = ? ORDER BY position, path, id LIMIT 1`,
		accountID, string(api.RoleOutbox))
	f, err := scanFolder(row)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Folder{}, fmt.Errorf("outbox folder: %w", err)
	}
	now := nowStamp()
	row = tx.QueryRowContext(ctx, `
		INSERT INTO folders (id, account_id, mailbox, delimiter, parent_id, name, path, role,
		                     subscribed, selectable, position, created_at, updated_at)
		VALUES (?, ?, '', '', '', ?, ?, ?, 1, 1, -1, ?, ?)
		RETURNING `+folderColumns,
		newID("f_"), accountID, outboxFolderName, outboxFolderName, string(api.RoleOutbox), now, now)
	if f, err = scanFolder(row); err != nil {
		return Folder{}, fmt.Errorf("create outbox folder: %w", err)
	}
	return f, nil
}

// EnqueueOutbox turns a draft into a queued message. The raw message is
// written first (WriteMessageRawFunc with in.Build and in.Limit → ErrTooBig);
// then, in one transaction, the draft is verified (missing → ErrNotFound,
// other version → ErrVersionConflict), the outbox folder ensured, the
// messages row inserted there (uid 0, flags ["seen"], body fetched with
// in.Text, size = bytes written), the outbox row inserted as queued, the
// draft deleted (its attachment rows cascade) and the folder recounted.
// The attachment files go after the commit. On any failure nothing is left
// behind, the raw file included. The stored message is returned.
func (s *Store) EnqueueOutbox(ctx context.Context, in EnqueueInput) (Message, error) {
	accountID := in.Message.AccountID
	if accountID == "" {
		return Message{}, fmt.Errorf("enqueue outbox: empty account id")
	}
	if in.Build == nil {
		return Message{}, fmt.Errorf("enqueue outbox: nil builder")
	}
	// A cheap pre-check spares building a message for a draft that is
	// already gone or stale; the transaction below is the authority.
	if err := s.checkDraftVersion(ctx, s.db, accountID, in.DraftID, in.DraftVersion); err != nil {
		return Message{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = api.MaxOutgoingMessageBytes
	}
	id := newID("m_")
	size, err := s.WriteMessageRawFunc(ctx, accountID, id, limit, in.Build)
	if err != nil {
		return Message{}, fmt.Errorf("enqueue outbox: %w", err)
	}
	m, attachments, err := s.enqueueOutboxTx(ctx, in, id, size)
	if err != nil {
		s.removeMessageFiles([]messageFile{{accountID: accountID, id: id}})
		return Message{}, err
	}
	for _, aid := range attachments {
		s.removeAttachmentFile(aid)
	}
	return m, nil
}

func (s *Store) checkDraftVersion(ctx context.Context, q execQuerier, accountID, draftID string, version int) error {
	var stored int
	err := q.QueryRowContext(ctx, `SELECT version FROM drafts WHERE id = ? AND account_id = ?`, draftID, accountID).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("check draft: %w", err)
	case stored != version:
		return ErrVersionConflict
	}
	return nil
}

func (s *Store) enqueueOutboxTx(ctx context.Context, in EnqueueInput, id string, size int64) (Message, []string, error) {
	accountID := in.Message.AccountID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, nil, fmt.Errorf("enqueue outbox: %w", err)
	}
	defer tx.Rollback()

	if err := s.checkDraftVersion(ctx, tx, accountID, in.DraftID, in.DraftVersion); err != nil {
		return Message{}, nil, err
	}
	folder, err := outboxFolderTx(ctx, tx, accountID)
	if err != nil {
		return Message{}, nil, err
	}

	m := in.Message
	m.ID, m.FolderID, m.UID, m.ModSeq = id, folder.ID, 0, 0
	m.Flags = []api.Flag{api.FlagSeen}
	// HasHTML is the caller's: a rich-text draft's outbox copy renders its
	// HTML part from the raw file like any received message.
	m.Size, m.BodyState, m.ThreadID = size, BodyFetched, ""
	enc, err := encodeMessage(&m)
	if err != nil {
		return Message{}, nil, err
	}
	now := nowStamp()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, account_id, folder_id, uid, modseq, flags, unread,
			from_json, to_json, cc_json, bcc_json, reply_to_json, subject, date, internal_date,
			rfc_message_id, in_reply_to, references_json, size, snippet, has_attachments,
			attachments_json, headers_json, has_html, text_body, body_state, thread_id, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, '', ?, ?)`,
		id, accountID, folder.ID, enc.flags, enc.unread,
		enc.from, enc.to, enc.cc, enc.bcc, enc.replyTo, m.Subject, stamp(m.Date), optStamp(m.InternalDate),
		m.RFCMessageID, m.InReplyTo, enc.references, size, m.Snippet, boolInt(m.HasAttachments),
		enc.attachments, enc.headers, in.Text, string(BodyFetched), now, now); err != nil {
		return Message{}, nil, fmt.Errorf("insert outbox message: %w", err)
	}
	recipients, err := encodeJSON(dedupeStrings(in.Recipients), "[]")
	if err != nil {
		return Message{}, nil, fmt.Errorf("encode recipients: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox (message_id, account_id, envelope_from, recipients_json, state, attempts,
		                    next_attempt_at, last_error_code, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, '', 0, '', ?, ?)`,
		id, accountID, in.EnvelopeFrom, recipients, string(OutboxQueued), now, now); err != nil {
		return Message{}, nil, fmt.Errorf("insert outbox entry: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id FROM attachments WHERE draft_id = ?`, in.DraftID)
	if err != nil {
		return Message{}, nil, fmt.Errorf("list draft attachments: %w", err)
	}
	var attachments []string
	for rows.Next() {
		var aid string
		if err := rows.Scan(&aid); err != nil {
			rows.Close()
			return Message{}, nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachments = append(attachments, aid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Message{}, nil, fmt.Errorf("list draft attachments: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE id = ? AND account_id = ? AND version = ?`,
		in.DraftID, accountID, in.DraftVersion)
	if err != nil {
		return Message{}, nil, fmt.Errorf("delete draft: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Message{}, nil, ErrVersionConflict
	}
	if _, _, err := recountFolderTx(ctx, tx, folder.ID); err != nil {
		return Message{}, nil, err
	}
	stored, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if err != nil {
		return Message{}, nil, fmt.Errorf("read outbox message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Message{}, nil, fmt.Errorf("enqueue outbox: %w", err)
	}
	return stored, attachments, nil
}

// GetOutbox returns the delivery state of one outbox message of the
// account; ErrNotFound otherwise.
func (s *Store) GetOutbox(ctx context.Context, accountID, messageID string) (OutboxEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE message_id = ? AND account_id = ?`, messageID, accountID)
	e, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEntry{}, ErrNotFound
	}
	if err != nil {
		return OutboxEntry{}, fmt.Errorf("get outbox entry: %w", err)
	}
	return e, nil
}

// OutboxEntries returns the entries of the account among ids, keyed by
// message id; ids without an entry are simply absent.
func (s *Store) OutboxEntries(ctx context.Context, accountID string, ids []string) (map[string]OutboxEntry, error) {
	out := make(map[string]OutboxEntry, len(ids))
	for _, chunk := range chunkStrings(ids, 500) {
		args := make([]any, 0, len(chunk)+1)
		args = append(args, accountID)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE account_id = ? AND message_id IN (`+inPlaceholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("list outbox entries: %w", err)
		}
		for rows.Next() {
			e, err := scanOutbox(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan outbox entry: %w", err)
			}
			out[e.MessageID] = e
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list outbox entries: %w", err)
		}
	}
	return out, nil
}

// ListOutbox returns the account's entries in state that are due at now
// (next_attempt_at empty or not after now), oldest created first.
func (s *Store) ListOutbox(ctx context.Context, accountID string, state OutboxState, now time.Time) ([]OutboxEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+outboxColumns+` FROM outbox
		WHERE account_id = ? AND state = ? AND (next_attempt_at = '' OR next_attempt_at <= ?)
		ORDER BY created_at, message_id`, accountID, string(state), stamp(now))
	if err != nil {
		return nil, fmt.Errorf("list outbox: %w", err)
	}
	defer rows.Close()
	var out []OutboxEntry
	for rows.Next() {
		e, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("scan outbox entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list outbox: %w", err)
	}
	return out, nil
}

// NextOutbox returns the oldest queued entry of the account that is due at
// now; false when there is none.
func (s *Store) NextOutbox(ctx context.Context, accountID string, now time.Time) (OutboxEntry, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+outboxColumns+` FROM outbox
		WHERE account_id = ? AND state = ? AND (next_attempt_at = '' OR next_attempt_at <= ?)
		ORDER BY created_at, message_id LIMIT 1`, accountID, string(OutboxQueued), stamp(now))
	e, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEntry{}, false, nil
	}
	if err != nil {
		return OutboxEntry{}, false, fmt.Errorf("next outbox entry: %w", err)
	}
	return e, true, nil
}

// NextOutboxDue returns the earliest scheduled attempt among the account's
// queued entries that carry a retry time (those due now, with none, are
// NextOutbox's business); false when there is no such entry. The time may
// already have passed: the worker then simply finds the entry due.
func (s *Store) NextOutboxDue(ctx context.Context, accountID string) (time.Time, bool, error) {
	var next sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MIN(next_attempt_at) FROM outbox WHERE account_id = ? AND state = ? AND next_attempt_at != ''`,
		accountID, string(OutboxQueued)).Scan(&next)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("next outbox due: %w", err)
	}
	if !next.Valid || next.String == "" {
		return time.Time{}, false, nil
	}
	return parseStamp(next.String), true, nil
}

// CountOutbox returns how many messages of the account still wait to be
// delivered (queued or sending).
func (s *Store) CountOutbox(ctx context.Context, accountID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE account_id = ? AND state IN (?, ?)`,
		accountID, string(OutboxQueued), string(OutboxSending)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count outbox: %w", err)
	}
	return n, nil
}

// MarkOutboxSending starts an attempt: queued → sending. ErrNotFound when
// the entry is not queued.
func (s *Store) MarkOutboxSending(ctx context.Context, id string) error {
	return s.transitionOutbox(ctx, "mark outbox sending",
		`UPDATE outbox SET state = ?, updated_at = ? WHERE message_id = ? AND state = ?`,
		string(OutboxSending), nowStamp(), id, string(OutboxQueued))
}

// MarkOutboxSent records a delivery: sending → sent, attempts+1, the error
// fields and the retry time cleared. ErrNotFound when the entry is not
// sending.
func (s *Store) MarkOutboxSent(ctx context.Context, id string) error {
	return s.transitionOutbox(ctx, "mark outbox sent",
		`UPDATE outbox SET state = ?, attempts = attempts + 1, next_attempt_at = '', last_error_code = 0, last_error = '', updated_at = ?
		 WHERE message_id = ? AND state = ?`,
		string(OutboxSent), nowStamp(), id, string(OutboxSending))
}

// MarkOutboxRetry records a transient failure: sending → queued,
// attempts+1, the error kept and the next attempt scheduled for retryAt
// (zero = due now). ErrNotFound when the entry is not sending.
func (s *Store) MarkOutboxRetry(ctx context.Context, id string, code api.ErrorCode, errText string, retryAt time.Time) error {
	return s.transitionOutbox(ctx, "mark outbox retry",
		`UPDATE outbox SET state = ?, attempts = attempts + 1, next_attempt_at = ?, last_error_code = ?, last_error = ?, updated_at = ?
		 WHERE message_id = ? AND state = ?`,
		string(OutboxQueued), optStamp(retryAt), int(code), capOutboxError(errText), nowStamp(), id, string(OutboxSending))
}

// MarkOutboxFailed records a permanent failure: sending → failed,
// attempts+1, the error kept, no retry time. ErrNotFound when the entry is
// not sending.
func (s *Store) MarkOutboxFailed(ctx context.Context, id string, code api.ErrorCode, errText string) error {
	return s.transitionOutbox(ctx, "mark outbox failed",
		`UPDATE outbox SET state = ?, attempts = attempts + 1, next_attempt_at = '', last_error_code = ?, last_error = ?, updated_at = ?
		 WHERE message_id = ? AND state = ?`,
		string(OutboxFailed), int(code), capOutboxError(errText), nowStamp(), id, string(OutboxSending))
}

// MarkOutboxAppendFailed records that the Sent copy could not be stored:
// the entry stays sent, attempts+1, the text kept (the code is left as it
// is) and the next attempt scheduled for retryAt (zero = due now).
// ErrNotFound when the entry is not sent.
func (s *Store) MarkOutboxAppendFailed(ctx context.Context, id string, errText string, retryAt time.Time) error {
	return s.transitionOutbox(ctx, "mark outbox append failed",
		`UPDATE outbox SET attempts = attempts + 1, next_attempt_at = ?, last_error = ?, updated_at = ?
		 WHERE message_id = ? AND state = ?`,
		optStamp(retryAt), capOutboxError(errText), nowStamp(), id, string(OutboxSent))
}

// RetryOutbox re-queues a failed (or defers a queued) entry of the account
// for an immediate attempt: the retry time is cleared, the last error is
// kept until the next attempt overwrites it. ErrOutboxBusy while it is
// sending, ErrOutbox once it is sent, ErrNotFound when unknown.
func (s *Store) RetryOutbox(ctx context.Context, accountID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("retry outbox: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM outbox WHERE message_id = ? AND account_id = ?`, id, accountID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("retry outbox: %w", err)
	}
	switch OutboxState(state) {
	case OutboxSending:
		return ErrOutboxBusy
	case OutboxSent:
		return ErrOutbox
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox SET state = ?, next_attempt_at = '', updated_at = ? WHERE message_id = ?`,
		string(OutboxQueued), nowStamp(), id); err != nil {
		return fmt.Errorf("retry outbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("retry outbox: %w", err)
	}
	return nil
}

// DeferOutbox postpones every queued entry of the account until `until`
// and records why (the account is offline, its credentials are missing…).
// It returns how many entries were deferred.
func (s *Store) DeferOutbox(ctx context.Context, accountID string, until time.Time, code api.ErrorCode, errText string) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET next_attempt_at = ?, last_error_code = ?, last_error = ?, updated_at = ? WHERE account_id = ? AND state = ?`,
		optStamp(until), int(code), capOutboxError(errText), nowStamp(), accountID, string(OutboxQueued))
	if err != nil {
		return 0, fmt.Errorf("defer outbox: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetOutbox is the worker's start-up sweep: an attempt interrupted by a
// crash (sending) counts as one more attempt and goes back to queued, and
// every queued entry becomes due now.
func (s *Store) ResetOutbox(ctx context.Context, accountID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reset outbox: %w", err)
	}
	defer tx.Rollback()
	now := nowStamp()
	if _, err := tx.ExecContext(ctx, `UPDATE outbox SET state = ?, attempts = attempts + 1, updated_at = ? WHERE account_id = ? AND state = ?`,
		string(OutboxQueued), now, accountID, string(OutboxSending)); err != nil {
		return fmt.Errorf("reset outbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox SET next_attempt_at = '', updated_at = ? WHERE account_id = ? AND state = ? AND next_attempt_at != ''`,
		now, accountID, string(OutboxQueued)); err != nil {
		return fmt.Errorf("reset outbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reset outbox: %w", err)
	}
	return nil
}

// DeleteOutboxMessage removes an outbox message of the account: the
// messages row (its outbox entry cascades) now, the raw file after the
// commit, the outbox folder recounted. No operation is queued — the server
// never had the message. The state is not checked: this is the worker's
// clean-up after the Sent copy is stored (the UI's deletions go through
// DeleteMessages/TrashMessages, which refuse a sending entry). ErrNotFound
// when id is not an outbox message of the account.
func (s *Store) DeleteOutboxMessage(ctx context.Context, accountID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete outbox message: %w", err)
	}
	defer tx.Rollback()

	var folderID string
	err = tx.QueryRowContext(ctx, `
		SELECT m.folder_id FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.id = ? AND m.account_id = ? AND f.role = ?`, id, accountID, string(api.RoleOutbox)).Scan(&folderID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("delete outbox message: %w", err)
	}
	if err := deleteOutboxRowTx(ctx, tx, messageLoc{id: id, folderID: folderID}); err != nil {
		return err
	}
	if _, _, err := recountFolderTx(ctx, tx, folderID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete outbox message: %w", err)
	}
	s.removeMessageFiles([]messageFile{{accountID: accountID, id: id}})
	return nil
}

// transitionOutbox runs one guarded state change; no row matched means the
// entry is unknown or not in the expected source state → ErrNotFound.
func (s *Store) transitionOutbox(ctx context.Context, what, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// capOutboxError makes errText fit last_error: invalid UTF-8 is dropped and
// the text is cut at a rune boundary to at most maxOutboxErrorBytes.
func capOutboxError(errText string) string {
	errText = strings.ToValidUTF8(errText, "")
	if len(errText) <= maxOutboxErrorBytes {
		return errText
	}
	cut := maxOutboxErrorBytes
	for cut > 0 && !utf8.RuneStart(errText[cut]) {
		cut--
	}
	return errText[:cut]
}

// dedupeStrings keeps the first occurrence of each value, in order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

const outboxColumns = `message_id, account_id, envelope_from, recipients_json, state, attempts,
	next_attempt_at, last_error_code, last_error, created_at, updated_at`

func scanOutbox(row scanner) (OutboxEntry, error) {
	var e OutboxEntry
	var recipients, state, next, created, updated string
	var code int
	if err := row.Scan(&e.MessageID, &e.AccountID, &e.EnvelopeFrom, &recipients, &state, &e.Attempts,
		&next, &code, &e.LastError, &created, &updated); err != nil {
		return OutboxEntry{}, err
	}
	if err := json.Unmarshal([]byte(recipients), &e.Recipients); err != nil {
		return OutboxEntry{}, fmt.Errorf("decode recipients of %s: %w", e.MessageID, err)
	}
	if e.Recipients == nil {
		e.Recipients = []string{}
	}
	e.State = OutboxState(state)
	e.LastErrorCode = api.ErrorCode(code)
	e.NextAttemptAt = parseStamp(next)
	e.CreatedAt, e.UpdatedAt = parseStamp(created), parseStamp(updated)
	return e, nil
}
