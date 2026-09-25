// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// RPC budgets. account.test probes two endpoints for up to 20 s each in
// parallel; account.add may wait for a keyring unlock dialog. The daemon
// answers account.oauthWait with "pending" after at most 60 s, so one call
// gets a little more than that; the page calls again until the browser has
// come back.
const (
	discoverTimeout      = 15 * time.Second
	testTimeout          = 45 * time.Second
	addTimeout           = 30 * time.Second
	oauthStartTimeout    = 10 * time.Second
	oauthWaitCallTimeout = 75 * time.Second
)

// Navigation page tags, as in account_wizard.blp.
const (
	tagIdentity = "identity"
	tagServers  = "servers"
	tagGOA      = "goa"
	tagOAuth    = "oauth"
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

	goaOpen, goaRecheck, goaBrowser *gtk.Button

	oauthPage                                            *adw.NavigationPage
	oauthStack                                           *gtk.Stack
	oauthPrompt, oauthWaiting, oauthUnavailable          *adw.StatusPage
	oauthSignIn, oauthReopen, oauthCancel, oauthPassword *gtk.Button

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

	// linkedCfg is set for an account whose sign-in lives in GNOME Online
	// Accounts (Microsoft 365 through Graph, Google over IMAP with a
	// token) or in the backend's own browser sign-in: the daemon built
	// it, there is no password and the servers are not the user's to
	// edit. nil is the password path.
	linkedCfg *api.AccountConfig
	goaHint   *adw.StatusPage
	linked    []api.LinkedAccount

	// goaDiscovery is the account.discover answer the GNOME Online
	// Accounts hint page shows, with the alternatives it offers.
	goaDiscovery signin.Discovery
	// oauth is the browser sign-in of the oauth page (oauth.go).
	oauth oauthState
	// appPassword is the app-password account the user chose instead of
	// the browser sign-in; Next continues with it while the address stays
	// the same.
	appPassword *api.AccountConfig
	// signInOnly is the dialog of NewEditSignIn: the browser sign-in and
	// the test of an existing account, without the identity page.
	signInOnly bool
	// signInAgain says the last test of a browser sign-in account was
	// refused: the results page's first button signs in again.
	signInAgain bool

	// tested is the configuration the last account.test ran with; a
	// certificate trusted on the results page is pinned to its endpoint.
	tested api.AccountConfig

	closed      bool
	op          int  // bumped per RPC so stale callbacks bail out
	applying    bool // rows are being set programmatically
	lastOutcome Outcome
}

