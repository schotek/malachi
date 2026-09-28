// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// blockStaging puts a file where the staging directory is: from then on
// staging a message on disk fails, so a test that still stores one proves
// that nothing was staged on disk.
func blockStaging(t *testing.T, s *Store) {
	t.Helper()
	if err := os.RemoveAll(s.stagingDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.stagingDir(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := s.StageRaw(context.Background(), 0); err == nil {
		st.Remove()
		t.Fatal("staged on disk without a staging directory")
	}
}

// A message staged in memory takes and reads back its bytes the way one
// staged on disk does, within its limit, and creates nothing.
func TestStageMemory(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	blockStaging(t, s)

	st := s.StageMemory(ctx, 10, 0)
	if _, err := st.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ReadFrom(strings.NewReader("defg")); err != nil || n != 4 {
		t.Fatalf("read from: %d %v", n, err)
	}
	for i := range 2 {
		got, err := io.ReadAll(st.Reader())
		if err != nil || string(got) != "abcdefg" || st.Size() != 7 {
			t.Fatalf("reader %d: %q %v size %d", i, got, err, st.Size())
		}
	}
	// A reader keeps what was staged when it was made.
	r := st.Reader()
	if _, err := st.Write([]byte("h")); err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(r); string(got) != "abcdefg" {
		t.Errorf("earlier reader %q", got)
	}
	if got := st.Bytes(); string(got) != "abcdefgh" || cap(got) != len(got) {
		t.Errorf("bytes %q (cap %d)", got, cap(got))
	}
	// Past the limit nothing more is taken, and it sticks.
	if n, err := st.ReadFrom(strings.NewReader("ijk")); !errors.Is(err, ErrTooBig) || n != 0 {
		t.Errorf("over the limit: %d %v", n, err)
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, ErrTooBig) {
		t.Errorf("after the limit: %v", err)
	}
	if err := st.usable(); !errors.Is(err, ErrTooBig) {
		t.Errorf("an overflowed stage is usable: %v", err)
	}
	if err := st.Remove(); err != nil || st.Bytes() != nil {
		t.Errorf("remove: %v, bytes %q", err, st.Bytes())
	}
	if err := st.Remove(); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, errStagedDone) {
		t.Errorf("write after remove: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	st2 := s.StageMemory(cctx, 0, 0)
	defer st2.Remove()
	if st2.limit != MaxRawBytes {
		t.Errorf("default limit %d", st2.limit)
	}
	cancel()
	if _, err := st2.ReadFrom(strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	// Nothing staged yet is an empty message, not none.
	if got := s.StageMemory(ctx, 0, 0).Bytes(); got == nil || len(got) != 0 {
		t.Errorf("empty stage: %q", got)
	}
	// An announced size is room taken up front, within the limit; the
	// bytes handed out still end where the message does.
	if st := s.StageMemory(ctx, 10, 1<<20); cap(st.mem) != 10 {
		t.Errorf("room over the limit: %d", cap(st.mem))
	}
	hinted := s.StageMemory(ctx, 0, 4096)
	if _, err := hinted.Write([]byte("abc")); err != nil || cap(hinted.mem) != 4096 {
		t.Fatalf("announced room: %d %v", cap(hinted.mem), err)
	}
	if got := hinted.Bytes(); string(got) != "abc" || cap(got) != 3 {
		t.Errorf("hinted bytes %q (cap %d)", got, cap(got))
	}
	if info, err := os.Stat(s.stagingDir()); err != nil || info.IsDir() {
		t.Errorf("the staging area was touched: %v", err)
	}
}

// A commit writes the message's own file from memory, in either codec, and
// leaves the staged bytes as they were: nothing but the message's file
// reaches the disk.
func TestCommitStagedInMemory(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedRow(t, s, inbox, 1)
	blockStaging(t, s)
	data := rawContent(200<<10, 5)
	for _, codec := range []RawCodec{RawZstd, RawPlain} {
		s.SetRawCodec(codec)
		st := s.StageMemory(ctx, 0, 0)
		if _, err := st.ReadFrom(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		n, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: st, StrippableBytes: StrippableUnknown})
		if err != nil || n != int64(len(data)) {
			t.Fatalf("%s: commit %d %v", codec, n, err)
		}
		if !bytes.Equal(st.Bytes(), data) {
			t.Errorf("%s: the commit used up the staged bytes", codec)
		}
		if err := st.Remove(); err != nil {
			t.Errorf("%s: remove after commit: %v", codec, err)
		}
		if got := readRaw(t, s, "acc", m.ID); !bytes.Equal(got, data) {
			t.Errorf("%s: content differs", codec)
		}
		if files, _ := statRaw(filepath.Join(s.MessageDir(), "acc"), m.ID); files.both() || files.of(codec) == nil {
			t.Errorf("%s: files %+v", codec, files)
		}
	}
	if names := dirNames(t, filepath.Join(s.MessageDir(), "acc")); len(names) != 1 {
		t.Errorf("message directory %v", names)
	}
}

// A message never to be reduced (StrippableNever) is a candidate under no
// query; AnyHydrated takes one downloaded a moment ago too.
func TestStrippableNever(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.AddAccount(ctx, &Account{ID: "acc", Config: api.AccountConfig{Email: "acc@example.invalid"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids := map[string]string{}
	day := 0
	add := func(name string, strippable int64, hydrated time.Time) {
		day++
		at := time.Date(2026, 1, day, 12, 0, 0, 0, time.UTC)
		m := &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(day), Subject: name, InternalDate: at, Date: at}
		if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: offlineAttachments}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE messages SET strippable_bytes = ?, hydrated_at = ? WHERE id = ?`,
			strippable, optStamp(hydrated), m.ID); err != nil {
			t.Fatal(err)
		}
		ids[name] = m.ID
	}
	add("never", StrippableNever, time.Time{})
	add("nothing", 0, time.Time{})
	add("unknown", StrippableUnknown, time.Time{})
	add("candidates", 5000, time.Time{})
	add("hydrated", 5000, time.Now().Add(-time.Hour))
	add("hydrated never", StrippableNever, time.Now().Add(-time.Hour))

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
		}
		return out
	}
	grace := time.Now().Add(-7 * 24 * time.Hour)
	if got, want := names(StripQuery{All: true, HydratedBefore: grace}), []string{"unknown", "candidates"}; !slices.Equal(got, want) {
		t.Errorf("all: %v, want %v", got, want)
	}
	if got, want := names(StripQuery{All: true, AnyHydrated: true}), []string{"unknown", "candidates", "hydrated"}; !slices.Equal(got, want) {
		t.Errorf("any hydrated: %v, want %v", got, want)
	}
	if got, want := names(StripQuery{Cutoff: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), AnyHydrated: true}),
		[]string{"unknown", "candidates", "hydrated"}; !slices.Equal(got, want) {
		t.Errorf("cutoff, any hydrated: %v, want %v", got, want)
	}

	if err := s.SetStrippableBytes(ctx, ids["unknown"], StrippableNever); err != nil {
		t.Fatalf("set never: %v", err)
	}
	if got := names(StripQuery{All: true, AnyHydrated: true}); slices.Contains(got, "unknown") {
		t.Errorf("a message never to be reduced is listed: %v", got)
	}
	if err := s.SetStrippableBytes(ctx, ids["unknown"], StrippableNever-1); err == nil {
		t.Error("-3 accepted")
	}
	st := s.StageMemory(ctx, 0, 0)
	defer st.Remove()
	if _, err := st.Write([]byte("Subject: x\r\n\r\nbody")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitMessageRaw(ctx, "acc", ids["candidates"], RawCommit{Source: st, StrippableBytes: StrippableNever}); err != nil {
		t.Fatalf("commit never: %v", err)
	}
	if _, _, _, got, _, _ := rawColumns(t, s, ids["candidates"]); got != StrippableNever {
		t.Errorf("committed strippable %d", got)
	}
}

// seedSettled stores rows in every state ReevaluateSettled tells apart,
// by name; only "small" and "partial holding" are to be judged again.
func seedSettled(t *testing.T, s *Store, inbox Folder) map[string]string {
	t.Helper()
	ctx := context.Background()
	add := func(uid uint32, atts []api.Attachment, strippable int64, extra string) string {
		m := seedRow(t, s, inbox, uid)
		if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: atts}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE messages SET strippable_bytes = ?`+extra+` WHERE id = ?`, strippable, m.ID); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	small := []api.Attachment{{PartID: "2", Filename: "a.txt", ContentType: "text/plain", Size: 1000}}
	ids := map[string]string{
		"small":     add(1, small, 0, ""),
		"none":      add(2, nil, 0, ""),
		"empty":     add(3, []api.Attachment{{PartID: "2", Filename: "e.txt", ContentType: "text/plain", Size: 0}}, 0, ""),
		"never":     add(4, small, StrippableNever, ""),
		"counted":   add(5, small, 42, ""),
		"unknown":   add(6, small, StrippableUnknown, ""),
		"partial":   add(7, small, 0, `, raw_state = 'partial', remote_parts = '["2"]', remote_bytes = 1000`),
		"unfetched": add(8, small, 0, `, body_state = 'none'`),
		"partial holding": add(9, []api.Attachment{small[0], {PartID: "3", Filename: "b.pdf", ContentType: "application/pdf", Size: 5000}}, 0,
			`, raw_state = 'partial', remote_parts = '["3"]', remote_bytes = 5000`),
	}
	return ids
}

// settledAfter is what seedSettled's rows say once "small" and "partial
// holding" (a partial message whose file still holds a.txt) were judged
// again, and only they.
var settledAfter = map[string]int64{"small": StrippableUnknown, "none": 0, "empty": 0, "never": StrippableNever,
	"counted": 42, "unknown": StrippableUnknown, "partial": 0, "unfetched": 0, "partial holding": StrippableUnknown}

// ReevaluateSettled makes the settled messages that list an attachment of
// the given size be judged again, and leaves every other row alone.
func TestReevaluateSettled(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids := seedSettled(t, s, inbox)
	n, err := s.ReevaluateSettled(ctx, 1)
	if err != nil || n != 2 {
		t.Fatalf("re-evaluated %d, %v", n, err)
	}
	for name, want := range settledAfter {
		if _, _, _, got, _, _ := rawColumns(t, s, ids[name]); got != want {
			t.Errorf("%s: strippable %d, want %d", name, got, want)
		}
	}
	if n, err := s.ReevaluateSettled(ctx, 1); err != nil || n != 0 {
		t.Errorf("second pass: %d %v", n, err)
	}

	// More rows than one slice of the key holds.
	rows := make([]*Message, classifyBatch+17)
	for i := range rows {
		rows[i] = &Message{AccountID: "acc", FolderID: inbox.ID, UID: uint32(100 + i), Subject: fmt.Sprintf("bulk %d", i)}
	}
	if err := s.UpsertMessages(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE messages SET body_state = 'fetched', strippable_bytes = 0,
		attachments_json = '[{"partId":"2","filename":"a.txt","contentType":"text/plain","size":7}]'
		WHERE subject LIKE 'bulk %'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReevaluateSettled(ctx, 1); err != nil || n != len(rows) {
		t.Fatalf("bulk: %d %v, want %d", n, err, len(rows))
	}
	// A threshold above every attachment leaves them all.
	if _, err := s.DB().Exec(`UPDATE messages SET strippable_bytes = 0 WHERE subject LIKE 'bulk %'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReevaluateSettled(ctx, api.LargeAttachmentMinBytes); err != nil || n != 0 {
		t.Fatalf("large threshold: %d %v", n, err)
	}
}

// A picture the HTML shows is listed as an attachment, so the threshold of
// rule 3 of NeverStoreAttachments (api.LargeAttachmentMinBytes) makes a
// settled message whose file holds a large one be judged again, whole or
// partial; one whose large picture is on the server already, one whose
// pictures are small and one never to be reduced keep their mark.
func TestReevaluateSettledShownPictures(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	add := func(uid uint32, atts []api.Attachment, strippable int64, extra string) string {
		m := seedRow(t, s, inbox, uid)
		if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: atts}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE messages SET strippable_bytes = ?`+extra+` WHERE id = ?`, strippable, m.ID); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	photo := api.Attachment{PartID: "1.2", Filename: "photo.jpg", ContentType: "image/jpeg", Size: api.LargeAttachmentMinBytes,
		Inline: true, ContentID: "photo@example.org"}
	icon := api.Attachment{PartID: "1.3", Filename: "icon.png", ContentType: "image/png", Size: api.LargeAttachmentMinBytes - 1,
		Inline: true, ContentID: "icon@example.org"}
	pdf := api.Attachment{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 5000}
	ids := map[string]string{
		"whole large": add(1, []api.Attachment{photo, icon}, 0, ""),
		"whole small": add(2, []api.Attachment{icon}, 0, ""),
		"partial large": add(3, []api.Attachment{photo, icon, pdf}, 0,
			`, raw_state = 'partial', remote_parts = '["2"]', remote_bytes = 5000`),
		"partial gone": add(4, []api.Attachment{photo, icon, pdf}, 0,
			fmt.Sprintf(`, raw_state = 'partial', remote_parts = '["1.2","2"]', remote_bytes = %d`, api.LargeAttachmentMinBytes+5000)),
		"never": add(5, []api.Attachment{photo}, StrippableNever, ""),
	}
	want := map[string]int64{"whole large": StrippableUnknown, "whole small": 0, "partial large": StrippableUnknown,
		"partial gone": 0, "never": StrippableNever}
	if n, err := s.ReevaluateSettled(ctx, api.LargeAttachmentMinBytes); err != nil || n != 2 {
		t.Fatalf("re-evaluated %d, %v", n, err)
	}
	for name, w := range want {
		if _, _, _, got, _, _ := rawColumns(t, s, ids[name]); got != w {
			t.Errorf("%s: strippable %d, want %d", name, got, w)
		}
	}
	if n, err := s.ReevaluateSettled(ctx, api.LargeAttachmentMinBytes); err != nil || n != 0 {
		t.Errorf("second pass: %d %v", n, err)
	}
}

// ReevaluateSettledMessage does for one message what ReevaluateSettled
// does for all: every row but "small" and "partial holding" stays as it
// is.
func TestReevaluateSettledMessage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids := seedSettled(t, s, inbox)
	for name, id := range ids {
		marked, err := s.ReevaluateSettledMessage(ctx, id, 1)
		if err != nil || marked != (name == "small" || name == "partial holding") {
			t.Errorf("%s: marked %v, %v", name, marked, err)
		}
	}
	for name, want := range settledAfter {
		if _, _, _, got, _, _ := rawColumns(t, s, ids[name]); got != want {
			t.Errorf("%s: strippable %d, want %d", name, got, want)
		}
	}
	if marked, err := s.ReevaluateSettledMessage(ctx, ids["small"], 1); err != nil || marked {
		t.Errorf("second time: %v %v", marked, err)
	}
	if marked, err := s.ReevaluateSettledMessage(ctx, "no such message", 1); err != nil || marked {
		t.Errorf("unknown id: %v %v", marked, err)
	}
}

// With Partial the pass lists the partial messages whose file still holds
// a non-empty attachment, oldest first, whatever their age: neither those
// with nothing left, settled (0) or never to be reduced, nor Drafts, a
// message without a server copy or of a paused account; and no message
// stored whole. Without it, no partial message.
func TestListStripCandidatesPartial(t *testing.T) {
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
	atts := []api.Attachment{
		{PartID: "1.2", Filename: "logo.png", ContentType: "image/png", Size: 0},
		{PartID: "2", Filename: "big.pdf", ContentType: "application/pdf", Size: 300 << 10},
		{PartID: "3", Filename: "small.txt", ContentType: "text/plain", Size: 1 << 10},
	}
	ids := map[string]string{}
	day := 0
	add := func(name string, f Folder, uid uint32, remote string, strippable int64) {
		day++
		at := time.Date(2026, 1, day, 12, 0, 0, 0, time.UTC)
		m := &Message{AccountID: f.AccountID, FolderID: f.ID, UID: uid, Subject: name, InternalDate: at, Date: at}
		if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMessageBody(ctx, m.ID, BodyUpdate{Attachments: atts}); err != nil {
			t.Fatal(err)
		}
		state := RawPartial
		if remote == "[]" {
			state = RawFull
		}
		if _, err := s.DB().Exec(`UPDATE messages SET raw_state = ?, remote_parts = ?, remote_bytes = ?, strippable_bytes = ? WHERE id = ?`,
			string(state), remote, len(remote), strippable, m.ID); err != nil {
			t.Fatal(err)
		}
		ids[name] = m.ID
	}
	add("small stored", inbox, 1, `["2"]`, 300<<10)
	add("not evaluated", inbox, 2, `["2"]`, StrippableUnknown)
	add("all remote", inbox, 3, `["2","3"]`, 300<<10)
	add("settled", inbox, 4, `["2"]`, 0)
	add("never", inbox, 5, `["2"]`, StrippableNever)
	add("draft", drafts, 6, `["2"]`, 300<<10)
	add("no server copy", inbox, 0, `["2"]`, 300<<10)
	add("paused account", offInbox, 7, `["2"]`, 300<<10)
	add("whole", inbox, 8, `[]`, 300<<10)

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
		}
		return out
	}
	if got, want := names(StripQuery{Partial: true}), []string{"small stored", "not evaluated"}; !slices.Equal(got, want) {
		t.Errorf("partial: %v, want %v", got, want)
	}
	if got, want := names(StripQuery{Partial: true, Limit: 1}), []string{"small stored"}; !slices.Equal(got, want) {
		t.Errorf("partial, limit 1: %v, want %v", got, want)
	}
	if got, want := names(StripQuery{All: true, AnyHydrated: true}), []string{"whole"}; !slices.Equal(got, want) {
		t.Errorf("whole: %v, want %v", got, want)
	}
}
