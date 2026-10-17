// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The Add CalDAV Calendar sheet's controller (CalDAVWizardTests and
// CalendarCalDAVWizardControllerTests of the macOS client) over the
// in-memory source: discover → list → add with their params, the
// mail-account popup, a refused discovery, and the certificate trust that
// runs discovery again with the pin only after the confirmation. The
// helpers here (g8…) serve G8's other tests too.

type g8En struct{}

func (g8En) T(msgid string) string { return msgid }
func (g8En) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (g8En) C(_, msgid string) string { return msgid }

// g8Loop holds what is posted until run.
type g8Loop struct{ queue []func() }

func (l *g8Loop) Post(f func())                   { l.queue = append(l.queue, f) }
func (l *g8Loop) After(_ time.Duration, f func()) { l.queue = append(l.queue, f) }

func (l *g8Loop) run() {
	for len(l.queue) > 0 {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
	}
}

var g8Zone = func() *time.Location {
	l, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		panic(err)
	}
	return l
}()

var g8Now = time.Date(2026, 10, 14, 12, 0, 0, 0, g8Zone)

// g8Env writes English with the calendar's own strftime.
var g8Env = calendar.Env{Tr: g8En{}, Format: calendar.EnglishStrftime, Loc: g8Zone}

func g8Opts(l *g8Loop) Options {
	return Options{Env: g8Env, Now: func() time.Time { return g8Now }, Loop: l}
}

// g8Samples are the samples with their remote ids; writes emulated or
// answering the preview's error.
func g8Samples(writes bool) (*calendardata.InMemorySource, *g8Loop) {
	l := &g8Loop{}
	s := calendardata.NewInMemorySource(calendar.SampleData(g8Now, g8Zone).WithRemoteIDs(), g8Zone, l)
	s.EmulatesWrites = writes
	return s, l
}

const g8SHA = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"

func g8Wizard(s *calendardata.InMemorySource, l *g8Loop) *CalDAVWizardController {
	s.DiscoverResult = api.AccountDiscoverCalDAVResult{ServerURL: "https://dav.example.net/remote.php/dav",
		FoundVia: api.CalDAVFoundWellKnown, NeedsCredentials: true}
	s.ListResult = api.AccountListCalDAVCalendarsResult{PrincipalURL: "https://dav.example.net/p/",
		HomeSetURL: "https://dav.example.net/c/", DisplayName: "Jana", Addresses: []string{"jana@example.org"},
		Scheduling: api.CalDAVSchedulingClient, Calendars: []api.CalDAVCalendar{
			{Href: "/c/personal/", Name: "Personal", Access: api.CalendarAccessOwner, IsDefault: true},
			{Href: "/c/work/", Name: "Work", Access: api.CalendarAccessOwner},
		}}
	s.AddedAccount = "acc_dav"
	w := NewCalDAVWizardController(s, g8Opts(l))
	w.Edit("", "dav.example.net", "jana", "secret")
	return w
}

func g8Methods(s *calendardata.InMemorySource) []string {
	var out []string
	for _, w := range s.Writes() {
		out = append(out, w.Method)
	}
	return out
}

func TestCalDAVWizardDiscoverListAndAdd(t *testing.T) {
	s, l := g8Samples(true)
	w := g8Wizard(s, l)
	m := w.MailAccounts()
	if len(m) != 2 || m[0].ID != calendar.SampleWork || m[1].ID != calendar.SampleGmail {
		t.Fatalf("mail accounts %+v", m)
	}
	if w.Wizard().MailAccount != calendar.SampleWork || !w.Wizard().CanDiscover() {
		t.Fatalf("wizard %+v", w.Wizard())
	}
	w.Discover()
	l.run()
	if w.Wizard().Phase != calendar.WizardListed {
		t.Fatalf("phase %v", w.Wizard().Phase)
	}
	if got := calendar.FoundLine(w.Wizard(), g8En{}); got != "Found through /.well-known/caldav · 2 calendars" {
		t.Fatalf("found %q", got)
	}
	if !w.Wizard().ShowsMailAccount() {
		t.Fatal("no mail account popup")
	}
	w.SetMailAccount(calendar.SampleGmail)
	w.Toggle("/c/work/", false)
	var done []api.AccountID
	w.OnDone = func(id api.AccountID) { done = append(done, id) }
	w.Add()
	l.run()
	if len(done) != 1 || done[0] != "acc_dav" || w.Wizard().Phase != calendar.WizardAdded {
		t.Fatalf("done %v phase %v", done, w.Wizard().Phase)
	}
	ws := s.Writes()
	if len(ws) != 3 {
		t.Fatalf("calls %v", g8Methods(s))
	}
	d := ws[0].Params.(api.AccountDiscoverCalDAVParams)
	ls := ws[1].Params.(api.AccountListCalDAVCalendarsParams)
	a := ws[2].Params.(api.AccountAddParams)
	if d.URL != "dav.example.net" || d.CertificateSHA256 != "" {
		t.Fatalf("discover %+v", d)
	}
	if ls.Config.CalDAV.ServerURL != "https://dav.example.net/remote.php/dav" || ls.Credentials.Password != "secret" {
		t.Fatalf("list %+v", ls)
	}
	if a.Config.Kind != api.AccountCalDAV || a.Config.Name != "Jana" || a.Config.Email != "jana@example.org" ||
		a.Config.CalDAV.MailAccountID != calendar.SampleGmail {
		t.Fatalf("add %+v", a.Config)
	}
	if k := a.Config.Calendar; len(k.DisabledCalendars) != 1 || k.DisabledCalendars[0] != "/c/work/" || k.DefaultCalendar != "/c/personal/" {
		t.Fatalf("calendar %+v", k)
	}
}

