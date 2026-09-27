// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The sweep of Maintain deletes a raw file whose message is gone once it is
// old enough, here the body of a message expunged while it downloaded,
// and logs how many it deleted; the raw file of a message that still
// exists stays however old it is.
func TestSweepFilesDeletesOrphanedRawFiles(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var logs bytes.Buffer
	b := New("test", st, config.Default(), slog.New(slog.NewTextHandler(&logs, nil)))

	folders, _, err := st.UpsertFolders(ctx, "acc", []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true}})
	if err != nil {
		t.Fatal(err)
	}
	inbox := folders[0].ID
	msgs := []*store.Message{
		{AccountID: "acc", FolderID: inbox, UID: 1, Subject: "kept", Date: time.Now()},
		{AccountID: "acc", FolderID: inbox, UID: 2, Subject: "expunged", Date: time.Now()},
	}
	if err := st.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	writeRaw := func(id string) string {
		t.Helper()
		if _, err := st.WriteMessageRaw(ctx, "acc", id, strings.NewReader("Subject: x\r\n\r\nbody\r\n"), 1<<20); err != nil {
			t.Fatal(err)
		}
		return st.MessageRawPath("acc", id)
	}
	kept := writeRaw(msgs[0].ID)
	if err := st.DeleteMessagesByUID(ctx, inbox, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	orphan := writeRaw(msgs[1].ID) // the body lands after the row went

	// Young, a file without a row may be an outgoing message a moment
	// before its row.
	b.sweepFiles(ctx)
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("a young file without a row was deleted: %v", err)
	}

	old := time.Now().Add(-messageSweepAge - time.Hour)
	for _, p := range []string{kept, orphan} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	b.sweepFiles(ctx)
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("orphaned raw file after the sweep: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("raw file of an existing message: %v", err)
	}
	logged := false
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, `msg="message file sweep"`) && strings.Contains(line, "removed=1") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("the sweep did not log its count:\n%s", logs.String())
	}
}
