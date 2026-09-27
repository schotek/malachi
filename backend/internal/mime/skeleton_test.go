// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// skeletonFallback pins the corpus messages Skeleton refuses whatever is
// omitted, with the reason; every other fixture must reduce.
var skeletonFallback = map[string]string{
	"deep-nesting.eml":                "nesting deeper than MaxDepth",
	"many-parts.eml":                  "more than MaxParts entities",
	"missing-boundary.eml":            "a multipart without a boundary",
	"truncated.eml":                   "no close delimiter: the part ends at EOF",
	"skeleton-unterminated-quote.eml": "the parameter error loses the boundary",
	"skeleton-signed.eml":             "multipart/signed",
	"skeleton-encrypted.eml":          "multipart/encrypted",
}

// skeletonOf runs Skeleton over raw and returns what it wrote.
func skeletonOf(raw []byte, omit map[string]bool, limits Limits) ([]byte, []string, error) {
	var out bytes.Buffer
	omitted, err := Skeleton(bytes.NewReader(raw), &out, omit, limits)
	return out.Bytes(), omitted, err
}

func setOf(ids ...string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// leafIDs lists the leaves Parse names: the attachments, then the bodies.
func leafIDs(p *Parsed) []string {
	var ids []string
	for _, a := range p.Attachments {
		ids = append(ids, a.PartID)
	}
	for _, id := range []string{p.TextPartID, p.HTMLPartID} {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// checkKeptParts asserts that every leaf of orig that was not omitted has
// the same decoded content in the skeleton, and every omitted one is empty.
func checkKeptParts(t *testing.T, raw, skel []byte, p *Parsed, omit map[string]bool) {
	t.Helper()
	for _, id := range leafIDs(p) {
		got, gerr := ExtractPart(bytes.NewReader(skel), id, DefaultLimits(), MaxInputBytes)
		if omit[id] {
			if gerr != nil || len(got.Body) != 0 {
				t.Errorf("omitted part %s: %v, %d bytes", id, gerr, len(got.Body))
			}
			continue
		}
		want, werr := ExtractPart(bytes.NewReader(raw), id, DefaultLimits(), MaxInputBytes)
		if werr != nil || gerr != nil {
			// A broken encoding breaks the same way in both.
			if werr == nil || gerr == nil || werr.Error() != gerr.Error() {
				t.Errorf("part %s: original %v, skeleton %v", id, werr, gerr)
			}
			continue
		}
		if !bytes.Equal(want.Body, got.Body) || want.ContentType != got.ContentType || want.Filename != got.Filename || want.ContentID != got.ContentID {
			t.Errorf("part %s differs: %d bytes, skeleton %d", id, len(want.Body), len(got.Body))
		}
	}
}

// checkSkeleton asserts everything a successful skeleton promises for an
// omit set that holds only attachments.
func checkSkeleton(t *testing.T, raw, skel []byte, omit map[string]bool, omitted []string) {
	t.Helper()
	if len(skel) > 2*len(raw)+64 {
		t.Errorf("skeleton of %d bytes from %d", len(skel), len(raw))
	}
	if !sameSet(omitted, omit) {
		t.Errorf("omitted %v, asked for %v", omitted, omit)
	}
	orig, err := Parse(bytes.NewReader(raw), DefaultLimits())
	if err != nil {
		t.Fatalf("parse original: %v", err)
	}
	p, err := Parse(bytes.NewReader(skel), DefaultLimits())
	if err != nil {
		t.Fatalf("parse skeleton: %v\n%q", err, skel)
	}
	checkInvariants(t, p, DefaultLimits())
	if err := VerifySkeleton(orig, p, omitted); err != nil {
		t.Errorf("VerifySkeleton: %v\n%q", err, skel)
	}
	checkKeptParts(t, raw, skel, orig, omit)
	again, omitted2, err := skeletonOf(skel, omit, DefaultLimits())
	if err != nil || !bytes.Equal(again, skel) || !slices.Equal(omitted, omitted2) {
		t.Errorf("not a fixed point: %v\n%q\n%q", err, skel, again)
	}
}

// sameSet reports whether ids, as a set, is exactly omit.
func sameSet(ids []string, omit map[string]bool) bool {
	if len(ids) != len(omit) {
		return false
	}
	for _, id := range ids {
		if !omit[id] {
			return false
		}
	}
	return true
}

func TestSkeletonCorpus(t *testing.T) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	reduced := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw := readFile(t, e.Name())
			orig, err := Parse(bytes.NewReader(raw), DefaultLimits())
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var atts []string
			for _, a := range orig.Attachments {
				atts = append(atts, a.PartID)
			}
			sets := []map[string]bool{{}, setOf(atts...)}
			if _, ok := skeletonFallback[e.Name()]; !ok {
				for _, id := range leafIDs(orig) {
					sets = append(sets, setOf(id))
				}
			}
			for _, omit := range sets {
				skel, omitted, err := skeletonOf(raw, omit, DefaultLimits())
				if why, ok := skeletonFallback[e.Name()]; ok {
					if !errors.Is(err, ErrNotReducible) {
						t.Fatalf("omit %v: want ErrNotReducible (%s), got %v", omit, why, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("omit %v: %v", omit, err)
				}
				bodyOmitted := omit[orig.TextPartID] || omit[orig.HTMLPartID]
				if !bodyOmitted {
					checkSkeleton(t, raw, skel, omit, omitted)
					continue
				}
				// Omitting a body is a caller's mistake that VerifySkeleton
				// must catch unless the body had nothing in it anyway.
				p, err := Parse(bytes.NewReader(skel), DefaultLimits())
				if err != nil {
					t.Fatalf("parse skeleton: %v", err)
				}
				verr := VerifySkeleton(orig, p, omitted)
				if verr != nil {
					if !errors.Is(verr, ErrNotReducible) {
						t.Errorf("omit %v: %v is not ErrNotReducible", omit, verr)
					}
					continue
				}
				for _, id := range []string{orig.TextPartID, orig.HTMLPartID} {
					if id == "" || !omit[id] {
						continue
					}
					body, err := ExtractPart(bytes.NewReader(raw), id, DefaultLimits(), MaxInputBytes)
					if err != nil || len(bytes.TrimSpace(body.Body)) > 0 {
						t.Errorf("omit %v: body %s went unnoticed", omit, id)
					}
				}
			}
			if _, ok := skeletonFallback[e.Name()]; !ok {
				reduced++
			}
		})
	}
	if reduced < 40 {
		t.Errorf("only %d fixtures reduce", reduced)
	}
	for name := range skeletonFallback {
		if _, err := os.Stat(filepath.Join(testdata, name)); err != nil {
			t.Errorf("fallback table names a missing fixture: %v", err)
		}
	}
}

// TestSkeletonFixtures pins what the skeleton fixtures show about the walk.
func TestSkeletonFixtures(t *testing.T) {
	cases := []struct {
		name        string
		attachments []string
		text, html  string
	}{
		{"skeleton-lf-endings.eml", []string{"2", "3"}, "1.1", "1.2"},
		{"skeleton-mixed-endings.eml", []string{"2"}, "1", "3.1"},
		{"skeleton-multipart-base64-cte.eml", []string{"2"}, "1.1", ""},
		{"skeleton-bad-boundary-chars.eml", []string{"2.1", "2.2.1"}, "1", ""},
		{"skeleton-encoded-boundary.eml", []string{"2.1"}, "1", ""},
		{"skeleton-uppercase-multipart-error.eml", []string{"1"}, "", ""},
		{"skeleton-boundary-prefix.eml", []string{"3"}, "1", "2.1"},
		{"skeleton-preamble-epilogue.eml", []string{"2"}, "1", ""},
		{"skeleton-empty-parts.eml", []string{"2", "3"}, "1", ""},
		{"skeleton-root-attachment.eml", []string{"1"}, "", ""},
		{"skeleton-rfc822-same-boundary.eml", []string{"2", "3"}, "1", ""},
		{"skeleton-nul-binary.eml", []string{"2", "3"}, "1", ""},
		{"skeleton-cid-refs.eml", []string{"1.2", "1.3", "2"}, "", "1.1"},
		{"skeleton-duplicate-cid.eml", []string{"2", "3", "4"}, "", "1"},
		{"skeleton-signed.eml", []string{"1.2", "2"}, "1.1", ""},
		{"skeleton-encrypted.eml", []string{"1", "2"}, "", ""},
	}
	for _, c := range cases {
		p := parseFile(t, c.name)
		var got []string
		for _, a := range p.Attachments {
			got = append(got, a.PartID)
		}
		if !slices.Equal(got, c.attachments) || p.TextPartID != c.text || p.HTMLPartID != c.html {
			t.Errorf("%s: attachments %v text %q html %q", c.name, got, p.TextPartID, p.HTMLPartID)
		}
		wantCrypto := c.name == "skeleton-signed.eml" || c.name == "skeleton-encrypted.eml"
		if p.Crypto != wantCrypto || p.Truncated {
			t.Errorf("%s: crypto %v truncated %v", c.name, p.Crypto, p.Truncated)
		}
	}
	// The broken parameter of a lower-case multipart loses the boundary;
	// an upper-case one is not descended into at all.
	p := parseFile(t, "skeleton-unterminated-quote.eml")
	if len(p.Attachments) != 0 || p.Text != "" || !p.walkFailed {
		t.Errorf("unterminated quote: %+v", p)
	}
	p = parseFile(t, "skeleton-uppercase-multipart-error.eml")
	if a := p.Attachments[0]; a.ContentType != "application/octet-stream" || a.Size == 0 {
		t.Errorf("upper case: %+v", a)
	}
	// The same boundary inside a forwarded message splits the outer one:
	// the forwarded message ends at its own first delimiter.
	p = parseFile(t, "skeleton-rfc822-same-boundary.eml")
	if a := p.Attachments[0]; a.ContentType != "message/rfc822" || a.Size > 120 {
		t.Errorf("rfc822: %+v", a)
	}
	// Invalid base64 in a part that stays is reported the same way twice.
	raw := readFile(t, "skeleton-nul-binary.eml")
	skel, omitted, err := skeletonOf(raw, setOf("2"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	checkSkeleton(t, raw, skel, setOf("2"), omitted)
	if !bytes.Contains(skel, []byte("QUJD!!!!not base64 at all\x00")) || bytes.Contains(skel, []byte("binary tail")) {
		t.Errorf("wrong part kept: %q", skel)
	}
}

func TestSkeletonKeepsHeaderBytes(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml")
	skel, _, err := skeletonOf(raw, setOf("2"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	end := bytes.Index(raw, []byte("\r\n\r\n")) + 4
	if !bytes.HasPrefix(skel, raw[:end]) {
		t.Errorf("header changed:\n%q\n%q", raw[:end], skel[:min(end, len(skel))])
	}
	// Bare LF header lines come out with CRLF, nothing else changes.
	raw = readFile(t, "skeleton-lf-endings.eml")
	skel, _, err = skeletonOf(raw, nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	end = bytes.Index(raw, []byte("\n\n")) + 2
	want := bytes.ReplaceAll(raw[:end], []byte("\n"), []byte("\r\n"))
	if !bytes.HasPrefix(skel, want) {
		t.Errorf("header changed:\n%q\n%q", want, skel[:min(len(want), len(skel))])
	}
}

func TestSkeletonOmitMustNameLeaves(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml")
	for _, omit := range []map[string]bool{
		setOf("1"),        // multipart/related, a container
		setOf("9"),        // no such part
		setOf("2", "1.9"), // one good, one missing
		setOf("0"),        // never a part number
	} {
		if _, _, err := skeletonOf(raw, omit, DefaultLimits()); !errors.Is(err, ErrNotReducible) {
			t.Errorf("omit %v: err = %v", omit, err)
		}
	}
	// A false entry is no entry.
	skel, omitted, err := skeletonOf(raw, map[string]bool{"2": false}, DefaultLimits())
	if err != nil || len(omitted) != 0 {
		t.Fatalf("false entry: %v %v", omitted, err)
	}
	checkSkeleton(t, raw, skel, nil, nil)
}

func TestSkeletonLimits(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml") // 5 entities, 2 deep
	for name, l := range map[string]Limits{
		"parts":  {MaxParts: 4},
		"depth":  {MaxDepth: 1},
		"header": {MaxHeaderBytes: 64},
	} {
		if _, _, err := skeletonOf(raw, setOf("2"), l); !errors.Is(err, ErrNotReducible) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := skeletonOf(raw, setOf("2"), Limits{MaxParts: 5, MaxDepth: 2}); err != nil {
		t.Errorf("exact limits: %v", err)
	}
	if _, _, err := skeletonOf(nil, nil, DefaultLimits()); !errors.Is(err, ErrNotReducible) {
		t.Errorf("empty input: err = %v", err)
	}

	// A header that fits only with bare LF does not fit once it is CRLF.
	var h strings.Builder
	h.WriteString("Subject: grows\n")
	for h.Len() < 900 {
		h.WriteString("X-A: 1\n")
	}
	lfMsg := []byte(h.String() + "\nbody\n")
	if _, err := Parse(bytes.NewReader(lfMsg), Limits{MaxHeaderBytes: 1000}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, err := skeletonOf(lfMsg, nil, Limits{MaxHeaderBytes: 1000}); !errors.Is(err, ErrNotReducible) {
		t.Errorf("growing header: err = %v", err)
	}

	// Input cut at MaxInputBytes.
	head := "Subject: big\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nx\r\n--b\r\nContent-Type: application/octet-stream\r\n\r\n"
	r := io.MultiReader(strings.NewReader(head), &repeatReader{'b', MaxInputBytes}, strings.NewReader("\r\n--b--\r\n"))
	var out bytes.Buffer
	if _, err := Skeleton(r, &out, setOf("2"), DefaultLimits()); !errors.Is(err, ErrNotReducible) {
		t.Errorf("over MaxInputBytes: err = %v", err)
	}
}

// failingWriter fails after n bytes; failingReader after n bytes of r.
type failingWriter struct{ n int }

var errDisk = errors.New("disk on fire")

func (w *failingWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		n := w.n
		w.n = 0
		return n, errDisk
	}
	w.n -= len(p)
	return len(p), nil
}

type failingReader struct {
	r io.Reader
	n int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errDisk
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

func TestSkeletonIOErrors(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml")
	for _, n := range []int{0, 10, 300, 600} { // the skeleton has 668 bytes
		_, err := Skeleton(bytes.NewReader(raw), &failingWriter{n: n}, setOf("2"), DefaultLimits())
		if !errors.Is(err, errDisk) || errors.Is(err, ErrNotReducible) {
			t.Errorf("writer failing after %d: %v", n, err)
		}
		_, err = Skeleton(&failingReader{r: bytes.NewReader(raw), n: n}, io.Discard, setOf("2"), DefaultLimits())
		if !errors.Is(err, errDisk) || errors.Is(err, ErrNotReducible) {
			t.Errorf("reader failing after %d: %v", n, err)
		}
	}
}

// The line endings of a nested multipart are LF and its parent's are CRLF,
// so its parent's reader does not see the lines below as delimiters. In
// the skeleton every line ends in CRLF: each of these would end the parent
// part early, and Skeleton must refuse instead.
var collisionCases = map[string]string{
	"delimiter": "Subject: c\r\nContent-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/mixed; boundary=\"outer-x\"\n\npreamble\n" +
		"--outer-x\nContent-Type: application/octet-stream\n\ninner\n--outer-x--\n" +
		"\r\n--outer--\r\n",
	"header line": "Subject: c\r\nContent-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/mixed; boundary=\"inner\"\n\n" +
		"--inner\nX-Foo: bar\n--outer-y: z\nContent-Type: application/octet-stream\n\ninner\n--inner--\n" +
		"\r\n--outer--\r\n",
	"body line": "Subject: c\r\nContent-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/mixed; boundary=\"inner\"\n\n" +
		"--inner\nContent-Type: application/octet-stream\n\n--outer-z starts this body\n--inner--\n" +
		"\r\n--outer--\r\n",
}

func TestSkeletonRefusesCollisions(t *testing.T) {
	for name, msg := range collisionCases {
		p := parseString(t, msg, DefaultLimits())
		if p.walkFailed || len(p.Attachments) != 1 || p.Attachments[0].PartID != "1.1" || p.Attachments[0].Size == 0 {
			t.Fatalf("%s: the original must parse cleanly: %+v", name, p)
		}
		if _, _, err := skeletonOf([]byte(msg), nil, DefaultLimits()); !errors.Is(err, ErrNotReducible) {
			t.Errorf("%s: err = %v", name, err)
		}
		// What a naive rewrite would store: every line CRLF. It does not
		// parse the same, and VerifySkeleton says so.
		naive := strings.ReplaceAll(strings.ReplaceAll(msg, "\r\n", "\n"), "\n", "\r\n")
		q := parseString(t, naive, DefaultLimits())
		if err := VerifySkeleton(p, q, nil); !errors.Is(err, ErrNotReducible) {
			t.Errorf("%s: the naive rewrite verifies: %v", name, err)
		}
	}
	// Omitting the part whose first line collides leaves nothing to collide.
	msg := []byte(collisionCases["body line"])
	skel, omitted, err := skeletonOf(msg, setOf("1.1"), DefaultLimits())
	if err != nil {
		t.Fatalf("omitted body: %v", err)
	}
	checkSkeleton(t, msg, skel, setOf("1.1"), omitted)
	// A boundary the outer one is only a prefix of is fine.
	msg = []byte(strings.ReplaceAll(collisionCases["delimiter"], "outer-x", "outerx"))
	skel, omitted, err = skeletonOf(msg, setOf("1.1"), DefaultLimits())
	if err != nil {
		t.Fatalf("prefix boundary: %v", err)
	}
	checkSkeleton(t, msg, skel, setOf("1.1"), omitted)
}

// largeMessage is a message with a 300 KiB attachment and a 150 KiB picture
// its HTML shows, both base64 in 76-column lines.
func largeMessage() (raw []byte, pdf, jpeg []byte) {
	fill := func(pattern string, n int) []byte {
		return bytes.Repeat([]byte(pattern), n/len(pattern)+1)[:n]
	}
	pdf = fill("%PDF-1.4 pretend \x00\x01\xfe\xff", 300<<10)
	jpeg = fill("\xff\xd8\xff\xe0 JFIF pretend ", 150<<10)
	b64 := func(data []byte) string {
		s := base64.StdEncoding.EncodeToString(data)
		var b strings.Builder
		for len(s) > 76 {
			b.WriteString(s[:76] + "\r\n")
			s = s[76:]
		}
		b.WriteString(s)
		return b.String()
	}
	var b strings.Builder
	b.WriteString("From: Alice <alice@example.org>\r\nSubject: Holiday\r\nMessage-ID: <large@example.org>\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n")
	b.WriteString("--mix\r\nContent-Type: multipart/related; boundary=\"rel\"\r\n\r\n")
	b.WriteString("--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Look: <img src=\"cid:Photo@Example.org\"></p>\r\n")
	b.WriteString("--rel\r\nContent-Type: image/jpeg\r\nContent-ID: <photo@example.org>\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(b64(jpeg))
	b.WriteString("\r\n--rel--\r\n")
	b.WriteString("--mix\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"plan.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(b64(pdf))
	b.WriteString("\r\n--mix--\r\n")
	return []byte(b.String()), pdf, jpeg
}

func TestSkeletonLargeAttachment(t *testing.T) {
	raw, pdf, jpeg := largeMessage()
	p, err := Parse(bytes.NewReader(raw), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	refs, complete := CIDReferences(p.RawHTML)
	if !complete {
		t.Fatal("references incomplete")
	}
	// The decision the ingest makes: large and not shown by the HTML.
	omit := map[string]bool{}
	for _, a := range p.Attachments {
		if a.Size >= api.LargeAttachmentMinBytes && !refs[NormalizeCID(a.ContentID)] {
			omit[a.PartID] = true
		}
	}
	if !sameSet([]string{"2"}, omit) {
		t.Fatalf("omit = %v (attachments %+v, refs %v)", omit, p.Attachments, refs)
	}
	skel, omitted, err := skeletonOf(raw, omit, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	checkSkeleton(t, raw, skel, omit, omitted)
	if len(skel) > len(raw)-400<<10 {
		t.Errorf("skeleton is %d of %d bytes", len(skel), len(raw))
	}
	img, err := ExtractPart(bytes.NewReader(skel), "1.2", DefaultLimits(), MaxInputBytes)
	if err != nil || !bytes.Equal(img.Body, jpeg) {
		t.Errorf("picture: %v", err)
	}
	full, err := ExtractPart(bytes.NewReader(raw), "2", DefaultLimits(), MaxInputBytes)
	if err != nil || !bytes.Equal(full.Body, pdf) {
		t.Errorf("original attachment: %v", err)
	}
	// Omitting the picture too is the caller's choice; the skeleton obliges.
	skel, omitted, err = skeletonOf(raw, setOf("1.2", "2"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	checkSkeleton(t, raw, skel, setOf("1.2", "2"), omitted)
}

func TestVerifySkeletonDetectsMismatch(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml")
	orig := parseFile(t, "mixed-attachments.eml")
	skelRaw, omitted, err := skeletonOf(raw, setOf("2"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	parse := func() *Parsed {
		p, err := Parse(bytes.NewReader(skelRaw), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := VerifySkeleton(orig, parse(), omitted); err != nil {
		t.Fatalf("the real skeleton: %v", err)
	}
	cases := map[string]struct {
		edit    func(p *Parsed)
		omitted []string
	}{
		"nothing claimed omitted": {nil, nil},
		"unknown omitted part":    {nil, []string{"2", "7"}},
		"container omitted":       {nil, []string{"2", "1"}},
		"omitted twice":           {nil, []string{"2", "2"}},
		"body omitted":            {nil, []string{"2", "1.1"}},
		"subject":                 {func(p *Parsed) { p.Subject += "!" }, omitted},
		"from":                    {func(p *Parsed) { p.From = nil }, omitted},
		"date":                    {func(p *Parsed) { p.Date = p.Date.Add(1) }, omitted},
		"references":              {func(p *Parsed) { p.References = append(p.References, "x@y") }, omitted},
		"headers":                 {func(p *Parsed) { p.Headers["X-Mailer"] = "x" }, omitted},
		"text":                    {func(p *Parsed) { p.Text = "" }, omitted},
		"html":                    {func(p *Parsed) { p.RawHTML += " " }, omitted},
		"html part":               {func(p *Parsed) { p.HTMLPartID = "1.2" }, omitted},
		"snippet":                 {func(p *Parsed) { p.Snippet = "" }, omitted},
		"kept size":               {func(p *Parsed) { p.Attachments[0].Size-- }, omitted},
		"omitted size":            {func(p *Parsed) { p.Attachments[1].Size = 1 }, omitted},
		"filename":                {func(p *Parsed) { p.Attachments[1].Filename = "x.pdf" }, omitted},
		"content id":              {func(p *Parsed) { p.Attachments[0].ContentID = "" }, omitted},
		"inline":                  {func(p *Parsed) { p.Attachments[0].Inline = false }, omitted},
		"attachment dropped":      {func(p *Parsed) { p.Attachments = p.Attachments[:1] }, omitted},
		"has attachments":         {func(p *Parsed) { p.HasAttachments = false }, omitted},
		"truncated":               {func(p *Parsed) { p.Truncated = true }, omitted},
		"crypto":                  {func(p *Parsed) { p.Crypto = true }, omitted},
		"walk failed":             {func(p *Parsed) { p.walkFailed = true }, omitted},
		"entities":                {func(p *Parsed) { p.entities++ }, omitted},
	}
	for name, c := range cases {
		p := parse()
		if c.edit != nil {
			c.edit(p)
		}
		if err := VerifySkeleton(orig, p, c.omitted); !errors.Is(err, ErrNotReducible) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := VerifySkeleton(nil, parse(), omitted); !errors.Is(err, ErrNotReducible) {
		t.Errorf("nil original: %v", err)
	}
	truncated := *orig
	truncated.Truncated = true
	if err := VerifySkeleton(&truncated, parse(), omitted); !errors.Is(err, ErrNotReducible) {
		t.Errorf("truncated original: %v", err)
	}
}

// fuzzLimits keeps each fuzz iteration cheap, as FuzzParse does.
func fuzzLimits() Limits {
	l := DefaultLimits()
	l.MaxTextBytes = 64 << 10
	return l
}

func FuzzSkeleton(f *testing.F) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".eml" {
			raw := readFile(f, e.Name())
			f.Add(raw, uint64(0))
			f.Add(raw, ^uint64(0)>>1)
		}
	}
	for _, msg := range collisionCases {
		f.Add([]byte(msg), uint64(0))
	}
	f.Add([]byte("Content-Type: multipart/mixed; boundary=\"=?utf-8?q?a=0Db?=\"\r\n\r\n--a\rb\r\n\r\nx\r\n--a\rb--\r\n"), uint64(1))
	limits := fuzzLimits()
	f.Fuzz(func(t *testing.T, data []byte, sel uint64) {
		orig, perr := Parse(bytes.NewReader(data), limits)
		omit := map[string]bool{}
		if perr == nil {
			for i, a := range orig.Attachments {
				if i < 63 && sel&(1<<i) != 0 {
					omit[a.PartID] = true
				}
			}
		}
		if sel&(1<<63) != 0 {
			omit["0"] = true // never a part number: must be refused
		}
		out, omitted, err := skeletonOf(data, omit, limits)
		if len(out) > 2*len(data)+64 {
			t.Fatalf("%d bytes out of %d", len(out), len(data))
		}
		if err != nil {
			if !errors.Is(err, ErrNotReducible) {
				t.Fatalf("error is not ErrNotReducible: %v", err)
			}
			return
		}
		if perr != nil || omit["0"] {
			t.Fatalf("skeleton although parse failed (%v) or omit is bogus", perr)
		}
		if !sameSet(omitted, omit) {
			t.Fatalf("omitted %v, asked for %v", omitted, omit)
		}
		// Parse's own invariants are FuzzParse's business; what matters here
		// is that the skeleton parses to what the original parses to.
		skel, err := Parse(bytes.NewReader(out), limits)
		if err != nil {
			t.Fatalf("skeleton does not parse: %v\n%q", err, out)
		}
		if err := VerifySkeleton(orig, skel, omitted); err != nil && !orig.Truncated {
			t.Fatalf("does not verify: %v\n%q\n%q", err, data, out)
		}
		again, _, err := skeletonOf(out, omit, limits)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not a fixed point: %v\n%q\n%q", err, out, again)
		}
		if len(omit) == 0 {
			return
		}
		// With nothing omitted every size must survive. Keeping more can
		// only add refusals: a part the omit set dropped may start with a
		// line that would read as an enclosing delimiter once it is kept.
		whole, none, err := skeletonOf(data, nil, limits)
		if err != nil {
			if !errors.Is(err, ErrNotReducible) {
				t.Fatalf("nothing omitted: %v", err)
			}
			return
		}
		if len(none) != 0 {
			t.Fatalf("nothing omitted, yet %v", none)
		}
		p, err := Parse(bytes.NewReader(whole), limits)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := VerifySkeleton(orig, p, nil); err != nil && !orig.Truncated {
			t.Fatalf("nothing omitted does not verify: %v", err)
		}
	})
}

func ExampleSkeleton() {
	raw := "Subject: report\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nSee attached.\r\n" +
		"--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=r.pdf\r\n\r\n%PDF-1.4 ...\r\n" +
		"--b--\r\n"
	var out bytes.Buffer
	omitted, err := Skeleton(strings.NewReader(raw), &out, map[string]bool{"2": true}, DefaultLimits())
	fmt.Println(omitted, err)
	// Every line of the skeleton ends in CRLF, shown here as "|".
	fmt.Print(strings.ReplaceAll(out.String(), "\r\n", "|\n"))
	// Output:
	// [2] <nil>
	// Subject: report|
	// Content-Type: multipart/mixed; boundary=b|
	// |
	// --b|
	// Content-Type: text/plain|
	// |
	// See attached.|
	// --b|
	// Content-Type: application/pdf|
	// Content-Disposition: attachment; filename=r.pdf|
	// |
	// |
	// --b--|
}
