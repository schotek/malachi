// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Attachment is a row of the attachments table. Data lives in
// AttachmentDir()/<ID>.
type Attachment struct {
	ID          string
	AccountID   string
	DraftID     string // empty while unbound
	Position    int
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	Inline      bool
	ContentID   string
	CreatedAt   time.Time
}

// AttachmentPath is the data file of attachment id.
func (s *Store) AttachmentPath(id string) string {
	return filepath.Join(s.AttachmentDir(), id)
}

// ImportAttachment copies r into the store and records a new, unbound
// attachment. a.AccountID, Filename, ContentType, Inline and ContentID are
// taken from a; ID, Size, SHA256 and CreatedAt are filled in. More than
// limit bytes → ErrTooBig and nothing is kept; an empty reader is an error.
func (s *Store) ImportAttachment(ctx context.Context, a *Attachment, r io.Reader, limit int64) error {
	a.ID = newID("att_")
	final := s.AttachmentPath(a.ID)
	tmp := final + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create attachment file: %w", err)
	}
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}

	h := sha256.New()
	// Read one byte past the limit so growth after a stat check cannot
	// slip through.
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if err != nil {
		cleanup()
		return fmt.Errorf("copy attachment: %w", err)
	}
	if n > limit {
		cleanup()
		return ErrTooBig
	}
	if n == 0 {
		cleanup()
		return fmt.Errorf("import attachment: empty file")
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close attachment file: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalise attachment file: %w", err)
	}

	a.Size = n
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	now := nowStamp()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO attachments (id, account_id, draft_id, position, filename, content_type, size, sha256, inline, content_id, created_at)
		VALUES (?, ?, NULL, 0, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.AccountID, a.Filename, a.ContentType, a.Size, a.SHA256, boolInt(a.Inline), a.ContentID, now); err != nil {
		os.Remove(final)
		return fmt.Errorf("insert attachment: %w", err)
	}
	a.DraftID = ""
	a.CreatedAt = parseStamp(now)
	return nil
}

// GetAttachments returns the attachments with the given ids, in that order.
// Any id that does not exist for the account yields ErrNotFound.
func (s *Store) GetAttachments(ctx context.Context, accountID string, ids []string) ([]Attachment, error) {
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		row := s.db.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE id = ? AND account_id = ?`, id, accountID)
		a, err := scanAttachment(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// OpenAttachment opens the data file of an attachment for reading.
func (s *Store) OpenAttachment(ctx context.Context, accountID, id string) (*os.File, Attachment, error) {
	atts, err := s.GetAttachments(ctx, accountID, []string{id})
	if err != nil {
		return nil, Attachment{}, err
	}
	f, err := os.Open(s.AttachmentPath(id))
	if err != nil {
		return nil, Attachment{}, fmt.Errorf("open attachment file: %w", err)
	}
	return f, atts[0], nil
}

// RemoveAttachment deletes the row and the file; unknown ids are ignored.
func (s *Store) RemoveAttachment(ctx context.Context, accountID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.removeAttachmentFile(id)
	}
	return nil
}

// SweepAttachments removes unbound attachments older than olderThan (rows
// and files), data files that have no row, and stale *.tmp files. It
// returns the number of files removed.
func (s *Store) SweepAttachments(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	removed := 0

	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM attachments WHERE draft_id IS NULL AND created_at < ?`, cutoff.Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("list orphan attachments: %w", err)
	}
	var orphans []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		orphans = append(orphans, id)
	}
	rows.Close()
	for _, id := range orphans {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
			return removed, fmt.Errorf("delete orphan attachment: %w", err)
		}
		s.removeAttachmentFile(id)
		removed++
	}

	entries, err := os.ReadDir(s.AttachmentDir())
	if err != nil {
		return removed, fmt.Errorf("read attachment directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".tmp"):
			if time.Since(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(s.AttachmentDir(), name))
				removed++
			}
		default:
			if info.ModTime().After(cutoff) {
				continue
			}
			var n int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE id = ?`, name).Scan(&n); err != nil {
				continue
			}
			if n == 0 {
				os.Remove(filepath.Join(s.AttachmentDir(), name))
				removed++
			}
		}
	}
	return removed, nil
}

const attachmentColumns = `id, account_id, COALESCE(draft_id, ''), position, filename, content_type, size, sha256, inline, content_id, created_at`

func scanAttachment(row scanner) (Attachment, error) {
	var a Attachment
	var inline int
	var created string
	if err := row.Scan(&a.ID, &a.AccountID, &a.DraftID, &a.Position, &a.Filename, &a.ContentType,
		&a.Size, &a.SHA256, &inline, &a.ContentID, &created); err != nil {
		return Attachment{}, err
	}
	a.Inline = inline != 0
	a.CreatedAt = parseStamp(created)
	return a, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func attachmentsForDraft(ctx context.Context, q querier, draftID string) ([]Attachment, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE draft_id = ? ORDER BY position, id`, draftID)
	if err != nil {
		return nil, fmt.Errorf("list draft attachments: %w", err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
