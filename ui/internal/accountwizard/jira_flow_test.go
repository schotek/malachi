// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The Jira account assistant's flow (jiraFlow) against a fake daemon:
// account.detectSite, account.listSpaces, account.add and account.update.
// The port of macos/Tests/MalachiCoreTests/JiraWizardControllerTests.swift.
// Fictional sites and people only.

const (
	jiraCloudID       = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455"
	jiraCloudSiteJSON = `{"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0b9e3d2c-1a2b-4c3d-8e9f-001122334455","title":"Acme"}`
	jiraDCSiteJSON    = `{"kind":"jira","siteUrl":"https://jira.acme.example/jira","deployment":"datacenter","title":"Acme Jira","version":"9.12.4"}`
	jiraSpacesJSON    = `{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":[` +
		`{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120},` +
		`{"id":"10002","key":"WEB","name":"Website","issues":1},` +
		`{"id":"10003","key":"MOB","name":"Mobile","issues":-1}],` +
		`"statuses":[{"id":"3","name":"In Progress","category":"inProgress"}]}`
	jiraRecountedJSON = `{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":[` +
		`{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":300},` +
		`{"id":"10002","key":"WEB","name":"Website","issues":4},` +
		`{"id":"10003","key":"MOB","name":"Mobile","issues":0}]}`
	// A Data Center user whose address the site does not reveal, one space.
	jiraDCSpacesJSON = `{"user":{"name":"Jana Dvořáková"},"spaces":[{"id":"20001","key":"OPS","name":"Operations","issues":7}]}`
)

// jiraSpaceRows are the rows of jiraSpacesJSON on the spaces page.
var jiraSpaceRows = []jira.SpaceRow{
	{ID: "10001", Title: "ITSD – IT Service Desk", Count: "about 120 issues", ServiceDesk: true},
	{ID: "10002", Title: "WEB – Website", Count: "about 1 issue"},
	{ID: "10003", Title: "MOB – Mobile", Count: ""},
}

// msgids is the jira.Translator of the tests: every text untranslated.
type msgids struct{}

func (msgids) T(msgid string) string { return msgid }
func (msgids) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (msgids) C(_, msgid string) string { return msgid }

// errText stands for widget.RPCErrorText, the client's sentence for a
// failed call.
func errText(what string, err error) string { return what + " failed: " + err.Error() }

// fakeJiraDaemon answers the assistant's calls with the JSON its handlers
// return, decoded as the client decodes the daemon's replies, and records
// what each method was sent and the time it was allowed.
type fakeJiraDaemon struct {
	mu       sync.Mutex
	handlers map[string]func() (string, error)
	calls    []string
	params   map[string][]json.RawMessage
	budgets  map[string]time.Duration
}

func newFakeJiraDaemon() *fakeJiraDaemon {
	return &fakeJiraDaemon{
		handlers: map[string]func() (string, error){},
		params:   map[string][]json.RawMessage{},
		budgets:  map[string]time.Duration{},
	}
}

func (d *fakeJiraDaemon) on(method string, h func() (string, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[method] = h
}

func (d *fakeJiraDaemon) reply(method, result string) {
	d.on(method, func() (string, error) { return result, nil })
}

func (d *fakeJiraDaemon) fail(method string, err error) {
	d.on(method, func() (string, error) { return "", err })
}

func (d *fakeJiraDaemon) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.calls = append(d.calls, method)
	d.params[method] = append(d.params[method], raw)
	if deadline, ok := ctx.Deadline(); ok {
		d.budgets[method] = time.Until(deadline)
	}
	h := d.handlers[method]
	d.mu.Unlock()
	if h == nil {
		return api.NewError(api.CodeMethodNotFound, "%s", method)
	}
	out, err := h()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), result)
}

func (d *fakeJiraDaemon) callList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func (d *fakeJiraDaemon) count(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.params[method])
}

func (d *fakeJiraDaemon) lastRaw(t *testing.T, method string) json.RawMessage {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	sent := d.params[method]
	if len(sent) == 0 {
		t.Fatalf("%s was never called", method)
	}
	return sent[len(sent)-1]
}

func (d *fakeJiraDaemon) budget(method string) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.budgets[method]
}

// lastParams decodes the parameters of the last call of method.
func lastParams[T any](t *testing.T, d *fakeJiraDaemon, method string) T {
	t.Helper()
	var p T
	if err := json.Unmarshal(d.lastRaw(t, method), &p); err != nil {
		t.Fatalf("%s params: %v", method, err)
	}
	return p
}

// mainLoop stands in for the GTK main loop: the flow posts every reply to
// it and the test runs them on its own goroutine, as glib.IdleAdd would.
type mainLoop chan func()

func (l mainLoop) post(f func()) { l <- f }

// runUntil runs posted callbacks until cond holds.
func (l mainLoop) runUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case f := <-l:
			f()
		case <-deadline:
			t.Fatal("timed out")
		}
	}
}

// runOne runs the next posted callback.
func (l mainLoop) runOne(t *testing.T) {
	t.Helper()
	select {
	case f := <-l:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a reply")
	}
}

