// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// RPC budgets of the Jira assistant (docs/api.md §4.1). account.detectSite
// asks the typed address a few anonymous questions; account.listSpaces
// signs in and lists, and counts, the spaces. account.add and
// account.update get addTimeout, as for a mail account: the keyring may
// prompt.
const (
	detectSiteTimeout = 15 * time.Second
	listSpacesTimeout = 45 * time.Second
)

// caller is the part of the RPC client (client.Client) the Jira assistant
// uses; the tests pass a fake daemon.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// jiraField is a field of the Jira assistant that can be flagged and
// focused.
type jiraField uint8

// The fields.
const (
	fieldSite jiraField = 1 << iota
	fieldLogin
	fieldToken
	// fieldEmail is the account's address on a Data Center site that
	// hides it.
	fieldEmail
)

// jiraFields is a set of fields.
type jiraFields uint8

func fieldSet(fs ...jiraField) jiraFields {
	var s jiraFields
	for _, f := range fs {
		s |= jiraFields(f)
	}
	return s
}

func (s jiraFields) has(f jiraField) bool { return s&jiraFields(f) != 0 }

// jiraFlow is the flow of the assistant that adds a Jira account (kind
// jira), with the widgets replaced by callbacks: the port of the macOS
// JiraWizardController, and its tests are ported with it. The pages,
// texts and rules are ui/internal/jira/wizard.go; JiraWizard (jira.go) is
// the thin GTK view over this.
//
// Three pages: the site (account.detectSite), the credentials (checked by
// account.listSpaces, which also lists the spaces with an estimate of
// their issues) and the spaces with the offline window and "Only Issues
// Involving Me" (account.add). Editing an account (its token only) opens
// on the credentials page and saves the unchanged configuration with the
// new token through account.update.
//
// Every method and callback runs on the main loop: a call's reply comes
// back through post (glib.IdleAdd in the dialog). A reply is dropped once
// the assistant closed or anything else started since (op: Back, another
// call). Nothing typed here is logged; the token leaves only in
// api.Credentials.
//
// Wire the callbacks, then call start.
type jiraFlow struct {
	// onPages: the page stack changed; the last page is the visible one.
	onPages func([]jira.Page)
	// onBusy: a call started, with its progress text, or finished ("").
	// The visible page waits for it; Back stays usable and drops its
	// reply.
	onBusy func(progress string)
	// onSiteCheck: what the site field allows now, Next and the text under
	// the field ("" for none; jira.CheckSiteInput).
	onSiteCheck func(ok bool, problem string)
	// onDetected: the text under the site field once the site answered
	// (jira.Detected); "" when the address changed since.
	onDetected func(string)
	// onCredentialPage: the credentials page for the site's deployment
	// (jira.CredentialFields); the fields are login and an empty token.
	onCredentialPage func(jira.CredentialPage)
	// onSpaces: the spaces page's list, in the daemon's order, with the
	// ids chosen.
	onSpaces func(rows []jira.SpaceRow, selected map[string]bool)
	// onSpacesProblem: why the spaces page cannot add the account now (""
	// when it can; jira.SpacesProblem).
	onSpacesProblem func(string)
	// onEmailField: whether the spaces page asks for the account's e-mail
	// address (a Data Center site that does not reveal it,
	// jira.NeedsEmail), and the address to show in the field.
	onEmailField func(shown bool, email string)
	// onBanner: a page's banner; "" hides it.
	onBanner func(page jira.Page, text string)
	// onProblems: the fields to flag; an empty set clears the flags.
	onProblems func(jiraFields)
	// onFocus: move the keyboard focus to a field.
	onFocus func(jiraField)
	// onOpenURL: open the page where a Jira Cloud user creates an API
	// token.
	onOpenURL func(string)
	// onDone: the account was stored; the view closes the assistant.
	onDone func(api.AccountID, api.AccountConfig)

	call caller
	post func(func())
	tr   jira.Translator
	// errorText is the client's sentence for a failed call whose step has
	// no banner of its own (widget.RPCErrorText).
	errorText func(what string, err error) string
	log       *slog.Logger

	// editing is the account whose token is replaced; nil when adding one.
	editing *api.Account
	// texts are the fixed texts (jira.WizardTexts).
	texts jira.WizardStrings

	pages  []jira.Page
	closed bool
	// op is bumped per RPC and by Back so stale replies bail out.
	op int
	// progress is the progress text of the call under way; "" when none
	// runs.
	progress string

	// siteInput is the site field as typed.
	siteInput string
	// site is what account.detectSite found for siteInput; nil until it
	// answered and again once the address changed. When editing, the
	// account's site.
	site *api.AccountDetectSiteResult
	// login is the Atlassian account's e-mail address (Jira Cloud's
	// login).
	login string
	// token is the token as typed; sent trimmed.
	token string
	// email is the account's address on Data Center (the signed-in
	// user's, or typed when the site hides it).
	email string
	// user is who account.listSpaces signed in as.
	user *api.SiteUser
	// spaces are the spaces of the last account.listSpaces, in its order.
	spaces []api.Space
	// selected are the ids of the chosen spaces.
	selected    map[string]bool
	onlyMine    bool
	offlineDays int
	// needsEmail: the spaces page asks for the account's address.
	needsEmail bool

	// spacesSite is the site spaces were listed for: a space id means
	// nothing on another site, so the choice starts over there.
	spacesSite string
	problems   jiraFields
	// tokenRequest is requestToken's reason, shown by start; 0 for none.
	tokenRequest api.ErrorCode
	started      bool
}

