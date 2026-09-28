// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The store's answer to a reader that keeps a message's file open for
// longer than a rename over it or its removal waits (fsretry). Windows
// refuses both while the file is open; refuseOpen makes the store behave
// so on any system, so that these tests mean the same on Linux and macOS.

// asOnWindows makes s refuse, as Windows does, to rename over or remove a
// message's file while one of its readers has it open, and gives fsretry
// short waits, for the rest of the test.
func asOnWindows(t *testing.T, s *Store) {
	t.Helper()
	s.refuseOpen = true
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
}

// openReader opens a message's file as a reader does (message.part
// streaming a large attachment, say) and returns what closes it; the end
// of the test closes it too.
func openReader(t *testing.T, s *Store, acc, id string) (release func()) {
	t.Helper()
	r, err := s.OpenMessageRaw(context.Background(), acc, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return func() { r.Close() }
}

// seedAccount adds an account row: the sweep leaves the files of a
// directory whose account the store does not know alone.
func seedAccount(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.AddAccount(context.Background(), &Account{ID: id, Config: api.AccountConfig{Email: id + "@example.invalid"}}); err != nil {
		t.Fatal(err)
	}
}

// A replacement that a reader holds up past the retries is ErrBusy and
// leaves the file as it was, with nothing behind; once the reader is done
// it goes through, and no lock or reader stays counted.
func TestReplaceBusyWhileAReaderHoldsTheFile(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "held", time.Now())
	before := readRaw(t, s, "acc", m.ID)

	release := openReader(t, s, "acc", m.ID)
	if _, err := s.WriteMessageRaw(ctx, "acc", m.ID, strings.NewReader("new"), 10); !errors.Is(err, ErrBusy) {
		t.Fatalf("replace while a reader holds the file: %v", err)
	}
	if got := readRaw(t, s, "acc", m.ID); !bytes.Equal(got, before) {
		t.Errorf("file changed by a refused replacement: %q", got)
	}
	for _, name := range dirNames(t, filepath.Join(s.MessageDir(), "acc")) {
		if strings.HasSuffix(name, tmpSuffix) {
			t.Errorf("left behind: %s", name)
		}
	}
	release()
	if _, err := s.WriteMessageRaw(ctx, "acc", m.ID, strings.NewReader("new"), 10); err != nil {
		t.Fatalf("replace once the reader is done: %v", err)
	}
	if got := readRaw(t, s, "acc", m.ID); string(got) != "new" {
		t.Errorf("after the replacement: %q", got)
	}
	s.rawMu.Lock()
	left := len(s.rawLocks)
	s.rawMu.Unlock()
	if left != 0 {
		t.Errorf("%d locks left", left)
	}
}

// A deleted message whose file a reader holds past the retries keeps the
// file for the sweep, which counts it busy while it is held and removes it
// as an orphan once it is not.
func TestDeletionLeavesAHeldFileToTheSweep(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "held", time.Now())
	path := s.MessageRawPath("acc", m.ID)

	release := openReader(t, s, "acc", m.ID)
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{1}); err != nil {
		t.Fatal(err)
	}
	if !fileExists(t, path) {
		t.Fatal("a file a reader holds was removed")
	}
	if res, err := s.SweepMessageFiles(ctx, 0); err != nil || res.Busy != 1 || res.Orphans != 0 {
		t.Errorf("sweep while the file is held: %+v %v", res, err)
	}
	release()
	if res, err := s.SweepMessageFiles(ctx, 0); err != nil || res.Busy != 0 || res.Orphans != 1 {
		t.Errorf("sweep once the reader is done: %+v %v", res, err)
	}
	if fileExists(t, path) {
		t.Error("the orphan stayed")
	}
}

// The conversion writes the new file beside a source a reader holds, and
// the new one is read from then on; the source's removal counts busy, and
// the sweep settles the pair once the reader is done.
func TestConversionLeavesAHeldSourceToTheSweep(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 1)
	id, dir := ids[0], filepath.Join(s.MessageDir(), "acc")

	release := openReader(t, s, "acc", id)
	s.SetRawCodec(RawZstd)
	if _, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 10); err != nil || res.Busy != 1 || res.Converted != 0 || res.Failed != 0 {
		t.Fatalf("conversion while the source is held: %+v %v", res, err)
	}
	if files, _ := statRaw(dir, id); !files.both() {
		t.Fatalf("files %+v, want the new one beside the held source", files)
	}
	if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
		t.Error("content changed")
	}
	release()
	older := time.Now().Add(-time.Minute)
	os.Chtimes(s.MessageRawPath("acc", id), older, older)
	if res, err := s.SweepMessageFiles(ctx, 0); err != nil || res.Resolved != 1 {
		t.Errorf("sweep once the reader is done: %+v %v", res, err)
	}
	if files, _ := statRaw(dir, id); files.plain != nil || files.zst == nil {
		t.Errorf("files after the sweep %+v, want the compressed one", files)
	}
	if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
		t.Error("content changed")
	}
}

// A commit whose file a reader holds past the retries is ErrBusy, and the
// row says what the file holds again: phase A's widening is undone, so
// that the message stays a candidate of the pass that tries again rather
// than partial with a whole file. Once the reader is done it goes through.
func TestCommitBusyUndoesPhaseA(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts")
	m := seedFetched(t, s, inbox, 1, full)
	state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID)
	commit := func() error {
		_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
			RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, StrippableBytes: 0})
		return err
	}

	release := openReader(t, s, "acc", m.ID)
	if err := commit(); !errors.Is(err, ErrBusy) {
		t.Fatalf("commit while a reader holds the file: %v", err)
	}
	if st, p, rb, _, _, _ := rawColumns(t, s, m.ID); st != state || p != parts || rb != remoteBytes {
		t.Errorf("row after a refused commit: %s %s %d, want %s %s %d", st, p, rb, state, parts, remoteBytes)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), full) {
		t.Error("file replaced by a refused commit")
	}
	release()
	if err := commit(); err != nil {
		t.Fatalf("commit once the reader is done: %v", err)
	}
	if st, p, _, _, _, _ := rawColumns(t, s, m.ID); st != string(RawPartial) || p != `["2"]` {
		t.Errorf("row after the commit: %s %s", st, p)
	}
}