// waitFor polls cond, which the flow's goroutines change.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

type jiraBanner struct {
	page jira.Page
	text string
}

type jiraCheck struct {
	ok      bool
	problem string
}

type jiraEmailField struct {
	shown bool
	email string
}

// jiraRecorder collects what the flow reports.
type jiraRecorder struct {
	pages           [][]jira.Page
	busy            []string
	checks          []jiraCheck
	detected        []string
	credentialPages []jira.CredentialPage
	spaces          [][]jira.SpaceRow
	selected        []map[string]bool
	spacesProblems  []string
	emailFields     []jiraEmailField
	banners         []jiraBanner
	problems        []jiraFields
	focus           []jiraField
	opened          []string
	done            []api.AccountID
	doneConfigs     []api.AccountConfig
}

func (r *jiraRecorder) attach(f *jiraFlow) {
	f.onPages = func(p []jira.Page) { r.pages = append(r.pages, p) }
	f.onBusy = func(s string) { r.busy = append(r.busy, s) }
	f.onSiteCheck = func(ok bool, problem string) { r.checks = append(r.checks, jiraCheck{ok, problem}) }
	f.onDetected = func(s string) { r.detected = append(r.detected, s) }
	f.onCredentialPage = func(p jira.CredentialPage) { r.credentialPages = append(r.credentialPages, p) }
	f.onSpaces = func(rows []jira.SpaceRow, selected map[string]bool) {
		r.spaces = append(r.spaces, rows)
		r.selected = append(r.selected, selected)
	}
	f.onSpacesProblem = func(s string) { r.spacesProblems = append(r.spacesProblems, s) }
	f.onEmailField = func(shown bool, email string) { r.emailFields = append(r.emailFields, jiraEmailField{shown, email}) }
	f.onBanner = func(p jira.Page, text string) { r.banners = append(r.banners, jiraBanner{p, text}) }
	f.onProblems = func(s jiraFields) { r.problems = append(r.problems, s) }
	f.onFocus = func(field jiraField) { r.focus = append(r.focus, field) }
	f.onOpenURL = func(url string) { r.opened = append(r.opened, url) }
	f.onDone = func(id api.AccountID, cfg api.AccountConfig) {
		r.done = append(r.done, id)
		r.doneConfigs = append(r.doneConfigs, cfg)
	}
}

func (r *jiraRecorder) lastPages() []jira.Page { return last(r.pages) }

func (r *jiraRecorder) on(pages ...jira.Page) func() bool {
	return func() bool { return slices.Equal(r.lastPages(), pages) }
}

func last[T any](s []T) T {
	var zero T
	if len(s) == 0 {
		return zero
	}
	return s[len(s)-1]
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// startJiraFlow is a started assistant over d; editing (nil when adding)
// and a reason (0 for none) as NewJiraEdit passes them.
func startJiraFlow(d *fakeJiraDaemon, editing *api.Account, reason api.ErrorCode) (*jiraFlow, *jiraRecorder, mainLoop) {
	loop := make(mainLoop, 16)
	f := newJiraFlow(d, loop.post, msgids{}, errText, nil, editing)
	if reason != 0 {
		f.requestToken(reason)
	}
	rec := &jiraRecorder{}
	rec.attach(f)
	f.start()
	return f, rec, loop
}

// jiraCloudAccount is a Jira Cloud account as account.list returns it.
func jiraCloudAccount() api.Account {
	return api.Account{
		ID: "acc-j1",
		Config: api.AccountConfig{
			Name: "Acme", Email: "jana@acme.example", Kind: api.AccountJira,
			Jira: &api.JiraConfig{
				SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, CloudID: jiraCloudID, Login: "jana@acme.example",
				Spaces:      []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB"}},
				OfflineDays: 90, OnlyMine: true, HideEvents: true, DisabledFolders: []api.VirtualFolder{api.VirtualWatching},
				BotNames: []string{"Issue Sync"},
			},
		},
		Enabled:      true,
		State:        api.SyncState{AccountID: "acc-j1", Status: api.SyncAuthRequired},
		Capabilities: []api.AccountCapability{},
	}
}

// reachCredentials finds a Jira Cloud site and shows the credentials page.
func reachCredentials(t *testing.T, f *jiraFlow, rec *jiraRecorder, loop mainLoop) {
	t.Helper()
	f.setSite("acme.atlassian.net")
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials))
}

// reachSpaces reaches the spaces page with valid Jira Cloud credentials.
func reachSpaces(t *testing.T, f *jiraFlow, rec *jiraRecorder, loop mainLoop) {
	t.Helper()
	reachCredentials(t, f, rec, loop)
	f.setCredentials("jana@acme.example", "tok-123")
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials, jira.PageSpaces))
}

func sortedKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

// checkBudget: the call was allowed its budget (docs/api.md §4.1), give or
// take the test's own time.
func checkBudget(t *testing.T, d *fakeJiraDaemon, method string, want time.Duration) {
	t.Helper()
	if got := d.budget(method); got > want || got < want-5*time.Second {
		t.Errorf("%s allowed %v, want %v", method, got, want)
	}
}

