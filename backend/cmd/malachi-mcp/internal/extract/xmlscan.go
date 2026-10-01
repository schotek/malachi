// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// The XML of an OOXML part, read as hostile input: encoding/xml in strict
// mode with no CharsetReader (a part that is not UTF-8 is refused, never
// converted) and no entities beyond XML's five, any DOCTYPE (or other
// markup declaration) refused before anything in it could be expanded,
// one tag capped at maxTagBytes before the decoder builds it, element
// nesting capped at MaxXMLDepth and tokens at MaxPartTokens per part and
// MaxTokens per document. The readers walk the tokens with explicit
// stacks, never by a recursion the document controls.

// The namespaces the readers know, transitional and strict.
const (
	nsW       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsWStrict = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	nsS       = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsSStrict = "http://purl.oclc.org/ooxml/spreadsheetml/main"
	nsR       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRStrict = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	nsM       = "http://schemas.openxmlformats.org/officeDocument/2006/math"
	nsMStrict = "http://purl.oclc.org/ooxml/officeDocument/math"
	nsMC      = "http://schemas.openxmlformats.org/markup-compatibility/2006"

	nsPackageRels  = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsContentTypes = "http://schemas.openxmlformats.org/package/2006/content-types"
)

// xmlHeadBytes is how much of a part is looked at for its encoding before
// the XML decoder starts.
const xmlHeadBytes = 1024

// ctxCheckEvery is how many tokens pass between two looks at the context.
const ctxCheckEvery = 1024

// maxTagBytes caps one start or end tag, from its "<" to its ">". The
// decoder builds every attribute of a start tag before the scanner can
// count them, at tens of bytes of memory for each five bytes of input
// (` a=""`), so a tag of millions of attributes would cost a gigabyte
// before the token caps saw it. No tag of a real document comes near.
const maxTagBytes = 1 << 20

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// xmlScanner reads the tokens of one part within the document's budget.
type xmlScanner struct {
	b      *budget
	dec    *xml.Decoder
	depth  int // elements open
	tokens int // tokens of this part
}

// newXMLScanner starts reading the XML in r. A byte order mark other than
// UTF-8's, a first character that is not ASCII-compatible or an encoding
// declaration other than UTF-8 refuses the part (xmlEncoding); a tag
// longer than maxTagBytes refuses it once the decoder reaches it
// (tooManyTokens).
func newXMLScanner(r io.Reader, b *budget) (*xmlScanner, error) {
	return newXMLScannerTags(r, b, maxTagBytes)
}

// newXMLScannerTags is newXMLScanner with tags capped at maxTag bytes.
func newXMLScannerTags(r io.Reader, b *budget, maxTag int) (*xmlScanner, error) {
	br := bufio.NewReaderSize(&tagGuard{r: r, limit: maxTag}, 4096)
	head, err := br.Peek(xmlHeadBytes)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		var ref *Refusal
		if errors.As(err, &ref) {
			return nil, ref
		}
		return nil, refuse(Damaged)
	}
	if bytes.HasPrefix(head, utf8BOM) {
		_, _ = br.Discard(len(utf8BOM))
		head = head[len(utf8BOM):]
	}
	if notUTF8(head) {
		return nil, &Refusal{Code: Unsupported, What: WhatXMLEncoding}
	}
	dec := xml.NewDecoder(br)
	dec.Strict = true
	dec.CharsetReader = nil
	dec.Entity = nil
	return &xmlScanner{b: b, dec: dec}, nil
}

// notUTF8 reports a part that starts like an encoding other than UTF-8: a
// UTF-16 or UTF-32 byte order mark or zero byte, or an XML declaration
// naming another encoding.
func notUTF8(head []byte) bool {
	switch {
	case len(head) >= 2 && (head[0] == 0xFE && head[1] == 0xFF || head[0] == 0xFF && head[1] == 0xFE):
		return true
	case len(head) >= 1 && head[0] == 0, len(head) >= 2 && head[1] == 0:
		return true
	}
	if !bytes.HasPrefix(head, []byte("<?xml")) {
		return false
	}
	end := bytes.Index(head, []byte("?>"))
	if end < 0 {
		return false
	}
	enc := declAttr(string(head[len("<?xml"):end]), "encoding")
	return enc != "" && !strings.EqualFold(enc, "utf-8")
}

// declAttr is the value of a pseudo-attribute of an XML declaration, read
// the way encoding/xml reads it, so that the two agree on the encoding.
func declAttr(decl, name string) string {
	param := name + "="
	i := 0
	var quote byte
	for i < len(decl) {
		sub := decl[i:]
		k := strings.Index(sub, param)
		if k < 0 || k+len(param) >= len(sub) {
			return ""
		}
		i += k + len(param) + 1
		if c := sub[k+len(param)]; c == '\'' || c == '"' {
			quote = c
			break
		}
	}
	if quote == 0 {
		return ""
	}
	j := strings.IndexByte(decl[i:], quote)
	if j < 0 {
		return ""
	}
	return decl[i : i+j]
}

