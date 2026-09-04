// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// onlyText reports whether re-tokenizing s yields nothing but text: no
// start tag, end tag, comment or doctype survives in the output. It is the
// "never emits a tag" guarantee of HTMLToText; it holds whenever the input
// contained no entity (an entity-encoded "&lt;b&gt;" legitimately decodes
// to the text "<b>"). An unfinished "<A" at the end of the input is text by
// the tokenizer's own rules and stays text when tokenized again.
func onlyText(s string) bool {
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return true
		case html.TextToken:
		default:
			return false
		}
	}
}

func TestHTMLToText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"entities", "a &amp; b &lt;c&gt; &copy; &#8212; &nbsp;d", "a & b <c> © — d"},
		{"collapse", "  a \n\t b   c  ", "a b c"},
		{"inline tags", "<b>bold</b> and <i>italic</i><span>!</span>", "bold and italic!"},
		{"paragraphs", "<p>one</p><p>two</p>", "one\n\ntwo"},
		{"div lines", "<div>a</div><div>b</div><div><br></div><div>c</div>", "a\nb\nc"},
		{"br", "a<br>b<br/>c<br />d", "a\nb\nc\nd"},
		{"list", "<ul><li>x</li><li>y</li></ul>after", "x\ny\n\nafter"},
		{"table", "<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>", "a\tb\nc"},
		{"headings", "<h1>Title</h1>text", "Title\n\ntext"},
		{"script", "before<script>alert('<b>x</b>')</script>after", "beforeafter"},
		{"style", "<style>p{color:red}</style>text", "text"},
		{"head", "<html><head><title>T</title><meta charset=utf-8></head><body>body</body></html>", "body"},
		{"unclosed head", "<html><head><title>T</title><p>body", "body"},
		{"head without body tag", "<head><style>x</style></head>text", "text"},
		{"noscript", "a<noscript><p>enable js</p></noscript>b", "ab"},
		{"template", "a<template><p>tpl</p></template>b", "ab"},
		{"self-closing template", "a<template/>b", "ab"},
		{"self-closing script", "a<script/>x<b>y</b></script>b", "ab"},
		{"xmp", "a<xmp><b>raw</b></xmp>b", "ab"},
		{"textarea", "a<textarea><b>x</b></textarea>b", "ab"},
		{"iframe", "a<iframe><b>x</b></iframe>b", "ab"},
		{"plaintext", "a<plaintext><b>rest", "a"},
		{"pre", "<pre>  keep\n   this\tspacing  \n</pre>x", "keep\n   this\tspacing\n\nx"},
		{"pre leading and trailing", "<pre>  a  </pre>", "a"},
		{"comment", "a<!-- <b>hidden</b> -->b", "ab"},
		{"doctype", "<!DOCTYPE html>x", "x"},
		// The tokenizer (like browsers) reads "scr<script" as one tag name;
		// what follows is text, but no '<' survives.
		{"broken tag", "a<scr<script>ipt>alert(1)</script>b", "aipt>alert(1)b"},
		{"unclosed tag", "text <b unclosed", "text"},
		{"lone lt", "a < b <3 <é", "a < b <3 <é"},
		{"nul", "a\x00b", "a b"},
		{"control", "a\x01b\x7fc\u0085d", "a b c d"},
		{"nul cannot fuse a tag", "<\x00A>x", "< A>x"},
		{"c1 cannot fuse a tag", "<\u0085A>x", "< A>x"},
		{"cr", "a\r\nb", "a b"},
		{"pre cr", "<pre>a\r\nb</pre>", "a\nb"},
		{"leading blocks", "<br><p></p><div>x</div>", "x"},
		{"trailing blocks", "x<br><p></p>", "x"},
		{"nested blocks", "<div><div><p>a</p></div></div><div>b</div>", "a\n\nb"},
		{"blockquote", "<p>Hi</p><blockquote><p>quoted</p></blockquote>", "Hi\n\nquoted"},
		{"empty", "", ""},
		{"only tags", "<div><span></span></div>", ""},
		{"invalid utf8", "a\xffb", "a�b"},
		{"link text", `<a href="https://example.org/">example</a>`, "example"},
		{"image alt ignored", `x<img src="cid:1" alt="logo">y`, "xy"},
		{"body attributes", `<body onload="evil()">safe</body>`, "safe"},
		{"case", "<P>A</P><DIV>B</DIV><SCRIPT>x</SCRIPT>", "A\n\nB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HTMLToText(c.in, 0); got != c.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHTMLToTextCap(t *testing.T) {
	in := strings.Repeat("ž", 100) // 200 bytes
	got := HTMLToText(in, 11)
	if got != strings.Repeat("ž", 5) || !utf8.ValidString(got) {
		t.Errorf("capped = %q", got)
	}
	long := "<p>" + strings.Repeat("word ", 100000) + "</p>"
	if got := HTMLToText(long, 1024); len(got) > 1024 {
		t.Errorf("cap not applied: %d bytes", len(got))
	}
	if got := HTMLToText(long, 0); len(got) != 5*100000-1 {
		t.Errorf("uncapped length = %d", len(got))
	}
}

func FuzzHTMLToText(f *testing.F) {
	for _, s := range []string{
		"<p>hi</p>", "<script>x</script>", "a &lt;b&gt; c", "<pre>\tx\n</pre>",
		"<scr<script>ipt>", "<xmp><b>", "<head><title>t</title>x", "<plaintext>rest",
		"<script/>x</script>", "\x00\xff<", "<table><tr><td>a<td>b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := HTMLToText(in, 4096)
		if len(got) > 4096 || !utf8.ValidString(got) {
			t.Errorf("bad length or encoding: %d", len(got))
		}
		for _, r := range got {
			if r != '\n' && r != '\t' && unicode.IsControl(r) {
				t.Errorf("control character %U", r)
				break
			}
		}
		if strings.Contains(strings.ToLower(got), "<script") && !strings.ContainsRune(in, '&') {
			t.Errorf("markup leaked: %q -> %q", in, got)
		}
		if !strings.ContainsRune(in, '&') && !onlyText(got) {
			t.Errorf("a tag survived without entities: %q -> %q", in, got)
		}
	})
}
