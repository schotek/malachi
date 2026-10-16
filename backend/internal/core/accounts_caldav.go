// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"

	"github.com/schotek/malachi/backend/internal/caldav"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// CalDAV accounts (docs/calendar.md §3.8, §4.3): validation of the kind,
// account.discoverCalDAV, account.listCalDAVCalendars and the caldav part
// of account.test. The password is Credentials.Password in the keyring
// (auth.KeyPassword) and goes only to the account's server and the
// origins of its principal and home set (calDAVOrigin).

// Limits of the caldav and calendar blocks of an account.
const (
	maxCalDAVURLBytes        = 2048
	maxCalendarRemoteIDBytes = 2048
	// maxCalDAVTransports bounds the per-host transports one client keeps
	// (a discovery tries a few hosts, an account one to three).
	maxCalDAVTransports = 8
	// calDAVOriginRounds is how often listing asks the server for the
	// origins of its principal and home set (one for each).
	calDAVOriginRounds = 2
)

// validCalendarKeepDays are the values of CalendarConfig.KeepDays.
var validCalendarKeepDays = []int{0, 30, 90, 180, 365, 730}

// validateCalDAV checks an account of kind caldav: the caldav block and no
// mail or issue-tracker settings. Every failure is invalidArgument.
func validateCalDAV(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if c.IMAP != nil || c.SMTP != nil || c.OAuth2 != nil || c.Graph != nil || c.Jira != nil {
		return bad("imap, smtp, oauth2, graph and jira settings do not apply to a caldav account")
	}
	if c.CalDAV == nil {
		return bad("caldav settings are required")
	}
	return validateCalDAVConnection(c.CalDAV)
}

// validateCalDAVConnection normalises the connection fields of a caldav
// block in place: the server URL (caldav.NormaliseServerURL), the user
// name, the pin, the principal and home set URLs (each on an origin
// calDAVOrigin trusts), the scheduling and the mail account id (its
// existence is checkCalDAVMailAccount's).
func validateCalDAVConnection(cd *api.CalDAVConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "caldav: "+format, args...)
	}
	server, err := caldav.NormaliseServerURL(cd.ServerURL)
	if err != nil {
		return err
	}
	cd.ServerURL = server
	cd.Username = strings.TrimSpace(cd.Username)
	switch {
	case cd.Username == "":
		return bad("username is required")
	case len(cd.Username) > maxUsernameBytes || !utf8.ValidString(cd.Username) || hasControl(cd.Username):
		return bad("username must be valid UTF-8 without control characters (limit %d bytes)", maxUsernameBytes)
	}
	cd.CertificateSHA256 = strings.TrimSpace(cd.CertificateSHA256)
	if cd.CertificateSHA256 != "" {
		pin, ok := api.NormalizeCertificateSHA256(cd.CertificateSHA256)
		if !ok {
			return bad("certificateSha256 must be a SHA-256 fingerprint of 64 hex digits")
		}
		if !strings.HasPrefix(server, "https://") {
			return bad("certificateSha256 needs an https server URL")
		}
		cd.CertificateSHA256 = pin
	}
	for _, f := range []*string{&cd.PrincipalURL, &cd.HomeSetURL} {
		*f = strings.TrimSpace(*f)
		if *f == "" {
			continue
		}
		if len(*f) > maxCalDAVURLBytes {
			return bad("principalUrl and homeSetUrl are limited to %d bytes", maxCalDAVURLBytes)
		}
		if _, ok := calDAVOrigin(server, *f); !ok {
			return bad("principalUrl and homeSetUrl must be URLs of the server's site")
		}
	}
	switch cd.Scheduling {
	case "", api.CalDAVSchedulingAuto, api.CalDAVSchedulingServer, api.CalDAVSchedulingClient:
	default:
		return bad("scheduling must be auto, server or client")
	}
	cd.MailAccountID = api.AccountID(strings.TrimSpace(string(cd.MailAccountID)))
	if len(cd.MailAccountID) > maxCalendarRemoteIDBytes || hasControl(string(cd.MailAccountID)) {
		return bad("mailAccountId is not an account id")
	}
	return nil
}

