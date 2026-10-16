// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// EventKind is cal_events.kind.
type EventKind string

const (
	EventSingle   EventKind = "single"   // a non-recurring event
	EventMaster   EventKind = "master"   // a series master
	EventOverride EventKind = "override" // a modified instance of a series
)

// Event is a row of cal_events: one component as the server has it.
// Times are UTC (StartAt, EndAt; zero = ""); an all-day event has
// StartDate and EndDate (exclusive) instead; a floating timed event keeps
// its wall clock in StartAt/EndAt as if it were UTC. RecurrenceID is the
// original start of an override in FormatRecurrenceID form. The JSON
// columns are decoded: Links (links_json), RDates/ExDates
// (rdates_json/exdates_json, strings as the caller wrote them), Recurrence
// (recurrence_json, nil = none), Organizer (organizer_json, nil = none),
// Attendees (attendees_json), Alarms (alarms_json, minutes before start).
// RecurRefused is recur_ok = 0 (the rule was refused, only the first
// instance shows). Empty Status and ShowAs are stored as "confirmed" and
// "busy". Raw (the CalDAV resource as fetched) is kept on the master or
// single row only; listings (EventRange) leave it nil.
type Event struct {
	ID                string
	AccountID         string
	CalendarID        string
	RemoteID          string
	ETag              string
	ChangeKey         string
	UID               string
	Kind              EventKind
	MasterID          string
	RecurrenceID      string
	Sequence          int
	Title             string
	Location          string
	Notes             string
	Links             []string
	VideoURL          string
	VideoProvider     string
	StartAt, EndAt    time.Time
	StartDate         string
	EndDate           string
	AllDay            bool
	Floating          bool
	TimeZone          string
	EndTimeZone       string
	RRule             string
	RDates            []string
	ExDates           []string
	Recurrence        *api.Recurrence
	RecurRefused      bool
	Status            api.EventStatus
	ShowAs            api.ShowAs
	Private           bool
	Organizer         *api.Address
	IAmOrganizer      bool
	MyResponse        api.EventResponse
	ResponseRequested bool
	NewTimeOK         bool
	AttendeeCount     int
	Attendees         []api.Attendee
	Alarms            []int
	OnlineProviders   string
	Raw               []byte
	ServerUpdated     time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// eventWriteColumns are written by ReplaceResource, in eventArgs' order.
const eventWriteColumns = `account_id, calendar_id, remote_id, etag, change_key, uid, kind, master_id,
	recurrence_id, sequence, title, location, notes, links_json, video_url, video_provider,
	start_at, end_at, start_date, end_date, all_day, floating, time_zone, end_time_zone, rrule,
	rdates_json, exdates_json, recurrence_json, recur_ok, status, show_as, private, organizer_json,
	i_am_organizer, my_response, response_requested, new_time_ok, attendee_count, attendees_json,
	alarms_json, online_providers, raw, server_updated`

// eventColumns read a row; the raw column is last so that listings can
// replace it (eventColumnsNoRaw).
const eventColumns = `id, ` + eventWriteColumns + `, created_at, updated_at`

var eventColumnsNoRaw = strings.Replace(eventColumns, " raw,", " NULL,", 1)

// eventUpdateSet is "col = ?, …" over eventWriteColumns.
var eventUpdateSet = func() string {
	cols := strings.Split(eventWriteColumns, ",")
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i]) + " = ?"
	}
	return strings.Join(cols, ", ")
}()

func eventArgs(e *Event) ([]any, error) {
	links, err := encodeJSON(e.Links, "[]")
	if err != nil {
		return nil, err
	}
	rdates, err := encodeJSON(e.RDates, "[]")
	if err != nil {
		return nil, err
	}
	exdates, err := encodeJSON(e.ExDates, "[]")
	if err != nil {
		return nil, err
	}
	attendees, err := encodeJSON(e.Attendees, "[]")
	if err != nil {
		return nil, err
	}
	alarms, err := encodeJSON(e.Alarms, "[]")
	if err != nil {
		return nil, err
	}
	recurrence, organizer := "", ""
	if e.Recurrence != nil {
		b, err := json.Marshal(e.Recurrence)
		if err != nil {
			return nil, err
		}
		recurrence = string(b)
	}
	if e.Organizer != nil {
		b, err := json.Marshal(e.Organizer)
		if err != nil {
			return nil, err
		}
		organizer = string(b)
	}
	status, showAs := e.Status, e.ShowAs
	if status == "" {
		status = api.EventStatusConfirmed
	}
	if showAs == "" {
		showAs = api.ShowAsBusy
	}
	var raw any
	if e.Raw != nil {
		raw = e.Raw
	}
	return []any{e.AccountID, e.CalendarID, e.RemoteID, e.ETag, e.ChangeKey, e.UID, string(e.Kind), e.MasterID,
		e.RecurrenceID, e.Sequence, e.Title, e.Location, e.Notes, links, e.VideoURL, e.VideoProvider,
		fmtStamp(e.StartAt), fmtStamp(e.EndAt), e.StartDate, e.EndDate, boolInt(e.AllDay), boolInt(e.Floating),
		e.TimeZone, e.EndTimeZone, e.RRule, rdates, exdates, recurrence, boolInt(!e.RecurRefused),
		string(status), string(showAs), boolInt(e.Private), organizer, boolInt(e.IAmOrganizer),
		string(e.MyResponse), boolInt(e.ResponseRequested), boolInt(e.NewTimeOK), e.AttendeeCount, attendees,
		alarms, e.OnlineProviders, raw, fmtStamp(e.ServerUpdated)}, nil
}

