// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// metaImportedAccounts is the meta key holding the JSON list of normalised
// e-mails already imported from config.toml, so that an account the user
// removed is not resurrected by the next daemon start.
const metaImportedAccounts = "accounts.imported"

// Limits for account configuration fields.
const (
	maxAccountNameBytes = 256
	maxUsernameBytes    = 256
	maxOAuth2Scopes     = 32
	maxClientIDBytes    = 256
)

type accountService struct{ b *Backend }

func (s *accountService) List(ctx context.Context, _ api.AccountListParams) (*api.AccountListResult, error) {
	items, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.Account, 0, len(items))
	for _, a := range items {
		out = append(out, s.b.toAPIAccount(a))
	}
	return &api.AccountListResult{Accounts: out}, nil
}

// Add validates the configuration, stores it and forwards the password to
// the keyring. The password never reaches the store or the log; when the
// keyring refuses it the account row is removed again. An account with the
// backend's own sign-in takes the refresh token of a completed session the
// same way (the session is consumed); without one it gets a sign-in
// session at once (notify.authRequired with authUrl). A successfully added
// account starts synchronising at once.
func (s *accountService) Add(ctx context.Context, p api.AccountAddParams) (*api.AccountAddResult, error) {
	if err := validateAccountConfig(&p.Config); err != nil {
		return nil, err
	}
	if err := checkCredentials(p.Config, p.Credentials); err != nil {
		return nil, err
	}
	_, daemon := daemonOAuth(p.Config)
	var grant oauth2flow.Grant
	if daemon {
		// Before the row exists, so a missing client leaves nothing.
		if _, _, err := s.b.resolveClient(p.Config.OAuth2); err != nil {
			return nil, err
		}
		if p.Credentials.OAuthSession != "" {
			g, err := s.b.sessionGrant(p.Credentials.OAuthSession, "", p.Config)
			if err != nil {
				return nil, err
			}
			grant = g
		}
	}

	a := store.Account{Name: p.Config.Name, Enabled: true, Config: p.Config}
	if err := s.b.store.AddAccount(ctx, &a); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, api.NewError(api.CodeConflict, "an account with this e-mail is already configured")
		}
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	id := api.AccountID(a.ID)

	unlock := s.b.lockSecrets(a.ID)
	defer unlock()
	if p.Credentials.Password != "" {
		if err := s.b.Keyring.Set(ctx, id, auth.KeyPassword, p.Credentials.Password); err != nil {
			if derr := s.b.store.DeleteAccount(ctx, a.ID, true); derr != nil {
				s.b.log.Warn("roll back account after keyring failure", "id", a.ID, "err", derr)
			}
			var apiErr *api.Error
			if errors.As(err, &apiErr) {
				return nil, apiErr
			}
			return nil, api.NewError(api.CodeKeyringError, "%v", err)
		}
	}
	if p.Credentials.OAuthSession != "" {
		ts, err := s.b.tokenSourceFor(a.ID, a.Config)
		if err == nil {
			err = ts.Seed(ctx, grant)
		}
		if err != nil {
			s.b.dropTokenSource(a.ID)
			if derr := s.b.store.DeleteAccount(ctx, a.ID, true); derr != nil {
				s.b.log.Warn("roll back account after keyring failure", "id", a.ID, "err", derr)
			}
			return nil, keyringErr(err)
		}
		s.b.consumeSession(p.Credentials.OAuthSession)
	}
	unlock()
	s.b.log.Info("account added", "id", a.ID, "signedIn", p.Credentials.OAuthSession != "")
	if daemon && p.Credentials.OAuthSession == "" {
		// No sign-in yet: open one now, so the syncer's authRequired
		// carries its URL.
		s.b.ensureReauthSession(a.ID)
	}
	if a.Enabled {
		s.b.Supervisor.Start(a)
		s.b.Delivery.Start(a)
	}
	s.b.accountsChanged()
	return &api.AccountAddResult{AccountID: id}, nil
}

// checkCredentials applies the credential rules shared by add, update and
// test: a password only for password endpoints, a sign-in session only for
// an account with the backend's own sign-in.
func checkCredentials(c api.AccountConfig, cr api.Credentials) error {
	if cr.Password != "" && !usesAuth(c, api.AuthPassword) {
		return api.NewError(api.CodeInvalidArgument, "password given but no endpoint uses password authentication")
	}
	if _, daemon := daemonOAuth(c); cr.OAuthSession != "" && !daemon {
		return api.NewError(api.CodeInvalidArgument, "oauthSession given but the account does not sign in through the backend (source daemon)")
	}
	return nil
}

