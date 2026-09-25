// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The backend's own OAuth2 sign-in (source "daemon"): account.oauthStart /
// oauthWait / oauthCancel, the token source of every such account, and the
// re-sign-in the backend opens by itself when an account's refresh token is
// missing or refused. oauth2flow runs the sessions and talks to the
// provider; this file ties them to accounts. Tokens never leave the
// backend: a session's grant goes to the keyring through the account's
// token source (account.add / account.update), access tokens stay in
// memory.

// oauthWaitSlice is how long one account.oauthWait blocks (docs/api.md
// §4.1); the UI calls again on pending.
const oauthWaitSlice = 60 * time.Second

// oauthHookTimeout bounds the store and keyring work of the backend's own
// session hooks, which run outside any request.
const oauthHookTimeout = 30 * time.Second

// oauthSession is what the backend remembers about a sign-in session it
// started: the config of a new account (account.oauthStart {config}) or
// the account a re-sign-in is for. The session's grant names its own
// provider, client and verified address.
type oauthSession struct {
	config    *api.AccountConfig // new account; nil for a re-sign-in
	accountID string             // re-sign-in; "" for a new account
}

// oauthClientsFrom turns config.toml's [oauth2.*] tables into the client
// registry's configured clients. Empty ids stay: the registry treats them
// as missing, and a Microsoft tenant applies only with the configured id.
// Microsoft's registration is a public client and has no secret.
func oauthClientsFrom(cfg config.Config) map[oauth2flow.Provider]oauth2flow.Client {
	g, m := cfg.OAuth2.Google, cfg.OAuth2.Microsoft
	return map[oauth2flow.Provider]oauth2flow.Client{
		oauth2flow.Google:    {ID: g.ClientID, Secret: g.ClientSecret},
		oauth2flow.Microsoft: {ID: m.ClientID, Tenant: m.Tenant},
	}
}

// daemonOAuth returns the OAuth2 block of an account that signs in
// through the backend's own flow: an IMAP account whose oauth2 source is
// "daemon", or a Graph account whose graph and oauth2 sources are.
func daemonOAuth(c api.AccountConfig) (*api.OAuth2Config, bool) {
	o := c.OAuth2
	if o == nil || o.Source != api.OAuth2SourceDaemon {
		return nil, false
	}
	switch c.Protocol() {
	case api.AccountIMAP:
		return o, true
	case api.AccountGraph:
		return o, c.Graph != nil && c.Graph.Source == api.GraphSourceDaemon
	}
	return nil, false
}

// resolveClient names the provider of a daemon OAuth2 block and resolves
// its client: oauthClientMissing without any client id.
func (b *Backend) resolveClient(o *api.OAuth2Config) (oauth2flow.Provider, oauth2flow.Client, error) {
	p := oauth2flow.Provider(o.Provider)
	c, err := b.OAuthClients.Resolve(p, o)
	return p, c, err
}

// sameTenant compares Microsoft tenants: "" is "common", case does not
// matter (a Google client has none on either side).
func sameTenant(a, b string) bool {
	norm := func(s string) string {
		if s = strings.TrimSpace(s); s == "" {
			return "common"
		}
		return s
	}
	return strings.EqualFold(norm(a), norm(b))
}

// grantFits checks that a completed sign-in may be stored for an account
// with configuration cfg: the account signs in through the backend with
// the grant's provider, the grant's verified mailbox is the account's
// address, and the tokens were issued to the client the account resolves
// to now. A grant naming no mailbox never fits. Every failure is
// invalidArgument, except a client that no longer resolves.
func (b *Backend) grantFits(g oauth2flow.Grant, cfg api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	o, ok := daemonOAuth(cfg)
	if !ok {
		return bad("the account does not sign in through the backend (source daemon)")
	}
	if string(g.Provider) != o.Provider {
		return bad("the sign-in is for another provider")
	}
	if g.Email == "" {
		return bad("the sign-in named no mailbox")
	}
	if store.NormalizeAddress(g.Email) != store.NormalizeAddress(cfg.Email) {
		return bad("the sign-in is for another address")
	}
	_, client, err := b.resolveClient(o)
	if err != nil {
		return err
	}
	if g.ClientID != client.ID || !sameTenant(g.Tenant, client.Tenant) {
		return bad("the sign-in is for another OAuth client")
	}
	return nil
}

