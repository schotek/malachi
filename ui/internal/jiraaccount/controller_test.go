// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jiraaccount

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The settings of a Jira account (Controller) against a fake daemon:
// account.listSpaces with the stored token and account.update with empty
// credentials. A port of the macOS JiraAccountControllerTests. Fictional
// sites and people only.

const listingJSON = `{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":[` +
	`{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":-1},` +
	`{"id":"10003","key":"MOB","name":"Mobile","issues":-1},` +
	`{"id":"10002","key":"WEB","name":"Website","issues":-1}],` +
	`"statuses":[{"id":"1","name":"Open","category":"todo"},` +
	`{"id":"3","name":"In Progress","category":"inProgress"},` +
	`{"id":"5","name":"Resolved","category":"done"},` +
	`{"id":"6","name":"Done","category":"done"},` +
	`{"id":"10010","name":"Done","category":"done"}]}`

// identity is the translator of the tests: every msgid is its own
// translation (the singular for n == 1).
type identity struct{}

func (identity) T(msgid string) string { return msgid }

func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}

func (identity) C(_, msgid string) string { return msgid }

// errorText stands for widget.RPCErrorText: the client's general sentence
// for a failed call, here the action and the error's code.
func errorText(what string, err error) string {
	var e *api.Error
	if errors.As(err, &e) {
		return what + " failed: " + e.Code.String()
	}
	return what + " failed"
}

// handler answers one method of the fake: the result's JSON, or an error.
type handler func(params json.RawMessage) (string, error)

// fakeDaemon is the Caller of the tests: handlers per method, swappable
// while the controller runs, and every call with its parameters recorded.
type fakeDaemon struct {
	mu       sync.Mutex
	handlers map[string]handler
	calls    []string
	params   map[string][]json.RawMessage
}

func newFake() *fakeDaemon {
	return &fakeDaemon{handlers: map[string]handler{}, params: map[string][]json.RawMessage{}}
}

func (f *fakeDaemon) on(method string, h handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func (f *fakeDaemon) Call(_ context.Context, method string, params any, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.params[method] = append(f.params[method], raw)
	h := f.handlers[method]
	f.mu.Unlock()
	if h == nil {
		return api.NewError(api.CodeMethodNotFound, "%s", method)
	}
	out, err := h(raw)
	if err != nil {
		return err
	}
	if result != nil && out != "" {
		return json.Unmarshal([]byte(out), result)
	}
	return nil
}

func (f *fakeDaemon) callsOf() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeDaemon) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.params[method])
}

// last decodes the parameters of the last call of method into v; false
// when there was none.
func (f *fakeDaemon) last(t *testing.T, method string, v any) bool {
	t.Helper()
	f.mu.Lock()
	all := f.params[method]
	f.mu.Unlock()
	if len(all) == 0 {
		return false
	}
	if err := json.Unmarshal(all[len(all)-1], v); err != nil {
		t.Fatalf("decode %s params: %v", method, err)
	}
	return true
}

// listingFake lists the spaces and accepts the update.
func listingFake() *fakeDaemon {
	f := newFake()
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) { return listingJSON, nil })
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) { return "{}", nil })
	return f
}

// loop is the main loop of the tests: the controller posts its replies
// here and the test runs them on its own goroutine, so that the
// controller's state is touched by one goroutine only, as on the GTK main
// loop.
type loop struct {
	t *testing.T
	q chan func()
}

func newLoop(t *testing.T) *loop { return &loop{t: t, q: make(chan func(), 64)} }

func (l *loop) post(f func()) { l.q <- f }

// until runs what is posted until cond holds; the test fails after 5 s.
func (l *loop) until(cond func() bool) {
	l.t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case f := <-l.q:
			f()
		case <-tick.C:
		case <-deadline:
			l.t.Fatal("timed out waiting for the controller")
		}
	}
}

// run runs what is posted for d.
func (l *loop) run(d time.Duration) {
	end := time.After(d)
	for {
		select {
		case f := <-l.q:
			f()
		case <-end:
			return
		}
	}
}

// recorder collects what the controller reports.
type recorder struct {
	changes       int
	busy          []string
	banners       []string
	done          []api.AccountID
	doneConfigs   []api.AccountConfig
	closes        int
	tokenRequests []api.AccountID
}

func (r *recorder) attach(c *Controller) {
	c.OnChange = func() { r.changes++ }
	c.OnBusy = func(text string) { r.busy = append(r.busy, text) }
	c.OnBanner = func(text string) { r.banners = append(r.banners, text) }
	c.OnDone = func(id api.AccountID, cfg api.AccountConfig) {
		r.done = append(r.done, id)
		r.doneConfigs = append(r.doneConfigs, cfg)
	}
	c.OnClose = func() { r.closes++ }
	c.OnReplaceToken = func(a api.Account) { r.tokenRequests = append(r.tokenRequests, a.ID) }
}

