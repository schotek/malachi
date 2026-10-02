// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/pkg/api"
)

// outlookHTMLReply is an Outlook desktop reply: own paragraphs, then the
// <div> with a top border around the bold header block, then the old
// message.
func outlookHTMLReply(own, quoted string) string {
	return `<div class="WordSection1"><p class="MsoNormal">` + own + `<o:p></o:p></p>` +
		`<div><div style="border:none;border-top:solid #E1E1E1 1.0pt;padding:3.0pt 0cm 0cm 0cm">` +
		`<p class="MsoNormal"><b>From:</b> Bob &lt;bob@example.invalid&gt;<br><b>Sent:</b> Wednesday, October 1, 2026 4:05 PM<br>` +
		`<b>To:</b> Alice &lt;alice@example.invalid&gt;<br><b>Subject:</b> RE: Figures<o:p></o:p></p></div></div>` +
		`<p class="MsoNormal">` + quoted + `<o:p></o:p></p></div>`
}

// board.get shows each member's own text: the quoted history goes as
// message.body with trimQuoted takes it off — by the text rules when they
// find it in the stored text, else by the HTML's when they cut the HTML —
// and in doubt the text stays whole. board.queue keeps its own excerpt of
// the stored text.
func TestBoardGetTrimsQuotedHistory(t *testing.T) {
	const quoted = "Which table do you mean? The old figures are here."
	cases := []struct {
		name, text, html string
		want             string
		trimmed          bool
	}{
		{
			name: "outlook html, text alternative with the header block",
			text: "Can you check the second table?\n\nAlice\n\nFrom: Bob <bob@example.invalid>\nSent: Wednesday, October 1, 2026 4:05 PM\n" +
				"To: Alice <alice@example.invalid>\nSubject: RE: Figures\n\n" + quoted,
			html: outlookHTMLReply("Can you check the second table?</p><p>Alice", quoted),
			want: "Can you check the second table?\n\nAlice", trimmed: true,
		},
		{
			// innerText of the HTML: the header block runs on from the
			// signature, which the text rules leave alone; the HTML is cut.
			name: "outlook html, text alternative the text rules cannot cut",
			text: "Can you check the second table?\nAlice\nFrom: Bob <bob@example.invalid>\nSent: Wednesday, October 1, 2026 4:05 PM\n" +
				"To: Alice <alice@example.invalid>\nSubject: RE: Figures\n" + quoted,
			html: outlookHTMLReply("Can you check the second table?</p><p>Alice", quoted),
			want: "Can you check the second table?\n\nAlice", trimmed: true,
		},
		{
			name: "outlook plain text",
			text: "Can you check the second table?\r\n\r\nAlice\r\n\r\nFrom: Bob <bob@example.invalid>\r\nSent: Wednesday, October 1, 2026 4:05 PM\r\n" +
				"To: Alice <alice@example.invalid>\r\nSubject: RE: Figures\r\n\r\n" + quoted + "\r\n",
			want: "Can you check the second table?\n\nAlice", trimmed: true,
		},
		{
			name: "outlook plain text, czech",
			text: "Můžeš zkontrolovat druhou tabulku?\n\nAlice\n\nOd: Bob <bob@example.invalid>\nOdesláno: středa 1. října 2026 16:05\n" +
				"Komu: Alice <alice@example.invalid>\nPředmět: RE: Čísla\n\n" + quoted,
			want: "Můžeš zkontrolovat druhou tabulku?\n\nAlice", trimmed: true,
		},
		{
			// Gmail's text alternative is innerText: the quote has no ">".
			name: "gmail",
			text: "Can you check the second table?\n\nOn Wed, Oct 1, 2026 at 4:05 PM Bob <bob@example.invalid> wrote:\n" + quoted,
			html: gmailQuote("Can you check the second table?", quoted),
			want: "Can you check the second table?", trimmed: true,
		},
		{
			name: "quoted lines after an attribution",
			text: "Can you check the second table?\n\nOn Wed, Oct 1, 2026 at 4:05 PM Bob <bob@example.invalid> wrote:\n> " + quoted + "\n>\n",
			want: "Can you check the second table?", trimmed: true,
		},
		{
			name: "interleaved reply",
			text: "Hi Bob,\n\nOn Wed, Bob wrote:\n> Which table?\nThe second one. Can you check it?\n> And the totals?\nThey do not add up.",
			want: "Hi Bob,\n\nOn Wed, Bob wrote:\n> Which table?\nThe second one. Can you check it?\n> And the totals?\nThey do not add up.",
		},
		{
			name: "a pure forward",
			text: "\nFrom: Bob <bob@example.invalid>\nSent: Wednesday, October 1, 2026 4:05 PM\nTo: Alice <alice@example.invalid>\n" +
				"Subject: Figures\n\nCan you check the second table?",
			want: "From: Bob <bob@example.invalid>\nSent: Wednesday, October 1, 2026 4:05 PM\nTo: Alice <alice@example.invalid>\n" +
				"Subject: Figures\n\nCan you check the second table?",
		},
		{
			name: "no quote",
			text: "Can you check the second table?\n\nThe totals do not add up.",
			want: "Can you check the second table?\n\nThe totals do not add up.",
		},
		{
			// Header-looking lines of the writer's own: introduced by a
			// colon, without a date, or not after a blank line.
			name: "header-looking own text",
			text: "Can you head the report like this:\n\nFrom: Sales\nSent: 6 Oct 2026\nTo: everyone\n\nand like this?\nFrom: Sales\n" +
				"Sent: 6 Oct 2026\nTo: everyone\n\nThanks.",
			want: "Can you head the report like this:\n\nFrom: Sales\nSent: 6 Oct 2026\nTo: everyone\n\nand like this?\nFrom: Sales\n" +
				"Sent: 6 Oct 2026\nTo: everyone\n\nThanks.",
		},
		{
			// An HTML whose quote is not at the end: neither side is cut.
			name: "html quote with a reply below",
			text: "Can you check it?\n\nOn Wed, Bob wrote:\n" + quoted + "\n\nAnd the totals too.",
			html: `<p>Can you check it?</p><blockquote type="cite">` + quoted + `</blockquote><p>And the totals too.</p>`,
			want: "Can you check it?\n\nOn Wed, Bob wrote:\n" + quoted + "\n\nAnd the totals too.",
		},
	}

	x := newBoardBox(t)
	threads := make([]string, len(cases))
	for i, c := range cases {
		threads[i] = "t_quote_" + string(rune('a'+i))
		x.put(bmail{folder: x.inbox, thread: threads[i], rfc: threads[i], from: boardBob, to: []api.Address{boardMe},
			at: 0, text: c.text, html: c.html})
	}
	x.drain()

	texts := map[api.BoardCaseID]string{}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := x.caseOf(threads[i]).ID
			texts[id] = c.text
			got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: id})
			if err != nil || len(got.Messages) != 1 {
				t.Fatalf("board.get: %+v %v", got, err)
			}
			m := got.Messages[0]
			if m.Text != c.want || m.Trimmed != c.trimmed {
				t.Fatalf("text %q, trimmed %v; want %q, %v", m.Text, m.Trimmed, c.want, c.trimmed)
			}
			if strings.Contains(m.Text, "old figures") && c.trimmed {
				t.Fatalf("the quote stayed: %q", m.Text)
			}
		})
	}

	// board.queue reads the stored plain text as it always did: an
	// excerpt the HTML did not shorten.
	x.assistantOn()
	ids := make([]api.BoardCaseID, 0, len(texts))
	for id := range texts {
		ids = append(ids, id)
	}
	var items []api.BoardQueueItem
	for len(ids) > 0 {
		n := min(len(ids), api.MaxBoardQueueLimit)
		q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{CaseIDs: ids[:n], Limit: n})
		if err != nil {
			t.Fatal(err)
		}
		items, ids = append(items, q.Items...), ids[n:]
	}
	seen := 0
	for _, it := range items {
		text, ok := texts[it.CaseID]
		if !ok || len(it.Messages) != 1 {
			continue
		}
		seen++
		want := board.QueueExcerpts([]string{text})[0]
		if m := it.Messages[0]; m.Text != want.Text || m.Truncated != want.Trimmed {
			t.Errorf("queue %s: %q, %v; want %q, %v", it.CaseID, m.Text, m.Truncated, want.Text, want.Trimmed)
		}
	}
	if seen != len(cases) {
		t.Fatalf("board.queue served %d of %d cases", seen, len(cases))
	}
}