// signInRebound reports that an update of a daemon account from old to
// cur changes what its stored sign-in is bound to: the address, the
// provider, or the client (id or tenant) the account resolves to. A
// client that no longer resolves counts as changed.
func (b *Backend) signInRebound(old, cur api.AccountConfig) bool {
	oo, ok1 := daemonOAuth(old)
	co, ok2 := daemonOAuth(cur)
	if !ok1 || !ok2 {
		return ok1 != ok2
	}
	if store.NormalizeAddress(old.Email) != store.NormalizeAddress(cur.Email) || oo.Provider != co.Provider {
		return true
	}
	_, oc, oerr := b.resolveClient(oo)
	_, cc, cerr := b.resolveClient(co)
	if oerr != nil || cerr != nil {
		return true
	}
	return oc.ID != cc.ID || !sameTenant(oc.Tenant, cc.Tenant)
}

// secretLock is one account's lock of lockSecrets.
type secretLock struct {
	mu   sync.Mutex
	refs int
}

// lockSecrets takes the account's secret lock and returns its release.
// It orders the backend's keyring writes of an account with the
// configuration they belong to: account.add, account.update and
// account.remove hold it from reading the account to their last keyring
// write, a completed re-sign-in from reading the account to storing its
// token, so neither sees the other half done. A token source's own
// writes (a rotated refresh token) do not take it; Retire, called when
// the source is dropped, stops them instead. Never called with oauthMu
// held.
func (b *Backend) lockSecrets(accountID string) func() {
	b.oauthMu.Lock()
	if b.secretLocks == nil {
		b.secretLocks = map[string]*secretLock{}
	}
	l := b.secretLocks[accountID]
	if l == nil {
		l = &secretLock{}
		b.secretLocks[accountID] = l
	}
	l.refs++
	b.oauthMu.Unlock()
	l.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Unlock()
			b.oauthMu.Lock()
			if l.refs--; l.refs == 0 {
				delete(b.secretLocks, accountID)
			}
			b.oauthMu.Unlock()
		})
	}
}

// keyringDisabled reports a keyring that can never hold a secret
// (MALACHI_KEYRING=none, or none installed), as opposed to one that
// refused this time (locked, prompt dismissed, service gone for now).
func (b *Backend) keyringDisabled() bool {
	switch b.Keyring.(type) {
	case nil, auth.UnavailableKeyring, *auth.UnavailableKeyring, auth.NotImplementedKeyring, *auth.NotImplementedKeyring:
		return true
	}
	return false
}

// deleteRefreshToken removes an account's stored refresh token, best
// effort (logged); the caller holds the account's secret lock.
func (b *Backend) deleteRefreshToken(ctx context.Context, accountID, why string) {
	err := b.Keyring.Delete(ctx, api.AccountID(accountID), auth.KeyRefreshToken)
	if err != nil && !errors.Is(err, auth.ErrNoSecret) && !errors.Is(err, api.ErrNotImplemented) {
		b.log.Warn("delete refresh token", "id", accountID, "why", why, "err", err)
	}
}

// notifyKeyringTrouble tells the UIs that an account's sign-in could not
// be kept in the keyring: notify.authRequired with reason keyringError,
// never with an authUrl (signing in again would not help).
func (b *Backend) notifyKeyringTrouble(accountID, message string) {
	b.SyncNotifier().AuthRequired(api.AuthRequiredNotification{
		AccountID: api.AccountID(accountID),
		Reason:    api.CodeKeyringError,
		Message:   message,
	})
}

// liveKeyring forwards to whatever keyring the backend has at the time of
// the call, so a token source made before malachid (or a test) installs
// the real one still uses it.
type liveKeyring struct{ b *Backend }

