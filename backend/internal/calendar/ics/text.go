// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Text returns the value as TEXT, unescaped (\n and \N a newline, \, \; \\
// themselves; any other escaped character is kept without its backslash,
// a lone trailing backslash dropped). The result is not cleaned: callers
// remove control and bidi characters before showing it.
func (p *Property) Text() string { return UnescapeText(p.val()) }

// TextList returns a multi-valued TEXT (CATEGORIES, RESOURCES), split on
// unescaped commas and unescaped.
func (p *Property) TextList() []string {
	var out []string
	start := 0
	if p == nil {
		return nil
	}
	v := p.Value
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '\\':
			i++
		case ',':
			out = append(out, UnescapeText(v[start:i]))
			start = i + 1
		}
	}
	return append(out, UnescapeText(v[start:]))
}

// Values splits a raw multi-valued value (EXDATE, RDATE) on commas.
func (p *Property) Values() []string {
	if p == nil {
		return nil
	}
	return strings.Split(p.Value, ",")
}

// URI returns the value of a URI property as text. Nothing here decides
// whether it is a link; it is never fetched.
func (p *Property) URI() string { return p.val() }

// Integer returns the value as INTEGER.
func (p *Property) Integer() (int32, error) { return ParseInteger(p.val()) }

// UnescapeText reads a TEXT value.
func UnescapeText(v string) string {
	if strings.IndexByte(v, '\\') < 0 {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i == len(v) {
			break
		}
		switch v[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

// EscapeText writes s as a TEXT value: backslash, comma and semicolon
// escaped, newlines (CRLF, LF, CR) as \n.
func EscapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', ',', ';':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\r':
			b.WriteString(`\n`)
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
		case '\n':
			b.WriteString(`\n`)
		case 0:
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ParseInteger reads an INTEGER bounded to int32 (ErrRange outside).
func ParseInteger(v string) (int32, error) {
	v = strings.TrimSpace(v)
	digits := strings.TrimLeft(v, "+-")
	if len(v)-len(digits) > 1 || digits == "" {
		return 0, fmt.Errorf("integer: %w", ErrSyntax)
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, fmt.Errorf("integer: %w", ErrSyntax)
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > math.MaxInt32 || n < math.MinInt32 {
		return 0, fmt.Errorf("integer: %w", ErrRange)
	}
	return int32(n), nil
}
