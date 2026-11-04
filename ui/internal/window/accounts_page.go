// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// accountRow is one account in the Accounts or the Integrations page:
// name, address or host, a status label, the sign-in button of an account
// waiting for the browser sign-in, the pause switch when the kind has one,
// the edit and the remove button (integrations.Row decides by kind).
// Everything shown comes from the daemon, so nothing is markup.
type accountRow struct {
	*adw.ActionRow

	account   api.Account
	spec      integrations.Row
	list      *accountList // the page's list the row is in
	handle    *gtk.Image   // drag handle; the whole row is the drop target
	status    *gtk.Label
	signIn    *gtk.Button
	toggle    *gtk.Switch // nil without integrations.Row.OffersSwitch
	edit      *gtk.Button // nil with integrations.EditorNone
	remove    *gtk.Button
	reverting bool // the switch is being set programmatically

	// hint is the insertion line shown while a drag hovers this row.
	hint dropHint
}

// accountList is the group of account rows of one page: the mail accounts
// of Accounts, or the accounts of Integrations, with its placeholder row.
type accountList struct {
	page  integrations.Page
	group *adw.PreferencesGroup
	empty *adw.ActionRow
	rows  []*accountRow
}

// dropHint is the insertion line an account row shows while a drag hovers
// it: none, above the row, or below it.
type dropHint int

const (
	hintNone dropHint = iota
	hintAbove
	hintBelow
)

// setDropHint shows or hides the insertion line (CSS in internal/style).
func (r *accountRow) setDropHint(h dropHint) {
	if r.hint == h {
		return
	}
	r.hint = h
	r.RemoveCSSClass("drop-above")
	r.RemoveCSSClass("drop-below")
	switch h {
	case hintAbove:
		r.AddCSSClass("drop-above")
	case hintBelow:
		r.AddCSSClass("drop-below")
	}
}

// bindAccounts fills the Accounts and the Integrations page from one
// account.list and wires the Accounts page's add button (the mail
// account assistant). The pages reload after their own actions; their
// groups stay insensitive until the first load succeeds.
func (d *PreferencesDialog) bindAccounts(c *client.Client) (unbind func()) {
	d.rpc = c
	for _, l := range d.accountLists() {
		l.group.SetSensitive(false)
	}
	d.loadAccounts(c)

	// The wizard is built here rather than through app.add-account so
	// the page can reload itself: the preferences dialog does not receive
	// notify.accountsChanged.
	group := gio.NewSimpleActionGroup()
	mail := gio.NewSimpleAction("add-account", nil)
	mail.ConnectActivate(func(*glib.Variant) {
		wz := accountwizard.New(c, d.log)
		wz.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
			d.loadAccounts(c)
			// TRANSLATORS: %s is the new account's e-mail address.
			d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Added %s"), cfg.Email)))
		}
		wz.Present(d)
	})
	group.AddAction(mail)
	d.InsertActionGroup("prefs", group)
	return func() { d.InsertActionGroup("prefs", nil) }
}

// accountLists are the lists of both pages, those that exist (a test may
// build only one).
func (d *PreferencesDialog) accountLists() []*accountList {
	var out []*accountList
	for _, l := range []*accountList{d.mailList, d.integrationList} {
		if l != nil {
			out = append(out, l)
		}
	}
	return out
}

// loadAccounts runs account.list and rebuilds the rows of both pages.
func (d *PreferencesDialog) loadAccounts(c *client.Client) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.AccountListResult
		err := c.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			if err != nil {
				for _, l := range d.accountLists() {
					l.group.SetDescription(widget.RPCErrorText(i18n.T("Loading accounts"), err))
					l.group.SetSensitive(false)
				}
				return
			}
			d.setAccounts(c, res.Accounts)
			for _, l := range d.accountLists() {
				l.group.SetDescription("")
				l.group.SetSensitive(true)
			}
		})
	}()
}

