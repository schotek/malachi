// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// XML namespaces of WebDAV, CalDAV and the two vendor extensions every
// server speaks.
const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
	NSCS     = "http://calendarserver.org/ns/" // getctag
	NSApple  = "http://apple.com/ns/ical/"     // calendar-color
)

// Limits on an XML answer (hostile input, docs/calendar.md §3.5).
const (
	MaxXMLDepth    = 32
	MaxXMLElements = 200_000
	// maxNodeText bounds the text of one element (a calendar-data
	// element holds one object): over it the element is marked too large
	// and its text dropped.
	maxNodeText = api.MaxICSBytes + 64<<10
	// Caps of the strings kept from a property.
	maxHrefBytes   = 2048
	maxETagBytes   = 256
	maxTokenBytes  = 2048
	maxNameBytes   = 256
	maxTZBytes     = 256 << 10
	maxAddrEntries = 32
	maxListEntries = 64
	maxAttrs       = 8
	maxAttrBytes   = 256
)

// Property names the client asks for.
var (
	PropResourceType          = xml.Name{Space: NSDAV, Local: "resourcetype"}
	PropDisplayName           = xml.Name{Space: NSDAV, Local: "displayname"}
	PropGetETag               = xml.Name{Space: NSDAV, Local: "getetag"}
	PropGetContentType        = xml.Name{Space: NSDAV, Local: "getcontenttype"}
	PropSyncToken             = xml.Name{Space: NSDAV, Local: "sync-token"}
	PropCurrentUserPrincipal  = xml.Name{Space: NSDAV, Local: "current-user-principal"}
	PropCurrentUserPrivileges = xml.Name{Space: NSDAV, Local: "current-user-privilege-set"}
	PropSupportedReportSet    = xml.Name{Space: NSDAV, Local: "supported-report-set"}
	PropCalendarHomeSet       = xml.Name{Space: NSCalDAV, Local: "calendar-home-set"}
	PropCalendarUserAddresses = xml.Name{Space: NSCalDAV, Local: "calendar-user-address-set"}
	PropScheduleInboxURL      = xml.Name{Space: NSCalDAV, Local: "schedule-inbox-URL"}
	PropScheduleOutboxURL     = xml.Name{Space: NSCalDAV, Local: "schedule-outbox-URL"}
	PropScheduleDefault       = xml.Name{Space: NSCalDAV, Local: "schedule-default-calendar-URL"}
	PropSupportedComponents   = xml.Name{Space: NSCalDAV, Local: "supported-calendar-component-set"}
	PropCalendarTimezone      = xml.Name{Space: NSCalDAV, Local: "calendar-timezone"}
	PropCalendarData          = xml.Name{Space: NSCalDAV, Local: "calendar-data"}
	PropGetCTag               = xml.Name{Space: NSCS, Local: "getctag"}
	PropCalendarColor         = xml.Name{Space: NSApple, Local: "calendar-color"}
)

// Resource types.
var (
	TypeCollection    = xml.Name{Space: NSDAV, Local: "collection"}
	TypePrincipal     = xml.Name{Space: NSDAV, Local: "principal"}
	TypeCalendar      = xml.Name{Space: NSCalDAV, Local: "calendar"}
	TypeScheduleInbox = xml.Name{Space: NSCalDAV, Local: "schedule-inbox"}
	TypeScheduleOut   = xml.Name{Space: NSCalDAV, Local: "schedule-outbox"}
)

// Reports.
var (
	ReportSyncCollection   = xml.Name{Space: NSDAV, Local: "sync-collection"}
	ReportCalendarQuery    = xml.Name{Space: NSCalDAV, Local: "calendar-query"}
	ReportCalendarMultiget = xml.Name{Space: NSCalDAV, Local: "calendar-multiget"}
)

// node is one element of a parsed answer.
type node struct {
	name    xml.Name
	attrs   []xml.Attr // at most maxAttrs
	text    strings.Builder
	tooBig  bool
	kids    []*node
	textLen int
}

func (n *node) child(name xml.Name) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *node) children(name xml.Name) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

func (n *node) str() string { return n.text.String() }

