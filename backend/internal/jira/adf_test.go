// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// richComment is compose HTML with everything a comment keeps: paragraphs
// the way the editor writes them (a div per line), marks, a line break,
// links of both kinds, nested lists, a quote nesting another and holding a
// heading, a heading, a code block, a rule, and text that looks like wiki
// markup.
const richComment = `<div>Hello <b>world</b> and <i>more</i><br>second line</div><div><br></div>` +
	`<p>A <a href="https://example.org/a|b">link</a> and <a href="javascript:alert(1)">bad</a></p>` +
	`<ul><li>one</li><li>two<ul><li>nested <code>x</code></li></ul></li></ul><ol><li>first</li></ol>` +
	`<blockquote type="cite"><p>quoted</p><blockquote><p>deeper</p></blockquote><h2>head in quote</h2></blockquote>` +
	`<h2>Title</h2><pre>code {line}
  two</pre><hr><p>h1. not a heading {html}[~admin] *x* e-mail C++ a\b &amp;copy; ??cite?? !img.png!</p>`

func buildADF(t *testing.T, src string) string {
	t.Helper()
	b, err := BuildComment(src, "", api.JiraCloud)
	if err != nil {
		t.Fatalf("BuildComment: %v", err)
	}
	if b.Wiki != "" {
		t.Fatalf("a cloud comment has wiki markup %q", b.Wiki)
	}
	if err := jiratest.ValidateADF(b.ADF); err != nil {
		t.Fatalf("invalid ADF: %v\n%s", err, b.ADF)
	}
	return string(b.ADF)
}

func TestADFDocument(t *testing.T) {
	got := buildADF(t, richComment)
	want := `{"version":1,"type":"doc","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Hello "},{"type":"text","text":"world","marks":[{"type":"strong"}]},{"type":"text","text":" and "},{"type":"text","text":"more","marks":[{"type":"em"}]},{"type":"hardBreak"},{"type":"text","text":"second line"}]},` +
		`{"type":"paragraph","content":[{"type":"text","text":"A "},{"type":"text","text":"link","marks":[{"type":"link","attrs":{"href":"https://example.org/a%7Cb"}}]},{"type":"text","text":" and bad"}]},` +
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"nested "},{"type":"text","text":"x","marks":[{"type":"code"}]}]}]}]}]}]},` +
		`{"type":"orderedList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]}]},` +
		`{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"quoted"}]},{"type":"paragraph","content":[{"type":"text","text":"deeper"}]},{"type":"paragraph","content":[{"type":"text","text":"head in quote","marks":[{"type":"strong"}]}]}]},` +
		`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},` +
		`{"type":"codeBlock","content":[{"type":"text","text":"code {line}\n  two"}]},` +
		`{"type":"rule"},` +
		`{"type":"paragraph","content":[{"type":"text","text":"h1. not a heading {html}[~admin] *x* e-mail C++ a\\b &copy; ??cite?? !img.png!"}]}]}`
	if got != want {
		t.Fatalf("ADF:\n got %s\nwant %s", got, want)
	}
}

// Where the schema does not allow a node, its content goes elsewhere: a
// heading or a rule in a list item or a quote, a quote in a list item, a
// list item that starts with a list, marks that code does not combine
// with, and inline content around blocks.
func TestADFPlacement(t *testing.T) {
	for name, src := range map[string]string{
		"heading and rule in an item":  `<ul><li><h3>Head</h3><hr>text</li></ul>`,
		"quote in an item":             `<ol><li><blockquote><p>q</p><ul><li>in</li></ul></blockquote></li></ol>`,
		"item starting with a list":    `<ul><li><ul><li>inner</li></ul></li></ul>`,
		"list in a quote":              `<blockquote><ul><li>a<pre>b</pre></li></ul><hr><h1>h</h1></blockquote>`,
		"code with marks":              `<p><b><i><code>c</code></i></b> <a href="https://acme.example/"><b><code>l</code></b></a></p>`,
		"text around blocks":           `x<div>y</div>z<ul><li>i</li></ul>w`,
		"stray content in a list":      `<ul>text<b>bold</b><li>item</li><p>para</p></ul>`,
		"empty items and paragraphs":   `<ul><li></li><li> </li><li>ok</li></ul><p></p><p>&nbsp;</p>`,
		"table":                        `<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td><b>2</b></td></tr></table>`,
		"heading with breaks and list": `<h1>one<br>two<ul><li>three</li></ul></h1>`,
		"link around blocks":           `<a href="https://acme.example/"><p>one</p><p>two</p></a>`,
	} {
		t.Run(name, func(t *testing.T) { buildADF(t, src) })
	}
	got := buildADF(t, `<ul><li><ul><li>inner</li></ul></li></ul>`)
	if !strings.Contains(got, `{"type":"listItem","content":[{"type":"paragraph"},{"type":"bulletList"`) {
		t.Errorf("an item starting with a list gets no empty paragraph first: %s", got)
	}
	got = buildADF(t, `<p><b><i><code>c</code></i></b></p>`)
	if !strings.Contains(got, `{"type":"text","text":"c","marks":[{"type":"code"}]}`) {
		t.Errorf("code keeps other marks: %s", got)
	}
	got = buildADF(t, `<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`)
	if !strings.Contains(got, `"text":"A | B"`) || !strings.Contains(got, `"text":"1 | 2"`) {
		t.Errorf("table rows: %s", got)
	}
}

