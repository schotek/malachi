// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"testing"
)

func TestButtonOpensPanel(t *testing.T) {
	tests := []struct {
		target   Target
		hasPanel bool
		want     bool
	}{
		{App, true, true},
		{App, false, false},
		{Desktop, true, false},
		{Code, true, false},
		{Desktop, false, false},
		{Code, false, false},
		{"", true, false},
		{"unknown", true, false},
	}
	for _, tt := range tests {
		if got := ButtonOpensPanel(tt.target, tt.hasPanel); got != tt.want {
			t.Errorf("ButtonOpensPanel(%q, %v) = %v, want %v", tt.target, tt.hasPanel, got, tt.want)
		}
	}
}

func TestPanelActions(t *testing.T) {
	want := []Action{Summarize, DraftReply, Tasks, Unread}
	if !slices.Equal(PanelActions, want) {
		t.Errorf("PanelActions = %q, want %q", PanelActions, want)
	}
	for _, a := range PanelActions {
		if Label(identity{}, a) == "" {
			t.Errorf("no label for %q", a)
		}
	}
}
