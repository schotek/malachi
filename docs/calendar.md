# Calendar

The design and work breakdown of calendar support in Malachi Mail: what is
built, how the daemon stores and synchronises calendars, the API contract
the clients and the MCP bridge use, the macOS client (the reference client
of this feature), and the tasks that implementation agents take one by one.

Status (2026-10-17): branch `feat/calendar`. The daemon (T0–T12, U1/U2,
P1, `malachi-rpc`) and the macOS client (M1–M11) are done and committed
(0b414da, 7315d7c); the owner passed gate 1 and gate 2 (§6.3) over his
real Microsoft 365 and Nextcloud accounts (D29–D32). The GTK client
(G1–G19, §6.5, as built in §5.12) is written, built and tested in the
Fedora container on the Mac (`scripts/gtk-container/gtk.sh`; full
`test-ui` green, every calendar smoke green, every view and window
looked at under Broadway) and waits for the owner's run in the Toolbx
against his accounts, then its commit. The Windows port is the owner's
on his Windows machine (§6.4); nothing under `windows/` was touched.

Contents:

1. Scope and decisions
2. Data model (migration 0020)
3. Daemon architecture
4. The API contract
5. The macOS client
6. Work breakdown
7. Open questions for the owner

The contract in §4 is written to be frozen: once task T1 (§6) lands it in
`backend/pkg/api` and `docs/api.md`, later tasks implement it and change it
only by an explicit decision recorded here.

---

## 1. Scope and decisions

### 1.1 Product decisions (made by the owner, not reopened)

- **Sources.** Microsoft 365 / Outlook.com through Graph: the calendars of
  an existing `kind: graph` mail account. CalDAV as a new, calendar-only
  account kind `caldav` (non-mail, like `jira`; kind capsule `CALDAV` in
  the sidebar; never shown in Mail). Google Calendar comes later, under
  the Gmail account: the abstraction allows a Google provider without a
  contract change, nothing Google is implemented now.
- **Third mode of the main window**: Mail ⌘1, Board ⌘2, Calendar ⌘3,
  sharing the sidebar slot, the status bar, the assistant and the
  accounts. Views Day, Week, Month, Agenda; Today, ‹ ›, event search in the
  toolbar, "+" New Event (⌥⌘N; ⌘N stays New Message everywhere), ✦.
- **Event editor** is a separate window like New Message: attendees in the
  recipient token field with `contact.search` suggestions, availability
  from free/busy (Graph only), alert popup, plain-text notes, a footer that
  names the account invitations leave from, Save / Save and Send
  Invitations.
- **Event detail** is a popover anchored to the event: calendar and
  account, title, date/time/duration, recurrence, location, video-call
  link (through "Open this link?"), organiser, attendees with their
  responses, the own response as a popup (changing it sends the reply),
  notes as plain text, Show Invitation in Mail, Edit, Remove from Calendar
  (own events: Delete), ✦.
- **Invitations in Mail**: any mail account receiving a message with a
  `text/calendar` part gets a list pill (Invitation / Update / Cancelled /
  Reply) and an invitation card above the message header, rendered from
  the parsed part, never from HTML: date tile, title, time, location,
  organiser, attendee count, conflict line, mini day strip, "Add to:"
  target calendar, Accept / Tentative / Decline, Propose New Time… where
  the source supports it, and after answering a state strip with Change
  Response and Show in Calendar. Unanswered invitations may be drawn
  tentatively (dashed) in the calendar (per-account preference).
- **Responses and outgoing invitations** go through the provider API where
  there is one (Graph accept / tentativelyAccept / decline; creating an
  event with attendees sends the invitations). For CalDAV and for
  invitations received on an IMAP account the daemon builds iMIP mail and
  queues it through the existing outbox (REPLY to the organiser, REQUEST
  to attendees, CANCEL).
- **Preferences**: Accounts → Calendar group (sync switch, calendars with
  checkbox and server colour, default calendar, "Keep events" window),
  Invitations group (card switch, tentative switch, after answering keep or
  archive); Add CalDAV Calendar… sheet with discovery and the existing
  certificate-trust flow, no generic "ignore certificate"; IMAP accounts
  show "This account has no calendar" with a link. General → Calendar: week
  starts on, default duration, default alert, show declined events,
  working hours.
  (Replaced by the owner on 2026-10-16: these settings are a Preferences
  tab Calendar instead, §5.8.)
- **Small integrations**: Board → Today strip "Today in Calendar"; the
  assistant panel's quick actions What's on Today and Find a Free Slot; MCP
  tools `list_events` and `find_free_time` read-only, `create_event` behind
  `--allow-modify`; macOS notifications for alerts (Join, Snooze), new
  invitations (Accept, Show) and cancellations.
- **Order of work**: daemon, then macOS with the pure UI logic and every
  user-visible string referenced in a Go package `ui/internal/calendar`;
  GTK and Windows only after the owner approves macOS (this document notes
  what must be portable, it does not plan their code).
- **Sample data**: `MALACHI_CALENDAR_SAMPLES=1` shows invented calendars and
  events without a daemon, for layout checks.

### 1.2 Technical decisions

Each with its reason; the alternatives considered are in §3 where they
matter.

