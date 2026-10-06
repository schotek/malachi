// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/pkg/api"
)

// A commitment is its case, its message and its quote: recording it again
// (in other words, with a shorter or longer span of the same sentence,
// with invisible or typographic characters) returns the one recorded.

// commitmentsThread stores a thread with two messages of the user's and
// returns them with the case.
func commitmentsThread(t *testing.T, s *Store) (*Message, *Message, BoardCase) {
	mine1, mine2, c, _ := commitmentsThreadIn(t, s)
	return mine1, mine2, c
}

func commitmentsThreadIn(t *testing.T, s *Store) (*Message, *Message, BoardCase, Folder) {
	t.Helper()
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	in := boardMail(t, s, inbox, 1, "a", "")
	mine1 := boardMail(t, s, sent, 2, "r1", "a", "a")
	in2 := boardMail(t, s, inbox, 3, "a2", "r1", "a", "r1")
	mine2 := boardMail(t, s, sent, 4, "r2", "a2", "a", "a2")
	tid := sameThread(t, s, in, mine1, in2, mine2)
	drainAll(t, s, boardNow, testDecider)
	return mine1, mine2, caseOf(t, s, "acc", tid), sent
}

func commitIn(c BoardCase, msg *Message, text, quote string) BoardCommitmentInput {
	return BoardCommitmentInput{CaseID: c.ID, InputKey: c.InputKey, MessageID: msg.ID, Text: text, Quote: quote,
		Source: "t", Run: BoardRunRef{Source: "t", Day: "2026-09-04"}, Now: boardNow, SameQuote: board.SameCommitment}
}

func countCommitments(t *testing.T, s *Store, caseID string) []BoardCommitment {
	t.Helper()
	ks, err := s.BoardCommitments(context.Background(), caseID)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestBoardCommitmentRecordedOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, mine2, c := commitmentsThread(t, s)

	const quote = "S magnetkou ti pomůžu, jestli budeš chtít"
	first, runID, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "Pomoci s magnetkou ke keši.", quote))
	if err != nil || first.Existing || runID == "" {
		t.Fatalf("first: %+v %q %v", first, runID, err)
	}
	for _, again := range []struct{ name, text, quote string }{
		{"same quote", "Pomoci s magnetkou, pokud o to bude zájem.", quote},
		{"shorter span", "Pomoci s magnetkou.", "S magnetkou ti pomůžu"},
		{"longer span", "Pomoci s magnetkou, pokud o to bude mít zájem.", quote + ", stačí napsat."},
		{"invisible characters", "x", "S mag\u200bnetkou ti\u00a0pomůžu,\u2060 jestli budeš chtít"},
		{"decomposed", "x", "S magnetkou ti pomu\u030ažu, jestli budeš chtít"},
	} {
		k, runID, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, again.text, again.quote))
		if err != nil || !k.Existing || k.ID != first.ID || k.Text != first.Text || runID != "" {
			t.Fatalf("%s: %+v %q %v", again.name, k, runID, err)
		}
	}
	if ks := countCommitments(t, s, c.ID); len(ks) != 1 {
		t.Fatalf("rows: %+v", ks)
	}
	run, err := s.GetBoardRun(ctx, first.RunID)
	if err != nil || run.Commitments != 1 {
		t.Fatalf("counted: %+v %v", run, err)
	}

	// A deadline the existing one lacked is taken; one it has stays.
	due := boardNow.Add(48 * time.Hour)
	in := commitIn(c, mine1, "x", quote)
	in.Due = due
	k, _, err := s.AddBoardCommitment(ctx, in)
	if err != nil || !k.Existing || !k.Due.Equal(due) {
		t.Fatalf("due taken: %+v %v", k, err)
	}
	if after := caseOf(t, s, "acc", c.ThreadID); after.Version != c.Version+1 {
		t.Fatalf("version after the deadline: %d, was %d", after.Version, c.Version)
	}
	in.Due = due.Add(24 * time.Hour)
	if k, _, err := s.AddBoardCommitment(ctx, in); err != nil || !k.Existing || !k.Due.Equal(due) {
		t.Fatalf("due kept: %+v %v", k, err)
	}

	// Another promise on the same message, and the same quote on another
	// message, are other commitments.
	other, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "Poslat fotky.", "Fotky pošlu v \"pondělí\""))
	if err != nil || other.Existing {
		t.Fatalf("other promise: %+v %v", other, err)
	}
	// Typographic quotation marks are the ASCII ones.
	if k, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "x", "Fotky pošlu v \u201epondělí\u201c")); err != nil || !k.Existing || k.ID != other.ID {
		t.Fatalf("typographic quotes: %+v %v", k, err)
	}
	second, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine2, "Pomoci s magnetkou.", quote))
	if err != nil || second.Existing || second.ID == first.ID {
		t.Fatalf("other message: %+v %v", second, err)
	}
	if ks := countCommitments(t, s, c.ID); len(ks) != 3 {
		t.Fatalf("rows: %+v", ks)
	}
}

