// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/background"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// PreferencesDialog is the application preferences dialog, built from
// data/ui/preferences.blp. UI-only rows are bound to the settings store;
// "Launch at Login" goes through the Background portal; the Mail group is
// owned by the daemon (config.get / config.set).
type PreferencesDialog struct {
	*adw.PreferencesDialog

	launchAtLogin        *adw.SwitchRow
	runInBackground      *adw.SwitchRow
	markReadDelay        *adw.SpinRow
	confirmDelete        *adw.SwitchRow
	desktopNotifications *adw.SwitchRow
	notificationSound    *adw.SwitchRow

	mailGroup     *adw.PreferencesGroup
	checkInterval *adw.ComboRow
	remoteImages  *adw.ComboRow

	colorScheme *adw.ComboRow
	density     *adw.ComboRow
	showPreview *adw.SwitchRow
	showAvatars *adw.SwitchRow
	monochrome  *adw.SwitchRow
	monospace   *adw.SwitchRow
	textZoom    *adw.SpinRow

	closed bool
}

// Combo row entries, in the order of the StringLists in preferences.blp.
var (
	colorSchemeChoices = []settings.ColorScheme{
		settings.ColorSchemeSystem, settings.ColorSchemeLight, settings.ColorSchemeDark,
	}
	densityChoices = []settings.Density{settings.DensityComfortable, settings.DensityCompact}
)

// Mail group choices, in the order of the StringLists in preferences.blp.
var (
	intervalChoices = []int{0, 300, 900, 1800} // Manually, 5, 15, 30 minutes
	remoteChoices   = []api.RemoteContentPolicy{api.RemoteBlock, api.RemoteKnownSenders, api.RemoteAllow}
)

// NewPreferences builds the dialog bound to s and, for the Mail group, to
// the daemon through c. Present it with Present(parent).
func NewPreferences(s *settings.Store, c *client.Client) *PreferencesDialog {
	b := data.Builder("preferences.ui")

	d := &PreferencesDialog{
		PreferencesDialog:    b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		launchAtLogin:        b.GetObject("launch_at_login").Cast().(*adw.SwitchRow),
		runInBackground:      b.GetObject("run_in_background").Cast().(*adw.SwitchRow),
		markReadDelay:        b.GetObject("mark_read_delay").Cast().(*adw.SpinRow),
		confirmDelete:        b.GetObject("confirm_delete").Cast().(*adw.SwitchRow),
		desktopNotifications: b.GetObject("desktop_notifications").Cast().(*adw.SwitchRow),
		notificationSound:    b.GetObject("notification_sound").Cast().(*adw.SwitchRow),
		mailGroup:            b.GetObject("mail_group").Cast().(*adw.PreferencesGroup),
		checkInterval:        b.GetObject("check_interval").Cast().(*adw.ComboRow),
		remoteImages:         b.GetObject("remote_images").Cast().(*adw.ComboRow),
		colorScheme:          b.GetObject("color_scheme").Cast().(*adw.ComboRow),
		density:              b.GetObject("list_density").Cast().(*adw.ComboRow),
		showPreview:          b.GetObject("show_preview_line").Cast().(*adw.SwitchRow),
		showAvatars:          b.GetObject("show_avatars").Cast().(*adw.SwitchRow),
		monochrome:           b.GetObject("monochrome_avatars").Cast().(*adw.SwitchRow),
		monospace:            b.GetObject("monospace_plain_text").Cast().(*adw.SwitchRow),
		textZoom:             b.GetObject("text_zoom").Cast().(*adw.SpinRow),
	}

	// The dialog is rebuilt on every open while the store lives for the whole
	// process, so every binding is undone when the dialog closes.
	cleanup := []func(){
		s.Bind(settings.KeyRunInBackground, d.runInBackground.Object, "active"),
		s.Bind(settings.KeyMarkReadDelay, d.markReadDelay.Object, "value"),
		s.Bind(settings.KeyConfirmDelete, d.confirmDelete.Object, "active"),
		s.Bind(settings.KeyDesktopNotifications, d.desktopNotifications.Object, "active"),
		s.Bind(settings.KeyNotificationSound, d.notificationSound.Object, "active"),
		s.Bind(settings.KeyShowPreviewLine, d.showPreview.Object, "active"),
		s.Bind(settings.KeyShowAvatars, d.showAvatars.Object, "active"),
		s.Bind(settings.KeyMonochromeAvatars, d.monochrome.Object, "active"),
		s.Bind(settings.KeyMonospacePlainText, d.monospace.Object, "active"),
		s.Bind(settings.KeyTextZoom, d.textZoom.Object, "value"),
		bindChoice(s, settings.KeyColorScheme, d.colorScheme, colorSchemeChoices, s.ColorScheme, s.SetColorScheme),
		bindChoice(s, settings.KeyDensity, d.density, densityChoices, s.Density, s.SetDensity),
		d.bindLaunchAtLogin(s),
		d.bindMail(c),
	}
	d.ConnectClosed(func() {
		d.closed = true
		for _, f := range cleanup {
			f()
		}
	})

	return d
}

