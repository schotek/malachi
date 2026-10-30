// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// vcard is a small vCard 3.0.
func vcard(uid, name string) []byte {
	return []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nFN:" + name +
		"\r\nN:" + name + ";;;;\r\nEMAIL:" + strings.ToLower(uid) + "@example.test\r\nEND:VCARD\r\n")
}

// cardClient is a client of s's CardDAV server.
func cardClient(t *testing.T, s *caldavtest.Server, edit ...func(*Options)) *Client {
	t.Helper()
	return newClient(t, s, append([]func(*Options){func(o *Options) { o.ServerURL = s.CardServerURL() }}, edit...)...)
}

var cardProfiles = []caldavtest.Profile{caldavtest.Nextcloud, caldavtest.ICloud, caldavtest.Fastmail, caldavtest.Radicale, caldavtest.Hostile}

func TestParseDAVHeaderAddressBook(t *testing.T) {
	caps := parseDAVHeader(http.Header{"Dav": {"1, 3, addressbook", "extended-mkcol"}}, 200)
	if !caps.AddressBook || caps.CalendarAccess {
		t.Fatalf("%+v", caps)
	}
	caps = parseDAVHeader(http.Header{"Dav": {"1, 3, calendar-access, ADDRESSBOOK"}}, 200)
	if !caps.AddressBook || !caps.CalendarAccess {
		t.Fatalf("%+v", caps)
	}
	if caps := parseDAVHeader(http.Header{"Dav": {"1, addressbook-x"}}, 200); caps.AddressBook {
		t.Fatalf("prefix taken for the class: %+v", caps)
	}
	for _, p := range cardProfiles {
		s := caldavtest.New(t, p)
		caps, err := cardClient(t, s).Options(t.Context(), "")
		if err != nil || !caps.AddressBook {
			t.Fatalf("%s: %+v %v", p, caps, err)
		}
	}
	// iCloud's CalDAV host does not offer CardDAV, Nextcloud's does.
	s := caldavtest.New(t, caldavtest.ICloud)
	if caps, _ := newClient(t, s).Options(t.Context(), ""); caps.AddressBook || !caps.CalendarAccess {
		t.Fatalf("icloud caldav host: %+v", caps)
	}
}

