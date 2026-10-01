// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// extractFiles builds a package from files and extracts it.
func extractFiles(t *testing.T, f Format, lim Limits, files ...zipEntry) Result {
	t.Helper()
	res, err := Extract(context.Background(), f, buildZip(t, files...), lim)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	checkReaderResult(t, res, lim)
	return res
}

// checkReaderResult holds a result to what any reader must give.
func checkReaderResult(t testing.TB, res Result, lim Limits) {
	t.Helper()
	if !utf8.ValidString(res.Text) {
		t.Errorf("text is not valid UTF-8")
	}
	if len(res.Text) > lim.MaxTextBytes {
		t.Errorf("text of %d bytes, cap %d", len(res.Text), lim.MaxTextBytes)
	}
	if err := res.Facts.Validate(); err != nil {
		t.Errorf("facts: %v", err)
	}
	if strings.Contains(res.Text, "\r") || strings.HasSuffix(res.Text, "\n") || strings.HasPrefix(res.Text, "\n") {
		t.Errorf("text not normalised: %q", res.Text)
	}
}

const featuresDOCX = `Meeting minutes
Present: Petr Svoboda, Jana Nováková.
Place: room 4.
Budget	12 500 EUR[footnote 1], approved by all members.
The deadline is thirty days.[comment 1]
Secret: this text is hiddenvisible again.
See the project page for details.[endnote 1]
- First point
  - Nested point
- Second point
- Numbered one
- Numbered two

Item	Owner	Due
Wide cell		Tall cell
Report second paragraph

Inner A	Inner B

The end.

--- comments ---
[comment 1] Petr Svoboda: Check with legal.

--- footnotes ---
[1] Excluding VAT.

--- endnotes ---
[1] Endnote text.

--- headers and footers ---
Fictional Ltd. – Meeting minutes

Page 1`

