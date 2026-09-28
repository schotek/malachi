// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
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

// lettingGo leaves fsretry a single attempt, and has the reader that
// releases holds for a message close its file just after an attempt to
// rename over or remove that file failed: the failure outlasts the
// retries, and by the time they are over the store counts no reader of
// the file any more. The store once counted them only then, and took such
// a failure for another than a reader's. Called after asOnWindows, which
// puts the waits back.
func lettingGo(s *Store, releases map[string]func()) {
	fsretry.Waits = nil
	s.attemptFailed = func(path string) {
		if release := releases[strings.TrimSuffix(filepath.Base(path), RawZstSuffix)]; release != nil {
			release()
		}
	}
}

// logWarnings has s log its warnings into the buffer it returns.
func logWarnings(s *Store) *bytes.Buffer {
	var buf bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return &buf
}

// rawCounts is how many message locks and open readers the store counts.
func rawCounts(s *Store) (locks, readers int) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	for _, l := range s.rawLocks {
		readers += l.readers[RawPlain] + l.readers[RawZstd]
	}
	return len(s.rawLocks), readers
}

// A commit whose stored file the reader lets go just after the last
// attempt of the rename failed is ErrBusy all the same, with nothing
// warned of, and phase A is undone: the row says what the file holds.
func TestCommitBusyWhenTheReaderLetsGoAfterTheLastAttempt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	warned := logWarnings(s)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts")
	m := seedFetched(t, s, inbox, 1, full)
	state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID)

	lettingGo(s, map[string]func(){m.ID: openReader(t, s, "acc", m.ID)})
	_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{RawState: RawFull}})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("commit: %v", err)
	}
	if st, p, rb, _, _, _ := rawColumns(t, s, m.ID); st != state || p != parts || rb != remoteBytes {
		t.Errorf("row after the refused commit: %s %s %d, want %s %s %d", st, p, rb, state, parts, remoteBytes)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), full) {
		t.Error("file replaced by a refused commit")
	}
	if warned.Len() != 0 {
		t.Errorf("warned of a reader:\n%s", warned)
	}
	if locks, readers := rawCounts(s); locks != 0 || readers != 0 {
		t.Errorf("%d locks, %d readers left", locks, readers)
	}
}

// The conversion's removal of the sources and a deletion, each while
// readers hold the files to remove and let go just after the attempt
// failed: the conversion counts the messages busy rather than failed,
// nothing is warned of, the content stays, and the sweep settles the
// pairs and removes the deleted messages' files once the readers are done.
func TestRemovalsBusyWhenTheReaderLetsGoAfterTheLastAttempt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	warned := logWarnings(s)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 4)
	dir := filepath.Join(s.MessageDir(), "acc")
	hold := func() map[string]func() {
		releases := map[string]func(){}
		for _, id := range ids {
			releases[id] = openReader(t, s, "acc", id)
		}
		return releases
	}

	lettingGo(s, hold())
	s.SetRawCodec(RawZstd)
	if _, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 10); err != nil || res.Busy != len(ids) || res.Failed != 0 || res.Converted != 0 {
		t.Fatalf("conversion: %+v %v", res, err)
	}
	for _, id := range ids {
		if files, _ := statRaw(dir, id); !files.both() {
			t.Fatalf("%s: files %+v, want the new one beside the source", id, files)
		}
		if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
			t.Errorf("%s: content changed", id)
		}
	}
	if res, err := s.SweepMessageFiles(ctx, 0); err != nil || res.Resolved != len(ids) || res.Busy != 0 {
		t.Errorf("sweep once the readers are done: %+v %v", res, err)
	}

	lettingGo(s, hold())
	uids := []uint32{1000, 1001, 1002, 1003}
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, uids); err != nil {
		t.Fatal(err)
	}
	if left := dirNames(t, dir); len(left) != len(ids) {
		t.Errorf("left after the deletion: %v, want the held files", left)
	}
	if res, err := s.SweepMessageFiles(ctx, 0); err != nil || res.Orphans != len(ids) {
		t.Errorf("sweep after the deletion: %+v %v", res, err)
	}
	if left := dirNames(t, dir); len(left) != 0 {
		t.Errorf("left after the sweep: %v", left)
	}
	if warned.Len() != 0 {
		t.Errorf("warned of readers:\n%s", warned)
	}
	if locks, readers := rawCounts(s); locks != 0 || readers != 0 {
		t.Errorf("%d locks, %d readers left", locks, readers)
	}
}

