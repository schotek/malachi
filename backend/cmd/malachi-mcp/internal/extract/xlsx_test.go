// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// sheetSpec is one sheet of a workbook built by a test.
type sheetSpec struct {
	name, state string
	data        string      // the content of worksheet
	rels        [][4]string // the sheet's relationships
	kind        string      // its relationship type; "" is a worksheet
}

// xlsxSpec is a workbook built by a test.
type xlsxSpec struct {
	workbookPr string // attributes of workbookPr
	sheets     []sheetSpec
	sst        string // the content of sst; "" for no shared strings
	styles     string // the content of styleSheet; "" for no styles
}

const sheetNSDecl = `xmlns="` + nsS + `" xmlns:r="` + nsR + `"`

// xlsxFiles are the parts of a workbook.
func xlsxFiles(spec xlsxSpec) []zipEntry {
	var sheets strings.Builder
	rels := [][4]string{}
	var parts []zipEntry
	for i, sh := range spec.sheets {
		n := strconv.Itoa(i + 1)
		state := ""
		if sh.state != "" {
			state = ` state="` + sh.state + `"`
		}
		sheets.WriteString(`<sheet name="` + sh.name + `" sheetId="` + n + `"` + state + ` r:id="rIdS` + n + `"/>`)
		kind := sh.kind
		if kind == "" {
			kind = "worksheet"
		}
		rels = append(rels, [4]string{"rIdS" + n, kind, "worksheets/sheet" + n + ".xml", ""})
		parts = append(parts, file("xl/worksheets/sheet"+n+".xml", xmlDecl+`<worksheet `+sheetNSDecl+`>`+sh.data+`</worksheet>`))
		if len(sh.rels) > 0 {
			parts = append(parts, file("xl/worksheets/_rels/sheet"+n+".xml.rels", relsXML(sh.rels...)))
		}
	}
	if spec.sst != "" {
		rels = append(rels, [4]string{"rIdSST", "sharedStrings", "sharedStrings.xml", ""})
		parts = append(parts, file("xl/sharedStrings.xml", xmlDecl+`<sst `+sheetNSDecl+`>`+spec.sst+`</sst>`))
	}
	if spec.styles != "" {
		rels = append(rels, [4]string{"rIdStyles", "styles", "styles.xml", ""})
		parts = append(parts, file("xl/styles.xml", xmlDecl+`<styleSheet `+sheetNSDecl+`>`+spec.styles+`</styleSheet>`))
	}
	files := []zipEntry{
		file("[Content_Types].xml", contentTypesXML("/xl/workbook.xml", ctXlsxMain)),
		file("_rels/.rels", relsXML([4]string{"rId1", "officeDocument", "xl/workbook.xml", ""})),
		file("xl/workbook.xml", xmlDecl+`<workbook `+sheetNSDecl+`><workbookPr `+spec.workbookPr+`/><sheets>`+sheets.String()+`</sheets></workbook>`),
		file("xl/_rels/workbook.xml.rels", relsXML(rels...)),
	}
	return append(files, parts...)
}

// rows is sheetData with these rows.
func rows(rs ...string) string { return `<sheetData>` + strings.Join(rs, "") + `</sheetData>` }

const featuresXLSX = `--- sheet 1: Data ---
1	Kind	Value	Hidden column
2	percent	15.5%
3	date	2026-09-30
4	time	13:45:30
5	date and time	2026-09-30 08:15
6	elapsed	36:30
7	boolean	TRUE
8	big and small	1E+20	-0.000000001234	1234567.891
9	hidden row	42
10	error	#DIV/0!
11	two lines	line one line two
15					far cell E15
--- sheet 2: Secret (hidden) ---
1	hidden sheet text
--- sheet 3: Empty ---
--- comments of sheet 1: Data ---
A3 Eva Malá: The due date.`

