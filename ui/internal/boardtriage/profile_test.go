// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// A triage run carries the profile, every account's note.
func TestRunCarriesTheProfile(t *testing.T) {
	fake := newFakeClaude(t, "true", fakeTurn{lines: []string{fakeInit, fakeText("Done."), fakeResult("Done.", true)}})
	h := newHarness(t, fake, options{})
	h.c.Profile = func() assistant.Profile {
		return assistant.Profile{Text: "Píšu stručně.", Accounts: []assistant.AccountNote{
			{AccountID: "acc_1", Note: "Podepisuj jako studio."}, {AccountID: "acc_2", Note: "Tykej."},
		}}
	}
	h.board(1, true, 0, nil)
	if !h.c.Start(Manual, 0) {
		t.Fatal("not started")
	}
	h.ended()
	joined := strings.Join(fake.args(), "\n")
	for _, want := range []string{"Píšu stručně.", "Podepisuj jako studio.", "Tykej."} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q missing from %q", want, fake.args())
		}
	}
}
