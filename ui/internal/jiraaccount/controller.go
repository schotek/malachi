// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package jiraaccount is the settings dialog of a Jira account (kind
// jira): the site (read only, with Replace Token…), the account's name,
// the spaces, the synchronisation, the folders with the statuses that
// count as closed, the notification e-mails and the comments posted by
// bots, on one scrolling page with Cancel and Save in the header bar.
//
// The texts and rules are ui/internal/jira (settings.go, wizard.go); the
// calls, the edited copy and the checks are the Controller, plain Go
// behind Caller so that its tests run without a display or a daemon (a
// port of the macOS JiraAccountController, whose tests it ports too); the
// widgets are Dialog (dialog.go) over data/ui/jira_account.blp, with the
// rows that depend on the account built in Go (lists.go, statuses.go).
package jiraaccount

import (
	"context"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// RPC budgets: docs/api.md §4.1 gives account.listSpaces 45 s (it signs
// in to the site and lists up to 1000 spaces); account.update may wait for
// a keyring unlock dialog, as account.add does in the account assistant.
const (
	listSpacesTimeout = 45 * time.Second
	updateTimeout     = 30 * time.Second
)

// Caller is the one thing the controller needs of the daemon: a call.
// *client.Client is one; the tests pass a fake.
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// Env is what the controller runs with besides the daemon. GTK passes
// i18n.Tr, widget.RPCErrorText and glib.IdleAdd; the tests pass stand-ins
// that need no gettext and no main loop.
type Env struct {
	// Tr translates the texts of ui/internal/jira.
	Tr jira.Translator
	// ErrorText is the client's general sentence for a failed call, with
	// the step's action ("Loading the spaces") as what.
	ErrorText func(what string, err error) string
	// Post runs f on the main loop, where every method of the controller
	// and every callback runs; replies come back through it.
	Post func(f func())
	// Log may be nil.
	Log *slog.Logger
}

// Controller is the settings of a Jira account with the widgets replaced
// by callbacks.
//
// The page opens with account.listSpaces for the stored account, without
// a token (the daemon takes the stored one, docs/api.md §4.1): the spaces
// and the statuses to choose from and the user the account signs in as.
// When that fails the banner says why and the page edits what is stored.
// Everything the page changes is a copy (the form) until Save, which is
// account.update with the form applied to the stored configuration and
// empty credentials, so the token stays as it is; a form that changes
// nothing the daemon acts on closes without a call. The token itself is
// replaced by the account assistant in its edit mode, which the window
// opens on OnReplaceToken and reports back with TokenReplaced.
//
// Every RPC reply is dropped once the page closed or another call started
// since (op), as in the account assistant. Nothing typed here is logged.
//
// Every method must be called, and every callback runs, on the main loop.
// Set the callbacks, then call Start.
type Controller struct {
	// OnChange: what the page shows changed (the rows, a selection, a
	// list, a problem); the UI reads the controller again.
	OnChange func()
	// OnBusy: a call started, with its progress text, or finished ("").
	// While the spaces load the page stays usable; while it saves it
	// waits (Saving).
	OnBusy func(progress string)
	// OnBanner: the page's banner; "" hides it.
	OnBanner func(text string)
	// OnDone: the account was stored; the UI closes the page.
	OnDone func(id api.AccountID, cfg api.AccountConfig)
	// OnClose: Save had nothing to store; the UI closes the page.
	OnClose func()
	// OnReplaceToken: "Replace Token…"; the UI opens the account
	// assistant in its edit mode for the account and calls TokenReplaced
	// once it stored the new token.
	OnReplaceToken func(a api.Account)

	// Texts are the fixed texts (jira.SettingsTexts).
	Texts jira.SettingsStrings

	caller    Caller
	account   api.Account
	tr        jira.Translator
	errorText func(what string, err error) string
	post      func(func())
	log       *slog.Logger

	form jira.SettingsForm
	// listing is what account.listSpaces answered; nil before it did and
	// when it failed.
	listing *api.AccountListSpacesResult
	// progress is the progress text of the call under way; "" when none
	// runs.
	progress string
	// saving: account.update is under way; the page waits.
	saving bool
	closed bool
	// op is bumped per RPC so that stale replies bail out.
	op int

	// problems say why the entry typed last was not added, per list.
	problems map[jira.ListKind]string
	// loadProblem is why account.listSpaces failed; it stays until the
	// spaces are asked for again.
	loadProblem string
	// saveProblem is why Save did nothing; it goes with the next change
	// of the form.
	saveProblem string
	// shownBanner is the banner the UI was told last.
	shownBanner string
}

// NewController is the controller of the stored account a.
func NewController(c Caller, a api.Account, env Env) *Controller {
	log := env.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Controller{
		Texts:     jira.SettingsTexts(env.Tr),
		caller:    c,
		account:   a,
		tr:        env.Tr,
		errorText: env.ErrorText,
		post:      env.Post,
		log:       log,
		form:      jira.NewSettingsForm(a.Config),
		problems:  map[jira.ListKind]string{},
	}
}

// Presentation

// Account is the account as it is stored.
func (c *Controller) Account() api.Account { return c.account }

// Form is the edited copy.
func (c *Controller) Form() jira.SettingsForm { return c.form }

// Listing is what account.listSpaces answered; nil before it did and when
// it failed.
func (c *Controller) Listing() *api.AccountListSpacesResult { return c.listing }

// Banner is the banner shown, "" for none: why Save failed, else why the
// spaces could not be listed.
func (c *Controller) Banner() string {
	if c.saveProblem != "" {
		return c.saveProblem
	}
	return c.loadProblem
}

// Progress is the progress text of the call under way; "" when none runs.
func (c *Controller) Progress() string { return c.progress }

// Busy reports a call under way.
func (c *Controller) Busy() bool { return c.progress != "" }

// Saving reports account.update under way: the page waits.
func (c *Controller) Saving() bool { return c.saving }

// Closed reports a page that went away.
func (c *Controller) Closed() bool { return c.closed }

// Title is the dialog's title.
func (c *Controller) Title() string { return c.Texts.Title }

// Deployment is the account's deployment; Jira Cloud when it has none.
func (c *Controller) Deployment() api.JiraDeployment {
	if c.account.Config.Jira != nil {
		return c.account.Config.Jira.Deployment
	}
	return api.JiraCloud
}

// Site is the site's rows; the user is the listing's once it answered.
func (c *Controller) Site() jira.SiteInfo {
	var user *api.SiteUser
	if c.listing != nil {
		user = &c.listing.User
	}
	return jira.SettingsSite(c.account.Config, user, c.tr)
}

func (c *Controller) storedSpaces() []api.SpaceRef {
	if c.account.Config.Jira == nil {
		return nil
	}
	return c.account.Config.Jira.Spaces
}

func (c *Controller) listedSpaces() []api.Space {
	if c.listing == nil {
		return nil
	}
	return c.listing.Spaces
}

func (c *Controller) listedStatuses() []api.IssueStatus {
	if c.listing == nil {
		return nil
	}
	return c.listing.Statuses
}

// SpaceRows are the spaces to choose from.
func (c *Controller) SpaceRows() []jira.SpaceRow {
	return jira.SettingsSpaceRows(c.storedSpaces(), c.listedSpaces(), c.tr)
}

// SelectedSpaces are the ids of the chosen spaces.
func (c *Controller) SelectedSpaces() map[string]bool {
	out := make(map[string]bool, len(c.form.Spaces))
	for _, ref := range c.form.Spaces {
		out[ref.ID] = true
	}
	return out
}

// SpacesProblem is why the chosen spaces cannot be saved; "" when they
// can.
func (c *Controller) SpacesProblem() string { return jira.SpacesProblem(len(c.form.Spaces), c.tr) }

// OfflineLabels are the labels of the offline window's choices.
func (c *Controller) OfflineLabels() []string { return jira.OfflineChoiceLabels(c.tr) }

// OfflineIndex is the offline window's choice shown.
func (c *Controller) OfflineIndex() int { return jira.IndexOfOfflineDays(c.form.OfflineDays) }

// FolderShown reports whether the view v is switched on.
func (c *Controller) FolderShown(v api.VirtualFolder) bool {
	return jira.FolderShown(c.form.DisabledFolders, v)
}

// StatusGroups is the picker of the closed statuses.
func (c *Controller) StatusGroups() []jira.StatusGroup {
	return jira.StatusGroups(c.listedStatuses(), c.form.ClosedStatuses, c.tr)
}

// StatusesProblem is why the chosen statuses cannot be saved; "" when
// they can.
func (c *Controller) StatusesProblem() string {
	return jira.StatusesProblem(c.form.ClosedStatuses, c.tr)
}

// NotificationLabels are the labels of the notification modes.
func (c *Controller) NotificationLabels() []string { return jira.NotificationModeLabels(c.tr) }

// NotificationIndex is the notification mode shown.
func (c *Controller) NotificationIndex() int {
	return jira.IndexOfNotificationMode(c.form.NotificationMail)
}

// NotificationHint is the text under the mode.
func (c *Controller) NotificationHint() string {
	return jira.NotificationHint(c.form.NotificationMail, c.tr)
}

// SendersEditable reports whether the senders matter in the mode shown.
func (c *Controller) SendersEditable() bool { return jira.SendersEditable(c.form.NotificationMail) }

// SendersPlaceholder is what an empty list of senders stands for.
func (c *Controller) SendersPlaceholder() string { return jira.DefaultSenders(c.account.Config) }

// Entries are the entries of a list.
func (c *Controller) Entries(kind jira.ListKind) []string {
	switch kind {
	case jira.ListBotNames:
		return c.form.BotNames
	case jira.ListMetadataFilters:
		return c.form.MetadataFilters
	case jira.ListAuthorPrefixes:
		return c.form.AuthorPrefixes
	case jira.ListSenders:
		return c.form.NotificationSenders
	}
	return nil
}

// Problem is why the entry typed last was not added to the list; "" for
// none.
func (c *Controller) Problem(kind jira.ListKind) string { return c.problems[kind] }

// Suggestions are the entries offered for a list with one click.
func (c *Controller) Suggestions(kind jira.ListKind) []jira.Suggestion {
	return jira.Suggestions(kind, c.Entries(kind), c.tr)
}

// IsChanged reports a form that differs from the stored account in what
// the daemon acts on.
func (c *Controller) IsChanged() bool {
	return jira.Changed(c.account.Config, c.form.Apply(c.account.Config))
}

// CanSave reports Save offered: nothing is being saved and the form can
// be.
func (c *Controller) CanSave() bool {
	return !c.saving && c.form.SettingsProblem(c.tr) == ""
}

// Lifecycle

// Start delivers the initial state and asks for the spaces and statuses.
// Call it once, after setting the callbacks.
func (c *Controller) Start() {
	c.changed()
	c.load()
}

// Close: the page went away, every late reply is dropped from now on.
func (c *Controller) Close() {
	c.closed = true
	c.progress = ""
	c.saving = false
}

// Inputs from the UI

// SetName takes the account's name as typed.
func (c *Controller) SetName(name string) {
	if name == c.form.Name {
		return
	}
	c.form.Name = name
	c.edited()
}

// SetSpace takes a space's check box.
func (c *Controller) SetSpace(id string, on bool) {
	if c.saving || on == c.SelectedSpaces()[id] {
		return
	}
	known := false
	for _, row := range c.SpaceRows() {
		known = known || row.ID == id
	}
	if !known {
		return
	}
	c.form.Spaces = jira.SetSpaceSelected(c.form.Spaces, c.storedSpaces(), c.listedSpaces(), id, on)
	c.edited()
}

// SetOfflineIndex takes the offline window's choice, an index of
// jira.OfflineChoices.
func (c *Controller) SetOfflineIndex(i int) {
	if c.saving || i < 0 || i >= len(jira.OfflineChoices) || jira.OfflineChoices[i] == c.form.OfflineDays {
		return
	}
	c.form.OfflineDays = jira.OfflineChoices[i]
	c.edited()
}

// SetOnlyMine takes "Only Issues Involving Me".
func (c *Controller) SetOnlyMine(on bool) {
	if c.saving || on == c.form.OnlyMine {
		return
	}
	c.form.OnlyMine = on
	c.edited()
}

// SetShowEvents takes "Show Status and Assignee Changes".
func (c *Controller) SetShowEvents(on bool) {
	if c.saving || on == c.form.ShowEvents {
		return
	}
	c.form.ShowEvents = on
	c.edited()
}

// SetFolder takes the switch of a view.
func (c *Controller) SetFolder(v api.VirtualFolder, shown bool) {
	known := false
	for _, f := range jira.VirtualFolders {
		known = known || f == v
	}
	if c.saving || !known || shown == c.FolderShown(v) {
		return
	}
	c.form.DisabledFolders = jira.SetFolderShown(c.form.DisabledFolders, v, shown)
	c.edited()
}

// SetStatus takes a check box of the picker of the closed statuses.
func (c *Controller) SetStatus(choice jira.StatusChoice, on bool) {
	if c.saving {
		return
	}
	c.form.ClosedStatuses = jira.SetStatusSelected(c.listedStatuses(), c.form.ClosedStatuses, choice, on)
	c.edited()
}

// SetNotificationIndex takes the notification mode's choice, an index of
// jira.NotificationModes.
func (c *Controller) SetNotificationIndex(i int) {
	if c.saving || i < 0 || i >= len(jira.NotificationModes) || jira.NotificationModes[i] == c.form.NotificationMail {
		return
	}
	c.form.NotificationMail = jira.NotificationModes[i]
	c.edited()
}

// AddEntry adds what the user typed to a list (jira.CheckEntry). It
// reports whether the field may be emptied: the entry was added, or there
// was nothing to add. Otherwise Problem(kind) says why not.
func (c *Controller) AddEntry(kind jira.ListKind, text string) bool {
	if c.saving {
		return false
	}
	entry, problem := jira.CheckEntry(kind, text, c.Entries(kind), c.tr)
	if entry == "" {
		if problem == "" {
			delete(c.problems, kind)
		} else {
			c.problems[kind] = problem
		}
		c.changed()
		return problem == ""
	}
	delete(c.problems, kind)
	c.setEntries(kind, append(append([]string(nil), c.Entries(kind)...), entry))
	c.edited()
	return true
}

// AddSuggestion adds a suggested entry (Suggestions).
func (c *Controller) AddSuggestion(kind jira.ListKind, value string) {
	for _, s := range c.Suggestions(kind) {
		if s.Value == value {
			c.AddEntry(kind, value)
			return
		}
	}
}

// RemoveEntry removes the entry at index i of a list.
func (c *Controller) RemoveEntry(kind jira.ListKind, i int) {
	list := c.Entries(kind)
	if c.saving || i < 0 || i >= len(list) {
		return
	}
	rest := append(append([]string(nil), list[:i]...), list[i+1:]...)
	delete(c.problems, kind)
	c.setEntries(kind, rest)
	c.edited()
}

// EntryTyped: the field of a list changed, so its problem belongs to what
// was typed before.
func (c *Controller) EntryTyped(kind jira.ListKind) {
	if c.problems[kind] == "" {
		return
	}
	delete(c.problems, kind)
	c.changed()
}

// ReplaceToken is "Replace Token…": the UI opens the account assistant.
func (c *Controller) ReplaceToken() {
	if c.closed || c.saving {
		return
	}
	if c.OnReplaceToken != nil {
		c.OnReplaceToken(c.account)
	}
}

// TokenReplaced: the account assistant stored a new token, so the spaces
// and statuses are asked for again with it. The form keeps what was
// edited.
func (c *Controller) TokenReplaced() {
	if c.closed || c.saving {
		return
	}
	c.load()
}

// Save is account.update with the form, or nothing (OnClose) when the
// form changes nothing.
func (c *Controller) Save() {
	if c.closed || c.saving {
		return
	}
	if problem := c.form.SettingsProblem(c.tr); problem != "" {
		c.saveProblem = problem
		c.showBanner()
		return
	}
	cfg := c.form.Apply(c.account.Config)
	if !jira.Changed(c.account.Config, cfg) {
		if c.OnClose != nil {
			c.OnClose()
		}
		return
	}
	c.saveProblem = ""
	c.showBanner()
	c.saving = true
	c.setBusy(c.Texts.Saving)
	c.changed()
	c.op++
	op := c.op
	id := c.account.ID
	// Empty credentials: the daemon keeps the stored token.
	params := api.AccountUpdateParams{AccountID: id, Config: cfg, Credentials: api.Credentials{}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		err := c.caller.Call(ctx, api.MethodAccountUpdate, params, &api.AccountUpdateResult{})
		c.post(func() {
			if c.closed || op != c.op {
				return
			}
			c.saving = false
			c.setBusy("")
			if err != nil {
				c.saveProblem = c.failure(jira.StepSave, err)
				c.showBanner()
				c.changed()
				return
			}
			c.log.Info("jira account saved", "account", string(id))
			if c.OnDone != nil {
				c.OnDone(id, cfg)
			}
		})
	}()
}

// Internals

func (c *Controller) setEntries(kind jira.ListKind, list []string) {
	switch kind {
	case jira.ListBotNames:
		c.form.BotNames = list
	case jira.ListMetadataFilters:
		c.form.MetadataFilters = list
	case jira.ListAuthorPrefixes:
		c.form.AuthorPrefixes = list
	case jira.ListSenders:
		c.form.NotificationSenders = list
	}
}

// edited: the form changed, so what the last Save said is about another
// form.
func (c *Controller) edited() {
	c.saveProblem = ""
	c.showBanner()
	c.changed()
}

func (c *Controller) changed() {
	if c.OnChange != nil {
		c.OnChange()
	}
}

func (c *Controller) setBusy(text string) {
	if c.progress == text {
		return
	}
	c.progress = text
	if c.OnBusy != nil {
		c.OnBusy(text)
	}
}

// showBanner tells the UI the banner when it changed.
func (c *Controller) showBanner() {
	text := c.Banner()
	if c.shownBanner == text {
		return
	}
	c.shownBanner = text
	if c.OnBanner != nil {
		c.OnBanner(text)
	}
}

// failure is the banner of a failed call: the step's own sentence
// (jira.FailureOf: a refused or missing token, an account that exists
// already), else the client's for the error. Only the class is logged:
// the error may carry text from the site.
func (c *Controller) failure(step jira.Step, err error) string {
	class := jira.Classify(err)
	c.log.Info("jira account step failed", "step", int(step), "class", int(class))
	f := jira.FailureOf(step, class, c.Deployment(), true, c.tr)
	if f.Banner != "" {
		return f.Banner
	}
	return c.errorText(f.What, err)
}

// load is account.listSpaces for the stored account with its stored
// token.
func (c *Controller) load() {
	params := api.AccountListSpacesParams{
		AccountID: c.account.ID, Config: c.account.Config, Credentials: api.Credentials{}, Counts: false,
	}
	c.loadProblem = ""
	c.showBanner()
	c.setBusy(c.Texts.Loading)
	c.op++
	op := c.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), listSpacesTimeout)
		defer cancel()
		var res api.AccountListSpacesResult
		err := c.caller.Call(ctx, api.MethodAccountListSpaces, params, &res)
		c.post(func() {
			if c.closed || op != c.op {
				return
			}
			c.setBusy("")
			if err != nil {
				// The page edits what is stored.
				c.loadProblem = c.failure(jira.StepSpaces, err)
				c.showBanner()
			} else {
				c.log.Info("jira account listed", "spaces", len(res.Spaces), "statuses", len(res.Statuses))
				c.listing = &res
			}
			c.changed()
		})
	}()
}
