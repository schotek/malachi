// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountservices"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signin"
)

// The calendar and the contacts of a new Microsoft 365 account
// (docs/plans/account-services.md §3.2): the group "Also Sync from This
// Account" with a switch per service, checked by default. On the browser
// sign-in's prompt the choice goes into account.oauthStart's configuration,
// so the daemon asks for the scopes of the services in the one consent;
// after a sign-in that asked for one did not succeed, "Try Again with
// Mail Only" unchecks them and signs in again. An account of GNOME Online
// Accounts (GTK only) gets the group on the connection test's page and
// the choice in account.add's configuration, without the retry: the
// services are switched on or off in Online Accounts. Editing an account
// never shows it; Preferences keep the switches. The rules are
// accountservices.Choice; this only wires the widgets.

// servicesGroup is one of the two groups (oauth_services_group,
// testing_services_group) with its switches.
type servicesGroup struct {
	group    *adw.PreferencesGroup
	rows     []*adw.SwitchRow
	services []accountservices.Service
	// setting is set while the group sets its switches itself.
	setting bool
}

// show shows v (nil hides the group); a switch the user flips calls set.
func (g *servicesGroup) show(v *accountservices.View, set func(accountservices.Service, bool)) {
	g.group.SetVisible(v != nil)
	if v == nil {
		return
	}
	g.group.SetTitle(v.Title)
	g.group.SetDescription(v.Note)
	var services []accountservices.Service
	for _, r := range v.Rows {
		services = append(services, r.Service)
	}
	if !slices.Equal(services, g.services) {
		for _, r := range g.rows {
			g.group.Remove(r)
		}
		g.rows, g.services = nil, services
		for _, s := range services {
			row := adw.NewSwitchRow()
			row.SetUseMarkup(false)
			s := s
			row.NotifyProperty("active", func() {
				if !g.setting {
					set(s, row.Active())
				}
			})
			g.group.Add(row)
			g.rows = append(g.rows, row)
		}
	}
	g.setting = true
	for i, r := range v.Rows {
		g.rows[i].SetTitle(r.Title)
		if g.rows[i].Active() != r.On {
			g.rows[i].SetActive(r.On)
		}
	}
	g.setting = false
}

// bindServices takes the groups and the retry's button from the builder.
func (w *Wizard) bindServices(b *gtk.Builder) {
	w.oauthServices = &servicesGroup{group: b.GetObject("oauth_services_group").Cast().(*adw.PreferencesGroup)}
	w.testServices = &servicesGroup{group: b.GetObject("testing_services_group").Cast().(*adw.PreferencesGroup)}
	w.oauthMailOnly = b.GetObject("oauth_mail_only_button").Cast().(*gtk.Button)
	w.oauthMailOnly.ConnectClicked(w.onMailOnly)
}

// showServices shows the choice in the group of the page it belongs to:
// the prompt's for a browser sign-in, the test page's for an account of
// GNOME Online Accounts.
func (w *Wizard) showServices() {
	set := func(s accountservices.Service, on bool) {
		w.services.Set(s, on)
		w.showServices()
	}
	if w.servicesOnTest {
		w.oauthServices.show(nil, set)
		w.oauthMailOnly.SetVisible(false)
		w.testServices.show(w.services.View(false, i18n.Tr), set)
		return
	}
	w.testServices.show(nil, set)
	v := w.services.View(true, i18n.Tr)
	w.oauthServices.show(v, set)
	w.oauthMailOnly.SetVisible(v != nil && v.MailOnly != "")
	if v != nil {
		w.oauthMailOnly.SetLabel(v.MailOnly)
	}
}

// offerServicesForSignIn sets the prompt's group for the account the
// browser signs in: cfg the new account, nil when editing.
func (w *Wizard) offerServicesForSignIn(cfg *api.AccountConfig) {
	w.servicesOnTest = false
	w.services.Offer(cfg, w.editing != nil)
	w.showServices()
}

// offerServicesForLinked sets the test page's group for an account of
// GNOME Online Accounts (startLinked); any other account has none there.
func (w *Wizard) offerServicesForLinked(cfg api.AccountConfig) {
	goa := w.editing == nil && signin.KindOf(cfg) == signin.GOA
	w.servicesOnTest = goa
	if goa {
		w.services.Offer(&cfg, false)
	} else {
		w.services.Offer(nil, false)
	}
	w.showServices()
}

// clearServices hides both groups (a new account's way is decided anew).
func (w *Wizard) clearServices() {
	w.servicesOnTest = false
	w.services.Offer(nil, false)
	w.showServices()
}

// servicesSignInFailed is a browser sign-in that ended without success:
// the prompt offers the mail-only retry when it asked for a service.
func (w *Wizard) servicesSignInFailed() {
	if w.servicesOnTest {
		return
	}
	w.services.Failed()
	w.showServices()
}

// onMailOnly is "Try Again with Mail Only": every service unchecked and
// the sign-in started again.
func (w *Wizard) onMailOnly() {
	if w.servicesOnTest || !w.services.MailOnly() {
		return
	}
	w.showServices()
	w.onOAuthSignIn()
}

// withServicesForAdd is account.add's configuration with the choice of an
// account of GNOME Online Accounts (a browser sign-in's configuration came
// back from the daemon with it already).
func (w *Wizard) withServicesForAdd(cfg api.AccountConfig) api.AccountConfig {
	if !w.servicesOnTest || w.editing != nil {
		return cfg
	}
	return w.services.Apply(cfg)
}
