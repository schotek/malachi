// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// What the reading pane shows of a message of a Jira account, above and in
// place of the body: the issue card (jira.IssueCard), the issue's summary
// as the subject, whether the card's key may open the issue, and for an
// event (a status or assignee change) the changes as the body, which then
// is never fetched. macOS has it as IssueReading.swift.

// issueReading is the Jira part of a message on display.
type issueReading struct {
	card jira.Card
	// subject is the header's subject: the issue's summary, or the
	// message's subject ("KEY: Summary") when the site sent none.
	subject string
	// openable says the card's key opens card.URL: a link to an issue of
	// the account's own site (jira.IsIssueURL).
	openable bool
	// event says the message is an event, whose changes, one sentence a
	// line (jira.EventLines), are eventBody, shown as the body. The
	// description or a comment has its body from message.body like mail.
	event     bool
	eventBody string
}

// readIssue is the Jira part of message s (the full message m once
// message.get answered, which wins), with site the account's
// JiraConfig.SiteURL ("" when unknown: the key opens nothing). false for a
// mail message.
func readIssue(s api.MessageSummary, m *api.Message, site string) (issueReading, bool) {
	issue := s.Issue
	if m != nil && m.Issue != nil {
		issue = m.Issue
	}
	if issue == nil {
		return issueReading{}, false
	}
	card := jira.IssueCard(issue.IssueInfo, issue, i18n.Tr)
	r := issueReading{
		card:     card,
		subject:  card.Summary,
		openable: card.URL != "" && jira.IsIssueURL(card.URL, site),
	}
	if r.subject == "" {
		subject := s.Subject
		if m != nil {
			subject = m.Subject
		}
		r.subject = subjectText(subject)
	}
	if jira.IsEvent(issue) {
		r.event = true
		r.eventBody = strings.Join(jira.EventLines(issue.Changes, i18n.Tr), "\n")
	}
	return r, true
}

// readsWithoutBody reports whether message s is shown without a body
// fetch: an event of an issue carries all it says in its summary.
func readsWithoutBody(s api.MessageSummary) bool {
	return jira.IsEvent(s.Issue)
}

// issueSite is the site of the Jira account acc as the window knows it
// ("" when unknown): the only site an issue card's key may open.
func (m *mailModel) issueSite(acc api.AccountID) string {
	a, ok := m.account(acc)
	if !ok || a.Config.Jira == nil {
		return ""
	}
	return a.Config.Jira.SiteURL
}
