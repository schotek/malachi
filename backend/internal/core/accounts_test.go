// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/account"
	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

// memKeyring records secrets in memory and every Delete it receives.
type memKeyring struct {
	values  map[string]string
	deleted []string
}

func newMemKeyring() *memKeyring { return &memKeyring{values: map[string]string{}} }

func (k *memKeyring) Get(_ context.Context, id api.AccountID, key string) (string, error) {
	return k.values[string(id)+"/"+key], nil
}
func (k *memKeyring) Set(_ context.Context, id api.AccountID, key, value string) error {
	k.values[string(id)+"/"+key] = value
	return nil
}
func (k *memKeyring) Delete(_ context.Context, id api.AccountID, key string) error {
	delete(k.values, string(id)+"/"+key)
	k.deleted = append(k.deleted, string(id)+"/"+key)
	return nil
}

// recNotifier counts notifications.
type recNotifier struct{ accountsChanged int }

func (*recNotifier) NewMessage(api.NewMessageNotification)             {}
func (*recNotifier) SyncState(api.SyncStateNotification)               {}
func (*recNotifier) AuthRequired(api.AuthRequiredNotification)         {}
func (n *recNotifier) AccountsChanged(api.AccountsChangedNotification) { n.accountsChanged++ }

var _ auth.Keyring = (*memKeyring)(nil)
var _ api.Notifier = (*recNotifier)(nil)

func validConfig() api.AccountConfig {
	return api.AccountConfig{
		Name:  "Work",
		Email: "me@example.invalid",
		IMAP:  api.ServerConfig{Host: "imap.example.invalid", Port: 993, Security: api.SecurityTLS, Username: "me@example.invalid", AuthMethod: api.AuthPassword},
		SMTP:  api.ServerConfig{Host: "smtp.example.invalid", Port: 587, Security: api.SecuritySTARTTLS, Username: "me@example.invalid", AuthMethod: api.AuthPassword},
	}
}

func TestAccountValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*api.AccountConfig)
		ok   bool
	}{
		{"valid", func(*api.AccountConfig) {}, true},
		{"empty name", func(c *api.AccountConfig) { c.Name = "  " }, false},
		{"name with newline", func(c *api.AccountConfig) { c.Name = "a\nb" }, false},
		{"bad email", func(c *api.AccountConfig) { c.Email = "not an address" }, false},
		{"email with display name", func(c *api.AccountConfig) { c.Email = "Me <me@example.invalid>" }, false},
		{"empty host", func(c *api.AccountConfig) { c.IMAP.Host = "" }, false},
		{"host with space", func(c *api.AccountConfig) { c.IMAP.Host = "imap example" }, false},
		{"host bad label", func(c *api.AccountConfig) { c.SMTP.Host = "-bad-.example" }, false},
		{"host ip", func(c *api.AccountConfig) { c.IMAP.Host = "192.0.2.1" }, true},
		{"port zero", func(c *api.AccountConfig) { c.IMAP.Port = 0 }, false},
		{"port too big", func(c *api.AccountConfig) { c.SMTP.Port = 70000 }, false},
		{"unknown security", func(c *api.AccountConfig) { c.IMAP.Security = "ssl" }, false},
		{"none on remote host", func(c *api.AccountConfig) { c.IMAP.Security = api.SecurityNone }, false},
		{"none on localhost", func(c *api.AccountConfig) { c.IMAP.Host = "LocalHost"; c.IMAP.Security = api.SecurityNone }, true},
		{"none on loopback ip", func(c *api.AccountConfig) { c.SMTP.Host = "127.0.0.1"; c.SMTP.Security = api.SecurityNone }, true},
		{"empty username", func(c *api.AccountConfig) { c.SMTP.Username = "" }, false},
		{"username control char", func(c *api.AccountConfig) { c.SMTP.Username = "a\x01b" }, false},
		{"unknown auth", func(c *api.AccountConfig) { c.IMAP.AuthMethod = "ntlm" }, false},
		{"oauth2 without settings", func(c *api.AccountConfig) { c.IMAP.AuthMethod = api.AuthOAuth2 }, false},
		{"oauth2 settings unused", func(c *api.AccountConfig) { c.OAuth2 = &api.OAuth2Config{Provider: "office365"} }, false},
		{"oauth2 office365", func(c *api.AccountConfig) {
			c.IMAP.AuthMethod = api.AuthOAuth2
			c.OAuth2 = &api.OAuth2Config{Provider: "office365"}
		}, true},
		{"oauth2 custom without token url", func(c *api.AccountConfig) {
			c.IMAP.AuthMethod = api.AuthOAuth2
			c.OAuth2 = &api.OAuth2Config{Provider: "custom", AuthURL: "https://auth.example.invalid/a"}
		}, false},
		{"oauth2 custom http", func(c *api.AccountConfig) {
			c.IMAP.AuthMethod = api.AuthOAuth2
			c.OAuth2 = &api.OAuth2Config{Provider: "custom", AuthURL: "http://a.invalid/a", TokenURL: "https://a.invalid/t"}
		}, false},
		{"oauth2 custom ok", func(c *api.AccountConfig) {
			c.IMAP.AuthMethod = api.AuthOAuth2
			c.OAuth2 = &api.OAuth2Config{Provider: "custom", AuthURL: "https://a.invalid/a", TokenURL: "https://a.invalid/t", Scopes: []string{"mail"}}
		}, true},
		{"oauth2 scope with space", func(c *api.AccountConfig) {
			c.IMAP.AuthMethod = api.AuthOAuth2
			c.OAuth2 = &api.OAuth2Config{Provider: "office365", Scopes: []string{"a b"}}
		}, false},
		{"interval too small", func(c *api.AccountConfig) { c.SyncInterval = 30 }, false},
		{"interval ok", func(c *api.AccountConfig) { c.SyncInterval = 600 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mut(&c)
			err := validateAccountConfig(&c)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("expected invalidArgument")
				}
				if errCode(t, err) != api.CodeInvalidArgument {
					t.Fatalf("code = %v", err)
				}
			}
		})
	}
}

