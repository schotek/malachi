// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeOAuth is an authorisation server on httptest for both providers:
// /auth redirects to the loopback redirect_uri with a code, /token
// exchanges codes (with an ID token naming email for Google) and refreshes
// the refresh tokens it issued until they are revoked.
type fakeOAuth struct {
	srv *httptest.Server

	mu        sync.Mutex
	email     string // the Google identity of the next exchange
	clientID  string
	seq       int
	valid     map[string]bool // refresh tokens that still refresh
	exchanges int
	refreshes int
	lastAuth  url.Values
	rotate    bool          // a refresh answers with a new refresh token
	gate      chan struct{} // refreshes wait for it to close (after counting)
}

func newFakeOAuth(t *testing.T, clientID string) *fakeOAuth {
	t.Helper()
	fp := &fakeOAuth{clientID: clientID, valid: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", fp.handleAuth)
	mux.HandleFunc("/token", fp.handleToken)
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakeOAuth) endpoints(oauth2flow.Provider, string) oauth2flow.Endpoints {
	return oauth2flow.Endpoints{AuthURL: fp.srv.URL + "/auth", TokenURL: fp.srv.URL + "/token"}
}

func (fp *fakeOAuth) handleAuth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fp.mu.Lock()
	fp.lastAuth = q
	fp.seq++
	code := fmt.Sprintf("code-%d", fp.seq)
	fp.mu.Unlock()
	v := url.Values{"state": {q.Get("state")}, "code": {code}}
	http.Redirect(w, r, q.Get("redirect_uri")+"?"+v.Encode(), http.StatusFound)
}

func (fp *fakeOAuth) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("client_id") != fp.clientID {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
		return
	}
	if r.PostForm.Get("grant_type") == "refresh_token" {
		fp.mu.Lock()
		gate := fp.gate
		if gate != nil {
			fp.refreshes++
		}
		fp.mu.Unlock()
		if gate != nil {
			<-gate
		}
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.seq++
	resp := map[string]any{"access_token": fmt.Sprintf("access-%d", fp.seq), "token_type": "Bearer", "expires_in": 3600}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		fp.exchanges++
		rt := fmt.Sprintf("refresh-%d", fp.seq)
		fp.valid[rt] = true
		resp["refresh_token"] = rt
		resp["id_token"] = fakeJWT(map[string]any{
			"iss": "https://accounts.google.com", "aud": fp.clientID, "email": fp.email, "email_verified": true,
			"exp": time.Now().Add(time.Hour).Unix(),
		})
	case "refresh_token":
		if fp.gate == nil {
			fp.refreshes++
		}
		rt := r.PostForm.Get("refresh_token")
		if !fp.valid[rt] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		if fp.rotate {
			delete(fp.valid, rt)
			nrt := fmt.Sprintf("refresh-%d-rot", fp.seq)
			fp.valid[nrt] = true
			resp["refresh_token"] = nrt
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// revoke makes every refresh token issued so far useless.
func (fp *fakeOAuth) revoke() {
	fp.mu.Lock()
	fp.valid = map[string]bool{}
	fp.mu.Unlock()
}

func (fp *fakeOAuth) counts() (exchanges, refreshes int) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.exchanges, fp.refreshes
}

func fakeJWT(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "RS256"}) + "." + enc(claims) + ".c2ln"
}

// browse plays the browser: it opens the authorisation URL, follows the
// provider's redirect to the backend's loopback listener and returns the
// page it shows.
func browse(t *testing.T, authURL string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 20 * time.Second}
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// lockedKeyring is a memKeyring safe for the token sources' goroutines,
// failing every Set while refuse is set.
type lockedKeyring struct {
	mu     sync.Mutex
	k      *memKeyring
	refuse bool
}

func newLockedKeyring() *lockedKeyring { return &lockedKeyring{k: newMemKeyring()} }

