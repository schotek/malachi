// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
)

// RPC budgets of the eWay-CRM assistant (docs/api.md §4.1).
// account.detectEway asks the instance one anonymous question (15 s);
// account.test asks it again and signs in once (the daemon allows itself
// 45 s); account.add and account.update do both again and store the
// password, so the keyring may prompt.
const (
	detectEwayTimeout = 15 * time.Second
	ewayTestTimeout   = 50 * time.Second
	ewaySaveTimeout   = 60 * time.Second
)

// ewayFlow is the flow of the assistant that adds an eWay-CRM account
// (kind eway, docs/eway.md §2.4), with the widgets replaced by callbacks;
// the macOS EWayWizardController and the Windows one port it, tests
// included. The texts and the rules of its fields and failures are
// ui/internal/crm (WizardTexts, CheckServiceInput, FailureOf).
//
// Two pages: the web service address (account.detectEway, which also says
// whether the instance signs in with Microsoft, which this version cannot
// do: the assistant says so there, before any password is asked), and the
// sign-in with the account's name, user name and password and the check
// box "Also save later messages of saved conversations" (on for a new
// account, docs/eway.md D22) (account.test,
// one sign-in, then account.add). Editing an account (integrations
// EditorEway) opens on the sign-in page with the account's address, name
// and user name; the password may stay empty to keep the stored one,
// unless the user name changed (the stored password never goes to
// another user, docs/api.md account.test). It saves through
// account.update.
//
// Every method and callback runs on the main loop: a call's reply comes
// back through post. A reply is dropped once the assistant closed or
// anything else started since (Back, another call). Nothing typed here is
// logged; the password leaves only in api.Credentials.
//
// Wire the callbacks, then call start.
type ewayFlow struct {
	// onPages: the page stack changed; the last page is the visible one.
	onPages func([]crm.WizardPage)
	// onBusy: a call started, with its progress text, or finished ("").
	onBusy func(progress string)
	// onServiceCheck: what the address field allows now, Next and the
	// text under the field ("" for none; crm.CheckServiceInput).
	onServiceCheck func(ok bool, problem string)
	// onName: the name field's text (the default name once the address
	// was looked up, while the user did not type one).
	onName func(string)
	// onBanner: a page's banner; "" hides it.
	onBanner func(page crm.WizardPage, text string)
	// onProblem: the field to flag; crm.FieldNone clears the flag.
	onProblem func(crm.WizardField)
	// onFocus: move the keyboard focus to a field.
	onFocus func(crm.WizardField)
	// onDone: the account was stored; the view closes the assistant.
	onDone func(api.AccountID, api.AccountConfig)

	call caller
	post func(func())
	tr   crm.Translator
	// errorText is the client's sentence for a failed call whose step has
	// no banner of its own (widget.RPCErrorText).
	errorText func(what string, err error) string
	log       *slog.Logger

	// editing is the account being edited; nil when adding one.
	editing *api.Account
	texts   crm.WizardStrings

	pages    []crm.WizardPage
	closed   bool
	op       int
	progress string

	// serviceInput is the address field as typed.
	serviceInput string
	// service is what account.detectEway found for serviceInput; nil
	// until it answered and again once the address changed. When editing,
	// the account's service.
	service *api.AccountDetectEwayResult
	// name, user and password as typed; nameTyped once the user typed a
	// name (the default no longer replaces it).
	name, user, password string
	nameTyped            bool
	// followThreads is the sign-in page's check box of
	// EwayConfig.FollowThreads (docs/eway.md D22): on for a new account,
	// the account's value when editing.
	followThreads bool
	problem       crm.WizardField
	started       bool
}