func TestJiraFlowAddsACloudAccount(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	d.reply(api.MethodAccountListSpaces, jiraSpacesJSON)
	d.reply(api.MethodAccountAdd, `{"accountId":"acc-7"}`)
	f, rec, loop := startJiraFlow(d, nil, 0)
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) {
		t.Errorf("pages = %v", rec.pages)
	}
	if !reflect.DeepEqual(rec.checks, []jiraCheck{{false, ""}}) {
		t.Errorf("checks = %v", rec.checks)
	}
	if f.isEditing() || f.canGoBack() || !f.loginEditable() {
		t.Error("a new assistant: not editing, no Back, the login editable")
	}
	if f.title() != "Add Jira Account" || f.pageTitle(jira.PageSite) != "Jira Site" ||
		f.pageTitle(jira.PageCredentials) != "Sign In" || f.pageTitle(jira.PageSpaces) != "Spaces" {
		t.Errorf("titles: %q %q %q %q", f.title(), f.pageTitle(jira.PageSite), f.pageTitle(jira.PageCredentials), f.pageTitle(jira.PageSpaces))
	}
	// GTK keeps the mnemonics (macOS strips them).
	if f.nextLabel(jira.PageSite) != "_Next" || f.nextLabel(jira.PageCredentials) != "_Next" ||
		f.nextLabel(jira.PageSpaces) != "_Add Account" {
		t.Errorf("buttons: %q %q %q", f.nextLabel(jira.PageSite), f.nextLabel(jira.PageCredentials), f.nextLabel(jira.PageSpaces))
	}
	if !slices.Equal(f.offlineLabels(), []string{"1 week", "1 month", "3 months", "1 year"}) {
		t.Errorf("offline labels = %q", f.offlineLabels())
	}
	if f.offlineIndex() != 1 {
		t.Errorf("offline index = %d, want 30 days by default", f.offlineIndex())
	}

	// Nothing typed: nothing asked.
	f.next()
	if !reflect.DeepEqual(rec.problems, []jiraFields{fieldSet(fieldSite)}) || len(rec.busy) != 0 {
		t.Errorf("empty site: problems %v, busy %q", rec.problems, rec.busy)
	}

	f.setSite("  acme.atlassian.net ")
	if last(rec.checks) != (jiraCheck{true, ""}) || last(rec.problems) != 0 {
		t.Errorf("typed site: check %v, problems %v", last(rec.checks), last(rec.problems))
	}
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials))
	if !slices.Equal(rec.busy, []string{"Looking up the Jira site", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	if !slices.Equal(rec.detected, []string{"Found Acme"}) {
		t.Errorf("detected = %q", rec.detected)
	}
	if last(rec.credentialPages) != jira.CredentialFields(api.JiraCloud, msgids{}) {
		t.Errorf("credential page = %+v", last(rec.credentialPages))
	}
	if last(rec.focus) != fieldLogin || !f.canGoBack() {
		t.Errorf("focus %v, Back %v", last(rec.focus), f.canGoBack())
	}
	if detect := lastParams[api.AccountDetectSiteParams](t, d, api.MethodAccountDetectSite); detect.URL != "acme.atlassian.net" {
		t.Errorf("detectSite url = %q, want it trimmed", detect.URL)
	}
	checkBudget(t, d, api.MethodAccountDetectSite, 15*time.Second)

	// Missing credentials are flagged, nothing is asked.
	f.next()
	if last(rec.problems) != fieldSet(fieldLogin, fieldToken) || last(rec.focus) != fieldLogin {
		t.Errorf("no credentials: problems %v, focus %v", last(rec.problems), last(rec.focus))
	}
	f.setCredentials("jana@acme.example", "tok")
	if last(rec.problems) != 0 {
		t.Errorf("typing clears the flags: %v", last(rec.problems))
	}
	f.setCredentials("jana@acme.example", " tok-123\n")
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials, jira.PageSpaces))
	if !slices.Equal(rec.busy[len(rec.busy)-2:], []string{"Loading the spaces", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	listed := lastParams[api.AccountListSpacesParams](t, d, api.MethodAccountListSpaces)
	if !listed.Counts || listed.AccountID != "" {
		t.Errorf("listSpaces counts %v, accountId %q", listed.Counts, listed.AccountID)
	}
	if listed.Credentials.Password != "tok-123" {
		t.Errorf("token = %q, want a pasted line break trimmed", listed.Credentials.Password)
	}
	lc := listed.Config
	if lc.Kind != api.AccountJira || lc.Email != "jana@acme.example" || lc.Name != "Acme" || lc.Jira == nil ||
		lc.Jira.Login != "jana@acme.example" || lc.Jira.CloudID != jiraCloudID || lc.Jira.Spaces == nil ||
		len(lc.Jira.Spaces) != 0 || lc.Jira.OfflineDays != 30 {
		t.Errorf("listSpaces config = %+v, jira %+v", lc, lc.Jira)
	}
	checkBudget(t, d, api.MethodAccountListSpaces, 45*time.Second)
	if !reflect.DeepEqual(rec.spaces, [][]jira.SpaceRow{jiraSpaceRows}) {
		t.Errorf("spaces = %+v", rec.spaces)
	}
	if !reflect.DeepEqual(rec.selected, []map[string]bool{{}}) {
		t.Errorf("selected = %v", rec.selected)
	}
	if last(rec.spacesProblems) != "Select at least one space" || last(rec.emailFields) != (jiraEmailField{false, ""}) {
		t.Errorf("spaces problem %q, email field %v", last(rec.spacesProblems), last(rec.emailFields))
	}
	if f.user == nil || *f.user != (api.SiteUser{Name: "Jana Dvořáková", Email: "jana@acme.example"}) {
		t.Errorf("user = %+v", f.user)
	}

	// Add without a space: the page says why.
	f.next()
	if last(rec.banners) != (jiraBanner{jira.PageSpaces, "Select at least one space"}) || d.count(api.MethodAccountAdd) != 0 {
		t.Errorf("no space: banner %v, add called %d times", last(rec.banners), d.count(api.MethodAccountAdd))
	}

	f.setSpace("10002", true)
	f.setSpace("10001", true)
	f.setSpace("99999", true)
	if !maps.Equal(f.selected, set("10001", "10002")) {
		t.Errorf("selected = %v, want an unknown space ignored", f.selected)
	}
	if last(rec.spacesProblems) != "" {
		t.Errorf("spaces problem = %q", last(rec.spacesProblems))
	}
	f.setOnlyMine(true)
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) > 0 })
	if !slices.Equal(rec.busy[len(rec.busy)-2:], []string{"Adding the account", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	want := api.AccountAddParams{
		Config: api.AccountConfig{
			Name: "Acme", Email: "jana@acme.example", Kind: api.AccountJira,
			Jira: &api.JiraConfig{
				SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, CloudID: jiraCloudID, Login: "jana@acme.example",
				Spaces:      []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB", Name: "Website"}},
				OfflineDays: 30, OnlyMine: true,
			},
		},
		Credentials: api.Credentials{Password: "tok-123"},
	}
	if sent := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd); !reflect.DeepEqual(sent, want) {
		t.Errorf("account.add = %+v, jira %+v", sent, sent.Config.Jira)
	}
	// Exactly these members on the wire: nothing a mail account has, no
	// empty Jira list.
	var sent struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(d.lastRaw(t, api.MethodAccountAdd), &sent); err != nil {
		t.Fatal(err)
	}
	if keys := sortedKeys(t, sent.Config); !slices.Equal(keys, []string{"email", "jira", "kind", "name"}) {
		t.Errorf("config members = %q", keys)
	}
	var config struct {
		Jira json.RawMessage `json:"jira"`
	}
	if err := json.Unmarshal(sent.Config, &config); err != nil {
		t.Fatal(err)
	}
	if keys := sortedKeys(t, config.Jira); !slices.Equal(keys, []string{"cloudId", "deployment", "login", "offlineDays", "onlyMine", "siteUrl", "spaces"}) {
		t.Errorf("jira members = %q", keys)
	}
	checkBudget(t, d, api.MethodAccountAdd, 30*time.Second)
	if !slices.Equal(rec.done, []api.AccountID{"acc-7"}) || !reflect.DeepEqual(rec.doneConfigs, []api.AccountConfig{want.Config}) {
		t.Errorf("done = %v, %+v", rec.done, rec.doneConfigs)
	}
	if calls := d.callList(); !slices.Equal(calls, []string{api.MethodAccountDetectSite, api.MethodAccountListSpaces, api.MethodAccountAdd}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestJiraFlowAnAddressThatIsNotOneIsRefusedWithoutACall(t *testing.T) {
	d := newFakeJiraDaemon()
	f, rec, _ := startJiraFlow(d, nil, 0)
	f.setSite("acme atlassian")
	if last(rec.checks) != (jiraCheck{false, "This is not a web address"}) {
		t.Errorf("check = %v", last(rec.checks))
	}
	f.next()
	if last(rec.problems) != fieldSet(fieldSite) || last(rec.focus) != fieldSite || len(rec.busy) != 0 {
		t.Errorf("problems %v, focus %v, busy %q", last(rec.problems), last(rec.focus), rec.busy)
	}
	if calls := d.callList(); len(calls) != 0 {
		t.Errorf("calls = %q", calls)
	}
}

func TestJiraFlowAFailedLookupShowsTheBanner(t *testing.T) {
	d := newFakeJiraDaemon()
	d.fail(api.MethodAccountDetectSite, api.NewError(api.CodeServerError, "not jira"))
	f, rec, loop := startJiraFlow(d, nil, 0)
	f.setSite("example.org")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) {
		t.Errorf("pages = %v", rec.pages)
	}
	if last(rec.banners) != (jiraBanner{jira.PageSite, "This address is not a Jira site"}) {
		t.Errorf("banner = %v", last(rec.banners))
	}
	if last(rec.problems) != fieldSet(fieldSite) || last(rec.focus) != fieldSite || len(rec.detected) != 0 {
		t.Errorf("problems %v, focus %v, detected %q", last(rec.problems), last(rec.focus), rec.detected)
	}

	// A network failure: the client's sentence for the step.
	network := api.NewError(api.CodeNetworkError, "no route")
	d.fail(api.MethodAccountDetectSite, network)
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 4 })
	if want := (jiraBanner{jira.PageSite, errText("Looking up the Jira site", network)}); last(rec.banners) != want {
		t.Errorf("banner = %v, want %v", last(rec.banners), want)
	}
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) {
		t.Errorf("pages = %v", rec.pages)
	}

	// Typing clears the banner and the flag.
	f.setSite("jira.example.org")
	if last(rec.banners) != (jiraBanner{jira.PageSite, ""}) || last(rec.problems) != 0 {
		t.Errorf("typing: banner %v, problems %v", last(rec.banners), last(rec.problems))
	}
}

