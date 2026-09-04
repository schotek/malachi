// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// syncStateInterval is the minimum spacing of progress-only
// notify.syncState notifications per account (docs/api.md §5).
const syncStateInterval = 500 * time.Millisecond

// notifyQueueSize bounds the events waiting for the sender goroutine.
// Sync states occupy at most one slot per account (latest wins); the rest
// is best-effort and dropped when a client stalls the RPC writer for that
// long.
const notifyQueueSize = 1024

// clock is the time source of the coalescer; tests substitute a fake.
type clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) stopper
}

type stopper interface{ Stop() bool }

type realClock struct{}

func (realClock) Now() time.Time                              { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }

type eventKind int

const (
	evNewMessage eventKind = iota
	evAuthRequired
	evAccountsChanged
	evSyncState
)

type event struct {
	kind       eventKind
	newMessage api.NewMessageNotification
	auth       api.AuthRequiredNotification
	accountID  api.AccountID // evSyncState: the value is read from accountState.next
}

// accountState is the coalescer's memory of one account.
type accountState struct {
	seen   bool
	last   api.SyncState // last state accepted for delivery
	lastAt time.Time
	next   api.SyncState // what the sender goroutine delivers for the queued event
	queued bool          // an evSyncState for this account waits in the channel

	pending *api.SyncState // newest progress-only update held back by the timer
	timer   stopper
}

// coalescingNotifier sits between the sync engine and the RPC server. It
// never blocks the caller: every event is delivered from one sender
// goroutine, because rpc.Server's broadcast writes synchronously to each
// client and a stalled client would otherwise stall a syncer.
//
// NewMessage, AuthRequired and AccountsChanged pass through in order.
// SyncState is delivered at once when status, folderId, error code,
// lastSync or pendingOutbox differ from the last delivered state of that
// account; a progress-only change is delivered at most every
// syncStateInterval, with a trailing timer that delivers the newest value.
type coalescingNotifier struct {
	inner    api.Notifier
	log      *slog.Logger
	clock    clock
	interval time.Duration

	ch       chan event
	stop     chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	accounts map[api.AccountID]*accountState
}

