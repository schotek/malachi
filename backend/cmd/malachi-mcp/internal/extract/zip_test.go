// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// documentsDir holds the checked-in sample documents.
var documentsDir = filepath.Join("..", "..", "..", "..", "testdata", "documents")

// readDocument reads a checked-in sample.
func readDocument(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(documentsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// zipEntry is one entry of a ZIP built by a test.
type zipEntry struct {
	name   string
	body   []byte
	method uint16 // zip.Deflate unless raw
	flags  uint16
	raw    bool // written as-is with method, whatever it is
}

// file is a deflated entry with a string body.
func file(name, body string) zipEntry {
	return zipEntry{name: name, body: []byte(body), method: zip.Deflate}
}

// buildZip writes the entries in order, duplicates and odd names included.
func buildZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		if e.raw {
			fh.CRC32 = crc32.ChecksumIEEE(e.body)
			fh.CompressedSize64 = uint64(len(e.body))
			fh.UncompressedSize64 = uint64(len(e.body))
			w, err := zw.CreateRaw(fh)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(e.body); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stored is files written without compression, which is cheaper to build
// (for fuzzing).
func stored(files []zipEntry) []zipEntry {
	out := make([]zipEntry, len(files))
	for i, f := range files {
		if !f.raw {
			f.method = zip.Store
		}
		out[i] = f
	}
	return out
}

// replaceFile is files with the entry named name replaced (or added).
func replaceFile(files []zipEntry, e zipEntry) []zipEntry {
	out := make([]zipEntry, 0, len(files)+1)
	done := false
	for _, f := range files {
		if f.name == e.name {
			f, done = e, true
		}
		out = append(out, f)
	}
	if !done {
		out = append(out, e)
	}
	return out
}

// withoutFile is files without the entry named name.
func withoutFile(files []zipEntry, name string) []zipEntry {
	var out []zipEntry
	for _, f := range files {
		if f.name != name {
			out = append(out, f)
		}
	}
	return out
}

const (
	xmlDecl     = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	relsNS      = `xmlns="http://schemas.openxmlformats.org/package/2006/relationships"`
	relTypeBase = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	wordNSDecl  = `xmlns:w="` + nsW + `" xmlns:r="` + nsR + `" xmlns:mc="` + nsMC + `" xmlns:m="` + nsM + `"`
)

// contentTypesXML is a [Content_Types].xml giving the main part mainType.
func contentTypesXML(mainPart, mainType string, overrides ...string) string {
	var b strings.Builder
	b.WriteString(xmlDecl + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="` + mainPart + `" ContentType="` + mainType + `"/>`)
	for i := 0; i+1 < len(overrides); i += 2 {
		b.WriteString(`<Override PartName="` + overrides[i] + `" ContentType="` + overrides[i+1] + `"/>`)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

// relsXML is a relationships part: id, type name (or a full URI), target,
// and "External" or "" for each relationship.
func relsXML(rels ...[4]string) string {
	var b strings.Builder
	b.WriteString(xmlDecl + `<Relationships ` + relsNS + `>`)
	for _, r := range rels {
		typ := r[1]
		if !strings.Contains(typ, ":") {
			typ = relTypeBase + typ
		}
		mode := ""
		if r[3] != "" {
			mode = ` TargetMode="` + r[3] + `"`
		}
		b.WriteString(`<Relationship Id="` + r[0] + `" Type="` + typ + `" Target="` + r[2] + `"` + mode + `/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// docxFiles are the parts of a DOCX whose w:body holds body; rels are the
// main part's relationships.
func docxFiles(body string, rels ...[4]string) []zipEntry {
	files := []zipEntry{
		file("[Content_Types].xml", contentTypesXML("/word/document.xml", ctDocxMain)),
		file("_rels/.rels", relsXML([4]string{"rId1", "officeDocument", "word/document.xml", ""})),
		file("word/document.xml", xmlDecl+`<w:document `+wordNSDecl+`><w:body>`+body+`</w:body></w:document>`),
	}
	if len(rels) > 0 {
		files = append(files, file("word/_rels/document.xml.rels", relsXML(rels...)))
	}
	return files
}

// wordPart is a WordprocessingML part with root element root.
func wordPart(root, content string) string {
	return xmlDecl + `<w:` + root + ` ` + wordNSDecl + `>` + content + `</w:` + root + `>`
}

// para is a paragraph of one run of text.
func para(text string) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

// refusalOf is the refusal err carries, nil for none.
func refusalOf(err error) *Refusal {
	var r *Refusal
	if errors.As(err, &r) {
		return r
	}
	return nil
}

// wantRefusal checks that extracting data gives the refusal want.
func wantRefusal(t *testing.T, f Format, data []byte, lim Limits, want Refusal) {
	t.Helper()
	res, err := Extract(context.Background(), f, data, lim)
	r := refusalOf(err)
	if r == nil || *r != want {
		t.Fatalf("got %v (text %q), want refusal %v", err, res.Text, &want)
	}
}

func TestContainerSignatures(t *testing.T) {
	cfb := append(append([]byte{}, cfbSignature...), make([]byte, 512)...)
	pdfThenZip := append([]byte("%PDF-1.7\n"), buildZip(t, docxFiles(para("x"))...)...)
	for name, c := range map[string]struct {
		data []byte
		want Refusal
	}{
		"cfb":                {cfb, Refusal{Code: OfficeCFB}},
		"pdf":                {[]byte("%PDF-1.7\n%%EOF\n"), Refusal{Code: WrongFormat}},
		"pdf with zip after": {pdfThenZip, Refusal{Code: WrongFormat}},
		"empty zip header":   {[]byte("PK\x03\x04"), Refusal{Code: Damaged}},
		"truncated zip":      {buildZip(t, docxFiles(para("x"))...)[:200], Refusal{Code: Damaged}},
		"plain text":         {[]byte("hello"), Refusal{Code: WrongFormat}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, f := range []Format{DOCX, XLSX} {
				wantRefusal(t, f, c.data, DefaultLimits(), c.want)
			}
		})
	}
}

// plus is files and more, in a slice of its own.
func plus(files []zipEntry, more ...zipEntry) []zipEntry {
	return append(append([]zipEntry{}, files...), more...)
}

func TestContainerEntries(t *testing.T) {
	base := docxFiles(para("x"))
	many := append([]zipEntry{}, base...)
	for i := range DefaultLimits().MaxZipEntries {
		many = append(many, zipEntry{name: "pad/" + strconv.Itoa(i), method: zip.Store})
	}
	for name, c := range map[string]struct {
		files []zipEntry
		want  Refusal
	}{
		"too many entries": {many, Refusal{Code: TooManyParts}},
		"encrypted entry": {plus(base[:2], zipEntry{name: "word/document.xml", body: []byte("x"),
			method: zip.Store, flags: 0x1, raw: true}), Refusal{Code: Unsupported, What: WhatZipEncryption}},
		"method 99": {plus(base[:2], zipEntry{name: "word/document.xml", body: []byte("x"),
			method: 99, raw: true}), Refusal{Code: Unsupported, What: WhatCompressionMethod}},
		"bzip2 elsewhere":       {plus(base, zipEntry{name: "x.bin", body: []byte("x"), method: 12, raw: true}), Refusal{Code: Unsupported, What: WhatCompressionMethod}},
		"duplicate":             {plus(base, base[2]), Refusal{Code: Damaged}},
		"duplicate by case":     {plus(base, file("Word/Document.XML", "x")), Refusal{Code: Damaged}},
		"duplicate by escape":   {plus(base, file("word/%64ocument.xml", "x")), Refusal{Code: Damaged}},
		"backslash":             {plus(base, file(`word\document.xml`, "x")), Refusal{Code: Damaged}},
		"climbs out":            {plus(base, file("../evil.xml", "x")), Refusal{Code: Damaged}},
		"dot segment":           {plus(base, file("word/./x.xml", "x")), Refusal{Code: Damaged}},
		"empty segment":         {plus(base, file("word//x.xml", "x")), Refusal{Code: Damaged}},
		"absolute":              {plus(base, file("/word/x.xml", "x")), Refusal{Code: Damaged}},
		"nul":                   {plus(base, file("word/x\x00.xml", "x")), Refusal{Code: Damaged}},
		"no content types":      {withoutFile(base, "[Content_Types].xml"), Refusal{Code: WrongFormat, What: WhatOtherZip}},
		"no package rels":       {withoutFile(base, "_rels/.rels"), Refusal{Code: Damaged}},
		"no main part":          {withoutFile(base, "word/document.xml"), Refusal{Code: Damaged}},
		"no officeDocument rel": {replaceFile(base, file("_rels/.rels", relsXML([4]string{"rId1", "styles", "word/document.xml", ""}))), Refusal{Code: WrongFormat, What: WhatOtherZip}},
		"two officeDocument rels": {replaceFile(base, file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "word/document.xml", ""},
			[4]string{"rId2", "officeDocument", "word/document.xml", ""}))), Refusal{Code: Damaged}},
		"duplicate rel id": {replaceFile(base, file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "word/document.xml", ""},
			[4]string{"rId1", "styles", "word/styles.xml", ""}))), Refusal{Code: Damaged}},
		"external main": {replaceFile(base, file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "word/document.xml", "External"}))), Refusal{Code: Damaged}},
		"main climbs out": {replaceFile(base, file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "../word/document.xml", ""}))), Refusal{Code: Damaged}},
		"main with scheme": {replaceFile(base, file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "file:///word/document.xml", ""}))), Refusal{Code: Damaged}},
		"duplicate override": {replaceFile(base, file("[Content_Types].xml", contentTypesXML("/word/document.xml", ctDocxMain,
			"/WORD/document.xml", ctDocxMain))), Refusal{Code: Damaged}},
	} {
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, DOCX, buildZip(t, c.files...), DefaultLimits(), c.want)
		})
	}
}

