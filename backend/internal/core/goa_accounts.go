// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"strings"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Accounts that sign in through GNOME Online Accounts: how a GOA account
// becomes an AccountConfig (account.linked, account.discover) and how the
// sync engine and the outbox get their credentials. Microsoft 365 goes
// through Microsoft Graph, since its token has no IMAP scope; Google goes
// through IMAP and SMTP with SASL XOAUTH2, the servers named by GOA's
// Mail interface. In both cases GOA owns the sign-in, the refresh token
// and the OAuth client id; the daemon only ever holds an access token in
// memory.

// providerOf maps a GOA provider type to the provider name of the
// contract (LinkedAccount.provider) and its display name; "" for a
// provider Malachi does not use.
func providerOf(providerType string) (provider, name string) {
	switch providerType {
	case goa.ProviderMicrosoft365:
		return "microsoft365", discover.MicrosoftProviderName
	case goa.ProviderGoogle:
		return api.OAuth2ProviderGoogle, discover.GoogleProviderName
	}
	return "", ""
}

// goaConfigFor builds the account a GOA account can be added as, complete
// and without credentials. ok is false for a provider Malachi does not
// use, an account without OAuth2 or with mail disabled, or a Google
// account whose Mail interface names no usable servers. The e-mail is
// the Mail address, else the identity; the caller validates it.
func goaConfigFor(a goa.Account) (cfg *api.AccountConfig, providerName string, ok bool) {
	if !a.OAuth2 || a.MailDisabled {
		return nil, "", false
	}
	email := strings.TrimSpace(a.Email)
	if email == "" {
		email = strings.TrimSpace(a.Identity)
	}
	switch a.ProviderType {
	case goa.ProviderMicrosoft365:
		cfg = &api.AccountConfig{
			Name: email, Email: email, Kind: api.AccountGraph,
			Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: a.ID},
		}
	case goa.ProviderGoogle:
		if cfg = googleMailConfig(a, email); cfg == nil {
			return nil, "", false
		}
	default:
		return nil, "", false
	}
	cfg.DisplayName = cleanDisplayName(a.Name)
	_, providerName = providerOf(a.ProviderType)
	return cfg, providerName, true
}

// googleMailConfig is the IMAP account of a Google account: the servers
// GOA's Mail interface names, oauth2 on both endpoints, the token from
// GOA. nil when the interface offers no IMAP, no SMTP, no XOAUTH2, or
// only plaintext — a token is not something to send in the clear.
func googleMailConfig(a goa.Account, email string) *api.AccountConfig {
	m := a.Mail
	if !m.IMAPSupported || !m.SMTPSupported || !m.SMTPAuthXOAuth2 || m.IMAPHost == "" || m.SMTPHost == "" {
		return nil
	}
	imapCfg := &api.ServerConfig{Host: m.IMAPHost, Username: userOr(m.IMAPUserName, email), AuthMethod: api.AuthOAuth2}
	switch {
	case m.IMAPUseSSL:
		imapCfg.Port, imapCfg.Security = 993, api.SecurityTLS
	case m.IMAPUseTLS:
		imapCfg.Port, imapCfg.Security = 143, api.SecuritySTARTTLS
	default:
		return nil
	}
	smtpCfg := &api.ServerConfig{Host: m.SMTPHost, Username: userOr(m.SMTPUserName, email), AuthMethod: api.AuthOAuth2}
	switch {
	case m.SMTPUseSSL:
		smtpCfg.Port, smtpCfg.Security = 465, api.SecurityTLS
	case m.SMTPUseTLS:
		smtpCfg.Port, smtpCfg.Security = 587, api.SecuritySTARTTLS
	default:
		return nil
	}
	return &api.AccountConfig{
		Name: email, Email: email, Kind: api.AccountIMAP,
		IMAP: imapCfg, SMTP: smtpCfg,
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle, GOAAccountID: a.ID},
	}
}

func userOr(username, fallback string) string {
	if u := strings.TrimSpace(username); u != "" {
		return u
	}
	return fallback
}

// goaAccountFor finds the address among the accounts of GNOME Online
// Accounts that Malachi can use, for account.discover, and returns the
// account they would be added as. A missing or failing GOA is simply no
// match.
func (b *Backend) goaAccountFor(ctx context.Context, email string) (*api.AccountConfig, string, bool) {
	if b.GOA == nil {
		return nil, "", false
	}
	accounts, err := b.GOA.Accounts(ctx)
	if err != nil {
		b.log.Debug("gnome online accounts lookup", "err", err)
		return nil, "", false
	}
	want := store.NormalizeAddress(email)
	for _, a := range accounts {
		cfg, name, ok := goaConfigFor(a)
		if ok && store.NormalizeAddress(cfg.Email) == want {
			return cfg, name, true
		}
	}
	return nil, "", false
}

// credentialFor is what the IMAP syncer and the outbox worker sign in
// with: the stored password, or — for an account whose endpoints use
// oauth2 through GNOME Online Accounts — an access token from GOA, which
// the clients present as SASL XOAUTH2. An own OAuth2 flow is reserved
// and reports notImplemented.
func (b *Backend) credentialFor(ctx context.Context, accountID string) (string, error) {
	a, err := b.requireAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	if !usesAuth(a.Config, api.AuthOAuth2) {
		return b.PasswordFor(ctx, accountID)
	}
	return b.oauth2Token(ctx, a.Config)
}

// oauth2Token fetches the access token of an account whose oauth2
// endpoints sign in through GNOME Online Accounts. No GOA is unavailable;
// a lost sign-in comes back from GOA as authRequired.
func (b *Backend) oauth2Token(ctx context.Context, cfg api.AccountConfig) (string, error) {
	o := cfg.OAuth2
	if o == nil || o.Source != api.OAuth2SourceGOA {
		return "", api.ErrNotImplemented
	}
	if b.GOA == nil {
		return "", api.NewError(api.CodeUnavailable, "GNOME Online Accounts is not available")
	}
	token, _, err := b.GOA.AccessToken(ctx, o.GOAAccountID)
	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) {
			return "", apiErr
		}
		return "", api.NewError(api.CodeServerError, "%v", err)
	}
	return token, nil
}

// invalidateCredentialsFor drops the cached access token of an account
// after a server refused it, so the next attempt asks GNOME Online
// Accounts again (which refreshes on its side). A password has nothing to
// drop.
func (b *Backend) invalidateCredentialsFor(accountID string) {
	a, err := b.store.GetAccount(context.Background(), accountID)
	if err != nil {
		return
	}
	if o := a.Config.OAuth2; o != nil && o.Source == api.OAuth2SourceGOA && b.GOA != nil {
		b.GOA.Invalidate(o.GOAAccountID)
	}
}

// serverFilesSentCopy says the delivery path leaves a copy in Sent on its
// own: Graph's sendMail does, and so does Gmail's SMTP, which files what
// it relays under the account's Sent Mail label. Appending another copy
// would show every sent message twice.
func serverFilesSentCopy(cfg api.AccountConfig) bool {
	if cfg.Protocol() == api.AccountGraph {
		return true
	}
	if cfg.SMTP == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SMTP.Host)) {
	case "smtp.gmail.com", "smtp.googlemail.com":
		return true
	}
	return false
}
