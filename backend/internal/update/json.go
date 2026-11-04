// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"errors"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// The envelope and the manifest are read by a small strict JSON reader
// instead of encoding/json, which would accept what the contract does not:
// it keeps the last of two equal keys, matches field names without regard
// to case ("Schema" fills schema), turns a lone surrogate escape into
// U+FFFD and decodes 1.0 or 1e3 into an integer field. The reader keeps
// every member in order and every number as its literal, so the checks in
// manifest.go see exactly what was sent.

type kind uint8

const (
	kNull kind = iota
	kBool
	kNumber
	kString
	kArray
	kObject
)

func (k kind) String() string {
	return [...]string{"null", "boolean", "number", "string", "array", "object"}[k]
}

// value is one JSON value. num is the number's literal; bad marks a string
// that held a lone surrogate escape (decoded as U+FFFD), which the text
// checks refuse with the path of the string.
type value struct {
	kind    kind
	boolean bool
	num     string
	str     string
	bad     bool
	arr     []*value
	obj     []member
}

type member struct {
	key string
	val *value
}

// maxDepth bounds nesting: the manifest is four levels deep.
const maxDepth = 16

var errJSON = errors.New("invalid JSON")

// parseJSON reads exactly one JSON value surrounded by optional
// whitespace. The input must be valid UTF-8 (checked by the caller).
func parseJSON(data []byte) (*value, error) {
	p := &parser{data: data}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.data) {
		return nil, errJSON
	}
	return v, nil
}

type parser struct {
	data []byte
	i    int
}

func (p *parser) space() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) literal(s string) bool {
	if len(p.data)-p.i < len(s) || string(p.data[p.i:p.i+len(s)]) != s {
		return false
	}
	p.i += len(s)
	return true
}

func (p *parser) value(depth int) (*value, error) {
	if depth > maxDepth || p.i >= len(p.data) {
		return nil, errJSON
	}
	switch c := p.data[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, bad, err := p.string()
		if err != nil {
			return nil, err
		}
		return &value{kind: kString, str: s, bad: bad}, nil
	case c == 't' && p.literal("true"):
		return &value{kind: kBool, boolean: true}, nil
	case c == 'f' && p.literal("false"):
		return &value{kind: kBool}, nil
	case c == 'n' && p.literal("null"):
		return &value{kind: kNull}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, errJSON
}

func (p *parser) object(depth int) (*value, error) {
	p.i++ // {
	v := &value{kind: kObject}
	p.space()
	if p.i < len(p.data) && p.data[p.i] == '}' {
		p.i++
		return v, nil
	}
	for {
		p.space()
		if p.i >= len(p.data) || p.data[p.i] != '"' {
			return nil, errJSON
		}
		key, bad, err := p.string()
		if err != nil || bad {
			return nil, errJSON
		}
		p.space()
		if p.i >= len(p.data) || p.data[p.i] != ':' {
			return nil, errJSON
		}
		p.i++
		p.space()
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.obj = append(v.obj, member{key, val})
		p.space()
		if p.i >= len(p.data) {
			return nil, errJSON
		}
		switch p.data[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return v, nil
		default:
			return nil, errJSON
		}
	}
}

func (p *parser) array(depth int) (*value, error) {
	p.i++ // [
	v := &value{kind: kArray}
	p.space()
	if p.i < len(p.data) && p.data[p.i] == ']' {
		p.i++
		return v, nil
	}
	for {
		p.space()
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.arr = append(v.arr, val)
		p.space()
		if p.i >= len(p.data) {
			return nil, errJSON
		}
		switch p.data[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return v, nil
		default:
			return nil, errJSON
		}
	}
}

// number takes the JSON number grammar and keeps the literal.
func (p *parser) number() (*value, error) {
	start := p.i
	if p.data[p.i] == '-' {
		p.i++
	}
	digits := func() int {
		n := 0
		for p.i < len(p.data) && p.data[p.i] >= '0' && p.data[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.i < len(p.data) && p.data[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		return nil, errJSON
	}
	if p.i < len(p.data) && p.data[p.i] == '.' {
		p.i++
		if digits() == 0 {
			return nil, errJSON
		}
	}
	if p.i < len(p.data) && (p.data[p.i] == 'e' || p.data[p.i] == 'E') {
		p.i++
		if p.i < len(p.data) && (p.data[p.i] == '+' || p.data[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			return nil, errJSON
		}
	}
	return &value{kind: kNumber, num: string(p.data[start:p.i])}, nil
}

// string reads a JSON string. bad: it held a lone surrogate escape.
func (p *parser) string() (s string, bad bool, err error) {
	p.i++ // "
	var out []byte
	for {
		if p.i >= len(p.data) {
			return "", false, errJSON
		}
		c := p.data[p.i]
		switch {
		case c == '"':
			p.i++
			return string(out), bad, nil
		case c < 0x20:
			return "", false, errJSON
		case c == '\\':
			p.i++
			if p.i >= len(p.data) {
				return "", false, errJSON
			}
			e := p.data[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				out = append(out, e)
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				r, ok := p.hex4()
				if !ok {
					return "", false, errJSON
				}
				switch {
				case utf16.IsSurrogate(r) && r < 0xDC00:
					// A high surrogate must be followed by \u and a low one.
					save := p.i
					if p.literal(`\u`) {
						if r2, ok := p.hex4(); ok && r2 >= 0xDC00 && r2 <= 0xDFFF {
							out = utf8.AppendRune(out, utf16.DecodeRune(r, r2))
							continue
						}
					}
					p.i = save
					bad = true
					out = utf8.AppendRune(out, utf8.RuneError)
				case utf16.IsSurrogate(r):
					bad = true
					out = utf8.AppendRune(out, utf8.RuneError)
				default:
					out = utf8.AppendRune(out, r)
				}
			default:
				return "", false, errJSON
			}
		default:
			out = append(out, c)
			p.i++
		}
	}
}

func (p *parser) hex4() (rune, bool) {
	if len(p.data)-p.i < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(string(p.data[p.i:p.i+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	p.i += 4
	return rune(n), true
}
