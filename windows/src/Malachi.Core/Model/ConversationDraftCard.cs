// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/drafts.go (DraftCard); macOS:
// ConversationDrafts.swift (Conversation.DraftCard). Immutable.

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// conversation.DraftCard: one draft card of the conversation view, the
/// user's unsent reply to the conversation (on an issue the unsent comment),
/// made by <see cref="Conversation.Drafts"/>. Every text is hostile input,
/// shown as plain text.
/// </summary>
public sealed record ConversationDraftCard
{
    /// <summary>The draft as thread.get returned it: what Edit opens.</summary>
    public required Draft Draft { get; init; }

    /// <summary>
    /// The name of the card's avatar: the user's own (the account's display
    /// name, else its address), cleaned for one line.
    /// </summary>
    public string Sender { get; init; } = "";

    /// <summary>
    /// The draft's plain text (the daemon's, derived from its HTML) as the
    /// card shows it: cleaned (<see cref="Malachi.Core.Boards.Board.CleanBlock"/>), at most
    /// <see cref="Conversation.DraftTextBytes"/> UTF-8 bytes; "" for an empty
    /// draft.
    /// </summary>
    public string Text { get; init; } = "";

    /// <summary>A comment draft of an issue (<see cref="Api.Draft.Comment"/>): Edit opens the comment window.</summary>
    public bool Comment { get; init; }

    /// <summary>A comment draft only the service-desk team will read, which shows the badge <see cref="InternalLabel"/>.</summary>
    public bool Internal { get; init; }

    /// <summary>The badge; "" when not internal.</summary>
    public string InternalLabel { get; init; } = "";
}
