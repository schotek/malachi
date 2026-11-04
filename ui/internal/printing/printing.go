// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package printing is the document a client prints: a message, a
// conversation, several messages, a calendar event or a calendar day
// (docs/plans/toolbar-customization.md §8, decisions P1–P8). It builds the
// HTML fragment that the client's locked message view loads in place of
// a message body: the header rows and the event's and day's lines, which
// the application writes and escapes here (Escape), around the message
// bodies, which are the sanitiser's output and are inserted verbatim. A
// body without HTML (or withheld) is its plain text, escaped, in a <pre>.
// The fragment goes through the same document and policy as a message
// on screen (no script, no network, `malachi-cid:` and `data:` pictures
// only); nothing here fetches anything. Dates, addresses and sizes come
// formatted by the client. Pure logic, no widgets; ported 1:1 to macOS
// (MalachiCore/Printing/Printing.swift), GTK and Windows later.
package printing

import "strings"

// Translator translates a msgid of the malachi domain (GTK passes an
// adapter over i18n.T, i18n.N and i18n.C; the tests one that returns the
// msgid).
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// MaxMessages is the most messages one print takes (a selection or a
// conversation); the client offers Print for no more.
const MaxMessages = 50

// MenuTitle is Print in the File menu (with a mnemonic).
func MenuTitle(tr Translator) string {
	// TRANSLATORS: File menu item that prints the selected message, conversation, event or day.
	return tr.T("_Print…")
}

// ItemLabel is Print as a toolbar item and its tooltip.
func ItemLabel(tr Translator) string {
	// TRANSLATORS: toolbar button that prints the selected message, conversation, event or day.
	return tr.T("Print")
}

// Header is a message's header as the client formatted it; empty fields
// are left out of the print.
type Header struct {
	From, To, Cc, Bcc, Date, Subject string
	// Attachments are the attachments' names with their sizes, as the
	// reader's chips show them (no inline pictures).
	Attachments []string
}

// Row is a labelled line of a header; Strong is the subject's.
type Row struct {
	Label, Value string
	Strong       bool
}

// HeaderRows are h's rows in print order: Subject, From, To, Cc, Bcc,
// Date, Attachments (joined); the empty ones left out (P6: Bcc is
// given only for the user's own messages).
func HeaderRows(tr Translator, h Header) []Row {
	var rows []Row
	add := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			rows = append(rows, Row{Label: label, Value: value})
		}
	}
	add(tr.T("Subject"), h.Subject)
	if len(rows) == 1 {
		rows[0].Strong = true
	}
	add(tr.T("From"), h.From)
	add(tr.T("To"), h.To)
	add(tr.T("Cc"), h.Cc)
	add(tr.T("Bcc"), h.Bcc)
	add(tr.T("Date"), h.Date)
	add(tr.T("Attachments"), strings.Join(h.Attachments, ", "))
	return rows
}

// Escape makes s text of HTML: & < > " ' as entities. Every string the
// application puts into the fragment goes through it.
func Escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Body is a message's body for the print: the sanitiser's HTML, or
// (empty HTML) the plain text.
type Body struct {
	// HTML is the sanitised fragment of message.body, inserted verbatim.
	HTML string
	// Text is the plain text, escaped here.
	Text string
}

// CSS is the print's own sheet, inside the fragment (the viewer's CSP
// allows inline style). The classes are ours; a message's own styles may
// still reach them in a conversation, which only changes the looks.
const CSS = `.malachi-print-header{border-collapse:collapse;margin:0 0 12px 0;font:13px -apple-system,system-ui,sans-serif;color:#000}` +
	`.malachi-print-header th{text-align:right;vertical-align:top;padding:1px 8px 1px 0;font-weight:600;white-space:nowrap;color:#444}` +
	`.malachi-print-header td{padding:1px 0;word-break:break-word}` +
	`.malachi-print-subject td{font-weight:700;font-size:15px}` +
	`.malachi-print-body{margin:0 0 12px 0}` +
	`.malachi-print-text{white-space:pre-wrap;word-wrap:break-word;font:12px ui-monospace,Menlo,monospace;margin:0}` +
	`.malachi-print-sep{border:0;border-top:1px solid #999;margin:18px 0}` +
	`.malachi-print-page{break-before:page;page-break-before:always}` +
	`.malachi-print-title{font:700 17px -apple-system,system-ui,sans-serif;margin:0 0 6px 0}` +
	`.malachi-print-line{font:13px -apple-system,system-ui,sans-serif;margin:2px 0}` +
	`.malachi-print-notes{white-space:pre-wrap;font:13px -apple-system,system-ui,sans-serif;margin:10px 0 0 0}` +
	`.malachi-print-day{border-collapse:collapse;width:100%;font:13px -apple-system,system-ui,sans-serif}` +
	`.malachi-print-day td{padding:4px 8px 4px 0;vertical-align:top;border-bottom:1px solid #ddd}` +
	`.malachi-print-day td.time{white-space:nowrap;color:#444;width:1%}`