// newJiraFlow builds the flow; editing (nil when adding) opens on the
// credentials page and saves with account.update, the configuration
// staying as it is.
func newJiraFlow(c caller, post func(func()), tr jira.Translator, errorText func(string, error) string,
	log *slog.Logger, editing *api.Account) *jiraFlow {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	f := &jiraFlow{
		call:        c,
		post:        post,
		tr:          tr,
		errorText:   errorText,
		log:         log,
		editing:     editing,
		texts:       jira.WizardTexts(tr),
		pages:       []jira.Page{jira.PageSite},
		selected:    map[string]bool{},
		offlineDays: api.DefaultJiraOfflineDays,
	}
	if editing == nil {
		return f
	}
	f.pages = []jira.Page{jira.PageCredentials}
	f.email = editing.Config.Email
	if jc := editing.Config.Jira; jc != nil {
		f.siteInput = jc.SiteURL
		f.site = &api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: jc.SiteURL, Deployment: jc.Deployment, CloudID: jc.CloudID}
		f.login = jc.Login
		f.offlineDays = jira.OfflineChoices[jira.IndexOfOfflineDays(jc.OfflineDays)]
		f.onlyMine = jc.OnlyMine
	}
	return f
}

// Presentation.

func (f *jiraFlow) isEditing() bool { return f.editing != nil }

// title is the dialog's title.
func (f *jiraFlow) title() string {
	if f.isEditing() {
		return f.tr.T("Edit Account")
	}
	return f.texts.Title
}

// deployment is the deployment the pages are for: the found site's, Jira
// Cloud before one was found.
func (f *jiraFlow) deployment() api.JiraDeployment {
	if f.site == nil {
		return api.JiraCloud
	}
	return f.site.Deployment
}

// credentialPage is the credentials page of deployment.
func (f *jiraFlow) credentialPage() jira.CredentialPage {
	return jira.CredentialFields(f.deployment(), f.tr)
}

// loginEditable: the login belongs to the account being edited (its
// address and its uniqueness); only the token changes.
func (f *jiraFlow) loginEditable() bool { return !f.isEditing() }

// pageTitle is a page's title in its header.
func (f *jiraFlow) pageTitle(p jira.Page) string {
	switch p {
	case jira.PageCredentials:
		return f.tr.T("Sign In")
	case jira.PageSpaces:
		return f.texts.SpacesTitle
	}
	return f.texts.SiteTitle
}

// nextLabel is a page's button, with its mnemonic: Next, Save when
// editing, Add Account on the spaces.
func (f *jiraFlow) nextLabel(p jira.Page) string {
	switch {
	case p == jira.PageSpaces:
		return f.tr.T("_Add Account")
	case p == jira.PageCredentials && f.isEditing():
		return f.tr.T("_Save")
	}
	return f.tr.T("_Next")
}

// emailLabel is the title of the spaces page's address field.
func (f *jiraFlow) emailLabel() string { return f.tr.T("E-mail Address") }

// canGoBack: the header's Back is offered.
func (f *jiraFlow) canGoBack() bool { return len(f.pages) > 1 }

// top is the visible page.
func (f *jiraFlow) top() jira.Page { return f.pages[len(f.pages)-1] }

