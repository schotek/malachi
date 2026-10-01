// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	stdmime "mime"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message"
	msgmail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/schotek/malachi/backend/pkg/api"
)

// maxAddresses bounds one address list; a 256 KiB To: field could hold
// thousands of addresses and nobody needs more than this in a summary.
const maxAddresses = 500

// curatedHeaders are the only extra header fields exposed through
// Parsed.Headers, by canonical name.
var curatedHeaders = []string{
	"List-Unsubscribe",
	"List-Unsubscribe-Post",
	"List-Id",
	"List-Post",
	"Auto-Submitted",
	"Precedence",
	"X-Priority",
	"Importance",
	"Sender",
	"Return-Path",
	"X-Mailer",
	"User-Agent",
	// The bulk-sending services' fields of internal/bulk
	// SenderFingerprints (kept in step by a test there).
	"Feedback-ID",
	"X-CSA-Complaints",
	"X-MSFBL",
	"X-SG-EID",
	"X-Mailgun-Sid",
	"X-SES-Outgoing",
	"X-MC-User",
	"X-Mandrill-User",
	"X-PM-Message-Id",
	"X-SFMC-Stack",
}

// lenientDateLayouts are tried after net/mail.ParseDate fails, with any
// trailing comment removed.
var lenientDateLayouts = []string{
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 06 15:04:05 -0700",
	"2 Jan 06 15:04:05 -0700",
	"Mon 2 Jan 2006 15:04:05 -0700",
	"Mon, Jan 2 2006 15:04:05 -0700",
	"Mon Jan 2 15:04:05 2006",
	"Mon Jan 2 15:04:05 -0700 2006",
	"Mon, 2 Jan 2006 15:04:05",
	"2 Jan 2006 15:04:05",
	"2 Jan 2006",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02",
}

// envelope fills the header-derived fields of the result from the
// top-level header.
func (p *parser) envelope(h message.Header) {
	mh := msgmail.Header{Header: h}
	out := p.out
	max := p.limits.MaxFieldBytes

	subject, err := mh.Subject()
	if err != nil {
		p.problem("subject: " + err.Error())
	}
	out.Subject = cleanField(subject, max)

	out.From = p.addresses(&mh, "From")
	out.To = p.addresses(&mh, "To")
	out.CC = p.addresses(&mh, "Cc")
	out.BCC = p.addresses(&mh, "Bcc")
	out.ReplyTo = p.addresses(&mh, "Reply-To")

	out.Date = p.date(mh.Get("Date"))

	id, err := mh.MessageID()
	if err != nil {
		p.problem("message-id: " + err.Error())
		id = firstMsgID(mh.Get("Message-Id"))
	}
	out.MessageID = cleanID(id, max)

	out.InReplyTo = ""
	if l := p.msgIDs(&mh, "In-Reply-To", 1, false); len(l) > 0 {
		out.InReplyTo = l[0]
	}
	out.References = p.msgIDs(&mh, "References", p.limits.MaxReferences, true)

	p.curated(h, out.Headers)
}

// maxListUnsubscribeBytes is the cap of a List-Unsubscribe value: up to
// eight URIs of 2048 bytes (internal/bulk), where the other curated
// fields keep MaxFieldBytes. A URI cut by a lower cap would be a wrong one.
const maxListUnsubscribeBytes = 17 << 10

// curated copies the curatedHeaders present in h into dst, cleaned and
// capped.
func (p *parser) curated(h message.Header, dst map[string]string) {
	for _, name := range curatedHeaders {
		if !h.Has(name) {
			continue
		}
		v, err := h.Text(name)
		if err != nil {
			p.problem(strings.ToLower(name) + ": " + err.Error())
		}
		max := p.limits.MaxFieldBytes
		if name == "List-Unsubscribe" && max > 0 && max < maxListUnsubscribeBytes {
			max = maxListUnsubscribeBytes
		}
		if v = cleanField(v, max); v != "" {
			dst[name] = v
		}
	}
}

