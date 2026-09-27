// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/textproto"
)

// ErrNotReducible reports that a message cannot be reduced to a skeleton,
// or that a skeleton does not show what its original shows. The caller
// keeps the message whole. Errors wrap it with the reason.
var ErrNotReducible = errors.New("mime: message cannot be reduced")

// maxSkeletonBoundary bounds the boundaries a skeleton writes, so every
// delimiter line fits the multipart reader's 4096-byte buffer with room to
// spare (RFC 2046 allows 70 bytes; longer ones exist in the wild).
const maxSkeletonBoundary = 1024

// errHeaderTooBig is what headerLimit returns once the top-level header
// used up its budget.
var errHeaderTooBig = errors.New("header exceeds MaxHeaderBytes")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotReducible, fmt.Sprintf(format, args...))
}

// Skeleton copies the message read from r to w with the bodies of the leaf
// parts named in omit (part numbers as Parse reports them, Attachment.PartID)
// left empty, and returns the numbers it omitted, in message order.
//
// Everything else is kept: every entity's header as go-message reads it (the
// same fields with the same raw values; line endings become CRLF, and a
// line without a field name, which go-message skips, is gone) and the bodies
// of the other leaves byte for byte, so ExtractPart returns the same data
// for them and an empty body for an omitted one.
// The multipart structure is written again with the same boundaries and
// CRLF delimiter lines; preambles and epilogues, which Parse never looks
// at, are dropped.
//
// The message is split into parts exactly as Parse splits it: the same
// go-message readers, the same limits, the same part numbering. Anything
// that makes that split doubtful is refused with an error wrapping
// ErrNotReducible: a multipart without a usable boundary, any reader error
// (a missing final boundary, a malformed part header or one that ends the
// input, a delimiter line longer than the reader's buffer), more than
// MaxParts entities or nesting deeper than MaxDepth, a top-level header over
// MaxHeaderBytes, input cut at MaxInputBytes, a signed or encrypted
// structure (Parsed.Crypto), output that a reader further out would split
// differently from the input, and an omit entry that names no leaf. A read
// error of r or a write error of w is returned as such, not as
// ErrNotReducible. After any error w holds a partial copy to discard.
//
// The result is only a candidate: the caller parses it and accepts it only
// when VerifySkeleton agrees with the parse of the original.
func Skeleton(r io.Reader, w io.Writer, omit map[string]bool, limits Limits) (omitted []string, err error) {
	limits = limits.withDefaults()
	// The reader chain of Parse (MaxInputBytes, the byte count that detects
	// empty input) and of message.ReadWithOptions (the header budget, then
	// a default-sized bufio.Reader), with the source's own errors recorded.
	src := &sourceReader{r: r}
	lr := &io.LimitedReader{R: src, N: MaxInputBytes}
	cr := &countingReader{r: lr}
	hl := &headerLimit{r: cr, n: limits.MaxHeaderBytes}
	br := bufio.NewReader(hl)

	s := &skeleton{limits: limits, omit: omit, w: &errWriter{w: w}}
	err = s.message(br, hl, cr)
	switch {
	case err != nil:
	case lr.N == 0:
		err = refuse("input exceeds MaxInputBytes")
	case len(s.omitted) != countTrue(omit):
		err = refuse("only %d of the %d parts to omit are leaves", len(s.omitted), countTrue(omit))
	}
	if err != nil {
		switch {
		case s.w.err != nil:
			return nil, fmt.Errorf("mime: skeleton: write: %w", s.w.err)
		case src.err != nil:
			return nil, fmt.Errorf("mime: skeleton: read: %w", src.err)
		}
		return nil, err
	}
	return s.omitted, nil
}

// skeleton carries the state of one Skeleton call.
type skeleton struct {
	limits  Limits
	omit    map[string]bool
	w       *errWriter
	omitted []string
	parts   int
}

// message reads the top-level header the way message.ReadWithOptions does
// and writes the whole message.
func (s *skeleton) message(br *bufio.Reader, hl *headerLimit, cr *countingReader) error {
	h, err := textproto.ReadHeader(br)
	if err != nil {
		return refuse("read header: %v", err)
	}
	if cr.n == 0 {
		return refuse("empty input")
	}
	hl.n = math.MaxInt64
	return s.entity(nil, nil, h, br)
}

// entity writes one entity: its header h, already read, and its body, read
// from body. path is its Walk path (nil for the root) and ancestors the
// boundaries of the multiparts it sits in, outermost first.
//
// Walk visits an entity, counts it against the limits and descends exactly
// when go-message's media type starts with "multipart/"; this mirrors it.
func (s *skeleton) entity(path []int, ancestors []string, h textproto.Header, body io.Reader) error {
	s.parts++
	if s.parts > s.limits.MaxParts {
		return refuse("more than %d parts", s.limits.MaxParts)
	}
	if len(path) > s.limits.MaxDepth {
		return refuse("nesting deeper than %d", s.limits.MaxDepth)
	}
	id := partID(path)
	mh := message.Header{Header: h}
	mediaType, params, _ := mh.ContentType()
	if isCryptoType(mediaType) {
		return refuse("part %s is signed or encrypted", id)
	}
	var limit int64 // only the top-level header has a budget
	if path == nil {
		limit = s.limits.MaxHeaderBytes
	}
	if err := s.header(h, outer(ancestors), limit); err != nil {
		return err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		return s.multipart(path, ancestors, params["boundary"], body)
	}
	return s.leaf(id, ancestors, body)
}

