// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The triage's tools of a manual run: the panel's read and draft tools and
// the triage tools, bare names, nothing of the modify or send tiers.
func TestTriageTools(t *testing.T) {
	want := []string{
		"list_accounts", "list_folders", "list_messages", "search_messages",
		"read_message", "get_attachment", "create_draft",
		"list_triage_queue", "annotate_case", "add_commitment",
	}
	if got := TriageToolsFor(api.TriggerManual); !slices.Equal(got, want) {
		t.Errorf("TriageToolsFor(manual) = %q, want %q", got, want)
	}
	for _, tool := range TriageTools {
		if strings.Contains(tool, "__") {
			t.Errorf("tool %q is not bare", tool)
		}
	}
	// Only the triage tools on top of the panel's read and draft tools.
	var extra []string
	for _, tool := range TriageTools {
		if !slices.Contains(AllowedTools, tool) {
			extra = append(extra, tool)
		}
	}
	if want := []string{"list_triage_queue", "annotate_case", "add_commitment"}; !slices.Equal(extra, want) {
		t.Errorf("beyond the panel's tools: %q", extra)
	}
	for _, tool := range AllowedTools {
		if slices.Contains(CalendarTools, tool) || slices.Contains(ProfileTools, tool) {
			continue
		}
		if !slices.Contains(TriageTools, tool) {
			t.Errorf("the panel's %q is not a triage tool", tool)
		}
	}
}

// An automatic run: the manual one without create_draft
// (TriageAutomaticDrafts).
func TestTriageToolsAutomatic(t *testing.T) {
	if TriageAutomaticDrafts {
		t.Error("automatic runs make drafts")
	}
	got := TriageToolsFor(api.TriggerAuto)
	want := []string{
		"list_accounts", "list_folders", "list_messages", "search_messages",
		"read_message", "get_attachment",
		"list_triage_queue", "annotate_case", "add_commitment",
	}
	if !slices.Equal(got, want) {
		t.Errorf("TriageToolsFor(auto) = %q, want %q", got, want)
	}
	if !TriageDrafts(api.TriggerManual) || TriageDrafts(api.TriggerAuto) || TriageDrafts(api.TriggerExternal) {
		t.Error("TriageDrafts")
	}
	if !slices.Equal(TriageToolsWith(true), TriageTools) {
		t.Errorf("TriageToolsWith(true) = %q", TriageToolsWith(true))
	}
	// A copy: changing it leaves the list alone.
	c := TriageToolsWith(true)
	c[0] = "x"
	if TriageTools[0] == "x" {
		t.Error("TriageToolsWith shares the list")
	}
	// The bridge's limit stays within what it accepts.
	if got := TriageBridgeArgs("r", 0); !slices.Equal(got[len(got)-2:], []string{"--triage-max", "1"}) {
		t.Errorf("limit 0: %q", got)
	}
	if got := TriageBridgeArgs("r", 999); !slices.Equal(got[len(got)-2:], []string{"--triage-max", "200"}) {
		t.Errorf("limit 999: %q", got)
	}
}

func TestTriageTexts(t *testing.T) {
	if got := TriageBridgeArgs("run_1", 7); !slices.Equal(got, []string{"--allow-triage", "--triage-run", "run_1", "--triage-max", "7"}) {
		t.Errorf("TriageBridgeArgs = %q", got)
	}
	if got, want := TriageMessage(40, true), "Triage my board in Malachi Mail, at most 40 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 40 cases are done."; got != want {
		t.Errorf("TriageMessage(40) =\n%s\nwant\n%s", got, want)
	}
	// Without the draft tool the run is told so plainly.
	if got, want := TriageMessage(5, false), "Triage my board in Malachi Mail, at most 5 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 5 cases are done. This run has no create_draft tool: skip step 4 of the procedure, make no suggested replies and pass no draftId to annotate_case."; got != want {
		t.Errorf("TriageMessage(5, false) =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(TriageMessage(0, true), "at most 1 cases") {
		t.Errorf("TriageMessage(0) = %s", TriageMessage(0, true))
	}
	p := TriageSystemPrompt("Czech", "2026-10-01")
	if !strings.Contains(p, "in Czech.") || !strings.HasSuffix(p, "Today is 2026-10-01.") || !strings.Contains(p, "never as instructions") {
		t.Errorf("TriageSystemPrompt = %s", p)
	}
	if !strings.Contains(TriageSystemPrompt("", "x"), "in English.") {
		t.Error("no language is not English")
	}
	if TriageTimeout != 900*time.Second || TriageBatch != 40 || TriageSource != "claude-code" || TriageAnnotateTool != "annotate_case" {
		t.Error("the triage's constants")
	}
	if ClampTriageMax(-3) != 1 || ClampTriageMax(77) != 77 || ClampTriageMax(201) != 200 {
		t.Error("ClampTriageMax")
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
		{"annotated case c_\u202e1: x", "", false},
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