// A closed commitment (the user replied after it) blocks the same promise
// on the same message; on a newer message it is a new one.
func TestBoardCommitmentClosedBlocksSameMessage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, _, c, sent := commitmentsThreadIn(t, s)
	const quote = "I will send the slides tomorrow"
	k, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "send the slides", quote))
	if err != nil {
		t.Fatal(err)
	}
	mine3 := boardMail(t, s, sent, 5, "r3", "r2", "a", "r2")
	sameThread(t, s, mine1, mine3)
	drainAll(t, s, boardNow, testDecider)
	if got, _ := s.GetBoardCommitment(ctx, k.ID); got.State != api.CommitmentClosed {
		t.Fatalf("not closed: %+v", got)
	}
	c = caseOf(t, s, "acc", c.ThreadID)
	again, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "send the slides", quote))
	if err != nil || !again.Existing || again.ID != k.ID || again.State != api.CommitmentClosed {
		t.Fatalf("same message after closed: %+v %v", again, err)
	}
	fresh, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine3, "send the slides", quote))
	if err != nil || fresh.Existing || fresh.State != api.CommitmentOpen {
		t.Fatalf("newer message: %+v %v", fresh, err)
	}
	if ks := countCommitments(t, s, c.ID); len(ks) != 2 {
		t.Fatalf("rows: %+v", ks)
	}
}

// The commitments recorded twice before the rule are merged once: the
// oldest of a group stays, done when any of it is, and a second pass
// changes nothing.
func TestBoardDedupCommitments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, mine2, c := commitmentsThread(t, s)
	never := func(a, b string) bool { return false }
	add := func(msg *Message, text, quote string, at time.Time) BoardCommitment {
		t.Helper()
		in := commitIn(c, msg, text, quote)
		in.SameQuote, in.Now = never, at
		k, _, err := s.AddBoardCommitment(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	const quote = "S magnetkou ti pomůžu, jestli budeš chtít"
	oldest := add(mine1, "Pomoci s magnetkou ke keši.", quote, boardNow)
	dupA := add(mine1, "Pomoci s magnetkou, pokud o to bude zájem.", "S magnetkou ti pomůžu", boardNow.Add(time.Hour))
	dupB := add(mine1, "Pomoci s magnetkou, pokud o to bude mít zájem.", "S magnetkou ti\u200b pomůžu, jestli\u00a0budeš chtít", boardNow.Add(2*time.Hour))
	other := add(mine1, "Poslat fotky.", "Fotky pošlu v pondělí", boardNow.Add(3*time.Hour))
	elsewhere := add(mine2, "Pomoci s magnetkou.", quote, boardNow.Add(4*time.Hour))
	if _, err := s.SetBoardCommitment(ctx, dupB.ID, true, boardNow.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	before := caseOf(t, s, "acc", c.ThreadID)

	changed, err := s.DedupBoardCommitments(ctx, board.SameCommitment)
	if err != nil || len(changed["acc"]) != 1 || changed["acc"][0] != c.ThreadID {
		t.Fatalf("changed: %v %v", changed, err)
	}
	ks := countCommitments(t, s, c.ID)
	ids := map[string]BoardCommitment{}
	for _, k := range ks {
		ids[k.ID] = k
	}
	if len(ks) != 3 || ids[dupA.ID].ID != "" || ids[dupB.ID].ID != "" || ids[other.ID].ID == "" || ids[elsewhere.ID].ID == "" {
		t.Fatalf("after the merge: %+v", ks)
	}
	kept := ids[oldest.ID]
	if kept.Text != oldest.Text || kept.State != api.CommitmentDone || !kept.ClosedAt.Equal(boardNow.Add(5*time.Hour)) {
		t.Fatalf("kept: %+v", kept)
	}
	if after := caseOf(t, s, "acc", c.ThreadID); after.Version <= before.Version {
		t.Fatalf("version %d, was %d", after.Version, before.Version)
	}

	again, err := s.DedupBoardCommitments(ctx, board.SameCommitment)
	if err != nil || len(again) != 0 || len(countCommitments(t, s, c.ID)) != 3 {
		t.Fatalf("second pass: %v %v", again, err)
	}
}
