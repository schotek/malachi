// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// accountRow is one account in the Accounts page: name, address, a status
// label, the pause switch and the remove button. Everything shown comes
// from the daemon, so nothing is markup.
type accountRow struct {
	*adw.ActionRow

	account   api.Account
	handle    *gtk.Image // drag handle; the whole row is the drop target
	status    *gtk.Label
	toggle    *gtk.Switch
	edit      *gtk.Button
	remove    *gtk.Button
	reverting bool // the switch is being set programmatically

	// hint is the insertion line shown while a drag hovers this row.
	hint dropHint
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

// bindAccounts fills the Accounts page from account.list and wires the add
// button. The page reloads after its own actions; the group stays
// insensitive until the first load succeeds.
func (d *PreferencesDialog) bindAccounts(c *client.Client) (unbind func()) {
	d.accountsGroup.SetSensitive(false)
	d.loadAccounts(c)

	// The wizard is built here rather than through app.add-account so the
	// page can reload itself: the preferences dialog does not receive
	// notify.accountsChanged.
	handle := d.addAccount.ConnectClicked(func() {
		wz := accountwizard.New(c, d.log)
		wz.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
			d.loadAccounts(c)
			// TRANSLATORS: %s is the new account's e-mail address.
			d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("Added %s"), cfg.Email)))
		}
		wz.Present(d)
	})
	return func() { d.addAccount.HandlerDisconnect(handle) }
}

// loadAccounts runs account.list and rebuilds the rows.
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
				d.accountsGroup.SetDescription(widget.RPCErrorText(i18n.T("Loading accounts"), err))
				d.accountsGroup.SetSensitive(false)
				return
			}
			d.accountsGroup.SetDescription("")
			d.setAccounts(c, res.Accounts)
			d.accountsGroup.SetSensitive(true)
		})
	}()
}

// setAccounts replaces the account rows.
func (d *PreferencesDialog) setAccounts(c *client.Client, accounts []api.Account) {
	for _, row := range d.accountRows {
		d.accountsGroup.Remove(row)
	}
	d.accountRows = d.accountRows[:0]
	for _, a := range accounts {
		row := d.newAccountRow(c, a)
		d.accountRows = append(d.accountRows, row)
		d.accountsGroup.Add(row)
	}
	d.accountsEmpty.SetVisible(len(accounts) == 0)
}

func (d *PreferencesDialog) newAccountRow(c *client.Client, a api.Account) *accountRow {
	row := &accountRow{ActionRow: adw.NewActionRow(), account: a}
	row.SetUseMarkup(false)
	row.AddCSSClass("account-row")
	row.SetTitle(accountRowTitle(a))
	row.SetSubtitle(a.Config.Email)

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
	row.AddSuffix(row.status)

	row.toggle = gtk.NewSwitch()
	row.toggle.SetVAlign(gtk.AlignCenter)
	row.toggle.SetTooltipText(i18n.T("Enabled"))
	row.AddSuffix(row.toggle)
	row.SetActivatableWidget(row.toggle)

	row.edit = gtk.NewButtonFromIconName("document-edit-symbolic")
	row.edit.SetVAlign(gtk.AlignCenter)
	row.edit.AddCSSClass("flat")
	row.edit.SetTooltipText(i18n.T("Edit Account"))
	row.AddSuffix(row.edit)

	row.remove = gtk.NewButtonFromIconName("user-trash-symbolic")
	row.remove.SetVAlign(gtk.AlignCenter)
	row.remove.AddCSSClass("flat")
	row.remove.SetTooltipText(i18n.T("Remove Account"))
	row.AddSuffix(row.remove)

	row.apply(a)

	row.toggle.NotifyProperty("active", func() {
		if row.reverting {
			return
		}
		d.setAccountEnabled(c, row, row.toggle.Active())
	})
	row.edit.ConnectClicked(func() { d.editAccount(c, row) })
	row.remove.ConnectClicked(func() { d.removeAccount(c, row) })
	d.addRowReorder(c, row)
	return row
}

// apply shows the account's current state.
func (r *accountRow) apply(a api.Account) {
	r.account = a
	r.reverting = true
	r.toggle.SetActive(a.Enabled)
	r.reverting = false
	text := accountStatusText(a.State.Status)
	r.status.SetText(text)
	r.status.SetVisible(text != "")
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

// editAccount opens the wizard prefilled with the row's account.
func (d *PreferencesDialog) editAccount(c *client.Client, row *accountRow) {
	wz := accountwizard.NewEdit(c, d.log, row.account)
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
	// TRANSLATORS: %s is the account's e-mail address.
	body := fmt.Sprintf(i18n.T("%s will be removed from Malachi Mail. Mail on the server is not affected."), row.account.Config.Email)
	widget.ConfirmDestructiveExtra(d, i18n.T("Remove this account?"), body, i18n.T("_Remove"), check, func() {
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
			})
		}()
	})
}

// accountRowTitle is the account name, or the address when unnamed.
func accountRowTitle(a api.Account) string {
	if a.Config.Name != "" {
		return a.Config.Name
	}
	return a.Config.Email
}

// accountStatusText is the short status shown next to the switch; empty
// for the unremarkable idle state.
func accountStatusText(s api.SyncStatus) string {
	switch s {
	case api.SyncDisabled:
		return i18n.T("Paused")
	case api.SyncSyncing:
		return i18n.T("Syncing…")
	case api.SyncOffline:
		return i18n.T("Offline")
	case api.SyncAuthRequired:
		return i18n.T("Sign-in required")
	case api.SyncError:
		return i18n.T("Error")
	}
	return ""
}

// accountIcon is the row icon by account kind: the provider's icon of
// GNOME Online Accounts when the account signs in there and the theme
// has it, the generic mail icon otherwise.
func accountIcon(a api.Account) string {
	return widget.ProviderIcon(widget.AccountProvider(a.Config))
}