func TestContainerResolvesTargets(t *testing.T) {
	for name, files := range map[string][]zipEntry{
		"absolute target": replaceFile(docxFiles(para("ok")), file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "/word/document.xml", ""}))),
		"strict relationship type": replaceFile(docxFiles(para("ok")), file("_rels/.rels", relsXML(
			[4]string{"rId1", "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument", "word/document.xml", ""}))),
		"percent-escaped target": replaceFile(docxFiles(para("ok")), file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "word/doc%75ment.xml", ""}))),
		"target in other case": replaceFile(docxFiles(para("ok")), file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "Word/Document.xml", ""}))),
		"dot segments": replaceFile(docxFiles(para("ok")), file("_rels/.rels", relsXML(
			[4]string{"rId1", "officeDocument", "./x/../word/document.xml", ""}))),
		"directory entries": append([]zipEntry{{name: "word/", method: zip.Store}}, docxFiles(para("ok"))...),
		"utf-8 bom": replaceFile(docxFiles(""), file("word/document.xml",
			"\xEF\xBB\xBF"+xmlDecl+`<w:document `+wordNSDecl+`><w:body>`+para("ok")+`</w:body></w:document>`)),
		"prepended data": nil,
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte(nil)
			if files == nil {
				// A ZIP with data before its first entry other than
				// another local header: archive/zip finds the entries
				// through the central directory and its offset.
				z := buildZip(t, docxFiles(para("ok"))...)
				data = append([]byte("PK\x03\x04junk"), z...)
			} else {
				data = buildZip(t, files...)
			}
			res, err := Extract(context.Background(), DOCX, data, DefaultLimits())
			if name == "prepended data" {
				// Either reading is fine, as long as it is a clean answer.
				if err != nil && refusalOf(err) == nil {
					t.Fatalf("error %v", err)
				}
				return
			}
			if err != nil || res.Text != "ok" {
				t.Fatalf("got %q, %v", res.Text, err)
			}
		})
	}
}

