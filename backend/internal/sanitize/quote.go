// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Trimming the quoted history (Input.TrimQuoted, message.body trimQuoted):
// a reply shows what was written, and the history its client quoted below
// it waits behind a button that asks for the whole body again. The view
// runs without JavaScript, so the cut is made here, on the tree the parser
// built from the raw HTML, where the attributes that mark a quote (a
// class, an id, type="cite") are still there. The cut only removes nodes
// from that tree before the walk, so everything that remains goes through
// the walk as before: trimming can make the output smaller, never less
// sanitised.
//
// The detection is conservative: when in doubt, the whole body is shown.
// A quote is recognised by the first reliable marker, in document order,
// that qualifies:
//   - a nested quote, the quote being the marker's subtree: Gmail's
//     class="gmail_quote" (or gmail_quote_container), Apple Mail's and
//     Thunderbird's <blockquote type="cite">, and any <blockquote> right
//     after an attribution line ("On … wrote:", "Dne … napsal(a):", …).
//     It is cut only when nothing visible follows it at any level and,
//     for Gmail's <div>, nothing visible follows a <blockquote> inside it
//     outside one, so a reply written between or below quoted passages
//     stays whole;
//   - the start of a history, everything after the marker being the
//     quoted message: Outlook on the web's #divRplyFwdMsg, #appendonsend
//     and #mail-editor-reference-message-container, Outlook's separator
//     (an <hr>, or a <div> with a top border) followed by a header block
//     whose bold labels say From, then Sent or Date, then To or Subject in
//     one of the languages of quoteLabels, and a "-----Original
//     Message-----" line or one of its translations.
//
// The marker goes together with what dangles before it: empty elements,
// line breaks, a separator, and for a nested quote one attribution line
// (Gmail's gmail_attr, Thunderbird's moz-cite-prefix, or text that reads
// as one). Nothing is cut when the part that would go shows nothing, or
// when nothing visible would be left above it: a forward that is only the
// forwarded message stays whole. sanitize checks the second rule again on
// the output, where hidden or dropped content no longer counts.

// Bounds of the search, so that a hostile tree cannot make it expensive.
// Running out of budget gives up on trimming, never on the body.
const (
	quoteMaxMarks     = 64        // markers considered, in document order
	quoteBudget       = 2_000_000 // node visits for one body, all checks together
	quoteMaxClimb     = 64        // steps from a marker back to the start of the cut
	quoteHeaderLines  = 12        // lines read for an Outlook header block
	quoteHeaderBytes  = 4096      // bytes read for an Outlook header block
	quoteLineDepth    = 64        // nesting the header reader follows
	attributionMaxLen = 400       // bytes of an attribution line
	quoteLabelMaxLen  = 40        // bytes of a header label
)

// quoteKind says how much a marker covers.
type quoteKind int

const (
	// quoteNested: the quote is the marker's subtree; trimmed only when
	// nothing visible follows it.
	quoteNested quoteKind = iota
	// quoteHistory: the marker opens the quoted message, which runs to the
	// end of the body.
	quoteHistory
)

type quoteMark struct {
	n    *html.Node
	kind quoteKind
}

// quoteScan carries the budget of one trimming attempt.
type quoteScan struct {
	body   *html.Node
	visits int
}

func (s *quoteScan) spend() bool {
	s.visits++
	return s.visits <= quoteBudget
}

// trimQuoted removes the quoted history from the parsed body and reports
// whether it did. body is the parser's <body> element; nil trims nothing.
func trimQuoted(body *html.Node) bool {
	if body == nil {
		return false
	}
	s := &quoteScan{body: body}
	for _, m := range s.marks() {
		if m.kind == quoteNested {
			after, ok := s.visible(nextNode(m.n, body, false), body, nil)
			if !ok {
				return false
			}
			if after {
				continue // a reply below the quoted passages
			}
			if mixed, ok := s.interleaved(m.n); !ok || mixed {
				if !ok {
					return false
				}
				continue // a reply between the quoted passages, inside Gmail's quote
			}
		}
		start, ok := s.start(m)
		if !ok {
			return false
		}
		cut, ok := s.visible(start, body, nil)
		if !ok {
			return false
		}
		if !cut {
			continue // the marker holds nothing to hide, e.g. an empty #appendonsend
		}
		// The first marker that would hide something decides: with nothing
		// above it, the message is a bare forward or quote and stays whole,
		// rather than losing the outer layer to a quote further in.
		above, ok := s.visible(body.FirstChild, body, start)
		if !ok || !above {
			return false
		}
		cutFrom(start, body)
		return true
	}
	return false
}