func TestDOCXSamples(t *testing.T) {
	res, err := Extract(context.Background(), DOCX, readDocument(t, "docx/features.docx"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	checkReaderResult(t, res, DefaultLimits())
	if res.Text != featuresDOCX {
		t.Errorf("features.docx:\n%s\n--- want ---\n%s", res.Text, featuresDOCX)
	}
	want := Facts{Comments: 1, Footnotes: 1, Endnotes: 1, HeadersFooters: 2, TrackedChanges: true, HiddenContent: true}
	if res.Facts != want {
		t.Errorf("facts %+v, want %+v", res.Facts, want)
	}
	if strings.Contains(res.Text, "never-in-the-text") || strings.Contains(res.Text, "twenty-one") {
		t.Errorf("a hyperlink target or deleted text came through")
	}

	res, err = Extract(context.Background(), DOCX, readDocument(t, "docx/contract.docx"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	checkReaderResult(t, res, DefaultLimits())
	for _, line := range []string{
		"Smlouva o sdružených službách dodávky elektřiny č. EPS-2026-047113\nDodavatel: Energie Pod Sněžkou",
		"\n\nPoložka\tJednotka\tCena bez DPH\tDPH 21 %\tCena s DPH\nSilová elektřina a služby dodavatele\nCena silové elektřiny VT\tKč/MWh\t2\u00a0890,00\t606,90\t3\u00a0496,90\n",
		"\nCelkem za rok bez DPH (odhad 4,2 MWh)\t\t\t\t21\u00a0305,94\n",
		"\n\n--- headers and footers ---\nEnergie Pod Sněžkou, a.s. · Smlouva o sdružených službách dodávky elektřiny č. EPS-2026-047113\n\nStrana 2 z 2",
	} {
		if !strings.Contains(res.Text, line) {
			t.Errorf("contract.docx lacks %q", line)
		}
	}
	if res.Facts != (Facts{HeadersFooters: 2}) {
		t.Errorf("contract facts %+v", res.Facts)
	}
}

func TestDOCXStrict(t *testing.T) {
	// The same document in the strict namespaces and relationship types:
	// the same text.
	zr, err := zip.NewReader(bytes.NewReader(readDocument(t, "docx/features.docx")), int64(len(readDocument(t, "docx/features.docx"))))
	if err != nil {
		t.Fatal(err)
	}
	strict := strings.NewReplacer(
		nsW, nsWStrict, nsR, nsRStrict, nsM, nsMStrict,
		"http://schemas.openxmlformats.org/officeDocument/2006/relationships/", "http://purl.oclc.org/ooxml/officeDocument/relationships/",
	)
	var files []zipEntry
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		files = append(files, file(f.Name, strict.Replace(b.String())))
	}
	res := extractFiles(t, DOCX, DefaultLimits(), files...)
	if res.Text != featuresDOCX {
		t.Errorf("strict:\n%s", res.Text)
	}
}

func TestDOCXBody(t *testing.T) {
	r := func(text string) string { return `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r>` }
	p := func(runs ...string) string { return `<w:p>` + strings.Join(runs, "") + `</w:p>` }
	fld := func(instr, result string) string {
		return `<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>` + instr + `</w:instrText></w:r>` +
			`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` + result + `<w:r><w:fldChar w:fldCharType="end"/></w:r>`
	}
	tc := func(props, content string) string {
		return `<w:tc><w:tcPr>` + props + `</w:tcPr>` + content + `</w:tc>`
	}
	for _, c := range []struct {
		name  string
		body  string
		want  string
		facts Facts
	}{
		{"paragraphs", p(r("Hello"), r(" world")) + `<w:p/>` + p(r("Two")), "Hello world\n\nTwo", Facts{}},
		{"blank runs collapse", p() + p() + p(r("a")) + p() + p() + p(r("b")) + p(), "a\n\nb", Facts{}},
		{"run content",
			p(`<w:r><w:t>a</w:t><w:tab/><w:t>b</w:t><w:br/><w:t>c</w:t><w:cr/><w:t>d</w:t><w:noBreakHyphen/><w:t>e</w:t><w:softHyphen/><w:t>f</w:t><w:sym w:font="Wingdings" w:char="F04A"/><w:t>g</w:t><w:ptab w:relativeTo="margin" w:alignment="right" w:leader="none"/><w:t>h</w:t></w:r>`),
			"a\tb\nc\nd-efg\th", Facts{}},
		{"line break inside w:t is a space", p(r("one\ntwo\r\nthree")), "one two three", Facts{}},
		{"trailing spaces trimmed", p(r("text   ")) + p(r("   indented")), "text\n   indented", Facts{}},
		{"hyperlink shows its text only",
			p(r("see "), `<w:hyperlink r:id="rIdLink" w:anchor="top" w:tooltip="https://tooltip.example">`+r("here")+`</w:hyperlink>`),
			"see here", Facts{}},
		{"simple field shows its result",
			p(`<w:fldSimple w:instr=" HYPERLINK &quot;https://evil.example&quot; ">` + r("result") + `</w:fldSimple>`), "result", Facts{}},
		{"complex field shows its result", p(r("page "), fld(" PAGE ", r("7")), r(" of 9")), "page 7 of 9", Facts{}},
		{"text in a field's instruction is left out",
			p(`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:t>instruction</w:t></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r>` + r("shown") + `<w:r><w:fldChar w:fldCharType="end"/></w:r>`),
			"shown", Facts{}},
		{"field nested in a result", p(fld(" IF ", r("a")+fld(" PAGE ", r("b"))+r("c"))), "abc", Facts{}},
		{"field nested in an instruction",
			p(`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>IF </w:instrText></w:r>` + fld(" PAGE ", r("hidden")) +
				`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` + r("outer") + `<w:r><w:fldChar w:fldCharType="end"/></w:r>`),
			"outer", Facts{}},
		{"unbalanced field ends", p(r("a"), `<w:r><w:fldChar w:fldCharType="end"/><w:fldChar w:fldCharType="separate"/></w:r>`, r("b")), "ab", Facts{}},
		{"containers are read through",
			`<w:sdt><w:sdtPr><w:alias w:val="alias"/><w:placeholder><w:docPart w:val="x"/></w:placeholder></w:sdtPr><w:sdtContent>` + p(r("sdt")) + `</w:sdtContent></w:sdt>` +
				p(`<w:smartTag w:uri="u" w:element="e"><w:smartTagPr/>`+r("smart")+`</w:smartTag>`, `<w:customXml w:element="c">`+r(" custom")+`</w:customXml>`) +
				`<w:customXml w:element="block">` + p(r("block")) + `</w:customXml>` +
				p(`<w:unknownElement><w:r><w:t>unknown</w:t></w:r></w:unknownElement>`, `<x:foreign xmlns:x="urn:x">`+r(" foreign")+`</x:foreign>`),
			"sdt\nsmart custom\nblock\nunknown foreign", Facts{}},
		{"tracked changes shown as accepted",
			p(r("kept "), `<w:ins w:id="1" w:author="A">`+r("inserted ")+`</w:ins>`,
				`<w:del w:id="2" w:author="A"><w:r><w:delText>deleted </w:delText></w:r></w:del>`,
				`<w:moveFrom w:id="3"><w:r><w:t>moved away </w:t></w:r></w:moveFrom>`,
				`<w:moveTo w:id="4">`+r("moved here")+`</w:moveTo>`),
			"kept inserted moved here", Facts{TrackedChanges: true}},
		{"deleted table row",
			`<w:tbl><w:tr>` + tc("", p(r("a"))) + `</w:tr><w:tr><w:trPr><w:del w:id="1" w:author="A"/></w:trPr>` + tc("", p(r("gone"))) + `</w:tr></w:tbl>`,
			"a", Facts{TrackedChanges: true}},
		{"formatting changes are no tracked text",
			p(`<w:r><w:rPr><w:b/><w:rPrChange w:id="1" w:author="A"><w:rPr><w:vanish/></w:rPr></w:rPrChange></w:rPr><w:t>bold</w:t></w:r>`),
			"bold", Facts{}},
		{"alternate content: the first choice",
			p(r("a"), `<mc:AlternateContent><mc:Choice Requires="wps">`+r("choice")+`</mc:Choice><mc:Choice Requires="x">`+r("second")+`</mc:Choice><mc:Fallback>`+r("fallback")+`</mc:Fallback></mc:AlternateContent>`, r("b")),
			"achoiceb", Facts{}},
		{"alternate content: a lone fallback",
			p(`<mc:AlternateContent><mc:Fallback>` + r("fallback") + `</mc:Fallback></mc:AlternateContent>`), "fallback", Facts{}},
		{"text box after its paragraph",
			p(r("anchor"), `<w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:anchor xmlns:wp="urn:wp"><wp:docPr descr="alt text never shown"/><a:graphic xmlns:a="urn:a"><a:graphicData><wps:wsp xmlns:wps="urn:wps"><wps:txbx><w:txbxContent>`+
				p(r("in the box"))+p(r("second box line"))+`</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></mc:Choice><mc:Fallback><w:pict><v:shape xmlns:v="urn:v"><v:textbox><w:txbxContent>`+
				p(r("fallback copy"))+`</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback></mc:AlternateContent></w:r>`, r(" continues")) + p(r("next")),
			"anchor continues\nin the box\nsecond box line\nnext", Facts{}},
		{"math", p(r("x = "), `<m:oMath><m:r><m:t>a</m:t></m:r><m:r><w:rPr><w:vanish/></w:rPr><m:t>+b</m:t></m:r></m:oMath>`), "x = a+b", Facts{}},
		{"hidden text", p(r("shown "), `<w:r><w:rPr><w:vanish/></w:rPr><w:t>hidden</w:t></w:r>`), "shown hidden", Facts{HiddenContent: true}},
		{"specVanish and webHidden", p(`<w:r><w:rPr><w:specVanish w:val="true"/></w:rPr><w:t>a</w:t></w:r>`), "a", Facts{HiddenContent: true}},
		{"vanish switched off", p(`<w:r><w:rPr><w:vanish w:val="0"/><w:webHidden w:val="false"/></w:rPr><w:t>visible</w:t></w:r>`), "visible", Facts{}},
		{"hidden blank", p(r("x"), `<w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">   </w:t></w:r>`), "x", Facts{}},
		{"hidden paragraph mark only", p(`<w:pPr><w:rPr><w:vanish/></w:rPr></w:pPr>`, r("mark")), "mark", Facts{}},
		{"text outside a paragraph is not read", `<w:r><w:t>loose</w:t></w:r>` + p(r("inside")), "inside", Facts{}},
		{"section properties and tab stops are no text",
			p(`<w:pPr><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs><w:sectPr><w:pgSz w:w="1"/></w:sectPr></w:pPr>`, r("a")) + `<w:sectPr><w:pgSz/></w:sectPr>`,
			"a", Facts{}},
		{"table",
			p(r("before")) + `<w:tbl><w:tblPr><w:tblStyle w:val="x"/></w:tblPr><w:tblGrid><w:gridCol/></w:tblGrid>` +
				`<w:tr><w:trPr><w:tblHeader/></w:trPr>` + tc("", p(r("A"))) + tc("", p(r("B"))) + tc("", p(r("C"))) + `</w:tr>` +
				`<w:tr>` + tc(`<w:gridSpan w:val="2"/>`, p(r("wide"))) + tc(`<w:vMerge w:val="restart"/>`, p(r("tall"))) + `</w:tr>` +
				`<w:tr>` + tc("", p(r("one"))+p()+p(r("two"))) + tc("", p(r("tab\there"))) + tc(`<w:vMerge/>`, p(r("continued"))) + `</w:tr>` +
				`<w:tr>` + tc("", p(r("x"))) + tc(`<w:vMerge w:val="continue"/>`, p(r("y"))) + tc("", "") + tc("", p(r("z"))) + `</w:tr>` +
				`</w:tbl>` + p(r("after")),
			"before\n\nA\tB\tC\nwide\t\ttall\none two\ttab here\nx\t\t\tz\n\nafter", Facts{}},
		{"huge grid span is capped",
			`<w:tbl><w:tr>` + tc(`<w:gridSpan w:val="1000000000"/>`, p(r("a"))) + tc("", p(r("b"))) + `</w:tr></w:tbl>`,
			"a" + strings.Repeat("\t", maxGridSpan) + "b", Facts{}},
		{"nested table after its row",
			`<w:tbl><w:tr>` + tc("", p(r("outer"))+`<w:tbl><w:tr>`+tc("", p(r("in1")))+tc("", p(r("in2")))+`</w:tr></w:tbl>`) + tc("", p(r("next"))) + `</w:tr>` +
				`<w:tr>` + tc("", p(r("row2"))) + `</w:tr></w:tbl>`,
			"outer\tnext\n\nin1\tin2\n\nrow2", Facts{}},
		{"rows inside a content control",
			`<w:tbl><w:sdt><w:sdtContent><w:tr><w:sdt><w:sdtContent>` + tc("", p(r("a"))) + `</w:sdtContent></w:sdt>` + tc("", p(r("b"))) + `</w:tr></w:sdtContent></w:sdt></w:tbl>`,
			"a\tb", Facts{}},
		{"cell outside a row", `<w:tbl>` + tc("", p(r("stray"))) + `</w:tbl>`, "stray", Facts{}},
		{"row outside a table", `<w:tr>` + tc("", p(r("stray"))) + `</w:tr>`, "stray", Facts{}},
		{"list in a table cell", `<w:tbl><w:tr>` + tc("", p(`<w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>`, r("item"))) + `</w:tr></w:tbl>`, "- item", Facts{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := docxFiles(c.body, [4]string{"rIdLink", "hyperlink", "https://evil.example/link", "External"},
				[4]string{"rIdNum", "numbering", "numbering.xml", ""})
			files = append(files, file("word/numbering.xml", wordPart("numbering", testNumbering)))
			res := extractFiles(t, DOCX, DefaultLimits(), files...)
			if res.Text != c.want {
				t.Errorf("text %q\nwant %q", res.Text, c.want)
			}
			if res.Facts != c.facts {
				t.Errorf("facts %+v, want %+v", res.Facts, c.facts)
			}
			if strings.Contains(res.Text, "evil") || strings.Contains(res.Text, "alt text") || strings.Contains(res.Text, "tooltip") {
				t.Errorf("an attribute came through: %q", res.Text)
			}
		})
	}
}

// testNumbering is numbering.xml content: list 1 has markers on levels 0
// to 2 (level 2 has no numFmt, which means decimal), list 2 none (as
// LibreOffice writes numbered headings), list 3 points to an abstract
// numbering that does not exist. The second abstract numbering 10 and the
// second list 1 are ignored: the first of each id counts.
const testNumbering = `<w:abstractNum w:abstractNumId="10"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="2"/></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="20"><w:lvl w:ilvl="0"><w:numFmt w:val="none"/></w:lvl></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="10"><w:lvl w:ilvl="0"><w:numFmt w:val="none"/></w:lvl></w:abstractNum>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="10"/></w:num>` +
	`<w:num w:numId="2"><w:abstractNumId w:val="20"/></w:num>` +
	`<w:num w:numId="3"><w:abstractNumId w:val="99"/></w:num>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="20"/></w:num>`

func TestDOCXLists(t *testing.T) {
	item := func(num, level, text string) string {
		return `<w:p><w:pPr><w:numPr><w:ilvl w:val="` + level + `"/><w:numId w:val="` + num + `"/></w:numPr></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	body := item("1", "0", "first") + item("1", "1", "nested") + item("1", "2", "level without format") +
		item("2", "0", "heading") + item("3", "0", "dangling") + item("0", "0", "numbering off") +
		item("1", "9", "level out of range") + item("1", "0", "") + `<w:p><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>no level</w:t></w:r></w:p>`
	files := docxFiles(body, [4]string{"rIdNum", "numbering", "numbering.xml", ""})
	res := extractFiles(t, DOCX, DefaultLimits(), append(files, file("word/numbering.xml", wordPart("numbering", testNumbering)))...)
	want := "- first\n  - nested\n    - level without format\nheading\ndangling\nnumbering off\n- level out of range\n\n- no level"
	if res.Text != want {
		t.Errorf("text %q\nwant %q", res.Text, want)
	}
	// Without a numbering part nothing is a list.
	res = extractFiles(t, DOCX, DefaultLimits(), docxFiles(body)...)
	if strings.Contains(res.Text, "- ") {
		t.Errorf("markers without numbering: %q", res.Text)
	}
}

// notesFiles is a document with comments, footnotes, endnotes, headers and
// footers.
func notesFiles(body string) []zipEntry {
	files := docxFiles(body,
		[4]string{"rIdC", "comments", "comments.xml", ""},
		[4]string{"rIdF", "footnotes", "footnotes.xml", ""},
		[4]string{"rIdE", "endnotes", "/word/endnotes.xml", ""},
		[4]string{"rIdH1", "header", "header1.xml", ""},
		[4]string{"rIdH2", "header", "header2.xml", ""},
		[4]string{"rIdH3", "header", "header3.xml", ""},
		[4]string{"rIdF1", "footer", "footer1.xml", ""},
		[4]string{"rIdX", "header", "https://example.com/h.xml", "External"},
		[4]string{"rIdUp", "footer", "../../up.xml", ""},
		[4]string{"rIdStyles", "styles", "styles.xml", ""},
	)
	return append(files,
		file("word/comments.xml", wordPart("comments",
			`<w:comment w:id="7" w:author="Eva&#10;Malá" w:date="2026-09-30T10:00:00Z">`+para("First comment.")+para("Second paragraph.")+`</w:comment>`+
				`<w:comment w:id="3" w:author="">`+para("No author.")+`</w:comment>`+
				`<w:comment w:id="9" w:author="Ann"><w:p/></w:comment>`)),
		file("word/footnotes.xml", wordPart("footnotes",
			`<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>`+
				`<w:footnote w:type="continuationSeparator" w:id="0"><w:p><w:r><w:continuationSeparator/></w:r></w:p></w:footnote>`+
				`<w:footnote w:id="1"><w:p><w:r><w:footnoteRef/></w:r><w:r><w:t xml:space="preserve"> Note one.</w:t></w:r></w:p></w:footnote>`+
				`<w:footnote w:id="2"><w:p><w:r><w:footnoteRef/></w:r><w:r><w:tab/><w:t>Note two</w:t></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t> hidden</w:t></w:r></w:p></w:footnote>`)),
		file("word/endnotes.xml", wordPart("endnotes",
			`<w:endnote w:id="5">`+para("End.")+`</w:endnote>`)),
		file("word/header1.xml", wordPart("hdr", para("Header text"))),
		file("word/header2.xml", wordPart("hdr", para("Header text"))),
		file("word/header3.xml", wordPart("hdr", `<w:p/>`)),
		file("word/footer1.xml", wordPart("ftr", `<w:p><w:r><w:t xml:space="preserve">Page </w:t></w:r>`+
			`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>1</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>`)),
		file("up.xml", wordPart("ftr", para("escaped"))),
		file("word/styles.xml", wordPart("styles", "")),
	)
}

const notesBody = `<w:p><w:r><w:t>Body</w:t></w:r><w:r><w:footnoteReference w:id="2"/></w:r><w:r><w:t xml:space="preserve"> and </w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r>` +
	`<w:r><w:endnoteReference w:id="5"/></w:r><w:commentRangeStart w:id="3"/><w:r><w:t xml:space="preserve"> commented</w:t></w:r><w:commentRangeEnd w:id="3"/><w:r><w:commentReference w:id="3"/></w:r>` +
	`<w:r><w:footnoteReference w:id="404"/></w:r></w:p>` +
	`<w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rIdH1"/><w:headerReference w:type="first" r:id="rIdX"/><w:footerReference w:type="default" r:id="rIdUp"/></w:sectPr></w:pPr></w:p>` +
	`<w:p><w:r><w:t>Second section</w:t></w:r></w:p>` +
	`<w:sectPr><w:headerReference w:type="default" r:id="rIdH2"/><w:headerReference w:type="even" r:id="rIdH3"/><w:footerReference w:type="default" r:id="rIdF1"/><w:headerReference w:type="first" r:id="rIdH1"/><w:headerReference r:id="rIdStyles"/><w:headerReference r:id="rIdMissing"/></w:sectPr>`

func TestDOCXNotes(t *testing.T) {
	res := extractFiles(t, DOCX, DefaultLimits(), notesFiles(notesBody)...)
	want := `Body[footnote 2] and [footnote 1][endnote 1] commented[comment 2]

Second section

--- comments ---
[comment 1] Eva Malá: First comment.
Second paragraph.
[comment 2] No author.
[comment 3] Ann:

--- footnotes ---
[1] Note one.
[2] Note two hidden

--- endnotes ---
[1] End.

--- headers and footers ---
Header text

Page 1`
	if res.Text != want {
		t.Errorf("text:\n%s\n--- want ---\n%s", res.Text, want)
	}
	wantFacts := Facts{Comments: 3, Footnotes: 2, Endnotes: 1, HeadersFooters: 2, HiddenContent: true}
	if res.Facts != wantFacts {
		t.Errorf("facts %+v, want %+v", res.Facts, wantFacts)
	}
	if strings.Contains(res.Text, "escaped") {
		t.Errorf("a footer outside the package was read")
	}
}

func TestDOCXCaps(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxTextBytes = 50
		res := extractFiles(t, DOCX, lim, docxFiles(para("first line of text")+para("second line of text")+para("third line, cut"))...)
		if res.Text != "first line of text\nsecond line of text" || !res.Facts.Cut || res.Facts.CutAt != CutTextBytes {
			t.Errorf("%q %+v", res.Text, res.Facts)
		}
	})
	t.Run("one huge paragraph", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxTextBytes = 100
		res := extractFiles(t, DOCX, lim, docxFiles(para(strings.Repeat("é", 500)))...)
		if len(res.Text) == 0 || len(res.Text) > 100 || !res.Facts.Cut || res.Facts.CutAt != CutTextBytes {
			t.Errorf("%d bytes %+v", len(res.Text), res.Facts)
		}
	})
	t.Run("a paragraph past the buffer", func(t *testing.T) {
		// The paragraph is longer than any buffer keeps, all of it spaces
		// after the cap: the text is cut even though the line that is
		// left fits.
		lim := DefaultLimits()
		lim.MaxTextBytes = 100
		res := extractFiles(t, DOCX, lim, docxFiles(para("x"+strings.Repeat(" ", 300)+"lost"))...)
		if res.Text != "x" || !res.Facts.Cut || res.Facts.CutAt != CutTextBytes {
			t.Errorf("%q %+v", res.Text, res.Facts)
		}
	})
	t.Run("notes", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxNotes = 2
		res := extractFiles(t, DOCX, lim, notesFiles(notesBody)...)
		if !strings.HasSuffix(res.Text, "[comment 2] No author.") || !res.Facts.Cut || res.Facts.CutAt != CutNotes ||
			res.Facts.Comments != 2 || res.Facts.Footnotes != 0 || res.Facts.HeadersFooters != 0 {
			t.Errorf("%q %+v", res.Text, res.Facts)
		}
		// The body still shows every note's number.
		if !strings.HasPrefix(res.Text, "Body[footnote 2] and [footnote 1][endnote 1]") {
			t.Errorf("body %q", res.Text)
		}
	})
	t.Run("headers and footers", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxHeadersFooters = 1
		res := extractFiles(t, DOCX, lim, notesFiles(notesBody)...)
		if !strings.HasSuffix(res.Text, "--- headers and footers ---\nHeader text") || res.Facts.HeadersFooters != 1 || res.Facts.Cut {
			t.Errorf("%q %+v", res.Text, res.Facts)
		}
	})
	t.Run("nested tables flattened", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxTableDepth = 2
		cell := func(s string) string { return `<w:tc>` + s + `</w:tc>` }
		table := func(cells ...string) string { return `<w:tbl><w:tr>` + strings.Join(cells, "") + `</w:tr></w:tbl>` }
		body := table(cell(para("L1")+table(cell(para("L2")+table(cell(para("L3a")), cell(para("L3b")+table(cell(para("L4")))))))), cell(para("L1b")))
		res := extractFiles(t, DOCX, lim, docxFiles(body)...)
		if res.Text != "L1\tL1b\n\nL2 L3a L3b L4" {
			t.Errorf("%q", res.Text)
		}
	})
}

