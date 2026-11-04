// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package discover suggests account settings for an e-mail address, in
// order of trust: an account already signed in through GNOME Online
// Accounts (a Microsoft Graph account, nothing leaves the machine), the
// Mozilla ISPDB (only the domain is sent), the provider's own autoconfig
// document (the address is sent to the provider itself), RFC 6186 DNS SRV
// records, a known provider recognised by DNS MX or autoconfig hosts
// (Microsoft 365 and Google, which sign in with OAuth2: through GNOME
// Online Accounts where it runs, else through the backend's own flow,
// with an app password as Google's last resort), and finally common host
// names verified by opening a TLS connection without authenticating.
// Nothing is stored; the caller validates the result like an account.add
// request.
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
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// MicrosoftMXSuffix is where every Microsoft 365 hosted domain points its
// MX record.
const MicrosoftMXSuffix = ".mail.protection.outlook.com"

// MicrosoftProviderName is the display name of the Microsoft 365 hint.
const MicrosoftProviderName = "Microsoft 365"

// GoogleMXSuffix is where Gmail and Google Workspace domains point their
// MX records (aspmx.l.google.com, gmail-smtp-in.l.google.com, …).
const GoogleMXSuffix = ".google.com"

// GoogleProviderName is the display name of the Google hint.
const GoogleProviderName = "Google"

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
	// GOA finds the address among the accounts signed in through GNOME
	// Online Accounts and answers with the complete account it would be
	// added as and the provider's display name; nil = no such lookup.
	GOA func(ctx context.Context, email string) (cfg *api.AccountConfig, providerName string, ok bool)
	// GOAAvailable reports whether GNOME Online Accounts runs on this
	// desktop, so that a provider address not signed in there can be
	// pointed at it; nil = it does not, and such an address is answered
	// with the backend's own sign-in.
	GOAAvailable func(ctx context.Context) bool
	Log          *slog.Logger
}

// Result is the suggestion. Source is DiscoverNone when Config is nil.
// Alternatives are further ways to add the address, in order of
// preference (only for a provider answer).
type Result struct {
	Config       *api.AccountConfig
	Source       api.DiscoverSource
	ProviderName string
	Alternatives []api.AccountConfig
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

	// Phase 0: an account the desktop is already signed in to.
	if d.GOA != nil {
		if cfg, name, ok := d.GOA(ctx, email); ok && cfg != nil {
			return Result{Config: cfg, Source: api.DiscoverGOA, ProviderName: name}, nil
		}
		if err := ctx.Err(); err != nil {
			return none, err
		}
	}

	// Phase 1: ISPDB and DNS in parallel.
	var (
		wg          sync.WaitGroup
		ispdb       autoconfig
		srv         srvResult
		microsoftMX bool
		googleMX    bool
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		ispdb = d.fetchAutoconfig(ctx, d.ISPDBBase+url.PathEscape(domain), email)
	}()
	go func() {
		defer wg.Done()
		srv = d.lookupSRV(ctx, domain)
	}()
	go func() {
		defer wg.Done()
		microsoftMX = d.hostedMX(ctx, domain, MicrosoftMXSuffix)
	}()
	go func() {
		defer wg.Done()
		googleMX = d.hostedMX(ctx, domain, GoogleMXSuffix)
	}()
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return none, err
	}

	// A Google mailbox signs in with OAuth2, so the answer is the
	// provider's even when the ISPDB has a password entry (which works
	// with an app password at best, and that is the last alternative).
	if googleDomain(domain) || ispdb.google || googleMX {
		d.Log.Debug("google domain", "domain", domain, "ispdb", ispdb.google, "mx", googleMX)
		return d.providerResult(ctx, email, domain, true), nil
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

	// A Microsoft 365 mailbox without a password entry in the ISPDB: the
	// answer is a Graph account that still needs its sign-in.
	if (in.cfg == nil || out.cfg == nil) && (ispdb.microsoft || microsoftMX) {
		d.Log.Debug("microsoft 365 domain", "domain", domain, "ispdb", ispdb.microsoft, "mx", microsoftMX)
		return d.providerResult(ctx, email, domain, false), nil
	}

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
	cfg := &api.AccountConfig{Name: name, Email: email, Kind: api.AccountIMAP, IMAP: in.cfg, SMTP: out.cfg}
	return Result{Config: cfg, Source: weakest(in.source, out.source), ProviderName: providerName}, nil
}

