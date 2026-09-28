// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The background passes over the raw files (raw.go): the conversion to the
// store's codec (ConvertRawBatch) and the sweep that repairs what crashes,
// deletions and older versions leave behind (SweepMessageFiles). The core
// runs both from its maintenance loop, paced; neither waits for a message
// a writer holds, and neither ever creates a directory.

// ConvertResult counts what one ConvertRawBatch did.
type ConvertResult struct {
	Visited   int // accounting rows dealt with
	Converted int // files now in the target codec
	Busy      int // skipped: a writer held the message (the sweep counts it as misplaced)
	Corrupt   int // skipped: the source is damaged and stays as it is
	Missing   int // rows whose file was gone; the row went too
	Failed    int // skipped after another error (logged)
}

// convertDefaultBatch is ConvertRawBatch's limit when none is given.
const convertDefaultBatch = 64

// ConvertRawBatch converts up to limit raw files (<= 0: 64) that are not
// in codec to, in message id order after afterID, and returns the last id
// it dealt with, "" when nothing was left after afterID (the pass is
// done). Outbox messages are never converted.
//
// It works in two phases so that a crash anywhere leaves one whole copy
// of every message: first each new file is written beside the old one,
// checked, flushed and renamed into place, then the touched directories
// are flushed once, and only then does each old file go, unless a writer
// replaced either file meanwhile (os.SameFile). A busy message is skipped
// and a damaged one kept, both counted. The batch stops at a full disk
// (ErrNoSpace), when ctx ends, and when the store's codec is no longer to
// (ErrConflict); last is then the last id dealt with, and what was
// converted so far is finished.
func (s *Store) ConvertRawBatch(ctx context.Context, afterID string, to RawCodec, limit int) (string, ConvertResult, error) {
	var res ConvertResult
	if !to.valid() {
		return afterID, res, fmt.Errorf("convert message files: codec %s", to)
	}
	if limit <= 0 {
		limit = convertDefaultBatch
	}
	if now := s.RawCodec(); now != to {
		return afterID, res, fmt.Errorf("%w: the store writes %s", ErrConflict, now)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT mf.message_id, m.account_id FROM message_files mf
		JOIN messages m ON m.id = mf.message_id JOIN folders f ON f.id = m.folder_id
		WHERE mf.message_id > ? AND mf.codec != ? AND f.role != ?
		ORDER BY mf.message_id LIMIT ?`, afterID, to.String(), string(api.RoleOutbox), limit)
	if err != nil {
		return afterID, res, fmt.Errorf("convert message files: %w", err)
	}
	var todo []messageFile
	for rows.Next() {
		var mf messageFile
		if err := rows.Scan(&mf.id, &mf.accountID); err != nil {
			rows.Close()
			return afterID, res, fmt.Errorf("convert message files: %w", err)
		}
		todo = append(todo, mf)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return afterID, res, fmt.Errorf("convert message files: %w", err)
	}
	if len(todo) == 0 {
		return "", res, nil
	}

	last := afterID
	var pending []convertPending
	dirs := map[string]bool{}
	var stop error
	for _, mf := range todo {
		if err := ctx.Err(); err != nil {
			stop = err
			break
		}
		if now := s.RawCodec(); now != to {
			stop = fmt.Errorf("%w: the store writes %s", ErrConflict, now)
			break
		}
		p, err := s.convertFirst(ctx, mf, to, &res)
		if errors.Is(err, ErrNoSpace) {
			stop = err
			break
		}
		if err != nil {
			s.log.Warn("convert message file", "id", mf.id, "err", err)
			res.Failed++
		}
		res.Visited++
		last = mf.id
		if p != nil {
			pending = append(pending, *p)
			dirs[s.accountDir(mf.accountID)] = true
		}
	}
	if len(pending) == 0 {
		return last, res, stop
	}
	for dir := range dirs {
		if err := syncDirectory(dir); err != nil {
			// The new files might not outlive a crash yet: the old ones
			// stay beside them, and the sweep settles each pair.
			if stop == nil {
				stop = noSpace(err)
			}
			return last, res, stop
		}
	}
	if s.betweenConvertPhases != nil {
		s.betweenConvertPhases()
	}
	for _, p := range pending {
		s.convertSecond(ctx, p, &res)
	}
	return last, res, stop
}

// convertPending is a message between the two phases of ConvertRawBatch:
// the files it left, to be recognised again.
type convertPending struct {
	messageFile
	from, to RawCodec
	src, dst fs.FileInfo
	info     RawInfo
}

// convertFirst is the first phase for one message. It returns nil when
// there is nothing for the second (busy, damaged, already converted, or
// without a file).
func (s *Store) convertFirst(ctx context.Context, mf messageFile, to RawCodec, res *ConvertResult) (*convertPending, error) {
	h, ok := s.tryLockRaw(mf.accountID, mf.id)
	if !ok {
		res.Busy++
		return nil, nil
	}
	defer h.unlock()
	files, err := statRaw(h.dir, mf.id)
	if err != nil {
		return nil, err
	}
	if files.both() {
		if files, err = s.resolveLocked(ctx, h, files); err != nil {
			return nil, err
		}
	}
	from := to.other()
	switch {
	case !files.any():
		// Removed outside the daemon: the row has nothing to account for.
		s.dropRawRow(ctx, mf.id)
		res.Missing++
		return nil, nil
	case files.of(from) == nil:
		// Already in the target codec; the row was behind.
		info, err := files.info(h.dir, mf.id)
		if err != nil {
			return nil, err
		}
		s.recordRaw(ctx, mf.id, info)
		res.Converted++
		return nil, nil
	}
	src := files.of(from)
	info, err := s.convertLocked(ctx, h, from, to, true)
	if errors.Is(err, ErrRawCorrupt) {
		s.log.Warn("damaged message file left as it is", "id", mf.id, "err", err)
		res.Corrupt++
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dst, err := os.Lstat(filepath.Join(h.dir, rawName(mf.id, to)))
	if err != nil {
		return nil, fmt.Errorf("stat message file: %w", err)
	}
	// The second phase tells by os.SameFile whether a writer replaced
	// either file meanwhile. Windows may read a file's identity lazily, by
	// path, at the first SameFile, which would then take whatever file has
	// the name by then for the one seen here: read both identities now,
	// while the names are still these files. Without them the second phase
	// could not tell, and the pair is left to the sweep.
	if !os.SameFile(src, src) || !os.SameFile(dst, dst) {
		s.log.Warn("cannot identify the converted message files; the sweep settles them", "id", mf.id)
		return nil, nil
	}
	return &convertPending{messageFile: mf, from: from, to: to, src: src, dst: dst, info: info}, nil
}

// convertSecond is the second phase for one message: the old file goes if
// both files are still the ones the first phase left.
func (s *Store) convertSecond(ctx context.Context, p convertPending, res *ConvertResult) {
	h, ok := s.tryLockRaw(p.accountID, p.id)
	if !ok {
		// Its writer settles the pair; if it does not, the sweep does.
		res.Busy++
		return
	}
	defer h.unlock()
	srcPath := filepath.Join(h.dir, rawName(p.id, p.from))
	src, err := os.Lstat(srcPath)
	if err != nil || !os.SameFile(src, p.src) {
		return
	}
	dst, err := os.Lstat(filepath.Join(h.dir, rawName(p.id, p.to)))
	if err != nil || !os.SameFile(dst, p.dst) {
		return
	}
	h.l.names.Lock()
	err = fsretry.Remove(srcPath)
	h.l.names.Unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("remove converted message file", "id", p.id, "err", err)
		return
	}
	s.recordRaw(ctx, p.id, p.info)
	res.Converted++
}

// of is the file of codec c.
func (f rawFiles) of(c RawCodec) fs.FileInfo {
	if c == RawZstd {
		return f.zst
	}
	return f.plain
}

// dropRawRow removes a message's accounting row.
func (s *Store) dropRawRow(ctx context.Context, id string) {
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM message_files WHERE message_id = ?`, id); err != nil {
		s.log.Warn("drop message file row", "id", id, "err", err)
	}
}

