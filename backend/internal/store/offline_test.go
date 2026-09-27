// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// offlineAttachments are the parts of the messages these tests reduce:
// part 2 large, part 3 small.
var offlineAttachments = []api.Attachment{
	{PartID: "2", Filename: "big.pdf", ContentType: "application/pdf", Size: 300 << 10},
	{PartID: "3", Filename: "small.txt", ContentType: "text/plain", Size: 1 << 10},
}

// seedFetched stores a fetched message with a whole raw file.
func seedFetched(t *testing.T, s *Store, f Folder, uid uint32, content []byte) *Message {
	t.Helper()
	ctx := context.Background()
	m := seedRow(t, s, f, uid)
	if _, err := s.PutMessageRaw(ctx, f.AccountID, m.ID, RawWrite{}, chunked(content)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Text: "text", Attachments: offlineAttachments, HasAttachments: true}); err != nil {
		t.Fatal(err)
	}
	return m
}

// stage makes a staged message of content.
func stage(t *testing.T, s *Store, content []byte) *Staged {
	t.Helper()
	st, err := s.StageRaw(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(content); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Remove() })
	return st
}

// rawColumns reads the partial-state columns straight from the row.
func rawColumns(t *testing.T, s *Store, id string) (state, parts string, remoteBytes, strippable int64, hydrated, updated string) {
	t.Helper()
	if err := s.DB().QueryRow(`SELECT raw_state, remote_parts, remote_bytes, strippable_bytes, hydrated_at, updated_at
		FROM messages WHERE id = ?`, id).Scan(&state, &parts, &remoteBytes, &strippable, &hydrated, &updated); err != nil {
		t.Fatal(err)
	}
	return
}

func attachmentsJSON(t *testing.T, s *Store, id string) string {
	t.Helper()
	var js string
	if err := s.DB().QueryRow(`SELECT attachments_json FROM messages WHERE id = ?`, id).Scan(&js); err != nil {
		t.Fatal(err)
	}
	return js
}

func TestCommitMessageRawPartialAndBack(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := rawContent(64<<10, 7)
	m := seedFetched(t, s, inbox, 1, full)
	_, _, _, _, _, updatedBefore := rawColumns(t, s, m.ID)

	skeleton := []byte("Subject: skeleton\r\n\r\nparts left out")
	n, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{
		Source: stage(t, s, skeleton), RemoteParts: []string{"2", "2", ""}, RemoteBytes: 300 << 10, StrippableBytes: 0,
		Expect: RawExpect{BodyState: BodyFetched, RawState: RawFull},
	})
	if err != nil || n != int64(len(skeleton)) {
		t.Fatalf("reduce: %d %v", n, err)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RawState != RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) || got.RemoteBytes != 300<<10 ||
		got.StrippableBytes != 0 || !got.HydratedAt.IsZero() {
		t.Errorf("row after reducing: %+v", got)
	}
	if !got.Attachments[0].Remote || got.Attachments[1].Remote {
		t.Errorf("overlay: %+v", got.Attachments)
	}
	if js := attachmentsJSON(t, s, m.ID); strings.Contains(js, "remote") {
		t.Errorf("remote flag stored: %s", js)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), skeleton) {
		t.Error("file not replaced")
	}
	// A change of the file only does not touch updated_at.
	if _, _, _, _, _, updated := rawColumns(t, s, m.ID); updated != updatedBefore {
		t.Errorf("updated_at bumped: %s → %s", updatedBefore, updated)
	}
	// Every listing overlays the flag.
	items, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, "", 10, "", "")
	if err != nil || len(items) != 1 || !items[0].Attachments[0].Remote {
		t.Errorf("list: %+v %v", items, err)
	}

	// A download makes it whole again.
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, full), Hydrated: true, StrippableBytes: 300 << 10,
		Expect: RawExpect{RawState: RawFull}}); !errors.Is(err, ErrConflict) {
		t.Errorf("expected full: %v", err)
	}
	var never time.Time
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, full), Hydrated: true, StrippableBytes: 300 << 10,
		Expect: RawExpect{RawState: RawPartial, HydratedAt: &never}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMessage(ctx, "acc", m.ID)
	if got.RawState != RawFull || len(got.RemoteParts) != 0 || got.RemoteBytes != 0 || got.StrippableBytes != 300<<10 ||
		time.Since(got.HydratedAt) > time.Minute || got.Attachments[0].Remote {
		t.Errorf("row after the download: %+v", got)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), full) {
		t.Error("file not whole")
	}
	// The expectation of when it was made whole.
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, full), Expect: RawExpect{HydratedAt: &never}}); !errors.Is(err, ErrConflict) {
		t.Errorf("hydrated since: %v", err)
	}
	hydrated := got.HydratedAt
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, full), StrippableBytes: -1,
		Expect: RawExpect{HydratedAt: &hydrated}}); err != nil {
		t.Errorf("same hydration: %v", err)
	}
	if got, _ := s.GetMessage(ctx, "acc", m.ID); !got.HydratedAt.Equal(hydrated) {
		t.Errorf("hydrated_at changed without Hydrated: %v", got.HydratedAt)
	}
}

