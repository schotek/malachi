// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Conversation.swift
// (Conversation.Item); GTK: ui/internal/conversation/conversation.go (Item).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// conversation.Item: one entry of the stack. Its equality compares
/// <see cref="EventLines"/> by reference, as every record's does.
/// </summary>
public sealed record ConversationItem
{
    /// <summary>What the item is.</summary>
    public ConversationItemKind Kind { get; init; }

    /// <summary>The member (Message and Event); null for Truncated.</summary>
    public MessageSummary? Message { get; init; }

    /// <summary>The member's id (Swift <c>id</c>); null for Truncated.</summary>
    public MessageId? Id => Message?.Id;

    /// <summary>
    /// The name the card's compact header shows: the display name (else the
    /// address) of the first sender that has one, cleaned for one line; ""
    /// when there is none.
    /// </summary>
    public string Sender { get; init; } = "";

    /// <summary>An unread message card; an event is never unread, whatever its flags.</summary>
    public bool Unread { get; init; }

    /// <summary>
    /// A member the account's own user wrote: the pane tints its avatar with
    /// the accent colour and changes nothing else. An item of an issue says so
    /// itself (MessageIssue.Mine), and a comment an integration relayed is
    /// never the user's; a mail message is the user's when the address of its
    /// first sender is the account's, compared without case and surrounding
    /// space. An address is what the sender wrote: a forged From looks like
    /// the user's own message.
    /// </summary>
    public bool Mine { get; init; }

    /// <summary>An internal comment of a service-desk issue, which shows the badge <see cref="InternalLabel"/>.</summary>
    public bool Internal { get; init; }

    /// <summary>The badge; "" when not internal.</summary>
    public string InternalLabel { get; init; } = "";

    /// <summary>The integration that posted a comment for its author ("via Issue Sync"); "" when none.</summary>
    public string Via { get; init; } = "";

    /// <summary>The badge of a comment changed after it was posted; "" when not.</summary>
    public string Edited { get; init; } = "";

    /// <summary>The sentences of an event, one per change; empty for other kinds.</summary>
    public IReadOnlyList<string> EventLines { get; init; } = [];

    /// <summary>The event's sentences on one line, for an accessible name; "" for other kinds.</summary>
    public string EventText { get; init; } = "";

    /// <summary>The sentence of the Truncated row ("112 earlier messages are not shown"); "" for other kinds.</summary>
    public string Text { get; init; } = "";
}