// offlineLabels are the labels of the offline window's choices, and
// offlineIndex the one shown.
func (f *jiraFlow) offlineLabels() []string { return jira.OfflineChoiceLabels(f.tr) }
func (f *jiraFlow) offlineIndex() int       { return jira.IndexOfOfflineDays(f.offlineDays) }

// busy: a call runs.
func (f *jiraFlow) busy() bool { return f.progress != "" }

// Lifecycle.

// start delivers the initial state to the callbacks. Call once, after
// wiring them.
func (f *jiraFlow) start() {
	ok, problem := jira.CheckSiteInput(f.siteInput, f.tr)
	f.onSiteCheck(ok, problem)
	f.onCredentialPage(f.credentialPage())
	f.onPages(slices.Clone(f.pages))
	f.started = true
	f.showTokenRequest()
}

// requestToken makes the edit assistant of an account whose token is
// missing (authRequired) or was refused (authFailed) say so in the
// credentials page's banner, with the token field flagged and focused
// (the sign-in banner's button; Wizard.RequestPassword for mail accounts).
// Ignored when adding. Call before start.
func (f *jiraFlow) requestToken(reason api.ErrorCode) {
	if !f.isEditing() {
		return
	}
	f.tokenRequest = reason
	if f.started {
		f.showTokenRequest()
	}
}

func (f *jiraFlow) showTokenRequest() {
	reason := f.tokenRequest
	if reason == 0 {
		return
	}
	f.tokenRequest = 0
	fl := jira.FailureOf(jira.StepSave, jira.ClassOf(reason), f.deployment(), true, f.tr)
	if fl.Banner != "" {
		f.onBanner(jira.PageCredentials, fl.Banner)
	}
	f.flag(fieldSet(fieldToken))
	f.onFocus(fieldToken)
}

// close: the assistant went away; every late reply is dropped from now
// on.
func (f *jiraFlow) close() {
	f.closed = true
	f.progress = ""
}

// Inputs from the view.

// setSite is the site field as typed. A changed address forgets the site
// found for the previous one.
func (f *jiraFlow) setSite(raw string) {
	if raw == f.siteInput {
		return
	}
	f.siteInput = raw
	ok, problem := jira.CheckSiteInput(raw, f.tr)
	f.onSiteCheck(ok, problem)
	f.clear(fieldSite)
	f.onBanner(jira.PageSite, "")
	if !f.isEditing() && f.site != nil {
		f.site = nil
		f.onDetected("")
	}
}

// setCredentials is the credentials as typed. The login of an account
// being edited stays its own.
func (f *jiraFlow) setCredentials(login, token string) {
	if f.isEditing() {
		login = f.login
	}
	if login == f.login && token == f.token {
		return
	}
	if login != f.login {
		f.clear(fieldLogin)
	}
	if token != f.token {
		f.clear(fieldToken)
	}
	f.login, f.token = login, token
	f.onBanner(jira.PageCredentials, "")
}

// setEmail is the account's address on the spaces page (Data Center).
func (f *jiraFlow) setEmail(s string) {
	if s == f.email {
		return
	}
	f.email = s
	f.clear(fieldEmail)
}

// setSpace is a space's check box.
func (f *jiraFlow) setSpace(id string, on bool) {
	known := slices.ContainsFunc(f.spaces, func(s api.Space) bool { return s.ID == id })
	if !known || on == f.selected[id] {
		return
	}
	if on {
		f.selected[id] = true
	} else {
		delete(f.selected, id)
	}
	f.onSpacesProblem(jira.SpacesProblem(len(f.selected), f.tr))
	f.onBanner(jira.PageSpaces, "")
}

// setOnlyMine is "Only Issues Involving Me".
func (f *jiraFlow) setOnlyMine(on bool) { f.onlyMine = on }

// setOfflineIndex is the offline window's choice (an index of
// jira.OfflineChoices). The estimates on the spaces page count the issues
// of the window, so a new window asks for them again.
func (f *jiraFlow) setOfflineIndex(i int) {
	if i < 0 || i >= len(jira.OfflineChoices) {
		return
	}
	days := jira.OfflineChoices[i]
	if days == f.offlineDays {
		return
	}
	f.offlineDays = days
	if !f.isEditing() && f.top() == jira.PageSpaces && len(f.spaces) > 0 {
		f.refreshCounts()
	}
}