// validateCalendarConfig checks the calendar block of a graph or caldav
// account (any other kind has none) and fills a caldav account's: its
// calendars always synchronise.
func validateCalendarConfig(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "calendar: "+format, args...)
	}
	kind := c.Protocol()
	if kind != api.AccountGraph && kind != api.AccountCalDAV {
		if c.Calendar != nil {
			return bad("calendar settings apply to graph and caldav accounts only")
		}
		return nil
	}
	if kind == api.AccountCalDAV {
		if c.Calendar == nil {
			c.Calendar = &api.CalendarConfig{}
		}
		c.Calendar.Sync = true
	}
	cal := c.Calendar
	if cal == nil {
		return nil
	}
	if len(cal.DisabledCalendars) > api.MaxCalendarsPerAccount {
		return bad("disabledCalendars is limited to %d entries", api.MaxCalendarsPerAccount)
	}
	for _, id := range cal.DisabledCalendars {
		if !calendarRemoteID(id) {
			return bad("disabledCalendars entries must be non-empty without control characters (limit %d bytes)", maxCalendarRemoteIDBytes)
		}
	}
	if cal.DefaultCalendar != "" && !calendarRemoteID(cal.DefaultCalendar) {
		return bad("defaultCalendar must be without control characters (limit %d bytes)", maxCalendarRemoteIDBytes)
	}
	if !slices.Contains(validCalendarKeepDays, cal.KeepDays) {
		return bad("keepDays must be 30, 90, 180, 365 or 730")
	}
	switch cal.AfterAnswer {
	case "", api.AfterAnswerKeep, api.AfterAnswerArchive:
	default:
		return bad("afterAnswer must be keep or archive")
	}
	return nil
}

func calendarRemoteID(s string) bool {
	return s != "" && len(s) <= maxCalendarRemoteIDBytes && utf8.ValidString(s) && !hasControl(s)
}

// checkCalDAVMailAccount enforces CalDAVConfig.mailAccountId: empty, or
// an existing account (not selfID) with the compose capability.
func (b *Backend) checkCalDAVMailAccount(ctx context.Context, c api.AccountConfig, selfID string) error {
	if c.Protocol() != api.AccountCalDAV || c.CalDAV == nil || c.CalDAV.MailAccountID == "" {
		return nil
	}
	id := string(c.CalDAV.MailAccountID)
	m, err := b.store.GetAccount(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound) || id == selfID:
		return api.NewError(api.CodeInvalidArgument, "caldav: mailAccountId names no mail account")
	case err != nil:
		return api.NewError(api.CodeStorageError, "%v", err)
	case !can(m.Config, api.CapabilityCompose):
		return api.NewError(api.CodeInvalidArgument, "caldav: mailAccountId must name an account that sends mail")
	}
	return nil
}

// calDAVSecretMoves: the stored password of a caldav account belongs to
// its server and user; a new realm or user name needs a new password (and
// a caldav account is never made from another kind's secret).
func calDAVSecretMoves(old, next api.AccountConfig) bool {
	switch {
	case next.Protocol() == api.AccountCalDAV:
		return old.Protocol() != api.AccountCalDAV || !sameCalDAVLogin(old, next)
	case old.Protocol() == api.AccountCalDAV:
		return usesAuth(next, api.AuthPassword) || next.Protocol() == api.AccountJira
	}
	return false
}

// sameCalDAVLogin: the same server (realm) and user name.
func sameCalDAVLogin(a, b api.AccountConfig) bool {
	return a.CalDAV != nil && b.CalDAV != nil && store.RealmOf(a) == store.RealmOf(b) &&
		a.CalDAV.Username == b.CalDAV.Username
}

// prepareCalDAV completes a caldav configuration before account.add or
// account.update stores it: the principal and home set kept from the
// stored account (existing, nil on add) when its login did not change,
// else, when the configuration lacks the home set or the address, a
// signed-in listing (password) fills them: the first calendar-user
// address, else the user name when it is an address.
func (b *Backend) prepareCalDAV(ctx context.Context, c *api.AccountConfig, existing *store.Account, password string) error {
	if c.Protocol() != api.AccountCalDAV || c.CalDAV == nil {
		return nil
	}
	if err := validateCalDAVConnection(c.CalDAV); err != nil {
		return err
	}
	cd := c.CalDAV
	if existing != nil && existing.Config.CalDAV != nil && sameCalDAVLogin(existing.Config, *c) {
		if cd.PrincipalURL == "" {
			cd.PrincipalURL = existing.Config.CalDAV.PrincipalURL
		}
		if cd.HomeSetURL == "" {
			cd.HomeSetURL = existing.Config.CalDAV.HomeSetURL
		}
	}
	c.Email = strings.TrimSpace(c.Email)
	if (cd.HomeSetURL != "" && c.Email != "") || password == "" {
		if c.Email == "" && validateAddress(api.Address{Address: cd.Username}) == nil {
			c.Email = cd.Username
		}
		return nil
	}
	info, err := b.listCalDAV(ctx, *cd, password)
	if err != nil {
		return err
	}
	if cd.PrincipalURL == "" {
		cd.PrincipalURL = info.PrincipalURL
	}
	if cd.HomeSetURL == "" {
		cd.HomeSetURL = info.HomeSetURL
	}
	if c.Email == "" {
		for _, a := range info.Addresses {
			if validateAddress(api.Address{Address: a}) == nil {
				c.Email = a
				break
			}
		}
	}
	if c.Email == "" && validateAddress(api.Address{Address: cd.Username}) == nil {
		c.Email = cd.Username
	}
	return nil
}

