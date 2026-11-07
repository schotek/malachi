// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package aiaccount

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The dialog's controller against a fake daemon: account.test,
// account.add, account.update and assistant.providerModels, decoded as
// the client decodes the daemon's replies. macOS and Windows port these
// tests with the controller. Fictional providers and keys only.

type msgids struct{}

func (msgids) T(msgid string) string { return msgid }
func (msgids) N(singular, plural string, n int) string {
	if n == 1 {
		return singular
	}
	return plural
}

func errText(what string, err error) string { return what + " failed: " + err.Error() }

type mainLoop chan func()

func (l mainLoop) post(f func()) { l <- f }

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

type fakeDaemon struct {
	mu       sync.Mutex
	handlers map[string]func() (string, error)
	calls    []string
	params   map[string][]json.RawMessage
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{handlers: map[string]func() (string, error){}, params: map[string][]json.RawMessage{}}
}

func (d *fakeDaemon) reply(method, result string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[method] = func() (string, error) { return result, nil }
}

func (d *fakeDaemon) fail(method string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[method] = func() (string, error) { return "", err }
}

func (d *fakeDaemon) Call(_ context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.calls = append(d.calls, method)
	d.params[method] = append(d.params[method], raw)
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

func (d *fakeDaemon) callList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func lastParams[T any](t *testing.T, d *fakeDaemon, method string) T {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	sent := d.params[method]
	if len(sent) == 0 {
		t.Fatalf("%s was never called", method)
	}
	var p T
	if err := json.Unmarshal(sent[len(sent)-1], &p); err != nil {
		t.Fatalf("%s params: %v", method, err)
	}
	return p
}

type recorder struct {
	changes  int
	busy     []string
	banners  []string
	problems []Field
	done     []api.AccountID
	configs  []api.AccountConfig
	closed   int
}

func (r *recorder) attach(c *Controller) {
	c.OnChange = func() { r.changes++ }
	c.OnBusy = func(s string) { r.busy = append(r.busy, s) }
	c.OnBanner = func(s string) { r.banners = append(r.banners, s) }
	c.OnProblem = func(f Field) { r.problems = append(r.problems, f) }
	c.OnDone = func(id api.AccountID, cfg api.AccountConfig) {
		r.done = append(r.done, id)
		r.configs = append(r.configs, cfg)
	}
	c.OnClose = func() { r.closed++ }
}

func start(d *fakeDaemon, p api.AIProvider, editing *api.Account) (*Controller, *recorder, mainLoop) {
	loop := make(mainLoop, 16)
	c := NewController(d, p, editing, Env{Tr: msgids{}, ErrorText: errText, Post: loop.post})
	rec := &recorder{}
	rec.attach(c)
	c.Start()
	return c, rec, loop
}

const (
	testOKJSON    = `{"ai":{"ok":true,"latencyMs":80,"models":[{"id":"claude-opus-5-5","displayName":"Claude Opus 5.5"},{"id":"claude-sonnet-5"}]}}`
	testNoneJSON  = `{"ai":{"ok":true,"latencyMs":80}}`
	test401JSON   = `{"ai":{"ok":false,"error":{"code":1201,"message":"401 from the provider"}}}`
	testTLSJSON   = `{"ai":{"ok":false,"error":{"code":1303,"message":"tls","data":{"reason":"untrusted","certificate":{"sha256":"` + fingerprint + `","subject":"CN=ollama.lan"}}}}}`
	addedJSON     = `{"accountId":"acc_0123456789abcdef0123456789abcdef"}`
	modelsJSON    = `{"models":[{"id":"gpt-6.1-sol"},{"id":"gpt-6.1-mini"}]}`
	fingerprint   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fingerprintTx = "0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567 89AB CDEF"
)

func storedAccount() api.Account {
	return api.Account{
		ID: "acc_0123456789abcdef0123456789abcdef", Enabled: true,
		Config: api.AccountConfig{
			Kind: api.AccountAI, Name: "Work",
			AI: &api.AIConfig{Provider: api.AIOpenAI, Model: "gpt-6.1-sol",
				Consent: &api.AIConsent{Version: api.AssistantConsentVersion}},
		},
		Capabilities: []api.AccountCapability{api.CapabilityAssistant},
	}
}

func TestAddingANamedProviderNeedsAKey(t *testing.T) {
	d := newFakeDaemon()
	c, rec, _ := start(d, api.AIAnthropic, nil)
	if c.IsEditing() || c.Provider() != api.AIAnthropic || c.ProviderName() != "Anthropic API" || c.Title() != "Add AI Provider" ||
		c.SaveLabel() != "_Add Account" || c.UsesServer() || c.KeyOptional() || c.KeyStored() || c.KeyLabel() != "API Key" ||
		!c.NeedsConsent() || c.Host() != "api.anthropic.com" || c.HTTPWarning() {
		t.Fatalf("fresh anthropic: %+v", c)
	}
	c.SetName("Work")
	c.SetModel("claude-opus-5-5")
	if f, p := c.Check(false); f != FieldKey || p != "Enter the API key" {
		t.Errorf("check without key: %v %q", f, p)
	}
	c.RunTest()
	c.Save()
	if len(d.callList()) != 0 {
		t.Errorf("called the daemon: %v", d.callList())
	}
	if c.Banner() != "Enter the API key" || c.Problem() != FieldKey || !slices.Contains(rec.problems, FieldKey) {
		t.Errorf("banner %q problem %v", c.Banner(), c.Problem())
	}
	c.SetKey("sk-ant-test")
	if c.Banner() != "" || c.Problem() != FieldNone {
		t.Errorf("typing the key did not clear the problem: %q %v", c.Banner(), c.Problem())
	}
	if f, p := c.Check(false); f != FieldNone || p != "" {
		t.Errorf("check with key: %v %q", f, p)
	}
	if f, p := c.Check(true); f != FieldConsent || p != "Allow sending mail to the provider first" {
		t.Errorf("check for save without consent: %v %q", f, p)
	}
}

func TestTestNeedsNoModelYet(t *testing.T) {
	// The B2 finding of 2026-11-07: the Test button did nothing with an
	// empty model, while the model is chosen from what the test lists.
	d := newFakeDaemon()
	d.reply(api.MethodAccountTest, testOKJSON)
	c, _, loop := start(d, api.AIAnthropic, nil)
	c.SetName("Work")
	c.SetKey("sk-ant-test")
	if f, p := c.Check(false); f != FieldModel || p != "Enter or choose a model" {
		t.Errorf("check without a model: %v %q", f, p)
	}
	c.RunTest()
	if !c.Busy() || c.Problem() != FieldNone {
		t.Fatalf("the test did not start: busy %v problem %v banner %q", c.Busy(), c.Problem(), c.Banner())
	}
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if p := lastParams[api.AccountTestParams](t, d, api.MethodAccountTest); p.Config.AI == nil || p.Config.AI.Model != "" {
		t.Errorf("test params %+v", p.Config.AI)
	}
	if ids := modelIDs(c.Models()); !slices.Equal(ids, []string{"claude-opus-5-5", "claude-sonnet-5"}) || !c.Test().OK {
		t.Errorf("models %v outcome %+v", ids, c.Test())
	}
	// Save still needs the model; an invalid one stops the test too.
	c.SetConsent(true)
	c.Save()
	if c.Problem() != FieldModel || len(d.callList()) != 1 {
		t.Errorf("saved without a model: %v %v", c.Problem(), d.callList())
	}
	c.SetModel("bad\x00model")
	c.RunTest()
	if c.Busy() || c.Problem() != FieldModel || len(d.callList()) != 1 {
		t.Errorf("tested an invalid model: busy %v problem %v", c.Busy(), c.Problem())
	}
}

func TestTestListsTheModelsAndSaveSendsTheKeyAndTheConsent(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountTest, testOKJSON)
	d.reply(api.MethodAccountAdd, addedJSON)
	c, rec, loop := start(d, api.AIAnthropic, nil)
	c.SetName(" Work ")
	c.SetKey(" sk-ant-test ")
	c.SetModel("claude-opus-5-5")
	c.RunTest()
	if !c.Busy() || c.Progress() != "Testing the connection" || c.CanTest() || c.CanSave() {
		t.Fatalf("not testing: busy %v %q", c.Busy(), c.Progress())
	}
	loop.runUntil(t, func() bool { return c.Test() != nil })
	out := c.Test()
	if !out.OK || out.Text != "Connected · 2 models" || out.Trust != nil || c.Busy() {
		t.Errorf("test outcome %+v", out)
	}
	if ids := modelIDs(c.Models()); !slices.Equal(ids, []string{"claude-opus-5-5", "claude-sonnet-5"}) {
		t.Errorf("models %v", ids)
	}
	p := lastParams[api.AccountTestParams](t, d, api.MethodAccountTest)
	if p.AccountID != "" || p.Credentials.Password != "sk-ant-test" || p.Config.Kind != api.AccountAI || p.Config.Name != "Work" ||
		p.Config.AI == nil || p.Config.AI.Provider != api.AIAnthropic || p.Config.AI.BaseURL != "" || p.Config.AI.Model != "claude-opus-5-5" ||
		p.Config.AI.Consent != nil {
		t.Errorf("test params %+v %+v", p, p.Config.AI)
	}
	if !slices.Equal(rec.busy, []string{"Testing the connection", ""}) {
		t.Errorf("busy %v", rec.busy)
	}

	// Save without the consent is refused here; with it, account.add
	// carries the key and the consent's version.
	c.Save()
	if c.Problem() != FieldConsent || len(d.callList()) != 1 {
		t.Fatalf("saved without consent: %v %v", c.Problem(), d.callList())
	}
	c.SetConsent(true)
	if c.Problem() != FieldNone || c.Banner() != "" {
		t.Errorf("consent did not clear the problem")
	}
	c.SetBoardModel("claude-sonnet-5")
	c.Save()
	if !c.Saving() || c.Progress() != "Adding the account" {
		t.Fatalf("not saving: %v %q", c.Saving(), c.Progress())
	}
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	if rec.done[0] != "acc_0123456789abcdef0123456789abcdef" || c.Saving() || c.Busy() {
		t.Errorf("done %v saving %v", rec.done, c.Saving())
	}
	a := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd)
	if a.Credentials.Password != "sk-ant-test" || a.Config.Kind != api.AccountAI || a.Config.Name != "Work" || a.Config.Email != "" ||
		a.Config.AI == nil || a.Config.AI.Provider != api.AIAnthropic || a.Config.AI.Model != "claude-opus-5-5" || a.Config.AI.BoardModel != "claude-sonnet-5" ||
		a.Config.AI.Consent == nil || a.Config.AI.Consent.Version != api.AssistantConsentVersion || a.Config.AI.Consent.AcceptedAt != nil {
		t.Errorf("add params %+v %+v", a, a.Config.AI)
	}
	if rec.configs[0].AI.Consent == nil || c.Form().Key != "" {
		t.Errorf("the key stayed in the form (%q) or the consent is missing from OnDone", c.Form().Key)
	}
	if got := d.callList(); !slices.Equal(got, []string{api.MethodAccountTest, api.MethodAccountAdd}) {
		t.Errorf("calls %v", got)
	}
}

