// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"time"
)

// metaSearchIndexed records the upgrade pass that indexes the messages
// stored before the full-text index existed (migration 0013): absent =
// not started, a message id = resume after it, "done" = finished.
const (
	metaSearchIndexed   = "search.indexed"
	searchIndexedDone   = "done"
	searchBackfillRows  = 200
	searchBackfillBytes = 4 << 20
	// searchBackfillPause lets the sync writers in between two batches.
	searchBackfillPause = 20 * time.Millisecond
)

// backfillSearch indexes, in the background and in batches, every message
// the store held before search existed. Messages written since are
// indexed as they arrive, so the batches skip them. The cursor is saved
// after every batch; an interrupted pass resumes where it stopped.
func (b *Backend) backfillSearch(ctx context.Context) error {
	cursor, found, err := b.store.GetMeta(ctx, metaSearchIndexed)
	if err != nil || cursor == searchIndexedDone {
		return err
	}
	if !found {
		cursor = ""
	}
	var indexed int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		last, n, err := b.store.IndexSearchBatch(ctx, cursor, searchBackfillRows, searchBackfillBytes)
		if err != nil {
			return err
		}
		if last == "" {
			break
		}
		indexed += n
		cursor = last
		if err := b.store.SetMeta(ctx, metaSearchIndexed, cursor); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(searchBackfillPause):
		}
	}
	if err := b.store.SetMeta(ctx, metaSearchIndexed, searchIndexedDone); err != nil {
		return err
	}
	if indexed > 0 {
		b.log.Info("search index built for existing messages", "messages", indexed)
	}
	return nil
}
