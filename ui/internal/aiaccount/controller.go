// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package aiaccount is the dialog that adds or edits an AI provider
// account (kind ai, docs/api.md §4.1; the design is docs/plans/ai-api.md
// §7.1): the provider (fixed: the catalogue entry on Preferences →
// Integrations chose it, and an existing account keeps its own), the
// name, the server address of an OpenAI-compatible server with the
// examples under it and a warning for http outside a loopback address
// (D8), the API key (write-only: an existing account shows "Key stored ·
// Replace Key…"), Test (account.test, which lists the provider's models
// and says what went wrong), the model to pick from them or type, the
// board's model, the certificate pin of an https server, and the consent
// to sending mail to the provider (D7, Q17), which Save needs.
//
// The texts are text.go, the failures failure.go; the calls, the form
// and the checks are the Controller, plain Go behind Caller so that its
// tests run without a display or a daemon; macOS and Windows port it
// with its tests (the model is ui/internal/jiraaccount and the eWay-CRM
// flow of ui/internal/accountwizard). The widgets are the GTK dialog
// over it.
package aiaccount

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/certtrust"
)

// RPC budgets (docs/api.md §4.1): account.test asks the provider for its
// models once; account.add does the same and stores the key, so the
// keyring may prompt; assistant.providerModels answers from the daemon's
// cache or asks once.
const (
	testTimeout   = 45 * time.Second
	saveTimeout   = 60 * time.Second
	modelsTimeout = 30 * time.Second
	// maxModelBytes is the daemon's limit of a model id (docs/api.md
	// §4.1); maxBaseURLBytes the one of baseUrl.
	maxModelBytes   = 256
	maxBaseURLBytes = 2048
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
	Tr Translator
	// ErrorText is the client's general sentence for a failed call, with
	// the step's action ("Testing the connection") as what.
	ErrorText func(what string, err error) string
	// Post runs f on the main loop, where every method of the controller
	// and every callback runs; replies come back through it.
	Post func(f func())
	// Log may be nil. Nothing typed in the dialog is ever logged.
	Log *slog.Logger
}

// Form is the dialog's fields as typed. Key is the API key field, empty
// when nothing was typed (an existing account then keeps its stored key);
// CertificateSHA256 the pin of an https server; Consent the check box.
type Form struct {
	Name, BaseURL, Key, Model, BoardModel string
	CertificateSHA256                     string
	Consent                               bool
}

// TestOutcome is what the last Test said: whether the provider answered,
// the status line, and the TLS problem of a failed https connection whose
// certificate the user may trust (nil otherwise).
type TestOutcome struct {
	OK    bool
	Text  string
	Trust *certtrust.Problem
}

// Controller is the dialog with the widgets replaced by callbacks.
//
// Everything typed is the form until Save, which is account.add (a new
// account, with the key as credentials.password and the consent) or
// account.update (the edited account, with credentials only when a new
// key was typed, and the consent only when it was given now). Test is
// account.test with the form, or with the account's stored key when
// editing without a replacement. The form is checked here, by the
// daemon's rules, before any call. Every reply is dropped once the
// dialog closed or another call started since (op).
//
// Every method must be called, and every callback runs, on the main loop.
// Set the callbacks, then call Start.
type Controller struct {
	// OnChange: what the dialog shows changed (the form's rules, the
	// models, the test's outcome, a problem); the UI reads the controller
	// again.
	OnChange func()
	// OnBusy: a call started, with its progress text, or finished ("").
	OnBusy func(progress string)
	// OnBanner: the dialog's banner; "" hides it.
	OnBanner func(text string)
	// OnProblem: the field to flag and focus; FieldNone clears the flag.
	OnProblem func(Field)
	// OnDone: the account was stored; the UI closes the dialog.
	OnDone func(id api.AccountID, cfg api.AccountConfig)
	// OnClose: Save had nothing to store; the UI closes the dialog.
	OnClose func()

	// Texts are the fixed texts; Consent the consent of the provider.
	Texts   Strings
	Consent ConsentStrings

	caller    Caller
	tr        Translator
	errorText func(what string, err error) string
	post      func(func())
	log       *slog.Logger

	provider api.AIProvider
	// editing is the account being edited; nil when adding one.
	editing *api.Account
	form    Form
	// replacing: an existing account's key field is open (Replace Key…).
	replacing bool
	// models are the provider's models: the last test's, else what
	// assistant.providerModels listed for an existing account.
	models []api.AIModel
	test   *TestOutcome

	progress string
	saving   bool
	closed   bool
	op       int

	problem     Field
	banner      string
	shownBanner string
}

