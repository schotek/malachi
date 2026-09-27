// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestStageRaw(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	st, err := s.StageRaw(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(st.path) != s.stagingDir() {
		t.Errorf("staged at %s", st.path)
	}
	if info, err := os.Stat(s.stagingDir()); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("staging dir: %v %v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(st.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("staged file: %v %v", info.Mode().Perm(), err)
	}
	if _, err := st.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ReadFrom(strings.NewReader("defg")); err != nil || n != 4 {
		t.Fatalf("read from: %d %v", n, err)
	}
	for i := 0; i < 2; i++ {
		got, err := io.ReadAll(st.Reader())
		if err != nil || string(got) != "abcdefg" || st.Size() != 7 {
			t.Fatalf("reader %d: %q %v size %d", i, got, err, st.Size())
		}
	}
	// Past the limit nothing more is taken, and it sticks.
	if n, err := st.ReadFrom(strings.NewReader("hijk")); !errors.Is(err, ErrTooBig) || n != 0 {
		t.Errorf("over the limit: %d %v", n, err)
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, ErrTooBig) {
		t.Errorf("after the limit: %v", err)
	}
	if err := st.usable(); !errors.Is(err, ErrTooBig) {
		t.Errorf("an overflowed stage is usable: %v", err)
	}
	if err := st.Remove(); err != nil || fileExists(t, st.path) {
		t.Errorf("remove: %v", err)
	}
	if err := st.Remove(); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, errStagedDone) {
		t.Errorf("write after remove: %v", err)
	}
	// The staging context bounds ReadFrom.
	cctx, cancel := context.WithCancel(ctx)
	st2, err := s.StageRaw(cctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Remove()
	if st2.limit != MaxRawBytes {
		t.Errorf("default limit %d", st2.limit)
	}
	cancel()
	if _, err := st2.ReadFrom(strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestStagingSweptAtOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	log := slog.New(slog.DiscardHandler)
	s, err := Open(ctx, path, log)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.StageRaw(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	st.Write([]byte("left behind by a crash"))
	s.Close()
	s, err = Open(ctx, path, log)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if names := dirNames(t, s.stagingDir()); len(names) != 0 {
		t.Errorf("staging after open: %v", names)
	}
}

func TestCommitStagedCompressed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedRow(t, s, inbox, 1)
	data := rawContent(200<<10, 4)
	for _, codec := range []RawCodec{RawZstd, RawPlain} {
		s.SetRawCodec(codec)
		st, err := s.StageRaw(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ReadFrom(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		n, err := s.CommitMessageRaw(ctx, "acc", m.ID, RawCommit{Source: st, StrippableBytes: -1})
		if err != nil || n != int64(len(data)) {
			t.Fatalf("%s: commit %d %v", codec, n, err)
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
	if names := dirNames(t, s.stagingDir()); len(names) != 0 {
		t.Errorf("staging left: %v", names)
	}
}
