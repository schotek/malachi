// Package auth handles credentials: keyring storage (libsecret via the
// org.freedesktop.secrets D-Bus API), OAuth2 authorisation-code flow with
// PKCE for Office 365, token refresh, and SASL mechanism selection (PLAIN,
// XOAUTH2 via github.com/emersion/go-sasl).
//
// Rules (docs/security.md):
//   - secrets are never written to config.toml, the SQLite store, or logs;
//   - the OAuth2 redirect listener binds to 127.0.0.1 on an ephemeral port
//     and accepts exactly one callback per pending flow;
//   - the UI opens the authorisation URL through the OpenURI portal; the
//     backend never spawns a browser itself.
//
// Gmail is deliberately out of scope (CASA audit / BYO credentials).
package auth

import (
	"context"

	// Pinned for the SASL mechanisms this package will implement.
	_ "github.com/emersion/go-sasl"

	"github.com/GITHUB_USER/malachi/backend/pkg/api"
)

// Keyring abstracts the secret store. The production implementation talks to
// org.freedesktop.secrets; tests use an in-memory one.
type Keyring interface {
	Get(ctx context.Context, account api.AccountID, key string) (string, error)
	Set(ctx context.Context, account api.AccountID, key, value string) error
	Delete(ctx context.Context, account api.AccountID, key string) error
}

// Secret keys stored per account.
const (
	KeyPassword     = "password"
	KeyRefreshToken = "oauth2.refresh_token"
)

// TokenSource yields a valid access token, refreshing if necessary, or
// returns an *api.Error with CodeAuthRequired when user interaction is needed.
type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

// NotImplementedKeyring is the bootstrap placeholder.
type NotImplementedKeyring struct{}

func (NotImplementedKeyring) Get(context.Context, api.AccountID, string) (string, error) {
	return "", api.ErrNotImplemented
}
func (NotImplementedKeyring) Set(context.Context, api.AccountID, string, string) error {
	return api.ErrNotImplemented
}
func (NotImplementedKeyring) Delete(context.Context, api.AccountID, string) error {
	return api.ErrNotImplemented
}