func (l *lockedKeyring) Get(ctx context.Context, id api.AccountID, key string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.k.Get(ctx, id, key)
}
func (l *lockedKeyring) Set(ctx context.Context, id api.AccountID, key, value string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refuse {
		return api.NewError(api.CodeKeyringError, "keyring locked")
	}
	return l.k.Set(ctx, id, key, value)
}
func (l *lockedKeyring) Delete(ctx context.Context, id api.AccountID, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.k.Delete(ctx, id, key)
}
func (l *lockedKeyring) value(id api.AccountID, key string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.k.values[string(id)+"/"+key]
}
func (l *lockedKeyring) setRefuse(v bool) {
	l.mu.Lock()
	l.refuse = v
	l.mu.Unlock()
}

// authRec records notify.authRequired.
type authRec struct {
	recNotifier
	mu   sync.Mutex
	auth []api.AuthRequiredNotification
}

func (n *authRec) AuthRequired(ev api.AuthRequiredNotification) {
	n.mu.Lock()
	n.auth = append(n.auth, ev)
	n.mu.Unlock()
}

const (
	testGoogleClient    = "google-client.apps.example"
	testMicrosoftClient = "00000000-1111-2222-3333-444444444444"
)

type oauthFixture struct {
	b   *Backend
	fp  *fakeOAuth
	k   *lockedKeyring
	sup *fakeSupervisor
	out *fakeOutbox
}

// newOAuthBackend is a backend whose own sign-in talks to a fake provider
// accepting clientID (config.toml names it for both providers unless
// clientID is "").
func newOAuthBackend(t *testing.T, clientID string) *oauthFixture {
	t.Helper()
	cfg := config.Default()
	cfg.OAuth2.Google.ClientID = clientID
	cfg.OAuth2.Microsoft.ClientID = clientID
	b := newTestBackend(t, cfg)
	fp := newFakeOAuth(t, clientID)
	fp.email = "me@gmail.invalid"
	b.OAuth.Close()
	b.OAuth = oauth2flow.NewManager(oauth2flow.Options{
		HTTP:      fp.srv.Client(),
		Endpoints: fp.endpoints,
		Identity:  map[oauth2flow.Provider]oauth2flow.IdentityFunc{oauth2flow.Microsoft: b.graphIdentity},
	})
	t.Cleanup(b.Close)
	b.TokenSourceDefaults = oauth2flow.TokenSourceOptions{HTTP: fp.srv.Client(), Endpoints: fp.endpoints}
	k := newLockedKeyring()
	b.Keyring = k
	sup, out := newFakeSupervisor(), newFakeOutbox()
	b.Supervisor, b.Delivery = sup, out
	return &oauthFixture{b: b, fp: fp, k: k, sup: sup, out: out}
}

func googleDaemonConfig() api.AccountConfig {
	c := gmailConfig()
	c.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderGoogle}
	return c
}

func graphDaemonConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Contoso", Email: "me@contoso.invalid", Kind: api.AccountGraph,
		Graph:  &api.GraphConfig{Source: api.GraphSourceDaemon},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderOffice365},
	}
}

// signIn runs a complete sign-in for cfg and returns the session id.
func (f *oauthFixture) signIn(t *testing.T, cfg api.AccountConfig) string {
	t.Helper()
	ctx := context.Background()
	start, err := f.b.Accounts().OAuthStart(ctx, api.AccountOAuthStartParams{Config: &cfg})
	if err != nil {
		t.Fatalf("oauthStart: %v", err)
	}
	if status, body := browse(t, start.AuthURL); status != http.StatusOK {
		t.Fatalf("browser page %d: %s", status, body)
	}
	res, err := f.b.Accounts().OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID})
	if err != nil || res.Status != api.OAuthSessionComplete {
		t.Fatalf("oauthWait: %+v, %v", res, err)
	}
	return start.SessionID
}