func TestJiraFlowASiteOfAnotherKindIsNotJira(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, `{"kind":"tracker","siteUrl":"https://tracker.acme.example","deployment":"cloud"}`)
	f, rec, loop := startJiraFlow(d, nil, 0)
	f.setSite("tracker.acme.example")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) {
		t.Errorf("pages = %v", rec.pages)
	}
	if last(rec.banners) != (jiraBanner{jira.PageSite, "This address is not a Jira site"}) {
		t.Errorf("banner = %v", last(rec.banners))
	}
	if last(rec.problems) != fieldSet(fieldSite) || f.site != nil {
		t.Errorf("problems %v, site %+v", last(rec.problems), f.site)
	}
}

func TestJiraFlowAChangedAddressForgetsTheSite(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	f, rec, loop := startJiraFlow(d, nil, 0)
	reachCredentials(t, f, rec, loop)
	if f.site == nil || f.site.SiteURL != "https://acme.atlassian.net" {
		t.Fatalf("site = %+v", f.site)
	}
	f.back()
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite}) {
		t.Errorf("pages after Back = %v", rec.lastPages())
	}
	f.setSite("acme.atlassian.net/")
	if f.site != nil || !slices.Equal(rec.detected, []string{"Found Acme", ""}) {
		t.Errorf("site %+v, detected %q", f.site, rec.detected)
	}
}

