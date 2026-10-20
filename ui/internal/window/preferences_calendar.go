// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/style"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Calendar (docs/calendar.md §5.8 and §6.5 G16; the macOS
// CalendarPaneViewController and CalendarAccountGroups): the group
// Display over calendarctl.CalendarPreferencesController, then one
// section per account with the calendar capability over a
// calendarctl.CalendarAccountController, in the order of
// calendar.PreferencesLayout (or its empty state), then Add CalDAV
// Calendar…. The page follows the main window's calendar source: a new
// snapshot (accounts, calendars, states) rebuilds the sections whose
// layout changed, and each controller re-renders its own rows. Every
// text is ui/internal/calendar's. Without a source the page is hidden.

// calendarPrefs is the page's state while the dialog is open.
type calendarPrefs struct {
	d    *PreferencesDialog
	rpc  *client.Client
	src  calendardata.DataSource
	opts calendarctl.Options

	page    *adw.PreferencesPage
	display *adw.PreferencesGroup
	empty   *adw.PreferencesGroup
	add     *adw.PreferencesGroup

	weekStart, duration, alert *prefsCombo
	workStart, workEnd         *prefsCombo
	declined                   *adw.SwitchRow
	workDays                   *adw.ActionRow
	dayButtons                 []*gtk.ToggleButton

	prefs    *calendarctl.CalendarPreferencesController
	token    *board.ObserverToken
	sections []*calendarSection
	// layoutKey is what the sections were built for (prefsLayoutKey).
	layoutKey string

	syncing        bool
	displayPending bool
	layoutPending  bool
	closed         bool
	lastErr        string
}

// prefsCombo is a combo row with the titles its model has, so the model
// is replaced only when they change (never inside its own notify).
type prefsCombo struct {
	row    *adw.ComboRow
	titles []string
}

func newPrefsCombo(row *adw.ComboRow) *prefsCombo { return &prefsCombo{row: row} }

// fill sets the titles (when they changed) and selects at (-1: none).
func (c *prefsCombo) fill(titles []string, at int) {
	if !equalStrings(c.titles, titles) {
		c.titles = append([]string(nil), titles...)
		c.row.SetModel(gtk.NewStringList(c.titles))
	}
	if at < 0 || at >= len(titles) {
		c.row.SetSelected(gtk.INVALID_LIST_POSITION)
		return
	}
	c.row.SetSelected(uint(at))
}

// selected is the chosen index, -1 for none.
func (c *prefsCombo) selected() int {
	i := c.row.Selected()
	if i == gtk.INVALID_LIST_POSITION || int(i) >= len(c.titles) {
		return -1
	}
	return int(i)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// bindCalendar fills the Calendar page from b and keeps it with the main
// window's calendar source (d.cal); c removes a CalDAV account.
func (d *PreferencesDialog) bindCalendar(b *gtk.Builder, c *client.Client) (unbind func()) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	page := get("calendar_page").(*adw.PreferencesPage)
	var src calendardata.DataSource
	if d.cal != nil {
		src = d.cal()
	}
	if src == nil {
		page.SetVisible(false)
		return func() {}
	}
	p := &calendarPrefs{
		d: d, rpc: c, src: src, opts: d.calendarOptions(),
		page:      page,
		display:   get("calendar_display_group").(*adw.PreferencesGroup),
		empty:     get("calendar_empty_group").(*adw.PreferencesGroup),
		add:       get("calendar_add_group").(*adw.PreferencesGroup),
		weekStart: newPrefsCombo(get("calendar_week_start").(*adw.ComboRow)),
		duration:  newPrefsCombo(get("calendar_duration").(*adw.ComboRow)),
		alert:     newPrefsCombo(get("calendar_alert").(*adw.ComboRow)),
		workStart: newPrefsCombo(get("calendar_work_start").(*adw.ComboRow)),
		workEnd:   newPrefsCombo(get("calendar_work_end").(*adw.ComboRow)),
		declined:  get("calendar_declined").(*adw.SwitchRow),
		workDays:  get("calendar_work_days").(*adw.ActionRow),
	}
	p.build(get("calendar_empty_row").(*adw.ActionRow), get("calendar_work_days_box").(*gtk.Box),
		get("calendar_add_caldav").(*adw.ButtonRow))
	d.calendarPage = p
	return p.stop
}

// calendarOptions are the main window's calendar options, or the same
// without a window (the tests).
func (d *PreferencesDialog) calendarOptions() calendarctl.Options {
	if w := d.calendarWindow(); w != nil {
		return w.calendarOptions()
	}
	return calendarctl.Options{
		Env:  calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.Local},
		Loop: glibLoop{}, LocaleFirstDay: time.Monday, RPCText: widget.RPCErrorText,
	}
}

