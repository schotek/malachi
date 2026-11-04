// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// conversationIssueCard is the issue card on top of a Jira conversation
// (convIssueSlot.show): the pane's issue card (issue_card.go) with the
// conversation's issue, its key a link on the account's own site only, its
// status pill the Change Status menu on an account that changes statuses.
// The menu acts on the issue through the conversation's first member shown
// (any member names the issue). Each layout of the conversation has its
// own card in its own slot (the timeline's beside its gutter, the chat's
// across the column), built anew only when the issue changed; the host
// hands the transitions' spinner and result to both
// (conversationSetIssueBusy, conversationApplyIssue), and Change Status in
// the menus pops up the card of the layout shown
// (conversationHost.shownIssueCard). Two cards are simpler than moving one
// between the layouts' columns, whose anchors and scroll positions it
// belongs to (as the macOS client's ConversationIssueBox).
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
// the conversation's issue cards (issueActions onBusy).
func (w *Window) conversationSetIssueBusy(acc api.AccountID, key string, busy bool) {
	if w.conv == nil {
		return
	}
	for _, slot := range w.conv.issueSlots() {
		if c := slot.card; c != nil && c.account == acc {
			c.setBusy(busy, key)
		}
	}
}

// conversationApplyIssue shows the refreshed issue of a transition on the
// conversation's issue card at once when it is the issue on show; the
// members follow with the daemon's notifications (issueActions onIssue).
func (w *Window) conversationApplyIssue(acc api.AccountID, info api.IssueInfo) {
	if w.conv == nil {
		return
	}
	for _, slot := range w.conv.issueSlots() {
		c := slot.card
		if c == nil || c.account != acc || c.key != jira.Clean(info.Key) {
			continue
		}
		card := jira.IssueCard(info, nil, i18n.Tr)
		r := issueReading{
			card:     card,
			subject:  card.Summary,
			openable: card.URL != "" && jira.IsIssueURL(card.URL, w.model.issueSite(acc)),
		}
		c.show(&r, acc, w.issues.canTransition(acc))
	}
}

// convIssueSlot is a layout's place for the issue card of a Jira
// conversation, on top of its column: the box from the layout's Blueprint,
// the card on show and what it was built from.
type convIssueSlot struct {
	box   *gtk.Box
	card  *issueCard
	shown *convIssueKey
}

// show shows the issue card of m once on top (built by the window,
// conversationIssueCard) when m is a Jira conversation, anew only when its
// issue changed; a mail conversation has none.
func (s *convIssueSlot) show(w *Window, ctrl *conversationController, m *conversation.Model) {
	info := ctrl.issue
	first, ok := convFirstMember(m)
	if m.Issue == nil || info == nil || !ok {
		s.clear()
		return
	}
	key := &convIssueKey{card: *m.Issue, account: first.AccountID, issue: *info}
	if s.shown != nil && reflect.DeepEqual(*s.shown, *key) {
		return
	}
	account, _ := w.model.account(first.AccountID)
	s.clear()
	card := w.conversationIssueCard(*m.Issue, account, *info)
	s.card = card
	s.shown = key
	s.box.Append(card)
	s.box.SetVisible(true)
}

// clear takes the issue card out.
func (s *convIssueSlot) clear() {
	if s.card != nil {
		s.box.Remove(s.card)
		s.card = nil
	}
	s.shown = nil
	s.box.SetVisible(false)
}