func TestARefusedKeyIsSaidOnTheKeyField(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountTest, test401JSON)
	c, rec, loop := start(d, api.AIOpenAI, nil)
	c.SetName("Work")
	c.SetKey("sk-bad")
	c.SetModel("gpt-6.1-sol")
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if out := c.Test(); out.OK || out.Text != "The provider refused the API key" || out.Trust != nil {
		t.Errorf("outcome %+v", out)
	}
	if c.Problem() != FieldKey || !slices.Contains(rec.problems, FieldKey) || len(c.Models()) != 0 {
		t.Errorf("problem %v models %v", c.Problem(), c.Models())
	}
	// Typing another key forgets the outcome.
	c.SetKey("sk-other")
	if c.Test() != nil || c.Problem() != FieldNone {
		t.Errorf("outcome kept after a new key: %+v %v", c.Test(), c.Problem())
	}

	// A failed call (not a failed endpoint) is worded the same way.
	d.fail(api.MethodAccountTest, api.NewError(api.CodeNetworkError, "dial"))
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if out := c.Test(); out.OK || out.Text != "The provider could not be reached" {
		t.Errorf("network outcome %+v", out)
	}
	if c.Problem() != FieldNone {
		t.Errorf("a named provider has no server field to flag: %v", c.Problem())
	}
	d.fail(api.MethodAccountTest, api.NewError(api.CodeInvalidArgument, "model: too long"))
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if out := c.Test(); out.Text != "Testing the connection failed: invalidArgument (1001): model: too long" {
		t.Errorf("general outcome %+v", out)
	}
	d.reply(api.MethodAccountTest, testNoneJSON)
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if out := c.Test(); !out.OK || out.Text != "Connected" {
		t.Errorf("no models outcome %+v", out)
	}
}

