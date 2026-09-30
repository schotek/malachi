// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package botclean cleans Jira comments, above all the ones a
// synchronisation bot relays from another site.
//
// An integration that mirrors comments between two Jira sites (for example
// "Issue Sync – Synchronization for Jira") posts every remote comment under
// its own account and names the real author and time in a header line:
//
//	ITSD-19 Jana Dvořáková added comment - 10/06/26 14:39 GMT+2
//
// sometimes followed by technical lines such as "Remote comment create
// date: …". For a comment whose author is one of Rules.BotNames, Clean
// takes the author and the time from that header, removes the header and
// strips Rules.AuthorPrefixes (a company name) from the author. From every
// comment, whoever wrote it, it removes the lines that match one of
// Rules.MetadataFilters. It never empties a comment: when nothing would be
// left, the lines stay (a header-only comment keeps its header and is still
// re-attributed).
//
// The input is the HTML the site rendered for the comment (renderedBody).
// Clean parses it with golang.org/x/net/html as the content of a <body>,
// removes whole lines from the tree and renders it again; when it removes
// nothing, the input comes back byte for byte. A line is a stretch of the
// comment between two of: the start or end of a block element (p, div,
// h1–h6 and similar containers), a <br>, and a newline in the text (Jira
// ends a line with <br/> and a newline; a newline alone is how a hand-made
// body does it). Inline elements (a, b, span, …) belong to the line they
// are in. Preformatted text, tables, lists, quotes, forms and similar
// blocks are left alone as a whole: a header or metadata line inside them
// is quoted content, never matched and never removed. Pictures and other
// embedded objects are never removed, and a line holding one but no text
// is blank for finding the header. What a line is matched on is its visible
// text: control and format characters (bidi controls, zero-width
// characters, soft hyphens) dropped, every run of white space (including
// no-break spaces) one space, no space at either end. Blank lines next to
// removed ones and at either end of the comment go with them, like the
// newlines the prototype trimmed.
//
// The header is the first line with visible text that no filter removed.
// It must read, in full,
//
//	KEY-123 <author> added comment - <date>
//
// (one or more spaces anywhere a space is, "-" or an en or em dash before
// the date, at most 512 bytes, the date part at most 64). The author must
// not be blank; the date part is read by the rules of parseDate and, when it
// cannot be, the fallback time passed to Clean stands in for it (the author
// is still taken).
//
// A comment's author is a bot when its display name, compared the way
// botKey normalises names (lower case, dashes unified, invisible characters
// dropped, spaces collapsed), equals a configured name or, when the
// configured name has at least three characters, contains it as whole
// words. A person called "Sync" or "Jo" is not the bot "Issue Sync –
// Synchronization for Jira"; the prototype's containment in both directions
// would have said so.
//
// This is a cleaner, not a security boundary: what it returns is stored and
// goes through internal/sanitize before anything displays it. It does hold
// up against hostile input: the input is capped (MaxInputBytes), so are the
// lines a regular expression sees and the number of lines; input the parser
// could inflate (formatting elements left open, which the parser re-opens
// at every following line, and svg or math content, where that cannot be
// counted beforehand) and a tree nested too deeply are returned unchanged;
// and a panic would be caught and the comment returned as it came.
//
// The rules come from the prototype (JiraMaster, BotCommentParser.kt) with
// the deliberate differences named here and in parseDate. The prototype's
// rule that dropped a lone "[attachment]" marker line does not apply to
// HTML, where a picture is an element that is never removed.
package botclean

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Version identifies the cleaning rules of this package. It is part of Key:
// bump it on any change of behaviour so that stored comments are rebuilt.
const Version = "1"

// Limits.
const (
	// MaxInputBytes is the largest comment HTML Clean works on; a larger
	// one is returned as it came.
	MaxInputBytes = 1 << 20
	// MaxEntries and MaxEntryBytes bound each list of Rules and each of its
	// entries. They equal api.MaxJiraListEntries and api.MaxJiraPatternBytes,
	// which the account configuration is validated against; Compile skips
	// and reports what exceeds them.
	MaxEntries    = 32
	MaxEntryBytes = 512

	maxLineBytes   = 4096   // a line with more visible text is never matched by a filter
	maxHeaderBytes = 512    // a longer first line is never a header
	maxDateBytes   = 64     // a header whose date part is longer is not one
	maxNameBytes   = 4096   // of a display name looked at
	maxLines       = 20_000 // a comment with more lines is left as it is
	maxDepth       = 1024   // elements nested deeper are left alone
)

