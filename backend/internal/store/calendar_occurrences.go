// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Occurrence is a row of cal_occurrences: one materialised instance in the
// account's window (a cache). EventID is the row whose fields show (the
// single, the master, or the override replacing the master's instance);
// OccurrenceKey is "" for a single event, else the recurrence id
// (FormatRecurrenceID); SeriesID the master of a series instance or
// override. A timed instance has UTC StartAt/EndAt; an all-day or
// floating one the date's midnight or its wall clock as if it were UTC.
// RemoteID is a Graph occurrence's own id.
type Occurrence struct {
	EventID       string
	OccurrenceKey string
	SeriesID      string
	CalendarID    string
	AccountID     string
	StartAt       time.Time
	EndAt         time.Time
	AllDay        bool
	Floating      bool
	RemoteID      string
}

const occurrenceColumns = `o.event_id, o.occurrence_key, o.series_id, o.calendar_id, o.account_id,
	o.start_at, o.end_at, o.all_day, o.floating, o.remote_id`

func scanOccurrence(row scanner, extra ...any) (Occurrence, error) {
	var o Occurrence
	var start, end string
	var allDay, floating int
	if err := row.Scan(append([]any{&o.EventID, &o.OccurrenceKey, &o.SeriesID, &o.CalendarID, &o.AccountID,
		&start, &end, &allDay, &floating, &o.RemoteID}, extra...)...); err != nil {
		return Occurrence{}, err
	}
	o.StartAt, o.EndAt = parseStamp(start), parseStamp(end)
	o.AllDay, o.Floating = allDay != 0, floating != 0
	return o, nil
}

