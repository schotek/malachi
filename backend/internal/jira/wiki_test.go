// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func buildWiki(t *testing.T, src string) string {
	t.Helper()
	b, err := BuildComment(src, "", api.JiraDataCenter)
	if err != nil {
		t.Fatalf("BuildComment: %v", err)
	}
	if len(b.ADF) != 0 {
		t.Fatalf("a datacenter comment has ADF %s", b.ADF)
	}
	return b.Wiki
}

func TestWikiMarkup(t *testing.T) {
	got := buildWiki(t, richComment)
	want := `Hello *world* and _more_ \\ second line

A [link|https://example.org/a%7Cb] and bad

* one
* two
** nested {{x}}

# first

{quote}
quoted

deeper

h2. head in quote
{quote}

h2. Title

{noformat}
code {line}
  two
{noformat}

----

&#104;1. not a heading \{html\}\[\~admin\] \*x\* e-mail C\+\+ a&#92;b &amp;copy; \?\?cite\?\? \!img.png\!`
	if got != want {
		t.Fatalf("wiki:\n got %q\nwant %q", got, want)
	}
}

func TestWikiStructures(t *testing.T) {
	for src, want := range map[string]string{
		`<ol><li>a<ul><li>b</li></ul></li><li>c</li></ol>`:                    "# a\n#* b\n# c",
		`<ul><li><ul><li>x</li></ul></li></ul>`:                               "* &nbsp;\n** x",
		"<ul><li>see<pre>a\n{b}</pre></li></ul>":                              `* see \\ a \\ \{b\}`,
		`<ul><li><blockquote><p>q</p></blockquote>r</li></ul>`:                `* q \\ r`,
		`<ul><li>one<br>two</li></ul>`:                                        `* one \\ two`,
		`<h3>A<br>B</h3>`:                                                     "h3. A B",
		`<p><a href="https://acme.example/x">*t*</a></p>`:                     `[\*t\*|https://acme.example/x]`,
		`<p><a href="https://acme.example/"><b>bold</b></a></p>`:              `[*bold*|https://acme.example/]`,
		`<p><a href="mailto:jana@acme.example">Jana</a></p>`:                  `[Jana|mailto:jana@acme.example]`,
		`<p><a href="javascript:alert(1)">[x|y]</a></p>`:                      `\[x\|y\]`,
		`<p><b> spaced </b>x</p>`:                                             `*spaced* x`,
		`<p><code>{{a}}</code> <s>gone</s> <u>under</u></p>`:                  `{{\{\{a\}\}}} -gone- +under+`,
		`<p><b><i><u><s>all</s></u></i></b></p>`:                              `*_+-all-+_*`,
		`<blockquote><h1>t</h1><hr><pre>c</pre></blockquote>`:                 "{quote}\nh1. t\n\n----\n\n{noformat}\nc\n{noformat}\n{quote}",
		"<pre>x {NoFormat} y</pre>":                                           `x \{NoFormat\} y`,
		"<blockquote><pre>{quote}</pre></blockquote>":                         "{quote}\n\\{quote\\}\n{quote}",
		`<table><tr><td>a</td><td>b</td></tr></table>`:                        `a \| b`,
		`<p>bq. x</p><p>H6.y</p><p>h7. z</p><p>xh1. w</p>`:                    "&#98;q. x\n\n&#72;6.y\n\nh7. z\n\nxh1. w",
		`<p>&amp;amp; &amp;#x41; &amp;#65 &amp; x &amp;;</p>`:                 `&amp;amp; &amp;#x41; &amp;#65 &amp; x &amp;;`,
		`<p>a-b -c d- --e f+g +h i++</p>`:                                     `a-b \-c d\- \-\-e f+g \+h i\+\+`,
		`<p>why? really?? ?</p>`:                                              `why? really\?\? ?`,
		`<p>tab` + "\t" + `and   spaces</p>`:                                  `tab and spaces`,
		`<p>ends with a break<br></p><p><br>starts with one</p>`:              "ends with a break\n\nstarts with one",
		`<p>` + string(rune(0x00A0)) + `nbsp` + string(rune(0x00A0)) + `</p>`: string(rune(0x00A0)) + "nbsp" + string(rune(0x00A0)),
	} {
		if got := buildWiki(t, src); got != want {
			t.Errorf("%q:\n got %q\nwant %q", src, got, want)
		}
	}
}

