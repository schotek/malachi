// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Sign-in sessions. Each session owns a listener on a loopback port that
// exists for the one redirect of the browser: the first request to "/"
// with the session's state ends the session (exchange, identity, page).
// A request naming another Host (DNS rebinding) or carrying a wrong state
// is refused with 400 and changes nothing, other paths and methods are
// 404, and at most maxConns connections are served at once. The
// session's outcome stays readable until its TTL has run out since the
// start (an expired one a little longer, so a waiting UI still reads
// serverTimeout); a complete one until it is consumed or cancelled.

const (
	defaultTTL         = 10 * time.Minute
	defaultMaxSessions = 8
	defaultListenHost  = "127.0.0.1"

	exchangeTimeout = 30 * time.Second
	identityTimeout = 30 * time.Second
	shutdownTimeout = 2 * time.Second

	// expiredGrace keeps an expired session readable after its TTL.
	expiredGrace = time.Minute
	// unknownLifetime is assumed for an access token without expires_in.
	unknownLifetime = 5 * time.Minute
)

// writeTimeout bounds writing one response; a variable for tests.
var writeTimeout = 10 * time.Second

type manager struct {
	http      *http.Client
	log       *slog.Logger
	now       func() time.Time
	host      string
	ttl       time.Duration
	max       int
	identity  map[Provider]IdentityFunc
	endpoints func(Provider, string) Endpoints

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
	// wg counts serve goroutines, callbacks in progress and graceful
	// shutdowns; Add happens under mu while !closed.
	wg sync.WaitGroup
}

type session struct {
	id, key    string
	provider   Provider
	clientID   string
	tenant     string
	cfg        *oauth2.Config
	verifier   string
	state      string
	expect     string
	onComplete func(Grant) error
	authURL    string
	host       string // the redirect's host:port, the only Host accepted
	expires    time.Time
	srv        *http.Server
	ln         net.Listener
	timer      *time.Timer
	done       chan struct{} // closed when the session leaves pending

	// Guarded by manager.mu.
	page       PageTexts
	status     Status
	err        *api.Error
	email      string
	grant      Grant
	handling   bool // a callback with the right state is being processed
	completing bool // success decided, OnComplete running
	cancelWork context.CancelFunc
	serverDown bool
}

func newManager(o Options) *manager {
	m := &manager{
		http:      o.HTTP,
		log:       o.Log,
		now:       o.Now,
		host:      o.ListenHost,
		ttl:       o.TTL,
		max:       o.MaxSessions,
		identity:  map[Provider]IdentityFunc{},
		endpoints: o.Endpoints,
		sessions:  map[string]*session{},
	}
	if m.http == nil {
		m.http = newHTTPClient()
	}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	m.log = m.log.With("component", "oauth2flow")
	if m.now == nil {
		m.now = time.Now
	}
	if m.host == "" {
		m.host = defaultListenHost
	}
	if m.ttl <= 0 {
		m.ttl = defaultTTL
	}
	if m.max <= 0 {
		m.max = defaultMaxSessions
	}
	if m.endpoints == nil {
		m.endpoints = defaultEndpoints
	}
	for p, f := range o.Identity {
		if f != nil {
			m.identity[p] = f
		}
	}
	return m
}

