// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package fsretry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortWaits gives Do n waits of a millisecond for the rest of the test.
func shortWaits(t *testing.T, n int) {
	t.Helper()
	saved := Waits
	Waits = make([]time.Duration, n)
	for i := range Waits {
		Waits[i] = time.Millisecond
	}
	t.Cleanup(func() { Waits = saved })
}

func TestDoStopsAtSuccessAndAtNotExist(t *testing.T) {
	shortWaits(t, 3)
	gone := &fs.PathError{Op: "remove", Path: "x", Err: fs.ErrNotExist}
	for _, want := range []error{nil, fs.ErrNotExist, gone} {
		calls := 0
		err := Do(func() error { calls++; return want })
		if err != want || calls != 1 {
			t.Errorf("Do returning %v: %v after %d calls, want one call", want, err, calls)
		}
	}
}

func TestDoRetriesOtherErrors(t *testing.T) {
	shortWaits(t, 3)
	busy := errors.New("busy")
	calls := 0
	if err := Do(func() error { calls++; return busy }); err != busy || calls != 4 {
		t.Errorf("an error that lasts: %v after %d calls, want busy after 4", err, calls)
	}
	calls = 0
	err := Do(func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("an error that passes: %v after %d calls, want nil after 3", err, calls)
	}
}

// holdFor opens path and closes it after d, as a reader of the file would.
func holdFor(t *testing.T, path string, d time.Duration) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	time.AfterFunc(d, func() {
		f.Close()
		close(released)
	})
	t.Cleanup(func() { <-released })
}

// A reader holding the file does not make Remove or Rename fail: Windows
// refuses both while the handle is open, and the retries outlast it.
func TestRemoveAndRenameOutlastAReader(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	held := write("held", "x")
	holdFor(t, held, 100*time.Millisecond)
	if err := Remove(held); err != nil {
		t.Fatalf("remove while a reader holds the file: %v", err)
	}
	if _, err := os.Stat(held); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed file still there: %v", err)
	}

	target, tmp := write("target", "old"), write("target.tmp", "new")
	holdFor(t, target, 100*time.Millisecond)
	if err := Rename(tmp, target); err != nil {
		t.Fatalf("rename over a file a reader holds: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "new" {
		t.Fatalf("target after rename: %q %v", b, err)
	}

	if err := Remove(filepath.Join(dir, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("remove of a missing file: %v", err)
	}
}
