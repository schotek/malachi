// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeResolver answers from a map keyed by "_service._tcp.domain".
type fakeResolver map[string][]*net.SRV

func (r fakeResolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	addrs, ok := r["_"+service+"._"+proto+"."+name]
	if !ok {
		return "", nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return name, addrs, nil
}

// ispdbServer serves testdata files by domain and records request paths.
type ispdbServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	files map[string]string // domain → testdata file
}

func newISPDB(t *testing.T, files map[string]string) *ispdbServer {
	t.Helper()
	s := &ispdbServer{files: files}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.RequestURI())
		s.mu.Unlock()
		domain := strings.TrimPrefix(r.URL.Path, "/v1.1/")
		f, ok := s.files[domain]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(testdata, f))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func newDiscoverer(t *testing.T, s *ispdbServer) *Discoverer {
	t.Helper()
	d := New(nil)
	d.HTTP = s.Client()
	d.HTTP.CheckRedirect = CheckRedirect
	d.ISPDBBase = s.URL + "/v1.1/"
	d.Provider = false // provider URLs would leave the test
	d.Resolver = fakeResolver{}
	d.VerifyIMAP = func(context.Context, api.ServerConfig) error { return errors.New("no") }
	d.VerifySMTP = func(context.Context, api.ServerConfig) error { return errors.New("no") }
	return d
}

func TestDiscoverISPDB(t *testing.T) {
	s := newISPDB(t, map[string]string{"example.org": "example-ssl-starttls.xml"})
	d := newDiscoverer(t, s)

	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != api.DiscoverISPDB || res.Config == nil || res.ProviderName != "Example Mail" {
		t.Fatalf("res = %+v", res)
	}
	c := res.Config
	if c.Name != "Example Mail" || c.Email != "me@example.org" || c.IMAP.Host != "imap.example.org" || c.SMTP.Port != 587 ||
		c.IMAP.Username != "me@example.org" || c.IMAP.AuthMethod != api.AuthPassword {
		t.Fatalf("config = %+v", c)
	}
	for _, p := range s.paths {
		if strings.Contains(p, "me@") || strings.Contains(p, "me%40") {
			t.Fatalf("address sent to ISPDB: %s", p)
		}
	}
	if s.paths[0] != "/v1.1/example.org" {
		t.Fatalf("path = %s", s.paths[0])
	}
}

func TestDiscoverSRVAndGuess(t *testing.T) {
	s := newISPDB(t, nil) // everything 404
	d := newDiscoverer(t, s)
	d.Resolver = fakeResolver{
		"_imaps._tcp.example.org": {{Target: "imap.example.org.", Port: 993}},
	}
	var tried []string
	var mu sync.Mutex
	d.VerifySMTP = func(_ context.Context, cfg api.ServerConfig) error {
		mu.Lock()
		tried = append(tried, cfg.Host)
		mu.Unlock()
		if cfg.Host == "mail.example.org" && cfg.Port == 587 {
			return nil
		}
		return errors.New("no")
	}
	imapCalls := 0
	d.VerifyIMAP = func(context.Context, api.ServerConfig) error { imapCalls++; return nil }

	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != api.DiscoverGuess || res.Config == nil {
		t.Fatalf("res = %+v", res)
	}
	if res.Config.IMAP.Host != "imap.example.org" || res.Config.IMAP.Security != api.SecurityTLS {
		t.Fatalf("imap from srv = %+v", res.Config.IMAP)
	}
	if res.Config.SMTP.Host != "mail.example.org" || res.Config.SMTP.Port != 587 || res.Config.SMTP.Security != api.SecuritySTARTTLS {
		t.Fatalf("smtp from guess = %+v", res.Config.SMTP)
	}
	if res.Config.Name != "example.org" {
		t.Fatalf("name = %q", res.Config.Name)
	}
	if imapCalls != 0 {
		t.Fatal("IMAP was guessed although SRV answered")
	}
	if len(tried) != 4 {
		t.Fatalf("smtp candidates tried = %v", tried)
	}
}

func TestDiscoverSRVNoService(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	d.Resolver = fakeResolver{
		"_imaps._tcp.example.org":      {{Target: ".", Port: 0}},
		"_imap._tcp.example.org":       {{Target: "imap.example.org.", Port: 143}},
		"_submission._tcp.example.org": {{Target: "smtp.example.org.", Port: 587}},
	}
	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != api.DiscoverSRV || res.Config.IMAP.Port != 143 || res.Config.IMAP.Security != api.SecuritySTARTTLS || res.Config.SMTP.Port != 587 {
		t.Fatalf("res = %+v cfg = %+v", res, res.Config)
	}
}

func TestDiscoverNothing(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil || res.Source != api.DiscoverNone || res.Config != nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	res, err = d.Discover(context.Background(), "me@exämple.org")
	if err != nil || res.Source != api.DiscoverNone {
		t.Fatalf("idna: %+v %v", res, err)
	}
	res, err = d.Discover(context.Background(), "me@[192.0.2.1]")
	if err != nil || res.Source != api.DiscoverNone {
		t.Fatalf("ip literal: %+v %v", res, err)
	}
}

func TestDiscoverGuessPrefersTableOrder(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	d.VerifyIMAP = func(_ context.Context, cfg api.ServerConfig) error {
		if cfg.Host == "mail.example.org" && cfg.Port == 993 {
			return nil
		}
		if cfg.Host == "imap.example.org" && cfg.Port == 143 {
			return nil
		}
		return errors.New("no")
	}
	d.VerifySMTP = func(context.Context, api.ServerConfig) error { return nil }
	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil || res.Config == nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if res.Config.IMAP.Host != "mail.example.org" || res.Config.IMAP.Port != 993 {
		t.Fatalf("winner = %+v", res.Config.IMAP)
	}
	if res.Config.SMTP.Host != "smtp.example.org" || res.Config.SMTP.Port != 587 {
		t.Fatalf("smtp winner = %+v", res.Config.SMTP)
	}
}

func TestDiscoverDeadline(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	d.VerifyIMAP = func(ctx context.Context, _ api.ServerConfig) error { <-ctx.Done(); return ctx.Err() }
	d.VerifySMTP = d.VerifyIMAP
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := d.Discover(ctx, "me@example.org")
	if err != nil || res.Source != api.DiscoverNone {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline not respected")
	}

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := d.Discover(cctx, "me@example.org"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestProviderURLsAndRedirects(t *testing.T) {
	urls := providerURLs("example.org", "me@example.org")
	if len(urls) != 2 || !strings.HasPrefix(urls[0], "https://autoconfig.example.org/") || !strings.Contains(urls[0], "emailaddress=me%40example.org") {
		t.Fatalf("urls = %v", urls)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.org/", nil)
	if CheckRedirect(req, nil) == nil {
		t.Fatal("http redirect accepted")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://example.org/", nil)
	if CheckRedirect(req, make([]*http.Request, maxRedirects)) == nil {
		t.Fatal("too many redirects accepted")
	}
	if !strings.HasPrefix(DefaultISPDBBase, "https://") {
		t.Fatal("ISPDB base must be https")
	}
}
