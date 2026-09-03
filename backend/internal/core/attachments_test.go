// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAttachmentImportRejects(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	a := b.Attachments()
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, nil, 0o600)
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(dir, "dirlink")
	os.Symlink(dir, dirLink)
	pdf := writeTestFile(t, "doc.pdf", []byte("%PDF-1.4 fake"))

	cases := map[string]struct {
		p    api.AttachmentImportParams
		code api.ErrorCode
	}{
		"no account":       {api.AttachmentImportParams{Path: pdf}, api.CodeInvalidArgument},
		"relative path":    {api.AttachmentImportParams{AccountID: "acc", Path: "doc.pdf"}, api.CodeInvalidArgument},
		"missing":          {api.AttachmentImportParams{AccountID: "acc", Path: filepath.Join(dir, "nope")}, api.CodeInvalidArgument},
		"directory":        {api.AttachmentImportParams{AccountID: "acc", Path: dir}, api.CodeInvalidArgument},
		"symlink to dir":   {api.AttachmentImportParams{AccountID: "acc", Path: dirLink}, api.CodeInvalidArgument},
		"fifo":             {api.AttachmentImportParams{AccountID: "acc", Path: fifo}, api.CodeInvalidArgument},
		"empty file":       {api.AttachmentImportParams{AccountID: "acc", Path: empty}, api.CodeInvalidArgument},
		"neither":          {api.AttachmentImportParams{AccountID: "acc"}, api.CodeInvalidArgument},
		"both":             {api.AttachmentImportParams{AccountID: "acc", Path: pdf, Data: []byte("x")}, api.CodeInvalidArgument},
		"data no name":     {api.AttachmentImportParams{AccountID: "acc", Data: []byte("x")}, api.CodeInvalidArgument},
		"inline non-image": {api.AttachmentImportParams{AccountID: "acc", Path: pdf, Inline: true}, api.CodeInvalidArgument},
		"data too big":     {api.AttachmentImportParams{AccountID: "acc", Data: make([]byte, api.MaxAttachmentDataBytes+1), Filename: "big"}, api.CodeAttachmentTooBig},
	}
	for name, c := range cases {
		done := make(chan error, 1)
		go func() {
			_, err := a.Import(ctx, c.p)
			done <- err
		}()
		select {
		case err := <-done:
			if code := errCode(t, err); code != c.code {
				t.Errorf("%s: code %d, want %d", name, code, c.code)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: import hung", name)
		}
	}

	// Sparse file one byte over the limit: rejected at stat time.
	sparse := filepath.Join(dir, "sparse")
	f, _ := os.Create(sparse)
	f.Truncate(api.MaxAttachmentBytes + 1)
	f.Close()
	_, err := a.Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: sparse})
	if errCode(t, err) != api.CodeAttachmentTooBig {
		t.Errorf("sparse over limit: %v", err)
	}
	var e *api.Error
	if errorsAs(err, &e) {
		if data, ok := e.Data.(map[string]int64); !ok || data["limit"] != api.MaxAttachmentBytes {
			t.Errorf("attachmentTooBig data = %#v", e.Data)
		}
	}
}

func TestAttachmentImportMetadata(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	a := b.Attachments()

	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	cases := []struct {
		name, want, wantName string
		content              []byte
	}{
		{"x.txt", "image/png", "x.txt", png},                                  // sniffed, extension ignored
		{"x.pdf", "application/pdf", "x.pdf", bytes.Repeat([]byte{0x7f}, 64)}, // inconclusive → extension
		{"../../etc/passwd", "text/plain", "passwd", []byte("root:x:0:0\n")},  // path stripped
		{"a\x00b\n.md", "text/markdown", "ab.md", []byte("# hi\n")},           // controls stripped
		{"  ...  ", "text/plain", "attachment", []byte("plain words\n")},      // nothing usable → fallback
	}
	for _, c := range cases {
		res, err := a.Import(ctx, api.AttachmentImportParams{AccountID: "acc", Data: c.content, Filename: c.name})
		if err != nil {
			t.Fatalf("%q: %v", c.name, err)
		}
		if res.Attachment.ContentType != c.want || res.Attachment.Filename != c.wantName || res.Attachment.Size != int64(len(c.content)) {
			t.Errorf("%q: got %+v, want type %s name %s", c.name, res.Attachment, c.want, c.wantName)
		}
	}

	// Symlink to a regular file is followed.
	target := writeTestFile(t, "real.txt", []byte("hello"))
	link := filepath.Join(filepath.Dir(target), "link.txt")
	os.Symlink(target, link)
	res, err := a.Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: link})
	if err != nil || res.Attachment.Filename != "link.txt" || res.Attachment.Size != 5 {
		t.Errorf("symlink: %+v %v", res, err)
	}

	// Remove is idempotent and tolerant of unknown ids.
	if _, err := a.Remove(ctx, api.AttachmentRemoveParams{AccountID: "acc", AttachmentID: res.Attachment.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Remove(ctx, api.AttachmentRemoveParams{AccountID: "acc", AttachmentID: res.Attachment.ID}); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.store.AttachmentDir(), res.Attachment.ID)); err == nil {
		t.Error("file survived remove")
	}
	if len(newContentID()) < 40 || !strings.HasSuffix(newContentID(), "@malachi.local") {
		t.Error("content id format")
	}
}

func TestDraftAttachmentTotalLimit(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	// Two 13 MiB files are each under the per-file limit but over the sum.
	big := make([]byte, 13<<20)
	var ids []api.DraftAttachment
	for i := 0; i < 2; i++ {
		res, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: "acc", Data: big, Filename: "big.bin"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, api.DraftAttachment{ID: res.Attachment.ID})
	}
	_, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: "acc", Attachments: ids}})
	if errCode(t, err) != api.CodeAttachmentTooBig {
		t.Fatalf("sum over limit: %v", err)
	}
}
