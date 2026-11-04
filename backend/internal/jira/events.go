// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Events: a changelog entry that changed the status or the assignee is one
// message of the issue (item "event"), sent by who made the change at the
// time they made it. It is stored read and never announced (the user did
// not miss a conversation), and it is left out altogether with
// JiraConfig.HideEvents. Its body is language-neutral: one line per
// change, "<from> → <to>" with "—" for an empty side (no earlier value,
// unassigned), such as "To Do → In Progress" or "— → Jana Dvořáková";
// clients build the sentence from MessageIssue.Changes, never from the
// body.

var (
	eventArrow = " " + string(rune(0x2192)) + " " // →
	eventNone  = string(rune(0x2014))             // —
)

// eventItems describes the issue's events, oldest first (hist is in that
// order already); an entry without a change of a watched field makes
// none.
func (y *synth) eventItems(is Issue, hist []History, descMsgID string) []*item {
	var out []*item
	for _, h := range hist {
		if len(h.Changes) == 0 {
			continue
		}
		changes := make([]api.IssueChange, 0, len(h.Changes))
		for _, c := range h.Changes {
			changes = append(changes, api.IssueChange{Field: c.Field, From: c.From, To: c.To})
		}
		out = append(out, &item{
			remoteID: "h:" + h.ID, kind: api.IssueItemEvent, issueID: is.ID,
			authorID: h.Author.ID, mine: y.isMe(h.Author.ID),
			from: userAddress(h.Author.ID, h.Author.Name),
			date: h.Created, created: h.Created, updated: h.Created,
			text: eventText(changes), changes: changes,
			msgID: y.historyMsgID(is.ID, h.ID), inReplyTo: descMsgID,
		})
	}
	return out
}

// eventText is the body of an event message.
func eventText(changes []api.IssueChange) string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, orNone(c.From)+eventArrow+orNone(c.To))
	}
	return strings.Join(lines, "\n")
}

func orNone(s string) string {
	if s == "" {
		return eventNone
	}
	return s
}
