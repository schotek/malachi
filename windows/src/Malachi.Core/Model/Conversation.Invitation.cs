// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/invitation.go (IsInvitationReply,
// InvitationReplyLine, invitationReplyItem); macOS:
// MalachiCore/Model/ConversationInvitation.swift.
//
// An answer to an invitation is a compact row of the conversation, never a
// card or a bubble. Go's empty EventResponse is null here.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>
    /// conversation.IsInvitationReply: whether member <paramref name="s"/> is
    /// an answer to an invitation (an iTIP REPLY,
    /// <see cref="MessageSummary.Invitation"/>): a message with an empty body
    /// that only carries the answer, the user's own from Sent or an
    /// attendee's. The conversation shows it as a compact row
    /// (<see cref="ConversationItemKind.Event"/>) with one sentence
    /// (<see cref="InvitationReplyLine"/>), never as a card or a bubble.
    /// </summary>
    public static bool IsInvitationReply(MessageSummary? s) =>
        s?.Invitation is { } inv && inv.Method.Value == InvitationMethod.Reply;

    /// <summary>
    /// conversation.InvitationReplyLine: the sentence of an answer to an
    /// invitation: what was answered; who answered is the row's sender, so
    /// the sentence names nobody and has no grammatical gender.
    /// </summary>
    public static string InvitationReplyLine(EventResponse? r)
    {
        switch (r?.Value)
        {
            case EventResponse.Accepted:
                // TRANSLATORS: a row of a conversation: someone (named beside it) accepted the invitation.
                return L10n.T("Invitation accepted");
            case EventResponse.Tentative:
                // TRANSLATORS: a row of a conversation: someone (named beside it) answered the invitation with Maybe.
                return L10n.T("Invitation tentatively accepted");
            case EventResponse.Declined:
                // TRANSLATORS: a row of a conversation: someone (named beside it) declined the invitation.
                return L10n.T("Invitation declined");
            default:
                // TRANSLATORS: a row of a conversation: someone (named beside it) answered the invitation, the answer none of the usual three.
                return L10n.T("Replied to the invitation");
        }
    }

    // invitationReplyItem: it (a member's item) as the compact row of an
    // answer to an invitation. Unlike an issue's event it keeps its unread
    // mark: an attendee's answer arrives unread and is marked read like a
    // card (MarkRead).
    private static ConversationItem InvitationReplyItem(ConversationItem it)
    {
        var line = InvitationReplyLine(it.Message!.Invitation!.ReplyResponse);
        return it with
        {
            Kind = ConversationItemKind.Event,
            EventLines = [line],
            EventText = line,
            Unread = !it.Message.Flags.Contains(Flag.Seen),
        };
    }
}
