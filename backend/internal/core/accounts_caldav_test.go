// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/goa"
	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testPin = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

func calDAVConfig(server string) api.AccountConfig {
	return api.AccountConfig{
		Name: "Calendar", Email: "jana@example.test", Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: server, Username: caldavtest.Username},
	}
}

// newCalDAVBackend is a backend whose CalDAV requests go to srv, with an
// in-memory keyring and recording supervisors.
func newCalDAVBackend(t *testing.T, srv *caldavtest.Server) (*Backend, *fakeSupervisor, *fakeOutbox, *memKeyring) {
	t.Helper()
	b := newTestBackend(t, config.Default())
	sup, out := newFakeSupervisor(), newFakeOutbox()
	b.Supervisor, b.Delivery = sup, out
	kr := newMemKeyring()
	b.Keyring = kr
	if srv != nil {
		b.CalDAVHTTP = srv.HTTPClient()
		b.CalDAVResolver = srv.Resolver()
	}
	return b, sup, out, kr
}

func TestCalDAVAccountValidation(t *testing.T) {
	ok := func() api.AccountConfig { return calDAVConfig("https://cloud.example.test/remote.php/dav/") }
	cases := []struct {
		name string
		edit func(c *api.AccountConfig)
	}{
		{"no caldav block", func(c *api.AccountConfig) { c.CalDAV = nil }},
		{"imap settings", func(c *api.AccountConfig) { c.IMAP = validConfig().IMAP }},
		{"graph settings", func(c *api.AccountConfig) { c.Graph = &api.GraphConfig{Source: api.GraphSourceGOA} }},
		{"http to a remote host", func(c *api.AccountConfig) { c.CalDAV.ServerURL = "http://cloud.example.test/dav" }},
		{"user info", func(c *api.AccountConfig) { c.CalDAV.ServerURL = "https://jana:pw@cloud.example.test/dav" }},
		{"query", func(c *api.AccountConfig) { c.CalDAV.ServerURL = "https://cloud.example.test/dav?x=1" }},
		{"fragment", func(c *api.AccountConfig) { c.CalDAV.ServerURL = "https://cloud.example.test/dav#x" }},
		{"too long", func(c *api.AccountConfig) {
			c.CalDAV.ServerURL = "https://cloud.example.test/" + strings.Repeat("a", 2100)
		}},
		{"no username", func(c *api.AccountConfig) { c.CalDAV.Username = " " }},
		{"control in username", func(c *api.AccountConfig) { c.CalDAV.Username = "ja\x01na" }},
		{"bad pin", func(c *api.AccountConfig) { c.CalDAV.CertificateSHA256 = "abc" }},
		{"pin over http", func(c *api.AccountConfig) {
			c.CalDAV.ServerURL = "http://127.0.0.1:8080/dav"
			c.CalDAV.CertificateSHA256 = testPin
		}},
		{"principal on another site", func(c *api.AccountConfig) { c.CalDAV.PrincipalURL = "https://evil.test/p/" }},
		{"home set over http", func(c *api.AccountConfig) { c.CalDAV.HomeSetURL = "http://cloud.example.test/h/" }},
		{"scheduling", func(c *api.AccountConfig) { c.CalDAV.Scheduling = "sometimes" }},
		{"keepDays", func(c *api.AccountConfig) { c.Calendar = &api.CalendarConfig{KeepDays: 31} }},
		{"afterAnswer", func(c *api.AccountConfig) { c.Calendar = &api.CalendarConfig{AfterAnswer: "burn"} }},
		{"too many disabled calendars", func(c *api.AccountConfig) {
			c.Calendar = &api.CalendarConfig{DisabledCalendars: make([]string, 201)}
			for i := range c.Calendar.DisabledCalendars {
				c.Calendar.DisabledCalendars[i] = "/c/" + strconv.Itoa(i) + "/"
			}
		}},
		{"empty disabled calendar", func(c *api.AccountConfig) { c.Calendar = &api.CalendarConfig{DisabledCalendars: []string{""}} }},
		{"long disabled calendar", func(c *api.AccountConfig) {
			c.Calendar = &api.CalendarConfig{DisabledCalendars: []string{strings.Repeat("x", 2049)}}
		}},
	}
	for _, tc := range cases {
		c := ok()
		tc.edit(&c)
		if err := validateAccountConfig(&c); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", tc.name, err)
		}
	}

	// calendar only on graph and caldav; caldav settings only on caldav.
	im := validConfig()
	im.Calendar = &api.CalendarConfig{Sync: true}
	if err := validateAccountConfig(&im); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("imap with calendar: %v", err)
	}
	im = validConfig()
	im.CalDAV = &api.CalDAVConfig{ServerURL: "https://x.test", Username: "u"}
	if err := validateAccountConfig(&im); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("imap with caldav: %v", err)
	}
	g := graphConfig()
	g.Calendar = &api.CalendarConfig{Sync: true, KeepDays: 90, DisabledCalendars: []string{"AAMk"}}
	if err := validateAccountConfig(&g); err != nil {
		t.Errorf("graph with calendar: %v", err)
	}

	// Normalised: trailing slash, lower-case host, the pin, sync forced on.
	c := ok()
	c.CalDAV.ServerURL = "HTTPS://Cloud.Example.TEST/remote.php/dav/"
	c.CalDAV.CertificateSHA256 = strings.ToUpper(testPin)
	c.CalDAV.HomeSetURL = "https://p01.example.test/h/"
	c.Calendar = &api.CalendarConfig{Sync: false, KeepDays: 730}
	if err := validateAccountConfig(&c); err != nil {
		t.Fatal(err)
	}
	if c.CalDAV.ServerURL != "https://cloud.example.test/remote.php/dav" || c.CalDAV.CertificateSHA256 != testPin || !c.Calendar.Sync {
		t.Errorf("normalised: %+v %+v", c.CalDAV, c.Calendar)
	}
	c = ok()
	c.Calendar = nil
	if err := validateAccountConfig(&c); err != nil || c.Calendar == nil || !c.Calendar.Sync {
		t.Errorf("caldav without calendar block: %v %+v", err, c.Calendar)
	}
	// http only for loopback.
	c = ok()
	c.CalDAV.ServerURL = "http://127.0.0.1:5232"
	if err := validateAccountConfig(&c); err != nil {
		t.Errorf("loopback http: %v", err)
	}
}

