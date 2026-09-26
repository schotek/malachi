// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package search

import "strings"

// The FTS5 columns of messages_fts (store migration 0013_search.sql) that
// field prefixes restrict a term to; FieldAny leaves the term unrestricted.
var fieldColumns = map[Field]string{
	FieldSubject:    "subject",
	FieldSender:     "sender",
	FieldRecipients: "recipients",
}

// MatchExpr is the FTS5 MATCH expression of the terms, "" when there are
// none (the filters are SQL, not FTS). Every value is an FTS5 string with
// its double quotes doubled, so nothing the user typed is operator syntax
// (AND, NEAR, *, ^, column filters): a word becomes a prefix query
// ("faktura"*), a phrase stays exact, a field prefix a column filter, and
// the terms are joined with AND. A word the tokenizer splits (e-mail,
// jan@firma) is a phrase of its parts with only the last one a prefix.
func (q Query) MatchExpr() string {
	parts := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		s := `"` + strings.ReplaceAll(t.Text, `"`, `""`) + `"`
		if !t.Phrase {
			s += "*"
		}
		if col, ok := fieldColumns[t.Field]; ok {
			s = col + " : " + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " AND ")
}
