// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/background"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/unified"
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

	log          *slog.Logger
	assist       *Assistant
	settings     *settings.Store
	cal          func() calendardata.DataSource // the main window's calendar source (Preferences → Calendar, G16); may be nil or return nil
	calendarPage *calendarPrefs                 // the Calendar page while open (preferences_calendar.go)
	contactsPage *contactsPrefs                 // the Contacts page while open (preferences_contacts.go)
	rulesPage    *rulesPrefs                    // the Rules page while open (preferences_rules.go)

	// The account rows: mailList on Accounts, integrationList on
	// Integrations (accounts_page.go); allAccounts is the last
	// account.list in the daemon's order, rpc the client they are bound to.
	rpc                  *client.Client
	allAccounts          []api.Account
	mailList             *accountList
	integrationList      *accountList
	integrationsPage     *adw.PreferencesPage
	integrationsServices *adw.PreferencesGroup
	// integrationsLocal holds the two local AI integrations' rows
	// (localRows), integrationsAI the AI section of the catalogue
	// (integrations_page.go).
	integrationsLocal *adw.PreferencesGroup
	integrationsAI    *adw.PreferencesGroup
	localRows         []*localRow

	launchAtLogin     *adw.SwitchRow
	runInBackground   *adw.SwitchRow
	boardGroup        *adw.PreferencesGroup
	boardShow         *adw.SwitchRow
	boardDefaultStyle *adw.ComboRow
	boardStartMode    *adw.ComboRow
	boardWindowsGroup *adw.PreferencesGroup
	// boardWindows are the windows' rows in board.States' order.
	boardWindows         [4]*adw.SpinRow
	markReadDelay        *adw.SpinRow
	confirmDelete        *adw.SwitchRow
	desktopNotifications *adw.SwitchRow
	notificationSound    *adw.SwitchRow

	mailGroup      *adw.PreferencesGroup
	checkInterval  *adw.ComboRow
	remoteImages   *adw.ComboRow
	offlineDays    *adw.ComboRow
	attachmentDays *adw.ComboRow
	neverStore     *adw.SwitchRow // never_store_attachments
	compressStore  *adw.SwitchRow
	storageRow     *adw.ActionRow
	storageSize    *gtk.Label

	colorScheme            *adw.ComboRow
	density                *adw.ComboRow
	showPreview            *adw.SwitchRow
	groupByConversation    *adw.SwitchRow
	conversationChatHTML   *adw.SwitchRow
	conversationChatMine   *adw.SwitchRow
	conversationChatOthers *adw.SwitchRow
	showAvatars            *adw.SwitchRow
	monochrome             *adw.SwitchRow
	monospace              *adw.SwitchRow
	textZoom               *adw.SpinRow

	assistantGroup  *adw.PreferencesGroup
	assistantSource *adw.ComboRow
	assistantMenu   *adw.SwitchRow
	assistantTarget *adw.ComboRow
	assistantModel  *adw.ComboRow

	boardTriageGroup    *adw.PreferencesGroup
	boardTriageConsent  *adw.SwitchRow
	boardTriageModel    *adw.ComboRow
	boardTriageAuto     *adw.SwitchRow
	boardTriageAccounts *adw.ExpanderRow
	boardTriageInterval *adw.ComboRow
	boardTriageDaily    *adw.ComboRow
	boardTriageStatus   *adw.ActionRow
	boardTriageUsage    *adw.ActionRow
	boardTriageTokens   *gtk.Label

	// toolbarGroup is Preferences → General → Toolbar (preferences_toolbar.go).
	toolbarGroup *adw.PreferencesGroup
	toolbarMode  *adw.ComboRow

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
// Assistant state as, and for Calendar to cal. Present it with Present(parent).
func NewPreferences(s *settings.Store, c *client.Client, as *Assistant, log *slog.Logger, cal func() calendardata.DataSource) *PreferencesDialog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("preferences.ui")

	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		log:               log.With("component", "preferences"),
		assist:            as, settings: s, cal: cal,
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
		launchAtLogin:        b.GetObject("launch_at_login").Cast().(*adw.SwitchRow),
		runInBackground:      b.GetObject("run_in_background").Cast().(*adw.SwitchRow),
		boardGroup:           b.GetObject("board_group").Cast().(*adw.PreferencesGroup),
		boardShow:            b.GetObject("board_show").Cast().(*adw.SwitchRow),
		boardDefaultStyle:    b.GetObject("board_default_style").Cast().(*adw.ComboRow),
		boardStartMode:       b.GetObject("board_start_mode").Cast().(*adw.ComboRow),
		boardWindowsGroup:    b.GetObject("board_windows_group").Cast().(*adw.PreferencesGroup),
		boardWindows: [4]*adw.SpinRow{
			b.GetObject("board_window_hot").Cast().(*adw.SpinRow),
			b.GetObject("board_window_you").Cast().(*adw.SpinRow),
			b.GetObject("board_window_them").Cast().(*adw.SpinRow),
			b.GetObject("board_window_info").Cast().(*adw.SpinRow),
		},
		markReadDelay:          b.GetObject("mark_read_delay").Cast().(*adw.SpinRow),
		confirmDelete:          b.GetObject("confirm_delete").Cast().(*adw.SwitchRow),
		desktopNotifications:   b.GetObject("desktop_notifications").Cast().(*adw.SwitchRow),
		notificationSound:      b.GetObject("notification_sound").Cast().(*adw.SwitchRow),
		mailGroup:              b.GetObject("mail_group").Cast().(*adw.PreferencesGroup),
		checkInterval:          b.GetObject("check_interval").Cast().(*adw.ComboRow),
		remoteImages:           b.GetObject("remote_images").Cast().(*adw.ComboRow),
		offlineDays:            b.GetObject("offline_days").Cast().(*adw.ComboRow),
		attachmentDays:         b.GetObject("attachment_days").Cast().(*adw.ComboRow),
		neverStore:             b.GetObject("never_store_attachments").Cast().(*adw.SwitchRow),
		compressStore:          b.GetObject("compress_store").Cast().(*adw.SwitchRow),
		storageRow:             b.GetObject("storage_row").Cast().(*adw.ActionRow),
		storageSize:            b.GetObject("storage_size").Cast().(*gtk.Label),
		colorScheme:            b.GetObject("color_scheme").Cast().(*adw.ComboRow),
		density:                b.GetObject("list_density").Cast().(*adw.ComboRow),
		showPreview:            b.GetObject("show_preview_line").Cast().(*adw.SwitchRow),
		groupByConversation:    b.GetObject("group_by_conversation").Cast().(*adw.SwitchRow),
		conversationChatHTML:   b.GetObject("conversation_chat_html").Cast().(*adw.SwitchRow),
		conversationChatMine:   b.GetObject("conversation_chat_truncate_mine").Cast().(*adw.SwitchRow),
		conversationChatOthers: b.GetObject("conversation_chat_truncate_others").Cast().(*adw.SwitchRow),
		showAvatars:            b.GetObject("show_avatars").Cast().(*adw.SwitchRow),
		monochrome:             b.GetObject("monochrome_avatars").Cast().(*adw.SwitchRow),
		monospace:              b.GetObject("monospace_plain_text").Cast().(*adw.SwitchRow),
		textZoom:               b.GetObject("text_zoom").Cast().(*adw.SpinRow),
		assistantGroup:         b.GetObject("assistant_group").Cast().(*adw.PreferencesGroup),
		assistantSource:        b.GetObject("assistant_source").Cast().(*adw.ComboRow),
		assistantMenu:          b.GetObject("assistant_menu_switch").Cast().(*adw.SwitchRow),
		assistantTarget:        b.GetObject("assistant_target").Cast().(*adw.ComboRow),
		assistantModel:         b.GetObject("assistant_model").Cast().(*adw.ComboRow),
		boardTriageGroup:       b.GetObject("board_triage_group").Cast().(*adw.PreferencesGroup),
		boardTriageConsent:     b.GetObject("board_triage_consent").Cast().(*adw.SwitchRow),
		boardTriageModel:       b.GetObject("board_triage_model").Cast().(*adw.ComboRow),
		boardTriageAuto:        b.GetObject("board_triage_auto").Cast().(*adw.SwitchRow),
		boardTriageAccounts:    b.GetObject("board_triage_accounts").Cast().(*adw.ExpanderRow),
		boardTriageInterval:    b.GetObject("board_triage_interval").Cast().(*adw.ComboRow),
		boardTriageDaily:       b.GetObject("board_triage_daily").Cast().(*adw.ComboRow),
		boardTriageStatus:      b.GetObject("board_triage_status").Cast().(*adw.ActionRow),
		boardTriageUsage:       b.GetObject("board_triage_usage").Cast().(*adw.ActionRow),
		boardTriageTokens:      b.GetObject("board_triage_usage_value").Cast().(*gtk.Label),
	}

	// Show All Inboxes: the sidebar item (unified.go), texts from Go.
	unifiedRow := b.GetObject("unified_inbox").Cast().(*adw.SwitchRow)
	unifiedRow.SetTitle(unified.PreferenceTitle(i18n.Tr))
	unifiedRow.SetSubtitle(unified.PreferenceSubtitle(i18n.Tr))

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
		s.Bind(settings.KeyUnifiedInbox, unifiedRow.Object, "active"),
		s.Bind(settings.KeyGroupByConversation, d.groupByConversation.Object, "active"),
		s.Bind(settings.KeyConversationChatHTML, d.conversationChatHTML.Object, "active"),
		s.Bind(settings.KeyConversationChatTruncateMine, d.conversationChatMine.Object, "active"),
		s.Bind(settings.KeyConversationChatTruncateOthers, d.conversationChatOthers.Object, "active"),
		s.Bind(settings.KeyShowAvatars, d.showAvatars.Object, "active"),
		s.Bind(settings.KeyMonochromeAvatars, d.monochrome.Object, "active"),
		s.Bind(settings.KeyMonospacePlainText, d.monospace.Object, "active"),
		s.Bind(settings.KeyTextZoom, d.textZoom.Object, "value"),
		bindChoice(s, settings.KeyColorScheme, d.colorScheme, colorSchemeChoices, s.ColorScheme, s.SetColorScheme),
		bindChoice(s, settings.KeyDensity, d.density, densityChoices, s.Density, s.SetDensity),
		d.bindBoard(s),
		d.bindLaunchAtLogin(s), d.bindUpdates(b, c),
		d.bindMail(c, refreshStorage),
		unbindStorage,
		d.bindAccounts(c),
		d.bindIntegrations(c),
		d.bindRules(b, c),
		d.bindCalendar(b, c),
		d.bindContacts(b, c, s),
		d.bindProfile(b, c),
		d.bindAssistant(s),
		d.bindBoardTriage(s, c),
		d.bindSecurity(c),
		d.bindToolbar(b, s),
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
		// TRANSLATORS: %s is a size such as "1.2 GB".
		lines = append(lines, fmt.Sprintf(i18n.T("Compression saves %s"), widget.FormatSize(r.SavedBytes)))
	}
	if r.RemoteAttachmentBytes > 0 {
		// TRANSLATORS: %s is a size such as "1.2 GB".
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
// application's Assistant state (assistantGroupFor). Assistant Uses
// chooses the provider (bindAssistantSource). The switch is set by hand,
// because what it shows depends on the bridge too (Claude Code only:
// ChatGPT and an AI provider account need no registration); "Open In" is
// bound to assistant-target and lists the targets this client does not
// support insensitive (targetListFactory), and says why the chosen
// target cannot run. While In App is chosen the "Model" row follows, with
// the models of the provider in effect (bindProviderModel). Without a
// bridge (the Flatpak build) the Assistant can never be shown with Claude
// Code, and the switch says to register first.
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
	d.assistantModel.SetTitle(assistant.PanelTexts(tr).Model)
	unbindSource := d.bindAssistantSource(s)
	unbindModel := bindProviderModel(d, s, d.assistantModel, modelPanel)

	var syncing bool
	update := func() {
		if d.closed {
			return
		}
		st := assistantGroupFor(s.AssistantMenu(), a.registered(), a.known(), a.problem(a.target()))
		local := a.source() == assistant.SourceClaude
		app := a.target() == assistant.App
		switch a.source() {
		case assistant.SourceChatGPT:
			d.assistantGroup.SetDescription(assistant.ChatGPTText(tr).Description)
		default:
			d.assistantGroup.SetDescription(texts.Description)
		}
		if !local && app {
			st.sensitive = true
			st.on = s.AssistantMenu()
			st.registerFirst = false
		}
		d.assistantMenu.SetSensitive(st.sensitive)
		d.assistantTarget.SetSensitive(st.sensitive || !local)
		syncing = true
		d.assistantMenu.SetActive(st.on)
		syncing = false
		menuSubtitle := ""
		if st.registerFirst {
			menuSubtitle = assistant.APIText(tr).RegisterInIntegrations
		}
		d.assistantMenu.SetSubtitle(menuSubtitle)
		d.assistantTarget.SetSubtitle(st.targetSubtitle)
		d.assistantModel.SetVisible(app)
		d.assistantModel.SetSensitive(st.sensitive || !local)
	}
	handle := d.assistantMenu.NotifyProperty("active", func() {
		if syncing {
			return
		}
		// The row is insensitive while the bridge is not registered.
		if !a.registered() && !(a.source() != assistant.SourceClaude && a.target() == assistant.App) {
			update()
			return
		}
		s.SetAssistantMenu(d.assistantMenu.Active())
	})
	unbindTarget := bindChoice(s, settings.KeyAssistantTarget, d.assistantTarget, assistantTargets, a.target, s.SetAssistantTarget)
	// The assistant keys are among the changes it reports.
	remove := a.OnChange(update)
	// Another claude chosen: the problem may differ.
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
		unbindSource()
		d.assistantMenu.HandlerDisconnect(handle)
	}
}

