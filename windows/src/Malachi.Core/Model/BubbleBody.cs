// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChatBody.swift
// (Conversation.BubbleBody, an enum with associated values: a closed record
// hierarchy here). Swift-first: ui/internal/conversation/chat.go leaves the
// states of a bubble's body to the clients (BubbleText is the shared part).

namespace Malachi.Core.Model;

/// <summary>
/// What a message bubble of the chat shows as its text
/// (<see cref="Conversation.BubbleBodyFor"/>).
/// </summary>
public abstract record BubbleBody
{
    private BubbleBody()
    {
    }

    /// <summary>Nothing loaded yet: the summary's snippet, dimmed (docs/plans/chat-view.md D17).</summary>
    /// <param name="Value">The snippet, cleaned as a bubble's text (<see cref="Conversation.BubbleText"/>).</param>
    public sealed record Placeholder(string Value) : BubbleBody;

    /// <summary>
    /// The message's text (<see cref="Conversation.BubbleText"/>), or the
    /// sentence the timeline's cards show for the state of the body (on its
    /// way, too large, unreadable, empty) or for the error of the call.
    /// </summary>
    /// <param name="Value">The text, plain.</param>
    public sealed record Text(string Value) : BubbleBody;
}
