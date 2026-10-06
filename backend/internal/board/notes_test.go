// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// self is me with the address of another of the user's accounts.
var self = me.WithSelf([]string{" ME@Home.Example ", "me@example.org"})

// noteTo is a message of the user's to their own addresses only, h hours old.
func noteTo(id string, h int, text string, to ...string) Member {
	return with(sent(id, h, text, to...), func(m *Member) { m.Subject = "Re: Lunch" })
}

func TestNotesToSelf(t *testing.T) {
	q := fixture(t, "note-to-self.txt")
	a := inbound("a", 80, "bob@example.com", "me@example.org")
	b := with(inbound("b", 3, "bob@example.com", "me@example.org"), func(m *Member) { m.InReplyTo = "<a@mail.example>" })
	cases := []struct {
		name    string
		id      Identity
		members []Member
		state   api.BoardState
		reason  api.BoardReason
		latest  api.MessageID
	}{
		// The owner's shape: Bob twice, then the user's reply to another
		// of their own addresses only.
		{"reply to another own account", self, []Member{a, b,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.InReplyTo = "<b@mail.example>" }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "b"},
		{"without the other account's address it is no note (today's rule)", me, []Member{a, b,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.InReplyTo = "<b@mail.example>" }),
		}, "", "", "c"},
		{"to this account's alias", me, []Member{a, b, noteTo("c", 2, q, "Me@Work.Example")}, api.BoardYou, api.BoardReasonYouAddressed, "b"},
		{"to self in To, Cc and Bcc", self, []Member{a, with(noteTo("c", 2, q, "me@home.example"), func(m *Member) {
			m.Cc, m.Bcc = addrs("me@work.example"), addrs("ME@EXAMPLE.ORG")
		})}, api.BoardYou, api.BoardReasonYouAddressed, "a"},
		{"bcc only to self", self, []Member{a, with(noteTo("c", 2, q), func(m *Member) { m.Bcc = addrs("me@home.example") })},
			api.BoardYou, api.BoardReasonYouAddressed, "a"},
		{"own to and a foreign cc is no note", self, []Member{a, b,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.Cc = addrs("carol@example.com") }),
		}, "", "", "c"},
		{"own to and a foreign bcc is no note", self, []Member{a, b,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.Bcc = addrs("bob@example.com") }),
		}, "", "", "c"},
		{"a recipient without an address is no note", self, []Member{a, b,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.To = append(m.To, api.Address{Name: "Group"}) }),
		}, "", "", "c"},
		{"no recipients at all: today's rule", self, []Member{a, b, noteTo("c", 2, q)}, "", "", "c"},
		{"a lookalike with invisible characters is foreign", self, []Member{a, b, noteTo("c", 2, q, "me@home.example\u200b")}, "", "", "c"},
		{"reply to bob, then a note: the reply decides", self, []Member{a,
			sent("c", 2, "Sure.", "bob@example.com"),
			noteTo("d", 1, q, "me@home.example"),
		}, api.BoardThem, api.BoardReasonThemReplied, "c"},
		{"a note between does not hide the inbound", self, []Member{a,
			noteTo("c", 50, q, "me@home.example"),
			b,
		}, api.BoardYou, api.BoardReasonYouAddressed, "b"},
		{"a question asked of bob, then a note", self, []Member{
			sent("c", 5, fixture(t, "question-own.txt"), "bob@example.com"),
			noteTo("d", 1, "Done.", "me@home.example"),
		}, api.BoardThem, api.BoardReasonThemAsked, "c"},
		{"nothing but notes, with a question", self, []Member{
			noteTo("c", 5, q, "me@home.example"),
			noteTo("d", 1, q, "me@example.org", "me@work.example"),
		}, "", "", "d"},
		{"a forward to self is a note too", self, []Member{a,
			with(noteTo("c", 2, q, "me@home.example"), func(m *Member) { m.Subject = "Fwd: Lunch" }),
		}, api.BoardYou, api.BoardReasonYouAddressed, "a"},
		{"the user's flag on a note still counts", self, []Member{
			with(noteTo("c", 5, q, "me@home.example"), func(m *Member) { m.Flagged = true }),
			inbound("d", 2, "carol@example.com", "team@example.com"),
		}, api.BoardHot, api.BoardReasonHotFlagged, "d"},
		{"an answer to a note is an answer to the user", self, []Member{
			noteTo("c", 5, q, "me@home.example"),
			with(inbound("d", 2, "dave@example.net", "team@example.com"), func(m *Member) { m.InReplyTo = "<c@mail.example>" }),
		}, api.BoardYou, api.BoardReasonYouRepliedToYou, "d"},
		// A forged inbound "from me to me" is no message of the user's:
		// it stays inbound, never passed over as a note.
		{"forged inbound from me to me", self, []Member{a, inbound("c", 2, "me@home.example", "me@example.org")},
			api.BoardInfo, api.BoardReasonInfoYourNote, "c"},
		{"forged inbound from this account to itself", self, []Member{a, inbound("c", 2, "me@example.org", "me@example.org")},
			api.BoardInfo, api.BoardReasonInfoYourNote, "c"},
		{"inbound from another own account, to a foreign address too", self, []Member{inbound("c", 2, "me@home.example", "me@example.org", "bob@example.com")},
			api.BoardInfo, api.BoardReasonInfoUnknownSender, "c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			th := Thread{Members: c.members}
			v := Evaluate(th, c.id, now)
			if v.State != c.state || v.Reason != c.reason {
				t.Fatalf("got %q/%q, want %q/%q", v.State, v.Reason, c.state, c.reason)
			}
			if v.LatestID != c.latest {
				t.Fatalf("latest %s, want %s", v.LatestID, c.latest)
			}
			if v.Count != len(c.members) {
				t.Fatalf("count %d, want %d: a note still counts", v.Count, len(c.members))
			}
			checkTextMembers(t, th, c.id, v)
		})
	}
}

