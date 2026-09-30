// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jiraaccount

import (
	"log/slog"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Dialog is the settings of a Jira account: one scrolling page
// (data/ui/jira_account.blp) under a header bar with Cancel and Save, the
// banner of a failed call under it. Its sections are the site (read only,
// with Replace Token…), the spaces, the synchronisation, the folders with
// the closed statuses, the notification e-mails and the comments posted
// by bots. The texts, the rules and the calls live in the Controller;
// this shows what it holds (refresh, on every change) and feeds back what
// the user did. Everything from the site (its address, the user, the
// names of spaces and statuses) and every entry of the lists is plain
// text.
//
// A mail account is edited in the account assistant, which builds its
// pages from imap and smtp; a Jira account has neither, so the window
// opens this dialog for it instead.
type Dialog struct {
	*adw.Dialog

	// OnSaved runs on the main loop after account.update stored the
	// account, before the dialog closes; cfg is what was stored. A dialog
	// that had nothing to save closes without it. May be nil.
	OnSaved func(cfg api.AccountConfig)
	// OnReplaceToken runs for "Replace Token…" with the stored account.
	// The window opens the Jira account assistant in its edit mode over
	// this dialog, which stays open with what was edited, and calls
	// TokenReplaced once the assistant stored the new token (macOS
	// JiraAccountWindowController.replaceToken). May be nil.
	OnReplaceToken func(a api.Account)

	ctl     *Controller
	started bool
	// applying is the widgets being set from the controller: their
	// signals are not the user's.
	applying bool

	windowTitle  *adw.WindowTitle
	cancel, save *gtk.Button
	spinner      *adw.Spinner
	banner       *adw.Banner

	siteGroup       *adw.PreferencesGroup
	addressRow      *adw.ActionRow
	deploymentLabel *gtk.Label
	nameRow         *adw.EntryRow
	userRow         *adw.ActionRow
	userLabel       *gtk.Label
	tokenRow        *adw.ActionRow
	tokenButton     *gtk.Button

	spacesGroup   *adw.PreferencesGroup
	spacesList    *gtk.ListBox
	spacesProblem *gtk.Label
	// spaceRows are the rows shown, spaceChecks their check boxes.
	spaceRows   []jira.SpaceRow
	spaceChecks []*gtk.CheckButton

	syncGroup     *adw.PreferencesGroup
	offlineRow    *adw.ComboRow
	onlyMineRow   *adw.SwitchRow
	showEventsRow *adw.SwitchRow

	foldersGroup *adw.PreferencesGroup
	// folderRows are the switches of jira.VirtualFolders, in its order.
	folderRows []*adw.SwitchRow
	statuses   *statusPicker

	notificationGroup *adw.PreferencesGroup
	modeRow           *adw.ComboRow
	botsGroup         *adw.PreferencesGroup
	// editors are the lists: the senders first, then the three of the
	// bots.
	editors []*listEditor
}

// New builds the settings of the stored account a. Present it with
// Present; it asks the daemon for the spaces and statuses then.
func New(c *client.Client, log *slog.Logger, a api.Account) *Dialog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("jira_account.ui")
	group := func(id string) *adw.PreferencesGroup { return b.GetObject(id).Cast().(*adw.PreferencesGroup) }
	action := func(id string) *adw.ActionRow { return b.GetObject(id).Cast().(*adw.ActionRow) }
	label := func(id string) *gtk.Label { return b.GetObject(id).Cast().(*gtk.Label) }
	button := func(id string) *gtk.Button { return b.GetObject(id).Cast().(*gtk.Button) }
	combo := func(id string) *adw.ComboRow { return b.GetObject(id).Cast().(*adw.ComboRow) }
	switchRow := func(id string) *adw.SwitchRow { return b.GetObject(id).Cast().(*adw.SwitchRow) }

	d := &Dialog{
		Dialog:            b.GetObject("jira_account_dialog").Cast().(*adw.Dialog),
		windowTitle:       b.GetObject("window_title").Cast().(*adw.WindowTitle),
		cancel:            button("cancel_button"),
		save:              button("save_button"),
		spinner:           b.GetObject("progress_spinner").Cast().(*adw.Spinner),
		banner:            b.GetObject("banner").Cast().(*adw.Banner),
		siteGroup:         group("site_group"),
		addressRow:        action("address_row"),
		deploymentLabel:   label("deployment_label"),
		nameRow:           b.GetObject("name_row").Cast().(*adw.EntryRow),
		userRow:           action("user_row"),
		userLabel:         label("user_label"),
		tokenRow:          action("token_row"),
		tokenButton:       button("token_button"),
		spacesGroup:       group("spaces_group"),
		spacesList:        b.GetObject("spaces_list").Cast().(*gtk.ListBox),
		spacesProblem:     label("spaces_problem"),
		syncGroup:         group("sync_group"),
		offlineRow:        combo("offline_row"),
		onlyMineRow:       switchRow("only_mine_row"),
		showEventsRow:     switchRow("show_events_row"),
		foldersGroup:      group("folders_group"),
		notificationGroup: group("notification_group"),
		modeRow:           combo("mode_row"),
		botsGroup:         group("bots_group"),
	}
	d.ctl = NewController(c, a, Env{
		Tr:        i18n.Tr,
		ErrorText: widget.RPCErrorText,
		// Replies arrive on the RPC client's goroutines.
		Post: func(f func()) { glib.IdleAdd(f) },
		Log:  log.With("component", "jiraaccount"),
	})
	d.build()
	d.wire()
	d.refresh()
	return d
}

