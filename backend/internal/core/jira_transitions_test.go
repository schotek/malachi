// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Status transitions on the production stack: issue.transitions lists what
// the site offers on the issue of any of its messages, issue.transition
// performs one and answers with the refreshed issue, whose new status and
// event row the listings show. A mail account, a transition that needs
// input and the site's refusals are mapped to the contract.
func TestJiraTransitions(t *testing.T) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) { testJiraTransitions(t, mode) })
	}
}

func testJiraTransitions(t *testing.T, mode jiratest.Mode) {
	var printer *jiratest.Issue
	e := startJiraE2E(t, mode, nil, func(f *jiratest.Server) {
		printer = f.AddIssue("ITSD", "Printer on the 2nd floor", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		f.AddComment(printer.ID, f.Petr, "<p>Still jammed.</p>")
	})
	ctx, b, f, id := e.ctx, e.b, e.f, e.id
	list, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil || !list.Accounts[0].Can(api.CapabilityTransition) {
		t.Fatalf("capabilities: %+v %v", list, err)
	}
	_, byName := e.folders()
	itsd := byName["IT Service Desk"]
	comment := item(t, e.list(itsd.ID), printer.Key, api.IssueItemComment)

	// Listed by the comment: the issue as stored, the site's transitions in
	// its order, those that need input marked.
	res, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: comment.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Issue.Key != printer.Key || res.Issue.Status != "To Do" || res.Issue.StatusCategory != api.StatusCategoryTodo ||
		res.Issue.URL != f.Site.String()+"/browse/"+printer.Key || len(res.Issue.CommentVisibilities) != 2 {
		t.Fatalf("issue = %+v", res.Issue)
	}
	want := []api.IssueTransition{
		{ID: "11", Name: "Start Progress", To: "In Progress", ToCategory: api.StatusCategoryInProgress},
		{ID: "21", Name: "Resolve", To: "Done", ToCategory: api.StatusCategoryDone, NeedsInput: true},
		{ID: "31", Name: "Wait for customer", To: "Waiting for customer", ToCategory: api.StatusCategoryInProgress, NeedsInput: true},
	}
	if fmt.Sprintf("%+v", res.Transitions) != fmt.Sprintf("%+v", want) {
		t.Fatalf("transitions =\n%+v\nwant\n%+v", res.Transitions, want)
	}

	// Refused before the site is asked: a transition that needs input, one
	// the issue does not offer, no transition at all.
	for name, tid := range map[string]string{"needs input": "21", "not offered": "99", "empty": ""} {
		_, err := b.Issues().Transition(ctx, api.IssueTransitionParams{AccountID: id, MessageID: comment.ID, TransitionID: tid})
		if errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := len(f.RequestsTo(http.MethodPost, "/transitions")); n != 0 {
		t.Fatalf("%d transitions posted for refused requests", n)
	}

	// Performed: the result carries the refreshed issue, the listings the
	// new status and the event row of the user's own change, read and not
	// announced.
	e.rec.mu.Lock()
	announced := len(e.rec.news)
	e.rec.mu.Unlock()
	done, err := b.Issues().Transition(ctx, api.IssueTransitionParams{AccountID: id, MessageID: comment.ID, TransitionID: "11"})
	if err != nil {
		t.Fatal(err)
	}
	if done.Issue.Key != printer.Key || done.Issue.Status != "In Progress" || done.Issue.StatusCategory != api.StatusCategoryInProgress {
		t.Fatalf("issue after the transition = %+v", done.Issue)
	}
	if got := f.TransitionsPerformed(printer.ID); fmt.Sprint(got) != "[11]" {
		t.Fatalf("performed = %v", got)
	}
	rows := e.list(itsd.ID)
	event := item(t, rows, printer.Key, api.IssueItemEvent)
	if fmt.Sprint(event.Issue.Changes) != "[{status To Do In Progress}]" || !event.Issue.Mine || !hasFlag(event.Flags, api.FlagSeen) ||
		event.Issue.Status != "In Progress" {
		t.Fatalf("event = %+v %+v", event, event.Issue)
	}
	if desc := item(t, rows, printer.Key, api.IssueItemDescription); desc.Issue.Status != "In Progress" {
		t.Fatalf("description's issue = %+v", desc.Issue)
	}
	thread, err := b.Threads().Get(ctx, api.ThreadGetParams{AccountID: id, ThreadID: comment.ThreadID, FolderID: itsd.ID})
	if err != nil || thread.Thread.Issue == nil || thread.Thread.Issue.Status != "In Progress" || len(thread.Messages) != 3 {
		t.Fatalf("thread = %+v %v", thread, err)
	}
	if e.rec.mu.Lock(); len(e.rec.news) != announced {
		e.rec.mu.Unlock()
		t.Fatal("the user's own status change was announced")
	} else {
		e.rec.mu.Unlock()
	}
	// The event names the issue too, and the offer changed with the status.
	res, err = b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: event.ID})
	if err != nil || res.Issue.Status != "In Progress" || len(res.Transitions) != 3 || res.Transitions[2].ID != "41" || res.Transitions[2].To != "To Do" {
		t.Fatalf("transitions after = %+v %v", res, err)
	}

	// The site refuses: serverError with its message; the stored issue is
	// untouched.
	f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/transitions", Status: http.StatusBadRequest,
		Body: `{"errorMessages":["Transition is not allowed: the issue is locked"],"errors":{}}`})
	_, err = b.Issues().Transition(ctx, api.IssueTransitionParams{AccountID: id, MessageID: comment.ID, TransitionID: "41"})
	if ae := apiErrorOf(t, err); ae.Code != api.CodeServerError || !strings.Contains(ae.Message, "the issue is locked") {
		t.Fatalf("refused by the site: %v", err)
	}
	if res, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: comment.ID}); err != nil || res.Issue.Status != "In Progress" {
		t.Fatalf("after the refusal: %+v %v", res, err)
	}
	// A revoked token, an issue gone from the site.
	times := 1
	if mode == jiratest.Cloud {
		times = 2 // the site and the gateway route
	}
	f.FailNext(jiratest.Failure{Method: http.MethodGet, Path: "/transitions", Status: http.StatusUnauthorized, Times: times})
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: comment.ID}); errCode(t, err) != api.CodeAuthFailed {
		t.Fatalf("revoked token: %v", err)
	}
	f.FailNext(jiratest.Failure{Method: http.MethodGet, Path: "/transitions", Status: http.StatusNotFound})
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: comment.ID}); errCode(t, err) != api.CodeMessageGone {
		t.Fatalf("gone: %v", err)
	}

	// Bad references.
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id, MessageID: "m_nobody"}); errCode(t, err) != api.CodeMessageNotFound {
		t.Errorf("unknown message: %v", err)
	}
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: id}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("no message: %v", err)
	}
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: "acc_nobody", MessageID: comment.ID}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}

	// A mail account has no transitions, whatever message is named.
	mail := store.Account{Name: "Work", Enabled: true, Config: validConfig()}
	if err := b.store.AddAccount(ctx, &mail); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Issues().Transitions(ctx, api.IssueTransitionsParams{AccountID: api.AccountID(mail.ID), MessageID: comment.ID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("mail account: %v", err)
	}
	if _, err := b.Issues().Transition(ctx, api.IssueTransitionParams{AccountID: api.AccountID(mail.ID), MessageID: comment.ID, TransitionID: "11"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("mail account: %v", err)
	}
}

func apiErrorOf(t *testing.T, err error) *api.Error {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	return ae
}