func TestCalDAVOrigin(t *testing.T) {
	cases := []struct {
		server, raw, want string
		ok                bool
	}{
		{"https://caldav.icloud.com", "https://caldav.icloud.com/1/principal/", "https://caldav.icloud.com", true},
		{"https://caldav.icloud.com", "https://p42-caldav.icloud.com/1/calendars/", "https://p42-caldav.icloud.com", true},
		{"https://caldav.icloud.com", "https://evil.com/1/", "", false},
		{"https://caldav.icloud.com", "http://p42-caldav.icloud.com/1/", "", false},
		{"https://caldav.icloud.com", "https://u:p@p42-caldav.icloud.com/", "", false},
		{"https://cloud.example.org", "https://other.example.org:8443/dav/", "https://other.example.org:8443", true},
		{"https://a.github.io", "https://b.github.io/x/", "", false}, // public suffix: different sites
		{"https://10.0.0.1", "https://10.0.0.2/x/", "", false},
		{"http://127.0.0.1:5232", "http://127.0.0.1:5232/jana/", "http://127.0.0.1:5232", true},
		{"http://127.0.0.1:5232", "http://127.0.0.1:5233/jana/", "", false},
		{"https://cloud.example.org", "/relative/", "", false},
	}
	for _, c := range cases {
		got, ok := calDAVOrigin(c.server, c.raw)
		if got != c.want || ok != c.ok {
			t.Errorf("calDAVOrigin(%q, %q) = %q, %v", c.server, c.raw, got, ok)
		}
	}
}