func TestCardDAVProps(t *testing.T) {
	base, _ := url.Parse("https://dav.example.test/books/")
	big := strings.Repeat("x", MaxCardBytes+1)
	body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
		`<d:response><d:href>/books/a/</d:href><d:propstat><d:prop>` +
		`<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>` +
		`<card:addressbook-description>  Line one` + "\n‮" + `line two</card:addressbook-description>` +
		`<card:supported-address-data><card:address-data-type content-type="text/vcard" version="3.0"/>` +
		`<card:address-data-type content-type="text/vcard" version="4.0"/><card:address-data-type content-type="application/vcard+json" version="4.0"/>` +
		`<card:address-data-type content-type="text/vcard" version="4.0"/><card:address-data-type content-type="text/vcard" version="4.0; rm -rf"/></card:supported-address-data>` +
		`<card:addressbook-home-set><d:href>/home/</d:href><d:href>https://contacts.example.test/h/</d:href></card:addressbook-home-set>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>/books/a/1.vcf</d:href><d:propstat><d:prop><d:getetag>"1"</d:getetag>` +
		`<card:address-data>BEGIN:VCARD&#13;&#10;END:VCARD</card:address-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>/books/a/2.vcf</d:href><d:propstat><d:prop><d:getetag>"2"</d:getetag>` +
		`<card:address-data>` + big + `</card:address-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`</d:multistatus>`
	ms, err := parseMultistatus([]byte(body), base)
	if err != nil || len(ms.Responses) != 3 {
		t.Fatalf("%+v %v", ms, err)
	}
	p := ms.Responses[0].Props
	if !p.Is(TypeAddressBook) || !p.Is(TypeCollection) || p.Description != "Line one line two" {
		t.Fatalf("collection %+v", p)
	}
	if !p.AddressDataSet || !slices.Equal(p.AddressDataVersions, []string{"3.0", "4.0"}) {
		t.Fatalf("versions %v", p.AddressDataVersions)
	}
	if !slices.Equal(p.AddressbookHomeSet, []string{"https://dav.example.test/home/", "https://contacts.example.test/h/"}) {
		t.Fatalf("home set %v", p.AddressbookHomeSet)
	}
	if c := ms.Responses[1].Props; string(c.AddressData) != "BEGIN:VCARD\r\nEND:VCARD" || c.AddressDataTooLarge {
		t.Fatalf("card %q", c.AddressData)
	}
	if c := ms.Responses[2].Props; c.AddressData != nil || !c.AddressDataTooLarge {
		t.Fatalf("huge card kept: %d bytes", len(c.AddressData))
	}
	// The request bodies declare the CardDAV prefix.
	b := string(addressbookMultigetBody([]string{"/books/a/1.vcf", "/books/a/<x>.vcf"}))
	if !strings.Contains(b, `xmlns:card="urn:ietf:params:xml:ns:carddav"`) || !strings.Contains(b, "<card:address-data/>") ||
		!strings.Contains(b, "/books/a/&lt;x&gt;.vcf") || !strings.HasSuffix(b, "</card:addressbook-multiget>") {
		t.Fatalf("multiget body %s", b)
	}
	if b := string(propfindBody([]xml.Name{PropAddressbookHomeSet, PropSupportedAddressData})); !strings.Contains(b, "<card:addressbook-home-set/><card:supported-address-data/>") {
		t.Fatalf("propfind body %s", b)
	}
}

func TestDiscoverCardDAVProfiles(t *testing.T) {
	for _, tc := range []struct {
		profile caldavtest.Profile
		url     string
		via     api.CalDAVFoundVia
	}{
		{caldavtest.Nextcloud, "https://cloud.example.test/remote.php/dav", api.CalDAVFoundWellKnown},
		{caldavtest.ICloud, "https://contacts.icloud.test", api.CalDAVFoundWellKnown},
		{caldavtest.Fastmail, "https://carddav.fastmail.test/dav", api.CalDAVFoundSRV},
		{caldavtest.Radicale, "https://radicale.example.test", api.CalDAVFoundURL},
		// .well-known/carddav of a hostile server leads elsewhere: named,
		// nothing but anonymous requests sent.
		{caldavtest.Hostile, "https://evil.test/dav", api.CalDAVFoundWellKnown},
	} {
		t.Run(string(tc.profile), func(t *testing.T) {
			s := caldavtest.New(t, tc.profile)
			res, err := discoverer(s).DiscoverService(t.Context(), s.CardTypedURL(), ServiceCardDAV)
			if err != nil {
				t.Fatal(err)
			}
			if res.ServerURL != tc.url || res.FoundVia != tc.via || !res.NeedsCredentials {
				t.Fatalf("result %+v", res)
			}
			if tc.profile != caldavtest.Hostile && res.ServerURL != s.CardServerURL() {
				t.Fatalf("server URL %q, the fake is at %q", res.ServerURL, s.CardServerURL())
			}
			noCredentials(t, s)
			if s.AuthLeaks() != 0 {
				t.Fatal("credentials leaked")
			}
			// CalDAV discovery is unchanged by CardDAV.
			cal, err := discoverer(s).Discover(t.Context(), s.TypedURL())
			if err != nil || (tc.profile != caldavtest.Hostile && cal.ServerURL != s.ServerURL()) {
				t.Fatalf("caldav: %+v %v", cal, err)
			}
		})
	}
}

func TestDiscoverCardDAVOnlyServer(t *testing.T) {
	typed := "https://cloud.example.test/remote.php/dav/"
	// A server with addressbook but without calendar-access is CardDAV,
	// not CalDAV.
	s := caldavtest.New(t, caldavtest.Nextcloud)
	s.Set(func(s *caldavtest.Server) { s.DAV = "1, 2, 3, addressbook" })
	if res, err := discoverer(s).DiscoverService(t.Context(), typed, ServiceCardDAV); err != nil || res.ServerURL != s.CardServerURL() {
		t.Fatalf("carddav: %+v %v", res, err)
	}
	if _, err := discoverer(s).Discover(t.Context(), typed); code(err) != api.CodeServerError || !strings.Contains(ToAPIError(err).Message, "calendar-access") {
		t.Fatalf("caldav: %v", err)
	}
	// And the other way round.
	s.Set(func(s *caldavtest.Server) { s.DAV = "1, 2, 3, calendar-access" })
	if _, err := discoverer(s).DiscoverService(t.Context(), typed, ServiceCardDAV); code(err) != api.CodeServerError ||
		!strings.Contains(ToAPIError(err).Message, "addressbook") {
		t.Fatalf("carddav without addressbook: %v", err)
	}
	// sabre/dav: 401 without any DAV header is a candidate for both.
	s.Set(func(s *caldavtest.Server) { s.DAV, s.OptionsNeedAuth = "", true })
	if res, err := discoverer(s).DiscoverService(t.Context(), s.CardTypedURL(), ServiceCardDAV); err != nil || res.ServerURL != s.CardServerURL() ||
		res.AddressBooks || res.Calendars {
		t.Fatalf("401 without DAV: %+v %v", res, err)
	}
	// A 401 whose DAV header lacks addressbook is not CardDAV.
	s.Set(func(s *caldavtest.Server) { s.DAVOnUnauthorized, s.DAV = true, "1, 3, calendar-access" })
	if _, err := discoverer(s).DiscoverService(t.Context(), typed, ServiceCardDAV); code(err) != api.CodeServerError {
		t.Fatalf("401 with DAV without addressbook: %v", err)
	}
	noCredentials(t, s)
}

func TestDiscoverAll(t *testing.T) {
	// Nextcloud offers both at the same URL.
	s := caldavtest.New(t, caldavtest.Nextcloud)
	d, err := discoverer(s).DiscoverAll(t.Context(), s.TypedURL())
	if err != nil || d.CalDAV == nil || d.CardDAV == nil || d.CalDAV.ServerURL != d.CardDAV.ServerURL ||
		d.CalDAVErr != nil || d.CardDAVErr != nil {
		t.Fatalf("nextcloud: %+v %v", d, err)
	}
	if !d.CardDAV.AddressBooks || !d.CardDAV.Calendars {
		t.Fatalf("nextcloud classes: %+v", d.CardDAV)
	}
	// iCloud: two hosts of one provider.
	s = caldavtest.New(t, caldavtest.ICloud)
	d, err = discoverer(s).DiscoverAll(t.Context(), s.TypedURL())
	if err != nil || d.CalDAV == nil || d.CardDAV == nil || d.CalDAV.ServerURL != s.ServerURL() || d.CardDAV.ServerURL != s.CardServerURL() {
		t.Fatalf("icloud: %+v %v", d, err)
	}
	// The contacts host advertises addressbook only (on its 401).
	if !d.CardDAV.AddressBooks || d.CardDAV.Calendars || !d.CalDAV.Calendars || d.CalDAV.AddressBooks {
		t.Fatalf("icloud classes: %+v %+v", d.CalDAV, d.CardDAV)
	}
	// Fastmail through SRV records for both.
	s = caldavtest.New(t, caldavtest.Fastmail)
	d, err = discoverer(s).DiscoverAll(t.Context(), s.TypedURL())
	if err != nil || d.CalDAV == nil || d.CardDAV == nil || d.CardDAV.ServerURL != "https://carddav.fastmail.test/dav" {
		t.Fatalf("fastmail: %+v %v", d, err)
	}
	// A CalDAV-only server: CardDAV missing with its reason, no error.
	s = caldavtest.New(t, caldavtest.Nextcloud)
	s.Set(func(s *caldavtest.Server) { s.CardDAV = false })
	d, err = discoverer(s).DiscoverAll(t.Context(), s.TypedURL())
	if err != nil || d.CalDAV == nil || d.CardDAV != nil || code(d.CardDAVErr) != api.CodeServerError {
		t.Fatalf("caldav only: %+v %v", d, err)
	}
	// Neither: an error.
	s.Set(func(s *caldavtest.Server) { s.DAV = "1, 2, 3" })
	if _, err := discoverer(s).DiscoverAll(t.Context(), s.TypedURL()); code(err) != api.CodeServerError {
		t.Fatalf("neither: %v", err)
	}
	if _, err := discoverer(s).DiscoverAll(t.Context(), "https://user:pw@x.test"); code(err) != api.CodeInvalidArgument {
		t.Fatalf("bad url: %v", err)
	}
	noCredentials(t, s)
}

func TestHomeSets(t *testing.T) {
	for _, p := range cardProfiles {
		t.Run(string(p), func(t *testing.T) {
			s := caldavtest.New(t, p)
			hs, err := cardClient(t, s).HomeSets(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if hs.PrincipalURL != s.CardPrincipalURL() || hs.AddressBookHomeSet != s.AddressBookHomeURL() || hs.DisplayName != "Jana Dvořáková" {
				t.Fatalf("%+v", hs)
			}
			sameHost := s.CardHost() == s.Host()
			if sameHost != (hs.CalendarHomeSet == s.HomeSetURL()) || (!sameHost && hs.CalendarHomeSet != "") {
				t.Fatalf("calendar home %q", hs.CalendarHomeSet)
			}
			if s.AuthLeaks() != 0 || s.EvilRequests() != 0 {
				t.Fatal("leak")
			}
		})
	}
	// Without CardDAV the home set is missing, the calendar one is there.
	s := caldavtest.New(t, caldavtest.Nextcloud)
	s.Set(func(s *caldavtest.Server) { s.CardDAV = false })
	hs, err := newClient(t, s).HomeSets(t.Context())
	if err != nil || hs.AddressBookHomeSet != "" || hs.CalendarHomeSet != s.HomeSetURL() {
		t.Fatalf("caldav only: %+v %v", hs, err)
	}
}

func TestAddressBookOnAnotherHost(t *testing.T) {
	// iCloud's CalDAV principal names the address book home set on the
	// contacts host: reported, not used without the caller's trust.
	s := caldavtest.New(t, caldavtest.ICloud)
	c := newClient(t, s)
	hs, err := c.HomeSets(t.Context())
	if err != nil || hs.CalendarHomeSet != s.HomeSetURL() || hs.AddressBookHomeSet != s.AddressBookHomeURL() {
		t.Fatalf("%+v %v", hs, err)
	}
	if c.Trusts(hs.AddressBookHomeSet) || !c.Trusts(hs.CalendarHomeSet) || !c.Trusts("/x/") {
		t.Fatal("trust")
	}
	s.ResetRequests()
	if _, err := c.ListAddressBooks(t.Context(), hs.AddressBookHomeSet); !errors.Is(err, ErrOtherOrigin) || len(s.Requests()) != 0 {
		t.Fatalf("untrusted home: %v, %d requests", err, len(s.Requests()))
	}
	// Trusted (core's calDAVOrigin: same registrable domain), it works and
	// the password goes only to the two hosts.
	c = newClient(t, s, func(o *Options) { o.Origins = []string{"https://" + caldavtest.ICloudContactsHost} })
	books, err := c.ListAddressBooks(t.Context(), hs.AddressBookHomeSet)
	if err != nil || len(books) != 1 || books[0].Href != s.AddressBookHref("card") {
		t.Fatalf("%+v %v", books, err)
	}
	href, _, err := c.CreateCard(t.Context(), "https://"+caldavtest.ICloudContactsHost+books[0].Href, vcard("A1", "Petr"))
	if err != nil || !strings.HasPrefix(href, books[0].Href) {
		t.Fatalf("create on the contacts host: %q %v", href, err)
	}
	for _, r := range s.Requests() {
		if !r.Authorized || (r.Host != caldavtest.ICloudContactsHost && r.Host != caldavtest.ICloudHost) {
			t.Fatalf("request %s %s%s", r.Method, r.Host, r.Path)
		}
	}
	if s.AuthLeaks() != 0 {
		t.Fatal("leak")
	}
	// A hostile principal naming another server's home set.
	h := caldavtest.New(t, caldavtest.Hostile)
	principal := "/dav/principals/users/jana/"
	h.FailNext(caldavtest.Failure{Method: "PROPFIND", Path: principal, Status: 207, Header: http.Header{"Content-Type": {"application/xml"}},
		Body: []byte(`<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:response><d:href>` + principal +
			`</d:href><d:propstat><d:prop><card:addressbook-home-set><d:href>https://evil.test/books/</d:href></card:addressbook-home-set></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)})
	hc := cardClient(t, h)
	hs, err = hc.HomeSets(t.Context())
	if err != nil || hs.AddressBookHomeSet != "https://evil.test/books/" {
		t.Fatalf("%+v %v", hs, err)
	}
	if _, err := hc.ListAddressBooks(t.Context(), hs.AddressBookHomeSet); !errors.Is(err, ErrOtherOrigin) || h.EvilRequests() != 0 {
		t.Fatalf("evil home: %v, %d", err, h.EvilRequests())
	}
}

