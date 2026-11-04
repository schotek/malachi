-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0021_crm_links: which messages are saved in a CRM account (kind eway) and
-- what they are linked to there, as the daemon last read it (docs/eway.md
-- D12; store/crm_links.go).
--
-- A display cache: the CRM server is the truth, and a row is checked again
-- against it when it is older than a day. Keyed by the message's
-- Message-ID (messages.rfc_message_id; 'id:' || messages.id for a message
-- without one), so every copy of a message (Inbox and Archive) shows as
-- saved. No foreign key to accounts (there are none in the store): the
-- rows of either account go in the account's removal path.
--
-- Tables and indexes only: no column of an existing table and no trigger,
-- so the board's triggers of 0017/0018 stay the only ones on messages.
--
-- Never edit this file after commit.

CREATE TABLE crm_links (
    crm_account_id  TEXT    NOT NULL,              -- the eway account
    mail_account_id TEXT    NOT NULL,              -- the account the message is in
    msg_key         TEXT    NOT NULL,              -- rfc_message_id, else 'id:' || messages.id
    item_guid       TEXT    NOT NULL,              -- the CRM's e-mail item
    saved_at        INTEGER NOT NULL,              -- unix seconds
    checked_at      INTEGER NOT NULL,              -- unix seconds, last read from the CRM
    private         INTEGER NOT NULL DEFAULT 0 CHECK (private IN (0, 1)),
    relations       TEXT    NOT NULL DEFAULT '[]', -- JSON [{kind, guid, name, detail, superior}]
    PRIMARY KEY (crm_account_id, mail_account_id, msg_key)
);

CREATE INDEX crm_links_by_message ON crm_links (mail_account_id, msg_key);
CREATE INDEX crm_links_by_mail_account ON crm_links (mail_account_id);
