// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed samples/*.json
var samples embed.FS

// Corpus is one sample mailbox: the user, the people who write to them
// and the messages, in the order they are appended.
type Corpus struct {
	Lang    string            `json:"lang"`
	Company string            `json:"company"`
	Me      Person            `json:"me"`
	People  map[string]Person `json:"people"`
	// Attribution is the line above a quoted message: "On %s, %s wrote:"
	// with the date and the name, in the corpus's language; DateFormat is
	// the date's Go layout.
	Attribution string    `json:"attribution"`
	DateFormat  string    `json:"dateFormat"`
	Messages    []Message `json:"messages"`
	// Jira is the issue tracker of the same story (jira.go).
	Jira *JiraSet `json:"jira,omitempty"`
	// Eway is the CRM of the same story (eway.go).
	Eway *EwaySet `json:"eway,omitempty"`
}

// Person is a correspondent.
type Person struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Company string `json:"company,omitempty"`
}

// Message is one message of the corpus. People are named by their key in
// Corpus.People, "me" for the user, or written out as "Name <address>".
type Message struct {
	ID      string   `json:"id"`
	Folder  string   `json:"folder,omitempty"` // INBOX when empty
	From    string   `json:"from"`
	To      []string `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	ReplyTo string   `json:"replyTo,omitempty"`
	// When is relative to the corpus's clock: "-5d 09:12" (five days ago
	// at that time), "-3h", "-45m", "+2d 10:00".
	When    string `json:"when"`
	Subject string `json:"subject"`
	// Text is the body: paragraphs separated by blank lines. A paragraph
	// starting with "- " is a list, lines inside a paragraph are kept.
	Text string `json:"text"`
	// HTML adds a text/html alternative of the same body.
	HTML bool `json:"html,omitempty"`
	// Table is appended as a table (HTML) and as aligned lines (text).
	Table [][]string `json:"table,omitempty"`
	// Quote cites the message InReplyTo names below the text, with the
	// attribution line, the way a mail client does.
	Quote bool `json:"quote,omitempty"`
	// Sign appends the sender's signature.
	Sign  bool     `json:"sign,omitempty"`
	Flags []string `json:"flags,omitempty"` // seen, flagged, answered, draft
	// InReplyTo names another message of the corpus by id.
	InReplyTo   string       `json:"inReplyTo,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	List        *ListInfo    `json:"list,omitempty"`
	// Automated marks mail from a machine (Auto-Submitted, Precedence).
	Automated bool `json:"automated,omitempty"`
	// RemoteImage puts an image from the web into the HTML body.
	RemoteImage bool `json:"remoteImage,omitempty"`
	// Invite adds a text/calendar part (METHOD:REQUEST) for the event.
	Invite *Invite `json:"invite,omitempty"`
}

// Attachment is a generated file: Kind pdf (a one-page PDF with Text),
// png (a drawn picture), or text (Text as is); KB pads it to about that
// size so that the size badge looks real.
type Attachment struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	KB   int    `json:"kb,omitempty"`
}

// ListInfo makes the message a mailing-list or newsletter message.
type ListInfo struct {
	ID          string `json:"id"`
	Unsubscribe string `json:"unsubscribe"` // a URL, a mailto: or both separated by ", "
	OneClick    bool   `json:"oneClick,omitempty"`
	// Post is the list's posting address (List-Post): with it the message
	// is a mailing-list message, without it a newsletter.
	Post string `json:"post,omitempty"`
}

// Invite is a meeting request.
type Invite struct {
	Summary   string   `json:"summary"`
	Location  string   `json:"location,omitempty"`
	When      string   `json:"when"` // as Message.When
	Hours     float64  `json:"hours"`
	Organizer string   `json:"organizer"` // a person key
	Attendees []string `json:"attendees"`
}

