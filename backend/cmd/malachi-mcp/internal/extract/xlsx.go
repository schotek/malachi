// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SpreadsheetML (XLSX) to text, design §4:
//
//	--- sheet 1: <name> ---
//	1	Name	Amount
//	2	Alice	1234.5
//	--- sheet 2: <name> (hidden) ---
//	--- comments of sheet 1: <name> ---
//	B3 <author>: <text>
//
// Worksheets in workbook order (chartsheets and dialogsheets are counted
// and skipped), one line per non-empty row: its number, then its cells
// from column A to the last non-empty one, tab-separated. Values as
// stored (numfmt.go), a formula's last calculated value and never the
// formula. Hidden sheets, rows and columns are included and flagged. Cell
// comments follow all the sheets.

// The largest row and column a worksheet has (XFD1048576); cells beyond
// them are not read. A row has a cell element for each column at most:
// one with more repeats columns, and the reading stops there (CutCells).
const (
	maxSheetRow = 1 << 20
	maxSheetCol = 16384
)

// xlsxLineBudget is how many times MaxTextBytes the rows may hand to the
// text in all, as lines. A line that fits loses only the white space at
// its end, so a workbook that comes this far before its text is full
// repeats values of little but white space (one shared string, a
// megabyte of spaces, in every row): its text is cut there rather than
// read on at a megabyte a row.
const xlsxLineBudget = 4

// extractXLSX turns an XLSX workbook into text.
func extractXLSX(ctx context.Context, data []byte, lim Limits) (Result, error) {
	p, err := openPackage(ctx, data, lim)
	if err != nil {
		return Result{}, err
	}
	main, err := p.mainPart(XLSX)
	if err != nil {
		return Result{}, err
	}
	x := &xlsxReader{pkg: p, lim: lim, main: main, b: newTextBuilder(lim.MaxTextBytes)}
	if err := x.read(); err != nil && !errors.Is(err, errFull) {
		return Result{}, err
	}
	return x.result(), nil
}

// xlsxSheet is a worksheet of the workbook.
type xlsxSheet struct {
	no     int // 1-based among the worksheets
	name   string
	hidden bool
	key    string
}

// xlsxReader reads one workbook.
type xlsxReader struct {
	pkg   *opcPackage
	lim   Limits
	main  string
	rels  rels
	b     *textBuilder
	facts Facts

	dates   dateSystem
	formats []numFormat // by cell format (the s attribute)
	strs    sharedStrings
	sheets  []*xlsxSheet
	done    []*xlsxSheet // the worksheets read, for their comments
	cells   int          // non-empty cells written
	lines   int          // bytes of the rows' lines handed to the text

	// Where the text was cut, set once by stop.
	cutAt            string
	cutSheet, cutRow int
}

// stop records that a cap left the rest out and ends the reading.
func (x *xlsxReader) stop(at string, sheet, row int) error {
	if x.cutAt == CutNone {
		x.cutAt, x.cutSheet, x.cutRow = at, sheet, row
	}
	return errFull
}

// valueLimit is the most one value or line is kept to: more than the text
// can ever take.
func (x *xlsxReader) valueLimit() int { return x.lim.MaxTextBytes + utf8.UTFMax }

// read reads the whole workbook into x.b and x.facts.
func (x *xlsxReader) read() error {
	var err error
	if x.rels, err = x.pkg.readRels(x.main); err != nil {
		return err
	}
	if err := x.workbook(); err != nil {
		return err
	}
	if err := x.styles(); err != nil {
		return err
	}
	if err := x.sharedStrings(); err != nil {
		return err
	}
	for _, sh := range x.sheets {
		if sh.no > x.lim.MaxSheets {
			return x.stop(CutSheets, sh.no, 0)
		}
		x.done = append(x.done, sh)
		x.facts.SheetsRead++
		if err := x.sheet(sh); err != nil {
			return err
		}
	}
	for _, sh := range x.done {
		if err := x.comments(sh); err != nil {
			return err
		}
	}
	return nil
}

