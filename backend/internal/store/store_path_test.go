// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// The path goes into a "file:" URI; characters that mean something there
// must not change which file is opened.
func TestOpenPathWithURICharacters(t *testing.T) {
	ctx := context.Background()
	// "?" is not a file name character everywhere (Windows refuses it);
	// where it is, it is part of the test.
	name := "odd #1? 100%"
	if !nameAllowed(t, "?") {
		name = "odd #1 100%"
	}
	dir := filepath.Join(t.TempDir(), name)
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

// nameAllowed reports whether the file system of the test's temporary
// directory takes a directory of this name.
func nameAllowed(t *testing.T, name string) bool {
	t.Helper()
	return os.Mkdir(filepath.Join(t.TempDir(), name), 0o700) == nil
}

// permOf returns the permissions a file, or with dir a directory, made
// with perm has in the test's temporary directory. A mode check compares
// with it, so that the check means the same where the platform keeps no
// Unix modes: Windows reports 0666 or 0777 whatever was asked for.
func permOf(t *testing.T, perm fs.FileMode, dir bool) fs.FileMode {
	t.Helper()
	p := filepath.Join(t.TempDir(), "probe")
	var err error
	if dir {
		err = os.Mkdir(p, perm)
	} else {
		err = os.WriteFile(p, nil, perm)
	}
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