func scanEvent(row scanner) (Event, error) {
	var e Event
	var kind, links, startAt, endAt, rdates, exdates, recurrence, status, showAs, organizer, myResponse string
	var attendees, alarms, serverUpdated, created, updated string
	var allDay, floating, recurOK, private, iAmOrganizer, responseRequested, newTimeOK int
	if err := row.Scan(&e.ID, &e.AccountID, &e.CalendarID, &e.RemoteID, &e.ETag, &e.ChangeKey, &e.UID, &kind, &e.MasterID,
		&e.RecurrenceID, &e.Sequence, &e.Title, &e.Location, &e.Notes, &links, &e.VideoURL, &e.VideoProvider,
		&startAt, &endAt, &e.StartDate, &e.EndDate, &allDay, &floating, &e.TimeZone, &e.EndTimeZone, &e.RRule,
		&rdates, &exdates, &recurrence, &recurOK, &status, &showAs, &private, &organizer,
		&iAmOrganizer, &myResponse, &responseRequested, &newTimeOK, &e.AttendeeCount, &attendees,
		&alarms, &e.OnlineProviders, &e.Raw, &serverUpdated, &created, &updated); err != nil {
		return Event{}, err
	}
	e.Kind, e.Status, e.ShowAs, e.MyResponse = EventKind(kind), api.EventStatus(status), api.ShowAs(showAs), api.EventResponse(myResponse)
	e.StartAt, e.EndAt, e.ServerUpdated = parseStamp(startAt), parseStamp(endAt), parseStamp(serverUpdated)
	e.CreatedAt, e.UpdatedAt = parseStamp(created), parseStamp(updated)
	e.AllDay, e.Floating, e.RecurRefused, e.Private = allDay != 0, floating != 0, recurOK == 0, private != 0
	e.IAmOrganizer, e.ResponseRequested, e.NewTimeOK = iAmOrganizer != 0, responseRequested != 0, newTimeOK != 0
	for _, p := range []struct {
		raw string
		dst any
	}{{links, &e.Links}, {rdates, &e.RDates}, {exdates, &e.ExDates}, {attendees, &e.Attendees}, {alarms, &e.Alarms}} {
		if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
			return Event{}, fmt.Errorf("decode event %s: %w", e.ID, err)
		}
	}
	if recurrence != "" {
		e.Recurrence = new(api.Recurrence)
		if err := json.Unmarshal([]byte(recurrence), e.Recurrence); err != nil {
			return Event{}, fmt.Errorf("decode recurrence of %s: %w", e.ID, err)
		}
	}
	if organizer != "" {
		e.Organizer = new(api.Address)
		if err := json.Unmarshal([]byte(organizer), e.Organizer); err != nil {
			return Event{}, fmt.Errorf("decode organizer of %s: %w", e.ID, err)
		}
	}
	return e, nil
}

