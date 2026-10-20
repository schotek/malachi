// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
)

// Preferences → Accounts and Integrations over made-up accounts (no
// daemon, no window): the mail accounts on Accounts, Jira and CalDAV on
// Integrations with the switch only on the Jira row, the catalogue, and
// the placeholders. Run with MALACHI_GTK_SMOKE=1 and a GTK display.
func TestPreferencesIntegrationsGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		mailList: &accountList{
			page:  integrations.PageAccounts,
			group: b.GetObject("accounts_group").Cast().(*adw.PreferencesGroup),
			empty: b.GetObject("accounts_empty_row").Cast().(*adw.ActionRow),
		},
		integrationList: &accountList{
			page:  integrations.PageIntegrations,
			group: b.GetObject("integrations_group").Cast().(*adw.PreferencesGroup),
			empty: b.GetObject("integrations_empty_row").Cast().(*adw.ActionRow),
		},
		integrationsPage:     b.GetObject("integrations_page").Cast().(*adw.PreferencesPage),
		integrationsServices: b.GetObject("integrations_available_group").Cast().(*adw.PreferencesGroup),
	}
	defer func() { d.closed = true }()
	d.bindIntegrations(nil)
	tx := integrations.Texts(i18n.Tr)
	if d.integrationsPage.Title() != tx.Title || d.integrationList.empty.Title() != tx.EmptyTitle {
		t.Errorf("texts: %q / %q", d.integrationsPage.Title(), d.integrationList.empty.Title())
	}

	caldav := api.Account{ID: "c", Config: api.AccountConfig{Name: "Nextcloud", Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: "https://cloud.example.org/dav"}}}
	mail := api.Account{ID: "m", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "me@example.invalid"}}
	d.setAccounts(nil, []api.Account{jiraTestAccount(), mail, caldav})
	if len(d.mailList.rows) != 1 || d.mailList.rows[0].account.ID != "m" || d.mailList.empty.Visible() {
		t.Errorf("accounts page: %d rows", len(d.mailList.rows))
	}
	rows := d.integrationList.rows
	if len(rows) != 2 || rows[0].account.ID != "j1" || rows[1].account.ID != "c" || d.integrationList.empty.Visible() {
		t.Fatalf("integrations page: %d rows", len(rows))
	}
	if rows[0].toggle == nil || rows[1].toggle != nil {
		t.Error("the switch: Jira only")
	}
	if rows[1].edit == nil || rows[1].edit.TooltipText() != tx.CalendarSettings {
		t.Error("the CalDAV row's Edit")
	}
	if got := d.currentAccounts(); len(got) != 3 {
		t.Errorf("current accounts %d", len(got))
	}
	d.setAccounts(nil, []api.Account{mail})
	if len(d.integrationList.rows) != 0 || !d.integrationList.empty.Visible() {
		t.Error("the Integrations placeholder")
	}
}
