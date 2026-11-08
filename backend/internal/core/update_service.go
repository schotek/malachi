// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/update"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Update serves the update.* methods (docs/api.md §4.20): the daemon asks
// the signed manifest of versions (docs/services.md §2) once a day and
// offers the newest version for the package the build was installed
// from. It downloads and installs nothing (docs/plans/update-check.md).
func (b *Backend) Update() api.UpdateService { return &updateService{b} }

type updateService struct{ b *Backend }

var _ api.UpdateService = (*updateService)(nil)

// What the check keeps in meta: the last accepted envelope with its
// issuedAt and ETag and the outcome of the last check (update.state), and
// the user's choices (update.prefs).
const (
	metaUpdateState = "update.state"
	metaUpdatePrefs = "update.prefs"
)

// Schedule of the checks (U7): the first one 5–15 minutes after the start
// but not before a day (± 2 hours) after the last one, then every day
// ± 2 hours; a server's Retry-After pushes the next one later.
const (
	updateFirstMin   = 5 * time.Minute
	updateFirstMax   = 15 * time.Minute
	updateInterval   = 24 * time.Hour
	updateJitter     = 2 * time.Hour
	updateCheckLimit = 70 * time.Second // both requests of one check
	maxSeenNotices   = 32
	maxPrompted      = 8
)