// A commit whose rename fails for another cause than a reader (here a
// directory where the new file is to go, which no system renames a file
// over) is no ErrBusy, and the row says what the file holds all the same:
// the stored file stays whole, so phase A is undone. Once the way is
// clear the commit goes through.
func TestCommitUndoneWhenTheRenameFails(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts")
	m := seedFetched(t, s, inbox, 1, full)
	state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID)
	s.SetRawCodec(RawZstd)
	blocker := s.MessageRawPath("acc", m.ID) + RawZstSuffix
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	commit := func() error {
		_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
			RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{RawState: RawFull}})
		return err
	}

	err := commit()
	if err == nil || errors.Is(err, ErrBusy) || !errors.Is(err, errNotReplaced) {
		t.Fatalf("commit over a directory: %v", err)
	}
	if st, p, rb, _, _, _ := rawColumns(t, s, m.ID); st != state || p != parts || rb != remoteBytes {
		t.Errorf("row after the failed commit: %s %s %d, want %s %s %d", st, p, rb, state, parts, remoteBytes)
	}
	if got, err := os.ReadFile(s.MessageRawPath("acc", m.ID)); err != nil || !bytes.Equal(got, full) {
		t.Errorf("stored file after the failed commit: %q %v", got, err)
	}
	for _, name := range dirNames(t, filepath.Join(s.MessageDir(), "acc")) {
		if strings.HasSuffix(name, tmpSuffix) {
			t.Errorf("left behind: %s", name)
		}
	}
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	if err := commit(); err != nil {
		t.Fatalf("commit once the way is clear: %v", err)
	}
	if st, p, _, _, _, _ := rawColumns(t, s, m.ID); st != string(RawPartial) || p != `["2"]` {
		t.Errorf("row after the commit: %s %s", st, p)
	}
	if got := readRaw(t, s, "acc", m.ID); string(got) != "skeleton" {
		t.Errorf("file after the commit: %q", got)
	}
}

// A reader the store does not count (another process, a virus scanner)
// holds the stored file through a commit: Windows refuses the rename, and
// the commit fails without calling it busy, the row and the file as they
// were; elsewhere the commit goes through. Either way the row says what
// the file holds.
func TestCommitWithAReaderTheStoreDoesNotCount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts")
	m := seedFetched(t, s, inbox, 1, full)
	state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID)

	f, err := os.Open(s.MessageRawPath("acc", m.ID))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{RawState: RawFull}})
	f.Close()
	st, p, rb, _, _, _ := rawColumns(t, s, m.ID)
	got := readRaw(t, s, "acc", m.ID)
	switch {
	case err == nil:
		if st != string(RawPartial) || p != `["2"]` || string(got) != "skeleton" {
			t.Errorf("committed: row %s %s, file %q", st, p, got)
		}
	case errors.Is(err, ErrBusy), !errors.Is(err, errNotReplaced):
		t.Fatalf("commit with a reader the store does not count: %v", err)
	default:
		if st != state || p != parts || rb != remoteBytes || !bytes.Equal(got, full) {
			t.Errorf("refused: row %s %s %d (was %s %s %d), file %q", st, p, rb, state, parts, remoteBytes, got)
		}
	}
}

