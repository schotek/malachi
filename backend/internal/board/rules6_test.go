// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"fmt"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// RulesVersion 6, decision (a): a message of the user's shaped like a
// forward inside a thread is passed over like a note to self — it does
// not decide the case, does not end it and is not the deciding member.
func TestOwnForwardPassedOver(t *testing.T) {
	bob := inbound("a", 5, "bob@example.com", "me@example.org")
	inThread := func(id string, h int, text string, to ...string) Member {
		return with(sent(id, h, text, to...), func(m *Member) { m.InReplyTo = "<a@mail.example>" })
	}
	q := fixture(t, "question-own.txt")
	cases := []struct {
		name     string
		members  []Member
		state    api.BoardState
		reason   api.BoardReason
		deciding api.MessageID
		mine     bool
	}{
		{"Fwd: subject", []Member{bob,
			with(inThread("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"no Fwd:, text starts with a forwarded message", []Member{bob,
			inThread("b", 2, fixture(t, "own-forward-in-thread.txt"), "carol@example.com"),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"no Fwd:, CRLF Outlook block with broken bytes", []Member{bob,
			inThread("b", 2, fixture(t, "own-forward-in-thread-crlf.txt"), "carol@example.com"),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"attached message", []Member{bob,
			with(inThread("b", 2, "See attached", "bob@example.com"), func(m *Member) { m.HasMessagePart = true }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"forward to the sender is not a reply", []Member{bob,
			with(inThread("b", 2, "FYI", "bob@example.com"), func(m *Member) { m.Subject = "FW: Lunch" }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"reply, then a forward: the reply decides", []Member{bob,
			inThread("b", 3, "Sure, Thursday works.", "bob@example.com"),
			with(inThread("c", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, api.BoardThem, api.BoardReasonThemReplied, "b", true},
		{"forward, then a reply: the reply decides", []Member{bob,
			with(inThread("b", 3, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
			inThread("c", 2, "Sure, Thursday works.", "bob@example.com"),
		}, api.BoardThem, api.BoardReasonThemReplied, "c", true},
		{"a question stays asked after a forward", []Member{
			sent("a", 5, q, "bob@example.com"),
			with(sent("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch"; m.InReplyTo = "<a@mail.example>" }),
		}, api.BoardThem, api.BoardReasonThemAsked, "a", true},
		{"a note and a forward after inbound", []Member{bob,
			sent("b", 3, "Remember the lunch.", "me@example.org"),
			inThread("c", 2, fixture(t, "own-forward-in-thread.txt"), "carol@example.com"),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"nothing but forwards is no case", []Member{
			sent("a", 5, fixture(t, "gmail-forward.txt"), "carol@example.com"),
			with(sent("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch"; m.InReplyTo = "<a@mail.example>" }),
		}, "", "", "", false},
		{"nothing but a note and a forward is no case", []Member{
			sent("a", 5, "Remember the lunch.", "me@example.org"),
			with(sent("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, "", "", "", false},
		{"a truncated forward header", []Member{bob,
			inThread("b", 2, fixture(t, "own-forward-truncated.txt"), "carol@example.com"),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a", false},
		{"a forged inbound Fwd: is inbound like any other", []Member{bob,
			with(inbound("b", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			th := Thread{Members: c.members}
			v := Evaluate(th, me, now)
			if v.State != c.state || v.Reason != c.reason {
				t.Fatalf("got %q/%q, want %q/%q", v.State, v.Reason, c.state, c.reason)
			}
			if v.DecidingMessageID != c.deciding || v.DecidingMine != c.mine {
				t.Fatalf("deciding %q mine %v, want %q %v", v.DecidingMessageID, v.DecidingMine, c.deciding, c.mine)
			}
			if c.deciding != "" && v.LatestID != c.deciding {
				t.Fatalf("latest %q, want the deciding member %q", v.LatestID, c.deciding)
			}
			if v.Count != len(c.members) {
				t.Fatalf("count %d: a forward still counts", v.Count)
			}
			checkTextMembers(t, th, me, v)
		})
	}
}

// The forward passed over leaves Date, Subject and the person to the
// deciding member.
func TestOwnForwardKeepsDisplay(t *testing.T) {
	bob := with(inbound("a", 5, "bob@example.com", "me@example.org"), func(m *Member) { m.Subject = "Contract" })
	fwd := with(sent("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Something else"; m.InReplyTo = "<a@mail.example>" })
	v := Evaluate(Thread{Members: []Member{bob, fwd}}, me, now)
	if v.Subject != "Contract" || !v.Date.Equal(Arrival(bob, now)) || v.Person.Address != "bob@example.com" || v.ReplyID != "a" {
		t.Fatalf("%+v", v)
	}
	// Every counting message a forward: the display comes from the newest
	// of them, and a note to self after them changes nothing.
	only := []Member{with(fwd, func(m *Member) { m.InReplyTo = "" })}
	o := Evaluate(Thread{Members: only}, me, now)
	n := Evaluate(Thread{Members: append(slices.Clone(only), sent("n", 1, "Note.", "me@example.org"))}, me, now)
	if o.LatestID != "b" || n.LatestID != "b" || !n.Date.Equal(o.Date) || n.DecidingMessageID != "" {
		t.Fatalf("only forwards: %+v / %+v", o, n)
	}
}

// The forwards looked at are the user's ten newest messages after the
// newest inbound one; their texts are what TextMembers names, so an older
// forward is judged without its text, the same with and without texts.
func TestOwnForwardScanBound(t *testing.T) {
	bob := inbound("a", 50, "bob@example.com", "me@example.org")
	fw := fixture(t, "own-forward-in-thread.txt")
	ms := []Member{bob}
	for i := range maxAskScan + 1 {
		ms = append(ms, with(sent(fmt.Sprintf("f%02d", i), 40-i, fw, "bob@example.com"), func(m *Member) { m.InReplyTo = "<a@mail.example>" }))
	}
	v := Evaluate(Thread{Members: ms}, me, now)
	if len(v.TextMembers()) != maxAskScan || slices.Contains(v.TextMembers(), "f00") {
		t.Fatalf("text members %v", v.TextMembers())
	}
	// f00 is beyond the scan: it decides, as a reply to bob.
	if v.DecidingMessageID != "f00" || v.State != api.BoardThem || v.Reason != api.BoardReasonThemReplied {
		t.Fatalf("%q %q/%q", v.DecidingMessageID, v.State, v.Reason)
	}
	checkTextMembers(t, Thread{Members: ms}, me, v)
	// Ten forwards after bob: bob decides.
	v = Evaluate(Thread{Members: append([]Member{bob}, ms[2:]...)}, me, now)
	if v.DecidingMessageID != "a" || v.Reason != api.BoardReasonYouAddressed {
		t.Fatalf("%q %q", v.DecidingMessageID, v.Reason)
	}
}

// RulesVersion 6, decision (b): the user's flag makes a case hot whoever
// wrote last, and the flagged copies are what board.unflag clears.
func TestFlaggedWhoeverWroteLast(t *testing.T) {
	flagged := with(inbound("a", 5, "bob@example.com", "me@example.org"), func(m *Member) { m.Flagged = true })
	runRules(t, []ruleCase{
		{"flagged inbound, my reply last", []Member{flagged, sent("b", 2, "Sure.", "bob@example.com")}, api.BoardHot, api.BoardReasonHotFlagged},
		{"flagged inbound, my reply to somebody else last", []Member{flagged, sent("b", 2, "Carol, see below.", "carol@example.com")}, api.BoardHot, api.BoardReasonHotFlagged},
		{"my flagged reply last", []Member{
			inbound("a", 5, "bob@example.com", "me@example.org"),
			with(sent("b", 2, "Sure.", "bob@example.com"), func(m *Member) { m.Flagged = true }),
		}, api.BoardHot, api.BoardReasonHotFlagged},
		{"flagged inbound, my forward last", []Member{flagged,
			with(sent("b", 2, "FYI", "carol@example.com"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, api.BoardHot, api.BoardReasonHotFlagged},
		{"flag in the trash does not count", []Member{
			with(flagged, func(m *Member) { m.FolderRole = api.RoleTrash }),
			sent("b", 2, "Sure.", "bob@example.com"),
		}, "", ""},
		{"flagged, pending classification decides nothing", []Member{
			flagged, with(inbound("c", 1, "carol@example.com", "me@example.org"), func(m *Member) { m.Bulk = BulkUnclassified }),
		}, "", ""},
	})
	th := Thread{Members: []Member{flagged, sent("b", 2, "Sure.", "bob@example.com")}}
	if got := FlaggedCopies(th, me, now); !slices.Equal(got, []api.MessageID{"a"}) {
		t.Fatalf("flagged copies %v", got)
	}
}

// RulesVersion 6, decision (c): an unknown sender with the user in To is a
// new contact (you); not in To, info.unknownSender; Importance of an
// unknown sender never counts.
func TestNewContact(t *testing.T) {
	dave := func(f func(*Member)) Member { return with(inbound("a", 2, "dave@example.net", "me@example.org"), f) }
	none := func(*Member) {}
	runRules(t, []ruleCase{
		{"unknown, in To", []Member{dave(none)}, api.BoardYou, api.BoardReasonYouNewContact},
		{"unknown, in To with another alias", []Member{dave(func(m *Member) { m.To = addrs("x@example.com", "ME@work.example") })}, api.BoardYou, api.BoardReasonYouNewContact},
		{"unknown, in To, Importance: High", []Member{dave(func(m *Member) { m.Importance = " High " })}, api.BoardYou, api.BoardReasonYouNewContact},
		{"unknown, in To, X-Priority 1", []Member{dave(func(m *Member) { m.XPriority = "1 (Highest)" })}, api.BoardYou, api.BoardReasonYouNewContact},
		{"unknown, only in Cc", []Member{dave(func(m *Member) { m.To = addrs("team@example.com"); m.Cc = addrs("me@example.org") })}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"unknown, not addressed", []Member{dave(func(m *Member) { m.To = addrs("list@example.com") })}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"unknown, empty and hostile To", []Member{dave(func(m *Member) { m.To = addrs("", " ", "‮me@example.org") })}, api.BoardInfo, api.BoardReasonInfoUnknownSender},
		{"unknown, Reply-To a known one", []Member{dave(func(m *Member) { m.ReplyTo = addrs("bob@example.com") })}, api.BoardYou, api.BoardReasonYouAddressed},
		{"known, only in Cc", []Member{with(inbound("a", 2, "bob@example.com", "team@example.com"), func(m *Member) { m.Cc = addrs("me@example.org") })}, api.BoardInfo, api.BoardReasonInfoCcOnly},
		{"known, not addressed", []Member{inbound("a", 2, "bob@example.com", "team@example.com")}, api.BoardInfo, api.BoardReasonInfoNotAddressed},
		{"unknown answering the user", []Member{
			sent("s", 5, "Plan attached.", "team@example.com"),
			dave(func(m *Member) { m.InReplyTo = "<s@mail.example>" }),
		}, api.BoardYou, api.BoardReasonYouRepliedToYou},
		{"unknown flagged", []Member{dave(func(m *Member) { m.Flagged = true })}, api.BoardHot, api.BoardReasonHotFlagged},
	})
}

// A subject with emoji ZWJ sequences and a Persian word keeps its joiners;
// stray ones go (CleanText, RulesVersion 6).
func TestJoinerSubject(t *testing.T) {
	family := "\U0001F468‍\U0001F469‍\U0001F467"
	m := with(inbound("a", 2, "bob@example.com", "me@example.org"), func(m *Member) {
		m.Subject = "Re: ‍" + family + "‍‍ piknik می‌خواهم ‌"
	})
	v := Evaluate(Thread{Members: []Member{m}}, me, now)
	if want := family + " piknik می‌خواهم"; v.Subject != want {
		t.Fatalf("subject %q, want %q", v.Subject, want)
	}
}
