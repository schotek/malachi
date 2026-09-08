// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
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

// Mode is the draft.create mode of the kind.
func (k Kind) Mode() api.ComposeMode {
	switch k {
	case KindReply:
		return api.ComposeReply
	case KindReplyAll:
		return api.ComposeReplyAll
	case KindForward:
		return api.ComposeForward
	}
	return api.ComposeNew
}

// Source is what the caller knows about the message being replied to or
// forwarded. Every field is hostile input.
type Source struct {
	ID      api.MessageID
	From    []api.Address
	ReplyTo []api.Address // Reply-To header; replies go here instead of From
	To      []api.Address
	CC      []api.Address
	Subject string
	Date    time.Time
	Text    string // plain-text body
}

// Params opens a compose window with these fields prefilled.
type Params struct {
	Kind Kind
	// AccountID preselects the From identity; empty means the first account.
	AccountID   api.AccountID
	To, CC, BCC []api.Address
	Subject     string
	// BodyHTML is inserted into the editor document verbatim and must
	// therefore already be safe: only the backend (FromDraft), Prefill and
	// ParseMailto produce it.
	BodyHTML   string
	InReplyTo  api.MessageID
	Forwarding api.MessageID
	// Attachments are what the backend imported for the draft (the quoted
	// original's pictures, a forwarded message's files): not yet bound,
	// the first draft.save binds them.
	Attachments []api.DraftAttachment
	// Blocked is what the backend's sanitiser removed from the quoted
	// original; the window says so once.
	Blocked api.BlockedContent
}

// FromDraft turns a draft.create result into window parameters. A draft
// without HTML (the backend could not quote formatted) shows its text.
func FromDraft(kind Kind, d api.Draft, blocked api.BlockedContent) Params {
	p := Params{
		Kind:        kind,
		AccountID:   d.AccountID,
		To:          d.To,
		CC:          d.CC,
		BCC:         d.BCC,
		Subject:     d.Subject,
		BodyHTML:    d.HTMLBody,
		InReplyTo:   d.InReplyTo,
		Forwarding:  d.Forwarding,
		Attachments: d.Attachments,
		Blocked:     blocked,
	}
	if p.BodyHTML == "" {
		p.BodyHTML = escapeText(d.TextBody)
	}
	return p
}

// Prefill builds the reply / reply-all / forward parameters from src on
// the UI's own: the fallback when draft.create cannot be asked (no
// backend), quoting the plain text only. The normal path is draft.create,
// which quotes the original formatted, with its pictures.
func Prefill(kind Kind, src Source, self api.Address) Params {
	p := Params{Kind: kind}
	switch kind {
	case KindReply:
		p.To = dedupeAddresses(replyTargets(src), nil)
		p.Subject = ReplySubject(src.Subject)
		p.BodyHTML = quoteHTML(kind, src)
		p.InReplyTo = src.ID
	case KindReplyAll:
		p.To = dedupeAddresses(replyTargets(src), nil)
		exclude := append([]api.Address{self}, p.To...)
		p.CC = dedupeAddresses(append(append([]api.Address{}, src.To...), src.CC...), exclude)
		p.Subject = ReplySubject(src.Subject)
		p.BodyHTML = quoteHTML(kind, src)
		p.InReplyTo = src.ID
	case KindForward:
		p.Subject = ForwardSubject(src.Subject)
		p.BodyHTML = forwardHTML(kind, src)
		p.Forwarding = src.ID
	}
	return p
}

// Attribution is the line above a quote in the user's language: "On
// <date>, <sender> wrote:" for a reply, the header block of a forwarded
// message. Plain text, lines separated by "\n", nothing escaped: it is
// what draft.create is handed (the backend escapes it) and what the
// fallback quote escapes itself. Nothing for a new message.
func Attribution(kind Kind, src Source) string {
	var s string
	switch kind {
	case KindReply, KindReplyAll:
		names := displayNames(src.From)
		if src.Date.IsZero() {
			// TRANSLATORS: quote header without a date; %s is the sender.
			s = fmt.Sprintf(i18n.T("%s wrote:"), names)
		} else {
			// TRANSLATORS: quote header; %s are the date and the sender.
			s = fmt.Sprintf(i18n.T("On %s, %s wrote:"), widget.FormatDateTime(src.Date), names)
		}
	case KindForward:
		lines := []string{
			i18n.T("---------- Forwarded message ----------"),
			fmt.Sprintf(i18n.T("From: %s"), formatAll(src.From)),
		}
		if !src.Date.IsZero() {
			lines = append(lines, fmt.Sprintf(i18n.T("Date: %s"), widget.FormatDateTime(src.Date)))
		}
		lines = append(lines, fmt.Sprintf(i18n.T("Subject: %s"), src.Subject))
		if len(src.To) > 0 {
			lines = append(lines, fmt.Sprintf(i18n.T("To: %s"), formatAll(src.To)))
		}
		s = strings.Join(lines, "\n")
	default:
		return ""
	}
	// The backend's cap; a message to hundreds of people has a To: line
	// that would break it.
	s = strings.ToValidUTF8(s, "�")
	if len(s) > api.MaxDraftAttributionBytes {
		s = s[:api.MaxDraftAttributionBytes-len("…")]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}

// replyTargets is where a reply goes: Reply-To when the sender set one,
// otherwise From.
func replyTargets(src Source) []api.Address {
	if len(src.ReplyTo) > 0 {
		return src.ReplyTo
	}
	return src.From
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
// The prefixes are deliberately not translated: other clients only
// recognise the English forms when threading and de-duplicating them.
func ReplySubject(s string) string { return "Re: " + stripPrefixes(s) }

// ForwardSubject is "Fwd: " + subject without existing prefixes.
func ForwardSubject(s string) string { return "Fwd: " + stripPrefixes(s) }

// quoteHTML renders the original's text as a cite block under the
// attribution and an empty paragraph for the answer, the layout
// draft.create produces. Everything from src is escaped.
func quoteHTML(kind Kind, src Source) string {
	var b strings.Builder
	b.WriteString("<p><br></p><div>")
	b.WriteString(escapeText(Attribution(kind, src)))
	b.WriteString("</div><blockquote type=\"cite\">")
	b.WriteString(escapeText(src.Text))
	b.WriteString("</blockquote>")
	return b.String()
}

// forwardHTML renders the forwarded-message header block and the text.
func forwardHTML(kind Kind, src Source) string {
	var b strings.Builder
	b.WriteString("<p><br></p><div>")
	b.WriteString(escapeText(Attribution(kind, src)))
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
