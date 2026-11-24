// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Folding the quoted history (Input.FoldQuoted, message.body foldQuoted):
// the same history trimQuoted would cut (quoteStart) stays in the body,
// folded. The view runs without JavaScript, so the fold is plain HTML: a
// <details class="malachi-quoted"> with a <summary> of three bullets, the
// toggle, stands where the history starts, and every node of the history
// carries the class malachi-quoted-more (a text node goes into a <span>
// that does). The client's stylesheet hides the marked nodes while the
// toggle is closed:
//
//	html:has(details.malachi-quoted:not([open])) .malachi-quoted-more { display: none }
//
// so a click on the toggle shows them in place, with nothing asked of the
// daemon and nothing loaded again. The history is not moved into the
// <details>: it runs from inside the element where it starts to the end of
// the body, across levels (a table row, the rest of a layout table), and
// moving it would break that structure.
//
// Only the walk adds the classes, to the nodes foldQuoted lists: the
// classes an e-mail writes itself are dropped from every element
// (stripFoldClasses), so mail cannot fake a fold or hide its own text
// behind one. Everything else of the walk applies to the fold as to any
// element; folding can make the output larger by the toggle and the
// classes, never less sanitised. Sanitising a folded output again drops
// the fold.

// The classes of the fold.
const (
	foldToggleClass = "malachi-quoted"
	foldHiddenClass = "malachi-quoted-more"
	foldClassPrefix = "malachi-quoted"
)

// foldDots is the face of the toggle: three bullets (U+2022), the same in
// every language.
const foldDots = "•••"

// foldMaxClimb bounds the climb of the toggle out of inline and table
// elements.
const foldMaxClimb = 256

// foldQuoted folds the quoted history of the parsed body and returns the
// class the walk gives each node of the fold (the toggle and the hidden
// nodes); nil when there is no history to fold. body is the parser's
// <body> element.
func foldQuoted(body *html.Node) map[*html.Node]string {
	start := quoteStart(body)
	if start == nil {
		return nil
	}
	marks := make(map[*html.Node]string)
	hide := func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			marks[n] = foldHiddenClass
		case html.TextNode:
			if blankText(n.Data) {
				return
			}
			span := &html.Node{Type: html.ElementNode, Data: "span", DataAtom: atom.Span}
			n.Parent.InsertBefore(span, n)
			n.Parent.RemoveChild(n)
			span.AppendChild(n)
			marks[span] = foldHiddenClass
		}
	}
	// The nodes cutFrom would remove, in the same order.
	parent := start.Parent
	for n := start; n != nil; {
		next := n.NextSibling
		hide(n)
		n = next
	}
	for a := parent; a != nil && a != body && a.Parent != nil; a = a.Parent {
		for n := a.NextSibling; n != nil; {
			next := n.NextSibling
			hide(n)
			n = next
		}
	}

	// The toggle goes right before the history, unless that is inside an
	// element where a <details> does not belong (inline, a paragraph, the
	// rows of a table): then right after the outermost such element, whose
	// next siblings are all part of the history.
	at := start
	for range foldMaxClimb {
		p := at.Parent
		if p == nil || p == body || p.Type != html.ElementNode || !foldClimbsOut(p) {
			break
		}
		at = p
	}
	summary := &html.Node{Type: html.ElementNode, Data: "summary", DataAtom: atom.Summary}
	summary.AppendChild(&html.Node{Type: html.TextNode, Data: foldDots})
	toggle := &html.Node{Type: html.ElementNode, Data: "details", DataAtom: atom.Details}
	toggle.AppendChild(summary)
	if at == start {
		at.Parent.InsertBefore(toggle, at)
	} else {
		at.Parent.InsertBefore(toggle, at.NextSibling)
	}
	marks[toggle] = foldToggleClass
	return marks
}

// foldClimbsOut reports whether the toggle must not stand inside element p.
func foldClimbsOut(p *html.Node) bool {
	switch p.Data {
	case "p", "table", "tbody", "thead", "tfoot", "tr", "caption", "colgroup":
		return true
	}
	return inlineElem[p.Data]
}

// folded reports whether element n of a sanitised tree is part of the
// fold (the toggle or a hidden node): only the walk writes the classes.
func folded(n *html.Node) bool {
	return hasClass(n, foldToggleClass, foldHiddenClass)
}

// foldClass is the class attribute value for an element: val, the
// e-mail's own classes, without any fold class, plus fold, the walk's own
// (empty for none). ok is false when nothing is left.
func foldClass(val, fold string) (string, bool) {
	var keep []string
	for _, c := range strings.Fields(val) {
		if strings.HasPrefix(strings.ToLower(c), foldClassPrefix) {
			continue
		}
		keep = append(keep, c)
	}
	if fold != "" {
		keep = append(keep, fold)
	}
	if len(keep) == 0 {
		return "", false
	}
	return strings.Join(keep, " "), true
}
