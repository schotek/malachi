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
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/calendar"
	chatgptpkg "github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/mcpsetup"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Integrations: the accounts that are not mail accounts
// (Connected Services; their rows are the Accounts page's, built in
// accounts_page.go from integrations.RowFor and filled by the same
// account.list), the two local AI integrations after them (Claude Code /
// Claude Desktop and ChatGPT, state of this client rather than accounts
// of the daemon: integrations.LocalRowFor over the Assistant's state,
// each shown only while it is connected and then gone from the
// catalogue, integrations.Offered), and the catalogue of what can be
// added in two sections, the services and the AI
// (integrations.CatalogSection). A Jira row edits its settings, a
// CalDAV row switches to the Calendar page, which keeps the CalDAV
// account's server, password, certificate and calendars, an AI provider
// account's row its dialog (ai_editors.go), a local row its own dialog
// (claude_integration.go, chatgpt_integration.go).

// bindIntegrations sets the page's texts, builds the catalogue rows and
// the local rows, and follows the Assistant's state for the latter and
// the tokens used for the AI provider accounts' rows.
func (d *PreferencesDialog) bindIntegrations(c *client.Client) (unbind func()) {
	tx := integrations.Texts(i18n.Tr)
	d.integrationsPage.SetTitle(tx.Title)
	d.integrationsPage.SetDescription(glib.MarkupEscapeText(tx.Description))
	d.integrationList.group.SetTitle(glib.MarkupEscapeText(tx.ConnectedTitle))
	d.integrationList.empty.SetTitle(tx.EmptyTitle)
	d.integrationList.empty.SetSubtitle(tx.EmptySubtitle)
	d.integrationsServices.SetTitle(glib.MarkupEscapeText(tx.AvailableTitle))
	for _, s := range integrations.CatalogSection(i18n.Tr, integrations.SectionServices) {
		d.integrationsServices.Add(d.newServiceRow(c, s, tx.Add))
	}
	if d.integrationsAI != nil {
		d.integrationsAI.SetTitle(glib.MarkupEscapeText(tx.AITitle))
		for _, s := range integrations.CatalogSection(i18n.Tr, integrations.SectionAI) {
			d.integrationsAI.Add(d.newServiceRow(c, s, tx.Add))
		}
	}
	var cleanup []func()
	if d.integrationsLocal != nil {
		cleanup = append(cleanup, d.bindLocalRows(tx))
	}
	cleanup = append(cleanup, listenAssistantUsage(func(id api.AccountID) {
		if !d.closed {
			d.refreshUsage(c, id)
		}
	}))
	return func() {
		for _, f := range cleanup {
			f()
		}
	}
}

// newServiceRow is a catalogue row: the service's icon, name and
// description, and its Add… button, which opens the service's assistant
// (or, for a local integration, its settings).
func (d *PreferencesDialog) newServiceRow(c *client.Client, s integrations.Service, add string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(s.Name)
	row.SetSubtitle(s.Description)
	row.AddPrefix(gtk.NewImageFromIconName(s.Icon))
	button := gtk.NewButtonWithLabel(add)
	button.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(button)
	row.SetActivatableWidget(button)
	button.ConnectClicked(func() { d.addService(c, s) })
	if s.Local != integrations.LocalNone {
		if d.localOffers == nil {
			d.localOffers = map[integrations.LocalKind]*adw.ActionRow{}
		}
		d.localOffers[s.Local] = row
	}
	return row
}

// addService opens the assistant of a catalogue service, or the settings
// of a local integration.
func (d *PreferencesDialog) addService(c *client.Client, s integrations.Service) {
	switch {
	case s.Local == integrations.LocalClaude:
		d.editClaudeIntegration()
	case s.Local == integrations.LocalChatGPT:
		d.editChatGPTIntegration()
	case s.Kind == api.AccountJira:
		d.addJiraAccount(c)
	case s.Kind == api.AccountCalDAV:
		d.addCalDAVAccount(c)
	case s.Kind == api.AccountEway:
		d.addEwayAccount(c)
	case s.Kind == api.AccountAI:
		d.addAIAccount(c, s.Provider)
	}
}

// localRow is the row of a local integration: the icon, the title and the
// subtitle of integrations.LocalRow, Settings and Remove with its label
// (unregister, disconnect). The row is shown only while the integration
// is connected; offer is its entry of the catalogue, shown the rest of
// the time.
type localRow struct {
	*adw.ActionRow
	spec     integrations.LocalRow
	settings *gtk.Button
	remove   *gtk.Button
	offer    *adw.ActionRow
}

