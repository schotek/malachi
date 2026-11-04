// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The access-token source of one "daemon" account. The refresh token
// lives in the keyring and, once read, in memory; access tokens only in
// memory. One refresh runs at a time and every caller waiting for it gets
// its result. Once the provider rejected the sign-in (or none is stored)
// the source answers authRequired without touching the network or the
// keyring until Seed brings a new one. A retired source (its account
// changed or went) never writes the keyring again.

const (
	defaultSlack = 60 * time.Second
	// refreshTimeout bounds one refresh including the keyring read.
	refreshTimeout = 45 * time.Second
)

type tokenSource struct {
	provider  Provider
	client    Client
	account   api.AccountID
	keyring   auth.Keyring
	http      *http.Client
	log       *slog.Logger
	now       func() time.Time
	slack     time.Duration
	endpoints func(Provider, string) Endpoints
	onAuth    func()

	// storeMu orders keyring writes: a rotated token of a refresh that
	// started before a Seed must not overwrite the seeded one, and Retire
	// waits for a write under way.
	storeMu sync.Mutex

	mu        sync.Mutex
	access    string
	expiry    time.Time
	refresh   string // "" = not read yet (or dropped)
	scope     string // the recorded scope (KeyGrantedScope); "" = mail scopes only
	scopeRead bool   // scope is known (read from the keyring or seeded)
	needsAuth bool
	gen       uint64 // bumped by seed: a refresh started before is stale
	retired   bool   // set under storeMu and mu: no keyring writes, no hook
	flight    *flight
}

// flight is one refresh in progress.
type flight struct {
	done  chan struct{}
	token string
	err   error
}

func newTokenSource(o TokenSourceOptions) *tokenSource {
	t := &tokenSource{
		provider:  o.Provider,
		client:    o.Client,
		account:   o.AccountID,
		keyring:   o.Keyring,
		http:      o.HTTP,
		log:       o.Log,
		now:       o.Now,
		slack:     o.Slack,
		endpoints: o.Endpoints,
		onAuth:    o.OnAuthRequired,
	}
	if t.keyring == nil {
		t.keyring = auth.UnavailableKeyring{}
	}
	if t.http == nil {
		t.http = newHTTPClient()
	}
	if t.log == nil {
		t.log = slog.New(slog.DiscardHandler)
	}
	t.log = t.log.With("component", "oauth2flow", "account", string(t.account), "provider", string(t.provider))
	if t.now == nil {
		t.now = time.Now
	}
	if t.slack <= 0 {
		t.slack = defaultSlack
	}
	if t.endpoints == nil {
		t.endpoints = defaultEndpoints
	}
	return t
}

func (t *tokenSource) accessToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.access != "" && t.now().Add(t.slack).Before(t.expiry) {
		tok := t.access
		t.mu.Unlock()
		return tok, nil
	}
	if t.needsAuth {
		t.mu.Unlock()
		return "", errAuthRequired()
	}
	f := t.flight
	if f == nil {
		f = &flight{done: make(chan struct{})}
		t.flight = f
		gen := t.gen
		// The refresh outlives a caller that gives up: the others may still
		// want it. It is bounded by refreshTimeout.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		go func() {
			defer cancel()
			t.run(rctx, f, gen)
		}()
	}
	t.mu.Unlock()

	select {
	case <-f.done:
		return f.token, f.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", api.NewError(api.CodeServerTimeout, "timed out waiting for the access token")
		}
		return "", api.NewError(api.CodeCancelled, "cancelled")
	}
}

func errAuthRequired() *api.Error {
	return api.NewError(api.CodeAuthRequired, "sign in again: no usable refresh token")
}

// run performs one refresh and publishes its result.
func (t *tokenSource) run(ctx context.Context, f *flight, gen uint64) {
	tok, expiry, rotated, scope, lost, err := t.refreshOnce(ctx, gen)

	notify := false
	t.mu.Lock()
	switch {
	case t.gen != gen:
		// Seeded meanwhile: the result belongs to the old sign-in and is
		// not cached. A token the new sign-in brought wins; a failure of
		// the old one is not the new one's (the caller retries).
		if t.access != "" && t.now().Add(t.slack).Before(t.expiry) {
			tok, err = t.access, nil
		} else if err != nil {
			err = api.NewError(api.CodeServerError, "sign-in replaced during the refresh; retry")
		}
	case err != nil:
		if lost && !t.needsAuth {
			t.needsAuth = true
			t.refresh, t.scope, t.scopeRead = "", "", false
			t.access, t.expiry = "", time.Time{}
			notify = !t.retired
		}
	default:
		t.access, t.expiry = tok, expiry
		if rotated != "" {
			t.refresh = rotated
		}
		if scope != "" {
			t.scope, t.scopeRead = scope, true
		}
	}
	f.token, f.err = tok, err
	if t.flight == f {
		t.flight = nil
	}
	close(f.done)
	t.mu.Unlock()

	if err != nil {
		code := api.CodeInternalError
		var aerr *api.Error
		if errors.As(err, &aerr) {
			code = aerr.Code
		}
		t.log.Warn("access token refresh failed", "code", code.String())
	} else {
		t.log.Debug("access token refreshed")
	}
	if notify && t.onAuth != nil {
		t.onAuth()
	}
}

