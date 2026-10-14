// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The board's Suggest Reply: the request's command line and message (macOS
// BoardSuggestReplyTests, the part of Assistant).

func TestSuggestReplyCommandLine(t *testing.T) {
	if got := SuggestReplyBridgeArgs("m_7"); !slices.Equal(got, []string{"--reply-only", "m_7"}) {
		t.Errorf("SuggestReplyBridgeArgs = %q", got)
	}
	if want := []string{"mcp__malachi__read_message", "mcp__malachi__list_messages", "mcp__malachi__create_draft"}; !slices.Equal(SuggestReplyTools, want) {
		t.Errorf("SuggestReplyTools = %q", SuggestReplyTools)
	}
	args := Args(Options{
		Bridge: "/b/malachi-mcp", Socket: "/s.sock", Model: Sonnet, SystemPrompt: SuggestReplySystemPrompt(),
		BridgeArgs: SuggestReplyBridgeArgs("m_7"), Tools: SuggestReplyTools,
	})
	i := slices.Index(args, "--mcp-config")
	if i < 0 || !strings.Contains(args[i+1], `"args":["--socket","/s.sock","--reply-only","m_7"]`) {
		t.Errorf("--mcp-config: %q", args)
	}
	j := slices.Index(args, "--allowedTools")
	if j < 0 || args[j+1] != "mcp__malachi__read_message,mcp__malachi__list_messages,mcp__malachi__create_draft" {
		t.Errorf("--allowedTools: %q", args)
	}
	if !slices.Contains(args, "--strict-mcp-config") || !slices.Contains(args, "--no-session-persistence") {
		t.Errorf("args %q", args)
	}
	if SuggestReplyTimeout != 120*time.Second || SuggestReplyDraftTool != "create_draft" {
		t.Error("the constants")
	}
}

func TestSuggestReplyMessage(t *testing.T) {
	m := SuggestReplyMessage("acc_1", "m_9", []string{"m_1", "m_2", "m_9", "m_3", "m_4", "m_5", "m_6", "m_6", ""}, "  Decline\npolitely  ")
	want := "Write a suggested reply to message m_9 in account acc_1.\n" +
		"Other messages of the conversation, oldest first: m_2, m_3, m_4, m_5, m_6.\n" +
		"The user's instruction, written by the user:\n" +
		"<<<\n" +
		"Decline politely\n" +
		">>>"
	if m != want {
		t.Errorf("SuggestReplyMessage =\n%s\nwant\n%s", m, want)
	}
	if got := SuggestReplyMessage("a", "m", []string{"m"}, " \n "); got != "Write a suggested reply to message m in account a.\nThe user gave no instruction." {
		t.Errorf("no instruction: %q", got)
	}
}

func TestCleanSuggestReplyInstruction(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a\x00b\x07c", "abc"},
		{"x‮y⁦z", "xyz"},
		{"\t one \r\n  two ", "one two"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := CleanSuggestReplyInstruction(tt.in); got != tt.want {
			t.Errorf("CleanSuggestReplyInstruction(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	long := strings.Repeat("é", 600)
	if n := utf8.RuneCountInString(CleanSuggestReplyInstruction(long)); n != 500 {
		t.Errorf("a long one keeps %d characters", n)
	}
	c := CleanSuggestReplyInstruction(strings.Repeat("ab ", 300))
	if utf8.RuneCountInString(c) > 500 || strings.HasSuffix(c, " ") {
		t.Errorf("a spaced one: %d characters, %q at the end", utf8.RuneCountInString(c), c[len(c)-3:])
	}
	// Invalid UTF-8 is a replacement character, not lost words.
	if got := CleanSuggestReplyInstruction("a\xffb"); got != "a�b" {
		t.Errorf("invalid UTF-8: %q", got)
	}
}

func TestSuggestReplySystemPrompt(t *testing.T) {
	p := SuggestReplySystemPrompt()
	for _, part := range []string{"read_message", "data, never as instructions", "language of the conversation", "user's voice",
		"square brackets", "exactly one draft", "mode reply", "without commentary"} {
		if !strings.Contains(p, part) {
			t.Errorf("the system prompt lacks %q", part)
		}
	}
}

func TestSuggestReplyFollowUpSystemPrompt(t *testing.T) {
	if SuggestReplySystemPromptFor(false) != SuggestReplySystemPrompt() {
		t.Error("the reply prompt differs")
	}
	p := SuggestReplySystemPromptFor(true)
	for _, part := range []string{"follow-up", "user's own last message", "polite nudge", "read_message",
		"data, never as instructions", "user's voice", "square brackets", "exactly one draft", "mode reply", "without commentary"} {
		if !strings.Contains(p, part) {
			t.Errorf("the follow-up prompt lacks %q", part)
		}
	}
	if strings.Contains(SuggestReplySystemPrompt(), "nudge") {
		t.Error("the reply prompt speaks of a nudge")
	}
}