// The background pass reads a message and reduces it under one lock.
func TestRawTxCommitUnderOneLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := rawContent(32<<10, 3)
	m := seedFetched(t, s, inbox, 1, full)
	skeleton := []byte("Subject: skeleton\r\n\r\nbody")
	err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		r, err := tx.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(data, full) {
			return fmt.Errorf("read under the lock: %d bytes, %v", len(data), err)
		}
		_, err = tx.Commit(RawCommit{Source: stage(t, s, skeleton), RemoteParts: []string{"2"}, RemoteBytes: 300 << 10,
			Expect: RawExpect{BodyState: BodyFetched, RawState: RawFull, HydratedAt: &m.HydratedAt}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "acc", m.ID); got.RawState != RawPartial || !bytes.Equal(readRaw(t, s, "acc", m.ID), skeleton) {
		t.Errorf("after the commit: %+v", got)
	}
	var kept *RawTx
	s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error { kept = tx; return nil })
	if _, err := kept.Commit(RawCommit{Source: stage(t, s, full)}); !errors.Is(err, errRawTxDone) {
		t.Errorf("commit after the end: %v", err)
	}
}

func TestCommitMessageRawRefusals(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedFetched(t, s, inbox, 1, []byte("Subject: x\r\n\r\nbody"))
	out, _ := seedOutbox(t, s, "acc")
	for name, c := range map[string]struct {
		id     string
		commit RawCommit
		want   error
	}{
		"no source":       {m.ID, RawCommit{}, nil},
		"parts, no bytes": {m.ID, RawCommit{Source: stage(t, s, []byte("x")), RemoteParts: []string{"2"}}, nil},
		"bytes, no parts": {m.ID, RawCommit{Source: stage(t, s, []byte("x")), RemoteBytes: 5}, nil},
		"strippable":      {m.ID, RawCommit{Source: stage(t, s, []byte("x")), StrippableBytes: -2}, nil},
		"unknown":         {"m_nope", RawCommit{Source: stage(t, s, []byte("x"))}, ErrNotFound},
		"outbox":          {out.ID, RawCommit{Source: stage(t, s, []byte("x"))}, ErrOutbox},
		"body state":      {m.ID, RawCommit{Source: stage(t, s, []byte("x")), Expect: RawExpect{BodyState: BodyNone}}, ErrConflict},
		"raw state":       {m.ID, RawCommit{Source: stage(t, s, []byte("x")), Expect: RawExpect{RawState: RawPartial}}, ErrConflict},
		"other account":   {m.ID, RawCommit{Source: stage(t, s, []byte("x"))}, ErrNotFound},
		"used-up source":  {m.ID, RawCommit{Source: func() *Staged { st := stage(t, s, []byte("x")); st.Remove(); return st }()}, errStagedDone},
	} {
		acc := "acc"
		if name == "other account" {
			acc = "other"
		}
		_, err := s.CommitMessageRaw(ctx, acc, c.id, c.commit)
		if err == nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := readRaw(t, s, "acc", m.ID); string(got) != "Subject: x\r\n\r\nbody" {
		t.Errorf("file changed by a refused commit: %q", got)
	}
}

func TestCommitMessageRawPhaseA(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	full := []byte("Subject: full\r\n\r\nall the parts")
	m := seedFetched(t, s, inbox, 1, full)
	crash := errors.New("crash after phase A")
	s.afterPhaseA = func() error { return crash }
	_, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, StrippableBytes: 0})
	if !errors.Is(err, crash) {
		t.Fatalf("commit: %v", err)
	}
	// The row already calls part 2 remote while the file still has it: the
	// safe side.
	if state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID); state != "partial" || parts != `["2"]` || remoteBytes <= 0 {
		t.Errorf("after phase A: %s %s %d", state, parts, remoteBytes)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), full) {
		t.Error("file replaced before phase B")
	}
	// Another set: the row names the union until the file has changed.
	_, err = s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"3"}, RemoteBytes: 1 << 10, StrippableBytes: 0})
	if !errors.Is(err, crash) {
		t.Fatalf("commit: %v", err)
	}
	if _, parts, _, _, _, _ := rawColumns(t, s, m.ID); parts != `["2","3"]` {
		t.Errorf("union: %s", parts)
	}
	// A narrower set needs no phase A, and a commit that finishes settles it.
	s.afterPhaseA = nil
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"3"}, RemoteBytes: 1 << 10, StrippableBytes: 0}); err != nil {
		t.Fatal(err)
	}
	if state, parts, remoteBytes, _, _, _ := rawColumns(t, s, m.ID); state != "partial" || parts != `["3"]` || remoteBytes != 1<<10 {
		t.Errorf("settled: %s %s %d", state, parts, remoteBytes)
	}
}

