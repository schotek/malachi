// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package search turns what the user typed into what the store can run,
// and cuts the excerpt a result is shown with (docs/api.md, search.query).
// It is pure: no SQL and no store access.
//
// The index is an SQLite FTS5 table over the messages (store migration
// 0013_search.sql): contentless, tokenised by unicode61 with diacritics
// removed, so every word typed matches as a case- and diacritic-insensitive
// prefix. Parse reads the documented syntax (free words, quoted phrases,
// from:, to:, subject:, has:attachment, is:unread, is:flagged,
// before:/after:YYYY-MM-DD, in:<folder>) and never fails: anything it does
// not understand is searched as plain words. MatchExpr quotes every value,
// so no user input is ever FTS5 syntax, and the expression is bound as an
// SQL parameter, never concatenated into SQL. Excerpt highlights matches
// in the body with the same folding the tokenizer applies.
package search
