// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A minimal CSS filter, deliberately not a CSS parser. It understands just
// enough structure to re-emit declarations and simple rules from an
// allow-list, and treats everything it does not understand as absent. The
// output is built from validated pieces only, so it can never contain '<'
// and therefore never terminates the <style> element it is rendered into.

const (
	maxCSSBytes    = 256 << 10 // per <style> element, before filtering
	maxCSSRules    = 4000      // across the whole message
	maxCSSValue    = 512
	maxCSSSelector = 512
	maxCSSPrelude  = 256
)

// allowedProps are the properties kept in style="" and <style>. Prefix
// families (border-*, margin-*, …) are in allowedPrefixes. Absent on
// purpose: content, z-index, opacity, cursor, pointer-events, filter,
// transform, animation, transition, clip, and every vendor prefix.
var allowedProps = map[string]bool{
	"color": true, "background": true, "background-color": true,
	"font": true, "line-height": true, "letter-spacing": true, "white-space": true,
	"vertical-align": true, "direction": true, "unicode-bidi": true,
	"margin": true, "padding": true, "border": true, "border-radius": true,
	"border-collapse": true, "border-spacing": true,
	"width": true, "height": true, "max-width": true, "max-height": true,
	"min-width": true, "min-height": true,
	"display": true, "float": true, "clear": true, "overflow": true,
	"table-layout": true, "empty-cells": true, "caption-side": true,
	"list-style": true, "list-style-type": true, "list-style-position": true,
	"position": true, "visibility": true, "box-sizing": true, "hyphens": true,
	"tab-size": true, "quotes": true,
}

var allowedPrefixes = []string{
	"border-", "margin-", "padding-", "font-", "text-", "word-", "list-style-",
	"background-", "column-", "overflow-",
}

func allowedProp(p string) bool {
	if allowedProps[p] {
		return true
	}
	for _, pre := range allowedPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// unsafeValueParts are rejected anywhere in a value, case-insensitively.
// url() and its cousins fetch; expression() is script; a backslash is a CSS
// escape that could spell any of them; the rest are structure characters
// that have no business inside a single value.
var unsafeValueParts = []string{
	"url(", "expression(", "image-set(", "src(", "element(", "javascript:",
	"vbscript:", "\\", "<", ">", "@", "{", "}", ";", "/*",
}

// safeValue trims a value and rejects anything that could fetch, script or
// escape the declaration.
func safeValue(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxCSSValue || hasControl(v) {
		return "", false
	}
	lower := strings.ToLower(v)
	for _, bad := range unsafeValueParts {
		if strings.Contains(lower, bad) {
			return "", false
		}
	}
	return v, true
}

// allowedPropValue applies the per-property restrictions: no overlays, no
// hidden text.
func allowedPropValue(prop, v string) bool {
	v = strings.ToLower(v)
	switch prop {
	case "position":
		return v == "static" || v == "relative"
	case "visibility":
		return v == "visible"
	}
	return true
}

type decl struct {
	prop, val string
	important bool
}

// parseDeclarations splits "a: b; c: d !important" into declarations. Only
// ';' outside quotes and parentheses separates, so "font-family: 'a;b'"
// stays one declaration.
func parseDeclarations(s string) []decl {
	var out []decl
	for _, item := range splitTop(s, ';') {
		i := strings.IndexByte(item, ':')
		if i <= 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(item[:i]))
		val := strings.TrimSpace(item[i+1:])
		important := false
		if lower := strings.ToLower(val); strings.HasSuffix(lower, "!important") {
			val = strings.TrimSpace(val[:len(val)-len("!important")])
			important = true
		}
		if prop == "" || !isPropName(prop) {
			continue
		}
		out = append(out, decl{prop: prop, val: val, important: important})
	}
	return out
}

func isPropName(p string) bool {
	if len(p) > 48 {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r == '-') {
			return false
		}
	}
	return true
}

// splitTop splits on sep outside quoted strings and parentheses.
func splitTop(s string, sep byte) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// stripComments removes /* … */ blocks; an unterminated comment swallows
// the rest, as it does in a browser.
func stripComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+2+j+2:]
	}
}

