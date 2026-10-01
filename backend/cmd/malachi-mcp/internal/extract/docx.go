// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WordprocessingML (DOCX) to text, design §4. The body comes first, one
// line per paragraph in document order; then comments, footnotes,
// endnotes, and the distinct headers and footers. Tracked changes are
// shown as accepted: inserted text stays, deleted text is left out.
// Hidden text is included and flagged. Field codes and hyperlink targets
// never reach the text, only what the document shows.

// The section lines between the body and what follows it.
const (
	docxCommentsLine  = "--- comments ---"
	docxFootnotesLine = "--- footnotes ---"
	docxEndnotesLine  = "--- endnotes ---"
	docxHeadersLine   = "--- headers and footers ---"
)

// maxGridSpan caps how many grid columns one table cell may say it spans.
const maxGridSpan = 64

// maxListLevel is the deepest list level WordprocessingML has (0-based).
const maxListLevel = 8

// errFull ends a reader once the text is cut: nothing more is taken.
var errFull = errors.New("extract: text full")

// extractDOCX turns a DOCX document into text.
func extractDOCX(ctx context.Context, data []byte, lim Limits) (Result, error) {
	p, err := openPackage(ctx, data, lim)
	if err != nil {
		return Result{}, err
	}
	main, err := p.mainPart(DOCX)
	if err != nil {
		return Result{}, err
	}
	r := &docxReader{
		pkg:        p,
		lim:        lim,
		main:       main,
		b:          newTextBuilder(lim.MaxTextBytes),
		headerSeen: map[string]bool{},
	}
	if err := r.read(); err != nil && !errors.Is(err, errFull) {
		return Result{}, err
	}
	return r.result(), nil
}

// docxReader reads one document.
type docxReader struct {
	pkg   *opcPackage
	lim   Limits
	main  string
	rels  rels
	b     *textBuilder
	facts Facts

	numbering numbering

	comments, footnotes, endnotes noteSet
	notesSeen                     int  // notes of the three kinds seen so far
	notesCut                      bool // more than MaxNotes: the rest left out
	notesHeld                     int  // bytes of note text kept

	headerIDs  []string // relationship ids of headers and footers, in order of first use
	headerSeen map[string]bool

	// lost is set when text that a buffer had to drop would have been
	// written: the text is cut even where the builder could still take
	// it. White space a buffer drops, and text that is never written (a
	// deleted row, a cell continuing a vertical merge, a header that is a
	// copy of another), leave the text whole.
	lost bool
}

// read reads the whole document into r.b and r.facts.
func (r *docxReader) read() error {
	var err error
	if r.rels, err = r.pkg.readRels(r.main); err != nil {
		return err
	}
	if err := r.readNumbering(); err != nil {
		return err
	}
	for _, n := range []struct {
		rel, root, item string
		set             *noteSet
	}{
		{"comments", "comments", "comment", &r.comments},
		{"footnotes", "footnotes", "footnote", &r.footnotes},
		{"endnotes", "endnotes", "endnote", &r.endnotes},
	} {
		if err := r.readNotes(n.rel, n.root, n.item, n.set); err != nil {
			return err
		}
	}
	if err := r.body(); err != nil {
		return err
	}
	for _, n := range []struct {
		line  string
		set   *noteSet
		count *int
		label string
	}{
		{docxCommentsLine, &r.comments, &r.facts.Comments, "comment "},
		{docxFootnotesLine, &r.footnotes, &r.facts.Footnotes, ""},
		{docxEndnotesLine, &r.endnotes, &r.facts.Endnotes, ""},
	} {
		if !r.writeNotes(n.line, n.set, n.count, n.label) {
			return errFull
		}
	}
	if r.notesCut {
		return errFull
	}
	return r.headers()
}

// result is the text and the facts once reading has stopped.
func (r *docxReader) result() Result {
	f := r.facts
	switch {
	case r.b.Cut(), r.lost:
		f.Cut, f.CutAt = true, CutTextBytes
	case r.notesCut:
		f.Cut, f.CutAt = true, CutNotes
	}
	f.Comments = min(f.Comments, MaxFact)
	f.Footnotes = min(f.Footnotes, MaxFact)
	f.Endnotes = min(f.Endnotes, MaxFact)
	f.HeadersFooters = min(f.HeadersFooters, MaxFact)
	return Result{Text: r.b.String(), Facts: f}
}