func TestOAuthAddGoogle(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()

	cfg := googleDaemonConfig()
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{
		Config:      &cfg,
		BrowserPage: &api.OAuthBrowserPage{SuccessTitle: "Přihlášeno <b>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(start.SessionID, "s_") || !strings.HasPrefix(start.AuthURL, f.fp.srv.URL+"/auth?") ||
		time.Until(start.ExpiresAt) < 9*time.Minute || start.ExpiresAt.Location() != time.UTC {
		t.Fatalf("start = %+v", start)
	}
	u, _ := url.Parse(start.AuthURL)
	if q := u.Query(); q.Get("login_hint") != cfg.Email || q.Get("client_id") != testGoogleClient || q.Get("code_challenge") == "" {
		t.Fatalf("auth URL query = %v", q)
	}

	// Nothing yet: a short wait answers pending.
	wctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	res, err := acc.OAuthWait(wctx, api.AccountOAuthWaitParams{SessionID: start.SessionID})
	cancel()
	if err != nil || res.Status != api.OAuthSessionPending || res.Config != nil {
		t.Fatalf("pending wait = %+v, %v", res, err)
	}

	status, page := browse(t, start.AuthURL)
	if status != http.StatusOK || !strings.Contains(page, "Přihlášeno &lt;b&gt;") {
		t.Fatalf("browser page %d: %s", status, page)
	}
	res, err = acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID})
	if err != nil || res.Status != api.OAuthSessionComplete || res.Config == nil || res.Config.Email != cfg.Email ||
		res.Config.OAuth2.Source != api.OAuth2SourceDaemon {
		t.Fatalf("complete wait = %+v, %v", res, err)
	}

	// account.test signs in with the session's token and leaves it.
	var secrets []string
	var mu sync.Mutex
	f.b.ProbeIMAP = func(_ context.Context, _ api.ServerConfig, s string) (imap.ProbeResult, error) {
		mu.Lock()
		secrets = append(secrets, s)
		mu.Unlock()
		return imap.ProbeResult{}, nil
	}
	f.b.ProbeSMTP = func(_ context.Context, _ api.ServerConfig, s string) (smtp.ProbeResult, error) {
		mu.Lock()
		secrets = append(secrets, s)
		mu.Unlock()
		return smtp.ProbeResult{}, nil
	}
	creds := api.Credentials{OAuthSession: start.SessionID}
	tr, err := acc.Test(ctx, api.AccountTestParams{Config: *res.Config, Credentials: creds})
	if err != nil || !tr.IMAP.OK || !tr.SMTP.OK || len(secrets) != 2 || secrets[0] != secrets[1] || !strings.HasPrefix(secrets[0], "access-") {
		t.Fatalf("test = %+v, %v (secrets %v)", tr, err, secrets)
	}

	added, err := acc.Add(ctx, api.AccountAddParams{Config: *res.Config, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	if rt := f.k.value(id, auth.KeyRefreshToken); !strings.HasPrefix(rt, "refresh-") {
		t.Fatalf("refresh token in keyring = %q", rt)
	}
	if got := f.sup.recorded(); len(got) != 1 || got[0] != "start:"+string(id) {
		t.Fatalf("supervisor = %v", got)
	}
	// Consumed: the session is gone, and cannot be added twice.
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("wait after add: %v", err)
	}
	if _, _, ok := f.b.OAuth.Pending(string(id)); ok {
		t.Fatal("a signed-in account got a re-sign-in")
	}

	// The engines get the seeded token without a refresh; a refused token
	// is refreshed with the stored refresh token.
	tok, err := f.b.credentialFor(ctx, string(id))
	if err != nil || tok != secrets[0] {
		t.Fatalf("credential = %q, %v", tok, err)
	}
	f.b.invalidateCredentialsFor(string(id))
	tok2, err := f.b.credentialFor(ctx, string(id))
	if _, refreshes := f.fp.counts(); err != nil || tok2 == tok || refreshes != 1 {
		t.Fatalf("after invalidate: %q, %v (refreshes %d)", tok2, err, refreshes)
	}

	// account.test of the stored account uses the stored sign-in.
	secrets = nil
	tr, err = acc.Test(ctx, api.AccountTestParams{AccountID: id, Config: *res.Config})
	if err != nil || !tr.IMAP.OK || len(secrets) != 2 || secrets[0] != tok2 {
		t.Fatalf("test with accountId = %+v, %v (%v)", tr, err, secrets)
	}
}

func TestOAuthWrongMailboxAndCancel(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()

	f.fp.email = "someone.else@gmail.invalid"
	cfg := googleDaemonConfig()
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := browse(t, start.AuthURL); status == http.StatusOK {
		t.Fatal("wrong mailbox accepted by the page")
	}
	_, err = acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID})
	var ae *api.Error
	if !errorsAs(err, &ae) || ae.Code != api.CodeInvalidArgument || fmt.Sprint(ae.Data) != "map[signedInAs:someone.else@gmail.invalid]" {
		t.Fatalf("wrong mailbox: %v (%+v)", err, ae)
	}
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: start.SessionID}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("failed session added: %v", err)
	}

	start, err = acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acc.OAuthCancel(ctx, api.AccountOAuthCancelParams{SessionID: start.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID}); errCode(t, err) != api.CodeCancelled {
		t.Fatalf("cancelled: %v", err)
	}
	for _, id := range []string{start.SessionID, "s_nope", ""} {
		if _, err := acc.OAuthCancel(ctx, api.AccountOAuthCancelParams{SessionID: id}); err != nil {
			t.Fatalf("cancel %q: %v", id, err)
		}
	}
	for _, id := range []string{"s_nope", ""} {
		if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: id}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("wait %q: %v", id, err)
		}
	}
}

