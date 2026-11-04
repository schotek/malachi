// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Calendars of migration 0020 (docs/calendar.md §2). The store knows rows
// only: it imports neither internal/calendar nor internal/calsync, and
// every string it is given has been cleaned by the caller. The cal_*
// tables carry no trigger on the mail tables, so nothing here touches the
// board (board_dirty).

// Meta keys of the calendar (docs/calendar.md §2.4).
const (
	// MetaCalendarPrefs holds the JSON api.CalendarPreferences.
	MetaCalendarPrefs = "calendar.prefs"
	// MetaCalendarInvitesScan is the invitation backfill cursor:
	// "<parser version>:<last message rowid>" or "<v>:done".
	MetaCalendarInvitesScan = "calendar.invites.scan"
	// MetaCalendarExpand is "<recur version>:<tzdata version>" of the
	// stored daemon-side expansions.
	MetaCalendarExpand = "calendar.expand"
)

// Calendar is a row of cal_calendars. Visible and UserColor are the
// user's; the rest is the server's (Name … Position) or the engine's
// (SyncMode … ErrorCode).
type Calendar struct {
	ID            string
	AccountID     string
	RemoteID      string // Graph calendar id or CalDAV collection href
	Name          string
	ServerColor   string // "#rrggbb" or ""
	UserColor     string // "#rrggbb" or ""
	Visible       bool
	Access        api.CalendarAccess
	Kind          api.CalendarKind
	OwnerName     string
	OwnerAddress  string
	ServerDefault bool
	TimeZone      string
	Position      int
	CalendarSyncState
	CreatedAt, UpdatedAt time.Time
}

// CalendarSyncState is the engine's state of one calendar: how it syncs,
// the token (Graph deltaLink, CalDAV sync-token) and ctag, the window the
// token was made for, the last pass and the error code of the last failed
// one (0 = none).
type CalendarSyncState struct {
	SyncMode   string
	SyncToken  string
	CTag       string
	WindowFrom string // Date
	WindowTo   string // Date
	LastSync   time.Time
	ErrorCode  int
}

const calendarColumns = `id, account_id, remote_id, name, server_color, user_color, visible, access, kind,
	owner_name, owner_address, server_default, time_zone, position, sync_mode, sync_token, ctag,
	window_from, window_to, last_sync, error_code, created_at, updated_at`

func scanCalendar(row scanner) (Calendar, error) {
	var c Calendar
	var visible, def int
	var access, kind, lastSync, created, updated string
	if err := row.Scan(&c.ID, &c.AccountID, &c.RemoteID, &c.Name, &c.ServerColor, &c.UserColor, &visible, &access, &kind,
		&c.OwnerName, &c.OwnerAddress, &def, &c.TimeZone, &c.Position, &c.SyncMode, &c.SyncToken, &c.CTag,
		&c.WindowFrom, &c.WindowTo, &lastSync, &c.ErrorCode, &created, &updated); err != nil {
		return Calendar{}, err
	}
	c.Visible, c.ServerDefault = visible != 0, def != 0
	c.Access, c.Kind = api.CalendarAccess(access), api.CalendarKind(kind)
	c.LastSync, c.CreatedAt, c.UpdatedAt = parseStamp(lastSync), parseStamp(created), parseStamp(updated)
	return c, nil
}

// UpsertCalendars writes the server's view of an account's calendars,
// matched by RemoteID: a known calendar keeps its id, its user columns
// (Visible, UserColor) and its sync state, and takes the server fields
// (Name, ServerColor, Access, Kind, OwnerName, OwnerAddress, ServerDefault,
// TimeZone, Position); a new one gets a "cal_" id, Visible true and an
// empty sync state. Calendars not in cals are left alone (DeleteCalendar).
// Returns the stored rows in the order given. One transaction.
func (s *Store) UpsertCalendars(ctx context.Context, accountID string, cals []Calendar) ([]Calendar, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("upsert calendars: %w", err)
	}
	defer tx.Rollback()
	out := make([]Calendar, 0, len(cals))
	for _, c := range cals {
		if c.RemoteID == "" {
			return nil, fmt.Errorf("upsert calendars: empty remote id")
		}
		access := c.Access
		if access == "" {
			access = api.CalendarAccessRead
		}
		kind := c.Kind
		if kind == "" {
			kind = api.CalendarKindEvents
		}
		now := nowStamp()
		if _, err := tx.ExecContext(ctx, `INSERT INTO cal_calendars (id, account_id, remote_id, name, server_color,
				access, kind, owner_name, owner_address, server_default, time_zone, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, remote_id) DO UPDATE SET
				name = excluded.name, server_color = excluded.server_color, access = excluded.access,
				kind = excluded.kind, owner_name = excluded.owner_name, owner_address = excluded.owner_address,
				server_default = excluded.server_default, time_zone = excluded.time_zone,
				position = excluded.position, updated_at = excluded.updated_at`,
			newID("cal_"), accountID, c.RemoteID, c.Name, c.ServerColor, string(access), string(kind),
			c.OwnerName, c.OwnerAddress, boolInt(c.ServerDefault), c.TimeZone, c.Position, now, now); err != nil {
			return nil, fmt.Errorf("upsert calendar: %w", err)
		}
		stored, err := scanCalendar(tx.QueryRowContext(ctx, `SELECT `+calendarColumns+` FROM cal_calendars
			WHERE account_id = ? AND remote_id = ?`, accountID, c.RemoteID))
		if err != nil {
			return nil, fmt.Errorf("read calendar: %w", err)
		}
		out = append(out, stored)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("upsert calendars: %w", err)
	}
	return out, nil
}

