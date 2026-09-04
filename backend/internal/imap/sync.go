// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// SyncPrefs is what the syncer reads from the preferences on every pass.
type SyncPrefs struct {
	IntervalSeconds int // polling interval; 0 = no periodic pass (IDLE or manual only)
	OfflineDays     int // retention window; 0 = everything
}

// Deps wires one syncer to the rest of the daemon.
type Deps struct {
	Store *store.Store
	// Password returns the account's password; an *api.Error with
	// CodeAuthRequired or CodeKeyringError drives the state machine.
	Password func(ctx context.Context) (string, error)
	Notifier api.Notifier // nil = no notifications
	Prefs    func() SyncPrefs
	Log      *slog.Logger
	Now      func() time.Time // retention window and op scheduling; nil = time.Now
	// CapFilter may hide server capabilities (tests: no IDLE).
	CapFilter func(imap.CapSet) imap.CapSet
	// NoSinceSearch forces the client-side retention window (as if every
	// server rejected SEARCH SINCE); a test hook.
	NoSinceSearch bool
	// Backoff overrides the reconnect delay for the given attempt (0-based);
	// nil = 5 s doubling to 5 min with ±20 % jitter.
	Backoff func(attempt int) time.Duration
}

// request is one queued pass.
type request struct {
	folder api.FolderID // "" = every selectable folder
	full   bool         // ignore change detection
}

// Syncer synchronises one account: a single goroutine (Run) owns the
// connection and the store writes; Trigger, Wake and State are safe from
// any goroutine.
type Syncer struct {
	account store.Account
	deps    Deps
	log     *slog.Logger

	wake chan struct{}

	mu      sync.Mutex
	state   api.SyncState
	pending struct {
		any, all, full bool
		folders        map[api.FolderID]bool
	}
	notifiedAuth api.ErrorCode // auth code already reported since the last good login

	// Owned by the Run goroutine.
	lastSince    time.Time // window start of the last completed pass
	passes       int
	inboxMailbox string
	inboxID      string
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
		log:     deps.Log.With("component", "imap", "account", account.ID),
		wake:    make(chan struct{}, 1),
		state:   api.SyncState{AccountID: api.AccountID(account.ID), Status: api.SyncIdle, Progress: -1},
	}
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

// Wake interrupts whatever wait the syncer is in: a backoff ends, an
// authRequired state retries, an idle connection runs a pass.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// takePending returns and clears the queued request.
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

// windowSince is the SINCE date for the retention window (zero = all).
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

// Run drives the account until ctx ends: connect, serve passes, and on
// failure degrade the state and wait with backoff (or for Wake when
// credentials are the problem).
func (s *Syncer) Run(ctx context.Context) error {
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pw, err := s.deps.Password(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.fail(err)
			if s.sleep(ctx, s.retryDelay(err, &attempt)) {
				attempt = 0
			}
			continue
		}
		sess, err := openSession(ctx, s.account.Config.IMAP, pw, s.deps.CapFilter)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.fail(err)
			if s.sleep(ctx, s.retryDelay(err, &attempt)) {
				attempt = 0
			}
			continue
		}
		attempt = 0
		s.mu.Lock()
		s.notifiedAuth = 0
		s.mu.Unlock()
		err = s.serve(ctx, sess)
		sess.logout()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.fail(err)
		if s.sleep(ctx, s.retryDelay(err, &attempt)) {
			attempt = 0
		}
	}
}

// serve runs passes on one connection until it fails or ctx ends.
func (s *Syncer) serve(ctx context.Context, sess *session) error {
	req, _ := s.takePending()
	req.folder = "" // the first pass on a connection covers everything
	for {
		if err := s.cycle(ctx, sess, req); err != nil {
			return err
		}
		now := time.Now()
		s.setState(func(st *api.SyncState) {
			st.Status, st.FolderID, st.Progress, st.Error, st.LastSync = api.SyncIdle, "", -1, nil, &now
		})
		var err error
		if req, err = s.wait(ctx, sess); err != nil {
			return err
		}
	}
}

