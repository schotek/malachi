// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package mime parses RFC 5322 messages into what the offline store keeps:
// the envelope, the plain-text body, an HTML body for later sanitisation
// and attachment metadata.
//
// Every message is treated as hostile input. The parser never panics and
// never hangs on malformed data: the raw input is capped at MaxInputBytes,
// header blocks at Limits.MaxHeaderBytes, the part tree at
// Limits.MaxParts/MaxDepth, text bodies at Limits.MaxTextBytes and every
// emitted string is valid UTF-8 without control characters. Parse fails only
// when the top-level header cannot be read; everything else is tolerated and
// recorded in Parsed.Problems.
//
// The package name shadows the standard library's mime; the standard package
// is imported as stdmime where needed.
//
// The blank import of github.com/emersion/go-message/charset installs
// go-message's charset table (backed by golang.org/x/text) as a side effect,
// so text parts and encoded words in legacy charsets such as ISO-8859-2 or
// windows-1250 decode to UTF-8. Unknown charsets are tolerated: the bytes are
// read as-is and invalid UTF-8 is replaced.
package mime

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // registers message.CharsetReader
	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/pkg/api"
)

// MaxInputBytes is the hard cap on raw message bytes Parse reads. Input
// beyond it is ignored and the result is marked Truncated.
const MaxInputBytes = 25 << 20

// maxProblems bounds Parsed.Problems so a message with thousands of broken
// parts cannot inflate the result.
const maxProblems = 32

// Limits bounds the work Parse does and the size of what it returns.
// Zero or negative fields are replaced by the DefaultLimits values.
type Limits struct {
	MaxParts        int   // parts visited before the walk stops
	MaxDepth        int   // nesting depth of multipart containers
	MaxHeaderBytes  int64 // size of the top-level header block (error when exceeded)
	MaxTextBytes    int64 // decoded bytes kept of the text and HTML bodies
	MaxAttachments  int   // entries in Parsed.Attachments
	MaxReferences   int   // entries in Parsed.References
	MaxSnippetRunes int   // length of Parsed.Snippet
	MaxFieldBytes   int   // bytes per header-derived string (subject, names, IDs…)
}

// DefaultLimits returns the limits used by the sync engine.
func DefaultLimits() Limits {
	return Limits{
		MaxParts:        500,
		MaxDepth:        20,
		MaxHeaderBytes:  256 << 10,
		MaxTextBytes:    1 << 20,
		MaxAttachments:  200,
		MaxReferences:   50,
		MaxSnippetRunes: 200,
		MaxFieldBytes:   2048,
	}
}

// withDefaults fills unset fields from DefaultLimits.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxParts <= 0 {
		l.MaxParts = d.MaxParts
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxHeaderBytes <= 0 {
		l.MaxHeaderBytes = d.MaxHeaderBytes
	}
	if l.MaxTextBytes <= 0 {
		l.MaxTextBytes = d.MaxTextBytes
	}
	if l.MaxAttachments <= 0 {
		l.MaxAttachments = d.MaxAttachments
	}
	if l.MaxReferences <= 0 {
		l.MaxReferences = d.MaxReferences
	}
	if l.MaxSnippetRunes <= 0 {
		l.MaxSnippetRunes = d.MaxSnippetRunes
	}
	if l.MaxFieldBytes <= 0 {
		l.MaxFieldBytes = d.MaxFieldBytes
	}
	return l
}

// Parsed is the result of Parse. All strings are valid UTF-8 without
// control characters (Text and RawHTML keep newlines and tabs).
type Parsed struct {
	Subject    string
	From       []api.Address
	To         []api.Address
	CC         []api.Address
	BCC        []api.Address
	ReplyTo    []api.Address
	Date       time.Time // zero when missing or unparsable
	MessageID  string    // without angle brackets
	InReplyTo  string    // first identifier of In-Reply-To, without brackets
	References []string  // capped at Limits.MaxReferences
	Headers    map[string]string

	Text       string // plain text body; derived from RawHTML when no text part exists
	HasHTML    bool
	RawHTML    string // unsanitised HTML body: internal, never sent over the API
	TextPartID string // IMAP-style part number of the chosen text/plain part
	HTMLPartID string // IMAP-style part number of the chosen text/html part
	Snippet    string

	Attachments    []api.Attachment
	HasAttachments bool // at least one non-inline attachment

	Truncated bool     // a limit was hit: the result is incomplete
	Problems  []string // technical English notes on tolerated defects (not for display)
}