// SweepResult counts what one SweepMessageFiles did.
type SweepResult struct {
	Temps      int // stale temporary files removed
	Staged     int // stale staged files removed
	Orphans    int // files of messages without a row removed
	Dirs       int // empty directories of unknown accounts removed
	Resolved   int // messages that had both a plain and a .zst file
	Backfilled int // accounting rows added for files that had none
	Fixed      int // accounting rows corrected
	Dropped    int // accounting rows without a file removed
	Misplaced  int // files not in the store's codec (outbox messages are not counted)
	Corrupt    int // .zst files whose frame header is damaged (kept)
	Busy       int // skipped: a writer held the message
}

// metaRawAccounted marks the first complete sweep: from then on every raw
// file has its accounting row, and Usage stops estimating.
const metaRawAccounted = "raw.accounted"

// SweepMessageFiles puts the raw files and their accounting in order
// after crashes, deletions and daemons before migration 0014. Where a
// writer could still be at work it touches only what is older than
// olderThan, and it skips a message a writer holds:
//   - stale temporary and staged files go, and so do the files of messages
//     without a row in the directories of the store's own accounts and
//     the empty directories of unknown accounts (a directory with files
//     may belong to another store beside this one, and stays);
//   - of a message with both a plain and a .zst file the newer stays, a
//     damaged .zst losing;
//   - files without an accounting row get one (in batches), rows without a
//     file go, and wrong rows are corrected;
//   - files not in the store's codec are counted (Misplaced), for the
//     conversion to deal with.
//
// Once a sweep completes, meta raw.accounted is "done".
func (s *Store) SweepMessageFiles(ctx context.Context, olderThan time.Duration) (SweepResult, error) {
	var res SweepResult
	cutoff := time.Now().Add(-olderThan)
	res.Staged = s.sweepStaging(cutoff)
	entries, err := os.ReadDir(s.MessageDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("read message directory: %w", err)
	}
	withDir := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		acc := e.Name()
		if !e.IsDir() || checkPathSegment(acc) != nil {
			continue
		}
		withDir[acc] = true
		if err := s.sweepAccount(ctx, acc, cutoff, &res); err != nil {
			return res, err
		}
	}
	accounts, err := s.accountsWithRawRows(ctx)
	if err != nil {
		return res, err
	}
	for _, acc := range accounts {
		if !withDir[acc] {
			if err := s.dropMissingRows(ctx, acc, nil, &res); err != nil {
				return res, err
			}
		}
	}
	if err := s.SetMeta(ctx, metaRawAccounted, "done"); err != nil {
		return res, err
	}
	return res, nil
}

