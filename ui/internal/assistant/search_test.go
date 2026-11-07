// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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
