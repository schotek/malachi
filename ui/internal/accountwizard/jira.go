// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

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

// jiraPageTags are the navigation page tags of jira_wizard.blp, by page.
var jiraPageTags = [...]string{jira.PageSite: "site", jira.PageCredentials: "credentials", jira.PageSpaces: "spaces"}

func jiraPageOfTag(tag string) (jira.Page, bool) {
	i := slices.Index(jiraPageTags[:], tag)
	return jira.Page(i), i >= 0
}

// jiraPageView is what every page of the Jira assistant has: its banner,
// the progress of a call under way and its button.
type jiraPageView struct {
	page          *adw.NavigationPage
	banner        *adw.Banner
	progress      *gtk.Box
	progressLabel *gtk.Label
	button        *gtk.Button
}

// jiraSpaceRow is a space on the spaces page: a check row titled
// "KEY – Name" with the estimate of its issues at the end.
type jiraSpaceRow struct {
	id    string
	row   *adw.ActionRow
	check *gtk.CheckButton
	count *gtk.Label
}

// JiraWizard is the assistant that adds a Jira account (kind jira), or
// replaces the token of one (NewJiraEdit): the pages site → credentials →
// spaces of the macOS sheet in an Adw.Dialog of the mail assistant's size.
// The flow, its texts and its calls are jiraFlow (jira_flow.go); this only
// shows what it delivers and feeds the fields back. Everything from the
// site (its title, the spaces' names) and the daemon's error sentences are
// plain text.
type JiraWizard struct {
	*adw.Dialog

	// OnDone runs on the main loop after account.add or account.update
	// succeeded, before the dialog closes. May be nil.
	OnDone func(id api.AccountID, cfg api.AccountConfig)

	flow *jiraFlow
	log  *slog.Logger

	nav    *adw.NavigationView
	toasts *adw.ToastOverlay
	pages  [3]jiraPageView

	siteRow    *adw.EntryRow
	siteStatus *gtk.Label

	credentialsHelp *gtk.Label
	helpButton      *gtk.Button
	loginRow        *adw.EntryRow
	tokenRow        *adw.PasswordEntryRow

	spacesList    *gtk.ListBox
	noSpacesRow   *adw.ActionRow
	spacesProblem *gtk.Label
	offlineRow    *adw.ComboRow
	onlyMineRow   *adw.SwitchRow
	emailRow      *adw.EntryRow

	// shown is the navigation stack as the view shows it; it follows the
	// flow's pages (showPages) and the user's own pops.
	shown []jira.Page
	// siteOK, siteProblem and detected are the site field's state
	// (onSiteCheck, onDetected): the text under it and its Next.
	siteOK                bool
	siteProblem, detected string
	// spaceRows are the rows of spaces_list in the flow's order, and
	// spacesProblemText why the account cannot be added yet.
	spaceRows         []jiraSpaceRow
	spacesProblemText string

	busy    bool
	closed  bool
	started bool
}

// NewJira is the assistant that adds a Jira account. Present it with
// Present(parent).
func NewJira(c *client.Client, log *slog.Logger) *JiraWizard {
	return newJira(c, log, nil)
}

// NewJiraEdit opens on the credentials page of account a to replace its
// token (account.update); reason is the notify.authRequired reason that
// led here (api.CodeAuthRequired / api.CodeAuthFailed), 0 when not known;
// the page's banner says why (jira.CredentialFields TokenPrompt /
// Rejected), with the token field flagged.
func NewJiraEdit(c *client.Client, log *slog.Logger, a api.Account, reason api.ErrorCode) *JiraWizard {
	w := newJira(c, log, &a)
	// The token is what this dialog asks for; focus needs the dialog in a
	// window, so it is the dialog's focus widget until it is presented.
	w.SetFocus(w.tokenRow)
	if reason != 0 {
		w.flow.requestToken(reason)
	}
	return w
}

