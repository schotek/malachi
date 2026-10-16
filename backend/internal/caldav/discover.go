// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Timeouts of discovery (account.discoverCalDAV allows 20 s in all).
const (
	DiscoverRequestTimeout = 8 * time.Second
	DiscoverLookupTimeout  = 5 * time.Second
	WellKnownPath          = "/.well-known/caldav"
)

// Resolver is the DNS of discovery: SRV and TXT, as net.Resolver has them
// (internal/discover's Resolver plus TXT); tests substitute a map.
type Resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (cname string, addrs []*net.SRV, err error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Discoverer finds the CalDAV server a typed URL names. It never sends
// credentials.
type Discoverer struct {
	// HTTP carries the requests (tests: caldavtest); nil = a transport per
	// host with the TLS policy (and Pin).
	HTTP     *http.Client
	Resolver Resolver // nil = net.DefaultResolver
	// Pin is a certificate the user trusted (after a tlsError); it
	// applies to every host tried.
	Pin       string
	Log       *slog.Logger
	Timeout   time.Duration // per request; 0 = DiscoverRequestTimeout
	UserAgent string
}

// Discover runs account.discoverCalDAV: the typed URL normalised (https
// assumed); with no path, /.well-known/caldav (RFC 6764; a redirect is
// followed anonymously, to another origin too, and the result names
// where it led), then DNS SRV _caldavs._tcp with a TXT path, then the URL
// itself; with a path, that URL only. A candidate counts when OPTIONS
// advertises calendar-access in DAV (on a 2xx or a 401 answer), or
// answers 401 with no DAV header at all (sabre/dav authenticates before
// it answers OPTIONS; account.listCalDAVCalendars then verifies CalDAV
// with the password). A 2xx, or a 401 with a DAV header, without
// calendar-access is not CalDAV. Errors:
// invalidArgument, tlsError (the first, with TLSErrorData), serverError
// (something answered, nothing is CalDAV), networkError, serverTimeout.
func (d *Discoverer) Discover(ctx context.Context, raw string) (api.AccountDiscoverCalDAVResult, error) {
	u, err := parseServerURL(raw)
	if err != nil {
		return api.AccountDiscoverCalDAVResult{}, err
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	var fail failures
	try := func(cand *url.URL, via api.CalDAVFoundVia) (api.AccountDiscoverCalDAVResult, bool) {
		res, err := d.probe(ctx, cand)
		if err != nil {
			fail.add(err)
			return api.AccountDiscoverCalDAVResult{}, false
		}
		res.FoundVia = via
		return res, true
	}
	if u.Path == "" || u.Path == "/" {
		if cand, err := d.wellKnown(ctx, u); err != nil {
			fail.add(err)
		} else if cand != nil {
			if res, ok := try(cand, api.CalDAVFoundWellKnown); ok {
				return res, nil
			}
		}
		if ctx.Err() == nil {
			if cand := d.srv(ctx, u.Hostname()); cand != nil {
				if res, ok := try(cand, api.CalDAVFoundSRV); ok {
					return res, nil
				}
			}
		}
		root := *u
		root.Path = "/"
		u = &root
	}
	if ctx.Err() == nil {
		if res, ok := try(u, api.CalDAVFoundURL); ok {
			return res, nil
		}
	}
	return api.AccountDiscoverCalDAVResult{}, fail.err(ctx)
}

// failures keeps the most telling error of discovery.
type failures struct{ tls, notCalDAV, other error }

func (f *failures) add(err error) {
	ae := ToAPIError(err)
	switch {
	case ae.Code == api.CodeTLSError && f.tls == nil:
		f.tls = ae
	case errors.Is(err, errNotCalDAV) && f.notCalDAV == nil:
		f.notCalDAV = ae
	case f.other == nil:
		f.other = ae
	}
}

func (f *failures) err(ctx context.Context) error {
	switch {
	case ctx.Err() != nil:
		return ToAPIError(ctx.Err())
	case f.tls != nil:
		return f.tls
	case f.notCalDAV != nil:
		return f.notCalDAV
	case f.other != nil:
		return f.other
	}
	return api.NewError(api.CodeServerError, "caldav: no CalDAV server found")
}

var errNotCalDAV = errors.New("caldav: the server does not offer CalDAV (no calendar-access)")

// client is an anonymous client for u's origin.
func (d *Discoverer) client(u *url.URL) (*Client, error) {
	o := *u
	o.Path, o.RawPath = "", ""
	t := d.Timeout
	if t <= 0 {
		t = DiscoverRequestTimeout
	}
	pin := d.Pin
	if o.Scheme != "https" {
		pin = ""
	}
	return NewClient(Options{ServerURL: o.String(), HTTP: d.HTTP, CertificateSHA256: pin, Timeout: t, Log: d.Log, UserAgent: d.UserAgent})
}

// wellKnown asks u's /.well-known/caldav and follows its redirects
// (anonymous, at most MaxRedirects, https or loopback http, any origin).
// The candidate is where the chain ends when it ended in a redirect; nil
// when the server has no well-known redirect.
func (d *Discoverer) wellKnown(ctx context.Context, u *url.URL) (*url.URL, error) {
	cur := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: WellKnownPath}
	redirected := false
	for hop := 0; hop <= MaxRedirects; hop++ {
		c, err := d.client(cur)
		if err != nil {
			return nil, nil
		}
		a, err := c.do(ctx, request{
			method: "PROPFIND", url: cur, depth: "0", anonymous: true, noRedirect: true,
			body:        propfindBody([]xml.Name{PropCurrentUserPrincipal}),
			contentType: `application/xml; charset="utf-8"`,
		})
		if err != nil {
			if redirected && !isTLS(err) {
				// The target answered (401, 403, 405...): it is the
				// candidate; OPTIONS decides.
				return cur, nil
			}
			if Status(err) != 0 {
				return nil, nil
			}
			return nil, err
		}
		if !isRedirect(a.status) {
			if redirected {
				return cur, nil
			}
			return nil, nil
		}
		next, err := cur.Parse(a.header.Get("Location"))
		if err != nil || next.User != nil || next.Host == "" {
			return nil, nil
		}
		if next.Scheme != "https" && !(next.Scheme == "http" && transport.IsLoopbackHost(next.Hostname())) {
			return nil, nil
		}
		next.RawQuery, next.Fragment, next.RawFragment = "", "", ""
		if !sameOrigin(next, cur) {
			d.Log.Debug("caldav: .well-known redirects to another server", "host", next.Hostname())
		}
		cur, redirected = next, true
	}
	return nil, nil
}

func isTLS(err error) bool { return ToAPIError(err).Code == api.CodeTLSError }

// srv looks up _caldavs._tcp.<host> and its TXT path.
func (d *Discoverer) srv(ctx context.Context, host string) *url.URL {
	if net.ParseIP(host) != nil || transport.IsLoopbackHost(host) {
		return nil
	}
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	lctx, cancel := context.WithTimeout(ctx, DiscoverLookupTimeout)
	defer cancel()
	_, addrs, err := r.LookupSRV(lctx, "caldavs", "tcp", host)
	if err != nil {
		d.Log.Debug("caldav srv lookup", "err", err)
		return nil
	}
	var target string
	var port uint16
	for _, a := range addrs {
		t := strings.ToLower(strings.TrimSuffix(a.Target, "."))
		if t != "" && transport.ValidHost(t) && a.Port != 0 {
			target, port = t, a.Port
			break
		}
	}
	if target == "" {
		return nil
	}
	p := "/"
	if txts, err := r.LookupTXT(lctx, "_caldavs._tcp."+host); err == nil {
		for _, t := range txts {
			if v, ok := strings.CutPrefix(strings.TrimSpace(t), "path="); ok {
				if cp := cleanPath(v); cp != "" {
					p = cp
				}
				break
			}
		}
	}
	h := target
	if port != 443 {
		h = net.JoinHostPort(target, strconv.Itoa(int(port)))
	}
	return &url.URL{Scheme: "https", Host: h, Path: p}
}

// cleanPath is a TXT path: absolute, no dot segments, no control
// characters, ≤ 2048 bytes; "" otherwise.
func cleanPath(p string) string {
	if !strings.HasPrefix(p, "/") || len(p) > maxHrefBytes {
		return ""
	}
	for _, r := range p {
		if r < 0x21 || r == 0x7f || r == '?' || r == '#' {
			return ""
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return ""
		}
	}
	return p
}

// probe sends OPTIONS to cand anonymously; calendar-access in DAV (on a
// 2xx or a 401), or a 401 with no DAV header at all, makes it the server.
func (d *Discoverer) probe(ctx context.Context, cand *url.URL) (api.AccountDiscoverCalDAVResult, error) {
	c, err := d.client(cand)
	if err != nil {
		return api.AccountDiscoverCalDAVResult{}, err
	}
	target := &url.URL{Scheme: cand.Scheme, Host: cand.Host, Path: cand.Path}
	a, err := c.do(ctx, request{method: http.MethodOptions, url: target, anonymous: true})
	var caps Capabilities
	switch {
	case err == nil:
		caps = parseDAVHeader(a.header, a.status)
	case IsUnauthorized(err):
		// A server may advertise before authentication; c.do dropped the
		// header with the error, so ask once more for it.
		caps, err = c.optionsAnyStatus(ctx, target)
		if err != nil {
			return api.AccountDiscoverCalDAVResult{}, err
		}
	default:
		return api.AccountDiscoverCalDAVResult{}, err
	}
	// sabre/dav (Nextcloud, ownCloud, Baïkal) authenticates before its
	// OPTIONS handler: an anonymous 401 without any DAV header is a
	// candidate, which account.listCalDAVCalendars verifies with the
	// password. A 401 whose DAV header lacks calendar-access, and a 2xx
	// without it, are not CalDAV.
	saysNothing := caps.Status == http.StatusUnauthorized && len(caps.DAV) == 0
	if !caps.CalendarAccess && !saysNothing {
		return api.AccountDiscoverCalDAVResult{}, errNotCalDAV
	}
	s, err := NormaliseServerURL(target.String())
	if err != nil {
		return api.AccountDiscoverCalDAVResult{}, err
	}
	return api.AccountDiscoverCalDAVResult{
		ServerURL:        s,
		NeedsCredentials: true,
		Certificate:      certificateInfo(c.PeerCertificate()),
	}, nil
}

// optionsAnyStatus is OPTIONS returning the DAV header of a 401 too.
func (c *Client) optionsAnyStatus(ctx context.Context, u *url.URL) (Capabilities, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return Capabilities{}, classifyTransport(ctx, ctx.Err())
	}
	defer func() { <-c.sem }()
	a, err := c.send(ctx, request{method: http.MethodOptions, url: u, anonymous: true}, "")
	if err != nil {
		return Capabilities{}, err
	}
	if a.status != http.StatusUnauthorized && (a.status < 200 || a.status > 299) {
		return Capabilities{}, c.statusError(http.MethodOptions, a)
	}
	return parseDAVHeader(a.header, a.status), nil
}

