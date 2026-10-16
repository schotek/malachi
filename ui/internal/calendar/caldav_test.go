// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

var testSHA = strings.Repeat("ab", 32)

func tlsErr(reason api.TLSErrorReason, sha string) *api.Error {
	d := api.TLSErrorData{Reason: reason}
	if sha != "" {
		d.Certificate = &api.CertificateInfo{SHA256: sha, Issuer: "Home CA", SelfSigned: true}
	}
	return &api.Error{Code: api.CodeTLSError, Message: "tls", Data: d}
}

func listed() *api.AccountListCalDAVCalendarsResult {
	return &api.AccountListCalDAVCalendarsResult{PrincipalURL: "https://dav.example.net/p/", HomeSetURL: "https://dav.example.net/c/",
		DisplayName: "Jana", Addresses: []string{"jana@example.org"}, Scheduling: api.CalDAVSchedulingClient,
		Calendars: []api.CalDAVCalendar{{Href: "/c/personal/", Name: "Personal", Access: api.CalendarAccessOwner, IsDefault: true},
			{Href: "/c/work/", Name: "Work", Access: api.CalendarAccessOwner}, {Href: "/c/holidays/", Name: "Holidays", Access: api.CalendarAccessRead}}}
}

func TestWizardFlow(t *testing.T) {
	w := NewWizard("acc_mail")
	if w.CanDiscover() || w.BeginDiscover() || w.CanAdd() {
		t.Fatal("an empty sheet can go on")
	}
	w.Edit("", "cloud.example.org", "jana", "")
	if w.CanDiscover() {
		t.Error("discovers without a password")
	}
	w.Edit("", "cloud.example.org", "jana", "secret")
	if !w.CanDiscover() || !w.BeginDiscover() || w.Phase != WizardDiscovering || w.Step != StepDiscover || w.FieldsEnabled() {
		t.Fatalf("discover did not start: %+v", w)
	}
	if p := w.DiscoverParams(); p.URL != "cloud.example.org" {
		t.Errorf("DiscoverParams %+v", p)
	}
	w.Edit("x", "other.example.org", "x", "x")
	if w.Server != "cloud.example.org" {
		t.Error("fields changed while discovering")
	}
	if WizardProgress(w, en{}) != "Looking for calendars…" || w.CanAdd() {
		t.Error("discovering")
	}
	w.ListDone(listed(), nil) // out of order: ignored
	if w.Listed != nil {
		t.Error("a listing before discovery was taken")
	}
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://dav.example.net", FoundVia: api.CalDAVFoundWellKnown,
		NeedsCredentials: true, Certificate: &api.CertificateInfo{SHA256: testSHA, Issuer: "R11"}}, nil)
	if w.Phase != WizardDiscovering || w.Step != StepList {
		t.Fatalf("after discovery %+v", w)
	}
	if ServerChanged(w, en{}) != "The calendars are at dav.example.net." || CertificateText(w, en{}) != "Verified (R11)" {
		t.Error("discovery texts")
	}
	lp := w.ListParams()
	if lp.Config.Kind != api.AccountCalDAV || lp.Config.CalDAV.ServerURL != "https://dav.example.net" ||
		lp.Config.CalDAV.Username != "jana" || lp.Credentials.Password != "secret" || lp.Config.Name != "cloud.example.org" {
		t.Errorf("ListParams %+v %+v", lp.Config, lp.Config.CalDAV)
	}
	w.ListDone(listed(), nil)
	if w.Phase != WizardListed || len(w.Selected) != 3 || w.Name != "Jana" || !w.ShowsMailAccount() || !w.CanAdd() {
		t.Fatalf("after listing %+v", w)
	}
	if FoundLine(w, en{}) != "Found through /.well-known/caldav · 3 calendars" {
		t.Errorf("FoundLine %q", FoundLine(w, en{}))
	}
	w.Toggle("/c/holidays/", false)
	w.Toggle("/nowhere/", true)
	if w.Selected["/nowhere/"] {
		t.Error("toggled a calendar that was not listed")
	}
	w.Toggle("/c/personal/", false)
	w.Toggle("/c/work/", false)
	if w.CanAdd() || w.BeginAdd() {
		t.Error("adds without a calendar")
	}
	w.Toggle("/c/personal/", true)
	w.Toggle("/c/work/", true)
	if !w.BeginAdd() || w.Phase != WizardAdding || WizardProgress(w, en{}) != "Adding the account…" {
		t.Fatalf("add did not start %+v", w)
	}
	ap := w.AddParams()
	if ap.Config.Name != "Jana" || ap.Config.Email != "jana@example.org" || ap.Config.CalDAV.MailAccountID != "acc_mail" ||
		ap.Config.CalDAV.PrincipalURL != "https://dav.example.net/p/" || ap.Config.Calendar == nil || !ap.Config.Calendar.Sync ||
		!slices.Equal(ap.Config.Calendar.DisabledCalendars, []string{"/c/holidays/"}) || ap.Config.Calendar.DefaultCalendar != "/c/personal/" ||
		ap.Credentials.Password != "secret" {
		t.Errorf("AddParams %+v %+v %+v", ap.Config, ap.Config.CalDAV, ap.Config.Calendar)
	}
	w.AddDone("", &api.Error{Code: api.CodeAuthFailed})
	if w.Phase != WizardListed || WizardErrorText(w, en{}) != "The server did not accept the username or password." {
		t.Errorf("after a failed add %+v", w)
	}
	w.BeginAdd()
	w.AddDone("acc_new", nil)
	if w.Phase != WizardAdded || w.AccountID != "acc_new" || w.Password != "" {
		t.Errorf("after the add %+v", w)
	}
}

