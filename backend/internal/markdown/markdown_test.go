// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package markdown

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestRender(t *testing.T) {
	cases := []struct {
		name, in string
		ok       bool
		want     []string // substrings of the output
		not      []string
	}{
		{name: "plain", in: "Dobrý den,\nposílám podklady.\n\nS pozdravem\nJan"},
		{name: "indented only", in: "Ahoj\n\n    odsazený řádek\n"},
		{name: "bare address", in: "viz https://example.com/a_b_c"},
		{name: "snake case", in: "soubor my_file_name.txt"},
		{name: "heading", in: "# Nadpis\ntext", ok: true, want: []string{"<h1>Nadpis</h1>"}},
		{name: "list", in: "- jedna\n- dvě", ok: true, want: []string{"<ul>", "<li>jedna</li>"}},
		{name: "ordered", in: "1. a\n2. b", ok: true, want: []string{"<ol>"}},
		{name: "emphasis", in: "je to **důležité** a *taky*", ok: true,
			want: []string{"<strong>důležité</strong>", "<em>taky</em>"}},
		{name: "hard wraps", in: "**a**\nb", ok: true, want: []string{"<br>"}},
		{name: "strike", in: "~~pryč~~", ok: true, want: []string{"<del>pryč</del>"}},
		{name: "code span", in: "spusť `make po`", ok: true, want: []string{"<code style=", ">make po</code>"}},
		{name: "fence", in: "```go\nx := <b>\n```", ok: true,
			want: []string{"<pre style=", "x := &lt;b&gt;"}, not: []string{"language-"}},
		{name: "quote", in: "> citace", ok: true, want: []string{"<blockquote style="}},
		{name: "table", in: "| a | b |\n|---|:-:|\n| 1 | 2 |", ok: true,
			want: []string{"<table style=", "<th style=", "text-align:center"}},
		{name: "link", in: "[web](https://example.com)", ok: true, want: []string{`<a href="https://example.com">web</a>`}},
		{name: "rule", in: "a\n\n---\n\nb", ok: true, want: []string{"<hr>"}},
		// Hostile text: nothing becomes markup the text did not ask for.
		{name: "raw html", in: "**x** <script>alert(1)</script> <img src=x onerror=y>", ok: true,
			want: []string{"&lt;script&gt;", "&lt;img"}, not: []string{"<script", "<img"}},
		{name: "html block", in: "# a\n\n<div onclick=x>\nhi\n</div>", ok: true,
			want: []string{"&lt;div onclick=x&gt;"}, not: []string{"<div"}},
		{name: "javascript link", in: "[x](javascript:alert(1))", ok: true, not: []string{"javascript:"}},
		{name: "image", in: "![logo](https://example.com/l.png)", ok: true,
			want: []string{`<a href="https://example.com/l.png">logo</a>`}, not: []string{"<img"}},
		{name: "image no alt", in: "![](https://example.com/l.png)", ok: true,
			want: []string{`>https://example.com/l.png</a>`}},
		{name: "javascript image", in: "![x](javascript:alert(1))", ok: true, not: []string{"<a", "<img"}},
		{name: "deep quotes", in: strings.Repeat(">", 10000) + " x"},
		{name: "deep lists", in: deepList(200)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := Render(c.in)
			if ok != c.ok {
				t.Fatalf("Render(%q) ok = %v, want %v (%q)", c.in, ok, c.ok, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output %q lacks %q", out, w)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("output %q has %q", out, n)
				}
			}
		})
	}
}

func deepList(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(strings.Repeat("  ", i) + "- x\n")
	}
	return b.String()
}

// The rendered styles and elements survive the compose sanitiser, which
// draft.markdown runs on every result.
func TestRenderSurvivesSanitiser(t *testing.T) {
	in := "# H\n\n**b** *i* ~~s~~ `c`\n\n> q\n\n```\npre\n```\n\n| a |\n|---|\n| 1 |\n\n- l\n\n[x](https://example.com)"
	html, ok := Render(in)
	if !ok {
		t.Fatal("not markdown")
	}
	out, err := sanitize.Sanitize(sanitize.Input{HTML: html, Mode: sanitize.ModeCompose, Policy: api.RemoteBlock})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"<h1>", "<strong>", "<em>", "<del>", "<code style=", "<blockquote style=",
		"<pre style=", "border-collapse", "<li>", `href="https://example.com"`} {
		if !strings.Contains(out.HTML, w) {
			t.Errorf("sanitised %q lacks %q", out.HTML, w)
		}
	}
}

func FuzzRender(f *testing.F) {
	for _, s := range []string{"# a", "- [x](y)", "> > `c`", "| a |\n|-|\n", "<b>**x**</b>", "![a](javascript:x)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, _ := Render(s)
		if strings.Contains(strings.ToLower(out), "<script") {
			t.Fatalf("script in %q", out)
		}
	})
}
