// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/schotek/malachi/backend/pkg/api"
)

// rawContent is n bytes that look like mail: header and text lines that
// compress well, with a base64 stretch that hardly does. The same seed
// gives the same bytes.
func rawContent(n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	var b bytes.Buffer
	b.WriteString("Subject: test message\r\nFrom: Alice <alice@example.invalid>\r\n\r\n")
	words := []string{"hello", "world", "přílohy", "message", "the", "store", "zstd", "and", "a", "of"}
	for b.Len() < n {
		if rng.Intn(4) == 0 {
			raw := make([]byte, 57)
			rng.Read(raw)
			b.WriteString(base64Line(raw))
			continue
		}
		for i := 0; i < 12; i++ {
			b.WriteString(words[rng.Intn(len(words))])
			b.WriteByte(' ')
		}
		b.WriteString("\r\n")
	}
	return b.Bytes()[:n]
}

func base64Line(raw []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for _, c := range raw {
		sb.WriteByte(alphabet[c&63])
	}
	sb.WriteString("\r\n")
	return sb.String()
}

// chunked is a producer writing data in uneven pieces.
func chunked(data []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		for rest, step := data, 1; len(rest) > 0; step = step*3 + 7 {
			k := min(step, len(rest))
			if _, err := w.Write(rest[:k]); err != nil {
				return err
			}
			rest = rest[k:]
		}
		return nil
	}
}

// readRaw returns a message's content through OpenMessageRaw, checking
// Size and a second pass after Rewind on the way.
func readRaw(t testing.TB, s *Store, acc, id string) []byte {
	t.Helper()
	r, err := s.OpenMessageRaw(context.Background(), acc, id)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	if n, err := r.Size(); err != nil || n != int64(len(got)) {
		t.Fatalf("size of %s: %d %v, read %d", id, n, err, len(got))
	}
	if err := r.Rewind(); err != nil {
		t.Fatalf("rewind %s: %v", id, err)
	}
	again, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("second pass of %s: %d bytes, %v", id, len(again), err)
	}
	return got
}