// Remove stops the account's syncer, then deletes the account and, on
// request, its local data. Keyring secrets are removed best-effort: a
// missing keyring must not keep a removed account alive.
func (s *accountService) Remove(ctx context.Context, p api.AccountRemoveParams) (*api.AccountRemoveResult, error) {
	if p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	// Stop before the rows go so neither the syncer nor the outbox worker
	// can write into a half-deleted account; Stop on an unknown id is a
	// no-op.
	s.b.Supervisor.Stop(string(p.AccountID))
	s.b.Delivery.Stop(string(p.AccountID))
	err := s.b.store.DeleteAccount(ctx, string(p.AccountID), p.DeleteLocalData)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", p.AccountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	unlock := s.b.lockSecrets(string(p.AccountID))
	s.b.cancelReauth(string(p.AccountID))
	s.b.dropTokenSource(string(p.AccountID))
	for _, key := range []string{auth.KeyPassword, auth.KeyRefreshToken} {
		if err := s.b.Keyring.Delete(ctx, p.AccountID, key); err != nil && !errors.Is(err, api.ErrNotImplemented) {
			s.b.log.Warn("delete account secret", "id", p.AccountID, "key", key, "err", err)
		}
	}
	unlock()
	s.b.log.Info("account removed", "id", p.AccountID, "deleteLocalData", p.DeleteLocalData)
	s.b.accountsChanged()
	return &api.AccountRemoveResult{}, nil
}

func (s *accountService) SetEnabled(ctx context.Context, p api.AccountSetEnabledParams) (*api.AccountSetEnabledResult, error) {
	if p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	err := s.b.store.SetAccountEnabled(ctx, string(p.AccountID), p.Enabled)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", p.AccountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if p.Enabled {
		if a, err := s.b.store.GetAccount(ctx, string(p.AccountID)); err != nil {
			s.b.log.Warn("start sync after enabling account", "id", p.AccountID, "err", err)
		} else {
			s.b.Supervisor.Start(a)
			s.b.Delivery.Start(a)
		}
	} else {
		s.b.Supervisor.Stop(string(p.AccountID))
		s.b.Delivery.Stop(string(p.AccountID))
	}
	s.b.accountsChanged()
	return &api.AccountSetEnabledResult{}, nil
}

