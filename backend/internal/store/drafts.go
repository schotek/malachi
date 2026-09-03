package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Draft is a row of the drafts table plus its bound attachments.
type Draft struct {
	ID         string
	AccountID  string
	Version    int
	Subject    string
	To, CC     []api.Address
	BCC        []api.Address
	TextBody   string
	HTMLBody   string // sanitiser output only
	InReplyTo  string
	Forwarding string
	CreatedAt  time.Time
	UpdatedAt  time.Time

	Attachments []Attachment // in position order
}

// SaveDraft creates d (empty ID → new id, version 1) or updates it (the
// stored version must equal d.Version; the result is d.Version+1). In the
// same transaction the listed attachments are bound to the draft in that
// order and every other attachment bound to it is released. Each listed
// attachment must belong to the same account and be unbound or bound to
// this draft (ErrNotFound / ErrAttachmentBound otherwise). d.ID, d.Version,
// d.UpdatedAt and d.Attachments are filled in on success.
func (s *Store) SaveDraft(ctx context.Context, d *Draft, attachmentIDs []string) error {
	to, cc, bcc, err := encodeRecipients(d)
	if err != nil {
		return err
	}
	now := nowStamp()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// d is only updated after a successful commit so a failed save leaves
	// the caller's view (in particular an empty ID) intact.
	draftID, version := d.ID, d.Version
	if draftID == "" {
		draftID = newID("d_")
		version = 1
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drafts (id, account_id, version, subject, to_json, cc_json, bcc_json,
			                    text_body, html_body, in_reply_to, forwarding, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			draftID, d.AccountID, d.Subject, to, cc, bcc, d.TextBody, d.HTMLBody,
			d.InReplyTo, d.Forwarding, now, now); err != nil {
			return fmt.Errorf("insert draft: %w", err)
		}
	} else {
		res, err := tx.ExecContext(ctx, `
			UPDATE drafts SET version = version + 1, subject = ?, to_json = ?, cc_json = ?, bcc_json = ?,
			       text_body = ?, html_body = ?, in_reply_to = ?, forwarding = ?, updated_at = ?
			WHERE id = ? AND account_id = ? AND version = ?`,
			d.Subject, to, cc, bcc, d.TextBody, d.HTMLBody, d.InReplyTo, d.Forwarding, now,
			draftID, d.AccountID, version)
		if err != nil {
			return fmt.Errorf("update draft: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var stored int
			err := tx.QueryRowContext(ctx, `SELECT version FROM drafts WHERE id = ? AND account_id = ?`,
				draftID, d.AccountID).Scan(&stored)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("check draft: %w", err)
			}
			return ErrVersionConflict
		}
		version++
	}

	// Bind listed attachments in order.
	for i, id := range attachmentIDs {
		res, err := tx.ExecContext(ctx, `
			UPDATE attachments SET draft_id = ?, position = ?
			WHERE id = ? AND account_id = ? AND (draft_id IS NULL OR draft_id = ?)`,
			draftID, i, id, d.AccountID, draftID)
		if err != nil {
			return fmt.Errorf("bind attachment: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var other sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT draft_id FROM attachments WHERE id = ? AND account_id = ?`,
				id, d.AccountID).Scan(&other)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("check attachment: %w", err)
			}
			return ErrAttachmentBound
		}
	}
	// Release everything else previously bound to this draft.
	query := `UPDATE attachments SET draft_id = NULL, position = 0 WHERE draft_id = ?`
	args := []any{draftID}
	if len(attachmentIDs) > 0 {
		query += ` AND id NOT IN (?` + strings.Repeat(",?", len(attachmentIDs)-1) + `)`
		for _, id := range attachmentIDs {
			args = append(args, id)
		}
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("release attachments: %w", err)
	}

	atts, err := attachmentsForDraft(ctx, tx, draftID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	d.ID, d.Version = draftID, version
	d.UpdatedAt = parseStamp(now)
	if d.CreatedAt.IsZero() {
		d.CreatedAt = d.UpdatedAt
	}
	d.Attachments = atts
	return nil
}

