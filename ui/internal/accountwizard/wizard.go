// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// RPC budgets. account.test probes two endpoints for up to 20 s each in
// parallel; account.add may wait for a keyring unlock dialog.
const (
	discoverTimeout = 15 * time.Second
	testTimeout     = 45 * time.Second
	addTimeout      = 30 * time.Second
)

// Navigation page tags, as in account_wizard.blp.
const (
	tagIdentity = "identity"
	tagServers  = "servers"
	tagGOA      = "goa"
	tagTesting  = "testing"
)

// Wizard is the "Add Account" dialog.
type Wizard struct {
	*adw.Dialog

	client *client.Client
	log    *slog.Logger

	// OnDone runs on the main loop after account.add or account.update
	// succeeded, before the dialog closes. May be nil.
	OnDone func(id api.AccountID, cfg api.AccountConfig)

	// editing is the account being changed; nil when adding a new one.
	editing *api.Account

	nav    *adw.NavigationView
	toasts *adw.ToastOverlay

	identityPage   *adw.NavigationPage
	identityBanner *adw.Banner
	identityRows   *gtk.ListBox
	displayName    *adw.EntryRow
	email          *adw.EntryRow
	password       *adw.PasswordEntryRow
	next           *gtk.Button
	linkedGroup    *adw.PreferencesGroup
	linkedRows     *gtk.ListBox

	goaOpen, goaRecheck *gtk.Button

	serversPrefs *adw.PreferencesPage
	accountName  *adw.EntryRow
	imap, smtp   serverRows
	test         *gtk.Button

	testingStack       *gtk.Stack
	progress           *adw.StatusPage
	results            *adw.StatusPage
	imapRow, smtpRow   *adw.ActionRow
	imapIcon, smtpIcon *gtk.Image
	graphRow           *adw.ActionRow
	graphIcon          *gtk.Image
	edit, retry        *gtk.Button
	addAnyway, add     *gtk.Button

	// graphCfg is set on the Microsoft 365 path: the account has no servers
	// and no password, the sign-in lives in GNOME Online Accounts. nil is
	// the IMAP path.
	graphCfg *api.AccountConfig
	linked   []api.LinkedAccount

	closed      bool
	op          int  // bumped per RPC so stale callbacks bail out
	applying    bool // rows are being set programmatically
	lastOutcome Outcome
}

// serverRows are one endpoint's editors on the Servers page.
type serverRows struct {
	kind         Endpoint
	host         *adw.EntryRow
	port         *adw.SpinRow
	security     *adw.ComboRow
	user         *adw.EntryRow
	lastSecurity api.Security
}

