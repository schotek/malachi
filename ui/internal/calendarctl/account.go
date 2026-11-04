// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"reflect"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// An account's section of Preferences → Calendar (docs/calendar.md §5.8),
// the port of CalendarAccountController.swift, over account.update and
// calendar.list: Sync This Account's Calendars (a Graph account;
// switching on when the grant lacks the calendar scopes ends in state
// consentRequired, and the controller then asks with
// calendar.SyncConsentNote and hands the account to the re-sign-in with
// calendar: true through OnSignIn — the window calls account.oauthStart
// {accountId, calendar: true} and opens the page in the browser), the
// calendars with their checkboxes (disabledCalendars), the default
// calendar for new events, Keep Events, and the invitation switches with
// After Answering. Each change is one account.update of the whole
// configuration with empty credentials (the keyring keeps the secret).
//
// The checkboxes and the default are keyed by the calendars' remote ids
// (api.Calendar.RemoteID; calendar.list lists the disabled calendars
// too), for Graph and CalDAV alike; a CalDAV account also lists the
// server's calendars (account.listCalDAVCalendars with accountId) so that
// one the daemon does not store yet can be switched on. A CalDAV
// account's connection rows (name, server, username, Replace Password…,
// mail account for invitations, certificate with Forget) are here too;
// Remove Account is the Accounts page's flow (OnRemove).

// CalendarRow is a calendar row of an account's section.
type CalendarRow struct {
	// RemoteID is the remote id (CalDAV href); "" when none is known.
	RemoteID string
	Name     string
	Color    string
	// Enabled: synchronised (not in disabledCalendars).
	Enabled bool
	// Note is "default" / "read only" / "shared · %s".
	Note     string
	Writable bool
	// IsDefault: new events go here (the configured default, else the
	// server's).
	IsDefault bool
}

// CanToggle reports a working checkbox (a remote id is known).
func (r CalendarRow) CanToggle() bool { return r.RemoteID != "" }

// CalendarAccountController runs one account's section.
type CalendarAccountController struct {
	Source    calendardata.DataSource
	AccountID api.AccountID
	Opts      Options

	// OnChange: the section changed; the view reads it anew.
	OnChange func()
	// OnQuestion asks a question (the consent explanation); never
	// answering is Cancel.
	OnQuestion func(q calendar.Question, answer func(Answer))
	// OnSignIn is the re-sign-in of the account with calendar: true
	// (account.oauthStart).
	OnSignIn func(api.AccountID)
	// OnAddCalDAV is Add CalDAV Calendar… of an account without a
	// calendar.
	OnAddCalDAV func()
	// OnRemove is a CalDAV account's Remove Account: the Accounts page's
	// question and account.remove.
	OnRemove func(api.AccountID)

	saving     bool
	err        string
	listed     []api.CalDAVCalendar
	listedOK   bool
	scheduling api.CalDAVScheduling
	config     *api.AccountConfig
	token      *board.ObserverToken
	askConsent bool
	closed     bool
}

// NewCalendarAccountController is the section of the account id.
func NewCalendarAccountController(src calendardata.DataSource, id api.AccountID, opts Options) *CalendarAccountController {
	c := &CalendarAccountController{Source: src, AccountID: id, Opts: opts}
	if a, ok := src.Snapshot().Account(id); ok {
		cfg := accountConfigClone(a.Config)
		c.config = &cfg
	}
	return c
}

// Start follows the source and, for a CalDAV account, lists the
// server's calendars.
func (c *CalendarAccountController) Start() {
	if c.token != nil || c.closed {
		return
	}
	c.token = c.Source.Observe(func(ch calendardata.Change) {
		if ch.Kind == calendardata.ChangeSnapshot {
			c.snapshotChanged()
		}
	})
	if c.IsCalDAV() {
		c.loadCalDAV()
	}
	c.snapshotChanged()
}

// Stop ends the section: no answer is taken any more.
func (c *CalendarAccountController) Stop() {
	c.closed = true
	if c.token != nil {
		c.token.Cancel()
		c.token = nil
	}
}

// Saving reports an account.update without its answer.
func (c *CalendarAccountController) Saving() bool { return c.saving }