func TestContentTypeGate(t *testing.T) {
	word := func(ct string) []byte {
		return buildZip(t, replaceFile(docxFiles(para("x")), file("[Content_Types].xml", contentTypesXML("/word/document.xml", ct)))...)
	}
	for name, c := range map[string]struct {
		f    Format
		data []byte
		want string
	}{
		"docm":              {DOCX, word("application/vnd.ms-word.document.macroEnabled.main+xml"), WhatMacroEnabled},
		"dotx":              {DOCX, word("application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml"), WhatTemplate},
		"dotm":              {DOCX, word("application/vnd.ms-word.template.macroEnabledTemplate.main+xml"), WhatTemplate},
		"xlsm":              {XLSX, word("application/vnd.ms-excel.sheet.macroEnabled.main+xml"), WhatMacroEnabled},
		"xltm":              {XLSX, word("application/vnd.ms-excel.template.macroEnabled.main+xml"), WhatTemplate},
		"xlsb":              {XLSX, word("application/vnd.ms-excel.sheet.binary.macroEnabled.main"), WhatBinaryWorkbook},
		"pptm":              {DOCX, word("application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml"), WhatPresentation},
		"docx as xlsx":      {XLSX, word(ctDocxMain), WhatOtherZip},
		"no content type":   {DOCX, word(""), WhatOtherZip},
		"generic xml":       {DOCX, word("application/xml"), WhatOtherZip},
		"dotx sample":       {DOCX, readDocument(t, "opc/template.dotx"), WhatTemplate},
		"xltx sample":       {XLSX, readDocument(t, "opc/template.xltx"), WhatTemplate},
		"pptx sample":       {DOCX, readDocument(t, "opc/presentation.pptx"), WhatPresentation},
		"pptx sample, xlsx": {XLSX, readDocument(t, "opc/presentation.pptx"), WhatPresentation},
		"odt sample":        {DOCX, readDocument(t, "opc/document.odt"), WhatOtherZip},
		"xlsx sample, docx": {DOCX, readDocument(t, "xlsx/pricelist.xlsx"), WhatOtherZip},
	} {
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, c.f, c.data, DefaultLimits(), Refusal{Code: WrongFormat, What: c.want})
		})
	}
	// The main type compares without regard to ASCII case and parameters.
	res, err := Extract(context.Background(), DOCX, word(strings.ToUpper(ctDocxMain)+"; charset=utf-8"), DefaultLimits())
	if err != nil || res.Text != "x" {
		t.Errorf("upper-case main type: %q, %v", res.Text, err)
	}
}

