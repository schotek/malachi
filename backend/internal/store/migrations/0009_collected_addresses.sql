-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0009_collected_addresses: recipients of mail the user sent, for recipient
-- completion in the compose window.
--
-- Rows come only from outgoing mail (the outbox worker after a delivery,
-- and a one-off backfill from the Sent folders already in the store); never
-- from incoming From headers, which are attacker-controlled — the same rule
-- as known_senders (0002). Addresses are compared case-insensitively.
-- name_lc is the name lower-cased in Go, so the search is case-insensitive
-- beyond ASCII, which SQLite's LIKE is not. The table is global, not per
-- account: these are people the user chose to write to. Never edit this
-- file after commit.

CREATE TABLE collected_addresses (
    address    TEXT    PRIMARY KEY COLLATE NOCASE,
    name       TEXT    NOT NULL DEFAULT '',
    name_lc    TEXT    NOT NULL DEFAULT '',
    first_used TEXT    NOT NULL,
    last_used  TEXT    NOT NULL,
    uses       INTEGER NOT NULL DEFAULT 1 CHECK (uses > 0)
);
CREATE INDEX collected_addresses_recent ON collected_addresses (last_used DESC);
CREATE INDEX collected_addresses_name   ON collected_addresses (name_lc);
