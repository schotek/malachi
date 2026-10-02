// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The board triage's command line (macOS AssistantTriageTests, the part
// without a process; the request with the bridge is pinned by
// ui/internal/assistantpanel's oneshot_tools_test.go).

// The triage's command line of a manual run: the panel's, with the bridge
// started for the run with its limit and only the triage's tools,
// create_draft included; nothing of the modify or send tiers.
func TestArgsTriage(t *testing.T) {
	got := Args(Options{
		Bridge: "/b/malachi-mcp", Socket: "/s.sock", Model: Opus, SystemPrompt: "P",
		BridgeArgs: TriageBridgeArgs("run_1", 40), Tools: TriageToolsFor(TriageManual),
	})
	want := []string{
		"-p", "--verbose", "--output-format", "stream-json", "--include-partial-messages",
		"--input-format", "stream-json",
		"--tools", "", "--disallowedTools", "LSP", "--disable-slash-commands", "--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config",
		`{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_1","--triage-max","40"]}}}`,
		"--allowedTools",
		"mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages," +
			"mcp__malachi__read_message,mcp__malachi__get_attachment,mcp__malachi__create_draft," +
			"mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
		"--permission-mode", "dontAsk", "--no-session-persistence",
		"--model", "opus", "--system-prompt", "P",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Args =\n%q\nwant\n%q", got, want)
	}
	all := strings.Join(got, " ")
	if strings.Contains(all, "allow-modify") || strings.Contains(all, "allow-send") {
		t.Errorf("a modify or send tier: %s", all)
	}
	for _, tool := range TriageTools {
		if !strings.HasPrefix(tool, "mcp__malachi__") {
			t.Errorf("tool %q is not the bridge's", tool)
		}
	}
	// Only the triage tools on top of the panel's read and draft tools.
	var extra []string
	for _, tool := range TriageTools {
		if !slices.Contains(AllowedTools, tool) {
			extra = append(extra, tool)
		}
	}
	if want := []string{"mcp__malachi__list_triage_queue", "mcp__malachi__annotate_case", "mcp__malachi__add_commitment"}; !slices.Equal(extra, want) {
		t.Errorf("beyond the panel's tools: %q", extra)
	}
	for _, tool := range AllowedTools {
		if !slices.Contains(TriageTools, tool) {
			t.Errorf("the panel's %q is not a triage tool", tool)
		}
	}
}

// An automatic run's command line: the manual one without create_draft
// (TriageAutomaticDrafts), with its own limit for the bridge.
func TestArgsTriageAutomatic(t *testing.T) {
	if TriageAutomaticDrafts {
		t.Error("automatic runs make drafts")
	}
	got := Args(Options{
		Bridge: "/b/malachi-mcp", Socket: "/s.sock", Model: Opus, SystemPrompt: "P",
		BridgeArgs: TriageBridgeArgs("run_2", 5), Tools: TriageToolsFor(TriageAutomatic),
	})
	want := []string{
		"-p", "--verbose", "--output-format", "stream-json", "--include-partial-messages",
		"--input-format", "stream-json",
		"--tools", "", "--disallowedTools", "LSP", "--disable-slash-commands", "--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config",
		`{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_2","--triage-max","5"]}}}`,
		"--allowedTools",
		"mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages," +
			"mcp__malachi__read_message,mcp__malachi__get_attachment," +
			"mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
		"--permission-mode", "dontAsk", "--no-session-persistence",
		"--model", "opus", "--system-prompt", "P",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Args =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "create_draft") {
		t.Error("an automatic run has create_draft")
	}
	if !TriageDrafts(TriageManual) || TriageDrafts(TriageAutomatic) {
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

// The bridge's extra arguments follow --socket, and stand alone without
// it; the JSON escapes them as encoding/json does.
func TestMCPConfigExtra(t *testing.T) {
	tests := []struct {
		bridge, socket string
		extra          []string
		want           string
	}{
		{"/b", "", []string{"--allow-triage", "--triage-run", "r"},
			`{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--allow-triage","--triage-run","r"]}}}`},
		{"/b", "/s", []string{`a"b\`},
			`{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--socket","/s","a\"b\\"]}}}`},
		{"/b", "", nil, `{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":[]}}}`},
	}
	for _, tt := range tests {
		if got := mcpConfig(tt.bridge, tt.socket, tt.extra...); got != tt.want {
			t.Errorf("mcpConfig(%q, %q, %q) = %s, want %s", tt.bridge, tt.socket, tt.extra, got, tt.want)
		}
	}
	// Unchanged without extra arguments.
	if mcpConfig("/b", "/s") != mcpConfig("/b", "/s", []string{}...) {
		t.Error("an empty extra changes the JSON")
	}
	// Without a bridge, neither the extra arguments nor the tools count.
	oneShot := Args(Options{SystemPrompt: "P", BridgeArgs: []string{"--allow-triage"}, Tools: TriageTools})
	if !slices.Equal(oneShot, Args(Options{SystemPrompt: "P"})) {
		t.Errorf("without a bridge: %q", oneShot)
	}
	// Tools nil is the panel's; an empty list is none.
	panel := Args(Options{Bridge: "/b"})
	if !slices.Contains(panel, strings.Join(AllowedTools, ",")) {
		t.Errorf("nil tools: %q", panel)
	}
	none := Args(Options{Bridge: "/b", Tools: []string{}})
	if i := slices.Index(none, "--allowedTools"); i < 0 || none[i+1] != "" {
		t.Errorf("no tools: %q", none)
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
