// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"net/url"
	"strings"
)

// Pure helpers around links and part references, testable without a
// display.

// AllowedLink reports whether a link target may be handed to the desktop
// or the composer: http, https or mailto, nothing else. The sanitiser only
// lets those through, so this is a second look, not the first.
func AllowedLink(uri string) bool {
	lower := strings.ToLower(uri)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

// Masked reports whether a link's visible text reads as a web address of a
// different site than the link really leads to — "https://bank.example"
// over a link to evil.example — which is the shape of a phishing link. Text
// that is not an address ("click here") is never masked.
func Masked(text, href string) bool {
	shown := hostOfText(text)
	if shown == "" {
		return false
	}
	target, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return false
	}
	real := strings.ToLower(target.Hostname())
	if real == "" {
		return false
	}
	return !sameSite(shown, real)
}

// hostOfText is the host name the text claims, "" when the text is not an
// address: a scheme, a www. prefix, or a bare host with a dot and an
// alphabetic top-level label.
func hostOfText(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" || strings.ContainsAny(t, " \t\n") {
		return ""
	}
	if !strings.Contains(t, "://") {
		if strings.Contains(t, "@") {
			return "" // an e-mail address, not a web address
		}
		t = "http://" + t
	}
	u, err := url.Parse(t)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if !looksLikeHost(host) {
		return ""
	}
	return host
}

func looksLikeHost(h string) bool {
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// sameSite treats a host and its subdomains as one site, www. aside.
func sameSite(a, b string) bool {
	a = strings.TrimPrefix(a, "www.")
	b = strings.TrimPrefix(b, "www.")
	return a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

// ParsePath splits the path of a malachi-cid: URL into the account, message
// and part it names. Every segment is a generated id or a part number;
// anything else is refused before it reaches the daemon.
func ParsePath(p string) (accountID, messageID, partID string, ok bool) {
	seg := strings.Split(p, "/")
	if len(seg) != 3 {
		return "", "", "", false
	}
	// Account and message ids are a prefix and hex digits; no dots, so no
	// "..".
	for _, s := range seg[:2] {
		if s == "" || len(s) > 128 {
			return "", "", "", false
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return "", "", "", false
			}
		}
	}
	// A part number: digits joined by single dots.
	if len(seg[2]) > 64 {
		return "", "", "", false
	}
	for _, n := range strings.Split(seg[2], ".") {
		if n == "" {
			return "", "", "", false
		}
		for _, r := range n {
			if r < '0' || r > '9' {
				return "", "", "", false
			}
		}
	}
	return seg[0], seg[1], seg[2], true
}

// ImageType reports whether a part's media type may be shown as a picture:
// image/* except SVG, which is a document with scripts of its own.
func ImageType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return strings.HasPrefix(ct, "image/") && ct != "image/svg+xml"
}