type storedUpdate struct {
	Envelope  string     `json:"envelope,omitempty"`
	IssuedAt  time.Time  `json:"issuedAt,omitzero"`
	ETag      string     `json:"etag,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Failure   string     `json:"failure,omitempty"`
	AttemptAt *time.Time `json:"attemptAt,omitempty"`
	NotBefore *time.Time `json:"notBefore,omitempty"` // Retry-After
}

type updateLater struct {
	Version string    `json:"version"`
	Until   time.Time `json:"until"`
}

type updatePrefs struct {
	Enabled     *bool        `json:"enabled,omitempty"` // nil = true (Q1)
	Skipped     string       `json:"skipped,omitempty"`
	Later       *updateLater `json:"later,omitempty"`
	Prompted    []string     `json:"prompted,omitempty"`
	NoticesSeen []string     `json:"noticesSeen,omitempty"`
}

func (p updatePrefs) enabled() bool { return p.Enabled == nil || *p.Enabled }

// updater is the in-memory side of the check.
type updater struct {
	mu       sync.Mutex
	loaded   bool
	state    storedUpdate
	prefs    updatePrefs
	manifest *update.Manifest
	checking bool
	// checkMu serialises checks; manual is when a user last asked.
	checkMu sync.Mutex
	manual  time.Time
	wake    chan struct{}
	// remind tells the clients when a "later" ends; u.mu.
	remind *time.Timer
}

// UpdateSettings are what the daemon's build and tests give the check.
type UpdateSettings struct {
	// Channel is the package the build was installed from
	// (update.Channels); "" turns the check off.
	Channel string
	// Client asks the server; nil = update.NewClient.
	Client *update.Client
	// Keys verify the envelope; nil = update.Keys.
	Keys map[string]ed25519.PublicKey
	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

func (b *Backend) updNow() time.Time {
	if b.Updates.Now != nil {
		return b.Updates.Now()
	}
	return time.Now()
}

func (b *Backend) updKeys() map[string]ed25519.PublicKey {
	if b.Updates.Keys != nil {
		return b.Updates.Keys
	}
	return update.Keys
}

// updSupported: a known channel and a plain version.
func (b *Backend) updSupported() (update.Version, bool) {
	v, ok := update.ParseVersion(b.Version)
	return v, ok && update.KnownChannel(b.Updates.Channel)
}

// updLoad reads meta once; u.mu held.
func (b *Backend) updLoad(ctx context.Context) {
	u := &b.upd
	if u.loaded {
		return
	}
	u.loaded = true
	if raw, ok, err := b.store.GetMeta(ctx, metaUpdateState); err != nil {
		b.log.Warn("update: read state", "err", err)
	} else if ok && json.Unmarshal([]byte(raw), &u.state) != nil {
		b.log.Warn("update: unreadable state, starting over")
		u.state = storedUpdate{}
	}
	if raw, ok, err := b.store.GetMeta(ctx, metaUpdatePrefs); err != nil {
		b.log.Warn("update: read preferences", "err", err)
	} else if ok && json.Unmarshal([]byte(raw), &u.prefs) != nil {
		b.log.Warn("update: unreadable preferences, using the defaults")
		u.prefs = updatePrefs{}
	}
	if l := u.prefs.Later; l != nil && l.Until.After(b.updNow()) {
		b.updRemindAt(l.Until)
	}
	if u.state.Envelope != "" {
		// Verified again: a key removed since then no longer counts.
		m, err := acceptEnvelope([]byte(u.state.Envelope), b.updKeys(), u.state.IssuedAt, time.Time{})
		if err != nil {
			b.log.Warn("update: stored manifest no longer verifies", "class", update.FailureOf(err))
			u.state.Envelope, u.state.ETag, u.state.IssuedAt = "", "", time.Time{}
		} else {
			u.manifest = m
		}
	}
}

func acceptEnvelope(env []byte, keys map[string]ed25519.PublicKey, now, last time.Time) (*update.Manifest, error) {
	payload, err := update.Verify(env, keys)
	if err != nil {
		return nil, err
	}
	m, _, err := update.Parse(payload, now, last)
	return m, err
}

func (b *Backend) updSaveState(ctx context.Context) {
	raw, _ := json.Marshal(b.upd.state)
	if err := b.store.SetMeta(ctx, metaUpdateState, string(raw)); err != nil {
		b.log.Warn("update: write state", "err", err)
	}
}

func (b *Backend) updSavePrefs(ctx context.Context) error {
	raw, _ := json.Marshal(b.upd.prefs)
	if err := b.store.SetMeta(ctx, metaUpdatePrefs, string(raw)); err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	return nil
}

func (b *Backend) updateChanged() {
	if n, ok := b.getNotifier().(api.UpdateNotifier); ok {
		n.UpdateChanged(api.UpdateChangedNotification{})
	}
}

func (s *updateService) Status(ctx context.Context, p api.UpdateStatusParams) (*api.UpdateStatus, error) {
	return s.b.updStatus(ctx, p.Locale), nil
}

func (s *updateService) Check(ctx context.Context, p api.UpdateCheckParams) (*api.UpdateStatus, error) {
	b := s.b
	b.upd.mu.Lock()
	b.updLoad(ctx)
	enabled := b.upd.prefs.enabled()
	// Asking by hand ends a "later": the user wants to see what there is
	// now. A skipped version stays skipped.
	putOff := b.upd.prefs.Later != nil
	if putOff {
		b.upd.prefs.Later = nil
		if b.upd.remind != nil {
			b.upd.remind.Stop()
		}
		if err := b.updSavePrefs(ctx); err != nil {
			b.log.Warn("update: write preferences", "err", err)
		}
	}
	b.upd.mu.Unlock()
	if _, ok := b.updSupported(); ok && enabled {
		b.updCheck(ctx, true)
	} else if putOff {
		b.updateChanged()
	}
	return b.updStatus(ctx, p.Locale), nil
}

func (s *updateService) SetPrefs(ctx context.Context, p api.UpdateSetPrefsParams) (*api.UpdateStatus, error) {
	b := s.b
	b.upd.mu.Lock()
	b.updLoad(ctx)
	changed := p.Enabled != nil && *p.Enabled != b.upd.prefs.enabled()
	if changed {
		if *p.Enabled {
			b.upd.prefs.Enabled = nil
		} else {
			off := false
			b.upd.prefs.Enabled = &off
		}
		if err := b.updSavePrefs(ctx); err != nil {
			b.upd.mu.Unlock()
			return nil, err
		}
	}
	b.upd.mu.Unlock()
	if changed {
		b.updWake()
		b.updateChanged()
	}
	return b.updStatus(ctx, p.Locale), nil
}

func (s *updateService) Dismiss(ctx context.Context, p api.UpdateDismissParams) (*api.UpdateStatus, error) {
	b := s.b
	cur, ok := b.updSupported()
	b.upd.mu.Lock()
	b.updLoad(ctx)
	var offer *update.Offer
	if ok {
		offer = update.Select(b.upd.manifest, b.Updates.Channel, cur)
	}
	if offer == nil || offer.Release.Version.String() != p.Version {
		b.upd.mu.Unlock()
		return nil, api.NewError(api.CodeInvalidArgument, "version %q is not offered", p.Version)
	}
	prefs := &b.upd.prefs
	switch p.Action {
	case api.UpdateSkip, api.UpdateLater:
		if offer.Security {
			b.upd.mu.Unlock()
			return nil, api.NewError(api.CodeInvalidArgument, "a security release cannot be put off")
		}
		if p.Action == api.UpdateSkip {
			prefs.Skipped, prefs.Later = p.Version, nil
		} else {
			prefs.Later = &updateLater{Version: p.Version, Until: b.updNow().Add(api.UpdateRemindDelay).UTC()}
			b.updRemindAt(prefs.Later.Until)
		}
	case api.UpdatePrompted:
		if !slices.Contains(prefs.Prompted, p.Version) {
			prefs.Prompted = lastN(append(prefs.Prompted, p.Version), maxPrompted)
		}
	default:
		b.upd.mu.Unlock()
		return nil, api.NewError(api.CodeInvalidArgument, "unknown action %q", p.Action)
	}
	err := b.updSavePrefs(ctx)
	b.upd.mu.Unlock()
	if err != nil {
		return nil, err
	}
	b.updateChanged()
	return b.updStatus(ctx, p.Locale), nil
}

func (s *updateService) DismissNotice(ctx context.Context, p api.UpdateDismissNoticeParams) (*api.UpdateStatus, error) {
	b := s.b
	b.upd.mu.Lock()
	b.updLoad(ctx)
	m := b.upd.manifest
	if m == nil || m.Notice == nil || m.Notice.ID != p.ID {
		b.upd.mu.Unlock()
		return nil, api.NewError(api.CodeInvalidArgument, "notice %q is not shown", p.ID)
	}
	prefs := &b.upd.prefs
	changed := !slices.Contains(prefs.NoticesSeen, p.ID)
	if changed {
		prefs.NoticesSeen = lastN(append(prefs.NoticesSeen, p.ID), maxSeenNotices)
		if err := b.updSavePrefs(ctx); err != nil {
			b.upd.mu.Unlock()
			return nil, err
		}
	}
	b.upd.mu.Unlock()
	if changed {
		b.updateChanged()
	}
	return b.updStatus(ctx, p.Locale), nil
}

func lastN(s []string, n int) []string {
	if len(s) > n {
		return slices.Clone(s[len(s)-n:])
	}
	return s
}

// updStatus computes what a client shows.
func (b *Backend) updStatus(ctx context.Context, locale string) *api.UpdateStatus {
	u := &b.upd
	u.mu.Lock()
	defer u.mu.Unlock()
	b.updLoad(ctx)
	st := &api.UpdateStatus{Enabled: u.prefs.enabled(), Current: b.Version}
	if update.KnownChannel(b.Updates.Channel) {
		st.Channel = b.Updates.Channel
	}
	cur, supported := b.updSupported()
	switch {
	case !st.Enabled:
		st.State = api.UpdateDisabled
		return st
	case !supported:
		st.State = api.UpdateUnsupported
		return st
	}
	st.CheckedAt = u.state.CheckedAt
	st.Failure = api.UpdateFailure(u.state.Failure)
	now := b.updNow()
	if o := update.Select(u.manifest, b.Updates.Channel, cur); o != nil {
		v := o.Release.Version.String()
		info := &api.UpdateInfo{
			Version:     v,
			PublishedAt: o.Release.PublishedAt,
			Security:    o.Security,
			Notes:       update.Text(o.Release.Notes, locale),
			InfoURL:     o.Release.InfoURL,
			URL:         o.Package.URL,
			Size:        o.Package.Size,
		}
		if o.Security {
			info.Prompt = !slices.Contains(u.prefs.Prompted, v)
		} else {
			info.Skipped = u.prefs.Skipped == v
			if l := u.prefs.Later; l != nil && l.Version == v && l.Until.After(now) {
				t := l.Until
				info.RemindAfter = &t
			}
		}
		st.Available = info
	}
	if n := update.NoticeFor(u.manifest, cur); n != nil && !slices.Contains(u.prefs.NoticesSeen, n.ID) {
		st.Notice = &api.UpdateNotice{ID: n.ID, Text: update.Text(n.Text, locale), URL: n.URL}
	}
	switch {
	case u.checking:
		st.State = api.UpdateChecking
	case st.Available != nil:
		st.State = api.UpdateAvailable
	case st.Failure != "":
		st.State = api.UpdateFailed
	case st.CheckedAt != nil:
		st.State = api.UpdateUpToDate
	default:
		st.State = api.UpdateIdle
	}
	return st
}

// updCheck asks the API, then the GitHub asset, and keeps what verifies.
// A manual check within api.UpdateCheckInterval of the last one does
// nothing.
func (b *Backend) updCheck(ctx context.Context, manual bool) {
	u := &b.upd
	u.checkMu.Lock()
	defer u.checkMu.Unlock()
	now := b.updNow()
	if manual {
		if !u.manual.IsZero() && now.Sub(u.manual) < api.UpdateCheckInterval {
			return
		}
		u.manual = now
	}
	client := b.Updates.Client
	if client == nil {
		client = update.NewClient(b.Version)
		b.Updates.Client = client
	}
	u.mu.Lock()
	b.updLoad(ctx)
	etag, last := "", u.state.IssuedAt
	if u.manifest != nil {
		etag = u.state.ETag
	}
	u.checking = true
	u.mu.Unlock()
	b.updateChanged()

	ctx, cancel := context.WithTimeout(ctx, updateCheckLimit)
	defer cancel()
	res := b.updFetch(ctx, client, etag, last, now)

	u.mu.Lock()
	u.checking = false
	at := b.updNow().UTC()
	u.state.AttemptAt = &at
	u.state.NotBefore = nil
	if res.fail == "" {
		u.state.CheckedAt, u.state.Failure = &at, ""
		if res.manifest != nil {
			u.manifest = res.manifest
			u.state.Envelope, u.state.IssuedAt, u.state.ETag = string(res.envelope), res.manifest.IssuedAt, res.etag
		}
	} else {
		u.state.Failure = string(res.fail)
		if res.retryAfter > 0 {
			nb := at.Add(res.retryAfter)
			u.state.NotBefore = &nb
		}
	}
	b.updSaveState(context.WithoutCancel(ctx))
	u.mu.Unlock()
	if res.fail != "" {
		b.log.Info("update check failed", "class", res.fail)
	} else {
		b.log.Debug("update check done", "new", res.manifest != nil)
	}
	b.updateChanged()
}

type updResult struct {
	manifest   *update.Manifest // nil: unchanged
	envelope   []byte
	etag       string
	fail       update.Failure
	retryAfter time.Duration
}

func (b *Backend) updFetch(ctx context.Context, c *update.Client, etag string, last, now time.Time) updResult {
	keys := b.updKeys()
	accept := func(r update.Response, err error) updResult {
		var fe *update.FetchError
		switch {
		case errors.As(err, &fe):
			return updResult{fail: fe.Class, retryAfter: fe.RetryAfter}
		case err != nil:
			return updResult{fail: update.FailNetwork}
		case r.NotModified:
			return updResult{}
		}
		m, err := acceptEnvelope(r.Envelope, keys, now, last)
		var ue *update.Error
		switch {
		case errors.As(err, &ue) && ue.Newer:
			return updResult{} // a later version reads it; keep what we have
		case err != nil:
			b.log.Info("update manifest refused", "err", err) // rule and path, never a value
			return updResult{fail: update.FailureOf(err)}
		}
		return updResult{manifest: m, envelope: r.Envelope, etag: r.ETag}
	}
	first := accept(c.FetchPrimary(ctx, etag))
	if first.fail == "" {
		return first
	}
	second := accept(c.FetchFallback(ctx))
	if second.fail == "" {
		second.etag = ""
		return second
	}
	// A signature failure on either host says more than the other one.
	if second.fail == update.FailSignature {
		first.fail = second.fail
	}
	return first
}

// updRemindAt sends notify.updateChanged when a "later" ends, so that the
// clients show the banner again without a clock of their own; u.mu held.
func (b *Backend) updRemindAt(until time.Time) {
	u := &b.upd
	if u.remind != nil {
		u.remind.Stop()
	}
	u.remind = time.AfterFunc(max(until.Sub(b.updNow()), 0)+time.Second, b.updateChanged)
}

// stopUpdates stops the reminder timer (Backend.Close).
func (b *Backend) stopUpdates() {
	b.upd.mu.Lock()
	defer b.upd.mu.Unlock()
	if b.upd.remind != nil {
		b.upd.remind.Stop()
	}
}

// updWake makes the scheduler look at the preferences again.
func (b *Backend) updWake() {
	select {
	case b.upd.wake <- struct{}{}:
	default:
	}
}

// startUpdates runs the scheduler until ctx ends.
func (b *Backend) startUpdates(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	b.upd.wake = make(chan struct{}, 1)
	if _, ok := b.updSupported(); !ok {
		if b.Updates.Channel != "" {
			b.log.Info("update check off", "reason", "version not MAJOR.MINOR.PATCH or unknown channel")
		}
		close(done)
		return done
	}
	started := b.updNow()
	go func() {
		defer close(done)
		for {
			b.upd.mu.Lock()
			b.updLoad(ctx)
			enabled := b.upd.prefs.enabled()
			due := nextUpdateCheck(started, b.upd.state, rand.Float64(), rand.Float64())
			b.upd.mu.Unlock()
			var (
				t     *time.Timer
				timer <-chan time.Time // nil while disabled: only a wake or the end
			)
			if enabled {
				t = time.NewTimer(max(due.Sub(b.updNow()), 0))
				timer = t.C
			}
			fire := false
			select {
			case <-ctx.Done():
			case <-b.upd.wake:
			case <-timer:
				fire = true
			}
			if t != nil {
				t.Stop()
			}
			if ctx.Err() != nil {
				return
			}
			if fire {
				b.updCheck(ctx, false)
			}
		}
	}()
	return done
}

// nextUpdateCheck is when the next scheduled check is due: a day ± 2 h
// after the last attempt, but never sooner than 5–15 minutes after the
// start nor before a server's Retry-After. r1 and r2 are in [0, 1).
func nextUpdateCheck(started time.Time, st storedUpdate, r1, r2 float64) time.Time {
	due := started.Add(updateFirstMin + time.Duration(r1*float64(updateFirstMax-updateFirstMin)))
	if st.AttemptAt != nil {
		next := st.AttemptAt.Add(updateInterval - updateJitter + time.Duration(r2*float64(2*updateJitter)))
		if next.After(due) {
			due = next
		}
	}
	if st.NotBefore != nil && st.NotBefore.After(due) {
		due = *st.NotBefore
	}
	return due
}