// Phase A of a reduction that replaces a stored file is on disk before the
// file changes: its transaction runs with synchronous FULL and fullfsync,
// and the connection goes back to the pool as the store's others are. A
// message's first file needs no such care, and gets the ordinary commit.
func TestCommitMessageRawPhaseADurable(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	// One connection: the one phase A borrowed is the next the pool hands out.
	s.DB().SetMaxOpenConns(1)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	type levels struct{ synchronous, fullfsync int }
	read := func(q interface {
		QueryRow(string, ...any) *sql.Row
	}) (levels, error) {
		var l levels
		if err := q.QueryRow(`PRAGMA synchronous`).Scan(&l.synchronous); err != nil {
			return l, err
		}
		err := q.QueryRow(`PRAGMA fullfsync`).Scan(&l.fullfsync)
		return l, err
	}
	var seen []levels
	s.phaseACommit = func(tx *sql.Tx) error {
		l, err := read(tx)
		seen = append(seen, l)
		return err
	}
	ordinary, flushed := levels{1, 0}, levels{2, 1} // NORMAL; FULL with F_FULLFSYNC
	if l, err := read(s.DB()); err != nil || l != ordinary {
		t.Fatalf("pool before: %+v %v", l, err)
	}

	m := seedFetched(t, s, inbox, 1, []byte("Subject: full\r\n\r\nall the parts"))
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("Subject: full\r\n\r\nskeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{BodyState: BodyFetched, RawState: RawFull}}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != flushed {
		t.Fatalf("phase A over a stored file: %+v", seen)
	}
	if l, err := read(s.DB()); err != nil || l != ordinary {
		t.Fatalf("pool after: %+v %v", l, err)
	}

	seen = nil
	first := seedRow(t, s, inbox, 2)
	if _, err := s.CommitMessageRaw(ctx, "acc", first.ID, RawCommit{Source: stage(t, s, []byte("Subject: first\r\n\r\nskeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, Expect: RawExpect{BodyState: BodyNone}}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != ordinary {
		t.Fatalf("phase A of a first file: %+v", seen)
	}
	if l, err := read(s.DB()); err != nil || l != ordinary {
		t.Fatalf("pool at the end: %+v %v", l, err)
	}
}

// A message in Drafts keeps every part: a commit that would leave some on
// the server is refused (it was moved there after the caller decided), a
// whole one is not.
func TestCommitMessageRawRefusesDrafts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	full := []byte("Subject: draft\r\n\r\nall the parts")
	m := seedFetched(t, s, drafts, 1, full)
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reducing a draft: %v", err)
	}
	if state, parts, _, _, _, _ := rawColumns(t, s, m.ID); state != "full" || parts != "[]" || !bytes.Equal(readRaw(t, s, "acc", m.ID), full) {
		t.Fatalf("after the refusal: %s %s", state, parts)
	}
	again := []byte("Subject: draft\r\n\r\nall the parts, again")
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, again)}); err != nil {
		t.Fatalf("a whole draft: %v", err)
	}
	if !bytes.Equal(readRaw(t, s, "acc", m.ID), again) {
		t.Fatal("whole commit not stored")
	}
}

