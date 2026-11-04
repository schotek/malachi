// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/eway"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// eWay-CRM accounts (docs/eway.md D1–D5, D21): validation of the kind,
// account.detectEway and the eway part of account.add and account.test.
// The password is Credentials.Password in the keyring (auth.KeyPassword)
// and goes, as its MD5 hex, only to LogIn of the account's own service
// URL (internal/eway).

// metaEwayClientID is the random clientMachineIdentifier of this daemon
// store (D5), created on first use.
const metaEwayClientID = "eway.clientId"

// validateEway checks an account of kind eway: the eway block and nothing
// of mail, issue trackers or calendars. The service URL is normalised in
// place. Every failure is invalidArgument.
func validateEway(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if c.IMAP != nil || c.SMTP != nil || c.OAuth2 != nil || c.Graph != nil || c.Jira != nil || c.CalDAV != nil || c.Calendar != nil {
		return bad("imap, smtp, oauth2, graph, jira, caldav and calendar settings do not apply to an eway account")
	}
	e := c.Eway
	if e == nil {
		return bad("eway settings are required")
	}
	svc, err := eway.NormaliseServiceURL(e.ServiceURL)
	if err != nil {
		return err
	}
	e.ServiceURL = svc
	e.Username = strings.TrimSpace(e.Username)
	switch {
	case e.Username == "":
		return bad("eway: username is required")
	case len(e.Username) > maxUsernameBytes || !utf8.ValidString(e.Username) || hasControl(e.Username):
		return bad("eway: username must be valid UTF-8 without control characters (limit %d bytes)", maxUsernameBytes)
	}
	e.UserGUID = strings.ToLower(strings.TrimSpace(e.UserGUID))
	if e.UserGUID != "" && !eway.ValidGUID(e.UserGUID) {
		return bad("eway: userGuid is not a GUID")
	}
	return nil
}

// isCrmAccount: an account of kind eway (no mail, no folders, no board).
func isCrmAccount(a store.Account) bool { return a.Config.Protocol() == api.AccountEway }

// hasNoMail: an account of a kind without mail (caldav, eway).
func hasNoMail(c api.AccountConfig) bool {
	k := c.Protocol()
	return k == api.AccountCalDAV || k == api.AccountEway
}

// sameEwayLogin: the same instance (realm) and user name.
func sameEwayLogin(a, b api.AccountConfig) bool {
	return a.Eway != nil && b.Eway != nil && store.RealmOf(a) == store.RealmOf(b) && a.Eway.Username == b.Eway.Username
}

// ewaySecretMoves reports an update after which the stored password would
// go to another instance or user: such an update needs its own password.
func ewaySecretMoves(old, next api.AccountConfig) bool {
	switch {
	case next.Protocol() == api.AccountEway:
		return old.Protocol() != api.AccountEway || !sameEwayLogin(old, next)
	case old.Protocol() == api.AccountEway:
		return usesAuth(next, api.AuthPassword) || next.Protocol() == api.AccountJira || next.Protocol() == api.AccountCalDAV
	}
	return false
}

// ewayTransport carries eWay-CRM requests through Backend.EwayHTTP when a
// test set one, else the default transport; read per request.
type ewayTransport struct{ b *Backend }