// GetDraft loads one draft with its attachments.
func (s *Store) GetDraft(ctx context.Context, accountID, id string) (Draft, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ? AND account_id = ?`, id, accountID)
	d, err := scanDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	if d.Attachments, err = attachmentsForDraft(ctx, s.db, d.ID); err != nil {
		return Draft{}, err
	}
	return d, nil
}

// ListDrafts pages through an account's drafts, newest first. cursor is
// opaque (empty for the first page); next is empty on the last page.
func (s *Store) ListDrafts(ctx context.Context, accountID, cursor string, limit int) (items []Draft, next string, total int, err error) {
	if limit <= 0 {
		limit = 50
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drafts WHERE account_id = ?`, accountID).Scan(&total); err != nil {
		return nil, "", 0, fmt.Errorf("count drafts: %w", err)
	}

	query := `SELECT ` + draftColumns + ` FROM drafts WHERE account_id = ?`
	args := []any{accountID}
	if cursor != "" {
		stamp, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", 0, err
		}
		query += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))`
		args = append(args, stamp, stamp, id)
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()
	var stamps []string
	for rows.Next() {
		d, stamp, err := scanDraftStamp(rows)
		if err != nil {
			return nil, "", 0, err
		}
		items = append(items, d)
		stamps = append(stamps, stamp)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, fmt.Errorf("list drafts: %w", err)
	}
	if len(items) > limit {
		items = items[:limit]
		next = encodeCursor(stamps[limit-1], items[limit-1].ID)
	}
	for i := range items {
		if items[i].Attachments, err = attachmentsForDraft(ctx, s.db, items[i].ID); err != nil {
			return nil, "", 0, err
		}
	}
	return items, next, total, nil
}

// DeleteDraft removes the draft, its attachment rows (cascade) and their
// files. Deleting an unknown draft is not an error.
func (s *Store) DeleteDraft(ctx context.Context, accountID, id string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM attachments WHERE draft_id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return fmt.Errorf("list draft attachments: %w", err)
	}
	var files []string
	for rows.Next() {
		var aid string
		if err := rows.Scan(&aid); err != nil {
			rows.Close()
			return err
		}
		files = append(files, aid)
	}
	rows.Close()

	if _, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE id = ? AND account_id = ?`, id, accountID); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	for _, aid := range files {
		s.removeAttachmentFile(aid)
	}
	return nil
}

const draftColumns = `id, account_id, version, subject, to_json, cc_json, bcc_json,
	text_body, html_body, in_reply_to, forwarding, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanDraft(row scanner) (Draft, error) {
	d, _, err := scanDraftStamp(row)
	return d, err
}

func scanDraftStamp(row scanner) (Draft, string, error) {
	var d Draft
	var to, cc, bcc, created, updated string
	if err := row.Scan(&d.ID, &d.AccountID, &d.Version, &d.Subject, &to, &cc, &bcc,
		&d.TextBody, &d.HTMLBody, &d.InReplyTo, &d.Forwarding, &created, &updated); err != nil {
		return Draft{}, "", err
	}
	for _, p := range []struct {
		raw string
		dst *[]api.Address
	}{{to, &d.To}, {cc, &d.CC}, {bcc, &d.BCC}} {
		if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
			return Draft{}, "", fmt.Errorf("decode recipients of %s: %w", d.ID, err)
		}
	}
	d.CreatedAt, d.UpdatedAt = parseStamp(created), parseStamp(updated)
	return d, updated, nil
}

func encodeRecipients(d *Draft) (to, cc, bcc string, err error) {
	enc := func(a []api.Address) (string, error) {
		if a == nil {
			a = []api.Address{}
		}
		b, err := json.Marshal(a)
		return string(b), err
	}
	if to, err = enc(d.To); err != nil {
		return
	}
	if cc, err = enc(d.CC); err != nil {
		return
	}
	bcc, err = enc(d.BCC)
	return
}

func encodeCursor(stamp, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(stamp + "\x00" + id))
}

func decodeCursor(c string) (stamp, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", ErrBadCursor
	}
	stamp, id, ok := strings.Cut(string(raw), "\x00")
	if !ok || stamp == "" || id == "" {
		return "", "", ErrBadCursor
	}
	return stamp, id, nil
}

// removeAttachmentFile unlinks an attachment's data file; a missing file is
// not an error (the row is authoritative).
func (s *Store) removeAttachmentFile(id string) {
	if err := os.Remove(filepath.Join(s.AttachmentDir(), id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("remove attachment file", "id", id, "err", err)
	}
}
