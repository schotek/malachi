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
	ErrOutboxBusy      = errors.New("store: outbox message is being sent")
	ErrOutbox          = errors.New("store: not allowed for an outbox message")
)

// Store wraps the database handle.
type Store struct {
	db   *sql.DB
	path string
	log  *slog.Logger
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
	dsn := "file:" + path + "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	_ = os.Chmod(path, 0o600)

	s := &Store{db: db, path: path, log: log.With("component", "store")}
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
	return s, nil
}

// Path returns the database file location.
func (s *Store) Path() string { return s.path }

// AttachmentDir is where attachment data lives (0600 files in a 0700
// directory next to the database); metadata is in the attachments table.
func (s *Store) AttachmentDir() string {
	return filepath.Join(filepath.Dir(s.path), "attachments")
}

// MessageDir is where raw RFC 822 messages live, one 0600 file per message
// under a 0700 per-account subdirectory (MessageRawPath); headers and text
// bodies are in the messages table.
func (s *Store) MessageDir() string {
	return filepath.Join(filepath.Dir(s.path), "messages")
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
