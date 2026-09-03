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
)

type accountService struct{ b *Backend }

func (s *accountService) List(ctx context.Context, _ api.AccountListParams) (*api.AccountListResult, error) {
	items, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.Account, 0, len(items))
	for _, a := range items {
		out = append(out, toAPIAccount(a))
	}
	return &api.AccountListResult{Accounts: out}, nil
}

// Add validates the configuration, stores it and forwards the password to
// the keyring. The password never reaches the store or the log; when the
// keyring refuses it the account row is removed again.
func (s *accountService) Add(ctx context.Context, p api.AccountAddParams) (*api.AccountAddResult, error) {
	if err := validateAccountConfig(&p.Config); err != nil {
		return nil, err
	}
	if p.Credentials.Password != "" && !usesAuth(p.Config, api.AuthPassword) {
		return nil, api.NewError(api.CodeInvalidArgument, "password given but no endpoint uses password authentication")
	}

	a := store.Account{Name: p.Config.Name, Enabled: true, Config: p.Config}
	if err := s.b.store.AddAccount(ctx, &a); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, api.NewError(api.CodeConflict, "an account with this e-mail is already configured")
		}
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	id := api.AccountID(a.ID)

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
	s.b.log.Info("account added", "id", a.ID)
	s.b.accountsChanged()
	return &api.AccountAddResult{AccountID: id}, nil
}

// Remove deletes the account and, on request, its local data. Keyring
// secrets are removed best-effort: a missing keyring must not keep a
// removed account alive.
func (s *accountService) Remove(ctx context.Context, p api.AccountRemoveParams) (*api.AccountRemoveResult, error) {
	if p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	err := s.b.store.DeleteAccount(ctx, string(p.AccountID), p.DeleteLocalData)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", p.AccountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	for _, key := range []string{auth.KeyPassword, auth.KeyRefreshToken} {
		if err := s.b.Keyring.Delete(ctx, p.AccountID, key); err != nil && !errors.Is(err, api.ErrNotImplemented) {
			s.b.log.Warn("delete account secret", "id", p.AccountID, "key", key, "err", err)
		}
	}
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
	s.b.accountsChanged()
	return &api.AccountSetEnabledResult{}, nil
}

// Test validates like Add, then probes both endpoints concurrently. Each
// endpoint reports its own outcome; the call itself fails only for an
// invalid configuration. The password is used for the connections and
// never logged.
func (s *accountService) Test(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	if err := validateAccountConfig(&p.Config); err != nil {
		return nil, err
	}
	if p.Credentials.Password != "" && !usesAuth(p.Config, api.AuthPassword) {
		return nil, api.NewError(api.CodeInvalidArgument, "password given but no endpoint uses password authentication")
	}

	var res api.AccountTestResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r, err := s.b.ProbeIMAP(ctx, p.Config.IMAP, p.Credentials.Password)
		res.IMAP = endpointResult(r.Capabilities, r.Latency, err)
		s.logProbe("imap", p.Config.IMAP, res.IMAP)
	}()
	go func() {
		defer wg.Done()
		r, err := s.b.ProbeSMTP(ctx, p.Config.SMTP, p.Credentials.Password)
		res.SMTP = endpointResult(r.Capabilities, r.Latency, err)
		s.logProbe("smtp", p.Config.SMTP, res.SMTP)
	}()
	wg.Wait()
	return &res, nil
}

func endpointResult(caps []string, latency time.Duration, err error) api.EndpointTestResult {
	r := api.EndpointTestResult{Capabilities: caps, LatencyMS: int(latency / time.Millisecond)}
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

// toAPIAccount derives the wire form. Until the sync engine exists the state
// is only "disabled" or "idle" with unknown progress.
func toAPIAccount(a store.Account) api.Account {
	state := api.SyncState{AccountID: api.AccountID(a.ID), Status: api.SyncIdle, Progress: -1}
	if !a.Enabled {
		state.Status = api.SyncDisabled
	}
	return api.Account{ID: api.AccountID(a.ID), Config: a.Config, Enabled: a.Enabled, State: state}
}

func usesAuth(c api.AccountConfig, m api.AuthMethod) bool {
	return c.IMAP.AuthMethod == m || c.SMTP.AuthMethod == m
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

	if err := validateServer("imap", &c.IMAP); err != nil {
		return err
	}
	if err := validateServer("smtp", &c.SMTP); err != nil {
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
	}

	if c.SyncInterval != 0 && c.SyncInterval < api.SyncIntervalMin {
		return bad("syncIntervalSeconds must be 0 or at least %d", api.SyncIntervalMin)
	}
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
	return nil
}

func validateOAuth2(o *api.OAuth2Config) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "oauth2: "+format, args...)
	}
	switch o.Provider {
	case "office365":
	case "custom":
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
