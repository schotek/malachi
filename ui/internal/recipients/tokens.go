// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package recipients is the model behind the To/Cc/Bcc fields of the
// compose window: every finished address is a token (a badge with an ×),
// what is still being typed is the pending text after the last token.
// Pure logic, no widgets; the view shows Items and Pending and feeds every
// keystroke to SetPending. It rests on the same list rules as
// compose.ParseAddressList (commas and semicolons outside quotes and angle
// brackets separate, net/mail reads one mailbox), repeated here because
// package compose imports GTK. Email is hostile input: the text taken in is
// bounded, stripped of control characters and the token count is capped.
package recipients

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// MaxInput is how many bytes of pasted text (pending plus the paste) are
	// looked at; the rest of a paste is ignored. Typed and initial text is
	// never clipped: nothing a user has in a field is lost silently.
	MaxInput = 64 << 10
	// MaxTokens is the most tokens text is split into automatically; the
	// rest of it stays in Pending verbatim. Add and Commit still append.
	MaxTokens = 1000
)

// Token is one finished entry of the field.
type Token struct {
	// Raw is the trimmed text the token was made from.
	Raw string
	// Address is the parsed mailbox, nil when Raw is not one.
	Address *api.Address
}

// Valid reports whether the entry parsed as a mailbox.
func (t Token) Valid() bool { return t.Address != nil }

// Label is what the badge shows: the display name, else the address; for an
// invalid entry its text. Direction override and isolate characters are
// removed so that it cannot reorder the surrounding UI.
func (t Token) Label() string {
	if t.Address == nil {
		return stripBidi(t.Raw)
	}
	if name := strings.TrimSpace(t.Address.Name); name != "" {
		return stripBidi(name)
	}
	return stripBidi(t.Address.Address)
}

// Tooltip is the full "Name <addr>" form, or the text of an invalid entry.
func (t Token) Tooltip() string {
	if t.Address == nil {
		return stripBidi(t.Raw)
	}
	return stripBidi(formatAddress(*t.Address))
}

// Tokens is the value of one recipient field.
type Tokens struct {
	items   []Token
	pending string
}

// New is an empty field.
func New() *Tokens { return &Tokens{} }

// NewTokens reads a whole field value: every non-empty entry becomes a
// token, valid or not. Nothing is pending unless MaxTokens is reached; the
// text from the first entry that no longer fits stays in Pending verbatim.
func NewTokens(text string) *Tokens {
	t := &Tokens{}
	text = sanitize(text)
	spans := split(text, false)
	if i := t.splitInto(text, spans, true); i >= 0 {
		t.pending = trimLeft(text[spans[i].start:])
	}
	return t
}

// Resolved is the recipients as they would be if pending were committed now,
// without changing the model: the addresses of the valid tokens in order, the
// raw text of the invalid ones (the shape of compose.ParseAddressList), the
// non-blank pending text evaluated as Commit would, so it may yield several
// entries. Unlike re-parsing Text it never merges or re-reads tokens.
func (t *Tokens) Resolved() (addresses []api.Address, invalid []string) {
	c := &Tokens{items: append([]Token(nil), t.items...), pending: t.pending}
	c.Commit()
	for _, tok := range c.items {
		if tok.Address != nil {
			addresses = append(addresses, *tok.Address)
		} else {
			invalid = append(invalid, tok.Raw)
		}
	}
	return addresses, invalid
}

// Items are the tokens in order. The slice is the model's own; do not modify.
func (t *Tokens) Items() []Token { return t.items }

// Pending is the text typed after the last token.
func (t *Tokens) Pending() string { return t.pending }

// HasInvalid reports whether any token is not a mailbox.
func (t *Tokens) HasInvalid() bool {
	for _, tok := range t.items {
		if !tok.Valid() {
			return true
		}
	}
	return false
}

