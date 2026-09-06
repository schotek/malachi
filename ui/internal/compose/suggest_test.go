// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"testing"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestTokenAt(t *testing.T) {
	cases := []struct {
		text  string
		caret int // characters
		token string
	}{
		{"", 0, ""},
		{"al", 2, "al"},
		{"al", 1, "al"},
		{"al", 0, "al"},
		{"bob@example.org, al", 19, "al"},
		{"bob@example.org, al", 17, "al"},
		{"bob@example.org, al", 16, "al"}, // in the space before the token: still that token
		{"bob@example.org, al", 15, "bob@example.org"},
		{"bob@example.org, al", 3, "bob@example.org"},
		{"al, bob@example.org", 2, "al"},
		{"al, bob@example.org", 5, "bob@example.org"},
		{"a;b", 2, "b"},
		{`"Nov, Jan" <jan@example.cz>, al`, 6, `"Nov, Jan" <jan@example.cz>`}, // comma in quotes
		{`Nov <a,b@example.cz>, al`, 7, `Nov <a,b@example.cz>`},               // comma in brackets
		{"Jan Novák, no", 11, "no"},                                           // multi-byte before the caret
		{"Jan Novák", 9, "Jan Novák"},
		{"al", 99, "al"}, // caret past the end
		{"  al  ", 3, "al"},
	}
	for _, c := range cases {
		start, end, token := tokenAt(c.text, c.caret)
		if token != c.token || c.text[start:end] != token {
			t.Errorf("tokenAt(%q, %d) = [%d,%d) %q, want %q", c.text, c.caret, start, end, token, c.token)
		}
	}
}

func TestReplaceToken(t *testing.T) {
	alice := api.Address{Name: "Alice Example", Address: "alice@example.org"}
	quoted := api.Address{Name: "Novák, Jan", Address: "jan@example.cz"}
	bare := api.Address{Address: "bob@example.org"}
	cases := []struct {
		text  string
		caret int
		addr  api.Address
		want  string
		after string // text before the caret, to check its position
	}{
		{"al", 2, alice, "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, "},
		{"bob@example.org, al", 19, alice, "bob@example.org, Alice Example <alice@example.org>, ", "bob@example.org, Alice Example <alice@example.org>, "},
		{"al, bob@example.org", 2, alice, "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>"},
		{"al , bob@example.org", 2, alice, "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>"},
		// Without a separator the whole segment is the token, as the
		// parser would read it.
		{"al bob@example.org", 2, alice, "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, "},
		{"no", 2, quoted, `"Novák, Jan" <jan@example.cz>, `, `"Novák, Jan" <jan@example.cz>, `},
		{"bo", 2, bare, "bob@example.org, ", "bob@example.org, "},
		{"Novák, bo", 9, bare, "Novák, bob@example.org, ", "Novák, bob@example.org, "},
	}
	for _, c := range cases {
		start, end, _ := tokenAt(c.text, c.caret)
		got, caret := replaceToken(c.text, start, end, c.addr)
		if got != c.want {
			t.Errorf("replaceToken(%q) = %q, want %q", c.text, got, c.want)
			continue
		}
		if caret != utf8.RuneCountInString(c.after) || byteOffset(got, caret) != len(c.after) {
			t.Errorf("replaceToken(%q): caret %d, want after %q", c.text, caret, c.after)
		}
	}
}

func TestByteOffset(t *testing.T) {
	text := "Novák, x"
	for chars, want := range map[int]int{0: 0, 3: 3, 4: 5, 5: 6, 8: 9, 20: 9, -1: 0} {
		if got := byteOffset(text, chars); got != want {
			t.Errorf("byteOffset(%d) = %d, want %d", chars, got, want)
		}
	}
}

func TestSuggestionIconAndTooltip(t *testing.T) {
	book := api.Contact{Source: api.ContactSourceAddressBook, Book: "Contacts"}
	unnamed := api.Contact{Source: api.ContactSourceAddressBook}
	sent := api.Contact{Source: api.ContactSourceSent}
	if suggestionIcon(book) == suggestionIcon(sent) {
		t.Error("sources share an icon")
	}
	if suggestionTooltip(book) != "Contacts" || suggestionTooltip(unnamed) == "" || suggestionTooltip(sent) == "" || suggestionTooltip(unnamed) == suggestionTooltip(sent) {
		t.Errorf("tooltips: %q %q %q", suggestionTooltip(book), suggestionTooltip(unnamed), suggestionTooltip(sent))
	}
}

func TestSplitAddressRanges(t *testing.T) {
	text := `a@x, "b,c" <b@x>;d@x`
	var got []string
	for _, r := range splitAddressRanges(text) {
		got = append(got, text[r.start:r.end])
	}
	want := []string{"a@x", ` "b,c" <b@x>`, "d@x"}
	if len(got) != len(want) {
		t.Fatalf("ranges = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range %d = %q, want %q", i, got[i], want[i])
		}
	}
	if r := splitAddressRanges(""); len(r) != 1 || r[0] != (span{}) {
		t.Errorf("empty text: %+v", r)
	}
}
