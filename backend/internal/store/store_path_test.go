// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// The path goes into a "file:" URI; characters that mean something there
// must not change which file is opened.
func TestOpenPathWithURICharacters(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "odd #1? 100%")
	path := filepath.Join(dir, "store.db")
	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, "probe", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not at %s: %v", path, err)
	}
	s, err = Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, ok, err := s.GetMeta(ctx, "probe"); err != nil || !ok || v != "1" {
		t.Fatalf("reopen lost the data: %q %v %v", v, ok, err)
	}
}
