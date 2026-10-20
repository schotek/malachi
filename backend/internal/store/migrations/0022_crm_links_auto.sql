-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0022_crm_links_auto: a CRM record saved by the daemon itself, following a
-- conversation the user saved (docs/eway.md D22; store/crm_links.go).
-- 0021 stays as it was committed: stores that ran it get the column here.
--
-- A column of crm_links only; no trigger.
--
-- Never edit this file after commit.

ALTER TABLE crm_links ADD COLUMN auto INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1));
