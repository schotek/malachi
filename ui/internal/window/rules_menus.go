// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/rules"
	"github.com/schotek/malachi/ui/internal/rulesctl"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The mail rules in the main window (docs/plans/rules.md R14): the
// context menu of a message has Create Rule from Message… (win.create-rule,
// the editor prefilled from the message by rules.FromMessage), the menu of
// a folder Apply Rules… (the question, the progress toast, the result as
// a toast: rulesctl.Controller.Apply). The window keeps one controller for
// them, made on first use, which reads the rules at connecting and hears
// notify.rulesChanged through listenRules; Preferences → Rules has its own
// (preferences_rules.go). An older daemon without the rules offers
// neither item.

// rulesController is the window's controller of the rules, nil without a
// daemon (and without the samples).
func (w *Window) rulesController() *rulesctl.Controller {
	if w.rules != nil {
		return w.rules
	}
	caller := rulesCaller(w.client)
	if caller == nil {
		return nil
	}
	c := rulesctl.NewController(rulesOptions(caller, w.log))
	c.OnQuestion = func(q rules.Question, answer func(bool)) { askRulesQuestion(w, q, answer) }
	c.OnToast = w.Toast
	c.OnBusy = w.rulesBusy
	// The rules' notifications reach the controller like the Preferences
	// page's: through listenRules (handleNotification dispatches them).
	listenRules(func(method string, params json.RawMessage) { c.Handle(method, params) })
	w.rules = c
	c.Refresh()
	return c
}

// rulesRefresh reads the rules again after connecting.
func (w *Window) rulesRefresh() {
	if w.rules != nil {
		w.rules.Refresh()
		return
	}
	w.rulesController()
}

// rulesBusy shows the progress line of Apply Rules… as a toast that stays
// until the call ends ("" ends it).
func (w *Window) rulesBusy(text string) {
	if w.rulesToast != nil {
		w.rulesToast.Dismiss()
		w.rulesToast = nil
	}
	if text == "" {
		return
	}
	t := widget.PlainToast(text)
	t.SetTimeout(0)
	w.toasts.AddToast(t)
	w.rulesToast = t
}

// canCreateRule reports whether Create Rule from Message… is offered for
// the selected rows: one message of a mail account, in a folder other than
// the outbox, the drafts and the sent mail (rules.CanCreateFrom).
func (w *Window) canCreateRule(rows []listRow) bool {
	if len(rows) != 1 {
		return false
	}
	c := w.rulesController()
	if c == nil || !c.Available() {
		return false
	}
	m := rows[0].Message
	acc, ok := w.model.account(m.AccountID)
	if !ok || !integrations.ShowsInAccounts(acc) {
		return false
	}
	f, ok := w.model.folder(folderKey{Account: m.AccountID, Folder: m.FolderID})
	return ok && rules.CanCreateFrom(f.Role)
}

// createRuleFromSelection is win.create-rule: the editor of a new rule
// made from the selected message.
func (w *Window) createRuleFromSelection() {
	rows := w.selectedRows()
	if !w.canCreateRule(rows) {
		return
	}
	m := rows[0].Message
	w.openRuleEditor(rules.FromMessage(m, m.AccountID, i18n.Tr), nil)
}

// openRuleEditor presents the editor of form over the window, its
// accounts and folders the window's own.
func (w *Window) openRuleEditor(form rules.Form, old *api.Rule) {
	c := w.rulesController()
	if c == nil {
		return
	}
	dir := rulesctl.NewDirectory(c.Opts)
	if rulesctl.SamplesOn() {
		dir.Load() // the samples' accounts, not the window's
	} else {
		dir.Seed(w.model.accounts, w.model.folders)
	}
	newRulesEditor(c.Opts, dir, form, old, func(api.Rule) { c.Refresh() }).present(w)
}

// rulesOfferedFor reports whether Apply Rules… is offered for the folder
// of an account (rules.CanApplyIn; a mail account) and whether an enabled
// rule runs on the account's mail (else the item is disabled).
func (w *Window) rulesOfferedFor(acc api.Account, f api.Folder) (offer, enabled bool) {
	c := w.rulesController()
	if c == nil || !c.Available() || !integrations.ShowsInAccounts(acc) || !rules.CanApplyIn(f) {
		return false, false
	}
	return true, c.AppliesTo(acc.ID)
}

// applyRules is Apply Rules… of a folder's menu: the question, then the
// account's rules by hand over the folder's stored messages.
func (w *Window) applyRules(f api.Folder) {
	if c := w.rulesController(); c != nil {
		c.Apply(f.AccountID, f.ID, folderTitle(f))
	}
}
