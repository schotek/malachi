// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCIDReferences(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		want     []string // must be there
		absent   []string // must not
		complete bool
	}{
		{"img src", `<img src="cid:a@x">`, []string{"a@x"}, nil, true},
		{"capitals", `<IMG SRC="CID:Upper@Example.ORG">`, []string{"upper@example.org"}, nil, true},
		{"entity scheme", `<img src="&#99;id:ent@x">`, []string{"ent@x"}, nil, true},
		{"entity colon", `<img src="cid&colon;colon@x">`, []string{"colon@x"}, nil, true},
		{"percent", `<img src="cid:pct%40x%2Fy">`, []string{"pct@x/y"}, nil, true},
		{"bad percent", `<img src="cid:bad%zz@x">`, []string{"bad%zz@x"}, nil, true},
		{"angle brackets", `<img src="cid:<br@x>">`, []string{"br@x"}, nil, true},
		{"encoded brackets", `<img src="cid:%3Cenc@x%3E">`, []string{"enc@x"}, nil, true},
		{"tab in scheme", "<img src=\"c\tid:tab@x\">", []string{"tab@x"}, nil, true},
		{"newline in id", "<img src=\"cid:new\nline@x\">", []string{"newline@x"}, nil, true},
		{"spaces around", "<img src=\"  \x01cid:sp@x \">", []string{"sp@x"}, nil, true},
		{"unquoted", `<img src=cid:unq@x alt=y>`, []string{"unq@x"}, nil, true},
		{"image element", `<image src="cid:image@x">`, []string{"image@x"}, nil, true},
		{"srcset", `<img srcset="cid:s1@x 1x, cid:s2@x 2x">`, []string{"s1@x", "s2@x"}, nil, true},
		{"style attribute", `<div style="background:url(cid:st@x)">`, []string{"st@x"}, nil, true},
		{"style quoted url", `<div style="background:url('cid:q@x')">`, []string{"q@x"}, nil, true},
		{"style element", `<style>p{background:url("cid:blk@x")}</style>`, []string{"blk@x"}, nil, true},
		{"background", `<table><tr><td background="cid:bg@x">x</td></tr></table>`, []string{"bg@x"}, nil, true},
		{"href", `<a href="cid:href@x">x</a>`, []string{"href@x"}, nil, true},
		{"text", `<p>see cid:text@x, please</p>`, []string{"text@x"}, nil, true},
		{"noscript is markup", `<noscript><img src="cid:ns@x"></noscript>`, []string{"ns@x"}, nil, true},
		{"noscript entity", `<noscript><img src="&#99;id:nse@x"></noscript>`, []string{"nse@x"}, nil, true},
		// A tokenizer that reads <noscript> as raw text ends it at the first
		// </noscript> and then sees a comment; the parser the sanitiser uses
		// sees an attribute value there and then a real image.
		{"noscript divergence", `<noscript><a title="</noscript><!--"><img src="cid:tricky@x">-->`, []string{"tricky@x"}, nil, true},
		{"svg breakout", `<svg><style><img src="cid:svg@x"></style></svg>`, []string{"svg@x"}, nil, true},
		{"comment", `<!-- <img src="cid:comment@x"> --><p>x</p>`, nil, []string{"comment@x"}, true},
		{"conditional comment", `<!--[if mso]><v:fill src="cid:vml@x"/><![endif]-->`, nil, []string{"vml@x"}, true},
		{"not a scheme", `<img src="xcid:no@x"><img src="%63id:pct@x">`, nil, []string{"pct@x"}, true},
		{"empty", `<img src="cid:"><img src="cid:<>">`, nil, []string{""}, true},
		{"unicode", `<img src="cid:Žluť@X">`, []string{"žluť@x"}, nil, true},
		{"own scheme", `<img src="malachi-cid:acc/msg/2">`, nil, nil, false},
		{"own scheme spelled", `<img src="MALACHI-&#99;ID:acc/msg/2">`, nil, nil, false},
		{"nothing", `<p>Hello</p>`, nil, nil, true},
		{"not html at all", "\x00\xff<<<>>>", nil, nil, true},
	}
	for _, c := range cases {
		refs, complete := CIDReferences(c.html)
		if complete != c.complete {
			t.Errorf("%s: complete = %v", c.name, complete)
		}
		for _, id := range c.want {
			if !refs[id] {
				t.Errorf("%s: %q missing from %q", c.name, id, sortedKeys(refs))
			}
		}
		for _, id := range c.absent {
			if refs[id] {
				t.Errorf("%s: %q should not be there: %q", c.name, id, sortedKeys(refs))
			}
		}
	}
}

func TestCIDReferencesFixture(t *testing.T) {
	p := parseFile(t, "skeleton-cid-refs.eml")
	refs, complete := CIDReferences(p.RawHTML)
	if !complete {
		t.Fatal("incomplete")
	}
	for _, id := range []string{
		"block@example.org", "upper@example.org", "entity@example.org",
		"percent@example.org", "angle@example.org", "tab@example.org",
		"srcset1@example.org", "srcset2@example.org", "style@example.org",
		"background@example.org", "href@example.org", "noscript@example.org",
	} {
		if !refs[id] {
			t.Errorf("%q missing from %q", id, sortedKeys(refs))
		}
	}
	// Referenced only from a comment, or not at all: candidates to leave on
	// the server, whatever their Content-ID looks like.
	for _, a := range p.Attachments {
		key := NormalizeCID(a.ContentID)
		want := a.PartID == "1.2"
		if refs[key] != want {
			t.Errorf("part %s (%q): referenced = %v", a.PartID, a.ContentID, refs[key])
		}
	}
	// Every part that shares an identifier with the picture counts as shown.
	p = parseFile(t, "skeleton-duplicate-cid.eml")
	refs, _ = CIDReferences(p.RawHTML)
	for _, a := range p.Attachments {
		if !refs[NormalizeCID(a.ContentID)] {
			t.Errorf("duplicate %s (%q) not referenced", a.PartID, a.ContentID)
		}
	}
}