// newCoalescingNotifier starts the sender goroutine. inner must be
// nil-safe itself or non-nil; clk nil means the wall clock.
func newCoalescingNotifier(inner api.Notifier, log *slog.Logger, clk clock) *coalescingNotifier {
	if clk == nil {
		clk = realClock{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &coalescingNotifier{
		inner:    inner,
		log:      log,
		clock:    clk,
		interval: syncStateInterval,
		ch:       make(chan event, notifyQueueSize),
		stop:     make(chan struct{}),
		accounts: map[api.AccountID]*accountState{},
	}
	go c.run()
	return c
}

var _ api.Notifier = (*coalescingNotifier)(nil)

// Close stops the sender goroutine; queued events are dropped.
func (c *coalescingNotifier) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
}

func (c *coalescingNotifier) NewMessage(n api.NewMessageNotification) {
	c.enqueue(event{kind: evNewMessage, newMessage: n})
}

func (c *coalescingNotifier) AuthRequired(n api.AuthRequiredNotification) {
	c.enqueue(event{kind: evAuthRequired, auth: n})
}

func (c *coalescingNotifier) AccountsChanged(api.AccountsChangedNotification) {
	c.enqueue(event{kind: evAccountsChanged})
}

func (c *coalescingNotifier) SyncState(n api.SyncStateNotification) {
	st := n.State
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accounts[st.AccountID]
	if a == nil {
		a = &accountState{}
		c.accounts[st.AccountID] = a
	}
	now := c.clock.Now()
	if !a.seen || syncStateChanged(a.last, st) {
		if a.timer != nil {
			a.timer.Stop()
			a.timer = nil
		}
		a.pending = nil
		c.accept(a, st, now)
		return
	}
	if a.pending == nil && a.last.Progress == st.Progress {
		return // nothing new
	}
	if a.timer == nil && now.Sub(a.lastAt) >= c.interval {
		c.accept(a, st, now)
		return
	}
	a.pending = &st
	if a.timer == nil {
		wait := c.interval - now.Sub(a.lastAt)
		if wait < 0 {
			wait = 0
		}
		id := st.AccountID
		a.timer = c.clock.AfterFunc(wait, func() { c.flush(id) })
	}
}

// flush delivers the progress update held back by the timer.
func (c *coalescingNotifier) flush(id api.AccountID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accounts[id]
	if a == nil {
		return
	}
	a.timer = nil
	if a.pending != nil {
		st := *a.pending
		a.pending = nil
		c.accept(a, st, c.clock.Now())
	}
}

// accept records st as the last delivered state and queues it. Called with
// c.mu held.
func (c *coalescingNotifier) accept(a *accountState, st api.SyncState, now time.Time) {
	a.seen = true
	a.last = st
	a.lastAt = now
	a.next = st
	if a.queued {
		return // the queued event picks up next when it runs
	}
	a.queued = true
	c.enqueue(event{kind: evSyncState, accountID: st.AccountID})
}

func (c *coalescingNotifier) enqueue(ev event) {
	select {
	case c.ch <- ev:
	default:
		c.log.Debug("notification dropped: queue full", "kind", ev.kind)
		if ev.kind == evSyncState {
			c.mu.Lock()
			if a := c.accounts[ev.accountID]; a != nil {
				a.queued = false
			}
			c.mu.Unlock()
		}
	}
}

func (c *coalescingNotifier) run() {
	for {
		select {
		case <-c.stop:
			return
		case ev := <-c.ch:
			c.deliver(ev)
		}
	}
}

func (c *coalescingNotifier) deliver(ev event) {
	switch ev.kind {
	case evNewMessage:
		c.inner.NewMessage(ev.newMessage)
	case evAuthRequired:
		c.inner.AuthRequired(ev.auth)
	case evAccountsChanged:
		c.inner.AccountsChanged(api.AccountsChangedNotification{})
	case evSyncState:
		c.mu.Lock()
		a := c.accounts[ev.accountID]
		if a == nil {
			c.mu.Unlock()
			return
		}
		st := a.next
		a.queued = false
		c.mu.Unlock()
		c.inner.SyncState(api.SyncStateNotification{State: st})
	}
}

// syncStateChanged reports whether cur differs from prev in anything but
// progress.
func syncStateChanged(prev, cur api.SyncState) bool {
	if prev.Status != cur.Status || prev.FolderID != cur.FolderID || prev.PendingOutbox != cur.PendingOutbox {
		return true
	}
	if errorCode(prev.Error) != errorCode(cur.Error) {
		return true
	}
	switch {
	case prev.LastSync == nil && cur.LastSync == nil:
		return false
	case prev.LastSync == nil || cur.LastSync == nil:
		return true
	default:
		return !prev.LastSync.Equal(*cur.LastSync)
	}
}

func errorCode(e *api.Error) api.ErrorCode {
	if e == nil {
		return 0
	}
	return e.Code
}

// outboxAwareNotifier completes every notify.syncState with the account's
// pendingOutbox count before it reaches the coalescer, so the sync engine
// (which knows nothing about the outbox) and the outbox worker report one
// consistent state. Other events pass through.
type outboxAwareNotifier struct {
	b     *Backend
	inner api.Notifier
}

var _ api.Notifier = outboxAwareNotifier{}

func (n outboxAwareNotifier) NewMessage(ev api.NewMessageNotification) { n.inner.NewMessage(ev) }
func (n outboxAwareNotifier) AuthRequired(ev api.AuthRequiredNotification) {
	n.inner.AuthRequired(ev)
}
func (n outboxAwareNotifier) AccountsChanged(ev api.AccountsChangedNotification) {
	n.inner.AccountsChanged(ev)
}
func (n outboxAwareNotifier) SyncState(ev api.SyncStateNotification) {
	ev.State.PendingOutbox = n.b.pendingOutbox(string(ev.State.AccountID))
	n.inner.SyncState(ev)
}

// forwardingNotifier hands events to whatever notifier the backend has at
// the time of the call (nil before SetNotifier: the event is dropped), so
// the sync engine can be built before the RPC server exists.
type forwardingNotifier struct{ b *Backend }

var _ api.Notifier = forwardingNotifier{}

func (f forwardingNotifier) NewMessage(n api.NewMessageNotification) {
	if nn := f.b.getNotifier(); nn != nil {
		nn.NewMessage(n)
	}
}

func (f forwardingNotifier) SyncState(n api.SyncStateNotification) {
	if nn := f.b.getNotifier(); nn != nil {
		nn.SyncState(n)
	}
}

func (f forwardingNotifier) AuthRequired(n api.AuthRequiredNotification) {
	if nn := f.b.getNotifier(); nn != nil {
		nn.AuthRequired(n)
	}
}

func (f forwardingNotifier) AccountsChanged(n api.AccountsChangedNotification) {
	if nn := f.b.getNotifier(); nn != nil {
		nn.AccountsChanged(n)
	}
}