// Error is the last refusal ("" none).
func (c *CalendarAccountController) Error() string { return c.err }

// Listed are the CalDAV calendars as account.listCalDAVCalendars listed
// them; false before the answer (or for another kind).
func (c *CalendarAccountController) Listed() ([]api.CalDAVCalendar, bool) {
	return c.listed, c.listedOK
}

// Scheduling is the server's scheduling from account.listCalDAVCalendars
// ("" before).
func (c *CalendarAccountController) Scheduling() api.CalDAVScheduling { return c.scheduling }

// Config is the account's configuration as shown (a written change
// before its answer); nil for an unknown account.
func (c *CalendarAccountController) Config() *api.AccountConfig { return c.config }

// Account is the account in the snapshot.
func (c *CalendarAccountController) Account() (api.Account, bool) {
	return c.Source.Snapshot().Account(c.AccountID)
}

// CalendarAccount is the account's calendar.list entry.
func (c *CalendarAccountController) CalendarAccount() (api.CalendarAccount, bool) {
	return c.Source.Snapshot().CalendarAccount(c.AccountID)
}

func (c *CalendarAccountController) kind() api.AccountKind {
	if c.config != nil {
		return c.config.Protocol()
	}
	a, _ := c.Account()
	return a.Config.Protocol()
}

// IsCalDAV reports a caldav account.
func (c *CalendarAccountController) IsCalDAV() bool { return c.kind() == api.AccountCalDAV }

// IsGraph reports a graph account.
func (c *CalendarAccountController) IsGraph() bool { return c.kind() == api.AccountGraph }

// HasCalendarCapability reports an account that can have calendars.
func (c *CalendarAccountController) HasCalendarCapability() bool {
	a, ok := c.Account()
	return ok && a.Can(api.CapabilityCalendar)
}

// ShowsNoCalendar: an account without the capability (IMAP) shows "This
// account has no calendar." with Add CalDAV Calendar….
func (c *CalendarAccountController) ShowsNoCalendar() bool { return !c.HasCalendarCapability() }

// CalendarConfig is the shown calendar configuration (a copy).
func (c *CalendarAccountController) CalendarConfig() api.CalendarConfig {
	if c.config != nil && c.config.Calendar != nil {
		return calendarConfigClone(*c.config.Calendar)
	}
	return api.CalendarConfig{Sync: c.IsCalDAV()}
}

// ShowsSyncSwitch: a Graph account has the Sync switch (a CalDAV account
// always synchronises).
func (c *CalendarAccountController) ShowsSyncSwitch() bool { return c.IsGraph() }

// SyncOn is the Sync switch's state.
func (c *CalendarAccountController) SyncOn() bool { return c.IsCalDAV() || c.CalendarConfig().Sync }

// ConsentRequired reports a grant without the calendar scopes.
func (c *CalendarAccountController) ConsentRequired() bool {
	a, ok := c.CalendarAccount()
	return ok && a.State.Status == api.CalendarSyncConsentRequired
}

// Rows are the calendars of calendar.list (the disabled ones too), the
// checkboxes as the shown configuration says; for a CalDAV account also
// the server's calendars the daemon does not store yet.
func (c *CalendarAccountController) Rows() []CalendarRow {
	cfg := c.CalendarConfig()
	def := cfg.DefaultCalendar
	tr := c.Opts.Env.Tr
	var out []CalendarRow
	known := map[string]bool{}
	if a, ok := c.CalendarAccount(); ok {
		for _, k := range a.Calendars {
			isDefault := k.IsDefault
			enabled := !k.Disabled
			if k.RemoteID != "" {
				if def != "" {
					isDefault = def == k.RemoteID
				}
				enabled = !slices.Contains(cfg.DisabledCalendars, k.RemoteID)
				known[k.RemoteID] = true
			}
			n := k
			n.IsDefault = isDefault
			out = append(out, CalendarRow{RemoteID: k.RemoteID, Name: accountLine(k.Name), Color: k.Color,
				Enabled: enabled, Note: calendar.CalendarRowNote(n, tr), Writable: calendar.Writable(k), IsDefault: isDefault})
		}
	}
	for _, k := range c.listed {
		if known[k.Href] {
			continue
		}
		isDefault := k.IsDefault
		if def != "" {
			isDefault = def == k.Href
		}
		out = append(out, CalendarRow{RemoteID: k.Href, Name: accountLine(k.Name), Color: k.Color,
			Enabled:  !slices.Contains(cfg.DisabledCalendars, k.Href),
			Note:     calendar.CalendarRowNote(api.Calendar{IsDefault: isDefault, Access: k.Access}, tr),
			Writable: k.Access == api.CalendarAccessOwner || k.Access == api.CalendarAccessWrite, IsDefault: isDefault})
	}
	return out
}

