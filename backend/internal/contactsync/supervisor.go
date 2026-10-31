// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactsync

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Cadence (docs/plans/address-book.md A4).
const (
	// Interval is the time between two passes of an account.
	Interval   = 15 * time.Minute
	backoffMin = 30 * time.Second
	backoffMax = time.Hour
)

// Status is the state of an account's syncer.
type Status string

const (
	StatusIdle            Status = "idle"
	StatusSyncing         Status = "syncing"
	StatusOffline         Status = "offline"
	StatusAuthRequired    Status = "authRequired"
	StatusConsentRequired Status = "consentRequired"
	StatusError           Status = "error"
)

// State is the state of an account's syncer.
type State struct {
	Status   Status
	Error    *api.Error // the last failure, nil after a good pass
	LastSync time.Time  // the end of the last good pass
}

// Options wire the engine to the daemon. Store and Providers are required.
type Options struct {
	Store *store.Store
	// Providers makes the source of an account; asked when the syncer
	// starts and again after an account-wide failure. An *api.Error
	// (authRequired, contactsConsentRequired, …) is the account's state
	// until the next attempt.
	Providers func(ctx context.Context, a store.Account) (Provider, error)
	// Changed is told which books of an account changed (contents, list,
	// visibility); core coalesces it into notify.contactsChanged. nil =
	// nobody.
	Changed func(accountID string, bookIDs []string)
	Log     *slog.Logger
	// Backoff overrides the retry delay after the attempt-th failure in a
	// row (nil = 30 s doubling to 1 h, ±10 % jitter).
	Backoff func(attempt int) time.Duration
	// Interval overrides the time between passes (0 = Interval).
	Interval time.Duration
}

// HasContacts reports whether the engine synchronises the account: an
// enabled graph account with contacts.sync, or an enabled caldav account
// with contacts.
func HasContacts(a store.Account) bool {
	return a.Enabled && ContactsOn(a.Config)
}

// ContactsOn reports whether a configuration has its contacts on.
func ContactsOn(c api.AccountConfig) bool {
	switch c.Protocol() {
	case api.AccountGraph:
		return c.Graph != nil && c.Graph.Contacts != nil && c.Graph.Contacts.Sync
	case api.AccountCalDAV:
		return c.CalDAV != nil && c.CalDAV.Contacts
	}
	return false
}

// Supervisor owns one syncer per account with contacts. Its methods are
// safe from any goroutine; syncers start once Run is called (Start before
// Run is queued).
type Supervisor struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	running bool
	stopped bool
	entries map[string]*entry
	queued  []store.Account
	wg      sync.WaitGroup
}

type entry struct {
	syncer *syncer
	cancel context.CancelFunc
	done   chan struct{}
}

// New prepares a supervisor.
func New(opts Options) *Supervisor {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Interval <= 0 {
		opts.Interval = Interval
	}
	return &Supervisor{opts: opts, log: opts.Log.With("component", "contactsync"), entries: map[string]*entry{}}
}

// Run serves until ctx is cancelled, then stops every syncer. A second,
// concurrent Run only waits for its ctx.
func (sv *Supervisor) Run(ctx context.Context) {
	sv.mu.Lock()
	if sv.running || sv.stopped {
		sv.mu.Unlock()
		<-ctx.Done()
		return
	}
	sv.running = true
	sv.ctx = ctx
	queued := sv.queued
	sv.queued = nil
	sv.mu.Unlock()
	for _, a := range queued {
		sv.Start(a)
	}
	<-ctx.Done()
	sv.mu.Lock()
	sv.stopped = true
	entries := sv.entries
	sv.entries = map[string]*entry{}
	sv.mu.Unlock()
	for _, e := range entries {
		e.cancel()
	}
	sv.wg.Wait()
}

