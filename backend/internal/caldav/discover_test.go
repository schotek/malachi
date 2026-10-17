// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func discoverer(s *caldavtest.Server) *Discoverer {
	return &Discoverer{HTTP: s.HTTPClient(), Resolver: s.Resolver()}
}

func noCredentials(t *testing.T, s *caldavtest.Server) {
	t.Helper()
	for _, r := range s.Requests() {
		if r.HasAuthorization {
			t.Fatalf("discovery sent credentials: %s %s%s", r.Method, r.Host, r.Path)
		}
	}
}

func TestDiscoverProfiles(t *testing.T) {
	for _, tc := range []struct {
		profile caldavtest.Profile
		typed   string
		url     string
		via     api.CalDAVFoundVia
	}{
		{caldavtest.Nextcloud, "", "https://cloud.example.test/remote.php/dav", api.CalDAVFoundWellKnown},
		{caldavtest.Nextcloud, "https://cloud.example.test/remote.php/dav/", "https://cloud.example.test/remote.php/dav", api.CalDAVFoundURL},
		{caldavtest.ICloud, "", "https://caldav.icloud.test", api.CalDAVFoundWellKnown},
		{caldavtest.Fastmail, "", "https://caldav.fastmail.test/dav", api.CalDAVFoundSRV},
		{caldavtest.Radicale, "", "https://radicale.example.test", api.CalDAVFoundURL},
		// A hostile server's .well-known leads to another host: the
		// result names it (the user confirms), nothing was sent there
		// but anonymous requests.
		{caldavtest.Hostile, "", "https://evil.test/dav", api.CalDAVFoundWellKnown},
	} {
		t.Run(string(tc.profile)+tc.typed, func(t *testing.T) {
			s := caldavtest.New(t, tc.profile)
			typed := tc.typed
			if typed == "" {
				typed = s.TypedURL()
			}
			res, err := discoverer(s).Discover(t.Context(), typed)
			if err != nil {
				t.Fatal(err)
			}
			if res.ServerURL != tc.url || res.FoundVia != tc.via || !res.NeedsCredentials {
				t.Fatalf("result %+v", res)
			}
			if tc.profile != caldavtest.Hostile && res.ServerURL != s.ServerURL() {
				t.Fatalf("server URL %q, the fake is at %q", res.ServerURL, s.ServerURL())
			}
			noCredentials(t, s)
			if s.AuthLeaks() != 0 {
				t.Fatal("credentials leaked")
			}
		})
	}
}

