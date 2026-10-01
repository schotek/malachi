// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oneclick

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "127.8.9.10": true, "::1": true,
		"10.0.0.1": true, "172.16.0.1": true, "172.31.255.255": true, "192.168.1.1": true,
		"fd00::1": true, "fc00::1": true,
		"169.254.169.254": true, "fe80::1": true, "ff02::1": true, "224.0.0.1": true,
		"0.0.0.0": true, "::": true, "0.1.2.3": true,
		"100.64.0.1": true, "100.127.255.255": true,
		"198.18.0.1": true, "240.0.0.1": true, "255.255.255.255": true,
		"::ffff:127.0.0.1": true, "::ffff:10.0.0.1": true, "::ffff:100.64.0.1": true,
		"8.8.8.8": false, "1.1.1.1": false, "100.63.255.255": false, "100.128.0.1": false,
		"172.32.0.1": false, "2606:4700:4700::1111": false, "::ffff:8.8.8.8": false,
	} {
		if got := BlockedIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("BlockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
	if !BlockedIP(netip.Addr{}) {
		t.Error("the zero address is not blocked")
	}
}

func TestGuard(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:443": false, "[::1]:443": false, "10.1.2.3:443": false, "[fe80::1%eth0]:443": false,
		"8.8.8.8:443": true, "[2606:4700:4700::1111]:443": true, "garbage": false,
	} {
		err := Guard("tcp", addr, nil)
		if (err == nil) != ok {
			t.Errorf("Guard(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

// The strict poster must not reach a loopback server, whatever the URL.
func TestPosterRefusesLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	err := New().Post(context.Background(), srv.URL)
	if err == nil || !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want a refused address", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the loopback server was reached")
	}
	if strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the error names the address: %v", err)
	}
}

func TestPosterRefusesNonHTTPS(t *testing.T) {
	for _, u := range []string{"http://example.com/u", "mailto:a@b.example", "https://", "https://user:pw@example.com/", "::bad", ""} {
		if err := New().Post(context.Background(), u); err == nil {
			t.Errorf("Post(%q) succeeded", u)
		}
	}
}

// testPoster reaches a test server: the transport trusts it and may dial
// loopback; everything else is Post's own.
func testPoster(srv *httptest.Server) *Poster {
	c := srv.Client()
	c.Jar = nil
	return &Poster{Client: c, Refuse: func(string) bool { return false }}
}

func TestPostRequest(t *testing.T) {
	var got struct {
		method, ctype, ua, referer, cookie, body string
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.ctype, got.ua, got.referer, got.cookie, got.body = r.Method, r.Header.Get("Content-Type"),
			r.Header.Get("User-Agent"), r.Header.Get("Referer"), r.Header.Get("Cookie"), string(b)
		http.SetCookie(w, &http.Cookie{Name: "a", Value: "b"})
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	if err := testPoster(srv).Post(context.Background(), srv.URL+"/u?id=1"); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.ctype != "application/x-www-form-urlencoded" || got.ua != "Malachi Mail" ||
		got.body != "List-Unsubscribe=One-Click" || got.referer != "" || got.cookie != "" {
		t.Errorf("request %+v", got)
	}
}

func TestPostStatuses(t *testing.T) {
	for _, c := range []struct {
		code int
		ok   bool
	}{{200, true}, {204, true}, {299, true}, {301, false}, {302, false}, {307, false}, {400, false}, {404, false}, {500, false}} {
		var follow atomic.Int32
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/target" {
				follow.Add(1)
				return
			}
			if c.code/100 == 3 {
				w.Header().Set("Location", "/target")
			}
			w.WriteHeader(c.code)
		}))
		err := testPoster(srv).Post(context.Background(), srv.URL+"/u")
		srv.Close()
		var se *StatusError
		switch {
		case c.ok && err != nil:
			t.Errorf("%d: %v", c.code, err)
		case !c.ok && !errors.As(err, &se):
			t.Errorf("%d: err = %v, want a status error", c.code, err)
		case !c.ok && se.Code != c.code:
			t.Errorf("%d: status %d", c.code, se.Code)
		}
		if follow.Load() != 0 {
			t.Errorf("%d: the redirect was followed", c.code)
		}
	}
}

func TestPostIgnoresClientRedirectPolicyAndJar(t *testing.T) {
	var followed atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed.Add(1)
			return
		}
		http.Redirect(w, r, "/target", http.StatusSeeOther)
	}))
	defer srv.Close()
	c := srv.Client()
	c.CheckRedirect = nil // the default policy would follow
	if err := (&Poster{Client: c, Refuse: func(string) bool { return false }}).Post(context.Background(), srv.URL); err == nil {
		t.Error("a redirect was accepted")
	}
	if followed.Load() != 0 {
		t.Error("redirect followed")
	}
}

func TestPostBoundsTheAnswerAndDropsTheURLFromErrors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.CopyN(w, zeroReader{}, 10<<20)
	}))
	defer srv.Close()
	if err := testPoster(srv).Post(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	// A dead address: the error carries no URL.
	dead := &Poster{Refuse: func(string) bool { return false }, Client: &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	}}}}
	err := dead.Post(context.Background(), "https://secret-host.example/secret-path")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("err = %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return len(p), nil }

func TestRefusedHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "10.0.0.5": true, "::1": true, "169.254.169.254": true, "100.64.0.1": true,
		"localhost": true, "intranet": true, "printer.local": true, "x.localhost": true, "Foo.INTERNAL": true,
		"nas.home.arpa": true, "example.local.": true, "": true,
		"8.8.8.8": false, "news.example": false, "a.b.example.com": false, "localhost.example.com": false,
		"2606:4700:4700::1111": false,
	} {
		if got := RefusedHost(host); got != want {
			t.Errorf("RefusedHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// The refusal comes before any transport: a proxy in the environment
// must not be asked to reach these.
func TestPostRefusesHostsBeforeTransport(t *testing.T) {
	var used atomic.Int32
	p := &Poster{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		used.Add(1)
		return nil, errors.New("must not be used")
	})}}
	for _, u := range []string{"https://127.0.0.1/u", "https://[::1]/u", "https://intranet/u", "https://x.local/u", "https://a.internal/u", "https://nas.home.arpa/u", "https://10.1.2.3/u"} {
		if err := p.Post(context.Background(), u); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("Post(%q) = %v", u, err)
		}
	}
	if used.Load() != 0 {
		t.Error("the transport was used")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClass(t *testing.T) {
	for want, err := range map[string]error{
		"status":  &StatusError{Code: 500},
		"blocked": fmt.Errorf("dial: %w", ErrBlockedAddress),
		"timeout": context.DeadlineExceeded,
		"dns":     &net.DNSError{Err: "no such host", Name: "secret.example"},
		"tls":     &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		"refused": fmt.Errorf("dial: %w", syscall.ECONNREFUSED),
		"other":   errors.New("whatever secret.example"),
	} {
		if got := Class(err); got != want {
			t.Errorf("Class(%v) = %q, want %q", err, got, want)
		}
	}
	if Class(nil) != "" {
		t.Error("nil")
	}
}