// interleaved reports whether a quote marker that is not a <blockquote>
// itself (Gmail's <div class="gmail_quote">) holds a reply written between
// its quoted passages: something visible after a <blockquote> in it that is
// not inside one. ok = false when the budget ran out.
func (s *quoteScan) interleaved(n *html.Node) (mixed, ok bool) {
	if n.Data == "blockquote" {
		return false, true
	}
	quoted := false
	for c := n.FirstChild; c != nil; {
		if !s.spend() {
			return false, false
		}
		descend := true
		switch c.Type {
		case html.TextNode:
			if quoted && !blankText(c.Data) {
				return true, true
			}
		case html.ElementNode:
			switch {
			case quoteOpaque(c):
				descend = false
			case c.Data == "blockquote":
				quoted, descend = true, false
			case c.Data == "img" && quoted:
				return true, true
			}
		}
		c = nextNode(c, n, descend)
	}
	return false, true
}

// marks lists the quote markers of the body in document order.
func (s *quoteScan) marks() []quoteMark {
	var out []quoteMark
	for n := s.body.FirstChild; n != nil && len(out) < quoteMaxMarks; {
		if !s.spend() {
			return nil
		}
		descend := true
		switch n.Type {
		case html.ElementNode:
			if quoteOpaque(n) {
				descend = false
			} else if kind, ok := s.markOf(n); ok {
				out = append(out, quoteMark{n: n, kind: kind})
			}
		case html.TextNode:
			if separatorText(n.Data) {
				out = append(out, quoteMark{n: n, kind: quoteHistory})
			}
		}
		n = nextNode(n, s.body, descend)
	}
	return out
}

// markOf says whether element n marks a quote, and of which kind.
func (s *quoteScan) markOf(n *html.Node) (quoteKind, bool) {
	switch {
	case hasClass(n, "gmail_quote", "gmail_quote_container"):
		return quoteNested, true
	case n.Data == "blockquote" && strings.EqualFold(strings.TrimSpace(attrValue(n, "type")), "cite"):
		return quoteNested, true
	case hasID(n, "divRplyFwdMsg", "appendonsend", "mail-editor-reference-message-container"):
		return quoteHistory, true
	case n.Data == "hr" && outlookHeader(s.lines(nextNode(n, s.body, false), s.body)):
		return quoteHistory, true
	case n.Data == "div" && hasTopBorder(n) && outlookHeader(s.lines(n, n)):
		return quoteHistory, true
	case n.Data == "blockquote":
		if p := s.prevSignificantSkippingEmpty(n); p != nil && s.attributionEndingAt(p) != nil {
			return quoteNested, true
		}
	}
	return 0, false
}

// start is the first node the cut removes for marker m: the marker, or
// what dangles before it (empty elements, line breaks, separators, and for
// a nested quote one attribution line). It climbs out of an element the
// cut would leave empty. ok = false when the budget ran out.
func (s *quoteScan) start(m quoteMark) (*html.Node, bool) {
	start := m.n
	attributed := m.kind != quoteNested // only a nested quote has one
	for range quoteMaxClimb {
		p := prevSignificant(start)
		if p == nil {
			if start.Parent == nil || start.Parent == s.body {
				break
			}
			start = start.Parent
			continue
		}
		shows, ok := s.visible(p, p, nil)
		if !ok {
			return nil, false
		}
		if !shows {
			start = p
			continue
		}
		if !attributed {
			if a := s.attributionEndingAt(p); a != nil {
				start, attributed = a, true
				continue
			}
		}
		break
	}
	return start, true
}

