// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestBackfillThreads(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true},
	})
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	msgs := []*store.Message{
		{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 1, RFCMessageID: "a", Date: day},
		{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 2, RFCMessageID: "b", InReplyTo: "a", Date: day.Add(time.Hour)},
		{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 3, RFCMessageID: "c", InReplyTo: "b", Date: day.Add(2 * time.Hour)},
		{AccountID: acc, FolderID: folders["INBOX"].ID, UID: 4, RFCMessageID: "d", Date: day.Add(3 * time.Hour)},
	}
	if err := b.store.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	threadIDs := func() []string {
		out := make([]string, len(msgs))
		for i, m := range msgs {
			got, err := b.store.GetMessage(ctx, acc, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			out[i] = got.ThreadID
		}
		return out
	}
	// Undo what the store did on insert: rows from before threading.
	split := func() {
		for i, m := range msgs {
			if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET thread_id = ? WHERE id = ?`, "t_legacy"+string(rune('0'+i)), m.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.store.DB().ExecContext(ctx, `DELETE FROM message_refs`); err != nil {
			t.Fatal(err)
		}
	}
	split()
	if err := b.backfillThreads(ctx); err != nil {
		t.Fatal(err)
	}
	if got := threadIDs(); got[0] != got[1] || got[1] != got[2] || got[3] == got[0] {
		t.Fatalf("after backfill: %v", got)
	}
	if v, ok, _ := b.store.GetMeta(ctx, metaThreadsLinked); !ok || v != threadsLinkedDone {
		t.Fatalf("meta = %q, %v", v, ok)
	}

	// Once done, a later start does nothing.
	split()
	if err := b.backfillThreads(ctx); err != nil {
		t.Fatal(err)
	}
	if got := threadIDs(); got[0] == got[1] {
		t.Fatalf("finished backfill ran again: %v", got)
	}

	// A saved cursor resumes after it: the rows before it are left alone.
	if err := b.store.SetMeta(ctx, metaThreadsLinked, ""); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillThreads(ctx); err != nil {
		t.Fatal(err)
	}
	if got := threadIDs(); got[0] != got[1] || got[1] != got[2] {
		t.Fatalf("restarted backfill: %v", got)
	}
}
