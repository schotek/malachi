// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/updates"
)

// Preferences → General → Updates (update_group in preferences.blp): the
// switch is the daemon's update.setPrefs {enabled}, the row under it the
// status line (updates.AboutLine) with Check Now (update.check), which
// GTK's About dialog has no room for. Both follow the application's
// update store (updates.go). No translatable text here: every text is
// ui/internal/updates'.

// bindUpdates fills the group from b and follows the store until unbind.
func (d *PreferencesDialog) bindUpdates(b *gtk.Builder, c *client.Client) (unbind func()) {
	group := b.GetObject("update_group").Cast().(*adw.PreferencesGroup)
	enabled := b.GetObject("update_enabled").Cast().(*adw.SwitchRow)
	status := b.GetObject("update_status").Cast().(*adw.ActionRow)
	checkNow := b.GetObject("update_check_now").Cast().(*gtk.Button)
	store := updateStore(c, d.log)

	group.SetTitle(updates.PrefsGroup(i18n.Tr))
	enabled.SetTitle(updates.PrefsTitle(i18n.Tr))
	enabled.SetSubtitle(updates.PrefsSubtitle(i18n.Tr))
	checkNow.SetLabel(updates.CheckNow(i18n.Tr))

	var showing bool
	show := func() {
		s := store.Status()
		// Without a status (no daemon yet, or one without the check) the
		// switch cannot say anything.
		enabled.SetSensitive(s != nil && s.State != api.UpdateUnsupported)
		showing = true
		enabled.SetActive(s != nil && s.Enabled)
		showing = false
		status.SetTitle(updates.AboutLine(s, updateCheckedAt, i18n.Tr))
		status.SetVisible(s != nil)
		checkNow.SetSensitive(updates.CanCheckNow(s))
	}
	handle := enabled.NotifyProperty("active", func() {
		if showing {
			return
		}
		// Insensitive until the daemon's answer shows the switch again
		// (a refusal shows it as it was).
		enabled.SetSensitive(false)
		store.SetEnabled(enabled.Active())
	})
	clicked := checkNow.ConnectClicked(func() {
		checkNow.SetSensitive(false)
		store.Check()
	})
	remove := store.Observe(show)
	show()
	if store.Status() == nil {
		store.Refresh()
	}
	return func() {
		remove()
		enabled.HandlerDisconnect(handle)
		checkNow.HandlerDisconnect(clicked)
	}
}