// sweepRow is what the sweep knows of a message's row.
type sweepRow struct {
	role      string
	codec     string // "" when the file has no accounting row
	bytes     int64
	diskBytes int64
}

// sweepAccount sweeps one account's directory.
func (s *Store) sweepAccount(ctx context.Context, accountID string, cutoff time.Time, res *SweepResult) error {
	dir := s.accountDir(accountID)
	var known bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = ?)
		OR EXISTS (SELECT 1 FROM messages WHERE account_id = ?)`, accountID, accountID).Scan(&known); err != nil {
		return fmt.Errorf("sweep message files: %w", err)
	}
	if !known {
		// Not necessarily a leftover of an account deleted here: two
		// stores in one data directory share messages/, and each knows
		// only its own accounts. Only an empty directory goes; the files
		// of one this store does not know are never its to judge.
		if info, err := os.Lstat(dir); err == nil && info.ModTime().Before(cutoff) {
			switch err := os.Remove(dir); {
			case err == nil:
				res.Dirs++
			case !errors.Is(err, fs.ErrNotExist):
				if _, seen := s.foreignDirs.LoadOrStore(accountID, true); !seen {
					s.log.Info("message directory of an unknown account left alone", "account", accountID)
				}
			}
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s.dropMissingRows(ctx, accountID, nil, res)
		}
		return fmt.Errorf("read message directory: %w", err)
	}
	files := map[string]*rawFiles{}
	var ids []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone meanwhile
		}
		name := e.Name()
		if strings.HasSuffix(name, tmpSuffix) {
			if info.ModTime().Before(cutoff) {
				s.sweepTemp(accountID, name, res)
			}
			continue
		}
		id := strings.TrimSuffix(name, RawZstSuffix)
		if checkMessagePath(accountID, id) != nil {
			continue // not a name of ours; left alone
		}
		f := files[id]
		if f == nil {
			f = &rawFiles{}
			files[id] = f
			ids = append(ids, id)
		}
		if id != name {
			f.zst = info
		} else {
			f.plain = info
		}
	}
	sort.Strings(ids)
	for _, chunk := range chunkStrings(ids, 500) {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.sweepRows(ctx, accountID, chunk)
		if err != nil {
			return err
		}
		var backfill []messageFileRow
		for _, id := range chunk {
			f := files[id]
			row, ok := rows[id]
			if !ok {
				s.sweepOrphan(ctx, accountID, id, *f, cutoff, res)
				continue
			}
			resolved := false
			if f.both() {
				settled, ok := s.sweepPair(ctx, accountID, id, res)
				if !ok {
					continue
				}
				*f, resolved = settled, true
			}
			if !f.any() {
				continue
			}
			codec := RawPlain
			if f.zst != nil {
				codec = RawZstd
			}
			if row.role != string(api.RoleOutbox) && codec != s.RawCodec() {
				res.Misplaced++
			}
			if resolved {
				continue // resolveLocked recorded the survivor
			}
			switch disk := f.of(codec).Size(); {
			case row.codec == "":
				info, err := f.info(dir, id)
				if err != nil {
					if errors.Is(err, ErrRawCorrupt) {
						res.Corrupt++
					}
					continue
				}
				backfill = append(backfill, messageFileRow{id: id, info: info})
			case row.codec != codec.String() || row.diskBytes != disk || codec == RawPlain && row.bytes != disk:
				s.sweepFix(ctx, accountID, id, res)
			}
		}
		if err := s.insertBackfill(ctx, backfill, res); err != nil {
			return err
		}
	}
	return s.dropMissingRows(ctx, accountID, files, res)
}

// sweepRows reads the rows of the ids of one account, with their
// accounting; an id without a row is absent from the result.
func (s *Store) sweepRows(ctx context.Context, accountID string, ids []string) (map[string]sweepRow, error) {
	args := append([]any{accountID}, toAny(ids)...)
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, f.role, COALESCE(mf.codec, ''), COALESCE(mf.bytes, -1), COALESCE(mf.disk_bytes, -1)
		FROM messages m JOIN folders f ON f.id = m.folder_id LEFT JOIN message_files mf ON mf.message_id = m.id
		WHERE m.account_id = ? AND m.id IN (`+inPlaceholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sweep message files: %w", err)
	}
	defer rows.Close()
	out := make(map[string]sweepRow, len(ids))
	for rows.Next() {
		var id string
		var r sweepRow
		if err := rows.Scan(&id, &r.role, &r.codec, &r.bytes, &r.diskBytes); err != nil {
			return nil, fmt.Errorf("sweep message files: %w", err)
		}
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sweep message files: %w", err)
	}
	return out, nil
}

