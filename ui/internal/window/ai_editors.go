// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/aiaccount"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// What adds and edits an AI provider account (kind ai, accountEditor
// editorAI, integrations.EditorAI): its dialog (ui/internal/aiaccount),
// which the preferences reach from the catalogue's Add… and an account's
// row on the Integrations page, the main window from the sign-in banner,
// the status popover and the board's Triage button (board_triage_button.go).
// The row of such an account on the Integrations page also says how many
// tokens the daemon's assistant used through it today (assistant.usage,
// again on notify.assistantUsageChanged).

// editAIAccount opens the dialog of AI provider account a from the main
// window; with replaceKey the key field is open (a route that says the
// provider refused the key). The main window learns of a change through
// notify.accountsChanged.
func (w *Window) editAIAccount(a api.Account, replaceKey bool) {
	d := aiaccount.New(w.client, w.log, "", &a)
	d.Present(w)
	if replaceKey {
		d.ReplaceKey()
	}
}

// addAIAccount opens the dialog that adds an account of provider p from
// the catalogue; the page reloads once it added the account (the
// preferences dialog does not receive notify.accountsChanged).
func (d *PreferencesDialog) addAIAccount(c *client.Client, p api.AIProvider) {
	dlg := aiaccount.New(c, d.log, p, nil)
	dlg.OnSaved = func(_ api.AccountID, cfg api.AccountConfig) {
		if d.closed {
			return
		}
		d.loadAccounts(c)
		// TRANSLATORS: %s is the new account's e-mail address.
		d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Added %s"), cfg.Name)))
	}
	dlg.Present(d)
}

// editAIAccount opens the dialog of account a from its row; the page
// reloads once it saved.
func (d *PreferencesDialog) editAIAccount(c *client.Client, a api.Account) {
	dlg := aiaccount.New(c, d.log, "", &a)
	dlg.OnSaved = func(_ api.AccountID, cfg api.AccountConfig) {
		if d.closed {
			return
		}
		d.loadAccounts(c)
		// TRANSLATORS: %s is the edited account's e-mail address.
		d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Saved %s"), cfg.Name)))
	}
	dlg.Present(d)
}

// The usage rows: notify.assistantUsageChanged reaches the preferences
// dialog through a listener, as the contacts' notifications do
// (contacts_notify.go), because the dialog is not a window.
var (
	assistantUsageListeners    = map[int]func(id api.AccountID){}
	assistantUsageListenerNext int
)

// listenAssistantUsage calls f with the account of every
// notify.assistantUsageChanged until cancel is called.
func listenAssistantUsage(f func(id api.AccountID)) (cancel func()) {
	assistantUsageListenerNext++
	id := assistantUsageListenerNext
	assistantUsageListeners[id] = f
	return func() { delete(assistantUsageListeners, id) }
}

// dispatchAssistantUsage hands a notify.assistantUsageChanged to the
// listeners (any other method is ignored).
func dispatchAssistantUsage(method string, params json.RawMessage) {
	if method != api.NotifyAssistantUsageChanged {
		return
	}
	var n struct {
		AccountID api.AccountID `json:"accountId"`
	}
	_ = json.Unmarshal(params, &n)
	ids := make([]int, 0, len(assistantUsageListeners))
	for id := range assistantUsageListeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := assistantUsageListeners[id]; ok {
			f(n.AccountID)
		}
	}
}

// refreshUsage asks assistant.usage for the tokens account id used today
// and shows them on its row of the Integrations page (usageLine). An
// account without a row, or a failed call, changes nothing.
func (d *PreferencesDialog) refreshUsage(c *client.Client, id api.AccountID) {
	if c == nil || d.integrationList == nil || d.usageRow(id) == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.AssistantUsageResult
		err := c.Call(ctx, api.MethodAssistantUsage, api.AssistantUsageParams{AccountID: id, Days: 0}, &res)
		glib.IdleAdd(func() {
			if d.closed || err != nil {
				return
			}
			row := d.usageRow(id)
			if row == nil {
				return
			}
			var tokens int64
			for _, day := range res.Days {
				tokens += day.InputTokens + day.OutputTokens + day.CacheReadInputTokens + day.CacheCreationInputTokens
			}
			row.showUsage(tokens)
		})
	}()
}

// usageRow is the Integrations page's row of AI provider account id, nil
// without one.
func (d *PreferencesDialog) usageRow(id api.AccountID) *accountRow {
	for _, row := range d.integrationList.rows {
		if row.account.ID == id && row.spec.Kind == api.AccountAI {
			return row
		}
	}
	return nil
}

// showUsage adds the tokens used today to the row's subtitle.
func (r *accountRow) showUsage(tokens int64) {
	r.SetSubtitle(r.spec.Subtitle + " · " + usageLine(tokens))
}

// usageLine is "<n> tokens today" with the digits grouped (formatTokens).
func usageLine(tokens int64) string {
	return fmt.Sprintf(assistant.APIText(tr).UsageToday, formatTokens(tokens))
}
