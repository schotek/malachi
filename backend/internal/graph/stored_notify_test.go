// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// notify.newMessage reads the message after Deps.Stored: one the hook (the
// mail rules) marked read or moved out of the inbox is not announced, nor
// is one that arrived read; the others are.
func TestNewMessageAfterStoredHook(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-INBOX", "Old", "alice@example.test", daysAgo(1), "x")
	h.syncer.deps.Stored = func(ctx context.Context, id string, _ ingest.Policy) {
		m, err := h.st.GetMessage(ctx, h.acc.ID, id)
		if err != nil {
			t.Error(err)
			return
		}
		switch m.Subject {
		case "Read by a rule":
			if err := h.st.FlagMessages(ctx, h.acc.ID, []string{id}, []api.Flag{api.FlagSeen}, nil); err != nil {
				t.Error(err)
			}
		case "Moved by a rule":
			if _, err := h.st.MoveMessages(ctx, h.acc.ID, []string{id}, h.folder("archive").ID); err != nil {
				t.Error(err)
			}
		}
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")

	h.fake.add("F-INBOX", "Plain", "alice@example.test", time.Now(), "x")
	h.fake.add("F-INBOX", "Read by a rule", "alice@example.test", time.Now(), "x")
	h.fake.add("F-INBOX", "Moved by a rule", "alice@example.test", time.Now(), "x")
	read := h.fake.add("F-INBOX", "Read elsewhere", "alice@example.test", time.Now(), "x")
	h.fake.setRead(read, true)
	h.pass(api.FolderID(inbox.ID), false)

	news := h.notes.newMessages()
	if len(news) != 1 || news[0].Message.Subject != "Plain" {
		t.Fatalf("notifications = %+v", news)
	}
}
