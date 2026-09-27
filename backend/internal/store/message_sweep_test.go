// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSweepMessageFiles(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	kept := seedMessage(t, s, inbox, 1, "kept", time.Now())
	past := time.Now().Add(-25 * time.Hour)
	file := func(account, name string, mtime time.Time) string {
		t.Helper()
		p := filepath.Join(s.MessageDir(), account, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keptPath := s.MessageRawPath("acc", kept.ID)
	if err := os.Chtimes(keptPath, past, past); err != nil {
		t.Fatal(err)
	}

	gone := map[string]string{
		"no row":                   file("acc", newID(messageIDPrefix), past),
		"a write that never ended": file("acc", newID(messageIDPrefix)+".tmp", time.Now().Add(-2*staleTempAge)),
		"a removed account's":      file("acc_gone", newID(messageIDPrefix), past),
	}
	stays := map[string]string{
		"a row's, however old": keptPath,
		// Written a moment before its row, as EnqueueOutbox does.
		"young":                 file("acc", newID(messageIDPrefix), time.Now()),
		"a write in progress":   file("acc", newID(messageIDPrefix)+".tmp", time.Now().Add(-staleTempAge/2)),
		"a row's id elsewhere":  file("acc_other", kept.ID, past),
		"not a message id":      file("acc", "notes.txt", past),
		"not a message tmp":     file("acc", "notes.tmp", past),
		"an id not the store's": file("acc", "m_1", past),
	}
	idDir := filepath.Join(s.MessageDir(), "acc", newID(messageIDPrefix))
	if err := os.Mkdir(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stays["a directory"] = idDir

	n, err := s.SweepMessageFiles(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(gone) {
		t.Errorf("removed %d, want %d", n, len(gone))
	}
	for what, p := range gone {
		if fileExists(t, p) {
			t.Errorf("%s: survived the sweep", what)
		}
	}
	for what, p := range stays {
		if !fileExists(t, p) {
			t.Errorf("%s: deleted by the sweep", what)
		}
	}
	if n, err := s.SweepMessageFiles(ctx, 24*time.Hour); n != 0 || err != nil {
		t.Errorf("second sweep: %d %v", n, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SweepMessageFiles(cancelled, 24*time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sweep: %v", err)
	}
}

// A store without a message directory has nothing to sweep.
func TestSweepMessageFilesWithoutDirectory(t *testing.T) {
	s := openTestStore(t)
	if err := os.RemoveAll(s.MessageDir()); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepMessageFiles(context.Background(), time.Hour); n != 0 || err != nil {
		t.Errorf("sweep: %d %v", n, err)
	}
}

func TestIsMessageID(t *testing.T) {
	if id := newID(messageIDPrefix); !isMessageID(id) {
		t.Errorf("%s is not a message id", id)
	}
	for _, s := range []string{"", "m_", "m_1", "att_0123456789abcdef0123456789abcdef",
		"m_0123456789ABCDEF0123456789ABCDEF", "m_0123456789abcdef0123456789abcdeg",
		"m_0123456789abcdef0123456789abcdef0", "m_0123456789abcdef0123456789abcdef.tmp"} {
		if isMessageID(s) {
			t.Errorf("%q taken for a message id", s)
		}
	}
}