// errStop is the sentinel returned from the walk callback to end the walk.
var errStop = errors.New("mime: walk stopped")

// Parse reads one message from r. It returns an error only when the
// top-level header cannot be read (including when it exceeds
// limits.MaxHeaderBytes); malformed bodies, unknown charsets or encodings and
// exceeded limits are reported through Parsed.Problems and Parsed.Truncated.
// At most MaxInputBytes are consumed from r.
func Parse(r io.Reader, limits Limits) (*Parsed, error) {
	limits = limits.withDefaults()
	lr := &io.LimitedReader{R: r, N: MaxInputBytes}
	cr := &countingReader{r: lr}
	// go-message accepts a header block without the terminating blank line
	// and, unfortunately, also completely empty input; the latter is
	// rejected below so a zero-byte file never becomes an empty message.
	root, err := message.ReadWithOptions(cr, &message.ReadOptions{MaxHeaderBytes: limits.MaxHeaderBytes})
	if root == nil {
		if err == nil {
			err = errors.New("no entity")
		}
		return nil, fmt.Errorf("mime: read header: %w", err)
	}
	if cr.n == 0 {
		return nil, errors.New("mime: read header: empty input")
	}
	p := &parser{limits: limits, out: &Parsed{Headers: map[string]string{}}}
	if err != nil {
		// Unknown charset or transfer encoding of the root entity: Walk does
		// not forward the root's own error, so record it here.
		p.problem("part 1: " + err.Error())
	}
	p.envelope(root.Header)
	if werr := root.Walk(p.visit); werr != nil && !errors.Is(werr, errStop) {
		p.problem("walk: " + werr.Error())
	}
	if lr.N == 0 {
		p.out.Truncated = true
		p.problem("input exceeds MaxInputBytes")
	}
	p.finish()
	return p.out, nil
}

// parser carries the state of one Parse call.
type parser struct {
	limits Limits
	out    *Parsed

	parts             int
	textDone          bool
	htmlDone          bool
	attachmentsCapped bool
	problemsCapped    bool
}

func (p *parser) problem(s string) {
	if len(p.out.Problems) >= maxProblems {
		if !p.problemsCapped {
			p.problemsCapped = true
			p.out.Problems = append(p.out.Problems, "further problems omitted")
		}
		return
	}
	p.out.Problems = append(p.out.Problems, cleanField(s, p.limits.MaxFieldBytes))
}

// visit is the message.WalkFunc. It consumes the bodies of leaf parts and
// leaves multipart containers untouched so Walk can descend into them.
func (p *parser) visit(path []int, e *message.Entity, err error) error {
	p.parts++
	if p.parts > p.limits.MaxParts {
		p.out.Truncated = true
		p.problem(fmt.Sprintf("more than %d parts", p.limits.MaxParts))
		return errStop
	}
	if len(path) > p.limits.MaxDepth {
		p.out.Truncated = true
		p.problem(fmt.Sprintf("nesting deeper than %d", p.limits.MaxDepth))
		return errStop
	}
	id := partID(path)
	if err != nil {
		p.problem("part " + id + ": " + err.Error())
	}
	mediaType, params, ctErr := e.Header.ContentType()
	if ctErr != nil {
		p.problem("part " + id + ": content-type: " + ctErr.Error())
	}
	// Mirror go-message's own test: it descends exactly when the (possibly
	// unparsed) media type starts with "multipart/".
	if strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	ct := normalizeMediaType(mediaType)
	disp, dparams, _ := e.Header.ContentDisposition()
	disp = strings.ToLower(strings.TrimSpace(disp))
	isAttachment := disp == "attachment"

	switch {
	case ct == "text/plain" && !isAttachment && !p.textDone:
		p.textDone = true
		p.out.TextPartID = id
		p.out.Text = p.readText(id, e.Body)
		return nil
	case ct == "text/html" && !isAttachment && !p.htmlDone:
		p.htmlDone = true
		p.out.HTMLPartID = id
		p.out.HasHTML = true
		p.out.RawHTML = p.readText(id, e.Body)
		return nil
	}

	// Everything else is an attachment. The body must be consumed anyway so
	// the multipart reader can find the next boundary; count it on the way.
	size, cerr := io.Copy(io.Discard, e.Body)
	if cerr != nil {
		p.problem("part " + id + ": body: " + cerr.Error())
	}
	if len(p.out.Attachments) >= p.limits.MaxAttachments {
		if !p.attachmentsCapped {
			p.attachmentsCapped = true
			p.out.Truncated = true
			p.problem(fmt.Sprintf("more than %d attachments", p.limits.MaxAttachments))
		}
		return nil
	}
	contentID := cleanID(strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>"), p.limits.MaxFieldBytes)
	p.out.Attachments = append(p.out.Attachments, api.Attachment{
		PartID:      id,
		Filename:    attachmentName(id, ct, params, dparams),
		ContentType: ct,
		Size:        size,
		Inline:      contentID != "" && !isAttachment,
		ContentID:   contentID,
	})
	return nil
}

// readText reads a text body through the MaxTextBytes cap and cleans it.
func (p *parser) readText(id string, r io.Reader) string {
	max := p.limits.MaxTextBytes
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		p.problem("part " + id + ": body: " + err.Error())
	}
	if int64(len(buf)) > max {
		p.out.Truncated = true
		p.problem("part " + id + ": text exceeds MaxTextBytes")
	}
	s := cleanText(string(buf))
	if int64(len(s)) > max {
		s = truncateBytes(s, int(max))
	}
	return s
}

