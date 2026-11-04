// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// rawTextTags are the elements the HTML tokenizer reads as raw text until
// the matching end tag (mirrors x/net/html) plus template; their content is
// never text a reader wants and, for the raw ones, could contain tag-like
// text. A self-closing "<script/>" still switches the tokenizer to raw mode,
// so raw tags count even when self-closing.
var rawTextTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "noembed": true,
	"noframes": true, "noscript": true, "plaintext": true, "textarea": true,
	"title": true, "xmp": true,
}

// headTags may appear inside <head>; any other start tag implicitly closes
// an unclosed head, as in the HTML5 tree construction rules.
var headTags = map[string]bool{
	"base": true, "basefont": true, "bgsound": true, "link": true,
	"meta": true, "noscript": true, "script": true, "style": true,
	"template": true, "title": true,
}

// paragraphTags separate their content with a blank line.
var paragraphTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "blockquote": true, "pre": true, "table": true, "ul": true,
	"ol": true, "dl": true, "hr": true, "section": true, "article": true,
	"header": true, "footer": true, "figure": true, "fieldset": true,
	"address": true, "center": true,
}

// lineTags start a new line.
var lineTags = map[string]bool{
	"br": true, "div": true, "li": true, "tr": true, "dd": true, "dt": true,
	"caption": true, "thead": true, "tbody": true, "tfoot": true,
	"nav": true, "aside": true, "main": true, "form": true, "legend": true,
	"figcaption": true, "summary": true, "details": true, "option": true,
	"label": true, "menu": true,
}

// HTMLToText converts HTML into plain text using a linear tokenizer, never
// building a tree. Content of script, style, head, title, template,
// noscript and the other raw-text elements is dropped; block-level
// elements, br, li and tr produce newlines; whitespace is collapsed except
// inside pre; entities are decoded; control characters count as
// whitespace; the output holds at most maxBytes (maxBytes <= 0 means no
// cap) and contains no control characters other than LF and TAB.
//
// The output never contains a tag: tag tokens are consumed, never copied,
// and nothing the writer does can fuse a literal '<' with a following name.
// Text that was entity-encoded in the source ("&lt;b&gt;") decodes to the
// literal characters, as it should — that is text, not markup.
func HTMLToText(src string, maxBytes int64) string {
	z := html.NewTokenizer(strings.NewReader(src))
	w := textWriter{max: maxBytes}
	skip := 0 // depth inside elements whose content is dropped
	inHead := false
	pre := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return w.result()
		case html.TextToken:
			if skip == 0 && !inHead {
				w.text(z.Text(), pre > 0)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch {
			case rawTextTags[tag]:
				skip++
			case tag == "template":
				if tt == html.StartTagToken {
					skip++
				}
			case tag == "body":
				inHead, skip = false, 0
			case tag == "head":
				inHead = true
			case inHead && !headTags[tag]:
				inHead = false
			}
			if skip > 0 || inHead {
				continue
			}
			switch {
			case tag == "pre":
				pre++
				w.newline(2)
			case paragraphTags[tag]:
				w.newline(2)
			case lineTags[tag]:
				w.newline(1)
			case tag == "td" || tag == "th":
				w.cell()
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch {
			case rawTextTags[tag] || tag == "template":
				if skip > 0 {
					skip--
				}
			case tag == "head":
				inHead = false
			}
			if skip > 0 || inHead {
				continue
			}
			switch {
			case tag == "pre":
				if pre > 0 {
					pre--
				}
				w.newline(2)
			case paragraphTags[tag]:
				w.newline(2)
			case lineTags[tag] && tag != "br":
				w.newline(1)
			}
		}
		if w.full() {
			return w.result()
		}
	}
}

// textWriter accumulates the text output with whitespace normalisation:
// outside pre, whitespace runs collapse to one space and vanish at line
// starts; inside pre, spaces and tabs are kept verbatim except at the very
// start of the output and at the end of a line.
type textWriter struct {
	b       strings.Builder
	ws      strings.Builder // pre mode: whitespace not yet known to be followed by text
	max     int64
	started bool // something other than whitespace has been written
	nl      int  // newlines at the end of b
	tab     bool // b ends with a cell separator
	space   bool // collapse mode: a space is pending
}

func (w *textWriter) full() bool {
	return w.max > 0 && int64(w.b.Len()) >= w.max
}

// newline ensures the output ends with at least n newlines (max two).
func (w *textWriter) newline(n int) {
	if !w.started {
		return
	}
	w.space, w.tab = false, false
	w.ws.Reset()
	for w.nl < n {
		w.b.WriteByte('\n')
		w.nl++
	}
}

// cell separates table cells with a tab.
func (w *textWriter) cell() {
	if !w.started || w.nl > 0 || w.tab {
		return
	}
	w.b.WriteByte('\t')
	w.space, w.tab = false, true
}

func (w *textWriter) text(s []byte, pre bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRune(s[i:])
		i += size
		if r == utf8.RuneError && size <= 1 {
			r = '�'
		}
		switch {
		case pre && r == '\n':
			if w.started {
				w.ws.Reset()
				w.b.WriteByte('\n')
				w.nl++
				w.tab = false
			}
		case isTextSpace(r) || unicode.IsControl(r):
			// Control characters (NUL, C1, escape sequences) become a space
			// rather than vanishing: the tokenizer reads "<\x00A>" as text,
			// and dropping the NUL would fuse it into the tag "<A>".
			if !w.started {
				continue
			}
			switch {
			case !pre:
				w.space = true
			case r == '\t':
				w.ws.WriteByte('\t')
			default:
				w.ws.WriteByte(' ')
			}
		default:
			if pre {
				w.b.WriteString(w.ws.String())
				w.ws.Reset()
			} else if w.space && w.nl == 0 && !w.tab {
				w.b.WriteByte(' ')
			}
			w.space = false
			w.b.WriteRune(r)
			w.started, w.nl, w.tab = true, 0, false
		}
		if w.full() {
			return
		}
	}
}

func (w *textWriter) result() string {
	s := w.b.String()
	if w.max > 0 && int64(len(s)) > w.max {
		s = truncateBytes(s, int(w.max))
	}
	return strings.TrimRight(s, " \t\n")
}