// adfLinks lists the href of every link mark of a document.
func adfLinks(t *testing.T, raw string) []string {
	t.Helper()
	var hrefs []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "link" {
				attrs, _ := x["attrs"].(map[string]any)
				href, _ := attrs["href"].(string)
				hrefs = append(hrefs, href)
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	walk(doc)
	return hrefs
}

// The hostile fixture: only http(s) and mailto links become links, nothing
// the HTML carried beyond text survives, and the text itself — wiki
// markup, bidirectional overrides, zero-width spaces, emoji — is data.
func TestADFHostile(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "jira", "comment", "hostile.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := buildADF(t, string(src))
	links := adfLinks(t, got)
	if strings.Join(links, " ") != "https://acme.example/wiki%7Cx mailto:jana@acme.example" {
		t.Errorf("links = %q", links)
	}
	for _, bad := range []string{"typed", "video text", "svg text", "evil.example/\"", "cid:x", `"type":"media`, "steal()", "iframe"} {
		if strings.Contains(got, bad) {
			t.Errorf("the comment carries %q:\n%s", bad, got)
		}
	}
	for _, keep := range []string{"{html}", "[~admin]", "!https://tracker.example/pixel.png!", "h1. fake heading",
		"{noformat}", "Dobrý den,", string(rune(0x1F600)), string(rune(0x202E)) + "evil", "zero" + string(rune(0x200B)) + "width"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the text %q is missing:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "alert(1)</") || strings.Contains(got, "injected</") {
		t.Errorf("markup survived as markup:\n%s", got)
	}
}

// linkTarget keeps only what a comment may link to.
func TestCommentLinkTarget(t *testing.T) {
	for raw, want := range map[string]string{
		"https://acme.example/a?b=c#d":     "https://acme.example/a?b=c#d",
		"HTTP://acme.example/":             "http://acme.example/",
		" https://acme.example/x ":         "https://acme.example/x",
		"mailto:jana@acme.example":         "mailto:jana@acme.example",
		"javascript:alert(1)":              "",
		"JaVaScRiPt:alert(1)":              "",
		"java\tscript:alert(1)":            "",
		"data:text/html,<script>":          "",
		"vbscript:msgbox":                  "",
		"cid:pic@acme.example":             "",
		"file:///etc/passwd":               "",
		"/relative":                        "",
		"//evil.example/":                  "",
		"https://user:pw@evil.example/":    "",
		"https:evil.example":               "",
		"https://acme.example/a b":         "",
		"https://acme.example/" + zwsp:     "",
		"mailto:":                          "",
		"https://acme.example/" + longPath: "",
		"":                                 "",
	} {
		if got := linkTarget(raw); got != want {
			t.Errorf("linkTarget(%q) = %q, want %q", raw, got, want)
		}
	}
}

var (
	zwsp     = string(rune(0x200B))
	longPath = strings.Repeat("a", maxLinkURLBytes)
)

// Pathological documents: deep nesting of every kind, a nesting the HTML
// parser refuses, too many nodes, too much text, nothing but whitespace.
func TestCommentPathological(t *testing.T) {
	deep := func(open, text, closing string, n int) string {
		return strings.Repeat(open, n) + text + strings.Repeat(closing, n)
	}
	for _, d := range []api.JiraDeployment{api.JiraCloud, api.JiraDataCenter} {
		t.Run(string(d), func(t *testing.T) {
			build := func(src string) (CommentBody, error) {
				b, err := BuildComment(src, "", d)
				if err == nil && d == api.JiraCloud {
					if verr := jiratest.ValidateADF(b.ADF); verr != nil {
						t.Fatalf("invalid ADF: %v", verr)
					}
				}
				return b, err
			}
			text := func(b CommentBody) string { return string(b.ADF) + b.Wiki }

			// Deeper than the walk follows: the text stays.
			b, err := build(deep("<div><span>", "deep <b>text</b>", "</span></div>", 200))
			if err != nil || !strings.Contains(text(b), "deep") || !strings.Contains(text(b), "text") {
				t.Fatalf("deep divs: %v %s", err, text(b))
			}
			// Deeper than the HTML parser goes (512 open elements): the
			// text stays too.
			b, err = build(deep("<div>", "deeper", "</div>", 1000))
			if err != nil || !strings.Contains(text(b), "deeper") {
				t.Fatalf("1000 divs: %v %s", err, text(b))
			}
			b, err = build(deep("<b><i>", "marked", "", 1000))
			if err != nil || !strings.Contains(text(b), "marked") {
				t.Fatalf("1000 marks: %v %s", err, text(b))
			}
			// Lists: at most maxCommentListDepth levels.
			b, err = build(deep("<ul><li>x", "", "</li></ul>", 100))
			if err != nil {
				t.Fatal(err)
			}
			if d == api.JiraCloud {
				if n := strings.Count(text(b), `"type":"bulletList"`); n != maxCommentListDepth {
					t.Errorf("%d list levels", n)
				}
			} else if strings.Contains(b.Wiki, strings.Repeat("*", maxCommentListDepth+1)) || !strings.Contains(b.Wiki, strings.Repeat("*", maxCommentListDepth)+" x") {
				t.Errorf("wiki list levels:\n%s", b.Wiki)
			}
			if _, err := build(deep("<ol><li>x", "", "</li></ol>", 1000)); err != nil && errCodeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("1000 lists: %v", err)
			}
			// Quotes do not nest.
			b, err = build(deep("<blockquote>q", "", "</blockquote>", 100))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(b.ADF), `"type":"blockquote"`) > 1 || strings.Count(b.Wiki, "{quote}") > 2 {
				t.Errorf("nested quotes: %s", text(b))
			}
			// Too many nodes, too much text.
			if _, err := build(strings.Repeat("<br>", maxCommentNodes+10) + "x"); errCodeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("too many nodes: %v", err)
			}
			if _, err := build("<p>" + strings.Repeat("a", 1<<20) + "</p>"); errCodeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("1 MiB of text: %v", err)
			}
			if _, err := build("<pre>" + strings.Repeat("a\n", 1<<19) + "</pre>"); errCodeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("1 MiB of code: %v", err)
			}
			// Nothing left after the conversion.
			for _, src := range []string{"", "   ", "<p> </p>", "<p>&nbsp;</p><br><hr>", "<div><br></div><div><br></div>",
				`<img src="cid:x@y">`, "<script>alert(1)</script>", "<p>\t\n\r</p>", "<ul><li> </li></ul>",
				"<pre>\n \n</pre>", "<blockquote> </blockquote>", "<!-- only a comment -->"} {
				if _, err := build(src); errCodeOf(err) != api.CodeInvalidArgument {
					t.Errorf("empty %q: %v", src, err)
				}
				if err := CheckComment(src, d); errCodeOf(err) != api.CodeInvalidArgument {
					t.Errorf("CheckComment empty %q: %v", src, err)
				}
			}
			if _, err := BuildComment("", " \n\n \t", d); errCodeOf(err) != api.CodeInvalidArgument {
				t.Errorf("empty text: %v", err)
			}
			// Invalid UTF-8 and control characters are dropped, the rest
			// of the text kept.
			b, err = build("<p>a\xffb\x01c\x7fd</p>")
			if err != nil || !strings.Contains(text(b), "abcd") {
				t.Errorf("invalid bytes: %v %s", err, text(b))
			}
		})
	}
}

