// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package remoteimg downloads the remote images of a message on the user's
// behalf, so the view never touches the network itself. It is called only
// after the remote-content policy resolved to allow (internal/core), and it
// fetches as little as it can: https only, no cookies, no Referer, image
// bytes only, with caps on size, count and time.
//
// What a fetch reveals to the sender is unavoidable by design — that the
// message was opened, from which address, at what time — and is exactly
// what the user consented to. What it must not reveal is anything else:
// no identity beyond a fixed User-Agent, no session, no page of origin.
package remoteimg

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Defaults.
const (
	DefaultMaxImageBytes = 2 << 20
	DefaultMaxTotalBytes = 8 << 20
	DefaultMaxImages     = 32
	DefaultConcurrency   = 4
	DefaultTimeout       = 15 * time.Second
	maxRedirects         = 5
	userAgent            = "Malachi Mail"
)

// Image is one fetched picture: the media type sniffed from its bytes (the
// server's Content-Type is not trusted) and the bytes themselves.
type Image struct {
	MediaType string
	Data      []byte
}

// Fetcher downloads images within its caps. The zero value is not usable;
// use New.
type Fetcher struct {
	Client        *http.Client
	MaxImageBytes int64
	MaxTotalBytes int64
	MaxImages     int
	Concurrency   int
	log           *slog.Logger
}

// New returns a Fetcher with the defaults: a client without a cookie jar
// that follows at most five redirects, https to https only, and never
// sends a Referer.
func New(log *slog.Logger) *Fetcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Fetcher{
		Client: &http.Client{
			Timeout:       DefaultTimeout,
			CheckRedirect: checkRedirect,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				MaxIdleConns:          DefaultConcurrency,
			},
		},
		MaxImageBytes: DefaultMaxImageBytes,
		MaxTotalBytes: DefaultMaxTotalBytes,
		MaxImages:     DefaultMaxImages,
		Concurrency:   DefaultConcurrency,
		log:           log.With("component", "remoteimg"),
	}
}

// checkRedirect keeps a redirect chain short, on https, and free of the
// Referer the client would otherwise add.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return errors.New("redirect leaves https")
	}
	req.Header.Del("Referer")
	return nil
}

// FetchAll downloads the images in parallel and returns the ones that came
// back as images within the caps, keyed by the URL they were asked for.
// Failures are logged at debug level and simply absent: the sanitiser
// counts a missing image as blocked.
func (f *Fetcher) FetchAll(ctx context.Context, urls []string) map[string]Image {
	if len(urls) > f.MaxImages {
		f.log.Debug("remote images capped", "requested", len(urls), "max", f.MaxImages)
		urls = urls[:f.MaxImages]
	}
	out := make(map[string]Image, len(urls))
	if len(urls) == 0 {
		return out
	}
	var (
		mu    sync.Mutex
		total int64
		wg    sync.WaitGroup
		sem   = make(chan struct{}, max(f.Concurrency, 1))
	)
	for _, u := range urls {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			img, err := f.fetch(ctx, u)
			if err != nil {
				f.log.Debug("remote image not loaded", "url", u, "err", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if total+int64(len(img.Data)) > f.MaxTotalBytes {
				f.log.Debug("remote image over the total cap", "url", u)
				return
			}
			total += int64(len(img.Data))
			out[u] = img
		}(u)
	}
	wg.Wait()
	return out
}

// fetch downloads one image.
func (f *Fetcher) fetch(ctx context.Context, raw string) (Image, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Image{}, err
	}
	if u.Scheme != "https" || u.Host == "" {
		return Image{}, errors.New("not an https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Image{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := f.Client.Do(req)
	if err != nil {
		return Image{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Image{}, errors.New("status " + resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxImageBytes+1))
	if err != nil {
		return Image{}, err
	}
	if int64(len(data)) > f.MaxImageBytes {
		return Image{}, errors.New("over the size cap")
	}
	mt := sniff(data)
	if mt == "" {
		return Image{}, errors.New("not an image")
	}
	return Image{MediaType: mt, Data: data}, nil
}

// sniff returns the media type of data when it is an image the view may
// show, "" otherwise. SVG is a document with scripts and references of its
// own and is never accepted, whatever the server called it.
func sniff(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	mt := http.DetectContentType(data)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	mt = strings.ToLower(strings.TrimSpace(mt))
	if !strings.HasPrefix(mt, "image/") || mt == "image/svg+xml" {
		return ""
	}
	return mt
}
