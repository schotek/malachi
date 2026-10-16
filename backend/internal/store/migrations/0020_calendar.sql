-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0020_calendar: calendars, events, their occurrences, alerts and the
-- invitations found in stored mail (docs/calendar.md §2; store/calendar*.go).
--
-- Tables and indexes only: no column of an existing table and no trigger on
-- messages, issues, issue_items, meta or drafts, so the board's triggers of
-- 0017/0018 stay the only ones there and nothing here marks board_dirty.
-- cal_invitations references messages(id) and goes with its message row
-- (ON DELETE CASCADE has no trigger of its own on messages).
--
-- Every TEXT time is RFC 3339 UTC with milliseconds (as the strftime
-- defaults), every date YYYY-MM-DD. cal_events has triggers (its search
-- index) and is written only with INSERT ... ON CONFLICT DO UPDATE, UPDATE
-- and DELETE: never INSERT OR REPLACE, which would bypass the delete
-- trigger.
--
-- The search index cal_events_fts is contentless with contentless_delete,
-- like messages_fts (0013), and for the same reason keyed by a rowid of its
-- own: cal_events.id is a TEXT primary key and the implicit rowid of such a
-- table may change on VACUUM, so cal_events_fts_docs maps a stable docid to
-- the event id.
--
-- Never edit this file after commit.

-- One calendar (Graph calendar, CalDAV collection) of an account. visible
-- and user_color are the user's (they survive a resync, matched by
-- remote_id); everything else is the server's or the engine's.
CREATE TABLE cal_calendars (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,
    remote_id      TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    server_color   TEXT NOT NULL DEFAULT '',
    user_color     TEXT NOT NULL DEFAULT '',
    visible        INTEGER NOT NULL DEFAULT 1 CHECK (visible IN (0, 1)),
    access         TEXT NOT NULL DEFAULT 'read' CHECK (access IN ('owner', 'write', 'read')),
    kind           TEXT NOT NULL DEFAULT 'events',
    owner_name     TEXT NOT NULL DEFAULT '',
    owner_address  TEXT NOT NULL DEFAULT '',
    server_default INTEGER NOT NULL DEFAULT 0 CHECK (server_default IN (0, 1)),
    time_zone      TEXT NOT NULL DEFAULT '',
    position       INTEGER NOT NULL DEFAULT 0,
    sync_mode      TEXT NOT NULL DEFAULT '',
    sync_token     TEXT NOT NULL DEFAULT '',
    ctag           TEXT NOT NULL DEFAULT '',
    window_from    TEXT NOT NULL DEFAULT '',
    window_to      TEXT NOT NULL DEFAULT '',
    last_sync      TEXT NOT NULL DEFAULT '',
    error_code     INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX cal_calendars_remote ON cal_calendars (account_id, remote_id);

-- One component as the server has it: a single event, a series master or
-- an override. Every row of one CalDAV resource has its remote_id; raw is
-- kept on the master (or single) row only.
CREATE TABLE cal_events (
    id                 TEXT PRIMARY KEY,
    account_id         TEXT NOT NULL,
    calendar_id        TEXT NOT NULL REFERENCES cal_calendars(id) ON DELETE CASCADE,
    remote_id          TEXT NOT NULL,
    etag               TEXT NOT NULL DEFAULT '',
    change_key         TEXT NOT NULL DEFAULT '',
    uid                TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL CHECK (kind IN ('single', 'master', 'override')),
    master_id          TEXT NOT NULL DEFAULT '',
    recurrence_id      TEXT NOT NULL DEFAULT '',
    sequence           INTEGER NOT NULL DEFAULT 0,
    title              TEXT NOT NULL DEFAULT '',
    location           TEXT NOT NULL DEFAULT '',
    notes              TEXT NOT NULL DEFAULT '',
    links_json         TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(links_json)),
    video_url          TEXT NOT NULL DEFAULT '',
    video_provider     TEXT NOT NULL DEFAULT '',
    start_at           TEXT NOT NULL DEFAULT '',
    end_at             TEXT NOT NULL DEFAULT '',
    start_date         TEXT NOT NULL DEFAULT '',
    end_date           TEXT NOT NULL DEFAULT '',
    all_day            INTEGER NOT NULL DEFAULT 0 CHECK (all_day IN (0, 1)),
    floating           INTEGER NOT NULL DEFAULT 0 CHECK (floating IN (0, 1)),
    time_zone          TEXT NOT NULL DEFAULT '',
    end_time_zone      TEXT NOT NULL DEFAULT '',
    rrule              TEXT NOT NULL DEFAULT '',
    rdates_json        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(rdates_json)),
    exdates_json       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(exdates_json)),
    recurrence_json    TEXT NOT NULL DEFAULT '',
    recur_ok           INTEGER NOT NULL DEFAULT 1 CHECK (recur_ok IN (0, 1)),
    status             TEXT NOT NULL DEFAULT 'confirmed',
    show_as            TEXT NOT NULL DEFAULT 'busy',
    private            INTEGER NOT NULL DEFAULT 0 CHECK (private IN (0, 1)),
    organizer_json     TEXT NOT NULL DEFAULT '',
    i_am_organizer     INTEGER NOT NULL DEFAULT 0 CHECK (i_am_organizer IN (0, 1)),
    my_response        TEXT NOT NULL DEFAULT '',
    response_requested INTEGER NOT NULL DEFAULT 0 CHECK (response_requested IN (0, 1)),
    new_time_ok        INTEGER NOT NULL DEFAULT 0 CHECK (new_time_ok IN (0, 1)),
    attendee_count     INTEGER NOT NULL DEFAULT 0,
    attendees_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attendees_json)),
    alarms_json        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(alarms_json)),
    online_providers   TEXT NOT NULL DEFAULT '',
    raw                BLOB,
    server_updated     TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX cal_events_remote ON cal_events (calendar_id, remote_id, recurrence_id);
