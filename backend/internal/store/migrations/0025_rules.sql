-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0025_rules: the mail rules (docs/api.md §4.21, docs/plans/rules.md R10;
-- store/rules.go).
--
-- rules is the user's rules in their order; the conditions and actions are
-- JSON of the API's RuleCondition and RuleAction lists, checked by
-- internal/rules before they are written. account_id NULL is a rule for
-- every mail account. enabled_at is when the rule was last enabled (unix
-- seconds; 0 = never): the automatic run leaves alone mail older than the
-- day before it. problem is why the daemon disabled the rule ('' = none).
--
-- rule_seen is the once-per-Message-ID record of the automatic run (R3):
-- each Message-ID of an account goes through it at most once, so a
-- message moved back into the inbox, also by another client (a deletion
-- and a new row), is left alone. msg_key is messages.rfc_message_id, else
-- 'id:' || messages.id.
--
-- rule_replies is when an autoReply rule last answered a sender (RFC 3834:
-- at most once in api.RuleReplyInterval); rule_outgoing is every message
-- an outgoing action queued, for the per-hour limit. Both are pruned by
-- the daemon's maintenance.
--
-- No foreign key to accounts (there are none in the store): the rows of an
-- account go in its removal path, those of a rule with the rule. Tables and
-- indexes only: no column of an existing table and no trigger.
--
-- Never edit this file after commit.

CREATE TABLE rules (
    id              TEXT    PRIMARY KEY,           -- 'r_' + 32 hex
    position        INTEGER NOT NULL,
    name            TEXT    NOT NULL,
    enabled         INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    account_id      TEXT,                          -- NULL = every mail account
    match           TEXT    NOT NULL CHECK (match IN ('all', 'any')),
    conditions      TEXT    NOT NULL DEFAULT '[]', -- JSON [RuleCondition]
    actions         TEXT    NOT NULL DEFAULT '[]', -- JSON [RuleAction]
    stop            INTEGER NOT NULL DEFAULT 0 CHECK (stop IN (0, 1)),
    problem         TEXT    NOT NULL DEFAULT '',   -- '', 'folderGone', 'sendFailed'
    created_at      INTEGER NOT NULL,              -- unix seconds
    updated_at      INTEGER NOT NULL,
    enabled_at      INTEGER NOT NULL DEFAULT 0,    -- unix seconds, 0 = never
    applied         INTEGER NOT NULL DEFAULT 0,
    last_applied_at INTEGER,                       -- unix seconds, NULL = never
    skipped         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX rules_by_position ON rules (position);
CREATE INDEX rules_by_account ON rules (account_id);

CREATE TABLE rule_seen (
    account_id      TEXT    NOT NULL,
    rfc_message_id  TEXT    NOT NULL,              -- the message's key (see above)
    seen_at         INTEGER NOT NULL,              -- unix seconds
    PRIMARY KEY (account_id, rfc_message_id)
) WITHOUT ROWID;

CREATE INDEX rule_seen_by_time ON rule_seen (seen_at);

CREATE TABLE rule_replies (
    rule_id         TEXT    NOT NULL,
    sender          TEXT    NOT NULL,              -- normalised address
    sent_at         INTEGER NOT NULL,              -- unix seconds
    PRIMARY KEY (rule_id, sender)
) WITHOUT ROWID;

CREATE TABLE rule_outgoing (
    rule_id         TEXT    NOT NULL,
    sent_at         INTEGER NOT NULL               -- unix seconds
);

CREATE INDEX rule_outgoing_by_rule ON rule_outgoing (rule_id, sent_at);
