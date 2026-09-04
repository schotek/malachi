// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// inboxPoll is the shortest wait between two inbox delta queries; the
	// other folders follow the account's sync interval. Graph has no push
	// channel a desktop can use, so polling is the whole story.
	inboxPoll = 60 * time.Second

	backoffMin    = 5 * time.Second // first retry delay after a failure
	backoffMax    = 5 * time.Minute // cap of the exponential backoff
	backoffJitter = 0.2             // ±20 % randomisation of every delay
	// authRetry is the wait after a token problem: the user may sign in
	// again in the desktop settings without anything waking the syncer.
	authRetry = 5 * time.Minute

	maxRawMessageBytes = 25 << 20 // larger messages are never downloaded (bodyState tooBig)
	bodyConcurrency    = 4        // parallel $value downloads (the service's per-mailbox limit)
	unfetchedBatch     = 200      // ListUnfetched page

	maxOpAttempts = 10 // a queued change is dropped after this many failures
	opBackoffMin  = 30 * time.Second
	opBackoffMax  = time.Hour
)

// SyncPrefs is what the syncer reads from the preferences on every pass.
type SyncPrefs struct {
	IntervalSeconds int // polling interval of every folder; 0 = manual only
	OfflineDays     int // retention window; 0 = everything
}

// Deps wires one syncer to the rest of the daemon.
type Deps struct {
	Store *store.Store
	// Token returns an access token for the mailbox; an *api.Error with
	// CodeAuthRequired or CodeUnavailable drives the state machine.
	Token func(ctx context.Context) (string, error)
	// Invalidate drops a cached token the service rejected (optional).
	Invalidate func()
	Notifier   api.Notifier // nil = no notifications
	Prefs      func() SyncPrefs
	Log        *slog.Logger
	Now        func() time.Time // retention window and op scheduling; nil = time.Now
	// BaseURL and HTTP override the Graph endpoint (tests).
	BaseURL string
	HTTP    *http.Client
	// Backoff overrides the retry delay for the given attempt (0-based).
	Backoff func(attempt int) time.Duration
	// Sleep is the client's Retry-After wait (tests).
	Sleep func(ctx context.Context, d time.Duration) error
}

// request is one queued pass.
type request struct {
	folder api.FolderID // "" = every folder
	full   bool         // resynchronise from scratch (ignore delta cursors)
}

// Syncer synchronises one Graph account: a single goroutine (Run) owns the
// passes; Trigger, Wake and State are safe from any goroutine.
type Syncer struct {
	account store.Account
	deps    Deps
	log     *slog.Logger
	client  *Client

	wake chan struct{}

	mu      sync.Mutex
	state   api.SyncState
	pending struct {
		any, all, full bool
		folders        map[api.FolderID]bool
	}
	notifiedAuth api.ErrorCode

	// Owned by the Run goroutine.
	roles     map[string]api.FolderRole // Graph folder id → role
	lastSince time.Time
	passes    int
	inboxID   string
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
		deps.Prefs = func() SyncPrefs { return SyncPrefs{IntervalSeconds: 300, OfflineDays: 30} }
	}
	s := &Syncer{
		account: account,
		deps:    deps,
		log:     deps.Log.With("component", "graph", "account", account.ID),
		wake:    make(chan struct{}, 1),
		state:   api.SyncState{AccountID: api.AccountID(account.ID), Status: api.SyncIdle, Progress: -1},
	}
	s.client = NewClient(Options{
		BaseURL: deps.BaseURL, HTTP: deps.HTTP, Token: deps.Token, Invalidate: deps.Invalidate,
		Log: s.log, Sleep: deps.Sleep,
	})
	return s
}

// State returns the live state.
func (s *Syncer) State() api.SyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Trigger queues a pass for one folder or (empty) the account and wakes
// the syncer. Triggers coalesce.
func (s *Syncer) Trigger(folder api.FolderID, full bool) {
	s.mu.Lock()
	s.pending.any = true
	s.pending.full = s.pending.full || full
	if folder == "" {
		s.pending.all = true
	} else {
		if s.pending.folders == nil {
			s.pending.folders = map[api.FolderID]bool{}
		}
		s.pending.folders[folder] = true
	}
	s.mu.Unlock()
	s.Wake()
}

// Wake interrupts whatever wait the syncer is in.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Syncer) takePending() (request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pending.any {
		return request{}, false
	}
	req := request{full: s.pending.full}
	if !s.pending.all && len(s.pending.folders) == 1 {
		for f := range s.pending.folders {
			req.folder = f
		}
	}
	s.pending.any, s.pending.all, s.pending.full, s.pending.folders = false, false, false, nil
	return req, true
}

func (s *Syncer) now() time.Time { return s.deps.Now() }

func (s *Syncer) prefs() SyncPrefs {
	p := s.deps.Prefs()
	if p.OfflineDays < 0 {
		p.OfflineDays = 0
	}
	if p.IntervalSeconds < 0 {
		p.IntervalSeconds = 0
	}
	return p
}