CREATE INDEX cal_events_uid    ON cal_events (account_id, uid);
CREATE INDEX cal_events_master ON cal_events (master_id) WHERE master_id != '';

-- Materialised occurrences inside the account's window (a cache). event_id
-- is the row whose fields show; occurrence_key is '' for a single event,
-- else the recurrence id. All-day and floating rows hold the date's or the
-- wall clock's time as if it were UTC.
CREATE TABLE cal_occurrences (
    event_id       TEXT NOT NULL REFERENCES cal_events(id) ON DELETE CASCADE,
    occurrence_key TEXT NOT NULL,
    series_id      TEXT NOT NULL DEFAULT '',
    calendar_id    TEXT NOT NULL,
    account_id     TEXT NOT NULL,
    start_at       TEXT NOT NULL,
    end_at         TEXT NOT NULL,
    all_day        INTEGER NOT NULL DEFAULT 0,
    floating       INTEGER NOT NULL DEFAULT 0,
    remote_id      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (event_id, occurrence_key)
) WITHOUT ROWID;
CREATE INDEX cal_occurrences_range   ON cal_occurrences (calendar_id, start_at);
CREATE INDEX cal_occurrences_account ON cal_occurrences (account_id, start_at);
CREATE INDEX cal_occurrences_series  ON cal_occurrences (series_id, occurrence_key) WHERE series_id != '';

-- CalDAV: every href the daemon knows in a collection, also those it does
-- not store as events, so that a pass does not fetch them again while their
-- ETag holds.
CREATE TABLE cal_resources (
    calendar_id TEXT NOT NULL REFERENCES cal_calendars(id) ON DELETE CASCADE,
    href        TEXT NOT NULL,
    etag        TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL CHECK (state IN ('stored', 'outside', 'skipped')),
    reason      TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (calendar_id, href)
) WITHOUT ROWID;

-- Alerts of occurrences of the next 7 days (derived) with the user's state
-- (authoritative).
CREATE TABLE cal_alerts (
    id             TEXT PRIMARY KEY,
    event_id       TEXT NOT NULL REFERENCES cal_events(id) ON DELETE CASCADE,
    occurrence_key TEXT NOT NULL,
    minutes_before INTEGER NOT NULL,
    due_at         TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'fired', 'dismissed')),
    snoozed_until  TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX cal_alerts_key ON cal_alerts (event_id, occurrence_key, minutes_before);
CREATE INDEX cal_alerts_due ON cal_alerts (due_at) WHERE state = 'pending';

