// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/background"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/mcpsetup"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// PreferencesDialog is the application preferences dialog, built from
// data/ui/preferences.blp. UI-only rows are bound to the settings store;
// "Launch at Login" goes through the Background portal; the Mail group and
// the Accounts page are owned by the daemon (config.get / config.set,
// system.storage, account.*); the AI page shows what the malachi-mcp
// bridge reports, and the Assistant group under it follows the
// application's Assistant state (assistant.go).
type PreferencesDialog struct {
	*adw.PreferencesDialog

	log    *slog.Logger
	assist *Assistant

	accountsGroup *adw.PreferencesGroup
	addAccount    *gtk.MenuButton
	accountsEmpty *adw.ActionRow
	accountRows   []*accountRow

	launchAtLogin        *adw.SwitchRow
	runInBackground      *adw.SwitchRow
	markReadDelay        *adw.SpinRow
	confirmDelete        *adw.SwitchRow
	desktopNotifications *adw.SwitchRow
	notificationSound    *adw.SwitchRow

	mailGroup      *adw.PreferencesGroup
	mcpGroup       *adw.PreferencesGroup
	checkInterval  *adw.ComboRow
	remoteImages   *adw.ComboRow
	offlineDays    *adw.ComboRow
	attachmentDays *adw.ComboRow
	neverStore     *adw.SwitchRow // never_store_attachments
	compressStore  *adw.SwitchRow
	storageRow     *adw.ActionRow
	storageSize    *gtk.Label

	colorScheme         *adw.ComboRow
	density             *adw.ComboRow
	showPreview         *adw.SwitchRow
	groupByConversation *adw.SwitchRow
	showAvatars         *adw.SwitchRow
	monochrome          *adw.SwitchRow
	monospace           *adw.SwitchRow
	textZoom            *adw.SpinRow

	mcpSwitch *adw.SwitchRow

	assistantGroup  *adw.PreferencesGroup
	assistantMenu   *adw.SwitchRow
	assistantTarget *adw.ComboRow
	claudeCodeRow   *adw.ActionRow
	claudeChoose    *gtk.Button
	assistantModel  *adw.ComboRow

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
	// retentionChoices are Preferences.OfflineDays per row: 1 week, 1 month,
	// 3 months, 1 year, Everything (0).
	retentionChoices = []int{7, 30, 90, 365, 0}
	// attachmentChoices are Preferences.AttachmentOfflineDays per row: Small
	// Attachments Only (-1), 1 week, 1 month, 3 months, Everything (0).
	attachmentChoices = []int{api.AttachmentOfflineNone, 7, 30, 90, 0}
)

// storagePollSeconds is how often the Disk Space Used row asks again while
// the dialog is open: the numbers move while stored mail is converted.
const storagePollSeconds = 5

