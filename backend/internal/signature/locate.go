// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package signature decides which signature, if any, is the verdict of a
// whole message (plan S4), cuts the signed bytes out of the raw message
// exactly as they are in the file and canonicalises line endings (S3),
// reads protected headers (S6) and composes the verdict of the findings of
// internal/smime or internal/pgpsig (plan §3). Pure: no store, no network,
// no clock. docs/plans/signatures.md.
package signature

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	stdmime "mime"
	"mime/quotedprintable"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/textproto"

	"github.com/schotek/malachi/backend/pkg/api"
)

// VerifierVersion names the rules of this package, internal/smime and
// internal/pgpsig; a stored verdict of another version is computed again
// (the backfill in core.Maintain).
const VerifierVersion = "1"

// Limits of Locate.
const (
	// MaxMessageBytes caps the raw message Locate looks at.
	MaxMessageBytes = 32 << 20
	// MaxHeaderBytes caps a header block.
	MaxHeaderBytes = 256 << 10
	// MaxSignatureBytes caps a detached signature part (decoded).
	MaxSignatureBytes = 1 << 20
	// MaxParts caps the parts of a multipart Locate splits.
	MaxParts = 500
)

// ErrNone: the message has no signature that is the verdict of the whole
// message (none at all, one deeper than S4 allows, or another protocol).
var ErrNone = errors.New("signature: no signature of the whole message")

// Located is the signature of a whole message as found in the raw bytes.
type Located struct {
	Kind api.SignatureKind
	// Opaque: S/MIME signed-data; Signature is the CMS and the content is
	// inside it. Inline: an inline OpenPGP signature, nothing to verify.
	Opaque bool
	Inline bool
	// Footer: the signed part is the first of a multipart/mixed whose only
	// other part is a text/plain after it (a list's footer, Q7).
	Footer bool
	// Malformed: the structure that names a signature is broken (no
	// closing boundary, not two parts); the verdict is invalid/malformed.
	Malformed bool
	// Signed is the signed entity (its header and body) exactly as in the
	// file, for multipart/signed; Signature the decoded signature part
	// (multipart/signed) or the decoded CMS (opaque).
	Signed    []byte
	Signature []byte
	// SigPartID is the signature part's number (the MIME parser's), for
	// multipart/signed.
	SigPartID string
}

// entity is one MIME entity cut out of a buffer.
type entity struct {
	raw    []byte // header and body as in the file
	header textproto.Header
	body   []byte
	ctype  string
	params map[string]string
}

// parseEntity splits raw at its header's end and reads the header.
func parseEntity(raw []byte) (*entity, bool) {
	end, body := headerEnd(raw)
	if end > MaxHeaderBytes {
		return nil, false
	}
	hb := make([]byte, 0, end+2)
	hb = append(hb, raw[:end]...)
	hb = append(hb, '\r', '\n')
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(hb)))
	if err != nil {
		return nil, false
	}
	e := &entity{raw: raw, header: h, body: raw[body:]}
	ct := h.Get("Content-Type")
	if ct == "" {
		e.ctype = "text/plain"
		return e, true
	}
	mt, params, err := stdmime.ParseMediaType(ct)
	if err != nil {
		// An unreadable Content-Type is not a structure we verify.
		e.ctype = "application/octet-stream"
		return e, true
	}
	e.ctype = strings.ToLower(mt)
	e.params = params
	return e, true
}

// headerEnd returns the length of the header block (up to, excluding,
// the empty line) and where the body starts. A header without an empty
// line has an empty body.
func headerEnd(raw []byte) (end, body int) {
	pos := 0
	for pos < len(raw) {
		nl := bytes.IndexByte(raw[pos:], '\n')
		if nl < 0 {
			return len(raw), len(raw)
		}
		line := raw[pos : pos+nl]
		if len(line) == 0 || (len(line) == 1 && line[0] == '\r') {
			return pos, pos + nl + 1
		}
		pos += nl + 1
	}
	return len(raw), len(raw)
}