// cloudConfig is a stored Jira Cloud account with every setting at its
// default (ui/internal/jira settings_test.go cloudAccount).
func cloudConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme", Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud,
			CloudID: "0b9e3d2c-1a2b-4c3d-8e9f-001122334455", Login: "jana@acme.example",
			Spaces: []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB", Name: "Website"}},
		},
	}
}

// storedAccount is the stored account, as account.list returns it, with
// edit applied to its Jira configuration.
func storedAccount(edit func(*api.JiraConfig)) api.Account {
	cfg := cloudConfig()
	if edit != nil {
		edit(cfg.Jira)
	}
	return api.Account{
		ID: "acc-j1", Config: cfg, Enabled: true,
		State:        api.SyncState{AccountID: "acc-j1", Status: api.SyncIdle},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward},
	}
}

// makeController is a controller on the fake, its callbacks recorded; not
// started.
func makeController(l *loop, f *fakeDaemon, a api.Account) (*Controller, *recorder) {
	c := NewController(f, a, Env{Tr: identity{}, ErrorText: errorText, Post: l.post})
	r := &recorder{}
	r.attach(c)
	return c, r
}

func set(ids ...string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func rowIDs(rows []jira.SpaceRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// emptyCredentials reports whether the last call of method sent
// "credentials": {} on the wire: no token, the daemon takes the stored
// one.
func emptyCredentials(t *testing.T, f *fakeDaemon, method string) bool {
	t.Helper()
	var object map[string]any
	if !f.last(t, method, &object) {
		return false
	}
	creds, ok := object["credentials"].(map[string]any)
	return ok && len(creds) == 0
}

func TestLoadsTheSpacesAndStatusesWithTheStoredToken(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	account := storedAccount(nil)
	c, rec := makeController(l, f, account)

	// Before the listing: what is stored.
	if c.Title() != "Jira Account" {
		t.Errorf("Title = %q", c.Title())
	}
	wantSite := jira.SiteInfo{Address: "https://acme.atlassian.net", Deployment: "Jira Cloud", User: "jana@acme.example", TokenLabel: "API Token"}
	if c.Site() != wantSite {
		t.Errorf("Site = %+v", c.Site())
	}
	if got := rowIDs(c.SpaceRows()); !reflect.DeepEqual(got, []string{"10001", "10002"}) {
		t.Errorf("SpaceRows = %v", got)
	}
	if !reflect.DeepEqual(c.SelectedSpaces(), set("10001", "10002")) {
		t.Errorf("SelectedSpaces = %v", c.SelectedSpaces())
	}
	if len(c.StatusGroups()) != 0 {
		t.Errorf("StatusGroups = %+v", c.StatusGroups())
	}
	if !reflect.DeepEqual(c.OfflineLabels(), []string{"1 week", "1 month", "3 months", "1 year"}) || c.OfflineIndex() != 1 {
		t.Errorf("offline = %v, %d", c.OfflineLabels(), c.OfflineIndex())
	}
	if c.NotificationIndex() != 0 || c.NotificationHint() != "" || !c.SendersEditable() {
		t.Errorf("notification = %d, %q, %v", c.NotificationIndex(), c.NotificationHint(), c.SendersEditable())
	}
	if c.SendersPlaceholder() != "@acme.atlassian.net" {
		t.Errorf("SendersPlaceholder = %q", c.SendersPlaceholder())
	}
	for _, v := range jira.VirtualFolders {
		if !c.FolderShown(v) {
			t.Errorf("FolderShown(%s) = false", v)
		}
	}
	if c.IsChanged() || !c.CanSave() || c.Busy() {
		t.Errorf("changed %v, can save %v, busy %v", c.IsChanged(), c.CanSave(), c.Busy())
	}

	c.Start()
	if !c.Busy() || c.Saving() {
		t.Errorf("after Start: busy %v, saving %v", c.Busy(), c.Saving())
	}
	if !reflect.DeepEqual(rec.busy, []string{"Loading the spaces"}) {
		t.Errorf("busy = %q", rec.busy)
	}
	l.until(func() bool { return c.Listing() != nil })
	if !reflect.DeepEqual(rec.busy, []string{"Loading the spaces", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	if len(rec.banners) != 0 {
		t.Errorf("banners = %q", rec.banners)
	}
	if rec.changes != 2 {
		t.Errorf("changes = %d, want 2: the initial state and the listing", rec.changes)
	}

	var sent api.AccountListSpacesParams
	if !f.last(t, api.MethodAccountListSpaces, &sent) {
		t.Fatal("account.listSpaces was not called")
	}
	if sent.AccountID != "acc-j1" || !reflect.DeepEqual(sent.Config, account.Config) {
		t.Errorf("listSpaces params = %+v", sent)
	}
	if sent.Credentials != (api.Credentials{}) || sent.Counts {
		t.Errorf("credentials %+v, counts %v: no token, the daemon takes the stored one", sent.Credentials, sent.Counts)
	}
	if !emptyCredentials(t, f, api.MethodAccountListSpaces) {
		t.Error("credentials on the wire are not an empty object")
	}

	if s := c.Site(); s.User != "Jana Dvořáková" || s.UserDetail != "jana@acme.example" {
		t.Errorf("Site after listing = %+v", s)
	}
	wantRows := []jira.SpaceRow{
		{ID: "10001", Title: "ITSD – IT Service Desk", ServiceDesk: true},
		{ID: "10003", Title: "MOB – Mobile"},
		{ID: "10002", Title: "WEB – Website"},
	}
	if !reflect.DeepEqual(c.SpaceRows(), wantRows) {
		t.Errorf("SpaceRows = %+v", c.SpaceRows())
	}
	if !reflect.DeepEqual(c.SelectedSpaces(), set("10001", "10002")) {
		t.Errorf("SelectedSpaces = %v", c.SelectedSpaces())
	}
	groups := c.StatusGroups()
	var titles []string
	for _, g := range groups {
		titles = append(titles, g.Title)
	}
	if !reflect.DeepEqual(titles, []string{"To Do", "In Progress", "Done"}) {
		t.Fatalf("status groups = %q", titles)
	}
	wantDone := []jira.StatusChoice{
		{Name: "Resolved", IDs: []string{"5"}, Selected: true},
		{Name: "Done", IDs: []string{"6", "10010"}, Selected: true},
	}
	if got := groups[len(groups)-1].Choices; !reflect.DeepEqual(got, wantDone) {
		t.Errorf("done choices = %+v", got)
	}
	if c.IsChanged() {
		t.Error("the listing changed the form")
	}
	if got := f.callsOf(); !reflect.DeepEqual(got, []string{api.MethodAccountListSpaces}) {
		t.Errorf("calls = %v", got)
	}
}

func TestAFailedListingStillEditsWhatIsStored(t *testing.T) {
	l := newLoop(t)
	f := newFake()
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeAuthFailed, "401")
	})
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) { return "{}", nil })
	account := storedAccount(func(jc *api.JiraConfig) { jc.ClosedStatuses = []api.StatusRef{{ID: "5", Name: "Resolved"}} })
	c, rec := makeController(l, f, account)
	c.Start()
	l.until(func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.banners, []string{"The Jira site rejected the token"}) || c.Banner() != "The Jira site rejected the token" {
		t.Errorf("banners = %q, Banner = %q", rec.banners, c.Banner())
	}
	if c.Listing() != nil {
		t.Error("a failed listing is kept")
	}
	// The stored spaces and statuses are what there is to choose from.
	var titles []string
	for _, r := range c.SpaceRows() {
		titles = append(titles, r.Title)
	}
	if !reflect.DeepEqual(titles, []string{"ITSD – IT Service Desk", "WEB – Website"}) {
		t.Errorf("space titles = %q", titles)
	}
	wantGroups := []jira.StatusGroup{{
		Category: "", Title: "Other", Style: jira.StatusPlain,
		Choices: []jira.StatusChoice{{Name: "Resolved", IDs: []string{"5"}, Selected: true}},
	}}
	if !reflect.DeepEqual(c.StatusGroups(), wantGroups) {
		t.Errorf("StatusGroups = %+v", c.StatusGroups())
	}
	if c.Site().User != "jana@acme.example" {
		t.Errorf("Site().User = %q", c.Site().User)
	}

	// Editing keeps the banner of the listing; Save stores the change.
	c.SetSpace("10002", false)
	c.SetOnlyMine(true)
	if len(rec.banners) != 1 {
		t.Errorf("banners = %q", rec.banners)
	}
	if !c.IsChanged() || !c.CanSave() {
		t.Errorf("changed %v, can save %v", c.IsChanged(), c.CanSave())
	}
	c.Save()
	l.until(func() bool { return len(rec.done) > 0 })
	var sent api.AccountUpdateParams
	if !f.last(t, api.MethodAccountUpdate, &sent) {
		t.Fatal("account.update was not called")
	}
	jc := sent.Config.Jira
	if !reflect.DeepEqual(jc.Spaces, []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}}) ||
		!jc.OnlyMine || !reflect.DeepEqual(jc.ClosedStatuses, []api.StatusRef{{ID: "5", Name: "Resolved"}}) {
		t.Errorf("update sent %+v", jc)
	}

	// Other failures: the client's sentence for the step.
	network := api.NewError(api.CodeNetworkError, "no route")
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) { return "", network })
	c2, rec2 := makeController(l, f, account)
	c2.Start()
	l.until(func() bool { return len(rec2.busy) == 2 })
	if want := []string{errorText("Loading the spaces", network)}; !reflect.DeepEqual(rec2.banners, want) {
		t.Errorf("banners = %q, want %q", rec2.banners, want)
	}
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeAuthRequired, "no token")
	})
	c3, rec3 := makeController(l, f, account)
	c3.Start()
	l.until(func() bool { return len(rec3.busy) == 2 })
	if !reflect.DeepEqual(rec3.banners, []string{"Enter the API token for this account"}) {
		t.Errorf("banners = %q", rec3.banners)
	}
}