// certificateInfo describes the certificate a discovered server
// presented (the sheet's "Verified (issuer)" row), cleaned like
// transport's TLSErrorData certificate.
func certificateInfo(c *x509.Certificate) *api.CertificateInfo {
	if c == nil || len(c.Raw) == 0 {
		return nil
	}
	sum := sha256.Sum256(c.Raw)
	name := func(cn string, full string) string {
		if s := cleanText(cn, transport.MaxCertTextBytes); s != "" {
			return s
		}
		return cleanText(full, transport.MaxCertTextBytes)
	}
	info := &api.CertificateInfo{
		SHA256:    hex.EncodeToString(sum[:]),
		Subject:   name(c.Subject.CommonName, c.Subject.String()),
		Issuer:    name(c.Issuer.CommonName, c.Issuer.String()),
		NotBefore: c.NotBefore.UTC(),
		NotAfter:  c.NotAfter.UTC(),
		SelfSigned: bytes.Equal(c.RawSubject, c.RawIssuer) &&
			c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil,
	}
	for _, n := range c.DNSNames {
		if len(info.DNSNames) == transport.MaxCertNames {
			break
		}
		if n = cleanText(n, transport.MaxCertTextBytes); n != "" {
			info.DNSNames = append(info.DNSNames, n)
		}
	}
	for _, ip := range c.IPAddresses {
		if len(info.IPAddresses) == transport.MaxCertNames {
			break
		}
		if len(ip) == 4 || len(ip) == 16 {
			info.IPAddresses = append(info.IPAddresses, ip.String())
		}
	}
	return info
}

