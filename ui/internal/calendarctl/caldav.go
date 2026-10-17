// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/certtrust"
)

// The Add CalDAV Calendar sheet (docs/calendar.md §5.8), the port of
// CalDAVWizardController.swift: it owns one calendar.Wizard and runs its
// calls — Discover Calendars (account.discoverCalDAV, then
// account.listCalDAVCalendars), Add (account.add) — and the certificate
// trust: on a tlsError the wizard offers Trust Certificate…, the sheet
// shows the account assistant's confirmation (OnConfirmTrust), and a
// confirmed certificate runs discovery again with the pin. The mail
// account popup lists the accounts that can send (compose), for a server
// that leaves the invitations to the client. Nothing typed is logged; the
// password leaves only in the calls' Credentials.

// MailAccount is a choice of a mail-account popup (the accounts
// invitations can leave from).
type MailAccount struct {
	ID   api.AccountID
	Name string
}

// MailAccountsOf are the accounts invitations can leave from, in the
// accounts' order: those that can compose, never a CalDAV account.
func MailAccountsOf(s calendardata.Snapshot) []MailAccount {
	var out []MailAccount
	for _, a := range s.Accounts {
		if a.Can(api.CapabilityCompose) && a.Config.Protocol() != api.AccountCalDAV {
			out = append(out, MailAccount{ID: a.ID, Name: board.CleanLine(a.Config.Name, api.MaxEventLocationBytes)})
		}
	}
	return out
}

// ListedCalendar is a found calendar as the sheet lists it.
type ListedCalendar struct {
	Href, Name string
	// Color is "#rrggbb", "" none (the view checks it again).
	Color string
	// Note is "default" / "read only" (calendar.ListedNote).
	Note     string
	Selected bool
}

// TrustRequest is what the certificate confirmation shows (the account
// assistant's dialog): the cleaned problem with its certificate, the
// server named in the body, whether a pinned certificate changed and the
// fingerprint trusted before ("" unknown).
type TrustRequest struct {
	Problem  certtrust.Problem
	Servers  []string
	Changed  bool
	Previous string
}

// CalDAVWizardController runs the sheet.
type CalDAVWizardController struct {
	Source calendardata.DataSource
	Opts   Options

	// OnChange: the state changed; the sheet reads it anew.
	OnChange func()
	// OnConfirmTrust asks the certificate confirmation; answer(true)
	// pins it, answer(false) or never answering pins nothing.
	OnConfirmTrust func(req TrustRequest, answer func(bool))
	// OnDone: the account was added; the sheet closes.
	OnDone func(api.AccountID)

	wizard         *calendar.Wizard
	previewRefused bool
	// op is bumped per call and by Cancel so stale answers bail out.
	op         int
	closed     bool
	token      *board.ObserverToken
	mailChosen bool
}

// NewCalDAVWizardController is an empty sheet over src; the first mail
// account is the default for invitations until the user chooses.
func NewCalDAVWizardController(src calendardata.DataSource, opts Options) *CalDAVWizardController {
	var first api.AccountID
	if m := MailAccountsOf(src.Snapshot()); len(m) > 0 {
		first = m[0].ID
	}
	return &CalDAVWizardController{Source: src, Opts: opts, wizard: calendar.NewWizard(first)}
}

// Wizard is the sheet's state (phase, fields, what was found) for the
// calendar package's texts (FoundLine, CertificateText, WizardProgress,
// ServerChanged, …). Change it only through the controller.
func (c *CalDAVWizardController) Wizard() *calendar.Wizard { return c.wizard }

// PreviewRefused reports the last call failed with the preview's error.
func (c *CalDAVWizardController) PreviewRefused() bool { return c.previewRefused }

// MailAccounts are the mail-account popup's choices (after None).
func (c *CalDAVWizardController) MailAccounts() []MailAccount {
	return MailAccountsOf(c.Source.Snapshot())
}

// ErrorText is the sheet's error line ("" none).
func (c *CalDAVWizardController) ErrorText() string {
	if c.previewRefused {
		return calendar.PreviewOnly(c.Opts.Env.Tr)
	}
	return calendar.WizardErrorText(c.wizard, c.Opts.Env.Tr)
}

// ErrorField is the field the error concerns, for the view to mark it:
// the password for a refused sign-in, the server for the other errors;
// false without an error (or for the preview's).
func (c *CalDAVWizardController) ErrorField() (calendar.CalDAVFieldKind, bool) {
	e := c.wizard.Err
	if e == nil || c.previewRefused {
		return 0, false
	}
	switch e.Code {
	case api.CodeAuthFailed, api.CodeAuthRequired:
		return calendar.CalDAVPassword, true
	case api.CodeKeyringError:
		return 0, false
	}
	return calendar.CalDAVServer, true
}

// Start follows the source: the mail accounts arrive with its snapshot,
// and the first one is the default until the user chooses.
func (c *CalDAVWizardController) Start() {
	if c.token != nil || c.closed {
		return
	}
	c.token = c.Source.Observe(func(ch calendardata.Change) {
		if ch.Kind == calendardata.ChangeSnapshot {
			c.snapshotChanged()
		}
	})
	c.snapshotChanged()
}

func (c *CalDAVWizardController) snapshotChanged() {
	if !c.mailChosen {
		var first api.AccountID
		if m := c.MailAccounts(); len(m) > 0 {
			first = m[0].ID
		}
		if c.wizard.MailAccount != first {
			c.wizard.SetMailAccount(first)
		}
	}
	c.changed()
}