func (k liveKeyring) Get(ctx context.Context, id api.AccountID, key string) (string, error) {
	return k.b.Keyring.Get(ctx, id, key)
}
func (k liveKeyring) Set(ctx context.Context, id api.AccountID, key, value string) error {
	return k.b.Keyring.Set(ctx, id, key, value)
}
func (k liveKeyring) Delete(ctx context.Context, id api.AccountID, key string) error {
	return k.b.Keyring.Delete(ctx, id, key)
}

// memoryKeyring keeps a refresh token in memory for the daemon's lifetime:
// the fallback of a completed re-sign-in whose token the system keyring
// refused, so the account works until the daemon stops (and then asks for
// a sign-in again). Nothing else ever uses it.
type memoryKeyring struct {
	mu     sync.Mutex
	values map[string]string
}

func (k *memoryKeyring) Get(_ context.Context, id api.AccountID, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.values[string(id)+"/"+key]
	if !ok {
		return "", auth.ErrNoSecret
	}
	return v, nil
}
func (k *memoryKeyring) Set(_ context.Context, id api.AccountID, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[string(id)+"/"+key] = value
	return nil
}
func (k *memoryKeyring) Delete(_ context.Context, id api.AccountID, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.values, string(id)+"/"+key)
	return nil
}

// newTokenSource builds the token source of a stored daemon account over
// keyring, from the TokenSourceDefaults template.
func (b *Backend) newTokenSource(accountID string, p oauth2flow.Provider, c oauth2flow.Client, keyring auth.Keyring) *oauth2flow.TokenSource {
	opts := b.TokenSourceDefaults
	opts.Provider, opts.Client = p, c
	opts.AccountID, opts.Keyring = api.AccountID(accountID), keyring
	if opts.Log == nil {
		opts.Log = b.log
	}
	opts.OnAuthRequired = func() { b.ensureReauthSession(accountID) }
	return oauth2flow.NewTokenSource(opts)
}

// tokenSourceFor returns the token source of a stored daemon account,
// making it on first use. cfg must be the account's stored configuration;
// the source is dropped whenever that changes (dropTokenSource).
func (b *Backend) tokenSourceFor(accountID string, cfg api.AccountConfig) (*oauth2flow.TokenSource, error) {
	o, ok := daemonOAuth(cfg)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "account %q does not sign in through the backend", accountID)
	}
	p, client, err := b.resolveClient(o)
	if err != nil {
		return nil, err
	}
	b.oauthMu.Lock()
	defer b.oauthMu.Unlock()
	if ts, ok := b.tokenSources[accountID]; ok {
		return ts, nil
	}
	ts := b.newTokenSource(accountID, p, client, liveKeyring{b})
	b.tokenSources[accountID] = ts
	return ts, nil
}

// cachedTokenSource returns an existing token source without making one.
func (b *Backend) cachedTokenSource(accountID string) *oauth2flow.TokenSource {
	b.oauthMu.Lock()
	defer b.oauthMu.Unlock()
	return b.tokenSources[accountID]
}

// dropTokenSource forgets an account's token source (and its cached
// access token) after the account changed or went, and retires it: a
// refresh still in flight no longer writes its rotated refresh token, so
// a caller that deletes the stored token next cannot see it come back.
// The refresh token itself stays in the keyring.
func (b *Backend) dropTokenSource(accountID string) {
	b.oauthMu.Lock()
	ts := b.tokenSources[accountID]
	delete(b.tokenSources, accountID)
	b.oauthMu.Unlock()
	if ts != nil {
		ts.Retire()
	}
}

// invalidateTokenSource drops the cached access token of an account after
// a server refused it; the next request refreshes.
func (b *Backend) invalidateTokenSource(accountID string) {
	if ts := b.cachedTokenSource(accountID); ts != nil {
		ts.Invalidate()
	}
}

// daemonToken is the access token of a stored daemon account; accountID
// "" (a configuration not stored yet, without a session) has no sign-in.
func (b *Backend) daemonToken(ctx context.Context, accountID string, cfg api.AccountConfig) (string, error) {
	if accountID == "" {
		return "", api.NewError(api.CodeAuthRequired, "not signed in: sign in with account.oauthStart")
	}
	ts, err := b.tokenSourceFor(accountID, cfg)
	if err != nil {
		return "", err
	}
	return ts.AccessToken(ctx)
}

