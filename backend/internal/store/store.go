// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns the SQLite database: opening, pragmas, migrations and
// (later) all queries. No other package issues SQL.
//
// Driver: modernc.org/sqlite (pure Go, no cgo). FTS5 is compiled in.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Sentinel errors returned by the query methods; callers map them to API
// error codes.
var (
	ErrNotFound        = errors.New("store: not found")
	ErrExists          = errors.New("store: already exists")
	ErrVersionConflict = errors.New("store: version conflict")
	ErrAttachmentBound = errors.New("store: attachment bound to another draft")
	ErrTooBig          = errors.New("store: size limit exceeded")
	ErrBadCursor       = errors.New("store: bad cursor")
	ErrBadOrder        = errors.New("store: bad account order")
	ErrOutboxBusy      = errors.New("store: outbox message is being sent")
	ErrOutbox          = errors.New("store: not allowed for an outbox message")
	// ErrRawCorrupt: a stored raw message file cannot be read back intact
	// (a .zst file that is not one whole frame of this store's).
	ErrRawCorrupt = errors.New("store: raw message file is damaged")
	// ErrNoSpace: a raw file could not be written because the disk (or the
	// user's quota) is full. The error also wraps the system's own
	// (syscall.ENOSPC, syscall.EDQUOT).
	ErrNoSpace = errors.New("store: no space left on the device")
	// ErrConflict: the message changed under a raw-file operation that
	// expected an earlier state (CommitMessageRaw's Expect, or a codec
	// change during ConvertRawBatch).
	ErrConflict = errors.New("store: message changed")
	// ErrBusy: a message's file could not be replaced or removed because
	// one of the store's readers kept it open for longer than the store
	// waits. Windows refuses both while a handle of the file is open;
	// Linux and macOS never do. The file is as it was, and a later
	// attempt, once the reader is done, goes through.
	ErrBusy = errors.New("store: message file in use by a reader")
)

// Store wraps the database handle.
type Store struct {
	db   *sql.DB
	path string
	log  *slog.Logger

	// Raw message files (raw.go): the codec new files are written in, and
	// the per-message locks, reference-counted under rawMu.
	rawCodec atomic.Int32
	rawMu    sync.Mutex
	rawLocks map[string]*rawLock

	// foreignDirs are the message directories of accounts this store does
	// not know that the sweep has reported (SweepMessageFiles), by account.
	foreignDirs sync.Map

	// Test hooks; nil in the daemon. createFile makes every file the raw
	// writers and the staging area write (a test runs it out of space),
	// phaseACommit runs inside CommitMessageRaw's phase A just before a
	// commit that widens the remote set, afterPhaseA between its two
	// database phases and betweenConvertPhases between ConvertRawBatch's.
	// refuseOpen makes the store refuse, as Windows does, to rename over
	// or remove a message's file while one of its readers has it open, on
	// any system (refused), and so to remove the directory of an account
	// while one of them has a file of it open (removeAccountDir).
	// attemptFailed runs after every failed attempt of such a rename or
	// removal (fileOp), with the file's path. renameFile, in place of
	// os.Rename, renames a new file over a message's (renameRaw): a test
	// has one take effect and report a failure all the same.
	createFile           func(path string) (rawFile, error)
	phaseACommit         func(tx *sql.Tx) error
	afterPhaseA          func() error
	betweenConvertPhases func()
	refuseOpen           bool
	attemptFailed        func(path string)
	renameFile           func(oldpath, newpath string) error
}

