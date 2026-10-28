// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
)

// JiraSet is the issue tracker of a sample set: a Jira Cloud site
// (internal/jira/jiratest, with Jana Dvořáková as the signed-in user,
// Petr Svoboda, and the spaces ITSD, WEB and MOB) with more users and the
// issues below.
type JiraSet struct {
	Users  map[string]JiraUser `json:"users"`
	Issues []JiraIssue         `json:"issues"`
}

// JiraUser is an extra user of the site, named by its key in the story.
type JiraUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// JiraIssue is one issue. People are keys ("me", "petr", a JiraSet user);
// statuses are named (To Do, In Progress, Done, Waiting for customer).
type JiraIssue struct {
	ID          string        `json:"id"`
	Space       string        `json:"space"`
	Summary     string        `json:"summary"`
	Status      string        `json:"status"`
	Assignee    string        `json:"assignee"`
	Reporter    string        `json:"reporter"`
	Created     string        `json:"created"`
	Watching    bool          `json:"watching,omitempty"`
	Description string        `json:"description"` // rendered HTML
	Comments    []JiraComment `json:"comments,omitempty"`
	History     []JiraHistory `json:"history,omitempty"`
}

// JiraComment is a comment with its rendered HTML.
type JiraComment struct {
	Author string `json:"author"`
	When   string `json:"when"`
	HTML   string `json:"html"`
}

// JiraHistory is a status change.
type JiraHistory struct {
	When   string `json:"when"`
	By     string `json:"by"`
	Status string `json:"status"`
}

// statusIDs are the fake site's statuses by name.
var statusIDs = map[string]string{"To Do": "1", "In Progress": "3", "Done": "10001", "Waiting for customer": "10002"}

// startJira starts the fake site on a loopback port and fills it with the
// set's users and issues, every change stamped with its own time.
func startJira(set *JiraSet, port int, now time.Time) (*jiratest.Server, int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, 0, fmt.Errorf("jira: %w", err)
	}
	f := jiratest.NewStandalone(jiratest.Cloud, ln)
	if err := seedJira(f, set, now); err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, ln.Addr().(*net.TCPAddr).Port, nil
}

// seedJira fills the site. The fake stamps every change with its clock,
// so the clock is set to each change's time in turn.
func seedJira(f *jiratest.Server, set *JiraSet, now time.Time) (err error) {
	defer func() {
		f.Now = time.Now
		if r := recover(); r != nil {
			err = fmt.Errorf("jira: %v", r)
		}
	}()
	users := map[string]string{"me": f.Me, "petr": f.Petr}
	for key, u := range set.Users {
		f.AddUser(&jiratest.User{ID: u.ID, Display: u.Name, Email: u.Email, TZ: "Europe/Prague"})
		users[key] = u.ID
	}
	user := func(key string) (string, error) {
		if key == "" {
			return "", nil
		}
		id, ok := users[key]
		if !ok {
			return "", fmt.Errorf("jira: unknown user %q", key)
		}
		return id, nil
	}
	type change struct {
		at   time.Time
		run  func()
		kind string
	}
	for _, is := range set.Issues {
		created, err := at(is.Created, now)
		if err != nil {
			return fmt.Errorf("jira issue %q: %w", is.ID, err)
		}
		reporter, err := user(is.Reporter)
		if err != nil {
			return err
		}
		assignee, err := user(is.Assignee)
		if err != nil {
			return err
		}
		statusID, ok := statusIDs[is.Status]
		if !ok {
			return fmt.Errorf("jira issue %q: unknown status %q", is.ID, is.Status)
		}
		f.Now = func() time.Time { return created }
		issue := f.AddIssue(is.Space, is.Summary, func(i *jiratest.Issue) {
			i.Description = is.Description
			i.Reporter = reporter
			i.Assignee = assignee
			i.Watching = is.Watching
		})
		var changes []change
		for _, c := range is.Comments {
			t, err := at(c.When, now)
			if err != nil {
				return fmt.Errorf("jira issue %q comment: %w", is.ID, err)
			}
			author, err := user(c.Author)
			if err != nil {
				return err
			}
			html := c.HTML
			changes = append(changes, change{at: t, kind: "comment", run: func() { f.AddComment(issue.ID, author, html) }})
		}
		for _, h := range is.History {
			t, err := at(h.When, now)
			if err != nil {
				return fmt.Errorf("jira issue %q history: %w", is.ID, err)
			}
			by, err := user(h.By)
			if err != nil {
				return err
			}
			id, ok := statusIDs[h.Status]
			if !ok {
				return fmt.Errorf("jira issue %q: unknown status %q", is.ID, h.Status)
			}
			changes = append(changes, change{at: t, kind: "status", run: func() { f.SetStatus(issue.ID, id, by) }})
		}
		sort.SliceStable(changes, func(a, b int) bool { return changes[a].at.Before(changes[b].at) })
		for _, c := range changes {
			t := c.at
			f.Now = func() time.Time { return t }
			c.run()
		}
		// The final status without a history entry of its own (the
		// history already moved it there, or it was never moved).
		f.Now = func() time.Time { return created }
		if f.IssueStatus(issue.ID) != statusID {
			last := created
			for _, c := range changes {
				if c.at.After(last) {
					last = c.at
				}
			}
			f.Now = func() time.Time { return last }
			f.SetStatus(issue.ID, statusID, reporter)
		}
	}
	f.Reindex()
	return nil
}