// next is the visible page's button: look the site up, check the
// credentials and list the spaces (or save the token when editing), add
// the account.
func (f *jiraFlow) next() {
	if f.closed || f.busy() {
		return
	}
	switch f.top() {
	case jira.PageSite:
		f.detect()
	case jira.PageCredentials:
		if f.isEditing() {
			f.save()
		} else {
			f.loadSpaces()
		}
	case jira.PageSpaces:
		f.add()
	}
}

// back is the header's Back: it pops the visible page; a call under way
// is dropped.
func (f *jiraFlow) back() {
	if !f.canGoBack() {
		return
	}
	f.op++
	f.setBusy("")
	f.pages = f.pages[:len(f.pages)-1]
	f.onPages(slices.Clone(f.pages))
}

// openTokenHelp is the credentials page's "Create API Token…" (Jira Cloud
// only).
func (f *jiraFlow) openTokenHelp() {
	if url := f.credentialPage().HelpURL; url != "" {
		f.onOpenURL(url)
	}
}

// Navigation.

func (f *jiraFlow) push(p jira.Page) {
	if i := slices.Index(f.pages, p); i >= 0 {
		f.pages = f.pages[:i+1]
	} else {
		f.pages = append(f.pages, p)
	}
	f.onPages(slices.Clone(f.pages))
}

func (f *jiraFlow) popTo(p jira.Page) {
	i := slices.Index(f.pages, p)
	if i < 0 || i+1 >= len(f.pages) {
		return
	}
	f.pages = f.pages[:i+1]
	f.onPages(slices.Clone(f.pages))
}

// Fields.

func (f *jiraFlow) setBusy(text string) {
	if f.progress == text {
		return
	}
	f.progress = text
	f.onBusy(text)
}

func (f *jiraFlow) flag(s jiraFields) {
	f.problems = s
	f.onProblems(s)
}

func (f *jiraFlow) clear(field jiraField) {
	if !f.problems.has(field) {
		return
	}
	f.problems &^= jiraFields(field)
	f.onProblems(f.problems)
}

// trimmedToken is the token as sent: a pasted one often carries a line
// break.
func (f *jiraFlow) trimmedToken() string { return strings.TrimSpace(f.token) }

// credentials are what account.listSpaces, account.add and account.update
// receive.
func (f *jiraFlow) credentials() api.Credentials {
	return api.Credentials{Password: f.trimmedToken()}
}

// setup is what the pages collected, for account.listSpaces and
// account.add.
func (f *jiraFlow) setup(site api.AccountDetectSiteResult) jira.Setup {
	s := jira.Setup{Site: site, Login: f.login, Email: f.email, OnlyMine: f.onlyMine, OfflineDays: f.offlineDays}
	for _, sp := range f.spaces {
		if f.selected[sp.ID] {
			s.Spaces = append(s.Spaces, sp)
		}
	}
	return s
}

// fail is a failed step (jira.FailureOf): back to its page with the
// banner, the client's sentence for the error when the step has none; a
// refused or missing token flags the token field.
func (f *jiraFlow) fail(step jira.Step, err error) {
	f.failClass(step, jira.Classify(err), err)
}

func (f *jiraFlow) failClass(step jira.Step, class jira.ErrorClass, err error) {
	f.log.Info("jira assistant step failed", "step", int(step), "class", int(class))
	fl := jira.FailureOf(step, class, f.deployment(), f.isEditing(), f.tr)
	f.popTo(fl.Page)
	f.onBanner(fl.Page, f.bannerOf(fl, err))
	switch {
	case fl.Page == jira.PageSite && (class == jira.ErrInvalid || class == jira.ErrServer):
		f.flag(fieldSet(fieldSite))
		f.onFocus(fieldSite)
	case fl.Page == jira.PageCredentials && (class == jira.ErrAuthFailed || class == jira.ErrAuthRequired):
		f.flag(fieldSet(fieldToken))
		f.onFocus(fieldToken)
	}
}

// bannerOf is a failure's banner: its own, else the client's sentence for
// err.
func (f *jiraFlow) bannerOf(fl jira.Failure, err error) string {
	if fl.Banner != "" || err == nil {
		return fl.Banner
	}
	return f.errorText(fl.What, err)
}

// Site.