// splitMultipart cuts body at the delimiter lines of boundary (RFC 2046
// §5.1.1): each part runs from the line after a delimiter to the line
// ending before the next one, exactly as in the file. closed is false
// when the closing delimiter is missing.
func splitMultipart(body []byte, boundary string) (parts [][]byte, closed bool, ok bool) {
	if boundary == "" || len(boundary) > 200 {
		return nil, false, false
	}
	dash := []byte("--" + boundary)
	start := -1 // start of the current part's content
	pos := 0
	for pos <= len(body) {
		nl := bytes.IndexByte(body[pos:], '\n')
		lineEnd := len(body)
		next := len(body) + 1
		if nl >= 0 {
			lineEnd = pos + nl
			next = lineEnd + 1
		}
		line := body[pos:lineEnd]
		if bytes.HasPrefix(line, dash) {
			rest := bytes.TrimRight(line[len(dash):], " \t\r")
			closing := bytes.Equal(rest, []byte("--"))
			if len(rest) == 0 || closing {
				if start >= 0 {
					end := pos
					// The line ending before the delimiter belongs to it.
					if end > start && body[end-1] == '\n' {
						end--
						if end > start && body[end-1] == '\r' {
							end--
						}
					}
					parts = append(parts, body[start:end])
					if len(parts) > MaxParts {
						return nil, false, false
					}
				}
				if closing {
					return parts, true, true
				}
				start = next
				if start > len(body) {
					start = len(body)
				}
			}
		}
		if nl < 0 {
			break
		}
		pos = next
	}
	return parts, false, true
}

// Locate finds the signature that is the verdict of the message raw (plan
// S4): a multipart/signed or an opaque signed-data as the root, or as the
// first part of a root multipart/mixed whose only other part is a
// text/plain after it; else an inline OpenPGP signature at the start of
// the root text (or of the first part of a root multipart). ErrNone
// otherwise.
func Locate(raw []byte) (*Located, error) {
	if len(raw) > MaxMessageBytes {
		raw = raw[:MaxMessageBytes]
	}
	root, ok := parseEntity(raw)
	if !ok {
		return nil, ErrNone
	}
	if loc, err := signedEntity(root, "1", "2"); !errors.Is(err, ErrNone) {
		return loc, err
	}
	if strings.HasPrefix(root.ctype, "multipart/") {
		parts, _, ok := splitMultipart(root.body, root.params["boundary"])
		if !ok || len(parts) == 0 {
			return nil, ErrNone
		}
		if root.ctype == "multipart/mixed" && len(parts) == 2 {
			first, ok1 := parseEntity(parts[0])
			footer, ok2 := parseEntity(parts[1])
			if ok1 && ok2 && footer.ctype == "text/plain" && !isAttachment(footer) {
				loc, err := signedEntity(first, "1.1", "1.2")
				if err == nil {
					loc.Footer = true
				}
				if !errors.Is(err, ErrNone) {
					return loc, err
				}
			}
		}
		if first, ok := parseEntity(parts[0]); ok && first.ctype == "text/plain" && inline(first) {
			return &Located{Kind: api.SignaturePGP, Inline: true}, nil
		}
		return nil, ErrNone
	}
	if root.ctype == "text/plain" && inline(root) {
		return &Located{Kind: api.SignaturePGP, Inline: true}, nil
	}
	return nil, ErrNone
}

// signedEntity reads e as multipart/signed or opaque signed-data; the part
// numbers are what the MIME parser gives e's children (or e itself).
func signedEntity(e *entity, signedID, sigID string) (*Located, error) {
	switch e.ctype {
	case "multipart/signed":
		kind, ok := protocolKind(e.params["protocol"])
		if !ok {
			return nil, ErrNone
		}
		loc := &Located{Kind: kind, SigPartID: sigID}
		parts, closed, ok := splitMultipart(e.body, e.params["boundary"])
		if !ok || !closed || len(parts) != 2 {
			loc.Malformed = true
			return loc, nil
		}
		sigEnt, ok := parseEntity(parts[1])
		if !ok {
			loc.Malformed = true
			return loc, nil
		}
		sig, err := decodeBody(sigEnt, MaxSignatureBytes)
		if err != nil {
			loc.Malformed = true
			return loc, nil
		}
		loc.Signed = parts[0]
		loc.Signature = sig
		_ = signedID
		return loc, nil
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		st := strings.ToLower(e.params["smime-type"])
		if st != "signed-data" && st != "" {
			return nil, ErrNone // enveloped data and others: not phase 1
		}
		cms, err := decodeBody(e, MaxMessageBytes)
		if err != nil {
			return &Located{Kind: api.SignatureSMIME, Opaque: true, Malformed: true}, nil
		}
		return &Located{Kind: api.SignatureSMIME, Opaque: true, Signature: cms}, nil
	}
	return nil, ErrNone
}

