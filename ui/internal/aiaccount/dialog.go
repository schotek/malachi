// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package aiaccount

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Dialog adds or edits an AI provider account (kind ai): one scrolling
// page (data/ui/ai_account.blp) under a header bar with Cancel and Add
// Account / Save, the banner of a failed call under it, the warning of a
// plain-http server under that. Its sections are the account (the kind,
// the name, the server address of an OpenAI-compatible one), the key
// (the field, or "Key stored" with Replace Key… for an existing
// account), the pinned certificate and Test, the models (typed, or picked
// from what Test listed), and the consent. The texts, the rules and the
// calls live in the Controller; this shows what it holds (refresh, on
// every change) and feeds back what the user did. Everything the
// provider answers (the model ids) is plain text.
//
// The pattern is ui/internal/jiraaccount's Dialog.
type Dialog struct {
	*adw.Dialog

	// OnSaved runs on the main loop after account.add or account.update
	// stored the account, before the dialog closes; id is the account's,
	// cfg what was stored. A dialog that had nothing to save closes
	// without it. May be nil.
	OnSaved func(id api.AccountID, cfg api.AccountConfig)

	ctl     *Controller
	log     *slog.Logger
	started bool
	// applying is the widgets being set from the controller: their
	// signals are not the user's.
	applying bool
	// shownModels are the models the popovers list.
	shownModels []api.AIModel

	windowTitle  *adw.WindowTitle
	cancel, save *gtk.Button
	spinner      *adw.Spinner
	banner       *adw.Banner
	httpBanner   *adw.Banner

	accountGroup *adw.PreferencesGroup
	providerRow  *adw.ActionRow
	nameRow      *adw.EntryRow
	addressRow   *adw.EntryRow
	addressHint  *gtk.Label

	keyGroup      *adw.PreferencesGroup
	keyStoredRow  *adw.ActionRow
	keyButton     *gtk.Button
	keyRow        *adw.PasswordEntryRow
	keepKeyButton *gtk.Button
	pinRow        *adw.ActionRow
	pinForget     *gtk.Button
	testResult    *gtk.Label
	trustButton   *gtk.Button
	testButton    *gtk.Button

	modelGroup     *adw.PreferencesGroup
	modelRow       *adw.EntryRow
	modelButton    *gtk.MenuButton
	boardModelRow  *adw.EntryRow
	boardModelMenu *gtk.MenuButton
	modelList      *gtk.ListBox
	boardModelList *gtk.ListBox
	consentGroup   *adw.PreferencesGroup
	consentRow     *adw.SwitchRow
	flaggedRow     gtk.Widgetter
}

// New builds the dialog that adds an account of provider p when editing
// is nil, else edits that account (its provider wins). Present it with
// Present; an existing account's models are asked for then. c may be nil
// for a dialog that never calls (a test).
func New(c Caller, log *slog.Logger, p api.AIProvider, editing *api.Account) *Dialog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("ai_account.ui")
	group := func(id string) *adw.PreferencesGroup { return b.GetObject(id).Cast().(*adw.PreferencesGroup) }
	action := func(id string) *adw.ActionRow { return b.GetObject(id).Cast().(*adw.ActionRow) }
	entry := func(id string) *adw.EntryRow { return b.GetObject(id).Cast().(*adw.EntryRow) }
	label := func(id string) *gtk.Label { return b.GetObject(id).Cast().(*gtk.Label) }
	button := func(id string) *gtk.Button { return b.GetObject(id).Cast().(*gtk.Button) }
	menu := func(id string) *gtk.MenuButton { return b.GetObject(id).Cast().(*gtk.MenuButton) }

	d := &Dialog{
		Dialog:         b.GetObject("ai_account_dialog").Cast().(*adw.Dialog),
		log:            log.With("component", "aiaccount"),
		windowTitle:    b.GetObject("window_title").Cast().(*adw.WindowTitle),
		cancel:         button("cancel_button"),
		save:           button("save_button"),
		spinner:        b.GetObject("progress_spinner").Cast().(*adw.Spinner),
		banner:         b.GetObject("banner").Cast().(*adw.Banner),
		httpBanner:     b.GetObject("http_banner").Cast().(*adw.Banner),
		accountGroup:   group("account_group"),
		providerRow:    action("provider_row"),
		nameRow:        entry("name_row"),
		addressRow:     entry("address_row"),
		addressHint:    label("address_hint"),
		keyGroup:       group("key_group"),
		keyStoredRow:   action("key_stored_row"),
		keyButton:      button("key_button"),
		keyRow:         b.GetObject("key_row").Cast().(*adw.PasswordEntryRow),
		keepKeyButton:  button("keep_key_button"),
		pinRow:         action("pin_row"),
		pinForget:      button("pin_forget_button"),
		testResult:     label("test_result_label"),
		trustButton:    button("trust_button"),
		testButton:     button("test_button"),
		modelGroup:     group("model_group"),
		modelRow:       entry("model_row"),
		modelButton:    menu("model_button"),
		boardModelRow:  entry("board_model_row"),
		boardModelMenu: menu("board_model_button"),
		consentGroup:   group("consent_group"),
		consentRow:     b.GetObject("consent_row").Cast().(*adw.SwitchRow),
	}
	d.ctl = NewController(c, p, editing, Env{
		Tr:        i18n.Tr,
		ErrorText: widget.RPCErrorText,
		// Replies arrive on the RPC client's goroutines.
		Post: func(f func()) { glib.IdleAdd(f) },
		Log:  d.log,
	})
	d.build()
	d.wire()
	d.refresh()
	return d
}