// bufferLimit is the most one buffer holds: more than the text can ever
// take, so a buffer that had to drop text that is written means a cut
// text.
func (r *docxReader) bufferLimit() int { return r.lim.MaxTextBytes + utf8.UTFMax }

// body reads w:body of the main part.
func (r *docxReader) body() error {
	s, done, err := r.pkg.scan(r.main)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isW(root.Name) || root.Name.Local != "document" {
		return refuse(Damaged)
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if isW(se.Name) && se.Name.Local == "body" {
			return r.walk(s, r.story(), true, &r.facts.HiddenContent)
		}
		if err := s.skip(); err != nil {
			return err
		}
	}
}

// --- numbering ---------------------------------------------------------------

// numbering is what numbering.xml says about list levels: whether a
// paragraph's numbering shows a marker at all (numFmt "none" does not).
type numbering struct {
	nums      map[string]string                  // numId → abstractNumId
	abstracts map[string]*[maxListLevel + 1]int8 // abstractNumId → per level: 0 undefined, 1 marked, 2 none
}

// marked reports whether a paragraph with this numbering shows a list
// marker. Numbering id 0, or one that names nothing, shows none.
func (n numbering) marked(numID string, level int) bool {
	if numID == "" || numID == "0" {
		return false
	}
	abs, ok := n.nums[numID]
	if !ok {
		return false
	}
	levels := n.abstracts[abs]
	return levels != nil && levels[level] == 1
}

// readNumbering reads the numbering part, when the document has one.
func (r *docxReader) readNumbering() error {
	r.numbering = numbering{nums: map[string]string{}, abstracts: map[string]*[maxListLevel + 1]int8{}}
	key, ok := r.rels.first("numbering")
	if !ok || !r.pkg.has(key) {
		return nil
	}
	s, done, err := r.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isW(root.Name) || root.Name.Local != "numbering" {
		return nil
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		switch {
		case isW(se.Name) && se.Name.Local == "abstractNum":
			id, _ := wAttr(*se, "abstractNumId")
			levels := new([maxListLevel + 1]int8)
			if err := r.readAbstractNum(s, levels); err != nil {
				return err
			}
			if r.numbering.abstracts[id] == nil {
				r.numbering.abstracts[id] = levels
			}
		case isW(se.Name) && se.Name.Local == "num":
			id, _ := wAttr(*se, "numId")
			abs, err := readNumAbstract(s)
			if err != nil {
				return err
			}
			if _, dup := r.numbering.nums[id]; !dup {
				r.numbering.nums[id] = abs
			}
		default:
			if err := s.skip(); err != nil {
				return err
			}
		}
	}
}

// readAbstractNum reads the levels of a w:abstractNum.
func (r *docxReader) readAbstractNum(s *xmlScanner, levels *[maxListLevel + 1]int8) error {
	for {
		lvl, err := s.child()
		if err != nil || lvl == nil {
			return err
		}
		if !isW(lvl.Name) || lvl.Name.Local != "lvl" {
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		v, _ := wAttr(*lvl, "ilvl")
		level, lerr := strconv.Atoi(v)
		mark := int8(1)
		for {
			se, err := s.child()
			if err != nil {
				return err
			}
			if se == nil {
				break
			}
			if isW(se.Name) && se.Name.Local == "numFmt" {
				if f, _ := wAttr(*se, "val"); f == "none" {
					mark = 2
				}
			}
			if err := s.skip(); err != nil {
				return err
			}
		}
		if lerr == nil && level >= 0 && level <= maxListLevel && levels[level] == 0 {
			levels[level] = mark
		}
	}
}

// readNumAbstract reads the abstract numbering a w:num points to.
func readNumAbstract(s *xmlScanner) (string, error) {
	abs, found := "", false
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return abs, err
		}
		if isW(se.Name) && se.Name.Local == "abstractNumId" && !found {
			abs, found = wAttr(*se, "val")
		}
		if err := s.skip(); err != nil {
			return "", err
		}
	}
}

// --- comments, footnotes, endnotes -------------------------------------------

// noteSet is the comments, the footnotes or the endnotes of a document.
// They are numbered in the order of their part: the body shows the same
// number where it refers to one.
type noteSet struct {
	index map[string]int // note id → number
	count int            // notes seen
	notes []docxNote     // notes kept (at most MaxNotes of all kinds)
}

