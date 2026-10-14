-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0017_board: the board (docs/api.md §4.13, store/board.go).
--
-- board_cases holds one row per thread of an account that is a case. Its
-- id ('c_' + 32 hex) is the API's case id: a thread merge moves the row to
-- the surviving thread (store/threads.go), so the id outlives thread ids.
-- The derived columns (rule_state … issue_status_category) are a cache the
-- daemon rebuilds from messages and issues at any time; the user's columns
-- (user_state … remind_at) are authoritative and never recomputed. A row
-- exists only while the thread is a case: rule_state is always one of the
-- four states (for a case the rules dropped but something keeps, the last
-- one, with rule_reason 'kept'), and a while after its thread lost every
-- visible member (orphaned_at). input_key names the members that count
-- and their body state (an annotation of another key is stale);
-- members_key also covers what board.get shows of them. version changes on
-- every change of the row, of its annotation and of its linked draft (the
-- drafts triggers below), so a client can cache board.get by (id,
-- version). Times are the store's '%Y-%m-%dT%H:%M:%fZ' stamps, '' = none.
--
-- draft_id is the draft linked to the case as its suggested reply ('' =
-- none): by the user (board.setDraft) or by an annotation (board.annotate
-- draftId). It is the case's, not the annotation's: a later annotation
-- without a draft keeps it, and it outlives the draft (the drafts join
-- shows none once the draft is gone).
--
-- board_annotations is an assistant's annotation of a case (board.annotate
-- replaces it whole); board_commitments what the
-- user promised in their own messages (board.commit), closed by the user
-- (done) or by the daemon (closed: replied, done). Both go with their case.
--
-- board_runs records triage runs: manual and auto ones a client starts
-- and ends, and one implicit external run per source and day (day = the
-- caller's local day, 'YYYY-MM-DD') for calls that name no open run.
-- error is a class, never text. The four *_tokens columns are the token
-- usage the client reported with board.runEnd, all NULL = unknown (runs
-- of other clients, external runs, runs the daemon closed itself).
--
-- board_dirty is the set of threads to evaluate again. The triggers below
-- fill it on every write a case depends on: a message stored, deleted
-- (also through the cascade of a folder or account deletion), moved,
-- merged into another thread, flagged or read, hidden, classified as bulk
-- mail, its body arriving or its envelope changing; an issue stored,
-- deleted, or its status, assignee, reporter, watching, key or summary
-- changing; an issue item stored or deleted (its kind and author decide
-- Jira cases); and the record of the Jira user ('issues.me.' meta keys,
-- which decides whose items are the user's). The UPDATE triggers fire only
-- when a watched column really changed. The triggers insert only what is
-- not there yet rather than INSERT OR IGNORE: a conflict clause of the
-- statement that fires a trigger overrides the trigger's own (an upsert of
-- messages would turn the IGNORE into a failure). A write that replaces a
-- row (INSERT OR REPLACE) would bypass the DELETE triggers: never write one
-- on these tables. The daemon drains the set in the background
-- (store.DrainBoard).
--
-- This migration creates objects only: nothing is scanned or backfilled.
-- The daemon fills the board for stored mail in the background. No table
-- has a foreign key to accounts; DeleteAccount removes the account's rows.
-- Never edit this file after commit.

CREATE TABLE board_cases (
    id                    TEXT PRIMARY KEY,
    account_id            TEXT    NOT NULL,
    thread_id             TEXT    NOT NULL,
    -- derived
    rule_state            TEXT    NOT NULL CHECK (rule_state IN ('hot', 'you', 'them', 'info')),
    rule_reason           TEXT    NOT NULL DEFAULT '',
    rules_version         TEXT    NOT NULL DEFAULT '',
    input_key             TEXT    NOT NULL DEFAULT '',
    members_key           TEXT    NOT NULL DEFAULT '',
    subject               TEXT    NOT NULL DEFAULT '',
    snippet               TEXT    NOT NULL DEFAULT '',
    person_json           TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(person_json)),
    date                  TEXT    NOT NULL DEFAULT '',
    unread                INTEGER NOT NULL DEFAULT 0 CHECK (unread IN (0, 1)),
    has_attachments       INTEGER NOT NULL DEFAULT 0 CHECK (has_attachments IN (0, 1)),
    can_archive           INTEGER NOT NULL DEFAULT 0 CHECK (can_archive IN (0, 1)),
    message_count         INTEGER NOT NULL DEFAULT 0,
    reply_message_id      TEXT    NOT NULL DEFAULT '',
    reply_folder_id       TEXT    NOT NULL DEFAULT '',
    latest_message_id     TEXT    NOT NULL DEFAULT '',
    issue_key             TEXT    NOT NULL DEFAULT '',
    issue_status          TEXT    NOT NULL DEFAULT '',
    issue_status_category TEXT    NOT NULL DEFAULT '',
    computed_at           TEXT    NOT NULL DEFAULT '',
    -- the user's
    user_state            TEXT    NOT NULL DEFAULT '' CHECK (user_state IN ('', 'hot', 'you', 'them', 'info')),
    user_state_at         TEXT    NOT NULL DEFAULT '',
    done_at               TEXT    NOT NULL DEFAULT '',
    remind_at             TEXT    NOT NULL DEFAULT '',
    -- 1 once remind_at came due and the clients were told (the case is live
    -- again; a past remind_at keeps it until done or a new remind)
    reminded              INTEGER NOT NULL DEFAULT 0 CHECK (reminded IN (0, 1)),
    -- the Message-IDs (bare, one per line, '\n' around each) of the inbound
    -- members that counted when the case was marked done: a later copy of
    -- one of them (a move by another client) does not reopen the case
    done_seen             TEXT    NOT NULL DEFAULT '',
    -- since when the thread has had no visible member ('' = it has some):
    -- the case is kept, off the board, and pruned after a grace period
    -- unless members come back (a move between folders)
    orphaned_at           TEXT    NOT NULL DEFAULT '',
    -- the Message-IDs of the thread's newest visible members (as done_seen):
    -- a new thread holding one of them adopts the orphaned case (a message
    -- another client moved is stored anew, in a thread of its own)
    member_ids            TEXT    NOT NULL DEFAULT '',
    draft_id              TEXT    NOT NULL DEFAULT '',
    version               INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (account_id, thread_id)
);
CREATE INDEX board_cases_by_date ON board_cases (account_id, date, id);
CREATE INDEX board_cases_reminded ON board_cases (remind_at) WHERE reminded = 0 AND remind_at != '';
CREATE INDEX board_cases_orphaned ON board_cases (orphaned_at) WHERE orphaned_at != '';
CREATE INDEX board_cases_by_draft ON board_cases (draft_id) WHERE draft_id != '';

