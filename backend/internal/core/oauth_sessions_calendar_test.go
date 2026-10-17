// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/oauth2flow"
	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/pkg/api"
)

// account.oauthStart {calendar: true} asks for the calendar scopes only
// when set, the stored grant records them, and CalendarConsent reads the
// record (docs/calendar.md D15).
func TestOAuthCalendarConsent(t *testing.T) {
	ctx := context.Background()
	f := newOAuthBackend(t, testMicrosoftClient)
	acc := f.b.Accounts()
	f.b.ProbeGraph = func(context.Context, string) (graph.ProbeResult, error) {
		return graph.ProbeResult{Email: "me@contoso.invalid", Capabilities: []string{"graph"}}, nil
	}

	signIn := func(cfg api.AccountConfig, accountID api.AccountID, calendar bool) string {
		t.Helper()
		p := api.AccountOAuthStartParams{Calendar: calendar}
		if accountID != "" {
			p.AccountID = accountID
		} else {
			p.Config = &cfg
		}
		start, err := acc.OAuthStart(ctx, p)
		if err != nil {
			t.Fatalf("oauthStart: %v", err)
		}
		f.fp.mu.Lock()
		f.fp.lastAuth = nil
		f.fp.mu.Unlock()
		if status, body := browse(t, start.AuthURL); status != http.StatusOK {
			t.Fatalf("browser page %d: %s", status, body)
		}
		f.fp.mu.Lock()
		scope := f.fp.lastAuth.Get("scope")
		f.fp.mu.Unlock()
		if got := strings.Contains(scope, "Calendars.ReadWrite"); got != calendar {
			t.Fatalf("calendar %v asked for %q", calendar, scope)
		}
		if res, err := acc.OAuthWait(ctx, api.AccountOAuthWaitParams{SessionID: start.SessionID}); err != nil || res.Status != api.OAuthSessionComplete {
			t.Fatalf("oauthWait: %+v, %v", res, err)
		}
		return start.SessionID
	}

	// A plain sign-in: mail only, stored in the old bare form.
	cfg := graphDaemonConfig()
	session := signIn(cfg, "", false)
	added, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{OAuthSession: session}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	if ok, err := f.b.CalendarConsent(ctx, string(id)); err != nil || ok {
		t.Fatalf("plain sign-in consent %v, %v", ok, err)
	}
	if v := f.k.value(id, auth.KeyRefreshToken); !strings.HasPrefix(v, "refresh-") {
		t.Fatalf("plain grant stored as %q", v)
	}

	// Signed in again for the calendar: the grant now allows it.
	signIn(cfg, id, true)
	if ok, err := f.b.CalendarConsent(ctx, string(id)); err != nil || !ok {
		t.Fatalf("calendar sign-in consent %v, %v", ok, err)
	}
	if v := f.k.value(id, auth.KeyRefreshToken); !strings.HasPrefix(v, "refresh-") {
		t.Fatalf("calendar grant's refresh-token item %q, want the bare token", v)
	}
	if v := f.k.value(id, oauth2flow.KeyGrantedScope); !strings.Contains(v, "Calendars.ReadWrite") {
		t.Fatalf("scope item %q", v)
	}
	f.b.dropTokenSource(string(id)) // a fresh source reads the keyring
	if ok, err := f.b.CalendarConsent(ctx, string(id)); err != nil || !ok {
		t.Fatalf("calendar consent from the keyring %v, %v", ok, err)
	}
	if tok, err := f.b.GraphTokenFor(ctx, string(id)); err != nil || tok == "" {
		t.Fatalf("token after the calendar sign-in: %q, %v", tok, err)
	}

	// Removing the account removes the scope item with the token.
	if _, err := acc.Remove(ctx, api.AccountRemoveParams{AccountID: id}); err != nil {
		t.Fatal(err)
	}
	if f.k.value(id, oauth2flow.KeyGrantedScope) != "" || f.k.value(id, auth.KeyRefreshToken) != "" {
		t.Fatal("secrets left after account.remove")
	}
}

// Google has no calendar provider: a calendar sign-in is refused before a
// session opens.
func TestOAuthCalendarRefusedForGoogle(t *testing.T) {
	f := newOAuthBackend(t, testGoogleClient)
	cfg := googleDaemonConfig()
	_, err := f.b.Accounts().OAuthStart(context.Background(), api.AccountOAuthStartParams{Config: &cfg, Calendar: true})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("google calendar sign-in: %v", err)
	}
}

func TestOAuthCalendarWanted(t *testing.T) {
	on := graphDaemonConfig()
	on.Calendar = &api.CalendarConfig{Sync: true}
	off := graphDaemonConfig()
	off.Calendar = &api.CalendarConfig{}
	gmail := googleDaemonConfig()
	gmail.Calendar = &api.CalendarConfig{Sync: true}
	for name, c := range map[string]struct {
		cfg  api.AccountConfig
		want bool
	}{
		"graph, sync on":  {on, true},
		"graph, sync off": {off, false},
		"graph, none":     {graphDaemonConfig(), false},
		"google":          {gmail, false},
	} {
		if got := calendarWanted(c.cfg); got != c.want {
			t.Errorf("%s: calendarWanted = %v", name, got)
		}
	}
	if cloned := cloneConfig(on); cloned.Calendar == on.Calendar || !cloned.Calendar.Sync {
		t.Error("cloneConfig shares or drops the calendar block")
	}
}