func TestDOCXCutOnlyWhenTextIsLost(t *testing.T) {
	// A buffer holds MaxTextBytes+4 bytes. What it drops cuts the text
	// only when it is text that would be written: not white space, not a
	// row that is deleted or a cell continuing a vertical merge, not a
	// header that copies another.
	lim := DefaultLimits()
	lim.MaxTextBytes = 100
	spill := "y" + strings.Repeat(" ", 200) + "z" // "z" is past the buffer
	cell := func(props, text string) string {
		return `<w:tc><w:tcPr>` + props + `</w:tcPr>` + para(text) + `</w:tc>`
	}
	for _, c := range []struct {
		name, body, want string
		facts            Facts
	}{
		{"white space past the buffer", para("x" + strings.Repeat(" ", 300)), "x", Facts{}},
		{"white space and breaks past the buffer",
			`<w:p><w:r><w:t xml:space="preserve">x` + strings.Repeat(" ", 300) + `</w:t><w:br/><w:tab/></w:r></w:p>`, "x", Facts{}},
		{"a continued merge cell past the buffer",
			`<w:tbl><w:tr>` + cell(`<w:vMerge w:val="restart"/>`, "a") + `</w:tr><w:tr>` + cell(`<w:vMerge/>`, spill) + `</w:tr></w:tbl>`, "a", Facts{}},
		{"a deleted row past the buffer",
			`<w:tbl><w:tr>` + cell("", "a") + `</w:tr><w:tr><w:trPr><w:del w:id="1" w:author="A"/></w:trPr>` + cell("", spill) + `</w:tr></w:tbl>`,
			"a", Facts{TrackedChanges: true}},
		{"a shown cell past the buffer",
			`<w:tbl><w:tr>` + cell("", "a") + `</w:tr><w:tr>` + cell("", spill) + `</w:tr></w:tbl>`,
			"a\ny", Facts{Cut: true, CutAt: CutTextBytes}},
		{"a row past the buffer",
			`<w:tbl><w:tr>` + cell("", "a") + strings.Repeat(cell("", ""), 120) + cell("", "b") + `</w:tr></w:tbl>`,
			"a", Facts{Cut: true, CutAt: CutTextBytes}},
		{"a row's empty cells past the buffer",
			`<w:tbl><w:tr>` + cell("", "a") + strings.Repeat(cell("", ""), 120) + `</w:tr></w:tbl>`, "a", Facts{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := extractFiles(t, DOCX, lim, docxFiles(c.body)...)
			if res.Text != c.want || res.Facts != c.facts {
				t.Errorf("text %q, facts %+v; want %q, %+v", res.Text, res.Facts, c.want, c.facts)
			}
		})
	}

	t.Run("white space in notes", func(t *testing.T) {
		// The notes share one buffer: trailing white space of one does
		// not spend it.
		files := docxFiles(para("B"), [4]string{"rIdF", "footnotes", "footnotes.xml", ""})
		files = append(files, file("word/footnotes.xml", wordPart("footnotes",
			`<w:footnote w:id="1">`+para("n"+strings.Repeat(" ", 90))+`</w:footnote>`+
				`<w:footnote w:id="2">`+para("m"+strings.Repeat("o", 20))+`</w:footnote>`)))
		res := extractFiles(t, DOCX, lim, files...)
		want := "B\n\n--- footnotes ---\n[1] n\n[2] m" + strings.Repeat("o", 20)
		if res.Text != want || res.Facts != (Facts{Footnotes: 2}) {
			t.Errorf("text %q, facts %+v", res.Text, res.Facts)
		}
	})

	t.Run("a copy of a header", func(t *testing.T) {
		// Two headers fit the buffer; a copy of the first, read between
		// them, would not, and is left out without spending it.
		lim := DefaultLimits()
		lim.MaxTextBytes = 200
		h, f := strings.Repeat("h", 120), strings.Repeat("f", 20)
		body := para("B") + `<w:sectPr><w:headerReference w:type="default" r:id="rIdH1"/><w:headerReference w:type="first" r:id="rIdH2"/>` +
			`<w:footerReference w:type="default" r:id="rIdF1"/></w:sectPr>`
		files := docxFiles(body, [4]string{"rIdH1", "header", "header1.xml", ""}, [4]string{"rIdH2", "header", "header2.xml", ""},
			[4]string{"rIdF1", "footer", "footer1.xml", ""})
		files = append(files, file("word/header1.xml", wordPart("hdr", para(h))), file("word/header2.xml", wordPart("hdr", para(h))),
			file("word/footer1.xml", wordPart("ftr", para(f))))
		res := extractFiles(t, DOCX, lim, files...)
		want := "B\n\n--- headers and footers ---\n" + h + "\n\n" + f
		if res.Text != want || res.Facts != (Facts{HeadersFooters: 2}) {
			t.Errorf("text %q, facts %+v", res.Text, res.Facts)
		}
		// A second header that is no copy and does not fit cuts the text.
		files = replaceFile(files, file("word/header2.xml", wordPart("hdr", para(strings.Repeat("g", 120)))))
		res = extractFiles(t, DOCX, lim, files...)
		if !res.Facts.Cut || res.Facts.CutAt != CutTextBytes {
			t.Errorf("text %q, facts %+v", res.Text, res.Facts)
		}
	})
}