// parseXML reads data into a tree with encoding/xml in Strict mode: no
// entity but the five predefined ones (Go's decoder expands none and
// fails on any other), directives and processing instructions skipped,
// at most MaxXMLDepth levels and MaxXMLElements elements, one root
// element and nothing after it but white space and comments. Any
// violation is malformedCalendar.
func parseXML(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	var (
		root     *node
		stack    []*node
		elements int
		done     bool
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, malformed("XML does not parse")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if done {
				return nil, malformed("XML has a second root element")
			}
			elements++
			if elements > MaxXMLElements {
				return nil, malformed("XML has more than %d elements", MaxXMLElements)
			}
			if len(stack) >= MaxXMLDepth {
				return nil, malformed("XML nested deeper than %d", MaxXMLDepth)
			}
			n := &node{name: t.Name}
			for _, a := range t.Attr {
				if len(n.attrs) < maxAttrs && len(a.Value) <= maxAttrBytes {
					n.attrs = append(n.attrs, a)
				}
			}
			if len(stack) == 0 {
				root = n
			} else {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, malformed("XML end without start")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				done = true
			}
		case xml.CharData:
			if len(stack) == 0 {
				if len(bytes.TrimSpace(t)) > 0 {
					return nil, malformed("XML has text outside the root")
				}
				continue
			}
			n := stack[len(stack)-1]
			if n.tooBig {
				continue
			}
			if n.textLen+len(t) > maxNodeText {
				n.tooBig = true
				n.text.Reset()
				continue
			}
			n.textLen += len(t)
			n.text.Write(t)
		}
	}
	if root == nil || !done {
		return nil, malformed("XML has no complete root element")
	}
	return root, nil
}

// Props is what a 200 propstat of one response carried. Every string is
// cleaned and capped; a value that fails its shape is left empty. URL
// properties are absolute URLs resolved against the request; whether one
// may be used is the caller's decision (Client.sameOrigin).
type Props struct {
	ResourceTypes []xml.Name
	DisplayName   string
	ETag          string
	CTag          string
	SyncToken     string
	// Color is #rrggbb (Apple's #RRGGBBAA loses its alpha), "" when absent
	// or not a colour.
	Color       string
	Timezone    string // calendar-timezone: a VCALENDAR with one VTIMEZONE, unparsed
	ContentType string
	// Components of supported-calendar-component-set; ComponentsSet tells
	// an absent property (all components, RFC 4791 §5.2.3) from an empty
	// one.
	Components    []string
	ComponentsSet bool
	// Privileges are the local names of current-user-privilege-set
	// (read, write, write-content, all, …); PrivilegesSet tells an
	// absent property from an empty one.
	Privileges    []string
	PrivilegesSet bool
	// CalendarData is the object of a calendar-multiget answer;
	// DataTooLarge when it exceeded the limit (and was dropped).
	CalendarData []byte
	DataTooLarge bool

	CurrentUserPrincipal       string
	CalendarHomeSet            []string
	CalendarUserAddresses      []string // mailto: values and URLs, as given (cleaned)
	ScheduleInboxURL           string
	ScheduleOutboxURL          string
	ScheduleDefaultCalendarURL string
	SupportedReports           []xml.Name
}

// Is reports whether the resource type holds t.
func (p *Props) Is(t xml.Name) bool {
	for _, r := range p.ResourceTypes {
		if r == t {
			return true
		}
	}
	return false
}

// HasComponent reports whether the collection holds component c (VEVENT):
// an absent component set means every component.
func (p *Props) HasComponent(c string) bool {
	if !p.ComponentsSet {
		return true
	}
	for _, x := range p.Components {
		if x == c {
			return true
		}
	}
	return false
}

// CanWrite reports whether the privileges allow changing the objects of
// a collection (write, write-content or all); an absent privilege set
// counts as writable (the server decides with a 403).
func (p *Props) CanWrite() bool {
	if !p.PrivilegesSet {
		return true
	}
	for _, x := range p.Privileges {
		switch x {
		case "write", "write-content", "all":
			return true
		}
	}
	return false
}

// SupportsReport reports whether supported-report-set names r.
func (p *Props) SupportsReport(r xml.Name) bool {
	for _, x := range p.SupportedReports {
		if x == r {
			return true
		}
	}
	return false
}

