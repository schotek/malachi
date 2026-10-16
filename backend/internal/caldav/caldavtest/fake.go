// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package caldavtest is an in-memory CalDAV server for tests: those of
// internal/caldav (client, discovery, provider) and of packages that drive
// a CalDAV account end to end (calsync, core). It is an http.RoundTripper
// (HTTPClient) that serves a few logical hosts and refuses every other
// one, so no test touches the network; Handler serves the same state over
// a real (loopback, TLS) httptest server for certificate tests.
//
// A Server speaks the subset internal/caldav uses: OPTIONS (DAV header),
// PROPFIND depth 0/1, REPORT calendar-query (time-range), calendar-multiget
// and sync-collection (RFC 6578, with truncation), GET, PUT with
// If-Match / If-None-Match and DELETE with If-Match (412 on a mismatch),
// /.well-known/caldav and DNS SRV/TXT through Resolver. Collections keep
// ETags, a ctag and sync tokens; every change through the builders or a
// client bumps them.
//
// Profiles (New) mimic real servers:
//
//   - Nextcloud: cloud.example.test, /remote.php/dav, .well-known on the
//     same host, scheduling on, sync-collection, a VTODO-only list and a
//     read-only shared calendar.
//   - ICloud: the user types icloud.test, whose .well-known redirects to
//     another host (caldav.icloud.test); no ETag on PUT; colours with
//     alpha (#RRGGBBAA); anonymous OPTIONS is 401 with the DAV header.
//   - Fastmail: fastmail.test has no .well-known; SRV _caldavs._tcp names
//     caldav.fastmail.test with TXT path=/dav/.
//   - Radicale: radicale.example.test, found by the URL itself; no
//     sync-collection, no server scheduling, no inbox.
//   - Hostile: hostile.test, laid out like Nextcloud under /dav, whose
//     .well-known redirects to EvilHost; hostile answers are queued with
//     FailNext (the helpers HugeMultistatus, DeepMultistatus,
//     Garbage207, WrongContentType, RedirectTo, FixtureAnswer) and the
//     knobs Delay and UnauthorizedAfter.
//
// Builders and inspection (all hold the lock): AddCalendar, RemoveCalendar, PutObject,
// RemoveObject, Object, Objects, CalendarState, ExpireSyncTokens, Set
// (knobs: Scheduling, SyncCollection, Multiget, TimeRange, ETagOnPut,
// DAVOnUnauthorized, OptionsNeedAuth, DAV, Delay, UnauthorizedAfter,
// SyncLimit, Password),
// FailNext, Requests, ResetRequests, AuthLeaks, EvilRequests;
// addresses: ServerURL, TypedURL, PrincipalURL, HomeSetURL,
// CalendarHref, DefaultCalendarHref; Resolver for discovery; Fixture
// reads backend/testdata/caldav. Test data is fictional (the user Jana
// Dvořáková, jana@example.test).
package caldavtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Profile picks the layout and behaviour of a server.
type Profile string

const (
	Nextcloud Profile = "nextcloud"
	ICloud    Profile = "icloud"
	Fastmail  Profile = "fastmail"
	Radicale  Profile = "radicale"
	Hostile   Profile = "hostile"
)

// Credentials and hosts.
const (
	Username = "jana"
	Password = "pw-Secret-4242"

	NextcloudHost   = "cloud.example.test"
	ICloudTypedHost = "icloud.test"
	ICloudHost      = "caldav.icloud.test"
	FastmailDomain  = "fastmail.test"
	FastmailHost    = "caldav.fastmail.test"
	RadicaleHost    = "radicale.example.test"
	HostileHost     = "hostile.test"
	// EvilHost is another origin a hostile server points at; it counts
	// every request and every Authorization header it receives
	// (EvilRequests, AuthLeaks), which a client must never send it.
	EvilHost = "evil.test"
)

// Calendar is a collection of the fake.
type Calendar struct {
	Href       string
	Name       string
	Color      string   // as the server sends it ("" = none)
	Components []string // nil = VEVENT
	ReadOnly   bool
	Timezone   string

	index    int
	ctag     int
	seq      int
	minToken int
	objects  map[string]*object // href → object
	removed  map[string]int     // href → seq of the removal
}

