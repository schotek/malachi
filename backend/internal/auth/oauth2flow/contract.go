// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package oauth2flow is the backend's own OAuth 2.0 sign-in for providers
// that GNOME Online Accounts would otherwise serve: the authorization-code
// flow with PKCE, a one-shot redirect listener on 127.0.0.1, the code
// exchange, and a token source that refreshes access tokens with the
// refresh token kept in the keyring. The UI opens the authorisation URL;
// this package never launches a browser and holds no user-facing text
// (the browser page's texts come from the UI).
//
// This file freezes the exported surface other packages build against.
// Names and signatures are fixed; bodies live in the package's other files.
package oauth2flow

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Provider names an authorisation server the backend knows.
type Provider string

const (
	Google    Provider = api.OAuth2ProviderGoogle    // Gmail / Google Workspace, IMAP+SMTP with XOAUTH2
	Microsoft Provider = api.OAuth2ProviderOffice365 // Microsoft 365 / Outlook.com through Graph
)

// Client is an OAuth client registration. Secret is an installed-app
// secret (Google's Desktop clients may require it at the token endpoint);
// Tenant applies to Microsoft only ("" = "common").
type Client struct {
	ID, Secret, Tenant string
}

// Endpoints are a provider's authorisation and token URLs. Only tests
// replace them (with a fake server, through Options.Endpoints and
// TokenSourceOptions.Endpoints); the daemon always uses DefaultEndpoints,
// and Google's identity trusts the ID token because it came from that
// token endpoint over TLS.
type Endpoints struct {
	AuthURL, TokenURL string
}

// DefaultEndpoints returns the real endpoints of p for the tenant ("" =
// "common"; ignored for Google).
func DefaultEndpoints(p Provider, tenant string) Endpoints { return defaultEndpoints(p, tenant) }

// Registry resolves the OAuth client of an account: the account's own
// ClientID/TenantID, else the client configured for the provider
// (config.toml), else a built-in one (none today).
type Registry struct {
	Configured map[Provider]Client
}

// Resolve returns the client for provider p and the account's OAuth2
// block (nil allowed). No client id anywhere → api CodeOAuthClientMissing.
func (r *Registry) Resolve(p Provider, o *api.OAuth2Config) (Client, error) { return r.resolve(p, o) }

// Available reports whether a client id exists for p without an
// account-specific override.
func (r *Registry) Available(p Provider) bool { return r.available(p) }

// PageTexts are the browser page's texts, plain text from the UI.
type PageTexts struct {
	SuccessTitle, SuccessText, FailureTitle, FailureText string
}

// PageTextsFrom converts the API type (nil → empty texts) and cleans the
// texts (valid UTF-8, no control characters, at most 200 characters each).
func PageTextsFrom(p *api.OAuthBrowserPage) PageTexts { return pageTextsFrom(p) }

// Grant is what a completed sign-in obtained. Email is the verified
// mailbox ("" when the provider named none). ClientID and Tenant name the
// client registration the tokens were issued to (the session's Client;
// Tenant only for Microsoft), so a caller can refuse to store them for an
// account that resolves to another client.
type Grant struct {
	Provider     Provider
	ClientID     string
	Tenant       string
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	IDToken      string
	Email        string
}

// IdentityFunc names the mailbox a fresh grant belongs to. Google's is
// built in (the ID token's email); Microsoft's is injected by the caller
// (Graph /me).
type IdentityFunc func(ctx context.Context, g Grant) (email string, err error)

// StartRequest describes one sign-in.
type StartRequest struct {
	Provider  Provider
	Client    Client
	LoginHint string // the address, sent as login_hint
	// ExpectEmail, when set, must equal the verified identity (trimmed,
	// case-insensitively, as store.NormalizeAddress compares); a mismatch
	// fails the session with invalidArgument and data {"signedInAs": …}.
	ExpectEmail string
	Page        PageTexts
	// Key deduplicates sessions (an account id for a re-sign-in): Start
	// with a Key that has a pending session for the same provider, client
	// (id and tenant) and ExpectEmail returns that session, with the new
	// page texts unless Page is empty; a pending session of the Key that
	// differs in any of them is cancelled and a new one started. "" never
	// deduplicates.
	Key string
	// OnComplete, when set, runs once on success from the session's
	// goroutine, before waiters are released. An error fails the session
	// with it (an *api.Error keeps its code, anything else is
	// internalError) and the grant is dropped.
	OnComplete func(Grant) error
}

// Status is a session's state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusComplete  Status = "complete"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

// Outcome is a session's state as seen from outside. Err is set for
// failed, cancelled and expired sessions (cancelled, serverTimeout,
// authFailed, invalidArgument with signedInAs, network codes).
type Outcome struct {
	Status Status
	Err    *api.Error
	Email  string
}