// Start begins synchronising the account's address books; a no-op for an
// account without contacts and for a running one.
func (sv *Supervisor) Start(a store.Account) {
	if !HasContacts(a) {
		return
	}
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.stopped {
		return
	}
	if sv.ctx == nil {
		if !slices.ContainsFunc(sv.queued, func(q store.Account) bool { return q.ID == a.ID }) {
			sv.queued = append(sv.queued, a)
		}
		return
	}
	if _, ok := sv.entries[a.ID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(sv.ctx)
	s := newSyncer(sv, a)
	e := &entry{syncer: s, cancel: cancel, done: make(chan struct{})}
	sv.entries[a.ID] = e
	sv.wg.Add(1)
	go func() {
		defer sv.wg.Done()
		defer close(e.done)
		s.run(ctx)
	}()
}

// Stop ends the account's syncer and waits for it. The stored rows stay
// (removing them is core's).
func (sv *Supervisor) Stop(accountID string) {
	sv.mu.Lock()
	e, ok := sv.entries[accountID]
	delete(sv.entries, accountID)
	sv.queued = slices.DeleteFunc(sv.queued, func(q store.Account) bool { return q.ID == accountID })
	sv.mu.Unlock()
	if ok {
		e.cancel()
		<-e.done
	}
}

// Restart applies a changed configuration: Stop followed by Start.
func (sv *Supervisor) Restart(a store.Account) {
	sv.Stop(a.ID)
	sv.Start(a)
}

// SyncNow asks for a pass of the account ("" = every account) now; false
// when the account has no running syncer.
func (sv *Supervisor) SyncNow(accountID string) bool {
	if accountID == "" {
		for _, e := range sv.snapshot() {
			e.syncer.request()
		}
		return true
	}
	e, ok := sv.entry(accountID)
	if ok {
		e.syncer.request()
	}
	return ok
}

// SyncIfStale asks for a pass when the last good one ended longer than
// age ago (or there was none yet and none runs).
func (sv *Supervisor) SyncIfStale(accountID string, age time.Duration) {
	e, ok := sv.entry(accountID)
	if !ok {
		return
	}
	st := e.syncer.State()
	if st.Status == StatusSyncing {
		return
	}
	if st.LastSync.IsZero() || time.Since(st.LastSync) > age {
		e.syncer.request()
	}
}

// State returns the state of a running syncer.
func (sv *Supervisor) State(accountID string) (State, bool) {
	e, ok := sv.entry(accountID)
	if !ok {
		return State{}, false
	}
	return e.syncer.State(), true
}

// Provider returns the running syncer's provider; false before it has
// one.
func (sv *Supervisor) Provider(accountID string) (Provider, bool) {
	e, ok := sv.entry(accountID)
	if !ok {
		return nil, false
	}
	return e.syncer.provider()
}

// ProviderFor is the running syncer's provider, else a new one from
// Options.Providers (an account whose syncer is offline or not running).
func (sv *Supervisor) ProviderFor(ctx context.Context, a store.Account) (Provider, error) {
	if p, ok := sv.Provider(a.ID); ok {
		return p, nil
	}
	if sv.opts.Providers == nil {
		return nil, api.NewError(api.CodeUnavailable, "contactsync: no contacts source")
	}
	return sv.opts.Providers(ctx, a)
}

func (sv *Supervisor) entry(accountID string) (*entry, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	e, ok := sv.entries[accountID]
	return e, ok
}

func (sv *Supervisor) snapshot() []*entry {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	out := make([]*entry, 0, len(sv.entries))
	for _, e := range sv.entries {
		out = append(out, e)
	}
	return out
}

func (sv *Supervisor) changed(accountID string, bookIDs []string) {
	if len(bookIDs) > 0 && sv.opts.Changed != nil {
		sv.opts.Changed(accountID, bookIDs)
	}
}

// --- the syncer --------------------------------------------------------------

type syncer struct {
	sv      *Supervisor
	account store.Account
	log     *slog.Logger
	wake    chan struct{}

	mu    sync.Mutex
	state State
	prov  Provider
}

func newSyncer(sv *Supervisor, a store.Account) *syncer {
	return &syncer{sv: sv, account: a, log: sv.log.With("account", a.ID), wake: make(chan struct{}, 1),
		state: State{Status: StatusIdle}}
}

// State returns the live state.
func (s *syncer) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *syncer) provider() (Provider, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prov, s.prov != nil
}

func (s *syncer) request() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *syncer) run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		err := s.ensureProvider(ctx)
		if err == nil {
			err = s.pass(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.fail(err)
			s.sleep(ctx, backoffDelay(s.sv.opts.Backoff, attempt))
			attempt++
			continue
		}
		attempt = 0
		s.sleep(ctx, s.sv.opts.Interval)
	}
}

