package compose

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSubjectPrefixes(t *testing.T) {
	cases := map[string]string{
		"Hello":            "Re: Hello",
		"Re: Hello":        "Re: Hello",
		"RE: re: Hello":    "Re: Hello",
		"Fwd: FW: Hello":   "Re: Hello",
		"AW: Hello":        "Re: Hello",
		"  Re:   spaced  ": "Re: spaced",
		"":                 "Re: ",
		"Rear window":      "Re: Rear window",
	}
	for in, want := range cases {
		if got := ReplySubject(in); got != want {
			t.Errorf("ReplySubject(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ForwardSubject("Re: Fwd: x"); got != "Fwd: x" {
		t.Errorf("ForwardSubject = %q", got)
	}
}

func TestPrefillEscapes(t *testing.T) {
	self := api.Address{Address: "me@example.invalid"}
	src := Source{
		ID:      "m_1",
		From:    []api.Address{{Name: "<b>Alice</b>", Address: "alice@example.invalid"}},
		To:      []api.Address{self, {Address: "bob@example.invalid"}},
		CC:      []api.Address{{Address: "ALICE@example.invalid"}, {Address: "carol@example.invalid"}},
		Subject: "Re: <script>alert(1)</script> & co",
		Date:    time.Date(2026, 9, 2, 14, 3, 0, 0, time.UTC),
		Text:    "line1\r\nline2 </blockquote><img src=x onerror=alert(1)>",
	}

	p := Prefill(KindReplyAll, src, self, time.Now())
	if p.Kind != KindReplyAll || p.InReplyTo != "m_1" || p.Forwarding != "" {
		t.Errorf("meta: %+v", p)
	}
	if len(p.To) != 1 || p.To[0].Address != "alice@example.invalid" {
		t.Errorf("To = %+v", p.To)
	}
	if len(p.CC) != 2 || p.CC[0].Address != "bob@example.invalid" || p.CC[1].Address != "carol@example.invalid" {
		t.Errorf("CC = %+v (self and duplicates must be dropped)", p.CC)
	}
	if p.Subject != "Re: <script>alert(1)</script> & co" {
		t.Errorf("Subject = %q", p.Subject)
	}
	// Nothing from the source may become markup: only our own tags exist.
	for _, bad := range []string{"<script", "<img", "<b>Alice"} {
		if strings.Contains(p.BodyHTML, bad) {
			t.Errorf("unescaped %q in body: %s", bad, p.BodyHTML)
		}
	}
	if strings.Count(p.BodyHTML, "<blockquote") != 1 || strings.Count(p.BodyHTML, "</blockquote>") != 1 {
		t.Errorf("source text broke out of the cite block: %s", p.BodyHTML)
	}
	for _, good := range []string{"&lt;b&gt;Alice&lt;/b&gt; wrote:<br>", "line1<br>line2", `<blockquote type="cite">`, "&lt;img src=x onerror=alert(1)&gt;"} {
		if !strings.Contains(p.BodyHTML, good) {
			t.Errorf("missing %q in body: %s", good, p.BodyHTML)
		}
	}

	f := Prefill(KindForward, src, self, time.Now())
	if f.Forwarding != "m_1" || f.InReplyTo != "" || len(f.To) != 0 || f.Subject != "Fwd: <script>alert(1)</script> & co" {
		t.Errorf("forward meta: %+v", f)
	}
	if !strings.Contains(f.BodyHTML, "Forwarded message") || strings.Contains(f.BodyHTML, "<script>") {
		t.Errorf("forward body: %s", f.BodyHTML)
	}

	r := Prefill(KindReply, Source{From: []api.Address{{Address: "x@example.invalid"}}, Text: "hi"}, self, time.Now())
	if len(r.To) != 1 || len(r.CC) != 0 || !strings.Contains(r.BodyHTML, "On x@example.invalid wrote:") {
		t.Errorf("reply without name/date: %+v", r)
	}
}
