// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// seedRawMessages stores n messages with raw files of various sizes in the
// store's current codec and returns their ids and the SHA-256 of each.
func seedRawMessages(t *testing.T, s *Store, f Folder, n int) ([]string, map[string][32]byte) {
	t.Helper()
	ids := make([]string, 0, n)
	sums := map[string][32]byte{}
	for i := 0; i < n; i++ {
		m := seedRow(t, s, f, uint32(1000+i))
		data := rawContent([]int{0, 17, 4 << 10, 200 << 10}[i%4]+i, int64(i))
		if _, err := s.PutMessageRaw(context.Background(), f.AccountID, m.ID, RawWrite{Size: int64(len(data))}, chunked(data)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		sums[m.ID] = sha256.Sum256(data)
	}
	return ids, sums
}

// convertAll runs ConvertRawBatch until the pass is done.
func convertAll(t *testing.T, s *Store, to RawCodec, batch int) ConvertResult {
	t.Helper()
	var total ConvertResult
	cursor := ""
	for i := 0; ; i++ {
		last, res, err := s.ConvertRawBatch(context.Background(), cursor, to, batch)
		if err != nil {
			t.Fatalf("convert to %s: %v", to, err)
		}
		total.Visited += res.Visited
		total.Converted += res.Converted
		total.Busy += res.Busy
		total.Corrupt += res.Corrupt
		total.Missing += res.Missing
		total.Failed += res.Failed
		if last == "" {
			return total
		}
		if i > 1000 {
			t.Fatal("conversion does not end")
		}
		cursor = last
	}
}

func TestConvertRawBatchBothWays(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 9)
	out, _ := seedOutbox(t, s, "acc")
	dir := filepath.Join(s.MessageDir(), "acc")

	check := func(want RawCodec) {
		t.Helper()
		for _, id := range ids {
			files, err := statRaw(dir, id)
			if err != nil || files.both() || files.of(want) == nil {
				t.Fatalf("%s: files %+v %v", id, files, err)
			}
			if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
				t.Errorf("%s: content changed in %s", id, want)
			}
			info, _ := files.info(dir, id)
			if c, b, d, ok := fileRow(t, s, id); !ok || c != want.String() || b != info.Bytes || d != info.DiskBytes {
				t.Errorf("%s: accounting %s %d %d %v, file %+v", id, c, b, d, ok, info)
			}
		}
		// The outbox stays plain whatever the codec.
		if !fileExists(t, s.MessageRawPath("acc", out.ID)) || fileExists(t, s.MessageRawPath("acc", out.ID)+RawZstSuffix) {
			t.Errorf("outbox message converted")
		}
		for _, name := range dirNames(t, dir) {
			if filepath.Ext(name) == tmpSuffix {
				t.Errorf("left behind: %s", name)
			}
		}
	}

	s.SetRawCodec(RawZstd)
	res := convertAll(t, s, RawZstd, 4)
	if res.Converted != len(ids) || res.Visited != len(ids) || res.Busy+res.Corrupt+res.Missing+res.Failed != 0 {
		t.Errorf("to zstd: %+v", res)
	}
	check(RawZstd)
	// Nothing left: a new pass ends at once.
	if last, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 4); last != "" || res.Visited != 0 || err != nil {
		t.Errorf("second pass: %q %+v %v", last, res, err)
	}

	s.SetRawCodec(RawPlain)
	res = convertAll(t, s, RawPlain, 0)
	if res.Converted != len(ids) {
		t.Errorf("to plain: %+v", res)
	}
	check(RawPlain)

	// The pass is refused, or stops, when the codec is not its target.
	if _, _, err := s.ConvertRawBatch(ctx, "", RawZstd, 4); !errors.Is(err, ErrConflict) {
		t.Errorf("codec is plain: %v", err)
	}
}