// Reorder sets the order account.list reports, which is the order the UI
// shows accounts in. Nothing about the accounts themselves changes.
func (s *accountService) Reorder(ctx context.Context, p api.AccountReorderParams) (*api.AccountReorderResult, error) {
	ids := make([]string, 0, len(p.AccountIDs))
	for _, id := range p.AccountIDs {
		if id == "" {
			return nil, api.NewError(api.CodeInvalidArgument, "accountIds must not contain an empty id")
		}
		ids = append(ids, string(id))
	}
	err := s.b.store.ReorderAccounts(ctx, ids)
	switch {
	case errors.Is(err, store.ErrBadOrder):
		return nil, api.NewError(api.CodeInvalidArgument, "%v", err)
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account in accountIds")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.log.Info("accounts reordered", "count", len(ids))
	s.b.accountsChanged()
	return &api.AccountReorderResult{}, nil
}

// Update replaces an account's configuration and, when a password is given,
// its keyring entry. The row is written first and reverted if the keyring
// refuses the new password, so both stay consistent. An account with the
// backend's own sign-in keeps its refresh token only while the token
// still belongs to it: a new address, provider or client without a new
// sign-in deletes the token and opens a re-sign-in session
// (notify.authRequired with its authUrl follows from the engines).
func (s *accountService) Update(ctx context.Context, p api.AccountUpdateParams) (*api.AccountUpdateResult, error) {
	if p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	if err := validateAccountConfig(&p.Config); err != nil {
		return nil, err
	}
	if err := checkCredentials(p.Config, p.Credentials); err != nil {
		return nil, err
	}
	unlock := s.b.lockSecrets(string(p.AccountID))
	defer unlock()
	existing, err := s.b.store.GetAccount(ctx, string(p.AccountID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", p.AccountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	_, daemon := daemonOAuth(p.Config)
	_, wasDaemon := daemonOAuth(existing.Config)
	var grant oauth2flow.Grant
	if daemon {
		if _, _, err := s.b.resolveClient(p.Config.OAuth2); err != nil {
			return nil, err
		}
		if p.Credentials.OAuthSession != "" {
			g, err := s.b.sessionGrant(p.Credentials.OAuthSession, string(p.AccountID), p.Config)
			if err != nil {
				return nil, err
			}
			grant = g
		}
	}
	// The stored sign-in no longer belongs to the account: its token must
	// not be refreshed (or sent) for the new configuration.
	rebound := wasDaemon && daemon && p.Credentials.OAuthSession == "" && s.b.signInRebound(existing.Config, p.Config)

	updated := existing
	updated.Name = p.Config.Name
	updated.Email = store.NormalizeAddress(p.Config.Email)
	updated.Config = p.Config
	err = s.b.store.UpdateAccount(ctx, &updated)
	switch {
	case errors.Is(err, store.ErrExists):
		return nil, api.NewError(api.CodeConflict, "another account already uses this e-mail")
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", p.AccountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}

	// The token source belongs to the old configuration.
	s.b.dropTokenSource(updated.ID)
	revert := func() {
		old := existing
		if rerr := s.b.store.UpdateAccount(ctx, &old); rerr != nil {
			s.b.log.Warn("revert account after keyring failure", "id", p.AccountID, "err", rerr)
		}
		s.b.dropTokenSource(updated.ID)
	}
	if p.Credentials.Password != "" {
		if err := s.b.Keyring.Set(ctx, p.AccountID, auth.KeyPassword, p.Credentials.Password); err != nil {
			revert()
			return nil, keyringErr(err)
		}
	}
	if p.Credentials.OAuthSession != "" {
		ts, err := s.b.tokenSourceFor(updated.ID, updated.Config)
		if err == nil {
			err = ts.Seed(ctx, grant)
		}
		if err != nil {
			revert()
			return nil, keyringErr(err)
		}
		s.b.consumeSession(p.Credentials.OAuthSession)
	}
	if !daemon || p.Credentials.OAuthSession != "" || rebound {
		// A waiting re-sign-in is moot: signed in now, no longer the
		// backend's to do, or for what the account was before.
		s.b.cancelReauth(updated.ID)
	}
	switch {
	case wasDaemon && !daemon:
		s.b.deleteRefreshToken(ctx, updated.ID, "the account left the backend's sign-in")
	case rebound:
		s.b.deleteRefreshToken(ctx, updated.ID, "the account's address, provider or client changed")
	}
	unlock()
	if daemon && p.Credentials.OAuthSession == "" && (!wasDaemon || rebound) {
		// Signed out now: a session waits at once, so the engines'
		// authRequired carries its URL.
		s.b.ensureReauthSession(updated.ID)
	}
	s.b.log.Info("account updated", "id", p.AccountID, "passwordChanged", p.Credentials.Password != "",
		"signedIn", p.Credentials.OAuthSession != "", "signInReset", rebound)
	if updated.Enabled {
		s.b.Supervisor.Restart(updated)
		s.b.Delivery.Restart(updated)
	}
	s.b.accountsChanged()
	return &api.AccountUpdateResult{}, nil
}

// Discover suggests server settings for an address. "Nothing found" is a
// result with source "none", not an error; a suggestion that would not
// pass Add is dropped the same way.
func (s *accountService) Discover(ctx context.Context, p api.AccountDiscoverParams) (*api.AccountDiscoverResult, error) {
	email := strings.TrimSpace(p.Email)
	if err := validateAddress(api.Address{Address: email}); err != nil {
		return nil, err
	}
	res, err := s.b.Discover(ctx, email)
	switch {
	case errors.Is(err, context.Canceled):
		return nil, err
	case err != nil:
		s.b.log.Warn("account discovery", "err", err)
		return &api.AccountDiscoverResult{Source: api.DiscoverNone}, nil
	}
	// A "provider" answer may be deliberately incomplete (the GNOME Online
	// Accounts hint: the sign-in is still missing); every other suggestion,
	// and every alternative, must pass Add.
	if res.Config != nil && res.Source != api.DiscoverProvider {
		if err := validateAccountConfig(res.Config); err != nil {
			s.b.log.Warn("discovered configuration rejected", "source", res.Source, "err", err)
			res.Config, res.Source = nil, api.DiscoverNone
		}
	}
	if res.Config == nil {
		res.Source = api.DiscoverNone
	}
	var alternatives []api.AccountConfig
	for i := range res.Alternatives {
		if res.Config == nil {
			break
		}
		alt := res.Alternatives[i]
		if err := validateAccountConfig(&alt); err != nil {
			s.b.log.Warn("discovered alternative rejected", "source", res.Source, "err", err)
			continue
		}
		alternatives = append(alternatives, alt)
	}
	s.b.log.Info("account discovery", "source", res.Source, "alternatives", len(alternatives))
	return &api.AccountDiscoverResult{Config: res.Config, Source: res.Source, ProviderName: res.ProviderName, Alternatives: alternatives}, nil
}

// Test validates like Add, then probes the endpoints of the account kind
// (IMAP and SMTP concurrently, or the Graph mailbox). Each endpoint reports
// its own outcome; the call itself fails only for an invalid configuration.
// The password is used for the connections and never logged; so is a
// token, which comes from GNOME Online Accounts, from the session named by
// credentials.oauthSession (not consumed) or from the stored sign-in of
// accountId.
func (s *accountService) Test(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	if err := validateAccountConfig(&p.Config); err != nil {
		return nil, err
	}
	if err := checkCredentials(p.Config, p.Credentials); err != nil {
		return nil, err
	}
	var grant *oauth2flow.Grant
	if p.Credentials.OAuthSession != "" {
		g, err := s.b.sessionGrant(p.Credentials.OAuthSession, string(p.AccountID), p.Config)
		if err != nil {
			return nil, err
		}
		grant = &g
	}
	if p.Config.Protocol() == api.AccountGraph {
		return &api.AccountTestResult{Graph: s.testGraph(ctx, p, grant)}, nil
	}
	password := p.Credentials.Password
	switch {
	case usesAuth(p.Config, api.AuthOAuth2):
		// The token stands in for the password. A token problem (sign-in
		// lost, no GNOME Online Accounts, no client) is both endpoints'
		// outcome, as a refused password would be.
		token, err := s.testToken(ctx, p, grant)
		if err != nil {
			s.b.log.Info("account test", "kind", "oauth2", "ok", false, "err", err)
			return &api.AccountTestResult{IMAP: endpointResult(nil, 0, err), SMTP: endpointResult(nil, 0, err)}, nil
		}
		password = token
	case password == "" && p.AccountID != "" && usesAuth(p.Config, api.AuthPassword):
		pw, err := s.b.PasswordFor(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		password = pw
	}

	var res api.AccountTestResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r, err := s.b.ProbeIMAP(ctx, *p.Config.IMAP, password)
		res.IMAP = endpointResult(r.Capabilities, r.Latency, err)
		s.logProbe("imap", *p.Config.IMAP, *res.IMAP)
	}()
	go func() {
		defer wg.Done()
		r, err := s.b.ProbeSMTP(ctx, *p.Config.SMTP, password)
		res.SMTP = endpointResult(r.Capabilities, r.Latency, err)
		s.logProbe("smtp", *p.Config.SMTP, *res.SMTP)
	}()
	wg.Wait()
	return &res, nil
}

// testToken is the access token account.test signs in with for an oauth2
// account or a Graph account: the session's (grant), else the stored
// sign-in of accountId for the backend's own flow, else GNOME Online
// Accounts'. Errors are the endpoints' outcome.
func (s *accountService) testToken(ctx context.Context, p api.AccountTestParams, grant *oauth2flow.Grant) (string, error) {
	cfg := p.Config
	if _, daemon := daemonOAuth(cfg); daemon {
		if grant != nil {
			return grant.AccessToken, nil
		}
		if p.AccountID == "" {
			return "", api.NewError(api.CodeAuthRequired, "not signed in: sign in with account.oauthStart")
		}
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return "", err
		}
		if _, stored := daemonOAuth(a.Config); !stored {
			return "", api.NewError(api.CodeAuthRequired, "account %q has no sign-in of the backend yet", p.AccountID)
		}
		return s.b.daemonToken(ctx, a.ID, a.Config)
	}
	if cfg.Protocol() == api.AccountGraph {
		return s.b.graphToken(ctx, string(p.AccountID), cfg)
	}
	return s.b.oauth2Token(ctx, string(p.AccountID), cfg)
}

// testGraph fetches a token from the account's source and opens the
// mailbox. A token problem (sign-in revoked, no GNOME Online Accounts) is
// the endpoint's error, like a refused password on IMAP. The mailbox must
// belong to the configured address.
func (s *accountService) testGraph(ctx context.Context, p api.AccountTestParams, grant *oauth2flow.Grant) *api.EndpointTestResult {
	cfg := p.Config
	token, err := s.testToken(ctx, p, grant)
	if err != nil {
		r := endpointResult(nil, 0, err)
		s.b.log.Info("account test", "kind", "graph", "ok", false, "code", r.Error.Code)
		return r
	}
	pr, err := s.b.ProbeGraph(ctx, token)
	if err == nil && pr.Email != "" && !strings.EqualFold(store.NormalizeAddress(pr.Email), store.NormalizeAddress(cfg.Email)) {
		err = api.NewError(api.CodeInvalidArgument, "the signed-in mailbox is not %s", cfg.Email)
	}
	r := endpointResult(pr.Capabilities, pr.Latency, err)
	attrs := []any{"kind", "graph", "ok", r.OK, "latencyMs", r.LatencyMS}
	if r.Error != nil {
		attrs = append(attrs, "code", r.Error.Code)
	}
	s.b.log.Info("account test", attrs...)
	return r
}

// Linked lists the accounts other desktop services are signed in to and
// that Malachi can use: Microsoft 365 accounts of GNOME Online Accounts
// with mail enabled. Without a session bus or GOA the list is empty.
func (s *accountService) Linked(ctx context.Context, _ api.AccountLinkedParams) (*api.AccountLinkedResult, error) {
	out := &api.AccountLinkedResult{Accounts: []api.LinkedAccount{}}
	if s.b.GOA == nil {
		return out, nil
	}
	accounts, err := s.b.GOA.Accounts(ctx)
	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Code == api.CodeUnavailable {
			s.b.log.Debug("gnome online accounts unavailable", "err", err)
			return out, nil
		}
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, api.NewError(api.CodeServerError, "%v", err)
	}
	existing, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	configured := make(map[string]bool, len(existing))
	for _, a := range existing {
		configured[a.Email] = true
	}
	for _, a := range accounts {
		cfg, _, ok := goaConfigFor(a)
		if !ok {
			continue
		}
		if validateAddress(api.Address{Address: cfg.Email}) != nil {
			s.b.log.Debug("linked account without a usable address", "id", a.ID)
			continue
		}
		provider, _ := providerOf(a.ProviderType)
		out.Accounts = append(out.Accounts, api.LinkedAccount{
			Provider:        provider,
			Email:           cfg.Email,
			Name:            cfg.DisplayName,
			GOAAccountID:    a.ID,
			Configured:      configured[store.NormalizeAddress(cfg.Email)],
			AttentionNeeded: a.AttentionNeeded,
			Config:          cfg,
		})
	}
	return out, nil
}

// cleanDisplayName trims an untrusted display name to what account.add
// would accept; anything else becomes empty.
func cleanDisplayName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxAccountNameBytes || !utf8.ValidString(s) || hasControl(s) {
		return ""
	}
	return s
}

func endpointResult(caps []string, latency time.Duration, err error) *api.EndpointTestResult {
	r := &api.EndpointTestResult{Capabilities: caps, LatencyMS: int(latency / time.Millisecond)}
	if err == nil {
		r.OK = true
		return r
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		apiErr = api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
	}
	r.Error = apiErr
	return r
}

func (s *accountService) logProbe(kind string, sc api.ServerConfig, r api.EndpointTestResult) {
	attrs := []any{"kind", kind, "host", sc.Host, "port", sc.Port, "security", sc.Security, "ok", r.OK, "latencyMs", r.LatencyMS}
	if r.Error != nil {
		attrs = append(attrs, "code", r.Error.Code)
	}
	s.b.log.Info("account test", attrs...)
}

// ImportConfigAccounts copies the [[accounts]] entries of config.toml into
// the store. An entry is skipped when it is invalid (logged), when an
// account with its e-mail exists, or when its e-mail was imported before
// (the user may have removed it since). Store failures are returned.
func (b *Backend) ImportConfigAccounts(ctx context.Context) error {
	if len(b.defaults.Accounts) == 0 {
		return nil
	}
	imported, err := b.importedAccounts(ctx)
	if err != nil {
		return err
	}
	changed := false
	for i, entry := range b.defaults.Accounts {
		cfg := entry.ToAPI()
		if err := validateAccountConfig(&cfg); err != nil {
			b.log.Warn("config.toml account skipped", "index", i, "err", err)
			continue
		}
		email := store.NormalizeAddress(cfg.Email)
		if imported[email] {
			continue
		}
		a := store.Account{ID: entry.ID, Email: email, Name: cfg.Name, Enabled: entry.IsEnabled(), Config: cfg}
		err := b.store.AddAccount(ctx, &a)
		switch {
		case errors.Is(err, store.ErrExists):
			b.log.Debug("config.toml account already in store", "email", email)
		case err != nil:
			return err
		default:
			b.log.Info("account imported from config.toml", "id", a.ID)
		}
		imported[email] = true
		changed = true
	}
	if !changed {
		return nil
	}
	return b.setImportedAccounts(ctx, imported)
}

func (b *Backend) importedAccounts(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	raw, ok, err := b.store.GetMeta(ctx, metaImportedAccounts)
	if err != nil || !ok {
		return out, err
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		b.log.Warn("ignoring malformed meta value", "key", metaImportedAccounts, "err", err)
		return out, nil
	}
	for _, e := range list {
		out[e] = true
	}
	return out, nil
}

func (b *Backend) setImportedAccounts(ctx context.Context, set map[string]bool) error {
	list := make([]string, 0, len(set))
	for e := range set {
		list = append(list, e)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return b.store.SetMeta(ctx, metaImportedAccounts, string(raw))
}

// accountsChanged broadcasts notify.accountsChanged when a notifier is set.
func (b *Backend) accountsChanged() {
	if n := b.getNotifier(); n != nil {
		n.AccountsChanged(api.AccountsChangedNotification{})
	}
}

// toAPIAccount derives the wire form with the live sync state (stateFor).
func (b *Backend) toAPIAccount(a store.Account) api.Account {
	return api.Account{ID: api.AccountID(a.ID), Config: a.Config, Enabled: a.Enabled, State: b.stateFor(a)}
}

// usesAuth reports whether an IMAP or SMTP endpoint of the account uses the
// method; a Graph account has neither.
func usesAuth(c api.AccountConfig, m api.AuthMethod) bool {
	return (c.IMAP != nil && c.IMAP.AuthMethod == m) || (c.SMTP != nil && c.SMTP.AuthMethod == m)
}

// validateAccountConfig checks an AccountConfig against the rules documented
// in docs/api.md §4.1 and trims the free-text fields in place. Every failure
// is invalidArgument.
func validateAccountConfig(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}

	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return bad("name is required")
	}
	if len(c.Name) > maxAccountNameBytes || !utf8.ValidString(c.Name) || hasHeaderBreak(c.Name) {
		return bad("name must be valid UTF-8 without line breaks (limit %d bytes)", maxAccountNameBytes)
	}
	c.Email = strings.TrimSpace(c.Email)
	if err := validateAddress(api.Address{Address: c.Email}); err != nil {
		return err
	}
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	if len(c.DisplayName) > maxAccountNameBytes || !utf8.ValidString(c.DisplayName) || hasHeaderBreak(c.DisplayName) {
		return bad("displayName must be valid UTF-8 without line breaks (limit %d bytes)", maxAccountNameBytes)
	}

	switch c.Protocol() {
	case api.AccountIMAP:
		if c.Graph != nil {
			return bad("graph settings given for an imap account")
		}
		if c.IMAP == nil || c.SMTP == nil {
			return bad("imap and smtp settings are required")
		}
		if err := validateServer("imap", c.IMAP); err != nil {
			return err
		}
		if err := validateServer("smtp", c.SMTP); err != nil {
			return err
		}
		switch {
		case usesAuth(*c, api.AuthOAuth2) && c.OAuth2 == nil:
			return bad("oauth2 settings are required when an endpoint uses oauth2")
		case !usesAuth(*c, api.AuthOAuth2) && c.OAuth2 != nil:
			return bad("oauth2 settings given but no endpoint uses oauth2")
		case c.OAuth2 != nil:
			if err := validateOAuth2(c.OAuth2); err != nil {
				return err
			}
			if c.OAuth2.Source == api.OAuth2SourceDaemon && c.OAuth2.Provider != api.OAuth2ProviderGoogle {
				return bad("oauth2: provider must be google with source daemon on an imap account")
			}
			// One sign-in for both: a GOA or daemon account has no
			// password to give the other endpoint.
			if c.OAuth2.Source != "" && (c.IMAP.AuthMethod != api.AuthOAuth2 || c.SMTP.AuthMethod != api.AuthOAuth2) {
				return bad("oauth2: both endpoints must use oauth2 with source %s", c.OAuth2.Source)
			}
			if c.OAuth2.Source == api.OAuth2SourceDaemon {
				if err := validateGmailEndpoints(c); err != nil {
					return err
				}
			}
		}
	case api.AccountGraph:
		if c.IMAP != nil || c.SMTP != nil {
			return bad("imap and smtp settings do not apply to a graph account")
		}
		if c.Graph == nil {
			return bad("graph settings are required")
		}
		c.Graph.GOAAccountID = strings.TrimSpace(c.Graph.GOAAccountID)
		switch c.Graph.Source {
		case api.GraphSourceGOA:
			if c.OAuth2 != nil {
				return bad("oauth2 settings do not apply to a graph account with source goa")
			}
			if !goa.ValidID(c.Graph.GOAAccountID) {
				return bad("graph: goaAccountId is required")
			}
		case api.GraphSourceDaemon:
			if c.Graph.GOAAccountID != "" {
				return bad("graph: goaAccountId needs source goa")
			}
			if c.OAuth2 == nil {
				return bad("oauth2 settings are required with graph source daemon")
			}
			if c.OAuth2.Source != api.OAuth2SourceDaemon || c.OAuth2.Provider != api.OAuth2ProviderOffice365 {
				return bad("oauth2: source daemon and provider office365 are required with graph source daemon")
			}
			if err := validateOAuth2(c.OAuth2); err != nil {
				return err
			}
		default:
			return bad("graph: source must be goa or daemon")
		}
	default:
		return bad("kind must be imap or graph")
	}

	if c.SyncInterval != 0 && c.SyncInterval < api.SyncIntervalMin {
		return bad("syncIntervalSeconds must be 0 or at least %d", api.SyncIntervalMin)
	}
	return nil
}

