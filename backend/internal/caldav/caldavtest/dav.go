// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldavtest

import (
	"bytes"
	"context"
	"encoding/xml"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	nsDAV    = "DAV:"
	nsCalDAV = "urn:ietf:params:xml:ns:caldav"
	nsCS     = "http://calendarserver.org/ns/"
	nsApple  = "http://apple.com/ns/ical/"
)

var prefix = map[string]string{nsDAV: "d", nsCalDAV: "c", nsCS: "cs", nsApple: "ic"}

const msOpen = `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/">`

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func escPath(p string) string { return esc((&url.URL{Path: p}).EscapedPath()) }

func hrefXML(p string) string { return "<d:href>" + escPath(p) + "</d:href>" }

func tag(n xml.Name, inner string) string {
	if p, ok := prefix[n.Space]; ok {
		if inner == "" {
			return "<" + p + ":" + n.Local + "/>"
		}
		return "<" + p + ":" + n.Local + ">" + inner + "</" + p + ":" + n.Local + ">"
	}
	return `<x:` + n.Local + ` xmlns:x="` + esc(n.Space) + `"/>`
}

// kind of a path.
const (
	kRoot = 1 << iota
	kPrincipal
	kHome
	kInbox
	kOutbox
	kCalendar
	kObject
)

func trimSlash(p string) string { return strings.TrimSuffix(p, "/") }

func (s *Server) kindOf(p string) (int, *Calendar) {
	k := 0
	tp := trimSlash(p)
	if tp == trimSlash(s.root) {
		k |= kRoot
	}
	if tp == trimSlash(s.principal) {
		k |= kPrincipal
	}
	if tp == trimSlash(s.home) {
		k |= kHome
	}
	if s.inbox != "" && tp == trimSlash(s.inbox) {
		k |= kInbox
	}
	if s.outbox != "" && tp == trimSlash(s.outbox) {
		k |= kOutbox
	}
	if c := s.calendar(p); c != nil {
		return k | kCalendar, c
	}
	if c := s.calendarOf(p); c != nil {
		if _, ok := c.objects[p]; ok {
			return k | kObject, c
		}
	}
	return k, nil
}

// canonical is the href the fake reports for a path of kind k.
func canonical(p string, k int) string {
	if k&kObject != 0 {
		return p
	}
	return trimSlash(p) + "/"
}

