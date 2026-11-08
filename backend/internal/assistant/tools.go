// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import "strings"

// The bridge's tools each purpose may call, by their bare names: the
// names malachi-mcp registers (backend/cmd/malachi-mcp). A client that
// runs Claude Code names the bridge "malachi" in its --mcp-config and
// prefixes them with ClaudeToolPrefix for --allowedTools; the daemon's own
// sessions offer them to the model as they are.

// ClaudeToolPrefix is the prefix Claude Code gives the tools of an MCP
// server called "malachi" (mcp__<server>__<tool>). BareTool takes it off.
const ClaudeToolPrefix = "mcp__malachi__"

// BareTool is name without ClaudeToolPrefix, when it has it.
func BareTool(name string) string {
	return strings.TrimPrefix(name, ClaudeToolPrefix)
}

// BareTools are names with ClaudeToolPrefix taken off each; a copy.
func BareTools(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = BareTool(n)
	}
	return out
}

// AllowedTools are the bridge's tools the panel's conversation may run,
// in this order: reading and drafting only. The bridge the panel starts
// has no --allow-modify or --allow-send, so nothing else would exist
// anyway.
var AllowedTools = []string{
	"list_accounts",
	"list_folders",
	"list_messages",
	"search_messages",
	"read_message",
	"get_attachment",
	"create_draft",
	// The calendar's read-only tools (CalendarTools, docs/calendar.md):
	// the panel's conversation only, not the triage's or the suggested
	// reply's.
	"list_calendars",
	"list_events",
	"get_event",
	"find_free_time",
	"list_invitations",
	// The user's profile (ProfileTools): the panel's conversation only.
	"get_user_profile",
	"propose_profile_note",
}

// CalendarTools are the calendar's read-only bridge tools the panel's
// conversation may run (they are part of AllowedTools; the board's triage
// and a suggested reply do not get them).
var CalendarTools = []string{
	"list_calendars",
	"list_events",
	"get_event",
	"find_free_time",
	"list_invitations",
}

// ProfileTools are the bridge's profile tools the panel's conversation
// may use (AllowedTools); never a one-shot request, triage or a suggested
// reply.
var ProfileTools = []string{
	"get_user_profile",
	"propose_profile_note",
}

// TriageDraftTool is the tool that creates a draft, which an automatic
// triage run goes without.
const TriageDraftTool = "create_draft"

// TriageTools are the tools of a triage run that may create drafts, in
// this order: the bridge's read tools of the panel, create_draft (a
// suggested reply, linked by annotate_case), and the triage tools.
var TriageTools = []string{
	"list_accounts",
	"list_folders",
	"list_messages",
	"search_messages",
	"read_message",
	"get_attachment",
	TriageDraftTool,
	"list_triage_queue",
	"annotate_case",
	"add_commitment",
}

// TriageAnnotateTool is the triage tool whose accepted calls are the
// run's progress.
const TriageAnnotateTool = "annotate_case"

// SuggestReplyTools are the tools of a suggested reply, in this order.
var SuggestReplyTools = []string{
	"read_message",
	"list_messages",
	"create_draft",
}

// SuggestReplyDraftTool is the tool whose result names the draft of a
// suggested reply.
const SuggestReplyDraftTool = "create_draft"