// attributionEndingAt returns the first node of an attribution line that
// ends with p (the last node with content before a quote), or nil: an
// element marked as one by its client, an element whose text reads as one,
// or a run of inline nodes after a line break that together read as one.
func (s *quoteScan) attributionEndingAt(p *html.Node) *html.Node {
	if p.Type == html.ElementNode && !inlineElem[p.Data] {
		if quoteOpaque(p) || p.Data == "img" || s.holdsImage(p) {
			return nil
		}
		text, ok := s.shortText(p, 2*attributionMaxLen)
		if !ok {
			return nil
		}
		if hasClass(p, "gmail_attr", "moz-cite-prefix") || attributionText(text) {
			return p
		}
		return nil
	}
	// An inline run: text and inline elements back to a line break, a
	// block or the start of the parent.
	first := p
	var parts []string
	size := 0
	for n := p; n != nil; n = n.PrevSibling {
		if n.Type == html.CommentNode {
			continue
		}
		if n.Type == html.ElementNode && (!inlineElem[n.Data] || n.Data == "br" || n.Data == "img" || quoteOpaque(n)) {
			break
		}
		text, ok := s.shortText(n, attributionMaxLen)
		if !ok {
			return nil
		}
		if size += len(text); size > attributionMaxLen {
			return nil
		}
		parts = append(parts, text)
		first = n
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
	}
	if attributionText(b.String()) {
		return first
	}
	return nil
}

// shortText is the text of n (a text node or an element's subtree) when it
// has at most max bytes; ok = false when it is longer or the budget ran out.
func (s *quoteScan) shortText(n *html.Node, max int) (string, bool) {
	var b strings.Builder
	for c := n; c != nil; {
		if !s.spend() {
			return "", false
		}
		descend := true
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
			if b.Len() > max {
				return "", false
			}
		case html.ElementNode:
			if quoteOpaque(c) {
				descend = false
			} else if c.Data == "br" {
				b.WriteByte(' ')
			}
		}
		c = nextNode(c, n, descend)
	}
	return b.String(), true
}

// holdsImage reports whether n's subtree has an <img>.
func (s *quoteScan) holdsImage(n *html.Node) bool {
	for c := n; c != nil; {
		if !s.spend() {
			return true // in doubt, it is not an attribution line
		}
		descend := true
		if c.Type == html.ElementNode {
			if quoteOpaque(c) {
				descend = false
			} else if c.Data == "img" {
				return true
			}
		}
		c = nextNode(c, n, descend)
	}
	return false
}

// prevSignificantSkippingEmpty is the nearest previous sibling of n that
// shows something, skipping whitespace, comments and empty elements such
// as <br>; nil when there is none or the budget ran out.
func (s *quoteScan) prevSignificantSkippingEmpty(n *html.Node) *html.Node {
	for p := prevSignificant(n); p != nil; p = prevSignificant(p) {
		shows, ok := s.visible(p, p, nil)
		if !ok {
			return nil
		}
		if shows {
			return p
		}
	}
	return nil
}

// visible reports whether the nodes from first on, in document order
// within root and before stop, show anything: text other than whitespace
// or an image. Content of elements the walk drops does not count. ok =
// false when the budget ran out.
func (s *quoteScan) visible(first, root, stop *html.Node) (shows, ok bool) {
	for n := first; n != nil && n != stop; {
		if !s.spend() {
			return false, false
		}
		descend := true
		switch n.Type {
		case html.TextNode:
			if !blankText(n.Data) {
				return true, true
			}
		case html.ElementNode:
			if quoteOpaque(n) {
				descend = false
			} else if n.Data == "img" {
				return true, true
			}
		}
		n = nextNode(n, root, descend)
	}
	return false, true
}

// shows reports whether a sanitised tree shows anything (visible, with a
// budget of its own; a tree too large to check counts as showing).
func shows(root *html.Node) bool {
	s := &quoteScan{body: root}
	v, ok := s.visible(root.FirstChild, root, nil)
	return v || !ok
}

// quoteLine is one line of text as a header reader sees it, and whether
// its first character is bold.
type quoteLine struct {
	text string
	bold bool
}

// lines reads the first lines of text from first on, in document order
// within root: <br> and block elements break lines, whitespace collapses.
func (s *quoteScan) lines(first, root *html.Node) []quoteLine {
	r := &lineReader{s: s}
	for n := first; n != nil && !r.done; {
		r.node(n, false, 0)
		n = nextNode(n, root, false)
	}
	r.flush()
	return r.lines
}

type lineReader struct {
	s     *quoteScan
	lines []quoteLine
	cur   strings.Builder
	bold  bool // the first character of cur is bold
	space bool // a collapsed space is pending
	bytes int
	done  bool
}