func TestSavesTheFormAndKeepsTheToken(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	account := storedAccount(nil)
	c, rec := makeController(l, f, account)
	c.Start()
	l.until(func() bool { return c.Listing() != nil })

	c.SetName("  Acme Jira ")
	c.SetSpace("10003", true)
	c.SetSpace("10001", false)
	c.SetSpace("99999", true)
	if !reflect.DeepEqual(c.SelectedSpaces(), set("10002", "10003")) {
		t.Errorf("SelectedSpaces = %v: an unknown space is ignored", c.SelectedSpaces())
	}
	c.SetOfflineIndex(2)
	c.SetOfflineIndex(17)
	if c.OfflineIndex() != 2 {
		t.Errorf("OfflineIndex = %d", c.OfflineIndex())
	}
	c.SetOnlyMine(true)
	c.SetShowEvents(false)
	c.SetFolder(api.VirtualWatching, false)
	c.SetFolder("archive", false)
	if c.FolderShown(api.VirtualWatching) || !c.FolderShown(api.VirtualOpen) {
		t.Error("the switch of Watching did not take, or another view went with it")
	}
	groups := c.StatusGroups()
	resolved := groups[len(groups)-1].Choices[0]
	c.SetStatus(resolved, false)
	groups = c.StatusGroups()
	var selected []bool
	for _, ch := range groups[len(groups)-1].Choices {
		selected = append(selected, ch.Selected)
	}
	if !reflect.DeepEqual(selected, []bool{false, true}) {
		t.Errorf("done choices selected = %v", selected)
	}
	c.SetNotificationIndex(1)
	if c.NotificationHint() != "Hidden e-mails stay in your mailbox and come back when you turn this off" {
		t.Errorf("NotificationHint = %q", c.NotificationHint())
	}
	for _, add := range []struct {
		kind jira.ListKind
		text string
	}{
		{jira.ListSenders, " Jira@Acme.Example "},
		{jira.ListBotNames, jira.SuggestedBotName},
		{jira.ListMetadataFilters, jira.SuggestedMetadataFilter},
		{jira.ListAuthorPrefixes, "ACME"},
	} {
		if !c.AddEntry(add.kind, add.text) {
			t.Errorf("AddEntry(%d, %q) refused: %q", add.kind, add.text, c.Problem(add.kind))
		}
	}
	if !c.IsChanged() || !c.CanSave() {
		t.Errorf("changed %v, can save %v", c.IsChanged(), c.CanSave())
	}
	changes := rec.changes

	c.Save()
	if !c.Saving() || !c.Busy() || c.CanSave() {
		t.Errorf("while saving: saving %v, busy %v, can save %v", c.Saving(), c.Busy(), c.CanSave())
	}
	if rec.changes != changes+1 {
		t.Errorf("changes = %d, want %d", rec.changes, changes+1)
	}
	// While it saves the page waits.
	c.SetOnlyMine(false)
	if c.AddEntry(jira.ListAuthorPrefixes, "Globex") {
		t.Error("an entry was added while saving")
	}
	c.Save()
	l.until(func() bool { return len(rec.done) > 0 })
	if got := rec.busy[len(rec.busy)-2:]; !reflect.DeepEqual(got, []string{"Saving the account", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	if c.Saving() {
		t.Error("still saving")
	}

	want := account.Config
	jc := *want.Jira
	want.Name = "Acme Jira"
	jc.Spaces = []api.SpaceRef{{ID: "10003", Key: "MOB", Name: "Mobile"}, {ID: "10002", Key: "WEB", Name: "Website"}}
	jc.OfflineDays = 90
	jc.OnlyMine = true
	jc.HideEvents = true
	jc.DisabledFolders = []api.VirtualFolder{api.VirtualWatching}
	jc.ClosedStatuses = []api.StatusRef{{ID: "6", Name: "Done"}, {ID: "10010", Name: "Done"}}
	jc.NotificationMail = api.NotificationMailHide
	jc.NotificationSenders = []string{"jira@acme.example"}
	jc.BotNames = []string{jira.SuggestedBotName}
	jc.MetadataFilters = []string{jira.SuggestedMetadataFilter}
	jc.AuthorPrefixes = []string{"ACME"}
	want.Jira = &jc
	var sent api.AccountUpdateParams
	if !f.last(t, api.MethodAccountUpdate, &sent) {
		t.Fatal("account.update was not called")
	}
	if wantParams := (api.AccountUpdateParams{AccountID: "acc-j1", Config: want}); !reflect.DeepEqual(sent, wantParams) {
		t.Errorf("update sent\n%+v\n%+v\nwant\n%+v\n%+v", sent, *sent.Config.Jira, wantParams, *wantParams.Config.Jira)
	}
	// No password on the wire: the daemon keeps the stored token.
	if !emptyCredentials(t, f, api.MethodAccountUpdate) {
		t.Error("credentials on the wire are not an empty object")
	}
	if !reflect.DeepEqual(rec.done, []api.AccountID{"acc-j1"}) || !reflect.DeepEqual(rec.doneConfigs, []api.AccountConfig{want}) {
		t.Errorf("done %v with %+v", rec.done, rec.doneConfigs)
	}
	if rec.closes != 0 || f.count(api.MethodAccountUpdate) != 1 || len(rec.banners) != 0 {
		t.Errorf("closes %d, updates %d, banners %q", rec.closes, f.count(api.MethodAccountUpdate), rec.banners)
	}
}

func TestAFormThatChangesNothingClosesWithoutACall(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	// The stored account spells its defaults out.
	account := storedAccount(func(jc *api.JiraConfig) {
		jc.OfflineDays = 30
		jc.NotificationMail = api.NotificationMailSync
	})
	c, rec := makeController(l, f, account)
	c.Start()
	l.until(func() bool { return c.Listing() != nil })
	// Changes that come back to where they were.
	c.SetOnlyMine(true)
	c.SetOnlyMine(false)
	c.SetSpace("10001", false)
	c.SetSpace("10001", true)
	c.SetName(" Acme ")
	groups := c.StatusGroups()
	choices := groups[len(groups)-1].Choices
	done := choices[len(choices)-1]
	c.SetStatus(done, false)
	c.SetStatus(done, true)
	if len(c.Form().ClosedStatuses) != 0 {
		t.Errorf("ClosedStatuses = %+v: the statuses of the category done are the default", c.Form().ClosedStatuses)
	}
	if c.IsChanged() {
		t.Error("IsChanged")
	}
	c.Save()
	if rec.closes != 1 {
		t.Errorf("closes = %d", rec.closes)
	}
	if c.Busy() || len(rec.done) != 0 {
		t.Errorf("busy %v, done %v", c.Busy(), rec.done)
	}
	l.run(50 * time.Millisecond)
	if n := f.count(api.MethodAccountUpdate); n != 0 {
		t.Errorf("account.update called %d times", n)
	}
}

func TestARefusedSaveSaysWhy(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeConflict, "exists")
	})
	c, rec := makeController(l, f, storedAccount(nil))
	c.Start()
	l.until(func() bool { return c.Listing() != nil })
	c.SetOnlyMine(true)
	c.Save()
	l.until(func() bool { return len(rec.banners) == 1 })
	if !reflect.DeepEqual(rec.banners, []string{"An account for this Jira site already exists"}) {
		t.Errorf("banners = %q", rec.banners)
	}
	if c.Saving() || c.Busy() || !c.CanSave() {
		t.Errorf("saving %v, busy %v, can save %v", c.Saving(), c.Busy(), c.CanSave())
	}
	if len(rec.done) != 0 || rec.closes != 0 {
		t.Errorf("done %v, closes %d", rec.done, rec.closes)
	}
	if !c.Form().OnlyMine {
		t.Error("the form did not stay as it was")
	}

	// The next change takes the banner away.
	c.SetShowEvents(false)
	if rec.banners[len(rec.banners)-1] != "" || c.Banner() != "" {
		t.Errorf("banners = %q, Banner = %q", rec.banners, c.Banner())
	}

	invalid := api.NewError(api.CodeInvalidArgument, "jira: metadataFilters entry 1 is not a valid RE2 pattern")
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) { return "", invalid })
	c.Save()
	l.until(func() bool { return len(rec.banners) == 3 })
	if want := errorText("Saving the account", invalid); rec.banners[2] != want || c.Banner() != want {
		t.Errorf("banners = %q, want %q last", rec.banners, want)
	}

	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeAuthFailed, "401")
	})
	c.Save()
	l.until(func() bool { return rec.banners[len(rec.banners)-1] == "The Jira site rejected the token" })
	keyring := api.NewError(api.CodeKeyringError, "locked")
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) { return "", keyring })
	c.Save()
	l.until(func() bool { return c.Banner() == errorText("Saving the account", keyring) })
}

