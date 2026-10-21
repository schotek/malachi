// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func fxProfile() Profile {
	return Profile{
		Text:   "  Píšu stručně.\nSchůzky v pondělí.  ",
		Gender: api.GenderMasculine,
		Accounts: []AccountNote{
			{AccountID: "acc_1", Name: "Studio Paprsek", Email: "studio@example.org", Note: "Podepisuj jako studio."},
			{AccountID: "acc_2", Name: "Osobní", Email: "me@example.org", Note: " "},
			{AccountID: "acc_3", Name: "", Email: "", Note: "Komentáře česky."},
		},
	}
}

func TestWithProfileEmpty(t *testing.T) {
	for _, p := range []Profile{
		{},
		{Gender: api.GenderUnset, Text: " \n "},
		{Accounts: []AccountNote{{AccountID: "acc_1", Note: ""}}},
	} {
		if !p.Empty() || WithProfile("System.", p) != "System." {
			t.Errorf("%+v changed the prompt", p)
		}
	}
}

func TestWithProfile(t *testing.T) {
	got := WithProfile("System.", fxProfile())
	want := "System.\n\n" + profileIntro + "\n" +
		"When writing in the user's name in a language with grammatical gender, use the masculine forms.\n\n" +
		"Píšu stručně.\nSchůzky v pondělí.\n\n" +
		"For the account \"Studio Paprsek\" (studio@example.org):\nPodepisuj jako studio.\n\n" +
		"For the account acc_3:\nKomentáře česky."
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestProfileScopes(t *testing.T) {
	p := fxProfile()
	got := WithProfile("S", p.For("acc_3"))
	if strings.Contains(got, "Studio") || !strings.Contains(got, "Komentáře česky.") || !strings.Contains(got, "Píšu stručně.") {
		t.Fatalf("For: %q", got)
	}
	if got := WithProfile("S", p.Global()); strings.Contains(got, "For the account") || !strings.Contains(got, "masculine") {
		t.Fatalf("Global: %q", got)
	}
	if got := WithProfile("S", p.For()); strings.Contains(got, "For the account") {
		t.Fatalf("For(): %q", got)
	}
	if got := WithProfile("S", p.For("")); strings.Contains(got, "For the account") {
		t.Fatalf("For(\"\"): %q", got)
	}
	// The original keeps its notes.
	if len(p.Accounts) != 3 {
		t.Fatal("For changed the profile")
	}
	// The gender alone is a profile.
	if g := (Profile{Gender: api.GenderNeutral}); g.Empty() || !strings.Contains(WithProfile("S", g), "avoid gendered forms") {
		t.Fatal("gender alone")
	}
}

func TestProfileFrom(t *testing.T) {
	p := ProfileFrom(api.AssistantProfile{
		Text: "T", Gender: api.GenderFeminine,
		Accounts: []api.ProfileAccountNote{{AccountID: "acc_1", Note: "N1"}, {AccountID: "acc_gone", Note: "N2"}},
	}, []api.Account{{ID: "acc_1", Config: api.AccountConfig{Name: "Studio", Email: "s@example.org"}}})
	want := []AccountNote{{AccountID: "acc_1", Name: "Studio", Email: "s@example.org", Note: "N1"}, {AccountID: "acc_gone", Note: "N2"}}
	if p.Text != "T" || p.Gender != api.GenderFeminine || len(p.Accounts) != 2 || p.Accounts[0] != want[0] || p.Accounts[1] != want[1] {
		t.Fatalf("%+v", p)
	}
}

func TestAccountLabel(t *testing.T) {
	for n, want := range map[AccountNote]string{
		{AccountID: "a", Name: "Studio \"X\"\nY", Email: "s@x.org"}: `For the account "Studio \"X\" Y" (s@x.org)`,
		{AccountID: "a", Email: "s@x.org"}:                          "For the account s@x.org",
		{AccountID: "a", Name: "Studio"}:                            `For the account "Studio"`,
		{AccountID: "a"}:                                            "For the account a",
	} {
		if got := accountLabel(n); got != want {
			t.Errorf("%+v: %q, want %q", n, got, want)
		}
	}
}

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
