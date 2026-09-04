// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	netmail "net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-message/mail"

	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// userAgent is the User-Agent header of every outgoing message.
	userAgent = "Malachi Mail"
	// maxHeaderBytes caps one cleaned header value.
	maxHeaderBytes = 2048
	// fallbackDomain is the Message-ID domain when the sender address has
	// no usable one. ".invalid" is reserved (RFC 2606) so it never routes.
	fallbackDomain = "malachi.invalid"
	// fallbackContentType is used for attachments whose declared type does
	// not parse or is a container type.
	fallbackContentType = "application/octet-stream"
)

// ErrInvalidAddress is returned by BuildMessage when a From/To/Cc entry
// cannot be formatted into a single, unambiguous RFC 5322 mailbox.
var ErrInvalidAddress = api.NewError(api.CodeInvalidArgument, "invalid email address")

// Attachment is one file to attach. Open is called once, while the
// message is being written; the returned reader is closed by BuildMessage.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int64
	Open        func() (io.ReadCloser, error)
}

// BuildInput is everything BuildMessage needs. There is deliberately no
// Bcc field: blind recipients belong to the SMTP envelope only.
type BuildInput struct {
	From api.Address
	To   []api.Address
	CC   []api.Address

	Subject string
	Text    string // UTF-8 plain text; CRLF and bare CR are normalised to LF

	InReplyTo  string   // bare id without <>; empty = omit
	References []string // bare ids without <>; empty = omit

	Date      time.Time // zero = now
	MessageID string    // bare id; NewMessageID(From.Address) when empty

	Attachments []Attachment
}

// NewMessageID returns a fresh "<32 hex random>@<domain>" identifier
// without angle brackets. domain is the lower-cased part after the last
// '@' of email; when there is none, or it contains characters that could
// not appear in a msg-id, the reserved fallback domain is used.
func NewMessageID(email string) string {
	var b [16]byte
	// crypto/rand.Read never returns an error on Linux (Go ≥ 1.24 makes
	// that a documented guarantee).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:]) + "@" + messageIDDomain(email)
}

func messageIDDomain(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return fallbackDomain
	}
	d := strings.ToLower(email[i+1:])
	if !utf8.ValidString(d) {
		return fallbackDomain
	}
	for _, r := range d {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`<>()[]:;,\"@`, r) {
			return fallbackDomain
		}
	}
	return d
}

// BuildMessage writes an RFC 5322 message to w. See BuildInput for the
// headers; the body is text/plain (quoted-printable) alone, or a
// multipart/mixed with the text as inline part and each attachment as a
// base64 part. Errors are *api.Error, except: errors from w are returned
// unchanged so that a counting writer's own sentinel propagates, and an
// attachment Open/read error is returned wrapped (errors.Is/As work).
func BuildMessage(w io.Writer, in BuildInput) error {
	h, err := buildHeader(in)
	if err != nil {
		return err
	}
	ew := &errWriter{w: w}
	err = writeBody(ew, h, in)
	if ew.err != nil {
		return ew.err
	}
	return err
}

func buildHeader(in BuildInput) (mail.Header, error) {
	var h mail.Header

	from, err := formatAddress(in.From)
	if err != nil {
		return h, err
	}
	h.SetAddressList("From", []*mail.Address{from})

	to, err := formatAddresses(in.To)
	if err != nil {
		return h, err
	}
	if len(to) > 0 {
		h.SetAddressList("To", to)
	}
	cc, err := formatAddresses(in.CC)
	if err != nil {
		return h, err
	}
	if len(cc) > 0 {
		h.SetAddressList("Cc", cc)
	}

	h.SetSubject(cleanHeader(in.Subject))

	date := in.Date
	if date.IsZero() {
		date = time.Now()
	}
	h.SetDate(date)

	id := cleanMsgID(in.MessageID)
	if id == "" {
		id = NewMessageID(from.Address)
	}
	h.SetMessageID(id)

	if irt := cleanMsgID(in.InReplyTo); irt != "" {
		h.SetMsgIDList("In-Reply-To", []string{irt})
	}
	if refs := cleanMsgIDs(in.References); len(refs) > 0 {
		h.SetMsgIDList("References", refs)
	}

	// MIME-Version is added by the message writer.
	h.Set("User-Agent", userAgent)
	return h, nil
}

