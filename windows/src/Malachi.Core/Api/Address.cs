// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Messages.swift (Address); Go:
// backend/pkg/api/types.go (Address).
//
// Stand-in for the API layer's Address (work package C1), which Text/Format
// needs: the same shape as C1's declaration in Api/Messages.cs. When both
// are merged, C1's declaration stays and this file goes.

using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.Address: a parsed RFC 5322 mailbox. Both fields are
/// attacker-controlled display data; never interpret them as markup.
/// </summary>
public sealed record Address
{
    /// <summary>The display name, when there is one.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>
    /// The address (Swift <c>address</c>, the wire's <c>"address"</c>): C#
    /// allows no member named like its type.
    /// </summary>
    [JsonPropertyName("address")]
    public required string Email { get; init; }
}
