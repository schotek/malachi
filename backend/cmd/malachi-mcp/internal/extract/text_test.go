// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextBuilderNormalises(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want string
	}{
		{"one line", []string{"hello"}, "hello"},
		{"final newline ends the line", []string{"a\n", "b\n"}, "a\nb"},
		{"CRLF and CR", []string{"a\r\nb\rc\r\n\r\nd"}, "a\nb\nc\n\nd"},
		{"CR split across calls", []string{"a\r", "\nb"}, "a\n\nb"},
		{"trailing white space", []string{"a  \t", "b\u00a0", "  c"}, "a\nb\n  c"},
		{"blank runs collapse", []string{"a\n\n\n\nb", "", "", "c"}, "a\n\nb\n\nc"},
		{"white-space lines are blank", []string{"a", " \t ", "b"}, "a\n\nb"},
		{"blank lines at the start", []string{"", "\n\n", "  ", "a"}, "a"},
		{"blank lines at the end", []string{"a", "", "\n\n"}, "a"},
		{"invalid UTF-8", []string{"a\xffb"}, "a" + string(utf8.RuneError) + "b"},
		{"empty", nil, ""},
	} {
		tb := newTextBuilder(1 << 10)
		for _, s := range c.in {
			if !tb.Lines(s) {
				t.Errorf("%s: cut", c.name)
			}
		}
		if got := tb.String(); got != c.want || tb.Cut() || tb.Len() != len(c.want) {
			t.Errorf("%s: %q (cut %v), want %q", c.name, got, tb.Cut(), c.want)
		}
	}
}

func TestTextBuilderBlank(t *testing.T) {
	tb := newTextBuilder(1 << 10)
	tb.Blank()
	tb.Lines("table")
	tb.Blank()
	tb.Blank()
	tb.Lines("")
	tb.Lines("after")
	tb.Blank()
	if got := tb.String(); got != "table\n\nafter" {
		t.Errorf("%q", got)
	}
}

func TestTextBuilderCutsAtLineBoundary(t *testing.T) {
	tb := newTextBuilder(10)
	if !tb.Lines("1234\n5678") { // 9 bytes
		t.Fatal("cut early")
	}
	if tb.Lines("ab") { // 9 + 1 + 2 > 10
		t.Fatal("not cut")
	}
	if tb.Lines("x") {
		t.Error("written after the cut")
	}
	if got := tb.String(); got != "1234\n5678" || !tb.Cut() {
		t.Errorf("%q, cut %v", got, tb.Cut())
	}

	// A line that fits exactly is no cut; the blank line before one
	// counts against the cap.
	tb = newTextBuilder(10)
	tb.Lines("12345\n1234")
	if tb.Cut() || tb.String() != "12345\n1234" {
		t.Errorf("exact fit: %q, cut %v", tb.String(), tb.Cut())
	}
	tb = newTextBuilder(10)
	tb.Lines("12345\n\n1234")
	if !tb.Cut() || tb.String() != "12345" {
		t.Errorf("blank line over the cap: %q, cut %v", tb.String(), tb.Cut())
	}

	// A cut in the middle of a multi-line write keeps the lines before it.
	tb = newTextBuilder(10)
	if tb.Lines("ab\ncd\nefghijk\nl") || tb.String() != "ab\ncd" {
		t.Errorf("mid-write: %q", tb.String())
	}
}

func TestTextBuilderLineLongerThanCap(t *testing.T) {
	// A line that could never fit fills the room left, cut at a
	// character boundary: here inside the 2-byte ü.
	long := strings.Repeat("\u00fc", 10) // 20 bytes
	tb := newTextBuilder(12)
	tb.Lines("ab")
	if tb.Lines(long) {
		t.Fatal("not cut")
	}
	got := tb.String()
	if got != "ab\n"+strings.Repeat("\u00fc", 4) || !utf8.ValidString(got) || len(got) > 12 {
		t.Errorf("%q", got)
	}

	// Its trailing white space goes too.
	tb = newTextBuilder(6)
	tb.Lines("abc    defghijklmnop")
	if got := tb.String(); got != "abc" || !tb.Cut() {
		t.Errorf("%q", got)
	}

	// A line that fits the cap but not the room left is not split.
	tb = newTextBuilder(12)
	tb.Lines("abcdef")
	tb.Lines("ghijklmnop")
	if got := tb.String(); got != "abcdef" || !tb.Cut() {
		t.Errorf("%q", got)
	}
}

func TestRunePrefix(t *testing.T) {
	s := "a\u00fcb\u4e2d" // 1 + 2 + 1 + 3 bytes
	for n, want := range map[int]string{
		-1: "", 0: "", 1: "a", 2: "a", 3: "a\u00fc", 4: "a\u00fcb", 6: "a\u00fcb", 7: s, 100: s,
	} {
		if got := runePrefix(s, n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}

func TestCountChars(t *testing.T) {
	fffd := string(utf8.RuneError)
	pua := string(rune(0xE000)) + string(rune(0xF8FF))
	plane15 := string(rune(0xF0000)) + string(rune(0x10FFFD))
	for _, c := range []struct {
		name          string
		s             string
		bad, nonSpace int
	}{
		{"plain", "Hello, world", 0, 11},
		{"white space", " \t\n\u00a0\u3000x", 0, 1},
		{"replacement", "ab" + fffd, 1, 3},
		{"invalid UTF-8", "ab\xff\xfe", 2, 4},
		{"private use", "a" + pua + plane15, 4, 5},
		{"just outside private use", string(rune(0xDFFF-0x1000)) + string(rune(0xF900)) + string(rune(0xEFFFF)), 0, 3},
		{"controls", "a\x00\x07\x7f\u0085\r\v\f", 7, 8},
		{"tab and newline are space", "\t\n", 0, 0},
	} {
		bad, nonSpace := countChars(c.s)
		if bad != c.bad || nonSpace != c.nonSpace {
			t.Errorf("%s: bad %d of %d, want %d of %d", c.name, bad, nonSpace, c.bad, c.nonSpace)
		}
	}
}

func TestGarbled(t *testing.T) {
	for _, c := range []struct {
		bad, nonSpace int
		want          bool
	}{
		{0, 100, false},
		{10, 100, false}, // exactly 10 % is not over
		{11, 100, true},
		{15, 15, false}, // too short to judge
		{2, 16, true},   // 12.5 %
		{1, 16, false},
		{0, 0, false},
	} {
		if got := garbled(c.bad, c.nonSpace, 10); got != c.want {
			t.Errorf("%d of %d: %v", c.bad, c.nonSpace, got)
		}
	}
}