func TestListAddressBooksProfiles(t *testing.T) {
	type book struct {
		name, desc string
		readOnly   bool
		versions   string
	}
	for _, tc := range []struct {
		profile caldavtest.Profile
		books   []book
	}{
		{caldavtest.Nextcloud, []book{{"Contacts", "Kontakty Jany", false, "3.0 4.0"}, {"Team contacts", "", true, "3.0"}}},
		{caldavtest.ICloud, []book{{"card", "", false, "3.0 4.0"}}},
		{caldavtest.Fastmail, []book{{"Personal", "", false, "3.0 4.0"}}},
		// Radicale names no supported-address-data: 3.0 (RFC 6352).
		{caldavtest.Radicale, []book{{"Contacts", "", false, "3.0"}}},
	} {
		t.Run(string(tc.profile), func(t *testing.T) {
			s := caldavtest.New(t, tc.profile)
			c := cardClient(t, s)
			hs, err := c.HomeSets(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			books, err := c.ListAddressBooks(t.Context(), hs.AddressBookHomeSet)
			if err != nil {
				t.Fatal(err)
			}
			var got []book
			for _, b := range books {
				got = append(got, book{b.Name, b.Description, b.ReadOnly, strings.Join(b.SupportedVersions, " ")})
				if b.CTag == "" || b.SupportsSync != (tc.profile != caldavtest.Radicale) || (b.SyncToken != "") != b.SupportsSync {
					t.Errorf("%s: ctag %q sync %v %q", b.Name, b.CTag, b.SupportsSync, b.SyncToken)
				}
			}
			if !slices.Equal(got, tc.books) {
				t.Fatalf("books %+v, want %+v", got, tc.books)
			}
			// A path works as well as the absolute URL.
			if again, err := c.ListAddressBooks(t.Context(), strings.TrimPrefix(hs.AddressBookHomeSet, "https://"+s.CardHost())); err != nil || len(again) != len(books) {
				t.Fatalf("by path: %d %v", len(again), err)
			}
			// The calendars of Radicale's shared home are not address books,
			// and ListCalendars ignores the address book.
			if tc.profile == caldavtest.Radicale {
				info, err := c.ListCalendars(t.Context())
				if err != nil || len(info.Calendars) != 1 || info.Skipped != 0 {
					t.Fatalf("calendars %+v %v", info, err)
				}
			}
		})
	}
}

func TestListAddressBooksLimit(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Radicale)
	for i := 0; i < MaxAddressBooks+5; i++ {
		s.AddAddressBook(fmt.Sprintf("b%03d", i), caldavtest.AddressBookOptions{Name: "B"})
	}
	books, err := cardClient(t, s).ListAddressBooks(t.Context(), s.AddressBookHomeURL())
	if err != nil || len(books) != MaxAddressBooks {
		t.Fatalf("%d %v", len(books), err)
	}
}

