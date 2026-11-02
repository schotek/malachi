// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import "github.com/schotek/malachi/backend/pkg/api"

// IsInvitationReply reports whether member s is an answer to an
// invitation (an iTIP REPLY, MessageSummary.Invitation): a message with an
// empty body that only carries the answer, the user's own from Sent or an
// attendee's. The conversation shows it as a compact row (ItemEvent) with
// one sentence (InvitationReplyLine), never as a card or a bubble.
func IsInvitationReply(s api.MessageSummary) bool {
	return s.Invitation != nil && s.Invitation.Method == api.InvitationMethodReply
}

// InvitationReplyLine is the sentence of an answer to an invitation: what
// was answered; who answered is the row's sender, so the sentence names
// nobody and has no grammatical gender.
func InvitationReplyLine(r api.EventResponse, tr Translator) string {
	switch r {
	case api.ResponseAccepted:
		// TRANSLATORS: a row of a conversation: someone (named beside it)
		// accepted the invitation.
		return tr.T("Invitation accepted")
	case api.ResponseTentative:
		// TRANSLATORS: a row of a conversation: someone (named beside it)
		// answered the invitation with Maybe.
		return tr.T("Invitation tentatively accepted")
	case api.ResponseDeclined:
		// TRANSLATORS: a row of a conversation: someone (named beside it)
		// declined the invitation.
		return tr.T("Invitation declined")
	}
	// TRANSLATORS: a row of a conversation: someone (named beside it)
	// answered the invitation, the answer none of the usual three.
	return tr.T("Replied to the invitation")
}

// invitationReplyItem makes it (a member's item) the compact row of an
// answer to an invitation. Unlike an issue's event it keeps its unread
// mark: an attendee's answer arrives unread and is marked read like a
// card (markRead).
func invitationReplyItem(it Item, tr Translator) Item {
	line := InvitationReplyLine(it.Message.Invitation.ReplyResponse, tr)
	it.Kind = ItemEvent
	it.EventLines = []string{line}
	it.EventText = line
	it.Unread = !hasFlag(it.Message.Flags, api.FlagSeen)
	return it
}
