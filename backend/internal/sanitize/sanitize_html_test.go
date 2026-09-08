// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// One test per row of the threat table in docs/security.md §3.1, plus the
// mechanics (hoisting, wrapping, caps, text) the plan relies on. Every
// successful output also goes through checkClean, so each case doubles as
// an idempotence and allow-list check.

var testKnown = map[string]string{
	"img1@example.org": "acc_1/msg_1/1.2",
	"logo":             "acc_1/msg_1/1.3",
}

func view(t *testing.T, src string) Output {
	t.Helper()
	in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: testKnown}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize(%q): %v", src, err)
	}
	checkClean(t, in, out)
	return out
}

func compose(t *testing.T, src string, known map[string]string) Output {
	t.Helper()
	in := Input{HTML: src, Mode: ModeCompose, Policy: api.RemoteBlock, KnownCIDs: known}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize(%q): %v", src, err)
	}
	checkClean(t, in, out)
	return out
}

func wantHTML(t *testing.T, out Output, want string) {
	t.Helper()
	if out.HTML != want {
		t.Errorf("html:\n got %q\nwant %q", out.HTML, want)
	}
}

func TestViewKeepsFormatting(t *testing.T) {
	out := view(t, `<p style="color: blue; position: fixed">Hi <b>there</b> <i>x</i><br><font color="#ff0000" face="Georgia, serif" size="+1">f</font></p>`)
	wantHTML(t, out, `<p style="color: blue">Hi <b>there</b> <i>x</i><br/><font color="#ff0000" face="Georgia, serif" size="+1">f</font></p>`)
	if out.Text != "Hi there x\nf" {
		t.Errorf("text = %q", out.Text)
	}
	if out.Blocked != (api.BlockedContent{}) {
		t.Errorf("blocked = %+v, want nothing", out.Blocked)
	}
}

func TestScriptsDropped(t *testing.T) {
	out := view(t, `<script>alert(1)</script><p>a</p><script src="https://x/s.js"></script><noscript><b>b</b></noscript>`)
	wantHTML(t, out, `<p>a</p><b>b</b>`)
	if out.Blocked.Scripts != 2 {
		t.Errorf("scripts = %d, want 2", out.Blocked.Scripts)
	}
}

func TestEventHandlersStripped(t *testing.T) {
	out := view(t, `<p onclick="x()" ONMOUSEOVER="y()" title="t">t</p><body onload="z()">`)
	wantHTML(t, out, `<p title="t">t</p>`)
	if out.Blocked.EventHandlers != 3 {
		t.Errorf("handlers = %d, want 3 (the parser merges a second <body>'s attributes into the first)", out.Blocked.EventHandlers)
	}
}

