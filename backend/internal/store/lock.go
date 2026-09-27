// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
// a second daemon is refused at once. A variable so that tests can shorten
// it.
var lockWaitMillis = 1000

// StoreLock is a process's claim on a store (Lock): while one process holds
// it, no other can take it, so two daemons never sync, send from and write
// the same store at once. The operating system gives it up when the
// process ends, however it ends.
type StoreLock struct {
	db   *sql.DB
	conn *sql.Conn
	path string
}

// LockPath returns the lock file of the store at storePath: the same path
// with ".lock" appended, beside the database in its private directory.
func LockPath(storePath string) string { return storePath + ".lock" }

// Lock takes the lock on the store at storePath before anything else of
// the daemon touches the store or the socket. The lock is an EXCLUSIVE
// transaction, never committed, on a SQLite file of its own beside the
// store (LockPath): SQLite's file locking works alike on every platform
// (POSIX record locks, LockFileEx on Windows) without code of our own for
// each, and the kernel drops such a lock with its process, so a crash
// leaves nothing to clean up. The file stays when the lock is given up.
// Lock fails with ErrStoreLocked while another process holds the lock.
func Lock(ctx context.Context, storePath string) (*StoreLock, error) {
	path := LockPath(storePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	// Create the file private before SQLite opens it, which would create
	// it with the process's umask: it holds nothing, but a process that
	// can open it can also hold a lock on it.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store lock %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("create store lock %s: %w", path, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", uriPath(path), lockWaitMillis)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	// One connection, held for the lock's life: the transaction is on it.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		conn.Close()
		db.Close()
		if isBusy(err) {
			return nil, fmt.Errorf("%w: %s", ErrStoreLocked, storePath)
		}
		return nil, fmt.Errorf("take store lock %s: %w", path, err)
	}
	return &StoreLock{db: db, conn: conn, path: path}, nil
}

// Path returns the lock file's location.
func (l *StoreLock) Path() string { return l.path }

// Close gives the lock up: closing its connection rolls the transaction
// back. The file stays for the next start.
func (l *StoreLock) Close() error {
	return errors.Join(l.conn.Close(), l.db.Close())
}

// isBusy reports whether err is SQLite's SQLITE_BUSY (in any of its
// extended forms): the lock is held elsewhere.
func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_BUSY
}
