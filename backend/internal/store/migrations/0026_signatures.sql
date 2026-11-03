-- SPDX-FileCopyrightText: 2026 Vladislav Janeček
-- SPDX-License-Identifier: AGPL-3.0-only

-- 0026_signatures: the verdicts of signed mail and what they rest on
-- (docs/plans/signatures.md §4, docs/api.md §4.22; store/signatures.go).
--
-- message_signatures holds one verdict per message whose signature covers
-- the whole message; a message without one has no row. It is a cache: the
-- daemon computes it from the raw file (internal/signature) when the
-- message is stored, and again when verifier_version or trust_key no
-- longer match (the backfill in core.Maintain, stale = 1 after a change of
-- trust). A row goes with its message (ON DELETE CASCADE). details is the
-- api.SignatureDetails JSON the details dialog shows (untrusted strings
-- already cleaned). cert_sha256 is the S/MIME signer's certificate (the
-- revocation check recomputes its messages), key_id and signer_email the
-- OpenPGP signing key and the From address it was judged against.
--
-- smime_revocation is what the last revocation check of a certificate
-- found; pgp_keys the stored OpenPGP public keys (never a secret one).
--
-- No trigger on messages or on any other table. Times are RFC 3339 UTC.
--
-- Never edit this file after commit.

CREATE TABLE message_signatures (
    message_id       TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    account_id       TEXT NOT NULL,
    kind             TEXT NOT NULL,
    status           TEXT NOT NULL,
    reason           TEXT NOT NULL DEFAULT '',
    signer_name      TEXT NOT NULL DEFAULT '',
    signer_email     TEXT NOT NULL DEFAULT '',
    fingerprint      TEXT NOT NULL DEFAULT '',
    notes            TEXT NOT NULL DEFAULT '',
    part_id          TEXT NOT NULL DEFAULT '',
    cert_sha256      TEXT NOT NULL DEFAULT '',
    key_id           TEXT NOT NULL DEFAULT '',
    from_email       TEXT NOT NULL DEFAULT '',
    details          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(details)),
    verifier_version TEXT NOT NULL,
    trust_key        TEXT NOT NULL DEFAULT '',
    stale            INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0, 1)),
    verified_at      TEXT NOT NULL
) WITHOUT ROWID;

CREATE INDEX message_signatures_cert ON message_signatures (cert_sha256) WHERE cert_sha256 != '';
CREATE INDEX message_signatures_stale ON message_signatures (stale) WHERE stale = 1;

CREATE TABLE smime_revocation (
    cert_sha256 TEXT PRIMARY KEY,
    state       TEXT NOT NULL,
    revoked_at  TEXT NOT NULL DEFAULT '',
    checked_at  TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE pgp_keys (
    fingerprint TEXT PRIMARY KEY,
    key_data    BLOB NOT NULL,
    user_ids    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(user_ids)),
    emails      TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(emails)),
    key_ids     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(key_ids)),
    trust       TEXT NOT NULL CHECK (trust IN ('seen', 'verified')),
    source      TEXT NOT NULL,
    first_seen  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT NOT NULL DEFAULT '',
    algorithm   TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
