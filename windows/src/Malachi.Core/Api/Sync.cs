// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Sync.swift; Go:
// backend/pkg/api/types.go ("Sync"); contract: docs/api.md §3 SyncState,
// §4.7.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.SyncState: the live state of one account's syncer (also
/// <c>Account.State</c> and the payload of <c>notify.syncState</c>).
/// </summary>
/// <remarks>
/// A state without <c>failedOutbox</c> (a daemon from before the field)
/// reads as 0, as Swift's own decoding does; everything else is required as
/// Swift's synthesized decoding requires it. The encoding always writes
/// <c>failedOutbox</c>.
/// </remarks>
public sealed record SyncState
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The coarse state.</summary>
    [JsonPropertyName("status")]
    public required SyncStatus Status { get; init; }

    /// <summary>Set while a specific folder is being synchronised.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init; }

    /// <summary>0–100 within the current pass, -1 otherwise.</summary>
    [JsonPropertyName("progress")]
    [JsonRequired]
    public int Progress { get; init; } = -1;

    /// <summary>End of the last successful pass; null before the first.</summary>
    [JsonPropertyName("lastSync")]
    public DateTimeOffset? LastSync { get; init; }

    /// <summary>The last failure, cleared by the next successful pass.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }

    /// <summary>Outbox messages in state queued or sending.</summary>
    [JsonPropertyName("pendingOutbox")]
    [JsonRequired]
    public int PendingOutbox { get; init; }

    /// <summary>
    /// Outbox messages in state failed: delivery gave up and they wait for
    /// outbox.retry or a delete. They never count in <see cref="PendingOutbox"/>.
    /// </summary>
    [JsonPropertyName("failedOutbox")]
    public int FailedOutbox { get; init; }
}

/// <summary>api.SyncStatusParams. <see cref="AccountId"/> null = every account.</summary>
public sealed record SyncStatusParams
{
    /// <summary>The account; null for every one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }
}

/// <summary>api.SyncStatusResult: in <c>account.list</c> order, paused accounts included.</summary>
public sealed record SyncStatusResult
{
    /// <summary>One state per account.</summary>
    [JsonPropertyName("accounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<SyncState>))]
    public IReadOnlyList<SyncState> Accounts { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.SyncTriggerParams. <see cref="AccountId"/> null = all,
/// <see cref="FolderId"/> null = the whole account, <see cref="Full"/> forces
/// a complete pass instead of an incremental one.
/// </summary>
public sealed record SyncTriggerParams
{
    /// <summary>The account; null for every one.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The folder; null for the whole account.</summary>
    [JsonPropertyName("folderId")]
    public FolderId? FolderId { get; init; }

    /// <summary>A complete pass.</summary>
    [JsonPropertyName("full")]
    public bool? Full { get; init; }
}
