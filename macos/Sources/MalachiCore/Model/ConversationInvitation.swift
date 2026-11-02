// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// Port of ui/internal/conversation/invitation.go: an answer to an
// invitation is a compact row of the conversation, never a card or a
// bubble.

extension Conversation {
    /// conversation.IsInvitationReply: whether member `s` is an answer to
    /// an invitation (an iTIP REPLY, `MessageSummary.invitation`): a
    /// message with an empty body that only carries the answer, the user's
    /// own from Sent or an attendee's. The conversation shows it as a
    /// compact row (`.event`) with one sentence (`invitationReplyLine`).
    public static func isInvitationReply(_ s: MessageSummary) -> Bool {
        s.invitation?.method == .reply
    }

    /// conversation.InvitationReplyLine: the sentence of an answer to an
    /// invitation: what was answered; who answered is the row's sender, so
    /// the sentence names nobody and has no grammatical gender.
    public static func invitationReplyLine(_ r: EventResponse?) -> String {
        switch r ?? .needsAction {
        case .accepted:
            // TRANSLATORS: a row of a conversation: someone (named beside it)
            // accepted the invitation.
            return L10n.T("Invitation accepted")
        case .tentative:
            // TRANSLATORS: a row of a conversation: someone (named beside it)
            // answered the invitation with Maybe.
            return L10n.T("Invitation tentatively accepted")
        case .declined:
            // TRANSLATORS: a row of a conversation: someone (named beside it)
            // declined the invitation.
            return L10n.T("Invitation declined")
        default:
            // TRANSLATORS: a row of a conversation: someone (named beside it)
            // answered the invitation, the answer none of the usual three.
            return L10n.T("Replied to the invitation")
        }
    }

    /// conversation.invitationReplyItem: `it` (a member's item) as the
    /// compact row of an answer to an invitation. Unlike an issue's event
    /// it keeps its unread mark: an attendee's answer arrives unread and is
    /// marked read like a card (`markRead`).
    static func invitationReplyItem(_ it: Item) -> Item {
        var it = it
        let line = invitationReplyLine(it.message?.invitation?.replyResponse)
        it.kind = .event
        it.eventLines = [line]
        it.eventText = line
        it.unread = !(it.message?.flags.contains(.seen) ?? false)
        return it
    }
}
