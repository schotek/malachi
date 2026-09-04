// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAccountUpdate(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	k := newMemKeyring()
	b.Keyring = k
	n := &recNotifier{}
	b.SetNotifier(n)
	acc := b.Accounts()

	added, err := acc.Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "old"}})
	if err != nil {
		t.Fatal(err)
	}
	other := validConfig()
	other.Email = "other@example.invalid"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: added.AccountID, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	before := n.accountsChanged

	// Config change without a password keeps the old one and the enabled flag.
	cfg := validConfig()
	cfg.Name = "Renamed"
	cfg.IMAP.Host = "imap2.example.invalid"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	list, _ := acc.List(ctx, api.AccountListParams{})
	got := list.Accounts[0]
	if got.Config.Name != "Renamed" || got.Config.IMAP.Host != "imap2.example.invalid" || got.Enabled {
		t.Fatalf("after update: %+v", got)
	}
	if k.values[string(added.AccountID)+"/"+auth.KeyPassword] != "old" {
		t.Fatal("password changed although none was given")
	}

	// New password.
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: cfg, Credentials: api.Credentials{Password: "new"}}); err != nil {
		t.Fatal(err)
	}
	if k.values[string(added.AccountID)+"/"+auth.KeyPassword] != "new" {
		t.Fatal("password not stored")
	}

	// E-mail clash with the other account.
	clash := cfg
	clash.Email = "OTHER@example.invalid"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: clash}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("clash: %v", err)
	}

	// Keyring refusal reverts the row.
	b.Keyring = auth.UnavailableKeyring{}
	broken := cfg
	broken.Name = "Should not stick"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: added.AccountID, Config: broken, Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("keyring: %v", err)
	}
	list, _ = acc.List(ctx, api.AccountListParams{})
	if list.Accounts[0].Config.Name != "Renamed" {
		t.Fatalf("row not reverted: %+v", list.Accounts[0].Config)
	}

	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: "acc_nope", Config: cfg}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{Config: cfg}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("missing id: %v", err)
	}
	if n.accountsChanged != before+2 {
		t.Fatalf("notifications = %d, want %d", n.accountsChanged, before+2)
	}
}

func TestAccountTestUsesStoredPassword(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	k := newMemKeyring()
	b.Keyring = k
	var seen []string
	b.ProbeIMAP = func(_ context.Context, _ api.ServerConfig, pw string) (imap.ProbeResult, error) {
		seen = append(seen, pw)
		return imap.ProbeResult{}, nil
	}
	b.ProbeSMTP = func(_ context.Context, _ api.ServerConfig, pw string) (smtp.ProbeResult, error) {
		return smtp.ProbeResult{}, nil
	}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "stored"}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: validConfig()})
	if err != nil || !res.IMAP.OK {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if len(seen) != 1 || seen[0] != "stored" {
		t.Fatalf("probe got %v", seen)
	}

	// An explicit password wins over the stored one.
	if _, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: validConfig(), Credentials: api.Credentials{Password: "typed"}}); err != nil {
		t.Fatal(err)
	}
	if seen[1] != "typed" {
		t.Fatalf("probe got %v", seen)
	}

	// No stored password → authRequired; unknown account → accountNotFound.
	delete(k.values, string(added.AccountID)+"/"+auth.KeyPassword)
	if _, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: validConfig()}); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("no secret: %v", err)
	}
	if _, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: "acc_nope", Config: validConfig()}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}
}