// newEwayFlow builds the flow; editing (nil when adding) opens on the
// sign-in page and saves with account.update.
func newEwayFlow(c caller, post func(func()), tr crm.Translator, errorText func(string, error) string,
	log *slog.Logger, editing *api.Account) *ewayFlow {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	f := &ewayFlow{
		call:      c,
		post:      post,
		tr:        tr,
		errorText: errorText,
		log:       log,
		editing:   editing,
		texts:     crm.WizardTexts(tr),
		pages:     []crm.WizardPage{crm.PageService},
		// D22: a new account follows saved conversations.
		followThreads: true,
	}
	if editing == nil {
		return f
	}
	f.pages = []crm.WizardPage{crm.PageCredentials}
	f.name, f.nameTyped = editing.Config.Name, true
	if ec := editing.Config.Eway; ec != nil {
		f.serviceInput = ec.ServiceURL
		f.service = &api.AccountDetectEwayResult{ServiceURL: ec.ServiceURL}
		f.user = ec.Username
		f.followThreads = ec.FollowThreads
	} else {
		f.followThreads = false
	}
	return f
}

// Presentation.

func (f *ewayFlow) isEditing() bool { return f.editing != nil }

// title is the dialog's title.
func (f *ewayFlow) title() string {
	if f.isEditing() {
		return f.texts.EditTitle
	}
	return f.texts.Title
}

// pageTitle is a page's title in its header.
func (f *ewayFlow) pageTitle(p crm.WizardPage) string {
	if p == crm.PageCredentials {
		return f.texts.CredentialsTitle
	}
	return f.texts.Title
}

// nextLabel is a page's button: Next on the address, Add Account on the
// sign-in, Save when editing.
func (f *ewayFlow) nextLabel(p crm.WizardPage) string {
	switch {
	case p == crm.PageService:
		return f.texts.Next
	case f.isEditing():
		return f.texts.Save
	}
	return f.texts.Add
}

// passwordLabel is the password field's label: optional when editing.
func (f *ewayFlow) passwordLabel() string {
	if f.isEditing() {
		return f.texts.NewPasswordLabel
	}
	return f.texts.PasswordLabel
}

// serviceURL is the address the sign-in page shows (read-only there).
func (f *ewayFlow) serviceURL() string {
	if f.service == nil {
		return ""
	}
	return f.service.ServiceURL
}

func (f *ewayFlow) canGoBack() bool     { return len(f.pages) > 1 }
func (f *ewayFlow) top() crm.WizardPage { return f.pages[len(f.pages)-1] }
func (f *ewayFlow) busy() bool          { return f.progress != "" }
func (f *ewayFlow) userName() string    { return f.user }
func (f *ewayFlow) accountName() string { return f.name }

// following is the check box "Also save later messages of saved
// conversations" as the page shows it.
func (f *ewayFlow) following() bool     { return f.followThreads }
func (f *ewayFlow) trimmedUser() string { return strings.TrimSpace(f.user) }
func (f *ewayFlow) userChanged() bool   { return f.editing == nil || f.trimmedUser() != f.editedUser() }
func (f *ewayFlow) credentials() api.Credentials {
	return api.Credentials{Password: f.password}
}

// editedUser is the user name of the account being edited.
func (f *ewayFlow) editedUser() string {
	if f.editing == nil || f.editing.Config.Eway == nil {
		return ""
	}
	return strings.TrimSpace(f.editing.Config.Eway.Username)
}

// Lifecycle.

// start delivers the initial state to the callbacks. Call once, after
// wiring them.
func (f *ewayFlow) start() {
	ok, problem := crm.CheckServiceInput(f.serviceInput, f.tr)
	f.onServiceCheck(ok, problem)
	f.onName(f.name)
	f.onPages(slices.Clone(f.pages))
	f.started = true
}

// close: the assistant went away; every late reply is dropped from now on.
func (f *ewayFlow) close() {
	f.closed = true
	f.progress = ""
}

// Inputs from the view.

// setService is the address field as typed. A changed address forgets
// what was found for the previous one.
func (f *ewayFlow) setService(raw string) {
	if f.isEditing() || raw == f.serviceInput {
		return
	}
	f.serviceInput = raw
	ok, problem := crm.CheckServiceInput(raw, f.tr)
	f.onServiceCheck(ok, problem)
	f.clear(crm.FieldService)
	f.onBanner(crm.PageService, "")
	f.service = nil
}

