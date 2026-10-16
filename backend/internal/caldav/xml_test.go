// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"net/url"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

var fixtureBase, _ = url.Parse("https://cloud.example.test/remote.php/dav/calendars/jana/personal/")

func TestFixturesRefused(t *testing.T) {
	for _, name := range []string{
		"multistatus-deep.xml", "multistatus-garbage.xml", "doctype-entities.xml", "propfind-no-namespaces.xml",
	} {
		ms, err := parseMultistatus(caldavtest.Fixture(t, name), fixtureBase)
		if code(err) != api.CodeMalformedCalendar {
			t.Errorf("%s: %+v, %v; want malformedCalendar", name, ms, err)
		}
	}
}

func TestDoctypeNeverExpands(t *testing.T) {
	// Whatever the decoder does with the DTD, no entity text may come out.
	ms, err := parseMultistatus(caldavtest.Fixture(t, "doctype-entities.xml"), fixtureBase)
	if err == nil {
		for _, r := range ms.Responses {
			if strings.Contains(r.Props.DisplayName, "lol") || strings.Contains(r.Props.DisplayName, "root") {
				t.Fatalf("entity expanded: %q", r.Props.DisplayName)
			}
		}
	}
}

func TestHrefsOnAnotherOriginDropped(t *testing.T) {
	ms, err := parseMultistatus(caldavtest.Fixture(t, "multistatus-href-other-host.xml"), fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms.Responses) != 1 || ms.Responses[0].Href != "/remote.php/dav/calendars/jana/personal/good.ics" {
		t.Fatalf("responses: %+v", ms.Responses)
	}
	if ms.Dropped != 8 {
		t.Fatalf("dropped %d, want 8", ms.Dropped)
	}
}

func TestTruncatedSyncFixture(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.FixtureAnswer(t, "sync-collection-truncated.xml", 0))
	res, err := newClient(t, s).SyncCollection(t.Context(), cal, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Token != "http://sabre.io/ns/sync/1-17" || len(res.Changed) != 1 || res.Changed[0].ETag != `"a1"` ||
		len(res.Removed) != 1 || !strings.HasSuffix(res.Removed[0], "/b.ics") {
		t.Fatalf("result: %+v", res)
	}
}

func TestCalendarColorAlpha(t *testing.T) {
	ms, err := parseMultistatus(caldavtest.Fixture(t, "calendar-color-alpha.xml"), fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"alpha": "#ff2968", "short": "#aabbcc", "named": "", "broken": "", "plain": "#0082c9"}
	if len(ms.Responses) != len(want) {
		t.Fatalf("responses %d", len(ms.Responses))
	}
	for _, r := range ms.Responses {
		name := hrefName(r.Href)
		if r.Props.Color != want[name] {
			t.Errorf("%s: %q, want %q", name, r.Props.Color, want[name])
		}
		if !r.Props.Is(TypeCalendar) {
			t.Errorf("%s: not a calendar", name)
		}
	}
	for in, out := range map[string]string{"#FFF": "#ffffff", "#12345678": "#123456", "#1234567": "", "": "", "#": "", "rgb(1,2,3)": ""} {
		if got := NormaliseColor(in); got != out {
			t.Errorf("NormaliseColor(%q) = %q, want %q", in, got, out)
		}
	}
}

func TestXMLCaps(t *testing.T) {
	gen := func(n int) []byte {
		return []byte(`<d:multistatus xmlns:d="DAV:">` + strings.Repeat("<d:x/>", n) + `</d:multistatus>`)
	}
	if _, err := parseXML(gen(MaxXMLElements - 1)); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if _, err := parseXML(gen(MaxXMLElements)); code(err) != api.CodeMalformedCalendar {
		t.Fatalf("over the element cap: %v", err)
	}
	deep := func(n int) []byte {
		return []byte(strings.Repeat("<a>", n) + strings.Repeat("</a>", n))
	}
	if _, err := parseXML(deep(MaxXMLDepth)); err != nil {
		t.Fatalf("at the depth cap: %v", err)
	}
	if _, err := parseXML(deep(MaxXMLDepth + 1)); code(err) != api.CodeMalformedCalendar {
		t.Fatalf("over the depth cap: %v", err)
	}
	for _, bad := range []string{
		"", "<a>", "<a></b>", "<a/><b/>", "<a/>text", "<a>&unknown;</a>",
		`<?xml version="1.0" encoding="ISO-8859-2"?><a/>`, "<a>\x01</a>",
	} {
		if _, err := parseXML([]byte(bad)); code(err) != api.CodeMalformedCalendar {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := parseXML([]byte("<!-- c --><a>&amp;&lt;</a>\n<!-- after -->\n")); err != nil {
		t.Errorf("comments and predefined entities: %v", err)
	}
}

func TestCalendarDataCap(t *testing.T) {
	big := strings.Repeat("x", api.MaxICSBytes+1)
	body := `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>a.ics</d:href><d:propstat><d:prop><d:getetag>"1"</d:getetag><c:calendar-data>` +
		big + `</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`
	ms, err := parseMultistatus([]byte(body), fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	if p := ms.Responses[0].Props; !p.DataTooLarge || p.CalendarData != nil {
		t.Fatalf("too-large data kept: %v %d", p.DataTooLarge, len(p.CalendarData))
	}
}

func TestPropertyCleaning(t *testing.T) {
	body := `<d:multistatus xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/"><d:response><d:href>/remote.php/dav/calendars/jana/personal/</d:href><d:propstat><d:prop>` +
		`<d:displayname>Ev&#x202E;il&#x200B; Name&#x2028;` + strings.Repeat("é", 300) + `</d:displayname>` +
		`<d:getetag>"bad` + "é" + `"</d:getetag><cs:getctag>  ctag-1 </cs:getctag><d:sync-token>` + strings.Repeat("t", 3000) + `</d:sync-token>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`
	ms, err := parseMultistatus([]byte(body), fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	p := ms.Responses[0].Props
	if strings.ContainsAny(p.DisplayName, "\u202e\u200b\u2028") || len(p.DisplayName) > maxNameBytes || !strings.HasPrefix(p.DisplayName, "Evil Name") {
		t.Errorf("display name %q", p.DisplayName)
	}
	if p.ETag != "" || p.CTag != "ctag-1" || p.SyncToken != "" {
		t.Errorf("tokens: %q %q %q", p.ETag, p.CTag, p.SyncToken)
	}
}

func TestNormaliseHref(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b%40c/":      "/a/b@c/",
		"/a/b@c/":        "/a/b@c/",
		"/a/b%20c.ics":   "/a/b%20c.ics",
		"/a/../b":        "",
		"https://x.y/a":  "",
		"relative/a.ics": "/relative/a.ics",
		"/a?x=1":         "/a",
		"/a/\u202eb.ics": "",
		"":               "",
	} {
		if got := NormaliseHref(in); got != want {
			t.Errorf("NormaliseHref(%q) = %q, want %q", in, got, want)
		}
	}
}