// detect is account.detectSite for the typed address.
func (f *jiraFlow) detect() {
	ok, problem := jira.CheckSiteInput(f.siteInput, f.tr)
	if !ok {
		f.onSiteCheck(ok, problem)
		f.flag(fieldSet(fieldSite))
		f.onFocus(fieldSite)
		return
	}
	f.onBanner(jira.PageSite, "")
	params := api.AccountDetectSiteParams{URL: strings.TrimSpace(f.siteInput)}
	f.setBusy(f.texts.LookingUp)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), detectSiteTimeout)
		defer cancel()
		var res api.AccountDetectSiteResult
		err := c.Call(ctx, api.MethodAccountDetectSite, params, &res)
		post(func() {
			if f.closed || op != f.op {
				return
			}
			f.setBusy("")
			f.detected(res, err)
		})
	}()
}

func (f *jiraFlow) detected(res api.AccountDetectSiteResult, err error) {
	if err != nil {
		f.fail(jira.StepDetect, err)
		return
	}
	if res.Kind != api.AccountJira || res.SiteURL == "" {
		// Not a site this client can add: as the daemon's serverError
		// says, it is not Jira.
		f.failClass(jira.StepDetect, jira.ErrServer, nil)
		return
	}
	f.log.Info("jira site found", "deployment", string(res.Deployment))
	f.site = &res
	f.onDetected(jira.Detected(res, f.tr))
	f.onCredentialPage(f.credentialPage())
	f.push(jira.PageCredentials)
	if res.Deployment == api.JiraCloud && f.login == "" {
		f.onFocus(fieldLogin)
	} else {
		f.onFocus(fieldToken)
	}
}

// Credentials.

// credentialsProblems checks the typed credentials before a call: Jira
// Cloud needs the login (an address), every site a token.
func (f *jiraFlow) credentialsProblems() jiraFields {
	var p jiraFields
	if _, ok := ValidateEmail(f.login); f.deployment() == api.JiraCloud && !ok {
		p |= jiraFields(fieldLogin)
	}
	if f.trimmedToken() == "" {
		p |= jiraFields(fieldToken)
	}
	return p
}

// showCredentialsProblems flags what credentialsProblems found; a missing
// token alone gets the page's prompt as the banner.
func (f *jiraFlow) showCredentialsProblems(p jiraFields) {
	f.flag(p)
	if p == fieldSet(fieldToken) {
		f.onBanner(jira.PageCredentials, f.credentialPage().TokenPrompt)
	}
	if p.has(fieldLogin) {
		f.onFocus(fieldLogin)
	} else {
		f.onFocus(fieldToken)
	}
}

// loadSpaces is account.listSpaces with the typed credentials: the
// sign-in test and the spaces page's list, with the estimates for the
// offline window.
func (f *jiraFlow) loadSpaces() {
	if f.site == nil {
		f.popTo(jira.PageSite)
		return
	}
	if p := f.credentialsProblems(); p != 0 {
		f.showCredentialsProblems(p)
		return
	}
	f.onBanner(jira.PageCredentials, "")
	f.listSpaces(*f.site, false)
}

// refreshCounts asks for the estimates again, for a new offline window,
// without leaving the spaces page; the old ones are gone meanwhile.
func (f *jiraFlow) refreshCounts() {
	if f.site == nil {
		return
	}
	for i := range f.spaces {
		f.spaces[i].Issues = -1
	}
	f.onSpaces(jira.SpaceRows(f.spaces, f.tr), maps.Clone(f.selected))
	f.listSpaces(*f.site, true)
}

func (f *jiraFlow) listSpaces(site api.AccountDetectSiteResult, refresh bool) {
	s := f.setup(site)
	s.Spaces = nil
	params := api.AccountListSpacesParams{Config: s.Config(), Credentials: f.credentials(), Counts: true}
	f.setBusy(f.texts.LoadingSpaces)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), listSpacesTimeout)
		defer cancel()
		var res api.AccountListSpacesResult
		err := c.Call(ctx, api.MethodAccountListSpaces, params, &res)
		post(func() {
			if f.closed || op != f.op {
				return
			}
			f.setBusy("")
			switch {
			case err == nil:
				f.spacesLoaded(res, site, refresh)
			case refresh:
				// The page stays; only the estimates are missing.
				class := jira.Classify(err)
				f.log.Info("jira spaces recount failed", "class", int(class))
				fl := jira.FailureOf(jira.StepSpaces, class, site.Deployment, false, f.tr)
				f.onBanner(jira.PageSpaces, f.bannerOf(fl, err))
			default:
				f.fail(jira.StepSpaces, err)
			}
		})
	}()
}