// Cancel closes the sheet: no answer is taken any more.
func (c *CalDAVWizardController) Cancel() {
	c.closed = true
	c.op++
	if c.token != nil {
		c.token.Cancel()
		c.token = nil
	}
}

// ListedCalendars are the found calendars, their names cleaned.
func (c *CalDAVWizardController) ListedCalendars() []ListedCalendar {
	if c.wizard.Listed == nil {
		return nil
	}
	out := make([]ListedCalendar, 0, len(c.wizard.Listed.Calendars))
	for _, k := range c.wizard.Listed.Calendars {
		out = append(out, ListedCalendar{Href: k.Href, Name: board.CleanLine(k.Name, api.MaxEventLocationBytes),
			Color: k.Color, Note: calendar.ListedNote(k, c.Opts.Env.Tr), Selected: c.wizard.Selected[k.Href]})
	}
	return out
}

// Edit takes the fields (ignored while a call runs).
func (c *CalDAVWizardController) Edit(name, server, username, password string) {
	before := *c.wizard
	c.wizard.Edit(name, server, username, password)
	w := c.wizard
	if w.Name != before.Name || w.Server != before.Server || w.Username != before.Username ||
		w.Password != before.Password || w.Phase != before.Phase || w.Pin != before.Pin {
		c.previewRefused = false
		c.changed()
	}
}

// Toggle selects or deselects a listed calendar.
func (c *CalDAVWizardController) Toggle(href string, on bool) {
	c.wizard.Toggle(href, on)
	c.changed()
}

// SetMailAccount is the popup's choice ("" None).
func (c *CalDAVWizardController) SetMailAccount(id api.AccountID) {
	c.mailChosen = true
	c.wizard.SetMailAccount(id)
	c.changed()
}

// Discover is Discover Calendars.
func (c *CalDAVWizardController) Discover() {
	if !c.wizard.BeginDiscover() {
		return
	}
	c.previewRefused = false
	c.changed()
	c.runDiscovery()
}

// runDiscovery runs the calls of the discovering phase: discovery, then
// the list.
func (c *CalDAVWizardController) runDiscovery() {
	c.op++
	cur := c.op
	c.Source.DiscoverCalDAV(c.wizard.DiscoverParams(), func(res *api.AccountDiscoverCalDAVResult, err *api.Error) {
		if c.closed || cur != c.op {
			return
		}
		c.wizard.DiscoverDone(res, c.wizardError(err))
		c.changed()
		if err != nil || c.wizard.Phase != calendar.WizardDiscovering || c.wizard.Step != calendar.StepList {
			return
		}
		c.Source.ListCalDAVCalendars(c.wizard.ListParams(), func(l *api.AccountListCalDAVCalendarsResult, err *api.Error) {
			if c.closed || cur != c.op {
				return
			}
			c.wizard.ListDone(l, c.wizardError(err))
			c.changed()
		})
	})
}

// Trust is Trust Certificate…: the confirmation, then discovery with the
// pin.
func (c *CalDAVWizardController) Trust() {
	p, ok := c.wizard.TrustOffer()
	if !ok || p.Cert == nil || c.OnConfirmTrust == nil {
		return
	}
	req := TrustRequest{Problem: p, Servers: []string{caldavHostOf(c.wizard.ServerURL())},
		Changed: p.Category() == certtrust.Changed, Previous: p.Expected}
	cur := c.op
	asked := false
	c.OnConfirmTrust(req, func(yes bool) {
		if asked {
			return
		}
		asked = true
		if !yes || c.closed || cur != c.op || !c.wizard.Trust() {
			return
		}
		c.changed()
		c.runDiscovery()
	})
}

// Add adds the account.
func (c *CalDAVWizardController) Add() {
	if !c.wizard.BeginAdd() {
		return
	}
	c.previewRefused = false
	c.changed()
	c.op++
	cur := c.op
	c.Source.AddAccount(c.wizard.AddParams(), func(res *api.AccountAddResult, err *api.Error) {
		if c.closed || cur != c.op {
			return
		}
		var id api.AccountID
		if res != nil {
			id = res.AccountID
		}
		if err == nil && res == nil {
			err = &api.Error{Code: api.CodeInternalError, Message: "no answer"}
		}
		c.wizard.AddDone(id, c.wizardError(err))
		c.changed()
		if err == nil && c.OnDone != nil {
			c.OnDone(id)
		}
	})
}

// wizardError is an error as the wizard takes it: the preview's as
// notImplemented (and remembered), a timeout as serverTimeout. The
// messages are technical English, never shown.
func (c *CalDAVWizardController) wizardError(e *api.Error) *api.Error {
	switch {
	case e == nil:
		return nil
	case calendardata.IsPreview(e):
		c.previewRefused = true
		return &api.Error{Code: api.CodeNotImplemented, Message: "preview"}
	case e.Code == api.CodeCancelled:
		return &api.Error{Code: api.CodeServerTimeout, Message: "timeout"}
	}
	return e
}

func (c *CalDAVWizardController) changed() {
	if !c.closed && c.OnChange != nil {
		c.OnChange()
	}
}

// caldavHostOf is the host of a server URL or a typed server
// ("cloud.example.org", "https://cloud.example.org/dav"); "" unreadable.
func caldavHostOf(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