// bindAssistantSource keeps the Assistant Uses row with assistant-provider:
// its entries are Claude Code, ChatGPT and every AI provider account of
// the daemon (assistant.ProviderChoices through the Assistant's state),
// an entry that cannot run insensitive with why as its tooltip
// (choiceListFactory); the row's subtitle is that reason for the chosen
// entry, else the row's own line. A chosen account the daemon no longer
// has is listed as such. The entries are rebuilt outside GTK's selection
// notification (deferChoiceUpdate). The returned function stops following.
func (d *PreferencesDialog) bindAssistantSource(s *settings.Store) (unbind func()) {
	a := d.assist
	x := assistant.APIText(tr)
	row := d.assistantSource
	row.SetTitle(x.UsesRow)
	var (
		syncing bool
		choices []assistant.ProviderChoice
	)
	row.SetListFactory(&choiceListFactory(func() []assistant.ProviderChoice { return choices }).ListItemFactory)
	update := func() {
		if d.closed {
			return
		}
		current := s.AssistantProvider()
		choices = a.providerChoices()
		if !slices.ContainsFunc(choices, func(c assistant.ProviderChoice) bool { return c.ID == current }) {
			choices = append(choices, assistant.ProviderChoice{ID: current, Name: x.AccountMissing})
		}
		names := make([]string, len(choices))
		subtitle := x.UsesSubtitle
		selected := 0
		for i, c := range choices {
			names[i] = c.Name
			if c.ID == current {
				selected = i
				if !c.Available && c.Problem != "" {
					subtitle = c.Problem
				}
			}
		}
		syncing = true
		row.SetModel(gtk.NewStringList(names))
		row.SetSelected(uint(selected))
		syncing = false
		row.SetSubtitle(subtitle)
	}
	handle := row.NotifyProperty("selected", func() {
		if syncing {
			return
		}
		i := row.Selected()
		if i >= uint(len(choices)) {
			return
		}
		if c := choices[i]; c.ID != s.AssistantProvider() {
			s.SetAssistantProvider(c.ID)
		}
	})
	scheduleUpdate := d.deferChoiceUpdate(update)
	remove := a.OnChange(scheduleUpdate)
	removeKey := s.OnChanged(settings.KeyAssistantProvider, scheduleUpdate)
	update()
	return func() {
		remove()
		removeKey()
		row.HandlerDisconnect(handle)
	}
}

