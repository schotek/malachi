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
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
)

// staleTempAge is how old the temporary file of a write (<name>.tmp) must
// be before a sweep deletes it. A write finishes or gives up long before:
// body downloads time out after minutes.
const staleTempAge = time.Hour

// SweepMessageFiles deletes what the raw message directories hold for no
// message: raw files no row references, once older than olderThan, and
// the temporary files of writes, once older than staleTempAge. The rows
// are authoritative. A raw file is written before its row only by
// EnqueueOutbox, a moment before, so an older file without a row is left
// over: by a removal that failed (Windows refuses to delete an open file),
// a body fetched for a message deleted meanwhile, a crash. Only the names
// the store gives its files are looked at, <message id> and <message
// id>.tmp in an account's directory; other files and the directories stay.
// It returns the number of files deleted. Files it cannot delete are
// logged, and the next sweep tries again.
func (s *Store) SweepMessageFiles(ctx context.Context, olderThan time.Duration) (int, error) {
	dirs, err := os.ReadDir(s.MessageDir())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read message directory: %w", err)
	}
	now := time.Now()
	sw := &messageSweep{s: s, cutoff: now.Add(-olderThan), tmpCutoff: now.Add(-staleTempAge)}
	defer sw.report()
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if err := sw.sweepDir(ctx, filepath.Join(s.MessageDir(), d.Name())); err != nil {
			return sw.removed, err
		}
	}
	return sw.removed, nil
}

// messageSweep is one run of SweepMessageFiles. One batch serves all its
// removals: a directory that refuses them costs the retries once.
type messageSweep struct {
	s                 *Store
	cutoff, tmpCutoff time.Time
	batch             fsretry.Batch
	removed, failed   int
	firstErr          error
}

// sweepDir sweeps one account's directory. Its raw files are looked up
// 500 at a time, and only the ones without a row are stat'ed.
func (sw *messageSweep) sweepDir(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // the account went meanwhile
	}
	if err != nil {
		return fmt.Errorf("read message directory: %w", err)
	}
	raw := make(map[string]fs.DirEntry)
	var ids []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		if id, ok := strings.CutSuffix(name, ".tmp"); ok {
			if isMessageID(id) {
				sw.removeOlder(filepath.Join(dir, name), e, sw.tmpCutoff)
			}
			continue
		}
		if isMessageID(name) {
			raw[name] = e
			ids = append(ids, name)
		}
	}
	for _, chunk := range chunkStrings(ids, 500) {
		if err := ctx.Err(); err != nil {
			return err
		}
		referenced, err := sw.s.messageRowsExist(ctx, chunk)
		if err != nil {
			return err
		}
		for _, id := range chunk {
			if !referenced[id] {
				sw.removeOlder(filepath.Join(dir, id), raw[id], sw.cutoff)
			}
		}
	}
	return nil
}

// removeOlder deletes the file at path when it is a regular file last
// written before cutoff.
func (sw *messageSweep) removeOlder(path string, e fs.DirEntry, cutoff time.Time) {
	info, err := e.Info()
	if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
		return
	}
	switch err := sw.batch.Remove(path); {
	case err == nil:
		sw.removed++
	case !errors.Is(err, fs.ErrNotExist):
		sw.failed++
		if sw.firstErr == nil {
			sw.firstErr = err
		}
	}
}

// report logs the files the sweep could not delete.
func (sw *messageSweep) report() {
	if sw.failed > 0 {
		sw.s.log.Warn("message file sweep left files it could not delete", "count", sw.failed, "err", sw.firstErr)
	}
}

// messageRowsExist reports which of ids have a message row. It asks for the
// id alone, not the account the directory is named after: ids are unique,
// and on a file system that ignores case two account ids that differ only
// in case share one directory.
func (s *Store) messageRowsExist(ctx context.Context, ids []string) (map[string]bool, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM messages WHERE id IN (`+inPlaceholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("look up message rows: %w", err)
	}
	defer rows.Close()
	exist := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("look up message rows: %w", err)
		}
		exist[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("look up message rows: %w", err)
	}
	return exist, nil
}
