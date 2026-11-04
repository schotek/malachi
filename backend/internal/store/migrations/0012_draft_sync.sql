-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0012_draft_sync: drafts get a copy in the account's Drafts folder.
--
-- The syncer uploads a draft once it has rested (synced_version <
-- version), every upload under a fresh Message-ID, and the copy it
-- replaces is deleted through an ordinary delete operation. server_*
-- locate the current copy: the folder, and UID under the folder's
-- UIDVALIDITY (IMAP) or the item's immutable id (Graph); rfc_message_id
-- is its Message-ID. A copy's local row is found through either, so a
-- message of the Drafts folder leads back to its draft.
--
-- version never moves for synchronisation: clients (the MCP bridge above
-- all) hold on to it for message.send. sync_* is the upload's retry
-- state, reset by every save. reply_rfc_id and references_json keep the
-- threading headers of a reply whose parent may leave the local store
-- (retention window) or never was in it (a draft taken over from another
-- client).
--
-- Drafts saved before this migration count as uploaded and stay local:
-- many are autosaves of windows the user discarded. Never edit this file
-- after commit.

ALTER TABLE drafts ADD COLUMN rfc_message_id TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN server_folder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN server_uidvalidity INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drafts ADD COLUMN server_uid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drafts ADD COLUMN server_remote_id TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN synced_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drafts ADD COLUMN synced_at TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN sync_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drafts ADD COLUMN sync_next_at TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN sync_error TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN reply_rfc_id TEXT NOT NULL DEFAULT '';
ALTER TABLE drafts ADD COLUMN references_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(references_json));

UPDATE drafts SET synced_version = version;

CREATE UNIQUE INDEX drafts_by_rfc_id ON drafts(account_id, rfc_message_id) WHERE rfc_message_id != '';
CREATE INDEX drafts_unsynced ON drafts(account_id) WHERE synced_version < version;
