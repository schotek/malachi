// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeProvider is an authorisation server on httptest: /auth redirects to
// the loopback redirect_uri with a code (or an error), /token exchanges
// codes (checking PKCE, client and redirect) and refreshes tokens.
type fakeProvider struct {
	t   *testing.T
	srv *httptest.Server

	clientID     string
	clientSecret string // expected at /token when set

	mu sync.Mutex
	// /auth
	authQueries []url.Values
	authError   string // /auth answers with error=… instead of a code
	codes       map[string]pendingCode
	// /token, authorization_code
	exchangeForms []url.Values
	exchangeError string // error code answered to every exchange
	exchangeDesc  string
	pkceChecked   int
	idEmail       string // email claim of the ID token ("" = no ID token)
	idIss         string
	idAud         string
	idClaims      map[string]any   // replace (or, with a nil value, drop) claims
	now           func() time.Time // the ID token's exp is now + 1 h
	noRefresh     bool             // exchange answers without refresh_token
	// /token, refresh_token
	refreshForms  []url.Values
	refreshValid  map[string]bool
	refreshError  string // error code answered to every refresh
	refreshStatus int    // HTTP status of that answer (default 400)
	rotate        bool
	gate          chan struct{} // refreshes block until closed
	seq           int
	expiresIn     int
	tenants       []string
}

