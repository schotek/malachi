// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// FuzzEvaluate feeds hostile text, subject and header values to the rules
// through a message of the user's and an inbound one, and checks
// metamorphic properties: a forged twin of the inbound message changes
// nothing, a later inbound message (spoofed sender) never removes a
// counting member nor gives them, and Importance never gives hot without
// the user in To. Run beyond the seeds only with the owner's consent.
func FuzzEvaluate(f *testing.F) {
	for _, name := range []string{"gmail-forward.txt", "outlook-short-forward.txt", "apple-forward.txt",
		"question-in-quote.txt", "question-in-signature.txt", "question-in-url.txt", "question-own.txt", "reply-with-quote.txt",
		"reply-innertext.txt", "reply-innertext-wrapped.txt", "reply-interleaved.txt", "reply-forged-quote-marker.txt",
		"outlook-header-reply.txt", "outlook-header-forward.txt"} {
		f.Add(fixture(f, name), "Re: Lunch", "high", "1", "bob@example.com", "me@example.org", "none")
	}
	f.Add("", "", "", "", "", "", "")
	f.Add("\xff\x00?", "Fwd[: \u202e", "\r\n high", "2 (High)", "ME@example.org", "me@example.org", "")
	f.Add("Lunch?", "Lunch", "high", "1", "me@work.example", "team@example.com", "newsletter")
	f.Fuzz(func(t *testing.T, text, subject, importance, priority, from, to, bulk string) {
		in := inbound("a", 5, "bob@example.com", "me@example.org")
		in.Subject, in.Importance, in.XPriority, in.Text = subject, importance, priority, text
		mine := sent("b", 2, text, "bob@example.com", text)
		mine.Subject = subject
		for _, ms := range [][]Member{{in}, {mine}, {in, mine}} {
			v := Evaluate(Thread{Members: ms}, me, now)
			if v.State != "" && !v.State.Valid() {
				t.Fatalf("state %q", v.State)
			}
			if !utf8.ValidString(v.Subject) || strings.ContainsAny(v.Subject, "\n\r\x00") || len(v.Subject) > maxSubjectBytes {
				t.Fatalf("subject %q", v.Subject)
			}
			if len(ms) == 1 && ms[0].Mine && v.State == api.BoardThem && v.Reason != api.BoardReasonThemAsked {
				t.Fatalf("reason %q", v.Reason)
			}

			// A twin of the inbound message, stored later, with forged
			// headers: nothing changes.
			twin := with(in, func(m *Member) {
				m.ID = "a0"
				m.StoredAt = m.StoredAt.Add(time.Hour)
				m.From.Address, m.To, m.Bulk = from, addrs(to), bulk
				m.Importance, m.XPriority = importance, priority
				m.InternalDate = m.InternalDate.Add(-time.Hour)
			})
			w := v
			if ms[0].ID == in.ID {
				w = Evaluate(Thread{Members: append(slices.Clone(ms), twin)}, me, now)
			}
			if w.State != v.State || w.Reason != v.Reason || w.Count != v.Count || w.LatestID != v.LatestID || w.Pending != v.Pending {
				t.Fatalf("twin changed %s/%s %d → %s/%s %d", v.State, v.Reason, v.Count, w.State, w.Reason, w.Count)
			}

			// A new inbound message (any sender, any headers), arriving now.
			spoof := with(in, func(m *Member) {
				m.ID, m.MessageID = "z", "<z@mail.example>"
				m.From.Address, m.To, m.Bulk = from, addrs(to), bulk
				m.Importance, m.XPriority = importance, priority
				m.InternalDate, m.StoredAt, m.Date = now, now, time.Time{}
				m.Flagged = false
			})
			s := Evaluate(Thread{Members: append(slices.Clone(ms), spoof)}, me, now)
			if !s.Pending {
				if s.State == api.BoardThem && v.State != api.BoardThem {
					t.Fatalf("spoof gave %s", s.Reason)
				}
				for _, m := range v.Members {
					if !slices.ContainsFunc(s.Members, func(x Member) bool { return x.ID == m.ID }) {
						t.Fatalf("spoof removed %s", m.ID)
					}
				}
			}
			if s.LatestID == spoof.ID && s.Reason == api.BoardReasonHotImportant && !me.Owns(to) {
				t.Fatalf("importance without the user in To: %+v", s)
			}
		}
		e := BoardMessageExcerpt(text)
		if !utf8.ValidString(e.Text) || len(e.Text) > api.MaxBoardMessageTextBytes {
			t.Fatalf("excerpt %q", e.Text)
		}
	})
}

// FuzzQuote checks that a quote that passes CleanQuote is always found in
// a text that holds it between other words, that it is never found in the
// user's own text when it stands only in the quoted part of a reply
// (prefixed, unprefixed below an attribution, or interleaved), and that
// nothing panics.
func FuzzQuote(f *testing.F) {
	f.Add("please send the signed contract by Friday", "send the signed contract")
	f.Add("cafe\u0301 is booked for ten", "caf\u00e9 is booked")
	f.Add("it\u2019s urgent, really urgent", "it's urgent, really")
	f.Add("\xff\xfe\u202e", "\u200b\u200b\u200b\u200b")
	f.Add("x", "I will pay the deposit by Friday")
	f.Add("x", "-- Thanks, noted. On Tue")
	f.Fuzz(func(t *testing.T, text, quote string) {
		_ = QuoteIn(text, quote)
		_ = QuoteInOwnText(Member{Text: text}, quote)
		_ = QuoteInOwnText(Member{Text: text, OwnText: text, OwnTextSet: true}, quote)
		q, ok := CleanQuote(quote)
		if !ok {
			return
		}
		if !QuoteIn("before "+q+" after", q) {
			t.Fatalf("quote %q not found around itself", q)
		}
		const own = "Thanks, noted."
		const attribution = "On Tue, 30 Sep 2026 at 09:12, Bob <bob@example.com> wrote:"
		for _, reply := range []string{
			own + "\n\n" + attribution + "\n> " + q + "\n",
			own + "\n\n" + attribution + "\n" + q + "\n",
			own + "\n\n" + attribution + "\n\n" + q + "\n> more\n",
			attribution + "\n> " + q + "\n" + own + "\n",
			own + "\n> " + q + "\n",
		} {
			for _, m := range []Member{{Text: reply}, {OwnText: reply, OwnTextSet: true}} {
				if QuoteInOwnText(m, q) && !strings.Contains(normalizeQuoted(own), normalizeQuoted(q)) {
					t.Fatalf("quoted %q accepted as own in %q", q, reply)
				}
			}
		}
	})
}