// result is the text and the facts once reading has stopped.
func (x *xlsxReader) result() Result {
	f := x.facts
	switch {
	case x.cutAt != CutNone:
		f.Cut, f.CutAt, f.CutSheet, f.CutRow = true, x.cutAt, x.cutSheet, x.cutRow
	case x.b.Cut():
		f.Cut, f.CutAt = true, CutTextBytes
	}
	for _, n := range []*int{
		&f.Sheets, &f.SheetsHidden, &f.SheetsRead, &f.SheetsSkipped, &f.Rows,
		&f.ColumnsDropped, &f.Formulas, &f.Uncalculated, &f.CellsUnresolved, &f.Comments,
		&f.CutSheet, &f.CutRow,
	} {
		*n = min(*n, MaxFact)
	}
	return Result{Text: x.b.String(), Facts: f}
}

// --- the workbook ------------------------------------------------------------

// workbook reads workbook.xml: the date system and the sheets.
func (x *xlsxReader) workbook() error {
	s, done, err := x.pkg.scan(x.main)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isS(root.Name) || root.Name.Local != "workbook" {
		return refuse(Damaged)
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isS(se.Name) && se.Name.Local == "workbookPr":
			v, ok := attr(*se, "date1904", "")
			c, cok := attr(*se, "dateCompatibility", "")
			switch {
			case xmlBool(v, ok, false):
				x.dates = date1904
			case !xmlBool(c, cok, true):
				x.dates = date1900NoBug
			}
			err = s.skip()
		case isS(se.Name) && se.Name.Local == "sheets":
			err = x.sheetList(s)
		default:
			err = s.skip()
		}
		if err != nil {
			return err
		}
	}
}

// sheetList reads the sheets of the workbook. A sheet whose part is not in
// the package makes it damaged.
func (x *xlsxReader) sheetList(s *xmlScanner) error {
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if isS(se.Name) && se.Name.Local == "sheet" {
			id, _ := rAttr(*se, "id")
			l, ok := x.rels.target(id)
			if !ok || !x.pkg.has(l.key) {
				return refuse(Damaged)
			}
			if l.typ != "worksheet" {
				x.facts.SheetsSkipped++
			} else {
				name, _ := attr(*se, "name", "")
				state, _ := attr(*se, "state", "")
				sh := &xlsxSheet{
					no:     len(x.sheets) + 1,
					name:   label(name),
					hidden: state == "hidden" || state == "veryHidden",
					key:    l.key,
				}
				x.sheets = append(x.sheets, sh)
				x.facts.Sheets++
				if sh.hidden {
					x.facts.SheetsHidden++
				}
			}
		}
		if err := s.skip(); err != nil {
			return err
		}
	}
}

// styles reads the number format of each cell format from styles.xml.
func (x *xlsxReader) styles() error {
	key, ok := x.rels.first("styles")
	if !ok || !x.pkg.has(key) {
		return nil
	}
	s, done, err := x.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isS(root.Name) || root.Name.Local != "styleSheet" {
		return nil
	}
	// Number formats come before the cell formats that use them (the
	// schema's order); each format code is parsed once.
	custom := map[int]string{}
	parsed := map[int]numFormat{}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isS(se.Name) && se.Name.Local == "numFmts":
			err = eachChild(s, "numFmt", func(c xmlStart) {
				id, ok := atoiAttr(c, "numFmtId")
				if _, dup := custom[id]; ok && !dup {
					custom[id], _ = attr(c, "formatCode", "")
				}
			})
		case isS(se.Name) && se.Name.Local == "cellXfs":
			err = eachChild(s, "xf", func(c xmlStart) {
				id, _ := atoiAttr(c, "numFmtId")
				f, ok := parsed[id]
				if !ok {
					if code, isCustom := custom[id]; isCustom {
						f = parseFormat(code)
					} else {
						f = builtinFormat(id)
					}
					parsed[id] = f
				}
				x.formats = append(x.formats, f)
			})
		default:
			err = s.skip()
		}
		if err != nil {
			return err
		}
	}
}

// format is the number format of cell format s.
func (x *xlsxReader) format(s string) numFormat {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 || i >= len(x.formats) {
		return numFormat{}
	}
	return x.formats[i]
}

// sharedStrings is the shared string table: every string in one arena,
// with the end of each. The strings are kept the way a cell shows them,
// decoded and flattened, so that a string named by a million cells is
// looked at once, not once for each of them.
type sharedStrings struct {
	data string
	ends []int
}

