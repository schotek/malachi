// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// seedRemote inserts one message addressed by a remote id (no UID).
func seedRemote(t *testing.T, s *Store, f Folder, remoteID, subject string, received time.Time, flags ...api.Flag) *Message {
	t.Helper()
	m := &Message{
		AccountID: f.AccountID, FolderID: f.ID, RemoteID: remoteID, Flags: flags,
		From:    []api.Address{{Name: "Alice", Address: "alice@example.invalid"}},
		Subject: subject, Date: received, InternalDate: received, RFCMessageID: "<" + subject + "@example.invalid>", Size: 100,
		ThreadID: "conv-" + subject,
	}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteMessageRaw(context.Background(), f.AccountID, m.ID, strings.NewReader("Subject: "+subject+"\r\n\r\nbody"), 1<<20); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpsertMessagesByRemoteID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "AAMkInbox", api.RoleInbox)
	day := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	m := seedRemote(t, s, inbox, "AAkA1", "one", day)
	if !strings.HasPrefix(m.ID, "m_") || m.UID != 0 {
		t.Fatalf("insert: %+v", m)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil || got.RemoteID != "AAkA1" || got.ThreadID != "conv-one" {
		t.Fatalf("round trip: %+v, %v", got, err)
	}

	// Same (folder, remote id): flags and thread id change, the id is kept.
	dup := &Message{AccountID: "acc", FolderID: inbox.ID, RemoteID: "AAkA1", Flags: []api.Flag{api.FlagSeen}, Subject: "changed", ThreadID: "conv-x"}
	if err := s.UpsertMessages(ctx, []*Message{dup}); err != nil {
		t.Fatal(err)
	}
	if dup.ID != m.ID {
		t.Fatalf("conflict id = %s, want %s", dup.ID, m.ID)
	}
	got, _ = s.GetMessage(ctx, "acc", m.ID)
	if got.Subject != "one" || fmt.Sprint(got.Flags) != fmt.Sprint([]api.Flag{api.FlagSeen}) || got.ThreadID != "conv-x" {
		t.Errorf("after conflict: %+v", got)
	}
	// An empty thread id in the batch keeps the stored one.
	keep := &Message{AccountID: "acc", FolderID: inbox.ID, RemoteID: "AAkA1"}
	if err := s.UpsertMessages(ctx, []*Message{keep}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetMessage(ctx, "acc", m.ID); got.ThreadID != "conv-x" {
		t.Errorf("thread id overwritten: %q", got.ThreadID)
	}

	// The same remote id in another folder is a different row (a copy);
	// IMAP rows with uid 0 and no remote id never collide.
	other := seedFolder(t, s, "acc", "AAMkArchive", api.RoleArchive)
	copyRow := &Message{AccountID: "acc", FolderID: other.ID, RemoteID: "AAkA1"}
	plain := &Message{AccountID: "acc", FolderID: inbox.ID}
	if err := s.UpsertMessages(ctx, []*Message{copyRow, plain}); err != nil {
		t.Fatal(err)
	}
	if copyRow.ID == m.ID || plain.ID == m.ID {
		t.Fatalf("rows collided: %s %s %s", m.ID, copyRow.ID, plain.ID)
	}

	// Unfetched listing includes remote rows, newest first, with the id.
	refs, err := s.ListUnfetched(ctx, inbox.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != m.ID || refs[0].RemoteID != "AAkA1" {
		t.Fatalf("unfetched = %+v", refs)
	}
}

func TestRemoteFlagsMoveAndDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "AAMkInbox", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "AAMkArchive", api.RoleArchive)
	day := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	m := seedRemote(t, s, inbox, "AAkA1", "one", day)
	old := seedRemote(t, s, inbox, "AAkA0", "old", day.AddDate(0, 0, -40), api.FlagSeen)

	changed, err := s.ApplyServerFlagsByRemoteID(ctx, inbox.ID, "AAkA1", []api.Flag{api.FlagSeen}, 0)
	if err != nil || !changed {
		t.Fatalf("apply flags: %v %v", changed, err)
	}
	if _, err := s.ApplyServerFlagsByRemoteID(ctx, inbox.ID, "nope", nil, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown remote id: %v", err)
	}
	// A pending local flag change wins.
	if err := s.FlagMessages(ctx, "acc", []string{m.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	if changed, _ := s.ApplyServerFlagsByRemoteID(ctx, inbox.ID, "AAkA1", []api.Flag{api.FlagSeen}, 0); changed {
		t.Fatal("server flags applied over a pending local change")
	}
	ops, err := s.NextOps(ctx, "acc", time.Now(), 10)
	if err != nil || len(ops) != 1 || ops[0].RemoteID != "AAkA1" || ops[0].UID != 0 {
		t.Fatalf("ops = %+v, %v", ops, err)
	}
	if err := s.MarkOpDone(ctx, ops[0].ID); err != nil {
		t.Fatal(err)
	}

	// Retention: only the old one is past the cutoff.
	ids, err := s.ListRemoteIDsOlderThan(ctx, inbox.ID, day.AddDate(0, 0, -30))
	if err != nil || fmt.Sprint(ids) != "[AAkA0]" {
		t.Fatalf("older = %v, %v", ids, err)
	}
	if ids, _ := s.ListRemoteIDs(ctx, inbox.ID); fmt.Sprint(ids) != "[AAkA0 AAkA1]" {
		t.Fatalf("remote ids = %v", ids)
	}

	// A server-side move keeps the local id and recounts both folders.
	moved, err := s.MoveByRemoteID(ctx, "acc", "AAkA1", archive.ID)
	if err != nil || !moved {
		t.Fatalf("move: %v %v", moved, err)
	}
	if moved, _ := s.MoveByRemoteID(ctx, "acc", "AAkA1", archive.ID); moved {
		t.Fatal("second move reported as moved")
	}
	if moved, _ := s.MoveByRemoteID(ctx, "acc", "unknown", archive.ID); moved {
		t.Fatal("unknown remote id moved")
	}
	got, _ := s.GetMessage(ctx, "acc", m.ID)
	if got.FolderID != archive.ID || got.RemoteID != "AAkA1" {
		t.Fatalf("after move: %+v", got)
	}
	in, _ := s.GetFolder(ctx, "acc", inbox.ID)
	ar, _ := s.GetFolder(ctx, "acc", archive.ID)
	if in.Total != 1 || ar.Total != 1 {
		t.Fatalf("counts inbox=%d archive=%d", in.Total, ar.Total)
	}
	// A duplicate already synchronised in the target gives way.
	dupRow := seedRemote(t, s, inbox, "AAkA2", "two", day)
	early := &Message{AccountID: "acc", FolderID: archive.ID, RemoteID: "AAkA2"}
	if err := s.UpsertMessages(ctx, []*Message{early}); err != nil {
		t.Fatal(err)
	}
	// The original must be the older row; the stamps have millisecond
	// resolution and the two inserts may share one.
	if _, err := s.DB().ExecContext(ctx, `UPDATE messages SET updated_at = ? WHERE id = ?`, stamp(time.Now().Add(time.Second)), early.ID); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.MoveByRemoteID(ctx, "acc", "AAkA2", archive.ID); err != nil || !moved {
		t.Fatalf("move over duplicate: %v %v", moved, err)
	}
	if _, err := s.GetMessage(ctx, "acc", early.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate kept: %v", err)
	}
	if got, err := s.GetMessage(ctx, "acc", dupRow.ID); err != nil || got.FolderID != archive.ID {
		t.Fatalf("original after move: %+v, %v", got, err)
	}

	// Stale-pending cleanup never touches rows with a remote id.
	if n, err := s.DeleteStalePending(ctx, archive.ID, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("stale pending removed remote rows: %d, %v", n, err)
	}

	// A delete operation keeps the remote id after the row is gone.
	if err := s.DeleteMessages(ctx, "acc", []string{m.ID}); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.NextOps(ctx, "acc", time.Now(), 10)
	if len(ops) != 1 || ops[0].Kind != OpDelete || ops[0].RemoteID != "AAkA1" || ops[0].FolderID != archive.ID {
		t.Fatalf("delete op = %+v", ops)
	}

	// Server-side deletion by remote id removes rows, ops and files.
	raw := s.MessageRawPath("acc", old.ID)
	if err := s.DeleteMessagesByRemoteID(ctx, inbox.ID, []string{"AAkA0", "", "missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old row kept: %v", err)
	}
	if fileExists(t, raw) {
		t.Fatal("raw file kept")
	}
}

func TestFolderDeltaLink(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "AAMkInbox", api.RoleInbox)
	link := "https://graph.microsoft.com/v1.0/me/mailFolders/x/messages/delta?$deltatoken=abc"
	if err := s.SetFolderSyncState(ctx, inbox.ID, FolderSyncState{DeltaLink: link, LastSyncAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFolder(ctx, "acc", inbox.ID)
	if err != nil || f.DeltaLink != link {
		t.Fatalf("delta link = %q, %v", f.DeltaLink, err)
	}
	// A folder-list refresh keeps it; a reset clears it.
	if _, _, err := s.UpsertFolders(ctx, "acc", []Folder{{Mailbox: "AAMkInbox", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox}}); err != nil {
		t.Fatal(err)
	}
	if f, _ = s.GetFolder(ctx, "acc", inbox.ID); f.DeltaLink != link {
		t.Fatalf("delta link lost on upsert: %q", f.DeltaLink)
	}
	if err := s.ResetFolder(ctx, inbox.ID, 0); err != nil {
		t.Fatal(err)
	}
	if f, _ = s.GetFolder(ctx, "acc", inbox.ID); f.DeltaLink != "" {
		t.Fatalf("delta link survived reset: %q", f.DeltaLink)
	}
}