// calendarWindow is the main window (the application's, through the
// board's triage state), nil without one.
func (d *PreferencesDialog) calendarWindow() *Window {
	if d.assist == nil || d.assist.BoardTriage() == nil {
		return nil
	}
	return d.assist.BoardTriage().window()
}

// toast shows text on the dialog.
func (p *calendarPrefs) toast(text string) {
	if text != "" && !p.closed {
		p.d.AddToast(widget.PlainToast(text))
	}
}

func (p *calendarPrefs) build(emptyRow *adw.ActionRow, daysBox *gtk.Box, addRow *adw.ButtonRow) {
	tr := i18n.Tr
	p.page.SetTitle(calendar.TabTitle(tr))
	p.display.SetTitle(glib.MarkupEscapeText(calendar.DisplayGroup(tr)))
	p.weekStart.row.SetTitle(calendar.WeekStartSetting(tr))
	p.duration.row.SetTitle(calendar.DurationSetting(tr))
	p.alert.row.SetTitle(calendar.AlertSetting(tr))
	p.declined.SetTitle(calendar.DeclinedSetting(tr))
	p.workDays.SetTitle(calendar.WorkingHoursSetting(tr))
	p.workDays.SetSubtitle(calendar.WorkingHoursSubtitle(tr))
	// TODO(G20): own msgids for the working day's start and end; the
	// editor's field labels without their colon until then.
	p.workStart.row.SetTitle(trimLabelColon(calendar.FieldLabel(calendar.FieldStart, tr)))
	p.workEnd.row.SetTitle(trimLabelColon(calendar.FieldLabel(calendar.FieldEnd, tr)))
	emptyRow.SetTitle(calendar.NoCalendarsTitle(tr))
	emptyRow.SetSubtitle(calendar.NoCalendarsBody(tr))
	addRow.SetTitle(calendar.AddCalDAV(tr))
	addRow.ConnectActivated(p.addCalDAV)

	p.prefs = calendarctl.NewCalendarPreferencesController(p.src, p.opts)
	p.prefs.OnChange = p.scheduleDisplay
	for _, ch := range p.prefs.WorkingDayChoices() {
		code := ch.Value
		t := gtk.NewToggleButtonWithLabel(ch.Title)
		t.SetTooltipText(ch.Title)
		t.ConnectToggled(func() {
			if p.syncing || p.closed {
				return
			}
			p.prefs.SetWorkingDay(code, t.Active())
			// A refused change (the last working day) shows the stored one.
			p.scheduleDisplay()
		})
		daysBox.Append(t)
		p.dayButtons = append(p.dayButtons, t)
	}
	p.weekStart.row.NotifyProperty("selected", func() {
		if i := p.weekStart.selected(); !p.syncing && i >= 0 {
			if ch := p.prefs.WeekStartChoices(); i < len(ch) {
				p.prefs.SetWeekStart(ch[i].Value)
			}
		}
	})
	p.duration.row.NotifyProperty("selected", func() {
		if i := p.duration.selected(); !p.syncing && i >= 0 {
			if ch := p.prefs.DurationChoices(); i < len(ch) {
				p.prefs.SetDefaultDuration(ch[i].Value)
			}
		}
	})
	p.alert.row.NotifyProperty("selected", func() {
		if i := p.alert.selected(); !p.syncing && i >= 0 {
			if ch := p.prefs.AlertChoices(); i < len(ch) {
				p.prefs.SetDefaultAlert(ch[i].Value)
			}
		}
	})
	p.declined.NotifyProperty("active", func() {
		if !p.syncing {
			p.prefs.SetShowDeclined(p.declined.Active())
		}
	})
	p.workStart.row.NotifyProperty("selected", func() {
		if i := p.workStart.selected(); !p.syncing && i >= 0 {
			if ch := p.prefs.WorkingTimeChoices(p.prefs.Preferences().WorkingHours.Start); i < len(ch) {
				p.prefs.SetWorkingStart(ch[i].Value)
			}
			p.scheduleDisplay()
		}
	})
	p.workEnd.row.NotifyProperty("selected", func() {
		if i := p.workEnd.selected(); !p.syncing && i >= 0 {
			if ch := p.prefs.WorkingTimeChoices(p.prefs.Preferences().WorkingHours.End); i < len(ch) {
				p.prefs.SetWorkingEnd(ch[i].Value)
			}
			p.scheduleDisplay()
		}
	})
	p.prefs.Start()
	p.token = p.src.Observe(func(ch calendardata.Change) {
		if ch.Kind == calendardata.ChangeSnapshot {
			p.scheduleLayout()
			p.scheduleDisplay()
		}
	})
	p.renderDisplay()
	p.renderLayout()
}

// trimLabelColon is a field label without its trailing colon.
func trimLabelColon(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ":：")
}

func (p *calendarPrefs) stop() {
	p.closed = true
	if p.token != nil {
		p.token.Cancel()
		p.token = nil
	}
	if p.prefs != nil {
		p.prefs.Stop()
	}
	for _, s := range p.sections {
		s.ctl.Stop()
	}
}

