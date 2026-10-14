// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import "testing"

func TestSameCommitment(t *testing.T) {
	const q = "S magnetkou ti pomůžu, jestli budeš chtít"
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{q, q, true},
		{q, "S magnetkou ti pomůžu", true},
		{"ti pomůžu", q + ", stačí napsat.", true},
		{q, "S mag​netkou ti  pomůžu,⁠ jestli‮ budeš chtít", true},
		{q, "S magnetkou ti pomůžu, jestli budeš chtít", true},
		{`budeš "chtít"`, "budeš „chtít“", true},
		{"I'll send it", "I’ll send it", true},
		{"send it https://evil.example/a today", "send it today", false},
		{"send it https://evil.example/a today", "send it https://other.example/b today", true},
		{q, "Fotky pošlu v pondělí", false},
		{q, "", false},
		{"​​", "​", false},
	} {
		if got := SameCommitment(c.a, c.b); got != c.want {
			t.Errorf("SameCommitment(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := SameCommitment(c.b, c.a); got != c.want {
			t.Errorf("SameCommitment(%q, %q) = %v, want %v (swapped)", c.b, c.a, got, c.want)
		}
	}
}
