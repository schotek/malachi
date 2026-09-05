// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// structure renders the MIME tree of raw as "type[child,child]", walking
// with go-message itself (mail.Reader flattens nested containers).
func structure(t *testing.T, raw []byte) string {
	t.Helper()
	root, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var walk func(e *message.Entity) string
	walk = func(e *message.Entity) string {
		ct, _, _ := e.Header.ContentType()
		mr := e.MultipartReader()
		if mr == nil {
			_, _ = io.Copy(io.Discard, e.Body)
			return ct
		}
		var kids []string
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			kids = append(kids, walk(p))
		}
		return ct + "[" + strings.Join(kids, ",") + "]"
	}
	return walk(root)
}

func png() Attachment {
	return Attachment{
		Filename: "logo.png", ContentType: "image/png", Size: 8, Inline: true, ContentID: "logo@malachi.local",
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\n")), nil },
	}
}

func pdf() Attachment {
	return Attachment{
		Filename: "příloha.pdf", ContentType: "application/pdf", Size: 9,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("%PDF-1.4\n")), nil },
	}
}

func TestBuildAlternative(t *testing.T) {
	long := strings.Repeat("dlouhý řádek s diakritikou ", 20)
	raw := build(t, BuildInput{From: alice, To: []api.Address{bob}, Subject: "rich",
		Text: "plain čeština\n" + long, HTML: "<p>rich <b>čeština</b></p><p>" + long + "</p>"})
	if got := structure(t, raw); got != "multipart/alternative[text/plain,text/html]" {
		t.Fatalf("structure = %s", got)
	}
	_, parts, bodies := readParts(t, raw)
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for i, want := range []string{"text/plain", "text/html"} {
		ih, ok := parts[i].Header.(*mail.InlineHeader)
		if !ok {
			t.Fatalf("part %d is %T, want an inline part", i, parts[i].Header)
		}
		ct, params, _ := ih.ContentType()
		if ct != want || params["charset"] != "utf-8" || ih.Get("Content-Transfer-Encoding") != "quoted-printable" {
			t.Errorf("part %d header: %s %v %q", i, ct, params, ih.Get("Content-Transfer-Encoding"))
		}
	}
	if !strings.HasPrefix(lf(bodies[0]), "plain čeština\n") || lf(bodies[1]) != "<p>rich <b>čeština</b></p><p>"+long+"</p>" {
		t.Errorf("bodies = %q / %q", bodies[0], bodies[1])
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 76 {
			t.Errorf("wire line longer than 76 bytes: %q", line)
		}
	}
	if got := PartIDs(BuildInput{HTML: "<p>x</p>"}); len(got) != 0 {
		t.Errorf("no attachments: ids = %v", got)
	}
}

func TestBuildRelatedInline(t *testing.T) {
	in := BuildInput{From: alice, To: []api.Address{bob}, Subject: "pic",
		Text: "see picture", HTML: `<p>see <img src="cid:logo@malachi.local"></p>`, Attachments: []Attachment{png()}}
	raw := build(t, in)
	if got := structure(t, raw); got != "multipart/alternative[text/plain,multipart/related[text/html,image/png]]" {
		t.Fatalf("structure = %s", got)
	}
	if got := PartIDs(in); len(got) != 1 || got[0] != "2.2" {
		t.Fatalf("ids = %v", got)
	}
	part, err := mime.ExtractPart(bytes.NewReader(raw), "2.2", mime.DefaultLimits(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if part.ContentType != "image/png" || !part.Inline || part.ContentID != "logo@malachi.local" || part.Filename != "logo.png" || string(part.Body) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("picture part = %+v", part)
	}
	html, err := mime.ExtractPart(bytes.NewReader(raw), "2.1", mime.DefaultLimits(), 0)
	if err != nil || html.ContentType != "text/html" || !strings.Contains(string(html.Body), "cid:logo@malachi.local") {
		t.Fatalf("html part = %+v, %v", html, err)
	}
	// go-message writes the canonical "Content-Id"; readers are case-blind.
	lower := strings.ToLower(string(raw))
	if !strings.Contains(lower, "content-id: <logo@malachi.local>") || !strings.Contains(lower, "content-disposition: inline; filename=logo.png") {
		t.Errorf("inline headers missing:\n%s", raw)
	}
	// The parser sees it as a message with HTML and an inline attachment,
	// nothing to download.
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.HasHTML || parsed.HasAttachments || len(parsed.Attachments) != 1 || !parsed.Attachments[0].Inline || parsed.Text != "see picture" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestBuildMixedWithAlternative(t *testing.T) {
	in := BuildInput{From: alice, To: []api.Address{bob}, Subject: "all",
		Text: "t", HTML: `<p><img src="cid:logo@malachi.local"></p>`, Attachments: []Attachment{png(), pdf()}}
	raw := build(t, in)
	if got := structure(t, raw); got != "multipart/mixed[multipart/alternative[text/plain,multipart/related[text/html,image/png]],application/pdf]" {
		t.Fatalf("structure = %s", got)
	}
	if got := strings.Join(PartIDs(in), ","); got != "1.2.2,2" {
		t.Fatalf("ids = %s", got)
	}
	for _, id := range []string{"1.1", "1.2.1", "1.2.2", "2"} {
		if _, err := mime.ExtractPart(bytes.NewReader(raw), id, mime.DefaultLimits(), 0); err != nil {
			t.Errorf("part %s: %v", id, err)
		}
	}
	file, _ := mime.ExtractPart(bytes.NewReader(raw), "2", mime.DefaultLimits(), 0)
	if file.ContentType != "application/pdf" || file.Inline || file.Filename != "příloha.pdf" {
		t.Errorf("file part = %+v", file)
	}

	// A file that says inline but has no HTML to sit in is a plain
	// attachment, and so is one without a Content-ID.
	plain := BuildInput{From: alice, To: []api.Address{bob}, Text: "t", Attachments: []Attachment{png()}}
	if got := structure(t, build(t, plain)); got != "multipart/mixed[text/plain,image/png]" {
		t.Errorf("inline without html: %s", got)
	}
	if got := PartIDs(plain); len(got) != 1 || got[0] != "2" {
		t.Errorf("inline without html: ids = %v", got)
	}
	noID := png()
	noID.ContentID = ""
	withHTML := BuildInput{From: alice, To: []api.Address{bob}, Text: "t", HTML: "<p>x</p>", Attachments: []Attachment{noID}}
	if got := structure(t, build(t, withHTML)); got != "multipart/mixed[multipart/alternative[text/plain,text/html],image/png]" {
		t.Errorf("inline without content id: %s", got)
	}
}

func TestBuildInlineBadContentID(t *testing.T) {
	bad := png()
	bad.ContentID = "not a msg id"
	_, err := buildErr(BuildInput{From: alice, To: []api.Address{bob}, HTML: "<p>x</p>", Attachments: []Attachment{bad}})
	if err == nil {
		t.Fatal("an invalid content id must fail the build")
	}
}

func buildErr(in BuildInput) ([]byte, error) {
	var buf bytes.Buffer
	err := BuildMessage(&buf, in)
	return buf.Bytes(), err
}
