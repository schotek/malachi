// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestCleanLine(t *testing.T) {
	cases := []struct {
		name, in, want string
		max            int
		fits           bool
	}{
		{"plain", "  Pay the   invoice\n\tby Friday ", "Pay the invoice by Friday", 0, true},
		{"controls and bidi", "a\x00b\x07c \u202ed\u202c \u2066e\u2069 f\u200bg\ufeffh", "abc d e fgh", 0, true},
		{"zwj and zwnj stay", "\U0001F469\u200d\U0001F4BB \u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645", "\U0001F469\u200d\U0001F4BB \u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645", 0, true},
		{"tags block", "ok\U000E0041\U000E0042", "ok", 0, true},
		{"invalid utf-8", "a\xffb", "a\ufffdb", 0, true},
		{"urls", "see https://evil.example/x?y=1 or (www.evil.example) and HTTP://X.example, mail me@x.example", "see or and mail me@x.example", 0, true},
		{"url glued to text", "click:https://evil.example now", "click: now", 0, true},
		{"punctuation before a url", "see (https://evil.example) now", "see now", 0, true},
		{"line breaks of every kind", "a\r\nb\rc\u2028d\u2029e\u0085f", "a b c d e f", 0, true},
		{"cut at a rune boundary", "\u010d\u010d\u010d", "\u010d", 3, false},
		{"cut drops trailing space", "ab cd", "ab", 3, false},
		{"exactly the limit", "abc", "abc", 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, fits := CleanLine(c.in, c.max)
			if got != c.want || fits != c.fits {
				t.Fatalf("got %q %v, want %q %v", got, fits, c.want, c.fits)
			}
		})
	}
}

func TestCleanBlock(t *testing.T) {
	got, fits := CleanBlock("\n\n  First   line \r\n\r\n\r\n\r\nSecond\u202e line https://x.example\n\n", api.MaxBoardSummaryBytes)
	if got != "First line\n\nSecond line" || !fits {
		t.Fatalf("got %q", got)
	}
	got, fits = CleanBlock(strings.Repeat("word ", 1000), api.MaxBoardSummaryBytes)
	if len(got) > api.MaxBoardSummaryBytes || fits || !utf8.ValidString(got) {
		t.Fatalf("cap: %d %v", len(got), fits)
	}
}

func TestCleanHuge(t *testing.T) {
	ws := strings.Repeat(" \t\r\n\u00a0\u3000", 1<<20/10)
	start := time.Now()
	for _, in := range []string{ws, ws + "text", strings.Repeat("\x00", 1<<21), strings.Repeat("https://x.example/ ", 1<<16)} {
		if got, _ := CleanLine(in, api.MaxBoardTitleBytes); len(got) > api.MaxBoardTitleBytes {
			t.Fatalf("line %d", len(got))
		}
		if got, _ := CleanBlock(in, api.MaxBoardSummaryBytes); len(got) > api.MaxBoardSummaryBytes {
			t.Fatalf("block %d", len(got))
		}
		if got := CleanText(in); len(got) > 4*maxInputBytes {
			t.Fatalf("text %d", len(got))
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("slow: %v", d)
	}
}

func TestCleanTasks(t *testing.T) {
	got, fits := CleanTasks(nil)
	if got == nil || len(got) != 0 || !fits {
		t.Fatal("nil tasks")
	}
	got, fits = CleanTasks([]string{" Call Bob ", "", "https://x.example", "Pay\ninvoice"})
	if strings.Join(got, "|") != "Call Bob|Pay invoice" || !fits {
		t.Fatalf("got %q", got)
	}
	many := make([]string, 12)
	for i := range many {
		many[i] = "task"
	}
	if got, fits = CleanTasks(many); len(got) != api.MaxBoardTasks || fits {
		t.Fatalf("too many: %d %v", len(got), fits)
	}
	if got, fits = CleanTasks([]string{strings.Repeat("x", 400)}); len(got[0]) != api.MaxBoardTaskBytes || fits {
		t.Fatal("too long")
	}
}

func TestQuote(t *testing.T) {
	text := "Hi Jan,\r\n\r\nplease  send the signed\ncontract by Friday, 3 October \u2014 it\u2019s urgent.\nThe cafe\u0301 is booked.\n"
	cases := []struct {
		name, quote string
		want        bool
	}{
		{"exact", "send the signed contract by Friday", true},
		{"whitespace runs and line breaks", "send   the signed\n\ncontract   by Friday", true},
		{"straight apostrophe for curly", "3 October \u2014 it's urgent.", true},
		{"curly apostrophe for curly", "October \u2014 it\u2019s urgent", true},
		{"nfc quote against nfd text", "The caf\u00e9 is booked.", true},
		{"nfd quote against nfd text", "The cafe\u0301 is booked.", true},
		{"bidi inserted into the quote", "send the \u202esigned contract", true},
		{"case differs", "Send The Signed Contract", false},
		{"not there", "send the unsigned contract", false},
		{"too short", "Friday", false},
		{"short after cleaning", "\u200b\u200b\u200b\u200b\u200b\u200bFriday", false},
		{"ten bytes but spaces", "a b c d e f", false},
		{"too long", strings.Repeat("send the signed ", 30), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := QuoteIn(text, c.quote); got != c.want {
				t.Fatalf("got %v", got)
			}
		})
	}

	// A quote with a URL matches the text with the same URL: both lose it.
	if !QuoteIn("Upload it to https://files.example/x by Monday noon.", "Upload it to https://files.example/x by Monday noon.") {
		t.Fatal("url in both")
	}
	q, ok := CleanQuote("  it\u2019s   urgent, really \u202e ")
	if !ok || q != "it\u2019s urgent, really" {
		t.Fatalf("clean quote %q %v", q, ok)
	}
}

