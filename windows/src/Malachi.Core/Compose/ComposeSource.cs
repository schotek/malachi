// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeParams.swift
// (ComposeSource); GTK: ui/internal/compose/prefill.go (Source).
//
// Swift's == compares the lists by value; so does Equals here (a record
// would compare them by reference).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>
/// compose.Source: what the caller knows about the message being replied to
/// or forwarded. Every field is hostile input. A null <see cref="Date"/> (or
/// Go's zero time) is a message without one.
/// </summary>
public sealed record ComposeSource
{
    /// <summary>The message; empty when unknown.</summary>
    public MessageId Id { get; init; } = new("");

    /// <summary>Its account; empty when unknown.</summary>
    public AccountId AccountId { get; init; } = new("");

    /// <summary>The From header.</summary>
    public IReadOnlyList<Address> From { get; init => field = value ?? []; } = [];

    /// <summary>The Reply-To header; replies go here instead of <see cref="From"/>.</summary>
    public IReadOnlyList<Address> ReplyTo { get; init => field = value ?? []; } = [];

    /// <summary>The To header.</summary>
    public IReadOnlyList<Address> To { get; init => field = value ?? []; } = [];

    /// <summary>The Cc header.</summary>
    public IReadOnlyList<Address> Cc { get; init => field = value ?? []; } = [];

    /// <summary>The subject.</summary>
    public string Subject { get; init => field = value ?? ""; } = "";

    /// <summary>The date; null for none.</summary>
    public DateTimeOffset? Date { get; init; }

    /// <summary>The plain-text body.</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <inheritdoc/>
    public bool Equals(ComposeSource? other) =>
        other is not null && Id == other.Id && AccountId == other.AccountId
        && From.SequenceEqual(other.From) && ReplyTo.SequenceEqual(other.ReplyTo)
        && To.SequenceEqual(other.To) && Cc.SequenceEqual(other.Cc)
        && string.Equals(Subject, other.Subject, StringComparison.Ordinal) && Date == other.Date
        && string.Equals(Text, other.Text, StringComparison.Ordinal);

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Id, AccountId, From.Count, To.Count, Subject, Date);

    /// <summary>
    /// The ids and the sizes of the rest: never an address, the subject, the
    /// date or the text (docs/windows-port.md §3.1: no mail content in logs).
    /// </summary>
    public override string ToString() => string.Create(
        CultureInfo.InvariantCulture,
        $"ComposeSource(id: {Id}, accountId: {AccountId}, from: {From.Count}, replyTo: {ReplyTo.Count}, to: {To.Count}, "
        + $"cc: {Cc.Count}, subject: {Subject.Length} chars, date: {(Date is null ? "null" : "set")}, text: {Text.Length} chars)");
}
