// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// foldOctets is the longest physical line written (RFC 6350 §3.2).
const foldOctets = 75

// prodID is the PRODID of the cards New writes.
const prodID = "-//Malachi Mail//vCard//EN"

// writer writes lines in the syntax of one version.
type writer struct {
	ver version
	eol string
}

// escapeText escapes a text value. vCard 2.1 escapes only "\" and ";"
// and leaves line breaks to quoted-printable.
func (w writer) escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			if w.ver == v21 {
				b.WriteByte(c)
			} else {
				b.WriteString(`\,`)
			}
		case '\n':
			if w.ver == v21 {
				b.WriteByte(c)
			} else {
				b.WriteString(`\n`)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// escapeURI escapes a URI value: only a backslash (the parser reads the
// text escapes in every value).
func escapeURI(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// typeParam returns the parameter that writes a standard label.
func (w writer) typeParam(label string) string {
	tok := map[string]string{LabelHome: "HOME", LabelWork: "WORK", LabelMobile: "CELL", LabelFax: "FAX", LabelOther: "OTHER"}[label]
	switch {
	case tok == "":
		return ""
	case w.ver == v21:
		return ";" + tok
	case w.ver == v40:
		return ";TYPE=" + strings.ToLower(tok)
	}
	return ";TYPE=" + tok
}

func hasNonASCIIOrNewline(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] == '\n' {
			return true
		}
	}
	return false
}

// line writes one property: params are raw parameter texts (each with its
// leading ";"), value is the escaped value. Unless raw, a vCard 2.1 value
// with non-ASCII text or a line break is written quoted-printable. Lines
// are folded at 75 octets, never inside a UTF-8 sequence; quoted-printable
// lines are not folded.
func (w writer) line(group, name string, params []string, value string, raw bool) string {
	var b strings.Builder
	if group != "" {
		b.WriteString(group)
		b.WriteByte('.')
	}
	b.WriteString(name)
	qp := false
	for _, p := range params {
		b.WriteString(p)
		if strings.Contains(strings.ToUpper(p), "QUOTED-PRINTABLE") {
			qp = true
		}
	}
	if !raw && w.ver == v21 && hasNonASCIIOrNewline(value) {
		b.WriteString(";CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE")
		value = qpEncode(value)
		qp = true
	}
	b.WriteByte(':')
	b.WriteString(value)
	if qp {
		return b.String() + w.eol
	}
	return fold(b.String(), w.eol)
}

// fold folds s at foldOctets octets per physical line, a continuation
// line starting with a space, and ends it with eol.
func fold(s, eol string) string {
	if len(s) <= foldOctets {
		return s + eol
	}
	var b strings.Builder
	limit := foldOctets
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			cut = limit
		}
		b.WriteString(s[:cut])
		b.WriteString(eol)
		b.WriteByte(' ')
		s = s[cut:]
		limit = foldOctets - 1
	}
	b.WriteString(s)
	b.WriteString(eol)
	return b.String()
}

// birthday formats a birthday; apple is the year Apple writes for no year
// (X-APPLE-OMIT-YEAR), 0 when the line has none.
func (w writer) birthday(b Birthday, apple int) string {
	switch {
	case b.Year > 0 && w.ver == v40:
		return fmt.Sprintf("%04d%02d%02d", b.Year, b.Month, b.Day)
	case b.Year > 0:
		return fmt.Sprintf("%04d-%02d-%02d", b.Year, b.Month, b.Day)
	case w.ver == v40:
		return fmt.Sprintf("--%02d%02d", b.Month, b.Day)
	case apple > 0:
		return fmt.Sprintf("%04d-%02d-%02d", apple, b.Month, b.Day)
	}
	return fmt.Sprintf("--%02d-%02d", b.Month, b.Day)
}

// rev formats the current time for REV.
func (w writer) rev() string {
	t := now().UTC()
	if w.ver == v40 {
		return t.Format("20060102T150405Z")
	}
	return t.Format("2006-01-02T15:04:05Z")
}

func (w writer) nameValue(n Name) string {
	return strings.Join([]string{w.escapeText(n.Family), w.escapeText(n.Given), w.escapeText(n.Middle),
		w.escapeText(n.Prefix), w.escapeText(n.Suffix)}, ";")
}

// item is one list entry of the model in a common form: EMAIL, TEL and URL
// use v[0], ADR v[0..4] (street, city, region, postcode, country).
type item struct {
	label string
	v     [5]string
}

// itemKind describes one list property.
type itemKind struct {
	name string // EMAIL, TEL, ADR, URL
}

var (
	kindEmail = itemKind{"EMAIL"}
	kindTel   = itemKind{"TEL"}
	kindAdr   = itemKind{"ADR"}
	kindURL   = itemKind{"URL"}
)