// ReplaceOccurrences replaces the stored occurrences of the events with
// occ, in one transaction: the CalDAV rebuild of a series (also
// ReplaceResource's expand), or what a Graph calendarView gave for an
// event. An occurrence whose key repeats replaces the earlier one.
func (s *Store) ReplaceOccurrences(ctx context.Context, eventIDs []string, occ []Occurrence) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace occurrences: %w", err)
	}
	defer tx.Rollback()
	if err := replaceOccurrencesTx(ctx, tx, eventIDs, occ); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceOccurrencesTx(ctx context.Context, tx *sql.Tx, eventIDs []string, occ []Occurrence) error {
	for _, chunk := range chunkStrings(eventIDs, 500) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cal_occurrences WHERE event_id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...); err != nil {
			return fmt.Errorf("delete occurrences: %w", err)
		}
	}
	for _, o := range occ {
		if o.EventID == "" {
			return fmt.Errorf("replace occurrences: occurrence without an event")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cal_occurrences (event_id, occurrence_key, series_id, calendar_id,
				account_id, start_at, end_at, all_day, floating, remote_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (event_id, occurrence_key) DO UPDATE SET series_id = excluded.series_id,
				calendar_id = excluded.calendar_id, account_id = excluded.account_id, start_at = excluded.start_at,
				end_at = excluded.end_at, all_day = excluded.all_day, floating = excluded.floating,
				remote_id = excluded.remote_id`,
			o.EventID, o.OccurrenceKey, o.SeriesID, o.CalendarID, o.AccountID, fmtStamp(o.StartAt), fmtStamp(o.EndAt),
			boolInt(o.AllDay), boolInt(o.Floating), o.RemoteID); err != nil {
			return fmt.Errorf("insert occurrence: %w", err)
		}
	}
	return nil
}

// DeleteOccurrencesOutside removes an account's occurrences that end at or
// before from or start at or after to (the window moved). The events stay
// (TrimEvents).
func (s *Store) DeleteOccurrencesOutside(ctx context.Context, accountID string, from, to time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cal_occurrences WHERE account_id = ? AND (end_at <= ? OR start_at >= ?)`,
		accountID, fmtStamp(from), fmtStamp(to))
	if err != nil {
		return 0, fmt.Errorf("delete occurrences: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// allDayWidening is how far an all-day or floating occurrence (stored as
// if UTC) may lie from a UTC range in some zone: UTC−12 to UTC+14.
const allDayWidening = 14 * time.Hour

// OccurrenceQuery selects the occurrences overlapping [From, To). Which
// accounts and calendars count is the caller's decision (docs/calendar.md
// §4.5): CalendarIDs and AccountIDs restrict (empty = all),
// ExcludeAccountIDs leaves accounts out (paused, disabled, sync off),
// IncludeHidden adds the calendars the user hid, IncludeDeclined the
// events the user declined, HideNeedsActionAccountIDs leaves out the
// occurrences still waiting for the user's answer (my_response
// needsAction) of accounts whose showUnanswered is off. Zone matches an
// all-day or floating occurrence by the wall clock of [From, To) in it;
// nil widens the range by ±14 h for them (the caller filters exactly).
// Limit 0 = 2000.
type OccurrenceQuery struct {
	From, To                  time.Time
	Zone                      *time.Location
	CalendarIDs               []string
	AccountIDs                []string
	ExcludeAccountIDs         []string
	IncludeHidden             bool
	IncludeDeclined           bool
	HideNeedsActionAccountIDs []string
	Limit                     int
}

// OccurrenceList is EventRange's result: the occurrences by start
// (all-day before timed at the same stamp, then calendar order), the
// event rows they show (without Raw) by id, and whether Limit cut the
// list.
type OccurrenceList struct {
	Occurrences []Occurrence
	Events      map[string]Event
	Truncated   bool
}

// EventRange returns the stored occurrences overlapping [q.From, q.To):
// a timed one with StartAt < To and EndAt > From (an instant one also
// when it is at From), an all-day or floating one the same against the
// range's wall clock in q.Zone, or against the range widened by 14 hours
// on either side when q.Zone is nil.
func (s *Store) EventRange(ctx context.Context, q OccurrenceQuery) (OccurrenceList, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 2000
	}
	from, to := fmtStamp(q.From), fmtStamp(q.To)
	var wallFrom, wallTo string
	if q.Zone != nil {
		wallFrom, wallTo = fmtStamp(wallClock(q.From, q.Zone)), fmtStamp(wallClock(q.To, q.Zone))
	} else {
		wallFrom, wallTo = fmtStamp(q.From.Add(-allDayWidening)), fmtStamp(q.To.Add(allDayWidening))
	}
	where := ` WHERE ((o.all_day = 0 AND o.floating = 0 AND o.start_at < ? AND (o.end_at > ? OR (o.end_at = o.start_at AND o.start_at >= ?)))
		OR ((o.all_day = 1 OR o.floating = 1) AND o.start_at < ? AND (o.end_at > ? OR (o.end_at = o.start_at AND o.start_at >= ?))))`
	args := []any{to, from, from, wallTo, wallFrom, wallFrom}
	if len(q.CalendarIDs) > 0 {
		where += ` AND o.calendar_id IN (` + inPlaceholders(len(q.CalendarIDs)) + `)`
		args = append(args, toAny(q.CalendarIDs)...)
	}
	if len(q.AccountIDs) > 0 {
		where += ` AND o.account_id IN (` + inPlaceholders(len(q.AccountIDs)) + `)`
		args = append(args, toAny(q.AccountIDs)...)
	}
	if len(q.ExcludeAccountIDs) > 0 {
		where += ` AND o.account_id NOT IN (` + inPlaceholders(len(q.ExcludeAccountIDs)) + `)`
		args = append(args, toAny(q.ExcludeAccountIDs)...)
	}
	if !q.IncludeHidden {
		where += ` AND c.visible = 1`
	}
	if !q.IncludeDeclined {
		where += ` AND e.my_response != 'declined'`
	}
	if len(q.HideNeedsActionAccountIDs) > 0 {
		where += ` AND NOT (e.my_response = 'needsAction' AND o.account_id IN (` + inPlaceholders(len(q.HideNeedsActionAccountIDs)) + `))`
		args = append(args, toAny(q.HideNeedsActionAccountIDs)...)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+occurrenceColumns+` FROM cal_occurrences o
		JOIN cal_calendars c ON c.id = o.calendar_id
		JOIN cal_events e ON e.id = o.event_id`+where+`
		ORDER BY o.start_at, o.all_day DESC, c.account_id, c.position, o.calendar_id, o.event_id, o.occurrence_key
		LIMIT ?`, args...)
	if err != nil {
		return OccurrenceList{}, fmt.Errorf("event range: %w", err)
	}
	var out OccurrenceList
	for rows.Next() {
		o, err := scanOccurrence(rows)
		if err != nil {
			rows.Close()
			return OccurrenceList{}, fmt.Errorf("scan occurrence: %w", err)
		}
		out.Occurrences = append(out.Occurrences, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return OccurrenceList{}, fmt.Errorf("event range: %w", err)
	}
	if len(out.Occurrences) > limit {
		out.Occurrences, out.Truncated = out.Occurrences[:limit], true
	}
	ids := make([]string, 0, len(out.Occurrences))
	seen := map[string]bool{}
	for _, o := range out.Occurrences {
		if !seen[o.EventID] {
			seen[o.EventID] = true
			ids = append(ids, o.EventID)
		}
	}
	if out.Events, err = eventsByIDs(ctx, s.db, ids); err != nil {
		return OccurrenceList{}, err
	}
	return out, nil
}

// GetOccurrence returns one stored occurrence; ErrNotFound when the event
// has no such instance in the window.
func (s *Store) GetOccurrence(ctx context.Context, eventID, occurrenceKey string) (Occurrence, error) {
	o, err := scanOccurrence(s.db.QueryRowContext(ctx, `SELECT `+occurrenceColumns+` FROM cal_occurrences o
		WHERE o.event_id = ? AND o.occurrence_key = ?`, eventID, occurrenceKey))
	if err == sql.ErrNoRows {
		return Occurrence{}, ErrNotFound
	}
	if err != nil {
		return Occurrence{}, fmt.Errorf("get occurrence: %w", err)
	}
	return o, nil
}

// SeriesOccurrence returns the stored instance key of a series (masterID)
// whatever row shows it: the master's own occurrence or an override's.
// ErrNotFound when the series has no such instance in the window.
func (s *Store) SeriesOccurrence(ctx context.Context, masterID, occurrenceKey string) (Occurrence, error) {
	o, err := scanOccurrence(s.db.QueryRowContext(ctx, `SELECT `+occurrenceColumns+` FROM cal_occurrences o
		WHERE (o.series_id = ? OR o.event_id = ?) AND o.occurrence_key = ? ORDER BY o.event_id = ? LIMIT 1`,
		masterID, masterID, occurrenceKey, masterID))
	if err == sql.ErrNoRows {
		return Occurrence{}, ErrNotFound
	}
	if err != nil {
		return Occurrence{}, fmt.Errorf("series occurrence: %w", err)
	}
	return o, nil
}

// RepresentativeOccurrences picks one stored occurrence per event (a
// single, or a master standing for its series with its overrides): the
// next one that has not ended at now, else the last one (event.search).
// Events without a stored occurrence are absent from the map.
func (s *Store) RepresentativeOccurrences(ctx context.Context, eventIDs []string, now time.Time) (map[string]Occurrence, error) {
	out := make(map[string]Occurrence, len(eventIDs))
	n := fmtStamp(now)
	for _, chunk := range chunkStrings(eventIDs, 400) {
		in := inPlaceholders(len(chunk))
		args := append([]any{n, n}, toAny(chunk)...)
		args = append(args, toAny(chunk)...)
		rows, err := s.db.QueryContext(ctx, `SELECT event_id, occurrence_key, series_id, calendar_id, account_id,
				start_at, end_at, all_day, floating, remote_id, root FROM (
			SELECT o.*, CASE WHEN o.series_id != '' THEN o.series_id ELSE o.event_id END AS root,
				ROW_NUMBER() OVER (PARTITION BY CASE WHEN o.series_id != '' THEN o.series_id ELSE o.event_id END
					ORDER BY o.end_at > ? DESC,
					         CASE WHEN o.end_at > ? THEN o.start_at END ASC,
					         o.start_at DESC) AS rn
			  FROM cal_occurrences o
			 WHERE o.series_id IN (`+in+`) OR o.event_id IN (`+in+`))
			WHERE rn = 1`, args...)
		if err != nil {
			return nil, fmt.Errorf("representative occurrences: %w", err)
		}
		for rows.Next() {
			var root string
			var o Occurrence
			var start, end string
			var allDay, floating int
			if err := rows.Scan(&o.EventID, &o.OccurrenceKey, &o.SeriesID, &o.CalendarID, &o.AccountID,
				&start, &end, &allDay, &floating, &o.RemoteID, &root); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan occurrence: %w", err)
			}
			o.StartAt, o.EndAt, o.AllDay, o.Floating = parseStamp(start), parseStamp(end), allDay != 0, floating != 0
			out[root] = o
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("representative occurrences: %w", err)
		}
	}
	return out, nil
}

// wallClock is t's wall clock in loc, as if it were UTC (how all-day and
// floating occurrences are stored).
func wallClock(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}
