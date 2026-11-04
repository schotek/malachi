// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"fmt"
	"strconv"
	"strings"
)

// version of a card's syntax.
type version int

const (
	v21 version = 21
	v30 version = 30
	v40 version = 40
)

func versionOf(s string) version {
	switch {
	case strings.HasPrefix(s, "4"):
		return v40
	case strings.HasPrefix(s, "2"):
		return v21
	}
	return v30
}

// doc is a parsed input: its lines, the first card in it and the lines
// each field of the model came from.
type doc struct {
	raw   []byte
	lines []logLine
	props []prop // parsed lines of the card (zero outside it and in nested cards)
	begin int    // line of BEGIN:VCARD
	end   int    // line of its END:VCARD, -1 when missing
	last  int    // last line of the card (end, or the last line)
	eol   string
	ver   version
	card  *Card

	fn, n, nickname, org, title, bday, note, rev, versionLine, photo int
	emails, phones, addrs, urls                                      []int
	groupLabel                                                       map[string]int // lower-case group → its X-ABLabel line
}

// Parse reads the first vCard of raw. It fails only when raw is larger
// than MaxCardBytes, has no BEGIN:VCARD or more than MaxProperties
// properties in the card; anything else malformed is skipped. A card
// without END:VCARD runs to the end of raw.
func Parse(raw []byte) (c *Card, err error) {
	d, err := parseDoc(raw)
	if err != nil {
		return nil, err
	}
	return d.card, nil
}

func parseDoc(raw []byte) (d *doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, fmt.Errorf("%w: %v", ErrMalformed, r)
		}
	}()
	if len(raw) > MaxCardBytes {
		return nil, ErrTooLarge
	}
	d = &doc{raw: raw, lines: splitLines(raw), begin: -1, end: -1,
		fn: -1, n: -1, nickname: -1, org: -1, title: -1, bday: -1, note: -1, rev: -1, versionLine: -1, photo: -1,
		groupLabel: map[string]int{}}
	d.props = make([]prop, len(d.lines))
	for i := range d.lines {
		p := parseProp(string(d.lines[i].buf))
		if p.ok && p.name == "BEGIN" && strings.EqualFold(strings.TrimSpace(p.value), "VCARD") {
			d.begin = i
			break
		}
	}
	if d.begin < 0 {
		return nil, ErrNoCard
	}
	d.eol = "\n"
	if l := d.lines[d.begin]; l.end >= 2 && d.raw[l.end-1] == '\n' && d.raw[l.end-2] == '\r' {
		d.eol = "\r\n"
	}
	depth, count := 1, 0
	d.last = len(d.lines) - 1
	for i := d.begin + 1; i < len(d.lines); i++ {
		if len(d.lines[i].buf) == 0 {
			continue
		}
		p := parseProp(string(d.lines[i].buf))
		vcardTag := p.ok && strings.EqualFold(strings.TrimSpace(p.value), "VCARD")
		if vcardTag && p.name == "END" {
			depth--
			if depth == 0 {
				d.end, d.last = i, i
				break
			}
			continue
		}
		if count++; count > MaxProperties {
			return nil, ErrTooManyProperties
		}
		if vcardTag && p.name == "BEGIN" {
			depth++
			continue
		}
		if depth == 1 {
			d.props[i] = p
		}
	}
	d.extract()
	return d, nil
}

// cardLines calls f for each parsed line of the card.
func (d *doc) cardLines(f func(i int, p prop)) {
	for i := d.begin + 1; i <= d.last && i < len(d.props); i++ {
		if d.props[i].ok {
			f(i, d.props[i])
		}
	}
}

// text returns the unescaped decoded value of p.
func text(p prop) (string, bool) {
	v, ok := p.decoded()
	if !ok {
		return "", false
	}
	return unescape(v), true
}

