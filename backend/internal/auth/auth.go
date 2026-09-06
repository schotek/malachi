// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package auth handles credentials: the keyring abstraction (libsecret
// through the org.freedesktop.secrets D-Bus API lives in the secretservice
// subpackage), and the SASL XOAUTH2 mechanism with which an OAuth2 access
// token signs in to IMAP and SMTP. The tokens themselves come from GNOME
// Online Accounts (the goa subpackage): it owns the sign-in, the refresh
// token and the OAuth client id, which is why Gmail needs no Google
// verification of an own client. An own authorisation-code flow for
// desktops without GNOME Online Accounts is reserved in the API
// (api.OAuth2Config) and not implemented.
//
// Rules (docs/security.md):
//   - secrets, tokens included, are never written to config.toml, the
//     SQLite store, or logs; a token lives in memory for the exchange.
package auth

import (
	"context"
	"errors"

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
