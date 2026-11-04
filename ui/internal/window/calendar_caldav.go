// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Add Calendar and Contacts (CalDAV/CardDAV) sheet (docs/calendar.md
// §5.9, §6.5 G17, docs/plans/address-book.md A8;
// caldav_sheet.blp) over calendarctl.CalDAVWizardController, the port of
// CalDAVSheetController.swift and CalDAVBannerView.swift: an Adw.Dialog
// of 560×560 with Account Name, Server Address, Username and Password (the
// password goes to the daemon's keyring with account.add and is never read
// back), Discover Calendars, the error as a card as wide as the fields,
// the Certificate row with Trust Certificate… (the account assistant's
// confirmation with the fingerprint), the found calendars with checkboxes
// under the found line, the found address books (plain rows: which are
// shown is chosen in Preferences → Contacts; calendar.Wizard.AddParams
// turns the contacts on when there are any), the mail account for
// invitations, the footer
// note, Cancel / Add. The rules are the controller's; this file only
// shows them. Everything from the server is plain text.

// caldavSheet is one open sheet.
type caldavSheet struct {
	ctl    *calendarctl.CalDAVWizardController
	dialog *adw.Dialog

	cancel, add, discover, trust                 *gtk.Button
	name, server, username                       *adw.EntryRow
	password                                     *adw.PasswordEntryRow
	subtitle, note, progress, errorLabel         *gtk.Label
	serverChanged, calendarsHeading, found       *gtk.Label
	footer                                       *gtk.Label
	spinner                                      *gtk.Spinner
	errorBox, calendarsBox, booksBox             *gtk.Box
	booksHeading                                 *gtk.Label
	books                                        *gtk.ListBox
	certificateList, calendars, mailList, fields *gtk.ListBox
	certificate                                  *adw.ActionRow
	mail                                         *adw.ComboRow
	column                                       *gtk.Box
	page                                         *adw.ToolbarView

	// setting guards the handlers against the sheet's own writes.
	setting bool
	// shown are the calendars and mail accounts the rows were built for.
	shownCalendars []calendarctl.ListedCalendar
	checks         []*gtk.CheckButton
	shownMail      []calendarctl.MailAccount
	shownBooks     []api.CardDAVAddressBook
	mailBuilt      bool
}

// showAddCalDAV presents the sheet over parent (the main window, the
// Preferences); onDone runs after an account was added (may be nil).
func (w *Window) showAddCalDAV(parent gtk.Widgetter, onDone func()) {
	s := w.newCalDAVSheet(onDone)
	s.dialog.Present(parent)
	s.ctl.Start()
}

// newCalDAVSheet builds the sheet over the window's calendar source.
func (w *Window) newCalDAVSheet(onDone func()) *caldavSheet {
	ctl := calendarctl.NewCalDAVWizardController(w.calendarSource(), w.calendarOptions())
	s := &caldavSheet{ctl: ctl}
	s.bind(data.Builder("caldav_sheet.ui"))
	s.texts()
	ctl.OnChange = s.render
	ctl.OnConfirmTrust = func(req calendarctl.TrustRequest, answer func(bool)) {
		confirmCalDAVTrust(s.dialog, req, answer)
	}
	ctl.OnDone = func(api.AccountID) {
		if w.log != nil {
			w.log.Info("caldav account added")
		}
		if onDone != nil {
			onDone()
		}
		s.dialog.Close()
	}
	s.dialog.ConnectClosed(ctl.Cancel)
	s.wire()
	s.render()
	return s
}

