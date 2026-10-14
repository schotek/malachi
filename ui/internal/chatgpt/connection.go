// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ConnectionService coordinates one renewable grant under a native process lock.
type ConnectionService struct {
	store          CredentialStore
	browser        Browser
	protocol       protocol
	gate           chan struct{}
	mu             sync.Mutex
	connection     Connection
	session        context.Context
	cancelSession  context.CancelFunc
	pending        context.CancelFunc
	lifetime       context.Context
	cancelLifetime context.CancelFunc
	observers      map[int]func(Connection)
	nextObserver   int
}

func NewConnectionService(httpClient *http.Client, store CredentialStore, browser Browser) *ConnectionService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	// Never follow a redirect carrying OAuth fields or a bearer credential.
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	lifetime, cancel := context.WithCancel(context.Background())
	session, cancelSession := context.WithCancel(lifetime)
	return &ConnectionService{store: store, browser: browser, protocol: protocol{&client, time.Now}, gate: make(chan struct{}, 1), session: session, cancelSession: cancelSession, lifetime: lifetime, cancelLifetime: cancel, observers: make(map[int]func(Connection))}
}
func (s *ConnectionService) Connection() Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connection
}
func (s *ConnectionService) IsReady() bool { return s.Connection().Status == Connected }
func (s *ConnectionService) SessionContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// Observe registers safe presentation changes. The caller marshals its UI callback.
func (s *ConnectionService) Observe(fn func(Connection)) func() {
	s.mu.Lock()
	s.nextObserver++
	id := s.nextObserver
	s.observers[id] = fn
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.observers, id); s.mu.Unlock() }
}
func (s *ConnectionService) set(r *Registration, status Status) {
	c := Connection{Status: status}
	if r != nil {
		c.Email = r.Email
		c.ConnectionID = r.ConnectionID
		c.ClientID = r.ClientID
		c.Subject = r.Subject
	}
	s.mu.Lock()
	s.connection = c
	listeners := make([]func(Connection), 0, len(s.observers))
	for _, fn := range s.observers {
		listeners = append(listeners, fn)
	}
	s.mu.Unlock()
	for _, fn := range listeners {
		fn(c)
	}
}
func (s *ConnectionService) resetSession() {
	s.mu.Lock()
	s.cancelSession()
	s.session, s.cancelSession = context.WithCancel(s.lifetime)
	s.mu.Unlock()
}
func (s *ConnectionService) cancelWork() { s.mu.Lock(); s.cancelSession(); s.mu.Unlock() }
func (s *ConnectionService) lock(ctx context.Context) (func(), error) {
	select {
	case s.gate <- struct{}{}:
		return func() { <-s.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *ConnectionService) registration(ctx context.Context) (*Registration, error) {
	r, err := s.store.ReadRegistration(ctx)
	if err != nil {
		return nil, authError("storage")
	}
	if r == nil {
		r = &Registration{HostID: hostID()}
		if err = s.store.WriteRegistration(ctx, *r); err != nil {
			return nil, authError("storage")
		}
	}
	return r, nil
}
func (s *ConnectionService) Initialize(ctx context.Context) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	release, err := s.store.Acquire(ctx)
	if err != nil {
		return authError("storage")
	}
	defer release()
	r, err := s.registration(ctx)
	if err != nil {
		return err
	}
	t, err := s.store.ReadTokens(ctx)
	if err != nil {
		return authError("storage")
	}
	status := Disconnected
	if t != nil && t.ConnectionID == r.ConnectionID && r.ClientID != "" && r.Subject != "" && hasScope(t.Scope) {
		status = Connected
	}
	s.set(r, status)
	return nil
}
func (s *ConnectionService) CancelSignIn() {
	s.mu.Lock()
	cancel := s.pending
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *ConnectionService) Close() { s.cancelLifetime(); s.cancelWork(); s.CancelSignIn() }
func (s *ConnectionService) SignIn(ctx context.Context) error {
	attempt, cancel := context.WithTimeout(ctx, 5*time.Minute)
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	defer cancel()
	unlock, err := s.lock(attempt)
	if err != nil {
		return err
	}
	defer unlock()
	before := s.Connection()
	s.mu.Lock()
	s.pending = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pending = nil; s.mu.Unlock() }()
	s.cancelWork()
	err = s.signIn(attempt)
	if err != nil {
		if before.Status == Connected {
			s.resetSession()
		}
		s.set(&Registration{Email: before.Email, ConnectionID: before.ConnectionID, ClientID: before.ClientID, Subject: before.Subject}, before.Status)
	}
	return err
}
func (s *ConnectionService) signIn(ctx context.Context) error {
	release, err := s.store.Acquire(ctx)
	if err != nil {
		return authError("storage")
	}
	defer release()
	r, err := s.registration(ctx)
	if err != nil {
		return err
	}
	s.set(r, SigningIn)
	state, nonce, verifier := randomValue(), randomValue(), randomValue()
	redirect := ""
	callback, err := s.browser.Authorize(ctx, func(uri string) (string, error) {
		redirect = uri
		return authorizationURL(*r, uri, state, nonce, verifier)
	}, state)
	if err != nil {
		return err
	}
	fields, err := parseCallback(callback, redirect, state)
	if err != nil {
		return err
	}
	if fields.Has("error") {
		return authError("permission_denied")
	}
	client := r.ClientID
	if client == "" {
		client = fields.Get("client_id")
	}
	if client == "" || client == DynamicClient || len(client) > 512 || (r.ClientID != "" && fields.Has("client_id") && fields.Get("client_id") != r.ClientID) || fields.Get("code") == "" {
		return authError("invalid_response")
	}
	if r.ClientID == "" {
		r.ClientID = client
		if err = s.store.WriteRegistration(ctx, *r); err != nil {
			return authError("storage")
		}
	}
	result, err := s.protocol.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {fields.Get("code")}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {Resource}})
	if err != nil {
		return err
	}
	if result.IDToken == "" || result.RefreshToken == "" {
		return authError("invalid_response")
	}
	sub, email, err := s.protocol.validate(ctx, result.IDToken, client, nonce, r.Subject)
	if err != nil {
		return err
	}
	if !hasScope(result.Scope) {
		return authError("permission_denied")
	}
	if r.ConnectionID == "" {
		r.ConnectionID = randomValue()
	}
	r.Subject, r.Email = sub, email
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.store.WriteRegistration(ctx, *r); err != nil {
		return authError("storage")
	}
	if err = s.store.WriteTokens(ctx, Tokens{r.ConnectionID, result.AccessToken, result.RefreshToken, result.IDToken, result.Scope, s.protocol.now().Add(time.Duration(result.ExpiresIn) * time.Second)}); err != nil {
		return authError("storage")
	}
	s.resetSession()
	s.set(r, Connected)
	return nil
}
func (s *ConnectionService) GetAccessToken(ctx context.Context) (string, error) {
	linked, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.SessionContext(), cancel)
	defer stop()
	defer cancel()
	unlock, err := s.lock(linked)
	if err != nil {
		return "", err
	}
	defer unlock()
	release, err := s.store.Acquire(linked)
	if err != nil {
		return "", authError("storage")
	}
	defer release()
	r, err := s.registration(linked)
	if err != nil {
		return "", err
	}
	t, err := s.store.ReadTokens(linked)
	if err != nil {
		return "", authError("storage")
	}
	if t == nil || r.ClientID == "" || r.Subject == "" || r.ConnectionID != t.ConnectionID || !hasScope(t.Scope) {
		s.cancelWork()
		s.set(r, Disconnected)
		return "", authError("not_connected")
	}
	if t.ExpiresAt.After(s.protocol.now().Add(2 * time.Minute)) {
		if err = linked.Err(); err != nil {
			return "", err
		}
		return t.AccessToken, nil
	}
	result, err := s.protocol.exchange(linked, url.Values{"grant_type": {"refresh_token"}, "client_id": {r.ClientID}, "refresh_token": {t.RefreshToken}, "resource": {Resource}})
	if err == nil {
		scope := result.Scope
		if scope == "" {
			scope = t.Scope
		}
		if !hasScope(scope) {
			err = authError("permission_denied")
		}
		if err == nil && result.IDToken != "" {
			_, _, err = s.protocol.validate(linked, result.IDToken, r.ClientID, "", r.Subject)
		}
		if err == nil {
			t.AccessToken = result.AccessToken
			if result.RefreshToken != "" {
				t.RefreshToken = result.RefreshToken
			}
			if result.IDToken != "" {
				t.IDToken = result.IDToken
			}
			t.Scope = scope
			t.ExpiresAt = s.protocol.now().Add(time.Duration(result.ExpiresIn) * time.Second)
			if err = linked.Err(); err == nil {
				if s.store.WriteTokens(linked, *t) != nil {
					err = authError("storage")
				}
			}
			if err == nil {
				return t.AccessToken, nil
			}
		}
	}
	var auth *AuthError
	if errors.As(err, &auth) && (auth.Code == "reconnect_required" || auth.Code == "permission_denied" || auth.Code == "identity_mismatch" || auth.Code == "invalid_response") {
		if s.store.DeleteTokens(context.Background()) != nil {
			return "", authError("storage")
		}
		s.cancelWork()
		s.set(r, ReconnectRequired)
	}
	return "", err
}
func (s *ConnectionService) InferenceRejected(ctx context.Context, token string, status int) error {
	if status != 401 {
		return nil
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	release, err := s.store.Acquire(ctx)
	if err != nil {
		return authError("storage")
	}
	defer release()
	r, err := s.store.ReadRegistration(ctx)
	if err != nil {
		return authError("storage")
	}
	t, err := s.store.ReadTokens(ctx)
	if err != nil {
		return authError("storage")
	}
	if r == nil || t == nil || r.ConnectionID != t.ConnectionID || t.AccessToken != token {
		return nil
	}
	if s.store.DeleteTokens(ctx) != nil {
		return authError("storage")
	}
	s.cancelWork()
	s.set(r, ReconnectRequired)
	return nil
}

// Disconnect cancels immediately; cancellation cannot prevent local deletion.
func (s *ConnectionService) Disconnect(ctx context.Context) (bool, error) {
	s.cancelWork()
	s.CancelSignIn()
	unlock, err := s.lock(context.Background())
	if err != nil {
		return false, err
	}
	defer unlock()
	release, err := s.store.Acquire(context.Background())
	if err != nil {
		return false, authError("storage")
	}
	defer release()
	// A canceled sign-in may have restored its previous session while we waited.
	s.cancelWork()
	registration, registrationErr := s.registration(context.Background())
	tokens, tokensErr := s.store.ReadTokens(context.Background())
	// Deletion is the first mutation, independent of malformed metadata/secret
	// snapshots. Revocation uses only the in-memory copy while the lease is held.
	if err = s.store.DeleteTokens(context.Background()); err != nil {
		s.set(registration, ReconnectRequired)
		return false, authError("storage")
	}
	s.set(registration, Disconnected)
	if registrationErr != nil || tokensErr != nil {
		return false, authError("storage")
	}
	confirmed := tokens == nil
	if tokens != nil && registration.ClientID != "" && tokens.ConnectionID == registration.ConnectionID {
		deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		for i := 0; i < 3 && !confirmed; i++ {
			confirmed, _ = s.protocol.revoke(deadline, registration.ClientID, tokens.RefreshToken)
			if !confirmed {
				select {
				case <-time.After(time.Duration(i+1) * 250 * time.Millisecond):
				case <-deadline.Done():
					i = 3
				}
			}
		}
	}
	return confirmed, nil
}