func TestAFormThatCannotBeSavedIsNotSent(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	c, rec := makeController(l, f, storedAccount(nil))
	c.Start()
	l.until(func() bool { return c.Listing() != nil })
	c.SetSpace("10001", false)
	c.SetSpace("10002", false)
	if len(c.SelectedSpaces()) != 0 {
		t.Errorf("SelectedSpaces = %v", c.SelectedSpaces())
	}
	if c.SpacesProblem() != "Select at least one space" || c.CanSave() {
		t.Errorf("SpacesProblem = %q, CanSave %v", c.SpacesProblem(), c.CanSave())
	}
	c.Save()
	if !reflect.DeepEqual(rec.banners, []string{"Select at least one space"}) || c.Busy() {
		t.Errorf("banners = %q, busy %v", rec.banners, c.Busy())
	}
	c.SetSpace("10003", true)
	if rec.banners[len(rec.banners)-1] != "" {
		t.Errorf("banners = %q", rec.banners)
	}
	if !c.CanSave() || c.SpacesProblem() != "" {
		t.Errorf("CanSave %v, SpacesProblem %q", c.CanSave(), c.SpacesProblem())
	}
	l.run(50 * time.Millisecond)
	if n := f.count(api.MethodAccountUpdate); n != 0 {
		t.Errorf("account.update called %d times", n)
	}
}