func (s *caldavSheet) bind(b *gtk.Builder) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	s.dialog = get("caldav_sheet").(*adw.Dialog)
	s.cancel = get("caldav_cancel").(*gtk.Button)
	s.add = get("caldav_add").(*gtk.Button)
	s.discover = get("caldav_discover").(*gtk.Button)
	s.trust = get("caldav_trust").(*gtk.Button)
	s.name = get("caldav_name").(*adw.EntryRow)
	s.server = get("caldav_server").(*adw.EntryRow)
	s.username = get("caldav_username").(*adw.EntryRow)
	s.password = get("caldav_password").(*adw.PasswordEntryRow)
	s.subtitle = get("caldav_subtitle").(*gtk.Label)
	s.note = get("caldav_password_note").(*gtk.Label)
	s.progress = get("caldav_progress").(*gtk.Label)
	s.errorLabel = get("caldav_error_label").(*gtk.Label)
	s.serverChanged = get("caldav_server_changed").(*gtk.Label)
	s.calendarsHeading = get("caldav_calendars_heading").(*gtk.Label)
	s.found = get("caldav_found").(*gtk.Label)
	s.footer = get("caldav_footer").(*gtk.Label)
	s.spinner = get("caldav_spinner").(*gtk.Spinner)
	s.errorBox = get("caldav_error").(*gtk.Box)
	s.calendarsBox = get("caldav_calendars_box").(*gtk.Box)
	s.certificateList = get("caldav_certificate_list").(*gtk.ListBox)
	s.calendars = get("caldav_calendars").(*gtk.ListBox)
	s.booksBox = get("caldav_books_box").(*gtk.Box)
	s.booksHeading = get("caldav_books_heading").(*gtk.Label)
	s.books = get("caldav_books").(*gtk.ListBox)
	s.mailList = get("caldav_mail_list").(*gtk.ListBox)
	s.fields = get("caldav_fields").(*gtk.ListBox)
	s.certificate = get("caldav_certificate").(*adw.ActionRow)
	s.mail = get("caldav_mail").(*adw.ComboRow)
	s.column = get("caldav_column").(*gtk.Box)
	s.page = get("caldav_sheet_page").(*adw.ToolbarView)
}

// texts sets the fixed texts (ui/internal/calendar/caldav.go).
func (s *caldavSheet) texts() {
	tr := i18n.Tr
	s.dialog.SetTitle(contacts.WizardTitle(tr))
	s.subtitle.SetText(calendar.CalDAVSubtitle(tr))
	s.name.SetTitle(calendar.CalDAVField(calendar.CalDAVName, tr))
	s.server.SetTitle(calendar.CalDAVField(calendar.CalDAVServer, tr))
	s.server.SetTooltipText(calendar.ServerPlaceholder)
	s.username.SetTitle(calendar.CalDAVField(calendar.CalDAVUsername, tr))
	s.password.SetTitle(calendar.CalDAVField(calendar.CalDAVPassword, tr))
	s.note.SetText(calendar.PasswordNote(tr))
	s.discover.SetLabel(calendar.DiscoverButton(tr))
	s.certificate.SetTitle(calendar.CertificateLabel(tr))
	s.trust.SetLabel(calendar.TrustButton(tr))
	s.calendarsHeading.SetText(contacts.CalendarsHeading(tr))
	s.booksHeading.SetText(contacts.AddressBooksHeading(tr))
	s.mail.SetTitle(calendar.MailAccountSetting(tr))
	s.footer.SetText(calendar.CalDAVFooter(tr))
	s.cancel.SetLabel(calendar.Cancel(tr))
	s.add.SetLabel(calendar.AddButton(tr))
}

func (s *caldavSheet) wire() {
	edit := func() {
		if !s.setting {
			s.sync()
		}
	}
	for _, e := range []*adw.EntryRow{s.name, s.server, s.username, &s.password.EntryRow} {
		e.ConnectChanged(edit)
		e.ConnectEntryActivated(s.handleReturn)
	}
	s.discover.ConnectClicked(func() {
		s.sync()
		s.ctl.Discover()
	})
	s.trust.ConnectClicked(s.ctl.Trust)
	s.add.ConnectClicked(func() {
		s.sync()
		s.ctl.Add()
	})
	s.cancel.ConnectClicked(func() { s.dialog.Close() })
	s.mail.NotifyProperty("selected", func() {
		if s.setting {
			return
		}
		i := int(s.mail.Selected()) - 1
		var id api.AccountID
		if i >= 0 && i < len(s.shownMail) {
			id = s.shownMail[i].ID
		}
		s.ctl.SetMailAccount(id)
	})
}