func TestWizardEditsAndErrors(t *testing.T) {
	w := NewWizard("")
	w.Edit("Home", "cloud.example.org", "jana", "pw")
	w.BeginDiscover()
	w.DiscoverDone(nil, &api.Error{Code: api.CodeNetworkError})
	if w.Phase != WizardEntering || WizardErrorText(w, en{}) != "The server could not be reached." || !w.CanDiscover() {
		t.Fatalf("after a failed discovery %+v", w)
	}
	w.BeginDiscover()
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://cloud.example.org/dav", FoundVia: api.CalDAVFoundSRV}, nil)
	w.ListDone(nil, &api.Error{Code: api.CodeServerError})
	if w.Phase != WizardEntering || w.Listed != nil || WizardErrorText(w, en{}) != "The server answered, but it is not a CalDAV server." {
		t.Fatalf("after a failed listing %+v", w)
	}
	w.BeginDiscover()
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://cloud.example.org/dav", FoundVia: api.CalDAVFoundSRV}, nil)
	l := listed()
	l.Scheduling = api.CalDAVSchedulingServer
	w.ListDone(l, nil)
	if w.Name != "Home" || w.ShowsMailAccount() || FoundLine(w, en{}) != "Found through a DNS SRV record · 3 calendars" {
		t.Errorf("listing %+v", w)
	}
	w.Edit("Home 2", "cloud.example.org", "jana", "pw")
	if w.Phase != WizardListed || w.Listed == nil {
		t.Error("renaming dropped the listing")
	}
	w.Edit("Home 2", "cloud.example.org", "jana2", "pw")
	if w.Phase != WizardEntering || w.Listed != nil || w.Discovered != nil || len(w.Selected) != 0 {
		t.Errorf("a new username kept the listing %+v", w)
	}
	if got := w.AddParams().Config.CalDAV.MailAccountID; got != "" {
		t.Errorf("mail account without client scheduling %q", got)
	}
}

