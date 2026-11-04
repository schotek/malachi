// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package search

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// excerptLead is how much text, in bytes, an excerpt keeps before the
// first match, so the match is read in context.
const excerptLead = 40

// needle is a term's folded tokens; the last one matches as a prefix
// unless the term is a phrase.
type needle struct {
	keys   []string
	prefix bool
}

type span struct{ start, end int }

// Excerpt cuts the plain-text excerpt a result is shown with: at most
// maxRunes runes of text around the first match of the query's free words
// and phrases (field-restricted terms name the subject or the people, not
// the body), whitespace collapsed, control and invisible formatting
// characters dropped (the joiners some scripts need are kept), an ellipsis
// where text was cut. ranges are the byte spans of the matched words in
// the excerpt, sorted and merged, on UTF-8 boundaries; a word matched as a
// prefix is highlighted whole. ok is false when the text has no match.
func Excerpt(text string, q Query, maxRunes int) (excerpt string, ranges []api.MatchRange, ok bool) {
	ns := needles(q)
	if len(ns) == 0 || text == "" || maxRunes <= 0 {
		return "", nil, false
	}
	spans := findSpans(Tokens(text), ns)
	if len(spans) == 0 {
		return "", nil, false
	}
	begin := windowStart(text, spans[0].start)

	need := make(map[int]bool, 2*len(spans))
	for _, s := range spans {
		need[s.start], need[s.end] = true, true
	}
	at := make(map[int]int, len(need))
	var b strings.Builder
	if begin > 0 {
		b.WriteString("…")
	}
	runes, stop := 0, len(text)
	lastSpace := true // no space at the start
	for i, r := range text[begin:] {
		i += begin
		if need[i] {
			at[i] = b.Len()
		}
		if runes >= maxRunes {
			stop = i
			break
		}
		switch {
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteByte(' ')
				runes++
			}
			lastSpace = true
			continue
		case dropInExcerpt(r):
			continue
		}
		b.WriteRune(r) // an invalid byte comes out as U+FFFD
		runes++
		lastSpace = false
	}
	if stop == len(text) && need[stop] {
		at[stop] = b.Len()
	}
	excerpt = strings.TrimRight(b.String(), " ")
	limit := len(excerpt)
	if stop < len(text) && strings.TrimSpace(text[stop:]) != "" {
		excerpt += "…"
	}

	for _, s := range spans {
		st, inside := at[s.start]
		if !inside || st >= limit {
			continue
		}
		en, done := at[s.end]
		if !done || en > limit {
			en = limit
		}
		if st < en {
			ranges = append(ranges, api.MatchRange{Start: st, End: en})
		}
	}
	return excerpt, mergeRanges(ranges), true
}

// needles are the query's free words and phrases as folded tokens.
func needles(q Query) []needle {
	var out []needle
	for _, t := range q.Terms {
		if t.Field != FieldAny {
			continue
		}
		var keys []string
		for _, tok := range Tokens(t.Text) {
			keys = append(keys, tok.Key)
		}
		if len(keys) > 0 {
			out = append(out, needle{keys: keys, prefix: !t.Phrase})
		}
	}
	return out
}

// findSpans finds every place a needle matches consecutive tokens, in
// text order.
func findSpans(toks []Token, ns []needle) []span {
	var out []span
	for i := range toks {
		for _, n := range ns {
			if i+len(n.keys) > len(toks) {
				continue
			}
			if matchesAt(toks[i:], n) {
				out = append(out, span{toks[i].Start, toks[i+len(n.keys)-1].End})
			}
		}
	}
	return out
}

func matchesAt(toks []Token, n needle) bool {
	for k, key := range n.keys {
		got := toks[k].Key
		if k == len(n.keys)-1 && n.prefix {
			if !strings.HasPrefix(got, key) {
				return false
			}
		} else if got != key {
			return false
		}
	}
	return true
}

// windowStart is where the excerpt opens: excerptLead bytes before the
// first match, moved to the start of a word when there is a space in
// between.
func windowStart(text string, first int) int {
	b := first - excerptLead
	if b <= 0 {
		return 0
	}
	for b > 0 && !utf8.RuneStart(text[b]) {
		b--
	}
	if sp := strings.IndexFunc(text[b:first], unicode.IsSpace); sp >= 0 {
		b += sp
		for b < first {
			r, w := utf8.DecodeRuneInString(text[b:])
			if !unicode.IsSpace(r) {
				break
			}
			b += w
		}
	}
	return b
}

// dropInExcerpt reports the characters an excerpt leaves out: controls
// and invisible formatting (bidi overrides, zero-width spaces), except the
// zero-width joiner and non-joiner some scripts are spelt with.
func dropInExcerpt(r rune) bool {
	if unicode.IsControl(r) {
		return true
	}
	return unicode.Is(unicode.Cf, r) && r != '‌' && r != '‍'
}

// mergeRanges sorts the ranges and joins the ones that overlap or touch.
func mergeRanges(rs []api.MatchRange) []api.MatchRange {
	if len(rs) < 2 {
		return rs
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
