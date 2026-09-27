// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A store from before migration 0014: its rows get the whole-file defaults,
// its files no accounting until the sweep, which the conversion then
// relies on.
func TestMigration0014RawStorage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(0)")
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
		if m.version > 13 {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO accounts (id, email, name, enabled, config, position) VALUES ('acc', 'a@example.invalid', '', 1, '{}', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO folders (id, account_id, mailbox, name, path, role) VALUES ('f1', 'acc', 'INBOX', 'Inbox', 'Inbox', 'inbox')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, state string
		size      int
	}{{"m1", "fetched", 20}, {"m2", "fetched", 999}, {"m3", "none", 50}, {"m4", "failed", 30}} {
		if _, err := db.Exec(`INSERT INTO messages (id, account_id, folder_id, uid, size, body_state, thread_id, attachments_json)
			VALUES (?, 'acc', 'f1', ?, ?, ?, 't', '[{"partId":"2","filename":"a.pdf","size":10}]')`,
			row.id, len(row.id)+int(row.id[1]), row.size, row.state); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(path), "messages", "acc")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "m1"), []byte("Subject: one\r\n\r\nbody"), 0o600)
	os.WriteFile(filepath.Join(dir, "m2"), []byte("Subject: two\r\n\r\nbody"), 0o600)
	os.WriteFile(filepath.Join(dir, "m4"), []byte("unparsable"), 0o600)

	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"m1", "m3"} {
		m, err := s.GetMessage(ctx, "acc", id)
		if err != nil {
			t.Fatal(err)
		}
		if m.RawState != RawFull || len(m.RemoteParts) != 0 || m.RemoteBytes != 0 || m.StrippableBytes != -1 ||
			!m.HydratedAt.IsZero() || m.Attachments[0].Remote {
			t.Errorf("%s: %+v", id, m)
		}
	}
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM message_files`).Scan(&n)
	if n != 0 {
		t.Errorf("%d accounting rows before the sweep", n)
	}
	for _, index := range []string{"messages_partial", "messages_strippable"} {
		var name string
		if err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name); err != nil {
			t.Errorf("index %s: %v", index, err)
		}
	}
	// The constraints hold.
	if _, err := s.DB().Exec(`UPDATE messages SET raw_state = 'half' WHERE id = 'm1'`); err == nil {
		t.Error("raw_state accepted a bogus value")
	}
	if _, err := s.DB().Exec(`UPDATE messages SET remote_parts = 'not json' WHERE id = 'm1'`); err == nil {
		t.Error("remote_parts accepted malformed JSON")
	}
	if _, err := s.DB().Exec(`INSERT INTO message_files (message_id, codec, bytes, disk_bytes) VALUES ('m3', 'gzip', 1, 1)`); err == nil {
		t.Error("message_files accepted a bogus codec")
	}

	u, err := s.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !u.Estimated || u.Messages != 3 || u.MessageBytes != 20+999+30 {
		t.Errorf("usage before the sweep: %+v", u)
	}
	res, err := s.SweepMessageFiles(ctx, time.Hour)
	if err != nil || res.Backfilled != 3 {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	u, _ = s.Usage(ctx)
	if u.Estimated || u.Messages != 3 || u.MessageBytes != int64(2*len("Subject: one\r\n\r\nbody")+len("unparsable")) {
		t.Errorf("usage after the sweep: %+v", u)
	}
	// The conversion now finds them.
	s.SetRawCodec(RawZstd)
	last, cres, err := s.ConvertRawBatch(ctx, "", RawZstd, 10)
	if err != nil || cres.Converted != 3 || last == "" {
		t.Errorf("convert: %q %+v %v", last, cres, err)
	}
	if got := readRaw(t, s, "acc", "m2"); string(got) != "Subject: two\r\n\r\nbody" {
		t.Errorf("m2 after conversion: %q", got)
	}
	// A deleted message takes its accounting row along.
	if err := s.DeleteMessagesByUID(ctx, "f1", []uint32{uint32(len("m1") + int('1'))}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := fileRow(t, s, "m1"); ok {
		t.Error("accounting row outlived its message")
	}
}