// loadCorpus reads the built-in sample set of a language.
func loadCorpus(lang string) (*Corpus, error) {
	b, err := samples.ReadFile("samples/" + lang + ".json")
	if err != nil {
		return nil, fmt.Errorf("no sample set for language %q", lang)
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("samples/%s.json: %w", lang, err)
	}
	if c.Me.Email == "" || len(c.Messages) == 0 {
		return nil, fmt.Errorf("samples/%s.json: no user or no messages", lang)
	}
	seen := map[string]bool{}
	for i := range c.Messages {
		m := &c.Messages[i]
		if m.ID == "" || seen[m.ID] {
			return nil, fmt.Errorf("samples/%s.json: message %d has no id or a repeated one", lang, i)
		}
		seen[m.ID] = true
		if m.Folder == "" {
			m.Folder = "INBOX"
		}
	}
	for i, m := range c.Messages {
		if m.InReplyTo != "" && !seen[m.InReplyTo] {
			return nil, fmt.Errorf("samples/%s.json: message %d replies to unknown %q", lang, i, m.InReplyTo)
		}
	}
	return &c, nil
}

// person resolves a people key, "me" or a literal "Name <addr>".
func (c *Corpus) person(key string) Person {
	if key == "me" {
		return c.Me
	}
	if p, ok := c.People[key]; ok {
		return p
	}
	if a, err := mail.ParseAddress(key); err == nil {
		return Person{Name: a.Name, Email: a.Address}
	}
	return Person{Name: key, Email: key}
}

func (c *Corpus) dateFormat() string {
	if c.DateFormat != "" {
		return c.DateFormat
	}
	return "2 Jan 2006 15:04"
}

func (c *Corpus) byID(id string) *Message {
	for i := range c.Messages {
		if c.Messages[i].ID == id {
			return &c.Messages[i]
		}
	}
	return nil
}

var whenRe = regexp.MustCompile(`^([+-]\d+)([dhm])(?:\s+(\d{1,2}):(\d{2}))?$`)

// at resolves a relative time against the clock in the local zone.
func at(spec string, now time.Time) (time.Time, error) {
	m := whenRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return time.Time{}, fmt.Errorf("bad time %q (want e.g. \"-5d 09:12\", \"-3h\", \"+2d 10:00\")", spec)
	}
	n, _ := strconv.Atoi(m[1])
	t := now
	switch m[2] {
	case "d":
		t = now.AddDate(0, 0, n)
	case "h":
		t = now.Add(time.Duration(n) * time.Hour)
	case "m":
		t = now.Add(time.Duration(n) * time.Minute)
	}
	if m[3] != "" {
		h, _ := strconv.Atoi(m[3])
		mi, _ := strconv.Atoi(m[4])
		t = time.Date(t.Year(), t.Month(), t.Day(), h, mi, 0, 0, t.Location())
	}
	return t.Truncate(time.Minute), nil
}

// messageID is the Message-ID of a corpus message.
func messageID(id string) string { return "<" + id + ".devmail@example.test>" }

// Built is a message rendered to RFC 5322 bytes.
type Built struct {
	Folder string
	Date   time.Time
	Flags  []string
	Raw    []byte
}

// build renders every message of the corpus.
func (c *Corpus) build(now time.Time) ([]Built, error) {
	var out []Built
	for i := range c.Messages {
		b, err := c.buildOne(&c.Messages[i], now)
		if err != nil {
			return nil, fmt.Errorf("message %q: %w", c.Messages[i].ID, err)
		}
		out = append(out, b)
	}
	return out, nil
}