// MarkPartsRemote repairs a row that calls stored what its file lacks:
// the parts join the remote set with their sizes, as a reduction's phase A
// would leave them, and the rest of the row stays.
func TestMarkPartsRemote(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedFetched(t, s, inbox, 1, []byte("Subject: x\r\n\r\nbody"))
	_, _, _, strippable, _, updated := rawColumns(t, s, m.ID)

	if err := s.MarkPartsRemote(ctx, "acc", m.ID, []string{"2", "9", "2"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, "acc", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RawState != RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) || got.RemoteBytes != 300<<10 ||
		!got.Attachments[0].Remote || got.Attachments[1].Remote {
		t.Fatalf("after marking part 2: %+v", got)
	}
	if _, _, _, st, _, up := rawColumns(t, s, m.ID); st != strippable || up != updated {
		t.Errorf("strippable %d → %d, updated_at %s → %s", strippable, st, updated, up)
	}
	// Again, and with another part: only what is new counts.
	if err := s.MarkPartsRemote(ctx, "acc", m.ID, []string{"2", "3"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, "acc", m.ID); !slices.Equal(got.RemoteParts, []string{"2", "3"}) || got.RemoteBytes != 300<<10+1<<10 {
		t.Fatalf("after marking part 3: %+v", got)
	}

	pending := seedRow(t, s, inbox, 2)
	out, _ := seedOutbox(t, s, "acc")
	for name, c := range map[string]struct {
		account, id string
		want        error
	}{
		"unknown":        {"acc", "m_nope", ErrNotFound},
		"other account":  {"other", m.ID, ErrNotFound},
		"body not there": {"acc", pending.ID, ErrConflict},
		"outbox":         {"acc", out.ID, ErrOutbox},
	} {
		if err := s.MarkPartsRemote(ctx, c.account, c.id, []string{"2"}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if state, _, _, _, _, _ := rawColumns(t, s, pending.ID); state != "full" {
		t.Errorf("pending message marked: %s", state)
	}
}

func TestCommitMessageRawRowVanishes(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	// Gone after phase A: nothing is written, and the old file goes with
	// the row.
	a := seedFetched(t, s, inbox, 1, []byte("Subject: a\r\n\r\nbody"))
	s.afterPhaseA = func() error { return s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{1}) }
	if _, err := s.CommitMessageRaw(ctx, "acc", a.ID, RawCommit{Source: stage(t, s, []byte("new"))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone after phase A: %v", err)
	}
	s.afterPhaseA = nil
	// Gone while the new file is written: the file just placed goes.
	s.SetRawCodec(RawZstd)
	b := seedFetched(t, s, inbox, 2, []byte("Subject: b\r\n\r\nbody"))
	s.createFile = func(path string) (rawFile, error) {
		s.createFile = nil
		if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{2}); err != nil {
			return nil, err
		}
		return s.createRawFile(path)
	}
	if _, err := s.CommitMessageRaw(ctx, "acc", b.ID, RawCommit{Source: stage(t, s, []byte("new"))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone during the write: %v", err)
	}
	for _, name := range dirNames(t, s.accountDir("acc")) {
		t.Errorf("left behind: %s", name)
	}
}

func TestCommitMessageRawWithBody(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedRow(t, s, inbox, 1)
	body := &BodyUpdate{Text: "hello body", Attachments: []api.Attachment{{PartID: "2", Size: 300 << 10, Remote: true}},
		HasAttachments: true, Size: 12345, RFCMessageID: "<ingest@example.invalid>"}
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")), Body: body,
		RemoteParts: []string{"2"}, RemoteBytes: 300 << 10, StrippableBytes: 0, Expect: RawExpect{BodyState: BodyNone}}); err != nil {
		t.Fatal(err)
	}
	text, _, state, err := s.GetMessageText(ctx, "acc", m.ID)
	if err != nil || text != "hello body" || state != BodyFetched {
		t.Errorf("body: %q %s %v", text, state, err)
	}
	got, _ := s.GetMessage(ctx, "acc", m.ID)
	if got.Size != 12345 || got.RFCMessageID != "<ingest@example.invalid>" || !got.Attachments[0].Remote || got.RawState != RawPartial {
		t.Errorf("row: %+v", got)
	}
	if js := attachmentsJSON(t, s, m.ID); strings.Contains(js, "remote") {
		t.Errorf("remote flag stored: %s", js)
	}
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("again")),
		Expect: RawExpect{BodyState: BodyNone}}); !errors.Is(err, ErrConflict) {
		t.Errorf("second ingest: %v", err)
	}
}