func TestDOCXStructure(t *testing.T) {
	for name, c := range map[string]struct {
		doc  string
		want *Refusal
		text string
	}{
		"wrong root":               {xmlDecl + `<w:notDocument ` + wordNSDecl + `/>`, refuse(Damaged), ""},
		"foreign root":             {xmlDecl + `<document><body/></document>`, refuse(Damaged), ""},
		"no body":                  {xmlDecl + `<w:document ` + wordNSDecl + `/>`, nil, ""},
		"empty part":               {"", refuse(Damaged), ""},
		"body cut short":           {xmlDecl + `<w:document ` + wordNSDecl + `><w:body><w:p><w:r><w:t>x</w:t>`, refuse(Damaged), ""},
		"doctype in body":          {xmlDecl + `<w:document ` + wordNSDecl + `><w:body><!DOCTYPE x>` + para("x") + `</w:body></w:document>`, &Refusal{Code: Unsupported, What: WhatDoctype}, ""},
		"second body ignored":      {xmlDecl + `<w:document ` + wordNSDecl + `><w:background/><w:body>` + para("one") + `</w:body><w:body>` + para("two") + `</w:body></w:document>`, nil, "one"},
		"trailing garbage ignored": {xmlDecl + `<w:document ` + wordNSDecl + `><w:body>` + para("one") + `</w:body></w:document><junk`, nil, "one"},
	} {
		t.Run(name, func(t *testing.T) {
			data := buildZip(t, replaceFile(docxFiles(""), file("word/document.xml", c.doc))...)
			res, err := Extract(context.Background(), DOCX, data, DefaultLimits())
			if c.want != nil {
				if r := refusalOf(err); r == nil || *r != *c.want {
					t.Fatalf("got %v, want %v", err, c.want)
				}
				return
			}
			if err != nil || res.Text != c.text {
				t.Fatalf("got %q, %v", res.Text, err)
			}
		})
	}
	// A damaged auxiliary part refuses the document like the body would.
	files := notesFiles(notesBody)
	files = replaceFile(files, file("word/footnotes.xml", `<w:footnotes `+wordNSDecl+`><w:footnote>`))
	wantRefusal(t, DOCX, buildZip(t, files...), DefaultLimits(), Refusal{Code: Damaged})
	// One with another root is left out.
	files = replaceFile(notesFiles(notesBody), file("word/comments.xml", wordPart("document", para("not comments"))))
	res := extractFiles(t, DOCX, DefaultLimits(), files...)
	if strings.Contains(res.Text, "not comments") || res.Facts.Comments != 0 {
		t.Errorf("comments part with another root: %q", res.Text)
	}
}

