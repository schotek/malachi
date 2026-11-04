// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package daemon starts and stops the malachid process on the UI's behalf.
//
// Nothing else on a desktop does: the Flatpak runs one command, the
// autostart entry launches only the UI, and there is no systemd unit. The
// supervisor is process management only: it knows where the daemon binary
// is, which socket to hand it and whether it is still alive. It contains no
// mail logic and never speaks the protocol (the client does that).
//
// A daemon that already answers on the socket (make run-backend, a debugger,
// one left behind by a UI crash) is used as is and never stopped: the
// supervisor only ever signals the process it started itself.
package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// PathEnv names the daemon executable, or "none" to never start one (the
// UI then only shows its banner until something else provides a daemon).
const PathEnv = "MALACHI_DAEMON"

// Variables rather than constants so the tests can shorten them.
var (
	// startTimeout bounds the wait for the socket after a spawn. The first
	// start runs the store migrations, which is still well under a second;
	// the margin is for a cold disk.
	startTimeout = 15 * time.Second
	// stopTimeout bounds the wait for a graceful exit after SIGTERM. The
	// daemon itself gives its syncers 10 s to log out, then closes the store.
	stopTimeout = 15 * time.Second
	// pollInterval is how often the socket is dialled while waiting.
	pollInterval = 100 * time.Millisecond
	// maxBackoff caps the pause between restarts of a daemon that keeps
	// exiting: the first exit is retried at once, then the pause doubles
	// from one second with every further exit in a row.
	maxBackoff = 60 * time.Second
)

// ErrNoDaemon is returned by Ensure when no daemon answers and the
// supervisor has no executable to start.
var ErrNoDaemon = errors.New("no malachid to start")

// Supervisor runs at most one daemon process. It is safe for concurrent use;
// Ensure calls serialise, so a second caller waits for the first spawn
// instead of racing it.
type Supervisor struct {
	// Socket is passed as --socket, so the daemon listens exactly where the
	// client dials, whatever MALACHI_SOCKET says.
	Socket string
	// Path is the daemon executable; "" disables spawning (see Locate).
	Path string

	log *slog.Logger

	ensureMu sync.Mutex // serialises Ensure
	mu       sync.Mutex // guards the fields below
	cmd      *exec.Cmd
	exited   chan struct{} // closed by the waiter when cmd has exited
	exitErr  error
	stopping bool
	failures int // exits in a row without the socket ever answering
	nextTry  time.Time
	spawns   int // for tests
}

// New returns a supervisor for the daemon at path, or one that never spawns
// when path is "".
func New(socket, path string, log *slog.Logger) *Supervisor {
	return &Supervisor{Socket: socket, Path: path, log: log}
}

// Locate finds the daemon executable: $MALACHI_DAEMON when set (a path, or
// "none" for nothing), otherwise malachid beside the running executable
// (/app/bin in Flatpak, build/ in a source tree, the install prefix
// otherwise), otherwise on $PATH. "" with a nil error means spawning is
// switched off on purpose; an error means nothing was found.
func Locate() (string, error) {
	if v, ok := os.LookupEnv(PathEnv); ok {
		if v == "" || v == "none" {
			return "", nil
		}
		return v, nil
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "malachid")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	p, err := exec.LookPath("malachid")
	if err != nil {
		return "", fmt.Errorf("malachid not found beside %s or on PATH (%s=none switches this off)", os.Args[0], PathEnv)
	}
	return p, nil
}

// Ensure returns nil once a daemon answers on the socket. When none does it
// starts the one at Path and waits for its socket. A daemon that exits
// before answering counts as a failure and the next start is delayed with
// an exponential backoff, during which Ensure returns an error at once.
func (s *Supervisor) Ensure() error {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()

	if answers(s.Socket) {
		return nil
	}
	if s.Path == "" {
		return ErrNoDaemon
	}

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return errors.New("shutting down")
	}
	if s.cmd != nil {
		select {
		case <-s.exited:
			s.noteExit()
		default:
			// Started earlier and still running: it may just be slow.
			s.mu.Unlock()
			return s.await()
		}
	}
	if wait := time.Until(s.nextTry); wait > 0 {
		s.mu.Unlock()
		return fmt.Errorf("malachid exited %d times in a row; next start in %s", s.failures, wait.Round(time.Second))
	}
	if err := s.spawn(); err != nil {
		s.failures++
		s.nextTry = time.Now().Add(s.backoff())
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.await()
}

// Stop terminates the daemon this supervisor started, if any: SIGTERM,
// then SIGKILL when it has not exited within stopTimeout. A daemon the
// supervisor did not start is left alone.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopping = true
	cmd, exited := s.cmd, s.exited
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	select {
	case <-exited:
		return // already gone
	default:
	}
	s.log.Info("stopping malachid", "pid", cmd.Process.Pid)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.log.Warn("signal malachid", "err", err)
	}
	select {
	case <-exited:
	case <-time.After(stopTimeout):
		s.log.Warn("malachid did not exit in time; killing it", "timeout", stopTimeout)
		_ = cmd.Process.Kill()
		<-exited
	}
}

// spawn starts the daemon. Called with mu held.
func (s *Supervisor) spawn() error {
	cmd := exec.Command(s.Path, "--socket", s.Socket)
	// The daemon logs to stderr like the UI; one terminal or journal shows
	// both. No process group of its own: a Ctrl+C in that terminal is meant
	// to reach both, and the daemon shuts down cleanly on SIGINT.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.Path, err)
	}
	s.cmd = cmd
	s.exited = make(chan struct{})
	s.exitErr = nil
	s.spawns++
	s.log.Info("started malachid", "pid", cmd.Process.Pid, "path", s.Path, "socket", s.Socket)
	exited := s.exited
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.exitErr = err
		stopping := s.stopping
		s.mu.Unlock()
		if !stopping {
			s.log.Error("malachid exited", "pid", cmd.Process.Pid, "err", err)
		}
		close(exited)
	}()
	return nil
}

// noteExit records the exit of the current process. Called with mu held
// after exited is closed.
func (s *Supervisor) noteExit() {
	s.cmd = nil
	s.failures++
	s.nextTry = time.Now().Add(s.backoff())
}

// backoff is the pause before the next start. Called with mu held.
func (s *Supervisor) backoff() time.Duration {
	if s.failures <= 1 {
		return 0 // a single exit (a crash after hours of running) is retried at once
	}
	d := time.Second
	for i := 2; i < s.failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// await waits for the running daemon's socket to answer. Called without
// mu held.
func (s *Supervisor) await() error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()

	deadline := time.After(startTimeout)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if answers(s.Socket) {
			s.mu.Lock()
			s.failures = 0
			s.nextTry = time.Time{}
			s.mu.Unlock()
			return nil
		}
		select {
		case <-exited:
			s.mu.Lock()
			err := s.exitErr
			if s.cmd != nil {
				s.noteExit()
			}
			s.mu.Unlock()
			if err == nil {
				err = errors.New("exited")
			}
			return fmt.Errorf("malachid %v before opening its socket", err)
		case <-deadline:
			return fmt.Errorf("malachid did not open %s within %s", s.Socket, startTimeout)
		case <-tick.C:
		}
	}
}

// Running reports whether a process started by this supervisor is alive.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return false
	}
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// answers reports whether something accepts connections on the socket.
func answers(socket string) bool {
	c, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
