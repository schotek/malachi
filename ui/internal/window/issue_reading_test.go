// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// What the reading pane shows of a Jira message (issue_reading.go; the
// port of macOS JiraReaderTests): the card, the issue's summary as the
// subject, whether the key opens the issue, and an event's changes in
// place of the body.

const readerSite = "https://acme.atlassian.net"

func readerInfo(key, summary, url string) api.IssueInfo {
	if url == "" {
		url = readerSite + "/browse/" + key
	}
	return api.IssueInfo{
		Key: key, URL: url, Summary: summary, Status: "In Progress",
		StatusCategory: api.StatusCategoryInProgress, Priority: "High", Assignee: "Jana Dvořáková",
	}
}

func readerMessage(issue *api.MessageIssue, subject string) api.MessageSummary {
	return api.MessageSummary{ID: "m1", AccountID: "j", FolderID: "f", Subject: subject, Flags: []api.Flag{api.FlagSeen}, Issue: issue}
}

func TestReadIssueMailHasNone(t *testing.T) {
	s := readerMessage(nil, "Hello")
	if _, ok := readIssue(s, nil, readerSite); ok {
		t.Error("a mail message has an issue reading")
	}
	if readsWithoutBody(s) {
		t.Error("a mail message reads without its body")
	}
}

func TestReadIssueComment(t *testing.T) {
	item := &api.MessageIssue{
		IssueInfo: readerInfo("ITSD-42", "VPN drops every 10 minutes", ""), Item: api.IssueItemComment,
		Visibility: api.CommentInternal, Via: "Issue Sync", Edited: true,
	}
	r, ok := readIssue(readerMessage(item, "ITSD-42: VPN drops every 10 minutes"), nil, readerSite)
	if !ok {
		t.Fatal("no reading")
	}
	if r.subject != "VPN drops every 10 minutes" {
		t.Errorf("subject %q: the summary, the key is on the card", r.subject)
	}
	c := r.card
	if c.Key != "ITSD-42" || c.Status != "In Progress" || c.StatusStyle != "status-in-progress" {
		t.Errorf("card %+v", c)
	}
	if !c.Internal || c.InternalLabel != "Internal" || c.Via != "via Issue Sync" || c.Edited != "Edited" {
		t.Errorf("badges %+v", c)
	}
	var labels, values []string
	for _, row := range c.Rows {
		labels = append(labels, row.Label)
		values = append(values, row.Value)
	}
	if !reflect.DeepEqual(labels, []string{"Assignee", "Priority", "Type", "Reporter"}) ||
		!reflect.DeepEqual(values, []string{"Jana Dvořáková", "High", "None", "None"}) {
		t.Errorf("rows %v %v", labels, values)
	}
	if !r.openable {
		t.Error("the key does not open its own site's issue")
	}
	if r.event || r.eventBody != "" || readsWithoutBody(readerMessage(item, "")) {
		t.Error("a comment's body comes from message.body")
	}
}

func TestReadIssueTheFullMessageWins(t *testing.T) {
	old := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", "Old summary", ""), Item: api.IssueItemDescription}
	fresh := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", "New summary", ""), Item: api.IssueItemDescription}
	full := &api.Message{MessageSummary: readerMessage(fresh, "ITSD-42: New summary")}
	if r, _ := readIssue(readerMessage(old, "ITSD-42: Old summary"), full, readerSite); r.subject != "New summary" {
		t.Errorf("subject %q", r.subject)
	}
	// The issue only in the full message still counts.
	if r, ok := readIssue(readerMessage(nil, ""), full, readerSite); !ok || r.subject != "New summary" {
		t.Errorf("subject %q", r.subject)
	}
}

func TestReadIssueEmptySummaryFallsBackToTheSubject(t *testing.T) {
	item := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", " ‮ ", ""), Item: api.IssueItemDescription}
	r, _ := readIssue(readerMessage(item, "ITSD-42"), nil, readerSite)
	if r.card.Summary != "" || r.subject != "ITSD-42" {
		t.Errorf("summary %q, subject %q", r.card.Summary, r.subject)
	}
}

func TestReadIssueTheKeyOpensOnlyTheAccountsSite(t *testing.T) {
	cases := []struct {
		name, url string
		want      bool
	}{
		{"own site", readerSite + "/browse/ITSD-42", true},
		{"another site", "https://evil.example/browse/ITSD-42", false},
		{"user info", "https://acme.atlassian.net@evil.example/browse/ITSD-42", false},
		{"http on an https site", "http://acme.atlassian.net/browse/ITSD-42", false},
		{"javascript", "javascript:alert(1)", false},
	}
	for _, c := range cases {
		info := readerInfo("ITSD-42", "S", c.url)
		item := &api.MessageIssue{IssueInfo: info, Item: api.IssueItemComment}
		if r, _ := readIssue(readerMessage(item, ""), nil, readerSite); r.openable != c.want {
			t.Errorf("%s: openable %v", c.name, r.openable)
		}
	}
	// No URL at all, and no site known (the account is gone): nothing opens.
	empty := &api.MessageIssue{IssueInfo: api.IssueInfo{Key: "ITSD-42"}, Item: api.IssueItemComment}
	if r, _ := readIssue(readerMessage(empty, ""), nil, readerSite); r.openable {
		t.Error("an empty URL opens")
	}
	item := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", "S", ""), Item: api.IssueItemComment}
	if r, _ := readIssue(readerMessage(item, ""), nil, ""); r.openable {
		t.Error("opens without a site")
	}
}

func TestReadIssueAnEventIsItsChanges(t *testing.T) {
	item := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", "S", ""), Item: api.IssueItemEvent, Changes: []api.IssueChange{
		{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"},
		{Field: "resolution", To: "Fixed"},
		{Field: api.IssueFieldAssignee, From: "Jana Dvořáková"},
	}}
	s := readerMessage(item, "")
	if !readsWithoutBody(s) {
		t.Error("an event reads with a body")
	}
	r, _ := readIssue(s, nil, readerSite)
	if !r.event || r.eventBody != "Status: To Do → In Progress\nAssignee: Jana Dvořáková → Unassigned" {
		t.Errorf("event body %q", r.eventBody)
	}
	// An event of changes this client does not know: an empty body.
	unknown := &api.MessageIssue{IssueInfo: readerInfo("ITSD-42", "S", ""), Item: api.IssueItemEvent,
		Changes: []api.IssueChange{{Field: "labels", To: "x"}}}
	if r, _ := readIssue(readerMessage(unknown, ""), nil, readerSite); !r.event || r.eventBody != "" {
		t.Errorf("unknown event body %q", r.eventBody)
	}
}
