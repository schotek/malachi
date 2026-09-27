// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrStoreLocked is what Lock fails with while another process holds the
// store.
var ErrStoreLocked = errors.New("another malachid is using the store")

// lockWaitMillis is how long Lock waits for a lock another process holds
// before it gives up: long enough for a holder that is just exiting (the
// system may drop a dead process's locks a moment late), short enough that
// a second daemon is refused within a second. A variable so that tests can
// shorten it.
var lockWaitMillis = 1000

// lockSuffix names the lock file after the store. Not "<store>.lock":
// that is the lock directory SQLite itself makes beside a database on a
// file system without byte-range locks (its dot-file locking).
const lockSuffix = ".daemon.lock"

// StoreLock is a process's claim on a store (Lock): while one process holds
// it, no other can take it, so two daemons never sync, send from and write
// the same store at once. The operating system gives it up when the
// process ends, however it ends.
type StoreLock struct {
	db   *sql.DB
	conn *sql.Conn
	path string
}

// LockPath returns the lock file of the store at storePath: beside the
// database in its private directory, named after it (lockSuffix). A
// store reached through a symbolic link has the lock of the file the link
// leads to, so that two paths to one store cannot have two locks.
func LockPath(storePath string) string {
	if real, err := filepath.EvalSymlinks(storePath); err == nil {
		storePath = real
	}
	return storePath + lockSuffix
}

// IsLockFile reports whether path names the lock file of the store at
// storePath, directly or through a link. It only looks at the files'
// identities (stat), without opening either.
func IsLockFile(storePath, path string) bool {
	lock, err := os.Stat(LockPath(storePath))
	if err != nil {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && os.SameFile(fi, lock)
}

// Lock takes the lock on the store at storePath before anything else of
// the daemon touches the store or the socket. The lock is an EXCLUSIVE
// transaction, never committed, on a SQLite file of its own (LockPath):
// SQLite's file locking works alike on every platform (POSIX record locks,
// LockFileEx on Windows) without code of our own for each, and the kernel
// drops such a lock with its process, so a crash leaves nothing to clean
// up. The file stays when the lock is given up. Lock fails with
// ErrStoreLocked while another process holds the lock.
//
// Nothing else in the process may open the lock file (IsLockFile guards
// attachment.import): POSIX record locks belong to the process, and
// closing any descriptor of the file, not only SQLite's own, drops them.
func Lock(ctx context.Context, storePath string) (*StoreLock, error) {
	path := LockPath(storePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	// Create the file private before SQLite opens it, which would create
	// it with the process's umask. An existing one is left unopened (see
	// above): it is the file of an earlier run, made here the same way.
	switch f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); {
	case err == nil:
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("create store lock %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrExist):
		return nil, fmt.Errorf("create store lock %s: %w", path, err)
	}

	// No journal: the transaction never writes, and a journal file would
	// only be one more file to explain.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(OFF)", uriPath(path), lockWaitMillis)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	// One connection, held for the lock's life: the transaction is on it.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, lockError(path, storePath, err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		conn.Close()
		db.Close()
		return nil, lockError(path, storePath, err)
	}
	return &StoreLock{db: db, conn: conn, path: path}, nil
}

// lockError explains why the lock at path could not be taken.
func lockError(path, storePath string, err error) error {
	switch sqliteCode(err) {
	case sqlite3.SQLITE_BUSY:
		return fmt.Errorf("%w: %s", ErrStoreLocked, storePath)
	case sqlite3.SQLITE_NOTADB:
		return fmt.Errorf("take store lock: %s is not a lock file; remove it if no malachid runs: %w", path, err)
	}
	return fmt.Errorf("take store lock %s: %w", path, err)
}

// Path returns the lock file's location.
func (l *StoreLock) Path() string { return l.path }

// Close gives the lock up: closing its connection rolls the transaction
// back. The file stays for the next start.
func (l *StoreLock) Close() error {
	return errors.Join(l.conn.Close(), l.db.Close())
}

// sqliteCode returns the primary SQLite result code of err (extended codes
// folded), or -1 when err is not a SQLite error.
func sqliteCode(err error) int {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return -1
	}
	return se.Code() & 0xff
}