func TestEditingKeepsTheStoredKey(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAssistantProviderModels, modelsJSON)
	d.reply(api.MethodAccountUpdate, `{}`)
	a := storedAccount()
	c, rec, loop := start(d, api.AIAnthropic, &a)
	if !c.IsEditing() || c.Provider() != api.AIOpenAI || c.Title() != "Edit Account" || c.SaveLabel() != "_Save" ||
		!c.KeyStored() || c.NeedsConsent() || c.Form().Name != "Work" || c.Form().Model != "gpt-6.1-sol" || c.Form().Key != "" {
		t.Fatalf("editing: %+v", c.Form())
	}
	loop.runUntil(t, func() bool { return len(c.Models()) == 2 })
	if p := lastParams[api.AssistantProviderModelsParams](t, d, api.MethodAssistantProviderModels); p.AccountID != a.ID {
		t.Errorf("providerModels params %+v", p)
	}
	if c.IsChanged() {
		t.Error("an untouched form counts as changed")
	}
	c.Save()
	if rec.closed != 1 || len(d.callList()) != 1 {
		t.Fatalf("unchanged save: closed %d calls %v", rec.closed, d.callList())
	}

	// A new name saves through account.update with empty credentials and
	// the stored consent.
	c.SetName("Work (OpenAI)")
	c.SetModel("gpt-6.1-mini")
	if !c.IsChanged() {
		t.Error("the change was not seen")
	}
	c.Save()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	u := lastParams[api.AccountUpdateParams](t, d, api.MethodAccountUpdate)
	if u.AccountID != a.ID || u.Credentials.Password != "" || u.Config.Name != "Work (OpenAI)" || u.Config.AI.Model != "gpt-6.1-mini" ||
		u.Config.AI.Provider != api.AIOpenAI || u.Config.AI.Consent == nil || u.Config.AI.Consent.Version != api.AssistantConsentVersion {
		t.Errorf("update params %+v %+v", u, u.Config.AI)
	}
}

