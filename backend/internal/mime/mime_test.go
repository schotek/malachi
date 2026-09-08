// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testdata = "../../testdata/mime"

func readFile(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func parseFile(t *testing.T, name string) *Parsed {
	t.Helper()
	p, err := Parse(bytes.NewReader(readFile(t, name)), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	checkInvariants(t, p, DefaultLimits())
	return p
}

func parseString(t *testing.T, s string, limits Limits) *Parsed {
	t.Helper()
	p, err := Parse(strings.NewReader(s), limits)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkInvariants(t, p, limits.withDefaults())
	return p
}

func addr(name, address string) api.Address { return api.Address{Name: name, Address: address} }

func attachmentByPart(p *Parsed, partID string) (api.Attachment, bool) {
	for _, a := range p.Attachments {
		if a.PartID == partID {
			return a, true
		}
	}
	return api.Attachment{}, false
}

func hasProblem(p *Parsed, substr string) bool {
	for _, s := range p.Problems {
		if strings.Contains(strings.ToLower(s), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

func noControl(tb testing.TB, what, s string) {
	tb.Helper()
	if !utf8.ValidString(s) {
		tb.Errorf("%s: invalid UTF-8: %q", what, s)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			tb.Errorf("%s: control character %U in %q", what, r, s)
			return
		}
	}
}

func noControlExceptNLTab(tb testing.TB, what, s string) {
	tb.Helper()
	if !utf8.ValidString(s) {
		tb.Errorf("%s: invalid UTF-8", what)
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			tb.Errorf("%s: control character %U", what, r)
			return
		}
	}
}

func isPartID(s string) bool {
	if s == "" {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" || strings.TrimLeft(seg, "0123456789") != "" || seg[0] == '0' {
			return false
		}
	}
	return true
}

// checkInvariants asserts what must hold for every Parsed regardless of
// input; it is shared by the corpus table test and the fuzzers.
func checkInvariants(tb testing.TB, p *Parsed, limits Limits) {
	tb.Helper()
	if p == nil {
		tb.Fatal("nil Parsed")
	}
	if n := utf8.RuneCountInString(p.Snippet); n > limits.MaxSnippetRunes {
		tb.Errorf("snippet has %d runes", n)
	}
	noControl(tb, "subject", p.Subject)
	noControl(tb, "snippet", p.Snippet)
	noControl(tb, "message-id", p.MessageID)
	noControl(tb, "in-reply-to", p.InReplyTo)
	if strings.ContainsAny(p.MessageID+p.InReplyTo, " <>") {
		tb.Errorf("identifiers keep whitespace or brackets: %q %q", p.MessageID, p.InReplyTo)
	}
	for _, s := range []string{p.Subject, p.MessageID, p.InReplyTo, p.Snippet} {
		if len(s) > max(limits.MaxFieldBytes, 4*limits.MaxSnippetRunes) {
			tb.Errorf("field too long: %d bytes", len(s))
		}
	}
	if len(p.References) > limits.MaxReferences {
		tb.Errorf("%d references", len(p.References))
	}
	for _, r := range p.References {
		noControl(tb, "reference", r)
		if r == "" || strings.ContainsAny(r, " <>") {
			tb.Errorf("bad reference %q", r)
		}
	}
	for k, v := range p.Headers {
		noControl(tb, "header "+k, v)
		if v == "" {
			tb.Errorf("empty header value for %s", k)
		}
		found := false
		for _, c := range curatedHeaders {
			found = found || c == k
		}
		if !found {
			tb.Errorf("header %s is not curated", k)
		}
	}
	for _, list := range [][]api.Address{p.From, p.To, p.CC, p.BCC, p.ReplyTo} {
		if len(list) > maxAddresses {
			tb.Errorf("%d addresses", len(list))
		}
		for _, a := range list {
			noControl(tb, "address name", a.Name)
			noControl(tb, "address", a.Address)
			if a == (api.Address{}) {
				tb.Error("empty address kept")
			}
			if len(a.Name) > limits.MaxFieldBytes || len(a.Address) > limits.MaxFieldBytes {
				tb.Error("address field too long")
			}
		}
	}
	for _, s := range p.Problems {
		noControl(tb, "problem", s)
	}
	if len(p.Problems) > maxProblems+1 {
		tb.Errorf("%d problems", len(p.Problems))
	}
	if len(p.Attachments) > limits.MaxAttachments {
		tb.Errorf("%d attachments", len(p.Attachments))
	}
	nonInline := false
	for _, a := range p.Attachments {
		noControl(tb, "filename", a.Filename)
		noControl(tb, "content-id", a.ContentID)
		if a.Filename == "" || strings.ContainsAny(a.Filename, `/\`) || len(a.Filename) > safename.MaxBytes {
			tb.Errorf("bad filename %q", a.Filename)
		}
		if got := safename.Filename(a.Filename); got != a.Filename {
			tb.Errorf("safename not idempotent: %q -> %q", a.Filename, got)
		}
		if !isPartID(a.PartID) {
			tb.Errorf("bad part id %q", a.PartID)
		}
		if a.ContentType != normalizeMediaType(a.ContentType) || strings.HasPrefix(a.ContentType, "multipart/") {
			tb.Errorf("bad content type %q", a.ContentType)
		}
		if a.Size < 0 {
			tb.Errorf("negative size for %s", a.PartID)
		}
		if a.Inline && a.ContentID == "" {
			tb.Errorf("inline without content-id: %s", a.PartID)
		}
		if strings.ContainsAny(a.ContentID, "<> ") {
			tb.Errorf("bad content-id %q", a.ContentID)
		}
		nonInline = nonInline || !a.Inline
	}
	if p.HasAttachments != nonInline {
		tb.Errorf("HasAttachments = %v, want %v", p.HasAttachments, nonInline)
	}
	if int64(len(p.Text)) > limits.MaxTextBytes+64 {
		tb.Errorf("text is %d bytes", len(p.Text))
	}
	if int64(len(p.RawHTML)) > limits.MaxTextBytes+64 {
		tb.Errorf("html is %d bytes", len(p.RawHTML))
	}
	noControlExceptNLTab(tb, "text", p.Text)
	noControlExceptNLTab(tb, "html", p.RawHTML)
	if strings.Contains(p.Text, "\r") {
		tb.Error("text contains CR")
	}
	if p.HasHTML != (p.HTMLPartID != "") {
		tb.Errorf("HasHTML = %v but HTMLPartID = %q", p.HasHTML, p.HTMLPartID)
	}
	if p.TextPartID != "" && !isPartID(p.TextPartID) || p.HTMLPartID != "" && !isPartID(p.HTMLPartID) {
		tb.Errorf("bad body part ids %q %q", p.TextPartID, p.HTMLPartID)
	}
	if p.TextPartID == "" && p.HasHTML {
		// Text was derived from HTML: it must never carry markup.
		if strings.Contains(strings.ToLower(p.Text), "<script") {
			tb.Errorf("HTML-derived text contains <script: %q", p.Text)
		}
		if !strings.ContainsRune(p.RawHTML, '&') && !onlyText(p.Text) {
			tb.Errorf("HTML-derived text still holds a tag: %q", p.Text)
		}
	}
	if p.Snippet != Snippet(p.Text, limits.MaxSnippetRunes) {
		tb.Error("snippet is not derived from text")
	}
}

func TestSimpleText(t *testing.T) {
	p := parseFile(t, "simple-text.eml")
	if p.Subject != "Hello" {
		t.Errorf("subject = %q", p.Subject)
	}
	wantFrom := []api.Address{addr("Alice Example", "alice@example.org")}
	if fmt.Sprint(p.From) != fmt.Sprint(wantFrom) {
		t.Errorf("from = %v", p.From)
	}
	wantTo := []api.Address{addr("Bob", "bob@example.net"), addr("Carol, C.", "carol@example.net")}
	if fmt.Sprint(p.To) != fmt.Sprint(wantTo) {
		t.Errorf("to = %v", p.To)
	}
	if fmt.Sprint(p.CC) != fmt.Sprint([]api.Address{addr("", "dave@example.net")}) {
		t.Errorf("cc = %v", p.CC)
	}
	if fmt.Sprint(p.ReplyTo) != fmt.Sprint([]api.Address{addr("Alice Reply", "reply@example.org")}) {
		t.Errorf("reply-to = %v", p.ReplyTo)
	}
	if p.BCC != nil {
		t.Errorf("bcc = %v", p.BCC)
	}
	want := time.Date(2026, 9, 2, 10, 15, 0, 0, time.FixedZone("", 2*3600))
	if !p.Date.Equal(want) {
		t.Errorf("date = %v", p.Date)
	}
	if p.MessageID != "simple-1@example.org" || p.InReplyTo != "root-0@example.org" {
		t.Errorf("ids = %q %q", p.MessageID, p.InReplyTo)
	}
	if fmt.Sprint(p.References) != "[root-0@example.org mid-1@example.org]" {
		t.Errorf("references = %v", p.References)
	}
	if p.Headers["List-Unsubscribe"] != "<mailto:unsub@example.org>" || p.Headers["X-Mailer"] != "TestMailer 1.0" {
		t.Errorf("headers = %v", p.Headers)
	}
	if _, ok := p.Headers["X-Not-Curated"]; ok || len(p.Headers) != 2 {
		t.Errorf("headers = %v", p.Headers)
	}
	if p.Text != "Hello Bob,\n\nthis is a simple message.\n> quoted line\nBye" {
		t.Errorf("text = %q", p.Text)
	}
	if p.Snippet != "Hello Bob, this is a simple message. Bye" {
		t.Errorf("snippet = %q", p.Snippet)
	}
	if p.TextPartID != "1" || p.HTMLPartID != "" || p.HasHTML || p.RawHTML != "" {
		t.Errorf("body parts = %q %q %v", p.TextPartID, p.HTMLPartID, p.HasHTML)
	}
	if len(p.Attachments) != 0 || p.HasAttachments || p.Truncated || len(p.Problems) != 0 {
		t.Errorf("attachments=%v truncated=%v problems=%v", p.Attachments, p.Truncated, p.Problems)
	}
}

func TestAlternative(t *testing.T) {
	p := parseFile(t, "alternative.eml")
	if p.Subject != "Předmět" {
		t.Errorf("subject = %q", p.Subject)
	}
	if p.Text != "Ahoj, tohle je český text." {
		t.Errorf("text = %q", p.Text)
	}
	if !p.HasHTML || !strings.Contains(p.RawHTML, "<b>český</b>") {
		t.Errorf("html = %q", p.RawHTML)
	}
	if p.TextPartID != "1" || p.HTMLPartID != "2" {
		t.Errorf("part ids = %q %q", p.TextPartID, p.HTMLPartID)
	}
	if len(p.Attachments) != 0 || p.HasAttachments {
		t.Errorf("attachments = %v", p.Attachments)
	}
	if p.Snippet != "Ahoj, tohle je český text." {
		t.Errorf("snippet = %q", p.Snippet)
	}
}

func TestHTMLOnly(t *testing.T) {
	p := parseFile(t, "html-only.eml")
	if !p.HasHTML || p.HTMLPartID != "1" || p.TextPartID != "" {
		t.Errorf("part ids = %q %q", p.TextPartID, p.HTMLPartID)
	}
	want := "Welcome & hello\n\nFirst paragraph with line break in source.\n\none\ntwo\n\nCopyright © 2026 — done"
	if p.Text != want {
		t.Errorf("text = %q, want %q", p.Text, want)
	}
	for _, bad := range []string{"<", "alert", "color", "Ignored title"} {
		if strings.Contains(p.Text, bad) {
			t.Errorf("text contains %q", bad)
		}
	}
	if p.Snippet != "Welcome & hello First paragraph with line break in source. one two Copyright © 2026 — done" {
		t.Errorf("snippet = %q", p.Snippet)
	}
}

func TestMixedAttachments(t *testing.T) {
	p := parseFile(t, "mixed-attachments.eml")
	if !p.HasHTML || p.HTMLPartID != "1.1" || p.TextPartID != "" {
		t.Errorf("part ids = %q %q", p.TextPartID, p.HTMLPartID)
	}
	if p.Text != "See the logo." {
		t.Errorf("text = %q", p.Text)
	}
	if len(p.Attachments) != 2 || !p.HasAttachments {
		t.Fatalf("attachments = %+v", p.Attachments)
	}
	png := p.Attachments[0]
	if png.PartID != "1.2" || png.Filename != "logo.png" || png.ContentType != "image/png" || png.Size != 64 || !png.Inline || png.ContentID != "img1@example.org" {
		t.Errorf("png = %+v", png)
	}
	pdf := p.Attachments[1]
	if pdf.PartID != "2" || pdf.Filename != "příloha.pdf" || pdf.ContentType != "application/pdf" || pdf.Size != 1000 || pdf.Inline || pdf.ContentID != "" {
		t.Errorf("pdf = %+v", pdf)
	}
	if p.Truncated || len(p.Problems) != 0 {
		t.Errorf("truncated=%v problems=%v", p.Truncated, p.Problems)
	}
}

func TestNestedRFC822(t *testing.T) {
	p := parseFile(t, "nested-rfc822.eml")
	if p.Text != "Forwarding the report." {
		t.Errorf("text = %q", p.Text)
	}
	if len(p.Attachments) != 1 {
		t.Fatalf("attachments = %+v", p.Attachments)
	}
	a := p.Attachments[0]
	if a.PartID != "2" || a.ContentType != "message/rfc822" || a.Filename != "report.eml" || a.Inline {
		t.Errorf("attachment = %+v", a)
	}
	raw := readFile(t, "nested-rfc822.eml")
	inner := raw[bytes.Index(raw, []byte("From: carol")):bytes.Index(raw, []byte("\r\n--outer--"))]
	if a.Size != int64(len(inner)) {
		t.Errorf("size = %d, want %d", a.Size, len(inner))
	}
	if strings.Contains(p.Text, "Inner text") || p.HasHTML {
		t.Error("descended into message/rfc822")
	}
}

func TestRFC822Bomb(t *testing.T) {
	p := parseFile(t, "rfc822-bomb.eml")
	if len(p.Attachments) != 1 || p.Attachments[0].ContentType != "message/rfc822" || p.Attachments[0].PartID != "1" {
		t.Errorf("attachments = %+v", p.Attachments)
	}
	if p.Attachments[0].Filename != "attachment-1.eml" {
		t.Errorf("filename = %q", p.Attachments[0].Filename)
	}
	if p.Text != "" || p.Truncated {
		t.Errorf("text = %q truncated = %v", p.Text, p.Truncated)
	}
}

func TestManyParts(t *testing.T) {
	p := parseFile(t, "many-parts.eml")
	if p.Text != "part 1" || p.TextPartID != "1" {
		t.Errorf("text = %q (%s)", p.Text, p.TextPartID)
	}
	if !p.Truncated || len(p.Attachments) != DefaultLimits().MaxAttachments {
		t.Errorf("truncated = %v, %d attachments", p.Truncated, len(p.Attachments))
	}
	if a := p.Attachments[0]; a.PartID != "2" || a.Filename != "attachment-2.txt" || a.ContentType != "text/plain" {
		t.Errorf("attachment = %+v", a)
	}
	if !hasProblem(p, "parts") || !hasProblem(p, "attachments") {
		t.Errorf("problems = %v", p.Problems)
	}
}

func TestDeepNesting(t *testing.T) {
	p := parseFile(t, "deep-nesting.eml")
	if !p.Truncated || p.Text != "" || !hasProblem(p, "nesting") {
		t.Errorf("truncated=%v text=%q problems=%v", p.Truncated, p.Text, p.Problems)
	}
	limits := DefaultLimits()
	limits.MaxDepth = 60
	q, err := Parse(bytes.NewReader(readFile(t, "deep-nesting.eml")), limits)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, q, limits)
	if q.Text != "deep" || q.Truncated {
		t.Errorf("text = %q truncated = %v", q.Text, q.Truncated)
	}
	if want := strings.TrimSuffix(strings.Repeat("1.", 50), "."); q.TextPartID != want {
		t.Errorf("part id = %q", q.TextPartID)
	}
}

func TestMissingBoundary(t *testing.T) {
	p := parseFile(t, "missing-boundary.eml")
	if len(p.Problems) == 0 || !hasProblem(p, "boundary") {
		t.Errorf("problems = %v", p.Problems)
	}
	if p.Text != "" || len(p.Attachments) != 0 || p.Subject != "No boundary" {
		t.Errorf("text=%q attachments=%v subject=%q", p.Text, p.Attachments, p.Subject)
	}
}

func TestBadCharset(t *testing.T) {
	p := parseFile(t, "bad-charset.eml")
	if p.Text != "Příliš žluťoučký kůň" {
		t.Errorf("text = %q", p.Text)
	}
	if !p.HasHTML || !strings.Contains(p.RawHTML, "<p>bogus</p>") {
		t.Errorf("html = %q", p.RawHTML)
	}
	if !hasProblem(p, "charset") {
		t.Errorf("problems = %v", p.Problems)
	}
}

func TestEightBitIn7bit(t *testing.T) {
	p := parseFile(t, "eightbit-in-7bit.eml")
	if p.Text != "Čeština ok\n� lone byte" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestHugeHeader(t *testing.T) {
	p := parseFile(t, "huge-header.eml")
	if !strings.HasPrefix(p.Subject, "folded xxxx") || len(p.Subject) != DefaultLimits().MaxFieldBytes || strings.Contains(p.Subject, "  ") {
		t.Errorf("subject = %d bytes, %q…", len(p.Subject), p.Subject[:20])
	}
	if p.Text != "body" {
		t.Errorf("text = %q", p.Text)
	}
	limits := DefaultLimits()
	limits.MaxHeaderBytes = 8 << 10
	if _, err := Parse(bytes.NewReader(readFile(t, "huge-header.eml")), limits); err == nil {
		t.Error("expected an error for a header over MaxHeaderBytes")
	}
}

func TestManyHeaders(t *testing.T) {
	p := parseFile(t, "many-headers.eml")
	if p.Subject != "Many" || p.Text != "body" || len(p.Headers) != 0 {
		t.Errorf("subject=%q text=%q headers=%v", p.Subject, p.Text, p.Headers)
	}
}

func TestCRLFInjection(t *testing.T) {
	p := parseFile(t, "crlf-injection.eml")
	if p.Subject != "HiX-Injected: yes" {
		t.Errorf("subject = %q", p.Subject)
	}
	if fmt.Sprint(p.From) != fmt.Sprint([]api.Address{addr("Alice", "alice@example.org")}) {
		t.Errorf("from = %v", p.From)
	}
	// net/mail rejects the field; the salvage path decodes the encoded
	// word, strips the CR/LF and keeps the angle-addr.
	if len(p.To) != 1 || p.To[0].Address != "bob@example.net" || p.To[0].Name != "BobBcc: evil@example.org" {
		t.Errorf("to = %v", p.To)
	}
	if p.BCC != nil {
		t.Errorf("injected bcc = %v", p.BCC)
	}
	if _, ok := p.Headers["X-Injected"]; ok {
		t.Error("injected header appeared")
	}
}

func TestInvalidBase64(t *testing.T) {
	p := parseFile(t, "invalid-base64.eml")
	if p.Text != "see attachment" {
		t.Errorf("text = %q", p.Text)
	}
	if len(p.Attachments) != 1 || p.Attachments[0].Filename != "data.bin" {
		t.Fatalf("attachments = %+v", p.Attachments)
	}
	if !hasProblem(p, "base64") {
		t.Errorf("problems = %v", p.Problems)
	}
}

func TestEvilFilename(t *testing.T) {
	p := parseFile(t, "evil-filename.eml")
	want := map[string]string{
		"1": "passwd",
		"2": "evilname.txt",
		"3": strings.Repeat("a", safename.MaxBytes-4) + ".bin",
		"4": "xy.txt",
		"5": "attachment-5.png",
		"6": "attachment-6",
	}
	if len(p.Attachments) != len(want) {
		t.Fatalf("attachments = %+v", p.Attachments)
	}
	for id, name := range want {
		a, ok := attachmentByPart(p, id)
		if !ok || a.Filename != name {
			t.Errorf("part %s: filename = %q, want %q", id, a.Filename, name)
		}
	}
	if !p.HasAttachments || p.Truncated {
		t.Errorf("hasAttachments=%v truncated=%v", p.HasAttachments, p.Truncated)
	}
}

func TestEmptyBody(t *testing.T) {
	p := parseFile(t, "empty-body.eml")
	if p.Subject != "Empty" || p.Text != "" || p.Snippet != "" || p.TextPartID != "1" {
		t.Errorf("parsed = %+v", p)
	}
	if len(p.Attachments) != 0 || p.HasHTML || p.Truncated || len(p.Problems) != 0 {
		t.Errorf("parsed = %+v", p)
	}
}

func TestTruncated(t *testing.T) {
	p := parseFile(t, "truncated.eml")
	if p.Text != "first" {
		t.Errorf("text = %q", p.Text)
	}
	if len(p.Attachments) != 1 || p.Attachments[0].Filename != "cut.bin" || p.Attachments[0].Size > int64(len("partial data")) {
		t.Errorf("attachments = %+v", p.Attachments)
	}
	if len(p.Problems) == 0 {
		t.Error("expected problems for a truncated multipart")
	}
}

func TestNoEnvelope(t *testing.T) {
	p := parseFile(t, "no-envelope.eml")
	if p.Subject != "" || p.From != nil || p.To != nil || !p.Date.IsZero() || p.MessageID != "" || len(p.Headers) != 0 {
		t.Errorf("envelope = %+v", p)
	}
	if p.Text != "Just a body." || p.Snippet != "Just a body." {
		t.Errorf("text = %q", p.Text)
	}
	if len(p.Problems) != 0 {
		t.Errorf("problems = %v", p.Problems)
	}
}

func TestBrokenQP(t *testing.T) {
	p := parseFile(t, "broken-qp.eml")
	if !strings.HasPrefix(p.Text, "Good linecontinued č ok\n") {
		t.Errorf("text = %q", p.Text)
	}
	if p.Subject == "" {
		t.Error("subject lost")
	}
}

func TestHeaderOnlyMessages(t *testing.T) {
	p := parseString(t, "Subject: only\r\nFrom: a@example.org", DefaultLimits())
	if p.Subject != "only" || len(p.From) != 1 || p.Text != "" || len(p.Problems) != 0 {
		t.Errorf("parsed = %+v", p)
	}
	p = parseString(t, "Subject: lf\nFrom: a@example.org\n\nbody\n", DefaultLimits())
	if p.Subject != "lf" || p.Text != "body" {
		t.Errorf("parsed = %+v", p)
	}
	p = parseString(t, "Subject: trailing\r\n\r\n", DefaultLimits())
	if p.Subject != "trailing" || p.Text != "" {
		t.Errorf("parsed = %+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"no colon":      "this is not a header\r\n\r\nbody",
		"leading space": " Subject: x\r\n\r\n",
		"bad key":       "Sub ject: x\r\n\r\n",
	} {
		if _, err := Parse(strings.NewReader(in), DefaultLimits()); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDateFallbacks(t *testing.T) {
	cases := map[string]time.Time{
		"Tue, 2 Sep 2026 10:15:00 +0200 (CEST)": time.Date(2026, 9, 2, 8, 15, 0, 0, time.UTC),
		"2 Sep 2026 10:15 +0200":                time.Date(2026, 9, 2, 8, 15, 0, 0, time.UTC),
		"2026-09-02 10:15:00":                   time.Date(2026, 9, 2, 10, 15, 0, 0, time.UTC),
		"Tue Sep 2 10:15:00 2026":               time.Date(2026, 9, 2, 10, 15, 0, 0, time.UTC),
		"garbage":                               {},
		"":                                      {},
	}
	for in, want := range cases {
		p := parseString(t, "Date: "+in+"\r\n\r\n", DefaultLimits())
		if !p.Date.Equal(want) {
			t.Errorf("Date %q = %v, want %v", in, p.Date, want)
		}
	}
}

func TestAddressFallback(t *testing.T) {
	p := parseString(t, "From: not an address at all <<>>\r\nTo: undisclosed-recipients:;\r\n\r\n", DefaultLimits())
	if len(p.From) != 1 || p.From[0].Address != "" || !strings.Contains(p.From[0].Name, "not an address") {
		t.Errorf("from = %v", p.From)
	}
	if p.To != nil {
		t.Errorf("to = %v", p.To)
	}
	if !hasProblem(p, "from") {
		t.Errorf("problems = %v", p.Problems)
	}
}

func TestReferencesFallbackAndCap(t *testing.T) {
	var refs []string
	for i := range 60 {
		refs = append(refs, fmt.Sprintf("<r%d@example.org>", i))
	}
	p := parseString(t, "References: "+strings.Join(refs, " ")+"\r\nIn-Reply-To: broken id without brackets\r\n\r\n", DefaultLimits())
	// The cap keeps the tail: the nearest ancestors are what threading links on.
	if n := DefaultLimits().MaxReferences; len(p.References) != n || p.References[0] != "r10@example.org" || p.References[n-1] != "r59@example.org" {
		t.Errorf("references = %v", p.References)
	}
	if !hasProblem(p, "references") {
		t.Errorf("problems = %v", p.Problems)
	}
	if p.InReplyTo != "broken" {
		t.Errorf("in-reply-to = %q", p.InReplyTo)
	}
}

// The threading samples in testdata: what a hostile or broken header
// leaves the linker with.
func TestThreadingHeaders(t *testing.T) {
	limits := DefaultLimits()
	p := parseFile(t, "thread-self-reference.eml")
	if p.MessageID != "self@example.org" || p.InReplyTo != "self@example.org" || len(p.References) != 1 || p.References[0] != "self@example.org" {
		t.Errorf("self-reference: id %q in-reply-to %q refs %v", p.MessageID, p.InReplyTo, p.References)
	}

	p = parseFile(t, "thread-references-flood.eml")
	if len(p.References) != limits.MaxReferences || p.References[len(p.References)-1] != "flood-4999@example.org" || p.References[0] != "flood-4950@example.org" {
		t.Errorf("flood: %d references, first %q, last %q", len(p.References), p.References[0], p.References[len(p.References)-1])
	}
	if !hasProblem(p, "references") {
		t.Errorf("flood problems = %v", p.Problems)
	}

	p = parseFile(t, "thread-references-garbage.eml")
	for _, r := range p.References {
		if r == "" || strings.ContainsAny(r, " <>\r\n\t") {
			t.Errorf("garbage reference %q kept", r)
		}
	}
	if len(p.References) == 0 || p.InReplyTo == "" {
		t.Errorf("garbage: nothing salvaged: %v / %q", p.References, p.InReplyTo)
	}

	p = parseFile(t, "thread-in-reply-to-list.eml")
	if p.InReplyTo != "first@example.org" {
		t.Errorf("in-reply-to list: %q", p.InReplyTo)
	}

	p = parseFile(t, "thread-no-message-id.eml")
	if p.MessageID != "" || len(p.References) != 2 {
		t.Errorf("no message-id: id %q refs %v", p.MessageID, p.References)
	}
}

func TestParseReferences(t *testing.T) {
	limits := DefaultLimits()
	cases := []struct {
		name, in string
		want     []string
	}{
		{"plain", "References: <a@x> <b@x>\r\n\r\n", []string{"a@x", "b@x"}},
		{"no blank line", "References: <a@x>", []string{"a@x"}},
		{"folded", "Subject: x\r\nReferences: <a@x>\r\n <b@x>\r\n\t<c@x>\r\n\r\n", []string{"a@x", "b@x", "c@x"}},
		{"missing", "Subject: x\r\n\r\n", nil},
		{"empty", "", nil},
		{"garbage", "References: <a@x b@x> junk\r\n\r\n", []string{"a@x", "b@x", "junk"}},
		{"duplicates", "References: <a@x> <b@x> <a@x>\r\n\r\n", []string{"a@x", "b@x"}},
	}
	for _, c := range cases {
		if got := ParseReferences(strings.NewReader(c.in), limits); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseReferences = %v, want %v", c.name, got, c.want)
		}
	}
	var many []string
	for i := range 70 {
		many = append(many, fmt.Sprintf("<r%d@x>", i))
	}
	got := ParseReferences(strings.NewReader("References: "+strings.Join(many, " ")+"\r\n\r\n"), limits)
	if len(got) != limits.MaxReferences || got[0] != "r20@x" || got[len(got)-1] != "r69@x" {
		t.Errorf("capped = %d, first %q", len(got), got[0])
	}
	huge := "References: " + strings.Repeat("<a@x> ", 100000) + "\r\n\r\n"
	if got := ParseReferences(strings.NewReader(huge), limits); len(got) > limits.MaxReferences {
		t.Errorf("huge block: %d references", len(got))
	}
}

func TestTextPartAsAttachment(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=notes.txt\r\n\r\nnotes\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n--b--\r\n"
	p := parseString(t, msg, DefaultLimits())
	if p.Text != "body" || p.TextPartID != "2" {
		t.Errorf("text = %q (%s)", p.Text, p.TextPartID)
	}
	if len(p.Attachments) != 1 || p.Attachments[0].Filename != "notes.txt" || p.Attachments[0].PartID != "1" {
		t.Errorf("attachments = %+v", p.Attachments)
	}
}

func TestUnparsableContentType(t *testing.T) {
	msg := "Content-Type: text/plain; charset\r\n\r\nstill text\r\n"
	p := parseString(t, msg, DefaultLimits())
	if p.Text != "still text" {
		t.Errorf("text = %q", p.Text)
	}
	msg = "Content-Type: Multipart/Mixed; boundary\r\n\r\nopaque\r\n"
	p = parseString(t, msg, DefaultLimits())
	if len(p.Attachments) != 1 || p.Attachments[0].ContentType != "application/octet-stream" {
		t.Errorf("attachments = %+v", p.Attachments)
	}
}

func TestCorpusInvariants(t *testing.T) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		seen++
		t.Run(e.Name(), func(t *testing.T) {
			data := readFile(t, e.Name())
			start := time.Now()
			p, err := Parse(bytes.NewReader(data), DefaultLimits())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("took %v", d)
			}
			checkInvariants(t, p, DefaultLimits())
			if p.TextPartID == "" && p.HasHTML && strings.Contains(p.Text, "<") {
				t.Errorf("HTML-derived text contains '<': %q", p.Text)
			}
			// Parsing the same bytes again must be deterministic.
			q, _ := Parse(bytes.NewReader(data), DefaultLimits())
			if fmt.Sprintf("%+v", p) != fmt.Sprintf("%+v", q) {
				t.Error("parse is not deterministic")
			}
		})
	}
	if seen < 20 {
		t.Errorf("only %d corpus files", seen)
	}
}

// Generated pathological inputs: too large to commit.

func TestGeneratedManyParts(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("Subject: many\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n")
	for i := range 5000 {
		fmt.Fprintf(&b, "--b\r\nContent-Type: application/octet-stream\r\n\r\n%d\r\n", i)
	}
	b.WriteString("--b--\r\n")
	start := time.Now()
	p := parseString(t, b.String(), DefaultLimits())
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if !p.Truncated || len(p.Attachments) != DefaultLimits().MaxAttachments {
		t.Errorf("truncated=%v attachments=%d", p.Truncated, len(p.Attachments))
	}
}

func TestGeneratedManyHeaders(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("Subject: many\r\n")
	for range 20000 {
		b.WriteString("X-A: 1\r\n")
	}
	b.WriteString("\r\nbody\r\n")
	start := time.Now()
	p := parseString(t, b.String(), DefaultLimits())
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if p.Subject != "many" || p.Text != "body" {
		t.Errorf("subject=%q text=%q", p.Subject, p.Text)
	}
	// Ten times more exceeds MaxHeaderBytes: must fail quickly, not hang.
	b.Reset()
	b.WriteString("Subject: many\r\n")
	for range 200000 {
		b.WriteString("X-A: 1\r\n")
	}
	b.WriteString("\r\nbody\r\n")
	start = time.Now()
	if _, err := Parse(&b, DefaultLimits()); err == nil {
		t.Error("expected an error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestGeneratedHugeHeader(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("Subject: huge\r\n")
	for range 4000 {
		b.WriteString(" " + strings.Repeat("x", 78) + "\r\n")
	}
	b.WriteString("\r\nbody\r\n")
	if b.Len() < 300<<10 {
		t.Fatalf("header only %d bytes", b.Len())
	}
	start := time.Now()
	_, err := Parse(&b, DefaultLimits())
	if err == nil {
		t.Error("expected an error for a 300 KiB header")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestGeneratedDeepNesting(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("Subject: deep\r\n")
	for i := range 200 {
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=b%d\r\n\r\n--b%d\r\n", i, i)
	}
	b.WriteString("Content-Type: text/plain\r\n\r\ndeep\r\n")
	for i := 199; i >= 0; i-- {
		fmt.Fprintf(&b, "--b%d--\r\n", i)
	}
	p := parseString(t, b.String(), DefaultLimits())
	if !p.Truncated || p.Text != "" {
		t.Errorf("truncated=%v text=%q", p.Truncated, p.Text)
	}
}

func TestGeneratedRFC822Bomb(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("Subject: bomb\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n")
	for range 500 {
		b.WriteString("Content-Type: message/rfc822\r\n\r\n")
	}
	b.WriteString("Content-Type: text/plain\r\n\r\nbottom\r\n--b--\r\n")
	p := parseString(t, b.String(), DefaultLimits())
	if len(p.Attachments) != 1 || p.Truncated || p.Text != "" {
		t.Errorf("attachments=%d truncated=%v text=%q", len(p.Attachments), p.Truncated, p.Text)
	}
}

// repeatReader yields n copies of a byte without allocating them.
type repeatReader struct {
	c byte
	n int64
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		p[i] = r.c
	}
	r.n -= int64(len(p))
	return len(p), nil
}

func TestGeneratedLargeBodies(t *testing.T) {
	limits := DefaultLimits()
	// A 3 MiB text part is capped at MaxTextBytes; the 3 MiB attachment
	// after it is still sized exactly, proving the stream stays in sync.
	head := "Subject: big\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\n"
	mid := "\r\n--b\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=big.bin\r\n\r\n"
	tail := "\r\n--b--\r\n"
	r := io.MultiReader(strings.NewReader(head), &repeatReader{'a', 3 << 20}, strings.NewReader(mid), &repeatReader{'b', 3 << 20}, strings.NewReader(tail))
	p, err := Parse(r, limits)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, p, limits)
	if int64(len(p.Text)) != limits.MaxTextBytes || !p.Truncated {
		t.Errorf("text = %d bytes, truncated = %v", len(p.Text), p.Truncated)
	}
	if len(p.Attachments) != 1 || p.Attachments[0].Size != 3<<20 {
		t.Errorf("attachments = %+v", p.Attachments)
	}
	if utf8.RuneCountInString(p.Snippet) != limits.MaxSnippetRunes {
		t.Errorf("snippet = %d runes", utf8.RuneCountInString(p.Snippet))
	}

	// Input over MaxInputBytes is cut off and flagged.
	r = io.MultiReader(strings.NewReader(head), &repeatReader{'a', 10}, strings.NewReader(mid), &repeatReader{'b', MaxInputBytes + 1024}, strings.NewReader(tail))
	p, err = Parse(r, limits)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, p, limits)
	if !p.Truncated || !hasProblem(p, "MaxInputBytes") || len(p.Attachments) != 1 {
		t.Errorf("truncated=%v problems=%v attachments=%d", p.Truncated, p.Problems, len(p.Attachments))
	}
}

func TestPartID(t *testing.T) {
	cases := map[string][]int{"1": nil, "2": {1}, "2.1": {1, 0}, "1.3.2": {0, 2, 1}}
	for want, path := range cases {
		if got := partID(path); got != want {
			t.Errorf("partID(%v) = %q, want %q", path, got, want)
		}
	}
}

func TestNormalizeMediaType(t *testing.T) {
	cases := map[string]string{
		"text/plain":                    "text/plain",
		"Text/HTML; charset=utf-8":      "text/html",
		"application/pdf ":              "application/pdf",
		"image/svg+xml":                 "image/svg+xml",
		"":                              "application/octet-stream",
		"text":                          "application/octet-stream",
		"text/":                         "application/octet-stream",
		"text/pl ain":                   "application/octet-stream",
		"text/plain\x00":                "application/octet-stream",
		"a/" + strings.Repeat("b", 200): "application/octet-stream",
	}
	for in, want := range cases {
		if got := normalizeMediaType(in); got != want {
			t.Errorf("normalizeMediaType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanField(t *testing.T) {
	cases := map[string]string{
		"  plain  ":                      "plain",
		"a\r\nb":                         "ab",
		"a \r\n b":                       "a b",
		"tab\tsep":                       "tab sep",
		"nul\x00byte":                    "nulbyte",
		"c1\u0085ctrl":                   "c1ctrl",
		"multi   space here":             "multi space here",
		"bad\xffutf8":                    "bad�utf8",
		strings.Repeat("ž", 3000):        strings.Repeat("ž", 1024),
		strings.Repeat("a", 2047) + "žž": strings.Repeat("a", 2047),
	}
	for in, want := range cases {
		if got := cleanField(in, 2048); got != want {
			t.Errorf("cleanField(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanField("no cap "+strings.Repeat("x", 5000), 0); len(got) != 5007 {
		t.Errorf("uncapped length = %d", len(got))
	}
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"a\r\nb\rc\nd":       "a\nb\nc\nd",
		"tab\tkept":          "tab\tkept",
		"nul\x00gone\x1b[0m": "nulgone[0m",
		"\xff\xfe":           "�",
		"clean text\n":       "clean text\n",
		"del\x7fcharx":      "delcharx",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnippet(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Hello\n\n> quoted\n  world  \n>> more\nend", 200, "Hello world end"},
		{"a\tb c", 200, "a b c"},
		{"Příliš žluťoučký kůň", 6, "Příliš"},
		{"Příliš žluťoučký kůň", 7, "Příliš"},
		{"Příliš žluťoučký kůň", 8, "Příliš ž"},
		{"one two", 4, "one"},
		{"one two", 0, ""},
		{"", 10, ""},
		{"> only quotes\n>", 10, ""},
		{"x\x01y\x7fz", 10, "xyz"},
	}
	for _, c := range cases {
		if got := Snippet(c.in, c.max); got != c.want {
			t.Errorf("Snippet(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".eml" {
			f.Add(readFile(f, e.Name()))
		}
	}
	f.Add([]byte("Subject: =?utf-8?q?a=0D=0Ab?=\r\nContent-Type: multipart/mixed; boundary=\r\n\r\n--\r\n"))
	limits := DefaultLimits()
	limits.MaxTextBytes = 64 << 10 // keep each iteration cheap
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(bytes.NewReader(data), limits)
		if err != nil {
			if p != nil {
				t.Error("error with non-nil result")
			}
			return
		}
		checkInvariants(t, p, limits)
	})
}
