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
		IMAP:  &api.ServerConfig{Host: "imap.example.invalid", Port: 993, Security: api.SecurityTLS, Username: email, AuthMethod: api.AuthPassword},
		SMTP:  &api.ServerConfig{Host: "smtp.example.invalid", Port: 587, Security: api.SecuritySTARTTLS, Username: email, AuthMethod: api.AuthPassword},
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

func TestAccountsUpdate(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := Account{Name: "Work", Enabled: false, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	b := Account{Name: "Other", Enabled: true, Config: testAccountConfig("other@example.invalid")}
	if err := s.AddAccount(ctx, &b); err != nil {
		t.Fatal(err)
	}

	upd := a
	upd.Name = "Renamed"
	upd.Email = ""
	upd.Config.Email = "New@Example.invalid"
	upd.Config.IMAP.Host = "imap2.example.invalid"
	if err := s.UpdateAccount(ctx, &upd); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.Email != "new@example.invalid" || got.Config.Email != "New@Example.invalid" ||
		got.Config.IMAP.Host != "imap2.example.invalid" || got.Enabled || got.Position != a.Position {
		t.Fatalf("after update: %+v", got)
	}

	clash := upd
	clash.Email = ""
	clash.Config.Email = "OTHER@example.invalid"
	if err := s.UpdateAccount(ctx, &clash); !errors.Is(err, ErrExists) {
		t.Fatalf("clash: %v", err)
	}
	same := upd // keeping one's own e-mail is not a clash
	if err := s.UpdateAccount(ctx, &same); err != nil {
		t.Fatalf("same e-mail: %v", err)
	}
	unknown := upd
	unknown.ID = "acc_nope"
	if err := s.UpdateAccount(ctx, &unknown); !errors.Is(err, ErrNotFound) {
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

// seedAccounts adds n accounts named a0..a(n-1) and returns their ids in
// creation order.
func seedAccounts(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := string(rune('a' + i))
		a := Account{Name: name, Enabled: true, Config: testAccountConfig(name + "@example.invalid")}
		if err := s.AddAccount(ctx, &a); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	return ids
}

// listIDs is the stored display order.
func listIDs(t *testing.T, s *Store) []string {
	t.Helper()
	list, err := s.ListAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func TestReorderAccounts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ids := seedAccounts(t, s, 4) // a b c d

	// A full order is taken verbatim and positions are compacted.
	want := []string{ids[3], ids[0], ids[2], ids[1]}
	if err := s.ReorderAccounts(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, s); !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	list, _ := s.ListAccounts(ctx)
	for i, a := range list {
		if a.Position != i {
			t.Fatalf("positions not compacted: %d has position %d", i, a.Position)
		}
	}

	// Accounts the caller did not name keep their relative order behind the
	// named ones: a client that has not seen a new account cannot move it.
	if err := s.ReorderAccounts(ctx, []string{ids[1]}); err != nil {
		t.Fatal(err)
	}
	if got, want := listIDs(t, s), []string{ids[1], ids[3], ids[0], ids[2]}; !equalIDs(got, want) {
		t.Fatalf("partial order = %v, want %v", got, want)
	}

	// An empty list changes nothing but still compacts.
	before := listIDs(t, s)
	if err := s.ReorderAccounts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, s); !equalIDs(got, before) {
		t.Fatalf("empty reorder changed the order: %v", got)
	}
}

func TestReorderAccountsRejects(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ids := seedAccounts(t, s, 2)
	before := listIDs(t, s)

	if err := s.ReorderAccounts(ctx, []string{ids[0], ids[0]}); err == nil {
		t.Error("duplicate id accepted")
	}
	if err := s.ReorderAccounts(ctx, []string{ids[0], ids[1], "acc_extra"}); err == nil {
		t.Error("more ids than accounts accepted")
	}
	if err := s.ReorderAccounts(ctx, []string{ids[1], "acc_nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if got := listIDs(t, s); !equalIDs(got, before) {
		t.Fatalf("a rejected reorder changed the order: %v", got)
	}
}

// TestReorderAccountsAfterDelete covers the gaps DeleteAccount leaves in the
// position sequence.
func TestReorderAccountsAfterDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ids := seedAccounts(t, s, 3)
	if err := s.DeleteAccount(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}
	// Positions are now 0 and 2.
	if err := s.ReorderAccounts(ctx, []string{ids[2], ids[0]}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListAccounts(ctx)
	if len(list) != 2 || list[0].ID != ids[2] || list[0].Position != 0 || list[1].ID != ids[0] || list[1].Position != 1 {
		t.Fatalf("after delete: %+v", list)
	}
	// A deleted account can no longer be named.
	if err := s.ReorderAccounts(ctx, []string{ids[1]}); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted id: %v", err)
	}
}

// TestReorderAccountsKeepsRows makes sure only the order changes.
func TestReorderAccountsKeepsRows(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ids := seedAccounts(t, s, 2)
	before, err := s.GetAccount(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderAccounts(ctx, []string{ids[1], ids[0]}); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetAccount(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != before.Name || after.Email != before.Email || after.Enabled != before.Enabled ||
		after.Config.Email != before.Config.Email || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("reorder changed the row: %+v vs %+v", after, before)
	}
	if after.Position != 1 {
		t.Fatalf("position = %d", after.Position)
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