// Present shows the dialog over parent and, the first time, asks for the
// spaces and statuses.
func (d *Dialog) Present(parent gtk.Widgetter) {
	d.Dialog.Present(parent)
	if !d.started {
		d.started = true
		d.ctl.Start()
	}
}

// TokenReplaced tells the dialog that the account assistant stored a new
// token for the account: the spaces and statuses are asked for again with
// it, and the page keeps what was edited.
func (d *Dialog) TokenReplaced() {
	d.ctl.TokenReplaced()
}

// build sets the texts and adds the rows that depend on the account;
// nothing is connected yet (wire), so setting the widgets reports nothing.
func (d *Dialog) build() {
	t := d.ctl.Texts
	d.SetTitle(t.Title)
	d.windowTitle.SetTitle(t.Title)

	d.siteGroup.SetTitle(t.SiteTitle)
	d.addressRow.SetTitle(t.SiteAddress)
	d.nameRow.SetTitle(t.AccountName)
	// Once: from here on the field is where the name changes.
	d.nameRow.SetText(d.ctl.Form().Name)
	d.userRow.SetTitle(t.SignedInAs)
	d.tokenButton.SetLabel(t.ReplaceToken)

	d.spacesGroup.SetTitle(t.SpacesTitle)
	d.spacesGroup.SetDescription(t.SpacesDescription)
	empty := gtk.NewLabel(t.NoSpaces)
	empty.SetWrap(true)
	empty.SetMarginTop(12)
	empty.SetMarginBottom(12)
	empty.SetMarginStart(12)
	empty.SetMarginEnd(12)
	empty.AddCSSClass("dim-label")
	d.spacesList.SetPlaceholder(empty)

	d.syncGroup.SetTitle(t.SyncTitle)
	d.offlineRow.SetTitle(t.KeepOffline)
	d.offlineRow.SetSubtitle(t.KeepOfflineSubtitle)
	d.offlineRow.SetModel(gtk.NewStringList(d.ctl.OfflineLabels()))
	d.onlyMineRow.SetTitle(t.OnlyMine)
	d.onlyMineRow.SetSubtitle(t.OnlyMineSubtitle)
	d.showEventsRow.SetTitle(t.ShowEvents)

	d.foldersGroup.SetTitle(t.FoldersTitle)
	for _, v := range jira.VirtualFolders {
		row := adw.NewSwitchRow()
		row.SetTitle(jira.VirtualFolderTitle(v, i18n.Tr))
		d.foldersGroup.Add(row)
		d.folderRows = append(d.folderRows, row)
	}
	d.statuses = newStatusPicker(t.ClosedStatuses, t.ClosedStatusesSubtitle)
	d.foldersGroup.Add(d.statuses)

	d.notificationGroup.SetTitle(t.NotificationTitle)
	d.modeRow.SetTitle(t.NotificationMode)
	d.modeRow.SetModel(gtk.NewStringList(d.ctl.NotificationLabels()))
	senders := newListEditor(jira.ListSenders, t.Senders, t.SendersSubtitle, t.Add, t.Remove)
	senders.setPlaceholder(d.ctl.SendersPlaceholder())
	d.notificationGroup.Add(senders)

	d.botsGroup.SetTitle(t.BotsTitle)
	d.editors = []*listEditor{
		senders,
		newListEditor(jira.ListBotNames, t.BotNames, t.BotNamesSubtitle, t.Add, t.Remove),
		newListEditor(jira.ListMetadataFilters, t.HiddenLines, t.HiddenLinesSubtitle, t.Add, t.Remove),
		newListEditor(jira.ListAuthorPrefixes, t.NamePrefixes, t.NamePrefixesSubtitle, t.Add, t.Remove),
	}
	for _, e := range d.editors[1:] {
		d.botsGroup.Add(e)
	}
}

