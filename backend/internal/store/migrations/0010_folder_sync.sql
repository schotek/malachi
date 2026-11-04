-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0010_folder_sync: folders the daemon lists but never downloads.
--
-- folders.unsynced marks a folder that exists on the server and may be a
-- move target, but whose contents are not synchronised: Gmail's All Mail,
-- which holds every message the other folders already hold (moving a
-- message there is what Gmail calls archiving). The IMAP folder discovery
-- sets it from the \All attribute; the sync loop skips such folders and a
-- move into one drops the local row. Never edit this file after commit.

ALTER TABLE folders ADD COLUMN unsynced INTEGER NOT NULL DEFAULT 0 CHECK (unsynced IN (0, 1));
