// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Own text: replies whose quote is not prefixed (the text alternative of
// HTML, innerText), interleaved replies, very long texts and own text the
// caller derived from HTML.
func TestOwnTextShapes(t *testing.T) {
	const own = "Sure, I will send the figures by Monday."
	const other = "I will send the slides by Friday morning"
	for _, name := range []string{"reply-innertext.txt", "reply-innertext-wrapped.txt", "reply-with-quote.txt"} {
		t.Run(name, func(t *testing.T) {
			m := Member{Text: fixture(t, name)}
			if got := OwnText(m); got != own {
				t.Fatalf("own text %q", got)
			}
			if !QuoteInOwnText(m, "I will send the figures by Monday") || QuoteInOwnText(m, other) || !QuoteIn(m.Text, other) {
				t.Fatal("quote check")
			}
			if hasQuestion(OwnText(m)) {
				t.Fatal("the quoted question counts")
			}
		})
	}

	t.Run("interleaved", func(t *testing.T) {
		m := Member{Text: fixture(t, "reply-interleaved.txt")}
		got := OwnText(m)
		if got != "Yes, by Monday at the latest.\n\n\nCould you send them as PDF?" {
			t.Fatalf("own text %q", got)
		}
		if !QuoteInOwnText(m, "Yes, by Monday at the latest") || QuoteInOwnText(m, other) || QuoteInOwnText(m, "Can you send the figures") {
			t.Fatal("quote check")
		}
		if !hasQuestion(got) {
			t.Fatal("own question missed")
		}
		if hasQuestion(OwnText(Member{Text: "On Tue, Bob wrote:\n> Can you?\nSure.\n> And this?\nYes.\n"})) {
			t.Fatal("quoted question counts")
		}
	})

	t.Run("over the line limit", func(t *testing.T) {
		m := Member{Text: own + "\n" + strings.Repeat("\n", maxOwnTextLines) + "On Tue, Bob wrote:\n> " + other + "?\n"}
		if OwnText(m) != "" || QuoteInOwnText(m, "I will send the figures by Monday") {
			t.Fatal("not failing closed")
		}
		just := Member{Text: own + strings.Repeat("\n", maxOwnTextLines-2)}
		if OwnText(just) != own {
			t.Fatal("just under the limit")
		}
	})

	t.Run("starts with a quoted history", func(t *testing.T) {
		for _, name := range []string{"gmail-forward.txt", "apple-forward.txt"} {
			m := Member{Text: fixture(t, name)}
			got := OwnText(m)
			if name == "gmail-forward.txt" && got != "" {
				t.Fatalf("%s: %q", name, got)
			}
			if name == "apple-forward.txt" && got == "" {
				t.Fatal("apple forward is a known miss, its text stays") // documents the miss
			}
		}
	})

	t.Run("own text from html", func(t *testing.T) {
		m := Member{Text: fixture(t, "reply-innertext.txt") + "\nI promise the moon by Friday.", OwnText: "Sure, I will send it.\r\n-- \r\nJan", OwnTextSet: true}
		if got := OwnText(m); got != "Sure, I will send it." {
			t.Fatalf("own text %q", got)
		}
		if QuoteInOwnText(m, "I will send the figures by Monday") || QuoteInOwnText(m, "I promise the moon by Friday") {
			t.Fatal("text used instead of the html's own text")
		}
		// An HTML quote the caller could not trim, its text quoting with a
		// forged ">" first line: cut at the attribution all the same.
		forged := Member{OwnText: fixture(t, "reply-forged-quote-marker.txt"), OwnTextSet: true}
		if got := OwnText(forged); got != "Thanks, noted." {
			t.Fatalf("forged marker %q", got)
		}
		if QuoteInOwnText(forged, "I will pay you the whole deposit by Friday") || hasQuestion(OwnText(forged)) {
			t.Fatal("forged marker accepted")
		}
		// Set but empty: no own text, not the plain text.
		if OwnText(Member{Text: "Could you call me?", OwnTextSet: true}) != "" {
			t.Fatal("empty own text")
		}
	})
}

