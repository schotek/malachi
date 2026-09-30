// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestCanTransition(t *testing.T) {
	jira := api.Account{Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition}}
	if !CanTransition(jira) {
		t.Error("an account with the transition capability offers the menu")
	}
	if CanTransition(api.Account{Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward}}) {
		t.Error("an account without it does not")
	}
	if CanTransition(api.Account{Capabilities: []api.AccountCapability{}}) {
		t.Error("an empty list offers nothing")
	}
	if CanTransition(api.Account{}) {
		t.Error("a mail account (nil capabilities, the mail default) never changes statuses")
	}
}

func TestTransitions(t *testing.T) {
	res := api.IssueTransitionsResult{
		Issue: api.IssueInfo{Key: "ITSD-42", Status: "To Do", StatusCategory: api.StatusCategoryTodo},
		Transitions: []api.IssueTransition{
			{ID: "11", Name: "Start Progress", To: "In Progress", ToCategory: api.StatusCategoryInProgress},
			{ID: "21", Name: "Done", To: "Done", ToCategory: api.StatusCategoryDone},
			{ID: "31", Name: "Resolve", To: "Resolved", NeedsInput: true},
			{ID: "41", Name: " " + rlo + "Escalate\n", To: "escalated" + zwsp, NeedsInput: true},
			{ID: "  ", Name: "No id", To: "Nowhere"},
			{ID: "51", Name: "", To: "Closed"},
			{ID: "61", Name: zwsp + shy, To: rlo},
			{ID: "71", Name: "DONE", To: "Done"},
			{ID: "81", Name: "Back to the backlog", To: "to do"},
		},
	}
	want := []TransitionItem{
		{ID: "11", Title: "Start Progress", Target: "In Progress", Subtitle: "In Progress", Enabled: true},
		{ID: "21", Title: "Done", Target: "Done", Enabled: true},
		{ID: "31", Title: "Resolve", Target: "Resolved", Subtitle: "Resolved", Hint: "Needs fields in Jira"},
		{ID: "41", Title: "Escalate", Target: "escalated", Subtitle: "escalated", Hint: "Needs fields in Jira"},
		{ID: "51", Title: "Closed", Target: "Closed", Enabled: true},
		{ID: "71", Title: "DONE", Target: "Done", Enabled: true},
	}
	if got := Transitions(res, tr); !reflect.DeepEqual(got, want) {
		t.Errorf("Transitions =\n%+v\nwant\n%+v", got, want)
	}
	if got := Transitions(api.IssueTransitionsResult{}, tr); got == nil || len(got) != 0 {
		t.Errorf("no transitions: got %#v, want an empty list", got)
	}
	// The hint comes from the catalogue.
	cs := catalog{{"", "Needs fields in Jira"}: "Vyžaduje pole v Jiře"}
	if got := Transitions(res, cs); got[2].Hint != "Vyžaduje pole v Jiře" || got[0].Hint != "" {
		t.Errorf("hints = %q, %q", got[2].Hint, got[0].Hint)
	}
}

func TestTransitionsCap(t *testing.T) {
	res := api.IssueTransitionsResult{Transitions: make([]api.IssueTransition, api.MaxIssueTransitions+5)}
	for i := range res.Transitions {
		res.Transitions[i] = api.IssueTransition{ID: strings.Repeat("1", i+1), Name: "t"}
	}
	if got := Transitions(res, tr); len(got) != api.MaxIssueTransitions {
		t.Errorf("%d items, want %d", len(got), api.MaxIssueTransitions)
	}
}

func TestTransitionTexts(t *testing.T) {
	if got := ChangeStatusLabel(tr); got != "Change Status" {
		t.Errorf("ChangeStatusLabel = %q", got)
	}
	if got := NeedsInputHint(tr); got != "Needs fields in Jira" {
		t.Errorf("NeedsInputHint = %q", got)
	}
	if got := TransitionsLoading(tr); got != "Loading…" {
		t.Errorf("TransitionsLoading = %q", got)
	}
	if got := NoTransitions(tr); got != "No status change is available" {
		t.Errorf("NoTransitions = %q", got)
	}
	if got := LoadTransitionsAction(tr); got != "Loading the status changes" {
		t.Errorf("LoadTransitionsAction = %q", got)
	}
	if got := TransitionAction(tr); got != "Changing the status" {
		t.Errorf("TransitionAction = %q", got)
	}
}

func TestStatusChanged(t *testing.T) {
	refreshed := api.IssueInfo{Key: "ITSD-42", Status: "In Progress"}
	stale := api.IssueInfo{Key: "ITSD-42", Status: "To Do"}
	tests := []struct {
		name   string
		chosen TransitionItem
		issue  api.IssueInfo
		want   string
	}{
		{"the target wins", TransitionItem{Title: "Start Progress", Target: "In Progress"}, refreshed, "Status changed to In Progress"},
		{"even over a stale issue", TransitionItem{Title: "Start Progress", Target: "In Progress"}, stale, "Status changed to In Progress"},
		{"no target: the issue's status", TransitionItem{Title: "Start Progress"}, refreshed, "Status changed to In Progress"},
		{"neither: the transition's name", TransitionItem{Title: "Start Progress"}, api.IssueInfo{Status: rlo + " "}, "Status changed to Start Progress"},
	}
	for _, tt := range tests {
		if got := StatusChanged(tt.chosen, tt.issue, tr); got != tt.want {
			t.Errorf("%s: StatusChanged = %q, want %q", tt.name, got, tt.want)
		}
	}
	cs := catalog{{"", "Status changed to %s"}: "Stav změněn na %s"}
	if got := StatusChanged(TransitionItem{Target: "Probíhá"}, refreshed, cs); got != "Stav změněn na Probíhá" {
		t.Errorf("Czech: %q", got)
	}
}

func TestTransitionFailed(t *testing.T) {
	const fallback = "Changing the status failed: the server returned an error"
	tests := []struct {
		name    string
		code    api.ErrorCode
		message string
		want    string
	}{
		{"the site's reason", api.CodeServerError, "Transition is not allowed by the workflow", "The status could not be changed: Transition is not allowed by the workflow"},
		{"cleaned", api.CodeServerError, " " + rlo + "Not\nallowed" + zwsp, "The status could not be changed: Not allowed"},
		{"no reason: the usual sentence", api.CodeServerError, zwsp, fallback},
		{"another code: the usual sentence", api.CodeInvalidArgument, "needs input", fallback},
		{"network", api.CodeNetworkError, "dial tcp: refused", fallback},
	}
	for _, tt := range tests {
		if got := TransitionFailed(tt.code, tt.message, fallback, tr); got != tt.want {
			t.Errorf("%s: TransitionFailed = %q, want %q", tt.name, got, tt.want)
		}
	}
	long := TransitionFailed(api.CodeServerError, strings.Repeat("x", maxText+10), fallback, tr)
	if len(long) > len("The status could not be changed: ")+maxText {
		t.Errorf("the reason is not capped: %d bytes", len(long))
	}
}