// bindLocalRows builds the two local rows, hidden, and keeps them with
// the Assistant's state: the bridge's registration, Claude Code (its
// path, version and sign-in, asked in the background), the ChatGPT
// connection and Codex. The returned function stops following.
func (d *PreferencesDialog) bindLocalRows(tx integrations.PageTexts) (unbind func()) {
	a := d.assist
	for _, kind := range integrations.LocalKinds {
		spec, _ := integrations.LocalRowFor(kind, integrations.ClaudeState{}, integrations.ChatGPTState{}, i18n.Tr)
		row := &localRow{ActionRow: adw.NewActionRow(), spec: spec, offer: d.localOffers[kind]}
		row.SetUseMarkup(false)
		row.AddCSSClass("account-row")
		row.AddPrefix(gtk.NewImageFromIconName(spec.Icon))
		row.settings = gtk.NewButtonFromIconName("emblem-system-symbolic")
		row.settings.SetVAlign(gtk.AlignCenter)
		row.settings.AddCSSClass("flat")
		row.settings.SetTooltipText(tx.Settings)
		row.AddSuffix(row.settings)
		row.SetActivatableWidget(row.settings)
		row.remove = gtk.NewButtonWithLabel("")
		row.remove.SetVAlign(gtk.AlignCenter)
		row.AddSuffix(row.remove)
		row.settings.ConnectClicked(func() {
			switch kind {
			case integrations.LocalClaude:
				d.editClaudeIntegration()
			case integrations.LocalChatGPT:
				d.editChatGPTIntegration()
			}
		})
		row.remove.ConnectClicked(func() { d.removeLocal(kind, tx) })
		row.apply(spec, false)
		d.integrationsLocal.Add(row)
		d.localRows = append(d.localRows, row)
	}
	d.integrationsLocal.SetVisible(false)
	if a == nil {
		return func() {}
	}
	gen := 0
	refresh := func() {
		if d.closed {
			return
		}
		gen++
		my := gen
		claude, chatgpt := d.localStates()
		d.applyLocalRows(claude, chatgpt)
		if !claude.Present {
			return
		}
		// The version and the sign-in come from Claude Code itself.
		a.locator.Version(func(version string) {
			a.locator.SignedIn(func(in assistantpanel.SignIn) {
				if d.closed || my != gen {
					return
				}
				claude.Version, claude.SignInKnown, claude.SignedIn = version, in.Known, in.SignedIn
				d.applyLocalRows(claude, chatgpt)
			})
		})
	}
	remove := a.OnChange(refresh)
	unwatch := a.locator.OnSignInChange(refresh)
	refresh()
	return func() {
		remove()
		unwatch()
	}
}

// localStates is what the Assistant knows of the Claude apps and ChatGPT
// now, without what Claude Code itself is asked for (the version, the
// sign-in).
func (d *PreferencesDialog) localStates() (integrations.ClaudeState, integrations.ChatGPTState) {
	a := d.assist
	path := a.locator.Locate()
	claude := integrations.ClaudeState{Registered: a.registered(), Present: path != "", Path: path}
	var chatgpt integrations.ChatGPTState
	if a.chatGPT != nil {
		conn := a.chatGPT.Connection()
		chatgpt.Connected = conn.Status == chatgptpkg.Connected
		chatgpt.Account = conn.Email
	}
	chatgpt.CodexFound = chatgptpkg.Locate(a.settings.AssistantCodexPath()) != ""
	return claude, chatgpt
}

// applyLocalRows shows the rows of the states: a connected integration's
// row, its catalogue entry otherwise; the rows' group only with a row in
// it, and the Connected Services placeholder only without any row.
func (d *PreferencesDialog) applyLocalRows(claude integrations.ClaudeState, chatgpt integrations.ChatGPTState) {
	group := false
	for i, kind := range integrations.LocalKinds {
		if i >= len(d.localRows) {
			break
		}
		spec, shown := integrations.LocalRowFor(kind, claude, chatgpt, i18n.Tr)
		d.localRows[i].apply(spec, shown)
		group = group || shown
	}
	d.integrationsLocal.SetVisible(group)
	d.refreshIntegrationsEmpty()
}

// localShown reports whether a local integration's row is shown.
func (d *PreferencesDialog) localShown() bool {
	for _, r := range d.localRows {
		if r.Visible() {
			return true
		}
	}
	return false
}

// refreshIntegrationsEmpty shows the Connected Services placeholder only
// while neither an account nor a local integration has a row there.
func (d *PreferencesDialog) refreshIntegrationsEmpty() {
	if l := d.integrationList; l != nil {
		l.empty.SetVisible(len(l.rows) == 0 && !d.localShown())
	}
}

