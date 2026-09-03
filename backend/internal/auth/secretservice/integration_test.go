// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package secretservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// TestIntegration talks to the real session bus. It is opt-in because a
// locked keyring pops a dialog: MALACHI_TEST_KEYRING=1 go test -run Integration.
func TestIntegration(t *testing.T) {
	if os.Getenv("MALACHI_TEST_KEYRING") != "1" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("set MALACHI_TEST_KEYRING=1 with a session bus to run")
	}
	var b [8]byte
	rand.Read(b[:])
	account := api.AccountID("acc_test_" + hex.EncodeToString(b[:]))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := New(nil)
	defer c.Close()
	t.Cleanup(func() { _ = c.Delete(context.Background(), account, auth.KeyPassword) })

	if _, err := c.Get(ctx, account, auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("before set: %v", err)
	}
	if err := c.Set(ctx, account, auth.KeyPassword, "integration"); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Get(ctx, account, auth.KeyPassword); err != nil || v != "integration" {
		t.Fatalf("get: %q %v", v, err)
	}
	if err := c.Set(ctx, account, auth.KeyPassword, "replaced"); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(ctx, account, auth.KeyPassword); v != "replaced" {
		t.Fatalf("replace: %q", v)
	}
	if err := c.Delete(ctx, account, auth.KeyPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, account, auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("after delete: %v", err)
	}
}