func TestLinkSchemes(t *testing.T) {
	cases := []struct {
		href      string
		wantHref  string // "" = attribute removed
		dangerous bool
	}{
		{"https://example.org/x", "https://example.org/x", false},
		{"HTTPS://Example.ORG/Path?Q=1#F", "https://Example.ORG/Path?Q=1#F", false},
		{"http://example.org", "http://example.org", false},
		{"mailto:a@example.org?subject=Hi", "mailto:a@example.org?subject=Hi", false},
		{"javascript:alert(1)", "", true},
		{" JaVaScRiPt:alert(1)", "", true},
		{"jav&#x09;ascript:alert(1)", "", true},
		{"jav&#x0A;ascript:alert(1)", "", true},
		{"&#106;avascript:alert(1)", "", true},
		{"javascript&colon;alert(1)", "", true},
		{"\t\n javascript:alert(1)", "", true},
		{"vbscript:MsgBox(1)", "", true},
		{"data:text/html,<script>alert(1)</script>", "", true},
		{"blob:https://example.org/x", "", true},
		{"ftp://files.example.org/x", "", true},
		{"/relative/path", "", false},
		{"#anchor", "", false},
		{"", "", false},
		{"java&#0;script:alert(1)", "", false}, // U+FFFD in the scheme: not a scheme, not dangerous, just gone
	}
	for _, c := range cases {
		out := view(t, `<a href="`+c.href+`" target="_blank">t</a>`)
		want := `<a>t</a>`
		if c.wantHref != "" {
			want = `<a href="` + c.wantHref + `" rel="noopener noreferrer">t</a>`
		}
		if out.HTML != want {
			t.Errorf("href %q: got %q, want %q", c.href, out.HTML, want)
		}
		if got := out.Blocked.DangerousURLs; got != boolInt(c.dangerous) {
			t.Errorf("href %q: dangerousUrls = %d, want %d", c.href, got, boolInt(c.dangerous))
		}
		if c.wantHref != "" {
			if len(out.Links) != 1 || out.Links[0].Href != c.wantHref || out.Links[0].Text != "t" {
				t.Errorf("href %q: links = %+v", c.href, out.Links)
			}
		} else if len(out.Links) != 0 {
			t.Errorf("href %q: links = %+v, want none", c.href, out.Links)
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestImagesUnderBlock(t *testing.T) {
	cases := []struct {
		name, src string
		wantHTML  string
		blocked   api.BlockedContent
	}{
		{"known cid", `<img src="cid:img1@example.org" width="64" alt="logo">`,
			`<img src="malachi-cid:acc_1/msg_1/1.2" width="64" alt="logo"/>`, api.BlockedContent{}},
		{"encoded cid", `<img src="cid:img1%40example.org">`,
			`<img src="malachi-cid:acc_1/msg_1/1.2"/>`, api.BlockedContent{}},
		{"bracketed cid", `<img src="cid:<img1@example.org>">`,
			`<img src="malachi-cid:acc_1/msg_1/1.2"/>`, api.BlockedContent{}},
		{"unknown cid", `<img src="cid:missing@example.org" alt="x">`, ``, api.BlockedContent{DangerousURLs: 1}},
		{"traversal cid", `<img src="cid:../../etc/passwd">`, ``, api.BlockedContent{DangerousURLs: 1}},
		{"own local scheme", `<img src="malachi-cid:acc_1/msg_1/1.3">`,
			`<img src="malachi-cid:acc_1/msg_1/1.3"/>`, api.BlockedContent{}},
		{"foreign local scheme", `<img src="malachi-cid:acc_2/msg_9/1">`, ``, api.BlockedContent{DangerousURLs: 1}},
		{"https", `<img src="https://cdn.example.org/a.png" width="300">`, ``, api.BlockedContent{RemoteImages: 1}},
		{"http", `<img src="http://cdn.example.org/a.png">`, ``, api.BlockedContent{RemoteImages: 1}},
		{"protocol relative", `<img src="//cdn.example.org/a.png">`, ``, api.BlockedContent{RemoteImages: 1}},
		{"relative", `<img src="images/a.png">`, ``, api.BlockedContent{RemoteImages: 1}},
		{"data", `<img src="data:image/png;base64,AAAA">`, ``, api.BlockedContent{DangerousURLs: 1}},
		{"javascript", `<img src="jav&#x09;ascript:alert(1)">`, ``, api.BlockedContent{DangerousURLs: 1}},
		{"no src", `<img alt="nothing" onerror="x()">`, ``, api.BlockedContent{EventHandlers: 1}},
		{"srcset", `<img srcset="https://x/a.png 1x" src="cid:img1@example.org">`,
			`<img src="malachi-cid:acc_1/msg_1/1.2"/>`, api.BlockedContent{RemoteImages: 1}},
		{"pixel by size", `<img src="https://t.example/p.gif" width="1" height="1">`, ``, api.BlockedContent{TrackingPixels: 1}},
		{"pixel by style", `<img src="https://t.example/p.gif" style="display:none">`, ``, api.BlockedContent{TrackingPixels: 1}},
		{"pixel by px", `<img src="https://t.example/p.gif" style="width:1px;height:1px">`, ``, api.BlockedContent{TrackingPixels: 1}},
		{"pixel hidden", `<img src="https://t.example/p.gif" hidden>`, ``, api.BlockedContent{TrackingPixels: 1}},
		{"pixel http", `<img src="http://t.example/p.gif" width="0" height="0">`, ``, api.BlockedContent{TrackingPixels: 1}},
		{"background attr", `<table background="https://x/b.png"><tr><td>c</td></tr></table>`,
			`<table><tbody><tr><td>c</td></tr></tbody></table>`, api.BlockedContent{RemoteImages: 1}},
		{"video", `<video poster="https://x/p.png" src="https://x/c.mp4"></video>`, ``, api.BlockedContent{RemoteImages: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := view(t, c.src)
			wantHTML(t, out, c.wantHTML)
			if out.Blocked != c.blocked {
				t.Errorf("blocked = %+v, want %+v", out.Blocked, c.blocked)
			}
		})
	}
	out := view(t, `<img src="cid:img1@example.org"><img src="cid:logo">`)
	if len(out.CIDs) != 2 || out.CIDs[0] != "img1@example.org" || out.CIDs[1] != "logo" {
		t.Errorf("cids = %v", out.CIDs)
	}
}

func TestImagesUnderAllow(t *testing.T) {
	// Without a hook the https: reference stays (tests, old documentation).
	in := Input{HTML: `<img src="https://x/1.png"><img src="http://x/2.png"><img src="https://x/p.gif" width="1">`, Mode: ModeView, Policy: api.RemoteAllow}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	checkClean(t, in, out)
	wantHTML(t, out, `<img src="https://x/1.png"/>`)
	if out.Blocked.RemoteImages != 1 || out.Blocked.TrackingPixels != 1 {
		t.Errorf("blocked = %+v", out.Blocked)
	}

	// With a hook the image is inlined; a refused, empty or non-image
	// answer drops it and counts it; a pixel is never even asked for.
	var asked []string
	hook := func(u string) (string, []byte, bool) {
		asked = append(asked, u)
		switch u {
		case "https://x/1.png":
			return "image/png", []byte("PNG"), true
		case "https://x/svg.png":
			return "image/svg+xml", []byte("<svg/>"), true
		case "https://x/empty.png":
			return "image/png", nil, true
		}
		return "", nil, false
	}
	in = Input{HTML: `<img src="https://x/1.png"><img src="https://x/svg.png"><img src="https://x/empty.png"><img src="https://x/404.png"><img src="https://x/p.gif" width="1">`,
		Mode: ModeView, Policy: api.RemoteAllow, RemoteImage: hook}
	out, err = Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	checkClean(t, in, out)
	wantHTML(t, out, `<img src="data:image/png;base64,UE5H"/>`)
	if out.Blocked.RemoteImages != 3 || out.Blocked.TrackingPixels != 1 {
		t.Errorf("blocked = %+v", out.Blocked)
	}
	if len(asked) != 4 {
		t.Errorf("hook asked for %v, want the four real images only", asked)
	}
	// Inlined bytes do not count against the output cap.
	big := make([]byte, 3000)
	in = Input{HTML: `<img src="https://x/big.png">`, Mode: ModeView, Policy: api.RemoteAllow, MaxOutputSize: 200,
		RemoteImage: func(string) (string, []byte, bool) { return "image/jpeg", big, true }}
	if _, err := Sanitize(in); err != nil {
		t.Errorf("inlined image counted against the cap: %v", err)
	}
}

func TestStyleAttribute(t *testing.T) {
	cases := []struct {
		in, want string
		blocked  api.BlockedContent
	}{
		{"color: red; position: fixed; top: 0", "color: red", api.BlockedContent{}},
		{"background: url(https://x/a.png); color: blue", "color: blue", api.BlockedContent{RemoteImages: 1}},
		{"background-image: URL( 'https://x/a.png' )", "", api.BlockedContent{RemoteImages: 1}},
		{"width: expression(alert(1))", "", api.BlockedContent{}},
		{`background: u\72 l(https://x/e)`, "", api.BlockedContent{}},
		{"background: u/**/rl(https://x/c)", "", api.BlockedContent{RemoteImages: 1}},
		{"font-size: 14px !important; margin: 0 auto", "font-size: 14px !important; margin: 0 auto", api.BlockedContent{}},
		{"visibility: hidden; opacity: 0; z-index: 9", "", api.BlockedContent{}},
		{"display: none", "display: none", api.BlockedContent{}},
		{"COLOR: Red; Font-Family: 'Segoe UI', Arial", "color: Red; font-family: 'Segoe UI', Arial", api.BlockedContent{}},
		{"behavior: url(x.htc); -moz-binding: url(https://x/b.xml)", "", api.BlockedContent{}},
		{"color: red; content: 'x'", "color: red", api.BlockedContent{}},
		{"font-family: a;b", "font-family: a", api.BlockedContent{}},
		{"color: r<ed", "", api.BlockedContent{}},
		{"position: relative; float: left; clear: both", "position: relative; float: left; clear: both", api.BlockedContent{}},
	}
	for _, c := range cases {
		var blocked api.BlockedContent
		got := filterDeclarations(c.in, &blocked)
		if got != c.want {
			t.Errorf("filterDeclarations(%q) = %q, want %q", c.in, got, c.want)
		}
		if blocked != c.blocked {
			t.Errorf("filterDeclarations(%q) blocked = %+v, want %+v", c.in, blocked, c.blocked)
		}
		if again := filterDeclarations(got, &blocked); again != got {
			t.Errorf("filterDeclarations(%q) not idempotent: %q", c.in, again)
		}
	}
}

func TestStylesheet(t *testing.T) {
	src := `
@import url("https://x/s.css");
@import 'https://x/t.css';
@font-face { font-family: leak; src: url(https://x/f.woff); }
input[value^="a"] { background: url(https://x/a); }
p { color: red; font-family: Arial, sans-serif }
.overlay { position: fixed; width: 100%; z-index: 9 }
@media screen and (max-width: 600px) { p { font-size: 18px } @font-face { src: url(https://x/n.woff) } }
@supports (display: grid) { p { display: grid } }
p::before { content: "Trusted: " }
a:hover, td.x > span { text-decoration: underline }
bad<sel { color: red }
p { font-family: "</style><script>alert(1)</script>" }
}
tr:nth-child(2n) { background-color: #eee }`
	var blocked api.BlockedContent
	var budget int
	got := filterStylesheet(src, &blocked, &budget)
	want := "p { color: red; font-family: Arial, sans-serif }\n" +
		".overlay { width: 100% }\n" +
		"@media screen and (max-width: 600px) {\np { font-size: 18px }\n}\n" +
		"a:hover, td.x > span { text-decoration: underline }\n" +
		"tr:nth-child(2n) { background-color: #eee }"
	if got != want {
		t.Errorf("stylesheet:\n got %q\nwant %q", got, want)
	}
	if blocked.RemoteStyles != 2 || blocked.RemoteFonts != 2 || blocked.RemoteImages != 1 {
		t.Errorf("blocked = %+v", blocked)
	}
	if strings.Contains(got, "<") {
		t.Errorf("stylesheet output contains '<': %q", got)
	}
	var again int
	if twice := filterStylesheet(got, &blocked, &again); twice != got {
		t.Errorf("not idempotent:\n%q\n%q", got, twice)
	}
}

func TestStylesHoistedAndBodyWrapped(t *testing.T) {
	out := view(t, `<!DOCTYPE html><html><head><title>T</title><meta charset="utf-8"><style>p { color: red }</style></head>
<body bgcolor="#000" text="#fff" link="#0ff" style="margin: 0; background: url(https://x/b.png)" onload="x()">
<p>a</p><style>.b { color: blue }</style><p class="b">b</p>
</body></html>`)
	wantHTML(t, out, `<style>p { color: red }</style><style>.b { color: blue }</style><div class="malachi-body" style="background-color: #000; color: #fff; margin: 0"><p>a</p><p class="b">b</p>
</div>`)
	if out.Blocked.EventHandlers != 1 || out.Blocked.RemoteImages != 1 {
		t.Errorf("blocked = %+v", out.Blocked)
	}
	if out.Text != "a\n\nb" {
		t.Errorf("text = %q", out.Text)
	}
	// No presentational body attributes: no wrapper.
	out = view(t, `<html><body>
<p>only</p>
</body></html>`)
	wantHTML(t, out, "<p>only</p>\n")
}

func TestFramesAndNavigation(t *testing.T) {
	out := view(t, `<base href="https://evil.example.org/"><meta http-equiv="refresh" content="0;url=https://evil"><meta name="viewport" content="x">
<iframe src="https://evil.example.org/f"></iframe><object data="x"></object><embed src="x"><applet code="E"></applet>
<link rel="stylesheet" href="https://evil.example.org/s.css"><link rel="icon" href="https://evil.example.org/i.ico">
<p>Visible text survives.</p>`)
	wantHTML(t, out, `<p>Visible text survives.</p>`)
	want := api.BlockedContent{EmbeddedFrames: 4, DangerousURLs: 2, RemoteStyles: 2}
	if out.Blocked != want {
		t.Errorf("blocked = %+v, want %+v", out.Blocked, want)
	}
}

func TestFormsDropped(t *testing.T) {
	out := view(t, `<form action="https://evil.example.org/c"><label>User <input name="u"></label><button>Go</button></form><fieldset><legend>L</legend>in</fieldset><p>after</p>`)
	wantHTML(t, out, `Lin<p>after</p>`)
	if out.Blocked.Forms != 1 {
		t.Errorf("forms = %d, want 1 (the form takes its controls with it)", out.Blocked.Forms)
	}
}

func TestForeignContentDropped(t *testing.T) {
	out := view(t, `<p>Before.</p><svg onload="x()"><script>y()</script><a xlink:href="javascript:z()"><circle r="1"/></a></svg><math><mi href="javascript:w()">m</mi></math><p>After.</p>`)
	wantHTML(t, out, `<p>Before.</p><p>After.</p>`)
	if out.Blocked.Scripts != 2 {
		t.Errorf("scripts = %d, want 2", out.Blocked.Scripts)
	}
}

func TestUnknownElementsUnwrapped(t *testing.T) {
	out := view(t, `<custom-widget data-x="1"><marquee>scroll</marquee><picture><source srcset="https://x/a.webp"><img src="cid:logo"></picture></custom-widget>`)
	wantHTML(t, out, `scroll<img src="malachi-cid:acc_1/msg_1/1.3"/>`)
	if out.Blocked.RemoteImages != 0 {
		t.Errorf("a <source> without src is not a remote image: %+v", out.Blocked)
	}
}

func TestAttributeValues(t *testing.T) {
	out := view(t, `<table width="100%" cellpadding="8" border="0" bgcolor="#202020" align="center" summary="s"><tr><td width="50%" valign="top" colspan="2" nowrap bgcolor="rgb(1,2,3)">x</td></tr></table><img src="cid:logo" width="1e3" height="abc" alt="a&lt;b">`)
	wantHTML(t, out, `<table width="100%" cellpadding="8" border="0" bgcolor="#202020" align="center" summary="s"><tbody><tr><td width="50%" valign="top" colspan="2" nowrap="">x</td></tr></tbody></table><img src="malachi-cid:acc_1/msg_1/1.3" alt="a&lt;b"/>`)
}

func TestLinksExported(t *testing.T) {
	out := view(t, `<p><a href="https://evil.example.org/login">https://bank.example.org/</a> and <a href="mailto:s@example.org"><b>mail</b> <img src="cid:logo" alt="us"></a></p>`)
	if len(out.Links) != 2 {
		t.Fatalf("links = %+v", out.Links)
	}
	if out.Links[0] != (api.Link{Text: "https://bank.example.org/", Href: "https://evil.example.org/login"}) {
		t.Errorf("link 0 = %+v", out.Links[0])
	}
	if out.Links[1] != (api.Link{Text: "mail us", Href: "mailto:s@example.org"}) {
		t.Errorf("link 1 = %+v", out.Links[1])
	}
}

func TestComposeMode(t *testing.T) {
	known := map[string]string{"a@b": "att_1"}
	out := compose(t, `<style>p { color: red }</style><p style="color: red; position: absolute"><b>bold</b> <img src="cid:a@b"> <img src="cid:nope"> <img src="data:image/png;base64,AAAA"> <img src="https://x/1.png"> <a href="https://x" target="_blank">l</a></p>`, known)
	wantHTML(t, out, `<p style="color: red"><b>bold</b> <img src="cid:a@b"/>    <a href="https://x">l</a></p>`)
	if out.Blocked.DangerousURLs != 2 || out.Blocked.RemoteImages != 1 {
		t.Errorf("blocked = %+v", out.Blocked)
	}
	if len(out.CIDs) != 1 || out.CIDs[0] != "a@b" {
		t.Errorf("cids = %v", out.CIDs)
	}
	if out.Text != "bold l <https://x>" {
		t.Errorf("text = %q", out.Text)
	}
	// Compose never fetches, whatever the caller says.
	in := Input{HTML: `<img src="https://x/1.png">`, Mode: ModeCompose, Policy: api.RemoteAllow,
		RemoteImage: func(string) (string, []byte, bool) { t.Fatal("compose mode fetched"); return "", nil, false }}
	if _, err := Sanitize(in); err == nil {
		t.Error("compose mode with allow must fail")
	}
}

func TestCapsFailClosed(t *testing.T) {
	cases := map[string]Input{
		"depth":  {HTML: strings.Repeat("<div>", maxDepth+5) + "x" + strings.Repeat("</div>", maxDepth+5)},
		"attrs":  {HTML: "<p " + strings.Repeat("a%d=1 ", maxAttrs+1) + ">x</p>"},
		"nodes":  {HTML: strings.Repeat("<b></b>", maxNodes+10)},
		"input":  {HTML: strings.Repeat("x", maxInputBytes+1)},
		"output": {HTML: "<p>" + strings.Repeat("y", 500) + "</p>", MaxOutputSize: 100},
		"policy": {HTML: "<p>x</p>", Policy: "whatever"},
		"mode":   {HTML: "<p>x</p>", Mode: Mode(7)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "attrs" {
				// Distinct names: the parser drops duplicates itself.
				var b strings.Builder
				b.WriteString("<p")
				for i := 0; i <= maxAttrs; i++ {
					b.WriteString(" data-a")
					b.WriteString(strings.Repeat("x", i))
					b.WriteString("=\"1\"")
				}
				b.WriteString(">x</p>")
				in.HTML = b.String()
			}
			if in.Policy == "" {
				in.Policy = api.RemoteBlock
			}
			out, err := Sanitize(in)
			if err == nil {
				t.Fatalf("expected an error, got %+v", out)
			}
			if out.HTML != "" || out.Text != "" || len(out.Links) != 0 || len(out.CIDs) != 0 || out.Blocked != (api.BlockedContent{}) {
				t.Errorf("failed sanitisation leaked output: %+v", out)
			}
			if out.Version != Version {
				t.Errorf("version = %q", out.Version)
			}
			if !strings.Contains(err.Error(), "sanitizeFailed") && !strings.Contains(err.Error(), "1501") {
				t.Errorf("error = %v, want sanitizeFailed", err)
			}
		})
	}
	// Just under the caps is fine.
	if _, err := Sanitize(Input{HTML: strings.Repeat("<div>", maxDepth-5) + "x" + strings.Repeat("</div>", maxDepth-5), Policy: api.RemoteBlock}); err != nil {
		t.Errorf("depth just under the cap: %v", err)
	}
}

func TestTextRendering(t *testing.T) {
	out := view(t, `<h1>Title</h1><p>Hello <b>world</b>, see <a href="https://x/y">the link</a> and <a href="https://x/z">https://x/z</a>.</p>
<ul><li>one</li><li>two <a href="https://x/w">w</a>x</li></ul>
<img src="cid:logo" alt="picture"><pre>  a
   b</pre><table><tr><td>1</td><td>2</td></tr><tr><td>3</td></tr></table><blockquote>quoted</blockquote><p>a&lt;b &amp; c</p>`)
	// Indentation inside <pre> is content and stays.
	want := "Title\n\nHello world, see the link <https://x/y> and https://x/z.\n\n- one\n- two w <https://x/w> x\n\n[picture]\n\n  a\n   b\n\n1\t2\n3\n> quoted\n\na<b & c"
	if out.Text != want {
		t.Errorf("text:\n got %q\nwant %q", out.Text, want)
	}
}

// A <blockquote> renders the way plain-text mail quotes: "> " on every
// line, the bare mark on blank lines inside, "> > " when nested; the quote
// starts right under what precedes it (its attribution), and the blank
// line after one that closed stays plain.
func TestTextQuotesBlockquote(t *testing.T) {
	cases := []struct{ html, want string }{
		{`<blockquote><div>On X wrote:</div><p>a</p><p>b</p><blockquote>c</blockquote></blockquote>`,
			"> On X wrote:\n>\n> a\n>\n> b\n> > c"},
		{`<div>On X wrote:</div><blockquote type="cite"><p>a</p><p>b</p></blockquote><p>after</p>`, "On X wrote:\n> a\n>\n> b\n\nafter"},
		{`<p>x</p><blockquote><p>a</p></blockquote><p>after</p>`, "x\n> a\n\nafter"},
		{`<blockquote><blockquote><p>a</p><p>b</p></blockquote><p>c</p></blockquote>`, "> > a\n> >\n> > b\n>\n> c"},
		{"<blockquote><pre>one\n\n  two</pre></blockquote>", "> one\n>\n>   two"},
		{`<blockquote><ul><li>one</li><li>two</li></ul>a<br>b</blockquote>`, "> - one\n> - two\n>\n> a\n> b"},
		{`<blockquote><table><tr><td>1</td><td>2</td></tr></table></blockquote>`, "> 1\t2"},
		{`<blockquote>   </blockquote><p>a</p>`, "a"},
		{`<blockquote><p>a</p><p></p><p></p></blockquote>`, "> a"},
	}
	for _, c := range cases {
		out := compose(t, c.html, nil)
		if out.Text != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.html, out.Text, c.want)
		}
	}
}

func TestLeadingWhitespaceDropped(t *testing.T) {
	out := view(t, "<html><body>\n\n  <p>a</p>\n</body></html>")
	wantHTML(t, out, "<p>a</p>\n")
	out = view(t, "   \n  ")
	wantHTML(t, out, "")
	if out.Text != "" {
		t.Errorf("text = %q", out.Text)
	}
}

func TestVersion(t *testing.T) {
	if Version == "0-stub" {
		t.Fatal("the stub version must not survive the real implementation")
	}
	out := view(t, "<p>x</p>")
	if out.Version != Version {
		t.Errorf("version = %q", out.Version)
	}
}