CREATE TABLE board_annotations (
    case_id        TEXT PRIMARY KEY REFERENCES board_cases(id) ON DELETE CASCADE,
    input_key      TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT '' CHECK (state IN ('', 'hot', 'you', 'them', 'info')),
    title          TEXT NOT NULL DEFAULT '',
    summary        TEXT NOT NULL DEFAULT '',
    why            TEXT NOT NULL DEFAULT '',
    tasks_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tasks_json)),
    due_at         TEXT NOT NULL DEFAULT '',
    due_quote      TEXT NOT NULL DEFAULT '',
    due_message_id TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT '',
    run_id         TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE board_commitments (
    id            TEXT PRIMARY KEY,
    case_id       TEXT NOT NULL REFERENCES board_cases(id) ON DELETE CASCADE,
    account_id    TEXT NOT NULL,
    message_id    TEXT NOT NULL,
    message_date  TEXT NOT NULL DEFAULT '',  -- the message's date when recorded
    -- the date of the user's newest member that counted when it was recorded
    -- (at least message_date): a message of the user's dated later closes it
    replied_after TEXT NOT NULL DEFAULT '',
    text          TEXT NOT NULL,
    quote         TEXT NOT NULL,
    due_at        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'done', 'closed')),
    closed_reason TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL DEFAULT '',
    run_id        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    closed_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX board_commitments_by_case ON board_commitments (case_id, created_at);