type pendingCode struct {
	challenge, method, redirect string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{
		t:            t,
		clientID:     "client-123.apps.example",
		codes:        map[string]pendingCode{},
		refreshValid: map[string]bool{},
		idEmail:      "user@example.org",
		idIss:        "https://accounts.google.com",
		expiresIn:    3600,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", fp.handleAuth)
	mux.HandleFunc("/token", fp.handleToken)
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

// endpoints is Options.Endpoints / TokenSourceOptions.Endpoints.
func (fp *fakeProvider) endpoints(p Provider, tenant string) Endpoints {
	fp.mu.Lock()
	fp.tenants = append(fp.tenants, tenant)
	fp.mu.Unlock()
	return Endpoints{AuthURL: fp.srv.URL + "/auth", TokenURL: fp.srv.URL + "/token"}
}

func (fp *fakeProvider) client() Client { return Client{ID: fp.clientID, Secret: fp.clientSecret} }

func (fp *fakeProvider) handleAuth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.authQueries = append(fp.authQueries, q)
	redirect, state := q.Get("redirect_uri"), q.Get("state")
	v := url.Values{"state": {state}}
	if fp.authError != "" {
		v.Set("error", fp.authError)
		v.Set("error_description", "The user said no")
	} else {
		fp.seq++
		code := fmt.Sprintf("authcode-%d-secretish", fp.seq)
		fp.codes[code] = pendingCode{challenge: q.Get("code_challenge"), method: q.Get("code_challenge_method"), redirect: redirect}
		v.Set("code", code)
	}
	http.Redirect(w, r, redirect+"?"+v.Encode(), http.StatusFound)
}

func (fp *fakeProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form", http.StatusBadRequest)
		return
	}
	form := r.PostForm
	switch form.Get("grant_type") {
	case "authorization_code":
		fp.exchange(w, form)
	case "refresh_token":
		fp.refreshGrant(w, form)
	default:
		writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (fp *fakeProvider) checkClient(form url.Values) bool {
	if form.Get("client_id") != fp.clientID {
		return false
	}
	if fp.clientSecret != "" {
		return form.Get("client_secret") == fp.clientSecret
	}
	_, has := form["client_secret"]
	return !has
}

func (fp *fakeProvider) exchange(w http.ResponseWriter, form url.Values) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.exchangeForms = append(fp.exchangeForms, form)
	if fp.exchangeError != "" {
		// %CODE% echoes the code back, as a careless server might.
		writeTokenError(w, http.StatusBadRequest, fp.exchangeError, strings.ReplaceAll(fp.exchangeDesc, "%CODE%", form.Get("code")))
		return
	}
	code := form.Get("code")
	pc, ok := fp.codes[code]
	delete(fp.codes, code)
	if !ok {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant", "unknown code")
		return
	}
	if !fp.checkClient(form) {
		writeTokenError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	if form.Get("redirect_uri") != pc.redirect {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if pc.method != "S256" || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.challenge {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	fp.pkceChecked++
	fp.seq++
	resp := map[string]any{
		"access_token": fmt.Sprintf("access-%d-xyz", fp.seq),
		"token_type":   "Bearer",
		"expires_in":   fp.expiresIn,
	}
	if !fp.noRefresh {
		rt := fmt.Sprintf("refresh-%d-abc", fp.seq)
		fp.refreshValid[rt] = true
		resp["refresh_token"] = rt
	}
	if fp.idEmail != "" {
		aud := fp.idAud
		if aud == "" {
			aud = fp.clientID
		}
		now := time.Now
		if fp.now != nil {
			now = fp.now
		}
		claims := map[string]any{
			"iss": fp.idIss, "aud": aud, "email": fp.idEmail, "email_verified": true,
			"exp": now().Add(time.Hour).Unix(),
		}
		for k, v := range fp.idClaims {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		resp["id_token"] = fakeIDToken(claims)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (fp *fakeProvider) refreshGrant(w http.ResponseWriter, form url.Values) {
	fp.mu.Lock()
	fp.refreshForms = append(fp.refreshForms, form)
	gate := fp.gate
	fp.mu.Unlock()
	if gate != nil {
		<-gate
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	rt := form.Get("refresh_token")
	if fp.refreshError != "" {
		st := fp.refreshStatus
		if st == 0 {
			st = http.StatusBadRequest
		}
		// A careless server echoing the token must not leak it further.
		writeTokenError(w, st, fp.refreshError, "token "+rt+" rejected")
		return
	}
	if !fp.checkClient(form) {
		writeTokenError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	if !fp.refreshValid[rt] {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	}
	fp.seq++
	resp := map[string]any{
		"access_token": fmt.Sprintf("access-%d-xyz", fp.seq),
		"token_type":   "Bearer",
		"expires_in":   fp.expiresIn,
	}
	if fp.rotate {
		delete(fp.refreshValid, rt)
		nrt := fmt.Sprintf("refresh-%d-rot", fp.seq)
		fp.refreshValid[nrt] = true
		resp["refresh_token"] = nrt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (fp *fakeProvider) refreshCount() int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return len(fp.refreshForms)
}

func (fp *fakeProvider) refreshForm(i int) url.Values {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if i >= len(fp.refreshForms) {
		fp.t.Fatalf("no refresh request %d", i)
	}
	return fp.refreshForms[i]
}

func (fp *fakeProvider) lastAuthQuery() url.Values {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.authQueries) == 0 {
		return nil
	}
	return fp.authQueries[len(fp.authQueries)-1]
}

func writeTokenError(w http.ResponseWriter, status int, code, desc string) {
	body := map[string]string{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeIDToken is an unsigned JWT: the flow trusts the token endpoint.
func fakeIDToken(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims) + ".c2lnbmF0dXJl"
}

// browser plays the user's browser: it opens the authorisation URL, reads
// the provider's redirect, and follows it to the loopback listener.
type browser struct {
	t *testing.T
	c *http.Client
}

func newBrowser(t *testing.T) *browser {
	return &browser{t: t, c: &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// authorize opens authURL and returns the redirect target.
func (b *browser) authorize(authURL string) string {
	b.t.Helper()
	resp, err := b.c.Get(authURL)
	if err != nil {
		b.t.Fatalf("open auth URL: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		b.t.Fatalf("auth status %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "http://127.0.0.1:") {
		b.t.Fatalf("redirect to %q", loc)
	}
	return loc
}

// get fetches a URL and returns the response with its body read.
func (b *browser) get(u string) (*http.Response, string) {
	b.t.Helper()
	resp, err := b.c.Get(u)
	if err != nil {
		b.t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// memKeyring is an in-memory auth.Keyring with switchable failures.
type memKeyring struct {
	mu     sync.Mutex
	items  map[string]string
	getErr error
	setErr error
	gets   int
	sets   int
}

func newMemKeyring() *memKeyring { return &memKeyring{items: map[string]string{}} }

func (k *memKeyring) Get(_ context.Context, a api.AccountID, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets++
	if k.getErr != nil {
		return "", k.getErr
	}
	v, ok := k.items[string(a)+"/"+key]
	if !ok {
		return "", auth.ErrNoSecret
	}
	return v, nil
}

func (k *memKeyring) Set(_ context.Context, a api.AccountID, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sets++
	if k.setErr != nil {
		return k.setErr
	}
	k.items[string(a)+"/"+key] = value
	return nil
}

func (k *memKeyring) Delete(_ context.Context, a api.AccountID, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.items, string(a)+"/"+key)
	return nil
}

func (k *memKeyring) value(a api.AccountID) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.items[string(a)+"/"+auth.KeyRefreshToken]
}

func (k *memKeyring) counts() (gets, sets int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gets, k.sets
}

// clock is an adjustable Now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// apiCode returns the contract code of err, or 0.
func apiCode(err error) api.ErrorCode {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// testHTTP is the daemon side's client in tests: plain HTTP to the fake.
func testHTTP() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
}