// graphIdentity names the mailbox a fresh Microsoft grant opens: Graph
// /me read with the grant's access token (through ProbeGraph, the probe
// account.test uses). A mailbox without an address is "", which fails a
// session that expects one (authFailed).
func (b *Backend) graphIdentity(ctx context.Context, g oauth2flow.Grant) (string, error) {
	pr, err := b.ProbeGraph(ctx, g.AccessToken)
	if err != nil {
		return "", err
	}
	return pr.Email, nil
}

// rememberSession records a session the backend started and forgets the
// ones oauth2flow no longer knows (expired or consumed). OAuth is never
// called with oauthMu held.
func (b *Backend) rememberSession(id string, s oauthSession) {
	b.oauthMu.Lock()
	ids := make([]string, 0, len(b.oauthSessions))
	for known := range b.oauthSessions {
		if known != id {
			ids = append(ids, known)
		}
	}
	b.oauthMu.Unlock()
	var gone []string
	for _, known := range ids {
		if _, ok := b.OAuth.Lookup(known); !ok {
			gone = append(gone, known)
		}
	}
	b.oauthMu.Lock()
	for _, known := range gone {
		delete(b.oauthSessions, known)
	}
	b.oauthSessions[id] = s
	b.oauthMu.Unlock()
}

func (b *Backend) sessionInfo(id string) (oauthSession, bool) {
	b.oauthMu.Lock()
	defer b.oauthMu.Unlock()
	s, ok := b.oauthSessions[id]
	return s, ok
}

// sessionGrant checks credentials.oauthSession for accountID ("" for
// account.add) with config cfg: a daemon account, a completed session the
// backend started, a re-sign-in only for its own account, and a grant
// that fits cfg (grantFits: provider, verified address, client). The
// session is not consumed. Every failure is invalidArgument.
func (b *Backend) sessionGrant(sessionID, accountID string, cfg api.AccountConfig) (oauth2flow.Grant, error) {
	bad := func(format string, args ...any) (oauth2flow.Grant, error) {
		return oauth2flow.Grant{}, api.NewError(api.CodeInvalidArgument, "credentials.oauthSession: "+format, args...)
	}
	if _, ok := daemonOAuth(cfg); !ok {
		return bad("only for an account that signs in through the backend (source daemon)")
	}
	info, known := b.sessionInfo(sessionID)
	g, ok := b.OAuth.Grant(sessionID)
	if !ok || !known {
		return bad("not a completed sign-in")
	}
	if info.accountID != "" && info.accountID != accountID {
		return bad("the sign-in is another account's")
	}
	if err := b.grantFits(g, cfg); err != nil {
		var aerr *api.Error
		if errors.As(err, &aerr) {
			return bad("%s", aerr.Message)
		}
		return bad("%v", err)
	}
	return g, nil
}

// consumeSession forgets a session whose grant an account now holds.
func (b *Backend) consumeSession(sessionID string) {
	b.OAuth.Consume(sessionID)
	b.forgetSession(sessionID)
}

// forgetSession drops what the backend remembers about a session.
func (b *Backend) forgetSession(sessionID string) {
	b.oauthMu.Lock()
	delete(b.oauthSessions, sessionID)
	b.oauthMu.Unlock()
}

// cancelReauth ends the pending re-sign-in of an account, if any.
func (b *Backend) cancelReauth(accountID string) {
	if id, _, ok := b.OAuth.Pending(accountID); ok {
		b.OAuth.Cancel(id)
	}
}