func (f *jiraFlow) spacesLoaded(res api.AccountListSpacesResult, site api.AccountDetectSiteResult, refresh bool) {
	f.log.Info("jira spaces listed", "spaces", len(res.Spaces))
	if f.spacesSite != site.SiteURL {
		f.selected = map[string]bool{}
	}
	f.spacesSite = site.SiteURL
	user := res.User
	f.user = &user
	f.spaces = res.Spaces
	listed := make(map[string]bool, len(f.spaces))
	for _, s := range f.spaces {
		listed[s.ID] = true
	}
	maps.DeleteFunc(f.selected, func(id string, _ bool) bool { return !listed[id] })
	if !refresh && len(f.selected) == 0 && len(f.spaces) == 1 {
		// A single space is what the user means.
		f.selected[f.spaces[0].ID] = true
	}
	f.needsEmail = jira.NeedsEmail(site.Deployment, res.User)
	if site.Deployment != api.JiraCloud && !f.needsEmail {
		f.email = strings.TrimSpace(res.User.Email)
	}
	f.onSpaces(jira.SpaceRows(f.spaces, f.tr), maps.Clone(f.selected))
	f.onSpacesProblem(jira.SpacesProblem(len(f.selected), f.tr))
	f.onEmailField(f.needsEmail, f.email)
	if !refresh {
		f.onBanner(jira.PageSpaces, "")
		f.push(jira.PageSpaces)
	}
}

// Save.

// add is account.add from the spaces page.
func (f *jiraFlow) add() {
	if f.site == nil {
		f.popTo(jira.PageSite)
		return
	}
	if problem := jira.SpacesProblem(len(f.selected), f.tr); problem != "" {
		f.onBanner(jira.PageSpaces, problem)
		return
	}
	s := f.setup(*f.site)
	if f.needsEmail {
		address, ok := ValidateEmail(f.email)
		if !ok {
			f.flag(fieldSet(fieldEmail))
			f.onFocus(fieldEmail)
			return
		}
		s.Email = address
	}
	f.onBanner(jira.PageSpaces, "")
	f.store(s.Config(), f.texts.Adding)
}

// save is account.update of the account being edited: its configuration
// as it is, with the new token. The token is tried on the site first
// (account.listSpaces with the account's configuration), so a refused one
// never replaces the stored token.
func (f *jiraFlow) save() {
	editing := f.editing
	if editing == nil {
		return
	}
	if f.trimmedToken() == "" {
		f.showCredentialsProblems(fieldSet(fieldToken))
		return
	}
	f.onBanner(jira.PageCredentials, "")
	params := api.AccountListSpacesParams{AccountID: editing.ID, Config: editing.Config, Credentials: f.credentials()}
	f.setBusy(f.texts.Saving)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), listSpacesTimeout)
		defer cancel()
		err := c.Call(ctx, api.MethodAccountListSpaces, params, &api.AccountListSpacesResult{})
		post(func() {
			if f.closed || op != f.op {
				return
			}
			if err != nil {
				f.setBusy("")
				f.fail(jira.StepSave, err)
				return
			}
			f.store(editing.Config, f.texts.Saving)
		})
	}()
}

// store is account.add, or account.update when editing, of cfg.
func (f *jiraFlow) store(cfg api.AccountConfig, progress string) {
	creds := f.credentials()
	editing := f.editing
	f.setBusy(progress)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
		defer cancel()
		var id api.AccountID
		var err error
		if editing != nil {
			id = editing.ID
			err = c.Call(ctx, api.MethodAccountUpdate,
				api.AccountUpdateParams{AccountID: id, Config: cfg, Credentials: creds}, &api.AccountUpdateResult{})
		} else {
			var res api.AccountAddResult
			err = c.Call(ctx, api.MethodAccountAdd, api.AccountAddParams{Config: cfg, Credentials: creds}, &res)
			id = res.AccountID
		}
		post(func() {
			if f.closed || op != f.op {
				return
			}
			f.setBusy("")
			if err != nil {
				f.fail(jira.StepSave, err)
				return
			}
			f.log.Info("jira account saved", "id", id, "edit", editing != nil)
			f.onDone(id, cfg)
		})
	}()
}
