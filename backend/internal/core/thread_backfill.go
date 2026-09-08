// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
)

// metaThreadsLinked records the upgrade pass that links the messages
// stored before threading existed (migration 0011 gave them singleton
// ids): absent = not started, a message id = resume after it, "done" =
// finished.
const (
	metaThreadsLinked   = "threads.linked"
	threadsLinkedDone   = "done"
	threadBackfillBatch = 500
)

// backfillThreads links, in the background and in batches, every message
// the store held before threading existed. Messages written since are
// linked as they arrive, so visiting them again is a no-op. The cursor is
// saved after every batch; an interrupted pass resumes where it stopped.
func (b *Backend) backfillThreads(ctx context.Context) error {
	cursor, found, err := b.store.GetMeta(ctx, metaThreadsLinked)
	if err != nil || cursor == threadsLinkedDone {
		return err
	}
	if !found {
		cursor = ""
	}
	var visited, linked int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		last, n, changed, err := b.store.LinkThreadsBatch(ctx, cursor, threadBackfillBatch)
		if err != nil {
			return err
		}
		visited += n
		linked += changed
		if last == "" {
			break
		}
		cursor = last
		if err := b.store.SetMeta(ctx, metaThreadsLinked, cursor); err != nil {
			return err
		}
	}
	if err := b.store.SetMeta(ctx, metaThreadsLinked, threadsLinkedDone); err != nil {
		return err
	}
	if visited > 0 {
		b.log.Info("conversations linked for existing messages", "messages", visited, "merged", linked)
	}
	return nil
}

// isCancelled reports whether err is the context's doing.
func isCancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
