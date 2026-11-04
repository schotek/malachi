// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// issuePoll is the wait between two passes while the interval
	// preference is not 0 (manual): Jira has no push channel a desktop can
	// use, and one incremental pass is one search.
	issuePoll = 60 * time.Second
	// lagMargin widens the incremental search back beyond the last pass's
	// start: the site's index trails its writes.
	lagMargin = 5 * time.Minute
	// reconcileEvery is how often a pass enumerates the ids in scope to
	// find deletions, moves and watching changes (which do not touch an
	// issue's updated time); spacesRefresh and meRefresh how often the
	// space list and the user are asked for again.
	reconcileEvery = time.Hour
	spacesRefresh  = 6 * time.Hour
	meRefresh      = 24 * time.Hour

	backoffMin    = 5 * time.Second // first retry delay after a failure
	backoffMax    = 5 * time.Minute // cap of the exponential backoff
	backoffJitter = 0.2             // ±20 % randomisation of every delay
	// authRetry is the wait after the site refused the token: the user
	// fixes it in the account settings, which restarts the syncer anyway.
	authRetry = 30 * time.Minute
	// passRetryAfterCap bounds a Retry-After the syncer honours as a whole.
	passRetryAfterCap = 15 * time.Minute

	// triggerDebounce gathers issue triggers into one pass; triggerBudget
	// is how many such early passes a minute may bring.
	triggerDebounce = 2 * time.Second
	triggerBudget   = 30
)

// SyncPrefs is what the syncer reads from the preferences on every pass.
type SyncPrefs struct {
	// IntervalSeconds 0 means manual: a pass runs only when triggered.
	// Any other value polls the site every issuePoll.
	IntervalSeconds int
	// AttachmentOfflineDays and NeverStoreAttachments decide which files
	// of the messages built are stored (ingest.Policy).
	AttachmentOfflineDays int
	NeverStoreAttachments bool
}

// Deps wires one syncer to the rest of the daemon.
type Deps struct {
	Store  *store.Store
	Remote Remote
	// Client is the client behind Remote: which URLs lie within the site.
	Client *Client
	// InvalidateToken drops a cached token the site refused (optional).
	InvalidateToken func()
	Notifier        api.Notifier // nil = no notifications
	Prefs           func() SyncPrefs
	Log             *slog.Logger
	Now             func() time.Time // nil = time.Now
	// Stored is told of every body stored with the attachment policy it
	// was stored under (see graph.Deps.Stored). nil = nothing.
	Stored func(ctx context.Context, messageID string, pol ingest.Policy)
	// Backoff overrides the retry delay for the given attempt (tests).
	Backoff func(attempt int) time.Duration
}

// passRequest is one queued pass.
type passRequest struct {
	full bool // enumerate the whole window again and reconcile
}

// refreshWaiter is a RefreshIssueWait caller, answered (buffered, once)
// when a pass that refreshed its key ends.
type refreshWaiter struct {
	done chan error
}

// Syncer synchronises one Jira account: a single goroutine (Run) owns the
// passes; Trigger, TriggerIssue, Wake and State are safe from any
// goroutine.
type Syncer struct {
	account store.Account
	deps    Deps
	log     *slog.Logger
	wake    chan struct{}

	mu      sync.Mutex
	state   api.SyncState
	pending struct {
		any, full bool
	}
	keys         map[string]bool // issue keys queued for a refresh
	waiters      []*refreshWaiter
	early        []time.Time // early passes issue triggers brought, within the last minute
	debounce     *time.Timer
	notifiedAuth api.ErrorCode
	// startErr is why the syncer cannot run (a configuration no client
	// can be made of); Run reports it and returns.
	startErr error

	// Owned by the Run goroutine.
	me            User
	meAt          time.Time
	listed        map[string]Space // the site's spaces by id
	spacesAt      time.Time
	lastReconcile time.Time
	passes        int
	retryAfter    time.Duration // the Retry-After of the last failure
}

// NewSyncer prepares a syncer; nothing runs until Run.
func NewSyncer(account store.Account, deps Deps) *Syncer {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Prefs == nil {
		deps.Prefs = func() SyncPrefs { return SyncPrefs{IntervalSeconds: 300} }
	}
	return &Syncer{
		account: account,
		deps:    deps,
		log:     deps.Log.With("component", "jira", "account", account.ID),
		wake:    make(chan struct{}, 1),
		keys:    map[string]bool{},
		state:   api.SyncState{AccountID: api.AccountID(account.ID), Status: api.SyncIdle, Progress: -1},
	}
}

// State returns the live state.
func (s *Syncer) State() api.SyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Trigger queues a pass (full: the whole window again) and wakes the
// syncer. Triggers coalesce; the folder does not matter, a pass covers
// the account.
func (s *Syncer) Trigger(full bool) {
	s.mu.Lock()
	s.pending.any = true
	s.pending.full = s.pending.full || full
	s.mu.Unlock()
	s.Wake()
}

