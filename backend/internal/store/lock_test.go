// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortLockWait makes a held lock fail fast in these tests.
func shortLockWait(t *testing.T) {
	t.Helper()
	old := lockWaitMillis
	lockWaitMillis = 50
	t.Cleanup(func() { lockWaitMillis = old })
}

func mustLock(t *testing.T, storePath string) *StoreLock {
	t.Helper()
	l, err := Lock(context.Background(), storePath)
	if err != nil {
		t.Fatalf("Lock(%s): %v", storePath, err)
	}
	return l
}

func TestLockIsExclusive(t *testing.T) {
	shortLockWait(t)
	store := filepath.Join(t.TempDir(), "store.db")

	first := mustLock(t, store)
	_, err := Lock(context.Background(), store)
	if !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("second Lock: err = %v, want ErrStoreLocked", err)
	}
	if !strings.Contains(err.Error(), store) {
		t.Errorf("error %q does not name the store", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := mustLock(t, store)
	if err := again.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLockIsPerStore(t *testing.T) {
	shortLockWait(t)
	dir := t.TempDir()
	a := mustLock(t, filepath.Join(dir, "a.db"))
	defer a.Close()
	b := mustLock(t, filepath.Join(dir, "b.db"))
	defer b.Close()
}

func TestLockFile(t *testing.T) {
	shortLockWait(t)
	// A directory that does not exist yet, below one named with the
	// characters a "file:" URI treats specially; Windows allows no "?" in a
	// name, so there the name goes without it.
	base := t.TempDir()
	odd := filepath.Join(base, "odd #1? 100%")
	if os.Mkdir(odd, 0o700) != nil {
		odd = filepath.Join(base, "odd #1 100%")
	}
	dir := filepath.Join(odd, "sub")
	store := filepath.Join(dir, "store.db")

	l := mustLock(t, store)
	defer l.Close()
	// Not "store.db.lock": SQLite's own dot-file lock would be that.
	if want := store + ".daemon.lock"; l.Path() != want || LockPath(store) != want {
		t.Errorf("lock path %q / %q, want %q", l.Path(), LockPath(store), want)
	}
	fi, err := os.Stat(l.Path())
	if err != nil {
		t.Fatalf("lock file: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("lock file mode %v, want a regular file", fi.Mode())
	}
	// Mode 0600, compared with a file made 0600 in the same directory so
	// that the check means the same wherever the platform has no modes.
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pfi, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != pfi.Mode().Perm() {
		t.Errorf("lock file permissions %v, want those of a 0600 file (%v)", fi.Mode().Perm(), pfi.Mode().Perm())
	}
	// Nothing but the lock file itself, and not the store.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != filepath.Base(l.Path()) && n != "probe" {
			t.Errorf("Lock left %s in the store's directory", n)
		}
	}
}

func TestLockSurvivesAnExistingLockFile(t *testing.T) {
	shortLockWait(t)
	store := filepath.Join(t.TempDir(), "store.db")
	// The file of an earlier run stays after Close.
	mustLock(t, store).Close()
	if _, err := os.Stat(LockPath(store)); err != nil {
		t.Fatalf("lock file after Close: %v", err)
	}
	l := mustLock(t, store)
	l.Close()
}

func TestLockRefusesAForeignFile(t *testing.T) {
	shortLockWait(t)
	store := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(LockPath(store), []byte("not a database, and long enough to be read as one"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Lock(context.Background(), store)
	if err == nil || errors.Is(err, ErrStoreLocked) || !strings.Contains(err.Error(), "not a lock file") {
		t.Fatalf("Lock over a foreign file: %v, want a \"not a lock file\" error", err)
	}
}

func TestLockPathFollowsALinkedStore(t *testing.T) {
	shortLockWait(t)
	dir := t.TempDir()
	store := filepath.Join(dir, "store.db")
	if err := os.WriteFile(store, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(store, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if LockPath(link) != LockPath(store) {
		t.Fatalf("LockPath(link) = %q, LockPath(store) = %q", LockPath(link), LockPath(store))
	}
	l := mustLock(t, store)
	defer l.Close()
	if _, err := Lock(context.Background(), link); !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("Lock through a link to a locked store: %v, want ErrStoreLocked", err)
	}
}

func TestIsLockFile(t *testing.T) {
	shortLockWait(t)
	dir := t.TempDir()
	store := filepath.Join(dir, "store.db")
	if IsLockFile(store, LockPath(store)) {
		t.Error("IsLockFile before the lock file exists")
	}
	l := mustLock(t, store)
	defer l.Close()

	if !IsLockFile(store, l.Path()) {
		t.Error("IsLockFile(lock file) = false")
	}
	if link := filepath.Join(dir, "sym"); os.Symlink(l.Path(), link) == nil && !IsLockFile(store, link) {
		t.Error("IsLockFile(symbolic link to it) = false")
	}
	if hard := filepath.Join(dir, "hard"); os.Link(l.Path(), hard) == nil && !IsLockFile(store, hard) {
		t.Error("IsLockFile(hard link to it) = false")
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{other, store, filepath.Join(dir, "missing"), dir} {
		if IsLockFile(store, p) {
			t.Errorf("IsLockFile(%s) = true", p)
		}
	}
}

func TestLockWaitsForAHolderThatLetsGo(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store.db")
	first := mustLock(t, store)
	// The default wait outlasts a holder that lets go meanwhile.
	go func() {
		time.Sleep(200 * time.Millisecond)
		first.Close()
	}()
	l := mustLock(t, store)
	l.Close()
}

func TestLockCanceledContext(t *testing.T) {
	shortLockWait(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Lock(ctx, filepath.Join(t.TempDir(), "store.db")); err == nil {
		t.Fatal("Lock with a canceled context succeeded")
	}
}

// The helper process of TestLockAcrossProcesses: lockHolderEnv names the
// store it locks, lockHolderModeEnv what it does next while it holds the
// lock ("relock": try Lock again, as a second daemon in the same process
// would; "stat": ask IsLockFile about the lock file).
const (
	lockHolderEnv     = "MALACHI_TEST_LOCK_HOLDER"
	lockHolderModeEnv = "MALACHI_TEST_LOCK_HOLDER_MODE"
)

// TestLockHelperProcess is not a test of its own: TestLockAcrossProcesses
// runs this test binary again with lockHolderEnv set, and this function
// then takes the lock, says so on stdout and holds it until the process is
// killed or its stdin closes.
func TestLockHelperProcess(t *testing.T) {
	store := os.Getenv(lockHolderEnv)
	if store == "" {
		t.Skip("helper process for TestLockAcrossProcesses")
	}
	lockWaitMillis = 50
	l, err := Lock(context.Background(), store)
	if err != nil {
		os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(3)
	}
	switch os.Getenv(lockHolderModeEnv) {
	case "relock":
		if _, err := Lock(context.Background(), store); !errors.Is(err, ErrStoreLocked) {
			os.Stdout.WriteString("error: second Lock in the holder: " + errString(err) + "\n")
			os.Exit(3)
		}
	case "stat":
		if !IsLockFile(store, l.Path()) {
			os.Stdout.WriteString("error: IsLockFile in the holder\n")
			os.Exit(3)
		}
	}
	os.Stdout.WriteString("locked\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	l.Close()
	os.Exit(0)
}

func errString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

// TestLockAcrossProcesses holds the lock in another process, which is what
// it is for: the lock refuses this process while the other one holds it,
// also after the holder tried to lock again or looked at the file (on
// Linux and macOS a stray open and close of the file in the holder would
// drop its lock), and the system gives it up when the holder is killed,
// the way a crash ends a daemon.
func TestLockAcrossProcesses(t *testing.T) {
	for _, mode := range []string{"", "relock", "stat"} {
		t.Run("mode="+mode, func(t *testing.T) {
			shortLockWait(t)
			store := filepath.Join(t.TempDir(), "store.db")

			cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
			cmd.Env = append(os.Environ(), lockHolderEnv+"="+store, lockHolderModeEnv+"="+mode)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start helper: %v", err)
			}
			killed := false
			defer func() {
				if !killed {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()

			ready := make(chan string, 1)
			go func() {
				sc := bufio.NewScanner(stdout)
				for sc.Scan() {
					if line := sc.Text(); line == "locked" || strings.HasPrefix(line, "error: ") {
						ready <- line
						return
					}
				}
				ready <- "helper exited without taking the lock"
			}()
			select {
			case line := <-ready:
				if line != "locked" {
					t.Fatalf("helper: %s", line)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("helper did not take the lock in time")
			}

			if _, err := Lock(context.Background(), store); !errors.Is(err, ErrStoreLocked) {
				t.Fatalf("Lock while another process holds it: err = %v, want ErrStoreLocked", err)
			}

			// Kill the holder as a crash would end it: no Close runs.
			if err := cmd.Process.Kill(); err != nil {
				t.Fatalf("kill helper: %v", err)
			}
			_ = cmd.Wait()
			killed = true

			deadline := time.Now().Add(10 * time.Second)
			for {
				l, err := Lock(context.Background(), store)
				if err == nil {
					l.Close()
					return
				}
				if !errors.Is(err, ErrStoreLocked) || time.Now().After(deadline) {
					t.Fatalf("Lock after the holder died: %v", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
