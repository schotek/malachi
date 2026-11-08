// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The search in the user's own words (target App only): what the user
// typed into the search field ("invoices from Jana in March") goes to the
// assistant, which answers with a query in Malachi Mail's search syntax
// (backend/internal/search/query.go, docs/api.md search.query); the
// application puts it into the field and searches as if it had been typed.
// The prompt and the answer's schema are the daemon's (assistant.prompt,
// purpose search, api.SearchInput; api.PromptResult.JSONSchema); the
// answer is read with ParseSearchQuery. Only the typed words go to the
// model, no mail.
//
// This file holds no translatable text (SearchTexts and SearchFailedText,
// in assistant.go, are the application's).

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSearchQueryBytes is the longest query ParseSearchQuery returns, in
// bytes: the daemon's cap (api.MaxSearchQueryBytes, docs/api.md
// search.query).
const MaxSearchQueryBytes = 1024

// ParseSearchQuery reads the query of the search's answer, the result
// event's structured_output (Event.Structured): the string member "query"
// of a JSON object (the last one when it is there twice) as one line,
// every run of white space and control characters one space, trimmed and
// cut to at most MaxSearchQueryBytes bytes at a character boundary. False
// when the answer is not a JSON object, has no "query", or its "query" is
// not a string or leaves nothing.
func ParseSearchQuery(structured []byte) (string, bool) {
	o := objectOf(bytes.TrimSpace(structured))
	if o == nil {
		return "", false
	}
	s, ok := str(o["query"])
	if !ok {
		return "", false
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	q := b.String()
	if len(q) > MaxSearchQueryBytes {
		cut := MaxSearchQueryBytes
		for cut > 0 && !utf8.RuneStart(q[cut]) {
			cut--
		}
		q = strings.TrimRight(q[:cut], " ")
	}
	if q == "" {
		return "", false
	}
	return q, true
}
