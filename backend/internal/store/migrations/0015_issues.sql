-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0015_issues: issue-tracker accounts (kind "jira", internal/jira).
-- accounts: an address is unique within its realm ('' for mail accounts,
-- the normalised site for kind jira), so a Jira account may share the
-- address of the user's mailbox. Rebuilt because the inline UNIQUE of 0004
-- cannot be dropped; no table has a foreign key to accounts and no view or
-- trigger names it.
--
-- An issue is a thread ('jira:' || issue id) whose items (description,
-- comments, events) are ordinary messages rows in the space's folder;
-- folders.virtual marks the fixed views (assignedToMe, watching, open),
-- whose rows are copies sharing remote_id and Message-ID with the space
-- folder's. issues, issue_items and issue_spaces hold what the rows are
-- built from; they have no foreign keys (one item is a row per folder).
-- messages.hidden is a display filter: a hidden row is left out of every
-- listing, count and search (a notification mail of the site hidden in a
-- mail account, linked to its issue by issue_mail_links). drafts and
-- outbox carry what a comment needs (the issue, its visibility). Never
-- edit this file after commit.

CREATE TABLE accounts_0015 (
    id         TEXT PRIMARY KEY,
    email      TEXT    NOT NULL COLLATE NOCASE,
    realm      TEXT    NOT NULL DEFAULT '' COLLATE NOCASE,
    name       TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    config     TEXT    NOT NULL CHECK (json_valid(config)),
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO accounts_0015 (id, email, realm, name, enabled, config, position, created_at, updated_at)
    SELECT id, email, '', name, enabled, config, position, created_at, updated_at FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_0015 RENAME TO accounts;
CREATE UNIQUE INDEX accounts_identity ON accounts (email, realm);
CREATE INDEX accounts_order ON accounts (position, created_at, id);

ALTER TABLE folders ADD COLUMN virtual TEXT NOT NULL DEFAULT '';

ALTER TABLE messages ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1));
CREATE INDEX messages_hidden ON messages (folder_id) WHERE hidden = 1;

ALTER TABLE drafts ADD COLUMN comment_visibility TEXT NOT NULL DEFAULT '';
ALTER TABLE outbox ADD COLUMN issue_id TEXT NOT NULL DEFAULT '';
ALTER TABLE outbox ADD COLUMN comment_visibility TEXT NOT NULL DEFAULT '';

CREATE TABLE issue_spaces (
    account_id   TEXT NOT NULL,
    space_id     TEXT NOT NULL,           -- folders.mailbox = 'space:' || space_id
    key          TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    service_desk INTEGER NOT NULL DEFAULT 0 CHECK (service_desk IN (0, 1)),
    PRIMARY KEY (account_id, space_id)
) WITHOUT ROWID;

CREATE TABLE issues (
    account_id      TEXT NOT NULL,
    issue_id        TEXT NOT NULL,        -- thread_id = 'jira:' || issue_id
    key             TEXT NOT NULL,
    space_id        TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    status_id       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    status_category TEXT NOT NULL DEFAULT '',
    type            TEXT NOT NULL DEFAULT '',
    priority        TEXT NOT NULL DEFAULT '',
    assignee_id     TEXT NOT NULL DEFAULT '',
    assignee_name   TEXT NOT NULL DEFAULT '',
    reporter_id     TEXT NOT NULL DEFAULT '',
    reporter_name   TEXT NOT NULL DEFAULT '',
    watching        INTEGER NOT NULL DEFAULT 0 CHECK (watching IN (0, 1)),
    service_desk    INTEGER NOT NULL DEFAULT 0 CHECK (service_desk IN (0, 1)),
    via_mail        INTEGER NOT NULL DEFAULT 0 CHECK (via_mail IN (0, 1)),
    created         TEXT NOT NULL DEFAULT '',
    updated         TEXT NOT NULL DEFAULT '',  -- the server's, as last seen
    synced_updated  TEXT NOT NULL DEFAULT '',  -- what the stored rows reflect; != updated → owes a refresh
    render_key      TEXT NOT NULL DEFAULT '',  -- hash of the rendering settings the rows were built with
    views           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(views)),
    PRIMARY KEY (account_id, issue_id)
) WITHOUT ROWID;
CREATE UNIQUE INDEX issues_by_key ON issues (account_id, key);
CREATE INDEX issues_by_updated ON issues (account_id, updated);

CREATE TABLE issue_items (
    account_id TEXT NOT NULL,
    remote_id  TEXT NOT NULL,             -- messages.remote_id of every copy: 'i:<issue>' | 'c:<comment>' | 'h:<history>'
    issue_id   TEXT NOT NULL,
    kind       TEXT NOT NULL,             -- description | comment | event
    visibility TEXT NOT NULL DEFAULT '',
    author_id  TEXT NOT NULL DEFAULT '',
    via        TEXT NOT NULL DEFAULT '',
    changes    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(changes)),
    edited     INTEGER NOT NULL DEFAULT 0 CHECK (edited IN (0, 1)),
    updated    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, remote_id)
) WITHOUT ROWID;
CREATE INDEX issue_items_by_issue ON issue_items (account_id, issue_id);

CREATE TABLE issue_mail_links (
    message_id       TEXT NOT NULL PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    issue_account_id TEXT NOT NULL,
    issue_key        TEXT NOT NULL,
    issue_id         TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) WITHOUT ROWID;
CREATE INDEX issue_mail_links_by_issue ON issue_mail_links (issue_account_id, issue_key);