type object struct {
	data []byte
	etag string
	seq  int
}

// CalendarOptions configures AddCalendar.
type CalendarOptions struct {
	Name       string
	Color      string
	Components []string
	ReadOnly   bool
	Timezone   string
}

// Request is one request the fake received.
type Request struct {
	Method string
	Host   string
	Path   string
	Depth  string
	Header http.Header
	Body   []byte // first 64 KiB
	// HasAuthorization: an Authorization header was sent; Authorized: it
	// carried the right credentials.
	HasAuthorization bool
	Authorized       bool
	// MultigetHrefs is the number of hrefs of a calendar-multiget.
	MultigetHrefs int
}

// Failure is an answer queued with FailNext: it replaces the fake's own
// answer for the next request that matches Method and Path ("" matches
// anything); Persist keeps it for every match.
type Failure struct {
	Method  string
	Path    string
	Host    string
	Status  int
	Header  http.Header
	Body    []byte
	Persist bool
	// Size > 0 makes the body Body repeated in whole copies until at
	// least Size bytes (a huge answer without holding it in memory).
	Size int64
	// Prefix and Suffix wrap a repeated body.
	Prefix, Suffix []byte
}

// Server is the fake. Change knobs only through Set.
type Server struct {
	t       testing.TB
	profile Profile

	mu sync.Mutex
	// Knobs.
	Scheduling        bool
	SyncCollection    bool
	Multiget          bool
	TimeRange         bool
	ETagOnPut         bool
	DAVOnUnauthorized bool
	// OptionsNeedAuth: an anonymous OPTIONS is 401 like any other
	// request (sabre/dav: no DAV header unless DAVOnUnauthorized).
	OptionsNeedAuth bool
	// DAV replaces the DAV header of OPTIONS ("" = the profile's).
	DAV string
	// Delay holds every answer (honouring the request's context).
	Delay time.Duration
	// UnauthorizedAfter > 0: after that many authorised requests every
	// further one is 401 (a password changed on the server).
	UnauthorizedAfter int
	// SyncLimit > 0 truncates sync-collection answers to that many
	// changes (507 for the collection, RFC 6578 §3.6).
	SyncLimit int
	// Password is the one the server accepts.
	Password string

	scheme      string
	host        string // the server's host
	typedHost   string
	root        string // context path, no trailing slash ("" = /)
	principal   string
	home        string
	inbox       string
	outbox      string
	addresses   []string
	displayName string
	wellKnown   map[string]string // host → Location of its .well-known (absolute)
	extraHosts  map[string]bool   // hosts that answer (404 except .well-known)
	calendars   []*Calendar
	defaultCal  string

	requests  []Request
	failures  []Failure
	authCount int
	leaks     int
	evil      int
	resolver  *Resolver
}