// prop is the value of property n on path p, ok false when the resource
// does not have it.
func (s *Server) prop(p string, k int, c *Calendar, n xml.Name) (string, bool) {
	switch n {
	case xml.Name{Space: nsDAV, Local: "resourcetype"}:
		var b strings.Builder
		if k&^kObject != 0 {
			b.WriteString("<d:collection/>")
		}
		if k&kPrincipal != 0 {
			b.WriteString("<d:principal/>")
		}
		if k&kCalendar != 0 {
			b.WriteString("<c:calendar/>")
		}
		if k&kInbox != 0 {
			b.WriteString("<c:schedule-inbox/>")
		}
		if k&kOutbox != 0 {
			b.WriteString("<c:schedule-outbox/>")
		}
		return b.String(), true
	case xml.Name{Space: nsDAV, Local: "current-user-principal"}:
		return hrefXML(s.principal), true
	case xml.Name{Space: nsDAV, Local: "displayname"}:
		switch {
		case k&kCalendar != 0:
			return esc(c.Name), c.Name != ""
		case k&kPrincipal != 0:
			return esc(s.displayName), true
		}
	case xml.Name{Space: nsCalDAV, Local: "calendar-home-set"}:
		if k&kPrincipal != 0 {
			return hrefXML(s.home), true
		}
	case xml.Name{Space: nsCalDAV, Local: "calendar-user-address-set"}:
		if k&kPrincipal != 0 {
			var b strings.Builder
			for _, a := range s.addresses {
				if strings.HasPrefix(a, "mailto:") {
					b.WriteString("<d:href>" + esc(a) + "</d:href>")
				} else {
					b.WriteString(hrefXML(a))
				}
			}
			return b.String(), true
		}
	case xml.Name{Space: nsCalDAV, Local: "schedule-inbox-URL"}:
		if k&kPrincipal != 0 && s.inbox != "" {
			return hrefXML(s.inbox), true
		}
	case xml.Name{Space: nsCalDAV, Local: "schedule-outbox-URL"}:
		if k&kPrincipal != 0 && s.outbox != "" {
			return hrefXML(s.outbox), true
		}
	case xml.Name{Space: nsCalDAV, Local: "schedule-default-calendar-URL"}:
		if k&kInbox != 0 && s.Scheduling && s.defaultCal != "" {
			return hrefXML(s.defaultCal), true
		}
	}
	if k&kCalendar != 0 {
		switch n {
		case xml.Name{Space: nsApple, Local: "calendar-color"}:
			return esc(c.Color), c.Color != ""
		case xml.Name{Space: nsCalDAV, Local: "supported-calendar-component-set"}:
			comps := c.Components
			if comps == nil {
				comps = []string{"VEVENT"}
			}
			var b strings.Builder
			for _, x := range comps {
				b.WriteString(`<c:comp name="` + esc(x) + `"/>`)
			}
			return b.String(), true
		case xml.Name{Space: nsDAV, Local: "current-user-privilege-set"}:
			b := "<d:privilege><d:read/></d:privilege><d:privilege><d:read-current-user-privilege-set/></d:privilege>"
			if !c.ReadOnly {
				b += "<d:privilege><d:write/></d:privilege><d:privilege><d:write-content/></d:privilege><d:privilege><d:bind/></d:privilege><d:privilege><d:unbind/></d:privilege>"
			}
			return b, true
		case xml.Name{Space: nsCS, Local: "getctag"}:
			return esc(c.ctagValue()), true
		case xml.Name{Space: nsDAV, Local: "sync-token"}:
			return esc(c.token(c.seq)), s.SyncCollection
		case xml.Name{Space: nsCalDAV, Local: "calendar-timezone"}:
			return esc(c.Timezone), c.Timezone != ""
		case xml.Name{Space: nsDAV, Local: "supported-report-set"}:
			b := "<d:supported-report><d:report><c:calendar-multiget/></d:report></d:supported-report>" +
				"<d:supported-report><d:report><c:calendar-query/></d:report></d:supported-report>"
			if s.SyncCollection {
				b += "<d:supported-report><d:report><d:sync-collection/></d:report></d:supported-report>"
			}
			return b, true
		}
	}
	if k&kObject != 0 {
		o := c.objects[p]
		switch n {
		case xml.Name{Space: nsDAV, Local: "getetag"}:
			return esc(o.etag), true
		case xml.Name{Space: nsDAV, Local: "getcontenttype"}:
			return "text/calendar; charset=utf-8; component=vevent", true
		}
	}
	return "", false
}

func (s *Server) response(b *strings.Builder, p string, k int, c *Calendar, props []xml.Name) {
	b.WriteString("<d:response>" + hrefXML(canonical(p, k)))
	var ok, missing strings.Builder
	for _, n := range props {
		if v, has := s.prop(p, k, c, n); has {
			ok.WriteString(tag(n, v))
		} else {
			missing.WriteString(tag(n, ""))
		}
	}
	if ok.Len() > 0 {
		b.WriteString("<d:propstat><d:prop>" + ok.String() + "</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>")
	}
	if missing.Len() > 0 {
		b.WriteString("<d:propstat><d:prop>" + missing.String() + "</d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>")
	}
	b.WriteString("</d:response>")
}

// parsed is what the fake reads of a request body.
type parsed struct {
	root      xml.Name
	props     []xml.Name
	hrefs     []string
	syncToken string
	start     string
	end       string
}