func (m *manager) start(req StartRequest) (string, string, time.Time, error) {
	if !knownProvider(req.Provider) {
		return "", "", time.Time{}, api.NewError(api.CodeInvalidArgument, "unknown OAuth2 provider %q", string(req.Provider))
	}
	if req.Client.ID == "" {
		return "", "", time.Time{}, api.NewError(api.CodeOAuthClientMissing, "no OAuth client id for provider %q", string(req.Provider))
	}
	page := cleanTexts(req.Page)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", "", time.Time{}, api.NewError(api.CodeUnavailable, "sign-in sessions are shutting down")
	}
	now := m.now()
	m.sweepLocked(now)
	if req.Key != "" {
		if s := m.pendingByKeyLocked(req.Key); s != nil {
			if s.provider == req.Provider && s.clientID == req.Client.ID && s.tenant == req.Client.Tenant &&
				normalizeAddress(s.expect) == normalizeAddress(req.ExpectEmail) {
				if page != (PageTexts{}) {
					s.page = page
				}
				m.log.Debug("sign-in session reused", "session", s.id, "key", req.Key)
				return s.id, s.authURL, s.expires, nil
			}
			// Started for another provider, client or address (the account
			// changed since): it must not complete for the new one.
			m.finishLocked(s, StatusCancelled, api.NewError(api.CodeCancelled, "sign-in replaced: the account changed"), "")
			m.stopLocked(s)
			m.log.Info("sign-in session replaced", "session", s.id, "key", req.Key)
		}
	}
	if m.pendingCountLocked() >= m.max {
		return "", "", time.Time{}, api.NewError(api.CodeUnavailable, "too many sign-in sessions under way (at most %d)", m.max)
	}

	id, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, err
	}
	state, err := randomB64(32)
	if err != nil {
		return "", "", time.Time{}, err
	}
	ep := m.endpoints(req.Provider, req.Client.Tenant)
	if ep.AuthURL == "" || ep.TokenURL == "" {
		return "", "", time.Time{}, api.NewError(api.CodeInvalidArgument, "no endpoints for OAuth2 provider %q", string(req.Provider))
	}

	ln, err := m.listen()
	if err != nil {
		return "", "", time.Time{}, err
	}
	redirect := redirectURL(ln.Addr())
	ru, err := url.Parse(redirect)
	if err != nil || ru.Host == "" {
		ln.Close()
		return "", "", time.Time{}, api.NewError(api.CodeInternalError, "unusable redirect address %q", redirect)
	}
	cfg := oauthConfig(req.Provider, req.Client, ep, redirect)
	verifier := oauth2.GenerateVerifier()
	opts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, authParams(req.Provider, req.LoginHint)...)

	s := &session{
		id:         "s_" + id,
		key:        req.Key,
		provider:   req.Provider,
		clientID:   req.Client.ID,
		tenant:     req.Client.Tenant,
		cfg:        cfg,
		verifier:   verifier,
		state:      state,
		expect:     strings.TrimSpace(req.ExpectEmail),
		onComplete: req.OnComplete,
		authURL:    cfg.AuthCodeURL(state, opts...),
		host:       ru.Host,
		expires:    now.Add(m.ttl),
		ln:         ln,
		done:       make(chan struct{}),
		page:       page,
		status:     StatusPending,
	}
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.serve(s, w, r) }),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(m.log.Handler(), slog.LevelDebug),
	}
	s.srv.SetKeepAlivesEnabled(false)
	m.sessions[s.id] = s

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			m.log.Debug("redirect listener stopped", "session", s.id, "err", err)
		}
	}()
	s.timer = time.AfterFunc(m.ttl, func() { m.onTimer(s) })

	m.log.Info("sign-in session started", "session", s.id, "provider", string(s.provider), "key", s.key,
		"listener", ln.Addr().String())
	return s.id, s.authURL, s.expires, nil
}

// listen opens the redirect listener on the loopback host and refuses
// anything that is not loopback. At most maxConns connections are served
// at once.
func (m *manager) listen() (net.Listener, error) {
	ln, err := net.Listen("tcp4", net.JoinHostPort(m.host, "0"))
	if err != nil {
		return nil, api.NewError(api.CodeUnavailable, "cannot open the redirect listener: %s", transport.CleanMessage(err.Error()))
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); !ok || !ta.IP.IsLoopback() {
		ln.Close()
		return nil, api.NewError(api.CodeUnavailable, "redirect listener is not on a loopback address")
	}
	return newLimitListener(ln, maxConns), nil
}

// --- the redirect ------------------------------------------------------------