// Present shows the dialog over parent and, the first time, delivers the
// controller's initial state (an existing account's models are asked
// for).
func (d *Dialog) Present(parent gtk.Widgetter) {
	d.Dialog.Present(parent)
	if !d.started {
		d.started = true
		d.ctl.Start()
	}
}

// ReplaceKey opens the key field of an existing account (a sign-in route
// that says the provider refused the key).
func (d *Dialog) ReplaceKey() {
	d.ctl.ReplaceKey()
	d.keyRow.GrabFocus()
}

// Controller is the dialog's controller (the tests read it).
func (d *Dialog) Controller() *Controller { return d.ctl }

// build sets the fixed texts; nothing is connected yet (wire), so setting
// the widgets reports nothing.
func (d *Dialog) build() {
	c := d.ctl
	t := c.Texts
	d.SetTitle(c.Title())
	d.windowTitle.SetTitle(c.Title())
	d.save.SetLabel(c.SaveLabel())
	d.save.SetUseUnderline(true)

	d.providerRow.SetTitle(t.ProviderLabel)
	d.providerRow.SetSubtitle(c.ProviderName())
	d.nameRow.SetTitle(t.NameLabel)
	// Once: from here on the fields are where the form changes.
	d.nameRow.SetText(c.Form().Name)
	d.addressRow.SetTitle(t.ServerLabel)
	d.addressRow.SetText(c.Form().BaseURL)
	d.addressHint.SetText(t.ServerHint)
	d.addressRow.SetVisible(c.UsesServer())
	d.addressHint.SetVisible(c.UsesServer())

	d.keyStoredRow.SetTitle(t.KeyStored)
	d.keyButton.SetLabel(t.ReplaceKey)
	d.keepKeyButton.SetLabel(t.KeepKey)
	d.pinRow.SetTitle(t.PinLabel)
	d.pinForget.SetLabel(t.PinForget)
	d.testButton.SetLabel(t.Test)
	// TRANSLATORS: button of the AI provider dialog after a test refused an https server's certificate the user may trust.
	d.trustButton.SetLabel(i18n.T("Trust Certificate…"))

	d.modelRow.SetTitle(t.ModelLabel)
	d.modelRow.SetText(c.Form().Model)
	d.modelGroup.SetDescription(t.ModelHint)
	d.boardModelRow.SetTitle(t.BoardModelLabel)
	d.boardModelRow.SetText(c.Form().BoardModel)
	d.boardModelRow.SetTooltipText(t.BoardModelHint)
	d.modelList = d.newModelPopover(d.modelButton, d.modelRow)
	d.boardModelList = d.newModelPopover(d.boardModelMenu, d.boardModelRow)
	d.fillModels(nil)

	d.consentGroup.SetTitle(c.Consent.Heading)
	d.consentRow.SetTitle(c.Consent.Checkbox)
}

