// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package bulk classifies bulk mail (newsletters, mailing lists,
// automated mail) from a few headers and picks the unsubscribe method a
// message offers. It is pure: no I/O, no clock, no logging.
//
// The headers are written by whoever sent the message and are hostile:
// they can be huge, repeated, folded, carry control and bidirectional
// characters or broken angle brackets. Only what this package accepts is
// ever shown, stored or used: at most MaxURIs bracketed URIs of at most
// MaxURIBytes each, only https: (with a host, ASCII, no credentials) and
// mailto: (with a usable address), and nothing the package cannot read
// whole.
package bulk

import (
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"

	"github.com/schotek/malachi/backend/pkg/api"
)

// RuleVersion identifies the classification rules; the backfill compares
// it with the stored cursor and starts over when it changes.
const RuleVersion = "2"

const (
	// MaxURIs is how many bracketed items of a List-Unsubscribe or
	// List-Post field are looked at; the rest is ignored.
	MaxURIs = 8
	// MaxURIBytes is the longest URI that is kept.
	MaxURIBytes = 2048
	// MaxListIDBytes caps the stored List-Id identifier.
	MaxListIDBytes = 255
	// maxScanBytes bounds how much of one header value is read.
	maxScanBytes = 32 << 10
)

// Stored values of the messages.bulk column besides the api kinds: ""
// (not classified yet) is never produced here.
const StoredNone = "none"

// Header names the rules read, by canonical name. Callers keep exactly
// these in the curated header map.
const (
	HeaderListID              = "List-Id"
	HeaderListUnsubscribe     = "List-Unsubscribe"
	HeaderListUnsubscribePost = "List-Unsubscribe-Post"
	HeaderListPost            = "List-Post"
	HeaderPrecedence          = "Precedence"
	HeaderAutoSubmitted       = "Auto-Submitted"
)

// SenderFingerprints are header fields that bulk-sending services add to
// every message they relay (rule version 2): feedback loops (Feedback-ID,
// X-CSA-Complaints, X-MSFBL) and the tracking ids of SendGrid, Mailgun,
// Amazon SES, Mailchimp/Mandrill, Postmark and Salesforce Marketing Cloud.
// One of them without an unsubscribe offer marks the message automated:
// such services carry notifications and receipts as much as marketing, so
// the fingerprint alone never makes a newsletter.
var SenderFingerprints = []string{
	"Feedback-ID",
	"X-CSA-Complaints",
	"X-MSFBL",
	"X-SG-EID",
	"X-Mailgun-Sid",
	"X-SES-Outgoing",
	"X-MC-User",
	"X-Mandrill-User",
	"X-PM-Message-Id",
	"X-SFMC-Stack",
}

// OneClickValue is the List-Unsubscribe-Post value of RFC 8058.
const OneClickValue = "List-Unsubscribe=One-Click"

// Result is the classification of one message. Kind is empty for mail
// that is not bulk.
type Result struct {
	Kind   api.BulkKind
	ListID string
}

// Stored is the value for the messages.bulk column: the kind, or "none".
func (r Result) Stored() string {
	if r.Kind == "" {
		return StoredNone
	}
	return string(r.Kind)
}

// Classify applies the rules to the curated headers of a message (keys
// as in the Header constants; matched case-insensitively).
func Classify(h map[string]string) Result {
	listID := ListID(get(h, HeaderListID))
	hasListID := cleanValue(get(h, HeaderListID)) != ""
	uris := UnsubscribeURIs(get(h, HeaderListUnsubscribe))
	switch {
	case hasListID && hasMailto(URIs(get(h, HeaderListPost))):
		return Result{Kind: api.BulkList, ListID: listID}
	case len(uris) > 0, hasListID && precedence(h) == "bulk":
		return Result{Kind: api.BulkNewsletter, ListID: listID}
	case autoSubmitted(h), precedence(h) == "bulk", precedence(h) == "junk", precedence(h) == "list",
		fingerprinted(h):
		return Result{Kind: api.BulkAutomated, ListID: listID}
	}
	return Result{}
}

// Info builds the API value from the stored columns and the From address
// of the row; nil for "", "none" or an unknown value.
func Info(stored, listID, fromAddress string) *api.BulkInfo {
	switch k := api.BulkKind(stored); k {
	case api.BulkNewsletter, api.BulkList, api.BulkAutomated:
		return &api.BulkInfo{Kind: k, ListID: listID, Domain: Domain(fromAddress)}
	}
	return nil
}

