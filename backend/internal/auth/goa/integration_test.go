// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package goa

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIntegration lists the accounts of the real GNOME Online Accounts
// daemon and, when a Microsoft 365 account exists, fetches its token. It
// is opt-in: MALACHI_TEST_GOA=1 go test -run Integration. Tokens are
// never printed.
func TestIntegration(t *testing.T) {
	if os.Getenv("MALACHI_TEST_GOA") != "1" {
		t.Skip("set MALACHI_TEST_GOA=1 with a session bus to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := New(nil)
	defer c.Close()

	accounts, err := c.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		t.Logf("%s provider=%s email=%q oauth2=%v attention=%v", a.ID, a.ProviderType, a.Email, a.OAuth2, a.AttentionNeeded)
		if a.ProviderType != ProviderMicrosoft365 || !a.OAuth2 {
			continue
		}
		tok, exp, err := c.AccessToken(ctx, a.ID)
		if err != nil {
			t.Fatalf("token for %s: %v", a.ID, err)
		}
		t.Logf("%s: token of %d bytes, expires %v", a.ID, len(tok), exp)
	}
}