// The pin replaces the verification only for the typed URL's host.
func TestCalDAVTransportPinsOnlyItsHost(t *testing.T) {
	tr := newCalDAVTransport("cloud.example.test", testPin)
	for raw, want := range map[string]string{
		"https://cloud.example.test/dav":     testPin,
		"https://CLOUD.example.test:443/dav": testPin,
		"https://cloud.example.test:8443/":   "",
		"https://p01.example.test/dav":       "",
		"http://cloud.example.test/dav":      "",
	} {
		u, _ := url.Parse(raw)
		if got := tr.pinFor(u); got != want {
			t.Errorf("pinFor(%s) = %q", raw, got)
		}
		cfg := tr.transportFor(u).TLSClientConfig
		if pinned := cfg.InsecureSkipVerify && cfg.VerifyConnection != nil; pinned != (want != "") {
			t.Errorf("%s: pinned transport %v", raw, pinned)
		}
		if cfg.ServerName != strings.ToLower(u.Hostname()) {
			t.Errorf("%s: server name %q", raw, cfg.ServerName)
		}
	}
	if newCalDAVTransport("cloud.example.test", "").pinFor(&url.URL{Scheme: "https", Host: "cloud.example.test"}) != "" {
		t.Error("no pin, yet pinned")
	}
}

