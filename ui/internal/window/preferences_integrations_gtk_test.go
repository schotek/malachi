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
// daemon, no window): the mail accounts on Accounts, Jira, CalDAV and an
// AI provider account on Integrations with the switch on the Jira and
// the AI row, the AI row's usage line, the two local rows, both sections
// of the catalogue, and the placeholders. Run with MALACHI_GTK_SMOKE=1
// and a GTK display.
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
		integrationsLocal:    b.GetObject("integrations_local_group").Cast().(*adw.PreferencesGroup),
		integrationsAI:       b.GetObject("integrations_ai_group").Cast().(*adw.PreferencesGroup),
	}
	defer func() { d.closed = true }()
	unbind := d.bindIntegrations(nil)
	defer unbind()
	tx := integrations.Texts(i18n.Tr)
	if d.integrationsPage.Title() != tx.Title || d.integrationList.empty.Title() != tx.EmptyTitle {
		t.Errorf("texts: %q / %q", d.integrationsPage.Title(), d.integrationList.empty.Title())
	}
	if d.integrationsAI.Title() != tx.AITitle || d.integrationsServices.Title() != tx.AvailableTitle {
		t.Errorf("catalogue titles: %q / %q", d.integrationsServices.Title(), d.integrationsAI.Title())
	}
	// The two local rows, without an Assistant: their titles and the
	// Settings button, Remove hidden while nothing is registered or
	// connected.
	locals := integrations.LocalRows(integrations.ClaudeState{}, integrations.ChatGPTState{}, i18n.Tr)
	if len(d.localRows) != 2 || d.localRows[0].Title() != locals[0].Title || d.localRows[1].Title() != locals[1].Title {
		t.Fatalf("local rows %d", len(d.localRows))
	}
	for _, r := range d.localRows {
		if r.settings.TooltipText() != tx.Settings || r.remove.Visible() {
			t.Errorf("local row %q: tooltip %q, remove %v", r.Title(), r.settings.TooltipText(), r.remove.Visible())
		}
	}
	d.applyLocalRows(integrations.ClaudeState{Registered: true, Present: true, Path: "/usr/bin/claude"}, integrations.ChatGPTState{})
	if !d.localRows[0].remove.Visible() || d.localRows[0].remove.Label() != locals[0].RemoveLabel || d.localRows[0].Subtitle() == locals[0].Subtitle {
		t.Error("the registered Claude row offers Unregister and says so")
	}

	caldav := api.Account{ID: "c", Config: api.AccountConfig{Name: "Nextcloud", Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: "https://cloud.example.org/dav"}}}
	mail := api.Account{ID: "m", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "me@example.invalid"}}
	ai := aiTestAccount("acc_0123456789abcdef0123456789abcdef")
	d.setAccounts(nil, []api.Account{jiraTestAccount(), mail, caldav, ai})
	if len(d.mailList.rows) != 1 || d.mailList.rows[0].account.ID != "m" || d.mailList.empty.Visible() {
		t.Errorf("accounts page: %d rows", len(d.mailList.rows))
	}
	rows := d.integrationList.rows
	if len(rows) != 3 || rows[0].account.ID != "j1" || rows[1].account.ID != "c" || rows[2].account.ID != ai.ID || d.integrationList.empty.Visible() {
		t.Fatalf("integrations page: %d rows", len(rows))
	}
	if rows[0].toggle == nil || rows[1].toggle != nil || rows[2].toggle == nil {
		t.Error("the switch: Jira and the AI account")
	}
	if rows[1].edit == nil || rows[1].edit.TooltipText() != tx.CalendarSettings {
		t.Error("the CalDAV row's Edit")
	}
	if spec := integrations.RowFor(ai, i18n.Tr); rows[2].Title() != "Work key" || rows[2].Subtitle() != spec.Subtitle || rows[2].edit == nil {
		t.Errorf("the AI row: %q / %q", rows[2].Title(), rows[2].Subtitle())
	}
	rows[2].showUsage(12345)
	if rows[2].Subtitle() != integrations.RowFor(ai, i18n.Tr).Subtitle+" · "+usageLine(12345) {
		t.Errorf("the AI row with its usage: %q", rows[2].Subtitle())
	}
	if d.usageRow(ai.ID) != rows[2] || d.usageRow("j1") != nil {
		t.Error("usageRow finds the AI row only")
	}
	if got := d.currentAccounts(); len(got) != 4 {
		t.Errorf("current accounts %d", len(got))
	}
	d.setAccounts(nil, []api.Account{mail})
	if len(d.integrationList.rows) != 0 || !d.integrationList.empty.Visible() {
		t.Error("the Integrations placeholder")
	}
}
