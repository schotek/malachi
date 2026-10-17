// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"
)

// The pace of the background writers.
//
// SQLite has one writer at a time and no queue for the others: a writer
// that finds the lock taken polls for it (busy_timeout, Open), at
// intervals that grow to 100 ms once it has waited about 300 ms, and gives
// up after 5 s (SQLITE_BUSY, "database is locked"). A loop that commits
// one short transaction after another therefore keeps the lock almost all
// the time — the gaps between its transactions last microseconds, and a
// poller at 100 ms never lands in one — and two such loops starve each
// other as well as every other writer. Measured on this store (one writer
// holding the lock 500 ms at a time, as the board drain's budget allows,
// plus four loops of back-to-back short transactions, as the calendar
// sync's one transaction per event does during a full re-list): every
// writer, the 500 ms one included, waited the full 5 s and failed. The
// first start of a daemon runs several such loops at once: the board's
// re-evaluation, the calendar re-list of every account, the invitation
// scan, the raw-file maintenance.
//
// Paced is how the background loops write: one at a time, in turn, and
// each turn is followed by a pause as long as the writing took, so that
// the lock is free at least half of the time for the writers that do not
// take turns — an RPC mutation, a syncer committing a message — whose
// polling then lands in a free moment within a few attempts.

// pacedMinPause is the shortest pause after a paced write: Windows' timer
// cannot wait less, and a poller needs a gap it can land in.
const pacedMinPause = time.Millisecond

// Paced runs fn, a background writer's burst of writes, in the store's
// turn: no other paced writer runs meanwhile, and after fn the turn is
// held idle for as long as fn took (at least pacedMinPause), which leaves
// the write lock free for the writers that do not take turns. fn's error
// is returned as it is (the pause follows all the same); ctx ending while
// waiting for the turn returns ctx.Err() without running fn, and ends the
// pause early. fn must not call Paced itself.
func (s *Store) Paced(ctx context.Context, fn func() error) error {
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.turn }()
	start := time.Now()
	err := fn()
	pause := max(time.Since(start), pacedMinPause)
	t := time.NewTimer(pause)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	return err
}