func TestOAuthStartValidation(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	pw := addTestAccount(t, acc, "pw@example.invalid")

	for name, p := range map[string]api.AccountOAuthStartParams{
		"neither":          {},
		"both":             {Config: &cfg, AccountID: pw},
		"goa config":       {Config: func() *api.AccountConfig { c := gmailConfig(); return &c }()},
		"password config":  {Config: func() *api.AccountConfig { c := validConfig(); return &c }()},
		"invalid config":   {Config: func() *api.AccountConfig { c := googleDaemonConfig(); c.Email = "x"; return &c }()},
		"password account": {AccountID: pw},
	} {
		if _, err := acc.OAuthStart(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{AccountID: "acc_nope"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}
	// The config is validated (and trimmed) on a copy: the caller's stays.
	c := googleDaemonConfig()
	c.Name = "  Gmail  "
	if _, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &c}); err != nil || c.Name != "  Gmail  " {
		t.Fatalf("start: %v, name %q", err, c.Name)
	}
}

func TestOAuthClientMissing(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, "")
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	if _, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &cfg}); errCode(t, err) != api.CodeOAuthClientMissing {
		t.Fatalf("start: %v", err)
	}
	// Google has no built-in client; Microsoft ships one (oauth2flow
	// builtinClients), so only Google is missing here.
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()}); errCode(t, err) != api.CodeOAuthClientMissing {
		t.Fatalf("add google: %v", err)
	}
	if !f.b.OAuthClients.Available(oauth2flow.Microsoft) {
		t.Fatal("the built-in Microsoft client is not available")
	}
	if list := listIDs(t, acc); len(list) != 0 {
		t.Fatalf("rows left: %v", list)
	}
	// account.test without a sign-in: the endpoints' outcome.
	res, err := acc.Test(ctx, api.AccountTestParams{Config: graphDaemonConfig()})
	if err != nil || res.Graph.OK || res.Graph.Error.Code != api.CodeAuthRequired {
		t.Fatalf("test without sign-in: %+v, %v", res, err)
	}

	// The account's own clientId is enough.
	cfg.OAuth2.ClientID = testGoogleClient
	f.fp.clientID = testGoogleClient
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.AuthURL)
	if u.Query().Get("client_id") != testGoogleClient {
		t.Fatalf("auth URL = %s", start.AuthURL)
	}

	// An account with its own client id: without a sign-in yet, account.test
	// of the stored account reports authRequired per endpoint; dropping the
	// client id is oauthClientMissing.
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	res, err = acc.Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: cfg})
	if err != nil || res.IMAP.Error == nil || res.IMAP.Error.Code != api.CodeAuthRequired {
		t.Fatalf("test of the stored account = %+v, %v", res, err)
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: googleDaemonConfig()}); errCode(t, err) != api.CodeOAuthClientMissing {
		t.Fatalf("update without a client: %v", err)
	}

	// A stored account whose client went away (config.toml edited): its
	// stored sign-in cannot refresh, account.test says why per endpoint.
	f2 := newOAuthBackend(t, testGoogleClient)
	added, err = f2.b.Accounts().Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	f2.b.OAuthClients = &oauth2flow.Registry{}
	f2.b.dropTokenSource(string(added.AccountID))
	res, err = f2.b.Accounts().Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: googleDaemonConfig()})
	if err != nil || res.IMAP.Error == nil || res.IMAP.Error.Code != api.CodeOAuthClientMissing ||
		res.SMTP.Error == nil || res.SMTP.Error.Code != api.CodeOAuthClientMissing {
		t.Fatalf("test without a client = %+v, %v", res, err)
	}
}

