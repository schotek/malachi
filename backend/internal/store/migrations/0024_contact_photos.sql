-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0024_contact_photos: the photos of contacts, read only
-- (docs/plans/address-book.md §9, F7; docs/api.md §4.19 contact.photo).
--
-- A cache of the server like the rest of the address books (0023): the
-- rows go with their contact (ON DELETE CASCADE from ab_contacts, so with
-- the book and the account too). A table of its own so that contact.list
-- never reads a BLOB. No trigger and no column of an existing table.
--
-- revision is the contact's version the row was made from (ab_contacts.etag:
-- the CardDAV ETag or the Graph changeKey); a row of another revision is
-- stale. state: 'ok' (data holds the picture as the daemon re-encoded it,
-- internal/contactphoto: JPEG or PNG, at most 256 × 256 px and 64 KiB,
-- mime its media type), 'none' (the contact has no photo, or only a link,
-- which is never fetched), 'refused' (the picture could not be decoded or
-- broke a limit). mime and data are empty unless 'ok'. A Graph contact
-- whose row is missing or stale is "remote": contact.photo fetches it.
--
-- Never edit this file after commit.

CREATE TABLE ab_photos (
    contact_id TEXT PRIMARY KEY REFERENCES ab_contacts(id) ON DELETE CASCADE,
    revision   TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL CHECK (state IN ('ok', 'none', 'refused')),
    mime       TEXT NOT NULL DEFAULT '',
    data       BLOB NOT NULL DEFAULT x'' CHECK (length(data) <= 65536),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