// value writes the value of it; prefix is the raw first components of an
// ADR to keep (post office box, extended address, and any after the
// country), or a "mailto:"/"tel:" scheme to keep.
func (w writer) itemValue(k itemKind, it item, keep []string, scheme string) string {
	switch k {
	case kindAdr:
		comps := make([]string, 7)
		copy(comps, keep)
		for i := 0; i < 5; i++ {
			comps[2+i] = w.escapeText(it.v[i])
		}
		if len(keep) > 7 {
			comps = append(comps, keep[7:]...)
		}
		return strings.Join(comps, ";")
	case kindURL:
		return escapeURI(it.v[0])
	}
	return scheme + w.escapeText(it.v[0])
}

// newItem writes a new item line (and the X-ABLabel line of a custom
// label in a new group from alloc).
func (w writer) newItem(k itemKind, it item, alloc func() string) string {
	var params []string
	if k == kindEmail && w.ver != v40 {
		if w.ver == v21 {
			params = append(params, ";INTERNET")
		} else {
			params = append(params, ";TYPE=INTERNET")
		}
	}
	group := ""
	if isCustom(it.label) {
		group = alloc()
	} else if p := w.typeParam(it.label); p != "" {
		params = append(params, p)
	}
	out := w.line(group, k.name, params, w.itemValue(k, it, nil, ""), false)
	if group != "" {
		out += w.line(group, "X-ABLabel", nil, w.escapeText(it.label), false)
	}
	return out
}

func emailItems(es []Email) []item {
	out := make([]item, len(es))
	for i, e := range es {
		out[i] = item{label: e.Label, v: [5]string{e.Address}}
	}
	return out
}

func phoneItems(ps []Phone) []item {
	out := make([]item, len(ps))
	for i, p := range ps {
		out[i] = item{label: p.Label, v: [5]string{p.Number}}
	}
	return out
}

func addressItems(as []Address) []item {
	out := make([]item, len(as))
	for i, a := range as {
		out[i] = item{label: a.Label, v: [5]string{a.Street, a.City, a.Region, a.Postcode, a.Country}}
	}
	return out
}

func urlItems(us []URL) []item {
	out := make([]item, len(us))
	for i, u := range us {
		out[i] = item{label: u.Label, v: [5]string{u.URL}}
	}
	return out
}

// New writes a new card in version "4.0" or "3.0" (any other value is
// 3.0) with the given UID, CRLF line ends and lines folded at 75 octets.
// The card is cleaned first; FN is DisplayNameOf the card and N is always
// written.
func New(card *Card, ver, uid string) []byte {
	c := Clean(card)
	w := writer{ver: v30, eol: "\r\n"}
	vs := "3.0"
	if ver == "4.0" {
		w.ver, vs = v40, "4.0"
	}
	group := 0
	alloc := func() string { group++; return fmt.Sprintf("item%d", group) }
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:" + vs + "\r\n")
	b.WriteString(w.line("", "PRODID", nil, w.escapeText(prodID), false))
	if u := cleanLine(uid, MaxUIDBytes); u != "" {
		b.WriteString(w.line("", "UID", nil, w.escapeText(u), false))
	}
	b.WriteString(w.line("", "FN", nil, w.escapeText(DisplayNameOf(c)), false))
	b.WriteString(w.line("", "N", nil, w.nameValue(c.Name), false))
	for _, f := range []struct{ name, v string }{{"NICKNAME", c.Nickname}, {"ORG", c.Org}, {"TITLE", c.Title}} {
		if f.v != "" {
			b.WriteString(w.line("", f.name, nil, w.escapeText(f.v), false))
		}
	}
	for _, it := range emailItems(c.Emails) {
		b.WriteString(w.newItem(kindEmail, it, alloc))
	}
	for _, it := range phoneItems(c.Phones) {
		b.WriteString(w.newItem(kindTel, it, alloc))
	}
	for _, it := range addressItems(c.Addresses) {
		b.WriteString(w.newItem(kindAdr, it, alloc))
	}
	for _, it := range urlItems(c.URLs) {
		b.WriteString(w.newItem(kindURL, it, alloc))
	}
	if !c.Birthday.IsZero() {
		b.WriteString(w.line("", "BDAY", nil, w.birthday(c.Birthday, 0), false))
	}
	if c.Note != "" {
		b.WriteString(w.line("", "NOTE", nil, w.escapeText(c.Note), false))
	}
	b.WriteString(w.line("", "REV", nil, w.rev(), false))
	b.WriteString("END:VCARD\r\n")
	return []byte(b.String())
}