// mcpStatusRetryDelays are the pauses before the repeats of a failed
// malachi-mcp status on the AI page (macOS MCPRegistrationController
// defaultStatusRetryDelays).
var mcpStatusRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// NewPreferences builds the dialog bound to s, for the Mail group and the
// Accounts page to the daemon through c, and for the AI page to the
// Assistant state as. Present it with Present(parent).
func NewPreferences(s *settings.Store, c *client.Client, as *Assistant, log *slog.Logger) *PreferencesDialog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("preferences.ui")

	d := &PreferencesDialog{
		PreferencesDialog:    b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		log:                  log.With("component", "preferences"),
		assist:               as,
		accountsGroup:        b.GetObject("accounts_group").Cast().(*adw.PreferencesGroup),
		addAccount:           b.GetObject("add_account_button").Cast().(*gtk.MenuButton),
		accountsEmpty:        b.GetObject("accounts_empty_row").Cast().(*adw.ActionRow),
		launchAtLogin:        b.GetObject("launch_at_login").Cast().(*adw.SwitchRow),
		runInBackground:      b.GetObject("run_in_background").Cast().(*adw.SwitchRow),
		markReadDelay:        b.GetObject("mark_read_delay").Cast().(*adw.SpinRow),
		confirmDelete:        b.GetObject("confirm_delete").Cast().(*adw.SwitchRow),
		desktopNotifications: b.GetObject("desktop_notifications").Cast().(*adw.SwitchRow),
		notificationSound:    b.GetObject("notification_sound").Cast().(*adw.SwitchRow),
		mailGroup:            b.GetObject("mail_group").Cast().(*adw.PreferencesGroup),
		checkInterval:        b.GetObject("check_interval").Cast().(*adw.ComboRow),
		remoteImages:         b.GetObject("remote_images").Cast().(*adw.ComboRow),
		offlineDays:          b.GetObject("offline_days").Cast().(*adw.ComboRow),
		attachmentDays:       b.GetObject("attachment_days").Cast().(*adw.ComboRow),
		neverStore:           b.GetObject("never_store_attachments").Cast().(*adw.SwitchRow),
		compressStore:        b.GetObject("compress_store").Cast().(*adw.SwitchRow),
		storageRow:           b.GetObject("storage_row").Cast().(*adw.ActionRow),
		storageSize:          b.GetObject("storage_size").Cast().(*gtk.Label),
		colorScheme:          b.GetObject("color_scheme").Cast().(*adw.ComboRow),
		density:              b.GetObject("list_density").Cast().(*adw.ComboRow),
		showPreview:          b.GetObject("show_preview_line").Cast().(*adw.SwitchRow),
		groupByConversation:  b.GetObject("group_by_conversation").Cast().(*adw.SwitchRow),
		showAvatars:          b.GetObject("show_avatars").Cast().(*adw.SwitchRow),
		monochrome:           b.GetObject("monochrome_avatars").Cast().(*adw.SwitchRow),
		monospace:            b.GetObject("monospace_plain_text").Cast().(*adw.SwitchRow),
		textZoom:             b.GetObject("text_zoom").Cast().(*adw.SpinRow),
		mcpSwitch:            b.GetObject("mcp_switch").Cast().(*adw.SwitchRow),
		mcpGroup:             b.GetObject("mcp_group").Cast().(*adw.PreferencesGroup),
		assistantGroup:       b.GetObject("assistant_group").Cast().(*adw.PreferencesGroup),
		assistantMenu:        b.GetObject("assistant_menu_switch").Cast().(*adw.SwitchRow),
		assistantTarget:      b.GetObject("assistant_target").Cast().(*adw.ComboRow),
		claudeCodeRow:        b.GetObject("assistant_claude_code").Cast().(*adw.ActionRow),
		claudeChoose:         b.GetObject("assistant_claude_choose").Cast().(*gtk.Button),
		assistantModel:       b.GetObject("assistant_model").Cast().(*adw.ComboRow),
	}

	// The dialog is rebuilt on every open while the store lives for the whole
	// process, so every binding is undone when the dialog closes.
	refreshStorage, unbindStorage := d.bindStorage(c)
	cleanup := []func(){
		s.Bind(settings.KeyRunInBackground, d.runInBackground.Object, "active"),
		s.Bind(settings.KeyMarkReadDelay, d.markReadDelay.Object, "value"),
		s.Bind(settings.KeyConfirmDelete, d.confirmDelete.Object, "active"),
		s.Bind(settings.KeyDesktopNotifications, d.desktopNotifications.Object, "active"),
		s.Bind(settings.KeyNotificationSound, d.notificationSound.Object, "active"),
		s.Bind(settings.KeyShowPreviewLine, d.showPreview.Object, "active"),
		s.Bind(settings.KeyGroupByConversation, d.groupByConversation.Object, "active"),
		s.Bind(settings.KeyShowAvatars, d.showAvatars.Object, "active"),
		s.Bind(settings.KeyMonochromeAvatars, d.monochrome.Object, "active"),
		s.Bind(settings.KeyMonospacePlainText, d.monospace.Object, "active"),
		s.Bind(settings.KeyTextZoom, d.textZoom.Object, "value"),
		bindChoice(s, settings.KeyColorScheme, d.colorScheme, colorSchemeChoices, s.ColorScheme, s.SetColorScheme),
		bindChoice(s, settings.KeyDensity, d.density, densityChoices, s.Density, s.SetDensity),
		d.bindLaunchAtLogin(s),
		d.bindMail(c, refreshStorage),
		unbindStorage,
		d.bindAccounts(c),
		d.bindMCP(),
		d.bindAssistant(s),
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
// succeeds; a failed save shows a toast and reverts the rows. A preference
// the daemon does not report (an older one: the field is absent) hides its
// row and is left absent in config.set, which leaves it unchanged; the
// attachment days go back as confirmed unless their row was changed
// (attachmentDaysToSave). While the daemon confirms that attachments are
// never stored, the attachment days do not apply and their row is
// insensitive (attachmentDaysApply). After a saved change refreshStorage
// asks for the disk space again.
func (d *PreferencesDialog) bindMail(c *client.Client, refreshStorage func()) (unbind func()) {
	var (
		current api.Preferences
		syncing bool // set while rows are updated programmatically
	)
	d.mailGroup.SetSensitive(false)

	apply := func(p api.Preferences) {
		syncing = true
		d.checkInterval.SetSelected(nearestInterval(p.SyncIntervalSeconds))
		d.remoteImages.SetSelected(indexOfPolicy(p.RemoteContent))
		d.offlineDays.SetSelected(indexOfRetention(p.OfflineDays))
		d.attachmentDays.SetVisible(p.AttachmentOfflineDays != nil)
		if p.AttachmentOfflineDays != nil {
			d.attachmentDays.SetSelected(indexOfAttachmentDays(*p.AttachmentOfflineDays))
		}
		// From what the daemon confirmed, so a failed save reverts it too.
		d.attachmentDays.SetSensitive(attachmentDaysApply(p))
		d.neverStore.SetVisible(p.NeverStoreAttachments != nil)
		if p.NeverStoreAttachments != nil {
			d.neverStore.SetActive(*p.NeverStoreAttachments)
		}
		d.compressStore.SetVisible(p.CompressStore != nil)
		if p.CompressStore != nil {
			d.compressStore.SetActive(*p.CompressStore)
		}
		syncing = false
	}
	save := func() {
		if syncing {
			return
		}
		// A copy of the last effective set: its pointers are shared with
		// current, so the optional fields get fresh ones and current keeps
		// what the daemon said, for reverting after a failure.
		want := current
		if i := d.checkInterval.Selected(); i < uint(len(intervalChoices)) {
			want.SyncIntervalSeconds = intervalChoices[i]
		}
		if i := d.remoteImages.Selected(); i < uint(len(remoteChoices)) {
			want.RemoteContent = remoteChoices[i]
		}
		if i := d.offlineDays.Selected(); i < uint(len(retentionChoices)) {
			want.OfflineDays = retentionChoices[i]
		}
		if current.AttachmentOfflineDays != nil {
			want.AttachmentOfflineDays = api.Ptr(attachmentDaysToSave(*current.AttachmentOfflineDays, d.attachmentDays.Selected()))
		}
		if current.NeverStoreAttachments != nil {
			want.NeverStoreAttachments = api.Ptr(d.neverStore.Active())
		}
		if current.CompressStore != nil {
			want.CompressStore = api.Ptr(d.compressStore.Active())
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
				// Compression or the attachments changed what the store
				// holds (or is about to, in the background).
				refreshStorage()
			})
		}()
	}
	h1 := d.checkInterval.NotifyProperty("selected", save)
	h2 := d.remoteImages.NotifyProperty("selected", save)
	h3 := d.offlineDays.NotifyProperty("selected", save)
	h4 := d.attachmentDays.NotifyProperty("selected", save)
	h5 := d.compressStore.NotifyProperty("active", save)
	h6 := d.neverStore.NotifyProperty("active", save)

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
		d.offlineDays.HandlerDisconnect(h3)
		d.attachmentDays.HandlerDisconnect(h4)
		d.compressStore.HandlerDisconnect(h5)
		d.neverStore.HandlerDisconnect(h6)
	}
}

