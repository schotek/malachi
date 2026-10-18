// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
)

// The eWay-CRM account assistant's flow (ewayFlow) against a fake daemon
// (fakeJiraDaemon, mainLoop and msgids of jira_flow_test.go):
// account.detectEway, account.test, account.add and account.update.
// macOS and Windows port these tests with the flow. Fictional instances
// and people only.

const (
	ewayDetectedJSON  = `{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":false,"version":"10.0"}`
	ewayMicrosoftJSON = `{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":true}`
	ewayTestOKJSON    = `{"eway":{"ok":true,"latencyMs":120}}`
	ewayAddedJSON     = `{"accountId":"acc-e1"}`
)

type ewayBanner struct {
	page crm.WizardPage
	text string
}

// ewayRecorder collects what the flow reports.
type ewayRecorder struct {
	pages    [][]crm.WizardPage
	busy     []string
	checks   []jiraCheck
	names    []string
	banners  []ewayBanner
	problems []crm.WizardField
	focus    []crm.WizardField
	done     []api.AccountID
	configs  []api.AccountConfig
}

func (r *ewayRecorder) attach(f *ewayFlow) {
	f.onPages = func(p []crm.WizardPage) { r.pages = append(r.pages, p) }
	f.onBusy = func(s string) { r.busy = append(r.busy, s) }
	f.onServiceCheck = func(ok bool, problem string) { r.checks = append(r.checks, jiraCheck{ok, problem}) }
	f.onName = func(s string) { r.names = append(r.names, s) }
	f.onBanner = func(p crm.WizardPage, text string) { r.banners = append(r.banners, ewayBanner{p, text}) }
	f.onProblem = func(field crm.WizardField) { r.problems = append(r.problems, field) }
	f.onFocus = func(field crm.WizardField) { r.focus = append(r.focus, field) }
	f.onDone = func(id api.AccountID, cfg api.AccountConfig) {
		r.done = append(r.done, id)
		r.configs = append(r.configs, cfg)
	}
}

func (r *ewayRecorder) on(pages ...crm.WizardPage) func() bool {
	return func() bool { return slices.Equal(last(r.pages), pages) }
}

func startEwayFlow(d *fakeJiraDaemon, editing *api.Account) (*ewayFlow, *ewayRecorder, mainLoop) {
	loop := make(mainLoop, 16)
	f := newEwayFlow(d, loop.post, msgids{}, errText, nil, editing)
	rec := &ewayRecorder{}
	rec.attach(f)
	f.start()
	return f, rec, loop
}

func ewayAccount() api.Account {
	return api.Account{
		ID: "acc-e1", Enabled: true,
		Config: api.AccountConfig{
			Kind: api.AccountEway, Name: "Acme", Email: "jana@acme.example",
			Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "jana",
				UserGUID: "0b9e3d2c-1a2b-4c3d-8e9f-001122334455"},
		},
		Capabilities: []api.AccountCapability{api.CapabilityCrm},
	}
}

