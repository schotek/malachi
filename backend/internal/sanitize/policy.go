// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"
	"unicode"
)

// The allow-lists. Everything not listed here is dropped or unwrapped; the
// lists only ever grow by deliberate decision, with a Version bump.

// allowedElems are kept with their filtered attributes. <style> is not here
// because it is handled apart (view mode hoists it, compose mode drops it),
// and neither are html/head/body, which are always unwrapped.
var allowedElems = map[string]bool{
	// text formatting
	"a": true, "abbr": true, "acronym": true, "b": true, "bdi": true, "bdo": true,
	"big": true, "cite": true, "code": true, "del": true, "dfn": true, "em": true,
	"font": true, "i": true, "ins": true, "kbd": true, "mark": true, "q": true,
	"s": true, "samp": true, "small": true, "span": true, "strike": true,
	"strong": true, "sub": true, "sup": true, "tt": true, "u": true, "var": true,
	"wbr": true,
	// structure
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "center": true, "details": true, "div": true, "figcaption": true,
	"figure": true, "footer": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hr": true, "main": true,
	"nav": true, "p": true, "pre": true, "section": true, "summary": true,
	// lists
	"dd": true, "dl": true, "dt": true, "li": true, "ol": true, "ul": true,
	// tables
	"caption": true, "col": true, "colgroup": true, "table": true, "tbody": true,
	"td": true, "tfoot": true, "th": true, "thead": true, "tr": true,
	// images (the only embedded content that survives)
	"img": true,
}

// dropCategory says which api.BlockedContent counter a dropped element feeds.
type dropCategory int

const (
	dropSilent dropCategory = iota
	dropScript
	dropFrame
	dropForm
	dropRemoteStyle
	dropNavigation
	dropMedia
)

// droppedElems vanish together with everything inside them. svg and math
// are here because both carry their own script and link semantics that the
// HTML allow-list knows nothing about. <noscript> is deliberately absent:
// the view runs without JavaScript, so its content is what the reader gets,
// and unwrapping it lets the tracking pixels mailers hide there be counted
// and removed like any other image.
var droppedElems = map[string]dropCategory{
	"script": dropScript, "svg": dropScript, "math": dropScript,
	"iframe": dropFrame, "frame": dropFrame, "frameset": dropFrame, "object": dropFrame,
	"embed": dropFrame, "applet": dropFrame, "portal": dropFrame,
	"form": dropForm, "input": dropForm, "button": dropForm, "select": dropForm,
	"textarea": dropForm, "option": dropForm, "optgroup": dropForm, "datalist": dropForm,
	"link": dropRemoteStyle,
	"meta": dropNavigation, "base": dropNavigation,
	"video": dropMedia, "audio": dropMedia, "source": dropMedia, "track": dropMedia,
	"canvas": dropSilent, "map": dropSilent, "area": dropSilent, "dialog": dropSilent,
	"template": dropSilent, "title": dropSilent, "noembed": dropSilent,
	"noframes": dropSilent, "slot": dropSilent, "head": dropSilent,
}

// globalAttrs may appear on any allowed element.
var globalAttrs = map[string]bool{
	"class": true, "id": true, "title": true, "dir": true, "lang": true,
	"style": true, "align": true, "valign": true, "width": true, "height": true,
	"bgcolor": true, "role": true, "border": true,
}

// elemAttrs are the per-element additions. href and src are listed for
// completeness but never pass through cleanAttrValue: they take the URL
// path in attrs.go.
var elemAttrs = map[string]map[string]bool{
	"a":          {"href": true, "name": true},
	"img":        {"src": true, "alt": true, "hspace": true, "vspace": true},
	"font":       {"color": true, "face": true, "size": true},
	"ol":         {"start": true, "type": true, "reversed": true},
	"ul":         {"type": true},
	"li":         {"value": true, "type": true},
	"table":      {"cellpadding": true, "cellspacing": true, "summary": true, "frame": true, "rules": true},
	"td":         {"colspan": true, "rowspan": true, "nowrap": true, "scope": true, "headers": true},
	"th":         {"colspan": true, "rowspan": true, "nowrap": true, "scope": true, "headers": true, "abbr": true},
	"col":        {"span": true},
	"colgroup":   {"span": true},
	"hr":         {"size": true, "noshade": true},
	"details":    {"open": true},
	"blockquote": {"type": true},
	"pre":        {"wrap": true},
}

// remoteAttrs are URL-valued attributes that reference a remote resource
// and have no place in the output; each one counts as a blocked image.
var remoteAttrs = map[string]bool{
	"background": true, "srcset": true, "poster": true, "lowsrc": true, "dynsrc": true,
}

// silentAttrs are dropped without a counter: navigation hints, form
// plumbing, namespaces and everything that only a script could use.
var silentAttrs = map[string]bool{
	"target": true, "rel": true, "ping": true, "usemap": true, "ismap": true,
	"formaction": true, "sizes": true, "xmlns": true, "loading": true,
	"referrerpolicy": true, "crossorigin": true, "decoding": true,
	"fetchpriority": true, "contenteditable": true, "draggable": true,
	"tabindex": true, "accesskey": true, "autofocus": true, "hidden": true,
	"is": true, "slot": true, "part": true, "itemprop": true, "itemscope": true,
	"itemtype": true, "itemid": true, "itemref": true,
}

// Value grammars for the attributes that are worth checking. Everything is
// inert in a JavaScript-disabled view, so this is about keeping the output
// boringly well-formed rather than about a specific attack.

// numericAttrs take a number, optionally signed, with a % or px suffix.
var numericAttrs = map[string]bool{
	"width": true, "height": true, "colspan": true, "rowspan": true, "border": true,
	"cellpadding": true, "cellspacing": true, "size": true, "start": true,
	"value": true, "span": true, "hspace": true, "vspace": true,
}

// colorAttrs take a #hex or a colour name.
var colorAttrs = map[string]bool{
	"color": true, "bgcolor": true, "text": true, "link": true, "alink": true, "vlink": true,
}

// keywordAttrs take a short keyword.
var keywordAttrs = map[string]bool{
	"align": true, "valign": true, "dir": true, "type": true, "scope": true,
	"rules": true, "frame": true, "wrap": true, "nowrap": true, "noshade": true,
	"open": true, "reversed": true,
}

// cleanAttrValue validates an attribute value against its grammar and
// reports whether the attribute may be kept. Free-text attributes (title,
// alt, class, …) are accepted when they hold no control characters and fit
// the cap; the HTML serialiser escapes them.
func cleanAttrValue(key, val string) (string, bool) {
	if len(val) > maxAttrValue || hasControl(val) {
		return "", false
	}
	switch {
	case numericAttrs[key]:
		return val, isNumericValue(val)
	case colorAttrs[key]:
		return val, isColorValue(val)
	case keywordAttrs[key]:
		return val, isKeyword(val)
	case key == "face":
		return val, isFontList(val)
	}
	return val, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f {
			return true
		}
	}
	return false
}

func isNumericValue(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 12 {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "px"), "%")
	if s == "" || s == "." {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r == '.') {
			return false
		}
	}
	return true
}

func isColorValue(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 32 {
		return false
	}
	if s[0] == '#' {
		s = s[1:]
		if len(s) < 3 || len(s) > 8 {
			return false
		}
		for _, r := range s {
			if !isHex(r) {
				return false
			}
		}
		return true
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func isHex(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

func isKeyword(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// isFontList accepts a comma-separated list of family names.
func isFontList(s string) bool {
	if len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == ',' || r == '-' || r == '\'' || r == '"' || r == '_') {
			return false
		}
	}
	return true
}
