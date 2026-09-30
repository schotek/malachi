// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// svgPicture is an SVG document with a script, which a site may serve
// under any name and type.
var svgPicture = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="1" height="1"/></svg>`)

func TestHostileRenderedHTML(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		f := jiratest.New(t, mode)
		stepClock(f, syncT0)
		ctx := context.Background()
		cp := ctxPath(f)
		f.AddSiteFile("/images/icons/emoticons/smile.png", "image/png", jiratest.PNG)
		f.AddSiteFile("/images/evil.svg", "image/png", svgPicture)
		f.AddSiteFile("/images/not-a-picture.png", "image/png", []byte("hello, not a picture"))
		fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "jira", "rendered", "hostile.html"))
		if err != nil {
			t.Fatal(err)
		}
		is := f.AddIssue("WEB", "Nepřátelský popis")
		att := f.AddAttachment(is.ID, "screen shot.png", "image/png", jiratest.PNG)
		desc := strings.NewReplacer("{ctx}", cp, "{att}", att).Replace(string(fixture))
		f.Update(is.ID, func(is *jiratest.Issue) { is.Description = desc })

		y, r := synthFor(t, f, f.Config())
		issue, items := siteItems(t, y, r, is.ID)
		raw, err := y.build(ctx, issue, items[0])
		if err != nil {
			t.Fatal(err)
		}
		p, err := imime.Parse(bytes.NewReader(raw), imime.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		site := f.Site.String()

		// Nothing outside the site was fetched.
		if n := f.OffSiteRequests(); n != 0 {
			t.Fatalf("%d requests left the site", n)
		}
		// Two pictures embedded — the emoticon and the attachment's
		// thumbnail; never the SVG, never what is no picture.
		var pics, files []api.Attachment
		for _, a := range p.Attachments {
			if a.Inline {
				pics = append(pics, a)
			} else {
				files = append(files, a)
			}
		}
		if len(pics) != 2 || pics[0].Filename != "smile.png" || pics[1].Filename != "screen shot.png" {
			t.Fatalf("pictures = %+v", pics)
		}
		for _, a := range append(pics, files...) {
			if strings.Contains(a.ContentType, "svg") || strings.Contains(a.ContentType, "html") {
				t.Fatalf("part %+v", a)
			}
		}
		if len(files) != 1 || files[0].Filename != "screen shot.png" {
			t.Fatalf("files = %+v", files)
		}
		h := p.RawHTML
		for _, want := range []string{
			`src="cid:img1.` + items[0].msgID + `"`,       // the emoticon, both times
			`src="cid:img2.` + items[0].msgID + `"`,       // the thumbnail
			`src="` + site + `/images/evil.svg"`,          // refused, left as an absolute link
			`src="` + site + `/images/not-a-picture.png"`, // not a picture
			`src="https://cdn.elsewhere.test/track.png"`,  // off-site: untouched
			`src="https://cdn.elsewhere.test/pixel.gif"`,  // protocol-relative: absolute
			`href="https://cdn.elsewhere.test/x"`,         // likewise
			`href="` + site + `/browse/WEB-2"`,            // relative: absolute
			`href="` + site + `/relative/page"`,           // relative to the site
			`src="data:image/png;base64,iVBORw0KGgo="`,    // the sanitiser's call
			`src="javascript:alert(1)"`,                   // the sanitiser's call
			`src="cid:forged@elsewhere"`,                  // no part of ours
			`href="https://user:secret@jira.acme.test/x"`, // not ours to resolve
			`href="` + site + `/secure/attachment/` + att + `/screen%20shot.png"`,
			"document.write('<img src=\"" + cp + "/images/icons/emoticons/smile.png\">')", // script text untouched
			`&lt;img src="` + cp + `/images/evil.svg"&gt;`,                                // text stays text
		} {
			if !strings.Contains(h, want) {
				t.Errorf("html lacks %s", want)
			}
		}
		if strings.Count(h, "cid:img1.") != 2 {
			t.Fatalf("the emoticon is embedded %d times", strings.Count(h, "cid:img1."))
		}
		// The site picture inside the comment, the textarea and the svg
		// stays as it was.
		if !strings.Contains(h, `<!-- <img src="`+cp+`/images/icons/emoticons/smile.png"> -->`) ||
			!strings.Contains(h, `<textarea><img src="`+cp+`/images/icons/emoticons/smile.png"></textarea>`) {
			t.Fatalf("raw text changed: %q", h)
		}
		// Each picture was downloaded once.
		if n := len(f.RequestsTo(http.MethodGet, "/images/icons/emoticons/smile.png")); n != 1 {
			t.Fatalf("the emoticon was downloaded %d times", n)
		}
	})
}

func TestRewriteAttrsPathological(t *testing.T) {
	nop := func(string, *html.Attribute) bool { return false }
	cases := map[string]string{
		"deep":       strings.Repeat("<div>", 200_000) + `<img src="/x">` + strings.Repeat("</div>", 200_000),
		"formatting": strings.Repeat(`<b class="a">`, 100_000) + "<p>text</p>",
		"entities":   strings.Repeat("&amp;&#x3C;&lt", 200_000),
		"unfinished": `<p>x <img src="/y`,
		"nul":        "<p>\x00<img src=\"/z\x00\">\x00</p>",
		"attributes": "<img " + strings.Repeat(`a=b `, 100_000) + `src="/q">`,
		"plain":      "no markup at all",
	}
	for name, doc := range cases {
		start := time.Now()
		if out := rewriteAttrs(doc, nop); out != doc {
			t.Errorf("%s: unchanged input came back changed", name)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
	// A change touches that tag only.
	doc := strings.Repeat("<div>", 1000) + `<img alt="a&amp;b" src="/x">` + "&amp; <b>t</b>"
	out := rewriteAttrs(doc, func(tag string, a *html.Attribute) bool {
		if tag == "img" && a.Val == "/x" {
			a.Val = `cid:"q"`
			return true
		}
		return false
	})
	want := strings.Repeat("<div>", 1000) + `<img alt="a&amp;b" src="cid:&#34;q&#34;">` + "&amp; <b>t</b>"
	if out != want {
		t.Fatalf("rewritten = %q", out[len(out)-80:])
	}
}

func TestAbsolutise(t *testing.T) {
	c, err := NewClient(Options{SiteURL: jiratest.DCSite, Deployment: api.JiraDataCenter})
	if err != nil {
		t.Fatal(err)
	}
	y, err := newSynth(api.JiraConfig{SiteURL: jiratest.DCSite}, nil, nil, c, User{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		`<a href="/jira/browse/X-1">`:       `<a href="https://jira.acme.test/jira/browse/X-1">`,
		`<a href="browse/X-1">`:             `<a href="https://jira.acme.test/jira/browse/X-1">`,
		`<a href="../x">`:                   `<a href="https://jira.acme.test/x">`,
		`<a href="//evil.test/x">`:          `<a href="https://evil.test/x">`,
		`<a href="#frag">`:                  `<a href="#frag">`,
		`<a href="/jira/browse/X-1#c-2">`:   `<a href="https://jira.acme.test/jira/browse/X-1#c-2">`,
		`<a href="mailto:a@b.test">`:        `<a href="mailto:a@b.test">`,
		`<a href="JavaScript:alert(1)">`:    `<a href="JavaScript:alert(1)">`,
		`<a href="https://x.test/a b">`:     `<a href="https://x.test/a b">`,
		`<a href=" /jira/x ">`:              `<a href="https://jira.acme.test/jira/x">`,
		"<a href=\"/jira/\x01x\">":          "<a href=\"/jira/\x01x\">",
		`<img src="" alt="x">`:              `<img src="" alt="x">`,
		`<link rel="x" href="/jira/c.css">`: `<link rel="x" href="https://jira.acme.test/jira/c.css">`,
	} {
		if got := y.absolutise(in); got != want {
			t.Errorf("absolutise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSniff(t *testing.T) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	for _, tc := range []struct {
		data    []byte
		picture string
		file    string
	}{
		{jiratest.PNG, "image/png", "image/png"},
		{gif, "image/gif", "image/gif"},
		{svgPicture, "", "text/plain; charset=utf-8"}, // text, never a picture
		{[]byte("<!DOCTYPE html><html><script>x</script>"), "", "application/octet-stream"},
		{[]byte("%PDF-1.7\n"), "", "application/pdf"},
		{[]byte("plain text"), "", "text/plain; charset=utf-8"},
		{nil, "", "text/plain; charset=utf-8"},
		{[]byte("<?xml version=\"1.0\"?><x/>"), "", "application/octet-stream"},
	} {
		if got := sniffPicture(tc.data); got != tc.picture {
			t.Errorf("sniffPicture(%.12q) = %q", tc.data, got)
		}
		if got := sniffFile(tc.data); got != tc.file {
			t.Errorf("sniffFile(%.12q) = %q", tc.data, got)
		}
	}
}

func TestContentRefused(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{ErrTooLarge, true},
		{&StatusError{Status: 404}, true},
		{&StatusError{Status: 403}, true},
		{&StatusError{Status: 410}, true},
		{&StatusError{Status: 401}, false},
		{&StatusError{Status: 429}, false},
		{&StatusError{Status: 500}, false},
		{&StatusError{Status: 503}, false},
		{api.NewError(api.CodeInvalidArgument, "outside"), true},
		{api.NewError(api.CodeServerError, "redirect leaves https"), true},
		{api.NewError(api.CodeNetworkError, "down"), false},
		{context.DeadlineExceeded, false},
		{errors.New("other"), false},
	} {
		if got := contentRefused(tc.err); got != tc.want {
			t.Errorf("contentRefused(%v) = %v", tc.err, got)
		}
	}
}
