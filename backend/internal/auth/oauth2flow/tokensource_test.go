// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testAccount api.AccountID = "acc_7"

type tsFixture struct {
	fp    *fakeProvider
	kr    *memKeyring
	clk   *clock
	hooks atomic.Int32
	ts    *TokenSource
}

func newTSFixture(t *testing.T, provider Provider, storedRT string) *tsFixture {
	t.Helper()
	f := &tsFixture{fp: newFakeProvider(t), kr: newMemKeyring(), clk: newClock()}
	if storedRT != "" {
		f.fp.refreshValid[storedRT] = true
		f.kr.items[string(testAccount)+"/"+auth.KeyRefreshToken] = storedRT
	}
	f.ts = NewTokenSource(TokenSourceOptions{
		Provider:       provider,
		Client:         f.fp.client(),
		AccountID:      testAccount,
		Keyring:        f.kr,
		HTTP:           testHTTP(),
		Now:            f.clk.Now,
		Endpoints:      f.fp.endpoints,
		OnAuthRequired: func() { f.hooks.Add(1) },
	})
	return f
}

func (f *tsFixture) waitHooks(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.hooks.Load() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.hooks.Load(); got != n {
		t.Fatalf("OnAuthRequired ran %d times, want %d", got, n)
	}
}

func TestTokenSourceCachingAndSlack(t *testing.T) {
	f := newTSFixture(t, Google, "refresh-stored")
	ctx := context.Background()
	tok1, err := f.ts.AccessToken(ctx)
	if err != nil || tok1 == "" {
		t.Fatalf("AccessToken = %q, %v", tok1, err)
	}
	if n := f.fp.refreshCount(); n != 1 {
		t.Fatalf("%d refreshes", n)
	}
	form := f.fp.refreshForm(0)
	if form.Get("refresh_token") != "refresh-stored" || form.Get("client_id") != f.fp.clientID || form.Has("client_secret") {
		t.Errorf("refresh form %v", form)
	}
	// expires_in 3600, slack 60 s: cached until 3540 s.
	f.clk.Advance(3539 * time.Second)
	if tok, _ := f.ts.AccessToken(ctx); tok != tok1 || f.fp.refreshCount() != 1 {
		t.Fatalf("not cached within the slack: %q, %d refreshes", tok, f.fp.refreshCount())
	}
	f.clk.Advance(2 * time.Second)
	tok2, err := f.ts.AccessToken(ctx)
	if err != nil || tok2 == tok1 || f.fp.refreshCount() != 2 {
		t.Fatalf("no refresh past the slack: %q %v, %d refreshes", tok2, err, f.fp.refreshCount())
	}
	// Invalidate drops only the access token; the refresh token stays in
	// memory (no second keyring read).
	f.ts.Invalidate()
	if tok3, err := f.ts.AccessToken(ctx); err != nil || tok3 == tok2 {
		t.Fatalf("after Invalidate: %q %v", tok3, err)
	}
	if gets, _ := f.kr.counts(); gets != 1 {
		t.Errorf("keyring read %d times", gets)
	}
}

func TestTokenSourceSingleFlight(t *testing.T) {
	f := newTSFixture(t, Microsoft, "refresh-stored")
	gate := make(chan struct{})
	f.fp.gate = gate
	const n = 16
	var wg sync.WaitGroup
	toks := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			toks[i], errs[i] = f.ts.AccessToken(context.Background())
		}(i)
	}
	// Let every caller queue behind the one refresh, then release it.
	deadline := time.Now().Add(2 * time.Second)
	for f.fp.refreshCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	for i := range n {
		if errs[i] != nil || toks[i] == "" || toks[i] != toks[0] {
			t.Fatalf("caller %d: %q %v", i, toks[i], errs[i])
		}
	}
	if c := f.fp.refreshCount(); c != 1 {
		t.Fatalf("%d refresh requests, want 1", c)
	}
}

func TestTokenSourceCallerGivesUp(t *testing.T) {
	f := newTSFixture(t, Google, "refresh-stored")
	gate := make(chan struct{})
	f.fp.gate = gate
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.ts.AccessToken(ctx); apiCode(err) != api.CodeServerTimeout {
		t.Fatalf("err = %v", err)
	}
	close(gate)
	// The refresh finished for the next caller.
	tok, err := f.ts.AccessToken(context.Background())
	if err != nil || tok == "" || f.fp.refreshCount() != 1 {
		t.Fatalf("%q %v, %d refreshes", tok, err, f.fp.refreshCount())
	}
}

