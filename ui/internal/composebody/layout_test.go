// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package composebody

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

const original = `<p><br></p><div>A wrote:</div><blockquote type="cite"><p>Original &amp; text</p><img src="cid:image"></blockquote>`

// A new reply or forward keeps the prepared original whole, apart from
// the editor, whether included or not (macOS preparedOriginalIsKeptWhole).
func TestPreparedOriginalIsKeptWhole(t *testing.T) {
	for _, mode := range []api.ComposeMode{api.ComposeReply, api.ComposeReplyAll, api.ComposeForward} {
		l := New(mode, false, original)
		if l.EditorHTML != EmptyBody || l.QuotedHTML != original || !l.HasQuote() {
			t.Errorf("%s: %+v", mode, l)
		}
		if got := l.HTML("<p>Answer</p>", true); got != "<p>Answer</p>\n"+original {
			t.Errorf("%s: included %q", mode, got)
		}
		if got := l.HTML("<p>Answer</p>", false); got != "<p>Answer</p>" {
			t.Errorf("%s: left out %q", mode, got)
		}
		if got := l.Text("Answer", "A wrote:\nOriginal & text", true); got != "Answer\n\nA wrote:\nOriginal & text" {
			t.Errorf("%s: text %q", mode, got)
		}
		if got := l.Text("Answer", "Original", false); got != "Answer" {
			t.Errorf("%s: text left out %q", mode, got)
		}
		if l.QuotedHTML != original {
			t.Errorf("%s: the original changed", mode)
		}
	}
}

// A body that may hold the user's edits, a comment and an unknown mode
// stay whole in the editor (macOS existingBodyIsNeverMistakenForAnOriginal).
func TestExistingBodyIsNeverMistakenForAnOriginal(t *testing.T) {
	body := "<p>User edits</p>" + original
	for _, c := range []struct {
		mode    api.ComposeMode
		comment bool
	}{{api.ComposeNew, false}, {api.ComposeReply, true}, {"", false}, {"draft", false}} {
		l := New(c.mode, c.comment, body)
		if l.EditorHTML != body || l.HasQuote() || l.HTML(body, true) != body || l.Text("x", "y", true) != "x" {
			t.Errorf("%+v: %+v", c, l)
		}
	}
}

// Without an original (draft.create brought none) there is no panel
// (macOS unavailableOriginalDoesNotCreateAnEmptySection).
func TestUnavailableOriginalDoesNotCreateAnEmptySection(t *testing.T) {
	l := New(api.ComposeReply, false, "")
	if l.HasQuote() || l.EditorHTML != "" || l.Text("Answer", "", true) != "Answer" || l.HTML("<p>A</p>", true) != "<p>A</p>" {
		t.Errorf("%+v", l)
	}
}