// NewController builds the controller of the dialog: adding an account
// of provider p when editing is nil, else editing that account (its
// provider wins over p).
func NewController(c Caller, p api.AIProvider, editing *api.Account, env Env) *Controller {
	log := env.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctl := &Controller{
		Texts:     Texts(env.Tr),
		caller:    c,
		tr:        env.Tr,
		errorText: env.ErrorText,
		post:      env.Post,
		log:       log,
		provider:  p,
	}
	if editing != nil {
		a := *editing
		ctl.editing = &a
		ctl.form.Name = a.Config.Name
		if ai := a.Config.AI; ai != nil {
			ctl.provider = ai.Provider
			ctl.form.BaseURL = ai.BaseURL
			ctl.form.Model = ai.Model
			ctl.form.BoardModel = ai.BoardModel
			ctl.form.CertificateSHA256 = ai.CertificateSHA256
		}
	}
	if !ctl.provider.Valid() {
		ctl.provider = api.AIOpenAICompatible
	}
	ctl.Consent = ConsentTexts(env.Tr, ctl.provider)
	return ctl
}

// Presentation

// IsEditing reports the dialog editing an existing account.
func (c *Controller) IsEditing() bool { return c.editing != nil }

// Account is the account being edited; nil when adding one.
func (c *Controller) Account() *api.Account { return c.editing }

// Provider is the provider kind.
func (c *Controller) Provider() api.AIProvider { return c.provider }

// ProviderName names the provider kind.
func (c *Controller) ProviderName() string { return assistant.ProviderName(c.tr, c.provider) }

// Title is the dialog's title.
func (c *Controller) Title() string {
	if c.IsEditing() {
		return c.Texts.EditTitle
	}
	return c.Texts.AddTitle
}

// SaveLabel is the primary button: Add Account, or Save when editing.
func (c *Controller) SaveLabel() string {
	if c.IsEditing() {
		return c.Texts.Save
	}
	return c.Texts.Add
}

// Form is the fields as typed.
func (c *Controller) Form() Form { return c.form }

// UsesServer reports a provider with a server address of its own (the
// OpenAI-compatible server); the named providers have none.
func (c *Controller) UsesServer() bool { return c.provider == api.AIOpenAICompatible }

// KeyOptional reports a provider that may have no key (a local server).
func (c *Controller) KeyOptional() bool { return c.provider == api.AIOpenAICompatible }

// KeyStored reports the key row of an existing account showing "Key
// stored" with Replace Key…, instead of the field.
func (c *Controller) KeyStored() bool { return c.IsEditing() && !c.replacing }

// KeyLabel is the key field's label: New API Key while replacing, API
// Key (optional) for a server, else API Key.
func (c *Controller) KeyLabel() string {
	switch {
	case c.IsEditing():
		return c.Texts.NewKeyLabel
	case c.KeyOptional():
		return c.Texts.KeyOptionalLabel
	}
	return c.Texts.KeyLabel
}

// Models are the provider's models to pick from: the last test's, else
// what the daemon listed for an existing account; nil before either.
func (c *Controller) Models() []api.AIModel { return c.models }

// Test is what the last Test said; nil before one, and again once the
// server address or the key changed.
func (c *Controller) Test() *TestOutcome { return c.test }

// TrustOffer is the TLS problem of the last test whose certificate the
// dialog may offer to trust (an https server whose certificate was
// refused or changed, certtrust.Problem.Category Certificate or
// Changed); false otherwise. Trust takes it.
func (c *Controller) TrustOffer() (certtrust.Problem, bool) {
	if c.test == nil || c.test.Trust == nil {
		return certtrust.Problem{}, false
	}
	return *c.test.Trust, true
}

// Pinned reports a certificate pin in the form.
func (c *Controller) Pinned() bool { return c.form.CertificateSHA256 != "" }

// PinText is the pinned fingerprint as shown ("AB12 CD34 …").
func (c *Controller) PinText() string { return certtrust.FormatFingerprint(c.form.CertificateSHA256) }

