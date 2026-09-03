// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

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
	"errors"

	// Pinned for the SASL mechanisms this package will implement.
	_ "github.com/emersion/go-sasl"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Keyring abstracts the secret store. The production implementation
// (secretservice) talks to org.freedesktop.secrets; tests use an in-memory
// one. Get returns ErrNoSecret when nothing is stored; any other failure is
// an *api.Error with CodeKeyringError.
type Keyring interface {
	Get(ctx context.Context, account api.AccountID, key string) (string, error)
	Set(ctx context.Context, account api.AccountID, key, value string) error
	Delete(ctx context.Context, account api.AccountID, key string) error
}

// ErrNoSecret is returned by Keyring.Get when no item matches. Callers decide
// whether that means authRequired (sync) or simply nothing to delete.
var ErrNoSecret = errors.New("auth: no such secret")

// UnavailableKeyring refuses every operation with keyringError. It is what
// MALACHI_KEYRING=none installs: accounts still work, but no password can be
// stored and nothing falls back to plaintext.
type UnavailableKeyring struct{}

func (UnavailableKeyring) Get(context.Context, api.AccountID, string) (string, error) {
	return "", errKeyringDisabled()
}
func (UnavailableKeyring) Set(context.Context, api.AccountID, string, string) error {
	return errKeyringDisabled()
}
func (UnavailableKeyring) Delete(context.Context, api.AccountID, string) error {
	return errKeyringDisabled()
}

func errKeyringDisabled() error {
	return api.NewError(api.CodeKeyringError, "keyring disabled (MALACHI_KEYRING=none)")
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