func (r *lineReader) node(n *html.Node, bold bool, depth int) {
	if r.done {
		return
	}
	if !r.s.spend() || depth > quoteLineDepth {
		r.done = true
		return
	}
	switch n.Type {
	case html.TextNode:
		r.text(n.Data, bold)
		return
	case html.ElementNode:
	default:
		return
	}
	if quoteOpaque(n) {
		return
	}
	if n.Data == "br" {
		r.flush()
		return
	}
	block := !inlineElem[n.Data]
	if block {
		r.flush()
	}
	bold = bold || boldElem(n)
	for c := n.FirstChild; c != nil && !r.done; c = c.NextSibling {
		r.node(c, bold, depth+1)
	}
	if block {
		r.flush()
	}
}

func (r *lineReader) text(t string, bold bool) {
	for _, c := range t {
		if blankRune(c) {
			r.space = r.cur.Len() > 0
			continue
		}
		if r.cur.Len() == 0 {
			r.bold = bold
		} else if r.space {
			r.cur.WriteByte(' ')
		}
		r.space = false
		r.cur.WriteRune(c)
		if r.bytes++; r.bytes > quoteHeaderBytes {
			r.done = true
			return
		}
	}
}

func (r *lineReader) flush() {
	if r.cur.Len() > 0 {
		r.lines = append(r.lines, quoteLine{text: r.cur.String(), bold: r.bold})
		if len(r.lines) >= quoteHeaderLines {
			r.done = true
		}
	}
	r.cur.Reset()
	r.space = false
}

// boldElem reports whether n makes its text bold.
func boldElem(n *html.Node) bool {
	switch n.Data {
	case "b", "strong":
		return true
	}
	style := strings.ToLower(attrValue(n, "style"))
	i := strings.Index(style, "font-weight")
	if i < 0 {
		return false
	}
	v := strings.TrimLeft(style[i+len("font-weight"):], " \t:")
	for _, w := range []string{"bold", "bolder", "600", "700", "800", "900"} {
		if strings.HasPrefix(v, w) {
			return true
		}
	}
	return false
}

// hasTopBorder reports whether n's inline style draws a top border, as the
// <div> around Outlook's header block does ("border:none;border-top:solid
// #E1E1E1 1.0pt").
func hasTopBorder(n *html.Node) bool {
	style := strings.ToLower(attrValue(n, "style"))
	i := strings.Index(style, "border-top")
	if i < 0 {
		return false
	}
	v := strings.TrimLeft(style[i+len("border-top"):], " \t:")
	return v != "" && !strings.HasPrefix(v, "none") && !strings.HasPrefix(v, "0")
}

// Header label categories of an Outlook header block.
type labelKind int

const (
	labelOther labelKind = iota + 1 // Cc and the like: allowed, not counted
	labelFrom
	labelSent
	labelTo
	labelSubject
)

// quoteLabels are the labels of Outlook's header block in English, Czech,
// Slovak, German, French, Spanish, Italian, Dutch, Polish, Portuguese,
// the Scandinavian languages, Finnish, Hungarian and Russian, lower case,
// without the colon.
var quoteLabels = map[string]labelKind{
	"from": labelFrom, "od": labelFrom, "von": labelFrom, "de": labelFrom,
	"da": labelFrom, "van": labelFrom, "från": labelFrom, "fra": labelFrom,
	"lähettäjä": labelFrom, "feladó": labelFrom, "от": labelFrom, "от кого": labelFrom,

	"sent": labelSent, "date": labelSent, "odesláno": labelSent, "datum": labelSent,
	"odoslané": labelSent, "gesendet": labelSent, "envoyé": labelSent,
	"envoyé le": labelSent, "enviado": labelSent, "enviado el": labelSent,
	"fecha": labelSent, "inviato": labelSent, "data": labelSent,
	"verzonden": labelSent, "wysłano": labelSent, "wysłane": labelSent,
	"skickat": labelSent, "sendt": labelSent, "lähetetty": labelSent,
	"elküldve": labelSent, "küldve": labelSent, "отправлено": labelSent, "дата": labelSent,

	"to": labelTo, "komu": labelTo, "an": labelTo, "à": labelTo, "a": labelTo,
	"para": labelTo, "aan": labelTo, "do": labelTo, "till": labelTo, "til": labelTo,
	"vastaanottaja": labelTo, "címzett": labelTo, "кому": labelTo,

	"subject": labelSubject, "předmět": labelSubject, "predmet": labelSubject,
	"betreff": labelSubject, "objet": labelSubject, "asunto": labelSubject,
	"oggetto": labelSubject, "onderwerp": labelSubject, "temat": labelSubject,
	"assunto": labelSubject, "ämne": labelSubject, "emne": labelSubject,
	"aihe": labelSubject, "tárgy": labelSubject, "тема": labelSubject,

	"cc": labelOther, "bcc": labelOther, "kopie": labelOther, "kópia": labelOther,
	"copie": labelOther, "kopia": labelOther, "copia": labelOther, "cópia": labelOther,
	"kopio": labelOther, "másolatot kap": labelOther, "копия": labelOther,
	"skrytá kopie": labelOther, "importance": labelOther, "důležitost": labelOther,
	"wichtigkeit": labelOther, "priorität": labelOther,
}

