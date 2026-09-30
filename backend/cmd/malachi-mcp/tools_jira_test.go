// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Fixture of an issue-tracker account: one space folder with a comment
// whose status carries a right-to-left override (display text of the
// site, hostile like any mail data).
const (
	fxJira      = api.AccountID("j1")
	fxJiraSpace = api.FolderID("f_itsd")
)

var rtlOverride = string(rune(0x202E))

func withJiraAccount(f *fakeBackend) *fakeBackend {
	now := f.messages["m1"].Date
	f.accounts = append(f.accounts, api.Account{
		ID: fxJira,
		Config: api.AccountConfig{Name: "Acme Jira", Email: "jana@acme.test", Kind: api.AccountJira,
			Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "jana@acme.test"}},
		Enabled:      true,
		State:        api.SyncState{AccountID: fxJira, Status: api.SyncIdle, Progress: -1},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition},
	})
	f.folders[fxJira] = []api.Folder{{ID: fxJiraSpace, AccountID: fxJira, Name: "IT Service Desk", Path: "IT Service Desk",
		Role: api.RoleNone, Subscribed: true, Selectable: true, Synced: true, Total: 1}}
	issue := &api.MessageIssue{
		IssueInfo: api.IssueInfo{Key: "ITSD-7", URL: "https://acme.atlassian.net/browse/ITSD-7", Summary: "Printer",
			Status: "In " + rtlOverride + "Progress", CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal}},
		Item: api.IssueItemComment,
	}
	c := api.Message{MessageSummary: api.MessageSummary{
		ID: "jc1", AccountID: fxJira, FolderID: fxJiraSpace, ThreadID: "jira:20001",
		From:    []api.Address{{Name: "Petr Svoboda", Address: "u-1@users.jira.invalid"}},
		Subject: "ITSD-7: Printer", Date: now, Snippet: "It works again.", Flags: []api.Flag{}, Size: 900,
		Issue: issue,
	}}
	f.lists[fxJiraSpace] = []api.MessageSummary{c.MessageSummary}
	f.messages["jc1"] = c
	f.transitions = map[api.MessageID][]api.IssueTransition{"jc1": {
		{ID: "21", Name: "Resolve", To: "Done", ToCategory: api.StatusCategoryDone, NeedsInput: true},
		{ID: "41", Name: "Reopen" + rtlOverride, To: "To " + rtlOverride + "Do", ToCategory: api.StatusCategoryTodo},
	}}
	f.bodies["jc1"] = api.MessageBodyResult{MessageID: "jc1", BodyState: api.BodyFetched, Text: "It works again.\n",
		RemoteContent: api.RemoteBlock, SanitizerVersion: "1"}
	f.searchResults = append(f.searchResults, api.SearchResult{Message: c.MessageSummary, Snippet: "It works again."})
	return f
}

// The issue key, status and item of a Jira message are listed, read and
// found inside the fence, cleaned like every other string of mail.
func TestJiraIssueFields(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), false, false)

	out := h.ok(t, "list_messages", map[string]any{"accountId": "j1", "folderId": "f_itsd"})
	body := fencedBody(t, out)
	mustContain(t, body, `"issue": {`, `"key": "ITSD-7"`, `"status": "In Progress"`, `"item": "comment"`)
	mustNotContain(t, out, rtlOverride)

	out = h.ok(t, "read_message", map[string]any{"accountId": "j1", "messageId": "jc1"})
	body = fencedBody(t, out)
	mustContain(t, body, "issue: ITSD-7\n", "issue-status: In Progress\n", "issue-item: comment\n", "It works again.")
	mustNotContain(t, out, rtlOverride)

	out = h.ok(t, "search_messages", map[string]any{"query": "works"})
	mustContain(t, fencedBody(t, out), `"key": "ITSD-7"`)

	// A mail message has no issue.
	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m1"})
	mustNotContain(t, out, "issue:")
	out = h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_in"})
	mustNotContain(t, out, `"issue"`)
}

