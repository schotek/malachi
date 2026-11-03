// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// notify.newMessage reads the message after Deps.Stored: one the hook (the
// mail rules) marked read or moved out of the inbox is not announced, nor
// is one that arrived read; the others are.
func TestNewMessageAfterStoredHook(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Archive", nil); err != nil {
		t.Fatal(err)
	}
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
			if _, err := h.st.MoveMessages(ctx, h.acc.ID, []string{id}, h.folder("Archive").ID); err != nil {
				t.Error(err)
			}
		}
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)

	h.append("INBOX", rawMessage("p1", "Plain", "x"), time.Now())
	if n := h.waitNewMessage(); n.Message.Subject != "Plain" {
		t.Fatalf("first = %+v", n.Message)
	}
	h.append("INBOX", rawMessage("r1", "Read by a rule", "x"), time.Now())
	h.append("INBOX", rawMessage("m1", "Moved by a rule", "x"), time.Now())
	h.append("INBOX", rawMessage("s1", "Read elsewhere", "x"), time.Now(), imap.FlagSeen)
	h.append("INBOX", rawMessage("l1", "Last", "x"), time.Now())
	if n := h.waitNewMessage(); n.Message.Subject != "Last" {
		t.Fatalf("announced %+v", n.Message)
	}
	if got := h.notes.newMessages(); len(got) != 2 {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
}
