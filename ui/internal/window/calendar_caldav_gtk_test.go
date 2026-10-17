// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run '^TestCalDAVSheetGTKSmoke$' -count=1 -v. The
// sheet (G17) over the in-memory sample source whose writes are emulated:
// Discover answers an invented CalDAV server with four calendars, then a
// tlsError with an invented certificate; no daemon, account or keyring.
// Logs LAYOUT: lines for the empty, the discovered and the error phase.
func TestCalDAVSheetGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	wb := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: wb.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		modeSwitchMail:    wb.GetObject("mode_switch").Cast().(*adw.ToggleGroup),
		log:               slog.Default(), mode: board.ModeCalendar,
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        adw.NewToastOverlay(),
		dateWeekStart: time.Monday,
	}
	defer w.Destroy()
	src := calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
	src.EmulatesWrites = true
	src.DiscoverResult = api.AccountDiscoverCalDAVResult{
		ServerURL: "https://cloud.example.com/remote.php/dav", FoundVia: api.CalDAVFoundWellKnown,
		NeedsCredentials: true, Certificate: &api.CertificateInfo{SHA256: strings.Repeat("ab", 32), Issuer: "R11"},
	}
	src.ListResult = api.AccountListCalDAVCalendarsResult{
		DisplayName: "Nextcloud", Scheduling: api.CalDAVSchedulingClient, Addresses: []string{"me@example.com"},
		Calendars: []api.CalDAVCalendar{
			{Href: "/cal/personal/", Name: "Personal", Color: "#3584e4", Access: api.CalendarAccessWrite, IsDefault: true},
			{Href: "/cal/sport/", Name: "Sport", Color: "#2ec27e", Access: api.CalendarAccessWrite},
			{Href: "/cal/family/", Name: "Family with a rather long name that has to fit the row", Access: api.CalendarAccessWrite},
			{Href: "/cal/holidays/", Name: "Holidays", Access: api.CalendarAccessRead},
		},
	}
	src.AddedAccount = "acc-caldav"
	w.cal.source = src
	w.SetDefaultSize(1000, 800)
	w.Present()

	ctx := glib.MainContextDefault()
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			for i := 0; i < 100 && ctx.Pending(); i++ {
				ctx.Iteration(false)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	open := func() *caldavSheet {
		s := w.newCalDAVSheet(nil)
		s.dialog.Present(w)
		s.ctl.Start()
		wait(400 * time.Millisecond)
		return s
	}
	layout := func(step string, s *caldavSheet) {
		t.Logf("LAYOUT: %s sheet=%dx%d column=%d fields=%d error=%d(visible %v) found=%q certificate=%q trust=%v calendars=%d mail=%v discover=%v add=%v",
			step, s.page.Width(), s.page.Height(), s.column.Width(), s.fields.Width(), s.errorBox.Width(), s.errorBox.Visible(),
			s.found.Text(), s.certificate.Subtitle(), s.trust.Visible(), len(s.checks), s.mailList.Visible(),
			s.discover.Sensitive(), s.add.Sensitive())
	}
	fill := func(s *caldavSheet) {
		s.server.SetText("cloud.example.com")
		s.username.SetText("me")
		s.password.SetText("not-a-real-password")
		wait(100 * time.Millisecond)
	}

	// Empty.
	s := open()
	layout("empty", s)
	if s.dialog.Title() == "" || s.discover.Label() == "" || s.add.Label() == "" {
		t.Error("the sheet has no title or button texts")
	}
	if s.discover.Sensitive() || s.add.Sensitive() || s.calendarsBox.Visible() || s.errorBox.Visible() {
		t.Error("an empty sheet offers Discover or Add, or shows calendars or an error")
	}
	if got := s.page.Width(); got != 560 {
		t.Errorf("the sheet is %d px wide, want 560", got)
	}

	// Discovered with calendars.
	fill(s)
	if !s.discover.Sensitive() {
		t.Fatal("Discover Calendars stays insensitive with the fields filled")
	}
	s.discover.Emit("clicked")
	wait(400 * time.Millisecond)
	layout("discovered", s)
	if s.ctl.Wizard().Phase != calendar.WizardListed || len(s.checks) != 4 {
		t.Fatalf("not listed: phase %d, %d rows", s.ctl.Wizard().Phase, len(s.checks))
	}
	if s.found.Text() == "" || !strings.Contains(s.certificate.Subtitle(), "R11") || !s.mailList.Visible() || !s.add.Sensitive() {
		t.Error("the found line, the certificate, the mail account or Add is missing")
	}
	if s.name.Text() != "Nextcloud" {
		t.Errorf("Account Name = %q, want the server's display name", s.name.Text())
	}
	s.checks[0].SetActive(false)
	s.checks[1].SetActive(false)
	s.checks[2].SetActive(false)
	s.checks[3].SetActive(false)
	wait(50 * time.Millisecond)
	if s.add.Sensitive() {
		t.Error("Add stays sensitive with no calendar selected")
	}
	s.checks[1].SetActive(true)
	wait(50 * time.Millisecond)
	var added bool
	s.ctl.OnDone = func(api.AccountID) { added = true; s.dialog.Close() }
	s.add.Emit("clicked")
	wait(400 * time.Millisecond)
	if !added {
		t.Error("Add did not add the account")
	}

	// The error banner: a tlsError with a certificate to trust.
	cert := &api.CertificateInfo{SHA256: strings.Repeat("cd", 32), Subject: "CN=cloud.example.com", Issuer: "CN=cloud.example.com", SelfSigned: true}
	src.Failures = map[string]*api.Error{api.MethodAccountDiscoverCalDAV: {Code: api.CodeTLSError, Message: "untrusted",
		Data: api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: cert}}}
	s = open()
	fill(s)
	s.discover.Emit("clicked")
	wait(400 * time.Millisecond)
	layout("error", s)
	if !s.errorBox.Visible() || s.errorLabel.Text() == "" || !s.trust.Visible() {
		t.Fatal("the tlsError shows no error or no Trust Certificate…")
	}
	if !s.server.HasCSSClass("error") || s.password.HasCSSClass("error") {
		t.Error("the tlsError does not mark the server field")
	}
	if s.errorBox.Width() != s.fields.Width() {
		t.Errorf("the error is %d px wide, the fields %d", s.errorBox.Width(), s.fields.Width())
	}
	// A long error wraps inside the card's width.
	s.errorLabel.SetText(strings.Repeat(s.errorLabel.Text()+" ", 6))
	wait(100 * time.Millisecond)
	t.Logf("LAYOUT: error-long error=%dx%d fields=%d", s.errorBox.Width(), s.errorBox.Height(), s.fields.Width())
	if s.errorBox.Width() != s.fields.Width() {
		t.Errorf("a long error widens the card to %d px (fields %d)", s.errorBox.Width(), s.fields.Width())
	}
	// Trust: the confirmation (scripted) pins and discovers again.
	var asked calendarctl.TrustRequest
	s.ctl.OnConfirmTrust = func(req calendarctl.TrustRequest, answer func(bool)) {
		asked = req
		delete(src.Failures, api.MethodAccountDiscoverCalDAV)
		answer(true)
	}
	s.trust.Emit("clicked")
	wait(400 * time.Millisecond)
	layout("trusted", s)
	if asked.Problem.Cert == nil || asked.Problem.Cert.SHA256 != cert.SHA256 || len(asked.Servers) != 1 || asked.Servers[0] != "cloud.example.com" {
		t.Errorf("the confirmation asked %+v", asked)
	}
	if s.errorBox.Visible() || s.certificate.Subtitle() == "" {
		t.Error("the trusted certificate did not clear the error")
	}
	if s.ctl.Wizard().Pin != cert.SHA256 || s.ctl.Wizard().Phase != calendar.WizardListed {
		t.Errorf("after Trust: pin %q, phase %d", s.ctl.Wizard().Pin, s.ctl.Wizard().Phase)
	}
	// The real confirmation is the account assistant's alert.
	confirmCalDAVTrust(s.dialog, asked, func(bool) {})
	wait(200 * time.Millisecond)
	s.dialog.ForceClose()
	wait(100 * time.Millisecond)
}