| # | Decision | Reason |
|---|---|---|
| D1 | Own iCalendar parser and encoder (`internal/calendar/ics`), no `emersion/go-ical`. | go-ical (MIT) is small and correct, but strict where real invitations are not (bare LF, missing `END`, Outlook quirks), has no limits on line length, components or properties, and returns whole-object errors; every parser of hostile input in this repo has its own limits and fuzz targets. The content-line grammar is small (≈ 600 lines with encoding). |
| D2 | RRULE expansion through `github.com/teambition/rrule-go` (MIT), wrapped in `internal/calendar/recur`, which parses and validates the rule itself and hands rrule-go only a validated `ROption` with `UNTIL` clamped to the query window. | Writing RFC 5545 BYxxx semantics ourselves is the largest correctness risk of the feature; rrule-go is a port of python-dateutil and has years of use. Its weak spots (no step budget, `SECONDLY`/`MINUTELY` with impossible filters loop for practically ever) are closed by validation before it runs (§3.3). A `recover()` boundary turns a panic into "recurrence unsupported". |
| D3 | Own CalDAV client (`internal/caldav`) over `net/http` + `encoding/xml`, no `emersion/go-webdav`. | We need a subset (PROPFIND depth 0/1, REPORT `calendar-multiget`, `calendar-query` with `time-range`, `sync-collection`, PUT with `If-Match`/`If-None-Match`, DELETE, OPTIONS), and we need control go-webdav does not give: a byte cap on every response, XML depth caps, redirects only to the same origin and never with credentials to another host (the Jira client's rule), TLS pinning through `transport.EndpointTLSConfig`, and error classification into contract codes. go-webdav is a 0.x API that changes between minors. It stays a reference for interoperability, not a dependency. |
| D4 | Time zones: IANA tzdata of Go, embedded with `import _ "time/tzdata"` in `cmd/malachid` (platform-neutral: the same import everywhere; Windows has no system zoneinfo). Windows zone names from Graph and Outlook `.ics` are mapped through a table generated from CLDR `windowsZones.xml` (Unicode licence v3, permissive). A `TZID` is resolved: IANA name → Windows name → known prefixes (`/mozilla.org/…/Europe/Prague`, `/softwarestudio.org/…`) → the zone whose offsets match the object's `VTIMEZONE` at the event's dates → a fixed offset from the `VTIMEZONE` (with a problem logged). We never interpret `VTIMEZONE` rules as the source of truth. When writing, `TZID` is always an IANA name with a `VTIMEZONE` generated from Go's zone (transitions found with `time.Time.ZoneBounds`) for the years the event spans, capped at 10. | Every server we target (Exchange, Google, iCloud, Nextcloud, Fastmail, Radicale) accepts IANA `TZID`s; interpreting hostile `VTIMEZONE` rules would be a second RRULE engine. |
| D5 | Graph expands recurrences itself (`calendarView`); CalDAV objects are expanded by the daemon. Both end in one table of materialised occurrences (`cal_occurrences`) that `event.list` reads. | One query path for every source; Exchange's own expansion is authoritative for Exchange's quirks (Windows zones, modified occurrences). |
| D6 | Calendar writes are **server-first** (like `issue.transition`), not local-first through an operation log: `event.create/update/delete/respond` go to the server and answer when it answered (≤ 30 s), offline is an error. iMIP mail, which the outbox already delivers offline, is the exception: it is queued. | A calendar edit with attendees is a message to other people; replaying it later against a changed event is worse than refusing it now. No new op-log kind, no offline conflict resolution. |
| D7 | Conflicts: the server wins. Every write carries the version we read (`If-Match` with the CalDAV ETag or the Graph `@odata.etag`); 412 is `conflict` with the current event in `error.data`. A sync pass replaces local rows with what the server has. | Same rule as drafts' optimistic concurrency; nothing local is authoritative except the user's own columns (§2.2). |
| D8 | CalDAV scheduling: when the server advertises `calendar-auto-schedule` (RFC 6638; iCloud, Fastmail, Nextcloud) the server sends invitations and replies and the daemon sends no iMIP; otherwise the daemon sends iMIP through a mail account of the user and marks the attendees `SCHEDULE-AGENT=CLIENT`. Per-account override `scheduling: auto|server|client`. | Sending iMIP to attendees of a server that schedules itself would deliver every invitation twice. The owner's "iMIP for CalDAV" applies where the server does not schedule. |
| D9 | iMIP is built by the daemon (`internal/calendar/imip`) and sent as `multipart/alternative[text/plain, text/calendar; method=X]` through `smtp.BuildMessage` (a new `Calendar` part in `BuildInput`) and `store.EnqueueOutbox`, from the mail account that received the invitation (a reply) or the CalDAV account's `mailAccountId` (invitations). The subject and the text are supplied by the client (localised, from `ui/internal/calendar`); the daemon only falls back to the event's title and an empty text (MCP). | Backend is language-neutral (no gettext in `backend/`); the client already supplies localised text the same way for a reply's attribution. |
| D10 | An iMIP REPLY goes to the `ORGANIZER` of the stored invitation, never to the mail's `From` or `Reply-To`. A received REPLY updates an attendee of a stored event only when the mail's `From` address equals that `ATTENDEE`'s address and the event exists with that attendee; it never adds attendees. | Hostile input: an invitation can name any organiser; the card shows the address the reply goes to, and warns when the organiser's domain differs from the sender's. |
| D11 | Event notes reach the UI as plain text only (`notes`), with the https links found in them listed separately (`links`). Graph HTML bodies are converted to text by the existing sanitiser (`sanitize` → text) inside the daemon; iCalendar `DESCRIPTION` is plain text by definition, `X-ALT-DESC` (HTML) is ignored. | Owner decision (notes as plain text) and rule 2: no HTML of an event ever crosses the API. |
| D12 | Reminders are scheduled by the daemon: it materialises alerts of the next 7 days (`cal_alerts`), keeps one timer for the next due one and sends `notify.eventAlert`; snooze and dismiss are daemon state (one notification per alert across clients). Only `DISPLAY` and `AUDIO` alarms count; `EMAIL`, `PROCEDURE` and any `ATTACH` of an alarm are ignored. Graph's `reminderMinutesBeforeStart` is the alarm of a Graph event; snooze/dismiss are local (not written to Outlook) in this iteration. | One scheduler for three clients; an alarm must never send mail or run anything. |
| D13 | Graph: one request limiter per account shared by the mail syncer, the outbox and the calendar (4 concurrent, Microsoft's per-mailbox limit), a new `graph.Options.Limiter`. Calendar passes have lower priority than the inbox check. | Today syncer and outbox each have 4 slots; a third client would exceed the mailbox limit and slow mail with 429s. |
| D14 | Graph sync: `GET /me/calendars/{id}/calendarView/delta` per calendar with a fixed window; a calendar whose delta is refused (400/404/501) falls back for good to a paged `calendarView` listing compared by `changeKey` (`delta_mode = list`). The default calendar uses the documented `/me/calendarView/delta`. Masters (`seriesMaster`) are fetched separately (`GET /me/events/{id}`) when a new `seriesMasterId` appears or its `changeKey` changes. | Per-calendar delta is documented only in beta (feasibility study); the fallback keeps every calendar working whatever Microsoft does. |
| D15 | Microsoft scopes: `Calendars.ReadWrite` and `Calendars.Read.Shared` are requested only by a sign-in made for the calendar (`account.oauthStart` `calendar: true`, which the Calendar switch starts). The grant records the `scope` of the token response in the keyring item `oauth2.scope` (only while it grants the calendar; no item = mail only; `oauth2.refresh_token` stays the bare token); a Graph account whose grant lacks the calendar scope, or whose calendar call answers 403 `ErrorAccessDenied`, has calendar state `consentRequired` and its mail keeps running. GOA accounts: `CalendarDisabled` of the GOA account is read like `MailDisabled`; GOA `ms_graph` ≥ 3.52 tokens already carry the scopes. | Users who do not want the calendar never see a new consent screen; organisations that restrict consent keep mail. Existing refresh tokens keep working (golang.org/x/oauth2 sends no `scope` on refresh). |
| D16 | Account capability `calendar` (new `AccountCapability`): the account can have calendars (graph, caldav; later imap with Google). A `caldav` account's capabilities are exactly `["calendar"]`. Clients show in Mail only accounts of the kinds `imap`, `graph`, `jira` (unknown kinds hidden). | A capability that does not flicker with consent or sync state; the Calendar group of Preferences keys on it. |
| D17 | Invitation detection happens in the `Stored` hook (`afterMailStored`, synchronous, before `notify.newMessage`): the daemon reads the message's `text/calendar` / `application/ics` parts (≤ 1 MiB each, at most 2), parses them and writes `cal_invitations`; `MessageSummary.invitation` is filled by a per-page primary-key lookup after each listing (§2.6; T5). `ingest.Decide` never leaves a calendar part ≤ `api.MaxICSBytes` on the server, under any policy. Mail stored before migration 0020 is scanned once in `core.Maintain` within `offlineDays` (meta `calendar.invites.scan`). | The pill and the notification text must be there when the message is announced; nothing is added to `messages` (whose board triggers stay untouched). |
| D18 | An invitation received by a Graph account is answered through the Graph event Exchange created from it (found by `iCalUId`); its "Add to:" is fixed (the calendar Exchange chose). An invitation on an IMAP account is answered by iMIP and, when the user picks a target calendar, stored there: in a CalDAV calendar as the organiser's object with the own `PARTSTAT` (and `SCHEDULE-AGENT=CLIENT` on the `ORGANIZER` when the daemon sends the reply itself); in a Graph calendar as a plain copy without attendees (Graph cannot store an event organised by someone else), linked by UID so that updates of the organiser update the copy. Any invitation whose UID matches a stored event is answered through that event. | One rule: answer where the event lives. |
| D19 | Edit scopes of a recurring event: `this` (an override / a Graph exception), `thisAndFuture` (the series is cut with `UNTIL` / Graph `range.endDate` the day before, a new series with a new UID starts at the occurrence), `all` (the master; overrides whose `RECURRENCE-ID` is no longer an instance are dropped and counted in the result). `RANGE=THISANDFUTURE` is never written. A rule outside what the editor can show (`recurrence.complex`) is edited only as `all` and kept as it is unless replaced. | `RANGE=THISANDFUTURE` is poorly supported by servers and clients; the split is what Apple Calendar and Outlook do. |
| D20 | "Propose New Time…" only for Graph events whose `allowNewTimeProposals` is true (`tentativelyAccept`/`decline` with `proposedNewTime`). No iMIP `COUNTER` in this iteration. | `COUNTER` is handled inconsistently by organisers' clients; offering it would promise something that mostly does nothing. |
| D21 | Event attachments: new events may carry files (the editor's attach button) that go as attachments of the iMIP mail and, on Graph, as event attachments (≤ 3 MiB each, the simple-upload limit; ≤ 10). Attachments of received events are not downloaded (the invitation mail has them). | Covers the wireframe without CalDAV managed attachments (RFC 8607), which few servers implement. |
| D22 | Free/busy of other people only through Graph `getSchedule` (work and school accounts; a personal account answers "unknown"); own busy time from the stored occurrences of every enabled calendar. CalDAV free-busy (`schedule-outbox` POST) is not used. | Owner decision ("availability from free/busy for Graph only"). |
| D23 | Calendar data of an account is cache: switching its calendar sync off, removing the account or disabling a calendar deletes its rows; switching on again resynchronises. Only the user's own columns (`visible`, `user_color`, responses, alert state) survive a resync. | The server is the authority; no migration of cached calendar data is ever needed. |
| D24 | Windows tz names, CLDR data and `time/tzdata` are the only new data; the one new Go dependency is `github.com/teambition/rrule-go` (MIT), in `backend/` (licence compatible with the commercial licence of the core: nothing stronger than MPL/LGPL). Its justification goes into the commit that adds it. | `CLAUDE.md` licensing rules. |
| D25 | The calendar view's display preferences (week start, default duration, default alert, show declined, working hours) are daemon preferences (`calendar.preferences`, meta `calendar.prefs`), not GSettings keys: the MCP bridge (`find_free_time` uses working hours; `create_event` the default alert and duration) and three clients read one source. Only the chosen view (`calendar-view`) and the window mode values are client keys. | Same split as `board.prefs`. |
| D26 | The board's rules change once: messages with a calendar part are not case members (`RulesVersion` "8", §7 Q1, task T12). | Owner decision 2026-10-16: the calendar and the Today strip handle invitations. |
| D27 | Calendar mode reuses the window-mode machinery of the Board: `Board.Mode` (Swift) / `board.Mode` (Go) gains `calendar` (raw value 2, nick `"calendar"`), `board-start-mode` gains the value `calendar`. No rename. | Renaming the mode type touches every board file for no behaviour. |
| D28 | The assistant panel stays one per window; in Calendar mode its view moves into the calendar page's split (as the board's reply pane moves between styles). Fallback, if moving proves fragile in AppKit: ✦ in Calendar switches to Mail with the panel open, as the Board does today. | "Sharing the assistant" with its transcript. |
| D29 | An answer given in another client counts: a request with no local answer whose linked stored event (same UID, not organised by the user, the message's account first) carries `myResponse` accepted / tentative / declined is `answered` — the card shows the state strip without `respondedAt`, Unanswered leaves it out; `needsAction` (Exchange's auto-placed copies) stays unanswered; a local answer wins (`store.InvitationLinkedEvent`, one lookup for list, summary and card). The list pill stays whatever the state (the Go `PillKind` rule). | Owner decision 2026-10-16 after the live test: invitations accepted in Outlook or on the phone kept showing Accept/Tentative/Decline. |
| D30 | `InvitationPrevious` reports every change an update makes against the linked event: `start`/`end` (optional now, only when the time changed), `location` (the previous room) and `recurrence` (the previous rule, incl. until/count); clients show one "Previously …" line per field (`PreviousLines`). | Owner decision 2026-10-16: a room change and an extended series showed no difference on the card. |
| D31 | An all-day event's Alert popup offers day-based choices at 9:00 — None, 1 day before (9:00), 2 days before (9:00), 1 week before (9:00) — stored as minutes before midnight (900, 2340, 9540; `AllDayAlertChoices`, `AlertTitleFor`, `AlertOnAllDayToggle`): the daemon's alerts stay 0–`MaxAlertMinutes`, so "on the day at 9:00" (a positive trigger) is not offered; switching All Day on remaps a timed value to 1 day before (9:00), off to the preference's default; a stored value outside the list (Outlook's 18 h) keeps the generic title. | Owner decision 2026-10-16: the timed choices read as "15 minutes before" = 23:45 the day before. |
| D32 | A request the user sent themself (the ORGANIZER is one of the account's addresses: the Sent copy, Exchange's mailbox copy) is `InvitationSummary.ownEvent`: state `processed`, out of `invitation.list` and the unanswered count, card without a response path (`canRespond` false, no targets) but linked to the stored event; the card line reads "You organise this event." with Show in Calendar. The kind (invitation / update by SEQUENCE) is unchanged. `calendar.ParserVersion` 3 rebuilds stored cards. | Live test 2026-10-16: the owner's own invitation counted as unanswered and read "cannot be answered from here". |

Nothing in the daemon branches by platform; the GTK, macOS and Windows
clients talk to the same methods.

### 1.3 Out of scope (this iteration)

Google Calendar (contract allows it: §4.2 `source: "google"`, a provider
of `internal/calsync`); tasks (VTODO), journals; editing other people's
shared calendars (shared calendars are read-only); delegation; iMIP
`COUNTER`/`DECLINECOUNTER`/`REFRESH` sending (received ones are shown as
`other` and not acted on); subscribed `webcal://` feeds; writing
snooze/dismiss to Outlook; CalDAV managed attachments; Linux EDS calendars;
Exchange rooms finder.

---

## 2. Data model (migration 0020)

Migrations 0017–0019 are frozen (they ran in the owner's store); the
calendar is `backend/internal/store/migrations/0020_calendar.sql`, forward
only, never edited after commit. It adds tables and indexes only: no
column of an existing table, no trigger on `messages`, `issues`,
`issue_items`, `meta` or `drafts` (the board's triggers in 0017/0018 stay
the only ones there). The `accounts` table keeps its JSON `config`; kind
`caldav` needs no schema change.

### 2.1 Tables

Every `TEXT` time below is RFC 3339 UTC with milliseconds
(`strftime('%Y-%m-%dT%H:%M:%fZ')` style, as the rest of the store), every
date `YYYY-MM-DD`. Every table that has triggers (`cal_events`, for its
FTS index) is written only with `INSERT`, `UPDATE` and `DELETE`, **never
`INSERT OR REPLACE`** (it would bypass the delete triggers; the rule of
0017).

```sql
-- One calendar (Graph calendar, CalDAV collection) of an account.
CREATE TABLE cal_calendars (
    id             TEXT PRIMARY KEY,               -- 'cal_' + 32 hex, stable
    account_id     TEXT NOT NULL,
    remote_id      TEXT NOT NULL,                  -- Graph calendar id | CalDAV collection href (absolute path)
    name           TEXT NOT NULL DEFAULT '',
    server_color   TEXT NOT NULL DEFAULT '',       -- '#rrggbb' or ''
    user_color     TEXT NOT NULL DEFAULT '',       -- user's override, survives resync
    visible        INTEGER NOT NULL DEFAULT 1 CHECK (visible IN (0, 1)),   -- user: shown in views
    access         TEXT NOT NULL DEFAULT 'read' CHECK (access IN ('owner', 'write', 'read')),
    kind           TEXT NOT NULL DEFAULT 'events', -- events|birthdays|holidays|other (open enum)
    owner_name     TEXT NOT NULL DEFAULT '',       -- shared calendars
    owner_address  TEXT NOT NULL DEFAULT '',
    server_default INTEGER NOT NULL DEFAULT 0 CHECK (server_default IN (0, 1)),
    time_zone      TEXT NOT NULL DEFAULT '',       -- IANA, the collection's / mailbox's
    position       INTEGER NOT NULL DEFAULT 0,     -- server order
    sync_mode      TEXT NOT NULL DEFAULT '',       -- delta|list (Graph), syncCollection|etags (CalDAV)
    sync_token     TEXT NOT NULL DEFAULT '',       -- Graph deltaLink | CalDAV sync-token
    ctag           TEXT NOT NULL DEFAULT '',       -- CalDAV getctag / Graph changeKey of the calendar
    window_from    TEXT NOT NULL DEFAULT '',       -- the window the token was made for
    window_to      TEXT NOT NULL DEFAULT '',
    last_sync      TEXT NOT NULL DEFAULT '',
    error_code     INTEGER NOT NULL DEFAULT 0,     -- api.ErrorCode of the last failed pass, 0 = none
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX cal_calendars_remote ON cal_calendars (account_id, remote_id);

-- One component as the server has it: a single event, a series master, or
-- an override (a modified occurrence). For CalDAV one resource (href) can
-- hold a master and its overrides: every row of it has the same remote_id;
-- the resource's bytes are kept on the master (or single) row only.
CREATE TABLE cal_events (
    id               TEXT PRIMARY KEY,             -- 'ev_' + 32 hex, stable across passes
    account_id       TEXT NOT NULL,
    calendar_id      TEXT NOT NULL REFERENCES cal_calendars(id) ON DELETE CASCADE,
    remote_id        TEXT NOT NULL,                -- Graph event id | CalDAV href
    etag             TEXT NOT NULL DEFAULT '',     -- CalDAV ETag | Graph @odata.etag
    change_key       TEXT NOT NULL DEFAULT '',     -- Graph changeKey
    uid              TEXT NOT NULL DEFAULT '',     -- iCalendar UID | Graph iCalUId (≤ 255 bytes, cleaned)
    kind             TEXT NOT NULL CHECK (kind IN ('single', 'master', 'override')),
    master_id        TEXT NOT NULL DEFAULT '',     -- override → its master's id ('' when the master is not stored)
    recurrence_id    TEXT NOT NULL DEFAULT '',     -- override: original start (UTC time, or date for all-day)
    sequence         INTEGER NOT NULL DEFAULT 0,
    title            TEXT NOT NULL DEFAULT '',
    location         TEXT NOT NULL DEFAULT '',
    notes            TEXT NOT NULL DEFAULT '',     -- plain text, cleaned, ≤ MaxEventNotesBytes
    links_json       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(links_json)),   -- https URLs found in notes/location
    video_url        TEXT NOT NULL DEFAULT '',     -- https only (§3.9)
    video_provider   TEXT NOT NULL DEFAULT '',     -- teams|zoom|meet|webex|other
    start_at         TEXT NOT NULL DEFAULT '',     -- timed: UTC
    end_at           TEXT NOT NULL DEFAULT '',
    start_date       TEXT NOT NULL DEFAULT '',     -- all-day: date; end_date exclusive
    end_date         TEXT NOT NULL DEFAULT '',
    all_day          INTEGER NOT NULL DEFAULT 0 CHECK (all_day IN (0, 1)),
    floating         INTEGER NOT NULL DEFAULT 0 CHECK (floating IN (0, 1)),   -- local time without zone
    time_zone        TEXT NOT NULL DEFAULT '',     -- IANA zone of start (resolved, D4)
    end_time_zone    TEXT NOT NULL DEFAULT '',
    rrule            TEXT NOT NULL DEFAULT '',     -- master: normalised RRULE value (validated by recur)
    rdates_json      TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(rdates_json)),
    exdates_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(exdates_json)),
    recurrence_json  TEXT NOT NULL DEFAULT '',     -- api.Recurrence of a master ('' = none)
    recur_ok         INTEGER NOT NULL DEFAULT 1 CHECK (recur_ok IN (0, 1)),   -- 0: rule refused, first instance only
    status           TEXT NOT NULL DEFAULT 'confirmed',  -- confirmed|tentative|cancelled
    show_as          TEXT NOT NULL DEFAULT 'busy',       -- free|tentative|busy|oof|workingElsewhere|unknown
    private          INTEGER NOT NULL DEFAULT 0 CHECK (private IN (0, 1)),
    organizer_json   TEXT NOT NULL DEFAULT '' ,          -- api.Address or ''
    i_am_organizer   INTEGER NOT NULL DEFAULT 0 CHECK (i_am_organizer IN (0, 1)),
    my_response      TEXT NOT NULL DEFAULT '',           -- ''|needsAction|accepted|tentative|declined|delegated
    response_requested INTEGER NOT NULL DEFAULT 0 CHECK (response_requested IN (0, 1)),
    new_time_ok      INTEGER NOT NULL DEFAULT 0 CHECK (new_time_ok IN (0, 1)),  -- Graph allowNewTimeProposals
    attendee_count   INTEGER NOT NULL DEFAULT 0,
    attendees_json   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attendees_json)), -- ≤ MaxEventAttendeesStored
    alarms_json      TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(alarms_json)),    -- [minutes before start]
    online_providers TEXT NOT NULL DEFAULT '',          -- Graph onlineMeetingProvider of the event
    raw              BLOB,                               -- CalDAV: the resource as fetched (≤ MaxICSBytes); NULL otherwise
    server_updated   TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX cal_events_remote ON cal_events (calendar_id, remote_id, recurrence_id);
CREATE INDEX cal_events_uid    ON cal_events (account_id, uid);
CREATE INDEX cal_events_master ON cal_events (master_id) WHERE master_id != '';

-- Materialised occurrences inside the account's window (§2.3): what
-- event.list reads. Derived (cache): rebuilt from cal_events for CalDAV,
-- written from calendarView for Graph. event_id is the row whose fields
-- show (the single, the master, or the override that replaces the
-- master's instance); occurrence_key is '' for a single event, else the
-- recurrence id. Cancelled instances are not stored.
CREATE TABLE cal_occurrences (
    event_id       TEXT NOT NULL REFERENCES cal_events(id) ON DELETE CASCADE,
    occurrence_key TEXT NOT NULL,
    series_id      TEXT NOT NULL DEFAULT '',        -- master id of a series instance or override
    calendar_id    TEXT NOT NULL,
    account_id     TEXT NOT NULL,
    start_at       TEXT NOT NULL,                   -- UTC; all-day/floating: the date's 00:00 as if UTC
    end_at         TEXT NOT NULL,
    all_day        INTEGER NOT NULL DEFAULT 0,
    floating       INTEGER NOT NULL DEFAULT 0,
    remote_id      TEXT NOT NULL DEFAULT '',        -- Graph: the occurrence's own id
    PRIMARY KEY (event_id, occurrence_key)
) WITHOUT ROWID;
CREATE INDEX cal_occurrences_range   ON cal_occurrences (calendar_id, start_at);
CREATE INDEX cal_occurrences_account ON cal_occurrences (account_id, start_at);
CREATE INDEX cal_occurrences_series  ON cal_occurrences (series_id, occurrence_key) WHERE series_id != '';

-- CalDAV: every href the daemon knows in a collection, also those it does
-- not store as events (outside the window, unparsable, too big, VTODO
-- only), so that a pass does not fetch them again while their ETag holds.
CREATE TABLE cal_resources (
    calendar_id TEXT NOT NULL REFERENCES cal_calendars(id) ON DELETE CASCADE,
    href        TEXT NOT NULL,
    etag        TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL CHECK (state IN ('stored', 'outside', 'skipped')),
    reason      TEXT NOT NULL DEFAULT '',            -- skipped: tooBig|malformed|noEvent|limit
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (calendar_id, href)
) WITHOUT ROWID;

-- Alerts of occurrences that start within the next 7 days (derived),
-- with the user's state (authoritative: fired, dismissed, snoozed).
CREATE TABLE cal_alerts (
    id             TEXT PRIMARY KEY,                 -- 'al_' + 32 hex
    event_id       TEXT NOT NULL REFERENCES cal_events(id) ON DELETE CASCADE,
    occurrence_key TEXT NOT NULL,
    minutes_before INTEGER NOT NULL,
    due_at         TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'fired', 'dismissed')),
    snoozed_until  TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX cal_alerts_key ON cal_alerts (event_id, occurrence_key, minutes_before);
CREATE INDEX cal_alerts_due ON cal_alerts (due_at) WHERE state = 'pending';

-- A calendar part of a stored message (D17). One row per message row
-- (a copy in another folder has its own row); the card's static fields
-- in card_json (api.InvitationCard without the derived fields).
CREATE TABLE cal_invitations (
    message_id      TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    account_id      TEXT NOT NULL,
    part_id         TEXT NOT NULL,
    method          TEXT NOT NULL,                   -- request|reply|cancel|counter|declineCounter|refresh|add|publish|other
    uid             TEXT NOT NULL,
    recurrence_id   TEXT NOT NULL DEFAULT '',
    sequence        INTEGER NOT NULL DEFAULT 0,
    dtstamp         TEXT NOT NULL DEFAULT '',
    organizer       TEXT NOT NULL DEFAULT '',        -- lower-case address
    start_at        TEXT NOT NULL DEFAULT '',        -- for the pill, the list and the Invitations section
    end_at          TEXT NOT NULL DEFAULT '',
    all_day         INTEGER NOT NULL DEFAULT 0,
    title           TEXT NOT NULL DEFAULT '',
    card_json       TEXT NOT NULL CHECK (json_valid(card_json)),
    parser_version  INTEGER NOT NULL,                -- calendar.ParserVersion; a higher one re-reads the part
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX cal_invitations_uid ON cal_invitations (account_id, uid, recurrence_id);

-- The user's answer to an invitation, shared by every copy of its
-- message (authoritative).
CREATE TABLE cal_responses (
    account_id    TEXT NOT NULL,
    uid           TEXT NOT NULL,
    recurrence_id TEXT NOT NULL DEFAULT '',
    sequence      INTEGER NOT NULL DEFAULT 0,        -- the invitation's SEQUENCE answered
    response      TEXT NOT NULL CHECK (response IN ('accepted', 'tentative', 'declined')),
    via           TEXT NOT NULL CHECK (via IN ('graph', 'caldav', 'imip', 'none')),
    calendar_id   TEXT NOT NULL DEFAULT '',          -- where Accept put it
    event_id      TEXT NOT NULL DEFAULT '',
    outbox_id     TEXT NOT NULL DEFAULT '',          -- the queued REPLY
    responded_at  TEXT NOT NULL,
    PRIMARY KEY (account_id, uid, recurrence_id)
) WITHOUT ROWID;

-- Event search (contentless FTS5 like messages_fts, 0013): title,
-- location, notes, organiser and attendee names and addresses. Keyed by
-- a docid of its own, as search_docs keys messages_fts: the implicit
-- rowid of cal_events (TEXT primary key) may change on VACUUM.
CREATE TABLE cal_events_fts_docs (
    docid    INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE
);
CREATE VIRTUAL TABLE cal_events_fts USING fts5(
    title, location, notes, people, content='', contentless_delete=1,
    tokenize='unicode61 remove_diacritics 2', prefix='2 3'
);
-- + the view cal_event_search_text (the indexed text) and three triggers
-- cal_events_fts_ai/_ad/_au on cal_events, keyed through cal_events_fts_docs.
```

The FTS index is keyed through `cal_events_fts_docs` (a stable `docid`
per event id), exactly as 0013 keys `messages_fts` through `search_docs`,
because the implicit rowid of a table with a `TEXT` primary key may change
on `VACUUM`. `contentless_delete` is available in the bundled
`modernc.org/sqlite` (0013 already relies on it;
`TestSearchEventsContentlessDelete` checks that a deleted event, also
through its calendar's cascade, leaves no index entry).

`cal_invitations.card_json` is JSON `null` for a calendar part that could
not be read (state `invalid`, no card); `store.Invitation.Card` is then
nil.

`cal_invitations.message_id` cascades with the message row, so a moved,
deleted or expired message takes its invitation with it; a move that makes
a new row (IMAP copy + expunge) re-detects through the `Stored` hook.

### 2.2 Cache versus authoritative

| Data | Kind | Survives |
|---|---|---|
| `cal_calendars` server fields, `cal_events`, `cal_occurrences`, `cal_resources`, sync tokens | cache of the server | nothing: sync off / account removed / calendar disabled deletes them (D23) |
| `cal_calendars.visible`, `user_color` | user | resync (matched by `remote_id`); deleted with the account |
| `cal_alerts.state`, `snoozed_until` | user | a pass that keeps the occurrence; an alert whose occurrence moved gets a new row |
| `cal_invitations` | derived from the stored message | re-made when `parser_version` grows |
| `cal_responses` | user | the account's removal |
| meta `calendar.prefs` | user (preferences) | everything |

### 2.3 Windows and retention

Each account with calendars has a window: from `today − keepDays` (the
account's `calendar.keepDays`, §4.2, default 365) to `today + 400 days`
(fixed; `api.CalendarFutureDays`). Occurrences are materialised inside it;
an event whose every occurrence ended before the window's start is
deleted, a master with an occurrence in the window is kept with only
those. The window moves: the daily step of `core.Maintain` trims below it;
Graph delta tokens are made for a fixed window, so a Graph calendar whose
`window_from` is more than 7 days behind the current one (or whose
`keepDays` changed) starts a new delta chain (a full listing of the
window). `event.list` of a range outside the window asks the server once
(Graph `calendarView`, CalDAV `calendar-query` with `time-range` and
daemon expansion), stores nothing and says so (`complete: false` when it
could not).

### 2.4 Meta keys

| Key | Value |
|---|---|
| `calendar.prefs` | JSON `api.CalendarPreferences` |
| `calendar.invites.scan` | `<calendar.ParserVersion>:<last message rowid>` or `<v>:done`: the backfill cursor of D17 |
| `calendar.expand` | `<recur.Version>.<calendar.ExpandVersion>:<Go version>` (Go exposes no tzdata version; `calsync/window.go`): when it changes, every daemon-expanded calendar's occurrences are rebuilt from `cal_events` (no network) |

### 2.5 Account removal and pause

`store.DeleteAccount` deletes the account's rows of every `cal_*` table
(they carry `account_id`; `cal_calendars` cascades to events, occurrences,
resources and alerts). A paused account keeps its rows; `event.list`,
`invitation.list`, alerts and free/busy leave paused accounts out.

### 2.6 What must not break

- The board triggers on `messages` (0017 `messages_board_au` and friends):
  calendar code never updates a `messages` column; invitations live in
  `cal_invitations`.
- `INSERT OR REPLACE` is never used on `cal_events` (FTS triggers) nor on
  `board_*`, `messages`, `issues`, `issue_items`, `meta` (existing rule).
- Message listings gain the invitation of their rows from `cal_invitations`
  (primary-key lookups): instead of a LEFT JOIN in each of the listing
  queries (several are nested selects over `messageColumns`), the store
  looks the page's message ids up in one query after the listing
  (`attachInvitations` in `ListMessages`, `GetMessage`, `SearchMessages`,
  `ThreadMessages`, `ThreadSentMessages` and the heads of `ListThreads`),
  filling `store.Message.Invitation` (`*api.InvitationSummary`, its `kind`
  and `state` derived there from `cal_invitations`, `cal_responses` and
  `cal_events`); `TestBoardDryRun` and the listing benchmarks must not
  regress.

---

## 3. Daemon architecture

### 3.1 Packages

```
backend/internal/calendar/            pure: no store, no network, no clock but a parameter
    ics/        iCalendar content lines → component tree, and back (D1)
    tz/         TZID resolution, Windows zone table (generated from CLDR), VTIMEZONE writer (D4)
    recur/      RRULE parse/validate/normalise, expansion through rrule-go with caps (D2)
    event.go    Event model ⇄ ics components; Graph-neutral
    expand.go   series (master + overrides + RDATE/EXDATE) → occurrences in a window
    text.go     cleaning of every string (control, bidi, invisible, length), https link and
                video-call detection
    freebusy.go busy intervals, merge, free slots within working hours, conflicts of a slot
    imip/       parse an iTIP message (METHOD) into an Invitation; build REPLY, REQUEST, CANCEL
backend/internal/calsync/             the calendar engine (store + providers)
    provider.go the Provider interface every source implements (written by T1, frozen)
    supervisor.go, syncer.go   one syncer per account with calendars; state machine and backoff
    window.go   the account's window, re-baselines
    occurrences.go  materialisation of cal_occurrences for daemon-expanded calendars
    alerts.go   the alert scheduler (D12)
backend/internal/caldav/              CalDAV client, discovery, Provider (D3, D8)
    caldavtest/ in-memory CalDAV server for tests, with server profiles
backend/internal/graph/               + calendar_*.go: calendars, calendarView (delta|list),
                                      masters, writes, responses, getSchedule, recurrence
                                      patterns, Provider; limiter.go (D13, D14)
backend/internal/core/                calendar_*.go, event_*.go, invitation_*.go, accounts_caldav.go
backend/internal/store/               calendar*.go + migration 0020
backend/internal/smtp/                BuildInput.Calendar (text/calendar alternative)
backend/internal/ingest/              Decide keeps calendar parts (D17)
backend/cmd/malachi-mcp/              tools_calendar.go
backend/testdata/calendar/            pathological .ics corpus (§3.10)
backend/testdata/caldav/              pathological XML / multistatus answers
```

Dependency direction: `calendar` (pure) ← `calsync` ← `caldav`, `graph`
← `core`. `calsync`'s Object, Event, Occurrence, Change, Response,
Availability and Attachment are aliases of the `calendar` types.

Model notes (T4, `calendar/event.go`): an `Object` is `{RemoteID, ETag,
Master *Event, Overrides []Event, Occurrences, Raw, Problems}`; `ToICS`
preserves from `Raw` everything the model does not hold and keeps a
field's original properties verbatim while it is unchanged (D1). A TZID
that resolves to nothing (no IANA, Windows or prefix name, no usable
`VTIMEZONE`) is read as floating (problem `zoneUnknown`); the fixed-offset
fallback of D4 becomes the IANA zone `Etc/GMT∓N` for a whole hour within
−12…+14, else the instants use the offset and the event is written in
UTC. A `DTSTART` before the year 1000 is no start. `DTSTART` always
counts as the first instance (RFC 5545 §3.8.5.3; recur alone counts it
only when it matches). Notes come from `DESCRIPTION`, else from
`STYLED-DESCRIPTION` (HTML through the sanitiser's text); `X-ALT-DESC` is
never read. `calsync` never imports `caldav` or `graph`; `core` wires them.
`store` imports neither (rows are primitives and `api` types).

### 3.2 iCalendar parsing (`calendar/ics`)

- Input: bytes ≤ `api.MaxICSBytes` (1 MiB), refused above (`ErrTooBig`).
  Invalid UTF-8 is replaced (U+FFFD), NUL bytes removed, CRLF, bare LF and
  bare CR all end a line; unfolding joins a line starting with space or
  tab. One unfolded content line ≤ 64 KiB (longer: the property is
  dropped, a problem noted).
- Grammar: `name *(";" param) ":" value`, names case-insensitive ASCII
  (`[A-Za-z0-9-]`, ≤ 64), parameters with quoted values (no `"` inside, no
  control characters), ≤ 32 per property; values unescaped (`\n`, `\,`,
  `\;`, `\\`) only for TEXT properties.
- Components nest `BEGIN`/`END` up to depth 4 (`VCALENDAR` → `VEVENT` →
  `VALARM`; `VTIMEZONE` → `STANDARD`/`DAYLIGHT`); a mismatched `END` closes
  up to the matching open component, a missing `END` closes at the end of
  input (tolerated, problem noted). Caps: 500 components per object, 1000
  properties per component, 5000 properties per object; reaching one
  truncates (`Truncated`, never a partial component used as if whole).
- Typed accessors: `DATE`, `DATE-TIME` (UTC, with `TZID`, floating),
  `DURATION`, `PERIOD`, `UTC-OFFSET`, `CAL-ADDRESS` (only `mailto:`
  addresses become addresses; others are kept opaque and never contacted),
  `INTEGER` (bounded), `URI` (kept as text; only `calendar/text.go`
  decides what is a link).
- Encoder: CRLF, folding at 75 octets on UTF-8 boundaries, escaping, and a
  round-trip guarantee for what the parser accepted: `Parse(Encode(x)) ==
  x`. Unknown properties and components of an object the daemon rewrites
  (a CalDAV PUT) are preserved from `raw`.
- Never panics (`recover()` at the API boundary turns a bug into
  `ErrMalformed`); fuzz target `FuzzParse` (round trip and no panic) and
  `FuzzParseInvitation`.

### 3.3 Recurrence (`calendar/recur`)

`recur.Parse(value) (Rule, error)` accepts RFC 5545 RRULE with these
limits, and refuses (`ErrUnsupported`) anything else:

- `FREQ` ∈ `HOURLY`, `DAILY`, `WEEKLY`, `MONTHLY`, `YEARLY` (`SECONDLY` and
  `MINUTELY` refused); `INTERVAL` 1–1000; `COUNT` 1–5000; `UNTIL` a DATE or
  DATE-TIME between 1900 and 2200; every BYxxx list ≤ 366 entries and
  values in range (`BYMONTHDAY` ±1..31, `BYYEARDAY` ±1..366, `BYWEEKNO`
  ±1..53, `BYHOUR` 0..23, `BYMINUTE`/`BYSECOND` only `0` or the
  DTSTART's value, `BYSETPOS` ±1..366); `WKST`; repeated parts refused.
- `DTSTART` between 1900-01-01 and 2200-01-01 for any recurring event
  (outside: the event is shown as its first instance only, `recur_ok = 0`).
- `Expand(rule, dtstart, loc, window, cap)`: builds an `rrule.ROption`
  whose `Until` is the earlier of the rule's `UNTIL` and the window's end,
  iterates with rrule-go's iterator, stops at `cap` occurrences (per series
  per window: `api.MaxSeriesOccurrences` = 1000) and at the window's end;
  `HOURLY` additionally needs `INTERVAL` ≥ 1 and stops after 20 000
  candidates. A `recover()` around rrule-go turns a panic into
  `ErrUnsupported`.
- Static check against filters that can never match (e.g. `BYMONTH=2;
  BYMONTHDAY=30,31`, `BYMONTHDAY=31` with only 30-day months, `BYSETPOS`
  beyond the set): such a rule is refused before rrule-go runs.
- `RDATE` (≤ 1000), `EXDATE` (≤ 5000) applied after expansion; an override
  (`RECURRENCE-ID`) replaces the instance with that id; an override
  without an instance is shown as itself.
- DST: instances are computed in the event's zone (`time.Date` in `loc`);
  a local time in a spring-forward gap moves forward by the gap (RFC 5545
  §3.3.10), a repeated local time takes the first.
- `recur.Version` (an integer) is part of meta `calendar.expand`.
- `api.Recurrence` (§4.2) is derived from a Rule; a rule that does not fit
  it is `complex: true` (D19). Graph's `patternedRecurrence` converts to
  and from a Rule in `internal/graph` (`calendar_recurrence.go`).
- Tests: table tests against expected instance lists (including DST in
  Europe/Prague and America/New_York, leap days, `BYDAY=-1FR`, `BYSETPOS`),
  every pathological rule of `testdata/calendar/rrule-*.ics` ends within
  100 ms, fuzz `FuzzExpand` with a 1-second budget per input.

### 3.4 iTIP / iMIP (`calendar/imip`)

- `ParseMessage(ics []byte, me []string) (Invitation, error)` (`me`: the
  addresses below, as the core collects them): the object's
  `METHOD` (absent: `publish`), its first `VEVENT` UID (all `VEVENT`s of
  one object must share the UID; others are ignored, problem noted), the
  master or the instance (`RECURRENCE-ID`), `SEQUENCE`, `DTSTAMP`,
  `ORGANIZER`, `ATTENDEE`s (≤ `MaxEventAttendeesStored`, count kept), the
  attendee that is the user (`me` = the account's address, its Sent
  senders and the user's other accounts' addresses, the board's
  `Identity.WithSelf`), the summary fields of the card, and for `reply`
  the replying attendee and its `PARTSTAT`.
- `BuildReply(inv, me, response, comment, now)`: `METHOD:REPLY`, one
  `VEVENT` with `UID`, `RECURRENCE-ID` (instance replies), the
  invitation's `SEQUENCE`, `DTSTAMP` now, `ORGANIZER`, the user's
  `ATTENDEE;PARTSTAT=…` (the invited address), `DTSTART`/`DTEND` and
  `SUMMARY` copied (Outlook needs them), `COMMENT` (≤ 2 KiB).
- `BuildRequest(obj, organizer, now)` / `BuildCancel(obj, organizer,
  scope, recurrenceId, now)`: from the stored object (master and
  overrides) after a successful write; `SEQUENCE` incremented by the
  caller on every significant change (time, recurrence, location, status,
  attendees: `calendar.Significant`), a CANCEL writes it plus one;
  `BuildCancel` takes `all` or `this` (the instance's override or the
  master's instance), `thisAndFuture` is refused (the cut series goes to
  the attendees as a REQUEST of the cut master), its attendees are those
  of the object given (removed attendees only: `imip.RemovedAttendees`);
  `ORGANIZER` the sending account's address,
  `ATTENDEE;RSVP=TRUE;PARTSTAT=NEEDS-ACTION;SCHEDULE-AGENT=CLIENT`,
  `VTIMEZONE`s from `tz`.
- Mail: `smtp.BuildInput.Calendar = &smtp.CalendarPart{Method, Data}`
  writes `multipart/alternative[text/plain, text/calendar;
  method=REQUEST; charset=UTF-8]` (+ `mixed` with attachments, D21). The
  outbox stores and sends it like any message; `notify.newMessage` is not
  produced for it (outbox). Recipients: REPLY → the organiser only;
  REQUEST/CANCEL → the attendees changed by the write (new ones get
  REQUEST, removed ones CANCEL), never the user's own addresses.

### 3.5 CalDAV (`internal/caldav`)

**Client.** One per account: base URL (https; http only for a loopback
host, as Jira), Basic authentication with `Credentials.Password` from the
keyring (`auth.KeyPassword`), TLS through `transport.EndpointTLSConfig`
with the account's pin (§3.9). Every response body ≤ 32 MiB (multistatus)
or `MaxICSBytes` + 64 KiB (GET of an object); XML depth ≤ 32, elements ≤
200 000 per answer, `encoding/xml` with `Strict` and no entity
expansion (Go's decoder has none; DTDs are skipped). Redirects: followed
up to 5, only to the same scheme+host+port, the `Authorization` header
is never sent to another origin; `.well-known/caldav` may redirect to
another path of the same origin, and to another origin only during
discovery, where the user confirms the new server (the result's
`serverUrl` shows it; credentials go only after `account.listCalDAVCalendars`
with that URL). Concurrency 2 per account; `Retry-After` honoured
(≤ 10 min); 401 → `authFailed`, 403 on a collection → the calendar
`readOnly`/skipped, 404/410 → gone, 412 → `conflict`, 507 → `serverError`.

**Discovery** (`account.discoverCalDAV`, `account.listCalDAVCalendars`):
the typed URL normalised (https assumed); if it has no path or `/`, try
`/.well-known/caldav` (RFC 6764), then DNS SRV `_caldavs._tcp` (through
`internal/discover`'s resolver) and a TXT `path`, then the URL itself;
`OPTIONS` must show `calendar-access` in `DAV:`. With credentials:
`PROPFIND current-user-principal` → `calendar-home-set`,
`calendar-user-address-set` (the `mailto:` values become the account's
addresses), `displayname`, `schedule-outbox-URL`; `DAV:` header
`calendar-auto-schedule` decides `scheduling` (D8). Then `PROPFIND` depth 1
on the home set: collections with `resourcetype` `calendar` whose
`supported-calendar-component-set` includes `VEVENT` (VTODO-only lists
are skipped), `displayname`, `calendar-color` (Apple: `#RRGGBBAA` →
`#rrggbb`), `current-user-privilege-set` (`write-content` → `write`),
`getctag`, `sync-token`, `calendar-timezone`. Max 200 calendars.

**Sync of a collection.** A pass: `PROPFIND` depth 0 for `getctag` /
`sync-token`; unchanged → nothing. Changed: with a stored sync-token,
`REPORT sync-collection` (RFC 6578; 403/409 `valid-sync-token` → a full
pass); without, `REPORT calendar-query` with `time-range` = window for
`href`+`getetag` (the first pass; servers that refuse `time-range` get a
depth-1 `PROPFIND getetag`). Compare with `cal_resources`; changed hrefs
are fetched by `calendar-multiget` in batches of 50 (or GET each when the
server refuses multiget). Each object is parsed, cut to the window
(`stored` / `outside`), and replaces the rows of its href in one
transaction; vanished hrefs are deleted. Limits: 50 000 hrefs per
collection (`skipped: limit` beyond; state `error` with `serverError`
data `tooManyEvents`).

**Writes.** Create: `PUT <collection>/<uid>.ics` with `If-None-Match: *`,
where the UID is a random UUID v4 in upper case (nothing derived from the
user or the host); update: `PUT` with
`If-Match: <etag>` of the resource bytes rebuilt from `raw` (unknown
properties kept, our fields replaced, `SEQUENCE` and `DTSTAMP`/
`LAST-MODIFIED` updated); delete: `DELETE` with `If-Match`. The answer's
`ETag` (or a following GET when absent, iCloud) updates the row.

**Fake server** `caldav/caldavtest`: in-memory collections with ETags,
ctag, sync tokens, scheduling on/off, profiles `nextcloud`, `icloud`
(no ETag on PUT, `calendar-color` with alpha, `.well-known` to another
host), `fastmail`, `radicale` (no sync-collection), `hostile` (huge
multistatus, deep XML, redirect to another host, 401 after 200, slow
answers, wrong content types, 207 with garbage). No network: an
`http.RoundTripper` like `jiratest`.

**As built (T6a).** Where the client is more precise than the text above:

- *Discovery* sends no credentials at all. A `.well-known` redirect is
  followed anonymously (≤ 5 hops, https or loopback http, another origin
  too) so that `OPTIONS` can check `calendar-access` where it led; the
  result names that server and the password goes there only after the
  user confirmed it (`account.listCalDAVCalendars` with that URL). An
  `OPTIONS` answer counts on a 2xx and on a 401 that carries the `DAV`
  header with `calendar-access` (iCloud advertises before
  authentication), and on a 401 with no `DAV` header at all (sabre/dav,
  i.e. Nextcloud, ownCloud, Baïkal, authenticates before its `OPTIONS`
  handler, found live 2026-10-16); the signed-in
  `account.listCalDAVCalendars` then verifies it (`serverError` when the
  home set has no calendars and its `OPTIONS` lacks `calendar-access`).
  A 2xx, or a 401 with a `DAV` header, without `calendar-access` is not
  CalDAV. The resolver is
  `caldav.Resolver` (SRV + TXT; `net.Resolver` satisfies it, as it does
  `discover.Resolver`, which has no TXT). Error priority when nothing
  works: `tlsError` (with `TLSErrorData`), then `serverError` (something
  answered, nothing is CalDAV), then the network error. `Discoverer.Pin`
  is the certificate the user trusted after a `tlsError` and applies to
  every host tried.
- *Other origins*: hrefs of a multistatus on another origin are dropped
  (as are hrefs with dot segments, user info, control or format
  characters, > 2048 B); a principal or home set on another origin is
  `ErrOtherOrigin` (serverError) unless the caller trusts that origin
  (`Options.Origins`). iCloud's real home set lives on a partition host
  (`pNN-caldav.icloud.com`): the core (T9a, `calDAVOrigin`) trusts an
  https host on the same registrable domain as `serverUrl` and retries
  the listing with the hosts the server names, so it works without the
  client passing anything.
- *Redirects* of PROPFIND, REPORT, GET and OPTIONS keep method and body
  (WebDAV servers expect it); PUT and DELETE are never redirected (the
  3xx is the error).
- *Throttling*: a 429/503 whose `Retry-After` is ≤ 30 s is waited out by
  the client (twice at most); a longer one ends the request with the wait
  (capped at 10 min) in `caldav.RetryAfter(err)` for the syncer's backoff.
- *Error mapping* (`caldav.ToAPIError`): 401 authFailed, 403 readOnly (a
  read pass treats `IsForbidden` as "calendar skipped"), 404/410
  eventGone, 412 conflict, 408/429/504 serverTimeout, 507 and every other
  status serverError, an answer that does not parse malformedCalendar.
  The server's free text never reaches `error.message`; only the status
  and a `DAV:error` precondition name (`valid-sync-token`,
  `need-privileges`) do.
- *XML*: elements outside the `DAV:` / CalDAV namespaces are not read, so
  a namespace-less "multistatus" is malformedCalendar. One answer holds
  at most 200 000 elements, i.e. about 33 000 members in one listing
  (six elements each); a collection beyond that fails its first pass
  (malformedCalendar) before the 50 000-href limit is reached, unless the
  server pages sync-collection with 507 truncation.
- `certificate` of the discovery result is built in `caldav` with the same
  cleaning and caps as `transport`'s TLS error data (`transport` does not
  export its helper).

**As built (T6b, the provider `caldav.NewProvider`).** Where the provider
differs from or adds to the text above and §3.7:

- *Known resources*: the `Provider` interface passes none, so
  `ProviderOptions.Resources` (`ListResources` + `PutResources`, which
  `*store.Store` satisfies) is read at the start of a pass. The engine's
  sink writes the rows of the objects it receives (stored/outside) and
  forgets removed ones; the provider writes only `skipped` rows
  (`tooBig`, `malformed`, `noEvent`). Without `Resources` it keeps an
  in-memory map per calendar id (a restart re-fetches the window).
- *Cut to the window*: every fetched object goes to `Upsert`, also one
  whose instances all lie outside the window (a PROPFIND fallback lists
  every member); the engine marks it `outside`. A pass whose window
  differs from the calendar row's `windowFrom`/`windowTo` lists the window
  again (calendar-query) and re-reads `outside` rows; otherwise a stored
  sync token is followed with sync-collection.
- *`Full`* is true only when the pass handed every object of the window to
  the sink (no known ETag let it skip one); otherwise it removes vanished
  hrefs itself (`Remove`).
- *Over 50 000 hrefs*: `serverError` with `data: {"reason": "tooManyEvents"}`.
- *iMIP*: `WriteResult` has no room for messages, so the provider builds
  them itself (`imip.BuildRequest/BuildCancel/BuildReply`, recipients by
  `imip.Recipients`) and hands them to `ProviderOptions.SendIMIP` (core
  queues them in the outbox of `WriteOptions.MailAccountID`) after the
  server accepted the write; `WriteResult.Invitations` says `server`,
  `mail` or `none` with the recipient count. A write that needs mail
  without `SendIMIP` or a mail account is `invitationsUnavailable` before
  anything is written. A failed hand-off returns the result together with
  the error.
- *Scheduling marks*: an organiser's PUT with client scheduling (or with
  server scheduling and `Notify: false`) sets `SCHEDULE-AGENT=CLIENT` on the
  ATTENDEEs; with server scheduling and `Notify` it removes the parameter.
  An event organised by someone else stored by `Create` (D18) and a
  response the daemon sends itself (client scheduling, an ORGANIZER that
  already has `SCHEDULE-AGENT=CLIENT`, or `Notify: false`) set it on the
  ORGANIZER.
- *Conflict*: a 412 is `conflict` with the re-fetched current object as
  `*calendar.Object` in `Error.Data`; core replaces it with
  `api.ConflictEventData`.
- *Range* returns `calendar.Occurrence` values with `RemoteID` set to the
  object's href; the event fields are not part of the interface's type.
- An attendee's delete sends nothing; core declines first through
  `Respond` when the user asks. `Respond` with a proposed time is
  `calsync.ErrUnsupported`.

### 3.6 Microsoft Graph (`internal/graph`, calendar files)

- Calendars: `GET /me/calendars?$top=200` (name, `hexColor` or `color` →
  hex palette table, `canEdit`, `isDefaultCalendar`, `owner`,
  `allowedOnlineMeetingProviders`, `defaultOnlineMeetingProvider`,
  `changeKey`). Birthdays and holiday calendars are read-only (kind
  `events`, see the T7b notes below).
- Events: `calendarView/delta` per calendar with `Prefer:
  outlook.timezone="UTC"` and `odata.maxpagesize=200` for timed values;
  all-day events arrive as midnight-to-midnight in UTC and are stored as
  dates (verified in the spike, T0). Items: `singleInstance`,
  `occurrence`, `exception` (stored as `cal_occurrences`; `exception` also
  as an `override` row), `@removed` (deleted). Masters fetched by id (D14).
  Fields read: `subject`, `bodyPreview`/`body` (→ text via the sanitiser),
  `location.displayName`, `start`/`end` with `originalStartTimeZone`
  (Windows or IANA name → `tz`), `isAllDay`, `showAs`, `sensitivity`,
  `isCancelled`, `responseStatus`, `responseRequested`, `organizer`,
  `attendees` (≤ 500), `isOrganizer`, `isOnlineMeeting`/`onlineMeeting.joinUrl`,
  `isReminderOn`/`reminderMinutesBeforeStart`, `allowNewTimeProposals`,
  `iCalUId`, `seriesMasterId`, `originalStart`, `recurrence`, `changeKey`.
- Writes: `POST /me/calendars/{id}/events` (with `transactionId` for
  idempotence; attendees → Exchange sends the invitations, it cannot be
  disabled), `PATCH /me/events/{id}` with `If-Match`, `DELETE`, `POST
  …/cancel` (organiser with attendees, `comment`), `POST …/accept |
  tentativelyAccept | decline` (`comment`, `sendResponse`,
  `proposedNewTime`), event attachments `POST …/attachments` (≤ 3 MiB).
- Free/busy: `POST /me/calendar/getSchedule` (≤ 20 addresses, ≤ 62 days,
  `availabilityViewInterval` 15/30/60); an error per schedule item →
  that attendee `unknown`; a personal account → every attendee `unknown`.
- Invitation messages of a Graph mailbox (`eventMessage`): the MIME
  Exchange produces carries a `text/calendar` part, detected by D17 (spike
  verifies; if a meeting request has none, the syncer marks
  `#microsoft.graph.eventMessageRequest` messages from the delta item's
  `@odata.type` and the card is built from the linked event, `GET
  /me/messages/{id}?$expand=microsoft.graph.eventMessage/event`).
- Errors: 403 `ErrorAccessDenied` / `Authorization_RequestDenied` on a
  calendar call → `calendarConsentRequired` (the account's mail
  unaffected); 404 / `ErrorItemNotFound` → `eventGone`; 412 →
  `conflict`; `ErrorInvalidRecurrencePattern` → `recurrenceUnsupported`.

**T0 findings (live, 2026-10-16, owner's work account, token with
`Calendars.Read Mail.Read`):**

- `GET /me/calendars`: four calendars — the default "Kalendář"
  (`isDefaultCalendar`, `canEdit`, `color: lightRed`, `hexColor: #d13438`,
  `allowedOnlineMeetingProviders: [teamsForBusiness]`), a second writable one
  (`color: lightOrange`, `hexColor: #e67e22`), the holidays calendar
  (`canEdit: false`) and "Narozeniny" (`canEdit: false`, `color: auto`,
  `hexColor: ""` — a calendar may have no hex colour: use the palette
  fallback). Keys present: `allowedOnlineMeetingProviders`, `canEdit`,
  `canShare`, `canViewPrivateItems`, `changeKey`, `color`,
  `defaultOnlineMeetingProvider`, `groupClassId`, `hexColor`, `id`,
  `isDefaultCalendar`, `isRemovable`, `isTallyingResponses`, `name`,
  `owner`.
- `GET /me/calendarView/delta` (30 days, `Prefer: outlook.timezone="UTC"`,
  `odata.maxpagesize=200`): works, one page, `@odata.deltaLink` present;
  item `type`s seen: `occurrence` (8), `seriesMaster` (3), `singleInstance`
  (1), all `@odata.type: #microsoft.graph.event`. `originalStartTimeZone`
  / `originalEndTimeZone` = `Central Europe Standard Time` (Windows name)
  on masters and the single instance, empty on occurrences. `iCalUId` on
  masters and singles only (4 of 12), `seriesMasterId` on occurrences
  (8 of 12). Start shape with the Prefer header:
  `{"dateTime":"2022-04-12T08:00:00.0000000","timeZone":"UTC"}` (seven
  fractional digits). Event keys include `uid`, `occurrenceId`,
  `transactionId`, `onlineMeeting`, `onlineMeetingUrl`, `onlineMeetingProvider`,
  `isOnlineMeeting`, `locations`, `hideAttendees`, `isDraft`, `webLink`,
  `categories`, `importance`, `sensitivity`, `isCancelled`.
- **Per-calendar delta works on v1.0**: `GET /me/calendars/{id}/calendarView/delta`
  answered 200 with a `deltaLink` for every non-default calendar
  (writable, holidays, birthdays). D14's list fallback stays as insurance.
- Series master (`GET /me/events/{id}`): `recurrence.pattern = {type:
  weekly, interval: 1, daysOfWeek: [tuesday], firstDayOfWeek: monday,
  index: first, dayOfMonth: 0, month: 0}`, `recurrence.range = {type:
  noEnd, startDate: 2022-04-12, endDate: 0001-01-01,
  numberOfOccurrences: 0, recurrenceTimeZone: Central Europe Standard
  Time}`; the master's `start` comes in UTC with the Prefer header while
  `recurrenceTimeZone` is the Windows zone (`tz` maps it).
- `POST /me/calendar/getSchedule` (own address, 1 day, 30-min interval):
  works on this work account — `availabilityView` of 48 characters,
  `scheduleItems`, `workingHours {daysOfWeek, startTime, endTime,
  timeZone}`.
- Mail: none of the last 50 messages was an `eventMessageRequest`, so the
  MIME shape of a meeting request (text/calendar part or not) is still
  unverified; D17 keeps both paths. No all-day event lay in the window
  either: the all-day shape is to be verified by T7b's fake from the
  documentation (midnight-to-midnight with `isAllDay`).

**T7b implementation notes (2026-10-16, `graph/calendar*.go`, `limiter.go`):**

- Calendar kind is always `events`: Graph has no property that tells the
  birthdays or a holidays calendar apart reliably (both are only
  `canEdit: false`, names are localised), so they are read-only `events`
  calendars (`access: read`). A writable calendar whose `owner` is not
  one of the account's addresses is `access: write` with `owner` set.
  Colour: `hexColor` when it is `#rrggbb`, else a fixed palette for the
  `color` name, `auto`/`maxColor`/`""` → none. `changeKey` of a calendar
  is not used as `CTag` (it changes with properties, not events).
- `tz` maps `Central Europe Standard Time` to CLDR's territory-001 zone
  `Europe/Budapest` (same rules as Prague); `Event.Zone` holds that.
- Sync: an occurrence or exception in an incremental delta, or a
  `@removed` occurrence id known to belong to a series, refetches the
  series (`GET /me/events/{id}` + `/instances` in the window); the first
  pass builds series from the delta's own items and fetches a master only
  when the delta's copy lacks `recurrence` or its version differs from
  the stored one. A stored deltaLink is followed only on the client's own
  host. List mode compares by `@odata.etag`. Both need what is stored:
  `CalendarProviderOptions.Known` (object remote id → ETag, occurrence
  remote id → object remote id); without it a list pass upserts all with
  `Full`. Cancelled instances (`isCancelled`) are not in `Occurrences`
  (as `calendar.Occurrences` does for CalDAV). Object `ETag` is the
  master's `@odata.etag`; exceptions are overrides, every Graph instance
  is an `Occurrence` with its `RemoteID`.
- `Range` (frozen signature) returns occurrences only; `RangeObjects`
  returns them with their events (a series without its master, every
  instance an override) for the core.
- Writes: a failed precondition (412, or 409 `ErrorIrresolvableConflict`)
  is `conflict` with the current object in `WriteResult.Objects`; the
  core builds `ConflictEventData` from it. An instance write (`this`)
  accepts the series' or the instance's version and sends the instance's
  own `If-Match`; the action POSTs (`accept`…, `cancel`, `decline`) carry
  `If-Match` too. With attachments and attendees the event is created
  without attendees, the files added, then the attendees patched (the
  invitations then carry them); a failed attachment deletes the new event.
  A typed video link (`videoCall.mode: link`) goes into the body text
  (Graph has no field for it). An attendee's update sends only reminder,
  `showAs` and `sensitivity`.
- Personal accounts: `CalendarProviderOptions.Personal` (core sets it
  from the token's tenant id, `graph.IsPersonalTenant`,
  `PersonalTenantID`); besides, a `getSchedule` refused as a whole (400,
  403 other than access denied, 404, 405, 501) answers `known: false` for
  everyone and turns `features.freeBusy` off for the provider's life.
- All-day (owner's live test): `isAllDay` true → dates, the start/end
  instant read in `originalStartTimeZone`/`originalEndTimeZone`
  (`tz.Resolve`, UTC when unknown) and rounded to the nearest midnight
  (midnight UTC and the zone's midnight in UTC both give the right date);
  instances of an all-day series are all-day; an item without `isAllDay`
  that runs midnight to midnight is all-day. Location = `displayName`
  split at `;` plus `locations[]`, each part once (case-insensitive).
  In a calendar the user does not own (`access` ≠ owner or another
  owner), `responseStatus`, `isOrganizer` and `responseRequested` are the
  owner's: `myResponse` comes only from an attendee who is one of `Me`,
  `IAmOrganizer` from the organiser's address.
- Limiter (D13): `graph.NewLimiter(4)` shared through `Options.Limiter`;
  mail requests and calendar writes, free/busy and `Range` run at
  `PriorityHigh`, calendar passes and `Calendars` at `PriorityLow`.

### 3.7 The engine (`internal/calsync`)

```go
// Provider is one account's calendar source. Implementations: caldav,
// graph; later google. Every method may block on the network and must
// honour ctx. Errors are *api.Error.
type Provider interface {
	// Calendars lists the account's calendars as the server has them.
	Calendars(ctx context.Context) ([]RemoteCalendar, error)
	// Sync brings one calendar up to date within window, given the
	// stored state (token, ctag, known resources); it calls sink for
	// every change, in order, and returns the new state.
	Sync(ctx context.Context, cal store.Calendar, window Window, sink Sink) (SyncState, error)
	// Range fetches occurrences of [from, to) without storing anything
	// (event.list outside the window).
	Range(ctx context.Context, cal store.Calendar, from, to time.Time) ([]calendar.Occurrence, error)
	Create(ctx context.Context, cal store.Calendar, ev calendar.Event, opts WriteOptions) (WriteResult, error)
	Update(ctx context.Context, cal store.Calendar, ev store.Event, change calendar.Change, opts WriteOptions) (WriteResult, error)
	Delete(ctx context.Context, cal store.Calendar, ev store.Event, scope calendar.Scope, opts WriteOptions) (WriteResult, error)
	Respond(ctx context.Context, cal store.Calendar, ev store.Event, r calendar.Response, opts WriteOptions) (WriteResult, error)
	// FreeBusy of other people; ErrUnsupported when the source has none.
	FreeBusy(ctx context.Context, attendees []string, from, to time.Time, step time.Duration) ([]calendar.Availability, error)
	// Features are what the source can do (api.CalendarFeatures).
	Features() api.CalendarFeatures
}
```

(The exact types are written by T1 into `calsync/provider.go` and frozen;
`Sink` receives `Upsert(calendar.Object)` and `Remove(remoteID)`.)

- One syncer per enabled account with calendars (graph with
  `calendar.sync`, caldav), started and stopped where the mail
  supervisors are (account add/remove/enable/update, `StartSync`,
  re-sign-in, `Reload`). For `caldav` accounts the engine is also the
  kind's `SyncSupervisor` in `core/dispatch.go` (`api.AccountCalDAV` entry,
  `kindOrder` gains it), so `sync.status`, `account.list` `state` and the
  status bar work as for any account; a Graph account's calendar state is
  separate (`CalendarSyncState`, §4.3) and never touches its mail state.
- Cadence: Graph default calendar every 2 min, other calendars every 10
  min; CalDAV every 5 min (the ctag check makes it cheap); a full
  reconcile once a day per calendar; `calendar.sync` asks for a pass now;
  writes trigger the calendar's pass after they answer. Backoff 30 s → 1 h
  on errors, the next pass waits on `offline`.
- Materialisation: after a CalDAV object or a Graph master changes, its
  occurrences in the window are rebuilt in the same transaction; an
  account's window move rebuilds lazily (only what entered or left).
- Notifications: `notify.calendarChanged` with the calendars and the
  changed time span (coalesced 1 s per account), `notify.calendarState`
  on every state change (progress at most every 500 ms),
  `notify.invitationsChanged` when unanswered invitations changed.
- Alerts (D12): after any change of `cal_events`/`cal_occurrences` of the
  next 7 days the scheduler recomputes `cal_alerts` (keeping state of
  unchanged keys) and arms one timer; due → `state = fired`,
  `notify.eventAlert {state: "due"}`; an alert found due late is fired if
  it is at most 15 minutes late and its occurrence has not ended, else
  marked fired silently; snooze sets `snoozed_until` and re-arms; dismiss
  sends `notify.eventAlert {state: "dismissed"}` so other clients withdraw
  theirs. Declined events and paused accounts have no alerts; all-day
  events alert at the user's working-day start minus the alarm.

**As built (T8).** Where the engine is more precise than the text above:

- *Surface.* `calsync.New(Options{Store, Providers, Notifier, Clock,
  Location, Preferences, Log, Backoff, ExpandKey})`; `Run`, `Start`,
  `Stop`, `Restart`, `Reload`, `SyncNow(account, calendar)`,
  `Touch(calendar)`, `CalendarState(s)`, `Provider(account)`,
  `Window(account)`, `ApplyWrite(ctx, account, calendar, WriteResult)`
  (a write's result stored like a pass, then notify, alerts and Touch),
  `SnoozeAlert`, `DismissAlert`, `NetworkChanged`. `Mail()` is the
  `core.SyncSupervisor` of the `caldav` kind: lifecycle calls go to the
  engine, `Trigger`/`State`/`States` see caldav accounts only (calendar
  state through `MailState`); a Graph account's calendar never reaches
  `notify.syncState` or `notify.authRequired`. A second concurrent `Run`
  only waits, so core may run it both as the kind's supervisor and
  directly. `HasCalendars`: enabled caldav, or enabled graph with
  `calendar.sync`.
- *Offline.* The daemon has no network monitor; offline waits on the
  backoff (30 s doubling to 1 h, ±10 %) and `NetworkChanged()` wakes the
  offline syncers when core learns of one. Auth and consent errors drop
  the provider, which the factory makes again on the next attempt.
- *Errors of one calendar* (anything but network, sign-in, consent) are
  that calendar's `error_code` and backoff; the other calendars go on and
  the account's state is `error` until a pass succeeds.
- *Storing.* Rows are converted by `StoreEvents`/`ObjectOf` (exported
  for core); keys are stored as `store.FormatRecurrenceID` (`StoredKey`;
  `ModelKey` is the model's form, used for `EventAlert.recurrenceId`). An
  object without an occurrence in the window is not stored (its rows go;
  CalDAV resource `outside`). Removal of what a pass did not name happens
  only when the provider says `Full`; skipped resources stay the
  provider's. `Object` has no change key, so `cal_events.change_key`
  stays empty (Graph uses the ETag); `RemoteCalendar.CTag` and
  `OnlineMeetingProviders` have no column and are not stored.
- *Which calendars the daemon expands* is read from `sync_mode`: Graph
  calendars store `delta`/`list` (from `DeltaMode`), every other one
  `daemon`. A Graph calendar's `window_from/to` are the window its token
  was made for (changed only with a new chain), a CalDAV calendar's the
  current window (the provider re-fetches `outside` resources when it
  differs).
- *Window move* (the first pass of a new local day): occurrences outside
  go, the daemon-expanded masters are re-expanded from `cal_events`
  (instances entering at the far edge), then `TrimEvents`; trimmed CalDAV
  resources become `outside`.
- *`calendar.expand`* is `<recur.Version>.<calendar.ExpandVersion>:<Go
  version>`: Go exposes no tzdata version, and the embedded tzdata comes
  with the Go release. The rebuild runs in `Run` before any syncer starts.
- *Alerts* are recomputed at once after a pass or write that changed
  something (not after the 1 s coalescing), on Start/Stop/Reload and
  hourly; they read occurrences up to 7 days + `MaxAlertMinutes` ahead
  so that a long alarm of a later event is not missed, of running
  accounts and visible calendars only (a hidden calendar has no alerts).
  An alert whose occurrence has ended has no row.
- *Disabled calendars* (`CalendarConfig.disabledCalendars`) keep their
  `cal_calendars` row, upserted from `Calendars()` like any other (user
  columns untouched), with `sync_mode = "disabled"`
  (`calsync.SyncModeDisabled`, `calsync.Disabled(row)`; `calendar.list`
  reads it for `Calendar.disabled`) and no events, occurrences, resources
  or alerts (`store.DeleteCalendarContents` when it becomes disabled);
  they are never synchronised and `ApplyWrite` refuses them
  (`invalidArgument`). Enabled again, the sync state is reset and the
  next pass synchronises it from scratch. A calendar gone from the server
  is deleted whole.
- *Provider data version.* A provider may implement `calsync.Versioned`
  (`DataVersion() int`): the version of its mapping of server data onto
  the model. The engine records the version it last synchronised an
  account with in meta `calendar.provider.<accountID>`
  (`calsync.MetaProviderVersion`); when the provider's differs, or none
  is recorded, the first pass after the provider is made clears every
  active calendar's token, ctag, sync mode and window, records the new
  version and runs a full pass, so every object is sent again and
  replaces its stored rows (user columns kept). An unversioned provider
  keeps its tokens. Core deletes the meta key with the account.

### 3.8 Core service and invitations

- `core/accounts_caldav.go`: validation of kind `caldav` in `account.add`
  / `account.update` / `account.test` (the rules of §4.1), realm (`store.RealmOf`
  extended: normalised server host[:port] + the path of `serverUrl`, as §4.3
  says; T5 implemented it so), keyring, and
  the two discovery methods.
- `core/calendar_service.go`, `core/event_service.go`: the methods of
  §4.4–4.6 over the store and the providers; every string from a server
  or an `.ics` is cleaned (`calendar/text.go`) before it is stored.
- `core/invitation_service.go`: the `Stored` hook (`afterMailStored` gains
  one call `b.noteInvitation(ctx, accountID, messageID)`): reads the
  message's attachments list, takes parts of type `text/calendar`,
  `application/ics`, or a `.ics` file name (≤ 2 parts, each ≤
  `MaxICSBytes`) through `store.WithMessageRaw` + `mime.ExtractPart`, parses
  with `imip.ParseMessage`, writes `cal_invitations`; for `reply` it applies
  D10; for `cancel` it marks the stored event's instance cancelled
  only when the event came from this invitation chain and is not on a
  server that processes cancellations itself (Graph, CalDAV with server
  scheduling); it never deletes an event (the user does, `invitation.applyCancel`).
  Messages in folders of role `junk` and `trash` get a row (the pill) but
  `canRespond: false` and never a tentative occurrence.
- `invitation.respond` (D18): resolves the linked event by UID; through
  the provider (Graph respond, CalDAV PUT of `PARTSTAT`) or by iMIP
  (`calendar_imip.go`: `smtp.BuildMessage` + `store.EnqueueOutbox` +
  `Delivery.Wake`, as `unsubscribeMailto` does), then `cal_responses`, then
  the `afterAnswer` move (`message.move` of the message to the account's
  archive folder when `archive`).
- `core/calendar_backfill.go`: the D17 scan in `core.Maintain`, 2000
  messages per step like the board backfill.
- Wiring in `core/backend.go`: the calsync supervisor, the providers per
  kind (`graph` → `graph.NewCalendarProvider` sharing the account's
  limiter and token, `caldav` → `caldav.NewProvider`), the Notifier
  extension `api.CalendarNotifier` (optional, asserted like
  `BoardNotifier`).

**As built (T9a).** Where core is more precise than the text above and §4.3:

- *Trusted origins.* The password goes to the server's origin and to the
  origins of the principal and home set only when they are https on the
  same site (registrable domain, `publicsuffix`) as `serverUrl`; IP
  literals and loopback hosts only as the server itself
  (`calDAVOrigin`). `principalUrl`/`homeSetUrl` failing that rule are
  `invalidArgument`. Listing (`listCalDAV`) retries after
  `caldav.ErrOtherOrigin` with the origins the server names (at most two
  rounds), so iCloud's `pNN-caldav` home set works without the client
  passing it; a home set on another site is `serverError` and gets no
  request.
- *Pin.* Discovery and the account's client use per-host transports
  (`calDAVTransport`): the pin replaces verification only for the typed
  URL's (discovery) or `serverUrl`'s (account) host; every other host is
  verified normally.
- *account.add* fills `email`, `principalUrl` and `homeSetUrl` from a
  signed-in listing when the client left `email` or `homeSetUrl` empty
  (else `email` = `username` when it is an address); `account.update`
  keeps them from the stored account while server and user name are the
  same, and a new server or user name needs the password again. The
  stored password serves `account.test`/`account.listCalDAVCalendars`
  only for a caldav account of the same realm.
- *Message methods* of a caldav account answer `invalidArgument` through
  a wrapper of the message service (`mailOnlyMessages`); `folder.list`
  is empty because the account has no folders.
- *Graph.* The calendar runs beside the mail behind the graph kind
  (`graphWithCalendar`: start, stop, restart, reload with the mail; states
  and triggers the mail's). The consent check is the provider factory's
  (`CalendarConsent`, GOA `CalendarDisabled`), so `account.update` never
  fails on it and the state becomes `consentRequired`. `Personal` comes
  from the `tid` claim of a JWT access token; an opaque token reads as
  not personal (the provider then learns from `getSchedule`). One
  `graph.NewLimiter(4)` per account (`graphLimiter`) goes to the mail
  syncer (`SupervisorDeps.Limiter`), the outbox's client and the
  calendar's.
- *Addresses* of a caldav provider are the account's address and the
  user's addresses of every enabled account (`userAddresses`); the
  provider therefore does not add the server's
  `calendar-user-address-set` to them. The ORGANIZER is the account's
  `email` and `displayName`.
- *Cache deletion* (D23, `dropCalendarCache`): calendar sync off, a new
  server or user name, or a change of kind deletes all rows; calendars
  newly in `disabledCalendars` lose theirs at once (the engine would at
  its next pass); `notify.calendarChanged` follows.
- *Local zone.* `malachid` names the zone for `tz.SetPreferred` from
  `MALACHI_TIME_ZONE` (the macOS and Windows clients are to set it), then
  `TZ`, then the `zoneinfo/` tail of `/etc/localtime`'s link target.

**As built (T9c).** Where the invitation service
(`core/invitation_service.go`, `core/calendar_backfill.go`) is more
precise than the text above and §4.6:

- *One row per message.* Of the at most two calendar parts the first
  that reads is the message's invitation (a second part with another UID
  is not stored); both unreadable, or one over `MaxICSBytes`, is the
  `invalid` row. The MIME walk never descends into an attached
  `message/rfc822`, so a forwarded invitation has no row.
- *D10.* Applied in the store only (the next pass brings the server's
  view), only when the REPLY has exactly one `ATTENDEE`, the From is that
  attendee, a stored event of the UID (any calendar account) has the
  attendee and no higher `SEQUENCE`; `replyApplied` is stored in
  `card_json` at detection. Junk and trash never apply a REPLY or a
  CANCEL.
- *CANCEL.* Marked (`status: cancelled`, never deleted) on the copy the
  daemon stored for the invitation (`cal_responses.event_id`, via `imip`
  or `none`), and on events of the same organiser in CalDAV accounts the
  daemon schedules for (`scheduling: client`, or a running provider that
  reports no server scheduling). An instance CANCEL (or REPLY) marks only
  a stored override of that instance; a series instance without an
  override is left alone.
- *The linked event* (`eventId`): the daemon's copy, else an event of the
  UID in the invitation's account, else in another calendar account —
  only one the user does not organise and whose `ORGANIZER` is the
  invitation's, so that an invitation reusing the UID of the user's own
  event answers nothing. A Graph copy is linked through `cal_responses`
  only (Graph gives it its own `iCalUId`). Any linked event that is not
  the copy is answered through its provider and gives `targets` = its
  calendar with `fixedTarget`.
- *targets* for a Graph mailbox without a stored event are empty (the
  answer only, by iMIP from the mailbox) until Exchange's event is
  synced. Otherwise: the writable event calendars of every calendar
  account, the copy's calendar first, then the default of a CalDAV
  account whose `mailAccountId` is the invitation's account, then other
  defaults; at most 50.
- *canRespond* is false also in Sent, Drafts and the outbox, for an
  invitation the user organises, without an organiser, when the user is
  not on the list (no link), and in any state other than `unanswered` /
  `answered`; `replyVia` is `none` when the account cannot compose.
- *The day strip* is the working hours of the invitation's start day in
  the daemon's zone, widened to the invitation but kept within that day;
  `busy` are the spans of busy occurrences (not merged, each with its
  `calendarId`, clipped, ≤ 100). Conflicts and busy leave out the
  invitation's own UID.
- *An unreadable part* still gives `Message.invitation` (it shadows the
  summary's key): a card with only the summary (`state: invalid`), empty
  lists, `replyVia: none`.
- *afterAnswer* is read from the invitation account's `calendar` block,
  else from the calendar account the answer went through (an IMAP
  account has no `calendar` block, §4.3); the archive folder is the
  account's `archive` role; no move without one.
- *invitation.respond* stores the copy before it queues the REPLY (a
  failed PUT sends nothing). The CalDAV copy goes through the provider's
  `Create` for a foreign organiser, which always marks the `ORGANIZER`
  `SCHEDULE-AGENT=CLIENT`; an answer changed later updates the copy's
  PARTSTAT through `Respond` without notifying, a decline or another
  target calendar deletes it. The REPLY is threaded under the invitation
  (`In-Reply-To`/`References`).
- *notify.invitationsChanged* is sent (gathered 1 s) whenever a request or
  a cancel row is written or removed, after an answer and after an
  `applyCancel`, not only when the unanswered set provably changed.
  `notify.newMessage` carries the summary because the mail syncers'
  `summaryOf` (imap, graph) copy `store.Message.Invitation`, which
  `GetMessage` fills after the hook wrote the row; `message.list` copies
  it in `toAPISummary`.
- *invitation.list* leaves out invalid rows and rows in junk, trash,
  Sent, Drafts and the outbox (after the store's choice of the newest
  message per UID).
- *Backfill* reads messages whose `internal_date` (else `date`) is within
  `offlineDays`; a row an older parser wrote that cannot be read again is
  deleted rather than retried for ever.

### 3.9 Security

The full threat notes go into `docs/security.md` as a new §4.3
"Calendar data (iCalendar, CalDAV, Graph events)"; the rules:

- **Every `.ics`, multistatus and Graph event is hostile input**: the
  limits of §3.2, §3.3 and §3.5, fuzz targets, `testdata/calendar` and
  `testdata/caldav` (§3.10). A parse failure withholds the card
  (`invitation.state: "invalid"`, error 1507 where a method needs the
  data) and never falls back to showing raw text of the part.
- **No HTML to the UI.** Notes are plain text (D11). `X-ALT-DESC`,
  `STYLED-DESCRIPTION` (RFC 9073), HTML bodies of Graph events go through
  the sanitiser and leave as text only. The MCP bridge gets the same
  text, fenced.
- **Strings**: every string is cleaned (control, bidi, invisible and line
  separator characters removed except `\n` in notes; length caps:
  title 1024 B, location 1024 B, names 256 B, notes 32 KiB) and shown by
  clients only as plain text (`stringValue`, `SetUseMarkup(false)`,
  `TextBlock.Text`).
- **Links**: only `https:` URLs with a host, no credentials, ASCII host
  (IDN as punycode), ≤ 2048 B, become `links` / `videoCall`; everything
  else (`http:`, `javascript:`, `data:`, `file:`, `tel:`, `webcal:`) stays
  text. `videoCall.provider` is set only for hosts of a fixed list
  (`teams.microsoft.com`, `teams.live.com`, `*.zoom.us`,
  `meet.google.com`, `*.webex.com`, `meet.jit.si`, `whereby.com`);
  any other https URL of `CONFERENCE`/`X-GOOGLE-CONFERENCE`/
  `X-MICROSOFT-SKYPETEAMSMEETINGURL`/Graph `joinUrl` is
  `provider: "other"`; a link in location or notes is a video call only
  on a host of the list. Clients open every link through the existing
  "Open this link?" confirmation that shows the host; a notification's
  Join opens that confirmation too, never the URL directly.
- **No fetching**: the daemon never fetches a URL from an `.ics`
  (`ATTACH`, `URL`, `IMAGE`, `CONFERENCE`, `TZURL`, `X-*`), never contacts a
  non-`mailto:` `CAL-ADDRESS`, never executes alarm actions other than
  showing a notification.
- **iMIP**: replies only to the stored `ORGANIZER` (D10); the card shows
  that address and a warning line when its domain differs from the
  `From` domain (`organizerMismatch`); received REPLYs applied only under
  D10; no automatic reply ever (the user answers); declining offers
  "Decline Without Reply" (`sendResponse: false`) so that spam
  invitations do not confirm the address.
- **CalDAV credentials and TLS**: the password goes only to the origin of
  the configured server (redirect rule §3.5); the certificate pin of
  `CalDAVConfig.certificateSha256` makes CalDAV the second kind of HTTP
  client with a pin (security.md §7 says "Graph and HTTP clients never
  have a pin": amended for CalDAV only); `tlsError` carries
  `TLSErrorData` so the clients' certificate-trust flow (`certtrust`)
  works unchanged; no generic "ignore certificate".
- **Graph scopes**: only on the user's switch (D15).
- **MCP**: every event string through `clean()` and the nonce fence (the
  organiser writes them); `create_event` behind `--allow-modify`, and with
  attendees also `--allow-send` (invitations are outgoing mail).

### 3.10 Test corpus

`backend/testdata/calendar/` (each file with a comment line `X-MALACHI-TEST:`
saying what it tests): `folded-utf8-split.ics`, `bare-lf.ics`,
`bare-cr.ics`, `nul-bytes.ics`, `invalid-utf8.ics`, `bidi-summary.ics`,
`control-chars.ics`, `long-line-1mib.ics`, `too-big.ics` (generated in the
test, not committed), `deep-nesting.ics`, `component-flood.ics`,
`property-flood.ics`, `param-flood.ics`, `param-quote-injection.ics`,
`missing-end.ics`, `mismatched-end.ics`, `no-vcalendar.ics`,
`two-vcalendars.ics`, `mixed-uids.ics`, `empty.ics`, `rrule-secondly.ics`,
`rrule-minutely.ics`, `rrule-impossible-feb30.ics`, `rrule-count-huge.ics`,
`rrule-interval-zero.ics`, `rrule-until-year-9999.ics`,
`rrule-byday-flood.ics`, `rrule-bysetpos-out-of-range.ics`,
`rrule-repeated-parts.ics`, `dtstart-year-0001.ics`,
`dtstart-year-9999.ics`, `end-before-start.ics`, `duration-negative.ics`,
`duration-huge.ics`, `exdate-flood.ics`, `rdate-flood.ics`,
`override-flood.ics`, `override-without-master.ics`,
`recurrence-id-tz-mismatch.ics`, `tzid-unknown.ics`, `tzid-windows.ics`,
`tzid-mozilla-prefix.ics`, `vtimezone-hostile.ics` (absurd offsets, RRULE
inside STANDARD), `vtimezone-missing.ics`, `floating-and-utc-mixed.ics`,
`all-day-datetime-mixed.ics`, `attendee-flood.ics`,
`attendee-non-mailto.ics`, `organizer-missing.ics`,
`organizer-spoofed.ics`, `sequence-overflow.ics`, `x-alt-desc-html.ics`,
`styled-description.ics`, `attach-uri.ics`, `attach-binary-huge.ics`,
`valarm-email-procedure.ics`, `valarm-absolute-past.ics`,
`url-javascript.ics`, `conference-http.ics`, `location-with-url.ics`,
`method-unknown.ics`, `method-cancel-unknown-uid.ics`,
`method-reply-spoofed.ics`, `outlook-request.ics`, `google-request.ics`,
`icloud-request.ics`, `teams-request.ics` (real-shape samples with
invented data).

`backend/testdata/mime/` gains `invite-outlook.eml`, `invite-google.eml`,
`invite-update.eml`, `invite-cancel.eml`, `invite-reply.eml`,
`invite-reply-from-mismatch.eml`, `invite-two-calendar-parts.eml`,
`invite-calendar-as-attachment.eml`, `invite-in-forwarded-message.eml`
(the calendar part inside a `message/rfc822` is not the message's
invitation: `internal/mime` lists the rfc822 as one leaf); the oversized
calendar part (> 1 MiB) is generated in `imip` tests, not committed.

`backend/testdata/caldav/`: `multistatus-deep.xml`, `multistatus-flood.xml`,
`multistatus-href-other-host.xml`, `multistatus-garbage.xml`,
`doctype-entities.xml`, `propfind-no-namespaces.xml`,
`sync-collection-truncated.xml` (507), `calendar-color-alpha.xml`.

---

## 4. The API contract

Everything here is a **compatible addition** to protocol 2 (docs/api.md
§6): new methods, notifications, error codes, optional fields, one new
`AccountKind` and one new `AccountCapability`. `api.ProtocolVersion` stays
2. A client that does not know a field ignores it; one that does not know
kind `caldav` must not show such an account in Mail (§4.3).

Task T1 writes this section into `docs/api.md` (new §4.14 `calendar`,
§4.15 `event`, §4.16 `invitation`, additions to §2, §3, §4.1, §5 and
the §7 changelog) and into Go (`backend/pkg/api/calendar.go`
new; `types.go`, `errors.go`, `methods.go`, `service.go`), in one commit
(rule 5). `TestDocsCoverAllMethods` then covers the 21 methods and 4
notifications below, and every new code has its `| code | name |` row.

### 4.1 Error codes

Appended within their groups (never renumbered):

| Code | Name | Meaning |
|---|---|---|
| 1006 | readOnly | the calendar or event cannot be changed by the user (a read-only or shared calendar, a birthdays/holidays calendar, an event the user does not organise for a change only the organiser may make) |
| 1007 | invitationsUnavailable | the event has attendees, but neither the server schedules nor a mail account can send the invitations (`CalDAVConfig.mailAccountId` empty or not a compose account) |
| 1107 | calendarNotFound | no calendar with that id (removed, its account gone or its sync switched off) |
| 1108 | eventNotFound | no stored event with that id, or `recurrenceId` names no occurrence of it |
| 1204 | calendarConsentRequired | the account's sign-in does not allow calendar access (Microsoft scope missing, consent withdrawn, GOA calendar disabled); `data` = `{ "accountId", "signIn": bool }` — `signIn` true when `account.oauthStart` with `calendar: true` can fix it (source `daemon`), false for GOA (the desktop's Online Accounts settings) |
| 1306 | eventGone | the server no longer has the event (deleted or moved elsewhere); the next pass removes the local copy |
| 1507 | malformedCalendar | an iCalendar object or a server's calendar answer could not be read even leniently; the data is withheld |
| 1508 | recurrenceUnsupported | the event's recurrence rule is outside what the daemon expands or edits (§3.3); the event shows its first instance only and may be edited with scope `all` only |

`conflict` (1002) gains a meaning: an event write whose `etag` is not the
server's current one; `data` = `{ "event": Event }` with the current event
(as `event.get` returns it). `offline` (1300), `networkError`,
`serverError`, `serverTimeout`, `tlsError` (with `TLSErrorData` for CalDAV
endpoints), `authRequired`, `authFailed`, `keyringError` keep their
meanings.

### 4.2 Common types

```jsonc
Date          "2026-10-14"                    // calendar date, no zone
LocalDateTime "2026-10-14T10:00:00"           // wall-clock time, with a separate IANA zone
TimeZone      "Europe/Prague"                 // IANA name; "" = floating

CalendarSource "graph" | "caldav" | "google"  // open enum; "google" reserved, not produced yet
CalendarAccess "owner" | "write" | "read"
CalendarKind   "events" | "birthdays" | "holidays" | "other"   // open enum
Response       "needsAction" | "accepted" | "tentative" | "declined" | "delegated"
ShowAs         "free" | "tentative" | "busy" | "oof" | "workingElsewhere" | "unknown"
EventStatus    "confirmed" | "tentative" | "cancelled"
Scope          "this" | "thisAndFuture" | "all"

Calendar {
  "id": "cal_…", "accountId": "acc_1", "name": "Calendar",
  "remoteId": "AAMkAD…" | "/remote.php/dav/calendars/jana/personal/",   // Graph calendar id or CalDAV href: the key of CalendarConfig
  "disabled": true (opt),             // in the account's disabledCalendars: listed, no events stored
  "color": "#1f6feb",                 // the user's colour if set, else the server's, else a palette colour by id
  "serverColor": "#1f6feb" (opt), "userColor": "#e5534b" (opt),
  "visible": true,                    // shown in the views (calendar.update)
  "access": CalendarAccess, "kind": CalendarKind,
  "owner": Address (opt),             // a calendar shared with the user
  "isDefault": true (opt),            // the account's default calendar for new events (CalendarConfig.defaultCalendar, else the server's)
  "timeZone": TimeZone (opt),
  "onlineMeetingProviders": ["teamsForBusiness"] (opt)   // Graph allowedOnlineMeetingProviders; open enum
}

CalendarFeatures {                    // what the account's source can do
  "freeBusy": bool,                   // calendar.freeBusy can ask about other people (Graph work/school)
  "proposeNewTime": bool,             // some events may offer it (Graph)
  "invitations": "server" | "mail" | "none",   // who delivers invitations and replies of its events
  "mailAccountId": "acc_2" (opt),     // invitations "mail": the account they leave from
  "onlineMeetings": bool              // "Add video call" can create a meeting (Graph)
}

CalendarSyncState {
  "accountId": "acc_1",
  "status": "idle|syncing|offline|authRequired|consentRequired|error|disabled",
  "progress": 42,                     // 0–100, -1 unknown
  "lastSync": Time (opt), "error": Error (opt)
}

CalendarAccount {                     // an account with the calendar capability (§4.3)
  "accountId": "acc_1", "source": CalendarSource,
  "sync": true,                       // CalendarConfig.sync (always true for caldav)
  "state": CalendarSyncState, "features": CalendarFeatures,
  "window": { "from": Date, "to": Date },   // what is stored (§2.3)
  "calendars": [Calendar]             // server order; empty while sync is off
}

Recurrence {
  "frequency": "daily|weekly|monthly|yearly",
  "interval": 1,                      // 1–99 in input; up to 1000 read
  "byDay": ["MO", "WE"] (opt),        // weekly: the days; monthly/yearly: with an ordinal, "2TU", "-1FR"
  "byMonthDay": [14] (opt),           // monthly/yearly: -31..31, not 0
  "byMonth": [10] (opt),              // yearly: 1–12
  "until": Date (opt) | "count": 10 (opt),   // at most one; neither = no end
  "weekStart": "MO" (opt),
  "complex": true (opt),              // read only: the rule has more than this shape (hourly, BYSETPOS
                                      // combinations, several ordinals, RDATE…); shown as "Custom", edited
                                      // only with scope "all" and kept unless replaced
  "exceptions": 3 (opt)               // read only: modified or removed instances in the window
}

Attendee {
  "name": "Jana Nováková" (opt), "address": "jana@example.org",
  "role": "required|optional|chair|nonParticipant",
  "response": Response, "type": "person|room|resource|group|unknown",
  "me": true (opt), "organizer": true (opt)
}

VideoCall { "url": "https://teams.microsoft.com/l/meetup-join/…",
            "provider": "teams|zoom|meet|webex|jitsi|whereby|other", "host": "teams.microsoft.com" }

Occurrence {                          // one instance in a view (event.list, event.search)
  "eventId": "ev_…" (opt),            // absent for an invitation not in any calendar
  "recurrenceId": Time | Date (opt),  // instance of a series: its original start
  "seriesId": "ev_…" (opt),           // the master of a series instance
  "calendarId": "cal_…" (opt), "accountId": "acc_1",
  "title": "Standup",                 // "" when private and not the user's
  "start": Time, "end": Time,         // timed: UTC instants; all-day: absent
  "startDate": Date, "endDate": Date, // all-day only; endDate exclusive
  "allDay": bool, "floating": bool (opt), "timeZone": TimeZone (opt),
  "location": "Room B" (opt),
  "status": EventStatus, "showAs": ShowAs,
  "myResponse": Response (opt),       // absent: the user is not an attendee (own event, no attendees)
  "organizer": bool (opt),            // the user organises it
  "attendeeCount": 4 (opt),
  "recurring": bool (opt), "videoCall": bool (opt), "private": bool (opt),
  "alert": bool (opt),                // an alert is set
  "readOnly": bool (opt),
  "invitation": { "accountId", "messageId" } (opt)   // an invitation shown tentatively (not in a calendar)
}

Event = Occurrence + {                // event.get, event.create/update results
  "etag": "opaque",                   // pass back on update/delete/respond
  "notes": "plain text" (opt), "links": ["https://…"] (opt),
  "videoCallLink": VideoCall (opt),
  "organizerAddress": Address (opt),
  "attendees": [Attendee] (opt), "attendeesTruncated": bool (opt),
  "recurrence": Recurrence (opt),     // of the series (on any instance)
  "alerts": [15] (opt),               // minutes before start
  "responseRequested": bool (opt),
  "canEdit": bool, "canDelete": bool, "canRespond": bool, "canProposeNewTime": bool,
  "removeIsDelete": bool,             // Remove from Calendar deletes the event for everyone (the user organises it)
  "invitationMessages": [{ "accountId", "messageId" }] (opt),   // Show Invitation in Mail
  "created": Time (opt), "updated": Time (opt)
}

EventInput {                          // event.create, event.update
  "title": "…",                       // ≤ 1024 bytes after cleaning; may be empty
  "allDay": bool,
  "start": LocalDateTime, "end": LocalDateTime, "timeZone": TimeZone,   // timed (timeZone required, "" refused)
  "startDate": Date, "endDate": Date,                                    // all-day (endDate exclusive, > startDate)
  "location": "…" (opt),              // ≤ 1024 bytes
  "notes": "…" (opt),                 // plain text, ≤ 32 KiB
  "recurrence": Recurrence (opt),     // without "complex"/"exceptions"; null/absent = none
  "keepRecurrence": bool (opt),       // update of a complex rule: keep it as it is (recurrence ignored)
  "alerts": [15] (opt),               // ≤ 4 values 0–40320; absent = the default alert (calendar.preferences); [] = none
  "showAs": ShowAs (opt), "private": bool (opt),
  "attendees": [{ "name" (opt), "address", "role": "required|optional" (opt) }] (opt),   // ≤ 100
  "videoCall": { "mode": "none|create|link", "provider": "teamsForBusiness" (create), "url": "https://…" (link) } (opt),
  "attachments": ["att_…"] (opt)      // attachment.import ids (D21), ≤ 10, each ≤ 3 MiB
}

MailText { "subject": "…", "text": "…" }   // a client's localised iMIP subject and body (D9); both ≤ 4 KiB

Availability {                        // calendar.freeBusy
  "address": "jana@example.org",
  "known": bool,                      // false: no free/busy for this person
  "busy": [{ "start": Time, "end": Time, "showAs": ShowAs }]
}

EventAlert {
  "alertId": "al_…", "state": "due|dismissed",
  "eventId": "ev_…", "recurrenceId": Time | Date (opt),
  "accountId", "calendarId", "title", "start": Time (opt), "end": Time (opt),
  "startDate": Date (opt), "allDay": bool, "location" (opt), "videoCall": bool (opt),
  "minutesBefore": 15
}

CalendarPreferences {
  "weekStart": "locale|monday|sunday|saturday",
  "defaultDurationMinutes": 60,       // 5–1440
  "defaultAlertMinutes": 15,          // -1 none, 0–40320
  "showDeclined": false,
  "workingHours": { "days": ["MO","TU","WE","TH","FR"], "start": "08:00", "end": "17:00" }
}
```

Every string of these types that comes from a server, an `.ics` or
another person (titles, locations, notes, names, addresses' names,
calendar names) is untrusted display text: cleaned by the daemon, shown
as plain text. `color` values are `#rrggbb`, lower case, always valid.
An unknown value of an open enum is treated as absent (`kind`, `provider`,
`source`) or as `unknown` (`showAs`, attendee `type`).

Times: `start`/`end` of a timed occurrence are UTC instants; a client
formats them in the user's zone. All-day events are dates, the same
everywhere. A floating event (no zone in the source) has `floating: true`
and `start`/`end` computed in the zone the caller passed (`event.list`
`timeZone`), so it shows at its wall-clock time.

### 4.3 Accounts

New values and fields (types.go):

```jsonc
AccountConfig += {
  "kind": "imap|graph|jira|caldav",
  "caldav": CalDAVConfig (caldav only),
  "calendar": CalendarConfig (opt; graph and caldav)
}
CalDAVConfig {
  "serverUrl": "https://cloud.example.org/remote.php/dav",   // normalised (account.discoverCalDAV)
  "principalUrl": "https://…/principals/users/jana/" (opt),  // from account.listCalDAVCalendars
  "homeSetUrl": "https://…/calendars/jana/" (opt),
  "username": "jana",
  "certificateSha256": "64 hex digits" (opt),                // the pin, as ServerConfig
  "mailAccountId": "acc_2" (opt),     // the mail account invitations and replies leave from (client scheduling)
  "scheduling": "auto|server|client" (opt, default auto)     // D8
}
CalendarConfig {
  "sync": bool,                       // graph: synchronise the calendars (default false); caldav: always true
  "disabledCalendars": ["remote id or href"] (opt),          // ≤ 200; not synchronised
  "defaultCalendar": "remote id or href" (opt),              // new events of this account
  "keepDays": 365 (opt; 30, 90, 180, 365 or 730; 0 = 365),
  "showUnanswered": true (opt, default true),                // draw unanswered invitations dashed
  "invitationCard": true (opt, default true),                // graph/imap mail accounts: show the card in Mail
  "afterAnswer": "keep|archive" (opt, default keep)          // move the invitation message after answering
}
AccountCapability += "calendar"       // the account can have calendars: graph, caldav (later imap with Google)
AccountTestResult += { "caldav": EndpointTestResult (opt) }  // capabilities: "calendar-access", "calendar-auto-schedule", "sync-collection"
AccountOAuthStartParams += { "calendar": bool (opt) }        // also ask for the calendar scopes (D15)
```

Rules, checked by `account.add`, `account.update`, `account.test`:

- `kind: caldav` has `caldav` and no `imap`, `smtp`, `oauth2`, `graph`,
  `jira`; `credentials.password` required on add (the keyring keeps it;
  no method ever returns it). `email` is the user's calendar address (the
  first `mailto:` of `calendar-user-address-set`, else `username` when it
  is an address, else what the client asks for); `name` required.
- `serverUrl`: `https` (or `http` for a loopback host), no user info,
  query or fragment, trailing slash removed; ≤ 2048 bytes. Realm (the
  account's uniqueness with `email`, `store.RealmOf`): lower-case
  host[:port] + path of `serverUrl`.
- `certificateSha256`: `api.NormalizeCertificateSHA256`; not allowed
  with an `http` URL.
- `mailAccountId`: an existing account with the `compose` capability, or
  empty. Empty with `scheduling` resolving to `client` means events with
  attendees answer `invitationsUnavailable`.
- `calendar` only on `graph` and `caldav` (`invalidArgument` elsewhere);
  `disabledCalendars` ≤ 200 entries ≤ 2048 bytes.
- Capabilities: a `caldav` account has exactly `["calendar"]`; `graph`
  accounts have `api.MailCapabilities` + `"calendar"`. **Clients show in
  Mail only accounts of kind `imap`, `graph` and `jira`**, in the Calendar
  only accounts with `calendar`. `folder.list` of a `caldav` account
  answers `{ "folders": [] }`; every message method answers
  `invalidArgument`.
- The `SyncState` of a `caldav` account (`account.list`, `sync.status`,
  `notify.syncState`) is its calendar state mapped onto the mail
  statuses (`consentRequired` never happens for caldav); a Graph account's
  `SyncState` stays the mail's.
- `account.update` that switches `calendar.sync` off, changes
  `serverUrl`/`username`, or disables calendars deletes the cached rows
  concerned (D23); switching on starts a first pass; a Graph account
  whose grant lacks the scopes gets state `consentRequired` (no error from
  `account.update`).

#### `account.discoverCalDAV`
Finds the CalDAV server a URL names, for the Add CalDAV Calendar sheet.
Nothing is stored and nothing is authenticated.

- params: `{ "url": "cloud.example.org", "certificateSha256": "64 hex digits" (opt) }` — a host or a full URL; `https` assumed; the pin (added 2026-10-16 after U1 found the gap: the sheet's Trust Certificate… runs discovery again with it, as §5.8 says) is accepted only for the typed URL's origin and is `api.NormalizeCertificateSHA256`-normalised
- result: `{ "serverUrl": "https://cloud.example.org/remote.php/dav", "foundVia": "wellKnown|srv|url",
  "needsCredentials": true, "certificate": CertificateInfo (opt) }`
- errors: invalidArgument (not a usable URL), networkError, tlsError
  (with `TLSErrorData`: the certificate-trust flow), serverTimeout,
  serverError (the server answered but is not CalDAV)

`certificate` describes the certificate the server presented when it was
verified (the sheet's "Verified (Let's Encrypt R11)" row: `issuer`).
`serverUrl` may name another host than the typed URL when `.well-known`
redirected there (iCloud does); the sheet shows it before any password is
sent. A client allows the call 20 s.

A server counts when its anonymous `OPTIONS` shows `calendar-access` in `DAV` (on a 2xx or a 401), or answers 401 with no `DAV` header at all (sabre/dav: Nextcloud, ownCloud, Baïkal authenticate before they answer `OPTIONS`); `account.listCalDAVCalendars` then verifies CalDAV with the password and is `serverError` when the home set has no calendars and the signed-in `OPTIONS` shows no `calendar-access`. A 2xx, or a 401 with a `DAV` header, without `calendar-access` is `serverError`.

#### `account.listCalDAVCalendars`
Signs in to the server of a `caldav` configuration and lists what the
account settings choose from. Nothing is stored.

- params: `{ "accountId" (opt), "config": AccountConfig, "credentials": { "password": "…" (opt) } }`
- result: `{ "principalUrl": "…", "homeSetUrl": "…", "displayName": "Jana" (opt),
  "addresses": ["jana@example.org"], "scheduling": "server|client",
  "calendars": [CalDAVCalendar] }`
- errors: invalidArgument (`config` not of kind `caldav`, its connection
  fields fail the rules above, or `accountId` names an account of another
  realm), authRequired (no password and no `accountId`), authFailed,
  keyringError, accountNotFound, networkError, tlsError, serverTimeout,
  serverError

```jsonc
CalDAVCalendar { "href": "/remote.php/dav/calendars/jana/personal/", "name": "Personal",
                 "color": "#0082c9" (opt), "access": CalendarAccess,
                 "isDefault": true (opt) }        // the server's schedule-default-calendar-URL
```

`addresses` are the `mailto:` values of the principal's
`calendar-user-address-set` (the client offers the first as `email`).
With `accountId` and no password the stored one is used only when that
account is a `caldav` account of the same realm (a password never goes to
another server). At most 200 calendars, VEVENT collections only. A
client allows the call 45 s.

### 4.4 calendar

The calendars of accounts with the `calendar` capability, their state and
the calendar preferences. Every method is local (the store) except
`calendar.freeBusy`, which may ask Graph, and `calendar.sync`.

#### `calendar.list`
- params: `{ "accountId" (opt) }` — absent: every account with the capability, in the accounts' order
- result: `{ "accounts": [CalendarAccount], "unanswered": 3 }` — `unanswered`:
  invitations of enabled accounts waiting for an answer (`invitation.list`
  with `state: "unanswered"`, counted)
- errors: accountNotFound, invalidArgument (an account without the capability), storageError

A Graph account with `sync: false` is listed with empty `calendars`, so
that Preferences can offer the switch. Disabled calendars are listed with
`disabled: true` and no occurrences; `remoteId` is the key for
`CalendarConfig` (`disabledCalendars`, `defaultCalendar`). A paused account is listed with
state `disabled`.

#### `calendar.update`
The user's own settings of one calendar; nothing goes to the server.

- params: `{ "calendarId", "visible": bool (opt), "userColor": "#rrggbb" | "" (opt; "" clears) }`
- result: `{ "calendar": Calendar }`
- errors: calendarNotFound, invalidArgument (a colour that is not `#rrggbb`), storageError

Followed by `notify.calendarChanged` naming the calendar (to every
client, the caller too). Which calendars are synchronised and which is
the default are account settings (`CalendarConfig`, `account.update`).

#### `calendar.sync`
- params: `{ "accountId" (opt), "calendarId" (opt) }` — absent: every account
- result: `{}` — the pass runs in the background; `notify.calendarState` follows
- errors: accountNotFound, calendarNotFound

#### `calendar.preferences`
- params: `{}`
- result: `{ "preferences": CalendarPreferences }`

#### `calendar.setPreferences`
- params: `{ "preferences": CalendarPreferences }` — a missing field keeps its value
- result: `{ "preferences": CalendarPreferences }`
- errors: invalidArgument (a value out of range, `workingHours.end` not after `start`, no day)

Followed by `notify.calendarChanged` without calendars (clients reread
what depends on the preferences: alerts of new events, working hours).

#### `calendar.freeBusy`
Who is busy when: the user from the stored occurrences of every visible
calendar of enabled accounts (not declined, not `showAs: free`), others
through the source of `accountId` when it has `features.freeBusy`.

- params: `{ "accountId", "attendees": ["jana@example.org"], "from": Time, "to": Time,
  "intervalMinutes": 30 (opt; 15, 30 or 60) }` — ≤ 20 attendees, `to − from` ≤ 62 days
- result: `{ "me": Availability, "attendees": [Availability] }` — in the order asked;
  `known: false` for an attendee the source cannot answer for (or for every attendee
  without `features.freeBusy`), never an error for that
- errors: accountNotFound, invalidArgument, calendarConsentRequired
  (only when `accountId`'s own calendar is unreadable). A failed or slow
  free/busy request of the source is not an error: the attendees come
  back `known: false` and `me` is still computed.

A client allows 20 s and asks again at most every 2 s while the user
edits.

#### `calendar.findFreeTime`
Slots in which the user and the known attendees are free.

- params: `{ "accountId" (opt), "attendees": [] (opt), "durationMinutes": 30, "from": Time, "to": Time,
  "workingHoursOnly": true (opt, default true), "limit": 5 (opt, ≤ 50) }`
- result: `{ "slots": [{ "start": Time, "end": Time }], "unknown": ["address"] }` —
  slots start on the quarter hour, inside working hours of the preferences
  (in the daemon's local zone) when asked, earliest first; `unknown` lists
  attendees without free/busy (not taken into account)
- errors: those of `calendar.freeBusy`

### 4.5 event

#### `event.list`
The occurrences of a range, from the stored window (§2.3), online for a
part outside it.

- params: `{ "from": Time, "to": Time, "timeZone": TimeZone, "calendarIds": [] (opt), "accountIds": [] (opt),
  "includeHidden": false (opt), "includeDeclined": bool (opt; default the preference),
  "includeInvitations": true (opt), "limit": 2000 (opt, ≤ 5000) }` — `to − from` ≤ 400 days
- result: `{ "occurrences": [Occurrence], "truncated": bool, "complete": bool }`
- errors: invalidArgument (range, zone not IANA, unknown ids), storageError

`occurrences` overlap `[from, to)` (an all-day one by its dates in
`timeZone`), sorted by start (all-day first on their first day), then
calendar order. Without `calendarIds` every visible calendar of enabled
accounts (`includeHidden` adds the hidden ones). `includeInvitations`
adds the unanswered invitations of accounts with `showUnanswered` that
are not in any calendar (`invitation` set, `myResponse: "needsAction"`).
An account whose `showUnanswered` is off contributes neither those nor
the occurrences of its calendars still waiting for the user's answer
(`myResponse: "needsAction"`, which Exchange adds to the calendar by
itself); they are in the Invitations section only. Clients draw
`needsAction` occurrences dashed with a "?" badge.
`truncated`: `limit` reached. `complete: false`: a part of the range lies
outside an account's window and could not be fetched (offline, an error);
what is stored is returned. A client allows 10 s.

#### `event.get`
- params: `{ "eventId", "recurrenceId": Time | Date (opt), "timeZone": TimeZone (opt) }`
- result: `{ "event": Event }`
- errors: eventNotFound, storageError

With `recurrenceId` the instance (its own fields if overridden, else the
series'); without it on a series, the master as its first instance.

#### `event.search`
- params: `{ "query": "supplier", "from": Time (opt), "to": Time (opt), "calendarIds": [] (opt), "limit": 50 (opt, ≤ 200) }`
- result: `{ "occurrences": [Occurrence], "total": 12 }`
- errors: invalidArgument (query empty or > 256 bytes)

Over title, location, notes and people's names and addresses of stored
events (prefix match per word, diacritics ignored). One occurrence per
event: a series' next instance from now (else its last), ordered:
upcoming by start, then past, latest first. `total` ≤ 1000.

#### `event.create`
- params: `{ "calendarId", "event": EventInput, "sendInvitations": true (opt), "mail": MailText (opt) }`
- result: `{ "event": Event, "invitations": { "via": "server|mail|none", "count": 3 } }`
- errors: calendarNotFound, readOnly, invalidArgument, invitationsUnavailable,
  attachmentNotFound, attachmentTooBig, calendarConsentRequired, authFailed,
  offline, networkError, serverTimeout, serverError, tlsError, recurrenceUnsupported

`sendInvitations: false` with attendees is refused on Graph (Exchange
always sends, `invalidArgument`); on CalDAV with client scheduling it
stores the attendees without mail. With `via: "mail"` the REQUEST is
queued in the outbox of the mail account (it appears there), `mail`
gives its subject and text (absent: the title, no text). The server
answered before the result (D6); a client allows 45 s.

#### `event.update`
- params: `{ "eventId", "recurrenceId" (opt), "scope": Scope, "etag", "event": EventInput,
  "sendUpdates": true (opt), "mail": MailText (opt) }`
- result: `{ "event": Event, "droppedExceptions": 0, "invitations": { "via", "count" } }`
- errors: those of `event.create`, eventNotFound, eventGone, conflict (`data.event`)

`event` replaces every field of the scope's target (the client sends the
whole edited event). `scope` must be `all` for a non-recurring event and
for a `complex` rule; `this` makes an exception; `thisAndFuture` splits
the series (D19; on the first instance it is `all`). An attendee (not the
organiser) may change only `alerts`, `showAs` and `private` (`readOnly`
otherwise). `droppedExceptions`: overrides that no longer fit the
changed rule (scope `all`). Updates to attendees follow `sendUpdates` as
invitations do. A client allows 45 s.

#### `event.delete`
- params: `{ "eventId", "recurrenceId" (opt), "scope": Scope, "etag",
  "notify": true (opt), "comment": "…" (opt, ≤ 2 KiB), "mail": MailText (opt) }`
- result: `{ "invitations": { "via", "count" } }`
- errors: eventNotFound, eventGone, conflict, readOnly, offline, networkError,
  serverTimeout, serverError, calendarConsentRequired

The organiser's delete of an event with attendees cancels it for them
(Graph `cancel`, CalDAV server scheduling, or iMIP CANCEL) unless
`notify: false`. An attendee's delete removes it from the user's calendar
only; with `notify: true` it declines first (as `event.respond` declined,
`sendResponse: true`) — the detail's Remove from Calendar asks which.

#### `event.respond`
The user's answer as an attendee of a stored event.

- params: `{ "eventId", "recurrenceId" (opt), "scope": "this|all", "etag",
  "response": "accepted|tentative|declined", "sendResponse": true (opt),
  "comment": "…" (opt, ≤ 2 KiB), "proposedStart": Time (opt), "proposedEnd": Time (opt),
  "mail": MailText (opt) }`
- result: `{ "event": Event, "via": "server|mail|none" }`
- errors: eventNotFound, eventGone, conflict, invalidArgument (the user is
  not an attendee; a proposed time without `canProposeNewTime`, or only
  one end of it), offline, networkError, serverTimeout, serverError,
  calendarConsentRequired

Graph: accept / tentativelyAccept / decline on the instance or the
series. CalDAV: the user's `PARTSTAT` written with `If-Match`; with
client scheduling an iMIP REPLY is queued (`via: "mail"`). The stored
invitations of the event (`cal_responses`) take the answer too.

#### `event.snoozeAlert`
- params: `{ "alertId", "minutes": 5 }` — 1–1440
- result: `{}` — the alert fires again then (`notify.eventAlert` `due`)
- errors: invalidArgument (unknown or not fired)

#### `event.dismissAlert`
- params: `{ "alertId" }`
- result: `{}` — every client gets `notify.eventAlert` `dismissed`
- errors: invalidArgument (unknown)

**As built (T9b, `core/calendar_service.go`, `event_service.go`,
`calendar_freebusy.go`).** Where core is more precise than §4.4–4.5:

- *Zone.* `event.list`/`event.get` `timeZone: ""` reads as the daemon's
  zone (the engine's), not `invalidArgument`; any other non-IANA name is.
- *Outside the window.* Graph and CalDAV answer through `RangeObjects`
  (asserted on the provider) with the events' fields; a provider without
  it falls back to `Range`, whose occurrences take their fields from the
  stored resource of their href. Only the part outside each account's
  window is asked, once per calendar, within 10 s; occurrences fetched
  there have an `eventId` only when the store has their resource.
- *Privacy.* A private event of a calendar shared with the user
  (`owner` set) that the user does not organise comes without title,
  location, notes, links and video link (occurrences and `event.get`).
- *Flags.* `readOnly`: calendar access `read` or kind birthdays/holidays;
  `canEdit`/`canDelete` = not read-only (and not cancelled for edit) of an
  enabled account; `canRespond`: the user is an attendee who does not
  organise; `canProposeNewTime`: `canRespond`, Graph, the event's
  `allowNewTimeProposals` and `features.proposeNewTime`;
  `removeIsDelete`: the user is not an attendee or organises it.
- *Writes.* No running provider: the account's calendar state's error
  (`calendarConsentRequired`, `authRequired`) or `offline`; a paused
  account `readOnly`. `etag` is required. `sendUpdates: false` of an
  organiser's event with attendees is refused on Graph like
  `sendInvitations: false`. `videoCall.mode: create` needs
  `features.onlineMeetings`. Attachment ids are looked up under the
  calendar's account, then under `features.mailAccountId`; with client
  scheduling they go with the iMIP REQUEST (`IMIPMail.Attachments`,
  `sendCalDAVIMIP`), the size estimate (base64 of the files and the
  calendar part + 16 KiB) is checked against the outbox limit before
  anything is written. An attendee's update keeps the stored event and
  takes only `alerts`, `showAs` and `private` (title, location, notes,
  times, recurrence or attendees that differ are `readOnly`); an
  organiser's keeps the organiser and the stored answers of attendees
  still listed. `event.update` returns the instance (`this`), the new
  series (`thisAndFuture`) or the master (`all`). An attendee's delete
  with `notify` declines (`Respond`, scope `this` or `all`), then deletes
  with `Notify: false`; its `invitations` are the decline's.
  `event.respond`/an attendee's delete record `cal_responses` for the
  `request` invitations of the UID in every account (scope `this`: those
  of the instance's recurrence id), `via` `imip` when mailed, else the
  source name, then `notify.invitationsChanged`.
- *Disabled calendars.* A calendar in `disabledCalendars` (or whose row
  the engine marked `sync_mode = "disabled"`, `calsync.Disabled`) is
  listed by `calendar.list` with `disabled: true` (never `isDefault`);
  every calendar carries `remoteId`. It is out of `event.list` (its id in
  `calendarIds` is `invalidArgument`), free/busy, search and the writes
  (`calendarNotFound`); `account.update` drops only its contents
  (`DeleteCalendarContents`). `calendar.update` stores its user columns
  without effect (no engine reload) until it is enabled again.
- *Search.* Over every calendar of readable accounts (hidden ones too);
  `from`/`to` filter the representative occurrence, and `total` then
  counts what is left.
- *Alerts.* An unknown or not fired alert is `invalidArgument` (the
  engine's `eventNotFound`/`conflict` mapped).
- *findFreeTime.* `durationMinutes` must be positive and fit the range;
  `limit` defaults to 5; without `accountId` every attendee is
  `unknown`.

### 4.6 invitation

An invitation is a stored message with a calendar part (§3.8): its
summary rides on the message (`MessageSummary.invitation`), its card on
`message.get` (`Message.invitation`).

```jsonc
InvitationKind  "invitation" | "update" | "cancelled" | "reply" | "other"
InvitationState "unanswered" | "answered" | "outdated" | "cancelled" | "processed" | "invalid"

MessageSummary += { "invitation": InvitationSummary (opt) }
InvitationSummary {
  "method": "request|reply|cancel|counter|declineCounter|refresh|add|publish|other",
  "kind": InvitationKind,             // the list pill: request → invitation (update when it changes a known
                                      // event or SEQUENCE > 0 of a known UID), cancel → cancelled,
                                      // reply → reply, everything else other (no pill)
  "state": InvitationState,
  "title": "Q4 planning", "start": Time (opt), "end": Time (opt),
  "startDate": Date (opt), "allDay": bool (opt),
  "organizer": Address (opt), "replyFrom": Address (opt),  // reply: the attendee who answered
  "ownEvent": bool (opt)              // the organiser is one of the user's addresses: a request is processed
}

Message += { "invitation": InvitationCard (opt) }   // message.get, message.download
InvitationCard = InvitationSummary + {
  "partId": "2",
  "uid": "…", "recurrenceId": Time | Date (opt), "sequence": 0,
  "end"/"endDate", "timeZone": TimeZone (opt), "recurrence": Recurrence (opt),
  "location": "…" (opt), "videoCall": VideoCall (opt), "notes": "plain text" (opt),
  "organizerMismatch": bool (opt),    // the organiser's domain is not the From domain
  "attendeeCount": 4, "attendees": [Attendee] (opt),   // ≤ 50 shown
  "me": { "address": "me@example.org", "response": Response } (opt),   // absent: the user is not on the list
  "response": "accepted|tentative|declined" (opt), "respondedAt": Time (opt),   // the user's answer; of a reply card the attendee's (replyFrom), without respondedAt
  "eventId": "ev_…" (opt), "calendarId": "cal_…" (opt),   // the stored event it is linked to
  "previous": { "start": Time | Date (opt), "end": Time | Date (opt), "location": "…" (opt), "recurrence": Recurrence (opt) } (opt),   // update: what the linked event had before
  "conflicts": [{ "title": "Standup" (opt), "start": Time, "end": Time, "calendarId", "eventId" }],   // ≤ 3
  "day": { "from": Time, "to": Time, "busy": [{ "start": Time, "end": Time, "calendarId" (opt) }] },  // the mini strip
  "targets": [{ "calendarId", "isDefault": bool (opt) }],   // "Add to:"; empty → the answer only
  "fixedTarget": bool (opt),          // the server chose the calendar (Graph): the popup is a label
  "canRespond": bool, "canProposeNewTime": bool,
  "replyVia": "server|mail|none",     // how the answer leaves
  "replyAccountId": "acc_1" (opt), "replyTo": Address (opt),   // the account it leaves from, the organiser it goes to
  "replyApplied": bool (opt)          // reply: the attendee's answer was applied to the user's event (D10)
}
```

- `state`: `unanswered` (a request the user has not answered, its event
  not in the past), `answered`, `outdated` (a newer `SEQUENCE` of the
  same UID arrived, or the event ended), `cancelled` (a cancel, or a
  request whose event a later cancel cancelled), `processed` (a reply or
  other message the daemon has nothing to ask about), `invalid` (the
  part could not be read: no card, the message reads as usual).
- *ownEvent.* A request whose `ORGANIZER` is one of the user's addresses
  (the account's own, its Sent senders, the other accounts', read at
  ingest and kept in the card JSON) is the user's own invitation (its
  Sent copy, the copy Exchange keeps): `processed` (a CANCEL stays
  `cancelled`, and so does a request a later cancel cancelled), never in
  `invitation.list`'s unanswered, the count of `calendar.list` or the
  tentative occurrences; its card has `canRespond`/`canProposeNewTime`
  false, `targets: []`, `replyVia: none`, and links (`eventId`,
  `calendarId`) to the event the user organises with that UID and
  organiser, for Show in Calendar. The kind (invitation/update) is
  unchanged. `calendar.ParserVersion` 3 rebuilds stored rows.
- *Answered in another client.* A request without a local answer of its
  `SEQUENCE` (`cal_responses`) whose linked event has the user's response
  `accepted`, `tentative` or `declined` is `answered`: the summary's
  `state`, `invitation.list` (which then leaves it out of `unanswered`)
  and the card agree; the card's `response` is the event's, without
  `respondedAt` (unknown). `needsAction` or no response stays
  `unanswered`; a local answer of the sequence wins. The linked event is
  `store.InvitationLinkedEvent`, one rule for card, summary and list: the
  events of the UID in every account, the message's account first, then
  by account order; per account the row the invitation names (the
  override of its recurrence id, else the first master or single row,
  else the first, by calendar, remote id, recurrence id); it counts when
  the user does not organise it, its `ORGANIZER` is the invitation's, the
  account is enabled and the calendar not disabled, else the next
  account. (A daemon copy is not consulted: it exists only with a local
  answer.)
- *previous* (update, against the linked event, never a copy, the event's
  `SEQUENCE` not above the invitation's): `start`/`end` only when the
  time changed, `location` when it changed and was not empty, `recurrence`
  (the stored rule, `until`/`count` included) when the rules differ by
  canonical form (sorted lists, default interval and week start, without
  `exceptions`) or the rule is gone; a new rule where there was none
  reports nothing; absent when nothing is to report.
- `conflicts` and `day` are computed at `message.get` from the stored
  occurrences of visible calendars (`day` spans the working hours of the
  invitation's day, widened to the invitation); private events of shared
  calendars come without `title`.
- `targets`: writable calendars that can take the event (D18), the
  account's default first; Graph mailboxes: the one Exchange used, with
  `fixedTarget`.
- A message in a folder of role `junk` or `trash` has `canRespond:
  false`.

#### `invitation.list`
- params: `{ "accountIds": [] (opt), "state": "unanswered" (opt), "limit": 200 (opt, ≤ 500) }`
- result: `{ "invitations": [{ "accountId", "messageId", "summary": InvitationSummary }] }` —
  one per UID (the newest message of it), by start
- errors: invalidArgument

#### `invitation.respond`
- params: `{ "accountId", "messageId", "response": "accepted|tentative|declined",
  "calendarId": "cal_…" (opt), "sendResponse": true (opt), "comment": "…" (opt, ≤ 2 KiB),
  "proposedStart": Time (opt), "proposedEnd": Time (opt), "mail": MailText (opt) }`
- result: `{ "invitation": InvitationCard, "eventId": "ev_…" (opt), "movedTo": FolderID (opt) }`
- errors: messageNotFound, invalidArgument (no invitation, a reply/cancel,
  `canRespond` false, a `calendarId` not in `targets`), malformedCalendar,
  calendarNotFound, readOnly, eventGone, offline, networkError,
  serverTimeout, serverError, calendarConsentRequired

D18 decides the path: a linked event → as `event.respond` (scope of the
invitation: the instance for an instance invitation, else `all`); else an
iMIP REPLY queued from `replyAccountId` (unless `sendResponse: false`) and,
with `calendarId` and an `accepted`/`tentative` answer, the event stored
in that calendar. Decline removes a copy the daemon stored earlier. Then
`cal_responses`, `afterAnswer` (`movedTo` when the message was archived),
`notify.invitationsChanged`, `notify.calendarChanged`. A changed answer is
just another call. A client allows 45 s.

#### `invitation.applyCancel`
Removes from the user's calendar the event a cancellation names.

- params: `{ "accountId", "messageId" }` — a message with `kind: "cancelled"`
- result: `{ "removed": bool }` — false when no stored event was linked
- errors: messageNotFound, invalidArgument (not a cancellation), readOnly,
  eventGone (already gone: `removed: false` instead), offline, networkError,
  serverError

Never sends anything (the organiser cancelled).

### 4.7 Notifications

| Method | params |
|---|---|
| `notify.calendarChanged` | `{ "accountIds": ["acc_1"] (opt), "calendarIds": ["cal_1"] (opt), "from": Time (opt), "to": Time (opt) }` |
| `notify.calendarState` | `{ "state": CalendarSyncState }` |
| `notify.eventAlert` | `{ "alert": EventAlert }` |
| `notify.invitationsChanged` | `{ "accountIds": ["acc_1"] (opt) }` |

- `notify.calendarChanged`: stored calendars or occurrences changed (a
  pass, a write by any client or the MCP bridge, `calendar.update`, the
  preferences, an account's calendar settings); `from`/`to` bound the
  changed occurrences when known; a client showing a range overlapping
  them (or any range when absent) runs `event.list` again. Gathered per
  account within 1 s.
- `notify.calendarState`: on every change of an account's calendar
  state, progress at most every 500 ms.
- `notify.eventAlert`: `due` when an alert fires or a snooze ends;
  `dismissed` when any client dismissed it (others withdraw their
  notification). Sent once per state change.
- `notify.invitationsChanged`: the unanswered invitations changed
  (arrived, answered, outdated, cancelled); a client reruns
  `invitation.list` / `calendar.list` for the count. Gathered within 1 s.
- `notify.newMessage` of a message with an invitation carries
  `message.invitation` (the summary): the invitation and cancellation
  notifications are built from it.
- A client that does not know these notifications ignores them.

### 4.8 Limits (`pkg/api/calendar.go`)

```go
const (
	MaxICSBytes              = 1 << 20  // one iCalendar object or calendar part
	MaxEventTitleBytes       = 1024
	MaxEventLocationBytes    = 1024
	MaxEventNotesBytes       = 32 << 10
	MaxMailTextBytes         = 4 << 10  // MailText.Subject, MailText.Text each
	MaxCommentBytes          = 2 << 10
	MaxEventAttendeesIn      = 100      // EventInput.Attendees
	MaxEventAttendeesStored  = 500      // Event.Attendees (attendeeCount counts all)
	MaxInvitationAttendees   = 50       // InvitationCard.Attendees
	MaxEventAlerts           = 4
	MaxAlertMinutes          = 40320    // 4 weeks
	MaxEventAttachments      = 10
	MaxEventAttachmentBytes  = 3 << 20
	MaxEventListDays         = 400
	MaxEventListResults      = 5000
	DefaultEventListResults  = 2000
	MaxEventSearchResults    = 200
	MaxSeriesOccurrences     = 1000     // per series per query window
	MaxCalendarsPerAccount   = 200
	MaxEventsPerCalendar     = 50000
	MaxFreeBusyAttendees     = 20
	MaxFreeBusyDays          = 62
	MaxFreeSlots             = 50
	MaxInvitationList        = 500
	CalendarFutureDays       = 400      // the window's forward edge (§2.3)
	DefaultCalendarKeepDays  = 365
	MaxRecurrenceInterval    = 99       // input
	MaxRecurrenceCount       = 999      // input
)
```

Exceeding an input limit is `invalidArgument`; a server's data over a
stored limit is truncated (and counted) or skipped as §3 says. The limits of
attachments are per file and per event (Graph's own); the iMIP path
additionally obeys the outbox's `MaxOutgoingMessageBytes` on the built
message (base64 of 10 × 3 MiB exceeds it): `event.create`/`event.update`
with `via: "mail"` answer `attachmentTooBig` when the built mail would be
over that limit, before anything is stored or sent (T9b). The
Windows and Swift mirrors check these values (`GoContractTests` counts
the limits: its expected count grows with this list).

### 4.9 Go surface

```go
// service.go
type CalendarService interface {
	List(ctx, CalendarListParams) (*CalendarListResult, error)
	Update(ctx, CalendarUpdateParams) (*CalendarUpdateResult, error)
	Sync(ctx, CalendarSyncParams) (*CalendarSyncResult, error)
	Preferences(ctx, CalendarPreferencesParams) (*CalendarPreferencesResult, error)
	SetPreferences(ctx, CalendarSetPreferencesParams) (*CalendarSetPreferencesResult, error)
	FreeBusy(ctx, CalendarFreeBusyParams) (*CalendarFreeBusyResult, error)
	FindFreeTime(ctx, CalendarFindFreeTimeParams) (*CalendarFindFreeTimeResult, error)
}
type EventService interface {
	List, Get, Search, Create, Update, Delete, Respond, SnoozeAlert, DismissAlert
}
type InvitationService interface { List, Respond, ApplyCancel }
// AccountService += DiscoverCalDAV, ListCalDAVCalendars
// Backend += Calendars() CalendarService, Events() EventService, Invitations() InvitationService
// Optional Notifier extension, asserted like BoardNotifier:
type CalendarNotifier interface {
	CalendarChanged(CalendarChangedNotification)
	CalendarState(CalendarStateNotification)
	EventAlert(EventAlertNotification)
	InvitationsChanged(InvitationsChangedNotification)
}
```

Method constants `MethodAccountDiscoverCalDAV = "account.discoverCalDAV"`,
`MethodAccountListCalDAVCalendars`, `MethodCalendarList` …
`MethodInvitationApplyCancel`, appended to `AllMethods` after the board's
in this order: account (2), calendar (7), event (9), invitation (3);
`NotifyCalendarChanged` … `NotifyInvitationsChanged` appended to
`AllNotifications`. Typed enums with Go constants (`CalendarSource`,
`CalendarAccess`, `CalendarKind`, `EventResponse`, `ShowAs`,
`EventStatus`, `EditScope`, `InvitationMethod`, `InvitationKind`,
`InvitationState`, `CalendarSyncStatus`, `InvitationsVia`,
`VideoProvider`, `CalDAVScheduling`, `AfterAnswer`, `WeekStart`) so that
the Windows `WireEnums` test can list them. T1 added six more typed enums
with the same wire values (mirrors copy them too): `RecurrenceFrequency`
(daily, weekly, monthly, yearly), `AttendeeRole` (required, optional,
chair, nonParticipant), `AttendeeType` (person, room, resource, group,
unknown), `EventAlertState` (due, dismissed), `VideoCallMode` (none,
create, link), `CalDAVFoundVia` (wellKnown, srv, url); ID types
`CalendarID`, `EventID`, `AlertID` and the scalar `DateOrTime`; Go-only:
`CalendarPreferencesUpdate` with pointer fields for `calendar.setPreferences`,
`*bool` for optional booleans that default to true, and `Message.Invitation`
(the card) replacing `MessageSummary.Invitation` under the one JSON key
`invitation` in `message.get`.

---

## 5. The macOS client

The macOS client is the reference client of the calendar. Its pure logic
and every user-visible string are referenced first in Go,
`ui/internal/calendar` (§5.1), so that the GTK and Windows ports later
port 1:1; Swift ports that package into `MalachiCore/Calendar` with its
tests. AppKit views only draw what Core computed.

### 5.1 The Go reference package `ui/internal/calendar`

Pure (no GTK, no gettext, no cgo), texts through the `Translator`
interface of `ui/internal/board` (same method set, so GTK later passes
`i18n.Tr`), every file with texts in `po/POTFILES`, a `po_test.go` like
the board's (every msgid is in `po/malachi.pot`). Strings from servers are
formatted in, never concatenated into sentences; dates and times through
strftime msgids (`// TRANSLATORS:` above each).

| File | Contents |
|---|---|
| `text.go` | mode name `Calendar`; view names Day, Week, Month, Agenda; Today; New Event; Search Events; status bar lines ("Calendars synced %s", "%d unanswered invitation" plural, "Calendar access needs a new sign-in"); empty states; errors by code (readOnly, eventGone, conflict, invitationsUnavailable, calendarConsentRequired, recurrenceUnsupported, malformedCalendar) |
| `view.go` | `View` (day, week, month, agenda), `Range(view, anchor, weekStart, zone)` → `[from, to)`; `Step(view, anchor, ±1)`; `Title(view, range)` "October 2026", `Subtitle` "12–18 October · week 42" (ISO week number, or the locale's when weekStart is Sunday: US rule); `WeekStart(pref, localeFirstDay)`; `SwitchAnchor(from, to, anchor, today, loc)`: switching to Agenda starts it at today, every other switch keeps the anchor |
| `week.go` | the time-grid layout: `LayoutDay(occurrences, day, zone)` → timed blocks with `column`, `columns` (greedy interval colouring of overlap clusters, stable by start then length then id), clipped to the day, min height 15 min; `AllDayLanes(occurrences, days)` → lanes of spanning bars; `NowLine(now, range)`; drawing state of a block: `Tentative` (striped), `Unanswered` (dashed with "?"), `Declined` (struck, only with showDeclined), `Cancelled` |
| `month.go` | 6×7 grid (`MonthCells(anchor, weekStart)`), week numbers, out-of-month flag, `CellCapacity(cellHeight, lineHeight, lanes, laneHeight)`, `MonthLanes` (bars of all-day and cross-midnight events per week row, §5.4), `CellItems(occurrences, day, capacity)` → pills of the timed events within a day + `More(n)` text "+%d more" (`N`) |
| `agenda.go` | grouping by day from `from` (Today / Tomorrow / weekday date headings), multi-day all-day events on each day with "Day %d of %d", empty-range text |
| `detail.go` | the popover's rows from an `Event`: calendar line "%s · %s", time line ("Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min"), duration text (`N` for hours and minutes), all-day and multi-day forms, recurrence text, people line ("Organiser %s · %d attendees"), response labels (Accepted, Tentative, Declined, No Reply), availability rules: `CanRespond`, `RemoveTitle` ("Remove from Calendar" / "Delete"), `RemoveQuestion` (attendee: "Also tell the organiser you won't attend?"), Show Invitation in Mail visibility |
| `recurrence.go` | `RecurrenceText(r)`: "Every day", "Every 2 weeks on Monday and Wednesday", "Monthly on the second Tuesday", "Yearly on 14 October", "until %s" / "%d times", "Custom" for `complex`; the editor's Repeat choices (Never, Every Day, Every Week, Every 2 Weeks, Every Month, Every Year, Custom…) and their `Recurrence` values |
| `editor.go` | `NewEventDefaults(start, prefs)` (rounded to the next half hour; double-click on a month cell = 9:00 that day; all-day row = all-day), validation (end after start, title length), the primary button label ("Save" / "Save and Send Invitations"; with Graph and attendees always the latter), footer text ("Invitations leave by e-mail from account %s (%s) through the Outbox." / "The server sends the invitations." / "Attachment: %s"), alert choices (None, At time of event, 5, 10, 15, 30 minutes, 1 hour, 1 day before), duration choices, time-zone link text, `MailTexts(kind, event)` (the iMIP subjects: "Invitation: %s", "Updated invitation: %s", "Cancelled: %s", "Accepted: %s", "Tentative: %s", "Declined: %s", and the bodies), recurring-scope question ("This Event Only", "This and Future Events", "All Events") |
| `availability.go` | the editor's availability rows from `calendar.freeBusy`: per attendee busy blocks on the editor's day axis, "availability unknown", the summary line ("%s: all known attendees are free" / "%d of %d attendees are busy"), Next Free Slot from `calendar.findFreeTime` |
| `invitation.go` | pills (`PillKind(summary)` → Invitation / Update / Cancelled / Reply / none, with the struck-through subject rule for cancelled), the card's lines: date tile (weekday, day, month abbreviation), time line, conflict line ("Conflicts with %s (%s) in calendar %s"; several: plural), "Add to:" label, response buttons, the reply note ("The reply leaves from account %s." / "The reply goes to %s.", organiser-mismatch warning), state strip ("Accepted · in calendar %s"), Change Response, Show in Calendar, Propose New Time…, Decline Without Reply, reply card ("%s accepted"), invalid/outdated/cancelled lines |
| `today.go` | the Board → Today strip: rows of today's occurrences (time range or All Day, title, place, "took place" for past ones, Respond for unanswered), the heading "Today in Calendar", Open Calendar |
| `notifications.go` | alert ("in %d minutes" plural, "now", "%s · %s" time and place), Join, Snooze; new invitation ("New invitation from %s"), Accept, Show; cancellation ("%s cancelled the event"), Remove from Calendar |
| `preferences.go` | General → Calendar rows (Week Starts On with System/Monday/Sunday/Saturday, Default Duration, Default Alert, Show Declined Events, Working Hours), Accounts → Calendar and Invitations groups (Sync This Account's Calendars, default/read only/shared labels, Default Calendar for New Events, Keep Events with the four windows, Show the Invitation Card above the Message in Mail, Show Unanswered Invitations Tentatively in the Calendar + subtitle, After Answering, Move the Invitation Message: Keep in Inbox / Archive), "This account has no calendar." + "Add CalDAV Calendar…" |
| `caldav.go` | the sheet's flow (`Wizard`: entering → discovering → listed → adding; which button is enabled; trust offer from `certtrust`) and texts (title, subtitle with the server examples, field labels, password note, Discover Calendars, "Found through %s · %d calendars", certificate row, footer "Not shown in Mail; in Calendar with the CALDAV capsule.") |
| `keys.go` | keyboard rules in Calendar mode: ⌘T Today, ⌘← / ⌘→ previous/next, ⌃⌘1–4 Day/Week/Month/Agenda, ⌥⌘N New Event, Return opens the selected event's detail, ⌫ deletes/removes it (with the question), Escape closes the popover then clears the search; arrows move the selection between occurrences in time order |
| `samples.go` | the invented sample data (§5.10), so that GTK and Windows can use the same samples |

Tests: table tests for every function (overlap layouts with 1–8
overlapping events, DST days with 23 and 25 hours, all-day lanes
spanning a week boundary, month capacity, ISO and US week numbers,
recurrence texts, pill kinds, availability summaries), and `po_test.go`.

As built (task U1, 2026-10-16), where the names differ from the table:
`format.go` (no texts) holds `Env` (`Tr`, `Format`, `Loc`), the
`Strftime` writer the client supplies (GLib, `DateFormatter`, .NET) and
`EnglishStrftime` for the tests and samples; `Translator` is an alias of
`board.Translator`. The range type is `Range` and its function
`ViewRange(view, anchor, weekStart, loc)`; an occurrence's identity is
`OccurrenceKey` (`Key` is the keyboard shortcut of `keys.go`); the
month cell's item is `Pill`, the list pill of an invitation
`InvitationPill` (`PillKind`). The layout functions take the zone and
`showDeclined` (`LayoutDay(occ, day, loc, showDeclined)`,
`AllDayLanes(occ, days, loc, showDeclined)`, `CellItems(occ, day,
capacity, loc, showDeclined)`), `MonthCells(anchor, weekStart, now,
loc)` marks today. `week.go` also has the blocks' accessibility label
(`BlockLabel`, §5.3); `text.go` the strftime msgids every file writes
dates with (`TimeText`, `LongDateText`, …, `RangeText` "%s–%s", `Dot`
"%s · %s"). `keys.go` names the modifiers by their macOS keys: ⌥ and ⌃
are Shift on Linux and Windows (`Press.ShiftModifier`; Ctrl+Alt is
AltGr there), so New Event is Ctrl+Shift+N and the views
Ctrl+Shift+1–4. The CalDAV `Wizard`'s Trust Certificate… runs
discovery again with the pin (`certificateSha256` of
`account.discoverCalDAV`), which then goes into the list and add params
while the discovered server is on the typed host.

The mode itself (`board.Mode` gains `ModeCalendar`, nick `calendar`;
`StartChoice` gains `StartCalendar`; `Command.allows` lets Calendar keep
Check Mail, New Message and New Event) is changed in
`ui/internal/board/mode.go` with its tests; the gschema
(`data/io.github.schotek.Malachi.gschema.xml`) gains the value `calendar`
of `BoardStartMode`, the description of `board-last-mode`, and the key
`calendar-view` (`day|week|month|agenda`, default `week`). The assistant's
quick actions What's on Today and Find a Free Slot, their prompts, and the
bridge tools they may use (`list_calendars`, `list_events`, `get_event`,
`find_free_time`, `list_invitations`) go into `ui/internal/assistant`
(`PanelActions`, `AllowedTools`).

**New msgids and the Windows coverage test.** Every msgid these files add
to `po/malachi.pot` must be used by the Windows client or listed in
`windows/parity-exclusions.txt` (`StringsCheckTests` enforces it); until
the Windows port they are listed there with the reason "calendar: Windows
port pending (docs/calendar.md)", and the gschema strings with the usual
gschema reason. `make po` runs in the Docker image `malachi-po:f42` on the
Mac (see the auto-memory note) or in Toolbx.

### 5.2 MalachiCore pieces (shared by every artboard)

| Piece | Files |
|---|---|
| API mirror | `API/CalendarAPI.swift` (every type of §4.2–4.7, codable, open enums as structs with `rawValue` like the board's), `API/Accounts.swift` (kind `caldav`, `CalDAVConfig`, `CalendarConfig`, capability `calendar`, `AccountTestResult.caldav`, `oauthStart.calendar`), `API/Messages.swift` (`invitation` on summary and message), `API/ErrorCode.swift`, `API/Notifications.swift`, `API/API.swift` (method table); `Tests/MalachiCoreTests/APICodingTests+Calendar.swift` and the `goMethods`/codes copies in `APICodingTests.swift` |
| Pure port of §5.1 | `Calendar/Calendar.swift` (namespace `enum Cal`, `View`, `Range`), `CalendarText.swift` (keys = msgids), `CalendarWeekLayout.swift`, `CalendarMonthLayout.swift`, `CalendarAgenda.swift`, `CalendarDetail.swift`, `CalendarRecurrence.swift`, `CalendarEditor.swift`, `CalendarAvailability.swift`, `CalendarInvitation.swift`, `CalendarToday.swift`, `CalendarNotificationText.swift`, `CalendarPreferences.swift`, `CalendarKeys.swift`, `CalDAVWizard.swift`, `MiniMonth.swift` (the sidebar's month: days, today, the banded week) — each with its test file porting the Go tests. The Swift namespace is `Cal` (`Cal.View`, `Cal.Range`, `Cal.Env`, …), not `Calendar`, because a module-level `Calendar` in MalachiCore would shadow Foundation's `Calendar` in the existing code and make it ambiguous in MalachiMail and the tests |
| Source seam | `Calendar/CalendarSource.swift` (protocol: `snapshot` of accounts/calendars/state/unanswered, `occurrences(range)` with a cache keyed by range and invalidated by `notify.calendarChanged`, writes returning results or errors, observers), `DaemonCalendarSource.swift` (over the RPC client; `calendar.list`, `event.list`, `event.get` cached by `etag`, every write; reacts to the four notifications), `InMemoryCalendarSource.swift` + `CalendarSamples.swift` (§5.10) |
| Controllers | `Controllers/CalendarController.swift` (one per window: view, anchor, selection, search, visible calendars, the page's state; `@MainActor`), `EventDetailController.swift` (one popover's event, respond/remove/edit flows and questions), `EventEditorController.swift` (one editor window: input, validation, availability requests debounced 500 ms, save paths, scope question, attachments through `attachment.import`), `InvitationController.swift` (the card of a message: respond, change, add-to target, propose time, apply cancel), `CalDAVWizardController.swift`, `CalendarPreferencesController.swift` (General → Calendar over `calendar.preferences`), `CalendarAccountController.swift` (an account's Calendar and Invitations groups over `account.update` and `calendar.list`), `CalendarAlertsController.swift` (owned by `AppState`: `notify.eventAlert` → notifications, snooze, dismiss) |
| Settings | `Settings/Settings.swift`: key `calendar-view`; `board-start-mode`/`board-last-mode` accept `calendar` |
| Mode | `Board/Board.swift`: `Mode.calendar` (raw 2, nick `calendar`), `StartChoice.calendar`, `Command.allows` for Calendar; every `switch` over `Mode` |

Swift tests port every Go test of §5.1 and add controller tests over a
fake source (`InMemoryCalendarSource`) and over `FakeDaemon` for
`DaemonCalendarSource` (notifications invalidate the right ranges; a
conflict reloads the event; consentRequired shows the banner state).

As built (task M3, 2026-10-16), where it differs from the table: the
protocol is `CalendarDataSource` (the API mirror already has
`CalendarSource`, the `graph`/`caldav` open enum), its observers get a
`Cal.SourceChange` (`snapshot`, `occurrences(from:to:)`, `event`,
`alert`, `error`), and `Cal.Snapshot` also carries `account.list`'s
accounts (names, kinds, `config.calendar`). Notifications are not added to
`DaemonNotification`: the app's notification hub decodes
`CalendarNotification(raw)` next to it and hands each one to every
calendar source (`handle(_:)`) and to the app's `CalendarAlertsController`
(alerts are routed there once, not through a window's source), so the
switch in `MailboxController` and its tests stay as they are; the hub also
calls `DaemonCalendarSource.accountsChanged()` and
`connectionChanged(connected:)` as for the board. Questions are async
closures that return the answer (`onQuestion` → `Cal.Answer` confirm /
alternative / cancel, `onScope` → `EditScope?`, `onConfirmTrust` →
`Bool`), hand-offs are plain callbacks (`onOpenDetail`, `onNewEvent`,
`onEdit`, `onShowInvitation`, `onShowInCalendar`, `onOpenLink`,
`onSignIn`), state is read after `onChange`. The samples' source answers
`Cal.SourceError.preview` ("The preview cannot do that.") for every write
except a calendar's checkbox, which only changes what is shown.
`CalendarAccountController` keys the calendars' checkboxes and the default
by `CalendarRecord.remoteId` (`calendar.list` lists the disabled calendars
with `disabled`) for Graph and CalDAV alike; a CalDAV account also lists
the server's calendars (`account.listCalDAVCalendars` with `accountId`),
so that one the daemon does not store yet can be switched on.

Calendars a client shows: `ActiveCalendars` (`ui/internal/calendar/calendars.go`)
drops every calendar `calendar.list` marks `disabled` (unchecked in
Preferences → Calendar): the sidebar's rows and visible-calendar set, the
editor's Calendar popup, the detail's calendar name, the free/busy and
search scopes and the counts use only the active ones.

### 5.3 Artboard 1 · Calendar · Week (and Day)

- **Mode plumbing.** `MainContentViewController.setMode` generalised:
  every mode other than `.mail` shows a page in place of the split view
  (today it tests `== .board`). `MainWindowController+Mode.swift` installs
  the calendar's toolbar and re-inserts `.sidebarTrackingSeparator` after
  every toolbar install (the measured AppKit behaviour noted in
  `CLAUDE.md`). `ModeSwitch` gets three segments (`envelope`,
  `square.grid.2x2`, `calendar`), View ▸ Mail / Board / Calendar with ⌘1
  / ⌘2 / ⌘3, physical-key handling as for ⌘1/⌘2 (Czech QWERTZ). New file
  `MainWindowController+Calendar.swift` owns the calendar page and its
  toolbar; `viewsMail` is false in Calendar (notifications of new mail
  still fire).
- **Toolbar** (`Calendar/CalendarToolbar.swift`): mode switch, sidebar
  toggle, tracking separator, Today, ‹ ›, a segmented Day / Week / Month /
  Agenda, the title with subtitle (window `title`/`subtitle` from
  `Calendar.Text.title/subtitle`), flexible space, a search field
  "Search Events", "+" (New Event), ✦. Overflow order: ✦, search, then
  the view segments.
- **Page** (`Calendar/CalendarPageViewController.swift`): an
  `NSSplitViewController` with the sidebar item (240 pt) and the content;
  the assistant panel item at the end when shown (D28).
- **Sidebar** (`Calendar/CalendarSidebarViewController.swift`,
  `MiniMonthView.swift`): the mini month (today filled, the shown range
  banded; click = go to that day, arrows change month), INVITATIONS with
  the Unanswered count badge (`CountBadgeView`; click = Agenda filtered
  to unanswered invitations), then per account a header with the kind
  capsule (`CapsuleStyle`; `accountHeaderBadge` gains `CALDAV` for kind
  caldav) and its calendars with checkbox (`calendar.update visible`) and
  colour square, a consent/state row when the account's state needs it
  ("Calendar access needs a new sign-in" + Sign In…, through the existing
  re-sign-in flow with `calendar: true`), at the bottom "Add Calendar…"
  (menu: Add CalDAV Calendar…, and "Sync a Microsoft 365 account's
  calendars…" which opens Preferences → Accounts).
- **Week / Day view** (`Calendar/CalendarTimeGridView.swift`,
  `CalendarDayHeaderView.swift`, `CalendarAllDayView.swift`,
  `EventBlockView.swift`): a scroll view with the hour axis (the whole day,
  scrolled to working hours at first show), day header row (today's
  number in a filled circle, weekends grey), all-day row with spanning
  bars (lanes from Core), event blocks as subviews (tinted fill, 3 pt
  bar in the calendar colour, title, time, recurring symbol, attendee
  count; dashed outline with a "?" badge for unanswered, diagonal stripes
  for tentative), today's column tinted and the red now line (a timer
  each minute). Day = the same grid with one column and room for location
  and video-call markers. Click selects and opens the detail popover
  (§5.5); double-click on empty time opens the editor at that slot;
  drag-to-move and resize are not in this iteration (open question Q4).
  Blocks are accessibility elements with the Core label ("Standup,
  Tuesday 10:00–10:30, awaiting your answer"). Owner findings of the
  live test (2026-10-16): block fills are *opaque* (the calendar colour
  blended at 18 % over the window background, computed for light and
  dark, dashed unanswered blocks too), drawn above the hour lines and
  day separators so nothing shows through; the now line is a subview
  above the blocks. The first scroll (`Cal.scrollMinute` × 60 pt − 10 pt)
  is re-applied whenever the clip view's height changes until the user
  scrolls, because the first layout clamped it against a provisional
  height (the owner saw 03:00); a resize before any scroll snaps back to
  working hours, a day change resets the rule.
- **Status bar** (`StatusBarViewController.swift`): in Calendar mode the
  left text is the calendar state ("Calendars synced 2 min ago", errors),
  the right the unanswered count and the Outbox state.

As built (task M4, 2026-10-16), where it differs from the above: the mode
switch sits in the sidebar section alone and is not navigational (as in
the board's List); the sidebar toggle comes right after the tracking
separator, because the three-segment switch (134 pt) and the window's
buttons fill the 240 pt sidebar section (measured at 1280 pt: the toggle
went to the overflow menu), and the Mail and board List toolbars place it
the same way (`macos/README.md`); Today, ‹ › and the view's segments are navigational, so
they come ahead of the title. ✦ is the assistant panel's button only,
shown while the panel exists (Assistant shown, In App chosen): the
Assistant menu holds message actions, which the Calendar does not allow.
The panel moves into the page's inspector item when ✦ shows it there and
back to the mail's split view whenever the window leaves the Calendar
(D28 as planned; its runtime behaviour is not yet measured). Requests
that reveal the panel from elsewhere (`Board.Request.revealAssistant`)
still go to Mail. With Show the Board off the mode switch stays (Mail and
Calendar) with its Board segment disabled; before, macOS 15 hid the
whole switch. The page folds its sidebar, then the panel, when the
content would get less than 360 pt (back at 240 pt once there is room),
like the board's List. The status bar's left text is the calendar line,
its note the unanswered count followed by the triage's note (`%s · %s`);
the popover with the accounts and the Outbox is the sync line's as
before. ⌘3 goes by the physical key like ⌘1/⌘2 (`Board.modeKeyDigit`,
`Board.modeForKey`). Entry points: `MainWindowController.calendarController`
/ `calendarPage` / `calendarToolbar` (made on first use, the controller
started on the first entry), `CalendarPageViewController.show(view:)`,
`controllerChanged()`, `sidebarContainer`, `contentContainer`
(`CalendarPaneContainer.install(_:)`), `assistantSlot`;
`AppState.calendarSource(for:)` and `calendarAlerts` (`AppCalendar` in
`App/Integration+Calendar.swift`, the hub's `addCalendar`).

### 5.4 Artboard 2 · Calendar · Month (and Agenda, Search)

- **Month bars** (owner, 2026-10-16): all-day events and timed events
  that cross midnight are one bar per week row across the days they
  cover, as in Apple Calendar, not a pill in every cell:
  `MonthLanes(occurrences, weekDays, loc, showDeclined)` → `[]MonthSpan`
  (`Lane`, `StartCol`/`EndCol` inclusive, `ContinuesBefore`/`After`,
  `Key`, `Title`, `CalendarID`, `State`; greedy lanes, earlier then
  longer first), `IsMonthSpan`, `LaneCount`; `CellItems` lists only the
  timed pills within a day and `CellCapacity(cellHeight, lineHeight,
  lanes, laneHeight)` leaves the row's lanes out, so "+N more" counts
  pills only.
- **Month bars as built** (`CalendarMonthView.swift`): one bar per span
  under the row's day numbers, rounded only where the event does not
  continue, the 3 pt colour bar at the left, the title once; click opens
  the detail popover; pills start below the lanes. Known gaps: lanes
  that do not fit the row are not drawn, and the "+N more" popover lists
  pills only.
- **Month** (`Calendar/CalendarMonthView.swift`, `MonthCellView.swift`):
  weekday header, 6 rows with week numbers at the left, day numbers,
  pills (dot + time + title; all-day pills filled with the colour),
  "+2 more" (a button: popover with that day's list, from
  `Calendar.CellItems`), today's ring, weekend shading, out-of-month days
  grey; double-click on an empty part of a cell = New Event that day
  (`NewEventDefaults`); unanswered invitations dashed. Capacity is
  recomputed on resize from Core, never measured by the view.
- **Agenda** always starts at today when the user switches to it (owner,
  2026-10-16), whatever day the other view showed; it moves only with ‹ ›
  or Today while it shows, and switching back keeps its day
  (`SwitchAnchor`, `ui/internal/calendar/view.go`).
- **Agenda** (`Calendar/CalendarAgendaViewController.swift`): an
  `NSTableView` with group rows per day (Core `Agenda`), rows like the
  Board's (time, colour dot, title, place, response badge); infinite
  forward scrolling in 30-day pages (`event.list` per page).
- **Search** (`Calendar/CalendarSearchController.swift`): the toolbar
  field runs `event.search` from 2 characters (debounced 300 ms) and
  shows the results in the agenda table in place of the view, "Results"
  heading, Escape returns.

As built (task M5, 2026-10-16), where it differs from §5.3–5.4: the
page's content is `CalendarContentViewController` (in
`CalendarPageViewController.swift`), which shows the agenda table for the
Unanswered filter and the search results in place of any view and
otherwise the view (`CalendarWeekView` in `CalendarTimeGridView.swift`
for Week and Day, `CalendarMonthView`, `CalendarAgendaViewController`),
or an empty state from `emptyKind` with Open Preferences. A click on a
block, bar, pill or row selects it and opens `EventDetailPopover.show`
anchored to that view (`CalendarPageViewController.open(_:anchoredTo:)`,
`anchor(for:)` finds an occurrence's view for other callers); an
invitation not in a calendar has no event id and is only selected. The
search results are one list under "Results" in the daemon's order (no
day groups: they are not one range), each row's day before its time.
The all-day row grows to five lanes; bars in further lanes are not drawn.
The agenda's badge is the response for unanswered, tentative and
declined occurrences, "cancelled", else "Day 2 of 3". The sidebar's
Add CalDAV Calendar… calls `CalendarSidebarViewController.onAddCalDAV`
(unset: the action `addCalDAVAccount:` up the responder chain) and
"Sync a Microsoft 365 account's calendars…" `onSyncMicrosoft` (unset:
Settings). Calendar-only CalDAV accounts are left out of the Mail
sidebar (`mailSidebarAccounts` in `FolderTree.swift`) and carry the
`CALDAV` capsule. The mini month's days are not separate accessibility
elements yet.

### 5.5 Artboard 3 · Event detail popover

`Calendar/EventDetailPopover.swift` (`NSPopover`, behaviour transient,
anchored to the `EventBlockView`/pill/row; a frozen entry point
`EventDetailPopover.show(eventID:recurrenceID:relativeTo:of:)` so that
the views of §5.3–5.4 and the notification click can call it) and
`EventDetailViewController.swift` over `EventDetailController`:

- Header: colour square, "Work · Calendar", kind capsule; title (bold,
  selectable, `stringValue`).
- Rows with SF Symbols: clock (date / time · duration), repeat (recurrence
  text), location, video link "Join Video Call" + host (opens through
  `MessageActionDelegate.openLink` with no listed links, so the "Open
  this link?" confirmation shows the host), people: "Organiser Jana
  Nováková · 4 attendees" with the attendee list (✓ accepted, ? no reply,
  ✕ declined, organiser label; collapsible beyond 6), "You: Accepted ▾"
  popup (changing it calls `event.respond`; with a series it asks This
  Event / All Events; Decline offers Decline Without Reply), notes in a
  grey non-editable `NSTextView` with plain text and the `links` as
  clickable ranges going through the same confirmation.
- "Show Invitation in Mail" (when `invitationMessages`): switches to Mail
  and reveals the message (`MailboxController+Reveal`).
- Footer: Edit (opens §5.6; for a series asks the scope first), Remove
  from Calendar / Delete (question per `Calendar.Detail.removeQuestion`;
  confirmation follows the Mail preference `confirm-delete`), spacer, ✦ ▾
  (Ask about This Event, Find Another Time → editor with Next Free Slot).
- Errors from writes show inline under the footer (Core text by code);
  `conflict` reloads the event and says so.

As built (task M6, 2026-10-16), where it differs from the above: the
popover is one per application (showing another closes it) and opens
with `EventDetailPopover.show(eventID:recurrenceID:relativeTo:of:)` (the
occurrence shown meanwhile is the window's selection when it matches) or
`show(occurrence:relativeTo:of:)` (the occurrence's title and time until
`event.get` answers); both need only the anchor view inside the main
window, whose `calendarController` gives the source and `Cal.Env`. Its
content is `EventDetailViewController` with `EventDetailViews.swift`
(attendee row, notes). The time row is two lines (the date, then
"10:00–11:30 · 1 h 30 min") for a timed event within one day, else
`Cal.timeLine`'s one line; tentative attendees show an orange "?",
delegated a grey arrow. The questions are sheets on the main window
(Cancel leads; the popover turns application-defined while one is up so
that it stays open); the series question leads with Cancel, then its
choices, the first the Return default. Find Another Time runs Edit (with
the series question) and opens the editor with
`EventEditorManager.Edit.findFreeSlot = true`, which the editor reads to
run Next Free Slot as it opens; it shows only for an editable event. Ask
about This Event shows only while the assistant panel exists (Assistant
shown, In App chosen): the panel unfolds in the Calendar page with a
question naming the event's `eventId` (and `recurrenceId`) and
`get_event` in its field, for the user to finish and send
(`Assistant.askAboutEventPrompt`; nothing of the event's text goes into
it). Show Invitation in Mail goes through the
board's `MainWindowController.boardMail.reveal` (`showInvitationInMail`).
Join Video Call and the notes' links close the popover and go to the
actions' `openLink` with no listed links.

### 5.6 Artboard 5 · New Event window

`EventEditor/EventEditorWindowController.swift` (760×800, a
`ComposeManager`-like registry `EventEditorManager.swift` so that an
event being edited raises its window), `EventEditorViewController.swift`,
`AvailabilityView.swift`, over `EventEditorController`:

- Title bar "New Event" / "Edit Event", toolbar buttons Attach (file
  panel → `attachment.import`, chips as `AttachmentChipsView` of Compose)
  and ✦ (Find a Free Slot; Ask the Assistant…).
- Form: Title (large field), Calendar popup (colour + "Account ·
  Calendar"; writable calendars only), All Day switch, Start date + time,
  End date + time (`NSDatePicker` text-field style), time-zone link (a
  popover with an IANA zone search field; default the system zone), Repeat
  popup (Core choices; Custom… is a small popover with interval and days),
  Location field + "Add Video Call ▾" (providers from the calendar's
  `onlineMeetingProviders` → `videoCall.mode create`; Paste Link… →
  `link`), Attendees: `RecipientTokenField` + `RecipientSuggestionsController`
  reused unchanged (tokens with ×, invalid red, `contact.search`
  suggestions; the Core `RecipientTokens` model), Availability rows per
  attendee (busy grey, proposed slot green, unknown people "availability
  unknown", the summary line and Next Free Slot; shown only when the
  calendar's account has `features.freeBusy`), Alert popup, Notes
  (plain-text `NSTextView`).
- All-day alerts (owner, 2026-10-16): the Alert popup of an all-day event
  offers None, 1 day before (9:00), 2 days before (9:00), 1 week before
  (9:00), stored as minutes before its midnight (900, 2340, 9540; the
  daemon takes nothing negative, so "on the day at 9:00" is not offered):
  `AllDayAlertChoices`, `AlertChoicesFor`, `AlertTitleFor` (other values,
  such as Outlook's 18 h, keep the usual titles), `AlertOnAllDayToggle`
  (on: 900 unless None or an all-day choice; off: the preference's
  default), and a new all-day event starts with 900 when the preference
  has an alert (`NewEventDefaults`).
- Footer: the note from `Calendar.Editor.footer` (invitations' account
  and attachments), Cancel, primary Save / Save and Send Invitations
  (⌘S saves when there are no attendees; ⌘↩ the primary).
  In Edit mode only, a destructive button at the footer's left
  (`EditorDelete`: offered when `canDelete`, titled as the detail's
  `RemoveTitle`, "Delete" / "Remove from Calendar") asks what the
  detail's Remove asks (`RemoveQuestion`, else `DeleteQuestion` under
  `confirm-delete`), runs `event.delete` with the scope the editor was
  opened with (no second series question) and closes the window without
  the unsaved-changes question.
- Closing with changes asks (Save / Discard / Cancel); nothing is stored
  locally before Save (D6), so Discard just closes.
- New Event: ⌥⌘N, File ▸ New Event, the toolbar "+", double-click in a
  view (prefilled time), Board/assistant hand-offs later.

As built (task M7, 2026-10-16), where it differs from the above:
`EventEditorManager` keeps the frozen `openNew(_:)` / `openEdit(_:)`;
`Edit` gained `findFreeSlot` (default false; the detail's Find Another
Time sets it, Next Free Slot runs once the window is open), and an
event already being edited (same event, scope and, below All Events,
occurrence) raises its window. The windows use the main window's
calendar source; one that has not loaded yet (New Event from Mail) is
started and given up to 5 s before the window opens, so that the
Calendar popup has the writable calendars. Helper views are in
`EventEditorPopovers.swift` (time-zone search, Custom… as "Every [n]
[unit ▾]" with the week's days and Done) and `EventEditorFieldFrame` (the
attendee row's frame). The title row has no label (the placeholder
"Title"); "All Day:" stands left of its switch. Add Video Call ▾ is a
pull-down whose title is the chosen provider or the link's host, with
Paste Link… (a sheet prefilled from an https link on the pasteboard)
and Remove once there is one. ✦'s Ask the Assistant… only opens the main
window's assistant panel (the Calendar's or Mail's, as the window
shows); the event is not handed to it. A conflict on Save shows the
Core text with Reload Event (`Cal.Text.reloadEvent`): the window closes
without asking and the event reopens as the daemon has it (the edits
are dropped). ⌘S and ⌘↩ arrive as the menu bar's Save Draft and Send
(the primary is the default button, Return; Notes grows from 72 pt to fill the window's height). The availability axis is 8:00–18:00
of the start's day in the system zone, labels every two hours.

### 5.7 Artboard 4 · Invitation in Mail

- **List pills**: `MessageList/MessageCellView` shows
  `Shared/InvitationPill.swift` (a variant of `IssuePill`: Invitation
  blue, Update orange, Cancelled red with the subject struck through,
  Reply grey) from `MessageSummary.invitation`.
- **Card**: `MessageView/InvitationCardView.swift` above the header
  (like `IssueCardView`), `InvitationDayStripView.swift`, and
  `MessageViewController+Invitation.swift` (shown when the account's
  `invitationCard` is on and `message.get` has `invitation` with state not
  `invalid`), also in the message window and in conversation cards
  (`ConversationCardView`) over `InvitationController`: date tile, pill +
  bold title, clock line, location line with Join Video Call, people
  line, the conflict strip, the day strip (working-hours axis, busy grey,
  proposed slot dashed blue), the bottom row "Add to:" popup (or a label
  with `fixedTarget`) + Accept (primary) / Tentative / Decline (menu with
  Decline Without Reply), the reply note and Propose New Time… (only
  `canProposeNewTime`: a small popover with start/end). After answering:
  the state strip "✓ Accepted · in calendar Work · Calendar" + Change
  Response ▾ + Show in Calendar. Reply messages show a one-line card
  ("Jana Nováková accepted"); cancellations "This event was cancelled" +
  Remove from Calendar (`invitation.applyCancel`).
- The user's own event (`InvitationSummary.ownEvent`: the organiser is
  one of the account's addresses; state `processed`, `canRespond` false)
  keeps the card (date tile, title, time, "Organiser …") with the state
  line "You organise this event." (`StateLine`, first of its cases) and
  Show in Calendar when the event is stored (`ShowsInCalendar`: `eventId`
  and own or answered).
- An update shows what changed under the clock line (owner, 2026-10-16),
  each line only when `previous` has that field: "Previously %s" (the
  former time), "Previously at %s" (the former location), "Previously
  repeated: %s" (the former rule's `RecurrenceText`) — `PreviousLines` in
  `ui/internal/calendar/invitation.go`.
- The card's texts are labels with `stringValue`; nothing of the part is
  rendered as HTML; the message body below is the usual one.
- As built (M8): `MalachiCore/Model/InvitationReading.swift` (Swift-first)
  holds `invitationPill(_:)` (the row's pill from `ListRow.message`, on a
  conversation row its newest member), `invitationReading(_:cardOn:)`
  (the card of `Message.invitation` when `Cal.cardModeOf` gives one),
  `showsInvitationCard(_:)` (`config.calendar.invitationCard`, nil = on),
  `Cal.cardClockLine` (the clock line with `recurrenceText`) and
  `Cal.dayStripOf` (the strip's fractions, whole-hour ticks ≤ 13, busy ≤ 48,
  no strip for a span under an hour or over two days). The views share an
  `InvitationCardPresenter` (`MessageViewController+Invitation.swift`) over
  the window's calendar source (`AppState.calendarSource(for:)`, started on
  first use); the card of an answer stays until message.get brings a new
  one. Deviations: in a conversation card the invitation card sits under
  the card's sender row (inside what folds), not above it; an attached
  message (`message/rfc822`) shows none; Remove from Calendar and Show in
  Calendar show only for a linked event (`eventId`); Propose New Time
  answers Tentative; Change Response is a pull-down (Accept, Tentative,
  Decline, Decline Without Reply), hidden without `canRespond`; without
  conflicts the strip reads "No conflicts in your calendars". Show in
  Calendar: `MainWindowController.showInCalendar(eventID:recurrenceID:day:)`
  (`MainWindowController+ShowInCalendar.swift`: Calendar mode, the Day view
  with the event selected, `EventDetailPopover.show`).

### 5.8 Artboard 6 · Preferences → Calendar, Artboard 7 · Add CalDAV sheet

**Owner decision (2026-10-16), replacing the placement below:** the
calendar's settings are a Preferences tab of their own, **Calendar**,
top to bottom: the group **Display** (Week Starts On, Default Duration,
Default Alert, Show Declined Events, Working Hours; no longer in
General); one section per account with the `calendar` capability,
headed by its name and kind capsule, with its Calendar rows (sync
switch for Graph, the calendars, Default Calendar for New Events, Keep
Events) and, for a mail account, its Invitations rows (card, tentative,
After Answering); a CalDAV account's section has instead its account
rows (server, username, Replace Password…, mail account for
invitations, certificate with Forget) and Remove Account; at the bottom
Add CalDAV Calendar…. Without such accounts an empty state "No calendars
yet." replaces the sections. **CalDAV accounts are not in the Accounts
tab.** The structure is `PreferencesLayout` in
`ui/internal/calendar/preferences.go` (with `ShowsInAccountsTab`). The
bullets below are the earlier placement; their row details still hold.

Preferences → Calendar is the only place that lists disabled calendars
(their checkbox switches them back on); everywhere else
`ActiveCalendars` applies (§5.2).

- **Accounts pane** (`Preferences/AccountsPaneViewController.swift`
  edits; new `Preferences/CalendarAccountGroups.swift`): the account list
  shows the kind capsules (CALDAV new); "+ ▾" gains Add CalDAV Calendar…;
  for a Graph account the groups Calendar (switch "Sync This Account's
  Calendars" — switching on when the grant lacks consent starts the
  re-sign-in with `calendar: true` after a short explanation; the
  calendars with checkbox (`disabledCalendars`), colour, name and
  "default" / "read only" / "shared · %s"; Default Calendar for New Events;
  Keep Events) and Invitations (the two switches and After Answering);
  for an IMAP account a dashed inset "This account has no calendar." with
  Add CalDAV Calendar…; for a CalDAV account an Account group (name,
  server, username, Replace Password…, mail account for invitations,
  certificate row with Forget for a pin) and the same Calendar group.
- **General pane** (`Preferences/GeneralPaneViewController.swift`
  edits): group Calendar over `CalendarPreferencesController`.
- **Add CalDAV sheet** (`AccountWizard/CalDAV/CalDAVSheetController.swift`,
  560×560) over `CalDAVWizardController`: icon, title, subtitle; fields
  Account Name, Server Address, Username, Password (with the keychain
  note: it goes to the daemon's keyring, the UI never reads it back);
  Discover Calendars (`account.discoverCalDAV` then
  `account.listCalDAVCalendars`); the list with checkboxes, colour,
  name, default, read only; the status line ("Found through
  /.well-known/caldav · 4 calendars"); the Certificate row ("Verified
  (Let's Encrypt R11)"; on `tlsError` the existing Trust Certificate…
  offer with the SHA-256 fingerprint, Core `CertTrust.swift`, then
  discovery again with `certificateSha256`); the mail-account popup for
  invitations when `scheduling` is `client`; footer note; Cancel / Add
  (`account.add`).
- As built (M9, 2026-10-16, after the owner's decision that replaced
  artboard 6's placement): Settings has a **Calendar** page after General
  (`Preferences/CalendarPaneViewController.swift`, `calendar.
  PreferencesLayout`): the group Display (the former General → Calendar
  rows; Working Hours is a start and an end at half hours plus seven day
  toggles, the last day stays on), a section per account with the
  calendar capability (`CalendarAccountGroups`: its name and kind
  capsule; a CalDAV account's Server and Username shown, not edited,
  Replace Password… — an alert with a secure field, `account.update` with
  `credentials.password` —, the mail account (None + the `compose`
  accounts), Certificate with Forget, Remove Account with the Accounts
  page's question; the Calendar rows; the Invitations rows for a Graph
  account only), the empty state, Add CalDAV Calendar… (the sheet; the
  page follows the source's snapshot after Add). The Accounts page lists
  no CalDAV account (`Cal.Prefs.showsInAccountsTab`; a reorder appends
  them) and has no calendar groups or Add CalDAV Calendar…. Consent's
  Sign In… calls `account.oauthStart {accountId, calendar: true}` and
  opens the page in the browser, as the sync banner does (the mail wizard
  cannot ask for the calendar scopes). The texts are Core's msgids, so the
  tentative subtitle, the sheet's subtitle and the password note read as
  the Go reference has them, not as the artboards. App entries
  (`App/Integration+CalDAV.swift`): `AppState.showAddCalDAVCalendar(from:
  onDone:)` for the sheet, `AppState.showCalendarPreferences()` for the
  Calendar page.

### 5.9 Artboard 8 · Small integrations

- **Board → Today** (`Board/BoardTodayViewController.swift` edits, new
  `Board/BoardTodayCalendarStrip.swift`): the strip "TODAY IN CALENDAR"
  from `Calendar.Today` over the window's calendar source (today's
  `event.list` and `invitation.list`): rows with time range, colour dot,
  title, "took place" / Respond (opens the invitation card's actions in a
  popover) / place, and "Open Calendar ›". Hidden when no account has the
  capability.
- **Assistant panel** (`MalachiCore/Assistant` port of the Go
  `PanelActions`/`AllowedTools`; `MalachiMail/Assistant` chips): What's on
  Today and Find a Free Slot chips; a tool result of `list_events` /
  `get_event` in the stream gives the answer an "Open in Calendar" card
  (Calendar mode, Day view of the first event's day); the footnote of
  the bridge tools.
- **Notifications** (`Notifications/NotificationService.swift` +
  `App/Integration+Calendar.swift`): categories `EVENT_ALERT` (Join only
  with a video call — opens the link confirmation in the app; Snooze →
  `event.snoozeAlert` 5 min), `INVITATION` (Accept → `invitation.respond`
  with the default target; Show), `CANCELLATION` (Remove from Calendar →
  `invitation.applyCancel`). Click: an alert opens Calendar mode with the
  event's popover; an invitation or cancellation opens the message in
  Mail. Sound as for mail; `notify.eventAlert dismissed` withdraws.
  Respects Settings → Notifications as mail does.
- As built on macOS (M10): Respond in the Today strip selects the
  invitation's message in Mail, where its card answers it (no popover
  with the card's actions); the strip is a row of the Today table after
  the greeting and loads today's `event.list` itself. The answer's card
  is labelled with the existing msgid "Show in Calendar" (no "Open in
  Calendar") and shows the event's day; it comes from the first
  `list_events` / `get_event` result of a turn, read only from the
  bridge's own lines before the fence (`Assistant.parseCalendarResult`).
  Find a Free Slot waits for the user's words like Draft a Reply…, What's
  on Today sends at once. An alert with a video call has the category
  `EVENT_ALERT_JOIN` (Join, Snooze), one without `EVENT_ALERT` (Snooze),
  since a category's actions are fixed; both report the close button,
  which dismisses the alert. `INVITATION` (Accept, Show) is used for an
  unanswered invitation or update (its title "%s updated the event"),
  `CANCELLATION` for a cancellation not yet processed; Accept and Remove
  from Calendar read the card with `message.get` and run it through an
  `InvitationController`. The macOS daemon gets `MALACHI_TIME_ZONE` (the
  user's zone) from `DaemonSupervisor.environment`.

### 5.10 Sample data and the development script

`MALACHI_CALENDAR_SAMPLES=1` (read once at start, like
`MALACHI_BOARD_SAMPLES`) makes every window's calendar source the
`InMemoryCalendarSource` over `CalendarSamples` (ported from
`ui/internal/calendar/samples.go`): accounts Work (`M365`: Calendar,
Birthdays, Czech holidays), Gmail (`GOOGLE`: Personal, Family — invented
layout data only, no Google provider exists), Nextcloud (`CALDAV`: Sport);
events of the current week relative to now (overlapping meetings, a
two-day all-day event, a recurring standup, an unanswered invitation, a
tentative one, a declined one, an event with a video call), three
unanswered invitations and one sample `InvitationCard` for each state.
Writes say "the preview cannot do that" (as the board's samples do).
`MainWindowController+DevStart.swift` (`MALACHI_START`) gains the steps
`calendar:day|week|month|agenda`, `event-popover`, `event-editor`,
`invitation-card` (a window with the sample cards), `caldav-sheet`, each
printing the frames of the toolbar items, dividers and the main views,
for layout checks with the debug binary only (never the owner's
`build/Malachi Mail.app`, never his data; the `CLAUDE.md` procedure).

### 5.11 What must stay portable

Everything in §5.1 is the port's contract; the macOS-only parts are the
views, the toolbar and the notification categories. Deviations from the
GTK reference to be listed in `macos/README.md` when GTK comes: unified
toolbar with the view segments, `NSPopover` detail, notification actions.

---

### 5.12 The GTK client as built (tasks G1–G20, 2026-10-17)

The GTK client follows §6.5. What each task left, where it differs from the
macOS client or the plan:

- **G1** (`ui/internal/calendar/minimonth.go`, `reading.go`;
  `ui/internal/assistant/calendar_result.go`): pure ports of
  `MiniMonth.swift`, `InvitationReading.swift` and `AssistantCalendar.swift`
  with their tests — `MiniMonth`/`NewMiniMonth`/`StepMonth`,
  `InvitationPillOf`, `ShowsInvitationCard`, `CardClockLine`, `DayStripOf`
  (whole hours on the zone's clock, so half-hour zones tick right),
  `CalendarRef`, `ParseCalendarResult`. No new msgids.
- **G2** (`ui/internal/calendardata`): the `DataSource` interface (frozen:
  `Snapshot`, `Observe`, `Refresh`, `Handle`, `Occurrences`,
  `CachedOccurrences`, `Event`, `Search`, `FreeBusy`, `FindFreeTime`,
  `SetVisible` without `done`, `SetColor`, `Sync`, `SetPreferences`,
  `CreateEvent`, `UpdateEvent` (also move), `DeleteEvent`, `Respond`,
  `SnoozeAlert`, `DismissAlert`, `RespondToInvitation` (also propose),
  `ApplyCancel`, `DiscoverCalDAV`, `ListCalDAVCalendars`, `AddAccount`,
  `UpdateAccount`, `ImportAttachment`), `Snapshot` helpers, `Phase`,
  `Change`, `ErrPreview` (`calendar.PreviewOnly`), `InMemorySource` over
  `calendar.SampleData` (+ `samples_remote.go` `WithRemoteIDs`). `Range`
  carries `Loc`; `ZoneName` turns Go's "Local" into an IANA name (TZ or
  `/etc/localtime`) because `event.list` refuses "Local". `AsError` maps
  transport errors to `*api.Error`. `Occurrences` carries no `complete`
  flag, so the GTK page shows no "some events may be missing" line.
- **G3** (`calendardata/daemon.go`): `NewDaemonSource(board.Caller,
  board.Loop, DaemonOptions{RetryDelay, CallTimeout, Loc, Log})`, `Start`,
  `Stop`, `AccountsChanged`, `ConnectionChanged`, `ConflictEvent(*api.Error)`.
  Only a failed load is retried (`board.RetryDelay`); offline is
  `PhaseUnavailable` and reloads on `ConnectionChanged(true)`. `SetVisible`
  also sends `ChangeOccurrences` on the optimistic change and on rollback.
  A range dropped while in flight is forgotten (a new ask starts a fresh
  call; Swift joins the stale one). Timeouts: `event.list` 10 s, free/busy,
  find free time and CalDAV discovery 20 s, `account.add`/`update` 30 s,
  `attachment.import` 60 s.
- **G4** (`settings/store.go`, `window/board.go`, `mode_keys.go`,
  `window.go`, `preferences_board.go`, `window.blp`, `board_page.blp`,
  `widget/format.go`): `KeyCalendarView`, `BoardStartCalendar`; three modes
  through one helper; Ctrl+3 and Ctrl+Shift+1–4 by physical key
  (`board.NumberRowDigit` covers 3 and 4), `win.show-calendar`, Calendar in
  "Open at Launch"; a third toggle in both switches, the `calendar_page_bin`
  page; `widget.Strftime` exported; with Show the Board off the Board
  toggle is insensitive and the switch stays. The board GTK smoke steps
  through all three modes. Open: on a Czech layout `<Control>1` might also
  fire on Ctrl+Shift+1 before the calendar's handler (unchecked by hand).
- **G5** (`calendarctl/options.go`, `calendar.go`): `Options{Env, Now,
  Loop, LocaleFirstDay, RPCText, View, SetView}` with `Clock()`, `Loc()`,
  `After()`, `ErrorMessage()` — frozen; `CalendarController`
  (`Start`/`Stop`, `SetView`, `Step`, `Today`, `GoTo`, `Show`,
  `StepMiniMonth`, `LoadMoreAgenda`, `ShowUnanswered`, `ClearFilter`,
  `SetVisible`, `SignIn`, `Select`, `MoveSelection`, `OpenDetail`,
  `NewEvent(At)`, `SetSearch`/`ClearSearch`, `HandleKey`, `Reload`;
  callbacks `OnChange`, `OnError`, `OnOpenDetail`, `OnRemove`, `OnNewEvent`,
  `OnClosePopover`, `OnSignIn`), `Sidebar`/`SidebarAccount`/`SidebarCalendar`,
  `SearchMinimum`, `SearchDebounce`, `AgendaPage`.
- **G6** (`calendarctl/detail.go`, `invitation.go`): the package's `Answer`
  (`AnswerCancel`, `AnswerConfirm`, `AnswerAlternative`) lives in detail.go;
  `EventDetailController` (`NewEventDetailController`, `NewEventDetailFor`),
  `InvitationController` (`TargetChoice`, `DefaultInvitationTarget`);
  `OnShowInCalendar(eventID, rec, day)` passes the recurrence id;
  `InvitationController.Stop()` silences `OnChange` after the card is gone;
  a conflict uses the error's `ConflictEventData`, else reloads.
- **G7** (`calendarctl/editor.go`): `NewEventEditor(src, opts, *Defaults,
  calendarID)`, `EditEventEditor(src, opts, event, scope, findFreeSlot)`;
  the editor never asks the scope (the detail chose it); `findFreeSlot`
  runs Next Free Slot from `Start`; a conflict sets `Conflict()` and
  `Reload()` hands the fresh event to `OnReload(fresh, scope)`; Save also
  checks the attendee field's pending text (`Resolved()`); `OnError`
  repeats each inline error.
- **G8** (`calendarctl/caldav.go`, `preferences.go`, `account.go`,
  `alerts.go`): `CalDAVWizardController` (`OnConfirmTrust(TrustRequest,
  func(bool))` — only the first answer counts; `ErrorField` marks the
  password for a refused sign-in, else the server), `CalendarPreferencesController`
  with `PreferenceChoice[T]`, `CalendarAccountController` (`CalendarRow`;
  `OnSignIn` and `OnRemove` hand the work to the window — the source has
  no `oauthStart` nor account removal), `CalendarAlertsController`
  (`AlertRequest`, `NotificationCategory`, `InvitationNotificationOf` from
  `Message.Invitation`; `invitationsChanged` is recognised, nothing shown).
  `account.go` uses a literal `tr.T` ("Saving the account") → POTFILES.
- **G9** (`window/calendar.go`, `calendar_page.blp`, `calendar_keys.go`,
  `calendar_notify.go`, `status.go`, `style/calendar.go`, `main.go`,
  `preferences.go`, `data/data.go`, the stubs of wave C2 and the smoke
  `calendar_gtk_smoke_test.go`): the page is an `Adw.BreakpointBin` →
  `Adw.OverlaySplitView` (sidebar 240) whose header gives way in four
  breakpoints (900, 720, 560, 460 sp; at 900 a view drop-down
  `calendar_view_menu` replaces the toggles); the search entry is only
  ever cleared by the controller, never overwritten while typing; the
  window's one calendar source lives in `w.cal` and Preferences gets it
  through `w.CalendarSource()`; notifications are routed through
  `otherNotification`/`modeAccountsChanged`/`modeConnectionChanged`; the
  sidebar toggle has no tooltip (no msgid fits — "Show Sidebar" would be
  new). Frozen stub signatures: `newCalendarSidebar(page) *calendarSidebar`
  + `update(calendarctl.Sidebar)`; `newCalendarAgenda(page)`,
  `newCalendarWeek(page, day bool)`, `newCalendarMonth(page)` each with
  `update()` and `anchorFor(key string) gtk.Widgetter`;
  `(w *Window) showEventDetail(occ, anchor)`, `closeEventDetail()`,
  `removeCalendarOccurrence(occ)`; `openEventEditor(defaults, calendarID)`,
  `editEvent(e, scope, findFreeSlot)`; `showAddCalDAV(parent, onDone)`;
  `askAboutEvent(e)`, `revealCalendarAssistant()`; `calendarAlert(raw)`.
  The GTK smoke tests need `xvfb-run -a -s "-screen 0 1600x1200x24"`
  (the default 640×480 screen cannot hold a 1280-wide window; `gtk.sh
  test-ui` passes it).
- **G10** (`calendar_sidebar.go`, `calendar_agenda.go`,
  `style/calendar_sidebar.go`): the Agenda and the Unanswered list sit in
  `agenda_bin`, the results in `search_bin`, two lists built the same way;
  paging starts on scroll as on macOS; the sidebar has its own
  `calendarAccountBadge` (the Mail `accountHeaderBadge` has no CALDAV case).
- **G11** (`calendar_week.go`, `calendar_allday.go`,
  `style/calendar_week.go`; `calendar/block.go` adds `BlockTitle`,
  `BlockPlace`): blocks and all-day bars are buttons without the `flat`
  class — Adwaita's `button.flat:hover` would outrank the opaque fill and
  let the grid show through a hovered block — so `style/calendar_week.go`
  strips the button look itself; a block shows only the lines that fit its
  height and clips the rest. Measured: grid 1030×1440, overlap columns,
  the now line last, first scroll 410 and 410 again after a resize, the
  all-day bar of "Team offsite" spanning two columns.
- **G12** (`calendar_month.go`, `style/calendar_month.go`): each week row
  is an Overlay over a DrawingArea (shading, grid lines, the resize probe)
  with a seven-column grid for day numbers, bars and pill boxes; pills and
  bars are 17 px + 1 px gap; the double-click gesture sits on the Overlay
  and derives the column from x; a row with no room still shows its
  "+N more" line (clipped), as macOS. Measured: a bar across a week
  boundary (before/after flags), capacity 3 at 129 px rows, 0 at 72 px.
- **G13** (`calendar_detail.go`, `event_detail.blp`,
  `style/calendar_detail.go`; `calendar.go` gained `windowCalendar.detail`
  and `.reveal`, `calendar_keys.go` passes `w.eventDetailShown()`): links go
  through `openEventLink` — `mailto:` opens a new message, anything else
  asks "Open This Link?" first (plain `w.openLink` without a links list
  would open unasked); while a question is up the popover closes so it
  cannot cover the `Adw.AlertDialog`, and comes back after the answer; the
  alert row shows `AlertTitleFor` titles joined by `Dot` (no alert-row
  msgid exists). Measured: width 380, rows time/location/video/people/
  notes/invitation/footer.
- **G14** (`event_editor.go`, `event_editor_popovers.go`,
  `event_editor_availability.go`, `event_editor.blp`,
  `compose/attendee_field.go`, `style/calendar_editor.go`; `compose/suggest.go`
  got a hook so the attendee field supplies its own contact search): an
  `Adw.Window` 750×790 (the header bar takes the rest of 760×800); zones
  from tzdata's `zone1970.tab`/`zone.tab` plus UTC and the local zone;
  Custom… is a popover on the Repeat drop-down; times are hour/minute
  spin buttons; Escape closes unless suggestions are showing; Find a Free
  Slot, Ask the Assistant… and Assistant come from the `assistant` texts,
  Done from `board.Done`; a literal `i18n.T("Remove")` in
  `event_editor_popovers.go` → POTFILES. Measured: label column 110,
  footer Delete at x 20, Cancel/Save at the right, the all-day alert items.
- **G15** (`invitation_card.go`, `invitation_card.blp`,
  `style/calendar_card.go`, `message_view.go`, `conversation_card.go`,
  `widget/message_row.go` + `.blp`, `widget/pill.go`, `folders.go`,
  `window.blp`, `message_window.blp`; one line each in `model.go`,
  `thread_model.go`, `style/style.go`): no `Adw.AlertDialog` (the
  controller asks no question); calendar-only accounts are filtered in
  `enabledAccounts` through `listedInMail`; the card CSS lives in the main
  stylesheet. Measured: the unanswered card 842×238 with the day strip,
  the reply one-liner, the own-event line with Show in Calendar, the
  accepted strip.
- **G16** (`preferences_calendar.go`, `preferences.blp` `calendar_page`,
  one bind line in `preferences.go`, `accounts_page.go`): an
  `Adw.PreferencesPage`; each account gets its own group (name + kind
  capsule) and an Invitations group; calendar colours are drawn squares,
  not the CSS classes; Add CalDAV Calendar…, Sign In… and Remove go
  through the main window (found via the board-triage state; without a
  window the "preview cannot do that" toast); the working day's Starts /
  Ends rows reuse the editor's "Starts:"/"Ends:" with the colon trimmed
  (no own msgid — TODO(G20) if the owner wants proper ones).
- **G17** (`calendar_caldav.go`, `caldav_sheet.blp`;
  `accountwizard/trust.go` gained the exported `CertificateDetails`
  wrapper): Cancel and Add sit in the header bar; the error card is a
  full-width wrapping box; found calendars carry no colour dot; the Trust
  Certificate… question reuses the account assistant's msgids. Measured:
  560×560, error 447 wide, four phases.
- **G19** (`board_today_calendar.go`, `calendar_assistant.go`,
  `calendar_assistant_move.go`, `assistant_panel.go`, `assistant.go`,
  `assistantpanel/controller.go` `RunCalendar`/`ContentCalendar`,
  `calendar_page.blp` wrapped in `calendar_assistant_split`): the strip
  stays visible on a day without events ("Nothing in the calendar
  today.", as macOS); a row opens the event in Calendar; a panel open in
  Mail moves into the Calendar page with its transcript and back.
- **Width** (after the first Broadway run, 2026-10-17): the content stack
  asked for 1038 px at a 1024 px window because pill, bar and block
  labels had no ellipsis; now every such label ellipsizes
  (`max-width-chars` 1, `hexpand`), and the views' measured minimums are
  Week 242, Day 84, Month 91 px, asserted in their smoke tests, so the
  page reaches its 560/460 sp breakpoints.
- **Looking at the GTK client on the Mac**: `gtk4-broadwayd` in the
  container + the app with `GDK_BACKEND=broadway` (the same
  `XDG_RUNTIME_DIR` for both; `GSETTINGS_BACKEND=keyfile` presets
  `window-width/height`, `board-start-mode` and `calendar-view`;
  `gtk-enable-animations=0` in `gtk-4.0/settings.ini`), opened in a
  browser. Broadway's screen is the browser viewport at connection time
  (a 1280 window gets clamped to 1024 if the page connects late), and
  automated clicks are unreliable (only the first after a page load
  registers), so the views are checked by starting the app in each view
  and the other windows through `MALACHI_GTK_START` (G9b), never by
  clicking.
- **G9b** (`window/devstart.go`): `MALACHI_GTK_START` = steps separated by
  `;` run after the window shows, `MALACHI_GTK_START_INTERVAL` seconds
  apart (default 3), nothing without the variable: `mail`, `board`,
  `calendar`, `calendar:day|week|month|agenda`, `event-popover`,
  `event-editor`, `event-editor:edit`, `caldav-dialog`, `prefs-calendar`,
  `size=WxH`, `quit`; each prints `MALACHI_GTK_START step <n> <name>` and
  `LAYOUT:` lines (page, sidebar, content, header minimum, stack child
  minimums, toplevels, `dialog` for the in-window `Adw.Dialog`s). The
  width finding was not the calendar's: the hidden Mail page of
  `mode_stack` asked for 1038 px and the stack sized itself to it in every
  mode; `mode_stack` is now `hhomogeneous: false`/`vhomogeneous: false`
  (calendar page minimum 360, 536 with the header at a folded sidebar).
  Mail alone still needs 1038 px between its own breakpoints — a
  pre-existing limit, outside the calendar.
- **Preferences on load** (G16 fix): the Keep Events and After Answering
  rows were filled while change tracking was on, so the first item was
  auto-selected and saved (the samples refuse writes and the error
  landed in the group's description); lists are now filled with tracking
  off and errors show as a toast only.

## 6. Work breakdown

**Status after the daemon phase (2026-10-16).** Implemented, with the
"As built" notes above: the contract (T1, with the Swift and C# mirrors
M1 and T1w), `calendar/ics`, `tz`, `recur`, `imip` and the event model
(T2 to T4), migration 0020 and the store (T5), the CalDAV client and
provider (T6a, T6b), the calendar scopes (T7a), the Graph provider and
the shared limiter (T7b), the engine (T8), core wiring, services and
invitations (T9a to T9c), the MCP tools (T10), board rules "8" (T12), the
Go reference `ui/internal/calendar` and the mode (U1, U2), and the
documentation (T11). Not tested live: CalDAV against a real server (only
`caldavtest` and its profiles), Graph beyond T0's read-only calls (writes,
responses, `getSchedule` of others, the calendar scopes' consent, the MIME
shape of a meeting request), answering an invitation through a Graph
event (no Graph fake in core), the real IMAP path of `summaryOf` with an
invitation, `TestBoardDryRun` with rules "8" and `BenchmarkSearchPrefix`;
the C# mirror is written without a compiler. Gate 1 checks, over a copy of
the owner's store with its own `MALACHI_SOCKET` (0020 is irreversible;
the first start re-evaluates the board under rules "8"): his Microsoft 365
account with calendar sync switched on (consent screen, calendars,
events, alerts, an invitation in Mail, accept or decline to an address he
chooses), a CalDAV account against Nextcloud or Radicale (discovery,
certificate trust, sync, create with and without attendees), the MCP
tools from Claude Code (with and without `--allow-modify` /
`--allow-send`), and Windows CI on the branch.

Each task is sized for one agent. **Files** lists everything the task may
create or edit; two tasks that may run at the same time never share a
file. A task hands over with its tests green (the ones this Mac can run:
Go everywhere; Swift on the Mac; GTK and C# per `CLAUDE.md`), says which
tests it ran, and does not commit (`CLAUDE.md`: commit only after the
owner's check). Heavy runs (fuzzing beyond the seed corpus, `-race` over
`core`, full suites) are announced and asked for first (auto-memory rule).

### 6.1 Order and parallel groups

```
G0  T0 (owner + agent, any time before gate 1)
A   T1  contract                                    ── everything below needs it
B   T1w  T1s=M1  T2  T3  T5  T6a  T7a  T10  U1  U2   (parallel)
C   T4                    (after T2, T3)            P1 (after U1, U2)
D   T6b  T7b  T8          (after T4, T5; T7b also T3)
E   T9a                   (after T6b, T7b, T8, T7a)
E'  T9b  T9c  T12         (after T9a, parallel; T12 needs only T5)
F   T11                   (after T9b, T9c)
    ── gate 1: the owner reviews the daemon (live test T0, a CalDAV server, a store copy)
G   M2                    (after M1, P1)
H   M3                    (after M2)
I   M4                    (after M3)
J   M5  M6  M7  M8  M9    (after M4; M8 and M9 need only M3 and may start with I)
K   M10                   (after M4, M6)
L   M11                   (after M5–M10)
    ── gate 2: the owner tests macOS on the Mac over a copy of his data
    ── then, on his approval, the GTK and Windows ports (§6.4)
```

### 6.2 Tasks

**T0 · Graph spike and prerequisites** (owner with an agent)
- Owner: add the delegated permissions `Calendars.ReadWrite` and
  `Calendars.Read.Shared` to the Entra registration of the built-in client
  (`af56e4e6-…`); approve removing "calendaring" from the README's
  non-goals.
- Agent files: `backend/internal/graph/calendar_live_test.go` (skipped
  unless `MALACHI_GRAPH_LIVE=1` and a token in `MALACHI_GRAPH_TOKEN`,
  read-only calls through the existing `graph.Client`), reporting:
  per-calendar `calendarView/delta` works or not, all-day value shape,
  `originalStartTimeZone` values, whether a meeting request's `$value`
  MIME has a `text/calendar` part, `getSchedule` on a personal account,
  calendar 403 shape without the scope.
- The owner runs it on his account; the agent writes the findings into
  §3.6 of this document. Nothing else depends on it until T7b, which
  keeps both paths (D14) whatever it finds.

**T1 · Contract** (serial, first)
- Files: `backend/pkg/api/calendar.go` (new), `types.go`, `errors.go`,
  `methods.go`, `service.go`, `api_test.go`, `calendar_test.go` (new),
  `backend/internal/rpc/dispatch.go`, `rpc/stub.go`,
  `backend/internal/calsync/provider.go` (new: the interface and its
  types, §3.7, no implementation), `backend/internal/core/calendar_stub_accounts.go`,
  `calendar_stub_calendar.go`, `calendar_stub_invitations.go` (new:
  `notImplemented` for every new method and the `Backend` accessors, so
  that later tasks each delete their own stub file), `docs/api.md`.
- Tests: `TestDocsCoverAllMethods`, `TestErrorCodesUnique`, a round trip of
  every new type with optional fields absent and present, enum value
  sets, `rpc` dispatch answering `notImplemented` for each new method,
  `go vet ./...`.

**T1w · Windows API mirror** (group B; no UI)
- Files: `windows/src/Malachi.Core/Api/Calendar.cs` (new) and the existing
  `Api/*.cs` that hold the method table, notifications, error codes,
  `AccountKind`, `Capability`, `AccountConfig`, `MessageSummary`,
  `Message`, limits; `windows/tests/Malachi.Core.Tests/Api/GoContractTests.cs`
  (method count, limit count, `WireEnums` rows), `ApiCodingTests.cs`
  (`GoMethods`, `GoCodes`), `ApiRoundTripTests.cs` (samples of every new
  record).
- Why now: those tests compare the C# mirror with Go's `methods.go`,
  `errors.go` and enums, so T1 alone turns Windows CI red. Written on the
  Mac without `dotnet`; the owner triggers `windows.yml` on the branch.

**M1 (= T1s) · Swift API mirror** (group B)
- Files: the API rows of §5.2 (`MalachiCore/API/CalendarAPI.swift`,
  `Accounts.swift`, `Messages.swift`, `ErrorCode.swift`,
  `Notifications.swift`, `API.swift`), `Tests/MalachiCoreTests/APICodingTests.swift`
  (`goMethods`, codes), `APICodingTests+Calendar.swift` (new).
- Tests: `swift test --filter APICoding` (with the plugin path noted in
  `CLAUDE.md` on this Mac).

**T2 · iCalendar parser and encoder** (group B)
- Files: `backend/internal/calendar/ics/*`, `backend/testdata/calendar/*`
  except `rrule-*.ics`.
- Tests: grammar and unfolding, every limit, round trip, every fixture
  parses without panic and with the expected problems, `FuzzParse` seeded
  with the corpus (a short run only).

**T3 · Time zones and recurrence** (group B)
- Files: `backend/internal/calendar/tz/*` (with the generator
  `tz/internal/genwindows/main.go` and the generated `windows_zones.go`
  committed), `backend/internal/calendar/recur/*`,
  `backend/testdata/calendar/rrule-*.ics`, `backend/go.mod`, `go.sum`
  (`github.com/teambition/rrule-go`), `backend/cmd/malachid/main.go`
  (`import _ "time/tzdata"`).
- Tests: TZID resolution table (IANA, Windows, prefixes, offsets match,
  fixed fallback), VTIMEZONE writer parsed back by T2's parser (if T2 has
  not landed, a test helper parses the minimal subset), expansion tables
  with DST, every `rrule-*` fixture ends within 100 ms, `FuzzExpand` seed
  run.

**T5 · Store** (group B)
- Files: `backend/internal/store/migrations/0020_calendar.sql`,
  `store/calendar.go`, `calendar_events.go`, `calendar_occurrences.go`,
  `calendar_alerts.go`, `calendar_invitations.go`, `calendar_search.go`
  (new), `store/accounts.go` (`RealmOf` for caldav; `DeleteAccount`
  deletes `cal_*` rows), `store/messages.go` (the LEFT JOIN that fills
  `MessageSummary.Invitation` in listings, the search listing and
  `thread.get`).
- Tests: migration on a store with 0001–0019 (board triggers in place);
  writing calendar rows leaves `board_dirty` untouched; no `INSERT OR
  REPLACE` (a grep test like the board's if one exists, else add one for
  `cal_` statements); occurrence range queries (timed, all-day widened by
  ±14 h, floating); FTS search with diacritics; invitation join in
  `message.list` results; account deletion; cascade from messages.

**T6a · CalDAV client and fake server** (group B)
- Files: `backend/internal/caldav/client.go`, `xml.go`, `discover.go`,
  `sync.go`, `write.go`, `errors.go` (new), `caldav/caldavtest/*`,
  `backend/testdata/caldav/*`.
- Tests: discovery (well-known, SRV through a fake resolver, URL),
  principal/home-set/calendars per server profile, sync-collection and
  etag fallbacks, multiget batching, PUT/DELETE with `If-Match` and 412,
  every hostile profile (size caps, depth caps, redirect to another host
  never gets the `Authorization` header, 401 handling), TLS pin through
  `transport.EndpointTLSConfig` with `transporttest`.

**T7a · Microsoft calendar scopes** (group B)
- Files: `backend/internal/auth/oauth2flow/*` (the calendar scope set,
  `calendar` sessions, recording the granted `scope`), `backend/internal/auth/goa/goa.go`
  (`CalendarDisabled`), `backend/internal/core/oauth_sessions.go`
  (`AccountOAuthStartParams.Calendar`).
- Tests: a session with `calendar` asks for the extra scopes; one
  without does not; the recorded scope says whether calendar access is
  granted; a refresh never adds a scope; GOA property read.

**T10 · MCP bridge** (group B)
- Files: `backend/cmd/malachi-mcp/tools_calendar.go` (+ `_test.go`),
  `main.go` (registration, the instructions text), `fake_backend_test.go`
  (calendar methods), `docs/mcp.md` (tools section).
- Tools: `list_calendars`, `list_events` (`from`, `to` ≤ 62 days,
  `calendarIds`, `query`), `get_event`, `find_free_time`
  (`durationMinutes`, `from`, `to`, `attendees`, `workingHoursOnly`),
  `list_invitations` — read-only, always registered; `create_event`
  behind `--allow-modify` (no attendees), with attendees only when
  `--allow-send` is also set (invitations are outgoing mail); not under
  `--reply-only` or `--triage-run`. Every string through `clean()` and
  the nonce fence. Tests against the fake backend: tool lists per flag
  combination, fencing, refusal texts without mail content.

**U1 · Go reference `ui/internal/calendar`** (group B)
- Files: `ui/internal/calendar/*` (§5.1, with tests and `po_test.go`).
- Tests: the tables of §5.1; `po_test.go` is expected to fail until P1.

**U2 · Mode, gschema and assistant actions in Go** (group B)
- Files: `ui/internal/board/mode.go` (+ `mode_test.go`),
  `ui/internal/assistant/calendar_actions.go` (new: the two quick
  actions, their prompts, the extra allowed tools), `assistant/button.go`
  (`PanelActions`), `assistant/claude.go` (`AllowedTools`) and their tests,
  `data/io.github.schotek.Malachi.gschema.xml`.
- Note: GTK's own window code is not touched (port later). Go switches
  are not exhaustive, so GTK still builds; until the port GTK never
  offers the Calendar mode, and a stored `calendar` start mode reads as
  Mail there (the start-mode rule of `mode.go` maps it when the client has no calendar,
  a flag the GTK window passes).

**P1 · Translations and parity** (group C, serial)
- Files: `po/POTFILES`, `po/malachi.pot`, `po/cs.po` (Czech translations
  of every new msgid), `windows/parity-exclusions.txt`.
- `make po` in the Docker image; the U1 `po_test.go` passes; the Windows
  coverage test is satisfied by the exclusions (checked by reading
  `StringsCheckTests`; CI confirms).

**T4 · Event model, expansion, free/busy, iMIP** (group C)
- Files: `backend/internal/calendar/event.go`, `expand.go`, `text.go`,
  `freebusy.go` (+ tests), `backend/internal/calendar/imip/*`,
  `backend/internal/smtp/build.go` (+ `build_calendar_test.go`),
  `backend/testdata/mime/invite-*.eml`.
- Tests: ics ⇄ Event round trips with unknown properties preserved;
  expansion with overrides, EXDATE, RDATE, caps; text cleaning and link /
  video-call detection against `url-javascript.ics`,
  `conference-http.ics`, `location-with-url.ics`; free slots and
  conflicts; `ParseMessage` over every `invite-*.eml` part and the
  `method-*.ics` fixtures; built REPLY/REQUEST/CANCEL parse back and
  match RFC 5546 required properties; the built MIME parses with
  `internal/mime` and has the calendar alternative.

**T6b · CalDAV provider** (group D)
- Files: `backend/internal/caldav/provider.go` (+ `provider_test.go`).
- Tests: the `calsync.Provider` contract over every `caldavtest`
  profile: first sync in a window, changes, deletes,
  outside-window resources, create/update/delete with scopes, respond
  with and without server scheduling (`SCHEDULE-AGENT`), conflict.

**T7b · Graph calendar** (group D)
- Files: `backend/internal/graph/calendar.go`, `calendar_sync.go`,
  `calendar_write.go`, `calendar_schedule.go`, `calendar_recurrence.go`,
  `calendar_provider.go`, `limiter.go` (new), `graph/client.go`
  (`Options.Limiter`), `graph/fake_calendar_test.go` (new).
- Tests: a fake Graph (round tripper) for calendars, delta and list modes
  and the fallback, masters, `@removed`, all-day, Windows zones, writes
  with `If-Match` and 412, respond with `proposedNewTime`, `getSchedule`
  partial answers, 403 → `calendarConsentRequired`, the limiter shared by
  two clients never exceeding 4.

**T8 · Calendar engine** (group D)
- Files: `backend/internal/calsync/supervisor.go`, `syncer.go`,
  `window.go`, `occurrences.go`, `alerts.go`, `notify.go` (+ tests and a
  fake provider in `calsync/fake_test.go`).
- Tests over a temp store and a fake clock: start/stop/restart per
  account, state machine and backoff, window move and Graph re-baseline,
  materialisation and its rebuild on `calendar.expand` change, alert
  timing (due, late within 15 min, snooze, dismiss, declined none),
  notification coalescing.

**T9a · Core accounts and wiring** (group E, first)
- Files: `backend/internal/core/accounts_caldav.go` (+ tests),
  `core/accounts.go`, `core/validate.go`, `core/dispatch.go` (`caldav`
  entry, `kindOrder`), `core/backend.go` (calsync supervisor, providers,
  limiter shared with the Graph mail syncer and outbox, the
  `Maintain` call of `backfillInvitations`), deletion of
  `core/calendar_stub_accounts.go`; `core/calendar_stub_invitations.go`
  gains a no-op `backfillInvitations` (T9c replaces the file).
- Tests: `account.add/update/test` for caldav against `caldavtest`
  (validation rules of §4.3, realm uniqueness, keyring, pin, mail
  account), `account.discoverCalDAV` and `account.listCalDAVCalendars`
  (redirect to another host shown, password not sent there), Graph
  `calendar.sync` switch (start, consentRequired, delete on off),
  capabilities, `sync.status` for caldav, `folder.list` empty.

**T9b · Core calendar and event services** (group E', parallel with T9c)
- Files: `core/calendar_service.go`, `core/event_service.go`,
  `core/calendar_freebusy.go` (+ tests), deletion of
  `core/calendar_stub_calendar.go`.
- Tests: every method of §4.4–4.5 over a temp store with the fake
  provider, then end to end over `caldavtest` and the Graph fake
  (create with attendees → iMIP in the outbox for client scheduling,
  none for server scheduling; scopes; conflict data; eventGone;
  readOnly; outside-window `complete: false` offline).

**T9c · Core invitations** (group E', parallel with T9b)
- Files: `core/invitation_service.go`, `core/calendar_imip.go`,
  `core/calendar_backfill.go` (+ tests), `core/issue_mail.go` (the one
  call in `afterMailStored`), `backend/internal/ingest/policy.go`
  (+ `policy_test.go`: calendar parts are never omitted), replacement of
  `core/calendar_stub_invitations.go` (deleted).
- Tests: ingest of every `invite-*.eml` through the IMAP fake → summary
  in `message.list` and `notify.newMessage`, card in `message.get`
  (conflicts, day strip, targets); respond on IMAP (REPLY in the outbox
  to the organiser, never the From; `afterAnswer` archive), on Graph
  (through the event), with a CalDAV target (stored with
  `SCHEDULE-AGENT=CLIENT`); spoofed reply not applied; junk without
  respond; cancel and `applyCancel`; backfill over mail stored before
  0020; never-store policy keeps the part.

**T12 · Board rules: invitations are not case members** (group E'; needs T5)
- Files: `backend/internal/board/*` (`RulesVersion` "8" and, if the rules
  see the member kind, the flag), `backend/internal/store/board_triage.go`
  and the store files that load a thread's members for the board (the
  query leaves out messages with a `cal_invitations` row), their tests;
  `docs/api.md` §4.13 "Rules" line and `docs/architecture.md` §3.7 if they
  list what counts as a member.
- Tests: a thread whose only inbound member is an invitation makes no
  case; an invitation reply in Sent does not turn a case into `them`; a
  thread with ordinary mail plus an invitation counts the mail only;
  the version bump re-evaluates (`board.rules` meta cursor restarts).
- No overlap with T9b/T9c files (they never touch `internal/board` or
  `store/board_*`).

**T11 · Documentation** (group F)
- Files: `docs/architecture.md` (new §3.8 Calendar, §7 entries for D1–D28
  in short), `docs/security.md` (§4.3 Calendar data, the §7 pin note for
  CalDAV, §10 calendar tools of the bridge, §12 checklist lines),
  `README.md` (non-goals; features), and a proposed status paragraph for
  `CLAUDE.md` handed to the owner (not written by the agent).

**M2 · MalachiCore calendar logic** (group G)
- Files: `macos/Sources/MalachiCore/Calendar/*` except the source files of
  M3, `macos/Tests/MalachiCoreTests/Calendar*Tests.swift`.
- Tests: ports of every U1 test; `scripts/check-strings.py` (macOS keys
  exist as msgids).

**M3 · Source and controllers** (group H)
- Files: `MalachiCore/Calendar/CalendarSource.swift`,
  `DaemonCalendarSource.swift`, `InMemoryCalendarSource.swift`,
  `CalendarSamples.swift`, `MalachiCore/Controllers/Calendar*.swift`,
  `EventDetailController.swift`, `EventEditorController.swift`,
  `InvitationController.swift`, `CalDAVWizardController.swift`,
  `MalachiCore/Settings/Settings.swift` (+ `SettingsTests.swift`), their
  tests.
- Tests: §5.2 controller tests over the in-memory source and `FakeDaemon`.

**M4 · Mode, toolbar and page** (group I)
- Files: `MalachiCore/Board/Board.swift` (+ the board tests that switch
  over modes), `MalachiMail/MainWindow/MainContentViewController.swift`,
  `MainWindowController+Mode.swift`, `MainWindowController.swift`,
  `MainWindowController+Calendar.swift` (new),
  `StatusBarViewController.swift`, `MalachiMail/App/MainMenu.swift`,
  `App/AppState.swift`, `MalachiMail/Calendar/CalendarToolbar.swift`,
  `Calendar/CalendarPageViewController.swift` (new; an empty content
  until M5).
- Frozen entry points for J: `CalendarPageViewController.show(view:)`,
  `EventDetailPopover.show(…)` (declared here as a stub that M6 fills),
  `EventEditorManager.openNew(_:)` / `openEdit(_:)` (stub, M7 replaces
  the file),
  `AppState.calendarSource(for:)`.
- Tests: `swift build`; mode and command tests in Core; a
  `MALACHI_START` run (`calendar:week;quit`, debug binary,
  `MALACHI_CALENDAR_SAMPLES=1`) showing the separator on the divider after
  Mail → Calendar → Board → Mail.

**M5 · Calendar views** (group J)
- Files: `MalachiMail/Calendar/CalendarSidebarViewController.swift`,
  `MiniMonthView.swift`, `CalendarTimeGridView.swift`,
  `CalendarDayHeaderView.swift`, `CalendarAllDayView.swift`,
  `EventBlockView.swift`, `CalendarMonthView.swift`, `MonthCellView.swift`,
  `CalendarAgendaViewController.swift`, `CalendarSearchController.swift`,
  `MalachiCore/Model/FolderTree.swift` (`CALDAV` capsule; caldav accounts
  left out of the Mail sidebar) and its tests.

**M6 · Event detail popover** (group J)
- Files: `MalachiMail/Calendar/EventDetailPopover.swift`,
  `EventDetailViewController.swift`.

**M7 · Event editor** (group J)
- Files: `MalachiMail/EventEditor/*` (window controller, view controller,
  `AvailabilityView.swift`, `EventEditorManager.swift`).

**M8 · Invitations in Mail** (group J; may start after M3)
- Files: `MalachiMail/MessageView/InvitationCardView.swift`,
  `InvitationDayStripView.swift`, `MessageViewController+Invitation.swift`
  (new), `MessageView/MessageViewController.swift`,
  `MessageView/ConversationCardView.swift`, `MessageList/MessageCellView.swift`,
  `Shared/InvitationPill.swift` (new),
  `Windows/MessageWindowController.swift`.

**M9 · Preferences and the CalDAV sheet** (group J; may start after M3)
- Files: `MalachiMail/Preferences/AccountsPaneViewController.swift`,
  `AccountRowCell.swift`, `CalendarAccountGroups.swift` (new),
  `GeneralPaneViewController.swift`,
  `MalachiMail/AccountWizard/CalDAV/CalDAVSheetController.swift` (new),
  `MalachiCore/Controllers/CalendarPreferencesController.swift`,
  `CalendarAccountController.swift` (new, with tests).

**M10 · Integrations** (group K)
- Files: `MalachiMail/Board/BoardTodayViewController.swift`,
  `Board/BoardTodayCalendarStrip.swift` (new),
  `MalachiCore/Assistant/*` files that hold the panel actions and allowed
  tools (port of U2), `MalachiMail/Assistant/*` files of the chips and the
  answer cards, `MalachiMail/Notifications/NotificationService.swift`,
  `App/Integration+Calendar.swift` (new), `App/Integration+Notifications.swift`,
  `MalachiCore/Controllers/CalendarAlertsController.swift` (new, tests).

**M11 · Development aids and docs** (group L)
- Files: `MalachiMail/MainWindow/MainWindowController+DevStart.swift`,
  `macos/README.md`, `docs/macos-port.md`, this document's status line.
- Checks: every `MALACHI_START` step of §5.10 with the samples; at most
  three screenshot rounds per item (auto-memory rule), then hand over.

### 6.3 Gates and what the owner checks

- **Gate 1 (daemon)**: the owner exercises the daemon over a copy of his
  store with its own socket, through the developer tool
  `backend/cmd/malachi-rpc` (a generic JSON-RPC client with the handshake;
  not packaged) and, optionally, the MCP tools from Claude Code; Windows
  CI (`windows.yml`) after the first commit of the branch. The procedure
  (macOS; migration 0020 and the board's rules "8" run on the copy only):

  1. Quit the daily Malachi Mail (its daemon holds the store lock and the
     WAL must be quiescent), then copy the data directory:
     `cp -R ~/Library/Application\ Support/Malachi\ Mail ~/malachi-cal-test/data`.
  2. Build the branch's binaries somewhere outside `build/`:
     `cd backend && for b in malachid malachi-rpc malachi-mcp; do go build -o ~/malachi-cal-test/bin/$b ./cmd/$b; done`.
  3. Start the daemon over the copy with the daily app's keychain helper
     (so the Microsoft refresh token can be read; the helper ships in the
     bundle) and its own socket:
     `MALACHI_KEYRING=helper MALACHI_KEYRING_HELPER="$PWD/build/Malachi Mail.app/Contents/MacOS/malachi-keychain" MALACHI_TIME_ZONE=Europe/Prague ~/malachi-cal-test/bin/malachid -config ~/malachi-cal-test/data/config.toml -store ~/malachi-cal-test/data/store.db -socket ~/malachi-cal-test/rpc.sock`
     (first start: migration 0020, the board recompute, the invitation
     backfill in `Maintain`). In a second terminal `export MALACHI_SOCKET=~/malachi-cal-test/rpc.sock`
     so that `malachi-rpc` and `malachi-mcp` find it.
  4. `malachi-rpc account.list` (note the Graph account's id),
     `malachi-rpc calendar.list` (the Graph account listed with `sync:false`
     and no calendars; `unanswered` counts the invitations found by the
     backfill), `malachi-rpc invitation.list`, and `message.get` of an
     invitation message (the card: `invitation.state`, `conflicts`, `day`,
     `targets`).
  5. Microsoft 365: `malachi-rpc account.oauthStart '{"accountId":"acc_…","calendar":true}'`
     → open `authUrl` in the browser (the consent screen now lists the
     calendar permissions) → `malachi-rpc account.oauthWait '{"sessionId":"s_…"}'`
     until `complete` → `malachi-rpc account.update` with the returned
     `config` plus `"calendar":{"sync":true}` and `"credentials":{"oauthSession":"s_…"}`
     (params through stdin) → `calendar.list` shows the calendars and the
     state, `malachi-rpc --listen 30s calendar.sync '{"accountId":"acc_…"}'`
     shows the pass and `notify.calendarChanged`, `event.list` of this week
     returns occurrences, `event.get`, `calendar.freeBusy` for a colleague,
     `event.create` in a test calendar (with and without an attendee you
     choose: Exchange sends the invitation), `event.update`/`event.delete`
     with the `etag`, `event.respond` on an invitation Exchange put into the
     calendar.
  6. CalDAV: `malachi-rpc account.discoverCalDAV '{"url":"cloud.example.org"}'`
     → `account.listCalDAVCalendars` with `config` (kind `caldav`,
     `serverUrl`, `username`) and `credentials.password` through stdin →
     `account.add` with the chosen `calendar.disabledCalendars`/
     `defaultCalendar` and `caldav.mailAccountId` (the mail account that
     sends invitations when the server does not schedule) → the same
     checks as for Graph; on a server without `calendar-auto-schedule`
     `event.create` with an attendee queues the invitation in that mail
     account's outbox (`message.list` of its Outbox folder shows it).
  7. Invitations: have an invitation sent to the IMAP or Graph mailbox
     from another calendar → after the sync `message.list` shows
     `invitation` on the row, `message.get` the card, `invitation.respond`
     answers (IMAP: the REPLY appears in the Outbox and goes to the
     organiser only; Graph: through the event) and `invitation.list` no
     longer lists it.
  8. Report back: what worked, every error object (`malachi-rpc` prints
     them to stderr), the daemon's log lines around a failure, and which
     steps were skipped. Secrets (passwords, tokens, the `.key` file) never
     go into chat.

- **Gate 2 (macOS)**: the eight artboards in the app built from the branch
  over a copy of the owner's data; invitations and replies are real mail,
  sent only to addresses the owner chooses. Procedure (after gate 1's copy
  exists; the app launches its own daemon from the bundle, so the daemon of
  gate 1 must be stopped first):

  1. Build the bundle from the branch the usual way (`make macos SIGN=…`;
     the agent never does this). The bundle carries the branch's `malachid`
     and `malachi-mcp`.
  2. Run it isolated over the copy with its own socket, exactly as for the
     Jira/board tests: `MALACHI_DATA_DIR=~/malachi-cal-test/data
     MALACHI_SOCKET=~/malachi-cal-test/rpc.sock open -n -W "build/Malachi Mail.app"`
     — or with `MALACHI_CALENDAR_SAMPLES=1 MALACHI_DAEMON=none` and an
     empty data dir for a first look at the layout with invented data.
     `MALACHI_TIME_ZONE` is set by the app from the system zone.
  3. Walk the artboards: ⌘3 (mode switch, View ▸ Calendar), Week/Day/
     Month/Agenda, Today, ‹ ›, the mini month, Invitations ▸ Unanswered,
     calendar checkboxes, Add Calendar…; click an event (popover:
     respond, Edit, Remove from Calendar, ✦); ⌥⌘N / + / double-click
     (editor: attendees, availability on the Microsoft 365 account,
     Add Video Call, Repeat, Alert, Save and Send Invitations to an
     address you own); Mail: an invitation's pill and card (Accept /
     Tentative / Decline, Add to:, the state strip, Show in Calendar);
     Preferences → Accounts (Calendar and Invitations groups, Sync This
     Account's Calendars with the consent sign-in), General → Calendar;
     Add CalDAV Calendar… (sheet, Discover, Trust Certificate… if the
     server's certificate is not public); Board → Today strip; the
     assistant chips What's on Today / Find a Free Slot; a notification
     for an event alert (set an alert a few minutes ahead) and for an
     incoming invitation.
  4. Report what differs from the wireframes and every error text; the
     `MALACHI_START` steps of §5.10 print the frames if a layout question
     needs numbers.


---

### 6.4 The Windows and GTK ports (after the owner's approval of macOS)

What a port takes, in the order the macOS client took it (rule 6 of
CLAUDE.md; the Go reference and the `.blp` are the pattern, the macOS
client the source of the port):

1. **The API mirror.** `windows/src/Malachi.Core/Api/Calendar.cs` (task
   T1w) mirrors `pkg/api/calendar.go` blind, with the round-trip samples in
   `windows/tests/Malachi.Core.Tests/Api/CalendarRoundTripTests.cs`; it has
   never been compiled — `build.ps1 app`/`test` first, then the
   `GoContractTests` limits. GTK uses `pkg/api` directly.
2. **The pure logic** of `ui/internal/calendar` (§5.1; every file lists its
   functions, every text its msgid) and `ui/internal/assistant/calendar_actions.go`,
   as `Malachi.Core` ports with the Go tests ported 1:1 (the Swift ports in
   `MalachiCore/Calendar/*` show the shape); the mode is
   `board.Mode` `calendar` (`ModeCalendar`, raw value 2).
3. **The data source and controllers** (§5.2: `CalendarDataSource`,
   `DaemonCalendarSource` with its cache and notification rules,
   `CalendarController`, `EventDetailController`, `EventEditorController`,
   `InvitationController`, `CalDAVWizardController`,
   `CalendarPreferencesController`, `CalendarAccountController`,
   `CalendarAlertsController`), then the views of §5.3–§5.9 with their "As
   built" notes (what the wireframe did not say and the owner decided: the
   month bars, opaque blocks, the Delete button, the all-day alert choices,
   the own-invitation card, Preferences → Calendar as its own tab).
4. **Parity.** Every calendar msgid is in `windows/parity-exclusions.txt`
   with the reason "calendar: Windows port pending"; the port removes each
   as it uses it. Strftime msgids keep `// xgettext:no-c-format` in the Go
   source. The daemon's zone comes from `MALACHI_TIME_ZONE`, which the
   Windows client sets like the macOS supervisor does.
5. **Verification.** The DevStart steps of the macOS client (§5.10) are the
   measured reference; the Windows client checks the same layouts with its
   UI Automation smoke tests over `MALACHI_CALENDAR_SAMPLES=1`.

### 6.5 The GTK port: tasks G1–G20 (plan 2026-10-17)

The GTK client ports the macOS client (rule 6: the Go reference and the
`.blp` are the pattern, the macOS client the source). Everything builds
and tests in the Fedora 42 container (`scripts/gtk-container/gtk.sh make
build`, `gtk.sh test-ui`; `make po` through `malachi-po:f42`;
`container system stop` after each job); the orchestrator looks at the
app under Broadway over `MALACHI_BOARD_SAMPLES=1 MALACHI_CALENDAR_SAMPLES=1`
(the calendar's sample switch is read once at start, like the board's in
`window/board.go`). Implementers never launch the app themselves.

**Ground rules.** No `backend/` changes (the daemon's calendar API is
complete). `SetUseMarkup(false)` on every label with server text (titles,
places, notes, names); bold and strike-through through `pango.AttrList`,
never markup; no HTML anywhere; links only through `w.openLink(parent,
uri, nil)` ("Open this link?"). RPC callbacks reach the main loop only
through `glib.IdleAdd` (the existing `glibLoop`). Every text comes from
`ui/internal/calendar` through `i18n.Tr`; dates through
`calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.Local}`.
New msgids only where nothing in `calendar`, `board` or the pot fits;
implementers do not edit `po/*`, `po/POTFILES` or
anything under `windows/` — they list new msgids and new Go files with
literal `i18n.T` calls in their handoff, G20 applies them (Czech in
`po/cs.po`, `// xgettext:no-c-format` on strftime msgids). **Nothing under
`windows/` is touched by the GTK port** (owner, 2026-10-17: the Windows
port is in progress on another machine, unpushed); a new msgid the GTK
client needs is listed in the handoff for the Windows side to use or
exclude after its push. No `MALACHI_TIME_ZONE`
(the daemon reads TZ / `/etc/localtime` on Linux), no build tags,
Blueprint only, SPDX headers. Tasks that write into one Go package in
parallel (G5–G8 in `calendarctl`) write each file complete and compiling
before running tests and never touch a sibling's file; a package that
fails to compile because of a sibling's unfinished file is retried, not
"fixed". `ui/internal/window/conversation_controller_test.go` is green
again since 2026-10-17 (`WithDrafts: true` expected).

**Architecture.**

- *Data seam* `ui/internal/calendardata`: `DataSource` interface
  (`Snapshot()`, `Observe(func(Change))`, `Refresh`, `Handle(method, raw)`,
  `Occurrences(range, done)`, `CachedOccurrences`, `Event(id, rec, reload,
  done)`, every write with `done(result, *api.Error)` on the main loop),
  `Snapshot` (with `ActiveCalendarAccounts`, `VisibleCalendars`,
  `WritableCalendars`, `AccountName`, `CalendarName`, `MyAddress`), `Phase`,
  `Change`, `ErrPreview` (text `calendar.PreviewOnly`); `DaemonSource`
  over `board.Caller` and `board.Loop` calling the raw `api.Method*`
  names through `client.Call`; `InMemorySource` over `calendar.SampleData`.
- *Controllers* `ui/internal/calendarctl`, one file per controller, plain
  structs with callbacks (`OnChange`, `OnError`, `OnOpenDetail`,
  `OnNewEvent`, `OnShowInvitation`, `OnShowInCalendar`, `OnOpenLink`,
  `OnSignIn`); Swift's async questions become continuations
  (`OnQuestion(q, answer func(Answer))`, `OnScope(q, func(*api.EditScope))`,
  `OnConfirmTrust(func(bool))`); debounces through `Loop.After`, tests
  drive a fake loop as `board/controller_test.go` does. Swift-first pure
  helpers (`MiniMonth`, `InvitationReading`, `ParseCalendarResult`) go
  into the reference packages (G1), so Windows gets a Go reference too.
- *Week/Day drawing*: a `Gtk.Overlay` inside a vertical `ScrolledWindow`,
  24 × 60 px tall; the main child a `Gtk.DrawingArea` (hour and half-hour
  lines, day separators, weekend shading, today's tint); every block an
  overlay child — a flat `Gtk.Button` with labels — positioned in
  `ConnectGetChildPosition` from `calendar.LayoutDay` (exact on resize,
  focusable, accessible label = `BlockLabel`); the now line the last
  child with `can-target=false` and a one-minute timer. A custom
  `GtkLayoutManager` is out (the pinned gotk4 has no constructor and the
  repository never subclasses GObject types). First scroll
  `ScrollMinute` px − 10, re-applied while the viewport height changes
  until the user scrolls, reset on a day change. All-day row and month
  bars: a `Gtk.Grid` with 7 homogeneous columns, bars attached at
  `(StartCol, lane+1, span, 1)`; classes `cal-continues-before/after`
  square the ends. Month rows: a DrawingArea base in an Overlay as the
  resize probe feeding `CellCapacity`, pills rebuilt in an `IdleAdd` only
  when the capacity changes. Keyboard: `calendar.KeyFor`,
  `SelectionOrder`, `MoveSelection`; the view grabs focus on the key's
  widget.
- *CSS*: `ui/internal/style/calendar.go` installs a second `CSSProvider`
  and a pure `CalendarCSS(colors []string) string`; calendars map to an
  index, classes `.cal-c-N` (never the id in CSS); a colour is accepted
  only when it matches `^#[0-9a-fA-F]{6}$`, else a fixed palette by index
  (tests with hostile values); opaque fills `mix(@window_bg_color,
  #rrggbb, 0.18)` (dark mode follows), the 3 px bar in the colour,
  unanswered `border: 1px dashed` + "?" badge, tentative stripes. Each
  view task owns its own `style/calendar_<view>.go` constants that
  `CalendarCSS` concatenates.
- *Invitation card*: `window/invitation_card.go` + `invitation_card.blp`
  per instance through `data.Builder` (like `html_card`), over
  `calendarctl.InvitationController` and the window's shared
  `w.calendarSource()`; `message_view.go` binds an `invitation_card_slot`
  above the headers exactly like `issue_card_slot` (`window.blp` and
  `message_window.blp`; none in `embedded_window.blp`); `renderHeaders`
  passes `Message.Invitation` and `calendar.CardModeOf(card,
  ShowsInvitationCard(account))`; conversation cards put the card under
  the card's header row, inside what folds, as macOS; `widget.MessageRow`
  gains the invitation pill and a Pango strike-through on cancelled
  subjects; a conversation row uses its newest member's pill
  (`calendar.InvitationPillOf`).
- *Preferences → Calendar*: `preferences.blp` gains
  `Adw.PreferencesPage calendar_page` after `general_page` with a static
  Display group (row IDs; titles from `calendar.TabTitle`,
  `DisplayGroup`); per-account `Adw.PreferencesGroup`s added/removed from
  Go following `calendar.PreferencesLayout`; `accounts_page.go` drops
  accounts where `ShowsInAccountsTab` is false; `NewPreferences` takes
  `cal func() calendardata.DataSource`.

**Tasks.**

Wave A (parallel): **G1** Swift-first pure catch-up
(`ui/internal/calendar/minimonth.go`, `reading.go` + tests;
`ui/internal/assistant/calendar_result.go` + test; ports `MiniMonth.swift`,
`Model/InvitationReading.swift` — `InvitationPillOf`, `ShowsInvitationCard`,
`CardClockLine`, `DayStripOf` —, `Assistant/AssistantCalendar.swift` —
`CalendarRef`, `ParseCalendarResult`; tests from `InvitationReadingTests`,
`AssistantEventsTests`, `CalendarHelpersTests`). **G2** data seam +
in-memory source (`ui/internal/calendardata/{doc,source,snapshot,memory}.go`
+ tests; ports `CalendarSource.swift`, `InMemoryCalendarSource.swift`,
`CalendarSamples.swift`; writes answer `ErrPreview` except visibility).
**G4** settings and mode plumbing (`settings/store.go`: `KeyCalendarView`,
`CalendarView()/SetCalendarView`, `BoardStartCalendar`; `window/board.go`:
`Mode.Nick`/`ParseMode`, three modes, `w.enterCalendar/leaveCalendar`
stubs in `window/calendar_mode.go`; `window/mode_keys.go`: Ctrl+3 and
Ctrl+Shift+1–4 by physical key; `window/window.go` `win.show-calendar`;
`window/preferences_board.go` `StartModesWithCalendar`; `window.blp`
third `Adw.Toggle` "calendar" (`x-office-calendar-symbolic`) and
`StackPage "calendar"` with `calendar_page_bin`; `board_page.blp` mirror
toggle; `widget/format.go` exported `Strftime`; with Show the Board off
the switch stays with its Board toggle insensitive; tests: nick
round-trip, start decision, `Strftime` with `%-d` and `%OB`, the board
smoke through all three modes).

Wave B (after G2, parallel): **G3** `calendardata/daemon.go` + test
(ports `DaemonCalendarSource.swift` + tests: `calendar.list`,
`account.list`, `invitation.list`, `calendar.preferences`; `event.list`
cached per range, `event.get` by etag; `calendarChanged` drops
overlapping ranges; `calendarState`, `invitationsChanged`,
`AccountsChanged`, `ConnectionChanged`; `board.RetryDelay`;
`methodNotFound` → `PhaseUnsupported`; conflict reloads; consentRequired).
**G5** `calendarctl/{options,calendar}.go` + test (ports
`CalendarController.swift` + tests: view, anchor with `SwitchAnchor`,
`Step/Today/GoTo`, selection, search (two characters, 300 ms), Unanswered
filter, agenda paging in 30 days, the `Sidebar` model with
`calendar.MiniMonth`, status and unanswered lines, `EmptyKind`, `KeyFor`,
`EscapeFor`, `calendar-view` through an injected getter/setter). **G6**
`calendarctl/{detail,invitation}.go` + tests (ports
`EventDetailController.swift`, `InvitationController.swift` + tests:
respond, scope question, Decline Without Reply, remove/delete questions
with `confirm-delete` passed in, conflict reload; Add-to target, propose
new time answering Tentative, apply cancel, own-event state). **G7**
`calendarctl/editor.go` + test (ports `EventEditorController.swift` +
tests: defaults, validation, all-day alert remap (D31), availability
debounced 500 ms with `NextFreeSlot`, video-call modes, attachments
through `attachment.import`, save paths and scope, `EditorDelete`, close
question, `findFreeSlot`, conflict → reload). **G8**
`calendarctl/{caldav,preferences,account,alerts}.go` + tests (ports
`CalDAVWizardController`, `CalendarPreferencesController`,
`CalendarAccountController` — checkboxes and default by `remoteId`,
`account.listCalDAVCalendars`, sync switch → consent →
`account.oauthStart{calendar:true}` —, `CalendarAlertsController` —
notification models, snooze 5 min, dismiss/withdraw).

Wave C1 (after G3, G4, G5): **G9** page shell and glue
(`window/calendar.go`: `calendarPage`, `ensureCalendar`, the shared
`w.calendarSource()` (samples or daemon), `showInCalendar(eventID, rec,
day)`, `w.calendarEnv()`, replacing `calendar_mode.go`;
`calendar_page.blp`: `Adw.BreakpointBin` → `Adw.OverlaySplitView` with a
240 px sidebar, header bar with the mode-switch mirror, sidebar toggle,
Today, ‹ ›, `Adw.ToggleGroup` Day/Week/Month/Agenda, `Adw.WindowTitle`,
search toggle with `Gtk.SearchBar`, "+", ✦; content `Gtk.Stack`, banner,
status-strip slot; `calendar_keys.go`, `calendar_notify.go` + a case group
in `notify.go` routing the four notifications; `status.go`
`moveStatusStripTo(mode)` with `calendar.StatusLine` and
`UnansweredCount`; `style/calendar.go` + test; `main.go` and
`preferences.go` signatures; `data/data.go`; stubs with frozen
signatures for `calendar_sidebar.go`, `calendar_agenda.go`,
`calendar_week.go`, `calendar_month.go`, `calendar_detail.go`
(`w.showEventDetail(occ, anchor gtk.Widgetter)`), `event_editor.go`
(`w.openEventEditor(defaults|edit)`), `calendar_caldav.go`
(`w.showAddCalDAV(parent, onDone)`), `calendar_assistant.go`
(`w.askAboutEvent`, `w.revealCalendarAssistant`) and stub blps
`event_detail`, `event_editor`, `invitation_card`, `caldav_sheet`; test
`calendar_gtk_smoke_test.go` building the page over the in-memory source,
stepping through the views, logging `LAYOUT:` allocations).

Wave C2 (after G9 and the named controllers; parallel, disjoint files):
**G10** sidebar, agenda, search (`calendar_sidebar.go`,
`calendar_agenda.go`, `style/calendar_sidebar.go`; ports
`CalendarSidebarViewController`, `MiniMonthView`,
`CalendarAgendaViewController`, `CalendarSearchController`: mini month as
a `Gtk.Grid` of buttons with accessible day labels, Invitations ▸
Unanswered with a count badge, accounts with the kind capsule and
calendar `CheckButton`s with a colour square, consent row with Sign In…,
Add Calendar… menu; agenda/results/unanswered as a `ListBox` with header
rows and paging). **G11** week/day (`calendar_week.go`,
`calendar_allday.go`, `style/calendar_week.go`; ports
`CalendarTimeGridView`, `CalendarDayHeaderView`, `CalendarAllDayView`,
`EventBlockView`: 60 px/hour, labels offset 10 px, all-day row up to five
lanes, double-click on empty time → `openEventEditor(NewEventDefaults)`,
click selects and `showEventDetail`; smoke: block rectangles of an
overlap cluster, now line above blocks, first scroll). **G12** month
(`calendar_month.go`, `style/calendar_month.go`; ports
`CalendarMonthView`, `MonthCellView`: 6 rows, week numbers, spanning
bars, pills, "+N more" popover from `CellItems`, today's ring,
double-click → 9:00; smoke: a bar across a week boundary, capacity on
resize). **G13** detail popover (`calendar_detail.go`, `event_detail.blp`,
`style/calendar_detail.go`; ports `EventDetailPopover`,
`EventDetailViewController`, `EventDetailViews`: one `Gtk.Popover` per
window, 380 px, parented to the page, `SetPointingTo` from the anchor's
`ComputeBounds`; two-line time row; attendee list collapsing beyond 6;
the "You:" response `MenuButton`; notes as a non-editable `TextView` with
link tags → `openLink`; Show Invitation in Mail through the board's
reveal; Edit and Remove questions as `Adw.AlertDialog`; ✦ menu with Ask
about This Event and Find Another Time; inline error). **G14** editor
window (`event_editor.go`, `event_editor_availability.go`,
`event_editor_popovers.go`, `event_editor.blp`,
`compose/attendee_field.go` — an exported wrapper over `recipientField`
and its suggestions, `recipient_field.go` unchanged —,
`style/calendar_editor.go`; ports `EventEditor/*`: an `Adw.Window` 760×800
with a registry raising an open edit; `Adw.EntryRow`/`ComboRow`/`SwitchRow`
form; date pickers as `Gtk.Calendar` in popovers plus time spin buttons;
time-zone search popover, Custom… repeat popover, Add Video Call
`MenuButton`; availability rows with a DrawingArea axis; footer Delete,
Cancel, primary; Ctrl+S / Ctrl+Return; close question Save / Discard /
Cancel). **G15** invitations in Mail (`invitation_card.go`,
`invitation_card.blp`, `message_view.go`, `window.blp` and
`message_window.blp` slot only, `conversation_card.go`,
`widget/message_row.go`, `widget/pill.go`, `folders.go` — the Mail
sidebar leaves out calendar-only accounts —, `style/calendar_card.go`;
ports `InvitationCardView`, `InvitationDayStripView`,
`MessageViewController+Invitation`, `InvitationPill`: date tile,
`PreviousLines`, conflict strip, day strip (`DayStripOf`), "Add to:",
Accept/Tentative/Decline with Decline Without Reply, reply note, Propose
New Time…, state strip with Change Response and Show in Calendar, the
reply one-liner, cancellation with Remove from Calendar; smoke: a card for
every `SampleData` card state). **G16** Preferences → Calendar
(`preferences_calendar.go`, `preferences.blp`, `preferences.go` fields +
one bind line, `accounts_page.go` CalDAV filter; ports
`CalendarPaneViewController`, `CalendarAccountGroups`: Working Hours as
half-hour combos plus seven day toggles with the last day kept on; CalDAV
section with Server and Username, Replace Password… as an
`Adw.AlertDialog` with `Adw.PasswordEntryRow`, Mail Account, Certificate
with Forget, Remove Account; empty state; Add CalDAV Calendar…; a model
test over the in-memory source). **G17** Add CalDAV sheet
(`calendar_caldav.go`, `caldav_sheet.blp`; ports `CalDAVSheetController`,
`CalDAVBannerView`: an `Adw.Dialog` 560×560 with fields, Discover,
calendar list, found line, certificate row with the certtrust Trust
Certificate… flow, mail-account popup, footer, Cancel/Add; smoke over a
scripted controller). **G18** notifications (`calendar_alerts.go`,
`main.go` app actions `app.calendar-open-event`, `-join`, `-snooze`,
`-accept-invitation`, `-remove-cancelled` with variant targets; ports the
calendar part of `NotificationService.swift` and `Integration+Calendar.swift`:
`gio.Notification` with `AddButtonWithTarget`, Join only with a video
call, Snooze, Accept and Show, Remove from Calendar; default action opens
the event (Calendar mode + popover) or the message; withdraw on
dismissed; respects `desktop-notifications` and the sound setting; test:
action targets round-trip). **G19** board Today strip + assistant
(`board_today_calendar.go`, `board_today.go`, `assistant_panel.go`
`quickActions` from `PanelActionsWith(true)`, `assistantpanel/controller.go`
+ test (`RunCalendar`, a calendar card item from `ParseCalendarResult`),
`calendar_assistant.go` replacing the stub; ports
`BoardTodayCalendarStrip.swift`, the calendar parts of
`AssistantPanelController.swift` and D28: the panel's widget moves into
the calendar page's split and back on leaving; Today strip's Respond
selects the message in Mail as macOS).

Wave D: **G20** translations, docs, verification (`po/POTFILES`,
`po/malachi.pot`, `po/cs.po` — never `windows/`,
`docs/calendar.md` §5 "As built (G1–G19)" and §6.4 status, CLAUDE.md,
`macos/README.md` GTK column, root README; done when `make lint` is
green, `test-ui` green, all smoke tests green and the Broadway screenshots
match artboards 1–8).

**Deviations from macOS** (for "As built (G…)" and the READMEs): header
bars per page instead of a unified toolbar; search a toggled
`Gtk.SearchBar`; view shortcuts Ctrl+Shift+1–4 and New Event
Ctrl+Shift+N (as the Go reference maps them); the detail a `Gtk.Popover`;
the editor an `Adw.Window`; the CalDAV sheet an `Adw.Dialog`; questions
`Adw.AlertDialog`s; notifications with per-notification buttons, not
categories; Preferences → Calendar an `Adw.PreferencesPage`; calendar
colours through CSS `mix()`.


## 7. Decisions on the open questions (owner, 2026-10-16)

Asked through the orchestrating session and answered the same day; each
is now a decision of §1 and binds the tasks of §6.

1. **Q1 · Board and calendar mail: out of the board.** A message with a
   `cal_invitations` row (an invitation, an update, a cancellation, a
   reply, and the user's own REPLY/REQUEST/CANCEL copies in Sent) is not a
   counted member of a board case. `board.RulesVersion` becomes "8" so that
   every case is re-evaluated once (task T12). The calendar and the Today
   strip handle invitations.
2. **Q2 · Existing Microsoft 365 accounts: calendar sync off** until the
   user switches it on (Preferences → Accounts, or the Calendar's empty
   state); no consent screen appears by itself. As §4.3 already says
   (`CalendarConfig.sync` default false).
3. **Q3 · Drag to move and resize: not in this iteration.** Click → detail
   → Edit only; the wireframes show no dragging.
4. **Q4 · CalDAV scheduling: the server, when it advertises
   `calendar-auto-schedule`** (D8 as written); the daemon sends iMIP only
   where the server does not schedule; the per-account override stays.
5. **Q5 · Decline Without Reply: kept** (`sendResponse: false`) on the
   card's and the popover's Decline.
6. **Entra registration:** the owner adds `Calendars.ReadWrite` and
   `Calendars.Read.Shared` to the built-in client now; T0 runs on his
   account before gate 1.
