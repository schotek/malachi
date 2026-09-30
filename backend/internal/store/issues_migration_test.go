// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A store from before issue-tracker accounts: migration 0015 rebuilds the
// accounts table with every row, id, position, config and time stamp as it
// was, in the mail realm, and afterwards an address is unique per realm.
func TestMigration0015Issues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version > 14 {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	cfgWork := `{"name":"Work","email":"Jana@Example.invalid","imap":{"host":"imap.example.invalid","port":993,"security":"tls","username":"jana","authMethod":"password"}}`
	cfgHome := `{"name":"Home","email":"home@example.invalid","kind":"graph","graph":{"source":"goa","goaAccountId":"account_1"}}`
	for _, row := range []struct {
		id, email, name, config, created, updated string
		enabled, position                         int
	}{
		{"acc_work", "jana@example.invalid", "Work", cfgWork, "2026-09-01T08:00:00.000Z", "2026-09-02T08:00:00.000Z", 1, 1},
		{"acc_home", "home@example.invalid", "Home", cfgHome, "2026-09-03T08:00:00.000Z", "2026-09-03T09:00:00.000Z", 0, 0},
	} {
		if _, err := db.Exec(`INSERT INTO accounts (id, email, name, enabled, config, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, row.id, row.email, row.name, row.enabled, row.config, row.position, row.created, row.updated); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO folders (id, account_id, mailbox, name, path, role) VALUES ('f1', 'acc_work', 'INBOX', 'Inbox', 'Inbox', 'inbox')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages (id, account_id, folder_id, uid, subject, thread_id) VALUES ('m1', 'acc_work', 'f1', 1, 'hello', 't_1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO drafts (id, account_id, subject) VALUES ('d1', 'acc_work', 'draft')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	list, err := s.ListAccounts(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("accounts after the migration: %+v %v", list, err)
	}
	home, work := list[0], list[1]
	if home.ID != "acc_home" || home.Position != 0 || home.Enabled || home.Realm != "" || home.Config.Protocol() != api.AccountGraph ||
		home.Config.Graph == nil || home.Config.Graph.GOAAccountID != "account_1" ||
		home.CreatedAt.Format(timeLayout) != "2026-09-03T08:00:00.000Z" || home.UpdatedAt.Format(timeLayout) != "2026-09-03T09:00:00.000Z" {
		t.Errorf("home: %+v", home)
	}
	if work.ID != "acc_work" || work.Position != 1 || !work.Enabled || work.Realm != "" || work.Email != "jana@example.invalid" ||
		work.Name != "Work" || work.Config.Email != "Jana@Example.invalid" || work.Config.IMAP == nil || work.Config.IMAP.Username != "jana" ||
		work.CreatedAt.Format(timeLayout) != "2026-09-01T08:00:00.000Z" || work.UpdatedAt.Format(timeLayout) != "2026-09-02T08:00:00.000Z" {
		t.Errorf("work: %+v", work)
	}
	// The rest of the store is where it was, with the new columns' defaults.
	f, err := s.GetFolder(ctx, "acc_work", "f1")
	if err != nil || f.Virtual != "" {
		t.Errorf("folder: %+v %v", f, err)
	}
	m, err := s.GetMessage(ctx, "acc_work", "m1")
	if err != nil || m.Hidden || m.Subject != "hello" {
		t.Errorf("message: %+v %v", m, err)
	}
	d, err := s.GetDraft(ctx, "acc_work", "d1")
	if err != nil || d.CommentVisibility != "" {
		t.Errorf("draft: %+v %v", d, err)
	}

	// The same address in another realm is fine, in the same one not
	// (case-insensitive), whether through the store or in SQL.
	jira := Account{Name: "Acme", Enabled: true, Config: jiraConfig("JANA@example.invalid", "https://acme.atlassian.net")}
	if err := s.AddAccount(ctx, &jira); err != nil {
		t.Fatalf("jira account with a mailbox's address: %v", err)
	}
	dup := Account{Name: "Dup", Enabled: true, Config: testAccountConfig("jana@EXAMPLE.invalid")}
	if err := s.AddAccount(ctx, &dup); !errors.Is(err, ErrExists) {
		t.Fatalf("second mailbox with the address: %v", err)
	}
	dupJira := Account{Name: "Dup", Enabled: true, Config: jiraConfig("jana@example.invalid", "https://acme.atlassian.net/")}
	if err := s.AddAccount(ctx, &dupJira); !errors.Is(err, ErrExists) {
		t.Fatalf("second account of the site: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO accounts (id, email, realm, name, config) VALUES ('x', 'Jana@Example.invalid', 'ACME.atlassian.net', 'x', '{}')`); err == nil ||
		!strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate identity in SQL: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO accounts (id, email, name, config) VALUES ('y', 'jana@example.invalid', 'y', '{}')`); err == nil {
		t.Fatal("duplicate mail address in SQL accepted")
	}

	// The indexes are the new ones, and the schema is sound.
	for _, name := range []string{"accounts_identity", "accounts_order", "messages_hidden", "issues_by_key", "issues_by_updated",
		"issue_items_by_issue", "issue_mail_links_by_issue"} {
		var n int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s: %d %v", name, n, err)
		}
	}
	var check string
	if err := s.DB().QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity: %q %v", check, err)
	}
	rows, err := s.DB().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("foreign key violations after the migration")
	}
	rows.Close()
	var version int
	if err := s.DB().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 15 {
		t.Errorf("schema version %d %v", version, err)
	}
}