func TestDOCXDeterministic(t *testing.T) {
	for _, data := range [][]byte{
		readDocument(t, "docx/features.docx"),
		readDocument(t, "docx/contract.docx"),
		buildZip(t, notesFiles(notesBody)...),
	} {
		a, err1 := Extract(context.Background(), DOCX, data, DefaultLimits())
		b, err2 := Extract(context.Background(), DOCX, data, DefaultLimits())
		if err1 != nil || err2 != nil || a != b {
			t.Errorf("two readings differ: %v %v", err1, err2)
		}
	}
}

// hostileDocuments are documents built to attack a reader, with the
// refusal each must end in.
func hostileDocuments(t testing.TB) []struct {
	name string
	f    Format
	data []byte
	want Refusal
} {
	deep := strings.Repeat("<w:sdt><w:sdtContent>", 100) + para("deep") + strings.Repeat("</w:sdtContent></w:sdt>", 100)
	bomb := bytes.Repeat([]byte{' '}, int(DefaultLimits().MaxEntryBytes)+1)
	laughs := strings.Replace(billionLaughs, "<lolz>&lol9;</lolz>", `<w:document `+wordNSDecl+`><w:body><w:p><w:r><w:t>&lol9;</w:t></w:r></w:p></w:body></w:document>`, 1)
	return []struct {
		name string
		f    Format
		data []byte
		want Refusal
	}{
		{"billion laughs", DOCX, buildZip(t, replaceFile(docxFiles(""), file("word/document.xml", laughs))...), Refusal{Code: Unsupported, What: WhatDoctype}},
		{"external entity", XLSX, buildZip(t, replaceFile(xlsxFiles(xlsxSpec{sst: "<si/>", sheets: []sheetSpec{{name: "S", data: ""}}}),
			file("xl/sharedStrings.xml", externalEntity))...), Refusal{Code: Unsupported, What: WhatDoctype}},
		{"deep nesting", DOCX, buildZip(t, docxFiles(deep)...), Refusal{Code: TooDeep}},
		{"part past its cap", DOCX, buildZip(t, replaceFile(docxFiles(""), zipEntry{name: "word/document.xml", body: bomb, method: zip.Deflate})...), Refusal{Code: Expands}},
		{"utf-16", XLSX, buildZip(t, replaceFile(xlsxFiles(xlsxSpec{sheets: []sheetSpec{{name: "S", data: ""}}}),
			zipEntry{name: "xl/workbook.xml", body: utf16Bytes(`<?xml version="1.0" encoding="UTF-16"?><workbook/>`, true), method: zip.Deflate})...), Refusal{Code: Unsupported, What: WhatXMLEncoding}},
		{"cfb", DOCX, append(append([]byte{}, cfbSignature...), make([]byte, 4096)...), Refusal{Code: OfficeCFB}},
		{"encrypted entry", XLSX, buildZip(t, plus(xlsxFiles(xlsxSpec{}), zipEntry{name: "x", body: []byte("x"), method: zip.Store, flags: 1, raw: true})...), Refusal{Code: Unsupported, What: WhatZipEncryption}},
	}
}

