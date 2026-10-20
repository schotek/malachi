// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package composebody is the body of a compose window in two parts (macOS
// ComposeBodyLayout): a new reply or forward starts with the backend's
// complete quoted original (draft.create), which the window shows apart
// from the editor, in a read-only panel under it; the editor starts
// empty. The panel's checkbox decides whether the saved draft and the
// message include the original. Anything else, a draft opened from the
// Drafts folder above all (its body may hold the user's edits), a comment
// and the board's inline reply, opens whole in the editor: nothing here
// looks for a quotation inside a body or splits one. Pure logic, no
// widgets; package compose imports GTK.
package composebody

import (
	"html"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// EmptyBody is the editor's body above an original shown apart.
const EmptyBody = "<p><br></p>"

// Layout is the body's two parts: what the editor shows and the original
// the panel shows ("" without one).
type Layout struct {
	EditorHTML string
	QuotedHTML string
}

// New lays bodyHTML out for a window of mode: a reply, a reply to all or a
// forward that is not a comment, with a body, has its body as the original
// and an empty editor; everything else has its body in the editor. A
// caller that must keep a body whole (a draft opened from Drafts, the
// board's inline reply) passes api.ComposeNew.
func New(mode api.ComposeMode, comment bool, bodyHTML string) Layout {
	switch mode {
	case api.ComposeReply, api.ComposeReplyAll, api.ComposeForward:
		if !comment && bodyHTML != "" {
			return Layout{EditorHTML: EmptyBody, QuotedHTML: bodyHTML}
		}
	}
	return Layout{EditorHTML: bodyHTML}
}

// WithOwn is l with own, the user's own text written before the window
// opened (the chat's quick-reply bar; already safe HTML, conversation
// QuickReplyHTML), in the editor in place of what it would start with;
// the original apart stays as it is. "" keeps l unchanged.
func (l Layout) WithOwn(own string) Layout {
	if own != "" {
		l.EditorHTML = own
	}
	return l
}

// HasQuote says whether the window shows an original apart.
func (l Layout) HasQuote() bool { return l.QuotedHTML != "" }

// HTML is the body to save and send: the editor's own HTML, and below it,
// when included, the original.
func (l Layout) HTML(own string, include bool) string {
	if include && l.HasQuote() {
		return own + "\n" + l.QuotedHTML
	}
	return own
}

// Text is the plain text of the body to save and send: the editor's own
// text and, when included, a blank line and the panel's text of the
// original.
func (l Layout) Text(own, quote string, include bool) string {
	if include && l.HasQuote() {
		return own + "\n\n" + quote
	}
	return own
}

// DraftBody is the body of a draft (draft.create, draft.open) as HTML for
// a window or a quick reply: its HTMLBody, safe since the backend's
// sanitiser produced it; a draft without one (the backend could not quote
// formatted, or it is plain text) has its TextBody escaped (EscapeText).
func DraftBody(d api.Draft) string {
	if d.HTMLBody != "" {
		return d.HTMLBody
	}
	return EscapeText(d.TextBody)
}

// EscapeText escapes plain text for HTML and turns newlines (CRLF
// included) into <br>.
func EscapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}