func TestOAuthAddKeyringRollback(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	cfg := googleDaemonConfig()
	session := f.signIn(t, cfg)

	f.b.Keyring = auth.UnavailableKeyring{}
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}}); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("add: %v", err)
	}
	if list := listIDs(t, acc); len(list) != 0 {
		t.Fatalf("row kept: %v", list)
	}
	if len(f.sup.recorded()) != 0 {
		t.Fatalf("supervisor = %v", f.sup.recorded())
	}
	// The session survives for another try.
	f.b.Keyring = f.k
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}}); err != nil {
		t.Fatalf("second try: %v", err)
	}
}

func TestOAuthSessionMisuse(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	session := f.signIn(t, googleDaemonConfig())

	other := googleDaemonConfig()
	other.Email = "other@gmail.invalid"
	cases := map[string]api.AccountAddParams{
		"password account": {Config: validConfig(), Credentials: api.Credentials{OAuthSession: session}},
		"goa account":      {Config: gmailConfig(), Credentials: api.Credentials{OAuthSession: session}},
		"another address":  {Config: other, Credentials: api.Credentials{OAuthSession: session}},
		"another provider": {Config: graphDaemonConfig(), Credentials: api.Credentials{OAuthSession: session}},
		"unknown session":  {Config: googleDaemonConfig(), Credentials: api.Credentials{OAuthSession: "s_nope"}},
	}
	for name, p := range cases {
		if _, err := acc.Add(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("add %s: %v", name, err)
		}
		if _, err := acc.Test(ctx, api.AccountTestParams{Config: p.Config, Credentials: p.Credentials}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("test %s: %v", name, err)
		}
	}
	if list := listIDs(t, acc); len(list) != 0 {
		t.Fatalf("rows: %v", list)
	}
	// The address compares case-insensitively.
	upper := googleDaemonConfig()
	upper.Email = "ME@Gmail.invalid"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: upper, Credentials: api.Credentials{OAuthSession: session}}); err != nil {
		t.Fatalf("case-insensitive address: %v", err)
	}
}