// Host is the host of the address the account talks to: the provider's
// fixed one, or the server's as typed ("" while it is not an address).
func (c *Controller) Host() string {
	if !c.UsesServer() {
		return c.fixedHost()
	}
	u, ok := NormalizeBaseURL(c.form.BaseURL)
	if !ok {
		return ""
	}
	p, _ := url.Parse(u)
	return p.Hostname()
}

func (c *Controller) fixedHost() string {
	u, _ := url.Parse(api.AIConfig{Provider: c.provider}.EffectiveBaseURL())
	if u == nil {
		return ""
	}
	return u.Hostname()
}

// HTTPWarning reports the warning under the server address: plain http
// to a host that is not a loopback address (docs/plans/ai-api.md D8).
func (c *Controller) HTTPWarning() bool {
	if !c.UsesServer() {
		return false
	}
	u, ok := NormalizeBaseURL(c.form.BaseURL)
	if !ok {
		return false
	}
	p, _ := url.Parse(u)
	return p.Scheme == "http" && !IsLoopback(p.Hostname())
}

// HTTPWarningText is the warning's text; "" when there is none.
func (c *Controller) HTTPWarningText() string {
	if !c.HTTPWarning() {
		return ""
	}
	return HTTPWarning(c.tr, c.Host())
}

// NeedsConsent reports the consent check box offered: a new account, or
// an existing one without the current consent text's version.
func (c *Controller) NeedsConsent() bool {
	return c.editing == nil || c.editing.Config.AI == nil || !c.editing.Config.AI.Consented()
}

// ConsentBody is the consent's text, with the http warning added for a
// server reached over plain http.
func (c *Controller) ConsentBody() string {
	body := c.Consent.Body()
	if w := c.HTTPWarningText(); w != "" {
		body += " " + w
	}
	return body
}

// Banner is the banner shown, "" for none.
func (c *Controller) Banner() string { return c.banner }

// Problem is the field flagged, FieldNone for none.
func (c *Controller) Problem() Field { return c.problem }

// Progress is the progress text of the call under way; "" when none runs.
func (c *Controller) Progress() string { return c.progress }

// Busy reports a call under way.
func (c *Controller) Busy() bool { return c.progress != "" }

// Saving reports account.add or account.update under way: the dialog
// waits.
func (c *Controller) Saving() bool { return c.saving }

// Closed reports a dialog that went away.
func (c *Controller) Closed() bool { return c.closed }

// CanTest reports Test offered: no call runs.
func (c *Controller) CanTest() bool { return !c.Busy() }

// CanSave reports Save offered: no call runs.
func (c *Controller) CanSave() bool { return !c.Busy() }

// IsChanged reports a form that differs from the stored account in what
// the daemon acts on: the name, the server, the models, the pin, a new
// key, or a consent given now. Always true when adding.
func (c *Controller) IsChanged() bool {
	if c.editing == nil {
		return true
	}
	if c.replacing && c.form.Key != "" {
		return true
	}
	if c.NeedsConsent() && c.form.Consent {
		return true
	}
	old := c.editing.Config
	cfg := c.config()
	if strings.TrimSpace(old.Name) != cfg.Name {
		return true
	}
	if old.AI == nil {
		return true
	}
	a, b := *old.AI, *cfg.AI
	a.Consent, b.Consent = nil, nil
	return a != b
}

// Check is the first problem of the form by the daemon's rules
// (docs/api.md §4.1), or FieldNone and "": the name, the server address
// (with https for a pin), the key (needed for a named provider, when
// adding or replacing), the models, and for Save the consent.
func (c *Controller) Check(forSave bool) (Field, string) {
	s := c.Texts
	if strings.TrimSpace(c.form.Name) == "" {
		return FieldName, s.NameMissing
	}
	if c.UsesServer() {
		raw := strings.TrimSpace(c.form.BaseURL)
		if raw == "" {
			return FieldServer, s.ServerMissing
		}
		u, ok := NormalizeBaseURL(raw)
		if !ok {
			return FieldServer, s.ServerInvalid
		}
		if c.form.CertificateSHA256 != "" && !strings.HasPrefix(u, "https://") {
			return FieldServer, s.PinNeedsHTTPS
		}
	}
	if !c.KeyOptional() && (!c.IsEditing() || c.replacing) && strings.TrimSpace(c.form.Key) == "" {
		return FieldKey, s.KeyMissing
	}
	switch checkModel(c.form.Model) {
	case modelMissing:
		return FieldModel, s.ModelMissing
	case modelInvalid:
		return FieldModel, s.ModelInvalid
	}
	if strings.TrimSpace(c.form.BoardModel) != "" && checkModel(c.form.BoardModel) == modelInvalid {
		return FieldBoardModel, s.ModelInvalid
	}
	if forSave && c.NeedsConsent() && !c.form.Consent {
		return FieldConsent, s.ConsentMissing
	}
	return FieldNone, ""
}

