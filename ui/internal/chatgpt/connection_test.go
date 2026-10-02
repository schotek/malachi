// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	gate chan struct{}
	r    *Registration
	t    *Tokens
}

func newMemoryStore() *memoryStore { return &memoryStore{gate: make(chan struct{}, 1)} }
func (m *memoryStore) Acquire(ctx context.Context) (func(), error) {
	select {
	case m.gate <- struct{}{}:
		return func() { <-m.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *memoryStore) ReadRegistration(context.Context) (*Registration, error) {
	if m.r == nil {
		return nil, nil
	}
	r := *m.r
	return &r, nil
}
func (m *memoryStore) WriteRegistration(_ context.Context, r Registration) error {
	m.r = &r
	return nil
}
func (m *memoryStore) ReadTokens(context.Context) (*Tokens, error) {
	if m.t == nil {
		return nil, nil
	}
	t := *m.t
	return &t, nil
}
func (m *memoryStore) WriteTokens(_ context.Context, t Tokens) error { m.t = &t; return nil }
func (m *memoryStore) DeleteTokens(context.Context) error            { m.t = nil; return nil }

type browserFunc func(context.Context, func(string) (string, error), string) (string, error)

func (f browserFunc) Authorize(ctx context.Context, fn func(string) (string, error), state string) (string, error) {
	return f(ctx, fn, state)
}
func signed(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test"})
	b, _ := json.Marshal(claims)
	content := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
	digest := sha256.Sum256([]byte(content))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return content + "." + base64.RawURLEncoding.EncodeToString(signature)
}
func jsonResponse(value any, status int) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b)))}
}
func identityClient(t *testing.T, key *rsa.PrivateKey, tokenHandler roundTrip) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			return jsonResponse(map[string]any{"issuer": Issuer, "jwks_uri": Issuer + "/jwks", "revocation_endpoint": Issuer + "/revoke"}, 200), nil
		case "/jwks":
			return jsonResponse(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}}, 200), nil
		default:
			return tokenHandler(r)
		}
	})}
}
func TestSignedIdentityAttacks(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := protocol{identityClient(t, key, func(r *http.Request) (*http.Response, error) { t.Fatal(r.URL); return nil, nil }), func() time.Time { return now }}
	base := func() map[string]any {
		return map[string]any{"iss": Issuer, "aud": "client", "sub": "subject", "nonce": "nonce", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "email": "person@example.test"}
	}
	token := signed(t, key, base())
	sub, email, err := p.validate(context.Background(), token, "client", "nonce", "")
	if err != nil || sub != "subject" || email != "person@example.test" {
		t.Fatalf("valid failed %s %s %v", sub, email, err)
	}
	for name, mutation := range map[string]func(map[string]any){"issuer": func(c map[string]any) { c["iss"] = "https://evil.test" }, "audience": func(c map[string]any) { c["aud"] = "other" }, "nonce": func(c map[string]any) { c["nonce"] = "other" }, "expired": func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() }, "future": func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() }, "multiAudience": func(c map[string]any) { c["aud"] = []string{"client", "other"} }, "authorizedParty": func(c map[string]any) { c["azp"] = "other" }, "subject": func(c map[string]any) { c["sub"] = "" }, "notBefore": func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }} {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutation(c)
			if _, _, err := p.validate(context.Background(), signed(t, key, c), "client", "nonce", ""); err == nil {
				t.Fatal("forged claims accepted")
			}
		})
	}
	parts := strings.Split(token, ".")
	parts[2] = "invalid"
	if _, _, err = p.validate(context.Background(), strings.Join(parts, "."), "client", "nonce", ""); err == nil {
		t.Fatal("signature forgery accepted")
	}
	if _, _, err = p.validate(context.Background(), token, "client", "nonce", "different"); err == nil || err.Error() != "chatgpt_identity_mismatch" {
		t.Fatal("identity replacement allowed")
	}
}
func TestConnectionSignInRotationAndCrossInstance(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	nonce := ""
	refreshes := 0
	client := identityClient(t, key, func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		if r.URL.Path == "/revoke" {
			return jsonResponse(map[string]any{}, 503), nil
		}
		if r.Form.Get("resource") != Resource {
			t.Fatal("resource omitted")
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			refreshes++
			return jsonResponse(map[string]any{"access_token": "rotated", "refresh_token": "refresh2", "token_type": "Bearer", "expires_in": 3600}, 200), nil
		}
		claims := map[string]any{"iss": Issuer, "aud": "issued-client", "sub": "person", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "email": "person@example.test"}
		return jsonResponse(map[string]any{"access_token": "original", "refresh_token": "refresh1", "id_token": signed(t, key, claims), "scope": Scope, "token_type": "Bearer", "expires_in": 3600}, 200), nil
	})
	browser := browserFunc(func(ctx context.Context, fn func(string) (string, error), state string) (string, error) {
		redirect := "http://127.0.0.1:12345" + CallbackPath
		u, err := fn(redirect)
		if err != nil {
			return "", err
		}
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		nonce = q.Get("nonce")
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") != state || q.Get("client_id") != DynamicClient || !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(q.Get("ext_agent_host_id")) {
			t.Fatalf("PKCE/host binding %v", q)
		}
		return redirect + "?" + url.Values{"state": {state}, "client_id": {"issued-client"}, "code": {"single-use"}}.Encode(), nil
	})
	s := NewConnectionService(client, store, browser)
	defer s.Close()
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.IsReady() || s.Connection().Email != "person@example.test" {
		t.Fatal(s.Connection())
	}
	store.t.ExpiresAt = time.Now()
	second := NewConnectionService(client, store, browser)
	defer second.Close()
	var wg sync.WaitGroup
	for _, svc := range []*ConnectionService{s, second} {
		wg.Add(1)
		go func(svc *ConnectionService) {
			defer wg.Done()
			token, err := svc.GetAccessToken(context.Background())
			if err != nil || token != "rotated" {
				t.Errorf("rotation %s %v", token, err)
			}
		}(svc)
	}
	wg.Wait()
	if refreshes != 1 || store.t.RefreshToken != "refresh2" {
		t.Fatal("rotation not serialized")
	}
	if err = s.InferenceRejected(context.Background(), "original", 401); err != nil || store.t == nil {
		t.Fatal("stale response erased rotation")
	}
	session := s.SessionContext()
	confirmed, err := s.Disconnect(context.Background())
	if err != nil || confirmed || store.t != nil || session.Err() == nil {
		t.Fatal("failed remote revocation retained local tokens")
	}
	if _, err = second.GetAccessToken(context.Background()); err == nil || second.Connection().Status != Disconnected {
		t.Fatal("other instance reused deleted token")
	}
}
func TestCallbackStateAndDuplicates(t *testing.T) {
	redirect := "http://127.0.0.1:10000" + CallbackPath
	for _, callback := range []string{redirect + "?state=wrong", redirect + "?state=s&state=s", redirect + "?state=s#fragment", "http://evil.test/auth/callback?state=s", redirect + "?state=s&code=a&code=b"} {
		if _, err := parseCallback(callback, redirect, "s"); err == nil {
			t.Fatalf("accepted %s", callback)
		}
	}
}
func TestSignInCancellationDropsLateCallbacks(t *testing.T) {
	store := newMemoryStore()
	started := make(chan struct{})
	s := NewConnectionService(nil, store, browserFunc(func(ctx context.Context, _ func(string) (string, error), _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}))
	defer s.Close()
	done := make(chan error, 1)
	go func() { done <- s.SignIn(context.Background()) }()
	<-started
	s.CancelSignIn()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("browser did not cancel")
	}
	if s.Connection().Status != Disconnected || store.t != nil {
		t.Fatal("cancel installed credential")
	}
}

