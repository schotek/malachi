-- 0003_drafts: local compose drafts and the compose-side attachment store.
--
-- html_body holds ONLY sanitiser output (internal/sanitize, compose mode);
-- raw editor HTML is never persisted. Recipient lists are JSON arrays of
-- {name, address}: no query needs them relationally.
--
-- attachments.draft_id is NULL between attachment.import and the first
-- draft.save that lists the id; such rows (and their files under
-- <dir of store.db>/attachments/<id>) are removed by the orphan sweep after
-- 24 h. Never edit this file after commit.

CREATE TABLE drafts (
    id          TEXT PRIMARY KEY,
    account_id  TEXT    NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    subject     TEXT    NOT NULL DEFAULT '',
    to_json     TEXT    NOT NULL DEFAULT '[]',
    cc_json     TEXT    NOT NULL DEFAULT '[]',
    bcc_json    TEXT    NOT NULL DEFAULT '[]',
    text_body   TEXT    NOT NULL DEFAULT '',
    html_body   TEXT    NOT NULL DEFAULT '',
    in_reply_to TEXT    NOT NULL DEFAULT '',
    forwarding  TEXT    NOT NULL DEFAULT '',
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX drafts_by_account ON drafts (account_id, updated_at DESC, id DESC);

CREATE TABLE attachments (
    id           TEXT PRIMARY KEY,
    account_id   TEXT    NOT NULL,
    draft_id     TEXT    REFERENCES drafts(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL DEFAULT 0,
    filename     TEXT    NOT NULL,
    content_type TEXT    NOT NULL,
    size         INTEGER NOT NULL CHECK (size > 0),
    sha256       TEXT    NOT NULL,
    inline       INTEGER NOT NULL DEFAULT 0 CHECK (inline IN (0, 1)),
    content_id   TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX attachments_by_draft ON attachments (draft_id, position);
CREATE INDEX attachments_orphans  ON attachments (created_at) WHERE draft_id IS NULL;