// bindStorage fills the Disk Space Used row from system.storage: now, every
// storagePollSeconds while the dialog is open, and when refresh is called
// (after a saved preference). One call at a time; a refresh asked for
// while one runs follows it. A daemon without the method hides the row for
// good; any other failure takes the subtitle until the next answer, the
// size stays what it was.
func (d *PreferencesDialog) bindStorage(c *client.Client) (refresh func(), unbind func()) {
	var (
		inFlight, again, stopped bool
		timer                    glib.SourceHandle
	)
	stopTimer := func() {
		if timer != 0 {
			glib.SourceRemove(timer)
			timer = 0
		}
	}
	refresh = func() {
		if stopped || d.closed {
			return
		}
		if inFlight {
			again = true
			return
		}
		inFlight = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.SystemStorageResult
			err := c.Call(ctx, api.MethodSystemStorage, api.SystemStorageParams{}, &res)
			glib.IdleAdd(func() {
				inFlight = false
				if stopped || d.closed {
					return
				}
				switch {
				case methodUnsupported(err):
					stopped = true
					stopTimer()
					d.storageRow.SetVisible(false)
					return
				case err != nil:
					d.log.Debug("system.storage", "err", err)
					d.storageRow.SetSubtitle(widget.RPCErrorText(i18n.T("Measuring the disk space"), err))
				default:
					value, details := storageTexts(res)
					d.storageSize.SetLabel(value)
					d.storageRow.SetSubtitle(details)
				}
				if again {
					again = false
					refresh()
				}
			})
		}()
	}
	timer = glib.TimeoutSecondsAdd(storagePollSeconds, func() bool {
		if stopped || d.closed {
			timer = 0
			return false
		}
		refresh()
		return true
	})
	refresh()
	return refresh, func() {
		stopped = true
		stopTimer()
	}
}

