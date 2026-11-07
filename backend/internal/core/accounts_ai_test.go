// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeLLM answers Models from a script and records the configurations
// the backend built; Stream is not used by the account methods.
type fakeLLM struct {
	models []api.AIModel
	err    error
	cfgs   []llm.Config
}

func (f *fakeLLM) Stream(context.Context, llm.Request, func(llm.Event) error) error {
	return errors.New("not used")
}

func (f *fakeLLM) Models(context.Context) ([]api.AIModel, error) { return f.models, f.err }

func aiBackend(t *testing.T) (*Backend, *fakeLLM) {
	t.Helper()
	b := newTestBackend(t, config.Config{})
	b.Keyring = newMemKeyring()
	f := &fakeLLM{models: []api.AIModel{{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5"}, {ID: "claude-haiku-5-5"}}}
	b.AIProvider = func(cfg llm.Config) (llm.Provider, error) {
		f.cfgs = append(f.cfgs, cfg)
		return f, nil
	}
	return b, f
}

func anthropicConfig(name string) api.AccountConfig {
	return api.AccountConfig{Name: name, Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "claude-opus-5-5"}}
}

func TestAIAccount(t *testing.T) {
	b, f := aiBackend(t)
	ctx := context.Background()
	svc := b.Accounts()

	// A named provider needs its key; the key is sent once, to the fixed
	// base URL, and the account is stored with the one capability.
	_, err := svc.Add(ctx, api.AccountAddParams{Config: anthropicConfig("Anthropic")})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("add without a key: %v", err)
	}
	res, err := svc.Add(ctx, api.AccountAddParams{Config: anthropicConfig("Anthropic"), Credentials: api.Credentials{Password: "sk-ant-test"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	id := res.AccountID
	if len(f.cfgs) != 1 || f.cfgs[0].Key != "sk-ant-test" || f.cfgs[0].BaseURL != api.AnthropicBaseURL || f.cfgs[0].Provider != api.AIAnthropic {
		t.Fatalf("the provider got %+v", f.cfgs)
	}
	if got := b.Keyring.(*memKeyring).values[string(id)+"/password"]; got != "sk-ant-test" {
		t.Fatalf("keyring holds %q", got)
	}
	list, err := svc.List(ctx, api.AccountListParams{})
	if err != nil || len(list.Accounts) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	a := list.Accounts[0]
	if a.Config.Kind != api.AccountAI || a.Config.Email != "" || len(a.Capabilities) != 1 || a.Capabilities[0] != api.CapabilityAssistant {
		t.Fatalf("account %+v caps %v", a.Config, a.Capabilities)
	}
	if a.Config.AI.Consent != nil {
		t.Fatal("a consent appeared from nowhere")
	}

	// account.test with the stored key (accountId, no password) lists
	// the models; with a password that one is used.
	tr, err := svc.Test(ctx, api.AccountTestParams{Config: a.Config, AccountID: id})
	if err != nil || tr.AI == nil || !tr.AI.OK || len(tr.AI.Models) != 2 || tr.AI.Models[0].DisplayName != "Claude Opus 5.5" {
		t.Fatalf("test: %+v %v", tr, err)
	}
	if f.cfgs[len(f.cfgs)-1].Key != "sk-ant-test" {
		t.Fatal("the stored key was not used")
	}
	if _, err := svc.Test(ctx, api.AccountTestParams{Config: a.Config, AccountID: id, Credentials: api.Credentials{Password: "other"}}); err != nil {
		t.Fatal(err)
	}
	if f.cfgs[len(f.cfgs)-1].Key != "other" {
		t.Fatal("the given key was not used")
	}
	tr, err = svc.Test(ctx, api.AccountTestParams{Config: anthropicConfig("x")})
	if err != nil || tr.AI == nil || tr.AI.OK || tr.AI.Error == nil || tr.AI.Error.Code != api.CodeAuthRequired {
		t.Fatalf("test without a key: %+v %v", tr, err)
	}
	// The state follows the last test with the stored key.
	f.err = &llm.Error{Class: api.FailureAuth, Status: 401}
	if tr, err := svc.Test(ctx, api.AccountTestParams{Config: a.Config, AccountID: id}); err != nil || tr.AI.OK {
		t.Fatalf("refused test: %+v %v", tr, err)
	}
	f.err = nil
	list, _ = svc.List(ctx, api.AccountListParams{})
	if list.Accounts[0].State.Status != api.SyncAuthRequired {
		t.Fatalf("state after a refused key: %s", list.Accounts[0].State.Status)
	}
	if _, err := svc.Test(ctx, api.AccountTestParams{Config: a.Config, AccountID: id}); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.List(ctx, api.AccountListParams{})
	if list.Accounts[0].State.Status != api.SyncIdle {
		t.Fatalf("state after a good test: %s", list.Accounts[0].State.Status)
	}

	// Another ai account's stored key is never used for this endpoint.
	compat := api.AccountConfig{Name: "Ollama", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "http://Localhost:11434/v1/", Model: "llama3.3"}}
	if _, err := svc.Test(ctx, api.AccountTestParams{Config: compat, AccountID: id}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("the stored key for another endpoint: %v", err)
	}

	// A compatible server needs no key; its base URL is normalised.
	res2, err := svc.Add(ctx, api.AccountAddParams{Config: compat})
	if err != nil {
		t.Fatalf("add compatible: %v", err)
	}
	list, _ = svc.List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 2 || list.Accounts[1].Config.AI.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("compatible stored as %+v", list.Accounts[1].Config.AI)
	}
	if f.cfgs[len(f.cfgs)-1].Key != "" || f.cfgs[len(f.cfgs)-1].BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("compatible provider got %+v", f.cfgs[len(f.cfgs)-1])
	}
	if _, err := b.Keyring.Get(ctx, res2.AccountID, "password"); err == nil {
		t.Fatal("a key was stored for a key-less account")
	}
	if tr, err := svc.Test(ctx, api.AccountTestParams{Config: compat, AccountID: res2.AccountID}); err != nil || !tr.AI.OK {
		t.Fatalf("test of the key-less account: %+v %v", tr, err)
	}

	// Validation.
	bad := []api.AccountConfig{
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "m"}, Jira: &api.JiraConfig{}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAI, Model: "m", BaseURL: "https://x.example.org/v1"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "m"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "m", BaseURL: "ftp://x/v1"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "m", BaseURL: "http://user:pw@x/v1"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "m", BaseURL: "http://x/v1", CertificateSHA256: "ab"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "", BaseURL: "http://x/v1"}},
		{Name: "x", Kind: api.AccountAI, AI: &api.AIConfig{Provider: "ollama", Model: "m"}},
		{Name: "x", Kind: api.AccountAI},
		{Name: "x", Email: "me@example.org", AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "m"},
			IMAP: validConfig().IMAP, SMTP: validConfig().SMTP},
	}
	for i, c := range bad {
		if _, err := svc.Add(ctx, api.AccountAddParams{Config: c, Credentials: api.Credentials{Password: "k"}}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("bad config %d accepted: %v", i, err)
		}
	}

	// A refused key stores nothing.
	f.err = &llm.Error{Class: api.FailureAuth, Status: 401}
	if _, err := svc.Add(ctx, api.AccountAddParams{Config: anthropicConfig("Refused"), Credentials: api.Credentials{Password: "bad"}}); errCode(t, err) != api.CodeAuthFailed {
		t.Fatalf("refused key: %v", err)
	}
	f.err = nil
	list, _ = svc.List(ctx, api.AccountListParams{})
	if len(list.Accounts) != 2 {
		t.Fatalf("%d accounts after a refused add", len(list.Accounts))
	}

	// Update: the key stays unless the endpoint moves; a consent sent is
	// stamped, a missing or lower one keeps the stored consent.
	cfg := a.Config
	cfg.AI.Model = "claude-haiku-5-5"
	cfg.AI.Consent = &api.AIConsent{Version: api.AssistantConsentVersion}
	if _, err := svc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: cfg}); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _ = svc.List(ctx, api.AccountListParams{})
	got := list.Accounts[0].Config.AI
	if got.Model != "claude-haiku-5-5" || got.Consent == nil || got.Consent.AcceptedAt == nil || !got.Consented() {
		t.Fatalf("after update %+v", got)
	}
	accepted := *got.Consent.AcceptedAt
	cfg = list.Accounts[0].Config
	cfg.AI.Consent = nil
	if _, err := svc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.List(ctx, api.AccountListParams{})
	if c := list.Accounts[0].Config.AI.Consent; c == nil || c.Version != api.AssistantConsentVersion || !c.AcceptedAt.Equal(accepted) {
		t.Fatalf("an update without a consent lost it: %+v", c)
	}
	cfg = list.Accounts[0].Config
	cfg.AI.Consent = &api.AIConsent{Version: 0}
	if _, err := svc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.List(ctx, api.AccountListParams{})
	if c := list.Accounts[0].Config.AI.Consent; c == nil || c.Version != api.AssistantConsentVersion {
		t.Fatalf("a zero consent replaced the stored one: %+v", c)
	}
	moved := list.Accounts[0].Config
	moved.AI.Provider = api.AIOpenAI
	moved.AI.Model = "gpt-6.1-sol"
	if _, err := svc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: moved}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("a moved key without a new one: %v", err)
	}
	if _, err := svc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: moved, Credentials: api.Credentials{Password: "sk-new"}}); err != nil {
		t.Fatalf("a moved key with a new one: %v", err)
	}
	if got := b.Keyring.(*memKeyring).values[string(id)+"/password"]; got != "sk-new" {
		t.Fatalf("keyring holds %q", got)
	}

	// Remove deletes the key.
	if _, err := svc.Remove(ctx, api.AccountRemoveParams{AccountID: id, DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	deleted := b.Keyring.(*memKeyring).deleted
	found := false
	for _, d := range deleted {
		if d == string(id)+"/password" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the key was not deleted: %v", deleted)
	}
}

func TestAIConsentSettling(t *testing.T) {
	now := time.Now()
	next := &api.AIConfig{Consent: &api.AIConsent{Version: 1}}
	settleAIConsent(next, nil, now)
	if next.Consent == nil || next.Consent.AcceptedAt == nil {
		t.Fatal("a first consent was not stamped")
	}
	stored := next.Consent
	next = &api.AIConfig{Consent: &api.AIConsent{Version: 1}}
	settleAIConsent(next, &api.AIConfig{Consent: stored}, now.Add(1e9))
	if next.Consent != stored {
		t.Fatal("the same version was re-stamped")
	}
	next = &api.AIConfig{Consent: &api.AIConsent{Version: 2}}
	settleAIConsent(next, &api.AIConfig{Consent: stored}, now.Add(1e9))
	if next.Consent == stored || next.Consent.Version != 2 {
		t.Fatal("a higher version was not accepted")
	}
}

func TestAIBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://Localhost:11434/v1/":         "http://localhost:11434/v1",
		"https://Gateway.Example.org/api/v1": "https://gateway.example.org/api/v1",
		"http://192.168.1.20:11434/v1?x=1#f": "http://192.168.1.20:11434/v1",
		"http://[::1]:8080/v1":               "http://[::1]:8080/v1",
		"http://host.example/":               "http://host.example",
		"ftp://x/v1":                         "",
		"http://user:pw@x/v1":                "",
		"/v1":                                "",
		"":                                   "",
		"http://":                            "",
	}
	for in, want := range cases {
		got, err := normaliseAIBaseURL(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q accepted as %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q → %q (%v), want %q", in, got, err, want)
		}
	}
}