const pricelistXLSX = `--- sheet 1: Ceník ---
1	Ceník elektřiny 2026 – Energie Pod Sněžkou, a.s.
2	Položka	Jednotka	Cena bez DPH	DPH 21 %	Cena s DPH	Roční množství	Roční částka
3	Cena silové elektřiny VT	Kč/MWh	2890	606.9	3496.9	1.2	3468
4	Cena silové elektřiny NT	Kč/MWh	2450	514.5	2964.5	3	7350
5	Stálý měsíční plat	Kč/měsíc	129	27.09	156.09	12	1548
6	Distribuční sazba D27d – VT	Kč/MWh	1832.15	384.75	2216.9	1.2	2198.58
7	Distribuční sazba D27d – NT	Kč/MWh	312.4	65.6	378	3	937.2
8	Měsíční plat za jistič 3×25 A	Kč/měsíc	412	86.52	498.52	12	4944
9	Cena za systémové služby	Kč/MWh	162.5	34.13	196.63	4.2	682.5
10	Poplatek za činnost operátora trhu	Kč/měsíc	4.9	1.03	5.93	12	58.8
11	Daň z elektřiny	Kč/MWh	28.3	5.94	34.24	4.2	118.86
12	Celkem za rok bez DPH						21305.94
13	Celkem za rok včetně DPH 21 %						25780.19
--- sheet 2: Odečty ---
1	Datum odečtu	VT [kWh]	NT [kWh]	Celkem [kWh]
2	2026-01-31	101.4	252.7	354.1
3	2026-02-28	95	240.3	335.3
4	2026-03-31	88.2	231.9	320.1
5	2026-04-30	70.6	190.4	261
--- sheet 3: Poznámky ---
1	Poznámka
2	Příliš žluťoučký kůň úpěl ďábelské ódy.
3	Žádost o změnu jističe podána 3. 9. 2026, vyřizuje Ing. Zdeňka Řeháková.`

