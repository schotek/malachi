-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0002_preferences: daemon-owned user preferences and the remote-content
-- allow-list.
--
-- preferences holds values set through config.set (sync interval, remote
-- content policy). Keys are dotted strings owned by internal/core; a missing
-- key means "use config.toml or the built-in default".
--
-- known_senders is the allow-list behind the "knownSenders" remote-content
-- policy. Rows come from addresses the user sent mail to ("sent") or from
-- explicit user decisions ("user"); never from incoming From headers.
-- Addresses are compared case-insensitively. Never edit this file after
-- commit.

CREATE TABLE preferences (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE known_senders (
    address  TEXT PRIMARY KEY COLLATE NOCASE,
    source   TEXT NOT NULL CHECK (source IN ('sent', 'user')),
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