// header writes h with textproto.WriteHeader: the fields as read, with CRLF
// line endings, then the blank line. The parent multipart reads a header
// with ReadHeader, but the readers further out (scan) see it as raw lines of
// their own part, now after a CRLF where the input may have had a bare LF;
// none of those lines may read as one of their delimiters. limit, when
// positive, is the budget the header must still fit once CRLF made it
// longer, so that the skeleton parses.
func (s *skeleton) header(h textproto.Header, scan []string, limit int64) error {
	var buf bytes.Buffer
	if err := textproto.WriteHeader(&buf, h); err != nil {
		return refuse("write header: %v", err)
	}
	if limit > 0 && int64(buf.Len()) > limit {
		return refuse("header grows over MaxHeaderBytes")
	}
	if len(scan) > 0 {
		for b := buf.Bytes(); len(b) > 0; {
			if delimiterLike(b, scan) {
				return refuse("a header line reads as a delimiter")
			}
			i := bytes.IndexByte(b, '\n')
			if i < 0 {
				break
			}
			b = b[i+1:]
		}
	}
	_, err := s.w.Write(buf.Bytes())
	return err
}

// multipart writes the body of a multipart entity: its parts, each after a
// CRLF delimiter line, and the close delimiter. The reader is the one
// go-message's Walk uses, with the boundary Walk uses.
func (s *skeleton) multipart(path []int, ancestors []string, boundary string, body io.Reader) error {
	id := partID(path)
	switch {
	case boundary == "":
		return refuse("part %s: multipart without a boundary", id)
	case len(boundary) > maxSkeletonBoundary:
		return refuse("part %s: boundary of %d bytes", id, len(boundary))
	}
	open := "--" + boundary + "\r\n"
	closing := "--" + boundary + "--\r\n"
	// Every enclosing reader scans these lines as part of its own part.
	if delimiterLike([]byte(open), ancestors) || delimiterLike([]byte(closing), ancestors) {
		return refuse("part %s: boundary collides with an enclosing one", id)
	}
	mr := textproto.NewMultipartReader(body, boundary)
	inner := append(slices.Clip(ancestors), boundary)
	child := append(slices.Clip(path), -1)
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return refuse("part %s: %v", id, err)
		}
		child[len(child)-1]++
		lead := open
		if n > 0 {
			lead = "\r\n" + open
		}
		n++
		if _, err := io.WriteString(s.w, lead); err != nil {
			return err
		}
		if err := s.entity(child, inner, part.Header, part); err != nil {
			return err
		}
	}
	if n > 0 {
		closing = "\r\n" + closing
	}
	_, err := io.WriteString(s.w, closing)
	return err
}

// leaf writes a leaf's body: nothing when it is to be omitted (it is still
// read to its end, as Parse does), otherwise every byte of it.
func (s *skeleton) leaf(id string, ancestors []string, body io.Reader) error {
	if s.omit[id] {
		if _, err := io.Copy(io.Discard, body); err != nil {
			return refuse("part %s: %v", id, err)
		}
		s.omitted = append(s.omitted, id)
		return nil
	}
	if scan := outer(ancestors); len(scan) > 0 {
		// The body now follows the CRLF that ends the header, where the
		// input may have had a bare LF: its first line must not read as a
		// delimiter of a reader further out. Its later lines keep their own
		// line endings, and the multipart reader it belongs to ends it
		// exactly where it ended before.
		longest := 0
		for _, b := range scan {
			longest = max(longest, len(b))
		}
		head, done, err := readHead(body, longest+3)
		if err != nil {
			return refuse("part %s: %v", id, err)
		}
		probe := head
		if done {
			probe = append(slices.Clip(head), "\r\n"...) // what follows it
		}
		if delimiterLike(probe, scan) {
			return refuse("part %s: its first line reads as a delimiter", id)
		}
		if _, err := s.w.Write(head); err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	if _, err := io.Copy(s.w, body); err != nil {
		return refuse("part %s: %v", id, err)
	}
	return nil
}

// outer returns the boundaries of the readers that see an entity's header
// and leaf body as plain bytes of their own part: all enclosing multiparts
// but the innermost, which parses the header and finds the body's end.
func outer(ancestors []string) []string {
	if len(ancestors) == 0 {
		return nil
	}
	return ancestors[:len(ancestors)-1]
}

// delimiterLike reports whether the line that starts b would end a part of
// a multipart with one of the boundaries: "--", the boundary, then a space,
// tab, CR, LF, "-" or the end of b. That is the test textproto's part
// reader applies after a newline (matchAfterPrefix), in either newline mode.
func delimiterLike(b []byte, boundaries []string) bool {
	if len(b) < 2 || b[0] != '-' || b[1] != '-' {
		return false
	}
	for _, bd := range boundaries {
		if !bytes.HasPrefix(b[2:], []byte(bd)) {
			continue
		}
		rest := b[2+len(bd):]
		if len(rest) == 0 || strings.IndexByte(" \t\r\n-", rest[0]) >= 0 {
			return true
		}
	}
	return false
}

// readHead reads up to n bytes of r; done reports that r ended within them.
func readHead(r io.Reader, n int) (head []byte, done bool, err error) {
	head = make([]byte, 0, n)
	for len(head) < n {
		m, err := r.Read(head[len(head):n])
		head = head[:len(head)+m]
		if err == io.EOF {
			return head, true, nil
		}
		if err != nil {
			return head, false, err
		}
	}
	return head, false, nil
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

// sourceReader records the first error of the underlying reader other than
// io.EOF, so that a failing disk is told apart from a malformed message.
type sourceReader struct {
	r   io.Reader
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF && s.err == nil {
		s.err = err
	}
	return n, err
}

// headerLimit is message.ReadWithOptions' header budget: reads fail once n
// bytes were delivered, until the caller lifts the limit.
type headerLimit struct {
	r io.Reader
	n int64
}

func (l *headerLimit) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errHeaderTooBig
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// errWriter records the first write error; every later write fails with it.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		e.err = err
	}
	return n, err
}

