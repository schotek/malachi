// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package discover suggests IMAP/SMTP settings for an e-mail address, in
// order of trust: the Mozilla ISPDB (only the domain is sent), the
// provider's own autoconfig document (the address is sent to the provider
// itself), RFC 6186 DNS SRV records, and finally common host names
// verified by opening a TLS connection without authenticating. Nothing is
// stored; the caller validates the result like an account.add request.
//
// Every document and DNS answer is hostile input: sizes are capped, hosts
// and ports are validated, plaintext socket types are refused.
package discover

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// DefaultISPDBBase is Mozilla's autoconfig database; the domain is
	// appended.
	DefaultISPDBBase = "https://autoconfig.thunderbird.net/v1.1/"
	// MaxAutoconfigBytes caps one autoconfig document.
	MaxAutoconfigBytes = 256 << 10

	HTTPTimeout    = 5 * time.Second
	SRVTimeout     = 5 * time.Second
	GuessTimeout   = 5 * time.Second
	OverallTimeout = 20 * time.Second
	maxRedirects   = 3
)

// Resolver is the slice of net.Resolver we use; tests substitute a map.
type Resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (cname string, addrs []*net.SRV, err error)
}

// Discoverer runs the lookups. Zero fields are filled by New.
type Discoverer struct {
	HTTP      *http.Client
	ISPDBBase string
	// Provider enables the provider's own autoconfig URLs, which receive
	// the full address.
	Provider   bool
	Resolver   Resolver
	VerifyIMAP func(ctx context.Context, cfg api.ServerConfig) error
	VerifySMTP func(ctx context.Context, cfg api.ServerConfig) error
	Log        *slog.Logger
}

// Result is the suggestion. Source is DiscoverNone when Config is nil.
type Result struct {
	Config       *api.AccountConfig
	Source       api.DiscoverSource
	ProviderName string
}

// New returns a Discoverer with the production defaults.
func New(log *slog.Logger) *Discoverer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Discoverer{
		HTTP:       NewHTTPClient(),
		ISPDBBase:  DefaultISPDBBase,
		Provider:   true,
		Resolver:   net.DefaultResolver,
		VerifyIMAP: imap.Verify,
		VerifySMTP: smtp.Verify,
		Log:        log.With("component", "discover"),
	}
}

// NewHTTPClient applies the transport TLS policy, a short timeout, no
// keep-alives and https-only redirects.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: HTTPTimeout,
		Transport: &http.Transport{
			TLSClientConfig:   transport.TLSConfig(""),
			DisableKeepAlives: true,
			Proxy:             http.ProxyFromEnvironment,
		},
		CheckRedirect: CheckRedirect,
	}
}

// CheckRedirect allows at most maxRedirects hops, all https.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return errors.New("redirect to non-https URL refused")
	}
	return nil
}

// Domain returns the lower-cased part after the last '@', or "".
func Domain(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

// endpoint is one suggested server with the source it came from.
type endpoint struct {
	cfg    *api.ServerConfig
	source api.DiscoverSource
}

// Discover never fails for "not found"; it returns DiscoverNone. The only
// error is context cancellation. email must already be syntactically valid.
func (d *Discoverer) Discover(ctx context.Context, email string) (Result, error) {
	none := Result{Source: api.DiscoverNone}
	domain := Domain(email)
	if domain == "" || !isASCII(domain) || !transport.ValidHost(domain) || net.ParseIP(domain) != nil {
		d.Log.Debug("discovery skipped: unusable domain", "domain", domain)
		return none, nil
	}
	ctx, cancel := context.WithTimeout(ctx, OverallTimeout)
	defer cancel()

	// Phase 1: ISPDB and DNS in parallel.
	var (
		wg    sync.WaitGroup
		ispdb autoconfig
		srv   srvResult
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		ispdb = d.fetchAutoconfig(ctx, d.ISPDBBase+url.PathEscape(domain), email)
	}()
	go func() {
		defer wg.Done()
		srv = d.lookupSRV(ctx, domain)
	}()
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return none, err
	}

	var in, out endpoint
	fill := func(imapCfg, smtpCfg *api.ServerConfig, src api.DiscoverSource) {
		if in.cfg == nil && imapCfg != nil {
			in = endpoint{imapCfg, src}
		}
		if out.cfg == nil && smtpCfg != nil {
			out = endpoint{smtpCfg, src}
		}
	}
	providerName := ispdb.providerName
	fill(ispdb.imap, ispdb.smtp, api.DiscoverISPDB)

	// Phase 2: the provider's own document, then DNS, then guesses.
	if (in.cfg == nil || out.cfg == nil) && d.Provider {
		for _, u := range providerURLs(domain, email) {
			ac := d.fetchAutoconfig(ctx, u, email)
			if ac.imap == nil && ac.smtp == nil {
				continue
			}
			if providerName == "" {
				providerName = ac.providerName
			}
			fill(ac.imap, ac.smtp, api.DiscoverAutoconfig)
			break
		}
	}
	fill(srv.imap, srv.smtp, api.DiscoverSRV)
	if in.cfg == nil || out.cfg == nil {
		g := d.guess(ctx, domain, in.cfg == nil, out.cfg == nil)
		fill(g.imap, g.smtp, api.DiscoverGuess)
	}
	if err := ctx.Err(); err != nil && errors.Is(err, context.Canceled) {
		return none, err
	}
	if in.cfg == nil || out.cfg == nil {
		return none, nil
	}

	for _, sc := range []*api.ServerConfig{in.cfg, out.cfg} {
		if sc.Username == "" {
			sc.Username = email
		}
		sc.AuthMethod = api.AuthPassword
	}
	name := providerName
	if name == "" {
		name = domain
	}
	cfg := &api.AccountConfig{Name: name, Email: email, IMAP: *in.cfg, SMTP: *out.cfg}
	return Result{Config: cfg, Source: weakest(in.source, out.source), ProviderName: providerName}, nil
}

var sourceRank = map[api.DiscoverSource]int{
	api.DiscoverISPDB: 0, api.DiscoverAutoconfig: 1, api.DiscoverSRV: 2, api.DiscoverGuess: 3,
}

func weakest(a, b api.DiscoverSource) api.DiscoverSource {
	if sourceRank[b] > sourceRank[a] {
		return b
	}
	return a
}

// providerURLs are the autoconfig locations Thunderbird tries at the
// provider itself. They receive the address.
func providerURLs(domain, email string) []string {
	q := url.QueryEscape(email)
	return []string{
		"https://autoconfig." + domain + "/mail/config-v1.1.xml?emailaddress=" + q,
		"https://" + domain + "/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=" + q,
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
