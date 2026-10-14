// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The upkeep merges the commitments recorded twice before board.commit
// returned the existing one, once: the mark in meta stops a second pass.
func TestBoardUpkeepMergesCommitmentsOnce(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, subject: "Magnet",
		text: "Could you help me with the magnet?"})
	mine := x.put(bmail{folder: x.sent, thread: "t_a", rfc: "a2", inReplyTo: "a1", from: boardMe, to: []api.Address{boardAlice},
		subject: "Re: Magnet", at: time.Hour, text: "I will help you with the magnet if you want."})
	x.drain()
	c, err := x.b.store.GetBoardCase(x.ctx, string(x.caseOf("t_a").ID))
	if err != nil {
		t.Fatal(err)
	}
	never := func(a, b string) bool { return false }
	for i, quote := range []string{"I will help you with the magnet", "I will help you with the magnet if you want", "help you with the​ magnet"} {
		if _, _, err := x.b.store.AddBoardCommitment(x.ctx, store.BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: mine,
			Text: "Help with the magnet", Quote: quote, Source: "claude", Now: time.Now().Add(time.Duration(i) * time.Minute), SameQuote: never}); err != nil {
			t.Fatal(err)
		}
	}
	x.b.dedupBoardCommitments(x.ctx)
	ks, err := x.b.store.BoardCommitments(x.ctx, c.ID)
	if err != nil || len(ks) != 1 || ks[0].Quote != "I will help you with the magnet" {
		t.Fatalf("after the upkeep: %+v %v", ks, err)
	}
	if v, _, _ := x.b.store.GetMeta(x.ctx, metaBoardCommitmentsDedup); v != "1" {
		t.Fatalf("mark: %q", v)
	}
	// Marked: a later duplicate (written past board.commit) stays.
	if _, _, err := x.b.store.AddBoardCommitment(x.ctx, store.BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: mine,
		Text: "x", Quote: "I will help you with the magnet", Source: "claude", Now: time.Now().Add(time.Hour), SameQuote: never}); err != nil {
		t.Fatal(err)
	}
	x.b.dedupBoardCommitments(x.ctx)
	if ks, _ := x.b.store.BoardCommitments(x.ctx, c.ID); len(ks) != 2 {
		t.Fatalf("a second pass ran: %+v", ks)
	}
}