// TriggerIssue queues a refresh of the issue with the key (a notification
// mail named it, a comment was delivered) and schedules a pass after
// triggerDebounce, so that a burst makes one pass; at most triggerBudget
// such passes a minute, later ones wait for the budget. false for a key
// that is no issue key.
func (s *Syncer) TriggerIssue(key string) bool {
	return s.queueIssue(key, nil, triggerDebounce)
}

// queueIssue queues the key (and the waiter) and schedules a pass after
// delay, within the budget.
func (s *Syncer) queueIssue(key string, w *refreshWaiter, delay time.Duration) bool {
	key = cleanIssueKey(flexString(key))
	if key == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[normKey(key)] = true
	if w != nil {
		s.waiters = append(s.waiters, w)
		// Someone waits: a pass scheduled for later comes forward.
		if s.debounce != nil && delay == 0 && s.debounce.Stop() {
			s.debounce = nil
		}
	}
	if s.debounce != nil {
		return true
	}
	keep := s.early[:0]
	for _, t := range s.early {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	s.early = keep
	if len(s.early) >= triggerBudget {
		delay = max(delay, time.Minute-now.Sub(s.early[0]))
	}
	s.early = append(s.early, now.Add(delay))
	s.debounce = time.AfterFunc(delay, func() {
		s.mu.Lock()
		s.debounce = nil
		s.pending.any = true
		s.mu.Unlock()
		s.Wake()
	})
	return true
}

// Wake interrupts whatever wait the syncer is in.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// takePending returns the queued pass, the issue keys to refresh in it and
// their waiters.
func (s *Syncer) takePending() (passRequest, bool, []string, []*refreshWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req := passRequest{full: s.pending.full}
	any := s.pending.any
	s.pending.any, s.pending.full = false, false
	var keys []string
	for k := range s.keys {
		keys = append(keys, k)
	}
	s.keys = map[string]bool{}
	if len(keys) > 0 && s.debounce != nil {
		// This pass serves them: the scheduled one is not needed.
		s.debounce.Stop()
		s.debounce = nil
	}
	sort.Strings(keys)
	waiters := s.waiters
	s.waiters = nil
	return req, any, keys, waiters
}

// requeue puts back keys and waiters a failed pass could not serve.
func (s *Syncer) requeue(keys []string, waiters []*refreshWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		s.keys[k] = true
	}
	s.waiters = append(s.waiters, waiters...)
}

func (s *Syncer) now() time.Time { return s.deps.Now() }

func (s *Syncer) prefs() SyncPrefs {
	p := s.deps.Prefs()
	if p.IntervalSeconds < 0 {
		p.IntervalSeconds = 0
	}
	return p
}

// setState applies a change and emits notify.syncState when anything
// observable differs.
func (s *Syncer) setState(mut func(st *api.SyncState)) {
	s.mu.Lock()
	prev := s.state
	mut(&s.state)
	s.state.AccountID = api.AccountID(s.account.ID)
	cur := s.state
	s.mu.Unlock()
	if sameState(prev, cur) || s.deps.Notifier == nil {
		return
	}
	s.deps.Notifier.SyncState(api.SyncStateNotification{State: cur})
}

func sameState(a, b api.SyncState) bool {
	if a.Status != b.Status || a.FolderID != b.FolderID || a.Progress != b.Progress ||
		a.PendingOutbox != b.PendingOutbox || a.FailedOutbox != b.FailedOutbox {
		return false
	}
	if (a.LastSync == nil) != (b.LastSync == nil) || (a.LastSync != nil && !a.LastSync.Equal(*b.LastSync)) {
		return false
	}
	if (a.Error == nil) != (b.Error == nil) {
		return false
	}
	return a.Error == nil || (a.Error.Code == b.Error.Code && a.Error.Message == b.Error.Message)
}

func (s *Syncer) setProgress(pct int) {
	s.setState(func(st *api.SyncState) { st.Progress = max(0, min(100, pct)) })
}

