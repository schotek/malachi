// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package search

import (
	"strings"
	"time"
	"unicode"
)

// Field is the part of a message a term is looked for in.
type Field int

const (
	FieldAny        Field = iota // any indexed text: subject, people, attachment names, body
	FieldSubject                 // subject:
	FieldSender                  // from:
	FieldRecipients              // to: (To, Cc and Bcc)
)

// Term is one word or quoted phrase of the query. A word matches as a
// prefix, a phrase as whole words in order.
type Term struct {
	Field  Field
	Text   string
	Phrase bool
}

// Date is a calendar day of before: or after:; the caller decides the
// time zone it starts in.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Start is the first instant of the day in loc.
func (d Date) Start(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// Query is a parsed search: all of its terms and filters must hold.
type Query struct {
	Terms         []Term
	HasAttachment bool
	Unread        bool
	Flagged       bool
	After, Before *Date // after: inclusive, before: exclusive
	In            []string
}

// Empty reports whether the query has nothing to search by.
func (q Query) Empty() bool {
	return len(q.Terms) == 0 && !q.HasAttachment && !q.Unread && !q.Flagged &&
		q.After == nil && q.Before == nil && len(q.In) == 0
}

// Size counts the terms and filters, the measure api.MaxSearchTerms caps.
func (q Query) Size() int {
	n := len(q.Terms) + len(q.In)
	for _, set := range []bool{q.HasAttachment, q.Unread, q.Flagged, q.After != nil, q.Before != nil} {
		if set {
			n++
		}
	}
	return n
}

// HasText reports whether any term needs the full-text index.
func (q Query) HasText() bool { return len(q.Terms) > 0 }

// Parse reads the query syntax of docs/api.md. It never fails: a prefix it
// does not know, a filter value it cannot read (has:foo, a bad date) or an
// empty value is searched as a plain word, an unbalanced quote runs to the
// end, control characters are dropped, and a word with no letter or digit
// is left out (the tokenizer would find nothing in it).
func Parse(s string) Query {
	var q Query
	rs := []rune(strings.Map(dropControl, s))
	i := 0
	for i < len(rs) {
		r := rs[i]
		if unicode.IsSpace(r) {
			i++
			continue
		}
		if isOpenQuote(r) {
			text, next := readQuoted(rs, i+1)
			i = next
			q.addTerm(FieldAny, text, true)
			continue
		}
		start := i
		for i < len(rs) && !unicode.IsSpace(rs[i]) && !(i > start && rs[i-1] == ':' && isOpenQuote(rs[i])) {
			i++
		}
		word := string(rs[start:i])
		name, value, found := strings.Cut(word, ":")
		if found && name != "" {
			quoted := value == "" && i < len(rs) && isOpenQuote(rs[i])
			if quoted {
				v, next := readQuoted(rs, i+1)
				i = next
				if !q.applyPrefix(strings.ToLower(name), v, true) {
					q.addTerm(FieldAny, name, false)
					q.addTerm(FieldAny, v, true)
				}
				continue
			}
			if q.applyPrefix(strings.ToLower(name), value, false) {
				continue
			}
		}
		q.addTerm(FieldAny, word, false)
	}
	return q
}

// applyPrefix applies name:value and reports whether it was understood.
func (q *Query) applyPrefix(name, value string, quoted bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch name {
	case "from":
		return q.addTerm(FieldSender, value, quoted)
	case "to":
		return q.addTerm(FieldRecipients, value, quoted)
	case "subject":
		return q.addTerm(FieldSubject, value, quoted)
	case "has":
		if strings.EqualFold(value, "attachment") {
			q.HasAttachment = true
			return true
		}
	case "is":
		switch strings.ToLower(value) {
		case "unread":
			q.Unread = true
			return true
		case "flagged":
			q.Flagged = true
			return true
		}
	case "before", "after":
		t, err := time.Parse("2006-01-02", value)
		if err != nil {
			return false
		}
		d := &Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
		if name == "before" {
			q.Before = d
		} else {
			q.After = d
		}
		return true
	case "in":
		q.In = append(q.In, value)
		return true
	}
	return false
}

// addTerm appends a term with something the tokenizer can index and
// reports whether it did (a prefix whose value has none is still
// understood, it just adds nothing).
func (q *Query) addTerm(f Field, text string, phrase bool) bool {
	text = strings.TrimSpace(text)
	if !hasWordRune(text) {
		return f != FieldAny
	}
	q.Terms = append(q.Terms, Term{Field: f, Text: text, Phrase: phrase})
	return true
}

// readQuoted reads up to the closing quote (or the end) from rs[i:] and
// returns the text and the index after it.
func readQuoted(rs []rune, i int) (string, int) {
	start := i
	for i < len(rs) && !isCloseQuote(rs[i]) {
		i++
	}
	text := string(rs[start:i])
	if i < len(rs) {
		i++ // the closing quote
	}
	return text, i
}

// Straight quotes and the typographic ones of Czech („…“) and English
// (“…”); “ opens in English and closes in Czech.
func isOpenQuote(r rune) bool  { return r == '"' || r == '„' || r == '“' }
func isCloseQuote(r rune) bool { return r == '"' || r == '“' || r == '”' }

// dropControl removes control characters (NUL included); strings.Map drops
// a rune mapped to -1.
func dropControl(r rune) rune {
	if unicode.IsControl(r) && !unicode.IsSpace(r) {
		return -1
	}
	return r
}

// hasWordRune reports whether s has a rune the tokenizer keeps in a token.
func hasWordRune(s string) bool {
	for _, r := range s {
		if isTokenRune(r) {
			return true
		}
	}
	return false
}