func TestCalDAVWizardRefusedDiscoveryGoesBackToEntering(t *testing.T) {
	s, l := g8Samples(true)
	s.Failures[api.MethodAccountDiscoverCalDAV] = &api.Error{Code: api.CodeServerError, Message: "not caldav"}
	w := g8Wizard(s, l)
	w.Discover()
	l.run()
	if w.Wizard().Phase != calendar.WizardEntering || w.Wizard().Err == nil {
		t.Fatalf("wizard %+v", w.Wizard())
	}
	if w.ErrorText() != "The server answered, but it is not a CalDAV server." {
		t.Fatalf("error %q", w.ErrorText())
	}
	if f, ok := w.ErrorField(); !ok || f != calendar.CalDAVServer {
		t.Fatalf("field %v %v", f, ok)
	}

	ps, pl := g8Samples(false)
	p := g8Wizard(ps, pl)
	p.Discover()
	pl.run()
	if p.Wizard().Phase != calendar.WizardEntering || !p.PreviewRefused() || p.ErrorText() != calendar.PreviewOnly(g8En{}) {
		t.Fatalf("preview %+v %q", p.Wizard(), p.ErrorText())
	}
	if _, ok := p.ErrorField(); ok {
		t.Fatal("the preview's error marks a field")
	}
	// An edit clears the preview's line.
	p.Edit("", "dav.example.org", "jana", "secret")
	if p.PreviewRefused() {
		t.Fatal("still refused after an edit")
	}

	// A timeout reads as the server's.
	s.Failures[api.MethodAccountDiscoverCalDAV] = &api.Error{Code: api.CodeCancelled, Message: "timed out"}
	w.Discover()
	l.run()
	if w.Wizard().Err == nil || w.Wizard().Err.Code != api.CodeServerTimeout || w.ErrorText() != "The server could not be reached." {
		t.Fatalf("timeout %+v", w.Wizard().Err)
	}
	// A refused sign-in marks the password.
	s.Failures[api.MethodAccountDiscoverCalDAV] = &api.Error{Code: api.CodeAuthFailed, Message: "401"}
	w.Discover()
	l.run()
	if f, ok := w.ErrorField(); !ok || f != calendar.CalDAVPassword {
		t.Fatalf("field %v %v", f, ok)
	}
}

