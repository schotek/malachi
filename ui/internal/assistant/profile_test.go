// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"testing"
)

func TestParseProposalResult(t *testing.T) {
	for in, want := range map[string]string{
		"profile note proposal p_1 is pending; the user accepts it":        "p_1",
		"profile note proposal p_1 is pending;":                            "p_1",
		"profile note proposal p_1 is pending; …\nprofile note proposal x": "p_1",
	} {
		if got, ok := ParseProposalResult(in); !ok || got != want {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
	for _, in := range []string{
		"",
		"x profile note proposal p_1 is pending;",
		"profile note proposal  is pending;",
		"profile note proposal p 1 is pending;",
		"profile note proposal p_1\u0007 is pending;",
		"profile note proposal p_1 is pending;x",
		"profile note proposal p_1 is pending",
		"note\nprofile note proposal p_1 is pending;",
	} {
		if got, ok := ParseProposalResult(in); ok {
			t.Errorf("%q parsed as %q", in, got)
		}
	}
}

func TestProfileProposalCount(t *testing.T) {
	tr := identity{}
	if got := ProfileProposalCount(tr, 0); got != "Edit What the Assistant Knows About You…" {
		t.Errorf("0: %q", got)
	}
	if got := ProfileProposalCount(tr, 2); got != "Edit What the Assistant Knows About You… (2)" {
		t.Errorf("2: %q", got)
	}
	if got := ProfileCounter(tr, 12, 2000); got != "12 / 2000" {
		t.Errorf("counter %q", got)
	}
}