func TestHostileDocumentsInWorker(t *testing.T) {
	for _, c := range hostileDocuments(t) {
		t.Run(c.name, func(t *testing.T) {
			res, ref, out := runChild(t, c.f, c.data)
			if out != Refused || ref == nil || *ref != c.want {
				t.Fatalf("outcome %v, refusal %v, text %q; want %v", out, ref, res.Text, &c.want)
			}
		})
	}
}

func TestSamplesInWorker(t *testing.T) {
	for _, c := range []struct {
		f    Format
		name string
		want string
	}{
		{DOCX, "docx/features.docx", featuresDOCX},
		{XLSX, "xlsx/features.xlsx", featuresXLSX},
	} {
		res, ref, out := runChild(t, c.f, readDocument(t, c.name))
		if out != OK || ref != nil || res.Text != c.want {
			t.Errorf("%s: outcome %v, refusal %v, text %q", c.name, out, ref, res.Text)
		}
	}
}

// fuzzLimits are small caps, so that fuzzing reaches them.
func fuzzLimits() Limits {
	lim := DefaultLimits()
	lim.MaxTextBytes = 4 << 10
	lim.MaxZipEntries = 64
	lim.MaxEntryBytes = 1 << 20
	lim.MaxExpandBytes = 2 << 20
	lim.MaxXMLDepth = 48
	lim.MaxPartTokens = 50_000
	lim.MaxTokens = 100_000
	lim.MaxSheets = 4
	lim.MaxRowsPerSheet = 64
	lim.MaxCells = 256
	lim.MaxColumns = 16
	lim.MaxSharedStrings = 128
	lim.MaxNotes = 8
	lim.MaxHeadersFooters = 4
	lim.MaxTableDepth = 3
	return lim
}