// sweepTemp removes a stale temporary file, unless its message is being
// written.
func (s *Store) sweepTemp(accountID, name string, res *SweepResult) {
	id := strings.TrimSuffix(strings.TrimSuffix(name, tmpSuffix), RawZstSuffix)
	if checkMessagePath(accountID, id) == nil {
		h, ok := s.tryLockRaw(accountID, id)
		if !ok {
			res.Busy++
			return
		}
		defer h.unlock()
	}
	if err := os.Remove(filepath.Join(s.accountDir(accountID), name)); err == nil {
		res.Temps++
	}
}

// sweepOrphan removes the files of a message without a row once all of
// them are older than cutoff (an outbox message's file is written just
// before its row).
func (s *Store) sweepOrphan(ctx context.Context, accountID, id string, f rawFiles, cutoff time.Time, res *SweepResult) {
	for _, info := range []fs.FileInfo{f.plain, f.zst} {
		if info != nil && !info.ModTime().Before(cutoff) {
			return
		}
	}
	h, ok := s.tryLockRaw(accountID, id)
	if !ok {
		res.Busy++
		return
	}
	defer h.unlock()
	if exists, err := s.messageExists(ctx, accountID, id); err != nil || exists {
		return
	}
	s.unlinkRaw(h.l, h.dir, id, nil)
	res.Orphans++
}