func TestTheListsCheckWhatIsAdded(t *testing.T) {
	l := newLoop(t)
	f := listingFake()
	c, rec := makeController(l, f, storedAccount(func(jc *api.JiraConfig) { jc.BotNames = []string{"Deploy Bot"} }))
	c.Start()
	l.until(func() bool { return c.Listing() != nil })

	if !reflect.DeepEqual(c.Entries(jira.ListBotNames), []string{"Deploy Bot"}) {
		t.Errorf("bot names = %q", c.Entries(jira.ListBotNames))
	}
	wantSuggestion := []jira.Suggestion{{Value: jira.SuggestedBotName, Label: "Add Issue Sync – Synchronization for Jira"}}
	if !reflect.DeepEqual(c.Suggestions(jira.ListBotNames), wantSuggestion) {
		t.Errorf("bot suggestions = %+v", c.Suggestions(jira.ListBotNames))
	}
	if s := c.Suggestions(jira.ListMetadataFilters); len(s) != 1 || s[0].Value != jira.SuggestedMetadataFilter {
		t.Errorf("pattern suggestions = %+v", s)
	}
	if len(c.Suggestions(jira.ListAuthorPrefixes)) != 0 || len(c.Suggestions(jira.ListSenders)) != 0 {
		t.Error("prefixes or senders have suggestions")
	}

	// Nothing typed: nothing added, nothing wrong.
	if !c.AddEntry(jira.ListBotNames, "  ") {
		t.Error("an empty field is not emptied")
	}
	if !reflect.DeepEqual(c.Entries(jira.ListBotNames), []string{"Deploy Bot"}) || c.Problem(jira.ListBotNames) != "" {
		t.Errorf("bot names %q, problem %q", c.Entries(jira.ListBotNames), c.Problem(jira.ListBotNames))
	}

	if c.AddEntry(jira.ListBotNames, "ab") {
		t.Error("a short bot name was added")
	}
	if p := c.Problem(jira.ListBotNames); p != "A bot name needs at least 3 characters" {
		t.Errorf("problem = %q", p)
	}
	if c.Problem(jira.ListMetadataFilters) != "" {
		t.Error("a problem went to another list")
	}
	changes := rec.changes
	c.EntryTyped(jira.ListBotNames)
	if c.Problem(jira.ListBotNames) != "" || rec.changes != changes+1 {
		t.Errorf("after typing: problem %q, changes %d", c.Problem(jira.ListBotNames), rec.changes-changes)
	}
	c.EntryTyped(jira.ListBotNames)
	if rec.changes != changes+1 {
		t.Error("typing again reported a change: there was nothing to clear")
	}

	for _, bad := range []struct {
		kind         jira.ListKind
		text, reason string
	}{
		{jira.ListBotNames, "deploy  bot", "This entry is already in the list"},
		{jira.ListMetadataFilters, "(unclosed", "This pattern is not valid: missing closing )"},
		{jira.ListMetadataFilters, `(?<=Remote).*`, "This pattern is not valid: invalid named capture"},
		{jira.ListSenders, "acme.example", "Enter an address, or a domain such as @example.org"},
	} {
		if c.AddEntry(bad.kind, bad.text) {
			t.Errorf("%q was added", bad.text)
		}
		if p := c.Problem(bad.kind); p != bad.reason {
			t.Errorf("problem of %q = %q, want %q", bad.text, p, bad.reason)
		}
	}
	if c.IsChanged() {
		t.Error("nothing was added, yet the form changed")
	}

	// A suggestion is added as it is, once.
	c.AddSuggestion(jira.ListBotNames, jira.SuggestedBotName)
	c.AddSuggestion(jira.ListBotNames, jira.SuggestedBotName)
	c.AddSuggestion(jira.ListBotNames, "Somebody Else")
	if !reflect.DeepEqual(c.Entries(jira.ListBotNames), []string{"Deploy Bot", jira.SuggestedBotName}) {
		t.Errorf("bot names = %q", c.Entries(jira.ListBotNames))
	}
	if c.Problem(jira.ListBotNames) != "" || len(c.Suggestions(jira.ListBotNames)) != 0 {
		t.Errorf("problem %q, suggestions %+v", c.Problem(jira.ListBotNames), c.Suggestions(jira.ListBotNames))
	}
	if !c.AddEntry(jira.ListMetadataFilters, ` ^Sent from .*$ `) {
		t.Error("a valid pattern was refused")
	}
	if !reflect.DeepEqual(c.Entries(jira.ListMetadataFilters), []string{`^Sent from .*$`}) || c.Problem(jira.ListMetadataFilters) != "" {
		t.Errorf("patterns %q, problem %q", c.Entries(jira.ListMetadataFilters), c.Problem(jira.ListMetadataFilters))
	}
	if !c.AddEntry(jira.ListSenders, "@Acme.Example") {
		t.Error("a domain was refused")
	}
	if !reflect.DeepEqual(c.Entries(jira.ListSenders), []string{"@acme.example"}) {
		t.Errorf("senders = %q", c.Entries(jira.ListSenders))
	}
	if !c.IsChanged() {
		t.Error("the added entries did not change the form")
	}

	c.RemoveEntry(jira.ListBotNames, 0)
	c.RemoveEntry(jira.ListBotNames, 7)
	if !reflect.DeepEqual(c.Entries(jira.ListBotNames), []string{jira.SuggestedBotName}) {
		t.Errorf("bot names = %q", c.Entries(jira.ListBotNames))
	}
	c.RemoveEntry(jira.ListBotNames, 0)
	if len(c.Entries(jira.ListBotNames)) != 0 {
		t.Errorf("bot names = %q", c.Entries(jira.ListBotNames))
	}
	if len(c.Suggestions(jira.ListBotNames)) != 1 {
		t.Error("the suggestion is not offered again once it is gone")
	}

	// The senders do not matter when notifications are left alone.
	c.SetNotificationIndex(2)
	if c.SendersEditable() || c.NotificationIndex() != 2 {
		t.Errorf("senders editable %v, mode %d", c.SendersEditable(), c.NotificationIndex())
	}
	c.SetNotificationIndex(9)
	if c.NotificationIndex() != 2 {
		t.Errorf("mode = %d", c.NotificationIndex())
	}
}