// filterDeclarations is the style="" filter: allowed properties with safe
// values, re-emitted in a fixed form so filtering its own output changes
// nothing. A url() in a rejected value is a remote image reference and is
// counted as one.
func filterDeclarations(src string, blocked *api.BlockedContent) string {
	var b strings.Builder
	for _, d := range parseDeclarations(stripComments(src)) {
		if !allowedProp(d.prop) {
			continue
		}
		v, ok := safeValue(d.val)
		if !ok {
			if strings.Contains(strings.ToLower(d.val), "url(") {
				blocked.RemoteImages++
			}
			continue
		}
		if !allowedPropValue(d.prop, v) {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(d.prop)
		b.WriteString(": ")
		b.WriteString(v)
		if d.important {
			b.WriteString(" !important")
		}
	}
	return b.String()
}

// filterStylesheet is the <style> filter. It keeps "selector { … }" rules
// with filtered declarations and one level of @media around them; every
// other at-rule is dropped (@import counts as a remote stylesheet,
// @font-face as a remote font). budget is the rule allowance shared by all
// stylesheets of one message.
func filterStylesheet(src string, blocked *api.BlockedContent, budget *int) string {
	src = stripComments(src)
	if len(src) > maxCSSBytes {
		src = src[:maxCSSBytes]
	}
	p := &cssParser{s: src, blocked: blocked, budget: budget}
	return strings.Join(p.rules(0), "\n")
}

type cssParser struct {
	s       string
	i       int
	blocked *api.BlockedContent
	budget  *int
}

// rules parses until the end of input or, below the top level, until the
// closing brace of the enclosing block (left for the caller to consume).
func (p *cssParser) rules(depth int) []string {
	var out []string
	for {
		p.skipSpace()
		if p.i >= len(p.s) {
			return out
		}
		if p.s[p.i] == '}' {
			if depth > 0 {
				return out
			}
			p.i++ // a stray brace at the top level
			continue
		}
		if *p.budget >= maxCSSRules {
			p.i = len(p.s)
			return out
		}
		if p.s[p.i] == '@' {
			if r, ok := p.atRule(depth); ok {
				out = append(out, r)
			}
			continue
		}
		sel, ok := p.readUntil('{')
		if !ok {
			p.i = len(p.s)
			return out
		}
		body, ok := p.readUntil('}')
		if !ok {
			p.i = len(p.s)
			return out
		}
		*p.budget++
		if strings.IndexByte(body, '{') >= 0 {
			continue // nested block inside a style rule: not CSS we keep
		}
		sel = normalizeSelector(sel)
		decls := filterDeclarations(body, p.blocked)
		if sel == "" || decls == "" {
			continue
		}
		out = append(out, sel+" { "+decls+" }")
	}
}

// atRule handles the at-rule starting at p.i.
func (p *cssParser) atRule(depth int) (string, bool) {
	j := p.i + 1
	for j < len(p.s) && (p.s[j] >= 'a' && p.s[j] <= 'z' || p.s[j] >= 'A' && p.s[j] <= 'Z' || p.s[j] == '-') {
		j++
	}
	name := strings.ToLower(p.s[p.i+1 : j])
	p.i = j
	switch name {
	case "media":
		if depth > 0 {
			p.skipAtRule()
			return "", false
		}
		prelude, ok := p.readUntil('{')
		if !ok {
			p.i = len(p.s)
			return "", false
		}
		*p.budget++
		inner := p.rules(depth + 1)
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
		}
		prelude = strings.Join(strings.Fields(prelude), " ")
		if !validMediaPrelude(prelude) || len(inner) == 0 {
			return "", false
		}
		return "@media " + prelude + " {\n" + strings.Join(inner, "\n") + "\n}", true
	case "import":
		p.blocked.RemoteStyles++
	case "font-face":
		p.blocked.RemoteFonts++
	}
	p.skipAtRule()
	return "", false
}

// skipAtRule consumes an at-rule statement (up to ';') or block (matching
// braces), whichever comes first.
func (p *cssParser) skipAtRule() {
	var quote byte
	depth := 0
	for ; p.i < len(p.s); p.i++ {
		c := p.s[p.i]
		switch {
		case quote != 0:
			if c == '\\' {
				p.i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ';' && depth == 0:
			p.i++
			return
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth <= 0 {
				p.i++
				return
			}
		}
	}
}

// readUntil returns the text up to stop (outside quotes) and moves past it.
func (p *cssParser) readUntil(stop byte) (string, bool) {
	var quote byte
	for j := p.i; j < len(p.s); j++ {
		c := p.s[j]
		switch {
		case quote != 0:
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == stop:
			out := p.s[p.i:j]
			p.i = j + 1
			return out, true
		}
	}
	return "", false
}

func (p *cssParser) skipSpace() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n' || p.s[p.i] == '\r' || p.s[p.i] == '\f') {
		p.i++
	}
}

// normalizeSelector collapses whitespace and accepts only the characters a
// selector needs. Attribute selectors are fine: without url() they cannot
// leak anything.
func normalizeSelector(sel string) string {
	sel = strings.Join(strings.Fields(sel), " ")
	if sel == "" || len(sel) > maxCSSSelector {
		return ""
	}
	for _, r := range sel {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune(" _.#:,>+~*=[]\"'()-^$|", r)) {
			return ""
		}
	}
	return sel
}

// validMediaPrelude accepts "screen and (max-width: 600px)" and rejects
// anything with characters a media query does not need.
func validMediaPrelude(s string) bool {
	if s == "" || len(s) > maxCSSPrelude {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune(" :(),.-", r)) {
			return false
		}
	}
	return true
}