// windowSince is the start of the retention window (zero = all).
func (s *Syncer) windowSince(days int) time.Time {
	if days <= 0 {
		return time.Time{}
	}
	t := s.now()
	return time.Date(t.Year(), t.Month(), t.Day()-days, 0, 0, 0, 0, time.UTC)
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
	if a.Status != b.Status || a.FolderID != b.FolderID || a.Progress != b.Progress || a.PendingOutbox != b.PendingOutbox {
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

// Run drives the account until ctx ends: run passes, and on failure
// degrade the state and wait with backoff.
func (s *Syncer) Run(ctx context.Context) error {
	attempt := 0
	req, _ := s.takePending()
	req.folder = "" // the first pass covers everything
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.cycle(ctx, req)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			s.fail(err)
			if s.sleep(ctx, s.retryDelay(err, &attempt)) {
				attempt = 0
			}
			req, _ = s.takePending()
			continue
		}
		attempt = 0
		s.mu.Lock()
		s.notifiedAuth = 0
		s.mu.Unlock()
		now := time.Now()
		s.setState(func(st *api.SyncState) {
			st.Status, st.FolderID, st.Progress, st.Error, st.LastSync = api.SyncIdle, "", -1, nil, &now
		})
		if req, err = s.wait(ctx); err != nil {
			return err
		}
	}
}

// cycle is one pass: resolve roles and folders, push local operations,
// synchronise the requested folders (all by default), then apply the
// removals collected along the way (so a move shows up as a move, not as
// a delete followed by a create).
func (s *Syncer) cycle(ctx context.Context, req request) error {
	prefs := s.prefs()
	since := s.windowSince(prefs.OfflineDays)
	full := req.full || s.passes == 0 || !since.Equal(s.lastSince)
	s.setState(func(st *api.SyncState) { st.Status, st.FolderID, st.Progress = api.SyncSyncing, "", 0 })

	if s.roles == nil || full {
		roles, err := resolveRoles(ctx, s.client)
		if err != nil {
			return err
		}
		s.roles = roles
	}
	listed, err := listFolders(ctx, s.client, s.roles)
	if err != nil {
		return err
	}
	stored, removed, err := s.deps.Store.UpsertFolders(ctx, s.account.ID, listed)
	if err != nil {
		return storageError(err)
	}
	if len(removed) > 0 {
		s.log.Info("folders removed on server", "count", len(removed))
	}
	s.inboxID = ""
	byMailbox := make(map[string]store.Folder, len(stored))
	for _, f := range stored {
		byMailbox[f.Mailbox] = f
		if f.Role == api.RoleInbox {
			s.inboxID = f.ID
		}
	}

	if err := s.pushOps(ctx, byMailbox); err != nil {
		return err
	}

	var targets []store.Folder
	for _, f := range stored {
		if req.folder != "" && api.FolderID(f.ID) != req.folder {
			continue
		}
		targets = append(targets, f)
	}
	n := len(targets)
	tombstones := map[string][]string{} // folder id → remote ids removed
	touched := map[string]bool{}
	for i, f := range targets {
		fresh, err := s.deps.Store.GetFolder(ctx, s.account.ID, f.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return storageError(err)
		}
		base := float64(i) / float64(n)
		s.setState(func(st *api.SyncState) { st.FolderID, st.Progress = api.FolderID(fresh.ID), int(base*100) })
		gone, err := s.syncFolder(ctx, fresh, since, full, func(frac float64) {
			s.setProgress(int((base + frac/float64(n)) * 100))
		})
		if err != nil {
			if IsNotFound(err) {
				s.log.Warn("folder vanished during sync", "folder", fresh.ID)
				continue
			}
			return err
		}
		// Counts now, so the UI sees the folder fill in during a long first
		// pass; again after the tombstones below.
		if _, _, err := s.deps.Store.RecountFolder(ctx, fresh.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
		if len(gone) > 0 {
			tombstones[fresh.ID] = gone
			touched[fresh.ID] = true
		}
	}
	for folderID, ids := range tombstones {
		if err := s.deps.Store.DeleteMessagesByRemoteID(ctx, folderID, ids); err != nil {
			return storageError(err)
		}
	}
	for folderID := range touched {
		if _, _, err := s.deps.Store.RecountFolder(ctx, folderID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	s.lastSince = since
	s.passes++
	return nil
}

// wait blocks until the next pass is due: a trigger or wake, the inbox
// poll (every inboxPoll, or the interval when that is shorter), or the
// interval tick for every folder. With interval 0 only triggers count.
func (s *Syncer) wait(ctx context.Context) (request, error) {
	if req, ok := s.takePending(); ok {
		return req, nil
	}
	var all, inbox <-chan time.Time
	if iv := s.prefs().IntervalSeconds; iv > 0 {
		every := time.Duration(iv) * time.Second
		t := time.NewTicker(every)
		defer t.Stop()
		all = t.C
		if s.inboxID != "" && every > inboxPoll {
			it := time.NewTicker(inboxPoll)
			defer it.Stop()
			inbox = it.C
		}
	}
	select {
	case <-ctx.Done():
		return request{}, ctx.Err()
	case <-s.wake:
		req, _ := s.takePending()
		return req, nil
	case <-inbox:
		return request{folder: api.FolderID(s.inboxID)}, nil
	case <-all:
		return request{}, nil
	}
}

// fail degrades the state according to the error and reports credential
// problems once per transition.
func (s *Syncer) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	ae := ToAPIError(err)
	if ae.Code == api.CodeCancelled {
		return
	}
	status := api.SyncError
	switch ae.Code {
	case api.CodeNetworkError, api.CodeServerTimeout, api.CodeTLSError:
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

// retryDelay is how long to wait after err: authRetry for token problems
// (the user may fix them without touching Malachi), the exponential
// backoff otherwise.
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