// DiscoverCalDAV finds the CalDAV server a typed URL names, anonymously
// (caldav.Discoverer). The pin applies to the typed URL's origin only: a
// host a redirect names is verified as any other.
func (s *accountService) DiscoverCalDAV(ctx context.Context, p api.AccountDiscoverCalDAVParams) (*api.AccountDiscoverCalDAVResult, error) {
	typed, err := caldav.NormaliseServerURL(p.URL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(typed)
	pin := strings.TrimSpace(p.CertificateSHA256)
	if pin != "" {
		n, ok := api.NormalizeCertificateSHA256(pin)
		if !ok {
			return nil, api.NewError(api.CodeInvalidArgument, "certificateSha256 must be a SHA-256 fingerprint of 64 hex digits")
		}
		if u.Scheme != "https" {
			return nil, api.NewError(api.CodeInvalidArgument, "certificateSha256 needs an https URL")
		}
		pin = n
	}
	d := &caldav.Discoverer{HTTP: s.b.calDAVHTTPClient(u.Host, pin), Resolver: s.b.CalDAVResolver, Log: s.b.log}
	res, err := d.Discover(ctx, p.URL)
	if err != nil {
		s.b.log.Info("caldav discovery", "ok", false, "err", err)
		return nil, err
	}
	s.b.log.Info("caldav discovery", "ok", true, "foundVia", res.FoundVia)
	return &res, nil
}

// ListCalDAVCalendars signs in to the server of a caldav configuration and
// lists its calendars; nothing is stored. Without a password the stored
// one of accountId is used, only when that account is a caldav account of
// the same realm.
func (s *accountService) ListCalDAVCalendars(ctx context.Context, p api.AccountListCalDAVCalendarsParams) (*api.AccountListCalDAVCalendarsResult, error) {
	cfg := p.Config
	if cfg.Protocol() != api.AccountCalDAV || cfg.CalDAV == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "config must be of kind caldav with caldav settings")
	}
	cd := *cfg.CalDAV
	if err := validateCalDAVConnection(&cd); err != nil {
		return nil, err
	}
	cfg.CalDAV = &cd
	password, err := s.calDAVPassword(ctx, p.AccountID, cfg, p.Credentials.Password)
	if err != nil {
		return nil, err
	}
	info, err := s.b.listCalDAV(ctx, cd, password)
	if err != nil {
		return nil, err
	}
	res := info.Result()
	s.b.log.Info("caldav calendars listed", "calendars", len(res.Calendars), "scheduling", res.Scheduling)
	return &res, nil
}

// calDAVPassword is the password given, else the stored one of accountID
// when that account is a caldav account of cfg's realm (a password never
// goes to another server); neither is authRequired.
func (s *accountService) calDAVPassword(ctx context.Context, accountID api.AccountID, cfg api.AccountConfig, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if accountID == "" {
		return "", api.NewError(api.CodeAuthRequired, "no password given and no accountId")
	}
	a, err := s.b.requireAccount(ctx, string(accountID))
	if err != nil {
		return "", err
	}
	if a.Config.Protocol() != api.AccountCalDAV || store.RealmOf(a.Config) != store.RealmOf(cfg) {
		return "", api.NewError(api.CodeInvalidArgument, "the stored password of account %q is for another server", accountID)
	}
	return s.b.PasswordFor(ctx, a.ID)
}

