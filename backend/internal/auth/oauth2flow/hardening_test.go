// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The findings of the flow's security review: DNS rebinding, connection
// cap, keyed-session binding, discarding a complete session, the
// OnComplete veto, ID token claims, identity clean-up, and retiring a
// token source.

func TestSessionRefusesOtherHosts(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)
	_, port, _ := net.SplitHostPort(u.Host)

	for _, host := range []string{"localhost:" + port, "evil.example:" + port, "127.0.0.1", "127.0.0.1:1", "[::1]:" + port} {
		req, _ := http.NewRequest(http.MethodGet, loc, nil)
		req.Host = host
		resp, err := b.c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Host %q answered %d", host, resp.StatusCode)
		}
	}
	fp.mu.Lock()
	exchanges := len(fp.exchangeForms)
	fp.mu.Unlock()
	if out, _ := m.Lookup(id); out.Status != StatusPending || exchanges != 0 {
		t.Fatalf("a foreign Host reached the session: %+v, %d exchanges", out, exchanges)
	}
	if resp, _ := b.get(loc); resp.StatusCode != http.StatusOK {
		t.Fatalf("the real redirect answered %d", resp.StatusCode)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestLimitListener(t *testing.T) {
	raw, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newLimitListener(raw, 1)
	defer l.Close()
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("Accept: %v", err)
				}
				close(accepted)
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp4", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1, c2 := dial(), dial()
	defer c1.Close()
	defer c2.Close()
	first := <-accepted
	select {
	case <-accepted:
		t.Fatal("a second connection accepted while the first is open")
	case <-time.After(100 * time.Millisecond):
	}
	first.Close()
	first.Close() // the slot is freed once
	select {
	case second := <-accepted:
		second.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the second connection not accepted after the first closed")
	}
	l.Close()
	select {
	case _, ok := <-accepted:
		if ok {
			t.Fatal("accepted after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept not ended by Close")
	}
}

func TestSessionServesDespiteIdleConnections(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	loc := b.authorize(authURL)
	u, _ := url.Parse(loc)
	for i := 0; i < maxConns-1; i++ {
		c, err := net.Dial("tcp4", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	if resp, _ := b.get(loc); resp.StatusCode != http.StatusOK {
		t.Fatalf("callback answered %d", resp.StatusCode)
	}
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestKeyedSessionReplacedWhenTheAccountChanged(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	base := StartRequest{Provider: Microsoft, Client: Client{ID: fp.clientID, Tenant: "contoso"}, ExpectEmail: "me@contoso.com", Key: "acc_1"}
	id1, _, _, err := m.Start(base)
	if err != nil {
		t.Fatal(err)
	}
	// The same address, however written, reuses the session.
	same := base
	same.ExpectEmail = "  Me@Contoso.COM "
	if id, _, _, err := m.Start(same); err != nil || id != id1 {
		t.Fatalf("same binding: %s %v", id, err)
	}
	prev := id1
	for name, mod := range map[string]func(*StartRequest){
		"address":  func(r *StartRequest) { r.ExpectEmail = "other@contoso.com" },
		"client":   func(r *StartRequest) { r.Client.ID = "another-client" },
		"tenant":   func(r *StartRequest) { r.Client.Tenant = "fabrikam" },
		"provider": func(r *StartRequest) { r.Provider = Google },
	} {
		req := base
		mod(&req)
		id, _, _, err := m.Start(req)
		if err != nil || id == prev {
			t.Fatalf("%s: %s %v", name, id, err)
		}
		if out, _ := m.Lookup(prev); out.Status != StatusCancelled {
			t.Fatalf("%s: the replaced session is %+v", name, out)
		}
		if pid, _, ok := m.Pending("acc_1"); !ok || pid != id {
			t.Fatalf("%s: Pending = %s %v", name, pid, ok)
		}
		// Back to the base binding for the next case.
		if prev, _, _, err = m.Start(base); err != nil || prev == id {
			t.Fatalf("%s: back: %s %v", name, prev, err)
		}
	}
}

func TestCancelDiscardsACompleteSession(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
	if !m.Cancel(id) {
		t.Fatal("Cancel of a complete session returned false")
	}
	if _, ok := m.Lookup(id); ok {
		t.Error("discarded session still known")
	}
	if _, ok := m.Grant(id); ok {
		t.Error("discarded session still has its grant")
	}
	if m.Cancel(id) {
		t.Error("second Cancel returned true")
	}

	// A failed session stays readable: Cancel ignores it.
	fp.idEmail = "other@example.org"
	id, authURL, _, _ = m.Start(googleReq(fp))
	b.get(b.authorize(authURL))
	waitOutcome(t, m, id)
	if m.Cancel(id) {
		t.Error("Cancel of a failed session returned true")
	}
	if out, ok := m.Lookup(id); !ok || out.Status != StatusFailed {
		t.Errorf("failed session after Cancel: %+v %v", out, ok)
	}
}

func TestOnCompleteErrorFailsTheSession(t *testing.T) {
	for name, tc := range map[string]struct {
		err  func(Grant) error
		code api.ErrorCode
	}{
		"api error": {func(g Grant) error {
			return api.NewError(api.CodeKeyringError, "cannot store %s", g.RefreshToken)
		}, api.CodeKeyringError},
		"plain error": {func(g Grant) error { return errors.New("boom " + g.AccessToken) }, api.CodeInternalError},
	} {
		t.Run(name, func(t *testing.T) {
			fp := newFakeProvider(t)
			m := newTestManager(t, fp, nil)
			req := googleReq(fp)
			req.OnComplete = tc.err
			id, authURL, _, _ := m.Start(req)
			b := newBrowser(t)
			resp, body := b.get(b.authorize(authURL))
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Sign-in failed") || !strings.Contains(body, tc.code.String()) {
				t.Fatalf("page %d:\n%s", resp.StatusCode, body)
			}
			out := waitOutcome(t, m, id)
			if out.Status != StatusFailed || out.Err == nil || out.Err.Code != tc.code {
				t.Fatalf("outcome %+v", out)
			}
			if strings.Contains(out.Err.Message, "refresh-") || strings.Contains(out.Err.Message, "access-") {
				t.Errorf("token in the message %q", out.Err.Message)
			}
			if _, ok := m.Grant(id); ok {
				t.Error("grant kept after the veto")
			}
		})
	}
}

func TestGrantNamesItsClient(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, func(o *Options) {
		o.Identity = map[Provider]IdentityFunc{Microsoft: func(context.Context, Grant) (string, error) { return "me@contoso.com", nil }}
	})
	id, authURL, _, _ := m.Start(StartRequest{Provider: Microsoft, Client: Client{ID: fp.clientID, Tenant: "contoso"}, ExpectEmail: "me@contoso.com"})
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	waitOutcome(t, m, id)
	g, ok := m.Grant(id)
	if !ok || g.ClientID != fp.clientID || g.Tenant != "contoso" || g.Email != "me@contoso.com" {
		t.Fatalf("grant %+v %v", g, ok)
	}
}

func TestSessionIDTokenClaims(t *testing.T) {
	for name, mod := range map[string]func(*fakeProvider){
		"unverified address": func(fp *fakeProvider) { fp.idClaims = map[string]any{"email_verified": false} },
		"no email_verified":  func(fp *fakeProvider) { fp.idClaims = map[string]any{"email_verified": nil} },
		"expired": func(fp *fakeProvider) {
			fp.now = func() time.Time { return time.Now().Add(-time.Hour - idTokenLeeway - time.Minute) }
		},
		"no exp":             func(fp *fakeProvider) { fp.idClaims = map[string]any{"exp": nil} },
		"array without azp":  func(fp *fakeProvider) { fp.idClaims = map[string]any{"aud": []string{fp.clientID, "other"}} },
		"foreign azp":        func(fp *fakeProvider) { fp.idClaims = map[string]any{"azp": "other"} },
		"bidi override mail": func(fp *fakeProvider) { fp.idEmail = "user" + string(rune(0x202E)) + "@example.org" },
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
	// An array audience with the client as azp is fine.
	fp := newFakeProvider(t)
	fp.idClaims = map[string]any{"aud": []string{fp.clientID, "other"}, "azp": fp.clientID}
	m := newTestManager(t, fp, nil)
	id, authURL, _, _ := m.Start(googleReq(fp))
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
}

func TestIDTokenEmail(t *testing.T) {
	const client = "client-1"
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	base := func() map[string]any {
		return map[string]any{"iss": "https://accounts.google.com", "aud": client, "email": "a@example.org",
			"email_verified": true, "exp": now.Add(time.Hour).Unix()}
	}
	cases := []struct {
		name string
		mod  func(map[string]any)
		ok   bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"issuer without scheme", func(c map[string]any) { c["iss"] = "accounts.google.com" }, true},
		{"verified as a string", func(c map[string]any) { c["email_verified"] = "true" }, true},
		{"unverified string", func(c map[string]any) { c["email_verified"] = "false" }, false},
		{"verified missing", func(c map[string]any) { delete(c, "email_verified") }, false},
		{"verified null", func(c map[string]any) { c["email_verified"] = nil }, false},
		{"exp within the leeway", func(c map[string]any) { c["exp"] = now.Add(-idTokenLeeway + time.Second).Unix() }, true},
		{"exp past the leeway", func(c map[string]any) { c["exp"] = now.Add(-idTokenLeeway - time.Second).Unix() }, false},
		{"exp as a string", func(c map[string]any) { c["exp"] = "4102444800" }, false},
		{"exp zero", func(c map[string]any) { c["exp"] = 0 }, false},
		{"exp fractional", func(c map[string]any) { c["exp"] = float64(now.Add(time.Hour).Unix()) + 0.5 }, true},
		{"array aud with azp", func(c map[string]any) { c["aud"], c["azp"] = []string{"x", client}, client }, true},
		{"array aud without azp", func(c map[string]any) { c["aud"] = []string{client} }, false},
		{"array aud, foreign azp", func(c map[string]any) { c["aud"], c["azp"] = []string{client, "x"}, "x" }, false},
		{"array aud without the client", func(c map[string]any) { c["aud"], c["azp"] = []string{"x"}, client }, false},
		{"string aud, foreign azp", func(c map[string]any) { c["azp"] = "x" }, false},
		{"string aud, matching azp", func(c map[string]any) { c["azp"] = client }, true},
		{"zero-width space", func(c map[string]any) { c["email"] = "a" + string(rune(0x200B)) + "@example.org" }, false},
	}
	for _, tc := range cases {
		c := base()
		tc.mod(c)
		email, err := idTokenEmail(fakeIDToken(c), client, now)
		if tc.ok && (err != nil || email != "a@example.org") {
			t.Errorf("%s: %q, %v", tc.name, email, err)
		}
		if !tc.ok && (err == nil || email != "") {
			t.Errorf("%s: accepted %q", tc.name, email)
		}
	}
}

func TestCleanEmailRefusesFormatCharacters(t *testing.T) {
	for _, r := range []rune{0x202E, 0x202D, 0x200B, 0x200E, 0x2066, 0xFEFF, 0x00AD} {
		s := "user" + string(r) + "@example.org"
		if got := cleanEmail(s); got != "" {
			t.Errorf("U+%04X kept: %q", r, got)
		}
	}
	if got := cleanEmail(" Ann@Example.org "); got != "Ann@Example.org" {
		t.Errorf("plain address: %q", got)
	}
}

func TestNormalizeAddressMatchesStore(t *testing.T) {
	for _, s := range []string{"", "a@b.c", " A@B.C ", "\tMe@Example.ORG\n", "ÉLOÏSE@Example.org", "x@ÖBB.at"} {
		if got, want := normalizeAddress(s), store.NormalizeAddress(s); got != want {
			t.Errorf("normalizeAddress(%q) = %q, store says %q", s, got, want)
		}
	}
}

func TestTokenSourceRetire(t *testing.T) {
	f := newTSFixture(t, Microsoft, "refresh-stored")
	f.fp.rotate = true
	gate := make(chan struct{})
	f.fp.gate = gate
	res := make(chan error, 1)
	go func() {
		_, err := f.ts.AccessToken(context.Background())
		res <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for f.fp.refreshCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f.ts.Retire()
	close(gate)
	if err := <-res; err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := f.kr.value(testAccount); got != "refresh-stored" {
		t.Fatalf("a retired source stored %q", got)
	}
	g := Grant{Provider: Microsoft, AccessToken: "access-new", RefreshToken: "refresh-late", Expiry: f.clk.Now().Add(time.Hour)}
	if err := f.ts.Seed(context.Background(), g); apiCode(err) != api.CodeConflict {
		t.Fatalf("Seed after Retire: %v", err)
	}
	if got := f.kr.value(testAccount); got != "refresh-stored" {
		t.Fatalf("Seed after Retire stored %q", got)
	}
	// A lost sign-in of a retired source runs no hook.
	f.fp.mu.Lock()
	f.fp.gate, f.fp.refreshError = nil, "invalid_grant"
	f.fp.mu.Unlock()
	f.ts.Invalidate()
	if _, err := f.ts.AccessToken(context.Background()); apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("revoked: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if f.hooks.Load() != 0 {
		t.Error("retired source ran OnAuthRequired")
	}
}

// blockingKeyring holds every Set until release is closed.
type blockingKeyring struct {
	*memKeyring
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (k *blockingKeyring) Set(ctx context.Context, a api.AccountID, key, value string) error {
	k.once.Do(func() { close(k.started) })
	<-k.release
	return k.memKeyring.Set(ctx, a, key, value)
}

func TestTokenSourceRetireWaitsForAWrite(t *testing.T) {
	fp := newFakeProvider(t)
	fp.rotate = true
	fp.refreshValid["refresh-stored"] = true
	kr := &blockingKeyring{memKeyring: newMemKeyring(), started: make(chan struct{}), release: make(chan struct{})}
	kr.items[string(testAccount)+"/"+auth.KeyRefreshToken] = "refresh-stored"
	ts := NewTokenSource(TokenSourceOptions{
		Provider: Microsoft, Client: fp.client(), AccountID: testAccount, Keyring: kr,
		HTTP: testHTTP(), Endpoints: fp.endpoints,
	})
	res := make(chan error, 1)
	go func() {
		_, err := ts.AccessToken(context.Background())
		res <- err
	}()
	select {
	case <-kr.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the rotated token was never written")
	}
	retired := make(chan struct{})
	go func() {
		ts.Retire()
		close(retired)
	}()
	select {
	case <-retired:
		t.Fatal("Retire returned while a keyring write was under way")
	case <-time.After(50 * time.Millisecond):
	}
	close(kr.release)
	select {
	case <-retired:
	case <-time.After(3 * time.Second):
		t.Fatal("Retire never returned")
	}
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	// The write that was under way landed; nothing after Retire does.
	if got := kr.value(testAccount); !strings.HasSuffix(got, "-rot") {
		t.Fatalf("keyring holds %q", got)
	}
}
