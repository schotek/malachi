// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

const corpusDir = "../../testdata/calendar"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseFixture(t *testing.T, name string, opts FromOptions) Object {
	t.Helper()
	obj, err := Parse(readFixture(t, name), opts)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return obj
}

func TestCleanLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Plan\u202Eing \u200B now\r\n second\tline ", "Planing now second line"},
		{"a\u2028b\u2029c\u0085d", "a b c d"},
		{"x\x00y\x1bz\x7f", "xyz"},
		{"\uFEFFlead", "lead"},
		{"bad \xff utf8", "bad � utf8"},
		{"emoji 👩\u200D💻 ok", "emoji 👩\u200D💻 ok"},
		{"\u2066isolate\u2069", "isolate"},
	}
	for _, c := range cases {
		if got := CleanLine(c.in, 0); got != c.want {
			t.Errorf("CleanLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := CleanLine("ěěěěě", 5); got != "ěě" {
		t.Errorf("cap on a rune boundary: %q", got)
	}
}

func TestCleanBlock(t *testing.T) {
	in := "\n\nAgenda:\r\n  - one  \r\n\r\n\r\n\r\n- two\u2028- three\u202E\n\n"
	want := "Agenda:\n  - one\n\n- two\n- three"
	if got := CleanBlock(in, 0); got != want {
		t.Errorf("CleanBlock = %q, want %q", got, want)
	}
	long := strings.Repeat("line\n", 20000)
	if got := CleanBlock(long, MaxNotesBytes); len(got) > MaxNotesBytes {
		t.Errorf("not capped: %d", len(got))
	}
}

func TestLinks(t *testing.T) {
	text := "Join https://Teams.Microsoft.com/l/meetup-join/19%3a, or (https://example.org/a_(b)) and " +
		"<https://example.org/x>; HTTPS://EXAMPLE.ORG/up. Not: http://example.org javascript:alert(1) " +
		"data:text/html,x file:///etc/passwd webcal://example.org tel:+1 https://user:pw@example.org/ " +
		"https:///nohost xhttps://example.org/glued https://bücher.example/straße https://example.org/a\u202Eb " +
		"https://example.org/x"
	got := Links(text)
	want := []string{
		"https://teams.microsoft.com/l/meetup-join/19%3a",
		"https://example.org/a_(b)",
		"https://example.org/x",
		"https://example.org/up",
		"https://xn--bcher-kva.example/stra%C3%9Fe",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Links =\n %q\nwant\n %q", got, want)
	}
	if _, ok := SafeLink("https://example.org/" + strings.Repeat("a", MaxLinkBytes)); ok {
		t.Error("an over-long link passed")
	}
	if _, ok := SafeLink("https://exa mple.org/"); ok {
		t.Error("a link with a space passed")
	}
}

func TestDetectVideoCall(t *testing.T) {
	cases := []struct {
		name            string
		location, notes string
		conference      []string
		join            string
		wantURL         string
		wantProvider    api.VideoProvider
	}{
		{"teams in notes", "", "Click https://teams.microsoft.com/l/meetup-join/abc to join", nil, "", "https://teams.microsoft.com/l/meetup-join/abc", api.VideoProviderTeams},
		{"zoom subdomain", "https://us02web.zoom.us/j/123?pwd=x", "", nil, "", "https://us02web.zoom.us/j/123?pwd=x", api.VideoProviderZoom},
		{"meet conference", "", "", []string{"https://meet.google.com/abc-defg-hij"}, "", "https://meet.google.com/abc-defg-hij", api.VideoProviderMeet},
		{"webex", "", "https://acme.webex.com/meet/ada", nil, "", "https://acme.webex.com/meet/ada", api.VideoProviderWebex},
		{"jitsi", "", "https://meet.jit.si/Room", nil, "", "https://meet.jit.si/Room", api.VideoProviderJitsi},
		{"whereby", "", "https://whereby.com/ada", nil, "", "https://whereby.com/ada", api.VideoProviderWhereby},
		{"teams live", "", "https://teams.live.com/meet/1", nil, "", "https://teams.live.com/meet/1", api.VideoProviderTeams},
		{"unknown host in notes is no call", "", "https://example.org/room", nil, "", "", ""},
		{"lookalike host", "", "https://zoom.us.example.org/j/1 https://evilzoom.us/j/2", nil, "", "", ""},
		{"unknown conference is other", "", "", []string{"https://meet.example.org/room"}, "", "https://meet.example.org/room", api.VideoProviderOther},
		{"join url first", "", "https://meet.jit.si/x", nil, "https://teams.microsoft.com/l/1", "https://teams.microsoft.com/l/1", api.VideoProviderTeams},
		{"http conference refused", "", "", []string{"http://meet.example.org/room", "https://u:p@meet.example.org/r", "webcal://x"}, "", "", ""},
	}
	for _, c := range cases {
		vc := DetectVideoCall(c.location, c.notes, c.conference, c.join)
		switch {
		case c.wantURL == "" && vc != nil:
			t.Errorf("%s: got %+v, want none", c.name, vc)
		case c.wantURL != "" && (vc == nil || vc.URL != c.wantURL || vc.Provider != c.wantProvider):
			t.Errorf("%s: got %+v, want %s %s", c.name, vc, c.wantURL, c.wantProvider)
		}
	}
}

func TestCorpusLinksAndVideo(t *testing.T) {
	uj := parseFixture(t, "url-javascript.ics", FromOptions{})
	if len(uj.Master.Links) != 0 || uj.Master.VideoCall != nil {
		t.Errorf("url-javascript: links %q video %+v", uj.Master.Links, uj.Master.VideoCall)
	}
	ch := parseFixture(t, "conference-http.ics", FromOptions{})
	if ch.Master.VideoCall != nil {
		t.Errorf("conference-http: %+v", ch.Master.VideoCall)
	}
	lw := parseFixture(t, "location-with-url.ics", FromOptions{})
	m := lw.Master
	if m.Location != "Room 4, Building B; or https://meet.example.org/abc-defg" {
		t.Errorf("location = %q", m.Location)
	}
	if !slices.Equal(m.Links, []string{"https://meet.example.org/abc-defg"}) {
		t.Errorf("links = %q", m.Links)
	}
	if m.VideoCall != nil {
		t.Errorf("an unknown host in location is not a call: %+v", m.VideoCall)
	}
	if !strings.Contains(m.Notes, "Phone: +1 000 000 0000\nhttp://plain.example.org") {
		t.Errorf("notes = %q", m.Notes)
	}
	gr := parseFixture(t, "google-request.ics", FromOptions{})
	if vc := gr.Master.VideoCall; vc == nil || vc.Provider != api.VideoProviderOther || vc.URL != "https://meet.example.org/abc-defg-hij" {
		t.Errorf("google X-GOOGLE-CONFERENCE: %+v", vc)
	}
	tr := parseFixture(t, "teams-request.ics", FromOptions{})
	if vc := tr.Master.VideoCall; vc == nil || vc.Host != "teams.example.org" || vc.Provider != api.VideoProviderOther {
		t.Errorf("teams X-MICROSOFT-SKYPETEAMSMEETINGURL: %+v", vc)
	}
}

func TestNotesFromHTML(t *testing.T) {
	got := NotesFromHTML(`<html><body><script>alert(1)</script><p onclick="x()">Agenda</p><p>Item&nbsp;<b>one</b><br>two</p><img src="https://example.org/t.gif"></body></html>`)
	if strings.ContainsAny(got, "<>") || strings.Contains(got, "alert") || !strings.Contains(got, "Agenda") || !strings.Contains(got, "one") {
		t.Errorf("NotesFromHTML = %q", got)
	}
	if NotesFromHTML("   ") != "" {
		t.Error("empty html")
	}
	xad := parseFixture(t, "x-alt-desc-html.ics", FromOptions{})
	if xad.Master.Notes != "Plain text agenda\nline two" {
		t.Errorf("X-ALT-DESC must be ignored: %q", xad.Master.Notes)
	}
	sd := parseFixture(t, "styled-description.ics", FromOptions{})
	if sd.Master.Notes != "Plain" {
		t.Errorf("DESCRIPTION wins over STYLED-DESCRIPTION: %q", sd.Master.Notes)
	}
}