// scheduleDisplay renders the Display group on the next idle: a
// controller's change arrives inside a row's own notify.
func (p *calendarPrefs) scheduleDisplay() {
	if p.displayPending || p.closed {
		return
	}
	p.displayPending = true
	glib.IdleAdd(func() {
		p.displayPending = false
		if !p.closed {
			p.renderDisplay()
		}
	})
}

func (p *calendarPrefs) scheduleLayout() {
	if p.layoutPending || p.closed {
		return
	}
	p.layoutPending = true
	glib.IdleAdd(func() {
		p.layoutPending = false
		if !p.closed {
			p.renderLayout()
		}
	})
}

func (p *calendarPrefs) renderDisplay() {
	c := p.prefs
	p.syncing = true
	defer func() { p.syncing = false }()
	phase := p.src.Snapshot().Phase
	p.display.SetSensitive(phase != calendardata.PhaseLoading && phase != calendardata.PhaseUnsupported)
	if e := c.Error(); e != p.lastErr {
		p.lastErr = e
		p.toast(e)
	}
	pr := c.Preferences()
	fillPrefsChoices(p.weekStart, c.WeekStartChoices(), pr.WeekStart)
	fillPrefsChoices(p.duration, c.DurationChoices(), pr.DefaultDurationMinutes)
	fillPrefsChoices(p.alert, c.AlertChoices(), pr.DefaultAlertMinutes)
	p.declined.SetActive(pr.ShowDeclined)
	fillPrefsChoices(p.workStart, c.WorkingTimeChoices(pr.WorkingHours.Start), pr.WorkingHours.Start)
	fillPrefsChoices(p.workEnd, c.WorkingTimeChoices(pr.WorkingHours.End), pr.WorkingHours.End)
	days := c.WorkingDayChoices()
	on := 0
	for _, ch := range days {
		if c.IsWorkingDay(ch.Value) {
			on++
		}
	}
	for i, t := range p.dayButtons {
		if i >= len(days) {
			break
		}
		active := c.IsWorkingDay(days[i].Value)
		t.SetActive(active)
		// The last working day stays on.
		t.SetSensitive(!(active && on <= 1))
	}
}

// fillPrefsChoices fills combo with choices and selects v.
func fillPrefsChoices[T comparable](combo *prefsCombo, choices []calendarctl.PreferenceChoice[T], v T) {
	titles := make([]string, len(choices))
	at := -1
	for i, ch := range choices {
		titles[i] = ch.Title
		if ch.Value == v {
			at = i
		}
	}
	combo.fill(titles, at)
}

// prefsLayoutKey is what decides the sections: their accounts, kinds and
// rows.
func prefsLayoutKey(layout []calendar.PrefsSection) string {
	var b strings.Builder
	for _, s := range layout {
		fmt.Fprintf(&b, "%d|%s|%s|%v;", s.Kind, s.AccountID, s.AccountKind, s.Rows)
	}
	return b.String()
}

// renderLayout rebuilds the account sections when the layout changed.
func (p *calendarPrefs) renderLayout() {
	layout := calendar.PreferencesLayout(p.src.Snapshot().Accounts)
	key := prefsLayoutKey(layout)
	if key == p.layoutKey {
		return
	}
	p.layoutKey = key
	for _, s := range p.sections {
		s.ctl.Stop()
		p.page.Remove(s.group)
		p.page.Remove(s.invitations)
	}
	p.sections = nil
	// Adw.PreferencesPage only appends: the empty state and Add go again
	// after the sections.
	p.page.Remove(p.empty)
	p.page.Remove(p.add)
	empty := false
	for _, s := range layout {
		switch s.Kind {
		case calendar.SectionAccount:
			sec := newCalendarSection(p, s)
			p.sections = append(p.sections, sec)
			p.page.Add(sec.group)
			p.page.Add(sec.invitations)
		case calendar.SectionEmpty:
			empty = true
		}
	}
	p.empty.SetVisible(empty)
	p.page.Add(p.empty)
	p.page.Add(p.add)
	for _, s := range p.sections {
		s.ctl.Start()
		s.render()
	}
}