func TestConvertRawBatchStopsOnCodecChange(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 6)
	s.SetRawCodec(RawZstd)
	made := 0
	s.createFile = func(path string) (rawFile, error) {
		if made++; made == 2 {
			s.SetRawCodec(RawPlain)
		}
		return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	}
	last, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 10)
	s.createFile = nil
	// The batch goes in id order; it stops after the second message.
	if second := slices.Sorted(slices.Values(ids))[1]; !errors.Is(err, ErrConflict) || last != second || res.Converted != 2 {
		t.Errorf("stopped at %q (want %q): %+v %v", last, second, res, err)
	}
	// What was converted is finished; the rest is untouched.
	dir := filepath.Join(s.MessageDir(), "acc")
	zstd := 0
	for _, id := range ids {
		files, _ := statRaw(dir, id)
		if files.both() {
			t.Errorf("%s: both variants", id)
		}
		if files.zst != nil {
			zstd++
		}
		if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
			t.Errorf("%s: content changed", id)
		}
	}
	if zstd != 2 {
		t.Errorf("%d compressed, want 2", zstd)
	}
}

func TestConvertRawBatchKeepsDamagedSource(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	s.SetRawCodec(RawZstd)
	ids, _ := seedRawMessages(t, s, inbox, 4)
	zst := s.MessageRawPath("acc", ids[3]) + RawZstSuffix
	data, _ := os.ReadFile(zst)
	data[len(data)/2] ^= 0x10
	os.WriteFile(zst, data, 0o600)
	before, _ := os.Stat(zst)

	s.SetRawCodec(RawPlain)
	res := convertAll(t, s, RawPlain, 0)
	if res.Corrupt != 1 || res.Converted != 3 {
		t.Errorf("result %+v", res)
	}
	after, err := os.Stat(zst)
	if err != nil || !os.SameFile(before, after) || fileExists(t, s.MessageRawPath("acc", ids[3])) {
		t.Errorf("damaged source not kept as it is: %v", err)
	}
	if c, _, _, _ := fileRow(t, s, ids[3]); c != "zstd" {
		t.Errorf("accounting of the damaged file: %s", c)
	}
}

func TestConvertRawBatchSameFileGuard(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 3)
	target, replaced := ids[0], ids[1]
	s.SetRawCodec(RawZstd)
	s.betweenConvertPhases = func() {
		// Another writer replaces one message's source and rewrites the
		// other between the phases.
		tmp := s.MessageRawPath("acc", replaced) + ".new"
		os.WriteFile(tmp, []byte("Subject: newer\r\n\r\nbody"), 0o600)
		os.Rename(tmp, s.MessageRawPath("acc", replaced))
		if _, err := s.PutMessageRaw(ctx, "acc", target, RawWrite{}, chunked([]byte("Subject: rewritten\r\n\r\nbody"))); err != nil {
			t.Error(err)
		}
	}
	_, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 10)
	s.betweenConvertPhases = nil
	if err != nil || res.Converted != 1 {
		t.Errorf("result %+v %v", res, err)
	}
	// The message whose source changed keeps both files for the sweep; the
	// rewritten one is the writer's.
	if files, _ := statRaw(filepath.Join(s.MessageDir(), "acc"), replaced); !files.both() {
		t.Errorf("replaced source removed: %+v", files)
	}
	if got := readRaw(t, s, "acc", target); string(got) != "Subject: rewritten\r\n\r\nbody" {
		t.Errorf("rewritten message: %q", got)
	}
	if sha256.Sum256(readRaw(t, s, "acc", ids[2])) != sums[ids[2]] {
		t.Error("untouched message changed")
	}
	// The sweep keeps the newer file of the pair.
	os.Chtimes(s.MessageRawPath("acc", replaced)+RawZstSuffix, time.Now().Add(-time.Minute), time.Now().Add(-time.Minute))
	if res, err := s.SweepMessageFiles(ctx, time.Hour); err != nil || res.Resolved != 1 {
		t.Errorf("sweep: %+v %v", res, err)
	}
	if got := readRaw(t, s, "acc", replaced); string(got) != "Subject: newer\r\n\r\nbody" {
		t.Errorf("after the sweep: %q", got)
	}
}