func TestEwayFlowAdds(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectEway, ewayDetectedJSON)
	d.reply(api.MethodAccountTest, ewayTestOKJSON)
	d.reply(api.MethodAccountAdd, ewayAddedJSON)
	f, rec, loop := startEwayFlow(d, nil)
	if last(rec.checks).ok || f.title() != "Add eWay-CRM Account" || f.nextLabel(crm.PageService) != "_Next" || f.canGoBack() {
		t.Errorf("start: %+v %q", rec.checks, f.title())
	}
	f.setService(" hosting.eway-crm.com/acme/API.svc ")
	if !last(rec.checks).ok {
		t.Fatal("address refused")
	}
	f.next()
	if last(rec.busy) != "Contacting eWay-CRM" {
		t.Errorf("busy %v", rec.busy)
	}
	loop.runUntil(t, rec.on(crm.PageService, crm.PageCredentials))
	if p := lastParams[api.AccountDetectEwayParams](t, d, api.MethodAccountDetectEway); p.ServiceURL != "hosting.eway-crm.com/acme/API.svc" {
		t.Errorf("detect %+v", p)
	}
	if d.budget(api.MethodAccountDetectEway) > detectEwayTimeout {
		t.Error("detect budget")
	}
	if last(rec.names) != "acme" || f.serviceURL() != "https://hosting.eway-crm.com/acme" || last(rec.focus) != crm.FieldUser {
		t.Errorf("found: %v %q %v", rec.names, f.serviceURL(), rec.focus)
	}
	if f.nextLabel(crm.PageCredentials) != "_Add Account" || f.passwordLabel() != "Password" || f.pageTitle(crm.PageCredentials) != "Sign In" {
		t.Error("credentials page texts")
	}

	// No password: flagged, nothing asked.
	f.setCredentials("jana", "")
	f.next()
	if last(rec.problems) != crm.FieldPassword || last(rec.banners).text != "Enter the password for this account" || d.count(api.MethodAccountTest) != 0 {
		t.Errorf("no password: %v %v", rec.problems, rec.banners)
	}
	f.setName("Acme CRM")
	f.setCredentials(" jana ", "s3cret pass")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	tp := lastParams[api.AccountTestParams](t, d, api.MethodAccountTest)
	if !tp.Config.Eway.FollowThreads {
		t.Error("a new account follows saved conversations (D22)")
	}
	if tp.AccountID != "" || tp.Credentials.Password != "s3cret pass" || tp.Config.Kind != api.AccountEway ||
		tp.Config.Eway.ServiceURL != "https://hosting.eway-crm.com/acme" || tp.Config.Eway.Username != "jana" {
		t.Errorf("test %+v", tp)
	}
	ap := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd)
	want := api.AccountConfig{Kind: api.AccountEway, Name: "Acme CRM",
		Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "jana", FollowThreads: true}}
	if ap.Config.Name != want.Name || *ap.Config.Eway != *want.Eway || ap.Config.Kind != want.Kind || ap.Credentials.Password != "s3cret pass" {
		t.Errorf("add %+v %+v", ap.Config, ap.Config.Eway)
	}
	if rec.done[0] != "acc-e1" || !slices.Equal(d.callList(), []string{api.MethodAccountDetectEway, api.MethodAccountTest, api.MethodAccountAdd}) {
		t.Errorf("done %v calls %v", rec.done, d.callList())
	}
	if !slices.Contains(rec.busy, "Signing in") || !slices.Contains(rec.busy, "Adding the account") || last(rec.busy) != "" {
		t.Errorf("busy %v", rec.busy)
	}
	if d.budget(api.MethodAccountAdd) > ewaySaveTimeout || d.budget(api.MethodAccountAdd) < 50*time.Second {
		t.Errorf("add budget %v", d.budget(api.MethodAccountAdd))
	}
}

// http is refused before anything is asked; a Microsoft instance is told
// on the address page, and no password page follows.
func TestEwayFlowRefusesHTTPAndMicrosoft(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectEway, ewayMicrosoftJSON)
	f, rec, loop := startEwayFlow(d, nil)
	f.setService("http://hosting.eway-crm.com/acme")
	if c := last(rec.checks); c.ok || c.problem != "Malachi Mail connects to eWay-CRM over https only." {
		t.Errorf("http: %+v", c)
	}
	f.next()
	if d.count(api.MethodAccountDetectEway) != 0 || last(rec.problems) != crm.FieldService {
		t.Error("http was looked up")
	}
	f.setService("hosting.eway-crm.com/acme")
	f.next()
	loop.runUntil(t, func() bool {
		return len(rec.busy) > 0 && last(rec.busy) == "" && d.count(api.MethodAccountDetectEway) == 1
	})
	b := last(rec.banners)
	if b.page != crm.PageService || b.text != "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet." {
		t.Errorf("microsoft banner %+v", b)
	}
	if f.top() != crm.PageService || last(rec.problems) != crm.FieldService || f.service != nil {
		t.Errorf("stays on the address: %v", rec.pages)
	}
}

