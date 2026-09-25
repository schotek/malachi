// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestLiveGoogle signs in against the real Google endpoints. Opt-in:
//
//	MALACHI_TEST_OAUTH_GOOGLE=1
//	MALACHI_TEST_OAUTH_GOOGLE_CLIENT_ID=…apps.googleusercontent.com
//	MALACHI_TEST_OAUTH_GOOGLE_CLIENT_SECRET=… (Desktop clients)
//	MALACHI_TEST_OAUTH_GOOGLE_EMAIL=… (optional: login_hint and the expected identity)
//
// It prints the authorisation URL to stderr and waits up to 5 minutes for
// the browser, then refreshes once with the obtained refresh token.
func TestLiveGoogle(t *testing.T) {
	if os.Getenv("MALACHI_TEST_OAUTH_GOOGLE") != "1" {
		t.Skip("set MALACHI_TEST_OAUTH_GOOGLE=1 and MALACHI_TEST_OAUTH_GOOGLE_CLIENT_ID to run")
	}
	client := Client{
		ID:     os.Getenv("MALACHI_TEST_OAUTH_GOOGLE_CLIENT_ID"),
		Secret: os.Getenv("MALACHI_TEST_OAUTH_GOOGLE_CLIENT_SECRET"),
	}
	if client.ID == "" {
		t.Fatal("MALACHI_TEST_OAUTH_GOOGLE_CLIENT_ID is empty")
	}
	email := os.Getenv("MALACHI_TEST_OAUTH_GOOGLE_EMAIL")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	m := NewManager(Options{Log: log})
	defer m.Close()
	id, authURL, _, err := m.Start(StartRequest{
		Provider:    Google,
		Client:      client,
		LoginHint:   email,
		ExpectEmail: email,
		Page:        PageTexts{SuccessTitle: "Signed in", SuccessText: "You can close this tab.", FailureTitle: "Sign-in failed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "\nOpen within 5 minutes:\n\n%s\n\n", authURL)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	out, err := m.Wait(ctx, id, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusComplete {
		t.Fatalf("sign-in ended %s: %v", out.Status, out.Err)
	}
	g, ok := m.Consume(id)
	if !ok {
		t.Fatal("no grant")
	}
	t.Logf("signed in as %s, access token valid until %s", g.Email, g.Expiry.Format(time.RFC3339))

	kr := newMemKeyring()
	ts := NewTokenSource(TokenSourceOptions{Provider: Google, Client: client, AccountID: "acc_live", Keyring: kr, Log: log})
	if err := ts.Seed(ctx, g); err != nil {
		t.Fatal(err)
	}
	ts.Invalidate()
	tok, err := ts.AccessToken(ctx)
	if err != nil || tok == "" {
		t.Fatalf("refresh: %v", err)
	}
	t.Log("refresh succeeded")
}