func TestCalDAVAccountAddUpdateRemove(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, sup, _, kr := newCalDAVBackend(t, srv)
	acc := b.Accounts()

	// The password is required on add.
	cfg := calDAVConfig(srv.TypedURL() + "/remote.php/dav/")
	cfg.Email = ""
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: cfg}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("add without password: %v", err)
	}
	// Without email and home set, a listing fills them.
	res, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	id := string(res.AccountID)
	a, err := b.store.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cd := a.Config.CalDAV
	if a.Config.Email != "jana@example.test" || cd.ServerURL != srv.ServerURL() ||
		cd.HomeSetURL != srv.HomeSetURL() || cd.PrincipalURL != srv.PrincipalURL() || !a.Config.Calendar.Sync {
		t.Fatalf("stored: %+v %+v", a.Config, cd)
	}
	if kr.values[id+"/"+auth.KeyPassword] != caldavtest.Password {
		t.Fatal("password not in the keyring")
	}
	if a.Realm != "cloud.example.test/remote.php/dav" {
		t.Errorf("realm %q", a.Realm)
	}
	if sup.count("start:"+id) != 1 {
		t.Errorf("supervisor calls %v", sup.recorded())
	}
	list, err := acc.List(ctx, api.AccountListParams{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if bytes.Contains(raw, []byte(caldavtest.Password)) {
		t.Fatal("account.list returns the password")
	}
	if got := list.Accounts[0].Capabilities; !slices.Equal(got, []api.AccountCapability{api.CapabilityCalendar}) {
		t.Errorf("capabilities %v", got)
	}

	// The same address on the same server is a conflict; on another
	// server (realm) it is another account. Email and home set given: no
	// listing.
	dup := calDAVConfig(srv.ServerURL())
	dup.CalDAV.HomeSetURL = srv.HomeSetURL()
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: dup, Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("duplicate: %v", err)
	}
	other := calDAVConfig("https://radicale.example.test")
	other.CalDAV.HomeSetURL = "https://radicale.example.test/jana/"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: other, Credentials: api.Credentials{Password: "x"}}); err != nil {
		t.Fatalf("other realm: %v", err)
	}

	// mailAccountId: an existing account that sends mail.
	mail, err := acc.Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	jira := store.Account{Name: "Acme", Enabled: false, Config: api.AccountConfig{
		Name: "Acme", Email: "me@example.invalid", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "me@example.invalid"},
	}}
	if err := b.store.AddAccount(ctx, &jira); err != nil {
		t.Fatal(err)
	}
	upd := a.Config
	for _, bad := range []api.AccountID{"acc_none", api.AccountID(jira.ID), api.AccountID(id)} {
		c := upd
		cdc := *c.CalDAV
		cdc.MailAccountID = bad
		c.CalDAV = &cdc
		if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: c}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("mailAccountId %s: %v", bad, err)
		}
	}
	c := upd
	cdc := *c.CalDAV
	cdc.MailAccountID = mail.AccountID
	cdc.PrincipalURL, cdc.HomeSetURL = "", "" // kept from the stored login
	c.CalDAV = &cdc
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: c}); err != nil {
		t.Fatalf("update with mail account: %v", err)
	}
	a, _ = b.store.GetAccount(ctx, id)
	if a.Config.CalDAV.MailAccountID != mail.AccountID || a.Config.CalDAV.HomeSetURL != srv.HomeSetURL() {
		t.Errorf("updated: %+v", a.Config.CalDAV)
	}

	// Another user name or server needs its own password.
	c = a.Config
	cdc = *c.CalDAV
	cdc.Username = "petr"
	c.CalDAV = &cdc
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: c}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("new user without password: %v", err)
	}

	// Remove: the keyring entry goes.
	if _, err := acc.Remove(ctx, api.AccountRemoveParams{AccountID: api.AccountID(id)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := kr.values[id+"/"+auth.KeyPassword]; ok || !slices.Contains(kr.deleted, id+"/"+auth.KeyPassword) {
		t.Errorf("keyring after remove: %v %v", kr.values, kr.deleted)
	}
	if sup.count("stop:"+id) == 0 {
		t.Errorf("not stopped: %v", sup.recorded())
	}
}

// A changed server deletes the cached calendars; disabling one deletes
// its contents and keeps its row (D23; calendar.list shows it disabled).
func TestCalDAVUpdateDropsCalendarCache(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, _, _, _ := newCalDAVBackend(t, srv)
	cfg := calDAVConfig(srv.ServerURL())
	cfg.CalDAV.HomeSetURL = srv.HomeSetURL()
	res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	id := string(res.AccountID)
	seed := func() {
		if _, err := b.store.UpsertCalendars(ctx, id, []store.Calendar{
			{RemoteID: "/a/", Name: "A", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents},
			{RemoteID: "/b/", Name: "B", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents},
		}); err != nil {
			t.Fatal(err)
		}
	}
	remotes := func() []string {
		cals, err := b.store.ListCalendars(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cals {
			out = append(out, c.RemoteID)
		}
		return out
	}
	seed()
	a, _ := b.store.GetAccount(ctx, id)
	c := a.Config
	c.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{"/b/"}}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: res.AccountID, Config: c}); err != nil {
		t.Fatal(err)
	}
	if got := remotes(); len(got) != 2 || !slices.Contains(got, "/b/") {
		t.Errorf("after disabling /b/: %v", got)
	}
	a, _ = b.store.GetAccount(ctx, id)
	c = a.Config
	cdc := *c.CalDAV
	cdc.ServerURL = srv.ServerURL() + "/other"
	cdc.PrincipalURL, cdc.HomeSetURL = "", srv.HomeSetURL()
	c.CalDAV = &cdc
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: res.AccountID, Config: c,
		Credentials: api.Credentials{Password: caldavtest.Password}}); err != nil {
		t.Fatal(err)
	}
	if got := remotes(); len(got) != 0 {
		t.Errorf("after a new server: %v", got)
	}
}

func TestCalDAVAccountTest(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, _, _, _ := newCalDAVBackend(t, srv)
	cfg := calDAVConfig(srv.ServerURL())
	res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	r := res.CalDAV
	if r == nil || !r.OK || !slices.Equal(r.Capabilities, []string{"calendar-access", "calendar-auto-schedule", "sync-collection", "addressbook"}) {
		t.Fatalf("test: %+v", r)
	}
	if res.IMAP != nil || res.SMTP != nil || res.Graph != nil {
		t.Errorf("other endpoints: %+v", res)
	}
	res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{Password: "wrong"}})
	if err != nil || res.CalDAV.OK || res.CalDAV.Error.Code != api.CodeAuthFailed {
		t.Fatalf("wrong password: %+v %v", res, err)
	}
	res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg})
	if err != nil || res.CalDAV.Error == nil || res.CalDAV.Error.Code != api.CodeAuthRequired {
		t.Fatalf("no password: %+v %v", res, err)
	}

	// The stored password of an account of the same realm, never another's.
	cfg.CalDAV.HomeSetURL = srv.HomeSetURL()
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	if res, err = b.Accounts().Test(ctx, api.AccountTestParams{AccountID: add.AccountID, Config: cfg}); err != nil || !res.CalDAV.OK {
		t.Fatalf("stored password: %+v %v", res, err)
	}
	if _, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: add.AccountID, Config: calDAVConfig("https://radicale.example.test")}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("stored password for another server: %v", err)
	}

	// Radicale has no server scheduling and no sync-collection.
	rad := caldavtest.New(t, caldavtest.Radicale)
	b.CalDAVHTTP = rad.HTTPClient()
	res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: calDAVConfig(rad.ServerURL()), Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil || !res.CalDAV.OK || !slices.Equal(res.CalDAV.Capabilities, []string{"calendar-access", "addressbook"}) {
		t.Fatalf("radicale: %+v %v", res.CalDAV, err)
	}
}

