// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"net/mail"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Every string from eWay-CRM is untrusted display text: cleaned of
// control, format, bidi and invisible characters, whitespace collapsed,
// capped (calendar.CleanLine, the board's rules).

// cleanName cleans an item's name.
func cleanName(s string) string { return calendar.CleanLine(s, api.MaxCrmNameBytes) }

// cleanDetail cleans an item's detail line.
func cleanDetail(s string) string { return calendar.CleanLine(s, api.MaxCrmDetailBytes) }

// cleanGUID is s lower-cased when it has a GUID's shape, else "".
func cleanGUID(s string) string {
	s = strings.TrimSpace(s)
	if !ValidGUID(s) {
		return ""
	}
	return strings.ToLower(s)
}

// maxAddressBytes caps an e-mail address read from the CRM or sent to it.
const maxAddressBytes = 320

// CleanEmail is a bare address lower-cased, or "" when s is not one.
func CleanEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxAddressBytes || strings.ContainsAny(s, " \t\r\n<>\",;") {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || !strings.Contains(s, "@") {
		return ""
	}
	for _, r := range s {
		if !unicode.IsGraphic(r) || unicode.In(r, unicode.Cf, unicode.Cc, unicode.Zs, unicode.Bidi_Control, unicode.Noncharacter_Code_Point) {
			return ""
		}
	}
	return strings.ToLower(s)
}

// DomainOf is the lower-case domain of an address, "" without one.
func DomainOf(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 || i == len(addr)-1 {
		return ""
	}
	return strings.ToLower(addr[i+1:])
}

// freemail domains are never matched against companies' tracked domains:
// they say nothing about the company a sender works for.
var freemail = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true,
	"live.com": true, "msn.com": true, "yahoo.com": true, "ymail.com": true,
	"icloud.com": true, "me.com": true, "mac.com": true, "aol.com": true,
	"proton.me": true, "protonmail.com": true, "gmx.com": true, "gmx.net": true,
	"gmx.de": true, "web.de": true, "mail.com": true, "yandex.com": true,
	"seznam.cz": true, "email.cz": true, "post.cz": true, "centrum.cz": true,
	"atlas.cz": true, "volny.cz": true, "tiscali.cz": true, "azet.sk": true,
	"zoznam.sk": true, "centrum.sk": true, "wp.pl": true, "o2.pl": true,
	"onet.pl": true, "interia.pl": true, "fastmail.com": true, "zoho.com": true,
	"tutanota.com": true, "mail.ru": true, "qq.com": true, "163.com": true,
}

// Freemail reports a public mail provider's domain.
func Freemail(domain string) bool { return freemail[strings.ToLower(domain)] }

// likeValue strips the LIKE wildcards and the bracket of a pattern from a
// user's search text, so it is matched as written.
func likeValue(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch r {
		case '%', '_', '[', ']':
			return ' '
		}
		return r
	}, s)), " ")
}

// capBytesAt cuts a list joined by sep to at most n bytes at an element
// boundary.
func joinCapped(items []string, sep string, n int) string {
	var b strings.Builder
	for _, it := range items {
		add := len(it)
		if b.Len() > 0 {
			add += len(sep)
		}
		if b.Len()+add > n {
			break
		}
		if b.Len() > 0 {
			b.WriteString(sep)
		}
		b.WriteString(it)
	}
	return b.String()
}