func newJira(c *client.Client, log *slog.Logger, editing *api.Account) *JiraWizard {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("component", "jirawizard")
	b := data.Builder("jira_wizard.ui")
	label := func(id string) *gtk.Label { return b.GetObject(id).Cast().(*gtk.Label) }
	pageView := func(prefix, button string) jiraPageView {
		return jiraPageView{
			page:          b.GetObject(prefix + "_page").Cast().(*adw.NavigationPage),
			banner:        b.GetObject(prefix + "_banner").Cast().(*adw.Banner),
			progress:      b.GetObject(prefix + "_progress").Cast().(*gtk.Box),
			progressLabel: label(prefix + "_progress_label"),
			button:        b.GetObject(button).Cast().(*gtk.Button),
		}
	}

	w := &JiraWizard{
		Dialog: b.GetObject("jira_wizard").Cast().(*adw.Dialog),
		log:    log,
		nav:    b.GetObject("jira_nav").Cast().(*adw.NavigationView),
		toasts: b.GetObject("jira_toasts").Cast().(*adw.ToastOverlay),
		pages: [...]jiraPageView{
			jira.PageSite:        pageView("site", "site_next_button"),
			jira.PageCredentials: pageView("credentials", "credentials_next_button"),
			jira.PageSpaces:      pageView("spaces", "spaces_add_button"),
		},
		siteRow:         b.GetObject("site_row").Cast().(*adw.EntryRow),
		siteStatus:      label("site_status"),
		credentialsHelp: label("credentials_help"),
		helpButton:      b.GetObject("credentials_help_button").Cast().(*gtk.Button),
		loginRow:        b.GetObject("login_row").Cast().(*adw.EntryRow),
		tokenRow:        b.GetObject("token_row").Cast().(*adw.PasswordEntryRow),
		spacesList:      b.GetObject("spaces_list").Cast().(*gtk.ListBox),
		spacesProblem:   label("spaces_problem"),
		offlineRow:      b.GetObject("offline_row").Cast().(*adw.ComboRow),
		onlyMineRow:     b.GetObject("only_mine_row").Cast().(*adw.SwitchRow),
		emailRow:        b.GetObject("email_row").Cast().(*adw.EntryRow),
		// The first page of a navigation view in a UI file is its root.
		shown: []jira.Page{jira.PageSite},
	}
	post := func(f func()) { glib.IdleAdd(f) }
	w.flow = newJiraFlow(c, post, i18n.Tr, widget.RPCErrorText, log, editing)
	w.setTexts(label("site_description"), label("spaces_description"))
	w.wire()
	w.showPages(slices.Clone(w.flow.pages))
	return w
}

// setTexts fills in the fixed texts (every text of the dialog comes from
// the flow and ui/internal/jira) and the fields the flow starts with.
func (w *JiraWizard) setTexts(siteDescription, spacesDescription *gtk.Label) {
	f := w.flow
	t := f.texts
	w.SetTitle(f.title())
	for p := range w.pages {
		pv := w.pages[p]
		pv.page.SetTitle(f.pageTitle(jira.Page(p)))
		pv.button.SetLabel(f.nextLabel(jira.Page(p)))
		// The banner says why a call failed, with the daemon's text.
		pv.banner.SetUseMarkup(false)
	}
	siteDescription.SetText(t.SiteDescription)
	w.siteRow.SetTitle(t.SiteAddress)
	w.siteRow.SetText(f.siteInput)
	spacesDescription.SetText(t.SpacesDescription)

	w.noSpacesRow = adw.NewActionRow()
	w.noSpacesRow.SetUseMarkup(false)
	w.noSpacesRow.SetTitle(t.NoSpaces)
	w.noSpacesRow.SetSensitive(false)
	w.spacesList.Append(w.noSpacesRow)

	w.offlineRow.SetTitle(t.KeepOffline)
	w.offlineRow.SetSubtitle(t.KeepOfflineSubtitle)
	w.offlineRow.SetModel(gtk.NewStringList(f.offlineLabels()))
	w.offlineRow.SetSelected(uint(f.offlineIndex()))
	w.onlyMineRow.SetTitle(t.OnlyMine)
	w.onlyMineRow.SetSubtitle(t.OnlyMineSubtitle)
	w.onlyMineRow.SetActive(f.onlyMine)
	w.emailRow.SetTitle(f.emailLabel())
}

