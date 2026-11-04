// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
)

// logLine is one unfolded line and the bytes of the input it came from
// (its physical lines with their terminators), so that Patch can copy it.
type logLine struct {
	start, end int
	buf        []byte
	colon      int  // index of the first ':' in buf, -1 while none
	scanned    int  // bytes of buf searched for the colon
	qp         int8 // quoted-printable header: 0 unknown, 1 yes, -1 no
}

func (l *logLine) findColon() int {
	if l.colon < 0 && l.scanned < len(l.buf) {
		if i := bytes.IndexByte(l.buf[l.scanned:], ':'); i >= 0 {
			l.colon = l.scanned + i
		}
		l.scanned = len(l.buf)
	}
	return l.colon
}

// qpSoftBreak reports whether the line is a quoted-printable value ending
// in a soft line break ("=" at the end), which vCard 2.1 continues on the
// next line without leading whitespace.
func (l *logLine) qpSoftBreak() bool {
	if len(l.buf) == 0 || l.buf[len(l.buf)-1] != '=' {
		return false
	}
	c := l.findColon()
	if c < 0 || c == len(l.buf)-1 {
		return false
	}
	if l.qp == 0 {
		// The header is before the first colon and never changes once
		// that colon is found.
		l.qp = -1
		if bytes.Contains(bytes.ToUpper(l.buf[:c]), []byte("QUOTED-PRINTABLE")) {
			l.qp = 1
		}
	}
	return l.qp == 1
}

// splitLines unfolds raw: a physical line starting with a space or a tab
// continues the previous one (that character removed, bytes joined, so a
// fold inside a UTF-8 sequence rejoins it); a quoted-printable soft break
// joins the next line. CR before LF is dropped.
func splitLines(raw []byte) []logLine {
	var out []logLine
	for i := 0; i < len(raw); {
		next := len(raw)
		text := raw[i:]
		if j := bytes.IndexByte(text, '\n'); j >= 0 {
			next = i + j + 1
			text = text[:j]
		}
		text = bytes.TrimSuffix(text, []byte{'\r'})
		if n := len(out); n > 0 {
			last := &out[n-1]
			switch {
			case last.qpSoftBreak():
				last.buf = append(last.buf[:len(last.buf)-1], text...)
				last.end = next
				i = next
				continue
			case len(text) > 0 && (text[0] == ' ' || text[0] == '\t') && len(last.buf) > 0:
				last.buf = append(last.buf, text[1:]...)
				last.end = next
				i = next
				continue
			}
		}
		out = append(out, logLine{start: i, end: next, buf: append([]byte(nil), text...), colon: -1})
		i = next
	}
	return out
}

// param is one parameter as written.
type param struct {
	raw    string   // the text, with its leading ';'
	name   string   // upper case; "" for a bare (vCard 2.1) parameter
	token  string   // a bare parameter, upper case
	values []string // values of a named parameter, quotes removed
}

// prop is one parsed content line.
type prop struct {
	ok      bool
	group   string // as written, "" when none
	rawName string // as written
	name    string // upper case
	params  []param
	value   string // raw (still escaped and encoded)
}

func isNameByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

func parseProp(content string) prop {
	var p prop
	i := 0
	for i < len(content) && content[i] != ';' && content[i] != ':' {
		i++
	}
	if i == len(content) {
		return p
	}
	tok := content[:i]
	if d := strings.LastIndexByte(tok, '.'); d >= 0 {
		p.group, tok = tok[:d], tok[d+1:]
		if p.group == "" {
			return p
		}
		for k := 0; k < len(p.group); k++ {
			if !isNameByte(p.group[k]) && p.group[k] != '.' {
				return p
			}
		}
	}
	if tok == "" || len(tok) > 64 {
		return p
	}
	for k := 0; k < len(tok); k++ {
		if !isNameByte(tok[k]) {
			return p
		}
	}
	p.rawName, p.name = tok, strings.ToUpper(tok)
	for i < len(content) && content[i] == ';' {
		j := i + 1
		quoted := false
		for j < len(content) {
			c := content[j]
			if c == '"' {
				quoted = !quoted
			} else if !quoted && (c == ';' || c == ':') {
				break
			}
			j++
		}
		p.params = append(p.params, parseParam(content[i:j]))
		i = j
	}
	if i >= len(content) || content[i] != ':' {
		return prop{}
	}
	p.value = content[i+1:]
	p.ok = true
	return p
}