// Open creates the parent directory if needed, opens the database with the
// project's pragmas and runs pending migrations.
func Open(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	// WAL for concurrent readers, foreign keys on, wait instead of failing on
	// short lock contention. Transactions take the write lock up front
	// (BEGIN IMMEDIATE): our transactions read then write, and in WAL mode a
	// deferred transaction upgrading to a write cannot wait on busy_timeout,
	// so a sync pass and an RPC mutation would otherwise fail with
	// SQLITE_BUSY instead of queueing. Mail data is private: restrict the
	// file mode.
	dsn := "file:" + uriPath(path) + "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	_ = os.Chmod(path, 0o600)

	s := &Store{db: db, path: path, log: log.With("component", "store"), rawLocks: map[string]*rawLock{}}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.MkdirAll(s.AttachmentDir(), 0o700); err != nil {
		db.Close()
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}
	if err := os.MkdirAll(s.MessageDir(), 0o700); err != nil {
		db.Close()
		return nil, fmt.Errorf("create message directory: %w", err)
	}
	if err := os.MkdirAll(s.stagingDir(), 0o700); err != nil {
		db.Close()
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	// Whatever is staged belongs to a process that is gone: the daemon
	// holds the store lock (Lock) before it opens the store.
	if n := s.sweepStaging(time.Time{}); n > 0 {
		s.log.Info("removed staged messages of an earlier run", "files", n)
	}
	return s, nil
}

// beginTx begins a transaction; with durable, one whose commit is on disk
// when Commit returns (beginDurable). end rolls back what was not
// committed and, for a durable one, gives its connection back.
func (s *Store) beginTx(ctx context.Context, durable bool) (tx *sql.Tx, end func(), err error) {
	if durable {
		return s.beginDurable(ctx)
	}
	if tx, err = s.db.BeginTx(ctx, nil); err != nil {
		return nil, nil, err
	}
	return tx, func() { _ = tx.Rollback() }, nil
}

// beginDurable begins a transaction whose commit is flushed to disk before
// Commit returns. The store commits with synchronous NORMAL (Open): in WAL
// mode a commit reaches the disk with the next checkpoint, and a crash of
// the system (not of the daemon) may lose the last ones, which leaves the
// database consistent and is all the store needs. A commit that a change
// outside the database relies on is different: CommitMessageRaw's phase A
// must be on disk before a stored file is replaced, or a power loss could
// keep the new file and lose the row that describes it. The transaction
// runs on a connection of its own set to synchronous FULL (the WAL is
// flushed at every commit) and fullfsync (F_FULLFSYNC where the system has
// it, macOS; elsewhere the pragma does nothing). end rolls back whatever
// was not committed and puts the connection's settings back before the
// pool gets it again, or closes the connection when they cannot be put
// back.
func (s *Store) beginDurable(ctx context.Context) (*sql.Tx, func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	release := func() {
		bg := context.WithoutCancel(ctx)
		_, err := conn.ExecContext(bg, `PRAGMA synchronous = NORMAL`)
		if err == nil {
			_, err = conn.ExecContext(bg, `PRAGMA fullfsync = 0`)
		}
		if err != nil {
			s.log.Warn("restore a connection after a flushed commit; closing it", "err", err)
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}
	// Neither setting can change inside a transaction.
	for _, pragma := range []string{`PRAGMA synchronous = FULL`, `PRAGMA fullfsync = 1`} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			release()
			return nil, nil, err
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		release()
		return nil, nil, err
	}
	return tx, func() { _ = tx.Rollback(); release() }, nil
}

// Path returns the database file location.
func (s *Store) Path() string { return s.path }

// uriPath makes a file name safe inside a "file:" URI: SQLite reads "?" as
// the start of the query and "#" as a fragment, and decodes "%HH", so all
// three are percent-encoded. A directory named after a fuzz seed
// ("seed#1") is where this bit first.
func uriPath(p string) string {
	return strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(p)
}

// AttachmentDir is where attachment data lives (0600 files in a 0700
// directory next to the database); metadata is in the attachments table.
func (s *Store) AttachmentDir() string {
	return filepath.Join(filepath.Dir(s.path), "attachments")
}

// MessageDir is where raw RFC 822 messages live, one 0600 file per message
// under a 0700 per-account subdirectory (MessageRawPath, plain or
// compressed); headers and text bodies are in the messages table.
func (s *Store) MessageDir() string {
	return filepath.Join(filepath.Dir(s.path), "messages")
}

// stagingDir holds messages being received before they are committed
// (StageRaw): 0600 files in a 0700 directory next to the database, on the
// file system of MessageDir, so that committing one is a rename.
func (s *Store) stagingDir() string {
	return filepath.Join(filepath.Dir(s.path), "staging")
}

// DB exposes the handle for internal packages. TODO: remove once all queries
// live in this package.
func (s *Store) DB() *sql.DB { return s.db }

// Close flushes and closes the database.
func (s *Store) Close() error {
	// Checkpoint so a clean shutdown leaves no -wal file behind.
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// migrate applies every embedded migration newer than the recorded version.
// Migrations are forward-only, numbered NNNN_name.sql, and never edited once
// committed (see CLAUDE.md).
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	migs, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migs {
		if m.version <= current {
			continue
		}
		s.log.Info("applying migration", "version", m.version, "name", m.name)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		base := e.Name()
		if !strings.HasSuffix(base, ".sql") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimSuffix(base, ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: expected NNNN_name.sql", base)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version: %w", base, err)
		}
		body, err := migrationFS.ReadFile("migrations/" + base)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: rest, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].version)
		}
	}
	return out, nil
}
