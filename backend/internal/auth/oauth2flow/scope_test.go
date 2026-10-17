// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	scopeCalRW     = "https://graph.microsoft.com/Calendars.ReadWrite"
	scopeCalShared = "https://graph.microsoft.com/Calendars.Read.Shared"
)

func authScopes(t *testing.T, authURL string) []string {
	t.Helper()
	q, err := url.ParseQuery(authURL[strings.IndexByte(authURL, '?')+1:])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(q.Get("scope"))
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Only a calendar session asks for the calendar scopes; the mail scopes
// stay in both.
func TestCalendarSessionScopes(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	_, plain, _, err := m.Start(StartRequest{Provider: Microsoft, Client: fp.client()})
	if err != nil {
		t.Fatal(err)
	}
	_, cal, _, err := m.Start(StartRequest{Provider: Microsoft, Client: fp.client(), Calendar: true})
	if err != nil {
		t.Fatal(err)
	}
	ps, cs := authScopes(t, plain), authScopes(t, cal)
	for _, s := range providerScopes[Microsoft] {
		if !has(ps, s) || !has(cs, s) {
			t.Errorf("mail scope %s missing: plain %v, calendar %v", s, ps, cs)
		}
	}
	for _, s := range []string{scopeCalRW, scopeCalShared} {
		if has(ps, s) {
			t.Errorf("plain sign-in asks for %s: %v", s, ps)
		}
		if !has(cs, s) {
			t.Errorf("calendar sign-in lacks %s: %v", s, cs)
		}
	}
}

// Google has no calendar provider yet: a calendar session is refused.
func TestCalendarSessionRefusedForGoogle(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	req := googleReq(fp)
	req.Calendar = true
	if _, _, _, err := m.Start(req); apiCode(err) != api.CodeInvalidArgument {
		t.Fatalf("google calendar session: %v", err)
	}
	if SupportsCalendar(Google) || !SupportsCalendar(Microsoft) {
		t.Fatal("SupportsCalendar")
	}
}

// A pending keyed session is reused only with the same Calendar.
func TestCalendarSessionNotReusedForOtherScopes(t *testing.T) {
	fp := newFakeProvider(t)
	m := newTestManager(t, fp, nil)
	req := StartRequest{Provider: Microsoft, Client: fp.client(), Key: "acc_1"}
	first, _, _, err := m.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	again, _, _, _ := m.Start(req)
	if again != first {
		t.Fatalf("same request not reused: %s, %s", first, again)
	}
	req.Calendar = true
	cal, authURL, _, err := m.Start(req)
	if err != nil || cal == first || !has(authScopes(t, authURL), scopeCalRW) {
		t.Fatalf("calendar request: %s %v", cal, err)
	}
	if out, _ := m.Lookup(first); out.Status != StatusCancelled {
		t.Fatalf("replaced session %+v", out)
	}
}

// signInGrant completes one Microsoft session against fp.
func signInGrant(t *testing.T, fp *fakeProvider, calendar bool) Grant {
	t.Helper()
	fp.idEmail = ""
	m := newTestManager(t, fp, nil)
	id, authURL, _, err := m.Start(StartRequest{Provider: Microsoft, Client: fp.client(), Calendar: calendar})
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t)
	b.get(b.authorize(authURL))
	if out := waitOutcome(t, m, id); out.Status != StatusComplete {
		t.Fatalf("outcome %+v", out)
	}
	g, ok := m.Consume(id)
	if !ok {
		t.Fatal("no grant")
	}
	return g
}

