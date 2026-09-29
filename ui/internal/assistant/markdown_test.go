// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// plain is one unstyled span.
func plain(s string) []Span { return []Span{{Text: s}} }

// para is a paragraph of one unstyled span.
func para(s string) Block { return Block{Kind: BlockParagraph, Spans: plain(s)} }

func TestMarkdownBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Block
	}{
		{"empty", "", nil},
		{"only blank lines", "\n  \n\t\n", nil},
		{"a paragraph", "Hello world", []Block{para("Hello world")}},
		{"lines stay lines, blank lines split", "a\n  b  \n\n\nc", []Block{para("a\nb"), para("c")}},
		{"line endings", "a\r\nb\rc\n\r\nd", []Block{para("a\nb\nc"), para("d")}},
		{"headings", "# One\n## Two\n### Three", []Block{
			{Kind: BlockHeading, Level: 1, Spans: plain("One")},
			{Kind: BlockHeading, Level: 2, Spans: plain("Two")},
			{Kind: BlockHeading, Level: 3, Spans: plain("Three")},
		}},
		{"what is no heading", "#### Four\n#NoSpace\n# \n    # indented four", []Block{para("#### Four\n#NoSpace\n#\n# indented four")}},
		{"a heading indented three spaces, with a tab", "   ##\tTitle  ", []Block{{Kind: BlockHeading, Level: 2, Spans: plain("Title")}}},
		{"a heading ends a paragraph", "text\n# Head\nmore", []Block{para("text"), {Kind: BlockHeading, Level: 1, Spans: plain("Head")}, para("more")}},
		{"bullets", "- one\n* two", []Block{
			{Kind: BlockBullet, Spans: plain("one")},
			{Kind: BlockBullet, Spans: plain("two")},
		}},
		{"numbered", "1. First\n2. Second\n10. Tenth\n007. Bond", []Block{
			{Kind: BlockNumbered, Number: 1, Spans: plain("First")},
			{Kind: BlockNumbered, Number: 2, Spans: plain("Second")},
			{Kind: BlockNumbered, Number: 10, Spans: plain("Tenth")},
			{Kind: BlockNumbered, Number: 7, Spans: plain("Bond")},
		}},
		{"nesting", "- a\n  - b\n    - c\n  - d\n- e\n\t- f", []Block{
			{Kind: BlockBullet, Level: 0, Spans: plain("a")},
			{Kind: BlockBullet, Level: 1, Spans: plain("b")},
			{Kind: BlockBullet, Level: 2, Spans: plain("c")},
			{Kind: BlockBullet, Level: 1, Spans: plain("d")},
			{Kind: BlockBullet, Level: 0, Spans: plain("e")},
			{Kind: BlockBullet, Level: 1, Spans: plain("f")},
		}},
		{"nested under a numbered item by three or four spaces", "1. a\n   - b\n2. c\n    - d", []Block{
			{Kind: BlockNumbered, Number: 1, Spans: plain("a")},
			{Kind: BlockBullet, Level: 1, Spans: plain("b")},
			{Kind: BlockNumbered, Number: 2, Spans: plain("c")},
			{Kind: BlockBullet, Level: 1, Spans: plain("d")},
		}},
		{"nesting stops at the limit", "- 0\n - 1\n  - 2\n   - 3\n    - 4\n     - 5\n - back", []Block{
			{Kind: BlockBullet, Level: 0, Spans: plain("0")},
			{Kind: BlockBullet, Level: 1, Spans: plain("1")},
			{Kind: BlockBullet, Level: 2, Spans: plain("2")},
			{Kind: BlockBullet, Level: 3, Spans: plain("3")},
			{Kind: BlockBullet, Level: 3, Spans: plain("4")},
			{Kind: BlockBullet, Level: 3, Spans: plain("5")},
			{Kind: BlockBullet, Level: 1, Spans: plain("back")},
		}},
		{"a list survives a blank line", "- a\n\n  - b", []Block{
			{Kind: BlockBullet, Level: 0, Spans: plain("a")},
			{Kind: BlockBullet, Level: 1, Spans: plain("b")},
		}},
		{"a paragraph starts the list afresh", "- a\n\ntext\n\n  - b", []Block{
			{Kind: BlockBullet, Level: 0, Spans: plain("a")},
			para("text"),
			{Kind: BlockBullet, Level: 0, Spans: plain("b")},
		}},
		{"an item continues", "- item\ncontinued\n  more", []Block{{Kind: BlockBullet, Spans: plain("item\ncontinued\nmore")}}},
		{"an item ends a paragraph", "text\n- item", []Block{para("text"), {Kind: BlockBullet, Spans: plain("item")}}},
		{"markers without text, and what is no marker", "- \n* \n1. \n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren",
			[]Block{para("-\n*\n1.\n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren")}},
		{"a code block", "```go\nfunc main() {\n\t**not bold** <b>\n}\n```\nafter", []Block{
			{Kind: BlockCode, Spans: []Span{{Text: "func main() {\n\t**not bold** <b>\n}", Code: true}}},
			para("after"),
		}},
		{"a code block keeps blank lines and indentation", "```\n  a\n\n  b\n```", []Block{
			{Kind: BlockCode, Spans: []Span{{Text: "  a\n\n  b", Code: true}}},
		}},
		{"a fence ends a paragraph", "para\n```\nx\n```", []Block{para("para"), {Kind: BlockCode, Spans: []Span{{Text: "x", Code: true}}}}},
		{"an unclosed fence runs to the end", "text\n```\ncode *x*\n# not a heading", []Block{
			para("text"),
			{Kind: BlockCode, Spans: []Span{{Text: "code *x*\n# not a heading", Code: true}}},
		}},
		{"an empty code block", "```\n```", []Block{{Kind: BlockCode}}},
		{"a fence indented four spaces is text", "    ```\n    x", []Block{para("```\nx")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Markdown(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Markdown(%q) =\n%+v\nwant\n%+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMarkdownInline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Span
	}{
		{"bold", "a **b** c", []Span{{Text: "a "}, {Text: "b", Bold: true}, {Text: " c"}}},
		{"italic", "*it* and _it_", []Span{{Text: "it", Italic: true}, {Text: " and "}, {Text: "it", Italic: true}}},
		{"snake_case stays", "create_draft and read_message_x", plain("create_draft and read_message_x")},
		{"an underscore closes at a word's edge only", "_a_b c_", []Span{{Text: "a_b c", Italic: true}}},
		{"code", "use `a **b** [x](https://x.org)` here", []Span{{Text: "use "}, {Text: "a **b** [x](https://x.org)", Code: true}, {Text: " here"}}},
		{"bold around italic and code", "**bold *it* `c`**", []Span{
			{Text: "bold ", Bold: true}, {Text: "it", Bold: true, Italic: true}, {Text: " ", Bold: true}, {Text: "c", Bold: true, Code: true},
		}},
		{"italic around bold", "*it **b** x*", []Span{{Text: "it ", Italic: true}, {Text: "b", Italic: true, Bold: true}, {Text: " x", Italic: true}}},
		{"no bold in bold", "**a **b** c**", []Span{{Text: "a **b", Bold: true}, {Text: " c**"}}},
		{"no italic in italic", "*a _b_ c*", []Span{{Text: "a _b_ c", Italic: true}}},
		{"bold over lines", "**one\ntwo**", []Span{{Text: "one\ntwo", Bold: true}}},
		{"adjacent code spans merge", "`a``b`", []Span{{Text: "ab", Code: true}}},
		{"an empty code span is text", "`` x", plain("`` x")},
		{"unclosed bold", "**open", plain("**open")},
		{"unclosed italic", "*open and _open", plain("*open and _open")},
		{"unclosed code", "`open", plain("`open")},
		{"a spaced opener", "** spaced** and * spaced*", plain("** spaced** and * spaced*")},
		{"a spaced closer", "**a **", plain("**a **")},
		{"arithmetic", "2 * 3 * 4 and a * b", plain("2 * 3 * 4 and a * b")},
		{"empty markers", "**** and ** and __", plain("**** and ** and __")},
		{"a link", "see [Malachi](https://github.com/schotek/malachi).", []Span{
			{Text: "see "}, {Text: "Malachi", Link: "https://github.com/schotek/malachi"}, {Text: "."},
		}},
		{"a link in bold", "**[a](http://x.org)**", []Span{{Text: "a", Bold: true, Link: "http://x.org"}}},
		{"an upper-case scheme", "[a](HTTPS://Example.org/P?q=1#f)", []Span{{Text: "a", Link: "HTTPS://Example.org/P?q=1#f"}}},
		{"link text is literal", "[**a** `b`](https://x.org)", []Span{{Text: "**a** `b`", Link: "https://x.org"}}},
		{"javascript stays text", "[click](javascript:alert(1))", plain("[click](javascript:alert(1))")},
		{"file stays text", "[x](file:///etc/passwd)", plain("[x](file:///etc/passwd)")},
		{"mailto stays text", "[x](mailto:a@b.cz)", plain("[x](mailto:a@b.cz)")},
		{"data stays text", "[x](data:text/html,<b>hi</b>)", plain("[x](data:text/html,<b>hi</b>)")},
		{"scheme-relative stays text", "[x](//evil.org)", plain("[x](//evil.org)")},
		{"no host stays text", "[x](http://) [y](https:///path)", plain("[x](http://) [y](https:///path)")},
		// Not links; the bare URL in them is, up to the space or quote.
		{"a space in the URL", "[x](https://exa mple.org)", []Span{{Text: "[x]("}, {Text: "https://exa", Link: "https://exa"}, {Text: " mple.org)"}}},
		{"a quote in the URL", `[x](https://x.org/"onmouseover=)`, []Span{{Text: "[x]("}, {Text: "https://x.org/", Link: "https://x.org/"}, {Text: `"onmouseover=)`}}},
		{"no link text", "[](https://x.org)", []Span{{Text: "[]("}, {Text: "https://x.org", Link: "https://x.org"}, {Text: ")"}}},
		{"link text over lines", "[a\nb](https://x.org)", []Span{{Text: "[a\nb]("}, {Text: "https://x.org", Link: "https://x.org"}, {Text: ")"}}},
		{"an unclosed link", "[text](https://x.org and [more]", []Span{{Text: "[text]("}, {Text: "https://x.org", Link: "https://x.org"}, {Text: " and [more]"}}},
		{"brackets without a link", "[1] and [a] (b)", plain("[1] and [a] (b)")},
		{"bare URLs", "see https://example.org/a_b_(c), and http://x.cz.", []Span{
			{Text: "see "}, {Text: "https://example.org/a_b_(c)", Link: "https://example.org/a_b_(c)"},
			{Text: ", and "}, {Text: "http://x.cz", Link: "http://x.cz"}, {Text: "."},
		}},
		{"a bare URL in parentheses", "(see https://x.org/y)", []Span{{Text: "(see "}, {Text: "https://x.org/y", Link: "https://x.org/y"}, {Text: ")"}}},
		{"a bare URL ends at a bracket or quote", `"https://x.org/a"<`, []Span{{Text: `"`}, {Text: "https://x.org/a", Link: "https://x.org/a"}, {Text: `"<`}}},
		{"a bare URL in bold", "**https://x.org**", []Span{{Text: "https://x.org", Bold: true, Link: "https://x.org"}}},
		{"no bare URL inside a word", "xhttps://x.org and 1http://y.org", plain("xhttps://x.org and 1http://y.org")},
		{"a scheme alone", "https:// and http://.", plain("https:// and http://.")},
		{"other schemes are text", "javascript:alert(1) ftp://x.org www.x.org", plain("javascript:alert(1) ftp://x.org www.x.org")},
		{"no HTML", "<b>bold</b> <script>alert(1)</script> &amp; <a href=\"https://x.org\">x</a>", []Span{
			{Text: "<b>bold</b> <script>alert(1)</script> &amp; <a href=\""}, {Text: "https://x.org", Link: "https://x.org"}, {Text: "\">x</a>"},
		}},
		{"control characters", "a\x00b\x1bc\x7fd" + string(rune(0x85)) + "e\tf", plain("abcde\tf")},
		{"invalid UTF-8", "a\xffb", plain("a" + string(utf8.RuneError) + "b")},
		{"Czech", "Příliš **žluťoučký** kůň", []Span{{Text: "Příliš "}, {Text: "žluťoučký", Bold: true}, {Text: " kůň"}}},
		{"underscores at Czech word edges", "_čau_ a x_č_y", []Span{{Text: "čau", Italic: true}, {Text: " a x_č_y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Markdown(tt.in)
			want := []Block{{Kind: BlockParagraph, Spans: tt.want}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Markdown(%q) =\n%+v\nwant\n%+v", tt.in, got, want)
			}
		})
	}
}

func TestMarkdownLinksAreWebURLs(t *testing.T) {
	// Whatever the input, a span's link is an http or https URL.
	inputs := []string{
		"[a](javascript:x) [b](https://ok.org) https://ok2.org/x) javascript://x.org",
		"[x](https://a.org\\@evil.org) http://a.org\\b [y](http://a.org`b)",
		"[x](https://" + string(rune(0x2028)) + "evil.org) https://x.org" + string(rune(0x00A0)) + "tail",
	}
	for _, in := range inputs {
		for _, b := range Markdown(in) {
			for _, s := range b.Spans {
				if s.Link != "" && !isWebURL(s.Link) {
					t.Errorf("Markdown(%q): link %q", in, s.Link)
				}
				if strings.ContainsAny(s.Link, " \\`\"<>") {
					t.Errorf("Markdown(%q): link %q", in, s.Link)
				}
			}
		}
	}
}

func TestMarkdownLinkTargetWithParens(t *testing.T) {
	// A target with a "(" of its own is no link target; the bare URL in it
	// is found instead, with its balanced parentheses.
	got := Markdown("[x](http://h.org/Foo_(bar)) end")
	want := []Span{{Text: "[x]("}, {Text: "http://h.org/Foo_(bar)", Link: "http://h.org/Foo_(bar)"}, {Text: ") end"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Spans, want) {
		t.Errorf("Markdown = %#v, want spans %#v", got, want)
	}
	// Without parentheses the target is a link as before.
	got = Markdown("[x](http://h.org/a) end")
	want = []Span{{Text: "x", Link: "http://h.org/a"}, {Text: " end"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Spans, want) {
		t.Errorf("Markdown = %#v, want spans %#v", got, want)
	}
}

func TestMarkdownIsLinear(t *testing.T) {
	const mb = 1 << 20
	nested := strings.Builder{}
	for i := 0; nested.Len() < mb; i++ {
		nested.WriteString(strings.Repeat(" ", i%400) + "- x\n")
	}
	inputs := map[string]string{
		"asterisks":         strings.Repeat("*", mb),
		"asterisk pairs":    strings.Repeat("**a", mb/3),
		"stars and spaces":  strings.Repeat("* ", mb/2),
		"underscores":       strings.Repeat("_a", mb/2),
		"backticks":         strings.Repeat("`", mb),
		"code spans":        strings.Repeat("`a`", mb/3),
		"open brackets":     strings.Repeat("[", mb),
		"bracket pairs":     strings.Repeat("[]", mb/2),
		"link openers":      strings.Repeat("[a](", mb/4),
		"bad links":         strings.Repeat("[a", mb/4) + "](javascript:" + strings.Repeat("x", mb/2) + ")",
		"one link, many [":  strings.Repeat("[", mb/2) + "a](https://x.org)",
		"schemes":           strings.Repeat("http://", mb/7),
		"schemes and dots":  strings.Repeat("http://.", mb/8),
		"a long URL":        "https://x.org/" + strings.Repeat("a", mb),
		"hashes":            strings.Repeat("#", mb),
		"digits":            strings.Repeat("1", mb) + ". x",
		"nested lists":      nested.String(),
		"fences":            strings.Repeat("```\n", mb/4),
		"lines":             strings.Repeat("a\n", mb/2),
		"mixed":             strings.Repeat("**_`[*h](", mb/9),
		"control bytes":     strings.Repeat("\x00\r", mb/2),
		"invalid UTF-8":     strings.Repeat("\xff", mb),
		"bold over a block": "**" + strings.Repeat("a *b* _c_ `d` ", mb/14) + "**",
		// Many link openers sharing one ")": each target would hold the
		// rest of the text.
		"openers, one paren":  strings.Repeat("[a](http://", mb/11) + ")",
		"openers, one paren2": strings.Repeat("[a](https://x.org/", mb/18) + ")",
		"openers, text paren": strings.Repeat("[a](http://x.org/b ", mb/19) + ")",
		"openers, no scheme":  strings.Repeat("[a](x", mb/5) + ")",
	}
	for name, in := range inputs {
		start := time.Now()
		blocks := Markdown(in)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(in), d)
		}
		// Nothing is lost but markers: the spans hold at most the input.
		n := 0
		for _, b := range blocks {
			for _, s := range b.Spans {
				n += len(s.Text)
			}
		}
		if n > 3*len(in) {
			t.Errorf("%s: %d bytes of spans from %d bytes", name, n, len(in))
		}
	}
}
