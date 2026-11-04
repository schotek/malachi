// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// maxCIDBytes caps what the cid: handler will serve; inline images are
// attachments and share their limit.
const maxCIDBytes = 25 << 20

// fetchTimeout bounds one Fetcher call: the daemon reads a file of the
// attachment store, which is quick, but the request must not hang the
// view's image forever when the daemon is gone.
const fetchTimeout = 60 * time.Second

// The cid: scheme serves the inline images of drafts being composed: files
// the UI itself picked, and copies the backend made of a quoted original's
// pictures, which the backend hands over through attachment.get. Only ids
// registered here are served, so a pasted <img src="cid:../../etc/passwd">
// yields an error. Registration on the default WebContext happens once per
// process, before the first view loads. The message viewer uses a
// different scheme (malachi-cid:), so a displayed message can never
// address compose attachments.
var cid = struct {
	once  sync.Once
	mu    sync.Mutex
	files map[string]cidFile
}{files: make(map[string]cidFile)}

// Fetcher produces the bytes and the media type of one inline image the
// backend holds. It is called off the main loop.
type Fetcher func(ctx context.Context) (data []byte, contentType string, err error)

// cidFile is one registered id: a local file, or a fetcher.
type cidFile struct {
	path        string
	contentType string
	fetch       Fetcher
}

func registerCIDScheme() {
	cid.once.Do(func() {
		webkit.WebContextGetDefault().RegisterURIScheme("cid", serveCID)
	})
}

// RegisterCID makes cid:<id> resolve to the file at path.
func RegisterCID(id, path, contentType string) {
	cid.mu.Lock()
	defer cid.mu.Unlock()
	cid.files[id] = cidFile{path: path, contentType: contentType}
}

// RegisterCIDFetcher makes cid:<id> resolve to what fetch returns.
func RegisterCIDFetcher(id string, fetch Fetcher) {
	cid.mu.Lock()
	defer cid.mu.Unlock()
	cid.files[id] = cidFile{fetch: fetch}
}

// CIDRegistered reports whether id resolves to anything.
func CIDRegistered(id string) bool {
	_, ok := lookupCID(id)
	return ok
}

// UnregisterCID forgets id.
func UnregisterCID(id string) {
	cid.mu.Lock()
	defer cid.mu.Unlock()
	delete(cid.files, id)
}

func lookupCID(id string) (cidFile, bool) {
	cid.mu.Lock()
	defer cid.mu.Unlock()
	f, ok := cid.files[id]
	return f, ok
}

// checkInline is the gate every served image passes: bytes present,
// within the cap, of a picture type the view may render (never SVG, which
// can script).
func checkInline(data []byte, contentType string) error {
	if len(data) == 0 {
		return errors.New("inline image is empty")
	}
	if len(data) > maxCIDBytes {
		return errors.New("inline image too big")
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if !strings.HasPrefix(ct, "image/") || ct == "image/svg+xml" {
		return errors.New("inline image is not a picture")
	}
	return nil
}

// serveCID answers one cid: request: a local file at once, a backend copy
// off the main loop, finished back on it (the request must be answered
// on the loop it came from).
func serveCID(req *webkit.URISchemeRequest) {
	id := req.Path()
	if id == "" {
		id = strings.TrimPrefix(req.URI(), "cid:")
	}
	f, ok := lookupCID(id)
	if !ok {
		req.FinishError(errors.New("unknown inline image"))
		return
	}
	if f.fetch != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
			defer cancel()
			data, ct, err := f.fetch(ctx)
			glib.IdleAdd(func() {
				if err == nil {
					err = checkInline(data, ct)
				}
				if err != nil {
					req.FinishError(err)
					return
				}
				finishCID(req, data, ct)
			})
		}()
		return
	}
	info, err := os.Stat(f.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCIDBytes {
		req.FinishError(errors.New("inline image unavailable"))
		return
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		req.FinishError(err)
		return
	}
	finishCID(req, data, f.contentType)
}

func finishCID(req *webkit.URISchemeRequest, data []byte, contentType string) {
	stream := gio.NewMemoryInputStreamFromBytes(glib.NewBytesWithGo(data))
	req.Finish(stream, int64(len(data)), contentType)
}
