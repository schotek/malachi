// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// logFileMax is the size at which the log file is rotated: the current
// one becomes PATH.1 (replacing an older PATH.1) and a new one starts, so
// at most twice this much stays on disk.
const logFileMax = 5 << 20

// logFile is the --log-file writer: the client that starts the daemon
// names the file (a desktop app has no terminal and no journal to read
// stderr from). It is created 0600 in a directory made 0700, appended to
// across starts and rotated by size. A failed write is dropped: logging
// never stops the daemon.
type logFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
	// crash: the runtime's crash output follows the file across rotations.
	crash bool
}

func openLogFile(path string) (*logFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("log file: %w", err)
	}
	l := &logFile{path: path}
	if err := l.open(); err != nil {
		return nil, err
	}
	if l.size >= logFileMax {
		l.rotate()
	}
	return l, nil
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("log file: %w", err)
	}
	// A file another user made readable stays private from now on.
	_ = f.Chmod(0o600)
	l.f, l.size = f, st.Size()
	return nil
}

// rotate closes the file before renaming it (Windows cannot rename an
// open file) and opens a new one; on failure it keeps writing where it can.
func (l *logFile) rotate() {
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	old := l.path + ".1"
	_ = os.Remove(old)
	_ = os.Rename(l.path, old)
	if l.open() == nil && l.crash {
		_ = debug.SetCrashOutput(l.f, debug.CrashOptions{})
	}
}

// Write appends p, rotating first when p would take the file over the limit.
func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > logFileMax {
		l.rotate()
	}
	if l.f == nil {
		return len(p), nil
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	if err != nil {
		return len(p), nil
	}
	return n, nil
}

// CrashOutput sends a panic or fatal error of the runtime to the file
// (debug.SetCrashOutput takes a duplicate of the descriptor, so it is set
// again after every rotation).
func (l *logFile) CrashOutput() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.crash = true
	if l.f == nil {
		return nil
	}
	return debug.SetCrashOutput(l.f, debug.CrashOptions{})
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
