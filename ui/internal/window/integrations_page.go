// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Integrations: the accounts that are not mail accounts
// (Connected Services; their rows are the Accounts page's, built in
// accounts_page.go from integrations.RowFor and filled by the same
// account.list) and the catalogue of services that can be added
// (Available Services, integrations.Catalog). A Jira row edits its
// settings, a CalDAV row switches to the Calendar page, which keeps the
// CalDAV account's server, password, certificate and calendars.

// bindIntegrations sets the page's texts and builds the catalogue rows.
func (d *PreferencesDialog) bindIntegrations(c *client.Client) (unbind func()) {
	tx := integrations.Texts(i18n.Tr)
	d.integrationsPage.SetTitle(tx.Title)
	d.integrationsPage.SetDescription(glib.MarkupEscapeText(tx.Description))
	d.integrationList.group.SetTitle(glib.MarkupEscapeText(tx.ConnectedTitle))
	d.integrationList.empty.SetTitle(tx.EmptyTitle)
	d.integrationList.empty.SetSubtitle(tx.EmptySubtitle)
	d.integrationsServices.SetTitle(glib.MarkupEscapeText(tx.AvailableTitle))
	for _, s := range integrations.Catalog(i18n.Tr) {
		d.integrationsServices.Add(d.newServiceRow(c, s, tx.Add))
	}
	return func() {}
}

// newServiceRow is a catalogue row: the service's icon, name and
// description, and its Add… button, which opens the service's assistant.
func (d *PreferencesDialog) newServiceRow(c *client.Client, s integrations.Service, add string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(s.Name)
	row.SetSubtitle(s.Description)
	row.AddPrefix(gtk.NewImageFromIconName(s.Icon))
	button := gtk.NewButtonWithLabel(add)
	button.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(button)
	row.SetActivatableWidget(button)
	kind := s.Kind
	button.ConnectClicked(func() { d.addService(c, kind) })
	return row
}

// addService opens the assistant of a catalogue service.
func (d *PreferencesDialog) addService(c *client.Client, kind api.AccountKind) {
	switch kind {
	case api.AccountJira:
		d.addJiraAccount(c)
	case api.AccountCalDAV:
		d.addCalDAVAccount(c)
	}
}

// addCalDAVAccount opens the Add CalDAV sheet over the dialog (as the
// Calendar page's Add CalDAV Calendar… does); both pages reload once it
// added the account. Without the main window there is no calendar source
// to add to.
func (d *PreferencesDialog) addCalDAVAccount(c *client.Client) {
	w := d.calendarWindow()
	if w == nil {
		d.AddToast(widget.PlainToast(calendar.PreviewOnly(i18n.Tr)))
		return
	}
	w.showAddCalDAV(d, func() {
		if d.closed {
			return
		}
		d.loadAccounts(c)
		d.refreshCalendarPage()
	})
}

// showCalendarPage is a CalDAV row's Edit: the Calendar page, where the
// account's section is. Hidden without a calendar source (no main
// window), so then it only says so.
func (d *PreferencesDialog) showCalendarPage() {
	if d.calendarPage == nil {
		d.AddToast(widget.PlainToast(calendar.PreviewOnly(i18n.Tr)))
		return
	}
	d.SetVisiblePageName("calendar")
}

// refreshCalendarPage asks the Calendar page's source again after an
// account was added or removed elsewhere in the dialog.
func (d *PreferencesDialog) refreshCalendarPage() {
	if p := d.calendarPage; p != nil && !p.closed {
		p.src.Refresh()
	}
}