// New builds the dialog. Present it with Present(parent).
func New(c *client.Client, log *slog.Logger) *Wizard {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("account_wizard.ui")
	entry := func(id string) *adw.EntryRow { return b.GetObject(id).Cast().(*adw.EntryRow) }
	button := func(id string) *gtk.Button { return b.GetObject(id).Cast().(*gtk.Button) }

	w := &Wizard{
		Dialog:         b.GetObject("account_wizard").Cast().(*adw.Dialog),
		client:         c,
		log:            log.With("component", "accountwizard"),
		nav:            b.GetObject("wizard_nav").Cast().(*adw.NavigationView),
		toasts:         b.GetObject("wizard_toasts").Cast().(*adw.ToastOverlay),
		identityPage:   b.GetObject("identity_page").Cast().(*adw.NavigationPage),
		identityBanner: b.GetObject("identity_banner").Cast().(*adw.Banner),
		identityRows:   b.GetObject("identity_rows").Cast().(*gtk.ListBox),
		displayName:    entry("display_name_row"),
		email:          entry("email_row"),
		password:       b.GetObject("password_row").Cast().(*adw.PasswordEntryRow),
		next:           button("identity_next_button"),
		linkedGroup:    b.GetObject("linked_group").Cast().(*adw.PreferencesGroup),
		linkedRows:     b.GetObject("linked_rows").Cast().(*gtk.ListBox),
		goaOpen:        button("goa_open_button"),
		goaRecheck:     button("goa_recheck_button"),
		serversPrefs:   b.GetObject("servers_prefs").Cast().(*adw.PreferencesPage),
		accountName:    entry("account_name_row"),
		imap: serverRows{
			kind:     EndpointIMAP,
			host:     entry("imap_host_row"),
			port:     b.GetObject("imap_port_row").Cast().(*adw.SpinRow),
			security: b.GetObject("imap_security_row").Cast().(*adw.ComboRow),
			user:     entry("imap_user_row"),
		},
		smtp: serverRows{
			kind:     EndpointSMTP,
			host:     entry("smtp_host_row"),
			port:     b.GetObject("smtp_port_row").Cast().(*adw.SpinRow),
			security: b.GetObject("smtp_security_row").Cast().(*adw.ComboRow),
			user:     entry("smtp_user_row"),
		},
		test:         button("servers_test_button"),
		testingStack: b.GetObject("testing_stack").Cast().(*gtk.Stack),
		progress:     b.GetObject("testing_progress").Cast().(*adw.StatusPage),
		results:      b.GetObject("testing_results").Cast().(*adw.StatusPage),
		imapRow:      b.GetObject("imap_result_row").Cast().(*adw.ActionRow),
		smtpRow:      b.GetObject("smtp_result_row").Cast().(*adw.ActionRow),
		imapIcon:     b.GetObject("imap_result_icon").Cast().(*gtk.Image),
		smtpIcon:     b.GetObject("smtp_result_icon").Cast().(*gtk.Image),
		graphRow:     b.GetObject("graph_result_row").Cast().(*adw.ActionRow),
		graphIcon:    b.GetObject("graph_result_icon").Cast().(*gtk.Image),
		edit:         button("test_edit_button"),
		retry:        button("test_retry_button"),
		addAnyway:    button("test_add_anyway_button"),
		add:          button("test_add_button"),
	}
	w.identityBanner.SetUseMarkup(false)
	w.wire()
	w.loadLinked(nil)
	return w
}

// NewEdit builds the dialog for changing an existing account: the pages
// are prefilled, discovery is skipped, an empty password keeps the stored
// one, and the final step is account.update. It opens on the Servers page;
// Back leads to the identity page.
func NewEdit(c *client.Client, log *slog.Logger, a api.Account) *Wizard {
	w := New(c, log)
	w.editing = &a
	w.SetTitle(i18n.T("Edit Account"))
	w.identityPage.SetTitle(i18n.T("Edit Account"))
	w.password.SetTitle(i18n.T("New Password (leave empty to keep)"))
	w.add.SetLabel(i18n.T("_Save"))
	w.addAnyway.SetLabel(i18n.T("Save _Anyway"))
	w.displayName.SetText(a.Config.DisplayName)
	w.email.SetText(a.Config.Email)
	if a.Config.Protocol() == api.AccountGraph {
		// The address and the sign-in belong to GNOME Online Accounts; only
		// the name can change here, and the test re-checks the sign-in.
		cfg := a.Config
		w.graphCfg = &cfg
		w.email.SetSensitive(false)
		w.password.SetVisible(false)
		w.next.SetLabel(i18n.T("_Test Connection"))
		w.nav.ReplaceWithTags([]string{tagIdentity})
		return w
	}
	w.applyConfig(a.Config)
	w.nav.ReplaceWithTags([]string{tagIdentity, tagServers})
	return w
}

