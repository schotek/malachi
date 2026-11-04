// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"fmt"
	"strconv"
	"strings"
)

// Patch returns raw with the edit from before to after applied: only the
// properties whose value differs between the two cleaned cards (FN, N,
// NICKNAME, ORG, TITLE, EMAIL, TEL, ADR, URL, BDAY, NOTE) are rewritten,
// added or removed, in the syntax of the card's own version; every other
// line is copied byte for byte and the order is kept. REV, when the card
// has one, gets the current time if anything changed. Without a change
// the result equals raw.
//
// before is what the user saw, raw is the card as it is now: a field the
// user did not change keeps raw's value even when raw changed since. List
// items are matched by value (a changed item rewrites its own line, keeping
// its group and parameters); a removed item takes its line and, when
// nothing else is left in its group, the group's X-ABLabel with it. A
// custom label goes to an X-ABLabel in the item's group (a new itemN
// group when it has none). Clearing FN writes DisplayNameOf(after); N is
// kept (empty) in 2.1 and 3.0, where it is required.
func Patch(raw []byte, before, after *Card) (out []byte, err error) {
	if before == nil || after == nil {
		return nil, ErrNilCard
	}
	d, err := parseDoc(raw)
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", ErrMalformed, r)
		}
	}()
	b, a := Clean(before), Clean(after)
	p := &patcher{d: d, w: writer{ver: d.ver, eol: d.eol},
		repl: map[int]string{}, del: map[int]bool{}, ins: map[int][]string{}}
	p.initGroups()
	afterHeader := d.begin
	if d.versionLine >= 0 {
		afterHeader = d.versionLine
	}
	beforeEnd := d.last
	if d.end >= 0 {
		beforeEnd = d.end - 1
	}

	if b.DisplayName != a.DisplayName {
		v := a.DisplayName
		if v == "" {
			v = DisplayNameOf(a)
		}
		p.setText("FN", d.fn, p.w.escapeText(v), true, afterHeader)
	}
	if b.Name != a.Name {
		empty := a.Name == (Name{})
		if empty && d.ver == v40 {
			p.setText("N", d.n, "", false, afterHeader)
		} else {
			p.setText("N", d.n, p.w.nameValue(a.Name), true, afterHeader)
		}
	}
	if b.Nickname != a.Nickname {
		p.setText("NICKNAME", d.nickname, p.w.escapeText(a.Nickname), false, beforeEnd)
	}
	if b.Org != a.Org {
		p.setOrg(a.Org, beforeEnd)
	}
	if b.Title != a.Title {
		p.setText("TITLE", d.title, p.w.escapeText(a.Title), false, beforeEnd)
	}
	if b.Birthday != a.Birthday {
		p.setBirthday(a.Birthday, beforeEnd)
	}
	if b.Note != a.Note {
		p.setText("NOTE", d.note, p.w.escapeText(a.Note), false, beforeEnd)
	}
	p.list(kindEmail, emailItems(b.Emails), emailItems(a.Emails), emailItems(d.card.Emails), d.emails, beforeEnd)
	p.list(kindTel, phoneItems(b.Phones), phoneItems(a.Phones), phoneItems(d.card.Phones), d.phones, beforeEnd)
	p.list(kindAdr, addressItems(b.Addresses), addressItems(a.Addresses), addressItems(d.card.Addresses), d.addrs, beforeEnd)
	p.list(kindURL, urlItems(b.URLs), urlItems(a.URLs), urlItems(d.card.URLs), d.urls, beforeEnd)

	if len(p.repl) == 0 && len(p.del) == 0 && len(p.ins) == 0 {
		return append([]byte(nil), raw...), nil
	}
	if d.rev >= 0 && !p.del[d.rev] {
		if _, done := p.repl[d.rev]; !done {
			p.repl[d.rev] = p.rewrite(d.rev, p.w.rev())
		}
	}
	return p.build(), nil
}

type patcher struct {
	d         *doc
	w         writer
	repl      map[int]string   // line → its new text
	del       map[int]bool     // lines removed
	ins       map[int][]string // line → texts inserted after it
	nextGroup int
}

// initGroups finds the highest itemN group of the card.
func (p *patcher) initGroups() {
	p.d.cardLines(func(_ int, pr prop) {
		g := strings.ToLower(pr.group)
		if strings.HasPrefix(g, "item") {
			if n, err := strconv.Atoi(g[4:]); err == nil && n >= p.nextGroup && n < 1<<20 {
				p.nextGroup = n
			}
		}
	})
}

func (p *patcher) allocGroup() string {
	p.nextGroup++
	return "item" + strconv.Itoa(p.nextGroup)
}

func (p *patcher) insertAfter(line int, text string) {
	p.ins[line] = append(p.ins[line], text)
}

// keptParams returns the raw parameters of pr without those that pass
// drop.
func keptParams(pr prop, drop func(param) bool) []string {
	var out []string
	for _, pa := range pr.params {
		if !drop(pa) {
			out = append(out, pa.raw)
		}
	}
	return out
}

func dropEncoding(pa param) bool { return pa.isEncodingParam() }

