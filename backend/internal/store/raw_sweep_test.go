// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSweepMessageFiles(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.AddAccount(ctx, &Account{ID: "acc", Config: api.AccountConfig{Email: "acc@example.invalid"}}); err != nil {
		t.Fatal(err)
	}
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	dir := filepath.Join(s.MessageDir(), "acc")
	old := time.Now().Add(-2 * time.Hour)
	age := func(path string) {
		t.Helper()
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put := func(id string) {
		t.Helper()
		if _, err := s.PutMessageRaw(ctx, "acc", id, RawWrite{}, chunked([]byte("Subject: "+id+"\r\n\r\nbody"))); err != nil {
			t.Fatal(err)
		}
	}
	s.SetRawCodec(RawZstd)

	// A file from before the accounting (plain, as all were).
	legacy := seedRow(t, s, inbox, 1)
	write(s.MessageRawPath("acc", legacy.ID), "Subject: legacy\r\n\r\nbody")
	// An accounting row that is wrong.
	wrong := seedRow(t, s, inbox, 2)
	put(wrong.ID)
	if _, err := s.DB().Exec(`UPDATE message_files SET codec = 'plain', disk_bytes = 1 WHERE message_id = ?`, wrong.ID); err != nil {
		t.Fatal(err)
	}
	// Both variants, the .zst newer; and a newer .zst that is damaged.
	pair := seedRow(t, s, inbox, 3)
	put(pair.ID)
	write(s.MessageRawPath("acc", pair.ID), "Subject: older plain\r\n\r\nbody")
	age(s.MessageRawPath("acc", pair.ID))
	badPair := seedRow(t, s, inbox, 4)
	write(s.MessageRawPath("acc", badPair.ID), "Subject: plain survivor\r\n\r\nbody")
	age(s.MessageRawPath("acc", badPair.ID))
	write(s.MessageRawPath("acc", badPair.ID)+RawZstSuffix, "not a frame at all")
	// Files without a row: an old one goes, a young one stays (an outbox
	// file is written just before its row).
	write(filepath.Join(dir, "m_orphan_old"), "x")
	age(filepath.Join(dir, "m_orphan_old"))
	write(filepath.Join(dir, "m_orphan_young"+RawZstSuffix), "x")
	// Temporary files: a stale one goes, a fresh one may be a writer's.
	write(filepath.Join(dir, "m_stale.tmp"), "x")
	age(filepath.Join(dir, "m_stale.tmp"))
	write(filepath.Join(dir, "m_fresh.zst.tmp"), "x")
	// An accounting row whose file is gone.
	gone := seedRow(t, s, inbox, 5)
	put(gone.ID)
	os.Remove(s.MessageRawPath("acc", gone.ID) + RawZstSuffix)
	// A .zst whose header is damaged and that has no row yet: kept.
	damaged := seedRow(t, s, inbox, 6)
	write(s.MessageRawPath("acc", damaged.ID)+RawZstSuffix, "garbage")
	// The outbox is plain by design, never misplaced.
	out, _ := seedOutbox(t, s, "acc")
	// Directories of accounts this store does not know: an old empty one
	// goes; one with files stays, whatever its age (it may be another
	// store's, beside this one), and nothing in it is touched.
	write(filepath.Join(s.MessageDir(), "ghost", "m_1"), "x")
	age(filepath.Join(s.MessageDir(), "ghost", "m_1"))
	write(filepath.Join(s.MessageDir(), "ghost", "m_2.tmp"), "x")
	age(filepath.Join(s.MessageDir(), "ghost", "m_2.tmp"))
	age(filepath.Join(s.MessageDir(), "ghost"))
	write(filepath.Join(s.MessageDir(), "ghost_young", "m_1"), "x")
	if err := os.Mkdir(filepath.Join(s.MessageDir(), "ghost_empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	age(filepath.Join(s.MessageDir(), "ghost_empty"))
	// Accounting rows of an account without a directory.
	elsewhere := seedFolder(t, s, "acc3", "INBOX", api.RoleInbox)
	lost := seedRow(t, s, elsewhere, 1)
	if _, err := s.DB().Exec(`INSERT INTO message_files (message_id, codec, bytes, disk_bytes) VALUES (?, 'plain', 5, 5)`, lost.ID); err != nil {
		t.Fatal(err)
	}
	// A stale staged file.
	st, _ := s.StageRaw(ctx, 0)
	st.f.Close()
	age(st.path)
	fresh, _ := s.StageRaw(ctx, 0)
	defer fresh.Remove()

	if v, ok, _ := s.GetMeta(ctx, metaRawAccounted); ok {
		t.Fatalf("accounted before the sweep: %q", v)
	}
	res, err := s.SweepMessageFiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := SweepResult{Temps: 1, Staged: 1, Orphans: 1, Dirs: 1, Resolved: 2, Backfilled: 1, Fixed: 1,
		Dropped: 2, Misplaced: 2, Corrupt: 1}
	if res != want {
		t.Errorf("sweep = %+v\nwant    %+v", res, want)
	}
	if v, _, _ := s.GetMeta(ctx, metaRawAccounted); v != "done" {
		t.Errorf("raw.accounted = %q", v)
	}

	exists := func(path string) bool { return fileExists(t, path) }
	for path, want := range map[string]bool{
		filepath.Join(dir, "m_orphan_old"):                 false,
		filepath.Join(dir, "m_orphan_young"+RawZstSuffix):  true,
		filepath.Join(dir, "m_stale.tmp"):                  false,
		filepath.Join(dir, "m_fresh.zst.tmp"):              true,
		s.MessageRawPath("acc", pair.ID):                   false,
		s.MessageRawPath("acc", pair.ID) + RawZstSuffix:    true,
		s.MessageRawPath("acc", badPair.ID):                true,
		s.MessageRawPath("acc", badPair.ID) + RawZstSuffix: false,
		s.MessageRawPath("acc", damaged.ID) + RawZstSuffix: true,
		s.MessageRawPath("acc", out.ID):                    true,
		filepath.Join(s.MessageDir(), "ghost", "m_1"):      true,
		filepath.Join(s.MessageDir(), "ghost", "m_2.tmp"):  true,
		filepath.Join(s.MessageDir(), "ghost_young"):       true,
		filepath.Join(s.MessageDir(), "ghost_empty"):       false,
		st.path:    false,
		fresh.path: true,
	} {
		if exists(path) != want {
			t.Errorf("%s: exists %v, want %v", path, !want, want)
		}
	}
	// Every file now has a row that describes it.
	for _, id := range []string{legacy.ID, wrong.ID, pair.ID, badPair.ID, out.ID} {
		files, err := statRaw(dir, id)
		if err != nil || !files.any() || files.both() {
			t.Fatalf("%s: %+v %v", id, files, err)
		}
		info, err := files.info(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		if c, b, d, ok := fileRow(t, s, id); !ok || c != info.Codec.String() || b != info.Bytes || d != info.DiskBytes {
			t.Errorf("%s: accounting %s %d %d %v, file %+v", id, c, b, d, ok, info)
		}
	}
	for _, id := range []string{gone.ID, lost.ID, damaged.ID} {
		if _, _, _, ok := fileRow(t, s, id); ok {
			t.Errorf("%s: row kept", id)
		}
	}
	if got := readRaw(t, s, "acc", badPair.ID); string(got) != "Subject: plain survivor\r\n\r\nbody" {
		t.Errorf("damaged pair kept %q", got)
	}

	// A second sweep finds only what the conversion is for.
	res, err = s.SweepMessageFiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SweepResult{Misplaced: 2, Corrupt: 1}); res != want {
		t.Errorf("second sweep = %+v", res)
	}
	// With the codec that matches, nothing is misplaced.
	s.SetRawCodec(RawPlain)
	if res, _ := s.SweepMessageFiles(ctx, time.Hour); res.Misplaced != 3 {
		// pair, wrong and damaged are .zst now
		t.Errorf("plain codec: misplaced %d", res.Misplaced)
	}
}

// The sweep never waits for a writer, and leaves its files alone.
func TestSweepSkipsBusyMessages(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	dir := filepath.Join(s.MessageDir(), "acc")
	os.MkdirAll(dir, 0o700)
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{"m_1", "m_1.tmp"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
		os.Chtimes(filepath.Join(dir, name), old, old)
	}
	if err := s.AddAccount(ctx, &Account{ID: "acc", Config: api.AccountConfig{Email: "acc@example.invalid"}}); err != nil {
		t.Fatal(err)
	}
	h, err := s.lockRaw(ctx, "acc", "m_1")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.SweepMessageFiles(ctx, time.Hour)
	h.unlock()
	if err != nil || res.Busy != 2 || res.Orphans+res.Temps != 0 {
		t.Errorf("busy: %+v %v", res, err)
	}
	if res, err := s.SweepMessageFiles(ctx, time.Hour); err != nil || res.Orphans != 1 || res.Temps != 1 {
		t.Errorf("free: %+v %v", res, err)
	}
}

// Two stores in one data directory share messages/ (and staging/): each
// knows only its own accounts, and the sweep of either leaves the other's
// mail alone, the outbox included, however old it is.
func TestSweepLeavesAnotherStoresMail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func(name string) *Store {
		t.Helper()
		s, err := Open(ctx, filepath.Join(dir, name), slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	a, b := open("a.db"), open("b.db")
	if a.MessageDir() != b.MessageDir() {
		t.Fatalf("message directories %s and %s", a.MessageDir(), b.MessageDir())
	}
	var files []string
	for i, s := range []*Store{a, b} {
		acc := &Account{Config: api.AccountConfig{Email: []string{"a@example.invalid", "b@example.invalid"}[i]}}
		if err := s.AddAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
		inbox := seedFolder(t, s, acc.ID, "INBOX", api.RoleInbox)
		m := seedRow(t, s, inbox, 1)
		if _, err := s.PutMessageRaw(ctx, acc.ID, m.ID, RawWrite{}, chunked([]byte("Subject: kept\r\n\r\nbody"))); err != nil {
			t.Fatal(err)
		}
		out, _ := seedOutbox(t, s, acc.ID)
		files = append(files, s.MessageRawPath(acc.ID, m.ID), s.MessageRawPath(acc.ID, out.ID))
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(a.MessageDir(), func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	for i, s := range []*Store{a, b, a} {
		res, err := s.SweepMessageFiles(ctx, time.Hour)
		if err != nil || res.Dirs != 0 || res.Orphans != 0 || res.Temps != 0 {
			t.Errorf("sweep %d: %+v %v", i, res, err)
		}
	}
	for _, path := range files {
		if !fileExists(t, path) {
			t.Errorf("%s removed by the other store's sweep", path)
		}
	}
}