func TestXLSXSamples(t *testing.T) {
	for _, c := range []struct {
		name, want string
		facts      Facts
	}{
		{"xlsx/pricelist.xlsx", pricelistXLSX, Facts{Sheets: 3, SheetsRead: 3, Rows: 21, Formulas: 33}},
		{"xlsx/features.xlsx", featuresXLSX, Facts{Sheets: 3, SheetsHidden: 1, SheetsRead: 3, Rows: 13, Formulas: 1, Comments: 1, HiddenContent: true}},
		// The same workbook in the 1904 date system: other serials, the
		// same dates.
		{"xlsx/dates1904.xlsx", strings.ReplaceAll(featuresXLSX, ": Data ---", ": Data1904 ---"),
			Facts{Sheets: 3, SheetsHidden: 1, SheetsRead: 3, Rows: 13, Formulas: 1, Comments: 1, HiddenContent: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := Extract(context.Background(), XLSX, readDocument(t, c.name), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			checkReaderResult(t, res, DefaultLimits())
			if res.Text != c.want {
				t.Errorf("text:\n%s\n--- want ---\n%s", res.Text, c.want)
			}
			if res.Facts != c.facts {
				t.Errorf("facts %+v, want %+v", res.Facts, c.facts)
			}
		})
	}
}

// valueStyles are the cell formats of TestXLSXValues: 0 General, 1 a
// percentage, 2 a date (built-in), 3 a custom time, 4 a custom date and
// time, 5 elapsed hours, 6 text, 7 a custom format replacing built-in 14.
const valueStyles = `<numFmts count="4"><numFmt numFmtId="164" formatCode="hh:mm:ss"/><numFmt numFmtId="165" formatCode="yyyy-mm-dd\ hh:mm"/>` +
	`<numFmt numFmtId="166" formatCode="[h]:mm"/><numFmt numFmtId="14" formatCode="0.00"/><numFmt numFmtId="164" formatCode="0%"/></numFmts>` +
	`<cellStyleXfs count="1"><xf numFmtId="10"/></cellStyleXfs>` +
	`<cellXfs count="8"><xf numFmtId="0"/><xf numFmtId="9"/><xf numFmtId="15"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="166"/><xf numFmtId="49"/><xf numFmtId="14"/></cellXfs>`

func TestXLSXValues(t *testing.T) {
	sst := `<si><t>plain</t></si>` +
		`<si><r><rPr><b/></rPr><t>rich </t></r><r><t xml:space="preserve">runs</t></r><rPh sb="0" eb="1"><t>PHONETIC</t></rPh><phoneticPr fontId="1"/></si>` +
		`<si><t>line_x000D__x000A_break_x0009_tab _x005F_x0041_ _xD83D__xDE00_ _xD800_ _x00zz_</t></si>` +
		`<si><t></t></si>`
	c := func(ref, attrs, inner string) string { return `<c r="` + ref + `"` + attrs + `>` + inner + `</c>` }
	v := func(s string) string { return `<v>` + s + `</v>` }
	for _, tc := range []struct {
		name, row, want string
		facts           Facts
	}{
		{"shared strings", c("A1", ` t="s"`, v("0")) + c("B1", ` t="s"`, v("1")) + c("C1", ` t="s"`, v("3")) + c("D1", ` t="s"`, v("0")),
			"1\tplain\trich runs\t\tplain", Facts{}},
		{"escapes and breaks in a cell", c("A1", ` t="s"`, v("2")),
			"1\tline  break tab _x0041_ " + string(rune(0x1F600)) + " " + string(rune(0xFFFD)) + " _x00zz_", Facts{}},
		{"unresolved shared strings", c("A1", ` t="s"`, v("4")) + c("B1", ` t="s"`, v("-1")) + c("C1", ` t="s"`, v("x")) + c("D1", ` t="s"`, "") + c("E1", "", v("1")),
			"1\t\t\t\t\t1", Facts{CellsUnresolved: 4}},
		{"inline and formula strings", c("A1", ` t="inlineStr"`, `<is><t>inline</t><rPh><t>no</t></rPh></is>`) + c("B1", ` t="inlineStr"`, `<is><r><t>a</t></r><r><t>b</t></r></is>`) +
			c("C1", ` t="str"`, `<f>A1&amp;"!"</f>`+v("inline!")),
			"1\tinline\tab\tinline!", Facts{Formulas: 1}},
		{"booleans, errors, ISO dates", c("A1", ` t="b"`, v("1")) + c("B1", ` t="b"`, v("0")) + c("C1", ` t="b"`, v("2")) + c("D1", ` t="e"`, v("#REF!")) + c("E1", ` t="d"`, v("2026-09-30T12:00:00")),
			"1\tTRUE\tFALSE\t2\t#REF!\t2026-09-30T12:00:00", Facts{}},
		{"numbers", c("A1", "", v("1234.5")) + c("B1", ` t="n"`, v("0.30000000000000004")) + c("C1", "", v("1E+20")) + c("D1", "", v("-0")) + c("E1", "", v("abc")) + c("F1", "", v(" 7 ")),
			"1\t1234.5\t0.3\t1E+20\t0\tabc\t7", Facts{}},
		{"number formats", c("A1", ` s="1"`, v("0.25")) + c("B1", ` s="2"`, v("46295")) + c("C1", ` s="3"`, v("0.5")) + c("D1", ` s="4"`, v("46295.75")) +
			c("E1", ` s="5"`, v("2.5")) + c("F1", ` s="6"`, v("12")) + c("G1", ` s="7"`, v("46295")) + c("H1", ` s="99"`, v("5")) + c("I1", ` s="x"`, v("6")),
			"1\t25%\t2026-09-30\t12:00:00\t2026-09-30 18:00\t60:00\t12\t46295\t5\t6", Facts{}},
		{"dates out of range stay numbers", c("A1", ` s="2"`, v("-1")) + c("B1", ` s="2"`, v("2958466")) + c("C1", ` s="2"`, v("60")),
			"1\t-1\t2958466\t1900-02-29", Facts{}},
		{"formulas", c("A1", "", `<f>1+1</f>`+v("2")) + c("B1", "", `<f>NOW()</f>`) + c("C1", "", `<f t="shared" si="0"/>`+v("3")) + c("D1", ` t="str"`, `<f>""</f><v></v>`),
			"1\t2\t\t3", Facts{Formulas: 3, Uncalculated: 1}},
		{"cells out of order, duplicates, missing references",
			c("C1", "", v("c")) + c("A1", "", v("a")) + c("C1", "", v("c2")) + `<c>` + v("d") + `</c>` + c("bad", "", v("e")) + c("B", "", v("f")),
			"1\ta\t\tc2\td\te\tf", Facts{}},
		{"lower-case reference", c("b1", "", v("b")), "1\t\tb", Facts{}},
		{"empty cells make no row", c("A1", "", "") + c("B1", ` t="s"`, v("3")), "", Facts{}},
		{"columns past the cap are dropped", c("A1", "", v("a")) + c("IV1", "", v("last")) + c("IW1", "", v("dropped")) + c("XFD1", "", v("dropped")) + c("IX1", "", "") + c("XFE1", "", v("ignored")) + c("ZZZZ1", "", v("ignored")),
			"1\ta" + strings.Repeat("\t", 255) + "last", Facts{ColumnsDropped: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := xlsxSpec{sst: sst, styles: valueStyles, sheets: []sheetSpec{{name: "S", data: rows(`<row r="1">` + tc.row + `</row>`)}}}
			res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(spec)...)
			want := "--- sheet 1: S ---"
			if tc.want != "" {
				want += "\n" + tc.want
			}
			if res.Text != want {
				t.Errorf("text %q\nwant %q", res.Text, want)
			}
			wantFacts := tc.facts
			wantFacts.Sheets, wantFacts.SheetsRead = 1, 1
			if tc.want != "" {
				wantFacts.Rows = 1
			}
			if res.Facts != wantFacts {
				t.Errorf("facts %+v, want %+v", res.Facts, wantFacts)
			}
		})
	}
}