// startReauth opens (or returns the pending) re-sign-in session of a
// stored daemon account, keyed by the account id. A pending session is
// reused only while it is for the account's current provider, client and
// address; otherwise oauth2flow cancels it and starts a new one. Empty
// page texts keep a reused session's. On success the new refresh token is
// stored and the account restarted (onReauthComplete).
func (b *Backend) startReauth(a store.Account, page oauth2flow.PageTexts) (string, string, time.Time, error) {
	o, ok := daemonOAuth(a.Config)
	if !ok {
		return "", "", time.Time{}, api.NewError(api.CodeInvalidArgument, "account %q does not sign in through the backend", a.ID)
	}
	p, client, err := b.resolveClient(o)
	if err != nil {
		return "", "", time.Time{}, err
	}
	id, authURL, expires, err := b.OAuth.Start(oauth2flow.StartRequest{
		Provider:    p,
		Client:      client,
		LoginHint:   a.Config.Email,
		ExpectEmail: a.Config.Email,
		Page:        page,
		Key:         a.ID,
		OnComplete:  b.onReauthComplete(a.ID),
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	b.rememberSession(id, oauthSession{accountID: a.ID})
	return id, authURL, expires, nil
}

// ensureReauthSession makes sure a daemon account that lost its sign-in
// has a session waiting, so notify.authRequired can carry its authUrl.
// Idempotent: a waiting session that still fits the account is kept with
// its page texts, one for another address or client is replaced
// (startReauth). Failures are logged. It is the token sources'
// OnAuthRequired hook.
func (b *Backend) ensureReauthSession(accountID string) {
	ctx, cancel := context.WithTimeout(context.Background(), oauthHookTimeout)
	defer cancel()
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil {
		b.log.Debug("re-sign-in for a missing account", "id", accountID, "err", err)
		return
	}
	if _, ok := daemonOAuth(a.Config); !ok {
		return
	}
	before, _, waiting := b.OAuth.Pending(accountID)
	id, _, _, err := b.startReauth(a, oauth2flow.PageTexts{})
	if err != nil {
		b.log.Warn("cannot open a re-sign-in", "id", accountID, "err", err)
		return
	}
	if !waiting || id != before {
		b.log.Info("re-sign-in waiting", "id", accountID, "session", id)
	}
}

// onReauthComplete is the OnComplete hook of an account's re-sign-in. It
// stores the new refresh token and restarts the account's syncer and
// sender, which report through notify.syncState — unless the grant no
// longer fits the account (it changed or went during the sign-in: the
// session fails and its tokens are dropped from memory). A keyring that
// refused the token this time (locked, prompt dismissed) leaves it in
// memory until the daemon stops, so the account works now; a keyring that
// can never hold it (MALACHI_KEYRING=none) fails the session and the
// account stays in authRequired. Both are reported with
// notify.authRequired reason keyringError.
func (b *Backend) onReauthComplete(accountID string) func(oauth2flow.Grant) error {
	return func(g oauth2flow.Grant) error {
		ctx, cancel := context.WithTimeout(context.Background(), oauthHookTimeout)
		defer cancel()
		unlock := b.lockSecrets(accountID)
		defer unlock()
		a, err := b.store.GetAccount(ctx, accountID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			b.log.Warn("re-sign-in for a removed account dropped", "id", accountID)
			return api.NewError(api.CodeAccountNotFound, "the account was removed during the sign-in")
		case err != nil:
			return api.NewError(api.CodeStorageError, "%v", err)
		}
		if err := b.grantFits(g, a.Config); err != nil {
			b.log.Warn("re-sign-in no longer fits the account; its tokens are dropped", "id", accountID, "err", err)
			return err
		}

		ts, err := b.tokenSourceFor(accountID, a.Config)
		if err == nil {
			err = ts.Seed(ctx, g)
		}
		stored, inMemory := err == nil, false
		if err != nil {
			var aerr *api.Error
			transient := errors.As(err, &aerr) && aerr.Code == api.CodeKeyringError && !b.keyringDisabled()
			if !transient {
				b.log.Warn("re-sign-in not stored; the account stays signed out", "id", accountID, "err", err)
				if errors.As(err, &aerr) && aerr.Code == api.CodeKeyringError {
					b.notifyKeyringTrouble(accountID, "the keyring cannot hold the sign-in")
				}
				return keyringErr(err)
			}
			if merr := b.seedInMemory(ctx, a, g); merr != nil {
				b.log.Warn("re-sign-in lost", "id", accountID, "err", merr)
				b.notifyKeyringTrouble(accountID, "the keyring refused the sign-in")
				return keyringErr(err)
			}
			inMemory = true
			b.log.Warn("re-sign-in not stored in the keyring; kept in memory until the daemon stops", "id", accountID, "err", err)
		}

		// Removed while the token was being stored: nothing may outlive it.
		if _, gerr := b.store.GetAccount(ctx, accountID); errors.Is(gerr, store.ErrNotFound) {
			b.dropTokenSource(accountID)
			if stored {
				b.deleteRefreshToken(ctx, accountID, "account removed during the sign-in")
			}
			return api.NewError(api.CodeAccountNotFound, "the account was removed during the sign-in")
		}
		unlock()
		if inMemory {
			b.notifyKeyringTrouble(accountID, "the keyring refused the sign-in; it is kept until the backend stops")
		} else {
			b.log.Info("account signed in again", "id", accountID)
		}
		if a.Enabled {
			b.Supervisor.Restart(a)
			b.Delivery.Restart(a)
		}
		return nil
	}
}

// seedInMemory replaces an account's token source with one over a
// memoryKeyring holding g; the source it replaces is retired.
func (b *Backend) seedInMemory(ctx context.Context, a store.Account, g oauth2flow.Grant) error {
	o, _ := daemonOAuth(a.Config)
	p, client, err := b.resolveClient(o)
	if err != nil {
		return err
	}
	ts := b.newTokenSource(a.ID, p, client, &memoryKeyring{values: map[string]string{}})
	if err := ts.Seed(ctx, g); err != nil {
		return err
	}
	b.oauthMu.Lock()
	old := b.tokenSources[a.ID]
	b.tokenSources[a.ID] = ts
	b.oauthMu.Unlock()
	if old != nil {
		old.Retire()
	}
	return nil
}

// reauthURL is the authUrl for a notify.authRequired of a daemon account:
// the URL of its waiting re-sign-in (re-checked against the account),
// opening one when the account lost its sign-in (reason authRequired).
// With reason authFailed only a session already waiting is named;
// keyringError, other reasons and other accounts get "".
func (b *Backend) reauthURL(n api.AuthRequiredNotification) string {
	if b.OAuth == nil {
		return ""
	}
	id := string(n.AccountID)
	switch n.Reason {
	case api.CodeAuthRequired:
	case api.CodeAuthFailed:
		if _, _, ok := b.OAuth.Pending(id); !ok {
			return ""
		}
	default:
		return ""
	}
	a, err := b.store.GetAccount(context.Background(), id)
	if err != nil {
		return ""
	}
	if _, ok := daemonOAuth(a.Config); !ok {
		return ""
	}
	b.ensureReauthSession(id)
	if _, u, ok := b.OAuth.Pending(id); ok {
		return u
	}
	return ""
}

// Close ends every sign-in session and closes their listeners. malachid
// calls it on shutdown; a second call does nothing.
func (b *Backend) Close() {
	b.closeOnce.Do(func() {
		if b.OAuth != nil {
			b.OAuth.Close()
		}
	})
}

// cloneConfig deep-copies an account configuration.
func cloneConfig(c api.AccountConfig) *api.AccountConfig {
	out := c
	if c.IMAP != nil {
		v := *c.IMAP
		out.IMAP = &v
	}
	if c.SMTP != nil {
		v := *c.SMTP
		out.SMTP = &v
	}
	if c.OAuth2 != nil {
		v := *c.OAuth2
		v.Scopes = slices.Clone(v.Scopes)
		out.OAuth2 = &v
	}
	if c.Graph != nil {
		v := *c.Graph
		out.Graph = &v
	}
	return &out
}

// OAuthStart begins a sign-in through the backend's own flow: for a new
// account (a validated source "daemon" config) or again for a stored daemon
// account (idempotent: a pending session is returned with the new page
// texts). The UI opens authUrl in the browser.
func (s *accountService) OAuthStart(ctx context.Context, p api.AccountOAuthStartParams) (*api.AccountOAuthStartResult, error) {
	if (p.AccountID == "") == (p.Config == nil) {
		return nil, api.NewError(api.CodeInvalidArgument, "exactly one of accountId and config is required")
	}
	page := oauth2flow.PageTextsFrom(p.BrowserPage)
	var (
		id, authURL string
		expires     time.Time
		err         error
	)
	if p.AccountID != "" {
		a, rerr := s.b.requireAccount(ctx, string(p.AccountID))
		if rerr != nil {
			return nil, rerr
		}
		if _, ok := daemonOAuth(a.Config); !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "account %q does not sign in through the backend", p.AccountID)
		}
		if id, authURL, expires, err = s.b.startReauth(a, page); err != nil {
			return nil, err
		}
		if p.BrowserPage != nil {
			s.b.OAuth.SetPage(id, page)
		}
		s.b.log.Info("sign-in started", "session", id, "account", a.ID)
	} else {
		cfg := cloneConfig(*p.Config)
		if verr := validateAccountConfig(cfg); verr != nil {
			return nil, verr
		}
		o, ok := daemonOAuth(*cfg)
		if !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "config does not sign in through the backend (source daemon)")
		}
		provider, client, rerr := s.b.resolveClient(o)
		if rerr != nil {
			return nil, rerr
		}
		id, authURL, expires, err = s.b.OAuth.Start(oauth2flow.StartRequest{
			Provider:    provider,
			Client:      client,
			LoginHint:   cfg.Email,
			ExpectEmail: cfg.Email,
			Page:        page,
		})
		if err != nil {
			return nil, err
		}
		s.b.rememberSession(id, oauthSession{config: cfg})
		s.b.log.Info("sign-in started", "session", id, "provider", provider)
	}
	return &api.AccountOAuthStartResult{SessionID: id, AuthURL: authURL, ExpiresAt: expires.UTC()}, nil
}

