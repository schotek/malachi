// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"net/url"
	"strings"
)

// urlClass is what a URL-valued attribute turned out to be after
// normalisation. The policy decisions in walk.go key on it.
type urlClass int

const (
	urlRelative  urlClass = iota // no scheme: nothing the view could resolve
	urlHTTP                      // plain http: never kept, images or links alike
	urlHTTPS                     // remote; kept for links, policy-dependent for images
	urlMailto                    // links only
	urlCID                       // a MIME part of this message
	urlLocal                     // malachi-cid:, the view's own scheme (output fed back in)
	urlData                      // inline data: never from the message itself
	urlDangerous                 // javascript:, vbscript: and every other scheme
	urlInvalid                   // control characters or over the length cap
)

// classifyURL normalises an attribute value the way an HTML URL parser
// would — strip surrounding C0 controls and spaces, remove tab, LF and CR
// anywhere — and classifies the scheme without regard to case. That is what
// defeats "jav&#x09;ascript:" and "  JAVASCRIPT:": the entity was decoded by
// the HTML parser, the tab is gone, the case is folded. A control character
// that survives makes the value invalid outright: browsers disagree on what
// "java\x00script:" means, so the only safe reading is "not a URL we keep".
func classifyURL(raw string) (string, urlClass) {
	v := strings.TrimFunc(raw, func(r rune) bool { return r <= ' ' })
	v = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
	if len(v) > maxURLLength {
		return "", urlInvalid
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", urlInvalid
		}
	}
	i := strings.IndexByte(v, ':')
	if i <= 0 || !validScheme(v[:i]) {
		return v, urlRelative
	}
	scheme := strings.ToLower(v[:i])
	v = scheme + v[i:]
	switch scheme {
	case "http":
		return v, urlHTTP
	case "https":
		return v, urlHTTPS
	case "mailto":
		return v, urlMailto
	case "cid":
		return v, urlCID
	case "malachi-cid":
		return v, urlLocal
	case "data":
		return v, urlData
	}
	return v, urlDangerous
}

// validScheme is RFC 3986: a letter, then letters, digits, +, - or . — so
// "foo/bar:baz" and "?x=1:2" are relative references, not schemes.
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if i == 0 && !alpha {
			return false
		}
		if !alpha && !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// contentID is the Content-ID a cid: URL names, in the form the MIME layer
// stores it: percent-decoded (RFC 2392) and without angle brackets.
func contentID(cidURL string) string {
	id := strings.TrimPrefix(cidURL, "cid:")
	if dec, err := url.PathUnescape(id); err == nil {
		id = dec
	}
	return strings.Trim(strings.TrimSpace(id), "<>")
}

// safeImageType accepts the media type of a fetched or inlined image:
// image/* except SVG, which is a document format with scripts and external
// references of its own. The type is a token, nothing else.
func safeImageType(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if !strings.HasPrefix(mt, "image/") || mt == "image/svg+xml" || len(mt) > 64 {
		return false
	}
	for _, r := range mt {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == '-' || r == '+' || r == '.') {
			return false
		}
	}
	return true
}
