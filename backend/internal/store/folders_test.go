// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// seedFolder stores one folder without disturbing the account's other
// folders (UpsertFolders replaces the whole list, so it re-lists them).
func seedFolder(t *testing.T, s *Store, account, mailbox string, role api.FolderRole) Folder {
	t.Helper()
	ctx := context.Background()
	existing, err := s.ListFolders(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	batch := make([]Folder, 0, len(existing)+1)
	for _, f := range existing {
		batch = append(batch, Folder{Mailbox: f.Mailbox, Name: f.Name, Path: f.Path, Role: f.Role, Selectable: f.Selectable, Subscribed: f.Subscribed})
	}
	batch = append(batch, Folder{Mailbox: mailbox, Name: mailbox, Path: mailbox, Role: role, Selectable: true, Subscribed: true})
	stored, _, err := s.UpsertFolders(ctx, account, batch)
	if err != nil {
		t.Fatal(err)
	}
	return stored[len(stored)-1]
}

// seedMessage inserts one message with a body file.
func seedMessage(t *testing.T, s *Store, f Folder, uid uint32, subject string, date time.Time, flags ...api.Flag) *Message {
	t.Helper()
	m := &Message{
		AccountID: f.AccountID, FolderID: f.ID, UID: uid, Flags: flags,
		From:    []api.Address{{Name: "Alice", Address: "alice@example.invalid"}},
		Subject: subject, Date: date, RFCMessageID: "<" + subject + "@example.invalid>", Size: 100,
	}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteMessageRaw(context.Background(), f.AccountID, m.ID, strings.NewReader("Subject: "+subject+"\r\n\r\nbody"), 1<<20); err != nil {
		t.Fatal(err)
	}
	return m
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return false
}

func TestUpsertFoldersIdempotentParentsAndCascade(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	long := strings.Repeat("ž", 1000)
	batch := []Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true},
		{Mailbox: "INBOX/Work", ParentMailbox: "INBOX", Delimiter: "/", Name: "Work", Path: "Inbox/Work", Selectable: true, Subscribed: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Selectable: true},
		{Mailbox: "Orphan/Child", ParentMailbox: "Orphan", Delimiter: "/", Name: "Child", Path: "Orphan/Child", Selectable: true},
		{Mailbox: long + "/../x\x00y", Name: "weird", Path: "weird", Selectable: false},
	}
	stored, removed, err := s.UpsertFolders(ctx, "acc", batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 5 || len(removed) != 0 {
		t.Fatalf("first upsert: %d stored, %v removed", len(stored), removed)
	}
	for i, f := range stored {
		if !strings.HasPrefix(f.ID, "f_") || f.AccountID != "acc" || f.Position != i || f.CreatedAt.IsZero() {
			t.Errorf("stored[%d] = %+v", i, f)
		}
	}
	if stored[1].ParentID != stored[0].ID || stored[1].ParentMailbox != "INBOX" {
		t.Errorf("parent not resolved: %+v", stored[1])
	}
	if stored[3].ParentID != "" || stored[3].ParentMailbox != "" {
		t.Errorf("unknown parent should be cleared: %+v", stored[3])
	}
	if stored[4].Role != api.RoleNone || stored[4].Mailbox != batch[4].Mailbox {
		t.Errorf("hostile mailbox: %+v", stored[4])
	}

	// Sync state and messages survive a re-upsert; ids are stable.
	work := stored[1]
	if err := s.SetFolderSyncState(ctx, work.ID, FolderSyncState{UIDValidity: 7, UIDNext: 42, HighestModSeq: 1 << 40, ServerMessages: 3, ServerUnseen: 1, LastSyncAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	m := seedMessage(t, s, work, 1, "hello", time.Now())
	batch[1].Name = "Work (renamed)"
	again, removed, err := s.UpsertFolders(ctx, "acc", batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed on identical batch: %v", removed)
	}
	for i := range stored {
		if again[i].ID != stored[i].ID {
			t.Errorf("id changed for %s: %s → %s", stored[i].Mailbox, stored[i].ID, again[i].ID)
		}
	}
	if again[1].Name != "Work (renamed)" || again[1].UIDValidity != 7 || again[1].UIDNext != 42 ||
		again[1].HighestModSeq != 1<<40 || again[1].ServerMessages != 3 || again[1].LastSyncAt.IsZero() ||
		!again[1].CreatedAt.Equal(stored[1].CreatedAt) {
		t.Errorf("sync columns not kept: %+v", again[1])
	}
	if _, err := s.GetMessage(ctx, "acc", m.ID); err != nil {
		t.Errorf("message lost on re-upsert: %v", err)
	}

	// Dropping the folder from the batch cascades to messages, ops, files.
	if err := s.FlagMessages(ctx, "acc", []string{m.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	third, removed, err := s.UpsertFolders(ctx, "acc", append(batch[:1:1], batch[2:]...))
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 4 || len(removed) != 1 || removed[0] != work.ID {
		t.Fatalf("third upsert: %d stored, removed %v (want %s)", len(third), removed, work.ID)
	}
	if _, err := s.GetMessage(ctx, "acc", m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("message survived folder removal: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", m.ID)) {
		t.Error("raw file survived folder removal")
	}
	if n, _ := s.CountPendingOps(ctx, "acc"); n != 0 {
		t.Errorf("ops survived folder removal: %d", n)
	}

	// Another account is untouched; duplicates are rejected wholesale.
	other := seedFolder(t, s, "other", "INBOX", api.RoleInbox)
	if _, _, err := s.UpsertFolders(ctx, "acc", []Folder{{Mailbox: "A", Name: "A", Path: "A"}, {Mailbox: "A", Name: "A", Path: "A"}}); err == nil {
		t.Error("duplicate mailbox accepted")
	}
	if list, _ := s.ListFolders(ctx, "acc"); len(list) != 4 {
		t.Errorf("list after rejected batch = %d", len(list))
	}
	if _, err := s.GetFolder(ctx, "other", other.ID); err != nil {
		t.Errorf("other account's folder: %v", err)
	}
	if _, err := s.GetFolder(ctx, "acc", other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign folder visible: %v", err)
	}
}

func TestFolderByRoleAndDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	seedFolder(t, s, "other", "Trash", api.RoleTrash)

	got, err := s.FolderByRole(ctx, "acc", api.RoleTrash)
	if err != nil || got.ID != trash.ID {
		t.Fatalf("by role: %+v %v", got, err)
	}
	if _, err := s.FolderByRole(ctx, "acc", api.RoleJunk); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing role: %v", err)
	}

	m := seedMessage(t, s, trash, 5, "gone", time.Now())
	if err := s.DeleteFolder(ctx, "other", trash.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := s.DeleteFolder(ctx, "acc", trash.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, "acc", m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("message survived: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", m.ID)) {
		t.Error("file survived")
	}
	if list, _ := s.ListFolders(ctx, "acc"); len(list) != 1 || list[0].ID != inbox.ID {
		t.Errorf("list after delete = %+v", list)
	}
	if err := s.DeleteFolder(ctx, "acc", trash.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestResetFolderAndRecount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	keep := seedFolder(t, s, "acc", "Keep", api.RoleNone)
	if err := s.SetFolderSyncState(ctx, inbox.ID, FolderSyncState{UIDValidity: 1, UIDNext: 10, HighestModSeq: 5, ServerMessages: 2, ServerUnseen: 1, LastSyncAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	m1 := seedMessage(t, s, inbox, 1, "one", time.Now(), api.FlagSeen)
	m2 := seedMessage(t, s, inbox, 2, "two", time.Now())
	other := seedMessage(t, s, keep, 1, "keep", time.Now())
	if err := s.FlagMessages(ctx, "acc", []string{m1.ID, other.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}

	unread, total, err := s.RecountFolder(ctx, inbox.ID)
	if err != nil || unread != 1 || total != 2 {
		t.Fatalf("recount: %d/%d %v", unread, total, err)
	}
	if _, _, err := s.RecountFolder(ctx, "f_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recount unknown: %v", err)
	}

	if err := s.ResetFolder(ctx, inbox.ID, 99); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFolder(ctx, "acc", inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.UIDValidity != 99 || f.UIDNext != 0 || f.HighestModSeq != 0 || f.ServerMessages != 0 || f.ServerUnseen != 0 ||
		f.Unread != 0 || f.Total != 0 || !f.LastSyncAt.IsZero() {
		t.Errorf("after reset: %+v", f)
	}
	for _, m := range []*Message{m1, m2} {
		if _, err := s.GetMessage(ctx, "acc", m.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s survived reset: %v", m.Subject, err)
		}
		if fileExists(t, s.MessageRawPath("acc", m.ID)) {
			t.Errorf("%s file survived reset", m.Subject)
		}
	}
	ops, err := s.NextOps(ctx, "acc", time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].MessageID != other.ID {
		t.Errorf("ops after reset = %+v (want only the Keep one)", ops)
	}
	if _, err := s.GetMessage(ctx, "acc", other.ID); err != nil {
		t.Errorf("other folder touched: %v", err)
	}
	if err := s.ResetFolder(ctx, "f_nope", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("reset unknown: %v", err)
	}
	if err := s.SetFolderSyncState(ctx, "f_nope", FolderSyncState{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("sync state unknown: %v", err)
	}
}