// choiceListFactory renders the entries of Assistant Uses in its popup:
// the name and, on the chosen one, a check mark, as the combo row's own
// factory does; an entry that cannot run (ProviderChoice.Available) is
// insensitive, with why as its tooltip.
func choiceListFactory(choices func() []assistant.ProviderChoice) *gtk.SignalListItemFactory {
	f := gtk.NewSignalListItemFactory()
	f.ConnectSetup(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		box := gtk.NewBox(gtk.OrientationHorizontal, 6)
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetUseMarkup(false)
		box.Append(label)
		check := gtk.NewImageFromIconName("object-select-symbolic")
		box.Append(check)
		item.SetChild(box)
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
		label, ok := box.FirstChild().(*gtk.Label)
		if !ok {
			return
		}
		check, ok := box.LastChild().(*gtk.Image)
		if !ok {
			return
		}
		i := int(item.Position())
		all := choices()
		if i < 0 || i >= len(all) {
			return
		}
		c := all[i]
		label.SetText(c.Name)
		item.SetSelectable(c.Available)
		box.SetSensitive(c.Available)
		box.SetTooltipText(c.Problem)
		// The chosen entry: the row shows the check on the selected
		// position, which the drop-down's own list keeps in sync.
		showCheck(check, item.Selected())
	})
	return f
}