func TestMarkBodyStateResetsRawState(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedFetched(t, s, inbox, 1, []byte("Subject: x\r\n\r\nbody"))
	if _, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
		RemoteParts: []string{"2"}, RemoteBytes: 5, StrippableBytes: 0, Hydrated: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBodyState(ctx, m.ID, BodyFailed); err != nil {
		t.Fatal(err)
	}
	if state, parts, remoteBytes, strippable, hydrated, _ := rawColumns(t, s, m.ID); state != "full" || parts != "[]" ||
		remoteBytes != 0 || strippable != -1 || hydrated != "" {
		t.Errorf("after reset: %s %s %d %d %q", state, parts, remoteBytes, strippable, hydrated)
	}
}

func TestStoredAttachmentsNeverCarryRemote(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 1,
		Attachments: []api.Attachment{{PartID: "2", Filename: "a", Remote: true}}}
	if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if js := attachmentsJSON(t, s, m.ID); strings.Contains(js, "remote") {
		t.Errorf("upsert stored %s", js)
	}
	if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: m.Attachments}); err != nil {
		t.Fatal(err)
	}
	if js := attachmentsJSON(t, s, m.ID); strings.Contains(js, "remote") {
		t.Errorf("body stored %s", js)
	}
	if !m.Attachments[0].Remote {
		t.Error("the caller's slice was changed")
	}
	if got, _ := s.GetMessage(ctx, "acc", m.ID); got.Attachments[0].Remote {
		t.Error("remote without a remote part")
	}
}