// storageTexts is the Disk Space Used row for a system.storage answer: the
// total as its value, and as its details what compression saves and how
// much of the attachments is on the server only, one line each and only
// when there is any.
func storageTexts(r api.SystemStorageResult) (value, details string) {
	var lines []string
	switch r.Conversion {
	case api.StorageConversionRunning:
		lines = append(lines, i18n.T("Converting the stored mail in the background"))
	case api.StorageConversionNoSpace:
		lines = append(lines, i18n.T("Converting stopped: the disk is full"))
	}
	if r.SavedBytes > 0 {
		// TRANSLATORS: %s is a size such as "1.2 GiB".
		lines = append(lines, fmt.Sprintf(i18n.T("Compression saves %s"), widget.FormatSize(r.SavedBytes)))
	}
	if r.RemoteAttachmentBytes > 0 {
		// TRANSLATORS: %s is a size such as "1.2 GiB".
		lines = append(lines, fmt.Sprintf(i18n.T("%s of attachments are on the server only"), widget.FormatSize(r.RemoteAttachmentBytes)))
	}
	return widget.FormatSize(r.TotalBytes), strings.Join(lines, "\n")
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

// indexOfRetention maps Preferences.OfflineDays to the closest combo
// position; 0 (keep everything) and invalid negative values select
// "Everything".
func indexOfRetention(days int) uint {
	if days <= 0 {
		return uint(len(retentionChoices) - 1)
	}
	best, bestDiff := uint(0), -1
	for i, v := range retentionChoices {
		if v == 0 {
			continue
		}
		diff := v - days
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = uint(i), diff
		}
	}
	return best
}

// indexOfAttachmentDays maps Preferences.AttachmentOfflineDays to the
// closest combo position: a negative value selects "Small Attachments
// Only", 0 (keep every attachment) "Everything", a number of days the
// nearest of the offered ones, a tie going to the shorter.
func indexOfAttachmentDays(days int) uint {
	switch {
	case days < 0:
		return 0
	case days == 0:
		return uint(len(attachmentChoices) - 1)
	}
	best, bestDiff := uint(0), -1
	for i, v := range attachmentChoices {
		if v <= 0 {
			continue
		}
		diff := v - days
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = uint(i), diff
		}
	}
	return best
}

// attachmentDaysToSave is Preferences.AttachmentOfflineDays for config.set
// when the row shows position selected and current is what the daemon
// last confirmed: current unchanged while the row still shows it (the
// position indexOfAttachmentDays gives it), else the value of the chosen
// row. A value between the offered ones (14 days from config.toml) is thus
// never rounded to its nearest row by a save of another row.
func attachmentDaysToSave(current int, selected uint) int {
	if selected == indexOfAttachmentDays(current) || selected >= uint(len(attachmentChoices)) {
		return current
	}
	return attachmentChoices[selected]
}

