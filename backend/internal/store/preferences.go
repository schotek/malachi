// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetPreference returns the stored value for key. The second result is false
// when no value has been set.
func (s *Store) GetPreference(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM preferences WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("get preference %q: %w", key, err)
	}
	return v, true, nil
}

// SetPreference stores value under key, replacing any previous value.
func (s *Store) SetPreference(ctx context.Context, key, value string) error {
	return s.SetPreferences(ctx, []Pref{{Key: key, Value: value}})
}

// Pref is one stored preference.
type Pref struct {
	Key, Value string
}

// SetPreferences stores every value under its key, replacing any previous
// one, in one transaction: all of them or none.
func (s *Store) SetPreferences(ctx context.Context, prefs []Pref) error {
	_, err := s.writePreferences(ctx, prefs, `
		INSERT INTO preferences (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`)
	return err
}

// InitPreferences stores each value whose key has none yet, in one
// transaction, and returns the keys it stored; a stored value is never
// replaced.
func (s *Store) InitPreferences(ctx context.Context, prefs []Pref) ([]string, error) {
	return s.writePreferences(ctx, prefs, `
		INSERT INTO preferences (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO NOTHING`)
}

// writePreferences runs query (an insert of key and value) for every pref
// in one transaction and returns the keys whose row it changed.
func (s *Store) writePreferences(ctx context.Context, prefs []Pref, query string) ([]string, error) {
	if len(prefs) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("set preferences: %w", err)
	}
	defer tx.Rollback()
	var changed []string
	for _, p := range prefs {
		res, err := tx.ExecContext(ctx, query, p.Key, p.Value)
		if err != nil {
			return nil, fmt.Errorf("set preference %q: %w", p.Key, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			changed = append(changed, p.Key)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("set preferences: %w", err)
	}
	return changed, nil
}