// loadStored returns the refresh token, reading the keyring on first
// use. lost reports that no sign-in is stored.
func (t *tokenSource) loadStored(ctx context.Context, gen uint64) (rt string, lost bool, err error) {
	t.mu.Lock()
	rt = t.refresh
	t.mu.Unlock()
	if rt != "" {
		return rt, false, nil
	}
	v, kerr := t.keyring.Get(ctx, t.account, auth.KeyRefreshToken)
	switch {
	case errors.Is(kerr, auth.ErrNoSecret):
		return "", true, errAuthRequired()
	case kerr != nil:
		return "", false, keyringReadErr(kerr, "read the refresh token")
	case v == "":
		return "", true, errAuthRequired()
	}
	t.mu.Lock()
	if t.gen == gen && t.refresh == "" {
		t.refresh = v
	}
	t.mu.Unlock()
	return v, false, nil
}

// loadScope returns the recorded scope (KeyGrantedScope), reading the
// keyring on first use; a missing item is "" (mail scopes only).
func (t *tokenSource) loadScope(ctx context.Context, gen uint64) (string, error) {
	t.mu.Lock()
	if t.scopeRead {
		s := t.scope
		t.mu.Unlock()
		return s, nil
	}
	t.mu.Unlock()
	v, kerr := t.keyring.Get(ctx, t.account, KeyGrantedScope)
	switch {
	case errors.Is(kerr, auth.ErrNoSecret):
		v = ""
	case kerr != nil:
		return "", keyringReadErr(kerr, "read the granted scope")
	}
	v = cleanScope(v)
	t.mu.Lock()
	if t.gen == gen && !t.scopeRead {
		t.scope, t.scopeRead = v, true
	}
	t.mu.Unlock()
	return v, nil
}

func keyringReadErr(err error, what string) *api.Error {
	var aerr *api.Error
	if errors.As(err, &aerr) {
		return aerr
	}
	return api.NewError(api.CodeKeyringError, "%s: %s", what, cleanMsg(err.Error()))
}

// refreshOnce reads the refresh token (lazily), asks the token endpoint
// and persists a rotated refresh token (bare, in its own item). When the
// response names a scope that changes whether the calendar or the
// contacts are granted,
// KeyGrantedScope is written or removed to match. scope is the response's
// scope ("" when it named none: the recorded one stands). lost reports
// that the sign-in is gone (nothing stored, or the provider rejected it).
func (t *tokenSource) refreshOnce(ctx context.Context, gen uint64) (access string, expiry time.Time, rotated, scope string, lost bool, err error) {
	rt, lost, err := t.loadStored(ctx, gen)
	if err != nil {
		return "", time.Time{}, "", "", lost, err
	}

	ep := t.endpoints(t.provider, t.client.Tenant)
	// The scopes of the config are never sent on a refresh
	// (golang.org/x/oauth2): a refresh keeps the grant as it is.
	cfg := oauthConfig(t.provider, t.client, ep, "", false, false)
	tok, rerr := cfg.TokenSource(withHTTP(ctx, t.http), &oauth2.Token{RefreshToken: rt}).Token()
	if rerr != nil {
		aerr := classifyToken(ctx, phaseRefresh, rerr, rt, t.client.Secret)
		return "", time.Time{}, "", "", aerr.Code == api.CodeAuthRequired, aerr
	}
	if tok.AccessToken == "" {
		return "", time.Time{}, "", "", false, api.NewError(api.CodeServerError, "token endpoint returned no access token")
	}
	expiry = expiryOf(tok, t.now())
	scope = responseScope(tok)

	if tok.RefreshToken != "" && tok.RefreshToken != rt {
		rotated = tok.RefreshToken
		t.storeRotated(ctx, gen, rotated)
	}
	if scope != "" {
		if stored, serr := t.loadScope(ctx, gen); serr == nil && (HasCalendarScope(stored) != HasCalendarScope(scope) ||
			HasContactsScope(stored) != HasContactsScope(scope)) {
			t.storeScope(ctx, gen, scope)
		}
	}
	return tok.AccessToken, expiry, rotated, scope, false, nil
}