// New builds a server of profile p with its default calendars.
func New(t testing.TB, p Profile) *Server {
	s := &Server{
		t: t, profile: p, scheme: "https", Password: Password,
		Multiget: true, TimeRange: true, ETagOnPut: true, SyncCollection: true, Scheduling: true,
		wellKnown: map[string]string{}, extraHosts: map[string]bool{},
		displayName: "Jana Dvořáková",
		resolver:    &Resolver{SRV: map[string][]*net.SRV{}, TXT: map[string][]string{}},
	}
	switch p {
	case Nextcloud, Hostile:
		s.host, s.typedHost, s.root = NextcloudHost, NextcloudHost, "/remote.php/dav"
		if p == Hostile {
			s.host, s.typedHost, s.root = HostileHost, HostileHost, "/dav"
		}
		s.principal = s.root + "/principals/users/jana/"
		s.home = s.root + "/calendars/jana/"
		s.inbox = s.home + "inbox/"
		s.outbox = s.home + "outbox/"
		s.addresses = []string{"mailto:jana@example.test", s.principal}
		s.wellKnown[s.host] = "https://" + s.host + s.root + "/"
		if p == Hostile {
			s.wellKnown[s.host] = "https://" + EvilHost + "/dav/"
		}
		s.defaultCal = s.addCalendar("personal", CalendarOptions{Name: "Personal", Color: "#0082c9", Components: []string{"VEVENT", "VTODO"}})
		s.addCalendar("tasks", CalendarOptions{Name: "Tasks", Components: []string{"VTODO"}})
		s.addCalendar("team-shared_by_petr", CalendarOptions{Name: "Team", Color: "#E9322D", ReadOnly: true})
	case ICloud:
		s.host, s.typedHost, s.root = ICloudHost, ICloudTypedHost, ""
		s.principal = "/123456789/principal/"
		s.home = "/123456789/calendars/"
		s.inbox = s.home + "inbox/"
		s.outbox = s.home + "outbox/"
		s.addresses = []string{"mailto:jana@icloud.test", "/123456789/principal/"}
		s.wellKnown[ICloudTypedHost] = "https://" + ICloudHost + "/"
		s.extraHosts[ICloudTypedHost] = true
		s.ETagOnPut = false
		s.DAVOnUnauthorized = true
		s.defaultCal = s.addCalendar("home", CalendarOptions{Name: "Home", Color: "#FF2968FF"})
		s.addCalendar("work", CalendarOptions{Name: "Work", Color: "#1BADF8FF"})
		s.addCalendar("reminders", CalendarOptions{Name: "Reminders", Components: []string{"VTODO"}})
	case Fastmail:
		s.host, s.typedHost, s.root = FastmailHost, FastmailDomain, "/dav"
		s.principal = "/dav/principals/user/jana@fastmail.test/"
		s.home = "/dav/calendars/user/jana@fastmail.test/"
		s.inbox = s.home + "Inbox/"
		s.outbox = s.home + "Outbox/"
		s.addresses = []string{"mailto:jana@fastmail.test"}
		s.extraHosts[FastmailDomain] = true
		s.resolver.SRV["_caldavs._tcp."+FastmailDomain] = []*net.SRV{{Target: FastmailHost + ".", Port: 443, Priority: 0, Weight: 1}}
		s.resolver.TXT["_caldavs._tcp."+FastmailDomain] = []string{"path=/dav/"}
		s.defaultCal = s.addCalendar("8f6a5e2c-1d1b-4bb2-9c55-2f0a7d1e6b10", CalendarOptions{Name: "Calendar", Color: "#3a429c"})
	case Radicale:
		s.host, s.typedHost, s.root = RadicaleHost, RadicaleHost, ""
		s.principal = "/jana/"
		s.home = "/jana/"
		s.addresses = []string{}
		s.SyncCollection = false
		s.Scheduling = false
		s.addCalendar("calendar.ics", CalendarOptions{Name: "Calendar"})
	default:
		t.Fatalf("caldavtest: unknown profile %q", p)
	}
	return s
}

func (s *Server) addCalendar(segment string, o CalendarOptions) string {
	c := &Calendar{
		Href: s.home + segment + "/", Name: o.Name, Color: o.Color, Components: o.Components,
		ReadOnly: o.ReadOnly, Timezone: o.Timezone, index: len(s.calendars) + 1,
		objects: map[string]*object{}, removed: map[string]int{}, seq: 1, ctag: 1,
	}
	s.calendars = append(s.calendars, c)
	return c.Href
}

// AddCalendar adds a collection named segment under the home set and
// returns its href.
func (s *Server) AddCalendar(segment string, o CalendarOptions) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addCalendar(segment, o)
}

// RemoveCalendar drops the collection href (an empty home set).
func (s *Server) RemoveCalendar(href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calendars = slices.DeleteFunc(s.calendars, func(c *Calendar) bool { return c.Href == href })
}

// Profile is the server's profile.
func (s *Server) Profile() Profile { return s.profile }

// ServerURL is the URL discovery should find (CalDAVConfig.ServerURL).
func (s *Server) ServerURL() string { return s.scheme + "://" + s.host + s.root }

// TypedURL is what a user types into the Add CalDAV sheet.
func (s *Server) TypedURL() string { return s.typedHost }

// PrincipalURL and HomeSetURL are absolute URLs.
func (s *Server) PrincipalURL() string { return s.scheme + "://" + s.host + s.principal }
func (s *Server) HomeSetURL() string   { return s.scheme + "://" + s.host + s.home }