// Message is one message's section: its header table and its body.
func Message(rows []Row, body Body) string {
	var b strings.Builder
	b.WriteString(`<table class="malachi-print-header">`)
	for _, r := range rows {
		cls := ""
		if r.Strong {
			cls = ` class="malachi-print-subject"`
		}
		b.WriteString(`<tr` + cls + `><th>` + Escape(r.Label) + `</th><td>` + Escape(r.Value) + `</td></tr>`)
	}
	b.WriteString(`</table><div class="malachi-print-body">`)
	if body.HTML != "" {
		b.WriteString(body.HTML)
	} else {
		b.WriteString(`<pre class="malachi-print-text">` + Escape(body.Text) + `</pre>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// Layout is how sections follow each other.
type Layout int

const (
	// Single: one section.
	Single Layout = iota
	// Conversation: one after the other, a rule between them (P5).
	Conversation
	// Pages: each section after the first on a new page (P5, a selection).
	Pages
)

// Fragment is the whole print: the sheet and the sections in order.
func Fragment(sections []string, layout Layout) string {
	var b strings.Builder
	b.WriteString(`<style>` + CSS + `</style>`)
	for i, s := range sections {
		if i > 0 {
			switch layout {
			case Conversation:
				b.WriteString(`<hr class="malachi-print-sep">`)
			case Pages:
				b.WriteString(`<div class="malachi-print-page"></div>`)
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// Event is a calendar event as the client formatted its detail: the
// title, the lines under it (time, recurrence, location, calendar,
// people) and the notes as plain text.
type Event struct {
	Title string
	Lines []string
	// Attendees are the rows of the people, each "name — response".
	Attendees []string
	Notes     string
}

// EventSection is an event's print: the title, its non-empty lines, the
// attendees and the notes, all escaped.
func EventSection(e Event) string {
	var b strings.Builder
	b.WriteString(`<h1 class="malachi-print-title">` + Escape(e.Title) + `</h1>`)
	for _, l := range e.Lines {
		if strings.TrimSpace(l) != "" {
			b.WriteString(`<p class="malachi-print-line">` + Escape(l) + `</p>`)
		}
	}
	for _, a := range e.Attendees {
		b.WriteString(`<p class="malachi-print-line">` + Escape(a) + `</p>`)
	}
	if strings.TrimSpace(e.Notes) != "" {
		b.WriteString(`<div class="malachi-print-notes">` + Escape(e.Notes) + `</div>`)
	}
	return b.String()
}

// DayItem is an event of a day's print: its time column (the agenda's
// "All Day" or "9:00–10:00"), title and location.
type DayItem struct{ Time, Title, Location string }

// DaySection is a day's print (P7): the heading and a row per event;
// "none" when there is nothing that day.
func DaySection(heading string, items []DayItem, none string) string {
	var b strings.Builder
	b.WriteString(`<h1 class="malachi-print-title">` + Escape(heading) + `</h1>`)
	if len(items) == 0 {
		b.WriteString(`<p class="malachi-print-line">` + Escape(none) + `</p>`)
		return b.String()
	}
	b.WriteString(`<table class="malachi-print-day">`)
	for _, it := range items {
		b.WriteString(`<tr><td class="time">` + Escape(it.Time) + `</td><td>` + Escape(it.Title))
		if strings.TrimSpace(it.Location) != "" {
			b.WriteString(`<br>` + Escape(it.Location))
		}
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}