// attachmentDaysApply reports whether Preferences.AttachmentOfflineDays
// decides which attachments are stored: not while none is stored at all
// (NeverStoreAttachments overrides it, docs/api.md §4.8). A daemon that
// does not report the latter never overrides.
func attachmentDaysApply(p api.Preferences) bool {
	return p.NeverStoreAttachments == nil || !*p.NeverStoreAttachments
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

// bindMCP drives the "Register with Claude" switch through the malachi-mcp
// bridge (status / install / uninstall). The Claude configuration files
// are the only state: the switch shows what the bridge reports, stays
// insensitive while a call runs and reverts when the change fails. The
// application's last status is shown at once, so the switch does not show
// "off" only because the dialog has not asked yet, and so is every newer
// one it learns while no call of the dialog runs; every status the dialog
// gets goes to the application (Assistant.Apply), whose menus follow it. A
// failed status check is asked again after 1, 2 and 4 s.
func (d *PreferencesDialog) bindMCP() (unbind func()) {
	row := d.mcpSwitch
	var (
		syncing bool // set while the switch is updated programmatically
		// op is bumped by every call; the reply of an older one is dropped.
		op       int
		changing bool // an install or uninstall runs
	)
	set := func(v bool) {
		syncing = true
		row.SetActive(v)
		syncing = false
	}
	row.SetSensitive(false)

	// Inside the Flatpak sandbox the bridge could neither see the Claude
	// apps' files nor be started by them; the group says so (bindMail
	// reports its failures in the same place).
	if os.Getenv("FLATPAK_ID") != "" {
		d.mcpGroup.SetDescription(i18n.T("Not available in the Flatpak build"))
		return func() {}
	}
	bridge, err := mcpsetup.Locate()
	if err != nil {
		d.log.Warn("locating the MCP bridge", "err", err)
		d.mcpGroup.SetDescription(i18n.T("The MCP bridge (malachi-mcp) was not found"))
		return func() {}
	}

	a := d.assist
	follow := func() {
		if d.closed || changing || !a.known() {
			return
		}
		set(a.registered())
		row.SetSensitive(true)
	}
	follow()
	removeFollow := a.OnChange(follow)

	// A failed status check is repeated after each of the pauses (a Claude
	// app may be rewriting its file just then), unless a newer call came
	// meanwhile; the last known state stays shown. Only when the repeats
	// are used up and no state is known does the group say why.
	retries := 0
	var query func()
	query = func() {
		op++
		my := op
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
			defer cancel()
			st, err := mcpsetup.Query(ctx, bridge)
			glib.IdleAdd(func() {
				if d.closed || my != op {
					return
				}
				if err != nil {
					d.log.Warn("malachi-mcp status", "err", err, "retry", retries < len(mcpStatusRetryDelays))
					if retries < len(mcpStatusRetryDelays) {
						delay := mcpStatusRetryDelays[retries]
						retries++
						glib.TimeoutAdd(uint(delay.Milliseconds()), func() bool {
							if !d.closed && my == op {
								query()
							}
							return false
						})
						return
					}
					if !a.known() {
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.mcpGroup.SetDescription(fmt.Sprintf(i18n.T("The MCP bridge did not answer: %s"), err))
					}
					return
				}
				retries = 0
				a.Apply(st)
				set(st.Registered())
				row.SetSensitive(true)
			})
		}()
	}
	query()

	handle := row.NotifyProperty("active", func() {
		if syncing {
			return
		}
		want := row.Active()
		row.SetSensitive(false)
		op++
		my := op
		changing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
			defer cancel()
			var (
				st  mcpsetup.Status
				err error
			)
			if want {
				st, err = mcpsetup.Install(ctx, bridge)
			} else {
				st, err = mcpsetup.Uninstall(ctx, bridge)
			}
			glib.IdleAdd(func() {
				if d.closed || my != op {
					return
				}
				changing = false
				row.SetSensitive(true)
				if err != nil {
					d.log.Warn("changing the MCP registration", "register", want, "err", err)
					set(!want)
					switch {
					case errors.Is(err, mcpsetup.ErrNoClient):
						d.AddToast(widget.PlainToast(i18n.T("No Claude app was found on this computer")))
					case want:
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("The MCP bridge could not be registered: %s"), err)))
					default:
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("The MCP bridge could not be unregistered: %s"), err)))
					}
					return
				}
				// The bridge's report is authoritative; it may differ from
				// what was asked for (e.g. one client left registered).
				retries = 0
				a.Apply(st)
				set(st.Registered())
			})
		}()
	})
	return func() {
		removeFollow()
		row.HandlerDisconnect(handle)
	}
}

