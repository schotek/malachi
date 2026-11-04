// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import (
	"io"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The vCard side of a search reply. Address books are servers' data, so a
// card is treated like any other network input: the parser reads only the
// few properties completion needs (FN, N, NICKNAME, EMAIL), tolerates the
// dialects EDS emits (2.1 with quoted-printable, 3.0, 4.0), and gives up
// on anything oversized or malformed instead of guessing.

const (
	// maxVCardBytes is the size past which a card is dropped unread. Cards
	// with an embedded photo run to tens of kilobytes; a megabyte is not a
	// contact.
	maxVCardBytes = 1 << 20
	// maxEmailsPerCard caps the addresses one card contributes.
	maxEmailsPerCard = 8
	// maxNameBytes caps a display name, like the rest of the backend does.
	maxNameBytes = 256
)

// parseVCard extracts the display name and the e-mail addresses of one
// vCard. ok is false when the input is not a vCard or has no usable
// address. The name is FN, else the N components, else NICKNAME, and ""
// when none survives cleaning. Addresses are normalised and validated;
// the preferred one comes first. A second card glued after END:VCARD is
// ignored.
func parseVCard(raw string) (name string, emails []string, ok bool) {
	if len(raw) > maxVCardBytes {
		return "", nil, false
	}
	var (
		began, ended bool
		fn, nick     string
		given, fam   string
		preferred    []string
		others       []string
		seen         = map[string]bool{}
	)
	for _, line := range unfold(raw) {
		p, ok := parseProperty(line)
		if !ok {
			continue
		}
		switch p.name {
		case "BEGIN":
			began = began || strings.EqualFold(p.value, "VCARD")
		case "END":
			if strings.EqualFold(p.value, "VCARD") {
				ended = true
			}
		case "FN":
			if fn == "" {
				fn = unescapeText(p.value)
			}
		case "N":
			if given == "" && fam == "" {
				parts := splitUnescaped(p.value, ';')
				if len(parts) > 0 {
					fam = unescapeText(parts[0])
				}
				if len(parts) > 1 {
					given = unescapeText(parts[1])
				}
			}
		case "NICKNAME":
			if nick == "" {
				nick = unescapeText(splitUnescaped(p.value, ',')[0])
			}
		case "EMAIL":
			addr, valid := validAddress(unescapeText(p.value))
			if !valid || seen[addr] || len(seen) >= maxEmailsPerCard {
				continue
			}
			seen[addr] = true
			if p.preferred {
				preferred = append(preferred, addr)
			} else {
				others = append(others, addr)
			}
		}
		if ended {
			break
		}
	}
	if !began {
		return "", nil, false
	}
	emails = append(preferred, others...)
	if len(emails) == 0 {
		return "", nil, false
	}
	name = fn
	if name == "" {
		name = strings.TrimSpace(given + " " + fam)
	}
	if name == "" {
		name = nick
	}
	return cleanName(name), emails, true
}

// property is one logical line of a vCard, decoded.
type property struct {
	name      string // upper-cased, group prefix removed
	value     string // transfer decoding (quoted-printable) undone; text escapes not
	preferred bool   // TYPE=PREF, PREF (2.1) or PREF=n
}

// parseProperty splits "group.NAME;PARAM=V;PARAM:value". The colon is
// searched outside double quotes, since a parameter value may be quoted.
func parseProperty(line string) (property, bool) {
	colon := -1
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon <= 0 {
		return property{}, false
	}
	head, value := line[:colon], line[colon+1:]
	parts := strings.Split(head, ";")
	name := strings.ToUpper(strings.TrimSpace(parts[0]))
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	if name == "" {
		return property{}, false
	}
	p := property{name: name}
	qp := false
	for _, param := range parts[1:] {
		k, v, hasValue := strings.Cut(param, "=")
		k = strings.ToUpper(strings.TrimSpace(k))
		v = strings.ToUpper(strings.Trim(strings.TrimSpace(v), `"`))
		switch {
		case k == "ENCODING" && v == "QUOTED-PRINTABLE":
			qp = true
		case k == "PREF" && (!hasValue || v == "1"):
			p.preferred = true
		case k == "TYPE" && hasValue:
			for _, t := range strings.Split(v, ",") {
				if strings.TrimSpace(t) == "PREF" {
					p.preferred = true
				}
			}
		case !hasValue && k == "PREF":
			p.preferred = true
		}
	}
	if qp {
		if decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(value))); err == nil {
			value = string(decoded)
		}
	}
	p.value = value
	return p, true
}

// unfold joins continuation lines (a line starting with space or tab
// continues the previous one) and strips CR. A quoted-printable soft break
// in a 2.1 card ("=" at the end of the line, next line not indented) is
// joined too, so the decoder sees the whole value.
func unfold(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		n := len(out)
		switch {
		case n > 0 && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			out[n-1] += line[1:]
		case n > 0 && strings.HasSuffix(out[n-1], "=") && strings.Contains(strings.ToUpper(out[n-1]), "QUOTED-PRINTABLE"):
			out[n-1] += "\r\n" + line
		default:
			out = append(out, line)
		}
	}
	return out
}

// splitUnescaped splits on sep except where a backslash precedes it.
func splitUnescaped(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++
		case s[i] == sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// unescapeText decodes the vCard text escapes \, \; \n \N and \\.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		case ',', ';', '\\':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// validAddress normalises a bare address and requires it to parse as one,
// so a card cannot smuggle a display name or a comment into the address.
func validAddress(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	parsed, err := mail.ParseAddress(s)
	if err != nil || parsed.Address != s {
		return "", false
	}
	return s, true
}

// cleanName keeps a display name only when it is short, valid UTF-8 and
// free of control characters; a newline from an escaped \n counts as one.
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxNameBytes || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return ""
	}
	return s
}