// unescapedMarkup finds a character that starts a macro, a link, a
// mention, a picture or a table without a backslash in front of it.
var unescapedMarkup = regexp.MustCompile(`(^|[^\\])[{}\[\]|!]`)

// The hostile fixture: none of the text becomes markup.
func TestWikiHostile(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "jira", "comment", "hostile.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := buildWiki(t, string(src))
	for _, want := range []string{
		`Dobrý den, \{html\}*injected*\{html\} \{include:secret\}`,
		`\[\~admin\] \[Klikni\|javascript:alert(1)\] \!https://tracker.example/pixel.png\! \[#anchor\] \[\^report.pdf\]`,
		`js js2 data vb cid relative protocol-relative userinfo space [bar|https://acme.example/wiki%7Cx] [mail|mailto:jana@acme.example] tab`,
		"&#104;1. fake heading", "&#98;q. fake quote", `\* fake bullet`, `\# fake number`, `\|\|fake\|\|table\|\|`, `\-\-\-\-`,
		`\{color:red\}red\{color\} \{panel\}p\{panel\} \{code\}x\{code\} \{noformat\}y\{noformat\}`,
		`\*bold\* \_em\_ \-strike\- \+under\+ \^sup\^ \~sub\~ \?\?cite\?\? \{\{mono\}\} &#92;&#92; break &#92; backslash &amp;copy; &amp;#65; e-mail C\+\+ x-y`,
		"emoji " + string(rune(0x1F600)) + " rtl " + string(rune(0x202E)) + "evil" + string(rune(0x202C)) + " zero" + string(rune(0x200B)) + "width",
		`\{noformat\} \\ \{html\}\{html\} \\ \[link\|javascript:alert(1)\]`,
		"{quote}\n\\{quote\\}escape\\{quote\\}\n{quote}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// Remove the markup of this file; what is left is text, and none of
	// it may start markup.
	rest := got
	for _, ours := range []string{"[bar|https://acme.example/wiki%7Cx]", "[mail|mailto:jana@acme.example]", "{quote}"} {
		rest = strings.ReplaceAll(rest, ours, "")
	}
	for _, line := range strings.Split(rest, "\n") {
		if m := unescapedMarkup.FindString(line); m != "" {
			t.Errorf("unescaped %q in %q", m, line)
		}
		if wikiBlockStart.MatchString(line) || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "-") {
			t.Errorf("a line starts markup: %q", line)
		}
	}
	for _, bad := range []string{"typed", "video text", "svg text", "steal()", "{noformat}\n{html}", "\\\\\\"} {
		if strings.Contains(got, bad) {
			t.Errorf("the markup carries %q:\n%s", bad, got)
		}
	}
}

func TestWikiEscape(t *testing.T) {
	for in, want := range map[string]string{
		`{html}<b>x</b>{html}`: `\{html\}<b>x</b>\{html\}`,
		`[~jana]`:              `\[\~jana\]`,
		`[a|b]`:                `\[a\|b\]`,
		`!pic.png!`:            `\!pic.png\!`,
		`\`:                    `&#92;`,
		`\\`:                   `&#92;&#92;`,
		`\*`:                   `&#92;\*`,
		`&copy;`:               `&amp;copy;`,
		`&#169;&#xA9;&#x;&#;`:  `&amp;#169;&amp;#xA9;&amp;#x;&amp;#;`,
		`R&D`:                  `R&amp;D`,
		`AT&T;`:                `AT&amp;T;`,
		`x^2 ~y #z _w_`:        `x\^2 \~y #z \_w\_`,
		`#1 in #2`:             `\#1 in #2`,
		`well-known +1 -1 a-`:  `well-known \+1 \-1 a\-`,
		`čeština-Ünïcode+ő`:    `čeština-Ünïcode+ő`,
	} {
		if got := wikiEscape(in); got != want {
			t.Errorf("wikiEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