func (t *sharedStrings) get(i int) (string, bool) {
	if i < 0 || i >= len(t.ends) {
		return "", false
	}
	start := 0
	if i > 0 {
		start = t.ends[i-1]
	}
	return t.data[start:t.ends[i]], true
}

// sharedStrings reads the shared string table, at most MaxSharedStrings
// of it; a cell naming one past those is unresolved.
func (x *xlsxReader) sharedStrings() error {
	key, ok := x.rels.first("sharedStrings")
	if !ok || !x.pkg.has(key) {
		return nil
	}
	s, done, err := x.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isS(root.Name) || root.Name.Local != "sst" {
		return nil
	}
	var arena strings.Builder
	defer func() { x.strs.data = arena.String() }()
	for len(x.strs.ends) < x.lim.MaxSharedStrings {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if !isS(se.Name) || se.Name.Local != "si" {
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		var b strings.Builder
		if err := x.richText(s, &b); err != nil {
			return err
		}
		arena.WriteString(flatten(decodeX(b.String())))
		x.strs.ends = append(x.strs.ends, arena.Len())
	}
	return nil
}

// richText reads the text of a rich-text element (si, is, a comment's
// text): its t, and the t of its runs; phonetic runs (rPh) are left out.
func (x *xlsxReader) richText(s *xmlScanner, b *strings.Builder) error {
	limit := x.valueLimit()
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isS(se.Name) && se.Name.Local == "t":
			t, _, err := s.text(limit - b.Len())
			if err != nil {
				return err
			}
			b.WriteString(t)
		case isS(se.Name) && se.Name.Local == "r":
			if err := eachChildErr(s, "t", func() error {
				t, _, err := s.text(limit - b.Len())
				b.WriteString(t)
				return err
			}); err != nil {
				return err
			}
		default:
			if err := s.skip(); err != nil {
				return err
			}
		}
	}
}

// --- worksheets --------------------------------------------------------------

// sheetState is what one worksheet's rows need.
type sheetState struct {
	no         int
	hidden     bool  // the sheet is hidden
	zeroHeight bool  // rows are hidden unless they say otherwise
	colDiff    []int // hidden column ranges, as a difference array
	hiddenCol  []bool
	rows       int // non-empty rows written

	// The row being read, by column (1..MaxColumns).
	vals     []string
	flags    []uint8
	used     []int
	maxCol   int
	dropped  int
	bytes    int
	overflow bool
}

// Cell flags.
const (
	cellFormula    = 1 << iota // a formula with its last calculated value
	cellUncalc                 // a formula never calculated
	cellUnresolved             // a shared string that does not exist
)

// sheet reads one worksheet into the text.
func (x *xlsxReader) sheet(sh *xlsxSheet) error {
	s, done, err := x.pkg.scan(sh.key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isS(root.Name) || root.Name.Local != "worksheet" {
		return refuse(Damaged)
	}
	header := "--- sheet " + strconv.Itoa(sh.no) + ": " + sh.name
	if sh.hidden {
		header += " (hidden)"
	}
	if !x.b.Lines(header + " ---") {
		return x.stop(CutTextBytes, sh.no, 0)
	}
	cols := min(x.lim.MaxColumns, maxSheetCol)
	st := &sheetState{
		no:      sh.no,
		hidden:  sh.hidden,
		colDiff: make([]int, cols+2),
		vals:    make([]string, cols+1),
		flags:   make([]uint8, cols+1),
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isS(se.Name) && se.Name.Local == "sheetFormatPr":
			v, ok := attr(*se, "zeroHeight", "")
			st.zeroHeight = xmlBool(v, ok, false)
			err = s.skip()
		case isS(se.Name) && se.Name.Local == "cols":
			err = eachChild(s, "col", func(c xmlStart) { st.hideCols(c) })
		case isS(se.Name) && se.Name.Local == "sheetData":
			st.hiddenCol = make([]bool, cols+1)
			for c, run := 1, 0; c <= cols; c++ {
				run += st.colDiff[c]
				st.hiddenCol[c] = run > 0
			}
			// The rest of the part (merged cells, hyperlinks, drawings)
			// is not read.
			return x.rows(s, sh, st)
		default:
			err = s.skip()
		}
		if err != nil {
			return err
		}
	}
}