// setAccounts keeps accounts, in the daemon's order, and replaces the rows
// of both pages: the mail accounts on Accounts, the rest on Integrations.
func (d *PreferencesDialog) setAccounts(c *client.Client, accounts []api.Account) {
	d.allAccounts = append([]api.Account(nil), accounts...)
	mail, other := integrations.Split(accounts)
	if d.mailList != nil {
		d.setListAccounts(c, d.mailList, mail)
	}
	if d.integrationList != nil {
		d.setListAccounts(c, d.integrationList, other)
	}
}

// setListAccounts replaces the rows of list l.
func (d *PreferencesDialog) setListAccounts(c *client.Client, l *accountList, accounts []api.Account) {
	for _, row := range l.rows {
		l.group.Remove(row)
	}
	l.rows = l.rows[:0]
	for _, a := range accounts {
		row := d.newAccountRow(c, l, a)
		l.rows = append(l.rows, row)
		l.group.Add(row)
	}
	l.empty.SetVisible(len(l.rows) == 0)
}

func (d *PreferencesDialog) newAccountRow(c *client.Client, l *accountList, a api.Account) *accountRow {
	spec := integrations.RowFor(a, i18n.Tr)
	row := &accountRow{ActionRow: adw.NewActionRow(), account: a, spec: spec, list: l}
	row.SetUseMarkup(false)
	row.AddCSSClass("account-row")
	row.SetTitle(spec.Title)
	row.SetSubtitle(spec.Subtitle)

	row.handle = gtk.NewImageFromIconName("list-drag-handle-symbolic")
	row.handle.AddCSSClass("drag-handle")
	row.handle.SetCursorFromName("grab")
	row.handle.SetTooltipText(i18n.T("Drag to reorder, or press Ctrl+Up and Ctrl+Down"))

	// AdwActionRow prepends prefixes, so the last one added sits leftmost:
	// the handle goes in after the account icon to end up before it.
	row.AddPrefix(gtk.NewImageFromIconName(accountIcon(a)))
	row.AddPrefix(row.handle)

	row.status = gtk.NewLabel("")
	row.status.AddCSSClass("caption")
	row.status.AddCSSClass("dim-label")
	// The status may say why the account is offline: long, so it gives way
	// to the name and shows whole in the tooltip.
	row.status.SetEllipsize(pango.EllipsizeEnd)
	row.AddSuffix(row.status)

	row.signIn = gtk.NewButtonWithLabel(i18n.T("Sign In…"))
	row.signIn.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(row.signIn)

	if spec.OffersSwitch {
		row.toggle = gtk.NewSwitch()
		row.toggle.SetVAlign(gtk.AlignCenter)
		row.toggle.SetTooltipText(i18n.T("Enabled"))
		row.AddSuffix(row.toggle)
		row.SetActivatableWidget(row.toggle)
	}

	if spec.Editor != integrations.EditorNone {
		row.edit = gtk.NewButtonFromIconName("document-edit-symbolic")
		row.edit.SetVAlign(gtk.AlignCenter)
		row.edit.AddCSSClass("flat")
		row.edit.SetTooltipText(spec.EditLabel)
		row.AddSuffix(row.edit)
	}

	row.remove = gtk.NewButtonFromIconName("user-trash-symbolic")
	row.remove.SetVAlign(gtk.AlignCenter)
	row.remove.AddCSSClass("flat")
	row.remove.SetTooltipText(i18n.T("Remove Account"))
	row.AddSuffix(row.remove)

	row.apply(a)

	if row.toggle != nil {
		row.toggle.NotifyProperty("active", func() {
			if row.reverting {
				return
			}
			d.setAccountEnabled(c, row, row.toggle.Active())
		})
	}
	row.signIn.ConnectClicked(func() { d.signInAccount(c, row) })
	if row.edit != nil {
		row.edit.ConnectClicked(func() { d.editAccount(c, row) })
	}
	row.remove.ConnectClicked(func() { d.removeAccount(c, row) })
	d.addRowReorder(c, row)
	return row
}

