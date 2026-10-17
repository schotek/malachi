// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/imip"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

func invitationICS(t *testing.T) []byte {
	t.Helper()
	ev, err := calendar.FromInput(api.EventInput{
		Title: "Planning", Start: "2026-10-22T10:00:00", End: "2026-10-22T11:00:00", TimeZone: "Europe/Prague",
		Attendees: []api.EventInputAttendee{{Name: "Bob", Address: bob.Address}},
	}, calendar.InputOptions{UID: "plan@example.org", Organizer: &alice, DefaultAlertMinutes: -1})
	if err != nil {
		t.Fatal(err)
	}
	data, err := imip.BuildRequest(calendar.Object{Master: &ev}, alice, time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// calendarPart returns the text/calendar part of a built message as
// internal/mime sees it.
func calendarPartOf(t *testing.T, raw []byte) (*mime.Parsed, *mime.Part) {
	t.Helper()
	p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Attachments {
		if a.ContentType == "text/calendar" {
			part, err := mime.ExtractPart(bytes.NewReader(raw), a.PartID, mime.DefaultLimits(), api.MaxICSBytes)
			if err != nil {
				t.Fatal(err)
			}
			return p, part
		}
	}
	t.Fatalf("no text/calendar part in\n%s", raw)
	return nil, nil
}

func TestBuildCalendarAlternative(t *testing.T) {
	data := invitationICS(t)
	raw := build(t, BuildInput{From: alice, To: []api.Address{bob}, Subject: "Invitation: Planning", Text: "Join us.",
		Calendar: &CalendarPart{Method: "request", Data: data}})
	p, part := calendarPartOf(t, raw)
	if p.Text != "Join us." || p.TextPartID != "1" || part.PartID != "2" || p.HasHTML {
		t.Errorf("parts: text %q (%s), calendar %s", p.Text, p.TextPartID, part.PartID)
	}
	if !bytes.Equal(part.Body, data) {
		t.Errorf("calendar part changed in transit:\n%q\n%q", part.Body, data)
	}
	s := string(raw)
	if !strings.Contains(s, "Content-Type: multipart/alternative;") ||
		!strings.Contains(s, `Content-Type: text/calendar; charset=UTF-8; method=REQUEST`) {
		t.Errorf("headers:\n%s", s)
	}
	inv, err := imip.ParseMessage(part.Body, []string{bob.Address})
	if err != nil || inv.Method != api.InvitationMethodRequest || inv.Me == nil {
		t.Errorf("parse back: %v %+v", err, inv)
	}
}

func TestBuildCalendarWithHTMLAndFiles(t *testing.T) {
	data := invitationICS(t)
	in := BuildInput{From: alice, To: []api.Address{bob}, Subject: "Planning", Text: "Agenda", HTML: "<p>Agenda</p>",
		Calendar: &CalendarPart{Method: "REQUEST", Data: data},
		Attachments: []Attachment{{Filename: "agenda.pdf", ContentType: "application/pdf",
			Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("%PDF-1.4")), nil }}}}
	raw := build(t, in)
	p, part := calendarPartOf(t, raw)
	// mixed[alternative[text/plain 1.1, text/html 1.2, text/calendar 1.3], file 2]
	if p.TextPartID != "1.1" || p.HTMLPartID != "1.2" || part.PartID != "1.3" || !bytes.Equal(part.Body, data) {
		t.Errorf("parts: text %s html %s calendar %s", p.TextPartID, p.HTMLPartID, part.PartID)
	}
	ids := PartIDs(in)
	var file string
	for _, a := range p.Attachments {
		if a.Filename == "agenda.pdf" {
			file = a.PartID
		}
	}
	if len(ids) != 1 || ids[0] != file || file != "2" {
		t.Errorf("PartIDs %v, file %s", ids, file)
	}
	// With inline pictures the calendar follows the related group.
	in.Attachments = append(in.Attachments, Attachment{Filename: "logo.png", ContentType: "image/png", Inline: true, ContentID: "logo@example.org",
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("png")), nil }})
	in.HTML = `<p>Agenda <img src="cid:logo@example.org"></p>`
	raw = build(t, in)
	_, part = calendarPartOf(t, raw)
	if part.PartID != "1.3" {
		t.Errorf("calendar after related: %s", part.PartID)
	}
}

func TestBuildCalendarRefused(t *testing.T) {
	var buf bytes.Buffer
	for _, c := range []*CalendarPart{
		{Method: "X-EVIL\r\nBcc: x@example.org", Data: []byte("BEGIN:VCALENDAR")},
		{Method: "REQUEST"},
		{Method: "REQUEST", Data: bytes.Repeat([]byte("x"), api.MaxICSBytes+1)},
	} {
		err := BuildMessage(&buf, BuildInput{From: alice, To: []api.Address{bob}, Calendar: c})
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.Code != api.CodeInvalidArgument {
			t.Errorf("%q: %v", c.Method, err)
		}
	}
}