// CalendarHref is the href of the calendar whose last segment is segment.
func (s *Server) CalendarHref(segment string) string { return s.home + segment + "/" }

// DefaultCalendarHref is the server's schedule-default-calendar-URL ("" =
// none).
func (s *Server) DefaultCalendarHref() string { return s.defaultCal }

// Host is the server's host.
func (s *Server) Host() string { return s.host }

// Set changes knobs under the lock.
func (s *Server) Set(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// FailNext queues a hostile or failing answer.
func (s *Server) FailNext(f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, f)
}

// Requests are the requests received so far (to any host).
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// ResetRequests forgets the requests received so far.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

// AuthLeaks counts Authorization headers sent to a host other than the
// server's own (EvilHost, the typed host of iCloud, …).
func (s *Server) AuthLeaks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaks
}

// EvilRequests counts requests to EvilHost.
func (s *Server) EvilRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evil
}

// Resolver is the DNS of the profile for caldav.Discoverer.
func (s *Server) Resolver() *Resolver { return s.resolver }

// HTTPClient is a client whose transport is the fake.
func (s *Server) HTTPClient() *http.Client { return &http.Client{Transport: s} }

// Fixture reads backend/testdata/caldav/<name>.
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "caldav", name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("caldavtest: fixture %s: %v", name, err)
	}
	return data
}

// --- objects ----------------------------------------------------------------

func (s *Server) calendar(href string) *Calendar {
	for _, c := range s.calendars {
		if c.Href == href || strings.TrimSuffix(c.Href, "/") == strings.TrimSuffix(href, "/") {
			return c
		}
	}
	return nil
}

// calendarOf is the calendar holding the object href.
func (s *Server) calendarOf(href string) *Calendar {
	i := strings.LastIndex(strings.TrimSuffix(href, "/"), "/")
	if i < 0 {
		return nil
	}
	return s.calendar(href[:i+1])
}

func etagOf(data []byte, seq int) string {
	sum := sha256.Sum256(append(fmt.Appendf(nil, "%d:", seq), data...))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func (c *Calendar) put(href string, data []byte) string {
	c.seq++
	c.ctag++
	o := &object{data: append([]byte(nil), data...), seq: c.seq}
	o.etag = etagOf(data, c.seq)
	c.objects[href] = o
	delete(c.removed, href)
	return o.etag
}

func (c *Calendar) remove(href string) bool {
	if _, ok := c.objects[href]; !ok {
		return false
	}
	c.seq++
	c.ctag++
	delete(c.objects, href)
	c.removed[href] = c.seq
	return true
}

// PutObject stores data as <calendar><name> (a change by another client)
// and returns the object's href and ETag.
func (s *Server) PutObject(calendar, name string, data []byte) (href, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calendar(calendar)
	if c == nil {
		s.t.Fatalf("caldavtest: no calendar %s", calendar)
	}
	href = c.Href + name
	return href, c.put(href, data)
}

// RemoveObject deletes the object href (another client); false when it
// does not exist.
func (s *Server) RemoveObject(href string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.calendarOf(href); c != nil {
		return c.remove(href)
	}
	return false
}

// Object is the stored object href.
func (s *Server) Object(href string) (data []byte, etag string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.calendarOf(href); c != nil {
		if o := c.objects[href]; o != nil {
			return append([]byte(nil), o.data...), o.etag, true
		}
	}
	return nil, "", false
}

// Objects are the hrefs of a calendar's objects, sorted.
func (s *Server) Objects(calendar string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calendar(calendar)
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.objects))
	for h := range c.objects {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// CalendarState is a calendar's ctag and current sync token.
func (s *Server) CalendarState(calendar string) (ctag, syncToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.calendar(calendar)
	if c == nil {
		return "", ""
	}
	return c.ctagValue(), c.token(c.seq)
}

// ExpireSyncTokens makes every token handed out so far invalid (403
// valid-sync-token).
func (s *Server) ExpireSyncTokens(calendar string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.calendar(calendar); c != nil {
		c.seq++
		c.minToken = c.seq
	}
}

func (c *Calendar) ctagValue() string { return fmt.Sprintf("ctag-%d", c.ctag) }

func (c *Calendar) token(seq int) string {
	return fmt.Sprintf("http://sabre.io/ns/sync/%d-%d", c.index, seq)
}

// parseToken is the seq of a token of c; ok false for a foreign or
// expired one.
func (c *Calendar) parseToken(tok string) (int, bool) {
	var idx, seq int
	if _, err := fmt.Sscanf(tok, "http://sabre.io/ns/sync/%d-%d", &idx, &seq); err != nil {
		return 0, false
	}
	if idx != c.index || seq < c.minToken || seq > c.seq || c.token(seq) != tok {
		return 0, false
	}
	return seq, true
}

// --- transport --------------------------------------------------------------

// RoundTrip serves req from memory. Hosts the profile does not know fail
// like a DNS lookup.
func (s *Server) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	if !s.knownHost(host) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	w := newRecorder()
	s.serve(w, req, host)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return w.response(req), nil
}

