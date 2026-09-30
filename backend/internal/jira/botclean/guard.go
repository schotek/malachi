// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"errors"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxClones bounds the elements the parser may have to re-create for
// formatting elements left open (see inflates).
const maxClones = 100_000

// errInflates refuses input before it is parsed.
var errInflates = errors.New("comment html refused: the parser could inflate it")

// formatting are the elements the parser keeps in its list of active
// formatting elements.
var formatting = map[atom.Atom]bool{
	atom.A: true, atom.B: true, atom.Big: true, atom.Code: true, atom.Em: true,
	atom.Font: true, atom.I: true, atom.Nobr: true, atom.S: true, atom.Small: true,
	atom.Strike: true, atom.Strong: true, atom.Tt: true, atom.U: true,
}

// inflates reports input whose tree could be far larger than the input.
//
// The HTML parser re-opens every formatting element (b, i, a, font, …) that
// something else closed before its own end tag, at every following text and
// element; with distinct attributes the list of such elements is not
// bounded, and "<p><b a1>…<b a400>x<p>x<p>x…" parses 20 KB into two million
// elements. Without foreign content (svg, math) the parser splits the input
// into exactly the tokens a plain html.Tokenizer does, so they can be
// counted first: per tag name, formatting start tags minus end tags is an
// upper bound of the elements that may be re-opened, and its sum over all
// tokens one of the elements re-opened in all. Input with foreign content,
// where the parser reads raw text and CDATA differently, is refused
// outright: comments rendered by Jira have none.
func inflates(src string) bool {
	if hasForeignContent(src) {
		return true
	}
	z := html.NewTokenizer(strings.NewReader(src))
	open := make(map[atom.Atom]int)
	pending, clones := 0, 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.CommentToken, html.DoctypeToken:
			continue
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if a := atom.Lookup(name); formatting[a] {
				open[a]++
				pending++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if a := atom.Lookup(name); formatting[a] && open[a] > 0 {
				open[a]--
				pending--
			}
		}
		// The parser keeps at most 512 elements open, clones included.
		clones += min(pending, 512)
		if clones > maxClones {
			return true
		}
	}
}

// hasForeignContent reports a "<svg" or "<math", in any case, anywhere.
func hasForeignContent(src string) bool {
	for rest := src; ; {
		i := strings.IndexByte(rest, '<')
		if i < 0 {
			return false
		}
		rest = rest[i+1:]
		if hasPrefixFold(rest, "svg") || hasPrefixFold(rest, "math") {
			return true
		}
	}
}

// hasPrefixFold is strings.HasPrefix ignoring ASCII case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
