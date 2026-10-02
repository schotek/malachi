-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0018_local_drafts: drafts that stay on this device (docs/api.md §4.5,
-- §4.13, store/draft_sync.go).
--
-- drafts.local = 1 keeps a draft in this store: no syncer uploads it to the
-- account's Drafts folder (store.DueDraftUploads and NextDraftUpload skip
-- it), and it leaves the device only when it is sent. It is a board case's
-- suggested reply: draft.save sets it on the first save (the MCP bridge's
-- -reply-only and -triage-run), and linking a draft to a case
-- (board.setDraft, board.annotate draftId) sets it and deletes a copy the
-- draft already has in the Drafts folder. Only a thread merge that leaves
-- a draft without its case clears it again (the draft becomes an ordinary
-- one and uploads). drafts_local serves the sweep of local drafts that no
-- case links (store.UnlinkedLocalDrafts).
--
-- Drafts linked to a case before this migration are made local here. A
-- server copy one of them already has cannot be deleted by SQL (the delete
-- goes through the operation log to the server): the copy columns stay,
-- and the daemon's hourly upkeep deletes the copy of every local draft
-- that still has one (store.DropLocalDraftCopies), then clears them.
--
-- drafts_board_ad (0017) is replaced: deleting a linked draft also marks
-- its case's thread dirty, since a suggested reply that still exists keeps
-- its case (store.DrainBoard) and one that is gone no longer does.
--
-- Never edit this file after commit.

ALTER TABLE drafts ADD COLUMN local INTEGER NOT NULL DEFAULT 0 CHECK (local IN (0, 1));
CREATE INDEX drafts_local ON drafts (updated_at) WHERE local = 1;

UPDATE drafts SET local = 1
 WHERE EXISTS (SELECT 1 FROM board_cases c WHERE c.draft_id = drafts.id AND c.account_id = drafts.account_id);

DROP TRIGGER drafts_board_ad;
CREATE TRIGGER drafts_board_ad AFTER DELETE ON drafts BEGIN
    UPDATE board_cases SET version = version + 1
     WHERE draft_id = old.id;
    INSERT INTO board_dirty (account_id, thread_id) SELECT c.account_id, c.thread_id FROM board_cases c
        WHERE c.draft_id = old.id AND c.account_id = old.account_id
          AND NOT EXISTS (SELECT 1 FROM board_dirty d WHERE d.account_id = c.account_id AND d.thread_id = c.thread_id);
END;