func (s *syncer) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-s.wake:
	case <-t.C:
	}
}

func backoffDelay(override func(int) time.Duration, attempt int) time.Duration {
	if override != nil {
		return override(attempt)
	}
	d := backoffMin
	for i := 0; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	d = min(d, backoffMax)
	return time.Duration(float64(d) * (1 + (rand.Float64()*2-1)*0.1))
}

func (s *syncer) ensureProvider(ctx context.Context) error {
	if _, ok := s.provider(); ok {
		return nil
	}
	if s.sv.opts.Providers == nil {
		return api.NewError(api.CodeUnavailable, "contactsync: no contacts source")
	}
	p, err := s.sv.opts.Providers(ctx, s.account)
	if err != nil {
		return err
	}
	if p == nil {
		return api.NewError(api.CodeUnavailable, "contactsync: no contacts source")
	}
	s.mu.Lock()
	s.prov = p
	s.mu.Unlock()
	return nil
}

// statusOf classifies an error.
func statusOf(err error) (Status, *api.Error) {
	var ae *api.Error
	if !errors.As(err, &ae) {
		ae = api.NewError(api.CodeStorageError, "contactsync: %s", cleanMessage(err.Error()))
	}
	switch ae.Code {
	case api.CodeOffline, api.CodeNetworkError, api.CodeServerTimeout, api.CodeTLSError:
		return StatusOffline, ae
	case api.CodeAuthFailed, api.CodeAuthRequired:
		return StatusAuthRequired, ae
	case api.CodeContactsConsentRequired:
		return StatusConsentRequired, ae
	}
	return StatusError, ae
}

func (s *syncer) fail(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	status, ae := statusOf(err)
	if ae.Code == api.CodeCancelled {
		return
	}
	s.log.Warn("contacts sync failed", "status", status, "code", ae.Code)
	s.mu.Lock()
	if status != StatusError {
		s.prov = nil // made again: a new token, a new consent
	}
	s.state.Status, s.state.Error = status, ae
	s.mu.Unlock()
}

func (s *syncer) setStatus(st Status) {
	s.mu.Lock()
	s.state.Status = st
	s.mu.Unlock()
}

// pass lists the books, stores the list (vanished books go), empties the
// hidden ones and synchronises the visible ones. An error of one book is
// recorded and the others go on; an account-wide error (network,
// sign-in, consent) ends the pass.
func (s *syncer) pass(ctx context.Context) error {
	st := s.sv.opts.Store
	prov, _ := s.provider()
	s.setStatus(StatusSyncing)
	remote, err := prov.AddressBooks(ctx)
	if err != nil {
		return err
	}
	before, err := st.ListAddressBooks(ctx, s.account.ID)
	if err != nil {
		return storageError(err)
	}
	var want []store.AddressBook
	seen := map[string]bool{}
	for _, r := range remote {
		if len(want) >= api.MaxAddressBooksPerAccount {
			break
		}
		if r.RemoteID == "" || len(r.RemoteID) > maxRemoteIDBytes || seen[r.RemoteID] {
			continue
		}
		seen[r.RemoteID] = true
		want = append(want, store.AddressBook{RemoteID: r.RemoteID, Name: calendar.CleanLine(r.Name, maxBookNameBytes),
			ReadOnly: r.ReadOnly, Position: len(want)})
	}
	stored, err := st.UpsertAddressBooks(ctx, s.account.ID, want)
	if err != nil {
		return storageError(err)
	}
	var changed []string
	keep := map[string]bool{}
	for _, b := range stored {
		keep[b.ID] = true
	}
	old := map[string]store.AddressBook{}
	for _, b := range before {
		old[b.ID] = b
		if !keep[b.ID] {
			if err := st.DeleteAddressBook(ctx, b.ID); err != nil {
				return storageError(err)
			}
			changed = append(changed, b.ID)
		}
	}
	for _, b := range stored {
		if o, ok := old[b.ID]; !ok || o.Name != b.Name || o.ReadOnly != b.ReadOnly || o.Position != b.Position {
			changed = append(changed, b.ID)
		}
	}
	version := -1
	if v, ok := prov.(Versioned); ok {
		version = v.DataVersion()
	}
	var bookErr error
	for _, b := range stored {
		if !b.Visible {
			// A hidden book is not synchronised at all: its cards go.
			if b.Count > 0 || b.SyncToken != "" || b.CTag != "" || b.DeltaLink != "" {
				if err := st.DeleteAddressBookContents(ctx, b.ID); err != nil {
					return storageError(err)
				}
				changed = append(changed, b.ID)
			}
			continue
		}
		did, err := s.syncBook(ctx, prov, b, version)
		if did {
			changed = append(changed, b.ID)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if status, _ := statusOf(err); status != StatusError {
				s.sv.changed(s.account.ID, dedupe(changed))
				return err
			}
			_, ae := statusOf(err)
			s.log.Warn("address book failed", "book", b.ID, "code", ae.Code)
			if bookErr == nil {
				bookErr = ae
			}
		}
	}
	s.sv.changed(s.account.ID, dedupe(changed))
	s.mu.Lock()
	defer s.mu.Unlock()
	if bookErr != nil {
		_, ae := statusOf(bookErr)
		s.state.Status, s.state.Error = StatusError, ae
		return nil
	}
	s.state = State{Status: StatusIdle, LastSync: time.Now()}
	return nil
}