func TestCardsSyncAndMultiget(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := cardClient(t, s)
	book := s.AddressBookHref("contacts")
	var hrefs []string
	for i := 0; i < 250; i++ {
		h, _ := s.PutCard(book, fmt.Sprintf("c%03d.vcf", i), vcard(fmt.Sprintf("U%03d", i), "Name"))
		hrefs = append(hrefs, h)
	}
	st, err := c.State(t.Context(), book)
	if err != nil || !st.SupportsSync || st.SyncToken == "" || st.CTag == "" {
		t.Fatalf("state %+v %v", st, err)
	}
	res, err := c.SyncCollection(t.Context(), book, "")
	if err != nil || len(res.Changed) != 250 || len(res.Removed) != 0 || res.Token == "" {
		t.Fatalf("initial sync %d %v", len(res.Changed), err)
	}
	list, err := c.ListETags(t.Context(), book)
	if err != nil || len(list) != 250 || list[0].ETag == "" {
		t.Fatalf("list %d %v", len(list), err)
	}

	s.ResetRequests()
	cards, err := c.MultigetCards(t.Context(), book, append(slices.Clone(hrefs), book+"missing.vcf"))
	if err != nil || len(cards) != 251 {
		t.Fatalf("multiget %d %v", len(cards), err)
	}
	var sizes []int
	for _, r := range s.Requests() {
		if r.Method != "REPORT" || !strings.Contains(string(r.Body), "addressbook-multiget") {
			t.Fatalf("request %s %s", r.Method, r.Body)
		}
		sizes = append(sizes, r.MultigetHrefs)
	}
	if !slices.Equal(sizes, []int{100, 100, 51}) {
		t.Fatalf("batches %v", sizes)
	}
	for i, o := range cards[:250] {
		data, etag, _ := s.Card(hrefs[i])
		if o.Href != hrefs[i] || o.Status != 200 || o.ETag != etag || string(o.Data) != string(data) {
			t.Fatalf("card %d %+v", i, o)
		}
	}
	if cards[250].Status != http.StatusNotFound {
		t.Fatalf("missing %+v", cards[250])
	}

	// Changes and removals by another client.
	s.RemoveCard(hrefs[0])
	s.PutCard(book, "c001.vcf", vcard("U001", "Renamed"))
	inc, err := c.SyncCollection(t.Context(), book, res.Token)
	if err != nil || !slices.Equal(inc.Removed, []string{hrefs[0]}) || len(inc.Changed) != 1 || inc.Changed[0].Href != hrefs[1] {
		t.Fatalf("incremental %+v %v", inc, err)
	}
	s.ExpireSyncTokens(book)
	if _, err := c.SyncCollection(t.Context(), book, inc.Token); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("expired token: %v", err)
	}
	// A calendar report on an address book is refused by the server.
	if _, err := c.Multiget(t.Context(), book, hrefs[1:2]); err != nil {
		// Refused (403) → GET per object; a vCard is not too large.
		t.Fatalf("calendar multiget on a book: %v", err)
	}

	// A server without the report: one GET per card.
	s.Set(func(s *caldavtest.Server) { s.Multiget = false })
	s.ResetRequests()
	cards, err = c.MultigetCards(t.Context(), book, hrefs[1:4])
	if err != nil || len(cards) != 3 || !strings.Contains(string(cards[0].Data), "FN:Renamed") {
		t.Fatalf("by GET %+v %v", cards, err)
	}
	if n := len(s.Requests()); n != 4 {
		t.Fatalf("%d requests", n)
	}
}