// Discovery sends no credentials; a .well-known redirect to another host
// is shown in serverUrl; the pin is checked.
func TestDiscoverCalDAV(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.ICloud)
	b, _, _, _ := newCalDAVBackend(t, srv)
	res, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: srv.TypedURL()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ServerURL != "https://"+caldavtest.ICloudHost || res.FoundVia != api.CalDAVFoundWellKnown || !res.NeedsCredentials {
		t.Errorf("icloud: %+v", res)
	}
	for _, r := range srv.Requests() {
		if r.HasAuthorization {
			t.Fatalf("discovery sent credentials to %s", r.Host)
		}
	}
	// The pin is normalised and checked.
	if _, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: srv.TypedURL(), CertificateSHA256: "zz"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("bad pin: %v", err)
	}
	if _, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: "http://127.0.0.1:5232", CertificateSHA256: testPin}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("pin over http: %v", err)
	}
	if _, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: srv.TypedURL(), CertificateSHA256: strings.ToUpper(testPin)}); err != nil {
		t.Errorf("with a pin: %v", err)
	}
	if _, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: "https://u:p@x.test"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("user info: %v", err)
	}

	// Fastmail through SRV.
	fm := caldavtest.New(t, caldavtest.Fastmail)
	b.CalDAVHTTP, b.CalDAVResolver = fm.HTTPClient(), fm.Resolver()
	if res, err := b.Accounts().DiscoverCalDAV(ctx, api.AccountDiscoverCalDAVParams{URL: fm.TypedURL()}); err != nil || res.FoundVia != api.CalDAVFoundSRV {
		t.Errorf("fastmail: %+v %v", res, err)
	}
}

func TestListCalDAVCalendars(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, _, _, _ := newCalDAVBackend(t, srv)
	cfg := calDAVConfig(srv.ServerURL())
	cfg.Name, cfg.Email = "", "" // the sheet has neither yet
	res, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range res.Calendars {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"Personal", "Team"}) || res.Scheduling != api.CalDAVSchedulingServer ||
		!slices.Equal(res.Addresses, []string{"jana@example.test"}) || res.HomeSetURL != srv.HomeSetURL() {
		t.Errorf("listing: %+v", res)
	}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: cfg}); errCode(t, err) != api.CodeAuthRequired {
		t.Errorf("no password: %v", err)
	}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: validConfig()}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("imap config: %v", err)
	}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: cfg, Credentials: api.Credentials{Password: "wrong"}}); errCode(t, err) != api.CodeAuthFailed {
		t.Errorf("wrong password: %v", err)
	}
	// The stored password only for an account of the same realm.
	full := calDAVConfig(srv.ServerURL())
	full.CalDAV.HomeSetURL = srv.HomeSetURL()
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: full, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{AccountID: add.AccountID, Config: cfg}); err != nil {
		t.Errorf("stored password: %v", err)
	}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{AccountID: add.AccountID,
		Config: calDAVConfig("https://evil.test/dav")}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("stored password for another server: %v", err)
	}
	if srv.EvilRequests() != 0 || srv.AuthLeaks() != 0 {
		t.Errorf("evil %d, leaks %d", srv.EvilRequests(), srv.AuthLeaks())
	}
}