// testCalDAV is account.test for a caldav account: a signed-in listing
// with the password given or stored. A missing password without accountId
// is the endpoint's authRequired; the stored one's own failures fail the
// call, as for a jira account.
func (s *accountService) testCalDAV(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	if p.Credentials.Password == "" && p.AccountID == "" {
		r := endpointResult(nil, 0, api.NewError(api.CodeAuthRequired, "no password given and no accountId"))
		s.b.log.Info("account test", "kind", "caldav", "ok", false, "code", r.Error.Code)
		return &api.AccountTestResult{CalDAV: r}, nil
	}
	password, err := s.calDAVPassword(ctx, p.AccountID, p.Config, p.Credentials.Password)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	info, err := s.b.listCalDAV(ctx, *p.Config.CalDAV, password)
	var caps []string
	if err == nil {
		caps = calDAVTestCapabilities(info)
	}
	r := endpointResult(caps, time.Since(start), err)
	attrs := []any{"kind", "caldav", "ok", r.OK, "latencyMs", r.LatencyMS}
	if r.Error != nil {
		attrs = append(attrs, "code", r.Error.Code)
	}
	s.b.log.Info("account test", attrs...)
	return &api.AccountTestResult{CalDAV: r}, nil
}

// calDAVTestCapabilities are EndpointTestResult.capabilities of a caldav
// endpoint: calendar-access, calendar-auto-schedule and sync-collection
// (when a calendar of the home set offers it).
func calDAVTestCapabilities(info *caldav.AccountInfo) []string {
	var caps []string
	if info.Capabilities.CalendarAccess {
		caps = append(caps, "calendar-access")
	}
	if info.Capabilities.AutoSchedule {
		caps = append(caps, "calendar-auto-schedule")
	}
	if slices.ContainsFunc(info.Calendars, func(c caldav.Calendar) bool { return c.SupportsSync }) {
		caps = append(caps, "sync-collection")
	}
	return caps
}

// listCalDAV signs in with password and lists the account. A principal or
// home set on another origin of the server's site (iCloud's partition
// hosts pNN-caldav.icloud.com) is trusted once the server names it; one
// on another site is serverError and gets no request with the password.
// Errors are *api.Error.
func (b *Backend) listCalDAV(ctx context.Context, cd api.CalDAVConfig, password string) (*caldav.AccountInfo, error) {
	origins := calDAVConfiguredOrigins(cd)
	pw := func(context.Context) (string, error) { return password, nil }
	for round := 0; ; round++ {
		c, err := b.newCalDAVClient(cd, origins, pw)
		if err != nil {
			return nil, err
		}
		info, err := c.ListCalendars(ctx)
		if err == nil {
			return info, nil
		}
		if !errors.Is(err, caldav.ErrOtherOrigin) || round >= calDAVOriginRounds {
			return nil, caldav.ToAPIError(err)
		}
		added := false
		for _, o := range calDAVNamedOrigins(ctx, c, cd.ServerURL) {
			if !slices.Contains(origins, o) {
				origins = append(origins, o)
				added = true
			}
		}
		if !added {
			return nil, caldav.ToAPIError(err)
		}
	}
}