func errCodeOf(err error) api.ErrorCode {
	if err == nil {
		return 0
	}
	return ToAPIError(err).Code
}

// The limit is the site's, counted in the format the site gets: the wiki
// markup, or the ADF document's JSON.
func TestCommentLimit(t *testing.T) {
	for _, d := range []api.JiraDeployment{api.JiraCloud, api.JiraDataCenter} {
		b, err := BuildComment("<p>a</p>", "", d)
		if err != nil {
			t.Fatal(err)
		}
		overhead := len(b.ADF) + len(b.Wiki) - 1
		fits := "<p>" + strings.Repeat("a", MaxCommentChars-overhead) + "</p>"
		if err := CheckComment(fits, d); err != nil {
			t.Errorf("%s: at the limit: %v", d, err)
		}
		over := "<p>" + strings.Repeat("a", MaxCommentChars-overhead+1) + "</p>"
		if err := CheckComment(over, d); errCodeOf(err) != api.CodeInvalidArgument {
			t.Errorf("%s: over the limit: %v", d, err)
		}
		// Characters, not bytes.
		wide := "<p>" + strings.Repeat("č", MaxCommentChars-overhead) + "</p>"
		if err := CheckComment(wide, d); err != nil {
			t.Errorf("%s: two-byte characters at the limit: %v", d, err)
		}
	}
	if _, err := BuildComment("<p>a</p>", "", "server"); errCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("unknown deployment: %v", err)
	}
}

func TestTextToHTML(t *testing.T) {
	got := TextToHTML("a & <b>\r\n\r\nline1\nline2\n\n \n\nlast\r")
	if want := "<p>a &amp; &lt;b&gt;</p><p>line1<br>line2</p><p>last</p>"; got != want {
		t.Errorf("TextToHTML = %q, want %q", got, want)
	}
	if got := TextToHTML(" \n\n"); got != "" {
		t.Errorf("blank text = %q", got)
	}
	b, err := BuildComment("", "Dobrý den,\n*to* je [link]", api.JiraDataCenter)
	if err != nil || b.Wiki != `Dobrý den, \\ \*to\* je \[link\]` {
		t.Errorf("plain text comment: %v %q", err, b.Wiki)
	}
}