// providerResult is the answer for an address of a provider that signs in
// with OAuth2 (Google, or Microsoft 365 through Graph) and that GNOME
// Online Accounts is not signed in to. Where GOA runs, Config is its hint
// (the account without a GOA id: add it there first) and the backend's
// own sign-in the first alternative; elsewhere the own sign-in is Config.
// Google adds IMAP/SMTP with an app password as the last alternative. The
// own sign-in is offered whether or not an OAuth client is configured
// (account.oauthStart reports oauthClientMissing).
func (d *Discoverer) providerResult(ctx context.Context, email, domain string, google bool) Result {
	res := Result{Source: api.DiscoverProvider, ProviderName: MicrosoftProviderName}
	hint, own := microsoftConfig(email, domain), microsoftDaemonConfig(email, domain)
	if google {
		res.ProviderName = GoogleProviderName
		hint, own = googleConfig(email, domain), googleDaemonConfig(email, domain)
	}
	if d.GOAAvailable != nil && d.GOAAvailable(ctx) {
		res.Config = hint
		res.Alternatives = append(res.Alternatives, *own)
	} else {
		res.Config = own
	}
	if google {
		res.Alternatives = append(res.Alternatives, *googlePasswordConfig(email, domain))
	}
	return res
}

// microsoftConfig is the Graph account for a Microsoft 365 address
// without its GOA account id: the hint that the address must be signed
// in through GNOME Online Accounts first.
func microsoftConfig(email, domain string) *api.AccountConfig {
	return &api.AccountConfig{
		Name: domain, Email: email, Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA},
	}
}

// microsoftDaemonConfig is the Graph account signed in through the
// backend's own flow; complete, it passes account.add validation.
func microsoftDaemonConfig(email, domain string) *api.AccountConfig {
	return &api.AccountConfig{
		Name: domain, Email: email, Kind: api.AccountGraph,
		Graph:  &api.GraphConfig{Source: api.GraphSourceDaemon},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderOffice365},
	}
}

// googleConfig is the IMAP account of a Google address without its GOA
// account id: Gmail's servers with oauth2 on both, the same hint as
// microsoftConfig. With a sign-in, GOA names the servers itself.
func googleConfig(email, domain string) *api.AccountConfig {
	cfg := gmailServers(email, domain, api.AuthOAuth2)
	cfg.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle}
	return cfg
}

// googleDaemonConfig is Gmail through the backend's own sign-in.
func googleDaemonConfig(email, domain string) *api.AccountConfig {
	cfg := gmailServers(email, domain, api.AuthOAuth2)
	cfg.OAuth2 = &api.OAuth2Config{Source: api.OAuth2SourceDaemon, Provider: api.OAuth2ProviderGoogle}
	return cfg
}

// googlePasswordConfig is Gmail with an app password (accounts with
// 2-Step Verification): the same servers, password authentication.
func googlePasswordConfig(email, domain string) *api.AccountConfig {
	return gmailServers(email, domain, api.AuthPassword)
}

// gmailServers is the IMAP account on Gmail's servers, implicit TLS on
// both, the address as the user name.
func gmailServers(email, domain string, auth api.AuthMethod) *api.AccountConfig {
	return &api.AccountConfig{
		Name: domain, Email: email, Kind: api.AccountIMAP,
		IMAP: &api.ServerConfig{Host: "imap.gmail.com", Port: 993, Security: api.SecurityTLS, Username: email, AuthMethod: auth},
		SMTP: &api.ServerConfig{Host: "smtp.gmail.com", Port: 465, Security: api.SecurityTLS, Username: email, AuthMethod: auth},
	}
}

// googleDomain reports Google's own consumer mail domains.
func googleDomain(domain string) bool {
	switch strings.ToLower(domain) {
	case "gmail.com", "googlemail.com":
		return true
	}
	return false
}

// hostedMX reports whether the domain's mail is hosted by the provider
// whose MX hosts end in suffix. The domain goes to the resolver.
func (d *Discoverer) hostedMX(ctx context.Context, domain, suffix string) bool {
	ctx, cancel := context.WithTimeout(ctx, SRVTimeout)
	defer cancel()
	records, err := d.Resolver.LookupMX(ctx, domain)
	if err != nil {
		d.Log.Debug("mx lookup", "domain", domain, "err", err)
		return false
	}
	for _, mx := range records {
		host := strings.ToLower(strings.TrimSuffix(mx.Host, "."))
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

var sourceRank = map[api.DiscoverSource]int{
	api.DiscoverGOA: 0, api.DiscoverISPDB: 1, api.DiscoverAutoconfig: 2, api.DiscoverSRV: 3, api.DiscoverProvider: 4, api.DiscoverGuess: 5,
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
