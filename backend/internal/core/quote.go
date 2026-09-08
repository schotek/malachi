// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The reply and forward templates of draft.create (docs/api.md §4.5): who
// the answer goes to, the Re:/Fwd: subject, and the original quoted the
// way draft.save will store it, so that the first save changes nothing.
// The line above the quote is the client's: it is the one part that has a
// language, and the backend has none.

// maxQuotedInline bounds the pictures copied out of one original.
const maxQuotedInline = 32

// quoteHTMLCap is what the quoted original may take of the body cap; the
// rest is for the attribution and the wrapper around the quote.
const quoteHTMLCap = api.MaxDraftBodyBytes - 16<<10

// quotedPartLimit caps one part copied out of the original. A variable so
// a test can hit the cap without a 16 MiB fixture.
var quotedPartLimit int64 = api.MaxAttachmentDataBytes

// validateAttribution normalises the client's attribution: CRLF becomes
// LF, trailing breaks go, and anything that is not text (control
// characters other than tab and newline, invalid UTF-8) or over the caps
// is invalidArgument.
func validateAttribution(s string) (string, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if len(s) > api.MaxDraftAttributionBytes {
		return "", bad("attribution too long (limit %d bytes)", api.MaxDraftAttributionBytes)
	}
	if !utf8.ValidString(s) {
		return "", bad("attribution must be valid UTF-8")
	}
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return "", bad("attribution must not contain control characters")
		}
	}
	s = strings.TrimRight(s, "\n")
	if strings.Count(s, "\n")+1 > api.MaxDraftAttributionLines {
		return "", bad("attribution has too many lines (limit %d)", api.MaxDraftAttributionLines)
	}
	return s, nil
}

// selfAddresses is the set of addresses a reply must not go back to.
func selfAddresses(a store.Account) map[string]bool {
	self := map[string]bool{}
	for _, addr := range []string{a.Email, a.Config.Email} {
		if n := store.NormalizeAddress(addr); n != "" {
			self[n] = true
		}
	}
	return self
}

// replyRecipients addresses a reply: Reply-To, else From, without the
// account's own addresses; a message of one's own is answered to whom it
// went. With all, To and CC of the original follow as CC (again without
// self and without whoever is in To). Duplicates and unusable addresses
// are dropped so the first draft.save cannot fail on them.
func replyRecipients(m store.Message, self map[string]bool, all bool) (to, cc []api.Address) {
	seen := map[string]bool{}
	n := 0
	add := func(dst []api.Address, src []api.Address, skipSelf bool) []api.Address {
		for _, a := range src {
			key := store.NormalizeAddress(a.Address)
			if key == "" || seen[key] || (skipSelf && self[key]) || n >= api.MaxDraftRecipients {
				continue
			}
			a, ok := cleanAddress(a)
			if !ok {
				continue
			}
			seen[key] = true
			n++
			dst = append(dst, a)
		}
		return dst
	}
	from := m.ReplyTo
	if len(from) == 0 {
		from = m.From
	}
	to = add(nil, from, true)
	if len(to) == 0 {
		to = add(nil, m.To, true)
	}
	if len(to) == 0 {
		// A note to oneself: the answer goes back to oneself.
		to = add(nil, from, false)
	}
	if all {
		cc = add(nil, m.To, true)
		cc = add(cc, m.CC, true)
	}
	return to, cc
}

// cleanAddress is a stored address as a draft may carry it: a bare,
// parseable address; a display name that would break a header is dropped
// rather than the recipient.
func cleanAddress(a api.Address) (api.Address, bool) {
	a.Address = strings.TrimSpace(a.Address)
	p, err := mail.ParseAddress(a.Address)
	if err != nil || p.Address != a.Address {
		return a, false
	}
	if !utf8.ValidString(a.Name) || hasHeaderBreak(a.Name) {
		a.Name = ""
	}
	return a, true
}