func (c *Corpus) buildOne(m *Message, now time.Time) (Built, error) {
	date, err := at(m.When, now)
	if err != nil {
		return Built{}, err
	}
	var h strings.Builder
	from := c.person(m.From)
	fmt.Fprintf(&h, "From: %s\r\n", addr(from))
	if len(m.To) > 0 {
		fmt.Fprintf(&h, "To: %s\r\n", c.addrs(m.To))
	}
	if len(m.Cc) > 0 {
		fmt.Fprintf(&h, "Cc: %s\r\n", c.addrs(m.Cc))
	}
	if m.ReplyTo != "" {
		fmt.Fprintf(&h, "Reply-To: %s\r\n", addr(c.person(m.ReplyTo)))
	}
	fmt.Fprintf(&h, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&h, "Date: %s\r\n", date.Format(time.RFC1123Z))
	fmt.Fprintf(&h, "Message-ID: %s\r\n", messageID(m.ID))
	if m.InReplyTo != "" {
		fmt.Fprintf(&h, "In-Reply-To: %s\r\n", messageID(m.InReplyTo))
		var refs []string
		for p := c.byID(m.InReplyTo); p != nil; p = c.byID(p.InReplyTo) {
			refs = append([]string{messageID(p.ID)}, refs...)
		}
		fmt.Fprintf(&h, "References: %s\r\n", strings.Join(refs, " "))
	}
	if m.List != nil {
		fmt.Fprintf(&h, "List-Id: %s\r\n", m.List.ID)
		var targets []string
		for _, t := range strings.Split(m.List.Unsubscribe, ",") {
			if t = strings.TrimSpace(t); t != "" {
				targets = append(targets, "<"+t+">")
			}
		}
		fmt.Fprintf(&h, "List-Unsubscribe: %s\r\n", strings.Join(targets, ", "))
		if m.List.OneClick {
			fmt.Fprintf(&h, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
		}
		if m.List.Post != "" {
			fmt.Fprintf(&h, "List-Post: <mailto:%s>\r\n", m.List.Post)
		}
		fmt.Fprintf(&h, "Precedence: bulk\r\n")
	}
	if m.Automated {
		fmt.Fprintf(&h, "Auto-Submitted: auto-generated\r\nPrecedence: bulk\r\n")
	}
	fmt.Fprintf(&h, "MIME-Version: 1.0\r\n")

	text, htmlBody := c.bodies(m, from, date)
	var body bytes.Buffer
	var parts []part
	if m.HTML || m.Invite != nil || m.RemoteImage || len(m.Table) > 0 {
		alt := []part{{ctype: "text/plain; charset=utf-8", body: qp(text)}, {ctype: "text/html; charset=utf-8", body: qp(htmlBody)}}
		if m.Invite != nil {
			ics, err := c.ics(m, date, now)
			if err != nil {
				return Built{}, err
			}
			alt = append(alt, part{ctype: `text/calendar; charset=utf-8; method=REQUEST; name="invite.ics"`, body: qp(ics)})
		}
		parts = append(parts, multipart("alternative", alt, m.ID+"-alt"))
	} else {
		parts = append(parts, part{ctype: "text/plain; charset=utf-8", body: qp(text)})
	}
	for _, a := range m.Attachments {
		data, ctype, err := generate(a)
		if err != nil {
			return Built{}, err
		}
		name := mime.QEncoding.Encode("utf-8", a.Name)
		parts = append(parts, part{
			ctype:       ctype + `; name="` + name + `"`,
			disposition: `attachment; filename="` + name + `"`,
			encoding:    "base64",
			body:        wrap76(base64.StdEncoding.EncodeToString(data)),
		})
	}
	var top part
	if len(parts) == 1 {
		top = parts[0]
	} else {
		top = multipart("mixed", parts, m.ID+"-mix")
	}
	fmt.Fprintf(&h, "Content-Type: %s\r\n", top.ctype)
	enc := top.encoding
	if enc == "" {
		enc = "quoted-printable"
	}
	fmt.Fprintf(&h, "Content-Transfer-Encoding: %s\r\n", enc)
	h.WriteString("\r\n")
	body.WriteString(h.String())
	body.WriteString(top.body)
	return Built{Folder: m.Folder, Date: date, Flags: m.Flags, Raw: body.Bytes()}, nil
}

// part is one MIME body part, already encoded.
type part struct {
	ctype       string
	disposition string
	encoding    string // "" = quoted-printable is already in body
	body        string
}

// multipart joins parts under a boundary.
func multipart(kind string, parts []part, tag string) part {
	boundary := "=_" + strings.NewReplacer("-", "_", ".", "_").Replace(tag) + "_" + fmt.Sprintf("%04x", len(parts))
	var b strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s\r\n", boundary, p.ctype)
		enc := p.encoding
		if enc == "" {
			enc = "quoted-printable"
		}
		fmt.Fprintf(&b, "Content-Transfer-Encoding: %s\r\n", enc)
		if p.disposition != "" {
			fmt.Fprintf(&b, "Content-Disposition: %s\r\n", p.disposition)
		}
		b.WriteString("\r\n")
		b.WriteString(p.body)
		if !strings.HasSuffix(p.body, "\r\n") {
			b.WriteString("\r\n")
		}
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return part{ctype: fmt.Sprintf(`multipart/%s; boundary="%s"`, kind, boundary), encoding: "7bit", body: b.String()}
}

func qp(s string) string {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(strings.ReplaceAll(s, "\n", "\r\n")))
	_ = w.Close()
	return b.String()
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteString("\r\n")
		s = s[76:]
	}
	b.WriteString(s)
	b.WriteString("\r\n")
	return b.String()
}