// Rules are the account's settings for cleaning comments
// (api.JiraConfig.BotNames, MetadataFilters and AuthorPrefixes).
type Rules struct {
	// BotNames are the display names of integrations that relay comments
	// with a header naming the real author.
	BotNames []string
	// MetadataFilters are RE2 patterns. A line whose whole visible text
	// matches one of them is removed, from every comment, whoever wrote it.
	MetadataFilters []string
	// AuthorPrefixes are stripped, in order, from the start of the author
	// named in a header ("ACME Jana Dvořáková" → "Jana Dvořáková" for
	// "ACME"): ignoring case, only as whole words, never leaving the name
	// empty.
	AuthorPrefixes []string
}

// Compiled is a compiled set of Rules. It is immutable and safe for
// concurrent use; a nil *Compiled cleans with no rules.
type Compiled struct {
	bots     []string // botKey form, sorted, unique
	filters  []*regexp.Regexp
	prefixes []string // visible form, in order
	key      string
}

var noRules, _ = Compile(Rules{})

// Compile checks and compiles the rules. Entries that cannot be used (an
// invalid pattern, one over MaxEntryBytes, entries past MaxEntries) are
// skipped, each reported by an error; the rest is compiled all the same.
// Empty entries are ignored silently.
func Compile(r Rules) (*Compiled, []error) {
	c := &Compiled{}
	var errs []error
	for i, name := range capped("bot names", r.BotNames, &errs) {
		if len(name) > MaxEntryBytes {
			errs = append(errs, fmt.Errorf("bot name %d: longer than %d bytes", i+1, MaxEntryBytes))
			continue
		}
		if k := botKey(name); k != "" {
			c.bots = append(c.bots, k)
		}
	}
	c.bots = uniqueSorted(c.bots)

	var sources []string
	for i, p := range capped("metadata filters", r.MetadataFilters, &errs) {
		if p == "" {
			continue
		}
		if len(p) > MaxEntryBytes {
			errs = append(errs, fmt.Errorf("metadata filter %d: longer than %d bytes", i+1, MaxEntryBytes))
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("metadata filter %d: %w", i+1, err))
			continue
		}
		// Leftmost-longest: the match that starts first is the longest one
		// there, so a match of the whole line is found if there is one (see
		// wholeMatch). Wrapping the pattern in ^(?:…)$ would do the same but
		// breaks a valid pattern that ends inside \Q….
		re.Longest()
		c.filters = append(c.filters, re)
		sources = append(sources, p)
	}

	for i, p := range capped("author prefixes", r.AuthorPrefixes, &errs) {
		if len(p) > MaxEntryBytes {
			errs = append(errs, fmt.Errorf("author prefix %d: longer than %d bytes", i+1, MaxEntryBytes))
			continue
		}
		if v, _ := visible(p, 0); v != "" {
			c.prefixes = append(c.prefixes, v)
		}
	}

	c.key = rulesKey(c.bots, uniqueSorted(sources), c.prefixes)
	return c, errs
}

// Key is a stable hash of the rules as they act (Version included): equal
// for rules that clean every comment the same way, such as the same bot
// names in another order, different when they may not. It is meant to be
// part of the key that decides whether stored comments must be rebuilt.
func (c *Compiled) Key() string {
	if c == nil {
		c = noRules
	}
	return c.key
}

// Result is what Clean made of one comment.
type Result struct {
	// AuthorName is the person named in the header of a relayed comment,
	// with AuthorPrefixes stripped and free of control and format
	// characters; "" = the comment's own author stands.
	AuthorName string
	// Created is the time in the header of a relayed comment, or the
	// fallback passed to Clean when the header's date cannot be read;
	// zero = the comment's own time stands.
	Created time.Time
	// HTML is the cleaned comment; the input itself when no line was
	// removed.
	HTML string
	// Text is the visible text of HTML, one line per line of the comment,
	// blank lines left out; "" when the input was not parsed (empty, over
	// MaxInputBytes, refused by the parser).
	Text string
	// Via is the short name of the bot that relayed a re-attributed
	// comment ("Issue Sync"); "" when the comment is not re-attributed.
	Via string
	// Changed reports that HTML differs from the input or that the comment
	// is re-attributed.
	Changed bool
}