func TestALateReplyIsDropped(t *testing.T) {
	l := newLoop(t)
	f := newFake()
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) {
		time.Sleep(300 * time.Millisecond)
		return listingJSON, nil
	})
	f.on(api.MethodAccountUpdate, func(json.RawMessage) (string, error) {
		time.Sleep(200 * time.Millisecond)
		return "{}", nil
	})
	// Save while the spaces load: the listing is not waited for.
	c, rec := makeController(l, f, storedAccount(nil))
	c.Start()
	l.until(func() bool { return f.count(api.MethodAccountListSpaces) > 0 })
	c.SetOnlyMine(true)
	c.Save()
	if !reflect.DeepEqual(rec.busy, []string{"Loading the spaces", "Saving the account"}) {
		t.Errorf("busy = %q", rec.busy)
	}
	l.until(func() bool { return len(rec.done) > 0 })
	l.run(400 * time.Millisecond)
	if c.Listing() != nil {
		t.Error("the listing that arrived after Save started was taken")
	}
	if !reflect.DeepEqual(rec.busy, []string{"Loading the spaces", "Saving the account", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}

	// Closed while it saves: nothing is reported any more.
	c2, rec2 := makeController(l, f, storedAccount(nil))
	c2.SetOnlyMine(true)
	c2.Save()
	l.until(func() bool { return f.count(api.MethodAccountUpdate) == 2 })
	c2.Close()
	if !c2.Closed() || c2.Busy() || c2.Saving() {
		t.Errorf("closed %v, busy %v, saving %v", c2.Closed(), c2.Busy(), c2.Saving())
	}
	l.run(400 * time.Millisecond)
	if len(rec2.done) != 0 || len(rec2.banners) != 0 {
		t.Errorf("done %v, banners %q after Close", rec2.done, rec2.banners)
	}
	if !reflect.DeepEqual(rec2.busy, []string{"Saving the account"}) {
		t.Errorf("busy = %q", rec2.busy)
	}
	// A closed page does nothing.
	c2.Save()
	c2.ReplaceToken()
	c2.TokenReplaced()
	if len(rec2.tokenRequests) != 0 {
		t.Errorf("token requests = %v", rec2.tokenRequests)
	}
	l.run(50 * time.Millisecond)
	if n := f.count(api.MethodAccountUpdate); n != 2 {
		t.Errorf("account.update called %d times", n)
	}
	if n := f.count(api.MethodAccountListSpaces); n != 1 {
		t.Errorf("account.listSpaces called %d times", n)
	}
}

