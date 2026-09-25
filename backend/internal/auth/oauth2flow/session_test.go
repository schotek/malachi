// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func newTestManager(t *testing.T, fp *fakeProvider, mod func(*Options)) *Manager {
	t.Helper()
	o := Options{HTTP: testHTTP(), Endpoints: fp.endpoints}
	if mod != nil {
		mod(&o)
	}
	m := NewManager(o)
	t.Cleanup(m.Close)
	return m
}

func googleReq(fp *fakeProvider) StartRequest {
	return StartRequest{
		Provider:    Google,
		Client:      fp.client(),
		LoginHint:   "user@example.org",
		ExpectEmail: "User@Example.org",
		Page:        PageTexts{SuccessTitle: "Signed in", FailureTitle: "Sign-in failed"},
	}
}

func waitOutcome(t *testing.T, m *Manager, id string) Outcome {
	t.Helper()
	out, err := m.Wait(context.Background(), id, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return out
}

func TestAuthURL(t *testing.T) {
	fp := newFakeProvider(t)
	fp.clientSecret = "installed-app-secret"
	m := newTestManager(t, fp, nil)
	id, authURL, expires, err := m.Start(googleReq(fp))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "s_") || len(id) != 2+32 {
		t.Errorf("session id %q", id)
	}
	if until := time.Until(expires); until < 9*time.Minute || until > 11*time.Minute {
		t.Errorf("expires in %v", until)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             fp.clientID,
		"scope":                 "https://mail.google.com/ openid email",
		"code_challenge_method": "S256",
		"access_type":           "offline",
		"prompt":                "consent",
		"login_hint":            "user@example.org",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if len(q.Get("state")) != 43 {
		t.Errorf("state %q", q.Get("state"))
	}
	if len(q.Get("code_challenge")) != 43 {
		t.Errorf("code_challenge %q", q.Get("code_challenge"))
	}
	if q.Has("client_secret") || q.Has("code_verifier") {
		t.Error("secret material in the authorisation URL")
	}
	ru, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || ru.Scheme != "http" || ru.Hostname() != "127.0.0.1" || ru.Port() == "" || ru.Path != "/" {
		t.Errorf("redirect_uri %q", q.Get("redirect_uri"))
	}

	// The verifier the token endpoint receives matches the challenge (the
	// fake checks it), and the configured secret travels in the body.
	b := newBrowser(t)
	resp, _ := b.get(b.authorize(authURL))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status %d", resp.StatusCode)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.pkceChecked != 1 {
		t.Errorf("PKCE checked %d times", fp.pkceChecked)
	}
	f := fp.exchangeForms[0]
	if f.Get("client_secret") != "installed-app-secret" || f.Get("redirect_uri") != q.Get("redirect_uri") {
		t.Errorf("exchange form %v", f)
	}
}