// onPanic receives what a panic in Clean carried; tests make it fatal.
var onPanic = func(any) {}

// Clean cleans one comment: authorName is the display name of its Jira
// author, html its rendered body, fallback its own creation time (used when
// a header's date cannot be read, and its location for a header time in an
// unknown zone). See the package documentation for the rules.
func (c *Compiled) Clean(authorName, html string, fallback time.Time) (res Result) {
	defer func() {
		if v := recover(); v != nil {
			onPanic(v)
			res = Result{HTML: html}
		}
	}()
	if c == nil {
		c = noRules
	}
	res.HTML = html
	if html == "" || len(html) > MaxInputBytes {
		return res
	}
	d, err := parse(html)
	if err != nil {
		return res
	}
	if d.tooMany {
		res.Text = d.text()
		return res
	}

	remove := make(map[*line]bool)
	for _, l := range d.lines {
		if c.metadata(l) {
			remove[l] = true
		}
	}
	if len(remove) > 0 && !d.contentWithout(remove) {
		clear(remove) // the filters would empty the comment
	}

	if c.isBot(authorName) {
		if l := d.firstText(remove); l != nil {
			if author, created, ok := c.header(l, fallback); ok {
				remove[l] = true
				if !d.contentWithout(remove) {
					delete(remove, l) // the header is all there is
				}
				res.AuthorName = author
				res.Created = created
				res.Via = shortName(authorName)
				res.Changed = true
			}
		}
	}

	if len(remove) > 0 {
		out, err := d.cut(remove)
		if err != nil {
			// The tree is half edited: describe the input instead.
			if d, err := parse(html); err == nil {
				res.Text = d.text()
			}
			return res
		}
		res.HTML = out
		res.Changed = true
	}
	res.Text = d.text()
	return res
}

// metadata reports whether one of the filters matches the whole of l.
func (c *Compiled) metadata(l *line) bool {
	if l.opaque || l.blank || l.long {
		return false
	}
	for _, re := range c.filters {
		if wholeMatch(re, l.text) {
			return true
		}
	}
	return false
}

// wholeMatch reports whether re, compiled with Longest, matches all of s.
func wholeMatch(re *regexp.Regexp, s string) bool {
	loc := re.FindStringIndex(s)
	return loc != nil && loc[0] == 0 && loc[1] == len(s)
}

// headerRE is the prototype's header pattern, also taking an en or em dash
// before the date. The key must be ASCII: a look-alike letter is no key.
var headerRE = regexp.MustCompile(`^([A-Z][A-Z0-9]*-\d+)\s+(.+?)\s+added\s+comment\s*[-\x{2013}\x{2014}]\s*(.+)$`)

// header reads l as the header of a relayed comment.
func (c *Compiled) header(l *line, fallback time.Time) (author string, created time.Time, ok bool) {
	if l.opaque || l.long || len(l.text) > maxHeaderBytes {
		return "", time.Time{}, false
	}
	m := headerRE.FindStringSubmatch(l.text)
	if m == nil || len(m[3]) > maxDateBytes {
		return "", time.Time{}, false
	}
	author = c.stripPrefixes(strings.TrimSpace(m[2]))
	if author == "" {
		return "", time.Time{}, false
	}
	created, ok = parseDate(m[3], fallback)
	if !ok {
		created = fallback
	}
	return author, created, true
}

// stripPrefixes strips the configured prefixes from name, which is in
// visible form (single spaces).
func (c *Compiled) stripPrefixes(name string) string {
	for _, p := range c.prefixes {
		rest, ok := cutPrefixFold(name, p)
		if !ok || !strings.HasPrefix(rest, " ") {
			continue // absent, or not a whole word ("ACMEX" for "ACME")
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			name = rest
		}
	}
	return name
}

