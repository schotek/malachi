// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AlertState is cal_alerts.state.
type AlertState string

const (
	AlertPending   AlertState = "pending"   // not fired yet, or snoozed (SnoozedUntil set)
	AlertFired     AlertState = "fired"     // announced (notify.eventAlert due)
	AlertDismissed AlertState = "dismissed" // the user dismissed it
)

// Alert is a row of cal_alerts: one alarm of one occurrence of the next 7
// days (derived), with the user's state (authoritative). Its key is
// (EventID, OccurrenceKey, MinutesBefore); DueAt is when it fires, and a
// snoozed alert is pending again until SnoozedUntil.
type Alert struct {
	ID            string
	EventID       string
	OccurrenceKey string
	MinutesBefore int
	DueAt         time.Time
	State         AlertState
	SnoozedUntil  time.Time
}

// When is the time a pending alert fires: SnoozedUntil when snoozed, else
// DueAt.
func (a Alert) When() time.Time {
	if !a.SnoozedUntil.IsZero() {
		return a.SnoozedUntil
	}
	return a.DueAt
}

const alertColumns = `id, event_id, occurrence_key, minutes_before, due_at, state, snoozed_until`

func scanAlert(row scanner) (Alert, error) {
	var a Alert
	var due, state, snoozed string
	if err := row.Scan(&a.ID, &a.EventID, &a.OccurrenceKey, &a.MinutesBefore, &due, &state, &snoozed); err != nil {
		return Alert{}, err
	}
	a.DueAt, a.State, a.SnoozedUntil = parseStamp(due), AlertState(state), parseStamp(snoozed)
	return a, nil
}

// SyncAlerts makes want the alerts of a scope, in one transaction: the
// alerts of eventIDs, or with eventIDs nil every stored alert. An alert of
// want whose key is stored with the same DueAt keeps its row (id, state,
// snooze); one whose DueAt changed (its occurrence moved) gets a new row
// and starts pending; stored alerts of the scope not in want are deleted.
// Only the key and DueAt of want are read. Returns the stored alerts of
// want, in its order.
func (s *Store) SyncAlerts(ctx context.Context, eventIDs []string, want []Alert) ([]Alert, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sync alerts: %w", err)
	}
	defer tx.Rollback()
	type key struct {
		event, occ string
		minutes    int
	}
	stored := map[key]Alert{}
	collect := func(rows *sql.Rows, err error) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAlert(rows)
			if err != nil {
				return err
			}
			stored[key{a.EventID, a.OccurrenceKey, a.MinutesBefore}] = a
		}
		return rows.Err()
	}
	if eventIDs == nil {
		if err := collect(tx.QueryContext(ctx, `SELECT `+alertColumns+` FROM cal_alerts`)); err != nil {
			return nil, fmt.Errorf("sync alerts: %w", err)
		}
	} else {
		for _, chunk := range chunkStrings(eventIDs, 500) {
			if err := collect(tx.QueryContext(ctx, `SELECT `+alertColumns+` FROM cal_alerts WHERE event_id IN (`+
				inPlaceholders(len(chunk))+`)`, toAny(chunk)...)); err != nil {
				return nil, fmt.Errorf("sync alerts: %w", err)
			}
		}
	}
	out := make([]Alert, 0, len(want))
	keep := map[string]bool{}
	for _, w := range want {
		k := key{w.EventID, w.OccurrenceKey, w.MinutesBefore}
		if old, ok := stored[k]; ok && old.DueAt.Equal(w.DueAt) && !keep[old.ID] {
			keep[old.ID] = true
			out = append(out, old)
			continue
		}
		if old, ok := stored[k]; ok && !keep[old.ID] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM cal_alerts WHERE id = ?`, old.ID); err != nil {
				return nil, fmt.Errorf("sync alerts: %w", err)
			}
			delete(stored, k)
		}
		a := Alert{ID: newID("al_"), EventID: w.EventID, OccurrenceKey: w.OccurrenceKey, MinutesBefore: w.MinutesBefore,
			DueAt: w.DueAt, State: AlertPending}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cal_alerts (id, event_id, occurrence_key, minutes_before, due_at, state)
			VALUES (?, ?, ?, ?, ?, 'pending')
			ON CONFLICT (event_id, occurrence_key, minutes_before) DO NOTHING`,
			a.ID, a.EventID, a.OccurrenceKey, a.MinutesBefore, fmtStamp(a.DueAt)); err != nil {
			return nil, fmt.Errorf("insert alert: %w", err)
		}
		keep[a.ID] = true
		out = append(out, a)
	}
	for _, old := range stored {
		if !keep[old.ID] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM cal_alerts WHERE id = ?`, old.ID); err != nil {
				return nil, fmt.Errorf("sync alerts: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sync alerts: %w", err)
	}
	return out, nil
}

// NextAlert returns when the next pending alert fires (Alert.When); false
// when none is pending.
func (s *Store) NextAlert(ctx context.Context) (time.Time, bool, error) {
	var next sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(CASE WHEN snoozed_until != '' THEN snoozed_until ELSE due_at END)
		FROM cal_alerts WHERE state = 'pending'`).Scan(&next); err != nil {
		return time.Time{}, false, fmt.Errorf("next alert: %w", err)
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	return parseStamp(next.String), true, nil
}

// DueAlerts returns the pending alerts whose time (Alert.When) is at or
// before now, earliest first.
func (s *Store) DueAlerts(ctx context.Context, now time.Time) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+alertColumns+` FROM cal_alerts
		WHERE state = 'pending' AND (CASE WHEN snoozed_until != '' THEN snoozed_until ELSE due_at END) <= ?
		ORDER BY CASE WHEN snoozed_until != '' THEN snoozed_until ELSE due_at END, id`, fmtStamp(now))
	if err != nil {
		return nil, fmt.Errorf("due alerts: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("due alerts: %w", err)
	}
	return out, nil
}

// GetAlert returns one alert; ErrNotFound for an unknown id.
func (s *Store) GetAlert(ctx context.Context, id string) (Alert, error) {
	a, err := scanAlert(s.db.QueryRowContext(ctx, `SELECT `+alertColumns+` FROM cal_alerts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	if err != nil {
		return Alert{}, fmt.Errorf("get alert: %w", err)
	}
	return a, nil
}

// FireAlert marks a pending alert fired (the scheduler announced it, or
// found it too late and skips it) and clears its snooze. ErrNotFound for
// an unknown id, ErrConflict when it is not pending.
func (s *Store) FireAlert(ctx context.Context, id string) error {
	return s.setAlert(ctx, id, `state = 'fired', snoozed_until = ''`, nil, `state = 'pending'`)
}

// SnoozeAlert makes a fired alert pending again until until
// (event.snoozeAlert). ErrNotFound for an unknown id, ErrConflict when it
// has not fired.
func (s *Store) SnoozeAlert(ctx context.Context, id string, until time.Time) error {
	return s.setAlert(ctx, id, `state = 'pending', snoozed_until = ?`, []any{fmtStamp(until)}, `state = 'fired'`)
}

// DismissAlert marks an alert dismissed (event.dismissAlert), whatever its
// state. ErrNotFound for an unknown id.
func (s *Store) DismissAlert(ctx context.Context, id string) error {
	return s.setAlert(ctx, id, `state = 'dismissed', snoozed_until = ''`, nil, `1`)
}

func (s *Store) setAlert(ctx context.Context, id, set string, args []any, cond string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE cal_alerts SET `+set+` WHERE id = ? AND `+cond, append(args, id)...)
	if err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.GetAlert(ctx, id); err != nil {
		return err
	}
	return ErrConflict
}
