// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountservices

import "github.com/schotek/malachi/backend/pkg/api"

// The account wizard's group "Also Sync from This Account"
// (docs/plans/account-services.md §3.2): a switch per service the new
// account can sync (OfferedForNew), checked by default (K1), and "Try
// Again with Mail Only" after a sign-in that asked for one of them did not
// succeed. The GTK wizard has no pure flow of its mail pages, so the rules
// of macOS WizardController (ServicesView, setService, signInWithMailOnly,
// signInDidNotSucceed and the choice applied in signInWithProvider) live
// here, and the wizard only wires its widgets to them. The browser
// sign-in shows the group on its prompt; an account of GNOME Online
// Accounts (GTK only) on the connection test's page, without the retry.

// Row is a switch of the group.
type Row struct {
	Service Service
	Title   string
	On      bool
}

// View is the group as shown.
type View struct {
	Title string
	Rows  []Row
	Note  string
	// MailOnly is the label of the mail-only retry; "" hides it.
	MailOnly string
}

// Choice is the wizard's choice of the services of the account it adds.
// The zero value offers nothing.
type Choice struct {
	offered []Service
	choice  map[Service]bool
	// failed: the last sign-in asked for a service and did not succeed.
	failed bool
}

// Offer sets what the account can sync: cfg is the new account (nil, or
// editing an existing one, offers nothing; Preferences keep the switches
// of an account). The user's choice stays while the wizard is open; a
// failure of before is forgotten.
func (c *Choice) Offer(cfg *api.AccountConfig, editing bool) {
	c.failed = false
	if cfg == nil || editing {
		c.offered = nil
		return
	}
	c.offered = OfferedForNew(*cfg)
}

// Offered are the services offered.
func (c *Choice) Offered() []Service { return c.offered }

// Chosen is the choice of the offered services: checked unless unchecked.
func (c *Choice) Chosen() map[Service]bool {
	out := make(map[Service]bool, len(c.offered))
	for _, s := range c.offered {
		on, ok := c.choice[s]
		out[s] = on || !ok
	}
	return out
}

// anyChosen reports a service switched on.
func (c *Choice) anyChosen() bool {
	for _, on := range c.Chosen() {
		if on {
			return true
		}
	}
	return false
}

// Set is a switch of the group; a service not offered is ignored.
func (c *Choice) Set(s Service, on bool) {
	if !c.offers(s) {
		return
	}
	if c.choice == nil {
		c.choice = map[Service]bool{}
	}
	c.choice[s] = on
}

func (c *Choice) offers(s Service) bool {
	for _, o := range c.offered {
		if o == s {
			return true
		}
	}
	return false
}

// Apply is cfg with the choice applied (Apply): the daemon asks for the
// scopes of the services switched on itself, so one consent covers them
// all.
func (c *Choice) Apply(cfg api.AccountConfig) api.AccountConfig {
	if len(c.offered) == 0 {
		return cfg
	}
	return Apply(cfg, c.Chosen())
}

// Started is a new sign-in: it forgets the failure.
func (c *Choice) Started() { c.failed = false }

// Failed is a sign-in that ended without success (refused, cancelled in
// the browser, timed out, or Cancel on the waiting page): when it asked
// for a service, the group offers the mail-only retry.
func (c *Choice) Failed() { c.failed = c.anyChosen() }

// MailOnly is "Try Again with Mail Only": it unchecks every service and
// forgets the failure; false when the retry is not offered (the wizard
// then does not sign in again).
func (c *Choice) MailOnly() bool {
	if !c.failed || !c.anyChosen() {
		return false
	}
	for _, s := range c.offered {
		c.Set(s, false)
	}
	c.failed = false
	return true
}

// View is the group to show; nil hides it (nothing offered). retry says
// the page offers the mail-only retry at all (the browser prompt does,
// the GNOME Online Accounts test page does not).
func (c *Choice) View(retry bool, tr Translator) *View {
	if len(c.offered) == 0 {
		return nil
	}
	chosen := c.Chosen()
	v := &View{Title: GroupTitle(tr), Note: LaterNote(tr)}
	for _, s := range c.offered {
		v.Rows = append(v.Rows, Row{Service: s, Title: SwitchTitle(s, tr), On: chosen[s]})
	}
	if retry && c.failed && c.anyChosen() {
		v.MailOnly = MailOnly(tr)
	}
	return v
}