// Options configure a Manager. Zero values take the defaults: HTTP
// NewHTTPClient(), Now time.Now, ListenHost "127.0.0.1", TTL 10 min,
// MaxSessions 8, Endpoints DefaultEndpoints.
type Options struct {
	HTTP        *http.Client
	Log         *slog.Logger
	Now         func() time.Time
	ListenHost  string
	TTL         time.Duration
	MaxSessions int
	Identity    map[Provider]IdentityFunc
	// Endpoints is test-only: it points the sessions at a fake provider.
	Endpoints func(p Provider, tenant string) Endpoints
}

// Manager runs sign-in sessions.
type Manager struct{ impl *manager }

func NewManager(o Options) *Manager { return &Manager{impl: newManager(o)} }

// Start opens the redirect listener and returns the session id, the URL
// the UI opens, and when the session expires. Too many sessions →
// unavailable.
func (m *Manager) Start(req StartRequest) (id, authURL string, expires time.Time, err error) {
	return m.impl.start(req)
}

// Lookup returns a session's outcome; false for an unknown id.
func (m *Manager) Lookup(id string) (Outcome, bool) { return m.impl.lookup(id) }

// Wait blocks until the session leaves pending, max elapses, or ctx ends;
// an unknown id is invalidArgument.
func (m *Manager) Wait(ctx context.Context, id string, max time.Duration) (Outcome, error) {
	return m.impl.wait(ctx, id, max)
}

// SetPage replaces a pending session's page texts.
func (m *Manager) SetPage(id string, p PageTexts) bool { return m.impl.setPage(id, p) }

// Cancel ends a pending session with cancelled, or discards a complete
// one (the session is forgotten and its grant dropped from memory); false
// when unknown, completing, or already failed, cancelled or expired.
func (m *Manager) Cancel(id string) bool { return m.impl.cancel(id) }

// Grant returns a complete session's grant without consuming it.
func (m *Manager) Grant(id string) (Grant, bool) { return m.impl.grant(id) }

// Consume returns a complete session's grant and forgets the session.
func (m *Manager) Consume(id string) (Grant, bool) { return m.impl.consume(id) }

// Pending returns the pending session started with key, if any.
func (m *Manager) Pending(key string) (id, authURL string, ok bool) { return m.impl.pending(key) }

// Close cancels every session and closes the listeners (daemon shutdown).
func (m *Manager) Close() { m.impl.close() }

// TokenSourceOptions configure the access-token source of one account.
type TokenSourceOptions struct {
	Provider  Provider
	Client    Client
	AccountID api.AccountID
	Keyring   auth.Keyring
	HTTP      *http.Client
	Log       *slog.Logger
	Now       func() time.Time
	Slack     time.Duration // default 60 s
	// Endpoints is test-only: it points the refreshes at a fake provider.
	Endpoints func(p Provider, tenant string) Endpoints
	// OnAuthRequired runs (once per loss) when the stored sign-in is
	// missing or the provider rejects the refresh token; never after
	// Retire.
	OnAuthRequired func()
}

// TokenSource implements auth.TokenSource for an account with source
// "daemon".
type TokenSource struct{ impl *tokenSource }

var _ auth.TokenSource = (*TokenSource)(nil)

func NewTokenSource(o TokenSourceOptions) *TokenSource { return &TokenSource{impl: newTokenSource(o)} }

// AccessToken returns a valid access token, refreshing (and persisting a
// rotated refresh token) when needed. A missing or rejected sign-in is
// authRequired.
func (t *TokenSource) AccessToken(ctx context.Context) (string, error) {
	return t.impl.accessToken(ctx)
}

// Invalidate drops the cached access token (a server refused it).
func (t *TokenSource) Invalidate() { t.impl.invalidate() }

// Seed stores g's refresh token in the keyring and caches its access
// token; a keyring failure is returned (keyringError) and nothing changes.
// A retired source stores nothing and answers conflict.
func (t *TokenSource) Seed(ctx context.Context, g Grant) error { return t.impl.seed(ctx, g) }

// Retire ends the source's keyring writes for good: a refresh in flight
// no longer stores its rotated refresh token, Seed refuses, and the
// OnAuthRequired hook stays quiet. It waits for a keyring write already
// under way, so once it returns the source writes nothing more. The owner
// calls it when it drops the source (the account changed or went).
func (t *TokenSource) Retire() { t.impl.retire() }

// NewHTTPClient is the hardened client for the token endpoints: TLS 1.2+
// with the system roots, 30 s, no redirects.
func NewHTTPClient() *http.Client { return newHTTPClient() }