func TestListStripCandidates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, id := range []string{"acc", "off"} {
		if err := s.AddAccount(ctx, &Account{ID: id, Config: api.AccountConfig{Email: id + "@example.invalid"}, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	s.SetAccountEnabled(ctx, "off", false)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	offInbox := seedFolder(t, s, "off", "INBOX", api.RoleInbox)
	day := func(month, d int) time.Time { return time.Date(2026, time.Month(month), d, 12, 0, 0, 0, time.UTC) }
	ids := map[string]string{}
	add := func(name string, f Folder, uid uint32, internal, date time.Time, fetched bool) {
		m := &Message{AccountID: f.AccountID, FolderID: f.ID, UID: uid, Subject: name, InternalDate: internal, Date: date}
		if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
			t.Fatal(err)
		}
		if fetched {
			if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: offlineAttachments}); err != nil {
				t.Fatal(err)
			}
		}
		ids[name] = m.ID
	}
	add("a", inbox, 1, day(1, 1), day(9, 1), true)
	add("b", inbox, 2, day(6, 1), day(1, 1), true)
	add("drafts", drafts, 3, day(1, 1), day(1, 1), true)
	add("pending", inbox, 0, day(1, 1), day(1, 1), true)
	add("disabled", offInbox, 4, day(1, 1), day(1, 1), true)
	add("nothing", inbox, 5, day(1, 1), day(1, 1), true)
	add("partial", inbox, 6, day(1, 1), day(1, 1), true)
	add("unfetched", inbox, 7, day(1, 1), day(1, 1), false)
	add("dated", inbox, 8, time.Time{}, day(2, 1), true)
	add("undated", inbox, 9, time.Time{}, time.Time{}, true)
	add("hydrated", inbox, 10, day(1, 2), day(1, 2), true)
	out, _ := seedOutbox(t, s, "acc")
	s.DB().Exec(`UPDATE messages SET strippable_bytes = 0 WHERE id = ?`, ids["nothing"])
	s.DB().Exec(`UPDATE messages SET raw_state = 'partial', remote_parts = '["2"]', remote_bytes = 5 WHERE id = ?`, ids["partial"])
	s.DB().Exec(`UPDATE messages SET hydrated_at = ? WHERE id = ?`, stamp(time.Now().Add(-24*time.Hour)), ids["hydrated"])

	names := func(q StripQuery) []string {
		t.Helper()
		got, err := s.ListStripCandidates(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range got {
			for name, id := range ids {
				if id == c.ID {
					out = append(out, name)
				}
			}
			if c.Role != api.RoleInbox {
				t.Errorf("%s: role %s", c.ID, c.Role)
			}
		}
		return out
	}
	grace := time.Now().Add(-7 * 24 * time.Hour)
	for _, c := range []struct {
		q    StripQuery
		want []string
	}{
		{StripQuery{Cutoff: day(3, 1), HydratedBefore: grace}, []string{"a", "dated"}},
		{StripQuery{Cutoff: day(7, 1), HydratedBefore: grace}, []string{"a", "dated", "b"}},
		{StripQuery{All: true, HydratedBefore: grace}, []string{"a", "dated", "b", "undated"}},
		{StripQuery{All: true}, []string{"a", "dated", "b", "undated"}},
		{StripQuery{All: true, HydratedBefore: time.Now()}, []string{"a", "hydrated", "dated", "b", "undated"}},
		{StripQuery{All: true, HydratedBefore: grace, Limit: 1}, []string{"a"}},
	} {
		if got := names(c.q); !slices.Equal(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.q, got, c.want)
		}
	}
	_ = out
}

func TestClassifySmall(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	add := func(uid uint32, atts []api.Attachment, fetched bool) string {
		m := seedRow(t, s, inbox, uid)
		if fetched {
			if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: atts}); err != nil {
				t.Fatal(err)
			}
		}
		return m.ID
	}
	small := add(1, []api.Attachment{{PartID: "2", Size: 1000}, {PartID: "3", Size: 99 << 10}}, true)
	none := add(2, nil, true)
	large := add(3, []api.Attachment{{PartID: "2", Size: 1000}, {PartID: "3", Size: 100 << 10}}, true)
	unfetched := add(4, []api.Attachment{{PartID: "2", Size: 1}}, false)
	evaluated := add(5, []api.Attachment{{PartID: "2", Size: 1}}, true)
	s.SetStrippableBytes(ctx, evaluated, 42)
	n, err := s.ClassifySmall(ctx, api.LargeAttachmentMinBytes)
	if err != nil || n != 2 {
		t.Fatalf("classified %d, %v", n, err)
	}
	for id, want := range map[string]int64{small: 0, none: 0, large: -1, unfetched: -1, evaluated: 42} {
		if _, _, _, got, _, _ := rawColumns(t, s, id); got != want {
			t.Errorf("%s: strippable %d, want %d", id, got, want)
		}
	}
	if n, _ := s.ClassifySmall(ctx, api.LargeAttachmentMinBytes); n != 0 {
		t.Errorf("second pass classified %d", n)
	}
	if err := s.SetStrippableBytes(ctx, "m_nope", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := s.SetStrippableBytes(ctx, small, -2); err == nil {
		t.Error("-2 accepted")
	}
}