// Gmail's servers: the only ones a Google token of the backend's own
// sign-in is sent to.
const (
	gmailIMAPHost = "imap.gmail.com"
	gmailSMTPHost = "smtp.gmail.com"
)

// validateGmailEndpoints pins an IMAP account with the backend's own
// Google sign-in to Gmail's servers. The token carries the
// https://mail.google.com/ scope and XOAUTH2 hands it to whatever server
// the account names, so the servers are part of the token's audience
// (RFC 9700 §4.10): IMAP imap.gmail.com:993 with tls, SMTP smtp.gmail.com
// with 465/tls or 587/starttls, and the address as the user name on both.
// Host names are stored lower-case.
func validateGmailEndpoints(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "oauth2: "+format, args...)
	}
	im, sm := c.IMAP, c.SMTP
	if !strings.EqualFold(im.Host, gmailIMAPHost) || im.Port != 993 || im.Security != api.SecurityTLS {
		return bad("with source daemon and provider google the imap server must be %s, port 993, tls", gmailIMAPHost)
	}
	smtpOK := strings.EqualFold(sm.Host, gmailSMTPHost) &&
		(sm.Port == 465 && sm.Security == api.SecurityTLS || sm.Port == 587 && sm.Security == api.SecuritySTARTTLS)
	if !smtpOK {
		return bad("with source daemon and provider google the smtp server must be %s, port 465 with tls or 587 with starttls", gmailSMTPHost)
	}
	if !strings.EqualFold(im.Username, c.Email) || !strings.EqualFold(sm.Username, c.Email) {
		return bad("with source daemon and provider google both user names must be the address")
	}
	im.Host, sm.Host = gmailIMAPHost, gmailSMTPHost
	return nil
}