// setName is the name field as typed; once typed, the default no longer
// replaces it.
func (f *ewayFlow) setName(s string) {
	if s == f.name {
		return
	}
	f.name, f.nameTyped = s, true
}

// setCredentials is the user name and password as typed.
func (f *ewayFlow) setCredentials(user, password string) {
	if user == f.user && password == f.password {
		return
	}
	if user != f.user {
		f.clear(crm.FieldUser)
	}
	if password != f.password {
		f.clear(crm.FieldPassword)
	}
	f.user, f.password = user, password
	f.onBanner(crm.PageCredentials, "")
}

// setFollowThreads is the check box as clicked; it goes with the
// account's configuration on account.test, account.add and account.update.
func (f *ewayFlow) setFollowThreads(on bool) {
	f.followThreads = on
}

// next is the visible page's button: look the address up, or test the
// sign-in and store the account.
func (f *ewayFlow) next() {
	if f.closed || f.busy() {
		return
	}
	if f.top() == crm.PageService {
		f.detect()
		return
	}
	f.test()
}

// back is the header's Back: it pops the visible page; a call under way is
// dropped.
func (f *ewayFlow) back() {
	if !f.canGoBack() {
		return
	}
	f.op++
	f.setBusy("")
	f.pages = f.pages[:len(f.pages)-1]
	f.onPages(slices.Clone(f.pages))
}

// Navigation and fields.

func (f *ewayFlow) push(p crm.WizardPage) {
	if i := slices.Index(f.pages, p); i >= 0 {
		f.pages = f.pages[:i+1]
	} else {
		f.pages = append(f.pages, p)
	}
	f.onPages(slices.Clone(f.pages))
}

func (f *ewayFlow) popTo(p crm.WizardPage) {
	i := slices.Index(f.pages, p)
	if i < 0 || i+1 >= len(f.pages) {
		return
	}
	f.pages = f.pages[:i+1]
	f.onPages(slices.Clone(f.pages))
}

func (f *ewayFlow) setBusy(text string) {
	if f.progress == text {
		return
	}
	f.progress = text
	f.onBusy(text)
}

func (f *ewayFlow) flag(field crm.WizardField) {
	f.problem = field
	f.onProblem(field)
	if field != crm.FieldNone {
		f.onFocus(field)
	}
}

func (f *ewayFlow) clear(field crm.WizardField) {
	if f.problem != field || field == crm.FieldNone {
		return
	}
	f.problem = crm.FieldNone
	f.onProblem(crm.FieldNone)
}

// fail is a failed step (crm.FailureOf): back to its page with the banner,
// the client's sentence for the error when the step has none, the field
// flagged and focused.
func (f *ewayFlow) fail(step crm.WizardStep, err error) {
	fl := crm.FailureOf(step, err, f.isEditing(), f.tr)
	code := 0
	var e *api.Error
	if errors.As(err, &e) {
		code = int(e.Code)
	}
	f.log.Info("eway assistant step failed", "step", int(step), "code", code)
	f.popTo(fl.Page)
	banner := fl.Banner
	if banner == "" && err != nil {
		banner = f.errorText(fl.What, err)
	}
	f.onBanner(fl.Page, banner)
	f.flag(fl.Field)
}

// Steps.

// detect is account.detectEway for the typed address. http is refused
// before anything is asked.
func (f *ewayFlow) detect() {
	ok, problem := crm.CheckServiceInput(f.serviceInput, f.tr)
	if !ok {
		f.onServiceCheck(ok, problem)
		f.flag(crm.FieldService)
		return
	}
	f.onBanner(crm.PageService, "")
	params := api.AccountDetectEwayParams{ServiceURL: strings.TrimSpace(f.serviceInput)}
	f.setBusy(f.texts.Contacting)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), detectEwayTimeout)
		defer cancel()
		var res api.AccountDetectEwayResult
		err := c.Call(ctx, api.MethodAccountDetectEway, params, &res)
		post(func() {
			if f.closed || op != f.op {
				return
			}
			f.setBusy("")
			f.detected(res, err)
		})
	}()
}