func TestReplaceKey(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAssistantProviderModels, modelsJSON)
	d.reply(api.MethodAccountTest, testNoneJSON)
	d.reply(api.MethodAccountUpdate, `{}`)
	a := storedAccount()
	c, rec, loop := start(d, "", &a)

	// A test of the untouched account goes with its id and no password.
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	p := lastParams[api.AccountTestParams](t, d, api.MethodAccountTest)
	if p.AccountID != a.ID || p.Credentials.Password != "" {
		t.Errorf("stored-key test params %+v", p)
	}

	c.ReplaceKey()
	if c.KeyStored() || c.KeyLabel() != "New API Key" || c.Test() != nil {
		t.Errorf("replacing: stored %v label %q", c.KeyStored(), c.KeyLabel())
	}
	if f, _ := c.Check(true); f != FieldKey {
		t.Errorf("an open, empty key field passes: %v", f)
	}
	c.KeepKey()
	if !c.KeyStored() || c.IsChanged() {
		t.Errorf("keep: stored %v changed %v", c.KeyStored(), c.IsChanged())
	}
	c.ReplaceKey()
	c.SetKey("sk-new")
	if !c.IsChanged() {
		t.Error("a new key is a change")
	}
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	p = lastParams[api.AccountTestParams](t, d, api.MethodAccountTest)
	if p.AccountID != a.ID || p.Credentials.Password != "sk-new" {
		t.Errorf("new-key test params %+v", p)
	}
	c.Save()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	u := lastParams[api.AccountUpdateParams](t, d, api.MethodAccountUpdate)
	if u.Credentials.Password != "sk-new" || u.Config.AI.Consent == nil {
		t.Errorf("update params %+v", u)
	}
	if c.Form().Key != "" || !c.KeyStored() {
		t.Errorf("after saving: key %q stored %v", c.Form().Key, c.KeyStored())
	}
}

func TestEditingWithoutConsentAsksForIt(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountUpdate, `{}`)
	a := storedAccount()
	a.Config.AI.Consent = &api.AIConsent{Version: api.AssistantConsentVersion - 1}
	c, rec, loop := start(d, "", &a)
	if !c.NeedsConsent() {
		t.Fatal("an older consent counts as current")
	}
	c.Save()
	if c.Problem() != FieldConsent {
		t.Fatalf("saved without the consent: %v", c.Problem())
	}
	c.SetConsent(true)
	if !c.IsChanged() {
		t.Error("a consent given now is a change")
	}
	c.Save()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	u := lastParams[api.AccountUpdateParams](t, d, api.MethodAccountUpdate)
	if u.Config.AI.Consent == nil || u.Config.AI.Consent.Version != api.AssistantConsentVersion || u.Credentials.Password != "" {
		t.Errorf("update params %+v", u.Config.AI)
	}
}