func TestRadicaleCardsWithoutSyncCollection(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Radicale)
	c := cardClient(t, s)
	book := s.AddressBookHref("contacts")
	s.PutCard(book, "a.vcf", vcard("A", "A"))
	if _, err := c.SyncCollection(t.Context(), book, ""); err == nil || errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("sync-collection: %v", err)
	}
	list, err := c.ListETags(t.Context(), book)
	if err != nil || len(list) != 1 || list[0].ETag == "" {
		t.Fatalf("%+v %v", list, err)
	}
}

func TestCardWrites(t *testing.T) {
	for _, p := range []caldavtest.Profile{caldavtest.Nextcloud, caldavtest.ICloud, caldavtest.Fastmail, caldavtest.Radicale} {
		t.Run(string(p), func(t *testing.T) {
			s := caldavtest.New(t, p)
			c := cardClient(t, s)
			book := s.AddressBookHref(map[caldavtest.Profile]string{
				caldavtest.Nextcloud: "contacts", caldavtest.ICloud: "card", caldavtest.Fastmail: "Default", caldavtest.Radicale: "contacts",
			}[p])
			s.ResetRequests()
			href, etag, err := c.CreateCard(t.Context(), book, vcard("NEW", "Nový Kontakt"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(href, book) || !strings.HasSuffix(href, ".vcf") {
				t.Fatalf("href %q", href)
			}
			if _, server, _ := s.Card(href); etag == "" || etag != server {
				t.Fatalf("etag %q server %q", etag, server)
			}
			reqs := s.Requests()
			if reqs[0].Method != http.MethodPut || reqs[0].Header.Get("Content-Type") != CardContentType ||
				reqs[0].Header.Get("If-None-Match") != "*" || reqs[0].Header.Get("If-Match") != "" {
				t.Fatalf("create %s %v", reqs[0].Method, reqs[0].Header)
			}
			if p == caldavtest.ICloud && (len(reqs) != 2 || reqs[1].Method != http.MethodGet || reqs[1].Header.Get("Accept") != "text/vcard") {
				t.Fatalf("no ETag on PUT must be followed by a GET: %d", len(reqs))
			}
			if _, err := c.PutCard(t.Context(), href, vcard("NEW", "X"), ""); !IsConflict(err) || code(err) != api.CodeConflict {
				t.Fatalf("second create: %v", err)
			}
			etag2, err := c.PutCard(t.Context(), href, vcard("NEW", "Změněný"), etag)
			if err != nil || etag2 == etag {
				t.Fatalf("update %q %v", etag2, err)
			}
			if _, err := c.PutCard(t.Context(), href, vcard("NEW", "Stale"), etag); !IsConflict(err) {
				t.Fatalf("stale update: %v", err)
			}
			if err := c.Delete(t.Context(), href, etag); !IsConflict(err) {
				t.Fatalf("stale delete: %v", err)
			}
			o, err := c.GetCard(t.Context(), href)
			if err != nil || o.ETag != etag2 || !strings.Contains(string(o.Data), "FN:Změněný") {
				t.Fatalf("get %+v %v", o, err)
			}
			// The calendar Put sends text/calendar, which a book refuses.
			if _, err := c.Put(t.Context(), href, vcard("NEW", "Y"), etag2); Status(err) != http.StatusUnsupportedMediaType {
				t.Fatalf("text/calendar to a book: %v", err)
			}
			if err := c.Delete(t.Context(), href, etag2); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(t.Context(), href, ""); !IsGone(err) {
				t.Fatalf("delete again: %v", err)
			}
			if _, err := c.GetCard(t.Context(), href); !IsGone(err) {
				t.Fatalf("get deleted: %v", err)
			}
		})
	}
}

func TestCardWritesRefused(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := cardClient(t, s)
	shared := s.AddressBookHref("team-shared_by_petr")
	if _, _, err := c.CreateCard(t.Context(), shared, vcard("A", "A")); !IsForbidden(err) || code(err) != api.CodeReadOnly {
		t.Fatalf("read-only book: %v", err)
	}
	book := s.AddressBookHref("contacts")
	for _, data := range [][]byte{nil, make([]byte, MaxCardBytes+1)} {
		if _, err := c.PutCard(t.Context(), book+"x.vcf", data, ""); code(err) != api.CodeInvalidArgument {
			t.Fatalf("%d bytes: %v", len(data), err)
		}
	}
	if _, err := c.PutCard(t.Context(), book+"x.vcf", vcard("A", "A"), "\"bad\r\netag\""); code(err) != api.CodeInvalidArgument {
		t.Fatalf("bad etag: %v", err)
	}
	if _, err := c.PutCard(t.Context(), book+"x.vcf", []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"), ""); Status(err) != http.StatusBadRequest {
		t.Fatalf("not a vCard: %v", err)
	}
	if _, _, err := c.CreateCard(t.Context(), "https://evil.test/books/", vcard("A", "A")); !errors.Is(err, ErrOtherOrigin) || s.EvilRequests() != 0 {
		t.Fatalf("create elsewhere: %v", err)
	}
	if h := CardHref("/b", "a/b c"); h != "/b/a%2Fb%20c.vcf" {
		t.Fatalf("href %q", h)
	}
	// One conflict on a fresh UID is tried again with another.
	s.FailNext(caldavtest.Failure{Method: http.MethodPut, Status: http.StatusPreconditionFailed})
	href, _, err := c.CreateCard(t.Context(), book, vcard("B", "B"))
	if err != nil || len(s.Cards(book)) != 1 || s.Cards(book)[0] != href {
		t.Fatalf("retry: %q %v", href, err)
	}
	s.FailNext(caldavtest.Failure{Method: http.MethodPut, Status: http.StatusPreconditionFailed, Persist: true})
	if _, _, err := c.CreateCard(t.Context(), book, vcard("C", "C")); !IsConflict(err) {
		t.Fatalf("two conflicts: %v", err)
	}
}

func TestHostileCardDAV(t *testing.T) {
	type call func(c *Client, s *caldavtest.Server) error
	book := func(s *caldavtest.Server) string { return s.AddressBookHref("contacts") }
	calls := map[string]struct {
		method string
		f      call
	}{
		"list": {"PROPFIND", func(c *Client, s *caldavtest.Server) error {
			_, err := c.ListAddressBooks(context.Background(), s.AddressBookHomeURL())
			return err
		}},
		"homesets": {"PROPFIND", func(c *Client, _ *caldavtest.Server) error {
			_, err := c.HomeSets(context.Background())
			return err
		}},
		"multiget": {"REPORT", func(c *Client, s *caldavtest.Server) error {
			_, err := c.MultigetCards(context.Background(), book(s), []string{book(s) + "a.vcf"})
			return err
		}},
		"sync": {"REPORT", func(c *Client, s *caldavtest.Server) error {
			_, err := c.SyncCollection(context.Background(), book(s), "")
			return err
		}},
	}
	for _, tc := range []struct {
		name string
		f    caldavtest.Failure
		want api.ErrorCode
	}{
		{"over 32 MiB", caldavtest.HugeMultistatus(MaxMultistatusBytes + 1), api.CodeServerError},
		{"deep", caldavtest.DeepMultistatus(100), api.CodeMalformedCalendar},
		{"garbage", caldavtest.Garbage207(), api.CodeMalformedCalendar},
		{"web page", caldavtest.WrongContentType(), api.CodeServerError},
		{"entities", caldavtest.FixtureAnswer(t, "doctype-entities.xml", 0), api.CodeMalformedCalendar},
		{"redirect to another host", caldavtest.RedirectTo("https://evil.test/steal", http.StatusTemporaryRedirect), api.CodeServerError},
		{"redirect to http", caldavtest.RedirectTo("http://"+caldavtest.HostileHost+"/dav/", 0), api.CodeServerError},
	} {
		for name, cl := range calls {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				s := caldavtest.New(t, caldavtest.Hostile)
				f := tc.f
				f.Method = cl.method
				s.FailNext(f)
				err := cl.f(cardClient(t, s), s)
				if code(err) != tc.want {
					t.Fatalf("%v, want %v", err, tc.want)
				}
				if s.AuthLeaks() != 0 || s.EvilRequests() != 0 {
					t.Fatalf("leaks %d evil %d", s.AuthLeaks(), s.EvilRequests())
				}
			})
		}
	}
}