func TestQuoteInOwnText(t *testing.T) {
	reply := Member{Text: fixture(t, "reply-with-quote.txt")}
	// The user's own promise.
	if !QuoteInOwnText(reply, "I will send the figures by Monday") {
		t.Fatal("own promise")
	}
	// The other party's promise is in the quoted history: found in the
	// text, never as the user's commitment.
	other := "I will send the slides by Friday morning"
	if !QuoteIn(reply.Text, other) || QuoteInOwnText(reply, other) {
		t.Fatal("quoted promise")
	}
	// Spanning the trim boundary.
	if QuoteInOwnText(reply, "by Monday. On Tue, 30 Sep 2026") {
		t.Fatal("across the boundary")
	}
	// After the signature.
	own := Member{Text: fixture(t, "question-own.txt")}
	if !QuoteInOwnText(own, "I promise to pay the invoice by 15 October") || QuoteInOwnText(own, "Jan Novak Jan Novak") {
		t.Fatal("signature")
	}
}

func TestDueInRange(t *testing.T) {
	d := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		due  time.Time
		want bool
	}{
		{d, true}, {d.Add(-24 * time.Hour), true}, {d.Add(-25 * time.Hour), false},
		{d.Add(400 * 24 * time.Hour), true}, {d.Add(401 * 24 * time.Hour), false},
		{time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), false}, {time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		if got := DueInRange(c.due, d); got != c.want {
			t.Fatalf("%v: %v", c.due, got)
		}
	}
}

func TestExcerpt(t *testing.T) {
	e := BoardMessageExcerpt(fixture(t, "reply-with-quote.txt"))
	if e.Text != "Sure, I will send the figures by Monday." || !e.Trimmed {
		t.Fatalf("reply %+v", e)
	}
	e = BoardMessageExcerpt(fixture(t, "question-own.txt"))
	if strings.Contains(e.Text, "Jan Novak") || !e.Trimmed || !strings.HasPrefix(e.Text, "Hi Bob,\n\ncould you") {
		t.Fatalf("signature %+v", e)
	}
	e = BoardMessageExcerpt("Hello\u202e\x00 there\n\n\n\n\nbye  ")
	if e.Text != "Hello there\n\nbye" || e.Trimmed {
		t.Fatalf("clean %+v", e)
	}
	e = MessageExcerpt(strings.Repeat("\u010d", 10), 5)
	if e.Text != "\u010d\u010d" || !e.Trimmed {
		t.Fatalf("cut %q", e.Text)
	}
	e = BoardMessageExcerpt(strings.Repeat(" ", 1<<20) + strings.Repeat("\n", 1<<20) + "late text")
	if e.Text != "" || !e.Trimmed {
		t.Fatalf("huge whitespace %q", e.Text)
	}
	e = BoardMessageExcerpt("")
	if e.Text != "" || e.Trimmed {
		t.Fatal("empty")
	}
}