// Response is one DAV:response of a multistatus.
type Response struct {
	// Href is the resource's path (percent-encoding normalised), on the
	// request's origin.
	Href string
	// Status is the response's own status (sync-collection: 404 for a
	// removed member, 507 for a truncated answer); 0 when it carries
	// propstats.
	Status int
	Props  Props
}

// multistatus is a parsed 207 answer.
type multistatus struct {
	Responses []Response
	SyncToken string // sync-collection's top-level token
	// Dropped counts responses whose href was unusable: another origin,
	// not a path, too long, with dot segments.
	Dropped int
}

// parseMultistatus reads a 207 body. base is the request's URL: hrefs
// are resolved against it and responses on another origin are dropped.
func parseMultistatus(data []byte, base *url.URL) (*multistatus, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	if root.name != (xml.Name{Space: NSDAV, Local: "multistatus"}) {
		return nil, malformed("answer is not a DAV:multistatus")
	}
	ms := &multistatus{}
	if t := root.child(PropSyncToken); t != nil {
		ms.SyncToken = cleanToken(t.str(), maxTokenBytes)
	}
	for _, r := range root.children(xml.Name{Space: NSDAV, Local: "response"}) {
		hrefs := r.children(xml.Name{Space: NSDAV, Local: "href"})
		if len(hrefs) == 0 {
			ms.Dropped++
			continue
		}
		status := 0
		if s := r.child(xml.Name{Space: NSDAV, Local: "status"}); s != nil {
			status = parseStatusLine(s.str())
		}
		var props Props
		for _, ps := range r.children(xml.Name{Space: NSDAV, Local: "propstat"}) {
			st := ps.child(xml.Name{Space: NSDAV, Local: "status"})
			if st == nil || parseStatusLine(st.str()) != 200 {
				continue
			}
			if p := ps.child(xml.Name{Space: NSDAV, Local: "prop"}); p != nil {
				readProps(p, base, &props)
			}
		}
		if status == 0 && len(r.children(xml.Name{Space: NSDAV, Local: "propstat"})) == 0 {
			// Neither a status nor a propstat: nothing to say about it.
			ms.Dropped++
			continue
		}
		// Several hrefs share one status (RFC 4918 §14.24); with
		// propstats only the first href applies.
		if status == 0 {
			hrefs = hrefs[:1]
		}
		for _, h := range hrefs {
			p, ok := resolveHref(base, h.str())
			if !ok {
				ms.Dropped++
				continue
			}
			ms.Responses = append(ms.Responses, Response{Href: p, Status: status, Props: props})
		}
	}
	return ms, nil
}