func TestMessageServerLocation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	imapMsg := seedRow(t, s, inbox, 5)
	loc, err := s.MessageServerLocation(ctx, "acc", imapMsg.ID)
	if err != nil || loc.Folder.ID != inbox.ID || loc.Folder.Mailbox != "INBOX" || loc.UID != 5 || loc.RemoteID != "" {
		t.Errorf("imap: %+v %v", loc, err)
	}
	graphMsg := seedRemote(t, s, inbox, "AAMkRemote", "remote", time.Now())
	if loc, err := s.MessageServerLocation(ctx, "acc", graphMsg.ID); err != nil || loc.RemoteID != "AAMkRemote" || loc.Folder.ID != inbox.ID {
		t.Errorf("graph: %+v %v", loc, err)
	}
	// Moved locally, the move not pushed yet: the server still has it where
	// the operation's snapshot says.
	if err := s.MoveMessages(ctx, "acc", []string{imapMsg.ID}, archive.ID); err != nil {
		t.Fatal(err)
	}
	if loc, err := s.MessageServerLocation(ctx, "acc", imapMsg.ID); err != nil || loc.Folder.ID != inbox.ID || loc.UID != 5 {
		t.Errorf("pending move: %+v %v", loc, err)
	}
	// Pushed but not reconciled: nowhere known.
	ops, _ := s.NextOps(ctx, "acc", time.Now(), 10)
	for _, op := range ops {
		s.MarkOpDone(ctx, op.ID)
	}
	if _, err := s.MessageServerLocation(ctx, "acc", imapMsg.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unreconciled: %v", err)
	}
	if _, err := s.MessageServerLocation(ctx, "other", graphMsg.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign: %v", err)
	}
}

func TestRemoteStats(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	folders := map[string]Folder{}
	for _, acc := range []string{"acc", "other"} {
		folders[acc] = seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	}
	for i, acc := range []string{"acc", "acc", "other"} {
		m := seedFetched(t, s, folders[acc], uint32(i+1), []byte(fmt.Sprintf("Subject: %d\r\n\r\nbody", i)))
		if _, err := s.CommitMessageRaw(ctx, acc, m.ID, RawCommit{Source: stage(t, s, []byte("skeleton")),
			RemoteParts: []string{"2"}, RemoteBytes: int64(i+1) * 1000}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.RemoteStats(ctx, "")
	if err != nil || all != (RemoteStats{Messages: 3, Bytes: 6000}) {
		t.Errorf("all: %+v %v", all, err)
	}
	if one, _ := s.RemoteStats(ctx, "acc"); one != (RemoteStats{Messages: 2, Bytes: 3000}) {
		t.Errorf("acc: %+v", one)
	}
}

func TestListUnfetchedDates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	internal := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	header := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	m := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 1, InternalDate: internal, Date: header}
	undated := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 2}
	if err := s.UpsertMessages(ctx, []*Message{m, undated}); err != nil {
		t.Fatal(err)
	}
	refs, err := s.ListUnfetched(ctx, inbox.ID, 10)
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs %+v %v", refs, err)
	}
	for _, r := range refs {
		switch r.ID {
		case m.ID:
			if !r.InternalDate.Equal(internal) || !r.Date.Equal(header) {
				t.Errorf("dates %+v", r)
			}
		case undated.ID:
			if !r.InternalDate.IsZero() || !r.Date.IsZero() {
				t.Errorf("undated %+v", r)
			}
		}
	}
}
