// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Contacts.swift; Go:
// backend/pkg/api/types.go (Contact, ContactSearchParams,
// ContactSearchResult); contract: docs/api.md §4.11.

using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.Contact: one recipient suggestion. <see cref="Name"/> and
/// <see cref="Book"/> are untrusted display text.
/// </summary>
public sealed record Contact
{
    /// <summary>The display name, when there is one.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>Normalised, syntactically valid.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>sent or addressBook.</summary>
    [JsonPropertyName("source")]
    public required ContactSource Source { get; init; }

    /// <summary>The address book the contact came from; null for a collected address.</summary>
    [JsonPropertyName("book")]
    public string? Book { get; init; }
}

/// <summary>
/// api.ContactSearchParams. <see cref="Limit"/> null =
/// <see cref="API.Limits.DefaultContactLimit"/>, clamped to
/// <see cref="API.Limits.MaxContactLimit"/>; <see cref="Query"/> at most
/// <see cref="API.Limits.MaxContactQueryBytes"/>.
/// </summary>
public sealed record ContactSearchParams
{
    /// <summary>The sending account, whose address books are searched.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>What the user typed.</summary>
    [JsonPropertyName("query")]
    public required string Query { get; init; }

    /// <summary>How many suggestions; null is the default.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.ContactSearchResult: ranked best first.</summary>
public sealed record ContactSearchResult
{
    /// <summary>The suggestions.</summary>
    [JsonPropertyName("contacts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Contact>))]
    public IReadOnlyList<Contact> Contacts { get; init => field = value ?? []; } = [];
}