// create_draft refuses what an issue-tracker account cannot take in words a
// model can act on, before the daemon is asked to build or store anything.
func TestJiraCreateDraftRefused(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "j1", "body": "Hello"}, "issue tracker (kind jira): it takes no e-mail draft (mode new)")
	h.fail(t, "create_draft", map[string]any{"accountId": "j1", "mode": "replyAll", "messageId": "jc1", "body": "Thanks"}, "mode reply with a messageId writes a comment")
	h.fail(t, "create_draft", map[string]any{"accountId": "j1", "mode": "forward", "messageId": "jc1"}, "accountId = a mail account and messageAccountId = j1")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "messageAccountId": "j1"}, "messageAccountId applies to mode forward only")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"alice@example.org"}, "visibility": "internal"}, "visibility applies only to a comment draft")
	// A comment has a body and nothing of mail.
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"to": []string{"x@example.org"}, "body": "x"}, "no recipients"},
		{map[string]any{"subject": "Re: printer", "body": "x"}, "no subject of its own"},
		{map[string]any{"attribution": "On Monday", "body": "x"}, "quotes nothing"},
		{map[string]any{"omitQuote": true, "body": "x"}, "quotes nothing"},
		{map[string]any{"body": "  "}, "body is required"},
		{map[string]any{"body": "x", "visibility": "team"}, "public or internal"},
	} {
		c.args["accountId"], c.args["mode"], c.args["messageId"] = "j1", "reply", "jc1"
		h.fail(t, "create_draft", c.args, c.want)
	}
	h.fb.mu.Lock()
	creates, saves, gets := len(h.fb.draftCreates), len(h.fb.draftSaves), h.fb.getCalls
	h.fb.mu.Unlock()
	if creates != 0 || saves != 0 || gets != 0 {
		t.Fatalf("the daemon was asked: %d draft.create, %d draft.save, %d message.get", creates, saves, gets)
	}
	// A mail account is not affected.
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"alice@example.org"}, "body": "Hi"})
}

// A reply on an issue-tracker account is a comment draft: the agent's text
// escaped as its body, the visibility asked for, the issue in the fence;
// send_message queues it like any draft of the session.
func TestJiraCommentDraft(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), false, true)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "j1", "mode": "reply", "messageId": "jc1",
		"body": "Toner <b>replaced</b>.\n\nClosing.", "visibility": "internal"})
	mustContain(t, out, "comment draft d1 (version 1) stored in account j1; it is NOT posted.", "send_message with draftId=d1 posts it")
	body := fencedBody(t, out)
	mustContain(t, body, "issue: ITSD-7\n", "summary: Printer\n", "status: In Progress\n", "visibility: internal\n", "in-reply-to: jc1\n")
	mustNotContain(t, out, rtlOverride)
	h.fb.mu.Lock()
	create, save := h.fb.draftCreates[0], h.fb.draftSaves[0].Draft
	h.fb.mu.Unlock()
	if create.AccountID != fxJira || create.Mode != api.ComposeReply || create.MessageID != "jc1" || create.Attribution != "" {
		t.Errorf("draft.create = %+v", create)
	}
	if save.AccountID != fxJira || save.Comment == nil || save.Comment.Visibility != api.CommentInternal || save.InReplyTo != "jc1" ||
		len(save.To) != 0 || save.TextBody != "" || save.HTMLBody != "<p>Toner &lt;b&gt;replaced&lt;/b&gt;.</p><p>Closing.</p>" {
		t.Errorf("draft.save = %+v %+v", save, save.Comment)
	}
	h.ok(t, "send_message", map[string]any{"draftId": "d1"})
	h.fb.mu.Lock()
	sends := h.fb.sends
	h.fb.mu.Unlock()
	if len(sends) != 1 || sends[0].AccountID != fxJira || sends[0].DraftID != "d1" {
		t.Errorf("message.send = %+v", sends)
	}

	// Public by default; internal only where the issue allows it.
	out = h.ok(t, "create_draft", map[string]any{"accountId": "j1", "mode": "reply", "messageId": "jc1", "body": "Done."})
	mustContain(t, fencedBody(t, out), "visibility: public\n")
	h.fb.mu.Lock()
	m := h.fb.messages["jc1"]
	issue := *m.Issue
	issue.CommentVisibilities = nil
	m.Issue = &issue
	h.fb.messages["jc1"] = m
	h.fb.mu.Unlock()
	h.fail(t, "create_draft", map[string]any{"accountId": "j1", "mode": "reply", "messageId": "jc1", "body": "x", "visibility": "internal"},
		"takes no internal comment")
}