func (f *ewayFlow) detected(res api.AccountDetectEwayResult, err error) {
	if err == nil && res.ServiceURL == "" {
		err = api.NewError(api.CodeServerError, "no service address")
	}
	if err == nil && res.MicrosoftSignIn {
		err = &api.Error{Code: api.CodeCrmSignInUnsupported, Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInMicrosoft}}
	}
	if err != nil {
		f.fail(crm.StepDetect, err)
		return
	}
	f.log.Info("eway instance found")
	f.service = &res
	if !f.nameTyped {
		f.name = crm.DefaultName(res.ServiceURL)
		f.onName(f.name)
	}
	f.push(crm.PageCredentials)
	if f.trimmedUser() == "" {
		f.onFocus(crm.FieldUser)
	} else {
		f.onFocus(crm.FieldPassword)
	}
}

// config is the account's configuration from the pages: the edited
// account's with the new name and user name (its user GUID forgotten when
// the user changed), or a new eway account.
func (f *ewayFlow) config() api.AccountConfig {
	name := strings.TrimSpace(f.name)
	if f.editing != nil {
		cfg := f.editing.Config
		cfg.Name = name
		e := api.EwayConfig{}
		if cfg.Eway != nil {
			e = *cfg.Eway
		}
		if f.userChanged() {
			e.UserGUID = ""
		}
		e.Username = f.trimmedUser()
		e.FollowThreads = f.followThreads
		cfg.Eway = &e
		return cfg
	}
	return api.AccountConfig{
		Kind: api.AccountEway,
		Name: name,
		Eway: &api.EwayConfig{ServiceURL: f.serviceURL(), Username: f.trimmedUser(), FollowThreads: f.followThreads},
	}
}

// test is account.test with the typed credentials (with the stored
// password when editing and it is left empty), then store.
func (f *ewayFlow) test() {
	if f.service == nil {
		f.popTo(crm.PageService)
		return
	}
	switch {
	case f.trimmedUser() == "":
		f.flag(crm.FieldUser)
		return
	case f.password == "" && f.userChanged():
		f.onBanner(crm.PageCredentials, f.texts.PasswordPrompt)
		f.flag(crm.FieldPassword)
		return
	}
	f.onBanner(crm.PageCredentials, "")
	params := api.AccountTestParams{Config: f.config(), Credentials: f.credentials()}
	if f.editing != nil {
		params.AccountID = f.editing.ID
	}
	f.setBusy(f.texts.SigningIn)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ewayTestTimeout)
		defer cancel()
		var res api.AccountTestResult
		err := c.Call(ctx, api.MethodAccountTest, params, &res)
		if err == nil {
			switch {
			case res.Eway == nil:
				err = api.NewError(api.CodeServerError, "no eway result")
			case !res.Eway.OK && res.Eway.Error != nil:
				err = res.Eway.Error
			case !res.Eway.OK:
				err = api.NewError(api.CodeServerError, "eway test failed")
			}
		}
		post(func() {
			if f.closed || op != f.op {
				return
			}
			if err != nil {
				f.setBusy("")
				f.fail(crm.StepTest, err)
				return
			}
			f.store(params.Config)
		})
	}()
}

// store is account.add, or account.update when editing, of cfg.
func (f *ewayFlow) store(cfg api.AccountConfig) {
	creds := f.credentials()
	editing := f.editing
	progress := f.texts.Adding
	if editing != nil {
		progress = f.texts.Saving
	}
	f.setBusy(progress)
	f.op++
	op := f.op
	c, post := f.call, f.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ewaySaveTimeout)
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
				f.fail(crm.StepSave, err)
				return
			}
			f.log.Info("eway account saved", "id", id, "edit", editing != nil)
			f.onDone(id, cfg)
		})
	}()
}