// Lifecycle

// Start delivers the initial state and, for an existing account, asks
// the daemon for the provider's models. Call it once, after setting the
// callbacks.
func (c *Controller) Start() {
	c.changed()
	if c.editing != nil {
		c.loadModels()
	}
}

// Close: the dialog went away, every late reply is dropped from now on.
func (c *Controller) Close() {
	c.closed = true
	c.progress = ""
	c.saving = false
}

// Inputs from the UI

// SetName takes the account's name as typed.
func (c *Controller) SetName(v string) {
	if c.saving || v == c.form.Name {
		return
	}
	c.form.Name = v
	c.edited(FieldName)
}

// SetBaseURL takes the server address as typed; the last test is about
// another server now, and a pin of another host goes.
func (c *Controller) SetBaseURL(v string) {
	if c.saving || v == c.form.BaseURL {
		return
	}
	oldHost, newHost := hostOf(c.form.BaseURL), hostOf(v)
	c.form.BaseURL = v
	if c.form.CertificateSHA256 != "" && oldHost != newHost {
		c.form.CertificateSHA256 = ""
	}
	c.test = nil
	c.edited(FieldServer)
}

// SetKey takes the key as typed; the last test is about another key now.
func (c *Controller) SetKey(v string) {
	if c.saving || v == c.form.Key {
		return
	}
	c.form.Key = v
	c.test = nil
	c.edited(FieldKey)
}

// SetModel takes the model as typed or picked.
func (c *Controller) SetModel(v string) {
	if c.saving || v == c.form.Model {
		return
	}
	c.form.Model = v
	c.edited(FieldModel)
}

// SetBoardModel takes the board's model as typed or picked.
func (c *Controller) SetBoardModel(v string) {
	if c.saving || v == c.form.BoardModel {
		return
	}
	c.form.BoardModel = v
	c.edited(FieldBoardModel)
}

// SetConsent takes the consent check box.
func (c *Controller) SetConsent(on bool) {
	if c.saving || on == c.form.Consent {
		return
	}
	c.form.Consent = on
	c.edited(FieldConsent)
}

// ReplaceKey is Replace Key… of an existing account: the key field opens,
// empty.
func (c *Controller) ReplaceKey() {
	if c.saving || !c.IsEditing() || c.replacing {
		return
	}
	c.replacing = true
	c.form.Key = ""
	c.test = nil
	c.edited(FieldKey)
}

// KeepKey takes a replacement back: the stored key stays, the field
// closes.
func (c *Controller) KeepKey() {
	if c.saving || !c.replacing {
		return
	}
	c.replacing = false
	c.form.Key = ""
	c.test = nil
	c.edited(FieldKey)
}

// Trust pins the certificate the last test refused (TrustOffer), after
// the user confirmed it with its fingerprint.
func (c *Controller) Trust() {
	p, ok := c.TrustOffer()
	if c.saving || !ok || p.Cert == nil {
		return
	}
	c.form.CertificateSHA256 = p.Cert.SHA256
	c.test = nil
	c.edited(FieldServer)
}

// ForgetPin drops the pin.
func (c *Controller) ForgetPin() {
	if c.saving || c.form.CertificateSHA256 == "" {
		return
	}
	c.form.CertificateSHA256 = ""
	c.test = nil
	c.edited(FieldServer)
}

// RunTest is account.test with the form: the provider's models and the
// status. An existing account without a replaced key is tested with its
// stored key.
func (c *Controller) RunTest() {
	if c.closed || c.Busy() {
		return
	}
	if field, problem := c.Check(false); field != FieldNone {
		c.fail(field, problem)
		return
	}
	c.test = nil
	c.clearProblem()
	params := api.AccountTestParams{Config: c.config(), Credentials: c.credentials()}
	if c.IsEditing() {
		params.AccountID = c.editing.ID
	}
	c.setBusy(c.Texts.Testing)
	c.changed()
	c.op++
	op := c.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var res api.AccountTestResult
		err := c.caller.Call(ctx, api.MethodAccountTest, params, &res)
		c.post(func() {
			if c.closed || op != c.op {
				return
			}
			c.setBusy("")
			c.tested(res.AI, err)
			c.changed()
		})
	}()
}