func addr(p Person) string {
	return (&mail.Address{Name: p.Name, Address: p.Email}).String()
}

func (c *Corpus) addrs(keys []string) string {
	var out []string
	for _, k := range keys {
		out = append(out, addr(c.person(k)))
	}
	return strings.Join(out, ",\r\n ")
}

// bodies renders the text and HTML versions of a message.
func (c *Corpus) bodies(m *Message, from Person, date time.Time) (text, htmlBody string) {
	var t, hb strings.Builder
	hb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"></head><body style="font-family:-apple-system,'Segoe UI',Cantarell,sans-serif;font-size:15px;line-height:1.5;color:#1b1b1f;margin:0;padding:8px 0">` + "\n")
	if m.RemoteImage {
		hb.WriteString(`<p><img src="https://img.example.test/mail/header-` + m.ID + `.png" width="560" height="120" alt="" style="max-width:100%;height:auto;display:block"></p>` + "\n")
	}
	for _, para := range strings.Split(strings.TrimSpace(m.Text), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		t.WriteString(para + "\n\n")
		lines := strings.Split(para, "\n")
		if strings.HasPrefix(lines[0], "- ") {
			hb.WriteString("<ul>\n")
			for _, l := range lines {
				hb.WriteString("<li>" + html.EscapeString(strings.TrimPrefix(l, "- ")) + "</li>\n")
			}
			hb.WriteString("</ul>\n")
			continue
		}
		var esc []string
		for _, l := range lines {
			esc = append(esc, html.EscapeString(l))
		}
		hb.WriteString("<p>" + strings.Join(esc, "<br>\n") + "</p>\n")
	}
	if len(m.Table) > 0 {
		widths := make([]int, len(m.Table[0]))
		for _, row := range m.Table {
			for i, cell := range row {
				if i < len(widths) && len([]rune(cell)) > widths[i] {
					widths[i] = len([]rune(cell))
				}
			}
		}
		hb.WriteString(`<table cellpadding="6" style="border-collapse:collapse;font-size:14px">` + "\n")
		for r, row := range m.Table {
			var cells []string
			hb.WriteString("<tr>")
			for i, cell := range row {
				cells = append(cells, cell+strings.Repeat(" ", widths[i]-len([]rune(cell))))
				tag := "td"
				style := ` style="border-bottom:1px solid #d9dbe3;text-align:left"`
				if r == 0 {
					tag = "th"
					style = ` style="border-bottom:2px solid #9aa0ad;text-align:left"`
				}
				hb.WriteString("<" + tag + style + ">" + html.EscapeString(cell) + "</" + tag + ">")
			}
			hb.WriteString("</tr>\n")
			t.WriteString(strings.TrimRight(strings.Join(cells, "  "), " ") + "\n")
		}
		hb.WriteString("</table>\n")
		t.WriteString("\n")
	}
	if m.Sign {
		sig := from.Name
		if from.Company != "" {
			sig += "\n" + from.Company
		}
		t.WriteString("-- \n" + sig + "\n")
		hb.WriteString(`<p style="color:#5d6068">-- <br>` + strings.ReplaceAll(html.EscapeString(sig), "\n", "<br>") + "</p>\n")
	}
	if m.Quote && m.InReplyTo != "" {
		if prev := c.byID(m.InReplyTo); prev != nil {
			pdate, _ := at(prev.When, date)
			who := c.person(prev.From)
			line := fmt.Sprintf(c.Attribution, pdate.Format(c.dateFormat()), who.Name)
			ptext, _ := c.bodies(prev, who, pdate)
			t.WriteString("\n" + line + "\n")
			for _, l := range strings.Split(strings.TrimRight(ptext, "\n"), "\n") {
				t.WriteString("> " + l + "\n")
			}
			hb.WriteString(`<div style="color:#5d6068;margin-top:1.2em">` + html.EscapeString(line) + "</div>\n")
			hb.WriteString(`<blockquote type="cite" style="margin:0.5em 0 0 0;padding-left:1em;border-left:2px solid #c7cad3;color:#5d6068">` + "\n")
			for _, l := range strings.Split(strings.TrimRight(ptext, "\n"), "\n\n") {
				hb.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(l), "\n", "<br>\n") + "</p>\n")
			}
			hb.WriteString("</blockquote>\n")
		}
	}
	hb.WriteString("</body></html>\n")
	return t.String(), hb.String()
}