// partitionTransport moves the home set of the fake iCloud to another
// host of its site, as iCloud's pNN-caldav partitions are: the fake
// answers for it as for its own host, and every home-set href becomes an
// absolute URL there.
type partitionTransport struct {
	srv       *caldavtest.Server
	partition string // host the home set moves to
	homePath  string
}

func (p *partitionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.EqualFold(r.URL.Hostname(), p.partition) && p.partition != caldavtest.EvilHost {
		r = r.Clone(r.Context())
		r.URL.Host = caldavtest.ICloudHost
		r.Host = caldavtest.ICloudHost
	}
	resp, err := p.srv.RoundTrip(r)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	body = bytes.ReplaceAll(body, []byte("<d:href>"+p.homePath), []byte("<d:href>https://"+p.partition+p.homePath))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Del("Content-Length")
	return resp, nil
}

// iCloud: the server is the host discovery showed (the typed host never
// gets the password), the home set lives on a partition host of the same
// site and is trusted; a home set on another site is not.
func TestListCalDAVCalendarsICloud(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.ICloud)
	b, _, _, _ := newCalDAVBackend(t, srv)
	cfg := calDAVConfig("https://" + caldavtest.ICloudHost)
	res, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Calendars) != 2 || srv.AuthLeaks() != 0 {
		t.Fatalf("icloud: %+v, leaks %d", res, srv.AuthLeaks())
	}

	home, _ := url.Parse(srv.HomeSetURL())
	pt := &partitionTransport{srv: srv, partition: "p01-caldav.icloud.test", homePath: home.Path}
	b.CalDAVHTTP = &http.Client{Transport: pt}
	res, err = b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatalf("partition home set: %v", err)
	}
	if res.HomeSetURL != "https://p01-caldav.icloud.test"+home.Path || len(res.Calendars) != 2 {
		t.Fatalf("partition: %+v", res)
	}
	// The account keeps the URLs, so its syncer trusts the partition.
	cfg.CalDAV.PrincipalURL, cfg.CalDAV.HomeSetURL = res.PrincipalURL, res.HomeSetURL
	cfg.Email = "jana@icloud.test"
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := b.store.GetAccount(ctx, string(add.AccountID))
	if got := calDAVConfiguredOrigins(*a.Config.CalDAV); !slices.Contains(got, "https://p01-caldav.icloud.test") {
		t.Errorf("origins %v", got)
	}

	// A home set on another site: refused, and the password never goes there.
	ev := &partitionTransport{srv: srv, partition: caldavtest.EvilHost, homePath: home.Path}
	b.CalDAVHTTP = &http.Client{Transport: ev}
	if _, err := b.Accounts().ListCalDAVCalendars(ctx, api.AccountListCalDAVCalendarsParams{Config: calDAVConfig("https://" + caldavtest.ICloudHost),
		Credentials: api.Credentials{Password: caldavtest.Password}}); errCode(t, err) != api.CodeServerError {
		t.Errorf("home set on another site: %v", err)
	}
	if srv.EvilRequests() != 0 || srv.AuthLeaks() != 0 {
		t.Fatalf("requests to the other site: %d, leaks %d", srv.EvilRequests(), srv.AuthLeaks())
	}
}