func TestOAuthAddWithoutSessionAndReauth(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	rec := &authRec{}
	notifier := outboxAwareNotifier{b: f.b, inner: rec}

	// Added without a sign-in (as config.toml would): a session waits at
	// once and the engines' authRequired carries its URL.
	added, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	sessionID, authURL, ok := f.b.OAuth.Pending(id)
	if !ok {
		t.Fatal("no re-sign-in waiting")
	}
	if _, err := f.b.credentialFor(ctx, id); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("credential without sign-in: %v", err)
	}
	notifier.AuthRequired(api.AuthRequiredNotification{AccountID: added.AccountID, Reason: api.CodeAuthRequired, Message: "x"})
	if len(rec.auth) != 1 || rec.auth[0].AuthURL != authURL {
		t.Fatalf("notification = %+v", rec.auth)
	}

	// The UI's oauthStart returns the waiting session.
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{AccountID: added.AccountID, BrowserPage: &api.OAuthBrowserPage{SuccessTitle: "Done"}})
	if err != nil || start.SessionID != sessionID || start.AuthURL != authURL {
		t.Fatalf("start = %+v, %v", start, err)
	}
	f.sup.reset()
	f.out.reset()
	if status, page := browse(t, authURL); status != http.StatusOK || !strings.Contains(page, "Done") {
		t.Fatalf("page %d: %s", status, page)
	}
	res, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: sessionID})
	if err != nil || res.Status != api.OAuthSessionComplete || res.Config == nil || res.Config.Email != "me@gmail.invalid" {
		t.Fatalf("wait = %+v, %v", res, err)
	}
	// Stored and restarted before the waiter was released.
	if !strings.HasPrefix(f.k.value(added.AccountID, auth.KeyRefreshToken), "refresh-") {
		t.Fatal("refresh token not stored")
	}
	expectCalls(t, "re-sign-in", f.sup, f.out, "restart:"+id)
	if _, err := f.b.credentialFor(ctx, id); err != nil {
		t.Fatalf("credential after sign-in: %v", err)
	}

	// The provider revokes the sign-in: the refresh fails, a new session
	// opens by itself and the notification carries it.
	f.fp.revoke()
	f.b.invalidateCredentialsFor(id)
	if _, err := f.b.credentialFor(ctx, id); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("revoked: %v", err)
	}
	var again string
	waitFor(t, "a re-sign-in", func() bool {
		var ok bool
		_, again, ok = f.b.OAuth.Pending(id)
		return ok
	})
	if again == authURL {
		t.Fatal("finished session reused")
	}
	notifier.AuthRequired(api.AuthRequiredNotification{AccountID: added.AccountID, Reason: api.CodeAuthRequired})
	if len(rec.auth) != 2 || rec.auth[1].AuthURL != again {
		t.Fatalf("notification = %+v", rec.auth)
	}
	// Other reasons and other accounts get no URL.
	pw := addTestAccount(t, acc, "pw@example.invalid")
	notifier.AuthRequired(api.AuthRequiredNotification{AccountID: pw, Reason: api.CodeAuthRequired})
	notifier.AuthRequired(api.AuthRequiredNotification{AccountID: "acc_nope", Reason: api.CodeAuthRequired})
	if len(rec.auth) != 4 || rec.auth[2].AuthURL != "" || rec.auth[3].AuthURL != "" {
		t.Fatalf("notifications = %+v", rec.auth)
	}
	if _, _, ok := f.b.OAuth.Pending(string(pw)); ok {
		t.Fatal("password account got a sign-in")
	}

	// Removing the account ends its sign-in.
	againID, _, _ := f.b.OAuth.Pending(id)
	if _, err := acc.Remove(ctx, api.AccountRemoveParams{AccountID: added.AccountID}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.b.OAuth.Pending(id); ok {
		t.Fatal("sign-in still waiting after remove")
	}
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: againID}); errCode(t, err) != api.CodeCancelled {
		t.Fatalf("wait after remove: %v", err)
	}
	if f.k.value(added.AccountID, auth.KeyRefreshToken) != "" {
		t.Fatal("refresh token kept after remove")
	}
}