func (m *manager) serve(s *session, w http.ResponseWriter, r *http.Request) {
	// The browser names the redirect's literal host:port. Anything else
	// (a rebound DNS name, "localhost", a missing Host) did not come from
	// the provider's redirect and is refused before anything is looked at.
	if r.Host != s.host {
		m.log.Debug("redirect request for another host refused", "session", s.id)
		writePage(w, http.StatusBadRequest, pageNeutral, PageTexts{}, "")
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		writePage(w, http.StatusNotFound, pageNeutral, PageTexts{}, "")
		return
	}
	q := r.URL.Query()

	m.mu.Lock()
	if m.closed || s.status != StatusPending || s.handling {
		m.mu.Unlock()
		writePage(w, http.StatusBadRequest, pageNeutral, PageTexts{}, "")
		return
	}
	if !stateMatches(q["state"], s.state) {
		// Refused without effect: a stray or forged request must not be
		// able to end the session the browser is about to complete.
		m.mu.Unlock()
		m.log.Debug("redirect with a wrong state refused", "session", s.id)
		writePage(w, http.StatusBadRequest, pageNeutral, PageTexts{}, "")
		return
	}
	s.handling = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelWork = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer cancel()

	// One callback only: no further connections.
	_ = s.ln.Close()

	kind, detail := m.callback(ctx, s, q)

	m.mu.Lock()
	texts := s.page
	m.mu.Unlock()
	status := http.StatusOK
	if kind != pageSuccess {
		status = http.StatusBadRequest
	}
	// The server's write deadline started with the request; the exchange,
	// the identity and OnComplete may have used it up.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
	writePage(w, status, kind, texts, detail)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	m.shutdownLater(s)
}

// stateMatches: exactly one state parameter, equal in constant time.
func stateMatches(got []string, want string) bool {
	if len(got) != 1 || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got[0]), []byte(want)) == 1
}

// callback ends the session from the redirect's query and returns the
// page to show and, on failure, a technical error name.
func (m *manager) callback(ctx context.Context, s *session, q url.Values) (pageKind, string) {
	if e := q.Get("error"); e != "" {
		name := errorName(e)
		if name == "" {
			name = "error"
		}
		if e == "access_denied" {
			m.finish(s, StatusCancelled, api.NewError(api.CodeCancelled, "access denied in the browser"), "")
			return pageFailure, name
		}
		msg := "provider returned " + name
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		m.finish(s, StatusFailed, api.NewError(api.CodeAuthFailed, "%s", cleanMsg(msg)), "")
		return pageFailure, name
	}
	codes := q["code"]
	if len(codes) != 1 || codes[0] == "" {
		m.finish(s, StatusFailed, api.NewError(api.CodeAuthFailed, "redirect without an authorization code"), "")
		return pageFailure, api.CodeAuthFailed.String()
	}

	g, aerr, name := m.exchange(ctx, s, codes[0])
	if aerr != nil {
		m.finish(s, statusFor(aerr), aerr, "")
		return pageFailure, name
	}
	email, aerr := m.identify(ctx, s, g)
	if aerr != nil {
		m.finish(s, statusFor(aerr), aerr, "")
		return pageFailure, aerr.Code.String()
	}
	g.Email = email
	if s.expect != "" {
		switch {
		case email == "":
			aerr = api.NewError(api.CodeAuthFailed, "the provider did not name the signed-in mailbox")
		case normalizeAddress(email) != normalizeAddress(s.expect):
			aerr = &api.Error{
				Code:    api.CodeInvalidArgument,
				Message: "signed in to a different mailbox",
				Data:    map[string]string{"signedInAs": email},
			}
			m.log.Debug("sign-in identity mismatch", "session", s.id, "expected", s.expect, "signedInAs", email)
		}
		if aerr != nil {
			m.finish(s, StatusFailed, aerr, "")
			return pageFailure, aerr.Code.String()
		}
	}
	return m.complete(s, g)
}

