-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0013_search: the full-text index behind search.query (internal/search,
-- store/search.go).
--
-- messages_fts is a contentless FTS5 table: it holds the tokens only, never
-- a second copy of the text, and contentless_delete lets a row's entry be
-- removed by rowid. unicode61 with remove_diacritics 2 folds case and
-- diacritics, so "priloh" finds "přílohy"; the query side turns every word
-- into a prefix query, and search runs while the user types from the
-- second character on, so the index keeps 2- and 3-character prefixes: on
-- 50,000 messages a query like "pr" (in Czech mail nearly every message
-- has pro-, při-) took 295 ms without them and 10 ms with them, for about
-- 70 % more index. The columns are what the field prefixes restrict a
-- term to (subject:, from: = sender, to: = recipients, which are To, Cc
-- and Bcc) plus the attachment names and the plain-text body; a message's
-- folder and account are joined from messages at query time, so a move
-- needs no reindexing.
--
-- messages.id is a TEXT primary key, and the implicit rowid of such a
-- table may change on VACUUM, so the index is keyed by search_docs.docid,
-- a rowid of its own mapped to the message id.
--
-- message_search_text is the one definition of the indexed text, shared by
-- the triggers and the backfill. The triggers keep the index current on
-- every write: a new row, a change of an indexed column (the body arrives
-- after the envelope), and every deletion, including the ones a folder or
-- account deletion cascades into (foreign-key actions fire the child
-- table's triggers). An INSERT OR REPLACE on messages would bypass the
-- delete trigger: never write one. Rows stored before this migration are
-- indexed in the background by core.Maintain (store.IndexSearchBatch), not
-- here, so a large store does not hold up the upgrade.
--
-- Never edit this file after commit.

CREATE TABLE search_docs (
    docid      INTEGER PRIMARY KEY,
    message_id TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    subject, sender, recipients, attachments, body,
    content = '',
    contentless_delete = 1,
    prefix = '2 3',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE VIEW message_search_text AS
SELECT m.id AS message_id,
       m.subject AS subject,
       (SELECT group_concat(coalesce(json_extract(j.value, '$.name'), '') || ' ' ||
                            coalesce(json_extract(j.value, '$.address'), ''), ' ')
          FROM json_each(m.from_json) j
         WHERE j.type = 'object') AS sender,
       (SELECT group_concat(coalesce(json_extract(j.value, '$.name'), '') || ' ' ||
                            coalesce(json_extract(j.value, '$.address'), ''), ' ')
          FROM (SELECT value, type FROM json_each(m.to_json)
                UNION ALL SELECT value, type FROM json_each(m.cc_json)
                UNION ALL SELECT value, type FROM json_each(m.bcc_json)) j
         WHERE j.type = 'object') AS recipients,
       (SELECT group_concat(coalesce(json_extract(j.value, '$.filename'), ''), ' ')
          FROM json_each(m.attachments_json) j
         WHERE j.type = 'object') AS attachments,
       m.text_body AS body
  FROM messages m;

CREATE TRIGGER messages_search_ai AFTER INSERT ON messages BEGIN
    INSERT INTO search_docs (message_id) VALUES (new.id);
    INSERT INTO messages_fts (rowid, subject, sender, recipients, attachments, body)
        SELECT d.docid, t.subject, t.sender, t.recipients, t.attachments, t.body
          FROM message_search_text t, search_docs d
         WHERE t.message_id = new.id AND d.message_id = new.id;
END;

CREATE TRIGGER messages_search_au
AFTER UPDATE OF subject, from_json, to_json, cc_json, bcc_json, attachments_json, text_body ON messages
WHEN old.subject IS NOT new.subject OR old.from_json IS NOT new.from_json
  OR old.to_json IS NOT new.to_json OR old.cc_json IS NOT new.cc_json
  OR old.bcc_json IS NOT new.bcc_json OR old.attachments_json IS NOT new.attachments_json
  OR old.text_body IS NOT new.text_body
BEGIN
    DELETE FROM messages_fts WHERE rowid = (SELECT docid FROM search_docs WHERE message_id = new.id);
    -- A row stored before this migration and not yet backfilled gets its
    -- entry here.
    INSERT OR IGNORE INTO search_docs (message_id) VALUES (new.id);
    INSERT INTO messages_fts (rowid, subject, sender, recipients, attachments, body)
        SELECT d.docid, t.subject, t.sender, t.recipients, t.attachments, t.body
          FROM message_search_text t, search_docs d
         WHERE t.message_id = new.id AND d.message_id = new.id;
END;

CREATE TRIGGER messages_search_ad AFTER DELETE ON messages BEGIN
    DELETE FROM messages_fts WHERE rowid = (SELECT docid FROM search_docs WHERE message_id = old.id);
    DELETE FROM search_docs WHERE message_id = old.id;
END;
