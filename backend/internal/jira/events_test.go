// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestEventItems(t *testing.T) {
	c, err := NewClient(Options{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.JiraConfig{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud}
	y, err := newSynth(cfg, nil, nil, c, User{ID: "me"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t0 := syncT0
	is := Issue{ID: "7", Key: "ITSD-3", Summary: "Události", Created: t0, Updated: t0}
	hist := []History{
		{ID: "100", Author: User{ID: "p", Name: "Petr"}, Created: t0.Add(time.Hour), Changes: []Change{
			{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress", FromID: "1", ToID: "3"},
			{Field: api.IssueFieldAssignee, To: "Jana", ToID: "me"},
		}},
		{ID: "101", Author: User{ID: "me", Name: "Jana"}, Created: t0.Add(2 * time.Hour)}, // no watched change
		{ID: "102", Author: User{}, Created: t0.Add(3 * time.Hour), Changes: []Change{
			{Field: api.IssueFieldAssignee, From: "Jana"},
		}},
	}
	items := y.items(is, nil, hist, false, false)
	if len(items) != 3 {
		t.Fatalf("items = %d", len(items))
	}
	ev := items[1]
	if ev.remoteID != "h:100" || ev.kind != api.IssueItemEvent || ev.html != "" ||
		ev.text != "To Do → In Progress\n— → Jana" ||
		fmt.Sprint(ev.changes) != "[{status To Do In Progress} {assignee  Jana}]" ||
		ev.msgID != "history.100.issue.7@acme.atlassian.net.malachi.invalid" ||
		ev.inReplyTo != "issue.7@acme.atlassian.net.malachi.invalid" ||
		!ev.date.Equal(t0.Add(time.Hour)) || ev.from.Name != "Petr" || len(ev.files) != 0 {
		t.Fatalf("event = %+v", ev)
	}
	// An automation without a user still makes a sender.
	if last := items[2]; last.remoteID != "h:102" || last.from.Address != "anonymous@"+userDomain || last.text != "Jana → —" {
		t.Fatalf("anonymous event = %+v", last)
	}
	if hidden := y.items(is, nil, hist, false, true); len(hidden) != 1 {
		t.Fatalf("hideEvents left %d items", len(hidden))
	}
}

func TestIssueTriggerBudget(t *testing.T) {
	s := NewSyncer(testAccount(), Deps{})
	if s.TriggerIssue("not a key") || s.TriggerIssue("") {
		t.Fatal("a bad key was queued")
	}
	// A spent budget postpones the pass instead of running it.
	now := time.Now()
	s.mu.Lock()
	for range triggerBudget {
		s.early = append(s.early, now)
	}
	s.mu.Unlock()
	if !s.queueIssue("itsd-1", nil, 0) || !s.TriggerIssue("ITSD-2") {
		t.Fatal("a key was refused")
	}
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	ready, queued := s.pending.any, len(s.keys)
	timer := s.debounce
	s.mu.Unlock()
	if ready || queued != 2 || timer == nil {
		t.Fatalf("ready %v, queued %d, timer %v", ready, queued, timer != nil)
	}
	// A pass takes the keys and the scheduled pass with them.
	_, _, keys, _ := s.takePending()
	if strings.Join(keys, ",") != "ITSD-1,ITSD-2" {
		t.Fatalf("keys = %v", keys)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.debounce != nil {
		t.Fatal("the scheduled pass survived")
	}
}

func testAccount() (a store.Account) {
	a.ID = "acc_test"
	a.Config = api.AccountConfig{Kind: api.AccountJira, Jira: &api.JiraConfig{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud}}
	return a
}
