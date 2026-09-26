// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package search

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testdata = "../../testdata/mime"

func word(f Field, s string) Term   { return Term{Field: f, Text: s} }
func phrase(f Field, s string) Term { return Term{Field: f, Text: s, Phrase: true} }

func TestParse(t *testing.T) {
	day := func(y, m, d int) *Date { return &Date{Year: y, Month: time.Month(m), Day: d} }
	tests := []struct {
		in   string
		want Query
	}{
		{"faktura", Query{Terms: []Term{word(FieldAny, "faktura")}}},
		{`"dobrý den"`, Query{Terms: []Term{phrase(FieldAny, "dobrý den")}}},
		{"„dobrý den“", Query{Terms: []Term{phrase(FieldAny, "dobrý den")}}},
		{"“hello world”", Query{Terms: []Term{phrase(FieldAny, "hello world")}}},
		{`"unbalanced quote`, Query{Terms: []Term{phrase(FieldAny, "unbalanced quote")}}},
		{`from:jan to:"Radka K" subject:faktura`, Query{Terms: []Term{
			word(FieldSender, "jan"), phrase(FieldRecipients, "Radka K"), word(FieldSubject, "faktura")}}},
		{"FROM:Jan", Query{Terms: []Term{word(FieldSender, "Jan")}}},
		{`from:"Jan Novák"`, Query{Terms: []Term{phrase(FieldSender, "Jan Novák")}}},
		{"has:attachment is:unread is:flagged", Query{HasAttachment: true, Unread: true, Flagged: true}},
		{"has:foo is:read", Query{Terms: []Term{word(FieldAny, "has:foo"), word(FieldAny, "is:read")}}},
		{"before:2026-09-01 after:2026-08-01", Query{Before: day(2026, 9, 1), After: day(2026, 8, 1)}},
		// Not a day: searched as a word, never a silently shifted date.
		{"before:2026-02-30", Query{Terms: []Term{word(FieldAny, "before:2026-02-30")}}},
		{`in:Inbox in:"Sent Items"`, Query{In: []string{"Inbox", "Sent Items"}}},
		{"from:", Query{Terms: []Term{word(FieldAny, "from:")}}},
		{"from: jan", Query{Terms: []Term{word(FieldAny, "from:"), word(FieldAny, "jan")}}},
		{"foo:bar", Query{Terms: []Term{word(FieldAny, "foo:bar")}}},
		{`unknown:"x y"`, Query{Terms: []Term{word(FieldAny, "unknown"), phrase(FieldAny, "x y")}}},
		{"https://example.cz/a?b=c", Query{Terms: []Term{word(FieldAny, "https://example.cz/a?b=c")}}},
		{"AND OR NOT NEAR", Query{Terms: []Term{
			word(FieldAny, "AND"), word(FieldAny, "OR"), word(FieldAny, "NOT"), word(FieldAny, "NEAR")}}},
		{`* ^ -- :: ""`, Query{}},
		{"from:---", Query{}},
		{"a\x00b\x07c", Query{Terms: []Term{word(FieldAny, "abc")}}},
		{"  \t\n ", Query{}},
	}
	for _, tc := range tests {
		if got := Parse(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseLongInput(t *testing.T) {
	in := strings.Repeat(`a"b from:"x `, 1<<16) // about 800 KiB, quotes everywhere
	q := Parse(in)
	if len(q.Terms) == 0 {
		t.Fatal("no terms from a long input")
	}
	expr := q.MatchExpr()
	if strings.Count(expr, `"`)%2 != 0 {
		t.Fatal("unbalanced quotes in the expression")
	}
}

func TestQueryEmptyAndSize(t *testing.T) {
	if !Parse("").Empty() || !Parse(`"" --`).Empty() {
		t.Error("an input with nothing to search by should be empty")
	}
	q := Parse(`a "b c" from:d has:attachment is:unread before:2026-01-01 in:x in:y`)
	if q.Empty() || q.Size() != 8 {
		t.Errorf("Size = %d, want 8", q.Size())
	}
	if Parse("is:unread").HasText() || !Parse("x").HasText() {
		t.Error("HasText")
	}
}

func TestMatchExpr(t *testing.T) {
	tests := []struct{ in, want string }{
		{`faktura from:jan "dobrý den"`, `"faktura"* AND sender : "jan"* AND "dobrý den"`},
		{"to:x subject:y", `recipients : "x"* AND subject : "y"*`},
		{`a"b`, `"a""b"*`},
		{"NEAR(a b)", `"NEAR(a"* AND "b)"*`},
		{"is:unread has:attachment", ""},
		{"e-mail", `"e-mail"*`},
	}
	for _, tc := range tests {
		if got := Parse(tc.in).MatchExpr(); got != tc.want {
			t.Errorf("MatchExpr(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"faktura", `from:"a b" to:c`, `"x`, "in:„a“ is:unread", "a\x00\"\"\"", "NEAR(a b)*^:"} {
		f.Add(s)
	}
	allowed := map[string]bool{"subject": true, "sender": true, "recipients": true}
	f.Fuzz(func(t *testing.T, s string) {
		q := Parse(s)
		for _, term := range q.Terms {
			if !hasWordRune(term.Text) || strings.TrimSpace(term.Text) != term.Text ||
				strings.IndexFunc(term.Text, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) >= 0 {
				t.Fatalf("bad term %+v", term)
			}
		}
		expr := q.MatchExpr()
		if strings.Count(expr, `"`)%2 != 0 {
			t.Fatalf("unbalanced quotes in %q", expr)
		}
		// Outside quoted strings only AND, column names, ':' and '*'.
		inQuote := false
		var outside strings.Builder
		for _, r := range expr {
			if r == '"' {
				inQuote = !inQuote
				outside.WriteByte(' ')
				continue
			}
			if !inQuote {
				outside.WriteRune(r)
			}
		}
		for _, tok := range strings.Fields(outside.String()) {
			if tok != "AND" && tok != ":" && tok != "*" && !allowed[tok] {
				t.Fatalf("unexpected %q outside strings in %q", tok, expr)
			}
		}
	})
}

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Přílohy":            "prilohy",
		"ŽLUŤOUČKÝ KŮŇ":      "zlutoucky kun",
		"Ďábelské ódy":       "dabelske ody",
		"e\u0301":            "e", // decomposed input
		"ABC123":             "abc123",
		"úpěl ďábelské ódy.": "upel dabelske ody.",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokens(t *testing.T) {
	s := "e-mail jan@firma.cz, Dobrý!"
	var keys []string
	for _, tok := range Tokens(s) {
		keys = append(keys, tok.Key)
		if Fold(s[tok.Start:tok.End]) != tok.Key {
			t.Errorf("span %d:%d = %q does not fold to %q", tok.Start, tok.End, s[tok.Start:tok.End], tok.Key)
		}
	}
	if want := []string{"e", "mail", "jan", "firma", "cz", "dobry"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if len(Tokens("")) != 0 || len(Tokens(" -- ")) != 0 {
		t.Error("tokens from nothing")
	}
}

func highlighted(s string, rs []api.MatchRange) []string {
	var out []string
	for _, r := range rs {
		out = append(out, s[r.Start:r.End])
	}
	return out
}

func TestExcerpt(t *testing.T) {
	czech := "Dobrý den,\n\nposílám přílohy k faktuře č. 2026/118 za měsíc září."
	tests := []struct {
		name, text, query string
		wantOK            bool
		wantHits          []string
		wantPrefix        bool // excerpt starts with an ellipsis
		wantSuffix        bool
	}{
		{"prefix without diacritics", czech, "priloh", true, []string{"přílohy"}, false, false},
		{"two words", czech, "faktur zari", true, []string{"faktuře", "září"}, false, false},
		{"phrase across lines", czech, `"dobry den"`, true, []string{"Dobrý den"}, false, false},
		{"overlapping terms merge", czech, "pri priloh", true, []string{"přílohy"}, false, false},
		{"no match", czech, "invoice", false, nil, false, false},
		{"field terms do not highlight the body", czech, "from:dobry subject:den", false, nil, false, false},
		{"filters only", czech, "is:unread", false, nil, false, false},
		{"far in", strings.Repeat("slovo ", 1000) + "cíl " + strings.Repeat("dál ", 1000), "cil", true, []string{"cíl"}, true, true},
		{"at the end", strings.Repeat("slovo ", 100) + "konec", "konec", true, []string{"konec"}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex, rs, ok := Excerpt(tc.text, Parse(tc.query), 200)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (%q)", ok, tc.wantOK, ex)
			}
			if !ok {
				return
			}
			checkExcerpt(t, ex, rs, 200)
			if got := highlighted(ex, rs); !reflect.DeepEqual(got, tc.wantHits) {
				t.Errorf("highlights %q, want %q in %q", got, tc.wantHits, ex)
			}
			if strings.HasPrefix(ex, "…") != tc.wantPrefix || strings.HasSuffix(ex, "…") != tc.wantSuffix {
				t.Errorf("ellipses wrong in %q", ex)
			}
		})
	}
}

func TestExcerptHostileText(t *testing.T) {
	text := "\xff\xfe prilohy \u202efdp.exe\u202c za\u200bver \u200dz\u200cj " + strings.Repeat("x", 1<<20) + " priloha"
	ex, rs, ok := Excerpt(text, Parse("priloh"), 200)
	if !ok {
		t.Fatal("no excerpt")
	}
	checkExcerpt(t, ex, rs, 200)
	if strings.ContainsAny(ex, "\u202e\u202c\u200b") {
		t.Errorf("formatting characters kept: %q", ex)
	}
	if !strings.Contains(ex, "\u200d") || !strings.Contains(ex, "\u200c") {
		t.Errorf("joiners dropped: %q", ex)
	}
	if got := highlighted(ex, rs); !reflect.DeepEqual(got, []string{"prilohy"}) {
		t.Errorf("highlights %q", got)
	}
}

// checkExcerpt asserts what every excerpt promises.
func checkExcerpt(t *testing.T, ex string, rs []api.MatchRange, maxRunes int) {
	t.Helper()
	if !utf8.ValidString(ex) {
		t.Fatalf("invalid UTF-8: %q", ex)
	}
	if n := utf8.RuneCountInString(ex); n > maxRunes+2 {
		t.Fatalf("%d runes, cap %d", n, maxRunes)
	}
	for _, r := range ex {
		if dropInExcerpt(r) {
			t.Fatalf("dropped character %U kept in %q", r, ex)
		}
	}
	prev := -1
	for _, r := range rs {
		if r.Start < 0 || r.Start >= r.End || r.End > len(ex) || r.Start <= prev {
			t.Fatalf("bad range %+v in %q (%v)", r, ex, rs)
		}
		if !utf8.RuneStart(ex[r.Start]) || (r.End < len(ex) && !utf8.RuneStart(ex[r.End])) {
			t.Fatalf("range %+v splits a rune in %q", r, ex)
		}
		prev = r.End
	}
}

func FuzzExcerpt(f *testing.F) {
	f.Add("Dobrý den, posílám přílohy.", "priloh")
	f.Add("\xff\u202e a\u0301\u0301b", `"a b" x`)
	f.Add("  \n\t ", "a")
	f.Fuzz(func(t *testing.T, text, query string) {
		ex, rs, ok := Excerpt(text, Parse(query), 50)
		if ok {
			checkExcerpt(t, ex, rs, 50)
		}
	})
}

// TestExcerptFixtures runs the excerpt over the text of every test message
// (CLAUDE.md rule 3: the pathological ones too).
func TestExcerptFixtures(t *testing.T) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".eml") {
			continue
		}
		f, err := os.Open(filepath.Join(testdata, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := mime.Parse(f, mime.DefaultLimits())
		f.Close()
		if err != nil {
			continue // unparsable fixtures never reach the index
		}
		texts[e.Name()] = p.Text
		for _, q := range []string{"a", "priloh", `"dobry den"`, "x", "the"} {
			if ex, rs, ok := Excerpt(p.Text, Parse(q), 200); ok {
				checkExcerpt(t, ex, rs, 200)
			}
		}
	}
	ex, rs, ok := Excerpt(texts["search-czech.eml"], Parse("priloh faktur"), 200)
	if !ok || !reflect.DeepEqual(highlighted(ex, rs), []string{"přílohy", "faktuře", "PŘÍLOHA"}) {
		t.Errorf("czech fixture: %q %v", ex, rs)
	}
	ex, _, ok = Excerpt(texts["search-hostile.eml"], Parse("prilohy"), 200)
	if !ok || strings.ContainsAny(ex, "\u202e\u2066\ufeff") {
		t.Errorf("hostile fixture: %q", ex)
	}
}