// apply shows the account's current state.
func (r *accountRow) apply(a api.Account) {
	r.account = a
	if r.toggle != nil {
		r.reverting = true
		r.toggle.SetActive(a.Enabled)
		r.reverting = false
	}
	text := accountStatusText(a.State)
	r.status.SetText(text)
	r.status.SetTooltipText(text)
	r.status.SetVisible(text != "")
	r.signIn.SetVisible(signin.NeedsBrowserSignIn(a))
}

// setAccountEnabled pauses or resumes through account.setEnabled; on
// failure the switch flips back and a toast explains.
func (d *PreferencesDialog) setAccountEnabled(c *client.Client, row *accountRow, want bool) {
	row.SetSensitive(false)
	what := i18n.T("Pausing the account")
	if want {
		what = i18n.T("Resuming the account")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		err := c.Call(ctx, api.MethodAccountSetEnabled,
			api.AccountSetEnabledParams{AccountID: row.account.ID, Enabled: want}, &api.AccountSetEnabledResult{})
		glib.IdleAdd(func() {
			if d.closed || row.Parent() == nil {
				return
			}
			row.SetSensitive(true)
			if err != nil {
				d.AddToast(widget.PlainToast(widget.RPCErrorText(what, err)))
				row.apply(row.account)
				return
			}
			a := row.account
			a.Enabled = want
			a.State.Status = api.SyncIdle
			if !want {
				a.State.Status = api.SyncDisabled
			}
			row.apply(a)
		})
	}()
}

// editAccount opens what edits the row's account (integrations.Row.Editor):
// the wizard prefilled with a mail account, a Jira account's settings
// (where its token is replaced too), or for a CalDAV account the Calendar
// page, which has its server, password, certificate and calendars.
func (d *PreferencesDialog) editAccount(c *client.Client, row *accountRow) {
	switch row.spec.Editor {
	case integrations.EditorJira:
		d.editJiraAccount(c, row.account)
	case integrations.EditorCalendarPage:
		d.showCalendarPage()
	case integrations.EditorMailWizard:
		d.presentEdit(c, accountwizard.NewEdit(c, d.log, row.account))
	case integrations.EditorEway:
		d.editEwayAccount(c, row.account)
	}
}

// signInAccount opens the browser sign-in of the row's account (the
// backend's own sign-in, refused or lost).
func (d *PreferencesDialog) signInAccount(c *client.Client, row *accountRow) {
	d.presentEdit(c, accountwizard.NewEditSignIn(c, d.log, row.account))
}

// presentEdit shows an editing wizard; the page reloads once it saved.
func (d *PreferencesDialog) presentEdit(c *client.Client, wz *accountwizard.Wizard) {
	wz.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
		d.loadAccounts(c)
		// TRANSLATORS: %s is the edited account's e-mail address.
		d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Saved %s"), cfg.Email)))
	}
	wz.Present(d)
}

// removeAccount confirms, then calls account.remove. The check button
// decides deleteLocalData.
func (d *PreferencesDialog) removeAccount(c *client.Client, row *accountRow) {
	check := gtk.NewCheckButtonWithMnemonic(i18n.T("Also delete _drafts and downloaded data"))
	check.SetActive(true)
	widget.ConfirmDestructiveExtra(d, i18n.T("Remove this account?"), row.spec.RemoveBody, i18n.T("_Remove"), check, func() {
		deleteLocal := check.Active()
		row.SetSensitive(false)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			err := c.Call(ctx, api.MethodAccountRemove,
				api.AccountRemoveParams{AccountID: row.account.ID, DeleteLocalData: deleteLocal}, &api.AccountRemoveResult{})
			glib.IdleAdd(func() {
				if d.closed {
					return
				}
				if err != nil {
					if row.Parent() != nil {
						row.SetSensitive(true)
					}
					d.AddToast(widget.PlainToast(widget.RPCErrorText(i18n.T("Removing the account"), err)))
					return
				}
				d.loadAccounts(c)
				d.refreshCalendarPage()
			})
		}()
	})
}