type docxNote struct {
	num    int
	author string
	lines  lineList
	hidden bool
}

// readNotes reads the notes of one kind, before the body, so that the
// body can show their numbers. Separator notes are not notes.
func (r *docxReader) readNotes(relType, rootName, itemName string, set *noteSet) error {
	set.index = map[string]int{}
	key, ok := r.rels.first(relType)
	if !ok || !r.pkg.has(key) {
		return nil
	}
	s, done, err := r.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isW(root.Name) || root.Name.Local != rootName {
		return nil
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		typ, _ := wAttr(*se, "type")
		if !isW(se.Name) || se.Name.Local != itemName || typ == "separator" || typ == "continuationSeparator" || typ == "continuationNotice" {
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		set.count++
		id, _ := wAttr(*se, "id")
		if _, dup := set.index[id]; !dup {
			set.index[id] = set.count
		}
		r.notesSeen++
		if r.notesSeen > r.lim.MaxNotes {
			r.notesCut = true
			if err := s.skip(); err != nil {
				return err
			}
			continue
		}
		author, _ := wAttr(*se, "author")
		n := docxNote{num: set.count, author: label(author)}
		n.lines = lineList{held: &r.notesHeld, limit: r.bufferLimit()}
		if err := r.walk(s, &n.lines, false, &n.hidden); err != nil {
			return err
		}
		set.notes = append(set.notes, n)
	}
}

// writeNotes writes one kind of notes after the body: its section line,
// then each note with its number (and author) in front of its first line.
// It reports false once the text is cut.
func (r *docxReader) writeNotes(section string, set *noteSet, count *int, kind string) bool {
	if len(set.notes) == 0 {
		return true
	}
	r.b.Blank()
	if !r.b.Lines(section) {
		return false
	}
	for _, n := range set.notes {
		r.lost = r.lost || n.lines.lost
		prefix := "[" + kind + strconv.Itoa(n.num) + "] "
		if n.author != "" {
			prefix += n.author + ": "
		}
		started := false
		for _, l := range n.lines.lines {
			switch {
			case l == "" && started:
				r.b.Blank()
				continue
			case l == "":
				continue
			case !started:
				// A note's first paragraph starts with its mark, then
				// usually a space or a tab: the number stands for both.
				l, started = prefix+strings.TrimLeft(l, " \t"), true
			}
			if !r.b.Lines(l) {
				return false
			}
		}
		if !started && !r.b.Lines(prefix) {
			return false
		}
		*count++
		r.facts.HiddenContent = r.facts.HiddenContent || n.hidden
	}
	return true
}

// --- headers and footers -----------------------------------------------------

// addHeader records a header or footer reference of the body.
func (r *docxReader) addHeader(id string) {
	if !r.headerSeen[id] {
		r.headerSeen[id] = true
		r.headerIDs = append(r.headerIDs, id)
	}
}

// headers writes each distinct header and footer once, in order of first
// use, after the notes; at most MaxHeadersFooters parts are read. Each
// part is read into a list of its own first: only a story that is neither
// blank nor a copy of one kept before counts against the budget the
// stories share.
func (r *docxReader) headers() error {
	type story struct {
		lines  lineList
		hidden bool
	}
	var (
		stories []story
		texts   = map[string]bool{}
		parts   = map[string]bool{}
		held    int // bytes of the stories kept
	)
	for _, id := range r.headerIDs {
		l, ok := r.rels.target(id)
		if !ok || (l.typ != "header" && l.typ != "footer") || !r.pkg.has(l.key) || parts[l.key] {
			continue
		}
		if len(parts) == r.lim.MaxHeadersFooters {
			break
		}
		parts[l.key] = true
		var own int
		read := lineList{held: &own, limit: r.bufferLimit()}
		var hidden bool
		if err := r.readHeader(l.key, &read, &hidden); err != nil {
			return err
		}
		read.lines = trimBlanks(read.lines)
		text := strings.Join(read.lines, "\n")
		if strings.TrimSpace(text) == "" || texts[text] {
			continue
		}
		texts[text] = true
		st := story{lines: lineList{held: &held, limit: r.bufferLimit()}, hidden: hidden}
		read.replay(&st.lines)
		st.lines.lines = trimBlanks(st.lines.lines)
		if strings.TrimSpace(strings.Join(st.lines.lines, "")) != "" {
			stories = append(stories, st)
		}
		if st.lines.lost {
			// The budget is spent: this story loses text, and any other
			// one would lose all of its own.
			r.lost = true
			break
		}
	}
	if len(stories) == 0 {
		return nil
	}
	r.b.Blank()
	if !r.b.Lines(docxHeadersLine) {
		return errFull
	}
	for i, st := range stories {
		if i > 0 {
			r.b.Blank()
		}
		if !st.lines.replay(r.story()) {
			return errFull
		}
		r.facts.HeadersFooters++
		r.facts.HiddenContent = r.facts.HiddenContent || st.hidden
	}
	return nil
}

// readHeader reads one header or footer part.
func (r *docxReader) readHeader(key string, sink lineSink, hidden *bool) error {
	s, done, err := r.pkg.scan(key)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if !isW(root.Name) || (root.Name.Local != "hdr" && root.Name.Local != "ftr") {
		return nil
	}
	return r.walk(s, sink, false, hidden)
}

// trimBlanks drops the blank lines at the start and the end of lines.
func trimBlanks(lines []string) []string {
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// --- where lines go ----------------------------------------------------------

// lineSink takes the lines of a story: the text itself, a list of lines
// kept for later, or a table cell.
type lineSink interface {
	line(s string) // one or more lines
	blank()        // a blank line
	lose()         // text that would have come here was dropped
	full() bool    // the text is cut: nothing more is taken
}

// storySink writes into the text.
type storySink struct {
	b    *textBuilder
	lost *bool
}

// story is a sink writing into the text of r.
func (r *docxReader) story() *storySink { return &storySink{b: r.b, lost: &r.lost} }

func (s *storySink) line(l string) { s.b.Lines(l) }
func (s *storySink) blank()        { s.b.Blank() }
func (s *storySink) lose()         { *s.lost = true }
func (s *storySink) full() bool    { return s.b.Cut() }

// lineList keeps lines for later ("" is a blank line), within a byte limit
// shared by the lists of one budget. A line is kept without the white
// space at its end, which the text would drop anyway, so that white space
// never spends the budget.
type lineList struct {
	lines []string
	held  *int
	limit int
	lost  bool // the list dropped text
}

func (l *lineList) line(s string) {
	if s == "" {
		l.blank()
		return
	}
	// Only the end of the last line goes: a line break stays, and so does
	// the white space before one, which a cell keeps. A last line of white
	// space only is kept as one space, so that it stays a line and not a
	// blank one (a note's first line takes its number, whatever it holds).
	if t := strings.TrimRightFunc(s, isLineSpace); len(t) < len(s) && (t == "" || isLineBreak(t[len(t)-1])) {
		s = t + " "
	} else {
		s = t
	}
	if *l.held+len(s) > l.limit {
		kept := runePrefix(s, l.limit-*l.held)
		l.lost = l.lost || hasText(s[len(kept):])
		if s = kept; s == "" {
			return
		}
	}
	*l.held += len(s)
	l.lines = append(l.lines, s)
}

func (l *lineList) blank() {
	if n := len(l.lines); n == 0 || l.lines[n-1] != "" {
		l.lines = append(l.lines, "")
	}
}

func (l *lineList) lose()      { l.lost = true }
func (l *lineList) full() bool { return false }

// replay passes the lines on, and that some were dropped; false once the
// sink is full.
func (l *lineList) replay(to lineSink) bool {
	if l.lost {
		to.lose()
	}
	for _, s := range l.lines {
		if s == "" {
			to.blank()
		} else {
			to.line(s)
		}
		if to.full() {
			return false
		}
	}
	return true
}

// cellBuf is the text of a table cell: its paragraphs joined with spaces
// on one line.
type cellBuf struct {
	b     strings.Builder
	limit int
	lost  bool // the cell dropped text
}

func (c *cellBuf) line(s string) {
	s = strings.TrimSpace(flatten(s))
	if s == "" {
		return
	}
	if c.b.Len() > 0 {
		s = " " + s
	}
	c.lost = appendText(&c.b, s, c.limit) || c.lost
}

func (c *cellBuf) blank()     {}
func (c *cellBuf) lose()      { c.lost = true }
func (c *cellBuf) full() bool { return false }

// appendText appends s to b up to limit bytes in all, as appendCapped
// does, and reports whether what was left out holds text: dropped white
// space would not show.
func appendText(b *strings.Builder, s string, limit int) bool {
	n := b.Len()
	if !appendCapped(b, s, limit) {
		return false
	}
	return hasText(s[b.Len()-n:])
}

// hasText reports whether s holds anything but white space.
func hasText(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsSpace(r) })
}