func TestXLSXDateSystems(t *testing.T) {
	data := rows(`<row r="1"><c r="A1" s="1"><v>0</v></c><c r="B1" s="1"><v>60</v></c><c r="C1" s="1"><v>61</v></c></row>`)
	styles := `<cellXfs><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs>`
	for pr, want := range map[string]string{
		``:                          "1\t1899-12-31\t1900-02-29\t1900-03-01",
		`date1904="false"`:          "1\t1899-12-31\t1900-02-29\t1900-03-01",
		`date1904="1"`:              "1\t1904-01-01\t1904-03-01\t1904-03-02",
		`dateCompatibility="false"`: "1\t1899-12-30\t1900-02-28\t1900-03-01",
		`dateCompatibility="1"`:     "1\t1899-12-31\t1900-02-29\t1900-03-01",
		`date1904="true" dateCompatibility="false"`: "1\t1904-01-01\t1904-03-01\t1904-03-02",
	} {
		res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(xlsxSpec{workbookPr: pr, styles: styles, sheets: []sheetSpec{{name: "S", data: data}}})...)
		if got := strings.TrimPrefix(res.Text, "--- sheet 1: S ---\n"); got != want {
			t.Errorf("%s: %q, want %q", pr, got, want)
		}
	}
}

func TestXLSXSheetsAndRows(t *testing.T) {
	spec := xlsxSpec{sheets: []sheetSpec{
		{name: "First", data: `<sheetFormatPr defaultRowHeight="15"/><cols><col min="2" max="2" hidden="1"/><col min="4" max="1000000" hidden="true"/><col min="3" max="2" hidden="1"/></cols>` +
			rows(`<row r="2"><c r="A2"><v>1</v></c></row>`, `<row><c><v>2</v></c><c r="B3"><v>hiddencol</v></c></row>`,
				`<row r="10" hidden="1"><c r="A10"><v>hiddenrow</v></c></row>`, `<row r="5"><c><v>back</v></c></row>`,
				`<row r="0"><c><v>zero</v></c></row>`, `<row r="1048577"><c><v>too far</v></c></row>`, `<row><c><v>after</v></c></row>`,
				`<row r="7"><c r="C7"><v>visible</v></c></row>`)},
		{name: "Chart", kind: "chartsheet", data: ""},
		{name: "Very", state: "veryHidden", data: rows(`<row r="1"><c><v>v</v></c></row>`)},
		{name: "Dialog", kind: "dialogsheet"},
		{name: "Hidden but empty", state: "hidden", data: rows()},
		{name: "Zero height", data: `<sheetFormatPr zeroHeight="1"/>` + rows(`<row r="1"><c><v>default hidden</v></c></row>`, `<row r="2" hidden="0"><c><v>shown</v></c></row>`)},
		{name: "Tab\tand\nbreak", data: `<cols><col min="1" max="1"/></cols><unknown/>` + rows(`<row r="1"><c><v>x</v></c></row>`) + `<mergeCells><mergeCell ref="A1:B1"/></mergeCells>`},
	}}
	res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(spec)...)
	want := `--- sheet 1: First ---
2	1
3	2	hiddencol
10	hiddenrow
5	back
6	zero
7			visible
--- sheet 2: Very (hidden) ---
1	v
--- sheet 3: Hidden but empty (hidden) ---
--- sheet 4: Zero height ---
1	default hidden
2	shown
--- sheet 5: Tab and break ---
1	x`
	if res.Text != want {
		t.Errorf("text:\n%s\n--- want ---\n%s", res.Text, want)
	}
	if f := res.Facts; f.Sheets != 5 || f.SheetsHidden != 2 || f.SheetsRead != 5 || f.SheetsSkipped != 2 || f.Rows != 10 || !f.HiddenContent {
		t.Errorf("facts %+v", f)
	}
}

func TestXLSXHiddenContent(t *testing.T) {
	for name, c := range map[string]struct {
		sheet  sheetSpec
		hidden bool
	}{
		"hidden column used":     {sheetSpec{name: "S", data: `<cols><col min="2" max="3" hidden="1"/></cols>` + rows(`<row r="1"><c r="C1"><v>x</v></c></row>`)}, true},
		"hidden column unused":   {sheetSpec{name: "S", data: `<cols><col min="2" max="3" hidden="1"/></cols>` + rows(`<row r="1"><c r="D1"><v>x</v></c><c r="B1"/></row>`)}, false},
		"hidden row empty":       {sheetSpec{name: "S", data: rows(`<row r="1" hidden="1"><c r="A1"/></row>`, `<row r="2"><c><v>x</v></c></row>`)}, false},
		"hidden row used":        {sheetSpec{name: "S", data: rows(`<row r="1" hidden="true"><c r="A1"><v>x</v></c></row>`)}, true},
		"hidden sheet with rows": {sheetSpec{name: "S", state: "hidden", data: rows(`<row r="1"><c><v>x</v></c></row>`)}, true},
		"hidden sheet, empty":    {sheetSpec{name: "S", state: "veryHidden", data: rows(`<row r="1"><c/></row>`)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(xlsxSpec{sheets: []sheetSpec{c.sheet}})...)
			if res.Facts.HiddenContent != c.hidden {
				t.Errorf("hidden %v, want %v (%q)", res.Facts.HiddenContent, c.hidden, res.Text)
			}
		})
	}
}