// sync gives the controller the fields.
func (s *caldavSheet) sync() {
	s.ctl.Edit(s.name.Text(), s.server.Text(), s.username.Text(), s.password.Text())
}

// handleReturn is Enter in a field: Discover Calendars until the
// calendars are listed, then Add.
func (s *caldavSheet) handleReturn() {
	s.sync()
	w := s.ctl.Wizard()
	switch {
	case w.Phase == calendar.WizardListed && w.CanAdd():
		s.ctl.Add()
	case w.CanDiscover():
		s.ctl.Discover()
	}
}

// render shows the controller's state.
func (s *caldavSheet) render() {
	s.setting = true
	defer func() { s.setting = false }()
	tr := i18n.Tr
	w := s.ctl.Wizard()

	enabled := w.FieldsEnabled()
	for _, e := range []*adw.EntryRow{s.name, s.server, s.username, &s.password.EntryRow} {
		e.SetSensitive(enabled)
		e.RemoveCSSClass("error")
	}
	// The server's display name fills an empty Account Name.
	if s.name.Text() == "" && w.Name != "" {
		s.name.SetText(w.Name)
	}
	if w.Phase == calendar.WizardAdded {
		s.password.SetText("")
	}
	if f, ok := s.ctl.ErrorField(); ok {
		switch f {
		case calendar.CalDAVPassword:
			s.password.AddCSSClass("error")
		case calendar.CalDAVServer:
			s.server.AddCSSClass("error")
		}
	}

	s.discover.SetSensitive(w.CanDiscover())
	busy := calendar.WizardProgress(w, tr)
	s.progress.SetText(busy)
	s.spinner.SetVisible(busy != "")
	s.spinner.SetSpinning(busy != "")

	errText := s.ctl.ErrorText()
	s.errorLabel.SetText(errText)
	s.errorBox.SetVisible(errText != "")

	changed := calendar.ServerChanged(w, tr)
	s.serverChanged.SetText(changed)
	s.serverChanged.SetVisible(changed != "")

	cert := calendar.CertificateText(w, tr)
	_, offer := w.TrustOffer()
	s.certificate.SetSubtitle(cert)
	s.trust.SetVisible(offer)
	s.certificateList.SetVisible(cert != "" || offer)

	s.calendarsBox.SetVisible(w.Listed != nil)
	s.found.SetText(calendar.FoundLine(w, tr))
	s.renderCalendars(w.Phase == calendar.WizardListed)
	// A server with only address books shows no empty calendar list.
	hasCalendars := w.Listed != nil && len(w.Listed.Calendars) > 0
	s.calendarsHeading.SetVisible(hasCalendars)
	s.calendars.SetVisible(hasCalendars)
	s.renderBooks(w)

	s.mailList.SetVisible(w.ShowsMailAccount())
	s.renderMail(w)
	s.mail.SetSensitive(enabled)

	s.add.SetSensitive(w.CanAdd())
}

// renderCalendars builds the calendar rows when the found calendars
// changed, and sets their checkboxes.
func (s *caldavSheet) renderCalendars(enabled bool) {
	listed := s.ctl.ListedCalendars()
	same := len(listed) == len(s.shownCalendars) && slices.EqualFunc(listed, s.shownCalendars,
		func(a, b calendarctl.ListedCalendar) bool {
			return a.Href == b.Href && a.Name == b.Name && a.Note == b.Note
		})
	if !same {
		s.calendars.RemoveAll()
		s.checks = s.checks[:0]
		for _, c := range listed {
			s.checks = append(s.checks, s.calendarRow(c))
		}
	}
	s.shownCalendars = listed
	for i, c := range listed {
		s.checks[i].SetActive(c.Selected)
		s.checks[i].SetSensitive(enabled)
	}
}