// wire connects the widgets to the controller and the controller to the
// widgets.
func (d *Dialog) wire() {
	d.cancel.ConnectClicked(d.dismiss)
	d.save.ConnectClicked(d.onSave)
	d.tokenButton.ConnectClicked(d.ctl.ReplaceToken)

	d.nameRow.ConnectChanged(func() {
		if !d.applying {
			d.ctl.SetName(d.nameRow.Text())
		}
	})
	d.offlineRow.NotifyProperty("selected", func() {
		if !d.applying {
			d.ctl.SetOfflineIndex(int(d.offlineRow.Selected()))
		}
	})
	d.onlyMineRow.NotifyProperty("active", func() {
		if !d.applying {
			d.ctl.SetOnlyMine(d.onlyMineRow.Active())
		}
	})
	d.showEventsRow.NotifyProperty("active", func() {
		if !d.applying {
			d.ctl.SetShowEvents(d.showEventsRow.Active())
		}
	})
	for i, row := range d.folderRows {
		v := jira.VirtualFolders[i]
		row.NotifyProperty("active", func() {
			if !d.applying {
				d.ctl.SetFolder(v, row.Active())
			}
		})
	}
	d.statuses.onToggle = func(choice jira.StatusChoice, on bool) {
		if !d.applying {
			d.ctl.SetStatus(choice, on)
		}
	}
	d.modeRow.NotifyProperty("selected", func() {
		if !d.applying {
			d.ctl.SetNotificationIndex(int(d.modeRow.Selected()))
		}
	})
	for _, e := range d.editors {
		kind := e.kind
		e.onAdd = func(text string) bool { return d.ctl.AddEntry(kind, text) }
		e.onRemove = func(i int) { d.ctl.RemoveEntry(kind, i) }
		e.onSuggestion = func(value string) { d.ctl.AddSuggestion(kind, value) }
		e.onTyped = func() { d.ctl.EntryTyped(kind) }
	}

	d.ctl.OnChange = d.refresh
	d.ctl.OnBusy = d.showBusy
	d.ctl.OnBanner = d.showBanner
	d.ctl.OnClose = d.dismiss
	d.ctl.OnDone = func(_ api.AccountID, cfg api.AccountConfig) {
		if d.OnSaved != nil {
			d.OnSaved(cfg)
		}
		d.dismiss()
	}
	d.ctl.OnReplaceToken = func(a api.Account) {
		if d.OnReplaceToken != nil {
			d.OnReplaceToken(a)
		}
	}
	// Escape, or the dialog going away with its parent: late replies are
	// dropped from now on.
	d.ConnectClosed(d.ctl.Close)
}

// dismiss closes the dialog; late replies are dropped from now on.
func (d *Dialog) dismiss() {
	d.ctl.Close()
	d.Close()
}

