// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package fsretry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
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

// A failure that no open handle causes and no wait ends is returned after
// one attempt, wrapped as os functions wrap it; a missing permission,
// which a handle's refusal also is on Windows, is still retried.
func TestDoReturnsLastingErrorsAtOnce(t *testing.T) {
	shortWaits(t, 3)
	for _, errno := range []syscall.Errno{syscall.EROFS, syscall.ENOSPC, syscall.EDQUOT, syscall.EXDEV, syscall.ENOTDIR, syscall.EISDIR} {
		for _, want := range []error{
			errno,
			&fs.PathError{Op: "remove", Path: "x", Err: errno},
			&os.LinkError{Op: "rename", Old: "x.tmp", New: "x", Err: errno},
		} {
			calls := 0
			if err := Do(func() error { calls++; return want }); err != want || calls != 1 {
				t.Errorf("Do returning %v: %v after %d calls, want one call", want, err, calls)
			}
		}
	}
	denied := &os.LinkError{Op: "rename", Old: "x.tmp", New: "x", Err: syscall.EACCES}
	calls := 0
	if err := Do(func() error { calls++; return denied }); err != denied || calls != 4 {
		t.Errorf("Do returning %v: %v after %d calls, want 4", denied, err, calls)
	}
}

// A batch retries until one removal fails for good, then tries each file
// once: a missing file does not count as a failure.
func TestBatchStopsRetryingAfterALastingFailure(t *testing.T) {
	shortWaits(t, 3)
	calls := map[string]int{}
	denied := errors.New("access denied")
	saved := remove
	remove = func(path string) error {
		calls[path]++
		switch path {
		case "gone":
			return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
		case "stuck", "stuck too":
			return denied
		}
		return nil
	}
	t.Cleanup(func() { remove = saved })

	var b Batch
	steps := []struct {
		path  string
		err   error
		calls int
	}{
		{"gone", fs.ErrNotExist, 1},
		{"first", nil, 1},
		{"stuck", denied, 4}, // retried: the batch has not given up yet
		{"stuck too", denied, 1},
		{"last", nil, 1},
	}
	for _, s := range steps {
		err := b.Remove(s.path)
		if (s.err == nil) != (err == nil) || (s.err != nil && !errors.Is(err, s.err)) || calls[s.path] != s.calls {
			t.Errorf("%s: %v after %d attempts, want %v after %d", s.path, err, calls[s.path], s.err, s.calls)
		}
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
