// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/schotek/malachi/backend/internal/thread"
)

// A store from before threading: migration 0011 gives every row a
// singleton id, rebuilds message_refs from the JSON columns and leaves a
// server id alone; the backfill batch then links what belongs together.
func TestMigration0011Backfill(t *testing.T) {
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
		if m.version > 10 {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]string{
		{"m1", "a", "", "[]", ""},
		{"m2", "b", "a", `["a"]`, ""},
		{"m3", "c", "", `["", "zzz"]`, ""},
		{"m4", "d", "", "[]", "conv-x"},
	} {
		if _, err := db.Exec(`INSERT INTO messages (id, account_id, folder_id, rfc_message_id, in_reply_to, references_json, thread_id)
			VALUES (?, 'acc', 'f1', ?, ?, ?, ?)`, row[0], row[1], row[2], row[3], row[4]); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tid := map[string]string{}
	readThreads := func() {
		for _, id := range []string{"m1", "m2", "m3", "m4"} {
			var v string
			if err := s.DB().QueryRow(`SELECT thread_id FROM messages WHERE id = ?`, id).Scan(&v); err != nil {
				t.Fatal(err)
			}
			tid[id] = v
		}
	}
	readThreads()
	if tid["m4"] != "conv-x" {
		t.Errorf("server id rewritten: %q", tid["m4"])
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if !thread.IsLocalID(tid[id]) || len(tid[id]) != len(thread.IDPrefix)+32 {
			t.Errorf("%s: thread id %q", id, tid[id])
		}
	}
	if tid["m1"] == tid["m2"] || tid["m2"] == tid["m3"] || tid["m1"] == tid["m3"] {
		t.Errorf("legacy rows share ids before the backfill: %v", tid)
	}
	var refs []string
	rows, err := s.DB().Query(`SELECT message_id || ':' || ref FROM message_refs ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	if len(refs) != 2 || refs[0] != "m2:a" || refs[1] != "m3:zzz" {
		t.Errorf("message_refs = %v", refs)
	}

	// The backfill links m2 to m1 and leaves the rest.
	last, visited, linked, err := s.LinkThreadsBatch(ctx, "", 3)
	if err != nil || last != "m3" || visited != 3 || linked != 1 {
		t.Fatalf("first batch: last %q visited %d linked %d, %v", last, visited, linked, err)
	}
	last, visited, linked, err = s.LinkThreadsBatch(ctx, last, 3)
	if err != nil || last != "m4" || visited != 1 || linked != 0 {
		t.Fatalf("second batch: last %q visited %d linked %d, %v", last, visited, linked, err)
	}
	if last, visited, _, err = s.LinkThreadsBatch(ctx, last, 3); err != nil || last != "" || visited != 0 {
		t.Fatalf("end: last %q visited %d, %v", last, visited, err)
	}
	readThreads()
	if tid["m1"] != tid["m2"] || tid["m3"] == tid["m1"] || tid["m4"] != "conv-x" {
		t.Errorf("after backfill: %v", tid)
	}
	// Running it again changes nothing.
	if _, _, linked, err := s.LinkThreadsBatch(ctx, "", 10); err != nil || linked != 0 {
		t.Errorf("second pass linked %d, %v", linked, err)
	}
}
