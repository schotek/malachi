// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"

	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

const googleGOAID = "account_1788683507_0"

// gmailSettings is what GOA's Mail interface says about a Google account.
func gmailSettings() goa.MailSettings {
	return goa.MailSettings{
		IMAPSupported: true, IMAPHost: "imap.gmail.com", IMAPUserName: "me@gmail.invalid", IMAPUseSSL: true,
		SMTPSupported: true, SMTPHost: "smtp.gmail.com", SMTPUserName: "me@gmail.invalid", SMTPUseAuth: true,
		SMTPUseSSL: true, SMTPUseTLS: true, SMTPAuthXOAuth2: true,
	}
}

// gmailConfig is the account goaConfigFor builds for gmailSettings.
func gmailConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Gmail", Email: "me@gmail.invalid", Kind: api.AccountIMAP,
		IMAP:   &api.ServerConfig{Host: "imap.gmail.com", Port: 993, Security: api.SecurityTLS, Username: "me@gmail.invalid", AuthMethod: api.AuthOAuth2},
		SMTP:   &api.ServerConfig{Host: "smtp.gmail.com", Port: 465, Security: api.SecurityTLS, Username: "me@gmail.invalid", AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle, GOAAccountID: googleGOAID},
	}
}

func TestOAuth2GOAValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*api.AccountConfig)
		ok   bool
	}{
		{"valid", func(*api.AccountConfig) {}, true},
		{"id trimmed", func(c *api.AccountConfig) { c.OAuth2.GOAAccountID = " " + googleGOAID + " " }, true},
		{"no oauth2 settings", func(c *api.AccountConfig) { c.OAuth2 = nil }, false},
		{"empty id", func(c *api.AccountConfig) { c.OAuth2.GOAAccountID = "" }, false},
		{"path in id", func(c *api.AccountConfig) { c.OAuth2.GOAAccountID = "../x" }, false},
		{"office365 with goa", func(c *api.AccountConfig) { c.OAuth2.Provider = api.OAuth2ProviderOffice365 }, false},
		{"client id with goa", func(c *api.AccountConfig) { c.OAuth2.ClientID = "abc" }, false},
		{"scopes with goa", func(c *api.AccountConfig) { c.OAuth2.Scopes = []string{"mail"} }, false},
		{"unknown source", func(c *api.AccountConfig) { c.OAuth2.Source = "keyring" }, false},
		{"id without source", func(c *api.AccountConfig) { c.OAuth2.Source = ""; c.OAuth2.Provider = "office365" }, false},
		{"smtp with password", func(c *api.AccountConfig) { c.SMTP.AuthMethod = api.AuthPassword }, false},
		{"imap with password", func(c *api.AccountConfig) { c.IMAP.AuthMethod = api.AuthPassword }, false},
		{"graph given", func(c *api.AccountConfig) {
			c.Graph = &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: goaID}
		}, false},
		// The own flow stays what it was: no source, its own provider.
		{"own flow office365", func(c *api.AccountConfig) {
			c.OAuth2 = &api.OAuth2Config{Provider: api.OAuth2ProviderOffice365}
		}, true},
	}
	for _, tc := range cases {
		c := gmailConfig()
		tc.mut(&c)
		err := validateAccountConfig(&c)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: code %v, want invalidArgument", tc.name, err)
		}
	}
}

func TestGoogleMailConfig(t *testing.T) {
	base := goa.Account{ID: googleGOAID, ProviderType: goa.ProviderGoogle, Email: "Me@Gmail.invalid", Name: " G Mail ", OAuth2: true, Mail: gmailSettings()}
	cfg, name, ok := goaConfigFor(base)
	if !ok || name != "Google" || cfg.DisplayName != "G Mail" || cfg.Email != "Me@Gmail.invalid" || cfg.Name != "Me@Gmail.invalid" {
		t.Fatalf("goaConfigFor = %+v %q %v", cfg, name, ok)
	}
	if cfg.IMAP.Security != api.SecurityTLS || cfg.IMAP.Port != 993 || cfg.SMTP.Security != api.SecurityTLS || cfg.SMTP.Port != 465 ||
		cfg.IMAP.Username != "me@gmail.invalid" || cfg.OAuth2.GOAAccountID != googleGOAID {
		t.Fatalf("servers = %+v %+v %+v", cfg.IMAP, cfg.SMTP, cfg.OAuth2)
	}

	// STARTTLS when that is all GOA offers; nothing for plaintext, for a
	// server without XOAUTH2, or for an account without mail.
	starttls := base
	starttls.Mail.IMAPUseSSL, starttls.Mail.IMAPUseTLS = false, true
	starttls.Mail.SMTPUseSSL = false
	starttls.Mail.IMAPUserName, starttls.Mail.SMTPUserName = "", ""
	if cfg, _, ok := goaConfigFor(starttls); !ok || cfg.IMAP.Port != 143 || cfg.IMAP.Security != api.SecuritySTARTTLS ||
		cfg.SMTP.Port != 587 || cfg.SMTP.Security != api.SecuritySTARTTLS || cfg.IMAP.Username != "Me@Gmail.invalid" {
		t.Fatalf("starttls = %+v %v", cfg, ok)
	}
	for name, mut := range map[string]func(*goa.Account){
		"plaintext imap":   func(a *goa.Account) { a.Mail.IMAPUseSSL, a.Mail.IMAPUseTLS = false, false },
		"plaintext smtp":   func(a *goa.Account) { a.Mail.SMTPUseSSL, a.Mail.SMTPUseTLS = false, false },
		"no xoauth2":       func(a *goa.Account) { a.Mail.SMTPAuthXOAuth2 = false },
		"no imap":          func(a *goa.Account) { a.Mail.IMAPSupported = false },
		"no smtp host":     func(a *goa.Account) { a.Mail.SMTPHost = "" },
		"mail disabled":    func(a *goa.Account) { a.MailDisabled = true },
		"no oauth2":        func(a *goa.Account) { a.OAuth2 = false },
		"unknown provider": func(a *goa.Account) { a.ProviderType = "owncloud" },
	} {
		a := base
		a.Mail = gmailSettings()
		mut(&a)
		if _, _, ok := goaConfigFor(a); ok {
			t.Errorf("%s: account offered", name)
		}
	}
}

