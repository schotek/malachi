-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0006_outbox: the delivery queue of locally composed messages.
--
-- A queued message is an ordinary messages row in the account's outbox
-- pseudo-folder (folders.role = 'outbox', mailbox '' — a name the IMAP LIST
-- never yields — uid 0, flags ["seen"]) whose raw RFC 5322 bytes live in the
-- usual per-account message directory; this table carries the delivery
-- state beside it and goes with the row (ON DELETE CASCADE). Rows of the
-- outbox folder never get a message_ops entry: they have no server identity
-- and an operation with uid 0 would wait forever.
--
-- state: queued (waiting for the next attempt), sending (an SMTP session is
-- running), sent (delivered, the Sent copy still pending) or failed
-- (permanent; outbox.retry re-queues). next_attempt_at '' means due now.
-- last_error is the technical text of the last failure, already stripped of
-- control characters and capped at 200 bytes by the store; last_error_code
-- is the api.ErrorCode. Never edit this file after commit.

CREATE TABLE outbox (
    message_id      TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    account_id      TEXT    NOT NULL,
    envelope_from   TEXT    NOT NULL,
    recipients_json TEXT    NOT NULL CHECK (json_valid(recipients_json)),
    state           TEXT    NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued', 'sending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT    NOT NULL DEFAULT '',
    last_error_code INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX outbox_by_account ON outbox (account_id, state, next_attempt_at);
