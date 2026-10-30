// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxSortKeyBytes caps a sort key.
const maxSortKeyBytes = 256

// DisplayNameOf returns the name to show for a card: FN, else the parts of
// N (prefix, given, middle, family, suffix), else the nickname, the
// organisation or the first email address; "" when the card has none.
func DisplayNameOf(c *Card) string {
	if c == nil {
		return ""
	}
	if s := cleanLine(c.DisplayName, MaxFieldBytes); s != "" {
		return s
	}
	var parts []string
	for _, p := range []string{c.Name.Prefix, c.Name.Given, c.Name.Middle, c.Name.Family, c.Name.Suffix} {
		if p = cleanLine(p, MaxFieldBytes); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		return capBytes(strings.Join(parts, " "), MaxFieldBytes)
	}
	for _, s := range []string{c.Nickname, c.Org} {
		if s = cleanLine(s, MaxFieldBytes); s != "" {
			return s
		}
	}
	for _, e := range c.Emails {
		if a := cleanEmail(e.Address); a != "" {
			return a
		}
	}
	return ""
}

// SortKey returns the key that orders contacts: "family given" when the
// card has either, else the organisation, FN, nickname or first email;
// lower case, without diacritics (NFD, combining marks dropped, ß → ss,
// ø → o, ł → l, đ → d, æ → ae, œ → oe), whitespace collapsed and leading
// punctuation dropped. Byte order of keys (SQLite's default BINARY
// collation) is the order of the address book.
func SortKey(c *Card) string {
	if c == nil {
		return ""
	}
	var src string
	if f, g := cleanLine(c.Name.Family, MaxFieldBytes), cleanLine(c.Name.Given, MaxFieldBytes); f != "" || g != "" {
		src = strings.TrimSpace(f + " " + g)
	} else {
		for _, s := range []string{c.Org, c.DisplayName, c.Nickname} {
			if s = cleanLine(s, MaxFieldBytes); s != "" {
				src = s
				break
			}
		}
		if src == "" {
			for _, e := range c.Emails {
				if src = cleanEmail(e.Address); src != "" {
					break
				}
			}
		}
	}
	return foldKey(src)
}

var foldSpecial = strings.NewReplacer("ß", "ss", "ø", "o", "ł", "l", "đ", "d", "æ", "ae", "œ", "oe", "þ", "th", "ı", "i")

func foldKey(s string) string {
	s = foldSpecial.Replace(strings.ToLower(norm.NFD.String(s)))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case b.Len() == 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && hasAlnum(s):
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return norm.NFC.String(capBytes(b.String(), maxSortKeyBytes))
}

func hasAlnum(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// Section returns the section a sort key belongs to: its first letter in
// upper case, "#" for a digit, punctuation or an empty key.
func Section(sortKey string) string {
	for _, r := range sortKey {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
		return "#"
	}
	return "#"
}