// Handler serves the same state over a real server; every request is for
// the server's own host, whatever its Host header.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := newRecorder()
		s.serve(rw, r, s.host)
		for k, v := range rw.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rw.status)
		_, _ = io.Copy(w, rw.body())
	})
}

func (s *Server) knownHost(h string) bool {
	return h == s.host || h == EvilHost || s.extraHosts[h]
}

// serve answers one request.
func (s *Server) serve(w *recorder, req *http.Request, host string) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(req.Body, 4<<20))
		req.Body.Close()
	}
	if d := s.delay(); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-req.Context().Done():
			t.Stop()
			w.status = http.StatusServiceUnavailable
			return
		case <-t.C:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	authz := req.Header.Get("Authorization")
	rec := Request{
		Method: req.Method, Host: host, Path: req.URL.EscapedPath(), Depth: req.Header.Get("Depth"),
		Header: req.Header.Clone(), HasAuthorization: authz != "",
	}
	if len(body) > 64<<10 {
		rec.Body = append([]byte(nil), body[:64<<10]...)
	} else {
		rec.Body = append([]byte(nil), body...)
	}
	rec.Authorized = authz == "Basic "+base64.StdEncoding.EncodeToString([]byte(Username+":"+s.Password))
	if req.Method == "REPORT" {
		rec.MultigetHrefs = bytes.Count(body, []byte("<d:href>"))
	}
	if host != s.host && authz != "" {
		s.leaks++
	}
	if host == EvilHost {
		s.evil++
	}
	s.requests = append(s.requests, rec)

	if f, ok := s.takeFailure(req.Method, rec.Path, host); ok {
		w.fail(f)
		return
	}
	if req.URL.Path == "/.well-known/caldav" {
		if loc, ok := s.wellKnown[host]; ok {
			w.header.Set("Location", loc)
			w.status = http.StatusMovedPermanently
			return
		}
		w.status = http.StatusNotFound
		return
	}
	switch host {
	case EvilHost:
		if req.Method == http.MethodOptions {
			w.header.Set("DAV", "1, 2, 3, calendar-access")
			w.status = http.StatusOK
			return
		}
		w.unauthorized()
		return
	case s.host:
	default:
		// A host of the profile that is only a web site.
		if req.Method == http.MethodOptions {
			w.header.Set("DAV", "1, 2")
			w.status = http.StatusOK
			return
		}
		w.status = http.StatusNotFound
		return
	}

	if req.Method == http.MethodOptions {
		s.options(w, rec.Authorized)
		return
	}
	if !rec.Authorized {
		w.unauthorized()
		return
	}
	s.authCount++
	if s.UnauthorizedAfter > 0 && s.authCount > s.UnauthorizedAfter {
		w.unauthorized()
		return
	}
	p, err := url.PathUnescape(rec.Path)
	if err != nil {
		w.status = http.StatusBadRequest
		return
	}
	switch req.Method {
	case "PROPFIND":
		s.propfind(w, p, rec.Depth, body)
	case "REPORT":
		s.report(w, p, body)
	case http.MethodGet:
		s.get(w, p)
	case http.MethodPut:
		s.put(w, p, req.Header, body)
	case http.MethodDelete:
		s.delete(w, p, req.Header)
	default:
		w.status = http.StatusMethodNotAllowed
	}
}