// isLineBreak reports a byte the text breaks a line at.
func isLineBreak(c byte) bool { return c == '\n' || c == '\r' }

// isLineSpace reports white space the text drops at the end of a line.
func isLineSpace(r rune) bool { return r != '\n' && r != '\r' && unicode.IsSpace(r) }

// --- the walker --------------------------------------------------------------

// docxFrame is what an open element is to the walker.
type docxFrame uint8

const (
	dfOther     docxFrame = iota // read through: its content counts as its parent's
	dfPara                       // w:p
	dfRun                        // w:r
	dfText                       // w:t, m:t
	dfTable                      // w:tbl
	dfRow                        // w:tr
	dfCell                       // w:tc
	dfAlt                        // mc:AlternateContent
	dfRunProps                   // w:rPr of a run
	dfMarkProps                  // w:rPr of a paragraph mark
	dfParaProps                  // w:pPr
	dfNumPr                      // w:numPr
	dfSectPr                     // w:sectPr
	dfCellProps                  // w:tcPr
	dfRowProps                   // w:trPr
)

type docxPara struct {
	text     strings.Builder
	lost     bool // text dropped from it
	numID    string
	level    int
	deferred lineList // paragraphs of text boxes anchored in it, shown after it
	held     int
}

type docxTable struct {
	flat bool     // nested deeper than MaxTableDepth: its text joins the enclosing cell
	out  lineSink // where its rows go
}