-- A calendar part of a stored message, one row per message row.
CREATE TABLE cal_invitations (
    message_id      TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    account_id      TEXT NOT NULL,
    part_id         TEXT NOT NULL,
    method          TEXT NOT NULL,
    uid             TEXT NOT NULL,
    recurrence_id   TEXT NOT NULL DEFAULT '',
    sequence        INTEGER NOT NULL DEFAULT 0,
    dtstamp         TEXT NOT NULL DEFAULT '',
    organizer       TEXT NOT NULL DEFAULT '',
    start_at        TEXT NOT NULL DEFAULT '',
    end_at          TEXT NOT NULL DEFAULT '',
    all_day         INTEGER NOT NULL DEFAULT 0,
    title           TEXT NOT NULL DEFAULT '',
    card_json       TEXT NOT NULL CHECK (json_valid(card_json)),
    parser_version  INTEGER NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX cal_invitations_uid ON cal_invitations (account_id, uid, recurrence_id);

-- The user's answer to an invitation, shared by every copy of its message.
CREATE TABLE cal_responses (
    account_id    TEXT NOT NULL,
    uid           TEXT NOT NULL,
    recurrence_id TEXT NOT NULL DEFAULT '',
    sequence      INTEGER NOT NULL DEFAULT 0,
    response      TEXT NOT NULL CHECK (response IN ('accepted', 'tentative', 'declined')),
    via           TEXT NOT NULL CHECK (via IN ('graph', 'caldav', 'imip', 'none')),
    calendar_id   TEXT NOT NULL DEFAULT '',
    event_id      TEXT NOT NULL DEFAULT '',
    outbox_id     TEXT NOT NULL DEFAULT '',
    responded_at  TEXT NOT NULL,
    PRIMARY KEY (account_id, uid, recurrence_id)
) WITHOUT ROWID;

-- Event search: title, location, notes, and the organiser's and attendees'
-- names and addresses.
CREATE TABLE cal_events_fts_docs (
    docid    INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE cal_events_fts USING fts5(
    title, location, notes, people,
    content = '',
    contentless_delete = 1,
    prefix = '2 3',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE VIEW cal_event_search_text AS
SELECT e.id AS event_id,
       e.title AS title,
       e.location AS location,
       e.notes AS notes,
       trim(
         CASE WHEN json_valid(e.organizer_json) AND json_type(e.organizer_json) = 'object'
              THEN coalesce(json_extract(e.organizer_json, '$.name'), '') || ' ' ||
                   coalesce(json_extract(e.organizer_json, '$.address'), '')
              ELSE '' END || ' ' ||
         coalesce((SELECT group_concat(coalesce(json_extract(j.value, '$.name'), '') || ' ' ||
                                       coalesce(json_extract(j.value, '$.address'), ''), ' ')
                     FROM json_each(e.attendees_json) j
                    WHERE j.type = 'object'), '')
       ) AS people
  FROM cal_events e;

CREATE TRIGGER cal_events_fts_ai AFTER INSERT ON cal_events BEGIN
    INSERT INTO cal_events_fts_docs (event_id) VALUES (new.id);
    INSERT INTO cal_events_fts (rowid, title, location, notes, people)
        SELECT d.docid, t.title, t.location, t.notes, t.people
          FROM cal_event_search_text t, cal_events_fts_docs d
         WHERE t.event_id = new.id AND d.event_id = new.id;
END;

CREATE TRIGGER cal_events_fts_au
AFTER UPDATE OF title, location, notes, organizer_json, attendees_json ON cal_events
WHEN old.title IS NOT new.title OR old.location IS NOT new.location
  OR old.notes IS NOT new.notes OR old.organizer_json IS NOT new.organizer_json
  OR old.attendees_json IS NOT new.attendees_json
BEGIN
    DELETE FROM cal_events_fts WHERE rowid = (SELECT docid FROM cal_events_fts_docs WHERE event_id = new.id);
    INSERT INTO cal_events_fts (rowid, title, location, notes, people)
        SELECT d.docid, t.title, t.location, t.notes, t.people
          FROM cal_event_search_text t, cal_events_fts_docs d
         WHERE t.event_id = new.id AND d.event_id = new.id;
END;

CREATE TRIGGER cal_events_fts_ad AFTER DELETE ON cal_events BEGIN
    DELETE FROM cal_events_fts WHERE rowid = (SELECT docid FROM cal_events_fts_docs WHERE event_id = old.id);
    DELETE FROM cal_events_fts_docs WHERE event_id = old.id;
END;