func parseBody(body []byte) (parsed, bool) {
	var p parsed
	if len(bytes.TrimSpace(body)) == 0 {
		return p, true
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	var stack []xml.Name
	for {
		tok, err := d.Token()
		if err != nil {
			return p, err.Error() == "EOF"
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				p.root = t.Name
			}
			if len(stack) > 0 && stack[len(stack)-1] == (xml.Name{Space: nsDAV, Local: "prop"}) {
				p.props = append(p.props, t.Name)
			}
			if t.Name == (xml.Name{Space: nsCalDAV, Local: "time-range"}) {
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "start":
						p.start = a.Value
					case "end":
						p.end = a.Value
					}
				}
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			switch stack[len(stack)-1] {
			case xml.Name{Space: nsDAV, Local: "href"}:
				if u, err := url.PathUnescape(strings.TrimSpace(string(t))); err == nil {
					p.hrefs = append(p.hrefs, u)
				}
			case xml.Name{Space: nsDAV, Local: "sync-token"}:
				p.syncToken += strings.TrimSpace(string(t))
			}
		}
	}
}

var defaultProps = []xml.Name{
	{Space: nsDAV, Local: "resourcetype"}, {Space: nsDAV, Local: "displayname"},
	{Space: nsDAV, Local: "getetag"}, {Space: nsCS, Local: "getctag"},
}

func (s *Server) propfind(w *recorder, p, depth string, body []byte) {
	req, ok := parseBody(body)
	if !ok {
		w.status = http.StatusBadRequest
		return
	}
	props := req.props
	if len(props) == 0 {
		props = defaultProps
	}
	k, c := s.kindOf(p)
	if k == 0 {
		w.status = http.StatusNotFound
		return
	}
	var b strings.Builder
	b.WriteString(msOpen)
	s.response(&b, p, k, c, props)
	if depth == "1" {
		switch {
		case k&kCalendar != 0:
			hrefs := make([]string, 0, len(c.objects))
			for h := range c.objects {
				hrefs = append(hrefs, h)
			}
			sort.Strings(hrefs)
			for _, h := range hrefs {
				s.response(&b, h, kObject, c, props)
			}
		case k&kHome != 0:
			for _, cal := range s.calendars {
				s.response(&b, cal.Href, kCalendar, cal, props)
			}
			if s.inbox != "" {
				s.response(&b, s.inbox, kInbox, nil, props)
			}
			if s.outbox != "" {
				s.response(&b, s.outbox, kOutbox, nil, props)
			}
		}
	}
	b.WriteString("</d:multistatus>")
	w.xml(http.StatusMultiStatus, b.String())
}

func (s *Server) report(w *recorder, p string, body []byte) {
	req, ok := parseBody(body)
	if !ok {
		w.status = http.StatusBadRequest
		return
	}
	c := s.calendar(p)
	if c == nil {
		w.status = http.StatusNotFound
		return
	}
	switch req.root {
	case xml.Name{Space: nsDAV, Local: "sync-collection"}:
		s.syncCollection(w, c, req.syncToken)
	case xml.Name{Space: nsCalDAV, Local: "calendar-query"}:
		if !s.TimeRange && req.start != "" {
			w.davError(http.StatusForbidden, "supported-filter")
			return
		}
		from, _ := time.Parse("20060102T150405Z", req.start)
		to, _ := time.Parse("20060102T150405Z", req.end)
		var b strings.Builder
		b.WriteString(msOpen)
		for _, h := range sortedHrefs(c) {
			if req.start != "" && !overlaps(c.objects[h].data, from, to) {
				continue
			}
			s.response(&b, h, kObject, c, []xml.Name{{Space: nsDAV, Local: "getetag"}})
		}
		b.WriteString("</d:multistatus>")
		w.xml(http.StatusMultiStatus, b.String())
	case xml.Name{Space: nsCalDAV, Local: "calendar-multiget"}:
		if !s.Multiget {
			w.status = http.StatusNotImplemented
			return
		}
		var b strings.Builder
		b.WriteString(msOpen)
		for _, h := range req.hrefs {
			o := c.objects[h]
			if o == nil {
				b.WriteString("<d:response>" + hrefXML(h) + "<d:status>HTTP/1.1 404 Not Found</d:status></d:response>")
				continue
			}
			b.WriteString("<d:response>" + hrefXML(h) + "<d:propstat><d:prop><d:getetag>" + esc(o.etag) +
				"</d:getetag><c:calendar-data>" + esc(string(o.data)) +
				"</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>")
		}
		b.WriteString("</d:multistatus>")
		w.xml(http.StatusMultiStatus, b.String())
	default:
		w.davError(http.StatusForbidden, "supported-report")
	}
}

