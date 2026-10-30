// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package vcard reads and writes contact cards: vCard 3.0 (RFC 2426) and
// 4.0 (RFC 6350), and vCard 2.1 leniently (quoted-printable values,
// CHARSET, bare parameters). It is the parser of the address books
// (docs/plans/address-book.md A6, A7) and has no dependency outside the
// standard library, golang.org/x/text and the board's text cleaner.
//
// A card is hostile input (CLAUDE.md rule 3): Parse never panics, caps the
// input (MaxCardBytes, MaxProperties, MaxLineBytes, MaxItems), replaces
// invalid UTF-8 and cleans every string of control, bidi and invisible
// characters before it reaches the model.
//
// Patch edits a card the way A6 requires: only the properties whose value
// the user changed are rewritten; every other line (unknown X- properties,
// PHOTO, groups, CATEGORIES, PRODID, other cards of the same object) is
// copied byte for byte, in its place.
package vcard