// Preferences → AI → Board, the choices of its combo rows (macOS
// AIPaneViewController boardIntervals and boardDailyCaps).
var (
	// boardTriageIntervals are the minutes "At most every" offers between
	// automatic runs.
	boardTriageIntervals = []int{15, 30, 60, 180}
	// boardTriageDailyCaps are the conversations a day "Conversations a
	// day" offers for automatic runs.
	boardTriageDailyCaps = []int{20, 60, 150}
)

// boardGroupInputs are what the Board group shows: the triage's view, the
// daemon's board preferences (prefsKnown: they answered), whether consent
// is given, and while it is being given (the sheet, then the daemon:
// giving) what the consent switch shows (switchOn).
type boardGroupInputs struct {
	view             boardtriage.View
	prefs            api.BoardPreferences
	prefsKnown       bool
	consentGiven     bool
	giving, switchOn bool
}

// boardGroupState is how the Board group shows.
type boardGroupState struct {
	// shown: triage is offered (boardtriage.View.Offered); nothing else
	// matters while it is not.
	shown bool
	// description says why triage cannot run now ("" when it can).
	description string
	// consentOn and autoOn are what the two switches show; *Sensitive
	// whether they can be changed.
	consentOn, consentSensitive bool
	autoOn, autoSensitive       bool
	// minutes and cases are the schedule's values (the daemon's, or its
	// defaults before it answered); scheduleSensitive whether their rows
	// can be changed.
	minutes, cases    int
	scheduleSensitive bool
	// status is the status row's subtitle.
	status string
	// usageShown: the row of the tokens of the last 24 hours shows, with
	// usageValue as its value, usageDetail as its subtitle and
	// usageToolTip as its tooltip.
	usageShown                            bool
	usageValue, usageDetail, usageToolTip string
}