// sweepPair settles a message with both files; false when it was busy or
// failed.
func (s *Store) sweepPair(ctx context.Context, accountID, id string, res *SweepResult) (rawFiles, bool) {
	h, ok := s.tryLockRaw(accountID, id)
	if !ok {
		res.Busy++
		return rawFiles{}, false
	}
	defer h.unlock()
	files, err := statRaw(h.dir, id)
	if err != nil {
		s.log.Warn("sweep message files", "id", id, "err", err)
		return rawFiles{}, false
	}
	if files.both() {
		if files, err = s.resolveLocked(ctx, h, files); err != nil {
			s.log.Warn("settle message files", "id", id, "err", err)
			return rawFiles{}, false
		}
		res.Resolved++
	}
	return files, true
}

// sweepFix corrects a message's accounting row from its file.
func (s *Store) sweepFix(ctx context.Context, accountID, id string, res *SweepResult) {
	h, ok := s.tryLockRaw(accountID, id)
	if !ok {
		res.Busy++
		return
	}
	defer h.unlock()
	files, err := statRaw(h.dir, id)
	if err != nil || !files.any() || files.both() {
		return
	}
	info, err := files.info(h.dir, id)
	if err != nil {
		if errors.Is(err, ErrRawCorrupt) {
			res.Corrupt++
		}
		return
	}
	s.recordRaw(ctx, id, info)
	res.Fixed++
}

// messageFileRow is an accounting row to add.
type messageFileRow struct {
	id   string
	info RawInfo
}

// insertBackfill adds accounting rows for files that had none, in one
// transaction; a row a writer added meanwhile is left as it is.
func (s *Store) insertBackfill(ctx context.Context, rows []messageFileRow, res *SweepResult) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account message files: %w", err)
	}
	defer tx.Rollback()
	added := 0
	for _, r := range rows {
		out, err := tx.ExecContext(ctx, `
			INSERT INTO message_files (message_id, codec, bytes, disk_bytes)
			SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM messages WHERE id = ?)
			ON CONFLICT (message_id) DO NOTHING`, r.id, r.info.Codec.String(), r.info.Bytes, r.info.DiskBytes, r.id)
		if err != nil {
			return fmt.Errorf("account message files: %w", err)
		}
		if n, _ := out.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account message files: %w", err)
	}
	res.Backfilled += added
	return nil
}

// dropMissingRows removes the account's accounting rows whose message has
// no file (files: what the sweep found in the directory; nil when there is
// none).
func (s *Store) dropMissingRows(ctx context.Context, accountID string, files map[string]*rawFiles, res *SweepResult) error {
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT mf.message_id FROM message_files mf JOIN messages m ON m.id = mf.message_id
			WHERE m.account_id = ? AND mf.message_id > ? ORDER BY mf.message_id LIMIT 500`, accountID, after)
		if err != nil {
			return fmt.Errorf("sweep message files: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("sweep message files: %w", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("sweep message files: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if f := files[id]; f != nil && f.any() {
				continue
			}
			if checkMessagePath(accountID, id) != nil {
				continue
			}
			h, ok := s.tryLockRaw(accountID, id)
			if !ok {
				res.Busy++
				continue
			}
			if now, err := statRaw(h.dir, id); err == nil && !now.any() {
				s.dropRawRow(ctx, id)
				res.Dropped++
			}
			h.unlock()
		}
		after = ids[len(ids)-1]
	}
}

// accountsWithRawRows lists the accounts that have accounting rows.
func (s *Store) accountsWithRawRows(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.account_id FROM message_files mf JOIN messages m ON m.id = mf.message_id`)
	if err != nil {
		return nil, fmt.Errorf("sweep message files: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var acc string
		if err := rows.Scan(&acc); err != nil {
			return nil, fmt.Errorf("sweep message files: %w", err)
		}
		out = append(out, acc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sweep message files: %w", err)
	}
	return out, nil
}