// newModelPopover gives button a popover with a list of the provider's
// models (fillModels); a row picked goes into target.
func (d *Dialog) newModelPopover(button *gtk.MenuButton, target *adw.EntryRow) *gtk.ListBox {
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("navigation-sidebar")
	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetPropagateNaturalHeight(true)
	scroller.SetMaxContentHeight(320)
	scroller.SetChild(list)
	popover := gtk.NewPopover()
	popover.SetChild(scroller)
	button.SetPopover(popover)
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		i := row.Index()
		if i < 0 || i >= len(d.shownModels) {
			return
		}
		popover.Popdown()
		target.SetText(d.shownModels[i].ID)
		target.GrabFocus()
	})
	return list
}

// fillModels replaces the rows of both popovers with models: the id,
// and the display name under it when it differs (both cleaned as any
// text of the provider's).
func (d *Dialog) fillModels(models []api.AIModel) {
	d.shownModels = slices.Clone(models)
	for _, list := range []*gtk.ListBox{d.modelList, d.boardModelList} {
		list.RemoveAll()
		for _, m := range models {
			row := adw.NewActionRow()
			row.SetUseMarkup(false)
			row.SetTitle(jira.Clean(m.ID))
			if name := jira.Clean(m.DisplayName); name != "" && name != m.ID {
				row.SetSubtitle(name)
			}
			row.SetActivatable(true)
			list.Append(row)
		}
	}
	has := len(models) > 0
	d.modelButton.SetSensitive(has)
	d.boardModelMenu.SetSensitive(has)
}

// wire connects the widgets to the controller and the controller to the
// widgets.
func (d *Dialog) wire() {
	c := d.ctl
	d.cancel.ConnectClicked(d.dismiss)
	d.save.ConnectClicked(d.onSave)
	d.testButton.ConnectClicked(c.RunTest)
	d.keyButton.ConnectClicked(func() {
		c.ReplaceKey()
		d.keyRow.GrabFocus()
	})
	d.keepKeyButton.ConnectClicked(c.KeepKey)
	d.pinForget.ConnectClicked(c.ForgetPin)
	d.trustButton.ConnectClicked(d.onTrust)

	typed := func(row *adw.EntryRow, set func(string)) {
		row.ConnectChanged(func() {
			if !d.applying {
				set(row.Text())
			}
		})
	}
	typed(d.nameRow, c.SetName)
	typed(d.addressRow, c.SetBaseURL)
	typed(d.modelRow, c.SetModel)
	typed(d.boardModelRow, c.SetBoardModel)
	d.keyRow.ConnectChanged(func() {
		if !d.applying {
			c.SetKey(d.keyRow.Text())
		}
	})
	d.consentRow.NotifyProperty("active", func() {
		if !d.applying {
			c.SetConsent(d.consentRow.Active())
		}
	})

	c.OnChange = d.refresh
	c.OnBusy = d.showBusy
	c.OnBanner = d.showBanner
	c.OnProblem = d.flag
	c.OnClose = d.dismiss
	c.OnDone = func(id api.AccountID, cfg api.AccountConfig) {
		if d.OnSaved != nil {
			d.OnSaved(id, cfg)
		}
		d.dismiss()
	}
	// Escape, or the dialog going away with its parent: late replies are
	// dropped from now on.
	d.ConnectClosed(c.Close)
}

// dismiss closes the dialog; late replies are dropped from now on.
func (d *Dialog) dismiss() {
	d.ctl.Close()
	d.Close()
}

// onSave is Save: the fields as typed, then account.add or
// account.update.
func (d *Dialog) onSave() {
	c := d.ctl
	c.SetName(d.nameRow.Text())
	if c.UsesServer() {
		c.SetBaseURL(d.addressRow.Text())
	}
	if !c.KeyStored() {
		c.SetKey(d.keyRow.Text())
	}
	c.SetModel(d.modelRow.Text())
	c.SetBoardModel(d.boardModelRow.Text())
	c.Save()
}