// serverRows are one endpoint's editors on the Servers page, its pinned
// certificate and the trust button of its result row.
type serverRows struct {
	kind         Endpoint
	host         *adw.EntryRow
	port         *adw.SpinRow
	security     *adw.ComboRow
	user         *adw.EntryRow
	pinRow       *adw.ActionRow
	pinForget    *gtk.Button
	trust        *gtk.Button
	lastSecurity api.Security

	// pinned is the endpoint a certificate was trusted for, with the pin
	// in CertificateSHA256 (loaded with the account, or trusted on the
	// results page); nil when none. read sends the pin only while the
	// rows still describe that server (certtrust.KeepPin).
	pinned *api.ServerConfig
	// offer is the refused certificate the results page offers to trust,
	// nil when none (trust.go).
	offer *trustOffer
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
		Dialog:           b.GetObject("account_wizard").Cast().(*adw.Dialog),
		client:           c,
		log:              log.With("component", "accountwizard"),
		nav:              b.GetObject("wizard_nav").Cast().(*adw.NavigationView),
		toasts:           b.GetObject("wizard_toasts").Cast().(*adw.ToastOverlay),
		identityPage:     b.GetObject("identity_page").Cast().(*adw.NavigationPage),
		identityBanner:   b.GetObject("identity_banner").Cast().(*adw.Banner),
		identityRows:     b.GetObject("identity_rows").Cast().(*gtk.ListBox),
		displayName:      entry("display_name_row"),
		email:            entry("email_row"),
		password:         b.GetObject("password_row").Cast().(*adw.PasswordEntryRow),
		next:             button("identity_next_button"),
		linkedGroup:      b.GetObject("linked_group").Cast().(*adw.PreferencesGroup),
		linkedRows:       b.GetObject("linked_rows").Cast().(*gtk.ListBox),
		goaOpen:          button("goa_open_button"),
		goaRecheck:       button("goa_recheck_button"),
		goaBrowser:       button("goa_browser_button"),
		goaHint:          b.GetObject("goa_hint").Cast().(*adw.StatusPage),
		oauthPage:        b.GetObject("oauth_page").Cast().(*adw.NavigationPage),
		oauthStack:       b.GetObject("oauth_stack").Cast().(*gtk.Stack),
		oauthPrompt:      b.GetObject("oauth_prompt").Cast().(*adw.StatusPage),
		oauthWaiting:     b.GetObject("oauth_waiting").Cast().(*adw.StatusPage),
		oauthUnavailable: b.GetObject("oauth_unavailable").Cast().(*adw.StatusPage),
		oauthSignIn:      button("oauth_signin_button"),
		oauthReopen:      button("oauth_reopen_button"),
		oauthCancel:      button("oauth_cancel_button"),
		oauthPassword:    button("oauth_password_button"),
		serversPrefs:     b.GetObject("servers_prefs").Cast().(*adw.PreferencesPage),
		accountName:      entry("account_name_row"),
		imap: serverRows{
			kind:      EndpointIMAP,
			host:      entry("imap_host_row"),
			port:      b.GetObject("imap_port_row").Cast().(*adw.SpinRow),
			security:  b.GetObject("imap_security_row").Cast().(*adw.ComboRow),
			user:      entry("imap_user_row"),
			pinRow:    b.GetObject("imap_pin_row").Cast().(*adw.ActionRow),
			pinForget: button("imap_pin_forget_button"),
			trust:     button("imap_trust_button"),
		},
		smtp: serverRows{
			kind:      EndpointSMTP,
			host:      entry("smtp_host_row"),
			port:      b.GetObject("smtp_port_row").Cast().(*adw.SpinRow),
			security:  b.GetObject("smtp_security_row").Cast().(*adw.ComboRow),
			user:      entry("smtp_user_row"),
			pinRow:    b.GetObject("smtp_pin_row").Cast().(*adw.ActionRow),
			pinForget: button("smtp_pin_forget_button"),
			trust:     button("smtp_trust_button"),
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
	// From Go rather than the Blueprint: GtkBuilder takes an inline object
	// in a paintable property for a file name ("Could not load image").
	w.progress.SetPaintable(adw.NewSpinnerPaintable(w.progress))
	w.oauthWaiting.SetPaintable(adw.NewSpinnerPaintable(w.oauthWaiting))
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
	if signin.KindOf(a.Config) != signin.Password {
		// The address and the sign-in belong to GNOME Online Accounts or to
		// the backend's own browser sign-in; only the name can change here,
		// and the test re-checks the stored sign-in (a refused browser
		// sign-in is renewed from the results page).
		cfg := a.Config
		w.linkedCfg = &cfg
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

// NewEditSignIn builds the dialog that signs an account of the backend's
// own sign-in in again (the Accounts page's "Sign In…"): it opens on the
// browser page, tests the account once the browser has come back and ends
// in account.update with the new sign-in. Any other account gets the plain
// NewEdit dialog.
func NewEditSignIn(c *client.Client, log *slog.Logger, a api.Account) *Wizard {
	w := NewEdit(c, log, a)
	if signin.KindOf(a.Config) != signin.OAuth {
		return w
	}
	w.signInOnly = true
	w.SetTitle(i18n.T("Sign In"))
	w.showOAuthPrompt(signin.Provider(a.Config), nil, nil)
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
			rows.showPin()
		})
		// The pin belongs to the server it was trusted for: another host
		// or port hides it (and read stops sending it).
		rows.host.ConnectChanged(func() {
			rows.host.RemoveCSSClass("error")
			rows.showPin()
		})
		rows.port.NotifyProperty("value", rows.showPin)
		rows.user.ConnectChanged(func() { rows.user.RemoveCSSClass("error") })
		rows.pinForget.ConnectClicked(rows.forgetPin)
		rows.trust.ConnectClicked(func() { w.onTrust(rows) })
	}
	w.test.ConnectClicked(w.onTest)
	w.retry.ConnectClicked(w.runTest)
	w.edit.ConnectClicked(w.onEdit)
	w.add.ConnectClicked(w.onAdd)
	w.addAnyway.ConnectClicked(w.onAdd)
	w.goaOpen.ConnectClicked(w.onGOAOpen)
	w.goaRecheck.ConnectClicked(w.onGOARecheck)
	w.goaBrowser.ConnectClicked(w.onGOABrowser)
	w.oauthSignIn.ConnectClicked(w.onOAuthSignIn)
	w.oauthReopen.ConnectClicked(w.onOAuthReopen)
	w.oauthCancel.ConnectClicked(w.onOAuthCancel)
	w.oauthPassword.ConnectClicked(w.onOAuthPassword)

	w.ConnectClosed(func() {
		w.closed = true
		// A sign-in still waiting in the daemon is no longer anyone's.
		w.cancelSession()
	})
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
	f := ServerFields{Host: r.host.Text(), Port: int(r.port.Value()), Security: securityAt(r.security.Selected()), Username: r.user.Text()}
	if r.pinned != nil {
		f.CertificateSHA256 = certtrust.KeepPin(*r.pinned, *serverConfig(f))
	}
	return f
}