// renderBooks lists the found address books when they changed.
func (s *caldavSheet) renderBooks(w *calendar.Wizard) {
	var books []api.CardDAVAddressBook
	if w.Listed != nil {
		books = w.Listed.AddressBooks
	}
	s.booksBox.SetVisible(len(books) > 0)
	if slices.Equal(books, s.shownBooks) {
		return
	}
	s.shownBooks = slices.Clone(books)
	s.books.RemoveAll()
	tr := i18n.Tr
	for _, b := range books {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		name := contacts.Clean(b.Name, api.MaxContactFieldBytes)
		if name == "" {
			name = contacts.WindowTitle(tr)
		}
		row.SetTitle(name) // the server's text
		if b.ReadOnly {
			l := gtk.NewLabel(contacts.ReadOnly(tr))
			l.SetUseMarkup(false)
			l.AddCSSClass("dim-label")
			row.AddSuffix(l)
		}
		s.books.Append(row)
	}
}

// calendarRow is one found calendar: a checkbox, its name, the "default"
// / "read only" note.
func (s *caldavSheet) calendarRow(c calendarctl.ListedCalendar) *gtk.CheckButton {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(c.Name) // the server's text
	check := gtk.NewCheckButton()
	check.SetVAlign(gtk.AlignCenter)
	href := c.Href
	check.ConnectToggled(func() {
		if !s.setting {
			s.ctl.Toggle(href, check.Active())
		}
	})
	row.AddPrefix(check)
	row.SetActivatableWidget(check)
	if c.Note != "" {
		l := gtk.NewLabel(c.Note)
		l.SetUseMarkup(false)
		l.AddCSSClass("dim-label")
		row.AddSuffix(l)
	}
	s.calendars.Append(row)
	return check
}

// renderMail fills the mail-account popup (None, then the accounts) and
// selects the wizard's choice.
func (s *caldavSheet) renderMail(w *calendar.Wizard) {
	accounts := s.ctl.MailAccounts()
	if !s.mailBuilt || !slices.Equal(accounts, s.shownMail) {
		names := []string{calendar.NoMailAccount(i18n.Tr)}
		for _, a := range accounts {
			names = append(names, a.Name)
		}
		s.mail.SetModel(gtk.NewStringList(names))
		s.shownMail, s.mailBuilt = accounts, true
	}
	sel := uint(0)
	if i := slices.IndexFunc(accounts, func(a calendarctl.MailAccount) bool { return a.ID == w.MailAccount }); i >= 0 && w.MailAccount != "" {
		sel = uint(i + 1)
	}
	if s.mail.Selected() != sel {
		s.mail.SetSelected(sel)
	}
}

// confirmCalDAVTrust is the account assistant's "Trust This
// Certificate?" (accountwizard/trust.go, the same msgids) for the sheet's
// server: the fingerprint first in the details, an explicit destructive
// confirmation. Only the confirmation answers; Cancel pins nothing.
func confirmCalDAVTrust(parent gtk.Widgetter, req calendarctl.TrustRequest, answer func(bool)) {
	if req.Problem.Cert == nil {
		return
	}
	tr := i18n.Tr
	servers := strings.Join(slices.DeleteFunc(slices.Clone(req.Servers), func(s string) bool { return s == "" }), ", ")
	previous := ""
	if req.Changed && req.Previous != "" {
		previous = certtrust.FormatFingerprint(req.Previous)
	}
	var heading, body string
	if req.Changed {
		heading = tr.T("Trust the New Certificate?")
		body = fmt.Sprintf(tr.T("The certificate of %s has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed."), servers)
	} else {
		heading = tr.T("Trust This Certificate?")
		body = fmt.Sprintf(tr.T("Only trust a certificate you expect, for example the one of a mail bridge on your own computer. Malachi Mail will then accept exactly this certificate for %s and no other, even if it is self-signed, issued for another name or expired."), servers)
	}
	widget.ConfirmDestructiveExtra(parent, heading, body, tr.T("_Trust"),
		accountwizard.CertificateDetails(*req.Problem.Cert, previous), func() { answer(true) })
}
