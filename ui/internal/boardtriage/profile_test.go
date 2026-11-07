// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A triage run runs with the daemon's prompt (which carries the whole
// profile there): asked for the triage purpose in the UI's language,
// without an account of its own.
func TestRunTakesTheDaemonsPrompt(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit, fakeText("Done."), fakeResult("Done.", true)}})
	h := newHarness(t, fake, options{})
	h.board(1, true, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.ended()
	if !strings.HasSuffix(strings.Join(fake.args(), "\n"), "--system-prompt\nSYS triage cs") {
		t.Errorf("args %q", fake.args())
	}
	if last, ok := h.prompter.Last(); !ok || last.Purpose != api.PurposeTriage || last.Language != testLanguage || last.AccountID != "" {
		t.Errorf("prompter %+v", last)
	}
}