// The case's date, subject and latest member are those of the member the
// rules decided by; the note still counts for the rest.
func TestNoteToSelfFields(t *testing.T) {
	a := inbound("a", 5, "bob@example.com", "me@example.org")
	n := with(noteTo("n", 1, "x", "me@home.example"), func(m *Member) {
		m.Subject = "Re: Something else"
		m.HasAttachments = true
		m.Unread = true
	})
	v := Evaluate(Thread{Members: []Member{a, n}}, self, now)
	if v.State != api.BoardYou || !v.Date.Equal(hoursAgo(5)) || v.Subject != "Lunch" || v.LatestID != "a" || v.ReplyID != "a" {
		t.Fatalf("verdict %+v", v)
	}
	if v.Count != 2 || len(v.Members) != 2 || !v.HasAttachments || !v.Unread || v.Person.Address != "bob@example.com" {
		t.Fatalf("counting %+v", v)
	}
	if got := v.TextMembers(); len(got) != 0 {
		t.Fatalf("text members %v", got)
	}
	// Nothing but notes: no case, the fields from the notes (a kept case
	// shows them), no person among the user's own addresses.
	o := Evaluate(Thread{Members: []Member{n}}, self, now)
	if o.State != "" || o.Count != 1 || o.LatestID != "n" || !o.Date.Equal(hoursAgo(1)) || o.Person.Address != "" {
		t.Fatalf("only notes %+v", o)
	}
	// The person of a question is never one of the user's other addresses.
	q := Evaluate(Thread{Members: []Member{sent("q", 1, "Lunch?", "me@home.example", "bob@example.com")}}, self, now)
	if q.State != api.BoardThem || q.Person.Address != "bob@example.com" {
		t.Fatalf("asked %+v", q)
	}
}

func TestIdentitySelf(t *testing.T) {
	if !self.Self("ME@HOME.EXAMPLE") || !self.Self(" me@work.example ") || self.Owns("me@home.example") {
		t.Fatal("self addresses")
	}
	if me.Self("me@home.example") || self.Self("") || self.Self("bob@example.com") {
		t.Fatal("not self")
	}
	many := make([]string, MaxSelfAddresses+10)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + "@x" + string(rune('0'+i/26%10)) + string(rune('0'+i/260)) + ".example"
	}
	if got := len(me.WithSelf(many).self); got > MaxSelfAddresses {
		t.Fatalf("%d self addresses", got)
	}
}