// grantedScope is the scope recorded with the stored sign-in.
func (t *tokenSource) grantedScope(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.needsAuth {
		t.mu.Unlock()
		return "", errAuthRequired()
	}
	gen := t.gen
	t.mu.Unlock()
	if _, _, err := t.loadStored(ctx, gen); err != nil {
		return "", err
	}
	return t.loadScope(ctx, gen)
}

// writeScope writes KeyGrantedScope for a grant with calendar or contacts
// access and removes it otherwise; the caller holds storeMu.
func (t *tokenSource) writeScope(ctx context.Context, scope string) error {
	if recordsScope(scope) {
		return t.keyring.Set(ctx, t.account, KeyGrantedScope, scope)
	}
	if err := t.keyring.Delete(ctx, t.account, KeyGrantedScope); err != nil && !errors.Is(err, auth.ErrNoSecret) {
		return err
	}
	return nil
}

// storeScope records a refresh's scope unless the sign-in was replaced or
// the source retired since the refresh began.
func (t *tokenSource) storeScope(ctx context.Context, gen uint64, scope string) {
	t.storeMu.Lock()
	defer t.storeMu.Unlock()
	t.mu.Lock()
	stale := t.gen != gen || t.retired
	t.mu.Unlock()
	if stale {
		return
	}
	if err := t.writeScope(ctx, scope); err != nil {
		t.log.Warn("could not record the granted scope", "code", keyringReadErr(err, "scope").Code.String())
	}
}

// storeRotated persists a rotated refresh token unless a Seed replaced the sign-in since the refresh began or
// the source was retired.
func (t *tokenSource) storeRotated(ctx context.Context, gen uint64, rotated string) {
	t.storeMu.Lock()
	defer t.storeMu.Unlock()
	t.mu.Lock()
	stale := t.gen != gen || t.retired
	t.mu.Unlock()
	if stale {
		t.log.Debug("rotated refresh token of a replaced sign-in not stored")
		return
	}
	if err := t.keyring.Set(ctx, t.account, auth.KeyRefreshToken, rotated); err != nil {
		code := api.CodeKeyringError
		var aerr *api.Error
		if errors.As(err, &aerr) {
			code = aerr.Code
		}
		// The new token stays in memory; the stored one may still be
		// accepted after a restart (providers usually allow the previous
		// one for a while).
		t.log.Warn("could not store the rotated refresh token", "code", code.String())
	}
}

func (t *tokenSource) invalidate() {
	t.mu.Lock()
	t.access, t.expiry = "", time.Time{}
	t.mu.Unlock()
}

// retire stops every keyring write of the source. Taking storeMu waits
// for a write under way (storeRotated, seed) to finish first.
func (t *tokenSource) retire() {
	t.storeMu.Lock()
	defer t.storeMu.Unlock()
	t.mu.Lock()
	t.retired = true
	t.mu.Unlock()
	t.log.Debug("token source retired")
}

func (t *tokenSource) seed(ctx context.Context, g Grant) error {
	if g.RefreshToken == "" {
		return api.NewError(api.CodeInvalidArgument, "grant without a refresh token")
	}
	t.storeMu.Lock()
	defer t.storeMu.Unlock()
	t.mu.Lock()
	retired := t.retired
	t.mu.Unlock()
	if retired {
		return api.NewError(api.CodeConflict, "the account's sign-in changed; the grant was not stored")
	}
	if err := t.keyring.Set(ctx, t.account, auth.KeyRefreshToken, g.RefreshToken); err != nil {
		msg := err.Error()
		var aerr *api.Error
		if errors.As(err, &aerr) {
			msg = aerr.Message
		}
		t.log.Warn("could not store the refresh token")
		return api.NewError(api.CodeKeyringError, "store the refresh token: %s", cleanMsg(msg, g.RefreshToken, g.AccessToken))
	}
	scope := cleanScope(g.Scope)
	if err := t.writeScope(ctx, scope); err != nil {
		// The sign-in works for mail; the calendar asks for consent again
		// after a restart (the recorded scope is what the keyring holds).
		t.log.Warn("could not record the granted scope", "code", keyringReadErr(err, "scope").Code.String())
	}
	t.mu.Lock()
	t.gen++
	t.refresh, t.scope, t.scopeRead = g.RefreshToken, scope, true
	t.needsAuth = false
	if g.AccessToken != "" && !g.Expiry.IsZero() {
		t.access, t.expiry = g.AccessToken, g.Expiry
	} else {
		t.access, t.expiry = "", time.Time{}
	}
	t.mu.Unlock()
	t.log.Info("sign-in stored")
	return nil
}