func TestCalDAVWizardTrustRunsDiscoveryAgainWithThePin(t *testing.T) {
	s, l := g8Samples(true)
	s.Failures[api.MethodAccountDiscoverCalDAV] = &api.Error{Code: api.CodeTLSError, Message: "x",
		Data: api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: &api.CertificateInfo{SHA256: g8SHA, Issuer: "Home CA",
			NotBefore: time.Unix(0, 0), NotAfter: time.Unix(0, 0), SelfSigned: true}}}
	w := g8Wizard(s, l)
	w.Discover()
	l.run()
	if _, ok := w.Wizard().TrustOffer(); !ok {
		t.Fatalf("no trust offer: %+v", w.Wizard().Err)
	}
	// No confirmation wired: nothing happens.
	w.Trust()
	l.run()
	// Refused: nothing pinned.
	var reqs []TrustRequest
	answer := false
	w.OnConfirmTrust = func(r TrustRequest, done func(bool)) {
		reqs = append(reqs, r)
		done(answer)
		done(true) // a second answer is ignored
	}
	w.Trust()
	l.run()
	if len(reqs) != 1 || len(s.Writes()) != 1 || w.Wizard().Pin != "" {
		t.Fatalf("refused: %d prompts, calls %v, pin %q", len(reqs), g8Methods(s), w.Wizard().Pin)
	}
	if r := reqs[0]; len(r.Servers) != 1 || r.Servers[0] != "dav.example.net" || r.Changed || r.Problem.Cert.SHA256 != g8SHA {
		t.Fatalf("request %+v", r)
	}
	// Confirmed: discovery again with the pin, then the list.
	delete(s.Failures, api.MethodAccountDiscoverCalDAV)
	answer = true
	w.Trust()
	l.run()
	if w.Wizard().Phase != calendar.WizardListed || len(reqs) != 2 {
		t.Fatalf("phase %v prompts %d", w.Wizard().Phase, len(reqs))
	}
	again, ok := s.Writes()[1].Params.(api.AccountDiscoverCalDAVParams)
	if !ok || again.CertificateSHA256 != g8SHA {
		t.Fatalf("second discovery %+v", s.Writes()[1])
	}
	if calendar.CertificateText(w.Wizard(), g8En{}) != "Trusted by you" {
		t.Fatalf("certificate %q", calendar.CertificateText(w.Wizard(), g8En{}))
	}
	// A confirmation that comes after Cancel pins nothing.
	w2 := g8Wizard(s, l)
	s.Failures[api.MethodAccountDiscoverCalDAV] = &api.Error{Code: api.CodeTLSError, Message: "x",
		Data: api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: &api.CertificateInfo{SHA256: g8SHA}}}
	w2.Discover()
	l.run()
	var later func(bool)
	w2.OnConfirmTrust = func(_ TrustRequest, done func(bool)) { later = done }
	w2.Trust()
	w2.Cancel()
	n := len(s.Writes())
	later(true)
	l.run()
	if len(s.Writes()) != n || w2.Wizard().Pin != "" {
		t.Fatal("a late confirmation pinned")
	}
}

func TestCalDAVWizardListedCalendarsAndTheMailAccountDefault(t *testing.T) {
	s, l := g8Samples(true)
	w := g8Wizard(s, l)
	changes := 0
	w.OnChange = func() { changes++ }
	w.Start()
	w.Discover()
	l.run()
	if w.Wizard().Phase != calendar.WizardListed {
		t.Fatalf("phase %v", w.Wizard().Phase)
	}
	cs := w.ListedCalendars()
	if len(cs) != 2 || cs[0].Name != "Personal" || cs[1].Name != "Work" || cs[0].Note != "default" || cs[1].Note != "" ||
		!cs[0].Selected || !cs[1].Selected {
		t.Fatalf("listed %+v", cs)
	}
	w.Toggle("/c/work/", false)
	if cs := w.ListedCalendars(); !cs[0].Selected || cs[1].Selected {
		t.Fatalf("toggled %+v", cs)
	}
	// None chosen stays None when the snapshot changes.
	w.SetMailAccount("")
	s.Update(func(sn *calendardata.Snapshot) { sn.Unanswered++ })
	if w.Wizard().MailAccount != "" {
		t.Fatalf("mail account %q", w.Wizard().MailAccount)
	}
	if changes == 0 {
		t.Fatal("no change reported")
	}
	w.Cancel()
	if s.Observers() != 0 {
		t.Fatal("still observing after Cancel")
	}
	n := changes
	s.Update(func(sn *calendardata.Snapshot) { sn.Unanswered++ })
	if changes != n {
		t.Fatal("a change after Cancel")
	}
}

func TestCalDAVWizardFollowsTheMailAccounts(t *testing.T) {
	s, l := g8Samples(true)
	s.Update(func(sn *calendardata.Snapshot) { sn.Accounts = sn.Accounts[2:] }) // Nextcloud only
	w := NewCalDAVWizardController(s, g8Opts(l))
	if w.Wizard().MailAccount != "" || len(w.MailAccounts()) != 0 {
		t.Fatalf("mail account %q", w.Wizard().MailAccount)
	}
	w.Start()
	s.Update(func(sn *calendardata.Snapshot) {
		sn.Accounts = append(sn.Accounts, api.Account{ID: "acc_mail", Config: api.AccountConfig{Name: "Mail‮"},
			Capabilities: api.MailCapabilities})
	})
	if w.Wizard().MailAccount != "acc_mail" || strings.ContainsRune(w.MailAccounts()[0].Name, '‮') {
		t.Fatalf("followed %q %+v", w.Wizard().MailAccount, w.MailAccounts())
	}
}