func TestTokenSourceRotation(t *testing.T) {
	f := newTSFixture(t, Microsoft, "refresh-stored")
	f.fp.rotate = true
	ctx := context.Background()
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	rotated := f.kr.value(testAccount)
	if rotated == "refresh-stored" || !strings.HasSuffix(rotated, "-rot") {
		t.Fatalf("keyring holds %q", rotated)
	}
	f.ts.Invalidate()
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.fp.refreshForm(1).Get("refresh_token"); got != rotated {
		t.Errorf("second refresh used %q, want the rotated %q", got, rotated)
	}
}

func TestTokenSourceRotationKeyringFailure(t *testing.T) {
	f := newTSFixture(t, Microsoft, "refresh-stored")
	f.fp.rotate = true
	f.kr.setErr = api.NewError(api.CodeKeyringError, "locked")
	ctx := context.Background()
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatalf("a failed store must not fail the refresh: %v", err)
	}
	// Kept in memory: the next refresh presents the rotated token.
	f.ts.Invalidate()
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.fp.refreshForm(1).Get("refresh_token"); !strings.HasSuffix(got, "-rot") {
		t.Errorf("second refresh used %q", got)
	}
}

func TestTokenSourceNoSecret(t *testing.T) {
	f := newTSFixture(t, Google, "")
	ctx := context.Background()
	if _, err := f.ts.AccessToken(ctx); apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("err = %v", err)
	}
	f.waitHooks(t, 1)
	if _, err := f.ts.AccessToken(ctx); apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("second err = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	f.waitHooks(t, 1)
	if gets, _ := f.kr.counts(); gets != 1 {
		t.Errorf("keyring read %d times", gets)
	}
	if f.fp.refreshCount() != 0 {
		t.Error("token endpoint asked without a refresh token")
	}
}

