// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testdata = "../../testdata/mime"

// corpusHTML is every HTML body the MIME corpus yields, as fuzz seeds and
// as the input of TestCorpusSanitised.
func corpusHTML(tb testing.TB) map[string]string {
	tb.Helper()
	entries, err := os.ReadDir(testdata)
	if err != nil {
		tb.Fatalf("read corpus: %v", err)
	}
	out := make(map[string]string)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(testdata, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
		if err != nil || !p.HasHTML {
			continue
		}
		out[e.Name()] = p.RawHTML
	}
	return out
}

var hostileSeeds = []string{
	`<script>alert(1)</script>`,
	`<a href="jav&#x09;ascript:alert(1)">x</a>`,
	`<img src="javascript:x" onerror="y()">`,
	`<style>@import url(https://x/s.css); p{background:url(https://x/a)}</style>`,
	`<div style="position:fixed;top:0;left:0">overlay</div>`,
	`<iframe src="https://x"></iframe><object data="x"></object><embed src="x">`,
	`<form action="https://x"><input name=p></form>`,
	`<svg onload="x()"><script>y()</script></svg><math><mi>m</mi></math>`,
	`<base href="https://x/"><meta http-equiv="refresh" content="0;url=https://x">`,
	`<img src="cid:known@x"><img src="cid:unknown@x"><img src="malachi-cid:acc/msg/9">`,
	`<a href="https://example.org/a">https://bank.example</a>`,
	`<p style="color:red;">text</p><table bgcolor="#fff"><tr><td background="https://x/b.png">c</td></tr></table>`,
	`<body bgcolor="#000" text="#fff" style="margin:0"><p>dark</p></body>`,
	`<img src="https://x/p.gif" width="1" height="1"><img src="https://x/q.gif" style="display:none">`,
	strings.Repeat("<div>", 250) + "deep" + strings.Repeat("</div>", 250),
	`<a href="https://x/1">1</a>` + strings.Repeat(`<b>`, 50) + "x" + strings.Repeat(`</b>`, 50),
	`<pre>\n  keep\n   spaces</pre><ul><li>a</li><li>b</li></ul>`,
	"<p>\x00null</p><a href=\"java\x00script:x\">n</a>",
}

var fuzzKnown = map[string]string{"known@x": "acc/msg/1.2"}

// checkClean is the invariant every output must satisfy: only allowed
// elements, no handlers, only expected URL schemes, no fetching CSS, a
// tag-free text form, and (under block) sanitising the output again changes
// nothing.
func checkClean(t testing.TB, in Input, out Output) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(out.HTML))
	if err != nil {
		t.Fatalf("output does not parse: %v", err)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data != "html" && n.Data != "head" && n.Data != "body" {
			if _, dropped := droppedElems[n.Data]; dropped || (!allowedElems[n.Data] && n.Data != "style") {
				t.Errorf("element <%s> in output: %q", n.Data, out.HTML)
			}
			if n.Data == "style" && (in.Mode == ModeCompose || n.Parent == nil || n.Parent.Data != "body" && n.Parent.Data != "head") {
				t.Errorf("<style> where it should not be: %q", out.HTML)
			}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				switch {
				case strings.HasPrefix(key, "on"):
					t.Errorf("handler %s= in output: %q", key, out.HTML)
				case key == "href":
					_, class := classifyURL(a.Val)
					if class != urlHTTP && class != urlHTTPS && class != urlMailto {
						t.Errorf("href %q in output", a.Val)
					}
				case key == "src":
					_, class := classifyURL(a.Val)
					switch class {
					case urlLocal:
						if in.Mode != ModeView {
							t.Errorf("malachi-cid: in compose output: %q", a.Val)
						}
					case urlCID:
						if in.Mode != ModeCompose {
							t.Errorf("cid: in view output: %q", a.Val)
						}
					case urlData:
						if in.Policy != api.RemoteAllow || in.RemoteImage == nil {
							t.Errorf("data: without an inlining hook: %q", a.Val)
						}
					case urlHTTPS:
						if in.Policy != api.RemoteAllow || in.RemoteImage != nil {
							t.Errorf("https: image kept under %s: %q", in.Policy, a.Val)
						}
					default:
						t.Errorf("src %q in output", a.Val)
					}
				case key == "style":
					lower := strings.ToLower(a.Val)
					for _, bad := range []string{"url(", "expression(", "\\", "position: fixed", "position: absolute"} {
						if strings.Contains(lower, bad) {
							t.Errorf("style %q in output", a.Val)
						}
					}
				case key == "srcset", key == "background", key == "target", key == "poster":
					t.Errorf("attribute %s in output: %q", key, out.HTML)
				}
			}
			if n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					lower := strings.ToLower(c.Data)
					for _, bad := range []string{"url(", "@import", "@font-face", "<", "expression("} {
						if strings.Contains(lower, bad) {
							t.Errorf("stylesheet contains %q: %q", bad, c.Data)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	// The text form is plain text: a literal "<b>" that the mail spelled as
	// "&lt;b&gt;" is legitimate content, so "no tags" is not the invariant;
	// "nothing a plain-text renderer would choke on" is.
	if !utf8.ValidString(out.Text) {
		t.Errorf("text form is not valid UTF-8: %q", out.Text)
	}
	for _, r := range out.Text {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			t.Errorf("text form holds control character %q: %q", r, out.Text)
			break
		}
	}
	if out.Version != Version {
		t.Errorf("version = %q", out.Version)
	}
	if in.Policy == api.RemoteBlock {
		again, err := Sanitize(Input{HTML: out.HTML, Mode: in.Mode, Policy: api.RemoteBlock, KnownCIDs: in.KnownCIDs, MaxOutputSize: in.MaxOutputSize})
		if err != nil {
			t.Errorf("sanitising the output failed: %v\n%q", err, out.HTML)
		} else if again.HTML != out.HTML {
			t.Errorf("not idempotent:\n first %q\nsecond %q", out.HTML, again.HTML)
		}
	}
}

func FuzzSanitize(f *testing.F) {
	for _, s := range hostileSeeds {
		f.Add(s, 0)
		f.Add(s, 1)
	}
	for _, h := range corpusHTML(f) {
		f.Add(h, 0)
	}
	f.Fuzz(func(t *testing.T, src string, mode int) {
		in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: fuzzKnown}
		if mode%2 == 1 {
			in.Mode = ModeCompose
		}
		out, err := Sanitize(in)
		if err != nil {
			if out.HTML != "" || out.Text != "" || len(out.Links) != 0 || len(out.CIDs) != 0 {
				t.Fatalf("failed sanitisation leaked output: %+v", out)
			}
			return
		}
		checkClean(t, in, out)
	})
}

// TestCorpusSanitised runs every HTML message of the MIME corpus through
// the view-mode sanitiser and checks the invariants; a message the
// sanitiser refuses is fine, a message it lets through unclean is not.
func TestCorpusSanitised(t *testing.T) {
	bodies := corpusHTML(t)
	if len(bodies) < 10 {
		t.Fatalf("only %d HTML messages in the corpus", len(bodies))
	}
	for name, h := range bodies {
		t.Run(name, func(t *testing.T) {
			in := Input{HTML: h, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: map[string]string{"img1@example.org": "acc/msg/1.2"}}
			out, err := Sanitize(in)
			if err != nil {
				if out.HTML != "" || out.Text != "" {
					t.Fatalf("refused body leaked output: %+v", out)
				}
				t.Logf("refused: %v", err)
				return
			}
			checkClean(t, in, out)
		})
	}
}