// A reduction and a download of one message, each tried while a reader
// holds the stored file past the retries, while one lets go just after
// the last attempt, while a reader the store does not count holds it (on
// Windows), and with no reader: after every attempt the row says what the
// file holds (the whole message under a full row, the skeleton under a
// partial one), and a refusal changes neither.
func TestDownloadAndReductionKeepRowAndFileInStep(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	releases := map[string]func(){}
	lettingGo(s, releases)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts" + strings.Repeat("y", 4000))
	skel := []byte("Subject: full\r\n\r\nskeleton")
	m := seedFetched(t, s, inbox, 1, full)
	reduce := func() error {
		_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, skel),
			RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{RawState: RawFull}})
		return err
	}
	download := func() error {
		_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, full), Hydrated: true,
			Expect: RawExpect{RawState: RawPartial}})
		return err
	}
	inStep := func(step string, want RawState) {
		t.Helper()
		state, parts, _, _, _, _ := rawColumns(t, s, m.ID)
		got := readRaw(t, s, "acc", m.ID)
		switch {
		case RawState(state) != want:
			t.Errorf("%s: row %s, want %s", step, state, want)
		case want == RawFull && (parts != "[]" || !bytes.Equal(got, full)):
			t.Errorf("%s: full row %s over %d bytes", step, parts, len(got))
		case want == RawPartial && (parts != `["2"]` || !bytes.Equal(got, skel)):
			t.Errorf("%s: partial row %s over %d bytes", step, parts, len(got))
		}
	}

	for _, op := range []struct {
		name          string
		run           func() error
		before, after RawState
	}{
		{"reduction", reduce, RawFull, RawPartial},
		{"download", download, RawPartial, RawFull},
	} {
		release := openReader(t, s, "acc", m.ID)
		if err := op.run(); !errors.Is(err, ErrBusy) {
			t.Fatalf("%s while a reader holds the file: %v", op.name, err)
		}
		release()
		inStep(op.name+" held", op.before)

		releases[m.ID] = openReader(t, s, "acc", m.ID)
		if err := op.run(); !errors.Is(err, ErrBusy) {
			t.Fatalf("%s while a reader lets go after the attempt: %v", op.name, err)
		}
		delete(releases, m.ID)
		inStep(op.name+" let go", op.before)

		f, err := os.Open(s.MessageRawPath("acc", m.ID))
		if err != nil {
			t.Fatal(err)
		}
		err = op.run()
		f.Close()
		if err != nil {
			if errors.Is(err, ErrBusy) || !errors.Is(err, errNotReplaced) {
				t.Fatalf("%s while a reader the store does not count holds the file: %v", op.name, err)
			}
			inStep(op.name+" uncounted", op.before)
			if err := op.run(); err != nil {
				t.Fatalf("%s: %v", op.name, err)
			}
		}
		inStep(op.name, op.after)
	}
	if locks, readers := rawCounts(s); locks != 0 || readers != 0 {
		t.Errorf("%d locks, %d readers left", locks, readers)
	}
}