func TestXLSXComments(t *testing.T) {
	comments := xmlDecl + `<comments ` + sheetNSDecl + `><authors><author>Eva Malá</author><author>Line&#10;Break</author></authors><commentList>` +
		`<comment ref="B3" authorId="0"><text><r><rPr><b/></rPr><t>Eva Malá:</t></r><r><t xml:space="preserve">&#10;Check the
total.</t></r></text></comment>` +
		`<comment ref="A1" authorId="1"><text><t>Second</t></text></comment>` +
		`<comment ref="C1" authorId="7"><text><t>No author</t><rPh><t>x</t></rPh></text></comment>` +
		`<comment ref="D1"><text/></comment></commentList></comments>`
	spec := xlsxSpec{sheets: []sheetSpec{
		{name: "One", data: rows(`<row r="1"><c><v>1</v></c></row>`), rels: [][4]string{
			{"rId1", "comments", "../comments1.xml", ""},
			{"rId2", "http://schemas.microsoft.com/office/2017/10/relationships/threadedComment", "../threadedComments/t1.xml", ""},
			{"rId3", "hyperlink", "https://example.com/never", "External"},
		}},
		{name: "Two", data: rows(`<row r="1"><c><v>2</v></c></row>`), rels: [][4]string{{"rId1", "comments", "../missing.xml", ""}}},
		{name: "Three", data: rows(), rels: [][4]string{{"rId1", "comments", "/xl/comments3.xml", ""}}},
	}}
	files := append(xlsxFiles(spec), file("xl/comments1.xml", comments),
		file("xl/comments3.xml", xmlDecl+`<comments `+sheetNSDecl+`><commentList><comment ref="A1"><text><t>On an empty sheet</t></text></comment></commentList></comments>`),
		file("xl/threadedComments/t1.xml", "<never/>"))
	res := extractFiles(t, XLSX, DefaultLimits(), files...)
	want := `--- sheet 1: One ---
1	1
--- sheet 2: Two ---
1	2
--- sheet 3: Three ---
--- comments of sheet 1: One ---
B3 Eva Malá: Eva Malá: Check the total.
A1 Line Break: Second
C1: No author
D1:
--- comments of sheet 3: Three ---
A1: On an empty sheet`
	if res.Text != want {
		t.Errorf("text:\n%s\n--- want ---\n%s", res.Text, want)
	}
	if res.Facts.Comments != 5 {
		t.Errorf("comments %d", res.Facts.Comments)
	}

	lim := DefaultLimits()
	lim.MaxNotes = 2
	res = extractFiles(t, XLSX, lim, files...)
	if !strings.HasSuffix(res.Text, "\nA1 Line Break: Second") || res.Facts.Comments != 2 || !res.Facts.Cut || res.Facts.CutAt != CutNotes {
		t.Errorf("notes cap: %q %+v", res.Text, res.Facts)
	}
}