func TestOptionalTokensAndMalformedLifetimeAreRejected(t *testing.T) {
	for _, body := range []string{`{"access_token":"a","token_type":"Bearer","expires_in":3600,"refresh_token":""}`, `{"access_token":"a","token_type":"Bearer","expires_in":3600,"id_token":null}`, `{"access_token":"a","token_type":"Bearer","expires_in":3600,"refresh_token":" "}`, `{"access_token":"a","token_type":"Bearer","expires_in":604801}`, `{"access_token":"a","token_type":"Bearer","expires_in":3600,"scope":""}`} {
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		p := protocol{client, time.Now}
		if _, err := p.exchange(context.Background(), url.Values{}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestCurrentToken401AndPlanLimits(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		store := newMemoryStore()
		store.r = &Registration{HostID: hostID(), ClientID: "client", Subject: "subject", ConnectionID: "connection"}
		store.t = &Tokens{ConnectionID: "connection", AccessToken: "current", RefreshToken: "refresh", Scope: Scope, ExpiresAt: time.Now().Add(time.Hour)}
		s := NewConnectionService(nil, store, nil)
		if err := s.Initialize(context.Background()); err != nil {
			t.Fatal(err)
		}
		session := s.SessionContext()
		if err := s.InferenceRejected(context.Background(), "current", status); err != nil {
			t.Fatal(err)
		}
		if status == 401 {
			if store.t != nil || session.Err() == nil || s.Connection().Status != ReconnectRequired {
				t.Fatal("401 retained rejected grant")
			}
		} else if store.t == nil || session.Err() != nil || !s.IsReady() {
			t.Fatal("model/plan denial erased grant")
		}
		s.Close()
	}
}

type brokenSnapshotStore struct {
	*memoryStore
	registrationBroken bool
	tokensBroken       bool
	deleted            bool
}

func (m *brokenSnapshotStore) ReadRegistration(ctx context.Context) (*Registration, error) {
	if m.registrationBroken {
		return nil, authError("storage")
	}
	return m.memoryStore.ReadRegistration(ctx)
}
func (m *brokenSnapshotStore) ReadTokens(ctx context.Context) (*Tokens, error) {
	if m.tokensBroken {
		return nil, authError("storage")
	}
	return m.memoryStore.ReadTokens(ctx)
}
func (m *brokenSnapshotStore) DeleteTokens(ctx context.Context) error {
	m.deleted = true
	return m.memoryStore.DeleteTokens(ctx)
}
func TestDisconnectDeletesDespiteBrokenSnapshots(t *testing.T) {
	for _, brokenRegistration := range []bool{false, true} {
		store := &brokenSnapshotStore{memoryStore: newMemoryStore(), registrationBroken: brokenRegistration, tokensBroken: !brokenRegistration}
		store.r = &Registration{HostID: hostID(), ClientID: "client", Subject: "subject", ConnectionID: "connection"}
		store.t = &Tokens{ConnectionID: "connection", AccessToken: "token", RefreshToken: "refresh"}
		s := NewConnectionService(nil, store, nil)
		_, err := s.Disconnect(context.Background())
		s.Close()
		if err == nil || !store.deleted || store.t != nil || s.Connection().Status != Disconnected {
			t.Fatal("malformed snapshot retained local grant")
		}
	}
}
func TestDisconnectDeletesBeforeRemoteRevocation(t *testing.T) {
	store := newMemoryStore()
	store.r = &Registration{HostID: hostID(), ClientID: "client", Subject: "subject", ConnectionID: "connection"}
	store.t = &Tokens{ConnectionID: "connection", AccessToken: "token", RefreshToken: "refresh"}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if store.t != nil {
			t.Fatal("revocation started before local deletion")
		}
		if r.URL.Path == "/.well-known/openid-configuration" {
			return jsonResponse(map[string]any{"issuer": Issuer, "revocation_endpoint": Issuer + "/revoke"}, 200), nil
		}
		return jsonResponse(map[string]any{}, 200), nil
	})}
	s := NewConnectionService(client, store, nil)
	defer s.Close()
	confirmed, err := s.Disconnect(context.Background())
	if err != nil || !confirmed || store.t != nil {
		t.Fatal("disconnect revocation failed")
	}
}