// outlookHeader reports whether lines open with an Outlook header block:
// consecutive "Label: value" lines, the first a bold From, which name at
// least From, Sent or Date, and To or Subject.
func outlookHeader(lines []quoteLine) bool {
	if len(lines) == 0 || !lines[0].bold {
		return false
	}
	return headerLabels(func(i int) (string, bool) {
		if i >= len(lines) {
			return "", false
		}
		return lines[i].text, true
	})
}

// headerLabels reads "Label: value" lines from line(0) on until one is not
// a known label, and reports whether the first is From and the block names
// Sent or Date and To or Subject.
func headerLabels(line func(int) (string, bool)) bool {
	seen := make(map[labelKind]bool)
	for i := 0; ; i++ {
		text, ok := line(i)
		if !ok {
			break
		}
		kind := labelOf(text)
		if kind == 0 || i == 0 && kind != labelFrom {
			break
		}
		seen[kind] = true
	}
	return seen[labelFrom] && seen[labelSent] && (seen[labelTo] || seen[labelSubject])
}

// labelOf is the category of the label a "Label: value" line opens with,
// 0 when it has none or an unknown one.
func labelOf(line string) labelKind {
	i := strings.IndexByte(line, ':')
	if i <= 0 || i > quoteLabelMaxLen {
		return 0
	}
	label := strings.ToLower(strings.TrimFunc(line[:i], blankRune))
	return quoteLabels[label]
}

// wroteWords end an attribution line ("On … Jan <jan@example.org> wrote:")
// in the languages of quoteLabels.
var wroteWords = []string{
	"wrote", "napsal", "napísal", "schrieb", "a écrit", "escribió",
	"ha scritto", "scrisse", "schreef", "napisał", "skrev", "kirjoitti",
	"escreveu", "írta", "написал", "yazdı",
}