// DefaultChoices are Default Calendar for New Events' choices: the
// enabled writable rows with a remote id.
func (c *CalendarAccountController) DefaultChoices() []CalendarRow {
	var out []CalendarRow
	for _, r := range c.Rows() {
		if r.Enabled && r.Writable && r.CanToggle() {
			out = append(out, r)
		}
	}
	return out
}

// KeepChoices are Keep Events' choices in days.
func (c *CalendarAccountController) KeepChoices() []int { return calendar.KeepChoices }

// KeepDays is Keep Events (0 resolved to the daemon's default).
func (c *CalendarAccountController) KeepDays() int {
	if k := c.CalendarConfig().KeepDays; k != 0 {
		return k
	}
	return api.DefaultCalendarKeepDays
}

// InvitationCard is the invitation card's switch.
func (c *CalendarAccountController) InvitationCard() bool {
	return c.CalendarConfig().ShowsInvitationCard()
}

// ShowUnanswered is the switch that draws unanswered invitations.
func (c *CalendarAccountController) ShowUnanswered() bool {
	return c.CalendarConfig().ShowsUnanswered()
}

// AfterAnswer is After Answering ("" resolved to keep).
func (c *CalendarAccountController) AfterAnswer() api.AfterAnswer {
	if a := c.CalendarConfig().AfterAnswer; a != "" {
		return a
	}
	return api.AfterAnswerKeep
}

// SetSync is Sync This Account's Calendars; switching on asks for the
// consent when the daemon then reports it missing.
func (c *CalendarAccountController) SetSync(on bool) {
	if !c.ShowsSyncSwitch() || on == c.CalendarConfig().Sync {
		return
	}
	c.askConsent = on
	c.change(func(k *api.CalendarConfig) { k.Sync = on })
}

// SetEnabled is a calendar's checkbox; switching the default off drops
// it.
func (c *CalendarAccountController) SetEnabled(on bool, remoteID string) {
	if !slices.ContainsFunc(c.Rows(), func(r CalendarRow) bool { return r.RemoteID == remoteID && r.CanToggle() }) {
		return
	}
	c.change(func(k *api.CalendarConfig) {
		d := slices.DeleteFunc(slices.Clone(k.DisabledCalendars), func(s string) bool { return s == remoteID })
		if !on {
			d = append(d, remoteID)
		}
		if len(d) == 0 {
			d = nil
		}
		k.DisabledCalendars = d
		if !on && k.DefaultCalendar == remoteID {
			k.DefaultCalendar = ""
		}
	})
}

// SetDefaultCalendar is Default Calendar for New Events ("" the
// server's).
func (c *CalendarAccountController) SetDefaultCalendar(remoteID string) {
	if remoteID == c.CalendarConfig().DefaultCalendar {
		return
	}
	c.change(func(k *api.CalendarConfig) { k.DefaultCalendar = remoteID })
}

// SetKeepDays is Keep Events; only one of KeepChoices.
func (c *CalendarAccountController) SetKeepDays(days int) {
	if days == c.KeepDays() || !slices.Contains(calendar.KeepChoices, days) {
		return
	}
	c.change(func(k *api.CalendarConfig) { k.KeepDays = days })
}

// SetInvitationCard is the invitation card's switch.
func (c *CalendarAccountController) SetInvitationCard(on bool) {
	if on == c.InvitationCard() {
		return
	}
	c.change(func(k *api.CalendarConfig) { k.InvitationCard = &on })
}