func TestAuthURLMicrosoft(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	_, authURL, _, err := m.Start(StartRequest{
		Provider:  Microsoft,
		Client:    Client{ID: fp.clientID, Tenant: "contoso"},
		LoginHint: "user@contoso.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(authURL[strings.IndexByte(authURL, '?')+1:])
	if got := q.Get("scope"); got != "https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read offline_access" {
		t.Errorf("scope %q", got)
	}
	if q.Has("access_type") || q.Has("prompt") {
		t.Errorf("google-only parameters in %v", q)
	}
	if q.Get("login_hint") != "user@contoso.com" {
		t.Errorf("login_hint %q", q.Get("login_hint"))
	}
	fp.mu.Lock()
	tenants := fp.tenants
	fp.mu.Unlock()
	if len(tenants) != 1 || tenants[0] != "contoso" {
		t.Errorf("endpoints asked for tenants %v", tenants)
	}
}

func TestSessionGoogleSuccess(t *testing.T) {
	fp := newFakeProvider(t)
	var completed []Grant
	var mu sync.Mutex
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.OnComplete = func(g Grant) error {
		mu.Lock()
		completed = append(completed, g)
		mu.Unlock()
		return nil
	}
	id, authURL, _, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if out, ok := m.Lookup(id); !ok || out.Status != StatusPending {
		t.Fatalf("lookup before callback: %+v %v", out, ok)
	}
	b := newBrowser(t)
	resp, body := b.get(b.authorize(authURL))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Signed in") || !strings.Contains(body, "✓") {
		t.Fatalf("page %d:\n%s", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("callback page cacheable")
	}
	out := waitOutcome(t, m, id)
	if out.Status != StatusComplete || out.Err != nil || out.Email != "user@example.org" {
		t.Fatalf("outcome %+v", out)
	}
	mu.Lock()
	if len(completed) != 1 || completed[0].Email != "user@example.org" || completed[0].RefreshToken == "" {
		t.Errorf("OnComplete got %+v", completed)
	}
	mu.Unlock()

	g, ok := m.Grant(id)
	if !ok || g.Provider != Google || g.AccessToken == "" || g.RefreshToken == "" || g.IDToken == "" || g.Expiry.IsZero() {
		t.Fatalf("Grant %+v %v", g, ok)
	}
	if g2, ok := m.Grant(id); !ok || g2 != g {
		t.Error("Grant consumed the session")
	}
	if g3, ok := m.Consume(id); !ok || g3 != g {
		t.Error("Consume failed")
	}
	if _, ok := m.Consume(id); ok {
		t.Error("Consume twice")
	}
	if _, ok := m.Lookup(id); ok {
		t.Error("consumed session still known")
	}
}

func TestSessionListenerClosedAfterCallback(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, err := m.Start(googleReq(fp))
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t)
	loc := b.authorize(authURL)
	b.get(loc)
	waitOutcome(t, m, id)
	if _, err := b.c.Get(loc); err == nil {
		t.Fatal("second callback reached the listener")
	}
	u, _ := url.Parse(loc)
	if c, err := net.DialTimeout("tcp", u.Host, time.Second); err == nil {
		c.Close()
		t.Fatal("listener still accepting")
	}
}

func TestSessionMicrosoftIdentity(t *testing.T) {
	fp := newFakeProvider(t)
	fp.idEmail = "" // no ID token: the identity comes from the injected func
	var gotAccess string
	m := newTestManager(t, fp, func(o *Options) {
		o.Identity = map[Provider]IdentityFunc{Microsoft: func(_ context.Context, g Grant) (string, error) {
			gotAccess = g.AccessToken
			return "Someone@Contoso.com", nil
		}}
	})
	id, authURL, _, err := m.Start(StartRequest{Provider: Microsoft, Client: fp.client(), ExpectEmail: "someone@contoso.com"})
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	out := waitOutcome(t, m, id)
	if out.Status != StatusComplete || out.Email != "Someone@Contoso.com" {
		t.Fatalf("outcome %+v", out)
	}
	if gotAccess == "" {
		t.Error("identity func got no access token")
	}
}

func TestSessionMicrosoftWithoutIdentityFunc(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	// Without an expected address the grant completes with no email.
	id, authURL, _, _ := m.Start(StartRequest{Provider: Microsoft, Client: fp.client()})
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusComplete || out.Email != "" {
		t.Fatalf("outcome %+v", out)
	}
	// With one, an unknown identity fails closed.
	id, authURL, _, _ = m.Start(StartRequest{Provider: Microsoft, Client: fp.client(), ExpectEmail: "a@b.c"})
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusFailed || out.Err.Code != api.CodeAuthFailed {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionIdentityMismatch(t *testing.T) {
	fp := newFakeProvider(t)
	fp.idEmail = "other@example.org"
	completed := false
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.OnComplete = func(Grant) error { completed = true; return nil }
	id, authURL, _, _ := m.Start(req)
	b := newBrowser(t)
	resp, body := b.get(b.authorize(authURL))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Sign-in failed") || !strings.Contains(body, "✗") {
		t.Fatalf("page %d:\n%s", resp.StatusCode, body)
	}
	out := waitOutcome(t, m, id)
	if out.Status != StatusFailed || out.Err == nil || out.Err.Code != api.CodeInvalidArgument {
		t.Fatalf("outcome %+v", out)
	}
	data, ok := out.Err.Data.(map[string]string)
	if !ok || data["signedInAs"] != "other@example.org" {
		t.Errorf("data %#v", out.Err.Data)
	}
	if completed {
		t.Error("OnComplete ran for a mismatch")
	}
	if _, ok := m.Grant(id); ok {
		t.Error("grant kept for a mismatch")
	}
}

func TestSessionIDTokenChecks(t *testing.T) {
	for name, mod := range map[string]func(*fakeProvider){
		"wrong audience": func(fp *fakeProvider) { fp.idAud = "someone-else" },
		"wrong issuer":   func(fp *fakeProvider) { fp.idIss = "https://evil.example" },
		"no id token":    func(fp *fakeProvider) { fp.idEmail = "" },
	} {
		t.Run(name, func(t *testing.T) {
			fp := newFakeProvider(t)
			mod(fp)
			m := newTestManager(t, fp, nil)
			id, authURL, _, _ := m.Start(googleReq(fp))
			b := newBrowser(t)
			b.get(b.authorize(authURL))
			if out := waitOutcome(t, m, id); out.Status != StatusFailed || out.Err.Code != api.CodeAuthFailed {
				t.Fatalf("outcome %+v", out)
			}
		})
	}
}

func TestSessionAccessDenied(t *testing.T) {
	fp := newFakeProvider(t)
	fp.authError = "access_denied"
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(StartRequest{Provider: Google, Client: fp.client()})
	b := newBrowser(t)
	_, body := b.get(b.authorize(authURL))
	if !strings.Contains(body, "access_denied") || !strings.Contains(body, "<title>Malachi Mail</title>") {
		t.Errorf("page without texts:\n%s", body)
	}
	out := waitOutcome(t, m, id)
	if out.Status != StatusCancelled || out.Err.Code != api.CodeCancelled {
		t.Fatalf("outcome %+v", out)
	}
	fp.mu.Lock()
	n := len(fp.exchangeForms)
	fp.mu.Unlock()
	if n != 0 {
		t.Error("exchange after access_denied")
	}
}

func TestSessionProviderError(t *testing.T) {
	fp := newFakeProvider(t)
	fp.authError = "server_error"
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(StartRequest{Provider: Google, Client: fp.client()})
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusFailed || out.Err.Code != api.CodeAuthFailed {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionExchangeErrorRedacted(t *testing.T) {
	fp := newFakeProvider(t)
	fp.exchangeError = "invalid_grant"
	fp.exchangeDesc = "code %CODE% is bad"
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	_, body := b.get(b.authorize(authURL))
	out := waitOutcome(t, m, id)
	if out.Status != StatusFailed || out.Err.Code != api.CodeAuthFailed {
		t.Fatalf("outcome %+v", out)
	}
	if strings.Contains(out.Err.Message, "authcode-") || !strings.Contains(out.Err.Message, "invalid_grant") {
		t.Errorf("message %q", out.Err.Message)
	}
	if !strings.Contains(body, "invalid_grant") || strings.Contains(body, "authcode") {
		t.Errorf("page:\n%s", body)
	}
}

func TestSessionWrongStateThenRight(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)
	good := u.Query()

	bad := url.Values{"code": {good.Get("code")}, "state": {"forged"}}
	resp, body := b.get("http://" + u.Host + "/?" + bad.Encode())
	if resp.StatusCode != http.StatusBadRequest || strings.Contains(body, "✗") {
		t.Fatalf("wrong state answered %d:\n%s", resp.StatusCode, body)
	}
	dup := url.Values{"code": {good.Get("code")}, "state": {good.Get("state"), good.Get("state")}}
	if resp, _ := b.get("http://" + u.Host + "/?" + dup.Encode()); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate state answered %d", resp.StatusCode)
	}
	if out, _ := m.Lookup(id); out.Status != StatusPending {
		t.Fatalf("session left pending: %+v", out)
	}
	if resp, _ := b.get(loc); resp.StatusCode != http.StatusOK {
		t.Fatalf("right state answered %d", resp.StatusCode)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionWrongStatesNeverFail(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)
	// However many stray requests arrive, none can end the session.
	for i := 0; i < 100; i++ {
		target := "http://" + u.Host + "/?state=nope"
		if i%2 == 1 {
			target = "http://" + u.Host + "/" // no state at all
		}
		resp, body := b.get(target)
		if resp.StatusCode != http.StatusBadRequest || strings.Contains(body, "✗") {
			t.Fatalf("request %d answered %d:\n%s", i, resp.StatusCode, body)
		}
	}
	if out, _ := m.Lookup(id); out.Status != StatusPending {
		t.Fatalf("session left pending: %+v", out)
	}
	if resp, _ := b.get(loc); resp.StatusCode != http.StatusOK {
		t.Fatalf("right state answered %d", resp.StatusCode)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionOtherRequests404(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)

	resp, body := b.get("http://" + u.Host + "/favicon.ico")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(body, "✓") || strings.Contains(body, "✗") {
		t.Errorf("other path %d:\n%s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("404 without headers")
	}
	post, err := b.c.Post(loc, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusNotFound {
		t.Errorf("POST answered %d", post.StatusCode)
	}
	if out, _ := m.Lookup(id); out.Status != StatusPending {
		t.Fatalf("session left pending: %+v", out)
	}
	b.get(loc)
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionExpiry(t *testing.T) {
	fp := newFakeProvider(t)
	clk := newClock()
	m := newTestManager(t, fp, func(o *Options) {
		o.Now = clk.Now
		o.TTL = 150 * time.Millisecond
	})
	id, authURL, expires, err := m.Start(googleReq(fp))
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(clk.Now().Add(150 * time.Millisecond)) {
		t.Errorf("expires %v", expires)
	}
	start := time.Now()
	out := waitOutcome(t, m, id)
	if out.Status != StatusExpired || out.Err.Code != api.CodeServerTimeout {
		t.Fatalf("outcome %+v", out)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("expiry took too long")
	}
	// The outcome stays readable for a moment, the listener is gone.
	if out, ok := m.Lookup(id); !ok || out.Status != StatusExpired {
		t.Errorf("lookup after expiry %+v %v", out, ok)
	}
	b := newBrowser(t)
	if _, err := b.c.Get(authURL); err != nil {
		t.Fatal(err)
	}
	q := fp.lastAuthQuery()
	if _, err := b.c.Get(q.Get("redirect_uri")); err == nil {
		t.Error("listener open after expiry")
	}
}

func TestSessionExpiryByClock(t *testing.T) {
	fp := newFakeProvider(t)
	clk := newClock()
	m := newTestManager(t, fp, func(o *Options) { o.Now = clk.Now })
	id, _, _, _ := m.Start(googleReq(fp))
	clk.Advance(defaultTTL)
	out, ok := m.Lookup(id)
	if !ok || out.Status != StatusExpired || out.Err.Code != api.CodeServerTimeout {
		t.Fatalf("outcome %+v %v", out, ok)
	}
	clk.Advance(expiredGrace)
	if _, ok := m.Lookup(id); ok {
		t.Error("expired session kept past its grace")
	}
}

func TestSessionFinishedRetainedUntilTTL(t *testing.T) {
	fp := newFakeProvider(t)
	clk := newClock()
	fp.now = clk.Now
	m := newTestManager(t, fp, func(o *Options) { o.Now = clk.Now })
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	waitOutcome(t, m, id)
	clk.Advance(defaultTTL - time.Second)
	if out, ok := m.Lookup(id); !ok || out.Status != StatusComplete {
		t.Fatalf("complete session lost early: %+v %v", out, ok)
	}
	clk.Advance(time.Second)
	if _, ok := m.Lookup(id); ok {
		t.Error("complete session kept past its TTL")
	}
	if _, ok := m.Consume(id); ok {
		t.Error("consumed after TTL")
	}
}

func TestSessionCancel(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	res := make(chan Outcome, 1)
	go func() {
		out, _ := m.Wait(context.Background(), id, 10*time.Second)
		res <- out
	}()
	time.Sleep(20 * time.Millisecond)
	if !m.Cancel(id) {
		t.Fatal("Cancel returned false")
	}
	select {
	case out := <-res:
		if out.Status != StatusCancelled || out.Err.Code != api.CodeCancelled {
			t.Fatalf("outcome %+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait not released by Cancel")
	}
	if m.Cancel(id) {
		t.Error("second Cancel returned true")
	}
	if m.Cancel("s_unknown") {
		t.Error("Cancel of an unknown session")
	}
	b := newBrowser(t)
	b.c.Get(authURL)
	if _, err := b.c.Get(fp.lastAuthQuery().Get("redirect_uri")); err == nil {
		t.Error("listener open after Cancel")
	}
}

func TestCloseReleasesWait(t *testing.T) {
	fp := newFakeProvider(t)
	m := NewManager(Options{HTTP: testHTTP(), Endpoints: fp.endpoints})
	id, _, _, _ := m.Start(googleReq(fp))
	res := make(chan Outcome, 1)
	go func() {
		out, _ := m.Wait(context.Background(), id, time.Minute)
		res <- out
	}()
	time.Sleep(20 * time.Millisecond)
	m.Close()
	select {
	case out := <-res:
		if out.Status != StatusCancelled {
			t.Fatalf("outcome %+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait not released by Close")
	}
	if _, _, _, err := m.Start(googleReq(fp)); apiCode(err) != api.CodeUnavailable {
		t.Errorf("Start after Close: %v", err)
	}
	m.Close() // idempotent
}

func TestWaitPendingAfterMax(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, _, _, _ := m.Start(googleReq(fp))
	start := time.Now()
	out, err := m.Wait(context.Background(), id, 100*time.Millisecond)
	if err != nil || out.Status != StatusPending || out.Err != nil {
		t.Fatalf("Wait = %+v, %v", out, err)
	}
	if d := time.Since(start); d < 90*time.Millisecond || d > 3*time.Second {
		t.Errorf("Wait took %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := m.Wait(ctx, id, time.Minute); err != nil || out.Status != StatusPending {
		t.Errorf("Wait with a done ctx = %+v, %v", out, err)
	}
	if _, err := m.Wait(context.Background(), "s_nope", time.Second); apiCode(err) != api.CodeInvalidArgument {
		t.Errorf("unknown session: %v", err)
	}
}

func TestKeyDedupeAndSetPage(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.Key = "acc_1"
	id1, url1, exp1, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Page = PageTexts{SuccessTitle: "Hotovo"}
	id2, url2, exp2, err := m.Start(req)
	if err != nil || id2 != id1 || url2 != url1 || !exp2.Equal(exp1) {
		t.Fatalf("dedupe: %s %s %v", id2, url2, err)
	}
	if pid, purl, ok := m.Pending("acc_1"); !ok || pid != id1 || purl != url1 {
		t.Errorf("Pending = %s %s %v", pid, purl, ok)
	}
	if _, _, ok := m.Pending(""); ok {
		t.Error("Pending(\"\")")
	}
	if _, _, ok := m.Pending("acc_2"); ok {
		t.Error("Pending of another key")
	}
	// No key never deduplicates.
	req.Key = ""
	id3, _, _, _ := m.Start(req)
	if id3 == id1 {
		t.Error("empty key deduplicated")
	}
	if !m.SetPage(id1, PageTexts{SuccessTitle: "Přihlášeno <b>"}) {
		t.Error("SetPage on a pending session")
	}
	b := newBrowser(t)
	_, body := b.get(b.authorize(url1))
	if !strings.Contains(body, "Přihlášeno &lt;b&gt;") {
		t.Errorf("page texts not updated:\n%s", body)
	}
	waitOutcome(t, m, id1)
	if m.SetPage(id1, PageTexts{}) {
		t.Error("SetPage on a finished session")
	}
	if _, _, ok := m.Pending("acc_1"); ok {
		t.Error("Pending returns a finished session")
	}
}

func TestMaxSessions(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, func(o *Options) { o.MaxSessions = 2 })
	id1, _, _, err1 := m.Start(googleReq(fp))
	_, _, _, err2 := m.Start(googleReq(fp))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if _, _, _, err := m.Start(googleReq(fp)); apiCode(err) != api.CodeUnavailable {
		t.Fatalf("third session: %v", err)
	}
	// A finished session frees its slot.
	m.Cancel(id1)
	if _, _, _, err := m.Start(googleReq(fp)); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
	if n := len(NewManager(Options{}).impl.sessions); n != 0 {
		t.Error("fresh manager has sessions")
	}
	if NewManager(Options{}).impl.max != defaultMaxSessions {
		t.Error("default MaxSessions")
	}
}

func TestStartValidation(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	if _, _, _, err := m.Start(StartRequest{Provider: Google}); apiCode(err) != api.CodeOAuthClientMissing {
		t.Errorf("no client: %v", err)
	}
	if _, _, _, err := m.Start(StartRequest{Provider: "custom", Client: Client{ID: "x"}}); apiCode(err) != api.CodeInvalidArgument {
		t.Errorf("unknown provider: %v", err)
	}
}

func TestOnCompleteBeforeWaitersAndCancelDuringExchange(t *testing.T) {
	fp := newFakeProvider(t)
	var released atomic.Bool
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.Key = "acc_c"
	var id string
	req.OnComplete = func(Grant) error {
		if out, _ := m.Lookup(id); out.Status != StatusPending {
			t.Error("waiters released before OnComplete")
		}
		released.Store(true)
		// Too late to cancel a session that is completing.
		if m.Cancel(id) {
			t.Error("Cancel succeeded during OnComplete")
		}
		// Its URL leads nowhere now: a keyed Pending does not return it.
		if _, _, ok := m.Pending("acc_c"); ok {
			t.Error("Pending returned a completing session")
		}
		return nil
	}
	id, authURL, _, _ := m.Start(req)
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusComplete || !released.Load() {
		t.Fatalf("outcome %+v", out)
	}
}

func TestSessionNoGoroutineLeak(t *testing.T) {
	fp := newFakeProvider(t)
	before := runtime.NumGoroutine()
	m := NewManager(Options{HTTP: testHTTP(), Endpoints: fp.endpoints})
	b := newBrowser(t)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b.get(b.authorize(authURL))
	waitOutcome(t, m, id)
	id2, _, _, _ := m.Start(googleReq(fp))
	m.Cancel(id2)
	m.Start(googleReq(fp))
	m.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= before+1 {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("%d goroutines before, %d after Close:\n%s", before, n, buf)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMalformedCallback(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)
	resp, body := b.get("http://" + u.Host + "/?state=" + url.QueryEscape(u.Query().Get("state")))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "authFailed") {
		t.Fatalf("page %d:\n%s", resp.StatusCode, body)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusFailed || out.Err.Code != api.CodeAuthFailed {
		t.Fatalf("outcome %+v", out)
	}
}

func TestExchangeNetworkError(t *testing.T) {
	fp := newFakeProvider(t)
	dead := func(p Provider, tenant string) Endpoints {
		e := fp.endpoints(p, tenant)
		e.TokenURL = "http://127.0.0.1:1/token"
		return e
	}
	m := newTestManager(t, fp, func(o *Options) { o.Endpoints = dead })
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	out := waitOutcome(t, m, id)
	if out.Status != StatusFailed || out.Err.Code != api.CodeNetworkError {
		t.Fatalf("outcome %+v", out)
	}
}

func TestIdentityErrorPassesThrough(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, func(o *Options) {
		o.Identity = map[Provider]IdentityFunc{Microsoft: func(_ context.Context, g Grant) (string, error) {
			return "", api.NewError(api.CodeServerError, "graph said no to %s", g.AccessToken)
		}}
	})
	id, authURL, _, _ := m.Start(StartRequest{Provider: Microsoft, Client: fp.client(), ExpectEmail: "a@b.c"})
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	out := waitOutcome(t, m, id)
	if out.Status != StatusFailed || out.Err.Code != api.CodeServerError || strings.Contains(out.Err.Message, "access-") {
		t.Fatalf("outcome %+v", out.Err)
	}
}

func TestSlowCallbackStillAnswers(t *testing.T) {
	old := writeTimeout
	writeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.OnComplete = func(Grant) error { time.Sleep(3 * writeTimeout); return nil }
	id, authURL, _, _ := m.Start(req)
	b := newBrowser(t)
	resp, body := b.get(b.authorize(authURL))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Signed in") {
		t.Fatalf("page %d:\n%s", resp.StatusCode, body)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}