func TestTokenSourceKeyringErrors(t *testing.T) {
	f := newTSFixture(t, Google, "refresh-stored")
	f.kr.getErr = api.NewError(api.CodeKeyringError, "dismissed")
	if _, err := f.ts.AccessToken(context.Background()); apiCode(err) != api.CodeKeyringError {
		t.Fatalf("api error: %v", err)
	}
	f.kr.getErr = errors.New("bus gone")
	if _, err := f.ts.AccessToken(context.Background()); apiCode(err) != api.CodeKeyringError {
		t.Fatalf("plain error: %v", err)
	}
	if f.hooks.Load() != 0 {
		t.Error("keyring failure treated as a lost sign-in")
	}
	// Not sticky: once the keyring answers, the refresh works.
	f.kr.getErr = nil
	if _, err := f.ts.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTokenSourceInvalidGrant(t *testing.T) {
	f := newTSFixture(t, Microsoft, "refresh-stored")
	f.fp.refreshError = "invalid_grant"
	ctx := context.Background()
	_, err := f.ts.AccessToken(ctx)
	if apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "refresh-stored") {
		t.Errorf("error leaks the refresh token: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error does not name the provider's code: %v", err)
	}
	f.waitHooks(t, 1)
	for range 3 {
		if _, err := f.ts.AccessToken(ctx); apiCode(err) != api.CodeAuthRequired {
			t.Fatalf("later err = %v", err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	f.waitHooks(t, 1)
	if c := f.fp.refreshCount(); c != 1 {
		t.Errorf("%d token requests after invalid_grant", c)
	}

	// Seed ends it: the grant's access token is served without network,
	// the refresh token is stored.
	f.fp.refreshError = ""
	g := Grant{Provider: Microsoft, AccessToken: "access-seeded", RefreshToken: "refresh-seeded", Expiry: f.clk.Now().Add(time.Hour)}
	f.fp.refreshValid["refresh-seeded"] = true
	if err := f.ts.Seed(ctx, g); err != nil {
		t.Fatal(err)
	}
	if f.kr.value(testAccount) != "refresh-seeded" {
		t.Errorf("keyring holds %q", f.kr.value(testAccount))
	}
	if tok, err := f.ts.AccessToken(ctx); err != nil || tok != "access-seeded" || f.fp.refreshCount() != 1 {
		t.Fatalf("after Seed: %q %v, %d requests", tok, err, f.fp.refreshCount())
	}
	f.ts.Invalidate()
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.fp.refreshForm(1).Get("refresh_token"); got != "refresh-seeded" {
		t.Errorf("refresh after Seed used %q", got)
	}
	// A second loss notifies again.
	f.fp.refreshError = "invalid_grant"
	f.ts.Invalidate()
	f.ts.AccessToken(ctx)
	f.waitHooks(t, 2)
}

func TestTokenSourceErrorMapping(t *testing.T) {
	cases := []struct {
		code   string
		status int
		want   api.ErrorCode
	}{
		{"invalid_client", 401, api.CodeAuthFailed},
		{"unauthorized_client", 400, api.CodeAuthFailed},
		{"temporarily_unavailable", 503, api.CodeServerError},
		{"whatever", 500, api.CodeServerError},
		{"invalid_scope", 400, api.CodeAuthFailed},
		{"interaction_required", 400, api.CodeAuthRequired},
	}
	for _, c := range cases {
		f := newTSFixture(t, Google, "refresh-stored")
		f.fp.refreshError, f.fp.refreshStatus = c.code, c.status
		_, err := f.ts.AccessToken(context.Background())
		if apiCode(err) != c.want {
			t.Errorf("%s/%d: %v, want %s", c.code, c.status, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "refresh-stored") {
			t.Errorf("%s: token leaked: %v", c.code, err)
		}
		wantHooks := int32(0)
		if c.want == api.CodeAuthRequired {
			wantHooks = 1
		}
		f.waitHooks(t, wantHooks)
	}
}

func TestTokenSourceNetworkError(t *testing.T) {
	f := newTSFixture(t, Google, "refresh-stored")
	f.fp.srv.Close()
	_, err := f.ts.AccessToken(context.Background())
	if apiCode(err) != api.CodeNetworkError {
		t.Fatalf("err = %v", err)
	}
	if f.hooks.Load() != 0 {
		t.Error("network failure treated as a lost sign-in")
	}
}

func TestTokenSourceSeedKeyringFailure(t *testing.T) {
	f := newTSFixture(t, Google, "")
	ctx := context.Background()
	f.ts.AccessToken(ctx) // needs-auth
	f.waitHooks(t, 1)
	f.kr.setErr = api.NewError(api.CodeKeyringError, "keyring disabled")
	g := Grant{Provider: Google, AccessToken: "access-new", RefreshToken: "refresh-new-token", Expiry: f.clk.Now().Add(time.Hour)}
	err := f.ts.Seed(ctx, g)
	if apiCode(err) != api.CodeKeyringError || strings.Contains(err.Error(), "refresh-new-token") {
		t.Fatalf("Seed = %v", err)
	}
	// Nothing changed: still needs a sign-in, nothing cached.
	if _, err := f.ts.AccessToken(ctx); apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("after a failed Seed: %v", err)
	}
	f.kr.setErr = errors.New("not an api error")
	if err := f.ts.Seed(ctx, g); apiCode(err) != api.CodeKeyringError {
		t.Fatalf("plain keyring error: %v", err)
	}
	f.kr.setErr = nil
	if err := f.ts.Seed(ctx, Grant{AccessToken: "x"}); apiCode(err) != api.CodeInvalidArgument {
		t.Fatalf("grant without a refresh token: %v", err)
	}
	if err := f.ts.Seed(ctx, g); err != nil {
		t.Fatal(err)
	}
	if tok, err := f.ts.AccessToken(ctx); err != nil || tok != "access-new" {
		t.Fatalf("after Seed: %q %v", tok, err)
	}
}

func TestTokenSourceSeedDuringRefresh(t *testing.T) {
	f := newTSFixture(t, Google, "refresh-stored")
	f.fp.refreshError = "invalid_grant"
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
	g := Grant{Provider: Google, AccessToken: "access-new", RefreshToken: "refresh-new", Expiry: f.clk.Now().Add(time.Hour)}
	if err := f.ts.Seed(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-res; err != nil {
		t.Fatalf("the stale refresh's failure reached the caller: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if f.hooks.Load() != 0 {
		t.Error("stale invalid_grant ran the hook")
	}
	if tok, err := f.ts.AccessToken(context.Background()); err != nil || tok != "access-new" {
		t.Fatalf("after: %q %v", tok, err)
	}
}

func TestNewHTTPClientRefusesRedirects(t *testing.T) {
	c := NewHTTPClient()
	if c.Timeout != httpTimeout || c.CheckRedirect == nil || c.CheckRedirect(nil, nil) == nil {
		t.Fatalf("client %+v", c)
	}
}

func TestTokenSourceStaleRotationNotStored(t *testing.T) {
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
	g := Grant{Provider: Microsoft, AccessToken: "access-new", RefreshToken: "refresh-seeded", Expiry: f.clk.Now().Add(time.Hour)}
	if err := f.ts.Seed(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if got := f.kr.value(testAccount); got != "refresh-seeded" {
		t.Fatalf("keyring holds %q, want the seeded token", got)
	}
}