// Run drives the account until ctx ends: run passes and, on failure,
// degrade the state and wait with backoff. The state it ends a successful
// pass with is account-wide (no folder): clients reload the account's
// folders, the views included, when they see it.
func (s *Syncer) Run(ctx context.Context) error {
	if s.startErr == nil && (s.account.Config.Jira == nil || s.deps.Remote == nil) {
		s.startErr = api.NewError(api.CodeInvalidArgument, "jira: the account has no jira configuration")
	}
	if s.startErr != nil {
		s.fail(s.startErr)
		return s.startErr
	}
	attempt := 0
	req, _, keys, waiters := s.takePending()
	for {
		if err := ctx.Err(); err != nil {
			finish(waiters, api.NewError(api.CodeCancelled, "cancelled"))
			return err
		}
		err := s.cycle(ctx, req, keys)
		if ctx.Err() != nil {
			finish(waiters, api.NewError(api.CodeCancelled, "cancelled"))
			return ctx.Err()
		}
		if err != nil {
			s.fail(err)
			if len(waiters) > 0 {
				finish(waiters, ToAPIError(err))
			}
			// The keys stay queued for the next pass; their waiters
			// were answered.
			s.requeue(keys, nil)
			if s.sleep(ctx, s.retryDelay(err, &attempt)) {
				attempt = 0
			}
			req, _, keys, waiters = s.takePending()
			continue
		}
		finish(waiters, nil)
		attempt = 0
		s.mu.Lock()
		s.notifiedAuth = 0
		s.mu.Unlock()
		now := time.Now()
		s.setState(func(st *api.SyncState) {
			st.Status, st.FolderID, st.Progress, st.Error, st.LastSync = api.SyncIdle, "", -1, nil, &now
		})
		var werr error
		if req, keys, waiters, werr = s.wait(ctx); werr != nil {
			finish(waiters, api.NewError(api.CodeCancelled, "cancelled"))
			return werr
		}
	}
}

// finish answers waiters.
func finish(waiters []*refreshWaiter, err error) {
	for _, w := range waiters {
		select {
		case w.done <- err:
		default:
		}
	}
}

// wait blocks until the next pass is due: a trigger or wake, or the poll
// when the interval is not 0.
func (s *Syncer) wait(ctx context.Context) (passRequest, []string, []*refreshWaiter, error) {
	s.mu.Lock()
	ready := s.pending.any
	s.mu.Unlock()
	if !ready {
		var tick <-chan time.Time
		if s.prefs().IntervalSeconds > 0 {
			t := time.NewTimer(issuePoll)
			defer t.Stop()
			tick = t.C
		}
		select {
		case <-ctx.Done():
			return passRequest{}, nil, nil, ctx.Err()
		case <-s.wake:
		case <-tick:
		}
	}
	req, _, keys, waiters := s.takePending()
	return req, keys, waiters, nil
}

// fail degrades the state according to the error and reports credential
// problems once per transition.
func (s *Syncer) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	s.retryAfter = RetryAfter(err)
	if IsUnauthorized(err) && s.deps.InvalidateToken != nil {
		s.deps.InvalidateToken()
	}
	ae := ToAPIError(err)
	if ae.Code == api.CodeCancelled {
		return
	}
	status := api.SyncError
	switch ae.Code {
	case api.CodeNetworkError, api.CodeServerTimeout, api.CodeTLSError, api.CodeOffline:
		status = api.SyncOffline
	case api.CodeAuthFailed, api.CodeAuthRequired:
		status = api.SyncAuthRequired
	}
	s.log.Warn("sync failed", "status", status, "code", ae.Code, "err", ae.Message)
	s.setState(func(st *api.SyncState) {
		st.Status, st.FolderID, st.Progress, st.Error = status, "", -1, ae
	})
	if status != api.SyncAuthRequired {
		return
	}
	s.mu.Lock()
	already := s.notifiedAuth == ae.Code
	s.notifiedAuth = ae.Code
	s.mu.Unlock()
	if already || s.deps.Notifier == nil {
		return
	}
	s.deps.Notifier.AuthRequired(api.AuthRequiredNotification{
		AccountID: api.AccountID(s.account.ID),
		Reason:    ae.Code,
		Message:   ae.Message,
	})
}

// retryDelay is how long to wait after err: authRetry for a refused
// token, a Retry-After the site asked for (capped at passRetryAfterCap) when
// it is longer than the backoff, the exponential backoff otherwise.
func (s *Syncer) retryDelay(err error, attempt *int) time.Duration {
	switch ToAPIError(err).Code {
	case api.CodeAuthFailed, api.CodeAuthRequired:
		if s.deps.Backoff != nil {
			return s.deps.Backoff(*attempt)
		}
		return authRetry
	}
	d := s.backoff(*attempt)
	*attempt++
	if ra := min(s.retryAfter, passRetryAfterCap); ra > d && s.deps.Backoff == nil {
		d = ra
	}
	return d
}

func (s *Syncer) backoff(attempt int) time.Duration {
	if s.deps.Backoff != nil {
		return s.deps.Backoff(attempt)
	}
	d := backoffMin
	for i := 0; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	d = min(d, backoffMax)
	jitter := 1 + (rand.Float64()*2-1)*backoffJitter
	return time.Duration(float64(d) * jitter)
}

// sleep waits d (forever when d <= 0) and reports whether Wake ended it.
func (s *Syncer) sleep(ctx context.Context, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		return true
	case <-timer:
		return false
	}
}

// storageError wraps a store failure into the contract code that maps to
// the "error" sync status.
func storageError(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return api.NewError(api.CodeStorageError, "%s", transport.CleanMessage(err.Error()))
}
