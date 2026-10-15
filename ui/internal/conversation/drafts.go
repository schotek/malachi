// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The user's unsent replies to the conversation (thread.get's drafts with
// withDrafts; api.ThreadSummary.DraftCount counts them), on an issue the
// unsent comments, are draft cards: the pane shows them right under what
// opened the conversation, above the newest member, newest first
// (DraftsFirst), with a Draft badge, when they were saved, their plain
// text and Edit and Discard. They are no members: never unread, never
// marked read, never folded, never among the messages the conversation's
// actions take, and not in Model.Items (whose rules they would change).
// Edit opens the draft in a compose window (a comment window for a comment
// draft); Discard asks DiscardQuestion and deletes it (draft.delete).
// notify.draftsChanged naming the thread asks thread.get again.

// draftTextBytes caps the text a draft card shows; Edit shows it whole.
const draftTextBytes = 64 << 10

// DraftCard is one draft card.
type DraftCard struct {
	// Draft is the draft as thread.get returned it: what Edit opens.
	Draft api.Draft
	// Sender is the name of the card's avatar: the user's own (the
	// account's display name, else its address), cleaned for one line.
	Sender string
	// Text is the draft's plain text (the daemon's, derived from its
	// HTML) as the card shows it: cleaned (board.CleanBlock), at most
	// draftTextBytes; "" for an empty draft.
	Text string
	// Comment marks a comment draft of an issue (api.Draft.Comment): Edit
	// opens the comment window.
	Comment bool
	// Internal marks a comment draft only the service-desk team will read,
	// which shows the badge InternalLabel ("" when not internal).
	Internal      bool
	InternalLabel string
}

// Drafts makes the draft cards of a conversation from thread.get's
// drafts: those of the account only, without an id or with a repeated
// one (the first kept), newest updatedAt first (ties by id, the greater
// first), at most api.MaxThreadDrafts.
func Drafts(drafts []api.Draft, account api.Account, tr Translator) []DraftCard {
	seen := make(map[api.DraftID]bool, len(drafts))
	list := make([]api.Draft, 0, len(drafts))
	for _, d := range drafts {
		if d.ID == "" || seen[d.ID] || (account.ID != "" && d.AccountID != "" && d.AccountID != account.ID) {
			continue
		}
		seen[d.ID] = true
		list = append(list, d)
	}
	slices.SortStableFunc(list, func(a, b api.Draft) int {
		switch {
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		case b.UpdatedAt.After(a.UpdatedAt):
			return 1
		}
		return strings.Compare(string(b.ID), string(a.ID))
	})
	if len(list) > api.MaxThreadDrafts {
		list = list[:api.MaxThreadDrafts]
	}
	name := jira.Clean(account.Config.DisplayName)
	if name == "" {
		name = jira.Clean(account.Config.Email)
	}
	out := make([]DraftCard, 0, len(list))
	for _, d := range list {
		c := DraftCard{Draft: d, Sender: name, Text: board.CleanBlock(d.TextBody, draftTextBytes), Comment: d.Comment != nil}
		if d.Comment != nil && d.Comment.Visibility == api.CommentInternal {
			c.Internal, c.InternalLabel = true, jira.InternalLabel(tr)
		}
		out = append(out, c)
	}
	return out
}

// SameDrafts reports whether two lists of draft cards show the same: the
// same drafts in the same order at the same versions.
func SameDrafts(a, b []DraftCard) bool {
	return slices.EqualFunc(a, b, func(x, y DraftCard) bool {
		return x.Draft.ID == y.Draft.ID && x.Draft.Version == y.Draft.Version && x.Text == y.Text && x.Internal == y.Internal
	})
}

// DraftBadge is the badge of a draft card.
func DraftBadge(tr Translator) string {
	// TRANSLATORS: a badge on a card of the user's unsent reply or comment in a conversation.
	return tr.C("conversation", "Draft")
}

// DraftEditLabel is the button of a draft card that opens it for editing.
func DraftEditLabel(tr Translator) string {
	return tr.T("Edit")
}

// DraftDiscardLabel is the button of a draft card that deletes it, after
// DiscardQuestion.
func DraftDiscardLabel(tr Translator) string {
	return tr.T("_Discard")
}

// DiscardQuestion is the question before a draft card's Discard deletes
// the draft (the compose window's own question).
func DiscardQuestion(tr Translator) string {
	return tr.T("Discard this message?")
}

// DraftGone is the toast of an Edit whose draft is gone (sent or deleted
// meanwhile).
func DraftGone(tr Translator) string {
	return tr.T("The draft is no longer there")
}

// DraftSaved is the line of a draft card that says when it was saved;
// when is the client's own short date of Draft.UpdatedAt.
func DraftSaved(when string, tr Translator) string {
	return fmt.Sprintf(tr.T("Draft saved %s"), when)
}

// DraftsFirst reports where the pane puts the draft cards among the items
// it shows (convDisplayOrder and its ports): right after the card that
// opened the conversation when it is first (root is 0), else first.
func DraftsFirst(root int) int {
	if root == 0 {
		return 1
	}
	return 0
}