func TestCompatibleServer(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountAdd, addedJSON)
	c, rec, loop := start(d, api.AIOpenAICompatible, nil)
	if !c.UsesServer() || !c.KeyOptional() || c.KeyLabel() != "API Key (optional)" || c.Host() != "" || c.HTTPWarning() ||
		c.Consent.Heading != "Send Mail to Your Server?" {
		t.Fatalf("fresh compatible: %+v", c)
	}
	c.SetName("Home")
	c.SetModel("llama3.3")
	if f, p := c.Check(false); f != FieldServer || p != "Enter the server's address, such as http://localhost:11434/v1" {
		t.Errorf("without an address: %v %q", f, p)
	}
	c.SetBaseURL("ollama.lan:11434")
	if f, p := c.Check(false); f != FieldServer || p != "This is not a web address" {
		t.Errorf("without a scheme: %v %q", f, p)
	}
	c.SetBaseURL("http://localhost:11434/v1/")
	if c.HTTPWarning() || c.HTTPWarningText() != "" || c.Host() != "localhost" {
		t.Errorf("loopback warned: %q", c.HTTPWarningText())
	}
	if f, _ := c.Check(true); f != FieldConsent {
		t.Errorf("a server without a key: %v", f)
	}
	c.SetBaseURL("http://Ollama.lan:11434/v1")
	if !c.HTTPWarning() || c.HTTPWarningText() != "The API key and your mail travel unencrypted over http to ollama.lan." {
		t.Errorf("http outside loopback: %q", c.HTTPWarningText())
	}
	if !strings.HasSuffix(c.ConsentBody(), "Over http they travel unencrypted. The API key and your mail travel unencrypted over http to ollama.lan.") {
		t.Errorf("consent body %q", c.ConsentBody())
	}
	c.SetBaseURL("https://ollama.lan:11434/v1")
	if c.HTTPWarning() || !strings.HasSuffix(c.ConsentBody(), "Over http they travel unencrypted.") {
		t.Errorf("https warned: %q", c.ConsentBody())
	}
	c.SetConsent(true)
	c.Save()
	loop.runUntil(t, func() bool { return len(rec.done) == 1 })
	a := lastParams[api.AccountAddParams](t, d, api.MethodAccountAdd)
	if a.Credentials.Password != "" || a.Config.AI.Provider != api.AIOpenAICompatible || a.Config.AI.BaseURL != "https://ollama.lan:11434/v1" ||
		a.Config.AI.Model != "llama3.3" || a.Config.AI.Consent == nil {
		t.Errorf("add params %+v", a.Config.AI)
	}
}

func TestCertificatePinOnlyWithHTTPS(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountTest, testTLSJSON)
	c, _, loop := start(d, api.AIOpenAICompatible, nil)
	c.SetName("Home")
	c.SetModel("llama3.3")
	c.SetBaseURL("https://ollama.lan/v1")
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	out := c.Test()
	if out.OK || out.Text != "The connection to the provider is not secure" || out.Trust == nil || c.Problem() != FieldServer {
		t.Fatalf("tls outcome %+v problem %v", out, c.Problem())
	}
	p, ok := c.TrustOffer()
	if !ok || p.Cert == nil || p.Cert.SHA256 != fingerprint || p.Reason != api.TLSUntrusted {
		t.Fatalf("trust offer %+v %v", p, ok)
	}
	c.Trust()
	if !c.Pinned() || c.PinText() != fingerprintTx || c.Test() != nil || c.Form().CertificateSHA256 != fingerprint {
		t.Errorf("pinned %v %q", c.Pinned(), c.PinText())
	}
	if f, _ := c.Check(false); f != FieldNone {
		t.Errorf("a pin with https: %v", f)
	}
	cfg := c.config()
	if cfg.AI.CertificateSHA256 != fingerprint || cfg.AI.BaseURL != "https://ollama.lan/v1" {
		t.Errorf("config %+v", cfg.AI)
	}

	// The same host over http keeps the pin, which the rules then refuse;
	// another host drops it.
	c.SetBaseURL("http://ollama.lan/v1")
	if !c.Pinned() {
		t.Error("the pin went with the scheme")
	}
	if f, p := c.Check(false); f != FieldServer || p != "A certificate can be pinned only for an https address" {
		t.Errorf("pin over http: %v %q", f, p)
	}
	c.ForgetPin()
	if c.Pinned() {
		t.Error("forget kept the pin")
	}
	c.SetBaseURL("https://ollama.lan/v1")
	c.Trust() // no offer any more: nothing happens
	if c.Pinned() {
		t.Error("trusted without an offer")
	}
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	c.Trust()
	c.SetBaseURL("https://other.lan/v1")
	if c.Pinned() {
		t.Error("the pin followed another host")
	}

	// Over http no certificate is offered at all.
	c.SetBaseURL("http://ollama.lan/v1")
	c.RunTest()
	loop.runUntil(t, func() bool { return c.Test() != nil })
	if _, ok := c.TrustOffer(); ok {
		t.Error("offered a certificate over http")
	}
}

