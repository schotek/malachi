-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0007_remote_ids: server identities for backends that are not IMAP.
--
-- messages.remote_id is the server's opaque message id (Microsoft Graph:
-- the immutable id), unique within a folder; IMAP rows keep '' and use
-- uid. message_ops.remote_id is the snapshot taken when the change was
-- queued, like folder_id/uid, so a delete can still address the server
-- copy after the local row is gone. folders.delta_link is the sync cursor
-- of a delta-query backend ('' = start from scratch). Never edit this file
-- after commit.

ALTER TABLE messages ADD COLUMN remote_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX messages_by_remote ON messages (folder_id, remote_id) WHERE remote_id != '';
CREATE INDEX messages_remote_by_account ON messages (account_id, remote_id) WHERE remote_id != '';

ALTER TABLE message_ops ADD COLUMN remote_id TEXT NOT NULL DEFAULT '';

ALTER TABLE folders ADD COLUMN delta_link TEXT NOT NULL DEFAULT '';
