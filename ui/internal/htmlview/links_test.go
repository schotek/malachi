// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"strings"
	"testing"
)

func TestMasked(t *testing.T) {
	cases := []struct {
		text, href string
		want       bool
	}{
		{"https://bank.example.org/", "https://evil.example.net/login", true},
		{"bank.example.org", "https://evil.example.net/", true},
		{"www.bank.example.org/account", "http://evil.example.net", true},
		{"https://example.org", "https://example.org/x", false},
		{"example.org", "https://www.example.org/", false},
		{"www.example.org", "https://example.org/", false},
		{"docs.example.org", "https://example.org/docs", false},
		{"example.org", "https://docs.example.org/", false},
		{"bank.example.org", "https://evil.example.org/", true},
		{"click here", "https://evil.example.net/", false},
		{"Read more", "https://example.org/", false},
		{"v1.2", "https://example.org/", false},
		{"e.g.", "https://example.org/", false},
		{"", "https://example.org/", false},
		{"https://example.org", "mailto:a@example.net", false},
		{"support@example.org", "https://evil.example.net/", false},
	}
	for _, c := range cases {
		if got := Masked(c.text, c.href); got != c.want {
			t.Errorf("Masked(%q, %q) = %v, want %v", c.text, c.href, got, c.want)
		}
	}
}

func TestAllowedLink(t *testing.T) {
	for _, ok := range []string{"https://x/", "HTTP://x", "mailto:a@b"} {
		if !AllowedLink(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"javascript:x", "ftp://x", "data:text/html,x", "", "malachi-cid:a/b/1", "cid:x"} {
		if AllowedLink(bad) {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestParsePath(t *testing.T) {
	acc, msg, part, ok := ParsePath("acc_1a2b/m_3c4d/1.2")
	if !ok || acc != "acc_1a2b" || msg != "m_3c4d" || part != "1.2" {
		t.Fatalf("ParsePath = %q %q %q %v", acc, msg, part, ok)
	}
	for _, bad := range []string{"", "a/b", "a/b/c/d", "/b/1", "a//1", "a/b/x", "a/b/1..2", "../etc/1", "a/b/" + strings.Repeat("1", 200), "a b/c/1", "a/b/1?x=1"} {
		if _, _, _, ok := ParsePath(bad); ok {
			t.Errorf("ParsePath(%q) accepted", bad)
		}
	}
}

func TestImageType(t *testing.T) {
	for _, ok := range []string{"image/png", "IMAGE/JPEG", "image/gif; charset=binary"} {
		if !ImageType(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"image/svg+xml", "text/html", "application/pdf", ""} {
		if ImageType(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDocument(t *testing.T) {
	doc := Document(`<p>a &amp; b</p>`)
	for _, want := range []string{CSP, `<meta charset="utf-8">`, `<body><p>a &amp; b</p></body>`, "img { max-width: 100%; }"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q:\n%s", want, doc)
		}
	}
	if !strings.HasPrefix(doc, "<!DOCTYPE html>") {
		t.Errorf("document has no doctype")
	}
}