// calDAVNamedOrigins asks the server (a client trusting what is known so
// far) for its current-user-principal and calendar-home-set and returns
// their origins calDAVOrigin trusts. Failures leave them out.
func calDAVNamedOrigins(ctx context.Context, c *caldav.Client, server string) []string {
	var named []string
	rs, err := c.Propfind(ctx, "", 0, caldav.PropCurrentUserPrincipal, caldav.PropCalendarHomeSet)
	var principal string
	if err == nil {
		for _, r := range rs {
			if r.Props.CurrentUserPrincipal != "" && principal == "" {
				principal = r.Props.CurrentUserPrincipal
			}
			named = append(named, r.Props.CalendarHomeSet...)
		}
	}
	if principal != "" {
		named = append(named, principal)
		if rs, err := c.Propfind(ctx, principal, 0, caldav.PropCalendarHomeSet); err == nil {
			for _, r := range rs {
				named = append(named, r.Props.CalendarHomeSet...)
			}
		}
	}
	var out []string
	for _, n := range named {
		if o, ok := calDAVOrigin(server, n); ok && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// calDAVConfiguredOrigins are the trusted origins of the principal and
// home set a configuration names.
func calDAVConfiguredOrigins(cd api.CalDAVConfig) []string {
	var out []string
	for _, u := range []string{cd.PrincipalURL, cd.HomeSetURL} {
		if o, ok := calDAVOrigin(cd.ServerURL, u); ok && u != "" && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// calDAVOrigin is the origin ("https://host[:port]") of raw, an absolute
// URL a CalDAV server named for its principal or home set, when the
// account's password may go there: the server's own origin, or an https
// origin of the same site (registrable domain, publicsuffix) as the
// server's host. An IP literal or a loopback host is trusted only as the
// server itself.
func calDAVOrigin(server, raw string) (string, bool) {
	su, err := url.Parse(server)
	if err != nil || su.Host == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	origin := scheme + "://" + host
	if scheme == strings.ToLower(su.Scheme) && host == strings.ToLower(su.Host) {
		return origin, true
	}
	if scheme != "https" || su.Scheme != "https" || !transport.ValidHost(u.Hostname()) {
		return "", false
	}
	h, sh := strings.ToLower(u.Hostname()), strings.ToLower(su.Hostname())
	if net.ParseIP(h) != nil || net.ParseIP(sh) != nil || transport.IsLoopbackHost(h) || transport.IsLoopbackHost(sh) {
		return "", false
	}
	site, err1 := publicsuffix.EffectiveTLDPlusOne(h)
	ssite, err2 := publicsuffix.EffectiveTLDPlusOne(sh)
	if err1 != nil || err2 != nil || site != ssite {
		return "", false
	}
	return origin, true
}

// newCalDAVClient is a client of the account's server for the origins
// (besides the server's own) with the password callback.
func (b *Backend) newCalDAVClient(cd api.CalDAVConfig, origins []string, password func(context.Context) (string, error)) (*caldav.Client, error) {
	u, err := url.Parse(cd.ServerURL)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "caldav: server URL does not parse")
	}
	return caldav.NewClient(caldav.Options{
		ServerURL: cd.ServerURL,
		Username:  cd.Username,
		Password:  password,
		Origins:   origins,
		HTTP:      b.calDAVHTTPClient(u.Host, cd.CertificateSHA256),
		Log:       b.log,
	})
}

// calDAVHTTPClient carries the requests of a CalDAV client or discovery:
// Backend.CalDAVHTTP when a test set one, else per-host transports with
// the TLS policy where pin applies to pinHost (host[:port]) only.
func (b *Backend) calDAVHTTPClient(pinHost, pin string) *http.Client {
	if b.CalDAVHTTP != nil {
		return b.CalDAVHTTP
	}
	return &http.Client{Transport: newCalDAVTransport(pinHost, pin)}
}

// calDAVTransport is an http.RoundTripper with one transport per host,
// each verifying its own host name; the pinned certificate replaces the
// verification only for pinHost.
type calDAVTransport struct {
	pinHost, pin string

	mu sync.Mutex
	by map[string]*http.Transport
}

func newCalDAVTransport(pinHost, pin string) *calDAVTransport {
	return &calDAVTransport{pinHost: canonicalHostPort(pinHost, "https"), pin: pin, by: map[string]*http.Transport{}}
}

func (t *calDAVTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.transportFor(r.URL).RoundTrip(r)
}

func (t *calDAVTransport) transportFor(u *url.URL) *http.Transport {
	key := u.Scheme + "://" + canonicalHostPort(u.Host, u.Scheme)
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr, ok := t.by[key]; ok {
		return tr
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSClientConfig:       transport.EndpointTLSConfig(api.ServerConfig{Host: strings.ToLower(u.Hostname()), CertificateSHA256: t.pinFor(u)}),
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: caldav.RequestTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   caldav.MaxConcurrent,
		IdleConnTimeout:       90 * time.Second,
	}
	if len(t.by) < maxCalDAVTransports {
		t.by[key] = tr
	}
	return tr
}

// pinFor is the pin of a request URL: only over https to pinHost.
func (t *calDAVTransport) pinFor(u *url.URL) string {
	if t.pin == "" || u.Scheme != "https" || canonicalHostPort(u.Host, u.Scheme) != t.pinHost {
		return ""
	}
	return t.pin
}

// canonicalHostPort lower-cases host[:port] and drops the scheme's
// default port.
func canonicalHostPort(hostPort, scheme string) string {
	h := strings.ToLower(hostPort)
	switch {
	case scheme == "https" && strings.HasSuffix(h, ":443"):
		h = strings.TrimSuffix(h, ":443")
	case scheme == "http" && strings.HasSuffix(h, ":80"):
		h = strings.TrimSuffix(h, ":80")
	}
	return h
}