// subjectPrefix matches one reply or forward marker, in the forms other
// clients write (Re, Fwd, Fw, the German Aw and Wg).
var subjectPrefix = regexp.MustCompile(`(?i)^\s*(re|fwd?|aw|wg)\s*:\s*`)

// stripSubjectPrefixes removes every leading marker, so that a reply to
// "RE: re: Fwd: x" is "Re: x".
func stripSubjectPrefixes(s string) string {
	for {
		loc := subjectPrefix.FindStringIndex(s)
		if loc == nil {
			return strings.TrimSpace(s)
		}
		s = s[loc[1]:]
	}
}

// replySubject is "Re: " and the subject without its markers. The marker
// is never translated: other clients recognise only the English form.
func replySubject(s string) string { return capSubject("Re: " + stripSubjectPrefixes(cleanSubject(s))) }

// forwardSubject is "Fwd: " and the subject without its markers.
func forwardSubject(s string) string {
	return capSubject("Fwd: " + stripSubjectPrefixes(cleanSubject(s)))
}

// cleanSubject makes a stored subject fit for a draft header.
func cleanSubject(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return ' '
		}
		return r
	}, s)
}

// capSubject cuts s to api.MaxDraftSubjectBytes at a character boundary.
func capSubject(s string) string {
	return capBytes(s, api.MaxDraftSubjectBytes)
}

func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// escapeLines escapes plain text for HTML and turns line breaks into <br>.
func escapeLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

// newDraft is the template of a new message: empty, or what a mailto: URI
// asks for (to, cc, bcc, subject, body; nothing else is interpreted). The
// URI comes from a web page or another application and is hostile:
// unusable addresses are dropped, the rest is capped like a draft, and
// the body goes through the sanitiser so the first save is the identity.
func newDraft(b *Backend, a store.Account, mailto string) (api.Draft, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	d := api.Draft{AccountID: api.AccountID(a.ID)}
	if mailto == "" {
		return d, nil
	}
	u, err := url.Parse(mailto)
	if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
		return d, bad("mailto must be a mailto: URI")
	}
	if to, err := url.PathUnescape(u.Opaque); err == nil {
		d.To = mailtoAddresses(to)
	}
	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		v := values[0]
		switch strings.ToLower(key) {
		case "to":
			d.To = append(d.To, mailtoAddresses(v)...)
		case "cc":
			d.CC = mailtoAddresses(v)
		case "bcc":
			d.BCC = mailtoAddresses(v)
		case "subject":
			d.Subject = capSubject(strings.TrimSpace(cleanSubject(v)))
		case "body":
			v = strings.ReplaceAll(strings.ToValidUTF8(v, "�"), "\r\n", "\n")
			v = strings.Map(func(r rune) rune {
				if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
					return -1
				}
				return r
			}, v)
			if len(v) > api.MaxDraftBodyBytes {
				return d, bad("mailto body too long (limit %d bytes)", api.MaxDraftBodyBytes)
			}
			d.TextBody = v
			if v == "" {
				continue
			}
			out, err := b.Sanitize(sanitize.Input{HTML: escapeLines(v), Mode: sanitize.ModeCompose,
				Policy: api.RemoteBlock, MaxOutputSize: api.MaxDraftBodyBytes})
			if err != nil {
				b.log.Warn("mailto body refused, keeping the text", "err", err)
				continue
			}
			d.HTMLBody, d.TextBody = out.HTML, out.Text
		}
	}
	if len(d.To)+len(d.CC)+len(d.BCC) > api.MaxDraftRecipients {
		return d, bad("too many recipients (limit %d)", api.MaxDraftRecipients)
	}
	return d, nil
}

// mailtoAddresses parses a comma-separated address list of a mailto: URI,
// keeping what a draft can carry.
func mailtoAddresses(s string) []api.Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	list, err := mail.ParseAddressList(s)
	if err != nil {
		return nil
	}
	out := make([]api.Address, 0, len(list))
	for _, a := range list {
		if addr, ok := cleanAddress(api.Address{Name: a.Name, Address: a.Address}); ok {
			out = append(out, addr)
		}
	}
	return out
}

