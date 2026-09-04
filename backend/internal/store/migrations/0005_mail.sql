-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0005_mail: the offline mail cache — folders, message headers/bodies and
-- the log of local changes waiting to be pushed to the IMAP server.
--
-- folders mirrors the server's mailbox list per account (no FK to accounts,
-- like drafts). mailbox is the raw IMAP name, path the "/"-separated display
-- path, parent_id the local id of the parent (empty for roots). The sync
-- columns (uidvalidity … last_sync_at) are owned by the sync engine and
-- survive a folder-list refresh; unread/total are local counts.
--
-- messages holds one row per message per folder. uid is the IMAP UID inside
-- folder_id, or 0 while a local move waits to be pushed (the row then has no
-- server identity yet). unread is derived: 1 when the "seen" flag is absent,
-- kept as a column so the unread-only listing can use an index. Address
-- lists, flags, references, attachments and curated headers are JSON.
-- text_body is plain text only — never HTML. Raw RFC 822 bytes live in
-- <dir of store.db>/messages/<account_id>/<message_id> (0600 in 0700).
--
-- message_ops is the append-only local-change log. folder_id/uid are the
-- snapshot taken before the change so the sync engine can address the
-- message on the server; uid 0 blocks the op until an earlier move is
-- reconciled (AssignUID). payload is JSON: {"set","clear"} for flag,
-- {"targetFolderId"} for move, {} for delete. Never edit this file after
-- commit.

CREATE TABLE folders (
    id              TEXT PRIMARY KEY,
    account_id      TEXT    NOT NULL,
    mailbox         TEXT    NOT NULL,
    delimiter       TEXT    NOT NULL DEFAULT '',
    parent_id       TEXT    NOT NULL DEFAULT '',
    name            TEXT    NOT NULL,
    path            TEXT    NOT NULL,
    role            TEXT    NOT NULL DEFAULT 'none'
                    CHECK (role IN ('none', 'inbox', 'sent', 'drafts', 'trash', 'junk', 'archive', 'all', 'outbox')),
    subscribed      INTEGER NOT NULL DEFAULT 1 CHECK (subscribed IN (0, 1)),
    selectable      INTEGER NOT NULL DEFAULT 1 CHECK (selectable IN (0, 1)),
    uidvalidity     INTEGER NOT NULL DEFAULT 0,
    uidnext         INTEGER NOT NULL DEFAULT 0,
    highestmodseq   INTEGER NOT NULL DEFAULT 0,
    server_messages INTEGER NOT NULL DEFAULT 0,
    server_unseen   INTEGER NOT NULL DEFAULT 0,
    unread          INTEGER NOT NULL DEFAULT 0,
    total           INTEGER NOT NULL DEFAULT 0,
    last_sync_at    TEXT    NOT NULL DEFAULT '',
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (account_id, mailbox)
);
CREATE INDEX folders_order ON folders (account_id, position, path);

CREATE TABLE messages (
    id               TEXT PRIMARY KEY,
    account_id       TEXT    NOT NULL,
    folder_id        TEXT    NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    uid              INTEGER NOT NULL DEFAULT 0,
    modseq           INTEGER NOT NULL DEFAULT 0,
    flags            TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(flags)),
    unread           INTEGER NOT NULL DEFAULT 1 CHECK (unread IN (0, 1)),
    from_json        TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(from_json)),
    to_json          TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(to_json)),
    cc_json          TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(cc_json)),
    bcc_json         TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(bcc_json)),
    reply_to_json    TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(reply_to_json)),
    subject          TEXT    NOT NULL DEFAULT '',
    date             TEXT    NOT NULL DEFAULT '',
    internal_date    TEXT    NOT NULL DEFAULT '',
    rfc_message_id   TEXT    NOT NULL DEFAULT '',
    in_reply_to      TEXT    NOT NULL DEFAULT '',
    references_json  TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(references_json)),
    size             INTEGER NOT NULL DEFAULT 0,
    snippet          TEXT    NOT NULL DEFAULT '',
    has_attachments  INTEGER NOT NULL DEFAULT 0 CHECK (has_attachments IN (0, 1)),
    attachments_json TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(attachments_json)),
    headers_json     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(headers_json)),
    has_html         INTEGER NOT NULL DEFAULT 0 CHECK (has_html IN (0, 1)),
    text_body        TEXT    NOT NULL DEFAULT '',
    body_state       TEXT    NOT NULL DEFAULT 'none'
                     CHECK (body_state IN ('none', 'fetched', 'tooBig', 'failed')),
    thread_id        TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX messages_by_uid   ON messages (folder_id, uid) WHERE uid > 0;
CREATE INDEX messages_by_date         ON messages (folder_id, date, id);
CREATE INDEX messages_unread          ON messages (folder_id, date, id) WHERE unread = 1;
CREATE INDEX messages_pending         ON messages (folder_id, rfc_message_id) WHERE uid = 0;
CREATE INDEX messages_unfetched       ON messages (folder_id, date, id) WHERE body_state = 'none';
CREATE INDEX messages_by_account      ON messages (account_id);

CREATE TABLE message_ops (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('flag', 'move', 'delete')),
    message_id      TEXT    NOT NULL,
    folder_id       TEXT    NOT NULL,
    uid             INTEGER NOT NULL DEFAULT 0,
    payload         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(payload)),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT    NOT NULL DEFAULT '',
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX message_ops_by_account ON message_ops (account_id, id);
CREATE INDEX message_ops_by_message ON message_ops (message_id, id);
CREATE INDEX message_ops_by_folder  ON message_ops (folder_id);
