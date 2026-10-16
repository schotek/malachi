// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Calendar mail is no member of a board case (board.RulesVersion "8"): a
// message with a cal_invitations row neither makes a case, nor decides
// one, nor counts.

// boardInvite stores m's invitation row (as the Stored hook does, after
// the message).
func boardInvite(t *testing.T, s *Store, m *Message, method api.InvitationMethod) {
	t.Helper()
	putInvite(t, s, m, method, "uid-"+m.RFCMessageID, 0, boardNow.Add(48*time.Hour))
}

func TestBoardInvitationOnlyThreadMakesNoCase(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	inv := boardMail(t, s, inbox, 1, "inv", "")
	boardInvite(t, s, inv, api.InvitationMethodRequest)
	update := boardMail(t, s, inbox, 2, "upd", "inv", "inv")
	boardInvite(t, s, update, api.InvitationMethodRequest)
	sameThread(t, s, inv, update)

	called := false
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		called = true
		return testDecider(th)
	})
	if called {
		t.Error("the rules were asked about a thread of invitations only")
	}
	if _, err := s.BoardCaseByThread(context.Background(), "acc", inv.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("case of an invitation: %v", err)
	}
}

func TestBoardInvitationReplyInSentIsNoMember(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)

	a := boardMail(t, s, inbox, 1, "a", "")
	drainAll(t, s, boardNow, testDecider)
	if c := caseOf(t, s, "acc", a.ThreadID); c.RuleState != api.BoardYou || c.MessageCount != 1 {
		t.Fatalf("before the reply: %+v", c)
	}
	// The user's iMIP reply (Accepted: …) in Sent, in the same thread.
	reply := boardMail(t, s, sent, 2, "acc-reply", "a", "a")
	boardInvite(t, s, reply, api.InvitationMethodReply)
	sameThread(t, s, a, reply)
	var seen []BoardMember
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		seen = th.Members
		return testDecider(th)
	})
	for _, m := range seen {
		if m.ID == reply.ID {
			t.Errorf("the invitation reply is a member: %+v", m)
		}
	}
	c := caseOf(t, s, "acc", a.ThreadID)
	if c.RuleState != api.BoardYou || c.MessageCount != 1 || c.LatestMessageID != a.ID {
		t.Fatalf("after the invitation reply: %+v", c)
	}
	msgs, err := s.BoardMessages(ctx, c.ID, 0)
	if err != nil || len(msgs) != 1 || msgs[0].ID != a.ID {
		t.Fatalf("board.get members: %+v %v", msgs, err)
	}
	if counts, mine, err := s.BoardMemberOf(ctx, c.ID, reply.ID); err != nil || counts || mine {
		t.Errorf("the invitation reply is a member of the case: %v %v %v", counts, mine, err)
	}
}

func TestBoardMailPlusInvitationCountsTheMail(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)

	a := boardMail(t, s, inbox, 1, "a", "")
	mine := boardMail(t, s, sent, 2, "r", "a", "a")
	b := boardMail(t, s, inbox, 3, "b", "r", "a", "r")
	inv := boardMail(t, s, inbox, 4, "inv", "b", "a", "r", "b") // the newest: an invitation
	boardInvite(t, s, inv, api.InvitationMethodRequest)
	cancel := boardMail(t, s, inbox, 5, "cancel", "inv", "a", "r", "b", "inv")
	boardInvite(t, s, cancel, api.InvitationMethodCancel)
	sameThread(t, s, a, mine, b, inv, cancel)

	var v BoardVerdict
	drainAll(t, s, boardNow, func(th *BoardThread) (BoardVerdict, error) {
		var err error
		v, err = testDecider(th)
		return v, err
	})
	c := caseOf(t, s, "acc", a.ThreadID)
	if c.MessageCount != 3 || c.LatestMessageID != b.ID || v.DecidingMessageID != b.ID || c.RuleState != api.BoardYou {
		t.Fatalf("mail plus invitations: count %d latest %s deciding %s state %s, want 3 %s %s you",
			c.MessageCount, c.LatestMessageID, v.DecidingMessageID, c.RuleState, b.ID, b.ID)
	}
}

// An invitation row written after the board judged its message as mail
// takes it off once the thread is marked dirty again
// (MarkBoardMessagesDirty), and the case of a thread of nothing else
// leaves the board.
func TestBoardInvitationWrittenLater(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	inv := boardMail(t, s, inbox, 1, "inv", "")
	other := boardMail(t, s, inbox, 2, "other", "")
	drainAll(t, s, boardNow, testDecider)
	if c := caseOf(t, s, "acc", inv.ThreadID); c.MessageCount != 1 || !c.OrphanedAt.IsZero() {
		t.Fatalf("judged as mail: %+v", c)
	}
	boardInvite(t, s, inv, api.InvitationMethodRequest)
	expectDirty(t, s, "an invitation row alone") // nothing marks it by itself
	if err := s.MarkBoardMessagesDirty(ctx, []string{inv.ID, "m_unknown"}); err != nil {
		t.Fatal(err)
	}
	expectDirty(t, s, "marked", "acc/"+inv.ThreadID)
	if err := s.MarkBoardMessagesDirty(ctx, []string{inv.ID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, s, boardNow, testDecider)
	if c := caseOf(t, s, "acc", inv.ThreadID); c.OrphanedAt.IsZero() {
		t.Fatalf("the case of an invitation stays on the board: %+v", c)
	}
	if c := caseOf(t, s, "acc", other.ThreadID); !c.OrphanedAt.IsZero() || c.MessageCount != 1 {
		t.Fatalf("an ordinary case changed: %+v", c)
	}
}
