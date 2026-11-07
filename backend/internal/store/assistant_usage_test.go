// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func aiAccount(id, name string, cfg api.AIConfig) Account {
	return Account{ID: id, Name: name, Enabled: true, Config: api.AccountConfig{Name: name, Kind: api.AccountAI, AI: &cfg}}
}

// The realm of an ai account is "ai:" and its effective base URL's host
// and path, so it never collides with a mailbox; several ai accounts may
// share it; one without a base URL is refused; an address is not needed.
func TestAIRealm(t *testing.T) {
	cases := map[string]api.AccountConfig{
		"ai:api.anthropic.com":        {Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "claude-opus-5-5"}},
		"ai:api.openai.com/v1":        {Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAI, Model: "gpt-6.1-sol"}},
		"ai:192.168.1.20:11434/v1":    {Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "http://192.168.1.20:11434/v1", Model: "llama3.3"}},
		"ai:gateway.example.org/v1":   {Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, BaseURL: "https://Gateway.example.org:443/v1/", Model: "x"}},
		"":                            {Kind: api.AccountAI, AI: &api.AIConfig{Provider: api.AIOpenAICompatible, Model: "x"}},
		"ai:localhost:11434/v1 (nil)": {Kind: api.AccountAI},
	}
	for want, cfg := range cases {
		got := RealmOf(cfg)
		if want == "ai:localhost:11434/v1 (nil)" {
			want = ""
		}
		if got != want {
			t.Errorf("realm of %+v = %q, want %q", cfg.AI, got, want)
		}
	}
	s := openTestStore(t)
	ctx := context.Background()
	mail := Account{ID: "mail", Name: "Mail", Enabled: true, Config: api.AccountConfig{Name: "Mail", Email: "me@example.org"}}
	if err := s.AddAccount(ctx, &mail); err != nil {
		t.Fatal(err)
	}
	a := aiAccount("ai1", "Anthropic", api.AIConfig{Provider: api.AIAnthropic, Model: "claude-opus-5-5"})
	b := aiAccount("ai2", "Anthropic work", api.AIConfig{Provider: api.AIAnthropic, Model: "claude-sonnet-5-5"})
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatalf("an ai account without an address: %v", err)
	}
	if err := s.AddAccount(ctx, &b); err != nil {
		t.Fatalf("a second ai account of the same provider: %v", err)
	}
	if a.Realm != "ai:api.anthropic.com#ai1" || b.Realm != "ai:api.anthropic.com#ai2" {
		t.Errorf("realms %q %q", a.Realm, b.Realm)
	}
	dup := aiAccount("ai1", "Again", api.AIConfig{Provider: api.AIOpenAI, Model: "x"})
	if err := s.AddAccount(ctx, &dup); !errors.Is(err, ErrExists) {
		t.Errorf("a taken id: %v", err)
	}
	bad := aiAccount("ai3", "No URL", api.AIConfig{Provider: api.AIOpenAICompatible, Model: "x"})
	if err := s.AddAccount(ctx, &bad); err == nil {
		t.Error("an ai account without a base URL was added")
	}
	// An update keeps the exemption: the same realm as the other account.
	b.Config.AI.Model = "claude-haiku-5-5"
	if err := s.UpdateAccount(ctx, &b); err != nil {
		t.Fatalf("update: %v", err)
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("%d accounts", len(accounts))
	}
}

func TestAssistantUsage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := aiAccount("ai1", "A", api.AIConfig{Provider: api.AIAnthropic, Model: "m"})
	b := aiAccount("ai2", "B", api.AIConfig{Provider: api.AIOpenAI, Model: "m"})
	for _, acc := range []*Account{&a, &b} {
		if err := s.AddAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
	}
	add := func(id, day, model string, in, out int64, sess bool) {
		t.Helper()
		if err := s.AddAssistantUsage(ctx, id, day, model, api.AssistantUsage{InputTokens: in, OutputTokens: out, CacheReadInputTokens: 1}, sess); err != nil {
			t.Fatal(err)
		}
	}
	add("ai1", "2026-11-07", "m1", 100, 10, true)
	add("ai1", "2026-11-07", "m1", 50, 5, false)
	add("ai1", "2026-11-07", "m2", 7, 1, true)
	add("ai1", "2026-11-06", "m1", 1, 1, true)
	add("ai2", "2026-11-07", "m1", 3, 3, true)
	if err := s.AddAssistantUsage(ctx, "nobody", "2026-11-07", "m", api.AssistantUsage{}, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if err := s.AddAssistantUsage(ctx, "ai1", "7.11.2026", "m", api.AssistantUsage{}, true); err == nil {
		t.Error("a bad day was accepted")
	}
	// Negative counters are clamped, nothing is refused.
	if err := s.AddAssistantUsage(ctx, "ai2", "2026-11-07", "m1", api.AssistantUsage{InputTokens: -5}, false); err != nil {
		t.Fatal(err)
	}

	days, err := s.AssistantUsage(ctx, "", "2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	want := []api.AssistantUsageDay{
		{Day: "2026-11-07", AccountID: "ai1", AssistantUsage: api.AssistantUsage{InputTokens: 157, OutputTokens: 16, CacheReadInputTokens: 3}, Sessions: 2, Turns: 3},
		{Day: "2026-11-07", AccountID: "ai2", AssistantUsage: api.AssistantUsage{InputTokens: 3, OutputTokens: 3, CacheReadInputTokens: 1}, Sessions: 1, Turns: 2},
		{Day: "2026-11-06", AccountID: "ai1", AssistantUsage: api.AssistantUsage{InputTokens: 1, OutputTokens: 1, CacheReadInputTokens: 1}, Sessions: 1, Turns: 1},
	}
	if len(days) != len(want) {
		t.Fatalf("got %+v", days)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Errorf("day %d: got %+v want %+v", i, days[i], want[i])
		}
	}
	one, err := s.AssistantUsage(ctx, "ai2", "2026-11-07")
	if err != nil || len(one) != 1 || one[0].AccountID != "ai2" {
		t.Fatalf("one account: %+v %v", one, err)
	}
	later, err := s.AssistantUsage(ctx, "", "2026-11-08")
	if err != nil || len(later) != 0 {
		t.Fatalf("a day after: %+v %v", later, err)
	}

	if n, err := s.PruneAssistantUsage(ctx, "2026-11-07"); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if err := s.DeleteAccount(ctx, "ai1", true); err != nil {
		t.Fatal(err)
	}
	rest, err := s.AssistantUsage(ctx, "", "2026-01-01")
	if err != nil || len(rest) != 1 || rest[0].AccountID != "ai2" {
		t.Fatalf("after delete: %+v %v", rest, err)
	}
}