// protocolKind maps multipart/signed's protocol parameter.
func protocolKind(p string) (api.SignatureKind, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "application/pkcs7-signature", "application/x-pkcs7-signature":
		return api.SignatureSMIME, true
	case "application/pgp-signature":
		return api.SignaturePGP, true
	}
	return "", false
}

func isAttachment(e *entity) bool {
	d := e.header.Get("Content-Disposition")
	if d == "" {
		return false
	}
	disp, _, err := stdmime.ParseMediaType(d)
	return err == nil && strings.EqualFold(disp, "attachment")
}

// inline: the decoded text starts with a cleartext signature.
func inline(e *entity) bool {
	b, err := decodeBody(e, 4096)
	if err != nil && !errors.Is(err, errTooBig) {
		return false
	}
	t := strings.TrimLeft(string(b), " \t\r\n\ufeff")
	line, _, _ := strings.Cut(t, "\n")
	return strings.TrimRight(line, " \t\r") == "-----BEGIN PGP SIGNED MESSAGE-----"
}

var errTooBig = errors.New("signature: part too large")

// decodeBody undoes the entity's transfer encoding, at most max bytes
// (errTooBig beyond, with the first max bytes).
func decodeBody(e *entity, max int) ([]byte, error) {
	cte := strings.ToLower(strings.TrimSpace(e.header.Get("Content-Transfer-Encoding")))
	var r io.Reader
	switch cte {
	case "base64":
		clean := bytes.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\r', '\n':
				return -1
			}
			return r
		}, e.body)
		r = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(clean))
	case "quoted-printable":
		r = quotedprintable.NewReader(bytes.NewReader(e.body))
	case "", "7bit", "8bit", "binary":
		r = bytes.NewReader(e.body)
	default:
		return nil, errors.New("signature: unknown transfer encoding")
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if len(b) > max {
		return b[:max], errTooBig
	}
	if err != nil {
		// A base64 body without padding: what was decoded counts when the
		// rest was only padding trouble.
		var ce base64.CorruptInputError
		if errors.As(err, &ce) && len(b) > 0 {
			return b, nil
		}
		return nil, err
	}
	return b, nil
}

// Canonical returns b with every bare LF made CRLF (RFC 8551 §3.1.1, RFC
// 3156 §5): the form a signer hashed. b itself when nothing changes.
func Canonical(b []byte) []byte {
	n := 0
	for i, c := range b {
		if c == '\n' && (i == 0 || b[i-1] != '\r') {
			n++
		}
	}
	if n == 0 {
		return b
	}
	out := make([]byte, 0, len(b)+n)
	for i, c := range b {
		if c == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	return out
}

// messageHeader wraps an entity's header for go-message's typed readers.
func messageHeader(e *entity) message.Header { return message.Header{Header: e.header} }

// Candidate is a cheap first look: whether raw may hold a signature Locate
// would find (the words of the media types or the inline marker anywhere),
// so mail without one is not parsed again.
func Candidate(raw []byte, _ []api.Attachment) bool {
	for _, w := range []string{"pkcs7", "PKCS7", "Pkcs7", "pgp-signature", "PGP-SIGNATURE", "Pgp-Signature", "-----BEGIN PGP SIGNED MESSAGE-----"} {
		if bytes.Contains(raw, []byte(w)) {
			return true
		}
	}
	return false
}

// LooksBulk: the message's header marks it as list or bulk mail (List-Id,
// List-Unsubscribe, Precedence bulk/list/junk); Autocrypt keys of such mail
// are never taken (plan §5).
func LooksBulk(raw []byte) bool {
	e, ok := parseEntity(raw)
	if !ok {
		return true
	}
	if e.header.Get("List-Id") != "" || e.header.Get("List-Unsubscribe") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(e.header.Get("Precedence"))) {
	case "bulk", "list", "junk":
		return true
	}
	return false
}