// boardGroupFor is the Board group for in (macOS AIPaneViewController
// updateBoardGroup). Turning off is always possible once the daemon's
// preferences are known; turning on needs a triage that can run (ready:
// the control is Triage or Stop). The consent switch shows the consent,
// or while it is being given what the user asked for; automatic triage
// can be turned on only with consent, and its interval and daily cap
// changed only while it is on; nothing changes while consent is being
// given.
func boardGroupFor(in boardGroupInputs, tr board.Translator) boardGroupState {
	v := in.view
	st := boardGroupState{shown: v.Offered()}
	if !st.shown {
		return st
	}
	ready := v.Control == boardtriage.ControlTriage || v.Control == boardtriage.ControlStop
	consent := in.consentGiven
	if in.giving {
		consent = in.switchOn
	}
	auto := in.prefsKnown && in.prefs.AutoTriage
	st.description = boardtriage.SettingsDescription(v, tr)
	st.consentOn = consent
	st.consentSensitive = in.prefsKnown && !in.giving && (ready || consent)
	st.autoOn = auto
	st.autoSensitive = in.prefsKnown && !in.giving && (auto || (consent && ready))
	st.minutes, st.cases = api.DefaultBoardAutoTriageMinutes, api.DefaultBoardAutoTriageDailyCases
	if in.prefsKnown {
		st.minutes, st.cases = in.prefs.AutoTriageMinutes, in.prefs.AutoTriageDailyCases
	}
	st.scheduleSensitive = in.prefsKnown && consent && auto && ready && !in.giving
	st.status = boardtriage.SettingsStatus(v)
	st.usageShown = v.UsageShown
	st.usageValue, st.usageDetail, st.usageToolTip = v.UsageValue, v.UsageDetail, v.UsageToolTip
	return st
}

// choiceValues are the items of a combo row of values: values, and
// selected after them when it is none of them (a value another client or
// the daemon chose shows as it is, not rounded to an offered one).
func choiceValues(values []int, selected int) []int {
	if slices.Contains(values, selected) {
		return values
	}
	return append(slices.Clip(values), selected)
}

