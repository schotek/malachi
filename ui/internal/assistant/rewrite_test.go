// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRewrites(t *testing.T) {
	if want := []Rewrite{Politer, Shorter, Fix, ToEnglish}; !slices.Equal(Rewrites, want) {
		t.Errorf("Rewrites = %q, want %q", Rewrites, want)
	}
	nicks := map[Rewrite]string{Politer: "politer", Shorter: "shorter", Fix: "fix", ToEnglish: "english", Custom: "custom"}
	for r, nick := range nicks {
		if string(r) != nick {
			t.Errorf("Rewrite %q, want %q", r, nick)
		}
	}
	if slices.Contains(Rewrites, Custom) {
		t.Error("Custom is the popover's field, not a preset")
	}
}

func TestCleanRewrite(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Dear Jana, thank you.", "Dear Jana, thank you."},
		{"trimmed", "\n\n  Dear Jana,\n\nthanks.  \n", "Dear Jana,\n\nthanks."},
		{"paragraphs and tabs kept", "a\n\n\tb\nc", "a\n\n\tb\nc"},
		{"CRLF", "a\r\nb\r\n\r\nc", "a\nb\n\nc"},
		{"a lone CR is a control character", "a\rb", "ab"},
		{"control characters", "a\x00b\x07c\x1b[31md\x7fe\u0085f", "abc[31mdef"},
		{"invalid UTF-8", "a\xffb", "a" + string(utf8.RuneError) + "b"},
		{"a fence", "```\nHello there.\n```", "Hello there."},
		{"a fence with a tag", "```text\nHello\n\nthere.\n```", "Hello\n\nthere."},
		{"a fence with a tag and trailing space", "```markdown  \nHello\n```", "Hello"},
		{"a fence with odd tag characters", "```c++\nx\n```", "x"},
		{"a fence on one line", "```Hello there.```", "Hello there."},
		{"a fence whose first line is text", "```Hello there,\nfriend.\n```", "Hello there,\nfriend."},
		{"a fence with space around it", "  \n```\nHi\n```\n\n", "Hi"},
		{"only one fence", "```\nHi", "```\nHi"},
		{"a fence inside is kept", "Use this:\n```\ncode\n```", "Use this:\n```\ncode\n```"},
		{"two fenced blocks stay", "```\na\n```\n\n```\nb\n```", "```\na\n```\n\n```\nb\n```"},
		{"too short for two fences", "`````", "`````"},
		{"only fences", "``````", ""},
		{"markers", "<<<\nHello there.\n>>>", "Hello there."},
		{"markers on one line", "<<<Hello>>>", "Hello"},
		{"only the opening marker", "<<< Hello", "Hello"},
		{"only the closing marker", "Hello\n>>>", "Hello"},
		{"markers inside stay", "a <<< b >>> c", "a <<< b >>> c"},
		{"markers in a fence", "```\n<<<\nHello\n>>>\n```", "Hello"},
		{"straight quotes", `"Hello there."`, "Hello there."},
		{"English quotes", "“Hello there.”", "Hello there."},
		{"Czech quotes", "„Dobrý den.“", "Dobrý den."},
		{"German closing", "„Guten Tag.”", "Guten Tag."},
		{"Swedish quotes", "”Hej.”", "Hej."},
		{"guillemets", "«Bonjour.»", "Bonjour."},
		{"reversed guillemets", "»Hallo.«", "Hallo."},
		{"single quotes", "'Hello.'", "Hello."},
		{"typographic single quotes", "‘Hello.’", "Hello."},
		{"low single quotes", "‚Ahoj.‘", "Ahoj."},
		{"quotes with space inside", "\"  Hello.  \"", "Hello."},
		{"a quote inside keeps them", `"Hello," she said. "Bye."`, `"Hello," she said. "Bye."`},
		{"an apostrophe inside keeps single quotes", "'I don't know.'", "'I don't know.'"},
		{"Czech quotes inside keep them", "„Ano,“ řekla. „Ne.“", "„Ano,“ řekla. „Ne.“"},
		{"only one pair", `""Hello""`, `""Hello""`},
		{"unpaired", "\"Hello", "\"Hello"},
		{"mismatched", "“Hello\"", "“Hello\""},
		{"one quotation mark", `"`, `"`},
		{"empty quotes", `""`, ""},
		{"quotes in a fence", "```\n\"Hello.\"\n```", "Hello."},
		{"quotes around markers", "\"<<<Hello>>>\"", "Hello"},
		{"markers around quotes", "<<<\n“Hello.”\n>>>", "Hello."},
		{"empty", "", ""},
		{"only space", " \n\t\u00a0", ""},
		{"no markup interpreted", "<b>Hi</b> &amp; *bye*", "<b>Hi</b> &amp; *bye*"},
		{"bidi characters are text", "a\u202eb", "a\u202eb"},
	}
	for _, tt := range tests {
		if got := CleanRewrite(tt.in); got != tt.want {
			t.Errorf("%s: CleanRewrite(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
	// Whatever comes in, what goes out is valid UTF-8, trimmed, and holds
	// no control character but newlines and tabs.
	for _, tt := range tests {
		got := CleanRewrite(tt.in)
		if !utf8.ValidString(got) || strings.TrimSpace(got) != got {
			t.Errorf("%s: CleanRewrite(%q) = %q is not clean", tt.name, tt.in, got)
		}
		for _, r := range got {
			if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
				t.Errorf("%s: CleanRewrite(%q) keeps %U", tt.name, tt.in, r)
			}
		}
	}
}