// hideCols records a hidden w:col range.
func (st *sheetState) hideCols(c xmlStart) {
	v, ok := attr(c, "hidden", "")
	if !xmlBool(v, ok, false) {
		return
	}
	lo, ok1 := atoiAttr(c, "min")
	hi, ok2 := atoiAttr(c, "max")
	cols := len(st.colDiff) - 2
	if !ok1 || !ok2 || lo < 1 || hi < lo || lo > cols {
		return
	}
	st.colDiff[lo]++
	st.colDiff[min(hi, cols)+1]--
}

// rows reads sheetData row by row.
func (x *xlsxReader) rows(s *xmlScanner, sh *xlsxSheet, st *sheetState) error {
	last := 0
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if !isS(se.Name) || se.Name.Local != "row" {
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		r := last + 1
		if v, ok := attr(*se, "r", ""); ok {
			if n, ok := parseIndex(v); ok {
				r = n
			}
		}
		last = r
		hv, hok := attr(*se, "hidden", "")
		hidden := xmlBool(hv, hok, st.zeroHeight)
		if err := x.row(s, st, r); err != nil {
			return err
		}
		if r > maxSheetRow {
			st.reset()
			continue
		}
		if err := x.writeRow(st, r, hidden); err != nil {
			return err
		}
	}
}

// row reads the cells of row r into st. Every cell element counts, empty
// or not, and whether or not a later one of its column replaces it: a row
// with more of them than a row has columns ends the reading.
func (x *xlsxReader) row(s *xmlScanner, st *sheetState, r int) error {
	last, seen := 0, 0
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if !isS(se.Name) || se.Name.Local != "c" {
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		if seen++; seen > maxSheetCol {
			return x.stop(CutCells, st.no, r)
		}
		col := last + 1
		if v, ok := attr(*se, "r", ""); ok {
			if n, ok := parseCellColumn(v); ok {
				col = n
			}
		}
		last = col
		typ, _ := attr(*se, "t", "")
		style, _ := attr(*se, "s", "")
		value, flag, more, err := x.cell(s, typ, style)
		if err != nil {
			return err
		}
		switch {
		case col > maxSheetCol:
		case col >= len(st.vals):
			if value != "" {
				st.dropped++
			}
		default:
			st.set(col, value, flag, more, x.valueLimit())
		}
	}
}

// cell reads one c element and returns what it shows: the value, its
// flags, and whether the value was too long to keep whole.
func (x *xlsxReader) cell(s *xmlScanner, typ, style string) (string, uint8, bool, error) {
	var (
		v, inline        string
		hasV, hasF, more bool
		limit            = x.valueLimit()
	)
	for {
		se, err := s.child()
		if err != nil {
			return "", 0, false, err
		}
		if se == nil {
			break
		}
		switch {
		case isS(se.Name) && se.Name.Local == "v":
			var m bool
			if v, m, err = s.text(limit); err != nil {
				return "", 0, false, err
			}
			hasV, more = true, more || m
		case isS(se.Name) && se.Name.Local == "f":
			hasF = true
			err = s.skip()
		case isS(se.Name) && se.Name.Local == "is":
			var b strings.Builder
			err = x.richText(s, &b)
			inline = b.String()
		default:
			err = s.skip()
		}
		if err != nil {
			return "", 0, false, err
		}
	}
	var flag uint8
	if hasF {
		if hasV {
			flag |= cellFormula
		} else {
			flag |= cellUncalc
		}
	}
	var value string
	switch typ {
	case "s":
		// Shared strings are flattened already, once each.
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if str, ok := x.strs.get(i); hasV && err == nil && ok {
			value = str
		} else {
			flag |= cellUnresolved
		}
		return value, flag, more || len(value) > x.lim.MaxTextBytes, nil
	case "inlineStr":
		value = decodeX(inline)
	case "str":
		value = decodeX(v)
	case "b":
		switch strings.TrimSpace(v) {
		case "1":
			value = "TRUE"
		case "0":
			value = "FALSE"
		default:
			value = v
		}
	case "", "n":
		if hasV {
			value = formatNumeric(strings.TrimSpace(v), x.format(style), x.dates)
		}
	default: // "e" (an error), "d" (an ISO 8601 date), anything else: as stored
		value = v
	}
	return flatten(value), flag, more || len(value) > x.lim.MaxTextBytes, nil
}