// OAuthWait blocks until the session finishes or oauthWaitSlice elapses
// (pending). complete carries the config to add (a new account) or the
// account's config (a re-sign-in, already stored); a failed, cancelled
// or expired session answers with its error.
func (s *accountService) OAuthWait(ctx context.Context, p api.AccountOAuthWaitParams) (*api.AccountOAuthWaitResult, error) {
	if p.SessionID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "sessionId is required")
	}
	out, err := s.b.OAuth.Wait(ctx, p.SessionID, oauthWaitSlice)
	if err != nil {
		return nil, err
	}
	switch out.Status {
	case oauth2flow.StatusPending:
		return &api.AccountOAuthWaitResult{Status: api.OAuthSessionPending}, nil
	case oauth2flow.StatusComplete:
		res := &api.AccountOAuthWaitResult{Status: api.OAuthSessionComplete}
		info, _ := s.b.sessionInfo(p.SessionID)
		switch {
		case info.accountID != "":
			a, err := s.b.requireAccount(ctx, info.accountID)
			if err != nil {
				return nil, err
			}
			res.Config = &a.Config
		case info.config != nil:
			res.Config = cloneConfig(*info.config)
		}
		return res, nil
	}
	if out.Err != nil {
		return nil, out.Err
	}
	switch out.Status {
	case oauth2flow.StatusCancelled:
		return nil, api.NewError(api.CodeCancelled, "sign-in cancelled")
	case oauth2flow.StatusExpired:
		return nil, api.NewError(api.CodeServerTimeout, "sign-in expired")
	default:
		return nil, api.NewError(api.CodeAuthFailed, "sign-in failed")
	}
}

// OAuthCancel ends a pending session, or discards a completed one (its
// tokens leave memory; a re-sign-in's are stored already); unknown,
// failed and expired ones are ignored.
func (s *accountService) OAuthCancel(_ context.Context, p api.AccountOAuthCancelParams) (*api.AccountOAuthCancelResult, error) {
	if p.SessionID != "" && s.b.OAuth.Cancel(p.SessionID) {
		if _, ok := s.b.OAuth.Lookup(p.SessionID); !ok {
			s.b.forgetSession(p.SessionID)
		}
		s.b.log.Info("sign-in cancelled", "session", p.SessionID)
	}
	return &api.AccountOAuthCancelResult{}, nil
}

// keyringErr returns err as an *api.Error, wrapping anything else as
// keyringError (the only other failure of storing a sign-in).
func keyringErr(err error) *api.Error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return api.NewError(api.CodeKeyringError, "%v", err)
}