// quoter builds the quoted body of one original.
type quoter struct {
	b           *Backend
	account     string
	forward     bool
	attribution string // validated, LF-separated, possibly empty
}

// quoteResult is what the quoter produced: the form it managed, the two
// bodies, the parts it copied into the store and the ones it did not.
type quoteResult struct {
	form    api.QuoteForm
	html    string
	text    string
	blocked api.BlockedContent
	atts    []store.Attachment
	skipped []api.Attachment
}

// quote quotes m in the best form the stored data allow: its sanitised
// HTML with the pictures copied, else its text in a cite block, else
// plain text. Nothing here fails the call but a store that will not take
// a copy; a body that is not downloaded quotes nothing.
func (q *quoter) quote(ctx context.Context, m store.Message) (quoteResult, error) {
	text, _, state, err := q.b.store.GetMessageText(ctx, q.account, m.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return quoteResult{}, api.NewError(api.CodeMessageNotFound, "unknown message %q", m.ID)
		}
		return quoteResult{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	if state != store.BodyFetched {
		return quoteResult{form: api.QuoteNone}, nil
	}
	raw, parsed := q.openRaw(ctx, m.ID)
	if raw != nil {
		defer raw.Close()
	}

	var res quoteResult
	if parsed != nil && parsed.HasHTML {
		if first, referenced, ok := q.sanitizeOriginal(parsed); ok {
			imp, err := q.importParts(ctx, raw, parsed, referenced)
			if err != nil {
				return quoteResult{}, err
			}
			if out, ok := q.sanitizeQuote(q.assemble(first.HTML), imp); ok {
				// What draft.save does with an inline attachment the body
				// does not reference: the copy goes.
				atts := q.keepReferenced(ctx, imp, out.CIDs)
				return quoteResult{form: api.QuoteHTML, html: out.HTML, text: out.Text,
					blocked: first.Blocked, atts: atts, skipped: imp.skipped}, nil
			}
			q.remove(ctx, imp.inline)
			res.atts, res.skipped = imp.regular, imp.skipped
			parsed = nil // the forwarded attachments are already copied
		}
	}
	if q.forward && parsed != nil {
		imp, err := q.importParts(ctx, raw, parsed, nil)
		if err != nil {
			return quoteResult{}, err
		}
		res.atts, res.skipped = imp.regular, imp.skipped
	}
	q.quoteText(text, &res)
	return res, nil
}

// openRaw opens and parses the stored original; nil, nil when it cannot
// be had, which only costs the pictures and the formatting.
func (q *quoter) openRaw(ctx context.Context, id string) (*os.File, *mime.Parsed) {
	f, err := q.b.store.OpenMessageRaw(ctx, q.account, id)
	if err != nil {
		q.b.log.Warn("quote: raw message unavailable", "id", id, "err", err)
		return nil, nil
	}
	parsed, err := mime.Parse(f, mime.DefaultLimits())
	if err != nil {
		f.Close()
		q.b.log.Warn("quote: raw message unparsable", "id", id, "err", err)
		return nil, nil
	}
	return f, parsed
}

// sanitizeOriginal is the first pass: the original's HTML in compose
// mode, its cid: references bound to its parts. It yields the clean
// fragment, the Content-IDs the fragment still references (with the part
// each names; the first part of a duplicated id wins, as in the view) and
// false when the sanitiser refuses the body.
func (q *quoter) sanitizeOriginal(parsed *mime.Parsed) (sanitize.Output, map[string]string, bool) {
	known := make(map[string]string)
	for _, att := range parsed.Attachments {
		if att.ContentID == "" {
			continue
		}
		if _, dup := known[att.ContentID]; !dup {
			known[att.ContentID] = att.PartID
		}
	}
	out, err := q.b.Sanitize(sanitize.Input{
		HTML:          parsed.RawHTML,
		Mode:          sanitize.ModeCompose,
		Policy:        api.RemoteBlock,
		KnownCIDs:     known,
		MaxOutputSize: quoteHTMLCap,
	})
	if err != nil {
		q.b.log.Warn("quote: html refused, quoting the text", "err", err)
		return sanitize.Output{}, nil, false
	}
	referenced := make(map[string]string, len(out.CIDs))
	for _, cid := range out.CIDs {
		referenced[cid] = known[cid]
	}
	return out, referenced, true
}

// imported is what importParts copied into the store.
type imported struct {
	inline  []store.Attachment // pictures the quote references, under new Content-IDs
	regular []store.Attachment // the rest of a forwarded message
	rewrite map[string]string  // original Content-ID → new one
	skipped []api.Attachment
}

// known maps the new Content-IDs to their attachment ids, the way
// draft.save binds them.
func (imp *imported) known() map[string]string {
	m := make(map[string]string, len(imp.inline))
	for _, a := range imp.inline {
		m[a.ContentID] = a.ID
	}
	return m
}

// all lists every copy, in the order of the original's parts.
func (imp *imported) all() []store.Attachment {
	out := make([]store.Attachment, 0, len(imp.inline)+len(imp.regular))
	out = append(out, imp.inline...)
	return append(out, imp.regular...)
}

// importParts copies the original's parts into the store: the pictures in
// referenced (Content-ID → part) as inline attachments under fresh ids,
// and, forwarding, every other part as a regular attachment. A part that
// is over a cap, unreadable, or (for a picture) not a safe image is
// skipped and reported; a store that will not take a copy undoes the ones
// made and is storageError.
func (q *quoter) importParts(ctx context.Context, raw *os.File, parsed *mime.Parsed, referenced map[string]string) (*imported, error) {
	imp := &imported{rewrite: map[string]string{}}
	if raw == nil {
		return imp, nil
	}
	var total int64
	for _, att := range parsed.Attachments {
		inline := att.ContentID != "" && referenced[att.ContentID] == att.PartID
		if !inline && !q.forward {
			continue
		}
		if att.Size > quotedPartLimit || total+att.Size > api.MaxDraftAttachmentBytes ||
			len(imp.inline)+len(imp.regular) >= api.MaxDraftAttachments ||
			(inline && len(imp.inline) >= maxQuotedInline) {
			imp.skipped = append(imp.skipped, att)
			continue
		}
		part, err := extractQuotedPart(raw, att.PartID)
		if err != nil || len(part.Body) == 0 {
			q.b.log.Warn("quote: part not copied", "part", att.PartID, "err", err)
			imp.skipped = append(imp.skipped, att)
			continue
		}
		if total+int64(len(part.Body)) > api.MaxDraftAttachmentBytes {
			imp.skipped = append(imp.skipped, att)
			continue
		}
		head := part.Body[:min(len(part.Body), 512)]
		ct := detectContentType(head, part.Filename)
		if inline && (!strings.HasPrefix(ct, "image/") || ct == "image/svg+xml") {
			// Referenced, but not a picture the editor may show: a
			// forward still carries it, a reply has no use for it.
			if !q.forward {
				imp.skipped = append(imp.skipped, att)
				continue
			}
			inline = false
		}
		if !inline {
			ct = forwardContentType(ct, att.ContentType)
		}
		a := store.Attachment{AccountID: q.account, Filename: part.Filename, ContentType: ct, Inline: inline}
		if inline {
			a.ContentID = newContentID()
		}
		switch err := q.b.store.ImportAttachment(ctx, &a, bytes.NewReader(part.Body), quotedPartLimit); {
		case errors.Is(err, store.ErrTooBig):
			imp.skipped = append(imp.skipped, att)
			continue
		case err != nil:
			q.remove(ctx, imp.all())
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		total += a.Size
		if inline {
			imp.inline = append(imp.inline, a)
			imp.rewrite[att.ContentID] = a.ContentID
		} else {
			imp.regular = append(imp.regular, a)
		}
	}
	return imp, nil
}

// extractQuotedPart reads one part out of the original, which the earlier
// parse left at its end.
func extractQuotedPart(raw *os.File, partID string) (*mime.Part, error) {
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return mime.ExtractPart(raw, partID, mime.DefaultLimits(), quotedPartLimit)
}

// forwardContentType is the type a forwarded part travels under: what its
// content says, or, when sniffing tells nothing, what its sender claimed.
func forwardContentType(sniffed, claimed string) string {
	if sniffed != "application/octet-stream" && sniffed != "text/plain" {
		return sniffed
	}
	if claimed != "" && !strings.HasPrefix(claimed, "multipart/") {
		return claimed
	}
	return sniffed
}

// assemble puts the attribution and the quote under an empty paragraph
// for the answer: a reply cites, a forward relays.
func (q *quoter) assemble(fragment string) string {
	var b strings.Builder
	b.WriteString("<p><br></p>")
	if q.attribution != "" {
		b.WriteString("<div>" + escapeLines(q.attribution) + "</div>")
	}
	if q.forward {
		b.WriteString(fragment)
		return b.String()
	}
	b.WriteString(`<blockquote type="cite">`)
	b.WriteString(fragment)
	b.WriteString("</blockquote>")
	return b.String()
}

// sanitizeQuote is the pass that decides: the assembled body, the copied
// pictures known under their new ids, the original ids rewritten to them.
// Its output is what draft.save will produce from the same input, so the
// first save is the identity.
func (q *quoter) sanitizeQuote(body string, imp *imported) (sanitize.Output, bool) {
	out, err := q.b.Sanitize(sanitize.Input{
		HTML:          body,
		Mode:          sanitize.ModeCompose,
		Policy:        api.RemoteBlock,
		KnownCIDs:     imp.known(),
		RewriteCIDs:   imp.rewrite,
		MaxOutputSize: api.MaxDraftBodyBytes,
	})
	if err != nil {
		q.b.log.Warn("quote: assembled body refused, quoting the text", "err", err)
		return sanitize.Output{}, false
	}
	return out, true
}

// keepReferenced drops the copied pictures the final body does not
// reference (keepReferencedInline of draft.save) and returns the rest,
// regular copies included.
func (q *quoter) keepReferenced(ctx context.Context, imp *imported, cids []string) []store.Attachment {
	referenced := make(map[string]bool, len(cids))
	for _, c := range cids {
		referenced[c] = true
	}
	kept := make([]store.Attachment, 0, len(imp.inline)+len(imp.regular))
	var gone []store.Attachment
	for _, a := range imp.inline {
		if referenced[a.ContentID] {
			kept = append(kept, a)
		} else {
			gone = append(gone, a)
		}
	}
	q.remove(ctx, gone)
	return append(kept, imp.regular...)
}

// remove deletes copies that will not be used.
func (q *quoter) remove(ctx context.Context, atts []store.Attachment) {
	for _, a := range atts {
		if err := q.b.store.RemoveAttachment(ctx, q.account, a.ID); err != nil {
			q.b.log.Warn("quote: cannot remove unused copy", "id", a.ID, "err", err)
		}
	}
}

// quoteText fills res with the original's text quoted: escaped into the
// cite block and sanitised like everything else, or, should even that be
// refused, as plain text with "> " on every line.
func (q *quoter) quoteText(text string, res *quoteResult) {
	res.form = api.QuoteText
	if out, ok := q.sanitizeQuote(q.assemble(escapeLines(text)), &imported{}); ok {
		res.html, res.text = out.HTML, out.Text
		return
	}
	var b strings.Builder
	b.WriteString("\n\n")
	if q.attribution != "" {
		b.WriteString(q.attribution)
		b.WriteString("\n")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if q.forward {
		b.WriteString(text)
	} else {
		for i, line := range strings.Split(text, "\n") {
			if i > 0 {
				b.WriteString("\n")
			}
			if line == "" {
				b.WriteString(">")
			} else {
				b.WriteString("> " + line)
			}
		}
	}
	res.html, res.text = "", capBytes(b.String(), api.MaxDraftBodyBytes)
}
