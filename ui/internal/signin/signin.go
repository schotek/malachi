// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package signin says how an account signs in and which way the account
// assistant continues after account.discover: a password, GNOME Online
// Accounts, or the backend's own sign-in in the browser
// (account.oauthStart, docs/api.md §4.1). It holds no GTK and no
// user-facing text, so its rules are tested without a display; the
// assistant, the sign-in banner and the Accounts page act on its answers.
package signin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Providers as account.linked names them (docs/api.md §4.1); the UI keys
// the provider's name and icon by them.
const (
	ProviderMicrosoft365 = "microsoft365"
	ProviderGoogle       = "google"
)

// Kind is where an account's sign-in lives, and so where it is repaired.
type Kind int

const (
	// Password: IMAP/SMTP with a password in the keyring, corrected in
	// the account assistant.
	Password Kind = iota
	// GOA: GNOME Online Accounts holds the sign-in (Microsoft 365 through
	// Graph, Google with a token); it is repaired in Settings → Online
	// Accounts.
	GOA
	// OAuth: the backend's own sign-in in the browser (source "daemon");
	// it is repaired with account.oauthStart.
	OAuth
)

// KindOf classifies an account: a Graph account signs in through GNOME
// Online Accounts when graph.source is "goa" and through the backend's own
// sign-in otherwise; an account with an oauth2 block likewise by its
// source; anything else with a password.
func KindOf(cfg api.AccountConfig) Kind {
	switch {
	case cfg.Protocol() == api.AccountGraph:
		if cfg.Graph != nil && cfg.Graph.Source == api.GraphSourceGOA {
			return GOA
		}
		return OAuth
	case cfg.OAuth2 != nil:
		if cfg.OAuth2.Source == api.OAuth2SourceGOA {
			return GOA
		}
		return OAuth
	}
	return Password
}

// Provider is the provider an account signs in with: Microsoft 365 for
// any Graph account and for an oauth2 block of provider "office365",
// Google for provider "google", "" for a password account or a provider
// the UI does not know.
func Provider(cfg api.AccountConfig) string {
	if cfg.Protocol() == api.AccountGraph {
		return ProviderMicrosoft365
	}
	if cfg.OAuth2 != nil {
		switch cfg.OAuth2.Provider {
		case api.OAuth2ProviderGoogle:
			return ProviderGoogle
		case api.OAuth2ProviderOffice365:
			return ProviderMicrosoft365
		}
	}
	return ""
}

// ProviderName is the provider's name as shown to the user, "" for an
// unknown one. These are brand names and are not translated; the
// backend's providerName is untrusted text and never used in their place.
func ProviderName(provider string) string {
	switch provider {
	case ProviderMicrosoft365:
		return "Microsoft 365"
	case ProviderGoogle:
		return "Google"
	}
	return ""
}

// goaAccountID is the GNOME Online Accounts id an account signs in with,
// "" when it has none: a password or own sign-in account, or the sign-in
// hint of account.discover.
func goaAccountID(cfg api.AccountConfig) string {
	switch {
	case cfg.Graph != nil && cfg.Graph.Source == api.GraphSourceGOA:
		return cfg.Graph.GOAAccountID
	case cfg.OAuth2 != nil && cfg.OAuth2.Source == api.OAuth2SourceGOA:
		return cfg.OAuth2.GOAAccountID
	}
	return ""
}

// NeedsBrowserSignIn reports an account that waits for the user to sign in
// again through the backend's own sign-in.
func NeedsBrowserSignIn(a api.Account) bool {
	return a.State.Status == api.SyncAuthRequired && KindOf(a.Config) == OAuth
}

// Path is the way the account assistant continues after account.discover.
type Path int

const (
	// PathPassword: the account needs a password. Config is the
	// discovered IMAP/SMTP account, nil when nothing was found (the
	// servers are guessed).
	PathPassword Path = iota
	// PathGOA: the address is signed in through GNOME Online Accounts;
	// Config is the complete account to test and add.
	PathGOA
	// PathGOAHint: the address belongs to a provider GNOME Online Accounts
	// could sign in, but it is not signed in there yet. OAuthAlt and
	// PasswordAlt are the other ways the backend offers, nil when none.
	PathGOAHint
	// PathOAuth: the backend's own sign-in in the browser with Config;
	// PasswordAlt is the app-password account when the provider has one.
	PathOAuth
)

// Discovery is what ClassifyDiscovery decided. Provider is derived from
// the configuration, never from the backend's untrusted providerName.
type Discovery struct {
	Path        Path
	Config      *api.AccountConfig
	OAuthAlt    *api.AccountConfig
	PasswordAlt *api.AccountConfig
	Provider    string
}

