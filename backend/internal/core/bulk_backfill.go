// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/bulk"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
)

// metaBulkClassified records the upgrade pass that classifies the messages
// stored before bulk mail was recognised (migration 0016): absent = not
// started, "<rule version>:<last message id>" = in progress,
// "<rule version>:done" = finished. A value of another rule version starts
// the pass over (every row is made unclassified again).
const (
	metaBulkClassified = "bulk.classified"
	bulkBackfillRows   = 200
	// bulkBackfillPause lets the sync writers in between two batches.
	bulkBackfillPause = 20 * time.Millisecond
)

// backfillBulk classifies, in the background and in batches, the rows
// whose bulk column is still empty: from the header block of the stored
// raw file when there is one, else from the curated headers kept in the
// row. Rows written since the migration are classified as they arrive, so
// the batches skip them. The cursor is saved after every batch; an
// interrupted pass resumes where it stopped (the rows still to visit are
// the ones still empty).
func (b *Backend) backfillBulk(ctx context.Context) error {
	cursor, found, err := b.store.GetMeta(ctx, metaBulkClassified)
	done := bulk.RuleVersion + ":done"
	if err != nil || cursor == done {
		return err
	}
	if found && !strings.HasPrefix(cursor, bulk.RuleVersion+":") {
		if err := b.store.ResetBulk(ctx); err != nil {
			return err
		}
	}
	var classified int
	prev := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := b.store.ListUnclassified(ctx, bulkBackfillRows)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		if batch[0].ID == prev {
			// A batch that changed nothing would repeat for ever.
			b.log.Warn("bulk classification made no progress")
			return nil
		}
		prev = batch[0].ID
		verdicts := make([]store.BulkVerdict, 0, len(batch))
		for _, c := range batch {
			headers := c.Headers
			if h, ok := b.rawCurated(ctx, c.AccountID, c.ID); ok {
				headers = h
			}
			r := bulk.Classify(headers)
			verdicts = append(verdicts, store.BulkVerdict{ID: c.ID, Bulk: r.Stored(), ListID: r.ListID})
		}
		if err := b.store.SetBulkBatch(ctx, verdicts); err != nil {
			return err
		}
		classified += len(batch)
		if err := b.store.SetMeta(ctx, metaBulkClassified, bulk.RuleVersion+":"+batch[len(batch)-1].ID); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bulkBackfillPause):
		}
	}
	if err := b.store.SetMeta(ctx, metaBulkClassified, done); err != nil {
		return err
	}
	if classified > 0 {
		b.log.Info("bulk mail classified for existing messages", "messages", classified)
	}
	return nil
}

// rawCurated reads the curated headers from the header block of the
// stored raw file (a header-only parse); false when there is no file or
// it cannot be read.
func (b *Backend) rawCurated(ctx context.Context, accountID, id string) (map[string]string, bool) {
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	_, h := mime.ParseHeaderFields(io.LimitReader(f, headBytes), mime.DefaultLimits())
	if h == nil {
		return nil, false
	}
	return h, true
}
