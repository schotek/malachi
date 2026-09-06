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

func (r fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// mxResolver adds MX answers to a fakeResolver.
type mxResolver struct {
	fakeResolver
	mx map[string][]*net.MX
}

func (r mxResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if records, ok := r.mx[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

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

func TestDiscoverGOAAccountWins(t *testing.T) {
	s := newISPDB(t, map[string]string{"contoso.example": "example-ssl-starttls.xml"})
	d := newDiscoverer(t, s)
	var asked string
	linked := &api.AccountConfig{
		Name: "Me@contoso.example", Email: "Me@contoso.example", Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: "account_1788512854_0"},
	}
	d.GOA = func(_ context.Context, email string) (*api.AccountConfig, string, bool) {
		asked = email
		if email != "Me@contoso.example" {
			return nil, "", false
		}
		return linked, MicrosoftProviderName, true
	}
	res, err := d.Discover(context.Background(), "Me@contoso.example")
	if err != nil || res.Source != api.DiscoverGOA || res.ProviderName != MicrosoftProviderName {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	// The hook's answer goes out as it is: it knows the account.
	if res.Config != linked {
		t.Fatalf("config = %+v", res.Config)
	}
	if asked != "Me@contoso.example" {
		t.Fatalf("GOA asked for %q", asked)
	}
	s.mu.Lock()
	paths := len(s.paths)
	s.mu.Unlock()
	if paths != 0 {
		t.Fatalf("ISPDB consulted although GOA matched: %v", s.paths)
	}

	// No match: the usual lookups run.
	d.GOA = func(context.Context, string) (*api.AccountConfig, string, bool) { return nil, "", false }
	res, err = d.Discover(context.Background(), "me@contoso.example")
	if err != nil || res.Source != api.DiscoverISPDB || res.Config.Kind != api.AccountIMAP {
		t.Fatalf("without GOA match: %+v err = %v", res, err)
	}
}

func TestDiscoverMicrosoftByMX(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	d.Resolver = mxResolver{fakeResolver: fakeResolver{}, mx: map[string][]*net.MX{
		"contoso.example": {{Host: "contoso-example.mail.protection.outlook.com.", Pref: 0}},
	}}
	verified := 0
	d.VerifyIMAP = func(context.Context, api.ServerConfig) error { verified++; return nil }
	d.VerifySMTP = d.VerifyIMAP
	res, err := d.Discover(context.Background(), "me@contoso.example")
	if err != nil || res.Source != api.DiscoverProvider || res.ProviderName != MicrosoftProviderName {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	c := res.Config
	if c == nil || c.Kind != api.AccountGraph || c.Graph == nil || c.Graph.Source != api.GraphSourceGOA || c.Graph.GOAAccountID != "" || c.IMAP != nil {
		t.Fatalf("config = %+v", c)
	}
	if verified != 0 {
		t.Fatalf("guesses verified for a Microsoft domain: %d", verified)
	}
}

// A Google address is the sign-in hint even though the ISPDB has a
// password entry for it: that entry works with an app password at best.
func TestDiscoverGoogleDomain(t *testing.T) {
	s := newISPDB(t, map[string]string{"gmail.com": "example-ssl-starttls.xml"})
	d := newDiscoverer(t, s)
	verified := 0
	d.VerifyIMAP = func(context.Context, api.ServerConfig) error { verified++; return nil }
	d.VerifySMTP = d.VerifyIMAP
	for _, email := range []string{"me@gmail.com", "me@GoogleMail.com"} {
		res, err := d.Discover(context.Background(), email)
		if err != nil || res.Source != api.DiscoverProvider || res.ProviderName != GoogleProviderName {
			t.Fatalf("%s: res = %+v err = %v", email, res, err)
		}
		c := res.Config
		if c == nil || c.Kind != api.AccountIMAP || c.IMAP == nil || c.SMTP == nil || c.OAuth2 == nil ||
			c.IMAP.Host != "imap.gmail.com" || c.IMAP.AuthMethod != api.AuthOAuth2 || c.SMTP.AuthMethod != api.AuthOAuth2 ||
			c.OAuth2.Source != api.OAuth2SourceGOA || c.OAuth2.Provider != api.OAuth2ProviderGoogle || c.OAuth2.GOAAccountID != "" ||
			c.Email != email {
			t.Fatalf("%s: config = %+v", email, c)
		}
	}
	if verified != 0 {
		t.Fatalf("guesses verified for a Google domain: %d", verified)
	}

	// Signed in: the hook's complete account wins over the hint.
	linked := &api.AccountConfig{Email: "me@gmail.com", Kind: api.AccountIMAP}
	d.GOA = func(context.Context, string) (*api.AccountConfig, string, bool) {
		return linked, GoogleProviderName, true
	}
	res, err := d.Discover(context.Background(), "me@gmail.com")
	if err != nil || res.Source != api.DiscoverGOA || res.Config != linked {
		t.Fatalf("with GOA: %+v err = %v", res, err)
	}
}

// A Google Workspace domain is recognised by its MX records.
func TestDiscoverGoogleByMX(t *testing.T) {
	s := newISPDB(t, nil)
	d := newDiscoverer(t, s)
	d.Resolver = mxResolver{fakeResolver: fakeResolver{}, mx: map[string][]*net.MX{
		"example.org": {{Host: "ASPMX.L.GOOGLE.COM.", Pref: 1}, {Host: "alt1.aspmx.l.google.com.", Pref: 5}},
	}}
	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil || res.Source != api.DiscoverProvider || res.ProviderName != GoogleProviderName ||
		res.Config == nil || res.Config.OAuth2 == nil || res.Config.OAuth2.Provider != api.OAuth2ProviderGoogle {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestDiscoverMicrosoftByISPDBHosts(t *testing.T) {
	s := newISPDB(t, map[string]string{"outlook.example": "microsoft-oauth2.xml"})
	d := newDiscoverer(t, s)
	res, err := d.Discover(context.Background(), "me@outlook.example")
	if err != nil || res.Source != api.DiscoverProvider || res.Config == nil || res.Config.Kind != api.AccountGraph {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestDiscoverPasswordEntryWinsOverMicrosoftMX(t *testing.T) {
	s := newISPDB(t, map[string]string{"example.org": "example-ssl-starttls.xml"})
	d := newDiscoverer(t, s)
	d.Resolver = mxResolver{fakeResolver: fakeResolver{}, mx: map[string][]*net.MX{
		"example.org": {{Host: "example-org.mail.protection.outlook.com."}},
	}}
	res, err := d.Discover(context.Background(), "me@example.org")
	if err != nil || res.Source != api.DiscoverISPDB || res.Config.Kind != api.AccountIMAP {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}