// fillChoices gives row the items of values, labelled by label, unless it
// shows them already (shown, kept up to date), and selects selected.
func fillChoices(row *adw.ComboRow, shown *[]int, values []int, selected int, label func(int) string) {
	if !slices.Equal(*shown, values) {
		names := make([]string, len(values))
		for i, v := range values {
			names[i] = label(v)
		}
		row.SetModel(gtk.NewStringList(names))
		*shown = slices.Clone(values)
	}
	if i := slices.Index(values, selected); i >= 0 && row.Selected() != uint(i) {
		row.SetSelected(uint(i))
	}
}

// modelNames are the names of assistant.Models, in their order (the items
// of the Model rows).
func modelNames() []string {
	names := make([]string, len(assistant.Models))
	for i, m := range assistant.Models {
		names[i] = assistant.ModelName(tr, m)
	}
	return names
}

// bindBoardTriage fills the Board group (the board's triage, macOS
// AIPaneViewController's Board group; its texts are ui/internal/board's
// and the panel's) and keeps it with the application's triage
// (Assistant.BoardTriage), by boardGroupFor; without one the group stays
// hidden. The consent switch turned on asks "Let the Assistant Triage the
// Board?" over the dialog first (unless consent is given already) and then
// gives the consent (the board's assistant preference on, then both keys);
// a declined sheet leaves it off. Turned off, the consent is withdrawn: a
// run under way stops and automatic triage goes off too. Automatic triage
// and its two choices write the daemon's board preferences (optimistic: a
// refused write is taken back, and a toast in the dialog says why); Model
// is bound to board-triage-model and can be chosen whenever the group
// shows (the next run takes it). Whenever the AI page comes up the board
// is listed again, so the tokens of the last 24 hours are current.
func (d *PreferencesDialog) bindBoardTriage(s *settings.Store, rpc *client.Client) (unbind func()) {
	group := d.boardTriageGroup
	var bt *BoardTriage
	if d.assist != nil {
		bt = d.assist.BoardTriage()
	}
	if bt == nil {
		group.SetVisible(false)
		return func() {}
	}
	c, p := bt.Controller(), bt.Preferences()
	btr := providerBoardTranslator{d.assist, i18n.Tr}
	d.boardTriageConsent.SetTitle(board.TriageSettingsConsent(btr))
	d.boardTriageConsent.SetSubtitle(board.TriageSettingsConsentSubtitle(btr))
	d.boardTriageModel.SetTitle(assistant.PanelTexts(tr).Model)
	d.boardTriageModel.SetModel(gtk.NewStringList(modelNames()))
	d.boardTriageAuto.SetTitle(board.TriageSettingsAutomatic(btr))
	d.boardTriageInterval.SetTitle(board.TriageSettingsInterval(btr))
	d.boardTriageDaily.SetTitle(board.TriageSettingsDaily(btr))
	d.boardTriageUsage.SetTitle(board.TriageSettingsUsage(btr))

	var (
		// syncing is set while the rows are set from here.
		syncing bool
		// giving: the consent is being given (the sheet, then the daemon).
		giving bool
		// The items the interval and the daily cap rows show.
		intervals, caps []int
	)
	interval := func(m int) string { return board.TriageInterval(m, btr) }
	daily := func(n int) string { return board.TriageDailyCap(n, btr) }
	update := func() {
		if d.closed {
			return
		}
		prefs, known := p.Current()
		d.boardTriageConsent.SetSubtitle(board.TriageSettingsConsentSubtitle(btr))
		st := boardGroupFor(boardGroupInputs{
			view: c.View(), prefs: prefs, prefsKnown: known, consentGiven: c.ConsentGiven(),
			giving: giving, switchOn: d.boardTriageConsent.Active(),
		}, btr)
		group.SetVisible(st.shown)
		if !st.shown {
			return
		}
		group.SetDescription(st.description)
		syncing = true
		d.boardTriageConsent.SetActive(st.consentOn)
		d.boardTriageAuto.SetActive(st.autoOn)
		fillChoices(d.boardTriageInterval, &intervals, choiceValues(boardTriageIntervals, st.minutes), st.minutes, interval)
		fillChoices(d.boardTriageDaily, &caps, choiceValues(boardTriageDailyCaps, st.cases), st.cases, daily)
		syncing = false
		d.boardTriageConsent.SetSensitive(st.consentSensitive)
		d.boardTriageAuto.SetSensitive(st.autoSensitive)
		d.boardTriageInterval.SetSensitive(st.scheduleSensitive)
		d.boardTriageDaily.SetSensitive(st.scheduleSensitive)
		d.boardTriageStatus.SetSubtitle(st.status)
		d.boardTriageUsage.SetVisible(st.usageShown)
		d.boardTriageTokens.SetLabel(st.usageValue)
		d.boardTriageUsage.SetSubtitle(st.usageDetail)
		d.boardTriageUsage.SetTooltipText(st.usageToolTip)
	}

	consentHandle := d.boardTriageConsent.NotifyProperty("active", func() {
		if syncing || giving {
			return
		}
		if !d.boardTriageConsent.Active() {
			c.WithdrawConsent()
			update()
			return
		}
		giving = true
		update()
		finish := func(bool) {
			giving = false
			update()
		}
		if !c.NeedsConsent() {
			c.GiveConsent(false, finish)
			return
		}
		d.assist.askTriageConsent(d, func(allowed bool) {
			if !allowed || d.closed {
				finish(false)
				return
			}
			c.GiveConsent(false, finish)
		})
	})
	autoHandle := d.boardTriageAuto.NotifyProperty("active", func() {
		if syncing {
			return
		}
		on := d.boardTriageAuto.Active()
		p.Update(false, func(bp *api.BoardPreferences) { bp.AutoTriage = on }, nil)
	})
	// chosen writes the value of row's item i (items: what it shows) with
	// set, unless the daemon's preferences are not known or have it.
	chosen := func(row *adw.ComboRow, items *[]int, current func(api.BoardPreferences) int, set func(*api.BoardPreferences, int)) func() {
		return func() {
			if syncing {
				return
			}
			i := row.Selected()
			prefs, known := p.Current()
			if !known || i >= uint(len(*items)) || current(prefs) == (*items)[i] {
				return
			}
			v := (*items)[i]
			p.Update(false, func(bp *api.BoardPreferences) { set(bp, v) }, nil)
		}
	}
	intervalHandle := d.boardTriageInterval.NotifyProperty("selected", chosen(d.boardTriageInterval, &intervals,
		func(bp api.BoardPreferences) int { return bp.AutoTriageMinutes },
		func(bp *api.BoardPreferences, v int) { bp.AutoTriageMinutes = v }))
	dailyHandle := d.boardTriageDaily.NotifyProperty("selected", chosen(d.boardTriageDaily, &caps,
		func(bp api.BoardPreferences) int { return bp.AutoTriageDailyCases },
		func(bp *api.BoardPreferences, v int) { bp.AutoTriageDailyCases = v }))
	unbindModel := bindProviderModel(d, s, d.boardTriageModel, modelBoard)
	reloadAccounts, unbindAccounts := d.bindTriageAccounts(rpc, p)

	// The board is listed again whenever the AI page comes up, and its
	// accounts asked again.
	relist := func() {
		if !d.closed && d.VisiblePageName() == "ai" {
			c.RelistBoard()
			reloadAccounts()
		}
	}
	pageHandle := d.NotifyProperty("visible-page-name", relist)
	removeToasts := bt.showToastsIn(func(text string) { d.AddToast(widget.PlainToast(text)) })
	removeTriage := c.Observe(update)
	removePrefs := p.Observe(update)
	// The daemon's preferences, should none have come yet; Claude Code may
	// have been signed in meanwhile (bindAssistant dropped the locator's
	// answers, so this asks it afresh, together with the Claude Code row).
	if _, known := p.Current(); !known {
		p.Load(nil)
	}
	c.CheckSignIn()
	update()
	relist()
	return func() {
		removeTriage()
		removePrefs()
		removeToasts()
		unbindModel()
		unbindAccounts()
		d.HandlerDisconnect(pageHandle)
		d.boardTriageConsent.HandlerDisconnect(consentHandle)
		d.boardTriageAuto.HandlerDisconnect(autoHandle)
		d.boardTriageInterval.HandlerDisconnect(intervalHandle)
		d.boardTriageDaily.HandlerDisconnect(dailyHandle)
	}
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