// rewrite gives line ref a new escaped value, keeping its group, name and
// parameters but its encoding and charset.
func (p *patcher) rewrite(ref int, value string) string {
	pr := p.d.props[ref]
	return p.w.line(pr.group, pr.rawName, keptParams(pr, dropEncoding), value, false)
}

// setText sets a single property: rewrites line ref, removes it (empty
// value and not required) or adds a new line after anchor.
func (p *patcher) setText(name string, ref int, value string, required bool, anchor int) {
	switch {
	case ref >= 0 && value == "" && !required:
		p.deleteLine(ref)
	case ref >= 0:
		p.repl[ref] = p.rewrite(ref, value)
	case value != "" || required:
		p.insertAfter(anchor, p.w.line("", name, nil, value, false))
	}
}

func (p *patcher) setOrg(org string, anchor int) {
	ref := p.d.org
	if ref < 0 {
		if org != "" {
			p.insertAfter(anchor, p.w.line("", "ORG", nil, p.w.escapeText(org), false))
		}
		return
	}
	v, _ := p.d.props[ref].decoded()
	comps := splitComponents(v)
	comps[0] = p.w.escapeText(org)
	if org == "" {
		rest := false
		for _, c := range comps[1:] {
			if strings.TrimSpace(unescape(c)) != "" {
				rest = true
			}
		}
		if !rest {
			p.deleteLine(ref)
			return
		}
	}
	p.repl[ref] = p.rewrite(ref, strings.Join(comps, ";"))
}

func (p *patcher) setBirthday(b Birthday, anchor int) {
	ref := p.d.bday
	if b.IsZero() {
		if ref >= 0 {
			p.deleteLine(ref)
		}
		return
	}
	if ref < 0 {
		p.insertAfter(anchor, p.w.line("", "BDAY", nil, p.w.birthday(b, 0), false))
		return
	}
	pr := p.d.props[ref]
	apple := 0
	if v, ok := pr.paramValue("X-APPLE-OMIT-YEAR"); ok && b.Year == 0 {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 9999 {
			apple = n
		}
	}
	params := keptParams(pr, func(pa param) bool {
		return pa.isEncodingParam() || pa.name == "VALUE" || (pa.name == "X-APPLE-OMIT-YEAR" && apple == 0)
	})
	p.repl[ref] = p.w.line(pr.group, pr.rawName, params, p.w.birthday(b, apple), false)
}

// deleteLine removes a line and, when only X- lines are left in its
// group, those too (Apple's X-ABLabel, X-ABADR).
func (p *patcher) deleteLine(ref int) {
	p.del[ref] = true
	delete(p.repl, ref)
	g := strings.ToLower(p.d.props[ref].group)
	if g == "" {
		return
	}
	var xs []int
	other := false
	p.d.cardLines(func(i int, pr prop) {
		if p.del[i] || strings.ToLower(pr.group) != g {
			return
		}
		if strings.HasPrefix(pr.name, "X-") {
			xs = append(xs, i)
		} else {
			other = true
		}
	})
	if !other {
		for _, i := range xs {
			p.del[i] = true
			delete(p.repl, i)
		}
	}
}

// list applies the edit before → after of one list to the lines refs of
// the current items cur.
func (p *patcher) list(k itemKind, before, after, cur []item, refs []int, beforeEnd int) {
	if equalItems(before, after) {
		return
	}
	// Each item of before claims the current line with the same value,
	// preferring the same position.
	used := make([]bool, len(cur))
	at := make([]int, len(before))
	for bi, it := range before {
		at[bi] = -1
		if bi < len(cur) && cur[bi] == it {
			at[bi], used[bi] = refs[bi], true
		}
	}
	for bi, it := range before {
		if at[bi] >= 0 {
			continue
		}
		for ci := range cur {
			if !used[ci] && cur[ci] == it {
				at[bi], used[ci] = refs[ci], true
				break
			}
		}
	}
	anchor := p.itemAnchor(k, beforeEnd)
	for _, op := range diffItems(before, after) {
		switch {
		case op.b >= 0 && op.a >= 0:
			if at[op.b] >= 0 {
				p.rewriteItem(k, at[op.b], before[op.b], after[op.a])
			} else {
				p.insertAfter(anchor, p.w.newItem(k, after[op.a], p.allocGroup))
			}
		case op.b >= 0:
			if at[op.b] >= 0 {
				p.deleteLine(at[op.b])
			}
		default:
			p.insertAfter(anchor, p.w.newItem(k, after[op.a], p.allocGroup))
		}
	}
}

// itemAnchor is the line new items of a kind go after: the last line of
// that property and the lines of its group that follow it, else the last
// line before END.
func (p *patcher) itemAnchor(k itemKind, beforeEnd int) int {
	last := -1
	p.d.cardLines(func(i int, pr prop) {
		if pr.name == k.name {
			last = i
		}
	})
	if last < 0 {
		return beforeEnd
	}
	g := strings.ToLower(p.d.props[last].group)
	for g != "" && last+1 <= beforeEnd && last+1 < len(p.d.props) &&
		p.d.props[last+1].ok && strings.ToLower(p.d.props[last+1].group) == g {
		last++
	}
	return last
}