func TestJiraFlowARefusedTokenGoesBackToTheCredentials(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	d.fail(api.MethodAccountListSpaces, api.NewError(api.CodeAuthFailed, "401"))
	f, rec, loop := startJiraFlow(d, nil, 0)
	reachCredentials(t, f, rec, loop)
	f.setCredentials("jana@acme.example", "wrong")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 4 })
	rejected := jiraBanner{jira.PageCredentials, "The Jira site rejected the token"}
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite, jira.PageCredentials}) || last(rec.banners) != rejected {
		t.Errorf("pages %v, banner %v", rec.lastPages(), last(rec.banners))
	}
	if last(rec.problems) != fieldSet(fieldToken) || last(rec.focus) != fieldToken || len(rec.spaces) != 0 {
		t.Errorf("problems %v, focus %v, spaces %v", last(rec.problems), last(rec.focus), rec.spaces)
	}

	// The site lists the spaces for the next token, but refuses the add:
	// back to the credentials again.
	d.reply(api.MethodAccountListSpaces, jiraSpacesJSON)
	d.fail(api.MethodAccountAdd, api.NewError(api.CodeAuthFailed, "401"))
	f.setCredentials("jana@acme.example", "tok-123")
	if last(rec.banners) != (jiraBanner{jira.PageCredentials, ""}) || last(rec.problems) != 0 {
		t.Errorf("typing: banner %v, problems %v", last(rec.banners), last(rec.problems))
	}
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials, jira.PageSpaces))
	f.setSpace("10001", true)
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials))
	if last(rec.banners) != rejected || last(rec.problems) != fieldSet(fieldToken) || len(rec.done) != 0 {
		t.Errorf("refused add: banner %v, problems %v, done %v", last(rec.banners), last(rec.problems), rec.done)
	}
}

func TestJiraFlowAReplyAfterBackIsDropped(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	release := make(chan struct{})
	d.on(api.MethodAccountListSpaces, func() (string, error) {
		<-release
		return jiraSpacesJSON, nil
	})
	f, rec, loop := startJiraFlow(d, nil, 0)
	reachCredentials(t, f, rec, loop)
	f.setCredentials("jana@acme.example", "tok-123")
	f.next()
	if !f.busy() {
		t.Error("not busy while the spaces load")
	}
	waitFor(t, func() bool { return d.count(api.MethodAccountListSpaces) == 1 })
	f.back()
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite}) || f.busy() {
		t.Errorf("after Back: pages %v, busy %v", rec.lastPages(), f.busy())
	}
	if !slices.Equal(rec.busy, []string{"Looking up the Jira site", "", "Loading the spaces", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	close(release)
	loop.runOne(t)
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite}) {
		t.Errorf("pages = %v: the late spaces pushed their page", rec.lastPages())
	}
	if len(rec.spaces) != 0 || len(f.spaces) != 0 || len(rec.busy) != 4 {
		t.Errorf("late reply: spaces %v / %v, busy %q", rec.spaces, f.spaces, rec.busy)
	}
}

