-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0016_bulk: bulk mail classification and remembered unsubscriptions
-- (internal/bulk, core/unsubscribe.go).
--
-- messages.bulk is what internal/bulk made of the message's List-Id,
-- List-Post, List-Unsubscribe, Precedence and Auto-Submitted headers:
-- 'newsletter', 'list' or 'automated', 'none' for personal mail (and for
-- every message of an issue-tracker account), and '' for a row that has not
-- been classified yet. A row is classified when its envelope arrives
-- (IMAP, from the header fields it fetches) and again when its body is
-- ingested; the daemon's upgrade pass (meta 'bulk.classified', rule
-- version in the value) classifies the rows that were stored before this
-- migration from the raw file's header block, else from headers_json, and
-- starts over when the rule version changes. messages.list_id is the
-- cleaned, lower-case List-Id identifier ('' = none). messages_bulk_todo
-- lists the rows the pass still has to visit, so that neither the pass nor
-- its "finished" test scans the table.
--
-- unsubscriptions remembers, per account, which list or sender the user
-- already unsubscribed from through message.unsubscribe (oneClick or
-- mailto; never a page opened in the browser). key is 'list:<List-Id>' or
-- 'from:<lower-case From address>'. It has no foreign key (no table
-- references accounts); DeleteAccount removes the rows. Never edit this
-- file after commit.

ALTER TABLE messages ADD COLUMN bulk TEXT NOT NULL DEFAULT ''
    CHECK (bulk IN ('', 'none', 'newsletter', 'list', 'automated'));
ALTER TABLE messages ADD COLUMN list_id TEXT NOT NULL DEFAULT '';

CREATE INDEX messages_bulk_todo ON messages (id) WHERE bulk = '';

CREATE TABLE unsubscriptions (
    account_id      TEXT NOT NULL,
    key             TEXT NOT NULL,
    method          TEXT NOT NULL CHECK (method IN ('oneClick', 'mailto')),
    unsubscribed_at TEXT NOT NULL,
    PRIMARY KEY (account_id, key)
) WITHOUT ROWID;