func TestHostileCardAnswers(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	c := cardClient(t, s)
	b := s.AddressBookHref("contacts")
	ms := func(inner string) caldavtest.Failure {
		return caldavtest.Failure{Method: "REPORT", Status: 207, Header: http.Header{"Content-Type": {"application/xml"}},
			Body: []byte(`<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` + inner + `</d:multistatus>`)}
	}
	// A card over MaxCardBytes inside the multistatus is TooLarge, not a
	// failure; a card answered for another origin's href is dropped and
	// counts as missing; an href not asked for is ignored.
	s.FailNext(ms(`<d:response><d:href>` + b + `a.vcf</d:href><d:propstat><d:prop><d:getetag>"1"</d:getetag><card:address-data>` +
		strings.Repeat("A", MaxCardBytes+10) + `</card:address-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>https://evil.test` + b + `b.vcf</d:href><d:propstat><d:prop><d:getetag>"2"</d:getetag><card:address-data>BEGIN:VCARD</card:address-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>` + b + `other.vcf</d:href><d:propstat><d:prop><d:getetag>"3"</d:getetag><card:address-data>BEGIN:VCARD</card:address-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
	cards, err := c.MultigetCards(t.Context(), b, []string{b + "a.vcf", b + "b.vcf"})
	if err != nil || len(cards) != 2 || !cards[0].TooLarge || cards[0].Data != nil || cards[1].Status != http.StatusNotFound {
		t.Fatalf("%+v %v", cards, err)
	}
	// A GET of a huge card.
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/vcard"}}, Body: []byte("x"), Size: MaxCardBytes + 64<<10 + 1})
	if _, err := c.GetCard(t.Context(), b+"a.vcf"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge GET: %v", err)
	}
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/vcard"}}, Body: []byte("x"), Size: MaxCardBytes + 1})
	if _, err := c.GetCard(t.Context(), b+"a.vcf"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("GET over MaxCardBytes: %v", err)
	}
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("<html>")})
	if _, err := c.GetCard(t.Context(), b+"a.vcf"); code(err) != api.CodeServerError {
		t.Fatalf("web page: %v", err)
	}
	// Multiget by GET turns a huge card into TooLarge.
	s.FailNext(caldavtest.Failure{Method: "REPORT", Status: 501})
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Body: []byte("x"), Size: MaxCardBytes + 10})
	cards, err = c.MultigetCards(t.Context(), b, []string{b + "a.vcf"})
	if err != nil || len(cards) != 1 || !cards[0].TooLarge {
		t.Fatalf("by GET %+v %v", cards, err)
	}
	// A PUT is never redirected.
	s.FailNext(caldavtest.RedirectTo("https://evil.test/x.vcf", http.StatusTemporaryRedirect))
	if _, err := c.PutCard(t.Context(), b+"x.vcf", vcard("X", "X"), ""); err == nil || s.EvilRequests() != 0 {
		t.Fatalf("redirected PUT: %v", err)
	}
	// A slow server is bounded by the context.
	s.Set(func(s *caldavtest.Server) { s.Delay = time.Second })
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.ListAddressBooks(ctx, s.AddressBookHomeURL()); code(err) != api.CodeServerTimeout && code(err) != api.CodeCancelled {
		t.Fatalf("slow: %v", err)
	}
	if s.AuthLeaks() != 0 {
		t.Fatal("leak")
	}
}

func TestHostileCardDiscovery(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	// .well-known/carddav redirecting to a web page host and to http:
	// skipped, the URL itself answers.
	s.FailNext(caldavtest.Failure{Path: WellKnownCardDAVPath, Status: 301, Header: http.Header{"Location": {"http://evil.test/dav/"}}})
	res, err := discoverer(s).DiscoverService(t.Context(), s.CardTypedURL(), ServiceCardDAV)
	if err != nil || res.ServerURL != "https://hostile.test" || res.FoundVia != api.CalDAVFoundURL {
		t.Fatalf("%+v %v", res, err)
	}
	// An SRV record naming a bad target is ignored.
	r := &caldavtest.Resolver{
		SRV: map[string][]*net.SRV{"_carddavs._tcp.hostile.test": {{Target: "bad host.", Port: 443}}},
		TXT: map[string][]string{"_carddavs._tcp.hostile.test": {"path=/../etc/"}},
	}
	s.FailNext(caldavtest.Failure{Path: WellKnownCardDAVPath, Status: 404})
	d := &Discoverer{HTTP: s.HTTPClient(), Resolver: r}
	if res, err := d.DiscoverService(t.Context(), s.CardTypedURL(), ServiceCardDAV); err != nil || res.FoundVia != api.CalDAVFoundURL {
		t.Fatalf("bad srv: %+v %v", res, err)
	}
	noCredentials(t, s)
}
