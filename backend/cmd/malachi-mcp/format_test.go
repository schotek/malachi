// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
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

func TestParseAddresses(t *testing.T) {
	as, err := parseAddresses([]string{"Alice <alice@example.org>", " bob@example.com ", ""})
	if err != nil || len(as) != 2 || as[0].Name != "Alice" || as[1].Address != "bob@example.com" || as[1].Name != "" {
		t.Errorf("parseAddresses = %+v, %v", as, err)
	}
	if _, err := parseAddresses([]string{"no at sign"}); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Errorf("parseAddresses should fail: %v", err)
	}
}

func TestParseComposeMode(t *testing.T) {
	cases := map[string]api.ComposeMode{"": api.ComposeNew, "new": api.ComposeNew, "reply": api.ComposeReply,
		"REPLY": api.ComposeReply, "replyall": api.ComposeReplyAll, "Forward": api.ComposeForward}
	for in, want := range cases {
		if got, ok := parseComposeMode(in); !ok || got != want {
			t.Errorf("parseComposeMode(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	if _, ok := parseComposeMode("bogus"); ok {
		t.Error("bogus mode accepted")
	}
}

func TestBodyHTMLInsert(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"  \n":                          "",
		"Thanks <b>!</b>\nBye":          "<p>Thanks &lt;b&gt;!&lt;/b&gt;<br/>Bye</p>",
		"one\r\ntwo\r\n\r\nthree":       "<p>one<br/>two</p><p>three</p>",
		"\n\npara\n\n\n\nnext & last\n": "<p>para</p><p>next &amp; last</p>",
	}
	for in, want := range cases {
		if got := bodyHTML(in); got != want {
			t.Errorf("bodyHTML(%q) = %q, want %q", in, got, want)
		}
	}
	tpl := `<p><br/></p><div>attr</div><blockquote type="cite">q</blockquote>`
	if got := insertBody(tpl, "<p>x</p>"); got != `<p>x</p><div>attr</div><blockquote type="cite">q</blockquote>` {
		t.Errorf("insertBody = %q", got)
	}
	if got := insertBody("<p><br></p><div>a</div>", "<p>x</p>"); got != "<p>x</p><div>a</div>" {
		t.Errorf("insertBody raw spelling = %q", got)
	}
	if got := insertBody("<div>no marker</div>", "<p>x</p>"); got != "<p>x</p><div>no marker</div>" {
		t.Errorf("insertBody without marker = %q", got)
	}
	if got := insertBody(tpl, ""); got != tpl {
		t.Errorf("insertBody empty body = %q", got)
	}
	if got := plainBody("Hi\n", "\n\nAlice wrote:\n> x"); got != "Hi\n\nAlice wrote:\n> x" {
		t.Errorf("plainBody = %q", got)
	}
	if got := plainBody("", "\n\n> x"); got != "> x" {
		t.Errorf("plainBody empty body = %q", got)
	}
	if got := plainBody("Hi", ""); got != "Hi" {
		t.Errorf("plainBody empty quote = %q", got)
	}
}

func TestDefaultAttributionCaps(t *testing.T) {
	date := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	m := api.Message{MessageSummary: api.MessageSummary{
		From:    []api.Address{{Name: "Ali" + string(rune(0x202E)) + "ce", Address: "alice@example.org"}},
		Subject: "Hello\r\nthere",
		Date:    date,
	}}
	if got := defaultAttribution(api.ComposeReply, m); got != "On Wed, 23 Sep 2026 10:00 UTC, Alice <alice@example.org> wrote:" {
		t.Errorf("reply attribution = %q", got)
	}
	m.Date = time.Time{}
	if got := defaultAttribution(api.ComposeReply, m); got != "Alice <alice@example.org> wrote:" {
		t.Errorf("reply attribution without date = %q", got)
	}
	m.From = nil
	if got := defaultAttribution(api.ComposeReply, m); got != "The sender wrote:" {
		t.Errorf("reply attribution without sender = %q", got)
	}

	m.Date = date
	m.From = []api.Address{{Name: "Alice", Address: "alice@example.org"}}
	for i := 0; i < 600; i++ {
		m.To = append(m.To, api.Address{Name: fmt.Sprintf("Person %d", i), Address: fmt.Sprintf("p%d@example.org", i)})
	}
	got := defaultAttribution(api.ComposeForward, m)
	if len(got) > api.MaxDraftAttributionBytes || !strings.HasSuffix(got, "…") || strings.Contains(got, "\r") {
		t.Errorf("forward attribution not capped: %d bytes, suffix %q", len(got), got[len(got)-3:])
	}
	if n := strings.Count(got, "\n") + 1; n > api.MaxDraftAttributionLines || n != 5 {
		t.Errorf("forward attribution has %d lines", n)
	}
	mustContain(t, got, "---------- Forwarded message ----------\nFrom: Alice <alice@example.org>\nDate: Wed, 23 Sep 2026 10:00 UTC\nSubject: Hello there\nTo: Person 0 <p0@example.org>")
	if got := capAttribution(strings.Repeat("x\n", 20)); strings.Count(got, "\n") != api.MaxDraftAttributionLines-1 {
		t.Errorf("capAttribution lines: %q", got)
	}
}

func TestBlockedSummary(t *testing.T) {
	if got := blockedSummary(api.BlockedContent{}); got != "" {
		t.Errorf("empty summary = %q", got)
	}
	if got := blockedSummary(api.BlockedContent{RemoteImages: 3, Scripts: 1}); got != "remoteImages=3 scripts=1" {
		t.Errorf("summary = %q", got)
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