// tagGuard passes the bytes of a part on to the decoder and stops at the
// first byte of a tag longer than limit: the bytes before it go through,
// then the read fails with tooManyTokens, so the decoder never holds more
// than limit bytes of one tag, however the bytes come in. It follows the
// markup only as far as it must to tell where each tag ends, the way the
// decoder reads it: a ">" in a quoted attribute value does not end a tag;
// comments (to "-->"), CDATA sections (to "]]>") and processing
// instructions (to "?>"), which may hold "<", ">" and quotes, are passed
// over whole and not capped; character data may hold anything but "<".
// A declaration (<!DOCTYPE …>) is passed over to its first ">" outside
// quotes; the scanner refuses any declaration, so where the guard takes
// one to end changes no document that is read.
type tagGuard struct {
	r      io.Reader
	limit  int // the longest tag let through, in bytes
	state  guardState
	tag    int  // bytes of the open tag, its "<" included
	quote  byte // the quote of the value being read, or 0
	b0, b1 byte // the last two bytes of a comment, CDATA section or instruction
	opened int  // bytes of "CDATA[" read after "<!["
	err    error
}

// guardState is where in the markup a tagGuard is.
type guardState uint8

const (
	guardText    guardState = iota // character data
	guardLT                        // after "<"
	guardBang                      // after "<!"
	guardDash                      // after "<!-"
	guardCDATAIn                   // after "<![" and guard.opened bytes of "CDATA["
	guardTag                       // in a start or end tag
	guardComment                   // in a comment
	guardCDATA                     // in a CDATA section
	guardPI                        // in a processing instruction
	guardDecl                      // in a declaration
)

const cdataOpen = "CDATA["

func (g *tagGuard) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.r.Read(p)
	if i := g.scan(p[:n]); i >= 0 {
		g.err = refuse(TooManyTokens)
		return i, g.err
	}
	return n, err
}

// scan follows the markup through p and returns the index of the byte
// that makes a tag longer than the limit, or -1.
func (g *tagGuard) scan(p []byte) int {
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch g.state {
		case guardText:
			j := bytes.IndexByte(p[i:], '<')
			if j < 0 {
				return -1
			}
			i += j
			g.state = guardLT
		case guardLT:
			switch c {
			case '!':
				g.state = guardBang
			case '?':
				g.state, g.b0 = guardPI, 0
			default:
				g.state, g.tag, g.quote = guardTag, 1, 0
				i-- // c is the tag's second byte
			}
		case guardBang:
			switch c {
			case '-':
				g.state = guardDash
			case '[':
				g.state, g.opened = guardCDATAIn, 0
			default:
				g.state, g.quote = guardDecl, 0
				i--
			}
		case guardDash:
			if c == '-' {
				g.state, g.b0, g.b1 = guardComment, 0, 0
			} else {
				// Not a comment, which the decoder refuses.
				g.state, g.quote = guardDecl, 0
				i--
			}
		case guardCDATAIn:
			if c != cdataOpen[g.opened] {
				// Not a CDATA section, which the decoder refuses.
				g.state, g.quote = guardDecl, 0
				i--
				break
			}
			if g.opened++; g.opened == len(cdataOpen) {
				g.state, g.b0, g.b1 = guardCDATA, 0, 0
			}
		case guardTag:
			if g.tag++; g.tag > g.limit {
				return i
			}
			if g.quoted(c) {
				g.state = guardText
			}
		case guardDecl:
			if g.quoted(c) {
				g.state = guardText
			}
		case guardComment:
			if g.b0 == '-' && g.b1 == '-' && c == '>' {
				g.state = guardText
			}
			g.b0, g.b1 = g.b1, c
		case guardCDATA:
			if g.b0 == ']' && g.b1 == ']' && c == '>' {
				g.state = guardText
			}
			g.b0, g.b1 = g.b1, c
		case guardPI:
			if g.b0 == '?' && c == '>' {
				g.state = guardText
			}
			g.b0 = c
		}
	}
	return -1
}

// quoted follows the quotes of a tag or declaration through c and reports
// whether c is the ">" that ends it.
func (g *tagGuard) quoted(c byte) bool {
	switch {
	case g.quote != 0:
		if c == g.quote {
			g.quote = 0
		}
	case c == '"' || c == '\'':
		g.quote = c
	case c == '>':
		return true
	}
	return false
}

// next returns the next start element, end element or character data.
// Comments and processing instructions are passed over; a DOCTYPE or any
// other markup declaration refuses the document. At the end of the part
// it returns io.EOF.
func (s *xmlScanner) next() (xml.Token, error) {
	for {
		tok, err := s.dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) && s.depth == 0 {
				return nil, io.EOF
			}
			return nil, s.fail(err)
		}
		n := 1
		if se, ok := tok.(xml.StartElement); ok {
			n += len(se.Attr)
		}
		s.tokens += n
		s.b.tokens += n
		if s.tokens > s.b.lim.MaxPartTokens || s.b.tokens > s.b.lim.MaxTokens {
			return nil, refuse(TooManyTokens)
		}
		if s.b.tokens%ctxCheckEvery < n {
			if err := s.b.ctx.Err(); err != nil {
				return nil, err
			}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s.depth++
			if s.depth > s.b.lim.MaxXMLDepth {
				return nil, refuse(TooDeep)
			}
			return t, nil
		case xml.EndElement:
			s.depth--
			return t, nil
		case xml.CharData:
			if s.depth > 0 {
				return t, nil
			}
		case xml.Directive:
			return nil, &Refusal{Code: Unsupported, What: WhatDoctype}
		}
	}
}