func writeBody(w io.Writer, h mail.Header, in BuildInput) error {
	text := normaliseNewlines(in.Text)

	if len(in.Attachments) == 0 {
		h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := mail.CreateSingleInlineWriter(w, h)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(pw, text); err != nil {
			return err
		}
		return pw.Close()
	}

	mw, err := mail.CreateWriter(w, h)
	if err != nil {
		return err
	}
	var ih mail.InlineHeader
	ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	pw, err := mw.CreateSingleInline(ih)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(pw, text); err != nil {
		return err
	}
	if err := pw.Close(); err != nil {
		return err
	}

	for i, a := range in.Attachments {
		if err := writeAttachment(mw, i, a); err != nil {
			return err
		}
	}
	return mw.Close()
}

func writeAttachment(mw *mail.Writer, i int, a Attachment) error {
	var ah mail.AttachmentHeader
	ah.SetContentType(attachmentContentType(a.ContentType))
	// Not AttachmentHeader.SetFilename: go-message puts an RFC 2047
	// encoded-word inside the parameter, while mime.FormatMediaType emits
	// the standard RFC 2231 filename*=utf-8''… form for non-ASCII names.
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": safename.Filename(a.Filename)})
	if disp == "" {
		disp = mime.FormatMediaType("attachment", map[string]string{"filename": safename.Fallback})
	}
	ah.Set("Content-Disposition", disp)

	if a.Open == nil {
		return api.NewError(api.CodeInvalidArgument, "attachment %d has no content", i)
	}
	rc, err := a.Open()
	if err != nil {
		return fmt.Errorf("opening attachment %d: %w", i, err)
	}
	defer rc.Close()

	aw, err := mw.CreateAttachment(ah)
	if err != nil {
		return err
	}
	if _, err := io.Copy(aw, rc); err != nil {
		// The caller distinguishes writer errors via errWriter; anything
		// else came from the attachment reader.
		return fmt.Errorf("reading attachment %d: %w", i, err)
	}
	return aw.Close()
}

// attachmentContentType validates a declared media type. Container types
// are demoted to octet-stream so that an attachment can never be
// re-interpreted as message structure.
func attachmentContentType(ct string) (string, map[string]string) {
	t, params, err := mime.ParseMediaType(cleanHeader(ct))
	if err != nil || t == "" || strings.HasPrefix(t, "multipart/") || strings.HasPrefix(t, "message/") {
		return fallbackContentType, nil
	}
	return t, params
}

// cleanHeader makes text safe as a header value: valid UTF-8, no control
// characters (so no CR/LF folding tricks and no NUL), capped in size on a
// rune boundary.
func cleanHeader(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > maxHeaderBytes {
		s = s[:maxHeaderBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// addrSpecials may not appear in an outgoing addr-spec: they would need
// quoting and are the characters that separate or delimit addresses.
const addrSpecials = " \t\"(),:;<>[]\\"

// formatAddress cleans a and proves the result is one plain mailbox: it
// must re-parse with net/mail into the same address.
func formatAddress(a api.Address) (*mail.Address, error) {
	addr := &mail.Address{Name: cleanHeader(a.Name), Address: cleanHeader(a.Address)}
	at := strings.LastIndexByte(addr.Address, '@')
	if at <= 0 || at == len(addr.Address)-1 || strings.ContainsAny(addr.Address, addrSpecials) {
		return nil, ErrInvalidAddress
	}
	parsed, err := netmail.ParseAddress(addr.String())
	if err != nil || parsed.Address != addr.Address {
		return nil, ErrInvalidAddress
	}
	return addr, nil
}

func formatAddresses(list []api.Address) ([]*mail.Address, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := make([]*mail.Address, 0, len(list))
	for _, a := range list {
		fa, err := formatAddress(a)
		if err != nil {
			return nil, err
		}
		out = append(out, fa)
	}
	return out, nil
}

// cleanMsgID strips what could break a msg-id token (brackets, whitespace,
// control characters) and then requires the result to re-parse as a
// msg-id. Empty means "omit" (or, for Message-ID, "generate").
func cleanMsgID(id string) string {
	id = cleanHeader(id)
	id = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '<' || r == '>' {
			return -1
		}
		return r
	}, id)
	if id == "" {
		return ""
	}
	var h mail.Header
	h.SetMessageID(id)
	if parsed, err := h.MessageID(); err != nil || parsed != id {
		return ""
	}
	return id
}

func cleanMsgIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if c := cleanMsgID(id); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// normaliseNewlines turns CRLF and bare CR into LF; the transfer encoder
// then emits canonical CRLF on the wire.
func normaliseNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// errWriter remembers the first error the destination returned so that
// BuildMessage can hand it back unchanged, whatever the encoders wrapped
// around it.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}