func TestEwayFlowErrors(t *testing.T) {
	d := newFakeJiraDaemon()
	d.fail(api.MethodAccountDetectEway, &api.Error{Code: api.CodeNetworkError, Message: "dial"})
	f, rec, loop := startEwayFlow(d, nil)
	f.setService("crm.example.org")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 2 && last(rec.busy) == "" })
	if b := last(rec.banners); b.page != crm.PageService || b.text != "Contacting eWay-CRM failed: networkError (1301): dial" {
		t.Errorf("network %+v", b)
	}

	// A wrong password (in the test's endpoint result) flags the password.
	d.reply(api.MethodAccountDetectEway, ewayDetectedJSON)
	d.reply(api.MethodAccountTest, `{"eway":{"ok":false,"latencyMs":0,"error":{"code":1201,"message":"eway: LogIn: rcLoginFailed"}}}`)
	f.next()
	loop.runUntil(t, rec.on(crm.PageService, crm.PageCredentials))
	f.setCredentials("jana", "wrong")
	f.next()
	loop.runUntil(t, func() bool { return last(rec.problems) == crm.FieldPassword })
	if b := last(rec.banners); b.page != crm.PageCredentials || b.text != "The server rejected the user name or password" || d.count(api.MethodAccountAdd) != 0 {
		t.Errorf("wrong password %+v", b)
	}

	// A plan without API access, at add (the daemon signs in again).
	d.reply(api.MethodAccountTest, ewayTestOKJSON)
	d.fail(api.MethodAccountAdd, &api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "plan"}})
	f.setCredentials("jana", "right")
	f.next()
	loop.runUntil(t, func() bool {
		return last(rec.banners).text == "This eWay-CRM does not allow access through its API."
	})
	if f.top() != crm.PageCredentials || len(rec.done) != 0 {
		t.Error("plan")
	}

	// Microsoft found only by account.add: back to the address page.
	d.fail(api.MethodAccountAdd, &api.Error{Code: api.CodeCrmSignInUnsupported, Data: map[string]any{"reason": "microsoft"}})
	f.next()
	loop.runUntil(t, rec.on(crm.PageService))
	if last(rec.problems) != crm.FieldService {
		t.Errorf("microsoft at add: %v", rec.problems)
	}
}

// Back drops the reply of the call under way; a changed address forgets
// the instance; Next twice while busy asks once.
func TestEwayFlowBackDropsReply(t *testing.T) {
	d := newFakeJiraDaemon()
	release := make(chan struct{})
	d.on(api.MethodAccountDetectEway, func() (string, error) { <-release; return ewayDetectedJSON, nil })
	d.reply(api.MethodAccountTest, ewayTestOKJSON)
	f, rec, loop := startEwayFlow(d, nil)
	f.setService("hosting.eway-crm.com/acme")
	f.next()
	f.next()
	waitFor(t, func() bool { return d.count(api.MethodAccountDetectEway) == 1 })
	f.close()
	close(release)
	loop.runOne(t)
	if len(rec.pages) != 1 || d.count(api.MethodAccountDetectEway) != 1 {
		t.Errorf("a late reply after close: %v", rec.pages)
	}

	d2 := newFakeJiraDaemon()
	d2.reply(api.MethodAccountDetectEway, ewayDetectedJSON)
	slow := make(chan struct{})
	d2.on(api.MethodAccountTest, func() (string, error) { <-slow; return ewayTestOKJSON, nil })
	f, rec, loop = startEwayFlow(d2, nil)
	f.setService("hosting.eway-crm.com/acme")
	f.next()
	loop.runUntil(t, rec.on(crm.PageService, crm.PageCredentials))
	f.setCredentials("jana", "pw")
	f.next()
	waitFor(t, func() bool { return d2.count(api.MethodAccountTest) == 1 })
	f.back()
	close(slow)
	loop.runOne(t)
	if d2.count(api.MethodAccountAdd) != 0 || f.top() != crm.PageService || f.busy() {
		t.Error("the test's reply after Back was used")
	}
	f.setService("crm.example.org")
	if f.service != nil {
		t.Error("a changed address keeps the instance")
	}
}