// set puts a cell's value in its column; a later cell of the same column
// wins.
func (st *sheetState) set(col int, value string, flag uint8, more bool, limit int) {
	if st.flags[col] == 0 && st.vals[col] == "" {
		st.used = append(st.used, col)
	}
	st.bytes -= len(st.vals[col])
	if st.bytes+len(value) > limit {
		value = runePrefix(value, limit-st.bytes)
		more = true
	}
	st.bytes += len(value)
	st.vals[col] = value
	st.flags[col] = flag | 0x80 // set: a column with an empty value still counts once
	st.overflow = st.overflow || more
	if value != "" {
		st.maxCol = max(st.maxCol, col)
	}
}

// reset clears the row.
func (st *sheetState) reset() {
	for _, c := range st.used {
		st.vals[c], st.flags[c] = "", 0
	}
	st.used, st.maxCol, st.dropped, st.bytes, st.overflow = st.used[:0], 0, 0, 0, false
}

// writeRow writes the row read into st as one line, if it shows anything:
// unless a cap stops the text there.
func (x *xlsxReader) writeRow(st *sheetState, r int, hidden bool) error {
	defer st.reset()
	nonEmpty, hiddenCell := 0, false
	var formulas, uncalc, unresolved int
	for _, c := range st.used {
		if st.vals[c] != "" {
			nonEmpty++
			hiddenCell = hiddenCell || st.hiddenCol[c]
		}
		f := st.flags[c]
		if f&cellFormula != 0 {
			formulas++
		}
		if f&cellUncalc != 0 {
			uncalc++
		}
		if f&cellUnresolved != 0 {
			unresolved++
		}
	}
	if nonEmpty > 0 {
		switch {
		case st.rows >= x.lim.MaxRowsPerSheet:
			return x.stop(CutRows, st.no, r)
		case x.cells+nonEmpty > x.lim.MaxCells:
			return x.stop(CutCells, st.no, r)
		}
		// The line is the row number, a tab before each column up to the
		// last non-empty one, and the values.
		n := len(strconv.Itoa(r)) + st.maxCol + st.bytes
		if x.lines+n > xlsxLineBudget*x.lim.MaxTextBytes {
			return x.stop(CutTextBytes, st.no, r)
		}
		x.lines += n
		var line strings.Builder
		line.WriteString(strconv.Itoa(r))
		for c := 1; c <= st.maxCol; c++ {
			line.WriteByte('\t')
			line.WriteString(st.vals[c])
		}
		if !x.b.Lines(line.String()) || st.overflow {
			return x.stop(CutTextBytes, st.no, r)
		}
		x.cells += nonEmpty
		st.rows++
		x.facts.Rows++
		if hidden || hiddenCell || st.hidden {
			x.facts.HiddenContent = true
		}
	}
	x.facts.Formulas += formulas
	x.facts.Uncalculated += uncalc
	x.facts.CellsUnresolved += unresolved
	x.facts.ColumnsDropped += st.dropped
	return nil
}

// --- comments ----------------------------------------------------------------

