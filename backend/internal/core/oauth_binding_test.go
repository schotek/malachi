// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/account"
	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

// What binds a sign-in of the backend's own flow to its account: the
// servers a Google token may go to, the address, provider and client a
// stored or completed sign-in belongs to, and the keyring writes around
// changes of the account.

func (n *authRec) snapshot() []api.AuthRequiredNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.AuthRequiredNotification(nil), n.auth...)
}

// withAddress is cfg moved to another address (user names included).
func withAddress(cfg api.AccountConfig, email string) api.AccountConfig {
	c := *cloneConfig(cfg)
	c.Email = email
	if c.IMAP != nil {
		c.IMAP.Username = email
	}
	if c.SMTP != nil {
		c.SMTP.Username = email
	}
	return c
}

// probeOK makes account.test's IMAP/SMTP/Graph probes succeed without a
// network.
func (f *oauthFixture) probeOK(mailbox string) {
	f.b.ProbeIMAP = func(context.Context, api.ServerConfig, string) (imap.ProbeResult, error) {
		return imap.ProbeResult{}, nil
	}
	f.b.ProbeSMTP = func(context.Context, api.ServerConfig, string) (smtp.ProbeResult, error) {
		return smtp.ProbeResult{}, nil
	}
	f.b.ProbeGraph = func(context.Context, string) (graph.ProbeResult, error) {
		return graph.ProbeResult{Email: mailbox, Capabilities: []string{"graph"}}, nil
	}
}

func TestDaemonGoogleEndpointsPinned(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*api.AccountConfig)
		ok   bool
	}{
		{"gmail, smtp 465 tls", func(*api.AccountConfig) {}, true},
		{"gmail, smtp 587 starttls", func(c *api.AccountConfig) { c.SMTP.Port, c.SMTP.Security = 587, api.SecuritySTARTTLS }, true},
		{"upper-case hosts and user names", func(c *api.AccountConfig) {
			c.IMAP.Host, c.SMTP.Host, c.IMAP.Username = "IMAP.Gmail.com", "SMTP.GMAIL.COM", "ME@Gmail.invalid"
		}, true},
		{"other imap host", func(c *api.AccountConfig) { c.IMAP.Host = "imap.example.invalid" }, false},
		{"imap look-alike", func(c *api.AccountConfig) { c.IMAP.Host = "imap.gmail.com.example.invalid" }, false},
		{"imap trailing dot", func(c *api.AccountConfig) { c.IMAP.Host = "imap.gmail.com." }, false},
		{"imap ip literal", func(c *api.AccountConfig) { c.IMAP.Host = "142.250.0.108" }, false},
		{"imap port 143", func(c *api.AccountConfig) { c.IMAP.Port, c.IMAP.Security = 143, api.SecuritySTARTTLS }, false},
		{"imap starttls on 993", func(c *api.AccountConfig) { c.IMAP.Security = api.SecuritySTARTTLS }, false},
		{"smtp as the imap host", func(c *api.AccountConfig) { c.IMAP.Host = "smtp.gmail.com" }, false},
		{"other smtp host", func(c *api.AccountConfig) { c.SMTP.Host = "smtp.example.invalid" }, false},
		{"smtp 587 tls", func(c *api.AccountConfig) { c.SMTP.Port = 587 }, false},
		{"smtp 465 starttls", func(c *api.AccountConfig) { c.SMTP.Security = api.SecuritySTARTTLS }, false},
		{"smtp 25", func(c *api.AccountConfig) { c.SMTP.Port, c.SMTP.Security = 25, api.SecuritySTARTTLS }, false},
		{"imap user name", func(c *api.AccountConfig) { c.IMAP.Username = "someone@gmail.invalid" }, false},
		{"smtp user name", func(c *api.AccountConfig) { c.SMTP.Username = "me" }, false},
	}
	for _, tc := range cases {
		c := googleDaemonConfig()
		tc.mod(&c)
		err := validateAccountConfig(&c)
		switch {
		case tc.ok && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.ok && (c.IMAP.Host != gmailIMAPHost || c.SMTP.Host != gmailSMTPHost):
			t.Errorf("%s: hosts not canonical: %s %s", tc.name, c.IMAP.Host, c.SMTP.Host)
		case !tc.ok && errCode(t, err) != api.CodeInvalidArgument:
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// GNOME Online Accounts names its own servers: not pinned here.
	c := gmailConfig()
	c.IMAP.Host = "imap.example.invalid"
	if err := validateAccountConfig(&c); err != nil {
		t.Errorf("goa account: %v", err)
	}

	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	f.probeOK("")
	acc := f.b.Accounts()
	session := f.signIn(t, googleDaemonConfig())
	evil := googleDaemonConfig()
	evil.IMAP.Host = "imap.evil.invalid"
	for name, err := range map[string]error{
		"add": func() error { _, err := acc.Add(ctx, api.AccountAddParams{Config: evil}); return err }(),
		"add session": func() error {
			_, err := acc.Add(ctx, api.AccountAddParams{Config: evil, Credentials: api.Credentials{OAuthSession: session}})
			return err
		}(),
		"test": func() error {
			_, err := acc.Test(ctx, api.AccountTestParams{Config: evil, Credentials: api.Credentials{OAuthSession: session}})
			return err
		}(),
		"oauthStart": func() error { _, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &evil}); return err }(),
	} {
		if errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if list := listIDs(t, acc); len(list) != 0 {
		t.Fatalf("rows: %v", list)
	}
	added, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig(), Credentials: api.Credentials{OAuthSession: session}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: evil}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("update: %v", err)
	}
	if a, _ := f.b.store.GetAccount(ctx, string(added.AccountID)); a.Config.IMAP.Host != gmailIMAPHost {
		t.Fatalf("update stored %s", a.Config.IMAP.Host)
	}

	// config.toml: the entry naming another server is skipped.
	toml := config.Default()
	server := func(host string, port int) *account.Server {
		return &account.Server{Host: host, Port: port, Security: "tls", Username: "", AuthMethod: "oauth2"}
	}
	entry := func(email, imapHost string) account.Config {
		e := account.Config{Name: "Gmail", Email: email, IMAP: server(imapHost, 993), SMTP: server("smtp.gmail.com", 465),
			OAuth2: &account.OAuth2{Source: "daemon", Provider: "google"}}
		e.IMAP.Username, e.SMTP.Username = email, email
		return e
	}
	toml.Accounts = []account.Config{entry("evil@gmail.invalid", "imap.evil.invalid"), entry("good@gmail.invalid", "imap.gmail.com")}
	b := newTestBackend(t, toml)
	if err := b.ImportConfigAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := b.Accounts().List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 1 || list.Accounts[0].Config.Email != "good@gmail.invalid" {
		t.Fatalf("imported %+v", list.Accounts)
	}
}