// addCalDAV opens the Add CalDAV sheet over the dialog; the source is
// asked again when an account was added.
func (p *calendarPrefs) addCalDAV() {
	w := p.d.calendarWindow()
	if w == nil {
		p.toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	w.showAddCalDAV(p.d, func() {
		if !p.closed {
			p.src.Refresh()
			p.reloadAccountPages()
		}
	})
}

// reloadAccountPages lists the accounts of the Accounts and Integrations
// pages again after this page added or removed a CalDAV account.
func (p *calendarPrefs) reloadAccountPages() {
	if p.d.rpc != nil {
		p.d.loadAccounts(p.d.rpc)
	}
}

// signIn is a consent row's Sign In…: the main window's browser sign-in
// with the calendar scopes.
func (p *calendarPrefs) signIn(id api.AccountID) {
	w := p.d.calendarWindow()
	if w == nil {
		p.toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	w.calendarSignIn(id)
}

// removeAccount asks as the Accounts page does, then account.remove; the
// source is asked again afterwards.
func (p *calendarPrefs) removeAccount(id api.AccountID) {
	a, ok := p.src.Snapshot().Account(id)
	if !ok {
		return
	}
	if p.rpc == nil {
		p.toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	check := gtk.NewCheckButtonWithMnemonic(i18n.T("Also delete _drafts and downloaded data"))
	check.SetActive(true)
	body := integrations.RowFor(a, i18n.Tr).RemoveBody
	widget.ConfirmDestructiveExtra(p.d, i18n.T("Remove this account?"), body, i18n.T("_Remove"), check, func() {
		deleteLocal := check.Active()
		c := p.rpc
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			err := c.Call(ctx, api.MethodAccountRemove,
				api.AccountRemoveParams{AccountID: id, DeleteLocalData: deleteLocal}, &api.AccountRemoveResult{})
			glib.IdleAdd(func() {
				if p.closed {
					return
				}
				if err != nil {
					p.toast(widget.RPCErrorText(i18n.T("Removing the account"), err))
					return
				}
				p.src.Refresh()
				p.reloadAccountPages()
			})
		}()
	})
}

// ask is a controller's question as an Adw.AlertDialog over the dialog.
func (p *calendarPrefs) ask(q calendar.Question, answer func(calendarctl.Answer)) {
	a := adw.NewAlertDialog(q.Heading, q.Body)
	a.SetHeadingUseMarkup(false)
	a.SetBodyUseMarkup(false)
	a.AddResponse("cancel", i18n.T("_Cancel"))
	if q.Alternative != "" {
		a.AddResponse("alternative", q.Alternative)
	}
	a.AddResponse("confirm", q.Confirm)
	a.SetResponseAppearance("confirm", adw.ResponseSuggested)
	a.SetDefaultResponse("confirm")
	a.SetCloseResponse("cancel")
	a.ConnectResponse(func(r string) {
		switch r {
		case "confirm":
			answer(calendarctl.AnswerConfirm)
		case "alternative":
			answer(calendarctl.AnswerAlternative)
		default:
			answer(calendarctl.AnswerCancel)
		}
	})
	a.Present(p.d)
}

// calendarSection is one account's groups: the account's (its CalDAV
// rows, its Calendar rows, Remove Account) and its Invitations group.
type calendarSection struct {
	p      *calendarPrefs
	layout calendar.PrefsSection
	ctl    *calendarctl.CalendarAccountController

	group, invitations *adw.PreferencesGroup
	badge              *gtk.Label

	server, username, password, certificate *adw.ActionRow
	forget                                  *gtk.Button
	mail                                    *prefsCombo
	mailIDs                                 []api.AccountID
	sync                                    *adw.SwitchRow
	consent                                 *adw.ActionRow
	defaultCal, keep                        *prefsCombo
	remove                                  *adw.ButtonRow
	card, tentative                         *adw.SwitchRow
	after                                   *prefsCombo

	// shown are the group's rows now; inviteShown the Invitations group's.
	shown, inviteShown []gtk.Widgetter
	// rowTitles are the shown rows' titles in order (LAYOUT lines).
	rowTitles []string
	shape     string
	calRows   []calendarctl.CalendarRow
	defaults  []calendarctl.CalendarRow
	syncing   bool
	pending   bool
	lastErr   string
	// calWidgets are the calendar rows shown, in s.calRows' order.
	calWidgets []*prefsCalendarRow
	// rebuildTimer is the pending coalesced rebuild (0 none).
	rebuildTimer glib.SourceHandle
	forceRebuild bool
}

// prefsRebuildDelay coalesces structural rebuilds of a section.
const prefsRebuildDelay = 300 // milliseconds

// scheduleRebuild rebuilds the section after prefsRebuildDelay (again
// later while a pointer button is down on the dialog).
func (s *calendarSection) scheduleRebuild() {
	if s.rebuildTimer != 0 || s.p.closed {
		return
	}
	s.rebuildTimer = glib.TimeoutAdd(prefsRebuildDelay, func() bool {
		if s.p.closed {
			s.rebuildTimer = 0
			return false
		}
		if s.p.pointerDown() {
			return true
		}
		s.rebuildTimer = 0
		s.shape = "\x00" // differs from every key: render rebuilds now
		s.forceRebuild = true
		s.render()
		return false
	})
}

// pointerDown reports a pointer button held down (a press whose release
// has not come yet): a rebuild then would destroy the pressed widget.
func (p *calendarPrefs) pointerDown() bool {
	d := gdk.DisplayGetDefault()
	if d == nil || d.DefaultSeat() == nil {
		return false
	}
	ptr := gdk.BaseSeat(d.DefaultSeat()).Pointer()
	if ptr == nil {
		return false
	}
	const buttons = gdk.Button1Mask | gdk.Button2Mask | gdk.Button3Mask
	return gdk.BaseDevice(ptr).ModifierState()&buttons != 0
}

// prefsCalendarRow is one calendar's row and the widgets updated in place.
type prefsCalendarRow struct {
	row   *adw.ActionRow
	check *gtk.CheckButton
	note  *gtk.Label
}

func (r *prefsCalendarRow) update(cr calendarctl.CalendarRow) {
	if r.check.Active() != cr.Enabled {
		r.check.SetActive(cr.Enabled)
	}
	r.check.SetSensitive(cr.CanToggle())
	if r.note.Text() != cr.Note {
		r.note.SetText(cr.Note)
	}
	r.note.SetVisible(cr.Note != "")
}

func newCalendarSection(p *calendarPrefs, layout calendar.PrefsSection) *calendarSection {
	tr := i18n.Tr
	s := &calendarSection{p: p, layout: layout,
		ctl:         calendarctl.NewCalendarAccountController(p.src, layout.AccountID, p.opts),
		group:       adw.NewPreferencesGroup(),
		invitations: adw.NewPreferencesGroup(),
	}
	s.badge = widget.NewKindBadge("")
	s.group.SetHeaderSuffix(s.badge)
	s.invitations.SetTitle(glib.MarkupEscapeText(calendar.InvitationsGroup(tr)))

	property := func(title string) *adw.ActionRow {
		r := adw.NewActionRow()
		r.SetUseMarkup(false)
		r.SetTitle(title)
		r.AddCSSClass("property")
		r.SetSubtitleSelectable(true)
		return r
	}
	s.server = property(calendar.CalDAVField(calendar.CalDAVServer, tr))
	s.username = property(calendar.CalDAVField(calendar.CalDAVUsername, tr))
	s.password = adw.NewActionRow()
	s.password.SetUseMarkup(false)
	s.password.SetTitle(calendar.CalDAVField(calendar.CalDAVPassword, tr))
	s.password.SetSubtitle(calendar.PasswordNote(tr))
	replace := gtk.NewButtonWithLabel(calendar.ReplacePassword(tr))
	replace.SetVAlign(gtk.AlignCenter)
	replace.ConnectClicked(s.replacePassword)
	s.password.AddSuffix(replace)
	s.mail = newPrefsCombo(adw.NewComboRow())
	s.mail.row.SetUseMarkup(false)
	s.mail.row.SetTitle(calendar.MailAccountSetting(tr))
	s.mail.row.NotifyProperty("selected", func() {
		if i := s.mail.selected(); !s.syncing && i >= 0 {
			var id api.AccountID
			if i > 0 && i-1 < len(s.mailIDs) {
				id = s.mailIDs[i-1]
			}
			s.ctl.SetMailAccount(id)
		}
	})
	s.certificate = adw.NewActionRow()
	s.certificate.SetUseMarkup(false)
	s.certificate.SetTitle(calendar.CertificateLabel(tr))
	s.forget = gtk.NewButtonWithMnemonic(i18n.T("_Forget"))
	s.forget.SetVAlign(gtk.AlignCenter)
	s.forget.ConnectClicked(func() { s.ctl.ForgetCertificate() })
	s.certificate.AddSuffix(s.forget)

	s.sync = adw.NewSwitchRow()
	s.sync.SetUseMarkup(false)
	s.sync.SetTitle(calendar.SyncSetting(tr))
	s.sync.NotifyProperty("active", func() {
		if !s.syncing {
			s.ctl.SetSync(s.sync.Active())
		}
	})
	s.consent = adw.NewActionRow()
	s.consent.SetUseMarkup(false)
	s.consent.SetTitle(calendar.ConsentLine(tr))
	s.consent.SetSubtitle(calendar.SyncConsentNote(tr))
	signIn := gtk.NewButtonWithLabel(calendar.SignIn(tr))
	signIn.SetVAlign(gtk.AlignCenter)
	signIn.AddCSSClass("suggested-action")
	signIn.ConnectClicked(func() { s.ctl.SignIn() })
	s.consent.AddSuffix(signIn)

	s.defaultCal = newPrefsCombo(adw.NewComboRow())
	s.defaultCal.row.SetUseMarkup(false)
	s.defaultCal.row.SetTitle(calendar.DefaultCalendarSetting(tr))
	s.defaultCal.row.NotifyProperty("selected", func() {
		if i := s.defaultCal.selected(); !s.syncing && i >= 0 && i < len(s.defaults) {
			s.ctl.SetDefaultCalendar(s.defaults[i].RemoteID)
		}
	})
	s.keep = newPrefsCombo(adw.NewComboRow())
	s.keep.row.SetUseMarkup(false)
	s.keep.row.SetTitle(calendar.KeepSetting(tr))
	s.keep.row.NotifyProperty("selected", func() {
		if i := s.keep.selected(); !s.syncing && i >= 0 && i < len(s.ctl.KeepChoices()) {
			s.ctl.SetKeepDays(s.ctl.KeepChoices()[i])
		}
	})
	s.remove = adw.NewButtonRow()
	s.remove.SetUseMarkup(false)
	s.remove.SetTitle(calendar.RemoveAccount(tr))
	s.remove.AddCSSClass("destructive-action")
	s.remove.ConnectActivated(func() { s.ctl.Remove() })

	s.card = adw.NewSwitchRow()
	s.card.SetUseMarkup(false)
	s.card.SetTitle(calendar.CardSetting(tr))
	s.card.NotifyProperty("active", func() {
		if !s.syncing {
			s.ctl.SetInvitationCard(s.card.Active())
		}
	})
	s.tentative = adw.NewSwitchRow()
	s.tentative.SetUseMarkup(false)
	s.tentative.SetTitle(calendar.TentativeSetting(tr))
	s.tentative.SetSubtitle(calendar.TentativeSubtitle(tr))
	s.tentative.NotifyProperty("active", func() {
		if !s.syncing {
			s.ctl.SetShowUnanswered(s.tentative.Active())
		}
	})
	s.after = newPrefsCombo(adw.NewComboRow())
	s.after.row.SetUseMarkup(false)
	s.after.row.SetTitle(calendar.AfterAnswerSetting(tr))
	s.after.row.NotifyProperty("selected", func() {
		if i := s.after.selected(); !s.syncing && i >= 0 && i < len(calendar.AfterAnswers) {
			s.ctl.SetAfterAnswer(calendar.AfterAnswers[i])
		}
	})
	keep := make([]string, 0, len(calendar.KeepChoices))
	for _, d := range s.ctl.KeepChoices() {
		keep = append(keep, calendar.KeepTitle(d, tr))
	}
	// A new model selects its first item: never a write.
	s.syncing = true
	s.keep.fill(keep, -1)
	after := make([]string, 0, len(calendar.AfterAnswers))
	for _, a := range calendar.AfterAnswers {
		after = append(after, calendar.AfterAnswerTitle(a, tr))
	}
	s.after.fill(after, -1)
	s.syncing = false

	s.ctl.OnChange = s.schedule
	s.ctl.OnQuestion = p.ask
	s.ctl.OnSignIn = p.signIn
	s.ctl.OnRemove = p.removeAccount
	s.ctl.OnAddCalDAV = p.addCalDAV
	return s
}

func (s *calendarSection) schedule() {
	if s.pending || s.p.closed {
		return
	}
	s.pending = true
	glib.IdleAdd(func() {
		s.pending = false
		if !s.p.closed {
			s.render()
		}
	})
}

// render shows the controller's state: the rows again when their shape
// changed, the values always.
func (s *calendarSection) render() {
	c := s.ctl
	s.syncing = true
	defer func() { s.syncing = false }()
	a, loaded := c.Account()
	s.group.SetVisible(loaded)
	if !loaded {
		s.invitations.SetVisible(false)
		return
	}
	s.group.SetTitle(glib.MarkupEscapeText(accountRowTitle(a)))
	badge := accountHeaderBadge(a)
	if c.IsCalDAV() {
		badge = caldavBadge
	}
	s.badge.SetText(badge)
	s.badge.SetVisible(badge != "")
	// A failed write is a toast, once; never the group's description.
	if e := c.Error(); e != s.lastErr {
		s.lastErr = e
		s.p.toast(e)
	}

	var rows, defaults []calendarctl.CalendarRow
	if c.SyncOn() {
		rows, defaults = c.Rows(), c.DefaultChoices()
	}
	consent := c.IsGraph() && c.SyncOn() && c.ConsentRequired()
	mails := c.MailAccounts()
	var shape strings.Builder
	// The structure only: the rows that exist and what each is. A state
	// or value change (checkbox, note, error) is applied in place.
	fmt.Fprintf(&shape, "%v|%v|%v|%v\x1e", c.IsCalDAV(), c.ShowsSyncSwitch(), c.SyncOn(), consent)
	for _, r := range rows {
		fmt.Fprintf(&shape, "%s\x1f%s\x1f%s\x1f%v\x1e", r.RemoteID, r.Name, r.Color, r.CanToggle())
	}
	for _, r := range defaults {
		fmt.Fprintf(&shape, "d%s\x1f%s\x1e", r.RemoteID, r.Name)
	}
	for _, m := range mails {
		fmt.Fprintf(&shape, "m%s\x1f%s\x1e", m.ID, m.Name)
	}
	if key := shape.String(); key != s.shape && s.shape != "" && !s.forceRebuild {
		// Coalesced, and never under a pressed button: the snapshot may
		// change many times a second (sync retries).
		s.scheduleRebuild()
	} else if key != s.shape {
		s.forceRebuild = false
		s.shape = key
		s.calRows, s.defaults = rows, defaults
		s.rebuild(consent)
		titles := []string{calendar.NoMailAccount(i18n.Tr)}
		s.mailIDs = s.mailIDs[:0]
		for _, m := range mails {
			titles = append(titles, m.Name)
			s.mailIDs = append(s.mailIDs, m.ID)
		}
		s.mail.titles = nil // the model is new with every shape
		s.mail.fill(titles, -1)
		dt := make([]string, len(defaults))
		for i, r := range defaults {
			dt[i] = r.Name
		}
		s.defaultCal.fill(dt, -1)
	}

	if s.rebuildTimer == 0 && len(rows) == len(s.calWidgets) {
		for i, r := range rows {
			s.calWidgets[i].update(r)
		}
	}
	s.sync.SetActive(c.SyncOn())
	def := -1
	for i, r := range s.defaults {
		if r.IsDefault {
			def = i
		}
	}
	s.defaultCal.fill(s.defaultCal.titles, def)
	keepAt := -1
	for i, d := range c.KeepChoices() {
		if d == c.KeepDays() {
			keepAt = i
		}
	}
	s.keep.fill(s.keep.titles, keepAt)
	s.card.SetActive(c.InvitationCard())
	s.tentative.SetActive(c.ShowUnanswered())
	afterAt := 0
	for i, x := range calendar.AfterAnswers {
		if x == c.AfterAnswer() {
			afterAt = i
		}
	}
	s.after.fill(s.after.titles, afterAt)
	if c.IsCalDAV() {
		s.server.SetSubtitle(c.ServerHost())
		s.username.SetSubtitle(c.Username())
		s.certificate.SetSubtitle(c.CertificateText())
		s.forget.SetVisible(c.CertificatePinned())
		mailAt := 0
		for i, id := range s.mailIDs {
			if id == c.MailAccountID() {
				mailAt = i + 1
			}
		}
		s.mail.fill(s.mail.titles, mailAt)
	}
}

// rebuild puts the rows into the groups in calendar.PreferencesLayout's
// order, as far as the controller shows them now.
func (s *calendarSection) rebuild(consent bool) {
	for _, w := range s.shown {
		s.group.Remove(w)
	}
	for _, w := range s.inviteShown {
		s.invitations.Remove(w)
	}
	s.shown, s.inviteShown, s.rowTitles, s.calWidgets = nil, nil, nil, nil
	c := s.ctl
	put := func(w gtk.Widgetter, title string) {
		s.group.Add(w)
		s.shown = append(s.shown, w)
		s.rowTitles = append(s.rowTitles, title)
	}
	invite := func(w gtk.Widgetter) {
		s.invitations.Add(w)
		s.inviteShown = append(s.inviteShown, w)
	}
	for _, r := range s.layout.Rows {
		switch r {
		case calendar.RowServer:
			if c.IsCalDAV() {
				put(s.server, s.server.Title())
			}
		case calendar.RowUsername:
			if c.IsCalDAV() {
				put(s.username, s.username.Title())
			}
		case calendar.RowReplacePassword:
			if c.IsCalDAV() {
				put(s.password, s.password.Title())
			}
		case calendar.RowMailAccount:
			if c.IsCalDAV() {
				put(s.mail.row, s.mail.row.Title())
			}
		case calendar.RowCertificate:
			if c.IsCalDAV() {
				put(s.certificate, s.certificate.Title())
			}
		case calendar.RowSync:
			if c.ShowsSyncSwitch() {
				put(s.sync, s.sync.Title())
			}
			if consent {
				put(s.consent, s.consent.Title())
			}
		case calendar.RowCalendars:
			for i, cr := range s.calRows {
				w := s.calendarRow(i, cr)
				s.calWidgets = append(s.calWidgets, w)
				put(w.row, "• "+cr.Name)
			}
		case calendar.RowDefaultCalendar:
			if c.SyncOn() && len(s.defaults) > 0 {
				put(s.defaultCal.row, s.defaultCal.row.Title())
			}
		case calendar.RowKeep:
			if c.SyncOn() {
				put(s.keep.row, s.keep.row.Title())
			}
		case calendar.RowRemove:
			if c.IsCalDAV() {
				put(s.remove, s.remove.Title())
			}
		case calendar.RowCard:
			invite(s.card)
		case calendar.RowTentative:
			invite(s.tentative)
		case calendar.RowAfterAnswer:
			invite(s.after.row)
		}
	}
	s.invitations.SetVisible(len(s.inviteShown) > 0)
}

// calendarRow is one calendar: its checkbox and colour square before the
// name, the note after it.
func (s *calendarSection) calendarRow(i int, cr calendarctl.CalendarRow) *prefsCalendarRow {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(cr.Name)
	check := gtk.NewCheckButton()
	check.SetActive(cr.Enabled)
	check.SetSensitive(cr.CanToggle())
	check.SetVAlign(gtk.AlignCenter)
	remoteID := cr.RemoteID
	check.ConnectToggled(func() {
		if !s.syncing {
			s.ctl.SetEnabled(check.Active(), remoteID)
		}
	})
	row.AddPrefix(check)
	row.AddPrefix(prefsSwatch(style.CalendarColor(cr.Color, i)))
	if cr.CanToggle() {
		row.SetActivatableWidget(check)
	}
	note := gtk.NewLabel(cr.Note)
	note.SetUseMarkup(false)
	note.SetEllipsize(pango.EllipsizeEnd)
	note.AddCSSClass("dim-label")
	note.AddCSSClass("caption")
	note.SetVisible(cr.Note != "")
	row.AddSuffix(note)
	return &prefsCalendarRow{row: row, check: check, note: note}
}

// prefsSwatch is a 12 px rounded square of the colour hex ("#rrggbb").
func prefsSwatch(hex string) *gtk.DrawingArea {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		v = 0x888888
	}
	r, g, b := float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	da := gtk.NewDrawingArea()
	da.SetContentWidth(12)
	da.SetContentHeight(12)
	da.SetVAlign(gtk.AlignCenter)
	da.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		const rad = 3.0
		fw, fh := float64(w), float64(h)
		cr.NewSubPath()
		cr.Arc(fw-rad, rad, rad, -1.5708, 0)
		cr.Arc(fw-rad, fh-rad, rad, 0, 1.5708)
		cr.Arc(rad, fh-rad, rad, 1.5708, 3.14159)
		cr.Arc(rad, rad, rad, 3.14159, 4.71239)
		cr.ClosePath()
		cr.SetSourceRGB(r, g, b)
		cr.Fill()
	})
	return da
}