// assistantGroupState is how the Assistant group shows: whether its rows
// can be changed, what the switch shows, whether it says to register first
// and what "Open In" says.
type assistantGroupState struct {
	sensitive, on, registerFirst bool
	targetSubtitle               string
}

// assistantGroupFor is the Assistant group for the assistant-menu key,
// the bridge's state (registered in a client; known: any status answered)
// and problem, why the chosen target cannot run the message actions
// (assistant.Problem). Registered, the switch shows the key and "Open In"
// the problem; known not to be registered, both rows are insensitive and
// the switch is off and says why, so the Assistant cannot be turned on
// without the bridge (assistant.Shown; the key keeps its value); not known
// yet, both rows are insensitive and the switch shows the key without a
// reason, rather than an "off" that may not be true.
func assistantGroupFor(menu, registered, known bool, problem string) assistantGroupState {
	st := assistantGroupState{sensitive: registered}
	switch {
	case registered:
		st.on = assistant.Shown(menu, true)
		st.targetSubtitle = problem
	case known:
		st.registerFirst = true
	default:
		st.on = menu
	}
	return st
}

// bindAssistant fills the Assistant group (its texts are
// ui/internal/assistant's) and keeps it with the assistant keys and the
// application's Assistant state (assistantGroupFor). The switch is set by
// hand, because what it shows depends on the bridge too; "Open In" is bound
// to assistant-target and lists the targets this client does not support
// insensitive (targetListFactory). While In App is chosen two more rows
// follow: "Claude Code", the executable the panel runs (bindClaudeCode),
// and "Model" (assistant-model). Without a bridge (the Flatpak build) the
// Assistant can never be shown, and the group is hidden.
func (d *PreferencesDialog) bindAssistant(s *settings.Store) (unbind func()) {
	a := d.assist
	texts := assistant.Texts(tr)
	d.assistantGroup.SetTitle(texts.Assistant)
	d.assistantGroup.SetDescription(texts.Description)
	d.assistantMenu.SetTitle(texts.ShowMenu)
	d.assistantTarget.SetTitle(texts.OpenIn)
	names := make([]string, len(assistantTargets))
	for i, t := range assistantTargets {
		names[i] = assistant.TargetName(tr, t)
	}
	d.assistantTarget.SetListFactory(&targetListFactory().ListItemFactory)
	d.assistantTarget.SetModel(gtk.NewStringList(names))
	panel := assistant.PanelTexts(tr)
	d.claudeCodeRow.SetTitle(assistant.TargetName(tr, assistant.Code))
	d.claudeChoose.SetLabel(panel.Choose)
	d.assistantModel.SetTitle(panel.Model)
	models := make([]string, len(assistant.Models))
	for i, m := range assistant.Models {
		models[i] = assistant.ModelName(tr, m)
	}
	d.assistantModel.SetModel(gtk.NewStringList(models))
	if !a.hasBridge() {
		d.assistantGroup.SetVisible(false)
		return func() {}
	}

	var syncing bool
	showClaudeCode, unbindClaude := d.bindClaudeCode(s)
	update := func() {
		if d.closed {
			return
		}
		st := assistantGroupFor(s.AssistantMenu(), a.registered(), a.known(), a.problem(a.target()))
		d.assistantMenu.SetSensitive(st.sensitive)
		d.assistantTarget.SetSensitive(st.sensitive)
		syncing = true
		d.assistantMenu.SetActive(st.on)
		syncing = false
		menuSubtitle := ""
		if st.registerFirst {
			menuSubtitle = texts.RegisterFirst
		}
		d.assistantMenu.SetSubtitle(menuSubtitle)
		// In App: the Claude Code row says what is wrong with it.
		app := a.target() == assistant.App
		if app {
			d.assistantTarget.SetSubtitle("")
		} else {
			d.assistantTarget.SetSubtitle(st.targetSubtitle)
		}
		for _, row := range []gtk.Widgetter{d.claudeCodeRow, d.assistantModel} {
			gtk.BaseWidget(row).SetVisible(app)
			gtk.BaseWidget(row).SetSensitive(st.sensitive)
		}
		if app {
			showClaudeCode()
		}
	}
	handle := d.assistantMenu.NotifyProperty("active", func() {
		if syncing {
			return
		}
		// The row is insensitive while the bridge is not registered.
		if !a.registered() {
			update()
			return
		}
		s.SetAssistantMenu(d.assistantMenu.Active())
	})
	unbindTarget := bindChoice(s, settings.KeyAssistantTarget, d.assistantTarget, assistantTargets, a.target, s.SetAssistantTarget)
	unbindModel := bindChoice(s, settings.KeyAssistantModel, d.assistantModel, assistant.Models, s.AssistantModel, s.SetAssistantModel)
	// The assistant keys are among the changes it reports.
	remove := a.OnChange(update)
	// Another claude chosen: its row looks again.
	removePath := s.OnChanged(settings.KeyAssistantClaudePath, update)
	// A Claude app may have been installed, or Claude Code signed in,
	// meanwhile.
	a.locator.Refresh()
	update()
	a.RefreshHandlers()
	return func() {
		remove()
		removePath()
		unbindTarget()
		unbindModel()
		unbindClaude()
		d.assistantMenu.HandlerDisconnect(handle)
	}
}

