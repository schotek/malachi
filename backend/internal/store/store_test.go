package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "store.db")
	log := slog.New(slog.DiscardHandler)

	s, err := Open(ctx, path, log)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.DB().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v < 1 {
		t.Errorf("expected at least migration 1 applied, got %d", v)
	}
	var fts string
	if err := s.DB().QueryRow(`SELECT sqlite_compileoption_used('ENABLE_FTS5')`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != "1" {
		t.Errorf("FTS5 not available in the SQLite build")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must be idempotent.
	s, err = Open(ctx, path, log)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s.Close()
}