func (w *JiraWizard) wire() {
	f := w.flow
	f.onPages = w.showPages
	f.onBusy = w.showBusy
	f.onSiteCheck = func(ok bool, problem string) {
		w.siteOK, w.siteProblem = ok, problem
		w.applySite()
	}
	f.onDetected = func(text string) {
		w.detected = text
		w.applySite()
	}
	f.onCredentialPage = w.showCredentialPage
	f.onSpaces = w.showSpaces
	f.onSpacesProblem = func(text string) {
		w.spacesProblemText = text
		w.applySpaces()
	}
	f.onEmailField = w.showEmailField
	f.onBanner = w.showBanner
	f.onProblems = w.showProblems
	f.onFocus = w.focus
	f.onOpenURL = w.openURL
	f.onDone = func(id api.AccountID, cfg api.AccountConfig) {
		if w.OnDone != nil {
			w.OnDone(id, cfg)
		}
		w.ForceClose()
	}

	w.siteRow.ConnectChanged(func() { f.setSite(w.siteRow.Text()) })
	siteNext := func() {
		f.setSite(w.siteRow.Text())
		f.next()
	}
	w.siteRow.ConnectEntryActivated(siteNext)
	w.pages[jira.PageSite].button.ConnectClicked(siteNext)

	syncCredentials := func() { f.setCredentials(w.loginRow.Text(), w.tokenRow.Text()) }
	w.loginRow.ConnectChanged(syncCredentials)
	w.tokenRow.ConnectChanged(syncCredentials)
	// Enter in the address goes to the token, in the token it is Next.
	w.loginRow.ConnectEntryActivated(func() {
		syncCredentials()
		w.tokenRow.GrabFocus()
	})
	credentialsNext := func() {
		syncCredentials()
		f.next()
	}
	w.tokenRow.ConnectEntryActivated(credentialsNext)
	w.pages[jira.PageCredentials].button.ConnectClicked(credentialsNext)
	w.helpButton.ConnectClicked(f.openTokenHelp)

	w.emailRow.ConnectChanged(func() { f.setEmail(w.emailRow.Text()) })
	spacesNext := func() {
		f.setEmail(w.emailRow.Text())
		f.next()
	}
	w.emailRow.ConnectEntryActivated(spacesNext)
	w.pages[jira.PageSpaces].button.ConnectClicked(spacesNext)
	w.offlineRow.NotifyProperty("selected", func() { f.setOfflineIndex(int(w.offlineRow.Selected())) })
	w.onlyMineRow.NotifyProperty("active", func() { f.setOnlyMine(w.onlyMineRow.Active()) })

	// A pop of the user's own (the header's Back, Escape, a swipe) is the
	// flow's Back, which drops a call under way. A pop showPages made is
	// the flow's already: its page is gone from shown and from the flow.
	w.nav.ConnectPopped(func(page *adw.NavigationPage) {
		p, ok := jiraPageOfTag(page.Tag())
		n := len(w.shown)
		if !ok || w.closed || n == 0 || w.shown[n-1] != p || f.top() != p {
			return
		}
		w.shown = w.shown[:n-1]
		f.back()
	})

	w.ConnectClosed(func() {
		w.closed = true
		f.close()
	})
}

// Present shows the dialog over parent; the first time, the flow delivers
// its initial state (and focuses the token field when asked for it).
func (w *JiraWizard) Present(parent gtk.Widgetter) {
	w.Dialog.Present(parent)
	if w.started {
		return
	}
	w.started = true
	w.flow.start()
}