// onTrust is Trust Certificate…: the confirmation with the certificate's
// details (the account assistant's words), then the pin and the test
// again.
func (d *Dialog) onTrust() {
	p, ok := d.ctl.TrustOffer()
	if !ok || p.Cert == nil {
		return
	}
	host := d.ctl.Host()
	previous := ""
	changed := p.Category() == certtrust.Changed
	if changed && p.Expected != "" {
		previous = certtrust.FormatFingerprint(p.Expected)
	}
	var heading, body string
	if changed {
		// TRANSLATORS: dialog heading, the pinned certificate of a server
		// has changed
		heading = i18n.T("Trust the New Certificate?")
		// TRANSLATORS: %s is one server ("host:port") or two joined by
		// "%s and %s".
		body = fmt.Sprintf(i18n.T("The certificate of %s has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed."), host)
	} else {
		// TRANSLATORS: dialog heading
		heading = i18n.T("Trust This Certificate?")
		// TRANSLATORS: %s is one server ("host:port") or two joined by
		// "%s and %s".
		body = fmt.Sprintf(i18n.T("Only trust a certificate you expect, for example the one of a mail bridge on your own computer. Malachi Mail will then accept exactly this certificate for %s and no other, even if it is self-signed, issued for another name or expired."), host)
	}
	// TRANSLATORS: destructive confirm button of "Trust This Certificate?"
	confirm := i18n.T("_Trust")
	widget.ConfirmDestructiveExtra(d, heading, body, confirm, accountwizard.CertificateDetails(*p.Cert, previous), func() {
		if d.ctl.Closed() {
			return
		}
		d.ctl.Trust()
		d.log.Info("certificate trusted")
		d.ctl.RunTest()
	})
}

// refresh shows what the controller holds now.
func (d *Dialog) refresh() {
	c := d.ctl
	was := d.applying
	d.applying = true
	defer func() { d.applying = was }()
	t := c.Texts
	editable := !c.Saving()
	form := c.Form()

	d.accountGroup.SetSensitive(editable)
	if d.nameRow.Text() != form.Name {
		d.nameRow.SetText(form.Name)
	}
	if c.UsesServer() && d.addressRow.Text() != form.BaseURL {
		d.addressRow.SetText(form.BaseURL)
	}
	if w := c.HTTPWarningText(); w != "" {
		d.httpBanner.SetTitle(w)
		d.httpBanner.SetRevealed(true)
	} else {
		d.httpBanner.SetRevealed(false)
	}

	stored := c.KeyStored()
	d.keyStoredRow.SetVisible(stored)
	d.keyRow.SetVisible(!stored)
	d.keyRow.SetTitle(c.KeyLabel())
	if d.keyRow.Text() != form.Key {
		d.keyRow.SetText(form.Key)
	}
	d.keepKeyButton.SetVisible(c.IsEditing() && !stored)
	d.pinRow.SetVisible(c.Pinned())
	d.pinRow.SetSubtitle(c.PinText())
	d.keyGroup.SetSensitive(editable)

	text := ""
	if o := c.Test(); o != nil {
		text = o.Text
	} else if c.Busy() {
		text = c.Progress()
	}
	d.testResult.SetText(text)
	_, trust := c.TrustOffer()
	d.trustButton.SetVisible(trust)
	d.testButton.SetSensitive(c.CanTest())
	d.testButton.SetTooltipText(t.Testing)

	if !slices.Equal(c.Models(), d.shownModels) {
		d.fillModels(c.Models())
	}
	if d.modelRow.Text() != form.Model {
		d.modelRow.SetText(form.Model)
	}
	if d.boardModelRow.Text() != form.BoardModel {
		d.boardModelRow.SetText(form.BoardModel)
	}
	d.modelGroup.SetSensitive(editable)

	d.consentGroup.SetVisible(c.NeedsConsent())
	d.consentRow.SetSubtitle(c.ConsentBody())
	d.consentRow.SetActive(form.Consent)
	d.consentGroup.SetSensitive(editable)

	d.save.SetSensitive(c.CanSave())
}

// flag marks the field the controller flagged and gives it the focus;
// FieldNone clears the mark.
func (d *Dialog) flag(f Field) {
	if d.flaggedRow != nil {
		gtk.BaseWidget(d.flaggedRow).RemoveCSSClass("error")
		d.flaggedRow = nil
	}
	var row gtk.Widgetter
	switch f {
	case FieldName:
		row = d.nameRow
	case FieldServer:
		row = d.addressRow
	case FieldKey:
		row = d.keyRow
	case FieldModel:
		row = d.modelRow
	case FieldBoardModel:
		row = d.boardModelRow
	case FieldConsent:
		row = d.consentRow
	}
	if row == nil {
		return
	}
	d.flaggedRow = row
	gtk.BaseWidget(row).AddCSSClass("error")
	gtk.BaseWidget(row).GrabFocus()
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