func TestJiraFlowCloseDropsLateReplies(t *testing.T) {
	d := newFakeJiraDaemon()
	release := make(chan struct{})
	d.on(api.MethodAccountDetectSite, func() (string, error) {
		<-release
		return jiraCloudSiteJSON, nil
	})
	f, rec, loop := startJiraFlow(d, nil, 0)
	f.setSite("acme.atlassian.net")
	f.next()
	waitFor(t, func() bool { return d.count(api.MethodAccountDetectSite) == 1 })
	f.close()
	if !f.closed {
		t.Error("not closed")
	}
	close(release)
	loop.runOne(t)
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) || len(rec.detected) != 0 || f.site != nil {
		t.Errorf("late reply: pages %v, detected %q, site %+v", rec.pages, rec.detected, f.site)
	}
	// Nothing starts once closed.
	f.next()
	if calls := d.callList(); !slices.Equal(calls, []string{api.MethodAccountDetectSite}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestJiraFlowAConflictStaysOnTheSpaces(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	d.reply(api.MethodAccountListSpaces, jiraSpacesJSON)
	d.fail(api.MethodAccountAdd, api.NewError(api.CodeConflict, "exists"))
	f, rec, loop := startJiraFlow(d, nil, 0)
	reachSpaces(t, f, rec, loop)
	f.setSpace("10003", true)
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 6 && last(rec.busy) == "" })
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite, jira.PageCredentials, jira.PageSpaces}) {
		t.Errorf("pages = %v", rec.lastPages())
	}
	if last(rec.banners) != (jiraBanner{jira.PageSpaces, "An account for this Jira site already exists"}) || len(rec.done) != 0 {
		t.Errorf("banner %v, done %v", last(rec.banners), rec.done)
	}
}

func TestJiraFlowANewOfflineWindowCountsAgain(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraCloudSiteJSON)
	d.on(api.MethodAccountListSpaces, func() (string, error) {
		if d.count(api.MethodAccountListSpaces) == 1 {
			return jiraSpacesJSON, nil
		}
		return jiraRecountedJSON, nil
	})
	d.reply(api.MethodAccountAdd, `{"accountId":"acc-8"}`)
	f, rec, loop := startJiraFlow(d, nil, 0)
	reachSpaces(t, f, rec, loop)
	f.setSpace("10002", true)
	f.setOfflineIndex(9)
	f.setOfflineIndex(1)
	if len(rec.spaces) != 1 {
		t.Errorf("spaces shown %d times: no change, no new count", len(rec.spaces))
	}

	counts := func(rows []jira.SpaceRow) []string {
		var c []string
		for _, r := range rows {
			c = append(c, r.Count)
		}
		return c
	}
	f.setOfflineIndex(2)
	if f.offlineDays != 90 {
		t.Errorf("offline days = %d", f.offlineDays)
	}
	if got := counts(last(rec.spaces)); !slices.Equal(got, []string{"", "", ""}) {
		t.Errorf("counts = %q, want the old estimates gone meanwhile", got)
	}
	if last(rec.busy) != "Loading the spaces" {
		t.Errorf("busy = %q", rec.busy)
	}
	loop.runUntil(t, func() bool { return !f.busy() })
	if !slices.Equal(rec.lastPages(), []jira.Page{jira.PageSite, jira.PageCredentials, jira.PageSpaces}) {
		t.Errorf("pages = %v", rec.lastPages())
	}
	if got := counts(last(rec.spaces)); !slices.Equal(got, []string{"about 300 issues", "about 4 issues", "about 0 issues"}) {
		t.Errorf("counts = %q", got)
	}
	if !maps.Equal(last(rec.selected), set("10002")) {
		t.Errorf("selected = %v, want the choice kept", last(rec.selected))
	}
	recount := lastParams[api.AccountListSpacesParams](t, d, api.MethodAccountListSpaces)
	if recount.Config.Jira == nil || recount.Config.Jira.OfflineDays != 90 || !recount.Counts {
		t.Errorf("recount = %+v", recount)
	}

	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) > 0 })
	sent := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd).Config.Jira
	if sent.OfflineDays != 90 || !reflect.DeepEqual(sent.Spaces, []api.SpaceRef{{ID: "10002", Key: "WEB", Name: "Website"}}) || sent.OnlyMine {
		t.Errorf("account.add jira = %+v", sent)
	}
}