// onSave is Save: the name as typed, what is still in the field of a
// list (it is meant to be in the list), then account.update. An entry
// that cannot be added keeps the dialog open with its field focused and
// the reason under it. A list that does not matter in the mode chosen
// (the senders while notification e-mails are left alone) is not looked
// at: its field is insensitive and could not take the focus.
func (d *Dialog) onSave() {
	d.ctl.SetName(d.nameRow.Text())
	for _, e := range d.editors {
		if !e.Sensitive() || !e.pending() {
			continue
		}
		if !e.commit() {
			e.field.GrabFocus()
			return
		}
	}
	d.ctl.Save()
}

// refresh shows what the controller holds now.
func (d *Dialog) refresh() {
	c := d.ctl
	was := d.applying
	d.applying = true
	defer func() { d.applying = was }()
	editable := !c.Saving()

	site := c.Site()
	d.addressRow.SetSubtitle(site.Address)
	d.deploymentLabel.SetText(site.Deployment)
	d.userLabel.SetText(site.User)
	d.userLabel.SetTooltipText(site.User)
	d.userRow.SetSubtitle(site.UserDetail)
	d.tokenRow.SetTitle(site.TokenLabel)
	d.siteGroup.SetSensitive(editable)

	rows := c.SpaceRows()
	if !slices.Equal(rows, d.spaceRows) {
		d.rebuildSpaces(rows)
	}
	selected := c.SelectedSpaces()
	for i, r := range d.spaceRows {
		d.spaceChecks[i].SetActive(selected[r.ID])
	}
	d.spacesGroup.SetSensitive(editable)
	problem := ""
	if len(rows) > 0 {
		problem = c.SpacesProblem()
	}
	d.spacesProblem.SetText(problem)
	d.spacesProblem.SetVisible(problem != "")

	d.offlineRow.SetSelected(uint(c.OfflineIndex()))
	d.onlyMineRow.SetActive(c.Form().OnlyMine)
	d.showEventsRow.SetActive(c.Form().ShowEvents)
	d.syncGroup.SetSensitive(editable)

	for i, v := range jira.VirtualFolders {
		d.folderRows[i].SetActive(c.FolderShown(v))
	}
	d.statuses.apply(c.StatusGroups(), c.StatusesProblem())
	d.foldersGroup.SetSensitive(editable)

	d.modeRow.SetSelected(uint(c.NotificationIndex()))
	d.modeRow.SetSubtitle(c.NotificationHint())
	for _, e := range d.editors {
		e.apply(c.Entries(e.kind), c.Suggestions(e.kind), c.Problem(e.kind))
		if e.kind == jira.ListSenders {
			e.SetSensitive(c.SendersEditable())
		}
	}
	d.notificationGroup.SetSensitive(editable)
	d.botsGroup.SetSensitive(editable)

	d.save.SetSensitive(c.CanSave())
}

// rebuildSpaces replaces the rows of the spaces: one check box per space,
// titled "KEY – Name" (jira.SpaceTitle, cleaned), plain and on one line,
// whole in the tooltip.
func (d *Dialog) rebuildSpaces(rows []jira.SpaceRow) {
	d.spacesList.RemoveAll()
	d.spaceRows = slices.Clone(rows)
	d.spaceChecks = d.spaceChecks[:0]
	for _, r := range rows {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(r.Title)
		row.SetTitleLines(1)
		row.SetTooltipText(r.Title)
		check := gtk.NewCheckButton()
		check.SetVAlign(gtk.AlignCenter)
		row.AddPrefix(check)
		row.SetActivatableWidget(check)
		id := r.ID
		check.ConnectToggled(func() {
			if !d.applying {
				d.ctl.SetSpace(id, check.Active())
			}
		})
		d.spacesList.Append(row)
		d.spaceChecks = append(d.spaceChecks, check)
	}
}

// showBusy shows the progress of a call ("" when none runs) as the
// subtitle of the title, with a spinner; the page follows (Save waits).
func (d *Dialog) showBusy(text string) {
	d.windowTitle.SetSubtitle(text)
	d.spinner.SetVisible(text != "")
	d.refresh()
}

// showBanner shows the page's banner; "" hides it. The text stays while
// the banner hides, so that it does not go blank as it slides away.
func (d *Dialog) showBanner(text string) {
	if text != "" {
		d.banner.SetTitle(text)
	}
	d.banner.SetRevealed(text != "")
}
