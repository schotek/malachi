-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0028_kept_messages: messages older than the offlineDays window that a
-- search on the server found (docs/api.md §4.6 search.server,
-- docs/plans/server-search.md; store/kept.go).
--
-- The sync engines drop every row that left the window. A row named here
-- is spared until kept_until: it was stored because the user searched for
-- it, and stays readable for a while. After kept_until the row is one
-- outside the window like any other and the next pass removes it. A row
-- goes with its message (ON DELETE CASCADE).
--
-- No trigger on messages or on any other table. Times are RFC 3339 UTC.
--
-- Never edit this file after commit.

CREATE TABLE kept_messages (
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    kept_until TEXT NOT NULL
);

CREATE INDEX kept_messages_until ON kept_messages (kept_until);