func TestDiscoverOptionsBehindAuth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edit     func(*caldavtest.Server)
		discover bool
	}{
		// sabre/dav (Nextcloud): 401, WWW-Authenticate, no DAV header.
		{"401 without DAV", func(s *caldavtest.Server) { s.OptionsNeedAuth = true }, true},
		{"401 with DAV but no calendar-access", func(s *caldavtest.Server) {
			s.OptionsNeedAuth, s.DAVOnUnauthorized, s.DAV = true, true, "1, 3"
		}, false},
		{"2xx without calendar-access", func(s *caldavtest.Server) { s.DAV = "1, 2, 3" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, typed := range []string{"", "https://cloud.example.test/remote.php/dav/"} {
				s := caldavtest.New(t, caldavtest.Nextcloud)
				s.Set(tc.edit)
				if typed == "" {
					typed = s.TypedURL() // through .well-known
				}
				res, err := discoverer(s).Discover(t.Context(), typed)
				if !tc.discover {
					if code(err) != api.CodeServerError {
						t.Fatalf("%s: %+v %v", typed, res, err)
					}
					continue
				}
				if err != nil || res.ServerURL != s.ServerURL() || !res.NeedsCredentials {
					t.Fatalf("%s: %+v %v", typed, res, err)
				}
				noCredentials(t, s)
			}
		})
	}
	// The authenticated step verifies CalDAV for real.
	s := caldavtest.New(t, caldavtest.Nextcloud)
	s.Set(func(s *caldavtest.Server) { s.OptionsNeedAuth = true })
	info, err := newClient(t, s).ListCalendars(t.Context())
	if err != nil || len(info.Calendars) == 0 || info.Scheduling != api.CalDAVSchedulingServer {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestDiscoverFailures(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Fastmail)
	// Without the SRV record fastmail.test is just a web site.
	d := &Discoverer{HTTP: s.HTTPClient(), Resolver: &caldavtest.Resolver{}}
	if _, err := d.Discover(t.Context(), "fastmail.test"); code(err) != api.CodeServerError {
		t.Fatalf("not CalDAV: %v", err)
	}
	if _, err := d.Discover(t.Context(), "nowhere.test"); code(err) != api.CodeNetworkError {
		t.Fatalf("unknown host: %v", err)
	}
	for _, bad := range []string{"", "http://cloud.example.test", "https://a:b@cloud.example.test", "ftp://x.test", "https://x.test/?q"} {
		if _, err := d.Discover(t.Context(), bad); code(err) != api.CodeInvalidArgument {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// A well-known chain that never ends falls through to the URL.
	n := caldavtest.New(t, caldavtest.Nextcloud)
	n.FailNext(caldavtest.Failure{Path: WellKnownPath, Status: 302, Header: map[string][]string{"Location": {WellKnownPath}}, Persist: true})
	n.FailNext(caldavtest.Failure{Method: "OPTIONS", Path: "/", Status: 200, Header: map[string][]string{"DAV": {"1, 2"}}})
	if _, err := discoverer(n).Discover(t.Context(), n.TypedURL()); code(err) != api.CodeServerError {
		t.Fatalf("redirect loop: %v", err)
	}
	wk := 0
	for _, r := range n.Requests() {
		if r.Path == WellKnownPath {
			wk++
		}
	}
	if wk != MaxRedirects+1 {
		t.Fatalf("well-known asked %d times", wk)
	}
}

func TestDiscoverSRVTargetsChecked(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Fastmail)
	r := &caldavtest.Resolver{SRV: s.Resolver().SRV, TXT: map[string][]string{"_caldavs._tcp.fastmail.test": {"path=/../etc"}}}
	res, err := (&Discoverer{HTTP: s.HTTPClient(), Resolver: r}).Discover(t.Context(), "fastmail.test")
	// A bad TXT path is ignored (path /): OPTIONS on / of the target
	// still advertises calendar-access in the fake.
	if err != nil || res.FoundVia != api.CalDAVFoundSRV || res.ServerURL != "https://caldav.fastmail.test" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestListCalendarsProfiles(t *testing.T) {
	type cal struct {
		name, color string
		access      api.CalendarAccess
		def         bool
	}
	for _, tc := range []struct {
		profile    caldavtest.Profile
		addresses  []string
		scheduling api.CalDAVScheduling
		cals       []cal
	}{
		{caldavtest.Nextcloud, []string{"jana@example.test"}, api.CalDAVSchedulingServer, []cal{
			{"Personal", "#0082c9", api.CalendarAccessWrite, true},
			{"Team", "#e9322d", api.CalendarAccessRead, false},
		}},
		{caldavtest.ICloud, []string{"jana@icloud.test"}, api.CalDAVSchedulingServer, []cal{
			{"Home", "#ff2968", api.CalendarAccessWrite, true},
			{"Work", "#1badf8", api.CalendarAccessWrite, false},
		}},
		{caldavtest.Fastmail, []string{"jana@fastmail.test"}, api.CalDAVSchedulingServer, []cal{
			{"Calendar", "#3a429c", api.CalendarAccessWrite, true},
		}},
		{caldavtest.Radicale, []string{}, api.CalDAVSchedulingClient, []cal{
			{"Calendar", "", api.CalendarAccessWrite, false},
		}},
	} {
		t.Run(string(tc.profile), func(t *testing.T) {
			s := caldavtest.New(t, tc.profile)
			info, err := newClient(t, s).ListCalendars(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if info.PrincipalURL != s.PrincipalURL() || info.HomeSetURL != s.HomeSetURL() {
				t.Fatalf("principal %q home %q", info.PrincipalURL, info.HomeSetURL)
			}
			if !slices.Equal(info.Addresses, tc.addresses) || info.Scheduling != tc.scheduling {
				t.Fatalf("addresses %v scheduling %v", info.Addresses, info.Scheduling)
			}
			if info.DisplayName != "Jana Dvořáková" {
				t.Fatalf("display name %q", info.DisplayName)
			}
			var got []cal
			for _, c := range info.Calendars {
				got = append(got, cal{c.Name, c.Color, c.Access, c.IsDefault})
				if c.CTag == "" {
					t.Errorf("%s: no ctag", c.Name)
				}
				if c.SupportsSync != (tc.profile != caldavtest.Radicale) || (c.SyncToken != "") != c.SupportsSync {
					t.Errorf("%s: sync %v token %q", c.Name, c.SupportsSync, c.SyncToken)
				}
			}
			if !slices.Equal(got, tc.cals) {
				t.Fatalf("calendars %+v, want %+v", got, tc.cals)
			}
			res := info.Result()
			if len(res.Calendars) != len(tc.cals) || res.Calendars[0].Href != info.Calendars[0].Href || res.PrincipalURL != info.PrincipalURL {
				t.Fatalf("result %+v", res)
			}
			if s.AuthLeaks() != 0 {
				t.Fatal("credentials leaked")
			}
		})
	}
}

func TestListCalendarsLimit(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Radicale)
	for i := 0; i < api.MaxCalendarsPerAccount+5; i++ {
		s.AddCalendar("c"+string(rune('a'+i%26))+string(rune('a'+i/26)), caldavtest.CalendarOptions{Name: "C"})
	}
	info, err := newClient(t, s).ListCalendars(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Calendars) != api.MaxCalendarsPerAccount || info.Skipped != 6 {
		t.Fatalf("calendars %d skipped %d", len(info.Calendars), info.Skipped)
	}
}

func TestListCalendarsOtherOrigin(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	root := "/remote.php/dav/"
	s.FailNext(caldavtest.Failure{Method: "PROPFIND", Path: root, Status: 207, Header: map[string][]string{"Content-Type": {"application/xml"}},
		Body: []byte(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>/remote.php/dav/</d:href><d:propstat><d:prop><d:current-user-principal><d:href>https://evil.test/principals/jana/</d:href></d:current-user-principal></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)})
	_, err := newClient(t, s).ListCalendars(t.Context())
	if err != ErrOtherOrigin || s.EvilRequests() != 0 {
		t.Fatalf("principal on another host: %v, evil requests %d", err, s.EvilRequests())
	}
	// An origin the caller trusts (a home set the user confirmed) is used.
	s.FailNext(caldavtest.Failure{Method: "PROPFIND", Path: root, Status: 207, Header: map[string][]string{"Content-Type": {"application/xml"}},
		Body: []byte(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>/remote.php/dav/</d:href><d:propstat><d:prop><d:current-user-principal><d:href>https://evil.test/principals/jana/</d:href></d:current-user-principal></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)})
	_, err = newClient(t, s, func(o *Options) { o.Origins = []string{"https://evil.test"} }).ListCalendars(t.Context())
	if s.EvilRequests() == 0 || err == nil {
		t.Fatalf("trusted origin not asked: %v", err)
	}
}

func TestUnauthorizedAfterSuccess(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := newClient(t, s)
	if _, err := c.ListCalendars(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.Set(func(s *caldavtest.Server) { s.UnauthorizedAfter = 1 })
	_, err := c.State(t.Context(), s.CalendarHref("personal"))
	if !IsUnauthorized(err) || code(err) != api.CodeAuthFailed {
		t.Fatalf("401 after 200: %v", err)
	}
}