// Domain is the lower-case domain of an address; "" when there is none or
// it holds anything but printable characters.
func Domain(address string) string {
	i := strings.LastIndexByte(address, '@')
	if i < 0 {
		return ""
	}
	d := strings.TrimSuffix(strings.ToLower(address[i+1:]), ".")
	if d == "" || len(d) > 255 || !plain(d) {
		return ""
	}
	return d
}

// Aligned reports whether the signing domain d of a DKIM signature
// belongs to the same organisation as the From domain: equal
// organisational domains (public suffix + 1). Both must have one.
func Aligned(d, fromDomain string) bool {
	d, fromDomain = strings.ToLower(strings.TrimSuffix(d, ".")), strings.ToLower(strings.TrimSuffix(fromDomain, "."))
	if d == "" || fromDomain == "" {
		return false
	}
	a, err := publicsuffix.EffectiveTLDPlusOne(d)
	if err != nil {
		return false
	}
	b, err := publicsuffix.EffectiveTLDPlusOne(fromDomain)
	if err != nil {
		return false
	}
	return a == b
}

// ListID extracts the identifier of a List-Id value: what stands inside
// the first <…> (a value without brackets counts when it is one bare
// token), lower case, at most MaxListIDBytes; "" when unusable.
func ListID(value string) string {
	value = trimScan(value)
	var id string
	if i := strings.IndexByte(value, '<'); i >= 0 {
		rest := value[i+1:]
		j := strings.IndexByte(rest, '>')
		if j < 0 {
			return ""
		}
		id = rest[:j]
	} else {
		id = value
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" || !plain(id) || strings.ContainsAny(id, "<>") {
		return ""
	}
	return truncate(id, MaxListIDBytes)
}

// Kind of a usable URI.
type Kind string

const (
	KindHTTPS  Kind = "https"
	KindMailto Kind = "mailto"
)

// URI is one usable URI of a List-Unsubscribe field.
type URI struct {
	Kind Kind
	// Raw is the URI as written (ASCII, at most MaxURIBytes).
	Raw string
	// Target is the lower-case host of an https URI, the address of a
	// mailto: one.
	Target string
}

// UnsubscribeURIs lists the usable URIs of a List-Unsubscribe value in
// order of appearance.
func UnsubscribeURIs(value string) []URI { return URIs(value) }

// URIs lists the usable https: and mailto: URIs among the first MaxURIs
// bracketed items of a header value. An item that is not closed, is too
// long, holds a space, control, format or non-ASCII character, or has
// another scheme is skipped. A "<" inside an item restarts it.
func URIs(value string) []URI {
	value = trimScan(value)
	var out []URI
	items := 0
	for len(value) > 0 && items < MaxURIs {
		i := strings.IndexByte(value, '<')
		if i < 0 {
			break
		}
		value = value[i+1:]
		j := strings.IndexAny(value, "<>")
		if j < 0 {
			break
		}
		if value[j] == '<' {
			// Broken bracket: the earlier one never closed.
			value = value[j:]
			continue
		}
		raw := value[:j]
		value = value[j+1:]
		items++
		if u, ok := usable(raw); ok {
			out = append(out, u)
		}
	}
	return out
}

func usable(raw string) (URI, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURIBytes || !plain(raw) {
		return URI{}, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] >= utf8.RuneSelf || raw[i] <= ' ' || raw[i] == 0x7f {
			return URI{}, false
		}
	}
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return URI{}, false
	}
	switch strings.ToLower(raw[:colon]) {
	case "https":
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Opaque != "" {
			return URI{}, false
		}
		host := strings.ToLower(u.Hostname())
		if host == "" || strings.Contains(u.Host, "..") {
			return URI{}, false
		}
		return URI{Kind: KindHTTPS, Raw: raw, Target: host}, true
	case "mailto":
		addr, ok := mailtoAddress(raw)
		if !ok {
			return URI{}, false
		}
		return URI{Kind: KindMailto, Raw: raw, Target: addr}, true
	}
	return URI{}, false
}

// mailtoAddress is the first address of a mailto: URI, when it is a bare
// addr-spec.
func mailtoAddress(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	path := u.Opaque
	if path == "" {
		path = u.Path
	}
	to, err := url.PathUnescape(path)
	if err != nil || to == "" || !plain(to) {
		return "", false
	}
	list, err := mail.ParseAddressList(to)
	if err != nil || len(list) == 0 {
		return "", false
	}
	a := list[0]
	if a.Name != "" || !plain(a.Address) || strings.Count(a.Address, "@") != 1 || len(a.Address) > 254 {
		return "", false
	}
	return a.Address, true
}

