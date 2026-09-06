// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package compose is the "New Message" window: recipients, subject,
// attachments, the rich-text editor and the draft lifecycle around them.
// Mail logic stays in the backend; this package parses what the user typed
// into the wire types and shows what the backend answers.
package compose

import (
	"net/mail"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ParseAddressList splits "Name <a@b>, c@d; e@f" into addresses. Tokens
// that do not parse are returned in invalid so the row can be flagged. The
// backend validates authoritatively; this is only immediate feedback.
func ParseAddressList(s string) (addrs []api.Address, invalid []string) {
	for _, tok := range splitAddresses(s) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		a, err := mail.ParseAddress(tok)
		if err != nil {
			invalid = append(invalid, tok)
			continue
		}
		addrs = append(addrs, api.Address{Name: a.Name, Address: a.Address})
	}
	return addrs, invalid
}

// splitAddresses splits on commas and semicolons that are outside quotes
// and angle brackets.
func splitAddresses(s string) []string {
	ranges := splitAddressRanges(s)
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, s[r.start:r.end])
	}
	return out
}

// span is a byte range [start, end) of s.
type span struct{ start, end int }

// splitAddressRanges is splitAddresses with positions: one range per
// token, separators excluded, the last one running to the end of s. The
// completion uses it to find the token under the caret with the same
// rules the parser applies.
func splitAddressRanges(s string) []span {
	var (
		out     []span
		start   int
		quoted  bool
		angled  bool
		escaped bool
	)
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			angled = true
		case r == '>':
			angled = false
		case (r == ',' || r == ';') && !angled:
			out = append(out, span{start, i})
			start = i + 1
		}
	}
	return append(out, span{start, len(s)})
}

// FormatAddressList is the inverse of ParseAddressList for prefilled rows:
// "Name <addr>, addr". Names containing separators or quotes are quoted;
// non-ASCII names are left readable (this is UI text, not a header).
func FormatAddressList(addrs []api.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		name := strings.TrimSpace(a.Name)
		switch {
		case name == "":
			parts = append(parts, a.Address)
		case strings.ContainsAny(name, `,;<>"\`):
			parts = append(parts, `"`+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)+`" <`+a.Address+`>`)
		default:
			parts = append(parts, name+" <"+a.Address+">")
		}
	}
	return strings.Join(parts, ", ")
}
