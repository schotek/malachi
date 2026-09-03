package compose

import (
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Kind is what the compose window was opened for.
type Kind int

const (
	KindNew Kind = iota
	KindReply
	KindReplyAll
	KindForward
)

// Source is what the caller knows about the message being replied to or
// forwarded. Every field is hostile input.
type Source struct {
	ID      api.MessageID
	From    []api.Address
	To      []api.Address
	CC      []api.Address
	Subject string
	Date    time.Time
	Text    string // plain-text body
}

// Params opens a compose window with these fields prefilled.
type Params struct {
	Kind        Kind
	To, CC, BCC []api.Address
	Subject     string
	// BodyHTML is inserted into the editor document verbatim and must
	// therefore already be safe: only Prefill and ParseMailto produce it.
	BodyHTML   string
	InReplyTo  api.MessageID
	Forwarding api.MessageID
}

// Prefill builds the reply / reply-all / forward parameters from src.
//
// TODO(phase-1): this is the UI fallback while draft.create is a stub;
// once the backend has the message store the window calls draft.create
// first and only falls back here for placeholder data.
func Prefill(kind Kind, src Source, self api.Address, now time.Time) Params {
	p := Params{Kind: kind}
	switch kind {
	case KindReply:
		p.To = dedupeAddresses(src.From, nil)
		p.Subject = ReplySubject(src.Subject)
		p.BodyHTML = quoteHTML(src)
		p.InReplyTo = src.ID
	case KindReplyAll:
		p.To = dedupeAddresses(src.From, nil)
		exclude := append([]api.Address{self}, p.To...)
		p.CC = dedupeAddresses(append(append([]api.Address{}, src.To...), src.CC...), exclude)
		p.Subject = ReplySubject(src.Subject)
		p.BodyHTML = quoteHTML(src)
		p.InReplyTo = src.ID
	case KindForward:
		p.Subject = ForwardSubject(src.Subject)
		p.BodyHTML = forwardHTML(src)
		p.Forwarding = src.ID
	}
	_ = now
	return p
}

var subjectPrefix = regexp.MustCompile(`(?i)^\s*(re|fwd?|aw|wg)\s*:\s*`)

// stripPrefixes removes any number of Re:/Fwd:-style prefixes.
func stripPrefixes(s string) string {
	for {
		loc := subjectPrefix.FindStringIndex(s)
		if loc == nil {
			return strings.TrimSpace(s)
		}
		s = s[loc[1]:]
	}
}

// ReplySubject is "Re: " + subject without existing prefixes (idempotent).
func ReplySubject(s string) string { return "Re: " + stripPrefixes(s) }

// ForwardSubject is "Fwd: " + subject without existing prefixes.
func ForwardSubject(s string) string { return "Fwd: " + stripPrefixes(s) }

// quoteHTML renders the original as a cite block under an empty paragraph
// for the answer. Everything from src is escaped.
func quoteHTML(src Source) string {
	var b strings.Builder
	b.WriteString("<p><br></p><blockquote type=\"cite\">")
	b.WriteString("On ")
	if !src.Date.IsZero() {
		b.WriteString(html.EscapeString(src.Date.Local().Format("Mon, 2 Jan 2006 at 15:04")))
		b.WriteString(", ")
	}
	b.WriteString(html.EscapeString(displayNames(src.From)))
	b.WriteString(" wrote:<br>")
	b.WriteString(escapeText(src.Text))
	b.WriteString("</blockquote>")
	return b.String()
}

// forwardHTML renders the forwarded-message header block and body.
func forwardHTML(src Source) string {
	var b strings.Builder
	b.WriteString("<p><br></p><div>---------- Forwarded message ----------<br>")
	b.WriteString("From: " + html.EscapeString(formatAll(src.From)) + "<br>")
	if !src.Date.IsZero() {
		b.WriteString("Date: " + html.EscapeString(src.Date.Local().Format("Mon, 2 Jan 2006 at 15:04")) + "<br>")
	}
	b.WriteString("Subject: " + html.EscapeString(src.Subject) + "<br>")
	if len(src.To) > 0 {
		b.WriteString("To: " + html.EscapeString(formatAll(src.To)) + "<br>")
	}
	b.WriteString("</div><br>")
	b.WriteString(escapeText(src.Text))
	return b.String()
}

// escapeText escapes plain text for HTML and turns newlines into <br>.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

func displayNames(addrs []api.Address) string {
	names := make([]string, 0, len(addrs))
	for _, a := range addrs {
		names = append(names, widget.DisplayName(a))
	}
	return strings.Join(names, ", ")
}

func formatAll(addrs []api.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, widget.FormatAddress(a))
	}
	return strings.Join(parts, ", ")
}

// dedupeAddresses keeps the first occurrence of each address
// (case-insensitively) that is not in exclude.
func dedupeAddresses(in, exclude []api.Address) []api.Address {
	seen := make(map[string]bool, len(exclude))
	for _, a := range exclude {
		seen[strings.ToLower(strings.TrimSpace(a.Address))] = true
	}
	var out []api.Address
	for _, a := range in {
		key := strings.ToLower(strings.TrimSpace(a.Address))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}
