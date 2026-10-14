// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// RulesVersion 4: an Outlook header block without a separator line ends
// the own text of a plain-text message (a question in the quote below it
// no longer counts, nor does a commitment quoted from it), and a text that
// starts with one is shaped like a forward. Apple Mail's "Begin forwarded
// message:" stays a known miss: the line above the block introduces it.
func TestOutlookHeaderBlock(t *testing.T) {
	reply := fixture(t, "outlook-header-reply.txt")
	own := OwnText(Member{Text: reply})
	if own != "Hi Bob,\n\nI will send the corrected table tomorrow.\n\nJan" {
		t.Fatalf("own text %q", own)
	}
	if QuoteInOwnText(Member{Text: reply}, "Could you send the corrected table") {
		t.Fatal("a quote from the history counted as the user's own text")
	}
	if !QuoteInOwnText(Member{Text: reply}, "I will send the corrected table tomorrow") {
		t.Fatal("the user's own words were not found")
	}
	if e := BoardMessageExcerpt(reply); !e.Trimmed || strings.Contains(e.Text, "Could you") {
		t.Fatalf("excerpt %+v", e)
	}

	fwd := fixture(t, "outlook-header-forward.txt")
	if OwnText(Member{Text: fwd}) != "" || !forwardShaped(&Member{Text: fwd}, false) {
		t.Fatal("a text starting with a header block is not a forward")
	}
	if forwardShaped(&Member{Text: reply}, false) {
		t.Fatal("a reply above a header block is a forward")
	}
	if !forwardShaped(&Member{Text: "FYI\n\n" + strings.TrimLeft(fwd, "\n")}, true) {
		t.Fatal("a short note above a header block starting the thread is not a forward")
	}
	if forwardShaped(&Member{Text: fixture(t, "apple-forward.txt")}, false) {
		t.Fatal("apple forward: the known miss changed")
	}

	// A reply (not starting its thread, so not a short forward).
	replying := func(text string) Member {
		return with(sent("a", 2, text, "bob@example.com"), func(m *Member) { m.InReplyTo = "<z@mail.example>" })
	}
	runRules(t, []ruleCase{
		{"question only below an outlook header block", []Member{replying(reply)}, "", ""},
		{"question above an outlook header block", []Member{replying(strings.Replace(reply, "tomorrow.", "tomorrow, all right?", 1))},
			api.BoardThem, api.BoardReasonThemAsked},
	})
}