func TestQueueExcerpts(t *testing.T) {
	long := strings.Repeat("word ", 1000) // 5000 bytes
	texts := []string{"oldest", long, long, long, long, long, "newest"}
	got := QueueExcerpts(texts)
	total := 0
	for _, e := range got {
		if len(e.Text) > api.MaxBoardQueueMessageBytes {
			t.Fatalf("message %d", len(e.Text))
		}
		total += len(e.Text)
	}
	if total > api.MaxBoardQueueCaseBytes {
		t.Fatalf("total %d", total)
	}
	if got[6].Text != "newest" || got[6].Trimmed || !got[5].Trimmed || got[0].Text != "" || !got[0].Trimmed {
		t.Fatalf("budget %+v %+v %+v", got[6], got[5], got[0])
	}
	if len(QueueExcerpts(nil)) != 0 {
		t.Fatal("nil")
	}
}

func TestOwnTextAndQuestion(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"Lunch?", true},
		{"\uff1f", true},
		{"\u061f", true},
		{"see https://x.example/?q=1", false},
		{"see <HTTPS://x.example/?q=1>", false},
		{"see (www.x.example/?q=1)", false},
		{"mail a?b@example.org", false},
		{"Really?!", true},
		{"no question here", false},
		{"Lunch\n-- \nWhy?", false},
		{"Lunch\r\n-- \r\nWhy?", false},
		{"Lunch\n--\nWhy?", true}, // not the RFC 3676 separator
	} {
		if got := hasQuestion(OwnText(Member{Text: c.text})); got != c.want {
			t.Fatalf("%q: %v", c.text, got)
		}
	}
}

func TestForwardSubject(t *testing.T) {
	for s, want := range map[string]bool{
		"Fwd: x": true, "FW: x": true, "fw:x": true, "Re: FW: x": true, "Fwd[3]: x": true, "WG: x": true,
		"Re: x": false, "x": false, "": false, "Fwd x": false, "Fwd:": true, "AW: Re: x": false,
		strings.Repeat("Re: ", 20) + "Fwd: x": false, // beyond NormalizeSubject's 16 markers
		"\xff\xfeFwd: x":                      false,
	} {
		if got := forwardSubject(s); got != want {
			t.Fatalf("%q: %v", s, got)
		}
	}
}

func TestForwardShapes(t *testing.T) {
	for _, c := range []struct {
		name     string
		m        Member
		starting bool
		want     bool
	}{
		{"gmail separator", Member{Text: fixture(t, "gmail-forward.txt")}, false, true},
		{"outlook separator under short own text, starting", Member{Text: fixture(t, "outlook-short-forward.txt")}, true, true},
		{"outlook separator under short own text, in a thread", Member{Text: fixture(t, "outlook-short-forward.txt")}, false, false},
		{"long own text above a quote", Member{Text: fixture(t, "question-in-quote.txt")}, true, false},
		{"reply quoting below", Member{Text: fixture(t, "reply-with-quote.txt")}, false, false},
		{"apple forward", Member{Text: fixture(t, "apple-forward.txt")}, true, false},
		{"plain question", Member{Text: fixture(t, "question-own.txt")}, true, false},
		{"rfc822 part", Member{HasMessagePart: true}, false, true},
		{"subject", Member{Subject: "Fwd: x"}, false, true},
		{"empty", Member{}, true, false},
	} {
		if got := forwardShaped(&c.m, c.starting); got != c.want {
			t.Fatalf("%s: %v", c.name, got)
		}
	}
}
