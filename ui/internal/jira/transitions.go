// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"fmt"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The status of an issue is changed from the Change Status menu (the
// status pill of the issue card, More Actions, the Message menu) on an
// account with api.CapabilityTransition: the menu lists what
// issue.transitions returned, in the site's order, the transitions that
// need fields in Jira disabled with a hint, and choosing one calls
// issue.transition. The daemon refreshes the issue right after, so the
// event row and the new status arrive through the usual notifications;
// the client applies the result's issue to the card at once all the same.

// TransitionItem is one entry of the Change Status menu.
type TransitionItem struct {
	// ID goes to issue.transition.
	ID string
	// Title is the transition's name, as the site's own status menu shows
	// it; Target the status it leads to ("" when the site did not say).
	Title, Target string
	// Subtitle is Target when it differs from Title ("Start Progress" →
	// "In Progress"), "" otherwise.
	Subtitle string
	// Enabled is false for a transition that needs fields in Jira
	// (NeedsInput): the item is listed disabled, with Hint saying why
	// ("" when enabled).
	Enabled bool
	Hint    string
}

// CanTransition reports an account whose issues offer the Change Status
// menu.
func CanTransition(a api.Account) bool {
	return a.Can(api.CapabilityTransition)
}

// Transitions builds the items of the Change Status menu from the result
// of issue.transitions: the site's order, every string cleaned, a
// transition without an id skipped, one without a name shown by its
// target, at most api.MaxIssueTransitions. A transition that leads to the
// status the issue already has is left out: a workflow with global
// transitions offers one for every status, the current one included.
func Transitions(res api.IssueTransitionsResult, tr Translator) []TransitionItem {
	out := make([]TransitionItem, 0, len(res.Transitions))
	current := Clean(res.Issue.Status)
	for _, t := range res.Transitions {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			continue
		}
		title, target := Clean(t.Name), Clean(t.To)
		if title == "" {
			title = target
		}
		if title == "" {
			continue
		}
		if current != "" && target != "" && strings.EqualFold(target, current) {
			continue
		}
		item := TransitionItem{ID: id, Title: title, Target: target, Enabled: !t.NeedsInput}
		if target != "" && !strings.EqualFold(target, title) {
			item.Subtitle = target
		}
		if t.NeedsInput {
			item.Hint = NeedsInputHint(tr)
		}
		out = append(out, item)
		if len(out) == api.MaxIssueTransitions {
			break
		}
	}
	return out
}

// ChangeStatusLabel is the title of the menu.
func ChangeStatusLabel(tr Translator) string {
	// TRANSLATORS: menu of the status changes a Jira issue allows.
	return tr.T("Change Status")
}

// NeedsInputHint says why a transition is listed disabled.
func NeedsInputHint(tr Translator) string {
	// TRANSLATORS: a status change of a Jira issue asks for more on the site and cannot be made here.
	return tr.T("Needs fields in Jira")
}

// TransitionsLoading is the menu's only item while issue.transitions runs.
func TransitionsLoading(tr Translator) string {
	return tr.T("Loading…")
}

// NoTransitions is the menu's only item when the site offers no status
// change on the issue.
func NoTransitions(tr Translator) string {
	// TRANSLATORS: the only item of the Change Status menu of a Jira issue whose status cannot be changed.
	return tr.T("No status change is available")
}

// LoadTransitionsAction is what failed when issue.transitions did, in the
// progressive form RPCErrorText takes ("Loading the status changes failed:
// …"); the menu's only item then.
func LoadTransitionsAction(tr Translator) string {
	// TRANSLATORS: progressive; %s of "%s failed: …" when the status changes of a Jira issue could not be listed.
	return tr.T("Loading the status changes")
}

// TransitionAction is what failed when issue.transition did, in the
// progressive form RPCErrorText takes ("Changing the status timed out").
func TransitionAction(tr Translator) string {
	// TRANSLATORS: progressive; %s of "%s failed: …" when the status of a Jira issue could not be changed.
	return tr.T("Changing the status")
}

// StatusChanged is the toast after issue.transition: the status the chosen
// transition leads to (the change was made even when the daemon's refresh
// did not finish in time, and issue then still shows the old status), else
// the refreshed issue's status, else the transition's name.
func StatusChanged(chosen TransitionItem, issue api.IssueInfo, tr Translator) string {
	status := chosen.Target
	if status == "" {
		status = Clean(issue.Status)
	}
	if status == "" {
		status = chosen.Title
	}
	// TRANSLATORS: toast; %s is the new status of a Jira issue, such as "In Progress".
	return fmt.Sprintf(tr.T("Status changed to %s"), status)
}

// TransitionFailed is the toast of a failed issue.transition: the site's
// own reason when it refused the change (a serverError whose message the
// daemon took from the site; cleaned again here), else fallback, the
// client's usual sentence for the error (RPCErrorText with
// TransitionAction).
func TransitionFailed(code api.ErrorCode, message, fallback string, tr Translator) string {
	if code == api.CodeServerError {
		if reason := Clean(message); reason != "" {
			// TRANSLATORS: toast; %s is the reason the Jira site gave, in its own words.
			return fmt.Sprintf(tr.T("The status could not be changed: %s"), reason)
		}
	}
	return fallback
}
