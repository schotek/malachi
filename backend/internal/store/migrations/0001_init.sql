-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0001_init: bootstrap schema.
--
-- The schema_migrations table itself is created by the migration runner.
-- This migration only establishes a key/value table for store-level metadata
-- (store UUID, sanitiser ruleset version, …). Real tables (accounts, folders,
-- messages, message_parts, threads, drafts, outbox, fts) arrive with the
-- IMAP phase in later numbered migrations. Never edit this file after commit.

CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO meta (key, value) VALUES ('created_at', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