func TestOAuthUpdateRebindsSignIn(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	rec := &authRec{}
	notifier := outboxAwareNotifier{b: f.b, inner: rec}
	cfg := googleDaemonConfig()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: f.signIn(t, cfg)}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	stored := func() string { return f.k.value(id, auth.KeyRefreshToken) }

	// A new name keeps the sign-in.
	renamed := googleDaemonConfig()
	renamed.Name = "Renamed"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: renamed}); err != nil {
		t.Fatal(err)
	}
	if stored() == "" {
		t.Fatal("a rename dropped the sign-in")
	}
	if _, _, ok := f.b.OAuth.Pending(string(id)); ok {
		t.Fatal("a rename opened a re-sign-in")
	}
	if _, err := f.b.credentialFor(ctx, string(id)); err != nil {
		t.Fatalf("credential after a rename: %v", err)
	}

	// A new address without a sign-in: the token goes, a session for the
	// new address waits and the engines' authRequired carries it.
	moved := withAddress(cfg, "new@gmail.invalid")
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: moved}); err != nil {
		t.Fatal(err)
	}
	if v := stored(); v != "" {
		t.Fatalf("refresh token kept after the address changed: %q", v)
	}
	sid, authURL, ok := f.b.OAuth.Pending(string(id))
	if !ok {
		t.Fatal("no re-sign-in after the address changed")
	}
	if u, _ := url.Parse(authURL); u.Query().Get("login_hint") != "new@gmail.invalid" {
		t.Fatalf("re-sign-in for %q", u.Query().Get("login_hint"))
	}
	if _, err := f.b.credentialFor(ctx, string(id)); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("credential after the address changed: %v", err)
	}
	notifier.AuthRequired(api.AuthRequiredNotification{AccountID: id, Reason: api.CodeAuthRequired})
	if got := rec.snapshot(); len(got) != 1 || got[0].AuthURL != authURL {
		t.Fatalf("notification %+v", got)
	}

	// A new client (the account's own): the same, and the session waiting
	// for the old client is replaced.
	f.k.Set(ctx, id, auth.KeyRefreshToken, "refresh-planted")
	own := moved
	own.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderGoogle, ClientID: "own-client.apps.example"}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: own}); err != nil {
		t.Fatal(err)
	}
	if v := stored(); v != "" {
		t.Fatalf("refresh token kept after the client changed: %q", v)
	}
	sid2, authURL2, ok := f.b.OAuth.Pending(string(id))
	if !ok || sid2 == sid {
		t.Fatalf("re-sign-in after the client changed: %s %v", sid2, ok)
	}
	if u, _ := url.Parse(authURL2); u.Query().Get("client_id") != "own-client.apps.example" {
		t.Fatalf("re-sign-in with client %q", u.Query().Get("client_id"))
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: sid}); errCode(t, err) != api.CodeCancelled {
		t.Fatalf("old session: %v", err)
	}
}