// fail turns an error of the decoder into the document's refusal: a
// refusal from below (a cap of the archive) stays one, the context's error
// stays itself, and anything else is damaged XML.
func (s *xmlScanner) fail(err error) error {
	var ref *Refusal
	if errors.As(err, &ref) {
		return ref
	}
	if cerr := s.b.ctx.Err(); cerr != nil {
		return cerr
	}
	// A declaration of another encoding after the start of the part (the
	// head check sees only the first one): encoding/xml says so in words.
	if strings.Contains(err.Error(), "CharsetReader") {
		return &Refusal{Code: Unsupported, What: WhatXMLEncoding}
	}
	return refuse(Damaged)
}

// root returns the root element. A part without one is damaged.
func (s *xmlScanner) root() (xml.StartElement, error) {
	for {
		tok, err := s.next()
		if errors.Is(err, io.EOF) {
			return xml.StartElement{}, refuse(Damaged)
		}
		if err != nil {
			return xml.StartElement{}, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se, nil
		}
	}
}

// child returns the next child element of the element being read, or nil
// once that element has ended. Character data between children is passed
// over. The caller reads each child to its end (skip, or child in a loop)
// before it asks for the next one.
func (s *xmlScanner) child() (*xml.StartElement, error) {
	for {
		tok, err := s.next()
		if err != nil {
			return nil, s.unexpectedEOF(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return &t, nil
		case xml.EndElement:
			return nil, nil
		}
	}
}

// skip reads the element just started to its end.
func (s *xmlScanner) skip() error {
	target := s.depth - 1
	for s.depth > target {
		if _, err := s.next(); err != nil {
			return s.unexpectedEOF(err)
		}
	}
	return nil
}

// text reads the element just started to its end and returns its own
// character data (not that of child elements), at most limit bytes of it
// cut at a character boundary; more reports that some was left out.
func (s *xmlScanner) text(limit int) (string, bool, error) {
	var b strings.Builder
	more := false
	level := s.depth
	for s.depth >= level {
		tok, err := s.next()
		if err != nil {
			return "", false, s.unexpectedEOF(err)
		}
		if cd, ok := tok.(xml.CharData); ok && s.depth == level && !more {
			more = appendCapped(&b, string(cd), limit)
		}
	}
	return b.String(), more, nil
}

// unexpectedEOF is err, except that the end of the part inside an element
// is damaged XML.
func (s *xmlScanner) unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return refuse(Damaged)
	}
	return err
}

// appendCapped appends s to b up to limit bytes in all, cut at a character
// boundary, and reports whether anything was left out.
func appendCapped(b *strings.Builder, s string, limit int) bool {
	room := limit - b.Len()
	if len(s) <= room {
		b.WriteString(s)
		return false
	}
	b.WriteString(runePrefix(s, room))
	return true
}

// isW, isS, isM and isMC report a name in the WordprocessingML,
// SpreadsheetML, Office Math or Markup Compatibility namespace.
func isW(n xml.Name) bool  { return n.Space == nsW || n.Space == nsWStrict }
func isS(n xml.Name) bool  { return n.Space == nsS || n.Space == nsSStrict }
func isM(n xml.Name) bool  { return n.Space == nsM || n.Space == nsMStrict }
func isMC(n xml.Name) bool { return n.Space == nsMC }

// wAttr is a WordprocessingML attribute (w:val, w:id, …); an unqualified
// one is taken too.
func wAttr(se xml.StartElement, local string) (string, bool) {
	return attr(se, local, nsW, nsWStrict, "")
}

// rAttr is a relationship id attribute (r:id).
func rAttr(se xml.StartElement, local string) (string, bool) {
	return attr(se, local, nsR, nsRStrict)
}

// xmlBool reads an XML Schema boolean the way OOXML writes on/off values:
// absent is def, "0", "false" and "off" are false, anything else is true.
func xmlBool(v string, present, def bool) bool {
	if !present {
		return def
	}
	switch strings.TrimSpace(v) {
	case "0", "false", "off":
		return false
	}
	return true
}

// flatten makes s one line for a table cell, a spreadsheet cell or a
// label: tabs, line breaks and the other line and paragraph separators
// become spaces.
func flatten(s string) string {
	if !strings.ContainsFunc(s, isBreak) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isBreak(r) {
			return ' '
		}
		return r
	}, s)
}

// isBreak reports a character that would break a line or a cell.
func isBreak(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// label is a name taken from the document (a sheet, an author), one line
// and at most maxLabelBytes long.
func label(s string) string {
	return strings.TrimSpace(flatten(runePrefix(strings.ToValidUTF8(s, string(utf8.RuneError)), maxLabelBytes)))
}

// maxLabelBytes caps a name taken from the document.
const maxLabelBytes = 256