// fuzzExtract extracts data twice and checks what any extraction must
// hold to.
func fuzzExtract(t *testing.T, f Format, data []byte) {
	lim := fuzzLimits()
	a, err1 := Extract(context.Background(), f, data, lim)
	b, err2 := Extract(context.Background(), f, data, lim)
	if err1 != nil {
		r := refusalOf(err1)
		if r == nil || !r.Valid() {
			t.Fatalf("error that is not a known refusal: %v", err1)
		}
		if r2 := refusalOf(err2); r2 == nil || *r2 != *r {
			t.Fatalf("refusals differ: %v / %v", err1, err2)
		}
		return
	}
	if err2 != nil || a != b {
		t.Fatalf("not deterministic")
	}
	checkReaderResult(t, a, lim)
}

func FuzzDOCX(f *testing.F) {
	f.Add([]byte(para("x")), []byte(nil), false)
	f.Add([]byte(notesBody), []byte(`<w:footnote w:id="1">`+para("n")+`</w:footnote>`), false)
	f.Add([]byte(`<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="3"/></w:tcPr><w:p/></w:tc></w:tr></w:tbl>`), []byte(nil), false)
	f.Add([]byte(`<mc:AlternateContent><mc:Choice>`+para("a")+`</mc:Choice></mc:AlternateContent>`), []byte(nil), false)
	f.Add(readDocument(f, "docx/features.docx"), []byte(nil), true)
	f.Add(readDocument(f, "docx/contract.docx"), []byte(nil), true)
	f.Fuzz(func(t *testing.T, body, notes []byte, whole bool) {
		if whole {
			fuzzExtract(t, DOCX, body)
			return
		}
		files := notesFiles(string(body))
		files = replaceFile(files, file("word/footnotes.xml", wordPart("footnotes", string(notes))))
		files = append(files, file("word/numbering.xml", wordPart("numbering", testNumbering)))
		files = replaceFile(files, file("word/_rels/document.xml.rels", relsXML(
			[4]string{"rIdF", "footnotes", "footnotes.xml", ""}, [4]string{"rIdN", "numbering", "numbering.xml", ""},
			[4]string{"rIdH1", "header", "header1.xml", ""}, [4]string{"rIdC", "comments", "comments.xml", ""})))
		fuzzExtract(t, DOCX, buildZip(t, stored(files)...))
	})
}