func TestOAuthUpdateRebindsOnTenant(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testMicrosoftClient)
	f.probeOK("me@contoso.invalid")
	acc := f.b.Accounts()
	cfg := graphDaemonConfig()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: f.signIn(t, cfg)}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	// "common" is what no tenant means: the same client.
	common := graphDaemonConfig()
	common.OAuth2.TenantID = "Common"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: common}); err != nil {
		t.Fatal(err)
	}
	if f.k.value(id, auth.KeyRefreshToken) == "" {
		t.Fatal("tenant common dropped the sign-in")
	}
	contoso := graphDaemonConfig()
	contoso.OAuth2.TenantID = "contoso.onmicrosoft.com"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: contoso}); err != nil {
		t.Fatal(err)
	}
	if f.k.value(id, auth.KeyRefreshToken) != "" {
		t.Fatal("refresh token kept after the tenant changed")
	}
	if _, _, ok := f.b.OAuth.Pending(string(id)); !ok {
		t.Fatal("no re-sign-in after the tenant changed")
	}
}

func TestOAuthReauthHookRefusesMisfits(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	hook := f.b.onReauthComplete(id)
	good := oauth2flow.Grant{Provider: oauth2flow.Google, ClientID: testGoogleClient, AccessToken: "access-hook",
		RefreshToken: "refresh-hook", Expiry: time.Now().Add(time.Hour), Email: "Me@Gmail.invalid"}
	f.sup.reset()
	for name, mod := range map[string]func(*oauth2flow.Grant){
		"another address":  func(g *oauth2flow.Grant) { g.Email = "else@gmail.invalid" },
		"no address":       func(g *oauth2flow.Grant) { g.Email = "" },
		"another client":   func(g *oauth2flow.Grant) { g.ClientID = "other-client" },
		"another tenant":   func(g *oauth2flow.Grant) { g.Tenant = "contoso" },
		"another provider": func(g *oauth2flow.Grant) { g.Provider = oauth2flow.Microsoft },
	} {
		g := good
		mod(&g)
		if err := hook(g); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if v := f.k.value(added.AccountID, auth.KeyRefreshToken); v != "" || f.sup.count("restart:") != 0 {
		t.Fatalf("a misfit was stored (%q) or restarted the account", v)
	}
	if err := hook(good); err != nil {
		t.Fatal(err)
	}
	if f.k.value(added.AccountID, auth.KeyRefreshToken) != "refresh-hook" || f.sup.count("restart:") != 1 {
		t.Fatal("the fitting grant was not stored")
	}
	if _, err := acc.Remove(ctx, api.AccountRemoveParams{AccountID: added.AccountID}); err != nil {
		t.Fatal(err)
	}
	if err := hook(good); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("after remove: %v", err)
	}
	if v := f.k.value(added.AccountID, auth.KeyRefreshToken); v != "" {
		t.Fatalf("token stored for a removed account: %q", v)
	}
}

