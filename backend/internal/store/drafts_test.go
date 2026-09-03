package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func importTestAttachment(t *testing.T, s *Store, account, name, content string) Attachment {
	t.Helper()
	a := Attachment{AccountID: account, Filename: name, ContentType: "text/plain"}
	if err := s.ImportAttachment(context.Background(), &a, strings.NewReader(content), 1<<20); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDraftVersions(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	d := Draft{AccountID: "acc", Subject: "hi", To: []api.Address{{Address: "a@b"}}, TextBody: "x"}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || d.Version != 1 || d.UpdatedAt.IsZero() {
		t.Fatalf("create: %+v", d)
	}

	d.Subject = "hi again"
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if d.Version != 2 {
		t.Fatalf("update version = %d", d.Version)
	}

	stale := d
	stale.Version = 1
	if err := s.SaveDraft(ctx, &stale, nil); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale save: %v", err)
	}
	unknown := Draft{ID: "d_nope", AccountID: "acc", Version: 2}
	if err := s.SaveDraft(ctx, &unknown, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	wrongAccount := d
	wrongAccount.AccountID = "other"
	if err := s.SaveDraft(ctx, &wrongAccount, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account: %v", err)
	}

	got, err := s.GetDraft(ctx, "acc", d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "hi again" || got.Version != 2 || len(got.To) != 1 || got.To[0].Address != "a@b" {
		t.Fatalf("get = %+v", got)
	}
	if _, err := s.GetDraft(ctx, "acc", "d_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: %v", err)
	}
}

func TestDraftListAndCursor(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for i := 0; i < 5; i++ {
		d := Draft{AccountID: "acc", Subject: string(rune('a' + i))}
		if err := s.SaveDraft(ctx, &d, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct updated_at
	}
	other := Draft{AccountID: "other"}
	if err := s.SaveDraft(ctx, &other, nil); err != nil {
		t.Fatal(err)
	}

	page1, next, total, err := s.ListDrafts(ctx, "acc", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page1) != 2 || next == "" {
		t.Fatalf("page1: total=%d len=%d next=%q", total, len(page1), next)
	}
	if page1[0].Subject != "e" || page1[1].Subject != "d" {
		t.Fatalf("ordering: %s %s", page1[0].Subject, page1[1].Subject)
	}
	page2, next2, _, err := s.ListDrafts(ctx, "acc", next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].Subject != "c" || next2 == "" {
		t.Fatalf("page2: %+v next=%q", page2, next2)
	}
	page3, next3, _, err := s.ListDrafts(ctx, "acc", next2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 || page3[0].Subject != "a" || next3 != "" {
		t.Fatalf("page3: %+v next=%q", page3, next3)
	}
	if _, _, _, err := s.ListDrafts(ctx, "acc", "!!not base64!!", 2); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestDraftAttachmentBinding(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a1 := importTestAttachment(t, s, "acc", "one.txt", "one")
	a2 := importTestAttachment(t, s, "acc", "two.txt", "two")
	foreign := importTestAttachment(t, s, "other", "x.txt", "x")

	d := Draft{AccountID: "acc"}
	if err := s.SaveDraft(ctx, &d, []string{a2.ID, a1.ID}); err != nil {
		t.Fatal(err)
	}
	if len(d.Attachments) != 2 || d.Attachments[0].ID != a2.ID || d.Attachments[1].ID != a1.ID {
		t.Fatalf("bound order: %+v", d.Attachments)
	}

	// Another draft cannot take a bound attachment.
	d2 := Draft{AccountID: "acc"}
	if err := s.SaveDraft(ctx, &d2, []string{a1.ID}); !errors.Is(err, ErrAttachmentBound) {
		t.Fatalf("steal bound: %v", err)
	}
	// Foreign account / unknown id.
	d3 := Draft{AccountID: "acc"}
	if err := s.SaveDraft(ctx, &d3, []string{foreign.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign: %v", err)
	}
	if err := s.SaveDraft(ctx, &d3, []string{"att_nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	// Re-saving with only a1 releases a2 (kept, unbound) and keeps a1.
	if err := s.SaveDraft(ctx, &d, []string{a1.ID}); err != nil {
		t.Fatal(err)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].ID != a1.ID {
		t.Fatalf("after release: %+v", d.Attachments)
	}
	got, err := s.GetAttachments(ctx, "acc", []string{a2.ID})
	if err != nil || got[0].DraftID != "" {
		t.Fatalf("a2 should be unbound: %+v %v", got, err)
	}
	// Now d2 can take a2.
	if err := s.SaveDraft(ctx, &d2, []string{a2.ID}); err != nil {
		t.Fatalf("bind released: %v", err)
	}

	// Deleting d removes a1's row and file, leaves a2.
	if err := s.DeleteDraft(ctx, "acc", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.AttachmentPath(a1.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a1 file survived delete: %v", err)
	}
	if _, err := s.GetAttachments(ctx, "acc", []string{a1.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a1 row survived delete: %v", err)
	}
	if _, err := os.Stat(s.AttachmentPath(a2.ID)); err != nil {
		t.Errorf("a2 file missing: %v", err)
	}
	if err := s.DeleteDraft(ctx, "acc", d.ID); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestImportAttachment(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	a := importTestAttachment(t, s, "acc", "hello.txt", "hello")
	info, err := os.Stat(s.AttachmentPath(a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Size() != 5 || a.Size != 5 {
		t.Errorf("file: mode=%v size=%d a.Size=%d", info.Mode().Perm(), info.Size(), a.Size)
	}
	if a.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("sha256 = %s", a.SHA256)
	}
	if !strings.HasPrefix(a.ID, "att_") || a.DraftID != "" || a.CreatedAt.IsZero() {
		t.Errorf("metadata: %+v", a)
	}

	f, meta, err := s.OpenAttachment(ctx, "acc", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(f)
	f.Close()
	if buf.String() != "hello" || meta.Filename != "hello.txt" {
		t.Errorf("round trip: %q %+v", buf.String(), meta)
	}

	big := Attachment{AccountID: "acc", Filename: "big", ContentType: "application/octet-stream"}
	err = s.ImportAttachment(ctx, &big, bytes.NewReader(make([]byte, 11)), 10)
	if !errors.Is(err, ErrTooBig) {
		t.Fatalf("over limit: %v", err)
	}
	entries, _ := os.ReadDir(s.AttachmentDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file left behind: %s", e.Name())
		}
	}
	empty := Attachment{AccountID: "acc", Filename: "empty", ContentType: "text/plain"}
	if err := s.ImportAttachment(ctx, &empty, strings.NewReader(""), 10); err == nil {
		t.Error("empty import accepted")
	}

	if err := s.RemoveAttachment(ctx, "acc", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.AttachmentPath(a.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Error("file survived remove")
	}
	if err := s.RemoveAttachment(ctx, "acc", a.ID); err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func TestSweepAttachments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	old := importTestAttachment(t, s, "acc", "old.txt", "old")
	fresh := importTestAttachment(t, s, "acc", "fresh.txt", "fresh")
	bound := importTestAttachment(t, s, "acc", "bound.txt", "bound")
	d := Draft{AccountID: "acc"}
	if err := s.SaveDraft(ctx, &d, []string{bound.ID}); err != nil {
		t.Fatal(err)
	}
	// Age the unbound one and the bound one; only the unbound must go.
	past := time.Now().UTC().Add(-25 * time.Hour)
	for _, id := range []string{old.ID, bound.ID} {
		if _, err := s.db.Exec(`UPDATE attachments SET created_at = ? WHERE id = ?`, past.Format(timeLayout), id); err != nil {
			t.Fatal(err)
		}
	}
	stray := filepath.Join(s.AttachmentDir(), "att_stray")
	os.WriteFile(stray, []byte("x"), 0o600)
	os.Chtimes(stray, past, past)
	staleTmp := filepath.Join(s.AttachmentDir(), "att_x.tmp")
	os.WriteFile(staleTmp, []byte("x"), 0o600)
	os.Chtimes(staleTmp, past, past)
	newTmp := filepath.Join(s.AttachmentDir(), "att_y.tmp")
	os.WriteFile(newTmp, []byte("x"), 0o600)

	n, err := s.SweepAttachments(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("removed %d, want 3 (old row, stray file, stale tmp)", n)
	}
	for _, p := range []string{s.AttachmentPath(old.ID), stray, staleTmp} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived sweep", p)
		}
	}
	for _, p := range []string{s.AttachmentPath(fresh.ID), s.AttachmentPath(bound.ID), newTmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed by sweep: %v", p, err)
		}
	}
	if _, err := s.GetAttachments(ctx, "acc", []string{old.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("old row survived: %v", err)
	}
}
