// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
)

// metaBoardRules records the board's first evaluation of the stored mail
// (migration 0017 creates the board empty and scans nothing): absent = not
// started, "<rules version>:<last message id>" = in progress,
// "<rules version>:done" = finished. A value of another rules version
// starts the pass over and evaluates every case again; so does turning the
// board on, or a window growing beyond the longest one before
// (restartBoardBackfill).
const metaBoardRules = "board.rules"

// The pace of the pass; variables so that tests can change them.
var (
	boardBackfillRows  = 2000
	boardBackfillPause = 20 * time.Millisecond
	// boardBackfillBatched, when set, is called after each batch the pass
	// marked, before it records its cursor (tests).
	boardBackfillBatched func()
)

func boardRulesDone() string { return board.RulesVersion + ":done" }

// backfillBoard marks dirty, in batches of messages, the threads with a
// visible member within the longest window of the preferences; the worker
// evaluates them. The cursor is saved after every batch; an interrupted
// pass resumes where it stopped. With the board off it does nothing (the
// pass runs when it is turned on). A restart (restartBoardBackfill) while
// a pass runs makes that pass start over with the preferences as they are
// then, before it records anything more: it never overwrites the restart.
func (b *Backend) backfillBoard(ctx context.Context) error {
	bs := &b.board
	bs.backfillMu.Lock()
	defer bs.backfillMu.Unlock()
	for {
		restarted, err := b.backfillBoardPass(ctx, bs.backfillGen.Load())
		if err != nil || !restarted {
			return err
		}
	}
}

// backfillBoardPass is one pass of backfillBoard under the restart
// generation gen; restarted when a restart came meanwhile.
func (b *Backend) backfillBoardPass(ctx context.Context, gen int64) (restarted bool, err error) {
	cursor, found, err := b.store.GetMeta(ctx, metaBoardRules)
	if err != nil || cursor == boardRulesDone() {
		return false, err
	}
	prefs, err := b.boardPrefs(ctx)
	if err != nil || !prefs.Enabled {
		return false, err
	}
	b.board.ready.Store(false)
	prefix := board.RulesVersion + ":"
	after := ""
	switch {
	case found && strings.HasPrefix(cursor, prefix):
		after = strings.TrimPrefix(cursor, prefix)
	case found:
		// Other rules: every case is judged again, and the pass starts over.
		if err := b.store.MarkBoardCasesDirty(ctx); err != nil {
			return false, err
		}
	}
	since := boardSinceOf(b.boardNow(), prefs.Windows)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		last, visited, err := b.store.MarkBoardDirtyBatch(ctx, since, after, boardBackfillRows)
		if err != nil {
			return false, err
		}
		if visited == 0 {
			break
		}
		after = last
		if boardBackfillBatched != nil {
			boardBackfillBatched()
		}
		if ok, err := b.recordBoardBackfill(ctx, gen, prefix+after); err != nil || !ok {
			return !ok && err == nil, err
		}
		b.wakeBoard()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(boardBackfillPause):
		}
	}
	if ok, err := b.recordBoardBackfill(ctx, gen, boardRulesDone()); err != nil || !ok {
		return !ok && err == nil, err
	}
	b.wakeBoard()
	return false, nil
}

// recordBoardBackfill records the pass's progress unless a restart came
// after the pass of generation gen began (ok false: nothing written).
func (b *Backend) recordBoardBackfill(ctx context.Context, gen int64, value string) (ok bool, err error) {
	bs := &b.board
	bs.backfillMetaMu.Lock()
	defer bs.backfillMetaMu.Unlock()
	if bs.backfillGen.Load() != gen {
		return false, nil
	}
	return true, b.store.SetMeta(ctx, metaBoardRules, value)
}

// restartBoardBackfill starts the pass over (the board turned on, or a
// longer window): it resets the record, which a pass running now notices
// before it records anything more (it then starts over itself), and runs
// a pass in the background of the worker, which waits for it when it
// stops. Before StartSync it only resets the record, and the next
// Maintain runs the pass.
func (b *Backend) restartBoardBackfill(ctx context.Context) {
	bs := &b.board
	bs.backfillMetaMu.Lock()
	bs.backfillGen.Add(1)
	err := b.store.SetMeta(ctx, metaBoardRules, board.RulesVersion+":")
	bs.backfillMetaMu.Unlock()
	if err != nil {
		b.log.Warn("board: restart the first evaluation", "err", err)
		return
	}
	bs.ready.Store(false)
	b.goBoard(func(wctx context.Context) {
		if err := b.backfillBoard(wctx); err != nil && !isCancelled(err) {
			b.log.Warn("board: first evaluation", "err", err)
		}
	})
}
