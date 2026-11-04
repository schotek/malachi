// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// foldOctets is the longest line Encode writes, CRLF not counted.
const foldOctets = 75

// Encode writes the object's calendars: CRLF line endings, lines folded at
// 75 octets on UTF-8 boundaries, each component's properties in order and
// then its child components. Values are written as they are stored (raw);
// parameter values are quoted when they contain ",", ";" or ":".
// Problems and Truncated are not written. For every Object that Parse
// returned without problems, Parse(Encode(o)) has equal Calendars.
func Encode(o *Object) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, ErrMalformed
		}
	}()
	var b bytes.Buffer
	if o != nil {
		for _, c := range o.Calendars {
			if err := encodeComponent(&b, c, 1); err != nil {
				return nil, err
			}
		}
	}
	return b.Bytes(), nil
}

// EncodeComponent writes one component (usually a VCALENDAR) like Encode.
func EncodeComponent(c *Component) (out []byte, err error) {
	return Encode(&Object{Calendars: []*Component{c}})
}

func encodeComponent(b *bytes.Buffer, c *Component, depth int) error {
	if c == nil {
		return fmt.Errorf("%w: nil component", ErrInvalid)
	}
	if depth > MaxDepth {
		return fmt.Errorf("%w: components nested deeper than %d", ErrInvalid, MaxDepth)
	}
	if !validName(c.Name) {
		return fmt.Errorf("%w: component name", ErrInvalid)
	}
	name := strings.ToUpper(c.Name)
	writeLine(b, "BEGIN:"+name)
	for i := range c.Props {
		line, err := contentLine(&c.Props[i])
		if err != nil {
			return err
		}
		writeLine(b, line)
	}
	for _, ch := range c.Components {
		if err := encodeComponent(b, ch, depth+1); err != nil {
			return err
		}
	}
	writeLine(b, "END:"+name)
	return nil
}

func contentLine(p *Property) (string, error) {
	if !validName(p.Name) {
		return "", fmt.Errorf("%w: property name", ErrInvalid)
	}
	name := strings.ToUpper(p.Name)
	if name == "BEGIN" || name == "END" {
		return "", fmt.Errorf("%w: BEGIN or END as a property", ErrInvalid)
	}
	if len(p.Params) > MaxParams {
		return "", fmt.Errorf("%w: more than %d parameters of %s", ErrInvalid, MaxParams, name)
	}
	var sb strings.Builder
	sb.WriteString(name)
	for _, pa := range p.Params {
		if !validName(pa.Name) {
			return "", fmt.Errorf("%w: parameter name of %s", ErrInvalid, name)
		}
		sb.WriteByte(';')
		sb.WriteString(strings.ToUpper(pa.Name))
		sb.WriteByte('=')
		values := pa.Values
		if len(values) == 0 {
			values = []string{""}
		}
		for i, v := range values {
			if i > 0 {
				sb.WriteByte(',')
			}
			if strings.IndexByte(v, '"') >= 0 || hasControl(v) || !utf8.ValidString(v) {
				return "", fmt.Errorf("%w: parameter value of %s", ErrInvalid, name)
			}
			if strings.ContainsAny(v, ",;:") {
				sb.WriteByte('"')
				sb.WriteString(v)
				sb.WriteByte('"')
			} else {
				sb.WriteString(v)
			}
		}
	}
	if strings.ContainsAny(p.Value, "\r\n\x00") || !utf8.ValidString(p.Value) {
		return "", fmt.Errorf("%w: value of %s", ErrInvalid, name)
	}
	sb.WriteByte(':')
	sb.WriteString(p.Value)
	if sb.Len() > MaxLineBytes {
		return "", fmt.Errorf("%w: content line of %s over %d bytes", ErrInvalid, name, MaxLineBytes)
	}
	return sb.String(), nil
}

// writeLine writes one logical line folded at 75 octets, never inside a
// UTF-8 sequence; continuation lines start with a space.
func writeLine(b *bytes.Buffer, s string) {
	limit := foldOctets
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 { // cannot happen with valid UTF-8 (runes ≤ 4 bytes)
			cut = limit
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = foldOctets - 1
	}
	b.WriteString(s)
	b.WriteString("\r\n")
}