// readProps fills props from a DAV:prop element.
func readProps(p *node, base *url.URL, props *Props) {
	for _, k := range p.kids {
		switch k.name {
		case PropResourceType:
			props.ResourceTypes = nil
			for _, t := range k.kids {
				if len(props.ResourceTypes) < maxListEntries {
					props.ResourceTypes = append(props.ResourceTypes, t.name)
				}
			}
		case PropDisplayName:
			props.DisplayName = cleanText(k.str(), maxNameBytes)
		case PropGetETag:
			props.ETag = cleanToken(k.str(), maxETagBytes)
		case PropGetCTag:
			props.CTag = cleanToken(k.str(), maxTokenBytes)
		case PropSyncToken:
			props.SyncToken = cleanToken(k.str(), maxTokenBytes)
		case PropCalendarColor:
			props.Color = NormaliseColor(k.str())
		case PropCalendarTimezone:
			if !k.tooBig && k.textLen <= maxTZBytes && utf8.ValidString(k.str()) {
				props.Timezone = k.str()
			}
		case PropGetContentType:
			props.ContentType = cleanToken(k.str(), maxNameBytes)
		case PropSupportedComponents:
			props.ComponentsSet = true
			props.Components = nil
			props.Components = k.compNames()
		case PropCurrentUserPrivileges:
			props.PrivilegesSet = true
			props.Privileges = nil
			for _, pr := range k.children(xml.Name{Space: NSDAV, Local: "privilege"}) {
				for _, x := range pr.kids {
					if len(props.Privileges) < maxListEntries {
						props.Privileges = append(props.Privileges, x.name.Local)
					}
				}
			}
		case PropCalendarData:
			if k.tooBig || k.textLen > api.MaxICSBytes {
				props.DataTooLarge = true
				props.CalendarData = nil
			} else {
				props.CalendarData = []byte(k.str())
			}
		case PropCurrentUserPrincipal:
			props.CurrentUserPrincipal = firstHref(k, base)
		case PropCalendarHomeSet:
			props.CalendarHomeSet = hrefList(k, base)
		case PropScheduleInboxURL:
			props.ScheduleInboxURL = firstHref(k, base)
		case PropScheduleOutboxURL:
			props.ScheduleOutboxURL = firstHref(k, base)
		case PropScheduleDefault:
			props.ScheduleDefaultCalendarURL = firstHref(k, base)
		case PropCalendarUserAddresses:
			props.CalendarUserAddresses = nil
			for _, h := range k.children(xml.Name{Space: NSDAV, Local: "href"}) {
				if len(props.CalendarUserAddresses) >= maxAddrEntries {
					break
				}
				if s := cleanToken(strings.TrimSpace(h.str()), maxHrefBytes); s != "" {
					props.CalendarUserAddresses = append(props.CalendarUserAddresses, s)
				}
			}
		case PropSupportedReportSet:
			props.SupportedReports = nil
			for _, sr := range k.children(xml.Name{Space: NSDAV, Local: "supported-report"}) {
				rep := sr.child(xml.Name{Space: NSDAV, Local: "report"})
				if rep == nil {
					continue
				}
				for _, x := range rep.kids {
					if len(props.SupportedReports) < maxListEntries {
						props.SupportedReports = append(props.SupportedReports, x.name)
					}
				}
			}
		}
	}
}

// firstHref is the first DAV:href of a property, resolved to an absolute
// URL; "" when it has none usable.
func firstHref(n *node, base *url.URL) string {
	if l := hrefList(n, base); len(l) > 0 {
		return l[0]
	}
	return ""
}

func hrefList(n *node, base *url.URL) []string {
	var out []string
	for _, h := range n.children(xml.Name{Space: NSDAV, Local: "href"}) {
		if len(out) >= maxListEntries {
			break
		}
		if u, ok := resolveURL(base, h.str()); ok {
			out = append(out, u.String())
		}
	}
	return out
}

// resolveURL resolves a server's href against base: http(s) only, a
// host, no user info, no dot segments, valid UTF-8 without control
// characters, ≤ maxHrefBytes. The path's percent-encoding is normalised,
// query and fragment dropped.
func resolveURL(base *url.URL, raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxHrefBytes || !utf8.ValidString(raw) {
		return nil, false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return nil, false
		}
	}
	ref, err := url.Parse(raw)
	if err != nil || ref.User != nil || ref.Opaque != "" {
		return nil, false
	}
	for _, seg := range strings.Split(ref.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, false
		}
	}
	u := base.ResolveReference(ref)
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, false
	}
	if !strings.HasPrefix(u.Path, "/") {
		return nil, false
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	u.RawPath = ""
	return u, true
}

// resolveHref is resolveURL for a response's href: it must stay on the
// origin of base; the result is the normalised path.
func resolveHref(base *url.URL, raw string) (string, bool) {
	u, ok := resolveURL(base, raw)
	if !ok || !sameOrigin(u, base) {
		return "", false
	}
	return u.EscapedPath(), true
}

// NormaliseHref returns the canonical form of a path href as the client
// reports it (percent-encoding normalised), "" when it is not a usable
// path.
func NormaliseHref(href string) string {
	base := &url.URL{Scheme: "https", Host: "x.invalid", Path: "/"}
	p, ok := resolveHref(base, href)
	if !ok || strings.Contains(href, "://") {
		return ""
	}
	return p
}

// compNames reads the name attributes of supported-calendar-component-set's
// comp children, upper-cased.
func (n *node) compNames() []string {
	var out []string
	for _, c := range n.children(xml.Name{Space: NSCalDAV, Local: "comp"}) {
		name := strings.ToUpper(strings.TrimSpace(c.attr("name")))
		if name != "" && len(name) <= 32 && isToken(name) && len(out) < maxListEntries {
			out = append(out, name)
		}
	}
	return out
}