// apply fills the rows from a configuration; its pin, if any, becomes the
// rows' pinned certificate and any earlier one is dropped.
func (r *serverRows) apply(sc *api.ServerConfig) {
	if sc == nil {
		return
	}
	r.host.SetText(sc.Host)
	r.port.SetValue(float64(sc.Port))
	r.security.SetSelected(indexOfSecurity(sc.Security))
	r.lastSecurity = sc.Security
	r.user.SetText(sc.Username)
	r.pinned = nil
	if sc.CertificateSHA256 != "" {
		c := *sc
		r.pinned = &c
	}
	r.showPin()
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
	if w.linkedCfg != nil {
		return withIdentity(*w.linkedCfg, w.readIdentity())
	}
	return BuildConfig(w.readIdentity(), w.accountName.Text(), w.imap.read(), w.smtp.read())
}

// requirePassword flags the empty password row once discovery has shown
// the account needs one (a Microsoft 365 account does not); banner says
// which password.
func (w *Wizard) requirePassword(banner string) bool {
	if w.editing != nil || w.password.Text() != "" {
		return true
	}
	w.password.AddCSSClass("error")
	w.identityBanner.SetTitle(banner)
	w.identityBanner.SetRevealed(true)
	w.password.GrabFocus()
	return false
}

// credentials are what account.test, account.add and account.update
// receive: the completed browser sign-in; nothing for GNOME Online
// Accounts (its token source holds the sign-in) or for a browser sign-in
// account tested with its stored sign-in; the typed password otherwise.
func (w *Wizard) credentials() api.Credentials {
	if w.linkedCfg == nil {
		return credentialsFor(w.readIdentity())
	}
	if signin.KindOf(*w.linkedCfg) == signin.OAuth && w.oauth.complete && w.oauth.session != "" {
		return api.Credentials{OAuthSession: w.oauth.session}
	}
	return api.Credentials{}
}