func validateServer(which string, sc *api.ServerConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, which+": "+format, args...)
	}
	sc.Host = strings.TrimSpace(sc.Host)
	if sc.Host == "" {
		return bad("host is required")
	}
	if !transport.ValidHost(sc.Host) {
		return bad("invalid host %q", sc.Host)
	}
	if sc.Port < 1 || sc.Port > 65535 {
		return bad("port must be 1–65535")
	}
	switch sc.Security {
	case api.SecurityTLS, api.SecuritySTARTTLS:
	case api.SecurityNone:
		if !transport.IsLoopbackHost(sc.Host) {
			return bad("security \"none\" is allowed only for localhost")
		}
	default:
		return bad("security must be tls, starttls or none")
	}
	sc.Username = strings.TrimSpace(sc.Username)
	if sc.Username == "" {
		return bad("username is required")
	}
	if len(sc.Username) > maxUsernameBytes || !utf8.ValidString(sc.Username) || hasControl(sc.Username) {
		return bad("username must be valid UTF-8 without control characters (limit %d bytes)", maxUsernameBytes)
	}
	switch sc.AuthMethod {
	case api.AuthPassword, api.AuthOAuth2:
	default:
		return bad("authMethod must be password or oauth2")
	}
	// A pinned certificate replaces the verification of the chain and the
	// name (docs/security.md §7): only over TLS, and never for a token of
	// a provider, which must reach nobody but the provider's servers.
	sc.CertificateSHA256 = strings.TrimSpace(sc.CertificateSHA256)
	if sc.CertificateSHA256 != "" {
		pin, ok := api.NormalizeCertificateSHA256(sc.CertificateSHA256)
		switch {
		case !ok:
			return bad("certificateSha256 must be a SHA-256 fingerprint of 64 hex digits")
		case sc.Security == api.SecurityNone:
			return bad("certificateSha256 needs security tls or starttls")
		case sc.AuthMethod != api.AuthPassword:
			return bad("certificateSha256 needs authMethod password")
		}
		sc.CertificateSHA256 = pin
	}
	return nil
}

