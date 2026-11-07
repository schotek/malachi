// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel/prompttest"
)

// A suggested reply asks the daemon for the reply prompt of the case's
// account and target, its newest members and the user's instruction (the
// daemon appends the note of that account); the bridge is confined to
// the target, and only the daemon's tools are allowed.
func TestReplyAsksForTheCasesPrompt(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Start(boardCase("1"), "  Say yes  ")
	h.ended()
	last, ok := h.prompter.Last()
	if !ok || last.Purpose != api.PurposeReply || last.Language != testLanguage || last.AccountID != "" || last.Input == nil || last.Input.Reply == nil {
		t.Fatalf("prompter %+v", last)
	}
	rp := last.Input.Reply
	if rp.AccountID != "acc_1" || rp.MessageID != "m_1" || rp.Instruction != "  Say yes  " || rp.FollowUp {
		t.Errorf("reply input %+v", rp)
	}
	joined := strings.Join(fake.args(), "\n")
	if !strings.HasSuffix(joined, "--system-prompt\nSYS reply cs note:acc_1") ||
		!strings.Contains(joined, `"args":["--socket","/s.sock","--in-app","--reply-only","m_1"]`) ||
		!strings.Contains(joined, "--allowedTools\nmcp__malachi__read_message,mcp__malachi__list_messages,mcp__malachi__create_draft\n") {
		t.Errorf("args %q", fake.args())
	}
	if !slices.Equal(fake.prompts(), []string{prompttest.ReplyMessage(*rp)}) {
		t.Errorf("prompts %q", fake.prompts())
	}
}
