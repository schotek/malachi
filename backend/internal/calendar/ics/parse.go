// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Parse reads an iCalendar stream. It is lenient: everything short of an
// input over MaxBytes yields an Object, with what it tolerated or dropped
// in Problems. It never panics; a bug becomes ErrMalformed.
func Parse(data []byte) (obj *Object, err error) {
	if len(data) > MaxBytes {
		return nil, ErrTooBig
	}
	defer func() {
		if r := recover(); r != nil {
			obj, err = nil, ErrMalformed
		}
	}()
	p := &parser{obj: &Object{}}
	p.run(data)
	return p.obj, nil
}

type frame struct {
	comp      *Component
	skip      bool // dropped: neither stored nor attached
	synthetic bool
	line      int
}

type parser struct {
	obj        *Object
	stack      []*frame
	components int
	properties int
	stopped    bool
	synthNoted bool
}

func (p *parser) problem(line int, code, detail string) {
	switch n := len(p.obj.Problems); {
	case n < MaxProblems:
		p.obj.Problems = append(p.obj.Problems, Problem{Line: line, Code: code, Detail: detail})
	case n == MaxProblems:
		p.obj.Problems = append(p.obj.Problems, Problem{Line: line, Code: ProblemTooManyProblems, Detail: "further problems not listed"})
	}
}

// run splits data into physical lines (CRLF, LF and CR all end one),
// unfolds them on bytes (so a UTF-8 sequence split by folding is whole
// again) and hands each logical line to line.
func (p *parser) run(data []byte) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var (
		cur     []byte
		curLine int
		tooLong bool
		have    bool
		lineNo  int
		nulSeen bool
	)
	flush := func() {
		if !have {
			return
		}
		have = false
		if tooLong {
			p.problem(curLine, ProblemLineTooLong, "content line over 64 KiB dropped")
		} else {
			p.logical(curLine, cur)
		}
		cur, tooLong = cur[:0], false
	}
	for len(data) > 0 && !p.stopped {
		lineNo++
		var phys []byte
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			phys = data[:i]
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				data = data[i+2:]
			} else {
				data = data[i+1:]
			}
		} else {
			phys, data = data, nil
		}
		if bytes.IndexByte(phys, 0) >= 0 {
			phys = bytes.ReplaceAll(phys, []byte{0}, nil)
			if !nulSeen {
				nulSeen = true
				p.problem(lineNo, ProblemNUL, "NUL bytes removed")
			}
		}
		if len(phys) == 0 {
			continue // blank lines neither end nor start a logical line
		}
		if phys[0] == ' ' || phys[0] == '\t' {
			if !have {
				have, curLine = true, lineNo
			}
			phys = phys[1:]
		} else {
			flush()
			have, curLine = true, lineNo
		}
		if tooLong {
			continue
		}
		if len(cur)+len(phys) > MaxLineBytes {
			tooLong = true
			cur = cur[:0]
			continue
		}
		cur = append(cur, phys...)
	}
	if !p.stopped {
		flush()
	}
	p.finish(lineNo)
}

func (p *parser) logical(line int, b []byte) {
	s := string(b)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
		p.problem(line, ProblemInvalidUTF8, "invalid UTF-8 replaced")
		if len(s) > MaxLineBytes {
			p.problem(line, ProblemLineTooLong, "content line over 64 KiB dropped")
			return
		}
	}
	prop, code, detail := parseContentLine(s)
	if code != "" {
		p.problem(line, code, detail)
		return
	}
	if hasControl(prop.Value) {
		p.problem(line, ProblemControlChar, "control character in the value of "+prop.Name)
	}
	switch prop.Name {
	case "BEGIN":
		p.begin(line, prop.Value)
	case "END":
		p.end(line, prop.Value)
	default:
		p.property(line, prop)
	}
}

func (p *parser) begin(line int, value string) {
	name, ok := componentName(value)
	if !ok {
		// Ignored, like its END: its properties land in the parent.
		p.problem(line, ProblemBadBegin, "BEGIN without a valid component name")
		return
	}
	if name == "VCALENDAR" {
		// VCALENDAR is only ever top-level: one opening inside another
		// object closes everything first.
		for len(p.stack) > 0 {
			f := p.stack[len(p.stack)-1]
			if !f.skip && !f.synthetic {
				p.problem(line, ProblemMissingEnd, "missing END:"+f.comp.Name+" before BEGIN:VCALENDAR")
			}
			p.pop()
		}
	}
	if len(p.stack) == 0 && name != "VCALENDAR" {
		if !p.open(line, "VCALENDAR", true) {
			return
		}
		if !p.synthNoted {
			p.synthNoted = true
			p.problem(line, ProblemNoVCalendar, "BEGIN:"+name+" outside VCALENDAR wrapped")
		}
	}
	if top := p.top(); top != nil && top.skip {
		p.stack = append(p.stack, &frame{skip: true, line: line, comp: &Component{Name: name}})
		return
	}
	if len(p.stack) >= MaxDepth {
		p.obj.Truncated = true
		p.problem(line, ProblemTooDeep, "BEGIN:"+name+" nested deeper than 4 dropped")
		p.stack = append(p.stack, &frame{skip: true, line: line, comp: &Component{Name: name}})
		return
	}
	p.open(line, name, false)
}

// open pushes a stored component, or stops parsing at MaxComponents.
func (p *parser) open(line int, name string, synthetic bool) bool {
	if p.components >= MaxComponents {
		p.stop(line, ProblemTooManyComponents, "more than 500 components")
		return false
	}
	p.components++
	p.stack = append(p.stack, &frame{comp: &Component{Name: name}, synthetic: synthetic, line: line})
	return true
}

