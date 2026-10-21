// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

func TestProposalCardFor(t *testing.T) {
	for _, c := range []struct {
		o     proposalOutcome
		found bool
		want  proposalCard
	}{
		{proposalPending, true, proposalCard{Buttons: true}},
		// Resolved elsewhere, or not read yet: no buttons.
		{proposalPending, false, proposalCard{}},
		{proposalSaved, false, proposalCard{Saved: true}},
		{proposalSaved, true, proposalCard{Saved: true}},
		{proposalDismissed, true, proposalCard{Hidden: true}},
	} {
		if got := proposalCardFor(c.o, c.found); got != c.want {
			t.Errorf("%v %v: %+v, want %+v", c.o, c.found, got, c.want)
		}
	}
}

func TestProposalAccountLine(t *testing.T) {
	texts := assistant.ProfileTexts(tr)
	notes := []assistant.AccountNote{
		{AccountID: "acc_1", Name: "Studio", Email: "s@example.org"},
		{AccountID: "acc_2", Email: "me@example.org"},
	}
	for acc, want := range map[api.AccountID]string{
		"":         "",
		"acc_1":    "For the account Studio",
		"acc_2":    "For the account me@example.org",
		"acc_gone": "For the account acc_gone",
	} {
		if got := proposalAccountLine(texts, api.ProfileProposal{ID: "p", AccountID: acc}, notes); got != want {
			t.Errorf("%q: %q, want %q", acc, got, want)
		}
	}
}