type docxRow struct {
	table    int // len(tables) when the row opened
	line     strings.Builder
	lost     bool // text dropped from its line or from a cell shown in it
	cells    int
	deleted  bool
	deferred lineList // rows of tables nested in its cells, shown after it
	held     int
}

type docxCell struct {
	buf   cellBuf
	span  int
	vcont bool // continues a vertical merge: shown empty
}

// docxWalker reads one story (the body, a note, a header) with explicit
// stacks; the depth of every stack is bounded by MaxXMLDepth.
type docxWalker struct {
	r      *docxReader
	s      *xmlScanner
	main   bool  // the body: its section properties name the headers and footers
	hidden *bool // set when hidden text is read

	frames []docxFrame
	alts   []bool // per open mc:AlternateContent: a branch was taken
	sinks  []lineSink
	paras  []*docxPara
	runs   []bool // per open run: its text is hidden
	tables []docxTable
	rows   []*docxRow
	cells  []*docxCell
	fields []bool // per open complex field: still in its instruction
	instr  int    // open fields in their instruction
}

// walk reads the content of the element just started into sink, up to
// that element's end. It returns errFull once the text is cut.
func (r *docxReader) walk(s *xmlScanner, sink lineSink, main bool, hidden *bool) error {
	w := &docxWalker{r: r, s: s, main: main, hidden: hidden, sinks: []lineSink{sink}}
	for {
		tok, err := s.next()
		if err != nil {
			return s.unexpectedEOF(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := w.start(t); err != nil {
				return err
			}
		case xml.EndElement:
			if len(w.frames) == 0 {
				return nil
			}
			w.end()
		case xml.CharData:
			if w.top() == dfText {
				w.addText(string(t))
			}
		}
		if sink.full() {
			return errFull
		}
	}
}

func (w *docxWalker) top() docxFrame {
	if len(w.frames) == 0 {
		return dfOther
	}
	return w.frames[len(w.frames)-1]
}

func (w *docxWalker) push(f docxFrame) { w.frames = append(w.frames, f) }

func (w *docxWalker) sink() lineSink { return w.sinks[len(w.sinks)-1] }

// start handles the start of an element: it either opens a frame, which
// end closes, or reads the element to its end at once.
func (w *docxWalker) start(se xml.StartElement) error {
	switch w.top() {
	case dfRunProps, dfMarkProps:
		return w.props(se)
	case dfParaProps:
		return w.paraProps(se)
	case dfNumPr:
		return w.numPr(se)
	case dfSectPr:
		return w.sectPr(se)
	case dfCellProps:
		return w.cellProps(se)
	case dfRowProps:
		return w.rowProps(se)
	case dfText:
		return w.s.skip()
	}
	switch {
	case isW(se.Name):
		return w.word(se)
	case isM(se.Name) && se.Name.Local == "t":
		w.push(dfText)
	case isMC(se.Name):
		return w.compat(se)
	default:
		w.push(dfOther)
	}
	return nil
}

// word handles a WordprocessingML element in the content of a story.
func (w *docxWalker) word(se xml.StartElement) error {
	top := w.top()
	switch se.Name.Local {
	case "p":
		p := &docxPara{}
		p.deferred = lineList{held: &p.held, limit: w.r.bufferLimit()}
		w.paras = append(w.paras, p)
		w.sinks = append(w.sinks, &p.deferred)
		w.push(dfPara)
	case "r":
		w.runs = append(w.runs, false)
		w.push(dfRun)
	case "t":
		if w.instr > 0 {
			return w.s.skip()
		}
		w.push(dfText)
	case "ins", "moveTo":
		w.r.facts.TrackedChanges = true
		w.push(dfOther)
	case "del", "moveFrom", "delText", "delInstrText":
		w.r.facts.TrackedChanges = true
		return w.s.skip()
	case "instrText", "fldData", "sdtPr", "sdtEndPr", "tblPr", "tblGrid", "tblPrEx",
		"rPrChange", "pPrChange", "sectPrChange", "tblPrChange", "tblGridChange",
		"trPrChange", "tcPrChange", "numberingChange", "customXmlPr", "smartTagPr", "subDoc":
		return w.s.skip()
	case "rPr":
		if top != dfRun {
			return w.s.skip()
		}
		w.push(dfRunProps)
	case "pPr":
		if top != dfPara {
			return w.s.skip()
		}
		w.push(dfParaProps)
	case "sectPr":
		w.push(dfSectPr)
	case "tbl":
		w.openTable()
	case "tr":
		w.openRow()
	case "tc":
		w.openCell()
	case "tcPr":
		if top != dfCell {
			return w.s.skip()
		}
		w.push(dfCellProps)
	case "trPr":
		if top != dfRow {
			return w.s.skip()
		}
		w.push(dfRowProps)
	case "tab", "ptab":
		w.add("\t")
		return w.s.skip()
	case "br", "cr":
		w.add("\n")
		return w.s.skip()
	case "noBreakHyphen":
		w.add("-")
		return w.s.skip()
	case "fldChar":
		w.fieldChar(se)
		return w.s.skip()
	case "footnoteReference":
		w.noteMark(se, &w.r.footnotes, "footnote")
		return w.s.skip()
	case "endnoteReference":
		w.noteMark(se, &w.r.endnotes, "endnote")
		return w.s.skip()
	case "commentReference":
		w.noteMark(se, &w.r.comments, "comment")
		return w.s.skip()
	default:
		// w:body content the walker does not know (w:hyperlink, w:sdt,
		// w:sdtContent, w:smartTag, w:customXml, w:fldSimple, w:drawing,
		// w:pict, w:txbxContent, …) is read through for its text.
		w.push(dfOther)
	}
	return nil
}

// compat handles Markup Compatibility: of mc:AlternateContent only the
// first branch is read (the first mc:Choice; mc:Fallback, which comes
// last, only when there was no choice), so that a text box and its
// fallback copy are not both read.
func (w *docxWalker) compat(se xml.StartElement) error {
	switch se.Name.Local {
	case "AlternateContent":
		w.alts = append(w.alts, false)
		w.push(dfAlt)
	case "Choice", "Fallback":
		if w.top() != dfAlt || w.alts[len(w.alts)-1] {
			return w.s.skip()
		}
		w.alts[len(w.alts)-1] = true
		w.push(dfOther)
	default:
		w.push(dfOther)
	}
	return nil
}

// end handles the end of the element of the top frame.
func (w *docxWalker) end() {
	f := w.frames[len(w.frames)-1]
	w.frames = w.frames[:len(w.frames)-1]
	switch f {
	case dfPara:
		w.closePara()
	case dfRun:
		w.runs = w.runs[:len(w.runs)-1]
	case dfTable:
		t := w.tables[len(w.tables)-1]
		w.tables = w.tables[:len(w.tables)-1]
		if !t.flat {
			t.out.blank()
		}
	case dfRow:
		w.closeRow()
	case dfCell:
		w.closeCell()
	case dfAlt:
		w.alts = w.alts[:len(w.alts)-1]
	}
}

// add adds text to the open paragraph, unless it is part of a field's
// instruction. Text outside any paragraph is not read.
func (w *docxWalker) add(s string) {
	if w.instr > 0 || len(w.paras) == 0 {
		return
	}
	p := w.paras[len(w.paras)-1]
	p.lost = appendText(&p.text, s, w.r.bufferLimit()) || p.lost
}

// addText adds the content of w:t or m:t. A line break inside it is a
// space, as Word shows it.
func (w *docxWalker) addText(s string) {
	if strings.ContainsAny(s, "\r\n") {
		s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	}
	if len(w.runs) > 0 && w.runs[len(w.runs)-1] && w.instr == 0 && strings.TrimSpace(s) != "" {
		*w.hidden = true
	}
	w.add(s)
}

// closePara writes a paragraph: its list marker, its text, then the
// paragraphs of text boxes anchored in it.
func (w *docxWalker) closePara() {
	p := w.paras[len(w.paras)-1]
	w.paras = w.paras[:len(w.paras)-1]
	w.sinks = w.sinks[:len(w.sinks)-1]
	line := p.text.String()
	if strings.TrimSpace(line) != "" && w.r.numbering.marked(p.numID, p.level) {
		line = strings.Repeat("  ", p.level) + "- " + line
	}
	out := w.sink()
	out.line(line)
	if p.lost {
		out.lose()
	}
	p.deferred.replay(out)
}

// fieldChar follows complex fields: from begin to separate is the field's
// instruction, left out; from separate to end its result, kept. Fields
// nest.
func (w *docxWalker) fieldChar(se xml.StartElement) {
	typ, _ := wAttr(se, "fldCharType")
	n := len(w.fields)
	switch typ {
	case "begin":
		w.fields = append(w.fields, true)
		w.instr++
	case "separate":
		if n > 0 && w.fields[n-1] {
			w.fields[n-1] = false
			w.instr--
		}
	case "end":
		if n > 0 {
			if w.fields[n-1] {
				w.instr--
			}
			w.fields = w.fields[:n-1]
		}
	}
}

// noteMark shows a reference to a note by its number, as "[footnote 2]".
func (w *docxWalker) noteMark(se xml.StartElement, set *noteSet, kind string) {
	id, _ := wAttr(se, "id")
	if n, ok := set.index[id]; ok {
		w.add("[" + kind + " " + strconv.Itoa(n) + "]")
	}
}

// props handles a child of w:rPr: the hidden-text properties of a run,
// and tracked changes of a paragraph mark.
func (w *docxWalker) props(se xml.StartElement) error {
	if isW(se.Name) {
		switch se.Name.Local {
		case "vanish", "specVanish", "webHidden":
			v, ok := wAttr(se, "val")
			if w.top() == dfRunProps && xmlBool(v, ok, true) {
				w.runs[len(w.runs)-1] = true
			}
		case "ins", "del", "moveFrom", "moveTo":
			w.r.facts.TrackedChanges = true
		}
	}
	return w.s.skip()
}

// paraProps handles a child of w:pPr.
func (w *docxWalker) paraProps(se xml.StartElement) error {
	if isW(se.Name) {
		switch se.Name.Local {
		case "numPr":
			w.push(dfNumPr)
			return nil
		case "sectPr":
			w.push(dfSectPr)
			return nil
		case "rPr":
			w.push(dfMarkProps)
			return nil
		}
	}
	return w.s.skip()
}

// numPr handles a child of w:numPr: the paragraph's list and level.
func (w *docxWalker) numPr(se xml.StartElement) error {
	if isW(se.Name) && len(w.paras) > 0 {
		p := w.paras[len(w.paras)-1]
		v, _ := wAttr(se, "val")
		switch se.Name.Local {
		case "ilvl":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= maxListLevel {
				p.level = n
			}
		case "numId":
			p.numID = v
		}
	}
	return w.s.skip()
}

// sectPr handles a child of w:sectPr: the body's references to headers and
// footers.
func (w *docxWalker) sectPr(se xml.StartElement) error {
	if w.main && isW(se.Name) && (se.Name.Local == "headerReference" || se.Name.Local == "footerReference") {
		if id, ok := rAttr(se, "id"); ok {
			w.r.addHeader(id)
		}
	}
	return w.s.skip()
}

// cellProps handles a child of w:tcPr: horizontal and vertical merges.
func (w *docxWalker) cellProps(se xml.StartElement) error {
	if isW(se.Name) && len(w.cells) > 0 {
		c := w.cells[len(w.cells)-1]
		v, ok := wAttr(se, "val")
		switch se.Name.Local {
		case "gridSpan":
			if n, err := strconv.Atoi(v); err == nil {
				c.span = min(max(n, 1), maxGridSpan)
			}
		case "vMerge":
			c.vcont = !ok || v == "continue"
		case "cellIns", "cellDel":
			w.r.facts.TrackedChanges = true
		}
	}
	return w.s.skip()
}

// rowProps handles a child of w:trPr: a row inserted or deleted as a
// tracked change (a deleted row is left out).
func (w *docxWalker) rowProps(se xml.StartElement) error {
	if isW(se.Name) && len(w.rows) > 0 {
		switch se.Name.Local {
		case "del":
			w.rows[len(w.rows)-1].deleted = true
			w.r.facts.TrackedChanges = true
		case "ins":
			w.r.facts.TrackedChanges = true
		}
	}
	return w.s.skip()
}

// openTable starts a table: a blank line, then a line per row. A table
// nested in a cell has its rows shown after the row of that cell; one
// nested deeper than MaxTableDepth is flattened into the enclosing cell.
func (w *docxWalker) openTable() {
	w.push(dfTable)
	if len(w.tables)+1 > w.r.lim.MaxTableDepth {
		w.tables = append(w.tables, docxTable{flat: true})
		return
	}
	out := w.sink()
	if n := len(w.cells); n > 0 && out == lineSink(&w.cells[n-1].buf) {
		out = &w.rows[len(w.rows)-1].deferred
	}
	out.blank()
	w.tables = append(w.tables, docxTable{out: out})
}

// openRow starts a row of the innermost table, unless it is flattened.
func (w *docxWalker) openRow() {
	if n := len(w.tables); n == 0 || w.tables[n-1].flat {
		w.push(dfOther)
		return
	}
	row := &docxRow{table: len(w.tables)}
	row.deferred = lineList{held: &row.held, limit: w.r.bufferLimit()}
	w.rows = append(w.rows, row)
	w.push(dfRow)
}

// openCell starts a cell of the innermost row, unless the table is
// flattened or the cell is in no row.
func (w *docxWalker) openCell() {
	n := len(w.rows)
	if n == 0 || w.rows[n-1].table != len(w.tables) || w.tables[len(w.tables)-1].flat {
		w.push(dfOther)
		return
	}
	c := &docxCell{span: 1, buf: cellBuf{limit: w.r.bufferLimit()}}
	w.cells = append(w.cells, c)
	w.sinks = append(w.sinks, &c.buf)
	w.push(dfCell)
}

// closeCell adds a cell to its row: its text, then an empty cell for each
// further grid column it spans. A cell continuing a vertical merge shows
// none, so text it dropped is not lost.
func (w *docxWalker) closeCell() {
	c := w.cells[len(w.cells)-1]
	w.cells = w.cells[:len(w.cells)-1]
	w.sinks = w.sinks[:len(w.sinks)-1]
	row := w.rows[len(w.rows)-1]
	text := c.buf.b.String()
	if c.vcont {
		text = ""
	} else {
		row.lost = row.lost || c.buf.lost
	}
	for i := range c.span {
		s := ""
		if row.cells > 0 {
			s = "\t"
		}
		if i == 0 {
			s += text
		}
		row.lost = appendText(&row.line, s, w.r.bufferLimit()) || row.lost
		row.cells++
	}
}

// closeRow writes a row: its cells tab-separated on one line, then the
// rows of tables nested in it. A deleted row is not written, so text it
// dropped is not lost.
func (w *docxWalker) closeRow() {
	row := w.rows[len(w.rows)-1]
	w.rows = w.rows[:len(w.rows)-1]
	if row.deleted {
		return
	}
	out := w.tables[row.table-1].out
	out.line(row.line.String())
	if row.lost {
		out.lose()
	}
	row.deferred.replay(out)
}