func TestJiraFlowADataCenterSiteAsksForTheHiddenAddress(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountDetectSite, jiraDCSiteJSON)
	d.reply(api.MethodAccountListSpaces, jiraDCSpacesJSON)
	d.reply(api.MethodAccountAdd, `{"accountId":"acc-9"}`)
	f, rec, loop := startJiraFlow(d, nil, 0)
	f.setSite("https://jira.acme.example/jira")
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials))
	if !slices.Equal(rec.detected, []string{"Found Acme Jira, version 9.12.4"}) {
		t.Errorf("detected = %q", rec.detected)
	}
	if last(rec.credentialPages) != jira.CredentialFields(api.JiraDataCenter, msgids{}) || last(rec.focus) != fieldToken {
		t.Errorf("credential page %+v, focus %v", last(rec.credentialPages), last(rec.focus))
	}

	// No token: the page asks for one.
	f.next()
	if last(rec.problems) != fieldSet(fieldToken) ||
		last(rec.banners) != (jiraBanner{jira.PageCredentials, "Enter the personal access token for this account"}) {
		t.Errorf("no token: problems %v, banner %v", last(rec.problems), last(rec.banners))
	}
	// Data Center has no token page to open.
	f.openTokenHelp()
	if len(rec.opened) != 0 {
		t.Errorf("opened %q", rec.opened)
	}

	f.setCredentials("", "pat-1")
	f.next()
	loop.runUntil(t, rec.on(jira.PageSite, jira.PageCredentials, jira.PageSpaces))
	listed := lastParams[api.AccountListSpacesParams](t, d, api.MethodAccountListSpaces)
	if j := listed.Config.Jira; j == nil || j.Login != "" || j.CloudID != "" || j.Deployment != api.JiraDataCenter ||
		listed.Credentials.Password != "pat-1" {
		t.Errorf("listSpaces = %+v, jira %+v", listed, listed.Config.Jira)
	}
	if last(rec.emailFields) != (jiraEmailField{true, ""}) {
		t.Errorf("email field = %v", last(rec.emailFields))
	}
	if !maps.Equal(last(rec.selected), set("20001")) || last(rec.spacesProblems) != "" {
		t.Errorf("selected %v, want a single space chosen; problem %q", last(rec.selected), last(rec.spacesProblems))
	}

	// The address is needed and must be one.
	f.next()
	if last(rec.problems) != fieldSet(fieldEmail) || last(rec.focus) != fieldEmail {
		t.Errorf("no address: problems %v, focus %v", last(rec.problems), last(rec.focus))
	}
	f.setEmail("jana")
	f.next()
	if last(rec.problems) != fieldSet(fieldEmail) || d.count(api.MethodAccountAdd) != 0 {
		t.Errorf("not an address: problems %v, add called %d times", last(rec.problems), d.count(api.MethodAccountAdd))
	}
	f.setEmail(" jana@acme.example ")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) > 0 })
	sent := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd)
	want := api.AccountConfig{
		Name: "Acme Jira", Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://jira.acme.example/jira", Deployment: api.JiraDataCenter,
			Spaces: []api.SpaceRef{{ID: "20001", Key: "OPS", Name: "Operations"}}, OfflineDays: 30,
		},
	}
	if !reflect.DeepEqual(sent.Config, want) || sent.Credentials != (api.Credentials{Password: "pat-1"}) {
		t.Errorf("account.add = %+v, jira %+v", sent, sent.Config.Jira)
	}
}

