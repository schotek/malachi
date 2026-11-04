-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0019_edited_drafts: what becomes of a suggested reply that loses its case,
-- and the Drafts folder copies no draft holds any more (docs/api.md §4.5,
-- §4.13, docs/architecture.md §7, store/draft_sync.go).
--
-- drafts.edited = 1 says the draft may hold text the user wrote: a
-- draft.save stored it while a case linked it (the board's inline editor;
-- the bridge saves before the link exists), or it was an ordinary draft
-- when a case linked it (the daemon cannot tell who wrote an ordinary
-- draft). Never cleared. When a linked draft loses its case without Send
-- or Discard (a thread merge, the prune of an orphaned case, the end of a
-- done case's retention, the removal of its account with its local data
-- kept) an edited draft becomes an ordinary one (local = 0: it uploads to
-- the Drafts folder and the user sees it in Mail) and an untouched one is
-- deleted, in the same transaction (store.releaseLinkedDraftTx). Local
-- drafts no case links and never edited (a triage leftover, a failed
-- Suggest Reply) go after 6 hours without a save (store.UnlinkedLocalDrafts).
--
-- A local draft (0018) no longer records a copy in the Drafts folder (the
-- server_* columns of 0012 stay empty). draft_stray_copies holds copies
-- that this store wants gone but cannot delete yet, none of them tied to a
-- draft: a copy known only by its Message-ID, or under a UIDVALIDITY the
-- folder has not reported (until a pass of the folder brings its row), and
-- a Graph copy (remote_id) until its account's syncer has asked whether
-- someone changed it on the server after synced_at — one changed there
-- (Outlook edits drafts in place) is the user's own draft and stays, one
-- not changed is deleted. store.DropStrayDraftCopies deletes them through
-- the operation log; it gives up on an entry, with a log line, a week
-- after recorded_at. No foreign key: DeleteAccount deletes an account's
-- rows.
--
-- On a store that ran 0018 every local draft, linked or not, counts as
-- edited (whether the user typed in it cannot be known now), and the copy
-- a local draft still records moves to draft_stray_copies with its copy
-- columns cleared, for the next pass of the account's syncer or the
-- board's upkeep. Case versions and board_dirty are not touched.
--
-- Never edit this file after commit.

ALTER TABLE drafts ADD COLUMN edited INTEGER NOT NULL DEFAULT 0 CHECK (edited IN (0, 1));

CREATE TABLE draft_stray_copies (
    id             INTEGER PRIMARY KEY,
    account_id     TEXT    NOT NULL,
    folder_id      TEXT    NOT NULL DEFAULT '',
    uidvalidity    INTEGER NOT NULL DEFAULT 0,
    uid            INTEGER NOT NULL DEFAULT 0,
    remote_id      TEXT    NOT NULL DEFAULT '',
    rfc_message_id TEXT    NOT NULL DEFAULT '',
    synced_at      TEXT    NOT NULL DEFAULT '',
    recorded_at    TEXT    NOT NULL
);
CREATE INDEX draft_stray_copies_by_account ON draft_stray_copies (account_id);

UPDATE drafts SET edited = 1 WHERE local = 1;

INSERT INTO draft_stray_copies (account_id, folder_id, uidvalidity, uid, remote_id, rfc_message_id, synced_at, recorded_at)
SELECT account_id, server_folder_id, server_uidvalidity, server_uid, server_remote_id, rfc_message_id, synced_at,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
  FROM drafts WHERE local = 1 AND (rfc_message_id != '' OR server_folder_id != '');

UPDATE drafts SET rfc_message_id = '', server_folder_id = '', server_uidvalidity = 0, server_uid = 0,
       server_remote_id = '', synced_version = 0, sync_attempts = 0, sync_next_at = '', sync_error = ''
 WHERE local = 1 AND (rfc_message_id != '' OR server_folder_id != '');