// addresses parses one address header. Control characters are removed
// before parsing so an injected CR/LF cannot split the field. When the
// strict parser rejects the list, encoded words are decoded, the value is
// cleaned again (which removes CR/LF smuggled through the encoding) and
// parsing is retried; if that fails too, the cleaned value becomes a single
// display name, with an angle-addr salvaged into Address when one is
// present, so nothing is silently lost.
func (p *parser) addresses(mh *msgmail.Header, key string) []api.Address {
	raw := cleanField(mh.Get(key), 0)
	if raw == "" {
		return nil
	}
	max := p.limits.MaxFieldBytes
	list, err := msgmail.ParseAddressList(raw)
	if err != nil {
		p.problem(strings.ToLower(key) + ": " + err.Error())
		dec := stdmime.WordDecoder{CharsetReader: message.CharsetReader}
		decoded, derr := dec.DecodeHeader(raw)
		if derr != nil {
			decoded = raw
		}
		decoded = cleanField(decoded, 0)
		list, err = msgmail.ParseAddressList(decoded)
		if err != nil {
			return salvageAddress(decoded, max)
		}
	}
	out := make([]api.Address, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		addr := api.Address{Name: cleanField(a.Name, max), Address: cleanField(a.Address, max)}
		if addr == (api.Address{}) {
			continue
		}
		if len(out) == maxAddresses {
			p.problem(fmt.Sprintf("%s: more than %d addresses", strings.ToLower(key), maxAddresses))
			break
		}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// salvageAddress turns an unparsable address field into one api.Address:
// the last "<…>" that is a valid addr-spec becomes Address, the rest the
// display name.
func salvageAddress(raw string, max int) []api.Address {
	a := api.Address{}
	if i := strings.LastIndexByte(raw, '<'); i >= 0 {
		if j := strings.IndexByte(raw[i:], '>'); j > 1 {
			cand := raw[i+1 : i+j]
			if parsed, err := netmail.ParseAddress("<" + cand + ">"); err == nil {
				a.Address = cleanField(parsed.Address, max)
				raw = raw[:i] + raw[i+j+1:]
			}
		}
	}
	a.Name = cleanField(raw, max)
	if a == (api.Address{}) {
		return nil
	}
	return []api.Address{a}
}

// date parses the Date field, strictly first and leniently second.
func (p *parser) date(raw string) time.Time {
	raw = cleanField(raw, 256)
	if raw == "" {
		return time.Time{}
	}
	if t, err := netmail.ParseDate(raw); err == nil {
		return t
	}
	if i := strings.IndexByte(raw, '('); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	for _, layout := range lenientDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	p.problem("date: unparsable")
	return time.Time{}
}

// msgIDs parses a message identifier list, falling back to msgIDFields
// when the strict parser rejects the field, and returns at most max
// distinct cleaned identifiers without angle brackets: the first max, or
// with tail the last max (References lists ancestors oldest first, and the
// nearest ones are what threading links on). A repeated identifier counts
// once, so repetition cannot fill the cap.
func (p *parser) msgIDs(mh *msgmail.Header, key string, max int, tail bool) []string {
	raw := mh.Get(key)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	list, err := mh.MsgIDList(key)
	if err != nil {
		p.problem(strings.ToLower(key) + ": " + err.Error())
	}
	if len(list) == 0 {
		list = msgIDFields(raw)
	}
	out := make([]string, 0, min(len(list), max))
	seen := make(map[string]bool, min(len(list), max))
	for _, id := range list {
		if id = cleanID(id, p.limits.MaxFieldBytes); id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > max {
		p.problem(fmt.Sprintf("%s: more than %d identifiers", strings.ToLower(key), max))
		if tail {
			out = out[len(out)-max:]
		} else {
			out = out[:max]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseReferences reads a header block (what IMAP's
// BODY[HEADER.FIELDS (REFERENCES)] returns, with or without the blank
// line) and returns the cleaned identifiers of its References field, the
// last limits.MaxReferences of them; nil when there are none or the block
// cannot be read. At most limits.MaxHeaderBytes are consumed from r.
func ParseReferences(r io.Reader, limits Limits) []string {
	limits = limits.withDefaults()
	h, err := textproto.ReadHeader(bufio.NewReader(io.LimitReader(r, limits.MaxHeaderBytes)))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	mh := msgmail.Header{Header: message.Header{Header: h}}
	p := &parser{limits: limits, out: &Parsed{}}
	return p.msgIDs(&mh, "References", limits.MaxReferences, true)
}

// ParseHeaderFields reads a header block (a HEADER.FIELDS literal or the
// top of a raw message, up to the blank line) and returns the cleaned
// identifiers of its References field and the curated headers present
// (curatedHeaders, as Parse reports them in Parsed.Headers). At most
// limits.MaxHeaderBytes are consumed from r; an unreadable block yields
// nothing.
func ParseHeaderFields(r io.Reader, limits Limits) (references []string, headers map[string]string) {
	limits = limits.withDefaults()
	h, err := textproto.ReadHeader(bufio.NewReader(io.LimitReader(r, limits.MaxHeaderBytes)))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, nil
	}
	mh := msgmail.Header{Header: message.Header{Header: h}}
	p := &parser{limits: limits, out: &Parsed{}}
	headers = map[string]string{}
	p.curated(message.Header{Header: h}, headers)
	return p.msgIDs(&mh, "References", limits.MaxReferences, true), headers
}

// CountHeaderFields reads a header block like ParseHeaderFields and
// returns, per requested name (canonical form), how many fields of that
// name the block holds. A name that is repeated is one of which the
// curated value (the first) may not be the one a signature covers.
func CountHeaderFields(r io.Reader, limits Limits, names ...string) map[string]int {
	limits = limits.withDefaults()
	h, err := textproto.ReadHeader(bufio.NewReader(io.LimitReader(r, limits.MaxHeaderBytes)))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	out := make(map[string]int, len(names))
	for _, n := range names {
		out[n] = len(h.Values(n))
	}
	return out
}

// HeaderValues reads a header block like ParseHeaderFields and returns,
// per requested name (canonical form), the raw values of every field of
// that name in document order, topmost first, each at most
// limits.MaxHeaderBytes long. Nothing is cleaned or decoded: the caller
// parses them (internal/bulk, Authentication-Results and DKIM-Signature).
func HeaderValues(r io.Reader, limits Limits, names ...string) map[string][]string {
	limits = limits.withDefaults()
	h, err := textproto.ReadHeader(bufio.NewReader(io.LimitReader(r, limits.MaxHeaderBytes)))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	out := make(map[string][]string, len(names))
	for _, n := range names {
		if v := h.Values(n); len(v) > 0 {
			out[n] = v
		}
	}
	return out
}

// firstMsgID extracts the first identifier from a raw Message-ID value
// that the strict parser rejected.
func firstMsgID(raw string) string {
	f := msgIDFields(raw)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// msgIDFields splits an identifier field the strict parser rejected into
// candidate identifiers. Whitespace and angle brackets both separate them:
// a bracket is never part of an identifier, so a stray one ("0>0") ends
// the identifier instead of surviving inside it, and identifiers written
// without a space between them ("<a@x><b@x>") stay apart.
func msgIDFields(raw string) []string {
	return strings.FieldsFunc(cleanField(raw, 0), func(r rune) bool {
		return r == ' ' || r == '<' || r == '>'
	})
}
