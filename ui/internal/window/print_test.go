// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/printing"
	"github.com/schotek/malachi/ui/internal/widget"
)

func TestPrintLayout(t *testing.T) {
	for _, c := range []struct {
		rows, messages int
		want           printing.Layout
	}{
		{1, 1, printing.Single},
		{1, 3, printing.Conversation},
		{2, 2, printing.Pages},
		{2, 5, printing.Pages},
	} {
		if got := printLayout(c.rows, c.messages); got != c.want {
			t.Errorf("printLayout(%d, %d) = %v, want %v", c.rows, c.messages, got, c.want)
		}
	}
}

func TestPrintOrder(t *testing.T) {
	day := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	list := []api.MessageSummary{
		{ID: "c", Date: day.Add(2 * time.Hour)},
		{ID: "b", Date: day},
		{ID: "a", Date: day},
	}
	ids := func(l []api.MessageSummary) string {
		var b strings.Builder
		for _, s := range l {
			b.WriteString(string(s.ID))
		}
		return b.String()
	}
	if got := ids(printOrder(list, printing.Conversation)); got != "abc" {
		t.Errorf("conversation order %q, want oldest first, id on a tie", got)
	}
	if got := ids(printOrder(list, printing.Pages)); got != "cba" {
		t.Errorf("pages order %q, want the selection's", got)
	}
	if ids(list) != "cba" {
		t.Error("printOrder changed its argument")
	}
	many := make([]api.MessageSummary, printing.MaxMessages+7)
	if n := len(printOrder(many, printing.Pages)); n != printing.MaxMessages {
		t.Errorf("%d messages, want at most %d", n, printing.MaxMessages)
	}
}

func TestOwnRole(t *testing.T) {
	for r, want := range map[api.FolderRole]bool{
		api.RoleSent: true, api.RoleDrafts: true, api.RoleOutbox: true,
		api.RoleInbox: false, api.RoleNone: false, api.RoleArchive: false, api.RoleJunk: false,
	} {
		if ownRole(r) != want {
			t.Errorf("ownRole(%q) = %v", r, !want)
		}
	}
}

func TestPrintHeader(t *testing.T) {
	date := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	s := api.MessageSummary{
		ID: "m1", Subject: "Hello", Date: date,
		From: []api.Address{{Name: "Jana", Address: "jana@example.com"}},
		To:   []api.Address{{Address: "petr@example.com"}},
	}
	// Without message.get: the summary's fields only.
	h := printHeader(s, nil, nil, true)
	if h.Subject != "Hello" || h.From != "Jana" || h.To != "petr@example.com" || h.Cc != "" || h.Bcc != "" {
		t.Errorf("summary header %+v", h)
	}
	if h.Date != widget.FormatDateTime(date) {
		t.Errorf("date %q", h.Date)
	}
	if got := printHeader(api.MessageSummary{}, nil, nil, false); got.Subject != subjectText("") || got.Date != "" {
		t.Errorf("empty summary: subject %q date %q", got.Subject, got.Date)
	}

	m := &api.Message{
		MessageSummary: s,
		CC:             []api.Address{{Name: "Eva", Address: "eva@example.com"}},
		BCC:            []api.Address{{Name: "Tajný", Address: "secret@example.com"}},
		Attachments: []api.Attachment{
			{PartID: "2", Filename: "report.pdf", Size: 41100},
			{PartID: "3", Filename: "logo.png", ContentID: "logo", Size: 900},
			{PartID: "4", Filename: "", Size: 10},
		},
	}
	html := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: `<img src="malachi-cid:x">`,
		InlineParts: map[string]string{"logo": "3"}}
	h = printHeader(s, m, html, false)
	if h.Cc != "Eva" || h.Bcc != "" {
		t.Errorf("Cc %q, Bcc %q (not own: no Bcc)", h.Cc, h.Bcc)
	}
	want := []string{"report.pdf (" + widget.FormatSize(41100) + ")", i18n.T("Attachment") + " (" + widget.FormatSize(10) + ")"}
	if strings.Join(h.Attachments, "|") != strings.Join(want, "|") {
		t.Errorf("attachments %q, want %q (the inline picture left out)", h.Attachments, want)
	}
	if h = printHeader(s, m, nil, true); h.Bcc != "Tajný" || len(h.Attachments) != 3 {
		t.Errorf("own: Bcc %q, %d attachments (a text body lists the picture)", h.Bcc, len(h.Attachments))
	}
}

func TestPrintBody(t *testing.T) {
	s := api.MessageSummary{Snippet: "snippet"}
	for _, c := range []struct {
		name string
		b    *api.MessageBodyResult
		want printing.Body
	}{
		{"html", &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", Text: "x"}, printing.Body{HTML: "<p>x</p>"}},
		{"withheld", &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", HTMLWithheld: true, Text: "t"}, printing.Body{Text: "t"}},
		{"text", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "plain"}, printing.Body{Text: "plain"}},
		{"not fetched", &api.MessageBodyResult{HTML: "<p>x</p>"}, printing.Body{Text: "snippet"}},
		{"none", nil, printing.Body{Text: "snippet"}},
	} {
		if got := printBody(s, c.b); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestPrintEvent(t *testing.T) {
	d := calendar.Detail{
		Title: "Planning", TimeLine: "Tuesday", Location: "Room 1", CalendarLine: "Work · Calendar",
		Attendees: []calendar.AttendeeRow{
			{Name: "Jana", Address: "jana@example.com", ResponseText: "Accepted"},
			{Address: "petr@example.com"},
		},
		Notes: "Bring <notes>",
	}
	e := printEvent(d)
	if e.Title != "Planning" || len(e.Lines) != 5 || e.Lines[0] != "Tuesday" || e.Lines[2] != "Room 1" || e.Notes != "Bring <notes>" {
		t.Errorf("event %+v", e)
	}
	if strings.Join(e.Attendees, "|") != "Jana — Accepted|petr@example.com" {
		t.Errorf("attendees %q", e.Attendees)
	}
	if html := printing.EventSection(e); strings.Contains(html, "<notes>") {
		t.Errorf("notes not escaped: %s", html)
	}
}

func TestPrintDay(t *testing.T) {
	env := calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.UTC}
	day := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	now := day.Add(8 * time.Hour)
	empty := printDay(nil, day, now, env, false)
	if !strings.Contains(empty, printing.Escape(calendar.AgendaEmpty(env.Tr))) {
		t.Errorf("empty day: %s", empty)
	}
	at := func(h, m int) *time.Time {
		t := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		return &t
	}
	occ := []api.Occurrence{
		{Title: "Standup <daily>", Location: "Room", Start: at(9, 0), End: at(9, 15)},
		{Title: "Tomorrow", Start: at(33, 0), End: at(34, 0)},
	}
	got := printDay(occ, day, now, env, false)
	if !strings.Contains(got, "Standup &lt;daily&gt;") || !strings.Contains(got, "Room") || strings.Contains(got, "Tomorrow") {
		t.Errorf("day: %s", got)
	}
}
