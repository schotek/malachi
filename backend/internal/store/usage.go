// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Usage is how much disk the store takes (system.storage). Byte counts are
// file lengths, not allocated blocks: portable, and the same on a file
// system that compresses by itself.
type Usage struct {
	DatabaseBytes   int64 // store.db with its -wal and -shm files
	MessageBytes    int64 // the raw message files as stored
	ContentBytes    int64 // the messages in them, uncompressed
	AttachmentBytes int64 // the compose-side attachment store (drafts, attachment.import)
	Messages        int   // messages with a raw file
	Compressed      int   // of them, stored compressed
	Partial         int   // messages whose large attachments stay on the server
	RemoteBytes     int64 // those attachments' decoded size
	// Estimated: some files stored before migration 0014 have no
	// accounting row yet (the first sweep has not completed, meta
	// raw.accounted); they are counted from messages.size, as plain files.
	Estimated bool
}

// TotalBytes is the store's footprint: the database, the raw messages and
// the attachment store.
func (u Usage) TotalBytes() int64 { return u.DatabaseBytes + u.MessageBytes + u.AttachmentBytes }

// SavedBytes is what compression saves: the messages' content less their
// files, never below 0 (a frame of incompressible data is a few bytes
// larger than its content).
func (u Usage) SavedBytes() int64 { return max(0, u.ContentBytes-u.MessageBytes) }

// Usage measures the store from the accounting rows, a few aggregate
// queries and three stats, cheap enough to poll every few seconds.
func (s *Store) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	rows, err := s.db.QueryContext(ctx, `SELECT codec, COUNT(*), COALESCE(SUM(bytes), 0), COALESCE(SUM(disk_bytes), 0)
		FROM message_files GROUP BY codec`)
	if err != nil {
		return Usage{}, fmt.Errorf("storage usage: %w", err)
	}
	for rows.Next() {
		var codec string
		var n int
		var content, disk int64
		if err := rows.Scan(&codec, &n, &content, &disk); err != nil {
			rows.Close()
			return Usage{}, fmt.Errorf("storage usage: %w", err)
		}
		u.Messages += n
		u.ContentBytes += content
		u.MessageBytes += disk
		if c, ok := parseRawCodec(codec); ok && c == RawZstd {
			u.Compressed += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Usage{}, fmt.Errorf("storage usage: %w", err)
	}

	accounted, _, err := s.GetMeta(ctx, metaRawAccounted)
	if err != nil {
		return Usage{}, err
	}
	if accounted != "done" {
		var n int
		var size int64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM messages m
			WHERE body_state IN (?, ?) AND NOT EXISTS (SELECT 1 FROM message_files mf WHERE mf.message_id = m.id)`,
			string(BodyFetched), string(BodyFailed)).Scan(&n, &size); err != nil {
			return Usage{}, fmt.Errorf("storage usage: %w", err)
		}
		u.Messages += n
		u.ContentBytes += size
		u.MessageBytes += size
		u.Estimated = n > 0
	}

	remote, err := s.RemoteStats(ctx, "")
	if err != nil {
		return Usage{}, err
	}
	u.Partial, u.RemoteBytes = remote.Messages, remote.Bytes
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM attachments`).Scan(&u.AttachmentBytes); err != nil {
		return Usage{}, fmt.Errorf("storage usage: %w", err)
	}
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		info, err := os.Stat(p)
		switch {
		case err == nil:
			u.DatabaseBytes += info.Size()
		case !errors.Is(err, fs.ErrNotExist):
			return Usage{}, fmt.Errorf("storage usage: %w", err)
		}
	}
	return u, nil
}