// Text is the field value the rest of the app sees: valid tokens as
// FormatAddressList writes them, invalid ones as typed, then the pending
// text unless blank, joined by ", ".
func (t *Tokens) Text() string {
	parts := make([]string, 0, len(t.items)+1)
	for _, tok := range t.items {
		if tok.Address != nil {
			parts = append(parts, formatAddress(*tok.Address))
		} else {
			parts = append(parts, tok.Raw)
		}
	}
	if p := trim(t.pending); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// SetPending takes the editor's full text after a keystroke. Entries
// completed by a separator become tokens and the rest stays pending; so
// does a bare address followed by whitespace ("a@b.cz "). It reports
// whether the tokens changed, in which case the editor must be reset to
// Pending.
func (t *Tokens) SetPending(text string) bool {
	text = sanitize(text)
	spans := split(text, false)
	if len(spans) > 1 {
		if i := t.splitInto(text, spans[:len(spans)-1], true); i >= 0 {
			t.pending = trimLeft(text[spans[i].start:])
			return true
		}
		t.pending = trimLeft(text[spans[len(spans)-1].start:])
		t.fold()
		return true
	}
	t.pending = text
	return t.fold()
}

// fold turns a pending bare addr-spec that ends in whitespace into a token.
func (t *Tokens) fold() bool {
	r, _ := utf8.DecodeLastRuneInString(t.pending)
	if t.pending == "" || !isSpace(r) || len(t.items) >= MaxTokens {
		return false
	}
	raw := trim(t.pending)
	a, ok := parse(raw)
	if !ok || a.Name != "" || strings.Contains(raw, "<") {
		return false
	}
	t.pending = ""
	t.addToken(raw, &a)
	return true
}

// Commit turns non-blank pending text into tokens, valid or not (Enter,
// Tab, focus loss); blank pending is just cleared. Normally pending is one
// entry; the rest left by MaxTokens is split into entries and appended past
// the cap. It reports whether the tokens changed.
func (t *Tokens) Commit() bool {
	text := t.pending
	t.pending = ""
	n := len(t.items)
	text = sanitize(text)
	t.splitInto(text, split(text, false), false)
	return len(t.items) != n
}

// Add appends a valid token (a suggestion picked) and clears pending.
func (t *Tokens) Add(a api.Address) {
	t.pending = ""
	a.Name = strings.TrimSpace(sanitize(a.Name))
	t.addToken(formatAddress(a), &a)
}

// Remove drops the token at index; out of range does nothing.
func (t *Tokens) Remove(index int) {
	if index < 0 || index >= len(t.items) {
		return
	}
	t.items = append(t.items[:index:index], t.items[index+1:]...)
}

// Edit commits pending, removes the token at index and makes its text the
// pending string, which it returns. Out of range changes nothing and
// returns the current pending text.
func (t *Tokens) Edit(index int) string {
	if index < 0 || index >= len(t.items) {
		return t.pending
	}
	t.Commit()
	tok := t.items[index]
	t.Remove(index)
	if tok.Address != nil {
		t.pending = formatAddress(*tok.Address)
	} else {
		t.pending = tok.Raw
	}
	return t.pending
}

// Paste adds pasted text to pending. Without any separator (comma,
// semicolon, line break, tab) it is typed text and goes through SetPending;
// otherwise every entry, the last one too, becomes a token.
func (t *Tokens) Paste(text string) {
	room := MaxInput - len(t.pending)
	if room < 0 {
		room = 0
	}
	all := t.pending + cut(text, room)
	if len(split(all, true)) <= 1 {
		t.SetPending(all)
		return
	}
	t.pending = ""
	sp := split(all, true)
	// Line breaks separate here, so work on the text with them still in.
	if i := t.splitInto(all, sp, true); i >= 0 {
		// Line breaks and tabs were separators: keep them as commas.
		var rest []string
		for _, s := range sp[i:] {
			if raw := trim(sanitize(all[s.start:s.end])); raw != "" {
				rest = append(rest, raw)
			}
		}
		t.pending = strings.Join(rest, ", ")
	}
}

// splitInto makes a token of every non-empty entry of text in spans. When
// capped and MaxTokens is reached it stops and returns the index of the first
// entry left, whose text and everything after it the caller keeps; -1 when
// all were taken.
func (t *Tokens) splitInto(text string, spans []span, capped bool) int {
	for i, sp := range spans {
		raw := trim(sanitize(text[sp.start:sp.end]))
		if raw == "" {
			continue
		}
		if capped && len(t.items) >= MaxTokens {
			return i
		}
		t.addEntry(raw)
	}
	return -1
}

func (t *Tokens) addEntry(raw string) {
	if a, ok := parse(raw); ok {
		t.addToken(raw, &a)
		return
	}
	t.addToken(raw, nil)
}

func (t *Tokens) addToken(raw string, a *api.Address) {
	t.items = append(t.items, Token{Raw: raw, Address: a})
}

// parse reads one mailbox the way compose.ParseAddressList does.
func parse(raw string) (api.Address, bool) {
	a, err := mail.ParseAddress(raw)
	if err != nil {
		return api.Address{}, false
	}
	return api.Address{Name: a.Name, Address: a.Address}, true
}

// formatAddress is one element of compose.FormatAddressList.
func formatAddress(a api.Address) string {
	name := strings.TrimSpace(a.Name)
	switch {
	case name == "":
		return a.Address
	case strings.ContainsAny(name, `,;<>"\`):
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `" <` + a.Address + `>`
	default:
		return name + " <" + a.Address + ">"
	}
}

// span is a byte range [start, end) of a string.
type span struct{ start, end int }

// split is compose.splitAddressRanges; with lineBreaks, CR, LF and tab
// outside quotes and angle brackets separate as well.
func split(s string, lineBreaks bool) []span {
	var (
		out     []span
		start   int
		quoted  bool
		angled  bool
		escaped bool
	)
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			angled = true
		case r == '>':
			angled = false
		case (r == ',' || r == ';' || (lineBreaks && (r == '\n' || r == '\r' || r == '\t'))) && !angled:
			out = append(out, span{start, i})
			start = i + len(string(r))
		}
	}
	return append(out, span{start, len(s)})
}

// cut is s limited to n bytes, on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// stripBidi removes the direction override, embedding and isolate marks, so
// that text shown in a badge cannot reorder what is around it.
func stripBidi(s string) string {
	if !strings.ContainsFunc(s, isBidi) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isBidi(r) {
			return -1
		}
		return r
	}, s)
}

func isBidi(r rune) bool {
	return r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 ||
		r == 0x200e || r == 0x200f || r == 0x061c
}

// sanitize turns tab and line breaks into spaces and drops the other C0
// controls, DEL and the Unicode line and paragraph separators.
func sanitize(s string) string {
	clean := true
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isSpace is Unicode white space, listed so that the Swift port agrees.
func isSpace(r rune) bool {
	switch {
	case r >= 9 && r <= 13, r == 32, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f,
		r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

func trim(s string) string { return strings.TrimFunc(s, isSpace) }

func trimLeft(s string) string { return strings.TrimLeftFunc(s, isSpace) }
