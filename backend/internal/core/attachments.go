package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type attachmentService struct{ b *Backend }

// Import copies a file (by path) or an inline payload into the attachment
// store. Paths are hostile input from the UI's point of view too: they must
// be absolute and name a regular file, are opened non-blocking so a FIFO or
// device cannot hang the daemon, and are size-capped during the copy, not
// only at stat time.
func (s *attachmentService) Import(ctx context.Context, p api.AttachmentImportParams) (*api.AttachmentImportResult, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if p.AccountID == "" {
		return nil, bad("accountId is required")
	}
	if (p.Path == "") == (len(p.Data) == 0) {
		return nil, bad("exactly one of path and data must be given")
	}

	var (
		r    io.Reader
		name string
	)
	if p.Path != "" {
		if !filepath.IsAbs(p.Path) {
			return nil, bad("path must be absolute")
		}
		f, err := os.OpenFile(p.Path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, bad("cannot open %q: %v", p.Path, err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, bad("cannot stat %q: %v", p.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, bad("%q is not a regular file", p.Path)
		}
		if info.Size() == 0 {
			return nil, bad("%q is empty", p.Path)
		}
		if info.Size() > api.MaxAttachmentBytes {
			return nil, tooBig(api.MaxAttachmentBytes, info.Size())
		}
		r, name = f, filepath.Base(p.Path)
	} else {
		if p.Filename == "" {
			return nil, bad("filename is required with data")
		}
		if len(p.Data) > api.MaxAttachmentDataBytes {
			return nil, tooBig(api.MaxAttachmentDataBytes, int64(len(p.Data)))
		}
		r = bytes.NewReader(p.Data)
	}
	if p.Filename != "" {
		name = p.Filename
	}
	name = safename.Filename(name)

	head, r, err := peek(r, 512)
	if err != nil {
		return nil, bad("cannot read file: %v", err)
	}
	ctype := detectContentType(head, name)
	if p.Inline && !strings.HasPrefix(ctype, "image/") {
		return nil, bad("only images can be inline (detected %s)", ctype)
	}

	a := store.Attachment{
		AccountID:   string(p.AccountID),
		Filename:    name,
		ContentType: ctype,
		Inline:      p.Inline,
	}
	if p.Inline {
		a.ContentID = newContentID()
	}
	switch err := s.b.store.ImportAttachment(ctx, &a, r, api.MaxAttachmentBytes); {
	case errors.Is(err, store.ErrTooBig):
		return nil, tooBig(api.MaxAttachmentBytes, api.MaxAttachmentBytes+1)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.AttachmentImportResult{Attachment: toAPIAttachment(a)}, nil
}

// Remove deletes an attachment; unknown ids are ignored. If it was bound to
// a draft it simply disappears from that draft.
func (s *attachmentService) Remove(ctx context.Context, p api.AttachmentRemoveParams) (*api.AttachmentRemoveResult, error) {
	if p.AccountID == "" || p.AttachmentID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and attachmentId are required")
	}
	if err := s.b.store.RemoveAttachment(ctx, string(p.AccountID), p.AttachmentID); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.AttachmentRemoveResult{}, nil
}

// peek reads up to n bytes for sniffing and returns a reader that replays
// them.
func peek(r io.Reader, n int) ([]byte, io.Reader, error) {
	head := make([]byte, n)
	m, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	head = head[:m]
	return head, io.MultiReader(bytes.NewReader(head), r), nil
}

// detectContentType sniffs the content and only falls back to the file
// extension when sniffing is inconclusive. The client never sets it.
func detectContentType(head []byte, name string) string {
	mt, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		mt = "application/octet-stream"
	}
	if mt == "application/octet-stream" || mt == "text/plain" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
			if emt, _, err := mime.ParseMediaType(byExt); err == nil {
				mt = emt
			}
		}
	}
	return mt
}

func newContentID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:]) + "@malachi.local"
}
