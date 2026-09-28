-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0014_raw_storage: compressed raw messages and attachments kept on the
-- server (store/raw.go, store/offline.go).
--
-- A raw message is <account>/<id> (the bytes as received) or
-- <account>/<id>.zst (one zstd frame that records its content size); the
-- file name, never the content, says which. message_files is the
-- accounting of those files: the codec, the logical length (bytes, the
-- message itself) and the length on disk. It feeds system.storage and
-- selects what the background conversion still has to convert, so neither
-- has to stat every file, and it lives in a table of its own so that
-- changing it never rewrites a messages row, whose text_body can be large.
-- Every write, conversion and removal of a file updates it; a row goes
-- with its message (ON DELETE CASCADE). Files stored before this migration
-- have no row until the daemon's sweep has accounted for them
-- (store.SweepMessageFiles, meta 'raw.accounted'); here the table starts
-- empty, because a migration cannot look at files.
--
-- The messages columns describe a stored message whose large attachments
-- stayed on the mail server. raw_state 'partial' means the file is a
-- skeleton of the message: the parts named in remote_parts (a JSON array
-- of part ids) have empty bodies, and remote_bytes is their decoded size.
-- raw_state = 'partial' holds exactly when remote_parts != '[]' and
-- remote_bytes > 0. strippable_bytes caches what the background pass
-- found it could leave on the server (-1 not evaluated yet, 0 nothing).
-- hydrated_at is when message.download last made the message whole, so
-- that the pass leaves it alone for a while. raw_state, remote_parts,
-- remote_bytes and hydrated_at are written only by CommitMessageRaw
-- (around the file's replacement, so that the row never claims a part is
-- stored when the file lacks it) and reset by MarkBodyState; none of
-- these columns bumps updated_at, which DeleteStalePending relies on. The
-- flag clients see, Attachment.remote, is derived from remote_parts when a
-- row is read and never stored in attachments_json.
--
-- messages_partial serves the counts of partial messages; the
-- messages_strippable partial index keeps the background pass's search
-- for candidates off the rows it can never change. Never edit this file
-- after commit.

CREATE TABLE message_files (
    message_id TEXT    PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    codec      TEXT    NOT NULL CHECK (codec IN ('plain', 'zstd')),
    bytes      INTEGER NOT NULL CHECK (bytes >= 0),      -- logical length
    disk_bytes INTEGER NOT NULL CHECK (disk_bytes >= 0)  -- file length
) WITHOUT ROWID;

ALTER TABLE messages ADD COLUMN raw_state TEXT NOT NULL DEFAULT 'full' CHECK (raw_state IN ('full', 'partial'));
ALTER TABLE messages ADD COLUMN hydrated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN remote_parts TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(remote_parts));
ALTER TABLE messages ADD COLUMN remote_bytes INTEGER NOT NULL DEFAULT 0;       -- decoded bytes left on the server
ALTER TABLE messages ADD COLUMN strippable_bytes INTEGER NOT NULL DEFAULT -1;  -- -1 not evaluated, 0 nothing, >0 bytes

CREATE INDEX messages_partial ON messages (account_id) WHERE raw_state = 'partial';
CREATE INDEX messages_strippable ON messages (internal_date)
    WHERE raw_state = 'full' AND body_state = 'fetched' AND strippable_bytes != 0;
