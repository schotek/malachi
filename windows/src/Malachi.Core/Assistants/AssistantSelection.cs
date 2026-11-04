// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/Assistant.swift
// (Assistant.Selection, Equatable); GTK: ui/internal/assistant (Selection).
// A record compares an IReadOnlyList by reference, so the equality here is
// by the ids, as Swift's arrays compare.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Assistants;

/// <summary>
/// What a message action works on: the account and the messages, newest
/// first (the members of a conversation in the folder, or the one message).
/// Opaque ids of the API, as strings.
/// </summary>
public sealed record AssistantSelection
{
    /// <summary>A selection of <paramref name="messageIds"/> (newest first) in <paramref name="accountId"/>.</summary>
    public AssistantSelection(string accountId, IReadOnlyList<string> messageIds)
    {
        ArgumentNullException.ThrowIfNull(accountId);
        ArgumentNullException.ThrowIfNull(messageIds);
        AccountId = accountId;
        MessageIds = messageIds.ToArray();
    }

    /// <summary>The account id.</summary>
    public string AccountId { get; }

    /// <summary>The message ids, newest first.</summary>
    public IReadOnlyList<string> MessageIds { get; }

    /// <inheritdoc/>
    public bool Equals(AssistantSelection? other) =>
        other is not null && string.Equals(AccountId, other.AccountId, StringComparison.Ordinal)
        && MessageIds.SequenceEqual(other.MessageIds, StringComparer.Ordinal);

    /// <inheritdoc/>
    public override int GetHashCode()
    {
        var hash = new HashCode();
        hash.Add(AccountId, StringComparer.Ordinal);
        foreach (var id in MessageIds)
        {
            hash.Add(id, StringComparer.Ordinal);
        }
        return hash.ToHashCode();
    }
}
