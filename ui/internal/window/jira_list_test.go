// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The message list of a Jira account (model.go alwaysGrouped,
// countsUnread, summaryMessage; thread_model.go applyNewMessage,
// summaryThread; the port of the model half of macOS JiraListTests): its
// folders are always listed as conversations, the rows carry the issue, an
// event never shows as unread, and a notified message brings the issue as
// it is now.

var jiraListBase = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func listIssue(key, status string, category api.IssueStatusCategory) api.IssueInfo {
	return api.IssueInfo{
		Key: key, URL: "https://acme.atlassian.net/browse/" + key, Summary: "Summary of " + key,
		Status: status, StatusCategory: category,
	}
}

// listItem is a message of issue key (its thread) dated hours after the
// base.
func listItem(id string, hours int, key string, kind api.IssueItemKind, info *api.IssueInfo, changes []api.IssueChange, flags ...api.Flag) api.MessageSummary {
	i := listIssue(key, "To Do", api.StatusCategoryTodo)
	if info != nil {
		i = *info
	}
	return api.MessageSummary{
		ID: api.MessageID(id), AccountID: "j", FolderID: "web", ThreadID: api.ThreadID("issue-" + key),
		From: []api.Address{{Name: "Jana Dvořáková"}}, Subject: key + ": " + i.Summary,
		Date: jiraListBase.Add(time.Duration(hours) * time.Hour), Snippet: "p-" + id, Flags: flags,
		Issue: &api.MessageIssue{IssueInfo: i, Item: kind, Changes: changes},
	}
}

func TestJiraMessageRowProjection(t *testing.T) {
	done := listIssue("ITSD-7", "Done", api.StatusCategoryDone)
	row := summaryMessage(listItem("c1", 1, "ITSD-7", api.IssueItemComment, &done, nil))
	r := row.Issue
	if r == nil {
		t.Fatal("no issue")
	}
	if r.Key != "ITSD-7" || r.Summary != "Summary of ITSD-7" || r.Status != "Done" || r.StatusStyle != "status-done" || r.Event {
		t.Errorf("issue %+v", r)
	}
	if !row.Unread || !r.Unread {
		t.Error("an unseen comment is not unread")
	}

	// An event is never unread, whatever its flags.
	event := summaryMessage(listItem("e1", 2, "ITSD-7", api.IssueItemEvent, nil,
		[]api.IssueChange{{Field: api.IssueFieldAssignee, To: "Jana Dvořáková"}}))
	if event.Unread || event.Issue == nil || !event.Issue.Event || event.Issue.EventText != "Assignee: Unassigned → Jana Dvořáková" {
		t.Errorf("event %+v %+v", event, event.Issue)
	}

	// An internal service-desk comment has the badge.
	internal := listItem("i1", 3, "ITSD-7", api.IssueItemComment, nil, nil, api.FlagSeen)
	internal.Issue.Visibility = api.CommentInternal
	if r := summaryMessage(internal).Issue; !r.Internal || r.InternalLabel != "Internal" {
		t.Errorf("internal %+v", r)
	}

	// A mail message has no issue and keeps its unread state.
	mail := summary("m1", api.FlagSeen)
	if row := summaryMessage(mail); row.Issue != nil || row.Unread {
		t.Errorf("mail %+v", row)
	}
}

func TestJiraSearchRowsCarryTheIssue(t *testing.T) {
	m := &mailModel{}
	s := listItem("c1", 1, "WEB-3", api.IssueItemComment, nil, nil)
	if row := m.rowMessage(s); row.Issue == nil || row.Issue.Key != "WEB-3" {
		t.Errorf("row %+v", row)
	}
}