// --- the account's calendars ------------------------------------------------

// Calendar is a VEVENT collection of the home set.
type Calendar struct {
	Href      string
	Name      string
	Color     string // #rrggbb or ""
	Access    api.CalendarAccess
	IsDefault bool
	CTag      string
	SyncToken string
	// Timezone is the collection's calendar-timezone (a VCALENDAR),
	// unparsed.
	Timezone     string
	Components   []string
	SupportsSync bool
}

// API is the calendar as account.listCalDAVCalendars reports it.
func (c Calendar) API() api.CalDAVCalendar {
	return api.CalDAVCalendar{Href: c.Href, Name: c.Name, Color: c.Color, Access: c.Access, IsDefault: c.IsDefault}
}

// AccountInfo is what ListCalendars found.
type AccountInfo struct {
	PrincipalURL string // absolute
	HomeSetURL   string // absolute
	DisplayName  string
	// Addresses are the mailto: values of calendar-user-address-set
	// (lower-cased, at most 32).
	Addresses []string
	// Scheduling is server when the home set (or the server) advertises
	// calendar-auto-schedule, client otherwise (D8).
	Scheduling   api.CalDAVScheduling
	Capabilities Capabilities
	InboxURL     string // absolute, "" when none
	OutboxURL    string // absolute, "" when none
	// DefaultCalendar is the href of schedule-default-calendar-URL, "".
	DefaultCalendar string
	Calendars       []Calendar // ≤ api.MaxCalendarsPerAccount, VEVENT collections only
	// Skipped counts collections left out: not VEVENT, over the limit.
	Skipped int
}