func (s *Server) delay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Delay
}

func (s *Server) takeFailure(method, path, host string) (Failure, bool) {
	for i, f := range s.failures {
		if (f.Method == "" || f.Method == method) && (f.Path == "" || f.Path == path) && (f.Host == "" || f.Host == host) {
			if !f.Persist {
				s.failures = append(s.failures[:i], s.failures[i+1:]...)
			}
			return f, true
		}
	}
	return Failure{}, false
}

func (s *Server) options(w *recorder, authorized bool) {
	dav := "1, 2, 3, access-control, extended-mkcol, calendar-access"
	if s.Scheduling {
		dav += ", calendar-auto-schedule"
	}
	if s.DAV != "" {
		dav = s.DAV
	}
	if !authorized && s.OptionsNeedAuth && !s.DAVOnUnauthorized {
		w.unauthorized()
		return
	}
	if !authorized && s.DAVOnUnauthorized {
		w.header.Set("DAV", dav)
		w.unauthorized()
		return
	}
	w.header.Set("DAV", dav)
	w.header.Set("Allow", "OPTIONS, GET, HEAD, DELETE, PROPFIND, PUT, PROPPATCH, COPY, MOVE, REPORT")
	w.status = http.StatusOK
}

// --- recorder ---------------------------------------------------------------

// recorder collects an answer; a repeated body (Failure.Size) is streamed.
type recorder struct {
	status int
	header http.Header
	buf    bytes.Buffer
	stream io.Reader
}

func newRecorder() *recorder { return &recorder{status: http.StatusOK, header: http.Header{}} }

func (w *recorder) body() io.Reader {
	if w.stream != nil {
		return w.stream
	}
	return bytes.NewReader(w.buf.Bytes())
}

func (w *recorder) response(req *http.Request) *http.Response {
	resp := &http.Response{
		Status: fmt.Sprintf("%d %s", w.status, http.StatusText(w.status)), StatusCode: w.status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: w.header, Body: io.NopCloser(w.body()), Request: req, ContentLength: -1,
	}
	if w.stream == nil {
		resp.ContentLength = int64(w.buf.Len())
	}
	return resp
}

func (w *recorder) unauthorized() {
	w.header.Set("WWW-Authenticate", `Basic realm="fake"`)
	w.status = http.StatusUnauthorized
}

func (w *recorder) xml(status int, body string) {
	w.header.Set("Content-Type", `application/xml; charset=utf-8`)
	w.status = status
	w.buf.WriteString(body)
}

func (w *recorder) davError(status int, precondition string) {
	w.xml(status, `<?xml version="1.0" encoding="utf-8"?><d:error xmlns:d="DAV:"><d:`+precondition+`/></d:error>`)
}

func (w *recorder) fail(f Failure) {
	for k, v := range f.Header {
		w.header[k] = append([]string(nil), v...)
	}
	w.status = f.Status
	if w.status == 0 {
		w.status = http.StatusInternalServerError
	}
	if f.Size > 0 && len(f.Body) > 0 {
		// Whole chunks only, so that the answer stays well-formed.
		n := (f.Size + int64(len(f.Body)) - 1) / int64(len(f.Body))
		w.stream = io.MultiReader(bytes.NewReader(f.Prefix), &repeater{chunk: f.Body, left: n * int64(len(f.Body))}, bytes.NewReader(f.Suffix))
		return
	}
	w.buf.Write(f.Body)
}

// repeater yields chunk over and over until left bytes.
type repeater struct {
	chunk []byte
	left  int64
	off   int
}

func (r *repeater) Read(p []byte) (int, error) {
	if r.left <= 0 || len(r.chunk) == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.left > 0 {
		c := copy(p[n:], r.chunk[r.off:])
		if int64(c) > r.left {
			c = int(r.left)
		}
		n += c
		r.left -= int64(c)
		r.off = (r.off + c) % len(r.chunk)
	}
	return n, nil
}