func TestJiraFlowEditingReplacesTheTokenOnly(t *testing.T) {
	d := newFakeJiraDaemon()
	d.reply(api.MethodAccountListSpaces, `{"user":{"name":"Jana Dvořáková"},"spaces":[],"statuses":[]}`)
	d.reply(api.MethodAccountUpdate, `{}`)
	account := jiraCloudAccount()
	f, rec, loop := startJiraFlow(d, &account, 0)
	if !f.isEditing() || f.canGoBack() || f.loginEditable() {
		t.Error("editing: no Back, the login fixed")
	}
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageCredentials}}) {
		t.Errorf("pages = %v", rec.pages)
	}
	if f.title() != "Edit Account" || f.nextLabel(jira.PageCredentials) != "_Save" || f.login != "jana@acme.example" {
		t.Errorf("title %q, button %q, login %q", f.title(), f.nextLabel(jira.PageCredentials), f.login)
	}
	if !reflect.DeepEqual(rec.credentialPages, []jira.CredentialPage{jira.CredentialFields(api.JiraCloud, msgids{})}) {
		t.Errorf("credential pages = %+v", rec.credentialPages)
	}
	if len(rec.banners) != 0 {
		t.Errorf("banners = %v: no reason, no banner", rec.banners)
	}
	if f.offlineDays != 90 || !f.onlyMine || f.siteInput != "https://acme.atlassian.net" {
		t.Errorf("from the account: offline %d, only mine %v, site %q", f.offlineDays, f.onlyMine, f.siteInput)
	}

	// The token is required; the login cannot change here.
	f.next()
	if last(rec.banners) != (jiraBanner{jira.PageCredentials, "Enter the API token for this account"}) ||
		last(rec.problems) != fieldSet(fieldToken) {
		t.Errorf("no token: banner %v, problems %v", last(rec.banners), last(rec.problems))
	}
	f.setCredentials("other@acme.example", " tok-new ")
	if f.login != "jana@acme.example" {
		t.Errorf("login = %q", f.login)
	}
	f.openTokenHelp()
	if !slices.Equal(rec.opened, []string{jira.TokenHelpURL}) {
		t.Errorf("opened = %q", rec.opened)
	}
	f.next()
	loop.runUntil(t, func() bool { return len(rec.done) > 0 })
	if !slices.Equal(rec.busy, []string{"Saving the account", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	creds := api.Credentials{Password: "tok-new"}
	if sent := lastParams[api.AccountUpdateParams](t, d, api.MethodAccountUpdate); !reflect.DeepEqual(sent,
		api.AccountUpdateParams{AccountID: "acc-j1", Config: account.Config, Credentials: creds}) {
		t.Errorf("account.update = %+v", sent)
	}
	if !slices.Equal(rec.done, []api.AccountID{"acc-j1"}) || !reflect.DeepEqual(rec.doneConfigs, []api.AccountConfig{account.Config}) {
		t.Errorf("done = %v, %+v", rec.done, rec.doneConfigs)
	}
	if tried := lastParams[api.AccountListSpacesParams](t, d, api.MethodAccountListSpaces); !reflect.DeepEqual(tried,
		api.AccountListSpacesParams{AccountID: "acc-j1", Config: account.Config, Credentials: creds}) {
		t.Errorf("account.listSpaces = %+v", tried)
	}
	if calls := d.callList(); !slices.Equal(calls, []string{api.MethodAccountListSpaces, api.MethodAccountUpdate}) {
		t.Errorf("calls = %q, want the token tried before it is stored", calls)
	}
	checkBudget(t, d, api.MethodAccountListSpaces, 45*time.Second)
	checkBudget(t, d, api.MethodAccountUpdate, 30*time.Second)
}

func TestJiraFlowEditingKeepsTheStoredTokenWhenTheNewOneIsRefused(t *testing.T) {
	d := newFakeJiraDaemon()
	d.fail(api.MethodAccountListSpaces, api.NewError(api.CodeAuthFailed, "401"))
	account := jiraCloudAccount()
	f, rec, loop := startJiraFlow(d, &account, 0)
	f.setCredentials("", "wrong")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 2 })
	if last(rec.banners) != (jiraBanner{jira.PageCredentials, "The Jira site rejected the token"}) ||
		last(rec.problems) != fieldSet(fieldToken) || len(rec.done) != 0 {
		t.Errorf("banner %v, problems %v, done %v", last(rec.banners), last(rec.problems), rec.done)
	}
	if calls := d.callList(); !slices.Equal(calls, []string{api.MethodAccountListSpaces}) {
		t.Errorf("calls = %q: account.update is never sent", calls)
	}
}

func TestJiraFlowEditingAsksForTheTokenWithTheReason(t *testing.T) {
	d := newFakeJiraDaemon()
	d.fail(api.MethodAccountListSpaces, api.NewError(api.CodeAuthFailed, "401"))
	account := jiraCloudAccount()
	f, rec, loop := startJiraFlow(d, &account, api.CodeAuthFailed)
	rejected := jiraBanner{jira.PageCredentials, "The Jira site rejected the token"}
	if !reflect.DeepEqual(rec.banners, []jiraBanner{rejected}) || !reflect.DeepEqual(rec.problems, []jiraFields{fieldSet(fieldToken)}) ||
		!reflect.DeepEqual(rec.focus, []jiraField{fieldToken}) {
		t.Errorf("banners %v, problems %v, focus %v", rec.banners, rec.problems, rec.focus)
	}
	f.requestToken(api.CodeAuthRequired)
	if last(rec.banners) != (jiraBanner{jira.PageCredentials, "Enter the API token for this account"}) {
		t.Errorf("banner = %v", last(rec.banners))
	}

	// A refused save stays on the page with the reason.
	f.setCredentials("", "still-wrong")
	f.next()
	loop.runUntil(t, func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageCredentials}}) || last(rec.banners) != rejected || len(rec.done) != 0 {
		t.Errorf("pages %v, banner %v, done %v", rec.pages, last(rec.banners), rec.done)
	}
}

func TestJiraFlowRequestTokenIsIgnoredWhenAdding(t *testing.T) {
	_, rec, _ := startJiraFlow(newFakeJiraDaemon(), nil, api.CodeAuthFailed)
	if len(rec.banners) != 0 || !reflect.DeepEqual(rec.pages, [][]jira.Page{{jira.PageSite}}) {
		t.Errorf("banners %v, pages %v", rec.banners, rec.pages)
	}
}

// The navigation page tags of jira_wizard.blp name every page once.
func TestJiraPageTags(t *testing.T) {
	for _, p := range []jira.Page{jira.PageSite, jira.PageCredentials, jira.PageSpaces} {
		if got, ok := jiraPageOfTag(jiraPageTags[p]); !ok || got != p {
			t.Errorf("page %d: tag %q reads as %d, %v", p, jiraPageTags[p], got, ok)
		}
	}
	if _, ok := jiraPageOfTag("identity"); ok {
		t.Error("a tag of the mail assistant is a Jira page")
	}
}