func TestContainerExpansion(t *testing.T) {
	big := para(strings.Repeat("a", 4000))
	lim := DefaultLimits()
	lim.MaxEntryBytes = 2000
	wantRefusal(t, DOCX, buildZip(t, docxFiles(big)...), lim, Refusal{Code: Expands})

	// Each part fits, all of them together do not.
	lim = DefaultLimits()
	lim.MaxExpandBytes = 3000
	files := docxFiles(para(strings.Repeat("b", 1500)), [4]string{"rIdN", "numbering", "numbering.xml", ""})
	files = append(files, file("word/numbering.xml", wordPart("numbering", strings.Repeat("<w:num/>", 200))))
	wantRefusal(t, DOCX, buildZip(t, files...), lim, Refusal{Code: Expands})

	// A stored entry whose header lies about its size: archive/zip
	// notices, the reader says damaged.
	z := buildZip(t, replaceFile(docxFiles(""), zipEntry{name: "word/document.xml", method: zip.Store,
		body: []byte(xmlDecl + `<w:document ` + wordNSDecl + `><w:body>` + para("x") + `</w:body></w:document>`)})...)
	z = corruptStoredSize(t, z, "word/document.xml")
	if _, err := Extract(context.Background(), DOCX, z, DefaultLimits()); refusalOf(err) == nil {
		t.Errorf("lying size: %v", err)
	}
}

// corruptStoredSize halves the sizes the central directory gives for name.
func corruptStoredSize(t *testing.T, z []byte, name string) []byte {
	t.Helper()
	sig := []byte("PK\x01\x02")
	for i := bytes.LastIndex(z, sig); i >= 0; i = bytes.LastIndex(z[:i], sig) {
		nameLen := int(z[i+28]) | int(z[i+29])<<8
		if string(z[i+46:i+46+nameLen]) != name {
			continue
		}
		out := append([]byte{}, z...)
		for _, off := range []int{i + 20, i + 24} {
			size := uint32(out[off]) | uint32(out[off+1])<<8 | uint32(out[off+2])<<16 | uint32(out[off+3])<<24
			size /= 2
			out[off], out[off+1], out[off+2], out[off+3] = byte(size), byte(size>>8), byte(size>>16), byte(size>>24)
		}
		return out
	}
	t.Fatalf("%s not in the central directory", name)
	return nil
}