// Of both variants of a message a reader takes the newer, as the sweep and
// RawTx.Stat keep it: a message replaced while a reader held its other
// variant (Windows keeps that file then) reads as replaced even once its
// codec is switched back, before the pair is settled as after. With equal
// times it takes the store codec's, which is again the one they keep.
func TestReadersTakeTheNewerVariant(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	s.SetRawCodec(RawZstd)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	dir := filepath.Join(s.MessageDir(), "acc")
	settled := func(id string) []byte {
		t.Helper()
		if err := s.WithMessageRaw(ctx, "acc", id, func(tx *RawTx) error { _, _, err := tx.Stat(); return err }); err != nil {
			t.Fatal(err)
		}
		if files, _ := statRaw(dir, id); files.both() {
			t.Fatalf("%s: both files after Stat", id)
		}
		return readRaw(t, s, "acc", id)
	}

	m := seedMessage(t, s, inbox, 1, "old", time.Now())
	release := openReader(t, s, "acc", m.ID)
	s.SetRawCodec(RawPlain)
	if _, err := s.WriteMessageRaw(ctx, "acc", m.ID, strings.NewReader("new"), 10); err != nil {
		t.Fatalf("replace while a reader holds the other variant: %v", err)
	}
	release()
	if files, _ := statRaw(dir, m.ID); !files.both() {
		t.Fatalf("files %+v, want the held one beside the new one", files)
	}
	// Older by a minute: a coarse clock may give both files one time.
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(s.MessageRawPath("acc", m.ID)+RawZstSuffix, past, past); err != nil {
		t.Fatal(err)
	}
	s.SetRawCodec(RawZstd)
	if got := readRaw(t, s, "acc", m.ID); string(got) != "new" {
		t.Errorf("read with the codec switched back: %q, want the replacement", got)
	}
	if got := settled(m.ID); string(got) != "new" {
		t.Errorf("read once settled: %q", got)
	}

	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		s.SetRawCodec(RawZstd)
		twin := seedMessage(t, s, inbox, uint32(2+codec), "compressed", time.Now())
		path := s.MessageRawPath("acc", twin.ID)
		if err := os.WriteFile(path, []byte("plain twin"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{path, path + RawZstSuffix} {
			if err := os.Chtimes(p, past, past); err != nil {
				t.Fatal(err)
			}
		}
		s.SetRawCodec(codec)
		read := readRaw(t, s, "acc", twin.ID)
		if want := (codec == RawPlain); bytes.Equal(read, []byte("plain twin")) != want {
			t.Errorf("equal times, codec %s: read %q", codec, read)
		}
		if got := settled(twin.ID); !bytes.Equal(got, read) {
			t.Errorf("equal times, codec %s: read %q, kept %q", codec, read, got)
		}
	}
}

// An account deleted while a reader has one of its files open, which
// refuseOpen makes refuse the removal of its directory as Windows refuses
// it, leaves the directory recorded as this store's to remove: the sweep
// counts it busy while the reader holds it, then removes it whole, however
// young its files, and forgets it.
func TestDeletedAccountDirectoryGoesWithTheSweep(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "held", time.Now())
	seedMessage(t, s, inbox, 2, "other", time.Now())
	dir := filepath.Join(s.MessageDir(), "acc")
	recorded := func() bool {
		t.Helper()
		_, ok, err := s.GetMeta(ctx, metaDeletedDir+"acc")
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	release := openReader(t, s, "acc", m.ID)
	if err := s.DeleteAccount(ctx, "acc", true); err != nil {
		t.Fatal(err)
	}
	if len(dirNames(t, dir)) == 0 || !recorded() {
		t.Fatalf("after the deletion: %v, recorded %v", dirNames(t, dir), recorded())
	}
	if res, err := s.SweepMessageFiles(ctx, time.Hour); err != nil || res.Busy != 1 || res.Dirs != 0 {
		t.Errorf("sweep while the reader holds a file: %+v %v", res, err)
	}
	if len(dirNames(t, dir)) == 0 || !recorded() {
		t.Fatalf("after the first sweep: %v, recorded %v", dirNames(t, dir), recorded())
	}
	release()
	if res, err := s.SweepMessageFiles(ctx, time.Hour); err != nil || res.Busy != 0 || res.Dirs != 1 {
		t.Errorf("sweep once the reader is done: %+v %v", res, err)
	}
	if fileExists(t, dir) || recorded() {
		t.Errorf("after the sweep: directory %v, recorded %v", fileExists(t, dir), recorded())
	}
}

// The same with a reader that really holds a file: Windows refuses to
// remove it, elsewhere the directory goes at once. Once the reader is done
// one sweep leaves nothing of the account, and no record of it.
func TestDeletedAccountHeldFileGoesWithTheSweep(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
	seedAccount(t, s, "acc")
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "held", time.Now())
	seedMessage(t, s, inbox, 2, "other", time.Now())
	dir := filepath.Join(s.MessageDir(), "acc")

	release := openReader(t, s, "acc", m.ID)
	if err := s.DeleteAccount(ctx, "acc", true); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := s.SweepMessageFiles(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if fileExists(t, dir) {
		t.Errorf("a deleted account's mail stays after the sweep: %v", dirNames(t, dir))
	}
	if _, ok, err := s.GetMeta(ctx, metaDeletedDir+"acc"); err != nil || ok {
		t.Errorf("record after the sweep: %v %v", ok, err)
	}
}

// The sweep removes a directory with files only for an account this store
// deleted: one recorded but not removed (a crash right after the
// deletion) goes whole, an unknown account's stays (another store's, see
// TestSweepLeavesAnotherStoresMail), and the record of an account added
// again under its id, or of a directory already gone, is forgotten, the
// account's directory swept as any known one's.
func TestSweepRemovesTheDirectoriesOfDeletedAccounts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	write := func(acc, name string) string {
		t.Helper()
		dir := filepath.Join(s.MessageDir(), acc)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	record := func(acc string) {
		t.Helper()
		if err := s.SetMeta(ctx, metaDeletedDir+acc, nowStamp()); err != nil {
			t.Fatal(err)
		}
	}
	write("gone", "m_1")
	write("gone", "m_1.zst")
	record("gone")
	foreign := write("foreign", "m_2")
	seedAccount(t, s, "back")
	inbox := seedFolder(t, s, "back", "INBOX", api.RoleInbox)
	kept := seedMessage(t, s, inbox, 1, "kept", time.Now())
	record("back")
	record("none")

	if res, err := s.SweepMessageFiles(ctx, time.Hour); err != nil || res.Dirs != 1 || res.Orphans != 0 {
		t.Errorf("sweep: %+v %v", res, err)
	}
	if fileExists(t, filepath.Join(s.MessageDir(), "gone")) {
		t.Error("the deleted account's directory stayed")
	}
	if !fileExists(t, foreign) {
		t.Error("an unknown account's file removed")
	}
	if got := readRaw(t, s, "back", kept.ID); !bytes.HasPrefix(got, []byte("Subject: kept")) {
		t.Errorf("the account added again lost its message: %q", got)
	}
	left, err := s.deletedDirs(ctx)
	if err != nil || len(left) != 0 {
		t.Errorf("records left: %v %v", left, err)
	}
}

// A download committed from staging is newer than a variant a reader kept
// beside it, whatever time the staged file was received: its file takes
// the time it is placed. The staged file here is older than the .zst the
// codec step wrote meanwhile; with the receive time kept, the reader
// (openRaw prefers the newer variant) served the skeleton under a row that
// says full, and the sweep then deleted the download as the older file.
func TestStagedDownloadIsNewerThanTheKeptVariant(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	asOnWindows(t, s)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	seedAccount(t, s, "acc")
	full := []byte("Subject: full\r\n\r\nall the parts" + strings.Repeat("y", 4000))
	skel := []byte("Subject: full\r\n\r\nskeleton")
	m := seedFetched(t, s, inbox, 1, full)
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, skel),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{RawState: RawFull}}); err != nil {
		t.Fatal(err)
	}
	// message.download receives the whole message into staging/, then the
	// codec step converts the skeleton, and compression is switched off
	// again while a reader (message.part) holds the .zst.
	dl := stage(t, s, full)
	time.Sleep(50 * time.Millisecond)
	s.SetRawCodec(RawZstd)
	if res := convertAll(t, s, RawZstd, 10); res.Converted != 1 {
		t.Fatalf("conversion: %+v", res)
	}
	s.SetRawCodec(RawPlain)
	release := openReader(t, s, "acc", m.ID)
	_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: dl, Hydrated: true,
		Expect: RawExpect{RawState: RawPartial}})
	release()
	if err != nil {
		t.Fatalf("download commit: %v", err)
	}
	if state, parts, _, _, _, _ := rawColumns(t, s, m.ID); state != string(RawFull) {
		t.Fatalf("row %s %s, want full", state, parts)
	}
	if got := readRaw(t, s, "acc", m.ID); !bytes.Equal(got, full) {
		t.Errorf("reader: %d bytes, want the download (%d)", len(got), len(full))
	}
	if _, err := s.SweepMessageFiles(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, s, "acc", m.ID); !bytes.Equal(got, full) {
		t.Errorf("after the sweep: %d bytes, want the download (%d)", len(got), len(full))
	}
}

