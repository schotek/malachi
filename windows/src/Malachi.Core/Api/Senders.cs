// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Senders.swift; Go:
// backend/pkg/api/types.go (KnownSender, SenderListResult, SenderAddParams,
// SenderRemoveParams); contract: docs/api.md §4.9.
//
// The allow-list behind the knownSenders remote-content policy: never fed
// from incoming From headers, matched on the bare address.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.KnownSender.</summary>
public sealed record KnownSender
{
    /// <summary>The bare address.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>sent or user.</summary>
    [JsonPropertyName("source")]
    public required KnownSenderSource Source { get; init; }

    /// <summary>When it was added.</summary>
    [JsonPropertyName("addedAt")]
    public required DateTimeOffset AddedAt { get; init; }
}

/// <summary>api.SenderListResult.</summary>
public sealed record SenderListResult
{
    /// <summary>The known senders.</summary>
    [JsonPropertyName("senders")]
    [JsonConverter(typeof(NullAsEmptyListConverter<KnownSender>))]
    public IReadOnlyList<KnownSender> Senders { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.SenderAddParams: a bare address or <c>Name &lt;address&gt;</c>; only
/// the address is stored.
/// </summary>
public sealed record SenderAddParams
{
    /// <summary>The address to trust.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }
}

/// <summary>api.SenderRemoveParams. Removing an unknown address is not an error.</summary>
public sealed record SenderRemoveParams
{
    /// <summary>The address to forget.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }
}