func TestPartKey(t *testing.T) {
	for in, want := range map[string]string{
		"word/document.xml":     "word/document.xml",
		"/Word/Document.XML":    "word/document.xml",
		"media/image%201.png":   "media/image 1.png",
		"100%.png":              "100%.png",
		"a%2Fb":                 "a%2fb",
		"a%5cb":                 "a%5cb",
		"[Content_Types].xml":   "[content_types].xml",
		"x%zz":                  "x%zz",
		"word/_rels/a.xml.rels": "word/_rels/a.xml.rels",
	} {
		if got := partKey(in); got != want {
			t.Errorf("partKey(%q) = %q, want %q", in, got, want)
		}
	}
	// Only ASCII letters fold: the Kelvin sign stays what it is.
	kelvin := string(rune(0x212A)) + "elvin.xml"
	if got := partKey(kelvin); got != kelvin {
		t.Errorf("partKey(%q) = %q", kelvin, got)
	}
	eAcute := string(rune(0xE9))
	if got := partKey("%C3%A9t%C3%A9.xml"); got != eAcute+"t"+eAcute+".xml" {
		t.Errorf("percent-escaped UTF-8: %q", got)
	}
}

func TestResolveTarget(t *testing.T) {
	for _, c := range []struct{ source, target, want string }{
		{"", "word/document.xml", "word/document.xml"},
		{"word/document.xml", "styles.xml", "word/styles.xml"},
		{"word/document.xml", "/xl/x.xml", "xl/x.xml"},
		{"word/document.xml", "../customXml/item1.xml", "customxml/item1.xml"},
		{"word/document.xml", "../../escape.xml", ""},
		{"", "../escape.xml", ""},
		{"word/document.xml", "https://example.com/x", ""},
		{"word/document.xml", "mailto:a@example.com", ""},
		{"word/document.xml", "C:/Windows/win.ini", ""},
		{"word/document.xml", "//host/share", ""},
		{"word/document.xml", "media/a.png#frag", ""},
		{"word/document.xml", "media/a.png?q", ""},
		{"word/document.xml", `media\a.png`, ""},
		{"word/document.xml", "", ""},
		{"word/document.xml", ".", "word"},
		{"word/document.xml", "media/a:b.png", "word/media/a:b.png"},
	} {
		if got := resolveTarget(c.source, c.target); got != c.want {
			t.Errorf("resolveTarget(%q, %q) = %q, want %q", c.source, c.target, got, c.want)
		}
	}
	if got := relsKey("word/document.xml"); got != "word/_rels/document.xml.rels" {
		t.Errorf("relsKey = %q", got)
	}
	if got := relsKey(""); got != packageRelsKey {
		t.Errorf("relsKey of the package = %q", got)
	}
}

func TestMainTypeWhat(t *testing.T) {
	for _, c := range []struct {
		f    Format
		ct   string
		want string
	}{
		{DOCX, ctDocxMain, WhatNone},
		{XLSX, ctXlsxMain, WhatNone},
		{XLSX, " " + strings.ToUpper(ctXlsxMain) + " ", WhatNone},
		{DOCX, ctXlsxMain, WhatOtherZip},
		{DOCX, "application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml", WhatPresentation},
		{DOCX, "application/vnd.ms-word.document.macroEnabled.main+xml", WhatMacroEnabled},
		{XLSX, "application/vnd.ms-excel.addin.macroEnabled.main+xml", WhatMacroEnabled},
		{XLSX, "application/vnd.ms-excel.sheet.binary.macroEnabled.main", WhatBinaryWorkbook},
		{DOCX, "application/vnd.oasis.opendocument.text", WhatOtherZip},
		{DOCX, "text/plain; template", WhatOtherZip},
	} {
		if got := mainTypeWhat(c.f, c.ct); got != c.want {
			t.Errorf("mainTypeWhat(%s, %q) = %q, want %q", c.f, c.ct, got, c.want)
		}
	}
}