// waitLong polls cond for up to 10 s.
func waitLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A caldav account runs in the calendar engine as its kind's supervisor:
// sync.status and account.list show its state, it has no folders, and
// every message method refuses it.
func TestCalDAVAccountSyncsInTheEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	b.CalDAVHTTP = srv.HTTPClient()
	done := b.StartSync(ctx)
	t.Cleanup(func() { cancel(); <-done })

	cfg := calDAVConfig(srv.ServerURL())
	cfg.CalDAV.HomeSetURL = srv.HomeSetURL()
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	id := add.AccountID
	waitLong(t, "the calendars stored", func() bool {
		cals, _ := b.store.ListCalendars(ctx, string(id))
		return len(cals) == 2
	})
	waitLong(t, "an idle state", func() bool {
		st, err := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: id})
		return err == nil && len(st.Accounts) == 1 && st.Accounts[0].Status == api.SyncIdle && st.Accounts[0].LastSync != nil
	})
	list, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil || len(list.Accounts) != 1 || list.Accounts[0].State.AccountID != id {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, ok := b.calendarEngine().CalendarState(string(id)); !ok {
		t.Error("no calendar state")
	}

	folders, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
	if err != nil || folders.Folders == nil || len(folders.Folders) != 0 {
		t.Fatalf("folder.list: %+v %v", folders, err)
	}
	if raw, _ := json.Marshal(folders); string(raw) != `{"folders":[]}` {
		t.Errorf("folder.list wire %s", raw)
	}
	msgs := b.Messages()
	for name, err := range map[string]error{
		"list": func() error {
			_, err := msgs.List(ctx, api.MessageListParams{AccountID: id, FolderID: "f"})
			return err
		}(),
		"get": func() error { _, err := msgs.Get(ctx, api.MessageGetParams{AccountID: id, MessageID: "m"}); return err }(),
		"body": func() error {
			_, err := msgs.Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: "m"})
			return err
		}(),
		"flag": func() error {
			_, err := msgs.Flag(ctx, api.MessageFlagParams{AccountID: id, MessageIDs: []api.MessageID{"m"}})
			return err
		}(),
		"move": func() error {
			_, err := msgs.Move(ctx, api.MessageMoveParams{AccountID: id, MessageIDs: []api.MessageID{"m"}})
			return err
		}(),
		"delete": func() error {
			_, err := msgs.Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{"m"}})
			return err
		}(),
		"send":     func() error { _, err := msgs.Send(ctx, api.MessageSendParams{AccountID: id}); return err }(),
		"download": func() error { _, err := msgs.Download(ctx, api.MessageDownloadParams{AccountID: id}); return err }(),
	} {
		if errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("message.%s: %v", name, err)
		}
	}
	// A mail account's methods still answer as before.
	if _, err := msgs.List(ctx, api.MessageListParams{AccountID: "acc_none", FolderID: "f"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}

	// Removing the account stops its syncer and drops its calendars and
	// the engine's data version of it.
	if err := b.store.SetMeta(ctx, calsync.MetaProviderVersion(string(id)), "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: id}); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.calendarEngine().CalendarState(string(id)); ok {
		t.Error("calendar state after remove")
	}
	if cals, _ := b.store.ListCalendars(ctx, string(id)); len(cals) != 0 {
		t.Errorf("calendars after remove: %d", len(cals))
	}
	if _, ok, _ := b.store.GetMeta(ctx, calsync.MetaProviderVersion(string(id))); ok {
		t.Error("calendar provider version after remove")
	}
}

// Graph's calendar.sync: on starts the account's calendar in the engine
// (consentRequired when the GOA entry's Calendar switch is off, without an
// error from account.update), off stops it and deletes the cached rows.
func TestGraphCalendarSyncSwitch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	fg := &fakeGOA{accounts: []goa.Account{{ID: goaID, ProviderType: goa.ProviderMicrosoft365, Email: "me@contoso.invalid", OAuth2: true, CalendarDisabled: true}}}
	b.GOA = fg
	done := b.StartSync(ctx)
	t.Cleanup(func() { cancel(); <-done })

	cfg := graphConfig()
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	id := string(add.AccountID)
	if _, ok := b.calendarEngine().CalendarState(id); ok {
		t.Fatal("calendar running with calendar.sync off")
	}
	cfg.Calendar = &api.CalendarConfig{Sync: true}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: add.AccountID, Config: cfg}); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	waitLong(t, "consentRequired", func() bool {
		st, ok := b.calendarEngine().CalendarState(id)
		return ok && st.Status == api.CalendarSyncConsentRequired
	})
	// The mail state is not the calendar's.
	if st, _ := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: add.AccountID}); len(st.Accounts) == 1 && st.Accounts[0].Status == api.SyncStatus(api.CalendarSyncConsentRequired) {
		t.Error("the mail state took the calendar's")
	}

	// Consent given in GOA: the next start is past the consent check (no
	// token here, so it waits for a sign-in).
	fg.accounts[0].CalendarDisabled = false
	b.Supervisor.Restart(store.Account{ID: id, Enabled: true, Config: cfg})
	waitLong(t, "past the consent check", func() bool {
		st, ok := b.calendarEngine().CalendarState(id)
		return ok && st.Status != api.CalendarSyncConsentRequired && st.Status != api.CalendarSyncIdle
	})

	if _, err := b.store.UpsertCalendars(ctx, id, []store.Calendar{{RemoteID: "AAMk1", Name: "Calendar", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents}}); err != nil {
		t.Fatal(err)
	}
	cfg.Calendar = &api.CalendarConfig{Sync: false}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: add.AccountID, Config: cfg}); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if _, ok := b.calendarEngine().CalendarState(id); ok {
		t.Error("calendar running after calendar.sync off")
	}
	if cals, _ := b.store.ListCalendars(ctx, id); len(cals) != 0 {
		t.Errorf("calendars after off: %d", len(cals))
	}
}

