// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func testAccountConfig(email string) api.AccountConfig {
	return api.AccountConfig{
		Name:  "Work",
		Email: email,
		IMAP:  api.ServerConfig{Host: "imap.example.invalid", Port: 993, Security: api.SecurityTLS, Username: email, AuthMethod: api.AuthPassword},
		SMTP:  api.ServerConfig{Host: "smtp.example.invalid", Port: 587, Security: api.SecuritySTARTTLS, Username: email, AuthMethod: api.AuthPassword},
	}
}

func TestAccountsAddListOrder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig("Me@Example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.ID, "acc_") || a.Position != 0 || a.CreatedAt.IsZero() || a.Email != "me@example.invalid" {
		t.Fatalf("add: %+v", a)
	}
	b := Account{ID: "acc_toml", Name: "Home", Enabled: false, Config: testAccountConfig("home@example.invalid")}
	if err := s.AddAccount(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if b.ID != "acc_toml" || b.Position != 1 {
		t.Fatalf("preset id: %+v", b)
	}

	list, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != "acc_toml" || list[1].Enabled {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Config.Email != "Me@Example.invalid" || list[0].Config.IMAP.Port != 993 {
		t.Fatalf("config round trip: %+v", list[0].Config)
	}
}

func TestAccountsDuplicates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	dupEmail := Account{Name: "Again", Enabled: true, Config: testAccountConfig("ME@example.invalid")}
	if err := s.AddAccount(ctx, &dupEmail); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate e-mail: %v", err)
	}
	dupID := Account{ID: a.ID, Name: "Again", Enabled: true, Config: testAccountConfig("other@example.invalid")}
	if err := s.AddAccount(ctx, &dupID); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate id: %v", err)
	}
	if list, _ := s.ListAccounts(ctx); len(list) != 1 {
		t.Fatalf("list after duplicates = %d", len(list))
	}
}

func TestAccountsConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	cfg := testAccountConfig("me@example.invalid")
	cfg.DisplayName = "Me"
	cfg.SyncInterval = 600
	cfg.IMAP.AuthMethod = api.AuthOAuth2
	cfg.OAuth2 = &api.OAuth2Config{Provider: "office365", ClientID: "cid", TenantID: "tid", Scopes: []string{"a", "b"}}
	a := Account{Name: cfg.Name, Enabled: true, Config: cfg}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.DisplayName != "Me" || got.Config.SyncInterval != 600 || got.Config.OAuth2 == nil ||
		got.Config.OAuth2.TenantID != "tid" || len(got.Config.OAuth2.Scopes) != 2 {
		t.Fatalf("round trip: %+v", got.Config)
	}
	if _, err := s.GetAccount(ctx, "acc_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestAccountsSetEnabled(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountEnabled(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAccount(ctx, a.ID); got.Enabled {
		t.Fatal("still enabled")
	}
	if err := s.SetAccountEnabled(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAccount(ctx, a.ID); !got.Enabled {
		t.Fatal("still disabled")
	}
	if err := s.SetAccountEnabled(ctx, "acc_nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestAccountsDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	bound := importTestAttachment(t, s, a.ID, "bound.txt", "bound")
	unbound := importTestAttachment(t, s, a.ID, "loose.txt", "loose")
	d1 := Draft{AccountID: a.ID, Subject: "one"}
	if err := s.SaveDraft(ctx, &d1, []string{bound.ID}); err != nil {
		t.Fatal(err)
	}
	d2 := Draft{AccountID: a.ID, Subject: "two"}
	if err := s.SaveDraft(ctx, &d2, nil); err != nil {
		t.Fatal(err)
	}
	other := Draft{AccountID: "acc_other", Subject: "keep"}
	if err := s.SaveDraft(ctx, &other, nil); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteAccount(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccount(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("account after delete: %v", err)
	}
	if _, _, total, _ := s.ListDrafts(ctx, a.ID, "", 10); total != 0 {
		t.Fatalf("drafts left: %d", total)
	}
	for _, id := range []string{bound.ID, unbound.ID} {
		if _, err := os.Stat(s.AttachmentPath(id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("attachment file %s: %v", id, err)
		}
	}
	if _, _, total, _ := s.ListDrafts(ctx, "acc_other", "", 10); total != 1 {
		t.Fatalf("other account's drafts: %d", total)
	}
	if err := s.DeleteAccount(ctx, a.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestAccountsDeleteKeepsData(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	d := Draft{AccountID: a.ID, Subject: "one"}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, total, _ := s.ListDrafts(ctx, a.ID, "", 10); total != 1 {
		t.Fatalf("drafts after keep: %d", total)
	}
}

func TestMeta(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, ok, err := s.GetMeta(ctx, "x"); err != nil || ok {
		t.Fatalf("absent: %v %v", ok, err)
	}
	if err := s.SetMeta(ctx, "x", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, "x", "2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.GetMeta(ctx, "x"); err != nil || !ok || v != "2" {
		t.Fatalf("get: %q %v %v", v, ok, err)
	}
	if v, ok, _ := s.GetMeta(ctx, "created_at"); !ok || v == "" {
		t.Fatal("created_at seed missing")
	}
}