// comments writes the legacy cell comments of a worksheet read, under a
// line naming the sheet.
func (x *xlsxReader) comments(sh *xlsxSheet) error {
	sr, err := x.pkg.readRels(sh.key)
	if err != nil {
		return err
	}
	key, ok := sr.first("comments")
	if !ok || !x.pkg.has(key) {
		return nil
	}
	s, done, err := x.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isS(root.Name) || root.Name.Local != "comments" {
		return nil
	}
	var authors []string
	started := false
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isS(se.Name) && se.Name.Local == "authors":
			err = eachChildErr(s, "author", func() error {
				t, _, err := s.text(maxLabelBytes)
				authors = append(authors, label(decodeX(t)))
				return err
			})
		case isS(se.Name) && se.Name.Local == "commentList":
			err = eachChildStart(s, "comment", func(c xmlStart) error {
				ref, _ := attr(c, "ref", "")
				author := ""
				if i, ok := atoiAttr(c, "authorId"); ok && i >= 0 && i < len(authors) {
					author = authors[i]
				}
				var text string
				if err := eachChildErr(s, "text", func() error {
					var b strings.Builder
					err := x.richText(s, &b)
					text = strings.TrimSpace(flatten(decodeX(b.String())))
					return err
				}); err != nil {
					return err
				}
				if x.facts.Comments >= x.lim.MaxNotes {
					return x.stop(CutNotes, 0, 0)
				}
				if !started {
					started = true
					if !x.b.Lines("--- comments of sheet " + strconv.Itoa(sh.no) + ": " + sh.name + " ---") {
						return x.stop(CutTextBytes, 0, 0)
					}
				}
				line := runePrefix(label(ref), 32)
				if author != "" {
					line += " " + author
				}
				if !x.b.Lines(line + ": " + text) {
					return x.stop(CutTextBytes, 0, 0)
				}
				x.facts.Comments++
				return nil
			})
		default:
			err = s.skip()
		}
		if err != nil {
			return err
		}
	}
}

// --- helpers -----------------------------------------------------------------

// xmlStart is an element just started.
type xmlStart = xml.StartElement

// eachChild calls fn with each child element named local (in the
// SpreadsheetML namespace) of the element being read, and reads every
// child to its end.
func eachChild(s *xmlScanner, local string, fn func(xmlStart)) error {
	return eachChildStart(s, local, func(c xmlStart) error {
		fn(c)
		return s.skip()
	})
}

// eachChildErr calls fn on each child element named local, which fn reads
// to its end; other children are skipped.
func eachChildErr(s *xmlScanner, local string, fn func() error) error {
	return eachChildStart(s, local, func(xmlStart) error { return fn() })
}

// eachChildStart calls fn with each child element named local, which fn
// reads to its end; other children are skipped.
func eachChildStart(s *xmlScanner, local string, fn func(xmlStart) error) error {
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if isS(se.Name) && se.Name.Local == local {
			err = fn(*se)
		} else {
			err = s.skip()
		}
		if err != nil {
			return err
		}
	}
}

// atoiAttr reads an unqualified integer attribute.
func atoiAttr(se xmlStart, local string) (int, bool) {
	v, ok := attr(se, local, "")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return n, err == nil
}

// parseIndex reads a 1-based row number. One too large to be a row is
// returned as maxSheetRow+1.
func parseIndex(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		if n <= maxSheetRow {
			n = n*10 + int(c-'0')
		}
	}
	if n == 0 {
		return 0, false
	}
	return min(n, maxSheetRow+1), true
}

// parseCellColumn reads the column of a cell reference ("B3" is 2). A
// column past XFD is returned as maxSheetCol+1; a reference that is not
// letters then digits is not one.
func parseCellColumn(ref string) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref); i++ {
		c := ref[i] | 0x20
		if c < 'a' || c > 'z' {
			break
		}
		if n <= maxSheetCol {
			n = n*26 + int(c-'a'+1)
		}
	}
	if i == 0 || i == len(ref) {
		return 0, false
	}
	if _, ok := parseIndex(ref[i:]); !ok {
		return 0, false
	}
	return min(n, maxSheetCol+1), true
}

// decodeX decodes the _xHHHH_ escapes of an ST_Xstring ("_x000D_" is a
// carriage return, "_x005F_" an underscore), surrogate pairs included; a
// lone surrogate becomes U+FFFD.
func decodeX(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, ok := xEscape(s[i:])
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		i += 7
		if r >= 0xD800 && r <= 0xDBFF {
			if lo, ok := xEscape(s[i:]); ok && lo >= 0xDC00 && lo <= 0xDFFF {
				r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				i += 7
			}
		}
		if r >= 0xD800 && r <= 0xDFFF {
			r = utf8.RuneError
		}
		b.WriteRune(r)
	}
	return b.String()
}

// xEscape reads one _xHHHH_ escape at the start of s.
func xEscape(s string) (rune, bool) {
	if len(s) < 7 || s[0] != '_' || s[1] != 'x' || s[6] != '_' {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[2:6]) {
		d, ok := unhex(c)
		if !ok {
			return 0, false
		}
		r = r<<4 | rune(d)
	}
	return r, true
}
