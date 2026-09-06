// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestFlagMessagesAtomic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	foreignFolder := seedFolder(t, s, "other", "INBOX", api.RoleInbox)
	a := seedMessage(t, s, inbox, 1, "a", time.Now(), api.FlagSeen)
	b := seedMessage(t, s, inbox, 2, "b", time.Now())
	foreign := seedMessage(t, s, foreignFolder, 1, "f", time.Now())

	// One unknown id → nothing changes, no op queued.
	err := s.FlagMessages(ctx, "acc", []string{a.ID, "m_nope"}, []api.Flag{api.FlagFlagged}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	err = s.FlagMessages(ctx, "acc", []string{a.ID, foreign.ID}, []api.Flag{api.FlagFlagged}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign id: %v", err)
	}
	if got, _ := s.GetMessage(ctx, "acc", a.ID); fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagSeen}) {
		t.Errorf("partial change: %v", got.Flags)
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 0 {
		t.Errorf("ops after failure: %d", n)
	}

	if err := s.FlagMessages(ctx, "acc", []string{a.ID, b.ID, a.ID}, []api.Flag{api.FlagFlagged, api.FlagSeen}, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Message{a, b} {
		got, _ := s.GetMessage(ctx, "acc", m.ID)
		if fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagFlagged}) {
			t.Errorf("%s flags = %v", m.Subject, got.Flags)
		}
	}
	f, _ := s.GetFolder(ctx, "acc", inbox.ID)
	if f.Unread != 2 || f.Total != 2 {
		t.Errorf("recount: %d/%d", f.Unread, f.Total)
	}
	ops, err := s.NextOps(ctx, "acc", time.Now(), 10)
	if err != nil || len(ops) != 2 {
		t.Fatalf("ops = %+v %v", ops, err)
	}
	for i, op := range ops {
		want := []*Message{a, b}[i]
		if op.Kind != OpFlag || op.MessageID != want.ID || op.FolderID != inbox.ID || op.UID != want.UID ||
			op.AccountID != "acc" || fmt.Sprint(op.Set) != fmt.Sprint([]api.Flag{api.FlagFlagged, api.FlagSeen}) ||
			fmt.Sprint(op.Clear) != fmt.Sprint([]api.Flag{api.FlagSeen}) || op.Attempts != 0 || !op.NextAttemptAt.IsZero() ||
			op.CreatedAt.IsZero() || op.TargetFolderID != "" {
			t.Errorf("op[%d] = %+v", i, op)
		}
	}
	if ops, _ := s.NextOps(ctx, "other", time.Now(), 10); len(ops) != 0 {
		t.Errorf("other account sees ops: %+v", ops)
	}
}

func TestMoveMessagesSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	foreignFolder := seedFolder(t, s, "other", "Archive", api.RoleArchive)
	a := seedMessage(t, s, inbox, 1, "a", time.Now())
	b := seedMessage(t, s, archive, 9, "b", time.Now(), api.FlagSeen)

	if err := s.MoveMessages(ctx, "acc", []string{a.ID}, foreignFolder.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target: %v", err)
	}
	if err := s.MoveMessages(ctx, "acc", []string{a.ID, "m_nope"}, archive.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if got, _ := s.GetMessage(ctx, "acc", a.ID); got.FolderID != inbox.ID || got.UID != 1 {
		t.Fatalf("moved despite failure: %+v", got)
	}

	if err := s.MoveMessages(ctx, "acc", []string{a.ID, b.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMessage(ctx, "acc", a.ID)
	if got.FolderID != archive.ID || got.UID != 0 || got.ModSeq != 0 || got.ID != a.ID {
		t.Errorf("after move: %+v", got)
	}
	if got, _ := s.GetMessage(ctx, "acc", b.ID); got.UID != 9 {
		t.Errorf("already-there message touched: %+v", got)
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 1 || ops[0].Kind != OpMove || ops[0].FolderID != inbox.ID || ops[0].UID != 1 || ops[0].TargetFolderID != archive.ID {
		t.Errorf("ops = %+v", ops)
	}
	in, _ := s.GetFolder(ctx, "acc", inbox.ID)
	ar, _ := s.GetFolder(ctx, "acc", archive.ID)
	if in.Total != 0 || in.Unread != 0 || ar.Total != 2 || ar.Unread != 1 {
		t.Errorf("recount: inbox %d/%d archive %d/%d", in.Unread, in.Total, ar.Unread, ar.Total)
	}
	if fileExists(t, s.MessageRawPath("acc", a.ID)) != true {
		t.Error("raw file lost on move")
	}
	if uids, _ := s.ListUIDs(ctx, archive.ID); len(uids) != 1 || uids[0] != 9 {
		t.Errorf("pending row listed: %v", uids)
	}
}

func TestTrashAndDeleteMessages(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	a := seedMessage(t, s, inbox, 1, "a", time.Now())
	inTrash := seedMessage(t, s, trash, 4, "t", time.Now())
	keep := seedMessage(t, s, inbox, 2, "keep", time.Now(), api.FlagSeen)

	if err := s.TrashMessages(ctx, "acc", []string{a.ID, "m_nope"}, trash.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := s.TrashMessages(ctx, "acc", []string{a.ID}, "f_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown trash: %v", err)
	}
	if err := s.TrashMessages(ctx, "acc", []string{a.ID, inTrash.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "acc", a.ID); got.FolderID != trash.ID || got.UID != 0 {
		t.Errorf("not moved to trash: %+v", got)
	}
	if _, err := s.GetMessage(ctx, "acc", inTrash.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("trashed twice still present: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", inTrash.ID)) {
		t.Error("file of permanently deleted message kept")
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 2 || ops[0].Kind != OpMove || ops[0].MessageID != a.ID || ops[0].FolderID != inbox.ID || ops[0].UID != 1 ||
		ops[1].Kind != OpDelete || ops[1].MessageID != inTrash.ID || ops[1].FolderID != trash.ID || ops[1].UID != 4 {
		t.Errorf("ops = %+v", ops)
	}
	tr, _ := s.GetFolder(ctx, "acc", trash.ID)
	in, _ := s.GetFolder(ctx, "acc", inbox.ID)
	if tr.Total != 1 || tr.Unread != 1 || in.Total != 1 || in.Unread != 0 {
		t.Errorf("recount: trash %d/%d inbox %d/%d", tr.Unread, tr.Total, in.Unread, in.Total)
	}

	// Permanent delete: row gone now, file gone, op queued with snapshot.
	if err := s.DeleteMessages(ctx, "acc", []string{keep.ID, "m_nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := s.GetMessage(ctx, "acc", keep.ID); err != nil {
		t.Fatalf("deleted despite failure: %v", err)
	}
	if err := s.DeleteMessages(ctx, "acc", []string{keep.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", keep.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("row kept: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", keep.ID)) {
		t.Error("file kept")
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 3 || ops[2].Kind != OpDelete || ops[2].MessageID != keep.ID || ops[2].FolderID != inbox.ID || ops[2].UID != 2 {
		t.Errorf("ops = %+v", ops)
	}
	if in, _ := s.GetFolder(ctx, "acc", inbox.ID); in.Total != 0 {
		t.Errorf("inbox total = %d", in.Total)
	}
	// Ops for a permanently deleted message are not dropped by AssignUID
	// or the like; they still count as pending.
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 3 {
		t.Errorf("pending = %d", n)
	}
}

func TestNextOpsOrderBackoffAndDrop(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	a := seedMessage(t, s, inbox, 1, "a", time.Now())
	b := seedMessage(t, s, inbox, 2, "b", time.Now())
	c := seedMessage(t, s, inbox, 3, "c", time.Now())
	for _, m := range []*Message{c, a, b} {
		if err := s.FlagMessages(ctx, "acc", []string{m.ID}, []api.Flag{api.FlagSeen}, nil); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	ops, err := s.NextOps(ctx, "acc", now, 2)
	if err != nil || len(ops) != 2 || ops[0].MessageID != c.ID || ops[1].MessageID != a.ID || ops[0].ID >= ops[1].ID {
		t.Fatalf("order/limit: %+v %v", ops, err)
	}
	all, _ := s.NextOps(ctx, "acc", now, 0)
	if len(all) != 3 {
		t.Fatalf("limit 0 = %d", len(all))
	}

	// Failure: attempts++, retry time honoured, error kept.
	retry := now.Add(time.Minute)
	if err := s.MarkOpFailed(ctx, all[0].ID, "NO [CANNOT] nope", retry); err != nil {
		t.Fatal(err)
	}
	due, _ := s.NextOps(ctx, "acc", now, 10)
	if len(due) != 2 || due[0].MessageID != a.ID {
		t.Errorf("failed op still due: %+v", due)
	}
	later, _ := s.NextOps(ctx, "acc", retry, 10)
	if len(later) != 3 || later[0].Attempts != 1 || later[0].LastError != "NO [CANNOT] nope" || !later[0].NextAttemptAt.Equal(retry.UTC().Truncate(time.Millisecond)) {
		t.Errorf("after retry time: %+v", later)
	}
	if err := s.MarkOpFailed(ctx, all[0].ID, "again", retry.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.NextOps(ctx, "acc", retry.Add(time.Hour), 10); again[0].Attempts != 2 {
		t.Errorf("attempts = %d", again[0].Attempts)
	}

	if err := s.MarkOpDone(ctx, all[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DropOp(ctx, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 1 {
		t.Errorf("pending = %d", n)
	}
	rest, _ := s.NextOps(ctx, "acc", now, 10)
	if len(rest) != 1 || rest[0].MessageID != b.ID {
		t.Errorf("rest = %+v", rest)
	}
	for _, err := range []error{s.MarkOpDone(ctx, all[1].ID), s.DropOp(ctx, all[0].ID), s.MarkOpFailed(ctx, 9999, "x", now)} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown op: %v", err)
		}
	}
	if n, _ := s.CountPendingOps(ctx, "nobody"); n != 0 {
		t.Errorf("count for unknown account = %d", n)
	}
}

func TestDeleteAccountRemovesMailCache(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	acc := Account{Name: "Work", Enabled: true, Config: testAccountConfig("me@example.invalid")}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	inbox := seedFolder(t, s, acc.ID, "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "a", time.Now())
	if err := s.FlagMessages(ctx, acc.ID, []string{m.ID}, []api.Flag{api.FlagSeen}, nil); err != nil {
		t.Fatal(err)
	}
	d := Draft{AccountID: acc.ID, Subject: "keep"}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	otherFolder := seedFolder(t, s, "other", "INBOX", api.RoleInbox)
	otherMsg := seedMessage(t, s, otherFolder, 1, "o", time.Now())

	// Even without deleteLocalData the mail cache goes; drafts stay.
	if err := s.DeleteAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListFolders(ctx, acc.ID); len(list) != 0 {
		t.Errorf("folders left: %+v", list)
	}
	if _, err := s.GetMessage(ctx, acc.ID, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("message left: %v", err)
	}
	if n, _ := s.CountPendingOps(ctx, acc.ID); n != 0 {
		t.Errorf("ops left: %d", n)
	}
	if _, err := os.Stat(filepath.Join(s.MessageDir(), acc.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("message directory left: %v", err)
	}
	if _, _, total, _ := s.ListDrafts(ctx, acc.ID, "", 10); total != 1 {
		t.Errorf("drafts = %d, want 1 (kept without deleteLocalData)", total)
	}
	if _, err := s.GetMessage(ctx, "other", otherMsg.ID); err != nil {
		t.Errorf("other account's message: %v", err)
	}
	if !fileExists(t, s.MessageRawPath("other", otherMsg.ID)) {
		t.Error("other account's file removed")
	}
}

// A move into a folder that is never downloaded (Gmail's All Mail) queues
// the server move and drops the local copy, file included.
func TestMoveMessagesIntoUnsyncedFolder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	stored, _, err := s.UpsertFolders(ctx, "acc", []Folder{
		{Mailbox: "INBOX", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Subscribed: true},
		{Mailbox: "[Gmail]/All Mail", Name: "All Mail", Path: "[Gmail]/All Mail", Role: api.RoleArchive, Selectable: true, Subscribed: true, Unsynced: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox, all := stored[0], stored[1]
	if got, _ := s.GetFolder(ctx, "acc", all.ID); !got.Unsynced || got.Role != api.RoleArchive {
		t.Fatalf("all mail stored as %+v", got)
	}
	if got, _ := s.GetFolder(ctx, "acc", inbox.ID); got.Unsynced {
		t.Fatalf("inbox unsynced: %+v", got)
	}
	a := seedMessage(t, s, inbox, 1, "a", time.Now())
	keep := seedMessage(t, s, inbox, 2, "keep", time.Now(), api.FlagSeen)

	if err := s.MoveMessages(ctx, "acc", []string{a.ID}, all.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row after archiving: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", a.ID)) {
		t.Error("raw file kept after archiving")
	}
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 1 || ops[0].Kind != OpMove || ops[0].FolderID != inbox.ID || ops[0].UID != 1 || ops[0].TargetFolderID != all.ID {
		t.Errorf("ops = %+v", ops)
	}
	in, _ := s.GetFolder(ctx, "acc", inbox.ID)
	allF, _ := s.GetFolder(ctx, "acc", all.ID)
	if in.Total != 1 || allF.Total != 0 {
		t.Errorf("counts: inbox %d, all mail %d", in.Total, allF.Total)
	}
	if got, _ := s.GetMessage(ctx, "acc", keep.ID); got.FolderID != inbox.ID || got.UID != 2 {
		t.Errorf("other message touched: %+v", got)
	}
}