// ReplaceResource makes events the stored rows of one remote resource of
// a calendar (a CalDAV href with its master and overrides, or one Graph
// event), in one transaction: each event is matched to a stored row of
// the resource by RecurrenceID and keeps its id (UPDATE), a new one gets
// an "ev_" id (INSERT), and the resource's rows not in events are deleted
// (with their occurrences and alerts). AccountID, CalendarID and RemoteID
// are taken from the calendar and the arguments; an override without a
// MasterID gets the id of the batch's master. Two events with the same
// RecurrenceID are refused. When expand is not nil, the resource's
// occurrences are replaced by what it returns for the stored rows (their
// ids set), in the same transaction; expand must not call the store.
// events empty removes the resource. Returns the stored rows in the order
// given; ErrNotFound for an unknown calendar.
func (s *Store) ReplaceResource(ctx context.Context, calendarID, remoteID string, events []Event, expand func(stored []Event) []Occurrence) ([]Event, error) {
	if remoteID == "" {
		return nil, fmt.Errorf("replace resource: empty remote id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("replace resource: %w", err)
	}
	defer tx.Rollback()
	var accountID string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM cal_calendars WHERE id = ?`, calendarID).Scan(&accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("replace resource: %w", err)
	}
	existing := map[string]string{} // recurrence id → event id
	rows, err := tx.QueryContext(ctx, `SELECT id, recurrence_id FROM cal_events WHERE calendar_id = ? AND remote_id = ?`, calendarID, remoteID)
	if err != nil {
		return nil, fmt.Errorf("replace resource: %w", err)
	}
	for rows.Next() {
		var id, rid string
		if err := rows.Scan(&id, &rid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("replace resource: %w", err)
		}
		existing[rid] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("replace resource: %w", err)
	}

	out := make([]Event, len(events))
	seen := map[string]bool{}
	masterID := ""
	for i := range events {
		e := events[i]
		switch e.Kind {
		case EventSingle, EventMaster, EventOverride:
		default:
			return nil, fmt.Errorf("replace resource: event kind %q", e.Kind)
		}
		if seen[e.RecurrenceID] {
			return nil, fmt.Errorf("replace resource: recurrence id %q twice", e.RecurrenceID)
		}
		seen[e.RecurrenceID] = true
		e.AccountID, e.CalendarID, e.RemoteID = accountID, calendarID, remoteID
		if id, ok := existing[e.RecurrenceID]; ok {
			e.ID = id
		} else {
			e.ID = newID("ev_")
		}
		if e.Kind == EventMaster {
			masterID = e.ID
		}
		out[i] = e
	}
	for i := range out {
		if out[i].Kind == EventOverride && out[i].MasterID == "" {
			out[i].MasterID = masterID
		}
	}
	keep := map[string]bool{}
	for i := range out {
		e := &out[i]
		args, err := eventArgs(e)
		if err != nil {
			return nil, fmt.Errorf("encode event: %w", err)
		}
		now := nowStamp()
		if _, ok := existing[e.RecurrenceID]; ok {
			if _, err := tx.ExecContext(ctx, `UPDATE cal_events SET `+eventUpdateSet+`, updated_at = ? WHERE id = ?`,
				append(args, now, e.ID)...); err != nil {
				return nil, fmt.Errorf("update event: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `INSERT INTO cal_events (id, `+eventWriteColumns+`, created_at, updated_at)
				VALUES (?, `+inPlaceholders(len(args))+`, ?, ?)`, append(append([]any{e.ID}, args...), now, now)...); err != nil {
				return nil, fmt.Errorf("insert event: %w", err)
			}
		}
		keep[e.ID] = true
		stored, err := scanEvent(tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM cal_events WHERE id = ?`, e.ID))
		if err != nil {
			return nil, fmt.Errorf("read event: %w", err)
		}
		*e = stored
	}
	for _, id := range existing {
		if !keep[id] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM cal_events WHERE id = ?`, id); err != nil {
				return nil, fmt.Errorf("delete event: %w", err)
			}
		}
	}
	if expand != nil && len(out) > 0 {
		ids := make([]string, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		if err := replaceOccurrencesTx(ctx, tx, ids, expand(out)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("replace resource: %w", err)
	}
	return out, nil
}

// RemoveResource deletes the rows of one remote resource of a calendar
// (with their occurrences and alerts) and says how many there were.
func (s *Store) RemoveResource(ctx context.Context, calendarID, remoteID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cal_events WHERE calendar_id = ? AND remote_id = ?`, calendarID, remoteID)
	if err != nil {
		return 0, fmt.Errorf("remove resource: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetEvent returns one event row with Raw; ErrNotFound for an unknown id.
func (s *Store) GetEvent(ctx context.Context, id string) (Event, error) {
	e, err := scanEvent(s.db.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM cal_events WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	return e, nil
}

// EventsByUID returns the rows of an iCalendar UID in an account (every
// calendar; master or single first, then overrides by recurrence id), with
// Raw.
func (s *Store) EventsByUID(ctx context.Context, accountID, uid string) ([]Event, error) {
	return s.queryEvents(ctx, eventColumns, `account_id = ? AND uid = ?`, accountID, uid)
}

// EventsByRemoteID returns the rows of one remote resource of a calendar
// (master or single first, then overrides by recurrence id), with Raw.
func (s *Store) EventsByRemoteID(ctx context.Context, calendarID, remoteID string) ([]Event, error) {
	return s.queryEvents(ctx, eventColumns, `calendar_id = ? AND remote_id = ?`, calendarID, remoteID)
}

// SeriesOverrides returns the overrides of a master, by recurrence id,
// without Raw.
func (s *Store) SeriesOverrides(ctx context.Context, masterID string) ([]Event, error) {
	return s.queryEvents(ctx, eventColumnsNoRaw, `master_id = ? AND kind = 'override'`, masterID)
}

// CalendarEvents returns every row of a calendar without Raw, by remote id
// and recurrence id (a rebuild of its occurrences, MetaCalendarExpand).
func (s *Store) CalendarEvents(ctx context.Context, calendarID string) ([]Event, error) {
	return s.queryEvents(ctx, eventColumnsNoRaw, `calendar_id = ?`, calendarID)
}

func (s *Store) queryEvents(ctx context.Context, cols, where string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM cal_events WHERE `+where+
		` ORDER BY calendar_id, remote_id, kind = 'override', recurrence_id, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return out, nil
}

// eventsByIDs returns the rows of ids without Raw, by id.
func eventsByIDs(ctx context.Context, q querier, ids []string) (map[string]Event, error) {
	out := make(map[string]Event, len(ids))
	for _, chunk := range chunkStrings(ids, 500) {
		rows, err := q.QueryContext(ctx, `SELECT `+eventColumnsNoRaw+` FROM cal_events WHERE id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan event: %w", err)
			}
			out[e.ID] = e
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
	}
	return out, nil
}

// RemovedResource names a remote resource whose rows TrimEvents deleted
// entirely.
type RemovedResource struct {
	CalendarID string
	RemoteID   string
}

// TrimEvents deletes an account's events that ended before before (the
// window's start, docs/calendar.md §2.3): singles and overrides whose end
// (the end date of an all-day one, exclusive) is not after it, and masters
// that started before it and have no occurrence ending after it. Call it
// after the occurrences are up to date: a master's stored occurrences are
// what keeps it. Returns the resources none of whose rows are left.
func (s *Store) TrimEvents(ctx context.Context, accountID string, before time.Time) ([]RemovedResource, error) {
	stamp, date := fmtStamp(before), before.UTC().Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("trim events: %w", err)
	}
	defer tx.Rollback()
	const ended = `((e.all_day = 0 AND max(e.start_at, e.end_at) <= ?) OR (e.all_day = 1 AND max(e.start_date, e.end_date) <= ?))`
	rows, err := tx.QueryContext(ctx, `SELECT e.id, e.calendar_id, e.remote_id FROM cal_events e
		WHERE e.account_id = ? AND (
			(e.kind != 'master' AND `+ended+`)
			OR (e.kind = 'master' AND ((e.all_day = 0 AND e.start_at < ?) OR (e.all_day = 1 AND e.start_date < ?))
				AND NOT EXISTS (SELECT 1 FROM cal_occurrences o WHERE o.series_id = e.id AND o.end_at > ?)
				AND NOT EXISTS (SELECT 1 FROM cal_occurrences o WHERE o.event_id = e.id AND o.end_at > ?)))`,
		accountID, stamp, date, stamp, date, stamp, stamp)
	if err != nil {
		return nil, fmt.Errorf("trim events: %w", err)
	}
	var ids []string
	touched := map[RemovedResource]bool{}
	for rows.Next() {
		var id string
		var r RemovedResource
		if err := rows.Scan(&id, &r.CalendarID, &r.RemoteID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("trim events: %w", err)
		}
		ids = append(ids, id)
		touched[r] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trim events: %w", err)
	}
	for _, chunk := range chunkStrings(ids, 500) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cal_events WHERE id IN (`+inPlaceholders(len(chunk))+`)`, toAny(chunk)...); err != nil {
			return nil, fmt.Errorf("trim events: %w", err)
		}
	}
	var gone []RemovedResource
	for r := range touched {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cal_events WHERE calendar_id = ? AND remote_id = ?`, r.CalendarID, r.RemoteID).Scan(&n); err != nil {
			return nil, fmt.Errorf("trim events: %w", err)
		}
		if n == 0 {
			gone = append(gone, r)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("trim events: %w", err)
	}
	return gone, nil
}
