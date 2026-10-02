// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"
)

type loopbackBrowser struct{ launch func(string) error }

func NewBrowser(launch func(string) error) Browser { return &loopbackBrowser{launch} }
func (b *loopbackBrowser) Authorize(ctx context.Context, authorization func(string) (string, error), state string) (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", authError("browser")
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + CallbackPath
	result := make(chan string, 1)
	srv := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxHeaderBytes: 16 << 10}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		callback := "http://" + listener.Addr().String() + r.URL.RequestURI()
		_, err := parseCallback(callback, redirect, state)
		if r.Method != "GET" || r.Host != listener.Addr().String() || !strings.HasPrefix(r.URL.RequestURI(), CallbackPath+"?") || len(r.TransferEncoding) > 0 || r.ContentLength > 0 || err != nil {
			http.Error(w, "Invalid sign-in callback.", 400)
			return
		}
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(200)
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		// Complete the response before the owner closes the one-shot listener.
		select {
		case result <- callback:
		default:
		}
	})
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()
	u, err := authorization(redirect)
	if err != nil {
		return "", err
	}
	if b.launch == nil || b.launch(u) != nil {
		return "", authError("browser")
	}
	select {
	case callback := <-result:
		return callback, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