// complete runs OnComplete and releases the waiters, unless the session
// was cancelled or expired while the exchange ran. An OnComplete error
// fails the session instead and the grant is dropped.
func (m *manager) complete(s *session, g Grant) (pageKind, string) {
	m.mu.Lock()
	if s.status != StatusPending {
		name := ""
		if s.err != nil {
			name = s.err.Code.String()
		}
		m.mu.Unlock()
		return pageFailure, name
	}
	s.completing = true
	m.mu.Unlock()

	var herr error
	if s.onComplete != nil {
		herr = s.onComplete(g)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s.completing = false
	if herr != nil {
		aerr := hookError(herr, g)
		m.finishLocked(s, statusFor(aerr), aerr, "")
		return pageFailure, aerr.Code.String()
	}
	s.grant = g
	m.finishLocked(s, StatusComplete, nil, g.Email)
	return pageSuccess, ""
}

// hookError is an OnComplete error as the session's outcome: an
// *api.Error keeps its code and data, anything else is internalError; the
// message is cleaned with the grant's tokens redacted.
func hookError(err error, g Grant) *api.Error {
	secrets := []string{g.AccessToken, g.RefreshToken, g.IDToken}
	var aerr *api.Error
	if errors.As(err, &aerr) {
		return &api.Error{Code: aerr.Code, Message: cleanMsg(aerr.Message, secrets...), Data: aerr.Data}
	}
	return api.NewError(api.CodeInternalError, "%s", cleanMsg(err.Error(), secrets...))
}

func statusFor(e *api.Error) Status {
	if e.Code == api.CodeCancelled {
		return StatusCancelled
	}
	return StatusFailed
}

// exchange trades the code for tokens. The error name is the provider's
// error code when it sent one, else the contract code's name.
func (m *manager) exchange(ctx context.Context, s *session, code string) (Grant, *api.Error, string) {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	tok, err := s.cfg.Exchange(withHTTP(ctx, m.http), code, oauth2.VerifierOption(s.verifier))
	if err != nil {
		aerr := classifyToken(ctx, phaseExchange, err, code, s.verifier, s.cfg.ClientSecret)
		name := aerr.Code.String()
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && errorName(re.ErrorCode) != "" {
			name = re.ErrorCode
		}
		m.log.Info("code exchange failed", "session", s.id, "code", aerr.Code.String())
		return Grant{}, aerr, name
	}
	g := grantFrom(tok, s.provider, m.now())
	g.ClientID, g.Tenant = s.clientID, s.tenant
	if g.RefreshToken == "" {
		return Grant{}, api.NewError(api.CodeAuthFailed, "the provider returned no refresh token"), api.CodeAuthFailed.String()
	}
	return g, nil, ""
}

// identify names the mailbox of a fresh grant: the injected function for
// the provider, else Google's built-in one, else none ("").
func (m *manager) identify(ctx context.Context, s *session, g Grant) (string, *api.Error) {
	fn := m.identity[s.provider]
	if fn == nil && s.provider == Google {
		fn = googleIdentity(s.clientID, m.now)
	}
	if fn == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	email, err := fn(ctx, g)
	if err != nil {
		secrets := []string{g.AccessToken, g.RefreshToken, g.IDToken}
		var aerr *api.Error
		if errors.As(err, &aerr) {
			return "", &api.Error{Code: aerr.Code, Message: cleanMsg(aerr.Message, secrets...), Data: aerr.Data}
		}
		if errors.Is(err, errIDToken) || s.provider == Google && !isNetErr(err) {
			return "", api.NewError(api.CodeAuthFailed, "%s", cleanMsg("identity: "+err.Error(), secrets...))
		}
		c := transport.Classify(ctx, transport.StageCommand, err)
		if c.Code == api.CodeCancelled {
			return "", c
		}
		return "", api.NewError(c.Code, "%s", cleanMsg("identity: "+err.Error(), secrets...))
	}
	return cleanEmail(email), nil
}

func isNetErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// grantFrom converts a token response; the expiry comes from expires_in
// against the injected clock.
func grantFrom(tok *oauth2.Token, p Provider, now time.Time) Grant {
	g := Grant{
		Provider:     p,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       expiryOf(tok, now),
	}
	if id, ok := tok.Extra("id_token").(string); ok {
		g.IDToken = id
	}
	return g
}

func expiryOf(tok *oauth2.Token, now time.Time) time.Time {
	switch {
	case tok.ExpiresIn > 0:
		return now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	case !tok.Expiry.IsZero():
		return tok.Expiry
	}
	return now.Add(unknownLifetime)
}

// shutdownLater closes the server once the callback's response is out.
// Called from the handler, so it cannot wait here.
func (m *manager) shutdownLater(s *session) {
	m.mu.Lock()
	if s.serverDown {
		m.mu.Unlock()
		return
	}
	s.serverDown = true
	if m.closed {
		m.mu.Unlock()
		_ = s.srv.Close()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			_ = s.srv.Close()
		}
	}()
}