// replacePassword asks for a CalDAV account's new password in an
// Adw.AlertDialog with an Adw.PasswordEntryRow; it goes to the daemon's
// keyring through account.update.
func (s *calendarSection) replacePassword() {
	tr := i18n.Tr
	a := adw.NewAlertDialog(calendar.ReplacePasswordTitle(tr), calendar.PasswordNote(tr))
	a.SetHeadingUseMarkup(false)
	a.SetBodyUseMarkup(false)
	entry := adw.NewPasswordEntryRow()
	entry.SetTitle(calendar.CalDAVField(calendar.CalDAVPassword, tr))
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(entry)
	a.SetExtraChild(list)
	a.AddResponse("cancel", i18n.T("_Cancel"))
	a.AddResponse("replace", i18n.T("Replace"))
	a.SetResponseAppearance("replace", adw.ResponseSuggested)
	a.SetResponseEnabled("replace", false)
	a.SetDefaultResponse("replace")
	a.SetCloseResponse("cancel")
	entry.ConnectChanged(func() { a.SetResponseEnabled("replace", entry.Text() != "") })
	entry.ConnectEntryActivated(func() {
		if entry.Text() != "" {
			s.ctl.ReplacePassword(entry.Text())
			entry.SetText("")
			a.ForceClose()
		}
	})
	a.ConnectResponse(func(r string) {
		if r == "replace" && entry.Text() != "" {
			s.ctl.ReplacePassword(entry.Text())
		}
		entry.SetText("")
	})
	a.Present(s.p.d)
	entry.GrabFocus()
}

// calendarLayoutLines describe the page for the smoke test: the Display
// group's rows, then each section with its rows.
func (p *calendarPrefs) calendarLayoutLines() []string {
	out := []string{"display: " + strings.Join([]string{
		p.weekStart.row.Title(), p.duration.row.Title(), p.alert.row.Title(), p.declined.Title(),
		p.workDays.Title(), p.workStart.row.Title(), p.workEnd.row.Title(),
	}, " | ")}
	for _, s := range p.sections {
		line := fmt.Sprintf("section %s [%s] %s: %s", s.layout.AccountID, s.badge.Text(), s.ctl.Name(), strings.Join(s.rowTitles, " | "))
		if s.invitations.Visible() {
			line += fmt.Sprintf(" ; invitations: %d rows", len(s.inviteShown))
		}
		out = append(out, line)
	}
	if e := p.prefs.Error(); e != "" {
		out = append(out, "display error: "+e)
	}
	for _, s := range p.sections {
		if e := s.ctl.Error(); e != "" || s.group.Description() != "" {
			out = append(out, fmt.Sprintf("section %s error: %q %q", s.layout.AccountID, e, s.group.Description()))
		}
	}
	if p.empty.Visible() {
		out = append(out, "empty")
	}
	return out
}
