// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"
	"time"
)

// The board triage's command line (macOS AssistantTriageTests, the part
// without a process; the request with the bridge is pinned by
// ui/internal/assistantpanel's oneshot_tools_test.go).

func TestTriageTexts(t *testing.T) {
	if TriageTimeout != 900*time.Second || TriageBatch != 40 || TriageSource != "claude-code" || TriageAnnotateTool != "annotate_case" {
		t.Error("the triage's constants")
	}
	if ClampTriageMax(-3) != 1 || ClampTriageMax(77) != 77 || ClampTriageMax(201) != 200 {
		t.Error("ClampTriageMax")
	}
	if TriageAutomaticDrafts {
		t.Error("automatic runs make drafts")
	}
	if !TriageDrafts(TriageManual) || TriageDrafts(TriageAutomatic) {
		t.Error("TriageDrafts")
	}
}

func TestTriageAnnotatedCase(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"annotated case c_0123456789abcdef0123456789abcdef: state in effect hot (decided by rules)", "c_0123456789abcdef0123456789abcdef", true},
		{"annotated case c_1: x\nannotations left in this session: 3", "c_1", true},
		{"annotated case : x", "", false},
		{"annotated case c 1: x", "", false},
		{"annotated case c_1", "", false},
		{"Annotated case c_1: x", "", false},
		{"this session already annotated 3 cases", "", false},
		{"annotated case c_‮1: x", "", false},
		{"annotated case " + strings.Repeat("a", 65) + ": x", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := TriageAnnotatedCase(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("TriageAnnotatedCase(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