func (d *doc) extract() {
	c := &Card{}
	d.cardLines(func(i int, p prop) {
		if p.name == "X-ABLABEL" && p.group != "" {
			if _, seen := d.groupLabel[strings.ToLower(p.group)]; !seen {
				d.groupLabel[strings.ToLower(p.group)] = i
			}
		}
	})
	single := func(ref *int, i int, p prop, set func(string)) {
		if *ref >= 0 {
			return
		}
		if v, ok := text(p); ok {
			*ref = i
			set(v)
		}
	}
	d.cardLines(func(i int, p prop) {
		switch p.name {
		case "VERSION":
			single(&d.versionLine, i, p, func(v string) { c.Version = cleanLine(v, 16) })
		case "UID":
			if c.UID == "" {
				if v, ok := text(p); ok {
					c.UID = cleanLine(v, MaxUIDBytes)
				}
			}
		case "FN":
			single(&d.fn, i, p, func(v string) { c.DisplayName = cleanLine(v, MaxFieldBytes) })
		case "NICKNAME":
			single(&d.nickname, i, p, func(v string) { c.Nickname = cleanLine(v, MaxFieldBytes) })
		case "TITLE":
			single(&d.title, i, p, func(v string) { c.Title = cleanLine(v, MaxFieldBytes) })
		case "NOTE":
			single(&d.note, i, p, func(v string) { c.Note = cleanBlock(v, MaxNoteBytes) })
		case "REV":
			if d.rev < 0 {
				d.rev = i
			}
		case "PHOTO":
			if d.photo < 0 {
				d.photo = i
				c.Photo, c.PhotoURI = photoOf(p)
			}
		case "N":
			if d.n < 0 {
				if v, ok := p.decoded(); ok {
					d.n = i
					comps := splitComponents(v)
					c.Name = Name{
						Family: cleanLine(component(comps, 0), MaxFieldBytes),
						Given:  cleanLine(component(comps, 1), MaxFieldBytes),
						Middle: cleanLine(component(comps, 2), MaxFieldBytes),
						Prefix: cleanLine(component(comps, 3), MaxFieldBytes),
						Suffix: cleanLine(component(comps, 4), MaxFieldBytes),
					}
				}
			}
		case "ORG":
			if d.org < 0 {
				if v, ok := p.decoded(); ok {
					d.org = i
					c.Org = cleanLine(component(splitComponents(v), 0), MaxFieldBytes)
				}
			}
		case "BDAY":
			if d.bday < 0 {
				if v, ok := text(p); ok {
					d.bday = i
					c.Birthday = parseBirthday(p, v)
				}
			}
		case "EMAIL":
			if len(c.Emails) < MaxItems {
				if v, ok := text(p); ok {
					if a := cleanEmail(v); a != "" {
						c.Emails = append(c.Emails, Email{Label: d.labelOf(p), Address: a})
						d.emails = append(d.emails, i)
					}
				}
			}
		case "TEL":
			if len(c.Phones) < MaxItems {
				if v, ok := text(p); ok {
					if n := cleanPhone(v); n != "" {
						c.Phones = append(c.Phones, Phone{Label: d.labelOf(p), Number: n})
						d.phones = append(d.phones, i)
					}
				}
			}
		case "ADR":
			if len(c.Addresses) < MaxItems {
				if v, ok := p.decoded(); ok {
					a := addressOf(splitComponents(v))
					if a != (Address{}) {
						a.Label = d.labelOf(p)
						c.Addresses = append(c.Addresses, a)
						d.addrs = append(d.addrs, i)
					}
				}
			}
		case "URL":
			if len(c.URLs) < MaxItems {
				if v, ok := text(p); ok {
					if u := cleanLine(v, MaxURLBytes); u != "" {
						c.URLs = append(c.URLs, URL{Label: d.labelOf(p), URL: u})
						d.urls = append(d.urls, i)
					}
				}
			}
		}
	})
	d.ver = versionOf(c.Version)
	d.card = c
}

func addressOf(comps []string) Address {
	return Address{
		Street:   cleanBlock(component(comps, 2), MaxFieldBytes),
		City:     cleanLine(component(comps, 3), MaxFieldBytes),
		Region:   cleanLine(component(comps, 4), MaxFieldBytes),
		Postcode: cleanLine(component(comps, 5), MaxFieldBytes),
		Country:  cleanLine(component(comps, 6), MaxFieldBytes),
	}
}

// labelOf returns the label of an item line: its group's X-ABLabel when
// it has one (Apple, Google), else the first standard type.
func (d *doc) labelOf(p prop) string {
	if p.group != "" {
		if li, ok := d.groupLabel[strings.ToLower(p.group)]; ok {
			v, _ := text(d.props[li])
			return normalizeLabel(v)
		}
	}
	return labelFromTypes(p.types())
}

func labelFromTypes(types []string) string {
	has := map[string]bool{}
	for _, t := range types {
		if l, ok := labelTokens[t]; ok {
			has[l] = true
		}
	}
	for _, l := range []string{LabelFax, LabelMobile, LabelWork, LabelHome, LabelOther} {
		if has[l] {
			return l
		}
	}
	return ""
}

// parseBirthday reads YYYY-MM-DD, YYYYMMDD, --MM-DD, --MMDD (time part
// ignored); year 1604, 0000 and Apple's X-APPLE-OMIT-YEAR are no year. A
// text value or an invalid date is no birthday.
func parseBirthday(p prop, v string) Birthday {
	if vt, ok := p.paramValue("VALUE"); ok && strings.EqualFold(vt, "text") {
		return Birthday{}
	}
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, "Tt"); i >= 0 {
		v = v[:i]
	}
	var y, m, day int
	digits := func(s string) (int, bool) {
		if s == "" {
			return 0, false
		}
		for k := 0; k < len(s); k++ {
			if s[k] < '0' || s[k] > '9' {
				return 0, false
			}
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	var ok1, ok2, ok3 bool
	if strings.HasPrefix(v, "--") {
		r := strings.ReplaceAll(v[2:], "-", "")
		if len(r) != 4 {
			return Birthday{}
		}
		ok1 = true
		m, ok2 = digits(r[:2])
		day, ok3 = digits(r[2:])
	} else {
		r := strings.ReplaceAll(v, "-", "")
		if len(r) != 8 {
			return Birthday{}
		}
		y, ok1 = digits(r[:4])
		m, ok2 = digits(r[4:6])
		day, ok3 = digits(r[6:])
	}
	if !ok1 || !ok2 || !ok3 {
		return Birthday{}
	}
	if oy, ok := p.paramValue("X-APPLE-OMIT-YEAR"); ok && oy == strconv.Itoa(y) {
		y = 0
	}
	return cleanBirthday(Birthday{Year: y, Month: m, Day: day})
}