func TestCredentialFor(t *testing.T) {
	ctx := context.Background()
	b, _ := newSyncBackend(t)
	fg := &fakeGOA{tokens: map[string]string{googleGOAID: "ya29.token"}}
	b.GOA = fg

	// A password account keeps reading the keyring.
	pw := api.AccountID(seedAccount(t, b, "pw@example.invalid"))
	if got, err := b.credentialFor(ctx, string(pw)); err != nil || got != "pw" {
		t.Fatalf("password account: %q, %v", got, err)
	}

	// A Google account gets its token from GNOME Online Accounts, and the
	// token is what the server refusing it invalidates.
	res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: gmailConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.credentialFor(ctx, string(res.AccountID)); err != nil || got != "ya29.token" {
		t.Fatalf("google account: %q, %v", got, err)
	}
	b.invalidateCredentialsFor(string(res.AccountID))
	b.invalidateCredentialsFor(string(pw))
	if len(fg.invalidated) != 1 || fg.invalidated[0] != googleGOAID {
		t.Fatalf("invalidated = %v", fg.invalidated)
	}

	// A lost sign-in, and no GOA at all.
	fg.tokenErr = api.NewError(api.CodeAuthRequired, "sign in again")
	if _, err := b.credentialFor(ctx, string(res.AccountID)); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("revoked: %v", err)
	}
	b.GOA = nil
	if _, err := b.credentialFor(ctx, string(res.AccountID)); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("no goa: %v", err)
	}
	if _, err := b.credentialFor(ctx, "acc_nope"); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}

	// The own OAuth2 flow is reserved.
	own := gmailConfig()
	own.Email = "own@example.invalid"
	own.OAuth2 = &api.OAuth2Config{Provider: api.OAuth2ProviderOffice365}
	res, err = b.Accounts().Add(ctx, api.AccountAddParams{Config: own})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.credentialFor(ctx, string(res.AccountID)); errCode(t, err) != api.CodeNotImplemented {
		t.Fatalf("own flow: %v", err)
	}
}

func TestAccountTestWithGOAToken(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.GOA = &fakeGOA{tokens: map[string]string{googleGOAID: "ya29.token"}}
	var imapSecret, smtpSecret string
	b.ProbeIMAP = func(_ context.Context, cfg api.ServerConfig, secret string) (imap.ProbeResult, error) {
		imapSecret = secret
		return imap.ProbeResult{Capabilities: []string{"AUTH=XOAUTH2"}}, nil
	}
	b.ProbeSMTP = func(_ context.Context, cfg api.ServerConfig, secret string) (smtp.ProbeResult, error) {
		smtpSecret = secret
		return smtp.ProbeResult{}, nil
	}
	res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: gmailConfig()})
	if err != nil || !res.IMAP.OK || !res.SMTP.OK || imapSecret != "ya29.token" || smtpSecret != "ya29.token" {
		t.Fatalf("test = %+v, %v (secrets %q %q)", res, err, imapSecret, smtpSecret)
	}
	if _, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: gmailConfig(), Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("password for an oauth2 account: %v", err)
	}

	// Without a sign-in both endpoints report it; nothing is probed.
	imapSecret, smtpSecret = "", ""
	b.GOA = &fakeGOA{tokenErr: api.NewError(api.CodeAuthRequired, "sign in again")}
	res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: gmailConfig()})
	if err != nil || res.IMAP.OK || res.SMTP.OK || res.IMAP.Error.Code != api.CodeAuthRequired || res.SMTP.Error.Code != api.CodeAuthRequired ||
		imapSecret != "" || smtpSecret != "" {
		t.Fatalf("without token = %+v, %v", res, err)
	}
}

func TestServerFilesSentCopy(t *testing.T) {
	if !serverFilesSentCopy(graphConfig()) {
		t.Error("graph account does not file its own copy")
	}
	if !serverFilesSentCopy(gmailConfig()) {
		t.Error("gmail account does not file its own copy")
	}
	other := gmailConfig()
	other.SMTP.Host = "SMTP.GOOGLEMAIL.COM "
	if !serverFilesSentCopy(other) {
		t.Error("googlemail host not recognised")
	}
	if serverFilesSentCopy(validConfig()) {
		t.Error("a plain IMAP account files its own copy")
	}
}
