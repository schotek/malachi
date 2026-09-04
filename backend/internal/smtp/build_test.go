// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/schotek/malachi/backend/pkg/api"
)

var (
	jiri  = api.Address{Name: "Jiří Novák", Address: "jiri@example.cz"}
	alice = api.Address{Name: "Alice", Address: "alice@example.org"}
	bob   = api.Address{Address: "bob@example.net"}
	carol = api.Address{Name: "Carol", Address: "carol@example.com"}
)

// build runs BuildMessage into a buffer and fails the test on error.
func build(t *testing.T, in BuildInput) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := BuildMessage(&buf, in); err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	return buf.Bytes()
}

// headerCount counts the top-level header fields with the given key.
func headerCount(t *testing.T, raw []byte, key string) int {
	t.Helper()
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("message.Read: %v", err)
	}
	n := 0
	for f := e.Header.FieldsByKey(key); f.Next(); {
		n++
	}
	return n
}

// rawHeaderLines returns the raw header block split into lines, unfolded.
func rawHeaderLines(t *testing.T, raw []byte) []string {
	t.Helper()
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if i < 0 {
		t.Fatalf("no header/body separator in %q", raw)
	}
	head := strings.ReplaceAll(string(raw[:i]), "\r\n ", " ")
	head = strings.ReplaceAll(head, "\r\n\t", " ")
	return strings.Split(head, "\r\n")
}

// readParts parses the message and returns its parts with bodies read.
func readParts(t *testing.T, raw []byte) (mail.Header, []mail.Part, [][]byte) {
	t.Helper()
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("CreateReader: %v", err)
	}
	var parts []mail.Part
	var bodies [][]byte
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		body, err := io.ReadAll(p.Body)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts = append(parts, *p)
		bodies = append(bodies, body)
	}
	return mr.Header, parts, bodies
}

func lf(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }

func TestBuildTextOnly(t *testing.T) {
	date := time.Date(2026, 9, 3, 10, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	raw := build(t, BuildInput{
		From:       jiri,
		To:         []api.Address{alice, bob},
		CC:         []api.Address{carol},
		Subject:    "Zpráva s češtinou",
		Text:       "Ahoj,\r\nčeština na druhém řádku\rtřetí řádek\n",
		InReplyTo:  "parent@example.org",
		References: []string{"root@example.org", "parent@example.org"},
		Date:       date,
	})

	h, parts, bodies := readParts(t, raw)

	from, err := h.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Name != jiri.Name || from[0].Address != jiri.Address {
		t.Fatalf("From = %v, %v", from, err)
	}
	to, err := h.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Name != "Alice" || to[0].Address != alice.Address || to[1].Address != bob.Address || to[1].Name != "" {
		t.Fatalf("To = %v, %v", to, err)
	}
	cc, err := h.AddressList("Cc")
	if err != nil || len(cc) != 1 || cc[0].Address != carol.Address {
		t.Fatalf("Cc = %v, %v", cc, err)
	}
	if bcc, _ := h.AddressList("Bcc"); bcc != nil {
		t.Fatalf("Bcc present: %v", bcc)
	}
	if s, err := h.Subject(); err != nil || s != "Zpráva s češtinou" {
		t.Fatalf("Subject = %q, %v", s, err)
	}
	if got, err := h.Date(); err != nil || !got.Equal(date) {
		t.Fatalf("Date = %v, %v", got, err)
	}
	id, err := h.MessageID()
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}@example\.cz$`).MatchString(id) {
		t.Fatalf("Message-ID = %q, %v", id, err)
	}
	if irt, err := h.MsgIDList("In-Reply-To"); err != nil || len(irt) != 1 || irt[0] != "parent@example.org" {
		t.Fatalf("In-Reply-To = %v, %v", irt, err)
	}
	if refs, err := h.MsgIDList("References"); err != nil || len(refs) != 2 || refs[1] != "parent@example.org" {
		t.Fatalf("References = %v, %v", refs, err)
	}
	if h.Get("User-Agent") != "Malachi Mail" || h.Get("Mime-Version") != "1.0" {
		t.Fatalf("User-Agent = %q, Mime-Version = %q", h.Get("User-Agent"), h.Get("Mime-Version"))
	}
	ct, params, err := h.ContentType()
	if err != nil || ct != "text/plain" || params["charset"] != "utf-8" {
		t.Fatalf("Content-Type = %q %v, %v", ct, params, err)
	}
	if h.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("CTE = %q", h.Get("Content-Transfer-Encoding"))
	}

	if len(parts) != 1 {
		t.Fatalf("parts = %d", len(parts))
	}
	if _, ok := parts[0].Header.(*mail.InlineHeader); !ok {
		t.Fatalf("part header is %T", parts[0].Header)
	}
	if got := lf(bodies[0]); got != "Ahoj,\nčeština na druhém řádku\ntřetí řádek\n" {
		t.Fatalf("body = %q", got)
	}

	for _, key := range []string{"Subject", "From", "To", "Cc", "Date", "Message-Id", "In-Reply-To", "References"} {
		if n := headerCount(t, raw, key); n != 1 {
			t.Fatalf("%s occurs %d times", key, n)
		}
	}
	if n := headerCount(t, raw, "Bcc"); n != 0 {
		t.Fatalf("Bcc occurs %d times", n)
	}
	// Bracketed forms on the wire.
	lines := rawHeaderLines(t, raw)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"In-Reply-To: <parent@example.org>", "References: <root@example.org> <parent@example.org>", "Message-Id: <"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
}

func TestBuildOptionalHeadersOmitted(t *testing.T) {
	raw := build(t, BuildInput{From: bob, To: []api.Address{alice}, Subject: "hi", Text: "x", MessageID: "fixed@example.net",
		References: []string{"", "  ", "<>"}})
	for _, key := range []string{"Cc", "Bcc", "In-Reply-To", "References"} {
		if n := headerCount(t, raw, key); n != 0 {
			t.Fatalf("%s occurs %d times", key, n)
		}
	}
	h, _, _ := readParts(t, raw)
	if id, _ := h.MessageID(); id != "fixed@example.net" {
		t.Fatalf("Message-ID = %q", id)
	}
	if d, err := h.Date(); err != nil || time.Since(d) > time.Minute {
		t.Fatalf("Date = %v, %v", d, err)
	}
}

func TestBuildBccOnlyIsLegal(t *testing.T) {
	raw := build(t, BuildInput{From: bob, Subject: "hi", Text: "x"})
	if headerCount(t, raw, "To") != 0 || headerCount(t, raw, "Bcc") != 0 {
		t.Fatalf("unexpected recipient headers in %q", raw)
	}
}

func TestBuildLongLinesSoftWrapped(t *testing.T) {
	long := strings.Repeat("dlouhý řádek bez zalomení ", 40) // ~1 KiB, non-ASCII
	text := long + "\n" + strings.Repeat("a", 500) + "\n"
	raw := build(t, BuildInput{From: bob, To: []api.Address{alice}, Subject: "long", Text: text})

	body := raw[bytes.Index(raw, []byte("\r\n\r\n"))+4:]
	for _, line := range bytes.Split(body, []byte("\r\n")) {
		if len(line) > 76 {
			t.Fatalf("line of %d bytes on the wire: %q", len(line), line)
		}
	}
	if !bytes.Contains(body, []byte("=\r\n")) {
		t.Fatalf("no soft line break in %q", body)
	}
	_, _, bodies := readParts(t, raw)
	if got := lf(bodies[0]); got != text {
		t.Fatalf("round trip lost data:\n%q\n%q", got, text)
	}
}

func TestBuildAttachments(t *testing.T) {
	pdf := bytes.Repeat([]byte{0x25, 0x50, 0x44, 0x46, 0xff, 0x00, 0x0d, 0x0a}, 200)
	open := func(b []byte) func() (io.ReadCloser, error) {
		return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	}
	raw := build(t, BuildInput{
		From: jiri, To: []api.Address{alice}, Subject: "příloha", Text: "viz přílohu\n",
		Attachments: []Attachment{
			{Filename: "příloha s mezerou.pdf", ContentType: "application/pdf", Size: int64(len(pdf)), Open: open(pdf)},
			{Filename: "../../etc/passwd", ContentType: "text/plain; charset=utf-8", Open: open([]byte("root:x:0:0\n"))},
			{Filename: "\x00.hidden\r\n.txt", ContentType: "multipart/mixed; boundary=x", Open: open([]byte("nested"))},
			{Filename: "", ContentType: "not a type", Open: open([]byte{1, 2, 3})},
		},
	})

	h, parts, bodies := readParts(t, raw)
	if ct, _, _ := h.ContentType(); ct != "multipart/mixed" {
		t.Fatalf("top-level Content-Type = %q", ct)
	}
	if len(parts) != 5 {
		t.Fatalf("parts = %d", len(parts))
	}
	if _, ok := parts[0].Header.(*mail.InlineHeader); !ok || lf(bodies[0]) != "viz přílohu\n" {
		t.Fatalf("inline part = %T %q", parts[0].Header, bodies[0])
	}

	want := []struct {
		name, ct string
		body     []byte
	}{
		{"příloha s mezerou.pdf", "application/pdf", pdf},
		{"passwd", "text/plain", []byte("root:x:0:0\n")},
		{"hidden.txt", "application/octet-stream", []byte("nested")},
		{"attachment", "application/octet-stream", []byte{1, 2, 3}},
	}
	for i, w := range want {
		ah, ok := parts[i+1].Header.(*mail.AttachmentHeader)
		if !ok {
			t.Fatalf("part %d header is %T", i+1, parts[i+1].Header)
		}
		name, err := ah.Filename()
		if err != nil || name != w.name {
			t.Fatalf("part %d filename = %q, %v (want %q)", i+1, name, err, w.name)
		}
		if ct, _, _ := ah.ContentType(); ct != w.ct {
			t.Fatalf("part %d Content-Type = %q (want %q)", i+1, ct, w.ct)
		}
		if ah.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatalf("part %d CTE = %q", i+1, ah.Get("Content-Transfer-Encoding"))
		}
		if !bytes.Equal(bodies[i+1], w.body) {
			t.Fatalf("part %d body mismatch", i+1)
		}
	}
	// The UTF-8 name went out as RFC 2231 parameters.
	if !bytes.Contains(raw, []byte("filename*")) || !bytes.Contains(raw, []byte("utf-8''")) {
		t.Fatalf("no RFC 2231 filename in %q", raw)
	}
	if bytes.Contains(raw, []byte("etc/passwd")) {
		t.Fatalf("path leaked into %q", raw)
	}
}

func TestBuildHeaderInjection(t *testing.T) {
	raw := build(t, BuildInput{
		From:       api.Address{Name: "Bob\r\nBcc: evil@example.org", Address: bob.Address},
		To:         []api.Address{{Name: "A\nBcc: evil@example.org\n", Address: alice.Address}},
		Subject:    "x\r\nBcc: evil@example.org",
		Text:       "body",
		InReplyTo:  "id\r\nBcc: evil@example.org",
		References: []string{"r\nBcc: evil@example.org", "ok@example.org"},
		MessageID:  "m\r\nBcc: evil@example.org",
	})
	if n := headerCount(t, raw, "Bcc"); n != 0 {
		t.Fatalf("Bcc occurs %d times in %q", n, raw)
	}
	// Every raw header line must be one of the fields we write: nothing
	// smuggled in through a folded or injected line. The mangled
	// In-Reply-To id no longer parses as a msg-id and is dropped.
	known := []string{"From:", "To:", "Subject:", "Date:", "Message-Id:", "References:",
		"User-Agent:", "Mime-Version:", "Content-Type:", "Content-Transfer-Encoding:"}
	lines := rawHeaderLines(t, raw)
	for _, l := range lines {
		ok := false
		for _, k := range known {
			ok = ok || strings.HasPrefix(l, k)
		}
		if !ok {
			t.Fatalf("unexpected header line %q", l)
		}
	}
	if len(lines) != len(known) {
		t.Fatalf("header field count %d, want %d:\n%s", len(lines), len(known), strings.Join(lines, "\n"))
	}
	if n := headerCount(t, raw, "Subject"); n != 1 {
		t.Fatalf("Subject occurs %d times", n)
	}
	h, _, _ := readParts(t, raw)
	if s, _ := h.Subject(); s != "xBcc: evil@example.org" {
		t.Fatalf("Subject = %q", s)
	}
	if from, _ := h.AddressList("From"); len(from) != 1 || from[0].Name != "BobBcc: evil@example.org" {
		t.Fatalf("From = %v", from)
	}
	// The mangled Message-ID was replaced by a generated one.
	if id, _ := h.MessageID(); !regexp.MustCompile(`^[0-9a-f]{32}@example\.net$`).MatchString(id) {
		t.Fatalf("Message-ID = %q", id)
	}
	if refs, _ := h.MsgIDList("References"); len(refs) != 1 || refs[0] != "ok@example.org" {
		t.Fatalf("References = %v", refs)
	}
	if irt, _ := h.MsgIDList("In-Reply-To"); irt != nil {
		t.Fatalf("In-Reply-To = %v", irt)
	}
}

func TestBuildInvalidAddress(t *testing.T) {
	bad := []api.Address{
		{Address: ""},
		{Address: "nope"},
		{Address: "@example.org"},
		{Address: "a@"},
		{Address: "a@example.org\nBcc: evil@example.org"},
		{Address: "a b@example.org"},
		{Address: "\"quoted\"@example.org"},
		{Address: "a@example.org,b@example.org"},
		{Address: "<a@example.org>"},
	}
	for _, a := range bad {
		var buf bytes.Buffer
		err := BuildMessage(&buf, BuildInput{From: bob, To: []api.Address{a}})
		if !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("To %q: err = %v", a.Address, err)
		}
		if code(t, err) != api.CodeInvalidArgument {
			t.Fatalf("To %q: code = %v", a.Address, err)
		}
		if err := BuildMessage(&buf, BuildInput{From: a}); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("From %q: err = %v", a.Address, err)
		}
	}
	// A non-ASCII local part is fine (SMTPUTF8 decides later).
	build(t, BuildInput{From: bob, To: []api.Address{{Address: "jiří@example.cz"}}})
}

func TestBuildHeaderCap(t *testing.T) {
	raw := build(t, BuildInput{From: bob, Subject: strings.Repeat("ř", 3000)})
	h, _, _ := readParts(t, raw)
	s, _ := h.Subject()
	if len(s) > maxHeaderBytes || len(s) < maxHeaderBytes-4 || !strings.HasPrefix(s, "řř") {
		t.Fatalf("subject len = %d", len(s))
	}
}

// failWriter returns sentinel after limit bytes, like a counting writer.
type failWriter struct {
	limit    int
	n        int
	sentinel error
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n+len(p) > f.limit {
		return 0, f.sentinel
	}
	f.n += len(p)
	return len(p), nil
}

func TestBuildWriterErrorPropagates(t *testing.T) {
	errTooBig := errors.New("too big")
	att := Attachment{Filename: "a.bin", ContentType: "application/octet-stream",
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(make([]byte, 4096))), nil }}
	for _, in := range []BuildInput{
		{From: bob, Subject: "s", Text: strings.Repeat("x", 4096)},
		{From: bob, Subject: "s", Text: "x", Attachments: []Attachment{att}},
	} {
		for _, limit := range []int{0, 100, 1000} {
			err := BuildMessage(&failWriter{limit: limit, sentinel: errTooBig}, in)
			if err != errTooBig {
				t.Fatalf("limit %d: err = %v (%T)", limit, err, err)
			}
		}
	}
}

func TestBuildAttachmentErrors(t *testing.T) {
	errOpen := errors.New("open failed")
	err := BuildMessage(io.Discard, BuildInput{From: bob, Attachments: []Attachment{{Filename: "a",
		Open: func() (io.ReadCloser, error) { return nil, errOpen }}}})
	if !errors.Is(err, errOpen) {
		t.Fatalf("open error = %v", err)
	}

	errRead := errors.New("read failed")
	err = BuildMessage(io.Discard, BuildInput{From: bob, Attachments: []Attachment{{Filename: "a",
		Open: func() (io.ReadCloser, error) { return io.NopCloser(&failReader{err: errRead}), nil }}}})
	if !errors.Is(err, errRead) {
		t.Fatalf("read error = %v", err)
	}

	err = BuildMessage(io.Discard, BuildInput{From: bob, Attachments: []Attachment{{Filename: "a"}}})
	if code(t, err) != api.CodeInvalidArgument {
		t.Fatalf("nil Open: %v", err)
	}
}

type failReader struct{ err error }

func (f *failReader) Read([]byte) (int, error) { return 0, f.err }

func TestNewMessageID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{32}@(.+)$`)
	cases := map[string]string{
		"user@Example.COM":  "example.com",
		"jiri@příklad.cz":   "příklad.cz",
		"nodomain":          fallbackDomain,
		"":                  fallbackDomain,
		"a@":                fallbackDomain,
		"a@b c":             fallbackDomain,
		"a@b\r\nX: y":       fallbackDomain,
		"Name <a@b.org>":    fallbackDomain,
		"a@b@example.org":   "example.org",
		"x@[192.168.0.1]":   fallbackDomain,
		"x@example.org\x00": fallbackDomain,
	}
	for email, domain := range cases {
		id := NewMessageID(email)
		m := re.FindStringSubmatch(id)
		if m == nil || m[1] != domain {
			t.Fatalf("NewMessageID(%q) = %q, want domain %q", email, id, domain)
		}
		if strings.ContainsAny(id, "<>") {
			t.Fatalf("brackets in %q", id)
		}
	}
	if NewMessageID("a@b.org") == NewMessageID("a@b.org") {
		t.Fatal("ids repeat")
	}
}