func TestOAuthPendingReauthReplacedWhenTheAccountChanged(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	sid, _, ok := f.b.OAuth.Pending(id)
	if !ok {
		t.Fatal("no re-sign-in")
	}
	// The stored configuration changes behind the waiting session (as a
	// race would leave it): the next start must not hand out the old one.
	a, _ := f.b.store.GetAccount(ctx, id)
	a.Config.OAuth2.ClientID = "own-client.apps.example"
	if err := f.b.store.UpdateAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{AccountID: added.AccountID,
		BrowserPage: &api.OAuthBrowserPage{FailureTitle: "Nezdar"}})
	if err != nil || start.SessionID == sid {
		t.Fatalf("start = %+v, %v", start, err)
	}
	if u, _ := url.Parse(start.AuthURL); u.Query().Get("client_id") != "own-client.apps.example" {
		t.Fatalf("new session's client %q", u.Query().Get("client_id"))
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: sid}); errCode(t, err) != api.CodeCancelled {
		t.Fatalf("old session: %v", err)
	}
	// The backend's own ensure keeps a fitting session and its texts.
	f.b.ensureReauthSession(id)
	if again, _, _ := f.b.OAuth.Pending(id); again != start.SessionID {
		t.Fatalf("ensure replaced a fitting session: %s", again)
	}
	if status, page := browse(t, start.AuthURL); status != http.StatusBadRequest || !strings.Contains(page, "Nezdar") {
		t.Fatalf("page %d: %s", status, page)
	}
}

func TestOAuthSessionBinding(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	f.probeOK("")
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	a1, err := acc.Add(ctx, api.AccountAddParams{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := acc.Add(ctx, api.AccountAddParams{Config: withAddress(cfg, "two@gmail.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	reauth, authURL, _ := f.b.OAuth.Pending(string(a1.AccountID))
	if status, _ := browse(t, authURL); status != http.StatusOK {
		t.Fatal("re-sign-in failed")
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: reauth}); err != nil {
		t.Fatal(err)
	}
	creds := api.Credentials{OAuthSession: reauth}
	// A re-sign-in serves its own account only.
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: withAddress(cfg, "me@gmail.invalid"), Credentials: creds}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("add with a re-sign-in: %v", err)
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: a2.AccountID, Config: cfg, Credentials: creds}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("update of another account: %v", err)
	}
	for name, id := range map[string]api.AccountID{"no account": "", "another account": a2.AccountID} {
		if _, err := acc.Test(ctx, api.AccountTestParams{AccountID: id, Config: cfg, Credentials: creds}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("test, %s: %v", name, err)
		}
	}
	if res, err := acc.Test(ctx, api.AccountTestParams{AccountID: a1.AccountID, Config: cfg, Credentials: creds}); err != nil || !res.IMAP.OK {
		t.Fatalf("test of its own account: %+v, %v", res, err)
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: a1.AccountID, Config: cfg, Credentials: creds}); err != nil {
		t.Fatalf("update of its own account: %v", err)
	}

	// A sign-in for the configured client does not fit an account with
	// its own client.
	three := withAddress(cfg, "three@gmail.invalid")
	f.fp.email = three.Email
	session := f.signIn(t, three)
	three.OAuth2.ClientID = "own-client.apps.example"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: three, Credentials: api.Credentials{OAuthSession: session}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("add for another client: %v", err)
	}

	// A grant that names no mailbox never fits, whatever was asked for.
	f.b.ProbeGraph = func(context.Context, string) (graph.ProbeResult, error) { return graph.ProbeResult{}, nil }
	gcfg := graphDaemonConfig()
	sid, u, _, err := f.b.OAuth.Start(oauth2flow.StartRequest{Provider: oauth2flow.Microsoft, Client: oauth2flow.Client{ID: testGoogleClient}})
	if err != nil {
		t.Fatal(err)
	}
	f.b.rememberSession(sid, oauthSession{config: &gcfg})
	browse(t, u)
	if out, _ := f.b.OAuth.Lookup(sid); out.Status != oauth2flow.StatusComplete || out.Email != "" {
		t.Fatalf("anonymous session %+v", out)
	}
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: gcfg, Credentials: api.Credentials{OAuthSession: sid}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("add with an anonymous sign-in: %v", err)
	}
}

func TestOAuthCancelDiscardsACompletedSession(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	session := f.signIn(t, cfg)
	if _, err := acc.OAuthCancel(ctx, api.AccountOAuthCancelParams{SessionID: session}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: session}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("wait after cancel: %v", err)
	}
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("add after cancel: %v", err)
	}
	if _, ok := f.b.sessionInfo(session); ok {
		t.Fatal("the backend still remembers the session")
	}
}