// attributionText reports whether s reads as an attribution line: short,
// ending with a colon, naming the writing.
func attributionText(s string) bool {
	s = strings.TrimFunc(s, blankRune)
	if s == "" || len(s) > attributionMaxLen || !strings.HasSuffix(s, ":") {
		return false
	}
	lower := strings.ToLower(s)
	for _, w := range wroteWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// separatorPhrases are what "-----Original Message-----" and the forward
// separators say between their dashes, lower case.
var separatorPhrases = map[string]bool{
	"original message": true, "původní zpráva": true, "pôvodná správa": true,
	"ursprüngliche nachricht": true, "message d'origine": true,
	"mensaje original": true, "messaggio originale": true,
	"oorspronkelijk bericht": true, "wiadomość oryginalna": true,
	"oryginalna wiadomość": true, "mensagem original": true,
	"ursprungligt meddelande": true, "oprindelig meddelelse": true,
	"opprinnelig melding": true, "alkuperäinen viesti": true,
	"eredeti üzenet": true, "исходное сообщение": true,
	"forwarded message": true, "přeposlaná zpráva": true,
	"preposlaná správa": true, "weitergeleitete nachricht": true,
	"message transféré": true, "mensaje reenviado": true,
	"messaggio inoltrato": true, "doorgestuurd bericht": true,
	"wiadomość przekazana": true, "mensagem encaminhada": true,
}

// rightQuote is the typographic apostrophe some clients write in
// "Message d’origine".
const rightQuote rune = 0x2019

// separatorLine reports whether line is a "-----Original Message-----"
// separator: dashes, a phrase of separatorPhrases, dashes.
func separatorLine(line string) bool {
	line = strings.TrimFunc(line, blankRune)
	if len(line) > 80 {
		return false
	}
	lead := len(line) - len(strings.TrimLeft(line, "-"))
	trail := len(line) - len(strings.TrimRight(line, "-"))
	if lead < 2 || trail < 2 || lead+trail >= len(line) {
		return false
	}
	phrase := strings.TrimFunc(line[lead:len(line)-trail], blankRune)
	phrase = strings.ReplaceAll(strings.ToLower(phrase), string(rightQuote), "'")
	return separatorPhrases[phrase]
}

// separatorText reports whether a text node opens with a separator line
// (its first line that is not blank).
func separatorText(t string) bool {
	for len(t) > 0 {
		line, rest, _ := strings.Cut(t, "\n")
		if !blankText(line) {
			return separatorLine(line)
		}
		t = rest
	}
	return false
}

// quoteOpaque reports whether the search skips element n with everything
// in it: what the walk drops (it never shows) and foreign content.
func quoteOpaque(n *html.Node) bool {
	if n.Namespace != "" || n.Data == "style" {
		return true
	}
	_, dropped := droppedElems[n.Data]
	return dropped
}

// inlineElem are the elements that do not break a line of text.
var inlineElem = map[string]bool{
	"a": true, "abbr": true, "acronym": true, "b": true, "bdi": true, "bdo": true,
	"big": true, "cite": true, "code": true, "del": true, "dfn": true, "em": true,
	"font": true, "i": true, "ins": true, "kbd": true, "mark": true, "q": true,
	"s": true, "samp": true, "small": true, "span": true, "strike": true,
	"strong": true, "sub": true, "sup": true, "tt": true, "u": true, "var": true,
	"wbr": true, "br": true, "img": true, "o:p": true,
}

// hasClass reports whether n's class attribute holds one of names (case
// does not matter: the detection reads mail, not CSS).
func hasClass(n *html.Node, names ...string) bool {
	for _, c := range strings.Fields(attrValue(n, "class")) {
		for _, name := range names {
			if strings.EqualFold(c, name) {
				return true
			}
		}
	}
	return false
}

// hasID reports whether n's id is one of ids, case aside.
func hasID(n *html.Node, ids ...string) bool {
	id := strings.TrimSpace(attrValue(n, "id"))
	for _, want := range ids {
		if strings.EqualFold(id, want) {
			return true
		}
	}
	return false
}

// nextNode is the node after n in document order within root, entering
// n's children only when descend; nil past the end of root.
func nextNode(n, root *html.Node, descend bool) *html.Node {
	if n == nil {
		return nil
	}
	if descend && n.FirstChild != nil {
		return n.FirstChild
	}
	for n != nil && n != root {
		if n.NextSibling != nil {
			return n.NextSibling
		}
		n = n.Parent
	}
	return nil
}

// prevSignificant is n's nearest previous sibling that is not a comment or
// whitespace-only text.
func prevSignificant(n *html.Node) *html.Node {
	for p := n.PrevSibling; p != nil; p = p.PrevSibling {
		switch {
		case p.Type == html.CommentNode:
		case p.Type == html.TextNode && blankText(p.Data):
		default:
			return p
		}
	}
	return nil
}

// cutFrom removes start and everything after it in document order within
// body: start's following siblings, and those of each of its ancestors.
func cutFrom(start, body *html.Node) {
	parent := start.Parent
	for n := start; n != nil; {
		next := n.NextSibling
		parent.RemoveChild(n)
		n = next
	}
	for a := parent; a != nil && a != body && a.Parent != nil; a = a.Parent {
		for n := a.NextSibling; n != nil; {
			next := n.NextSibling
			a.Parent.RemoveChild(n)
			n = next
		}
	}
}

// Characters that show nothing although unicode.IsSpace does not say so.
const (
	zeroWidthSpace  rune = 0x200B
	zeroWidthNonJ   rune = 0x200C
	zeroWidthJoiner rune = 0x200D
	wordJoiner      rune = 0x2060
	byteOrderMark   rune = 0xFEFF
	softHyphen      rune = 0x00AD
)

// blankRune reports whether r shows nothing on its own.
func blankRune(r rune) bool {
	switch r {
	case zeroWidthSpace, zeroWidthNonJ, zeroWidthJoiner, wordJoiner, byteOrderMark, softHyphen:
		return true
	}
	return unicode.IsSpace(r)
}

// blankText reports whether s shows nothing.
func blankText(s string) bool {
	for _, r := range s {
		if !blankRune(r) {
			return false
		}
	}
	return true
}
