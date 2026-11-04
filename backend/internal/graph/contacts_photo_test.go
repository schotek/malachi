// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestContactPhoto(t *testing.T) {
	big := bytes.Repeat([]byte{0xff}, MaxContactPhotoBytes+1)
	var calls atomic.Int32
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		paths = append(paths, r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "Bearer photo-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.EscapedPath() {
		case "/me/contacts/c1/photo/$value":
			w.Header().Set("Content-Type", "image/JPEG; charset=binary")
			_, _ = w.Write([]byte("\xff\xd8\xffjpeg"))
		case "/me/contactFolders/f%2F1/contacts/c2/photo/$value":
			w.Header()["Content-Type"] = nil // no type, no sniffing
			_, _ = w.Write([]byte("\x89PNG"))
		case "/me/contacts/big/photo/$value":
			_, _ = w.Write(big)
		case "/me/contacts/chunked/photo/$value":
			w.Header().Set("Transfer-Encoding", "chunked")
			for i := 0; i < len(big); i += 1 << 20 {
				_, _ = w.Write(big[i:min(i+1<<20, len(big))])
				w.(http.Flusher).Flush()
			}
		case "/me/contacts/denied/photo/$value":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"ErrorAccessDenied","message":"Access is denied."}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"ErrorItemNotFound","message":"The specified object was not found."}}`))
		}
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, HTTP: srv.Client(), Limiter: NewLimiter(1),
		Token: func(context.Context) (string, error) { return "photo-token", nil },
		Sleep: func(context.Context, time.Duration) error { return nil }})
	ctx := context.Background()

	data, mt, err := c.ContactPhoto(ctx, DefaultContactFolderID, "c1")
	if err != nil || mt != "image/jpeg" || string(data) != "\xff\xd8\xffjpeg" {
		t.Errorf("default folder: %q %q %v", data, mt, err)
	}
	data, mt, err = c.ContactPhoto(ctx, "f/1", "c2")
	if err != nil || mt != "" || string(data) != "\x89PNG" {
		t.Errorf("other folder: %q %q %v", data, mt, err)
	}
	if _, _, err := c.ContactPhoto(ctx, "", "none"); !errors.Is(err, ErrNoPhoto) {
		t.Errorf("404: %v", err)
	}
	for _, id := range []string{"big", "chunked"} {
		if _, _, err := c.ContactPhoto(ctx, "", id); !errors.Is(err, ErrPhotoTooLarge) {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, _, err := c.ContactPhoto(ctx, "", "denied"); !errors.Is(err, ErrContactsAccessDenied) {
		t.Errorf("403: %v", err)
	}
	before := calls.Load()
	if _, _, err := c.ContactPhoto(ctx, "", "bad\nid"); !errors.Is(err, ErrNoPhoto) || calls.Load() != before {
		t.Errorf("bad id: %v, %d calls", err, calls.Load()-before)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := c.ContactPhoto(cctx, "", "c1"); err == nil {
		t.Error("cancelled context")
	}
}