func TestLateRepliesAreDropped(t *testing.T) {
	d := newFakeDaemon()
	d.reply(api.MethodAccountTest, testOKJSON)
	c, rec, loop := start(d, api.AIAnthropic, nil)
	c.SetName("Work")
	c.SetKey("sk")
	c.SetModel("m")
	c.RunTest()
	c.Close()
	loop.runUntil(t, func() bool { return len(loop) == 0 })
	// Drain whatever the goroutine posts.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case f := <-loop:
			f()
			continue
		case <-deadline:
		}
		break
	}
	if c.Test() != nil || len(c.Models()) != 0 || !c.Closed() {
		t.Errorf("a late reply landed: %+v", c.Test())
	}
	_ = rec
}

func TestModelRules(t *testing.T) {
	d := newFakeDaemon()
	c, _, _ := start(d, api.AIOpenAI, nil)
	c.SetName("Work")
	c.SetKey("sk")
	if f, p := c.Check(false); f != FieldModel || p != "Enter or choose a model" {
		t.Errorf("no model: %v %q", f, p)
	}
	c.SetModel("gpt\x01")
	if f, p := c.Check(false); f != FieldModel || p != "This is not a model id" {
		t.Errorf("control character: %v %q", f, p)
	}
	c.SetModel(strings.Repeat("m", 257))
	if f, _ := c.Check(false); f != FieldModel {
		t.Errorf("too long: %v", f)
	}
	c.SetModel(" gpt-6.1-sol ")
	c.SetBoardModel("x\ty")
	if f, _ := c.Check(false); f != FieldBoardModel {
		t.Errorf("bad board model: %v", f)
	}
	c.SetBoardModel("")
	if f, _ := c.Check(false); f != FieldNone {
		t.Errorf("optional board model: %v", f)
	}
	if cfg := c.config(); cfg.AI.Model != "gpt-6.1-sol" || cfg.AI.BoardModel != "" {
		t.Errorf("config %+v", cfg.AI)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"http://localhost:11434/v1", "http://localhost:11434/v1", true},
		{" HTTPS://OpenRouter.ai/api/v1/ ", "https://openrouter.ai/api/v1", true},
		{"http://user:pw@host.lan/v1?x=1#f", "http://host.lan/v1", true},
		{"http://[::1]:8000/v1", "http://[::1]:8000/v1", true},
		{"https://api.groq.com/openai/v1", "https://api.groq.com/openai/v1", true},
		{"http://host.lan", "http://host.lan", true},
		{"", "", false},
		{"host.lan:11434/v1", "", false},
		{"ftp://host.lan/v1", "", false},
		{"http:///v1", "", false},
		{"http://host .lan/v1", "", false},
		{"http://host.lan/v1\x00", "", false},
		{"http://host.lan/" + strings.Repeat("a", 2048), "", false},
	} {
		got, ok := NormalizeBaseURL(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	for _, c := range []struct {
		host string
		want bool
	}{
		{"localhost", true}, {"LOCALHOST", true}, {"ollama.localhost", true}, {"127.0.0.1", true}, {"127.1.2.3", true},
		{"::1", true}, {"[::1]", true}, {"ollama.lan", false}, {"10.0.0.5", false}, {"", false}, {"localhost.lan", false},
	} {
		if got := IsLoopback(c.host); got != c.want {
			t.Errorf("IsLoopback(%q) = %v", c.host, got)
		}
	}
}

func TestFailureOf(t *testing.T) {
	tr := msgids{}
	for _, c := range []struct {
		err    error
		banner string
		field  Field
	}{
		{api.NewError(api.CodeAuthFailed, "401"), "The provider refused the API key", FieldKey},
		{api.NewError(api.CodeAuthRequired, "no key"), "Enter the API key", FieldKey},
		{api.NewError(api.CodeNetworkError, "dial"), "The provider could not be reached", FieldServer},
		{api.NewError(api.CodeOffline, "offline"), "The provider could not be reached", FieldServer},
		{api.NewError(api.CodeServerTimeout, "slow"), "The provider did not answer in time", FieldServer},
		{api.NewError(api.CodeTLSError, "tls"), "The connection to the provider is not secure", FieldServer},
		{api.NewError(api.CodeServerError, "500"), "The provider answered with an error", FieldNone},
		{api.NewError(api.CodeAssistantConsentRequired, "consent"), "Allow sending mail to the provider first", FieldConsent},
		{api.NewError(api.CodeInvalidArgument, "bad"), "", FieldNone},
		{api.NewError(api.CodeKeyringError, "locked"), "", FieldNone},
		{errors.New("plain"), "", FieldNone},
		{nil, "", FieldNone},
	} {
		got := FailureOf(c.err, tr)
		if got.Banner != c.banner || got.Field != c.field {
			t.Errorf("FailureOf(%v) = %+v, want %q %v", c.err, got, c.banner, c.field)
		}
	}
}

func TestTexts(t *testing.T) {
	tr := msgids{}
	s := Texts(tr)
	for _, x := range []string{s.AddTitle, s.EditTitle, s.ProviderLabel, s.NameLabel, s.ServerLabel, s.ServerHint, s.KeyLabel, s.KeyOptionalLabel,
		s.NewKeyLabel, s.KeyStored, s.ReplaceKey, s.KeepKey, s.ModelLabel, s.ModelHint, s.BoardModelLabel, s.BoardModelHint, s.PinLabel, s.PinForget,
		s.Test, s.Testing, s.ConnectedNoModels, s.Add, s.Save, s.Adding, s.Saving, s.NameMissing, s.ServerMissing, s.ServerInvalid, s.PinNeedsHTTPS,
		s.KeyMissing, s.ModelMissing, s.ModelInvalid, s.ConsentMissing, s.KeyRefused, s.Unreachable, s.Timeout, s.Insecure, s.ServerError} {
		if x == "" {
			t.Errorf("empty text in %+v", s)
		}
	}
	if s.Connected(0) != "Connected" || s.Connected(1) != "Connected · 1 model" || s.Connected(3) != "Connected · 3 models" {
		t.Errorf("Connected: %q %q %q", s.Connected(0), s.Connected(1), s.Connected(3))
	}
	hint := AddressHint(tr)
	for _, e := range AddressExamples {
		if !strings.Contains(hint, e.Name+"  "+e.URL) {
			t.Errorf("hint lacks %s: %q", e.Name, hint)
		}
	}
	if len(AddressExamples) != 8 || AddressExamples[0].URL != "http://localhost:11434/v1" || AddressExamples[4].Name != "OpenRouter" {
		t.Errorf("examples %+v", AddressExamples)
	}
	if HTTPWarning(tr, "ollama.lan") != "The API key and your mail travel unencrypted over http to ollama.lan." {
		t.Errorf("warning %q", HTTPWarning(tr, "ollama.lan"))
	}
	for _, c := range []struct {
		p        api.AIProvider
		heading  string
		specific string
	}{
		{api.AIAnthropic, "Send Mail to Anthropic?", "They go to Anthropic under its API terms, which do not use data sent through the API for training by default."},
		{api.AIOpenAI, "Send Mail to OpenAI?", "They go to OpenAI under its API terms, which do not use data sent through the API for training by default."},
		{api.AIOpenAICompatible, "Send Mail to Your Server?", "They go to the server at the address you set. Over http they travel unencrypted."},
	} {
		got := ConsentTexts(tr, c.p)
		if got.Heading != c.heading || got.Specific != c.specific || got.Checkbox != "Allow Malachi Mail to send mail to this provider" ||
			!strings.HasPrefix(got.Common, "The assistant sends the prompt, the text you write, the mail it reads for you") ||
			got.Body() != got.Common+"\n\n"+got.Specific {
			t.Errorf("%s: %+v", c.p, got)
		}
	}
}

func modelIDs(models []api.AIModel) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}