func TestXLSXCaps(t *testing.T) {
	row := func(r int, cells ...string) string {
		var b strings.Builder
		b.WriteString(`<row r="` + strconv.Itoa(r) + `">`)
		for _, c := range cells {
			b.WriteString(`<c t="inlineStr"><is><t>` + c + `</t></is></c>`)
		}
		b.WriteString(`</row>`)
		return b.String()
	}
	three := []sheetSpec{
		{name: "A", data: rows(row(1, "a1"), row(2, "a2", "b2"), row(3, "a3"))},
		{name: "B", data: rows(row(1, "b1"), row(4, "b4"))},
		{name: "C", data: rows(row(1, "c1"))},
	}
	for _, c := range []struct {
		name  string
		lim   func(*Limits)
		want  string
		cut   string
		sheet int
		row   int
	}{
		{"sheets", func(l *Limits) { l.MaxSheets = 2 }, "--- sheet 1: A ---\n1\ta1\n2\ta2\tb2\n3\ta3\n--- sheet 2: B ---\n1\tb1\n4\tb4", CutSheets, 3, 0},
		{"rows", func(l *Limits) { l.MaxRowsPerSheet = 2 }, "--- sheet 1: A ---\n1\ta1\n2\ta2\tb2", CutRows, 1, 3},
		{"cells", func(l *Limits) { l.MaxCells = 5 }, "--- sheet 1: A ---\n1\ta1\n2\ta2\tb2\n3\ta3\n--- sheet 2: B ---\n1\tb1", CutCells, 2, 4},
		{"text in a row", func(l *Limits) { l.MaxTextBytes = 33 }, "--- sheet 1: A ---\n1\ta1\n2\ta2\tb2", CutTextBytes, 1, 3},
		{"text at a sheet line", func(l *Limits) { l.MaxTextBytes = 40 }, "--- sheet 1: A ---\n1\ta1\n2\ta2\tb2\n3\ta3", CutTextBytes, 2, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			lim := DefaultLimits()
			c.lim(&lim)
			res := extractFiles(t, XLSX, lim, xlsxFiles(xlsxSpec{sheets: three})...)
			f := res.Facts
			if res.Text != c.want || !f.Cut || f.CutAt != c.cut || f.CutSheet != c.sheet || f.CutRow != c.row {
				t.Errorf("text %q\nfacts %+v", res.Text, f)
			}
		})
	}
	t.Run("a value longer than the text", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxTextBytes = 64
		res := extractFiles(t, XLSX, lim, xlsxFiles(xlsxSpec{sheets: []sheetSpec{{name: "S", data: rows(row(1, "ok"), row(2, "x"+strings.Repeat(" ", 200)+"y"))}}})...)
		f := res.Facts
		if !strings.HasPrefix(res.Text, "--- sheet 1: S ---\n1\tok") || !f.Cut || f.CutAt != CutTextBytes || f.CutSheet != 1 || f.CutRow != 2 {
			t.Errorf("text %q\nfacts %+v", res.Text, f)
		}
	})
	t.Run("shared strings", func(t *testing.T) {
		lim := DefaultLimits()
		lim.MaxSharedStrings = 1
		spec := xlsxSpec{sst: `<si><t>kept</t></si><si><t>not kept</t></si>`, sheets: []sheetSpec{{name: "S",
			data: rows(`<row r="1"><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row>`)}}}
		res := extractFiles(t, XLSX, lim, xlsxFiles(spec)...)
		if res.Text != "--- sheet 1: S ---\n1\tkept" || res.Facts.CellsUnresolved != 1 {
			t.Errorf("text %q facts %+v", res.Text, res.Facts)
		}
	})
	t.Run("many cells in one row", func(t *testing.T) {
		// The same few columns written over and over, as many cells as a
		// row has columns: the row keeps one value per column.
		var b strings.Builder
		var last [3]int
		for i := range maxSheetCol {
			b.WriteString(`<c r="` + string(rune('A'+i%3)) + `1"><v>` + strconv.Itoa(i) + `</v></c>`)
			last[i%3] = i
		}
		res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(xlsxSpec{sheets: []sheetSpec{{name: "S", data: rows(`<row r="1">` + b.String() + `</row>`)}}})...)
		want := "--- sheet 1: S ---\n1\t" + strconv.Itoa(last[0]) + "\t" + strconv.Itoa(last[1]) + "\t" + strconv.Itoa(last[2])
		if res.Text != want || res.Facts.Cut {
			t.Errorf("text %q, want %q; facts %+v", res.Text, want, res.Facts)
		}
	})
	t.Run("more cells in a row than it has columns", func(t *testing.T) {
		// One cell repeated past the columns a row has (a scaled-down
		// row of a million copies of A1 naming a long shared string):
		// every cell element counts, so the reading stops there.
		var b strings.Builder
		for range maxSheetCol + 1 {
			b.WriteString(`<c r="A1" t="s"><v>0</v></c>`)
		}
		spec := xlsxSpec{sst: `<si><t>` + strings.Repeat(" ", 900) + `x</t></si>`,
			sheets: []sheetSpec{{name: "S", data: rows(`<row r="1">`+b.String()+`</row>`, `<row r="2"><c><v>2</v></c></row>`)}}}
		res := extractFiles(t, XLSX, DefaultLimits(), xlsxFiles(spec)...)
		f := res.Facts
		if res.Text != "--- sheet 1: S ---" || !f.Cut || f.CutAt != CutCells || f.CutSheet != 1 || f.CutRow != 1 || f.Rows != 0 {
			t.Errorf("text %q\nfacts %+v", res.Text, f)
		}
	})
}

// readXLSX reads a workbook built from files the way extractXLSX does and
// returns the reader, so that a test can look at what it counted.
func readXLSX(t *testing.T, lim Limits, files ...zipEntry) *xlsxReader {
	t.Helper()
	p, err := openPackage(context.Background(), buildZip(t, files...), lim)
	if err != nil {
		t.Fatal(err)
	}
	main, err := p.mainPart(XLSX)
	if err != nil {
		t.Fatal(err)
	}
	x := &xlsxReader{pkg: p, lim: lim, main: main, b: newTextBuilder(lim.MaxTextBytes)}
	if err := x.read(); err != nil && !errors.Is(err, errFull) {
		t.Fatal(err)
	}
	return x
}