// finish derives the fields that depend on the whole walk.
func (p *parser) finish() {
	out := p.out
	// Trailing whitespace carries nothing and differs between a root body
	// (keeps its final CRLF) and a multipart part (the CRLF before the
	// boundary belongs to the delimiter); normalise it away.
	out.Text = strings.TrimRight(out.Text, " \t\n")
	if out.Text == "" && out.HasHTML {
		out.Text = HTMLToText(out.RawHTML, p.limits.MaxTextBytes)
	}
	out.Snippet = Snippet(out.Text, p.limits.MaxSnippetRunes)
	for _, a := range out.Attachments {
		if !a.Inline {
			out.HasAttachments = true
			break
		}
	}
}

// partID formats a Walk path as an IMAP part number: children of the root
// are "1", "2", …, nested parts "2.1". A non-multipart root is "1".
func partID(path []int) string {
	if len(path) == 0 {
		return "1"
	}
	var b strings.Builder
	for i, n := range path {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(n + 1))
	}
	return b.String()
}

// normalizeMediaType reduces a leaf part's Content-Type value to a
// lower-case "type/subtype" and falls back to application/octet-stream when
// the value is not a valid media type (go-message returns the raw header on
// parse errors) or claims to be multipart (a leaf whose multipart header
// could not be parsed is opaque data).
func normalizeMediaType(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	t, sub, ok := strings.Cut(s, "/")
	if !ok || len(s) > 127 || t == "multipart" || !isToken(t) || !isToken(sub) {
		return "application/octet-stream"
	}
	return s
}

// isToken reports whether s is a non-empty RFC 2045 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("()<>@,;:\\\"/[]?=", c) >= 0 {
			return false
		}
	}
	return true
}

// extensionByType lists the media types whose file extension is obvious;
// it deliberately does not consult the system mime.types table so fallback
// names are stable across machines.
var extensionByType = map[string]string{
	"text/plain":       ".txt",
	"text/html":        ".html",
	"text/calendar":    ".ics",
	"text/csv":         ".csv",
	"message/rfc822":   ".eml",
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"image/svg+xml":    ".svg",
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"application/json": ".json",
}

// attachmentName picks the file name from Content-Disposition (RFC 2231
// values are already decoded by go-message) or the Content-Type name
// parameter, sanitises it and falls back to "attachment-<partID>".
func attachmentName(id, ct string, params, dparams map[string]string) string {
	raw := dparams["filename"]
	if raw == "" {
		raw = params["name"]
	}
	if raw != "" {
		if name := safename.Filename(raw); name != safename.Fallback {
			return name
		}
	}
	return safename.Fallback + "-" + id + extensionByType[ct]
}

// countingReader counts the bytes delivered from r.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