// A message of an issue tracker is forwarded by e-mail from a mail
// account: the original is read from its own account, the draft is the
// mail account's.
func TestJiraForwardByMail(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "jc1", "messageAccountId": "j1",
		"to": []string{"alice@example.org"}, "body": "FYI"})
	mustContain(t, out, "stored in account a1", "mode: forward")
	h.fb.mu.Lock()
	create, save, gets := h.fb.draftCreates[0], h.fb.draftSaves[0].Draft, h.fb.getCalls
	h.fb.mu.Unlock()
	if create.AccountID != fxAccount || create.MessageAccountID != fxJira || create.MessageID != "jc1" || create.Mode != api.ComposeForward {
		t.Errorf("draft.create = %+v", create)
	}
	if save.AccountID != fxAccount || save.Forwarding != "jc1" || save.Comment != nil || save.Subject != "Fwd: ITSD-7: Printer" {
		t.Errorf("draft.save = %+v", save)
	}
	if gets != 1 {
		t.Errorf("message.get calls = %d", gets)
	}
	// The default attribution names the original from its own account.
	mustContain(t, save.HTMLBody, "Forwarded message", "Subject: ITSD-7: Printer")
}

// list_transitions fences the site's names, marks what needs input, and
// transition_issue (behind --allow-modify) performs one by id and reports
// the issue's new status; what the daemon refuses comes back as its error.
func TestJiraTransitionsTools(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), true, false)
	out := h.ok(t, "list_transitions", map[string]any{"accountId": "j1", "messageId": "jc1"})
	mustContain(t, out, "2 transitions offered on the issue of message jc1 in account j1")
	body := fencedBody(t, out)
	mustContain(t, body, "issue: ITSD-7\n", "summary: Printer\n", "status: In Progress\n",
		`- id=21 name="Resolve" to="Done" category=done needsInput`, `- id=41 name="Reopen" to="To Do" category=todo`)
	mustNotContain(t, out, rtlOverride, "id=41 name=\"Reopen\" to=\"To Do\" category=todo needsInput")

	h.fail(t, "list_transitions", map[string]any{"accountId": "j1", "messageId": ""}, "accountId and messageId are required")
	h.fail(t, "list_transitions", map[string]any{"accountId": "a1", "messageId": "m1"}, "invalidArgument")
	h.fail(t, "list_transitions", map[string]any{"accountId": "j1", "messageId": "m_nobody"}, "messageNotFound")

	h.fail(t, "transition_issue", map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": ""}, "transitionId are required")
	h.fail(t, "transition_issue", map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": "21"}, "needs fields filled in on the site")
	h.fail(t, "transition_issue", map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": "99"}, "offers no transition")
	out = h.ok(t, "transition_issue", map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": "41"})
	mustContain(t, out, "transition 41 performed on the issue of message jc1 in account j1")
	mustContain(t, fencedBody(t, out), "issue: ITSD-7\n", "summary: Printer\n", "status: To Do")
	mustNotContain(t, out, rtlOverride)
	h.fb.mu.Lock()
	calls := h.fb.transitionCalls
	h.fb.mu.Unlock()
	if len(calls) != 3 || calls[2] != (api.IssueTransitionParams{AccountID: fxJira, MessageID: "jc1", TransitionID: "41"}) {
		t.Fatalf("issue.transition calls = %+v", calls)
	}
	// The issue's status changed for every later reading.
	out = h.ok(t, "read_message", map[string]any{"accountId": "j1", "messageId": "jc1"})
	mustContain(t, fencedBody(t, out), "issue-status: To Do\n")

	// An issue the site no longer shows, a token the site refuses.
	h.fb.setFail(api.MethodIssueTransitions, api.NewError(api.CodeMessageGone, "jira: HTTP 404: Issue does not exist"))
	h.fail(t, "list_transitions", map[string]any{"accountId": "j1", "messageId": "jc1"}, "messageGone")
	h.fb.setFail(api.MethodIssueTransition, api.NewError(api.CodeAuthFailed, "jira: HTTP 401: Unauthorized"))
	h.fail(t, "transition_issue", map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": "41"}, "authFailed")
}

// Without --allow-modify the status of an issue cannot be changed, only
// listed.
func TestJiraTransitionGated(t *testing.T) {
	h := newHarness(t, withJiraAccount(newFixture()), false, false)
	h.ok(t, "list_transitions", map[string]any{"accountId": "j1", "messageId": "jc1"})
	params := mcpCallParams("transition_issue")
	params.Arguments = map[string]any{"accountId": "j1", "messageId": "jc1", "transitionId": "41"}
	if _, err := h.cs.CallTool(t.Context(), &params); err == nil {
		t.Fatal("transition_issue must be unknown without --allow-modify")
	}
	h.fb.mu.Lock()
	defer h.fb.mu.Unlock()
	if len(h.fb.transitionCalls) != 0 {
		t.Fatalf("issue.transition was called: %+v", h.fb.transitionCalls)
	}
}
