// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Search.swift; Go:
// backend/pkg/api/types.go ("Search"); contract: docs/api.md §4.6.
//
// The daemon searches its local store, newest first; Ranges are byte ranges
// into Snippet.

using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.SearchQueryParams. <see cref="AccountId"/> null = every account.</summary>
public sealed record SearchQueryParams
{
    /// <summary>The account to search; null for every enabled one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The folder to search; needs <see cref="AccountId"/>.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init; }

    /// <summary>The user-typed query (FTS5 subset with field prefixes; docs/api.md §4.6).</summary>
    [JsonPropertyName("query")]
    public required string Query { get; init; }

    /// <summary>Which page of the results.</summary>
    [JsonPropertyName("page")]
    [JsonRequired]
    public Page Page { get; init; } = new();
}

/// <summary>api.MatchRange: a byte range within <see cref="SearchResult.Snippet"/>.</summary>
public sealed record MatchRange
{
    /// <summary>The first byte.</summary>
    [JsonPropertyName("start")]
    public required int Start { get; init; }

    /// <summary>The byte after the last.</summary>
    [JsonPropertyName("end")]
    public required int End { get; init; }
}

/// <summary>api.SearchResult. <see cref="Snippet"/> is a plain-text excerpt, never HTML.</summary>
public sealed record SearchResult
{
    /// <summary>The message found.</summary>
    [JsonPropertyName("message")]
    public required MessageSummary Message { get; init; }

    /// <summary>Up to 200 characters around the first match.</summary>
    [JsonPropertyName("snippet")]
    public required string Snippet { get; init; }

    /// <summary>The matched words in <see cref="Snippet"/>; absent when the body did not match.</summary>
    [JsonPropertyName("ranges")]
    public IReadOnlyList<MatchRange>? Ranges { get; init; }

    /// <summary>Reserved; always 0.</summary>
    [JsonPropertyName("score")]
    public required double Score { get; init; }
}

/// <summary>api.SearchQueryResult.</summary>
public sealed record SearchQueryResult
{
    /// <summary>The results, newest first.</summary>
    [JsonPropertyName("results")]
    [JsonConverter(typeof(NullAsEmptyListConverter<SearchResult>))]
    public IReadOnlyList<SearchResult> Results { get; init => field = value ?? []; } = [];

    /// <summary>The page; total exact up to <see cref="API.Limits.MaxSearchTotal"/>, -1 beyond.</summary>
    [JsonPropertyName("page")]
    public required PageInfo Page { get; init; }
}