// onNext validates the identity page and asks the daemon for server
// settings. A Google or Microsoft 365 address goes to the connection test
// (signed in through GNOME Online Accounts), to the sign-in hint or to the
// browser sign-in; an IMAP hit goes to the connection test; a miss opens
// the Servers page with guessed defaults. The password is asked for only
// once the account turns out to need one.
func (w *Wizard) onNext() {
	id := w.readIdentity()
	if p := ValidateIdentity(id, false); p.Any() {
		w.email.AddCSSClass("error")
		w.email.GrabFocus()
		return
	}
	id.Email, _ = ValidateEmail(id.Email)
	if w.editing != nil {
		if w.linkedCfg != nil {
			w.nav.ReplaceWithTags([]string{tagIdentity, tagTesting})
			w.runTest()
			return
		}
		// The servers are known; only the identity may have changed.
		w.nav.PushByTag(tagServers)
		return
	}
	// A new account's way is decided afresh: a sign-in of an earlier
	// attempt is dropped.
	w.linkedCfg = nil
	w.cancelSession()
	if w.appPassword != nil {
		if strings.EqualFold(w.appPassword.Email, id.Email) {
			if w.requirePassword(i18n.T("Enter the app password for this account")) {
				w.useAppPassword()
			}
			return
		}
		w.appPassword = nil
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
			d := signin.ClassifyDiscovery(res, err)
			switch d.Path {
			case signin.PathGOA:
				// Signed in through GNOME Online Accounts: the daemon's
				// account is complete.
				w.log.Info("account discovered", "source", res.Source)
				w.startLinked(*d.Config)
				return
			case signin.PathGOAHint:
				// GNOME Online Accounts could sign it in, but has not yet.
				w.log.Info("account discovered", "source", res.Source, "browser", d.OAuthAlt != nil)
				w.showGOAHint(d, res.ProviderName)
				return
			case signin.PathOAuth:
				w.log.Info("account discovered", "source", res.Source, "appPassword", d.PasswordAlt != nil)
				w.showOAuthPrompt(d.Provider, d.Config, d.PasswordAlt)
				return
			}
			if !w.requirePassword(i18n.T("Enter the password for this account")) {
				return
			}
			if d.Config == nil {
				w.log.Debug("account.discover", "err", err, "source", res.Source)
				w.applyConfig(MergeIdentity(GuessConfig(id.Email), id))
				w.nav.PushByTag(tagServers)
				return
			}
			w.log.Info("account discovered", "source", res.Source)
			w.applyConfig(MergeIdentity(*d.Config, id))
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
	w.edit.SetVisible(w.linkedCfg == nil || w.signInAgain)
	w.retry.SetVisible(o == OutcomeFailed)
	w.addAnyway.SetVisible(o == OutcomeFailed)
	w.add.SetVisible(o == OutcomeOK)
	w.imap.trust.SetVisible(w.imap.offer != nil)
	w.smtp.trust.SetVisible(w.smtp.offer != nil)
}

func (w *Wizard) hideButtons() {
	for _, b := range []*gtk.Button{w.edit, w.retry, w.addAnyway, w.add, w.imap.trust, w.smtp.trust} {
		b.SetVisible(false)
	}
}

// runTest calls account.test with the current settings.
func (w *Wizard) runTest() {
	w.progress.SetTitle(i18n.T("Testing Connection…"))
	w.testingStack.SetVisibleChildName("progress")
	w.hideButtons()
	params := api.AccountTestParams{Config: w.assembleConfig(), Credentials: w.credentials()}
	w.tested = params.Config
	if w.editing != nil {
		// Empty credentials mean "use the stored password or sign-in".
		params.AccountID = w.editing.ID
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
	// A Graph account has one endpoint, the mailbox; a Google account is
	// tested like any IMAP one, only without a password to correct.
	linked := w.linkedCfg != nil
	browser := linked && signin.KindOf(*w.linkedCfg) == signin.OAuth
	graph := linked && w.linkedCfg.Protocol() == api.AccountGraph
	w.graphRow.SetVisible(graph)
	w.imapRow.SetVisible(!graph)
	w.smtpRow.SetVisible(!graph)
	w.results.SetDescription("")
	w.imap.offer, w.smtp.offer = nil, nil
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
		if !linked {
			w.imap.offer = offerFor(res.IMAP, w.tested.IMAP)
			w.smtp.offer = offerFor(res.SMTP, w.tested.SMTP)
		}
	}
	w.signInAgain = false
	switch {
	case browser && (outcome == OutcomeAuthFailed || signin.TestNeedsSignIn(res, err)):
		// No password to correct either: the browser sign-in, or the
		// permissions granted in it, must be renewed.
		outcome = OutcomeFailed
		w.signInAgain = true
		w.results.SetDescription(i18n.T("The server refused the sign-in. Sign in again and make sure access to mail is allowed."))
	case linked && outcome == OutcomeAuthFailed:
		// There is no password to correct here: the sign-in, or the
		// permissions it was granted, live in GNOME Online Accounts.
		outcome = OutcomeFailed
		w.results.SetDescription(i18n.T("The server refused the sign-in. Sign in to the account again in Settings → Online Accounts and make sure access to mail is allowed."))
	}
	if w.signInAgain {
		w.edit.SetLabel(i18n.T("_Sign In Again"))
	} else {
		w.edit.SetLabel(i18n.T("_Edit Servers"))
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

// onEdit is the results page's first button: back to the Servers page,
// or, when the browser sign-in was refused, to the browser page.
func (w *Wizard) onEdit() {
	if w.signInAgain {
		w.onSignInAgain()
		return
	}
	w.nav.PopToTag(tagServers)
}

// onAdd stores the account (account.add, or account.update when editing)
// and closes on success.
func (w *Wizard) onAdd() {
	cfg := w.assembleConfig()
	creds := w.credentials()
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
			// The daemon consumed the sign-in: closing must not cancel it.
			w.oauth = oauthState{}
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
