-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0008_flagged: a derived column for the flagged-only listing.
--
-- messages.flagged mirrors the "flagged" entry of the flags JSON, exactly
-- as unread mirrors the absence of "seen" (0005_mail). The flags array
-- stays the source of truth; the column exists so message.list can filter
-- through a partial index instead of scanning the folder with json_each.
-- The backfill derives it for rows written before this migration. Never
-- edit this file after commit.

ALTER TABLE messages ADD COLUMN flagged INTEGER NOT NULL DEFAULT 0
    CHECK (flagged IN (0, 1));

UPDATE messages SET flagged = 1
 WHERE EXISTS (SELECT 1 FROM json_each(messages.flags) WHERE value = 'flagged');

CREATE INDEX messages_flagged ON messages (folder_id, date, id) WHERE flagged = 1;
