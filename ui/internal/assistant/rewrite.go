// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The compose window's rewrite (target App only): a passage of the message
// being written, the selection or the user's own text above the quoted
// original, goes to the assistant with an instruction, and the answer,
// cleaned (CleanRewrite), replaces the passage or goes below it as plain
// text. The prompt is the daemon's (assistant.prompt, purpose rewrite,
// api.RewriteInput): the client names the preset (Rewrite) or the user's
// own instruction and the passage, which may hold text quoted from other
// people's mail, which the system prompt says is data.
//
// This file holds no translatable text (RewriteLabel, in assistant.go,
// names the presets).

import (
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Rewrite is what the compose window's assistant does with a passage: one
// of the presets, or Custom, the user's own instruction. Its values are
// api.RewritePreset's.
type Rewrite string

// The rewrites.
const (
	Politer   Rewrite = Rewrite(api.RewritePoliter)
	Shorter   Rewrite = Rewrite(api.RewriteShorter)
	Fix       Rewrite = Rewrite(api.RewriteFix)
	ToEnglish Rewrite = Rewrite(api.RewriteToEnglish)
	Custom    Rewrite = Rewrite(api.RewriteCustom)
)

// Rewrites are the presets in the order of the popover; Custom is its free
// field.
var Rewrites = []Rewrite{Politer, Shorter, Fix, ToEnglish}

// Preset is the rewrite as the daemon's input names it.
func (r Rewrite) Preset() api.RewritePreset { return api.RewritePreset(r) }

// The markers around the passage in the daemon's rewrite message, which
// a model may echo (CleanRewrite takes them off).
const (
	passageOpen  = "<<<"
	passageClose = ">>>"
)

// CleanRewrite is the model's answer as the text that goes into the
// message: CRLF as LF, the control characters other than "\n" and "\t"
// left out (a lone CR too), invalid UTF-8 as U+FFFD, trimmed; then, in
// this order and each at most once, a pair of ``` fences around the whole
// answer (with an optional language tag on the opening line), the "<<<"
// and ">>>" markers of the daemon's rewrite message at its start and end,
// a pair of quotation marks around the whole answer (straight or
// typographic, and only when neither mark occurs inside), and once more
// the markers; the rest trimmed after each step. "" when nothing is left.
func CleanRewrite(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimSpace(b.String())
	s = stripFences(s)
	s = stripMarkers(s)
	s = stripQuotes(s)
	s = stripMarkers(s)
	return s
}

// fence is Markdown's code fence.
const fence = "```"

// stripFences removes one pair of ``` fences that wrap all of s (trimmed):
// the text between them, without a first line that is only a language tag
// (fenceTag), trimmed. s is left alone when it does not start and end with
// a fence or holds another fence inside.
func stripFences(s string) string {
	if len(s) < 2*len(fence) || !strings.HasPrefix(s, fence) || !strings.HasSuffix(s, fence) {
		return s
	}
	inner := s[len(fence) : len(s)-len(fence)]
	if strings.Contains(inner, fence) {
		return s
	}
	if tag, rest, ok := strings.Cut(inner, "\n"); ok && fenceTag(tag) {
		inner = rest
	}
	return strings.TrimSpace(inner)
}

// fenceTag says whether the rest of a fence's opening line is a language
// tag: ASCII letters, digits and _ + - . # only (none is fine), trailing
// spaces and tabs allowed.
func fenceTag(s string) bool {
	for _, c := range []byte(strings.TrimRight(s, " \t")) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("_+-.#", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// stripMarkers removes the "<<<" at the start of s and the ">>>" at its end
// (each when there), trimming what remains.
func stripMarkers(s string) string {
	if rest, ok := strings.CutPrefix(s, passageOpen); ok {
		s = strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutSuffix(s, passageClose); ok {
		s = strings.TrimSpace(rest)
	}
	return s
}

// quotePairs are the quotation marks stripQuotes takes off, opening and
// closing: straight, English, Czech and German, Swedish, the guillemets
// both ways, and the single ones.
var quotePairs = [][2]string{
	{`"`, `"`},
	{"“", "”"},
	{"„", "“"},
	{"„", "”"},
	{"”", "”"},
	{"«", "»"},
	{"»", "«"},
	{"'", "'"},
	{"‘", "’"},
	{"‚", "‘"},
	{"‚", "’"},
}

// stripQuotes removes the first pair of quotePairs that wraps all of s when
// neither of its marks occurs between them, and trims the rest.
func stripQuotes(s string) string {
	for _, q := range quotePairs {
		open, close := q[0], q[1]
		if len(s) < len(open)+len(close) || !strings.HasPrefix(s, open) || !strings.HasSuffix(s, close) {
			continue
		}
		inner := s[len(open) : len(s)-len(close)]
		if strings.Contains(inner, open) || strings.Contains(inner, close) {
			continue
		}
		return strings.TrimSpace(inner)
	}
	return s
}
