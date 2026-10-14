// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// TestBoardDryRun evaluates the board over a copy of a store and prints
// one line per case: state, reason code, account, the other party's
// display name, the subject cut to 60 characters and the message count,
// then the totals per state and per reason. No body, no address. It is
// skipped unless MALACHI_BOARD_DRYRUN_STORE names a store file:
//
//	MALACHI_BOARD_DRYRUN_STORE=/path/to/store.db go test ./internal/core/ -run 'TestBoardDryRun$' -v -count=1
//
// The test never opens the store it is given: it copies store.db (with
// its -wal and -shm files) into a temporary directory of its own and works
// on the copy, which opening migrates (0017) and which is deleted at the
// end. The raw messages (for the bulk classification and the own text of
// HTML messages) are read through a link to the messages directory beside
// the given store; nothing the test runs writes there. As a second line, a
// store inside the user's real data directories is refused: quit the app
// and copy the data directory first.
func TestBoardDryRun(t *testing.T) {
	path := os.Getenv("MALACHI_BOARD_DRYRUN_STORE")
	if path == "" {
		t.Skip("MALACHI_BOARD_DRYRUN_STORE is not set")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if dir, ok := realDataDir(abs); ok || realStoreFile(abs) {
		t.Fatalf("refusing %s: it is (or lies in) the real data directory %s.\n"+
			"The dry run is meant for a copy: quit the app, copy the whole data directory elsewhere and set "+
			"MALACHI_BOARD_DRYRUN_STORE to the copy's store.db.", abs, dir)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("store: %v", err)
	}
	work := t.TempDir()
	copyPath := filepath.Join(work, "store.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(abs+suffix, copyPath+suffix); err != nil && !(suffix != "" && os.IsNotExist(err)) {
			t.Fatalf("copy %s: %v", filepath.Base(abs+suffix), err)
		}
	}
	if msgs := filepath.Join(filepath.Dir(abs), "messages"); dirExists(msgs) {
		if err := os.Symlink(msgs, filepath.Join(work, "messages")); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Logf("no messages directory beside the store: bulk classification and HTML own texts fall back")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, copyPath, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := New("dryrun", st, config.Default(), nil)
	start := time.Now()
	if err := b.backfillBulk(ctx); err != nil {
		t.Fatalf("bulk classification: %v", err)
	}
	if err := b.backfillBoard(ctx); err != nil {
		t.Fatalf("first evaluation: %v", err)
	}
	for {
		if err := b.drainBoard(ctx); err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		n, err := st.CountBoardDirty(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	accounts, err := st.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[api.AccountID]string{}
	for _, a := range accounts {
		names[api.AccountID(a.ID)] = a.Name
	}
	res, err := b.Board().List(ctx, api.BoardListParams{})
	if err != nil {
		t.Fatal(err)
	}
	totals := map[api.BoardState]int{}
	reasons := map[api.BoardReason]int{}
	for _, c := range res.Cases {
		totals[c.RuleState]++
		reasons[c.RuleReason]++
		t.Logf("%-5s %-18s %-20s %-24s %-60s %3d", c.RuleState, c.RuleReason, cutRunes(names[c.AccountID], 20),
			cutRunes(c.Person.Name, 24), cutRunes(c.Subject, 60), c.MessageCount)
	}
	t.Logf("cases %d (truncated %v) in %s", len(res.Cases), res.Truncated, time.Since(start).Round(time.Millisecond))
	for _, s := range api.BoardStates {
		t.Logf("  %-5s %d", s, totals[s])
	}
	codes := make([]string, 0, len(reasons))
	for r, n := range reasons {
		codes = append(codes, fmt.Sprintf("  %-18s %d", r, n))
	}
	sort.Strings(codes)
	for _, line := range codes {
		t.Log(line)
	}
}

// copyFile copies src to dst (a new file, 0600).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// realDataDirs are the user's real data directories (macOS, Linux,
// Flatpak, Windows, and MALACHI_DATA_DIR), symlinks resolved.
func realDataDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, "Library", "Application Support", "Malachi Mail"),
			filepath.Join(home, ".local", "share", "malachi"),
			filepath.Join(home, ".var", "app", "io.github.schotek.Malachi"))
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		dirs = append(dirs, filepath.Join(x, "malachi"))
	}
	if l := os.Getenv("LOCALAPPDATA"); l != "" {
		dirs = append(dirs, filepath.Join(l, "Malachi Mail"))
	}
	if d := os.Getenv("MALACHI_DATA_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	for i, d := range dirs {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			dirs[i] = r
		}
	}
	return dirs
}

// realDataDir reports whether path lies in one of the user's real data
// directories, and which.
func realDataDir(path string) (string, bool) {
	clean := strings.ToLower(filepath.Clean(path))
	for _, d := range realDataDirs() {
		d = strings.ToLower(filepath.Clean(d))
		if clean == d || strings.HasPrefix(clean, d+string(filepath.Separator)) {
			return d, true
		}
	}
	return "", false
}

// realStoreFile reports whether path is (a hard link of) a real store:
// the same file as a store.db of a real data directory.
func realStoreFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, d := range realDataDirs() {
		for _, name := range []string{"store.db", filepath.Join("data", "malachi", "store.db")} {
			if real, err := os.Stat(filepath.Join(d, name)); err == nil && os.SameFile(fi, real) {
				return true
			}
		}
	}
	return false
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// The dry run's guard refuses the real data directories.
func TestBoardDryRunGuard(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	for _, p := range []string{
		filepath.Join(home, "Library", "Application Support", "Malachi Mail", "store.db"),
		filepath.Join(home, ".local", "share", "malachi", "store.db"),
		filepath.Join(home, ".var", "app", "io.github.schotek.Malachi", "data", "malachi", "store.db"),
	} {
		if _, ok := realDataDir(p); !ok {
			t.Errorf("%s is not refused", p)
		}
	}
	if _, ok := realDataDir(filepath.Join(t.TempDir(), "store.db")); ok {
		t.Error("a temporary copy is refused")
	}
}

// The dry run works on its own copy: the store it is given is not changed
// (its bytes stay the same) and nothing is added beside it.
func TestBoardDryRunWorksOnACopy(t *testing.T) {
	src := t.TempDir()
	path := filepath.Join(src, "store.db")
	st, err := store.Open(context.Background(), path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]string {
		out := map[string]string{}
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(src, e.Name()))
			out[e.Name()] = string(b)
		}
		return out
	}
	before := read()
	t.Setenv("MALACHI_BOARD_DRYRUN_STORE", path)
	TestBoardDryRun(t)
	after := read()
	if len(after) != len(before) {
		t.Fatalf("files beside the store: %d, were %d", len(after), len(before))
	}
	for name, b := range before {
		if after[name] != b {
			t.Errorf("%s changed", name)
		}
	}
}