func (t ewayTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if c := t.b.EwayHTTP; c != nil && c.Transport != nil {
		return c.Transport.RoundTrip(r)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (b *Backend) ewayHTTP() *http.Client { return &http.Client{Transport: ewayTransport{b}} }

// ewayClientID is this store's clientMachineIdentifier, created on first
// use (D5); "" when the store cannot keep it (the trial accepts that).
func (b *Backend) ewayClientID(ctx context.Context) string {
	if v, ok, err := b.store.GetMeta(ctx, metaEwayClientID); err == nil && ok && v != "" {
		return v
	}
	var r [16]byte
	_, _ = rand.Read(r[:])
	id := hex.EncodeToString(r[:])
	if err := b.store.SetMeta(ctx, metaEwayClientID, id); err != nil {
		b.log.Warn("eway client id not stored", "err", err)
	}
	return id
}

// newEwayClient is a client for a configuration that is not (yet) a
// running account: account.add, account.test, account.detectEway.
func (b *Backend) newEwayClient(ctx context.Context, cfg api.EwayConfig, password string) (*eway.Client, error) {
	var pw func(context.Context) (string, error)
	if password != "" {
		pw = func(context.Context) (string, error) { return password, nil }
	}
	return eway.NewClient(eway.Options{
		ServiceURL: cfg.ServiceURL,
		Username:   cfg.Username,
		Password:   pw,
		ClientID:   b.ewayClientID(ctx),
		HTTP:       b.ewayHTTP(),
		UserAgent:  eway.UserAgent(b.Version),
		Log:        b.log.With("component", "eway"),
	})
}

// ewaySignInUnsupported is error 1205 for a Microsoft sign-in.
func ewayMicrosoftSignIn() *api.Error {
	return &api.Error{Code: api.CodeCrmSignInUnsupported, Message: "eway: the instance signs in with a Microsoft account",
		Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInMicrosoft}}
}

// prepareEway completes an eway configuration before account.add stores
// it, signing in once with the password: an instance that signs in
// through Microsoft is crmSignInUnsupported before the password is sent;
// the user's GUID is filled, and the e-mail when the client left it empty
// (the user record's, else the user name when it is an address; with
// neither it stays empty — an eway account needs no address,
// validateAccountConfig).
func (b *Backend) prepareEway(ctx context.Context, cfg *api.AccountConfig, password string) error {
	if cfg.Eway == nil {
		return api.NewError(api.CodeInvalidArgument, "eway settings are required")
	}
	svc, err := eway.NormaliseServiceURL(cfg.Eway.ServiceURL)
	if err != nil {
		return err
	}
	cfg.Eway.ServiceURL = svc
	cfg.Eway.Username = strings.TrimSpace(cfg.Eway.Username)
	c, err := b.newEwayClient(ctx, *cfg.Eway, password)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s, err := c.DetectAuth(ctx); err != nil {
		return err
	} else if s.AzureAuth {
		return ewayMicrosoftSignIn()
	}
	u, err := c.SignIn(ctx)
	if err != nil {
		return err
	}
	defer c.LogOut(context.WithoutCancel(ctx))
	cfg.Eway.UserGUID = u.GUID
	if strings.TrimSpace(cfg.Email) == "" {
		switch {
		case u.Email != "":
			cfg.Email = u.Email
		case eway.CleanEmail(cfg.Eway.Username) != "":
			cfg.Email = cfg.Eway.Username
		}
	}
	return nil
}

// DetectEway is account.detectEway: the normalised address and whether
// the instance signs in through Microsoft, asked anonymously (15 s).
func (s *accountService) DetectEway(ctx context.Context, p api.AccountDetectEwayParams) (*api.AccountDetectEwayResult, error) {
	svc, err := eway.NormaliseServiceURL(p.ServiceURL)
	if err != nil {
		return nil, err
	}
	c, err := s.b.newEwayClient(ctx, api.EwayConfig{ServiceURL: svc}, "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := c.DetectAuth(ctx)
	if err != nil {
		s.b.log.Info("eway detection", "ok", false, "code", eway.ToAPIError(err).Code.String())
		return nil, eway.ToAPIError(err)
	}
	s.b.log.Info("eway detection", "ok", true, "known", st.Known, "microsoft", st.AzureAuth)
	return &api.AccountDetectEwayResult{ServiceURL: svc, MicrosoftSignIn: st.AzureAuth, Version: st.WcfVersion}, nil
}

// testEway is account.test for an eway account: one sign-in with the
// password given, or with accountId the stored one (only for an eway
// account of the same instance and user).
func (s *accountService) testEway(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	cfg := *p.Config.Eway
	password := p.Credentials.Password
	if password == "" {
		if p.AccountID == "" {
			return &api.AccountTestResult{Eway: endpointResult(nil, 0, api.NewError(api.CodeAuthRequired, "no password given and no accountId"))}, nil
		}
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if a.Config.Protocol() != api.AccountEway || !sameEwayLogin(a.Config, p.Config) {
			return nil, api.NewError(api.CodeInvalidArgument, "the stored password belongs to another instance or user")
		}
		pw, err := s.b.PasswordFor(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		password = pw
	}
	c, err := s.b.newEwayClient(ctx, cfg, password)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	start := time.Now()
	res := func() *api.EndpointTestResult {
		st, err := c.DetectAuth(ctx)
		if err != nil {
			return endpointResult(nil, 0, eway.ToAPIError(err))
		}
		if st.AzureAuth {
			return endpointResult(nil, 0, ewayMicrosoftSignIn())
		}
		if _, err := c.SignIn(ctx); err != nil {
			return endpointResult(nil, 0, eway.ToAPIError(err))
		}
		c.LogOut(context.WithoutCancel(ctx))
		return endpointResult(nil, time.Since(start), nil)
	}()
	code := ""
	if res.Error != nil {
		code = res.Error.Code.String()
	}
	s.b.log.Info("account test", "kind", "eway", "ok", res.OK, "code", code)
	return &api.AccountTestResult{Eway: res}, nil
}