// VerifySkeleton checks that skel, the Parse result of a skeleton, shows
// everything orig, the Parse result of its original (same limits), shows:
// the same envelope, curated headers, text and HTML bodies, part numbers of
// the bodies, snippet and attachment list, every attachment with its
// original size except the omitted ones, which must be there with size 0.
// Neither parse may be truncated or signed or encrypted, or come from a walk
// that did not reach the final boundary, and both must have walked the
// same number of entities. Problems are not compared: an omitted part can
// no longer report its broken encoding. A mismatch returns an error
// wrapping ErrNotReducible.
func VerifySkeleton(orig, skel *Parsed, omitted []string) error {
	switch {
	case orig == nil || skel == nil:
		return refuse("nothing to compare")
	case orig.Truncated || skel.Truncated:
		return refuse("a parse is truncated")
	case orig.Crypto || skel.Crypto:
		return refuse("signed or encrypted")
	case orig.walkFailed || skel.walkFailed:
		return refuse("the part structure does not read to its end")
	case orig.entities != skel.entities:
		return refuse("%d entities, the skeleton has %d", orig.entities, skel.entities)
	case orig.Subject != skel.Subject,
		!slices.Equal(orig.From, skel.From),
		!slices.Equal(orig.To, skel.To),
		!slices.Equal(orig.CC, skel.CC),
		!slices.Equal(orig.BCC, skel.BCC),
		!slices.Equal(orig.ReplyTo, skel.ReplyTo),
		!orig.Date.Equal(skel.Date),
		orig.MessageID != skel.MessageID,
		orig.InReplyTo != skel.InReplyTo,
		!slices.Equal(orig.References, skel.References),
		!maps.Equal(orig.Headers, skel.Headers):
		return refuse("the envelope differs")
	case orig.Text != skel.Text,
		orig.HasHTML != skel.HasHTML,
		orig.RawHTML != skel.RawHTML,
		orig.TextPartID != skel.TextPartID,
		orig.HTMLPartID != skel.HTMLPartID,
		orig.Snippet != skel.Snippet,
		orig.HasAttachments != skel.HasAttachments:
		return refuse("the bodies differ")
	case len(orig.Attachments) != len(skel.Attachments):
		return refuse("%d attachments, the skeleton has %d", len(orig.Attachments), len(skel.Attachments))
	}
	omit := make(map[string]bool, len(omitted))
	for _, id := range omitted {
		if omit[id] {
			return refuse("part %s omitted twice", id)
		}
		omit[id] = true
	}
	found := 0
	for i, a := range orig.Attachments {
		b := skel.Attachments[i]
		if a.PartID != b.PartID || a.Filename != b.Filename || a.ContentType != b.ContentType ||
			a.Inline != b.Inline || a.ContentID != b.ContentID || a.Remote != b.Remote {
			return refuse("attachment %s differs", a.PartID)
		}
		switch {
		case omit[a.PartID]:
			found++
			if b.Size != 0 {
				return refuse("omitted part %s still has %d bytes", a.PartID, b.Size)
			}
		case a.Size != b.Size:
			return refuse("part %s has %d bytes, the skeleton %d", a.PartID, a.Size, b.Size)
		}
	}
	if found != len(omit) {
		return refuse("an omitted part is not an attachment of the original")
	}
	return nil
}
