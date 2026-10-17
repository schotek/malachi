// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import "strings"

// maxAddressBytes caps an e-mail address read from a CAL-ADDRESS.
const maxAddressBytes = 254

// CalAddress is a CAL-ADDRESS value (ORGANIZER, ATTENDEE). URI is the value
// as written. Email is set only for a well-formed mailto: URI: the address
// with the domain lower-cased and the local part as given (compare with
// SameEmail, which ignores case of the whole address, as mail systems do in
// practice). Any other URI (http:, urn:, tel:, …) stays opaque: Email is ""
// and nothing should ever contact it.
type CalAddress struct {
	URI   string
	Email string
}

// CalAddress reads the value as a CAL-ADDRESS.
func (p *Property) CalAddress() CalAddress { return ParseCalAddress(p.val()) }

// ParseCalAddress reads a CAL-ADDRESS; it never fails, a value that is not
// a usable mailto: address just has no Email.
func ParseCalAddress(v string) CalAddress {
	a := CalAddress{URI: v}
	s := strings.TrimSpace(v)
	if len(s) < 7 || !strings.EqualFold(s[:7], "mailto:") {
		return a
	}
	s = s[7:]
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || len(s) > maxAddressBytes || strings.Count(s, "@") != 1 {
		return a
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == 0xfffd || strings.ContainsRune(`<>"(),;:[]\`, r) {
			return a
		}
	}
	domain := s[at+1:]
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return a
	}
	a.Email = s[:at] + "@" + strings.ToLower(domain)
	return a
}

// IsEmail reports whether the address is a usable mailto: address.
func (a CalAddress) IsEmail() bool { return a.Email != "" }

// SameEmail compares the address with an e-mail address, ignoring case.
// An address without Email is never equal to anything.
func (a CalAddress) SameEmail(addr string) bool {
	return a.Email != "" && strings.EqualFold(a.Email, strings.TrimSpace(addr))
}
