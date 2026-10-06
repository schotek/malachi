// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A reply of the user's sent only to an address of another of the user's
// accounts is a note to self: the correspondent's mail before it stays a
// case, end to end, once that address is known to be the user's.
func TestBoardNoteToSelf(t *testing.T) {
	x := newBoardBox(t)
	home := api.Address{Name: "Me at home", Address: "Me.Home@example.invalid"}
	// The user wrote to Bob before (he is a known sender).
	x.put(bmail{folder: x.sent, thread: "t_known", rfc: "k1", from: boardMe, to: []api.Address{boardBob}, subject: "Hello", at: -72 * time.Hour, text: "Hello."})
	// Bob writes twice; the user's reply goes only to their home address.
	x.put(bmail{folder: x.inbox, thread: "t_note", rfc: "n1", from: boardBob, to: []api.Address{boardMe}, subject: "Quote", at: -48 * time.Hour, text: "Our quote"})
	bob := x.put(bmail{folder: x.inbox, thread: "t_note", rfc: "n2", inReplyTo: "n1", from: boardBob, to: []api.Address{boardMe}, subject: "Re: Quote", text: "Any news?"})
	x.put(bmail{folder: x.sent, thread: "t_note", rfc: "n3", inReplyTo: "n2", from: boardMe, to: []api.Address{home}, subject: "Re: Quote", at: time.Hour, text: "Answer Bob on Monday?"})
	// Mixed recipients are no note: own Bcc to the home address, but Bob
	// in Cc; the reply's To names no sender of the thread, as today.
	x.put(bmail{folder: x.inbox, thread: "t_mixed", rfc: "m1", from: boardBob, to: []api.Address{boardMe}, subject: "Visit", text: "Visit?"})
	x.put(bmail{folder: x.sent, thread: "t_mixed", rfc: "m2", inReplyTo: "m1", from: boardMe, to: []api.Address{home}, cc: []api.Address{boardBob},
		subject: "Re: Visit", at: time.Hour, text: "Yes."})
	// The home address is not the user's yet: today's rule, no case.
	x.drain()
	x.noCase("t_note")
	x.noCase("t_mixed")

	// The other account appears: its address is the user's, the threads
	// are judged again.
	seedAccount(t, x.b, home.Address)
	x.drain()
	c := x.caseOf("t_note")
	if c.RuleState != api.BoardYou || c.RuleReason != api.BoardReasonYouAddressed {
		t.Fatalf("note to self: %s %s", c.RuleState, c.RuleReason)
	}
	if c.MessageCount != 3 || c.LatestMessageID != api.MessageID(bob) || c.ReplyMessageID != api.MessageID(bob) ||
		!c.Date.Equal(x.base) || c.Person.Address != boardBob.Address || c.Snippet != "Any news?" || c.Subject != "Quote" {
		t.Fatalf("note to self case: %+v", c)
	}
	x.noCase("t_mixed")

	// A note to self after a reply to Bob: the reply decides.
	x.put(bmail{folder: x.inbox, thread: "t_after", rfc: "a1", from: boardBob, to: []api.Address{boardMe}, subject: "Plan", text: "Plan?"})
	x.put(bmail{folder: x.sent, thread: "t_after", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardBob}, subject: "Re: Plan", at: time.Hour, text: "Looks good."})
	x.put(bmail{folder: x.sent, thread: "t_after", rfc: "a3", inReplyTo: "a2", from: boardMe, bcc: []api.Address{home}, subject: "Re: Plan", at: 2 * time.Hour, text: "Remind me?"})
	// Nothing but notes: no case.
	x.put(bmail{folder: x.sent, thread: "t_self", rfc: "s1", from: boardMe, to: []api.Address{home, boardMe}, subject: "Todo", text: "Buy milk?"})
	x.drain()
	if c := x.caseOf("t_after"); c.RuleReason != api.BoardReasonThemReplied || !c.Date.Equal(x.base.Add(time.Hour)) || c.MessageCount != 3 {
		t.Fatalf("reply then note: %s %v %d", c.RuleReason, c.Date, c.MessageCount)
	}
	x.noCase("t_self")
}
