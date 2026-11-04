// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"sync"
)

// Priority orders the waiters of a Limiter.
type Priority int

const (
	// PriorityHigh is mail (the syncer's passes and inbox check, the
	// outbox) and what a user waits for (calendar writes, free/busy, an
	// event list outside the stored window).
	PriorityHigh Priority = iota
	// PriorityLow is background calendar work (calendar passes); it gets
	// a free slot only while no high-priority request is waiting.
	PriorityLow
)

// Limiter bounds the concurrent Graph requests of one account across the
// clients that share it (docs/calendar.md D13: the mail syncer, the
// outbox and the calendar together stay within Outlook's per-mailbox
// limit of 4). It is a two-level semaphore: a released slot goes to the
// oldest high-priority waiter, else to the oldest low-priority one, and a
// low-priority request never takes a free slot while a high-priority one
// waits. Safe for concurrent use; the zero value is not usable.
type Limiter struct {
	mu    sync.Mutex
	slots int
	used  int
	high  []*waiter
	low   []*waiter
}

type waiter struct {
	ready   chan struct{}
	granted bool
}

// NewLimiter returns a limiter with n slots (n < 1 = maxConcurrent).
func NewLimiter(n int) *Limiter {
	if n < 1 {
		n = maxConcurrent
	}
	return &Limiter{slots: n}
}

// Acquire waits for a slot and returns its release function (call it
// exactly once), or ctx's error.
func (l *Limiter) Acquire(ctx context.Context, p Priority) (func(), error) {
	l.mu.Lock()
	if l.used < l.slots && (p == PriorityHigh || len(l.high) == 0) {
		l.used++
		l.mu.Unlock()
		return l.releaseOnce(), nil
	}
	w := &waiter{ready: make(chan struct{})}
	if p == PriorityHigh {
		l.high = append(l.high, w)
	} else {
		l.low = append(l.low, w)
	}
	l.mu.Unlock()

	select {
	case <-w.ready:
		return l.releaseOnce(), nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.granted {
			// The slot arrived as ctx ended: pass it on.
			l.mu.Unlock()
			l.release()
			return nil, ctx.Err()
		}
		l.high = removeWaiter(l.high, w)
		l.low = removeWaiter(l.low, w)
		l.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (l *Limiter) releaseOnce() func() {
	var once sync.Once
	return func() { once.Do(l.release) }
}

// release hands the slot to the next waiter or frees it.
func (l *Limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	var next *waiter
	switch {
	case len(l.high) > 0:
		next, l.high = l.high[0], l.high[1:]
	case len(l.low) > 0:
		next, l.low = l.low[0], l.low[1:]
	}
	if next == nil {
		l.used--
		return
	}
	next.granted = true
	close(next.ready)
}

// InUse reports the slots taken (tests, diagnostics).
func (l *Limiter) InUse() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

func removeWaiter(ws []*waiter, w *waiter) []*waiter {
	for i, x := range ws {
		if x == w {
			return append(ws[:i], ws[i+1:]...)
		}
	}
	return ws
}