func TestAccountAddListSetEnabled(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	n := &recNotifier{}
	b.SetNotifier(n)
	acc := b.Accounts()

	res, err := acc.Add(ctx, api.AccountAddParams{Config: validConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(res.AccountID), "acc_") {
		t.Fatalf("id = %q", res.AccountID)
	}
	list, err := acc.List(ctx, api.AccountListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 1 {
		t.Fatalf("list = %+v", list.Accounts)
	}
	got := list.Accounts[0]
	if got.ID != res.AccountID || !got.Enabled || got.State.Status != api.SyncIdle || got.State.Progress != -1 ||
		got.State.AccountID != res.AccountID || got.Config.Email != "me@example.invalid" {
		t.Fatalf("account = %+v", got)
	}

	dup := validConfig()
	dup.Email = "ME@example.invalid"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: dup}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: res.AccountID, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	list, _ = acc.List(ctx, api.AccountListParams{})
	if list.Accounts[0].Enabled || list.Accounts[0].State.Status != api.SyncDisabled {
		t.Fatalf("after pause: %+v", list.Accounts[0])
	}
	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: "acc_nope", Enabled: true}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("missing id: %v", err)
	}
	if n.accountsChanged != 2 {
		t.Fatalf("notifications = %d", n.accountsChanged)
	}
}