// apply shows spec on the row, and the row or its catalogue entry.
func (r *localRow) apply(spec integrations.LocalRow, shown bool) {
	r.spec = spec
	r.SetTitle(spec.Title)
	r.SetSubtitle(spec.Subtitle)
	r.remove.SetLabel(spec.RemoveLabel)
	r.remove.SetVisible(spec.CanRemove)
	r.SetVisible(shown)
	if r.offer != nil {
		r.offer.SetVisible(!shown)
	}
}

// removeLocal is a local row's Remove: after a confirmation, the bridge
// unregistered from the Claude apps (mcpsetup.Uninstall, the report
// handed to the Assistant as the Claude integration's switch hands it),
// or ChatGPT disconnected (its sign-in cancelled first).
func (d *PreferencesDialog) removeLocal(kind integrations.LocalKind, tx integrations.PageTexts) {
	a := d.assist
	if a == nil {
		return
	}
	var row *localRow
	for _, r := range d.localRows {
		if r.spec.Kind == kind {
			row = r
		}
	}
	if row == nil {
		return
	}
	switch kind {
	case integrations.LocalClaude:
		bridge := a.bridge
		if bridge == "" {
			return
		}
		widget.ConfirmDestructive(d, tx.UnregisterHeading, tx.UnregisterBody, row.spec.RemoveLabel, func() {
			row.remove.SetSensitive(false)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
				defer cancel()
				st, err := mcpsetup.Uninstall(ctx, bridge)
				glib.IdleAdd(func() {
					if d.closed {
						return
					}
					row.remove.SetSensitive(true)
					if err != nil {
						d.log.Warn("unregistering the MCP bridge", "err", err)
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("The MCP bridge could not be unregistered: %s"), err)))
						return
					}
					a.Apply(st)
				})
			}()
		})
	case integrations.LocalChatGPT:
		if a.chatGPT == nil {
			return
		}
		widget.ConfirmDestructive(d, tx.DisconnectHeading, tx.DisconnectBody, row.spec.RemoveLabel, func() {
			a.chatGPT.CancelSignIn()
			row.remove.SetSensitive(false)
			go func() {
				confirmed, err := a.chatGPT.Disconnect(context.Background())
				glib.IdleAdd(func() {
					if d.closed {
						return
					}
					row.remove.SetSensitive(true)
					if err != nil || !confirmed {
						d.AddToast(widget.PlainToast(assistant.ChatGPTText(tr).RevocationUnconfirmed))
					}
				})
			}()
		})
	}
}

// addEwayAccount opens the eWay-CRM assistant from the catalogue; both
// account pages reload once it added the account (the preferences dialog
// does not receive notify.accountsChanged). eWay-CRM accounts are added
// only here (owner's decision 2026-10-17, as on macOS).
func (d *PreferencesDialog) addEwayAccount(c *client.Client) {
	wz := accountwizard.NewEway(c, d.log)
	wz.OnDone = func(api.AccountID, api.AccountConfig) {
		if !d.closed {
			d.loadAccounts(c)
		}
	}
	wz.Present(d)
}

// editEwayAccount opens the eWay-CRM assistant in its edit mode on account
// a (the row's Edit): name, user name, a new password, following saved
// conversations; the pages reload once it saved.
func (d *PreferencesDialog) editEwayAccount(c *client.Client, a api.Account) {
	wz := accountwizard.NewEwayEdit(c, d.log, a)
	wz.OnDone = func(api.AccountID, api.AccountConfig) {
		if !d.closed {
			d.loadAccounts(c)
		}
	}
	wz.Present(d)
}

// addCalDAVAccount opens the Add CalDAV sheet over the dialog (as the
// Calendar page's Add CalDAV Calendar… does); both pages reload once it
// added the account. Without the main window there is no calendar source
// to add to.
func (d *PreferencesDialog) addCalDAVAccount(c *client.Client) {
	w := d.calendarWindow()
	if w == nil {
		d.AddToast(widget.PlainToast(calendar.PreviewOnly(i18n.Tr)))
		return
	}
	w.showAddCalDAV(d, func() {
		if d.closed {
			return
		}
		d.loadAccounts(c)
		d.refreshCalendarPage()
	})
}

// showCalendarPage is a CalDAV row's Edit: the Calendar page, where the
// account's section is. Hidden without a calendar source (no main
// window), so then it only says so.
func (d *PreferencesDialog) showCalendarPage() {
	if d.calendarPage == nil {
		d.AddToast(widget.PlainToast(calendar.PreviewOnly(i18n.Tr)))
		return
	}
	d.SetVisiblePageName("calendar")
}

// refreshCalendarPage asks the Calendar page's source again after an
// account was added or removed elsewhere in the dialog.
func (d *PreferencesDialog) refreshCalendarPage() {
	if p := d.calendarPage; p != nil && !p.closed {
		p.src.Refresh()
	}
}
