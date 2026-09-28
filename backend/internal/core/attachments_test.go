// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"mime"
	"os"
	"os/exec"
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
	pdf := writeTestFile(t, "doc.pdf", []byte("%PDF-1.4 fake"))

	type importCase struct {
		p    api.AttachmentImportParams
		code api.ErrorCode
	}
	cases := map[string]importCase{
		"no account":       {api.AttachmentImportParams{Path: pdf}, api.CodeInvalidArgument},
		"relative path":    {api.AttachmentImportParams{AccountID: "acc", Path: "doc.pdf"}, api.CodeInvalidArgument},
		"missing":          {api.AttachmentImportParams{AccountID: "acc", Path: filepath.Join(dir, "nope")}, api.CodeInvalidArgument},
		"directory":        {api.AttachmentImportParams{AccountID: "acc", Path: dir}, api.CodeInvalidArgument},
		"empty file":       {api.AttachmentImportParams{AccountID: "acc", Path: empty}, api.CodeInvalidArgument},
		"neither":          {api.AttachmentImportParams{AccountID: "acc"}, api.CodeInvalidArgument},
		"both":             {api.AttachmentImportParams{AccountID: "acc", Path: pdf, Data: []byte("x")}, api.CodeInvalidArgument},
		"data no name":     {api.AttachmentImportParams{AccountID: "acc", Data: []byte("x")}, api.CodeInvalidArgument},
		"inline non-image": {api.AttachmentImportParams{AccountID: "acc", Path: pdf, Inline: true}, api.CodeInvalidArgument},
		"data too big":     {api.AttachmentImportParams{AccountID: "acc", Data: make([]byte, api.MaxAttachmentDataBytes+1), Filename: "big"}, api.CodeAttachmentTooBig},
	}
	// Where this process may make symbolic links (on Windows that takes a
	// privilege or developer mode).
	if dirLink := filepath.Join(dir, "dirlink"); os.Symlink(dir, dirLink) == nil {
		cases["symlink to dir"] = importCase{api.AttachmentImportParams{AccountID: "acc", Path: dirLink}, api.CodeInvalidArgument}
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

// A named pipe is refused at once: the import opens without waiting for a
// writer, and a pipe is not a regular file.
func TestAttachmentImportRejectsNamedPipe(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	fifo := filepath.Join(t.TempDir(), "fifo")
	mkfifo(t, fifo)

	done := make(chan error, 1)
	go func() {
		_, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: fifo})
		done <- err
	}()
	select {
	case err := <-done:
		if code := errCode(t, err); code != api.CodeInvalidArgument {
			t.Errorf("code %d, want %d", code, api.CodeInvalidArgument)
		}
	case <-time.After(5 * time.Second):
		// A writer lets the import go on, so it does not outlive the test.
		if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
		t.Fatal("import hung on a named pipe")
	}
}

// mkfifo makes a named pipe at path, or skips the test where it cannot. On
// Windows, Git's mkfifo writes a Cygwin shortcut beside path, which Go does
// not see as anything.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("cannot make a named pipe here: %v %s", err, out)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode().Type() != fs.ModeNamedPipe {
		t.Skipf("mkfifo made no named pipe here (%v)", err)
	}
}

func TestAttachmentImportMetadata(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	a := b.Attachments()
	// The extension decides only when sniffing is inconclusive, and then
	// through the host's type database: Linux has .md as text/markdown,
	// the Windows registry and macOS's mime.types have no .md at all. The
	// test pins the one type it relies on.
	if err := mime.AddExtensionType(".md", "text/markdown"); err != nil {
		t.Fatal(err)
	}

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

	// Symlink to a regular file is followed, where this process may make
	// one (on Windows that takes a privilege or developer mode); the file
	// itself otherwise, for what follows.
	target := writeTestFile(t, "real.txt", []byte("hello"))
	path, wantName := filepath.Join(filepath.Dir(target), "link.txt"), "link.txt"
	if err := os.Symlink(target, path); err != nil {
		t.Logf("no symbolic link here, importing the file itself: %v", err)
		path, wantName = target, "real.txt"
	}
	res, err := a.Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: path})
	if err != nil || res.Attachment.Filename != wantName || res.Attachment.Size != 5 {
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

func TestAttachmentGet(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	imp, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: writeTestFile(t, "pic.png", png), Inline: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: "acc", AttachmentID: imp.Attachment.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.AttachmentID != imp.Attachment.ID || got.Filename != "pic.png" || got.ContentType != "image/png" || got.Size != int64(len(png)) || !bytes.Equal(got.Data, png) {
		t.Errorf("get = %+v", got)
	}

	for name, p := range map[string]api.AttachmentGetParams{
		"other account": {AccountID: "other", AttachmentID: imp.Attachment.ID},
		"unknown":       {AccountID: "acc", AttachmentID: "att_nope"},
		"traversal":     {AccountID: "acc", AttachmentID: "../store.db"},
	} {
		if _, err := b.Attachments().Get(ctx, p); errCode(t, err) != api.CodeAttachmentNotFound {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: "acc"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("no id: %v", err)
	}

	// Over the payload cap: the file is not read.
	big := make([]byte, api.MaxAttachmentDataBytes+1)
	copy(big, png)
	imp, err = b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: writeTestFile(t, "big.png", big)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: "acc", AttachmentID: imp.Attachment.ID})
	var apiErr *api.Error
	if errCode(t, err) != api.CodeAttachmentTooBig || !errors.As(err, &apiErr) || apiErr.Data == nil {
		t.Errorf("over the cap: %v", err)
	}
}
