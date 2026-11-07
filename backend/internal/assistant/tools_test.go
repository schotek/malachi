// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"slices"
	"testing"
)

func TestCalendarTools(t *testing.T) {
	want := []string{"list_calendars", "list_events", "get_event", "find_free_time", "list_invitations"}
	if !slices.Equal(CalendarTools, want) {
		t.Errorf("CalendarTools = %q", CalendarTools)
	}
	for _, tool := range CalendarTools {
		if !slices.Contains(AllowedTools, tool) {
			t.Errorf("the panel lacks %s", tool)
		}
		if slices.Contains(TriageTools, tool) || slices.Contains(TriageToolsWith(true), tool) || slices.Contains(TriageToolsWith(false), tool) {
			t.Errorf("triage has %s", tool)
		}
		if slices.Contains(SuggestReplyTools, tool) {
			t.Errorf("a suggested reply has %s", tool)
		}
	}
	for _, tool := range ProfileTools {
		if !slices.Contains(AllowedTools, tool) {
			t.Errorf("the panel lacks %s", tool)
		}
		if slices.Contains(TriageTools, tool) || slices.Contains(SuggestReplyTools, tool) {
			t.Errorf("%s beyond the panel", tool)
		}
	}
}

func TestBareTool(t *testing.T) {
	for in, want := range map[string]string{
		"mcp__malachi__read_message": "read_message",
		"read_message":               "read_message",
		"mcp__other__read_message":   "mcp__other__read_message",
		"":                           "",
	} {
		if got := BareTool(in); got != want {
			t.Errorf("BareTool(%q) = %q, want %q", in, got, want)
		}
	}
	in := []string{"mcp__malachi__a", "b"}
	got := BareTools(in)
	if !slices.Equal(got, []string{"a", "b"}) || in[0] != "mcp__malachi__a" {
		t.Errorf("BareTools(%q) = %q", in, got)
	}
	if got := BareTools(nil); len(got) != 0 {
		t.Errorf("BareTools(nil) = %q", got)
	}
}