func (p *parser) end(line int, value string) {
	name, ok := componentName(value)
	if !ok {
		p.problem(line, ProblemBadBegin, "END without a valid component name")
		return
	}
	at := -1
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].comp.Name == name {
			at = i
			break
		}
	}
	if at < 0 {
		p.problem(line, ProblemStrayEnd, "END:"+name+" without BEGIN")
		return
	}
	for len(p.stack)-1 > at {
		f := p.top()
		if !f.skip {
			p.problem(line, ProblemMismatchedEnd, "END:"+f.comp.Name+" missing, closed by END:"+name)
		}
		p.pop()
	}
	p.pop()
}

func (p *parser) property(line int, prop Property) {
	f := p.top()
	if f == nil {
		p.problem(line, ProblemStrayProperty, prop.Name+" outside any component dropped")
		return
	}
	if f.skip {
		return
	}
	if p.properties >= MaxProperties {
		p.stop(line, ProblemTooManyProperties, "more than 5000 properties")
		return
	}
	if len(f.comp.Props) >= MaxComponentProperties {
		p.obj.Truncated = true
		p.problem(line, ProblemTooManyProperties, f.comp.Name+" with more than 1000 properties dropped")
		f.skip = true
		f.comp = &Component{Name: f.comp.Name}
		return
	}
	p.properties++
	f.comp.Props = append(f.comp.Props, prop)
}

func (p *parser) top() *frame {
	if len(p.stack) == 0 {
		return nil
	}
	return p.stack[len(p.stack)-1]
}

// pop closes the top frame: a stored component goes to its parent, a
// top-level one to the object.
func (p *parser) pop() {
	f := p.top()
	p.stack = p.stack[:len(p.stack)-1]
	if f.skip {
		return
	}
	if parent := p.top(); parent != nil {
		if !parent.skip {
			parent.comp.Components = append(parent.comp.Components, f.comp)
		}
		return
	}
	p.obj.Calendars = append(p.obj.Calendars, f.comp)
}

// stop ends parsing at an object-wide cap: components still open below the
// top level are partial and dropped, the open VCALENDAR is kept.
func (p *parser) stop(line int, code, detail string) {
	p.obj.Truncated = true
	p.stopped = true
	p.problem(line, code, detail+"; the rest not read")
	for len(p.stack) > 1 {
		p.stack[len(p.stack)-1].skip = true
		p.pop()
	}
	if len(p.stack) == 1 {
		p.pop()
	}
}

func (p *parser) finish(lastLine int) {
	for len(p.stack) > 0 {
		f := p.top()
		if !f.skip && !f.synthetic {
			p.problem(lastLine, ProblemMissingEnd, "missing END:"+f.comp.Name+" at end of input")
		}
		p.pop()
	}
	if len(p.obj.Calendars) == 0 && !p.stopped {
		p.problem(0, ProblemEmpty, "no component")
	}
	if len(p.obj.Calendars) > 1 {
		p.problem(0, ProblemMultipleCalendars, fmt.Sprintf("%d VCALENDAR objects", len(p.obj.Calendars)))
	}
}

func componentName(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !validName(v) {
		return "", false
	}
	return strings.ToUpper(v), true
}

func validName(s string) bool {
	if s == "" || len(s) > MaxNameBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isNameChar(s[i]) {
			return false
		}
	}
	return true
}

func isNameChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
}

// isCTL: the CONTROL characters of RFC 5545 (HTAB is allowed).
func isCTL(c byte) bool { return c < 0x20 && c != '\t' || c == 0x7f }

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if isCTL(s[i]) {
			return true
		}
	}
	return false
}

// parseContentLine reads name *(";" param) ":" value. A non-empty code
// means the line is dropped.
func parseContentLine(s string) (Property, string, string) {
	i := 0
	for i < len(s) && isNameChar(s[i]) {
		i++
	}
	if i == 0 || i > MaxNameBytes || i == len(s) || s[i] != ';' && s[i] != ':' {
		return Property{}, ProblemBadLine, "not a content line"
	}
	prop := Property{Name: strings.ToUpper(s[:i])}
	for s[i] == ';' {
		i++
		j := i
		for j < len(s) && isNameChar(s[j]) {
			j++
		}
		if j == i || j-i > MaxNameBytes || j == len(s) || s[j] != '=' {
			return Property{}, ProblemBadParam, "malformed parameter of " + prop.Name
		}
		param := Param{Name: strings.ToUpper(s[i:j])}
		i = j + 1
		for {
			var v string
			if i < len(s) && s[i] == '"' {
				end := strings.IndexByte(s[i+1:], '"')
				if end < 0 {
					return Property{}, ProblemBadParam, "unterminated quoted parameter of " + prop.Name
				}
				v = s[i+1 : i+1+end]
				i += end + 2
				if i == len(s) || s[i] != ',' && s[i] != ';' && s[i] != ':' {
					return Property{}, ProblemBadParam, "text after a quoted parameter of " + prop.Name
				}
			} else {
				j := i
				for j < len(s) && s[j] != ',' && s[j] != ';' && s[j] != ':' {
					if s[j] == '"' {
						return Property{}, ProblemBadParam, "quote inside a parameter of " + prop.Name
					}
					j++
				}
				if j == len(s) {
					return Property{}, ProblemBadParam, "parameter of " + prop.Name + " without a value"
				}
				v = s[i:j]
				i = j
			}
			if hasControl(v) {
				return Property{}, ProblemBadParam, "control character in a parameter of " + prop.Name
			}
			param.Values = append(param.Values, v)
			if s[i] != ',' {
				break
			}
			i++
		}
		if len(prop.Params) >= MaxParams {
			return Property{}, ProblemTooManyParams, prop.Name + " with more than 32 parameters dropped"
		}
		prop.Params = append(prop.Params, param)
	}
	prop.Value = s[i+1:]
	return prop, "", ""
}
