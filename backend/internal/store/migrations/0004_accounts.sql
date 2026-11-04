-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0004_accounts: the account registry. The store is authoritative;
-- [[accounts]] entries in config.toml are imported once at daemon start when
-- no account with the same e-mail exists (internal/core). config holds the
-- JSON form of api.AccountConfig: non-secret settings only, secrets live in
-- the keyring (docs/security.md §6). email is the normalised copy of
-- config.email kept for uniqueness. Never edit this file after commit.

CREATE TABLE accounts (
    id         TEXT PRIMARY KEY,
    email      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    name       TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    config     TEXT    NOT NULL CHECK (json_valid(config)),
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX accounts_order ON accounts (position, created_at, id);