// accountRowTitle is the account name, or the address when unnamed; an
// unnamed Jira account shows its site's host before the address
// (integrations.RowFor). Unlike accountLabel nothing is trimmed: this is
// the name as the user typed it.
func accountRowTitle(a api.Account) string { return integrations.RowFor(a, i18n.Tr).Title }

// accountRowSubtitle is the line under an account's name in Preferences →
// Accounts and Integrations: the address of a mail account, the host of a
// Jira site or a CalDAV server (the address when the title shows the host
// already).
func accountRowSubtitle(a api.Account) string { return integrations.RowFor(a, i18n.Tr).Subtitle }

// editorKind is what edits an account: the mail account assistant, or
// what a Jira account has for it (jiraEditor). Every "edit account" route
// asks accountEditor first, since the mail assistant builds its pages from
// imap and smtp, which a Jira account has not.
type editorKind int

const (
	editorMailWizard editorKind = iota
	editorJira
	// editorEway: the eWay-CRM assistant in its edit mode.
	editorEway
)

// accountEditor is the editorKind of account a, by its kind.
func accountEditor(a api.Account) editorKind {
	switch {
	case jira.IsJira(a.Config):
		return editorJira
	case a.Config.Protocol() == api.AccountEway:
		return editorEway
	}
	return editorMailWizard
}

// jiraEditorKind is what edits a Jira account: its settings
// (jiraaccount), or the Jira assistant in its edit mode, which asks for a
// new token and says why.
type jiraEditorKind int

const (
	jiraEditSettings jiraEditorKind = iota
	jiraEditToken
)

// jiraEditor is the jiraEditorKind of an "edit account" route: the
// settings, unless the route asks for the token (reason is the reason of
// the sign-in banner or of an account's Sign In in the status popover; 0
// when it is not known).
func jiraEditor(reason api.ErrorCode) jiraEditorKind {
	if reason == 0 {
		return jiraEditSettings
	}
	return jiraEditToken
}

// accountStatusText is the short status shown next to the switch; empty
// for the unremarkable idle state. A refused or changed server certificate
// (certtrust.FromSyncState) is named instead of "Offline"; otherwise an
// offline or failed account says why when the state carries an error.
func accountStatusText(s api.SyncState) string {
	if p, ok := certtrust.FromSyncState(s); ok {
		return certStatusText(p.Category())
	}
	switch s.Status {
	case api.SyncDisabled:
		return i18n.T("Paused")
	case api.SyncSyncing:
		return i18n.T("Syncing…")
	case api.SyncOffline:
		if s.Error != nil {
			// TRANSLATORS: account status in Settings → Accounts; %s says why.
			return fmt.Sprintf(i18n.T("Offline: %s"), widget.EndpointErrorText(s.Error))
		}
		return i18n.T("Offline")
	case api.SyncAuthRequired:
		return i18n.T("Sign-in required")
	case api.SyncError:
		if s.Error != nil {
			// TRANSLATORS: account status in Settings → Accounts; %s says why.
			return fmt.Sprintf(i18n.T("Error: %s"), widget.EndpointErrorText(s.Error))
		}
		return i18n.T("Error")
	}
	return ""
}

// accountIcon is the row icon by account kind: for a mail account the
// provider's icon of GNOME Online Accounts when the account signs in with
// a provider and the theme has it, the generic mail icon otherwise; for
// any other kind its icon from integrations.RowFor (a task list for Jira,
// a calendar for CalDAV).
func accountIcon(a api.Account) string {
	if integrations.ShowsInAccounts(a) {
		return widget.ProviderIcon(signin.Provider(a.Config))
	}
	return integrations.RowFor(a, i18n.Tr).Icon
}

// jiraAccountIcon is the icon of a Jira account's row (Adwaita has no
// ticket).
const jiraAccountIcon = integrations.JiraIcon
