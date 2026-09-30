// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every JiraConfig field but the site, the deployment and the spaces is
// optional: its zero value is the default and stays off the wire.
func TestJiraConfigJSON(t *testing.T) {
	minimal := AccountConfig{
		Name: "Acme Jira", Email: "jana@example.org", Kind: AccountJira,
		Jira: &JiraConfig{
			SiteURL: "https://acme.atlassian.net", Deployment: JiraCloud, Login: "jana@example.org",
			Spaces: []SpaceRef{{ID: "10001", Key: "ITSD"}},
		},
	}
	raw, err := json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Acme Jira","email":"jana@example.org","kind":"jira","jira":{"siteUrl":"https://acme.atlassian.net",` +
		`"deployment":"cloud","login":"jana@example.org","spaces":[{"id":"10001","key":"ITSD"}]}}`
	if string(raw) != want {
		t.Fatalf("minimal config:\n got %s\nwant %s", raw, want)
	}

	full := JiraConfig{
		SiteURL: "https://jira.example.org/jira", Deployment: JiraDataCenter, CloudID: "",
		Spaces:              []SpaceRef{{ID: "1", Key: "WEB", Name: "Web"}, {ID: "2", Key: "MOB"}},
		OfflineDays:         90,
		OnlyMine:            true,
		HideEvents:          true,
		DisabledFolders:     []VirtualFolder{VirtualWatching},
		ClosedStatuses:      []StatusRef{{ID: "6", Name: "Closed"}},
		NotificationMail:    NotificationMailHide,
		NotificationSenders: []string{"@example.org"},
		BotNames:            []string{"Issue Sync"},
		MetadataFilters:     []string{`^Sent from .*$`},
		AuthorPrefixes:      []string{"ACME"},
	}
	if raw, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"offlineDays":90`, `"onlyMine":true`, `"hideEvents":true`, `"disabledFolders":["watching"]`,
		`"closedStatuses":[{"id":"6","name":"Closed"}]`, `"notificationMail":"hide"`, `"notificationSenders":["@example.org"]`,
		`"botNames":["Issue Sync"]`, `"metadataFilters":["^Sent from .*$"]`, `"authorPrefixes":["ACME"]`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("full config lacks %s: %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "cloudId") || strings.Contains(string(raw), "login") {
		t.Errorf("empty cloudId or login sent: %s", raw)
	}
	var back JiraConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, full) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, full)
	}
}

// MessageIssue embeds IssueInfo: one flat "issue" object on the wire.
func TestMessageIssueJSON(t *testing.T) {
	m := MessageSummary{
		ID: "m_1", AccountID: "acc_1", FolderID: "f_1", ThreadID: "jira:10042",
		From:    []Address{{Name: "Jana Dvořáková", Address: "u-1@users.jira.invalid"}},
		Subject: "ITSD-42: Printer", Date: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), Flags: []Flag{},
		Issue: &MessageIssue{
			IssueInfo: IssueInfo{
				Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42", Summary: "Printer",
				Status: "In Progress", StatusCategory: StatusCategoryInProgress, AssignedToMe: true,
				CommentVisibilities: []CommentVisibility{CommentPublic, CommentInternal},
			},
			Item:    IssueItemEvent,
			Changes: []IssueChange{{Field: IssueFieldStatus, From: "To Do", To: "In Progress"}, {Field: IssueFieldAssignee, To: "Jana Dvořáková"}},
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	var issue map[string]any
	if err := json.Unmarshal(generic["issue"], &issue); err != nil {
		t.Fatal(err)
	}
	if _, nested := issue["IssueInfo"]; nested {
		t.Fatalf("IssueInfo nested instead of flattened: %s", generic["issue"])
	}
	for key, want := range map[string]any{"key": "ITSD-42", "item": "event", "status": "In Progress",
		"statusCategory": "inProgress", "assignedToMe": true} {
		if issue[key] != want {
			t.Errorf("issue.%s = %v, want %v", key, issue[key], want)
		}
	}
	for _, absent := range []string{"visibility", "via", "edited", "type", "priority", "assignee", "watching"} {
		if _, ok := issue[absent]; ok {
			t.Errorf("zero issue.%s sent: %s", absent, generic["issue"])
		}
	}
	if !strings.Contains(string(generic["issue"]), `"changes":[{"field":"status","from":"To Do","to":"In Progress"},{"field":"assignee","to":"Jana Dvořáková"}]`) {
		t.Errorf("changes: %s", generic["issue"])
	}
	var back MessageSummary
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, m)
	}

	// A mail message has no issue at all.
	m.Issue = nil
	if raw, _ = json.Marshal(m); strings.Contains(string(raw), `"issue"`) {
		t.Errorf("issue sent for a mail message: %s", raw)
	}
}

// Capabilities: an older daemon sends none (nil), which means the mail
// set; this daemon's empty list means nothing beyond reading and flags.
func TestAccountCan(t *testing.T) {
	var old Account
	if err := json.Unmarshal([]byte(`{"id":"acc_1","config":{"name":"W","email":"w@example.org"},"enabled":true,"state":{}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Capabilities != nil {
		t.Fatalf("absent capabilities decoded as %v", old.Capabilities)
	}
	for _, c := range MailCapabilities {
		if !old.Can(c) {
			t.Errorf("an older daemon's account cannot %s", c)
		}
	}
	if old.Can(CapabilityComment) {
		t.Error("an older daemon's account can comment")
	}

	var jira Account
	if err := json.Unmarshal([]byte(`{"id":"acc_2","config":{"name":"J","email":"j@example.org","kind":"jira"},"capabilities":[]}`), &jira); err != nil {
		t.Fatal(err)
	}
	if jira.Capabilities == nil {
		t.Fatal("empty capabilities decoded as nil")
	}
	for _, c := range append(append([]AccountCapability{}, MailCapabilities...), CapabilityComment) {
		if jira.Can(c) {
			t.Errorf("an account with no capabilities can %s", c)
		}
	}

	comment := Account{Capabilities: []AccountCapability{CapabilityComment, CapabilityForward}}
	if !comment.Can(CapabilityComment) || !comment.Can(CapabilityForward) || comment.Can(CapabilityReply) || comment.Can(CapabilityMove) {
		t.Errorf("explicit list: %v", comment.Capabilities)
	}
	raw, err := json.Marshal(Account{Capabilities: []AccountCapability{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"capabilities":[]`) {
		t.Errorf("an empty list must be sent: %s", raw)
	}
}

// The M2/M3 additions stay off the wire when unused.
func TestDraftCommentAndNotificationJSON(t *testing.T) {
	raw, err := json.Marshal(Draft{AccountID: "acc_1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "comment") {
		t.Errorf("mail draft carries a comment: %s", raw)
	}
	d := Draft{AccountID: "acc_2", Comment: &DraftComment{Issue: IssueInfo{Key: "WEB-7"}, Visibility: CommentInternal}}
	if raw, err = json.Marshal(d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"comment":{"issue":{"key":"WEB-7","url":"","summary":"","status":""},"visibility":"internal"}`) {
		t.Errorf("comment draft: %s", raw)
	}
	if raw, _ = json.Marshal(DraftCreateParams{AccountID: "acc_1", Mode: ComposeForward}); strings.Contains(string(raw), "messageAccountId") {
		t.Errorf("empty messageAccountId sent: %s", raw)
	}
	if raw, _ = json.Marshal(MessagesChangedNotification{AccountID: "acc_1"}); string(raw) != `{"accountId":"acc_1"}` {
		t.Errorf("notification without folders: %s", raw)
	}
	if raw, _ = json.Marshal(Folder{ID: "f_1", Role: RoleNone}); strings.Contains(string(raw), "virtual") {
		t.Errorf("a mail folder carries virtual: %s", raw)
	}
}