// showPages mirrors the flow's page stack in the navigation view: a push,
// a pop to a page, or a replacement (the initial page of the edit
// dialog).
func (w *JiraWizard) showPages(pages []jira.Page) {
	cur := w.shown
	w.shown = pages
	n := len(pages)
	switch {
	case n == 0 || slices.Equal(pages, cur):
		return
	case n == len(cur)+1 && slices.Equal(pages[:n-1], cur):
		w.nav.PushByTag(jiraPageTags[pages[n-1]])
	case n < len(cur) && slices.Equal(cur[:n], pages):
		w.nav.PopToTag(jiraPageTags[pages[n-1]])
	default:
		tags := make([]string, n)
		for i, p := range pages {
			tags[i] = jiraPageTags[p]
		}
		w.nav.ReplaceWithTags(tags)
	}
	if pages[n-1] == jira.PageSpaces && w.emailRow.Visible() {
		w.emailRow.GrabFocus()
	}
}

// showBusy shows a call under way (its progress text) or none (""): the
// pages wait with their fields and buttons insensitive; the header's Back
// and the close button stay usable.
func (w *JiraWizard) showBusy(progress string) {
	w.busy = progress != ""
	for _, pv := range w.pages {
		if w.busy {
			pv.progressLabel.SetText(progress)
		}
		pv.progress.SetVisible(w.busy)
	}
	w.applySite()
	w.applyCredentials()
	w.applySpaces()
}

// applySite shows the text under the site field, the problem with the
// address first, else the site found; Next follows the field.
func (w *JiraWizard) applySite() {
	text := w.siteProblem
	if text != "" {
		w.siteStatus.RemoveCSSClass("dim-label")
		w.siteStatus.AddCSSClass("error")
	} else {
		text = w.detected
		w.siteStatus.RemoveCSSClass("error")
		w.siteStatus.AddCSSClass("dim-label")
	}
	w.siteStatus.SetText(text)
	w.siteStatus.SetVisible(text != "")
	w.siteRow.SetSensitive(!w.busy)
	w.pages[jira.PageSite].button.SetSensitive(w.siteOK && !w.busy)
}

// showCredentialPage is the credentials page for the site's deployment;
// the address comes from the flow (the edited account's, or what was
// typed before).
func (w *JiraWizard) showCredentialPage(p jira.CredentialPage) {
	w.credentialsHelp.SetText(p.Help)
	w.credentialsHelp.SetVisible(p.Help != "")
	w.helpButton.SetLabel(p.HelpButton)
	w.helpButton.SetVisible(p.HelpButton != "")
	w.loginRow.SetTitle(p.LoginLabel)
	w.loginRow.SetVisible(p.ShowsLogin)
	w.tokenRow.SetTitle(p.TokenLabel)
	if w.loginRow.Text() != w.flow.login {
		w.loginRow.SetText(w.flow.login)
	}
	w.applyCredentials()
}

func (w *JiraWizard) applyCredentials() {
	w.loginRow.SetSensitive(!w.busy && w.flow.loginEditable())
	w.tokenRow.SetSensitive(!w.busy)
	w.helpButton.SetSensitive(!w.busy)
	w.pages[jira.PageCredentials].button.SetSensitive(!w.busy)
}

// showSpaces shows the spaces page's list. The same spaces again (new
// estimates for another offline window) are updated in place, so the
// list keeps its scroll position; others replace the rows.
func (w *JiraWizard) showSpaces(rows []jira.SpaceRow, selected map[string]bool) {
	same := len(rows) == len(w.spaceRows)
	for i := 0; same && i < len(rows); i++ {
		same = rows[i].ID == w.spaceRows[i].id
	}
	if !same {
		for _, sr := range w.spaceRows {
			w.spacesList.Remove(sr.row)
		}
		w.spaceRows = make([]jiraSpaceRow, 0, len(rows))
		for _, r := range rows {
			sr := w.newSpaceRow(r.ID)
			w.spaceRows = append(w.spaceRows, sr)
			w.spacesList.Append(sr.row)
		}
	}
	for i, r := range rows {
		sr := w.spaceRows[i]
		sr.row.SetTitle(r.Title)
		// A long name is cut to one line; the tooltip (plain text) has it
		// whole.
		sr.row.SetTooltipText(r.Title)
		sr.count.SetText(r.Count)
		sr.count.SetVisible(r.Count != "")
		// A change calls setSpace with what the flow holds already.
		sr.check.SetActive(selected[r.ID])
	}
	w.noSpacesRow.SetVisible(len(rows) == 0)
	w.applySpaces()
}

