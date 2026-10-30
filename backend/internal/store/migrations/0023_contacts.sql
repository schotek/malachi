-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0023_contacts: the address books of connected services and their
-- contacts (docs/plans/address-book.md §4.1, docs/api.md §4.19).
--
-- A cache of the server, like the calendar's (0020): switching contacts
-- off, changing the server or user, or removing the account deletes the
-- account's rows; only ab_books.visible is the user's (it survives a
-- resync, matched by remote_id). The virtual book of recently used
-- addresses is collected_addresses (0009) and has no row here.
--
-- Tables and indexes only: no column of an existing table and no trigger
-- on messages, issues, issue_items, meta or drafts, so the board's
-- triggers of 0017/0018 stay the only ones there. No foreign key to
-- accounts (there are none in the store): the rows go in the account's
-- removal path; ab_contacts and ab_emails go with their book (ON DELETE
-- CASCADE).
--
-- Every TEXT time is RFC 3339 UTC with milliseconds (as the strftime
-- defaults). ab_contacts has triggers (its search index) and is written
-- only with INSERT ... ON CONFLICT DO UPDATE, UPDATE and DELETE: never
-- INSERT OR REPLACE, which would bypass the delete trigger; the same holds
-- for ab_books (its delete cascades).
--
-- The search index ab_contacts_fts is contentless with contentless_delete,
-- like messages_fts (0013) and cal_events_fts (0020), and for the same
-- reason keyed by a rowid of its own: ab_contacts.id is a TEXT primary key
-- and the implicit rowid of such a table may change on VACUUM, so
-- ab_contacts_fts_docs maps a stable docid to the contact id.
--
-- Never edit this file after commit.

-- One address book (Graph contact folder, CardDAV collection) of an
-- account. id is 'b_' + hex; remote_id the Graph folder id or the CardDAV
-- href. sync_token (CardDAV sync-collection), ctag and delta_link (Graph)
-- are the engine's; version is the provider's data version (a change
-- lists the book again whole).
CREATE TABLE ab_books (
    id           TEXT PRIMARY KEY,
    account_id   TEXT NOT NULL,
    remote_id    TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    read_only    INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    visible      INTEGER NOT NULL DEFAULT 1 CHECK (visible IN (0, 1)),
    sync_token   TEXT NOT NULL DEFAULT '',
    ctag         TEXT NOT NULL DEFAULT '',
    delta_link   TEXT NOT NULL DEFAULT '',
    version      INTEGER NOT NULL DEFAULT 0,
    last_sync_at TEXT NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX ab_books_remote ON ab_books (account_id, remote_id);

-- One contact as the server has it. id is 'p_' + hex; remote_id the
-- CardDAV href or the Graph contact id; etag the CardDAV ETag or the Graph
-- changeKey (ContactCard.revision). raw is the vCard (raw_kind 'vcard') or
-- the Graph JSON ('graph') as received, ≤ 1 MiB; an update patches it
-- (docs/plans/address-book.md A6). The other columns are derived from raw,
-- cleaned: display_name, sort_key (family name first when known, lower
-- case), section (its index letter, upper case, or '#'), org,
-- primary_email (the first address, normalised) and emails (every address,
-- space separated, for the search index).
CREATE TABLE ab_contacts (
    id            TEXT PRIMARY KEY,
    book_id       TEXT NOT NULL REFERENCES ab_books(id) ON DELETE CASCADE,
    remote_id     TEXT NOT NULL,
    etag          TEXT NOT NULL DEFAULT '',
    raw           BLOB NOT NULL,
    raw_kind      TEXT NOT NULL CHECK (raw_kind IN ('vcard', 'graph')),
    display_name  TEXT NOT NULL DEFAULT '',
    sort_key      TEXT NOT NULL DEFAULT '',
    section       TEXT NOT NULL DEFAULT '#',
    org           TEXT NOT NULL DEFAULT '',
    primary_email TEXT NOT NULL DEFAULT '',
    emails        TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX ab_contacts_remote ON ab_contacts (book_id, remote_id);
CREATE INDEX ab_contacts_sort ON ab_contacts (book_id, sort_key);

-- Every address of a contact, normalised (lower case): recipient
-- completion and contact.lookup. label is the ContactLabel.
CREATE TABLE ab_emails (
    contact_id TEXT NOT NULL REFERENCES ab_contacts(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    address    TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (contact_id, position)
) WITHOUT ROWID;
CREATE INDEX ab_emails_address ON ab_emails (address);

-- Contact search: names, organisation and addresses.
CREATE TABLE ab_contacts_fts_docs (
    docid      INTEGER PRIMARY KEY,
    contact_id TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE ab_contacts_fts USING fts5(
    name, org, emails,
    content = '',
    contentless_delete = 1,
    prefix = '2 3',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER ab_contacts_fts_ai AFTER INSERT ON ab_contacts BEGIN
    INSERT INTO ab_contacts_fts_docs (contact_id) VALUES (new.id);
    INSERT INTO ab_contacts_fts (rowid, name, org, emails)
        SELECT d.docid, new.display_name, new.org, new.emails
          FROM ab_contacts_fts_docs d
         WHERE d.contact_id = new.id;
END;

CREATE TRIGGER ab_contacts_fts_au
AFTER UPDATE OF display_name, org, emails ON ab_contacts
WHEN old.display_name IS NOT new.display_name OR old.org IS NOT new.org
  OR old.emails IS NOT new.emails
BEGIN
    DELETE FROM ab_contacts_fts WHERE rowid = (SELECT docid FROM ab_contacts_fts_docs WHERE contact_id = new.id);
    INSERT INTO ab_contacts_fts (rowid, name, org, emails)
        SELECT d.docid, new.display_name, new.org, new.emails
          FROM ab_contacts_fts_docs d
         WHERE d.contact_id = new.id;
END;

CREATE TRIGGER ab_contacts_fts_ad AFTER DELETE ON ab_contacts BEGIN
    DELETE FROM ab_contacts_fts WHERE rowid = (SELECT docid FROM ab_contacts_fts_docs WHERE contact_id = old.id);
    DELETE FROM ab_contacts_fts_docs WHERE contact_id = old.id;
END;
