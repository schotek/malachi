package window

import (
	"errors"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/background"
	"github.com/schotek/malachi/ui/internal/settings"
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

// NewPreferences builds the dialog bound to s. Present it with Present(parent).
func NewPreferences(s *settings.Store) *PreferencesDialog {
	b := gtk.NewBuilderFromString(data.MustUI("preferences.ui"))

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
	}
	d.ConnectClosed(func() {
		d.closed = true
		for _, f := range cleanup {
			f()
		}
	})

	// TODO(part D): bind the Mail group to config.get / config.set.
	d.mailGroup.SetSensitive(false)

	return d
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
				d.AddToast(adw.NewToast("The desktop portal refused to change autostart"))
			case err != nil:
				set(!want)
				d.AddToast(adw.NewToast("Autostart needs xdg-desktop-portal"))
			case res.Autostart != want:
				set(!want)
				d.AddToast(adw.NewToast("Autostart was not granted"))
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