func TestOAuthReauthRefusedByDisabledKeyring(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	rec := &authRec{}
	f.b.SetNotifier(rec)
	added, err := f.b.Accounts().Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	sid, authURL, _ := f.b.OAuth.Pending(string(added.AccountID))
	f.b.Keyring = auth.UnavailableKeyring{}
	f.sup.reset()
	if status, page := browse(t, authURL); status != http.StatusBadRequest || !strings.Contains(page, "keyringError") {
		t.Fatalf("page %d: %s", status, page)
	}
	if _, err := f.b.Accounts().OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: sid}); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("wait: %v", err)
	}
	if f.sup.count("restart:") != 0 {
		t.Fatal("an account without a stored sign-in was restarted")
	}
	if _, err := f.b.credentialFor(ctx, string(added.AccountID)); err == nil {
		t.Fatal("the refused sign-in is usable")
	}
	waitFor(t, "a keyringError notification", func() bool {
		for _, n := range rec.snapshot() {
			if n.AccountID == added.AccountID && n.Reason == api.CodeKeyringError && n.AuthURL == "" {
				return true
			}
		}
		return false
	})
}

func TestOAuthReauthKeptInMemoryIsReported(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	rec := &authRec{}
	f.b.SetNotifier(rec)
	added, err := f.b.Accounts().Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	_, authURL, _ := f.b.OAuth.Pending(string(added.AccountID))
	f.k.setRefuse(true)
	if status, _ := browse(t, authURL); status != http.StatusOK {
		t.Fatalf("page %d", status)
	}
	if _, err := f.b.credentialFor(ctx, string(added.AccountID)); err != nil {
		t.Fatalf("in-memory sign-in: %v", err)
	}
	waitFor(t, "a keyringError notification", func() bool {
		for _, n := range rec.snapshot() {
			if n.AccountID == added.AccountID && n.Reason == api.CodeKeyringError && n.AuthURL == "" {
				return true
			}
		}
		return false
	})
}

func TestOAuthRotationRacesAccountChanges(t *testing.T) {
	for _, op := range []string{"update", "remove"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			f := newOAuthBackend(t, testGoogleClient)
			acc := f.b.Accounts()
			cfg := googleDaemonConfig()
			added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: f.signIn(t, cfg)}})
			if err != nil {
				t.Fatal(err)
			}
			id := string(added.AccountID)
			gate := make(chan struct{})
			f.fp.mu.Lock()
			f.fp.rotate, f.fp.gate = true, gate
			f.fp.mu.Unlock()
			f.b.invalidateCredentialsFor(id)
			done := make(chan error, 1)
			go func() {
				_, err := f.b.credentialFor(ctx, id)
				done <- err
			}()
			waitFor(t, "the refresh", func() bool { _, n := f.fp.counts(); return n >= 1 })
			switch op {
			case "update":
				_, err = acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: withAddress(cfg, "new@gmail.invalid")})
			case "remove":
				_, err = acc.Remove(ctx, api.AccountRemoveParams{AccountID: added.AccountID})
			}
			if err != nil {
				t.Fatal(err)
			}
			close(gate)
			if err := <-done; err != nil {
				t.Fatalf("the refresh in flight: %v", err)
			}
			if v := f.k.value(added.AccountID, auth.KeyRefreshToken); v != "" {
				t.Fatalf("the rotated refresh token came back: %q", v)
			}
		})
	}
}

func TestOAuthConcurrentUse(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: f.signIn(t, cfg)}})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	f.fp.mu.Lock()
	f.fp.rotate = true
	f.fp.mu.Unlock()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				switch (w + i) % 4 {
				case 0:
					f.b.credentialFor(ctx, id)
				case 1:
					f.b.invalidateCredentialsFor(id)
				case 2:
					c := googleDaemonConfig()
					c.Name = "n" + string(rune('a'+i%26))
					acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: c})
				case 3:
					f.b.ensureReauthSession(id)
					f.b.reauthURL(api.AuthRequiredNotification{AccountID: added.AccountID, Reason: api.CodeAuthRequired})
				}
			}
		}(w)
	}
	wg.Wait()
}
