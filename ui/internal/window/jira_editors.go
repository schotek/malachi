// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jiraaccount"
	"github.com/schotek/malachi/ui/internal/widget"
)

// What edits and adds a Jira account (accountEditor, jiraEditor): its
// settings dialog (ui/internal/jiraaccount), or the Jira assistant
// (accountwizard.NewJira, NewJiraEdit), which adds an account and, in its
// edit mode, replaces the token. The main window reaches them from the
// sign-in banner and the status popover, the preferences from an
// account's row and the add menu. A mail account is edited in the mail
// assistant as before.

// editJiraAccount opens what edits Jira account a from the main window:
// the assistant on its token page when a sign-in route names the reason
// (jiraEditor), its settings otherwise, whose Replace Token… opens the
// assistant over them. The main window learns of a change through
// notify.accountsChanged.
func (w *Window) editJiraAccount(a api.Account, reason api.ErrorCode) {
	if jiraEditor(reason) == jiraEditToken {
		accountwizard.NewJiraEdit(w.client, w.log, a, reason).Present(w)
		return
	}
	d := jiraaccount.New(w.client, w.log, a)
	d.OnReplaceToken = func(a api.Account) {
		// Over the settings, which list the spaces again with the new
		// token once it is stored.
		wz := accountwizard.NewJiraEdit(w.client, w.log, a, 0)
		wz.OnDone = func(api.AccountID, api.AccountConfig) { d.TokenReplaced() }
		wz.Present(d)
	}
	d.Present(w)
}

// addJiraAccount opens the Jira assistant from the preferences; the page
// reloads once it added the account (the preferences dialog does not
// receive notify.accountsChanged).
func (d *PreferencesDialog) addJiraAccount(c *client.Client) {
	wz := accountwizard.NewJira(c, d.log)
	wz.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
		d.loadAccounts(c)
		// TRANSLATORS: %s is the new account's e-mail address.
		d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Added %s"), cfg.Email)))
	}
	wz.Present(d)
}

// editJiraAccount opens the settings of Jira account a from its row; the
// page reloads once they were saved, and Replace Token… opens the
// assistant in its edit mode over the settings, which list the spaces
// again with the new token.
func (d *PreferencesDialog) editJiraAccount(c *client.Client, a api.Account) {
	saved := func(cfg api.AccountConfig) {
		if d.closed {
			return
		}
		d.loadAccounts(c)
		// TRANSLATORS: %s is the edited account's e-mail address.
		d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Saved %s"), cfg.Email)))
	}
	dlg := jiraaccount.New(c, d.log, a)
	dlg.OnSaved = saved
	dlg.OnReplaceToken = func(a api.Account) {
		wz := accountwizard.NewJiraEdit(c, d.log, a, 0)
		wz.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
			dlg.TokenReplaced()
			saved(cfg)
		}
		wz.Present(dlg)
	}
	dlg.Present(d)
}