func (w *JiraWizard) newSpaceRow(id string) jiraSpaceRow {
	sr := jiraSpaceRow{id: id, row: adw.NewActionRow(), check: gtk.NewCheckButton(), count: gtk.NewLabel("")}
	sr.row.SetUseMarkup(false)
	sr.row.SetTitleLines(1)
	sr.check.SetVAlign(gtk.AlignCenter)
	sr.row.AddPrefix(sr.check)
	sr.row.SetActivatableWidget(sr.check)
	sr.count.SetUseMarkup(false)
	sr.count.AddCSSClass("dim-label")
	sr.count.AddCSSClass("numeric")
	sr.row.AddSuffix(sr.count)
	check := sr.check
	check.ConnectToggled(func() { w.flow.setSpace(id, check.Active()) })
	return sr
}

// applySpaces shows why the account cannot be added yet (under a list
// that has spaces) and what can be used while no call runs.
func (w *JiraWizard) applySpaces() {
	w.spacesProblem.SetText(w.spacesProblemText)
	w.spacesProblem.SetVisible(w.spacesProblemText != "" && len(w.spaceRows) > 0)
	w.spacesList.SetSensitive(!w.busy)
	w.offlineRow.SetSensitive(!w.busy)
	w.onlyMineRow.SetSensitive(!w.busy)
	w.emailRow.SetSensitive(!w.busy)
	w.pages[jira.PageSpaces].button.SetSensitive(!w.busy && w.spacesProblemText == "" && len(w.spaceRows) > 0)
}

// showEmailField shows or hides the account's address on the spaces page
// (a Data Center site that does not reveal it).
func (w *JiraWizard) showEmailField(shown bool, email string) {
	if w.emailRow.Text() != email {
		w.emailRow.SetText(email)
	}
	w.emailRow.SetVisible(shown)
}

// showBanner shows a page's banner; "" hides it (the title stays for the
// animation).
func (w *JiraWizard) showBanner(p jira.Page, text string) {
	b := w.pages[p].banner
	if text != "" {
		b.SetTitle(text)
	}
	b.SetRevealed(text != "")
}

// showProblems flags the fields of s and clears the others.
func (w *JiraWizard) showProblems(s jiraFields) {
	for _, fw := range []struct {
		field jiraField
		row   gtk.Widgetter
	}{{fieldSite, w.siteRow}, {fieldLogin, w.loginRow}, {fieldToken, w.tokenRow}, {fieldEmail, w.emailRow}} {
		base := gtk.BaseWidget(fw.row)
		if s.has(fw.field) {
			base.AddCSSClass("error")
		} else {
			base.RemoveCSSClass("error")
		}
	}
}

func (w *JiraWizard) focus(field jiraField) {
	switch field {
	case fieldSite:
		w.siteRow.GrabFocus()
	case fieldLogin:
		w.loginRow.GrabFocus()
	case fieldToken:
		w.tokenRow.GrabFocus()
	case fieldEmail:
		w.emailRow.GrabFocus()
	}
}

// openURL opens the page where a Jira Cloud user creates an API token.
func (w *JiraWizard) openURL(url string) {
	widget.LaunchURI(nil, url, func(err error) {
		if err != nil && !w.closed {
			w.log.Warn("open the token page", "err", err)
			w.toasts.AddToast(widget.PlainToast(widget.LaunchErrorText(err)))
		}
	})
}
