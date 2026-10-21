// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardreply

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// A suggested reply gets the profile with the note of the case's account
// only.
func TestReplyCarriesTheCaseAccountsNote(t *testing.T) {
	fake := newFakeClaude(t, "true", draftTurn("d_9", "acc_1", ""))
	h := newReplyHarness(t, fake, true, testBridge)
	h.c.Profile = func() assistant.Profile {
		return assistant.Profile{Text: "Píšu stručně.", Accounts: []assistant.AccountNote{
			{AccountID: "acc_1", Note: "Podepisuj jako studio."},
			{AccountID: "acc_2", Note: "Tykej."},
		}}
	}
	h.c.Start(boardCase("1"), "")
	h.ended()
	joined := strings.Join(fake.args(), "\n")
	if !strings.Contains(joined, "Píšu stručně.") || !strings.Contains(joined, "Podepisuj jako studio.") || strings.Contains(joined, "Tykej.") {
		t.Errorf("args %q", fake.args())
	}
}
