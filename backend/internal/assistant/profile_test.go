// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

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
