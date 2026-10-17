// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// An account's section of Preferences → Calendar
// (CalendarAccountControllerTests of the macOS client) over the in-memory
// samples: the Graph switch with the consent question and the re-sign-in
// hand-off, the calendars' checkboxes and default by remote id (Graph
// from calendar.list, CalDAV also from the server), Keep Events, the
// invitation switches, an account without a calendar, a refusal undone,
// the CalDAV connection rows.

func g8Account(s *calendardata.InMemorySource, l *g8Loop, id api.AccountID) *CalendarAccountController {
	c := NewCalendarAccountController(s, id, g8Opts(l))
	c.Start()
	return c
}

func g8LastUpdate(t *testing.T, s *calendardata.InMemorySource) api.AccountUpdateParams {
	t.Helper()
	ws := s.Writes()
	for i := len(ws) - 1; i >= 0; i-- {
		if p, ok := ws[i].Params.(api.AccountUpdateParams); ok {
			return p
		}
	}
	t.Fatal("no account.update")
	return api.AccountUpdateParams{}
}

func g8Names(rows []CalendarRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestAccountGraphSyncAsksForConsent(t *testing.T) {
	s, l := g8Samples(true)
	s.Update(func(sn *calendardata.Snapshot) { sn.Accounts[0].Config.Calendar = &api.CalendarConfig{Sync: false} })
	c := g8Account(s, l, calendar.SampleWork)
	if !c.IsGraph() || !c.ShowsSyncSwitch() || c.SyncOn() || !c.HasCalendarCapability() || c.ShowsNoCalendar() {
		t.Fatal("graph account rows")
	}
	var questions []calendar.Question
	var signIn []api.AccountID
	c.OnQuestion = func(q calendar.Question, answer func(Answer)) {
		questions = append(questions, q)
		answer(AnswerConfirm)
	}
	c.OnSignIn = func(id api.AccountID) { signIn = append(signIn, id) }
	c.SetSync(true)
	if !c.SyncOn() || !c.Saving() {
		t.Fatal("not shown at once")
	}
	l.run()
	p := g8LastUpdate(t, s)
	if c.Saving() || !p.Config.Calendar.Sync || p.Credentials != (api.Credentials{}) {
		t.Fatalf("update %+v", p)
	}
	if len(questions) != 0 {
		t.Fatal("asked before the daemon said so")
	}
	// The daemon reports the missing scopes.
	raw, _ := json.Marshal(api.CalendarStateNotification{State: api.CalendarSyncState{AccountID: calendar.SampleWork,
		Status: api.CalendarSyncConsentRequired, Progress: -1}})
	s.Handle(api.NotifyCalendarState, raw)
	if len(questions) != 1 || questions[0].Body != calendar.SyncConsentNote(g8En{}) || questions[0].Confirm != calendar.SignIn(g8En{}) {
		t.Fatalf("questions %+v", questions)
	}
	if !slices.Equal(signIn, []api.AccountID{calendar.SampleWork}) || !c.ConsentRequired() {
		t.Fatalf("sign in %v", signIn)
	}
	// Asked once.
	s.Update(func(sn *calendardata.Snapshot) { sn.Unanswered++ })
	if len(questions) != 1 {
		t.Fatal("asked again")
	}
}

func TestAccountGraphConsentCancelled(t *testing.T) {
	s, l := g8Samples(true)
	s.Update(func(sn *calendardata.Snapshot) { sn.Accounts[0].Config.Calendar = &api.CalendarConfig{Sync: false} })
	c := g8Account(s, l, calendar.SampleWork)
	signedIn := 0
	c.OnQuestion = func(_ calendar.Question, answer func(Answer)) { answer(AnswerCancel) }
	c.OnSignIn = func(api.AccountID) { signedIn++ }
	c.SetSync(true)
	l.run()
	s.Update(func(sn *calendardata.Snapshot) {
		sn.CalendarAccounts[0].State.Status = api.CalendarSyncConsentRequired
	})
	if signedIn != 0 {
		t.Fatal("signed in after Cancel")
	}
	c.SignIn()
	if signedIn != 1 {
		t.Fatal("Sign In… of the consent row")
	}
}

func TestAccountGraphCalendarsToggleAndDefault(t *testing.T) {
	s, l := g8Samples(true)
	s.Update(func(sn *calendardata.Snapshot) {
		sn.CalendarAccounts[0].Calendars = append(sn.CalendarAccounts[0].Calendars, api.Calendar{
			ID: "cal_sample_old", AccountID: calendar.SampleWork, RemoteID: "AAMkSampleOld", Name: "Old projects",
			Color: "#888888", Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, Disabled: true})
		// As the daemon keeps it: the account's configuration names it.
		sn.Accounts[0].Config.Calendar = &api.CalendarConfig{Sync: true, DisabledCalendars: []string{"AAMkSampleOld"}}
	})
	c := g8Account(s, l, calendar.SampleWork)
	if _, ok := c.Listed(); ok {
		t.Fatal("a Graph account lists CalDAV calendars")
	}
	rows := c.Rows()
	if !slices.Equal(g8Names(rows), []string{"Calendar", "Birthdays", "Czech holidays", "Old projects"}) {
		t.Fatalf("rows %v", g8Names(rows))
	}
	for i, want := range []bool{true, true, true, false} {
		if !rows[i].CanToggle() || rows[i].Enabled != want {
			t.Fatalf("row %d %+v", i, rows[i])
		}
	}
	if rows[0].Note != "default" {
		t.Fatalf("note %q", rows[0].Note)
	}
	c.SetEnabled(false, "AAMkSampleHolidays")
	l.run()
	if d := g8LastUpdate(t, s).Config.Calendar.DisabledCalendars; !slices.Equal(d, []string{"AAMkSampleOld", "AAMkSampleHolidays"}) {
		t.Fatalf("disabled %v", d)
	}
	if c.Rows()[2].Enabled {
		t.Fatal("still enabled")
	}
	c.SetEnabled(true, "AAMkSampleOld")
	l.run()
	var def []string
	for _, r := range c.DefaultChoices() {
		def = append(def, r.RemoteID)
	}
	if !c.Rows()[3].Enabled || !slices.Equal(def, []string{"AAMkSampleCalendar", "AAMkSampleOld"}) {
		t.Fatalf("default choices %v", def)
	}
	c.SetDefaultCalendar("AAMkSampleOld")
	l.run()
	if g8LastUpdate(t, s).Config.Calendar.DefaultCalendar != "AAMkSampleOld" {
		t.Fatal("default not written")
	}
	if r := c.Rows(); r[3].Note != "default" || r[0].Note != "" || !r[3].IsDefault || r[0].IsDefault {
		t.Fatalf("notes %q %q", r[3].Note, r[0].Note)
	}
	// Disabling the default drops it.
	c.SetEnabled(false, "AAMkSampleOld")
	l.run()
	if g8LastUpdate(t, s).Config.Calendar.DefaultCalendar != "" {
		t.Fatal("default kept")
	}
	if slices.Contains(s.Reads(), api.MethodAccountListCalDAVCalendars) ||
		slices.Contains(g8Methods(s), api.MethodAccountListCalDAVCalendars) {
		t.Fatal("listed CalDAV calendars of a Graph account")
	}
	// An unknown remote id changes nothing.
	n := len(s.Writes())
	c.SetEnabled(false, "nope")
	if len(s.Writes()) != n {
		t.Fatal("wrote an unknown calendar")
	}
	// The snapshot's config stays as it was until the daemon's answer.
	if a, _ := s.Snapshot().Account(calendar.SampleWork); a.Config.Calendar == c.Config().Calendar {
		t.Fatal("shares the snapshot's config")
	}
}

func TestAccountCalDAVCalendarsDefaultAndKeep(t *testing.T) {
	s, l := g8Samples(true)
	s.ListResult = api.AccountListCalDAVCalendarsResult{PrincipalURL: "p", HomeSetURL: "h", Scheduling: api.CalDAVSchedulingClient,
		Calendars: []api.CalDAVCalendar{
			{Href: "/remote.php/dav/calendars/me/sport/", Name: "Sport", Color: "#0082c9", Access: api.CalendarAccessOwner, IsDefault: true},
			{Href: "/c/club/", Name: "Club", Access: api.CalendarAccessRead},
		}}
	c := g8Account(s, l, calendar.SampleNextcloud)
	l.run()
	if _, ok := c.Listed(); !ok {
		t.Fatal("not listed")
	}
	lp, ok := s.Writes()[0].Params.(api.AccountListCalDAVCalendarsParams)
	if !ok || lp.AccountID != calendar.SampleNextcloud || lp.Credentials != (api.Credentials{}) {
		t.Fatalf("list %+v", s.Writes()[0])
	}
	if !c.IsCalDAV() || c.ShowsSyncSwitch() || !c.SyncOn() {
		t.Fatal("caldav rows")
	}
	// The stored calendar once, then the server's calendar not stored.
	rows := c.Rows()
	if !slices.Equal(g8Names(rows), []string{"Sport", "Club"}) || !rows[0].CanToggle() || !rows[1].CanToggle() {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Note != "default" || rows[1].Note != "read only" {
		t.Fatalf("notes %q %q", rows[0].Note, rows[1].Note)
	}
	c.SetEnabled(false, "/c/club/")
	l.run()
	if d := g8LastUpdate(t, s).Config.Calendar.DisabledCalendars; !slices.Equal(d, []string{"/c/club/"}) {
		t.Fatalf("disabled %v", d)
	}
	if c.Rows()[1].Enabled {
		t.Fatal("still enabled")
	}
	if d := c.DefaultChoices(); len(d) != 1 || d[0].RemoteID != "/remote.php/dav/calendars/me/sport/" {
		t.Fatalf("default choices %+v", d)
	}
	c.SetKeepDays(90)
	l.run()
	if g8LastUpdate(t, s).Config.Calendar.KeepDays != 90 || c.KeepDays() != 90 {
		t.Fatal("keep days")
	}
	c.SetKeepDays(91)
	if c.KeepDays() != 90 {
		t.Fatal("kept a value that is no choice")
	}
}

func TestAccountInvitationSwitches(t *testing.T) {
	s, l := g8Samples(true)
	c := g8Account(s, l, calendar.SampleWork)
	if !c.InvitationCard() || !c.ShowUnanswered() || c.AfterAnswer() != api.AfterAnswerKeep || c.KeepDays() != api.DefaultCalendarKeepDays {
		t.Fatal("defaults")
	}
	c.SetInvitationCard(false)
	l.run()
	c.SetAfterAnswer(api.AfterAnswerArchive)
	l.run()
	c.SetShowUnanswered(false)
	l.run()
	k := g8LastUpdate(t, s).Config.Calendar
	if k.InvitationCard == nil || *k.InvitationCard || k.AfterAnswer != api.AfterAnswerArchive ||
		k.ShowUnanswered == nil || *k.ShowUnanswered {
		t.Fatalf("config %+v", k)
	}
	if c.InvitationCard() || c.ShowUnanswered() || c.AfterAnswer() != api.AfterAnswerArchive {
		t.Fatal("not shown")
	}
}

func TestAccountWithoutACalendar(t *testing.T) {
	s, l := g8Samples(true)
	s.Update(func(sn *calendardata.Snapshot) {
		sn.Accounts = append(sn.Accounts, api.Account{ID: "acc_imap", Enabled: true,
			Config: api.AccountConfig{Name: "Club", Email: "c@example.org", Kind: api.AccountIMAP}, Capabilities: api.MailCapabilities})
	})
	c := g8Account(s, l, "acc_imap")
	if !c.ShowsNoCalendar() {
		t.Fatal("has a calendar")
	}
	asked := 0
	c.OnAddCalDAV = func() { asked++ }
	c.AddCalDAV()
	if asked != 1 {
		t.Fatal("Add CalDAV Calendar…")
	}
	// Remove Account is a CalDAV account's.
	removed := 0
	c.OnRemove = func(api.AccountID) { removed++ }
	c.Remove()
	if removed != 0 {
		t.Fatal("removed an IMAP account")
	}
}

func TestAccountRefusalIsUndone(t *testing.T) {
	s, l := g8Samples(true)
	s.Failures[api.MethodAccountUpdate] = &api.Error{Code: api.CodeInvalidArgument, Message: "no"}
	c := g8Account(s, l, calendar.SampleWork)
	c.SetAfterAnswer(api.AfterAnswerArchive)
	if c.AfterAnswer() != api.AfterAnswerArchive {
		t.Fatal("not shown at once")
	}
	l.run()
	if c.AfterAnswer() != api.AfterAnswerKeep || c.Error() != "Saving the account" || c.Saving() {
		t.Fatalf("after %q error %q", c.AfterAnswer(), c.Error())
	}
	// The preview says so.
	ps, pl := g8Samples(false)
	p := g8Account(ps, pl, calendar.SampleWork)
	p.SetInvitationCard(false)
	pl.run()
	if !p.InvitationCard() || p.Error() != calendar.PreviewOnly(g8En{}) {
		t.Fatalf("preview %q", p.Error())
	}
}

func TestAccountCalDAVAccountGroup(t *testing.T) {
	s, l := g8Samples(true)
	s.ListResult = api.AccountListCalDAVCalendarsResult{PrincipalURL: "p", HomeSetURL: "h", Scheduling: api.CalDAVSchedulingClient,
		Calendars: []api.CalDAVCalendar{}}
	c := g8Account(s, l, calendar.SampleNextcloud)
	l.run()
	if c.Scheduling() != api.CalDAVSchedulingClient || c.ServerHost() != "cloud.example.com" || c.Username() != "me" {
		t.Fatalf("rows %q %q %q", c.Scheduling(), c.ServerHost(), c.Username())
	}
	if c.CertificatePinned() || c.CertificateText() != "Verified" || c.MailAccountID() != "" {
		t.Fatal("certificate and mail account")
	}
	if m := c.MailAccounts(); len(m) != 2 || m[0].ID != calendar.SampleWork || m[1].ID != calendar.SampleGmail {
		t.Fatalf("mail accounts %+v", m)
	}
	c.SetName("  Cloud  ")
	l.run()
	if p := g8LastUpdate(t, s); p.Config.Name != "Cloud" || p.Credentials != (api.Credentials{}) {
		t.Fatalf("name %+v", p)
	}
	n := len(s.Writes())
	c.SetName(" ")
	if len(s.Writes()) != n {
		t.Fatal("wrote an empty name")
	}
	c.SetMailAccount(calendar.SampleWork)
	l.run()
	if p := g8LastUpdate(t, s); p.Config.CalDAV.MailAccountID != calendar.SampleWork || p.Config.Name != "Cloud" {
		t.Fatalf("mail account %+v", p.Config)
	}
	c.ReplacePassword("new secret")
	l.run()
	pw := g8LastUpdate(t, s)
	if pw.Credentials.Password != "new secret" {
		t.Fatal("password not sent")
	}
	n = len(s.Writes())
	c.ReplacePassword("")
	if len(s.Writes()) != n {
		t.Fatal("sent an empty password")
	}
	// A pin, then Forget.
	s.Update(func(sn *calendardata.Snapshot) {
		for i := range sn.Accounts {
			if sn.Accounts[i].ID == calendar.SampleNextcloud {
				cfg := pw.Config
				d := *cfg.CalDAV
				d.CertificateSHA256 = strings.Repeat("ab", 32)
				cfg.CalDAV = &d
				sn.Accounts[i].Config = cfg
			}
		}
	})
	if !c.CertificatePinned() || c.CertificateText() != "Trusted by you" {
		t.Fatal("pin not shown")
	}
	c.ForgetCertificate()
	l.run()
	if f := g8LastUpdate(t, s); f.Config.CalDAV.CertificateSHA256 != "" || c.CertificatePinned() {
		t.Fatalf("forget %+v", f.Config.CalDAV)
	}
	removed := []api.AccountID{}
	c.OnRemove = func(id api.AccountID) { removed = append(removed, id) }
	c.Remove()
	if !slices.Equal(removed, []api.AccountID{calendar.SampleNextcloud}) {
		t.Fatalf("removed %v", removed)
	}
	c.Stop()
	if s.Observers() != 0 {
		t.Fatal("still observing")
	}
}