// One limiter per Graph account, shared by everything that talks to its
// mailbox (D13).
func TestGraphLimiterPerAccount(t *testing.T) {
	b := newTestBackend(t, config.Default())
	l := b.graphLimiter("acc_1")
	if l == nil || b.graphLimiter("acc_1") != l || b.graphLimiter("acc_2") == l {
		t.Fatal("not one limiter per account")
	}
	b.dropGraphLimiter("acc_1")
	if b.graphLimiter("acc_1") == l {
		t.Error("a removed account's limiter survived")
	}
}

func TestCalendarCacheDropped(t *testing.T) {
	g := graphConfig()
	on := g
	on.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{"a"}}
	more := g
	more.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{"a", "b", "b"}}
	off := g
	off.Calendar = &api.CalendarConfig{Sync: false}
	if all, cals := calendarCacheDropped(g, on); all || len(cals) != 0 {
		t.Errorf("off → on: %v %v", all, cals)
	}
	if all, cals := calendarCacheDropped(on, more); all || !slices.Equal(cals, []string{"b"}) {
		t.Errorf("disable b: %v %v", all, cals)
	}
	if all, _ := calendarCacheDropped(on, off); !all {
		t.Error("on → off keeps the rows")
	}
	if all, _ := calendarCacheDropped(on, validConfig()); !all {
		t.Error("kind change keeps the rows")
	}
}

// The iMIP hook queues a message with the calendar part in the mail
// account's outbox and wakes it.
func TestQueueIMIP(t *testing.T) {
	ctx := context.Background()
	b, _, out, _ := newCalDAVBackend(t, nil)
	mail, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:U1\r\nSUMMARY:Planning\r\nDTSTART:20261020T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if err := b.queueIMIP(ctx, "", IMIPMail{Method: "REQUEST", Data: data, Recipients: []string{"petr@example.test"}}); errCode(t, err) != api.CodeInvitationsUnavailable {
		t.Fatalf("no account: %v", err)
	}
	if err := b.queueIMIP(ctx, string(mail.AccountID), IMIPMail{Method: "REQUEST", Data: data,
		Recipients: []string{"petr@example.test", "not an address"}, Mail: &api.MailText{Subject: "Invitation:\r\nPlanning", Text: "Come."}}); err != nil {
		t.Fatal(err)
	}
	pending, _, err := b.store.OutboxCounts(ctx, string(mail.AccountID))
	if err != nil || pending != 1 {
		t.Fatalf("outbox: %d %v", pending, err)
	}
	if !slices.Contains(out.recorded(), "wake:"+string(mail.AccountID)) {
		t.Errorf("outbox calls %v", out.recorded())
	}
	if s, _ := imipText(IMIPMail{Method: "REQUEST", Data: data}); s != "Planning" {
		t.Errorf("fallback subject %q", s)
	}
	if s, _ := imipText(IMIPMail{Method: "REQUEST", Data: data, Mail: &api.MailText{Subject: "A\r\nB"}}); s != "A B" {
		t.Errorf("subject %q", s)
	}
}
