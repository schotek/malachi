// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChatBody.swift
// (Conversation.BubbleAttachments, an enum with associated values: a closed
// record hierarchy here). Swift-first: ui/internal/conversation/chat.go
// leaves a bubble's header to the clients.

namespace Malachi.Core.Model;

/// <summary>
/// What the header of a message bubble shows of the message's attachments
/// (docs/plans/chat-view.md D3; <see cref="Conversation.BubbleAttachmentBadge"/>):
/// a paperclip pill, with how many once known.
/// </summary>
public abstract record BubbleAttachments
{
    private BubbleAttachments()
    {
    }

    /// <summary>No pill.</summary>
    public sealed record None : BubbleAttachments;

    /// <summary>
    /// The pill without a number: the summary says the message has
    /// attachments, message.get has not told which yet.
    /// </summary>
    public sealed record Some : BubbleAttachments;

    /// <summary>
    /// The pill with the number of chips the timeline's card shows for the
    /// message (<see cref="AttachmentChips.ChipAttachments"/>).
    /// </summary>
    /// <param name="Value">How many chips, more than zero.</param>
    public sealed record Count(int Value) : BubbleAttachments;
}