// tested reads what account.test answered.
func (c *Controller) tested(res *api.AITestResult, err error) {
	if err == nil && res != nil && res.OK {
		c.models = res.Models
		c.test = &TestOutcome{OK: true, Text: c.Texts.Connected(len(res.Models))}
		c.log.Info("ai account tested", "models", len(res.Models))
		return
	}
	if err == nil {
		if res == nil || res.Error == nil {
			c.test = &TestOutcome{Text: c.Texts.Unreachable}
			return
		}
		err = res.Error
	}
	f := c.failure(c.Texts.Testing, err)
	out := &TestOutcome{Text: f.Banner}
	if p, ok := c.trustable(err); ok {
		out.Trust = &p
	}
	c.test = out
	c.flag(f.Field)
}

// trustable is the TLS problem of a failed test the dialog may offer to
// trust: an https server (only its host may be pinned), a certificate the
// system refused or one other than the pinned (never a connection
// failure), and not the pinned one.
func (c *Controller) trustable(err error) (certtrust.Problem, bool) {
	var e *api.Error
	if !c.UsesServer() || !asAPIError(err, &e) {
		return certtrust.Problem{}, false
	}
	u, ok := NormalizeBaseURL(c.form.BaseURL)
	if !ok || !strings.HasPrefix(u, "https://") {
		return certtrust.Problem{}, false
	}
	p, ok := certtrust.Details(e)
	if !ok || p.Cert == nil || p.Category() == certtrust.Connection {
		return certtrust.Problem{}, false
	}
	if pinned, ok := api.NormalizeCertificateSHA256(c.form.CertificateSHA256); ok && pinned == p.Cert.SHA256 {
		return certtrust.Problem{}, false
	}
	return p, true
}

// Save is account.add with the form, the key and the consent, or
// account.update of the edited account (credentials only with a new key,
// the consent only when given now); nothing (OnClose) when an edited
// form changes nothing.
func (c *Controller) Save() {
	if c.closed || c.Busy() {
		return
	}
	if field, problem := c.Check(true); field != FieldNone {
		c.fail(field, problem)
		return
	}
	cfg := c.config()
	if c.NeedsConsent() && c.form.Consent {
		cfg.AI.Consent = &api.AIConsent{Version: api.AssistantConsentVersion}
	}
	if c.IsEditing() && !c.IsChanged() {
		if c.OnClose != nil {
			c.OnClose()
		}
		return
	}
	c.clearProblem()
	c.saving = true
	c.changed()
	c.op++
	op := c.op
	editing := c.editing
	creds := c.credentials()
	var what string
	if editing == nil {
		what = c.Texts.Adding
	} else {
		what = c.Texts.Saving
	}
	c.setBusy(what)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()
		var id api.AccountID
		var err error
		if editing == nil {
			var res api.AccountAddResult
			err = c.caller.Call(ctx, api.MethodAccountAdd, api.AccountAddParams{Config: cfg, Credentials: creds}, &res)
			id = res.AccountID
		} else {
			id = editing.ID
			err = c.caller.Call(ctx, api.MethodAccountUpdate,
				api.AccountUpdateParams{AccountID: id, Config: cfg, Credentials: creds}, &api.AccountUpdateResult{})
		}
		c.post(func() {
			if c.closed || op != c.op {
				return
			}
			c.saving = false
			c.setBusy("")
			if err != nil {
				f := c.failure(what, err)
				c.fail(f.Field, f.Banner)
				c.changed()
				return
			}
			// The key is write-only: it leaves the form once stored.
			c.form.Key = ""
			c.replacing = false
			c.log.Info("ai account saved", "account", string(id), "provider", string(c.provider))
			if c.OnDone != nil {
				c.OnDone(id, cfg)
			}
		})
	}()
}

// Internals