// cutPrefixFold is strings.CutPrefix ignoring case (simple Unicode folding).
func cutPrefixFold(s, prefix string) (string, bool) {
	for _, pr := range prefix {
		sr, size := utf8.DecodeRuneInString(s)
		if size == 0 {
			return "", false
		}
		if sr != pr && !strings.EqualFold(string(sr), string(pr)) {
			return "", false
		}
		s = s[size:]
	}
	return s, true
}

// isBot reports whether a comment by authorName is relayed by a bot.
func (c *Compiled) isBot(authorName string) bool {
	if len(c.bots) == 0 {
		return false
	}
	author := botKey(authorName)
	if author == "" {
		return false
	}
	authorLen := utf8.RuneCountInString(author)
	for _, bot := range c.bots {
		if author == bot {
			return true
		}
		if utf8.RuneCountInString(bot) >= 3 && authorLen >= 3 && containsWords(author, bot) {
			return true
		}
	}
	return false
}

// containsWords reports whether s contains w with no letter or digit
// directly before or after it.
func containsWords(s, w string) bool {
	for from := 0; from <= len(s)-len(w); {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(w)
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if (start == 0 || !wordRune(before)) && (end == len(s) || !wordRune(after)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[start:])
		from = start + size
	}
	return false
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// botKey is the form bot names are compared in: visible text, lower case,
// every hyphen, dash and minus sign "-".
func botKey(name string) string {
	v, _ := visible(name, maxNameBytes)
	return strings.Map(func(r rune) rune {
		switch r {
		case 0x2010, 0x2011, 0x2012, 0x2013, 0x2014, 0x2015, 0x2212, 0xfe58, 0xfe63, 0xff0d:
			return '-'
		}
		return r
	}, strings.ToLower(v))
}

// shortName is the bot's name up to its first en or em dash, " - " or "("
// ("Issue Sync – Synchronization for Jira" → "Issue Sync"), in visible
// form; the whole name when that leaves nothing.
func shortName(name string) string {
	v, _ := visible(name, maxNameBytes)
	cut := len(v)
	for _, sep := range []string{string(rune(0x2013)), string(rune(0x2014)), " - ", "("} {
		if i := strings.Index(v, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	if s := strings.TrimSpace(v[:cut]); s != "" {
		return s
	}
	return v
}

// texter collects visible text: control and format characters dropped,
// every run of white space one space, none at either end. With max > 0 it
// stops once the text would grow past max bytes and reports long.
type texter struct {
	b     strings.Builder
	max   int
	space bool
	long  bool
}

func (t *texter) write(s string) {
	if t.long {
		return
	}
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			t.space = true
		case invisible(r):
		default:
			if t.max > 0 && t.b.Len()+utf8.RuneLen(r)+1 > t.max {
				t.long = true
				return
			}
			if t.space && t.b.Len() > 0 {
				t.b.WriteByte(' ')
			}
			t.space = false
			t.b.WriteRune(r)
		}
	}
}

func (t *texter) reset() {
	t.b.Reset()
	t.space = false
	t.long = false
}

// visible returns s as a reader sees it (see texter).
func visible(s string, max int) (string, bool) {
	t := texter{max: max}
	t.write(s)
	return t.b.String(), t.long
}

// invisible reports control and format characters: C0 and C1 controls,
// bidi controls and isolates, zero-width characters, the soft hyphen, the
// byte order mark.
func invisible(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// blankText reports whether s has no visible character.
func blankText(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && !invisible(r) {
			return false
		}
	}
	return true
}

func capped(what string, list []string, errs *[]error) []string {
	if len(list) > MaxEntries {
		*errs = append(*errs, fmt.Errorf("%s: %d entries, only the first %d are used", what, len(list), MaxEntries))
		return list[:MaxEntries]
	}
	return list
}

func uniqueSorted(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	n := 0
	for i, s := range out {
		if i == 0 || s != out[n-1] {
			out[n] = s
			n++
		}
	}
	return out[:n]
}

func rulesKey(bots, filters, prefixes []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "botclean %s\n", Version)
	for _, part := range []struct {
		tag  string
		list []string
	}{{"bots", bots}, {"filters", filters}, {"prefixes", prefixes}} {
		fmt.Fprintf(h, "%s %d\n", part.tag, len(part.list))
		for _, s := range part.list {
			fmt.Fprintf(h, "%d %s\n", len(s), s)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}
