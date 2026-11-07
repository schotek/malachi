// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The board's Suggest Reply (docs/mcp.md "A suggested reply on the board",
// docs/security.md §10.2): the pure half of the one-shot request the board
// sends the assistant for one case, on the user's click. The prompt, the
// tools and the bridge's arguments ("--reply-only <replyMessageId>", which
// confines create_draft to one reply to that message) are the daemon's
// (assistant.prompt, purpose reply, api.ReplyInput); what stays here is
// what the client reads back and its limits. The macOS client leads
// (MalachiCore Assistant/AssistantSuggestReply.swift); this is its port.
//
// This file holds no translatable text.

import (
	"strings"
	"time"
	"unicode"
)

// SuggestReplyDraftTool is the tool whose result names the draft
// (ParseDraftResult), without the bridge's prefix, as Event.Tool names it.
const SuggestReplyDraftTool = "create_draft"

// SuggestReplyTimeout is how long a suggested reply may take before it
// ends as a timeout.
const SuggestReplyTimeout = 120 * time.Second

// SuggestReplyMaxInstruction is the longest instruction the daemon's
// message carries, in characters (runes); CleanSuggestReplyInstruction
// cuts to it.
const SuggestReplyMaxInstruction = 500

// CleanSuggestReplyInstruction is the instruction field's text as the
// daemon puts it into the message: every run of white space one space,
// control and bidirectional formatting characters left out, trimmed, cut
// to SuggestReplyMaxInstruction characters (invalid UTF-8 counts as
// U+FFFD). The daemon cleans the instruction itself; a client uses this
// to show what will be sent.
func CleanSuggestReplyInstruction(text string) string {
	var b strings.Builder
	count := 0
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || bidiControl(r) {
			continue
		}
		if space {
			if count >= SuggestReplyMaxInstruction-1 {
				break
			}
			b.WriteByte(' ')
			count++
			space = false
		}
		if count >= SuggestReplyMaxInstruction {
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}