// config is the account configuration of the form: the edited account's
// with the name and the AI part replaced, or a new one.
func (c *Controller) config() api.AccountConfig {
	var cfg api.AccountConfig
	if c.editing != nil {
		cfg = c.editing.Config
	}
	cfg.Kind = api.AccountAI
	cfg.Name = strings.TrimSpace(c.form.Name)
	ai := api.AIConfig{
		Provider:   c.provider,
		Model:      strings.TrimSpace(c.form.Model),
		BoardModel: strings.TrimSpace(c.form.BoardModel),
	}
	if c.UsesServer() {
		if u, ok := NormalizeBaseURL(c.form.BaseURL); ok {
			ai.BaseURL = u
		}
		ai.CertificateSHA256 = c.form.CertificateSHA256
	}
	if c.editing != nil && c.editing.Config.AI != nil {
		ai.Consent = c.editing.Config.AI.Consent
	}
	cfg.AI = &ai
	return cfg
}

// credentials is the key as typed when it goes with the call: always
// when adding, when editing only as a replacement.
func (c *Controller) credentials() api.Credentials {
	if c.IsEditing() && !c.replacing {
		return api.Credentials{}
	}
	return api.Credentials{Password: strings.TrimSpace(c.form.Key)}
}

// loadModels is assistant.providerModels for the edited account, so that
// the model row has the provider's models without a test; a failure
// leaves the row to the typed id.
func (c *Controller) loadModels() {
	c.op++
	op := c.op
	params := api.AssistantProviderModelsParams{AccountID: c.editing.ID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
		defer cancel()
		var res api.AssistantProviderModelsResult
		err := c.caller.Call(ctx, api.MethodAssistantProviderModels, params, &res)
		c.post(func() {
			if c.closed || op != c.op || err != nil || c.test != nil {
				return
			}
			c.models = res.Models
			c.changed()
		})
	}()
}

// failure is the banner and field of a failed call: the dialog's own
// sentence (FailureOf), else the client's for the error. Only the code
// is logged: the error may carry text from the provider.
func (c *Controller) failure(what string, err error) Failure {
	var e *api.Error
	code := api.ErrorCode(0)
	if asAPIError(err, &e) {
		code = e.Code
	}
	c.log.Info("ai account call failed", "what", what, "code", int(code))
	f := FailureOf(err, c.tr)
	if f.Banner == "" {
		f.Banner = c.errorText(what, err)
	}
	if f.Field == FieldServer && !c.UsesServer() {
		f.Field = FieldNone
	}
	return f
}

// fail shows a problem: the banner and the flagged field.
func (c *Controller) fail(field Field, banner string) {
	c.banner = banner
	c.flag(field)
	c.showBanner()
	c.changed()
}

// edited: a field changed, so the problem of that field and the banner
// are about another form.
func (c *Controller) edited(field Field) {
	if c.problem == field || c.banner != "" {
		c.clearProblem()
	}
	c.changed()
}

func (c *Controller) clearProblem() {
	c.banner = ""
	c.flag(FieldNone)
	c.showBanner()
}

func (c *Controller) flag(field Field) {
	if c.problem == field {
		return
	}
	c.problem = field
	if c.OnProblem != nil {
		c.OnProblem(field)
	}
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
	if c.shownBanner == c.banner {
		return
	}
	c.shownBanner = c.banner
	if c.OnBanner != nil {
		c.OnBanner(c.banner)
	}
}

// The address and model rules of the daemon (docs/api.md §4.1).

// NormalizeBaseURL is the server address as the daemon stores it: http or
// https, a host, no user info, query or fragment, the host lower case, no
// trailing slash, at most 2048 bytes; false for anything else (an empty
// address, another scheme, a space or control character, no host).
func NormalizeBaseURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxBaseURLBytes {
		return "", false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\\' {
			return "", false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	out := scheme + "://" + host + path
	if len(out) > maxBaseURLBytes {
		return "", false
	}
	return out, true
}

// hostOf is the host of an address as typed; "" when it is none.
func hostOf(raw string) string {
	u, ok := NormalizeBaseURL(raw)
	if !ok {
		return ""
	}
	p, _ := url.Parse(u)
	return p.Hostname()
}

// IsLoopback reports a host that is this computer: localhost, a name
// under .localhost, or a loopback IP address (127.0.0.0/8, ::1).
func IsLoopback(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

type modelCheck int

const (
	modelOK modelCheck = iota
	modelMissing
	modelInvalid
)

// checkModel checks a model id as the daemon does: not empty, at most
// 256 bytes, no control characters.
func checkModel(raw string) modelCheck {
	s := strings.TrimSpace(raw)
	if s == "" {
		return modelMissing
	}
	if len(s) > maxModelBytes || !utf8.ValidString(s) {
		return modelInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return modelInvalid
		}
	}
	return modelOK
}