// ics is the calendar part of an invitation.
func (c *Corpus) ics(m *Message, date, now time.Time) (string, error) {
	start, err := at(m.Invite.When, now)
	if err != nil {
		return "", err
	}
	end := start.Add(time.Duration(m.Invite.Hours * float64(time.Hour)))
	org := c.person(m.Invite.Organizer)
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\r\n") }
	w("BEGIN:VCALENDAR")
	w("PRODID:-//Malachi devmail//EN")
	w("VERSION:2.0")
	w("METHOD:REQUEST")
	w("BEGIN:VEVENT")
	w("UID:" + m.ID + "-event@example.test")
	w("DTSTAMP:" + date.UTC().Format("20060102T150405Z"))
	w("DTSTART:" + start.UTC().Format("20060102T150405Z"))
	w("DTEND:" + end.UTC().Format("20060102T150405Z"))
	w("SUMMARY:" + icsText(m.Invite.Summary))
	if m.Invite.Location != "" {
		w("LOCATION:" + icsText(m.Invite.Location))
	}
	w("ORGANIZER;CN=" + icsText(org.Name) + ":mailto:" + org.Email)
	for _, a := range m.Invite.Attendees {
		p := c.person(a)
		w("ATTENDEE;CN=" + icsText(p.Name) + ";ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + p.Email)
	}
	w("SEQUENCE:0")
	w("STATUS:CONFIRMED")
	w("END:VEVENT")
	w("END:VCALENDAR")
	return b.String(), nil
}

func icsText(s string) string {
	return strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n").Replace(s)
}

// generate makes an attachment's bytes and content type.
func generate(a Attachment) ([]byte, string, error) {
	switch a.Kind {
	case "pdf":
		return pdfBytes(a.Text, a.KB), "application/pdf", nil
	case "png":
		return pngBytes(a.Name), "image/png", nil
	case "text":
		data := []byte(a.Text)
		if a.KB > 0 {
			for len(data) < a.KB*1024 {
				data = append(data, []byte(a.Text+"\n")...)
			}
		}
		ctype := "text/plain"
		if strings.HasSuffix(a.Name, ".csv") {
			ctype = "text/csv"
		}
		return data, ctype, nil
	}
	return nil, "", fmt.Errorf("attachment %q: unknown kind %q", a.Name, a.Kind)
}

// pdfBytes is a one-page PDF with the text in Helvetica, padded with a
// comment to about kb kilobytes. Every offset in the xref is real.
func pdfBytes(text string, kb int) []byte {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(l))
	}
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 72 760 Td 16 TL\n")
	for _, l := range lines {
		content.WriteString("(" + l + ") Tj T*\n")
	}
	content.WriteString("ET\n")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	if pad := kb*1024 - 700 - content.Len(); pad > 0 {
		b.WriteString("%")
		for pad > 0 {
			n := min(pad, 72)
			b.WriteString(strings.Repeat("x", n) + "\n%")
			pad -= n + 2
		}
		b.WriteString("\n")
	}
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// pngBytes draws a picture whose colours follow the name, so that two
// attachments differ.
func pngBytes(name string) []byte {
	const w, h = 640, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := 0
	for _, r := range name {
		seed = seed*31 + int(r)
	}
	seed &= 0xff
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8((x*255/w + seed) & 0xff)
			g := uint8((y*255/h + seed/2) & 0xff)
			b := uint8((160 + (x+y)*60/(w+h)) & 0xff)
			if (x/40+y/40)%7 == 0 {
				r, g, b = r/2, g/2, b/2+40
			}
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