// --- state -------------------------------------------------------------------

func (m *manager) finish(s *session, st Status, e *api.Error, email string) {
	m.mu.Lock()
	m.finishLocked(s, st, e, email)
	m.mu.Unlock()
}

// finishLocked moves a pending session to st and releases its waiters;
// false when it had already left pending.
func (m *manager) finishLocked(s *session, st Status, e *api.Error, email string) bool {
	if s.status != StatusPending {
		return false
	}
	s.status, s.err, s.email = st, e, email
	close(s.done)
	code, detail := "", ""
	if e != nil {
		// e.Message is cleaned and redacted where it was made (cleanMsg):
		// the provider's or Graph's error code and description, no token.
		code, detail = e.Code.String(), e.Message
	}
	if detail != "" {
		m.log.Warn("sign-in session finished", "session", s.id, "provider", string(s.provider), "key", s.key,
			"status", string(st), "code", code, "err", detail)
	} else {
		m.log.Info("sign-in session finished", "session", s.id, "provider", string(s.provider), "key", s.key,
			"status", string(st), "code", code)
	}
	if email != "" {
		m.log.Debug("sign-in identity", "session", s.id, "email", email)
	}
	return true
}

// stopLocked ends a session's listener after it left pending from outside
// the redirect: a callback in progress is cancelled and shuts the server
// down itself; otherwise the server closes now.
func (m *manager) stopLocked(s *session) {
	if s.cancelWork != nil {
		s.cancelWork()
	}
	if s.handling || s.serverDown {
		return
	}
	s.serverDown = true
	_ = s.srv.Close()
}

// onTimer fires at the TTL: a pending session expires (and is kept
// expiredGrace longer), a finished one is forgotten.
func (m *manager) onTimer(s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.sessions[s.id] != s {
		return
	}
	if s.status == StatusPending {
		if s.completing {
			s.timer.Reset(time.Second)
			return
		}
		m.expireLocked(s)
		s.timer.Reset(expiredGrace)
		return
	}
	m.forgetLocked(s)
}

func (m *manager) expireLocked(s *session) {
	m.finishLocked(s, StatusExpired, api.NewError(api.CodeServerTimeout, "the sign-in session expired"), "")
	m.stopLocked(s)
}

func (m *manager) forgetLocked(s *session) {
	delete(m.sessions, s.id)
	s.grant = Grant{}
	if s.timer != nil {
		s.timer.Stop()
	}
}

// sweepLocked applies the clock lazily (the timer runs on the monotonic
// clock, which may stand still in suspend): overdue pending sessions
// expire, finished ones past their retention are forgotten.
func (m *manager) sweepLocked(now time.Time) {
	for _, s := range m.sessions {
		switch {
		case s.status == StatusPending:
			if !s.completing && !now.Before(s.expires) {
				m.expireLocked(s)
			}
		case s.status == StatusExpired:
			if !now.Before(s.expires.Add(expiredGrace)) {
				m.forgetLocked(s)
			}
		default:
			if !now.Before(s.expires) {
				m.forgetLocked(s)
			}
		}
	}
}