// labelish are the type values that make a label; others (pref, voice,
// internet…) are kept when a label changes.
var labelish = map[string]bool{"home": true, "work": true, "cell": true, "mobile": true, "iphone": true, "fax": true, "other": true}

// rewriteItem rewrites the line of an item whose value or label changed.
func (p *patcher) rewriteItem(k itemKind, ref int, old, nw item) {
	pr := p.d.props[ref]
	group := pr.group
	var params []string
	var value string
	raw := old.v == nw.v
	if raw {
		value = pr.value
		params = keptParams(pr, func(param) bool { return false })
	} else {
		dec, _ := pr.decoded()
		var keep []string
		scheme := ""
		if k == kindAdr {
			keep = splitComponents(dec)
		} else {
			low := strings.ToLower(strings.TrimSpace(unescape(dec)))
			if k == kindEmail && strings.HasPrefix(low, "mailto:") {
				scheme = "mailto:"
			}
			if k == kindTel && strings.HasPrefix(low, "tel:") {
				scheme = "tel:"
			}
		}
		value = p.w.itemValue(k, nw, keep, scheme)
		params = keptParams(pr, dropEncoding)
	}
	if old.label != nw.label {
		params = p.relabel(pr, params, raw)
		gl, hasGL := -1, false
		if group != "" {
			gl, hasGL = p.d.groupLabel[strings.ToLower(group)]
		}
		if isCustom(nw.label) {
			lv := p.w.escapeText(nw.label)
			switch {
			case hasGL && !p.del[gl]:
				p.repl[gl] = p.rewrite(gl, lv)
			default:
				if group == "" {
					group = p.allocGroup()
				}
				p.insertAfter(ref, p.w.line(group, "X-ABLabel", nil, lv, false))
			}
		} else {
			if hasGL {
				p.del[gl] = true
				delete(p.repl, gl)
			}
			if tp := p.w.typeParam(nw.label); tp != "" {
				params = append(params, tp)
			}
		}
	}
	p.repl[ref] = p.w.line(group, pr.rawName, params, value, raw)
}

// relabel returns the parameters of pr (those in kept, by raw text)
// without the type values that make a label.
func (p *patcher) relabel(pr prop, kept []string, _ bool) []string {
	keep := map[string]bool{}
	for _, k := range kept {
		keep[k] = true
	}
	var out []string
	for _, pa := range pr.params {
		if !keep[pa.raw] {
			continue
		}
		if !pa.isTypeParam() {
			out = append(out, pa.raw)
			continue
		}
		if pa.name == "" {
			if !labelish[strings.ToLower(pa.token)] {
				out = append(out, pa.raw)
			}
			continue
		}
		var vals []string
		changed := false
		for _, v := range pa.values {
			for _, t := range strings.Split(v, ",") {
				if labelish[strings.ToLower(strings.TrimSpace(t))] {
					changed = true
				} else if strings.TrimSpace(t) != "" {
					vals = append(vals, strings.TrimSpace(t))
				}
			}
		}
		switch {
		case !changed:
			out = append(out, pa.raw)
		case len(vals) > 0:
			name := strings.TrimSpace(pa.raw[1:strings.IndexByte(pa.raw, '=')])
			out = append(out, ";"+name+"="+strings.Join(vals, ","))
		}
	}
	return out
}

// build writes the patched input.
func (p *patcher) build() []byte {
	var b []byte
	for i, l := range p.d.lines {
		switch {
		case p.del[i]:
		case p.repl[i] != "":
			b = append(b, p.repl[i]...)
		default:
			b = append(b, p.d.raw[l.start:l.end]...)
		}
		if ins := p.ins[i]; len(ins) > 0 {
			if len(b) > 0 && b[len(b)-1] != '\n' {
				b = append(b, p.d.eol...)
			}
			for _, s := range ins {
				b = append(b, s...)
			}
		}
	}
	return b
}

func equalItems(a, b []item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// itemOp is one step of a list edit: b and a are indices of before and
// after; -1 when the step has none (an addition, a removal).
type itemOp struct{ b, a int }

// diffItems aligns before and after by their longest common subsequence;
// between two common items, items of before and after are paired in
// order as changes, the rest are removals or additions. Common items
// produce no step.
func diffItems(before, after []item) []itemOp {
	n, m := len(before), len(after)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []itemOp
	var gb, ga []int
	flush := func() {
		k := 0
		for ; k < len(gb) && k < len(ga); k++ {
			ops = append(ops, itemOp{gb[k], ga[k]})
		}
		for _, x := range gb[k:] {
			ops = append(ops, itemOp{x, -1})
		}
		for _, y := range ga[k:] {
			ops = append(ops, itemOp{-1, y})
		}
		gb, ga = gb[:0], ga[:0]
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case before[i] == after[j]:
			flush()
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			gb = append(gb, i)
			i++
		default:
			ga = append(ga, j)
			j++
		}
	}
	for ; i < n; i++ {
		gb = append(gb, i)
	}
	for ; j < m; j++ {
		ga = append(ga, j)
	}
	flush()
	return ops
}
