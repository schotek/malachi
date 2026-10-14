// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// waitChanged waits for the coalesced notify.messagesChanged and returns
// them as noteRecorder.takeChanged does.
func waitChanged(t *testing.T, rec *noteRecorder, want int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = append(got, rec.takeChanged()...)
		if len(got) >= want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return got
}

// unreadOf is the folder's unread count from folder.list.
func unreadOf(t *testing.T, b *Backend, acc api.AccountID, folder api.FolderID) int {
	t.Helper()
	res, err := b.Folders().List(context.Background(), api.FolderListParams{AccountID: acc})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Folders {
		if f.ID == folder {
			return f.Unread
		}
	}
	t.Fatalf("folder %s not listed", folder)
	return 0
}

func TestFolderMarkRead(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	rec := &noteRecorder{}
	m.b.SetNotifier(rec)
	svc := m.b.Folders()

	outbox, err := m.b.store.OutboxFolder(ctx, string(m.acc))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		p    api.FolderMarkReadParams
		code api.ErrorCode
	}{
		{"no folder", api.FolderMarkReadParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"no account", api.FolderMarkReadParams{FolderID: m.inbox}, api.CodeInvalidArgument},
		{"unknown account", api.FolderMarkReadParams{AccountID: "acc_nope", FolderID: m.inbox}, api.CodeAccountNotFound},
		{"unknown folder", api.FolderMarkReadParams{AccountID: m.acc, FolderID: "f_nope"}, api.CodeFolderNotFound},
		{"outbox", api.FolderMarkReadParams{AccountID: m.acc, FolderID: api.FolderID(outbox.ID)}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		if _, err := svc.MarkRead(ctx, c.p); errCode(t, err) != c.code {
			t.Errorf("%s: %v, want %v", c.name, err, c.code)
		}
	}
	if m.f.count("trigger:") != 0 {
		t.Fatal("a refused call nudged the syncer")
	}

	// A hidden message is not shown, so it stays unread.
	hidden := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 9, Subject: "hidden", Date: time.Now()}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{hidden}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.b.store.SetMessageHidden(ctx, hidden.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := unreadOf(t, m.b, m.acc, m.inbox); n != 2 {
		t.Fatalf("unread before = %d", n)
	}

	res, err := svc.MarkRead(ctx, api.FolderMarkReadParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil || res.Marked != 2 {
		t.Fatalf("markRead: %+v %v", res, err)
	}
	for _, id := range m.msgs {
		msg, _ := m.get(t, id)
		if !slices.Contains(msg.Flags, api.FlagSeen) {
			t.Errorf("%s still unread: %v", id, msg.Flags)
		}
	}
	if n := unreadOf(t, m.b, m.acc, m.inbox); n != 0 {
		t.Fatalf("unread after = %d", n)
	}
	if h, err := m.b.store.GetMessage(ctx, string(m.acc), hidden.ID); err != nil || slices.Contains(h.Flags, api.FlagSeen) {
		t.Fatalf("the hidden message was marked: %+v %v", h, err)
	}
	if got := m.f.recorded(); len(got) != 1 || got[0] != fmt.Sprintf("trigger:%s::false", m.acc) {
		t.Fatalf("supervisor calls = %v", got)
	}
	if n, _ := m.b.store.CountPendingOps(ctx, string(m.acc)); n != 2 {
		t.Fatalf("pending ops = %d", n)
	}
	if got := waitChanged(t, rec, 1); len(got) != 1 || got[0] != fmt.Sprintf("%s:%s", m.acc, m.inbox) {
		t.Fatalf("messagesChanged = %v", got)
	}

	// Nothing left to mark: no pass, no notification.
	res, err = svc.MarkRead(ctx, api.FolderMarkReadParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil || res.Marked != 0 || m.f.count("trigger:") != 1 {
		t.Fatalf("second markRead: %+v %v, calls %v", res, err, m.f.recorded())
	}
	time.Sleep(400 * time.Millisecond)
	if got := rec.takeChanged(); len(got) != 0 {
		t.Fatalf("messagesChanged for nothing: %v", got)
	}
}

// More than one batch of api.MaxMessageIDsPerCall.
func TestFolderMarkReadBatches(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	n := api.MaxMessageIDsPerCall + 5
	rows := make([]*store.Message, n)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	for i := range rows {
		rows[i] = &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uint32(100 + i),
			Subject: fmt.Sprintf("bulk %d", i), Date: base.Add(time.Duration(i) * time.Minute)}
	}
	if err := m.b.store.UpsertMessages(ctx, rows); err != nil {
		t.Fatal(err)
	}
	res, err := m.b.Folders().MarkRead(ctx, api.FolderMarkReadParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil || res.Marked != n+2 {
		t.Fatalf("markRead: %+v %v", res, err)
	}
	if left, _ := m.b.store.FolderMessageIDs(ctx, string(m.inbox), true, true); len(left) != 0 {
		t.Fatalf("still unread: %d", len(left))
	}
	if c := m.f.count("trigger:"); c != 1 {
		t.Fatalf("triggers = %d", c)
	}
}

func TestFolderEmpty(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	rec := &noteRecorder{}
	m.b.SetNotifier(rec)
	svc := m.b.Folders()

	if _, err := svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc, FolderID: m.inbox}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("inbox: %v", err)
	}
	if _, err := svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no folder: %v", err)
	}
	if _, err := svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc, FolderID: "f_nope"}); errCode(t, err) != api.CodeFolderNotFound {
		t.Fatalf("unknown folder: %v", err)
	}

	// An empty Trash: nothing to do.
	res, err := svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc, FolderID: m.trash})
	if err != nil || res.Deleted != 0 || m.f.count("trigger:") != 0 {
		t.Fatalf("empty trash: %+v %v, calls %v", res, err, m.f.recorded())
	}

	// Two messages in the Trash, one of them hidden: both go for good.
	if _, err := m.b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: m.acc, MessageIDs: m.msgs[:2]}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.b.store.SetMessageHidden(ctx, string(m.msgs[1]), true); err != nil {
		t.Fatal(err)
	}
	m.f.reset()
	rec.takeChanged()
	res, err = svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc, FolderID: m.trash})
	if err != nil || res.Deleted != 2 {
		t.Fatalf("empty: %+v %v", res, err)
	}
	for _, id := range m.msgs[:2] {
		if _, err := m.b.store.GetMessage(ctx, string(m.acc), string(id)); err == nil {
			t.Errorf("%s still stored", id)
		}
	}
	if m.folderOf(t, m.msgs[2]) != m.inbox {
		t.Fatal("the inbox lost a message")
	}
	if got := m.f.recorded(); len(got) != 1 || got[0] != fmt.Sprintf("trigger:%s::false", m.acc) {
		t.Fatalf("supervisor calls = %v", got)
	}
	ops, err := m.b.store.NextOps(ctx, string(m.acc), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	deletes := 0
	for _, op := range ops {
		if op.Kind == store.OpDelete && op.FolderID == string(m.trash) {
			deletes++
		}
	}
	if deletes != 2 {
		t.Fatalf("queued deletes from the trash = %d of %+v", deletes, ops)
	}
	if got := waitChanged(t, rec, 1); len(got) != 1 || got[0] != fmt.Sprintf("%s:%s", m.acc, m.trash) {
		t.Fatalf("messagesChanged = %v", got)
	}

	// A Junk folder is emptied for good too, not moved to the Trash.
	// UpsertFolders replaces the account's list: the others again, kept
	// under their ids.
	junk := seedFolders(t, m.b, string(m.acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Container", Name: "Container", Path: "Container", Subscribed: true, Selectable: false},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
	})
	if _, err := m.b.Messages().Move(ctx, api.MessageMoveParams{AccountID: m.acc, MessageIDs: m.msgs[2:], TargetFolderID: api.FolderID(junk["Junk"].ID)}); err != nil {
		t.Fatal(err)
	}
	res, err = svc.Empty(ctx, api.FolderEmptyParams{AccountID: m.acc, FolderID: api.FolderID(junk["Junk"].ID)})
	if err != nil || res.Deleted != 1 {
		t.Fatalf("empty junk: %+v %v", res, err)
	}
	if _, err := m.b.store.GetMessage(ctx, string(m.acc), string(m.msgs[2])); err == nil {
		t.Fatal("the junk message went elsewhere")
	}
}
