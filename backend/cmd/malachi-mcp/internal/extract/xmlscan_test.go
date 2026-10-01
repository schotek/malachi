// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf16"
)

// scanAll reads data as one part to its end, the way the readers do, and
// returns the tokens' text (elements as <name>, character data as is).
func scanAll(data []byte, lim Limits) (string, error) {
	return scanReader(bytes.NewReader(data), lim, maxTagBytes)
}

// scanReader is scanAll over r, with tags capped at maxTag bytes.
func scanReader(r io.Reader, lim Limits, maxTag int) (string, error) {
	b := &budget{ctx: context.Background(), lim: lim}
	s, err := newXMLScannerTags(r, b, maxTag)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for {
		tok, err := s.next()
		if errors.Is(err, io.EOF) {
			return out.String(), nil
		}
		if err != nil {
			return out.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			out.WriteString("<" + t.Name.Local + ">")
		case xml.EndElement:
			out.WriteString("</" + t.Name.Local + ">")
		case xml.CharData:
			out.Write(t)
		}
	}
}

// utf16Bytes is s in UTF-16, little-endian with a byte order mark when bom.
func utf16Bytes(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

const billionLaughs = `<?xml version="1.0"?>
<!DOCTYPE lolz [
 <!ENTITY lol "lol">
 <!ENTITY lol1 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
 <!ENTITY lol2 "&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;">
 <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
 <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
 <!ENTITY lol5 "&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;">
 <!ENTITY lol6 "&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;">
 <!ENTITY lol7 "&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;">
 <!ENTITY lol8 "&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;">
 <!ENTITY lol9 "&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;">
]>
<lolz>&lol9;</lolz>`

const externalEntity = `<?xml version="1.0"?>
<!DOCTYPE foo [ <!ENTITY xxe SYSTEM "file:///etc/passwd"> ]>
<foo>&xxe;</foo>`

func TestXMLScanRefusals(t *testing.T) {
	deep := strings.Repeat("<a>", 129) + strings.Repeat("</a>", 129)
	for name, c := range map[string]struct {
		data string
		want Refusal
	}{
		"billion laughs":              {billionLaughs, Refusal{Code: Unsupported, What: WhatDoctype}},
		"external entity":             {externalEntity, Refusal{Code: Unsupported, What: WhatDoctype}},
		"doctype without subset":      {`<!DOCTYPE html><html/>`, Refusal{Code: Unsupported, What: WhatDoctype}},
		"declaration inside":          {`<a><!ENTITY x "y"></a>`, Refusal{Code: Unsupported, What: WhatDoctype}},
		"undefined entity":            {`<a>&nbsp;</a>`, Refusal{Code: Damaged}},
		"utf-16 declared":             {`<?xml version="1.0" encoding="UTF-16"?><a/>`, Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"latin-1 declared":            {`<?xml version='1.0' encoding='ISO-8859-1'?><a/>`, Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"utf-16 bom":                  {string(utf16Bytes(`<?xml version="1.0"?><a/>`, true)), Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"utf-16 without bom":          {string(utf16Bytes(`<a/>`, false)), Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"utf-16 big-endian bom":       {"\xFE\xFF\x00<\x00a\x00/\x00>", Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"encoding declared later":     {`<a><?xml version="1.0" encoding="latin1"?></a>`, Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		"too deep":                    {deep, Refusal{Code: TooDeep}},
		"mismatched tags":             {`<a><b></a></b>`, Refusal{Code: Damaged}},
		"unclosed":                    {`<a><b>`, Refusal{Code: Damaged}},
		"control character":           {"<a>\x01</a>", Refusal{Code: Damaged}},
		"invalid utf-8":               {"<a>\xff\xfe</a>", Refusal{Code: Damaged}},
		"xml 1.1":                     {`<?xml version="1.1"?><a/>`, Refusal{Code: Damaged}},
		"unquoted attribute (strict)": {`<a b=c/>`, Refusal{Code: Damaged}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := scanAll([]byte(c.data), DefaultLimits())
			if r := refusalOf(err); r == nil || *r != c.want {
				t.Fatalf("got %v, want %v", err, &c.want)
			}
		})
	}
}

func TestXMLScanAccepts(t *testing.T) {
	for name, c := range map[string]struct{ data, want string }{
		"plain":              {`<?xml version="1.0" encoding="utf-8"?><a>x<b/>y</a>`, "<a>x<b></b>y</a>"},
		"utf-8 bom":          {"\xEF\xBB\xBF<a>x</a>", "<a>x</a>"},
		"cdata and comments": {`<a><!-- c --><![CDATA[<x>]]><?pi data?></a>`, "<a><x></a>"},
		"character refs":     {`<a>&lt;&#x41;&#66;&amp;</a>`, "<a><AB&</a>"},
		"text outside root":  {"\n<a/>\n", "<a></a>"},
		"deep enough":        {strings.Repeat("<a>", 128) + strings.Repeat("</a>", 128), strings.Repeat("<a>", 128) + strings.Repeat("</a>", 128)},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := scanAll([]byte(c.data), DefaultLimits())
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestXMLScanTokenCaps(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxPartTokens = 100
	data := []byte("<a>" + strings.Repeat("<b/>", 60) + "</a>")
	if _, err := scanAll(data, lim); refusalOf(err) == nil || refusalOf(err).Code != TooManyTokens {
		t.Errorf("part cap: %v", err)
	}
	// Attributes count as tokens.
	data = []byte("<a" + attrs(120) + "/>")
	if _, err := scanAll(data, lim); refusalOf(err) == nil || refusalOf(err).Code != TooManyTokens {
		t.Errorf("attributes: %v", err)
	}
	// The document's cap counts across parts.
	lim = DefaultLimits()
	lim.MaxTokens = 150
	b := &budget{ctx: context.Background(), lim: lim}
	part := []byte("<a>" + strings.Repeat("<b/>", 40) + "</a>")
	var err error
	for range 3 {
		var s *xmlScanner
		if s, err = newXMLScanner(bytes.NewReader(part), b); err != nil {
			break
		}
		for err == nil {
			_, err = s.next()
		}
		if errors.Is(err, io.EOF) {
			err = nil
			continue
		}
		break
	}
	if r := refusalOf(err); r == nil || r.Code != TooManyTokens {
		t.Errorf("document cap: %v", err)
	}
}

func TestXMLScanTagCap(t *testing.T) {
	const limit = 64
	// Quotes and angle brackets, longer than a tag may be: markup that is
	// not a tag holds them without being capped, and text holds all but
	// "<".
	long := strings.Repeat(`"it's" <b> '>' `, 20)
	text := strings.Repeat(`"it's" > 1 '`, 20)
	fill := func(n int) string { return strings.Repeat("x", n) }
	for name, c := range map[string]struct {
		data, want string
		refused    bool
	}{
		"comment":                 {`<a><!--` + long + `--></a>`, "<a></a>", false},
		"cdata":                   {`<a><![CDATA[` + long + `]]></a>`, "<a>" + long + "</a>", false},
		"instruction":             {`<?xml version="1.0"?><a><?pi ` + long + `?></a>`, "<a></a>", false},
		"text":                    {`<a>` + text + `</a>`, "<a>" + text + "</a>", false},
		"quoted >":                {`<a b="x>y" c='>"'><d e="'"/></a>`, "<a><d></d></a>", false},
		"tag of the cap":          {`<a b="` + fill(limit-9) + `"/>`, "<a></a>", false},
		"end tag of the cap":      {`<a></a` + strings.Repeat(" ", limit-4) + `>`, "<a></a>", false},
		"tag past the cap":        {`<a b="` + fill(limit-8) + `"/>`, "", true},
		"attributes past the cap": {`<a>` + `<b` + strings.Repeat(` c=""`, 20) + `/></a>`, "", true},
		"end tag past the cap":    {`<a></a` + strings.Repeat(" ", limit-3) + `>`, "", true},
		"quote never closed":      {`<a b="` + fill(2*limit), "", true},
		"after long markup":       {`<a><!--` + long + `--><![CDATA[` + long + `]]><b c="` + fill(limit) + `"/></a>`, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			// The same whether the bytes come at once or one by one.
			for _, r := range []io.Reader{strings.NewReader(c.data), iotest.OneByteReader(strings.NewReader(c.data))} {
				got, err := scanReader(r, DefaultLimits(), limit)
				if c.refused {
					if ref := refusalOf(err); ref == nil || ref.Code != TooManyTokens {
						t.Errorf("got %v, want tooManyTokens", err)
					}
					continue
				}
				if err != nil || got != c.want {
					t.Errorf("got %q, %v; want %q", got, err, c.want)
				}
			}
		})
	}
	// The bytes before the one that makes a tag too long go through.
	g := &tagGuard{r: strings.NewReader("<a>text<b" + strings.Repeat(" ", 100) + "/>"), limit: limit}
	got, err := io.ReadAll(g)
	if ref := refusalOf(err); len(got) != len("<a>text")+limit || ref == nil || ref.Code != TooManyTokens {
		t.Errorf("read %d bytes, %v", len(got), err)
	}
}

func TestXMLScanHugeTag(t *testing.T) {
	// A start tag of a million bytes of attributes, as a package's
	// [Content_Types].xml: refused by its length before the decoder has
	// built more than a megabyte's worth of them.
	types := xmlDecl + `<Types xmlns="` + nsContentTypes + `"` + strings.Repeat(` a=""`, maxTagBytes/5+1) + `/>`
	if _, err := scanAll([]byte(types), DefaultLimits()); refusalOf(err) == nil || refusalOf(err).Code != TooManyTokens {
		t.Errorf("scan: %v", err)
	}
	files := replaceFile(docxFiles(para("x")), file("[Content_Types].xml", types))
	wantRefusal(t, DOCX, buildZip(t, files...), DefaultLimits(), Refusal{Code: TooManyTokens})
	// Real documents pass.
	for _, c := range []struct {
		f    Format
		name string
	}{
		{DOCX, "docx/features.docx"}, {DOCX, "docx/contract.docx"},
		{XLSX, "xlsx/features.xlsx"}, {XLSX, "xlsx/pricelist.xlsx"},
	} {
		if res, err := Extract(context.Background(), c.f, readDocument(t, c.name), DefaultLimits()); err != nil || res.Text == "" {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// attrs is n distinct attributes.
func attrs(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(" a")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + i/26)))
		b.WriteString(`="1"`)
	}
	return b.String()
}

func TestXMLScanContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &budget{ctx: ctx, lim: DefaultLimits()}
	s, err := newXMLScanner(strings.NewReader("<a>"+strings.Repeat("<b/>", 5000)+"</a>"), b)
	if err != nil {
		t.Fatal(err)
	}
	for err == nil {
		_, err = s.next()
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

func TestXMLScanHelpers(t *testing.T) {
	b := &budget{ctx: context.Background(), lim: DefaultLimits()}
	s, err := newXMLScanner(strings.NewReader(`<r><t>ab<x>skipped</x>cd</t><u><v/></u><w>tail</w></r>`), b)
	if err != nil {
		t.Fatal(err)
	}
	if root, err := s.root(); err != nil || root.Name.Local != "r" {
		t.Fatalf("root %v, %v", root, err)
	}
	c, err := s.child()
	if err != nil || c == nil || c.Name.Local != "t" {
		t.Fatalf("child %v, %v", c, err)
	}
	text, more, err := s.text(3)
	if err != nil || text != "abc" || !more {
		t.Errorf("text %q %v %v", text, more, err)
	}
	if c, err = s.child(); err != nil || c == nil || c.Name.Local != "u" {
		t.Fatalf("second child %v, %v", c, err)
	}
	if err := s.skip(); err != nil {
		t.Fatal(err)
	}
	if c, err = s.child(); err != nil || c == nil || c.Name.Local != "w" {
		t.Fatalf("third child %v, %v", c, err)
	}
	if err := s.skip(); err != nil {
		t.Fatal(err)
	}
	if c, err = s.child(); err != nil || c != nil {
		t.Fatalf("after the last child %v, %v", c, err)
	}
	if _, err := s.next(); !errors.Is(err, io.EOF) {
		t.Errorf("end: %v", err)
	}
}

func TestXMLHelpers(t *testing.T) {
	for _, c := range []struct {
		v       string
		present bool
		def     bool
		want    bool
	}{
		{"", false, true, true}, {"", false, false, false}, {"0", true, true, false},
		{"false", true, true, false}, {"off", true, true, false}, {"1", true, false, true},
		{"true", true, false, true}, {"on", true, false, true}, {" 0 ", true, true, false},
	} {
		if got := xmlBool(c.v, c.present, c.def); got != c.want {
			t.Errorf("xmlBool(%q, %v, %v) = %v", c.v, c.present, c.def, got)
		}
	}
	ls := string(rune(0x2028))
	if got := flatten("a\tb\nc\r\nd" + ls + "e\vf"); got != "a b c  d e f" {
		t.Errorf("flatten = %q", got)
	}
	if got := label("  name\nwith break  "); got != "name with break" {
		t.Errorf("label = %q", got)
	}
	if got := label(strings.Repeat("x", 1000)); len(got) != maxLabelBytes {
		t.Errorf("label length %d", len(got))
	}
	if got := label("bad\xffbyte"); got != "bad"+string(rune(0xFFFD))+"byte" {
		t.Errorf("label of invalid UTF-8 = %q", got)
	}
	if got := declAttr(` version="1.0" encoding='ascii'`, "encoding"); got != "ascii" {
		t.Errorf("declAttr = %q", got)
	}
	if got := declAttr(` version="1.0"`, "encoding"); got != "" {
		t.Errorf("declAttr without = %q", got)
	}
}

func FuzzXMLScan(f *testing.F) {
	for _, s := range []string{
		`<a>x</a>`, billionLaughs, externalEntity, `<?xml version="1.0" encoding="UTF-16"?><a/>`,
		"\xEF\xBB\xBF<a b='c'>&amp;</a>", `<a><b></a>`, strings.Repeat("<a>", 140),
		`<w:document xmlns:w="` + nsW + `"><w:body><w:p/></w:body></w:document>`,
	} {
		f.Add([]byte(s))
	}
	lim := DefaultLimits()
	lim.MaxPartTokens, lim.MaxTokens, lim.MaxXMLDepth = 10000, 10000, 32
	f.Fuzz(func(t *testing.T, data []byte) {
		got1, err1 := scanAll(data, lim)
		got2, err2 := scanAll(data, lim)
		if got1 != got2 || (err1 == nil) != (err2 == nil) {
			t.Fatalf("not deterministic: %q %v / %q %v", got1, err1, got2, err2)
		}
		if err1 != nil {
			r := refusalOf(err1)
			if r == nil || !r.Valid() {
				t.Fatalf("error that is not a known refusal: %v", err1)
			}
			if *r != *refusalOf(err2) {
				t.Fatalf("refusals differ: %v / %v", err1, err2)
			}
		}
	})
}
