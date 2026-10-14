// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The store's fixes after the review of 2026-10-08 (H1).

// A done case that new mail reopens opens again the commitments done
// closed, as setDone false, a remind and a merge do (H1-4).
func TestBoardReopenByMailReopensCommitments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mine1, _, c, _ := commitmentsThreadIn(t, s)
	k, _, err := s.AddBoardCommitment(ctx, commitIn(c, mine1, "send it", "I will send it"))
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.SetBoardDone(ctx, c.ID, true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetBoardCommitment(ctx, k.ID); err != nil || got.State != api.CommitmentClosed ||
		got.ClosedReason != api.CommitmentClosedDone {
		t.Fatalf("done did not close it: %+v %v", got, err)
	}
	inbox, err := s.FolderByRole(ctx, "acc", api.RoleInbox)
	if err != nil {
		t.Fatal(err)
	}
	var firstID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM messages WHERE rfc_message_id = 'a'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetMessage(ctx, "acc", firstID)
	if err != nil {
		t.Fatal(err)
	}
	b := seedThread(t, s, inbox, Message{UID: 9, RFCMessageID: "new", InReplyTo: "r2", References: []string{"a", "r2"},
		Bulk: "none", Date: time.Now().UTC()})
	sameThread(t, s, &first, b)
	storedAfter(t, s, done.DoneAt, b)
	drainAll(t, s, boardNow, seenDecider)
	got := caseOf(t, s, "acc", c.ThreadID)
	if !got.DoneAt.IsZero() {
		t.Fatalf("not reopened: %+v", got)
	}
	if k2, err := s.GetBoardCommitment(ctx, k.ID); err != nil || k2.State != api.CommitmentOpen || k2.ClosedReason != "" {
		t.Fatalf("commitment after the reopen: %+v %v", k2, err)
	}
}

// A thread merge whose reopening of the commitments fails still merges
// the threads: the board's data never fails a mail write (H1-10).
func TestBoardMergeCarriesOnPastAReopenFailure(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	var logged bytes.Buffer
	prev := boardMergeLog
	boardMergeLog = func() *slog.Logger { return slog.New(slog.NewTextHandler(&logged, nil)) }
	t.Cleanup(func() { boardMergeLog = prev })
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	x, xm, cx := boardThreadWithMine(t, s, inbox, sent, 10, "x")
	y, _, _ := boardThreadWithMine(t, s, inbox, sent, 20, "y")
	k, _, err := s.AddBoardCommitment(ctx, BoardCommitmentInput{CaseID: cx.ID, InputKey: cx.InputKey, MessageID: xm.ID, Text: "x",
		Quote: "qx", Run: BoardRunRef{Source: "t", Day: "2026-09-04"}, Now: boardNow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBoardDone(ctx, cx.ID, true, boardNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER refuse_reopen BEFORE UPDATE OF state ON board_commitments
		WHEN OLD.state = 'closed' AND NEW.state = 'open' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	both := boardMail(t, s, inbox, 30, "both", "x-re", "y", "y-re", "x", "x-re")
	sameThread(t, s, x, y, both) // the mail write went through
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER refuse_reopen`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetBoardCommitment(ctx, k.ID); err != nil || got.State != api.CommitmentClosed {
		t.Fatalf("commitment: %+v %v", got, err)
	}
	if !strings.Contains(logged.String(), "reopen the commitments done closed") {
		t.Fatalf("not logged: %q", logged.String())
	}
}

// A member row that does not decode does not fail board.remind or
// board.done: the marker names every inbound member that counts (H1-10).
func TestBoardRemindAndDoneWithUndecodableMember(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, _, c, _ := commitmentsThreadIn(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET to_json = '"not a list"', attachments_json = '{"no": "list"}' WHERE rfc_message_id = 'a'`); err != nil {
		t.Fatal(err)
	}
	r, err := s.SetBoardRemind(ctx, c.ID, boardNow.Add(24*time.Hour))
	if err != nil || r.RemindSetAt().IsZero() || !r.SeenAtDone("a") || !r.SeenAtDone("a2") || r.SeenAtDone("r1") {
		t.Fatalf("remind: %+v %v", r, err)
	}
	d, err := s.SetBoardDone(ctx, c.ID, true, boardNow)
	if err != nil || d.DoneAt.IsZero() || !d.SeenAtDone("a") || !d.SeenAtDone("a2") {
		t.Fatalf("done: %+v %v", d, err)
	}
}
