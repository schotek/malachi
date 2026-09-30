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
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward},
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