// dirNames lists the file names in dir.
func dirNames(t testing.TB, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// seedRow stores a message row without a raw file.
func seedRow(t testing.TB, s *Store, f Folder, uid uint32) *Message {
	t.Helper()
	m := &Message{AccountID: f.AccountID, FolderID: f.ID, UID: uid, Subject: fmt.Sprintf("m%d", uid),
		Date: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Size: 100}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	return m
}

// fileRow is a message's accounting row; ok false when it has none.
func fileRow(t testing.TB, s *Store, id string) (codec string, bytes, disk int64, ok bool) {
	t.Helper()
	err := s.DB().QueryRow(`SELECT codec, bytes, disk_bytes FROM message_files WHERE message_id = ?`, id).Scan(&codec, &bytes, &disk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return codec, bytes, disk, true
}

func TestPutMessageRawRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	dir := filepath.Join(s.MessageDir(), "acc")
	filePerm, dirPerm := permOf(t, 0o600, false), permOf(t, 0o700, true)
	lengths := []int{0, 1, 255, 256, 1 << 10, 128<<10 - 1, 128 << 10, 128<<10 + 1, 1 << 20}
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		for _, known := range []bool{false, true} {
			for _, n := range lengths {
				id := fmt.Sprintf("m_%s_%v_%d", codec, known, n)
				data := rawContent(n, int64(n))
				s.SetRawCodec(codec)
				w := RawWrite{}
				if known {
					w.Size = int64(n)
				}
				info, err := s.PutMessageRaw(ctx, "acc", id, w, chunked(data))
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				name := rawName(id, codec)
				st, err := os.Stat(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				if info.Codec != codec || info.Bytes != int64(n) || info.DiskBytes != st.Size() || st.Mode().Perm() != filePerm {
					t.Errorf("%s: info %+v, file %d bytes mode %v", id, info, st.Size(), st.Mode().Perm())
				}
				if _, err := os.Stat(filepath.Join(dir, rawName(id, codec.other()))); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s: other variant exists: %v", id, err)
				}
				if codec == RawZstd {
					file, _ := os.ReadFile(filepath.Join(dir, name))
					var h zstd.Header
					if err := h.Decode(file); err != nil || !h.HasFCS || h.FrameContentSize != uint64(n) {
						t.Errorf("%s: frame header %+v %v", id, h, err)
					}
					if n > 0 && !h.HasCheckSum {
						t.Errorf("%s: no checksum", id)
					}
				}
				// Reading does not depend on the codec now in force.
				s.SetRawCodec(codec.other())
				if got := readRaw(t, s, "acc", id); !bytes.Equal(got, data) {
					t.Errorf("%s: read %d bytes back, want %d", id, len(got), n)
				}
			}
		}
	}
	for _, name := range dirNames(t, dir) {
		if strings.HasSuffix(name, tmpSuffix) {
			t.Errorf("left behind: %s", name)
		}
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != dirPerm {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	// No row, no accounting.
	if _, _, _, ok := fileRow(t, s, "m_plain_false_1"); ok {
		t.Error("accounting row for a message without a row")
	}
}

func TestPutMessageRawLimitCountsContent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	s.SetRawCodec(RawZstd)
	zeros := make([]byte, 1001)
	for _, known := range []bool{false, true} {
		w := RawWrite{Limit: 1000}
		if known {
			w.Size = 1000
		}
		if _, err := s.PutMessageRaw(ctx, "acc", "m_1", w, chunked(zeros[:1000])); err != nil {
			t.Fatalf("exactly the limit (known %v): %v", known, err)
		}
		w.Size = 0
		// A thousand zeros compress to a few bytes; the limit is the
		// message's length all the same.
		if _, err := s.PutMessageRaw(ctx, "acc", "m_2", w, chunked(zeros)); !errors.Is(err, ErrTooBig) {
			t.Fatalf("over the limit: %v", err)
		}
	}
	called := false
	if _, err := s.PutMessageRaw(ctx, "acc", "m_3", RawWrite{Limit: 10, Size: 11}, func(io.Writer) error {
		called = true
		return nil
	}); !errors.Is(err, ErrTooBig) || called {
		t.Errorf("declared size over the limit: %v, producer called %v", err, called)
	}
	if _, err := s.PutMessageRaw(ctx, "acc", "m_3", RawWrite{Size: -1}, chunked(nil)); err == nil {
		t.Error("negative size accepted")
	}
	names := dirNames(t, filepath.Join(s.MessageDir(), "acc"))
	if !slices.Equal(names, []string{"m_1.zst"}) {
		t.Errorf("files = %v", names)
	}
	// A limit above the cap is the cap.
	if _, err := s.PutMessageRaw(ctx, "acc", "m_4", RawWrite{Limit: MaxRawBytes * 2, Size: MaxRawBytes + 1}, chunked(nil)); !errors.Is(err, ErrTooBig) {
		t.Errorf("over MaxRawBytes: %v", err)
	}
}

func TestPutMessageRawFailureKeepsEarlierFile(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		s.SetRawCodec(codec)
		id := "m_" + codec.String()
		old := rawContent(300<<10, 1)
		if _, err := s.PutMessageRaw(ctx, "acc", id, RawWrite{}, chunked(old)); err != nil {
			t.Fatal(err)
		}
		oldInfo, _ := os.Stat(filepath.Join(s.MessageDir(), "acc", rawName(id, codec)))
		failures := map[string]struct {
			w  RawWrite
			fn func(io.Writer) error
		}{
			"short":    {RawWrite{Size: 10}, chunked(make([]byte, 9))},
			"long":     {RawWrite{Size: 10}, chunked(make([]byte, 11))},
			"long big": {RawWrite{Size: 200 << 10}, chunked(rawContent(250<<10, 2))},
			"producer": {RawWrite{}, func(w io.Writer) error {
				w.Write([]byte("partial"))
				return errors.New("network down")
			}},
			"swallowed limit": {RawWrite{Limit: 5}, func(w io.Writer) error {
				w.Write([]byte("0123456789"))
				return nil
			}},
			"cancelled": {RawWrite{}, func(w io.Writer) error { return context.Canceled }},
		}
		for name, c := range failures {
			if _, err := s.PutMessageRaw(ctx, "acc", id, c.w, c.fn); err == nil {
				t.Errorf("%s/%s: no error", codec, name)
			}
			info, err := os.Stat(filepath.Join(s.MessageDir(), "acc", rawName(id, codec)))
			if err != nil || !os.SameFile(info, oldInfo) {
				t.Errorf("%s/%s: earlier file replaced: %v", codec, name, err)
			}
			if got := readRaw(t, s, "acc", id); !bytes.Equal(got, old) {
				t.Errorf("%s/%s: content changed", codec, name)
			}
		}
		if _, err := s.PutMessageRaw(ctx, "acc", id, RawWrite{Size: 10}, chunked(make([]byte, 9))); !errors.Is(err, errSizeMismatch) {
			t.Errorf("%s: short write: %v", codec, err)
		}
	}
	for _, name := range dirNames(t, filepath.Join(s.MessageDir(), "acc")) {
		if strings.HasSuffix(name, tmpSuffix) {
			t.Errorf("left behind: %s", name)
		}
	}
	// A cancelled context stops the write before the rename.
	cctx, cancel := context.WithCancel(ctx)
	if _, err := s.PutMessageRaw(cctx, "acc", "m_c", RawWrite{}, func(w io.Writer) error {
		cancel()
		_, err := w.Write([]byte("x"))
		return err
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if fileExists(t, s.MessageRawPath("acc", "m_c")) {
		t.Error("cancelled write kept")
	}
}

func TestPutMessageRawRequireRow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedRow(t, s, inbox, 1)
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		s.SetRawCodec(codec)
		info, err := s.PutMessageRaw(ctx, "acc", m.ID, RawWrite{RequireRow: true}, chunked([]byte("Subject: x\r\n\r\nbody")))
		if err != nil {
			t.Fatal(err)
		}
		if c, b, d, ok := fileRow(t, s, m.ID); !ok || c != codec.String() || b != info.Bytes || d != info.DiskBytes {
			t.Errorf("%s: accounting %s %d %d %v, info %+v", codec, c, b, d, ok, info)
		}
	}
	// Without a row nothing is kept, not even an earlier file.
	if err := os.WriteFile(s.MessageRawPath("acc", "m_gone"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutMessageRaw(ctx, "acc", "m_gone", RawWrite{RequireRow: true}, chunked([]byte("new"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
	for _, name := range dirNames(t, filepath.Join(s.MessageDir(), "acc")) {
		if strings.HasPrefix(name, "m_gone") {
			t.Errorf("left behind: %s", name)
		}
	}
	// Another account's row does not count.
	if _, err := s.PutMessageRaw(ctx, "other", m.ID, RawWrite{RequireRow: true}, chunked([]byte("x"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign account: %v", err)
	}
}

func TestRawNameDecidesNotContent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	// A message may begin with the zstd magic number, even with a whole
	// frame after it.
	frame := make([]byte, 0, 64)
	frame = append(frame, zstdEmptyFrame...)
	evil := append(slices.Clone(frame), []byte("\r\nSubject: evil\r\n\r\nbody")...)
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		s.SetRawCodec(codec)
		id := "m_magic_" + codec.String()
		if _, err := s.WriteMessageRaw(ctx, "acc", id, bytes.NewReader(evil), 1<<20); err != nil {
			t.Fatal(err)
		}
		if got := readRaw(t, s, "acc", id); !bytes.Equal(got, evil) {
			t.Errorf("%s: read %q", codec, got)
		}
	}
	// A .zst that holds plain text is damaged, never read as the message.
	plain := []byte("Subject: plain\r\n\r\nbody")
	if err := os.WriteFile(s.MessageRawPath("acc", "m_plain")+RawZstSuffix, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenMessageRaw(ctx, "acc", "m_plain"); !errors.Is(err, ErrRawCorrupt) {
		t.Errorf("plain text in a .zst: %v", err)
	}
	// And an empty frame under the plain name is a message of nine bytes.
	if err := os.WriteFile(s.MessageRawPath("acc", "m_frame"), zstdEmptyFrame, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, s, "acc", "m_frame"); !bytes.Equal(got, zstdEmptyFrame) {
		t.Errorf("frame under the plain name: %q", got)
	}
}

func TestOpenMessageRawOrder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.OpenMessageRaw(ctx, "acc", "m_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("none: %v", err)
	}
	s.SetRawCodec(RawZstd)
	if _, err := s.WriteMessageRaw(ctx, "acc", "m_1", strings.NewReader("compressed"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.MessageRawPath("acc", "m_1"), []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Both exist (a conversion between its phases): the store codec's
	// name first.
	if got := readRaw(t, s, "acc", "m_1"); string(got) != "compressed" {
		t.Errorf("zstd first: %q", got)
	}
	s.SetRawCodec(RawPlain)
	if got := readRaw(t, s, "acc", "m_1"); string(got) != "plain" {
		t.Errorf("plain first: %q", got)
	}
	os.Remove(s.MessageRawPath("acc", "m_1"))
	if got := readRaw(t, s, "acc", "m_1"); string(got) != "compressed" {
		t.Errorf("the other when the first is missing: %q", got)
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", "m_1.zst", "m_1.tmp", "x\x00"} {
		if _, err := s.OpenMessageRaw(ctx, "acc", bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("id %q: %v", bad, err)
		}
		if _, err := s.WriteMessageRaw(ctx, "acc", bad, strings.NewReader("x"), 10); err == nil {
			t.Errorf("write id %q accepted", bad)
		}
	}
	// Closing twice is harmless, reading after close is not.
	r, err := s.OpenMessageRaw(ctx, "acc", "m_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	if _, err := r.Read(make([]byte, 4)); err == nil {
		t.Error("read after close")
	}
}

func TestRemovalDeletesBothNames(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	s.SetRawCodec(RawZstd)
	a := seedMessage(t, s, inbox, 1, "a", time.Now())
	b := seedMessage(t, s, inbox, 2, "b", time.Now())
	// b is between two phases of a conversion: both variants.
	if err := os.WriteFile(s.MessageRawPath("acc", b.ID), []byte("plain copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExists(t, s.MessageRawPath("acc", a.ID)+RawZstSuffix) {
		t.Fatal("seeded message not compressed")
	}
	if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{1, 2}); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, filepath.Join(s.MessageDir(), "acc")); len(names) != 0 {
		t.Errorf("left behind: %v", names)
	}
	if _, _, _, ok := fileRow(t, s, a.ID); ok {
		t.Error("accounting row outlived its message")
	}

	// A message whose writer holds it when its row goes: the writer removes
	// the files before it lets go.
	c := seedMessage(t, s, inbox, 3, "c", time.Now())
	err := s.WithMessageRaw(ctx, "acc", c.ID, func(tx *RawTx) error {
		if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{3}); err != nil {
			return err
		}
		// Still here while the writer holds it.
		if _, ok, err := tx.Stat(); err != nil || !ok {
			return fmt.Errorf("file gone early: %v %v", ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, filepath.Join(s.MessageDir(), "acc")); len(names) != 0 {
		t.Errorf("doomed message left behind: %v", names)
	}
	s.rawMu.Lock()
	left := len(s.rawLocks)
	s.rawMu.Unlock()
	if left != 0 {
		t.Errorf("%d locks left", left)
	}
}

func TestReplaceRules(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "m", time.Now())
	produce := RawProducer(chunked([]byte("Subject: replaced\r\n\r\nbody")))

	if err := s.WithMessageRaw(ctx, "acc", "m_nope", func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{}, produce)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
	out, _ := seedOutbox(t, s, "acc")
	if err := s.WithMessageRaw(ctx, "acc", out.ID, func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{}, produce)
		return err
	}); !errors.Is(err, ErrOutbox) {
		t.Errorf("outbox: %v", err)
	}
	if err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{Plain: true}, produce)
		return err
	}); err == nil {
		t.Error("plain replacement accepted")
	}

	// A plain staged file is renamed into place.
	s.SetRawCodec(RawPlain)
	st, err := s.StageRaw(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("Subject: staged\r\n\r\nbody")); err != nil {
		t.Fatal(err)
	}
	staged, _ := os.Stat(st.path)
	// Windows may read a file's identity lazily, by path, at the first
	// SameFile: read it while the path still names the staged file.
	os.SameFile(staged, staged)
	var info RawInfo
	if err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		info, err = tx.Replace(RawWrite{}, st)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	placed, err := os.Stat(s.MessageRawPath("acc", m.ID))
	if err != nil || !os.SameFile(staged, placed) {
		t.Errorf("staged file not renamed into place: %v", err)
	}
	if err := st.Remove(); err != nil || !fileExists(t, s.MessageRawPath("acc", m.ID)) {
		t.Errorf("removing a committed staged file: %v", err)
	}
	if got := readRaw(t, s, "acc", m.ID); string(got) != "Subject: staged\r\n\r\nbody" || info.Bytes != int64(len(got)) {
		t.Errorf("replaced content %q, info %+v", got, info)
	}

	// In zstd the other variant goes and the accounting follows.
	s.SetRawCodec(RawZstd)
	if err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		before, ok, err := tx.Stat()
		if err != nil || !ok || before.Codec != RawPlain {
			return fmt.Errorf("stat before: %+v %v %v", before, ok, err)
		}
		info, err = tx.Replace(RawWrite{}, produce)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fileExists(t, s.MessageRawPath("acc", m.ID)) || !fileExists(t, s.MessageRawPath("acc", m.ID)+RawZstSuffix) {
		t.Error("variants after a zstd replacement")
	}
	if c, b, d, ok := fileRow(t, s, m.ID); !ok || c != "zstd" || b != info.Bytes || d != info.DiskBytes {
		t.Errorf("accounting %s %d %d %v, info %+v", c, b, d, ok, info)
	}
	if got := readRaw(t, s, "acc", m.ID); string(got) != "Subject: replaced\r\n\r\nbody" {
		t.Errorf("content %q", got)
	}
	// A staged file of a declared size that differs is refused.
	st2, _ := s.StageRaw(ctx, 0)
	st2.Write([]byte("abc"))
	defer st2.Remove()
	if err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{Size: 4}, st2)
		return err
	}); !errors.Is(err, errSizeMismatch) {
		t.Errorf("staged size mismatch: %v", err)
	}

	// The transaction ends with its function.
	var kept *RawTx
	s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
		kept = tx
		return nil
	})
	if _, err := kept.Open(); !errors.Is(err, errRawTxDone) {
		t.Errorf("open after the end: %v", err)
	}
	if _, err := kept.Replace(RawWrite{}, produce); !errors.Is(err, errRawTxDone) {
		t.Errorf("replace after the end: %v", err)
	}

	// A message's first file may create the account directory, but not
	// bring back the directory of an account deleted meanwhile.
	other := seedFolder(t, s, "acc2", "INBOX", api.RoleInbox)
	fresh := seedRow(t, s, other, 7)
	if err := s.WithMessageRaw(ctx, "acc2", fresh.ID, func(tx *RawTx) error {
		_, err := tx.Replace(RawWrite{}, produce)
		return err
	}); err != nil || !fileExists(t, s.MessageRawPath("acc2", fresh.ID)+RawZstSuffix) {
		t.Errorf("first file: %v", err)
	}
}