// SetShowUnanswered is the switch that draws unanswered invitations.
func (c *CalendarAccountController) SetShowUnanswered(on bool) {
	if on == c.ShowUnanswered() {
		return
	}
	c.change(func(k *api.CalendarConfig) { k.ShowUnanswered = &on })
}

// SetAfterAnswer is After Answering.
func (c *CalendarAccountController) SetAfterAnswer(a api.AfterAnswer) {
	if a == c.AfterAnswer() {
		return
	}
	c.change(func(k *api.CalendarConfig) { k.AfterAnswer = a })
}

// SignIn is the consent row's Sign In….
func (c *CalendarAccountController) SignIn() {
	if c.OnSignIn != nil {
		c.OnSignIn(c.AccountID)
	}
}

// AddCalDAV is Add CalDAV Calendar….
func (c *CalendarAccountController) AddCalDAV() {
	if c.OnAddCalDAV != nil {
		c.OnAddCalDAV()
	}
}

// Remove is a CalDAV account's Remove Account.
func (c *CalendarAccountController) Remove() {
	if c.IsCalDAV() && c.OnRemove != nil {
		c.OnRemove(c.AccountID)
	}
}

func (c *CalendarAccountController) change(f func(*api.CalendarConfig)) {
	k := c.CalendarConfig()
	f(&k)
	c.changeConfig(func(cfg *api.AccountConfig) { cfg.Calendar = &k }, api.Credentials{})
}

// changeConfig is one account.update of the whole configuration changed
// by f; empty credentials keep the stored secret.
func (c *CalendarAccountController) changeConfig(f func(*api.AccountConfig), creds api.Credentials) {
	if c.config == nil {
		return
	}
	before := accountConfigClone(*c.config)
	cfg := accountConfigClone(*c.config)
	f(&cfg)
	if reflect.DeepEqual(cfg, before) && creds == (api.Credentials{}) {
		return
	}
	c.config = &cfg
	c.saving, c.err = true, ""
	c.changed()
	params := api.AccountUpdateParams{AccountID: c.AccountID, Config: accountConfigClone(cfg), Credentials: creds}
	c.Source.UpdateAccount(params, func(e *api.Error) {
		if c.closed {
			return
		}
		c.saving = false
		if e != nil {
			c.config = &before
			c.askConsent = false
			c.err = c.Opts.ErrorMessage(e, c.Opts.Env.Tr.T("Saving the account"))
		}
		c.changed()
		c.snapshotChanged()
	})
}

// Caldav is the CalDAV connection as shown (nil: not a CalDAV account).
func (c *CalendarAccountController) Caldav() *api.CalDAVConfig {
	if !c.IsCalDAV() || c.config == nil {
		return nil
	}
	return c.config.CalDAV
}

// Name is the account's name as typed.
func (c *CalendarAccountController) Name() string {
	if c.config == nil {
		return ""
	}
	return c.config.Name
}

// ServerHost is the Server row: the server's host, else its URL cleaned.
func (c *CalendarAccountController) ServerHost() string {
	d := c.Caldav()
	if d == nil {
		return ""
	}
	if h := caldavHostOf(d.ServerURL); h != "" {
		return accountLine(h)
	}
	return accountLine(d.ServerURL)
}

// Username is the Username row.
func (c *CalendarAccountController) Username() string {
	if d := c.Caldav(); d != nil {
		return accountLine(d.Username)
	}
	return ""
}

// MailAccounts are the mail-account popup's choices (after None).
func (c *CalendarAccountController) MailAccounts() []MailAccount {
	return MailAccountsOf(c.Source.Snapshot())
}

// MailAccountID is the mail account for invitations ("" None).
func (c *CalendarAccountController) MailAccountID() api.AccountID {
	if d := c.Caldav(); d != nil {
		return d.MailAccountID
	}
	return ""
}

// CertificatePinned reports a pinned certificate (the row offers
// Forget).
func (c *CalendarAccountController) CertificatePinned() bool {
	d := c.Caldav()
	return d != nil && d.CertificateSHA256 != ""
}

// CertificateText is the Certificate row: "Trusted by you", "Not
// encrypted (local server)", "Verified".
func (c *CalendarAccountController) CertificateText() string {
	tr := c.Opts.Env.Tr
	switch d := c.Caldav(); {
	case c.CertificatePinned():
		return tr.T("Trusted by you")
	case d != nil && strings.HasPrefix(strings.ToLower(d.ServerURL), "http://"):
		return tr.T("Not encrypted (local server)")
	}
	return tr.T("Verified")
}

