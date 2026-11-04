// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChatBody.swift
// (Conversation.BubbleBody, an enum with associated values: a closed record
// hierarchy here). Swift-first: ui/internal/conversation/chat.go leaves the
// states of a bubble's body to the clients (BubbleText is the shared part).
// Html is the bubble that renders its message's HTML (the preference
// conversation-chat-html, Conversation.BubbleRendersHtml).

using System.Collections.Generic;
using Malachi.Core.Api;

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

    /// <summary>
    /// The message's sanitized HTML, for the locked web view of the timeline's
    /// cards (<see cref="Conversation.BubbleRendersHtml"/>): the same
    /// fragment the card of that message loads (message.body's html with
    /// trimQuoted). The view shows <paramref name="Fallback"/> while the
    /// bubble holds no web view (far from the viewport, beyond the cap, or
    /// the view could not show the document).
    /// </summary>
    /// <param name="Value">The sanitized fragment (never raw mail: message.body's html).</param>
    /// <param name="Links">The body's links, for the card's link path (LinkOpener).</param>
    /// <param name="Fallback">The text the bubble shows without a web view, as <see cref="Text"/> would be.</param>
    public sealed record Html(string Value, IReadOnlyList<Link> Links, string Fallback) : BubbleBody;
}