// Editing: on the sign-in page with the account's address, name and user;
// an empty password keeps the stored one (account.test with accountId),
// unless the user name changed.
func TestEwayFlowEdits(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountTest, ewayTestOKJSON)
	d.reply(api.MethodAccountUpdate, `{}`)
	acc := ewayAccount()
	f, rec, loop := startEwayFlow(d, &acc)
	if f.title() != "Edit Account" || f.top() != crm.PageCredentials || f.canGoBack() || f.nextLabel(crm.PageCredentials) != "_Save" ||
		f.passwordLabel() != "New Password (leave empty to keep)" || last(rec.names) != "Acme" || f.userName() != "jana" ||
		f.serviceURL() != "https://hosting.eway-crm.com/acme" {
		t.Fatalf("edit start %q %v", f.title(), rec.names)
	}
	f.setService("https://other.example.org")
	if f.serviceURL() != "https://hosting.eway-crm.com/acme" {
		t.Error("the address changed when editing")
	}
	f.setName("Acme Sales")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	tp := lastParams[api.AccountTestParams](t, d, api.MethodAccountTest)
	if tp.AccountID != "acc-e1" || tp.Credentials.Password != "" || tp.Config.Eway.Username != "jana" {
		t.Errorf("test %+v", tp)
	}
	up := lastParams[api.AccountUpdateParams](t, d, api.MethodAccountUpdate)
	if up.AccountID != "acc-e1" || up.Config.Name != "Acme Sales" || up.Config.Email != "jana@acme.example" ||
		up.Config.Eway.UserGUID != acc.Config.Eway.UserGUID || up.Credentials.Password != "" {
		t.Errorf("update %+v %+v", up, up.Config.Eway)
	}
	if !slices.Contains(rec.busy, "Saving the account") {
		t.Errorf("busy %v", rec.busy)
	}

	// Another user needs a password, and forgets the user GUID.
	d2 := newFakeJiraDaemon()
	d2.reply(api.MethodAccountTest, ewayTestOKJSON)
	d2.reply(api.MethodAccountUpdate, `{}`)
	f, rec, loop = startEwayFlow(d2, &acc)
	f.setCredentials("petr", "")
	f.next()
	if d2.count(api.MethodAccountTest) != 0 || last(rec.problems) != crm.FieldPassword {
		t.Error("another user without a password")
	}
	f.setCredentials("petr", "pw2")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	up = lastParams[api.AccountUpdateParams](t, d2, api.MethodAccountUpdate)
	if up.Config.Eway.Username != "petr" || up.Config.Eway.UserGUID != "" || up.Credentials.Password != "pw2" {
		t.Errorf("another user %+v", up.Config.Eway)
	}
	if acc.Config.Eway.Username != "jana" {
		t.Error("the edited account's configuration was changed in place")
	}

	// A timeout of the update: the client's sentence, on the sign-in page.
	d3 := newFakeJiraDaemon()
	d3.reply(api.MethodAccountTest, ewayTestOKJSON)
	d3.fail(api.MethodAccountUpdate, errors.New("boom"))
	f, rec, loop = startEwayFlow(d3, &acc)
	f.next()
	loop.runUntil(t, func() bool { return last(rec.banners).text == "Saving the account failed: boom" })
	if len(rec.done) != 0 || f.top() != crm.PageCredentials {
		t.Error("failed update")
	}
}

// D22: the check box "Also save later messages of saved conversations":
// on for a new account, the account's value when editing, sent with
// account.test and account.update.
func TestEwayFlowFollowThreads(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectEway, ewayDetectedJSON)
	d.reply(api.MethodAccountTest, ewayTestOKJSON)
	d.reply(api.MethodAccountAdd, ewayAddedJSON)
	f, rec, loop := startEwayFlow(d, nil)
	if !f.following() {
		t.Error("a new account is not followed")
	}
	f.setService("hosting.eway-crm.com/acme")
	f.next()
	loop.runUntil(t, rec.on(crm.PageService, crm.PageCredentials))
	f.setFollowThreads(false)
	f.setCredentials("jana", "pw")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	if ap := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd); ap.Config.Eway.FollowThreads {
		t.Error("unchecked, still sent on")
	}

	d2 := newFakeJiraDaemon()
	d2.reply(api.MethodAccountTest, ewayTestOKJSON)
	d2.reply(api.MethodAccountUpdate, `{}`)
	acc := ewayAccount()
	f, rec, loop = startEwayFlow(d2, &acc)
	if f.following() {
		t.Error("the edited account's value (off) is not shown")
	}
	f.setFollowThreads(true)
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	tp := lastParams[api.AccountTestParams](t, d2, api.MethodAccountTest)
	up := lastParams[api.AccountUpdateParams](t, d2, api.MethodAccountUpdate)
	if !tp.Config.Eway.FollowThreads || !up.Config.Eway.FollowThreads || acc.Config.Eway.FollowThreads {
		t.Errorf("update %+v, account %+v", up.Config.Eway, acc.Config.Eway)
	}
	if w := crm.WizardTexts(msgids{}); w.FollowThreads != "Also save later messages of saved conversations" ||
		w.FollowThreadsHint != "Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links." {
		t.Errorf("texts %q %q", w.FollowThreads, w.FollowThreadsHint)
	}
}