func (w *Wizard) wire() {
	clearIdentity := func() {
		w.email.RemoveCSSClass("error")
		w.password.RemoveCSSClass("error")
		w.identityBanner.SetRevealed(false)
	}
	w.email.ConnectChanged(clearIdentity)
	w.password.ConnectChanged(clearIdentity)
	w.email.ConnectEntryActivated(func() { w.password.GrabFocus() })
	w.password.ConnectEntryActivated(w.onNext)
	w.next.ConnectClicked(w.onNext)

	for _, rows := range []*serverRows{&w.imap, &w.smtp} {
		rows := rows
		rows.lastSecurity = securityAt(rows.security.Selected())
		rows.security.NotifyProperty("selected", func() {
			if w.applying {
				return
			}
			to := securityAt(rows.security.Selected())
			rows.port.SetValue(float64(PortForSecurityChange(rows.kind, int(rows.port.Value()), rows.lastSecurity, to)))
			rows.lastSecurity = to
		})
		rows.host.ConnectChanged(func() { rows.host.RemoveCSSClass("error") })
		rows.user.ConnectChanged(func() { rows.user.RemoveCSSClass("error") })
	}
	w.test.ConnectClicked(w.onTest)
	w.retry.ConnectClicked(w.runTest)
	w.edit.ConnectClicked(func() { w.nav.PopToTag(tagServers) })
	w.add.ConnectClicked(w.onAdd)
	w.addAnyway.ConnectClicked(w.onAdd)
	w.goaOpen.ConnectClicked(w.onGOAOpen)
	w.goaRecheck.ConnectClicked(w.onGOARecheck)

	w.ConnectClosed(func() { w.closed = true })
}

// Present shows the dialog over parent.
func (w *Wizard) Present(parent gtk.Widgetter) { w.Dialog.Present(parent) }

func (w *Wizard) toast(text string) { w.toasts.AddToast(widget.PlainToast(text)) }

// setBusy blocks the editable pages during an RPC. The navigation and the
// close button keep working; callbacks check closed and op.
func (w *Wizard) setBusy(busy bool) {
	w.identityRows.SetSensitive(!busy)
	w.linkedGroup.SetSensitive(!busy)
	w.next.SetSensitive(!busy)
	w.serversPrefs.SetSensitive(!busy)
	w.test.SetSensitive(!busy)
}

func (w *Wizard) readIdentity() Identity {
	return Identity{DisplayName: w.displayName.Text(), Email: w.email.Text(), Password: w.password.Text()}
}

func (r *serverRows) read() ServerFields {
	return ServerFields{Host: r.host.Text(), Port: int(r.port.Value()), Security: securityAt(r.security.Selected()), Username: r.user.Text()}
}

func (r *serverRows) apply(sc *api.ServerConfig) {
	if sc == nil {
		return
	}
	r.host.SetText(sc.Host)
	r.port.SetValue(float64(sc.Port))
	r.security.SetSelected(indexOfSecurity(sc.Security))
	r.lastSecurity = sc.Security
	r.user.SetText(sc.Username)
}

// applyConfig fills the Servers page without triggering the port logic.
func (w *Wizard) applyConfig(cfg api.AccountConfig) {
	w.applying = true
	w.accountName.SetText(cfg.Name)
	w.imap.apply(cfg.IMAP)
	w.smtp.apply(cfg.SMTP)
	w.applying = false
}

func (w *Wizard) assembleConfig() api.AccountConfig {
	if w.graphCfg != nil {
		return GraphConfig(w.readIdentity(), w.graphCfg.Name, w.graphCfg.Graph.GOAAccountID)
	}
	return BuildConfig(w.readIdentity(), w.accountName.Text(), w.imap.read(), w.smtp.read())
}

// requirePassword flags the empty password row once discovery has shown
// the account needs one (a Microsoft 365 account does not).
func (w *Wizard) requirePassword() bool {
	if w.editing != nil || w.password.Text() != "" {
		return true
	}
	w.password.AddCSSClass("error")
	w.identityBanner.SetTitle(i18n.T("Enter the password for this account"))
	w.identityBanner.SetRevealed(true)
	w.password.GrabFocus()
	return false
}

