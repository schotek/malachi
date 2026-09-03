// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// maxCIDBytes caps what the cid: handler will read; inline images are
// attachments and share their limit.
const maxCIDBytes = 25 << 20

// The cid: scheme serves inline images of drafts being composed, from files
// the UI itself picked. Only ids registered here are served, so a pasted
// <img src="cid:../../etc/passwd"> yields an error. Registration on the
// default WebContext happens once per process, before the first view loads.
// The message viewer will use a different scheme (malachi-cid:), so a
// displayed message can never address compose attachments.
var cid = struct {
	once  sync.Once
	mu    sync.Mutex
	files map[string]cidFile
}{files: make(map[string]cidFile)}

type cidFile struct {
	path        string
	contentType string
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

// UnregisterCID forgets id.
func UnregisterCID(id string) {
	cid.mu.Lock()
	defer cid.mu.Unlock()
	delete(cid.files, id)
}

func serveCID(req *webkit.URISchemeRequest) {
	id := req.Path()
	if id == "" {
		id = strings.TrimPrefix(req.URI(), "cid:")
	}
	cid.mu.Lock()
	f, ok := cid.files[id]
	cid.mu.Unlock()
	if !ok {
		req.FinishError(errors.New("unknown inline image"))
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
	stream := gio.NewMemoryInputStreamFromBytes(glib.NewBytesWithGo(data))
	req.Finish(stream, int64(len(data)), f.contentType)
}