func hasMailto(uris []URI) bool {
	for _, u := range uris {
		if u.Kind == KindMailto {
			return true
		}
	}
	return false
}

// HasOneClickPost reports whether List-Unsubscribe-Post announces RFC 8058
// one-click unsubscribing.
func HasOneClickPost(h map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(get(h, HeaderListUnsubscribePost)), OneClickValue)
}

// Offer is the unsubscribe method chosen for a message.
type Offer struct {
	Method api.UnsubscribeMethod
	// Target is the host (oneClick, url) or address (mailto).
	Target string
	// URI is the https URL (oneClick, url) or the mailto: URI.
	URI string
}

// Choose picks the unsubscribe method from the curated headers of a
// message of the given kind: a list prefers mailto, then one-click, then
// the page; anything else prefers one-click, then mailto, then the page.
// nil when the headers offer nothing usable.
func Choose(h map[string]string, kind api.BulkKind) *Offer {
	var https, mailto *URI
	uris := UnsubscribeURIs(get(h, HeaderListUnsubscribe))
	for i := range uris {
		switch u := &uris[i]; {
		case u.Kind == KindHTTPS && https == nil:
			https = u
		case u.Kind == KindMailto && mailto == nil:
			mailto = u
		}
	}
	oneClick := https != nil && HasOneClickPost(h)
	mk := func(m api.UnsubscribeMethod, u *URI) *Offer {
		return &Offer{Method: m, Target: u.Target, URI: u.Raw}
	}
	if kind == api.BulkList && mailto != nil {
		return mk(api.UnsubscribeMailto, mailto)
	}
	switch {
	case oneClick:
		return mk(api.UnsubscribeOneClick, https)
	case mailto != nil:
		return mk(api.UnsubscribeMailto, mailto)
	case https != nil:
		return mk(api.UnsubscribeURL, https)
	}
	return nil
}

// MailtoAlternative is the first usable mailto: URI of the headers as an
// Offer (method mailto), whatever method Choose prefers; nil when there is
// none.
func MailtoAlternative(h map[string]string) *Offer {
	for _, u := range UnsubscribeURIs(get(h, HeaderListUnsubscribe)) {
		if u.Kind == KindMailto {
			return &Offer{Method: api.UnsubscribeMailto, Target: u.Target, URI: u.Raw}
		}
	}
	return nil
}

// RememberKey is the key of the remembered unsubscription of a message:
// the list when it has a List-Id, else the sender address.
func RememberKey(listID, fromAddress string) string {
	if listID != "" {
		return "list:" + listID
	}
	return "from:" + strings.ToLower(strings.TrimSpace(fromAddress))
}

func precedence(h map[string]string) string {
	return strings.ToLower(strings.TrimSpace(firstToken(get(h, HeaderPrecedence))))
}

// fingerprinted reports a bulk-sending service's header with a value.
func fingerprinted(h map[string]string) bool {
	for _, name := range SenderFingerprints {
		if cleanValue(get(h, name)) != "" {
			return true
		}
	}
	return false
}

func autoSubmitted(h map[string]string) bool {
	v := strings.ToLower(strings.TrimSpace(firstToken(get(h, HeaderAutoSubmitted))))
	return v != "" && v != "no"
}

// firstToken is the value up to the first ";" (RFC 3834 parameters).
func firstToken(s string) string {
	s = trimScan(s)
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return s
}

// get looks a header up by name, ignoring case.
func get(h map[string]string, name string) string {
	if v, ok := h[name]; ok {
		return v
	}
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// cleanValue is a header value with surrounding space removed, "" when it
// is not valid text.
func cleanValue(s string) string {
	s = strings.TrimSpace(trimScan(s))
	if !utf8.ValidString(s) {
		return ""
	}
	return s
}

// trimScan caps a value at maxScanBytes.
func trimScan(s string) string {
	if len(s) > maxScanBytes {
		return s[:maxScanBytes]
	}
	return s
}

// plain reports whether s has no control, format (bidirectional marks,
// zero-width characters), space or invalid UTF-8 content.
func plain(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// truncate cuts s to max bytes at a character boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