// The grant records the response's scope, or the requested set without
// one; the recorded scope says whether the calendar is granted.
func TestGrantRecordsScope(t *testing.T) {
	cases := []struct {
		name     string
		calendar bool
		response string
		want     bool
	}{
		{"calendar, response names it", true, "Calendars.ReadWrite Calendars.Read.Shared Mail.ReadWrite Mail.Send User.Read", true},
		{"calendar, response without scope", true, "", true},
		{"calendar asked, consent narrowed", true, "Mail.ReadWrite Mail.Send User.Read", false},
		{"plain, response without scope", false, "", false},
		{"plain, earlier consent carried", false, "https://graph.microsoft.com/Calendars.ReadWrite https://graph.microsoft.com/Mail.ReadWrite", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fp := newFakeProvider(t)
			fp.exchangeScope = c.response
			g := signInGrant(t, fp, c.calendar)
			if g.Scope == "" {
				t.Fatal("no scope recorded")
			}
			if c.response != "" && g.Scope != c.response {
				t.Errorf("scope %q, want the response's %q", g.Scope, c.response)
			}
			if g.HasCalendarScope() != c.want {
				t.Errorf("HasCalendarScope(%q) = %v, want %v", g.Scope, !c.want, c.want)
			}
		})
	}
}

// The refresh-token item is always the bare token; a calendar grant adds
// KeyGrantedScope, a later grant or refresh answer without the calendar
// removes it; a refresh asks for no scope and rotation keeps the item.
func TestStoredScopeAndRefresh(t *testing.T) {
	ctx := context.Background()
	rtKey := string(testAccount) + "/" + auth.KeyRefreshToken
	scopeKey := string(testAccount) + "/" + KeyGrantedScope
	scopeItem := func(kr *memKeyring) (string, bool) {
		kr.mu.Lock()
		defer kr.mu.Unlock()
		v, ok := kr.items[scopeKey]
		return v, ok
	}

	// Legacy: the bare token an older daemon stored, no scope item.
	f := newTSFixture(t, Microsoft, "refresh-legacy")
	if ok, err := f.ts.CalendarConsent(ctx); err != nil || ok {
		t.Fatalf("legacy grant: %v %v", ok, err)
	}
	if _, err := f.ts.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	if f.kr.items[rtKey] != "refresh-legacy" {
		t.Fatalf("legacy value rewritten: %q", f.kr.items[rtKey])
	}
	if _, ok := scopeItem(f.kr); ok {
		t.Fatal("scope item written for a legacy grant")
	}

	// A calendar grant through Seed, then a source that reads it fresh.
	f = newTSFixture(t, Microsoft, "")
	f.fp.rotate = true
	f.fp.refreshValid["refresh-cal"] = true
	calScope := scopeCalRW + " " + scopeCalShared
	if err := f.ts.Seed(ctx, Grant{Provider: Microsoft, RefreshToken: "refresh-cal", Scope: calScope}); err != nil {
		t.Fatal(err)
	}
	if v := f.kr.items[rtKey]; v != "refresh-cal" {
		t.Fatalf("refresh-token item %q, want the bare token", v)
	}
	if v, ok := scopeItem(f.kr); !ok || v != calScope {
		t.Fatalf("scope item %q %v", v, ok)
	}
	fresh := NewTokenSource(TokenSourceOptions{Provider: Microsoft, Client: f.fp.client(), AccountID: testAccount,
		Keyring: f.kr, HTTP: testHTTP(), Now: f.clk.Now, Endpoints: f.fp.endpoints})
	if ok, err := fresh.CalendarConsent(ctx); err != nil || !ok {
		t.Fatalf("fresh source: %v %v", ok, err)
	}
	if _, err := fresh.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	form := f.fp.refreshForm(0)
	if _, sent := form["scope"]; sent {
		t.Fatalf("refresh sent a scope: %v", form)
	}
	// Rotation: the new bare token, the scope item untouched.
	rt := f.kr.items[rtKey]
	if !strings.HasSuffix(rt, "-rot") || strings.HasPrefix(rt, "{") {
		t.Fatalf("after rotation %q", rt)
	}
	if v, ok := scopeItem(f.kr); !ok || v != calScope {
		t.Fatalf("rotation changed the scope item: %q %v", v, ok)
	}

	// The provider narrows the grant on a refresh: the item goes.
	f.fp.rotate = false
	f.fp.refreshScope = "Mail.ReadWrite Mail.Send User.Read"
	fresh.Invalidate()
	if _, err := fresh.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fresh.CalendarConsent(ctx); ok {
		t.Fatal("narrowed grant still has the calendar")
	}
	if _, ok := scopeItem(f.kr); ok {
		t.Fatal("scope item kept after the calendar was dropped")
	}
	if f.kr.items[rtKey] != rt {
		t.Fatalf("refresh-token item %q, want %q", f.kr.items[rtKey], rt)
	}
	for i := 0; i < f.fp.refreshCount(); i++ {
		if _, sent := f.fp.refreshForm(i)["scope"]; sent {
			t.Fatalf("refresh %d sent a scope", i)
		}
	}

	// A calendar grant again, then a mail-only re-sign-in removes it.
	if err := fresh.Seed(ctx, Grant{Provider: Microsoft, RefreshToken: "refresh-cal2", Scope: calScope}); err != nil {
		t.Fatal(err)
	}
	if _, ok := scopeItem(f.kr); !ok {
		t.Fatal("no scope item after a calendar grant")
	}
	if err := fresh.Seed(ctx, Grant{Provider: Microsoft, RefreshToken: "refresh-mail", Scope: "Mail.ReadWrite"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := scopeItem(f.kr); ok {
		t.Fatal("scope item kept after a mail-only grant")
	}
	if ok, _ := fresh.CalendarConsent(ctx); ok {
		t.Fatal("mail-only grant has the calendar")
	}
	for k, v := range f.kr.items {
		if strings.HasPrefix(v, "{") {
			t.Fatalf("JSON written to %s: %q", k, v)
		}
	}
}

// A mail-only grant writes no scope item; nothing stored is authRequired.
func TestStoredScopeMailOnly(t *testing.T) {
	ctx := context.Background()
	f := newTSFixture(t, Microsoft, "")
	if _, err := f.ts.CalendarConsent(ctx); apiCode(err) != api.CodeAuthRequired {
		t.Fatalf("nothing stored: %v", err)
	}
	if err := f.ts.Seed(ctx, Grant{Provider: Microsoft, RefreshToken: "refresh-mail", Scope: "Mail.ReadWrite User.Read"}); err != nil {
		t.Fatal(err)
	}
	if v := f.kr.value(testAccount); v != "refresh-mail" {
		t.Fatalf("mail grant stored as %q", v)
	}
	if _, ok := f.kr.items[string(testAccount)+"/"+KeyGrantedScope]; ok {
		t.Fatal("scope item for a mail grant")
	}
	if ok, err := f.ts.CalendarConsent(ctx); err != nil || ok {
		t.Fatalf("mail grant: %v %v", ok, err)
	}
}

func TestHasCalendarScope(t *testing.T) {
	for in, want := range map[string]bool{
		"":                           false,
		"Mail.ReadWrite User.Read":   false,
		"Calendars.Read":             false,
		"Calendars.Read.Shared":      false,
		"calendars.readwrite":        true,
		"Calendars.ReadWrite.Shared": true,
		scopeCalRW:                   true,
		"HTTPS://GRAPH.MICROSOFT.COM/Calendars.ReadWrite":          true,
		"00000003-0000-0000-c000-000000000000/Calendars.ReadWrite": true,
		"https://evil.example/Calendars.ReadWrite":                 false,
		"XCalendars.ReadWrite":                                     false,
	} {
		if got := HasCalendarScope(in); got != want {
			t.Errorf("HasCalendarScope(%q) = %v", in, got)
		}
	}
}

func TestCleanScope(t *testing.T) {
	if got := cleanScope("a\x00b  c\td"); got != "c d" {
		t.Errorf("cleanScope = %q", got)
	}
	if got := cleanScope(strings.Repeat("a ", 5000)); len(got) > maxScopeLen {
		t.Errorf("cleanScope kept %d bytes", len(got))
	}
}