func TestWizardTrust(t *testing.T) {
	w := NewWizard("")
	w.Edit("", "cloud.example.org", "jana", "pw")
	w.BeginDiscover()
	w.DiscoverDone(nil, tlsErr(api.TLSUntrusted, testSHA))
	p, ok := w.TrustOffer()
	if !ok || p.Cert.SHA256 != testSHA || WizardErrorText(w, en{}) != "The server’s certificate is not trusted." {
		t.Fatalf("no trust offer: %+v %v", p, ok)
	}
	if !w.Trust() || w.Pin != testSHA || w.Err != nil || w.Phase != WizardDiscovering || w.Step != StepDiscover ||
		CertificateText(w, en{}) != "Trusted by you" {
		t.Fatalf("Trust %+v", w)
	}
	if _, ok := w.TrustOffer(); ok {
		t.Error("an offer after trusting")
	}
	// Discovery runs again with the pin, which goes on into the listing
	// and the account.
	if dp := w.DiscoverParams(); dp.CertificateSHA256 != testSHA || dp.URL != "cloud.example.org" {
		t.Errorf("DiscoverParams after trust %+v", dp)
	}
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://cloud.example.org/remote.php/dav", FoundVia: api.CalDAVFoundWellKnown}, nil)
	lp := w.ListParams()
	if lp.Config.CalDAV.CertificateSHA256 != testSHA || lp.Config.CalDAV.ServerURL != "https://cloud.example.org/remote.php/dav" {
		t.Errorf("ListParams after trust %+v", lp.Config.CalDAV)
	}
	w.ListDone(listed(), nil)
	w.BeginAdd()
	if ap := w.AddParams(); ap.Config.CalDAV.CertificateSHA256 != testSHA {
		t.Errorf("AddParams after trust %+v", ap.Config.CalDAV)
	}
	w.AddDone("", &api.Error{Code: api.CodeServerError})
	// A redirect to another host does not take the pin along.
	w.Edit(w.Name, w.Server, w.Username, "pw2")
	w.BeginDiscover()
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://elsewhere.example.net/dav", FoundVia: api.CalDAVFoundWellKnown}, nil)
	if got := w.ListParams().Config.CalDAV.CertificateSHA256; got != "" {
		t.Errorf("the pin went to another host: %q", got)
	}
	w.ListDone(nil, tlsErr(api.TLSUntrusted, testSHA))
	// The same certificate refused again offers nothing.
	w.ListDone(nil, tlsErr(api.TLSUntrusted, testSHA))
	if _, ok := w.TrustOffer(); ok {
		t.Error("offer for the pinned certificate")
	}
	// Another host forgets the pin; the same host keeps it.
	w.Edit("", "https://cloud.example.org/remote.php/dav", "jana", "pw")
	if w.Pin != testSHA {
		t.Error("the same host lost the pin")
	}
	w.Edit("", "evil.example.net", "jana", "pw")
	if w.Pin != "" {
		t.Error("another host kept the pin")
	}
	offers := []struct {
		name string
		err  *api.Error
		url  string
		ok   bool
	}{
		{"handshake", tlsErr(api.TLSHandshake, testSHA), "cloud.example.org", false},
		{"no certificate", tlsErr(api.TLSUntrusted, ""), "cloud.example.org", false},
		{"bad fingerprint", tlsErr(api.TLSUntrusted, "xyz"), "cloud.example.org", false},
		{"pin mismatch", tlsErr(api.TLSPinMismatch, testSHA), "cloud.example.org", true},
		{"plain http", tlsErr(api.TLSUntrusted, testSHA), "http://127.0.0.1:5232", false},
		{"another error", &api.Error{Code: api.CodeNetworkError}, "cloud.example.org", false},
	}
	for _, tt := range offers {
		w := NewWizard("")
		w.Edit("", tt.url, "u", "p")
		w.BeginDiscover()
		w.DiscoverDone(nil, tt.err)
		if _, ok := w.TrustOffer(); ok != tt.ok {
			t.Errorf("%s: offer %v", tt.name, ok)
		}
	}
}