// bindClaudeCode drives the Claude Code row: show fills its subtitle with
// the executable the panel runs, its version and whether it is signed in
// (asked once, then kept by the locator until the dialog opens again, the
// path changes or a sign-in starts or ends), or that none was found;
// "Choose…" picks one of the user's own (assistant-claude-path). The file
// found automatically stores nothing, so choosing it goes back to looking
// in the usual places.
//
// A second button, made here, is what the row offers: "Sign In…" while
// Claude Code says it is signed out, which runs Claude Code's own sign-in
// in the browser (the locator's, shared with the panel: the row says
// "Waiting for the sign-in in your browser…" whoever started it, and a
// second click starts it afresh), and "Get Claude Code…" while there is
// none, which opens Anthropic's page with the installers.
func (d *PreferencesDialog) bindClaudeCode(s *settings.Store) (show func(), unbind func()) {
	a := d.assist
	offer := assistantpanel.OfferNone
	button := gtk.NewButtonWithLabel("")
	button.SetVAlign(gtk.AlignCenter)
	button.SetVisible(false)
	d.claudeCodeRow.AddSuffix(button)
	setOffer := func(o assistantpanel.Offer) {
		offer = o
		button.SetLabel(offerLabel(o))
		button.SetVisible(o != assistantpanel.OfferNone)
	}
	gen := 0
	show = func() {
		gen++
		my := gen
		path := a.locator.Locate()
		if path == "" {
			d.claudeCodeRow.SetSubtitle(assistant.Problem(tr, assistant.App, assistant.Availability{}))
			setOffer(assistantpanel.OfferInstall)
			return
		}
		if !strings.HasPrefix(d.claudeCodeRow.Subtitle(), path) {
			d.claudeCodeRow.SetSubtitle(path)
			setOffer(assistantpanel.OfferNone)
		}
		a.locator.Version(func(version string) {
			a.locator.SignedIn(func(in assistantpanel.SignIn) {
				if d.closed || my != gen {
					return
				}
				d.claudeCodeRow.SetSubtitle(claudeCodeState(path, version, in, a.locator.SigningIn()))
				if in.Known && !in.SignedIn {
					setOffer(assistantpanel.OfferSignIn)
				} else {
					setOffer(assistantpanel.OfferNone)
				}
			})
		})
	}
	offered := button.ConnectClicked(func() {
		switch offer {
		case assistantpanel.OfferInstall:
			// The preferences are a dialog, not a window: no parent.
			widget.LaunchURI(nil, assistant.InstallURL, func(err error) {
				if err != nil && !d.closed {
					d.AddToast(widget.PlainToast(widget.LaunchErrorText(err)))
				}
			})
		case assistantpanel.OfferSignIn:
			a.locator.SignIn(func(r assistantpanel.SignInResult) {
				if d.closed {
					return
				}
				switch r.Outcome {
				case assistantpanel.SignInFailed:
					d.AddToast(widget.PlainToast(assistant.SignInFailedText(tr, r.Reason)))
				case assistantpanel.SignInTimedOut:
					d.AddToast(widget.PlainToast(assistant.SignInTexts(tr).TimedOut))
				}
			})
		}
	})
	// A sign-in started or ended, here or in the panel: the row looks again.
	unwatch := a.locator.OnSignInChange(func() {
		if !d.closed {
			show()
		}
	})
	handle := d.claudeChoose.ConnectClicked(func() {
		dlg := gtk.NewFileDialog()
		dlg.SetTitle(assistant.TargetName(tr, assistant.Code))
		if current := a.locator.Locate(); current != "" {
			dlg.SetInitialFolder(gio.NewFileForPath(filepath.Dir(current)))
		}
		// The preferences are a dialog, not a window: no parent.
		dlg.Open(context.Background(), nil, func(r gio.AsyncResulter) {
			f, err := dlg.OpenFinish(r)
			if err != nil || d.closed {
				return // dismissed
			}
			chosen := f.Path()
			if chosen == "" || !assistantpanel.IsExecutableFile(chosen) {
				return
			}
			value := chosen
			if chosen == a.locator.AutomaticPath() {
				value = ""
			}
			a.locator.Refresh()
			if s.AssistantClaudePath() == value {
				show()
				return
			}
			// The change handlers look again.
			s.SetAssistantClaudePath(value)
		})
	})
	return show, func() {
		d.claudeChoose.HandlerDisconnect(handle)
		button.HandlerDisconnect(offered)
		unwatch()
	}
}

