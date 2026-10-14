// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"testing"
)

func TestMessageActionsFor(t *testing.T) {
	for _, n := range []int{0, 1} {
		if got := MessageActionsFor(n); !slices.Equal(got, MessageActions) {
			t.Errorf("%d: %v", n, got)
		}
	}
	if got := MessageActionsFor(3); !slices.Equal(got, []Action{Summarize, Tasks, Ask}) {
		t.Errorf("several: %v", got)
	}
	if got := LabelFor(identity{}, Ask, 2); got != "Ask About These Messages…" {
		t.Errorf("ask several: %q", got)
	}
	if got := LabelFor(identity{}, Ask, 1); got != Label(identity{}, Ask) {
		t.Errorf("ask one: %q", got)
	}
	if got := LabelFor(identity{}, Summarize, 4); got != Label(identity{}, Summarize) {
		t.Errorf("summarize several: %q", got)
	}
}
