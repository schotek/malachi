// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import "strings"

// byteOrderMark may lead a body decoded from UTF-16 or saved by an editor.
const byteOrderMark = '\uFEFF'

// htmlLeadTags are the elements a text/plain part that is really HTML
// starts with: some senders put their HTML markup into the plain-text
// alternative too, and the snippet then showed "<p data-…" instead of the
// text. Only these names count, so the angle brackets plain text uses
// ("<https://example.com>", "<jan@example.com>") never do.
var htmlLeadTags = map[string]bool{
	"html": true, "head": true, "body": true, "meta": true, "style": true,
	"title": true, "link": true, "div": true, "p": true, "span": true,
	"table": true, "tbody": true, "tr": true, "td": true, "center": true,
	"font": true, "br": true, "img": true, "a": true, "b": true, "i": true,
	"u": true, "em": true, "strong": true, "ul": true, "ol": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true,
	"blockquote": true, "pre": true,
}

// LooksLikeHTML reports whether a plain-text body is HTML markup: after
// leading whitespace it starts with a doctype, a comment or a start tag of
// htmlLeadTags (the name followed by whitespace, "/" or ">") whose tag is
// closed by a ">". Text that merely mentions a tag later on is text.
func LooksLikeHTML(text string) bool {
	s := strings.TrimLeft(text, " \t\r\n"+string(byteOrderMark))
	if len(s) < 3 || s[0] != '<' {
		return false
	}
	rest := s[1:]
	if strings.HasPrefix(rest, "!--") || len(rest) >= 8 && strings.EqualFold(rest[:8], "!doctype") {
		return true
	}
	i := 0
	for i < len(rest) && (rest[i] >= 'a' && rest[i] <= 'z' || rest[i] >= 'A' && rest[i] <= 'Z' || rest[i] >= '0' && rest[i] <= '9') {
		i++
	}
	if i == 0 || i == len(rest) || !htmlLeadTags[strings.ToLower(rest[:i])] {
		return false
	}
	switch rest[i] {
	case ' ', '\t', '\r', '\n', '/', '>':
	default:
		return false
	}
	return strings.ContainsRune(rest[i:], '>')
}

// PlainText is the plain text of a text/plain body: the body itself, or
// its text through HTMLToText when the body LooksLikeHTML; trailing
// whitespace is trimmed either way, as the parser stores it. maxBytes caps
// the converted text as HTMLToText does.
func PlainText(text string, maxBytes int64) string {
	if LooksLikeHTML(text) {
		text = HTMLToText(text, maxBytes)
	}
	return strings.TrimRight(text, " \t\n")
}
