// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchSchema(t *testing.T) {
	const want = `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`
	if SearchSchema != want {
		t.Errorf("SearchSchema = %s, want %s", SearchSchema, want)
	}
	if !json.Valid([]byte(SearchSchema)) {
		t.Error("SearchSchema is not JSON")
	}
}

func TestSearchSystemPrompt(t *testing.T) {
	got := SearchSystemPrompt("2026-09-29")
	if !strings.HasSuffix(got, "Return only the query in the JSON field query. Today is 2026-09-29.") {
		t.Errorf("SearchSystemPrompt ends with %q", got[max(0, len(got)-120):])
	}
	if strings.Contains(got, "%!") || strings.Count(got, "2026-09-29") != 1 {
		t.Errorf("SearchSystemPrompt has a bad verb or the date twice: %q", got)
	}
	// Every operator of backend/internal/search/query.go and docs/api.md
	// search.query, and what does not exist.
	for _, op := range []string{
		`"exact phrase"`, "from:", "to:", "(To, Cc and Bcc)", "subject:", "has:attachment", "is:unread", "is:flagged",
		"after:YYYY-MM-DD", "(inclusive)", "before:YYYY-MM-DD", "(exclusive)",
		"in:inbox", "in:sent", "in:drafts", "in:trash", "in:junk", "in:archive", "in: with a folder name",
		"no OR", "no NOT", "no parentheses", "prefix", "diacritics",
		"the next word or quoted phrase", "first day of the next month",
		"in their language", "single spaces",
	} {
		if !strings.Contains(got, op) {
			t.Errorf("SearchSystemPrompt does not mention %q", op)
		}
	}
	// Three examples, in English and Czech, whose queries are the syntax.
	examples := 0
	for _, line := range strings.Split(got, "\n") {
		if words, query, ok := strings.Cut(line, " -> "); ok {
			examples++
			if words == "" || query == "" || strings.ContainsAny(query, "\t") || strings.Contains(query, "  ") {
				t.Errorf("an odd example: %q", line)
			}
		}
	}
	if examples != 3 || !strings.Contains(got, "faktury od Jany z března 2026 -> faktur from:jan after:2026-03-01 before:2026-04-01") {
		t.Errorf("SearchSystemPrompt has %d examples:\n%s", examples, got)
	}
	// Model-facing: English, whatever the UI language; the date is the
	// only thing filled in.
	if a, b := SearchSystemPrompt("A"), SearchSystemPrompt("B"); strings.TrimSuffix(a, "A.") != strings.TrimSuffix(b, "B.") {
		t.Error("SearchSystemPrompt depends on more than the date")
	}
}

func TestSearchMessage(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"invoices from Jana", "invoices from Jana"},
		{"  faktury od Jany\n", "faktury od Jany"},
		{"a\nb", "a\nb"},
		{"100% %s", "100% %s"},
		{strings.Repeat("ž", MaxSearchWords), strings.Repeat("ž", MaxSearchWords)},
		{" " + strings.Repeat("a", MaxSearchWords) + " ", strings.Repeat("a", MaxSearchWords)},
	}
	for _, tt := range tests {
		got, err := SearchMessage(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("SearchMessage(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	errs := []struct {
		name, in string
		want     error
	}{
		{"empty", "", errNoWords},
		{"only space", " \n\t\u00a0", errNoWords},
		{"too long", strings.Repeat("ž", MaxSearchWords+1), errWordsTooLong},
	}
	for _, tt := range errs {
		got, err := SearchMessage(tt.in)
		if !errors.Is(err, tt.want) || got != "" {
			t.Errorf("%s: SearchMessage = %q, %v; want \"\", %v", tt.name, got, err, tt.want)
		}
	}
}

func TestParseSearchQuery(t *testing.T) {
	long := strings.Repeat("a", 1023) + "č" // 1025 bytes: the č does not fit
	tests := []struct {
		name, in string
		want     string
		ok       bool
	}{
		{"a query", `{"query":"from:jana faktur is:unread"}`, "from:jana faktur is:unread", true},
		{"white space around", " \n{ \"query\" : \"x\" }\n", "x", true},
		{"other members ignored", `{"note":"hi","query":"x","n":1}`, "x", true},
		{"the last of duplicates", `{"query":"first","query":"second"}`, "second", true},
		{"escapes", `{"query":"subject:\"annual report\" \u010dern\u00fd"}`, `subject:"annual report" černý`, true},
		{"trimmed", `{"query":"  x  "}`, "x", true},
		{"one line", `{"query":"from:jana\nfaktur\r\nis:unread"}`, "from:jana faktur is:unread", true},
		{"runs of space", "{\"query\":\"a \\t  b\\u2028c\\u00a0d\\u0085e\"}", "a b c d e", true},
		{"control characters", `{"query":"a\u0000b\u001bc\u007f"}`, "a b c", true},
		{"a bad surrogate", `{"query":"a\ud800b"}`, "a" + string(utf8.RuneError) + "b", true},
		{"invalid UTF-8", "{\"query\":\"a\xffb\"}", "a" + string(utf8.RuneError) + "b", true},
		{"no markup interpreted", `{"query":"<b>x</b> &amp;"}`, "<b>x</b> &amp;", true},
		{"cut at a character", `{"query":"` + long + `"}`, strings.Repeat("a", 1023), true},
		{"exactly the cap", `{"query":"` + strings.Repeat("b", 1024) + `"}`, strings.Repeat("b", 1024), true},
		{"no space left at the cut", `{"query":"` + strings.Repeat("c", 1023) + ` dd"}`, strings.Repeat("c", 1023), true},
		{"empty", `{"query":""}`, "", false},
		{"only space", `{"query":" \n\t "}`, "", false},
		{"only controls", `{"query":"\u0000\u0007"}`, "", false},
		{"missing", `{"q":"x"}`, "", false},
		{"null", `{"query":null}`, "", false},
		{"a number", `{"query":42}`, "", false},
		{"a boolean", `{"query":true}`, "", false},
		{"an array", `{"query":["x"]}`, "", false},
		{"an object", `{"query":{"text":"x"}}`, "", false},
		{"not an object", `["x"]`, "", false},
		{"a bare string", `"from:jana"`, "", false},
		{"null answer", `null`, "", false},
		{"nothing", ``, "", false},
		{"invalid JSON", `{"query":"x"`, "", false},
		{"trailing garbage", `{"query":"x"} y`, "", false},
		{"two objects", `{"query":"x"}{"query":"y"}`, "", false},
		{"a case-folded key is not the key", `{"Query":"x"}`, "", false},
	}
	for _, tt := range tests {
		got, ok := ParseSearchQuery([]byte(tt.in))
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: ParseSearchQuery(%q) = %q, %v; want %q, %v", tt.name, tt.in, got, ok, tt.want, tt.ok)
		}
		if ok && (len(got) > MaxSearchQueryBytes || !utf8.ValidString(got) || strings.ContainsAny(got, "\n\r\t") || strings.TrimSpace(got) != got) {
			t.Errorf("%s: ParseSearchQuery = %q is not one clean line", tt.name, got)
		}
	}
	if got, ok := ParseSearchQuery(nil); ok || got != "" {
		t.Errorf("ParseSearchQuery(nil) = %q, %v", got, ok)
	}
}