// claudeCodeState is the Claude Code row's subtitle: "path · version ·
// Signed in"; what is not known is left out, and while a sign-in is under
// way (signingIn) the row says that it waits for the browser instead.
func claudeCodeState(path, version string, in assistantpanel.SignIn, signingIn bool) string {
	t := assistant.PanelTexts(tr)
	parts := []string{path}
	if version != "" {
		parts = append(parts, version)
	}
	switch {
	case in.Known && in.SignedIn:
		parts = append(parts, t.SignedIn)
	case signingIn:
		parts = append(parts, assistant.SignInTexts(tr).Waiting)
	case in.Known:
		parts = append(parts, t.NotSignedInShort)
	}
	return strings.Join(parts, " · ")
}

// targetListFactory renders the choices of "Open In" in its popup, in the
// order of assistantTargets: the name and, on the chosen one, a check mark,
// as the combo row's own factory does; a target this client does not
// support (supportedTarget) is insensitive and cannot be chosen.
func targetListFactory() *gtk.SignalListItemFactory {
	f := gtk.NewSignalListItemFactory()
	f.ConnectSetup(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetHExpand(true)
		check := gtk.NewImageFromIconName("object-select-symbolic")
		box := gtk.NewBox(gtk.OrientationHorizontal, 6)
		box.Append(l)
		box.Append(check)
		item.SetChild(box)
		item.NotifyProperty("selected", func() { showCheck(check, item.Selected()) })
	})
	f.ConnectBind(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		box, ok := item.Child().(*gtk.Box)
		if !ok {
			return
		}
		l, ok := box.FirstChild().(*gtk.Label)
		if !ok {
			return
		}
		check, ok := box.LastChild().(*gtk.Image)
		if !ok {
			return
		}
		if s, ok := item.Item().Cast().(*gtk.StringObject); ok {
			l.SetLabel(s.String())
		}
		pos := item.Position()
		supported := pos < uint(len(assistantTargets)) && supportedTarget(assistantTargets[pos])
		box.SetSensitive(supported)
		item.SetActivatable(supported)
		item.SetSelectable(supported)
		showCheck(check, item.Selected())
	})
	return f
}

// showCheck shows or hides a list item's check mark without moving the
// label.
func showCheck(check *gtk.Image, on bool) {
	if on {
		check.SetOpacity(1)
	} else {
		check.SetOpacity(0)
	}
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
