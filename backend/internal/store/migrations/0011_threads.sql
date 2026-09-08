-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0011_threads: conversation threading over the messages table.
--
-- messages.thread_id is never empty from now on: a locally threaded row
-- (IMAP, Gmail, the outbox) carries "t_" + 32 hex, a Graph row the
-- server's conversation id (0005, 0007). Rows written before this
-- migration get a singleton id here; the daemon links them into
-- conversations on its next start (core.Maintain), in the background.
-- There is no threads table: a listing groups the messages of a folder by
-- thread_id at query time, which the folder/thread index serves.
--
-- message_refs is the derived index of the identifiers a message points at
-- (In-Reply-To and References), so a parent that arrives after its replies
-- finds them; it is rewritten whenever those columns change and goes with
-- its message. Never edit this file after commit.

UPDATE messages SET thread_id = 't_' || lower(hex(randomblob(16))) WHERE thread_id = '';

CREATE INDEX messages_by_thread     ON messages (account_id, thread_id, date, id);
CREATE INDEX messages_folder_thread ON messages (folder_id, thread_id, date, id, unread, flagged, has_attachments);
CREATE INDEX messages_by_rfc_id     ON messages (account_id, rfc_message_id) WHERE rfc_message_id != '';

CREATE TABLE message_refs (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL,
    ref        TEXT NOT NULL,
    PRIMARY KEY (message_id, ref)
) WITHOUT ROWID;
CREATE INDEX message_refs_by_ref ON message_refs (account_id, ref);

INSERT OR IGNORE INTO message_refs (message_id, account_id, ref)
    SELECT id, account_id, in_reply_to FROM messages WHERE in_reply_to != '';
INSERT OR IGNORE INTO message_refs (message_id, account_id, ref)
    SELECT m.id, m.account_id, j.value
    FROM messages m, json_each(m.references_json) j
    WHERE typeof(j.value) = 'text' AND j.value != '';
