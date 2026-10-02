// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"testing"
)

func TestLateBoardConsentDoesNotApproveNewProvider(t *testing.T) {
	prefs := prefsWith(func(p *api.BoardPreferences) { p.Assistant = false })
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("unused")), options{noConsent: true, prefs: &prefs})
	identity := 1
	h.c.ConsentIdentity = func() int { return identity }
	h.daemon.hold(api.MethodBoardSetPreferences, true)
	finished, accepted := false, false
	h.c.GiveConsent(false, func(ok bool) { finished, accepted = true, ok })
	h.loop.runUntil(t, func() bool { return h.daemon.waiting(api.MethodBoardSetPreferences) == 1 })
	identity++
	h.daemon.hold(api.MethodBoardSetPreferences, false)
	h.loop.runUntil(t, func() bool { return finished && h.prefs.Idle() })
	if accepted || h.settings.AssistantConsent() || h.settings.BoardTriageConsent() {
		t.Fatal("old approval authorized the new provider")
	}
	current, known := h.prefs.Current()
	if !known || current.Assistant {
		t.Fatal("stale approval left backend assistant enabled")
	}
}

func TestRunCapturesProviderSource(t *testing.T) {
	h := newHarness(t, newFakeClaude(t, "true", answerTurn("synthetic")), options{})
	h.c.Source = func() string { return "malachi-chatgpt" }
	h.board(1, true, 0, nil)
	if !h.c.Start(Manual, 1) {
		t.Fatal("not started")
	}
	h.ended()
	starts := h.daemon.starts()
	if len(starts) != 1 || starts[0].Source != "malachi-chatgpt" {
		t.Fatalf("wrong provenance: %+v", starts)
	}
}