func TestReplacingTheTokenListsAgain(t *testing.T) {
	l := newLoop(t)
	f := newFake()
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeAuthFailed, "401")
	})
	c, rec := makeController(l, f, storedAccount(nil))
	c.Start()
	l.until(func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.banners, []string{"The Jira site rejected the token"}) {
		t.Errorf("banners = %q", rec.banners)
	}

	c.SetOnlyMine(true)
	c.ReplaceToken()
	if !reflect.DeepEqual(rec.tokenRequests, []api.AccountID{"acc-j1"}) {
		t.Errorf("token requests = %v", rec.tokenRequests)
	}

	// The assistant stored a new token: the listing works now, and the
	// form keeps what was edited.
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) { return listingJSON, nil })
	c.TokenReplaced()
	if rec.banners[len(rec.banners)-1] != "" {
		t.Errorf("banners = %q: the old reason goes at once", rec.banners)
	}
	l.until(func() bool { return c.Listing() != nil })
	if !reflect.DeepEqual(rec.busy, []string{"Loading the spaces", "", "Loading the spaces", ""}) {
		t.Errorf("busy = %q", rec.busy)
	}
	if c.Banner() != "" || len(c.SpaceRows()) != 3 || !c.Form().OnlyMine {
		t.Errorf("Banner %q, %d space rows, only mine %v", c.Banner(), len(c.SpaceRows()), c.Form().OnlyMine)
	}
}