// onNext validates the identity page and asks the daemon for server
// settings. A Microsoft 365 address goes to the connection test (signed in
// through GNOME Online Accounts) or to the sign-in hint; an IMAP hit goes
// to the connection test; a miss opens the Servers page with guessed
// defaults. The password is asked for only once the account turns out to
// need one.
func (w *Wizard) onNext() {
	id := w.readIdentity()
	if p := ValidateIdentity(id, false); p.Any() {
		w.email.AddCSSClass("error")
		w.email.GrabFocus()
		return
	}
	id.Email, _ = ValidateEmail(id.Email)
	if w.editing != nil {
		if w.graphCfg != nil {
			w.nav.ReplaceWithTags([]string{tagIdentity, tagTesting})
			w.runTest()
			return
		}
		// The servers are known; only the identity may have changed.
		w.nav.PushByTag(tagServers)
		return
	}
	if l, ok := LinkedMatch(w.linked, id.Email); ok && !l.Configured {
		w.useLinked(l)
		return
	}
	w.setBusy(true)
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
		defer cancel()
		var res api.AccountDiscoverResult
		err := w.client.Call(ctx, api.MethodAccountDiscover, api.AccountDiscoverParams{Email: id.Email}, &res)
		glib.IdleAdd(func() {
			if w.closed || op != w.op {
				return
			}
			w.setBusy(false)
			if err == nil && res.Config != nil && res.Config.Protocol() == api.AccountGraph {
				w.log.Info("account discovered", "source", res.Source)
				if res.Config.Graph != nil && res.Config.Graph.GOAAccountID != "" {
					w.startGraph(GraphConfig(id, res.Config.Name, res.Config.Graph.GOAAccountID))
					return
				}
				w.showGOAHint()
				return
			}
			if !w.requirePassword() {
				return
			}
			if err != nil || res.Config == nil {
				w.log.Debug("account.discover", "err", err, "source", res.Source)
				w.applyConfig(MergeIdentity(GuessConfig(id.Email), id))
				w.nav.PushByTag(tagServers)
				return
			}
			w.log.Info("account discovered", "source", res.Source)
			w.applyConfig(MergeIdentity(*res.Config, id))
			w.nav.ReplaceWithTags([]string{tagIdentity, tagServers, tagTesting})
			w.runTest()
		})
	}()
}

// onTest validates the Servers page and starts the test.
func (w *Wizard) onTest() {
	p := ValidateServers(w.imap.read(), w.smtp.read())
	if p.Any() {
		for _, bad := range []struct {
			flag bool
			row  *adw.EntryRow
		}{{p.IMAPHost, w.imap.host}, {p.IMAPUser, w.imap.user}, {p.SMTPHost, w.smtp.host}, {p.SMTPUser, w.smtp.user}} {
			if bad.flag {
				bad.row.AddCSSClass("error")
			}
		}
		return
	}
	if page := w.nav.VisiblePage(); page == nil || page.Tag() != tagTesting {
		w.nav.PushByTag(tagTesting)
	}
	w.runTest()
}

func (w *Wizard) showButtons(o Outcome) {
	w.edit.SetVisible(w.graphCfg == nil)
	w.retry.SetVisible(o == OutcomeFailed)
	w.addAnyway.SetVisible(o == OutcomeFailed)
	w.add.SetVisible(o == OutcomeOK)
}

func (w *Wizard) hideButtons() {
	for _, b := range []*gtk.Button{w.edit, w.retry, w.addAnyway, w.add} {
		b.SetVisible(false)
	}
}

// runTest calls account.test with the current settings.
func (w *Wizard) runTest() {
	w.progress.SetTitle(i18n.T("Testing Connection…"))
	w.testingStack.SetVisibleChildName("progress")
	w.hideButtons()
	params := api.AccountTestParams{Config: w.assembleConfig()}
	if w.graphCfg == nil {
		params.Credentials = credentialsFor(w.readIdentity())
	}
	if w.editing != nil {
		params.AccountID = w.editing.ID // an empty password means "use the stored one"
	}
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var res api.AccountTestResult
		err := w.client.Call(ctx, api.MethodAccountTest, params, &res)
		glib.IdleAdd(func() {
			if w.closed || op != w.op {
				return
			}
			w.showResults(res, err)
		})
	}()
}