// them.asked never comes from a question in the quote of an innerText
// reply or a very long text.
func TestAskedOwnTextOnly(t *testing.T) {
	news := with(inbound("a", 5, "news@shop.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkNewsletter) })
	reply := func(text string) Member {
		return with(sent("b", 2, text, "news@shop.example"), func(m *Member) { m.InReplyTo = "<a@mail.example>" })
	}
	runRules(t, []ruleCase{
		{"innertext reply to a newsletter", []Member{news, reply("Please remove me.\n\nOn Tue, 30 Sep 2026, News <news@shop.example> wrote:\nDid you see our sale? Shop now?\n")}, "", ""},
		{"own question in an innertext reply", []Member{news, reply("Is the sale on Monday too?\n\nOn Tue, 30 Sep 2026, News <news@shop.example> wrote:\nDid you see our sale?\n")}, api.BoardThem, api.BoardReasonThemAsked},
		{"question below 100k lines", []Member{news, reply("Hello\n" + strings.Repeat("\n", maxOwnTextLines) + "Why?")}, "", ""},
		{"question in html own text", []Member{news, with(reply("Thanks."), func(m *Member) { m.OwnText, m.OwnTextSet = "When does it end?", true })}, api.BoardThem, api.BoardReasonThemAsked},
		{"no question in html own text", []Member{news, with(reply("When does it end?"), func(m *Member) { m.OwnText, m.OwnTextSet = "Thanks.", true })}, "", ""},
	})
}

// Starting the thread is decided by In-Reply-To and References, not by the
// place among the counting members.
func TestStartsThread(t *testing.T) {
	short := fixture(t, "reply-with-quote.txt") // short own text above a quote
	news := with(inbound("a", 5, "news@shop.example", "me@example.org"), func(m *Member) { m.Bulk = string(api.BulkNewsletter) })
	runRules(t, []ruleCase{
		// The newsletter does not count, so the reply is the first counting
		// member; it still answers something and is no forward.
		{"short top-posted reply to bulk mail", []Member{news, with(sent("b", 2, "Is it still on? "+short, "news@shop.example"), func(m *Member) { m.InReplyTo = "<a@mail.example>" })}, api.BoardThem, api.BoardReasonThemAsked},
		{"references alone", []Member{news, with(sent("b", 2, "Is it still on? "+short, "news@shop.example"), func(m *Member) { m.References = []string{" ", "<a@mail.example>"} })}, api.BoardThem, api.BoardReasonThemAsked},
		{"answering nothing", []Member{news, sent("b", 2, "Is it still on? "+short, "news@shop.example")}, "", ""},
		{"blank references answer nothing", []Member{news, with(sent("b", 2, "Is it still on? "+short, "news@shop.example"), func(m *Member) { m.References = []string{"", "<>"} })}, "", ""},
		{"an identifier past the scan answers nothing", []Member{news, with(sent("b", 2, "Is it still on? "+short, "news@shop.example"), func(m *Member) {
			m.References = append(make([]string, maxReferencesScan), "<a@mail.example>")
		})}, "", ""},
		{"an over-long identifier answers nothing", []Member{news, with(sent("b", 2, "Is it still on? "+short, "news@shop.example"), func(m *Member) {
			m.References = []string{"<" + strings.Repeat("a", maxMessageIDBytes) + "@x>"}
		})}, "", ""},
		{"references without in-reply-to, nothing inbound", []Member{with(sent("b", 2, "Is it still on? "+short, "bob@example.com"), func(m *Member) { m.References = []string{"<gone@mail.example>"} })}, api.BoardThem, api.BoardReasonThemAsked},
	})
}

// Twins that are not the user's: the copy stored first represents the
// message; a later copy changes nothing the rules read.
func TestForgedTwins(t *testing.T) {
	boss := with(inbound("y", 30, "boss@example.com", "me@example.org"), func(m *Member) {
		m.MessageID = "<Y@corp.example>"
		m.FolderID, m.FolderRole = "f_archive", api.RoleArchive
	})
	bossKnown := NewIdentity("", []string{"me@example.org"}, []string{"boss@example.com"})
	twin := func(id string, h int, f func(*Member)) Member {
		m := inbound(id, h, "mallory@example.net", "team@example.com")
		m.MessageID = "<Y@corp.example>"
		f(&m)
		return m
	}
	forgeries := map[string]Member{
		"from me to me":  twin("a1", 1, func(m *Member) { m.From.Address, m.To = "me@example.org", addrs("me@example.org") }),
		"importance":     twin("a2", 1, func(m *Member) { m.Importance = "high"; m.To = addrs("me@example.org") }),
		"bulk":           twin("a3", 1, func(m *Member) { m.Bulk = string(api.BulkNewsletter) }),
		"unclassified":   twin("a4", 1, func(m *Member) { m.Bulk = "" }),
		"earlier date":   twin("a5", 1, func(m *Member) { m.Date, m.InternalDate = hoursAgo(1000), hoursAgo(1000) }),
		"lower id":       twin("0", 1, func(m *Member) { m.Importance = "high"; m.To = addrs("me@example.org") }),
		"no stored time": twin("a6", 1, func(m *Member) { m.StoredAt = time.Time{}; m.Importance = "high" }),
	}
	base := Evaluate(Thread{Members: []Member{boss}}, bossKnown, now)
	if base.State != api.BoardYou || base.Reason != api.BoardReasonYouAddressed {
		t.Fatalf("base %s/%s", base.State, base.Reason)
	}
	for name, f := range forgeries {
		t.Run(name, func(t *testing.T) {
			for _, ms := range [][]Member{{boss, f}, {f, boss}, {f, f, boss}} {
				v := Evaluate(Thread{Members: ms}, bossKnown, now)
				if v.State != base.State || v.Reason != base.Reason || v.Count != 1 || v.LatestID != "y" ||
					v.ReplyFolderID != "f_archive" || !v.Date.Equal(base.Date) || v.Person != base.Person || v.Pending {
					t.Fatalf("changed: %+v", v)
				}
			}
		})
	}
	// Two genuine copies (another client moved one): the earlier stays,
	// flags of either count.
	moved := with(boss, func(m *Member) {
		m.ID, m.FolderID, m.FolderRole = "y2", "f_inbox", api.RoleInbox
		m.StoredAt = m.StoredAt.Add(time.Hour)
		m.Flagged = true
	})
	v := Evaluate(Thread{Members: []Member{moved, boss}}, bossKnown, now)
	if v.State != api.BoardHot || v.Reason != api.BoardReasonHotFlagged || v.LatestID != "y" {
		t.Fatalf("moved copy %+v", v)
	}
	// The same stored time: the inbox copy.
	same := with(moved, func(m *Member) { m.StoredAt = boss.StoredAt; m.Flagged = false })
	if v := Evaluate(Thread{Members: []Member{boss, same}}, bossKnown, now); v.ReplyFolderID != "f_inbox" {
		t.Fatalf("tie %+v", v)
	}
}

// The owner's decision: you.addressed and hot.important need a sender the
// user has written to; unknown senders are info.unknownSender.
func TestKnownSenders(t *testing.T) {
	runRules(t, []ruleCase{
		{"unknown sender", []Member{inbound("a", 2, "dave@example.com", "me@example.org")}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"unknown sender, other case of a known one", []Member{inbound("a", 2, "BOB@example.COM", "me@example.org")}, api.BoardYou, api.BoardReasonYouAddressed},
		{"unknown sender, importance high", []Member{with(inbound("a", 2, "dave@example.com", "me@example.org"), func(m *Member) { m.Importance = "high" })}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"unknown sender, flagged by the user", []Member{with(inbound("a", 2, "dave@example.com", "me@example.org"), func(m *Member) { m.Flagged = true })}, api.BoardHot, api.BoardReasonHotFlagged},
		{"unknown sender, cc", []Member{with(inbound("a", 2, "dave@example.com", "x@example.com"), func(m *Member) { m.Cc = addrs("me@example.org") })}, api.BoardInfo, api.BoardReasonInfoCcOnly},
		{"known reply-to", []Member{with(inbound("a", 2, "noreply@shop.example", "me@example.org"), func(m *Member) { m.ReplyTo = addrs("x@shop.example", "Carol@example.com") })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"unknown sender answering the user", []Member{
			sent("a", 5, "Plan attached.", "team@example.com"),
			with(inbound("b", 2, "dave@example.com", "me@example.org"), func(m *Member) { m.InReplyTo = "<a@mail.example>"; m.Importance = "high" }),
		}, api.BoardYou, api.BoardReasonYouRepliedToYou},
		{"written to in this thread", []Member{
			sent("a", 5, "Hello Dave.", "team@example.com", "dave@example.com"),
			inbound("b", 2, "dave@example.com", "me@example.org"),
		}, api.BoardYou, api.BoardReasonYouAddressed},
		{"cc'd in this thread", []Member{
			with(sent("a", 5, "Hello.", "team@example.com"), func(m *Member) { m.Cc = addrs("Dave@Example.com") }),
			with(inbound("b", 2, "dave@example.com", "me@example.org"), func(m *Member) { m.Importance = "high" }),
		}, api.BoardHot, api.BoardReasonHotImportant},
		{"spoofed from me with importance", []Member{
			with(inbound("a", 2, "me@example.org", "me@example.org", "x@example.com"), func(m *Member) { m.Importance = "high" }),
		}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"automated sender", []Member{inbound("a", 2, "noreply@ci.example", "me@example.org")}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
	})
	// Nobody known at all: every addressed message is info.
	nobody := NewIdentity("", []string{"me@example.org"}, nil)
	if v := Evaluate(Thread{Members: []Member{inbound("a", 2, "bob@example.com", "me@example.org")}}, nobody, now); v.Reason != api.BoardReasonInfoUnknownSender {
		t.Fatalf("nobody known: %s", v.Reason)
	}
	// Reply-To beyond the scanned ones does not make a sender known.
	many := make([]string, 0, maxReplyToScan+1)
	for range maxReplyToScan {
		many = append(many, "x@shop.example")
	}
	far := with(inbound("a", 2, "noreply@shop.example", "me@example.org"), func(m *Member) { m.ReplyTo = addrs(append(many, "bob@example.com")...) })
	if v := Evaluate(Thread{Members: []Member{far}}, me, now); v.Reason != api.BoardReasonInfoUnknownSender {
		t.Fatalf("far reply-to: %s", v.Reason)
	}
}

func TestQuestionAfterAddresses(t *testing.T) {
	for text, want := range map[string]bool{
		"Could you send it to anna@example.cz?":   true,
		"Did you see https://x.example?":          true,
		"Did you see https://x.example/?)":        true,
		"Did you see (https://x.example/?q=1).":   false,
		"Write to anna@example.cz.":               false,
		"mail a?b@example.org!":                   false,
		"Is it www.x.example\uff1f":               true,
		"see <https://x.example/?q=1>, thanks":    false,
		"Could you check www.x.example/a?b=1?!":   true,
		"https://x.example/?\"":                   true,
		"https://x.example/a?b ok":                false,
		"The link https://x.example/?q=? is fine": true, // a query ending in "?" reads as a question
	} {
		if got := hasQuestion(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

func TestQuoteDoesNotBridgeURLs(t *testing.T) {
	cases := []struct {
		text, quote string
		want        bool
	}{
		{"I will not://x pay by Friday", "I will pay by Friday", false},
		{"I will https://x.example/not pay by Friday", "I will pay by Friday", false},
		{"I will https://x.example/not pay by Friday", "I will https://x.example/not pay by Friday", true},
		{"I will https://x.example/a pay by Friday", "I will https://other.example/b pay by Friday", true}, // a URL is a URL
		{"Upload it to click:https://x.example by noon", "Upload it to click: by noon", false},
		{"Upload it to click:https://x.example by noon", "Upload it to click:https://y.example by noon", true},
		{"I will https://x.example pay by Friday", "I will \ufffc pay by Friday", false}, // a placeholder cannot be forged
	}
	for _, c := range cases {
		if got := QuoteIn(c.text, c.quote); got != c.want {
			t.Errorf("%q in %q: %v", c.quote, c.text, got)
		}
	}
}

// Subject and person are mail: their URLs stay (only annotation strings
// lose them).
func TestFieldsKeepURLs(t *testing.T) {
	m := inbound("a", 2, "bob@example.com", "me@example.org")
	m.Subject = "Re: Invoice for www.shop.cz order 42 \u2066https://x.example\u2069"
	m.From.Name = "Shop https://shop.example"
	v := Evaluate(Thread{Members: []Member{m}}, me, now)
	if v.Subject != "Invoice for www.shop.cz order 42 https://x.example" || v.Person.Name != "Shop https://shop.example" {
		t.Fatalf("%q %q", v.Subject, v.Person.Name)
	}
	if a := CleanAddress(api.Address{Name: " A\u034f\u115fB ", Address: "x y@example.com"}); a.Name != "AB" || a.Address != "" {
		t.Fatalf("%+v", a)
	}
}

func TestInvisibleCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"a\u034fb":                         "ab",  // combining grapheme joiner
		"a\ufe00b\U000E0100c":              "abc", // variation selectors
		"a\u115f\u1160\u3164\uffa0b":       "ab",  // Hangul fillers
		"a\u17b4\u180bb":                   "ab",
		"a\ufe0fb":                         "ab", // no emoji before it
		"\u2764\ufe0f":                     "\u2764\ufe0f",
		"\u2764\ufe0f\ufe0f\ufe0e":         "\u2764\ufe0f",
		"1\ufe0f\u20e3":                    "1\ufe0f\u20e3", // keycap
		"\U0001F3F3\ufe0f\u200d\U0001F308": "\U0001F3F3\ufe0f\u200d\U0001F308",
		"\u2764\n\ufe0f":                   "\u2764\n",
		"\u0645\u06cc\u200c\u062e":         "\u0645\u06cc\u200c\u062e",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("%+q: %+q, want %+q", in, got, want)
		}
	}
}