// ClassifyDiscovery turns an account.discover answer into the assistant's
// next step. An error or an answer without a config is the password path
// with guessed servers; a GNOME Online Accounts config is complete with an
// account id and the sign-in hint without one; a config of the backend's
// own sign-in opens the browser page; anything else asks for a password.
// The alternatives are picked in the backend's order of preference.
func ClassifyDiscovery(res api.AccountDiscoverResult, err error) Discovery {
	if err != nil || res.Config == nil {
		return Discovery{Path: PathPassword}
	}
	cfg := *res.Config
	d := Discovery{Config: &cfg, Provider: Provider(cfg)}
	switch KindOf(cfg) {
	case GOA:
		if goaAccountID(cfg) != "" {
			d.Path = PathGOA
			return d
		}
		d.Path = PathGOAHint
		d.OAuthAlt = firstAlternative(res.Alternatives, func(c api.AccountConfig) bool { return KindOf(c) == OAuth })
		d.PasswordAlt = firstAlternative(res.Alternatives, isPasswordAccount)
	case OAuth:
		d.Path = PathOAuth
		d.PasswordAlt = firstAlternative(res.Alternatives, isPasswordAccount)
	default:
		d.Path = PathPassword
	}
	return d
}

// isPasswordAccount reports an IMAP/SMTP account whose endpoints both sign
// in with a password (Google's app-password alternative).
func isPasswordAccount(c api.AccountConfig) bool {
	return KindOf(c) == Password && c.Protocol() == api.AccountIMAP &&
		c.IMAP != nil && c.IMAP.AuthMethod == api.AuthPassword &&
		c.SMTP != nil && c.SMTP.AuthMethod == api.AuthPassword
}

// firstAlternative returns a copy of the first alternative that matches,
// nil when none does.
func firstAlternative(alts []api.AccountConfig, match func(api.AccountConfig) bool) *api.AccountConfig {
	for _, c := range alts {
		if match(c) {
			c := c
			return &c
		}
	}
	return nil
}

// Failure is why a browser sign-in ended without an account.
type Failure int

const (
	// FailureOther: anything else; the caller shows the generic RPC text.
	FailureOther Failure = iota
	// FailureCancelled: access was denied in the browser, or the session
	// was cancelled.
	FailureCancelled
	// FailureRefused: the provider refused the sign-in (authFailed).
	FailureRefused
	// FailureTimeout: the session expired, or the call to the backend
	// timed out.
	FailureTimeout
	// FailureWrongAccount: the browser signed in to another mailbox.
	FailureWrongAccount
)

// ClassifyFailure sorts an account.oauthWait error. signedInAs is the
// mailbox the browser signed in to, set for FailureWrongAccount only.
func ClassifyFailure(err error) (f Failure, signedInAs string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout, ""
	}
	var e *api.Error
	if !errors.As(err, &e) {
		return FailureOther, ""
	}
	switch e.Code {
	case api.CodeCancelled:
		return FailureCancelled, ""
	case api.CodeAuthFailed:
		return FailureRefused, ""
	case api.CodeServerTimeout:
		return FailureTimeout, ""
	case api.CodeInvalidArgument:
		if who := signedInAsOf(e); who != "" {
			return FailureWrongAccount, who
		}
	}
	return FailureOther, ""
}

// maxAddressLen bounds data.signedInAs (RFC 5321 path limit).
const maxAddressLen = 254

// signedInAsOf reads data.signedInAs of an error: a map once the client
// has decoded the JSON. The address comes from the provider through the
// backend, so it is only ever shown as plain text, and anything that is
// not a short single line is dropped, as is one with an invisible format
// character (category Cf, such as U+202E, which reverses what follows).
func signedInAsOf(e *api.Error) string {
	var v any
	switch d := e.Data.(type) {
	case map[string]any:
		v = d["signedInAs"]
	case map[string]string:
		v = d["signedInAs"]
	}
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxAddressLen || !utf8.ValidString(s) || strings.ContainsFunc(s, isHiddenRune) {
		return ""
	}
	return s
}

// isHiddenRune reports a control or format character: nothing an address
// shown to the user may contain.
func isHiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// IsClientMissing reports the oauthClientMissing error of
// account.oauthStart: no OAuth client is configured for the provider.
func IsClientMissing(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == api.CodeOAuthClientMissing
}

// TestNeedsSignIn reports an account.test outcome that only a new sign-in
// can fix: the call or an endpoint failed with authFailed or authRequired.
func TestNeedsSignIn(res api.AccountTestResult, err error) bool {
	var e *api.Error
	if errors.As(err, &e) {
		return isSignInCode(e.Code)
	}
	for _, r := range []*api.EndpointTestResult{res.IMAP, res.SMTP, res.Graph} {
		if r != nil && r.Error != nil && isSignInCode(r.Error.Code) {
			return true
		}
	}
	return false
}

func isSignInCode(c api.ErrorCode) bool {
	return c == api.CodeAuthFailed || c == api.CodeAuthRequired
}

// BrowserURL reports an address the UI may open in the browser for a
// sign-in (account.oauthStart's authUrl, notify.authRequired's authUrl):
// an absolute https URL with a host and no user information.
func BrowserURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Opaque == ""
}