func TestXLSXSharedStringInEveryRow(t *testing.T) {
	// One shared string of white space, nearly as long as the text may
	// be, in every row (a scaled-down workbook of a megabyte of spaces in
	// half a million rows): each row would cost the whole string and add
	// only its number to the text. The rows hand the text at most
	// xlsxLineBudget times MaxTextBytes, then the text is cut.
	lim := DefaultLimits()
	lim.MaxTextBytes = 1000
	var b strings.Builder
	for range 2000 {
		b.WriteString(`<row><c t="s"><v>0</v></c></row>`)
	}
	x := readXLSX(t, lim, xlsxFiles(xlsxSpec{sst: `<si><t>` + strings.Repeat(" ", 900) + `</t></si>`,
		sheets: []sheetSpec{{name: "S", data: rows(b.String())}}})...)
	if budget := xlsxLineBudget * lim.MaxTextBytes; x.lines > budget {
		t.Errorf("rows handed the text %d bytes, budget %d", x.lines, budget)
	}
	res := x.result()
	checkReaderResult(t, res, lim)
	// Each line is a number, a tab and the 900 spaces: four fit.
	f := res.Facts
	if res.Text != "--- sheet 1: S ---\n1\n2\n3\n4" || !f.Cut || f.CutAt != CutTextBytes || f.CutSheet != 1 || f.CutRow != 5 || f.Rows != 4 {
		t.Errorf("text %q\nfacts %+v", res.Text, f)
	}

	// The table holds each string as a cell shows it, flattened once.
	x = readXLSX(t, DefaultLimits(), xlsxFiles(xlsxSpec{sst: `<si><t>a&#10;b&#9;c_x000D_d</t></si><si><t>e</t></si>`,
		sheets: []sheetSpec{{name: "S", data: rows(`<row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row>`)}}})...)
	if s, _ := x.strs.get(0); s != "a b c d" {
		t.Errorf("shared string %q", s)
	}
	if res := x.result(); res.Text != "--- sheet 1: S ---\n1\ta b c d\te" {
		t.Errorf("text %q", res.Text)
	}
}

func TestXLSXStructure(t *testing.T) {
	base := xlsxSpec{sheets: []sheetSpec{{name: "S", data: rows(`<row r="1"><c><v>1</v></c></row>`)}}}
	for name, c := range map[string]struct {
		files []zipEntry
		want  Refusal
	}{
		"sheet part missing":      {withoutFile(xlsxFiles(base), "xl/worksheets/sheet1.xml"), Refusal{Code: Damaged}},
		"sheet relationship gone": {replaceFile(xlsxFiles(base), file("xl/_rels/workbook.xml.rels", relsXML())), Refusal{Code: Damaged}},
		"sheet outside": {replaceFile(xlsxFiles(base), file("xl/_rels/workbook.xml.rels", relsXML(
			[4]string{"rIdS1", "worksheet", "../../sheet1.xml", ""}))), Refusal{Code: Damaged}},
		"sheet external": {replaceFile(xlsxFiles(base), file("xl/_rels/workbook.xml.rels", relsXML(
			[4]string{"rIdS1", "worksheet", "https://example.com/sheet1.xml", "External"}))), Refusal{Code: Damaged}},
		"wrong workbook root": {replaceFile(xlsxFiles(base), file("xl/workbook.xml", xmlDecl+`<w:document `+wordNSDecl+`/>`)), Refusal{Code: Damaged}},
		"wrong sheet root":    {replaceFile(xlsxFiles(base), file("xl/worksheets/sheet1.xml", xmlDecl+`<chartsheet `+sheetNSDecl+`/>`)), Refusal{Code: Damaged}},
		"damaged styles":      {replaceFile(xlsxFiles(xlsxSpec{styles: "<cellXfs>", sheets: base.sheets}), file("xl/styles.xml", `<styleSheet `+sheetNSDecl+`><cellXfs>`)), Refusal{Code: Damaged}},
		"doctype in a sheet": {replaceFile(xlsxFiles(base), file("xl/worksheets/sheet1.xml",
			`<!DOCTYPE worksheet [<!ENTITY e SYSTEM "file:///etc/passwd">]><worksheet `+sheetNSDecl+`>&e;</worksheet>`)), Refusal{Code: Unsupported, What: WhatDoctype}},
	} {
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, XLSX, buildZip(t, c.files...), DefaultLimits(), c.want)
		})
	}
	// Strict SpreadsheetML reads the same.
	strict := strings.NewReplacer(nsS, nsSStrict, nsR, nsRStrict,
		"http://schemas.openxmlformats.org/officeDocument/2006/relationships/", "http://purl.oclc.org/ooxml/officeDocument/relationships/")
	var files []zipEntry
	for _, f := range xlsxFiles(xlsxSpec{sst: `<si><t>s</t></si>`, sheets: []sheetSpec{{name: "S", data: rows(`<row r="1"><c t="s"><v>0</v></c><c><v>2</v></c></row>`)}}}) {
		files = append(files, file(f.name, strict.Replace(string(f.body))))
	}
	if res := extractFiles(t, XLSX, DefaultLimits(), files...); res.Text != "--- sheet 1: S ---\n1\ts\t2" {
		t.Errorf("strict: %q", res.Text)
	}
}