// ListCalendars returns the calendars of an account ("" = every account),
// by account and server order (Position, then id).
func (s *Store) ListCalendars(ctx context.Context, accountID string) ([]Calendar, error) {
	q := `SELECT ` + calendarColumns + ` FROM cal_calendars`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY account_id, position, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, fmt.Errorf("scan calendar: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}
	return out, nil
}

// GetCalendar returns one calendar; ErrNotFound for an unknown id.
func (s *Store) GetCalendar(ctx context.Context, id string) (Calendar, error) {
	c, err := scanCalendar(s.db.QueryRowContext(ctx, `SELECT `+calendarColumns+` FROM cal_calendars WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Calendar{}, ErrNotFound
	}
	if err != nil {
		return Calendar{}, fmt.Errorf("get calendar: %w", err)
	}
	return c, nil
}

// UpdateCalendarUser changes the user's columns of a calendar
// (calendar.update): nil leaves a field as it is, userColor "" clears the
// override. Returns the stored row; ErrNotFound for an unknown id.
func (s *Store) UpdateCalendarUser(ctx context.Context, id string, visible *bool, userColor *string) (Calendar, error) {
	set, args := `updated_at = ?`, []any{nowStamp()}
	if visible != nil {
		set += `, visible = ?`
		args = append(args, boolInt(*visible))
	}
	if userColor != nil {
		set += `, user_color = ?`
		args = append(args, *userColor)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE cal_calendars SET `+set+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return Calendar{}, fmt.Errorf("update calendar: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Calendar{}, ErrNotFound
	}
	return s.GetCalendar(ctx, id)
}

// SetCalendarSyncState stores the engine's state of a calendar after a
// pass. ErrNotFound for an unknown id.
func (s *Store) SetCalendarSyncState(ctx context.Context, id string, st CalendarSyncState) error {
	res, err := s.db.ExecContext(ctx, `UPDATE cal_calendars SET sync_mode = ?, sync_token = ?, ctag = ?,
			window_from = ?, window_to = ?, last_sync = ?, error_code = ?, updated_at = ?
		WHERE id = ?`, st.SyncMode, st.SyncToken, st.CTag, st.WindowFrom, st.WindowTo, fmtStamp(st.LastSync),
		st.ErrorCode, nowStamp(), id)
	if err != nil {
		return fmt.Errorf("set calendar sync state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCalendar removes a calendar with its events, occurrences,
// resources and alerts (ON DELETE CASCADE; the search index follows its
// events). Deleting an unknown id is not an error.
func (s *Store) DeleteCalendar(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cal_calendars WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete calendar: %w", err)
	}
	return nil
}

// DeleteCalendarContents removes a calendar's events (with their
// occurrences, alerts and search rows), its occurrences and its known
// resources, and keeps the calendar row with its user columns (a calendar
// the account disabled, D23). One transaction; an unknown id is not an
// error.
func (s *Store) DeleteCalendarContents(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete calendar contents: %w", err)
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM cal_events WHERE calendar_id = ?`,
		`DELETE FROM cal_occurrences WHERE calendar_id = ?`,
		`DELETE FROM cal_resources WHERE calendar_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete calendar contents: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteAccountCalendars removes every cached calendar row of an account
// (D23: calendar sync switched off, the server or user changed). The
// user's answers to invitations (cal_responses) and the invitations of its
// mail stay; DeleteAccount removes those too.
func (s *Store) DeleteAccountCalendars(ctx context.Context, accountID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete account calendars: %w", err)
	}
	defer tx.Rollback()
	if err := deleteCalendarCacheTx(ctx, tx, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteCalendarCacheTx removes an account's calendars and, should any be
// left without one, its events (which take occurrences and alerts along).
func deleteCalendarCacheTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	for _, q := range []string{
		`DELETE FROM cal_calendars WHERE account_id = ?`,
		`DELETE FROM cal_events WHERE account_id = ?`,
		`DELETE FROM cal_occurrences WHERE account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, accountID); err != nil {
			return fmt.Errorf("delete account calendars: %w", err)
		}
	}
	return nil
}

// deleteCalendarAccountTx removes every cal_* row of an account
// (DeleteAccount).
func deleteCalendarAccountTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	if err := deleteCalendarCacheTx(ctx, tx, accountID); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM cal_invitations WHERE account_id = ?`,
		`DELETE FROM cal_responses WHERE account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, accountID); err != nil {
			return fmt.Errorf("delete account invitations: %w", err)
		}
	}
	return nil
}

// CalendarPreferences reads meta calendar.prefs; a store that never had
// any set gives api.DefaultCalendarPreferences. A stored value is decoded
// over the defaults, so a field added later keeps its default.
func (s *Store) CalendarPreferences(ctx context.Context) (api.CalendarPreferences, error) {
	p := api.DefaultCalendarPreferences()
	v, ok, err := s.GetMeta(ctx, MetaCalendarPrefs)
	if err != nil || !ok {
		return p, err
	}
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return api.DefaultCalendarPreferences(), fmt.Errorf("decode calendar preferences: %w", err)
	}
	return p, nil
}

// SetCalendarPreferences stores p (validated by the caller) in meta
// calendar.prefs.
func (s *Store) SetCalendarPreferences(ctx context.Context, p api.CalendarPreferences) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode calendar preferences: %w", err)
	}
	return s.SetMeta(ctx, MetaCalendarPrefs, string(b))
}

// FormatCalendarTime formats an instant the way every cal_* time column
// holds it (UTC, milliseconds; "" for the zero time). Recurrence ids and
// occurrence keys of timed instances use it, so that equal instants are
// equal strings.
func FormatCalendarTime(t time.Time) string { return fmtStamp(t) }

// FormatRecurrenceID is the stored form of a recurrence id or occurrence
// key: the instance's original start as FormatCalendarTime, or for an
// all-day instance its date (YYYY-MM-DD, of t's own wall clock).
func FormatRecurrenceID(t time.Time, allDay bool) string {
	if allDay {
		return t.Format("2006-01-02")
	}
	return fmtStamp(t)
}

func fmtStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// ResourceState is cal_resources.state.
type ResourceState string

const (
	ResourceStored  ResourceState = "stored"  // its events are in cal_events
	ResourceOutside ResourceState = "outside" // every occurrence lies outside the window
	ResourceSkipped ResourceState = "skipped" // not stored: Reason says why
)

// Resource is a row of cal_resources: an href of a CalDAV collection the
// daemon knows, stored or not, with the ETag it had. Reason (skipped
// only): tooBig, malformed, noEvent or limit.
type Resource struct {
	CalendarID string
	Href       string
	ETag       string
	State      ResourceState
	Reason     string
	UpdatedAt  time.Time
}

// ListResources returns the known hrefs of a calendar, by href.
func (s *Store) ListResources(ctx context.Context, calendarID string) ([]Resource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT calendar_id, href, etag, state, reason, updated_at
		FROM cal_resources WHERE calendar_id = ? ORDER BY href`, calendarID)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()
	var out []Resource
	for rows.Next() {
		var r Resource
		var state, updated string
		if err := rows.Scan(&r.CalendarID, &r.Href, &r.ETag, &state, &r.Reason, &updated); err != nil {
			return nil, fmt.Errorf("scan resource: %w", err)
		}
		r.State, r.UpdatedAt = ResourceState(state), parseStamp(updated)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	return out, nil
}

// PutResources writes hrefs of a calendar (each replacing its earlier
// row), in one transaction; CalendarID of the rows is ignored.
func (s *Store) PutResources(ctx context.Context, calendarID string, res []Resource) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put resources: %w", err)
	}
	defer tx.Rollback()
	now := nowStamp()
	for _, r := range res {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cal_resources (calendar_id, href, etag, state, reason, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (calendar_id, href) DO UPDATE SET etag = excluded.etag, state = excluded.state,
				reason = excluded.reason, updated_at = excluded.updated_at`,
			calendarID, r.Href, r.ETag, string(r.State), r.Reason, now); err != nil {
			return fmt.Errorf("put resource: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteResources forgets hrefs of a calendar (gone from the server). The
// events of a stored one go with RemoveResource.
func (s *Store) DeleteResources(ctx context.Context, calendarID string, hrefs []string) error {
	for _, chunk := range chunkStrings(hrefs, 500) {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM cal_resources WHERE calendar_id = ? AND href IN (`+
			inPlaceholders(len(chunk))+`)`, append([]any{calendarID}, toAny(chunk)...)...); err != nil {
			return fmt.Errorf("delete resources: %w", err)
		}
	}
	return nil
}