// fullFile is a file that runs out of space after left bytes.
type fullFile struct {
	*os.File
	left int
}

func (f *fullFile) Write(p []byte) (int, error) {
	if len(p) > f.left {
		n, _ := f.File.Write(p[:f.left])
		f.left = 0
		return n, &fs.PathError{Op: "write", Path: f.Name(), Err: syscall.ENOSPC}
	}
	f.left -= len(p)
	return f.File.Write(p)
}

func TestConvertRawBatchNoSpace(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, sums := seedRawMessages(t, s, inbox, 4)
	s.SetRawCodec(RawZstd)
	s.createFile = func(path string) (rawFile, error) {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		return &fullFile{File: f, left: 0}, nil
	}
	last, res, err := s.ConvertRawBatch(ctx, "", RawZstd, 10)
	s.createFile = nil
	if !errors.Is(err, ErrNoSpace) || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("no space: %v", err)
	}
	if last != "" || res.Converted != 0 {
		t.Errorf("stopped at %q: %+v", last, res)
	}
	dir := filepath.Join(s.MessageDir(), "acc")
	for _, id := range ids {
		if files, _ := statRaw(dir, id); files.zst != nil || files.plain == nil {
			t.Errorf("%s: %+v", id, files)
		}
		if sha256.Sum256(readRaw(t, s, "acc", id)) != sums[id] {
			t.Errorf("%s: content changed", id)
		}
	}
	for _, name := range dirNames(t, dir) {
		if filepath.Ext(name) == tmpSuffix {
			t.Errorf("left behind: %s", name)
		}
	}
	// The same for a write: ErrNoSpace, and the earlier file stays.
	s.createFile = func(path string) (rawFile, error) {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		return &fullFile{File: f, left: 100}, nil
	}
	defer func() { s.createFile = nil }()
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		s.SetRawCodec(codec)
		big := rawContent(300<<10, 11)
		for _, size := range []int64{0, int64(len(big))} {
			_, err := s.PutMessageRaw(ctx, "acc", ids[0], RawWrite{Size: size}, chunked(big))
			if !errors.Is(err, ErrNoSpace) {
				t.Errorf("%s size %d: %v", codec, size, err)
			}
		}
		if sha256.Sum256(readRaw(t, s, "acc", ids[0])) != sums[ids[0]] {
			t.Errorf("%s: earlier file changed", codec)
		}
	}
	st, err := s.StageRaw(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(bytes.Repeat([]byte("x"), 200)); !errors.Is(err, ErrNoSpace) {
		t.Errorf("staging: %v", err)
	}
	if err := st.usable(); !errors.Is(err, ErrNoSpace) {
		t.Errorf("a staged message short of space is committed: %v", err)
	}
	st.Remove()
}

func TestConvertRawBatchMissingAndStaleRows(t *testing.T) {
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	ids, _ := seedRawMessages(t, s, inbox, 3)
	// One file vanished outside the daemon, one row lags behind its file.
	os.Remove(s.MessageRawPath("acc", ids[0]))
	s.SetRawCodec(RawZstd)
	if err := s.WithMessageRaw(context.Background(), "acc", ids[1], func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{}, RawProducer(chunked([]byte("Subject: z\r\n\r\nbody"))))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE message_files SET codec = 'plain' WHERE message_id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	res := convertAll(t, s, RawZstd, 0)
	if res.Missing != 1 || res.Converted != 2 {
		t.Errorf("result %+v", res)
	}
	if _, _, _, ok := fileRow(t, s, ids[0]); ok {
		t.Error("row of the missing file kept")
	}
	if c, _, _, _ := fileRow(t, s, ids[1]); c != "zstd" {
		t.Errorf("stale row: %s", c)
	}
}
