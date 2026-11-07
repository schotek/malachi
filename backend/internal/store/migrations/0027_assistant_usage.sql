-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0027_assistant_usage: the token usage of the daemon's own assistant
-- over ai accounts (docs/api.md §4.18, docs/plans/ai-api.md D9;
-- store/assistant_usage.go).
--
-- One row per ai account, local day and model: the tokens the model API
-- reported, summed over the day's turns, and how many sessions and turns
-- contributed. The daemon adds to it after every turn; assistant.usage
-- reads it by account and day, summed over models; rows of days older
-- than 400 days are pruned. A row goes with its account (ON DELETE
-- CASCADE). No trigger on any other table.
--
-- Never edit this file after commit.

CREATE TABLE assistant_usage (
    account_id                  TEXT    NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    day                         TEXT    NOT NULL,                       -- local day, YYYY-MM-DD
    model                       TEXT    NOT NULL,
    input_tokens                INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens               INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_input_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_input_tokens >= 0),
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_creation_input_tokens >= 0),
    sessions                    INTEGER NOT NULL DEFAULT 0 CHECK (sessions >= 0),
    turns                       INTEGER NOT NULL DEFAULT 0 CHECK (turns >= 0),
    updated_at                  TEXT    NOT NULL,
    PRIMARY KEY (account_id, day, model)
);

CREATE INDEX assistant_usage_day ON assistant_usage (day);