func TestADataCenterAccount(t *testing.T) {
	l := newLoop(t)
	f := newFake()
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) {
		return "", api.NewError(api.CodeAuthRequired, "no token")
	})
	account := api.Account{
		ID: "acc-dc", Enabled: true,
		Config: api.AccountConfig{
			Name: "Acme Jira", Email: "jana@acme.example", Kind: api.AccountJira,
			Jira: &api.JiraConfig{
				SiteURL: "https://jira.acme.example/jira", Deployment: api.JiraDataCenter,
				Spaces: []api.SpaceRef{{ID: "20001", Key: "OPS", Name: "Operations"}},
			},
		},
		State:        api.SyncState{AccountID: "acc-dc", Status: api.SyncAuthRequired},
		Capabilities: []api.AccountCapability{},
	}
	c, rec := makeController(l, f, account)
	if c.Deployment() != api.JiraDataCenter {
		t.Errorf("Deployment = %q", c.Deployment())
	}
	if s := c.Site(); s.Deployment != "Jira Data Center" || s.TokenLabel != "Personal Access Token" {
		t.Errorf("Site = %+v", s)
	}
	if c.SendersPlaceholder() != "" {
		t.Errorf("SendersPlaceholder = %q", c.SendersPlaceholder())
	}
	c.Start()
	l.until(func() bool { return len(rec.busy) == 2 })
	if !reflect.DeepEqual(rec.banners, []string{"Enter the personal access token for this account"}) {
		t.Errorf("banners = %q", rec.banners)
	}
}

// Every string the page shows from the site is cleaned before it gets to
// a widget: a hostile listing (bidirectional overrides, line breaks,
// zero-width characters) comes out as one plain line.
func TestHostileListingIsCleaned(t *testing.T) {
	l := newLoop(t)
	f := newFake()
	rlo, zwsp := string(rune(0x202E)), string(rune(0x200B))
	hostile := api.AccountListSpacesResult{
		User:     api.SiteUser{Name: "Eve" + rlo + "\n<b>Admin</b>", Email: "eve@acme.example" + zwsp},
		Spaces:   []api.Space{{ID: "10001", Key: "ITSD" + zwsp, Name: "IT\r\nService" + rlo + " Desk", Issues: -1}},
		Statuses: []api.IssueStatus{{ID: "6", Name: "Do" + zwsp + "ne\t<i>x</i>", Category: api.StatusCategoryDone}},
	}
	body, err := json.Marshal(hostile)
	if err != nil {
		t.Fatal(err)
	}
	f.on(api.MethodAccountListSpaces, func(json.RawMessage) (string, error) { return string(body), nil })
	c, _ := makeController(l, f, storedAccount(nil))
	c.Start()
	l.until(func() bool { return c.Listing() != nil })
	if s := c.Site(); s.User != "Eve <b>Admin</b>" || s.UserDetail != "eve@acme.example" {
		t.Errorf("Site = %+v", s)
	}
	if rows := c.SpaceRows(); rows[0].Title != "ITSD – IT Service Desk" {
		t.Errorf("space title = %q", rows[0].Title)
	}
	for _, g := range c.StatusGroups() {
		for _, ch := range g.Choices {
			if strings.ContainsAny(ch.Name, "\t\n"+zwsp) {
				t.Errorf("status name %q is not cleaned", ch.Name)
			}
		}
	}
}