CREATE INDEX board_commitments_open ON board_commitments (case_id) WHERE state = 'open';

CREATE TABLE board_runs (
    id          TEXT PRIMARY KEY,
    trigger     TEXT    NOT NULL CHECK (trigger IN ('manual', 'auto', 'external')),
    source      TEXT    NOT NULL DEFAULT '',
    day         TEXT    NOT NULL DEFAULT '',  -- external runs: the local day they stand for
    started_at  TEXT    NOT NULL,
    ended_at    TEXT    NOT NULL DEFAULT '',
    annotated   INTEGER NOT NULL DEFAULT 0,
    commitments INTEGER NOT NULL DEFAULT 0,
    rejected    INTEGER NOT NULL DEFAULT 0,
    error       TEXT    NOT NULL DEFAULT '' CHECK (error IN ('', 'cancelled', 'timeout', 'signedOut', 'failed')),
    input_tokens                INTEGER CHECK (input_tokens >= 0),
    output_tokens               INTEGER CHECK (output_tokens >= 0),
    cache_creation_input_tokens INTEGER CHECK (cache_creation_input_tokens >= 0),
    cache_read_input_tokens     INTEGER CHECK (cache_read_input_tokens >= 0)
);
CREATE INDEX board_runs_by_start ON board_runs (started_at, id);
CREATE UNIQUE INDEX board_runs_external ON board_runs (source, day) WHERE trigger = 'external';
CREATE INDEX board_runs_open ON board_runs (started_at) WHERE ended_at = '' AND trigger != 'external';
CREATE INDEX board_runs_usage ON board_runs (ended_at) WHERE input_tokens IS NOT NULL;

CREATE TABLE board_dirty (
    account_id TEXT NOT NULL,
    thread_id  TEXT NOT NULL,
    PRIMARY KEY (account_id, thread_id)
) WITHOUT ROWID;

CREATE TRIGGER messages_board_ai AFTER INSERT ON messages BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, new.thread_id
        WHERE new.thread_id != '' AND NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = new.thread_id);
END;

CREATE TRIGGER messages_board_ad AFTER DELETE ON messages BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT old.account_id, old.thread_id
        WHERE old.thread_id != '' AND NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = old.account_id AND d.thread_id = old.thread_id);
END;

CREATE TRIGGER messages_board_au
AFTER UPDATE OF thread_id, folder_id, flags, hidden, bulk, body_state, date, internal_date,
    from_json, to_json, cc_json, bcc_json, subject, snippet, rfc_message_id, in_reply_to,
    headers_json, has_attachments, attachments_json, text_body, remote_id ON messages
WHEN old.thread_id IS NOT new.thread_id OR old.folder_id IS NOT new.folder_id
  OR old.flags IS NOT new.flags OR old.hidden IS NOT new.hidden OR old.bulk IS NOT new.bulk
  OR old.body_state IS NOT new.body_state OR old.date IS NOT new.date
  OR old.internal_date IS NOT new.internal_date OR old.from_json IS NOT new.from_json
  OR old.to_json IS NOT new.to_json OR old.cc_json IS NOT new.cc_json OR old.bcc_json IS NOT new.bcc_json
  OR old.subject IS NOT new.subject OR old.snippet IS NOT new.snippet
  OR old.rfc_message_id IS NOT new.rfc_message_id OR old.in_reply_to IS NOT new.in_reply_to
  OR old.headers_json IS NOT new.headers_json OR old.has_attachments IS NOT new.has_attachments
  OR old.attachments_json IS NOT new.attachments_json OR old.text_body IS NOT new.text_body
  OR old.remote_id IS NOT new.remote_id
BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, new.thread_id
        WHERE new.thread_id != '' AND NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = new.thread_id);
    INSERT INTO board_dirty (account_id, thread_id) SELECT old.account_id, old.thread_id
        WHERE old.thread_id != '' AND NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = old.account_id AND d.thread_id = old.thread_id);
END;

CREATE TRIGGER issues_board_ai AFTER INSERT ON issues BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, 'jira:' || new.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = 'jira:' || new.issue_id);
END;

CREATE TRIGGER issues_board_ad AFTER DELETE ON issues BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT old.account_id, 'jira:' || old.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = old.account_id AND d.thread_id = 'jira:' || old.issue_id);
END;

CREATE TRIGGER issues_board_au
AFTER UPDATE OF key, summary, status_id, status, status_category, assignee_id, reporter_id, watching ON issues
WHEN old.key IS NOT new.key OR old.summary IS NOT new.summary OR old.status_id IS NOT new.status_id
  OR old.status IS NOT new.status OR old.status_category IS NOT new.status_category
  OR old.assignee_id IS NOT new.assignee_id OR old.reporter_id IS NOT new.reporter_id
  OR old.watching IS NOT new.watching
BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, 'jira:' || new.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = 'jira:' || new.issue_id);
END;

CREATE TRIGGER issue_items_board_ai AFTER INSERT ON issue_items BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, 'jira:' || new.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = 'jira:' || new.issue_id);
END;

CREATE TRIGGER issue_items_board_ad AFTER DELETE ON issue_items BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT old.account_id, 'jira:' || old.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = old.account_id AND d.thread_id = 'jira:' || old.issue_id);
END;

CREATE TRIGGER issue_items_board_au AFTER UPDATE OF issue_id, kind, author_id ON issue_items
WHEN old.issue_id IS NOT new.issue_id OR old.kind IS NOT new.kind OR old.author_id IS NOT new.author_id
BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT new.account_id, 'jira:' || new.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = new.account_id AND d.thread_id = 'jira:' || new.issue_id);
    INSERT INTO board_dirty (account_id, thread_id) SELECT old.account_id, 'jira:' || old.issue_id
        WHERE NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = old.account_id AND d.thread_id = 'jira:' || old.issue_id);
END;

-- The Jira user's record (store.MetaIssueMePrefix + account id): every
-- issue of the account is evaluated again when it arrives or changes.
CREATE TRIGGER meta_board_me_ai AFTER INSERT ON meta
WHEN substr(new.key, 1, 10) = 'issues.me.'
BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT i.account_id, 'jira:' || i.issue_id FROM issues i
        WHERE i.account_id = substr(new.key, 11) AND NOT EXISTS (SELECT 1 FROM board_dirty d
            WHERE d.account_id = i.account_id AND d.thread_id = 'jira:' || i.issue_id);
END;

CREATE TRIGGER meta_board_me_au AFTER UPDATE OF value ON meta
WHEN substr(new.key, 1, 10) = 'issues.me.' AND old.value IS NOT new.value
BEGIN
    INSERT INTO board_dirty (account_id, thread_id) SELECT i.account_id, 'jira:' || i.issue_id FROM issues i
        WHERE i.account_id = substr(new.key, 11) AND NOT EXISTS (SELECT 1 FROM board_dirty d
            WHERE d.account_id = i.account_id AND d.thread_id = 'jira:' || i.issue_id);
END;

-- A linked draft is part of its case (BoardCase.draft): its edits and its
-- deletion change the case's version.
CREATE TRIGGER drafts_board_au AFTER UPDATE OF version, text_body ON drafts
WHEN old.version IS NOT new.version OR old.text_body IS NOT new.text_body
BEGIN
    UPDATE board_cases SET version = version + 1
     WHERE draft_id = new.id;
END;

CREATE TRIGGER drafts_board_ad AFTER DELETE ON drafts BEGIN
    UPDATE board_cases SET version = version + 1
     WHERE draft_id = old.id;
END;
