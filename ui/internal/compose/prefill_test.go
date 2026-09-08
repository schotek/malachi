// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

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

	p := Prefill(KindReplyAll, src, self)
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
	for _, good := range []string{"&lt;b&gt;Alice&lt;/b&gt; wrote:</div>", "line1<br>line2", `<blockquote type="cite">`, "&lt;img src=x onerror=alert(1)&gt;"} {
		if !strings.Contains(p.BodyHTML, good) {
			t.Errorf("missing %q in body: %s", good, p.BodyHTML)
		}
	}

	f := Prefill(KindForward, src, self)
	if f.Forwarding != "m_1" || f.InReplyTo != "" || len(f.To) != 0 || f.Subject != "Fwd: <script>alert(1)</script> & co" {
		t.Errorf("forward meta: %+v", f)
	}
	if !strings.Contains(f.BodyHTML, "Forwarded message") || strings.Contains(f.BodyHTML, "<script>") {
		t.Errorf("forward body: %s", f.BodyHTML)
	}

	r := Prefill(KindReply, Source{From: []api.Address{{Address: "x@example.invalid"}}, Text: "hi"}, self)
	if len(r.To) != 1 || len(r.CC) != 0 || !strings.Contains(r.BodyHTML, "<div>x@example.invalid wrote:</div>") {
		t.Errorf("reply without name/date: %+v", r)
	}
}

// Attribution is plain text for the backend to escape: names go in as
// they are, lines are joined with "\n", and a new message has none.
func TestAttribution(t *testing.T) {
	src := Source{
		From:    []api.Address{{Name: "<b>Alice</b>", Address: "alice@example.invalid"}, {Address: "bob@example.invalid"}},
		To:      []api.Address{{Name: "Me", Address: "me@example.invalid"}},
		Subject: "Hi & bye",
		Date:    time.Date(2026, 9, 2, 14, 3, 0, 0, time.UTC),
	}
	reply := Attribution(KindReply, src)
	if !strings.HasPrefix(reply, "On ") || !strings.HasSuffix(reply, ", <b>Alice</b>, bob@example.invalid wrote:") || strings.Contains(reply, "\n") {
		t.Errorf("reply = %q", reply)
	}
	if got := Attribution(KindReplyAll, Source{From: src.From[:1]}); got != "<b>Alice</b> wrote:" {
		t.Errorf("reply without date = %q", got)
	}
	lines := strings.Split(Attribution(KindForward, src), "\n")
	if len(lines) != 5 || lines[0] != "---------- Forwarded message ----------" ||
		!strings.HasPrefix(lines[1], "From: ") || !strings.Contains(lines[1], "alice@example.invalid") ||
		!strings.HasPrefix(lines[2], "Date: ") || lines[3] != "Subject: Hi & bye" || !strings.HasPrefix(lines[4], "To: ") {
		t.Errorf("forward = %q", lines)
	}
	if got := Attribution(KindForward, Source{Subject: "x"}); strings.Count(got, "\n") != 2 {
		t.Errorf("forward without date and To = %q", got)
	}
	if Attribution(KindNew, src) != "" {
		t.Error("a new message has an attribution")
	}
	// A To: line of hundreds of addresses is cut to the backend's cap.
	many := Source{Subject: "x"}
	for i := 0; i < 300; i++ {
		many.To = append(many.To, api.Address{Name: "Řehoř", Address: "r@example.invalid"})
	}
	if got := Attribution(KindForward, many); len(got) > api.MaxDraftAttributionBytes || !strings.HasSuffix(got, "…") {
		t.Errorf("long attribution: %d bytes, suffix %q", len(got), got[len(got)-3:])
	}
}

func TestKindMode(t *testing.T) {
	for k, want := range map[Kind]api.ComposeMode{KindNew: api.ComposeNew, KindReply: api.ComposeReply, KindReplyAll: api.ComposeReplyAll, KindForward: api.ComposeForward} {
		if got := k.Mode(); got != want {
			t.Errorf("Kind(%d).Mode() = %q, want %q", k, got, want)
		}
	}
}

// FromDraft carries the backend's template over as it is, and shows the
// text when there is no HTML.
func TestFromDraft(t *testing.T) {
	d := api.Draft{
		AccountID: "acc_1", To: []api.Address{{Address: "a@example.invalid"}}, CC: []api.Address{{Address: "c@example.invalid"}},
		Subject: "Re: x", HTMLBody: `<p><br/></p><blockquote type="cite">hi</blockquote>`, TextBody: "> hi",
		InReplyTo: "m_1", Attachments: []api.DraftAttachment{{ID: "att_1", Inline: true, ContentID: "c@malachi.local"}},
	}
	blocked := api.BlockedContent{RemoteImages: 2}
	p := FromDraft(KindReply, d, blocked)
	if p.Kind != KindReply || p.AccountID != "acc_1" || len(p.To) != 1 || len(p.CC) != 1 || p.Subject != "Re: x" ||
		p.BodyHTML != d.HTMLBody || p.InReplyTo != "m_1" || p.Forwarding != "" || len(p.Attachments) != 1 || p.Blocked != blocked {
		t.Errorf("params = %+v", p)
	}
	plain := FromDraft(KindForward, api.Draft{TextBody: "a <b>\nc", Forwarding: "m_2"}, api.BlockedContent{})
	if plain.BodyHTML != "a &lt;b&gt;<br>c" || plain.Forwarding != "m_2" {
		t.Errorf("plain params = %+v", plain)
	}
}