// bindMail loads the daemon preferences with config.get and writes every
// change back with config.set. The group stays insensitive until the load
// succeeds; a failed save shows a toast and reverts the combos.
func (d *PreferencesDialog) bindMail(c *client.Client) (unbind func()) {
	var (
		current api.Preferences
		syncing bool // set while combos are updated programmatically
	)
	d.mailGroup.SetSensitive(false)

	apply := func(p api.Preferences) {
		syncing = true
		d.checkInterval.SetSelected(nearestInterval(p.SyncIntervalSeconds))
		d.remoteImages.SetSelected(indexOfPolicy(p.RemoteContent))
		syncing = false
	}
	save := func() {
		if syncing {
			return
		}
		want := current
		if i := d.checkInterval.Selected(); i < uint(len(intervalChoices)) {
			want.SyncIntervalSeconds = intervalChoices[i]
		}
		if i := d.remoteImages.Selected(); i < uint(len(remoteChoices)) {
			want.RemoteContent = remoteChoices[i]
		}
		d.mailGroup.SetSensitive(false)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.ConfigSetResult
			err := c.Call(ctx, api.MethodConfigSet, api.ConfigSetParams{Preferences: want}, &res)
			glib.IdleAdd(func() {
				if d.closed {
					return
				}
				d.mailGroup.SetSensitive(true)
				if err != nil {
					d.AddToast(widget.PlainToast(widget.RPCErrorText(i18n.T("Saving mail settings"), err)))
					apply(current)
					return
				}
				current = res.Preferences
				apply(current)
			})
		}()
	}
	h1 := d.checkInterval.NotifyProperty("selected", save)
	h2 := d.remoteImages.NotifyProperty("selected", save)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ConfigGetResult
		err := c.Call(ctx, api.MethodConfigGet, api.ConfigGetParams{}, &res)
		glib.IdleAdd(func() {
			if d.closed {
				return
			}
			if err != nil {
				d.mailGroup.SetDescription(widget.RPCErrorText(i18n.T("Loading mail settings"), err))
				return
			}
			current = res.Preferences
			apply(current)
			d.mailGroup.SetSensitive(true)
		})
	}()

	return func() {
		d.checkInterval.HandlerDisconnect(h1)
		d.remoteImages.HandlerDisconnect(h2)
	}
}

// nearestInterval maps a sync interval in seconds to the closest combo
// position (0 stays "Manually").
func nearestInterval(seconds int) uint {
	if seconds <= 0 {
		return 0
	}
	best, bestDiff := uint(1), -1
	for i, v := range intervalChoices[1:] {
		diff := v - seconds
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = uint(i+1), diff
		}
	}
	return best
}

func indexOfPolicy(p api.RemoteContentPolicy) uint {
	for i, c := range remoteChoices {
		if c == p {
			return uint(i)
		}
	}
	return 0
}

// bindLaunchAtLogin drives the autostart switch through the Background
// portal. The portal's answer is authoritative: the switch (and the mirror
// key in the store) only flip when the portal granted the change.
func (d *PreferencesDialog) bindLaunchAtLogin(s *settings.Store) (unbind func()) {
	row := d.launchAtLogin
	var reverting bool
	set := func(v bool) {
		reverting = true
		row.SetActive(v)
		reverting = false
	}
	set(s.LaunchAtLogin())

	handle := row.NotifyProperty("active", func() {
		if reverting {
			return
		}
		want := row.Active()
		row.SetSensitive(false)
		background.RequestAutostart(want, func(res background.Result, err error) {
			if err == nil && res.Autostart == want {
				s.SetLaunchAtLogin(want)
			}
			if d.closed {
				return
			}
			row.SetSensitive(true)
			switch {
			case errors.Is(err, background.ErrCancelled):
				set(!want)
			case errors.Is(err, background.ErrFailed):
				// Typical inside a container or when launched outside a
				// .desktop file: the portal cannot identify the application.
				set(!want)
				d.AddToast(widget.PlainToast(i18n.T("The desktop portal refused to change autostart")))
			case err != nil:
				set(!want)
				d.AddToast(widget.PlainToast(i18n.T("Autostart needs xdg-desktop-portal")))
			case res.Autostart != want:
				set(!want)
				d.AddToast(widget.PlainToast(i18n.T("Autostart was not granted")))
			}
		})
	})
	return func() { row.HandlerDisconnect(handle) }
}

// bindChoice keeps a combo row and a string-enum setting in sync in both
// directions (GSettings cannot bind an enum key to a position property).
func bindChoice[T ~string](s *settings.Store, key string, row *adw.ComboRow, choices []T, get func() T, set func(T)) (unbind func()) {
	indexOf := func(v T) uint {
		for i, c := range choices {
			if c == v {
				return uint(i)
			}
		}
		return 0
	}
	row.SetSelected(indexOf(get()))
	handle := row.NotifyProperty("selected", func() {
		if i := row.Selected(); i < uint(len(choices)) {
			set(choices[i])
		}
	})
	remove := s.OnChanged(key, func() {
		if want := indexOf(get()); row.Selected() != want {
			row.SetSelected(want)
		}
	})
	return func() {
		remove()
		row.HandlerDisconnect(handle)
	}
}