func TestCIDReferencesCaps(t *testing.T) {
	var b strings.Builder
	for i := range maxCIDRefs + 100 {
		fmt.Fprintf(&b, `<img src="cid:%d@x">`, i)
	}
	refs, complete := CIDReferences(b.String())
	if complete || len(refs) != maxCIDRefs {
		t.Errorf("cap: complete = %v, %d refs", complete, len(refs))
	}
	refs, complete = CIDReferences(strings.Repeat(`<img src="cid:same@x">`, 5000))
	if !complete || len(refs) != 1 {
		t.Errorf("repeats: complete = %v, %d refs", complete, len(refs))
	}
	if refs, complete := CIDReferences(strings.Repeat("x", maxCIDScanBytes+1)); complete || len(refs) != 0 {
		t.Errorf("oversized: complete = %v", complete)
	}
	// An identifier longer than any Content-ID can be is not one.
	long := "cid:" + strings.Repeat("a", maxCIDBytes+1)
	if refs, complete := CIDReferences(`<img src="` + long + `">`); !complete || len(refs) != 0 {
		t.Errorf("long id: %d refs", len(refs))
	}
	// Linear on the pathological cases: many references without an end,
	// and a million bytes of "cid:".
	start := time.Now()
	for _, s := range []string{
		"<style>" + strings.Repeat("cid:", 1<<18) + "</style>",
		"<p title=\"" + strings.Repeat("cid:a", 1<<17) + "\">",
		strings.Repeat("<b>cid:x ", 1<<16),
	} {
		refs, _ := CIDReferences(s)
		if len(refs) > maxCIDRefs {
			t.Errorf("%d refs", len(refs))
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestNormalizeCID(t *testing.T) {
	cases := map[string]string{
		"Part1@Example.ORG":            "part1@example.org",
		"<angle@x>":                    "angle@x",
		" <spaced@x> ":                 "spaced@x",
		"in side@x":                    "inside@x",
		"tab\tnl\ncr\r@x":              "tabnlcr@x",
		"ctl\x00\x1f\x7f@x":            "ctl@x",
		"bad\xffutf8":                  "bad\xef\xbf\xbdutf8",
		"ŽLUŤ@X":                       "žluť@x",
		"<<>>":                         "",
		"a<b>c":                        "a<b>c",
		"\xc2\xa0nbsp\xe2\x80\x83em@x": "nbspem@x",
	}
	for in, want := range cases {
		got := NormalizeCID(in)
		if got != want {
			t.Errorf("NormalizeCID(%q) = %q, want %q", in, got, want)
		}
		if again := NormalizeCID(got); again != got {
			t.Errorf("not idempotent: %q -> %q", got, again)
		}
	}
}

func FuzzCIDReferences(f *testing.F) {
	for _, s := range []string{
		`<img src="cid:a@x">`,
		`<img src="&#99;id:%3Cb%40x%3E">`,
		`<noscript><a title="</noscript><!--"><img src="cid:c@x">-->`,
		`<style>p{background:url(cid:d@x)}</style><div style="background:url('cid:e@x')">`,
		`<img srcset="cid:f@x 1x, CID:G@X 2x"><td background="cid:h@x">`,
		`<!--[if mso]><v:fill src="cid:i@x"/><![endif]--><img src="malachi-cid:a/b/1">`,
		"cid:cid:cid:\x00\xff",
	} {
		f.Add(s)
	}
	for _, h := range corpusHTML(f) {
		f.Add(h)
	}
	f.Fuzz(func(t *testing.T, s string) {
		refs, complete := CIDReferences(s)
		if len(refs) > maxCIDRefs {
			t.Fatalf("%d refs", len(refs))
		}
		for id := range refs {
			if id == "" || len(id) > 3*maxCIDBytes || NormalizeCID(id) != id {
				t.Fatalf("bad key %q", id)
			}
		}
		again, complete2 := CIDReferences(s)
		if complete != complete2 || !slices.Equal(sortedKeys(refs), sortedKeys(again)) {
			t.Fatal("not deterministic")
		}
	})
}

// corpusHTML returns the HTML bodies of the corpus, as fuzz seeds.
func corpusHTML(tb testing.TB) []string {
	tb.Helper()
	var out []string
	for _, name := range []string{"skeleton-cid-refs.eml", "html-inline-cid.eml", "html-cid-foreign.eml", "html-obfuscated-urls.eml", "html-svg-math.eml"} {
		p, err := Parse(strings.NewReader(string(readFile(tb, name))), DefaultLimits())
		if err == nil && p.HasHTML {
			out = append(out, p.RawHTML)
		}
	}
	return out
}
