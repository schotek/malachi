// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Threads.swift; Go:
// backend/pkg/api/types.go ("Threads"); contract: docs/api.md §4.4.
//
// Computed per account, listed per folder: every field of a summary a
// folder listing returns describes the members in that folder, except
// FolderIds.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.ThreadSummary.</summary>
public sealed record ThreadSummary
{
    /// <summary>The conversation.</summary>
    [JsonPropertyName("id")]
    public required ThreadId Id { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The newest member's, Re:/Fwd: stripped.</summary>
    [JsonPropertyName("subject")]
    public required string Subject { get; init; }

    /// <summary>Distinct senders, newest first, at most <see cref="API.Limits.MaxThreadParticipants"/>.</summary>
    [JsonPropertyName("participants")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Address>))]
    public IReadOnlyList<Address> Participants { get; init => field = value ?? []; } = [];

    /// <summary>Members in scope.</summary>
    [JsonPropertyName("messageCount")]
    public required int MessageCount { get; init; }

    /// <summary>Unread members in scope.</summary>
    [JsonPropertyName("unreadCount")]
    public required int UnreadCount { get; init; }

    /// <summary>The date of <see cref="Latest"/>.</summary>
    [JsonPropertyName("latestDate")]
    public required DateTimeOffset LatestDate { get; init; }

    /// <summary>The newest member in scope, in full.</summary>
    [JsonPropertyName("latest")]
    public required MessageSummary Latest { get; init; }

    /// <summary>From the latest member.</summary>
    [JsonPropertyName("snippet")]
    public required string Snippet { get; init; }

    /// <summary>Union of member flags; read state comes from <see cref="UnreadCount"/>.</summary>
    [JsonPropertyName("flags")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Flag>))]
    public IReadOnlyList<Flag> Flags { get; init => field = value ?? []; } = [];

    /// <summary>Whether some member carries attachments.</summary>
    [JsonPropertyName("hasAttachments")]
    public required bool HasAttachments { get; init; }

    /// <summary>Every folder of the account with at least one member, whatever the scope.</summary>
    [JsonPropertyName("folderIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<FolderId>))]
    public IReadOnlyList<FolderId> FolderIds { get; init => field = value ?? []; } = [];

    /// <summary>
    /// Present only for a thread of a jira account, which is one issue;
    /// <see cref="Latest"/> may then be an event row.
    /// </summary>
    [JsonPropertyName("issue")]
    public IssueInfo? Issue { get; init; }
}

/// <summary>
/// api.ThreadListParams: a thread is unread or flagged when any member in
/// the folder is.
/// </summary>
public sealed record ThreadListParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>Which page.</summary>
    [JsonPropertyName("page")]
    [JsonRequired]
    public Page Page { get; init; } = new();

    /// <summary>dateDesc (the default) or dateAsc.</summary>
    [JsonPropertyName("sort")]
    public SortOrder? Sort { get; init; }

    /// <summary>all (the default), unread or flagged.</summary>
    [JsonPropertyName("filter")]
    public MessageFilter? Filter { get; init; }
}

/// <summary>api.ThreadListResult.</summary>
public sealed record ThreadListResult
{
    /// <summary>The threads of the page.</summary>
    [JsonPropertyName("threads")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ThreadSummary>))]
    public IReadOnlyList<ThreadSummary> Threads { get; init => field = value ?? []; } = [];

    /// <summary>The next cursor and the total.</summary>
    [JsonPropertyName("page")]
    public required PageInfo Page { get; init; }
}

/// <summary>
/// api.ThreadGetParams. <see cref="FolderId"/> restricts the members and the
/// summary to one folder; null = every member of the account.
/// </summary>
public sealed record ThreadGetParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The conversation.</summary>
    [JsonPropertyName("threadId")]
    public required ThreadId ThreadId { get; init; }

    /// <summary>The folder to restrict to.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init; }
}

/// <summary>
/// api.ThreadGetResult: <see cref="Messages"/> oldest first, at most
/// <see cref="API.Limits.MaxThreadMessages"/> (the newest).
/// </summary>
public sealed record ThreadGetResult
{
    /// <summary>The summary over the members returned.</summary>
    [JsonPropertyName("thread")]
    public required ThreadSummary Thread { get; init; }

    /// <summary>The members, oldest first.</summary>
    [JsonPropertyName("messages")]
    [JsonConverter(typeof(NullAsEmptyListConverter<MessageSummary>))]
    public IReadOnlyList<MessageSummary> Messages { get; init => field = value ?? []; } = [];
}
