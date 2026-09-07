// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package daemon

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the daemon: when fakeEnv is set, TestMain
// runs fakeDaemon instead of the tests. The supervisor inherits the
// environment, so a t.Setenv in the test selects the behaviour of the
// process it spawns.
const fakeEnv = "MALACHI_TEST_FAKE_DAEMON"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		fakeDaemon(mode)
		return
	}
	os.Exit(m.Run())
}

// fakeDaemon mimics malachid's process behaviour: "listen" opens the socket
// and exits on SIGTERM; "slow" does the same after a delay; "exit" exits
// with status 1 at once; "deaf" listens and ignores SIGTERM.
func fakeDaemon(mode string) {
	socket := ""
	for i, a := range os.Args {
		if a == "--socket" && i+1 < len(os.Args) {
			socket = os.Args[i+1]
		}
	}
	switch mode {
	case "exit":
		os.Exit(1)
	case "slow":
		time.Sleep(400 * time.Millisecond)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(2)
	}
	sig := make(chan os.Signal, 1)
	if mode == "deaf" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	<-sig
	ln.Close()
	os.Exit(0)
}

func newSupervisor(t *testing.T, mode string) (*Supervisor, string) {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	sock := filepath.Join(t.TempDir(), "rpc.sock")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := New(sock, exe, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(s.Stop)
	return s, sock
}

func TestEnsureUsesRunningDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "rpc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := New(sock, "/nonexistent/malachid", slog.Default())
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if s.spawns != 0 || s.Running() {
		t.Fatalf("spawned %d, running %v; expected the listening daemon to be used", s.spawns, s.Running())
	}
	s.Stop() // must not touch the foreign process
	if !answers(sock) {
		t.Fatal("Stop closed a daemon it did not start")
	}
}

func TestEnsureWithoutPath(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "rpc.sock"), "", slog.Default())
	if err := s.Ensure(); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("Ensure = %v, want ErrNoDaemon", err)
	}
}

func TestEnsureStartsAndStops(t *testing.T) {
	s, sock := newSupervisor(t, "listen")
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !s.Running() || !answers(sock) {
		t.Fatalf("running %v, answers %v", s.Running(), answers(sock))
	}
	if s.spawns != 1 {
		t.Fatalf("spawns = %d, want 1", s.spawns)
	}
	// A second Ensure finds the daemon and does nothing.
	if err := s.Ensure(); err != nil || s.spawns != 1 {
		t.Fatalf("second Ensure: err %v, spawns %d", err, s.spawns)
	}

	s.Stop()
	if s.Running() {
		t.Fatal("daemon still running after Stop")
	}
	if answers(sock) {
		t.Fatal("socket still answers after Stop")
	}
	// After Stop nothing is started any more.
	if err := s.Ensure(); err == nil || s.spawns != 1 {
		t.Fatalf("Ensure after Stop: err %v, spawns %d", err, s.spawns)
	}
}

func TestEnsureWaitsForSlowDaemon(t *testing.T) {
	s, sock := newSupervisor(t, "slow")
	// Concurrent callers (the startup goroutine and the window's reconnect)
	// share one spawn and both return once the socket answers.
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.Ensure()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Ensure #%d: %v", i, err)
		}
	}
	if s.spawns != 1 || !answers(sock) {
		t.Fatalf("spawns = %d, answers %v", s.spawns, answers(sock))
	}
}

func TestEnsureBacksOffAfterRepeatedExits(t *testing.T) {
	s, _ := newSupervisor(t, "exit")
	err := s.Ensure()
	if err == nil || !strings.Contains(err.Error(), "before opening its socket") {
		t.Fatalf("first Ensure = %v", err)
	}
	// One exit is retried at once.
	err = s.Ensure()
	if err == nil || !strings.Contains(err.Error(), "before opening its socket") {
		t.Fatalf("second Ensure = %v", err)
	}
	if s.spawns != 2 {
		t.Fatalf("spawns = %d, want 2", s.spawns)
	}
	// The second exit in a row starts the backoff: no spawn, an immediate error.
	start := time.Now()
	err = s.Ensure()
	if err == nil || !strings.Contains(err.Error(), "next start in") {
		t.Fatalf("third Ensure = %v", err)
	}
	if s.spawns != 2 {
		t.Fatalf("spawned during backoff: spawns = %d", s.spawns)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Ensure blocked %s during backoff", time.Since(start))
	}
}

func TestEnsureTimesOut(t *testing.T) {
	old := startTimeout
	startTimeout = 300 * time.Millisecond
	t.Cleanup(func() { startTimeout = old })

	// A daemon slower than the timeout: alive, but the socket never comes.
	s, _ := newSupervisor(t, "slow")
	err := s.Ensure()
	if err == nil || !strings.Contains(err.Error(), "did not open") {
		t.Fatalf("Ensure = %v, want timeout", err)
	}
}

func TestStopKillsDeafDaemon(t *testing.T) {
	old := stopTimeout
	stopTimeout = 300 * time.Millisecond
	t.Cleanup(func() { stopTimeout = old })

	s, _ := newSupervisor(t, "deaf")
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	start := time.Now()
	s.Stop()
	if s.Running() {
		t.Fatal("daemon survived Stop")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Stop took %s", time.Since(start))
	}
}

func TestStopWithoutDaemon(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "rpc.sock"), "/nonexistent/malachid", slog.Default())
	s.Stop() // no panic, nothing to do
}

// unsetenv removes key for the duration of the test (t.Setenv registers the
// restore; "" alone would mean "none" to Locate).
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func TestLocate(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		t.Setenv(PathEnv, "none")
		if p, err := Locate(); p != "" || err != nil {
			t.Fatalf("Locate = %q, %v", p, err)
		}
	})
	t.Run("explicit", func(t *testing.T) {
		t.Setenv(PathEnv, "/opt/malachi/malachid")
		if p, err := Locate(); p != "/opt/malachi/malachid" || err != nil {
			t.Fatalf("Locate = %q, %v", p, err)
		}
	})
	t.Run("path", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "malachid")
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		unsetenv(t, PathEnv)
		if got, err := Locate(); got != p || err != nil {
			t.Fatalf("Locate = %q, %v; want %q", got, err, p)
		}
	})
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		unsetenv(t, PathEnv)
		if got, err := Locate(); err == nil {
			t.Fatalf("Locate = %q, want an error", got)
		}
	})
}
