// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package remoteimg

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// pngBytes is a 1×1 PNG; the sniffer recognises the signature.
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40))

func newServer(t *testing.T) (*httptest.Server, *Fetcher, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.png", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Referer") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("request carried Referer %q / Cookie %q", r.Header.Get("Referer"), r.Header.Get("Cookie"))
		}
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		http.SetCookie(w, &http.Cookie{Name: "track", Value: "1"})
		w.Header().Set("Content-Type", "text/plain") // lies; the bytes decide
		w.Write(pngBytes)
	})
	mux.HandleFunc("/html.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // lies the other way
		w.Write([]byte("<html><body>not an image</body></html>"))
	})
	mux.HandleFunc("/svg.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>x()</script></svg>`))
	})
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
		w.Write(bytes.Repeat([]byte{0}, 5000))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok.png", http.StatusFound)
	})
	mux.HandleFunc("/downgrade", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/ok.png", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	mux.HandleFunc("/missing.png", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	f := New(nil)
	f.Client = srv.Client()
	f.Client.CheckRedirect = checkRedirect
	f.MaxImageBytes = 2000
	return srv, f, &hits
}

func TestFetchAll(t *testing.T) {
	srv, f, hits := newServer(t)
	ctx := context.Background()
	base := srv.URL

	got := f.FetchAll(ctx, []string{
		base + "/ok.png", base + "/html.png", base + "/svg.png", base + "/big.png",
		base + "/redirect", base + "/downgrade", base + "/loop", base + "/missing.png",
		"http://" + strings.TrimPrefix(base, "https://") + "/ok.png",
		"ftp://x/y.png", "not a url",
	})
	if img, ok := got[base+"/ok.png"]; !ok || img.MediaType != "image/png" || !bytes.Equal(img.Data, pngBytes) {
		t.Errorf("ok.png = %+v, %v", img, ok)
	}
	if img, ok := got[base+"/redirect"]; !ok || img.MediaType != "image/png" {
		t.Errorf("redirect = %+v, %v", img, ok)
	}
	for _, bad := range []string{"/html.png", "/svg.png", "/big.png", "/downgrade", "/loop", "/missing.png"} {
		if _, ok := got[base+bad]; ok {
			t.Errorf("%s was accepted", bad)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d images, want 2: %v", len(got), got)
	}
	// The plain-http and junk URLs never reached the server.
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want 2 (ok.png directly and via redirect)", hits.Load())
	}
}

func TestFetchAllCaps(t *testing.T) {
	srv, f, _ := newServer(t)
	ctx := context.Background()
	var urls []string
	for i := 0; i < 10; i++ {
		urls = append(urls, srv.URL+"/ok.png?"+strings.Repeat("x", i))
	}
	f.MaxImages = 3
	if got := f.FetchAll(ctx, urls); len(got) != 3 {
		t.Errorf("image count cap: got %d, want 3", len(got))
	}
	f.MaxImages = 10
	f.MaxTotalBytes = int64(len(pngBytes)) * 2
	if got := f.FetchAll(ctx, urls); len(got) != 2 {
		t.Errorf("total bytes cap: got %d, want 2", len(got))
	}
	if got := f.FetchAll(ctx, nil); len(got) != 0 {
		t.Errorf("no urls: got %v", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if got := f.FetchAll(cancelled, urls); len(got) != 0 {
		t.Errorf("cancelled context fetched %d images", len(got))
	}
}

func TestSniff(t *testing.T) {
	if sniff(pngBytes) != "image/png" {
		t.Errorf("png: %q", sniff(pngBytes))
	}
	if sniff([]byte("GIF89a"+strings.Repeat("\x00", 20))) != "image/gif" {
		t.Errorf("gif: %q", sniff([]byte("GIF89a")))
	}
	for _, bad := range [][]byte{nil, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), []byte("plain text"), []byte("%PDF-1.4")} {
		if mt := sniff(bad); mt != "" {
			t.Errorf("sniff(%q) = %q, want none", bad, mt)
		}
	}
}