func (w *Wizard) showResults(res api.AccountTestResult, err error) {
	graph := w.graphCfg != nil
	w.graphRow.SetVisible(graph)
	w.imapRow.SetVisible(!graph)
	w.smtpRow.SetVisible(!graph)
	var outcome Outcome
	if err != nil {
		text := widget.RPCErrorText(i18n.T("Testing the connection"), err)
		for _, row := range []*adw.ActionRow{w.imapRow, w.smtpRow, w.graphRow} {
			row.SetSubtitle(text)
		}
		for _, icon := range []*gtk.Image{w.imapIcon, w.smtpIcon, w.graphIcon} {
			icon.SetFromIconName("dialog-warning-symbolic")
		}
		outcome = OutcomeFailed
	} else if graph {
		icon, text := EndpointSummary(res.Graph)
		w.graphIcon.SetFromIconName(icon)
		w.graphRow.SetSubtitle(text)
		outcome = Classify(res)
	} else {
		icon, text := EndpointSummary(res.IMAP)
		w.imapIcon.SetFromIconName(icon)
		w.imapRow.SetSubtitle(text)
		icon, text = EndpointSummary(res.SMTP)
		w.smtpIcon.SetFromIconName(icon)
		w.smtpRow.SetSubtitle(text)
		outcome = Classify(res)
	}
	if graph && outcome == OutcomeAuthFailed {
		outcome = OutcomeFailed // there is no password to correct here
	}
	w.lastOutcome = outcome

	switch outcome {
	case OutcomeOK:
		w.results.SetIconName("emblem-ok-symbolic")
		if w.editing != nil {
			w.results.SetTitle(i18n.T("Ready to Save"))
		} else {
			w.results.SetTitle(i18n.T("Ready to Add"))
		}
	case OutcomeAuthFailed:
		w.results.SetIconName("dialog-warning-symbolic")
		w.results.SetTitle(i18n.T("Connection Failed"))
		w.nav.PopToTag(tagIdentity)
		w.password.AddCSSClass("error")
		w.identityBanner.SetTitle(i18n.T("The server rejected the user name or password"))
		w.identityBanner.SetRevealed(true)
		w.password.GrabFocus()
	default:
		w.results.SetIconName("dialog-warning-symbolic")
		w.results.SetTitle(i18n.T("Connection Failed"))
	}
	w.showButtons(outcome)
	w.testingStack.SetVisibleChildName("results")
}

// onAdd stores the account (account.add, or account.update when editing)
// and closes on success.
func (w *Wizard) onAdd() {
	cfg := w.assembleConfig()
	var creds api.Credentials
	if w.graphCfg == nil {
		creds = credentialsFor(w.readIdentity())
	}
	editing := w.editing
	if editing != nil {
		w.progress.SetTitle(i18n.T("Saving Account…"))
	} else {
		w.progress.SetTitle(i18n.T("Adding Account…"))
	}
	w.testingStack.SetVisibleChildName("progress")
	w.hideButtons()
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
		defer cancel()
		var id api.AccountID
		var err error
		if editing != nil {
			id = editing.ID
			err = w.client.Call(ctx, api.MethodAccountUpdate,
				api.AccountUpdateParams{AccountID: id, Config: cfg, Credentials: creds}, &api.AccountUpdateResult{})
		} else {
			var res api.AccountAddResult
			err = w.client.Call(ctx, api.MethodAccountAdd, api.AccountAddParams{Config: cfg, Credentials: creds}, &res)
			id = res.AccountID
		}
		glib.IdleAdd(func() {
			if w.closed || op != w.op {
				return
			}
			if err != nil {
				w.testingStack.SetVisibleChildName("results")
				w.showButtons(w.lastOutcome)
				w.toast(saveErrorText(err, editing != nil))
				return
			}
			w.log.Info("account saved", "id", id, "edit", editing != nil)
			if w.OnDone != nil {
				w.OnDone(id, cfg)
			}
			w.ForceClose()
		})
	}()
}

func saveErrorText(err error, editing bool) string {
	var e *api.Error
	if errors.As(err, &e) && e.Code == api.CodeConflict {
		return i18n.T("An account with this e-mail address already exists")
	}
	if editing {
		return widget.RPCErrorText(i18n.T("Saving the account"), err)
	}
	return widget.RPCErrorText(i18n.T("Adding the account"), err)
}