// attr is the value of the unqualified attribute local, "" when absent.
func (n *node) attr(local string) string {
	for _, a := range n.attrs {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func isToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// parseStatusLine reads "HTTP/1.1 404 Not Found"; 0 when it is not one.
func parseStatusLine(s string) int {
	f := strings.Fields(s)
	if len(f) < 2 || !strings.HasPrefix(f[0], "HTTP/") {
		return 0
	}
	n, err := strconv.Atoi(f[1])
	if err != nil || n < 100 || n > 599 {
		return 0
	}
	return n
}

// parsePrecondition is the local name of the first child of a DAV:error
// body ("valid-sync-token"), "" when data is not one.
func parsePrecondition(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	root, err := parseXML(data)
	if err != nil || root.name != (xml.Name{Space: NSDAV, Local: "error"}) || len(root.kids) == 0 {
		return ""
	}
	l := root.kids[0].name.Local
	if len(l) > 64 || !isLowerToken(l) {
		return ""
	}
	return l
}

func isLowerToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return s != ""
}

// NormaliseColor turns a server's calendar colour into #rrggbb: #RGB,
// #RRGGBB and Apple's #RRGGBBAA (alpha dropped) are accepted, anything
// else is "".
func NormaliseColor(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return ""
	}
	h := s[1:]
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return ""
		}
	}
	switch len(h) {
	case 3:
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6:
	case 8:
		h = h[:6]
	default:
		return ""
	}
	return "#" + strings.ToLower(h)
}

// cleanText removes control, format (bidi, zero-width), line and
// paragraph separator characters, collapses white space and caps the
// result at max bytes on a rune boundary.
func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	return capBytes(s, max)
}

// cleanToken keeps an opaque protocol value (ETag, sync token, ctag):
// trimmed, printable ASCII only, ≤ max bytes; anything else is "".
func cleanToken(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > max {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return ""
		}
	}
	return s
}

func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// --- request bodies ---------------------------------------------------------

var prefixes = map[string]string{NSDAV: "d", NSCalDAV: "c", NSCS: "cs", NSApple: "ic"}

const nsDecl = ` xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/"`

func qname(n xml.Name) string { return prefixes[n.Space] + ":" + n.Local }

func propList(b *strings.Builder, props []xml.Name) {
	b.WriteString("<d:prop>")
	for _, p := range props {
		if _, ok := prefixes[p.Space]; !ok {
			continue
		}
		b.WriteString("<" + qname(p) + "/>")
	}
	b.WriteString("</d:prop>")
}

func propfindBody(props []xml.Name) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:propfind` + nsDecl + `>`)
	propList(&b, props)
	b.WriteString("</d:propfind>")
	return []byte(b.String())
}

func escapeText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// calendarQueryBody asks for the hrefs and ETags of the VEVENT objects
// overlapping [start, end) (RFC 4791 §7.8); start and end are UTC
// "20060102T150405Z".
func calendarQueryBody(start, end string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><c:calendar-query` + nsDecl + `>`)
	propList(&b, []xml.Name{PropGetETag})
	b.WriteString(`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">`)
	b.WriteString(`<c:time-range start="` + start + `" end="` + end + `"/>`)
	b.WriteString(`</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`)
	return []byte(b.String())
}

func multigetBody(hrefs []string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><c:calendar-multiget` + nsDecl + `>`)
	propList(&b, []xml.Name{PropGetETag, PropCalendarData})
	for _, h := range hrefs {
		b.WriteString("<d:href>" + escapeText(h) + "</d:href>")
	}
	b.WriteString("</c:calendar-multiget>")
	return []byte(b.String())
}

func syncCollectionBody(token string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:sync-collection` + nsDecl + `>`)
	b.WriteString("<d:sync-token>" + escapeText(token) + "</d:sync-token><d:sync-level>1</d:sync-level>")
	propList(&b, []xml.Name{PropGetETag})
	b.WriteString("</d:sync-collection>")
	return []byte(b.String())
}