func TestJiraApplyNewMessageCarriesTheIssue(t *testing.T) {
	first := listItem("w1d", 1, "WEB-1", api.IssueItemDescription, nil, nil, api.FlagSeen)
	latest := listItem("w1c", 4, "WEB-1", api.IssueItemComment, nil, nil)
	web1 := listIssue("WEB-1", "To Do", api.StatusCategoryTodo)
	t1 := api.ThreadSummary{
		ID: "issue-WEB-1", AccountID: "j", Subject: latest.Subject, Participants: latest.From,
		MessageCount: 2, UnreadCount: 1, LatestDate: latest.Date, Latest: latest, Snippet: latest.Snippet,
		Flags: []api.Flag{}, FolderIDs: []api.FolderID{"web"}, Issue: &web1,
	}
	m := &mailModel{grouped: true, listFolder: folderKey{Account: "j", Folder: "web"}}
	m.setThreads([]api.ThreadSummary{t1}, api.PageInfo{Total: 1})
	m.setMembers("issue-WEB-1", t1, []api.MessageSummary{first, latest}, nil)

	// A new comment comes with the issue as it is now: the row follows.
	moved := listIssue("WEB-1", "In Progress", api.StatusCategoryInProgress)
	m.applyNewMessage(listItem("w1n", 6, "WEB-1", api.IssueItemComment, &moved, nil), api.FilterAll, listKey{})
	th := m.threads[0]
	if th.Issue == nil || !reflect.DeepEqual(*th.Issue, moved) || th.MessageCount != 3 || th.UnreadCount != 2 {
		t.Errorf("thread %+v", th)
	}
	if got := summaryThread(th, false, false).Issue; got == nil || got.Status != "In Progress" {
		t.Errorf("row %+v", got)
	}

	// An event, even one delivered unseen, is never unread.
	done := listIssue("WEB-1", "Done", api.StatusCategoryDone)
	m.applyNewMessage(listItem("w1e", 7, "WEB-1", api.IssueItemEvent, &done,
		[]api.IssueChange{{Field: api.IssueFieldStatus, From: "In Progress", To: "Done"}}), api.FilterAll, listKey{})
	th = m.threads[0]
	if th.UnreadCount != 2 || th.Issue.StatusCategory != api.StatusCategoryDone {
		t.Errorf("after the event %+v", th)
	}
	if got := summaryThread(th, false, false).Issue; got == nil || !got.Event {
		t.Errorf("row %+v", got)
	}

	// A new conversation starts with the issue of its first message.
	m.applyNewMessage(listItem("w9", 8, "WEB-9", api.IssueItemComment, nil, nil), api.FilterAll, listKey{})
	if t9 := m.threads[0]; t9.ID != "issue-WEB-9" || t9.Issue == nil || t9.Issue.Key != "WEB-9" || t9.UnreadCount != 1 {
		t.Errorf("new conversation %+v", t9)
	}
	m.applyNewMessage(listItem("w8e", 9, "WEB-8", api.IssueItemEvent, nil,
		[]api.IssueChange{{Field: api.IssueFieldStatus, From: "A", To: "B"}}), api.FilterAll, listKey{})
	if m.threads[0].UnreadCount != 0 {
		t.Errorf("an unseen event counted: %+v", m.threads[0])
	}

	// A message without an issue leaves the conversation's alone.
	plain := api.MessageSummary{
		ID: "p1", AccountID: "j", FolderID: "web", ThreadID: "issue-WEB-9", Subject: "x",
		Date: jiraListBase.Add(10 * time.Hour), Flags: []api.Flag{api.FlagSeen},
	}
	m.applyNewMessage(plain, api.FilterAll, listKey{})
	for _, th := range m.threads {
		if th.ID == "issue-WEB-9" && (th.Issue == nil || th.Issue.Key != "WEB-9") {
			t.Errorf("the issue went: %+v", th)
		}
	}
}

func TestJiraAlwaysGroupedFollowsTheAccountKind(t *testing.T) {
	m := &mailModel{accounts: []api.Account{
		{ID: "a", Enabled: true, Config: api.AccountConfig{Email: "a@example.invalid"}},
		{ID: "j", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira, Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net"}}},
	}}
	if !m.alwaysGrouped(folderKey{Account: "j", Folder: "anything"}) {
		t.Error("a Jira folder is not always grouped")
	}
	for _, k := range []folderKey{{Account: "a", Folder: "in"}, {Account: "gone", Folder: "in"}, {}} {
		if m.alwaysGrouped(k) {
			t.Errorf("%v is always grouped", k)
		}
	}
}

func TestJiraSidebar(t *testing.T) {
	jira := api.Account{ID: "j", Enabled: true, Config: api.AccountConfig{
		Email: "jana@acme.example", Kind: api.AccountJira, Jira: &api.JiraConfig{SiteURL: "https://Acme.atlassian.net/"},
	}}
	mail := api.Account{ID: "a", Enabled: true, Config: api.AccountConfig{Email: "a@example.invalid"}}
	// An unnamed Jira account is named after its site's host; the
	// capsule says what it is.
	if accountLabel(jira) != "acme.atlassian.net" || accountHeaderBadge(jira) != "JIRA" {
		t.Errorf("label %q, badge %q", accountLabel(jira), accountHeaderBadge(jira))
	}
	if accountLabel(mail) != "a@example.invalid" || accountHeaderBadge(mail) != "IMAP" {
		t.Errorf("mail: %q, %q", accountLabel(mail), accountHeaderBadge(mail))
	}
	// A mail account names the provider it signs in with.
	google := mail
	google.Config.OAuth2 = &api.OAuth2Config{Provider: api.OAuth2ProviderGoogle}
	graph := mail
	graph.Config.Kind = api.AccountGraph
	office := mail
	office.Config.OAuth2 = &api.OAuth2Config{Provider: api.OAuth2ProviderOffice365}
	for _, c := range []struct {
		a    api.Account
		want string
	}{{google, "GOOGLE"}, {graph, "M365"}, {office, "M365"}} {
		if got := accountHeaderBadge(c.a); got != c.want {
			t.Errorf("badge %q, want %q", got, c.want)
		}
	}
	// The views after the role folders, before the spaces, by their rank.
	folders := []api.Folder{
		{ID: "space:2", Path: "Alpha"},
		{ID: "open", Path: "open", Virtual: api.VirtualOpen},
		{ID: "out", Path: "Outbox", Role: api.RoleOutbox},
		{ID: "watching", Path: "watching", Virtual: api.VirtualWatching},
		{ID: "assigned", Path: "zzz", Virtual: api.VirtualAssignedToMe},
	}
	sortSiblings(folders)
	var got []api.FolderID
	for _, f := range folders {
		got = append(got, f.ID)
	}
	if want := []api.FolderID{"out", "assigned", "watching", "open", "space:2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
	// Their names are localised, their icon is a saved search.
	if folderTitle(api.Folder{Name: "assignedToMe", Virtual: api.VirtualAssignedToMe}) != "Assigned to Me" {
		t.Error("the view's title")
	}
	if folderTitle(api.Folder{Name: "Web"}) != "Web" {
		t.Error("a space's title")
	}
	if folderIcon(api.Folder{Virtual: api.VirtualOpen}) != "folder-saved-search-symbolic" || folderIcon(api.Folder{Role: api.RoleInbox}) != "mail-unread-symbolic" {
		t.Error("the icons")
	}
}
