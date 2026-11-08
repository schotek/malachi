// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The board's triage run (docs/mcp.md "Triage of the board", docs/security.md
// §10.2): the pure half of the one-shot request ui/internal/boardtriage
// sends the assistant. The prompt, the tools (the bridge's read tools,
// create_draft for a manual run only, and the three triage tools) and the
// bridge's arguments ("--allow-triage --triage-run <runId> --triage-max
// <limit>", never --allow-modify or --allow-send) are the daemon's
// (assistant.prompt, purpose triage, api.TriageInput with the trigger and
// the run); what stays here is the run's rules the client keeps: who
// starts it, whether it may draft, how many cases, how long, and how the
// bridge's results read. The macOS client leads (MalachiCore
// Assistant/AssistantTriage.swift); this is its port.
//
// This file holds no translatable text.

import (
	"strings"
	"time"
)

// TriageTrigger is who starts a triage run (macOS Board.TriageTrigger).
type TriageTrigger int

// The triggers.
const (
	// TriageManual is the user's Triage button.
	TriageManual TriageTrigger = iota
	// TriageAutomatic is the application's schedule.
	TriageAutomatic
)

// TriageAutomaticDrafts is the rule (kept when suggested replies became
// local drafts) that an automatic run creates
// no drafts. Nobody watches it; a suggested reply stays on this device as
// the case's local draft (never in the Drafts folder, docs/mcp.md "A
// suggested reply on the board"), but it is still written to the
// recipients the original names, maybe from a hostile Reply-To, on a Jira
// case it is a public comment draft, and the user edits and sends it from
// the board itself. So an automatic run gets no create_draft and is told
// to skip suggested replies; a manual run, which the user started and
// watches, keeps it. The daemon enforces the same for an automatic
// trigger (backend/internal/assistant TriageAutomaticDrafts); flip both
// to give automatic runs drafts again.
const TriageAutomaticDrafts = false

// TriageDrafts says whether a run started by t may create drafts.
func TriageDrafts(t TriageTrigger) bool {
	return t == TriageManual || TriageAutomaticDrafts
}

// TriageAnnotateTool is the triage tool whose accepted calls are the run's
// progress (without the bridge's prefix, as Event.Tool names it).
const TriageAnnotateTool = "annotate_case"

// triageAnnotatedPrefix starts the text of every accepted annotate_case
// result: "annotated case <caseId>: …" (the bridge's annotateCase).
const triageAnnotatedPrefix = "annotated case "

// TriageAnnotatedCase is the case an accepted annotate_case result names
// (its text, Event.ResultText), false when the text does not start the
// bridge's way or the id is not one: 1 to 64 bytes of ASCII letters,
// digits, "_" and "-". The bridge accepts a second annotation of a case
// without charging another of the run's cases, so the run counts the
// distinct cases.
func TriageAnnotatedCase(result string) (string, bool) {
	rest, ok := strings.CutPrefix(result, triageAnnotatedPrefix)
	if !ok {
		return "", false
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok || id == "" || len(id) > 64 {
		return "", false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", false
		}
	}
	return id, true
}

// The range the bridge's --triage-max accepts (the daemon clamps the
// run's limit to it).
const (
	TriageMaxLowest  = 1
	TriageMaxHighest = 200
)

// ClampTriageMax is n within TriageMaxLowest..TriageMaxHighest.
func ClampTriageMax(n int) int {
	return min(max(n, TriageMaxLowest), TriageMaxHighest)
}

// TriageSource is the run's source for board.runStart: what the bridge
// reports as its client's name for the annotations of the run.
const TriageSource = "claude-code"

// TriageTimeout is how long a triage run may take before it is ended as a
// timeout.
const TriageTimeout = 15 * time.Minute

// TriageBatch is the most cases one run is asked to triage (a manual run,
// and the most an automatic run takes of the day's remaining cases).
const TriageBatch = 40