// validateOAuth2 checks an oauth2 block on its own; the caller checks it
// fits the account (its kind, its endpoints).
func validateOAuth2(o *api.OAuth2Config) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "oauth2: "+format, args...)
	}
	o.GOAAccountID = strings.TrimSpace(o.GOAAccountID)
	switch o.Source {
	case api.OAuth2SourceGOA:
		// GNOME Online Accounts holds the sign-in: nothing of an own flow
		// applies, and the only provider used this way is Google.
		if o.Provider != api.OAuth2ProviderGoogle {
			return bad("provider must be google with source goa")
		}
		if !goa.ValidID(o.GOAAccountID) {
			return bad("goaAccountId is required with source goa")
		}
		if o.ClientID != "" || o.TenantID != "" || o.AuthURL != "" || o.TokenURL != "" || len(o.Scopes) > 0 {
			return bad("clientId, tenantId, authUrl, tokenUrl and scopes do not apply with source goa")
		}
		return nil
	case api.OAuth2SourceDaemon:
		// The backend's own sign-in: its provider table owns the
		// endpoints and scopes; only the client may be overridden.
		// Google on an IMAP account, Microsoft 365 on a Graph account.
		if o.Provider != api.OAuth2ProviderGoogle && o.Provider != api.OAuth2ProviderOffice365 {
			return bad("provider must be google or office365 with source daemon")
		}
		if o.GOAAccountID != "" {
			return bad("goaAccountId needs source goa")
		}
		if o.AuthURL != "" || o.TokenURL != "" || len(o.Scopes) > 0 {
			return bad("authUrl, tokenUrl and scopes do not apply with source daemon")
		}
		o.ClientID = strings.TrimSpace(o.ClientID)
		if !config.PrintableToken(o.ClientID, maxClientIDBytes) {
			return bad("clientId must be printable ASCII without spaces (limit %d bytes)", maxClientIDBytes)
		}
		o.TenantID = strings.TrimSpace(o.TenantID)
		switch {
		case o.TenantID == "":
		case o.Provider != api.OAuth2ProviderOffice365:
			return bad("tenantId applies to office365 only")
		case !config.ValidTenant(o.TenantID):
			return bad("tenantId must be letters, digits, '.' and '-', not starting with '.' (limit 64 bytes)")
		}
		return nil
	case "":
		if o.GOAAccountID != "" {
			return bad("goaAccountId needs source goa")
		}
	default:
		return bad("source must be goa, daemon or absent")
	}
	switch o.Provider {
	case api.OAuth2ProviderOffice365:
	case api.OAuth2ProviderCustom:
		for name, raw := range map[string]string{"authUrl": o.AuthURL, "tokenUrl": o.TokenURL} {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return bad("%s must be an https URL", name)
			}
		}
	default:
		return bad("provider must be office365 or custom")
	}
	if len(o.Scopes) > maxOAuth2Scopes {
		return bad("too many scopes (limit %d)", maxOAuth2Scopes)
	}
	for _, s := range o.Scopes {
		if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 {
			return bad("scopes must be non-empty without whitespace")
		}
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}