// cycle is one pass: discover folders, push local operations, then
// synchronise the requested folders (all selectable ones by default, only
// those whose STATUS changed unless the pass is full, INBOX always).
func (s *Syncer) cycle(ctx context.Context, sess *session, req request) error {
	sess.drainEvents()
	prefs := s.prefs()
	since := s.windowSince(prefs.OfflineDays)
	full := req.full || s.passes == 0 || !since.Equal(s.lastSince)
	s.setState(func(st *api.SyncState) { st.Status, st.FolderID, st.Progress = api.SyncSyncing, "", 0 })

	disc, err := discoverFolders(ctx, sess)
	if err != nil {
		return err
	}
	stored, removed, err := s.deps.Store.UpsertFolders(ctx, s.account.ID, disc.folders)
	if err != nil {
		return storageError(err)
	}
	if len(removed) > 0 {
		s.log.Info("folders removed on server", "count", len(removed))
	}
	s.inboxMailbox, s.inboxID = "", ""
	for _, f := range stored {
		if f.Role == api.RoleInbox {
			s.inboxMailbox, s.inboxID = f.Mailbox, f.ID
			break
		}
	}

	if err := s.pushOps(ctx, sess); err != nil {
		return err
	}
	// Sent copies go up before the folders are synchronised, so the Sent
	// folder's pass below fetches them back at once: its LIST-STATUS is
	// stale after the APPEND and it joins the targets whatever was requested.
	// The outbox pseudo-folder is local only and never part of stored.
	appended, err := s.appendSent(ctx, sess)
	if err != nil {
		return err
	}
	for mailbox := range appended {
		delete(disc.status, mailbox)
	}

	var targets []store.Folder
	for _, f := range stored {
		if !f.Selectable || (req.folder != "" && api.FolderID(f.ID) != req.folder && !appended[f.Mailbox]) {
			continue
		}
		targets = append(targets, f)
	}
	n := len(targets)
	for i, f := range targets {
		fresh, err := s.deps.Store.GetFolder(ctx, s.account.ID, f.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return storageError(err)
		}
		if !full && fresh.Role != api.RoleInbox && !fresh.LastSyncAt.IsZero() {
			st := disc.status[fresh.Mailbox]
			if st == nil {
				if st, err = folderStatus(ctx, sess, fresh.Mailbox); err != nil {
					if isStatusError(err) {
						s.log.Warn("folder status refused", "folder", fresh.ID, "err", transport.CleanMessage(err.Error()))
						continue
					}
					return err
				}
			}
			if !folderChanged(fresh, st) {
				continue
			}
		}
		base := float64(i) / float64(n)
		s.setState(func(st *api.SyncState) { st.FolderID, st.Progress = api.FolderID(fresh.ID), int(base*100) })
		err = s.syncFolder(ctx, sess, fresh, since, func(frac float64) {
			s.setProgress(int((base + frac/float64(n)) * 100))
		})
		if err != nil {
			if isStatusError(err) {
				s.log.Warn("folder skipped", "folder", fresh.ID, "err", transport.CleanMessage(err.Error()))
				continue
			}
			return err
		}
	}
	s.lastSince = since
	s.passes++
	return nil
}

// wait blocks until the next pass is due: a trigger or wake, a unilateral
// change while idling on INBOX (when the server offers IDLE), the polling
// ticker, or — without IDLE — a keep-alive NOOP every idleRecheck.
func (s *Syncer) wait(ctx context.Context, sess *session) (request, error) {
	if req, ok := s.takePending(); ok {
		return req, nil
	}
	var tick <-chan time.Time
	if iv := s.prefs().IntervalSeconds; iv > 0 {
		t := time.NewTicker(time.Duration(iv) * time.Second)
		defer t.Stop()
		tick = t.C
	}
	useIdle := sess.caps.Has(imap.CapIdle) && s.inboxMailbox != ""
	for {
		var idle *imapclient.IdleCommand
		if useIdle {
			if sess.selected != s.inboxMailbox {
				if _, err := sess.selectMailbox(ctx, s.inboxMailbox); err != nil {
					if isStatusError(err) {
						useIdle = false
						continue
					}
					return request{}, err
				}
			}
			if req, ok := s.takePending(); ok {
				return req, nil
			}
			err := sess.do(ctx, commandTimeout, func() error {
				var err error
				idle, err = sess.Idle()
				return err
			})
			if err != nil {
				if isStatusError(err) {
					useIdle = false
					continue
				}
				return request{}, err
			}
		}
		stopIdle := func() error {
			if idle == nil {
				return nil
			}
			return sess.do(ctx, commandTimeout, func() error {
				if err := idle.Close(); err != nil {
					return err
				}
				return idle.Wait()
			})
		}
		recheck := time.NewTimer(idleRecheck)
		select {
		case <-ctx.Done():
			recheck.Stop()
			return request{}, ctx.Err()
		case <-s.wake:
			recheck.Stop()
			if err := stopIdle(); err != nil {
				return request{}, err
			}
			req, _ := s.takePending()
			return req, nil
		case <-sess.events:
			recheck.Stop()
			if err := stopIdle(); err != nil {
				return request{}, err
			}
			return request{folder: api.FolderID(s.inboxID)}, nil
		case <-tick:
			recheck.Stop()
			if err := stopIdle(); err != nil {
				return request{}, err
			}
			return request{}, nil
		case <-sess.Closed():
			recheck.Stop()
			return request{}, api.NewError(api.CodeNetworkError, "connection closed")
		case <-recheck.C:
			if err := stopIdle(); err != nil {
				return request{}, err
			}
			err := sess.do(ctx, commandTimeout, func() error { return sess.Noop().Wait() })
			if err != nil && !isStatusError(err) {
				return request{}, err
			}
		}
	}
}

// fail degrades the state according to the error and reports credential
// problems once per transition.
func (s *Syncer) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	var ae *api.Error
	if !errors.As(err, &ae) {
		ae = api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
	}
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
	if status != api.SyncAuthRequired && ae.Code != api.CodeKeyringError {
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

// retryDelay is how long to wait after err: forever (until Wake) for
// credential problems, the exponential backoff otherwise.
func (s *Syncer) retryDelay(err error, attempt *int) time.Duration {
	switch errorCode(err) {
	case api.CodeAuthFailed, api.CodeAuthRequired:
		return 0
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