// Result is the answer of account.listCalDAVCalendars.
func (a *AccountInfo) Result() api.AccountListCalDAVCalendarsResult {
	r := api.AccountListCalDAVCalendarsResult{
		PrincipalURL: a.PrincipalURL, HomeSetURL: a.HomeSetURL, DisplayName: a.DisplayName,
		Addresses: append([]string{}, a.Addresses...), Scheduling: a.Scheduling,
		Calendars: make([]api.CalDAVCalendar, 0, len(a.Calendars)),
	}
	for _, c := range a.Calendars {
		r.Calendars = append(r.Calendars, c.API())
	}
	return r
}

// ListCalendars signs in and reads the principal (current-user-principal
// of the server URL), its calendar-home-set, calendar-user-address-set,
// displayname and scheduling URLs, the DAV header of the home set, the
// default calendar of the inbox, and the calendars of the home set
// (depth 1). A principal or home set on an origin the client does not
// trust is ErrOtherOrigin.
func (c *Client) ListCalendars(ctx context.Context) (*AccountInfo, error) {
	root := c.rootURL()
	ms, err := c.propfind(ctx, root, 0, []xml.Name{PropCurrentUserPrincipal, PropCalendarHomeSet})
	if err != nil {
		return nil, err
	}
	info := &AccountInfo{}
	var principal string
	var homes []string
	if r := findResponse(ms, root.EscapedPath()); r != nil {
		principal = r.Props.CurrentUserPrincipal
		homes = r.Props.CalendarHomeSet
	}
	if principal == "" {
		if len(homes) == 0 {
			return nil, api.NewError(api.CodeServerError, "caldav: the server names no current-user-principal")
		}
		principal = root.String()
	}
	pu, err := c.trustedURL(principal)
	if err != nil {
		return nil, err
	}
	info.PrincipalURL = pu.String()
	ms, err = c.propfind(ctx, pu, 0, []xml.Name{
		PropDisplayName, PropCalendarHomeSet, PropCalendarUserAddresses,
		PropScheduleInboxURL, PropScheduleOutboxURL,
	})
	if err != nil {
		return nil, err
	}
	if r := findResponse(ms, pu.EscapedPath()); r != nil {
		info.DisplayName = r.Props.DisplayName
		if len(r.Props.CalendarHomeSet) > 0 {
			homes = r.Props.CalendarHomeSet
		}
		info.Addresses = mailtoAddresses(r.Props.CalendarUserAddresses)
		info.InboxURL = r.Props.ScheduleInboxURL
		info.OutboxURL = r.Props.ScheduleOutboxURL
	}
	if len(homes) == 0 {
		return nil, api.NewError(api.CodeServerError, "caldav: the principal names no calendar-home-set")
	}
	hu, err := c.trustedURL(homes[0])
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(hu.Path, "/") {
		hu.Path += "/"
	}
	info.HomeSetURL = hu.String()

	caps, err := c.Options(ctx, hu.String())
	if err != nil {
		if IsUnauthorized(err) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		if caps, err = c.Options(ctx, ""); err != nil && (IsUnauthorized(err) || errors.Is(err, context.Canceled)) {
			return nil, err
		}
	}
	info.Capabilities = caps
	info.Scheduling = api.CalDAVSchedulingClient
	if caps.AutoSchedule {
		info.Scheduling = api.CalDAVSchedulingServer
	}

	if info.InboxURL != "" {
		if iu, err := c.trustedURL(info.InboxURL); err == nil {
			if ms, err := c.propfind(ctx, iu, 0, []xml.Name{PropScheduleDefault}); err == nil {
				if r := findResponse(ms, iu.EscapedPath()); r != nil && r.Props.ScheduleDefaultCalendarURL != "" {
					if du, err := c.trustedURL(r.Props.ScheduleDefaultCalendarURL); err == nil {
						info.DefaultCalendar = du.EscapedPath()
					}
				}
			} else if IsUnauthorized(err) {
				return nil, err
			}
		}
	}

	ms, err = c.propfind(ctx, hu, 1, []xml.Name{
		PropResourceType, PropDisplayName, PropCalendarColor, PropSupportedComponents,
		PropCurrentUserPrivileges, PropGetCTag, PropSyncToken, PropCalendarTimezone,
		PropSupportedReportSet,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range ms.Responses {
		if samePath(r.Href, hu.EscapedPath()) || !r.Props.Is(TypeCalendar) {
			continue
		}
		if !r.Props.HasComponent("VEVENT") || len(info.Calendars) >= api.MaxCalendarsPerAccount {
			info.Skipped++
			continue
		}
		cal := Calendar{
			Href: r.Href, Name: r.Props.DisplayName, Color: r.Props.Color,
			Access: api.CalendarAccessRead, CTag: r.Props.CTag, SyncToken: r.Props.SyncToken,
			Timezone: r.Props.Timezone, Components: r.Props.Components,
			SupportsSync: r.Props.SupportsReport(ReportSyncCollection),
		}
		if r.Props.CanWrite() {
			cal.Access = api.CalendarAccessWrite
		}
		if cal.Name == "" {
			cal.Name = hrefName(r.Href)
		}
		cal.IsDefault = info.DefaultCalendar != "" && samePath(info.DefaultCalendar, r.Href)
		info.Calendars = append(info.Calendars, cal)
	}
	// Discovery may have taken a 401 without DAV header for a candidate:
	// a home without calendars whose server does not advertise
	// calendar-access to the signed-in user is not CalDAV.
	if len(info.Calendars) == 0 && info.Skipped == 0 && !caps.CalendarAccess {
		return nil, errNotCalDAV
	}
	return info, nil
}

// trustedURL resolves a URL the server named and refuses one on an
// origin the client does not trust with the password.
func (c *Client) trustedURL(raw string) (*url.URL, error) {
	u, ok := resolveURL(c.base, raw)
	if !ok {
		return nil, malformed("the server named an unusable URL")
	}
	if !c.trusted(u) {
		return nil, ErrOtherOrigin
	}
	return u, nil
}

// mailtoAddresses are the mailto: values, lower-cased, deduplicated.
func mailtoAddresses(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		v, ok := cutPrefixFold(s, "mailto:")
		if !ok {
			continue
		}
		if dec, err := url.PathUnescape(v); err == nil {
			v = dec
		}
		v = strings.ToLower(strings.TrimSpace(v))
		at := strings.LastIndexByte(v, '@')
		if at <= 0 || at == len(v)-1 || len(v) > 254 || strings.ContainsAny(v, " <>,;\"") || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= maxAddrEntries {
			break
		}
	}
	return out
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// hrefName is the last segment of an href, for a calendar without a
// displayname.
func hrefName(href string) string {
	seg := path.Base(strings.TrimSuffix(href, "/"))
	if s, err := url.PathUnescape(seg); err == nil {
		seg = s
	}
	return cleanText(seg, maxNameBytes)
}