func sortedHrefs(c *Calendar) []string {
	out := make([]string, 0, len(c.objects))
	for h := range c.objects {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func (s *Server) syncCollection(w *recorder, c *Calendar, token string) {
	if !s.SyncCollection {
		w.davError(http.StatusForbidden, "supported-report")
		return
	}
	since := 0
	if token != "" {
		n, ok := c.parseToken(token)
		if !ok {
			w.davError(http.StatusForbidden, "valid-sync-token")
			return
		}
		since = n
	}
	type change struct {
		href    string
		seq     int
		removed bool
	}
	var changes []change
	for h, o := range c.objects {
		if o.seq > since {
			changes = append(changes, change{h, o.seq, false})
		}
	}
	if token != "" {
		for h, seq := range c.removed {
			if seq > since {
				changes = append(changes, change{h, seq, true})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].seq < changes[j].seq })
	newSeq := c.seq
	truncated := false
	if s.SyncLimit > 0 && len(changes) > s.SyncLimit {
		changes = changes[:s.SyncLimit]
		newSeq = changes[len(changes)-1].seq
		truncated = true
	}
	var b strings.Builder
	b.WriteString(msOpen)
	for _, ch := range changes {
		if ch.removed {
			b.WriteString("<d:response>" + hrefXML(ch.href) + "<d:status>HTTP/1.1 404 Not Found</d:status></d:response>")
			continue
		}
		s.response(&b, ch.href, kObject, c, []xml.Name{{Space: nsDAV, Local: "getetag"}})
	}
	if truncated {
		b.WriteString("<d:response>" + hrefXML(c.Href) + "<d:status>HTTP/1.1 507 Insufficient Storage</d:status></d:response>")
	}
	b.WriteString("<d:sync-token>" + esc(c.token(newSeq)) + "</d:sync-token></d:multistatus>")
	w.xml(http.StatusMultiStatus, b.String())
}

func (s *Server) get(w *recorder, p string) {
	c := s.calendarOf(p)
	if c == nil || c.objects[p] == nil {
		w.status = http.StatusNotFound
		return
	}
	o := c.objects[p]
	w.header.Set("Content-Type", "text/calendar; charset=utf-8")
	w.header.Set("ETag", o.etag)
	w.buf.Write(o.data)
}

func (s *Server) put(w *recorder, p string, h http.Header, body []byte) {
	c := s.calendarOf(p)
	if c == nil {
		w.status = http.StatusConflict
		return
	}
	if c.ReadOnly {
		w.davError(http.StatusForbidden, "need-privileges")
		return
	}
	if !bytes.Contains(body, []byte("BEGIN:VCALENDAR")) {
		w.status = http.StatusBadRequest
		return
	}
	o := c.objects[p]
	if h.Get("If-None-Match") == "*" && o != nil {
		w.status = http.StatusPreconditionFailed
		return
	}
	if m := h.Get("If-Match"); m != "" && (o == nil || o.etag != m) {
		w.status = http.StatusPreconditionFailed
		return
	}
	etag := c.put(p, body)
	if s.ETagOnPut {
		w.header.Set("ETag", etag)
	}
	if o == nil {
		w.status = http.StatusCreated
	} else {
		w.status = http.StatusNoContent
	}
}

func (s *Server) delete(w *recorder, p string, h http.Header) {
	c := s.calendarOf(p)
	if c == nil || c.objects[p] == nil {
		w.status = http.StatusNotFound
		return
	}
	if c.ReadOnly {
		w.davError(http.StatusForbidden, "need-privileges")
		return
	}
	if m := h.Get("If-Match"); m != "" && c.objects[p].etag != m {
		w.status = http.StatusPreconditionFailed
		return
	}
	c.remove(p)
	w.status = http.StatusNoContent
}

// overlaps is the fake's crude time-range test: the first DTSTART and
// DTEND (UTC, date or floating as UTC) of data against [from, to); a
// recurring or unreadable object always overlaps.
func overlaps(data []byte, from, to time.Time) bool {
	if bytes.Contains(data, []byte("\nRRULE")) || bytes.Contains(data, []byte("\nRDATE")) {
		return true
	}
	start, ok := icsTime(data, "DTSTART")
	if !ok {
		return true
	}
	end, ok := icsTime(data, "DTEND")
	if !ok {
		end = start.Add(time.Hour)
	}
	return (to.IsZero() || start.Before(to)) && (from.IsZero() || end.After(from))
}

func icsTime(data []byte, name string) (time.Time, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, name+":") && !strings.HasPrefix(line, name+";") {
			continue
		}
		i := strings.LastIndexByte(line, ':')
		v := strings.TrimSuffix(line[i+1:], "Z")
		for _, l := range []string{"20060102T150405", "20060102"} {
			if t, err := time.Parse(l, v); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	return time.Time{}, false
}

// --- DNS --------------------------------------------------------------------

// Resolver is a map-backed DNS for caldav.Discoverer: SRV keyed by
// "_service._proto.name", TXT by name.
type Resolver struct {
	SRV map[string][]*net.SRV
	TXT map[string][]string
}

// LookupSRV answers from the map; a missing name is "no such host".
func (r *Resolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	key := "_" + service + "._" + proto + "." + name
	if a, ok := r.SRV[key]; ok {
		return key + ".", a, nil
	}
	return "", nil, &net.DNSError{Err: "no such host", Name: key, IsNotFound: true}
}

// LookupTXT answers from the map; a missing name is "no such host".
func (r *Resolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if a, ok := r.TXT[name]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// --- hostile answers --------------------------------------------------------

func xmlHeader() http.Header {
	return http.Header{"Content-Type": {"application/xml; charset=utf-8"}}
}

// HugeMultistatus is a 207 of size bytes of valid responses, streamed.
func HugeMultistatus(size int64) Failure {
	return Failure{
		Status: http.StatusMultiStatus, Header: xmlHeader(), Size: size,
		Prefix: []byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`),
		Body:   []byte(`<d:response><d:href>/x/</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`),
		Suffix: []byte(`</d:multistatus>`),
	}
}

// DeepMultistatus is a 207 whose response nests depth elements deep.
func DeepMultistatus(depth int) Failure {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>/x/</d:href><d:propstat><d:prop>`)
	for i := 0; i < depth; i++ {
		b.WriteString("<d:x>")
	}
	for i := 0; i < depth; i++ {
		b.WriteString("</d:x>")
	}
	b.WriteString(`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
	return Failure{Status: http.StatusMultiStatus, Header: xmlHeader(), Body: []byte(b.String())}
}

// Garbage207 is a 207 whose body is not XML.
func Garbage207() Failure {
	return Failure{Status: http.StatusMultiStatus, Header: xmlHeader(), Body: []byte("\x00\xff<d:multistatus <<< not xml at all")}
}

// WrongContentType is a 207 that is a web page.
func WrongContentType() Failure {
	return Failure{
		Status: http.StatusMultiStatus, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body: []byte("<!doctype html><html><body>Please sign in</body></html>"),
	}
}

// RedirectTo is a redirect (status 301 when 0) to location.
func RedirectTo(location string, status int) Failure {
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	return Failure{Status: status, Header: http.Header{"Location": {location}}}
}

// FixtureAnswer is a fixture of backend/testdata/caldav served with
// status (207 when 0) as XML.
func FixtureAnswer(t testing.TB, name string, status int) Failure {
	t.Helper()
	if status == 0 {
		status = http.StatusMultiStatus
	}
	return Failure{Status: status, Header: xmlHeader(), Body: Fixture(t, name)}
}
