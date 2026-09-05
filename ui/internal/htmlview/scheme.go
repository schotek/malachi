// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Scheme is the URL scheme the sanitiser rewrites cid: references to:
// malachi-cid:<accountId>/<messageId>/<partId>. The path names the message,
// so the handler below is stateless and switching messages while pictures
// still load cannot hand one message another one's part.
const Scheme = "malachi-cid"

// partTimeout bounds one part fetch from the daemon.
const partTimeout = 30 * time.Second

// PartFetcher gets the content of one MIME part of a message from the
// daemon (message.part). It is called off the main loop.
type PartFetcher func(ctx context.Context, accountID api.AccountID, messageID api.MessageID, partID string) (contentType string, data []byte, err error)

// The scheme is registered on the default WebContext once per process; the
// fetcher behind it may be replaced (tests, a reconnected client).
var scheme = struct {
	once  sync.Once
	mu    sync.Mutex
	fetch PartFetcher
}{}

func registerScheme(fetch PartFetcher) {
	scheme.mu.Lock()
	scheme.fetch = fetch
	scheme.mu.Unlock()
	scheme.once.Do(func() {
		ctx := webkit.WebContextGetDefault()
		ctx.RegisterURIScheme(Scheme, serve)
		// Documents from the scheme (there are none) could reach nothing,
		// and loading it is not mixed content.
		sec := ctx.SecurityManager()
		sec.RegisterURISchemeAsNoAccess(Scheme)
		sec.RegisterURISchemeAsSecure(Scheme)
	})
}

// serve answers one malachi-cid: request: the part's bytes when the daemon
// has them and they are a picture, an error otherwise. The daemon is asked
// off the main loop and the request is finished back on it.
func serve(req *webkit.URISchemeRequest) {
	acc, msg, part, ok := ParsePath(strings.TrimPrefix(req.URI(), Scheme+":"))
	if !ok {
		req.FinishError(errors.New("malformed part reference"))
		return
	}
	scheme.mu.Lock()
	fetch := scheme.fetch
	scheme.mu.Unlock()
	if fetch == nil {
		req.FinishError(errors.New("no part source"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), partTimeout)
		defer cancel()
		ct, data, err := fetch(ctx, api.AccountID(acc), api.MessageID(msg), part)
		glib.IdleAdd(func() {
			switch {
			case err != nil:
				req.FinishError(err)
			case !ImageType(ct):
				req.FinishError(errors.New("part is not a picture"))
			default:
				stream := gio.NewMemoryInputStreamFromBytes(glib.NewBytesWithGo(data))
				req.Finish(stream, int64(len(data)), ct)
			}
		})
	}()
}