// pendingByKeyLocked is the pending session started with key. One whose
// OnComplete is running counts as finished: its listener is closed and its
// URL leads nowhere.
func (m *manager) pendingByKeyLocked(key string) *session {
	for _, s := range m.sessions {
		if s.key == key && s.status == StatusPending && !s.completing {
			return s
		}
	}
	return nil
}

func (m *manager) pendingCountLocked() int {
	n := 0
	for _, s := range m.sessions {
		if s.status == StatusPending {
			n++
		}
	}
	return n
}

func outcomeLocked(s *session) Outcome {
	o := Outcome{Status: s.status, Email: s.email}
	if s.err != nil {
		e := *s.err
		o.Err = &e
	}
	return o
}

// --- the Manager methods -----------------------------------------------------

func (m *manager) lookup(id string) (Outcome, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(m.now())
	s := m.sessions[id]
	if s == nil {
		return Outcome{}, false
	}
	return outcomeLocked(s), true
}

func (m *manager) wait(ctx context.Context, id string, max time.Duration) (Outcome, error) {
	m.mu.Lock()
	m.sweepLocked(m.now())
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return Outcome{}, api.NewError(api.CodeInvalidArgument, "unknown sign-in session")
	}
	out := outcomeLocked(s)
	m.mu.Unlock()
	if out.Status != StatusPending || max <= 0 {
		return out, nil
	}
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case <-s.done:
	case <-t.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	out = outcomeLocked(s)
	m.mu.Unlock()
	return out, nil
}

func (m *manager) setPage(id string, p PageTexts) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.status != StatusPending {
		return false
	}
	s.page = cleanTexts(p)
	return true
}

// cancel ends a pending session, or forgets a complete one together with
// its grant (the UI gave up on it: its tokens leave memory now rather
// than at the TTL).
func (m *manager) cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil || s.completing:
		return false
	case s.status == StatusComplete:
		m.forgetLocked(s)
		m.log.Info("sign-in session discarded", "session", s.id)
		return true
	case s.status != StatusPending:
		return false
	}
	m.finishLocked(s, StatusCancelled, api.NewError(api.CodeCancelled, "sign-in cancelled"), "")
	m.stopLocked(s)
	return true
}

func (m *manager) grant(id string) (Grant, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(m.now())
	s := m.sessions[id]
	if s == nil || s.status != StatusComplete {
		return Grant{}, false
	}
	return s.grant, true
}

func (m *manager) consume(id string) (Grant, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(m.now())
	s := m.sessions[id]
	if s == nil || s.status != StatusComplete {
		return Grant{}, false
	}
	g := s.grant
	m.forgetLocked(s)
	m.log.Debug("sign-in session consumed", "session", s.id)
	return g, true
}

func (m *manager) pending(key string) (string, string, bool) {
	if key == "" {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(m.now())
	s := m.pendingByKeyLocked(key)
	if s == nil {
		return "", "", false
	}
	return s.id, s.authURL, true
}

func (m *manager) close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for _, s := range m.sessions {
		if s.status == StatusPending && !s.completing {
			m.finishLocked(s, StatusCancelled, api.NewError(api.CodeCancelled, "the backend is shutting down"), "")
		}
		if s.cancelWork != nil && !s.completing {
			s.cancelWork()
		}
		s.serverDown = true
		_ = s.srv.Close()
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	m.mu.Unlock()

	m.wg.Wait()

	m.mu.Lock()
	for _, s := range m.sessions {
		s.grant = Grant{}
	}
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	m.log.Debug("sign-in sessions closed")
}

// --- helpers -----------------------------------------------------------------

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth2flow: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth2flow: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