func TestAccountAddPassword(t *testing.T) {
	ctx := context.Background()

	t.Run("stored in keyring", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		k := newMemKeyring()
		b.Keyring = k
		res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "s3cret"}})
		if err != nil {
			t.Fatal(err)
		}
		if k.values[string(res.AccountID)+"/"+auth.KeyPassword] != "s3cret" {
			t.Fatalf("keyring = %v", k.values)
		}
	})

	t.Run("keyring not implemented rolls back", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		_, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "s3cret"}})
		if errCode(t, err) != api.CodeNotImplemented {
			t.Fatalf("err = %v", err)
		}
		list, _ := b.Accounts().List(ctx, api.AccountListParams{})
		if len(list.Accounts) != 0 {
			t.Fatalf("account kept after keyring failure: %+v", list.Accounts)
		}
	})

	t.Run("password without password auth", func(t *testing.T) {
		b := newTestBackend(t, config.Default())
		c := validConfig()
		c.IMAP.AuthMethod, c.SMTP.AuthMethod = api.AuthOAuth2, api.AuthOAuth2
		c.OAuth2 = &api.OAuth2Config{Provider: "office365"}
		_, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: c, Credentials: api.Credentials{Password: "x"}})
		if errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAccountRemove(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	k := newMemKeyring()
	b.Keyring = k
	n := &recNotifier{}
	b.SetNotifier(n)

	res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: res.AccountID, Subject: "d"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: res.AccountID, DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	drafts, err := b.Drafts().List(ctx, api.DraftListParams{AccountID: res.AccountID})
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts.Drafts) != 0 {
		t.Fatalf("drafts left: %+v", drafts.Drafts)
	}
	if len(k.deleted) != 2 || len(k.values) != 0 {
		t.Fatalf("keyring deletes = %v, values = %v", k.deleted, k.values)
	}
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: res.AccountID}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("second remove: %v", err)
	}
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("missing id: %v", err)
	}
	if n.accountsChanged != 2 {
		t.Fatalf("notifications = %d", n.accountsChanged)
	}

	// A stub keyring must not keep a removed account alive.
	b.Keyring = auth.NotImplementedKeyring{}
	res, err = b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: res.AccountID}); err != nil {
		t.Fatalf("remove with stub keyring: %v", err)
	}
}

func TestImportConfigAccounts(t *testing.T) {
	ctx := context.Background()
	off := false
	cfg := config.Default()
	cfg.Accounts = []account.Config{
		{
			Name: "Work", Email: "work@example.invalid",
			IMAP: account.Server{Host: "imap.example.invalid", Port: 993, Security: "tls", Username: "w", AuthMethod: "password"},
			SMTP: account.Server{Host: "smtp.example.invalid", Port: 587, Security: "starttls", Username: "w", AuthMethod: "password"},
		},
		{
			ID: "acc_home", Name: "Home", Email: "home@example.invalid", Enabled: &off, SyncIntervalSeconds: 600,
			IMAP: account.Server{Host: "imap.example.invalid", Port: 993, Security: "tls", Username: "h", AuthMethod: "password"},
			SMTP: account.Server{Host: "smtp.example.invalid", Port: 587, Security: "starttls", Username: "h", AuthMethod: "password"},
		},
		{Name: "Broken", Email: "nope"},
	}
	b := newTestBackend(t, cfg)

	if err := b.ImportConfigAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := b.Accounts().List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 2 {
		t.Fatalf("imported = %+v", list.Accounts)
	}
	if list.Accounts[0].Config.Name != "Work" || !list.Accounts[0].Enabled {
		t.Fatalf("first = %+v", list.Accounts[0])
	}
	if list.Accounts[1].ID != "acc_home" || list.Accounts[1].Enabled || list.Accounts[1].Config.SyncInterval != 600 {
		t.Fatalf("second = %+v", list.Accounts[1])
	}

	// A second start adds nothing.
	if err := b.ImportConfigAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ = b.Accounts().List(ctx, api.AccountListParams{}); len(list.Accounts) != 2 {
		t.Fatalf("after second import = %d", len(list.Accounts))
	}

	// A removed account stays removed at the next start.
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: "acc_home", DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.ImportConfigAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ = b.Accounts().List(ctx, api.AccountListParams{}); len(list.Accounts) != 1 || list.Accounts[0].Config.Name != "Work" {
		t.Fatalf("after removal = %+v", list.Accounts)
	}
}

func TestImportConfigAccountsKeepsStoreVersion(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Accounts = []account.Config{{
		Name: "TOML", Email: "me@example.invalid",
		IMAP: account.Server{Host: "toml.example.invalid", Port: 993, Security: "tls", Username: "w", AuthMethod: "password"},
		SMTP: account.Server{Host: "toml.example.invalid", Port: 587, Security: "starttls", Username: "w", AuthMethod: "password"},
	}}
	b := newTestBackend(t, cfg)
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig()}); err != nil {
		t.Fatal(err)
	}
	if err := b.ImportConfigAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := b.Accounts().List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 1 || list.Accounts[0].Config.IMAP.Host != "imap.example.invalid" {
		t.Fatalf("store row replaced: %+v", list.Accounts)
	}
}