// SetName is the Account Name field; an empty name is not written.
func (c *CalendarAccountController) SetName(n string) {
	n = strings.TrimSpace(n)
	if !c.IsCalDAV() || n == "" || n == c.Name() {
		return
	}
	c.changeConfig(func(cfg *api.AccountConfig) { cfg.Name = n }, api.Credentials{})
}

// SetMailAccount is Mail Account for Invitations ("" None).
func (c *CalendarAccountController) SetMailAccount(id api.AccountID) {
	if !c.IsCalDAV() || c.Caldav() == nil || id == c.MailAccountID() {
		return
	}
	c.changeConfig(func(cfg *api.AccountConfig) { cfg.CalDAV.MailAccountID = id }, api.Credentials{})
}

// ForgetCertificate drops the pin; the server's certificate must verify
// again.
func (c *CalendarAccountController) ForgetCertificate() {
	if !c.CertificatePinned() {
		return
	}
	c.changeConfig(func(cfg *api.AccountConfig) { cfg.CalDAV.CertificateSHA256 = "" }, api.Credentials{})
}

// ReplacePassword sends a new password to the keyring with
// account.update; it is never kept here.
func (c *CalendarAccountController) ReplacePassword(password string) {
	if !c.IsCalDAV() || password == "" {
		return
	}
	c.changeConfig(func(*api.AccountConfig) {}, api.Credentials{Password: password})
}

func (c *CalendarAccountController) loadCalDAV() {
	if c.config == nil {
		return
	}
	p := api.AccountListCalDAVCalendarsParams{AccountID: c.AccountID, Config: accountConfigClone(*c.config)}
	c.Source.ListCalDAVCalendars(p, func(r *api.AccountListCalDAVCalendarsResult, e *api.Error) {
		if c.closed || e != nil || r == nil {
			return
		}
		c.listed, c.listedOK, c.scheduling = slices.Clone(r.Calendars), true, r.Scheduling
		c.changed()
	})
}

func (c *CalendarAccountController) snapshotChanged() {
	if !c.saving {
		if a, ok := c.Account(); ok && (c.config == nil || !reflect.DeepEqual(a.Config, *c.config)) {
			cfg := accountConfigClone(a.Config)
			c.config = &cfg
		}
	}
	if c.askConsent && c.ConsentRequired() {
		c.askConsent = false
		if c.OnQuestion != nil {
			tr := c.Opts.Env.Tr
			q := calendar.Question{Heading: calendar.ConsentLine(tr), Body: calendar.SyncConsentNote(tr), Confirm: calendar.SignIn(tr)}
			c.OnQuestion(q, func(a Answer) {
				if a == AnswerConfirm && !c.closed {
					c.SignIn()
				}
			})
		}
	}
	c.changed()
}

func (c *CalendarAccountController) changed() {
	if !c.closed && c.OnChange != nil {
		c.OnChange()
	}
}

// accountLine is one line of server text, cleaned.
func accountLine(s string) string { return board.CleanLine(s, api.MaxEventLocationBytes) }

// accountConfigClone copies the parts of a configuration this controller
// changes (the calendar and CalDAV configs), so that the snapshot's
// stays as it is.
func accountConfigClone(c api.AccountConfig) api.AccountConfig {
	if c.Calendar != nil {
		k := calendarConfigClone(*c.Calendar)
		c.Calendar = &k
	}
	if c.CalDAV != nil {
		d := *c.CalDAV
		c.CalDAV = &d
	}
	return c
}

// calendarConfigClone copies a calendar config's list and pointers.
func calendarConfigClone(k api.CalendarConfig) api.CalendarConfig {
	k.DisabledCalendars = slices.Clone(k.DisabledCalendars)
	if k.ShowUnanswered != nil {
		v := *k.ShowUnanswered
		k.ShowUnanswered = &v
	}
	if k.InvitationCard != nil {
		v := *k.InvitationCard
		k.InvitationCard = &v
	}
	return k
}