func TestXLSXHelpers(t *testing.T) {
	for ref, want := range map[string]int{"A1": 1, "Z9": 26, "AA1": 27, "XFD1048576": 16384, "xfd1": 16384, "XFE1": 16385, "ZZZZZZZZ1": 16385} {
		if got, ok := parseCellColumn(ref); !ok || got != want {
			t.Errorf("parseCellColumn(%q) = %d, %v", ref, got, ok)
		}
	}
	for _, ref := range []string{"", "A", "1", "A0", "A-1", "$A$1", "A1B", "Ä1"} {
		if got, ok := parseCellColumn(ref); ok {
			t.Errorf("parseCellColumn(%q) = %d", ref, got)
		}
	}
	for s, want := range map[string]int{"1": 1, "1048576": 1048576, "1048577": 1048577, "99999999999999999999": 1048577} {
		if got, ok := parseIndex(s); !ok || got != want {
			t.Errorf("parseIndex(%q) = %d, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "0", "-1", "+1", "1.0", " 1"} {
		if _, ok := parseIndex(s); ok {
			t.Errorf("parseIndex(%q) accepted", s)
		}
	}
	for in, want := range map[string]string{
		"plain":                "plain",
		"_x0041_":              "A",
		"_x005F_x0041_":        "_x0041_",
		"_x0041":               "_x0041",
		"_X0041_":              "_X0041_",
		"_xDBFF_":              string(rune(0xFFFD)),
		"_xDC00__xD800_":       string(rune(0xFFFD)) + string(rune(0xFFFD)),
		"_xD83D__xDE00_ after": string(rune(0x1F600)) + " after",
		"a_x000A_b":            "a\nb",
	} {
		if got := decodeX(in); got != want {
			t.Errorf("decodeX(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXLSXDeterministic(t *testing.T) {
	for _, name := range []string{"xlsx/pricelist.xlsx", "xlsx/features.xlsx", "xlsx/dates1904.xlsx"} {
		data := readDocument(t, name)
		a, err1 := Extract(context.Background(), XLSX, data, DefaultLimits())
		b, err2 := Extract(context.Background(), XLSX, data, DefaultLimits())
		if err1 != nil || err2 != nil || a != b {
			t.Errorf("%s: two readings differ: %v %v", name, err1, err2)
		}
	}
}

func FuzzXLSX(f *testing.F) {
	f.Add([]byte(rows(`<row r="1"><c t="s"><v>0</v></c><c s="1"><v>46295.5</v></c></row>`)), []byte(`<si><t>x</t></si>`), []byte(valueStyles), false)
	f.Add([]byte(`<cols><col min="1" max="3" hidden="1"/></cols>`+rows(`<row hidden="1"><c><f>1</f></c><c t="inlineStr"><is><t>i</t></is></c></row>`)), []byte(nil), []byte(nil), false)
	f.Add(readDocument(f, "xlsx/pricelist.xlsx"), []byte(nil), []byte(nil), true)
	f.Add(readDocument(f, "xlsx/features.xlsx"), []byte(nil), []byte(nil), true)
	f.Fuzz(func(t *testing.T, sheet, sst, styles []byte, whole bool) {
		if whole {
			fuzzExtract(t, XLSX, sheet)
			return
		}
		spec := xlsxSpec{sst: string(sst) + " ", styles: string(styles) + " ", sheets: []sheetSpec{
			{name: "S", data: string(sheet), rels: [][4]string{{"rId1", "comments", "../comments1.xml", ""}}},
			{name: "H", state: "hidden", data: string(sheet)},
		}}
		files := append(xlsxFiles(spec), file("xl/comments1.xml", xmlDecl+`<comments `+sheetNSDecl+`><authors><author>a</author></authors><commentList><comment ref="A1" authorId="0"><text><t>c</t></text></comment></commentList></comments>`))
		fuzzExtract(t, XLSX, buildZip(t, stored(files)...))
	})
}