func parseParam(raw string) param {
	pa := param{raw: raw}
	text := raw[1:]
	eq := strings.IndexByte(text, '=')
	if eq < 0 {
		pa.token = strings.ToUpper(strings.TrimSpace(text))
		return pa
	}
	pa.name = strings.ToUpper(strings.TrimSpace(text[:eq]))
	v := text[eq+1:]
	start, quoted := 0, false
	for k := 0; k <= len(v); k++ {
		if k == len(v) || (v[k] == ',' && !quoted) {
			pa.values = append(pa.values, strings.ReplaceAll(v[start:k], `"`, ""))
			start = k + 1
			continue
		}
		if v[k] == '"' {
			quoted = !quoted
		}
	}
	return pa
}

// isEncodingToken reports a bare parameter that is an encoding, not a type.
func isEncodingToken(t string) bool {
	switch t {
	case "QUOTED-PRINTABLE", "BASE64", "B", "8BIT", "7BIT":
		return true
	}
	return false
}

// isTypeParam reports a parameter that carries types: TYPE=… or a bare
// vCard 2.1 parameter other than an encoding.
func (pa param) isTypeParam() bool {
	return pa.name == "TYPE" || (pa.name == "" && pa.token != "" && !isEncodingToken(pa.token))
}

// isEncodingParam reports ENCODING, CHARSET or a bare encoding.
func (pa param) isEncodingParam() bool {
	return pa.name == "ENCODING" || pa.name == "CHARSET" || (pa.name == "" && isEncodingToken(pa.token))
}

// types returns the lower-case types of p.
func (p prop) types() []string {
	var out []string
	for _, pa := range p.params {
		if !pa.isTypeParam() {
			continue
		}
		if pa.name == "" {
			out = append(out, strings.ToLower(pa.token))
			continue
		}
		for _, v := range pa.values {
			for _, t := range strings.Split(v, ",") {
				if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// paramValue returns the first value of the named parameter.
func (p prop) paramValue(name string) (string, bool) {
	for _, pa := range p.params {
		if pa.name == name && len(pa.values) > 0 {
			return strings.TrimSpace(pa.values[0]), true
		}
	}
	return "", false
}

func (p prop) encoding() string {
	for _, pa := range p.params {
		if pa.name == "ENCODING" && len(pa.values) > 0 {
			return strings.ToUpper(strings.TrimSpace(pa.values[0]))
		}
		if pa.name == "" && isEncodingToken(pa.token) {
			return pa.token
		}
	}
	return ""
}

// decoded returns the value of p with its transfer encoding and charset
// undone, as valid UTF-8 (still escaped); false when the line is too long
// or its encoding cannot be read.
func (p prop) decoded() (string, bool) {
	if len(p.value) > MaxLineBytes {
		return "", false
	}
	v := p.value
	switch p.encoding() {
	case "QUOTED-PRINTABLE":
		v = qpDecode(v)
	case "B", "BASE64":
		s := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, v)
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err != nil {
				return "", false
			}
		}
		v = string(b)
	}
	if cs, ok := p.paramValue("CHARSET"); ok {
		v = toUTF8(v, cs)
	}
	if !utf8.ValidString(v) {
		v = strings.ToValidUTF8(v, "�")
	}
	return v, true
}

// toUTF8 converts s from the named charset, best effort: an unknown
// charset or a failed conversion leaves s for ToValidUTF8.
func toUTF8(s, charset string) string {
	switch strings.ToLower(charset) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return s
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return s
	}
	out, err := enc.NewDecoder().String(s)
	if err != nil {
		return s
	}
	return out
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// qpDecode decodes quoted-printable; a malformed "=" stays as it is.
func qpDecode(s string) string {
	if strings.IndexByte(s, '=') < 0 {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '=' && i+2 < len(s) {
			h, ok1 := unhex(s[i+1])
			l, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b = append(b, h<<4|l)
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

// qpEncode encodes s for a vCard 2.1 line: printable ASCII but "=" as it
// is, a line break as "=0D=0A", everything else "=XX".
func qpEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n':
			b.WriteString("=0D=0A")
		case (c > ' ' && c < 0x7f && c != '=') || (c == ' ' && i < len(s)-1):
			b.WriteByte(c)
		default:
			b.WriteByte('=')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// unescape undoes the text escapes \n \N \\ \, \; and Apple's \:; any
// other backslash stays.
func unescape(s string) string {
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		case '\\', ',', ';', ':':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// splitComponents splits a structured value at the semicolons that are
// not escaped; the components stay escaped.
func splitComponents(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case ';':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// component returns the unescaped component i of comps, "" when missing.
func component(comps []string, i int) string {
	if i < len(comps) {
		return unescape(comps[i])
	}
	return ""
}
