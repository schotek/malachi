// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// A provider's failures read as sentences, never as its codes: the usage
// limit in the board's words, Codex missing and the ChatGPT connection in
// the provider's; Claude Code's own failures keep theirs.
func TestFailureTextsOfProviders(t *testing.T) {
	words := assistant.ChatGPTText(tr)
	limit := ProviderFailure("chatgpt_usage_limit")
	for _, got := range []string{limit.Text(tr), limit.ReasonText(tr)} {
		if strings.Contains(got, "chatgpt_") || !strings.Contains(got, "usage limit") {
			t.Errorf("limit text = %q", got)
		}
	}
	if got := ProviderFailure("codex_not_found").Text(tr); got != words.NativeMissingCodex {
		t.Errorf("Codex missing = %q", got)
	}
	for _, reason := range []string{"chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_identity_mismatch"} {
		f := ProviderFailure(reason)
		if f.Text(tr) != words.Reconnect || f.ReasonText(tr) != words.Reconnect {
			t.Errorf("%s = %q / %q", reason, f.Text(tr), f.ReasonText(tr))
		}
	}
	claude := Failure{Kind: FailureNotSignedIn}
	if got := claude.Text(tr); got != assistant.SignInTexts(tr).Hint {
		t.Errorf("Claude Code not signed in = %q", got)
	}
	if got := (Failure{Kind: FailureNotFound}).Text(tr); got != assistant.PanelTexts(tr).NotFound {
		t.Errorf("Claude Code not found = %q", got)
	}
}

// startProviderTurn submits a question to a panel over provider and
// returns its session, the question sent.
func startProviderTurn(t *testing.T, loop *testLoop, c *Controller, provider *fakeProvider) *fakeSession {
	t.Helper()
	if !c.Submit("input") {
		t.Fatal("submission refused")
	}
	loop.runUntil(t, func() bool { return c.providerSession != nil })
	s := <-provider.sessions
	<-s.sent
	return s
}

func lastItem(c *Controller) Content {
	items := c.Items()
	if len(items) == 0 {
		return Content{}
	}
	return items[len(items)-1].Content
}

// The plan's usage limit ends the panel's turn with a sentence and Try
// Again, not "The assistant stopped: chatgpt_usage_limit".
func TestPanelProviderUsageLimit(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	c := New(Config{Provider: func() Provider { return provider }, Translator: tr, Settings: &memSettings{}, Loop: loop, Log: discardLog(), Bridge: "/fixture/malachi-mcp"})
	defer c.Close()
	s := startProviderTurn(t, loop, c, provider)
	go s.emit(assistant.Event{Kind: assistant.EventResult, IsError: true, ResultText: "chatgpt_usage_limit"})
	loop.runUntil(t, func() bool { return !c.Running() && lastItem(c).Kind == ContentError })
	got := lastItem(c)
	if strings.Contains(got.Text, "chatgpt_") || !got.Retry || got.Offer != OfferNone {
		t.Fatalf("error line = %+v", got)
	}
}

// A lapsed ChatGPT connection offers Reconnect (OfferReconnect, not
// Claude Code's Sign In…), which reconnects through the window's
// ReconnectProvider and asks the question again in a new session; a failed
// reconnect offers it again.
func TestPanelProviderReconnect(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	c := New(Config{Provider: func() Provider { return provider }, Translator: tr, Settings: &memSettings{}, Loop: loop, Log: discardLog(), Bridge: "/fixture/malachi-mcp"})
	defer c.Close()
	var reconnects []func(bool)
	c.ReconnectProvider = func(_ context.Context, done func(bool)) { reconnects = append(reconnects, done) }
	s := startProviderTurn(t, loop, c, provider)
	s.mu.Lock()
	exit := s.exit
	s.mu.Unlock()
	go exit(Exit{Status: 1, Reason: "chatgpt_reconnect_required"})
	loop.runUntil(t, func() bool { return !c.Running() && lastItem(c).Kind == ContentError })
	line := c.Items()[len(c.Items())-1]
	if line.Content.Text != assistant.ChatGPTText(tr).Reconnect || line.Content.Offer != OfferReconnect {
		t.Fatalf("error line = %+v", line.Content)
	}

	c.SignIn(line.ID)
	loop.runUntil(t, func() bool { return len(reconnects) == 1 })
	reconnects[0](false)
	loop.runUntil(t, func() bool { return !c.Running() && lastItem(c).Kind == ContentError })
	again := c.Items()[len(c.Items())-1]
	if again.Content.Text != assistant.ChatGPTText(tr).ConnectionFailed || again.Content.Offer != OfferReconnect {
		t.Fatalf("after a failed reconnect = %+v", again.Content)
	}

	c.SignIn(again.ID)
	loop.runUntil(t, func() bool { return len(reconnects) == 2 })
	reconnects[1](true)
	loop.runUntil(t, func() bool { return len(provider.sessions) > 0 })
	next := <-provider.sessions
	loop.runUntil(t, func() bool { return len(next.sent) > 0 })
	if msg := <-next.sent; msg == "" {
		t.Fatal("the question was not asked again")
	}
}

// Without ReconnectProvider the line offers nothing (Claude Code's sign-in
// would not help).
func TestPanelProviderNotSignedInWithoutReconnect(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	c := New(Config{Provider: func() Provider { return provider }, Translator: tr, Settings: &memSettings{}, Loop: loop, Log: discardLog(), Bridge: "/fixture/malachi-mcp"})
	defer c.Close()
	s := startProviderTurn(t, loop, c, provider)
	go s.emit(assistant.Event{Kind: assistant.EventResult, IsError: true, ResultText: "chatgpt_not_connected"})
	loop.runUntil(t, func() bool { return !c.Running() && lastItem(c).Kind == ContentError })
	if got := lastItem(c); got.Offer != OfferNone || got.Text != assistant.ChatGPTText(tr).Reconnect {
		t.Fatalf("error line = %+v", got)
	}
}

// Stop while the provider reconnects cancels the reconnect's context (the
// browser sign-in ends) and a late done changes nothing.
func TestPanelProviderReconnectStopCancels(t *testing.T) {
	loop := newTestLoop()
	provider := newFakeProvider()
	c := New(Config{Provider: func() Provider { return provider }, Translator: tr, Settings: &memSettings{}, Loop: loop, Log: discardLog(), Bridge: "/fixture/malachi-mcp"})
	defer c.Close()
	var ctxs []context.Context
	var dones []func(bool)
	c.ReconnectProvider = func(ctx context.Context, done func(bool)) {
		ctxs = append(ctxs, ctx)
		dones = append(dones, done)
	}
	s := startProviderTurn(t, loop, c, provider)
	s.mu.Lock()
	exit := s.exit
	s.mu.Unlock()
	go exit(Exit{Status: 1, Reason: "chatgpt_reconnect_required"})
	loop.runUntil(t, func() bool { return !c.Running() && lastItem(c).Kind == ContentError })
	line := c.Items()[len(c.Items())-1]
	c.SignIn(line.ID)
	loop.runUntil(t, func() bool { return len(ctxs) == 1 })
	if ctxs[0].Err() != nil {
		t.Fatal("the reconnect's context is cancelled before Stop")
	}
	c.Stop()
	loop.runUntil(t, func() bool { return ctxs[0].Err() != nil })
	n := len(c.Items())
	dones[0](true)
	loop.settle(t, 50*time.Millisecond)
	if len(c.Items()) != n || c.Running() {
		t.Fatalf("a late reconnect changed the panel: %+v", c.Items())
	}
}