func dedupe(ids []string) []string {
	slices.Sort(ids)
	return slices.Compact(ids)
}

// syncBook runs one Sync of a book and stores its state; did reports a
// change of its contacts.
func (s *syncer) syncBook(ctx context.Context, prov Provider, b store.AddressBook, version int) (did bool, err error) {
	st := s.sv.opts.Store
	if version >= 0 && b.Version != version {
		if b.Count > 0 || b.SyncToken != "" || b.DeltaLink != "" {
			if err := st.DeleteAddressBookContents(ctx, b.ID); err != nil {
				return false, storageError(err)
			}
			did = true
		}
		b.SyncToken, b.CTag, b.DeltaLink, b.Count = "", "", "", 0
	}
	known, err := st.ContactETags(ctx, b.ID)
	if err != nil {
		return did, storageError(err)
	}
	sk := &sink{ctx: ctx, st: st, log: s.log, book: b, seen: map[string]bool{}}
	res, err := prov.Sync(ctx, b, known, sk)
	did = did || sk.changed
	if err != nil {
		return did, err
	}
	if res.Full {
		for remote := range known {
			if !sk.seen[remote] {
				if _, err := st.RemoveContactByRemote(ctx, b.ID, remote); err != nil {
					return did, storageError(err)
				}
				did = true
			}
		}
	}
	v := b.Version
	if version >= 0 {
		v = version
	}
	if err := st.SetAddressBookSyncState(ctx, b.ID, store.AddressBookSyncState{
		SyncToken: res.Token, CTag: res.CTag, DeltaLink: res.DeltaLink, Version: v, LastSyncAt: time.Now(),
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		return did, storageError(err)
	}
	photos, err := backfillPhotos(ctx, st, s.log, b.ID)
	return did || photos, storageError(err)
}

// sink writes one Sync's changes; its errors are *api.Error.
type sink struct {
	ctx     context.Context
	st      *store.Store
	log     *slog.Logger
	book    store.AddressBook
	seen    map[string]bool
	changed bool
	count   int
}

func (k *sink) Upsert(c Card) error {
	if c.RemoteID == "" {
		return api.NewError(api.CodeServerError, "contactsync: a contact without a remote id")
	}
	k.seen[c.RemoteID] = true
	row, ok := Row(k.book.ID, c)
	if !ok {
		return nil // a card the store cannot keep is left out
	}
	if k.count >= api.MaxContactsPerBook {
		return nil
	}
	k.count++
	var id string
	if err := k.st.Paced(k.ctx, func() error {
		var err error
		id, err = k.st.UpsertContact(k.ctx, row)
		k.changed = k.changed || err == nil
		return err
	}); err != nil {
		return storageError(err)
	}
	_, err := storePhoto(k.ctx, k.st, k.log, id, c)
	return storageError(err)
}

func (k *sink) Remove(remoteID string) error {
	k.seen[remoteID] = true
	gone, err := k.st.RemoveContactByRemote(k.ctx, k.book.ID, remoteID)
	if err != nil {
		return storageError(err)
	}
	k.changed = k.changed || gone
	return nil
}

func (k *sink) Seen(remoteID string) {
	k.seen[remoteID] = true
	k.count++
}
