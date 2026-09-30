// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// conversationIssueCard is the issue card on top of a Jira conversation
// (conversation_view.go showIssue): the pane's issue card (issue_card.go)
// with the conversation's issue, its key a link on the account's own site
// only, its status pill the Change Status menu on an account that changes
// statuses. The menu acts on the issue through the conversation's first
// member shown (any member names the issue). The view builds a new card
// only when the issue changed and keeps the one on show (issueCard) for
// the transitions' spinner and result (conversationSetIssueBusy,
// conversationApplyIssue) and for Change Status in the menus.
func (w *Window) conversationIssueCard(card jira.Card, account api.Account, info api.IssueInfo) *issueCard {
	c := newIssueCard(w, &w.ApplicationWindow.Window, w.conversationIssueSubject)
	r := issueReading{
		card:     card,
		subject:  card.Summary,
		openable: card.URL != "" && jira.IsIssueURL(card.URL, w.model.issueSite(account.ID)),
	}
	c.show(&r, account.ID, w.issues.canTransition(account.ID))
	return c
}

// conversationIssueSubject is what the Change Status menu of the
// conversation's issue card acts on: the issue of the first member shown.
func (w *Window) conversationIssueSubject() (issueSubject, bool) {
	if w.conv == nil || w.conv.ctrl == nil || w.conv.ctrl.model == nil {
		return issueSubject{}, false
	}
	first, ok := convFirstMember(w.conv.ctrl.model)
	if !ok {
		return issueSubject{}, false
	}
	return subjectOf(first)
}

// conversationSetIssueBusy shows the spinner of a running transition on
// the conversation's issue card (issueActions onBusy).
func (w *Window) conversationSetIssueBusy(acc api.AccountID, key string, busy bool) {
	if w.conv == nil {
		return
	}
	if c := w.conv.issueCard; c != nil && c.account == acc {
		c.setBusy(busy, key)
	}
}

// conversationApplyIssue shows the refreshed issue of a transition on the
// conversation's issue card at once when it is the issue on show; the
// members follow with the daemon's notifications (issueActions onIssue).
func (w *Window) conversationApplyIssue(acc api.AccountID, info api.IssueInfo) {
	if w.conv == nil {
		return
	}
	c := w.conv.issueCard
	if c == nil || c.account != acc || c.key != jira.Clean(info.Key) {
		return
	}
	card := jira.IssueCard(info, nil, i18n.Tr)
	r := issueReading{
		card:     card,
		subject:  card.Summary,
		openable: card.URL != "" && jira.IsIssueURL(card.URL, w.model.issueSite(acc)),
	}
	c.show(&r, acc, w.issues.canTransition(acc))
}
