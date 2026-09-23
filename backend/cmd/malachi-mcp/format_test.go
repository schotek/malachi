// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanStripsFormatChars(t *testing.T) {
	// Built from rune values so that no invisible character sits in the
	// source: right-to-left override, a Tags-block character, the BOM, a
	// soft hyphen, ZWJ (kept), NUL, CRLF, tab, ESC.
	in := "a" + string(rune(0x202E)) + "b" + string(rune(0xE0041)) + "c" + string(rune(0xFEFF)) +
		"d" + string(rune(0xAD)) + "e" + string(rune(0x200D)) + "f" + string(rune(0)) + "g\r\nh\ti" + string(rune(0x1B))
	got := clean(in)
	want := "abcde" + string(rune(0x200D)) + "fg\nh\ti"
	if got != want {
		t.Errorf("clean(%q) = %q, want %q", in, got, want)
	}
	if got := clean(string([]byte{'c', 'a', 'f', 0xE9})); got != "caf"+string(utf8.RuneError) {
		t.Errorf("invalid UTF-8: %q", got)
	}
	if got := oneLine("  Subject\r\n  line two\t\tend  "); got != "Subject line two end" {
		t.Errorf("oneLine: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	s := "ěšč" + "řžý" + "áíé" // 9 runes, 18 bytes
	cases := []struct {
		offset, max int
		want        string
		end         int
	}{
		{0, 3, "ěšč", 3},
		{3, 3, "řžý", 6},
		{6, 100, "áíé", 9},
		{9, 3, "", 9},
		{50, 3, "", 9},
		{-5, 2, "ěš", 2},
		{0, 0, s, 9},
	}
	for _, c := range cases {
		got, total, end := truncateRunes(s, c.offset, c.max)
		if got != c.want || total != 9 || end != c.end {
			t.Errorf("truncateRunes(%d,%d) = %q,%d,%d; want %q,9,%d", c.offset, c.max, got, total, end, c.want, c.end)
		}
	}
}

func TestSliceBytes(t *testing.T) {
	s := "ěšč"                             // 6 bytes
	got, total, end := sliceBytes(s, 1, 3) // starts mid-rune: skip to š, cut before č
	if got != "š" || total != 6 || end != 4 {
		t.Errorf("sliceBytes mid-rune = %q,%d,%d", got, total, end)
	}
	got, _, end = sliceBytes(s, 0, 100)
	if got != s || end != 6 {
		t.Errorf("sliceBytes whole = %q,%d", got, end)
	}
	if !utf8.ValidString(got) {
		t.Error("slice not valid UTF-8")
	}
	if got := truncateBytes("ěšč", 3); got != "ě" {
		t.Errorf("truncateBytes = %q", got)
	}
}

func TestReplySubjectAndAddresses(t *testing.T) {
	if got := replySubject("Hello"); got != "Re: Hello" {
		t.Errorf("replySubject = %q", got)
	}
	if got := replySubject("  RE: Hello "); got != "RE: Hello" {
		t.Errorf("replySubject keeps prefix: %q", got)
	}
	as, err := parseAddresses([]string{"Alice <alice@example.org>", " bob@example.com ", ""})
	if err != nil || len(as) != 2 || as[0].Name != "Alice" || as[1].Address != "bob@example.com" || as[1].Name != "" {
		t.Errorf("parseAddresses = %+v, %v", as, err)
	}
	if _, err := parseAddresses([]string{"no at sign"}); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Errorf("parseAddresses should fail: %v", err)
	}
}

func TestNonceIsRandomHex(t *testing.T) {
	a, b := newNonce(), newNonce()
	if len(a) != 12 || a == b {
		t.Errorf("nonces %q %q", a, b)
	}
	if strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("nonce not hex: %q", a)
	}
}