func TestReplaceFromDeletedAccount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := Account{Email: "a@example.invalid", Config: api.AccountConfig{Email: "a@example.invalid"}}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	f := seedFolder(t, s, acc.ID, "INBOX", api.RoleInbox)
	m := seedRow(t, s, f, 1)
	err := s.WithMessageRaw(ctx, acc.ID, m.ID, func(tx *RawTx) error {
		// The account goes while the download is under way: after the row
		// was checked, before the directory is made.
		s.createFile = func(path string) (rawFile, error) {
			s.createFile = nil
			if err := s.DeleteAccount(ctx, acc.ID, true); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, err
			}
			return s.createRawFile(path)
		}
		_, err := tx.Replace(RawWrite{}, RawProducer(chunked([]byte("Subject: x\r\n\r\nbody"))))
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("replace for a deleted account: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.MessageDir(), acc.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("account directory came back: %v", err)
	}
}

func TestCodecChangeDuringWrite(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	data := rawContent(400<<10, 3)
	for _, known := range []bool{false, true} {
		for _, from := range []RawCodec{RawPlain, RawZstd} {
			id := fmt.Sprintf("m_%v_%s", known, from)
			s.SetRawCodec(from)
			w := RawWrite{}
			if known {
				w.Size = int64(len(data))
			}
			info, err := s.PutMessageRaw(ctx, "acc", id, w, func(w io.Writer) error {
				if _, err := w.Write(data[:1000]); err != nil {
					return err
				}
				s.SetRawCodec(from.other())
				_, err := w.Write(data[1000:])
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			to := from.other()
			if info.Codec != to || !fileExists(t, filepath.Join(s.MessageDir(), "acc", rawName(id, to))) ||
				fileExists(t, filepath.Join(s.MessageDir(), "acc", rawName(id, from))) {
				t.Errorf("%s: stored %s, want %s", id, info.Codec, to)
			}
			if got := readRaw(t, s, "acc", id); !bytes.Equal(got, data) {
				t.Errorf("%s: content differs", id)
			}
		}
	}
	// The outbox's plain files never follow.
	s.SetRawCodec(RawPlain)
	info, err := s.PutMessageRaw(ctx, "acc", "m_out", RawWrite{Plain: true, Sync: true}, func(w io.Writer) error {
		s.SetRawCodec(RawZstd)
		_, err := w.Write(data)
		return err
	})
	if err != nil || info.Codec != RawPlain || !fileExists(t, s.MessageRawPath("acc", "m_out")) {
		t.Errorf("plain write: %+v %v", info, err)
	}
}

// A replacement is flushed before it counts. When the codec turns plain
// while a compressed write of unknown size is being spooled, the spool
// becomes the new file and is flushed by name (syncFile), through a handle
// that may write: Windows flushes through no other, and the replacement
// failed there with "access denied".
func TestReplacementSpoolFlushed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	s.SetRawCodec(RawZstd)
	if _, err := s.PutMessageRaw(ctx, "acc", "m_spool", RawWrite{}, chunked([]byte("Subject: first\r\n\r\nbody"))); err != nil {
		t.Fatal(err)
	}
	info, err := s.PutMessageRaw(ctx, "acc", "m_spool", RawWrite{}, func(w io.Writer) error {
		s.SetRawCodec(RawPlain)
		_, err := w.Write([]byte("Subject: second\r\n\r\nbody"))
		return err
	})
	if err != nil || info.Codec != RawPlain {
		t.Fatalf("replacement: %+v %v", info, err)
	}
	if got := readRaw(t, s, "acc", "m_spool"); string(got) != "Subject: second\r\n\r\nbody" {
		t.Errorf("replaced content %q", got)
	}
	if err := syncFile(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("flush of a missing file: %v", err)
	}
}

func TestRawTxStatSettlesLeftover(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	old := time.Now().Add(-time.Hour)
	for _, c := range []struct {
		name      string
		newer     RawCodec
		damageZst bool
		want      string
	}{
		{"zstd newer", RawZstd, false, "zstd content"},
		{"plain newer", RawPlain, false, "plain content"},
		{"damaged zstd newer", RawZstd, true, "plain content"},
	} {
		m := seedRow(t, s, inbox, uint32(len(c.name)))
		s.SetRawCodec(RawZstd)
		if _, err := s.WriteMessageRaw(ctx, "acc", m.ID, strings.NewReader("zstd content"), 0); err != nil {
			t.Fatal(err)
		}
		zst := s.MessageRawPath("acc", m.ID) + RawZstSuffix
		plain := s.MessageRawPath("acc", m.ID)
		if c.damageZst {
			b, _ := os.ReadFile(zst)
			b[len(b)-1] ^= 0xff
			os.WriteFile(zst, b, 0o600)
		}
		os.WriteFile(plain, []byte("plain content"), 0o600)
		older, newer := plain, zst
		if c.newer == RawPlain {
			older, newer = zst, plain
		}
		os.Chtimes(older, old, old)
		os.Chtimes(newer, time.Now(), time.Now())
		var info RawInfo
		if err := s.WithMessageRaw(ctx, "acc", m.ID, func(tx *RawTx) error {
			var ok bool
			var err error
			info, ok, err = tx.Stat()
			if err == nil && !ok {
				err = errors.New("no file")
			}
			return err
		}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := readRaw(t, s, "acc", m.ID); string(got) != c.want || info.Bytes != int64(len(c.want)) {
			t.Errorf("%s: kept %q, info %+v", c.name, got, info)
		}
		if fileExists(t, plain) && fileExists(t, zst) {
			t.Errorf("%s: both variants left", c.name)
		}
		if codec, _, _, ok := fileRow(t, s, m.ID); !ok || codec != info.Codec.String() {
			t.Errorf("%s: accounting %s %v", c.name, codec, ok)
		}
	}
}

// stressVersion is a self-checking message: its first line carries the
// hash of the rest, so a reader can tell a whole version from a torn one.
func stressVersion(k int) []byte {
	body := rawContent(1<<10+k*37<<10, int64(k))
	sum := sha256.Sum256(body)
	return append([]byte("X-Sum: "+hex.EncodeToString(sum[:])+"\r\n"), body...)
}

func stressValid(data []byte) bool {
	line, body, ok := bytes.Cut(data, []byte("\r\n"))
	if !ok || !bytes.HasPrefix(line, []byte("X-Sum: ")) {
		return false
	}
	sum := sha256.Sum256(body)
	return string(line[len("X-Sum: "):]) == hex.EncodeToString(sum[:])
}

// Writers, replacers, the conversion, the sweep, readers and deletions on
// one message at once: no reader ever sees a torn or damaged message, and
// each round ends in order.
func TestRawStressOneMessage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	// A real account: the sweep removes the directory of an unknown one.
	if err := s.AddAccount(ctx, &Account{ID: "acc", Config: api.AccountConfig{Email: "acc@example.invalid"}}); err != nil {
		t.Fatal(err)
	}
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	m := seedRow(t, s, inbox, 1)
	var versions [][]byte
	for k := 0; k < 8; k++ {
		versions = append(versions, stressVersion(k))
	}
	rounds := 6
	if testing.Short() {
		rounds = 2
	}
	var total struct{ writes, replaces, converted, reads int64 }
	for round := 0; round < rounds; round++ {
		// A round lasts until it has done some of everything (a race
		// build is many times slower), within a cap.
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		quiet := round%4 >= 2
		// Writers leave the lock free now and then, the passes only try
		// it; in quiet rounds long enough for the conversion to get
		// through between writes.
		pause := func(rng *rand.Rand) {
			ms := 5 + rng.Intn(20)
			if quiet {
				ms = 30 + rng.Intn(50)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var failures []string
		var writes, replaces, converted, busy, reads atomic.Int64
		fail := func(format string, args ...any) {
			mu.Lock()
			failures = append(failures, fmt.Sprintf(format, args...))
			mu.Unlock()
		}
		run := func(seed int64, fn func(rng *rand.Rand)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(seed))
				for rctx.Err() == nil {
					fn(rng)
				}
			}()
		}
		for i := 0; i < 2; i++ {
			run(int64(round*10+i), func(rng *rand.Rand) {
				v := versions[rng.Intn(len(versions))]
				w := RawWrite{}
				if rng.Intn(2) == 0 {
					w.Size = int64(len(v))
				}
				switch _, err := s.PutMessageRaw(rctx, "acc", m.ID, w, chunked(v)); {
				case err == nil:
					writes.Add(1)
				case rctx.Err() == nil:
					fail("write: %v", err)
				}
				pause(rng)
			})
		}
		run(int64(round*10+3), func(rng *rand.Rand) {
			v := versions[rng.Intn(len(versions))]
			err := s.WithMessageRaw(rctx, "acc", m.ID, func(tx *RawTx) error {
				if r, err := tx.Open(); err == nil {
					data, err := io.ReadAll(r)
					r.Close()
					if err != nil || !stressValid(data) {
						fail("read under the lock: %d bytes, %v", len(data), err)
					}
				}
				_, err := tx.Replace(RawWrite{}, RawProducer(chunked(v)))
				return err
			})
			switch {
			case err == nil:
				replaces.Add(1)
			case !errors.Is(err, ErrNotFound) && rctx.Err() == nil:
				fail("replace: %v", err)
			}
			pause(rng)
		})
		run(int64(round*10+4), func(rng *rand.Rand) {
			if rng.Intn(3) == 0 {
				s.SetRawCodec(RawCodec(rng.Intn(2)))
			}
			_, res, err := s.ConvertRawBatch(rctx, "", s.RawCodec(), 8)
			if err != nil && !errors.Is(err, ErrConflict) && rctx.Err() == nil {
				fail("convert: %v", err)
			}
			converted.Add(int64(res.Converted))
			busy.Add(int64(res.Busy))
		})
		run(int64(round*10+5), func(rng *rand.Rand) {
			if _, err := s.SweepMessageFiles(rctx, 0); err != nil && rctx.Err() == nil {
				fail("sweep: %v", err)
			}
			time.Sleep(time.Millisecond)
		})
		for i := 0; i < 3; i++ {
			run(int64(round*10+6+i), func(rng *rand.Rand) {
				r, err := s.OpenMessageRaw(rctx, "acc", m.ID)
				if errors.Is(err, ErrNotFound) {
					return
				}
				if err != nil {
					fail("open: %v", err)
					return
				}
				data, err := io.ReadAll(r)
				n, serr := r.Size()
				r.Close()
				if err != nil || serr != nil || !stressValid(data) || n != int64(len(data)) {
					fail("read: %d bytes (size %d), %v %v", len(data), n, err, serr)
					return
				}
				reads.Add(1)
			})
		}
		// In odd rounds the row goes in the middle, once the others have
		// had their turn with it.
		deleted := round%2 == 1
		var deletedDone atomic.Bool
		if deleted {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for rctx.Err() == nil && (writes.Load() < 2 || replaces.Load() < 1 || (quiet && converted.Load() < 1)) {
					time.Sleep(5 * time.Millisecond)
				}
				if err := s.DeleteMessagesByUID(ctx, inbox.ID, []uint32{1}); err != nil {
					fail("delete: %v", err)
				}
				deletedDone.Store(true)
			}()
		}
		start := time.Now()
		for rctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
			enough := time.Since(start) > 250*time.Millisecond && writes.Load() >= 4 &&
				replaces.Load() >= 1 && reads.Load() >= 10 && (!quiet || converted.Load() >= 1) &&
				(!deleted || deletedDone.Load())
			if enough {
				break
			}
		}
		cancel()
		wg.Wait()
		for _, f := range failures {
			t.Errorf("round %d: %s", round, f)
		}
		t.Logf("round %d: %d writes, %d replacements, %d converted (%d busy), %d reads", round,
			writes.Load(), replaces.Load(), converted.Load(), busy.Load(), reads.Load())
		total.writes += writes.Load()
		total.replaces += replaces.Load()
		total.converted += converted.Load()
		total.reads += reads.Load()
		if t.Failed() {
			return
		}
		// Settle what busy passes skipped, then check the state.
		if _, err := s.SweepMessageFiles(ctx, 0); err != nil {
			t.Fatal(err)
		}
		files, err := statRaw(filepath.Join(s.MessageDir(), "acc"), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if deleted {
			if files.any() {
				// Writers without RequireRow may leave a file of a deleted
				// row; the sweep takes it once it is old.
				s.removeMessageFiles([]messageFile{{"acc", m.ID}})
			}
			m.UID, m.ThreadID = 1, ""
			if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if files.both() {
			t.Fatalf("round %d: both variants after the sweep", round)
		}
		if files.any() {
			data := readRaw(t, s, "acc", m.ID)
			if !stressValid(data) {
				t.Fatalf("round %d: stored message torn", round)
			}
			info, _ := files.info(filepath.Join(s.MessageDir(), "acc"), m.ID)
			if c, b, d, ok := fileRow(t, s, m.ID); !ok || c != info.Codec.String() || b != info.Bytes || d != info.DiskBytes {
				t.Errorf("round %d: accounting %s %d %d %v, file %+v", round, c, b, d, ok, info)
			}
		}
		for _, name := range dirNames(t, filepath.Join(s.MessageDir(), "acc")) {
			if strings.HasSuffix(name, tmpSuffix) {
				t.Errorf("round %d: left behind: %s", round, name)
			}
		}
		s.rawMu.Lock()
		left := len(s.rawLocks)
		s.rawMu.Unlock()
		if left != 0 {
			t.Errorf("round %d: %d locks left", round, left)
		}
	}
	if total.writes == 0 || total.replaces == 0 || total.reads == 0 || (total.converted == 0 && rounds > 2) {
		t.Errorf("the store was not exercised: %+v", total)
	}
}