func TestOAuthReauthKeyringRefusesKeepsInMemory(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()
	added, err := acc.Add(ctx, api.AccountAddParams{Config: googleDaemonConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	_, authURL, ok := f.b.OAuth.Pending(id)
	if !ok {
		t.Fatal("no re-sign-in")
	}
	f.k.setRefuse(true)
	f.sup.reset()
	f.out.reset()
	if status, _ := browse(t, authURL); status != http.StatusOK {
		t.Fatalf("page %d", status)
	}
	waitFor(t, "the restart", func() bool { return f.sup.count("restart:") == 1 })
	if f.k.value(added.AccountID, auth.KeyRefreshToken) != "" {
		t.Fatal("refused keyring holds the token")
	}
	f.b.invalidateCredentialsFor(id)
	if tok, err := f.b.credentialFor(ctx, id); err != nil || !strings.HasPrefix(tok, "access-") {
		t.Fatalf("in-memory sign-in: %q, %v", tok, err)
	}
	// An update of the account forgets it again.
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: googleDaemonConfig()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.b.credentialFor(ctx, id); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("after update: %v", err)
	}
}

func TestOAuthUpdate(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testGoogleClient)
	acc := f.b.Accounts()

	// A password Gmail account moves to the backend's sign-in.
	pwCfg := googleDaemonConfig()
	pwCfg.OAuth2 = nil
	pwCfg.IMAP.AuthMethod, pwCfg.SMTP.AuthMethod = api.AuthPassword, api.AuthPassword
	added, err := acc.Add(ctx, api.AccountAddParams{Config: pwCfg, Credentials: api.Credentials{Password: "app-pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	session := f.signIn(t, googleDaemonConfig())

	// A keyring failure reverts the configuration.
	f.k.setRefuse(true)
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: googleDaemonConfig(), Credentials: api.Credentials{OAuthSession: session}}); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("update with refusing keyring: %v", err)
	}
	a, _ := f.b.store.GetAccount(ctx, string(id))
	if a.Config.OAuth2 != nil || a.Config.IMAP.AuthMethod != api.AuthPassword {
		t.Fatalf("not reverted: %+v", a.Config)
	}
	f.k.setRefuse(false)

	f.sup.reset()
	f.out.reset()
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: googleDaemonConfig(), Credentials: api.Credentials{OAuthSession: session}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.k.value(id, auth.KeyRefreshToken), "refresh-") {
		t.Fatal("refresh token not stored")
	}
	expectCalls(t, "update", f.sup, f.out, "restart:"+string(id))
	if tok, err := f.b.credentialFor(ctx, string(id)); err != nil || !strings.HasPrefix(tok, "access-") {
		t.Fatalf("credential: %q, %v", tok, err)
	}
	if _, ok := f.b.OAuth.Grant(session); ok {
		t.Fatal("session not consumed")
	}

	// And back to a password: the refresh token goes, nothing waits.
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: pwCfg}); err != nil {
		t.Fatal(err)
	}
	if f.k.value(id, auth.KeyRefreshToken) != "" {
		t.Fatal("refresh token kept")
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: pwCfg, Credentials: api.Credentials{OAuthSession: "s_x"}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("session for a password account: %v", err)
	}
}

func TestOAuthGraphAccount(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testMicrosoftClient)
	acc := f.b.Accounts()
	var probed []string
	var mu sync.Mutex
	mailbox := "Me@Contoso.invalid"
	f.b.ProbeGraph = func(_ context.Context, token string) (graph.ProbeResult, error) {
		mu.Lock()
		defer mu.Unlock()
		probed = append(probed, token)
		return graph.ProbeResult{Email: mailbox, Capabilities: []string{"graph"}}, nil
	}

	cfg := graphDaemonConfig()
	cfg.OAuth2.TenantID = "contoso.onmicrosoft.com"
	session := f.signIn(t, cfg)
	mu.Lock()
	identityToken := probed[0] // the identity check read /me with the fresh token
	mu.Unlock()

	res, err := acc.Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}})
	if err != nil || !res.Graph.OK || res.IMAP != nil {
		t.Fatalf("test = %+v, %v", res, err)
	}
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}})
	if err != nil {
		t.Fatal(err)
	}
	id := string(added.AccountID)
	if tok, err := f.b.GraphTokenFor(ctx, id); err != nil || tok != identityToken {
		t.Fatalf("graph token = %q, %v (want %q)", tok, err, identityToken)
	}
	f.b.invalidateGraphTokenFor(id)
	if tok, err := f.b.GraphTokenFor(ctx, id); err != nil || tok == identityToken {
		t.Fatalf("after invalidate: %q, %v", tok, err)
	}

	// Signed in to another mailbox: refused before anything is kept.
	mailbox = "other@contoso.invalid"
	other := graphDaemonConfig()
	other.Email = "second@contoso.invalid"
	start, err := acc.OAuthStart(ctx, api.AccountOAuthStartParams{Config: &other})
	if err != nil {
		t.Fatal(err)
	}
	browse(t, start.AuthURL)
	if _, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("other mailbox: %v", err)
	}
}

func TestDaemonAccountValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  func() api.AccountConfig
		ok   bool
	}{
		{"google", googleDaemonConfig, true},
		{"google client id", func() api.AccountConfig {
			c := googleDaemonConfig()
			c.OAuth2.ClientID = " 123-abc.apps.googleusercontent.com "
			return c
		}, true},
		{"google client id with space", func() api.AccountConfig { c := googleDaemonConfig(); c.OAuth2.ClientID = "a b"; return c }, false},
		{"google client id too long", func() api.AccountConfig {
			c := googleDaemonConfig()
			c.OAuth2.ClientID = strings.Repeat("a", 257)
			return c
		}, false},
		{"google tenant", func() api.AccountConfig { c := googleDaemonConfig(); c.OAuth2.TenantID = "common"; return c }, false},
		{"google goa id", func() api.AccountConfig { c := googleDaemonConfig(); c.OAuth2.GOAAccountID = googleGOAID; return c }, false},
		{"google scopes", func() api.AccountConfig { c := googleDaemonConfig(); c.OAuth2.Scopes = []string{"mail"}; return c }, false},
		{"google token url", func() api.AccountConfig {
			c := googleDaemonConfig()
			c.OAuth2.TokenURL = "https://a.invalid/t"
			return c
		}, false},
		{"google smtp password", func() api.AccountConfig { c := googleDaemonConfig(); c.SMTP.AuthMethod = api.AuthPassword; return c }, false},
		{"office365 on imap", func() api.AccountConfig {
			c := googleDaemonConfig()
			c.OAuth2.Provider = api.OAuth2ProviderOffice365
			return c
		}, false},
		{"custom daemon", func() api.AccountConfig {
			c := googleDaemonConfig()
			c.OAuth2.Provider = api.OAuth2ProviderCustom
			return c
		}, false},
		{"graph", graphDaemonConfig, true},
		{"graph tenant", func() api.AccountConfig {
			c := graphDaemonConfig()
			c.OAuth2.TenantID, c.OAuth2.ClientID = "72f988bf-86f1-41af-91ab-2d7cd011db47", testMicrosoftClient
			return c
		}, true},
		{"graph bad tenant", func() api.AccountConfig { c := graphDaemonConfig(); c.OAuth2.TenantID = "a/b"; return c }, false},
		{"graph dot-dot tenant", func() api.AccountConfig { c := graphDaemonConfig(); c.OAuth2.TenantID = ".."; return c }, false},
		{"graph dotted tenant", func() api.AccountConfig { c := graphDaemonConfig(); c.OAuth2.TenantID = ".contoso"; return c }, false},
		{"graph long tenant", func() api.AccountConfig {
			c := graphDaemonConfig()
			c.OAuth2.TenantID = strings.Repeat("a", 65)
			return c
		}, false},
		{"graph without oauth2", func() api.AccountConfig { c := graphDaemonConfig(); c.OAuth2 = nil; return c }, false},
		{"graph google", func() api.AccountConfig {
			c := graphDaemonConfig()
			c.OAuth2.Provider = api.OAuth2ProviderGoogle
			return c
		}, false},
		{"graph oauth2 goa", func() api.AccountConfig {
			c := graphDaemonConfig()
			c.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle, GOAAccountID: goaID}
			return c
		}, false},
		{"graph oauth2 without source", func() api.AccountConfig { c := graphDaemonConfig(); c.OAuth2.Source = ""; return c }, false},
		{"graph goa id", func() api.AccountConfig { c := graphDaemonConfig(); c.Graph.GOAAccountID = goaID; return c }, false},
		{"graph servers", func() api.AccountConfig { c := graphDaemonConfig(); c.IMAP = validConfig().IMAP; return c }, false},
		{"graph goa with oauth2", func() api.AccountConfig {
			c := graphConfig()
			c.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderOffice365}
			return c
		}, false},
	}
	for _, tc := range cases {
		c := tc.cfg()
		err := validateAccountConfig(&c)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: code %v, want invalidArgument", tc.name, err)
		}
		if tc.ok && c.OAuth2.ClientID != strings.TrimSpace(c.OAuth2.ClientID) {
			t.Errorf("%s: clientId not trimmed", tc.name)
		}
	}
}

func TestGOAAvailable(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	for name, tc := range map[string]struct {
		goa  GOAClient
		want bool
	}{
		"nil client":   {nil, false},
		"no service":   {&fakeGOA{listErr: api.NewError(api.CodeUnavailable, "no bus")}, false},
		"running":      {&fakeGOA{}, true},
		"failing call": {&fakeGOA{listErr: api.NewError(api.CodeServerError, "boom")}, true},
	} {
		b.GOA = tc.goa
		if got := b.goaAvailable(ctx); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
